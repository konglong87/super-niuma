#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "${ROOT}/scripts/lib/product-env.sh"
ACTION="${1:-start}"

normalize_channel_env_aliases() {
  NIUMA_CHANNEL_WORKER_NAME="${NIUMA_CHANNEL_WORKER_NAME:-}}"
  NIUMA_MYSQL_DSN="${NIUMA_MYSQL_DSN:-}}"
  NIUMA_FEISHU_CREDENTIAL_FILE="${NIUMA_FEISHU_CREDENTIAL_FILE:-}}"
  NIUMA_CHANNEL_TENANT_ID="${NIUMA_CHANNEL_TENANT_ID:-}}"
  NIUMA_CHANNEL_ACCOUNT_ID="${NIUMA_CHANNEL_ACCOUNT_ID:-}}"
  NIUMA_CHANNEL_ACCOUNT_KEY="${NIUMA_CHANNEL_ACCOUNT_KEY:-}}"
  NIUMA_CHANNEL_USER_ID="${NIUMA_CHANNEL_USER_ID:-}}"
  NIUMA_CHANNEL_PAYLOAD_KEY="${NIUMA_CHANNEL_PAYLOAD_KEY:-}}"
  NIUMA_CHANNEL_PAYLOAD_KEY_VERSION="${NIUMA_CHANNEL_PAYLOAD_KEY_VERSION:-}}"
  NIUMA_CHANNEL_WORKSPACE_ROOTS="${NIUMA_CHANNEL_WORKSPACE_ROOTS:-}}"
  NIUMA_CHANNEL_PERMISSION_MODE="${NIUMA_CHANNEL_PERMISSION_MODE:-}}"
  NIUMA_CHANNEL_SETTINGS="${NIUMA_CHANNEL_SETTINGS:-}}"
  NIUMA_CHANNEL_SETTINGS_FILE="${NIUMA_CHANNEL_SETTINGS_FILE:-}}"
  NIUMA_CONFIG_DIR="${NIUMA_CONFIG_DIR:-}}"
  NIUMA_CHANNEL_MODEL_PROVIDER="${NIUMA_CHANNEL_MODEL_PROVIDER:-}}"
  NIUMA_CHANNEL_MODEL="${NIUMA_CHANNEL_MODEL:-}}"
  NIUMA_CHANNEL_STREAMING="${NIUMA_CHANNEL_STREAMING:-}}"
  NIUMA_CHANNEL_STREAMING_ACCOUNT_MODE="${NIUMA_CHANNEL_STREAMING_ACCOUNT_MODE:-}}"
  NIUMA_CHANNEL_REACTIONS="${NIUMA_CHANNEL_REACTIONS:-}}"
  NIUMA_CHANNEL_QUESTIONS="${NIUMA_CHANNEL_QUESTIONS:-}}"
  NIUMA_CHANNEL_REDIS_ADDR="${NIUMA_CHANNEL_REDIS_ADDR:-}}"
  NIUMA_CHANNEL_REDIS_PASSWORD="${NIUMA_CHANNEL_REDIS_PASSWORD:-}}"
  NIUMA_CHANNEL_REDIS_PREFIX="${NIUMA_CHANNEL_REDIS_PREFIX:-}}"
  NIUMA_CHANNEL_ADMIN_OPEN_IDS="${NIUMA_CHANNEL_ADMIN_OPEN_IDS:-}}"
  NIUMA_CHANNEL_TOOL_DETAILS="${NIUMA_CHANNEL_TOOL_DETAILS:-}}"
  export NIUMA_CHANNEL_WORKER_NAME NIUMA_MYSQL_DSN NIUMA_SQLITE_PATH
  export NIUMA_FEISHU_CREDENTIAL_FILE NIUMA_CHANNEL_TENANT_ID NIUMA_CHANNEL_ACCOUNT_ID
  export NIUMA_CHANNEL_ACCOUNT_KEY NIUMA_CHANNEL_USER_ID NIUMA_CHANNEL_PAYLOAD_KEY
  export NIUMA_CHANNEL_PAYLOAD_KEY_VERSION NIUMA_CHANNEL_WORKSPACE_ROOTS
  export NIUMA_CHANNEL_PERMISSION_MODE NIUMA_CHANNEL_SETTINGS NIUMA_CHANNEL_SETTINGS_FILE
  export NIUMA_CONFIG_DIR NIUMA_CHANNEL_MODEL_PROVIDER NIUMA_CHANNEL_MODEL
  export NIUMA_CHANNEL_STREAMING NIUMA_CHANNEL_STREAMING_ACCOUNT_MODE NIUMA_CHANNEL_REACTIONS
  export NIUMA_CHANNEL_QUESTIONS NIUMA_CHANNEL_REDIS_ADDR NIUMA_CHANNEL_REDIS_PASSWORD
  export NIUMA_CHANNEL_REDIS_PREFIX NIUMA_CHANNEL_ADMIN_OPEN_IDS NIUMA_CHANNEL_TOOL_DETAILS
}

normalize_channel_env_aliases
WORKER_NAME="${NIUMA_CHANNEL_WORKER_NAME:-feishu-e2e}"
DEFAULT_STATE_DIR="${HOME}/.niuma/channel-workers"
STATE_DIR="${NIUMA_CHANNEL_WORKER_STATE_DIR:-${DEFAULT_STATE_DIR}}"
LEGACY_STATE_DIR="${NIUMA_CHANNEL_WORKER_LEGACY_STATE_DIR:-/tmp/niuma-channel-workers}"
BINARY="${NIUMA_CHANNEL_WORKER_BINARY:-${STATE_DIR}/niuma-channel-worker-${WORKER_NAME}}"
SCREEN_NAME="${NIUMA_CHANNEL_WORKER_SCREEN:-niuma-channel-${WORKER_NAME}}"
LOG_FILE="${NIUMA_CHANNEL_WORKER_LOG:-${STATE_DIR}/${WORKER_NAME}.log}"
ENV_FILE="${STATE_DIR}/${WORKER_NAME}.env"
WORKSPACE="${NIUMA_CHANNEL_WORKSPACE:-${ROOT}}"
SETTINGS_FILE="${NIUMA_CHANNEL_SETTINGS_FILE:-${NIUMA_CHANNEL_SETTINGS:-${HOME}/.niuma/settings.json}}"
PROVIDER="${NIUMA_CHANNEL_MODEL_PROVIDER:-${NIUMA_PROVIDER:-}}"
MODEL="${NIUMA_CHANNEL_MODEL:-${CLAUDE_CODE_MODEL:-}}"
RUNNER_FILE="${STATE_DIR}/${WORKER_NAME}.run.sh"

migrate_legacy_worker_config() {
  local legacy_env migration_file
  [[ "${STATE_DIR}" != "${LEGACY_STATE_DIR}" ]] || return 0
  [[ ! -f "${ENV_FILE}" ]] || return 0
  legacy_env="${LEGACY_STATE_DIR}/${WORKER_NAME}.env"
  [[ -f "${legacy_env}" ]] || return 0

  mkdir -p "${STATE_DIR}"
  chmod 700 "${STATE_DIR}"
  migration_file="${ENV_FILE}.migrate.$$"
  cp "${legacy_env}" "${migration_file}"
  chmod 600 "${migration_file}"
  mv "${migration_file}" "${ENV_FILE}"
  rm -f "${legacy_env}"
  echo "migrated worker config: ${ENV_FILE}"
}

migrate_legacy_worker_config

if [[ -f "${ENV_FILE}" ]]; then
  persisted_override_keys=(
    WORKSPACE
    NIUMA_MYSQL_DSN
    NIUMA_SQLITE_PATH
    NIUMA_FEISHU_CREDENTIAL_FILE
    NIUMA_CHANNEL_TENANT_ID
    NIUMA_CHANNEL_ACCOUNT_ID
    NIUMA_CHANNEL_ACCOUNT_KEY
    NIUMA_CHANNEL_USER_ID
    NIUMA_CHANNEL_PAYLOAD_KEY
    NIUMA_CHANNEL_PAYLOAD_KEY_VERSION
    NIUMA_CHANNEL_WORKER_NAME
    NIUMA_CHANNEL_WORKSPACE_ROOTS
    NIUMA_CHANNEL_PERMISSION_MODE
    NIUMA_CHANNEL_SETTINGS
    NIUMA_CHANNEL_SETTINGS_FILE
    NIUMA_CONFIG_DIR
    NIUMA_CHANNEL_MODEL_PROVIDER
    NIUMA_CHANNEL_MODEL
    NIUMA_CHANNEL_STREAMING
    NIUMA_CHANNEL_STREAMING_ACCOUNT_MODE
    NIUMA_CHANNEL_REACTIONS
    NIUMA_CHANNEL_REDIS_ADDR
    NIUMA_CHANNEL_REDIS_PASSWORD
    NIUMA_CHANNEL_REDIS_PREFIX
    NIUMA_CHANNEL_ADMIN_OPEN_IDS
    NIUMA_CHANNEL_TOOL_DETAILS
    NIUMA_CHANNEL_QUESTIONS
  )
  persisted_override_declarations=()
  for key in "${persisted_override_keys[@]}"; do
    if [[ -n "${!key:-}" ]] && declaration="$(declare -p "${key}" 2>/dev/null)"; then
      persisted_override_declarations+=("${declaration}")
    fi
  done
  set -a
  . "${ENV_FILE}"
  set +a
  normalize_channel_env_aliases
  for declaration in "${persisted_override_declarations[@]}"; do
    eval "${declaration}"
  done
fi
SETTINGS_FILE="${NIUMA_CHANNEL_SETTINGS_FILE:-${NIUMA_CHANNEL_SETTINGS:-${HOME}/.niuma/settings.json}}"
PROVIDER="${NIUMA_CHANNEL_MODEL_PROVIDER:-${NIUMA_PROVIDER:-}}"
MODEL="${NIUMA_CHANNEL_MODEL:-${CLAUDE_CODE_MODEL:-}}"
ACCOUNT_LOCK_ROOT="${NIUMA_CHANNEL_ACCOUNT_LOCK_ROOT:-${STATE_DIR}/accounts}"
ACCOUNT_LOCK_DIR=""
LOCK_PERSIST=0
if [[ -n "${NIUMA_CHANNEL_TENANT_ID:-}" && -n "${NIUMA_CHANNEL_ACCOUNT_ID:-}" ]]; then
  ACCOUNT_LOCK_DIR="${ACCOUNT_LOCK_ROOT}/${NIUMA_CHANNEL_TENANT_ID}-${NIUMA_CHANNEL_ACCOUNT_ID}"
fi

need() {
  command -v "$1" >/dev/null 2>&1 || { echo "channel-worker-screen requires $1" >&2; exit 2; }
}

require_env() {
  local key="$1"
  if [[ -z "${!key:-}" ]]; then echo "channel-worker-screen requires ${key}" >&2; exit 2; fi
}

screen_exists() {
  local sessions
  sessions="$(screen -ls 2>/dev/null || true)"
  [[ "${sessions}" == *".${SCREEN_NAME}"* ]]
}

worker_process_pids() {
  local processes
  processes="$(ps eww -axo pid=,command= 2>/dev/null || true)"
  awk -v binary="${BINARY}" '$0 ~ /channels run/ && index($0, binary) {print $1}' <<<"${processes}"
}

stop_worker() {
  if screen_exists; then
    screen -S "${SCREEN_NAME}" -X quit >/dev/null 2>&1 || true
    for _ in $(seq 1 50); do screen_exists || break; sleep 0.1; done
  fi
  rm -f "${RUNNER_FILE}"
  release_account_lock
}

account_process_pids() {
  local processes
  [[ -n "${NIUMA_CHANNEL_ACCOUNT_ID:-}" ]] || return 0
  processes="$(ps eww -axo pid=,command= 2>/dev/null || true)"
  awk \
    -v go_marker="NIUMA_CHANNEL_ACCOUNT_ID=${NIUMA_CHANNEL_ACCOUNT_ID}" \
    -v legacy_marker="NIUMA_CHANNEL_ACCOUNT_ID=${NIUMA_CHANNEL_ACCOUNT_ID}" \
    '$0 ~ /channels run/ && ($0 ~ go_marker || $0 ~ legacy_marker) {print $1}' <<<"${processes}"
}

kill_account_processes() {
  local pids
  pids="$(account_process_pids || true)"
  [[ -z "${pids}" ]] && return 0
  while read -r pid; do
    [[ -z "${pid}" ]] || kill "${pid}" >/dev/null 2>&1 || true
  done <<<"${pids}"
  for _ in $(seq 1 50); do
    [[ -z "$(account_process_pids || true)" ]] && break
    sleep 0.1
  done
}

release_account_lock() {
  [[ -n "${ACCOUNT_LOCK_DIR}" ]] || return 0
  rm -f "${ACCOUNT_LOCK_DIR}/owner"
  rmdir "${ACCOUNT_LOCK_DIR}" >/dev/null 2>&1 || true
  LOCK_PERSIST=0
}

account_lock_is_active() {
  [[ -d "${ACCOUNT_LOCK_DIR}" ]] || return 1
  local owner_screen owner_pid owner_command
  owner_screen="$(sed -n '1p' "${ACCOUNT_LOCK_DIR}/owner" 2>/dev/null || true)"
  owner_pid="$(sed -n '3p' "${ACCOUNT_LOCK_DIR}/owner" 2>/dev/null || true)"
  if [[ -n "${owner_screen}" && "$(screen -ls 2>/dev/null || true)" == *".${owner_screen}"* ]]; then
    return 0
  fi
  if [[ "${owner_pid}" =~ ^[0-9]+$ ]] && kill -0 "${owner_pid}" >/dev/null 2>&1; then
    owner_command="$(ps -p "${owner_pid}" -o command= 2>/dev/null || true)"
    if [[ "${owner_command}" == *"channel-worker-screen.sh"* ]]; then
      return 0
    fi
  fi
  [[ -n "$(account_process_pids || true)" ]]
}

acquire_account_lock() {
  [[ -n "${ACCOUNT_LOCK_DIR}" ]] || return 0
  mkdir -p "${ACCOUNT_LOCK_ROOT}"
  if mkdir "${ACCOUNT_LOCK_DIR}" 2>/dev/null; then
    printf '%s\n%s\n%s\n' "${SCREEN_NAME}" "${WORKER_NAME}" "$$" >"${ACCOUNT_LOCK_DIR}/owner"
    return 0
  fi
  if account_lock_is_active; then
    echo "worker for account ${NIUMA_CHANNEL_TENANT_ID}/${NIUMA_CHANNEL_ACCOUNT_ID} already exists" >&2
    exit 1
  fi
  rm -f "${ACCOUNT_LOCK_DIR}/owner"
  rmdir "${ACCOUNT_LOCK_DIR}" >/dev/null 2>&1 || true
  mkdir "${ACCOUNT_LOCK_DIR}"
  printf '%s\n%s\n%s\n' "${SCREEN_NAME}" "${WORKER_NAME}" "$$" >"${ACCOUNT_LOCK_DIR}/owner"
}

status_worker() {
  if screen_exists; then
    local pids
    pids="$(worker_process_pids || true)"
    if [[ -z "${pids}" ]]; then
      echo "degraded: ${SCREEN_NAME} exists but worker process is not running" >&2
      return 1
    fi
    echo "running: ${SCREEN_NAME}"
    echo "binary: ${BINARY}"
    echo "log: ${LOG_FILE}"
    echo "pids: ${pids//$'\n'/ }"
    return 0
  fi
  echo "stopped: ${SCREEN_NAME}"
  return 1
}

start_worker() {
  need go
  need screen
  if [[ -z "${NIUMA_SQLITE_PATH:-}" && -z "${NIUMA_MYSQL_DSN:-}" ]]; then
    echo "channel-worker-screen requires NIUMA_SQLITE_PATH or NIUMA_MYSQL_DSN" >&2
    exit 2
  fi
  require_env NIUMA_FEISHU_CREDENTIAL_FILE
  require_env NIUMA_CHANNEL_TENANT_ID
  require_env NIUMA_CHANNEL_ACCOUNT_ID
  require_env NIUMA_CHANNEL_ACCOUNT_KEY
  require_env NIUMA_CHANNEL_USER_ID
  mkdir -p "${STATE_DIR}"
  chmod 700 "${STATE_DIR}"
  ACCOUNT_LOCK_DIR="${ACCOUNT_LOCK_ROOT}/${NIUMA_CHANNEL_TENANT_ID}-${NIUMA_CHANNEL_ACCOUNT_ID}"
  if screen_exists || [[ -n "$(account_process_pids || true)" ]]; then
    echo "worker for account ${NIUMA_CHANNEL_ACCOUNT_ID} already exists; use restart" >&2
    exit 1
  fi
  acquire_account_lock
  trap 'if [[ "${LOCK_PERSIST}" -eq 0 ]]; then release_account_lock; fi' EXIT
  go build -o "${BINARY}.tmp" ./cmd/niuma
  mv "${BINARY}.tmp" "${BINARY}"
  chmod 700 "${BINARY}"
  umask 077
  {
    printf 'NIUMA_MYSQL_DSN=%q\n' "${NIUMA_MYSQL_DSN:-}"
    printf 'NIUMA_SQLITE_PATH=%q\n' "${NIUMA_SQLITE_PATH:-}"
    printf 'NIUMA_FEISHU_CREDENTIAL_FILE=%q\n' "${NIUMA_FEISHU_CREDENTIAL_FILE}"
    printf 'NIUMA_CHANNEL_TENANT_ID=%q\n' "${NIUMA_CHANNEL_TENANT_ID}"
    printf 'NIUMA_CHANNEL_ACCOUNT_ID=%q\n' "${NIUMA_CHANNEL_ACCOUNT_ID}"
    printf 'NIUMA_CHANNEL_ACCOUNT_KEY=%q\n' "${NIUMA_CHANNEL_ACCOUNT_KEY}"
    printf 'NIUMA_CHANNEL_USER_ID=%q\n' "${NIUMA_CHANNEL_USER_ID}"
    printf 'NIUMA_CHANNEL_PAYLOAD_KEY=%q\n' "${NIUMA_CHANNEL_PAYLOAD_KEY:-}"
    printf 'NIUMA_CHANNEL_PAYLOAD_KEY_VERSION=%q\n' "${NIUMA_CHANNEL_PAYLOAD_KEY_VERSION:-}"
    printf 'NIUMA_CHANNEL_WORKER_NAME=%q\n' "${WORKER_NAME}"
    printf 'WORKSPACE=%q\n' "${WORKSPACE}"
    printf 'NIUMA_CHANNEL_WORKSPACE_ROOTS=%q\n' "${NIUMA_CHANNEL_WORKSPACE_ROOTS:-${WORKSPACE}}"
    printf 'NIUMA_CHANNEL_PERMISSION_MODE=%q\n' "${NIUMA_CHANNEL_PERMISSION_MODE:-ask}"
    printf 'NIUMA_CHANNEL_SETTINGS=%q\n' "${SETTINGS_FILE}"
    printf 'NIUMA_CHANNEL_SETTINGS_FILE=%q\n' "${SETTINGS_FILE}"
    # Keep config-dir overrides in the screen environment so imageGeneration
    # resolution and other global settings use the same isolated profile.
    printf 'NIUMA_CONFIG_DIR=%q\n' "${NIUMA_CONFIG_DIR:-}"
    printf 'NIUMA_CHANNEL_MODEL_PROVIDER=%q\n' "${PROVIDER}"
    printf 'NIUMA_CHANNEL_MODEL=%q\n' "${MODEL}"
    printf 'NIUMA_CHANNEL_STREAMING=%q\n' "${NIUMA_CHANNEL_STREAMING:-}"
    printf 'NIUMA_CHANNEL_STREAMING_ACCOUNT_MODE=%q\n' "${NIUMA_CHANNEL_STREAMING_ACCOUNT_MODE:-}"
    printf 'NIUMA_CHANNEL_REACTIONS=%q\n' "${NIUMA_CHANNEL_REACTIONS:-}"
    printf 'NIUMA_CHANNEL_REDIS_ADDR=%q\n' "${NIUMA_CHANNEL_REDIS_ADDR:-}"
    printf 'NIUMA_CHANNEL_REDIS_PASSWORD=%q\n' "${NIUMA_CHANNEL_REDIS_PASSWORD:-}"
    printf 'NIUMA_CHANNEL_REDIS_PREFIX=%q\n' "${NIUMA_CHANNEL_REDIS_PREFIX:-}"
    printf 'NIUMA_CHANNEL_ADMIN_OPEN_IDS=%q\n' "${NIUMA_CHANNEL_ADMIN_OPEN_IDS:-}"
    printf 'NIUMA_CHANNEL_TOOL_DETAILS=%q\n' "${NIUMA_CHANNEL_TOOL_DETAILS:-}"
    printf 'NIUMA_CHANNEL_QUESTIONS=%q\n' "${NIUMA_CHANNEL_QUESTIONS:-}"
  } >"${ENV_FILE}"
  chmod 600 "${ENV_FILE}"
  : >"${LOG_FILE}"
  cat >"${RUNNER_FILE}" <<EOF
#!/usr/bin/env bash
set -euo pipefail
inherited_keys="\$(compgen -v NIUMA_ || true)"
while IFS= read -r inherited_key; do
  case "\${inherited_key}" in
    *_SQLITE_PATH) unset "\${inherited_key}" ;;
  esac
done <<<"\${inherited_keys}"
set -a
. '${ENV_FILE}'
set +a
args=(--cwd "\${WORKSPACE}")
if [[ -n "\${NIUMA_CHANNEL_SETTINGS:-}" && -f "\${NIUMA_CHANNEL_SETTINGS}" ]]; then args+=(--settings "\${NIUMA_CHANNEL_SETTINGS}"); fi
if [[ -n "\${NIUMA_CHANNEL_MODEL_PROVIDER:-}" ]]; then args+=(--provider "\${NIUMA_CHANNEL_MODEL_PROVIDER}"); fi
if [[ -n "\${NIUMA_CHANNEL_MODEL:-}" ]]; then args+=(--model "\${NIUMA_CHANNEL_MODEL}"); fi
exec '${BINARY}' "\${args[@]}" channels run >>'${LOG_FILE}' 2>&1
EOF
  chmod 700 "${RUNNER_FILE}"
  screen -dmS "${SCREEN_NAME}" "${RUNNER_FILE}"
  for _ in $(seq 1 50); do
    if screen_exists && [[ -n "$(worker_process_pids || true)" ]]; then
      break
    fi
    sleep 0.1
  done
  status_worker
  LOCK_PERSIST=1
  trap - EXIT
}

case "${ACTION}" in
  start) start_worker ;;
  stop) need screen; stop_worker; kill_account_processes; echo "stopped: ${SCREEN_NAME}" ;;
  restart) need screen; stop_worker; kill_account_processes; start_worker ;;
  status) need screen; status_worker ;;
  *) echo "usage: $0 {start|stop|restart|status}" >&2; exit 2 ;;
esac
