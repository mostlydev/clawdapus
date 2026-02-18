# CYCLE CONTROL

Operator-editable cycle instructions. Read every heartbeat.

## Architecture

Cron runs your scripts automatically inside this container. The schedule is in `/workspace/crontab`. All env vars are available via `. /etc/bot-env.sh` (sourced in each cron line). Cron output goes to `state/logs/`.

You can view, edit, and reload your crontab:
- Read: `crontab -l`
- Edit: write to `/workspace/crontab`, then run `crontab /workspace/crontab`
- Logs: read `state/logs/*.log`

Your job each heartbeat:
1. Read `state/` files for fresh data from cron jobs.
2. Check `state/logs/` for errors.
3. Analyze the data. Think. Act.

## Mission

Survival first, growth second. Increase bankroll net of all costs.

## What "act" means

If the cron data shows opportunities: execute a trade.

If the cron data shows nothing: **that is your problem to solve, not a reason to do nothing.** 63 consecutive empty cycles means the scripts need to change. You must iterate:

- Lower thresholds in `scripts/clob_scan_opportunities.cjs` or `run_cycle.cjs`.
- Add new scanning strategies. Write new scripts.
- Search different market segments, time horizons, or signal sources.
- Fix bugs in existing scripts that might be filtering too aggressively.
- Read the actual orderbook data and understand WHY nothing passes.

Zero-action heartbeats are not allowed except during hard outages. If you can't trade, improve the system and record what you changed.

## Your workspace is yours

Edit any file in `/workspace` (scripts, state, memory, CYCLE.md, MEMORY.md, TOOLS.md). Only AGENTS.md is read-only.

If a script isn't working, fix it. If a script is missing, write it. Changes are picked up by cron on its next iteration.

## Persistence

- Update `MEMORY.md` with durable learnings so you don't repeat mistakes.
- Update `TOOLS.md` with tools you create or modify.
- Write daily notes to `memory/YYYY-MM-DD.md`.

## Env

- Trading signer key: `POLYMARKET_PRIVATE_KEY` (canonical). `PRIVATE_KEY` is a compat alias.

## Output minimum each heartbeat

- `BANKROLL_STATUS`
- `HYPOTHESIS` (what edge or improvement you are testing)
- `ACTION_COUNT` (must be >= 1)
- `ACTIONS` (what you concretely did)
- `RESULT` (measured outcome)
- `NEXT_MOVE` (what changes next cycle based on evidence)
