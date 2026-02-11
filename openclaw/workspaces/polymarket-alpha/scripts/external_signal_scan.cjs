#!/usr/bin/env node
/* eslint-disable no-console */

const fs = require("node:fs/promises");
const path = require("node:path");

const OUTPUT =
  process.env.POLY_EXTERNAL_SNAPSHOT_OUTPUT ||
  "/workspace/state/external_signal_snapshot.json";
const GAMMA_LIMIT = Number(process.env.POLY_GAMMA_LIMIT || 500);
const GAMMA_PAGES = Number(process.env.POLY_GAMMA_PAGES || 4);
const TIMEOUT_MS = Number(process.env.POLY_EXTERNAL_TIMEOUT_MS || 9000);

function nowIso() {
  return new Date().toISOString().replace(/\.\d{3}Z$/, "Z");
}

function toNum(v, fallback = NaN) {
  const n = Number(v);
  return Number.isFinite(n) ? n : fallback;
}

function round4(n) {
  return Math.round(n * 1e4) / 1e4;
}

async function writeJsonAtomic(filePath, data) {
  const dir = path.dirname(filePath);
  await fs.mkdir(dir, { recursive: true });
  const tmp = `${filePath}.tmp.${process.pid}.${Date.now()}`;
  await fs.writeFile(tmp, `${JSON.stringify(data, null, 2)}\n`, "utf8");
  await fs.rename(tmp, filePath);
}

async function fetchJson(url, timeoutMs = TIMEOUT_MS) {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  try {
    const res = await fetch(url, {
      signal: controller.signal,
      headers: { accept: "application/json" },
    });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    return await res.json();
  } finally {
    clearTimeout(timer);
  }
}

function median(values) {
  const clean = values.filter((v) => Number.isFinite(v)).sort((a, b) => a - b);
  if (clean.length === 0) return NaN;
  const mid = Math.floor(clean.length / 2);
  if (clean.length % 2 === 0) return (clean[mid - 1] + clean[mid]) / 2;
  return clean[mid];
}

function parseJsonArray(v) {
  if (Array.isArray(v)) return v;
  if (typeof v !== "string") return [];
  try {
    const p = JSON.parse(v);
    return Array.isArray(p) ? p : [];
  } catch {
    return [];
  }
}

function parseMoneyToken(token) {
  if (!token) return NaN;
  const clean = String(token)
    .toLowerCase()
    .replace(/\$/g, "")
    .replace(/,/g, "")
    .trim();
  const m = clean.match(/^([0-9]+(?:\.[0-9]+)?)([kmb])?$/i);
  if (!m) return NaN;
  const base = Number(m[1]);
  if (!Number.isFinite(base)) return NaN;
  const suffix = (m[2] || "").toLowerCase();
  const mult =
    suffix === "k" ? 1e3 : suffix === "m" ? 1e6 : suffix === "b" ? 1e9 : 1;
  return base * mult;
}

function extractThreshold(question) {
  const q = String(question || "");
  const patterns = [
    {
      direction: "above",
      re: /\b(?:above|over|greater than|more than|at least)\s*\$?\s*([0-9][0-9,]*(?:\.[0-9]+)?(?:\s*[kmb])?)/i,
    },
    {
      direction: "below",
      re: /\b(?:below|under|less than|at most)\s*\$?\s*([0-9][0-9,]*(?:\.[0-9]+)?(?:\s*[kmb])?)/i,
    },
    {
      direction: "hit",
      re: /\b(?:hit|reach|touch)\s*\$?\s*([0-9][0-9,]*(?:\.[0-9]+)?(?:\s*[kmb])?)/i,
    },
  ];
  for (const p of patterns) {
    const m = q.match(p.re);
    if (!m) continue;
    const threshold = parseMoneyToken(m[1]);
    if (Number.isFinite(threshold)) {
      return { direction: p.direction, threshold };
    }
  }
  return null;
}

function parseYesNoPricing(m) {
  const outcomes = parseJsonArray(m.outcomes).map((x) =>
    String(x || "").trim().toLowerCase()
  );
  const pricesRaw = parseJsonArray(m.outcomePrices);
  const prices = pricesRaw.map((x) => toNum(x));
  if (outcomes.length === 0 || outcomes.length !== prices.length) return null;

  const yesIdx = outcomes.findIndex((o) => o === "yes");
  const noIdx = outcomes.findIndex((o) => o === "no");
  if (yesIdx === -1 && noIdx === -1) return null;

  const yesPrice =
    yesIdx >= 0 ? prices[yesIdx] : noIdx >= 0 ? 1 - prices[noIdx] : NaN;
  const noPrice =
    noIdx >= 0 ? prices[noIdx] : yesIdx >= 0 ? 1 - prices[yesIdx] : NaN;
  if (!Number.isFinite(yesPrice) || !Number.isFinite(noPrice)) return null;
  return { yesPrice, noPrice };
}

function daysToEnd(endDateIso) {
  const t = Date.parse(String(endDateIso || ""));
  if (!Number.isFinite(t)) return NaN;
  return (t - Date.now()) / (1000 * 60 * 60 * 24);
}

function buildBtcHeuristics(markets, btcSpot) {
  const candidates = [];
  const parsed = [];

  for (const m of markets) {
    const pricing = parseYesNoPricing(m);
    const thresholdInfo = extractThreshold(m.question);
    const dte = daysToEnd(m.endDateIso || m.endDate);
    if (!pricing || !thresholdInfo) continue;

    const { yesPrice, noPrice } = pricing;
    const { direction, threshold } = thresholdInfo;
    if (!Number.isFinite(threshold)) continue;

    let moneyness = NaN;
    if (Number.isFinite(btcSpot) && threshold > 0) {
      if (direction === "above" || direction === "hit") {
        moneyness = (btcSpot - threshold) / threshold;
      } else if (direction === "below") {
        moneyness = (threshold - btcSpot) / threshold;
      }
    }

    const row = {
      id: m.id,
      question: m.question,
      end: m.endDateIso || m.endDate || null,
      days_to_end: Number.isFinite(dte) ? round4(dte) : null,
      liquidity: toNum(m.liquidityNum, 0),
      volume_24h: toNum(m.volume24hr, 0),
      direction,
      threshold_usd: round4(threshold),
      yes_price: round4(yesPrice),
      no_price: round4(noPrice),
      moneyness: Number.isFinite(moneyness) ? round4(moneyness) : null,
    };
    parsed.push(row);

    if (!Number.isFinite(moneyness) || !Number.isFinite(dte)) continue;
    if (dte < 0 || dte > 14) continue;

    const favorable = moneyness >= 0.04;
    const unfavorable = moneyness <= -0.04;
    if (favorable && yesPrice < 0.55) {
      candidates.push({
        type: "possible_underpriced_yes",
        score: round4(0.55 - yesPrice + Math.min(0.25, Math.abs(moneyness))),
        ...row,
      });
    } else if (unfavorable && yesPrice > 0.45) {
      candidates.push({
        type: "possible_overpriced_yes",
        score: round4(yesPrice - 0.45 + Math.min(0.25, Math.abs(moneyness))),
        ...row,
      });
    }
  }

  const monotonicViolations = [];
  const byEnd = new Map();

  for (const p of parsed) {
    if (!Number.isFinite(p.threshold_usd)) continue;
    const day = String(p.end || "").slice(0, 10);
    const key = `${day}|${p.direction}`;
    if (!byEnd.has(key)) byEnd.set(key, []);
    byEnd.get(key).push(p);
  }

  for (const [key, rows] of byEnd.entries()) {
    if (rows.length < 2) continue;
    const [day, direction] = key.split("|");
    rows.sort((a, b) => a.threshold_usd - b.threshold_usd);

    for (let i = 1; i < rows.length; i++) {
      const prev = rows[i - 1];
      const curr = rows[i];
      if (direction === "above" || direction === "hit") {
        // Higher threshold should not have higher YES probability.
        if (curr.yes_price > prev.yes_price + 0.03) {
          monotonicViolations.push({
            day,
            direction,
            lower_threshold: prev.threshold_usd,
            lower_yes: prev.yes_price,
            higher_threshold: curr.threshold_usd,
            higher_yes: curr.yes_price,
            delta: round4(curr.yes_price - prev.yes_price),
          });
        }
      } else if (direction === "below") {
        // Higher threshold should not have lower YES probability.
        if (curr.yes_price < prev.yes_price - 0.03) {
          monotonicViolations.push({
            day,
            direction,
            lower_threshold: prev.threshold_usd,
            lower_yes: prev.yes_price,
            higher_threshold: curr.threshold_usd,
            higher_yes: curr.yes_price,
            delta: round4(prev.yes_price - curr.yes_price),
          });
        }
      }
    }
  }

  candidates.sort((a, b) => b.score - a.score);
  parsed.sort((a, b) => b.volume_24h - a.volume_24h);

  return {
    parsed_markets: parsed.slice(0, 80),
    candidates: candidates.slice(0, 25),
    monotonic_violations: monotonicViolations.slice(0, 25),
  };
}

async function fetchBtcSpotBundle() {
  const out = [];
  const add = (source, price, error = null) => {
    out.push({
      source,
      price_usd: Number.isFinite(price) ? round4(price) : null,
      error,
    });
  };

  try {
    const j = await fetchJson("https://api.binance.com/api/v3/ticker/price?symbol=BTCUSDT");
    add("binance", toNum(j?.price));
  } catch (err) {
    add("binance", NaN, err.message);
  }

  try {
    const j = await fetchJson("https://api.coinbase.com/v2/prices/BTC-USD/spot");
    add("coinbase", toNum(j?.data?.amount));
  } catch (err) {
    add("coinbase", NaN, err.message);
  }

  try {
    const j = await fetchJson("https://api.kraken.com/0/public/Ticker?pair=XBTUSD");
    const firstPair = Object.values(j?.result || {})[0];
    const px = Array.isArray(firstPair?.c) ? toNum(firstPair.c[0]) : NaN;
    add("kraken", px);
  } catch (err) {
    add("kraken", NaN, err.message);
  }

  const prices = out.map((s) => s.price_usd).filter((v) => Number.isFinite(v));
  return {
    median_btc_usd: Number.isFinite(median(prices)) ? round4(median(prices)) : null,
    sources: out,
  };
}

async function fetchGammaMarkets() {
  const markets = [];
  for (let page = 0; page < GAMMA_PAGES; page++) {
    const offset = page * GAMMA_LIMIT;
    const url = `https://gamma-api.polymarket.com/markets?active=true&closed=false&limit=${GAMMA_LIMIT}&offset=${offset}`;
    try {
      const batch = await fetchJson(url);
      if (!Array.isArray(batch) || batch.length === 0) break;
      markets.push(...batch);
    } catch (err) {
      // Keep going; partial data is still useful.
      markets.push({
        __error: `gamma_page_${page}: ${err.message}`,
      });
    }
  }
  return markets;
}

async function main() {
  const started = Date.now();

  const [spotBundle, gammaRaw] = await Promise.all([
    fetchBtcSpotBundle(),
    fetchGammaMarkets(),
  ]);

  const gammaErrors = gammaRaw.filter((m) => m && m.__error).map((m) => m.__error);
  const gammaMarkets = gammaRaw.filter((m) => m && !m.__error);
  const btcMarkets = gammaMarkets.filter((m) => {
    const q = String(m.question || "").toLowerCase();
    return q.includes("bitcoin") || /\bbtc\b/.test(q);
  });

  const heuristics = buildBtcHeuristics(
    btcMarkets,
    toNum(spotBundle.median_btc_usd)
  );

  const payload = {
    timestamp: nowIso(),
    source: {
      gamma_limit: GAMMA_LIMIT,
      gamma_pages: GAMMA_PAGES,
      timeout_ms: TIMEOUT_MS,
    },
    external: {
      btc_spot_median_usd: spotBundle.median_btc_usd,
      btc_spot_sources: spotBundle.sources,
    },
    gamma: {
      fetched_markets: gammaMarkets.length,
      btc_markets: btcMarkets.length,
      errors: gammaErrors.slice(0, 10),
    },
    heuristics: {
      candidate_count: heuristics.candidates.length,
      monotonic_violation_count: heuristics.monotonic_violations.length,
      candidates: heuristics.candidates,
      monotonic_violations: heuristics.monotonic_violations,
    },
    markets: heuristics.parsed_markets,
    duration_ms: Date.now() - started,
  };

  await writeJsonAtomic(OUTPUT, payload);
  console.log(
    `[external-scan] btcSpot=${payload.external.btc_spot_median_usd ?? "na"} btcMarkets=${payload.gamma.btc_markets} candidates=${payload.heuristics.candidate_count} monoViolations=${payload.heuristics.monotonic_violation_count} in ${payload.duration_ms}ms`
  );
}

main().catch((err) => {
  const msg = err instanceof Error ? err.message : String(err);
  console.error(`[external-scan] error: ${msg}`);
  process.exitCode = 1;
});

