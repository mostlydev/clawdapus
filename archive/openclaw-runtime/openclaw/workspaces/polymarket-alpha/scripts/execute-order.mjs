// Polymarket CLOB Order Execution Module
// Uses L2 signature authentication (no private key exposure)

const CLOB_HOST = "https://clob.polymarket.com";
const fs = await import('fs');
const path = await import('path');

// Load credentials from state (key kept in env, not file)
const credsPath = '/workspace/state/polymarket_l2_creds.json';
let creds;
try {
  creds = JSON.parse(await fs.promises.readFile(credsPath, 'utf8'));
} catch (e) {
  console.error('No credentials found. Run auth setup first.');
  process.exit(1);
}

// API key optional for public read endpoints
const API_KEY = process.env.POLYMARKET_API_KEY;
const PRIVATE_KEY = process.env.POLYMARKET_PRIVATE_KEY;

/**
 * Create and sign an order
 * Uses Polymarket's L2 order signing (EIP-712 style via CTFExchange)
 */
export async function createOrder({
  tokenId,
  side, // 'BUY' or 'SELL'
  size, // in number of outcome tokens
  price, // 0.0 to 1.0
  feeRateBps = 100 // 1% default
}) {
  const orderData = {
    salt: Date.now().toString(),
    maker: creds.address,
    signer: creds.address,
    taker: "0x0000000000000000000000000000000000000000",
    tokenId: tokenId,
    makerAmount: side === 'BUY' 
      ? Math.floor(size * price * 1e6).toString() // USDC amount
      : Math.floor(size * 1e6).toString(), // Token amount
    takerAmount: side === 'BUY'
      ? Math.floor(size * 1e6).toString() // Token amount
      : Math.floor(size * price * 1e6).toString(), // USDC amount
    expiration: (Math.floor(Date.now() / 1000) + 86400).toString(), // 24h
    nonce: Date.now().toString(),
    feeRateBps: feeRateBps.toString(),
    side: side,
    signatureType: 2 // Polymarket L2
  };

  // Note: Actual signing requires ethers.js or viem with the derived key
  // This is the structure - signing happens client-side or via secure enclave
  return orderData;
}

/**
 * Submit order to CLOB
 */
export async function submitOrder(orderData) {
  const resp = await fetch(`${CLOB_HOST}/order`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'POLYMARKET_API_KEY': API_KEY
    },
    body: JSON.stringify(orderData)
  });
  
  if (!resp.ok) {
    const err = await resp.text();
    throw new Error(`Order failed: ${resp.status} - ${err}`);
  }
  
  return await resp.json();
}

/**
 * Execute an arbitrage trade: buy YES + buy NO simultaneously
 */
export async function executeArbitrage({
  tokenYes,
  tokenNo,
  sizeYes,
  sizeNo,
  priceYes,
  priceNo
}) {
  console.log(`Executing arb: YES@${priceYes} + NO@${priceNo} = ${priceYes + priceNo}`);
  
  const [orderYes, orderNo] = await Promise.all([
    createOrder({ tokenId: tokenYes, side: 'BUY', size: sizeYes, price: priceYes }),
    createOrder({ tokenId: tokenNo, side: 'BUY', size: sizeNo, price: priceNo })
  ]);
  
  // In production, these would be signed and submitted atomically
  // For now, log the order structure for verification
  console.log('YES order:', orderYes);
  console.log('NO order:', orderNo);
  
  return { orderYes, orderNo };
}

/**
 * Quick arbitrage scan - checks top N markets only
 */
export async function quickArbScan(limit = 20) {
  const resp = await fetch(`${CLOB_HOST}/markets?active=true&limit=${limit}`);
  const data = await resp.json();
  const markets = data.data || [];
  
  const opportunities = [];
  
  for (const market of markets) {
    const tokens = market.tokens || [];
    if (tokens.length !== 2) continue;
    
    const yesToken = tokens.find(t => t.outcome?.toUpperCase() === 'YES');
    const noToken = tokens.find(t => t.outcome?.toUpperCase() === 'NO');
    if (!yesToken || !noToken) continue;
    
    // Fetch orderbooks in parallel
    const [bookYes, bookNo] = await Promise.all([
      fetch(`${CLOB_HOST}/book/${yesToken.token_id}`).then(r => r.ok ? r.json() : null),
      fetch(`${CLOB_HOST}/book/${noToken.token_id}`).then(r => r.ok ? r.json() : null)
    ]);
    
    if (!bookYes?.asks?.[0] || !bookNo?.asks?.[0]) continue;
    
    const askYes = parseFloat(bookYes.asks[0].price);
    const askNo = parseFloat(bookNo.asks[0].price);
    const implied = askYes + askNo;
    
    if (implied < 0.995) { // 0.5% threshold after estimated fees
      opportunities.push({
        market: market.question,
        conditionId: market.condition_id,
        impliedTotal: implied,
        edge: 1 - implied,
        yesAsk: askYes,
        noAsk: askNo,
        tokenYes: yesToken.token_id,
        tokenNo: noToken.token_id,
        sizeYes: parseFloat(bookYes.asks[0].size),
        sizeNo: parseFloat(bookNo.asks[0].size)
      });
    }
  }
  
  return opportunities.sort((a, b) => b.edge - a.edge);
}

// CLI usage
if (import.meta.main) {
  const args = process.argv.slice(2);
  const cmd = args[0];
  
  if (cmd === 'scan') {
    console.log('Quick arb scan...');
    const opps = await quickArbScan(30);
    console.log(`Found ${opps.length} opportunities`);
    for (const opp of opps.slice(0, 5)) {
      console.log(`  ${opp.market?.slice(0, 50)} | Edge: ${(opp.edge * 100).toFixed(2)}% | Total: ${opp.impliedTotal.toFixed(4)}`);
    }
    
    // Save results
    await fs.promises.writeFile(
      '/workspace/state/arb_scan.json',
      JSON.stringify({ timestamp: new Date().toISOString(), opportunities: opps }, null, 2)
    );
  }
  
  if (cmd === 'execute') {
    const tokenYes = args[1];
    const tokenNo = args[2];
    if (!tokenYes || !tokenNo) {
      console.error('Usage: execute <tokenYes> <tokenNo>');
      process.exit(1);
    }
    console.log('Order execution module ready (signing required)');
    // Full implementation requires ethers.js signing integration
  }
}
