# Troubleshooting

Symptom-first. Find what you're seeing, get the diagnosis and the fix. Every entry here maps to a failure mode observed in real deployments.

## Agent turns fail with 502 and `context deadline exceeded` at ~120s

**Cause:** the managed-tool mediation budget. The whole mediated turn — every inference round plus every tool execution — shares one `total_timeout_ms` budget (default 120s). Slow reasoning models exhaust it mid-chain.

**Fix:** raise the budget in pod YAML and redeploy:

```yaml
x-claw:
  tool-policy-defaults:
    total-timeout-ms: 300000
```

```bash
claw up -d
```

See [Managed Tools § Budget limits](/guide/tools#mediation-loop). Check which model is slow with `claw audit` (latency column).

## A feed is missing from agent context / `feed_fetch` errors at ~3s

**Cause:** the feed provider responded slower than the fetch timeout (default 3s). The block is skipped silently from the agent's perspective; only the proxy logs the failure.

**Fix:** raise the fetch timeout via cllama env:

```yaml
x-claw:
  cllama-defaults:
    env:
      CLLAMA_FEED_FETCH_TIMEOUT_MS: "10000"
```

Then fix the slow provider — the timeout knob buys headroom, it doesn't make a synchronous upstream computation fast. Audit trail: `claw audit` shows `feed_fetch` errors and `feed_injection` events with a `skipped (...)` notice.

## Managed tool calls are rejected with `schema_validation` errors

**Cause:** the model emitted arguments that violate the tool's declared `inputSchema` — most commonly a required field at the wrong nesting level. cllama rejects these before the service is called and tells the model exactly what's wrong (`missing required property "x" at top level; found at "ctx.x"`).

**Fix:** this is working as intended — the model corrects itself in-round. If a *valid* call is being rejected, the descriptor's `inputSchema` doesn't match what the service actually accepts: fix the service's `claw.describe` descriptor. Emergency bypass: `CLLAMA_TOOL_SCHEMA_VALIDATION: "off"` in cllama env. Audit trail: `managed_tool_schema_rejected` interventions in `claw audit`; full arguments in the session-history `tool_trace`.

## Managed tool calls repeatedly rejected by the providing service (4xx)

**Cause:** the service validates more strictly than its descriptor declares, so schema validation passes but the provider refuses. The model retries by guessing and burns mediation rounds.

**Fix:** make the descriptor's `inputSchema` declare everything the service enforces (`required`, nesting, enums). The schema is the model's only contract — anything enforced but undeclared turns into guess-and-retry. Read the rejection bodies in `tool_trace` in session history.

## Agent has no provider access / "credential starvation" preflight failures

**Cause:** provider API keys placed in the agent's `environment:` block. Agents must never hold provider keys — cllama does.

**Fix:** move keys to `x-claw.cllama-env` (service level) or `x-claw.cllama-defaults.env` (pod level):

```yaml
    x-claw:
      cllama: passthrough
      cllama-env:
        ANTHROPIC_API_KEY: "${ANTHROPIC_API_KEY}"
```

## Discord bot connected but never replies

**Diagnosis first:**

```bash
claw compose exec <service> cat /root/.hermes/logs/gateway.log   # Hermes
claw logs <service>                                              # any driver
```

Zero gateway entries after startup = connected but not receiving events.

**Causes, in order of likelihood:**
1. **MESSAGE CONTENT intent** not enabled in the Discord developer portal — the bot sees mentions but no message text.
2. Stale gateway session — `claw compose restart <service>`.
3. The bot requires a mention (`mention_only` is set by all drivers to prevent multi-agent loops) and the message didn't mention it.

## Agents mention-looping each other in a multi-agent pod

**Cause:** a driver config that dropped `require_mention`, or a runner replying with an auto-mention (Hermes's reply feature pings the original author unless patched).

**Fix:** Clawdapus sets `mention_only`/`requireMention` and suppresses reply mentions in all built-in drivers — if you see loops, check for a hand-edited runner config overriding the generated one, and confirm your images are built from current runner bases (`claw pull`, then `claw build`).

## `claw ps` / `claw logs` / `claw health` refuse to run

**Symptom:** "pod file is newer than compose.generated.yml".

**Cause:** you edited `claw-pod.yml` after the last compile. The generated compose file is the single source of truth, and stale state is fail-closed.

**Fix:** `claw up -d` to recompile. (`claw down` is exempt — you can always tear down.)

## cllama returns "missing API key for provider" 502s, but the key is configured

**Cause:** a pre-v0.2.2 cllama image silently loading a v2-format `providers.json` with empty key pools, or a stale container running an old image.

**Fix:** the four-verb refresh:

```bash
claw pull
claw up -d        # recreates the proxy container from the pulled image
```

Verify key state live: `curl -N -H "Authorization: Bearer <ui_token>" http://<host>:8181/events` — the initial payload has `providers[name].maskedKey`; an empty string means no active key loaded.

## `claw build` fails closed asking for `claw pull`

**Cause:** the service image's runner base (`openclaw:latest` etc.) has no versioned sibling tag — usually because it was built with a manual `docker build` instead of `claw pull`.

**Fix:** run `claw pull` (refreshes runner aliases properly), then `claw build`.

## `claw audit` quick reference

| Event type | Meaning |
|------------|---------|
| `request` / `response` | A proxied LLM call: agent, model, latency, tokens, cost |
| `error` | Upstream/provider failure (timeouts, 4xx/5xx from the LLM provider) |
| `intervention` | Governance event — see below |
| `feed_fetch` / `feed_injection` | Feed provider fetch results and what was injected into context |
| `tool_call` | Managed tool execution from session-history `tool_trace` |

Intervention reasons you may see:

| Intervention | Meaning |
|--------------|---------|
| `managed_tool_schema_rejected:<tool>` | Tool call rejected pre-dispatch for schema violations |
| `duplicate_managed_tool_call` | Same tool, same args, same turn — skipped, structured error returned |
| `mixed_tool_order_internal_retry` | Model mixed native-first/managed-later tool order; cllama replanned internally |
| `managed_tool_budget_finalization` | Budget exhausted; cllama forced a final text turn instead of returning empty |

`manifest_present` and `tools_count` fields on each audit line confirm whether a compiled tool manifest actually reached cllama for that agent.

## Still stuck?

- `claw doctor` — environment sanity checks
- `claw inspect <service>` — what was actually compiled for a service
- [Open an issue](https://github.com/mostlydev/clawdapus/issues/new/choose) with your `claw doctor` output, driver type, and the relevant pod YAML snippet
