#!/usr/bin/env python3
"""Live macOS Computer Use helper-fault acceptance.

This is deterministic native acceptance, not a model run. It starts one host-owned
session, captures one real observation, kills exactly the bundled ComputerHelper
process, verifies the next observation returns a stable fault and a degraded
runtime status, then stops the session and runtime. It never sends input actions
and never persists screenshot bytes outside the requested evidence directory.
"""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import signal
import shlex
import subprocess
import time

SPEC = importlib.util.spec_from_file_location(
    "computer_acceptance", Path(__file__).with_name("computer-acceptance.py"))
DRIVER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(DRIVER)

FAULT_CODES = {"helper_unavailable", "helper_outcome_uncertain", "worker_exited"}
EXPECTED_HELPER = (Path(__file__).resolve().parents[1] /
                   "desktop-v2/build/bin/niuma.app/Contents/Helpers/ComputerHelper.app/Contents/MacOS/computer-helper-macos")


def process_rows():
    raw = subprocess.check_output(
        ["ps", "-axo", "pid=,ppid=,command="], text=True, stderr=subprocess.DEVNULL)
    rows = []
    for line in raw.splitlines():
        fields = line.strip().split(None, 2)
        if len(fields) != 3:
            continue
        try:
            pid, ppid = int(fields[0]), int(fields[1])
        except ValueError:
            continue
        rows.append((pid, ppid, fields[2]))
    return rows


def helper_pids():
    expected = str(EXPECTED_HELPER.resolve())
    matches = []
    for pid, _, command in process_rows():
        try:
            executable = str(Path(shlex.split(command)[0]).resolve())
        except (IndexError, ValueError):
            continue
        if executable == expected:
            matches.append(pid)
    return matches


def wait_until(predicate, timeout=8.0, interval=0.1):
    deadline = time.monotonic() + timeout
    last = None
    while time.monotonic() < deadline:
        last = predicate()
        if last:
            return last
        time.sleep(interval)
    return last


def call_status(driver):
    return driver.call("runtime_status")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--socket", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--helper-pid", type=int, default=0,
                        help="optional exact bundled helper PID; otherwise require one match")
    args = parser.parse_args()
    if not args.socket.is_absolute() or not args.output.is_absolute():
        raise SystemExit("socket and output must be absolute")
    args.output.mkdir(parents=True, exist_ok=True, mode=0o700)

    driver = DRIVER.Driver(args.socket, args.output)
    report = {
        "kind": "live-macos-computer-runtime-helper-fault",
        "passed": False,
        "input_actions_sent": 0,
        "steps": [],
    }
    session_active = False
    runtime_stop_requested = False
    try:
        started = driver.start()
        session_active = True
        report["session_id"] = started["session_id"]
        report["steps"].append("start")
        report["runtime_before"] = call_status(driver)

        before = driver.observe("fault-before")
        report["before_observation_id"] = before["id"]
        report["steps"].append("observe_before_fault")

        pids = helper_pids()
        target = args.helper_pid or (pids[0] if len(pids) == 1 else 0)
        if target == 0 or target not in pids:
            raise RuntimeError(f"expected exactly one bundled helper; found {pids}")
        report["helper_pid"] = target
        os.kill(target, signal.SIGKILL)
        report["steps"].append("kill_helper")

        fault_response = wait_until(
            lambda: driver.call("observe", session_id=driver.session), timeout=10.0)
        if not fault_response or not fault_response.get("error"):
            raise RuntimeError("post-kill observe did not return an explicit error")
        report["post_kill_observe"] = {
            "error": fault_response.get("error"),
            "data_present": bool(fault_response.get("data")),
        }
        report["steps"].append("observe_fault")

        status_response = wait_until(
            lambda: call_status(driver),
            timeout=5.0,
        )
        status = status_response.get("data", {}) if status_response else {}
        fault = status.get("fault") or {}
        if status.get("state") != "degraded" or fault.get("error_code") not in FAULT_CODES:
            raise RuntimeError(f"unexpected runtime fault status: {status_response}")
        report["runtime_after_fault"] = status
        report["steps"].append("runtime_status_fault")

        stopped = driver.call("stop", session_id=driver.session)
        if stopped.get("error") or stopped.get("data", {}).get("state") != "stopped":
            raise RuntimeError(f"session Stop did not converge: {stopped}")
        session_active = False
        report["stop"] = stopped.get("data")
        report["steps"].append("stop_session")

        runtime_stopped = driver.call("runtime_stop")
        if runtime_stopped.get("error"):
            raise RuntimeError(f"runtime Stop did not converge: {runtime_stopped}")
        runtime_stop_requested = True
        report["steps"].append("stop_runtime")
        if wait_until(lambda: target not in helper_pids(), timeout=5.0) is not True:
            raise RuntimeError("bundled helper remained alive after runtime Stop")
        report["passed"] = True
    except Exception as error:
        report["error"] = str(error) or type(error).__name__
    finally:
        if session_active and driver.session:
            try:
                driver.call("stop", session_id=driver.session)
            except Exception:
                pass
        if not runtime_stop_requested:
            try:
                driver.call("runtime_stop")
            except Exception:
                pass
        (args.output / "runtime-fault-results.json").write_text(
            json.dumps(report, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps(report, ensure_ascii=False, indent=2))
    raise SystemExit(0 if report["passed"] else 1)


if __name__ == "__main__":
    main()
