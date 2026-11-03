#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/product-env.sh"


ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
HOST="${NIUMA_WEBUI_DEV_HOST:-127.0.0.1}"
PORT="${NIUMA_WEBUI_DEV_PORT:-18080}"
AUTH_TOKEN="${NIUMA_WEBUI_DEV_AUTH_TOKEN:-test-token}"
DB_NAME="${NIUMA_WEBUI_DEV_DB:-golang_cc_webui_local}"
MYSQL_DSN="${NIUMA_MYSQL_DSN:-root@tcp(127.0.0.1:3306)/${DB_NAME}?multiStatements=true&parseTime=true&loc=UTC&time_zone=%27%2B00%3A00%27&charset=utf8mb4}"

if [[ -n "${NIUMA_MOBILE_JWT_SECRET:-}" || -n "${MOBILE_JWT_SECRET:-}" ]]; then
  cat >&2 <<'EOF'
webui-dev refuses to start with a mobile JWT secret.
Local WebUI uses Mobile dev auth so the browser can call /mobile/chat with X-Tenant-Key/X-User-Id.
Unset NIUMA_MOBILE_JWT_SECRET and MOBILE_JWT_SECRET, or run the server manually for JWT testing.
EOF
  exit 2
fi

export NIUMA_MYSQL_DSN="${MYSQL_DSN}"
export NIUMA_MOBILE_DEV_AUTH=true

echo "webui-dev: tenant storage db=${DB_NAME}"
echo "webui-dev: mobile auth=dev-auth"
echo "webui-dev: api=http://${HOST}:${PORT}"

cd "${ROOT}"
go run ./cmd/niuma server --host "${HOST}" --port "${PORT}" --auth-token "${AUTH_TOKEN}"
