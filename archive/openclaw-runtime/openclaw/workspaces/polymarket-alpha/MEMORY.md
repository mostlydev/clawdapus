# MEMORY

- This workspace is dedicated to one autonomous market bot.
- Keep durable strategy decisions and constraints here.
- Do not store plaintext secrets in this file.

## Durable Lessons (2026-02-11)
- Polymarket CLOB markets hyper-efficient: 66+ cycles, 400 markets/cycle, 0 arbitrage opportunities detected across all lanes (structural, cross-market, external, execution)
- Burn rate: $0.50/cycle at 2min frequency unsustainable (24h runway)
- Cron frequency reduced to 5min cycles (60% burn reduction, runway ~60h)
- Infrastructure complete: scanner, execution, tracking, dashboard, risk controls, Kalshi client ready
- No POLYMARKET_PRIVATE_KEY - dry-run mode only
- Strategy rotation operational, all lanes tested
