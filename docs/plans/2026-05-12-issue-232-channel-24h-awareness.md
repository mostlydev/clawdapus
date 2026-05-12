# Issue 232 — Bounded 24h channel awareness independent of cursors and restarts

**Author (draft):** claude (`claude:dbaba2df`) on branch `issue-232-channel-24h-awareness` — for adversarial review by codex before any implementation.

**Revision history:**
- 2026-05-12 r1 — initial draft.
- 2026-05-12 r2 — codex (`codex:27f8b72e`) r1 adversarial pass; folded in: (1) header rewrite now targets the real after-bound tail path (kind discriminator keyed on `after=` presence, not on `mode=delta` — which is no longer the default per #201); (2) explicit tool ACL/auto-subscription via compose-materialized per-agent channel allowlist; (3) v2 producer model clarified — `claw-channel-memory` emits the channel-awareness wire surface and may use #164 internals, but channel events do **not** flow through ADR-021's `/recall` in any phase; (4) v1 default flipped from opt-in to default-on with smaller per-Discord-channel caps, and #232 explicitly stays open through v2 since v1 is not acceptance-complete; (5) feed ordering enforced via compose-emitted manifest order with regression test; (6) retrieval out-of-buffer semantics spelled out (structured `not_in_buffer` response with retained-coverage range).

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
| (2) Rolling 24h digest | **stopgap: omitted** with explicit metadata label `digest=unavailable`; busy rooms rely on retrieval | `claw-channel-memory` service (new) — subscribes to claw-wall events, builds rolling digest (may use #164 salience-memory internals as a library, but channel events do **not** transit ADR-021 `/recall`); emits the **same** `channel-awareness` wire surface with `digest=ready` and an appended digest section | feed: `channel-awareness` (composite block, same name) |
| (3) Retrieval path | `claw-wall` exposes managed tools (ADR-020) `search_channel_context` and `get_channel_messages`, gated by per-agent channel allowlist (see §4.2) | `claw-channel-memory` may offer richer retrieval (e.g., digest-cited source pull); v1 tools remain | `tools[]` |
| (4) Metadata | header-line discipline keyed on `after=` presence (see §4.4), new `channel_context_op` telemetry distinguishing `delta_tail` / `raw_window` / `digest` / `tool_call` | unchanged | feed header + telemetry |

The v1 channel-awareness block is **not** rebranded as "the 24h fix". The plan explicitly labels it as bounded raw-window + retrieval; full 24h coverage under busy-room load is only guaranteed once digest lands. **#232 itself stays open through v2** (codex r1) — v1 ships a meaningful improvement but is not acceptance-complete against the issue body's r2 contract.

**Architectural invariant (codex r1):** in no phase do channel events flow through ADR-021's per-agent `/recall`. `/recall` payload is `messages[]`/`system`/metadata — agent/session shaped, not room-shaped. Channel awareness is its own producer/consumer pair with its own wire surface. #164's salience-memory adapter may be used as an internal library by `claw-channel-memory` (sharing LLM prompts, dedupe heuristics, etc.) but the two adapters do not share the recall/retain wire endpoints.

## 4. Wire surfaces

### 4.1 New feed: `channel-awareness` (v1, producer = claw-wall)

```
GET /channel-awareness?channels=<csv>&since=24h&limit=120&max_chars=12288
```

- **No cursor**. No `consumer=` param. No `mode=delta` path. This feed exists precisely to be cursor-independent.
- Same channel-authorization model as `channel-context` (caller is upstream-restricted to surface-derived channels), PLUS the per-agent ACL of §4.2.1 (the same allowlist guards both feed and tool paths).
- Reuses `consumeTail` semantics from #201 (newest-first walk, dual cap, sort ASC for output) but rebranded.
- v1 defaults (r2): `since=24h`, `limit=60`, `max_chars=8192` (8KB, aligned with cllama's `MaxFeedResponseBytes` so we don't double-truncate). Operator-tunable via `x-claw.context.channel-awareness` (parallel of #201's `x-claw.context.channel`).
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

Both are **mediated** by cllama (ADR-020) so policy/telemetry/auth flow through the standard plane.

#### 4.2.1 Per-agent channel ACL (codex r1 must-fix)

Today's `claw-wall` (`cmd/claw-wall/store.go`, `newHandler`) accepts `channels=` from any caller — auto-generated feed URLs are safe only because compose-up writes surface-derived channel IDs into the URL. Model-supplied tool arguments are **not** safe by construction: an agent could call `search_channel_context(channels=["<other-channel-id>"])` and read a room it has no surface to.

Compose-up materializes a per-agent allowlist at `claw up` time and writes it into the `claw-wall` service config alongside `CLAW_WALL_TOKENS`:

```
CLAW_WALL_AGENT_CHANNELS=<agentID>:<chA>,<chB>;<agentID2>:<chC>,...
```

`agentID` is the bearer-authenticated principal (already validated by claw-wall for the feed path; same path used for tool path). The allowlist is exactly the surface-derived channel set used to materialize that agent's `channel-context` and `channel-awareness` feed URLs — i.e., what the agent already legitimately has access to.

Tool request handler:
1. Authenticate caller → `agentID` (existing Bearer validation; tools share auth with feeds).
2. Parse requested `channels[]`.
3. Reject any channel not in `allowlist[agentID]` with HTTP 403 and a structured `{"error": "channel_not_allowed", "agent": "...", "channel": "..."}` body. cllama surfaces this as a tool-call error to the model with a short hint ("This agent has no surface to channel X; ask the operator to add it.").
4. Pass through to store with the validated channel set.

The same allowlist guards both new tool endpoints and is checked once per request.

#### 4.2.2 Auto-subscription

Both tools are auto-registered for any service whose `x-claw` consumes channel surfaces (i.e., the same condition that auto-injects `claw-wall`). No separate pod knob. Rationale: if the agent has channel surfaces declared, it already needs the tools to recover from cursor/buffer-bound gaps. Forcing operators to opt-in twice (channel surface + retrieval tool) is friction without value.

#### 4.2.3 Out-of-buffer semantics (codex r1 must-fix)

claw-wall can only search/return messages currently in its in-process ring buffer (size from `CLAW_WALL_LIMIT`, default 50, configurable up to operator-set ceiling). When a query falls outside that window — either by `since` reach or by `message_ids` referring to evicted ids — the response is structured:

```json
{
  "messages": [],
  "retained_coverage": {
    "oldest_ts": "2026-05-12T01:14Z",
    "newest_ts": "2026-05-12T05:42Z",
    "buffer_size": 500
  },
  "status": "not_in_buffer",
  "hint": "Requested window extends before buffer's oldest_ts. Operator can widen CLAW_WALL_LIMIT or rely on v2 digest once available."
}
```

`status: "ok"` for normal hits, `"empty"` for in-buffer-but-no-match, `"not_in_buffer"` for the eviction case. cllama renders this as a tool response with header `[channel-tool] kind=tool_call name=... status=not_in_buffer retained_coverage=...` so the model can see explicitly that the gap is buffer-bound, not absence-of-message.

Tool responses include the same `kind=tool_call source=claw-wall channels=…` metadata header as the feed, so the model treats tool output and feed injection symmetrically. Every returned message carries its Discord snowflake `id` so the model can chain `search_channel_context` → `get_channel_messages` deterministically.

### 4.3 Late-context placement and feed ordering (per #204)

The `channel-awareness` block is appended to the late runtime-context system message (OpenAI) or final user content block (Anthropic), alongside the existing `channel-context` delta block and the memory recall block.

Order inside the late block (deterministic for cache-friendliness):

1. memory recall (existing, from ADR-021)
2. `channel-awareness` (new — broadest layer, anchors the model in the last 24h)
3. `channel-context` delta (existing, narrowest layer, fresh post-cursor)
4. current time line (existing)

Rationale: digest/awareness first sets the model's frame of "what the room knows"; delta then signals "what's new since you last looked"; time anchors the moment. Reversing this lets a stale-but-large window dominate fresh signal.

#### 4.3.1 Manifest-order enforcement (codex r1 must-fix)

cllama's `fetchFeeds` / `FormatAllFeeds` (`cllama/internal/feeds/inject.go`) iterate the feed manifest in order. There is no sort step today. The r1 plan said "order inside the late block" without saying who enforces it; codex flagged this. v1 enforces ordering at the **compose-up emission** side:

- `appendConversationWallAwarenessFeed` emits the awareness entry **before** `appendConversationWallFeed` emits the delta entry, both immediately after any memory recall slot.
- Regression test `TestConversationWallFeedsEmittedInAwarenessBeforeDeltaOrder` asserts the manifest order matches the §4.3 list.
- A cllama-side deterministic sort for channel-flavored feeds is **deferred** (would require feed-name introspection in `inject.go`). Manifest-order discipline is sufficient because compose-up is the single writer.

### 4.4 Header discipline (part 4 of contract)

Every channel-flavored block carries an explicit `kind=` field in its header. The implicit assumption "channel-context = full room" is the bug we are killing.

**Important (codex r1 must-fix):** the discriminator is keyed on the **presence of an `after=` cursor in the fetch URL**, not on the legacy `mode=delta` query param. Since #201, the default `channel-context` fetch uses `mode=tail`, and cllama injects `after=` from its cursor ledger when one exists (see `cllama/internal/proxy/channel_context_feed.go::prepareChannelContextFeed`). The r1 plan's "rewrite mode=delta header" missed this — the real steady-state delta path is `mode=tail` with an `after=` injection, and that is the path the model actually sees and misreads.

| Source URL pattern | Block kind | Header prefix | Notes |
|--------------------|------------|---------------|-------|
| `channel-context` with `after=` (steady-state, post-cursor) | `delta_tail` | `[channel-context delta] kind=delta_tail cursor=<ch>:<id>,...` | the headline rewrite; today emits `[channel-context]` with no kind |
| `channel-context` without `after=` (epoch bootstrap, first turn, empty cursor map) | `tail` | `[channel-context] kind=tail` | one-shot bootstrap or legacy; the cllama-side decision struct (`channelContextPrepareDecision.AppliedAfter == false`) is the trigger |
| `channel-awareness` (always uncursored) | `raw_window` | `[channel-awareness] kind=raw_window since=...` | v1 default |
| `channel-awareness` v2 composite | `raw_window` + `digest` sections | `[channel-awareness] kind=raw_window+digest since=... digest_source_count=...` | v2 only; v1 emits `digest=unavailable` instead |
| Tool response | `tool_call` | `[channel-tool] kind=tool_call name=... status=...` | retrieval tool output |

The header rewrite is generated in claw-wall's response formatter (it already produces the `[channel-context] mode=tail …` line per #201 §4.3). claw-wall doesn't know whether cllama is injecting `after=` against it; but the `mode=tail`-with-`after=` URL parameter is visible to claw-wall on the request, so it can switch the header based on that signal directly. v2 of #204's `coverage_partial=true` annotation is preserved verbatim.

**Back-compat note:** model prompts that contained the old `[channel-context]` header for delta will now see `[channel-context delta] kind=delta_tail` once v1 ships. This is the intended behavior — the old prefix was the bug. No tool/agent code parses this header; only the model reads it.

### 4.5 Telemetry (part 4 of contract)

New normalized event kind: `channel_context_op` with fields `{kind, channels[], retained, returned, omitted, latency_ms, source, status}`. Emitted for:
- every channel-awareness fetch (kind=raw_window)
- every channel-context fetch — kind=delta_tail if `after=` was present, kind=tail otherwise — normalizes the existing claw-wall log line through cllama
- every retrieval tool call (kind=tool_call) with `status` from §4.2.3 (`ok` / `empty` / `not_in_buffer`)
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
    channel-awareness:          # NEW — defaults applied automatically; pod block only needed for overrides
      since: 24h
      limit: 60                 # default (r2: tightened from 120)
      max_chars: 8192           # default (r2: tightened from 12288)
```

Service-level overrides via the same pod-defaults pattern (`x-claw.context.channel-awareness` on individual services). There is **no** `enabled` knob — see §8; the feed is emitted automatically when the service consumes any channel surface, matching the auto-injection of `claw-wall` itself.

`appendConversationWallFeed` (in `cmd/claw/compose_up.go`) gains a sibling `appendConversationWallAwarenessFeed` that emits `/channel-awareness?channels=...&since=...&limit=...&max_chars=...` whenever the service has any consumed channel IDs. Tool registration uses the existing claw-wall descriptor extraction path. A second emission writes `CLAW_WALL_AGENT_CHANNELS` into the auto-injected `claw-wall` service env, per §4.2.1.

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
- `TestChannelContextHeaderRewriteByAfterPresence` — same handler, two requests: one with `after=…` query param emits `[channel-context delta] kind=delta_tail`; one without emits `[channel-context] kind=tail`. (codex r1 must-fix)
- `TestPerAgentChannelACLAllowsListed` — `CLAW_WALL_AGENT_CHANNELS=A:ch1,ch2` + Bearer for A; tool request for `channels=[ch1]` → 200 with messages.
- `TestPerAgentChannelACLRejectsUnlisted` — same env; tool request for `channels=[ch3]` → 403 with `{"error":"channel_not_allowed",...}`.
- `TestRetrievalToolNotInBufferStatus` — buffer has 10min; tool query with `since=12h` returns `status=not_in_buffer` + `retained_coverage` shape per §4.2.3.
- `TestRetrievalToolEmptyVsNotInBuffer` — distinguishes `status=empty` (in-buffer, no match) from `status=not_in_buffer` (out-of-buffer).
- `TestGetChannelMessagesByIDRange` — fetch by id range returns exact messages, ordered, each carrying its snowflake id.

### 7.2 cllama (`cllama/internal/proxy/`)

- `TestChannelAwarenessFeedIsFetchedWithoutCursor` — register a `channel-awareness` feed entry; assert URL has no `after=` and `X-Claw-Consumer-Session-Epoch` is **not** consulted for it (orthogonal to #220's epoch).
- `TestChannelAwarenessAndDeltaCoExist` — pod wires both feeds; both blocks appear in the late-context section, in the §4.3 order, with their respective `kind=` headers.
- `TestChannelContextHeaderRewriteFlowsThroughCllama` — claw-wall returns `[channel-context delta] kind=delta_tail` for an `after=`-bound fetch; cllama formats it into the late context without further mutation. (codex r1 must-fix)
- `TestChannelContextOpTelemetryNormalized` — fetch each kind, assert `audit.go`-style normalized records for `kind=raw_window`, `kind=delta_tail`, `kind=tail`, `kind=tool_call`.

### 7.3 compose_up (`cmd/claw/`)

- `TestAppendChannelAwarenessFeedHonorsPodConfig` — pod-level `x-claw.context.channel-awareness` overrides flow into generated URL.
- `TestChannelAwarenessFeedDefaultOnWithChannelSurface` — service consumes a Discord channel surface; pod has no explicit `channel-awareness` block; feed entry IS emitted with default `since=24h&limit=60&max_chars=8192`. (codex r1, default-on)
- `TestChannelAwarenessFeedAbsentWithoutChannelSurface` — service has no channel surface; no awareness feed entry emitted.
- `TestConversationWallFeedsEmittedInAwarenessBeforeDeltaOrder` — manifest-order regression: when both feeds are emitted, awareness appears before delta in the materialized `feeds.json`. (codex r1 must-fix §4.3.1)
- `TestComposeMaterializesAgentChannelAllowlist` — when claw-wall is auto-injected, the service env contains `CLAW_WALL_AGENT_CHANNELS=<agentID>:<chA>,<chB>;...` reflecting each consuming agent's surface-derived channel set. (codex r1 must-fix §4.2.1)
- `TestChannelAwarenessToolsRegisteredFromDescriptor` — claw-wall descriptor extraction picks up the two tool entries; tools auto-subscribe to any service with a channel surface (§4.2.2).

### 7.4 Pod parser (`internal/pod/`)

- `TestParseChannelAwarenessConfig` — accepts `since` / `limit` / `max_chars`; rejects negatives; rejects unparseable durations. No `enabled` field (default-on).
- `TestPodDefaultsChannelAwareness` — service-level overrides pod defaults using the standard pattern.

### 7.5 Integration / spike

- Extend `TestSpikeRollCall` (or a sibling spike) so that the rollcall pod's `channel-awareness` feed body contains a header-line `kind=raw_window` and matches the retained-coverage shape. The spike already exercises real LLM calls through cllama; this just asserts the new feed flows end-to-end.
- A targeted fixture test reproduces the Tiverton case: pre-seed `claw-wall` buffer with messages spanning the cursor, run a turn that advances cursor past Logan's hypothetical CMCSA message, then run a second turn with a human-mention prompt and assert the `channel-awareness` block contains Logan's message even though the `channel-context` delta does not.

## 8. Default behavior and ramp (revised r2)

**Codex r1 made the v1-opt-in posture untenable:** if v1 is opt-in and explicitly partial, then operators who don't flip the knob get nothing, and #232 closes on a stopgap that pods haven't actually adopted. The new default is **on**, with smaller caps tuned to Discord channel density, and `digest=unavailable` honestly documenting the gap in v1.

v1 defaults (active when the service consumes any channel surface, no explicit opt-in needed):

| Knob | v1 default | r1 draft | Rationale |
|------|-----------|----------|-----------|
| `enabled` | `true` (implicit) | `false` (explicit opt-in) | codex r1: opt-in fails the issue's acceptance contract |
| `since` | `24h` | `24h` | unchanged |
| `limit` | `60` | `120` | r1 caps were generous; smaller default reflects "raw window + retrieval pressure valve" model |
| `max_chars` | `8192` (8KB) | `12288` (12KB) | aligned with cllama's `MaxFeedResponseBytes` so we don't risk double-truncation |

Per-pod overrides still available via `x-claw.context.channel-awareness`. Operators with low-density rooms can raise the caps; high-traffic rooms keep the smaller defaults and lean on retrieval until v2.

**#232 stays open through v2.** Per codex r1: v1 is a meaningful improvement (raw-window + retrieval + metadata) but does not satisfy the issue body's r2 acceptance contract ("guaranteed 24h awareness regardless of session state and trigger"). The PR that lands v1 is titled `#232 phase 1: raw-window + retrieval + metadata` and closes #232 only when v2 (digest via `claw-channel-memory`) ships.

The PR body explicitly says:
> Phase 1 of #232. Lands the raw-window feed, retrieval tools, and metadata header discipline; busy rooms still rely on retrieval to close gaps. Phase 2 (rolling digest via `claw-channel-memory`) lands when #164 is far enough along to share its salience primitives. #232 stays open through both phases.

## 9. Out of scope (deliberately)

- **Cross-channel digest correlation.** v1 and v2 both keep channel-awareness scoped per-channel within a single feed call. Cross-channel synthesis is a future memory adapter feature.
- **Affect/salience scoring** of channel messages. Same #164 territory.
- **Discord history backfill** beyond claw-wall's in-process buffer. Per #201 §3 Q3, the wider-continuity feeds cover cold-start. Operators who need a true 24h window today can raise `CLAW_WALL_LIMIT`/buffer; v2's digest reduces the urgency further.
- **Per-channel awareness/delta toggles.** v1 keeps the pod/service knob granularity from #201.
- **Anthropic `cache_control` markers** for awareness blocks. Same deferral as #204 v2.

## 10. r1 open questions — codex answers and r2 resolution

(Original five questions kept for traceability; codex r1 answers and r2 outcome inline.)

1. **Composite vs co-injection.** r1 leaned two-feed.
   - **Codex r1:** two-feed OK for v1 *if* rendered/ordered as one coherent channel layer.
   - **r2 resolution:** two feeds (`channel-awareness`, `channel-context`) with compose-emitted manifest order enforced (§4.3.1). Telemetry treats them as one logical layer via `channel_context_op`. v2 can collapse to one feed if useful but is not required.

2. **Header rewrite on `channel-context`.** r1 said "change to `[channel-context delta] kind=delta`".
   - **Codex r1 must-fix:** rewrite targeted the wrong path (`mode=delta` is no longer the default since #201). The real delta path is `mode=tail` + `after=`.
   - **r2 resolution:** rewrite is keyed on `after=` presence. See §4.4 for the four kinds (`delta_tail`, `tail`, `raw_window`, `tool_call`).

3. **v1 retrieval tools live where?** r1 leaned claw-wall-direct.
   - **Codex r1:** claw-wall-direct OK *only after* ACL/auto-subscribe is solved.
   - **r2 resolution:** claw-wall-direct with §4.2.1 per-agent channel allowlist materialized at compose time + §4.2.2 auto-subscription. ACL is the gating condition; not a separate phase.

4. **v1 default opt-in vs opt-out.** r1 leaned opt-in for token cost discipline.
   - **Codex r1:** opt-in means v1 is not acceptance-complete; either split issues or make default-on with smaller caps.
   - **r2 resolution:** default-on with smaller caps (`limit=60`, `max_chars=8192`), and #232 stays open through v2 (§8). The smaller caps keep token-cost discipline without requiring operators to flip a knob.

5. **Spike test scope.** r1 leaned plain integration over spike-tagged.
   - **Codex r1:** plain integration, fake claw-wall, no spike-tag.
   - **r2 resolution:** confirmed — §7.5 targeted reproduction is a normal integration test.

## 10b. Open questions for codex r2 review

1. **Compose CLAW_WALL_AGENT_CHANNELS env shape.** §4.2.1 specifies `<agentID>:<chA>,<chB>;<agentID2>:...` as a single env var. Alternative: a separate small JSON file mounted into claw-wall (e.g., `claw-wall-agent-channels.json`) like the cllama memory.json pattern. Env-var keeps wiring simple but is shell-fragile for large pods; JSON is more idiomatic but adds a mount. Lean env-var for v1 because the value is small and pure ASCII.
2. **Tool error rendering.** §4.2.3 specifies `status=not_in_buffer` with a `hint` field. Should cllama's tool-call telemetry record `status` separately from `error_class`, or fold it into the existing `tool_call_result` event? Lean separate so audits can filter "the model retrieved nothing" cases without parsing free-text hints.
3. **digest=unavailable signaling.** v1 header always carries `digest=unavailable`. Should the model see this once at the top of `channel-awareness` or repeated in `channel-context delta` blocks too? Lean once (in awareness only) — repeating it in delta would invite the model to assume delta is the entire room when digest is unavailable, which is the original bug.
4. **`claw-channel-memory` service naming and image location.** v2 introduces a new service. Live under `examples/channel-memory/` like `examples/reference-memory/`, or directly as `cmd/claw-channel-memory/` + published image? Lean `examples/` for v2 initial wave (operator-visible but not yet a published infra image), promote later. Matches #164's `examples/salience-memory/` pattern.

## 11. Build sequence (assuming codex r2 sign-off)

1. **claw-wall** — `channel-awareness` endpoint reusing `consumeTail`; per-agent channel ACL (§4.2.1) parsing `CLAW_WALL_AGENT_CHANNELS`; new tool handlers (`search_channel_context`, `get_channel_messages`) with `not_in_buffer` semantics (§4.2.3); descriptor declares tools. Tests in §7.1.
2. **compose_up wiring** — `appendConversationWallAwarenessFeed`; pod parser for `x-claw.context.channel-awareness`; ACL allowlist materialization writes `CLAW_WALL_AGENT_CHANNELS` into the `claw-wall` service env; manifest-order discipline emits awareness before delta. Tests in §7.3, §7.4.
3. **Header rewrite (claw-wall side)** — `channel-context` response formatter switches on `after=` presence to emit `[channel-context delta] kind=delta_tail` vs `[channel-context] kind=tail`. Tests in §7.1.
4. **cllama feed plumbing** — feed name registry already supports per-feed configuration; the new feed flows through with `kind=raw_window` metadata parsing. Tests in §7.2.
5. **Telemetry normalization** — `channel_context_op` event kind, `claw audit` rendering; `tool_call_result` status fold-in (or new field per §10b.2).
6. **Docs sweep** — `docs/CLLAMA_SPEC.md`, ADR (likely new ADR-025 once codex agrees), `skills/clawdapus/SKILL.md` mirror.
7. **Integration test** — Tiverton-reproduction fixture (§7.5).
8. **PR body** explicitly labels phase 1 vs phase 2 per §6; does **not** close #232.

## 12. Non-goals for this PR (clean cut-line)

- No #164 implementation. This PR depends on #164 *contract* only at the descriptor level; v1 wire surface has no digest section.
- **No channel events flow through ADR-021's `/recall` in any phase.** v1 produces only through the `channel-awareness` feed and the retrieval tools. v2 produces only through the same `channel-awareness` feed and the same retrieval tools, via `claw-channel-memory` as the new producer; the v2 producer may use #164's salience-memory primitives **as a library**, but channel events do not transit per-agent `/recall`/`/retain`.
- No release artifact changes (no `release_manifest.go` bumps, no changelog `Latest` badge, no nav dropdown). Per repo CLAUDE.md release discipline.
