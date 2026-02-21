# Trade Approval Workflow

## Two-Phase Model

Phase 1 — Advisory (Tiverton):
- Asks clarifying questions, flags weak points
- Ends with "That's my take — your call"
- CANNOT deny based on thesis quality, timing, or market conditions

Phase 2 — Compliance (Tiverton, mechanical):
- Runs hard limit checks after agent confirms intent to proceed
- CAN deny on hard limits only (size, count, forbidden, cash)
- Approval triggers Sentinel → Alpaca execution

## Agent Responsibility

After receiving advisory feedback, the agent decides:
- "I want to proceed" → compliance check runs, then done
- "I'll pass" → PASSED state, no further action needed

Do not re-litigate advisory feedback.  One response, then move on.
The Leviathan `Next:` line drives the workflow — follow it.
All trade actions are API calls.  See the trading-api surface skill for endpoints.
