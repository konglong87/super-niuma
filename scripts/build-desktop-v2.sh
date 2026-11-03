#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DESKTOP_DIR="${ROOT}/desktop-v2"
COMPUTER_USE_RELEASE_CANDIDATE="${COMPUTER_USE_RELEASE_CANDIDATE:-0}"

if [[ "${COMPUTER_USE_RELEASE_CANDIDATE}" == "1" ]]; then
  if [[ -n "$(git -C "${ROOT}" status --porcelain=v1 --untracked-files=all)" ]]; then
    echo "release candidate requires a clean git worktree" >&2
    exit 2
  fi
fi

VITE_DESKTOP_UI_VERSION=2 npm --prefix "${ROOT}/web" run build -- --mode desktop-v2
FRONTEND_DIST="${DESKTOP_DIR}/frontend/dist"
mkdir -p "${FRONTEND_DIST}"
# Refresh generated assets without deleting the Git-tracked embed placeholder.
find "${FRONTEND_DIST}" -mindepth 1 -maxdepth 1 ! -name ".gitkeep" -exec rm -rf -- {} +
if [[ ! -f "${FRONTEND_DIST}/.gitkeep" ]]; then
  printf '%s\n' \
    'Go embed placeholder for fresh-clone checks only; not a frontend.' \
    'Build the real desktop UI with scripts/build-desktop-v2.sh before running Wails.' \
    > "${FRONTEND_DIST}/.gitkeep"
fi
rm -f "${DESKTOP_DIR}/niuma"
cp -R "${ROOT}/web/dist/." "${FRONTEND_DIST}/"

LDFLAGS="-s -w" OUTPUT="${DESKTOP_DIR}/niuma" \
  "${ROOT}/scripts/build.sh" >/dev/null
(
  cd "${DESKTOP_DIR}"
  wails build -s "$@"
)

APP_BIN="${DESKTOP_DIR}/build/bin/niuma.app/Contents/MacOS"
APP_PATH="${DESKTOP_DIR}/build/bin/niuma.app"
# Ad-hoc signatures otherwise designate the binary by cdhash, so every local
# rebuild invalidates the user's TCC grant. A stable designated requirement
# keeps the local development identity consistent until a real Apple identity
# is supplied for release signing.
MACOS_ADHOC_DESIGNATED_REQUIREMENT="${MACOS_ADHOC_DESIGNATED_REQUIREMENT:-=designated => identifier \"com.wails.niuma\"}"
MACOS_ADHOC_SIGN="${MACOS_ADHOC_SIGN:-1}"
MACOS_VERIFY_SIGNATURE="${MACOS_VERIFY_SIGNATURE:-1}"
macos_codesign() {
  local target="$1"
  codesign --force --identifier "com.wails.niuma" --sign - --requirements "${MACOS_ADHOC_DESIGNATED_REQUIREMENT}" --timestamp=none "${target}"
}
if [[ -d "${APP_BIN}" ]]; then
  # Unlink before replacing embedded executables. Reusing an inode can leave
  # macOS's code-signature pager with stale page hashes and later kill the
  # long-lived local server with CODESIGNING/Invalid Page.
  rm -f "${APP_BIN}/niuma"
  cp "${DESKTOP_DIR}/niuma" "${APP_BIN}/niuma"

  if [[ "$(uname -s)" == "Darwin" ]]; then
    helper_arch="$(uname -m)"
    for arg in "$@"; do
      case "${arg}" in
        darwin/arm64|darwin/amd64) helper_arch="${arg#darwin/}" ;;
      esac
    done
    helper_dir="${APP_PATH}/Contents/Helpers"
    # Remove the pre-bundle helper path from older builds; leaving it behind
    # would keep an unsigned/stale resource in the outer app seal.
    rm -f "${helper_dir}/computer-helper-macos"
    helper_app="${helper_dir}/ComputerHelper.app"
    mkdir -p "${helper_app}/Contents/MacOS"
    MACOS_ARCH="${helper_arch}" bash "${ROOT}/scripts/build-computer-helper-macos.sh" "${helper_app}/Contents/MacOS/computer-helper-macos" >/dev/null
    # The service/helper binaries are embedded after Wails creates its app signature.
    # Sign every executable/bundle with the same stable local requirement, then
    # seal only the outer app (without --deep re-signing nested bundles). Unsigned
    # release packaging skips this step so a missing Apple identity remains an
    # honest unsigned DMG instead of failing while trying to seal nested code.
    if [[ "${MACOS_ADHOC_SIGN}" == "1" ]]; then
      macos_codesign "${APP_BIN}/niuma-desktop"
      macos_codesign "${APP_BIN}/niuma"
      macos_codesign "${helper_app}"
    else
      echo "Skipping ad-hoc macOS code signing for unsigned packaging."
    fi
    # Attest the exact committed source and executable bytes before sealing.
    python3 - "${APP_PATH}" "${ROOT}" <<'PYBUILD'
import datetime, hashlib, json, os, pathlib, subprocess, sys
app, root = map(pathlib.Path, sys.argv[1:])
def digest(path):
    sha = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            sha.update(chunk)
    return sha.hexdigest()
def command_output(args, fallback="unavailable"):
    try:
        return subprocess.check_output(args, text=True, stderr=subprocess.DEVNULL).strip() or fallback
    except (OSError, subprocess.CalledProcessError):
        return fallback
commit = subprocess.check_output(["git", "-C", str(root), "rev-parse", "HEAD"], text=True).strip()
status = subprocess.check_output(["git", "-C", str(root), "status", "--porcelain=v1", "--untracked-files=all"], text=True)
paths = []
for line in status.splitlines():
    value = line[3:] if len(line) >= 4 else ""
    if " -> " in value:
        value = value.rsplit(" -> ", 1)[-1]
    if value:
        paths.append(value)
web_dirty = bool(paths) and all(path == "web" or path.startswith("web/") for path in paths)
runtime_dirty = bool(paths) and not web_dirty
resources = app / "Contents/Resources"
resources.mkdir(exist_ok=True)
(resources / "computer-use-build.json").write_text(json.dumps({
    "schema_version": "computer-use-build.v1", "source_commit": commit,
    # Web UI-only edits do not alter the Computer Use runtime/service/helper
    # bytes. Keep that fact explicit instead of mixing UI work into runtime
    # evidence or silently pretending the entire workspace is clean.
    "source_dirty": runtime_dirty, "web_source_dirty": web_dirty,
    "release_candidate": os.environ.get("COMPUTER_USE_RELEASE_CANDIDATE", "0") == "1",
    "version": command_output(["git", "-C", str(root), "describe", "--tags", "--always", "--dirty"]),
    "built_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
    "go_version": command_output(["go", "version"]),
    "node_version": command_output(["node", "--version"]),
    "wails_version": command_output(["wails", "version"]),
    "target": "/".join(command_output(["go", "env", "GOOS", "GOARCH"]).split()),
    "signing_mode": "adhoc" if os.environ.get("MACOS_ADHOC_SIGN", "1") == "1" else "unsigned",
    # Outer app sealing re-signs its main executable; a full SHA here would be circular.
    "desktop_build_id": subprocess.check_output(["go", "tool", "buildid", str(app / "Contents/MacOS/niuma-desktop")], text=True).strip(),
    "helper_sha256": digest(app / "Contents/Helpers/ComputerHelper.app/Contents/MacOS/computer-helper-macos"),
    "service_sha256": digest(app / "Contents/MacOS/niuma"),
}, indent=2) + "\n")
PYBUILD
    if [[ "${MACOS_ADHOC_SIGN}" == "1" ]]; then
      codesign --force --sign - --requirements "${MACOS_ADHOC_DESIGNATED_REQUIREMENT}" --timestamp=none "${APP_PATH}"
      if [[ "${MACOS_VERIFY_SIGNATURE}" == "1" ]]; then
        codesign --verify --deep --strict --verbose=2 "${APP_PATH}"
      else
        echo "Skipping macOS nested signature verification for unsigned/unnotarized packaging."
      fi
    else
      echo "Skipping outer macOS code signing for unsigned packaging."
    fi
  fi
  # Keep Finder's package modification time aligned with the actual build.
  touch "${APP_PATH}"
  if [[ "${COMPUTER_USE_RELEASE_CANDIDATE}" == "1" ]]; then
    python3 "${ROOT}/scripts/verify-computer-use-build.py" \
      --app "${APP_PATH}" --workspace "${ROOT}" \
      --require-clean --require-release-candidate --require-signature
  fi
fi
