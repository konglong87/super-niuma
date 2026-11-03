# Computer Use 使用说明

## 当前状态

Computer Use 当前以 **macOS single-display Developer Preview（单屏开发者预览）** 形式提供。
最新 macOS tagged desktop build 已通过 deterministic/native gates 和一次真实
`gpt-6-sol/high` 闭环验收：启动目标应用、点击、`Command+A`、输入 `1+1=2`、发送、等待真实回复截图，以及显式 Stop。2026 年 10 月 19 日，当前构建还完成了真实截图-backed bridge disconnect acceptance：断开 bridge 后状态 fail-closed，显式 runtime Stop 通过父进程 handle 收敛，helper/runtime 均退出。

Computer Use workspace 现在会展示稳定的 runtime fault code；bridge、worker、helper、permission 和 Stop fault 会明确说明是否可继续操作。fault 状态下会禁用新的 observe/pause/resume，保留安全 Stop/runtime Stop，不会 replay 上一次 action。

这表示当前 Computer Use runtime 可以供已配置 Provider、已完成 macOS 权限授权的开发者使用；它不是已经完成所有平台和发行条件的稳定版。

## 已实现的安全边界

- 每个 Computer Use 调用返回同一个 turn 的 receipt、observation、screenshot 和错误/重试语义。
- action 已 dispatch 后不会自动 replay；`unknown`、`partial` 和 bridge ambiguity 都禁止重放输入。
- 输入动作绑定已授权 session、目标应用和目标窗口；focus、window、permission 和 screenshot guard 不会被放宽。
- 成功输入会返回新的 observation；post-action observation 不可用时，不会把已经执行的 action 改写成 rejected。
- Stop 返回可验证的 control receipt，并终止当前 Computer Use session。
- Wails UI 是 runtime client；Computer Use runtime、helper 和 bridge 使用独立的长生命周期监督路径。
- runtime fault 会通过 `GetComputerRuntimeStatus` 暴露稳定的脱敏状态；UI 不会用 transport 文本推断 action 是否成功。
- runtime fault 时 UI 会阻止新的 observe/pause/resume，保留显式 `Stop runtime`；已 dispatch、unknown 或 bridge ambiguity 的 action 永远不会 replay。

## macOS 构建与启动

从仓库根目录执行：

```bash
npm --prefix web ci --legacy-peer-deps
COMPUTER_USE_RELEASE_CANDIDATE=1 bash scripts/build-desktop-v2.sh -tags computeracceptance
python3 scripts/verify-computer-use-build.py \
  --app desktop-v2/build/bin/niuma.app \
  --workspace . --require-clean --require-release-candidate --require-signature
open desktop-v2/build/bin/niuma.app
```

首次使用需要：

1. 在桌面应用中配置自己的 Provider、endpoint、model 和凭据；
2. 按 macOS 提示授予 Accessibility 和 Screen Recording 权限；
3. 创建会话并显式批准 Computer Use；
4. 使用目标应用的真实窗口完成任务。

项目不附带 Provider 服务、API key、用户密码或其他凭据。不要让模型输入密码、Token 或 OTP。

## 当前支持范围

- macOS Host；
- 单物理显示器；
- 已注册且经过 host 校验的目标应用和窗口；
- click、double-click、right-click、move、scroll、drag、key、hotkey、type、wait、observe、Stop 等受安全边界约束的动作；
- 原子 turn result、PNG observation、action receipt 和本地 validation evidence；
- runtime fault status、fail-closed recovery guidance 和显式 Stop runtime。

## 尚未作为正式发布能力宣称的范围

- 第二块 physical display（second physical display）的实机验收；
- Developer ID signing、notarization、clean install/upgrade 和正式分发；
- Windows/Linux 原生 GUI 的完整真实验收；
- 未注册目标、模糊窗口匹配或无法确认 dispatch 状态的输入。

失败、unknown 或 dispatch 状态不明确时，系统会停止并保留 receipt；不会为了“继续完成任务”而重放原输入。

## 开发者验收

确定性和原生 gate：

```bash
go test ./... -count=1
go test -race ./...
python3 -m unittest scripts/computer-use-120s-acceptance_test.py scripts/computer_acceptance_safety_test.py
python3 -m unittest scripts/public_computer_use_docs_test.py
python3 -B scripts/computer-use-build-verifier_test.py
bash native/macos/tests/run.sh
bash native/macos/tests/platform.sh
bash scripts/build-desktop-v2.sh -tags computeracceptance
codesign --verify --deep --strict desktop-v2/build/bin/niuma.app
```

真实模型闭环应只在 deterministic/native gates 通过后执行，并且必须检查真实 reply screenshot、同一 session/run 的 receipts，以及合法 Stop receipt。测试截图、日志、数据库和本地凭据不提交到 Git。

## 进一步阅读

- [Desktop 构建与配置](../../desktop-v2/README.md)
- [Computer Use 功能实施与验收边界](../plans/2026-09-27-computer-use-product-functions.md)
- [Runtime 重构架构说明](../plans/2026-10-10-computer-use-runtime-rearchitecture.md)
- [公开发布清单](../deployment/open_source_release_checklist.md)
