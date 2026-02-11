# AGENTS.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

Clawdapus is a Docker-based runtime for running one or many autonomous OpenClaw bots in parallel. Each bot runs inside its own container with heartbeat scheduling, workspace-backed memory, tool execution, and cron-scheduled tasks via the `openclaw` npm CLI.

## Common Commands

All scripts are in `scripts/` and run from the repo root.

```bash
bash scripts/openclaw-up.sh alpha     # start a specific bot (builds + health check)
bash scripts/openclaw-up.sh           # start all bots from openclaw/bots/*.env
bash scripts/openclaw-down.sh alpha   # stop a specific bot
bash scripts/openclaw-down.sh         # stop all

# Observability
bash scripts/openclaw-logs.sh alpha                  # docker compose logs
bash scripts/openclaw-tail-session.sh alpha --with-tools  # live session JSONL stream
bash scripts/openclaw-console.sh alpha               # health + heartbeat + live conversation
bash scripts/openclaw-last.sh alpha                  # health + balance + last assistant message
bash scripts/openclaw-live.sh alpha                  # combined session + cron job logs

# Run arbitrary openclaw CLI inside container
bash scripts/openclaw-cmd.sh alpha 'openclaw health --json'
bash scripts/openclaw-cmd.sh alpha 'openclaw models set openrouter/anthropic/claude-sonnet-4'
```

## Architecture

### Compose Stack

`openclaw/compose.yml` defines a single `openclaw` service using `openclaw/runner/Dockerfile` (node-based with `openclaw` npm package). The agent gateway handles heartbeat + sessions + tool execution. Cron runs inside the container to schedule periodic tasks (balance sync, opportunity scanning, cycle execution) via a workspace `crontab` file.

### Per-Bot Isolation

Each bot gets its own:
- **Env file**: `openclaw/bots/<name>.env`
- **Docker Compose project**: `openclaw-<name>`
- **State directory**: `openclaw/runtime/<name>/`
- **Workspace mount**: `BOT_REPO_PATH -> /workspace` (read/write)

Scripts resolve bot names to env files: pass either a name (`alpha`) or explicit path (`/path/to/file.env`).

### Container Internals

- `openclaw.json` is generated on the host by `openclaw-up.sh` (from env vars) and bind-mounted **read-only** into the container. The bot cannot change its own heartbeat frequency, model, or scheduling.
- The openclaw npm package is made non-writable at build time (`chmod -R a-w`) to prevent runtime self-patching.
- A system heartbeat cron is installed in `/etc/cron.d/heartbeat-override` by the entrypoint — it fires `openclaw gateway call wake` at the operator-configured `OPENCLAW_HEARTBEAT_EVERY` interval. The bot cannot disable or modify this cron.
- The bot's workspace crontab (`/workspace/crontab`) is installed separately and is bot-editable. The bot manages its own task crons.
- Entrypoint (`openclaw/runner/entrypoint.sh`) runs `openclaw setup`, generates a gateway auth token (persisted at `/state/openclaw/gateway.token`), installs crons, then starts `openclaw gateway`.
- `openclaw-cmd.sh` auto-injects the gateway token before running commands.
- Session data lives at `/state/openclaw/agents/main/sessions/` as JSONL files.
- The Dockerfile patches OpenClaw's cron prompt relay to empty string (removes built-in cron reminder text from heartbeat payloads).

### Workspace Model

Bot workspaces live in `openclaw/workspaces/<bot>/` and contain:
- `AGENTS.md` — agent instructions (mounted read-only)
- `CYCLE.md` — operator override / mission control input
- `MEMORY.md` — durable lessons
- `memory/YYYY-MM-DD.md` — daily research/strategy log
- `scripts/` — bot-specific scripts (e.g., `run_cycle.cjs`, `clob_scan_opportunities.cjs`)
- `state/` — runtime artifacts (balance.json, opportunities.json, trades.json, positions.json)

Directories `state/`, `memory/`, `research/`, `logs/` are gitignored per workspace.

## Adding a New Bot

```bash
cp openclaw/bots/example.env openclaw/bots/mybot.env
# Edit openclaw/bots/mybot.env: set BOT_REPO_PATH, AGENTS_FILE_PATH, model/provider keys
# Create workspace: mkdir -p openclaw/workspaces/mybot && cp openclaw/workspaces/default/AGENTS.md openclaw/workspaces/mybot/
bash scripts/openclaw-up.sh mybot
```

## Gotchas

- **AGENTS.md is mounted read-only** inside containers. Edit the file on the host at `AGENTS_FILE_PATH`; changes appear inside the container immediately but the agent only re-reads on session/heartbeat boundaries.
- **Dockerfile patches node_modules at build time** (removes cron prompt relay text). Changing `OPENCLAW_NPM_PACKAGE` version requires verifying the regex patch in the Dockerfile still matches the new package.
- **Cron jobs log to `/workspace/state/logs/`** inside the container. The agent can read these logs and the workspace `crontab` is editable by the agent at runtime (`crontab /workspace/crontab` to reload).
- **Gateway token is auto-generated on first run** and persisted at `/state/openclaw/gateway.token`. If you delete the state directory, the token regenerates and any external references to the old token break.
- **Entrypoint uses `set -euo pipefail`** — config errors are fatal.

## Key Conventions

- Shell scripts use `set -euo pipefail` and the `sanitize()` function to normalize bot names to DNS-safe slugs for Docker project names.
- All timing values (intervals, delays, timeouts) are configured via env vars and validated as non-negative integers at startup.
- Periodic tasks (balance sync, scanning, cycle execution) run via cron inside the container, scheduled by the workspace `crontab` file.
- The OpenClaw npm package version is pinned via `OPENCLAW_NPM_PACKAGE` build arg (default: `openclaw@2026.2.9`).
