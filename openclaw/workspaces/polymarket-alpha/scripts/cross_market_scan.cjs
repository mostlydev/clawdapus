#!/usr/bin/env node
/* eslint-disable no-console */

/**
 * Cross-market consistency scanner.
 * Finds monotonicity and probability-shape violations in related market clusters.
 */

const fs = require("node:fs/promises");
const path = require("node:path");

const GAMMA_API = "https://gamma-api.polymarket.com/markets";
const OUTPUT = "/workspace/state/cross_market_snapshot.json";

function nowIso() {
  return new Date().toISOString().replace(/\.\d{3}Z$/, "Z");
}

function toNum(v, fallback = 0) {
  const n = Number(v);
  return Number.isFinite(n) ? n : fallback;
}

async function writeJsonAtomic(filePath, data) {
  await fs.mkdir(path.dirname(filePath), { recursive: true });
  const tmp = `${filePath}.tmp.${process.pid}.${Date.now()}`;
  await fs.writeFile(tmp, `${JSON.stringify(data, null, 2)}\n`, "utf8");
  await fs.rename(tmp, filePath);
}

function parseOutcomePrices(raw) {
  if (Array.isArray(raw)) return raw.map((x) => toNum(x, NaN)).filter(Number.isFinite);
  if (typeof raw === "string") {
    try {
      const parsed = JSON.parse(raw);
      if (Array.isArray(parsed)) {
        return parsed.map((x) => toNum(x, NaN)).filter(Number.isFinite);
      }
    } catch {
      // ignore
    }
  }
  return [];
}

function yesPrice(market) {
  const prices = parseOutcomePrices(market?.outcomePrices);
  if (prices.length === 0) return NaN;
  return prices[0];
}

function parseThreshold(question) {
  const q = String(question || "").toLowerCase();
  const matches = q.match(/\$?\s?([0-9]+(?:\.[0-9]+)?)(k|m)?/g);
  if (!matches || matches.length === 0) return null;
  // Take largest money-like figure from question
  let best = null;
  for (const m of matches) {
    const clean = m.replace(/[^0-9.km]/g, "");
    const num = toNum(clean.replace(/[km]/g, ""), NaN);
    if (!Number.isFinite(num)) continue;
    let scaled = num;
    if (clean.endsWith("k")) scaled *= 1_000;
    if (clean.endsWith("m")) scaled *= 1_000_000;
    if (best == null || scaled > best) best = scaled;
  }
  return best;
}

function parseEndDate(market) {
  const raw = market?.endDate || market?.end_date || "";
  const t = Date.parse(raw);
  return Number.isFinite(t) ? t : null;
}

function clusterKey(question) {
  const q = String(question || "").toLowerCase();
  if (!q) return null;
  if (q.includes("bitcoin") || /\bbtc\b/.test(q)) return "btc";
  if (q.includes("ethereum") || /\beth\b/.test(q)) return "eth";
  if (q.includes("fed") || q.includes("interest rate")) return "fed";
  if (q.includes("trump") && q.includes("2028")) return "trump_2028";
  if (q.includes("election")) return "elections";
  return null;
}

function findMonotonicViolations(markets) {
  // Example: for "will BTC hit X by date", higher X should not have higher probability.
  const enriched = markets
    .map((m) => ({
      id: m.id,
      question: m.question,
      threshold: parseThreshold(m.question),
      yes: yesPrice(m),
      endTs: parseEndDate(m),
      liquidity: toNum(m.liquidity, 0),
    }))
    .filter((m) => Number.isFinite(m.yes) && Number.isFinite(m.threshold));

  const out = [];
  for (let i = 0; i < enriched.length; i += 1) {
    for (let j = i + 1; j < enriched.length; j += 1) {
      const a = enriched[i];
      const b = enriched[j];
      // Compare only same-ish horizons to reduce false positives.
      if (a.endTs && b.endTs && Math.abs(a.endTs - b.endTs) > 1000 * 60 * 60 * 24 * 120) {
        continue;
      }
      if (a.threshold < b.threshold && a.yes + 0.02 < b.yes) {
        out.push({
          type: "threshold_monotonicity_violation",
          lower_threshold_market: a.question,
          higher_threshold_market: b.question,
          lower_yes: a.yes,
          higher_yes: b.yes,
          edge_proxy: toNum((b.yes - a.yes).toFixed(4)),
        });
      }
      if (b.threshold < a.threshold && b.yes + 0.02 < a.yes) {
        out.push({
          type: "threshold_monotonicity_violation",
          lower_threshold_market: b.question,
          higher_threshold_market: a.question,
          lower_yes: b.yes,
          higher_yes: a.yes,
          edge_proxy: toNum((a.yes - b.yes).toFixed(4)),
        });
      }
    }
  }
  return out;
}

async function fetchMarkets() {
  const url = `${GAMMA_API}?active=true&closed=false&limit=2000`;
  const res = await fetch(url, { method: "GET" });
  if (!res.ok) throw new Error(`gamma markets fetch failed: ${res.status}`);
  const data = await res.json();
  if (Array.isArray(data)) return data;
  if (Array.isArray(data?.markets)) return data.markets;
  return [];
}

async function main() {
  const started = Date.now();
  const markets = await fetchMarkets();

  const clusters = {};
  for (const m of markets) {
    const key = clusterKey(m?.question);
    if (!key) continue;
    if (!clusters[key]) clusters[key] = [];
    clusters[key].push(m);
  }

  const violations = [];
  for (const [key, group] of Object.entries(clusters)) {
    if (!Array.isArray(group) || group.length < 2) continue;
    const v = findMonotonicViolations(group).map((x) => ({ cluster: key, ...x }));
    violations.push(...v);
  }

  const payload = {
    timestamp: nowIso(),
    stats: {
      fetched_markets: markets.length,
      clusters: Object.fromEntries(
        Object.entries(clusters).map(([k, v]) => [k, v.length])
      ),
      violations: violations.length,
      duration_ms: Date.now() - started,
    },
    violations: violations.slice(0, 50),
  };

  await writeJsonAtomic(OUTPUT, payload);
  console.log(
    `[cross-market] markets=${payload.stats.fetched_markets} clusters=${Object.keys(clusters).length} violations=${payload.stats.violations}`
  );
}

main().catch((err) => {
  const msg = err instanceof Error ? err.message : String(err);
  console.error(`[cross-market] error: ${msg}`);
  process.exit(1);
});
