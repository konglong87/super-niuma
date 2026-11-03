#!/usr/bin/env python3
"""Opt-in same-app window test. Inputs use only the actual Wails Controller.

WindowFixture supplies geometry/counters, not an input API. This is deterministic
native acceptance, NOT a model-autonomy claim. Never target user application data.
"""
import argparse
import importlib.util
import json
from pathlib import Path
import time
import uuid

SPEC = importlib.util.spec_from_file_location("native_driver", Path(__file__).with_name("computer-acceptance.py"))
DRIVER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(DRIVER)
FIXTURE_BUNDLE = "com.niuma.validation.window-fixture"
WAIT_SECONDS = 2
POLL_SECONDS = 0.05


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--fixture", type=Path, required=True)
    parser.add_argument("--socket", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    driver = DRIVER.Driver(args.socket, args.output)
    results = {"kind": "scripted-Wails-native-window-acceptance", "steps": [], "passed": False}

    def state():
        value = json.loads(args.fixture.read_text())
        if value["coordinateSystem"] != "CG-global-top-left-points" or len(value["windows"]) != 2:
            raise RuntimeError("expected isolated two-window fixture")
        return value

    def counters():
        return {w["key"]: w["clickCount"] for w in state()["windows"]}

    def target_observe(key, label):
        fixture = state()
        window = next(w for w in fixture["windows"] if w["key"] == key)
        caps = driver.require(driver.call("capabilities"))
        native = next(w for w in caps["windows"] if w.get("id") == str(window["windowNumber"]))
        if native.get("owner_pid") != fixture["processID"] or native.get("bundle_id") != FIXTURE_BUNDLE:
            raise RuntimeError("refusing input: target is not the isolated fixture")
        obs = driver.observe(label, window_id=native["id"])
        if obs.get("window_id") != native["id"] or obs.get("active_window", {}).get("id") != native["id"]:
            raise RuntimeError("exact window was not activated")
        return obs, window

    try:
        baseline = counters()
        driver.start()
        expected = dict(baseline)
        for index, key in enumerate(["A", "B", "A"]):
            label = f"{index+1:02d}-{key}"
            observation, window = target_observe(key, label + "-before")
            geometry = observation["capabilities"]["coordinate_space"]
            bounds, button = geometry["bounds"], window["buttonScreenFrame"]
            scale = geometry["scale_factor"]
            point = {"x": round((button["x"] + button["width"] / 2 - bounds["x"]) * scale),
                     "y": round((button["y"] + button["height"] / 2 - bounds["y"]) * scale)}
            if not (0 <= point["x"] < observation["width"] and 0 <= point["y"] < observation["height"]):
                raise RuntimeError("fixture button lies outside captured target")
            receipt = driver.execute({"kind": "click", "window_id": observation["window_id"], "point": point}, observation["id"], label)
            if receipt.get("outcome") != "executed":
                raise RuntimeError("input outcome not confirmed; no retry")
            expected[key] += 1
            until = time.monotonic() + WAIT_SECONDS
            while counters() != expected and time.monotonic() < until:
                time.sleep(POLL_SECONDS)
            actual = counters()
            results["steps"].append({"window": key, "id": observation["window_id"], "expected": dict(expected), "actual": actual})
            if actual != expected:
                raise RuntimeError("wrong window received click; no retry")
            driver.observe(label + "-verified", window_id=observation["window_id"])
        # Observing a different same-app target must invalidate the previous image.
        old, _ = target_observe("A", "04-old-A")
        target_observe("B", "05-new-B")
        denied = driver.call("execute", session_id=driver.session, action={
            "id": str(uuid.uuid4()), "session_id": driver.session, "observation_id": old["id"],
            "window_id": old["window_id"], "kind": "move", "point": {"x": 10, "y": 10}})
        results["stale_target"] = denied
        if not denied.get("error") or denied.get("data", {}).get("outcome") != "rejected" or counters() != expected:
            raise RuntimeError("stale target input was not rejected")
        results["passed"] = True
    except Exception as error:
        results["error"] = str(error) or type(error).__name__
    finally:
        if driver.session:
            try:
                stopped = driver.call("stop", session_id=driver.session)
                results["stop_confirmed"] = not stopped.get("error") and stopped.get("data", {}).get("state") == "stopped"
            except Exception as error:
                results["stop_confirmed"] = False
                results["stop_error"] = str(error)
            results["passed"] = results["passed"] and results["stop_confirmed"]
        args.output.mkdir(parents=True, exist_ok=True)
        (args.output / "results.json").write_text(json.dumps(results, indent=2))
    print(json.dumps(results, indent=2))
    return 0 if results["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
