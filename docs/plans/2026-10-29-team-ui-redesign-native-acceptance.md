# Team 工作台 UI 重设计：macOS 原生验收

> 后续深色下拉控件修复和最终 `64f2991` 构建验收见 `docs/plans/2026-10-30-team-ui-review-native-acceptance.md`；本页截图只对应旧源码 `633c0ae`。

日期：2026-10-29。范围：桌面 V2 Team 工作台的 UI/UX 改造，不改变编排器、模型设置、Computer Use、单 Agent 或 screen profile。

## 构建身份和隔离

- 最终 UI 源码提交：`633c0aed566367487ec1746e4987e5e7445cbd35`；`COMPUTER_USE_RELEASE_CANDIDATE=1 GOTOOLCHAIN=local bash scripts/build-desktop-v2.sh`，使用已缓存的 Go 1.26 工具链。`computer-use-build.json` 显示 source/web 均 clean、release candidate、签名验证通过。
- 以真实 macOS Wails 窗口执行点击和原生截图；不是浏览器 fixture。使用独立的 HOME、桌面配置和 SQLite，只连接项目自带的 127.0.0.1 离线 OpenAI-compatible fixture（dummy key）；无真实付费模型请求或真实密钥。结束后停止测试进程并清理隔离目录和浏览器 fixture 图片。
- 原生 JPEG 由 `sips` 转为 PNG，SHA-256、尺寸和源码提交记录在 `desktop-v2/build/validation/20261029/team-ui-redesign/evidence-manifest.json`。该目录为本地忽略的构建验收证据，不加入 Git。

## 最终构建的真实桌面操作

| 操作及验收点 | 观察结果 | 截图 |
|---|---|---|
| 1024×700 打开 Team 工作台 | 模板、工作流和主要操作在首屏形成明确层级，无横向溢出 | `01-final-workbench.png` |
| 1024×700 查看 Agent 列表及编辑器 | 双栏列表、角色/状态/模型/工具权限可读 | `02-final-agent-directory.png` |
| 点击创建、保存、发布 Agent | 新建“最终版规划员”，草稿提示和已发布状态正确 | `03-final-agent-published.png` |
| 点击新建双 Agent 工作流、设置顺序依赖、保存 | Planner → Reviewer 依赖摘要为步骤 1；保存不自动运行 | `04-final-workflow-editor.png` |
| 输入目标、点击启动离线 Run | 2/2 完成，Reviewer 接收上一步 1 个产物；进度、结果和用量可见 | `05-final-completed-run.png` |
| 慢请求执行中点击取消 | 请求仍活动时有取消控件、未知用量未伪装为免费 | `06-final-running-before-cancel.png` |
| 取消后读回终态 | Run 与下游步骤均为已取消，原因使用警告色而非红色错误；无产物 | `07-final-cancelled.png` |
| 设置页切换深色，再回 Team 列表 | 深色 token 下文字、边框、CTA 与列表层级可读 | `08-final-dark-workbench.png` |

首三张为 1024×700；后五张为 1230×768（系统窗口在编辑过程中恢复较大尺寸）。浏览器 Playwright 分别检查 360、768、1024、1440 宽度的英文/中文与浅色/深色，无横向溢出；这些浏览器检查不冒充原生验收。

## 自动化和可见风险

- `cd web && npm run typecheck`：通过。
- `cd web && npm test -- --run src/v2/workflows/WorkflowWorkbench.test.tsx src/v2/workflows/WorkflowShell.test.tsx`：18/18 通过；包含创建/发布、依赖、保存/运行分离、幂等、恢复/取消状态、未知用量与取消非错误提示。
- `cd web && npm run test:e2e -- e2e/workflows-v2.spec.ts --project=chromium-webui-v2-desktop --project=chromium-webui-v2-narrow --project=chromium-webui-v2-mobile`：15/15 通过，真实 HTTP adapter 搭配离线浏览器 fixture；未发送付费请求。
- 最终 `.app` 构建、ad-hoc codesign 验证通过。原生读取过已持久化的失败 Run，AX 显示“失败 / 下游已阻塞 / 无产物”；但截图拍摄时 UI 仍停留在切换前的编辑器，已删除这张误导性的图片。随后 Mac 锁屏且自动解锁失败，**最终构建的失败页原生截图未完成**，不能用浏览器截图顶替。需用户解锁后补拍。
- 1024×700 的 Agent 目录标题区两个小按钮仍会换行；交互可用但不够整齐。优先在下一轮将目录操作固定为同一行或独立工具栏，并在解锁后重建、原生复验。其余延期：真实付费模型压测、任意条件分支/循环、暂停/审批、分布式 Worker、签名公证发布。
