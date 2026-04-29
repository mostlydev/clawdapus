# Vercel AI Gateway as a cllama provider

Status: Draft by Claude. Pending codex design challenge before implementation.

## Goal

Let users route cllama-managed LLM traffic through Vercel AI Gateway by setting one env var (`AI_GATEWAY_API_KEY`) and using `vercel/<provider>/<model>` model identifiers in pod YAML.

## Why

- One key, many models: Vercel AI Gateway exposes hundreds of upstream models behind a single OpenAI-compatible endpoint with budgets, fallbacks, and observability. That's a strict superset of what the current single-provider seed flow gives users.
- BYOK / no-markup billing: Vercel doesn't add token markup, so this is a cleaner economic story than OpenRouter for many users.
- Aligns with the cllama "governance proxy" thesis: an upstream gateway with observability complements cllama, it doesn't replace it. cllama still enforces our policy layer (model allow-lists, key cooldown, cost telemetry, session history) on top.

## Verified upstream facts (https://vercel.com/docs/ai-gateway, fetched 2026-04-29)

- **OpenAI-compatible endpoint:** `https://ai-gateway.vercel.sh/v1` (path `/v1/chat/completions`).
- **Anthropic-compatible endpoint:** `https://ai-gateway.vercel.sh` (path `/v1/messages`). Same key.
- **Auth:** `Authorization: Bearer $AI_GATEWAY_API_KEY` for OpenAI path. `x-api-key: $AI_GATEWAY_API_KEY` *or* `Authorization: Bearer` for Anthropic path. Static API key only — OIDC out of scope.
- **Model namespace:** `<provider>/<model>` (e.g. `anthropic/claude-opus-4.6`, `xai/grok-4.1-fast-non-reasoning`, `openai/gpt-5.4`).
- **Env var:** `AI_GATEWAY_API_KEY` is the official name in Vercel's docs.

## Scope (Phase 1 — this PR)

OpenAI-format only. The Anthropic-format dispatch path stays untouched.

### 1. cllama provider table (`cllama/internal/provider/provider.go`)

Four edits:

**`knownProviders`**
```go
"vercel": "https://ai-gateway.vercel.sh/v1",
```

**`envKeyMap`** — for diagnostic listing parity with other providers:
```go
"AI_GATEWAY_API_KEY":   "vercel",
"AI_GATEWAY_API_KEY_1": "vercel",
"AI_GATEWAY_API_KEY_2": "vercel",
```

**`envBaseURLMap`** — let operators override the base URL (Vercel does have regional and BYOK preview endpoints):
```go
"AI_GATEWAY_BASE_URL": "vercel",
```

**`envKeysByProvider`** — pool definition:
```go
"vercel": {
    {"AI_GATEWAY_API_KEY", "seed:AI_GATEWAY_API_KEY", "primary"},
    {"AI_GATEWAY_API_KEY_1", "seed:AI_GATEWAY_API_KEY_1", "backup-1"},
    {"AI_GATEWAY_API_KEY_2", "seed:AI_GATEWAY_API_KEY_2", "backup-2"},
},
```

`defaultAuth` returns `"bearer"` for any unknown name → correct for vercel. `defaultAPIFormat` returns `"openai"` for any non-anthropic name → correct. No changes needed in those helpers.

### 2. Model identifier convention

Agents call models as `vercel/anthropic/claude-opus-4.6` (or `vercel/openai/gpt-5.4`, etc.).

`splitModel()` in `cllama/internal/proxy/handler.go` uses `strings.Cut(model, "/")` — first `/` only — so it splits cleanly:
- `providerName = "vercel"`
- `upstreamModel = "anthropic/claude-opus-4.6"`

The upstream model string is then forwarded verbatim in the `model` field of the OpenAI request body, which is exactly what Vercel expects.

### 3. Tests (`cllama/internal/provider/provider_test.go`)

Add to the existing env-loading test pattern:

- **TestLoadFromEnvSeedsVercelProvider**: `t.Setenv("AI_GATEWAY_API_KEY", "vck_test")` → `r.Get("vercel")` returns `BaseURL=https://ai-gateway.vercel.sh/v1`, `Auth=bearer`, `APIFormat=openai`, `APIKey=vck_test`.
- **TestLoadFromEnvVercelKeyPool**: with `AI_GATEWAY_API_KEY` + `AI_GATEWAY_API_KEY_1` set, the pool has two ready keys with the expected labels.
- **TestLoadFromEnvAIGatewayBaseURLOverride**: with `AI_GATEWAY_API_KEY` + `AI_GATEWAY_BASE_URL` set, the base URL reflects the override.

These mirror the existing openai/google patterns in `provider_test.go` and need no new test infrastructure.

### 4. Documentation

Two small touches in this PR:

- `cllama/README.md` — add `vercel` row to whatever provider table exists there (verify before editing). One line: name, env var, base URL, format.
- `site/guide/cllama.md` — same addition in the public docs. Mention model identifier convention `vercel/<provider>/<model>` with one example.

No changes to the cllama proxy handler, model policy, or feed code. This is purely a registry entry.

## Out of scope (separate follow-up issue)

**Anthropic-format `/v1/messages` via Vercel.** Currently `anthropicCandidateFromRef` in `cllama/internal/proxy/modelpolicy.go:202` hard-rejects any provider name except literal `"anthropic"`:

```go
if providerName != "anthropic" {
    return dispatchCandidate{}, fmt.Errorf("model policy requires anthropic-compatible provider on /v1/messages")
}
```

Allowing `vercel/anthropic/claude-opus-4.6` on `/v1/messages` requires changing that check to "any provider whose registered `api_format == "anthropic"`" and adding a second `vercel-anthropic` provider entry (different base URL, `x-api-key` auth, `api_format: anthropic`, sharing the same `AI_GATEWAY_API_KEY` env). That's a real change to dispatch semantics — keeping it out of this PR. File as a separate issue once Phase 1 lands.

**OIDC auth.** Vercel deployments can authenticate via OIDC instead of an API key. cllama has no OIDC machinery and Hermes/openclaw runners don't either. Out of scope.

**Cost telemetry pricing.** `cllama/internal/cost/` likely has no Vercel pricing tables. The audit cost columns will be approximate or zero for vercel-routed traffic until that's filled in. Acceptable for Phase 1; explicitly note in the changelog entry.

**`claw.describe` registry of Vercel models.** Vercel's model catalog is dynamic. Not enumerating it.

## Non-goals

- Don't bake any Vercel-specific request shaping into the proxy handler. The handler must remain pure passthrough (per repo gotchas in CLAUDE.md). All Vercel-specific behavior comes from the registry entry; the dispatch path is unchanged.
- Don't add Vercel as a default provider in any seed pod / example. Operators opt in by setting `AI_GATEWAY_API_KEY` in `x-claw.cllama-env`.

## Risks

- **Model identifier mistakes.** Users typing `vercel/claude-opus-4.6` (without the `anthropic/` second segment) will get a 4xx from Vercel saying the model doesn't exist. Mitigated by the docs example and by Vercel's clear error response. cllama doesn't need to validate.
- **Bearer header conflict.** None. cllama already handles `auth: bearer` for openai/xai/openrouter/google; same code path serves vercel.
- **Pool drift.** Same operational profile as any other env-seeded provider — operators rotate `AI_GATEWAY_API_KEY` and `claw down && claw up -d` to reload. No new failure modes.

## Test matrix

| Layer | Test | Expectation |
|-------|------|-------------|
| Unit | `TestLoadFromEnvSeedsVercelProvider` (new) | base URL, auth, format, key |
| Unit | `TestLoadFromEnvVercelKeyPool` (new) | primary + backup-1 in pool |
| Unit | `TestLoadFromEnvAIGatewayBaseURLOverride` (new) | env override beats default |
| Unit | existing provider_test.go | unchanged, still green |
| Vet | `go vet ./...` (in cllama/) | green |
| Manual | curl through cllama with `model: "vercel/anthropic/claude-opus-4.6"` | reaches Vercel; 200 with content. Operator-run, not in CI. |

## Release coordination

cllama is on its own tag cycle (currently `v0.6.1`). The clawdapus side needs:

1. Cut `cllama v0.6.2` (tag in submodule + GitHub release + multi-arch image push to `ghcr.io/mostlydev/cllama:v0.6.2`).
2. Bump submodule pointer in clawdapus root.
3. Bump `DefaultCllamaTag = "v0.6.2"` in `internal/infraimages/release_manifest.go`.
4. Cut a clawdapus patch release (e.g. `v0.14.3`) so the new pin ships to users via the release verifier.

This is the standard "cllama-first" sequence from the `clawdapus-release` skill; nothing unusual here.

## Open questions for codex

1. **Are three env-var slots (`AI_GATEWAY_API_KEY{,_1,_2}`) the right pool depth?** All other providers in the table use either 1+1 (primary + backup-1) or 1+2 (openai pattern). Vercel is more likely to be the single canonical key for many setups, so 1+2 feels generous. Proposal: match openai (1+2). Open to dropping to 1+1 if codex thinks the longer pool encourages bad multi-account behavior.

2. **`AI_GATEWAY_BASE_URL` env name.** Vercel's docs don't define an official env var for the base URL — they always hardcode the URL. We're inventing this for symmetry with `OPENAI_BASE_URL` etc. Should the var be `VERCEL_BASE_URL` (matches the `vercel` provider name) or `AI_GATEWAY_BASE_URL` (matches the `AI_GATEWAY_API_KEY` family)? Proposal: `AI_GATEWAY_BASE_URL` because it groups with the API key visually in env files.

3. **Should we add a default model alias?** Other providers don't. Vercel's catalog changes too fast to be worth pinning a default. Proposal: no.

4. **Documentation placement.** `site/guide/cllama.md` exists but I haven't verified it has a provider list section. If it doesn't, a paragraph in the intro plus an example block is fine. Should we instead create a dedicated `site/guide/cllama-providers.md`? Proposal: minimal addition to the existing page.

5. **Phase 2 scoping.** Open as a follow-up issue immediately after this lands, with a one-line title like "Allow non-`anthropic` providers on cllama `/v1/messages` dispatch" — or wait until someone actually asks for it? Proposal: file as soon as Phase 1 lands so the constraint is visible to anyone reading the code.

## Workflow

- Plan drafted by Claude (this doc).
- Codex: design challenge in a talking-stick note. If consensus, proceed to implementation.
- Codex implements: edits `cllama/internal/provider/provider.go` + `provider_test.go` + a docs paragraph. Commits in cllama submodule on a branch (the cllama repo has its own PR flow).
- Claude: tests, opens cllama PR with `Closes #<issue>`, merges, cuts cllama release, bumps submodule + `DefaultCllamaTag`, cuts clawdapus release.

## Acceptance

- `cd cllama && go test ./internal/provider/...` green with new test cases.
- `cd cllama && go vet ./...` green.
- `cllama/README.md` (or `site/guide/cllama.md`) lists vercel under known providers.
- Operator manual test: `claw up` a pod with `AI_GATEWAY_API_KEY` set in `x-claw.cllama-env` and `model: "vercel/anthropic/claude-opus-4.6"`; confirm a real completion lands.
- cllama released; clawdapus submodule + pin bumped; clawdapus released.
