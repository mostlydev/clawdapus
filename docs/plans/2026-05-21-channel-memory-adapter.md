# Channel-Memory Adapter For Clawdapus Phase 2

Date: 2026-05-21
Status: Upstream Clawdapus design draft

This is the Phase 2 design companion for
`docs/plans/2026-05-12-issue-232-channel-24h-awareness.md`.

## Prerequisite (#232 Phase 1 follow-up)

Phase 2 cannot land safely without exposing stable Discord message identity in
`channel-awareness` and the retrieval tools. Today the feed emits
`[YYYY-MM-DD HH:MM] author: text` without a message ID; multiple authors can
post in the same minute and chunked reports cross minute boundaries, so
timestamp-only provenance is not a reliable retrieval handle.

Required `channel-awareness` surface change before this plan implements:

- emit Discord message ID (or a stable equivalent handle) per line
- expose the same handle in `search_channel_context` / `get_channel_messages`
  results so digest provenance round-trips
- preserve identity across edits via content-hash (edits do not change ID;
  the handle is the identity)

This is a #232 Phase 1 follow-up, scoped tightly. The channel-memory adapter
in this plan depends on it but does not own it.

## Goal

Complete Clawdapus issue #232 Phase 2 by adding a reusable, source-backed
rolling digest for Discord channel awareness.

This is not a deployment-specific compactor and not a new sibling feed. The
existing Clawdapus contract already has the right surface:

- `channel-awareness` is the room-awareness feed.
- `channel-context` is the cursorized live-continuity feed.
- `search_channel_context` and `get_channel_messages` are the exact-source
  retrieval tools.
- `channel_context_op` telemetry already reports feed/tool channel operations.
- `raw_window+digest` is already reserved as the Phase 2 context kind.
- Phase 1 already emits `digest=unavailable`, which is the explicit placeholder
  this work should fill.

The missing piece is a reusable channel-memory producer, likely
`claw-channel-memory`, that receives retained channel messages, processes them
asynchronously, and returns compact digest blocks with provenance.

## Design Principle

Use the same product model as ADR-021 and the #164 salience-memory adapter:
retention and recall are infrastructure responsibilities, while model-powered
summarization happens off the hot path.

The channel version differs because the source is shared room traffic rather
than per-agent session history. That means:

- claw-wall, not cllama, owns channel ingest and Discord message identity.
- cllama remains the context-aware injection and telemetry layer.
- Clawdapus pod generation remains the owner of Discord surface awareness:
  channel IDs, agent ACLs, generated tool policy, and generated feed URLs.
- The wire surface stays `channel-awareness`, upgraded from `raw_window` to
  `raw_window+digest` when a digest is available.

## Existing Upstream Architecture To Reuse

### #232 Channel Awareness

`~/dev/ai/clawdapus/docs/plans/2026-05-12-issue-232-channel-24h-awareness.md`
already defines the four-part contract:

1. raw recent window
2. rolling digest
3. retrieval path
4. provider-visible metadata

Phase 1 shipped the raw window, retrieval path, and metadata. Phase 2 is the
rolling digest producer.

### #164 Salience Memory

`~/dev/ai/clawdapus/docs/plans/2026-04-29-salience-memory-adapter.md` already
settles the important processing rule:

- LLM processing is asynchronous.
- Hot-path recall does not call an LLM.
- Recall selects and formats already-processed blocks within a budget.
- The service is swappable and deterministic without provider credentials.

Channel memory should reuse that pattern, not invent a separate inline
summarizer.

## Architecture

```text
Discord gateway / polling
        |
        v
    claw-wall
    - owns channel surfaces
    - stores recent raw buffer
    - assigns/retains Discord message IDs
    - enforces per-agent channel ACLs
        |
        | async push after message retention
        v
    claw-channel-memory
    - durable channel-message ledger
    - deterministic telemetry/hard-event processor
    - async LLM rollup worker
    - digest blocks with source provenance
        |
        | sync digest lookup, no LLM
        v
    claw-wall /channel-awareness
    - raw recent window
    - older rolling digest
    - digest status + coverage metadata
        |
        v
    cllama feed injection
    - provider-visible `raw_window+digest`
    - feed budgets
    - `channel_context_op` telemetry
```

## Capability And Wiring

The channel-memory service should expose a descriptor separate from the generic
memory plane. It is channel-shaped, not agent-session-shaped.

Draft descriptor:

```json
{
  "version": 2,
  "channel_memory": {
    "ingest": { "path": "/ingest" },
    "digest": { "path": "/digest" },
    "forget": { "path": "/forget" },
    "health": { "path": "/health" }
  }
}
```

Draft pod wiring:

```yaml
x-claw:
  channel-memory:
    service: claw-channel-memory
```

The pod-level wiring should be optional. If no channel-memory service is
configured, existing pods continue to receive `channel-awareness` with
`digest=unavailable`.

When configured, Clawdapus generation should provide claw-wall with:

- adapter base URL
- service token
- channels that should be ingested
- per-agent channel allowlist already generated from Discord surfaces
- digest policy defaults such as max digest bytes and max source age

Avoid per-agent YAML knobs in the first pass. The pod's Discord surfaces already
declare which channels exist and which agents can see them.

## Ingest Path

Use push ingest from claw-wall to channel-memory.

Reasons:

- claw-wall already owns Discord channel surfaces, raw messages, and channel
  ACLs.
- claw-wall sees Discord message IDs and edit/delete events earliest.
- push ingest mirrors the existing infrastructure-retention model: the owner of
  the source stream retains it, then asynchronous processors derive compact
  recall blocks.
- a pulling adapter would need its own claw-wall cursor semantics and would
  duplicate retention logic.
- direct Discord gateway ingest would bypass Clawdapus channel surface policy
  and create another source of truth.

Pull/backfill can exist later as a repair path, but it should not be the normal
live ingest path.

Draft `/ingest` payload:

```json
{
  "channel_id": "1234567890123456789",
  "message": {
    "id": "2234567890123456789",
    "author_id": "3234567890123456789",
    "author_name": "analyst-a",
    "created_at": "2026-05-21T16:23:22Z",
    "edited_at": null,
    "deleted": false,
    "content": "[PROPOSED] signal-123 SELL ACME ...",
    "content_hash": "sha256:..."
  },
  "source": {
    "service": "claw-wall",
    "surface": "discord",
    "guild_id": "..."
  }
}
```

Ingest must be idempotent by `(channel_id, message.id, content_hash)`.

## Durable Retention

The current claw-wall phase-1 buffer is not enough for Phase 2. It is an
in-process retention window; digest-backed awareness needs a durable channel
message substrate.

The v1 adapter should use a database, not another JSONL or in-memory buffer.
The core queries are timestamp and source-id driven:

- fetch raw recent messages by channel + time window
- fetch exact source messages by source ID
- find all messages covered by a sparse digest block
- mark derived blocks dirty after edit/delete/forget
- report retained coverage and gaps by channel + time range

Those are database-shaped operations. A flat append log may remain useful for
export/debugging, but it should not be the primary recall substrate.

The channel-memory adapter should persist:

- source messages, at least for the active retention window
- source content hashes
- edit/delete tombstones
- processing queue state
- raw and sparse digest blocks plus their source message IDs
- digest coverage metadata
- per-channel telemetry/cost counters

SQLite is the right v1 storage choice, matching the salience-memory adapter:

- idempotent ingest is simple
- queue/retry state is local
- provenance queries are indexed
- deterministic tests are easy
- the database can be inspected directly during incidents

Raw message content retention can be bounded by policy, but digest provenance
must survive long enough for exact-source retrieval to work over the advertised
awareness window.

Recommended minimal schema shape:

- `source_messages`: one retained source event per Discord message, keyed by
  `(source_kind, channel_id, message_id, content_hash)` with indexed
  `timestamp`, `channel_id`, `author_id`, edit/delete state, and visibility
  scope. Edits create new rows because `content_hash` changes; recall and feed
  serving select the current non-deleted version unless the request explicitly
  asks for historical versions.
- `derived_blocks`: deterministic or LLM-produced recall blocks with `kind`,
  `sparse`, `source_window`, `generated_at`, `stale/dirty` state, and cost/
  processor metadata.
- `derived_block_sources`: many-to-many mapping from derived block to covered
  source message IDs.
- `coverage_gaps`: explicit missing ranges by channel.

Do not model sparse recall as a separate primary `discord_sparse` history that
can diverge from raw retention. Model it as derived rows over the same source
messages.

## Raw And Sparse Recall Model

The recall view should be tiered:

- recent material returns raw source messages or near-verbatim hard events
- older material returns sparse derived blocks
- exact source retrieval remains available when a sparse block cites retained
  messages

The `sparse` flag is about content fidelity, not age. `sparse=false` means the
block reproduces source content faithfully, as with `hard_event` and
`raw_excerpt` blocks. `sparse=true` means the block summarizes, elides, or
otherwise omits source content, as with `topic_rollup`, `sequence_rollup`, and
`telemetry_count`.

Sparse blocks can cover one message or many messages. A short exchange in a
Discord channel may become one `sequence_rollup` block with the gist of the
exchange, its timestamp range, participants, source message IDs, and channel
visibility metadata.

Example sparse block:

```json
{
  "kind": "sequence_rollup",
  "sparse": true,
  "text": "Analyst A and analyst B rejected chasing ACME after the signal faded; router C treated the related broad market recap as non-actionable.",
  "source_channel": "1234567890123456789",
  "source_messages": ["2234567890123456789", "2234567890123456790", "2234567890123456791"],
  "source_window": {
    "from": "2026-05-21T16:23:22Z",
    "to": "2026-05-21T16:27:10Z"
  },
  "participants": ["analyst-a", "analyst-b", "router-c"],
  "score": 0.73
}
```

The sparse block is not authoritative history. It is a compact recall artifact
with provenance back to authoritative source messages. If source messages are
forgotten, deleted, edited, or fall out of the exact-retrieval window, the
block must be marked dirty, tombstoned, or downgraded with explicit coverage
metadata.

## Digest Request

The hot-path request must not call an LLM.

Draft `/digest` request:

```json
{
  "channel_ids": ["1234567890123456789"],
  "since": "24h",
  "raw_recent": {
    "max_messages": 50,
    "max_age": "60m"
  },
  "budget": {
    "max_digest_bytes": 8192,
    "max_blocks": 32
  },
  "agent_id": "agent-0",
  "allowlist_hash": "sha256:..."
}
```

Draft `/digest` response:

```json
{
  "status": "ok",
  "generated_at": "2026-05-21T21:55:00Z",
  "coverage": {
    "from": "2026-05-20T21:55:00Z",
    "to": "2026-05-21T21:55:00Z",
    "source_messages": 200,
    "digest_messages": 150,
    "raw_recent_messages": 50,
    "gaps": []
  },
  "blocks": [
    {
      "kind": "hard_event",
      "event_type": "trade_proposal",
      "text": "[16:23] analyst-a proposed SELL ACME signal-123; reason momentum faded.",
      "source_channel": "1234567890123456789",
      "source_messages": ["2234567890123456789"],
      "source_window": {
        "from": "2026-05-21T16:23:22Z",
        "to": "2026-05-21T16:23:22Z"
      },
      "score": 1.0
    }
  ],
  "cost": {
    "llm_calls_today": 12,
    "llm_calls_cap": 500,
    "llm_cost_today_usd": 0.034,
    "llm_cost_cap_usd": 5.00,
    "deterministic_only": false
  }
}
```

Normal statuses:

- `ok`: digest returned
- `stale`: digest returned but older than policy
- `unavailable`: no digest for requested window
- `coverage_gap`: source retention has a gap in the requested window
- `error`: adapter failed; caller should fall back to raw window only

## Digest Block Kinds

Use typed blocks. Do not return one composed prose blob.

Required fields for every block:

- `kind`
- `text`
- `source_channel`
- `source_messages`
- `source_window`
- `sparse`
- `score`

Recommended block kinds:

- `hard_event`: exact or near-exact trade/risk/routing event
- `topic_rollup`: ticker, sector, or topic summary over older messages
- `sequence_rollup`: compressed multi-message decision sequence where order
  matters
- `telemetry_count`: counted marker for runtime/status noise
- `coverage_gap`: explicit marker that retained source coverage is incomplete
- `tombstone`: source message was deleted or intentionally forgotten.
  Model-visible content is the author, deletion timestamp, and a
  `[message removed]` placeholder; pre-deletion content is NOT carried in the
  tombstone. If an agent needs pre-deletion content for audit/reasoning, the
  retrieval tools are the gated path and may themselves return `not_in_buffer`
  if claw-wall has already evicted the source. Compliance-side preservation
  is the operator's choice; the digest should not be the audit trail.
- `raw_excerpt`: high-relevance message preserved verbatim within the digest

For `hard_event`, use a typed `event_type`, such as:

- `trade_proposal`
- `trade_approval`
- `trade_confirmation`
- `trade_fill`
- `stop_or_target_change`
- `route_decision`
- `no_route_decision`
- `held_position_news`
- `watchlist_news`
- `thesis_update`
- `risk_limit`

Classifier rule: fail open. If a message might be a hard event, preserve it as
`hard_event` or `raw_excerpt`. Do not roll it into vague prose.

## Feed Output Shape

`channel-awareness` should remain the feed. Phase 2 changes the body and
metadata, not the conceptual surface.

Example header:

```text
[channel-awareness] kind=raw_window+digest since=24h channels=1234567890123456789 raw_messages=50 digest_messages=150 retained=200/since-24h omitted=0 digest=ok digest_generated_at=2026-05-21T21:55:00Z digest_bytes=6812
```

Example body:

```text
## Raw Recent Window
[16:23] analyst-a: [PROPOSED] signal-123 SELL ACME ...

## Digest: Hard Events
- [16:23] analyst-a proposed SELL ACME signal-123; source=123.../223...

## Digest: Topic Rollups
- ACME: entry invalidated and exited after signal faded from strong to weak; sources=...

## Digest: Telemetry Elisions
- [15:45-15:46] provider retry/status noise elided: agent-0 x4, agent-1 x4, agent-2 x4.

## Digest Coverage
- coverage=ok from=... to=... generated_at=...
```

The model-visible feed must make source and coverage obvious. If the digest is
stale or missing, say so in the header.

## Retrieval Semantics

Digest summaries are only safe if exact-source retrieval is cheap.

Existing managed tools:

- `search_channel_context`
- `get_channel_messages`

Phase 2 requirements:

- retrieval tools accept Discord message IDs returned in digest provenance
- retrieval tools can fetch a time range when only a source window is present
- tool results report `not_in_buffer` or `coverage_gap` honestly
- cllama preserves `[channel-tool]` metadata in tool results
- `channel_context_op` telemetry records digest-backed retrieval outcomes

If message IDs are not currently emitted by phase-1 `channel-awareness`, that
is a #232 Phase 2 requirement. Timestamp-only provenance is not adequate for
chunked reports or same-minute multi-agent bursts.

## Deterministic Processor

The adapter must run usefully with no LLM key.

Deterministic mode should:

- parse Discord message lines
- preserve hard events verbatim or near-verbatim
- collapse runtime/status noise into counted `telemetry_count` blocks
- group obvious ticker messages by ticker symbol
- emit `coverage_gap` when source coverage is incomplete
- expose all source provenance

This is the test harness and the safety fallback. The LLM processor only adds
better `topic_rollup` and `sequence_rollup` blocks for older non-hard content.

## Async LLM Processor

The LLM worker processes queued windows off the hot path.

Rules:

- never process hard events into lossy prose
- never block `/digest` waiting for an LLM
- require structured JSON output
- reject malformed output
- cache by source message IDs and content hashes
- enforce per-channel and per-pod daily call/cost caps
- store model/provider/version with generated blocks

If the LLM fails, the digest stays deterministic-only and the feed header says
so.

## Cllama Side Changes

The plan is mostly claw-wall + channel-memory work, but cllama owns the
injection and telemetry path and needs corresponding updates:

- recognize `context_kind=raw_window+digest` from claw-wall responses and
  surface it in feed prompt headers (already partially supported via the
  existing `context_kind` framework).
- preserve digest metadata in `channel_context_op` events:
  `digest_status`, `digest_age_ms`, `digest_bytes`, `raw_bytes`,
  `source_messages_covered`, `coverage_gap_count`, `deterministic_only`.
- track digest bytes separately from raw bytes in feed-budget accounting so
  operators can see where bytes come from in a budget-constrained turn.
- propagate the per-agent allowlist hash on `/digest` requests so claw-wall
  can pass it through to channel-memory for re-verification.

No new pod YAML is required on the cllama side; this is internal handling of
the upgraded surface.

## Companion ADR

The new capability + context-kind change is architecturally analogous to
ADR-021 (memory plane). It deserves a companion ADR alongside this plan.

ADR scope:

- introduce `channel-memory` as a sibling compiled capability (next to
  `memory`, `feeds`, `tools`) in the canonical capability IR
- define the ownership boundary: claw-wall owns identity + serving;
  channel-memory owns durable ledger + processing; cllama owns injection +
  telemetry
- define the retention/forget propagation contract analogous to
  ADR-018/ADR-021
- record `raw_window+digest` as the new context kind and the fallback
  behavior when no adapter is configured
- explicitly avoid forcing channel-memory into the existing `memory`
  capability (channel data is shared, not per-agent; recall is via
  `channel-awareness`, not `/recall`)

Drafted as ADR-025 in
`docs/decisions/025-channel-memory-and-digest-backed-awareness.md`.

## Edit, Delete, Forget, And Gaps

Discord messages can be edited or deleted. claw-wall retention can also reset.
The digest must not pretend stale summaries are fresh truth.

Required behavior:

- edited message with new content hash marks derived blocks dirty
- deleted message creates a tombstone block and marks derived blocks dirty
- `/forget` suppresses the source message and derived blocks
- source gaps produce `coverage_gap` blocks and feed header metadata
- backfill/repair can replay retained raw messages through the same `/ingest`
  path

Do not mutate away the fact that something was once summarized. Tombstones and
coverage gaps are part of safe recall.

## Cost And Budget Policy

Default budgets should be internal adapter settings with environment overrides,
not a large public YAML surface in v1.

Suggested defaults:

- digest budget: 4-8KB per channel per 24h window
- raw recent window: existing #232 policy unless changed upstream
- LLM daily call cap: conservative
- per-request `/digest` timeout: short enough that claw-wall can fail open to
  raw window only

Telemetry should expose:

- digest status
- digest age
- source messages covered
- source gaps
- LLM calls/cost estimate
- deterministic-only fallback count

## Production Evidence And Acceptance Input

The motivating production evidence should drive the upstream acceptance suite
without encoding a deployment identity in public Clawdapus docs:

- A raw 24h `channel-awareness` feed can reach roughly 90k characters across
  200 Discord messages, including dozens of runtime/status lines and recurring
  cron/status lines.
- A provider request can hit the roughly 120 second envelope even when feed
  fetches complete quickly. Raw 24h `channel-awareness` size was the driver,
  not feed latency.
- Telemetry stripping alone is not enough; long agent decision reports dominate
  bytes.
- Hard events that must be preserved include trade proposal, approval,
  confirmation, fill, stop/target changes, held-position news, watchlist news,
  route/no-route decisions, thesis updates, and action-changing
  disagreements.
- Provider-visible side channels have been verified for structured trade
  reasoning, watchlist/position state, scaffold, chronicle, and agent memory.

Acceptance for the upstream implementation:

- digest-backed `channel-awareness` emits `raw_window+digest`
- feed serving does no LLM work
- source messages and derived sparse blocks are stored in an indexed database
- deterministic mode works with no LLM key
- exact source retrieval works from digest provenance
- hard events are preserved in the sanitized production fixture
- coverage gaps and stale digest states are explicit
- provider-visible capture shows meaningful byte reduction after LLM rollup
- `channel_context_op` telemetry distinguishes raw-only, digest, stale, and
  coverage-gap states

## Design Tasks

D1. Update #232 plan with the Phase 2 architecture pointer + link to this plan.
D2. Draft companion ADR (see Companion ADR section).
D3. Land #232 Phase 1 follow-up for Discord message ID exposure (see
    Prerequisite).

## Implementation Slices

1. Add `examples/channel-memory` with descriptor, SQLite source-message store,
   derived sparse-block tables, deterministic processor, tests, and README.
2. Add claw-wall push ingest to the adapter, gated by optional pod wiring.
3. Add digest lookup from claw-wall's `/channel-awareness` serving path, with
   fail-open behavior.
4. Emit `raw_window+digest` headers and expanded `channel_context_op`
   telemetry.
5. Add exact message ID provenance to feed output and retrieval tools (if
   not already landed via the Phase 1 follow-up).
6. Add async LLM worker and cost caps.
7. Add a sanitized production fixture for hard-event preservation and byte
   measurement.
8. Only after the example proves out, decide whether to promote it to a
   published `cmd/claw-channel-memory` image.

## Open Questions

- Does the first public surface update #232's plan in place, create a new plan,
  or both?
- Does v1 require durable raw message content in channel-memory, or only enough
  source metadata plus digest provenance for 24h retrieval?
  Recommended: v1 stores full content + hash + metadata for source messages
  inside the digest window (24h). Beyond the window, retain only content-hash
  + ID + author + timestamp for tombstone/audit continuity. This keeps the
  active window safely re-processable on regen and keeps the long tail cheap.
- Should claw-wall own `/digest` fanout for multiple channel-memory backends in
  the future, or is one pod-level backend enough?
- Should digest block budgets be per channel, per agent, or both? Start per
  channel; per-agent can be a later policy layer.
- How should Discord edits/deletes reach claw-wall in every supported surface?

## Next Step

Open a Clawdapus implementation issue from the design tasks above, starting
with the stable Discord message ID follow-up. Keep deployment-local plans as
private incident evidence, acceptance criteria, and later deployment checklists
rather than building a deployment-specific compactor.
