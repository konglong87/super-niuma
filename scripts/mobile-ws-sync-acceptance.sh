#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/product-env.sh"


TEST_PATTERN="${NIUMA_MOBILE_WS_ACCEPTANCE_PATTERN:-TestMobileWebSocketMultiDeviceReceivesSubscribedStreamEvents|TestMobileWebSocketReceivesSubscribedStreamEvents|TestMobileStreamRegistryCancelIsScopedToPrincipal}"

echo "Running mobile WebSocket multi-device sync acceptance"
go test ./internal/server -run "$TEST_PATTERN" -count=1

echo "mobile WebSocket sync acceptance passed"
