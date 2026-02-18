#!/usr/bin/env node
/* eslint-disable no-console */

/**
 * Kalshi API Client
 * REST API wrapper for CFTC-regulated prediction market
 * Reference: https://trading-api.readme.io/reference/
 */

const https = require("https");
const crypto = require("crypto");

const KALSHI_API_HOST = process.env.KALSHI_API_HOST || "api.elections.kalshi.com";
const KALSHI_API_KEY = process.env.KALSHI_API_KEY || "";
const KALSHI_API_SECRET = process.env.KALSHI_API_SECRET || "";

class KalshiClient {
  constructor(apiKey = KALSHI_API_KEY, apiSecret = KALSHI_API_SECRET) {
    this.apiKey = apiKey;
    this.apiSecret = apiSecret;
    this.basePath = "/trade-api/v2";
  }

  // Generate request signature
  _sign(method, path, timestamp, body = "") {
    if (!this.apiSecret) return null;
    const message = `${method}${path}${timestamp}${body}`;
    return crypto.createHmac("sha256", this.apiSecret).update(message).digest("base64");
  }

  // Generic API request
  async _request(method, path, body = null) {
    return new Promise((resolve, reject) => {
      const timestamp = String(Math.floor(Date.now() / 1000));
      const bodyStr = body ? JSON.stringify(body) : "";
      const signature = this._sign(method, path, timestamp, bodyStr);

      const options = {
        hostname: KALSHI_API_HOST,
        port: 443,
        path: path,
        method: method,
        headers: {
          "Accept": "application/json",
          "Content-Type": "application/json",
        },
      };

      if (this.apiKey && signature) {
        options.headers["KALSHI-ACCESS-KEY"] = this.apiKey;
        options.headers["KALSHI-ACCESS-TIMESTAMP"] = timestamp;
        options.headers["KALSHI-ACCESS-SIGNATURE"] = signature;
      }

      const req = https.request(options, (res) => {
        let data = "";
        res.on("data", (chunk) => (data += chunk));
        res.on("end", () => {
          try {
            const parsed = JSON.parse(data);
            if (res.statusCode >= 200 && res.statusCode < 300) {
              resolve(parsed);
            } else {
              reject(new Error(`HTTP ${res.statusCode}: ${parsed.message || data}`));
            }
          } catch {
            resolve(data);
          }
        });
      });

      req.on("error", reject);
      if (bodyStr) req.write(bodyStr);
      req.end();
    });
  }

  // PUBLIC ENDPOINTS (no auth required)

  // Get exchange status
  async getExchangeStatus() {
    return this._request("GET", `${this.basePath}/exchange/status`);
  }

  // Get all markets
  async getMarkets(params = {}) {
    const query = new URLSearchParams(params).toString();
    const path = query
      ? `${this.basePath}/markets?${query}`
      : `${this.basePath}/markets`;
    return this._request("GET", path);
  }

  // Get specific market
  async getMarket(ticker) {
    return this._request("GET", `${this.basePath}/markets/${ticker}`);
  }

  // Get market orderbook
  async getOrderBook(ticker, params = {}) {
    const query = new URLSearchParams(params).toString();
    const path = query
      ? `${this.basePath}/markets/${ticker}/orderbook?${query}`
      : `${this.basePath}/markets/${ticker}/orderbook`;
    return this._request("GET", path);
  }

  // Get series (grouped markets)
  async getSeries(seriesTicker) {
    return this._request("GET", `${this.basePath}/series/${seriesTicker}`);
  }

  // AUTHENTICATED ENDPOINTS (requires API key)

  // Get account balance
  async getBalance() {
    return this._request("GET", `${this.basePath}/portfolio/balance`);
  }

  // Get positions
  async getPositions() {
    return this._request("GET", `${this.basePath}/portfolio/positions`);
  }

  // Get orders
  async getOrders(params = {}) {
    const query = new URLSearchParams(params).toString();
    const path = query
      ? `${this.basePath}/portfolio/orders?${query}`
      : `${this.basePath}/portfolio/orders`;
    return this._request("GET", path);
  }

  // Place order
  async createOrder(order) {
    return this._request("POST", `${this.basePath}/portfolio/orders`, order);
  }

  // Cancel order
  async cancelOrder(orderId) {
    return this._request("DELETE", `${this.basePath}/portfolio/orders/${orderId}`);
  }
}

// CLI test mode
async function main() {
  const client = new KalshiClient();

  // Test public endpoint (no auth required)
  try {
    console.log("[kalshi] Testing exchange status...");
    const status = await client.getExchangeStatus();
    console.log("[kalshi] Exchange status:", JSON.stringify(status, null, 2));
  } catch (err) {
    console.log("[kalshi] Status check failed:", err.message);
  }

  // Count available markets
  try {
    console.log("[kalshi] Fetching markets...");
    const markets = await client.getMarkets({ limit: "20" });
    const count = markets?.markets?.length || 0;
    console.log(`[kalshi] Found ${count} markets`);
    
    if (count > 0) {
      const sample = markets.markets[0];
      console.log(`[kalshi] Sample market: ${sample.ticker} - ${sample.title}`);
      
      // Try to fetch orderbook for sample market
      try {
        const book = await client.getOrderBook(sample.ticker);
        console.log(`[kalshi] Orderbook available for ${sample.ticker}`);
      } catch (bookErr) {
        console.log(`[kalshi] Orderbook fetch failed: ${bookErr.message}`);
      }
    }
  } catch (err) {
    console.log("[kalshi] Markets fetch failed:", err.message);
  }

  // Test authenticated endpoints (will fail without credentials)
  if (KALSHI_API_KEY) {
    try {
      console.log("[kalshi] Testing authenticated endpoints...");
      const balance = await client.getBalance();
      console.log("[kalshi] Balance:", JSON.stringify(balance, null, 2));
    } catch (err) {
      console.log("[kalshi] Auth test failed:", err.message);
    }
  } else {
    console.log("[kalshi] No API credentials - skipping authenticated tests");
  }
}

if (require.main === module) {
  main().catch((err) => {
    console.error("[kalshi] Fatal error:", err.message);
    process.exit(1);
  });
}

module.exports = { KalshiClient };
