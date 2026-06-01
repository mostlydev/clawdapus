# Social Topology

Agents have identities on chat platforms. A trading bot needs a Discord presence. A support agent needs a Slack identity. Clawdapus manages the full lifecycle of these identities -- declaration, wiring, peer discovery, and cross-service broadcasting -- through the `HANDLE` directive and pod-level topology defaults.

## The HANDLE Directive

`HANDLE discord` in a Clawfile declares that this agent has a Discord identity. At deploy time, Clawdapus translates this into the runner's native configuration -- enabling the Discord plugin, setting up authentication, configuring mention patterns.

```dockerfile
FROM openclaw:latest

CLAW_TYPE openclaw
AGENT AGENTS.md
HANDLE discord
```

The actual identity details (bot token, guild ID, username) are provided at the pod level, keeping secrets out of the image.

## Environment Variable Broadcasting

Clawdapus broadcasts every agent's handles as environment variables into **every** service in the pod -- including non-claw services:

```bash
# Available in every pod service automatically:
CLAW_HANDLE_CRYPTO_CRUSHER_DISCORD_ID=123456789
CLAW_HANDLE_CRYPTO_CRUSHER_DISCORD_GUILDS=111222333
```

The variable names are derived from the service name, uppercased and hyphen-replaced. Every service in the pod knows every agent's platform identity without hardcoding anything.

::: tip Non-AI Services Get Handles Too
A trading API, a webhook receiver, a monitoring service -- any container in the pod receives handle environment variables. This enables rich integration between AI and non-AI services.
:::

## The Leviathan Pattern

The broadcasting mechanism enables a named integration pattern: **the Leviathan Pattern**. A non-AI API service reads `CLAW_HANDLE_*` environment variables to dynamically construct `@mentions`, addressing cognitive services through the pod's social topology without any hardcoded coupling.

For example, a trading API detects an anomaly and needs to alert the risk analyst bot. It reads `CLAW_HANDLE_RISK_ANALYST_DISCORD_ID` from its environment and constructs a Discord message that mentions the bot by ID. The bot receives the mention, processes it, and responds.

```python
# In a non-AI trading API service (Python example)
import os

risk_bot_id = os.environ["CLAW_HANDLE_RISK_ANALYST_DISCORD_ID"]
alert_message = f"<@{risk_bot_id}> Anomaly detected in ETH/USD — drawdown exceeds 5% threshold."
send_to_discord(channel_id=alerts_channel, content=alert_message)
```

The pattern works because `claw up` injects every agent's handle environment variables into every service in the pod at compile time. The trading API does not import any Clawdapus library or know anything about the agent runtime. It just reads an environment variable and formats a string. Add a new agent to the pod, and its handle variables appear automatically in every service on the next `claw up`.

## Shared Topology with `handles-defaults`

When many services share the same Discord guild and channel topology, declare the shared parts once at the pod level:

```yaml
x-claw:
  pod: operations-room
  handles-defaults:
    discord:
      guilds: ["${DISCORD_GUILD_ID}"]
      channels:
        ops-floor: "${OPS_FLOOR_CHANNEL_ID}"
        alerts: "${ALERTS_CHANNEL_ID}"
```

All services inherit this topology. Each service's `x-claw.handles` block then only needs the identity fields that differ:

```yaml
services:
  coordinator:
    image: operations-coordinator:latest
    x-claw:
      handles:
        discord:
          id: "${COORDINATOR_DISCORD_ID}"
          username: "coordinator"

  analyst:
    image: operations-analyst:latest
    x-claw:
      handles:
        discord:
          id: "${ANALYST_DISCORD_ID}"
          username: "analyst"
```

::: info DRY Topology
Put shared guild IDs, channel mappings, and routing policy in `handles-defaults`. Keep per-service `handles` focused on identity: bot ID and username. This avoids duplicating topology across every service.
:::

## Automatic Driver Wiring

When `claw up` processes handle declarations, the driver automatically wires several things into each agent's runner configuration:

- **`allowBots: true`** -- Enables bot-to-bot messaging within the pod. Without this, Discord bots ignore messages from other bots.
- **`mentionPatterns`** -- Derived from the platform handle. Discord uses native `<@...>` mention patterns so agents only trigger on explicit mentions; text-mention platforms still use usernames.
- **`requireMention`** -- Enabled by default for guild channels to prevent agents from responding to every message and entering feedback loops.
- **Guild `users[]` allowlist** -- Populated with every peer bot in the pod, so agents can communicate with each other.

::: warning Mention Safety
Chat-capable drivers that configure Discord handles set `requireMention` (or the driver-specific equivalent) for guild channels. Without this, multi-agent pods enter feedback loops where bots respond to each other's messages indefinitely. This is enforced at the driver level, not left to the agent's discretion.
:::

## Per-Service Identity Overrides

While topology is shared, identity is always per-service. Each agent has its own bot token, its own Discord ID, and its own username. These are provided through environment variables resolved at deploy time:

```yaml
services:
  coordinator:
    image: coordinator:latest
    x-claw:
      handles:
        discord:
          id: "${COORDINATOR_DISCORD_ID}"
          username: "coordinator"
          token: "${COORDINATOR_DISCORD_TOKEN}"
```

## Multi-Platform Handles

An agent can have handles on multiple platforms simultaneously. Platform support varies by driver:

| Platform | `openclaw` | `hermes` | `nanoclaw` | `nanobot` | `picoclaw` | `nullclaw` | `microclaw` |
|----------|:---:|:---:|:---:|:---:|:---:|:---:|:---:|
| Discord | yes | yes | -- | yes | yes | yes | yes |
| Telegram | yes | yes | -- | yes | yes | yes | yes |
| Slack | yes | yes | -- | yes | yes | yes | yes |
| Long-tail chat | -- | -- | -- | -- | yes | -- | -- |

PicoClaw additionally supports WhatsApp, Feishu, LINE, QQ, DingTalk, OneBot, WeCom, and other long-tail platforms.

## Channel Surfaces

Beyond basic handle identity, channel surfaces provide fine-grained routing control. Map-form channel surfaces allow per-channel policy:

```yaml
x-claw:
  surfaces:
    - channel://discord:
        policy: allowlist
        allow_from_handles: true
        allow_from_services: [trading-api]
```

This declares that the agent's Discord channel surface only accepts messages from known handles and the `trading-api` service. The `allow_from_handles: true` flag expands into each guild's `users[]` allowlist using the handle IDs of all peer agents in the pod. The `allow_from_services` list derives Discord IDs from the named services' bot tokens.

## Channel Context Feed (`claw-wall`)

When a cllama-enabled service has Discord channels in its handle config, `claw up` automatically injects the `claw-wall` sidecar and a `channel-context` feed for that service. The feed delivers the recent channel transcript so the agent sees the conversation immediately preceding any mention — without it, an invocation can land in the prompt while the messages that triggered it are missing from the agent's context.

The wall polls Discord every 30s, backfills Discord history on startup to the configured retention horizon (default 24h), keeps a per-channel time-retained store bounded by a safety cap (default 5000 messages), and serves the latest tail on each fetch. By default the tail covers the last 24 hours, capped at 40 messages or 8KB whichever fires first, sorted oldest-to-newest like a chat log. A coverage header line declares what was actually returned so silent gaps are visible:

```
[channel-context] mode=tail since=24h channels=1464509330... messages=23 available=23 omitted=0 range=2026-04-29T03:14Z..2026-04-29T05:42Z buffer_range=2026-04-28T17:02Z..2026-04-29T05:42Z backfill_status=complete
```

Tune the window per pod or service with `x-claw.context.channel: { since, limit, max-chars, buffer }` (see [Pod YAML · Channel Context Tuning](/guide/pod-yaml#channel-context-tuning)). Each consuming agent only sees channels it has surface authorization for.

`buffer` is a memory-safety cap on retained messages, not the time contract. The sidecar's retention and backfill horizon come from `CLAW_WALL_RETENTION` (default `24h`), and startup pagination is bounded by `CLAW_WALL_BACKFILL_MAX_PAGES` (default `25`) per channel. If a channel cannot be fully backfilled because of the page budget, safety cap, or Discord rate limits, the feed header reports `backfill_status=partial` or `backfill_status=rate_limited`; mixed-channel responses use `channel_id:status` pairs.

### Cursored Append-Only Deltas

Starting in v0.14.0, cllama drives the feed as a delta-since-watermark instead of re-pasting the full recent tail on every turn. The proxy keeps a per-agent vector cursor (one entry per visible channel) and rewrites the `channel-context` feed URL to add an `after=channel_id:message_id,...` query string before sending it to `claw-wall`:

```
GET /channel-context?consumer={claw_id}&channels=A,B&mode=tail&since=24h&limit=40&max_chars=8192&after=A:1464900000,B:1464900001
```

Channels named in `after=` but missing from `channels=` are a 400. Channels in `channels=` but absent from `after=` get the bootstrap tail (the v0.13.7 behavior). Both cursored and bootstrap channels can compose in a single request -- `since=` bounds bootstrap reach, `after=` bounds cursored reach.

The cursor advances only after a successful 2xx response is recorded by the proxy's session-history writer. Streaming truncation, 5xx upstream errors, and 4xx rejections all leave the cursor untouched, so a failed turn replays the same delta cleanly on the next mention. When `claw-wall` caps a delta response (dual-cap on `limit` or `max_chars`), cllama appends a `coverage_partial=true omitted_after_cursor=N newest_returned=...` annotation so the partial coverage stays visible in the prompt rather than silently swallowing a gap.

Cursors persist on disk under `$CLAW_CONTEXT_LEDGER_DIR/<agent-id>/cursor.json` (defaults to `$CLAW_SESSION_HISTORY_DIR/context-ledger`). When session history is disabled, cursors live in-memory only and every cold start re-bootstraps with a 24h tail. See [cllama · Channel Context Cursors](/guide/cllama#channel-context-cursors) for the proxy-side model.

The legacy cursor/mailbox semantics from earlier releases remain callable as `mode=delta`, but generated feeds use `mode=tail` with `after=` cursors. Channel-derived auto-injection is suppressed for sidecars (`mcp-stdio`) and any service without a cllama proxy.

## OpenClaw Discord Routing Compatibility

The OpenClaw driver maps supported `channel://discord` routing controls directly into generated config and rejects unsupported ones early at compile time, rather than letting the container reject them at boot.

| `channel://discord` map-form setting | `openclaw` support |
|---|:---:|
| DM `policy` (`pairing`, `allowlist`, `open`, `disabled`) | yes |
| DM `allowFrom` | yes |
| Guild `requireMention` | yes |
| Guild `users[]` allowlist | yes |
| Surface `allow_from_handles: true` -- expands into each guild `users[]` | yes |
| Surface `allow_from_services: [svc...]` -- derives Discord IDs from service bot tokens and expands each guild `users[]` | yes |
| Guild `policy` | no |

::: warning Guild Policy Not Supported
The current OpenClaw runtime rejects guild-level `policy`. Clawdapus fails during config generation instead of writing a config the container would reject at boot. Use `requireMention` and `users[]` allowlists for guild-level access control.
:::
