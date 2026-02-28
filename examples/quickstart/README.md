# Clawdapus Quickstart

Get a governed OpenClaw agent running in 5 minutes.

## Prerequisites

- Docker Desktop running
- An [OpenClaw](https://github.com/openclaws/openclaw) base image (`openclaw:latest`)
- An [OpenRouter](https://openrouter.ai/) API key (or Anthropic/OpenAI)
- A Discord bot token ([create one here](https://discord.com/developers/applications))

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

## 3. Build and launch

```bash
source .env
claw build -t quickstart-claw .
claw up -f claw-pod.yml -d
```

## 4. Verify

```bash
claw ps -f claw-pod.yml       # assistant + cllama-passthrough both running
claw health -f claw-pod.yml   # both healthy
```

Open **http://localhost:8081** — the cllama governance proxy dashboard. This shows every LLM call in real time: which agent, which model, token counts, estimated cost, latency.

## 5. Talk to your bot

Message `@quickstart-bot` in your Discord server. Every message routes through the cllama proxy — the bot has no direct API access. Watch the dashboard update in real time as the bot responds.

Check the audit trail:

```bash
claw logs -f claw-pod.yml cllama-passthrough
```

Structured JSON for every request: agent, model, tokens, cost, latency.

## What's happening under the hood

- **Credential starvation:** Your OpenRouter key lives in the proxy only. The agent gets a bearer token. It literally cannot call providers directly — it doesn't have the keys.
- **Behavioral contract:** `AGENTS.md` is bind-mounted read-only. Even root inside the container can't modify it.
- **Identity projection:** The HANDLE directive wires Discord config automatically — `allowBots`, `mentionPatterns`, guild membership. No manual config.
- **Cost tracking:** The proxy extracts `usage` from every LLM response and tracks cost per agent/model/provider.

## Using Telegram or Slack instead

Replace the Discord configuration:

**Telegram:** Change `HANDLE discord` to `HANDLE telegram` in the Clawfile. In `claw-pod.yml`, replace the `handles:` block with `telegram: {id: "${TELEGRAM_BOT_ID}", username: "mybot"}` and set `TELEGRAM_BOT_TOKEN` in `environment:`.

**Slack:** Same pattern — `HANDLE slack`, swap the handles block, use `SLACK_BOT_TOKEN`.

## Migrate from existing OpenClaw

Already have an OpenClaw bot running? Import your config:

```bash
claw init --from ~/path/to/openclaw/config
# Generates Clawfile, claw-pod.yml, AGENTS.md pre-configured from your setup
```

## Clean up

```bash
claw down -f claw-pod.yml
```
