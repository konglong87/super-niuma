#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DESKTOP_DIR="${ROOT}/desktop-v2"
VERSION="${VERSION:?VERSION is required}"
ARCH="${ARCH:-amd64}"
DIST_DIR="${DIST_DIR:-${ROOT}/dist}"
PLATFORM="linux/${ARCH}"
STAGE="niuma-desktop_${VERSION}_linux_${ARCH}"
ARCHIVE="${DIST_DIR}/${STAGE}.tar.gz"

if [[ "${ARCH}" != "amd64" ]]; then
  echo "package-desktop-v2-linux.sh: unsupported architecture: ${ARCH}" >&2
  exit 2
fi

mkdir -p "${DIST_DIR}"
bash "${ROOT}/scripts/build-desktop-v2.sh" -platform "${PLATFORM}" -tags webkit2_41

DESKTOP_BINARY="${DESKTOP_DIR}/build/bin/niuma-desktop"
SERVER_BINARY="${DESKTOP_DIR}/niuma"
if [[ ! -s "${DESKTOP_BINARY}" ]]; then
  echo "package-desktop-v2-linux.sh: missing Wails binary: ${DESKTOP_BINARY}" >&2
  exit 1
fi
if [[ ! -s "${SERVER_BINARY}" ]]; then
  echo "package-desktop-v2-linux.sh: missing sidecar binary: ${SERVER_BINARY}" >&2
  exit 1
fi

stage_dir="$(mktemp -d)"
trap 'rm -rf "${stage_dir}"' EXIT
mkdir -p "${stage_dir}/${STAGE}/THIRD_PARTY_LICENSES/termenv"
cp "${DESKTOP_BINARY}" "${stage_dir}/${STAGE}/niuma-desktop"
cp "${SERVER_BINARY}" "${stage_dir}/${STAGE}/niuma"
cp "${ROOT}/README.md" "${stage_dir}/${STAGE}/README.md"
cp "${ROOT}/LICENSE" "${stage_dir}/${STAGE}/LICENSE"
cp "${ROOT}/third_party/termenv/LICENSE" "${stage_dir}/${STAGE}/THIRD_PARTY_LICENSES/termenv/LICENSE"
chmod 755 "${stage_dir}/${STAGE}/niuma-desktop" "${stage_dir}/${STAGE}/niuma"

tar --format=ustar --owner=0 --group=0 --numeric-owner \
  -czf "${ARCHIVE}" -C "${stage_dir}" "${STAGE}"
printf 'packaged %s\n' "${ARCHIVE}"
