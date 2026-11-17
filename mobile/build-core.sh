#!/usr/bin/env bash
# Builds the Go phone core (mobile/niumacore) into the Android app's AAR with
# the gomobile version pinned in mobile/go.mod.
#
# Needs: Go 1.26+, JDK 17, the Android SDK (ANDROID_HOME) with an NDK under
# $ANDROID_HOME/ndk (or ANDROID_NDK_HOME set).
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
export ANDROID_HOME="${ANDROID_HOME:-$HOME/Library/Android/sdk}"
if [[ -z "${ANDROID_NDK_HOME:-}" ]]; then
	ndk="$(ls -d "$ANDROID_HOME"/ndk/* 2>/dev/null | sort -V | tail -1 || true)"
	[[ -n "$ndk" ]] || { echo "no NDK under $ANDROID_HOME/ndk" >&2; exit 1; }
	export ANDROID_NDK_HOME="$ndk"
fi
export GOSUMDB="${GOSUMDB:-sum.golang.org}"

cd "$here"
# gomobile runs the gobind binary from PATH; build the pinned one first.
bin="$here/android/build/tools"
mkdir -p "$bin" android/app/libs
go build -o "$bin/gobind" golang.org/x/mobile/cmd/gobind
export PATH="$bin:$PATH"

go tool gomobile bind \
	-target=android/arm64 \
	-androidapi 29 \
	-javapkg io.github.konglong87.superniuma \
	-trimpath -ldflags="-s -w" \
	-o android/app/libs/niumacore.aar \
	./niumacore

unzip -l android/app/libs/niumacore.aar | awk '/libgojni.so/ {printf "libgojni.so (arm64): %.1f MB\n", $1/1048576}'
