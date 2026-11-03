#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUTPUT="${1:-$ROOT_DIR/desktop-v2/build/bin/computer-helper-macos}"
mkdir -p "$(dirname "$OUTPUT")"
# Compile to a fresh inode; replacing a signed Mach-O in place can preserve a
# stale macOS code-signature pager and trigger CODESIGNING/Invalid Page later.
rm -f "$OUTPUT"
case "${MACOS_ARCH:-$(uname -m)}" in
  amd64) SWIFT_ARCH="x86_64" ;;
  arm64) SWIFT_ARCH="arm64" ;;
  x86_64) SWIFT_ARCH="x86_64" ;;
  *) printf 'unsupported macOS architecture\n' >&2; exit 1 ;;
esac
swiftc -O \
  -target "${SWIFT_ARCH}-apple-macosx${MACOSX_DEPLOYMENT_TARGET:-14.0}" \
  -framework AppKit \
  -framework ApplicationServices \
  -framework CoreGraphics \
  -framework Foundation \
  -framework ImageIO \
  -framework ScreenCaptureKit \
  -framework UniformTypeIdentifiers \
  "$ROOT_DIR/native/macos/Protocol.swift" \
  "$ROOT_DIR/native/macos/Diagnostics.swift" \
  "$ROOT_DIR/native/macos/WindowReadiness.swift" \
  "$ROOT_DIR/native/macos/Safety.swift" \
  "$ROOT_DIR/native/macos/MouseButtonClient.swift" \
  "$ROOT_DIR/native/macos/Platform.swift" \
  "$ROOT_DIR/native/macos/WindowActivation.swift" \
  "$ROOT_DIR/native/macos/Engine.swift" \
  "$ROOT_DIR/native/macos/main.swift" \
  -o "$OUTPUT"
chmod 0755 "$OUTPUT"
# When placed inside Contents/MacOS, turn the helper into a real nested app
# bundle so macOS TCC can identify it and list it in Privacy settings.
if [[ "$(basename "$(dirname "$OUTPUT")")" == "MacOS" ]]; then
  helper_contents="$(cd "$(dirname "$OUTPUT")/.." && pwd)"
  cp "$ROOT_DIR/native/macos/Info.plist" "$helper_contents/Info.plist"
fi
printf '%s\n' "$OUTPUT"
