#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/product-env.sh"


GO_BIN="${GO_BIN:-go}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"

OUT_DIR="/tmp/niuma-cache-stable-prefix-ab-${TIMESTAMP}"
CWD="$ROOT_DIR"
MODEL="glm-5.1"
SCENARIO="agent"
SEQUENCE="baseline:warmup,candidate:warmup,baseline,candidate,candidate,baseline,baseline,candidate"
PROMPT=""
TOOLS=""
MAX_TURNS=""
MAX_TOKENS=""
PERMISSION_MODE="allow"
FORCE="false"
VERIFY_ONLY="false"
DRY_RUN="false"

usage() {
  cat <<'USAGE'
Usage:
  scripts/prompt-cache-stable-prefix-ab.sh [flags]

Runs an interleaved real-provider A/B harness for the
NIUMA_STABLE_PREFIX_SKILLS gate. Each run writes a prompt dump, transcript
diagnostics summary, and paired A/B report. The final aggregate report excludes
items marked with :warmup.

Flags:
  --out-dir <path>       Artifact directory. Default:
                         /tmp/niuma-cache-stable-prefix-ab-<timestamp>
  --cwd <path>           Workspace cwd for niuma. Default: repo root.
  --model <name>         Model to use. Default: glm-5.1.
  --scenario <name>      Scenario preset: agent or structure. Default: agent.
  --sequence <csv>       Comma-separated run order. Items are baseline or
                         candidate, optionally suffixed with :warmup.
                         Default:
                         baseline:warmup,candidate:warmup,baseline,candidate,
                         candidate,baseline,baseline,candidate
  --prompt <text>        Override scenario prompt.
  --tools <list>         Tool list passed to niuma. Scenario default:
                         agent=LS,Grep,Read; structure=LS.
  --max-turns <n>        Maximum turns. Scenario default: agent=4; structure=1.
  --max-tokens <n>       Optional max output tokens per turn.
  --permission-mode <m>  Permission mode. Default: allow.
  --verify-only          Do not run model; reuse existing run artifacts in
                         --out-dir and regenerate reports.
  --dry-run              Print the resolved plan and exit without model calls.
  --force                Remove existing --out-dir before running.
  -h, --help             Show this help.
USAGE
}

json_string() {
  local value="${1:-}"
  value="${value//\\/\\\\}"
  value="${value//\"/\\\"}"
  value="${value//$'\n'/\\n}"
  value="${value//$'\r'/\\r}"
  value="${value//$'\t'/\\t}"
  printf '"%s"' "$value"
}

require_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "missing required command: $cmd" >&2
    exit 2
  fi
}

lower_uuid() {
  uuidgen | tr '[:upper:]' '[:lower:]'
}

scenario_defaults() {
  case "$SCENARIO" in
    agent)
      if [[ -z "$PROMPT" ]]; then
        PROMPT="只读分析当前仓库 token cache diagnostics 和 skills_catalog stable-prefix 实验的实现边界。必须至少使用一次 LS 和一次 Grep 或 Read；不要修改文件。最后用 5 条以内要点回答：当前实现做了什么、A/B 应该比较哪些指标。"
      fi
      if [[ -z "$TOOLS" ]]; then
        TOOLS="LS,Grep,Read"
      fi
      if [[ -z "$MAX_TURNS" ]]; then
        MAX_TURNS="4"
      fi
      ;;
    structure)
      if [[ -z "$PROMPT" ]]; then
        PROMPT="只读回答 CACHE_AB_STRUCTURE_OK。不要调用工具，不要修改文件。"
      fi
      if [[ -z "$TOOLS" ]]; then
        TOOLS="LS"
      fi
      if [[ -z "$MAX_TURNS" ]]; then
        MAX_TURNS="1"
      fi
      ;;
    *)
      echo "unknown scenario: $SCENARIO" >&2
      exit 2
      ;;
  esac
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --out-dir)
      OUT_DIR="${2:?missing value for --out-dir}"
      shift 2
      ;;
    --cwd)
      CWD="${2:?missing value for --cwd}"
      shift 2
      ;;
    --model)
      MODEL="${2:?missing value for --model}"
      shift 2
      ;;
    --scenario)
      SCENARIO="${2:?missing value for --scenario}"
      shift 2
      ;;
    --sequence)
      SEQUENCE="${2:?missing value for --sequence}"
      shift 2
      ;;
    --prompt)
      PROMPT="${2:?missing value for --prompt}"
      shift 2
      ;;
    --tools)
      TOOLS="${2:?missing value for --tools}"
      shift 2
      ;;
    --max-turns)
      MAX_TURNS="${2:?missing value for --max-turns}"
      shift 2
      ;;
    --max-tokens)
      MAX_TOKENS="${2:?missing value for --max-tokens}"
      shift 2
      ;;
    --permission-mode)
      PERMISSION_MODE="${2:?missing value for --permission-mode}"
      shift 2
      ;;
    --verify-only)
      VERIFY_ONLY="true"
      shift
      ;;
    --dry-run)
      DRY_RUN="true"
      shift
      ;;
    --force)
      FORCE="true"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

scenario_defaults
require_cmd jq
require_cmd uuidgen

IFS=',' read -r -a RAW_ITEMS <<<"$SEQUENCE"
if [[ ${#RAW_ITEMS[@]} -eq 0 ]]; then
  echo "--sequence must not be empty" >&2
  exit 2
fi

if [[ "$DRY_RUN" == "true" ]]; then
  {
    printf '{\n'
    printf '  "out_dir": %s,\n' "$(json_string "$OUT_DIR")"
    printf '  "cwd": %s,\n' "$(json_string "$CWD")"
    printf '  "model": %s,\n' "$(json_string "$MODEL")"
    printf '  "scenario": %s,\n' "$(json_string "$SCENARIO")"
    printf '  "sequence": %s,\n' "$(json_string "$SEQUENCE")"
    printf '  "tools": %s,\n' "$(json_string "$TOOLS")"
    printf '  "max_turns": %s,\n' "$(json_string "$MAX_TURNS")"
    printf '  "max_tokens": %s,\n' "$(json_string "$MAX_TOKENS")"
    printf '  "permission_mode": %s,\n' "$(json_string "$PERMISSION_MODE")"
    printf '  "prompt": %s\n' "$(json_string "$PROMPT")"
    printf '}\n'
  }
  exit 0
fi

if [[ -e "$OUT_DIR" && "$VERIFY_ONLY" != "true" && "$FORCE" != "true" ]]; then
  echo "out dir already exists: $OUT_DIR (use --force or choose another --out-dir)" >&2
  exit 2
fi
if [[ -e "$OUT_DIR" && "$VERIFY_ONLY" != "true" && "$FORCE" == "true" ]]; then
  rm -rf "$OUT_DIR"
fi
mkdir -p "$OUT_DIR"

RUNS_JSONL="$OUT_DIR/runs.jsonl"
PAIRS_JSONL="$OUT_DIR/pairs.jsonl"
AGGREGATE_JSON="$OUT_DIR/aggregate-report.json"
: > "$RUNS_JSONL"
: > "$PAIRS_JSONL"

transcript_path_for_session() {
  local session_id="$1"
  find "$HOME/.niuma/projects" -name "${session_id}.jsonl" -print -quit 2>/dev/null || true
}

write_run_record() {
  local index="$1"
  local variant="$2"
  local warmup="$3"
  local session_id="$4"
  local dump_path="$5"
  local transcript_path="$6"
  local summary_path="$7"
  local log_path="$8"
  local exit_code="$9"
  jq -n -c \
    --argjson index "$index" \
    --arg variant "$variant" \
    --argjson warmup "$warmup" \
    --arg session_id "$session_id" \
    --arg dump "$dump_path" \
    --arg transcript "$transcript_path" \
    --arg summary "$summary_path" \
    --arg log "$log_path" \
    --argjson exit_code "$exit_code" \
    '{index:$index,variant:$variant,warmup:$warmup,session_id:$session_id,dump:$dump,transcript:$transcript,summary:$summary,log:$log,exit_code:$exit_code}' >> "$RUNS_JSONL"
}

run_one() {
  local index="$1"
  local item="$2"
  local variant="${item%%:*}"
  local suffix="${item#*:}"
  local warmup="false"
  if [[ "$suffix" == "warmup" && "$item" == *:* ]]; then
    warmup="true"
  fi
  if [[ "$variant" != "baseline" && "$variant" != "candidate" ]]; then
    echo "sequence item must be baseline or candidate, got: $item" >&2
    exit 2
  fi

  local session_id
  local dump_path
  local summary_path
  local log_path
  local transcript_path
  local exit_code=0
  local run_dir="$OUT_DIR/$(printf '%02d' "$index")-${variant}"
  local session_id_file="$run_dir/session_id"
  mkdir -p "$run_dir"
  if [[ "$VERIFY_ONLY" == "true" ]]; then
    if [[ ! -f "$session_id_file" ]]; then
      echo "missing session id for verify-only run: $session_id_file" >&2
      exit 2
    fi
    session_id="$(<"$session_id_file")"
  else
    session_id="$(lower_uuid)"
    printf '%s\n' "$session_id" > "$session_id_file"
  fi
  dump_path="$run_dir/prompt.jsonl"
  summary_path="$run_dir/summary.json"
  log_path="$run_dir/run.log"

  if [[ "$VERIFY_ONLY" != "true" ]]; then
    local -a cmd=("$GO_BIN" "run" "./cmd/niuma" "-p" "$PROMPT" "--model" "$MODEL" "--session-id" "$session_id" "--max-turns" "$MAX_TURNS" "--tools" "$TOOLS" "--permission-mode" "$PERMISSION_MODE" "--cwd" "$CWD")
    if [[ -n "$MAX_TOKENS" ]]; then
      cmd+=("--max-tokens" "$MAX_TOKENS")
    fi
    echo "cache A/B run $index: $variant warmup=$warmup session=$session_id" >&2
    if [[ "$variant" == "candidate" ]]; then
      NIUMA_STABLE_PREFIX_SKILLS=1 \
      NIUMA_DUMP_PROMPT_JSON="$dump_path" \
      NIUMA_DUMP_PROMPT_FULL=false \
      "${cmd[@]}" >"$log_path" 2>&1 || exit_code=$?
    else
      env -u NIUMA_STABLE_PREFIX_SKILLS \
      NIUMA_DUMP_PROMPT_JSON="$dump_path" \
      NIUMA_DUMP_PROMPT_FULL=false \
      "${cmd[@]}" >"$log_path" 2>&1 || exit_code=$?
    fi
  fi

  transcript_path="$(transcript_path_for_session "$session_id")"
  if [[ "$VERIFY_ONLY" == "true" && -s "$summary_path" ]]; then
    :
  else
    if [[ -z "$transcript_path" ]]; then
      echo "missing transcript for run $index session $session_id" >&2
      exit_code=2
    fi
    if [[ ! -s "$dump_path" ]]; then
      echo "missing prompt dump for run $index: $dump_path" >&2
      exit_code=2
    fi
  fi
  if [[ "$exit_code" -eq 0 && ! -s "$summary_path" ]]; then
    "$GO_BIN" run ./scripts/prompt-cache-diagnostics \
      --dump "$dump_path" \
      --session "$session_id" \
      --summary \
      --transcript "$transcript_path" > "$summary_path"
  fi
  write_run_record "$index" "$variant" "$warmup" "$session_id" "$dump_path" "$transcript_path" "$summary_path" "$log_path" "$exit_code"
  if [[ "$exit_code" -ne 0 ]]; then
    echo "run $index failed with exit code $exit_code; see $log_path" >&2
    exit "$exit_code"
  fi
}

index=0
for raw in "${RAW_ITEMS[@]}"; do
  item="$(echo "$raw" | xargs)"
  if [[ -z "$item" ]]; then
    continue
  fi
  index=$((index + 1))
  run_one "$index" "$item"
done

jq -c 'select(.warmup == false and .variant == "baseline")' "$RUNS_JSONL" > "$OUT_DIR/baseline-runs.jsonl"
jq -c 'select(.warmup == false and .variant == "candidate")' "$RUNS_JSONL" > "$OUT_DIR/candidate-runs.jsonl"
baseline_count="$(wc -l < "$OUT_DIR/baseline-runs.jsonl" | tr -d ' ')"
candidate_count="$(wc -l < "$OUT_DIR/candidate-runs.jsonl" | tr -d ' ')"
pair_count="$(( baseline_count < candidate_count ? baseline_count : candidate_count ))"

if [[ "$pair_count" -eq 0 ]]; then
  echo "need at least one non-warmup baseline and candidate run" >&2
  exit 2
fi

for ((i=1; i<=pair_count; i++)); do
  baseline_summary="$(sed -n "${i}p" "$OUT_DIR/baseline-runs.jsonl" | jq -r '.summary')"
  candidate_summary="$(sed -n "${i}p" "$OUT_DIR/candidate-runs.jsonl" | jq -r '.summary')"
  pair_report="$OUT_DIR/pair-${i}-ab-report.json"
  "$GO_BIN" run ./scripts/prompt-cache-ab-report \
    --baseline "$baseline_summary" \
    --candidate "$candidate_summary" \
    --baseline-label "baseline-${i}" \
    --candidate-label "candidate-${i}" > "$pair_report"
  jq -n -c --argjson pair "$i" --arg report "$pair_report" '{pair:$pair,report:$report}' >> "$PAIRS_JSONL"
done

/usr/bin/python3 - "$OUT_DIR" "$RUNS_JSONL" "$PAIRS_JSONL" "$AGGREGATE_JSON" <<'PY'
import json
import os
import sys

out_dir, runs_jsonl, pairs_jsonl, aggregate_path = sys.argv[1:]

def read_json(path):
    with open(path, "r", encoding="utf-8") as f:
        return json.load(f)

def read_jsonl(path):
    if not os.path.exists(path):
        return []
    out = []
    with open(path, "r", encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if line:
                out.append(json.loads(line))
    return out

def load_run(run):
    data = read_json(run["summary"])
    session = data["sessions"][0]
    usage = session.get("transcript_usage") or {}
    enriched = dict(run)
    enriched.update({
        "records": session.get("records", 0),
        "provider_cache_usage_state": session.get("provider_cache_usage_state", ""),
        "hit_input": session.get("provider_cache_hit_ratio_input", 0.0),
        "hit_total": session.get("provider_cache_hit_ratio_total_input", 0.0),
        "skills_catalog_position": session.get("skills_catalog_position"),
        "skills_catalog_hash": session.get("skills_catalog_hash", ""),
        "skills_catalog_bytes": session.get("skills_catalog_bytes", 0),
        "system_hash_stable": session.get("system_hash_stable", False),
        "cache_control_hash_stable": session.get("cache_control_hash_stable", False),
        "tools_hash_stable": session.get("tools_hash_stable", False),
        "message_prefix_hash_stable": session.get("message_prefix_hash_stable", False),
        "thinking_hash_stable": session.get("thinking_hash_stable", False),
        "changed_fields": session.get("request_signature_changed_fields", []),
        "input_tokens": usage.get("input_tokens", 0),
        "cache_read_tokens": usage.get("cache_read_input_tokens", 0),
        "cache_creation_tokens": usage.get("cache_creation_input_tokens", 0),
        "output_tokens": usage.get("output_tokens", 0),
    })
    try:
        with open(run["log"], "r", encoding="utf-8", errors="replace") as f:
            text = f.read()
        marker = '"tool_calls":'
        if marker in text:
            import re
            matches = re.findall(r'"tool_calls":(\d+)', text)
            enriched["tool_calls"] = int(matches[-1]) if matches else 0
        else:
            enriched["tool_calls"] = 0
    except OSError:
        enriched["tool_calls"] = 0
    return enriched

def stats(items):
    if not items:
        return {"count": 0}
    def avg(key):
        return sum(float(item.get(key, 0) or 0) for item in items) / len(items)
    def minv(key):
        return min(float(item.get(key, 0) or 0) for item in items)
    def maxv(key):
        return max(float(item.get(key, 0) or 0) for item in items)
    return {
        "count": len(items),
        "avg_hit_input": avg("hit_input"),
        "min_hit_input": minv("hit_input"),
        "max_hit_input": maxv("hit_input"),
        "avg_hit_total": avg("hit_total"),
        "min_hit_total": minv("hit_total"),
        "max_hit_total": maxv("hit_total"),
        "avg_input_tokens": avg("input_tokens"),
        "avg_cache_read_tokens": avg("cache_read_tokens"),
        "avg_output_tokens": avg("output_tokens"),
        "avg_tool_calls": avg("tool_calls"),
        "avg_records": avg("records"),
    }

runs = [load_run(run) for run in read_jsonl(runs_jsonl)]
measured = [run for run in runs if not run.get("warmup")]
baseline = [run for run in measured if run.get("variant") == "baseline"]
candidate = [run for run in measured if run.get("variant") == "candidate"]
pairs = []
for item in read_jsonl(pairs_jsonl):
    report = read_json(item["report"])
    pairs.append({
        "pair": item["pair"],
        "report": item["report"],
        "hit_input_delta": report["delta"].get("provider_cache_hit_ratio_input", 0),
        "hit_total_delta": report["delta"].get("provider_cache_hit_ratio_total_input", 0),
        "input_tokens_delta": report["delta"].get("input_tokens", 0),
        "cache_read_tokens_delta": report["delta"].get("cache_read_input_tokens", 0),
        "skills_catalog_position_delta": report["delta"].get("skills_catalog_position"),
        "signals": report.get("signals", []),
        "warnings": report.get("warnings", []),
    })

candidate_wins = sum(1 for pair in pairs if pair["hit_input_delta"] > 0)
pair_count = len(pairs)
baseline_stats = stats(baseline)
candidate_stats = stats(candidate)
avg_delta = {
    "hit_input": candidate_stats.get("avg_hit_input", 0) - baseline_stats.get("avg_hit_input", 0),
    "hit_total": candidate_stats.get("avg_hit_total", 0) - baseline_stats.get("avg_hit_total", 0),
    "input_tokens": candidate_stats.get("avg_input_tokens", 0) - baseline_stats.get("avg_input_tokens", 0),
    "cache_read_tokens": candidate_stats.get("avg_cache_read_tokens", 0) - baseline_stats.get("avg_cache_read_tokens", 0),
}
decision = "inconclusive"
if pair_count and candidate_wins / pair_count >= 0.70 and avg_delta["hit_input"] >= 0.05:
    decision = "candidate_supported"
elif pair_count and (avg_delta["hit_input"] <= -0.05 or (candidate_wins / pair_count <= 0.30 and avg_delta["hit_input"] <= -0.01)):
    decision = "candidate_not_supported"

aggregate = {
    "out_dir": out_dir,
    "decision": decision,
    "candidate_win_rate": (candidate_wins / pair_count) if pair_count else 0,
    "pair_count": pair_count,
    "baseline": baseline_stats,
    "candidate": candidate_stats,
    "avg_delta": avg_delta,
    "runs": runs,
    "pairs": pairs,
}
with open(aggregate_path, "w", encoding="utf-8") as f:
    json.dump(aggregate, f, indent=2, ensure_ascii=False)
    f.write("\n")
print(json.dumps(aggregate, indent=2, ensure_ascii=False))
PY

echo "aggregate: $AGGREGATE_JSON" >&2
