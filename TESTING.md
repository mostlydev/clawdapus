# Testing

Three tiers of tests, increasing in scope and external dependencies.

---

## Unit Tests

No external dependencies. Run anywhere.

```bash
go test ./...
go vet ./...
```

All packages are covered. These must pass before any commit.

---

## E2E Tests (Docker required)

Build-tagged `e2e`. Require Docker running locally. No real credentials needed —
tests use locally-built fixture images.

```bash
go test -tags e2e -v ./...
```

---

## Spike Tests (live Discord + Docker required)

Spike tests are the live end-to-end validation layer. They require real credentials,
Docker, and a real Discord server. They are not CI tests.

There are currently two spike paths:

- `TestSpikeRollCall`: the broad driver-parity validation path. Boots all 6 driver
  types plus `cllama` passthrough and `clawdash`, sends a Discord roll call, and
  verifies runtime-specific responses.
- `TestSpikeComposeUp`: the deeper trading-desk validation path. Focuses on artifact
  generation, startup wiring, and Discord activity for the richer multi-service example.

Run a spike test when implementing or validating driver/runtime behavior end to end.

### Rollcall Driver Parity Spike

The rollcall spike (`TestSpikeRollCall`) is the best single validation path for
cross-driver support. It uses [`examples/rollcall/`](./examples/rollcall/) and
exercises:

- `openclaw`
- `nullclaw`
- `microclaw`
- `nanoclaw`
- `nanobot`
- `picoclaw`
- `cllama` passthrough
- `clawdash`

### What it validates

- Base images build for all 6 driver families
- Agent images build from their `Clawfile`s
- `claw up` succeeds on the rollcall pod
- All agent containers converge to healthy/running state
- A Discord trigger message causes each runtime to post an AI-generated
  self-identification response
- `cllama` exposes cost data after traffic flows through the proxy

### Prerequisites

- Docker running
- Go toolchain
- A Discord server with:
  - One bot application token with permission to read and post in the target channel
  - A text channel for the roll call
  - An incoming webhook URL for posting the non-bot trigger message
- At least one LLM provider key:
  - `OPENROUTER_API_KEY` or
  - `ANTHROPIC_API_KEY`

### Setup

```bash
cd examples/rollcall
cp .env.example .env
# Edit .env with real values
```

Required `.env` values:

| Variable | What it is |
|----------|------------|
| `DISCORD_BOT_TOKEN` | Bot token used by all rollcall services |
| `DISCORD_BOT_ID` | Discord application/user ID for that bot |
| `DISCORD_GUILD_ID` | Discord server (guild) ID |
| `ROLLCALL_CHANNEL_ID` | Channel ID used for the roll call |
| `DISCORD_WEBHOOK_URL` | Incoming webhook URL used to post the trigger message |
| `OPENROUTER_API_KEY` | Optional, used by OpenRouter-backed passthrough services |
| `ANTHROPIC_API_KEY` | Optional, used by Anthropic-backed passthrough services |

### Running

```bash
go test -tags spike -v -run TestSpikeRollCall ./cmd/claw/...
```

Expected duration: 3-10 minutes depending on Docker cache warmth, image build time,
Discord gateway connection, and LLM latency.

### Output

The test logs:

- Image builds and compose startup progress
- Health convergence for each rollcall container
- Matching Discord responses for each runtime name
- Recent container logs on teardown or failure
- `cllama` health/cost endpoint checks

### Cleanup

Containers are torn down automatically on success, failure, or Ctrl-C.

If a run is killed hard, clean up manually:

```bash
docker compose -p rollcall down --volumes --remove-orphans
```

## Trading-Desk Spike

The trading-desk spike (`TestSpikeComposeUp`) remains the deeper artifact and
workflow validation instrument for the richer `examples/trading-desk/` example.

### What it validates

- `claw up` succeeds without error on `examples/trading-desk/claw-pod.yml`
- `openclaw.json` generated correctly: `channels.discord.token`, `guilds` keyed by
  guild ID, `groupPolicy`, `dmPolicy`, `allowBots`, `mentionPatterns`, peer `users[]`
- `jobs.json` generated correctly: `agentTurn` payloads with `delivery.mode=announce`
  and `delivery.to` resolved to the real channel ID
- `compose.generated.yml` contains correct bind mounts for `/app/config` and
  `/app/state/cron`
- Both agent containers start and serve mounted files at the expected paths
- `openclaw health --json` reports healthy inside the tiverton container
- Both agents post startup greetings to Discord (`tiverton online.`, `westin online.`)
- `trading-api` posts a webhook startup message mentioning both agent Discord IDs
  (`<@TIVERTON_ID>` and `<@WESTIN_ID>`) — proves `CLAW_HANDLE_*` env vars are
  broadcast to non-claw pod services

### Prerequisites

- Docker running
- Go toolchain
- A Discord server with:
  - Two bot applications (tiverton, westin), each with a bot token
  - A text channel the bots can read and post to
  - An incoming webhook URL for the `trading-api` startup announcement (optional —
    if absent, webhook posting is skipped and logged)

### Setup

```bash
cd examples/trading-desk
cp .env.example .env
# Edit .env with real values:
```

| Variable | What it is |
|----------|------------|
| `TIVERTON_BOT_TOKEN` | Bot token for the tiverton Discord application |
| `TIVERTON_DISCORD_ID` | Application/user ID for tiverton bot |
| `WESTIN_BOT_TOKEN` | Bot token for the westin Discord application |
| `WESTIN_DISCORD_ID` | Application/user ID for westin bot |
| `DISCORD_GUILD_ID` | Discord server (guild) ID |
| `DISCORD_TRADING_FLOOR_CHANNEL` | Channel ID the bots post to |
| `DISCORD_TRADING_API_WEBHOOK` | Incoming webhook URL (optional) |

Both bot tokens need **Read Messages** and **Send Messages** permissions in the target
channel. The test reads message history via the Discord REST API using these tokens.

### Running

```bash
go test -tags spike -v -run TestSpikeComposeUp ./cmd/claw/...
```

The test builds `trading-desk:latest` and `trading-api:latest` fresh on every run
(so image changes are always picked up). `openclaw:latest` is only built if not
already present locally.

Expected duration: 2–5 minutes depending on Docker layer cache warmth and Discord
gateway connection time.

### Output

The test logs:

- Generated artifact paths and content excerpts
- Container health state
- `openclaw health --json` output from inside the tiverton container
- `trading-api` env var presence (no values) and early logs
- Each Discord message found that matches a verification check

On failure, teardown dumps the last 100 log lines from each container.

### Cleanup

Containers are torn down automatically on test completion or Ctrl-C. Interrupt is
handled gracefully — compose down runs before exit.

If a run is killed hard (SIGKILL), clean up manually:

```bash
docker compose -p trading-desk down --volumes --remove-orphans
```

### Quickstart Docs Smoke Test

Validates that the documented quickstart shell blocks are runnable end-to-end in a
fresh Docker CLI container:

- Extracts shell code blocks from:
  - `README.md` quickstart section
  - `examples/quickstart/README.md`
- Runs extracted commands in a new `docker:27-cli` container (mounted to local repo
  + Docker socket)
- Rewrites `.env` from provided credentials and checks runtime health convergence
  (`assistant=healthy`, `cllama-passthrough=healthy`)
- Verifies cllama runtime signals in logs (`api listening on :8080`, `ui listening on :8081`)

Run:

```bash
go test -tags spike -v -run TestQuickstartDocsRunInFreshDockerContainer ./cmd/claw/...
```

Required env vars (or values in `examples/quickstart/.env`):

- `OPENROUTER_API_KEY`
- `DISCORD_BOT_TOKEN`
- `DISCORD_BOT_ID`
- `DISCORD_GUILD_ID`
