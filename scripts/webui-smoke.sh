#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/product-env.sh"


BASE_URL="${NIUMA_WEBUI_SMOKE_BASE_URL:-http://127.0.0.1:18080}"
AUTH_TOKEN="${NIUMA_WEBUI_SMOKE_AUTH_TOKEN:-${NIUMA_WEBUI_DEV_AUTH_TOKEN:-test-token}}"
TENANT="${NIUMA_WEBUI_SMOKE_TENANT:-webui-local}"
USER_ID="${NIUMA_WEBUI_SMOKE_USER:-webui-local-user}"
DEVICE_ID="${NIUMA_WEBUI_SMOKE_DEVICE:-webui-local-device}"
SESSION_ID="${NIUMA_WEBUI_SMOKE_SESSION_ID:-}"

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "webui-smoke requires $1" >&2
    exit 2
  }
}

need curl
need jq

mobile_json="$(curl -fsS "${BASE_URL}/mobile/chat/sessions?limit=20" \
  -H "X-Tenant-Key: ${TENANT}" \
  -H "X-User-Id: ${USER_ID}" \
  -H "X-Device-Id: ${DEVICE_ID}")"

mobile_count="$(jq '.data | length' <<<"${mobile_json}")"
if [[ "${mobile_count}" -le 0 ]]; then
  echo "webui-smoke failed: /mobile/chat/sessions returned no sessions for ${TENANT}/${USER_ID}" >&2
  echo "${mobile_json}" >&2
  exit 1
fi

tenant_json="$(curl -fsS "${BASE_URL}/tenant/sessions?limit=20" \
  -H "Authorization: Bearer ${AUTH_TOKEN}" \
  -H "X-Tenant-Key: ${TENANT}" \
  -H "X-User-Id: ${USER_ID}")"

tenant_count="$(jq '.data | length' <<<"${tenant_json}")"
if [[ "${tenant_count}" -le 0 ]]; then
  echo "webui-smoke failed: /tenant/sessions returned no sessions for ${TENANT}/${USER_ID}" >&2
  echo "${tenant_json}" >&2
  exit 1
fi

if [[ -z "${SESSION_ID}" ]]; then
  SESSION_ID="$(jq -r '.data[0].id' <<<"${mobile_json}")"
fi

trace_json="$(curl -fsS "${BASE_URL}/trace/api/sessions/${SESSION_ID}?source=tenant&limit=200&trace_limit=100&task_limit=500" \
  -H "Authorization: Bearer ${AUTH_TOKEN}" \
  -H "X-Tenant-Key: ${TENANT}" \
  -H "X-User-Id: ${USER_ID}")"

trace_session="$(jq -r '.session_id // empty' <<<"${trace_json}")"
if [[ "${trace_session}" != "${SESSION_ID}" ]]; then
  echo "webui-smoke failed: trace session ${SESSION_ID} was not readable" >&2
  echo "${trace_json}" >&2
  exit 1
fi

token_summary="$(jq -r '.summary.token_summary // "no token summary"' <<<"${trace_json}")"

echo "webui-smoke ok"
echo "mobile sessions: ${mobile_count}"
echo "tenant sessions: ${tenant_count}"
echo "trace session: #${SESSION_ID}"
echo "token summary: ${token_summary}"
