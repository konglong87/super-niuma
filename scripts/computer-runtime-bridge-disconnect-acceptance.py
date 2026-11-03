#!/usr/bin/env python3
"""Live macOS bridge-disconnect acceptance.

This deterministic acceptance starts a real host-owned Computer Use session and
observation, unlinks only the authenticated standalone runtime bridge socket,
verifies that status no longer reports stale ready authority, then asks the
parent desktop supervisor to Stop through its process-handle fallback. It sends
no input action and never starts a model run.
"""
import argparse
import importlib.util
import json
from pathlib import Path
import shlex
import subprocess
import time

SPEC = importlib.util.spec_from_file_location(
    "computer_acceptance", Path(__file__).with_name("computer-acceptance.py"))
DRIVER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(DRIVER)


def process_rows():
    raw = subprocess.check_output(
        ["ps", "-axo", "pid=,ppid=,command="], text=True, stderr=subprocess.DEVNULL)
    rows = []
    for line in raw.splitlines():
        fields = line.strip().split(None, 2)
        if len(fields) != 3:
            continue
        try:
            rows.append((int(fields[0]), int(fields[1]), fields[2]))
        except ValueError:
            continue
    return rows


def executable(command):
    try:
        return str(Path(shlex.split(command)[0]).resolve())
    except (IndexError, ValueError):
        return ""


def process_matches(predicate):
    return [pid for pid, _, command in process_rows() if predicate(pid, command)]


def wait_gone(predicate, timeout=6.0):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if not process_matches(predicate):
            return True
        time.sleep(0.1)
    return not process_matches(predicate)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--socket", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    if not args.socket.is_absolute() or not args.output.is_absolute():
        raise SystemExit("socket and output must be absolute")
    args.output.mkdir(parents=True, exist_ok=True, mode=0o700)

    driver = DRIVER.Driver(args.socket, args.output)
    report = {
        "kind": "live-macos-computer-runtime-bridge-disconnect",
        "passed": False,
        "input_actions_sent": 0,
        "steps": [],
    }
    try:
        started = driver.start()
        report["session_id"] = started["session_id"]
        report["steps"].append("start")
        before = driver.observe("bridge-before")
        report["before_observation_id"] = before["id"]
        report["steps"].append("observe_before_disconnect")

        disconnected = driver.call("runtime_disconnect_bridge")
        if disconnected.get("error") or disconnected.get("data", {}).get("state") != "bridge_disconnected":
            raise RuntimeError(f"bridge disconnect injection failed: {disconnected}")
        report["steps"].append("disconnect_bridge")

        status = driver.call("runtime_status")
        if not status.get("error"):
            raise RuntimeError(f"bridge still reported status after disconnect: {status}")
        report["runtime_status_after_disconnect"] = {"error": status.get("error")}
        report["steps"].append("status_disconnected")

        stopped = driver.call("runtime_stop")
        if stopped.get("error") or stopped.get("data", {}).get("state") != "stopped":
            raise RuntimeError(f"process-handle Stop fallback failed: {stopped}")
        report["stop"] = stopped.get("data")
        report["steps"].append("stop_runtime_fallback")

        workspace = Path(__file__).resolve().parents[1]
        helper = str((workspace / "desktop-v2/build/bin/niuma.app/Contents/Helpers/ComputerHelper.app/Contents/MacOS/computer-helper-macos").resolve())
        runtime = str((workspace / "desktop-v2/build/bin/niuma.app/Contents/MacOS/niuma-desktop").resolve())
        helper_gone = wait_gone(lambda _pid, command: executable(command) == helper)
        runtime_gone = wait_gone(lambda _pid, command: executable(command) == runtime and "--computer-runtime-process" in command)
        if not helper_gone or not runtime_gone:
            raise RuntimeError(f"process cleanup failed: helper_gone={helper_gone} runtime_gone={runtime_gone}")
        report["process_cleanup"] = {"helper_gone": helper_gone, "runtime_gone": runtime_gone}
        report["passed"] = True
    except Exception as error:
        report["error"] = str(error) or type(error).__name__
    finally:
        (args.output / "bridge-disconnect-results.json").write_text(
            json.dumps(report, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps(report, ensure_ascii=False, indent=2))
    raise SystemExit(0 if report["passed"] else 1)


if __name__ == "__main__":
    main()
