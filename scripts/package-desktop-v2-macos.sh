#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DESKTOP_DIR="${ROOT}/desktop-v2"
VERSION="${VERSION:?VERSION is required}"
ARCH="${ARCH:?ARCH is required}"
DIST_DIR="${DIST_DIR:-${ROOT}/dist}"
REQUIRE_NOTARIZATION="${REQUIRE_NOTARIZATION:-0}"
PLATFORM="darwin/${ARCH}"
DMG="${DIST_DIR}/niuma-${VERSION}-macos-${ARCH}.dmg"
APP_PATH="${DESKTOP_DIR}/build/bin/niuma.app"

case "${ARCH}" in
  arm64|amd64) ;;
  *)
    echo "package-desktop-v2-macos.sh: unsupported architecture: ${ARCH}" >&2
    exit 2
    ;;
esac

mkdir -p "${DIST_DIR}"
MACOS_ADHOC_SIGN="${REQUIRE_NOTARIZATION}" MACOS_VERIFY_SIGNATURE="${REQUIRE_NOTARIZATION}" bash "${ROOT}/scripts/build-desktop-v2.sh" -platform "${PLATFORM}"

if [[ ! -d "${APP_PATH}" ]]; then
  echo "package-desktop-v2-macos.sh: missing app bundle: ${APP_PATH}" >&2
  exit 1
fi

if [[ "${REQUIRE_NOTARIZATION}" == "1" ]]; then
  : "${APPLE_SIGNING_IDENTITY:?APPLE_SIGNING_IDENTITY is required for signed releases}"
  : "${APPLE_ID:?APPLE_ID is required for notarized releases}"
  : "${APPLE_TEAM_ID:?APPLE_TEAM_ID is required for notarized releases}"
  : "${APPLE_APP_SPECIFIC_PASSWORD:?APPLE_APP_SPECIFIC_PASSWORD is required for notarized releases}"

  codesign --force --options runtime --timestamp --sign "${APPLE_SIGNING_IDENTITY}" \
    "${APP_PATH}/Contents/MacOS/niuma-desktop"
  codesign --force --options runtime --timestamp --sign "${APPLE_SIGNING_IDENTITY}" \
    "${APP_PATH}/Contents/MacOS/niuma"
  codesign --force --options runtime --timestamp --sign "${APPLE_SIGNING_IDENTITY}" \
    "${APP_PATH}/Contents/Helpers/ComputerHelper.app"
  codesign --force --options runtime --timestamp --sign "${APPLE_SIGNING_IDENTITY}" \
    "${APP_PATH}"
  codesign --verify --deep --strict --verbose=2 "${APP_PATH}"
else
  echo "Unsigned/unnotarized macOS package: skipping final nested signature verification."
fi

staging="$(mktemp -d)"
trap 'rm -rf "${staging}"' EXIT
mkdir -p "${staging}/niuma"
cp -R "${APP_PATH}" "${staging}/niuma/"

rm -f "${DMG}"
hdiutil create \
  -volname "niuma ${VERSION}" \
  -srcfolder "${staging}" \
  -ov \
  -format UDZO \
  "${DMG}"

if [[ "${REQUIRE_NOTARIZATION}" == "1" ]]; then
  xcrun notarytool submit "${DMG}" \
    --apple-id "${APPLE_ID}" \
    --team-id "${APPLE_TEAM_ID}" \
    --password "${APPLE_APP_SPECIFIC_PASSWORD}" \
    --wait
  xcrun stapler staple "${DMG}"
  xcrun stapler validate "${DMG}"
fi

printf 'packaged %s\n' "${DMG}"
