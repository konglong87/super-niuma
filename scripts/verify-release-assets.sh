#!/usr/bin/env bash
set -euo pipefail

VERSION="${1:?usage: verify-release-assets.sh VERSION DIST_DIR}"
DIST_DIR="${2:?usage: verify-release-assets.sh VERSION DIST_DIR}"
export LC_ALL=C

expected=(
  "niuma_${VERSION}_darwin_arm64.tar.gz"
  "niuma_${VERSION}_darwin_amd64.tar.gz"
  "niuma_${VERSION}_linux_amd64.tar.gz"
  "niuma_${VERSION}_linux_arm64.tar.gz"
  "niuma_${VERSION}_windows_amd64.zip"
  "niuma-${VERSION}-macos-arm64.dmg"
  "niuma-${VERSION}-macos-amd64.dmg"
  "niuma-desktop_${VERSION}_linux_amd64.tar.gz"
  "niuma-setup.exe"
)

for asset in "${expected[@]}"; do
  path="${DIST_DIR}/${asset}"
  if [[ ! -s "${path}" ]]; then
    echo "verify-release-assets.sh: missing or empty asset: ${asset}" >&2
    exit 1
  fi
done

shopt -s nullglob
actual=("${DIST_DIR}"/niuma-* "${DIST_DIR}"/niuma_*)
if [[ "${#actual[@]}" -ne "${#expected[@]}" ]]; then
  echo "verify-release-assets.sh: expected ${#expected[@]} assets, found ${#actual[@]}" >&2
  printf 'found: %s\n' "${actual[@]##*/}" >&2
  exit 1
fi

for path in "${actual[@]}"; do
  if [[ ! -f "${path}" ]]; then
    echo "verify-release-assets.sh: asset path is not a file: ${path}" >&2
    exit 1
  fi
done

manifest="${DIST_DIR}/SHA256SUMS"
if [[ ! -s "${manifest}" ]]; then
  echo "verify-release-assets.sh: missing or empty SHA256SUMS" >&2
  exit 1
fi

if [[ "$(wc -l < "${manifest}" | tr -d ' ')" -ne "${#expected[@]}" ]]; then
  echo "verify-release-assets.sh: unexpected checksum count" >&2
  exit 1
fi
for asset in "${expected[@]}"; do
  if ! awk -v name="${asset}" '
    length($1) == 64 && $1 !~ /[^0-9a-fA-F]/ && $2 == name && NF == 2 { count++ }
    END { exit count != 1 }
  ' "${manifest}"; then
    echo "verify-release-assets.sh: missing or duplicate checksum for: ${asset}" >&2
    exit 1
  fi
done
(
  cd "${DIST_DIR}"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum -c SHA256SUMS
  else
    shasum -a 256 -c SHA256SUMS
  fi
)

notes="${DIST_DIR}/RELEASE_NOTES.md"
if [[ ! -s "${notes}" ]]; then
  echo "verify-release-assets.sh: missing or empty RELEASE_NOTES.md" >&2
  exit 1
fi
for heading in "## 新增功能" "## 修复问题" "## 安装方式" "## 已知问题" "## 系统要求"; do
  if ! grep -Fqx "${heading}" "${notes}"; then
    echo "verify-release-assets.sh: missing Release Notes section: ${heading}" >&2
    exit 1
  fi
done

printf 'verified %s release assets for %s\n' "${#expected[@]}" "${VERSION}"
