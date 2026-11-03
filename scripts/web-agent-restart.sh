#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/product-env.sh"
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/web-agent-profile.sh"


ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PROFILE="${1:-${NIUMA_WEB_AGENT_PROFILE:-manual}}"
if [[ "${PROFILE}" == "-h" || "${PROFILE}" == "--help" ]]; then
  cat <<'EOF'
Usage: scripts/web-agent-restart.sh [manual|e2e]

Profiles:
  manual  Reuse golang_cc_webui_local / yutang / feishu-e2e-user.
  e2e     Use isolated golang_cc_web_agent_real_e2e / webui-local / webui-local-user.
EOF
  exit 0
fi
if [[ $# -gt 1 ]]; then
  echo "web-agent-restart: expected at most one profile argument" >&2
  exit 2
fi
web_agent_profile_apply "${PROFILE}"
HOST="${NIUMA_WEB_AGENT_HOST:-127.0.0.1}"
PORT="${NIUMA_WEB_AGENT_PORT:-18087}"
AUTH_TOKEN="${NIUMA_WEB_AGENT_AUTH_TOKEN:-test-token}"
SKIP_BUILD="${NIUMA_WEB_AGENT_SKIP_BUILD:-true}"

if command -v lsof >/dev/null 2>&1; then
  pids="$(lsof -tiTCP:"${PORT}" -sTCP:LISTEN || true)"
  if [[ -n "${pids}" ]]; then
    echo "web-agent-restart: stopping listeners on :${PORT}: ${pids}"
    kill ${pids} || true
    while lsof -nP -iTCP:"${PORT}" -sTCP:LISTEN >/dev/null 2>&1; do
      sleep 0.2
    done
  fi
else
  echo "web-agent-restart: lsof not found; cannot stop existing :${PORT} listener" >&2
  exit 2
fi

echo "web-agent-restart: starting http://${HOST}:${PORT}/webui/agent?token=${AUTH_TOKEN}"
cd "${ROOT}"
NIUMA_WEB_AGENT_HOST="${HOST}" \
NIUMA_WEB_AGENT_PORT="${PORT}" \
NIUMA_WEB_AGENT_AUTH_TOKEN="${AUTH_TOKEN}" \
NIUMA_WEB_AGENT_SKIP_BUILD="${SKIP_BUILD}" \
NIUMA_WEB_AGENT_PROFILE="${PROFILE}" \
exec ./scripts/web-agent-start.sh "${PROFILE}"
