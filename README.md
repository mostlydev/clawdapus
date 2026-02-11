# Clawdapus

Docker-based runtime for running one or many autonomous bots in parallel.

Two modes:

- **Generic Loop Runner** (`compose.yml` + `runner/`) — runs any shell command on an interval inside a container. Bring your own bot logic; Clawdapus handles scheduling, timeouts, jitter, and per-bot isolation.
- **OpenClaw Stack** (`openclaw/`) — full agent runtime powered by [OpenClaw](https://docs.openclaw.ai). Heartbeat-driven scheduling, workspace-backed memory, tool execution, and cron-scheduled periodic tasks.

## Layout

- `compose.yml`: generic loop-runner compose file
- `runner/`: generic loop-runner image
- `bots/*.env`: per-bot env files for generic loop runner
- `openclaw/`: OpenClaw-specific stack, workspaces, and docs
- `scripts/`: start/stop/log helper scripts for both modes
- `runtime/`: per-bot persisted runtime state

## Mode 1: Generic Loop Runner

Use this when you already have your own bot logic and only need scheduling/containerization.

### Quick Start

1. Create env files:

```bash
cp bots/example.env bots/alpha.env
cp bots/example.env bots/beta.env
```

2. Edit each env:

- `BOT_REPO_PATH`: host path to mounted workspace
- `BOT_COMMAND`: command executed each cycle inside `/workspace`
- Optional: `BOT_INTERVAL_SECONDS`, `BOT_FAIL_DELAY_SECONDS`, provider keys

3. Start:

```bash
bash scripts/fleet-up.sh
```

4. Logs:

```bash
bash scripts/fleet-logs.sh alpha
```

5. Stop:

```bash
bash scripts/fleet-down.sh
```

## Mode 2: OpenClaw Stack

Use this when you want agent runtime behavior (heartbeat, memory files, tool execution, isolated sessions).

See `openclaw/README.md`.

## Notes

- Keys are not required by the framework itself. Provide only what your bot needs in each bot env file.
- Keep credentials isolated per bot env.
- Generic loop runner logs persist under `runtime/<bot>/logs`.
