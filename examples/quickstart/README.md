# Clawdapus Quickstart

Get a governed OpenClaw agent running in 5 minutes.

## Prerequisites

- Docker Desktop running
- An [OpenRouter](https://openrouter.ai/) API key (or Anthropic/OpenAI)
- A Discord bot token ([create one here](https://discord.com/developers/applications))

## Discord bot setup

If you already have a Discord bot, skip to [Install](#1-install).

1. Go to the [Discord Developer Portal](https://discord.com/developers/applications) and click **New Application**. Name it whatever you want.
2. Go to **Bot** in the left sidebar. Click **Reset Token** and copy it — this is your `DISCORD_BOT_TOKEN`.
3. Under **Privileged Gateway Intents**, enable **Message Content Intent**. The bot needs this to read messages.
4. Go to **OAuth2 → URL Generator**. Select scopes: `bot`. Select permissions: `Send Messages`, `Read Message History`. Copy the generated URL and open it to invite the bot to your server.
5. To get your `DISCORD_BOT_ID`: on the **General Information** page, copy the **Application ID**.
6. To get your `DISCORD_GUILD_ID`: in Discord, enable Developer Mode (Settings → Advanced → Developer Mode), then right-click your server name and **Copy Server ID**.

## 1. Install

```bash
curl -sSL https://raw.githubusercontent.com/mostlydev/clawdapus/master/install.sh | sh
claw doctor    # verify Docker + buildx + compose
```

## 2. Clone and configure

```bash
git clone https://github.com/mostlydev/clawdapus.git
cd clawdapus/examples/quickstart

cp .env.example .env
# Edit .env — add your OPENROUTER_API_KEY, DISCORD_BOT_TOKEN, DISCORD_BOT_ID, DISCORD_GUILD_ID
```

## 3. Run the four verbs

```bash
source .env

# 1. Pull pinned runtime infra + any registry-backed pod services
claw pull

# 2. Build this pod's local build: services
claw build

# 3. Compile the pod and launch it
claw up -d
```

The quickstart follows the four-verb operator loop:

- `claw pull` fetches pinned runtime infra and registry-backed pod services
- `claw build` builds pod services that declare `build:`
- `claw up` compiles the pod and launches it
- `claw down` tears the pod back down in [step 6](#6-clean-up)

`claw up` is strict by default. If something is missing, it prints the exact remediation command (`claw pull` or `claw build`). If you want a first-run shortcut, use `claw up --fix -d`.

## 4. Verify

```bash
claw ps       # assistant + cllama both running
claw health   # both healthy
```

Open **http://localhost:8181** — the cllama governance proxy dashboard. Every LLM call in real time: which agent, which model, token counts, cost.

Open **http://localhost:8082** — the Clawdapus Dash fleet dashboard. Live service health, topology wiring, and per-service drill-down.

## 5. Talk to your bot

Message `@quickstart-bot` in your Discord server. Every message routes through the cllama proxy — the bot has no direct API access. Watch the dashboard update in real time as the bot responds.

Check the audit trail:

```bash
claw logs cllama
```

Structured JSON for every request: agent, model, tokens, cost, latency.

## What's happening under the hood

- **Credential starvation:** Your OpenRouter key lives in the proxy only. The agent gets a bearer token. It literally cannot call providers directly — it doesn't have the keys.
- **Behavioral contract:** `agents/assistant/AGENTS.md` is bind-mounted read-only. Even root inside the container can't modify it.
- **Identity projection:** The HANDLE directive wires Discord config automatically — `allowBots`, native Discord `mentionPatterns`, guild membership. No manual config.
- **Cost tracking:** The proxy extracts `usage` from every LLM response and tracks cost per agent/model/provider.

## Using Telegram or Slack instead

Replace the Discord configuration:

**Telegram:** Change `HANDLE discord` to `HANDLE telegram` in `agents/assistant/Clawfile`. In `claw-pod.yml`, replace the `handles:` block with `telegram: {id: "${TELEGRAM_BOT_ID}", username: "mybot"}` and set `TELEGRAM_BOT_TOKEN` in `environment:`.

**Slack:** Same pattern — `HANDLE slack`, swap the handles block, use `SLACK_BOT_TOKEN`.

## Add another agent

From this quickstart (canonical) layout:

```bash
claw agent add researcher
```

This creates `agents/researcher/` and updates `claw-pod.yml` + `.env.example`.
In flat projects, `claw agent add` preserves flat style by default (`Clawfile.<name>`, `AGENTS-<name>.md`).

## Migrate from existing OpenClaw

Already have an OpenClaw bot running? Import your config:

```bash
claw init --from ~/path/to/openclaw/config
# Generates migration scaffold pre-configured from your setup
```

## Clean up

```bash
claw down
```
