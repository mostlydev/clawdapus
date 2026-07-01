# Master Claw Example

Demonstrates a closed-loop fleet governor using a Master Claw (ADR-012): read
telemetry, check one reference policy threshold, and apply one scoped write
through `claw-api`.

## What's in the pod

- **worker-a** and **worker-b**: Simple OpenClaw agents on Discord
- **governor**: Fleet governor that monitors worker health via `claw-api`
- **cllama**: Shared LLM proxy (auto-injected) routing all inference
- **claw-api**: Governance API (auto-injected when `master:` is set)

## How it works

1. `x-claw.master: governor` tells `claw up` to auto-inject a `claw-api` service
2. Governor receives `CLAW_API_URL` and a scoped `CLAW_API_TOKEN`
3. `/fleet/alerts` is injected into the governor's context on every LLM turn
4. Governor has an INVOKE schedule that fires every 5 minutes for periodic review
5. When the reference cost policy is breached, governor calls
   `POST /fleet/budget/set` for the affected worker claw

## Running

```bash
cp .env.example .env
# Fill in Discord bot tokens and OPENROUTER_API_KEY
export CLAW_ALERT_MAX_COST_USD=2.00
claw pull
claw build
claw up -d
claw ps
claw logs governor
claw audit
```

`CLAW_ALERT_MAX_COST_USD` is a host-side setting forwarded into the
auto-injected `claw-api` container. Keep it high enough for normal use and low
enough for the governor to demonstrate the loop in a sandbox.

## Reference policy

The governor contract implements one deliberately small policy:

1. Read `/fleet/alerts?since=15m`.
2. If a worker alert says its cost crossed the configured threshold, write a
   stricter budget for that claw:

```bash
curl -sS -X POST \
  -H "Authorization: Bearer $CLAW_API_TOKEN" \
  -H "Content-Type: application/json" \
  "$CLAW_API_URL/fleet/budget/set" \
  -d '{"claw_id":"worker-a","max_requests":20,"window":"1h","behavior":"hard_stop"}'
```

That write creates `.claw-governance/<claw-id>/budget.json`, which cllama reads
on subsequent requests. This example uses `fleet.budget.set` because the
observable effect is a plain JSON override and does not require stopping a
container.

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
