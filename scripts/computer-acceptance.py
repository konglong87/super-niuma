#!/usr/bin/env python3
"""Opt-in real-input driver for the tagged niuma desktop acceptance build.

No OS input APIs, AppleScript, browser automation or shell launch is used here.
All observations/actions go through the running Wails host's approved Controller.
This is a scripted acceptance client, NOT evidence of production LLM routing.
"""
import argparse
import base64
import http.client
import json
import os
from pathlib import Path
import socket
import time
import uuid

DEFAULT_SOCKET = Path.home() / ".niuma/computer-acceptance/control.sock"
DEFAULT_OUTPUT = Path(__file__).resolve().parents[1] / "desktop-v2/build/validation" / time.strftime("%Y%m%d") / "native-input"

class UnixHTTP(http.client.HTTPConnection):
    def __init__(self, path):
        super().__init__("localhost", timeout=16)
        self.path = str(path)

    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(self.timeout)
        self.sock.connect(self.path)

class Driver:
    def __init__(self, socket_path=DEFAULT_SOCKET, output=DEFAULT_OUTPUT):
        self.socket_path = Path(socket_path)
        self.output = Path(output)
        self.output.mkdir(parents=True, exist_ok=True, mode=0o700)
        self.state_path = self.socket_path.parent / "session.json"
        self.session = json.loads(self.state_path.read_text()).get("session_id", "") if self.state_path.exists() else ""

    def call(self, op, **fields):
        conn = UnixHTTP(self.socket_path)
        try:
            conn.request("POST", "/command", json.dumps({"op": op, **fields}),
                         {"X-Go-E2E-Acceptance": "1", "Content-Type": "application/json"})
            resp = conn.getresponse()
            data = resp.read()
            if resp.status != 200:
                raise RuntimeError(f"acceptance HTTP {resp.status}: {data[:200]!r}")
            return json.loads(data)
        finally:
            conn.close()

    def require(self, response):
        if response.get("error"):
            raise RuntimeError(response["error"])
        return response.get("data")

    def start(self):
        data = self.require(self.call("start", approved=True))
        self.session = data["session_id"]
        fd = os.open(self.state_path, os.O_CREAT | os.O_TRUNC | os.O_WRONLY, 0o600)
        with os.fdopen(fd, "w") as out:
            json.dump({"session_id": self.session}, out)
        return data

    def image(self, encoded, label):
        if not label or Path(label).name != label:
            raise ValueError("evidence label must be a filename component")
        path = self.output / (label + ".png")
        path.write_bytes(base64.b64decode(encoded, validate=True))
        return str(path)

    def observe(self, label=None, display_id="", window_id=""):
        fields = {"session_id": self.session}
        if display_id: fields["display_id"] = display_id
        if window_id: fields["window_id"] = window_id
        data = self.require(self.call("observe", **fields))
        if label:
            self.image(data.pop("image_data"), label)
            (self.output / (label + ".json")).write_text(json.dumps(data, ensure_ascii=False, indent=2))
        else:
            data.pop("image_data", None)
        return data["observation"]

    def execute(self, action, observation_id, label=None):
        request = dict(action, id=str(uuid.uuid4()), session_id=self.session, observation_id=observation_id)
        response = self.call("execute", session_id=self.session, action=request)
        # Receipts are already redacted by the domain. Never persist typed text.
        if label:
            (self.output / (label + "-receipt.json")).write_text(json.dumps(response, ensure_ascii=False, indent=2))
        receipt = self.require(response)
        if label and receipt.get("after_observation_id"):
            data = self.require(self.call("image", session_id=self.session, observation_id=receipt["after_observation_id"]))
            self.image(data["image_data"], label + "-after")
        return receipt

    def step(self, action, label=None):
        observation = self.observe(label + "-before" if label else None)
        return self.execute(action, observation["id"], label)

    def control(self, op):
        return self.require(self.call(op, session_id=self.session))

def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--socket", type=Path, default=DEFAULT_SOCKET)
    p.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    p.add_argument("--label", default=None)
    p.add_argument("op", choices=["start", "capabilities", "runtime_status", "observe", "step", "pause", "resume", "stop", "snapshot"])
    p.add_argument("action", nargs="?", help="Structured native Action JSON for step")
    args = p.parse_args()
    d = Driver(args.socket, args.output)
    if args.op == "start":
        result = d.start()
    elif args.op == "capabilities":
        result = d.require(d.call("capabilities"))
    elif args.op == "observe":
        result = d.observe(args.label)
    elif args.op == "runtime_status":
        result = d.require(d.call("runtime_status"))
    elif args.op == "step":
        if not args.action:
            p.error("step requires Action JSON")
        result = d.step(json.loads(args.action), args.label)
    else:
        result = d.control(args.op)
    print(json.dumps(result, ensure_ascii=False, indent=2))

if __name__ == "__main__":
    main()
