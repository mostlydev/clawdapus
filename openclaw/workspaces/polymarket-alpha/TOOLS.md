# TOOLS.md - Local Notes

Skills define _how_ tools work. This file is for _your_ specifics — the stuff that's unique to your setup.

## What Goes Here

Things like:

- SSH hosts and aliases
- Anything environment-specific

## Examples

```markdown
### SSH

- market-server → 192.168.1.100, user: admin

```

## Why Separate?

Skills are shared. Your setup is yours. Keeping them apart means you can update skills without losing your notes, and share skills without leaking your infrastructure.

---

Add whatever helps you do your job. This is your cheat sheet.

## Trading Utilities

- `node /workspace/scripts/run_cycle.cjs`
  - Runs adaptive full cycle: rotate lane -> mutate params -> scan -> discover -> execute -> track.
- `node /workspace/scripts/canary_trade_test.cjs`
  - Places a tiny `postOnly` canary BUY and immediately cancels all open orders.
  - Use to verify live trading path and credentials.
- `node /workspace/scripts/execute_opportunity.cjs`
  - Attempts execution from `/workspace/state/opportunities.json` under risk limits.
- `node /workspace/scripts/track_positions.cjs`
  - Captures current open positions and trade counters.
- `node /workspace/scripts/news_signal_scan.cjs`
  - Uses Brave search + current market topics to discover catalyst/news candidates.
- `node /workspace/scripts/cross_market_scan.cjs`
  - Detects monotonicity violations in related market clusters.
- `node /workspace/scripts/correlation_scan.cjs`
  - Scans event-level exclusivity inconsistencies.
- `node /workspace/scripts/risk_controls.cjs`
  - Kelly Criterion sizing, slippage estimation, exposure limits
- `node /workspace/scripts/opportunity_dashboard.cjs`
  - Unified signal aggregation and regime detection
- `node /workspace/scripts/survival_mode.cjs`
  - Burn rate analysis and cycle frequency recommendations
- `node /workspace/scripts/cycle_performance.cjs`
  - Tracks cycle metrics and no-opp streaks

## State Files

- `/workspace/state/balance.json` (wallet/bankroll snapshot from sync daemon)
- `/workspace/state/opportunities.json` (latest scanner output)
- `/workspace/state/trades.json` (execution history)
- `/workspace/state/positions.json` (position snapshot)
- `/workspace/state/strategy_state.json` (lane rotation + parameter mutation state)
- `/workspace/state/news_signal_snapshot.json` (discovery/catalyst findings)
- `/workspace/state/cross_market_snapshot.json` (cluster consistency findings)
- `/workspace/state/correlation_snapshot.json` (event-level correlation findings)

## Safety

- Never print raw secrets from env.
- Keep API keys in env only; do not write them to memory logs.
