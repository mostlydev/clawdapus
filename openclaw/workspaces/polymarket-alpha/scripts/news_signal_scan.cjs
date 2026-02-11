#!/usr/bin/env node
/* eslint-disable no-console */

/**
 * News/catalyst discovery lane.
 * Uses Brave Search + live Polymarket topics to detect potential repricing catalysts.
 */

const fs = require("node:fs/promises");
const path = require("node:path");

const OUTPUT = "/workspace/state/news_signal_snapshot.json";
const BRAVE_ENDPOINT = "https://api.search.brave.com/res/v1/web/search";
const GAMMA_ENDPOINT = "https://gamma-api.polymarket.com/markets";

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

async function fetchJson(url, opts = {}) {
  const timeoutMs = opts.timeoutMs || 10000;
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  try {
    const res = await fetch(url, {
      method: "GET",
      headers: opts.headers || {},
      signal: controller.signal,
    });
    if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
    return await res.json();
  } finally {
    clearTimeout(timer);
  }
}

function tokenize(text) {
  return String(text || "")
    .toLowerCase()
    .replace(/[^a-z0-9\s]/g, " ")
    .split(/\s+/)
    .filter((w) => w.length >= 3);
}

function unique(arr) {
  return [...new Set(arr)];
}

async function loadTopMarketTopics(limit = 8) {
  const url = `${GAMMA_ENDPOINT}?active=true&closed=false&limit=500`;
  const data = await fetchJson(url, { timeoutMs: 12000 });
  const markets = Array.isArray(data) ? data : (Array.isArray(data?.markets) ? data.markets : []);

  const ranked = markets
    .map((m) => ({
      question: m?.question || "",
      volume24h: toNum(m?.volume24hr, 0),
      liquidity: toNum(m?.liquidity, 0),
    }))
    .filter((m) => m.question)
    .sort((a, b) => (b.volume24h + b.liquidity) - (a.volume24h + a.liquidity))
    .slice(0, Math.max(limit, 1));

  const topicWords = [];
  for (const m of ranked) {
    topicWords.push(...tokenize(m.question));
  }

  const blocked = new Set([
    "the", "will", "what", "when", "where", "which", "before", "after", "have", "has",
    "been", "into", "with", "that", "this", "from", "market", "polymarket", "2028",
    "win", "wins", "presidential", "democratic", "nomination", "election", "party",
  ]);
  const topicCounts = {};
  for (const w of topicWords) {
    if (blocked.has(w)) continue;
    topicCounts[w] = (topicCounts[w] || 0) + 1;
  }
  const topWords = Object.entries(topicCounts)
    .sort((a, b) => b[1] - a[1])
    .slice(0, 16)
    .map(([w]) => w);

  return {
    sampled_markets: ranked.length,
    top_market_questions: ranked.map((r) => r.question),
    topic_keywords: topWords,
  };
}

async function braveSearch(apiKey, query, count = 8) {
  const url = `${BRAVE_ENDPOINT}?q=${encodeURIComponent(query)}&count=${count}&result_filter=web`;
  const data = await fetchJson(url, {
    timeoutMs: 10000,
    headers: {
      "Accept": "application/json",
      "X-Subscription-Token": apiKey,
    },
  });
  return Array.isArray(data?.web?.results) ? data.web.results : [];
}

function scoreResult(result, topicKeywords) {
  const text = `${result?.title || ""} ${result?.description || ""}`.toLowerCase();
  const matched = topicKeywords.filter((k) => text.includes(k));

  // Urgency proxy terms often tied to rapid repricing
  const urgencyTerms = [
    "breaking", "lawsuit", "emergency", "attack", "missile", "hack",
    "sec", "fed", "rate", "approval", "ban", "shutdown", "resign",
  ];
  const urgencyHits = urgencyTerms.filter((k) => text.includes(k)).length;

  return {
    matched_keywords: matched,
    urgency_hits: urgencyHits,
    score: matched.length * 1.2 + urgencyHits * 1.8,
  };
}

async function main() {
  const braveKey = process.env.BRAVE_API_KEY || "";
  if (!braveKey) {
    const payload = {
      timestamp: nowIso(),
      enabled: false,
      reason: "missing BRAVE_API_KEY",
      candidates: [],
    };
    await writeJsonAtomic(OUTPUT, payload);
    console.log("[news-scan] skipped: missing BRAVE_API_KEY");
    return;
  }

  const marketTopics = await loadTopMarketTopics(10);
  const baseQueries = [
    "breaking prediction market relevant macro events today",
    "crypto regulation SEC ETF breaking",
    "US election campaign odds breaking news",
  ];

  const keywordQuery = marketTopics.topic_keywords.slice(0, 6).join(" ");
  const queries = unique([
    ...baseQueries,
    keywordQuery ? `latest news ${keywordQuery}` : "",
  ].filter(Boolean));

  const findings = [];
  for (const q of queries.slice(0, 4)) {
    let results = [];
    try {
      results = await braveSearch(braveKey, q, 8);
    } catch (err) {
      findings.push({
        query: q,
        error: err instanceof Error ? err.message : String(err),
        results: [],
      });
      continue;
    }

    const scored = results.map((r) => {
      const s = scoreResult(r, marketTopics.topic_keywords);
      return {
        title: r?.title || "",
        url: r?.url || "",
        age: r?.age || null,
        description: r?.description || "",
        ...s,
      };
    });

    findings.push({
      query: q,
      result_count: scored.length,
      top_results: scored
        .sort((a, b) => b.score - a.score)
        .slice(0, 6),
    });
  }

  const allCandidates = findings
    .flatMap((f) => f.top_results || [])
    .filter((r) => r.score >= 2.4)
    .sort((a, b) => b.score - a.score)
    .slice(0, 12);

  const payload = {
    timestamp: nowIso(),
    enabled: true,
    source: {
      provider: "brave-search",
      queries_run: queries.length,
    },
    market_topics: marketTopics,
    findings,
    candidates: allCandidates,
    candidate_count: allCandidates.length,
  };

  await writeJsonAtomic(OUTPUT, payload);
  console.log(
    `[news-scan] queries=${queries.length} candidates=${payload.candidate_count} output=${OUTPUT}`
  );
}

main().catch((err) => {
  const msg = err instanceof Error ? err.message : String(err);
  console.error(`[news-scan] error: ${msg}`);
  process.exit(1);
});
