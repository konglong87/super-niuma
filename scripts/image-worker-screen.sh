#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "${ROOT}/scripts/lib/product-env.sh"
ACTION="${1:-start}"
if [[ "${NIUMA_IMAGE_WORKER_NAME+x}" == x ]]; then
  WORKER_NAME="${NIUMA_IMAGE_WORKER_NAME}"
else
  WORKER_NAME=""
fi
if [[ -z "${WORKER_NAME}" || ! "${WORKER_NAME}" =~ ^[A-Za-z0-9_.@-]{1,64}$ ]]; then
  echo "image-worker-screen requires NIUMA_IMAGE_WORKER_NAME (1-64 characters using only A-Za-z0-9_.@-)" >&2
  exit 2
fi
STATE_DIR="${NIUMA_IMAGE_WORKER_STATE_DIR:-${HOME}/.niuma/image-workers}"
BINARY="${NIUMA_IMAGE_WORKER_BINARY:-${STATE_DIR}/niuma-image-worker-${WORKER_NAME}}"
SCREEN_NAME="${NIUMA_IMAGE_WORKER_SCREEN:-niuma-image-${WORKER_NAME}}"
LOG_FILE="${NIUMA_IMAGE_WORKER_LOG:-${STATE_DIR}/${WORKER_NAME}.log}"
ENV_FILE="${STATE_DIR}/${WORKER_NAME}.env"
RUNNER_FILE="${STATE_DIR}/${WORKER_NAME}.run.sh"
READY_FILE="${NIUMA_IMAGE_WORKER_READY_FILE:-${STATE_DIR}/${WORKER_NAME}.ready.json}"
WORKSPACE="${NIUMA_IMAGE_WORKER_WORKSPACE:-${ROOT}}"
SETTINGS_FILE="${NIUMA_IMAGE_WORKER_SETTINGS_FILE:-${HOME}/.niuma/settings.json}"

need() {
  command -v "$1" >/dev/null 2>&1 || { echo "image-worker-screen requires $1" >&2; exit 2; }
}

require_env() {
  local key="$1"
  if [[ -z "${!key:-}" ]]; then
    echo "image-worker-screen requires ${key}" >&2
    exit 2
  fi
}

screen_exists() {
  local sessions
  sessions="$(screen -ls 2>/dev/null || true)"
  [[ "${sessions}" == *".${SCREEN_NAME}"* ]]
}

worker_process_pids() {
  local processes
  processes="$(ps -axo pid=,command= 2>/dev/null || true)"
  awk -v binary="${BINARY}" '$0 ~ /image-worker run/ && index($0, binary) {print $1}' <<<"${processes}"
}

readiness_valid() {
  [[ -s "${READY_FILE}" ]] || return 1
  local state
  state="$(<"${READY_FILE}")"
  [[ "${state}" == *'"ready":true'* && "${state}" == *'"tenant_id":'* ]]
}

stop_worker() {
  if screen_exists; then
    screen -S "${SCREEN_NAME}" -X quit >/dev/null 2>&1 || true
    for _ in $(seq 1 50); do
      screen_exists || break
      sleep 0.1
    done
  fi
  rm -f "${READY_FILE}" "${ENV_FILE}" "${RUNNER_FILE}"
}

status_worker() {
  if ! screen_exists; then
    echo "stopped: ${SCREEN_NAME}"
    return 1
  fi
  local pids
  pids="$(worker_process_pids || true)"
  if [[ -z "${pids}" ]]; then
    echo "degraded: ${SCREEN_NAME} exists but worker process is not running" >&2
    return 1
  fi
  if ! readiness_valid; then
    echo "starting: ${SCREEN_NAME} process is running but not ready" >&2
    return 1
  fi
  echo "ready: ${SCREEN_NAME}"
  echo "binary: ${BINARY}"
  echo "log: ${LOG_FILE}"
  echo "readiness: ${READY_FILE}"
  echo "pids: ${pids//$'\n'/ }"
}

write_env_value() {
  local key="$1"
  printf '%s=%q\n' "${key}" "${!key:-}"
}

preflight_worker() {
  require_env NIUMA_MYSQL_DSN
  require_env NIUMA_IMAGE_WORKER_TENANT_ID
  need go
  need screen
  if [[ ! -f "${SETTINGS_FILE}" ]]; then
    echo "image-worker-screen requires a stable settings file: ${SETTINGS_FILE}" >&2
    exit 2
  fi
  mkdir -p "${STATE_DIR}"
  chmod 700 "${STATE_DIR}"
}

build_worker() {
  (
    cd "${ROOT}"
    go build -o "${BINARY}.tmp" ./cmd/niuma
  )
  mv "${BINARY}.tmp" "${BINARY}"
  chmod 700 "${BINARY}"
}

write_worker_runtime_files() {
  rm -f "${READY_FILE}"
  umask 077
  {
    for key in \
      NIUMA_MYSQL_DSN \
      NIUMA_IMAGE_WORKER_TENANT_ID \
      ANTHROPIC_API_KEY \
      ANTHROPIC_AUTH_TOKEN \
      CLAUDE_CODE_AUTH_TOKEN \
      CLAUDE_CODE_OAUTH_TOKEN \
      NIUMA_CONFIG_DIR \
      NIUMA_LOG_LEVEL; do
      write_env_value "${key}"
    done
    printf 'NIUMA_IMAGE_WORKER_NAME=%q\n' "${WORKER_NAME}"
    printf 'NIUMA_IMAGE_WORKER_READY_FILE=%q\n' "${READY_FILE}"
    printf 'WORKSPACE=%q\n' "${WORKSPACE}"
    printf 'NIUMA_IMAGE_WORKER_SETTINGS_FILE=%q\n' "${SETTINGS_FILE}"
  } >"${ENV_FILE}"
  chmod 600 "${ENV_FILE}"
  : >"${LOG_FILE}"
  chmod 600 "${LOG_FILE}"
  cat >"${RUNNER_FILE}" <<EOF
#!/usr/bin/env bash
set -euo pipefail
set -a
. '${ENV_FILE}'
set +a
exec '${BINARY}' --cwd "\${WORKSPACE}" --settings "\${NIUMA_IMAGE_WORKER_SETTINGS_FILE}" image-worker run >>'${LOG_FILE}' 2>&1
EOF
  chmod 700 "${RUNNER_FILE}"
}

launch_worker() {
  write_worker_runtime_files
  screen -dmS "${SCREEN_NAME}" "${RUNNER_FILE}"
  for _ in $(seq 1 100); do
    if screen_exists && [[ -n "$(worker_process_pids || true)" ]] && readiness_valid; then
      status_worker
      return 0
    fi
    if ! screen_exists; then
      echo "image worker exited before readiness; inspect ${LOG_FILE}" >&2
      stop_worker
      return 1
    fi
    sleep 0.1
  done
  echo "image worker readiness timed out; inspect ${LOG_FILE}" >&2
  stop_worker
  return 1
}

start_worker() {
  preflight_worker
  if screen_exists || [[ -n "$(worker_process_pids || true)" ]]; then
    echo "image worker ${WORKER_NAME} already exists; use restart" >&2
    exit 1
  fi
  build_worker
  launch_worker
}

case "${ACTION}" in
  start)
    start_worker
    ;;
  status)
    need screen
    status_worker
    ;;
  restart)
    preflight_worker
    build_worker
    stop_worker
    launch_worker
    ;;
  stop)
    need screen
    stop_worker
    echo "stopped: ${SCREEN_NAME}"
    ;;
  *)
    echo "usage: $0 {start|status|restart|stop}" >&2
    exit 2
    ;;
esac
