#!/usr/bin/env node
/* eslint-disable no-console */

/**
 * Adaptive cycle orchestrator.
 * - Rotates strategy lanes (no single-lane lock-in)
 * - Mutates scan/execute thresholds after no-op streaks
 * - Guarantees concrete actions each cycle
 */

const fs = require("node:fs/promises");
const path = require("node:path");
const { execSync } = require("node:child_process");

const SCRIPTS_DIR = "/workspace/scripts";
const STATE_DIR = "/workspace/state";
const STRATEGY_STATE_FILE = `${STATE_DIR}/strategy_state.json`;
const PERFORMANCE_FILE = `${STATE_DIR}/cycle_performance.json`;
const OPPORTUNITIES_FILE = `${STATE_DIR}/opportunities.json`;

const LANES = [
  "orderbook_arb",
  "cross_market",
  "external_reality",
  "news_discovery",
  "execution_quality",
];

function nowIso() {
  return new Date().toISOString().replace(/\.\d{3}Z$/, "Z");
}

function toNum(v, fallback = 0) {
  const n = Number(v);
  return Number.isFinite(n) ? n : fallback;
}

function clamp(v, min, max) {
  return Math.max(min, Math.min(max, v));
}

async function readJson(filePath, fallback) {
  try {
    const raw = await fs.readFile(filePath, "utf8");
    return JSON.parse(raw);
  } catch {
    return fallback;
  }
}

async function writeJson(filePath, data) {
  await fs.mkdir(path.dirname(filePath), { recursive: true });
  await fs.writeFile(filePath, `${JSON.stringify(data, null, 2)}\n`);
}

function runScript(name, timeoutSec = 60, envOverrides = {}) {
  const scriptPath = path.join(SCRIPTS_DIR, name);
  const started = Date.now();
  try {
    const output = execSync(`node ${scriptPath}`, {
      timeout: timeoutSec * 1000,
      encoding: "utf8",
      stdio: ["pipe", "pipe", "pipe"],
      env: {
        ...process.env,
        ...envOverrides,
      },
    });
    return {
      script: name,
      success: true,
      duration_ms: Date.now() - started,
      output: output.trim(),
    };
  } catch (err) {
    return {
      script: name,
      success: false,
      duration_ms: Date.now() - started,
      error: err.message,
      output: err.stdout?.toString()?.trim() || "",
    };
  }
}

function deriveNoOppStreak(perf) {
  const cycles = Array.isArray(perf?.cycles) ? perf.cycles : [];
  if (cycles.length === 0) return 0;
  let streak = 0;
  for (let i = cycles.length - 1; i >= 0; i -= 1) {
    const c = cycles[i];
    const opps = toNum(c?.opportunities, 0);
    if (opps > 0) break;
    streak += 1;
  }
  return streak;
}

function pickLane(prevState, noOppStreak) {
  const lastLane = prevState?.last_lane || null;
  const laneStreak = toNum(prevState?.lane_streak, 0);
  const cycleCount = toNum(prevState?.cycle_count, 0);

  let preferred = null;
  if (noOppStreak >= 25) preferred = "news_discovery";
  else if (noOppStreak >= 15) preferred = "cross_market";
  else if (noOppStreak >= 8) preferred = "external_reality";

  if (preferred && !(lastLane === preferred && laneStreak >= 2)) {
    return preferred;
  }

  if (lastLane && laneStreak >= 2) {
    const idx = LANES.indexOf(lastLane);
    return LANES[(idx + 1) % LANES.length];
  }

  return LANES[cycleCount % LANES.length];
}

function mutateParams(noOppStreak) {
  const baseScanEdge = toNum(process.env.POLY_SCAN_MIN_EDGE, 0.0025);
  const baseExecEdge = toNum(process.env.POLY_MIN_EDGE_EXECUTE, 0.005);
  const baseScanLimit = toNum(process.env.POLY_SCAN_MARKET_LIMIT, 250);
  const basePositionUsd = toNum(process.env.POLY_MAX_POSITION_USD, 5);

  let scanEdge = baseScanEdge;
  let execEdge = baseExecEdge;
  let scanLimit = baseScanLimit;
  let maxPositionUsd = basePositionUsd;

  if (noOppStreak >= 8) {
    scanLimit = clamp(Math.round(baseScanLimit * 1.2), 150, 400);
    scanEdge = Math.min(scanEdge, 0.002);
    execEdge = Math.min(execEdge, 0.0045);
    maxPositionUsd = clamp(basePositionUsd + 1, 4, 15);
  }
  if (noOppStreak >= 15) {
    scanLimit = clamp(Math.round(baseScanLimit * 1.4), 200, 600);
    scanEdge = Math.min(scanEdge, 0.0015);
    execEdge = Math.min(execEdge, 0.0035);
    maxPositionUsd = clamp(basePositionUsd + 2, 5, 18);
  }
  if (noOppStreak >= 25) {
    scanLimit = clamp(Math.round(baseScanLimit * 1.6), 250, 800);
    scanEdge = Math.min(scanEdge, 0.001);
    execEdge = Math.min(execEdge, 0.0025);
    maxPositionUsd = clamp(basePositionUsd + 4, 6, 24);
  }

  return {
    scan_edge: scanEdge,
    execute_edge: execEdge,
    scan_limit: scanLimit,
    max_position_usd: maxPositionUsd,
  };
}

function summarizeResult(result) {
  if (!result) return "";
  if (result.success) {
    return `${result.script}:ok:${result.duration_ms}ms`;
  }
  return `${result.script}:err:${result.duration_ms}ms`;
}

async function main() {
  const start = Date.now();
  const prevState = await readJson(STRATEGY_STATE_FILE, {});
  const perf = await readJson(PERFORMANCE_FILE, { cycles: [] });
  const noOppStreak = deriveNoOppStreak(perf);
  const lane = pickLane(prevState, noOppStreak);
  const params = mutateParams(noOppStreak);

  console.log("[cycle] === Adaptive Trading Cycle Start ===");
  console.log(`[cycle] lane=${lane} no_opp_streak=${noOppStreak}`);
  console.log(
    `[cycle] params scan_limit=${params.scan_limit} scan_edge=${params.scan_edge} exec_edge=${params.execute_edge} max_pos_usd=${params.max_position_usd}`
  );

  const commonEnv = {
    POLY_SCAN_MARKET_LIMIT: String(params.scan_limit),
    POLY_SCAN_MIN_EDGE: String(params.scan_edge),
    POLY_MIN_EDGE_EXECUTE: String(params.execute_edge),
    POLY_MAX_POSITION_USD: String(params.max_position_usd),
  };

  const actions = [];

  // Mandatory baseline actions
  console.log("[cycle] 1/7 Baseline CLOB scan...");
  actions.push(runScript("clob_scan_opportunities.cjs", 70, commonEnv));

  console.log("[cycle] 2/7 Baseline external signal scan...");
  actions.push(runScript("external_signal_scan.cjs", 70));

  // Lane-specific exploration actions
  console.log(`[cycle] 3/7 Lane execution: ${lane}`);
  if (lane === "orderbook_arb") {
    actions.push(
      runScript("clob_scan_opportunities.cjs", 80, {
        ...commonEnv,
        POLY_SCAN_MARKET_LIMIT: String(clamp(params.scan_limit + 100, 150, 900)),
        POLY_SCAN_MIN_EDGE: String(Math.max(0.0008, params.scan_edge * 0.9)),
      })
    );
  } else if (lane === "cross_market") {
    actions.push(runScript("cross_market_scan.cjs", 40));
    actions.push(runScript("correlation_scan.cjs", 40));
  } else if (lane === "external_reality") {
    actions.push(runScript("price_history.cjs", 30));
    actions.push(runScript("external_signal_scan.cjs", 60));
  } else if (lane === "news_discovery") {
    actions.push(runScript("news_signal_scan.cjs", 45));
    actions.push(runScript("cross_market_scan.cjs", 40));
  } else if (lane === "execution_quality") {
    actions.push(runScript("risk_controls.cjs", 20));
    actions.push(runScript("opportunity_dashboard.cjs", 20));
  }

  // Attempt execution every cycle if key exists.
  console.log("[cycle] 4/7 Execution attempt...");
  if (process.env.POLYMARKET_PRIVATE_KEY || process.env.PRIVATE_KEY) {
    actions.push(runScript("execute_opportunity.cjs", 35, commonEnv));
  } else {
    console.log("[cycle] execution skipped: no POLYMARKET_PRIVATE_KEY");
  }

  console.log("[cycle] 5/7 Position tracking...");
  actions.push(runScript("track_positions.cjs", 30));

  console.log("[cycle] 6/7 Performance tracking...");
  actions.push(runScript("cycle_performance.cjs", 25));

  console.log("[cycle] 7/7 Opportunity dashboard...");
  actions.push(runScript("opportunity_dashboard.cjs", 25));

  for (const result of actions) {
    if (result.success) {
      if (result.output) console.log(`[cycle] ${result.output}`);
      else console.log(`[cycle] ${result.script} completed`);
    } else {
      console.log(`[cycle] ${result.script} failed: ${result.error}`);
      if (result.output) console.log(`[cycle] ${result.output}`);
    }
  }

  const latestOpp = await readJson(OPPORTUNITIES_FILE, { stats: {} });
  const oppCount = toNum(latestOpp?.stats?.opportunities, 0);
  const cycleCount = toNum(prevState?.cycle_count, 0) + 1;
  const laneStreak =
    prevState?.last_lane === lane ? toNum(prevState?.lane_streak, 0) + 1 : 1;

  const nextState = {
    timestamp: nowIso(),
    cycle_count: cycleCount,
    last_lane: lane,
    lane_streak: laneStreak,
    last_no_opp_streak: noOppStreak,
    last_opportunities: oppCount,
    last_params: params,
    recent_actions: actions.slice(-8).map(summarizeResult),
    last_duration_ms: Date.now() - start,
  };

  await writeJson(STRATEGY_STATE_FILE, nextState);

  console.log(
    `[cycle] summary lane=${lane} opps=${oppCount} duration=${nextState.last_duration_ms}ms`
  );
  console.log(`[cycle] strategy state updated: ${STRATEGY_STATE_FILE}`);
  console.log("[cycle] === Adaptive Trading Cycle Complete ===");
}

main().catch((err) => {
  console.error(`[cycle] fatal: ${err.message}`);
  process.exit(1);
});
