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
claw up -d
claw ps
claw logs governor
claw audit
```
