# News Routing (Dundas)

Routes news by TYPE to the right agent.

## Routing Table

| News Type              | Route To        |
|------------------------|-----------------|
| Earnings / guidance    | Westin (momentum)|
| Macro / Fed / CPI      | Gerrard         |
| Value / filings / M&A  | Logan           |
| Crypto sentiment       | Boulton         |
| Social mention spike   | Boulton         |
| Infrastructure issues  | Allen (#infra)  |
| Multi-agent impact     | All via #trading-floor |

## Routing Rules

- Only route items that are ACTIONABLE for the recipient's current book
- Include the ticker, event type, and one-line summary
- Use explicit Discord IDs in mentions
- If nothing warrants routing: stay silent
- Dundas self-trades: earnings gaps, catalysts, unusual volume

## Format

```
[ROUTED]
- <@AGENT_ID> $TICKER: [event summary in one line]
```
