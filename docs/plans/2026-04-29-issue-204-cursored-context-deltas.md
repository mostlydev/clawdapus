# Issue #204 — Cursored append-only Discord context, not first-system rewrites

**Status:** implemented by Codex on branch `issue-204-cursored-live-context`; awaiting Claude review and release.

**Linked:** [#204](https://github.com/mostlydev/clawdapus/issues/204). Predecessor: #201 / v0.13.7 `mode=tail`.

**Author:** Claude (df3fed3a) on branch `issue-204-cursored-live-context`. Codex review at [#204 comment](https://github.com/mostlydev/clawdapus/issues/204#issuecomment-4341099912) drove rev2.

## TL;DR

cllama currently appends the entire feed block + memory recall + the current time line into `messages[0]["content"]` (or top-level `system` for Anthropic) on every request. That mutates the first system message every turn, even when nothing about the agent's contract has changed. Three concrete consequences from the live Tiverton incident:

1. OpenRouter sticky-routing/cache identity is computed against the first system message + first non-system message. Mutating the first system kills cache hit ratio on cache-supported providers and changes conversation identity for sticky routing.
2. Every Discord mention re-pastes a 24h channel transcript into system, even when only one new message arrived.
3. The `refreshed <ts>` line baked into every feed header changes the rendered text on every TTL refresh, even for byte-identical feed content.

**v1 fix in this issue**: stop mutating the first system message for volatile content. Move feed blocks + memory recall + time context + live channel context to late runtime context outside the stable contract. OpenAI-compatible requests use a later `system` message inserted immediately before the invoking user message; Anthropic requests use a separate `user` message inserted immediately after the final user message so top-level `system` and the existing conversation prefix stay byte-stable. Add a cllama-side per-agent/channel cursor so live channel context is fetched as a delta-since-watermark instead of a full tail every turn. Coverage on cap overflow stays the v0.13.7 plain-text header plus an explicit `coverage_partial=true omitted_after_cursor=N` annotation when capping leaves messages unrepresented.

**v1 does not add a synthetic first-non-system anchor.** Instead, the implementation avoids putting volatile context before the existing first user message. For normal OpenAI and Anthropic conversations this keeps `first_non_system_hash` stable while still surfacing drift if a runner sends no stable non-system message or later provider formatting changes.

Everything else from the issue body — dispatch fallback policy, tool manifest pruning, session-history `token too long` fix, Anthropic `cache_control` markers, LLM summarization of overflow — is **deferred to follow-up issues** to keep this PR shippable.

## What we are not changing in v1

- **Dispatch fallback on transport timeout / 5xx** (issue §H) → split out as #205.
- **Tool manifest pruning** (issue §I) → split out as #206.
- **Session-history `bufio.Scanner: token too long`** (issue §J) → split out as #207.
- **Anthropic `cache_control` markers**. v1 stops mutating top-level `system` and routes volatile context into a final user-role content block. Explicit cache breakpoints come in v2 once telemetry proves it matters.
- **LLM-generated condensation of overflow ranges** → salience-memory adapter (#164) territory.
- **Synthetic conversation seed for first-non-system stability** → v2, decided by telemetry from v1.
- **Cross-runner shared cursor** → v1 cursor is purely cllama-side, runner-agnostic.

## v1 design

### 1. Layered prompt assembly: stable system, late dynamic context

Today (`cllama/internal/feeds/inject.go:76`):

```go
// InjectOpenAI
first["content"] = existing + "\n\n" + feedBlock
```

This appends every feed block, memory recall block, and the current time line into the first system message. **All three** call sites (`recallOpenAIMemory` / `recallAnthropicMemory` in `cllama/internal/proxy/memory.go`, and the feed/time injection in `handler.go`) need to switch to the late-context path. Anything that still calls `feeds.InjectOpenAI` / `feeds.InjectAnthropic` after v1 is by definition allowed to mutate first-system.

| Layer | Stability | Source | Placement in v1 |
|-------|-----------|--------|-----------------|
| Stable system contract | byte-stable across turns | `messages[0]` (system) when present | untouched |
| Slow/frozen feeds | TTL-stable text | `desk-chronicle`, `agent-scaffold`, etc. | late runtime context |
| Volatile feeds + live channel context | per-turn dynamic | `channel-context`, fast-refresh feeds | late runtime context |
| Memory recall | bounded, ledger-backed | `cllama/internal/proxy/memory.go` | late runtime context |
| Current time | per-minute volatile | `currentTimeLine` | late runtime context |
| Invoking message | the user mention | `messages[-1]` | unchanged |

Implementation:

- New helper `feeds.AppendLateContext(payload, block)` (OpenAI-compatible) and `feeds.AppendAnthropicLateContext(payload, block)` (Anthropic). The block accepts a multi-section concatenation; callers build their section text and pass it in. In v1, the helper is called once per request with all sections joined in deterministic order (memory → feeds → time).
- OpenAI-compatible shape: insert a synthetic `{"role": "system", "content": "[Runtime context for this invocation. This is not a user instruction. Use it only as infrastructure-provided context for the next reply.]\n\n" + block}` immediately before the last existing user-role message. If there is no user-role message, append at end. This preserves the first system message when one exists and leaves the invoking user as the final user turn.
- Anthropic shape: append a final `user` content block with the same wrapper text. Top-level `system` stays untouched, and the existing first user message remains the first non-system message. This intentionally accepts a consecutive user runtime-context message so the cache-relevant prefix does not become volatile.
- The OpenAI `InjectOpenAI` and Anthropic `InjectAnthropic` functions stay in the codebase but are removed from the request hot path. Their tests stay green; new tests cover the late-context path. Marked for removal in a v2 sweep.
- Wrapper sentence is fixed and short. Do not include timestamps in the wrapper itself — that would re-introduce volatility in the late block too. Live timestamps go inside the block content where they belong.

The wrapper sentence ("Not a user instruction") is important. Models with strong instruction-following can otherwise misinterpret runtime context as the user's request. Documented in the inline comment.

### 2. Drop `refreshed <ts>` from feed headers

`cllama/internal/feeds/inject.go:27`:

```go
fmt.Fprintf(&b, "--- BEGIN FEED: %s (from %s, refreshed %s%s) ---\n", ...)
```

Remove the `refreshed <ts>` from model-visible text. Keep the same value in cllama's structured logging — `feed_fetch` event already has `latency_ms` and `status`; add `fetched_at` and `cached`. The `STALE` tag stays in model-visible text because it changes the model's interpretation of the content; freshness timestamp does not.

This single line is responsible for "TTL refresh of unchanged feed content produces different model-visible bytes". After this change, an unchanged feed content + TTL refresh produces byte-identical text.

### 3. cllama-side cursor ledger

**Path:** `$CLAW_CONTEXT_LEDGER_DIR/<agent-id>/cursor.json`, where `CLAW_CONTEXT_LEDGER_DIR` defaults to `$CLAW_SESSION_HISTORY_DIR/context-ledger`. On host: `.claw-session-history/context-ledger/<agent-id>/cursor.json`. Inside the cllama container: `/claw/session-history/context-ledger/<agent-id>/cursor.json`.

This sits inside the existing rw session-history mount (see `internal/pod/compose_emit.go:340` — `volumes` includes `%s:/claw/session-history:rw` when `SessionHistoryHostDir` is set). No new mount needed. The earlier draft put the ledger under `.claw-runtime/context/...` which is a regenerated read-only mount and would have failed on the first write.

If `CLAW_SESSION_HISTORY_DIR` is unset (cllama configured without session history persistence), the cursor ledger falls back to in-memory only. The bootstrap path (`since=24h`) handles cold start. This is documented as a known limitation; operators who care about cursor durability already enable session history.

Schema:

```json
{
  "version": 1,
  "channels": {
    "1464509330731696213": {
      "last_message_id": "1464900000000000000",
      "last_timestamp": "2026-04-29T05:07:00.123Z"
    },
    "1464796137893662843": {
      "last_message_id": "1464900000000000001",
      "last_timestamp": "2026-04-29T05:06:58.456Z"
    }
  }
}
```

**Cursor scope:** keyed by `agent_id` (which already maps 1:1 to ordinal services for `count > 1`). Channels are vector-cursored. An agent that loses access to a channel keeps its stale cursor entry; recall ignores it.

**Commit point: from the successful record path, not from observing 2xx.** Specifically, the cursor advances **after** `recordResponse` (the session-history writer) completes for a 2xx response. Rationale (per codex review):

- Streaming: if streaming fails before record (e.g. client disconnect mid-stream, upstream truncates), no commit happens because record never finalizes.
- Non-streaming 5xx: forwarded to the client but never recorded as a successful turn → no commit.
- Non-streaming 2xx: recorded → cursor commits.
- Recorder write error: log a warning, **still commit the cursor**, because the model already saw the prompt and the runner already saw the response. Failing to commit here would re-paste the same delta on the next turn for content the model has already used — wasteful but not incorrect. The tradeoff is documented and operators can monitor recorder errors via existing telemetry.
- Cursor-write disk error: log a warning, do not fail the request. Worst case is duplicate delta next turn.

Alternate path considered: commit inside `dispatchWithRetry` immediately after seeing 2xx. Rejected because it does not naturally handle streaming truncation, and it couples cursor commit to status-code observation rather than to "the model actually used this delta".

### 4. claw-wall: optional `after=` cursor on `mode=tail`

`since=` is already used by claw-wall as a duration window (`since=24h`). The cursor input must use a different query key. v1 adds `after=`.

`cmd/claw-wall/store.go` handler change:

- Accept an optional `after=` query param.
- Format: `after=A:<msg_a>,B:<msg_b>` (comma-separated `<channel_id>:<message_id>` pairs).
- Channels named in `after=` but not in `channels=` are a 400.
- Channels in `channels=` but not in `after=` get the existing default tail behavior (treated as bootstrap for that channel).
- When `after=` is present, the tail filter walks newest→oldest until it crosses the per-channel watermark, then stops or hits limit/max_chars. Dual-cap behavior from v0.13.7 is preserved.
- `since=` (duration) and `after=` (cursor map) compose: `since` bounds how far back claw-wall will walk on bootstrap channels; `after` bounds how far back on cursored channels. Both can be present.
- Coverage header: header gains `after=A:msg_a,B:msg_b` when applied. Empty when bootstrap.

Wire shape:

```
GET /channel-context?consumer={claw_id}&channels=A,B&mode=tail&since=24h&limit=40&max_chars=8192&after=A:1464900000,B:1464900001
```

The compose-generated feed path stays static; cllama rewrites the URL at fetch time to inject `after=...` based on its ledger. Feed-manifest layer unchanged.

### 5. Cap-overflow coverage annotation

claw-wall already emits `[omitted N older retained messages due to max_chars; newest retained messages follow]` on dual-cap pressure (v0.13.7). cllama propagates that text unchanged.

When `after=` is present and the cap fires, the response covers the newest tail but does not represent every message after the cursor. Per codex review, the annotation should describe what is provable from store metadata, not invent gap claims.

cllama appends one line to the late context block when this happens:

```
[channel-context delta] coverage_partial=true omitted_after_cursor=N newest_returned=2026-04-29T05:42Z
```

`omitted_after_cursor=N` is computed by cllama as `available_in_window − messages_returned` from the existing claw-wall coverage header. The cursor still advances to the newest message returned (not back to the cursor) — this is at-least-once delivery: the agent sees the newest tail; the omitted middle range is acknowledged but not silently re-fetched (because future deltas use the new newest watermark, not the old cursor).

When the response is in-window and uncapped, no annotation.

### 6. Hash telemetry

cllama logger gains five hash fields per request event:

- `static_system_hash` — sha256 of `messages[0]["content"]` for OpenAI / `system` field for Anthropic.
- `first_system_hash` — same as `static_system_hash` for v1 (kept distinct so v2's Anthropic `cache_control` work has a place to differentiate).
- `first_non_system_hash` — sha256 of the first non-system message. The implementation avoids putting volatile context before the existing first user turn, so normal OpenAI and Anthropic conversations should keep this stable. Telemetry still surfaces drift so v2 can decide whether a synthetic anchor is justified.
- `dynamic_context_hash` — sha256 of the late-context block we inserted.
- `tools_hash` — sha256 of `payload["tools"]` after JSON canonicalization with sorted keys.

Plus pass-through of provider cache fields when present in the upstream response:

- `usage.prompt_tokens_details.cached_tokens` → `cached_tokens`
- `usage.prompt_tokens_details.cache_write_tokens` and Anthropic `cache_creation_input_tokens` → `cache_write_tokens`
- Anthropic `cache_read_input_tokens` → `cached_tokens`

Acceptance for "first system is stable across turns" becomes hash-comparable in tests instead of vibe-based.

### 7. Acceptance criteria (v1)

1. With identical agent contract and zero new Discord messages, identical feed content, and the same rendered minute-level time line, two consecutive mentions produce identical `static_system_hash` and identical `dynamic_context_hash`.
2. With identical agent contract and one new Discord message, two consecutive mentions produce identical `static_system_hash` and different `dynamic_context_hash`.
3. TTL refresh of unchanged feed content produces identical `static_system_hash`; it also produces identical `dynamic_context_hash` when the rendered time line is unchanged.
4. Steady-state mention with N>0 new messages includes only those new messages in the late context block, not the whole 24h tail.
5. Bootstrap mention (no cursor on disk) includes the v0.13.7 tail (`since=24h`, dual-capped, coverage header).
6. Failed upstream (5xx, transport timeout, 4xx) does not advance the cursor; the next mention re-fetches the same delta.
7. Streaming response truncated mid-stream does not advance the cursor.
8. Recorder write error is logged but does not block cursor commit (documented tradeoff).
9. Cursor file is per-agent and isolated per ordinal in `count > 1` services.
10. `coverage_partial=true omitted_after_cursor=N` line appears when cap pressure left messages unrepresented; absent when the response covers everything-after-cursor.
11. `[channel-context]` v0.13.7 tail header is preserved in bootstrap and steady-state.
12. cllama log records `static_system_hash`, `first_system_hash`, `first_non_system_hash`, `dynamic_context_hash`, `tools_hash`, plus `cached_tokens` and `cache_write_tokens` when OpenAI-compatible or Anthropic providers return them.
13. Memory recall is included in the late-context block; calling `feeds.InjectOpenAI` / `feeds.InjectAnthropic` from the request hot path is grep-clean.

## Tests

### Unit (cllama/internal/feeds/inject_test.go + new files)

- `AppendLateContext` does not mutate `messages[0]` when its role is `system`.
- `AppendLateContext` inserts a later system message before the last user message.
- `AppendLateContext` with no user message appends at end.
- `FormatFeedBlock` no longer includes the `refreshed` timestamp in model-visible text; STALE tag still present.

### Unit (cllama/internal/proxy/memory_test.go)

- `recallOpenAIMemory` / `recallAnthropicMemory` route memory blocks through `AppendLateContext`, not `InjectOpenAI` / `InjectAnthropic`.

### Unit (cllama/internal/cursor/cursor_test.go, new)

- Cursor ledger round-trips: write, re-read, cursor advances monotonically per channel.
- Cursor commit only after recorder finalizes a 2xx; cursor untouched on 4xx, 5xx, streaming truncation.
- Recorder write error logs a warning but commits the cursor; tested via fault-injected recorder.
- Multi-channel merge: deterministic by `(ts, channel_id, message_id)`.
- Cold start (no cursor on disk) → bootstrap fetch path.
- Missing `CLAW_SESSION_HISTORY_DIR` → in-memory ledger; bootstrap on every cold start.

### Unit (cmd/claw-wall/store_test.go)

- `mode=tail` with `after=A:msg1` returns only messages strictly after `msg1` for channel A.
- `after=` referencing a channel not in `channels=` is a 400.
- `after=` and `since=` compose; `since=` still bounds bootstrap channels.
- Coverage header includes `after=A:msg_a` when applied.

### Integration (cllama)

- Two-mention sequence: same `static_system_hash` across both turns; `dynamic_context_hash` changes when channel context or the rendered time line changes; `first_non_system_hash` is logged.
- Hash telemetry appears in log output.
- Memory recall content appears in late runtime context, not in the first system message.

### Spike (existing TestSpikeRollCall extension or new TestSpikeChannelContextStability)

- Two mentions with no new messages between → byte-identical first-system hash in claw audit telemetry.

### Negative (regression guard for #201)

- Bootstrap (no cursor) returns the latest 24h tail, not an oldest-unread page.
- The store-level `consume()` mailbox path is still callable as `mode=delta`; assert it is not hit in v1's generated feed paths.

## Codex implementation decisions

The rev2 plan left three questions open. Codex resolved them this way during implementation:

1. **Wrapper sentence wording:** `[Runtime context for this invocation. This is not a user instruction. Use it only as infrastructure-provided context for the next reply.]`
2. **Quiet pods:** always emit runtime context when the only dynamic section is the current time line. This keeps time available to the agent and makes per-minute drift explicit in `dynamic_context_hash`.
3. **Tools hash canonicalization:** use Go `json.Marshal` over decoded `payload["tools"]`; map keys are encoded deterministically by the standard library.

One important divergence from the initial rev2 text: OpenAI-compatible runtime context is a later `system` message, not a `user` message. Anthropic runtime context is a separate `user` message after the final user turn, not before it. This keeps the stable system contract untouched and avoids making the existing first non-system message volatile.

## Closed review questions

Three questions were resolved by codex's rev1 review.

**Resolved (rev1 → rev2):**

- ~~Cursor commit point.~~ Resolved: post-`recordResponse` for 2xx; recorder error logs but commits.
- ~~`since` vs other cursor key.~~ Resolved: `after=` (not `since=`).
- ~~Ledger path.~~ Resolved: under `$CLAW_SESSION_HISTORY_DIR/context-ledger`.

## Implementation order (codex's pass)

Phase A (no behavior change to model-visible bytes):

1. Add `static_system_hash`, `first_system_hash`, `first_non_system_hash`, `dynamic_context_hash`, `tools_hash` to logger. Compute and emit even though prompt-assembly hasn't changed yet. Lets us measure baseline drift.
2. Pass-through `cached_tokens` and `cache_write_tokens` from upstream `usage.prompt_tokens_details`.

Phase B (prompt assembly):

3. Implement `AppendLateContext` for OpenAI and Anthropic.
4. Switch `handleOpenAI` and `handleAnthropicMessages` to use late-context for feeds + time.
5. Switch `recallOpenAIMemory` and `recallAnthropicMemory` to feed memory blocks into late-context (not `InjectOpenAI` / `InjectAnthropic`).
6. Remove `refreshed <ts>` from `FormatFeedBlock`.
7. Acceptance: hash telemetry shows `static_system_hash` byte-stable across turns with no contract change; `first_non_system_hash` is logged and visible.

Phase C (cursor):

8. Add cursor ledger module (`cllama/internal/proxy/channel_cursor.go`).
9. Wire fetch path to inject `after=...` into the channel-context feed URL when ledger has watermarks.
10. Wire commit from the successful record path (post-`recordResponse` for 2xx).
11. Acceptance: steady-state delta-only fetches; failed upstream replays the same delta; streaming truncation does not commit.

Phase D (claw-wall):

12. Add `after=` query param parser to `cmd/claw-wall/store.go`.
13. Implement per-channel filter on top of v0.13.7 tail.
14. `coverage_partial` annotation in cllama's late-context block when applicable.

Phase E (release):

15. Changelog, docs sweep (pod-yaml.md, social-topology.md, cllama.md once #203 lands or as part of this), tests, release skill.

Phases A and B are independently shippable; A first (telemetry-only) gives a baseline measurement before B changes the prompt shape. C and D are tightly coupled.

## Out-of-scope follow-up issues filed

- #205 — dispatchWithRetry fallback policy on transport timeout / 5xx
- #206 — tool manifest bloat (trigger-scoped exposure)
- #207 — session-history `bufio.Scanner: token too long`
- v2: Anthropic `cache_control` markers, synthetic first-non-system anchor (driven by v1 telemetry)

## Operator deploy path

After release: SSH to clawdbot@tiverton, run `claw pull && claw up -d`, send a Tiverton mention, then a second one with no Discord activity in between. Two `claw audit` rows should show:

- Identical `static_system_hash`.
- Identical `tools_hash`.
- `first_non_system_hash` present; it should stay stable when the runner preserves the same first user message.
- On a cache-supported provider: non-zero `cached_tokens` on the second turn. Zero `cached_tokens` on a documented-cache-supported provider is the v2 signal that we need a stable first-non-system anchor.

No `x-claw.context.channel` config change required. The bootstrap → steady-state cursor transition is automatic on first commit.
