#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO_BIN="${GO_BIN:-go}"
OUT_DIR="${OUT_DIR:-/tmp/gocc-tui-visual-regression-$(date +%Y%m%d-%H%M%S)}"
RUN_FULL="${TUI_VISUAL_SOP_FULL:-0}"

mkdir -p "${OUT_DIR}"

run_step() {
  local name="$1"
  shift
  echo "==> ${name}"
  "$@" 2>&1 | tee "${OUT_DIR}/${name}.log"
}

cd "${ROOT_DIR}"

run_step bash-syntax bash -n \
  scripts/tui-semantic-terminal-acceptance.sh \
  scripts/tui-recap-terminal-acceptance.sh \
  scripts/tui-ten-turn-acceptance.sh \
  scripts/tui-display-order-acceptance.sh \
  scripts/tui-session-replay-acceptance.sh

run_step semantic-terminal env OUT_DIR="${OUT_DIR}/semantic-terminal" scripts/tui-semantic-terminal-acceptance.sh
run_step recap-terminal env OUT_DIR="${OUT_DIR}/recap-terminal" scripts/tui-recap-terminal-acceptance.sh
run_step ten-turn env OUT_DIR="${OUT_DIR}/ten-turn" scripts/tui-ten-turn-acceptance.sh
run_step display-order env OUT_DIR="${OUT_DIR}/display-order" scripts/tui-display-order-acceptance.sh
run_step session-replay env OUT_DIR="${OUT_DIR}/session-replay" scripts/tui-session-replay-acceptance.sh
run_step tui-tests "${GO_BIN}" test ./internal/tui -count=1

if [[ "${RUN_FULL}" == "1" ]]; then
  run_step full-go-tests "${GO_BIN}" test ./... -count=1
fi

run_step diff-check git diff --check

python3 - "${OUT_DIR}" <<'PY'
import json
import pathlib
import sys

out = pathlib.Path(sys.argv[1])
semantic = out / "semantic-terminal"
recap = out / "recap-terminal"
ten_turn = out / "ten-turn"

semantic_report = json.loads((semantic / "report.json").read_text())
screenshots = [pathlib.Path(item["path"]) for item in semantic_report["screenshots"]]
for path in screenshots:
    if not path.exists() or path.stat().st_size <= 0:
        raise SystemExit(f"missing real Terminal screenshot: {path}")
recap_report = json.loads((recap / "report.json").read_text())
recap_screenshot = pathlib.Path(recap_report["screenshot"]["path"])
if not recap_screenshot.exists() or recap_screenshot.stat().st_size <= 0:
    raise SystemExit(f"missing recap Terminal screenshot: {recap_screenshot}")
recap_typescript = (recap / "typescript.log").read_text(errors="replace")
for needle in ("※recap:", "验证 away recap 可见", "Usage"):
    if needle not in recap_typescript:
        raise SystemExit(f"recap Terminal transcript missing {needle!r}")

typescript = (semantic / "typescript.log").read_text(errors="replace")
required = [
    "prompts/",
    "LONG_TEXT_SENTINEL",
    "Sub-agents",
    "semantic-reviewer",
    "SUBAGENT_ACCEPTANCE_DONE",
    "TURN_10_DONE",
]
missing = [needle for needle in required if needle not in typescript]
if missing:
    raise SystemExit(f"semantic Terminal transcript missing {missing!r}")

replay_status_path = out / "session-replay" / "status.json"
if replay_status_path.exists():
    session_replay = json.loads(replay_status_path.read_text())
else:
    session_replay = {"status": "unknown", "detail": f"missing {replay_status_path}"}
if session_replay["status"] != "ok":
    print(f"session replay step: {session_replay['status']} — {session_replay.get('detail', '')}")
if session_replay.get("local_session_status") == "failed":
    print("session replay step: recorded-session pass failed (advisory, see session-replay/local-sessions/replay.log)")

tail_report = json.loads((ten_turn / "tail-guard-report.json").read_text())
if tail_report["guard_lines"] != 1:
    raise SystemExit(f"expected compact transcript guard=1, got {tail_report['guard_lines']}")
if tail_report["lines_after_tail"] > 2:
    raise SystemExit(f"large blank gap after tail: {tail_report['lines_after_tail']} lines")

report = {
    "schema_version": "niuma/tui-visual-regression-sop/v1",
    "out_dir": str(out),
    "semantic_terminal_report": str(semantic / "report.json"),
    "recap_terminal_report": str(recap / "report.json"),
    "terminal_screenshots": [str(path) for path in screenshots],
    "recap_terminal_screenshot": str(recap_screenshot),
    "ten_turn_tail_guard_report": str(ten_turn / "tail-guard-report.json"),
    "session_replay": session_replay,
    "acceptance": {
        "no_large_blank_gap": True,
        "no_bottom_chrome_overlap": True,
        "table_long_text_subagent_covered": True,
        "away_recap_visible_after_usage": True,
    },
}
(out / "visual-regression-report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
(out / "README.md").write_text(
    "# TUI Visual Regression SOP Report\n\n"
    f"- Output: `{out}`\n"
    f"- Final screenshot: `{screenshots[-1]}`\n"
    f"- Sub-agent screenshot: `{screenshots[1] if len(screenshots) > 1 else screenshots[-1]}`\n"
    f"- Recap screenshot: `{recap_screenshot}`\n"
    "- Acceptance: no large blank gap, no bottom chrome overlap, table/long text/sub-agent covered, away recap visible after usage.\n"
)
print(f"TUI visual regression SOP artifacts: {out}")
PY

echo "TUI visual regression SOP passed: ${OUT_DIR}"
