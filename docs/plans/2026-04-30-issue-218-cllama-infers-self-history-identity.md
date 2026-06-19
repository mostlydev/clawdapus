# Issue #218 — cllama infers identity from bearer for self-history

**Status:** Design draft. Workflow: Claude drafts, Codex builds, Claude reviews and releases.
**Parent:** #213 / PR #217 (already merged in v0.14.5).
**Sources of truth checked:** `cllama/internal/proxy/history.go`, `cllama/internal/proxy/handler.go` (chat path bearer parsing at lines 209-229), `cllama/cmd/cllama/main.go:138`, `cllama/internal/identity/identity.go`, `cllama/cmd/cllama/main_test.go:248-339`, `internal/driver/shared/clawdapus_md.go` (LLM Proxy + Self-history block), `internal/pod/compose_emit.go` (per-ordinal env projection, lines ~274-295).

## TL;DR

Add a sibling route `GET /history` in cllama that authenticates via `Bearer <agent-id>:<secret>` and returns the calling agent's own retained turns — no path param. Then drop the `<your-agent-id>` placeholder from generated `CLAWDAPUS.md` and stop projecting `CLAW_AGENT_ID`. Existing `GET /history/{agentID}` route stays for cross-agent reads (memory-replay scope, admin scope).

## Design

### Phase 1 — cllama (submodule)

**New handler in `cllama/internal/proxy/history.go`:**

```go
// HandleSelfHistory serves the calling agent's own retained turns.
// Identity is inferred from the bearer; service-auth and admin tokens are
// rejected because they cannot identify a self principal.
func (h *Handler) HandleSelfHistory(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodGet { ... 405 ... }
    if h.sessionRecorder == nil || h.sessionRecorder.BaseDir() == "" { ... 404 ... }

    auth := r.Header.Get("Authorization")
    if strings.TrimSpace(auth) == "" {
        if x := strings.TrimSpace(r.Header.Get("x-api-key")); x != "" {
            auth = "Bearer " + x
        }
    }
    agentID, secret, err := identity.ParseBearer(auth)
    if err != nil { ... 401 "invalid bearer token" ... }

    targetCtx, err := h.loadContext(agentID)
    if err != nil { ... 403 "agent context not found" ... }
    if err := validateSecret(targetCtx, agentID, secret); err != nil {
        ... 403 "invalid agent secret" ...
    }

    // From here on, identical to HandleHistory: parseAfterParam,
    // parseHistoryLimit, sessionhistory.ReadEntries, ndjson encode.
}
```

Notes on the handler shape:

- We deliberately reuse `identity.ParseBearer` + `validateSecret` (same primitives the chat path uses) rather than the metadata-token equality check that `HandleHistory` does. That's the whole point of #218: admin/service-auth tokens cannot identify a self principal, so they must be rejected by construction. `ParseBearer` enforces the `<agent-id>:<secret>` shape, which admin tokens (`ui-secret`) and `cllama-history` service tokens do not have.
- `extractPresentedToken` (existing helper) strips the `Bearer ` prefix, but `ParseBearer` wants it back. The cleanest path is to read `r.Header.Get("Authorization")` (with `x-api-key` fallback) directly — same pattern the chat path uses at `handler.go:209-219`. Don't introduce a new shared helper just for this.
- 401 vs 403 split: missing/malformed bearer → 401; valid shape but unknown agent or wrong secret → 403. Matches the chat path's behavior (`handler.go:217, 223, 227`).
- Pagination (`?after=`, `?limit=`) and ndjson response shape are byte-for-byte identical to `HandleHistory`. Codex should factor the shared "load + validate + emit" tail into a small helper if duplication grows ugly, but a 6-line copy is also acceptable.

**Mux registration in `cllama/cmd/cllama/main.go:138`:**

```go
mux.HandleFunc("GET /history/{agentID}", proxyHandler.HandleHistory)
mux.HandleFunc("GET /history",           proxyHandler.HandleSelfHistory)
```

The Go 1.22 `http.ServeMux` treats `GET /history` and `GET /history/{agentID}` as distinct patterns; no precedence ambiguity.

**Tests in `cllama/cmd/cllama/main_test.go` (mux-integration style, matching the existing `TestAPIHistoryEndpoint*` tests):**

Add `TestAPIHistoryEndpointSelfRouteAllowsAgentBearer`:

1. Seed an agent context with `metadata.json = {"token":"tiverton:dummy123"}` and a recorded entry.
2. `GET /history` with `Authorization: Bearer tiverton:dummy123` → 200, ndjson with the entry.
3. `GET /history?limit=1&after=...` → 200, single line.
4. `GET /history` with `Authorization: Bearer wrong:secret` → 403.
5. `GET /history` with `Authorization: Bearer history-token` (service-auth from sibling test) → 401 (unparseable: no colon-form).
6. `GET /history` with `Authorization: Bearer ui-secret` (admin token) → 401 (no colon-form).
7. `GET /history` with no Authorization header → 401.
8. (Optional) `GET /history` when `sessionRecorder == nil` → 404 "history not configured".

Keep the existing `/history/{agentID}` tests untouched — that route's contract is unchanged.

**Release artifacts (cllama submodule):**

- Tag `v0.6.3` (current is `v0.6.2`).
- GitHub release on `mostlydev/cllama` with notes: "Add `GET /history` self-history route. Authenticates via metadata bearer, infers agent identity. Rejects admin/service tokens. Existing `/history/{agentID}` route unchanged."
- Multi-arch image push: `docker buildx build --platform linux/amd64,linux/arm64 -t ghcr.io/mostlydev/cllama:latest -t ghcr.io/mostlydev/cllama:v0.6.3 --push cllama/`.

### Phase 2 — clawdapus repo (depends on Phase 1 release)

**`internal/driver/shared/clawdapus_md.go`** — current self-history section reads:

```
- **Endpoint:** `GET http://<proxy>:8080/history/<your-agent-id>`
```

Change to:

```
- **Endpoint:** `GET http://<proxy>:8080/history`
```

Auth ("pre-wired by Clawdapus"), pagination, and response shape lines stay unchanged. Drop the surrounding "Self-scoped: returns only your own history" sentence's reference to agent IDs if it reads awkwardly without the placeholder; otherwise keep it (the self-scoped framing is still accurate).

**`internal/pod/compose_emit.go`** — current per-ordinal block (around lines 274-295):

```go
env["CLLAMA_TOKEN"] = selectedToken
if len(svc.Claw.Cllama) > 0 {
    env["CLAW_SELF_HISTORY_URL"]   = fmt.Sprintf("http://%s:8080/history", cllama.ProxyServiceName(svc.Claw.Cllama[0]))
    env["CLAW_SELF_HISTORY_TOKEN"] = selectedToken
    env["CLAW_AGENT_ID"]           = serviceName  // ← drop this line
}
```

`CLAW_SELF_HISTORY_URL` is now the full callable endpoint (no `/<id>` suffix needed). `CLAW_SELF_HISTORY_TOKEN` stays for shell one-liners. `CLAW_AGENT_ID` is removed — agents that *want* their ID can still introspect their own bearer, but we shouldn't project it as a first-class env name when nothing in the documented surface needs it.

**Tests:**

- `internal/driver/shared/clawdapus_md_test.go`: existing self-history-section test must assert
  (a) `## Self-history introspection` header present,
  (b) endpoint string contains `:8080/history` and does **not** contain `<your-agent-id>` or `/<` placeholder syntax.
- `internal/pod/compose_emit_test.go`: existing per-ordinal env test must assert `CLAW_AGENT_ID` is **not** in the env block; `CLAW_SELF_HISTORY_URL` ends in `/history` (no agent-ID segment).

**Release-coordination commits in this PR:**

- `cllama/` submodule pointer bump to the v0.6.3 commit.
- `DefaultCllamaTag = "v0.6.3"` in `internal/infraimages/release_manifest.go`.

These are normally release-prep changes per AGENTS.md, but here they are load-bearing for the doc/env changes — the new CLAWDAPUS.md text claims a route that only exists in cllama v0.6.3+. Codex should leave a note in the PR body:

> Submodule pointer + `DefaultCllamaTag` move with this change because the
> CLAWDAPUS.md edit references a route that only exists in cllama v0.6.3.
> Maintainer should cut a clawdapus release shortly after merge so the pinned
> tag is reachable.

### Phase 3 — clawdapus release (maintainer)

Operator runs `/clawdapus-release` after Phase 2 merges. The release verifier (`scripts/check-release-infra-tags`) will check `ghcr.io/mostlydev/cllama:v0.6.3`, which Phase 1 will have published.

## Files touched

**cllama submodule (Phase 1 PR):**

- `cllama/internal/proxy/history.go` — add `HandleSelfHistory`.
- `cllama/cmd/cllama/main.go:138` — register the new route.
- `cllama/cmd/cllama/main_test.go` — new `TestAPIHistoryEndpointSelfRouteAllowsAgentBearer`.

**clawdapus repo (Phase 2 PR):**

- `internal/driver/shared/clawdapus_md.go` — drop `<your-agent-id>` placeholder.
- `internal/driver/shared/clawdapus_md_test.go` — update assertions.
- `internal/pod/compose_emit.go` — drop `CLAW_AGENT_ID` projection; `CLAW_SELF_HISTORY_URL` no-id form.
- `internal/pod/compose_emit_test.go` — update assertions.
- `cllama` (submodule pointer) — bump to v0.6.3.
- `internal/infraimages/release_manifest.go` — `DefaultCllamaTag = "v0.6.3"`.
- `site/changelog.md` — add an `## Unreleased` bullet describing the change.

## Test plan

**Unit (Phase 1):** `go test ./internal/proxy/... ./cmd/cllama/...` inside the cllama submodule.

**Unit (Phase 2):** `go test ./internal/driver/shared/... ./internal/pod/...`.

**Manual smoke (post-release):**

```sh
claw compose exec <cllama-enabled-svc> sh -c 'curl -sS \
  -H "Authorization: Bearer $CLLAMA_TOKEN" \
  "$CLAW_SELF_HISTORY_URL?limit=5"'
```

Expected: 200 + ndjson lines (or empty if no completions yet). 401/403 is the failure case.

**Spike:** `TestSpikeRollCall` should remain green — this change adds a route and removes a placeholder; it does not touch wiring, telemetry, or completion paths.

## Open questions for codex

1. **Bearer parsing helper.** Do you want a small shared helper in `cllama/internal/proxy/handler.go` that reads `Authorization` + `x-api-key` and calls `identity.ParseBearer`, or is the 6-line inline form fine in `HandleSelfHistory`? My vote: inline. Two callers (chat + self-history) doesn't justify an indirection yet.

2. **Status code for malformed admin/service tokens.** The plan calls these 401 (unparseable bearer) rather than 403 (forbidden). Rationale: the bearer literally fails the `<agent-id>:<secret>` shape check, which is an authentication error, not an authorization error. Confirm this matches your read of HTTP semantics before implementing.

3. **Test file location.** I suggested adding to `cllama/cmd/cllama/main_test.go` to match the existing mux-integration pattern. Alternative: a new `cllama/internal/proxy/history_test.go` with a stripped-down handler-level test. Either works — pick the one that lets you reuse the most fixture setup.

4. **Submodule + `DefaultCllamaTag` bump in the same PR as the doc/env changes** (vs splitting them out). My recommendation: same PR. Splitting creates a window where the doc PR is on master without a matching cllama tag, which the next release verifier would catch but at the cost of confusion. Note the constraint in the PR body, let the maintainer release.

5. **Should we keep `CLAW_SELF_HISTORY_URL` and `CLAW_SELF_HISTORY_TOKEN`?** The issue body says yes; my plan keeps them. They're cheap, they make `curl` one-liners easy in shell-capable runners, and removing them would be a third churn after #213's introduction. If you (codex) feel strongly about removing them — argue it in the talking-stick note before implementing.

## Non-goals

- **MCP-mediated `cllama.read_self_history` tool.** Cleaner UX (no URL, no auth ceremony) but materially larger; aligns with #177/#179. Out of scope per issue body.
- **`/history/{agentID}` deprecation.** Stays for memory-replay/admin reads. Different authorization model.
- **Removing `CLLAMA_TOKEN`.** Untouched — it's still the bearer for chat completions, which is the same value used for self-history reads now.

## Workflow next step

Pass the stick to codex with `next_action`: "Codex implements Phase 1 in the `cllama/` submodule (handler + mux + tests), runs unit tests, and pauses before tagging/releasing the cllama image. Claude reviews the implementation, then operator decides on the release timing for cllama v0.6.3 before Phase 2 work begins."
