#!/usr/bin/env python3
"""Developer-only loopback model relay for the phone core.

Listens on 127.0.0.1 and forwards OpenAI-compatible POST /v1/chat/completions
to one provider from ~/.niuma/settings.json, swapping the caller's test token
for that provider's credential. The phone reaches it through
`adb reverse tcp:18080 tcp:18080`, so the provider key never leaves this
computer and Super's execution adapter accepts the plain-http loopback route.
It logs method, path, status and latency only: never bodies, tokens or keys.

usage:
  RELAY_TEST_TOKEN=$(openssl rand -hex 16) python3 -I dev-model-relay.py <provider-name> [port] [--inject STATUS:EVERY[:RETRY_AFTER]]...

--inject answers every EVERY-th request with STATUS itself, without
forwarding it (so nothing is billed), adding Retry-After when given, for
testing how the phone core retries: e.g. --inject 429:3:4 --inject 500:5.
"""
import argparse
import hmac
import itertools
import json
import threading
import os
import sys
import time
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

parser = argparse.ArgumentParser()
parser.add_argument("provider")
parser.add_argument("port", nargs="?", type=int, default=18080)
parser.add_argument("--inject", action="append", default=[], help="STATUS:EVERY[:RETRY_AFTER]")
args = parser.parse_args()
PROVIDER, PORT = args.provider, args.port
INJECT = []
for rule in args.inject:
    parts = rule.split(":")
    if len(parts) not in (2, 3) or not all(x.isdigit() for x in parts) or int(parts[1]) < 1:
        sys.exit(f"bad --inject {rule!r}; want STATUS:EVERY[:RETRY_AFTER]")
    INJECT.append((int(parts[0]), int(parts[1]), parts[2] if len(parts) == 3 else None))
COUNTER = itertools.count(1)
COUNTER_LOCK = threading.Lock()
TEST_TOKEN = os.environ["RELAY_TEST_TOKEN"]

with open(os.path.expanduser("~/.niuma/settings.json"), encoding="utf-8") as f:
    settings = json.load(f)
MAX_BODY = 4 << 20
providers = settings.get("fallback", {}).get("providers", [])
entry = next((p for p in providers if p.get("name") == PROVIDER), None)
if entry is None:
    sys.exit(f"provider {PROVIDER!r} not found; available: {', '.join(sorted(p.get('name', '') for p in providers))}")
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
            if length < 0 or length > MAX_BODY:
                status = 413
                self._send(413, b'{"error":"body too large"}')
                return
            body = self.rfile.read(length)
            with COUNTER_LOCK:
                n = next(COUNTER)
            for code, every, retry_after in INJECT:
                if n % every == 0:
                    status = code
                    self.send_response(code)
                    if retry_after is not None:
                        self.send_header("Retry-After", retry_after)
                    payload = b'{"error":{"type":"injected","message":"injected by dev-model-relay"}}'
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(payload)))
                    self.end_headers()
                    self.wfile.write(payload)
                    self.command += " (injected)"
                    return
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
            except (urllib.error.URLError, OSError):
                status, data, ctype = 502, b'{"error":"upstream unreachable"}', "application/json"
            self._send(status, data, ctype)
        finally:
            print(f"relay {self.command} {self.path} -> {status} in {int((time.time() - start) * 1000)} ms", flush=True)


print(f"relay ready on 127.0.0.1:{PORT} for provider {PROVIDER}" + (f" injecting {args.inject}" if INJECT else ""), flush=True)
ThreadingHTTPServer(("127.0.0.1", PORT), Relay).serve_forever()
