#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/product-env.sh"


ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
WORK_DIR="/tmp/niuma-guidance-source-${TIMESTAMP}"
FORCE="false"
MODEL=""
PROMPT_PROFILE="claude-compatible"

usage() {
  cat <<'USAGE'
Usage:
  scripts/workspace-guidance-source-acceptance.sh [flags]

Builds a temporary niuma binary, then runs isolated HOME/CLAUDE_CONFIG_DIR
prompt-dump probes for workspace guidance source loading:
  - ~/.claude/CLAUDE.md only
  - project CLAUDE.md plus AGENTS.md in the same directory
  - project AGENTS.md fallback when project CLAUDE.md is absent
  - project CLAUDE.md plus .claude/CLAUDE.md plus AGENTS.md

The script writes only under --work-dir and /tmp prompt dump paths.

Flags:
  --work-dir <path>  Output/work directory. Default: /tmp/niuma-guidance-source-<timestamp>.
  --model <name>     Optional model override passed to the temporary binary.
  --prompt-profile <name>
                     Prompt profile. Default: claude-compatible.
                     claude-compatible-strict expects AGENTS.md fallback to be absent.
  --force            Remove existing --work-dir first.
  -h, --help         Show this help.
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --work-dir)
      WORK_DIR="${2:?missing value for --work-dir}"
      shift 2
      ;;
    --model)
      MODEL="${2:?missing value for --model}"
      shift 2
      ;;
    --prompt-profile)
      PROMPT_PROFILE="${2:?missing value for --prompt-profile}"
      shift 2
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

if [[ -e "$WORK_DIR" && "$FORCE" != "true" ]]; then
  echo "work dir already exists: $WORK_DIR (use --force or choose another --work-dir)" >&2
  exit 2
fi
if [[ -e "$WORK_DIR" ]]; then
  rm -rf "$WORK_DIR"
fi
mkdir -p "$WORK_DIR"

BIN="$WORK_DIR/niuma"
REPORT_JSONL="$WORK_DIR/report.jsonl"
REPORT_JSON="$WORK_DIR/report.json"
HOST_HOME="${HOME:-}"

echo "building temporary binary: $BIN" >&2
(cd "$ROOT_DIR" && go build -o "$BIN" ./cmd/niuma)

copy_auth_settings() {
  local target_home="$1"
  local source_settings=""
  if [[ -n "$HOST_HOME" && -f "$HOST_HOME/.niuma/settings.json" ]]; then
    source_settings="$HOST_HOME/.niuma/settings.json"
  elif [[ -n "$HOST_HOME" && -f "$HOST_HOME/.niuma-legacy/settings.json" ]]; then
    source_settings="$HOST_HOME/.niuma-legacy/settings.json"
  fi
  if [[ -n "$source_settings" ]]; then
    mkdir -p "$target_home/.niuma"
    cp "$source_settings" "$target_home/.niuma/settings.json"
  fi
}

request_text() {
  local dump="$1"
  jq -r '
    [
      (.request.system_blocks[]?.text // empty),
      (.request.messages[]?.content[]? | select(.type == "text") | .text)
    ] | join("\n")
  ' "$dump"
}

check_contains() {
  local text_file="$1"
  local want="$2"
  if ! grep -Fq -- "$want" "$text_file"; then
    echo "missing required request text: $want" >&2
    return 1
  fi
}

check_absent() {
  local text_file="$1"
  local forbidden="$2"
  if grep -Fq -- "$forbidden" "$text_file"; then
    echo "request contained forbidden text: $forbidden" >&2
    return 1
  fi
}

run_case() {
  local name="$1"
  local required_csv="$2"
  local forbidden_csv="$3"
  local case_dir="$WORK_DIR/$name"
  local home="$case_dir/home"
  local config="$case_dir/claude-config"
  local workspace="$case_dir/workspace"
  local dump="$case_dir/prompt.jsonl"
  local log="$case_dir/run.log"
  local text="$case_dir/request.txt"
  mkdir -p "$home/.claude" "$config" "$workspace"
  copy_auth_settings "$home"

  case "$name" in
    user_only)
      cat > "$home/.claude/CLAUDE.md" <<'EOF_USER'
USER_GUIDANCE_MARKER
EOF_USER
      ;;
    project_claude_preempts_agents)
      cat > "$home/.claude/CLAUDE.md" <<'EOF_USER'
USER_GUIDANCE_MARKER
EOF_USER
      cat > "$workspace/CLAUDE.md" <<'EOF_PROJECT'
PROJECT_CLAUDE_MARKER
EOF_PROJECT
      cat > "$workspace/AGENTS.md" <<'EOF_AGENTS'
PROJECT_AGENTS_SHOULD_NOT_LOAD
EOF_AGENTS
      ;;
    project_agents_fallback)
      cat > "$home/.claude/CLAUDE.md" <<'EOF_USER'
USER_GUIDANCE_MARKER
EOF_USER
      cat > "$workspace/AGENTS.md" <<'EOF_AGENTS'
PROJECT_AGENTS_MARKER
EOF_AGENTS
      ;;
    project_and_dot_claude_preempt_agents)
      cat > "$home/.claude/CLAUDE.md" <<'EOF_USER'
USER_GUIDANCE_MARKER
EOF_USER
      cat > "$workspace/CLAUDE.md" <<'EOF_PROJECT'
PROJECT_CLAUDE_MARKER
EOF_PROJECT
      mkdir -p "$workspace/.claude"
      cat > "$workspace/.claude/CLAUDE.md" <<'EOF_DOT'
PROJECT_DOT_CLAUDE_MARKER
EOF_DOT
      cat > "$workspace/AGENTS.md" <<'EOF_AGENTS'
PROJECT_AGENTS_SHOULD_NOT_LOAD
EOF_AGENTS
      ;;
    *)
      echo "unknown case: $name" >&2
      return 2
      ;;
  esac

  local prompt="只回答一句话：WORKSPACE_GUIDANCE_SOURCE_CHECK。不要调用工具。"
  local cmd=("$BIN" --cwd "$workspace" --max-turns 1 --tools Read -p "$prompt")
  if [[ -n "$MODEL" ]]; then
    cmd+=(--model "$MODEL")
  fi

  echo "running case: $name" >&2
  run_status=0
  (
    export HOME="$home"
    export CLAUDE_CONFIG_DIR="$config"
    export NIUMA_DUMP_PROMPT_JSON="$dump"
    export NIUMA_DUMP_PROMPT_FULL="true"
    export NIUMA_PROMPT_PROFILE="$PROMPT_PROFILE"
    "${cmd[@]}"
  ) >"$log" 2>&1 || run_status=$?

  if [[ ! -s "$dump" ]]; then
    echo "prompt dump missing for $name; run_status=$run_status log=$log" >&2
    sed -n '1,160p' "$log" >&2 || true
    return 1
  fi

  request_text "$dump" > "$text"
  IFS=',' read -ra required <<<"$required_csv"
  for item in "${required[@]}"; do
    item="${item#"${item%%[![:space:]]*}"}"
    item="${item%"${item##*[![:space:]]}"}"
    [[ -z "$item" ]] && continue
    check_contains "$text" "$item"
  done
  IFS=',' read -ra forbidden <<<"$forbidden_csv"
  for item in "${forbidden[@]}"; do
    item="${item#"${item%%[![:space:]]*}"}"
    item="${item%"${item##*[![:space:]]}"}"
    [[ -z "$item" ]] && continue
    check_absent "$text" "$item"
  done

  jq -c --arg name "$name" --arg profile "$PROMPT_PROFILE" --arg dump "$dump" --arg log "$log" --argjson run_status "$run_status" '
    {
      case: $name,
      prompt_profile: $profile,
      ok: true,
      run_status: $run_status,
      dump: $dump,
      log: $log,
      system_block_count,
      system_bytes,
      user_messages: ([.request.messages[]? | select(.role == "user")] | length),
      document_summary: (.context_manifest.code_context.document_summary // []),
      by_type: (.context_manifest.code_context.by_type // {}),
      by_type_bytes: (.context_manifest.code_context.by_type_bytes // {}),
      prompt_bytes: (.context_manifest.code_context.prompt_bytes // 0),
      workflow_rules: (.context_manifest.code_context.workflow_rules // 0)
    }
  ' "$dump" >> "$REPORT_JSONL"
}

run_case "user_only" \
  "USER_GUIDANCE_MARKER,# claudeMd,# currentDate" \
  "PROJECT_CLAUDE_MARKER,PROJECT_AGENTS_MARKER,PROJECT_AGENTS_SHOULD_NOT_LOAD,PROJECT_DOT_CLAUDE_MARKER"

run_case "project_claude_preempts_agents" \
  "USER_GUIDANCE_MARKER,PROJECT_CLAUDE_MARKER,# claudeMd,# currentDate" \
  "PROJECT_AGENTS_SHOULD_NOT_LOAD,PROJECT_AGENTS_MARKER,PROJECT_DOT_CLAUDE_MARKER"

if [[ "$PROMPT_PROFILE" == "claude-compatible-strict" ]]; then
  run_case "project_agents_fallback" \
    "USER_GUIDANCE_MARKER,# claudeMd,# currentDate" \
    "PROJECT_AGENTS_MARKER,PROJECT_CLAUDE_MARKER,PROJECT_AGENTS_SHOULD_NOT_LOAD,PROJECT_DOT_CLAUDE_MARKER"
else
  run_case "project_agents_fallback" \
    "USER_GUIDANCE_MARKER,PROJECT_AGENTS_MARKER,# claudeMd,# currentDate" \
    "PROJECT_CLAUDE_MARKER,PROJECT_AGENTS_SHOULD_NOT_LOAD,PROJECT_DOT_CLAUDE_MARKER"
fi

run_case "project_and_dot_claude_preempt_agents" \
  "USER_GUIDANCE_MARKER,PROJECT_CLAUDE_MARKER,PROJECT_DOT_CLAUDE_MARKER,# claudeMd,# currentDate" \
  "PROJECT_AGENTS_SHOULD_NOT_LOAD,PROJECT_AGENTS_MARKER"

jq -s '{ok: (all(.[]; .ok == true)), cases: .}' "$REPORT_JSONL" > "$REPORT_JSON"
cat "$REPORT_JSON"
echo "report: $REPORT_JSON" >&2
