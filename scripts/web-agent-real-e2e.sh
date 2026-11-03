#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/product-env.sh"
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/web-agent-profile.sh"


ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
web_agent_profile_apply e2e
HOST="${NIUMA_WEB_AGENT_HOST:-127.0.0.1}"
PORT="${NIUMA_WEB_AGENT_PORT:-18087}"
AUTH_TOKEN="${NIUMA_WEB_AGENT_AUTH_TOKEN:-test-token}"
BASE_URL="http://${HOST}:${PORT}"
DB_NAME="${NIUMA_WEB_AGENT_DB:-golang_cc_web_agent_real_e2e}"
TENANT="${NIUMA_WEB_AGENT_TENANT:-webui-local}"
USER_ID="${NIUMA_WEB_AGENT_USER:-webui-local-user}"
WORKSPACE="${NIUMA_WEB_AGENT_WORKSPACE:-${ROOT}}"
MODEL="${NIUMA_WEB_AGENT_MODEL:-glm-5.1}"
PROVIDER="${NIUMA_WEB_AGENT_PROVIDER:-}"
TRACE_ID="${NIUMA_WEB_AGENT_E2E_TRACE_ID:-web-agent-e2e-$(date -u +%Y%m%d%H%M%S)-$$}"
PROMPT="${NIUMA_WEB_AGENT_E2E_PROMPT:-请用一句中文回复：真实后端连通测试。不要使用英文。}"
EXPECTED="${NIUMA_WEB_AGENT_E2E_EXPECTED:-真实后端}"
STUB_TEXT="${NIUMA_WEB_AGENT_E2E_STUB_TEXT:-web agent browser flow ok}"
TIMEOUT_SECONDS="${NIUMA_WEB_AGENT_E2E_TIMEOUT_SECONDS:-90}"
AUTO_START="${NIUMA_WEB_AGENT_E2E_AUTO_START:-true}"
MYSQL_USER="${NIUMA_WEB_AGENT_MYSQL_USER:-root}"
MYSQL_HOST="${NIUMA_WEB_AGENT_MYSQL_HOST:-127.0.0.1}"
MYSQL_PORT="${NIUMA_WEB_AGENT_MYSQL_PORT:-3306}"
MYSQL_DSN="${NIUMA_MYSQL_DSN:-${MYSQL_USER}@tcp(${MYSQL_HOST}:${MYSQL_PORT})/${DB_NAME}?multiStatements=true&parseTime=true&loc=UTC&time_zone=%27%2B00%3A00%27&charset=utf8mb4}"

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "web-agent-real-e2e requires $1" >&2
    exit 2
  }
}

safe_ident() {
  local name="$1"
  local value="$2"
  if [[ ! "${value}" =~ ^[A-Za-z0-9_.@-]+$ ]]; then
    echo "web-agent-real-e2e: ${name} contains unsupported characters: ${value}" >&2
    exit 2
  fi
}

api() {
  local method="$1"
  local path="$2"
  local body="${3:-}"
  local trace_id="${4:-}"
  local args=(
    -fsS
    -X "${method}"
    "${BASE_URL}${path}"
    -H "Authorization: Bearer ${AUTH_TOKEN}"
    -H "X-Tenant-Key: ${TENANT}"
    -H "X-User-Id: ${USER_ID}"
  )
  if [[ -n "${trace_id}" ]]; then
    args+=(-H "X-Trace-Id: ${trace_id}")
  fi
  if [[ -n "${body}" ]]; then
    args+=(-H "Content-Type: application/json" -d "${body}")
  fi
  curl "${args[@]}"
}

wait_for_server() {
  for _ in $(seq 1 80); do
    if curl -fsS "${BASE_URL}/health" -H "Authorization: Bearer ${AUTH_TOKEN}" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.25
  done
  echo "web-agent-real-e2e failed: server did not become healthy at ${BASE_URL}" >&2
  return 1
}

cleanup() {
  if [[ -n "${SERVER_PID:-}" ]]; then
    kill "${SERVER_PID}" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

need curl
need jq
need mysql
need node
safe_ident "database" "${DB_NAME}"
safe_ident "tenant" "${TENANT}"
safe_ident "user" "${USER_ID}"
safe_ident "trace" "${TRACE_ID}"

cd "${ROOT}"

if [[ "${AUTO_START}" == "true" ]]; then
  if lsof -nP -iTCP:"${PORT}" -sTCP:LISTEN >/dev/null 2>&1; then
    cat >&2 <<EOF
web-agent-real-e2e failed: ${HOST}:${PORT} is already listening.
Use NIUMA_WEB_AGENT_PORT=<free-port> for an isolated run, or set
NIUMA_WEB_AGENT_E2E_AUTO_START=false to validate an already-running server.
EOF
    exit 2
  fi
  export NIUMA_WEB_AGENT_PORT="${PORT}"
  export NIUMA_WEB_AGENT_AUTH_TOKEN="${AUTH_TOKEN}"
  export NIUMA_WEB_AGENT_DB="${DB_NAME}"
  export NIUMA_WEB_AGENT_MODEL="${MODEL}"
  export NIUMA_WEB_AGENT_SKIP_BUILD="${NIUMA_WEB_AGENT_SKIP_BUILD:-false}"
  export NIUMA_MYSQL_DSN="${MYSQL_DSN}"
  export NIUMA_WEB_AGENT_PROFILE=e2e
  ./scripts/web-agent-start.sh e2e >"/tmp/web-agent-real-e2e-server.log" 2>&1 &
  SERVER_PID="$!"
fi

wait_for_server

workspace_json="$(api POST /agent/workspaces/validate "$(jq -n --arg cwd "${WORKSPACE}" '{cwd:$cwd}')")"
if [[ "$(jq -r '.is_git_repo' <<<"${workspace_json}")" != "true" ]]; then
  echo "web-agent-real-e2e failed: workspace validation did not detect git repo" >&2
  echo "${workspace_json}" >&2
  exit 1
fi

session_key="web-agent-${TRACE_ID}"
session_body="$(jq -n \
  --arg model "${MODEL}" \
  --arg cwd "${WORKSPACE}" \
  --arg trace_id "${TRACE_ID}" \
  --arg session_key "${session_key}" \
  --arg created "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  '{session_key:$session_key,title:"Real provider lifecycle E2E",status:"active",model:$model,cwd:$cwd,metadata_json:({source:"webui-agent",web_agent_session:true,trace_id:$trace_id,cwd:$cwd,workspace_name:"niuma",created_at:$created}|tojson)}')"
session_id="$(api POST /tenant/sessions "${session_body}" "${TRACE_ID}" | jq -r '.id')"
if [[ -z "${session_id}" || "${session_id}" == "null" ]]; then
  echo "web-agent-real-e2e failed: session id missing" >&2
  exit 1
fi

create_body="$(jq -n \
  --argjson parent_session_id "${session_id}" \
  --arg model "${MODEL}" \
  --arg provider "${PROVIDER}" \
  --arg cwd "${WORKSPACE}" \
  --arg trace_id "${TRACE_ID}" \
  --arg session_key "${session_key}" \
  --arg created "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  '{parent_session_id:$parent_session_id,agent_name:"web-agent",description:"Real provider lifecycle E2E",prompt:"",status:"ready",model:$model,trace_id:$trace_id,metadata_json:({source:"webui-agent",trace_id:$trace_id,run_trace_id:$trace_id,web_agent_session_id:$parent_session_id,web_agent_session_key:$session_key,run_index:1,cwd:$cwd,workspace_name:"niuma",permission_mode:"ask",effort:"medium",prompt_mode:"code",created_at:$created} + if $provider == "" then {} else {provider:$provider} end)}')"
task_id="$(api POST /tenant/agent-tasks "${create_body}" "${TRACE_ID}" | jq -r '.id')"
if [[ -z "${task_id}" || "${task_id}" == "null" ]]; then
  echo "web-agent-real-e2e failed: task id missing" >&2
  exit 1
fi

api POST "/tenant/agent-tasks/${task_id}/message" "$(jq -n --arg prompt "${PROMPT}" --arg trace_id "${TRACE_ID}" '{from_agent:"webui",content:$prompt,trace_id:$trace_id}')" "${TRACE_ID}" >/dev/null

deadline=$((SECONDS + TIMEOUT_SECONDS))
status=""
response=""
while (( SECONDS < deadline )); do
  task_json="$(api GET "/tenant/agent-tasks/${task_id}")"
  status="$(jq -r '.status // ""' <<<"${task_json}")"
  response="$(jq -r '.result_json | fromjson? | .response // ""' <<<"${task_json}")"
  case "${status}" in
    completed|failed|cancelled)
      break
      ;;
  esac
  sleep 1
done

if [[ "${status}" != "completed" ]]; then
  echo "web-agent-real-e2e failed: task ${task_id} ended with status=${status}" >&2
  api GET "/tenant/agent-tasks/${task_id}/events?limit=50" >&2 || true
  exit 1
fi
if [[ "${response}" != *"${EXPECTED}"* ]]; then
  echo "web-agent-real-e2e failed: response did not include expected text ${EXPECTED}" >&2
  echo "${response}" >&2
  exit 1
fi
if [[ "${response}" == *"${STUB_TEXT}"* ]]; then
  echo "web-agent-real-e2e failed: response matched known stub text" >&2
  echo "${response}" >&2
  exit 1
fi

events_json="$(api GET "/tenant/agent-tasks/${task_id}/events?limit=100")"
message_count="$(jq '[.data[] | select(.event_type=="message")] | length' <<<"${events_json}")"
delta_count="$(jq '[.data[] | select(.event_type=="text_delta")] | length' <<<"${events_json}")"
completed_count="$(jq '[.data[] | select(.event_type=="completed")] | length' <<<"${events_json}")"
if [[ "${message_count}" -lt 1 || "${delta_count}" -lt 1 || "${completed_count}" -lt 1 ]]; then
  echo "web-agent-real-e2e failed: missing lifecycle events" >&2
  echo "${events_json}" >&2
  exit 1
fi

conversation_json="$(api GET "/tenant/web-agent/conversations?limit=100")"
conversation_count="$(jq --argjson session_id "${session_id}" --argjson task_id "${task_id}" '[.data[] | select(.session_id==$session_id and (.tasks | map(.id) | index($task_id)))] | length' <<<"${conversation_json}")"
if [[ "${conversation_count}" -ne 1 ]]; then
  echo "web-agent-real-e2e failed: conversation API did not return task under session ${session_id}" >&2
  echo "${conversation_json}" >&2
  exit 1
fi
conversation_id="$(jq -r --argjson session_id "${session_id}" --argjson task_id "${task_id}" '.data[] | select(.session_id==$session_id and (.tasks | map(.id) | index($task_id))) | .id' <<<"${conversation_json}" | head -n 1)"
conversation_detail_json="$(api GET "/tenant/web-agent/conversations/$(node -e 'process.stdout.write(encodeURIComponent(process.argv[1]))' "${conversation_id}")?limit=100&event_limit=500")"
detail_task_count="$(jq --argjson task_id "${task_id}" '[.tasks[] | select(.id==$task_id)] | length' <<<"${conversation_detail_json}")"
detail_event_count="$(jq --argjson task_id "${task_id}" '[.events[] | select(.task_id==$task_id)] | length' <<<"${conversation_detail_json}")"
detail_total_runs="$(jq -r '.usage.total_runs // 0' <<<"${conversation_detail_json}")"
detail_context_percent="$(jq -r '.usage.context_percent // 0' <<<"${conversation_detail_json}")"
if [[ "${detail_task_count}" -ne 1 || "${detail_event_count}" -lt 3 || "${detail_total_runs}" -lt 1 ]]; then
  echo "web-agent-real-e2e failed: conversation detail did not include run/events/usage for ${conversation_id}" >&2
  echo "${conversation_detail_json}" >&2
  exit 1
fi

mysql_counts="$(mysql -u"${MYSQL_USER}" -h"${MYSQL_HOST}" -P"${MYSQL_PORT}" "${DB_NAME}" -N -e "SELECT COUNT(*) FROM tenant_sessions WHERE id=${session_id} AND session_key='${session_key}'; SELECT COUNT(*) FROM tenant_agent_tasks WHERE id=${task_id} AND parent_session_id=${session_id} AND status='completed'; SELECT COUNT(*) FROM tenant_agent_task_events WHERE task_id=${task_id};")"
session_rows="$(sed -n '1p' <<<"${mysql_counts}")"
task_rows="$(sed -n '2p' <<<"${mysql_counts}")"
event_rows="$(sed -n '3p' <<<"${mysql_counts}")"
if [[ ! "${session_rows}" =~ ^[0-9]+$ || ! "${task_rows}" =~ ^[0-9]+$ || ! "${event_rows}" =~ ^[0-9]+$ || "${session_rows}" -lt 1 || "${task_rows}" -lt 1 || "${event_rows}" -lt 3 ]]; then
  echo "web-agent-real-e2e failed: MySQL task/event rows missing" >&2
  echo "${mysql_counts}" >&2
  exit 1
fi

trace_counts="$(mysql -u"${MYSQL_USER}" -h"${MYSQL_HOST}" -P"${MYSQL_PORT}" "${DB_NAME}" -N -e "
SELECT COUNT(*) FROM tenant_agent_tasks
WHERE id=${task_id}
  AND trace_id='${TRACE_ID}'
  AND JSON_UNQUOTE(JSON_EXTRACT(metadata_json, '$.run_trace_id'))='${TRACE_ID}'
  AND JSON_UNQUOTE(JSON_EXTRACT(metadata_json, '$.prompt_mode'))='code';
SELECT COUNT(*) FROM tenant_agent_task_events
WHERE task_id=${task_id} AND COALESCE(trace_id, '') <> '${TRACE_ID}';
SELECT COUNT(*) FROM tenant_agent_task_events
WHERE task_id=${task_id} AND trace_id='${TRACE_ID}' AND event_type IN ('message','text_delta','completed');
SELECT COUNT(*) FROM tenant_telemetry_events
WHERE trace_id='${TRACE_ID}'
  AND resource_type='agent_task'
  AND resource_id='${task_id}'
  AND event_name='agent.run.started';
SELECT COUNT(*) FROM tenant_telemetry_events
WHERE trace_id='${TRACE_ID}'
  AND resource_type='agent_task'
  AND resource_id='${task_id}'
  AND event_name='agent.run.finished';
SELECT COUNT(*) FROM tenant_telemetry_events
WHERE trace_id='${TRACE_ID}'
  AND event_name IN ('api.request.started','api.request.finished');
")"
task_trace_rows="$(sed -n '1p' <<<"${trace_counts}")"
mismatched_event_rows="$(sed -n '2p' <<<"${trace_counts}")"
traced_lifecycle_rows="$(sed -n '3p' <<<"${trace_counts}")"
run_started_rows="$(sed -n '4p' <<<"${trace_counts}")"
run_finished_rows="$(sed -n '5p' <<<"${trace_counts}")"
api_trace_rows="$(sed -n '6p' <<<"${trace_counts}")"
if [[ "${task_trace_rows}" -ne 1 || "${mismatched_event_rows}" -ne 0 || "${traced_lifecycle_rows}" -lt 3 || "${run_started_rows}" -lt 1 || "${run_finished_rows}" -lt 1 || "${api_trace_rows}" -lt 2 ]]; then
  echo "web-agent-real-e2e failed: observability trace chain mismatch for ${TRACE_ID}" >&2
  echo "${trace_counts}" >&2
  exit 1
fi

telemetry_json="$(api GET "/tenant/telemetry?limit=500&search=${TRACE_ID}")"
telemetry_started_count="$(jq --arg trace_id "${TRACE_ID}" '[.data[] | select(.trace_id==$trace_id and .name=="agent.run.started")] | length' <<<"${telemetry_json}")"
telemetry_finished_count="$(jq --arg trace_id "${TRACE_ID}" '[.data[] | select(.trace_id==$trace_id and .name=="agent.run.finished")] | length' <<<"${telemetry_json}")"
if [[ "${telemetry_started_count}" -lt 1 || "${telemetry_finished_count}" -lt 1 ]]; then
  echo "web-agent-real-e2e failed: telemetry API search did not find agent run events" >&2
  echo "${telemetry_json}" >&2
  exit 1
fi

browser_json="$(BASE_URL="${BASE_URL}" AUTH_TOKEN="${AUTH_TOKEN}" EXPECTED="${EXPECTED}" STUB_TEXT="${STUB_TEXT}" node <<'NODE'
const { chromium } = require("./web/node_modules/playwright");
(async () => {
  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage({ viewport: { width: 1440, height: 950 } });
  const issues = [];
  page.on("console", msg => {
    if (["error", "warning"].includes(msg.type())) issues.push(`${msg.type()}: ${msg.text()}`);
  });
  page.on("pageerror", err => issues.push(`pageerror: ${err.message}`));
  const url = `${process.env.BASE_URL}/webui/agent?token=${process.env.AUTH_TOKEN}`;
  const resp = await page.goto(url, { waitUntil: "networkidle", timeout: 30000 });
  await page.waitForTimeout(1500);
  const body = await page.locator("body").innerText({ timeout: 10000 });
  const screenshot = `/tmp/web-agent-real-e2e-${Date.now()}.png`;
  await page.screenshot({ path: screenshot, fullPage: true });
  await browser.close();
  console.log(JSON.stringify({
    status: resp && resp.status(),
    hasExpected: body.includes(process.env.EXPECTED),
    hasStub: body.includes(process.env.STUB_TEXT),
    consoleIssues: issues,
    screenshot
  }));
})().catch(err => {
  console.error(err);
  process.exit(1);
});
NODE
)"

if [[ "$(jq -r '.status' <<<"${browser_json}")" != "200" || "$(jq -r '.hasExpected' <<<"${browser_json}")" != "true" || "$(jq -r '.hasStub' <<<"${browser_json}")" != "false" ]]; then
  echo "web-agent-real-e2e failed: browser validation failed" >&2
  echo "${browser_json}" >&2
  exit 1
fi
if [[ "$(jq '.consoleIssues | length' <<<"${browser_json}")" -ne 0 ]]; then
  echo "web-agent-real-e2e failed: browser console issues" >&2
  echo "${browser_json}" >&2
  exit 1
fi

echo "web-agent-real-e2e ok"
echo "url: ${BASE_URL}/webui/agent?token=${AUTH_TOKEN}"
echo "session: ${session_id}"
echo "conversation: ${conversation_id}"
echo "task: ${task_id}"
echo "trace: ${TRACE_ID}"
echo "model: ${MODEL}"
echo "provider: ${PROVIDER:-default}"
echo "response: ${response}"
echo "events: message=${message_count} text_delta=${delta_count} completed=${completed_count}"
echo "conversation detail: events=${detail_event_count} runs=${detail_total_runs} context=${detail_context_percent}%"
echo "telemetry: agent.run.started=${run_started_rows} agent.run.finished=${run_finished_rows} api.request=${api_trace_rows}"
echo "screenshot: $(jq -r '.screenshot' <<<"${browser_json}")"
