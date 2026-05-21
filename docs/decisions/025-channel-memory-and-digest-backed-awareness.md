# ADR-025: Channel Memory and Digest-Backed Awareness

**Date:** 2026-05-21
**Status:** Draft
**Depends on:** ADR-013 (Context Feeds), ADR-018 (Session History and Memory Retention), ADR-021 (Memory Plane and Pluggable Recall)
**Implementation:** `docs/plans/2026-05-21-channel-memory-adapter.md`

## Context

Issue #232 added bounded channel awareness for agents that consume Discord
surfaces. Phase 1 gave those agents a provider-visible uncursored
`channel-awareness` raw window, cursorized `channel-context` continuity, exact
retrieval tools, per-agent channel ACLs, explicit context-kind metadata, and
`channel_context_op` telemetry.

That is enough to avoid pure cursor blindness, but it does not solve high-volume
rooms. A busy 24 hour channel window can become too large to inject raw on every
turn, and aggressive truncation risks dropping the exact floor discussion where
decisions were made.

ADR-021 already established the right pattern for model-assisted retention and
recall: infrastructure retains source material, asynchronous processors derive
compact artifacts, and hot-path recall selects already-processed blocks without
calling an LLM. Channel awareness needs the same pattern, but channel data is
shared room state rather than per-agent session history.

## Decision

Introduce `channel-memory` as a sibling compiled capability for digest-backed
channel awareness.

The ownership boundary is:

- `claw-wall` owns Discord channel ingest, message identity, edit/delete
  observation, per-agent channel ACLs, and serving the `channel-awareness` feed.
- `channel-memory` owns durable channel-message retention, deterministic
  hard-event and telemetry processing, asynchronous LLM-backed digest
  generation, tombstones, and digest provenance.
- `cllama` owns provider-visible injection, feed budgets, context-kind handling,
  managed retrieval tool mediation, and `channel_context_op` telemetry.
- Clawdapus pod generation owns Discord surface awareness: channel ids,
  generated feed URLs, generated tool policy, and channel allowlist projection.

The existing `channel-awareness` feed remains the wire surface. When no digest
producer is configured, it behaves as Phase 1 and advertises
`digest=unavailable`. When channel memory is available, claw-wall serves
`context_kind=raw_window+digest`: a raw recent window plus source-backed digest
blocks for older material in the same awareness window.

Channel memory is not a subtype of the existing ADR-021 `memory` capability.
It is channel-shaped:

- source is shared channel traffic, not one agent's retained session history
- recall is through `channel-awareness`, not `/recall`
- ACLs are channel allowlists, not only agent memory ownership
- exact-source retrieval uses channel tools, not memory block lookup

## Consequences

Positive:

- Pods get the "high-resolution recent, compact older" awareness model without
  inventing pod-specific compactor sidecars.
- Existing #232 surfaces stay coherent: `channel-awareness`, `channel-context`,
  retrieval tools, channel ACLs, context kinds, and `channel_context_op`.
- LLM processing remains off the model-request hot path.
- Digest blocks can be inspected, budgeted, and traced back to source messages.
- Channel-memory implementations can improve without changing every pod's agent
  contracts.

Negative:

- A new capability surface needs descriptor, compose generation, service-token,
  and telemetry plumbing.
- claw-wall must expose stable Discord message IDs, or equivalent source
  handles, before digest provenance can be trusted.
- Durable channel retention introduces storage policy questions that Phase 1's
  in-memory claw-wall buffer avoided.
- Digest-backed awareness needs an indexed durable store. The first
  implementation should use SQLite or an equivalent embedded database rather
  than extending the current in-memory claw-wall buffer.

## Required Behavior

- Feed serving never calls an LLM.
- `channel-awareness` advertises whether digest is `ok`, `stale`,
  `unavailable`, or blocked by a `coverage_gap`.
- Digest entries carry source message provenance sufficient for
  `search_channel_context` / `get_channel_messages` to retrieve exact source.
- Sparse digest entries are derived artifacts over retained source-message
  ranges, not a parallel primary message history.
- Hard events such as trade proposal, approval, confirmation, fill, stop/target
  change, explicit route/no-route decision, and risk-limit event are preserved
  verbatim or near-verbatim.
- Edited/deleted/forgotten messages propagate to derived digest artifacts via
  dirty markers and tombstones.
- The digest producer runs deterministically without an LLM key.
- LLM-backed rollups are asynchronous, cached by source ids and content hashes,
  and bounded by configurable cost/call caps.

## Follow-Up

- Update issue #232 Phase 2 around `raw_window+digest` and channel-memory.
- Add a tightly scoped #232 Phase 1 follow-up for stable Discord message IDs in
  `channel-awareness` and retrieval tool results.
- Build `examples/channel-memory` first. Promote to a published
  `cmd/claw-channel-memory` image only after the example proves out.
- Add sanitized high-volume channel fixtures that exercise hard-event
  preservation, telemetry elision, source retrieval, and coverage gaps.
