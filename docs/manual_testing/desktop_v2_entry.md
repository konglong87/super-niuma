# go-e2e 首次入口与发布文档验收

日期：2026-09-16

## 架构与计划

- Topology impact: none
- Blast radius: B1_SCENARIO
- Topology reason: 仅在 RT-OUTPUT 的 desktop-v2 路由消费者中接受 Wails 根路径；
  RT-ENTRY 的资源加载、RT-BOUNDARY 的鉴权/readiness 和 RT-PERSIST 的 SQLite
  既有契约不变，不新增节点、因果边、API 或存储。
- 原因：桌面构建直接加载 `/`，App 根据构建标识选择 WebUI v2，但其路由解析器
  将 `/` 判为 invalid。入口壳与路由解析器的预期不一致。
- 方案：路由解析显式接收 desktop-v2 标识，仅在该入口将 `/` 解析为 index；
  不重定向、不自动选择或创建会话，不把真正无效的 session/settings URL 吞掉。
  浏览器 WebUI 与 legacy WebUI 继续使用既有行为。
- 收益：首次进入与返回根路径不再显示错误。潜在影响是导航状态恢复，
  以解析器、组件、浏览器历史和原生安装包覆盖。
- 无新增 gate、持久化、HTTP 请求、模型 token/turn/tool call 或 prompt/cache 成本。
- 文档同步桌面构建入口、依赖、SQLite、模型设置、数据目录、已知限制；
  区分已实现、当前已验收和仍待发布验证的能力。
- 回滚：回退路由与文档提交后重建前端/桌面壳，不迁移或删除用户数据。

## 验证矩阵

1. 单元：桌面 `/`、Web `/`、合法深链接、无效链接、设置页及历史返回。
2. 浏览器：桌面宽屏、最小窗口和移动视口；首次 onboarding、关闭后首页、
   刷新、会话进入和返回、无效链接恢复、配置未完成时转入模型设置。
3. 原生：隔离 HOME/配置与 SQLite，启动实际 macOS Wails 包并查看截图，
   确认首页无错误、设置可进入、退出后 server 回收；重启复核。
4. 回归：Go 全量测试、Web 全量测试/typecheck/build、既有浏览器导航与
   PromptPicker、拓扑检查和 diff check。

## 执行结果

- 两条新增单元回归在修复前失败，修复后通过；相关 25 项、全量 Vitest
  58 文件 / 549 项通过。旧 invalid-link 恢复测试原先断言瞬态
  `Loading sessions`，全量并发运行时可能已完成加载；现在等待最终首页，
  同时保留 URL、错误消失和会话工作区断言。
- 新桌面构建专用 Playwright 配置：3 视口 x 2 场景，6/6 通过。
  已查看桌面和移动截图；根路径首页无错误、无横向溢出。动画 logo 的
  DOM、图片解码与可见性通过，截图可能捕获动画淡出帧。
- 旧版/新版 PromptPicker 浏览器回归 9/9 通过。额外运行的旧布局回归
  有 2 项在查找旧 Settings dialog/Theme select 时超时，另 1 项按平台跳过；
  当前设置已是 SettingsCenter 与主题按钮。本次未更改设置产品行为，
  也不把这次扩展运行写成全量 E2E 通过。
- `GOTOOLCHAIN=local go test ./... -count=1`（Go 1.25.0）、
  typecheck、desktop-v2 生产构建、拓扑检查通过。
  本机 Node 为 25.4.0；不是固定发布工具链的干净构建证明。
- `scripts/build-desktop-v2.sh` 生成实际 macOS arm64 Wails 包，包内同时存在
  桌面壳和 server。通过 CUA 查看实际原生截图，`wails://wails/` 显示
  “一起开始”，无“此会话链接无效”；进入设置和大模型设置成功，
  空白模型字段及配置路径确认未读取开发者 runtime settings。
- 原生测试进程使用空环境、独立 HOME 与空工作目录，固定诊断端口 18193。
  系统工作目录选择器已出现，但 CUA Go-to 输入不稳定，未将目录选择
  交互记为通过；终止该测试进程后在隔离目录预置 `config-v2.json` 重试。
  WebKit UI 偏好不保证随 HOME 隔离，首次 onboarding 由独立浏览器上下文验证。
- 独立 HOME 下实际生成 `go-e2e.sqlite`，readback 的 tenant_sessions
  数量为 0。正常退出后无 18193 监听；再次启动仍落到有效首页，
  再正常退出。未修改默认 SQLite、模型配置或用户会话。
- 本次未调用真实模型、不重新声称模型发送/模板持久化验收；
  它们的上一批证据见 `prompt_picker_ui.md`。Windows 安装执行仍待验证。
- 构建仍有既有 bundle >500 kB 提示。测试进程和临时数据在验收后清理；
  本文与浏览器测试源码保留为可重跑证据，原生截图见本次任务记录。
