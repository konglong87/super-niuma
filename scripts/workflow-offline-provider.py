#!/usr/bin/env python3
"""Loopback-only, bounded OpenAI chat-completions SSE fixture (Python stdlib).

Never forwards requests, reads credentials, or records prompts/Authorization.
Startup prints only public loopback coordinates. Request accounting retains only
whitelisted mode, ordinal, dependency COUNT, timing, and outcome.

  python3 scripts/workflow-offline-provider.py --port 0 --settings-out /isolated/settings.json
  POST /__control/reset {"mode":"fast|slow|fail-3-times|by-model", "slow_seconds":30}
  GET /__control/state   (safe counters only)
The first THREE fail-3-times HTTP calls return 503 (initial + two client retries).
Call four succeeds, allowing the real workflow node retry to be observed.
"""
import argparse
import json
import os
import signal
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

SERVICE = "niuma-workflow-offline-provider"
MODES = ("fast", "slow", "fail-3-times", "always-fail")
MAX_BODY = 256 * 1024
DUMMY_KEY = "offline-dummy-key"


def fixture_settings(base_url):
    return {
        "provider": "openai", "providerProtocol": "openai-chat-completions",
        "baseURL": base_url, "apiKey": DUMMY_KEY, "model": "fast",
        "modelOptions": list(MODES), "effort": "off",
        "fallback": {"enabled": False, "providers": []},
        "update": {"enabled": False, "checkOnStartup": False, "autoPull": False},
        "growthbook": {"enabled": False}, "imageGeneration": {"enabled": False},
        "hooks": {}, "mcpServers": {}, "additionalDirectories": [],
    }


class OfflineServer(ThreadingHTTPServer):
    daemon_threads = True
    allow_reuse_address = False

    def __init__(self, port=0, mode="by-model", slow_seconds=30, max_requests=200):
        super().__init__(("127.0.0.1", port), Handler)
        self.lock = threading.Lock()
        self.mode = mode
        self.slow_seconds = slow_seconds
        self.max_requests = max_requests
        self.calls = []
        self.inflight = 0
        self.max_inflight = 0
        self.mode_counts = {}
        self.lifetime_requests = 0
        self.lifetime_max_inflight = 0

    def state(self):
        with self.lock:
            return {"service": SERVICE, "mode": self.mode, "requests": len(self.calls),
                    "inflight": self.inflight, "max_inflight": self.max_inflight, "lifetime_requests": self.lifetime_requests,
                    "lifetime_max_inflight": self.lifetime_max_inflight,
                    "calls": [dict(call) for call in self.calls]}

    def handle_error(self, _request, _client_address):
        # Base implementation prints traceback/request details. Intentionally quiet.
        pass


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    server_version = SERVICE

    def log_message(self, _format, *args):
        pass

    def respond(self, status, value, retry=False):
        encoded = json.dumps(value).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(encoded)))
        self.send_header("Cache-Control", "no-store")
        if retry:
            self.send_header("Retry-After", "0")
        self.end_headers()
        self.wfile.write(encoded)

    def read_json(self):
        try:
            length = int(self.headers.get("Content-Length", "0"))
            if length < 1 or length > MAX_BODY or self.headers.get("Transfer-Encoding"):
                raise ValueError()
            value = json.loads(self.rfile.read(length))
            if not isinstance(value, dict):
                raise ValueError()
            return value
        except (ValueError, UnicodeError):
            self.respond(400, {"error": {"message": "Invalid bounded JSON request"}})
            return None

    def do_GET(self):
        if self.path == "/health":
            return self.respond(200, {"service": SERVICE, "version": 1, "host": "127.0.0.1"})
        if self.path == "/__control/state":
            return self.respond(200, self.server.state())
        if self.path == "/v1/models":
            return self.respond(200, {"object": "list", "data": [{"id": mode, "object": "model", "owned_by": "offline-fixture"} for mode in MODES]})
        self.respond(404, {"error": {"message": "Offline fixture route not found"}})

    def do_POST(self):
        try:
            value = self.read_json()
            if value is None:
                return
            if self.path == "/__control/reset":
                return self.reset(value)
            if self.path != "/v1/chat/completions":
                return self.respond(404, {"error": {"message": "Offline fixture route not found"}})
            return self.completion(value)
        except (BrokenPipeError, ConnectionResetError, TimeoutError):
            self.close_connection = True

    def reset(self, value):
        mode = value.get("mode", "by-model")
        seconds = value.get("slow_seconds", 30)
        if mode not in (*MODES, "by-model") or not isinstance(seconds, (int, float)) or not 0.2 <= seconds <= 120:
            return self.respond(400, {"error": {"message": "Invalid fixture mode or bounded delay"}})
        with self.server.lock:
            if self.server.inflight:
                return self.respond(409, {"error": {"message": "Wait for active fixture calls to stop before reset"}})
            self.server.mode = mode
            self.server.slow_seconds = seconds
            self.server.calls = []
            self.server.mode_counts = {}
            self.server.max_inflight = 0
        self.respond(200, {"reset": True, "mode": mode})

    def completion(self, value):
        # Reject arbitrary model names rather than retaining user-supplied strings.
        model = value.get("model")
        if model not in MODES or value.get("tools") or value.get("functions"):
            return self.respond(400, {"error": {"message": "Only whitelisted offline text models without tools are allowed"}})
        dependency_count = 0
        for message in value.get("messages", []):
            if isinstance(message, dict) and message.get("role") == "user" and isinstance(message.get("content"), str):
                try:
                    inner = json.loads(message["content"])
                    if isinstance(inner, dict) and isinstance(inner.get("declared_dependencies"), list):
                        dependency_count = min(len(inner["declared_dependencies"]), 16)
                except ValueError:
                    pass
        with self.server.lock:
            if self.server.lifetime_requests >= self.server.max_requests:
                return self.respond(429, {"error": {"message": "Offline fixture request bound reached"}})
            mode = model if self.server.mode == "by-model" else self.server.mode
            ordinal = len(self.server.calls) + 1
            count = self.server.mode_counts.get(mode, 0) + 1
            self.server.mode_counts[mode] = count
            call = {"ordinal": ordinal, "mode": mode, "dependency_count": dependency_count,
                    "started": time.monotonic(), "outcome": "active"}
            self.server.calls.append(call)
            self.server.lifetime_requests += 1
            self.server.inflight += 1
            self.server.max_inflight = max(self.server.max_inflight, self.server.inflight)
            self.server.lifetime_max_inflight = max(self.server.lifetime_max_inflight, self.server.inflight)
            slow_seconds = self.server.slow_seconds
        outcome = "disconnected"
        try:
            if mode == "always-fail" or mode == "fail-3-times" and count <= 3:
                self.respond(503, {"error": {"message": "Offline fixture transient failure", "type": "server_error", "code": "offline_fixture_unavailable"}}, retry=True)
                outcome = "503"
                return
            text = f"Offline fixture result {ordinal}. Declared dependency artifacts: {dependency_count}."
            if not value.get("stream"):
                time.sleep(min(slow_seconds if mode == "slow" else 0.25, 120))
                self.respond(200, {"id": f"offline-{ordinal}", "object": "chat.completion", "created": int(time.time()), "model": model, "choices": [{"index": 0, "message": {"role": "assistant", "content": text}, "finish_reason": "stop"}], "usage": self.usage()})
                outcome = "completed"
                return
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Cache-Control", "no-cache")
            self.send_header("Connection", "close")
            self.end_headers()
            self.close_connection = True
            def chunk(delta, finish=None, usage=None):
                payload = {"id": f"offline-{ordinal}", "object": "chat.completion.chunk", "created": int(time.time()), "model": model, "choices": [{"index": 0, "delta": delta, "finish_reason": finish}]}
                if usage is not None:
                    payload["usage"] = usage
                self.wfile.write(("data: " + json.dumps(payload) + "\n\n").encode())
                self.wfile.flush()
            chunk({"role": "assistant"})
            deadline = time.monotonic() + (slow_seconds if mode == "slow" else 0.35)
            while time.monotonic() < deadline:
                time.sleep(0.1)
                self.wfile.write(b": offline heartbeat\n\n")
                self.wfile.flush()
            chunk({"content": text})
            chunk({}, "stop", self.usage())
            self.wfile.write(b"data: [DONE]\n\n")
            self.wfile.flush()
            outcome = "completed"
        except (BrokenPipeError, ConnectionResetError, TimeoutError):
            self.close_connection = True
        finally:
            with self.server.lock:
                call["outcome"] = outcome
                call["finished"] = time.monotonic()
                self.server.inflight -= 1

    @staticmethod
    def usage():
        return {"prompt_tokens": 24, "completion_tokens": 12, "total_tokens": 36, "prompt_tokens_details": {"cached_tokens": 0}}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--port", type=int, default=0)
    parser.add_argument("--mode", choices=(*MODES, "by-model"), default="by-model")
    parser.add_argument("--slow-seconds", type=float, default=30)
    parser.add_argument("--max-requests", type=int, default=200)
    parser.add_argument("--settings-out", type=Path)
    args = parser.parse_args()
    if not 0 <= args.port <= 65535 or not 0.2 <= args.slow_seconds <= 120 or not 1 <= args.max_requests <= 1000:
        parser.error("port/delay/request bound is invalid")
    server = OfflineServer(args.port, args.mode, args.slow_seconds, args.max_requests)
    base_url = f"http://127.0.0.1:{server.server_port}/v1"
    if args.settings_out:
        descriptor = os.open(args.settings_out, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(descriptor, "w") as output:
            json.dump(fixture_settings(base_url), output, indent=2)
    for signum in (signal.SIGINT, signal.SIGTERM):
        signal.signal(signum, lambda *_: threading.Thread(target=server.shutdown, daemon=True).start())
    print(json.dumps({"service": SERVICE, "base_url": base_url, "control_url": base_url[:-3] + "/__control", "models": MODES}), flush=True)
    try:
        server.serve_forever(poll_interval=0.1)
    finally:
        server.server_close()


if __name__ == "__main__":
    main()
