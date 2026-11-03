#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="$(date -u +%Y%m%dT%H%M%SZ)"
REPORT_DIR="${TUI_TOOL_PROGRESS_ACCEPTANCE_REPORT_DIR:-${ROOT}/reports/tui-tool-progress/${TIMESTAMP}}"
REPORT_JSON="${TUI_TOOL_PROGRESS_ACCEPTANCE_REPORT_JSON:-${REPORT_DIR}/tui-acceptance-report.json}"
GO_BIN="${GO_BIN:-go}"
BINARY="${TUI_TOOL_PROGRESS_ACCEPTANCE_BINARY:-${REPORT_DIR}/niuma-test}"
RUN_TESTS="${TUI_TOOL_PROGRESS_ACCEPTANCE_TESTS:-1}"
RUN_FULL_TESTS="${TUI_TOOL_PROGRESS_ACCEPTANCE_FULL_TESTS:-0}"
RUN_MODE="${TUI_TOOL_PROGRESS_ACCEPTANCE_RUN:-prepare}"
PERMISSION_FLAG="${TUI_TOOL_PROGRESS_ACCEPTANCE_PERMISSION_FLAG:---dangerously-skip-permissions}"

mkdir -p "${REPORT_DIR}/fixtures"
cd "${ROOT}"

run_step() {
  local name="$1"
  shift
  echo "==> ${name}"
  "$@"
}

write_success_fixture() {
  local dir="$1"
  mkdir -p "${dir}/internal/demo"
  printf '%s\n' 'module example.com/tui-success' 'go 1.22' >"${dir}/go.mod"
  printf '%s\n' '# TUI success fixture' '' 'Used by niuma TUI tool progress acceptance.' >"${dir}/README.md"
  printf '%s\n' \
    'package demo' \
    '' \
    'func Add(a, b int) int {' \
    '	return a + b' \
    '}' >"${dir}/internal/demo/calc.go"
  printf '%s\n' \
    'package demo' \
    '' \
    'import "testing"' \
    '' \
    'func TestAdd(t *testing.T) {' \
    '	if Add(2, 3) != 5 {' \
    '		t.Fatal("bad add")' \
    '	}' \
    '}' >"${dir}/internal/demo/calc_test.go"
}

write_failure_fixture() {
  local dir="$1"
  mkdir -p "${dir}"
  printf '%s\n' 'module example.com/tui-failure' 'go 1.22' >"${dir}/go.mod"
  printf '%s\n' '# TUI failure fixture' >"${dir}/README.md"
}

write_multi_fixture() {
  local dir="$1"
  mkdir -p "${dir}/internal/alpha" "${dir}/internal/beta"
  printf '%s\n' 'module example.com/tui-multi' 'go 1.22' >"${dir}/go.mod"
  printf '%s\n' '# TUI multi-tool fixture' '' 'Contains multiple files for Read, Glob, Grep, and Bash checks.' >"${dir}/README.md"
  printf '%s\n' \
    'package alpha' \
    '' \
    'func Alpha() string {' \
    '	return "alpha"' \
    '}' >"${dir}/internal/alpha/alpha.go"
  printf '%s\n' \
    'package beta' \
    '' \
    'func Beta() string {' \
    '	return "beta"' \
    '}' >"${dir}/internal/beta/beta.go"
}

write_prompts() {
  local success_dir="$1"
  local failure_dir="$2"
  local multi_dir="$3"
  cat >"${REPORT_DIR}/prompts.md" <<EOF
# TUI Tool Progress Acceptance Prompts

## success

cwd: \`${success_dir}\`

\`\`\`text
请用 TodoWrite 创建 3 个任务：1 读取 README.md 和 internal/demo/calc.go，2 运行 go test ./...，3 用一句中文总结结果。不要修改任何文件。
\`\`\`

Expected:
- Inline tool rows show TodoWrite, Read, and Bash in the Go Claude message area.
- No separate legacy \`Tools\` panel appears below the transcript.
- Todo panel reaches \`Tasks 3/3 all completed\`.
- Status line switches to the current \`Task: ...\` after TodoWrite.

## failure

cwd: \`${failure_dir}\`

\`\`\`text
请用 TodoWrite 创建 2 个任务：1 读取不存在的 missing-readme.md，2 运行 ls missing-file-for-tui-acceptance。不要修改任何文件，最后一句中文总结。
\`\`\`

Expected:
- Read failure renders like \`Read missing-readme.md ✗ → not found\`.
- Bash failure renders like \`Bash ls missing-file-for-tui-acceptance ✗ → exit 1\` or \`→ failed\`.
- No contradictory \`✗ → exit 0\`.
- No raw TodoWrite JSON appears in tool rows.

## multi

cwd: \`${multi_dir}\`

\`\`\`text
请用 TodoWrite 创建 5 个任务：1 读取 README.md，2 Glob 查找 **/*.go，3 Grep 搜索 "func"，4 运行 go test ./...，5 运行 ls missing-multi-tool。不要修改文件，最后一句中文总结。
\`\`\`

Expected:
- Collapsed tool view shows the latest 3 inline tool rows plus \`... +N tool uses ctrl+t expand\`.
- Pressing Ctrl+T expands more tool rows, pressing Ctrl+T again collapses.
- Todo panel can expand/collapse with Ctrl+Y.
- The transcript stays readable without a duplicate bottom \`Tools\` block.
EOF
}

write_report_template() {
  cat >"${REPORT_DIR}/report-template.md" <<'EOF'
# TUI Tool Progress Acceptance Report

Date:
Binary:
Commit:
Tester:

## Automated Checks

- [ ] capability visibility tests passed
- [ ] targeted tests passed
- [ ] full tests passed or intentionally skipped
- [ ] build passed

## Real TUI Checks

- [ ] success scenario
- [ ] failure scenario
- [ ] multi-tool scenario

## Evidence

Record the exact visible lines that prove the behavior:

```text
TodoWrite ...
Read ...
Bash ...
Tasks ...
```

## Findings

- Pass/fail:
- Regressions:
- Follow-up:
EOF
}

launch_tui() {
  local name="$1"
  local cwd="$2"
  echo
  echo "==> launching TUI scenario: ${name}"
  echo "cwd: ${cwd}"
  echo "prompt: ${REPORT_DIR}/prompts.md#${name}"
  echo "After validation, type /exit in the TUI."
  echo
  "${BINARY}" --cwd "${cwd}" ${PERMISSION_FLAG}
}

SUCCESS_DIR="${REPORT_DIR}/fixtures/success"
FAILURE_DIR="${REPORT_DIR}/fixtures/failure"
MULTI_DIR="${REPORT_DIR}/fixtures/multi"

write_success_fixture "${SUCCESS_DIR}"
write_failure_fixture "${FAILURE_DIR}"
write_multi_fixture "${MULTI_DIR}"
write_prompts "${SUCCESS_DIR}" "${FAILURE_DIR}" "${MULTI_DIR}"
write_report_template

{
  echo "root=${ROOT}"
  echo "report_dir=${REPORT_DIR}"
  echo "binary=${BINARY}"
  echo "run_mode=${RUN_MODE}"
  echo "go=$(${GO_BIN} version 2>/dev/null || echo unavailable)"
  echo "commit=$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
} >"${REPORT_DIR}/environment.txt"

if [[ "${RUN_TESTS}" == "1" ]]; then
  run_step "go test tui capability visibility" "${GO_BIN}" test ./internal/tui \
    -run 'TestModelModeHintShowsRuntimeContextStatus|TestModelModeHintShowsRecentAgentEvidenceSourceStatus|TestModelModeHintShowsRecentTaskFailureStatus|TestModelRendersNestedAgentProgressPanel|TestModelTaskToolInlineViewShowsCapabilityLoopSummary|TestModelAgentGetInlineViewShowsNestedCapabilityLoopSummary' \
    -count=1
  run_step "go test tui/query/cli" "${GO_BIN}" test ./internal/tui/... ./internal/query/... ./internal/cli/... -count=1
else
  echo "skip: targeted tests (TUI_TOOL_PROGRESS_ACCEPTANCE_TESTS=${RUN_TESTS})"
fi

if [[ "${RUN_FULL_TESTS}" == "1" ]]; then
  run_step "go test ./..." "${GO_BIN}" test ./... -count=1
else
  echo "skip: full tests (TUI_TOOL_PROGRESS_ACCEPTANCE_FULL_TESTS=${RUN_FULL_TESTS})"
fi

run_step "build ${BINARY}" "${GO_BIN}" build -o "${BINARY}" ./cmd/niuma

node - "$REPORT_JSON" "$REPORT_DIR" "$BINARY" "$RUN_MODE" "$RUN_TESTS" "$RUN_FULL_TESTS" "$(git rev-parse --short HEAD 2>/dev/null || echo unknown)" <<'NODE'
const fs = require("fs");
const [reportPath, reportDir, binary, runMode, runTests, runFullTests, commit] = process.argv.slice(2);
const report = {
  schema_version: "niuma/tui-tool-progress-acceptance/v1",
  ok: true,
  generated_at: new Date().toISOString(),
  report_dir: reportDir,
  binary,
  run_mode: runMode,
  commit,
  automated_checks: {
    capability_visibility_tests: runTests === "1" ? "passed" : "skipped",
    targeted_tests: runTests === "1" ? "passed" : "skipped",
    full_tests: runFullTests === "1" ? "passed" : "skipped",
    build: "passed",
  },
  covered_signals: [
    "tool_result_capability_loop_inline_summary",
    "sub_agent_progress_capability_loop_terminal_summary",
    "sub_agent_output_transcript_worktree_terminal_paths",
    "runtime_context_goal_cwd_session_agent_counts_status_bar",
  ],
};
fs.writeFileSync(reportPath, JSON.stringify(report, null, 2) + "\n");
NODE

echo
echo "TUI tool progress acceptance prepared."
echo "report: ${REPORT_DIR}"
echo "report_json: ${REPORT_JSON}"
echo "prompts: ${REPORT_DIR}/prompts.md"
echo "template: ${REPORT_DIR}/report-template.md"
echo

case "${RUN_MODE}" in
  prepare|"")
    echo "To run a real TUI scenario:"
    echo "  TUI_TOOL_PROGRESS_ACCEPTANCE_RUN=success scripts/tui-tool-progress-acceptance.sh"
    echo "  TUI_TOOL_PROGRESS_ACCEPTANCE_RUN=failure scripts/tui-tool-progress-acceptance.sh"
    echo "  TUI_TOOL_PROGRESS_ACCEPTANCE_RUN=multi scripts/tui-tool-progress-acceptance.sh"
    ;;
  success)
    launch_tui "success" "${SUCCESS_DIR}"
    ;;
  failure)
    launch_tui "failure" "${FAILURE_DIR}"
    ;;
  multi)
    launch_tui "multi" "${MULTI_DIR}"
    ;;
  all)
    launch_tui "success" "${SUCCESS_DIR}"
    launch_tui "failure" "${FAILURE_DIR}"
    launch_tui "multi" "${MULTI_DIR}"
    ;;
  *)
    echo "unknown TUI_TOOL_PROGRESS_ACCEPTANCE_RUN=${RUN_MODE}; expected prepare, success, failure, multi, or all" >&2
    exit 2
    ;;
esac
