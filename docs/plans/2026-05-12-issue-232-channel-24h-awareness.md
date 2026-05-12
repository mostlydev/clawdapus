# Issue 232 — Bounded 24h channel awareness independent of cursors and restarts

**Author (draft):** claude (`claude:dbaba2df`) on branch `issue-232-channel-24h-awareness` — for adversarial review by codex before any implementation.

**Issue:** https://github.com/mostlydev/clawdapus/issues/232

**Predecessors:**
- #201 (channel-context tail mode, v0.13.7) — flipped delta→tail as default, dual cap.
- #204 (cursored append-only context deltas) — late-context placement, no first-system mutation.
- #220 (channel-cursor session epoch, v0.14.7) — restart bootstrap via `X-Claw-Consumer-Session-Epoch`.

**Sibling:** #164 (salience-memory adapter) — owns LLM-derived compaction. This plan does **not** duplicate that work; v2 of #232 consumes #164's adapter.

## 1. Problem (one paragraph)

#220 fixed *across-restart* cursor bootstrap. #201 fixed tail vs delta semantics. The remaining failure mode is *within an active session*: the agent's cursor legitimately advances past a same-day floor message, then a human prompt refers to that older message, and the agent answers "I don't see it in the last 24h" because its prompt only contains the post-cursor delta. The live Tiverton incident (May 11, 2026, boulton on `#trading-floor`) is the canonical case: Logan posted a CMCSA read at 07:47 ET, boulton's cursor advanced normally, and at 09:50 ET the operator asked "wanna pick up CMCSA on Logan's read?" — boulton's prompt contained only the immediate ping thread because the cursor was already past Logan's message.

This is not a cursor bug. It is a missing *awareness layer*: cursorized delta is the right product for live continuity but the wrong product for "what does this agent know about the last 24h of the room?".

## 2. Contract (the four parts from issue #232 comment r2)

The operator's acceptance contract has four parts. They are not independently dispensable; they compose into "guaranteed bounded 24h awareness":

1. **Raw recent window** — exact messages for immediate continuity, bounded.
2. **Rolling 24h digest** — LLM-derived, source-backed compaction of older same-day material, regenerated under buffer churn and after restart.
3. **Retrieval path** — explicit fetch of exact source messages by author/ticker/time/id, so a digest can cite without restating.
4. **Provider-visible metadata** — every channel-context block in the prompt declares whether it is `delta` / `raw_window` / `digest`, with retained-coverage so the model never claims "I don't see the last 24h" when only a delta was injected.

## 3. Phasing

The plan splits along a hard dependency: part (2) needs a memory-grade producer, and the closest fit is #164's salience-memory adapter. v1 ships parts (1), (3), (4) and an explicit stopgap label on (2). v2 retires the stopgap once #164 lands.

| Part | v1 producer | v2 producer | Wire surface in both phases |
|------|-------------|-------------|------------------------------|
| (1) Raw recent window | `claw-wall` (new `channel-awareness` endpoint, uncursored, dual-capped) | unchanged | feed: `channel-awareness` |
| (2) Rolling 24h digest | **stopgap: omitted** with explicit metadata label `digest=unavailable`; busy rooms rely on retrieval | `claw-channel-memory` (subscribes to claw-wall events, retains via #164's adapter contract, emits `digest=ready`) | feed: `channel-awareness` (composite block) |
| (3) Retrieval path | `claw-wall` exposes managed tools (ADR-020): `search_channel_context`, `get_channel_messages` | unchanged | `tools[]` |
| (4) Metadata | header-line discipline on every channel-flavored feed; new `channel_context_op` telemetry event distinguishing `delta` / `raw_window` / `digest` / `tool_call` | unchanged | feed header + telemetry |

The v1 channel-awareness block is **not** rebranded as "the 24h fix". The plan explicitly labels it as bounded raw-window + retrieval; full 24h coverage under busy-room load is only guaranteed once digest lands.

## 4. Wire surfaces

### 4.1 New feed: `channel-awareness` (v1, producer = claw-wall)

```
GET /channel-awareness?channels=<csv>&since=24h&limit=120&max_chars=12288
```

- **No cursor**. No `consumer=` param. No `mode=delta` path. This feed exists precisely to be cursor-independent.
- Same channel-authorization model as `channel-context` (caller is upstream-restricted to surface-derived channels).
- Reuses `consumeTail` semantics from #201 (newest-first walk, dual cap, sort ASC for output) but rebranded:
  - Defaults: `since=24h`, `limit=120`, `max_chars=12288` (≈12KB). These are **larger** than `channel-context` tail defaults because this block carries 24h, not "recent ping context". Operator-tunable via `x-claw.context.channel-awareness` (parallel of #201's `x-claw.context.channel`).
- Header line is explicit about layer kind, exactly so the model cannot conflate it with a delta:

```
[channel-awareness] kind=raw_window since=24h messages=87 range=2026-05-11T05:42Z..2026-05-12T05:42Z channels=14645…,14647… retained=87/since-24h digest=unavailable
```

`digest=unavailable` in v1 documents the known gap. In v2 the same feed emits `digest=ready` and a digest section is appended below the raw window.

`channel-context` (the existing delta feed) keeps its `mode=tail` and `mode=delta` headers from #201 verbatim — but #232 lands a **header rewrite** on the `mode=delta` path so it stops claiming `[channel-context]` and instead reads `[channel-context delta] kind=delta`. See §4.4.

### 4.2 New tools: retrieval (v1, producer = claw-wall, ADR-020 managed)

`claw.describe` v2 on the `claw-wall` image declares two managed tools:

```yaml
tools:
  - name: search_channel_context
    description: "Search recent channel buffer (up to retained-coverage window) for messages matching a query."
    params:
      channels: { type: array, items: string, required: true }
      query: { type: string, required: true }
      since: { type: string, default: "24h" }
      author: { type: string, required: false }
      limit: { type: integer, default: 20 }
    http: { path: /search_channel_context }
  - name: get_channel_messages
    description: "Fetch exact channel messages by ID range, or by author+time window."
    params:
      channels: { type: array, items: string, required: true }
      message_ids: { type: array, items: string, required: false }
      after: { type: string, required: false }    # snowflake id or ISO ts
      before: { type: string, required: false }
      limit: { type: integer, default: 20 }
    http: { path: /get_channel_messages }
```

Both are **mediated** by cllama (ADR-020) so policy/telemetry/auth flow through the standard plane. Mediation gives us per-call audit, channel ACL enforcement, and bounded response sizing without a runner-specific tool plumbing pass.

Tool responses include the same `kind=raw_window source=claw-wall channels=…` metadata header as the feed, so the model treats tool output and feed injection symmetrically.

### 4.3 Late-context placement (per #204)

The `channel-awareness` block is appended to the late runtime-context system message (OpenAI) or final user content block (Anthropic), alongside the existing `channel-context` delta block and the memory recall block.

Order inside the late block (deterministic for cache-friendliness):

1. memory recall (existing, from ADR-021)
2. `channel-awareness` (new — broadest layer, anchors the model in the last 24h)
3. `channel-context` delta (existing, narrowest layer, fresh post-cursor)
4. current time line (existing)

Rationale: digest/awareness first sets the model's frame of "what the room knows"; delta then signals "what's new since you last looked"; time anchors the moment. Reversing this lets a stale-but-large window dominate fresh signal.

### 4.4 Header discipline (part 4 of contract)

Every channel-flavored block carries an explicit `kind=` field in its header. The implicit assumption "channel-context = full room" is the bug we are killing.

| Block kind | Header prefix | Notes |
|------------|---------------|-------|
| `delta` | `[channel-context delta] kind=delta cursor=<ch>:<id>,...` | rewrite of today's `[channel-context]` header; cursor pair already present |
| `raw_window` | `[channel-awareness] kind=raw_window since=...` | v1 default |
| `digest` | `[channel-awareness] kind=digest since=... source_count=...` | v2 only, appended below raw_window in composite block |
| `tool_call` | tool response body, header `[channel-tool] kind=tool_call name=...` | retrieval tool output |

A small change to the existing `[channel-context]` header is the riskiest back-compat surface. It's necessary: today's header has no `kind=` discriminator and operators have already observed the model misinterpreting delta as "the room". v2 of #204's `coverage_partial=true` annotation is preserved verbatim.

### 4.5 Telemetry (part 4 of contract)

New normalized event kind: `channel_context_op` with fields `{kind, channels[], retained, returned, omitted, latency_ms, source, status}`. Emitted for:
- every channel-awareness fetch (kind=raw_window)
- every channel-context delta fetch (kind=delta) — reuses existing claw-wall log line but normalizes it through cllama
- every retrieval tool call (kind=tool_call)
- v2 digest production (kind=digest_built) and consumption (kind=digest)

`claw audit` surfaces this alongside `memory_op` and `feed_fetch`.

## 5. Compose-up wiring

Pod-level config extends #201's `x-claw.context.channel` with a sibling block:

```yaml
x-claw:
  context:
    channel:                    # existing delta feed (from #201)
      since: 24h
      limit: 40
      max_chars: 8192
    channel-awareness:          # NEW
      since: 24h
      limit: 120
      max_chars: 12288
      enabled: true             # explicit opt-in for v1 (default false) — see §8
```

Service-level overrides via the same pod-defaults pattern (`x-claw.context.channel-awareness` on individual services).

`appendConversationWallFeed` (in `cmd/claw/compose_up.go`) gains a sibling `appendConversationWallAwarenessFeed` that emits `/channel-awareness?channels=...&since=...&limit=...&max_chars=...` when `enabled: true` and the service has any consumed channel IDs. Tool registration uses the existing claw-wall descriptor extraction path.

## 6. Stopgap discipline (codex challenge #2)

This plan **must not** ship as "the 24h fix" without phase 2. The framing in the PR body, the release note, and the on-call runbook reads:

> v1 of #232 ships bounded raw-window awareness, retrieval tools, and metadata. **Busy rooms (>120 messages in 24h) will exceed the v1 dual cap.** When that happens, the agent will see the most recent N messages plus retrieved older ones on demand; full 24h compaction is delivered in v2 once #164's salience-memory adapter lands. The `digest=unavailable` header line documents this in-prompt so the model can decide whether to call retrieval.

The acceptance criterion in the issue body — "later Boulton turn still includes Logan's CMCSA read in the 24h awareness layer without requiring the human to restate it" — is **partially satisfied** in v1: Logan's read is reachable via retrieval (and likely also visible in the raw 24h window for a not-too-busy floor), but not guaranteed-injected. v2 closes the gap.

## 7. Tests

### 7.1 claw-wall (`cmd/claw-wall/`)

- `TestChannelAwarenessHandlerReturnsRawWindow` — `since=24h`, no cursor write, header reads `kind=raw_window`, `digest=unavailable` in v1.
- `TestChannelAwarenessHandlerDualCap` — 200-message buffer, `limit=50&max_chars=…` → returns newest 50 sorted ASC, header shows `retained=50/...`.
- `TestChannelAwarenessHandlerColdStart` — buffer holds 10 minutes worth → header reads `retained=N/buffer range=...` honest about coverage.
- `TestChannelAwarenessHandlerDoesNotMutateCursor` — interleave with `channel-context` delta consumer; awareness fetch leaves delta cursor untouched.
- `TestSearchChannelContextRespectsACL` — request includes channel not in caller's surface → 403 (reuses claw-wall's existing ACL).
- `TestGetChannelMessagesByIDRange` — fetch by id range returns exact messages, ordered.

### 7.2 cllama (`cllama/internal/proxy/`)

- `TestChannelAwarenessFeedIsFetchedWithoutCursor` — register a `channel-awareness` feed entry; assert URL has no `after=` and `X-Claw-Consumer-Session-Epoch` is **not** consulted for it (orthogonal to #220's epoch).
- `TestChannelAwarenessAndDeltaCoExist` — pod wires both feeds; both blocks appear in the late-context section, in the §4.3 order, with their respective `kind=` headers.
- `TestChannelContextDeltaHeaderRewrite` — existing delta path now emits `[channel-context delta] kind=delta cursor=...`. Regression test reads the new prefix.
- `TestChannelContextOpTelemetryNormalized` — fetch each kind, assert `audit.go`-style normalized records for `kind=raw_window`, `kind=delta`, `kind=tool_call`.

### 7.3 compose_up (`cmd/claw/`)

- `TestAppendChannelAwarenessFeedHonorsPodConfig` — pod-level `x-claw.context.channel-awareness` overrides flow into generated URL.
- `TestChannelAwarenessFeedDisabledByDefault` — without explicit `enabled: true`, no feed entry is emitted (v1 opt-in; see §8).
- `TestChannelAwarenessToolsRegisteredFromDescriptor` — claw-wall descriptor extraction picks up the two tool entries when service is auto-injected.

### 7.4 Pod parser (`internal/pod/`)

- `TestParseChannelAwarenessConfig` — accepts `since` / `limit` / `max_chars` / `enabled`; rejects negatives; rejects unparseable durations.
- `TestPodDefaultsChannelAwareness` — service-level overrides pod defaults using the standard pattern.

### 7.5 Integration / spike

- Extend `TestSpikeRollCall` (or a sibling spike) so that the rollcall pod's `channel-awareness` feed body contains a header-line `kind=raw_window` and matches the retained-coverage shape. The spike already exercises real LLM calls through cllama; this just asserts the new feed flows end-to-end.
- A targeted fixture test reproduces the Tiverton case: pre-seed `claw-wall` buffer with messages spanning the cursor, run a turn that advances cursor past Logan's hypothetical CMCSA message, then run a second turn with a human-mention prompt and assert the `channel-awareness` block contains Logan's message even though the `channel-context` delta does not.

## 8. Default behavior and ramp

`channel-awareness` is **opt-in** in v1 (`enabled: true` required at pod level). Two reasons:

1. Token cost is non-trivial: 12KB default added to every cllama turn for pods with active channels. Operators should choose the trade-off explicitly until v2 makes the cost bounded by digest length rather than raw message count.
2. The header rewrite of `channel-context` delta is the only forced change. Awareness layering itself is opt-in until digest lands and v2 can flip the default.

Once v2 ships, the default flips to `enabled: true` and pods get the full contract automatically.

## 9. Out of scope (deliberately)

- **Cross-channel digest correlation.** v1 and v2 both keep channel-awareness scoped per-channel within a single feed call. Cross-channel synthesis is a future memory adapter feature.
- **Affect/salience scoring** of channel messages. Same #164 territory.
- **Discord history backfill** beyond claw-wall's in-process buffer. Per #201 §3 Q3, the wider-continuity feeds cover cold-start. Operators who need a true 24h window today can raise `CLAW_WALL_LIMIT`/buffer; v2's digest reduces the urgency further.
- **Per-channel awareness/delta toggles.** v1 keeps the pod/service knob granularity from #201.
- **Anthropic `cache_control` markers** for awareness blocks. Same deferral as #204 v2.

## 10. Open questions for codex r1 review

1. **Composite vs co-injection.** Plan currently treats `channel-awareness` and `channel-context` as two separate feeds. Codex flagged "make the provider-visible surface a channel-awareness block, with the memory adapter as the producer/backend." Should the single block model be enforced from v1 — i.e., one `channel-awareness` feed that *contains* a delta section + a raw-window section + (v2) a digest section, and `channel-context` is retired? Two-feed wins on cache stability and incremental v1→v2 path; one-block wins on model framing and metadata clarity. Lean two-feed but flag for challenge.
2. **Header rewrite on existing `channel-context`.** v1 changes the prefix from `[channel-context]` to `[channel-context delta] kind=delta`. This is necessary for part-4 metadata discipline but is back-compat-flavored. Acceptable, or should the existing header stay verbatim and only the new feed introduce `kind=`?
3. **v1 retrieval tools live where?** Plan puts them on `claw-wall` via `claw.describe` v2. Alternative: a thin `claw-channel-memory` shim service that proxies to claw-wall, so when v2 lands the same service produces the digest. Lower v1 cost vs lower v2 refactor cost. Lean claw-wall-direct for v1 with a clear v2 migration note.
4. **Should the v1 default really be opt-in?** §8 argues opt-in for token-cost discipline. Counter-argument: the issue body is a live trading-desk incident and Tiverton operators would prefer opt-out semantics. Lean opt-in but flag.
5. **Spike test scope.** Should the targeted Tiverton-reproduction fixture (§7.5 last bullet) be a new spike (`-tags spike`) or a normal integration test against a faked claw-wall? Lean integration; spike-tag inflates CI cost and requires real provider credentials, which this scenario doesn't need.

## 11. Build sequence (assuming codex sign-off)

1. **claw-wall** — `channel-awareness` endpoint reusing `consumeTail`; new tool handlers; descriptor declares tools. Tests in §7.1.
2. **cllama feed plumbing** — feed name registry already supports per-feed configuration; the new feed flows through with `kind=raw_window` metadata parsing. Tests in §7.2.
3. **compose_up wiring** — `appendConversationWallAwarenessFeed`; pod parser for `x-claw.context.channel-awareness`. Tests in §7.3, §7.4.
4. **Header rewrite for delta path** — minimal change in `channel_context_feed.go`; regression covers existing tests.
5. **Telemetry normalization** — `channel_context_op` event kind, `claw audit` rendering.
6. **Docs sweep** — `docs/CLLAMA_SPEC.md`, ADR (likely new ADR-025 once codex agrees), `skills/clawdapus/SKILL.md` mirror.
7. **Integration test** — Tiverton-reproduction fixture (§7.5).
8. **PR body** explicitly labels phase 1 vs phase 2 per §6.

## 12. Non-goals for this PR (clean cut-line)

- No #164 implementation. This PR depends on #164 *contract* only at the descriptor level; v1 wire surface has no digest section.
- No changes to ADR-021 (memory plane). v1 of #232 produces nothing through `/recall`; v2 does, but only via the #164 adapter, not directly from claw-wall.
- No release artifact changes (no `release_manifest.go` bumps, no changelog `Latest` badge, no nav dropdown). Per repo CLAUDE.md release discipline.
