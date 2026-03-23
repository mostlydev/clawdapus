# Conversation Wall — Channel Context Injection for Multi-Agent Pods

**Date:** 2026-03-23
**Issue:** #71
**Status:** Planned (revised after two Codex reviews)

## Problem

Agents using `mention_only` only see messages directly addressed to them. They respond without awareness of the surrounding channel conversation, which breaks coordination in multi-agent setups like trading desks.

## Solution

A pod-level `claw-wall` sidecar that polls Discord channels, maintains a cursor-aware message cache, and serves channel history as a feed. cllama injects the feed as context before each LLM call. Agents see new channel messages without triggering on every message and without replaying stale context on every turn.

## Architecture

### claw-wall sidecar

One `claw-wall` container per pod (not per service). It:

- Polls all Discord channels declared by any agent in the pod every 15 seconds
- Maintains an in-memory ring buffer per channel (50 messages)
- Tracks a **per-consumer cursor** (last injected message ID) so each agent only receives messages it hasn't seen yet
- Serves `GET /channel-context?consumer=<agent-id>&channels=<id1>,<id2>&limit=N`
- Exposes `/health` for Docker healthcheck

Response when there are new messages: text block, newest last:
```
[2026-03-23 14:32] alice: Has anyone reviewed the latest signals?
[2026-03-23 14:33] bob: Looking now — AAPL is trending up
[2026-03-23 14:34] alice: @trader-0 what's your read?
```

**Empty-response and cllama injection:** When there are no new messages since the consumer's cursor, the wall returns an empty body (200). However, `cllama/internal/feeds/inject.go:FormatFeedBlock` currently wraps all feed content — including empty strings — in `BEGIN/END` markers. The implementation must also patch `FormatFeedBlock` to return `""` when `!r.Unavailable && r.Content == ""`, so that `FormatAllFeeds` collapses to `""` and `InjectOpenAI`/`InjectAnthropic` skip injection on quiet turns (both already guard on `feedBlock == ""`).

The cursor is in-memory; it resets on wall restart. Agents see some overlap after a restart — acceptable for v1.

### Source of channel IDs

Channel IDs come from `handles.discord.guilds[].channels[].id` (`HandleInfo.GuildInfo.ChannelInfo`). This is the correct and only source in the current data model — `ResolvedSurface.ChannelConfig` carries routing policy (allowlists, mention requirements) but not channel IDs. The wall polls all channels declared in an agent's handle graph.

### Credentials

`claw up` builds a list of `(channel_id, token)` pairs by walking all services that have Discord handles. For each such service, it pairs that service's channel IDs with its `DISCORD_BOT_TOKEN`. The wall receives these as:

```
CLAW_WALL_TOKENS=<channelID1>:<token1>,<channelID2>:<token1>,<channelID3>:<token2>
```

This is a list of pairs, not a map — the same channel ID can appear more than once with different tokens. Within the wall, deduplication is by `(channel_id, token)` pair: each unique pair is polled once, results merged into the per-channel ring buffer. This supports multi-bot pods where different services use different bot tokens for overlapping channels.

### Feed wiring

`claw-wall` is an infrastructure service, in the same class as cllama, clawdash, and claw-api. These are all imperatively injected by `compose_up.go`. `claw-wall` follows the same pattern.

`claw up` injects `claw-wall` into `p.Services` as a plain `Service{Claw: nil}` **before `resolveFeedSubscriptions` and `buildFeedManifestEntries` run**. Because it is in `p.Services` with `Expose: ["8080"]`, `resolveFeedURL("claw-wall", ...)` resolves naturally via the existing non-claw service path. The existing `EmitCompose` loop already emits non-claw services from `p.Services` — no changes to `compose_emit.go` are needed.

Each cllama-enabled agent service with Discord channels gets a `FeedEntry` appended by `claw up`:
```go
pod.FeedEntry{
    Name:   "channel-context",
    Source: "claw-wall",
    Path:   "/channel-context?consumer=<service-name>&channels=<ids>&limit=20",
    TTL:    30,
}
```
This flows through `buildFeedManifestEntries` into `feeds.json` as a standard `FeedManifestEntry`. No auth needed (internal network).

For `count > 1` services: all ordinals share the same wall. Each ordinal uses its own `consumer=<ordinal-id>` for independent cursor tracking.

## claw up Injection

**Trigger:** at least one cllama-enabled service has Discord channel IDs in its handle graph. Does not require `x-claw.master`.

**Injection steps:**

1. Walk all services' `handles.discord.guilds[].channels[].id` — build list of `(channel_id, DISCORD_BOT_TOKEN)` pairs from each service's environment
2. Add `claw-wall` to `p.Services` as `Service{Image: "ghcr.io/mostlydev/claw-wall:latest", Claw: nil, Expose: ["8080"], Environment: {...}, Compose: {"networks": ["claw-internal"], "healthcheck": ...}}` — before feed resolution runs
3. For each cllama-enabled agent service with Discord channel IDs, append to `svc.Claw.Feeds`:
   ```go
   pod.FeedEntry{
       Name:   "channel-context",
       Source: "claw-wall",
       Path:   "/channel-context?consumer=<service-name>&channels=<ids>&limit=20",
       TTL:    30,
   }
   ```
   For `count > 1`: path uses the ordinal ID (`consumer=trader-0-0`, etc.)

## New Binary: cmd/claw-wall

Small Go HTTP server.

| Var | Description | Default |
|-----|-------------|---------|
| `CLAW_WALL_TOKENS` | `channelID:token,...` pairs (list, not map) | required |
| `CLAW_WALL_LIMIT` | Ring buffer size per channel | 50 |
| `CLAW_WALL_POLL_INTERVAL` | Discord poll interval (seconds) | 15 |
| `CLAW_WALL_ADDR` | Listen address | `:8080` |

Endpoints:
- `GET /channel-context?consumer=<id>&channels=<ids>&limit=N` — new messages since consumer's cursor; empty body (200) if none
- `GET /health` — `{"ok":true}`

Internal state per channel: ring buffer of messages. Internal state per consumer: last seen message ID (cursor). Both in-memory.

## Implementation Sequence

1. **`cllama/internal/feeds/inject.go`** — patch `FormatFeedBlock` to return `""` for empty non-unavailable content
2. **`cmd/claw-wall/`** — binary: Discord poller, ring buffer, cursor tracking, HTTP handler
3. **`dockerfiles/claw-wall/Dockerfile`** — image build
4. **`cmd/claw/compose_up.go`** — `injectConversationWall()`: trigger detection, add `claw-wall` to `p.Services`, append feed entries per consumer agent
5. **Unit tests** — injection logic, feed path generation, cursor advancement, empty-body on no-new-messages, cllama skip-on-empty-feed
6. **Spike test** — verify that `mention_only` agents in a multi-agent pod respond with awareness of ambient channel context; assert no reply loops introduced

## Known Limitations (v1)

- Cursor resets on wall restart — agents may see some message overlap after redeploy
- Discord-only; no Slack/Telegram support yet
- Polls all channels in the handle graph — no per-channel opt-out in v1
