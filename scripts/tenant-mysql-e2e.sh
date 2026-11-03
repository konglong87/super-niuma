#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/product-env.sh"


MYSQL_HOST="${NIUMA_MYSQL_E2E_HOST:-127.0.0.1}"
MYSQL_PORT="${NIUMA_MYSQL_E2E_PORT:-3306}"
MYSQL_USER="${NIUMA_MYSQL_E2E_USER:-root}"
MYSQL_PASSWORD="${NIUMA_MYSQL_E2E_PASSWORD:-}"
MYSQL_DATABASE="${NIUMA_MYSQL_E2E_DATABASE:-golang_cc_tenant_e2e}"
MYSQL_TIME_ZONE="${NIUMA_MYSQL_E2E_TIME_ZONE:-%27%2B00%3A00%27}"

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required command: $1" >&2
    exit 1
  }
}

need mysql
need go

mysql_args=(-h"$MYSQL_HOST" -P"$MYSQL_PORT" -u"$MYSQL_USER")
if [[ -n "$MYSQL_PASSWORD" ]]; then
  mysql_args+=(-p"$MYSQL_PASSWORD")
fi

echo "Resetting MySQL database $MYSQL_DATABASE on $MYSQL_HOST:$MYSQL_PORT"
mysql "${mysql_args[@]}" -e "DROP DATABASE IF EXISTS \`$MYSQL_DATABASE\`; CREATE DATABASE \`$MYSQL_DATABASE\` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;"

auth_prefix="$MYSQL_USER"
if [[ -n "$MYSQL_PASSWORD" ]]; then
  auth_prefix="$MYSQL_USER:$MYSQL_PASSWORD"
fi
export NIUMA_MYSQL_E2E_DSN="${NIUMA_MYSQL_E2E_DSN:-$auth_prefix@tcp($MYSQL_HOST:$MYSQL_PORT)/$MYSQL_DATABASE?multiStatements=true&parseTime=true&loc=UTC&time_zone=$MYSQL_TIME_ZONE&charset=utf8mb4}"

echo "Running tenant/server/storage MySQL E2E tests"
go test ./internal/storage/mysql ./internal/tenant ./internal/server -run 'TestMySQLE2E' -count=1 -v

echo "tenant mysql e2e passed"
