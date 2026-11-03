#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO_BIN="${GO_BIN:-go}"
OUT_DIR="${OUT_DIR:-/tmp/gocc-tui-display-order-$(date +%Y%m%d-%H%M%S)}"

mkdir -p "$OUT_DIR"

cd "$ROOT_DIR"
NIUMA_TUI_DISPLAY_ORDER_OUT="$OUT_DIR" "$GO_BIN" test ./internal/tui -run TestModelLiveDisplayOrderAcceptanceExport -count=1

python3 - "$OUT_DIR" <<'PY'
import json
import pathlib
import sys

out = pathlib.Path(sys.argv[1])
report = json.loads((out / "report.json").read_text())

def entry(name):
    for item in report:
        if item["name"] == name:
            return item
    raise SystemExit(f"missing report entry {name}")

for before_name, after_name in (
    ("tool-first-before-usage", "tool-first-after-usage"),
    ("assistant-first-before-usage", "assistant-first-after-usage"),
):
    before = entry(before_name)["line_index"]
    after = entry(after_name)["line_index"]
    for key in ("assistant", "tool"):
        if before[key] >= 0 and before[key] != after[key]:
            raise SystemExit(f"{key} line moved for {before_name}: before={before[key]} after={after[key]}")

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

echo "display order acceptance artifacts: $OUT_DIR"
