#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO_BIN="${GO_BIN:-go}"
OUT_DIR="${OUT_DIR:-/tmp/gocc-tui-semantic-terminal-$(date +%Y%m%d-%H%M%S)}"
TMP_ROOT="${ROOT_DIR}/.tmp"
mkdir -p "${TMP_ROOT}"
HARNESS_DIR="$(mktemp -d "${TMP_ROOT}/tui-semantic-terminal-harness-XXXXXX")"
HARNESS_BIN="${OUT_DIR}/gocc-tui-semantic-harness"

cleanup() {
  rm -rf "${HARNESS_DIR}"
}
trap cleanup EXIT

mkdir -p "${OUT_DIR}"

cat >"${HARNESS_DIR}/main.go" <<'GO'
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/konglong87/super-niuma/internal/tui"
)

func main() {
	outDir := os.Getenv("GOCC_TUI_ACCEPTANCE_OUT")
	if outDir == "" {
		fmt.Fprintln(os.Stderr, "GOCC_TUI_ACCEPTANCE_OUT is required")
		os.Exit(2)
	}
	// Display-only: the semantic terminal fixture just needs a stable cwd string.
	const harnessCWD = "/workspace/niuma"
	var turn atomic.Int32
	opts := tui.Options{
		Title: "niuma",
		Welcome: tui.WelcomeInfo{
			Version:        "semantic-terminal",
			Model:          "deterministic-tui-harness",
			Provider:       "local/fake",
			PromptMode:     "chat",
			ContextLength:  200000,
			CWD:            harnessCWD,
			ToolSummary:    "31",
			MCPServers:     0,
			PermissionMode: "ask",
			Sandbox:        "off",
			SessionID:      "semantic-terminal-acceptance",
			SessionStatus:  "new",
		},
		RunStream: func(ctx context.Context, prompt string, events chan<- tui.StreamEvent) error {
			n := int(turn.Add(1))
			response := semanticResponse(n)
			if err := os.WriteFile(filepath.Join(outDir, fmt.Sprintf("turn-%02d-prompt.txt", n)), []byte(prompt), 0o644); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(outDir, fmt.Sprintf("turn-%02d-response.txt", n)), []byte(response), 0o644); err != nil {
				return err
			}
			chunks := splitResponse(n, response)
			for i, chunk := range chunks {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case events <- tui.StreamEvent{Type: tui.StreamText, Text: chunk}:
				}
				if n == 8 && i == 0 {
					if err := emitSubAgentProgress(ctx, events); err != nil {
						return err
					}
				}
				if i == 0 {
					events <- tui.StreamEvent{Type: tui.StreamUsage, Result: &tui.QueryResult{
						Model: "deterministic-tui-harness",
						Turns: 1,
						Usage: tui.Usage{InputTokens: 1000 + n, OutputTokens: 120 + n},
						Context: tui.RuntimeContext{
							CWD:           harnessCWD,
							MaxTurns:      100,
							MaxTokens:     200000,
							ToolCount:     31,
							ContextWindow: 200000,
						},
					}}
				}
				time.Sleep(35 * time.Millisecond)
			}
			done := fmt.Sprintf("TURN_%02d_DONE\n", n)
			return os.WriteFile(filepath.Join(outDir, fmt.Sprintf("turn-%02d.done", n)), []byte(done), 0o644)
		},
	}
	if err := tui.Run(context.Background(), os.Stdin, os.Stdout, opts); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func emitSubAgentProgress(ctx context.Context, events chan<- tui.StreamEvent) error {
	items := []tui.StreamEvent{
		{
			Type:    tui.StreamNestedProgress,
			Event:   "started",
			TaskID:  77,
			Payload: json.RawMessage(`{"agent_name":"semantic-reviewer","model":"deterministic-subagent","description":"Review table, long text, and display ordering","started_at":"2026-07-12T10:00:00Z"}`),
		},
		{
			Type:    tui.StreamNestedProgress,
			Event:   "text_delta",
			TaskID:  77,
			Payload: json.RawMessage(`{"text":"Sub-agent confirms table rows, long text, and bottom chrome ordering remain stable."}`),
		},
		{
			Type:    tui.StreamNestedProgress,
			Event:   "tool_call",
			TaskID:  77,
			Payload: json.RawMessage(`{"tool_name":"Read"}`),
		},
		{
			Type:    tui.StreamNestedProgress,
			Event:   "completed",
			TaskID:  77,
			Payload: json.RawMessage(`{"turns":1,"tool_calls":1,"duration_ms":42,"session_id":"subagent-terminal-acceptance","output_file":"/tmp/subagent-terminal-acceptance.txt"}`),
		},
	}
	for _, event := range items {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case events <- event:
		}
		time.Sleep(25 * time.Millisecond)
	}
	return nil
}

func semanticResponse(turn int) string {
	marker := fmt.Sprintf("TURN_%02d_DONE", turn)
	switch turn {
	case 1:
		return strings.Join([]string{
			"第一轮回答：Anything-AI 是一个系统性 AI 知识索引项目，目标是帮助普通人认识、理解和驾驭 AI。",
			"",
			"| 目录 | 内容 |",
			"|---|---|",
			"| `0-start-here/` | AI 认知入门、学习路径、反焦虑说明 |",
			"| `1-understand-ai/` | AI 原理、LLM 基础、推理和幻觉 |",
			"| `2-choose-tools/` | 工具选择矩阵、场景推荐 |",
			"| `roles/` | 按角色分类的使用指南 |",
			"| `prompts/` | 提示词库 |",
			"",
			"核心理念：实践出真知，用 AI 但不盲从。 " + marker,
		}, "\n")
	case 2:
		return "第二轮延续第一轮：学习路径建议从 `0-start-here/` 开始，再进入 `1-understand-ai/`，最后按场景去 `2-choose-tools/`。重点是先建立判断力，再选择工具。 " + marker
	case 3:
		return "第三轮继续解释第二轮的判断力：判断一个 AI 工具，先看任务类型、输入输出质量、成本、隐私和可复用流程，而不是只看宣传。 " + marker
	case 4:
		return "第四轮展开第三轮的任务类型：写作、代码、研究、图片、自动化和知识管理要分开评估；Anything-AI 的角色目录就是为了降低选择成本。 " + marker
	case 5:
		return "第五轮承接第四轮：对程序员来说，可以把 Claude 用于架构和审查，把 Codex 用于实现和验证，形成稳定的工程闭环。 " + marker
	case 6:
		return strings.Join([]string{
			"第六轮追问工程闭环：一次好的 AI 协作应包含需求澄清、方案设计、实现、测试、真实环境验收和复盘记录。",
			"",
			"长文本压力段 A：需求澄清阶段要把目标、边界、非目标、风险和验收标准写清楚，避免模型在执行阶段不断重排上下文。",
			"长文本压力段 B：方案设计阶段要先判断数据流、事件顺序和渲染边界，尤其 TUI 这类系统必须区分实时层、归档层和底部 chrome。",
			"长文本压力段 C：实现阶段要把竞态封在架构里，而不是靠局部 if 判断补洞；完成事件必须服从同一事件队列的顺序约束。",
			"长文本压力段 D：验收阶段要真实 PTY 连续多轮运行，并用 Terminal 截图确认表格、长文本、sub-agent 和底部输入区没有互相覆盖。",
			"LONG_TEXT_SENTINEL " + marker,
		}, "\n")
	case 7:
		return "第七轮聚焦测试：TUI 这种可见交互不能只靠字符串测试，还要真实 PTY、真实 Terminal 截图和多轮连续对话。 " + marker
	case 8:
		return "第八轮说明风险：如果完成事件早于剩余文本事件进入渲染层，就会把同一条回复拆成两个 assistant 块；本轮还触发 semantic-reviewer sub-agent 简单进度，用于验证 sub-agent 块不会漂移到底部或覆盖正文。 SUBAGENT_ACCEPTANCE_DONE " + marker
	case 9:
		return "第九轮给出验收标准：每轮回复只允许一个 niuma 标题，表格行不能被拆走，底部输入区不能覆盖最后一行正文。 " + marker
	default:
		return "第十轮总结：这次真实 PTY 连续会话保持语义连贯，且专门覆盖 usage 后仍有文本的场景；如果屏幕上没有重复 niuma 和底部遮挡，则通过。 " + marker
	}
}

func splitResponse(turn int, response string) []string {
	if turn == 1 {
		needle := "| `prompts/` | 提示词库 |"
		idx := strings.Index(response, needle)
		if idx > 0 {
			return []string{response[:idx], response[idx:]}
		}
	}
	runes := []rune(response)
	mid := len(runes) / 2
	return []string{string(runes[:mid]), string(runes[mid:])}
}
GO

cat >"${OUT_DIR}/stdin_driver.py" <<'PY'
import os
import pathlib
import sys
import time

out_dir = pathlib.Path(os.environ["OUT_DIR"])
prompts = [
    "Turn 1: what is this project for?",
    "Turn 2: expand the learning path from turn 1.",
    "Turn 3: from turn 2, what judgment criteria matter most?",
    "Turn 4: from turn 3, how should users classify task types?",
    "Turn 5: from turn 4, how should engineers use this project?",
    "Turn 6: from turn 5, describe the engineering loop.",
    "Turn 7: from turn 6, why is unit testing alone not enough?",
    "Turn 8: from turn 7, what TUI race is most dangerous?",
    "Turn 9: from turn 8, how should the fix be accepted?",
    "Turn 10: summarize the ten-turn conversation and result.",
]

def wait_marker(turn: int) -> None:
    marker = out_dir / f"turn-{turn:02d}.done"
    deadline = time.time() + 15
    while time.time() < deadline:
        if marker.exists():
            time.sleep(0.65)
            return
        time.sleep(0.1)
    raise SystemExit(f"timed out waiting for {marker}")

# Let Bubble Tea finish terminal initialization and raw-mode setup before the
# first bytes arrive; otherwise the line discipline can translate Enter into a
# textarea newline instead of a submit key.
time.sleep(2.0)
for idx, prompt in enumerate(prompts, start=1):
    sys.stdout.write(prompt + "\r")
    sys.stdout.flush()
    wait_marker(idx)

(out_dir / "driver.done").write_text("done\n")
time.sleep(5.0)
sys.stdout.write("\x03")
sys.stdout.flush()
PY

cat >"${OUT_DIR}/terminal_driver.py" <<'PY'
import json
import os
import pathlib
import shlex
import subprocess
import sys
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

def wait_marker(turn: int) -> None:
    marker = out_dir / f"turn-{turn:02d}.done"
    deadline = time.time() + 15
    while time.time() < deadline:
        if marker.exists():
            time.sleep(0.55)
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
    for idx in range(1, 11):
        wait_marker(idx)
        if idx == 5:
            screenshots.append(capture(window_id, "mid-turn-05-terminal.png"))
        if idx == 8:
            screenshots.append(capture(window_id, "subagent-turn-08-terminal.png"))
    screenshots.append(capture(window_id, "final-turn-10-terminal.png"))
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

markers = []
for marker in sorted(out_dir.glob("turn-*.done")):
    markers.append(marker.read_text().strip())

report = {
    "schema_version": "niuma/tui-semantic-terminal-acceptance/v1",
    "window_id": window_id,
    "turns": 10,
    "markers": markers,
    "screenshots": screenshots,
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
if report["turns"] != 10:
    raise SystemExit(f"turns={report['turns']}, want 10")
if len(report["markers"]) != 10:
    raise SystemExit(f"markers={len(report['markers'])}, want 10")
for idx, marker in enumerate(report["markers"], start=1):
    want = f"TURN_{idx:02d}_DONE"
    if marker != want:
        raise SystemExit(f"marker {idx} = {marker!r}, want {want!r}")
for shot in report["screenshots"]:
    path = pathlib.Path(shot["path"])
    if not path.exists() or path.stat().st_size <= 0:
        raise SystemExit(f"missing screenshot: {path}")
needles = (
    "prompts/",
    "LONG_TEXT_SENTINEL",
    "Sub-agents",
    "semantic-reviewer",
    "SUBAGENT_ACCEPTANCE_DONE",
    "TURN_10_DONE",
)
deadline = time.time() + 5
missing = list(needles)
while time.time() < deadline:
    typescript = (out / "typescript.log").read_text(errors="replace")
    missing = [needle for needle in needles if needle not in typescript]
    if not missing:
        break
    time.sleep(0.1)
if missing:
    raise SystemExit(f"typescript missing {missing!r}")
print(f"semantic Terminal TUI acceptance artifacts: {out}")
PY
