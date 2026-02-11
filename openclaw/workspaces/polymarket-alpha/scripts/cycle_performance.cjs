#!/usr/bin/env node
/* eslint-disable no-console */

/**
 * Cycle performance — records this cycle snapshot only (no time series).
 * The agent owns analysis and strategy; this script just captures data.
 */

const fs = require("node:fs/promises");
const path = require("node:path");

const PERFORMANCE_FILE = "/workspace/state/cycle_performance.json";

async function loadJson(filePath, fallback) {
  try { return JSON.parse(await fs.readFile(filePath, "utf8")); }
  catch { return fallback; }
}

async function main() {
  const opp = await loadJson("/workspace/state/opportunities.json", { stats: {} });
  const balance = await loadJson("/workspace/state/balance.json", { bankroll_usd: 0 });

  const snapshot = {
    timestamp: new Date().toISOString().replace(/\.\d{3}Z$/, "Z"),
    bankroll_usd: balance.bankroll_usd || 0,
    available_usd: balance.available_usd || 0,
    opportunities: opp.stats?.opportunities || 0,
    scanned: opp.stats?.scanned || 0,
    duration_ms: opp.stats?.duration_ms || 0,
    empty_books: opp.stats?.empty_books || 0,
    errors: opp.stats?.errors || 0,
  };

  await fs.mkdir(path.dirname(PERFORMANCE_FILE), { recursive: true });
  await fs.writeFile(PERFORMANCE_FILE, JSON.stringify(snapshot, null, 2) + "\n");
  console.log("[performance] " + JSON.stringify(snapshot));
}

main().catch((err) => { console.error("[performance] error: " + err.message); process.exit(1); });
