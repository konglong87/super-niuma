#!/usr/bin/env python3
"""Verify Playwright opt-in and URL refusals at config-load time (no server/browser/network)."""
import os
from pathlib import Path
import subprocess
import unittest

WEB = Path(__file__).resolve().parents[1] / "web"


class IntegrationGateTest(unittest.TestCase):
    def invoke(self, enabled="1", backend="http://127.0.0.1:65533", provider="http://127.0.0.1:65534", web_port="65532"):
        env = dict(os.environ)
        for prefix in ("NIUMA_", "GOLANG_CLAUDE_CODE_"):
            for suffix in ("WORKFLOW_INTEGRATION_E2E", "WEBUI_LIVE_API_BASE", "WORKFLOW_OFFLINE_PROVIDER_BASE", "WEBUI_E2E_PORT"):
                env.pop(prefix + suffix, None)
        env.update({"NPM_CONFIG_OFFLINE": "true", "NPM_CONFIG_UPDATE_NOTIFIER": "false", "npm_config_offline": "true", "npm_config_update_notifier": "false", "NIUMA_WORKFLOW_INTEGRATION_E2E": enabled, "NIUMA_WEBUI_LIVE_API_BASE": backend, "NIUMA_WORKFLOW_OFFLINE_PROVIDER_BASE": provider, "NIUMA_WEBUI_E2E_PORT": web_port})
        return subprocess.run([str(WEB / "node_modules/.bin/playwright"), "test", "--list", "--project=chromium-workflow-integrated"], cwd=WEB, env=env, capture_output=True, text=True, timeout=60)

    def test_default_and_non_exact_optin_do_not_register_the_project(self):
        for value in ("", "0", "true"):
            result = self.invoke(enabled=value)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("not found", result.stdout + result.stderr)

    def test_explicit_loopback_optin_lists_only_the_five_real_acceptance_scenarios(self):
        result = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Total: 5 tests in 1 file", result.stdout)
        self.assertNotIn("workflows-v2.spec.ts", result.stdout)

    def test_refuses_public_dns_aliases_credentials_queries_and_bad_ports(self):
        for unsafe in ("http://example.test:1234", "http://localhost:1234", "http://2130706433:1234", "http://127.0.0.1:1234?token=dummy", "http://dummy@127.0.0.1:1234", "http://127.0.0.1:65536"):
            result = self.invoke(backend=unsafe)
            self.assertNotEqual(result.returncode, 0)
        result = self.invoke(provider="https://example.test:1234")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("NIUMA_WORKFLOW_OFFLINE_PROVIDER_BASE", result.stderr)
        self.assertNotEqual(self.invoke(web_port="5131; echo unsafe").returncode, 0)


if __name__ == "__main__":
    unittest.main()
