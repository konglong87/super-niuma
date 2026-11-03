#!/usr/bin/env python3
"""Opt-in safety acceptance against the actual running niuma host/helper.

Wait interruptions do not exercise a held mouse/key. Permission revocation is
not performed here: it requires a separate user-controlled OS authorization run.
"""
import argparse
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timedelta
import importlib.util
import json
import math
import os
from pathlib import Path
import re
import signal
import subprocess
import time
import uuid

HOST_EXECUTABLE = "MacOS/niuma-desktop"
HELPER_EXECUTABLE = "Helpers/ComputerHelper.app/Contents/MacOS/computer-helper-macos"
DANGEROUS_SENTINEL = "MUST_NOT_APPEAR"
INPUT_SETTLE_SECONDS = 0.3
INTERRUPT_DELAY_SECONDS = 0.25
INFLIGHT_WAIT_MS = 5000
INFLIGHT_RESULT_TIMEOUT_SECONDS = 3
INTERRUPT_LIMIT_SECONDS = 2
EXPIRY_GUARD_SECONDS = 0.1
# Harness deadline only, NOT a replacement for expires_at. Allow the current
# 120-second native TTL plus clock/transport margin; always wait for the actual
# returned expiry before dispatch. Reject unreasonably distant metadata.
MAX_EXPIRY_FUTURE_SECONDS = 180
CLOCK_ADJUSTMENT_BUDGET_SECONDS = 2
EXPIRY_POLL_SECONDS = 0.25
RFC3339_EXPIRY = re.compile(
    r"[0-9]{4}-[0-9]{2}-[0-9]{2}[Tt][0-9]{2}:[0-9]{2}:[0-9]{2}"
    r"(?:\.[0-9]+)?(?:[Zz]|[+-](?:[01][0-9]|2[0-3]):[0-5][0-9])"
)


class ExpiryWaitError(ValueError):
    """Expiry cannot be established safely; no input may be dispatched."""


def wait_for_observation_expiry(observation, *, wall_clock=time.time,
                                monotonic=time.monotonic, sleep=time.sleep):
    """Wait for actual expiry, conservatively across wall-clock adjustments.

    A forward jump cannot shorten the initial monotonic wait. A backward jump
    may extend it only by the fixed adjustment budget, then fails closed.
    """
    raw = observation.get("expires_at") if isinstance(observation, dict) else None
    if not isinstance(raw, str) or not RFC3339_EXPIRY.fullmatch(raw):
        raise ExpiryWaitError("expires_at must be a timezone-aware RFC3339 timestamp")
    try:
        # Python 3.9 accepts only 3/6 fractional digits. Normalize to microseconds
        # and round UP any discarded precision so expiry is never understated.
        fraction = re.search(r"\.([0-9]+)", raw)
        normalized = re.sub(r"\.([0-9]+)",
                            lambda m: "." + (m[1] + "000000")[:6], raw)
        parsed = datetime.fromisoformat(normalized.upper().replace("Z", "+00:00"))
        if fraction and any(digit != "0" for digit in fraction[1][6:]):
            parsed += timedelta(microseconds=1)
        expiry = parsed.timestamp()
    except (ValueError, OverflowError, OSError) as exc:
        raise ExpiryWaitError("malformed expires_at") from exc
    if not math.isfinite(expiry):
        raise ExpiryWaitError("nonfinite expires_at")

    def clocks():
        wall, mono = wall_clock(), monotonic()
        if not math.isfinite(wall) or not math.isfinite(mono):
            raise ExpiryWaitError("nonfinite clock reading")
        return wall, mono

    wall, mono = clocks()
    if expiry - wall > MAX_EXPIRY_FUTURE_SECONDS:
        raise ExpiryWaitError("expires_at is unreasonably far in the future")
    target = math.nextafter(expiry + EXPIRY_GUARD_SECONDS, math.inf)
    earliest = mono + max(0, target - wall, expiry - wall + EXPIRY_GUARD_SECONDS)
    deadline = earliest + CLOCK_ADJUSTMENT_BUDGET_SECONDS
    previous = mono
    while True:
        if mono > deadline:
            raise ExpiryWaitError("expiry wait exceeded monotonic deadline")
        if wall >= target and mono >= earliest:
            return
        if mono >= deadline:
            raise ExpiryWaitError("expiry wait exceeded monotonic deadline")
        delay = min(EXPIRY_POLL_SECONDS, deadline - mono,
                    max(target - wall, earliest - mono))
        sleep(delay)
        wall, mono = clocks()
        if mono <= previous:
            raise ExpiryWaitError("monotonic clock did not advance")
        previous = mono


def require_app_path(app_path):
    """Validate explicit crash scope before touching any native session."""
    if app_path is None:
        raise ValueError("--crash-helper requires explicit --app-path")
    app = Path(app_path).expanduser().resolve(strict=True)
    if app.suffix != ".app" or not app.is_dir():
        raise ValueError("--app-path must identify an existing .app directory")
    for executable in (HOST_EXECUTABLE, HELPER_EXECUTABLE):
        if not (app / "Contents" / executable).is_file():
            raise ValueError("--app-path is missing a required host/helper executable")
    return app


def select_helper_pid(app_path, process_table):
    """Select only the explicit bundle's unique host child; never global matching."""
    contents = require_app_path(app_path) / "Contents"
    host_path = str(contents / HOST_EXECUTABLE)
    helper_path = str(contents / HELPER_EXECUTABLE)
    rows = [row.strip().split(None, 2) for row in process_table.splitlines()]
    hosts = [int(row[0]) for row in rows if len(row) == 3 and row[2] == host_path]
    if len(hosts) != 1:
        raise RuntimeError("refusing crash injection: expected exactly one host for --app-path")
    helpers = [int(row[0]) for row in rows
               if len(row) == 3 and row[2] == helper_path and int(row[1]) == hosts[0]]
    if len(helpers) != 1 or helpers[0] <= 0 or hosts[0] <= 0:
        raise RuntimeError("refusing crash injection: expected exactly one child of this desktop host")
    return helpers[0]


spec = importlib.util.spec_from_file_location("fixture_runner", Path(__file__).with_name("computer-acceptance-fixture.py"))
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)

class SafetyRun:
    def __init__(self, fixture_file, app_path=None):
        self.app_path = require_app_path(app_path) if app_path is not None else None
        self.d = mod.module.Driver()
        self.fixture = mod.FixtureRun(fixture_file, self.d)
        self.results = []

    def record(self, name, passed, **data):
        self.results.append(dict(case=name, passed=passed, **data))
        (self.d.output / "safety-results.json").write_text(json.dumps(self.results, ensure_ascii=False, indent=2))
        print("PASS" if passed else "FAIL", name, flush=True)

    def restart(self):
        if self.d.session:
            self.d.call("stop", session_id=self.d.session)
        self.d.start()

    def action(self, obs, **fields):
        return dict(id=str(uuid.uuid4()), session_id=self.d.session, observation_id=obs["id"], **fields)

    def denied(self, action):
        baseline = self.fixture.snapshot()["state"]["inputValue"]
        r = self.d.call("execute", session_id=self.d.session, action=action)
        time.sleep(INPUT_SETTLE_SECONDS)
        unchanged = self.fixture.snapshot()["state"]["inputValue"] == baseline
        return r, unchanged and bool(r.get("error")) and r.get("data", {}).get("outcome") == "rejected"

    def expired(self, *, wall_clock=time.time, monotonic=time.monotonic, sleep=time.sleep):
        self.restart()
        obs = self.d.observe("safety-expired-before")
        try:
            wait_for_observation_expiry(obs, wall_clock=wall_clock,
                                        monotonic=monotonic, sleep=sleep)
        except ExpiryWaitError as exc:
            self.record("expired-observation", False, error=str(exc))
            raise
        r, passed = self.denied(self.action(obs, kind="type", text=DANGEROUS_SENTINEL))
        self.record("expired-observation", passed, response=r)

    def stale(self):
        self.restart()
        old = self.d.observe()
        self.d.observe()
        r, passed = self.denied(self.action(old, kind="type", text=DANGEROUS_SENTINEL))
        self.record("superseded-observation", passed, response=r)

    def interrupted(self, control):
        self.restart()
        obs = self.d.observe()
        action = self.action(obs, kind="wait", duration_ms=INFLIGHT_WAIT_MS)
        with ThreadPoolExecutor(max_workers=1) as pool:
            future = pool.submit(self.d.call, "execute", session_id=self.d.session, action=action)
            time.sleep(INTERRUPT_DELAY_SECONDS)
            began = time.monotonic()
            response = self.d.call(control, session_id=self.d.session)
            inflight = future.result(timeout=INFLIGHT_RESULT_TIMEOUT_SECONDS)
            elapsed = time.monotonic() - began
        snapshot = self.d.control("snapshot")
        next_response, denied = self.denied(self.action(obs, kind="type", text=DANGEROUS_SENTINEL))
        wanted = "paused" if control == "pause" else "stopped"
        safe = snapshot["state"] == wanted and elapsed < INTERRUPT_LIMIT_SECONDS and denied and inflight.get("data", {}).get("outcome") != "executed"
        self.record(control + "-during-wait", safe, elapsed_seconds=elapsed, control_response=response,
                    action_response=inflight, next_response=next_response, state=snapshot["state"])
        if control == "pause":
            resume = self.d.call("resume", session_id=self.d.session)
            observed = self.d.call("observe", session_id=self.d.session) if not resume.get("error") else {}
            observed.pop("data", None) # no PNGs in this summary
            self.record("resume-after-interrupted-wait", not resume.get("error") and not observed.get("error"), response=resume, observe_error=observed.get("error"))

    def crash_helper_pid(self):
        app = require_app_path(self.app_path)
        rows = subprocess.check_output(["ps", "-axo", "pid,ppid,comm"], text=True)
        return select_helper_pid(app, rows)

    def crash(self):
        require_app_path(self.app_path)
        # The preceding Stop case legitimately closes its helper. Start this
        # case's session before selecting its unique helper; still validate the
        # exact app/parent relationship before any input and again before kill.
        self.restart()
        self.crash_helper_pid()
        obs = self.d.observe()
        with ThreadPoolExecutor(max_workers=1) as pool:
            future = pool.submit(self.d.call, "execute", session_id=self.d.session, action=self.action(obs, kind="wait", duration_ms=INFLIGHT_WAIT_MS))
            time.sleep(INTERRUPT_DELAY_SECONDS)
            os.kill(self.crash_helper_pid(), signal.SIGKILL)
            response = future.result(timeout=INFLIGHT_RESULT_TIMEOUT_SECONDS)
        later = self.d.call("observe", session_id=self.d.session)
        later.pop("data", None)
        safe = bool(response.get("error")) and response.get("data", {}).get("outcome") == "unknown" and bool(later.get("error"))
        self.record("native-helper-crash", safe, response=response, later_response=later)
        self.restart()
        recovery = self.d.observe("safety-crash-recovered")
        self.record("new-session-after-helper-crash", bool(recovery.get("id")))

    def run(self, crash):
        if crash:
            self.crash_helper_pid()  # Preflight before any safety case can dispatch input.
        try:
            self.expired(); self.stale(); self.interrupted("pause"); self.interrupted("stop")
            if crash:
                self.crash()
        finally:
            self.d.call("stop", session_id=self.d.session)

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--connection", type=Path, default=Path.home() / ".niuma/computer-acceptance/fixture.json")
    parser.add_argument("--crash-helper", action="store_true", help="explicitly kill only this host's bundled helper during wait")
    parser.add_argument("--app-path", type=Path,
                        help="exact .app bundle to target; required with --crash-helper")
    args = parser.parse_args()
    if args.crash_helper or args.app_path is not None:
        try:
            require_app_path(args.app_path)
        except (ValueError, OSError) as exc:
            parser.error(str(exc))
    runner = SafetyRun(args.connection, app_path=args.app_path)
    runner.run(args.crash_helper)
    if any(not case["passed"] for case in runner.results):
        raise SystemExit("safety acceptance has failing cases; inspect safety-results.json")

if __name__ == "__main__":
    main()
