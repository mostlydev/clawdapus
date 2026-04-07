# Master Claw Example

Demonstrates fleet governance using a Master Claw (ADR-012).

## What's in the pod

- **worker-a** and **worker-b**: Simple OpenClaw agents on Discord
- **governor**: Fleet governor that monitors worker health via `claw-api`
- **cllama**: Shared LLM proxy (auto-injected) routing all inference
- **claw-api**: Governance API (auto-injected when `master:` is set)

## How it works

1. `x-claw.master: governor` tells `claw up` to auto-inject a `claw-api` service
2. Governor receives a bearer token for `claw-api` via `CLAW_API_URL` env var
3. `/fleet/alerts` feed is injected into governor's context on every LLM turn
4. Governor has an INVOKE schedule that fires every 5 minutes for periodic review

## Running

```bash
cp .env.example .env
# Fill in Discord bot tokens and OPENROUTER_API_KEY
claw pull
claw build
claw up -d
claw ps
claw logs governor
claw audit
```

## Feed authentication

The governor's `/fleet/alerts` feed requires bearer auth. `claw up` automatically:
1. Generates a bearer token for the master claw principal
2. Writes the token into the feed manifest (`feeds.json`) as `auth`
3. cllama's feed fetcher sends `Authorization: Bearer <token>` on authenticated feeds

## Alert thresholds

Configure alert sensitivity via env vars on the host (forwarded to `claw-api`):

```bash
CLAW_ALERT_ERROR_RATE_PERCENT=5.0      # default: 5%
CLAW_ALERT_MAX_COST_USD=10.0           # default: $10 per query window
CLAW_ALERT_FEED_ERROR_RATE_PERCENT=20.0 # default: 20%
CLAW_ALERT_INTERVENTION_COUNT=5         # default: 5
```
