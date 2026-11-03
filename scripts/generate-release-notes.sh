#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="${VERSION:?VERSION is required}"
OUTPUT="${OUTPUT:-${ROOT}/dist/RELEASE_NOTES.md}"
TAG="${TAG:-${VERSION}}"
MACOS_RELEASE_MODE="${MACOS_RELEASE_MODE:-unsigned}"

previous_tag=""
if git rev-parse "${TAG}^" >/dev/null 2>&1; then
  previous_tag="$(git describe --tags --abbrev=0 "${TAG}^" 2>/dev/null || true)"
fi

{
  printf '# niuma %s\n\n' "${VERSION}"
  printf '## 新增功能\n\n'
  printf -- '- 本版本改动见下方提交列表。\n\n'
  printf '## 修复问题\n\n'
  printf -- '- 具体修复项以本版本提交记录和变更文件为准。\n\n'
  printf '## 安装方式\n\n'
  if [[ "${MACOS_RELEASE_MODE}" == "signed" ]]; then
    printf -- '- macOS：下载对应架构的 `.dmg`，打开后将 `niuma.app` 拖入“应用程序”；本版本已使用 Developer ID 签名并完成 notarization。\n'
  else
    printf -- '- macOS：下载对应架构的 `.dmg`，打开后将 `niuma.app` 拖入“应用程序”。本版本未进行 Apple Developer ID 签名和 notarization；首次打开时，如果 macOS 阻止启动，请进入“系统设置 → 隐私与安全性 → 安全性”，点击“仍要打开”，再确认启动。\n'
  fi
  printf -- '- Windows：下载 `niuma-setup.exe` 并运行安装程序。\n'
  printf -- '- Linux：下载 `.tar.gz`，解压后运行 `niuma-desktop`；需安装 GTK 3 和 WebKitGTK 4.1 运行库。\n\n'
  printf '## 已知问题\n\n'
  printf -- '- Android 和 iOS 移动端不包含在本次桌面 Release 中。\n'
  printf -- '- macOS 提供 arm64 与 amd64 安装包；Windows 当前仅提供 amd64 安装包。\n\n'
  if [[ "${MACOS_RELEASE_MODE}" != "signed" ]]; then
    printf -- '- macOS 安装包未签名且未公证，首次打开可能触发 Gatekeeper，需要按安装方式中的步骤手动允许打开。\n\n'
  fi
  printf '## 系统要求\n\n'
  printf -- '- macOS 10.13 或更高版本。\n'
  printf -- '- Windows 10/11 amd64，并需要 WebView2 Runtime。\n'
  printf -- '- Linux amd64，并需要 GTK 3、WebKitGTK 4.1 和对应系统运行库。\n\n'
  printf '## 变更提交\n\n'
  if [[ -n "${previous_tag}" ]]; then
    git log --no-merges --pretty='- %h %s' "${previous_tag}..${TAG}"
  else
    git log --no-merges --pretty='- %h %s' -20 "${TAG}"
  fi
  printf '\n'
} > "${OUTPUT}"

printf 'wrote %s\n' "${OUTPUT}"
