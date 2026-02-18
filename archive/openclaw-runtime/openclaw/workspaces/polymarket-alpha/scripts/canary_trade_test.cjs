#!/usr/bin/env node
/* eslint-disable no-console */

const { ClobClient, Side } = require("/opt/polymarket-sync/node_modules/@polymarket/clob-client");
const { Wallet } = require("/opt/polymarket-sync/node_modules/ethers");

const originalConsoleError = console.error.bind(console);
const redactionPatterns = [
  /"POLY_API_KEY":"[^"]*"/g,
  /"POLY_PASSPHRASE":"[^"]*"/g,
  /"POLY_SIGNATURE":"[^"]*"/g,
  /"POLY_PRIVATE_KEY":"[^"]*"/g,
  /"secret":"[^"]*"/g,
  /"passphrase":"[^"]*"/g,
  /"apiKey":"[^"]*"/g,
];

function redactSensitive(text) {
  let out = text;
  for (const pattern of redactionPatterns) {
    out = out.replace(pattern, (m) => {
      const idx = m.indexOf(":");
      if (idx === -1) return m;
      return `${m.slice(0, idx + 1)}"REDACTED"`;
    });
  }
  return out;
}

console.error = (...args) => {
  const joined = args
    .map((a) => (typeof a === "string" ? a : JSON.stringify(a)))
    .join(" ");
  originalConsoleError(redactSensitive(joined));
};

function normalizePrivateKey(pk) {
  if (!pk) return "";
  return pk.startsWith("0x") ? pk : `0x${pk}`;
}

async function main() {
  const host = process.env.POLYMARKET_HOST || "https://clob.polymarket.com";
  const chainId = Number(process.env.POLYMARKET_CHAIN_ID || 137);
  const signatureType = Number(process.env.POLYMARKET_SIGNATURE_TYPE || 2);
  const funderAddress = (process.env.POLYMARKET_FUNDER_ADDRESS || "").trim();
  const privateKey = normalizePrivateKey(process.env.POLYMARKET_PRIVATE_KEY || process.env.PRIVATE_KEY || "");

  const apiKey = process.env.POLYMARKET_API_KEY || "";
  const apiSecret = process.env.POLYMARKET_API_SECRET || "";
  const apiPassphrase = process.env.POLYMARKET_API_PASSPHRASE || "";

  if (!privateKey) {
    throw new Error("POLYMARKET_PRIVATE_KEY not set");
  }
  if (!apiKey || !apiSecret || !apiPassphrase) {
    throw new Error("Missing POLYMARKET_API_KEY / POLYMARKET_API_SECRET / POLYMARKET_API_PASSPHRASE");
  }

  const signer = new Wallet(privateKey);
  const client = new ClobClient(
    host,
    chainId,
    signer,
    {
      key: apiKey,
      secret: apiSecret,
      passphrase: apiPassphrase,
    },
    signatureType,
    funderAddress || signer.address
  );

  const tokenID = process.env.CANARY_TOKEN_ID || "21265207456609426291246075480390336499088453711419597084147957999650569091884";
  const minSize = Number(process.env.CANARY_MIN_SIZE || 5);

  const book = await client.getOrderBook(tokenID);
  const bestBid = Number(book?.bids?.[0]?.price || 0.01);
  const bestAsk = Number(book?.asks?.[0]?.price || 0.99);

  const price = Math.max(0.01, Math.min(bestBid || 0.01, bestAsk - 0.01));
  const size = Math.max(5, Number.isFinite(minSize) ? minSize : 5);

  const orderResp = await client.createAndPostOrder(
    {
      tokenID,
      side: Side.BUY,
      price,
      size,
    },
    undefined,
    undefined,
    false,
    true
  );

  const openAfter = await client.getOpenOrders({});
  const cancel = await client.cancelAll();

  const payload = {
    ok: true,
    signer: signer.address,
    signature_type: signatureType,
    funder_address: funderAddress || signer.address,
    token_id: tokenID,
    best_bid: bestBid,
    best_ask: bestAsk,
    test_price: price,
    test_size: size,
    order: orderResp,
    open_after_count: Array.isArray(openAfter?.data)
      ? openAfter.data.length
      : Array.isArray(openAfter)
        ? openAfter.length
        : null,
    cancel,
  };

  console.log(JSON.stringify(payload, null, 2));
}

main().catch((err) => {
  console.error(`[canary] error: ${err?.message || String(err)}`);
  process.exit(1);
});
