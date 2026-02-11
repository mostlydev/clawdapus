# API Keys / Secrets Checklist

The fleet framework does not require keys by itself.
Keys depend on the command you run in `BOT_COMMAND`.

## Usually Required

- One LLM provider key:
  - `ANTHROPIC_API_KEY`
  - `OPENAI_API_KEY`
  - `OPENROUTER_API_KEY`

## Trading / Venue Credentials (if your strategy executes trades)

- Market API credentials (exchange-specific), often:
  - API key / secret / passphrase
- Wallet credential for signing (if required by venue):
  - private key or managed signer config

## Recommended Per-Bot Isolation

- Use one `.env` file per bot under `bots/`.
- Keep separate wallet/API credentials per bot.
- Never share production keys across all bots.
