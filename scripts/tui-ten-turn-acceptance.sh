#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO_BIN="${GO_BIN:-go}"
OUT_DIR="${OUT_DIR:-/tmp/gocc-tui-ten-turn-$(date +%Y%m%d-%H%M%S)}"

mkdir -p "$OUT_DIR"

cd "$ROOT_DIR"
NIUMA_TUI_TEN_TURN_OUT="$OUT_DIR" "$GO_BIN" test ./internal/tui -run TestModelTenTurnTranscriptAcceptanceExport -count=1

python3 - "$OUT_DIR" <<'PY'
import json
import pathlib
import sys

out = pathlib.Path(sys.argv[1])
report = json.loads((out / "report.json").read_text())
if len(report) != 10:
    raise SystemExit(f"report entries = {len(report)}, want 10")
for entry in report:
    if entry["count"] != 1:
        raise SystemExit(f"{entry['answer']} count = {entry['count']}, want 1")
    if entry["live_present"]:
        raise SystemExit(f"{entry['answer']} remained in final live view")

tail_report = json.loads((out / "tail-guard-report.json").read_text())
tail = tail_report["tail"]
tail_text = (out / "long-tail-transcript.txt").read_text(errors="replace")
if tail not in tail_text:
    raise SystemExit(f"tail sentinel {tail!r} missing from long-tail transcript")
if tail_report["lines_after_tail"] > 2:
    raise SystemExit(
        "tail guard too large: "
        f"lines_after_tail={tail_report['lines_after_tail']} "
        f"chrome_height={tail_report['chrome_height']}"
    )
if tail_report["guard_lines"] != 1:
    raise SystemExit(
        "computed transcript guard should stay compact: "
        f"guard_lines={tail_report['guard_lines']} "
        f"chrome_height={tail_report['chrome_height']}"
    )

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

echo "ten-turn TUI acceptance artifacts: $OUT_DIR"
