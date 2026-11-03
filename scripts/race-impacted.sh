#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BASE_SHA="${1:?usage: scripts/race-impacted.sh <base-sha>}"

cd "$ROOT_DIR"
scope="$(go run ./scripts/race-impact --base "$BASE_SHA")"

if [[ "$scope" == "__FULL__" ]]; then
  echo "race scope: full repository"
  go test -race ./... -count=1
  exit 0
fi

if [[ -z "$scope" ]]; then
  echo "race scope: no Go source changes; skipped"
  exit 0
fi

packages=()
while IFS= read -r package; do
  [[ -n "$package" ]] && packages+=("$package")
done <<<"$scope"
echo "race scope: ${#packages[@]} impacted package(s)"
printf '  %s\n' "${packages[@]}"
go test -race -count=1 "${packages[@]}"
