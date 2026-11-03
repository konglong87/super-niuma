#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/product-env.sh"


BASE_URL="${NIUMA_TENANT_SKILLS_SMOKE_BASE_URL:-http://127.0.0.1:18080}"
AUTH_TOKEN="${NIUMA_TENANT_SKILLS_SMOKE_AUTH_TOKEN:-test-token}"
TENANT="${NIUMA_TENANT_SKILLS_SMOKE_TENANT:-yutang}"
USER_KEY="${NIUMA_TENANT_SKILLS_SMOKE_USER:-smoke-user}"
DEVICE_ID="${NIUMA_TENANT_SKILLS_SMOKE_DEVICE:-smoke-device}"
SKILL_KEY="${NIUMA_TENANT_SKILLS_SMOKE_SKILL:-smoke-review-skill}"
SESSION_KEY="${NIUMA_TENANT_SKILLS_SMOKE_SESSION:-smoke-session-$(date +%s)}"
RUN_QUERY="${NIUMA_TENANT_SKILLS_SMOKE_QUERY:-0}"
MYSQL_DSN="${NIUMA_TENANT_SKILLS_SMOKE_MYSQL_DSN:-${NIUMA_MYSQL_DSN:-}}"

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required command: $1" >&2
    exit 1
  }
}

need curl
need jq

api() {
  local method="$1"
  local path="$2"
  local body="${3:-}"
  local response
  if [[ -n "$body" ]]; then
    response="$(curl -fsS -X "$method" "$BASE_URL$path" \
      -H "Authorization: Bearer $AUTH_TOKEN" \
      -H "Content-Type: application/json" \
      -H "X-Tenant-Key: $TENANT" \
      -H "X-User-Id: $USER_KEY" \
      -H "X-Device-Id: $DEVICE_ID" \
      --data "$body")"
  else
    response="$(curl -fsS -X "$method" "$BASE_URL$path" \
      -H "Authorization: Bearer $AUTH_TOKEN" \
      -H "X-Tenant-Key: $TENANT" \
      -H "X-User-Id: $USER_KEY" \
      -H "X-Device-Id: $DEVICE_ID")"
  fi
  printf '%s\n' "$response"
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
api GET /health >/dev/null

echo "Upserting current tenant user $TENANT/$USER_KEY"
api PATCH /tenant/user "$(jq -n '{
  display_name: "Tenant skill smoke user",
  role: "owner",
  status: "active"
}')" >/dev/null

echo "Saving tenant skill $SKILL_KEY"
skill_content="$(cat <<'EOF'
---
description: Smoke tenant skill
version: smoke-v1
allowed-tools:
  - Echo
---
Use this tenant smoke skill when validating runtime skill loading.
EOF
)"
api POST /tenant/skills "$(jq -n --arg key "$SKILL_KEY" --arg content "$skill_content" '{
  skill_key: $key,
  name: "Smoke Tenant Skill",
  version: 1,
  enabled: true,
  content_md: $content
}')" >/dev/null

echo "Saving skill override"
api POST /tenant/skill-overrides "$(jq -n --arg key "$SKILL_KEY" '{
  skill_key: $key,
  version: 1,
  enabled: true,
  config_json: "{\"source\":\"smoke\"}"
}')" >/dev/null

echo "Verifying effective skills"
effective="$(api GET "/tenant/effective-skills?enabled=true&limit=50")"
echo "$effective" | jq -e --arg key "$SKILL_KEY" '.data[] | select(.skill_key == $key)' >/dev/null

if [[ "$RUN_QUERY" == "1" ]]; then
  echo "Running optional query smoke for session $SESSION_KEY"
  stream_file="$(mktemp)"
  curl -fsS -N -X POST "$BASE_URL/query" \
    -H "Authorization: Bearer $AUTH_TOKEN" \
    -H "Content-Type: application/json" \
    -H "X-Tenant-Key: $TENANT" \
    -H "X-User-Id: $USER_KEY" \
    -H "X-Device-Id: $DEVICE_ID" \
    --data "$(jq -n --arg session "$SESSION_KEY" --arg prompt "Use skill $SKILL_KEY and reply ok." '{
      prompt: $prompt,
      session_key: $session,
      stream: true
    }')" | tee "$stream_file" >/dev/null
  if grep -q '^data:' "$stream_file"; then
    grep -E '"type":"(message_stop|error|skill_activated)"' "$stream_file" >/dev/null || {
      echo "query stream did not include expected SSE events: $stream_file" >&2
      exit 1
    }
  else
    jq -e --arg key "$SKILL_KEY" '
      (.response | length > 0) and
      ((.tool_calls // []) | any(.name == "Skill" and (.input | contains($key)) and (.output | contains("Smoke tenant skill"))))
    ' "$stream_file" >/dev/null || {
      echo "query JSON response did not show tenant Skill tool activation: $stream_file" >&2
      exit 1
    }
  fi
  api GET "/tenant/sessions?limit=20" | jq -e --arg session "$SESSION_KEY" '.data[] | select(.session_key == $session)' >/dev/null
fi

if [[ -n "$MYSQL_DSN" ]]; then
  need mysql
  echo "Verifying MySQL rows"
  mysql_query "select skill_key from tenant_skills where skill_key='${SKILL_KEY}' limit 1" | grep -F "$SKILL_KEY" >/dev/null
  if [[ "$RUN_QUERY" == "1" ]]; then
    mysql_query "select session_key from tenant_sessions where session_key='${SESSION_KEY}' limit 1" | grep -F "$SESSION_KEY" >/dev/null
  fi
fi

echo "tenant skills runtime smoke passed"
