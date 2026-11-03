#!/usr/bin/env python3
"""Bounded real SQLite/backend/browser acceptance using ONLY a local fake provider.

Requires a locally built niuma binary; does not download/build/install anything.
Creates isolated HOME/config/SQLite/settings/logs, disables scheduling and update,
uses dummy credentials, then deletes all temporary state on success AND failure.
Browser screenshots persist under desktop-v2/build/validation/YYYYMMDD/.

  python3 scripts/run-workflow-integrated.py --binary /path/to/niuma
  python3 scripts/test-workflow-offline-provider.py
  python3 scripts/test-workflow-integration-gate.py

Manual browser project (both endpoints MUST be literal loopback; default is off):
  NIUMA_WORKFLOW_INTEGRATION_E2E=1
  NIUMA_WEBUI_LIVE_API_BASE=http://127.0.0.1:<isolated-backend-port>
  NIUMA_WORKFLOW_OFFLINE_PROVIDER_BASE=http://127.0.0.1:<offline-provider-port>
  ./node_modules/.bin/playwright test --project=chromium-workflow-integrated --workers=1

The project refuses nonempty customer Profile/Workflow databases and checks both
resolved settings and the process startup route before creating anything. It does
not intercept any business API. This runner retains only requested browser PNGs;
SQLite proof/counter summary is printed without payloads. Native Wails clicks are
intentionally the caller's next acceptance gate, not claimed by this script.
"""
import argparse
import datetime
import contextlib
import importlib.util
import json
import sys
import os
from pathlib import Path
import signal
import socket
import sqlite3
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
OFFLINE = ROOT / "scripts/workflow-offline-provider.py"
sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location("offline_fixture", OFFLINE)
fixture = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fixture)
HTTP = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def stop(process):
    if process and process.poll() is None:
        os.killpg(process.pid, signal.SIGTERM)
        try:
            process.wait(timeout=8)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGKILL)
            process.wait(timeout=3)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True, type=Path)
    parser.add_argument("--timeout", type=int, default=240)
    args = parser.parse_args()
    binary = args.binary.resolve()
    if not binary.is_file() or not 30 <= args.timeout <= 600:
        parser.error("a local binary and a timeout between 30 and 600 seconds are required")
    for signum in (signal.SIGINT, signal.SIGTERM):
        signal.signal(signum, lambda *_: (_ for _ in ()).throw(KeyboardInterrupt()))
    provider = fixture.OfflineServer()
    provider_thread = threading.Thread(target=provider.serve_forever, daemon=True)
    provider_thread.start()
    backend = browser = None
    try:
        with tempfile.TemporaryDirectory(prefix="niuma-workflow-integrated-") as directory:
            work = Path(directory)
            for name in ("home", "config", "workspace", "temp"):
                (work / name).mkdir()
            settings = work / "config/settings.json"
            settings.write_text(json.dumps(fixture.fixture_settings(f"http://127.0.0.1:{provider.server_port}/v1")))
            settings.chmod(0o600)
            # Deliberately NOT os.environ: ambient provider secrets/proxies/plugins must not enter the backend.
            env = {"PATH": "/usr/bin:/bin:/usr/sbin:/sbin", "HOME": str(work / "home"), "TMPDIR": str(work / "temp"), "LANG": "en_US.UTF-8", "NO_PROXY": "127.0.0.1,::1", "NIUMA_SQLITE_PATH": str(work / "workflow.sqlite"), "NIUMA_TENANT_KEY": "webui-local", "NIUMA_USER_ID": "webui-local-user", "NIUMA_CONFIG_DIR": str(work / "config"), "NIUMA_DESKTOP_SESSION_BACKEND": "jsonl", "NIUMA_DISABLE_SCHEDULER": "1"}
            api_port, web_port = free_port(), free_port()
            base = f"http://127.0.0.1:{api_port}"
            with (work / "backend.log").open("w") as log, contextlib.ExitStack() as cleanup:
                cleanup.callback(lambda: stop(backend))
                cleanup.callback(lambda: stop(browser))
                backend = subprocess.Popen([str(binary), "--cwd", str(work / "workspace"), "--settings", str(settings), "server", "--host", "127.0.0.1", "--port", str(api_port), "--auth-token", "offline-auth", "--desktop-local"], env=env, stdout=log, stderr=log, start_new_session=True)
                deadline = time.monotonic() + 30
                while time.monotonic() < deadline:
                    if backend.poll() is not None:
                        # Never dump raw server logs/settings: synthetic diagnostics only.
                        raise RuntimeError(f"isolated backend exited with code {backend.returncode} before readiness")
                    try:
                        request = urllib.request.Request(base + "/health", headers={"Authorization": "Bearer offline-auth"})
                        with HTTP.open(request, timeout=1) as response:
                            if response.status == 200:
                                break
                    except (OSError, urllib.error.URLError):
                        time.sleep(0.15)
                else:
                    raise RuntimeError("isolated backend did not become healthy within 30 seconds")
                browser_env = dict(os.environ)
                browser_env.update({"NPM_CONFIG_OFFLINE": "true", "NPM_CONFIG_UPDATE_NOTIFIER": "false", "npm_config_offline": "true", "npm_config_update_notifier": "false", "NIUMA_WORKFLOW_INTEGRATION_E2E": "1", "NIUMA_WEBUI_LIVE_API_BASE": base, "NIUMA_WORKFLOW_OFFLINE_PROVIDER_BASE": f"http://127.0.0.1:{provider.server_port}", "NIUMA_WEBUI_E2E_PORT": str(web_port), "NIUMA_WEBUI_E2E_REUSE_SERVER": "0", "NIUMA_WORKFLOW_EVIDENCE_DATE": datetime.date.today().strftime("%Y%m%d"), "VITE_DESKTOP_UI_VERSION": ""})
                print(f"Isolated backend {base}; offline provider http://127.0.0.1:{provider.server_port}; temporary state will be removed", flush=True)
                browser = subprocess.Popen([str(ROOT / "web/node_modules/.bin/playwright"), "test", "e2e/workflows-integrated.spec.ts", "--project=chromium-workflow-integrated", "--workers=1"], cwd=ROOT / "web", env=browser_env, start_new_session=True)
                try:
                    code = browser.wait(timeout=args.timeout)
                finally:
                    stop(browser)
                    stop(backend)
                with sqlite3.connect(work / "workflow.sqlite") as database:
                    tables = ("agent_team_runs", "agent_team_workflow_nodes", "agent_team_workflow_attempts", "agent_team_workflow_artifacts")
                    persisted = {table: database.execute(f"SELECT COUNT(*) FROM {table}").fetchone()[0] for table in tables}
                    persisted["run_statuses"] = dict(database.execute("SELECT status, COUNT(*) FROM agent_team_runs GROUP BY status").fetchall())
                state = provider.state()
                print(json.dumps({"offline_provider": {"requests": state["lifetime_requests"], "max_inflight": state["lifetime_max_inflight"]}, "sqlite_rows": persisted, "browser_exit": code, "temporary_state": "removed on context exit"}), flush=True)
                return code
    finally:
        stop(browser)
        stop(backend)
        provider.shutdown()
        provider.server_close()
        provider_thread.join(timeout=3)


if __name__ == "__main__":
    raise SystemExit(main())
