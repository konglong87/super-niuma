#!/usr/bin/env bash
set -euo pipefail

readonly DEFAULT_MAX_CACHE_GB=6
readonly SCRIPT_NAME="$(basename "${BASH_SOURCE[0]}")"

usage() {
  cat <<EOF
Usage: $SCRIPT_NAME [--check|--status|--clean|--help]

Manage the user-level Go build cache.

Actions:
  --check   Clean the cache only when it exceeds the configured limit (default).
  --status  Print the cache path, current size, and configured limit.
  --clean   Unconditionally run 'go clean -cache'.

Environment:
  NIUMA_GO_CACHE_MAX_GB   Maximum cache size in decimal GB (default: ${DEFAULT_MAX_CACHE_GB}).
  NIUMA_GO_CACHE_MAINTENANCE=0
                           Disable automatic checks from project entrypoints.
EOF
}

action="check"
case "${1:-}" in
  "")
    ;;
  --check|--status|--clean)
    action="${1#--}"
    ;;
  -h|--help)
    usage
    exit 0
    ;;
  *)
    echo "unknown argument: $1" >&2
    usage >&2
    exit 2
    ;;
esac

max_cache_gb="${NIUMA_GO_CACHE_MAX_GB:-$DEFAULT_MAX_CACHE_GB}"
case "$max_cache_gb" in
  ''|*[!0-9]*|0)
    echo "NIUMA_GO_CACHE_MAX_GB must be a positive integer, got: $max_cache_gb" >&2
    exit 2
    ;;
esac

cache_path="$(go env GOCACHE)"
cache_size_kib=0
if [[ -d "$cache_path" ]]; then
  cache_size_kib="$(LC_ALL=C du -sk "$cache_path" | awk 'NR == 1 { print $1 }')"
fi

if [[ ! "$cache_size_kib" =~ ^[0-9]+$ ]]; then
  echo "could not determine Go cache size for: $cache_path" >&2
  exit 1
fi

max_cache_bytes=$((max_cache_gb * 1000 * 1000 * 1000))
cache_size_bytes=$((cache_size_kib * 1024))
cache_size_gb="$(awk -v kib="$cache_size_kib" 'BEGIN { printf "%.2f", kib * 1024 / 1000000000 }')"

print_status() {
  printf 'Go build cache: %s (%s GB), limit: %s GB\n' \
    "$cache_path" "$cache_size_gb" "$max_cache_gb"
}

if [[ "$action" == "status" ]]; then
  print_status
  exit 0
fi

if [[ "$action" == "clean" ]]; then
  print_status
  echo "Cleaning Go build cache..."
  go clean -cache
  exit 0
fi

if (( cache_size_bytes > max_cache_bytes )); then
  print_status
  echo "Go build cache exceeds the limit; cleaning..."
  go clean -cache
else
  print_status
  echo "Go build cache is within the limit; keeping it."
fi
