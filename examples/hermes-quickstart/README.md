# Hermes Quickstart

A single Hermes agent on one Discord channel, governed by the cllama proxy. This is the minimal Hermes deployment: one Clawfile, one pod file, one agent contract.

The full walkthrough with Hermes-specific gotchas lives at [clawdapus.dev/guide/hermes](https://clawdapus.dev/guide/hermes).

## Prerequisites

- Docker with Compose
- The `claw` CLI ([install](https://clawdapus.dev/guide/quickstart#install))
- An Anthropic API key
- A Discord bot: application + bot token, **MESSAGE CONTENT intent enabled**, invited to a server you control

## Run

```bash
cp .env.example .env
# fill in ANTHROPIC_API_KEY, DISCORD_BOT_TOKEN, DISCORD_BOT_ID, DISCORD_GUILD_ID

claw pull        # pinned infra images (cllama, hermes-base)
claw build       # compile Clawfile -> agent image
claw up -d       # compile pod -> compose, launch, verify
```

## Verify

Mention the bot in any channel it can read:

> @hermes-assistant hello

You should get a governed reply — the turn routes through cllama, so it shows up in the audit log:

```bash
claw audit
claw logs assistant
```

## If the bot stays silent

First diagnostic surface, always:

```bash
claw compose exec assistant cat /root/.hermes/logs/gateway.log
```

Zero entries after startup means the bot is connected but not receiving events — almost always a missing MESSAGE CONTENT intent or a stale gateway session. See the [troubleshooting guide](https://clawdapus.dev/guide/troubleshooting) for the full symptom table.
