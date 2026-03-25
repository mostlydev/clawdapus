# Provider Token Pool

**Status:** Draft
**Date:** 2026-03-23

## Problem

`cllama` currently routes each provider through one API key. If that key is revoked, quota-exhausted, or temporarily rate-limited, every cllama-backed claw in the pod fails.

The first draft of this plan got three core things wrong:

- it stored runtime-mutated auth state under `.claw-runtime`, which `claw up` deletes
- it tried to use `clawdash` as a secret write plane, but `clawdash` is read-only today
- it split provider state across env, `providers.json`, and a new `token-pool.json`

This revision keeps one runtime authority, one existing UI, and one persistent host path.

## Decisions

1. Runtime-mutable provider state lives at [`.claw-auth/providers.json`](/Users/wojtek/dev/ai/clawdapus/.claw-auth/providers.json), mounted into the proxy as `/claw/auth/providers.json`. Nothing pool-related lives under `.claw-runtime` or `.claw-governance`.
2. `providers.json` is the only runtime authority for provider keys. `x-claw.cllama-env` is a compile-time seed input to `claw up`, not a second runtime source of truth. `claw up` stops injecting provider keys into the proxy service environment, and `cllama` must not let `LoadFromEnv()` override a provider already present in `providers.json`.
3. Key management UI lives in `cllama`’s existing built-in UI at [`cllama/internal/ui/handler.go`](/Users/wojtek/dev/ai/clawdapus/cllama/internal/ui/handler.go) and [`cllama/internal/ui/templates/dashboard.html`](/Users/wojtek/dev/ai/clawdapus/cllama/internal/ui/templates/dashboard.html). Because that dashboard port is host-published, the same UI server gets simple bearer auth via `CLLAMA_UI_TOKEN`, injected by `claw up`. No clawdash mutations. No new admin port.
4. Rate-limit failover uses temporary cooldown, not permanent failure. Only auth/quota/billing failures burn a key.
5. Pool transition events are durable stdout JSON first. Webhooks are best-effort notification layered on top.
6. Alert config is pod-scoped only.

## Canonical Provider State

`providers.json` grows from “one provider, one api_key” into a single canonical provider-state document:

```json
{
  "version": 2,
  "providers": {
    "openai": {
      "base_url": "https://api.openai.com/v1",
      "auth": "bearer",
      "api_format": "openai",
      "active_key_id": "seed:OPENAI_API_KEY",
      "keys": [
        {
          "id": "seed:OPENAI_API_KEY",
          "label": "primary",
          "secret": "sk-...",
          "source": "seed",
          "state": "ready",
          "cooldown_until": "",
          "last_error_code": 0,
          "last_error_reason": "",
          "last_error_at": "",
          "added_at": "2026-03-23T12:00:00Z"
        },
        {
          "id": "runtime:3b5f8d6c",
          "label": "backup-1",
          "secret": "sk-...",
          "source": "runtime",
          "state": "ready",
          "cooldown_until": "",
          "last_error_code": 0,
          "last_error_reason": "",
          "last_error_at": "",
          "added_at": "2026-03-23T14:12:09Z"
        }
      ]
    }
  }
}
```

State meanings:

- `ready`: eligible for selection
- `cooldown`: temporarily skipped until `cooldown_until`
- `dead`: permanently removed from rotation until operator replaces or deletes it
- `disabled`: operator-disabled

`failed` goes away. It is too coarse for rate limits.

Compatibility:

- `cllama/internal/provider/provider.go` must still accept the current v1 `api_key` shape on load
- v1 loads are normalized to one `keys[]` entry in memory
- `SaveToFile()` writes only the v2 shape

## Pod Contract

Provider seeds stay in `x-claw.cllama-env`, including indexed backups:

```yaml
x-claw:
  pod: trading-desk
  alert-webhooks:
    - "${DISCORD_INFRA_WEBHOOK}"
  alert-mentions:
    - "@wojtek"
    - "@infra"
  cllama-defaults:
    proxy: [passthrough]
    env:
      OPENAI_API_KEY: "${OPENAI_API_KEY_PRIMARY}"
      OPENAI_API_KEY_1: "${OPENAI_API_KEY_BACKUP}"
      OPENAI_BASE_URL: "${OPENAI_BASE_URL}"
```

Rules:

- `alert-webhooks` and `alert-mentions` are pod-level `x-claw` fields only
- services cannot override them
- if a service-level raw `x-claw` block contains either key, `claw up` fails hard
- provider keys still never appear in agent `environment:`, `env_file`, or image-baked env

Because [`rawClawBlock`](/Users/wojtek/dev/ai/clawdapus/internal/pod/parser.go) does not define `alert-webhooks` or `alert-mentions` today, that rejection must happen against the raw service `x-claw` map during `expandPodDefaults()` or an equivalent pre-decode validation step in `Parse()`. Typed decode is too late and would silently drop the fields.

## Compile-Time Flow (`claw up`)

1. [`internal/pod/parser.go`](/Users/wojtek/dev/ai/clawdapus/internal/pod/parser.go) keeps using `applyRawCllamaDefaults()` to expand `cllama-defaults.env` into each service’s `rawClawBlock`, which ends up in `ClawBlock.CllamaEnv`.
2. [`rawPodClaw`](/Users/wojtek/dev/ai/clawdapus/internal/pod/parser.go) and [`Pod`](/Users/wojtek/dev/ai/clawdapus/internal/pod/types.go) gain `AlertWebhooks []string` and `AlertMentions []string`.
3. [`cmd/claw/compose_up.go`](/Users/wojtek/dev/ai/clawdapus/cmd/claw/compose_up.go) still wipes `.claw-runtime`, but writes proxy auth state under `filepath.Join(podDir, ".claw-auth")`, not under `.claw-runtime` and not in the claw-api governance mount.
4. `runComposeUp()` loads any existing persistent `providers.json`, compiles the current `ClawBlock.CllamaEnv` seeds, and merges them into the canonical file:
   - seed entries get deterministic IDs like `seed:OPENAI_API_KEY` and `seed:OPENAI_API_KEY_1`
   - duplicate inherited seeds across services are deduped
   - conflicting provider config for the same provider across services is a hard error
   - existing `source:"runtime"` keys are preserved
   - existing seed entries keep their runtime state only when both ID and secret are unchanged
   - removed seed vars remove their matching `source:"seed"` entries
5. Generated proxy env stops carrying provider secrets. It carries only non-provider `cllama-env` values, `CLAW_POD`, alert env vars, and `CLLAMA_UI_TOKEN`.
6. [`cllama/cmd/cllama/main.go`](/Users/wojtek/dev/ai/clawdapus/cllama/cmd/cllama/main.go) must stop calling `LoadFromEnv()` as an unconditional override for provider keys. Env fallback is allowed only for providers missing from `providers.json`; file-backed providers remain authoritative.

This preserves compile-time wiring while allowing runtime key additions to survive redeploys.

## Credential Starvation Checks

[`isProviderKey()`](/Users/wojtek/dev/ai/clawdapus/cmd/claw/compose_up.go) must match indexed backups:

- `OPENAI_API_KEY`
- `OPENAI_API_KEY_1`
- `OPENAI_API_KEY_2`
- `ANTHROPIC_API_KEY`
- `ANTHROPIC_API_KEY_1`
- `OPENROUTER_API_KEY`
- `OPENROUTER_API_KEY_1`

That widened check must be used everywhere current starvation validation already runs:

- service `environment:`
- `env_file`
- image-baked env from `inspectImageEnv()`
- `stripLLMKeys()`

Only `x-claw.cllama-env` is allowed to carry provider keys.

## Runtime Selection and Failover (`cllama`)

[`cllama/internal/provider/provider.go`](/Users/wojtek/dev/ai/clawdapus/cllama/internal/provider/provider.go) becomes the pool state machine, not just a string registry.

Add registry operations along these lines:

- `SelectKey(provider string) (Provider, KeyLease, error)`
- `MarkCooldown(provider, keyID, reason string, until time.Time) error`
- `MarkDead(provider, keyID, reason string, statusCode int) error`
- `ActivateKey(provider, keyID string) error`
- `AddRuntimeKey(provider, label, secret string) error`
- `DeleteKey(provider, keyID string) error`

Selection rules:

- prefer `active_key_id` if that key is `ready`
- otherwise choose the next `ready` key in list order
- `cooldown` keys automatically return to `ready` once `cooldown_until` has passed
- request-level selection uses a short in-memory lease so concurrent retries do not stampede the same candidate

Classification rules in [`cllama/internal/proxy/handler.go`](/Users/wojtek/dev/ai/clawdapus/cllama/internal/proxy/handler.go):

- `401`, `403`, `402`: mark key `dead`, persist, retry next key
- `429` with rate-limit semantics: mark key `cooldown`, persist, retry next key
- `429` with `insufficient_quota`: mark key `dead`, persist, retry next key
- `5xx` and transport failures: retry request, no key state change

If every key is cooling down, wait until the earliest `cooldown_until`, bounded by `CLLAMA_RATE_LIMIT_BACKOFF_MAX`. Return `503` only when no usable key becomes available in that budget. Agents should not see a raw `429`.

## UI and Persistence

The write plane stays on the existing `cllama` UI server started by [`cllama/cmd/cllama/main.go`](/Users/wojtek/dev/ai/clawdapus/cllama/cmd/cllama/main.go). No new `internal/admin/`, no `CLLAMA_ADMIN_PORT`, no clawdash forms.

Because the dashboard port remains published to the host, all UI routes for this feature use a simple bearer check: `Authorization: Bearer $CLLAMA_UI_TOKEN`. The same token gates both the HTML dashboard and key-management POST routes.

UI changes in [`cllama/internal/ui/handler.go`](/Users/wojtek/dev/ai/clawdapus/cllama/internal/ui/handler.go):

- require bearer auth on UI routes using `CLLAMA_UI_TOKEN`
- add pool status to the existing provider table: active key, ready/cooldown/dead counts, last failure
- add POST handlers on the same server for add key, activate key, disable key, delete key
- keep provider base URL/auth/api format read-only in this feature; runtime UI manages keys only

Persistence rules:

- `Registry.SaveToFile()` writes back to `/claw/auth/providers.json`
- writes must be atomic (`tmp file + rename`)
- the host auth directory must be created with `0o777`, same reason runtime dirs use `0o777`: the container UID is not guaranteed to match the host UID

## Alerts and Audit

Pool transitions emit structured stdout first via [`cllama/internal/logging/logger.go`](/Users/wojtek/dev/ai/clawdapus/cllama/internal/logging/logger.go), for example:

```json
{
  "ts": "2026-03-23T14:31:08Z",
  "type": "provider_pool",
  "provider": "openai",
  "key_id": "seed:OPENAI_API_KEY",
  "action": "cooldown",
  "reason": "rate_limit",
  "cooldown_until": "2026-03-23T14:31:18Z",
  "intervention": null
}
```

Webhook delivery happens after the state change is logged and persisted. Webhook failure must never block request failover.

`CLLAMA_ALERT_WEBHOOKS` and `CLLAMA_ALERT_MENTIONS` are injected once per pod from `Pod.AlertWebhooks` and `Pod.AlertMentions`.

## Implementation Map

Clawdapus:

- [`internal/pod/types.go`](/Users/wojtek/dev/ai/clawdapus/internal/pod/types.go): add pod-scoped alert fields
- [`internal/pod/parser.go`](/Users/wojtek/dev/ai/clawdapus/internal/pod/parser.go): parse pod alerts, reject service-level alert keys from the raw service `x-claw` map in `expandPodDefaults()` or a new pre-decode validation step in `Parse()`, keep `applyRawCllamaDefaults()` focused on `proxy` and `env`
- [`cmd/claw/compose_up.go`](/Users/wojtek/dev/ai/clawdapus/cmd/claw/compose_up.go): broaden provider-key detection, compile/merge provider seeds, move proxy auth dir to `.claw-auth`, inject only non-secret proxy env plus `CLLAMA_UI_TOKEN`
- [`internal/audit/event.go`](/Users/wojtek/dev/ai/clawdapus/internal/audit/event.go) and [`internal/audit/query.go`](/Users/wojtek/dev/ai/clawdapus/internal/audit/query.go): add `provider_pool` event-type support
- [`cmd/claw/audit.go`](/Users/wojtek/dev/ai/clawdapus/cmd/claw/audit.go): allow `--type provider_pool`

cllama:

- [`cllama/cmd/cllama/main.go`](/Users/wojtek/dev/ai/clawdapus/cllama/cmd/cllama/main.go): stop env override of file-backed provider keys; only use env as fallback when `providers.json` has no entry for that provider, and wire `CLLAMA_UI_TOKEN` into UI auth
- [`cllama/internal/alert/webhook.go`](/Users/wojtek/dev/ai/clawdapus/cllama/internal/alert/webhook.go): best-effort webhook delivery for provider-pool events
- [`cllama/internal/provider/provider.go`](/Users/wojtek/dev/ai/clawdapus/cllama/internal/provider/provider.go): replace single-key model with pooled canonical state and atomic save
- [`cllama/internal/proxy/handler.go`](/Users/wojtek/dev/ai/clawdapus/cllama/internal/proxy/handler.go): classify upstream failures and retry via pool transitions
- [`cllama/internal/ui/handler.go`](/Users/wojtek/dev/ai/clawdapus/cllama/internal/ui/handler.go): require bearer auth and add key-management POST handlers on the existing UI
- [`cllama/internal/ui/templates/dashboard.html`](/Users/wojtek/dev/ai/clawdapus/cllama/internal/ui/templates/dashboard.html): render pool status and forms
- [`cllama/internal/logging/logger.go`](/Users/wojtek/dev/ai/clawdapus/cllama/internal/logging/logger.go): add provider pool event fields/helpers

Tests:

- [`cmd/claw/compose_up_test.go`](/Users/wojtek/dev/ai/clawdapus/cmd/claw/compose_up_test.go): indexed-key starvation checks and `.claw-auth` path expectations
- [`cllama/internal/provider/provider_test.go`](/Users/wojtek/dev/ai/clawdapus/cllama/internal/provider/provider_test.go): pool state machine and v2 `providers.json` coverage
- [`cllama/internal/proxy/handler_test.go`](/Users/wojtek/dev/ai/clawdapus/cllama/internal/proxy/handler_test.go): failure classification and retry behavior
- [`cllama/internal/ui/handler_test.go`](/Users/wojtek/dev/ai/clawdapus/cllama/internal/ui/handler_test.go): write-route auth coverage

## Out of Scope

- clawdash write mutations
- a second cllama admin server or admin port
- runtime creation of entirely new providers not present in compiled pod config
- OAuth or short-lived credential refresh flows
