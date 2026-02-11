# OpenClaw Fleet

Containerized OpenClaw runtime for one or many bots, each with isolated workspace and state.

All paths below are relative to the repository root.

## Features

- Per-bot isolated OpenClaw container with cron-scheduled periodic tasks
- Immutable config — `openclaw.json` is generated on the host and bind-mounted read-only; the bot cannot change its own heartbeat frequency or model
- System heartbeat cron (`/etc/cron.d/`) fires at the operator-set interval regardless of bot behavior
- Bot-managed workspace crons for tasks the bot controls
- Workspace-backed memory and editable strategy files
- Tool/runtime execution inside the mounted workspace

## Directory Model

- `openclaw/compose.yml`: OpenClaw stack definition
- `openclaw/bots/*.env`: per-bot configuration
- `openclaw/workspaces/<bot>/`: bot workspace (strategy files, scripts, state)
- `scripts/openclaw-*.sh`: lifecycle and observability helpers

Container mounts:

- `BOT_REPO_PATH -> /workspace` (read/write)
- `AGENTS_FILE_PATH -> /workspace/AGENTS.md` (read-only)
- `BOT_STATE_PATH -> /state` (read/write)

## Quick Start

1. Create a bot env file:

```bash
cp openclaw/bots/example.env openclaw/bots/alpha.env
```

2. Edit at minimum:

- `BOT_REPO_PATH`
- `AGENTS_FILE_PATH`
- model/provider keys
- heartbeat/cycle settings you want enabled

3. Start:

```bash
bash scripts/openclaw-up.sh alpha
```

4. Stop:

```bash
bash scripts/openclaw-down.sh alpha
```

## Operate and Observe

Run OpenClaw CLI in-container:

```bash
bash scripts/openclaw-cmd.sh alpha 'openclaw health --json'
```

Log streams:

```bash
bash scripts/openclaw-logs.sh alpha
bash scripts/openclaw-tail-session.sh alpha --with-tools
bash scripts/openclaw-console.sh alpha
bash scripts/openclaw-last.sh alpha
bash scripts/openclaw-live.sh alpha
```

`openclaw-live.sh` combines assistant session stream with cron job logs.

## Run Multiple Bots

Create one env file per bot in `openclaw/bots/`, each with its own workspace/state paths.

Start all:

```bash
bash scripts/openclaw-up.sh
```

Stop all:

```bash
bash scripts/openclaw-down.sh
```

## Configuration Overview

Core:

- `OPENCLAW_MODEL_PRIMARY`
- `OPENCLAW_HEARTBEAT_EVERY`
- `OPENCLAW_HEARTBEAT_TARGET`

Credentials (as needed):

- `OPENROUTER_API_KEY` / `OPENAI_API_KEY` / `ANTHROPIC_API_KEY` / `GEMINI_API_KEY`
- venue credentials required by your strategy

Cron-scheduled tasks (configured via workspace `crontab`):

- Balance sync: `POLYMARKET_SYNC_*`
- Opportunity scan: `POLY_SCAN_*`

## Publishing Notes

- Remove secrets from all `*.env` files before publishing.
- Keep `openclaw/bots/example.env` as template-only.
- Avoid committing runtime state under `openclaw/workspaces/*/state` unless intentional.
