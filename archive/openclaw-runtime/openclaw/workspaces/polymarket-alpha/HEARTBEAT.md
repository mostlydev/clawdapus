# HEARTBEAT

Run every heartbeat:

1. Read `CYCLE.md` **EVERY** heartbeat first (operator override and current mission).  This will change over time.
2. Execute `node /workspace/scripts/run_cycle.cjs` as the baseline action loop unless hard outage.
3. Read `state/balance.json` and restate key fields.
4. Treat each heartbeat as an action cycle (high-frequency mode; API burn is continuous).
5. Choose the highest-value next action for survival and growth, using any available tool or data source.
6. Strategy selection is open-ended: discover opportunities, test ideas, execute risk-bounded trades when edge is credible, or improve the system when edge is unclear.
7. Avoid repeating the same reasoning loop. Consecutive cycles should show adaptation, not template output.
8. Rotate strategy lanes. Do not stay in pure arbitrage mode for more than two consecutive cycles.
9. Include external or cross-market validation regularly (for example `node /workspace/scripts/external_signal_scan.cjs`).
10. Target `ACTION_COUNT >= 2` when tooling is available (baseline + exploration).
11. If no trade executes, still complete one concrete improvement or experiment and record evidence.
12. Report: `BANKROLL_STATUS`, `BURN_PRESSURE`, `HYPOTHESIS`, `ACTION_COUNT` (>=1 unless hard outage), `ACTIONS`, `RESULT`, `NEXT_MOVE`, and `IMPROVEMENT_DELTA` (when files changed).
13. Do not reply only `HEARTBEAT_OK` during normal operation.
14. Do not declare termination on a single zero-balance reading; confirm with two fresh sync samples at least 60s apart.

Output contract:

1. `BANKROLL_STATUS`
2. `BURN_PRESSURE`
3. `HYPOTHESIS` (what edge or improvement you are testing)
4. `ACTION_COUNT` (integer, must be `>=1` unless hard outage)
5. `ACTIONS` (concrete actions taken this cycle)
6. `RESULT` (measured outcome, including failures)
7. `NEXT_MOVE` (what changes next cycle based on evidence)
8. `IMPROVEMENT_DELTA` with exact file path(s) edited this cycle when files were changed.
9. Do not return only `HEARTBEAT_OK` during normal operation.
