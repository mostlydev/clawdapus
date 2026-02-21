"""
Mock trading-api server for local development and claw.skill.emit testing.

Replace with your real application in production.
"""

import json
import uuid
from http.server import BaseHTTPRequestHandler, HTTPServer
from datetime import datetime, timezone

PORT = 4000

TRADES = {}  # in-memory store


def ok(data):
    return 200, data


def created(data):
    return 201, data


def not_found(msg):
    return 404, {"error": msg}


def bad_request(msg):
    return 400, {"error": msg}


class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        print(f"[trading-api] {fmt % args}")

    def send_json(self, status, data):
        body = json.dumps(data).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def read_json(self):
        length = int(self.headers.get("Content-Length", 0))
        if length == 0:
            return {}
        return json.loads(self.rfile.read(length))

    def do_GET(self):
        if self.path == "/health":
            status, data = ok({"status": "ok", "time": datetime.now(timezone.utc).isoformat()})
        elif self.path == "/trades":
            status, data = ok(list(TRADES.values()))
        elif self.path.startswith("/trades/"):
            trade_id = self.path.split("/")[-1]
            trade = TRADES.get(trade_id)
            status, data = (ok(trade) if trade else not_found(f"trade {trade_id} not found"))
        else:
            status, data = not_found("unknown route")
        self.send_json(status, data)

    def do_POST(self):
        body = self.read_json()

        if self.path == "/trades/propose":
            required = {"agent", "symbol", "side", "quantity"}
            missing = required - body.keys()
            if missing:
                status, data = bad_request(f"missing fields: {', '.join(sorted(missing))}")
            else:
                trade_id = str(uuid.uuid4())[:8]
                trade = {
                    "id": trade_id,
                    "agent": body["agent"],
                    "symbol": body["symbol"].upper(),
                    "side": body["side"],
                    "quantity": body["quantity"],
                    "price_limit": body.get("price_limit"),
                    "thesis": body.get("thesis", ""),
                    "status": "advisory",
                    "created_at": datetime.now(timezone.utc).isoformat(),
                    "next": f"GET /trades/{trade_id} to check status, or POST /trades/{trade_id}/confirm to proceed",
                }
                TRADES[trade_id] = trade
                status, data = created(trade)

        elif self.path.endswith("/confirm"):
            trade_id = self.path.split("/")[-2]
            trade = TRADES.get(trade_id)
            if not trade:
                status, data = not_found(f"trade {trade_id} not found")
            elif trade["status"] != "advisory":
                status, data = bad_request(f"trade is {trade['status']}, cannot confirm")
            else:
                trade["status"] = "approved"
                trade["approved_at"] = datetime.now(timezone.utc).isoformat()
                trade["next"] = "Trade approved and queued for execution."
                status, data = ok(trade)

        elif self.path.endswith("/cancel"):
            trade_id = self.path.split("/")[-2]
            trade = TRADES.get(trade_id)
            if not trade:
                status, data = not_found(f"trade {trade_id} not found")
            else:
                trade["status"] = "cancelled"
                trade["next"] = "Trade cancelled."
                status, data = ok(trade)

        else:
            status, data = not_found("unknown route")

        self.send_json(status, data)


if __name__ == "__main__":
    server = HTTPServer(("0.0.0.0", PORT), Handler)
    print(f"[trading-api] mock server listening on :{PORT}")
    server.serve_forever()
