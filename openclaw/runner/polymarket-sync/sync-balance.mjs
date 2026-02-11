import fs from "node:fs/promises";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";
import { Wallet } from "ethers";
import { ClobClient, AssetType } from "@polymarket/clob-client";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

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

function nowIso() {
  return new Date().toISOString().replace(/\.\d{3}Z$/, "Z");
}

function parseBool(input, defaultValue = false) {
  if (input == null || input === "") return defaultValue;
  const v = String(input).toLowerCase();
  return v === "1" || v === "true" || v === "yes" || v === "on";
}

function toFiniteNumber(input, fallback = 0) {
  const n = Number(input);
  return Number.isFinite(n) ? n : fallback;
}

function normalizePrivateKey(key) {
  if (!key) return "";
  return key.startsWith("0x") ? key : `0x${key}`;
}

function toUsdNumber(input) {
  const raw = String(input ?? "").trim();
  if (raw === "") return 0;
  const n = Number(raw);
  if (!Number.isFinite(n)) return 0;

  // Some APIs return USDC with 6 decimals as integer micro-units.
  if (Math.abs(n) >= 1_000_000) {
    return n / 1_000_000;
  }
  return n;
}

function round4(n) {
  return Math.round(n * 10_000) / 10_000;
}

async function readJsonSafe(filePath) {
  try {
    const raw = await fs.readFile(filePath, "utf8");
    return JSON.parse(raw);
  } catch {
    return {};
  }
}

async function writeJsonAtomic(filePath, data) {
  const dir = path.dirname(filePath);
  await fs.mkdir(dir, { recursive: true });
  // Use a unique temp path so concurrent/manual runs cannot clobber each other.
  const tmp = `${filePath}.tmp.${process.pid}.${Date.now()}`;
  await fs.writeFile(tmp, `${JSON.stringify(data, null, 2)}\n`, "utf8");
  await fs.rename(tmp, filePath);
}

async function loadDerivedCreds(cachePath) {
  try {
    const raw = await fs.readFile(cachePath, "utf8");
    const parsed = JSON.parse(raw);
    if (parsed?.key && parsed?.secret && parsed?.passphrase) {
      return {
        key: parsed.key,
        secret: parsed.secret,
        passphrase: parsed.passphrase,
      };
    }
  } catch {
    // no-op
  }
  return null;
}

async function storeDerivedCreds(cachePath, creds) {
  const payload = {
    key: creds.key,
    secret: creds.secret,
    passphrase: creds.passphrase,
    updated_at: nowIso(),
    note: "Auto-derived Polymarket L2 creds from L1 signer; keep file private.",
  };
  await writeJsonAtomic(cachePath, payload);
}

async function main() {
  const enabled = parseBool(process.env.POLYMARKET_SYNC_ENABLED, false);
  if (!enabled) {
    console.log("[polymarket-sync] disabled");
    return;
  }

  const outputFile =
    process.env.POLYMARKET_SYNC_OUTPUT ||
    "/workspace/state/balance.json";
  const credsCacheFile =
    process.env.POLYMARKET_SYNC_CREDS_CACHE ||
    "/workspace/state/polymarket_l2_creds.json";
  const host = process.env.POLYMARKET_HOST || "https://clob.polymarket.com";
  const chainId = toFiniteNumber(process.env.POLYMARKET_CHAIN_ID, 137);
  const privateKey = normalizePrivateKey(process.env.POLYMARKET_PRIVATE_KEY || "");
  const apiKey = process.env.POLYMARKET_API_KEY || "";
  const apiSecret = process.env.POLYMARKET_API_SECRET || "";
  const apiPassphrase = process.env.POLYMARKET_API_PASSPHRASE || "";
  const configuredFunder = (process.env.POLYMARKET_FUNDER_ADDRESS || "").trim();
  const configuredSignatureType = process.env.POLYMARKET_SIGNATURE_TYPE;
  const burnPerHour = toFiniteNumber(process.env.POLYMARKET_SYNC_BURN_USD_PER_HOUR, 0);
  const cacheDerivedCreds = parseBool(process.env.POLYMARKET_SYNC_CACHE_DERIVED_CREDS, true);

  if (!privateKey) {
    throw new Error("missing required env var: POLYMARKET_PRIVATE_KEY");
  }

  const signer = new Wallet(privateKey);
  const signatureType =
    configuredSignatureType != null && configuredSignatureType !== ""
      ? Number(configuredSignatureType)
      : configuredFunder
        ? 2
        : 0;

  const funderAddress =
    signatureType === 0 ? signer.address : (configuredFunder || signer.address);

  const l1Client = new ClobClient(
    host,
    chainId,
    signer,
    undefined,
    signatureType,
    funderAddress
  );

  async function deriveCreds() {
    let derived;
    try {
      derived = await l1Client.createOrDeriveApiKey();
    } catch {
      // Some CLOB deployments reject create and only allow derive on existing creds.
      derived = await l1Client.deriveApiKey();
    }
    if (!derived?.key || !derived?.secret || !derived?.passphrase) {
      throw new Error("unable to derive valid Polymarket L2 API credentials");
    }
    if (cacheDerivedCreds) {
      await storeDerivedCreds(credsCacheFile, derived);
    }
    return derived;
  }

  let apiCreds = null;
  if (apiKey && apiSecret && apiPassphrase) {
    apiCreds = {
      key: apiKey,
      secret: apiSecret,
      passphrase: apiPassphrase,
    };
  } else {
    apiCreds = await loadDerivedCreds(credsCacheFile);
    if (!apiCreds) {
      apiCreds = await deriveCreds();
    }
  }

  async function fetchAllowanceWithCreds(creds) {
    const client = new ClobClient(
      host,
      chainId,
      signer,
      creds,
      signatureType,
      funderAddress
    );

    await client.updateBalanceAllowance({
      asset_type: AssetType.COLLATERAL,
    });

    const allowance = await client.getBalanceAllowance({
      asset_type: AssetType.COLLATERAL,
    });

    const rawBalance = String(allowance?.balance ?? "").trim();
    const rawAllowance = String(allowance?.allowance ?? "").trim();
    return { rawBalance, rawAllowance };
  }

  let { rawBalance, rawAllowance } = await fetchAllowanceWithCreds(apiCreds);

  // If configured creds fail, attempt L1 derive once and retry.
  if (rawBalance === "") {
    const derivedCreds = await deriveCreds();
    ({ rawBalance, rawAllowance } = await fetchAllowanceWithCreds(derivedCreds));
    apiCreds = derivedCreds;
  }

  if (rawBalance === "") {
    throw new Error(
      "balance sync failed: missing balance response (check POLYMARKET_API_KEY credentials, signature type, and funder address)"
    );
  }

  const totalBalance = toUsdNumber(rawBalance);
  const allowanceKnown = rawAllowance !== "";
  const allowedBalance = allowanceKnown ? toUsdNumber(rawAllowance) : totalBalance;
  const available = Math.max(0, Math.min(totalBalance, allowedBalance));

  const prev = await readJsonSafe(outputFile);
  const dailyPnl = Number.isFinite(Number(prev.daily_pnl_usd))
    ? Number(prev.daily_pnl_usd)
    : 0;
  const runway =
    burnPerHour > 0 ? round4(available / burnPerHour) : (
      Number.isFinite(Number(prev.runway_hours_estimate))
        ? Number(prev.runway_hours_estimate)
        : 0
    );

  const payload = {
    timestamp: nowIso(),
    bankroll_usd: round4(totalBalance),
    available_usd: round4(available),
    daily_pnl_usd: round4(dailyPnl),
    runway_hours_estimate: round4(runway),
    notes:
      "Auto-synced from Polymarket CLOB balance/allowance. Keep keys in env only.",
    source: {
      provider: "polymarket-clob",
      host,
      chain_id: chainId,
      signature_type: signatureType,
      signer_address: signer.address,
      funder_address: funderAddress,
      allowance_raw: allowanceKnown ? rawAllowance : null,
      balance_raw: rawBalance,
      creds_source: apiKey ? "env-or-fallback" : "derived",
    },
  };

  await writeJsonAtomic(outputFile, payload);
  console.log(
    `[polymarket-sync] updated ${outputFile} bankroll=${payload.bankroll_usd} available=${payload.available_usd}`
  );
}

main().catch((err) => {
  const msg = err instanceof Error ? err.message : String(err);
  console.error(`[polymarket-sync] error: ${msg}`);
  process.exitCode = 1;
});
