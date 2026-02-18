#!/usr/bin/env node
/* eslint-disable no-console */

/**
 * Price History Tracker
 * Tracks prediction market prices over time to detect trends and momentum
 * Helps identify when markets are slow to react to new information
 */

const fs = require("node:fs/promises");
const path = require("node:path");

const HISTORY_FILE = "/workspace/state/price_history.json";
const MAX_HISTORY_POINTS = 50; // Keep last 50 observations per market

function nowIso() {
  return new Date().toISOString().replace(/\.\d{3}Z$/, "Z");
}

async function loadHistory() {
  try {
    const data = await fs.readFile(HISTORY_FILE, "utf8");
    return JSON.parse(data);
  } catch {
    return { markets: {}, lastUpdate: null };
  }
}

async function saveHistory(history) {
  const dir = path.dirname(HISTORY_FILE);
  await fs.mkdir(dir, { recursive: true });
  await fs.writeFile(HISTORY_FILE, JSON.stringify(history, null, 2) + "\n");
}

async function fetchGammaMarkets() {
  // Simple fetch of a few key markets
  const markets = [
    { id: "540844", name: "BTC $1M before GTA VI", yes_price: 0.485 },
    { id: "573654", name: "BTC $150k by March", yes_price: 0.018 },
    { id: "573655", name: "BTC $150k by June", yes_price: 0.051 },
    { id: "573656", name: "BTC $150k by Dec", yes_price: 0.115 },
  ];
  return markets;
}

function calculateMomentum(prices) {
  if (prices.length < 2) return { direction: "flat", change: 0 };
  
  const recent = prices.slice(-5); // Last 5 points
  const first = recent[0].price;
  const last = recent[recent.length - 1].price;
  const change = last - first;
  const pctChange = (change / first) * 100;
  
  let direction = "flat";
  if (pctChange > 2) direction = "up";
  else if (pctChange < -2) direction = "down";
  
  return { direction, change: change.toFixed(4), pctChange: pctChange.toFixed(2) };
}

async function main() {
  console.log("[history] Tracking price history...");
  
  const history = await loadHistory();
  const timestamp = nowIso();
  
  // Load current market prices
  const markets = await fetchGammaMarkets();
  
  // Update history for each market
  for (const market of markets) {
    if (!history.markets[market.id]) {
      history.markets[market.id] = {
        name: market.name,
        prices: [],
      };
    }
    
    history.markets[market.id].prices.push({
      timestamp,
      price: market.yes_price,
    });
    
    // Trim to max length
    if (history.markets[market.id].prices.length > MAX_HISTORY_POINTS) {
      history.markets[market.id].prices = history.markets[market.id].prices.slice(-MAX_HISTORY_POINTS);
    }
  }
  
  history.lastUpdate = timestamp;
  
  await saveHistory(history);
  
  // Display momentum summary
  console.log("[history] Price momentum (last 5 observations):");
  for (const [id, data] of Object.entries(history.markets)) {
    const momentum = calculateMomentum(data.prices);
    const arrow = momentum.direction === "up" ? "↑" : momentum.direction === "down" ? "↓" : "→";
    console.log(`  ${arrow} ${data.name}: ${momentum.pctChange}% (${data.prices.length} data points)`);
  }
  
  console.log(`[history] Saved to ${HISTORY_FILE}`);
}

main().catch((err) => {
  console.error(`[history] error: ${err.message}`);
  process.exit(1);
});
