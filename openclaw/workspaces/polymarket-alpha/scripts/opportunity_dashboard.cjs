#!/usr/bin/env node
/* eslint-disable no-console */

/**
 * Opportunity dashboard — aggregates raw signal data.
 * No recommendations, no regime labels. Just data for the agent.
 */

const fs = require("node:fs/promises");
const path = require("node:path");

const DASHBOARD_FILE = "/workspace/state/opportunity_dashboard.json";

async function loadJson(filePath, fallback) {
  try { return JSON.parse(await fs.readFile(filePath, "utf8")); }
  catch { return fallback; }
}

async function main() {
  const opportunities = await loadJson("/workspace/state/opportunities.json", { opportunities: [], stats: {} });
  const external = await loadJson("/workspace/state/external_signal_snapshot.json", {});
  const balance = await loadJson("/workspace/state/balance.json", { bankroll_usd: 0 });
  const risk = await loadJson("/workspace/state/risk_params.json", {});
  const news = await loadJson("/workspace/state/news_signal_snapshot.json", {});

  const clobOpps = opportunities.opportunities || [];

  const dashboard = {
    timestamp: new Date().toISOString().replace(/\.\d{3}Z$/, "Z"),
    bankroll_usd: balance.bankroll_usd || 0,
    available_usd: balance.available_usd || 0,
    available_capacity_usd: risk.available_capacity_usd || 0,
    clob: {
      total: clobOpps.length,
      buy_both: clobOpps.filter(o => o.type === "buy_both").length,
      sell_both: clobOpps.filter(o => o.type === "sell_both").length,
      buy_all: clobOpps.filter(o => o.type === "buy_all").length,
      sell_all: clobOpps.filter(o => o.type === "sell_all").length,
      best_edge: clobOpps.length > 0 ? Math.max(...clobOpps.map(o => o.gross_edge || 0)) : 0,
      scanned: opportunities.stats?.scanned || 0,
    },
    external: {
      btc_spot_usd: external.external?.btc_spot_median_usd || 0,
      btc_prediction_markets: external.gamma?.btc_markets || 0,
      candidates: external.heuristics?.candidate_count || 0,
    },
    news: {
      candidates: news.candidates?.length || 0,
      queries: news.queries_run || 0,
    },
  };

  await fs.mkdir(path.dirname(DASHBOARD_FILE), { recursive: true });
  await fs.writeFile(DASHBOARD_FILE, JSON.stringify(dashboard, null, 2) + "\n");
  console.log("[dashboard] " + JSON.stringify(dashboard));
}

main().catch((err) => { console.error("[dashboard] error: " + err.message); process.exit(1); });
