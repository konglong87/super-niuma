#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/product-env.sh"


BASE_URL="${NIUMA_CHAT_HISTORY_SMOKE_BASE_URL:-http://127.0.0.1:18080}"
AUTH_TOKEN="${NIUMA_CHAT_HISTORY_SMOKE_AUTH_TOKEN:-test-token}"
TENANT="${NIUMA_CHAT_HISTORY_SMOKE_TENANT:-yutang}"
USER_KEY="${NIUMA_CHAT_HISTORY_SMOKE_USER:-history-smoke-user}"
DEVICE_A="${NIUMA_CHAT_HISTORY_SMOKE_DEVICE_A:-history-device-a}"
DEVICE_B="${NIUMA_CHAT_HISTORY_SMOKE_DEVICE_B:-history-device-b}"
SESSION_KEY="${NIUMA_CHAT_HISTORY_SMOKE_SESSION:-history-session-$(date +%s)}"
MYSQL_DSN="${NIUMA_CHAT_HISTORY_SMOKE_MYSQL_DSN:-${NIUMA_MYSQL_DSN:-}}"

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required command: $1" >&2
    exit 1
  }
}

need curl
need jq

api() {
  local device="$1"
  local method="$2"
  local path="$3"
  local body="${4:-}"
  if [[ -n "$body" ]]; then
    curl -fsS -X "$method" "$BASE_URL$path" \
      -H "Authorization: Bearer $AUTH_TOKEN" \
      -H "Content-Type: application/json" \
      -H "X-Tenant-Key: $TENANT" \
      -H "X-User-Id: $USER_KEY" \
      -H "X-Device-Id: $device" \
      --data "$body"
  else
    curl -fsS -X "$method" "$BASE_URL$path" \
      -H "Authorization: Bearer $AUTH_TOKEN" \
      -H "X-Tenant-Key: $TENANT" \
      -H "X-User-Id: $USER_KEY" \
      -H "X-Device-Id: $device"
  fi
}

mysql_query() {
  local sql="$1"
  local dsn_no_query="${MYSQL_DSN%%\?*}"
  local userpass="${dsn_no_query%@tcp(*}"
  local hostport_db="${dsn_no_query#*@tcp(}"
  local hostport="${hostport_db%%)*}"
  local database="${hostport_db#*)/}"
  local host="${hostport%%:*}"
  local port="${hostport##*:}"
  local user="${userpass%%:*}"
  local password=""
  if [[ "$userpass" == *:* ]]; then
    password="${userpass#*:}"
  fi
  local args=(-N -B -h "$host" -P "$port" -u "$user" "$database" -e "$sql")
  if [[ -n "$password" ]]; then
    MYSQL_PWD="$password" mysql "${args[@]}"
  else
    mysql "${args[@]}"
  fi
}

echo "Checking server health at $BASE_URL"
api "$DEVICE_A" GET /health >/dev/null

echo "Upserting tenant user $TENANT/$USER_KEY from $DEVICE_A"
api "$DEVICE_A" PATCH /tenant/user "$(jq -n '{
  display_name: "Chat history smoke user",
  role: "owner",
  status: "active"
}')" >/dev/null

echo "Creating session $SESSION_KEY from $DEVICE_A"
session_resp="$(api "$DEVICE_A" POST /tenant/sessions "$(jq -n --arg session "$SESSION_KEY" '{
  session_key: $session,
  title: "Chat history smoke",
  status: "active",
  model: "claude-test",
  cwd: "/workspace"
}')")"
session_id="$(echo "$session_resp" | jq -r '.id')"
if [[ -z "$session_id" || "$session_id" == "null" ]]; then
  echo "failed to create session: $session_resp" >&2
  exit 1
fi

echo "Writing messages from $DEVICE_A"
api "$DEVICE_A" POST /tenant/messages "$(jq -n --argjson session "$session_id" '{
  session_id: $session,
  turn_index: 1,
  role: "user",
  content: "history smoke user message"
}')" >/dev/null
api "$DEVICE_A" POST /tenant/messages "$(jq -n --argjson session "$session_id" '{
  session_id: $session,
  turn_index: 2,
  role: "assistant",
  content: "history smoke assistant message",
  model: "claude-test"
}')" >/dev/null

echo "Reading session and messages from $DEVICE_B"
api "$DEVICE_B" GET "/tenant/sessions?limit=50" | jq -e --arg session "$SESSION_KEY" '.data[] | select(.session_key == $session)' >/dev/null
api "$DEVICE_B" GET "/tenant/messages?session_id=$session_id&limit=20" \
  | jq -e '[.data[]?.content] | index("history smoke user message") != null and index("history smoke assistant message") != null' >/dev/null

if [[ -n "$MYSQL_DSN" ]]; then
  need mysql
  echo "Verifying MySQL rows"
  mysql_query "select session_key from tenant_sessions where session_key='${SESSION_KEY}' limit 1" | grep -F "$SESSION_KEY" >/dev/null
  mysql_query "select count(*) from tenant_session_messages m join tenant_sessions s on s.id=m.session_id where s.session_key='${SESSION_KEY}'" | awk '$1 >= 2 { found=1 } END { exit found ? 0 : 1 }'
fi

echo "tenant chat history smoke passed"
