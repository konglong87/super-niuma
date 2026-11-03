#!/usr/bin/env python3
"""Black-box acceptance of the built intake binary; synthetic data only.

No model, legacy credentials, Session store or external channel is used.
Owned processes and their temporary databases are cleaned even after failure.
"""
import argparse
import hashlib
import json
from pathlib import Path
import selectors
import subprocess
import tempfile
import urllib.error
import urllib.request
from urllib.parse import urlparse

API = "/api/super/v1"
MARKER = {"X-Super-Niuma-Request": "1"}
KEY = "super-intake-blackbox-fixture"
PAYLOAD = {"title": "Synthetic intake acceptance", "objective": "Record only; never execute.",
           "acceptance_criteria": ["Survives restart", "Cancellation preserves history"], "workspace": "/workspace/demo"}


class Process:
    def __init__(self, binary, directory):
        self.process = subprocess.Popen([str(binary), "--listen", "127.0.0.1:0", "--state-dir", directory],
                                        stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True)
        try:
            with selectors.DefaultSelector() as poller:
                poller.register(self.process.stdout, selectors.EVENT_READ)
                if not poller.select(12):
                    raise RuntimeError("startup deadline exceeded")
                line = self.process.stdout.readline().strip()
            prefix = "Super Niuma intake-only: "
            if not line.startswith(prefix):
                raise RuntimeError("unexpected startup response")
            self.base = line[len(prefix):]
            url = urlparse(self.base)
            if url.scheme != "http" or url.hostname != "127.0.0.1" or not url.port:
                raise RuntimeError("acceptance target must be the owned loopback listener")
        except BaseException:
            self.close()
            raise

    def close(self):
        if self.process.poll() is None:
            self.process.terminate()
            try:
                self.process.wait(timeout=8)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait(timeout=3)
        if self.process.stdout:
            self.process.stdout.close()

    def request(self, path, method="GET", payload=None, headers=None, marker=True, raw=None):
        values = dict(MARKER) if marker else {}
        if payload is not None:
            raw = json.dumps(payload).encode()
        if raw is not None:
            values["Content-Type"] = "application/json"
        values.update(headers or {})
        request = urllib.request.Request(self.base + API + path, data=raw, headers=values, method=method)
        try:
            response = urllib.request.urlopen(request, timeout=5)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            return response.status, json.load(response)


def verify(binary):
    checks = []
    def expect(result, status, label):
        code, data = result
        if code != status:
            raise AssertionError(f"{label}: HTTP {code}, expected {status}")
        checks.append(label)
        return data
    with tempfile.TemporaryDirectory(prefix="super-intake-smoke-") as directory:
        # SQLite's direct parent must be private; TemporaryDirectory provides 0700.
        server = Process(binary, directory)
        try:
            status = expect(server.request("/status"), 200, "status")
            assert status["mode"] == "intake_only" and not status["execution_enabled"] and not status["llm_routing_enabled"]
            expect(server.request("/status", marker=False), 403, "missing marker")
            expect(server.request("/status", headers={"Origin": "https://attacker.example"}), 403, "cross origin")
            expect(server.request("/status", headers={"Host": "attacker.example"}), 403, "wrong host")
            expect(server.request("//status"), 404, "unclean path")
            expect(server.request("/status?unexpected=1"), 400, "unknown query")
            order = expect(server.request("/work-orders", "POST", PAYLOAD, {"Idempotency-Key": KEY}), 201, "create")
            identity = order["id"]
            assert order["status"] == "recorded"
            assert all(order[name] == "not_started" for name in ("execution_status", "verification_status", "delivery_status"))
            retry = expect(server.request("/work-orders", "POST", PAYLOAD, {"Idempotency-Key": KEY}), 200, "same-key retry")
            assert retry["id"] == identity
            expect(server.request("/work-orders", "POST", dict(PAYLOAD, title="Changed"), {"Idempotency-Key": KEY}), 409, "key conflict")
            expect(server.request("/work-orders", "POST", dict(PAYLOAD, workspace="relative"), {"Idempotency-Key": "relative"}), 400, "relative workspace")
            expect(server.request("/work-orders", "POST", dict(PAYLOAD, unknown=True), {"Idempotency-Key": "unknown"}), 400, "unknown field")
            expect(server.request("/work-orders", "POST", PAYLOAD, {"Idempotency-Key": "media", "Content-Type": "text/plain"}), 415, "wrong media type")
            expect(server.request("/work-orders", "POST", headers={"Idempotency-Key": "large"}, raw=b"x"*70000), 413, "body limit")
            items = expect(server.request("/work-orders"), 200, "list")["items"]
            assert len(items) == 1 and items[0]["id"] == identity
            cancelled = expect(server.request(f"/work-orders/{identity}/cancel", "POST", {}), 200, "cancel")
            again = expect(server.request(f"/work-orders/{identity}/cancel", "POST", {}), 200, "repeat cancel")
            assert cancelled["status"] == again["status"] == "cancelled" and cancelled["updated_at"] == again["updated_at"]
            retry = expect(server.request("/work-orders", "POST", PAYLOAD, {"Idempotency-Key": KEY}), 200, "retry cannot revive")
            assert retry["id"] == identity and retry["status"] == "cancelled"
        finally:
            server.close()
        server = Process(binary, directory)
        try:
            restored = expect(server.request(f"/work-orders/{identity}"), 200, "restart persistence")
            assert restored["status"] == "cancelled" and restored["acceptance_criteria"] == PAYLOAD["acceptance_criteria"]
        finally:
            server.close()
    return {"passed": True, "checks": checks, "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
            "execution_enabled": status["execution_enabled"], "llm_routing_enabled": status["llm_routing_enabled"],
            "synthetic_data_only": True, "owned_processes_and_state_cleaned": True}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True)
    arguments = parser.parse_args()
    print(json.dumps(verify(arguments.binary.resolve()), ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
