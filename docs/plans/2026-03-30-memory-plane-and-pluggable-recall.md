# Memory Plane and Pluggable Recall Plan

## Goal

Introduce memory as a first-class infrastructure plane in Clawdapus:

- runner-agnostic
- durable across rebuilds and runner swaps
- governed by `cllama`
- implemented by swappable memory services rather than by runner plugins

This document is intentionally a plan, not yet an ADR. It is meant to sharpen the boundary between:

- what Clawdapus should own
- what `cllama` should own
- what memory backends and vendors should own

The central claim is:

**Clawdapus should own the reliable lifecycle hooks and policy surface for memory, but it should not own the intelligence of memory itself.**

**Raw recent history is not the product. Derived durable state is the product.**

That intelligence includes retention strategy, salience, summarization, embeddings, graph extraction, affect modeling, ranking, deduplication, decay, and recall selection.

Those should remain swappable behind a stable service contract.

---

## Why This Exists

The current repo already has the right primitives, but not yet a complete memory plane:

- `cllama` already captures durable per-agent session history at the proxy boundary.
- `cllama` already injects live context through feeds and request decoration.
- Clawdapus already compiles per-agent manifests into mounted context directories.
- Services already self-describe through `claw.describe`.
- The manifesto already states that memory must survive the container and the runner.

What is missing is the pipeline that connects:

1. raw retained turns
2. derived memory artifacts
3. request-time recall

through a clean, pluggable service contract.

The problem is not only persistence. The problem is useful recall.

If the system only re-injects the most recent raw turns, it adds very little. Runners already maintain live sessions and recency windows. The real value of infrastructure memory is the ability to surface durable, relevant, derived context that the live session window does not preserve reliably.

Examples:

- long-lived user preferences
- stable facts about operators, services, repos, or accounts
- open commitments and unresolved tasks
- previous decisions and their rationale
- episodic summaries from older sessions
- project state that spans many conversations
- cross-runner continuity after migration or rebuild

In other words:

**Raw recent history is not the product. Derived durable state is the product.**

Raw history is still essential, but as the source of truth, not as the typical recall payload.

---

## Current Repo Position

The architecture in-tree already points in this direction.

### 1. Infra-owned retention already exists

ADR-018 established:

- session history is infra-owned
- session history is written by `cllama`
- portable memory is runner-owned
- the two surfaces must remain distinct

This is the correct foundation. Raw history should be captured once at the one place all cllama-enabled runners share: the governance proxy boundary.

### 2. Request-time context injection already exists

The current `cllama` implementation already:

- loads per-agent feed manifests
- fetches live context
- prepends it into OpenAI and Anthropic requests
- injects current time

So memory recall is not a new category of behavior. It is a new kind of request-time enrichment.

### 3. Service self-description already exists

Services already advertise capabilities through `claw.describe`, and `claw up` already compiles those capabilities into per-agent runtime artifacts.

That means memory backends do not need to be runner plugins. They can be pod services with normal Clawdapus discovery, auth projection, and compile-time wiring.

### 4. Tool mediation is already converging on the same architecture

ADR-020 is already establishing the pattern:

- service declares capability
- `claw up` compiles a manifest
- `cllama` may mediate or inject behavior at request time
- backend implementation remains external

Memory should follow the same architectural logic instead of inventing a separate plugin universe.

More strongly:

**Memory should use the exact same structural pattern that tools are moving toward.**

- provider declares capability in `claw.describe`
- consumer subscribes in pod YAML
- `claw up` compiles a per-agent manifest
- `cllama` enforces and mediates request-time behavior
- backend implementation remains swappable

This parallel should be treated as core architectural framing, not as an incidental similarity.

---

## Implementation Status (2026-04-03)

This branch now implements the core memory-plane substrate that this plan was proposing.

This plan should now be read alongside ADR-021 as its implementation-status companion:

- ADR-021 carries the architectural decision
- this document tracks implementation status, intentional deviations, and remaining work

Completed:

- descriptor `version: 2` support for `tools[]` and `memory`
- pod grammar for `x-claw.tools`, `x-claw.memory`, `tools-defaults`, and `memory-defaults`
- compiled `tools.json` and `memory.json` manifests in the per-agent `cllama` context
- a scoped `cllama` history read API at `GET /history/{agentID}`
- dedicated replay auth projection for subscribed memory services via:
  - `service-auth/cllama-history.json` in agent context
  - `CLAW_HISTORY_URL`
  - `CLAW_HISTORY_TOKEN`
  - `CLAW_HISTORY_AGENT_IDS`
- automatic attachment of declared feed/tool/memory provider services to `claw-internal`
- pre-turn recall and post-turn best-effort retain hooks in `cllama`
- provider-format-aware memory injection for OpenAI-style and Anthropic-style requests
- structured `memory_op` telemetry in `cllama` for:
  - recall skipped, succeeded, timed out, and failed outcomes
  - retain skipped, succeeded, timed out, and failed outcomes
  - latency, HTTP status, block count, and injected byte count where applicable
- operator replay UX via `claw memory backfill`, which:
  - discovers subscribed agents from generated context
  - replays the immutable local ledger back through the memory retain contract
  - auto-resolves a host-published retain URL when possible
  - supports explicit `--url` override when the memory service remains internal-only
  - maintains a lightweight append-only checkpoint index so `--after` replays do not always rescan from byte zero
- governed operator forget UX via `claw memory forget`, which:
  - targets stable session-history source-event IDs
  - dispatches the declared memory-service `forget` endpoint when present
  - writes append-only infra-owned tombstones instead of mutating `history.jsonl`
- a boring reference memory adapter under `examples/reference-memory`, which:
  - dedupes by stable `(agent_id, entry.id)`
  - persists forget tombstones and suppresses later replay of forgotten entries
  - provides simple recent / token-matching recall over retained summaries
  - is used by the rollcall example and the capability-wave spike path
- retain-side and recall-side policy enforcement in `cllama`, which:
  - drops blocked recall sources and kinds such as raw transcript tails
  - enforces bounded recall block-count and text-byte budgets before injection
  - redacts secret-shaped values from recalled memory blocks before they are injected into model context
  - scrubs secret-shaped values from retained request/response payloads before they are handed to the memory backend
  - records policy-removal / redaction counts in retain metadata and `memory_op` telemetry
- stable source-event IDs on session-history entries, propagated through:
  - live `retain` payloads
  - `GET /history/{agentID}`
  - `claw memory backfill`
  - `claw history export`
- `history export`, `claw memory backfill`, and `GET /history/{agentID}` now use the same per-agent `history.index.json` checkpoint sidecar to seek near `after` timestamps instead of always forward-scanning the full ledger
- tombstone-aware replay hygiene in `claw memory backfill`, so forgotten source-event IDs are not re-retained on later rebuilds
- `cllama` now loads `tools.json` into typed agent context
- managed OpenAI-compatible tool presentation and mediation in `cllama`, including:
  - replacement of outgoing runner-local `tools[]` with compiled managed tools
  - HTTP execution of declared managed tools
  - bounded mediation with `max_rounds`, per-tool timeout, total timeout, and response size limits
  - structured tool error feedback back into the model within the mediated loop
  - synthetic downstream SSE re-streaming when the runner requested `stream: true`
- managed Anthropic-format tool presentation and mediation in `cllama`, including:
  - replacement of outgoing runner-local `tools` / `tool_choice` with compiled managed tools
  - HTTP execution of declared managed tools via `tool_use` / `tool_result`
  - synthetic downstream SSE re-streaming when the runner requested `stream: true`
- ADR-020 session-history extensions for mediated requests, including:
  - `status`
  - `usage.total_rounds`
  - `tool_trace`
- bounded cross-turn continuity for managed OpenAI-compatible tools by reinjecting the hidden assistant/tool transcript into later upstream requests
- bounded cross-turn continuity for managed Anthropic tools by reinjecting the hidden assistant/tool transcript into later upstream requests
- a live capability-wave spike in `cmd/claw/spike_capability_wave_live_test.go`, which drives one real Discord turn through:
  - managed tool mediation
  - session-history `tool_trace`
  - `claw audit` telemetry
  - memory recall/retain telemetry in the same turn

Important ADR-020 status note:

- the compiler side of the capability wave is now present for tools:
  - descriptor `version: 2` accepts `tools[]`
  - `x-claw.tools` is parsed and normalized
  - `claw up` writes `tools.json`
  - `CLAWDAPUS.md` lists managed tools
  - `claw up` now fails fast when non-`cllama` services declare `x-claw.tools` or `x-claw.memory`, rather than accepting silent no-op capability config
- the mediated runtime side is now substantially landed for OpenAI-compatible and Anthropic-format requests:
  - `cllama` loads `tools.json`
  - `cllama` injects managed tools into upstream OpenAI-compatible and Anthropic requests
  - `cllama` executes managed HTTP tools in a bounded mediation loop across both provider formats
  - session history emits `tool_trace` and mediated `status` for these turns
  - `claw audit` now merges session-history-derived `tool_call` events with proxy logs, so operators can see managed tool activity and failures without manual ledger inspection
  - downstream streaming requests are satisfied by synthetic SSE re-streaming after mediation completes
  - long-running managed streaming requests now emit transport-level SSE keepalive/progress comments while the hidden tool loop is in flight
  - hidden mediated tool rounds are preserved across later turns for both request formats
- the first-wave ADR-020 runtime slices are now effectively landed for the mediated `cllama` path; later ADR-020 work is mostly native-projection and broader roadmap follow-through

The first ADR-021 hardening wave is now effectively landed:

- stable source-event IDs define the replay/dedupe contract
- forget is represented as tombstones rather than ledger mutation
- backfill honors those tombstones and indexed `after` reads
- retain/recall policy filtering is observable in telemetry
- the repo now ships a small reference adapter that follows the contract end to end

Implemented with minor intentional drift from the first sketch:

- recall currently sends the full inbound `messages` payload and, for Anthropic requests, the top-level `system` field rather than pre-shaping a smaller recent-message slice
- `claw memory backfill` currently replays the local immutable ledger through the memory service's declared `retain` endpoint rather than through a backend-native replay control plane

These are implementation-shape choices, not architectural deviations. They preserve the core model:

- `cllama` owns orchestration
- the ledger remains the source of truth
- the memory backend remains swappable behind the same contract

That means the plan should now be read as:

- architectural rationale for the memory plane
- explanation of the boundaries that remain important
- checklist of the remaining hardening and operator-facing work

---

## Problem Statement

Without a shared memory plane, users are pushed toward runner-local memory systems:

- OpenClaw plugins
- per-runner vector databases
- runner-specific hooks
- incompatible stores and formats

This has several structural downsides:

- memory becomes coupled to one runner family
- changing `CLAW_TYPE` threatens continuity
- memory persistence depends on runner cooperation
- every runner may duplicate infrastructure work
- every runner may spin up its own retrieval stack
- governance over retained and recalled content becomes inconsistent

This violates the repository's direction in two ways:

1. It undermines runner-agnostic persistence.
2. It moves a governance-relevant concern back into the trusted application layer.

Memory quality may ultimately be where a large share of agent performance comes from. That is a reason to expose strong hooks and clean contracts, not a reason to hardcode memory intelligence into the proxy or into runners.

---

## Design Principles

### 1. The ledger is sacred

`history.jsonl` is the immutable substrate.

It is:

- append-only
- normalized
- operator-visible
- rebuildable input for any future memory backend

Memory services may fail, change, or be replaced. The ledger remains the stable truth.

### 2. Portable memory stays runner-owned

`/claw/memory` remains:

- runner-writable
- agent-authored
- format-agnostic
- separate from infra-owned history

We should not collapse session history and portable memory into a single surface.

### 3. `cllama` owns orchestration, not cognition

`cllama` should know:

- when to call recall
- when to call retain
- how to inject recall results
- how to apply policy filters
- how to measure failures and latency

`cllama` should not know:

- how to embed text
- how to rank memories
- how to build graphs
- how to infer affect
- how to compact or summarize
- which vendor algorithm is best

### 4. Memory intelligence must be swappable

Clawdapus should make it possible to plug in:

- mem0
- supermemory
- graph-based memory systems
- local pgvector/Qdrant/Chroma implementations
- simple rolling-summary stores
- domain-specific memory engines

without requiring:

- runner changes
- new proxy code for each backend
- store migration to a Clawdapus-owned schema

### 5. One memory relationship per agent

An agent should subscribe to one memory service, not to an arbitrary list of memory backends.

If an operator wants a layered strategy, such as:

- raw retention
- semantic recall
- graph memory
- periodic summarization

that should be composed behind one memory service boundary.

This keeps the Clawdapus surface simple and avoids exploding the agent-facing memory model.

### 6. Recall should return derived state, not transcript tails

The memory plane should optimize for:

- stable facts
- commitments
- episodic summaries
- project state
- relevant long-range context

not for "last N messages."

If a backend cannot produce anything more useful than recent transcript slices, it should not yet be in the hot path.

### 6.5. Hot-path latency must be budgeted aggressively

Recall runs in the inference hot path, so it must be treated like an expensive privilege, not a free convenience.

That implies:

- strict short timeouts
- no automatic retries on the hot path
- graceful degradation when recall fails
- explicit per-agent opt-in
- bounded payload size

If a backend cannot return useful derived state within the allowed budget, it should not be enabled for synchronous recall.

### 7. Governance must apply to memory traffic too

Memory is a cognitive surface.

That means the same infrastructure that governs model traffic should be able to:

- scrub sensitive data before retention
- redact or suppress recalled content before reinjection
- forget or purge retained content when policy requires it

This is one of the strongest reasons not to leave memory solely inside runners.

### 7.5. Forget must be compatible with an append-only ledger

The raw ledger should remain append-only.

That means a governed forget operation should not rewrite `history.jsonl` in place.

Instead, forgetting should eventually work through:

- deletion in the external memory backend
- a tombstone or redaction sidecar ledger owned by infrastructure
- replay and backfill logic that honors those tombstones and does not re-ingest forgotten material

The goal is:

- preserve auditability of the raw retention substrate
- prevent forgotten content from re-entering derived memory on a later rebuild or backfill

### 8. Compile-time wiring, not runtime self-registration

The memory relationship should be declared in pod YAML and compiled by `claw up`, just like feeds and tools.

No runtime plugin discovery.
No runner-specific boot-time registration.
No hidden self-attachment logic.

---

## The Memory Pipeline

The proposed memory plane has four stages.

### Stage 1: Capture

`cllama` records every successful inference turn into the durable ledger.

This already exists.

Output:

- append-only `history.jsonl`

### Stage 2: Retain

After a successful turn, `cllama` may send a best-effort structured retention webhook to a configured memory service.

This is an optimization and low-latency trigger, not the source of truth.

If it fails:

- the turn is still durable in `history.jsonl`
- the memory service may catch up later from the ledger

### Stage 3: Process

The memory service performs its own internal work:

- summarization
- salience extraction
- fact extraction
- embeddings
- graph linking
- deduplication
- affect tagging
- decayed ranking updates

This stage is entirely outside `cllama`.

### Stage 4: Recall

Before forwarding the next model request upstream, `cllama` may query the memory service for relevant derived context and inject the returned memory blocks into the prompt.

This is synchronous and bounded:

- timeout-controlled
- size-capped
- policy-filtered

This is where memory affects model behavior.

---

## What Clawdapus Should Own

Clawdapus should own the shared contract and lifecycle hooks.

### A. The raw ledger

Already implemented:

- one normalized history stream per agent
- outside `.claw-runtime`
- durable across restarts and `claw up`

### B. The memory relationship declaration

At pod level and/or service level, operators should be able to declare:

- which memory service an agent uses
- whether recall is enabled
- whether retain webhook is enabled
- bounded hot-path knobs such as timeouts and recall-context size

### C. Compile-time wiring

`claw up` should:

- validate that the referenced memory service exists
- inspect its descriptor
- resolve URLs and auth
- project per-agent memory config into context
- mount the needed runtime files into `cllama`

### D. The request lifecycle hooks

`cllama` should:

- call recall before the upstream LLM request
- inject returned memory blocks into the prompt
- call retain after a successful turn
- log memory hook failures and latency

### E. Governance hooks

`cllama` should be able to:

- scrub retained content before forwarding to memory service
- redact recalled content before injecting it
- support a future governed `forget` action

### F. Observability

We should be able to answer:

- did recall run?
- how long did it take?
- did it time out?
- how many bytes were injected?
- how many blocks were returned?
- did policy remove any blocks?
- did retain webhook fail?

### G. A minimal reference implementation

Clawdapus should eventually ship a small reference memory service image that proves the contract end-to-end.

This reference is not meant to be state of the art. It is meant to:

- validate the contract
- provide spike coverage
- offer a baseline for operators

---

## What Clawdapus Should Not Own

### 1. A universal memory algorithm

Clawdapus should not define:

- the one true salience metric
- the one true embedding model
- the one true summary format
- the one true graph extraction strategy

### 2. Vendor-specific backend semantics

Clawdapus should not hardcode:

- mem0 APIs
- supermemory APIs
- Graphiti semantics
- Qdrant schema assumptions
- Chroma collection naming

### 3. Per-runner memory plugins as the primary path

Runners may still offer native memory tools or plugins, but those should not be the architecture Clawdapus depends on.

### 4. Memory store internals

Clawdapus should not care whether a backend uses:

- SQLite
- JSONL
- Postgres + pgvector
- Qdrant
- graph DBs
- hybrid layers

as long as it obeys the stable service contract.

---

## Proposed User-Facing Model

The agent should declare one memory service relationship.

Suggested shape:

```yaml
x-claw:
  memory-defaults:
    service: claw-memory
    timeout-ms: 300

services:
  analyst:
    x-claw:
      agent: ./agents/ANALYST.md
      memory:
        service: claw-memory
```

Notes:

- `memory` should be an object, not only a scalar.
- We may support scalar sugar later, but the compiled model should be object-shaped.
- One memory service per agent is intentional.
- Simple payload-shaping bounds should begin as implementation defaults rather than as a large operator-facing knob surface.

This is deliberately modest. The operator is declaring:

- who the memory provider is
- how much hot-path budget is available

The operator is not trying to teach Clawdapus how memory works internally.

---

## Proposed Descriptor Extension

The current descriptor should gain an optional memory capability section in the next descriptor version line.

This plan must not create a second incompatible `claw.describe` version `2`.

ADR-020 already drafts descriptor version `2` for tools. Memory must therefore do one of the following:

- fold into the same `version: 2` descriptor expansion as tools
- or, if it lands later and cannot be merged cleanly, become `version: 3`

There must not be two competing meanings for descriptor version `2`.

Example:

```json
{
  "version": 2,
  "description": "Shared memory service with semantic recall and durable turn retention.",
  "memory": {
    "recall": {
      "path": "/recall"
    },
    "retain": {
      "path": "/retain"
    },
    "forget": {
      "path": "/forget"
    }
  },
  "auth": {
    "type": "bearer",
    "env": "CLAW_MEMORY_TOKEN"
  }
}
```

Notes:

- `forget` is optional.
- The descriptor does not declare ranking semantics or embedding behavior.
- The descriptor does not negotiate a request vocabulary.
- The service receives a fixed bounded payload and ignores what it does not need.

This matches the current Clawdapus style:

- provider declares capability
- consumer subscribes by service
- `claw up` compiles the projection

---

## Proposed Runtime Manifest

`claw up` should compile a new per-agent manifest:

```text
/claw/context/<agent-id>/memory.json
```

This mirrors the current:

- `feeds.json`
- `service-auth/`
- future `tools.json`

Suggested shape:

```json
{
  "service": "claw-memory",
  "recall": {
    "url": "http://claw-memory:8080/recall",
    "enabled": true,
    "timeout_ms": 300,
    "max_bytes": 4096,
    "recent_messages": 3,
    "auth": "bearer-token-if-needed"
  },
  "retain": {
    "url": "http://claw-memory:8080/retain",
    "enabled": true,
    "auth": "bearer-token-if-needed"
  },
  "forget": {
    "url": "http://claw-memory:8080/forget",
    "enabled": true,
    "auth": "bearer-token-if-needed"
  }
}
```

This manifest is consumed by `cllama`, not by the runner.

That is important:

- no runner plugin system required
- no per-runner memory client code
- no duplication across drivers

---

## Proposed Wire Contracts

The wire contract should be deliberately small.

### 1. Recall

`cllama` sends a fixed request body to the memory service.

Suggested request:

```json
{
  "agent_id": "analyst-0",
  "pod": "trading-desk",
  "ts": "2026-03-30T15:04:05Z",
  "request_path": "/v1/chat/completions",
  "requested_model": "anthropic/claude-sonnet-4",
  "messages": [
    {"role":"assistant","content":"..."},
    {"role":"user","content":"..."},
    {"role":"user","content":"..."}
  ],
  "metadata": {
    "timezone": "America/New_York"
  }
}
```

Notes:

- `messages` is bounded only by simple numeric limits such as recent message count or byte cap.
- The payload is intentionally generic.
- The memory service may ignore fields it does not need.

Suggested response:

```json
{
  "blocks": [
    {
      "kind": "profile",
      "text": "Operator prefers concise summaries and dislikes speculative tone.",
      "source": "user-profile",
      "score": 0.93,
      "ts": "2026-03-28T12:00:00Z"
    },
    {
      "kind": "commitment",
      "text": "Open action: finalize the migration ADR and reconcile model-policy docs drift.",
      "source": "episodic-summary",
      "score": 0.88,
      "ts": "2026-03-29T19:00:00Z"
    }
  ],
  "ttl_seconds": 30
}
```

`cllama` then:

- applies policy filtering
- formats the returned blocks into a bounded injected context block
- prepends that block into the outbound LLM request

### 2. Retain

Retain should be best-effort and should happen after a successful turn.

Suggested request:

```json
{
  "agent_id": "analyst-0",
  "pod": "trading-desk",
  "entry": {
    "version": 1,
    "ts": "2026-03-30T15:04:05Z",
    "claw_id": "analyst-0",
    "path": "/v1/chat/completions",
    "requested_model": "anthropic/claude-sonnet-4",
    "effective_provider": "anthropic",
    "effective_model": "claude-sonnet-4",
    "status_code": 200,
    "stream": false,
    "request_original": {},
    "request_effective": {},
    "response": {},
    "usage": {}
  }
}
```

Notes:

- The ledger remains the durable truth regardless of webhook outcome.
- The memory service may process this immediately or queue it internally.
- The retain contract deliberately reuses the normalized session-history entry rather than inventing a second event shape.

### 3. Forget

`forget` is optional and should be treated as a governance operation, not a normal runner capability.

Suggested request:

```json
{
  "agent_id": "analyst-0",
  "scope": {
    "from": "2026-03-01T00:00:00Z",
    "to": "2026-03-30T00:00:00Z"
  },
  "reason": "policy_redaction"
}
```

We should not overdesign this early. The important point is that the service contract leaves room for a governed deletion path.

---

## Request Lifecycle in `cllama`

The hot-path and non-hot-path behavior should be explicit.

### Pre-turn recall path

For every proxied inference request:

1. resolve the agent identity as usual
2. load `memory.json` if present
3. if recall is enabled:
   - build the bounded recall request
   - call the memory service with a short timeout
   - parse returned blocks
   - apply policy filters
   - inject the resulting memory block into the prompt
4. continue the normal upstream request flow

Failure behavior:

- timeout: continue without memory
- 5xx from memory service: continue without memory
- malformed response: continue without memory

The memory plane should degrade gracefully. It must not become a single point of total inference failure by default.

### Read-after-write semantics

The memory plane should not promise strong read-after-write consistency for rapid-fire turns.

That is acceptable because:

- runner sessions already cover immediate recency
- the memory plane is for durable derived state, not for replacing the live conversation window
- the retain webhook is best-effort and asynchronous by design

In practice this means:

- the next turn may run recall before the previous turn has been fully processed by the backend
- the system remains correct because immediate continuity still comes from the runner session
- the memory plane improves medium- and long-range continuity, not single-turn echoing

### Post-turn retain path

After a successful upstream completion:

1. write the normalized entry to the ledger as usual
2. if retain is enabled:
   - dispatch a best-effort webhook to the memory service
   - do not block the response already returning to the runner

The retain webhook may fail silently except for observability and alerting. Recovery comes from the ledger.

---

## What Counts As Real Memory Recall

This is the product line we should draw explicitly.

The recall layer is worthwhile when it returns:

- durable facts
- user/operator preferences
- open loops and commitments
- prior decisions and rationale
- episodic summaries
- project state
- relevant older context outside the runner session window

The recall layer is not yet worthwhile if it mostly returns:

- the last few turns
- transcript tails that the runner already has
- an unprocessed dump of recent messages

This distinction matters because it keeps the design honest.

If the memory service is not adding meaningful abstraction over the live session window, it is not yet justifying hot-path latency.

---

## Session Stitching

Session stitching will come up quickly, but it should not be treated as a gating prerequisite.

There are three levels:

### Level 1: No stitching

The service processes the full ledger keyed by agent identity and whatever surface metadata is already available.

This is still useful for:

- durable facts
- recurring preferences
- high-level commitments

### Level 2: Soft stitching

The service groups events by obvious metadata when it exists, such as:

- DM peer
- thread ID
- channel ID
- task ID
- repo or project hints

This is likely enough for early "resume where we left off" quality.

### Level 3: Hard stitching

The service infers continuity across fragmented contexts and restarts even when metadata is weak.

This is valuable, but should remain a backend problem.

Clawdapus does not need to solve stitching globally in order to provide a good memory plane.

---

## Governance Model

Memory traffic should be governable in both directions.

### Retention governance

Before retain webhook delivery, `cllama` may:

- remove secrets
- redact known sensitive patterns
- suppress content classes from retention entirely

### Recall governance

Before reinjection, `cllama` may:

- remove restricted content
- suppress blocks from disallowed sources
- cap categories or sizes
- redact content that now violates stricter policy than when it was originally retained

### Forget governance

The operator or a future Master Claw should be able to trigger targeted forgetting through a governed path.

This is one of the strongest arguments for making memory a first-class infra surface rather than only a runner convenience.

Forget must also be compatible with an append-only ledger.

That implies a future forget implementation should likely include:

- deletion in the external memory backend
- an infra-owned tombstone or redaction ledger
- backfill and replay logic that honors those tombstones and does not re-ingest forgotten material

---

## Persistence Model

The memory service itself is a normal compose service.

That means its persistence model should be the same as other stateful pod services:

- named volumes
- bind mounts
- external databases

`claw up` and `claw down` should not destroy those stores unless the operator explicitly destroys them through normal container lifecycle actions.

This is much cleaner than runner-local plugin stores because:

- store lifetime is independent from the runner container
- one memory engine can serve many agents
- state can survive `CLAW_TYPE` migrations

---

## Backfill And Replay

Backfill should be treated as a first-class operation, not as an implementation detail.

If a new memory backend is introduced after months of retained history already exist, the operator must be able to populate it from the ledger deterministically.

The architecture should therefore assume a future explicit backfill path, likely involving:

- a `cllama` history read API suitable for replay consumers
- a dedicated CLI flow such as `claw memory backfill`
- stable source-event IDs plus backend dedupe so the same ledger can be consumed safely more than once

The first two now exist in-tree:

- `cllama` exposes a scoped history read API
- subscribed memory services receive dedicated replay token plus history URL projection
- operators can trigger replay with `claw memory backfill`
- history entries now carry stable source-event IDs, and legacy entries are hydrated with those IDs on read/export/replay

The retain webhook is the low-latency path. Backfill is the durability path for new or recovering services.

The current CLI shape is deliberately pragmatic:

- it uses the local immutable ledger as the replay source
- it replays through the memory service's declared `retain` endpoint
- it auto-discovers a host URL when the service publishes a host port
- it allows `--url` override when the service is reachable some other way

That is enough to make replay operational today without adding a second memory-specific control plane. A future backend-native replay trigger may still be worth adding later.

---

## Relationship to Runner-Native Memory

Runners may continue to provide native memory tools or session systems.

That is acceptable, but it should not be the infrastructure dependency.

The intended architecture is:

- runner-native session and short-term working memory remain local concerns
- infrastructure memory provides durable, governed, cross-session recall

In practice, once an agent is behind `cllama` and subscribed to a memory service, many runner-native memory tools may become redundant.

That redundancy is tolerable in principle but dangerous in practice.

If the infrastructure plane injects memory context while the runner also injects its own memory context, the agent may see:

- duplicate facts
- contradictory summaries
- repeated commitments
- different privacy or forgetting policies

So the operational recommendation should be:

- when using the infrastructure memory plane, operators should disable runner-native memory plugins or memory-search tools where practical
- Clawdapus should document that guidance clearly
- Clawdapus should not attempt to force-disable runner behavior generically across all runners

Clawdapus should not attempt to disable runner-native memory features globally. It should provide a better shared path.

---

## Recommended Phase Plan

### Milestone 1: Complete ADR-018 Phase 2 and define backfill

Status: functionally complete on this branch, with scaling work and backend dedupe guidance still open.

Add the self-scoped history read surface to `cllama`.

Benefits:

- memory services can consume normalized history through a stable proxy-owned interface
- backfill does not require filesystem coupling
- future operators and tools gain a consistent introspection surface
- replay becomes a first-class lifecycle rather than an implicit recovery hack

This milestone should also define the expected operational backfill flow for new or recovering memory services.

Current branch status:

- `GET /history/{agentID}` exists as the scoped history read surface
- subscribed memory services receive dedicated replay credentials
- `claw memory backfill` provides an operator-facing replay path today
- replay currently uses the local immutable ledger as its source and the memory service's `retain` endpoint as its sink
- a lightweight `history.index.json` checkpoint sidecar now lets `GET /history`, `claw history export`, and `claw memory backfill --after` seek near the requested timestamp without always rescanning the whole ledger

What remains here is mostly operator guidance and backend contract hardening:

- backend dedupe guidance on top of the stable source-event ID contract

### Milestone 2: Add the memory capability and `cllama` hooks

Status: functionally complete on this branch, with governance hardening still open.

Implement:

- descriptor extension for memory capability
- `x-claw.memory`
- pod defaults for memory
- `memory.json` compilation
- auth projection for memory services
- pre-turn recall call
- bounded injection
- post-turn retain webhook
- graceful degradation
- memory-specific observability events

This is the first full end-to-end memory plane.

The main remaining gaps are:

- policy filtering on retain and recall
- any optional payload-bounding refinements beyond the current fixed request shape

### Milestone 3: Reference adapter and governance hardening

Implement:

- retain-side filtering
- recall-side filtering
- alerting for repeated memory-service failures

Provide a small baseline image, likely:

- Go-based service
- durable SQLite or JSONL storage
- rolling summaries
- simple fact extraction
- simple BM25 or similarly boring local ranking
- no vendor-specific dependencies required to prove the contract

This reference should be intentionally modest. The point is to validate the contract, not to define the state of the art.

---

## Candidate File Map

This is not a full implementation checklist, but it identifies the likely change surface.

### Main repo

- `internal/describe/descriptor.go`
- `internal/describe/registry.go`
- `internal/pod/types.go`
- `internal/pod/parser.go`
- `cmd/claw/compose_up.go`
- `internal/cllama/context.go`
- `internal/pod/compose_emit.go`
- `docs/CLLAMA_SPEC.md`
- `docs/decisions/021-memory-plane-and-pluggable-recall.md`

### cllama submodule

- `cllama/internal/proxy/handler.go`
- `cllama/internal/agentctx/...`
- new memory manifest loader package or extension of existing context loading
- logging and audit additions

---

## Open Questions

These are important, but they should not block the core architecture.

### 1. How much request context should recall receive?

The proxy should send a fixed request shape with only simple numeric payload bounds such as:

- last N messages
- max request bytes

We should avoid a richer negotiated vocabulary here unless real implementations prove it necessary.

Current implementation note:

- the branch currently sends the full inbound `messages` payload and, for Anthropic requests, the top-level `system` field
- this is acceptable as a first implementation because the service may ignore what it does not need
- if payload size becomes a practical problem, bounded recent-context shaping can be added later without changing the core contract
- this means the architecture is implemented, but one of the intended payload-tightening refinements remains open

### 2. Should recall responses support categories?

Probably yes, eventually.

Possible categories:

- `profile`
- `commitment`
- `decision`
- `episode`
- `state`

But the first version can simply accept opaque blocks with optional metadata.

### 3. Should `cllama` cache recall results?

Maybe, but not initially.

Recall is more query-shaped than feeds. A poor cache may create incorrect reuse and hide backend problems.

### 4. Should retain delivery be in-process async or delegated to a queue?

For the first implementation, best-effort in-process dispatch is likely enough because the ledger is the real durability mechanism.

### 5. Should the memory service read the ledger directly or through an API?

Long-term, the stable read API is cleaner.

Short-term, direct ledger reading may be acceptable for local prototypes.

Current implementation note:

- the stable read API now exists
- subscribed memory services also receive a dedicated replay auth projection
- direct filesystem scraping should therefore be treated as a prototype shortcut, not as the intended supported path

### 6. How should affect fit into the model?

Affect is exactly the kind of advanced derived state that should remain backend-defined.

Clawdapus should make it possible, not standardize it early.

### 7. How should multi-agent sharing work?

The first version should assume private per-agent recall by default.

Shared or world memory should require explicit backend semantics and likely future policy controls for:

- agent-private memory
- pod-shared memory
- operator-defined namespaces

This is important, but not required to define the initial memory plane.

### 8. What metadata can the proxy reliably provide for stitching?

Today the proxy may not always have a canonical thread or session identifier across all runners and providers.

The first version should therefore treat:

- `agent_id`
- `pod`
- recent conversation context
- whatever stable metadata is already present

as the minimum recall input.

Current implementation note:

- the branch currently forwards full inbound request messages as that recent conversation context
- richer stitching metadata is still future work

Richer stitching metadata may require later surface-specific propagation through headers, request bodies, or runner config.

---

## Non-Goals

This plan does not propose:

- replacing runner-native sessions
- collapsing portable memory into proxy-owned memory
- mandating one storage engine
- defining a canonical embedding model
- defining a canonical graph schema
- forcing all runners to adopt a common memory plugin
- making `cllama` itself a memory database
- exposing vendor-specific memory tools directly to agents by default

---

## Decision Shape Captured In ADR-021

ADR-021 now captures the main architectural decisions this plan was arguing for:

1. Memory is a first-class Clawdapus plane with compile-time wiring.
2. `cllama` owns pre-turn recall orchestration and post-turn retain orchestration.
3. Session history remains the immutable ledger and source of truth.
4. Portable memory remains runner-owned and separate.
5. Memory intelligence lives in pluggable services, not in `cllama`.
6. Agents subscribe to one memory service relationship at a time.
7. Recall should optimize for derived durable state, not transcript tails.

---

## Recommended Next Step

The next implementation work should be split explicitly by ADR:

- ADR-020 runtime phase:
  - load `tools.json` in `cllama`
  - inject managed tools into upstream requests
  - intercept and execute mediated tool calls
  - record mediated tool rounds in history/audit output
  - either fail fast for non-`cllama` capability consumers or add a real native projection path
- ADR-021 later follow-through:
  - improve retrieval quality and ranking beyond the boring reference adapter
  - add backend-specific recall heuristics only where they do not change the core contract
