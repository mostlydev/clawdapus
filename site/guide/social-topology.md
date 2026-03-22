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

The broadcasting mechanism enables a powerful integration pattern: a non-AI service can dynamically `@mention` a specific bot in a chat channel without hardcoding IDs.

For example, a trading API detects an anomaly and needs to alert the risk analyst bot. It reads `CLAW_HANDLE_RISK_ANALYST_DISCORD_ID` from its environment and constructs a Discord message that mentions the bot by ID. The bot receives the mention, processes it, and responds -- all without any hardcoded coupling between the services.

This is the **Leviathan Pattern**: non-cognitive services dynamically addressing cognitive services through the pod's social topology.

## Shared Topology with `handles-defaults`

When many services share the same Discord guild and channel topology, declare the shared parts once at the pod level:

```yaml
x-claw:
  pod: trading-desk
  handles-defaults:
    discord:
      guilds: ["${DISCORD_GUILD_ID}"]
      channels:
        trading-floor: "${TRADING_FLOOR_CHANNEL_ID}"
        alerts: "${ALERTS_CHANNEL_ID}"
```

All services inherit this topology. Each service's `x-claw.handles` block then only needs the identity fields that differ:

```yaml
services:
  tiverton:
    image: trading-desk-tiverton:latest
    x-claw:
      handles:
        discord:
          id: "${TIVERTON_DISCORD_ID}"
          username: "tiverton"

  analyst:
    image: trading-desk-analyst:latest
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
- **`mentionPatterns`** -- Derived from the handle username and ID, so agents can route incoming messages correctly.
- **`requireMention`** -- Enabled by default for guild channels to prevent agents from responding to every message and entering feedback loops.
- **Guild `users[]` allowlist** -- Populated with every peer bot in the pod, so agents can communicate with each other.

::: warning Mention Safety
All drivers set `requireMention` (or the driver-specific equivalent) for guild channels. Without this, multi-agent pods enter feedback loops where bots respond to each other's messages indefinitely. This is enforced at the driver level, not left to the agent's discretion.
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

| Platform | `openclaw` | `hermes` | `nanobot` | `picoclaw` | `nullclaw` | `microclaw` |
|----------|:---:|:---:|:---:|:---:|:---:|:---:|
| Discord | yes | yes | yes | yes | yes | yes |
| Telegram | -- | yes | yes | yes | yes | yes |
| Slack | -- | yes | yes | yes | yes | yes |

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
