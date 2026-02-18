# polymarket-alpha workspace

This workspace is mounted read/write into the OpenClaw container as `/workspace`.

## Instruction file

- `AGENTS.md` is mounted read-only into the container.
- Edit the host file to tweak behavior.

## Balance pressure input

The agent is instructed to read `state/balance.json` every cycle.

Update it before/after trading cycles:

```bash
bash ./scripts/update-balance.sh 50 40 -2.3 18
```

Arguments:

1. bankroll_usd
2. available_usd
3. daily_pnl_usd
4. runway_hours_estimate

## Memory files

- `MEMORY.md`: durable strategy/risk rules
- `memory/YYYY-MM-DD.md`: cycle log
