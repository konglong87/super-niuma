# niuma Desktop

niuma 是基于 Wails v2 的唯一桌面 AI 工作台，复用本仓库的 WebUI 2.0、
HTTP/SSE API 和 runtime。不是独立 Go module，必须从仓库根目录构建。
legacy WebUI 仍可通过浏览器/server 入口使用，但不再提供第二个原生桌面包。

## 从源码构建

需要 Go、Node.js/npm、Wails CLI v2.10.2 和平台开发工具。发布工具链以根目录
`.tool-versions` 为准；macOS 需要 Xcode Command Line Tools，Windows 需要
WebView2 Runtime，制作安装包还需要 NSIS。首次安装前端与 Wails 依赖：

```bash
npm --prefix web ci --legacy-peer-deps
go install github.com/wailsapp/wails/v2/cmd/wails@v2.10.2
wails doctor
```

macOS：

```bash
scripts/build-Niuma Desktop.sh
open Niuma Desktop/build/bin/niuma.app
```

脚本以 `VITE_DESKTOP_UI_VERSION=2` 构建前端并复制到 `Niuma Desktop/frontend/dist`，
编译本地 server，再用 Wails 打包，并将 `niuma` 放入 app 的
`Contents/MacOS/`。不能只复制桌面可执行文件而遗漏 server。macOS 在复制
server 后会按“先签名嵌套 server，再签名外层 app”的顺序重新签名，并严格校验
整个 app；不要在构建完成后手工替换 `Contents/MacOS/niuma`。
脚本会重建 `web/dist`；若随后运行独立 Web server，应重新执行
`npm --prefix web run build` 生成 `/webui/` 前缀的 Web 产物。

Windows（在 Windows 上执行 PowerShell）：

```powershell
.\scripts\build-Niuma Desktop-windows.ps1
node scripts/test-Niuma Desktop-windows-packaging.mjs --require-pwsh
```

脚本目标产物为 `dist\niuma-setup.exe`，仅支持 Windows amd64。
自定义 Wails v2.10.2 NSIS 模板将 `niuma-desktop.exe` 与 `niuma.exe`
安装到同一目录；卸载删除两个程序和安装注册信息，保留用户配置、SQLite、
WebView2 数据及安装目录内的用户文件。npm、Go、Wails 任一步骤失败都会中止，
不会将旧安装包复制为本次产物。构建会重建前端和 `Niuma Desktop/build/bin`。

`Desktop Windows` workflow 已配置隔离安装、两个 EXE 的 SHA256 readback、
卸载及用户文件保留检查；本地测试使用临时 fixture，不修改已有打包产物。
无 PowerShell 时可去掉 `--require-pwsh` 仅跑静态契约检查，原生命令失败测试
会明确跳过。仅有构建配置不等于通过原生验收：此修复未触发 CI，
Windows 安装/卸载实际运行、WebView2 与 GUI 首次启动仍须在 Windows 上验收。

## 首次使用

1. 启动后自动创建并选中 `~/niuma-workspace/niuma` 作为 Default 工作区。
   该目录是 Agent 读写文件和执行命令的边界，不是数据库存储位置。
2. 如果尚未配置模型，应用会引导进入模型设置；配置自己的 provider、地址、
   凭据和模型并完成连接验证。应用不附赠模型服务或凭据。
3. 创建会话并发送消息。新建会话默认使用实际配置的主 Provider/Model；
   高级用户仍可在会话输入区手动切换。也可以随时选择其他工作区。
   后续启动会恢复上次工作区和会话首页；已有会话可从侧栏打开。
   有效深链接仍打开对应会话，无效链接显示可恢复错误。

## 桌面个性化

Desktop 提供面向长时间 Agent 任务的个性化工作台：

- `设置 → 宠物`：在 Go 伙伴和中国龙之间切换，调整动画、大小、位置、状态气泡，并按设备隐藏或恢复。
- `设置 → 外观`：设置背景颜色、背景图片 URL、透明度和空状态/对话/编辑区模糊。
- Desktop 额外支持上传、替换和移除 PNG、JPEG/JPG、WebP、GIF 本地背景。

当前实现是内置陪伴角色和视觉外观设置，不是皮肤商城，也不提供任意 3D 角色上传。完整设置项、数据边界和扩展限制见 [桌面个性化指南](../docs/usage/desktop-personalization.md)。

本地背景保存在 Desktop 本地数据目录，不会作为模型请求内容发送给 Provider。

本地 server 只绑定 `127.0.0.1` 的动态端口，使用每次启动生成的随机 token。
前端等待 health/readiness 后才访问业务接口。关闭应用会请求停止 server，
超时后结束子进程。桌面默认禁用 scheduler，不把它当作常驻渠道 worker 使用。

## 配置与数据

已实现本地 SQLite 与启动 migration，**不需要安装 MySQL**。
桌面配置和数据默认位于 `~/.niuma/`：

| 文件 | 用途 |
| --- | --- |
| `config-v2.json` | 工作目录选择 |
| `niuma.sqlite` | 本地会话、消息、提示词模板等 |
| `niuma-server.log` | 本地 server 诊断 |
| `niuma-startup.log` | 桌面启动诊断 |

Niuma Desktop 固定使用用户 home 下的 `.niuma` 目录，和平台默认的应用支持目录
无关。删除旧桌面实现后，不提供旧桌面配置或 SQLite 数据的自动迁移。

模型配置仍复用 runtime 的 `~/.niuma/settings.json`，可用
`NIUMA_CONFIG_DIR` 重定位；它与桌面工作目录配置不是同一文件。
凭据目前由 runtime 配置机制管理，**尚未接入 Keychain/Credential Manager**。
不要提交 settings、数据库、日志或包含真实内容的验收截图。

诊断环境变量：

| 变量 | 作用 |
| --- | --- |
| `NIUMA_SERVER_BINARY` | 指定本地 server 二进制 |
| `NIUMA_DESKTOP_SERVER_PORT` | 指定诊断端口；默认动态选择 |
| `NIUMA_DESKTOP_CONFIG_DIR` | 将 Niuma Desktop 数据根目录重定位到该目录下的 `niuma/` |
| `NIUMA_CONFIG_DIR` | 重定位 runtime 模型配置目录 |

需要隔离验收数据时使用独立 OS 用户，或在 macOS/Linux 为测试进程指定独立 HOME；
设置 `NIUMA_DESKTOP_CONFIG_DIR` 也会同时隔离 Niuma Desktop 的配置、SQLite 和日志。

## Computer Use（Phase 1，macOS Host）

Niuma Desktop 现在提供独立的 Computer Workspace 控制面，不修改 WebBrowser 语义：

- `GetComputerCapabilities`：读取 macOS Host 的 capture/input/focus/permission readiness。
- `GetComputerRuntimeStatus`：读取脱敏的 runtime lifecycle/fault 状态；bridge 断开时 fail-closed，不返回 token、路径或原始 transport 文本。
- `StopComputerRuntime`：用户显式确认后的 runtime teardown；bridge 不可用时使用父进程 handle 收敛，不会 replay 任何 action。
- `StartComputerSession`：创建 session-level approval 会话；未批准不会进入 ready。
- `ObserveComputerSession`：从 native helper 获取真实桌面 PNG，并展示 Preview。
- `PauseComputerSession` / `ResumeComputerSession` / `StopComputerSession`：控制同一
  session 的输入生命周期；Stop 后不会自动恢复。
- `GetComputerActionReceipt`：读取结构化 action receipt 和 before/after observation 引用。

helper 位于 `.app/Contents/Helpers/computer-helper-macos`，使用平台无关的长度前缀 JSON
协议与 Go backend 通信。helper 环境采用白名单，不继承 provider key；协议只接收经校验的结构化动作。
不得要求模型输入密码、Token、OTP 等凭据；`type` 的普通输入在执行时仍需传给 helper，
不能把脱敏摘要误认为已有凭据识别/安全输入能力。第一阶段只支持 macOS Host，不实现
Windows/Linux/TUI/Code Execution/Replay。

真实验收命令：

```bash
bash scripts/build-Niuma Desktop.sh
open Niuma Desktop/build/bin/niuma.app
```

模型 ComputerUse 已通过受信任的本地 bridge 接入 Wails Controller；在模型图片路线
已显式配置、工具权限允许且 macOS 权限就绪时，首次 `observe` 可以自动建立当前会话，
后续动作使用单次有效的 observation。输入回执并不等于视觉验收；成功动作还会返回
新截图，结果不明时停止且不重放输入。系统首次授权仍由用户完成。

当前版本可作为 **macOS 单屏 Developer Preview** 交付开发者使用。最新 tagged
desktop build 已通过 deterministic/native gates，并完成一次真实的
`gpt-6-sol/high` 闭环：launch、click、Command+A、type、send、wait、真实回复截图和
ComputerUse Stop。receipt、observation、screenshot、dispatch state 和错误语义来自同一个
turn；unknown 或已 dispatch 输入不会自动 replay。

这不是所有平台和发行条件都完成的稳定正式版。第二块物理显示器、Developer ID 签名/
公证/正式分发和 Windows/Linux 原生 GUI 完整验收仍未宣称完成；permission/helper/bridge
fault 的 deterministic 和当前 macOS live recovery gates 已通过。面向用户的安装、权限、
使用和验收边界见 [Computer Use 使用说明](../docs/usage/computer-use.md)，实现台账见
[功能实施与验收台账](../docs/plans/2026-09-27-computer-use-product-functions.md)。

## 原生窗口控制

Niuma Desktop 的 host 绑定提供以下 Wails bridge 方法：

```javascript
await window.go.main.app.Maximize();
await window.go.main.app.Unmaximize();
await window.go.main.app.ToggleMaximize();
await window.go.main.app.Fullscreen();
await window.go.main.app.Unfullscreen();
await window.go.main.app.ToggleFullscreen();
const state = await window.go.main.app.GetWindowState();
```

窗口状态变化会通过 `window.runtime.EventsOn` 广播：

```javascript
window.runtime.EventsOn("niuma:window-state-changed", (state) => {
  console.log(state);
});
```

普通窗口的位置和尺寸、最大化状态、全屏状态会保存到 `config-v2.json`。
最大化或全屏退出时会恢复最后一次普通窗口几何；无效或过小的历史几何会回退到
`1440x900` 的默认窗口和 `1024x700` 的最小尺寸。Wails v2.10.2 在 macOS、
Windows 和 Linux 使用同一组 runtime API，不需要额外依赖。

## 验证与限制

```bash
go test ./Niuma Desktop -count=1
npm --prefix web run typecheck
npm --prefix web test
cd web
npx playwright test --config playwright.desktop.config.ts
```

浏览器回归使用确定性 API fixture，不能替代原生进程、真实模型和落库验收。
当前入口修复证据见 [首次入口验收](../docs/manual_testing/desktop_v2_entry.md)，
已有模型回复与持久化记录见 [PromptPicker 验收](../docs/manual_testing/prompt_picker_ui.md)。

自动更新、系统凭据库仍未接入。Windows 安装/卸载和跨平台发布验收由
GitHub Actions 的 Release workflow 执行；本机可运行不能替代发布就绪结论。

## 正式发布

正式桌面发布由 GitHub Actions 的 `Release` workflow 统一完成。推送
`v*` tag 后会构建 macOS DMG、Windows 安装程序、Linux 桌面归档和 CLI
归档，最后生成 `SHA256SUMS` 并创建 GitHub Release。macOS tag 发布时，可以
配置 Developer ID 签名和 notarization Secrets，也可以让六个 Apple Secret
全部为空，以未签名/未公证模式发布。未签名模式下，用户首次打开需要在
“系统设置 → 隐私与安全性 → 安全性”点击“仍要打开”。具体 Secret 名称、
发布命令和验收步骤见 [Release Pipeline](../docs/deployment/release_pipeline.md)。
