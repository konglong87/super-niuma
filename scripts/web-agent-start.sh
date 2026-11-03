#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/product-env.sh"
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/web-agent-profile.sh"


ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PROFILE="${1:-${NIUMA_WEB_AGENT_PROFILE:-manual}}"
if [[ $# -gt 1 || "${PROFILE}" == "-h" || "${PROFILE}" == "--help" ]]; then
  cat <<'EOF'
Usage: scripts/web-agent-start.sh [manual|e2e]

Profiles:
  manual  Reuse golang_cc_webui_local / yutang / feishu-e2e-user.
  e2e     Use isolated golang_cc_web_agent_real_e2e / webui-local / webui-local-user.
EOF
  [[ "${PROFILE}" == "-h" || "${PROFILE}" == "--help" ]] && exit 0
  exit 2
fi
web_agent_profile_apply "${PROFILE}"
HOST="${NIUMA_WEB_AGENT_HOST:-127.0.0.1}"
PORT="${NIUMA_WEB_AGENT_PORT:-18087}"
AUTH_TOKEN="${NIUMA_WEB_AGENT_AUTH_TOKEN:-test-token}"
DB_NAME="${NIUMA_WEB_AGENT_DB}"
MYSQL_USER="${NIUMA_WEB_AGENT_MYSQL_USER:-root}"
MYSQL_HOST="${NIUMA_WEB_AGENT_MYSQL_HOST:-127.0.0.1}"
MYSQL_PORT="${NIUMA_WEB_AGENT_MYSQL_PORT:-3306}"
MYSQL_DSN="${NIUMA_WEB_AGENT_MYSQL_DSN:-${NIUMA_MYSQL_DSN:-${MYSQL_USER}@tcp(${MYSQL_HOST}:${MYSQL_PORT})/${DB_NAME}?multiStatements=true&parseTime=true&loc=UTC&time_zone=%27%2B00%3A00%27&charset=utf8mb4}}"
TENANT="${NIUMA_WEB_AGENT_TENANT}"
USER_ID="${NIUMA_WEB_AGENT_USER}"
MODEL="${NIUMA_WEB_AGENT_MODEL:-}"
PROVIDER="${NIUMA_WEB_AGENT_PROVIDER:-}"
IMAGE_PROVIDER="${NIUMA_WEB_AGENT_IMAGE_PROVIDER:-jiuan-responses-gpt-5.6sol}"
IMAGE_SETTINGS="${NIUMA_WEB_AGENT_IMAGE_SETTINGS:-{\"imageGeneration\":{\"enabled\":true,\"provider\":\"${IMAGE_PROVIDER}\",\"model\":\"gpt-image-2\"}}}"
SKIP_BUILD="${NIUMA_WEB_AGENT_SKIP_BUILD:-false}"
MOBILE_DEV_AUTH="${NIUMA_MOBILE_DEV_AUTH:-true}"

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "web-agent-start requires $1" >&2
    exit 2
  }
}

safe_ident() {
  local name="$1"
  local value="$2"
  if [[ ! "${value}" =~ ^[A-Za-z0-9_.@-]+$ ]]; then
    echo "web-agent-start: ${name} contains unsupported characters: ${value}" >&2
    exit 2
  fi
}

mysql_exec() {
  mysql -u"${MYSQL_USER}" -h"${MYSQL_HOST}" -P"${MYSQL_PORT}" "$@"
}

need go
need mysql
need npm
safe_ident "database" "${DB_NAME}"
safe_ident "tenant" "${TENANT}"
safe_ident "user" "${USER_ID}"
if [[ -n "${NIUMA_WEB_AGENT_MYSQL_DSN:-}" || -n "${NIUMA_MYSQL_DSN:-}" ]]; then
  web_agent_profile_validate_dsn "${MYSQL_DSN}" "${DB_NAME}"
fi

cd "${ROOT}"
if [[ "${SKIP_BUILD}" != "true" ]]; then
  npm --prefix web run build
fi

mysql_exec -e "CREATE DATABASE IF NOT EXISTS ${DB_NAME} CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;"
NIUMA_MYSQL_DSN="${MYSQL_DSN}" go run ./cmd/niuma tenant migrate up
mysql_exec "${DB_NAME}" <<SQL
INSERT INTO tenants (tenant_key, name, status)
VALUES ('${TENANT}', 'WebUI Local', 'active')
ON DUPLICATE KEY UPDATE name=VALUES(name), status=VALUES(status);

SET @tenant_id = (SELECT id FROM tenants WHERE tenant_key='${TENANT}');

INSERT INTO tenant_users (tenant_id, user_key, email, display_name, role, status)
VALUES (@tenant_id, '${USER_ID}', '${USER_ID}@example.test', 'WebUI Local User', 'owner', 'active')
ON DUPLICATE KEY UPDATE display_name=VALUES(display_name), role=VALUES(role), status=VALUES(status);
SQL

export NIUMA_WEBUI_DIR="${ROOT}/web/dist"
export NIUMA_MYSQL_DSN="${MYSQL_DSN}"
export NIUMA_MOBILE_DEV_AUTH="${MOBILE_DEV_AUTH}"
if [[ -n "${MODEL}" ]]; then
  export CLAUDE_CODE_MODEL="${MODEL}"
fi

echo "web-agent-start: profile=${PROFILE}"
echo "web-agent-start: db=${DB_NAME}"
echo "web-agent-start: tenant=${TENANT} user=${USER_ID}"
echo "web-agent-start: url=http://${HOST}:${PORT}/webui/agent?token=${AUTH_TOKEN}"
echo "web-agent-start: mobile_dev_auth=${MOBILE_DEV_AUTH} (local testing only)"
echo "web-agent-start: provider comes from env/config; this script does not set a stub provider"
server_args=(--settings "${IMAGE_SETTINGS}" server --host "${HOST}" --port "${PORT}" --auth-token "${AUTH_TOKEN}")
if [[ -n "${PROVIDER}" ]]; then
  server_args=(--provider "${PROVIDER}" "${server_args[@]}")
fi
if [[ -n "${MODEL}" ]]; then
  server_args=(--model "${MODEL}" "${server_args[@]}")
fi
exec go run ./cmd/niuma "${server_args[@]}"
