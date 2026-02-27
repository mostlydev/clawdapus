# Trading Desk Example

A reference pod showing two OpenClaw agents running as a governed fleet — each with a Discord presence, scheduled tasks, and shared infrastructure context.
This spike now enables cllama passthrough routing for trader agents (`cllama-passthrough`).

## What's in the box

| File | Purpose |
|------|---------|
| `Clawfile` | Shared agent image — all trader agents build from this |
| `claw-pod.yml` | Pod definition — per-agent handles, surfaces, invoke schedules |
| `Dockerfile.openclaw-base` | Builds the local `openclaw:latest` base image |
| `Dockerfile.trading-api` | Mock trading API with skill label emission |
| `entrypoint.sh` | Baked into the base image — starts gateway, sends greeting |
| `agents/` | Per-agent AGENTS.md contracts |
| `policy/` | Shared skills (risk limits, approval workflow) |

## Prerequisites

- Docker with Compose V2 (`docker compose version`)
- `claw` binary on PATH (`go build -o bin/claw ./cmd/claw`)
- Discord bot tokens — one per agent — plus server/channel IDs

## .env file

Copy `.env.example` and fill in real values:

```
TIVERTON_BOT_TOKEN=Bot ...
WESTIN_BOT_TOKEN=Bot ...
DUNDAS_BOT_TOKEN=Bot ...
BOULTON_BOT_TOKEN=Bot ...
GERRARD_BOT_TOKEN=Bot ...
LOGAN_BOT_TOKEN=Bot ...
ALLEN_BOT_TOKEN=Bot ...

DISCORD_GUILD_ID=...
DISCORD_TRADING_FLOOR_CHANNEL=...
DISCORD_INFRA_CHANNEL=...

POSTGRES_PASSWORD=...
OPENROUTER_API_KEY=...
ANTHROPIC_API_KEY=...
```

## Running

```bash
# Build base image once (skip if openclaw:latest exists)
docker build -t openclaw:latest -f Dockerfile.openclaw-base .

# Deploy the full fleet
claw up claw-pod.yml

# Check health
docker exec trading-desk-tiverton-1 openclaw health --json

# Tail logs
docker compose -f compose.generated.yml logs -f tiverton
```

## Spike integration test

`cmd/claw/spike_test.go` is a full end-to-end test that:

1. Builds all images (Clawfile → `trading-desk:latest`, mock trading API)
2. Runs `claw up` on a pre-expanded pod YAML
3. Asserts generated artifacts — `openclaw.json` structure, `jobs.json` channel IDs, compose mounts
4. Asserts cllama wiring — proxy service emitted, provider endpoints in `models.providers.*` rewritten to `cllama-passthrough`, bearer token injected, per-agent context dir generated
5. Waits for containers to be healthy
6. Polls the Discord channel REST API to confirm startup greetings arrived

**Requirements:**
- Docker running
- Real bot tokens in `examples/trading-desk/.env` (`TIVERTON_BOT_TOKEN` and `WESTIN_BOT_TOKEN` at minimum)
- Provider key for proxy env (`OPENROUTER_API_KEY`; `ANTHROPIC_API_KEY` optional fallback)
- Internet access from Docker containers (no internal-only Docker Desktop network mode)

**Run:**

```bash
go test -tags spike -v -run TestSpikeComposeUp -timeout 300s ./cmd/claw/
```

The test skips automatically if `TIVERTON_BOT_TOKEN` is not set in `.env`.

**What it verifies:**

| Assertion | What it checks |
|-----------|---------------|
| `openclaw.json` structure | `channels.discord.token`, `guilds` keyed by guild ID |
| `openclaw.json` cllama rewrite | `models.providers.<provider>.baseUrl` points to `cllama-passthrough`, `apiKey` is per-agent token |
| `jobs.json` structure | `agentTurn` payload, `delivery.to` = resolved channel ID |
| cllama context files | `.claw-runtime/context/<agent>/` contains `AGENTS.md`, `CLAWDAPUS.md`, `metadata.json` |
| Compose mounts | `/app/config` directory, `/app/state/cron` directory |
| Container readability | `/app/config/openclaw.json`, `/app/state/cron/jobs.json`, `/claw/AGENTS.md` |
| Skills populated | `/claw/skills/` contains extracted skill files |
| Health check | Docker healthcheck reports healthy |
| Discord greetings | Messages appear in the channel via REST API polling |
