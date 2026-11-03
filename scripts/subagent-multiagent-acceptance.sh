#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/product-env.sh"


ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="$(date -u +%Y%m%dT%H%M%SZ)"
REPORT_DIR="${SUBAGENT_MULTIAGENT_ACCEPTANCE_REPORT_DIR:-${ROOT}/reports/subagent-multiagent/${TIMESTAMP}}"
RUN_GO_TESTS="${SUBAGENT_MULTIAGENT_ACCEPTANCE_GO_TEST:-1}"
RUN_DIFF_CHECK="${SUBAGENT_MULTIAGENT_ACCEPTANCE_DIFF_CHECK:-1}"
LIVE_BASE="${NIUMA_EVAL_LIVE_API_BASE:-}}"
ANTHROPIC_AUTH="${ANTHROPIC_API_KEY:-${ANTHROPIC_AUTH_TOKEN:-${CLAUDE_CODE_AUTH_TOKEN:-${CLAUDE_CODE_OAUTH_TOKEN:-}}}}"

mkdir -p "${REPORT_DIR}"
cd "${ROOT}"

run_step() {
  local name="$1"
  shift
  echo "==> ${name}"
  "$@"
}

run_eval() {
  local profile="$1"
  local output="${REPORT_DIR}/eval-${profile}.json"
  local log="${REPORT_DIR}/eval-${profile}.log"
  echo "==> eval agents --profile ${profile}"
  if ! go run ./cmd/niuma eval agents --profile "${profile}" --json --output "${output}" >"${log}" 2>&1; then
    echo "eval agents --profile ${profile} failed" >&2
    echo "log: ${log}" >&2
    tail -n 80 "${log}" >&2 || true
    exit 1
  fi
  echo "report: ${output}"
  echo "log: ${log}"
}

echo "subagent/multi-agent acceptance"
echo "reports: ${REPORT_DIR}"

if [[ "${RUN_GO_TESTS}" == "1" ]]; then
  run_step "go test ./... -count=1" go test ./... -count=1
else
  echo "skip: go test ./... -count=1 (SUBAGENT_MULTIAGENT_ACCEPTANCE_GO_TEST=${RUN_GO_TESTS})"
fi

run_eval "local"

if [[ -n "${LIVE_BASE}" ]]; then
  run_eval "live"
  run_eval "live-agent-api"
else
  echo "skip: live API profiles require NIUMA_EVAL_LIVE_API_BASE"
fi

if [[ -n "${ANTHROPIC_AUTH}" ]]; then
  run_eval "anthropic-thinking"
else
  echo "skip: anthropic-thinking requires ANTHROPIC_API_KEY or ANTHROPIC_AUTH_TOKEN"
fi

if [[ "${RUN_DIFF_CHECK}" == "1" ]]; then
  run_step "git diff --check" git diff --check
else
  echo "skip: git diff --check (SUBAGENT_MULTIAGENT_ACCEPTANCE_DIFF_CHECK=${RUN_DIFF_CHECK})"
fi

echo "subagent/multi-agent acceptance ok"
