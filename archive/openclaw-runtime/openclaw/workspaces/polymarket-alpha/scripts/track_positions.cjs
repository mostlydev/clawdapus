#!/usr/bin/env node
/* eslint-disable no-console */

const fs = require("node:fs/promises");
const path = require("node:path");

const { ClobClient } = require("/opt/polymarket-sync/node_modules/@polymarket/clob-client");
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
const POSITIONS_FILE = process.env.POLY_POSITIONS_FILE || "/workspace/state/positions.json";
const TRADES_FILE = process.env.POLY_TRADES_FILE || "/workspace/state/trades.json";
const CREDS_CACHE_FILE =
  process.env.POLYMARKET_CREDS_FILE || "/workspace/state/polymarket_l2_creds.json";
const CACHE_DERIVED_CREDS = process.env.POLYMARKET_SYNC_CACHE_DERIVED_CREDS !== "0";

function normalizePrivateKey(key) {
  if (!key) return "";
  return key.startsWith("0x") ? key : `0x${key}`;
}

function nowIso() {
  return new Date().toISOString().replace(/\.\d{3}Z$/, "Z");
}

function round4(n) {
  return Math.round(n * 1e4) / 1e4;
}

async function loadJson(filePath, defaultVal = {}) {
  try {
    const data = await fs.readFile(filePath, "utf8");
    return JSON.parse(data);
  } catch {
    return defaultVal;
  }
}

async function saveJson(filePath, data) {
  const dir = path.dirname(filePath);
  await fs.mkdir(dir, { recursive: true });
  await fs.writeFile(filePath, JSON.stringify(data, null, 2) + "\n");
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
  
  let client;
  let positions = [];
  let signerAddress = "watch-only";
  
  if (privateKey) {
    const signer = new Wallet(normalizePrivateKey(privateKey));
    signerAddress = signer.address;
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
    client = new ClobClient(HOST, CHAIN_ID, signer, apiCreds, signatureType, funderAddress);
    console.log(
      `[positions] auth source=${apiCreds.source} signer=${signer.address} signature_type=${signatureType}`
    );
    
    try {
      // Try to get positions - method availability depends on SDK version
      positions = await client.getPositions?.() || [];
    } catch (err) {
      console.log(`[positions] API error (getPositions may not be available): ${err.message}`);
      positions = [];
    }
  } else {
    console.log("[positions] No POLYMARKET_PRIVATE_KEY set - running in watch-only mode");
  }

  const trades = await loadJson(TRADES_FILE, []);
  
  // Calculate metrics
  const openPositions = positions.filter((p) => toNum(p.size) !== 0);
  const totalPositions = positions.length;
  const totalOpenValue = openPositions.reduce((sum, p) => {
    return sum + toNum(p.size) * toNum(p.avg_price);
  }, 0);

  const tradeStats = trades.reduce(
    (acc, t) => {
      if (t.result?.success) {
        acc.successful++;
        acc.totalVolume += toNum(t.opportunity?.basket_cost) || 0;
      } else if (t.result?.error) {
        acc.failed++;
      }
      return acc;
    },
    { successful: 0, failed: 0, totalVolume: 0 }
  );

  const payload = {
    timestamp: nowIso(),
    wallet: {
      address: signerAddress,
    },
    positions: {
      total: totalPositions,
      open: openPositions.length,
      open_value_usd: round4(totalOpenValue),
      details: openPositions.slice(0, 10), // Limit output
    },
    trades: {
      total: trades.length,
      successful: tradeStats.successful,
      failed: tradeStats.failed,
      total_volume_usd: round4(tradeStats.totalVolume),
    },
    pnl: {
      daily: 0, // TODO: Calculate from position changes
      total_realized: 0,
      total_unrealized: round4(totalOpenValue),
    },
  };

  await saveJson(POSITIONS_FILE, payload);
  console.log(
    `[positions] tracked open=${payload.positions.open} value=$${payload.positions.open_value_usd} trades=${payload.trades.total}`
  );
}

function toNum(v) {
  const n = Number(v);
  return Number.isFinite(n) ? n : 0;
}

main().catch((err) => {
  console.error(`[positions] error: ${err.message}`);
  process.exitCode = 1;
});
