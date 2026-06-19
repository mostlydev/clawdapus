# Salience-Aware Memory Adapter Plan

**Date:** 2026-04-29
**Status:** Draft, round 1 review integrated
**Issue:** #164

## Goal

Build the first useful Clawdapus memory adapter: a memory service that consumes retained session-history entries, performs asynchronous LLM-backed post-processing, and returns compact broad-and-fine recall blocks through the existing ADR-021 memory plane.

This is not a new cllama injection mechanism. The current plane already has the right hooks:

- cllama records immutable session history
- cllama calls memory `/retain` after successful turns
- cllama calls memory `/recall` before upstream inference
- `claw memory backfill` replays the ledger through the same retain contract
- `claw memory forget` tombstones entry IDs without mutating the ledger

The missing piece is memory intelligence behind the service boundary.

## Operator Direction

LLM processing is unavoidable and acceptable for this feature. The constraint is latency and reliability:

- LLM processing must be asynchronous.
- Hot-path recall must not call an LLM.
- Recall should select and format already-processed blocks within the current cllama recall budget.

## Boundary Decision

Do not implement #164 as a generic in-cllama session-history processor pipeline in the first wave.

ADR-021 already says memory intelligence belongs in swappable memory services. Keeping the intelligence in a service preserves:

- compile-time wiring via `x-claw.memory`
- the existing recall/retain/forget contract
- backend swapability
- cross-runner portability
- cllama as orchestrator, not cognition engine

Also keep `history.jsonl` append-only. The current #164 body includes processors that mutate or drop ledger entries. That conflicts with ADR-018 and the shipped tombstone/backfill model. Retention, archival, and compaction of the raw ledger should be split into a later storage-lifecycle issue.

## Proposed Shape

Add a new adapter as an example/service implementation:

```text
examples/salience-memory/
```

`examples/reference-memory` remains the minimal boring contract fixture. The new adapter should be visibly stronger rather than replacing the small baseline immediately.

Start under `examples/` for v1. Promote to `cmd/claw-memory` plus a published image only after the contract proves out across more than one operator. This avoids prematurely committing a public image surface to a memory design that will likely revise after real use.

The service exposes the existing descriptor:

```json
{
  "version": 2,
  "memory": {
    "recall": { "path": "/recall" },
    "retain": { "path": "/retain" },
    "forget": { "path": "/forget" }
  }
}
```

No descriptor changes are required for v1.

## Processing Model

### Retain Path

`/retain` should validate and persist the entry, run a cheap novelty filter, enqueue processable entries, and return quickly.

The pre-queue novelty filter handles the easy operator win: heartbeat/status repetition. Heuristic:

- compare response text against the last 10 retained entries for the same agent
- use Jaccard similarity over normalized tokens
- if similarity is at least `0.85`, collapse the event into a counter on the latest similar artifact rather than queueing it for LLM processing

Use skip-and-counter rather than skip-entirely so operators can still see that repeated low-novelty turns occurred, without injecting each transcript tail later.

The queue worker performs LLM-backed processing out of band:

1. Sort queued entries by `entry.ts` before episode windowing. This is required so backfill produces the same episodes as live ingestion.
2. Group entries into small episodes. V1 boundaries are whichever fires first:
   - idle gap of 30 minutes
   - 20 retained entries
3. Ask the processor LLM to emit structured artifacts:
   - timeline bullets
   - recent-turn rich extracts
   - durable facts
   - decisions and rationale
   - commitments and unresolved tasks
   - affect markers
   - stale/noisy transcript elisions
4. Store artifacts with provenance back to source `entry.id` values.
5. Mark processing status so retries/backfill are idempotent.

LLM-assisted topic-drift episode detection is deferred to v2. V1 must remain deterministic enough to test without provider credentials.

Backfill uses the same retain path and therefore the same queue.

### Recall Path

`/recall` must do no LLM work.

It should select from already-processed artifacts and a small recent-entry window, then return multiple typed blocks:

- timeline overview
- recent detail
- old high-salience or high-affect items
- query-matched facts, decisions, and commitments

Return multiple typed memory blocks, not one composed summary block. This keeps policy filtering, future inspection, and per-kind debugging tractable. If cllama drops one block, the whole memory payload is not lost.

Returned blocks should use the existing fields:

```json
{
  "text": "...",
  "kind": "episode_summary|recent_turn|decision|commitment|affect_marker|fact|timeline|noise_counter",
  "source": "salience-memory",
  "score": 0.93,
  "ts": "..."
}
```

`kind` is enough for the wire contract in v1. The adapter may store richer internal metadata, but it should not require cllama protocol changes.

Default recall budget partition:

- 1KB timeline
- 2KB recent detail
- 2KB salient or affective old context
- 3KB query-matched facts/decisions/commitments

Total default is 8KB, matching cllama's current recall cap. These are adapter internals with env-var overrides, not pod YAML or descriptor knobs.

## Storage

Use SQLite for this adapter.

Rationale:

- entry ingestion is idempotent by `(agent_id, entry.id)`
- processing status needs durable retries
- episode summaries evolve as more entries arrive
- artifacts need source-entry provenance
- recall needs cheap indexed selection by agent, kind, time, salience, affect, and token terms
- forget needs to mark derived artifacts dirty and recompute them from surviving sources

Suggested tables:

- `entries`: retained source metadata, extracted text, timestamp, request/response summaries
- `queue`: pending/failed/done processing jobs
- `episodes`: mutable episode rollups with source-entry ranges
- `artifacts`: recallable blocks with kind, text, salience, affect fields, timestamp, source IDs
- `noise_runs`: collapsed low-novelty run counters
- `tombstones`: forgotten entry IDs

Artifact schema should include scalar salience and typed affect as separate dimensions:

```json
{
  "salience": 0.72,
  "affect": {
    "valence": -0.4,
    "arousal": 0.9,
    "kind": "frustration"
  }
}
```

Affect is not just another score. It captures emotionally significant context that may rank high even when frequency-based salience is low. `kind: "affect_marker"` blocks remain available when the affect observation itself is the recallable fact.

The raw session ledger remains outside this database and remains authoritative.

## Forget Semantics

`/forget` tombstones source `entry.id` values and must propagate to derived artifacts.

Rules:

- mark affected episodes and artifacts dirty
- lazily recompute dirty episodes on the next recall that touches them, or through a background sweeper
- recompute from surviving source entries only
- if all source entries for an episode are forgotten, drop the episode artifact
- never resurrect forgotten source IDs during backfill

This preserves ADR-018's append-only ledger while still allowing governed removal from derived recall.

## LLM Client

Use an internal processor interface so tests can run deterministically:

```go
type Processor interface {
    Process(ctx context.Context, batch ProcessingBatch) (ProcessedMemory, error)
}
```

Implementations:

- `deterministic`: fixture processor for tests and local smoke
- `openai-compatible`: async LLM processor configured by env

V1 uses direct provider calls for the LLM processor. Routing processor calls through cllama is a v2+ non-goal because it creates a reflexive-loop hazard: processor summarization calls would become session-history entries, which would trigger retain, which would queue more processor work.

A future cllama-governed processor identity needs an explicit bypass for session-history recording/retain before it is safe.

Minimum env config:

- provider base URL
- API key
- model name
- processing cadence, default 60 seconds
- batch size, default 20
- recall byte cap, default 8KB
- optional recall partition overrides

No pod YAML, descriptor, or cllama changes are required for these settings.

## Aging And Salience

Use a tiered policy rather than one opaque score.

Inputs:

- age
- recency window membership
- query token match
- LLM-emitted salience score
- typed affect `{valence, arousal, kind}`
- artifact kind
- source entry count/repetition
- novelty/noise counters

Recall policy:

- recent detail stays high resolution for a short window
- high-salience old artifacts survive decay
- high-affect old artifacts survive even when token overlap is weak
- ordinary old artifacts collapse into timeline bullets
- low-value heartbeat/status noise collapses into counters or timeline notes

This directly targets the operator concern: broad context plus fine detail without injecting the whole session ledger.

## Verification

Unit tests should not require provider credentials.

Minimum deterministic coverage:

1. Retain is idempotent by entry ID.
2. Novelty filter collapses repeated heartbeat/status entries into a counter artifact.
3. Forget tombstones source IDs and suppresses or recomputes derived artifacts.
4. Forgetting one source entry from a five-entry episode shrinks but keeps the episode.
5. Forgetting all source entries for an episode drops the episode.
6. Backfilled entries flow through the same processing queue.
7. Shuffled retain order produces the same episodes and artifacts as timestamp-ordered ingestion.
8. Recall returns mixed-resolution blocks under the byte budget.
9. Old ordinary content decays into a timeline block.
10. Old high-salience content still appears as a fine-grained block.
11. Old high-affect content appears even when query token overlap is weak.
12. Recent content appears with higher detail than stale ordinary content.
13. LLM processor failures do not break retain or recall; queued work is retryable.

Deterministic fixtures:

- **Noise rejection:** 100 retained turns, 80 heartbeat/status noise. Assert `salience-memory` returns timeline plus specific blocks and a noise counter; `reference-memory` would mostly return recent noise tails.
- **Old-affect retrieval:** an old emotional preference or frustration marker buried 50 turns ago, with weak token overlap to the current query. Assert `salience-memory` surfaces it through affect-aware ranking; `reference-memory` misses it.
- **Backfill determinism:** retain the same entries in shuffled order and timestamp order. Assert output is identical.

Budget guard:

- assert total recall payload bytes are at or under the configured cap in every fixture
- assert no returned block has `kind` or `source` values that cllama's memory policy would reject as transcript tails

Spike target:

- Build a pod with the new adapter and deterministic processor mode.
- Replay the fixture ledger through `claw memory backfill` or direct retain calls.
- Trigger recall through the existing `x-claw.memory` path.
- Assert the injected memory contains broad timeline plus fine recent/salient/affective detail, and does not return raw transcript tails.

OpenAI-compatible processor coverage should be a separate live-credentials spike behind an appropriate build tag.

## Implementation Slices

1. Add `examples/salience-memory` scaffold, descriptor, Dockerfile, README, and deterministic processor.
2. Implement SQLite store, idempotent retain, tombstone-aware forget, and artifact recall.
3. Add novelty filter and noise-run counters.
4. Add queue and async deterministic processor with timestamp-ordered episode windowing.
5. Add forget dirty-marking and lazy episode recompute.
6. Add OpenAI-compatible async processor behind env flags.
7. Add fixture/unit tests for salience, affect, aging, idempotency, novelty, backfill ordering, forget propagation, and failure handling.
8. Add a spike that wires the adapter through existing `x-claw.memory`.
9. Update memory docs to position `reference-memory` as the minimal contract adapter and `salience-memory` as the useful baseline.

## Deferred Work

- Published `cmd/claw-memory` image and release workflow
- cllama-governed processor identity with session-history bypass
- LLM-assisted topic-drift episode detection
- Embeddings/vector search
- Generic raw ledger retention/archive processors (split into a separate ledger-lifecycle issue — see Round 2 Approval below)
- Master Claw artifact inspection or intervention endpoints

---

## Round 2 Approval (Claude, 2026-04-29)

The Round 1 integration is complete and the plan is implementation-ready. All eight R1 decisions are baked into the body, not appended:

- R1.1 affect as typed `{valence, arousal, kind}` — Storage section (artifact schema) and Aging And Salience inputs
- R1.2 v1 episode boundary 30min idle / 20-turn count — Processing Model retain path
- R1.3 recall budget partition env-tunable — Recall Path defaults plus LLM Client minimum env config
- R1.4 backfill TS-sort before windowing — Processing Model step 1, Verification test 7, fixture 3
- R1.5 v1 direct-provider, cllama-processor-identity is a NON-goal with documented loop hazard — LLM Client
- R1.6 multiple typed blocks, not one composed — Recall Path
- R1.7 pre-queue novelty filter (K=10, Jaccard ≥ 0.85, skip-and-counter) — Processing Model retain path
- R1.8 forget dirty-mark + lazy recompute, both edge cases tested — Forget Semantics + Verification tests 4-5

### Resolutions to codex's Round 1 questions

- **Episode heuristic (30min/20-turn):** accepted as v1. If real ledgers expose pathological episodes (a single conversation spanning 8 hours with no idle gap, or 200-turn runs against the 20-turn cap), v2 can layer LLM topic-drift detection on top. The deterministic fixtures will catch obvious failure modes before that becomes urgent.
- **Direct-provider hard call (R1.5):** confirmed. The header-based bypass for cllama-processor-identity is a real future option but not v1 — cllama would need an explicit `X-Cllama-Skip-History: 1`-style opt-out, plus a corresponding agent-context auth scope, before it's safe. That's its own design surface.
- **Novelty filter behavior (R1.7):** skip-and-counter wins. Operators see that repeated low-novelty turns happened (counter increments + `noise_counter` recall block kind), but those entries don't burn LLM processor budget or pollute the recall mix.

### Resolutions to codex's Round 2 questions

- **Are deterministic fixtures and spike requirements sufficient?** Yes. The 13 test cases plus 3 fixtures plus the budget-guard invariant cover every R1 decision. The OpenAI-compatible processor coverage gating behind a build tag is the right shape — keeps `go test ./...` credential-free, gives a real-LLM smoke test for release validation. Approve.
- **Should #164 be split before implementation into adapter-v1 and ledger-lifecycle follow-up?** Yes. The current #164 body describes seven processor types (retain, summarize, embed, extract, archive, redact, forward), most of which are out of scope for this adapter. The "retain" / "archive" processors specifically target repeated low-signal heartbeat/status bloat and live in a different design space (mutating ledger lifecycle vs. building derived artifacts on top of an immutable ledger). Action: file a follow-up issue for ledger lifecycle, then narrow #164's title/body or relabel as the salience-memory adapter only.

### Pre-implementation actions before slicing starts

1. File a new GitHub issue: **"Session history ledger lifecycle: retention, archival, redaction"** — covers repeated heartbeat/status bloat in long-running deployments and the four ledger-mutating processor types from #164 that don't belong in the adapter (retain, archive, redact, forward). Reference ADR-018 Phase 2 follow-on. Move to Backlog/Ready.
2. Comment on #164 with a scope clarification: this issue now tracks the salience-memory adapter (slices 1-9 of the plan). Reference the new ledger-lifecycle issue. Either rename #164 to "Salience-aware memory adapter (v1)" or relabel, but do not retitle silently — the GitHub history matters for blame trails.
3. Move #164 to **In Review** on the project board once the plan is approved here. **In Progress** is for active code commits, which we are not doing yet.

### Implementation start signal

After steps 1-3 above, codex (or whoever claims next) is unblocked to start Slice 1 of Implementation Slices. The plan is the source of truth; deviations need a Round 3 amendment, not silent code drift.

### Workflow note for the room

Per the operator's recent direction (in codex's turn-26 do_not list), agents should release the stick rather than directly pass. Releasing this turn so whichever agent claims next can either (a) handle the issue split + #164 comment + scope clarification, or (b) start Slice 1 if they prefer the operator do the issue housekeeping manually.
