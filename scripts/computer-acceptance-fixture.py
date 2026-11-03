#!/usr/bin/env python3
"""Validate trusted fixture events produced through niuma's native host.

Requires a running fixture and an explicit screenshot-derived calibration point.
This runner never generates DOM events or uses another desktop automation API.
"""
import argparse
import importlib.util
import json
from pathlib import Path
import time
import urllib.request

TEST_TEXT = "niuma 中文🙂"
TEST_TEXT_UTF16_LENGTH = len(TEST_TEXT.encode("utf-16-le")) // 2

spec = importlib.util.spec_from_file_location("native_driver", Path(__file__).with_name("computer-acceptance.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)

class FixtureRun:
    def __init__(self, connection, driver):
        self.driver = driver
        base, token = json.loads(Path(connection).read_text())["url"].split("#")
        self.request = urllib.request.Request(base + "snapshot", headers={"X-Fixture-Token": token})
        self.results = []

    def snapshot(self):
        with urllib.request.urlopen(self.request, timeout=3) as response:
            return json.load(response)

    def wait_for(self, predicate):
        deadline = time.monotonic() + 4
        while time.monotonic() < deadline:
            snap = self.snapshot()
            if snap["dropped"] or snap["clientDropped"]:
                raise RuntimeError("fixture lost events; acceptance evidence incomplete")
            if predicate(snap):
                return snap
            time.sleep(.1)
        raise AssertionError("expected fixture effect was not observed")

    def point(self, target):
        g = self.snapshot()["geometry"]
        c = g["calibration"]
        if not c:
            raise RuntimeError("trusted pointer calibration required")
        center = next(t["center"] for t in g["targets"] if t["id"] == target)
        # Primary single-screen acceptance at screenshot scale from the backend,
        # not a guessed browser title-bar offset or untrusted DPR assumption.
        scale = self.driver.require(self.driver.call("capabilities"))["coordinate_space"]["scale_factor"]
        x = round((center["x"] + c["screen"]["x"] - c["client"]["x"]) * scale)
        y = round((center["y"] + c["screen"]["y"] - c["client"]["y"]) * scale)
        return {"x": x, "y": y}

    def case(self, name, action, event_type, target, effect=lambda _: True):
        baseline = self.snapshot()["totalEvents"]
        receipt = self.driver.step(action, "fixture-" + name)
        if receipt["outcome"] != "executed":
            raise AssertionError(receipt)
        result = self.wait_for(lambda s: effect(s) and any(e["sequence"] > baseline and e["type"] == event_type and e["target"] == target and e["isTrusted"] for e in s["events"]))
        events = [e for e in result["events"] if e["sequence"] > baseline]
        if "point" in action:
            scale = self.driver.require(self.driver.call("capabilities"))["coordinate_space"]["scale_factor"]
            matched = [e for e in events if e["type"] == event_type and e["target"] == target and e["isTrusted"]]
            if not any(abs(e["screen"]["x"] * scale - action["point"]["x"]) <= 2 and abs(e["screen"]["y"] * scale - action["point"]["y"]) <= 2 for e in matched):
                raise AssertionError("trusted pointer coordinates differ from screenshot-pixel target")
        self.results.append({"case": name, "passed": True, "action_id": receipt["action_id"], "events": events, "state": result["state"]})
        self.save()
        print("PASS", name, flush=True)

    def drag_case(self):
        baseline = self.snapshot()["totalEvents"]
        start = self.point("drag-source")
        end = self.point("drag-drop-target")
        receipt = self.driver.step({"kind": "drag", "start_point": start, "point": end, "duration_ms": 320}, "fixture-drag")
        if receipt["outcome"] != "executed":
            raise AssertionError(receipt)
        result = self.wait_for(lambda s: s["state"]["dragCompleted"] and
            any(e["sequence"] > baseline and e["type"] == "mousedown" and e["target"] == "drag-source" and e["isTrusted"] for e in s["events"]) and
            any(e["sequence"] > baseline and e["type"] == "mouseup" and e["target"] == "drag-drop-target" and e["isTrusted"] for e in s["events"]))
        events = [e for e in result["events"] if e["sequence"] > baseline]
        scale = self.driver.require(self.driver.call("capabilities"))["coordinate_space"]["scale_factor"]
        downs = [e for e in events if e["type"] == "mousedown" and e["target"] == "drag-source" and e["isTrusted"]]
        ups = [e for e in events if e["type"] == "mouseup" and e["target"] == "drag-drop-target" and e["isTrusted"]]
        if not downs or not ups:
            raise AssertionError("trusted drag endpoints were not observed")
        if abs(downs[-1]["screen"]["x"] * scale - start["x"]) > 2 or abs(downs[-1]["screen"]["y"] * scale - start["y"]) > 2:
            raise AssertionError("drag start coordinate mismatch")
        if abs(ups[-1]["screen"]["x"] * scale - end["x"]) > 2 or abs(ups[-1]["screen"]["y"] * scale - end["y"]) > 2:
            raise AssertionError("drag end coordinate mismatch")
        self.results.append({"case": "drag", "passed": True, "action_id": receipt["action_id"], "events": events, "state": result["state"]})
        self.save()
        print("PASS drag", flush=True)

    def save(self):
        (self.driver.output / "fixture-results.json").write_text(json.dumps(self.results, ensure_ascii=False, indent=2))

    def run(self, x, y):
        self.driver.step({"kind": "move", "point": {"x": x, "y": y}}, "fixture-calibrate")
        self.wait_for(lambda s: s["geometry"]["calibration"] is not None)
        for kind, event, target in [("click", "click", "click-target"), ("double_click", "dblclick", "double-click-target"), ("right_click", "contextmenu", "context-menu-target"), ("move", "mousemove", "mouse-move-target")]:
            self.case(kind, {"kind": kind, "point": self.point(target)}, event, target)
        self.drag_case()
        self.driver.step({"kind": "move", "point": self.point("scroll-target")})
        baseline = self.snapshot()["state"]["scrollTop"]
        self.case("scroll", {"kind": "scroll", "delta_y": -420}, "wheel", "scroll-target", lambda s: s["state"]["scrollTop"] > baseline)
        self.case("focus-input", {"kind": "click", "point": self.point("text-target")}, "click", "text-target")
        self.driver.step({"kind": "hotkey", "keys": ["command", "a"]})
        self.case("unicode", {"kind": "type", "text": TEST_TEXT}, "input", "text-target", lambda s: s["state"]["inputValue"] == TEST_TEXT)
        self.case("select-all", {"kind": "hotkey", "keys": ["command", "a"]}, "keydown", "text-target", lambda s: s["state"]["selectionStart"] == 0 and s["state"]["selectionEnd"] == TEST_TEXT_UTF16_LENGTH)
        self.case("replace-selection", {"kind": "type", "text": "验收ABC"}, "input", "text-target", lambda s: s["state"]["inputValue"] == "验收ABC")
        self.case("backspace", {"kind": "key", "key": "backspace"}, "input", "text-target", lambda s: s["state"]["inputValue"] == "验收AB")
        self.case("arrow-left", {"kind": "key", "key": "left"}, "keydown", "text-target", lambda s: s["state"]["selectionStart"] == 3)
        self.driver.observe("fixture-final")

def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--connection", type=Path, default=Path.home() / ".niuma/computer-acceptance/fixture.json")
    p.add_argument("--socket", type=Path, default=module.DEFAULT_SOCKET)
    p.add_argument("--output", type=Path, default=module.DEFAULT_OUTPUT)
    p.add_argument("--calibrate-x", type=int, required=True)
    p.add_argument("--calibrate-y", type=int, required=True)
    args = p.parse_args()
    FixtureRun(args.connection, module.Driver(args.socket, args.output)).run(args.calibrate_x, args.calibrate_y)

if __name__ == "__main__":
    main()
