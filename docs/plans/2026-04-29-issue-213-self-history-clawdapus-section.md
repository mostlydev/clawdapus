# Issue #213 — Surface cllama self-history as a discoverable skill in CLAWDAPUS.md

**Status:** Design draft, awaiting codex challenge before implementation.
**Workflow:** Claude drafts, codex reviews, consensus, codex implements, Claude reviews + releases.
**Sources of truth checked:** `cllama/internal/proxy/history.go`, `internal/cllama/context.go`, `cmd/claw/compose_up.go` (`prepareHistoryReplayRuntime`, `materializeService`), `internal/driver/shared/clawdapus_md.go`, `internal/driver/types.go`.

## TL;DR

Add a "Self-history introspection" section to every cllama-enabled `CLAWDAPUS.md`. To make the curl example actually executable, project a small set of env vars into the agent's runtime container at compile time: `CLAW_SELF_HISTORY_URL`, `CLAW_SELF_HISTORY_TOKEN`, and `CLAW_AGENT_ID`. The section references the env vars (never raw secrets), shows one curl shape, describes the response shape, and is written only for cllama-enabled services.

## Reality check on the issue body

The issue claims:
> the env vars `CLAW_HISTORY_URL` + `CLAW_HISTORY_TOKEN` projected into cllama-enabled runner containers (ADR-021 auth projection)

This is **not accurate** for self-history. In the current code (`cmd/claw/compose_up.go::prepareHistoryReplayRuntime`, lines ~2077–2126):

- `CLAW_HISTORY_URL` and `CLAW_HISTORY_TOKEN` are projected only onto **memory-adapter consumer services** (e.g. a `claw-memory` service subscribed to another agent's history). They point at *another* service's `cllama-history` scope.
- A regular cllama-enabled agent reading **his own** history has no `CLAW_HISTORY_*` env vars projected today.
- What the agent *does* have is `CLLAMA_TOKEN` (his metadata bearer token, format `<agent-id>:<48-hex>`). The cllama history endpoint authorizes self-reads against `targetCtx.MetadataToken()` (`cllama/internal/proxy/history.go::authorizeHistoryRequest`), so the same token already works for self-history. He does not have an explicit env-var pointer to either his agent_id or the cllama base URL.

So #213 is two interlocking gaps:
1. **No discoverability:** nothing in `CLAWDAPUS.md` tells the agent the endpoint exists.
2. **No clean env handle for the curl shape:** even if we documented the endpoint, the example would have to either (a) use a placeholder like `<agent-id>` and a hardcoded `http://cllama:8080/history`, or (b) introduce env vars the agent can interpolate.

This plan addresses both.

## Design

### 1. New env vars projected per cllama-enabled service

Set in `cmd/claw/compose_up.go` during cllama wiring (next to where `CLLAMA_TOKEN` is set today, lines 741–748). For every cllama-enabled service:

| Env var | Value | Per-ordinal? |
|---|---|---|
| `CLAW_SELF_HISTORY_URL` | `http://<first-cllama-proxy>:8080/history` | Same for all ordinals of a service |
| `CLAW_SELF_HISTORY_TOKEN` | The agent's own metadata bearer token (same value already used as `CLLAMA_TOKEN`) | **Yes — per-ordinal** for `count > 1` (each ordinal gets his own bearer) |
| `CLAW_AGENT_ID` | The agent's own ID (`name` for count=1, `name-N` for count>1) | **Yes — per-ordinal** |

Naming rationale:
- `CLAW_SELF_HISTORY_URL` and `CLAW_SELF_HISTORY_TOKEN` deliberately avoid the existing memory-replay env names `CLAW_HISTORY_URL` and `CLAW_HISTORY_TOKEN`, which carry a peer-service replay capability rather than a self-read capability.
- Keeping the self-history names distinct means a service that is *both* a cllama agent *and* a memory-replay consumer doesn't have one env name overloaded with two semantically different roles.
- `CLAW_AGENT_ID` is new but lightweight — it's the only way to write a copy-pasteable curl one-liner, and any future per-agent introspection will want this anyway.

### 2. CLAWDAPUS.md "Self-history introspection" section

Emit only when `len(rc.Cllama) > 0`. Place immediately after the existing "LLM Proxy" section in `internal/driver/shared/clawdapus_md.go`. Suggested copy:

```markdown
## Self-history introspection

You can read your own retained turns through the cllama proxy. This is a self-scoped read — it returns only your own history, not pod-wide or cross-agent.

- **Endpoint env:** `CLAW_SELF_HISTORY_URL` (base URL, e.g. `http://cllama:8080/history`)
- **Auth env:** `CLAW_SELF_HISTORY_TOKEN` (your own bearer; same scope as your LLM calls)
- **Identity env:** `CLAW_AGENT_ID` (your own agent ID)
- **Pagination:** optional `?after=<RFC3339>` and `?limit=<N>` query parameters.
- **Response:** newline-delimited JSON (`application/x-ndjson`); one retained entry per line in chronological order.

Example:

    curl -H "Authorization: Bearer $CLAW_SELF_HISTORY_TOKEN" \
      "$CLAW_SELF_HISTORY_URL/$CLAW_AGENT_ID?limit=20"

Use this when an operator asks you to explain past behavior, when introspecting before a long task, or when reasoning over your own session. Do not poll on a hot path — this is a discovery surface, not a hot loop.
```

The text deliberately:
- references env vars only, never raw values;
- mentions the ndjson response shape so the agent doesn't try to parse a JSON array;
- names the pagination params explicitly because that's what the underlying handler accepts (`parseAfterParam`, `parseHistoryLimit`);
- adds a one-line norm against polling, since this surface will be tempting once it's discoverable.

### 3. No spec/wire changes to cllama itself

The cllama history endpoint and its auth model are unchanged. We're projecting env vars at compile time and adding a markdown section. No new routes, no new tokens, no new auth paths.

## Files touched (proposed)

- `internal/pod/compose_emit.go` — project `CLAW_SELF_HISTORY_URL`, `CLAW_SELF_HISTORY_TOKEN`, `CLAW_AGENT_ID` for every cllama-enabled service next to the existing per-ordinal `CLLAMA_TOKEN` handling.
- `internal/driver/shared/clawdapus_md.go` — emit the new section, gated on `len(rc.Cllama) > 0`. Likely inserted right after the existing `## LLM Proxy` block (line ~136).
- `internal/driver/shared/clawdapus_md_test.go` — two tests: one asserting the section is present for a cllama-enabled `rc`; one asserting it is **absent** when `rc.Cllama` is empty. Use the existing `TestClawdapusMDIncludesProxySection` / `TestClawdapusMDNoProxyWhenNoCllama` pair as the template.
- `cmd/claw/compose_up_test.go` — extend an existing cllama-wiring test (or add a small new one) asserting all three new env vars land on a cllama-enabled service's compose `environment:` block, with the per-ordinal expansion preserved for `count > 1`.

## Test plan

**Unit:**
1. `clawdapus_md_test.go`:
   - cllama-enabled rc → markdown contains `## Self-history introspection`, `CLAW_SELF_HISTORY_URL`, `CLAW_SELF_HISTORY_TOKEN`, `CLAW_AGENT_ID`, and the curl shape.
   - cllama-disabled rc → markdown does **not** contain the section header or any of the three env names.
2. `compose_up_test.go`:
   - count=1 cllama service: env block has all three vars set, `CLAW_SELF_HISTORY_TOKEN` matches `CLLAMA_TOKEN`, `CLAW_AGENT_ID` matches the service name.
   - count=2 cllama service: each ordinal gets its own `CLAW_SELF_HISTORY_TOKEN` and `CLAW_AGENT_ID` (matching `name-0`, `name-1`); `CLAW_SELF_HISTORY_URL` is identical across ordinals.
   - non-cllama service: none of the three vars are set.

**Spike (manual):**
- Run `examples/quickstart/` (cllama-enabled), `claw up -d`, `claw compose exec <svc> sh -c 'curl -sS -H "Authorization: Bearer $CLAW_SELF_HISTORY_TOKEN" "$CLAW_SELF_HISTORY_URL/$CLAW_AGENT_ID?limit=5"'`. Expected: ndjson lines (or empty, if no completions yet — make one completion first to seed). 401/403 is the failure case.
- Cross-check: same curl from a non-cllama-enabled sidecar should fail (no env vars).

**Existing spikes:** `TestSpikeRollCall` should remain green — this change adds env vars and one markdown section but does not touch cllama wiring or telemetry normalization.

## Open questions for codex

1. **Naming collision with the memory-replay path.** Resolved by using `CLAW_SELF_HISTORY_URL` and `CLAW_SELF_HISTORY_TOKEN` for self-history and leaving memory replay on `CLAW_HISTORY_URL` and `CLAW_HISTORY_TOKEN`. This avoids collision without migrating existing memory-adapter consumers.

2. **`CLAW_AGENT_ID` scope.** I'm proposing it only for cllama-enabled services so we don't grow env surface for agents that have nothing to introspect. But once it's wired for cllama, follow-up features (memory adapters, master-claw-driven reasoning, audit prompts) will want it everywhere. Should we just project it universally for all `x-claw` services from day one and let it be unused by non-cllama paths? Marginal cost, future-proof.

3. **URL shape.** Use `http://<proxy>:8080/history` (no trailing slash, no `/v1`). The handler is mounted at `/history/{agentID}`. The curl example concatenation `$CLAW_SELF_HISTORY_URL/$CLAW_AGENT_ID` lands on the right path.

4. **Markdown placement.** I have it right after `## LLM Proxy`. Alternative: under a top-level `## Introspection` umbrella that future surfaces (memory recall API, audit log) can join. Probably premature; one section is enough for v1.

5. **Documentation in `site/guide/`.** Out of scope for this PR? Or should the user-facing site mention the env vars too? My instinct: this PR ships the discoverability for the agent, and we add a `site/guide/` paragraph in a follow-up after we've seen agents actually use it.

## Non-goals

- **No new auth route.** Self-history is already authorized via the metadata token; we don't add new scopes or tokens.
- **No pagination cursor change.** The handler already takes `?after=<RFC3339>` and `?limit=<N>`; we just document them.
- **No agent-self-curation.** The sibling issue covers retain/forget shape. This issue ends at "agent can read."
- **No `site/changelog.md` `Latest` badge bump.** Per repo discipline, that lands with the release tag, not this PR.

## Workflow next step

Pass the stick to codex with `next_action`: "Codex challenges the design (especially the naming-collision question) and either confirms the plan or proposes a revised approach. No code commits yet from anyone."
