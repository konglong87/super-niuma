#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT_DIR/scripts/lib/product-env.sh"
GO_BIN="${GO_BIN:-go}"
OUT_DIR="${OUT_DIR:-/tmp/gocc-tui-session-replay-$(date +%Y%m%d-%H%M%S)}"
FIXTURE_DIR="${TUI_REPLAY_FIXTURE_DIR:-${ROOT_DIR}/internal/tui/testdata/session-replay}"
SESSION_DIR="${NIUMA_SESSION_DIR:-$HOME/.niuma/projects}}"
# Recorded sessions are machine-local, so they are replayed as an advisory extra
# pass only. Set to 0 to replay the in-repo fixtures alone.
LOCAL_SESSION_LIMIT="${TUI_REPLAY_LOCAL_SESSIONS:-2}"

mkdir -p "$OUT_DIR"

gate_source="none"
gate_files=()
local_files=()
local_status="not-run"

write_status() {
  GOCC_STATUS="$1" \
  GOCC_DETAIL="$2" \
  GOCC_OUT="$OUT_DIR" \
  GOCC_GATE_SOURCE="$gate_source" \
  GOCC_LOCAL_STATUS="$local_status" \
  GOCC_GATE_FILES="$(printf '%s\n' ${gate_files[@]+"${gate_files[@]}"})" \
  GOCC_LOCAL_FILES="$(printf '%s\n' ${local_files[@]+"${local_files[@]}"})" \
  python3 - <<'PY'
import json
import os
import pathlib


def lines(name):
    return [line for line in os.environ.get(name, "").splitlines() if line.strip()]


status = {
    "schema_version": "niuma/tui-session-replay-acceptance/v1",
    "status": os.environ["GOCC_STATUS"],
    "detail": os.environ["GOCC_DETAIL"],
    "gate_source": os.environ["GOCC_GATE_SOURCE"],
    "gate_files": lines("GOCC_GATE_FILES"),
    "local_sessions": lines("GOCC_LOCAL_FILES"),
    "local_session_status": os.environ["GOCC_LOCAL_STATUS"],
}
out = pathlib.Path(os.environ["GOCC_OUT"]) / "status.json"
out.write_text(json.dumps(status, ensure_ascii=False, indent=2) + "\n")
PY
}

fixtures=()
while IFS= read -r file; do
  if [[ -n "$file" ]]; then
    fixtures+=("$file")
  fi
done <<<"$(find "$FIXTURE_DIR" -maxdepth 1 -name '*.jsonl' 2>/dev/null | sort)"

if [[ "$LOCAL_SESSION_LIMIT" -gt 0 && -d "$SESSION_DIR" ]]; then
  # Skip transcripts too small to contain a tool round trip, and ones large
  # enough to dominate the step's runtime. Most recent first.
  local_selection="$(python3 -c '
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
limit = int(sys.argv[2])
found = [p for p in root.rglob("*.jsonl") if 4096 <= p.stat().st_size <= 524288]
found.sort(key=lambda p: p.stat().st_mtime, reverse=True)
for path in found[:limit]:
    print(path)
' "$SESSION_DIR" "$LOCAL_SESSION_LIMIT")"
  while IFS= read -r file; do
    if [[ -n "$file" ]]; then
      local_files+=("$file")
    fi
  done <<<"$local_selection"
fi

if [[ "${#fixtures[@]}" -gt 0 ]]; then
  gate_source="fixtures"
  gate_files=("${fixtures[@]}")
elif [[ "${#local_files[@]}" -gt 0 ]]; then
  # No fixtures in the checkout: gate on the recorded sessions instead.
  gate_source="local-sessions"
  gate_files=("${local_files[@]}")
  local_files=()
fi

if [[ "${#gate_files[@]}" -eq 0 ]]; then
  write_status skipped "no fixture under ${FIXTURE_DIR} and no usable session under ${SESSION_DIR}"
  echo "session replay TUI acceptance: SKIPPED (no fixture and no usable recorded session)"
  echo "session replay TUI acceptance status: ${OUT_DIR}/status.json"
  exit 0
fi

gate_csv="$(IFS=,; echo "${gate_files[*]}")"

cd "$ROOT_DIR"
NIUMA_TUI_SESSION_REPLAY_OUT="$OUT_DIR" \
NIUMA_TUI_SESSION_REPLAY_FILES="$gate_csv" \
  "$GO_BIN" test ./internal/tui -run TestModelSessionReplayAcceptanceExport -count=1

if [[ "${#local_files[@]}" -gt 0 ]]; then
  local_out="$OUT_DIR/local-sessions"
  mkdir -p "$local_out"
  local_csv="$(IFS=,; echo "${local_files[*]}")"
  if NIUMA_TUI_SESSION_REPLAY_OUT="$local_out" \
    NIUMA_TUI_SESSION_REPLAY_FILES="$local_csv" \
    "$GO_BIN" test ./internal/tui -run TestModelSessionReplayAcceptanceExport -count=1 \
    >"$local_out/replay.log" 2>&1; then
    local_status="ok"
  else
    local_status="failed"
    echo "warning: recorded session replay failed (advisory, not gating): $local_out/replay.log" >&2
  fi
fi

python3 - "$OUT_DIR" <<'PY'
import json
import pathlib
import sys

out = pathlib.Path(sys.argv[1])
report = json.loads((out / "report.json").read_text())
if not report:
    raise SystemExit("empty replay report")
for entry in report:
    line_index = entry["line_index"]
    if "assistant_before_tool" in line_index and "first_tool" in line_index and "assistant_after_tool" in line_index:
        if not (line_index["assistant_before_tool"] < line_index["first_tool"] < line_index["assistant_after_tool"]):
            raise SystemExit(f"bad replay order for {entry['session']}: {line_index}")
    if line_index.get("last_assistant_tail", -1) < 0:
        raise SystemExit(f"missing assistant tail for {entry['session']}: {line_index}")

for txt in out.glob("*.txt"):
    lines = txt.read_text(errors="replace").splitlines()
    width = max([len(line) for line in lines] + [1])
    height = max(len(lines), 1)
    cell_w, cell_h = 8, 16
    img_w, img_h = width * cell_w, height * cell_h
    ppm = out / (txt.stem + ".ppm")
    with ppm.open("wb") as f:
        f.write(f"P6\n{img_w} {img_h}\n255\n".encode())
        for y in range(img_h):
            line_idx = y // cell_h
            row = bytearray()
            text = lines[line_idx] if line_idx < len(lines) else ""
            for x in range(img_w):
                col = x // cell_w
                ink = col < len(text) and text[col] != " "
                row.extend((230, 235, 242) if ink else (31, 36, 46))
            f.write(row)
PY

if command -v sips >/dev/null 2>&1; then
  for ppm in "$OUT_DIR"/*.ppm; do
    sips -s format png "$ppm" --out "${ppm%.ppm}.png" >/dev/null
  done
fi

write_status ok "replayed ${#gate_files[@]} gate transcript(s) from ${gate_source}"
echo "session replay TUI acceptance artifacts: $OUT_DIR"
