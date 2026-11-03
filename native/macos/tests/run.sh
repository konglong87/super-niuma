#!/usr/bin/env bash
set -euo pipefail
NATIVE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BUILD_DIR="$(mktemp -d "$NATIVE_DIR/.safety-tests.XXXXXX")"
trap 'rm -rf "$BUILD_DIR"' EXIT
swiftc -O -target "$(uname -m)-apple-macosx14.0" -framework AppKit -framework ApplicationServices -framework CoreGraphics \
  -framework Foundation -framework ImageIO -framework UniformTypeIdentifiers \
  "$NATIVE_DIR/Protocol.swift" "$NATIVE_DIR/Diagnostics.swift" "$NATIVE_DIR/WindowReadiness.swift" "$NATIVE_DIR/Safety.swift" "$NATIVE_DIR/MouseButtonClient.swift" "$NATIVE_DIR/Platform.swift" "$NATIVE_DIR/WindowActivation.swift" \
  "$NATIVE_DIR/Engine.swift" "$NATIVE_DIR/tests/main.swift" -o "$BUILD_DIR/safety-tests"
"$BUILD_DIR/safety-tests"
