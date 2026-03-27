# Desk Risk Context Feed Plan

## Status

- Implemented live on Tiverton on 2026-03-27.
- The live rollout also exposed a Clawdapus compiler gap:
  - plain `build:` services sharing one build context could not self-describe independently
  - descriptor discovery now inspects each service's configured Dockerfile labels before falling back to a default `.claw-describe.json`

## Goal

Add a first-class desk-wide risk feed for coordinator/risk agents so Tiverton does not consume trader-scoped `market-context` data.

## Current State

- Live Tiverton pod uses pod-level `feeds-defaults` in `~/tiverton-house/claw-pod.yml`:
  - `market-context -> /api/v1/market_context/{claw_id}`
- That default applies to `tiverton` as well as traders.
- `trading-api`'s `MarketContextService` is intentionally per-agent:
  - positions are scoped to `@agent.id`
  - wallet is `@agent.wallet`
  - buying power is derived from that same wallet
- Result:
  - Tiverton truthfully reports Tiverton's own wallet
  - Tiverton does not see desk-wide trader risk

## Design Decision

This should be implemented as a provider-owned service feed in `trading-api`, not as role logic in Clawdapus core.

Why:

- Matches Clawdapus principles:
  - provider owns, consumer subscribes
  - one canonical descriptor
  - services self-describe
- Avoids teaching `claw up` about semantic roles like "risk officer"
- Keeps feed semantics explicit and stable

## Feed Shape

Add a new `trading-api` feed:

- Name: `desk-risk-context`
- Endpoint: `/api/v1/desk_risk_context/{claw_id}`

Passing `{claw_id}` is still useful:

- allows service-side tailoring later
- preserves consumer identity in logs and auth
- stays consistent with current feed substitution machinery

## Payload Scope

`desk-risk-context` should be desk-wide, not agent-wallet scoped.

Expected fields:

- timestamp
- market_status
- trader_wallets
  - one entry per funded trader
  - wallet size, cash, invested, utilization
- open_positions
  - ticker, agent, qty, cost basis/current value, unrealized P&L
- pending_orders
  - grouped by agent and status
- exposure_summary
  - gross exposure
  - per-agent exposure
  - per-ticker concentration
- risk_alerts
  - empty array when healthy
  - populated with hard-limit, stale-price, stale-sync, or concentration warnings
- recent_fills
  - desk-wide recent fills

## Clawdapus Wiring

### Target End State

`trading-api` self-describes both feeds via `claw.describe` / `.claw-describe.json`:

- `market-context`
- `desk-risk-context`

Then pod services subscribe by short-form name:

- traders: `feeds: [market-context]`
- tiverton: `feeds: [desk-risk-context]`
- sentinel: likely `feeds: [fleet-alerts, desk-risk-context]`

### Interim Reality

The live `trading-api` service is not yet self-described from this repo.
Implementation will likely need both:

1. Live Rails-side endpoint/service
2. Live pod YAML feed subscription updates

Descriptor work should still be added to the example/local Clawdapus side so the canonical pattern is captured in-tree.

## Pod Wiring Change

Remove the current pod-wide default:

- `feeds-defaults: market-context`

Replace it with role-specific feed wiring:

- trader anchor:
  - `feeds: [market-context]`
- Tiverton:
  - `feeds: [desk-risk-context]`
- Sentinel:
  - `feeds: [fleet-alerts, desk-risk-context]` if desired

Do not keep `market-context` as a universal default.

## Implementation Steps

1. Add a new Rails service, likely `DeskRiskContextService`.
2. Add `Api::V1::DeskRiskContextController#show`.
3. Add route:
   - `get 'desk_risk_context/:agent_id', to: 'desk_risk_context#show'`
4. Add Rails specs for:
   - payload shape
   - desk-wide aggregation
   - non-funded/infrastructure agent requests
5. Update live Tiverton pod YAML:
   - remove universal `market-context` default
   - wire traders to `market-context`
   - wire Tiverton to `desk-risk-context`
   - wire Sentinel as needed
6. Update local example pod YAML in-repo to match the same convention.
7. Add/prepare descriptor support for `trading-api` feed names if the service image path is available in-repo or via live service build context.
8. Redeploy Tiverton and validate:
   - Tiverton feed now includes Logan/Gerrard wallets and open positions
   - Tiverton no longer reports zero desk state when traders have live risk

## Validation Checklist

- `curl http://trading-api:4000/api/v1/desk_risk_context/tiverton`
  returns desk-wide trader risk
- `cllama` logs show Tiverton fetching `desk-risk-context`
- Tiverton answers mention with desk-wide positions/wallets
- traders still receive their own scoped `market-context`
- no regression in existing `market_context` specs

## Rollout Notes

- This change spans two codebases:
  - local Clawdapus repo
  - live Tiverton `services/trading-api`
- Record each major rollout step in `docs/migrations/2026-03-26-tiverton-openclaw-migration.md`
- Rebuild/redeploy order:
  1. Rails trading-api
  2. Clawdapus binary / pod YAML
  3. `claw up -d`
