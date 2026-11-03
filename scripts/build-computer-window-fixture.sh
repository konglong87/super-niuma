#!/usr/bin/env bash
# Local test fixture only: no signing, installation, distribution, or launch.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP="${1:-${ROOT}/desktop-v2/build/validation/$(date +%Y%m%d)/window-selection/WindowFixture.app}"
if [[ "$(uname -s)" != Darwin || "${APP}" != /*.app ]]; then
  echo 'macOS and an absolute .app output path are required' >&2
  exit 1
fi
mkdir -p "${APP}/Contents/MacOS"
swiftc -swift-version 6 -warnings-as-errors -parse-as-library -framework AppKit \
  "${ROOT}/native/macos/tests/window_fixture.swift" -o "${APP}/Contents/MacOS/window-fixture"
python3 - "${APP}/Contents/Info.plist" <<'PY'
import plistlib, sys
from pathlib import Path
Path(sys.argv[1]).write_bytes(plistlib.dumps({
    'CFBundleIdentifier': 'com.niuma.validation.window-fixture',
    'CFBundleExecutable': 'window-fixture', 'CFBundleName': 'WindowFixture',
    'CFBundlePackageType': 'APPL', 'NSHighResolutionCapable': True,
    'NSPrincipalClass': 'NSApplication',
}))
PY
printf '%s\n' "${APP}"
