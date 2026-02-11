#!/usr/bin/env node
/* eslint-disable no-console */

const fs = require("node:fs/promises");
const path = require("node:path");

// Reuse deps already installed in the OpenClaw runner image.
const { ClobClient } = require("/opt/polymarket-sync/node_modules/@polymarket/clob-client");

const HOST = process.env.POLYMARKET_HOST || "https://clob.polymarket.com";
const CHAIN_ID = Number(process.env.POLYMARKET_CHAIN_ID || 137);
const OUTPUT = process.env.POLY_OPPORTUNITIES_OUTPUT || "/workspace/state/opportunities.json";
const MARKET_LIMIT = Number(process.env.POLY_SCAN_MARKET_LIMIT || 100); // Reduced default for faster cycles
const MIN_EDGE = Number(process.env.POLY_SCAN_MIN_EDGE || 0.0025); // 0.25%
const CONCURRENCY = Number(process.env.POLY_SCAN_CONCURRENCY || 16);
const RESULTS_LIMIT = Number(process.env.POLY_SCAN_RESULTS_LIMIT || 50);
const ENABLE_MULTI_TOKEN = process.env.POLY_SCAN_MULTI_TOKEN === "1"; // Enable n-outcome scanning

function nowIso() {
  return new Date().toISOString().replace(/\.\d{3}Z$/, "Z");
}

function toNum(v, fallback = 0) {
  const n = Number(v);
  return Number.isFinite(n) ? n : fallback;
}

function round6(n) {
  return Math.round(n * 1e6) / 1e6;
}

async function withRetry(fn, retries = 3, delay = 500) {
  for (let i = 0; i < retries; i++) {
    try {
      return await fn();
    } catch (err) {
      if (i === retries - 1) throw err;
      await new Promise((r) => setTimeout(r, delay * Math.pow(2, i)));
    }
  }
}

function bestAsk(book) {
  const asks = Array.isArray(book?.asks) ? book.asks : [];
  let min = Number.POSITIVE_INFINITY;
  let size = 0;
  for (const row of asks) {
    const p = toNum(row.price, NaN);
    const s = toNum(row.size, 0);
    if (!Number.isFinite(p) || p <= 0) continue;
    if (p < min) {
      min = p;
      size = s;
    }
  }
  if (!Number.isFinite(min)) return null;
  return { price: min, size };
}

function bestBid(book) {
  const bids = Array.isArray(book?.bids) ? book.bids : [];
  let max = Number.NEGATIVE_INFINITY;
  let size = 0;
  for (const row of bids) {
    const p = toNum(row.price, NaN);
    const s = toNum(row.size, 0);
    if (!Number.isFinite(p) || p <= 0) continue;
    if (p > max) {
      max = p;
      size = s;
    }
  }
  if (!Number.isFinite(max) || max < 0) return null;
  return { price: max, size };
}

async function writeJsonAtomic(filePath, data) {
  const dir = path.dirname(filePath);
  await fs.mkdir(dir, { recursive: true });
  const tmp = `${filePath}.tmp.${process.pid}.${Date.now()}`;
  await fs.writeFile(tmp, `${JSON.stringify(data, null, 2)}\n`, "utf8");
  await fs.rename(tmp, filePath);
}

async function mapConcurrent(items, limit, fn) {
  const out = [];
  let idx = 0;

  async function worker() {
    while (idx < items.length) {
      const i = idx++;
      try {
        out[i] = await fn(items[i], i);
      } catch (err) {
        out[i] = { __error: err instanceof Error ? err.message : String(err) };
      }
    }
  }

  const workers = Array.from({ length: Math.max(1, limit) }, () => worker());
  await Promise.all(workers);
  return out;
}

async function main() {
  const startTime = Date.now();
  const clob = new ClobClient(HOST, CHAIN_ID);
  const sampled = await clob.getSamplingSimplifiedMarkets();
  const all = Array.isArray(sampled?.data) ? sampled.data : [];

  // Separate binary and multi-token markets
  // Sort by liquidity (highest first) to focus on tradeable markets
  const binaryMarkets = all
    .filter(
      (m) =>
        m &&
        m.active === true &&
        m.closed !== true &&
        m.archived !== true &&
        m.accepting_orders === true &&
        Array.isArray(m.tokens) &&
        m.tokens.length === 2
    )
    .sort((a, b) => toNum(b.liquidity) - toNum(a.liquidity));

  const multiTokenMarkets = ENABLE_MULTI_TOKEN
    ? all.filter(
        (m) =>
          m &&
          m.active === true &&
          m.closed !== true &&
          m.archived !== true &&
          m.accepting_orders === true &&
          Array.isArray(m.tokens) &&
          m.tokens.length > 2 &&
          m.tokens.length <= 6 // Limit to prevent excessive API calls
      )
    : [];

  const candidates = binaryMarkets.slice(0, MARKET_LIMIT);

  let errorCount = 0;
  const scanErrors = [];
  let emptyBookCount = 0;
  let bookFetchSuccessCount = 0;
  
  const scanned = await mapConcurrent(candidates, CONCURRENCY, async (m) => {
    const [a, b] = m.tokens;
    let bookA, bookB;
    
    try {
      [bookA, bookB] = await Promise.all([
        withRetry(() => clob.getOrderBook(a.token_id), 2, 250),
        withRetry(() => clob.getOrderBook(b.token_id), 2, 250),
      ]);
      bookFetchSuccessCount++;
    } catch (err) {
      errorCount++;
      if (scanErrors.length < 5) {
        scanErrors.push(`${m.condition_id}: ${err.message}`);
      }
      return { __error: err.message, condition_id: m.condition_id };
    }

    const hasAsksA = Array.isArray(bookA?.asks) && bookA.asks.length > 0;
    const hasAsksB = Array.isArray(bookB?.asks) && bookB.asks.length > 0;

    if (!hasAsksA || !hasAsksB) {
      emptyBookCount++;
      return null;
    }

    const askA = bestAsk(bookA);
    const askB = bestAsk(bookB);
    const bidA = bestBid(bookA);
    const bidB = bestBid(bookB);

    const opportunities = [];

    // ASK-side: buy both for < $1
    if (askA && askB) {
      const basketCost = askA.price + askB.price;
      const edge = 1 - basketCost;
      const maxShares = Math.max(0, Math.min(askA.size, askB.size));
      if (edge >= MIN_EDGE && maxShares > 0) {
        opportunities.push({
          condition_id: m.condition_id,
          type: "buy_both",
          token_a: {
            outcome: a.outcome,
            token_id: a.token_id,
            best_ask: round6(askA.price),
            ask_size: round6(askA.size),
          },
          token_b: {
            outcome: b.outcome,
            token_id: b.token_id,
            best_ask: round6(askB.price),
            ask_size: round6(askB.size),
          },
          basket_cost: round6(basketCost),
          gross_edge: round6(edge),
          max_shares_at_best: round6(maxShares),
          accepting_orders: m.accepting_orders === true,
        });
      }
    }

    // BID-side: sell both for > $1
    if (bidA && bidB) {
      const basketProceeds = bidA.price + bidB.price;
      const edge = basketProceeds - 1;
      const maxShares = Math.max(0, Math.min(bidA.size, bidB.size));
      if (edge >= MIN_EDGE && maxShares > 0) {
        opportunities.push({
          condition_id: m.condition_id,
          type: "sell_both",
          token_a: {
            outcome: a.outcome,
            token_id: a.token_id,
            best_bid: round6(bidA.price),
            bid_size: round6(bidA.size),
          },
          token_b: {
            outcome: b.outcome,
            token_id: b.token_id,
            best_bid: round6(bidB.price),
            bid_size: round6(bidB.size),
          },
          basket_proceeds: round6(basketProceeds),
          gross_edge: round6(edge),
          max_shares_at_best: round6(maxShares),
          accepting_orders: m.accepting_orders === true,
        });
      }
    }

    return opportunities.length > 0 ? opportunities : null;
  });

  // Scan multi-token markets for n-outcome arbitrage
  const multiScanned = ENABLE_MULTI_TOKEN
    ? await mapConcurrent(multiTokenMarkets.slice(0, 50), CONCURRENCY, async (m) => {
        const tokens = m.tokens;
        const books = await Promise.all(
          tokens.map((t) => clob.getOrderBook(t.token_id))
        );

        const asks = books.map((b) => bestAsk(b));
        const bids = books.map((b) => bestBid(b));

        const opportunities = [];

        // Check if sum of all asks < 1 (buy all outcomes for guaranteed profit)
        const validAsks = asks.filter((a) => a !== null);
        if (validAsks.length === tokens.length) {
          const basketCost = validAsks.reduce((sum, a) => sum + a.price, 0);
          const edge = 1 - basketCost;
          const maxShares = Math.max(
            0,
            Math.min(...validAsks.map((a) => a.size))
          );
          if (edge >= MIN_EDGE && maxShares > 0) {
            opportunities.push({
              condition_id: m.condition_id,
              type: "buy_all",
              token_count: tokens.length,
              basket_cost: round6(basketCost),
              gross_edge: round6(edge),
              max_shares_at_best: round6(maxShares),
              accepting_orders: m.accepting_orders === true,
            });
          }
        }

        // Check if sum of all bids > 1 (sell all outcomes for guaranteed profit)
        const validBids = bids.filter((b) => b !== null);
        if (validBids.length === tokens.length) {
          const basketProceeds = validBids.reduce((sum, b) => sum + b.price, 0);
          const edge = basketProceeds - 1;
          const maxShares = Math.max(
            0,
            Math.min(...validBids.map((b) => b.size))
          );
          if (edge >= MIN_EDGE && maxShares > 0) {
            opportunities.push({
              condition_id: m.condition_id,
              type: "sell_all",
              token_count: tokens.length,
              basket_proceeds: round6(basketProceeds),
              gross_edge: round6(edge),
              max_shares_at_best: round6(maxShares),
              accepting_orders: m.accepting_orders === true,
            });
          }
        }

        return opportunities.length > 0 ? opportunities : null;
      })
    : [];

  // Flatten nested opportunity arrays (binary + multi)
  const allOpps = [...scanned, ...multiScanned]
    .filter((x) => x && !x.__error)
    .flat()
    .filter((x) => x && x.gross_edge >= MIN_EDGE && x.max_shares_at_best > 0)
    .sort((x, y) => y.gross_edge - x.gross_edge)
    .slice(0, RESULTS_LIMIT);

  const durationMs = Date.now() - startTime;
  
  const payload = {
    timestamp: nowIso(),
    source: {
      provider: "polymarket-clob",
      host: HOST,
      chain_id: CHAIN_ID,
      scan_market_limit: MARKET_LIMIT,
      min_edge: MIN_EDGE,
      concurrency: CONCURRENCY,
    },
    stats: {
      sampled: all.length,
      scanned: candidates.length,
      multi_token_scanned: multiTokenMarkets.length,
      valid_books: bookFetchSuccessCount,
      empty_books: emptyBookCount,
      errors: errorCount,
      sample_errors: scanErrors.slice(0, 3),
      opportunities: allOpps.length,
      duration_ms: durationMs,
    },
    opportunities: allOpps,
  };

  await writeJsonAtomic(OUTPUT, payload);
  console.log(
    `[clob-scan] scanned=${payload.stats.scanned} in ${durationMs}ms valid=${payload.stats.valid_books} opportunities=${payload.stats.opportunities}`
  );
  if (payload.stats.sample_errors.length > 0) {
    console.log(`[clob-scan] sample errors:`, payload.stats.sample_errors);
  }
}

main().catch((err) => {
  const msg = err instanceof Error ? err.message : String(err);
  console.error(`[clob-scan] error: ${msg}`);
  process.exitCode = 1;
});
