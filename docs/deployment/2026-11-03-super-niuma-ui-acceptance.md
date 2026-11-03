# Super Niuma：UI 修复与控制面验收

## 结论

**本轮 UI 修复、美化与原生操作验收通过；平台整体尚未通过真实模型＋外部渠道的统一端到端验收。**

构建源码：`526c14c59f8903da32ab65d0782ca2bdc64e1465`。UI 切片 `9cd8b41`、计量诊断切片 `fb22e1b` 与控制面切片 `526c14c` 已分别提交并推送。正式签名/分发未在本轮完成。

## UI 修复

- 将授权确认封装为独立组件：固定 checkbox 尺寸，文本使用独立 span 和可收缩 grid，隔离全局 input/label 样式污染。
- 修复文字挤成竖列、横向溢出和异常留白；统一卡片边距、状态条与表单层次。
- 原生 WebKit select 统一为 40px 控件，保留原生菜单、键盘和无障碍语义。
- 高级 JSON 默认折叠；未配置状态显示设置引导，不再误报后端异常。
- 正常取消显示“任务已取消，不会继续执行”，保留真正异常的警告；增加 Scope/Owner 来源标签。
- 入口仍在同一个 Desktop 左侧“新建会话”旁，中文名为“超级牛马干活”；旧 Session 源码边界检查通过。

## 真实原生操作

使用重新构建的 Wails `.app` 私有副本、独立 bundle ID 与显式配置/状态根进行操作，不操作日常 App。实际完成：入口点击、登记工单、保存受限配置、原生菜单选择、确认勾选、创建待审批执行单、未批准直接取消、重启后读取持久状态、返回普通会话入口。

最终两张执行单均为 `cancelled`，所有 job 的 attempts 均为 0，预算预留为 0，没有独立验收报告或交付通知。**这证明 UI/审批边界，不是自主模型交付成功。**

原生截图（不是 DOM 或 mock）：

- [最终控制台：确认框、下拉框、正常取消](../../desktop-v2/build/validation/20261103/platform/final-native-controls.png)
- [原会话与独立 Super 入口](../../desktop-v2/build/validation/20261103/platform/final-native-session-entry.png)
- [取消、零执行次数与事件时间线](../../desktop-v2/build/validation/20261103/platform/final-native-cancelled.png)
- [用户提供的修复前截图](../../desktop-v2/build/validation/20261103/platform/ui-before-user.png)

含私有 workspace 路径的首屏与勾选截图只在本地保留，不发布到 Git；文件名和 SHA-256 记录在 [native-receipt.json](../../desktop-v2/build/validation/20261103/platform/native-receipt.json)。构建身份见 [build-receipt.json](../../desktop-v2/build/validation/20261103/platform/build-receipt.json)。

CUA 的滚轮/坐标操作曾间歇返回无可用窗口；通过原生语义控件与激活 QA 窗口后，菜单键盘选择和状态变化得到实际确认。未把工具失败当成产品通过。

## 自动回归

| 验证 | 结果 |
| --- | --- |
| 最终源码短路径副本 `go test ./...`，无 skip | 132 个测试包通过 |
| `go test -race ./internal/super/...` | 全部通过 |
| Desktop Super focused race；更广 Desktop race | 通过；长路径 socket 用上方短路径全量回归覆盖 |
| `go vet ./internal/super/... ./desktop-v2` | 通过 |
| `npm test` | 96 文件、941 tests 通过 |
| TypeScript typecheck、相关文件 lint | 通过 |
| Super/桥接/导航 focused tests | 81 tests 通过；另一扩展 focused 组 121 tests 通过 |
| legacy boundary / guard | 通过；6 项 guard tests |
| public docs checks | 7 项通过；新报告中的本地路径前缀已脱敏，未放宽规则 |
| intake HTTP smoke | 18 项通过，含权限、幂等、取消和重启 |
| macOS build / ad-hoc codesign verify | 通过；不代表 notarized 正式发行 |

并发交付取消、Failed 根遗留过期 claim、verification expiry 重复 revision、外部 scope 本地审批/人工停止、跨 scope 事件/DLQ 均有真实 SQLite 回归。独立复核在本次审查范围未发现剩余 P0/P1；这不是不存在所有缺陷的保证。

## 真实模型与剩余门禁

原失败记录保留。修复计量诊断后，仅另外执行了一次预登记复测：原配置的 `custom / glm-5.2` 返回 **HTTP 429**，另一并行请求随根任务停止而取消。没有 writer、验收或交付成功；没有再重试。

- 根状态 `needs_attention`；两次预算 admission，确认计量响应 0。
- 保留预算 13,822 tokens 是不确定请求的账本预留，**不是声称实际消费 13,822 tokens**。
- 真实模型 fixture、数据库和日志均清理；仅保留安全诊断、计量和结果收据。
- 详见 [独立复测收据](2026-11-03-super-niuma-model-recheck.md) 与 [原失败记录](2026-11-03-super-niuma-model-acceptance.md)。

下一优先级按依赖排列：

1. 模型供应商恢复可用/限流解除后，重新授权一次受限验收；不后台自动循环。
2. 配置真实外部渠道账号、白名单与明确授权的收件目标，再做收发验收。
3. 使用同一工单串联原生 UI、自主模型、真实外部传输与验收证据；此前不得宣称“全平台全部验收完成”。

## 清理与边界

已停止并删除本任务拥有的 QA App/helper、私有配置、DB/WAL、原始日志、短路径源码副本和 mock 截图。保留所请求的真实原生证据及正式源码构建输出。未创建分支/worktree，未修改原 niuma 仓库；原仓库仍 clean，HEAD 为 `6b021afb71de9b6b43fa8de0e35e62638df75581`。
