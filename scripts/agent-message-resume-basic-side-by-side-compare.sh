#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

"$ROOT_DIR/scripts/agent-message-resume-side-by-side-compare.sh" \
  --model agent-message-resume-basic-stub \
  --no-worktree-resume \
  "$@"
