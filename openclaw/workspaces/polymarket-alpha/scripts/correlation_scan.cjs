#!/usr/bin/env node
/* eslint-disable no-console */

/**
 * Event-level correlation scanner.
 * Detects potential exclusivity/sum inconsistencies inside multi-market events.
 */

const fs = require("node:fs/promises");
const path = require("node:path");

const EVENTS_API = "https://gamma-api.polymarket.com/events?active=true&closed=false&limit=250";
const OUTPUT = "/workspace/state/correlation_snapshot.json";

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

function likelyMutuallyExclusive(markets) {
  const qs = markets.map((m) => String(m.question || "").toLowerCase());
  // Simple heuristic: similar stem and different terminal options (e.g., candidate names).
  const stems = qs.map((q) =>
    q
      .replace(/\b(will|be|is|the|a|an|in|on|by|for|to|of)\b/g, " ")
      .replace(/\s+/g, " ")
      .trim()
      .slice(0, 80)
  );
  const uniq = new Set(stems);
  return uniq.size <= Math.max(2, Math.floor(markets.length * 0.6));
}

async function fetchEvents() {
  const res = await fetch(EVENTS_API, { method: "GET" });
  if (!res.ok) throw new Error(`gamma events fetch failed: ${res.status}`);
  const data = await res.json();
  if (Array.isArray(data)) return data;
  if (Array.isArray(data?.events)) return data.events;
  return [];
}

async function main() {
  const started = Date.now();
  const events = await fetchEvents();

  const findings = [];
  let scanned = 0;

  for (const event of events) {
    const markets = Array.isArray(event?.markets) ? event.markets : [];
    if (markets.length < 2) continue;
    scanned += 1;

    const withPrices = markets
      .map((m) => ({
        id: m.id,
        question: m.question,
        yes: yesPrice(m),
        liquidity: toNum(m.liquidity, 0),
      }))
      .filter((m) => Number.isFinite(m.yes));

    if (withPrices.length < 2) continue;
    if (!likelyMutuallyExclusive(withPrices)) continue;

    // For candidate mutually-exclusive sets, summed YES should not exceed ~1 materially.
    const top = withPrices
      .sort((a, b) => b.liquidity - a.liquidity)
      .slice(0, 6);
    const sumYes = top.reduce((acc, m) => acc + m.yes, 0);

    if (sumYes > 1.05) {
      findings.push({
        event_id: event.id,
        event_title: event.title || event.slug || "unknown",
        type: "exclusive_sum_exceeded",
        sum_yes: toNum(sumYes.toFixed(4)),
        markets: top.map((m) => ({
          question: m.question,
          yes: toNum(m.yes.toFixed(4)),
          liquidity: m.liquidity,
        })),
      });
    }
  }

  const payload = {
    timestamp: nowIso(),
    stats: {
      fetched_events: events.length,
      scanned_events: scanned,
      findings: findings.length,
      duration_ms: Date.now() - started,
    },
    findings: findings.slice(0, 30),
  };

  await writeJsonAtomic(OUTPUT, payload);
  console.log(
    `[correlation] events=${payload.stats.fetched_events} scanned=${payload.stats.scanned_events} findings=${payload.stats.findings}`
  );
}

main().catch((err) => {
  const msg = err instanceof Error ? err.message : String(err);
  console.error(`[correlation] error: ${msg}`);
  process.exit(1);
});

