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

## Spike Test (live Discord + Docker required)

The spike test (`TestSpikeComposeUp`) is the primary end-to-end validation instrument
for the trading-desk example. It builds images, runs `claw compose up`, verifies all
generated artifacts, starts containers, and confirms live Discord activity.

**It is not a CI test.** It requires real credentials and a real Discord server.
Run it when implementing or validating new driver behavior end-to-end.

### What it validates

- `claw compose up` succeeds without error on `examples/trading-desk/claw-pod.yml`
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
