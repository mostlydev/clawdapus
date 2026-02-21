# trading-api

**Host:** trading-api
**Port:** 4000
**Base URL:** `http://trading-api:4000`

All requests and responses are JSON. All trade actions require an `agent` field
identifying the requesting agent (your username, e.g. `"westin"`).

---

## Workflow

Every trade follows a two-phase flow driven by the `next` field in each response.
Read `next` and follow it — do not skip phases.

```
POST /trades/propose   →  status: "advisory"
                              ↓  (Tiverton reviews and responds on Discord)
POST /trades/{id}/confirm  →  status: "approved"  (queued for execution)
   or
POST /trades/{id}/cancel   →  status: "cancelled"
```

Tiverton will mention you on Discord with advisory feedback after you propose.
Wait for that before confirming. If you decide to pass, cancel the trade.

---

## Endpoints

### Health

```
GET /health
→ { "status": "ok", "time": "<iso8601>" }
```

### Propose a trade

```
POST /trades/propose
{
  "agent":       "westin",          // required — your username
  "symbol":      "NVDA",            // required — ticker symbol, uppercased
  "side":        "buy",             // required — "buy" or "sell"
  "quantity":    50,                // required — number of shares / units
  "price_limit": 142.50,            // optional — limit price (omit for market)
  "thesis":      "Momentum breakout above 200-day MA on volume confirmation."
                                    // optional but strongly recommended
}
→ 201 { "id": "a3f9c1b2", "status": "advisory", "next": "...", ... }
```

### Get trade status

```
GET /trades/{id}
→ 200 { "id": "...", "status": "advisory|approved|cancelled", "next": "...", ... }
```

### List all trades

```
GET /trades
→ 200 [ { trade }, ... ]
```

### Confirm a trade (after Tiverton advisory)

```
POST /trades/{id}/confirm
→ 200 { "status": "approved", "next": "Trade approved and queued for execution.", ... }
```

Only valid when status is `"advisory"`. Compliance checks run on confirm.
If a hard limit is breached, the response will say `"status": "rejected"` with a reason.

### Cancel a trade

```
POST /trades/{id}/cancel
→ 200 { "status": "cancelled", ... }
```

---

## Status values

| Status      | Meaning                                      |
|-------------|----------------------------------------------|
| `advisory`  | Proposed, awaiting Tiverton review           |
| `approved`  | Confirmed, queued for Alpaca execution       |
| `rejected`  | Blocked by compliance (hard limit breached)  |
| `cancelled` | Agent passed or explicitly cancelled         |
| `filled`    | Executed by Alpaca                           |
| `failed`    | Execution error — check `error` field        |

---

## Notes

- You do not have direct Alpaca access. All execution goes through this API.
- Hard limits (position size, daily count, forbidden tickers) are enforced on confirm.
  See `risk-limits.md` for current thresholds.
- The `next` field in every response tells you exactly what to do next. Follow it.
