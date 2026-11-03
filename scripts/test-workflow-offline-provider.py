#!/usr/bin/env python3
"""Stdlib wire/privacy/cancellation tests for the offline fixture. No external I/O."""
import http.client
import importlib.util
import json
import sys
from pathlib import Path
import threading
import time
import unittest

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location("offline_fixture", Path(__file__).with_name("workflow-offline-provider.py"))
fixture = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fixture)


class OfflineProviderTest(unittest.TestCase):
    def setUp(self):
        self.server = fixture.OfflineServer(slow_seconds=2)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=3)

    def request(self, path, value=None):
        connection = http.client.HTTPConnection("127.0.0.1", self.server.server_port, timeout=5)
        connection.request("POST" if value is not None else "GET", path, json.dumps(value) if value is not None else None, {"Content-Type": "application/json", "Authorization": "SENTINEL-AUTHORIZATION-NOT-RECORDED"})
        response = connection.getresponse()
        status, content = response.status, response.read().decode()
        connection.close()
        return status, content

    def completion(self, model="fast"):
        return self.request("/v1/chat/completions", {"model": model, "stream": True, "messages": [{"role": "user", "content": json.dumps({"objective": "SENTINEL-PRIVATE-PROMPT-NOT-RECORDED", "declared_dependencies": [{"content": "PRIVATE-ARTIFACT"}]})}]})

    def test_literal_bind_and_no_prompt_or_auth_in_state(self):
        self.assertEqual(self.server.server_address[0], "127.0.0.1")
        status, content = self.completion()
        self.assertEqual(status, 200)
        self.assertIn('"finish_reason": "stop"', content)
        self.assertIn('"prompt_tokens": 24', content)
        self.assertTrue(content.endswith("data: [DONE]\n\n"))
        state = json.dumps(self.server.state())
        for secret in ("SENTINEL", "PRIVATE-ARTIFACT", "Authorization", "messages", "objective"):
            self.assertNotIn(secret, state)
        self.assertEqual(self.server.state()["calls"][0]["dependency_count"], 1)

    def test_three_503_calls_then_success_and_reset(self):
        self.assertEqual([self.completion("fail-3-times")[0] for _ in range(4)], [503, 503, 503, 200])
        self.assertEqual(self.request("/__control/reset", {"mode": "fast"})[0], 200)
        self.assertEqual(self.server.state()["requests"], 0)

    def test_global_request_budget_cannot_be_bypassed_by_reset(self):
        self.server.max_requests = 1
        self.assertEqual(self.completion()[0], 200)
        self.assertEqual(self.request("/__control/reset", {"mode": "fast"})[0], 200)
        self.assertEqual(self.completion()[0], 429)
        self.assertEqual(self.server.state()["lifetime_requests"], 1)

    def test_slow_disconnect_releases_inflight_and_blocks_reset_while_active(self):
        connection = http.client.HTTPConnection("127.0.0.1", self.server.server_port, timeout=5)
        connection.request("POST", "/v1/chat/completions", json.dumps({"model": "slow", "stream": True, "messages": []}), {"Content-Type": "application/json"})
        response = connection.getresponse()
        self.assertEqual(response.status, 200)
        self.assertEqual(self.request("/__control/reset", {"mode": "fast"})[0], 409)
        response.close()
        connection.close()
        deadline = time.monotonic() + 4
        while self.server.state()["inflight"] and time.monotonic() < deadline:
            time.sleep(0.05)
        self.assertEqual(self.server.state()["inflight"], 0)
        self.assertEqual(self.server.state()["calls"][0]["outcome"], "disconnected")

    def test_rejects_tools_unknown_models_and_overlong_control_delay(self):
        self.assertEqual(self.completion("public-provider-model")[0], 400)
        self.assertEqual(self.request("/v1/chat/completions", {"model": "fast", "tools": [{}]})[0], 400)
        self.assertEqual(self.request("/__control/reset", {"mode": "slow", "slow_seconds": 121})[0], 400)
        self.assertEqual(self.server.state()["requests"], 0)


if __name__ == "__main__":
    unittest.main()
