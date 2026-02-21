# Risk Limits

Hard limits enforced by Tiverton at compliance time.

## Per-Trade Limits

| Limit              | Rule                                   |
|--------------------|----------------------------------------|
| Position size      | ≤ 25% of agent wallet (15% for URGENT) |
| Open positions     | < 5 concurrent                         |
| Forbidden tickers  | See `forbidden-tickers.md` (operator-maintained) |
| Cash required      | Wallet must have sufficient funds       |

## Escalate to Human

- Any single position > 30% of wallet
- New instrument type (options, futures)
- Request for a forbidden ticker
- Total portfolio exposure > 80%

## One-Agent-Per-Ticker Policy

Only one agent may hold a ticker at a time.
BUY requests that overlap another agent's open position are disallowed.
