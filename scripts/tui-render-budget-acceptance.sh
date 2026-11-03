#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO_BIN="${GO_BIN:-go}"
OUT_DIR="${OUT_DIR:-/tmp/gocc-tui-render-budget-$(date +%Y%m%d-%H%M%S)}"

mkdir -p "$OUT_DIR"

NIUMA_TUI_RENDER_BUDGET_OUT="$OUT_DIR" "$GO_BIN" test ./internal/tui -run TestModelRenderBudgetAcceptanceExport -count=1

if command -v tmux >/dev/null 2>&1; then
  for spec in 150x40 120x32 96x24; do
    width="${spec%x*}"
    height="${spec#*x}"
    session="gocc-render-budget-${spec}-$$"
    view_file="$OUT_DIR/view-${spec}.txt"
    pane_file="$OUT_DIR/pane-${spec}.txt"
    tmux new-session -d -x "$width" -y "$height" -s "$session" "cat '$view_file'; sleep 2"
    sleep 0.4
    tmux capture-pane -t "$session" -p -S -"$height" > "$pane_file"
    tmux kill-session -t "$session" >/dev/null 2>&1 || true
    if grep -q 'last=B$' "$pane_file"; then
      echo "usage tail clipped in $pane_file" >&2
      exit 1
    fi
    if grep -q 'ctrl+v paste imag$' "$pane_file"; then
      echo "controls image hint clipped in $pane_file" >&2
      exit 1
    fi
    grep -q 'last=Bash' "$pane_file"
    grep -q 'ctrl+v paste image' "$pane_file"
    grep -q 'controls' "$pane_file"
  done
else
  python3 - "$OUT_DIR" <<'PY'
import fcntl
import os
import pathlib
import pty
import select
import struct
import sys
import termios
import time

out = pathlib.Path(sys.argv[1])
for spec in ("150x40", "120x32", "96x24"):
    width, height = map(int, spec.split("x"))
    view_file = out / f"view-{spec}.txt"
    pane_file = out / f"pane-{spec}.txt"
    pid, fd = pty.fork()
    if pid == 0:
        os.execlp("cat", "cat", str(view_file))
    fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", height, width, 0, 0))
    chunks = []
    deadline = time.time() + 3
    while time.time() < deadline:
        ready, _, _ = select.select([fd], [], [], 0.1)
        if not ready:
            try:
                done, _ = os.waitpid(pid, os.WNOHANG)
            except ChildProcessError:
                break
            if done:
                break
            continue
        try:
            data = os.read(fd, 4096)
        except OSError:
            break
        if not data:
            break
        chunks.append(data)
    try:
        os.close(fd)
    except OSError:
        pass
    try:
        os.waitpid(pid, 0)
    except ChildProcessError:
        pass
    text = b"".join(chunks).decode(errors="replace").replace("\r\n", "\n").replace("\r", "\n")
    pane_file.write_text(text)
    if "last=B\n" in text:
        raise SystemExit(f"usage tail clipped in {pane_file}")
    if "ctrl+v paste imag\n" in text:
        raise SystemExit(f"controls image hint clipped in {pane_file}")
    for needle in ("last=Bash", "ctrl+v paste image", "controls"):
        if needle not in text:
            raise SystemExit(f"missing {needle!r} in {pane_file}")
PY
fi

if command -v python3 >/dev/null 2>&1; then
  python3 - "$OUT_DIR" <<'PY'
import pathlib
import sys

out = pathlib.Path(sys.argv[1])
for txt in out.glob("pane-*.txt"):
    lines = txt.read_text(errors="replace").splitlines()
    width = max([len(line) for line in lines] + [1])
    height = max(len(lines), 1)
    cell_w, cell_h = 8, 16
    img_w, img_h = width * cell_w, height * cell_h
    ppm = out / (txt.stem.replace("pane-", "screenshot-") + ".ppm")
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
fi

echo "render budget acceptance artifacts: $OUT_DIR"
