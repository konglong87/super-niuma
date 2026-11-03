#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO_BIN="${GO_BIN:-go}"
OUT_DIR="${OUT_DIR:-/tmp/gocc-tui-recap-terminal-$(date +%Y%m%d-%H%M%S)}"
TMP_ROOT="${ROOT_DIR}/.tmp"
mkdir -p "${TMP_ROOT}" "${OUT_DIR}"
HARNESS_DIR="$(mktemp -d "${TMP_ROOT}/tui-recap-terminal-harness-XXXXXX")"
HARNESS_BIN="${OUT_DIR}/gocc-tui-recap-harness"

cleanup() {
  rm -rf "${HARNESS_DIR}"
}
trap cleanup EXIT

cat >"${HARNESS_DIR}/main.go" <<'GO'
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/konglong87/super-niuma/internal/tui"
)

func main() {
	outDir := os.Getenv("GOCC_TUI_ACCEPTANCE_OUT")
	if outDir == "" {
		fmt.Fprintln(os.Stderr, "GOCC_TUI_ACCEPTANCE_OUT is required")
		os.Exit(2)
	}
	opts := tui.Options{
		Title: "niuma",
		Welcome: tui.WelcomeInfo{
			Version:        "recap-terminal",
			Model:          "deterministic-recap-harness",
			Provider:       "local/fake",
			PromptMode:     "chat",
			ContextLength:  200000,
			CWD:            "/tmp/gocc-tui-recap",
			ToolSummary:    "31",
			PermissionMode: "ask",
			Sandbox:        "off",
			SessionID:      "recap-terminal-acceptance",
			SessionStatus:  "new",
		},
		RunStream: func(ctx context.Context, prompt string, events chan<- tui.StreamEvent) error {
			events <- tui.StreamEvent{Type: tui.StreamText, Text: "本轮回答已经完成，接下来等待 away recap。 TURN_01_DONE"}
			events <- tui.StreamEvent{Type: tui.StreamUsage, Result: &tui.QueryResult{
				Model: "deterministic-recap-harness",
				Turns: 1,
				Usage: tui.Usage{InputTokens: 1001, OutputTokens: 121},
				Context: tui.RuntimeContext{MaxTurns: 100, MaxTokens: 200000, ToolCount: 31},
			}}
			return os.WriteFile(filepath.Join(outDir, "turn-01.done"), []byte("TURN_01_DONE\n"), 0o644)
		},
		RunAwayRecap: func(ctx context.Context, events chan<- tui.StreamEvent) error {
			events <- tui.StreamEvent{Type: tui.StreamRecap, Text: "本次会话目标：验证 away recap 可见\n已完成：usage 后 recap 仍显示\n下一步：继续对话"}
			return os.WriteFile(filepath.Join(outDir, "recap.done"), []byte("RECAP_DONE\n"), 0o644)
		},
		AwayRecapDelay: time.Second,
	}
	if err := tui.Run(context.Background(), os.Stdin, os.Stdout, opts); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
GO

cat >"${OUT_DIR}/stdin_driver.py" <<'PY'
import os
import pathlib
import sys
import time

out_dir = pathlib.Path(os.environ["OUT_DIR"])

def wait_marker(name: str, timeout: float = 15.0) -> None:
    marker = out_dir / name
    deadline = time.time() + timeout
    while time.time() < deadline:
        if marker.exists():
            return
        time.sleep(0.1)
    raise SystemExit(f"timed out waiting for {marker}")

time.sleep(2.0)
sys.stdout.write("请回答一句话，然后等待 recap\r")
sys.stdout.flush()
wait_marker("turn-01.done")
wait_marker("recap.done")
time.sleep(1.2)
sys.stdout.write("\x03")
sys.stdout.flush()
PY

cat >"${OUT_DIR}/terminal_driver.py" <<'PY'
import json
import os
import pathlib
import shlex
import subprocess
import time

out_dir = pathlib.Path(os.environ["OUT_DIR"])
stdin_driver = pathlib.Path(os.environ["STDIN_DRIVER"])

def run_osascript(script: str) -> str:
    return subprocess.check_output(["osascript", "-e", script], text=True).strip()

def wait_marker(name: str, timeout: float = 15.0) -> None:
    marker = out_dir / name
    deadline = time.time() + timeout
    while time.time() < deadline:
        if marker.exists():
            return
        time.sleep(0.1)
    raise SystemExit(f"timed out waiting for {marker}")

def capture(window_id: str, name: str) -> dict:
    path = out_dir / name
    result = subprocess.run(["screencapture", "-x", "-l", window_id, str(path)])
    mode = "window"
    if result.returncode != 0 or not path.exists() or path.stat().st_size == 0:
        subprocess.run(["screencapture", "-x", str(path)], check=True)
        mode = "screen"
    return {"path": str(path), "mode": mode}

child_command = 'stty rows 26 cols 100; exec "$HARNESS_BIN"'
command = (
    f"OUT_DIR={shlex.quote(str(out_dir))} python3 {shlex.quote(str(stdin_driver))} | "
    f"GOCC_TUI_ACCEPTANCE_OUT={shlex.quote(str(out_dir))} "
    f"HARNESS_BIN={shlex.quote(os.environ['HARNESS_BIN'])} "
    f"script -q {shlex.quote(str(out_dir / 'typescript.log'))} "
    f"/bin/sh -c {shlex.quote(child_command)}"
)
window_id = run_osascript(
    f"""
    tell application "Terminal"
      activate
      set w to do script {json.dumps(command)}
      set number of columns of front window to 100
      set number of rows of front window to 26
      set bounds of front window to {{80, 80, 1120, 760}}
      return id of front window
    end tell
    """
)
time.sleep(1.5)
try:
    wait_marker("recap.done")
    time.sleep(1.2)
    screenshot = capture(window_id, "recap-terminal.png")
finally:
    subprocess.run(
        ["osascript", "-e", f'tell application "Terminal" to close window id {window_id}'],
        check=False,
    )

report = {
    "schema_version": "niuma/tui-recap-terminal-acceptance/v1",
    "window_id": window_id,
    "screenshot": screenshot,
}
(out_dir / "report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
PY

cd "${ROOT_DIR}"
"${GO_BIN}" build -o "${HARNESS_BIN}" "${HARNESS_DIR}"
OUT_DIR="${OUT_DIR}" HARNESS_BIN="${HARNESS_BIN}" STDIN_DRIVER="${OUT_DIR}/stdin_driver.py" python3 "${OUT_DIR}/terminal_driver.py"

python3 - "${OUT_DIR}" <<'PY'
import json
import pathlib
import sys
import time

out = pathlib.Path(sys.argv[1])
report = json.loads((out / "report.json").read_text())
shot = pathlib.Path(report["screenshot"]["path"])
if not shot.exists() or shot.stat().st_size <= 0:
    raise SystemExit(f"missing screenshot: {shot}")
needles = ("TURN_01_DONE", "※recap:", "验证 away recap 可见", "Usage")
deadline = time.time() + 5
missing = list(needles)
while time.time() < deadline:
    text = (out / "typescript.log").read_text(errors="replace")
    missing = [needle for needle in needles if needle not in text]
    if not missing:
        break
    time.sleep(0.1)
if missing:
    raise SystemExit(f"typescript missing {missing!r}")
print(f"TUI recap Terminal acceptance artifacts: {out}")
PY
