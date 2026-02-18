// Fetch Polymarket markets and scan for arbitrage
const CLOB_HOST = "https://clob.polymarket.com";

async function fetchActiveMarkets() {
  const markets = [];
  let offset = 0;
  const limit = 100;
  
  while (true) {
    try {
      const url = `${CLOB_HOST}/markets?active=true&limit=${limit}&offset=${offset}`;
      const r = await fetch(url, { timeout: 10000 });
      if (!r.ok) break;
      const data = await r.json();
      const batch = data.data || [];
      if (batch.length === 0) break;
      markets.push(...batch);
      offset += limit;
      if (batch.length < limit) break;
    } catch (e) {
      console.error("Error:", e.message);
      break;
    }
  }
  return markets;
}

async function fetchOrderbook(tokenId) {
  try {
    const r = await fetch(`${CLOB_HOST}/book/${tokenId}`, { timeout: 5000 });
    if (r.ok) return await r.json();
  } catch (e) {}
  return null;
}

async function scanArbitrage(markets) {
  const opportunities = [];
  
  for (const market of markets.slice(0, 50)) {  // Limit scan to first 50 for speed
    const tokens = market.tokens || [];
    if (tokens.length !== 2) continue;
    
    let tokenYes = null, tokenNo = null;
    for (const t of tokens) {
      const outcome = (t.outcome || "").toUpperCase();
      if (outcome === "YES") tokenYes = t;
      if (outcome === "NO") tokenNo = t;
    }
    if (!tokenYes || !tokenNo) continue;
    
    const [bookYes, bookNo] = await Promise.all([
      fetchOrderbook(tokenYes.token_id),
      fetchOrderbook(tokenNo.token_id)
    ]);
    
    if (!bookYes || !bookNo) continue;
    if (!bookYes.asks?.[0] || !bookNo.asks?.[0]) continue;
    
    const askYes = parseFloat(bookYes.asks[0].price);
    const askNo = parseFloat(bookNo.asks[0].price);
    const implied = askYes + askNo;
    
    if (implied < 0.99) {
      opportunities.push({
        market: market.question || "Unknown",
        marketId: market.condition_id,
        impliedTotal: implied,
        yesAsk: askYes,
        noAsk: askNo,
        edge: 1.0 - implied,
        tokenYes: tokenYes.token_id,
        tokenNo: tokenNo.token_id
      });
    }
  }
  
  return opportunities;
}

console.log("Fetching active markets...");
const markets = await fetchActiveMarkets();
console.log(`Found ${markets.length} active markets`);

console.log("Scanning for YES+NO arbitrage...");
const arb = await scanArbitrage(markets);

const result = {
  timestamp: new Date().toISOString(),
  marketsCount: markets.length,
  arbitrageOpportunities: arb
};

await Bun.write("/workspace/state/market_scan.json", JSON.stringify(result, null, 2));
console.log(`Found ${arb.length} arbitrage opportunities`);
for (const opp of arb.slice(0, 3)) {
  console.log(`  ${opp.market.slice(0, 60)}... | Implied: ${opp.impliedTotal.toFixed(4)} | Edge: ${(opp.edge * 100).toFixed(2)}%`);
}
