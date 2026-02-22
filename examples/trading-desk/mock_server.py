"""
Mock trading-api server for local development and claw.skill.emit testing.

On startup, posts a status message to Discord via webhook, mentioning the
sibling agent bots by their Discord IDs (injected via CLAW_HANDLE_* env vars).
This proves:
  1. Non-claw services receive CLAW_HANDLE_* vars from the pod topology.
  2. The webhook wiring works end-to-end.
  3. Agent mention IDs are available for cross-service coordination.

Replace with your real application in production.
"""

import json
import os
import urllib.error
import urllib.request
import uuid
from http.server import BaseHTTPRequestHandler, HTTPServer
from datetime import datetime, timezone

PORT = 4000

TRADES = {}  # in-memory store

# Injected by claw-pod.yml + computeHandleEnvs — read once at startup.
_DISCORD_WEBHOOK = os.environ.get("DISCORD_TRADING_API_WEBHOOK", "")
_DATABASE_URL = os.environ.get("DATABASE_URL", "")

# CLAW_HANDLE_* vars are broadcast to all pod services, including non-claw ones.
# Format: CLAW_HANDLE_<SERVICE_UPPER>_DISCORD_ID
_TIVERTON_DISCORD_ID = os.environ.get("CLAW_HANDLE_TIVERTON_DISCORD_ID", "")
_WESTIN_DISCORD_ID = os.environ.get("CLAW_HANDLE_WESTIN_DISCORD_ID", "")


def _post_webhook(content: str) -> None:
    """Post a message to Discord via the configured webhook URL."""
    if not _DISCORD_WEBHOOK:
        print(f"[trading-api] no webhook configured — would have posted: {content}")
        return
    data = json.dumps({"content": content}).encode()
    req = urllib.request.Request(
        _DISCORD_WEBHOOK,
        data=data,
        headers={
            "Content-Type": "application/json",
            "User-Agent": "DiscordBot (https://github.com/mostlydev/clawdapus, 1.0)",
        },
    )
    print(f"[trading-api] webhook url prefix: {_DISCORD_WEBHOOK[:60]}...")
    try:
        urllib.request.urlopen(req, timeout=10)
        print(f"[trading-api] posted to Discord: {content}")
    except urllib.error.HTTPError as e:
        body = e.read().decode("utf-8", errors="replace")
        print(f"[trading-api] Discord post failed: HTTP {e.code} — {body}")
    except Exception as e:
        print(f"[trading-api] Discord post failed: {e}")


def announce_startup():
    """Post a startup message mentioning all known agent bots."""
    mentions = " ".join(
        f"<@{id}>" for id in [_TIVERTON_DISCORD_ID, _WESTIN_DISCORD_ID] if id
    )
    parts = ["trading-api online"]
    if mentions:
        parts.append(mentions)
    parts.append(f"DATABASE_URL: {'ok' if _DATABASE_URL else 'missing'}")
    _post_webhook(" | ".join(parts))


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
        elif self.path == "/env":
            # Report which key env vars are present — safe for logs (no values exposed)
            status, data = ok({
                "DISCORD_TRADING_API_WEBHOOK": bool(_DISCORD_WEBHOOK),
                "CLAW_HANDLE_TIVERTON_DISCORD_ID": bool(_TIVERTON_DISCORD_ID),
                "CLAW_HANDLE_WESTIN_DISCORD_ID": bool(_WESTIN_DISCORD_ID),
                "DATABASE_URL": bool(_DATABASE_URL),
            })
        elif self.path == "/ping":
            # On-demand: post a mention message to Discord for debugging.
            announce_startup()
            status, data = ok({"pong": True})
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
    announce_startup()
    server = HTTPServer(("0.0.0.0", PORT), Handler)
    print(f"[trading-api] mock server listening on :{PORT}")
    server.serve_forever()
