#!/usr/bin/env node
/* eslint-disable no-console */

/**
 * Execution Quality & Risk Controls
 * Calculates optimal order sizes, estimates slippage, manages exposure
 * Strategy lane 4: Execution quality and risk controls
 */

const fs = require("node:fs/promises");
const path = require("node:path");

const POSITIONS_FILE = process.env.POLY_POSITIONS_FILE || "/workspace/state/positions.json";
const BALANCE_FILE = process.env.POLY_BALANCE_FILE || "/workspace/state/balance.json";

// Risk parameters
const MAX_POSITION_PCT = 0.15; // 15% max per position
const MIN_EDGE_FOR_KELLY = 0.02; // 2% minimum edge
const KELLY_FRACTION = 0.25; // Quarter Kelly for safety
const MIN_ORDER_SIZE = 5; // $5 minimum
const MAX_ORDER_SIZE = 50; // $50 maximum

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

// Calculate optimal position size using Kelly Criterion
function calculateKellySize(bankroll, edge, odds = 1.0) {
  // Kelly fraction: (bp - q) / b
  // where b = odds, p = win probability, q = lose probability
  // Simplified: edge / odds
  
  if (edge < MIN_EDGE_FOR_KELLY) return 0;
  
  const kellyFull = edge / odds;
  const kellySafe = kellyFull * KELLY_FRACTION;
  const maxByPct = bankroll * MAX_POSITION_PCT;
  
  // Take minimum of Kelly, max position %, and absolute max
  let size = Math.min(
    bankroll * kellySafe,
    maxByPct,
    MAX_ORDER_SIZE
  );
  
  // Ensure minimum size
  if (size < MIN_ORDER_SIZE) return 0;
  
  return round4(size);
}

// Estimate slippage based on order size and book depth
function estimateSlippage(orderSize, bestPrice, bookDepth) {
  if (!bookDepth || bookDepth.length === 0) return { slippage: 0, filled: false };
  
  let remaining = orderSize;
  let totalCost = 0;
  let totalShares = 0;
  let weightedPrice = 0;
  
  for (const level of bookDepth) {
    const price = parseFloat(level.price);
    const size = parseFloat(level.size);
    
    const take = Math.min(remaining, size);
    totalCost += take * price;
    totalShares += take;
    remaining -= take;
    
    if (remaining <= 0) break;
  }
  
  if (totalShares === 0) return { slippage: 0, filled: false };
  
  weightedPrice = totalCost / totalShares;
  const slippage = (weightedPrice - bestPrice) / bestPrice;
  const filled = remaining <= 0;
  
  return {
    slippage: round4(slippage),
    filled,
    avgPrice: round4(weightedPrice),
    sharesFilled: round4(totalShares),
  };
}

// Check if total exposure within limits
async function checkExposure(currentSize = 0) {
  const positions = await loadJson(POSITIONS_FILE, { positions: { open_value_usd: 0 } });
  const balance = await loadJson(BALANCE_FILE, { bankroll_usd: 0 });
  
  const currentExposure = positions.positions?.open_value_usd || 0;
  const bankroll = balance.bankroll_usd || 0;
  
  const newExposure = currentExposure + currentSize;
  const exposurePct = newExposure / bankroll;
  
  return {
    withinLimits: exposurePct <= MAX_POSITION_PCT * 2, // Allow 2 positions at max
    currentExposure: round4(currentExposure),
    newExposure: round4(newExposure),
    exposurePct: round4(exposurePct),
    bankroll: round4(bankroll),
  };
}

async function main() {
  console.log("[risk] Execution quality check...");
  
  // Load current state
  const balance = await loadJson(BALANCE_FILE, { bankroll_usd: 99.29 });
  const bankroll = balance.bankroll_usd || 99.29;
  
  console.log(`[risk] Bankroll: $${bankroll}`);
  
  // Example Kelly calculations for various edges
  const testEdges = [0.01, 0.02, 0.05, 0.10];
  console.log("[risk] Kelly position sizing:");
  for (const edge of testEdges) {
    const size = calculateKellySize(bankroll, edge);
    const pct = ((size / bankroll) * 100).toFixed(1);
    console.log(`  - ${(edge * 100).toFixed(0)}% edge → $${size} (${pct}% of bankroll)`);
  }
  
  // Check current exposure
  const exposure = await checkExposure(0);
  console.log(`[risk] Current exposure: $${exposure.currentExposure} (${(exposure.exposurePct * 100).toFixed(1)}%)`);
  
  // Example slippage calculation
  const exampleBook = [
    { price: "0.45", size: "100" },
    { price: "0.46", size: "200" },
    { price: "0.47", size: "500" },
  ];
  
  const testSizes = [10, 25, 50, 100];
  console.log("[risk] Slippage estimates (example book at 0.45):");
  for (const size of testSizes) {
    const est = estimateSlippage(size, 0.45, exampleBook);
    const status = est.filled ? "filled" : "partial";
    console.log(`  - $${size} order → avg ${est.avgPrice} (${(est.slippage * 100).toFixed(2)}% slippage, ${status})`);
  }
  
  // Write risk parameters to state for other modules
  const riskParams = {
    timestamp: new Date().toISOString().replace(/\.\d{3}Z$/, "Z"),
    bankroll_usd: bankroll,
    max_position_pct: MAX_POSITION_PCT,
    kelly_fraction: KELLY_FRACTION,
    min_order_size: MIN_ORDER_SIZE,
    max_order_size: MAX_ORDER_SIZE,
    current_exposure_usd: exposure.currentExposure,
    available_capacity_usd: round4(bankroll * MAX_POSITION_PCT * 2 - exposure.currentExposure),
  };
  
  const riskFile = "/workspace/state/risk_params.json";
  await fs.mkdir(path.dirname(riskFile), { recursive: true });
  await fs.writeFile(riskFile, JSON.stringify(riskParams, null, 2) + "\n");
  
  console.log(`[risk] Risk parameters saved to ${riskFile}`);
  console.log(`[risk] Available capacity: $${riskParams.available_capacity_usd}`);
}

main().catch((err) => {
  console.error(`[risk] error: ${err.message}`);
  process.exit(1);
});
