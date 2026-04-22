# Agent Context Visibility for clawdash

**Date:** 2026-04-17
**Status:** Draft — revised after Codex review (v3)

## Problem

Operators have no way to inspect what an agent actually sees at runtime. clawdash shows fleet health, topology, and schedules, but nothing about agent contracts, injected context, feeds, tools, memory, or model policy. Debugging agent behavior requires manually reading files under `.claw-runtime/context/` and guessing what cllama assembled on any given turn.

## Goals

1. **Static contract view** — inspect the compiled artifacts written by `claw up` for any agent: AGENTS.md, CLAWDAPUS.md, metadata, feeds manifest, tools manifest, memory config.
2. **Live assembled context** — see the exact system message, tools array, and memory recall block that cllama delivered to the provider on the most recent turn. This must use the same execution path as real requests so it validates actual output, not a reconstruction.
3. **Single API convention** — all observability queries go through `claw-api`. clawdash talks to one backend.

## Non-goals

- Per-turn history browser (session history already exists in `.claw-session-history/`; a full turn explorer is a separate feature).
- Modifying agent context at runtime (write-plane concern, out of scope).
- Replacing the cllama operator dashboard (it stays for provider key management; context visibility is an operator concern surfaced in clawdash).

---

## Architecture

### Data flow

```
claw up
  │
  ├─ writes .claw-runtime/context/<agent-id>/*   (static artifacts)
  │     mounted read-only into both cllama and claw-api
  │
  └─ cllama (runtime)
        │
        ├─ assembles system message per-turn (feeds + time + tools + memory recall)
        ├─ holds last assembled context per agent in memory  ← NEW
        └─ exposes GET /internal/context/<agent-id>/snapshot ← NEW
              │
              │  (internal network, not operator-facing)
              ▼
        claw-api
          ├─ GET /agents                            ← NEW (index)
          ├─ GET /agents/<id>/contract              ← NEW (static)
          └─ GET /agents/<id>/context               ← NEW (live, proxied from cllama)
              │
              ▼
        clawdash
          └─ /agents page with contract + context views  ← NEW
```

### Why this layering

**claw-api as the single query surface.** clawdash already talks to claw-api for fleet status, logs, metrics, alerts, and schedule. Adding agent context queries to the same surface keeps one auth model, one base URL, one convention. The existing `CLAWDASH_CLLAMA_COSTS_URL` direct-to-cllama path is a legacy shortcut; this design does not add another one.

**cllama holds the live snapshot.** The assembled system message only exists in cllama's process memory during request handling. A file-based or external approach would require reconstruction, violating the "same execution path" principle. cllama captures and retains the snapshot as a side effect of the real assembly, then serves it on an internal endpoint. claw-api proxies it.

**Static artifacts served from disk by claw-api.** The context directory is already mounted into cllama; adding it to claw-api's mounts is a one-line change in `ClawAPIConfig`/`compose_emit.go`. claw-api reads the files directly — no proxying needed for the static view.

---

## Detailed Design

### 1. cllama: capture and serve last assembled context

**What changes:**

In `cllama/internal/proxy/handler.go`, after the full assembly sequence (memory recall → feeds → time → tools → model resolution) but before dispatch to the provider, capture a snapshot of the assembled payload.

**Snapshot struct** (new file `cllama/internal/proxy/snapshot.go`):

```go
type ContextSnapshot struct {
    AgentID        string              `json:"agent_id"`
    CapturedAt     time.Time           `json:"captured_at"`
    Format         string              `json:"format"`           // "openai" or "anthropic"
    System         any                 `json:"system"`           // the full system content — string or []ContentBlock
    Tools          []any               `json:"tools"`            // tool schemas as sent to provider
    RequestedModel string              `json:"requested_model"`  // what the agent asked for
    ChosenRef      string              `json:"chosen_ref"`       // normalized model ref after policy
    Candidates     []CandidateSnapshot `json:"candidates"`       // ordered dispatch candidates
    FeedBlocks     []string            `json:"feed_blocks"`      // individual feed results (for UI breakdown)
    MemoryRecall   string              `json:"memory_recall"`    // formatted memory block, empty if none
    TimeContext    string              `json:"time_context"`     // the injected time line
    Intervention   string              `json:"intervention"`     // model policy intervention, empty if none
    ManagedTool    bool                `json:"managed_tool"`     // true if this was a managed-tool turn
    TurnCount      int                 `json:"turn_count"`       // for managed-tool: how many internal rounds
}

type CandidateSnapshot struct {
    Provider      string `json:"provider"`       // e.g. "anthropic", "openrouter"
    UpstreamModel string `json:"upstream_model"` // e.g. "claude-sonnet-4-20250514"
}
```

**Key type decisions:**

- `System` is `any`, not `string`. Anthropic's system field can be a plain string OR a `[]ContentBlock` array (block-array form preserved by `feeds.InjectAnthropic` in `cllama/internal/feeds/inject.go:128`). Capturing as `any` preserves the actual shape sent to the provider. The clawdash UI renders either form — string as monospace text, block-array as structured blocks.

- `RequestedModel` vs `Candidates`: the agent requests a model name (e.g., `"claude-sonnet"`), but `resolveOpenAIExecution` (`handler.go:217`) produces a `modelResolution` with a `Candidates []dispatchCandidate` list, not a single model. `dispatchCandidates` (`handler.go:322`) iterates through candidates, rewriting `payload["model"]` per attempt, with failover when a provider is exhausted. A pre-dispatch snapshot cannot know which candidate will succeed — only which ones are available. The snapshot therefore captures the full candidate list (`ChosenRef` + provider/model per candidate), not a single "resolved model." The operator sees the resolution policy (what was tried) rather than a potentially misleading single value.

- `ManagedTool` and `TurnCount`: in managed-tool mode (`handleManagedOpenAI` in `toolmediation.go`), the upstream request mutates across internal rounds as tool results are appended. The initial snapshot captures the first-turn context (before any tool rounds). `TurnCount` is updated when the managed-tool loop completes, so the operator knows this was a multi-round interaction. A full replay of intermediate rounds is out of scope — that belongs in session history.

**Storage:** A `sync.Map` keyed by agent ID, holding the most recent `ContextSnapshot`. One entry per agent, overwritten each turn. Memory cost is bounded by agent count (typically <20 per pod).

**Why capture individual components in addition to the full system content:** The `system` field is the source of truth (what the provider actually sees), but operators also need to understand *which parts came from where* — was the feed stale? Did memory recall return anything? Was there an intervention? The breakdown fields enable this without requiring the operator to parse the assembled blob.

**Capture is two-phase:**

**Phase A — after all injection, after model resolution, before dispatch.** This is the primary snapshot point. At this point:
- Memory recall, feeds, time context, and tools are all injected into the payload
- `resolveOpenAIExecution` / `resolveAnthropicExecution` has run, so we have the resolved model and any intervention
- The payload is the exact first-turn request the provider will see

For the **OpenAI flow**, this is after `resolveOpenAIExecution` (`handler.go:217`) returns `resolution`, right before the branch into managed-tool vs simple dispatch (~line 230). The snapshot reads `system` from `payload["messages"]` (first system message), tools from `payload["tools"]`, and model info from `resolution`.

For the **Anthropic flow**, same position — after resolution, before dispatch. System read from `payload["system"]` (preserving string or block-array form).

**Phase B — managed-tool completion (conditional).** If the request enters `handleManagedOpenAI`, the tool mediation loop may run multiple internal rounds. When the loop completes, update the existing snapshot's `TurnCount` field. This is a lightweight mutation (one int write to the sync.Map entry), not a full re-snapshot. The operator sees "this was a 4-round managed-tool turn" without needing the intermediate states.

**Why not snapshot after every managed-tool round?** Each round appends tool results and assistant messages to the payload. Capturing all of them would mean a growing array of snapshots per turn, which is session-history territory. The snapshot's job is "what context did the agent start with" — the first turn captures that.

**New internal endpoint** on the cllama UI handler (`cllama/internal/ui/handler.go`):

```
GET /internal/context/<agent-id>/snapshot
```

Returns the `ContextSnapshot` JSON for the given agent. Returns 404 if no request has been processed for that agent since cllama startup. Auth: bearer check (same `CLLAMA_UI_TOKEN` as existing endpoints).

This endpoint is internal — only claw-api calls it. It is not documented as an operator-facing surface.

**New list endpoint:**

```
GET /internal/context
```

Returns `{"agents": ["agent-0", "agent-1", ...]}` — the set of agents that have at least one captured snapshot. claw-api uses this to annotate the agent index with "has live context" availability.

### 2. claw-api: new agent context endpoints

**New verb:** `agent.context` — added to `AllReadVerbs` in `internal/clawapi/principal.go`. This gates all three new endpoints.

**Justification for a single verb:** The static contract and live context are both read-only observability of the same conceptual data (what an agent knows). Splitting into `agent.contract` and `agent.context` would add principal configuration burden with no meaningful security distinction — if you can see the contract, you can see the live context, and vice versa.

**New config fields** in `ClawAPIConfig` (`internal/pod/compose_emit.go`):

```go
ContextHostDir  string // host path to .claw-runtime/context/ directory
CllamaAPIURL    string // internal URL for cllama snapshot endpoint (e.g. http://cllama-passthrough:8081)
CllamaAPIToken  string // CLLAMA_UI_TOKEN for authenticating against cllama's internal endpoints
```

**Mount:** `.claw-runtime/context/` mounted read-only at `/claw/context` in the claw-api container. Same directory already mounted into cllama — no new generation, just a second consumer.

**Environment:** `CLAW_CONTEXT_ROOT=/claw/context`, `CLAW_CLLAMA_API_URL=http://<cllama-service>:<dashboard-port>`, and `CLAW_CLLAMA_API_TOKEN=<ui-token>` injected into claw-api env by `compose_up.go`.

**cllama auth for claw-api (CRITICAL):** cllama's UI handler enforces bearer auth on every route via `checkBearer()` (`cllama/internal/ui/handler.go:120`). The `CLLAMA_UI_TOKEN` is currently provisioned only into the proxy container env (`compose_up.go:648`). claw-api needs this token to call the snapshot endpoints. `compose_up.go` must pass the same `uiToken` value into `ClawAPIConfig` so `compose_emit.go` can inject it as `CLAW_CLLAMA_API_TOKEN`. claw-api sends it as `Authorization: Bearer <token>` when proxying to cllama. This is the same token, not a new credential — claw-api acts as a trusted internal consumer of cllama's UI surface.

**Endpoints** (in `cmd/claw-api/handler.go`):

#### `GET /agents`

Returns an index of all agents in the pod.

```json
{
  "agents": [
    {
      "claw_id": "analyst-0",
      "service": "analyst",
      "claw_type": "openclaw",
      "has_live_context": true
    }
  ]
}
```

**Implementation:** Scan `/claw/context/` for subdirectories (each is an agent ID). Enrich with service name and claw type from the pod manifest. `has_live_context` is populated by calling cllama's `GET /internal/context` list endpoint (cached with short TTL, e.g. 5s).

**Scope filtering:** The agent list is filtered by the calling principal's scope, consistent with how `handleStatus` and `handleMetrics` work today (`handler.go:496-553`).

**Important: `AllowsClawID` does not check service scope.** `AllowsClawID()` (`principal.go:132`) only checks `AllowsPod()` (needs `Pods` set) and explicit `ClawIDs` match. But `BuildSelfPrincipal()` (`principal.go:166`) only sets `Services`, not `Pods` or `ClawIDs`. A self principal for service `analyst` has `Services: ["analyst"]` but no `Pods` or `ClawIDs` — so `AllowsClawID("pod", "analyst-0")` returns false.

**Fix:** The `/agents` endpoints use a **new scope check function** `allowsAgentContext(principal, podName, clawID, serviceName)` that accepts if ANY of:
- `principal.AllowsPod(podName)` — pod-scoped principals (master, dashboard) see everything
- `principal.AllowsClawID(podName, clawID)` — explicit claw_id scope
- `principal.AllowsService(podName, serviceName)` — service-scoped principals see their service's agents

The handler resolves `serviceName` from the claw_id by reading `metadata.json` (which has the `service` field) or from the pod manifest. This way a self principal for `analyst` (which has `Services: ["analyst"]`) can inspect `analyst-0` and `analyst-1` but not `trader-0`.

For `GET /agents`, the list is filtered using this function per entry. For `GET /agents/<id>/contract` and `GET /agents/<id>/context`, the target is checked before serving — 403 if out of scope.

#### `GET /agents/<claw-id>/contract`

Returns the compiled artifacts for one agent.

```json
{
  "claw_id": "analyst-0",
  "agents_md": "# analyst\n\nYou are a market analyst...",
  "clawdapus_md": "# CLAWDAPUS infrastructure context\n...",
  "metadata": { "pod": "trading-desk", "service": "analyst", ... },
  "feeds": [ { "name": "channel-context", "source": "claw-wall", "ttl": 30, ... } ],
  "tools": { "version": 1, "tools": [...], "policy": {...} },
  "memory": { "service": "mem-service", "base_url": "...", ... }
}
```

**Implementation:** Read files from `/claw/context/<claw-id>/`. AGENTS.md and CLAWDAPUS.md returned as strings. JSON files (metadata, feeds, tools, memory) parsed and inlined. Missing optional files (feeds.json, tools.json, memory.json) returned as `null`.

**Credential redaction (CRITICAL):** Multiple context artifacts carry bearer tokens that must not be exposed:

| File | Field | Source |
|------|-------|--------|
| `metadata.json` | `token` | Agent's cllama bearer secret (`compose_up.go:572`) |
| `feeds.json` | `[].auth` | Feed bearer tokens (`compose_up.go:1269`) |
| `tools.json` | `tools[].execution.auth` | Tool endpoint auth (`ToolExecution.Auth` in `context.go:57`, populated at `compose_up.go:1333`) |
| `memory.json` | `auth` | Memory service auth (`MemoryManifestEntry.Auth` in `context.go:79`, populated at `compose_up.go:1371`) |
| `service-auth/*.json` | `token` | Per-service bearer tokens (`ServiceAuthEntry.Token` in `context.go:33`) |

The contract handler applies a recursive redaction pass before serialization: any JSON key matching `token`, `auth`, or `secret` at any depth is replaced with `"[REDACTED]"` (for strings) or `{"type": "[REDACTED]"}` (for auth objects, preserving the `type` field so operators can see *what kind* of auth is configured without seeing the credential). This is a deny-list — new fields default to visible, and credential fields must be added explicitly when introduced. The redaction function is unit-tested against the actual context file schemas to catch drift.

**Why return everything in one envelope:** The operator will always want the full picture. Splitting into sub-endpoints (`/contract/agents-md`, `/contract/metadata`, etc.) adds round trips for no benefit — these files are small.

#### `GET /agents/<claw-id>/context`

Returns the last assembled turn context from cllama.

**Implementation:** Proxies to cllama's `GET /internal/context/<claw-id>/snapshot`. Returns the `ContextSnapshot` JSON directly. Returns `404` with `{"error": "no context captured yet"}` if the agent hasn't processed a request since cllama startup.

**Why proxy instead of direct access:** Keeps the single-API convention. clawdash never needs to know cllama's address. The proxy is trivial (one HTTP call, pass through response).

### 3. claw up: wiring changes

**`compose_up.go`:**

- Set `ClawAPIConfig.ContextHostDir` to the same `filepath.Join(runtimeDir, "context")` used for cllama proxy config.
- Set `ClawAPIConfig.CllamaAPIURL` to `http://<cllama-compose-service>:<dashboard-port>` (constructed from the proxy config, same pattern as `CLAWDASH_CLLAMA_COSTS_URL`).
- Set `ClawAPIConfig.CllamaAPIToken` to the same `uiToken` generated at line 635. This is the existing `CLLAMA_UI_TOKEN` — claw-api needs it to authenticate against cllama's internal endpoints.
- Change the clawdash API wiring conditional from `p.ClawAPI != nil && hasPodInvokeEntries(p)` to `p.ClawAPI != nil` so clawdash gets credentials on all pods that have claw-api.

**`compose_emit.go`:**

- In the claw-api volume list, add `ContextHostDir → /claw/context:ro`.
- In `clawAPIEnvironment()`, emit `CLAW_CONTEXT_ROOT`, `CLAW_CLLAMA_API_URL`, and `CLAW_CLLAMA_API_TOKEN`.

**Principal generation (`prepareClawAPIRuntime`):**

- Add `agent.context` to the master claw's verb set (master already gets `AllReadVerbs`; adding `agent.context` there is sufficient).
- Add `BuildDashboardPrincipal(podName)` call — always created when `p.ClawAPI != nil`, returns a principal with `AllReadVerbs` + `agent.context`, scoped to the pod. This is the auth identity for clawdash.
- The scheduler principal is unchanged — it keeps its narrow `schedule.read`/`schedule.control` verbs for invoke operations.

### 4. clawdash: agent context UI

**New route:** `GET /agents` → `h.renderAgents(w, r)`

**Fleet page integration:** Add an "Agents" link in the nav bar alongside Fleet, Topology, Schedule.

**Agents index page:** Card grid of agents (same visual pattern as fleet service cards). Each card shows: agent ID, service name, claw type, whether live context is available. Click through to detail.

**Agent detail page:** `GET /agents/<claw-id>`

Two-tab layout:

**Tab 1 — Contract** (static, from `/agents/<id>/contract`):
- AGENTS.md rendered as formatted text (monospace block, or markdown if we add a renderer)
- CLAWDAPUS.md rendered similarly
- Metadata as a key-value table
- Feeds manifest as a table (name, source, TTL, URL)
- Tools manifest as collapsible list (name, description, schema expandable)
- Memory config as key-value pairs

**Tab 2 — Live Context** (from `/agents/<id>/context`):
- "Last captured" timestamp with relative time
- Full system message in a scrollable monospace block (the source of truth)
- Breakdown panel: feed blocks listed individually, memory recall block, time context line, model policy intervention if any
- Tools array as collapsible schemas
- Resolved model name
- "No context yet" state if the agent hasn't made a request

**Refresh:** Manual refresh button on the Live Context tab. No auto-polling — this is a debugging tool, not a monitoring dashboard.

**Data fetching:** clawdash calls claw-api's `/agents` and `/agents/<id>/contract` or `/agents/<id>/context` endpoints using `CLAW_API_URL` + `CLAW_API_TOKEN`.

**Wiring gap (CRITICAL):** Today, `CLAW_API_URL` and `CLAW_API_TOKEN` are only injected into clawdash when the pod has invoke entries (`compose_up.go:695`: `if p.ClawAPI != nil && hasPodInvokeEntries(p)`). The Agents UI needs credentials on *every* pod that has claw-api.

The root problem is deeper than just the conditional: the token lookup calls `lookupClawAPIPrincipalToken(_, "claw-scheduler")`, but the `claw-scheduler` principal is only auto-created when invokes exist (`compose_up.go:1807`). On a master-only pod with no invokes, there is no scheduler principal to look up.

**Fix:** Introduce a `claw-dashboard` principal alongside the existing auto-generated principals. `prepareClawAPIRuntime` always creates this principal when clawdash is injected (which is always — clawdash is unconditional). The principal gets `AllReadVerbs` + `agent.context` scoped to the pod. `compose_up.go` then:

1. Always creates the `claw-dashboard` principal when `p.ClawAPI != nil` (in the auto-principal block at ~line 1800, after the master principal and before the self principals).
2. Changes the clawdash env injection conditional from `p.ClawAPI != nil && hasPodInvokeEntries(p)` to `p.ClawAPI != nil`.
3. Looks up `claw-dashboard` instead of `claw-scheduler` for the token.

The scheduler principal continues to exist separately for invoke/schedule operations — it keeps its narrower verb set (`schedule.read`, `schedule.control`). The dashboard principal is the correct auth identity for clawdash's read-only observability role.

For pods that have *neither* master nor invoke (no claw-api at all), clawdash won't have API credentials and the Agents nav link should be hidden. The template checks `{{ if .HasAPIAccess }}` before rendering the link.

---

## Implementation Sequence

Work is ordered to deliver value incrementally and allow each layer to be tested before the next depends on it.

### Phase 1: cllama snapshot capture
1. Add `ContextSnapshot` struct and `sync.Map` store in `cllama/internal/proxy/`
2. Capture snapshot at the assembly-complete point in both OpenAI and Anthropic flows
3. Add `/internal/context` and `/internal/context/<id>/snapshot` endpoints to cllama UI handler
4. Unit test: mock request → verify snapshot captured with correct fields
5. Integration test: real cllama startup → send request → GET snapshot → verify content matches

### Phase 2: claw-api context endpoints
1. Add `agent.context` verb to principal system
2. Add `ContextHostDir` and `CllamaAPIURL` to `ClawAPIConfig`
3. Mount context directory and inject env vars in `compose_emit.go` / `compose_up.go`
4. Implement `GET /agents`, `GET /agents/<id>/contract`, `GET /agents/<id>/context` handlers
5. Add principal wiring in `prepareClawAPIRuntime`
6. Unit test: handler tests with fixture context directory
7. Integration test: full `claw up` → verify mounts and env vars in generated compose

### Phase 3: clawdash agents view
1. Add `/agents` route and index page
2. Add `/agents/<id>` detail page with contract tab
3. Add live context tab
4. Add nav bar link
5. Manual testing against a running pod (quickstart or trading-desk example)

---

## Codex Review Fixes

### Round 1 (v2)

| # | Severity | Finding | Fix |
|---|----------|---------|-----|
| 1 | High | `metadata.json` contains agent bearer token (`compose_up.go:572`); raw `/contract` response would leak credentials | Contract endpoint redacts `token` from metadata, `auth` from feeds, `token` from service-auth entries. Explicit redaction list, not generic filter. |
| 2 | High | claw-api has no credential to call cllama's bearer-authenticated UI endpoints | `compose_up.go` passes the existing `CLLAMA_UI_TOKEN` into `ClawAPIConfig.CllamaAPIToken`; emitted as `CLAW_CLLAMA_API_TOKEN` env var. |
| 3 | High | Snapshot `SystemMessage string` doesn't cover Anthropic block-array; capture point is before model resolution; managed-tool rounds mutate payload | `System` field is `any` (preserves string or block-array). Capture moved to after resolution. Managed-tool turns get TurnCount update on completion. |
| 4 | Medium | `CLAW_API_URL`/`TOKEN` only wired into clawdash when pod has invoke entries (`compose_up.go:695`) | Conditional changed to `p.ClawAPI != nil` (drop `hasPodInvokeEntries` guard). Agents nav hidden when no API access. |
| 5 | Medium | `/agents` returns all agents regardless of principal scope | Agent list and detail endpoints filtered by claw_id scope via `AllowsClawID()`, consistent with `fleet.query_metrics`. |

### Round 2 (v3)

| # | Severity | Finding | Fix |
|---|----------|---------|-----|
| 6 | High | Redaction missed `tools.json` (`ToolExecution.Auth` at `context.go:57`) and `memory.json` (`MemoryManifestEntry.Auth` at `context.go:79`) — both carry bearer tokens | Redaction is now recursive across all artifacts with a complete field table. Unit-tested against actual schemas. |
| 7 | High | Master-only pods have no `claw-scheduler` principal; `lookupClawAPIPrincipalToken("claw-scheduler")` fails when no invoke entries | Introduced dedicated `claw-dashboard` principal, always created when `p.ClawAPI != nil`. Decoupled from scheduler. |
| 8 | Medium | `AllowsClawID()` only checks pod scope and explicit ClawIDs; `BuildSelfPrincipal` only sets Services — self principals would 403 on all agent endpoints | New `allowsAgentContext()` function checks pod OR claw_id OR service scope. Self principals reach their service's agents via service match. |
| 9 | Medium | `ResolvedModel` described as "what the provider received" but resolution produces a candidate list with failover; pre-dispatch snapshot can't know which candidate succeeds | Replaced single `ResolvedModel` with `ChosenRef` + `[]CandidateSnapshot`. Snapshot shows the resolution policy, not a single potentially wrong value. |

## Open Questions

1. **Snapshot size budgeting.** A system message with multiple feeds and memory recall can be 10-50KB. The `sync.Map` holding one per agent is negligible for typical pods (<20 agents). Should we add a hard cap or TTL eviction? Leaning no — the memory cost is trivially bounded by agent count, and stale snapshots are still useful ("this is what it looked like on the last turn, 3 hours ago").

2. **Feed content in snapshot.** The `feed_blocks` breakdown includes actual feed content (channel messages, API data). This is useful for debugging but could be large. Should the snapshot truncate feed content? Leaning no for the snapshot itself — truncation should be a UI concern in clawdash if needed.

3. **Multi-proxy pods.** The design assumes one cllama proxy. The existing runtime already fails fast on multi-proxy, so this isn't a new limitation, but worth noting for the `CllamaAPIURL` wiring.

4. **Costs migration.** clawdash currently talks to cllama directly for costs via `CLAWDASH_CLLAMA_COSTS_URL`. Should we move costs behind claw-api in this work? Leaning no — costs are a working feature today, and migrating them adds scope without adding context visibility. File a separate issue.

---

## Files Changed (estimated)

| File | Change |
|------|--------|
| `cllama/internal/proxy/snapshot.go` | New — snapshot struct and sync.Map store |
| `cllama/internal/proxy/handler.go` | Capture snapshot after assembly (~10 lines per flow) |
| `cllama/internal/ui/handler.go` | Two new endpoint cases in ServeHTTP, two handler methods |
| `internal/clawapi/principal.go` | Add `VerbAgentContext`, update `AllReadVerbs`, add `BuildDashboardPrincipal()` |
| `internal/pod/compose_emit.go` | Add `ContextHostDir`, `CllamaAPIURL` to config; mount + env |
| `cmd/claw/compose_up.go` | Set new ClawAPIConfig fields; add dashboard principal; widen clawdash env conditional; pass cllama UI token |
| `cmd/claw-api/handler.go` | Three new route cases, three handler methods, cllama proxy helper |
| `cmd/claw-api/main.go` | Read `CLAW_CONTEXT_ROOT`, `CLAW_CLLAMA_API_URL`, `CLAW_CLLAMA_API_TOKEN` from env |
| `cmd/clawdash/handler.go` | New route, agent page rendering, claw-api client calls |
| `cmd/clawdash/templates/agents.html` | New — agent index page |
| `cmd/clawdash/templates/agent_detail.html` | New — agent detail page with tabs |
| `cmd/clawdash/templates/fleet.html` | Add "Agents" to nav bar |
| `internal/clawapi/skill.go` | Update skill descriptor with new endpoints |
