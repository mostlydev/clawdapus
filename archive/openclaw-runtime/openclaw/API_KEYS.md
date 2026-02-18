# API Keys You Need

## Minimum (required)

Set at least one model provider key:

- `ANTHROPIC_API_KEY`, or
- `OPENAI_API_KEY`, or
- `OPENROUTER_API_KEY`, or
- `GEMINI_API_KEY`

For trading on Polymarket CLOB, you also need:

- wallet private key for L1 signing (`POLYMARKET_PRIVATE_KEY`)
- L2 API credentials (`POLYMARKET_API_KEY`, `POLYMARKET_API_SECRET`, `POLYMARKET_API_PASSPHRASE`)
- for proxy/safe-style auth (signature type 1/2): funder/proxy address (`POLYMARKET_FUNDER_ADDRESS`)

Note:
- This stack can auto-derive L2 creds from L1 signer (`createOrDeriveApiKey`) if explicit L2 creds are missing or invalid.
- `Builder Keys` are not the same as standard CLOB user API credentials used by balance/order endpoints.

## How to get Polymarket keys

1. Create/import a wallet dedicated to this bot (do not reuse personal hot wallet keys).
2. Fund that wallet with USDC on Polygon (required for Polymarket activity).
3. Follow Polymarket CLOB auth flow to create/retrieve L2 credentials from your L1 signer.
4. Put those values in bot env:
   - `POLYMARKET_PRIVATE_KEY`
   - `POLYMARKET_API_KEY`
   - `POLYMARKET_API_SECRET`
   - `POLYMARKET_API_PASSPHRASE`
   - `POLYMARKET_SIGNATURE_TYPE` (`0` for EOA, `1`/`2` for proxy/safe flows)
   - `POLYMARKET_FUNDER_ADDRESS` (required for signature type `1`/`2`)

Reference:
- Polymarket CLOB authentication: https://docs.polymarket.com/developers/CLOB/authentication
- Polymarket CLOB clients (TypeScript): https://docs.polymarket.com/developers/CLOB/clients
- Polymarket Builder settings UI (for builder attribution keys): https://polymarket.com/settings?tab=builder

Important:
- If you only have a Builder API key ID (UUID) from Builder settings, that is not sufficient for CLOB balance sync/trading auth.
- CLOB sync/trading needs either:
  - valid `POLYMARKET_API_KEY` + `POLYMARKET_API_SECRET` + `POLYMARKET_API_PASSPHRASE` tied to your signer/funder, or
  - only `POLYMARKET_PRIVATE_KEY` with auto-derive enabled.

## Optional

- `VOYAGE_API_KEY` for Voyage-based memory embeddings in OpenClaw
- `POLYMARKET_SYNC_BURN_USD_PER_HOUR` if you want runway auto-derived in balance sync

## Crypto-funded model credits

If you want crypto-funded model credits via OpenRouter:

- `OPENROUTER_API_KEY`
- fund OpenRouter credits through their Coinbase crypto flow

## Should you create a separate OpenRouter account?

Recommended: yes, create a dedicated OpenRouter project/account (or at minimum a dedicated API key) per fleet.

Why:
- budget isolation
- easier kill-switch and rotation
- cleaner attribution of model spend/profit

Technically optional:
- you can reuse an existing OpenRouter account, but use a distinct key with spend limits where possible.

## Where model is selected

Choose model in one of these places:

1. Startup env:
   - set `OPENCLAW_MODEL_PRIMARY=provider/model` in `openclaw/bots/<bot>.env`
2. Runtime command:
   - `bash scripts/openclaw-cmd.sh <bot> 'openclaw models set <provider/model>'`
3. Direct config path:
   - `agents.defaults.model.primary` in `/state/openclaw/openclaw.json`

Source references:

- OpenRouter crypto credits API: https://openrouter.ai/docs/use-cases/crypto-api
- OpenClaw models/providers docs: https://docs.openclaw.ai/providers/models
