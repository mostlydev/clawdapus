#!/usr/bin/env node
/* eslint-disable no-console */

const fs = require("node:fs/promises");
const path = require("node:path");

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

const HOST = process.env.POLYMARKET_HOST || "https://clob.polymarket.com";
const CHAIN_ID = Number(process.env.POLYMARKET_CHAIN_ID || 137);
const OPPORTUNITIES_FILE = process.env.POLY_OPPORTUNITIES_FILE || "/workspace/state/opportunities.json";
const TRADES_LOG = process.env.POLY_TRADES_LOG || "/workspace/state/trades.json";
const MAX_POSITION_USD = Number(process.env.POLY_MAX_POSITION_USD || 10);
const MIN_EDGE_EXECUTE = Number(process.env.POLY_MIN_EDGE_EXECUTE || 0.01); // 1% min to execute
const MIN_SHARES_PER_LEG = Number(process.env.POLY_MIN_SHARES_PER_LEG || 1);
const CREDS_CACHE_FILE =
  process.env.POLYMARKET_CREDS_FILE || "/workspace/state/polymarket_l2_creds.json";
const CACHE_DERIVED_CREDS = process.env.POLYMARKET_SYNC_CACHE_DERIVED_CREDS !== "0";

function nowIso() {
  return new Date().toISOString().replace(/\.\d{3}Z$/, "Z");
}

function normalizePrivateKey(key) {
  if (!key) return "";
  return key.startsWith("0x") ? key : `0x${key}`;
}

async function loadOpportunities() {
  try {
    const data = await fs.readFile(OPPORTUNITIES_FILE, "utf8");
    return JSON.parse(data);
  } catch (err) {
    return { opportunities: [] };
  }
}

async function logTrade(trade) {
  let trades = [];
  try {
    const data = await fs.readFile(TRADES_LOG, "utf8");
    trades = JSON.parse(data);
  } catch {
    trades = [];
  }
  trades.push({
    timestamp: nowIso(),
    ...trade,
  });
  await fs.writeFile(TRADES_LOG, JSON.stringify(trades, null, 2) + "\n");
}

async function loadCachedCreds() {
  try {
    const data = await fs.readFile(CREDS_CACHE_FILE, "utf8");
    const parsed = JSON.parse(data);
    if (parsed?.key && parsed?.secret && parsed?.passphrase) {
      return {
        key: String(parsed.key),
        secret: String(parsed.secret),
        passphrase: String(parsed.passphrase),
        source: "cache",
      };
    }
  } catch {
    // ignore
  }
  return null;
}

async function storeCachedCreds(creds) {
  const payload = {
    key: creds.key,
    secret: creds.secret,
    passphrase: creds.passphrase,
    updated_at: nowIso(),
    note: "Auto-derived Polymarket L2 creds from L1 signer; keep file private.",
  };
  const dir = path.dirname(CREDS_CACHE_FILE);
  await fs.mkdir(dir, { recursive: true });
  await fs.writeFile(CREDS_CACHE_FILE, JSON.stringify(payload, null, 2) + "\n");
}

async function resolveApiCreds(l1Client) {
  const envCreds =
    process.env.POLYMARKET_API_KEY &&
    process.env.POLYMARKET_API_SECRET &&
    process.env.POLYMARKET_API_PASSPHRASE
      ? {
          key: process.env.POLYMARKET_API_KEY,
          secret: process.env.POLYMARKET_API_SECRET,
          passphrase: process.env.POLYMARKET_API_PASSPHRASE,
          source: "env",
        }
      : null;
  if (envCreds) return envCreds;

  const cachedCreds = await loadCachedCreds();
  if (cachedCreds) return cachedCreds;

  let derived;
  try {
    if (typeof l1Client.createOrDeriveApiKey === "function") {
      derived = await l1Client.createOrDeriveApiKey();
    } else if (typeof l1Client.createApiKey === "function") {
      derived = await l1Client.createApiKey();
    }
  } catch (createErr) {
    if (typeof l1Client.deriveApiKey === "function") {
      derived = await l1Client.deriveApiKey();
    } else {
      throw createErr;
    }
  }

  if (!derived?.key || !derived?.secret || !derived?.passphrase) {
    throw new Error("Failed to resolve Polymarket L2 credentials");
  }

  if (CACHE_DERIVED_CREDS) {
    await storeCachedCreds(derived);
  }

  return {
    key: derived.key,
    secret: derived.secret,
    passphrase: derived.passphrase,
    source: "derived",
  };
}

async function executeBuyBoth(client, opp, maxUsd) {
  const priceA = Number(opp.token_a?.best_ask);
  const priceB = Number(opp.token_b?.best_ask);
  if (!Number.isFinite(priceA) || !Number.isFinite(priceB) || priceA <= 0 || priceB <= 0) {
    return { error: "Invalid ask price(s) on opportunity" };
  }

  // Budget is split across both legs in USD terms.
  const usdPerLeg = Math.max(0, maxUsd / 2);
  let sizeA = usdPerLeg / priceA;
  let sizeB = usdPerLeg / priceB;

  const askSizeA = Number(opp.token_a?.ask_size);
  const askSizeB = Number(opp.token_b?.ask_size);
  if (Number.isFinite(askSizeA) && askSizeA > 0) sizeA = Math.min(sizeA, askSizeA);
  if (Number.isFinite(askSizeB) && askSizeB > 0) sizeB = Math.min(sizeB, askSizeB);

  // Keep legs balanced to reduce directional exposure.
  const size = Math.min(sizeA, sizeB);
  if (!Number.isFinite(size) || size < MIN_SHARES_PER_LEG) {
    return { error: `Size below minimum (${MIN_SHARES_PER_LEG} shares per leg)` };
  }

  const orders = [];
  
  try {
    const orderA = await client.createAndPostOrder({
      tokenID: opp.token_a.token_id,
      price: priceA,
      size: size,
      side: Side.BUY,
    });
    orders.push(orderA);
  } catch (err) {
    return { error: `Failed to buy token_a: ${err.message}` };
  }

  try {
    const orderB = await client.createAndPostOrder({
      tokenID: opp.token_b.token_id,
      price: priceB,
      size: size,
      side: Side.BUY,
    });
    orders.push(orderB);
  } catch (err) {
    return { error: `Failed to buy token_b: ${err.message}` };
  }

  return {
    success: true,
    orders,
    sizing: {
      shares_per_leg: size,
      usd_per_leg_estimate: size * ((priceA + priceB) / 2),
      total_usd_estimate: size * (priceA + priceB),
    },
  };
}

async function executeSellBoth(client, opp, maxUsd) {
  // Requires holding both tokens - check positions first
  return { error: "Sell both requires existing positions - not yet implemented" };
}

async function main() {
  const privateKey = process.env.POLYMARKET_PRIVATE_KEY || process.env.PRIVATE_KEY;
  const configuredFunder = (process.env.POLYMARKET_FUNDER_ADDRESS || "").trim();
  const configuredSignatureType = process.env.POLYMARKET_SIGNATURE_TYPE;
  const signatureType =
    configuredSignatureType != null && configuredSignatureType !== ""
      ? Number(configuredSignatureType)
      : configuredFunder
        ? 2
        : 0;

  if (!privateKey) {
    console.log("[execute] No POLYMARKET_PRIVATE_KEY set - running in dry-run mode");
  }

  const oppData = await loadOpportunities();
  const opportunities = oppData.opportunities || [];

  // Filter for executable opportunities
  const executable = opportunities.filter(
    (o) => o.gross_edge >= MIN_EDGE_EXECUTE && o.max_shares_at_best >= 5
  );

  if (executable.length === 0) {
    console.log(`[execute] No executable opportunities (need ${MIN_EDGE_EXECUTE * 100}%+ edge)`);
    return;
  }

  console.log(`[execute] Found ${executable.length} executable opportunities`);

  if (!privateKey) {
    console.log("[execute] Dry-run - would execute:");
    for (const opp of executable.slice(0, 3)) {
      console.log(`  - ${opp.type} edge=${(opp.gross_edge * 100).toFixed(2)}%`);
    }
    return;
  }

  // Live execution
  const signer = new Wallet(normalizePrivateKey(privateKey));
  const funderAddress =
    signatureType === 0 ? signer.address : (configuredFunder || signer.address);
  const l1Client = new ClobClient(
    HOST,
    CHAIN_ID,
    signer,
    undefined,
    signatureType,
    funderAddress
  );
  const apiCreds = await resolveApiCreds(l1Client);
  console.log(
    `[execute] auth source=${apiCreds.source} signer=${signer.address} signature_type=${signatureType}`
  );
  const client = new ClobClient(
    HOST,
    CHAIN_ID,
    signer,
    apiCreds,
    signatureType,
    funderAddress
  );

  for (const opp of executable.slice(0, 1)) { // Execute max 1 per cycle
    let result;
    if (opp.type === "buy_both") {
      result = await executeBuyBoth(client, opp, MAX_POSITION_USD);
    } else if (opp.type === "sell_both") {
      result = await executeSellBoth(client, opp, MAX_POSITION_USD);
    } else {
      result = { error: `Unknown opportunity type: ${opp.type}` };
    }

    await logTrade({
      opportunity: opp,
      result,
    });

    if (result.success) {
      console.log(`[execute] SUCCESS: ${opp.type} edge=${(opp.gross_edge * 100).toFixed(2)}%`);
    } else {
      console.log(`[execute] FAILED: ${opp.type} - ${result.error}`);
    }
  }
}

main().catch((err) => {
  console.error(`[execute] error: ${err.message}`);
  process.exitCode = 1;
});
