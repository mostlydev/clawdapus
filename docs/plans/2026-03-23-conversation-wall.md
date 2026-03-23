# Conversation Wall — Channel Context Injection for Multi-Agent Pods

**Date:** 2026-03-23
**Issue:** #71
**Status:** Planned (revised after Codex review)

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

Response: new messages since the consumer's last cursor, formatted as a readable text block (newest last):
```
[2026-03-23 14:32] alice: Has anyone reviewed the latest signals?
[2026-03-23 14:33] bob: Looking now — AAPL is trending up
[2026-03-23 14:34] alice: @trader-0 what's your read?
```

Returns empty body (200) if no new messages since last cursor — cllama skips injection of empty feeds, avoiding prompt bloat on quiet turns.

The cursor is in-memory; it resets on wall restart. That's acceptable for v1 — agents see some overlap after a restart, not a correctness failure.

### Source of channel IDs

Channel IDs come from `handles.discord.guilds[].channels[].id` (`HandleInfo.GuildInfo.ChannelInfo`). This is the correct and only source in the current data model — `ResolvedSurface.ChannelConfig` carries routing policy (allowlists, mention requirements) but not channel IDs. The wall polls all channels declared in an agent's handle graph, which is the intended scope.

### Credentials

`claw up` builds a channel→token map by walking all services that have Discord handles. For each such service, it maps that service's channel IDs to its `DISCORD_BOT_TOKEN`. The wall receives these as structured env configuration:

```
CLAW_WALL_TOKENS=<channelID1>:<token1>,<channelID2>:<token1>,<channelID3>:<token2>
```

This supports multi-bot pods where different services have different bot tokens for different channels. No reliance on master owning all tokens.

### Feed wiring

`claw-wall` is an infrastructure service, in the same class as cllama, clawdash, and claw-api. These are all imperatively injected by `compose_up.go` without `claw.describe` involvement. `claw-wall` follows the same pattern — not a self-describing user service.

`claw up` injects `claw-wall` into `p.Services` as a virtual service **before feed resolution runs**, so `resolveFeedURL("claw-wall", ...)` resolves naturally via the normal service path (looks up port from `Expose: ["8080"]`). No special-casing in `resolveFeedURL` needed.

Each cllama-enabled agent service with Discord channels gets a `FeedEntry` appended by `claw up`:
```go
pod.FeedEntry{
    Name:   "channel-context",
    Source: "claw-wall",
    Path:   "/channel-context?consumer=<agent-id>&channels=<ids>&limit=20",
    TTL:    30,
}
```

This flows through `buildFeedManifestEntries` into `feeds.json` as a standard `FeedManifestEntry`. No auth needed (internal network).

For `count > 1` services: all ordinals share the same wall. Each ordinal uses its own `consumer=<ordinal-id>` for cursor tracking, so cursors don't cross between replicas.

## claw up Injection

**Trigger:** at least one cllama-enabled service has Discord channel IDs in its handle graph.

Does **not** require `x-claw.master`. The credential model derives tokens directly from service environments, not from the master service.

**Injection steps:**

1. Walk all services' `handles.discord.guilds[].channels[].id` — build `channelID → DISCORD_BOT_TOKEN` map (token from each service's environment)
2. Inject `claw-wall` into `p.Services` as a virtual service (before `resolveFeedSubscriptions` and `buildFeedManifestEntries` run):
   - Image: `ghcr.io/mostlydev/claw-wall:latest`
   - Env: `CLAW_WALL_TOKENS=<channel:token pairs>`, `CLAW_WALL_LIMIT=50`, `CLAW_WALL_POLL_INTERVAL=15`
   - `Expose: ["8080"]`
   - Networks: `claw-internal`
   - Healthcheck: `GET /health`
3. For each cllama-enabled agent service with Discord channel IDs, append to `svc.Claw.Feeds`:
   ```go
   pod.FeedEntry{
       Name:   "channel-context",
       Source: "claw-wall",
       Path:   "/channel-context?consumer=<service-name>&channels=<ids>&limit=20",
       TTL:    30,
   }
   ```
   For count > 1: path uses the ordinal ID (`consumer=trader-0-0`, etc.)

## New Binary: cmd/claw-wall

Small Go HTTP server.

| Var | Description | Default |
|-----|-------------|---------|
| `CLAW_WALL_TOKENS` | `channelID:token,...` pairs | required |
| `CLAW_WALL_LIMIT` | Ring buffer size per channel | 50 |
| `CLAW_WALL_POLL_INTERVAL` | Discord poll interval (seconds) | 15 |
| `CLAW_WALL_ADDR` | Listen address | `:8080` |

Endpoints:
- `GET /channel-context?consumer=<id>&channels=<ids>&limit=N` — messages since consumer's cursor; empty body if none
- `GET /health` — `{"ok":true}`

Internal state per channel: ring buffer of messages. Internal state per consumer: last seen message ID (cursor). Both in-memory.

## Implementation Sequence

1. **`cmd/claw-wall/`** — binary: Discord poller, ring buffer, cursor tracking, HTTP handler
2. **`dockerfiles/claw-wall/Dockerfile`** — image build
3. **`cmd/claw/compose_up.go`** — `injectConversationWall()`: trigger detection, virtual service injection into `p.Services`, feed entry appending per consumer
4. **`internal/pod/compose_emit.go`** — emit `claw-wall` in compose output (same pattern as clawdash)
5. **Unit tests** — injection logic, feed path generation, cursor advancement, empty-response when no new messages
6. **Spike test** — verify that `mention_only` agents in a multi-agent pod respond with awareness of ambient channel context; assert no reply loops introduced

## Known Limitations (v1)

- Cursor resets on wall restart — agents may see some message overlap after redeploy
- Discord-only; no Slack/Telegram support yet
- Polls all channels in the handle graph — no per-channel opt-out in v1
- Multiple services polling the same channel with the same token are deduplicated at the wall level (one poll, shared cache) only if their tokens match; different tokens for the same channel result in separate polls
