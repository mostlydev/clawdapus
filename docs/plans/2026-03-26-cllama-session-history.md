# cllama Session History Plan

## Goal

Retain durable per-agent session history inside `cllama`, owned by infrastructure rather than by the runner.

This plan is intentionally split into phases:

- Phase 1 in this document: persist normalized session history to disk.
- Later phases: expose scoped history reads, build summaries and retrieval indices, and eventually use time-decayed retrieval in `cllama`.

Phase 1 does not implement RAG, prompt injection from history, or any new runner instructions.

## Terminology

- Session history: the append-only record of LLM turns observed by `cllama` at the proxy boundary.
- Memory: agent-authored notes and files in the runner-owned portable memory surface, currently mounted at `/claw/memory` via `CLAW_MEMORY_DIR`.
- Derived memory: summaries, embeddings, indices, or other retrieval artifacts built from session history and possibly other sources later.

This distinction matters:

- Session history is infra-owned and should be captured even if a claw never writes notes.
- Memory remains runner-owned scratch and note-taking space. This plan does not change that contract.
- A future retrieval layer may consume session history, memory, or both, but that is a separate feature.

## Current Repo Constraints

- `claw up` deletes `.claw-runtime` on every run, so durable session history must not live under that tree.
- Portable memory already exists as a separate runner-facing surface in `internal/driver/shared/memory.go`; this plan must not overload or rename it.
- `cllama` already handles both JSON and SSE responses, so session history storage must support both formats cleanly.
- `cllama` currently exposes proxy routes and health only. A history query surface is future work, not part of this phase.

## Phase Breakdown

### Phase 1: Durable Session Retention

Capture successful turns in `cllama` and write them to a persistent host directory mounted into the proxy container.

Deliverables:

- durable host-backed per-agent session logs
- normalized JSONL envelope that can represent both JSON and SSE responses
- no retrieval API yet
- no prompt decoration from prior sessions yet

### Phase 2: Scoped Session Read Surface

Add a self-scoped read API in `cllama` so a claw can ask for its own recent session history using the same bearer token model already used for proxy auth.

This is the point where instructions can tell claws they may query `cllama` for session history.

### Phase 3: Derived Memory / Retrieval

Build derived artifacts on top of the durable session log:

- rolling summaries
- recency windows
- optional embeddings or keyword index
- time-decayed ranking for retrieval

This is where `cllama` starts behaving like a memory-aware retrieval layer rather than only a recorder.

## Architecture For Phase 1

### Ownership Boundary

- `cllama` owns session history because all LLM traffic already passes through the proxy.
- claws continue to own their explicit notes in `/claw/memory`.
- Phase 1 does not attempt to synchronize these two surfaces.

### Persistent Host Layout

Use a persistent sibling directory next to `.claw-runtime`, following the same persistence model as `.claw-auth`.

Proposed host layout:

```text
<pod-dir>/
├── .claw-runtime/
├── .claw-auth/
└── .claw-session-history/
    ├── analyst-0/
    │   └── history.jsonl
    └── researcher/
        └── history.jsonl
```

Container mount:

```text
/claw/session-history/
```

Environment variable:

```text
CLAW_SESSION_HISTORY_DIR=/claw/session-history
```

### Recording Model

Phase 1 stores one JSONL entry per successful turn.

Rules:

- record only successful upstream completions (`2xx`)
- do not treat provider errors or retries as session history
- preserve the original agent request separately from the effective upstream request
- support both JSON and SSE response bodies without writing invalid JSONL

### Entry Shape

Use a normalized envelope rather than writing raw bytes directly into `json.RawMessage` fields.

Suggested schema:

```go
type Payload struct {
	Format string          `json:"format"`           // "json" or "sse"
	JSON   json.RawMessage `json:"json,omitempty"`   // used when Format == "json"
	Text   string          `json:"text,omitempty"`   // used when Format == "sse"
}

type Usage struct {
	PromptTokens     int      `json:"prompt_tokens,omitempty"`
	CompletionTokens int      `json:"completion_tokens,omitempty"`
	ReportedCostUSD  *float64 `json:"reported_cost_usd,omitempty"`
}

type Entry struct {
	Version           int             `json:"version"`
	TS                string          `json:"ts"`
	ClawID            string          `json:"claw_id"`
	Path              string          `json:"path"`
	RequestedModel    string          `json:"requested_model"`
	EffectiveProvider string          `json:"effective_provider"`
	EffectiveModel    string          `json:"effective_model"`
	StatusCode        int             `json:"status_code"`
	Stream            bool            `json:"stream"`
	RequestOriginal   json.RawMessage `json:"request_original,omitempty"`
	RequestEffective  json.RawMessage `json:"request_effective,omitempty"`
	Response          Payload         `json:"response"`
	Usage             Usage           `json:"usage,omitempty"`
}
```

Notes:

- `RequestOriginal` is the claw-visible request body before feed or time injection.
- `RequestEffective` is the body actually forwarded upstream after `cllama` modifications.
- `Response.Format == "sse"` stores the captured event stream as text, not as invalid JSON.

This schema is for retention, not retrieval. Retrieval-specific summaries and chunking come later.

## Codebase Map

### cllama submodule

- `cllama/internal/sessionhistory/` - new package for entry schema and recorder
- `cllama/internal/proxy/handler.go` - capture request and response data and record successful turns
- `cllama/internal/proxy/handler_test.go` - add JSON, SSE, and non-2xx coverage
- `cllama/cmd/cllama/main.go` - wire env var into the proxy handler
- `cllama/cmd/cllama/main_test.go` - env/config coverage

### main repo

- `internal/pod/compose_emit.go` - mount session history volume into the `cllama` service
- `internal/pod/compose_emit_test.go` - assert env and volume are emitted
- `cmd/claw/compose_up.go` - create the persistent host directory outside `.claw-runtime`
- `cmd/claw/compose_up_test.go` - add coverage for persistent dir helper or path wiring
- `docs/CLLAMA_SPEC.md` - document the new mount and env var
- `cllama/README.md` - document local runs and new storage path

## Key Files To Read Before Starting

- `cllama/internal/proxy/handler.go`
- `cllama/internal/proxy/handler_test.go`
- `cllama/cmd/cllama/main.go`
- `internal/pod/compose_emit.go`
- `cmd/claw/compose_up.go`
- `internal/driver/shared/memory.go`
- `internal/driver/shared/clawdapus_md.go`

## Implementation Tasks

## Task 1: Add a dedicated session history package

Files:

- create `cllama/internal/sessionhistory/recorder.go`
- create `cllama/internal/sessionhistory/recorder_test.go`

Implementation requirements:

- use the package name `sessionhistory`, not `history`, to avoid confusion with runner memory
- recorder is append-only and thread-safe
- empty base dir means no-op
- write to `baseDir/<agent-id>/history.jsonl`
- create agent subdirectories with permissive runtime-friendly permissions
- marshal the normalized `Entry` envelope shown above

Test coverage:

- writes one JSON entry for a normal JSON response
- appends multiple turns for the same agent
- supports concurrent writes without line corruption
- supports `Payload{Format: "sse", Text: ...}` without JSON marshal failure
- no-op when base dir is empty

Suggested commit:

```bash
cd cllama
git add internal/sessionhistory/
git commit -m "feat(sessionhistory): add durable session history recorder"
```

## Task 2: Wire session recording into the proxy handler

Files:

- modify `cllama/internal/proxy/handler.go`
- modify `cllama/internal/proxy/handler_test.go`

Handler changes:

- add a `sessionRecorder *sessionhistory.Recorder` field to `Handler`
- add `WithSessionHistory(dir string)` as a `HandlerOption`
- thread both request bodies through the call stack:
  - original request body from the claw
  - effective upstream request body after injection and model rewrite
- after the upstream response has been streamed to the client, capture the response body and convert it into a `sessionhistory.Payload`

Recording rules:

- record only when `200 <= status < 300`
- keep structured logs as the source of truth for failures; do not mix failures into session history
- reuse existing usage extraction so history entries carry token counts when available
- preserve current cost tracking behavior

Payload rules:

- for JSON responses: `Payload{Format: "json", JSON: captured}`
- for SSE responses: `Payload{Format: "sse", Text: string(captured)}`

Required tests:

- `TestHandlerRecordsSessionHistoryJSON`
- `TestHandlerRecordsSessionHistorySSE`
- `TestHandlerSkipsSessionHistoryOnUpstreamError`

Suggested commit:

```bash
cd cllama
git add internal/proxy/handler.go internal/proxy/handler_test.go
git commit -m "feat(proxy): record successful turns to session history"
```

## Task 3: Wire config and env in cllama main

Files:

- modify `cllama/cmd/cllama/main.go`
- modify `cllama/cmd/cllama/main_test.go`

Changes:

- add `SessionHistoryDir string` to the `config` struct
- read `CLAW_SESSION_HISTORY_DIR` in `configFromEnv`
- pass the value into `newAPIHandler`
- append `proxy.WithSessionHistory(cfg.SessionHistoryDir)` when non-empty

Tests:

- env set -> config field populated
- env unset -> config field empty

Suggested commit:

```bash
cd cllama
git add cmd/cllama/main.go cmd/cllama/main_test.go
git commit -m "feat(main): wire CLAW_SESSION_HISTORY_DIR into proxy"
```

## Task 4: Mount session history into the cllama compose service

Files:

- modify `internal/pod/compose_emit.go`
- modify `internal/pod/compose_emit_test.go`

Changes:

- add `SessionHistoryHostDir string` to `CllamaProxyConfig`
- if set, mount it at `/claw/session-history:rw`
- if set, emit `CLAW_SESSION_HISTORY_DIR=/claw/session-history`

Do not reuse the ambiguous name `CLAW_HISTORY_DIR`.

Required test update:

- extend `TestEmitComposeWithCllamaProxy` to assert both the mount and env var are present

Suggested commit:

```bash
git add internal/pod/compose_emit.go internal/pod/compose_emit_test.go
git commit -m "feat(compose): mount session history into cllama service"
```

## Task 5: Create the persistent host directory in claw up

Files:

- modify `cmd/claw/compose_up.go`
- modify `cmd/claw/compose_up_test.go`

Changes:

- create a persistent sibling directory:

```text
<pod-dir>/.claw-session-history
```

- do not place session history under `.claw-runtime`
- wire that host path into `CllamaProxyConfig.SessionHistoryHostDir`

Recommended refactor:

- extract a small helper for persistent `cllama` dirs so this behavior has a direct unit-test seam
- keep `.claw-auth` and `.claw-session-history` together in that helper

Required test coverage:

- helper returns sibling paths outside `.claw-runtime`
- helper creates `.claw-session-history` with writable permissions
- proxy config wiring uses the persistent path

Suggested commit:

```bash
git add cmd/claw/compose_up.go cmd/claw/compose_up_test.go
git commit -m "feat(compose_up): create persistent session history dir for cllama"
```

## Task 6: Update docs to reflect the new boundary

Files:

- modify `docs/CLLAMA_SPEC.md`
- modify `cllama/README.md`
- optionally update `AGENTS.md` if the runtime model section should mention persistent session history explicitly

Doc changes:

- describe `CLAW_SESSION_HISTORY_DIR`
- document `/claw/session-history` as distinct from `/claw/memory`
- state clearly that Phase 1 is retention only, not retrieval

## Verification

After implementation:

1. Run `cd cllama && go test ./...`
2. Run `go test ./...` from the repo root
3. Bring up a real cllama-enabled pod, for example:

```bash
go build -o /tmp/claw ./cmd/claw
/tmp/claw up -d examples/rollcall/claw-pod.yml
```

4. Verify the persistent host dir exists outside runtime reset:

```bash
ls examples/rollcall/.claw-session-history
```

5. Trigger a real LLM turn.
6. Verify a per-agent history file exists:

```bash
cat examples/rollcall/.claw-session-history/<agent-id>/history.jsonl
```

7. Confirm:

- entries are valid JSONL
- JSON responses are stored as `response.format == "json"`
- streamed responses are stored as `response.format == "sse"`
- non-2xx upstream failures do not create session history entries

## Explicit Non-Goals For Phase 1

- no history query API
- no prompt injection from prior sessions
- no embeddings or vector index
- no time-decay ranking
- no merging of session history into `/claw/memory`
- no attempt to standardize scratch-memory conventions used by claws

## Follow-On Work

### Phase 2: Self-Scoped Read API

Add a read API in `cllama` so a claw can retrieve its own recent session history using its bearer token. At that point, updating claw instructions to say "you can query cllama for session history" becomes meaningful and enforceable.

### Phase 3: Derived Retrieval Layer

Build retrieval on top of the retained session log:

- recent-turn window
- rolling summaries for older sessions
- time-decayed ranking
- optional semantic search

Likely retrieval strategy:

- prefer very recent exact turns
- prefer summaries for older history
- apply recency decay so stale sessions lose relevance over time

### Phase 4: Cross-Surface Memory Strategy

Decide whether future retrieval should consume:

- session history only
- portable memory only
- both, but as separate ranked sources

That decision should be made after the scratch-memory standardization work settles. Phase 1 deliberately does not pre-commit to a merged model.
