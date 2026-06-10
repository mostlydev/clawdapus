# News Routing (News-Router)

Routes news by TYPE to the right agent.

## Routing Table

| News Type              | Route To        |
|------------------------|-----------------|
| Earnings / guidance    | Momentum-Trader |
| Macro / Fed / CPI      | Macro-Trader    |
| Value / filings / M&A  | Value-Trader    |
| Crypto sentiment       | Social-Trader   |
| Social mention spike   | Social-Trader   |
| Infrastructure issues  | Systems-Monitor (#infra) |
| Multi-agent impact     | All via #trading-floor |

## Routing Rules

- Only route items that are ACTIONABLE for the recipient's current book
- Include the ticker, event type, and one-line summary
- Use explicit Discord IDs in mentions
- If nothing warrants routing: stay silent
- News-Router self-trades: earnings gaps, catalysts, unusual volume

## Format

```
[ROUTED]
- <@AGENT_ID> $TICKER: [event summary in one line]
```
