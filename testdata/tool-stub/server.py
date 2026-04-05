import json
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, HTTPServer


class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        print(f"[tool-stub] {fmt % args}")

    def send_json(self, status, payload):
        body = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path == "/health":
            self.send_json(200, {"status": "ok"})
            return

        prefix = "/api/v1/runtime_context/"
        if self.path.startswith(prefix):
            claw_id = self.path[len(prefix):].strip()
            if not claw_id:
                self.send_json(400, {"ok": False, "error": "missing claw_id"})
                return
            self.send_json(200, {
                "ok": True,
                "claw_id": claw_id,
                "runtime": "openclaw",
                "status": "capability wave online",
                "tool": "tool-stub.get_runtime_context",
                "ts": datetime.now(timezone.utc).isoformat(),
            })
            return

        self.send_json(404, {"ok": False, "error": "unknown route"})


if __name__ == "__main__":
    server = HTTPServer(("0.0.0.0", 8080), Handler)
    print("[tool-stub] listening on :8080")
    server.serve_forever()
