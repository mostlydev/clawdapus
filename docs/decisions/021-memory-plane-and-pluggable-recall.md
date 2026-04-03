# ADR-021: Memory Plane as a Compiled Capability

**Date:** 2026-03-30
**Status:** Draft
**Depends on:** ADR-017 (Pod Defaults and Service Self-Description), ADR-018 (Session History and Persistent Memory Surfaces), ADR-020 (Compiled Tool Plane with Native and Mediated Execution Modes)
**Amends:** ADR-018 (defines the derived retrieval plane), ADR-020 (extends the canonical capability IR)
**Implementation:** Plan: docs/plans/2026-03-30-memory-plane-and-pluggable-recall.md

## Context

ADR-018 established the substrate for infrastructure-owned retention:

- `cllama` writes normalized per-agent session history
- session history and runner-owned `/claw/memory` are separate surfaces
- Phase 2 (scoped read API) and Phase 3 (derived retrieval) were deferred

ADR-020 establishes the next major pattern:

- services self-describe through `claw.describe`
- `claw up` compiles per-agent manifests
- `cllama` may mediate request-time behavior from those manifests
- the backend implementation remains external

The open question is how memory fits this model.

Memory clearly resembles ADR-020's compiled capability flow:

- it should be declared by a self-described service
- the consumer should subscribe in pod YAML
- `claw up` should compile per-agent manifests
- `cllama` should orchestrate request-time behavior
- backend logic should remain external and swappable

But memory is not the same kind of capability as either feeds or tools:

- **Feeds** are query-agnostic live context with TTL semantics.
- **Tools** are model-invoked callable operations.
- **Memory** needs both a synchronous pre-turn recall path and an asynchronous post-turn retain path, both initiated by infrastructure rather than by the model.

If memory is forced into the feed shape, recall becomes query-blind and loses most of its value.

If memory is forced into the tool shape, recall becomes opt-in at the model layer and loses the reliability that makes it infrastructure-worthy.

The right question is therefore not "is memory a feed or a tool?"

The right question is: "how does memory extend the same compiled-capability architecture without pretending to be a different lifecycle than it is?"

## Decision

### 1. Memory is a first-class capability in the same compiled model as feeds and tools

ADR-020's architectural pattern is the right one:

- declare capability in `claw.describe`
- subscribe in pod YAML
- compile per-agent runtime artifacts
- let `cllama` mediate request-time behavior

Memory follows that exact pattern.

It is therefore part of the same compiled capability model as feeds and tools.

It is not a plugin universe, not a runner-local convention, and not a special one-off side channel.

### 2. Memory is a sibling capability, not a subtype of feeds or tools

The canonical capability model is extended from:

- `tools[]`
- `feeds[]`
- `skill`
- `endpoints[]`

to:

- `tools[]`
- `feeds[]`
- `memory`
- `skill`
- `endpoints[]`

The distinction is lifecycle, not importance.

| Capability | Primary purpose | Trigger | Query-aware | Typical artifact | Runtime owner |
|---|---|---|---|---|---|
| `feeds[]` | Ambient live context | Service/polling cadence | No | `feeds.json` | `cllama` fetch + inject |
| `tools[]` | Explicit callable operations | Model tool call | Yes, via arguments | `tools.json` or runner config | `cllama` (`mediated`) or runner (`native`) |
| `memory` | Derived durable context and retention hooks | Infra lifecycle | Yes | `memory.json` | `cllama` |

Feeds tell the model what is happening now.

Tools let the model do something on purpose.

Memory lets infrastructure retain derived continuity and re-surface it automatically.

### 3. Memory is `mediated` by definition in v1

ADR-020 distinguishes `native` and `mediated` execution for tools.

Memory does not follow that split in the same way.

For the ambient memory plane, Clawdapus only supports the mediated model:

- `cllama` orchestrates recall before the upstream inference request
- `cllama` dispatches retain after the request completes
- `cllama` applies governance filters on both directions

There is no runner-native equivalent that preserves the trust boundary, compile-time determinism, and cross-runner portability that motivate this feature.

This does **not** mean memory services can never expose tools.

A memory service may also declare ordinary `tools[]`, such as:

- `search_memory`
- `pin_fact`
- `forget_memory`
- `list_open_commitments`

Those explicit operations live on the tool plane and follow ADR-020 normally.

But ambient recall and retain are part of the memory plane, not the tool plane.

### 4. ADR-020's descriptor version should be treated as the umbrella capability version

ADR-020 already proposes `claw.describe` `version: 2`.

Because ADR-020 is still draft and unimplemented, this ADR amends its interpretation:

`version: 2` is the umbrella schema version for compiled service capabilities, not a tools-only bump.

A `version: 2` descriptor may therefore include any combination of:

- `feeds[]`
- `tools[]`
- `memory`
- `skill`
- `endpoints[]`

This avoids pointless schema churn where tools land as one `v2` and memory immediately forces a second incompatible revision for the same implementation wave.

If ADR-020 were to ship first exactly as currently written, then memory would need either:

- an explicit amendment to ADR-020 before implementation, or
- a `version: 3` descriptor bump

The preferred path is to avoid that split and treat `v2` as the shared capability-evolution step.

### 5. The memory capability is declared by providers and subscribed to by consumers

Memory follows the same provider-owns, consumer-subscribes rule as feeds and tools.

A service declares memory capability in its descriptor.

An agent subscribes to exactly one memory relationship in pod YAML.

That relationship points to one service boundary, even if the service internally layers multiple strategies such as:

- semantic retrieval
- graph memory
- rolling summaries
- periodic consolidation

Clawdapus should not expose an arbitrary stack of memory backends directly to one agent.

### 6. The memory descriptor is small and lifecycle-shaped

The provider descriptor adds an optional `memory` object:

```json
{
  "version": 2,
  "description": "Derived memory service",
  "memory": {
    "recall": { "path": "/recall" },
    "retain": { "path": "/retain" },
    "forget": { "path": "/forget" }
  },
  "auth": { "type": "bearer", "env": "MEMORY_API_TOKEN" }
}
```

Notes:

- `recall` is required when a service wants to participate in hot-path context injection
- `retain` is required when a service wants low-latency processing of new turns
- `forget` is optional and reserved for governed operations

The descriptor does **not** negotiate a semantic vocabulary for recall inputs.

`cllama` sends a fixed payload shape with only simple numeric bounds configured at compile time.

The service ignores fields it does not need.

### 7. Pod subscription is explicit and singular

The consumer surface in pod YAML is an explicit memory relationship:

```yaml
x-claw:
  memory:
    service: team-memory
    timeout-ms: 300
```

Pod-level `memory-defaults` follows the normal defaults model.

Service-level declaration overrides the default unless `...`-style list composition is later proven necessary.

For memory, the default expectation is one relationship, not list composition.

V1 should keep this operator surface small.

Simple numeric shaping such as recent-window size, request byte caps, and injected byte caps should begin as implementation defaults rather than as a large user-facing knob surface.

### 8. `claw up` compiles a dedicated per-agent `memory.json`

Memory follows ADR-020's compile pipeline:

| Step | Feeds | Tools (`mediated`) | Memory |
|---|---|---|---|
| Descriptor declares | `feeds[]` | `tools[]` | `memory` |
| Consumer policy | `feeds:` subscription | `tools:` allowlist | `memory:` relationship |
| Artifact written | `feeds.json` | `tools.json` | `memory.json` |
| Runtime consumer | `cllama` feed fetcher | `cllama` mediator | `cllama` recall/retain orchestrator |

`memory.json` is per-agent because:

- auth is per agent
- the subscribed service is per agent
- future policy and observability may differ per agent

The manifest shape should be simple:

```json
{
  "version": 1,
  "service": "team-memory",
  "base_url": "http://team-memory:8080",
  "recall": {
    "path": "/recall",
    "timeout_ms": 300
  },
  "retain": {
    "path": "/retain"
  },
  "forget": {
    "path": "/forget"
  },
  "auth": {
    "type": "bearer",
    "token": "resolved-token-value"
  }
}
```

Auth resolution follows the same order as ADR-020 mediated tools and existing feeds:

- projected per-agent service credential when available
- otherwise descriptor-declared auth when that fallback is valid

Memory should not invent a second auth model.

The implementation may still compile default bounds into the runtime config, but those should begin as internal defaults, not as a large operator-facing contract.

### 9. The memory plane has three distinct operations

#### Recall

Recall is synchronous and hot-path:

1. `cllama` authenticates the agent as usual
2. `cllama` loads `memory.json`
3. `cllama` builds a bounded recall payload from the current request
4. `cllama` calls the memory service
5. `cllama` filters and injects the returned blocks
6. `cllama` forwards the enriched request upstream

Recall exists to surface **derived durable state**, not transcript tails.

If recall fails, the request continues without memory by default.

At the contract level, recall has a fixed shape:

- request carries agent identity, pod identity, basic request metadata, and the latest user message plus a small bounded recent context window
- response carries a bounded list of text blocks with optional metadata such as `kind`, `source`, `score`, and `ts`

The exact JSON can evolve, but that shape is architectural.

The recent context window is an implementation default in v1, not a large declarative vocabulary and not an operator tuning surface unless real usage proves it necessary.

Injection is provider-format-aware.

The implementation must resolve how the same logical memory block is rendered for:

- OpenAI-style `messages[]` requests
- Anthropic-style requests with top-level `system` handling

This ADR does not prescribe the exact injection primitive, but it does require one bounded logical memory block that is inserted consistently across both request families.

#### Retain

Retain is asynchronous and best-effort:

1. the normalized session-history entry is appended to the ledger
2. `cllama` dispatches that same normalized entry to the memory service
3. failures are observed but do not fail the already-completed inference request

Retain exists to reduce freshness lag.

It does not replace ledger durability.

#### Forget

Forget is governed and optional:

- it is not a normal runner capability
- it exists for operator policy, future Master Claw workflows, and backfill hygiene

Forget applies to the external memory backend and to replay behavior.

It does **not** justify mutating the append-only ledger in place.

Instead, forgetting requires tombstone or redaction metadata that future replay and backfill paths honor.

Retain idempotency is keyed by the stable session-history `entry.id` within an agent scope:

- live retain and later replay/backfill may deliver the same `entry.id` more than once
- duplicate retain for the same `(agent_id, entry.id)` must be a no-op rather than a second derived memory row
- forget tombstones must suppress later recall of that `entry.id`
- replay/backfill of a tombstoned `entry.id` must remain a no-op rather than resurrecting forgotten material

### 10. Memory traffic must be observable

Memory mediation is part of the governed request path and must emit structured telemetry.

At minimum, the implementation should record:

- whether recall was attempted, skipped, succeeded, timed out, or failed
- recall latency
- number of blocks returned
- number of blocks removed by policy
- injected byte count
- whether retain delivery succeeded or failed
- retain delivery latency

This should align with the existing structured logging and audit direction rather than inventing a separate unstructured debug path.

### 11. Session history remains the substrate and source of truth

This ADR does not change ADR-018's ownership boundary.

The roles become:

- `history.jsonl`: immutable ledger, audit substrate, replay substrate
- memory service: derived state, indexing, summarization, salience, ranking
- `cllama`: orchestration, policy filtering, hot-path injection, best-effort delivery
- runner `/claw/memory`: local scratchpad and portable runner-owned state

This means memory quality can improve radically over time without changing the retention substrate.

### 12. Backfill is first-class, not a repair hack

The retain webhook is only the low-latency path.

A memory service must also be able to build or rebuild from the ledger.

This requires:

- ADR-018 Phase 2 style scoped history read access
- a future explicit replay or backfill flow
- replay semantics that honor forget tombstones

Without backfill, the retain webhook is merely a convenience.

With backfill, memory services become truly swappable.

ADR-018 Phase 2 style scoped history read access is therefore a prerequisite for the first supported rollout of the memory plane.

A local prototype may read ledger files directly, but that is not sufficient for the supported, swappable, runner-agnostic memory plane this ADR defines.

### 13. Memory is not the same as runner session continuity

The runner still owns immediate conversational recency.

The memory plane is deliberately not a strong read-after-write substitute for the runner's live session window.

That is acceptable because the memory plane is for:

- cross-session continuity
- durable facts
- older episodic summaries
- decisions and commitments
- long-range project state

not for replaying the last few raw turns back into the model.

### 14. Operators should prefer one ambient memory plane

When the infrastructure memory plane is enabled, runner-native memory injection and runner-native memory-search tools may become redundant or actively conflicting.

If `cllama` injects governed memory context while the runner also injects its own memory context, the agent may receive:

- duplicate facts
- contradictory summaries
- repeated commitments
- mismatched privacy or forgetting policy

The operational guidance should therefore be:

- prefer the infrastructure memory plane as the single ambient recall mechanism
- disable runner-native memory plugins or memory injection where practical when using the infrastructure plane
- do not attempt generic forced disablement across all runners from Clawdapus itself

Clawdapus should document this overlap explicitly rather than treating it as a purely neutral coexistence case.

## Rationale

### Why not model memory as a feed?

Feeds are the wrong shape:

- they are query-agnostic
- they are naturally TTL-cached
- they represent live service state, not derived continuity

Memory recall needs the current request as input.

If it does not, it is usually not doing real recall.

### Why not model memory as a tool?

Tool-based memory search is useful, but it is not sufficient as the infrastructure plane.

If recall depends on the model deciding to call a tool:

- reliability becomes model-dependent
- runners without shared tool hosting lose parity
- cross-runner portability collapses back toward runner plugins

Explicit memory tools are a complement, not the substrate.

### Why keep memory separate from runner-owned `/claw/memory`?

The ownership boundary from ADR-018 is still correct.

Runner memory is agent-authored and writable.

Infrastructure memory is operator-governed and proxy-mediated.

Collapsing them would blur authority and make replay, redaction, and audit much harder.

### Why no `native` memory mode?

Ambient memory recall is valuable precisely because it is reliable, governed, and runner-agnostic.

If runners each implement their own retain and recall path:

- persistence becomes runner-coupled
- backend stores fragment
- cross-runner continuity regresses
- policy enforcement becomes inconsistent

That is the failure mode this ADR exists to avoid.

### Why a dedicated `memory.json` instead of folding everything into one manifest?

Today the codebase already uses small dedicated per-agent artifacts:

- `metadata.json`
- `feeds.json`
- `service-auth/*.json`

ADR-020 adds `tools.json`.

Adding `memory.json` is consistent with that pattern and avoids prematurely inventing a generic super-manifest before the capability shapes stabilize.

A future manifest unification is possible, but not required to land the architecture cleanly.

## Consequences

**Positive:**

- Memory fits the same declare -> compile -> mediate architecture as feeds and tools.
- Memory vendors remain swappable behind one stable contract.
- Cross-runner continuity no longer depends on runner-native plugins or per-runner databases.
- Governance applies to both retention and recall traffic.
- Backfill and replay become first-class concerns rather than an afterthought.
- The descriptor and context changes can be made once, alongside ADR-020, instead of in two conflicting passes.

**Negative:**

- `cllama` gains another hot-path responsibility and must budget latency tightly.
- The capability model becomes broader: operators must understand feeds, tools, and memory as related but distinct surfaces.
- Runner-native memory systems may overlap or conflict with the infrastructure plane and require operator discipline.
- Because memory is mediated-only in v1, there is no short path that reuses runner-local memory plumbing.

**Neutral:**

- A memory service may expose both `memory` and `tools[]`; these are complementary, not duplicative.
- This ADR does not standardize embeddings, ranking, graph schemas, or salience logic.
- This ADR does not require immediate implementation of shared or cross-agent memory namespaces.

## Implementation Direction

To avoid shape churn, ADR-020 and this ADR should be implemented as one descriptor/context evolution wave.

The practical order is:

1. extend `internal/describe.ServiceDescriptor` for `version: 2` capability parsing
2. add the new user-facing pod grammar for tools and memory together
3. extend `internal/cllama.AgentContextInput` and manifest generation once
4. implement ADR-018 Phase 2 history read/backfill substrate
5. implement `memory.json` compilation and `cllama` recall/retain hooks
6. implement `tools.json` mediation and any shared manifest/auth helpers that fall out of the work

The important point is not the exact file order.

The important point is to avoid implementing ADR-020 as if tools are the only future compiled capability and then immediately refactoring the same surfaces again for memory.

The first supported end-to-end checkpoint is after steps 4 and 5:

- one self-described memory service can be wired to one agent
- recall injects derived blocks in the request path
- retain delivers normalized entries post-turn
- replay/backfill is supported through the scoped history surface

Without that checkpoint, the system may be an interesting prototype, but it is not yet the supported memory plane defined by this ADR.
