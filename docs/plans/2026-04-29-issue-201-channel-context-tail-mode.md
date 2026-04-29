# Plan — Issue #201: claw-wall channel-context returns oldest unread page instead of latest tail

**Issue:** https://github.com/mostlydev/clawdapus/issues/201
**Branch:** `issue-201-channel-context-tail` (already created by codex)
**Workflow:** Claude drafts → codex reviews / implements → Claude tests + releases. Same cadence as #177/#179/#182.

## 1. Problem

`claw-wall`'s generated `channel-context` feed currently behaves like an unread mailbox: each fetch returns the **oldest** `limit` messages newer than the consumer's cursor, then advances the cursor. For Discord-mention/invocation context, this is the wrong semantic. Live evidence from Tiverton: an agent received a `channel-context` feed in its prompt that contained backlog from 17:02–20:37, while the actual mention happened at 23:53. The conversation that prompted the invocation was not visible to the agent, even though ingestion had captured it.

The bug is at `cmd/claw-wall/store.go:116-118`: when `collected` (messages newer than cursor) exceeds `limit`, we keep `collected[:limit]` (oldest first) and bump the cursor to that page's last ID. After a long quiet period, the agent drains the buffer oldest-first across many invocations before ever seeing the latest message.

A second-order consequence: cache instability. Even in a quiet channel, every fetch advances the cursor, so the rendered feed body changes each turn even when the room is silent. That breaks the cllama feed cache (and downstream prompt caches) for no benefit.

The "background context" branch (`store.go:120-146`) is already the right shape — newest tail, no cursor write — but it only fires when there are **no** unread messages. As soon as backlog exists, we drop back to mailbox mode.

## 2. Design constraints

- **Agent perspective:** when the bot is mentioned in a channel it's part of, it must see the recent room conversation, just like a human walking back into the room would.
- **Cache stability:** identical store contents should produce identical feed body. This is a hard requirement for prompt-cache-friendliness.
- **No silent gaps:** if the buffer or the requested window can't cover the full request, the agent must be able to see that.
- **No layering violations:** claw-wall stays a dumb tail buffer. Summarization, salience, episodic compression — those belong to the salience-memory adapter (in-flight per `docs/plans/2026-04-29-salience-memory-adapter.md`). Don't bake LLM-derived condensation into claw-wall.
- **Wider continuity already exists:** Tiverton confirms `agent-scaffold` (daily, ~2.8KB), `desk-chronicle` (daily, ~3.5KB), and `agent-memory` (10min, small/private) already cover non-recent context. `channel-context` is strictly *recent room transcript*.
- **Backwards-compatibility:** the existing cursor/delta API path stays callable for any consumer that genuinely wants mailbox semantics, but it stops being the default.

## 3. Answers to codex's open questions

### Q1 — Default API behavior: keep delta default, or flip to tail?

**Flip to tail by default.** Both the public API and the auto-generated feed move to `mode=tail`. `mode=delta` remains supported but explicit.

Reasoning: there is exactly one in-tree consumer of `/channel-context` — the compose-generated feed — and that consumer is the bug victim. Keeping `delta` as the implicit default leaves a footgun for any future caller. There are no documented external consumers to preserve. The existing `TestConversationStoreConsumeAdvancesCursorWithoutSkipping` regression remains green by becoming explicit about `mode=delta`.

### Q2 — Default cap: limit (count), max_chars (bytes), or both?

**Both, dual cap with whichever-fires-first semantics. Defaults: `limit=40` and `max_chars=8KB`.**

Reasoning:
- Pure count cap explodes the prompt budget when a single Discord message has a 4KB embed.
- Pure byte cap silently drops sane numbers of messages when content is dense.
- `8KB` aligns with cllama's existing `MaxFeedResponseBytes` truncation threshold, so a well-formed tail fits without hitting cllama's "truncated" path.
- `40` is ≈2× a reasonable "enough recent context for a mention" baseline (the current 20 was tight; it's the value the issue notes as not-enough-on-its-own).

The dual cap renders newest-first walk: walk messages from newest backward, accept while both `count < limit` and `bytes < max_chars`, stop on either limit. Then sort ASC for output.

### Q3 — Startup Discord backfill?

**No, not in v1.** Honest retained-coverage metadata is sufficient.

Reasoning:
- Backfill against Discord history brings its own concerns: rate-limit budgets, auth scoping, "how far back is far enough", out-of-order vs. paginated fetches, dedupe against the live poller.
- The wider-continuity feeds (agent-scaffold, desk-chronicle, agent-memory) already cover the cold-start window for Tiverton-class deployments.
- The coverage header (see §4.3) makes the "I just came online, my buffer only has 5 minutes" case visible to the agent so it can reason about its own context.
- Operators who need a true 24h window today can raise `CLAW_WALL_LIMIT` (and pod-level `buffer`) so the in-process buffer is the durable record over restarts that fit inside `CLAW_WALL_BUFFER_RETENTION_HOURS` worth of Discord traffic. (claw-wall already runs as a sidecar across normal restart cycles; cold-start happens on `claw down && claw up`, not on every poll.)
- A separate follow-up issue can scope backfill once we have real signal that retained-coverage isn't enough.

## 4. Design

### 4.1 Wire contract

```
GET /channel-context?consumer=<id>&channels=<csv>&mode=tail&since=24h&limit=40&max_chars=8192
```

Query params:
- `consumer` — required in `mode=delta`, optional and ignored in `mode=tail`.
- `channels` — required CSV of channel IDs. Same channel-authorization model as today (caller already restricted upstream to surface-derived channels).
- `mode` — `tail` (default) | `delta` (legacy cursor paging).
- `since` — Go-duration-parsable window (`24h`, `30m`, `2h15m`). Optional. If absent, no time filter; only count/byte caps apply. Tail mode only.
- `limit` — positive integer message count cap. Defaults: `mode=tail` → 40, `mode=delta` → 20 (existing).
- `max_chars` — positive integer byte cap on rendered body (excluding header). Tail mode only. Default 8192.

`mode=delta` keeps its current behavior verbatim (cursor paging, oldest-page truncation, cursor advance). No new params apply to it.

### 4.2 Store API

New method on `*conversationStore`:

```go
type tailRequest struct {
    channels []string
    since    time.Duration  // 0 = no time filter
    limit    int
    maxChars int            // 0 = no byte cap
    now      time.Time
}

type tailResult struct {
    messages       []wallMessage
    bufferOldest   time.Time   // earliest message in any requested channel buffer
    bufferNewest   time.Time   // latest message
    truncatedByLimit    bool
    truncatedByMaxChars bool
    truncatedByWindow   bool   // since-window contains > limit/max_chars; older-in-window omitted
}

func (s *conversationStore) consumeTail(req tailRequest) tailResult
```

Implementation:
1. Lock store, walk per-channel buffers, collect all messages where `since == 0 || msg.Timestamp >= now-since`.
2. Sort DESC by snowflake.
3. Walk newest-first, accumulate while `count < limit` and `bytes < maxChars`. Track which cap fired.
4. Reverse to ASC for output.
5. Compute `bufferOldest`/`bufferNewest` across all requested buffers (for coverage telemetry, regardless of `since`).
6. Set `truncatedByWindow` if there exist messages within `since` that we didn't return.
7. Do **not** touch cursors. The function is pure-read.

### 4.3 Coverage header

Tail-mode response is one header line (terminated by `\n\n`), then existing `[ts] author: content` lines:

```
[channel-context] mode=tail since=24h messages=23 range=2026-04-29T03:14Z..2026-04-29T05:42Z channels=1464509330731696213,1464796137893662843 retained=23/since-24h
[2026-04-29 03:14] alice: Has anyone reviewed the latest signals?
[2026-04-29 03:18] bob: I'll take it.
...
```

`retained=N/M` in the header reads as "we returned N messages; M is the largest set we could have returned given store contents, the since window, and surface-authorized channels." When `truncatedByLimit` or `truncatedByMaxChars` fires, `M > N`. When `truncatedByWindow` fires, `retained=N/since-Xh` instead of a number, signaling "this is the entire since-bounded set, capped." When the buffer itself is the limit (cold start, restart): `retained=N/buffer` with `range` showing the actual oldest_ts.

`mode=delta` responses do **not** include the header (preserves existing format and existing tests).

Header cost: ~140 bytes. Trivial relative to message body. Worth it because:
- Agents can reason about coverage explicitly ("I'm only seeing the last 12 minutes — must have just rebooted").
- Operators debugging issue-201-class problems get coverage info inline without extra plumbing.
- Coverage info is stable across quiet-period fetches, so it doesn't bust the cache.

### 4.4 Generated feed (compose_up)

`appendConversationWallFeed` (`cmd/claw/compose_up.go:1732`) emits:

```
/channel-context?consumer={claw_id}&channels=<sorted-ids>&mode=tail&since=<dur>&limit=<n>&max_chars=<n>
```

Defaults if pod doesn't override: `since=24h`, `limit=40`, `max_chars=8192`.

`{claw_id}` template stays so the URL keeps a deterministic per-ordinal shape (consistent with `delta` mode generation), even though `consumer` is unused in tail mode. This avoids a second feed-template branch.

### 4.5 Pod YAML configuration

Pod-level config under `x-claw.context.channel` (new key, no migration needed since claw-wall auto-injection has no current config knobs):

```yaml
x-claw:
  pod: tiverton-house
  master: sentinel
  context:
    channel:
      since: 24h         # default 24h
      limit: 40          # default 40
      max_chars: 8192    # default 8192
      buffer: 500        # default unset (claw-wall env CLAW_WALL_LIMIT default 50)
```

`since`/`limit`/`max_chars` flow into the generated feed path. `buffer` flows into the auto-injected `claw-wall` service env as `CLAW_WALL_LIMIT=<buffer>` so chatty channels can hold a real 24h window.

Service-level overrides via `x-claw.context.channel` on individual services follow the same pod-defaults pattern as `surfaces`/`feeds`/`tools`.

### 4.6 Cache stability claim

For a quiet channel (no new poll deltas), tail mode returns the identical body — same messages, same coverage header — across every fetch within the buffer-retention window. The cllama 30s feed cache stays warm; downstream prompt caches stay warm. The current bug forces every fetch to mutate the cursor and produce a new body even in a silent room. Fixing this is the headline win.

For an active channel, the body changes on each new message, as it should. The bug fix doesn't hurt this case; it just stops creating fake churn in the silent case. Stable-prefix + fresh-tail two-block rendering is interesting but **out of scope** — defer to salience-memory adapter integration when that lands (#164 / `docs/plans/2026-04-29-salience-memory-adapter.md`).

## 5. Non-goals (v1)

- LLM-driven summarization of older content. Salience-memory adapter handles that.
- Discord history backfill beyond the in-process buffer.
- Two-block stable-prefix + fresh-tail rendering for max prompt-cache stability.
- Per-channel (vs. per-pod/service) override of since/limit/max_chars.
- Header-line localization, channel-name resolution (we only have IDs), or richer telemetry (e.g. message-id high-watermark in the header).

## 6. Build sequence

1. **Store + handler** — `consumeTail` + dual cap + coverage header. New tests; existing tests stay green.
2. **Compose generation** — `appendConversationWallFeed` writes `mode=tail&since=...&limit=...&max_chars=...`. Pod parser picks up `x-claw.context.channel`. Buffer flows into `CLAW_WALL_LIMIT`.
3. **Skill + docs** — `skills/clawdapus/SKILL.md` (and embedded mirror), guide pages mentioning channel-context, ADR/changelog. Roadmap reference.
4. **Spike check** — verify generated feed in `examples/quickstart`/local pod produces correct paths; manual fetch round-trip against a fresh pod.
5. **Tiverton rollout** — separate from this PR. After release, update `tiverton-house/claw-pod.yml` on the live host with `x-claw.context.channel.since: 24h, limit: 40, buffer: 500`, run `claw up -d`, verify.

## 7. Tests

### claw-wall (`cmd/claw-wall/`)
- `TestConsumeTailReturnsLatestIdempotent` — store has 100/101/102, repeated `consumeTail{limit:2}` returns 101,102 every time.
- `TestConsumeTailFiltersBySinceWindow` — messages spanning 0–48h, `since=24h` returns only the recent half.
- `TestConsumeTailRespectsMaxChars` — single 9KB message + smaller messages; `max_chars=8KB` excludes the giant one and truncates by bytes, not just count.
- `TestConsumeTailDoesNotMutateCursor` — interleave delta and tail consumers; tail fetches don't move the delta consumer's cursor.
- `TestConsumeDeltaPreservesExistingPagination` — keeps current `100,101 → 102` behavior verbatim (rename of existing test).
- `TestChannelContextHandlerTailModeIsDefault` — hit `/channel-context` with no `mode` → tail mode header, no cursor advance.
- `TestChannelContextHandlerDeltaMode` — `mode=delta` returns existing format with no coverage header (regression).
- `TestChannelContextHandlerCoverageHeaderShape` — header line parses, contains expected fields, `range` reflects actual oldest/newest in the result.
- `TestChannelContextHandlerCoverageHeaderTruncation` — when limit fires, header reads `retained=N/M` with M > N; when window-bounded full set returned, `retained=N/since-24h`; when buffer is the limit, `retained=N/buffer`.

### compose_up (`cmd/claw/`)
- `TestAppendConversationWallFeedTailMode` — generated feed path includes `mode=tail&since=24h&limit=40&max_chars=8192` by default.
- `TestAppendConversationWallFeedHonorsPodConfig` — pod-level `x-claw.context.channel` overrides flow into the path.
- `TestConversationWallServiceUsesPodBuffer` — pod-level `buffer` flows into wall service `CLAW_WALL_LIMIT` env.

### pod parser (`internal/pod/`)
- `TestParseClawContextChannel` — accepts `since`/`limit`/`max_chars`/`buffer`, rejects negatives, rejects unparseable durations.
- `TestPodDefaultsContextChannel` — service-level `x-claw.context.channel` overrides pod defaults using the standard pattern.

## 8. Open questions for design challenge

1. **Coverage header format** — single line vs. multi-line vs. JSON sidecar response? I picked single-line for cache-friendliness and minimal token cost; flag if you'd rather have it as a separate JSON metadata line so consumers can parse.
2. **Buffer-as-pod-config name** — `x-claw.context.channel.buffer` is a bit indirect (it's really claw-wall ring-buffer size). Alternative: keep it as a claw-wall sidecar env override the operator sets directly, and don't surface it through pod-yml. I lean toward surfacing it (operators shouldn't need to know about claw-wall as an env-var contract), but flag if you'd rather defer.
3. **`since` lower bound** — should we reject `since < 1m` to prevent agents from accidentally requesting 5-second windows? Or trust the value? I lean trust + clamp at zero.
4. **`max_chars` interaction with cllama's `MaxFeedResponseBytes`** — are we double-truncating? Verify `MaxFeedResponseBytes` value and confirm 8KB is at or below it. If cllama caps lower, drop our default to match.
5. **Tail rendering sort order** — ASC (oldest at top, newest at bottom = like reading a chat log) vs. DESC (newest at top, "latest first" framing). I picked ASC because it matches the current format and how humans read Discord. Flag if reverse-chrono is better for prompt locality.

## 9. Acceptance criteria mapping (from issue body)

| Issue criterion | This plan |
|---|---|
| Mention sees latest channel messages immediately preceding the mention | `mode=tail` returns newest-first walk, sorted ASC, no cursor effect |
| Repeated quiet-period fetches show recent context, not draining backlog | `consumeTail` is pure-read, no cursor mutation; identical body across quiet fetches |
| Backlog does not hide current conversation | Newest-first walk with dual cap; latest always present |
| Agents only receive their configured surface-derived channels | Unchanged from today; channel filtering happens upstream of claw-wall |
| If the last 24h exceeds context cap, summary + latest full | **Partially deferred:** v1 caps and signals truncation in the header; LLM summarization is the salience-memory adapter's job, not claw-wall's |
| Tests cover both modes | Yes (see §7) |

## 10. Out of this PR

- Tiverton pod-yml update (separate change on the host after release).
- Salience-memory adapter integration that could replace the older end of the channel transcript with a stable summary block.
- Backfill from Discord history.
- Renaming `claw-wall` (the name is reserved and stable).
