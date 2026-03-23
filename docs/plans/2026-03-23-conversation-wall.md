# Conversation Wall — Channel Context Injection for Multi-Agent Pods

**Date:** 2026-03-23
**Issue:** #71
**Status:** Planned

## Problem

Agents using `mention_only` only see messages directly addressed to them. They respond without awareness of the surrounding channel conversation, which breaks coordination in multi-agent setups like trading desks.

## Solution

A pod-level `claw-wall` sidecar that polls Discord channels, caches recent message history, and serves it as a feed. cllama injects the feed as context before each LLM call. Agents stay aware of the full channel conversation without triggering on every message.

## Architecture

### claw-wall sidecar

One `claw-wall` container per pod (not per service). It:

- Polls all Discord channels declared by any agent in the pod every 15 seconds
- Maintains an in-memory ring buffer of 50 messages per channel
- Serves `GET /channel-context?channels=<id1>,<id2>&limit=N` → readable text block
- Exposes `/health` for Docker healthcheck
- State: in-memory now; on-disk persistence is a future upgrade without API changes

Response format (newest last):
```
[2026-03-23 14:32] alice: Has anyone reviewed the latest signals?
[2026-03-23 14:33] bob: Looking now — AAPL is trending up
[2026-03-23 14:34] alice: @trader-0 what's your read?
```

### Feed wiring

Each cllama-enabled agent service gets a standard `FeedManifestEntry` in `feeds.json`:
```json
{
  "name": "channel-context",
  "source": "claw-wall",
  "path": "/channel-context?channels=<this agent's channel IDs>&limit=20",
  "ttl": 30,
  "url": "http://claw-wall:8080/channel-context?channels=...&limit=20"
}
```

`resolveFeedURL` handles `claw-wall` through the normal service path — wall is injected as a real compose service with `expose: ["8080"]`, so no special-casing needed.

### Credentials

The wall inherits `DISCORD_BOT_TOKEN` from the pod master service. This is a master-pod feature: no wall is injected if `x-claw.master` is not set. Agents in a pod typically share channels, so one token is sufficient to read them all.

## claw up Injection

**Trigger:** `p.Master != ""` AND at least one cllama-enabled service has Discord channel IDs in its handle graph (`handles.discord.guilds[].channels[].id`).

**Injection steps:**

1. Walk all services' handle graphs — collect union of all Discord channel IDs across the pod
2. Inject `claw-wall` as a virtual service:
   - Image: `ghcr.io/mostlydev/claw-wall:latest`
   - Env: `DISCORD_BOT_TOKEN` (from master service env), `CLAW_WALL_CHANNELS` (all IDs, comma-separated), `CLAW_WALL_LIMIT=50`, `CLAW_WALL_POLL_INTERVAL=15`
   - `expose: ["8080"]`
   - Networks: `claw-internal`
   - Healthcheck: `GET /health`
3. For each cllama-enabled agent service with Discord channel IDs, append to `svc.Claw.Feeds`:
   ```go
   pod.FeedEntry{
       Name:   "channel-context",
       Source: "claw-wall",
       Path:   "/channel-context?channels=<agent channel IDs>&limit=20",
       TTL:    30,
   }
   ```
4. `buildFeedManifestEntries` resolves to full URL naturally via `resolveFeedURL`

For `count > 1` services: the same `claw-wall` serves all ordinals (channels and token are the same). Each ordinal's feed path is identical — no per-ordinal wall instances needed.

## New Binary: cmd/claw-wall

Small Go HTTP server. Env vars:

| Var | Description | Default |
|-----|-------------|---------|
| `DISCORD_BOT_TOKEN` | Bot token for reading channels | required |
| `CLAW_WALL_CHANNELS` | Comma-separated Discord channel IDs to poll | required |
| `CLAW_WALL_LIMIT` | Ring buffer size per channel | 50 |
| `CLAW_WALL_POLL_INTERVAL` | Discord poll interval (seconds) | 15 |
| `CLAW_WALL_ADDR` | Listen address | `:8080` |

Endpoints:
- `GET /channel-context?channels=<ids>&limit=N` — recent messages, filtered and formatted
- `GET /health` — `{"ok":true}`

## Implementation Sequence

1. **`cmd/claw-wall/`** — binary: Discord poller, ring buffer, HTTP handler
2. **`dockerfiles/claw-wall/Dockerfile`** — image build
3. **`cmd/claw/compose_up.go`** — `injectConversationWall()` function: trigger detection, service injection, feed entry appending
4. **`internal/pod/compose_emit.go`** — emit `claw-wall` service in compose output (same pattern as clawdash/cllama)
5. **Tests** — unit tests for injection logic and feed path generation; integration test for wall HTTP handler

## What This Doesn't Do (Yet)

- High-water mark per agent (serving only messages not yet seen) — next iteration
- Non-Discord platforms (Slack etc.) — wall is Discord-only for now
- Pods without a master service
- Filtering by guild (all channels in `CLAW_WALL_CHANNELS` are polled regardless of guild)
