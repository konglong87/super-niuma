#!/usr/bin/env bash
# 真实 Terminal + 真实 PTY 验收：AskUserQuestion / 权限卡片弹出时，本轮已产生的输出
# 必须仍在屏幕上，卡片只占底部若干行。见 docs/tui/tui_interactive_prompt_scrollback_fix_plan.md。
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO_BIN="${GO_BIN:-go}"
OUT_DIR="${OUT_DIR:-/tmp/gocc-tui-interactive-prompt-$(date +%Y%m%d-%H%M%S)}"
TMP_ROOT="${ROOT_DIR}/.tmp"
mkdir -p "${TMP_ROOT}"
HARNESS_DIR="$(mktemp -d "${TMP_ROOT}/tui-interactive-prompt-harness-XXXXXX")"
HARNESS_BIN="${OUT_DIR}/gocc-tui-interactive-prompt-harness"

cleanup() {
  rm -rf "${HARNESS_DIR}"
}
trap cleanup EXIT

mkdir -p "${OUT_DIR}"

cat >"${HARNESS_DIR}/main.go" <<'GO'
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/konglong87/super-niuma/internal/tui"
)

// 一轮里先流出足够长的正文（超过一屏），再弹卡片。卡片弹出时正文尾部必须仍可见。
const answerLines = 40

func main() {
	outDir := os.Getenv("GOCC_TUI_ACCEPTANCE_OUT")
	if outDir == "" {
		fmt.Fprintln(os.Stderr, "GOCC_TUI_ACCEPTANCE_OUT is required")
		os.Exit(2)
	}
	mark := func(name, body string) error {
		return os.WriteFile(filepath.Join(outDir, name), []byte(body+"\n"), 0o644)
	}

	opts := tui.Options{
		Title: "niuma",
		Welcome: tui.WelcomeInfo{
			Version:        "interactive-prompt-acceptance",
			Model:          "deterministic-tui-harness",
			Provider:       "local/fake",
			PromptMode:     "chat",
			ContextLength:  200000,
			CWD:            "/workspace/niuma",
			ToolSummary:    "31",
			MCPServers:     0,
			PermissionMode: "ask",
			Sandbox:        "off",
			SessionID:      "interactive-prompt-acceptance",
			SessionStatus:  "new",
		},
		RunStream: func(ctx context.Context, prompt string, events chan<- tui.StreamEvent) error {
			send := func(ev tui.StreamEvent) error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case events <- ev:
					return nil
				}
			}

			// 1) 长正文，逐行流出。
			for i := 1; i <= answerLines; i++ {
				line := fmt.Sprintf("ANSWER_LINE_%02d 这一行属于本轮回答，卡片弹出后必须还在屏幕上。\n", i)
				if err := send(tui.StreamEvent{Type: tui.StreamText, Text: line}); err != nil {
					return err
				}
			}
			if err := send(tui.StreamEvent{Type: tui.StreamText, Text: "\nANSWER_TAIL_SENTINEL 正文到此结束，下面开始问问题。\n"}); err != nil {
				return err
			}
			if err := mark("answer.done", "ANSWER_DONE"); err != nil {
				return err
			}

			// 2) 弹 AskUserQuestion 卡片，阻塞等答案。
			reply := make(chan tui.UserQuestionAnswer, 1)
			if err := send(tui.StreamEvent{
				Type: tui.StreamUserQuestion,
				Question: &tui.UserQuestionRequest{
					Question: "QUESTION_CARD_SENTINEL 想继续深入哪个方向？",
					Choices: []string{
						"CHOICE_ONE 继续第一个方向",
						"CHOICE_TWO 换第二个方向",
						"CHOICE_THREE 换个话题",
					},
				},
				QuestionReply: reply,
			}); err != nil {
				return err
			}
			if err := mark("question-raised.done", "QUESTION_RAISED"); err != nil {
				return err
			}

			var answer tui.UserQuestionAnswer
			select {
			case <-ctx.Done():
				return ctx.Err()
			case answer = <-reply:
			}
			if err := mark("question-answered.done", "ANSWERED:"+strings.TrimSpace(answer.Answer)); err != nil {
				return err
			}

			// 3) 收到答案后继续输出，验证卡片消失后正常回到流式渲染。
			if err := send(tui.StreamEvent{
				Type: tui.StreamText,
				Text: "\nAFTER_ANSWER_SENTINEL 已收到答案：" + answer.Answer + "\n",
			}); err != nil {
				return err
			}
			return mark("turn.done", "TURN_DONE")
		},
	}
	if err := tui.Run(context.Background(), os.Stdin, os.Stdout, opts); err != nil {
		fmt.Fprintln(os.Stderr, "tui run failed:", err)
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


def wait_marker(name: str, timeout: float = 30.0) -> None:
    marker = out_dir / name
    deadline = time.time() + timeout
    while time.time() < deadline:
        if marker.exists():
            return
        time.sleep(0.1)
    raise SystemExit(f"timed out waiting for {marker}")


# 让 Bubble Tea 完成 raw mode 初始化，否则 Enter 会被行规则翻译成换行而不是提交。
time.sleep(2.0)
sys.stdout.write("请给一段足够长的回答，然后问我一个问题\r")
sys.stdout.flush()

# 卡片弹出后停住，留给 terminal_driver 截图。
wait_marker("question-raised.done")
time.sleep(4.0)

# 选第 2 项，验证卡片可正常作答。
sys.stdout.write("2\r")
sys.stdout.flush()
wait_marker("turn.done")
time.sleep(3.0)

(out_dir / "driver.done").write_text("done\n")
sys.stdout.write("\x03")
sys.stdout.flush()
time.sleep(1.0)
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
keep_terminal = os.environ.get("GOCC_TUI_ACCEPTANCE_KEEP_TERMINAL") == "1"


def run_osascript(script: str) -> str:
    return subprocess.check_output(["osascript", "-e", script], text=True).strip()


def capture(window_id: str, name: str) -> dict:
    path = out_dir / name
    result = subprocess.run(["screencapture", "-x", "-l", window_id, str(path)])
    mode = "window"
    if result.returncode != 0 or not path.exists() or path.stat().st_size == 0:
        subprocess.run(["screencapture", "-x", str(path)], check=True)
        mode = "screen"
    return {"path": str(path), "mode": mode}


def wait_marker(name: str, timeout: float = 30.0) -> None:
    marker = out_dir / name
    deadline = time.time() + timeout
    while time.time() < deadline:
        if marker.exists():
            return
        time.sleep(0.1)
    raise SystemExit(f"timed out waiting for {marker}")


child_command = 'stty rows 38 cols 100; exec "$HARNESS_BIN"'
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
      set number of rows of front window to 38
      set bounds of front window to {{60, 60, 1060, 900}}
      return id of front window
    end tell
    """
)
time.sleep(1.5)

screenshots = []
try:
    # 关键截图：卡片正在显示，此时正文尾部必须仍在屏幕上。
    wait_marker("question-raised.done")
    time.sleep(2.5)
    screenshots.append(capture(window_id, "question-card-visible.png"))
    # 作答之后：卡片消失，回到正常流式渲染。
    wait_marker("turn.done")
    time.sleep(2.0)
    screenshots.append(capture(window_id, "after-answer.png"))
finally:
    if not keep_terminal:
        subprocess.run(
            [
                "osascript",
                "-e",
                f'tell application "Terminal" to close window id {window_id}',
            ],
            check=False,
        )

report = {
    "schema_version": "niuma/tui-interactive-prompt-terminal-acceptance/v1",
    "window_id": window_id,
    "screenshots": screenshots,
    "answered": (out_dir / "question-answered.done").read_text().strip(),
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

out = pathlib.Path(sys.argv[1])
report = json.loads((out / "report.json").read_text())

for shot in report["screenshots"]:
    path = pathlib.Path(shot["path"])
    if not path.exists() or path.stat().st_size <= 0:
        raise SystemExit(f"missing real Terminal screenshot: {path}")

if not report["answered"].startswith("ANSWERED:"):
    raise SystemExit(f"card was not answerable: {report['answered']!r}")
if "CHOICE_TWO" not in report["answered"]:
    raise SystemExit(f"expected the second choice to be selected, got {report['answered']!r}")

typescript = (out / "typescript.log").read_text(errors="replace")
required = [
    "ANSWER_LINE_01",
    "ANSWER_TAIL_SENTINEL",
    "QUESTION_CARD_SENTINEL",
    "CHOICE_TWO",
    "AFTER_ANSWER_SENTINEL",
]
missing = [needle for needle in required if needle not in typescript]
if missing:
    raise SystemExit(f"typescript missing {missing!r}")

# 正文必须出现至少两次：一次是流式渲染进 viewport，另一次是弹卡片前 flush 到真实
# scrollback（transcriptFlushInteractivePrompt）。修复前只会出现 1 次 —— 画一遍就被
# 卡片抹掉，从未进滚动区。这条断言守的是「内容进了 scrollback」；卡片只占底部若干行
# 这个版式结论由截图和 internal/tui 单测负责。
tail_paints = typescript.count("ANSWER_TAIL_SENTINEL")
if tail_paints < 2:
    raise SystemExit(
        "answer text never reached terminal scrollback "
        f"(ANSWER_TAIL_SENTINEL painted {tail_paints}x, want >=2): "
        "the interactive prompt flush is missing"
    )

print(f"interactive prompt Terminal acceptance artifacts: {out}")
print(f"card screenshot: {report['screenshots'][0]['path']}")
PY

echo "interactive prompt Terminal acceptance passed: ${OUT_DIR}"
