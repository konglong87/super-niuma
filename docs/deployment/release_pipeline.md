# Release Pipeline

本仓库的正式发布入口是 GitHub Actions 的 `Release` workflow。推送
`v*` tag 时会创建 GitHub Release；手动运行 workflow 只生成供维护者检查的
Preview artifacts，不会创建正式 Release。

## 发布前准备

macOS 支持两种正式发布模式。若暂时没有 Apple Developer 账号或凭据，可以不
配置任何 Apple Secret，照常公开发布未签名/未公证的 DMG；用户首次打开时按
README 中的 Gatekeeper 提示手动允许打开。若要让用户下载后直接通过 Gatekeeper，
则将下面六个 Secret **全部**配置到仓库的 GitHub Actions Secrets：

| Secret | 内容 |
| --- | --- |
| `APPLE_CERTIFICATE_P12_BASE64` | Developer ID Application 证书导出的 `.p12` 文件，经 base64 编码 |
| `APPLE_CERTIFICATE_PASSWORD` | `.p12` 导出密码 |
| `APPLE_SIGNING_IDENTITY` | 证书完整名称，例如 `Developer ID Application: Example, Inc. (TEAMID)` |
| `APPLE_ID` | Apple Developer 账号邮箱 |
| `APPLE_TEAM_ID` | Apple Developer Team ID |
| `APPLE_APP_SPECIFIC_PASSWORD` | 用于 `notarytool` 的 Apple ID app-specific password |

证书和密码不进入仓库、不写入 workflow 文件。tag 发布时六个 Secret 全部为空会
进入 `unsigned` 模式；六个全部存在会进入 Developer ID 签名和 notarization
模式。只配置其中一部分会直接失败，避免生成状态不明确的 macOS 包。手动运行
workflow 始终生成未签名 Preview artifact。

未签名 macOS 包的首次打开：

1. 双击 `.dmg`，将 `niuma.app` 拖入“应用程序”。
2. 首次打开被阻止时，打开“系统设置 → 隐私与安全性 → 安全性”。
3. 点击“仍要打开”，按系统提示确认。

## 发布命令

先确保目标 commit 已通过快速 CI；发布前如近期没有跑过维护检查，再手动运行
`Quality and maintenance` workflow，确认拓扑、Swagger 生成物、脚本离线检查和
`govulncheck` 没有新的阻塞项，然后创建并推送版本 tag：

```bash
git tag -a v0.1.0 -m "Release v0.1.0"
git push origin v0.1.0
```

快速 CI 与 Release 是两个独立 workflow：快速 CI 验证源码质量，Release 在 tag
上重新构建三平台产物、校验清单和 SHA256，并执行发布后验收。Release 不会因为
某一次普通分支 CI 失败而自动停止；因此不能把“Release 成功”理解为所有维护
检查都通过。tag push 不再重复触发普通 CI，避免同一版本出现两套重复构建信号。

## 自动产物

Release workflow 会并行生成：

```text
niuma-v0.1.0-macos-arm64.dmg
niuma-v0.1.0-macos-amd64.dmg
niuma_v0.1.0_linux_amd64.tar.gz
niuma-desktop_v0.1.0_linux_amd64.tar.gz
niuma_v0.1.0_linux_arm64.tar.gz
niuma_v0.1.0_darwin_amd64.tar.gz
niuma_v0.1.0_darwin_arm64.tar.gz
niuma_v0.1.0_windows_amd64.zip
niuma-setup.exe
SHA256SUMS
RELEASE_NOTES.md
```

CLI 归档也会继续保留在同一 Release 中。所有最终资产由 publish job 统一生成
`SHA256SUMS`，避免不同构建 job 产生互相覆盖的校验文件。
CLI 和桌面 Linux 归档使用不同名称，不能合并为同一个文件。
发布前检查全部 9 个包非空、清单记录唯一，并重新计算 SHA256；
缺包、多包、重复记录或内容被修改都会中止发布。
手动预览也执行同一校验，输出 `verified-preview-*` artifact，
包含全部包、`SHA256SUMS` 和 `RELEASE_NOTES.md`，但不会创建 Release。

## 验收

`publish` 成功后，workflow 会从刚创建的 GitHub Release 重新下载资产并自动执行
三平台验收；维护者仍需查看 job 日志和 Release 页面：

1. macOS DMG 挂载、安装复制和启动是否通过；签名模式校验会在配置
   Developer ID 时执行，未签名模式会跳过 Gatekeeper 评估。
2. Windows 安装程序是否能安装、启动和卸载，并保留用户数据。
3. Linux 归档是否能解包，`niuma-desktop` 与旁边的 `niuma` 是否都存在，sidecar 版本是否正确。
4. 各平台下载的资产校验和是否通过。
5. Release 页面、README 安装说明和版本 tag 是否指向同一个 commit。

移动端 Android/iOS 不属于当前桌面 Release workflow，需要后续分别建立
Android APK/AAB 和 iOS IPA/TestFlight 发布链路。
