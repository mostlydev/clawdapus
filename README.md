# Clawdapus

Docker-based runtime for running one or many autonomous [OpenClaw](https://docs.openclaw.ai) bots in parallel, each with isolated workspace and state.

## Features

- Per-bot isolated OpenClaw container with cron-scheduled periodic tasks
- Immutable config — `openclaw.json` is generated on the host and bind-mounted read-only; the bot cannot change its own heartbeat frequency or model
- System heartbeat cron (`/etc/cron.d/`) fires at the operator-set interval regardless of bot behavior
- Bot-managed workspace crons for tasks the bot controls
- Workspace-backed memory and editable strategy files
- Tool/runtime execution inside the mounted workspace

## Quick Start

1. Create a bot env file:

```bash
cp openclaw/bots/example.env openclaw/bots/alpha.env
```

2. Edit at minimum:

- `BOT_REPO_PATH` — host path to the bot workspace
- `AGENTS_FILE_PATH` — host path to the agent instructions file
- model/provider keys (`OPENROUTER_API_KEY`, `ANTHROPIC_API_KEY`, etc.)
- heartbeat/cycle settings you want enabled

3. Start:

```bash
bash scripts/openclaw-up.sh alpha
```

4. Stop:

```bash
bash scripts/openclaw-down.sh alpha
```

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

## Operate and Observe

Run OpenClaw CLI in-container:

```bash
bash scripts/openclaw-cmd.sh alpha 'openclaw health --json'
```

Log streams:

```bash
bash scripts/openclaw-logs.sh alpha                       # docker compose logs
bash scripts/openclaw-tail-session.sh alpha --with-tools   # live session JSONL stream
bash scripts/openclaw-console.sh alpha                     # health + heartbeat + live conversation
bash scripts/openclaw-last.sh alpha                        # health + balance + last assistant message
bash scripts/openclaw-live.sh alpha                        # combined session + cron job logs
```

## Directory Layout

```
openclaw/
  compose.yml          # Docker Compose stack definition
  bots/*.env           # per-bot configuration
  workspaces/<bot>/    # bot workspace (strategy files, scripts, state)
  runner/              # Dockerfile + entrypoint
  runtime/<bot>/       # persisted runtime state (gitignored)
scripts/openclaw-*.sh  # lifecycle and observability helpers
```

Container mounts:

- `BOT_REPO_PATH -> /workspace` (read/write)
- `AGENTS_FILE_PATH -> /workspace/AGENTS.md` (read-only)
- `BOT_STATE_PATH -> /state` (read/write)

## Configuration

Core:

- `OPENCLAW_MODEL_PRIMARY` — model identifier (e.g. `openrouter/anthropic/claude-sonnet-4`)
- `OPENCLAW_HEARTBEAT_EVERY` — heartbeat interval (e.g. `30m`)
- `OPENCLAW_HEARTBEAT_TARGET` — heartbeat target (e.g. `none`)

Credentials (as needed):

- `OPENROUTER_API_KEY` / `OPENAI_API_KEY` / `ANTHROPIC_API_KEY` / `GEMINI_API_KEY`
- venue credentials required by your strategy

Cron-scheduled tasks (configured via workspace `crontab`):

- Balance sync: `POLYMARKET_SYNC_*`
- Opportunity scan: `POLY_SCAN_*`

See `openclaw/API_KEYS.md` for full key setup guide.

## Adding a New Bot

```bash
cp openclaw/bots/example.env openclaw/bots/mybot.env
# Edit: set BOT_REPO_PATH, AGENTS_FILE_PATH, model/provider keys
# Create workspace:
mkdir -p openclaw/workspaces/mybot
cp openclaw/workspaces/default/AGENTS.md openclaw/workspaces/mybot/
bash scripts/openclaw-up.sh mybot
```

## Publishing Notes

- Remove secrets from all `*.env` files before publishing.
- Keep `openclaw/bots/example.env` as template-only.
- Avoid committing runtime state under `openclaw/workspaces/*/state` unless intentional.
