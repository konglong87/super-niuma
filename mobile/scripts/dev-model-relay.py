#!/usr/bin/env python3
"""Developer-only loopback model relay for the phone core.

Listens on 127.0.0.1 and forwards OpenAI-compatible POST /v1/chat/completions
to one provider from ~/.niuma/settings.json, swapping the caller's test token
for that provider's credential. The phone reaches it through
`adb reverse tcp:18080 tcp:18080`, so the provider key never leaves this
computer and Super's execution adapter accepts the plain-http loopback route.
It logs method, path, status and latency only: never bodies, tokens or keys.

usage:
  RELAY_TEST_TOKEN=$(openssl rand -hex 16) python3 -I dev-model-relay.py <provider-name> [port]
"""
import hmac
import json
import os
import sys
import time
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PROVIDER = sys.argv[1]
PORT = int(sys.argv[2]) if len(sys.argv) > 2 else 18080
TEST_TOKEN = os.environ["RELAY_TEST_TOKEN"]

with open(os.path.expanduser("~/.niuma/settings.json"), encoding="utf-8") as f:
    settings = json.load(f)
entry = next(p for p in settings["fallback"]["providers"] if p.get("name") == PROVIDER)
if entry.get("protocol") != "openai-chat-completions":
    sys.exit("relay supports openai-chat-completions providers only")
UPSTREAM = entry["baseURL"].rstrip("/")
CREDENTIAL = entry.get("apiKey") or entry.get("authToken")
if not CREDENTIAL:
    sys.exit("provider has no credential")


class Relay(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def _send(self, status, body, ctype="application/json"):
        self.send_response(status)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):
        start = time.time()
        status = 0
        try:
            given = self.headers.get("Authorization", "")
            if not hmac.compare_digest(given, "Bearer " + TEST_TOKEN):
                status = 401
                self._send(401, b'{"error":"bad test token"}')
                return
            if self.path != "/v1/chat/completions":
                status = 404
                self._send(404, b'{"error":"not found"}')
                return
            length = int(self.headers.get("Content-Length", "0"))
            body = self.rfile.read(length)
            req = urllib.request.Request(
                UPSTREAM + "/chat/completions",
                data=body,
                method="POST",
                headers={"Content-Type": "application/json", "Authorization": "Bearer " + CREDENTIAL},
            )
            try:
                with urllib.request.urlopen(req, timeout=110) as resp:
                    status, data = resp.status, resp.read()
                    ctype = resp.headers.get("Content-Type", "application/json")
            except urllib.error.HTTPError as e:
                status, data, ctype = e.code, e.read(), e.headers.get("Content-Type", "application/json")
            self._send(status, data, ctype)
        finally:
            print(f"relay {self.command} {self.path} -> {status} in {int((time.time() - start) * 1000)} ms", flush=True)


print(f"relay ready on 127.0.0.1:{PORT} for provider {PROVIDER}", flush=True)
ThreadingHTTPServer(("127.0.0.1", PORT), Relay).serve_forever()
