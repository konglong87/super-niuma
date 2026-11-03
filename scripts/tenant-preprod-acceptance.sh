#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/product-env.sh"


BASE_URL="${NIUMA_PREPROD_BASE_URL:-http://127.0.0.1:18080}"
AUTH_TOKEN="${NIUMA_PREPROD_AUTH_TOKEN:-test-token}"
MYSQL_DSN="${NIUMA_PREPROD_MYSQL_DSN:-${NIUMA_MYSQL_DSN:-}}"
TENANT_A="${NIUMA_PREPROD_TENANT_A:-yutang}"
TENANT_B="${NIUMA_PREPROD_TENANT_B:-tenant-b}"
USER_KEY="${NIUMA_PREPROD_USER:-preprod-user}"
SKILL_KEY="${NIUMA_PREPROD_SKILL:-preprod-tenant-skill}"

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required command: $1" >&2
    exit 1
  }
}

need curl
need jq

api() {
  local tenant="$1"
  local method="$2"
  local path="$3"
  local body="${4:-}"
  if [[ -n "$body" ]]; then
    curl -fsS -X "$method" "$BASE_URL$path" \
      -H "Authorization: Bearer $AUTH_TOKEN" \
      -H "Content-Type: application/json" \
      -H "X-Tenant-Key: $tenant" \
      -H "X-User-Id: $USER_KEY" \
      --data "$body"
  else
    curl -fsS -X "$method" "$BASE_URL$path" \
      -H "Authorization: Bearer $AUTH_TOKEN" \
      -H "X-Tenant-Key: $tenant" \
      -H "X-User-Id: $USER_KEY"
  fi
}

echo "Checking main server status"
api "$TENANT_A" GET /health >/dev/null

echo "Preparing tenant A user"
api "$TENANT_A" PATCH /tenant/user "$(jq -n '{
  display_name: "Preprod Tenant A User",
  role: "owner",
  status: "active"
}')" >/dev/null

echo "Ensuring tenant B exists"
api "$TENANT_A" POST /tenant/tenants "$(jq -n --arg tenant "$TENANT_B" '{
  tenant_key: $tenant,
  name: "Tenant B",
  status: "active"
}')" >/dev/null

echo "Preparing tenant B user"
api "$TENANT_B" PATCH /tenant/user "$(jq -n '{
  display_name: "Preprod Tenant B User",
  role: "owner",
  status: "active"
}')" >/dev/null

echo "Running tenant A skills smoke"
NIUMA_TENANT_SKILLS_SMOKE_BASE_URL="$BASE_URL" \
NIUMA_TENANT_SKILLS_SMOKE_AUTH_TOKEN="$AUTH_TOKEN" \
NIUMA_TENANT_SKILLS_SMOKE_TENANT="$TENANT_A" \
NIUMA_TENANT_SKILLS_SMOKE_USER="$USER_KEY" \
NIUMA_TENANT_SKILLS_SMOKE_SKILL="$SKILL_KEY" \
NIUMA_TENANT_SKILLS_SMOKE_MYSQL_DSN="$MYSQL_DSN" \
  "$(dirname "$0")/tenant-skills-runtime-smoke.sh"

echo "Verifying tenant B cannot see tenant A skill"
api "$TENANT_B" GET "/tenant/effective-skills?enabled=true&limit=50" \
  | jq -e --arg key "$SKILL_KEY" '(.data // []) | map(.skill_key) | index($key) == null' >/dev/null

echo "Running chat history smoke"
NIUMA_CHAT_HISTORY_SMOKE_BASE_URL="$BASE_URL" \
NIUMA_CHAT_HISTORY_SMOKE_AUTH_TOKEN="$AUTH_TOKEN" \
NIUMA_CHAT_HISTORY_SMOKE_TENANT="$TENANT_A" \
NIUMA_CHAT_HISTORY_SMOKE_USER="$USER_KEY" \
NIUMA_CHAT_HISTORY_SMOKE_MYSQL_DSN="$MYSQL_DSN" \
  "$(dirname "$0")/tenant-chat-history-smoke.sh"

if [[ "${NIUMA_PREPROD_SKIP_MOBILE_WS:-0}" != "1" ]]; then
  "$(dirname "$0")/mobile-ws-sync-acceptance.sh"
fi

echo "tenant preprod acceptance passed"
