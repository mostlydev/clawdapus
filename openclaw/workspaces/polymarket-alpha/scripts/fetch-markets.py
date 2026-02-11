#!/usr/bin/env python3
"""Fetch Polymarket markets and scan for arbitrage opportunities."""
import json
import requests
import os
from datetime import datetime

CLOB_HOST = "https://clob.polymarket.com"

def fetch_active_markets():
    """Fetch active markets from Polymarket CLOB."""
    markets = []
    offset = 0
    limit = 100
    
    while True:
        url = f"{CLOB_HOST}/markets?active=true&limit={limit}&offset={offset}"
        try:
            r = requests.get(url, timeout=10)
            if r.status_code != 200:
                break
            data = r.json()
            batch = data.get("data", [])
            if not batch:
                break
            markets.extend(batch)
            offset += limit
            if len(batch) < limit:
                break
        except Exception as e:
            print(f"Error: {e}")
            break
    
    return markets

def fetch_orderbook(token_id):
    """Fetch top of book for a token."""
    try:
        url = f"{CLOB_HOST}/book/{token_id}"
        r = requests.get(url, timeout=5)
        if r.status_code == 200:
            return r.json()
    except:
        pass
    return None

def scan_yes_no_arbitrage(markets):
    """Scan for YES+NO mispricing opportunities."""
    opportunities = []
    
    for market in markets:
        tokens = market.get("tokens", [])
        if len(tokens) != 2:
            continue
        
        token_yes = None
        token_no = None
        
        for t in tokens:
            outcome = t.get("outcome", "").upper()
            if outcome == "YES":
                token_yes = t
            elif outcome == "NO":
                token_no = t
        
        if not token_yes or not token_no:
            continue
        
        # Fetch orderbooks
        book_yes = fetch_orderbook(token_yes.get("token_id"))
        book_no = fetch_orderbook(token_no.get("token_id"))
        
        if not book_yes or not book_no:
            continue
        
        asks_yes = book_yes.get("asks", [])
        bids_yes = book_yes.get("bids", [])
        asks_no = book_no.get("asks", [])
        bids_no = book_no.get("bids", [])
        
        if not asks_yes or not asks_no:
            continue
        
        best_ask_yes = float(asks_yes[0]["price"])
        best_ask_no = float(asks_no[0]["price"])
        
        implied_total = best_ask_yes + best_ask_no
        
        if implied_total < 0.99:  # 1% margin after estimated fees
            opportunities.append({
                "market": market.get("question", "Unknown"),
                "market_id": market.get("condition_id"),
                "implied_total": implied_total,
                "yes_ask": best_ask_yes,
                "no_ask": best_ask_no,
                "edge": 1.0 - implied_total,
                "token_yes": token_yes.get("token_id"),
                "token_no": token_no.get("token_id")
            })
    
    return opportunities

if __name__ == "__main__":
    print("Fetching active markets...")
    markets = fetch_active_markets()
    print(f"Found {len(markets)} active markets")
    
    print("Scanning for YES+NO arbitrage...")
    arb = scan_yes_no_arbitrage(markets)
    
    result = {
        "timestamp": datetime.utcnow().isoformat(),
        "markets_count": len(markets),
        "arbitrage_opportunities": arb
    }
    
    with open("/workspace/state/market_scan.json", "w") as f:
        json.dump(result, f, indent=2)
    
    print(f"Found {len(arb)} arbitrage opportunities")
    for opp in arb[:3]:
        print(f"  {opp['market'][:60]}... | Implied: {opp['implied_total']:.4f} | Edge: {opp['edge']:.2%}")
