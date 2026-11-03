#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="${ROOT}/scripts/channel-worker-screen.sh"
TEST_ROOT="$(mktemp -d)"
TEST_HOME="${TEST_ROOT}/home"
FAKE_BIN="${TEST_ROOT}/bin"
FAKE_SCREEN_STATE="${TEST_ROOT}/screen"
WORKER_NAME="persist-test-$$"
LEGACY_WORKER_NAME="legacy-test-$$"
STALE_LOCK_WORKER_NAME="stale-lock-test-$$"
LEGACY_PREFIX_WORKER_NAME="legacy-prefix-test-$$"
PERSISTENT_STATE_DIR="${TEST_HOME}/.niuma/channel-workers"
WORKER_BINARY="${PERSISTENT_STATE_DIR}/niuma-channel-worker-${WORKER_NAME}"
LEGACY_WORKER_BINARY="${PERSISTENT_STATE_DIR}/niuma-channel-worker-${LEGACY_WORKER_NAME}"
STALE_LOCK_WORKER_BINARY="${PERSISTENT_STATE_DIR}/niuma-channel-worker-${STALE_LOCK_WORKER_NAME}"
LEGACY_PREFIX_WORKER_BINARY="${PERSISTENT_STATE_DIR}/niuma-channel-worker-${LEGACY_PREFIX_WORKER_NAME}"
LEGACY_STATE_DIR="/tmp/niuma-channel-workers"

cleanup() {
  rm -rf "${TEST_ROOT}"
  rm -f "${LEGACY_STATE_DIR}/${WORKER_NAME}.env"
  rm -f "${LEGACY_STATE_DIR}/${WORKER_NAME}.run.sh"
  rm -f "${LEGACY_STATE_DIR}/${WORKER_NAME}.log"
  rm -f "${LEGACY_STATE_DIR}/niuma-channel-worker-${WORKER_NAME}"
  rm -f "${LEGACY_STATE_DIR}/${LEGACY_WORKER_NAME}.env"
  rm -f "${LEGACY_STATE_DIR}/${LEGACY_WORKER_NAME}.run.sh"
  rm -f "${LEGACY_STATE_DIR}/${LEGACY_WORKER_NAME}.log"
  rm -f "${LEGACY_STATE_DIR}/niuma-channel-worker-${LEGACY_WORKER_NAME}"
  rm -f "${LEGACY_STATE_DIR}/accounts/91-92/owner"
  rmdir "${LEGACY_STATE_DIR}/accounts/91-92" 2>/dev/null || true
  rm -f "${LEGACY_STATE_DIR}/accounts/94-95/owner"
  rmdir "${LEGACY_STATE_DIR}/accounts/94-95" 2>/dev/null || true
  rm -f "${PERSISTENT_STATE_DIR}/${LEGACY_PREFIX_WORKER_NAME}.env"
  rm -f "${PERSISTENT_STATE_DIR}/${LEGACY_PREFIX_WORKER_NAME}.run.sh"
  rm -f "${PERSISTENT_STATE_DIR}/${LEGACY_PREFIX_WORKER_NAME}.log"
  rm -f "${PERSISTENT_STATE_DIR}/niuma-channel-worker-${LEGACY_PREFIX_WORKER_NAME}"
  rm -f "${PERSISTENT_STATE_DIR}/accounts/101-102/owner"
  rmdir "${PERSISTENT_STATE_DIR}/accounts/101-102" 2>/dev/null || true
}
trap cleanup EXIT

mkdir -p "${TEST_HOME}" "${FAKE_BIN}"

cat >"${FAKE_BIN}/go" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
output=""
while (($#)); do
  if [[ "$1" == "-o" ]]; then
    output="$2"
    break
  fi
  shift
done
[[ -n "${output}" ]]
mkdir -p "$(dirname "${output}")"
: >"${output}"
EOF

cat >"${FAKE_BIN}/screen" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
case "${1:-}" in
  -ls)
    if [[ -f "${FAKE_SCREEN_STATE}" ]]; then
      printf '4242.%s\n' "$(cat "${FAKE_SCREEN_STATE}")"
    fi
    ;;
  -dmS)
    printf '%s\n' "$2" >"${FAKE_SCREEN_STATE}"
    ;;
  -S)
    rm -f "${FAKE_SCREEN_STATE}"
    ;;
esac
EOF

cat >"${FAKE_BIN}/ps" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ -f "${FAKE_SCREEN_STATE}" ]]; then
  printf '4242 %s channels run NIUMA_CHANNEL_ACCOUNT_ID=92\n' "${FAKE_WORKER_BINARY}"
fi
EOF

chmod +x "${FAKE_BIN}/go" "${FAKE_BIN}/screen" "${FAKE_BIN}/ps"

common_env=(
  "PATH=${FAKE_BIN}:/usr/bin:/bin"
  "HOME=${TEST_HOME}"
  "FAKE_SCREEN_STATE=${FAKE_SCREEN_STATE}"
  "FAKE_WORKER_BINARY=${WORKER_BINARY}"
  "NIUMA_CHANNEL_WORKER_NAME=${WORKER_NAME}"
)

env -i "${common_env[@]}" \
  NIUMA_CHANNEL_WORKER_BINARY="${WORKER_BINARY}" \
  NIUMA_MYSQL_DSN='test-dsn' \
  NIUMA_FEISHU_CREDENTIAL_FILE='test-credentials.json' \
  NIUMA_CHANNEL_TENANT_ID=91 \
  NIUMA_CHANNEL_ACCOUNT_ID=92 \
  NIUMA_CHANNEL_ACCOUNT_KEY='persistent-worker' \
  NIUMA_CHANNEL_USER_ID=93 \
  NIUMA_CHANNEL_PAYLOAD_KEY='test-payload-key' \
  /bin/bash "${SCRIPT}" start >/dev/null

CONFIG_FILE="${PERSISTENT_STATE_DIR}/${WORKER_NAME}.env"
[[ -f "${CONFIG_FILE}" ]] || {
  echo "expected persistent worker config at ${CONFIG_FILE}" >&2
  exit 1
}
[[ "$(stat -f '%Sp' "${CONFIG_FILE}")" == '-rw-------' ]]

env -i "${common_env[@]}" /bin/bash "${SCRIPT}" stop >/dev/null
[[ -f "${CONFIG_FILE}" ]] || {
  echo "stop removed persistent worker config" >&2
  exit 1
}

# Duplicate detection must also find a legacy worker that still exposes the
# account through the historical environment variable.
cat >"${FAKE_BIN}/ps" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${FAKE_LEGACY_PROCESS:-}" == "1" ]]; then
  printf '4242 %s channels run NIUMA_CHANNEL_ACCOUNT_ID=92\n' "${FAKE_WORKER_BINARY}"
fi
EOF
chmod +x "${FAKE_BIN}/ps"
if env -i "${common_env[@]}" \
  FAKE_LEGACY_PROCESS=1 \
  NIUMA_CHANNEL_WORKER_BINARY="${WORKER_BINARY}" \
  NIUMA_MYSQL_DSN='test-dsn' \
  NIUMA_FEISHU_CREDENTIAL_FILE='test-credentials.json' \
  NIUMA_CHANNEL_TENANT_ID=91 \
  NIUMA_CHANNEL_ACCOUNT_ID=92 \
  NIUMA_CHANNEL_ACCOUNT_KEY='persistent-worker' \
  NIUMA_CHANNEL_USER_ID=93 \
  NIUMA_CHANNEL_PAYLOAD_KEY='test-payload-key' \
  /bin/bash "${SCRIPT}" start >/dev/null 2>&1; then
  echo "legacy account marker was not detected" >&2
  exit 1
fi

cat >"${FAKE_BIN}/ps" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ -f "${FAKE_SCREEN_STATE}" ]]; then
  printf '4242 %s channels run NIUMA_CHANNEL_ACCOUNT_ID=92\n' "${FAKE_WORKER_BINARY}"
fi
EOF
chmod +x "${FAKE_BIN}/ps"

# A fresh login shell after reboot has only HOME and the worker name. The
# launcher must recover every required setting from the durable config.
env -i "${common_env[@]}" /bin/bash "${SCRIPT}" start >/dev/null
env -i "${common_env[@]}" /bin/bash "${SCRIPT}" status >/dev/null
env -i "${common_env[@]}" /bin/bash "${SCRIPT}" stop >/dev/null

legacy_prefix_env=(
  "PATH=${FAKE_BIN}:/usr/bin:/bin"
  "HOME=${TEST_HOME}"
  "FAKE_SCREEN_STATE=${FAKE_SCREEN_STATE}"
  "FAKE_WORKER_BINARY=${LEGACY_PREFIX_WORKER_BINARY}"
  "NIUMA_CHANNEL_WORKER_NAME=${LEGACY_PREFIX_WORKER_NAME}"
)
env -i "${legacy_prefix_env[@]}" \
  NIUMA_SQLITE_PATH='legacy-desktop.sqlite' \
  NIUMA_FEISHU_CREDENTIAL_FILE='legacy-credentials.json' \
  NIUMA_CHANNEL_TENANT_ID=101 \
  NIUMA_CHANNEL_ACCOUNT_ID=102 \
  NIUMA_CHANNEL_ACCOUNT_KEY='legacy-prefix-worker' \
  NIUMA_CHANNEL_USER_ID=103 \
  NIUMA_CHANNEL_PAYLOAD_KEY='legacy-payload-key' \
  /bin/bash "${SCRIPT}" start >/dev/null
LEGACY_PREFIX_CONFIG="${PERSISTENT_STATE_DIR}/${LEGACY_PREFIX_WORKER_NAME}.env"
grep -q '^NIUMA_SQLITE_PATH=legacy-desktop.sqlite$' "${LEGACY_PREFIX_CONFIG}"
grep -q '^NIUMA_FEISHU_CREDENTIAL_FILE=legacy-credentials.json$' "${LEGACY_PREFIX_CONFIG}"
grep -q '^NIUMA_CHANNEL_TENANT_ID=101$' "${LEGACY_PREFIX_CONFIG}"
grep -q '^NIUMA_CHANNEL_ACCOUNT_ID=102$' "${LEGACY_PREFIX_CONFIG}"
grep -q '^NIUMA_CHANNEL_USER_ID=103$' "${LEGACY_PREFIX_CONFIG}"
env -i "${legacy_prefix_env[@]}" /bin/bash "${SCRIPT}" stop >/dev/null

env -i "${common_env[@]}" \
  NIUMA_CHANNEL_MODEL_PROVIDER='updated-provider' \
  NIUMA_CHANNEL_MODEL='updated-model' \
  /bin/bash "${SCRIPT}" start >/dev/null
grep -q '^NIUMA_CHANNEL_MODEL_PROVIDER=updated-provider$' "${CONFIG_FILE}"
grep -q '^NIUMA_CHANNEL_MODEL=updated-model$' "${CONFIG_FILE}"
env -i "${common_env[@]}" /bin/bash "${SCRIPT}" stop >/dev/null

mkdir -p "${LEGACY_STATE_DIR}"
cat >"${LEGACY_STATE_DIR}/${LEGACY_WORKER_NAME}.env" <<EOF
NIUMA_MYSQL_DSN=test-dsn
NIUMA_FEISHU_CREDENTIAL_FILE=test-credentials.json
NIUMA_CHANNEL_TENANT_ID=94
NIUMA_CHANNEL_ACCOUNT_ID=95
NIUMA_CHANNEL_ACCOUNT_KEY=legacy-worker
NIUMA_CHANNEL_USER_ID=96
NIUMA_CHANNEL_PAYLOAD_KEY=test-payload-key
WORKSPACE=${ROOT}
NIUMA_CHANNEL_WORKSPACE_ROOTS=${ROOT}
NIUMA_CHANNEL_PERMISSION_MODE=ask
NIUMA_CHANNEL_SETTINGS=
NIUMA_CHANNEL_SETTINGS_FILE=
NIUMA_CONFIG_DIR=
NIUMA_CHANNEL_MODEL_PROVIDER=jiuan-responses-gpt-5.6sol
NIUMA_CHANNEL_MODEL=gpt-5.6-sol
EOF
chmod 600 "${LEGACY_STATE_DIR}/${LEGACY_WORKER_NAME}.env"

legacy_env=(
  "PATH=${FAKE_BIN}:/usr/bin:/bin"
  "HOME=${TEST_HOME}"
  "FAKE_SCREEN_STATE=${FAKE_SCREEN_STATE}"
  "FAKE_WORKER_BINARY=${LEGACY_WORKER_BINARY}"
  "NIUMA_CHANNEL_WORKER_NAME=${LEGACY_WORKER_NAME}"
)
env -i "${legacy_env[@]}" /bin/bash "${SCRIPT}" start >/dev/null
[[ -f "${PERSISTENT_STATE_DIR}/${LEGACY_WORKER_NAME}.env" ]] || {
  echo "legacy worker config was not migrated" >&2
  exit 1
}
[[ ! -f "${LEGACY_STATE_DIR}/${LEGACY_WORKER_NAME}.env" ]] || {
  echo "legacy worker config remains in temporary storage" >&2
  exit 1
}
env -i "${legacy_env[@]}" /bin/bash "${SCRIPT}" status >/dev/null
env -i "${legacy_env[@]}" /bin/bash "${SCRIPT}" stop >/dev/null

cat >"${PERSISTENT_STATE_DIR}/${STALE_LOCK_WORKER_NAME}.env" <<EOF
NIUMA_MYSQL_DSN=test-dsn
NIUMA_FEISHU_CREDENTIAL_FILE=test-credentials.json
NIUMA_CHANNEL_TENANT_ID=97
NIUMA_CHANNEL_ACCOUNT_ID=98
NIUMA_CHANNEL_ACCOUNT_KEY=stale-lock-worker
NIUMA_CHANNEL_USER_ID=99
NIUMA_CHANNEL_PAYLOAD_KEY=test-payload-key
WORKSPACE=${ROOT}
NIUMA_CHANNEL_WORKSPACE_ROOTS=${ROOT}
NIUMA_CHANNEL_PERMISSION_MODE=ask
NIUMA_CHANNEL_SETTINGS=
NIUMA_CHANNEL_SETTINGS_FILE=
NIUMA_CONFIG_DIR=
NIUMA_CHANNEL_MODEL_PROVIDER=jiuan-responses-gpt-5.6sol
NIUMA_CHANNEL_MODEL=gpt-5.6-sol
EOF
chmod 600 "${PERSISTENT_STATE_DIR}/${STALE_LOCK_WORKER_NAME}.env"
mkdir -p "${PERSISTENT_STATE_DIR}/accounts/97-98"
printf '%s\n%s\n%s\n' 'old-screen-after-reboot' "${STALE_LOCK_WORKER_NAME}" "$$" >"${PERSISTENT_STATE_DIR}/accounts/97-98/owner"

stale_lock_env=(
  "PATH=${FAKE_BIN}:/usr/bin:/bin"
  "HOME=${TEST_HOME}"
  "FAKE_SCREEN_STATE=${FAKE_SCREEN_STATE}"
  "FAKE_WORKER_BINARY=${STALE_LOCK_WORKER_BINARY}"
  "NIUMA_CHANNEL_WORKER_NAME=${STALE_LOCK_WORKER_NAME}"
)
env -i "${stale_lock_env[@]}" /bin/bash "${SCRIPT}" start >/dev/null
env -i "${stale_lock_env[@]}" /bin/bash "${SCRIPT}" status >/dev/null

echo "channel-worker persistent recovery test passed"
