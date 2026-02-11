#!/usr/bin/env node
/* eslint-disable no-console */

/**
 * Survival Mode Configuration
 * Reduces operational costs when no opportunities detected for extended periods
 * Extends runway by reducing scan frequency
 */

const fs = require("node:fs/promises");
const path = require("node:path");

const CONFIG_FILE = "/workspace/state/survival_config.json";

// Burn rate estimates (USD per cycle at different frequencies)
const BURN_RATES = {
  aggressive: 0.08,   // 1-minute cycles
  normal: 0.05,       // 2-minute cycles (current)
  conservative: 0.03, // 5-minute cycles
  survival: 0.01,     // 15-minute cycles
  hibernation: 0.005, // 60-minute cycles
};

const CYCLE_INTERVALS = {
  aggressive: 1,
  normal: 2,
  conservative: 5,
  survival: 15,
  hibernation: 60,
};

async function loadConfig() {
  try {
    const data = await fs.readFile(CONFIG_FILE, "utf8");
    return JSON.parse(data);
  } catch {
    return {
      mode: "normal",
      cycles_without_opportunity: 0,
      last_opportunity_at: null,
      estimated_runway_hours: 24,
    };
  }
}

async function saveConfig(config) {
  const dir = path.dirname(CONFIG_FILE);
  await fs.mkdir(dir, { recursive: true });
  await fs.writeFile(CONFIG_FILE, JSON.stringify(config, null, 2) + "\n");
}

async function loadPerformance() {
  try {
    const data = await fs.readFile("/workspace/state/cycle_performance.json", "utf8");
    return JSON.parse(data);
  } catch {
    return { summary: { current_streak_no_opp: 0 } };
  }
}

async function loadBalance() {
  try {
    const data = await fs.readFile("/workspace/state/balance.json", "utf8");
    return JSON.parse(data);
  } catch {
    return { bankroll_usd: 99.29 };
  }
}

function calculateRunway(bankroll, mode) {
  const burnPerCycle = BURN_RATES[mode] || BURN_RATES.normal;
  const intervalMinutes = CYCLE_INTERVALS[mode] || CYCLE_INTERVALS.normal;
  const cyclesPerHour = 60 / intervalMinutes;
  const burnPerHour = burnPerCycle * cyclesPerHour;
  return bankroll / burnPerHour;
}

function recommendMode(streak, runway) {
  if (streak > 100 || runway < 6) return "hibernation";
  if (streak > 60 || runway < 12) return "survival";
  if (streak > 40 || runway < 24) return "conservative";
  if (streak > 20) return "normal";
  return "aggressive";
}

console.log(`[survival] Recommended mode based on streak ${streak}: ${recommended}`);

async function main() {
  console.log("[survival] Checking survival mode configuration...");
  
  const config = await loadConfig();
  const perf = await loadPerformance();
  const balance = await loadBalance();
  
  const bankroll = balance.bankroll_usd || 99.29;
  const streak = perf.summary?.current_streak_no_opp || 0;
  
  // Update config with current state
  config.cycles_without_opportunity = streak;
  if (streak === 0) {
    config.last_opportunity_at = new Date().toISOString();
  }
  
  // Calculate runway for each mode
  const runways = {};
  for (const [mode, rate] of Object.entries(BURN_RATES)) {
    runways[mode] = calculateRunway(bankroll, mode);
  }
  
  // Recommend mode based on streak and runway
  const recommended = recommendMode(streak, runways[config.mode]);
  
  console.log("[survival] === Survival Analysis ===");
  console.log(`[survival] Current bankroll: $${bankroll.toFixed(2)}`);
  console.log(`[survival] Cycles without opportunity: ${streak}`);
  console.log(`[survival] Current mode: ${config.mode}`);
  console.log(`[survival] Estimated runway at current mode: ${runways[config.mode].toFixed(1)} hours`);
  
  console.log("[survival] Runway by mode:");
  for (const [mode, hours] of Object.entries(runways)) {
    const marker = mode === config.mode ? "← current" : "";
    const rec = mode === recommended ? " [RECOMMENDED]" : "";
    console.log(`  - ${mode}: ${hours.toFixed(1)}h (${CYCLE_INTERVALS[mode]}min cycles)${marker}${rec}`);
  }
  
  if (recommended !== config.mode) {
    console.log(`[survival] ⚠️  RECOMMENDATION: Switch to ${recommended} mode`);
    console.log(`[survival]    This extends runway from ${runways[config.mode].toFixed(1)}h to ${runways[recommended].toFixed(1)}h`);
  } else {
    console.log(`[survival] ✓ Current mode (${config.mode}) is optimal`);
  }
  
  config.estimated_runway_hours = runways[config.mode];
  config.recommended_mode = recommended;
  
  await saveConfig(config);
  console.log(`[survival] Config saved to ${CONFIG_FILE}`);
}

main().catch((err) => {
  console.error(`[survival] error: ${err.message}`);
  process.exit(1);
});
