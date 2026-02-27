# Clawdapus Quickstart

Get a governed OpenClaw agent running in 5 minutes.

## Prerequisites

- Docker Desktop running
- An OpenClaw base image (`openclaw:latest`)
- An OpenRouter API key (or Anthropic/OpenAI key)
- A bot token for your platform (Discord, Telegram, or Slack)

## 1. Install Clawdapus

curl -sSL https://raw.githubusercontent.com/mostlydev/clawdapus/master/install.sh | sh

## 2. Set up credentials

cp .env.example .env
# Edit .env — add your OPENROUTER_API_KEY and platform bot token

## 3. Build

source .env
claw build -t quickstart-claw .

## 4. Run

claw up -d

## 5. Verify

claw ps          # Both assistant + cllama-passthrough should be running
claw health      # Both should show healthy

Open http://localhost:8081 for the cllama proxy dashboard.
You'll see every LLM call with token counts and cost tracking.

## 6. Talk to your bot

Use your platform (Discord/Telegram/Slack) to message your bot.
Every message routes through the cllama proxy — the bot has no direct API access.

## What's happening

- **Credential starvation:** Your API key is in the proxy only. The agent has a bearer token. It cannot call providers directly.
- **Behavioral contract:** `AGENTS.md` is mounted read-only. Even root inside the container can't change it.
- **Cost tracking:** The proxy logs every request with token usage and estimated cost.
- **Audit trail:** `claw logs cllama-passthrough` shows structured JSON audit logs.

## Migrate from existing OpenClaw

Already have an OpenClaw bot running? Import your config:

claw init --from ~/path/to/openclaw/config
# Generates Clawfile, claw-pod.yml, AGENTS.md pre-configured from your setup

## Clean up

claw down
