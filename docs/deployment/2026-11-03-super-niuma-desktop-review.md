# Super Niuma Desktop：独立 Review 与真实端到端验收

日期：2026-11-03。审查对象：`4971f40..99542bb`（已提交的 Desktop Super 集成，不是 main 与自身的空 diff）。起始工作区干净，已执行 `git pull --ff-only origin main`。本轮没有产品代码修复，只有审查/验收记录。

## 结论

本轮主审、独立后端复核和原生操作范围内，**未发现可复现的阻断性缺陷**。这不是所有环境、所有并发时序下“绝对无问题”的保证。当前验收对象是**已实现的工单登记闭环**，不能把它等同于尚未实现的自动执行—自动验收—自动交付平台。

## 代码审查覆盖

- Desktop/Wails 桥接：惰性初始化、独立互斥、排队和执行期限、关闭取消与 drain、关闭后拒绝重新打开、错误脱敏和日志内容。
- 独立状态目录、相对路径拒绝、文件权限、事务/幂等及取消事件；复用现有 Service/Repository，不经过旧 Session 执行路径。
- React 客户端：完整能力检测、异步过期响应隔离、并发请求抑制、重复提交 key、失败后重试和表单保留。
- Shell/导航：打开 Super 不创建 Session；隐藏而非销毁原会话；新建/选择/返回会话恢复原路径；Inspector 布局和两种草稿。
- 基线保护：四个经过精确 hash 审核的 composition 例外，没有重写原始 manifest；修改受保护 Session 或已批准文件内容仍会失败。
- 独立后端 reviewer 只读复核同一提交范围，未发现具体可复现缺陷，并执行了 bridge/state/store 的 focused race 测试和 guard 回归。

## 全新构建与自动化结果

| 验证 | 本轮结果 |
| --- | --- |
| `scripts/build-desktop-v2.sh` | PASS，真实 macOS Wails `.app`；构建记录为 `99542bbb35d0998be87283f9ee969de84ab1af11`、`source_dirty=false` |
| `npm --prefix web test` | PASS：92 个测试文件 / 896 项测试 |
| `npm --prefix web run typecheck` | PASS |
| `GOSUMDB=sum.golang.org go test ./...` | PASS：125 个有测试的包 |
| `go test -race ./desktop-v2 ./internal/super/... -count=1` | PASS |
| `go vet ./desktop-v2 ./internal/super/...` | PASS |
| Super boundary checker + guard self-tests | PASS，6 项 guard 自测；受保护代码的模拟变异被拒绝 |
| Public Computer Use documentation tests | PASS，7 项 |

macOS 下原仓库路径会超过既有 Unix socket fixture 的路径限制，因此 Go 全量及 race 测试使用私有 home 目录中的短路径源码副本，未修改任何安全校验。测试副本已清理。

## 真实原生验收结果

通过 CUA 操作真实 Wails 窗口，**不是浏览器 DOM 替代验收，也不是示意截图**。使用隔离的 HOME、Desktop 状态和 Super 状态；所有输入为合成验收数据。普通会话回归使用本地不可达的测试模型配置满足现有配置检查，没有发送模型消息。

| 场景 | 原生观察及结果 |
| --- | --- |
| 普通启动 | 尚未打开 Super 时，没有创建工单数据库 |
| 无模型接单 | 从 onboarding 的“先登记工单（无需模型）”进入 Super，普通会话仍保留配置要求 |
| 不安全目录 | 预置 0755 测试目录被拒绝；未创建数据库，也未偷偷 chmod |
| 错误恢复 | 显式修复测试目录至 0700 后，点击“刷新工单”恢复，无需重启 |
| 非法工作目录 | 相对路径提交被拒绝，界面显示安全错误，已填写内容仍保留，没有多建工单 |
| 正常登记 | 原生输入、点击登记，详情展示目标/验收标准，执行/验收/交付均未开始 |
| 重复提交 | 再次点击，列表仍只有一张工单；数据库仅一个 creation event，日志 `created=false` |
| 登记后重启 | 旧测试 UI 进程已退出，新进程重新读取到同一工单 ID |
| 取消后重启 | 原生取消；再次退出/启动，状态仍为 cancelled，取消按钮禁用 |
| 原会话新建 | 使用原有“新建会话”对话框创建一个普通会话，不自动执行 |
| 会话草稿 | 输入未发送草稿 → Super → 点击原会话，内容逐字保留 |
| Super 草稿 | 输入未提交工单标题 → 原会话 → Super，标题保留，不产生额外工单 |
| Inspector | 已展开 Inspector → Super 隐藏会话 Inspector 且布局完整 → 返回后 Inspector 恢复 |
| 语言和主题 | 真实设置界面切换简体中文，检查浅色主题；本轮亦检查深色工单界面 |

最终合成工单数据库：**1 张工单、状态 cancelled、2 个审计事件（创建+取消）**，文件权限 `0600`。重复提交和非法输入没有制造多余记录。

## 发布环境风险（不是已确认的业务缺陷）

旧版与新项目当前使用相同 bundle identifier `com.wails.niuma`。两个构建同时存在时，原生工具出现同名窗口选择歧义；观察到非验收工作区后立即停止该目标上的操作。后续改用**仅调整测试副本包标识并重新 ad-hoc 签名**的 `Super Niuma Review`，继续验证同一编译代码；没有改产品源码、产品包标识或覆盖原安装。副本的服务和 helper 内容与正式本地构建一致。

发布前 P5 必须明确“原应用原位升级”还是“独立安装”的迁移策略；若要并存，需要唯一身份及数据迁移方案。这一决策不在本轮偷偷修改。正式签名、notarization、分发和第二显示器验收仍是既有延期项。

## 原生证据

新的截图全部位于 `desktop-v2/build/validation/20261103/review/`（忽略的本地证据，不上传 Git）：

1. `01-permission-rejected.png` — 目录权限拒绝。
2. `02-invalid-path-rejected.png` — 非法路径拒绝并保留表单。
3. `03-created-once.png` — 创建及重复提交后仍为一张工单。
4. `04-recorded-after-restart.png` — 重启后登记工单存在。
5. `05-cancelled-light-theme.png` — 原生取消及浅色主题。
6. `06-cancelled-after-restart.png` — 取消状态跨进程重启持久化。
7. `07-session-draft-retained.png` — 普通会话与未发送草稿保留。
8. `08-super-draft-and-layout.png` — Super 草稿保留，原会话仍在侧栏，Inspector 不侵入工单布局。

`review-receipt.json` 保存验证清单、构建记录和上述 PNG 的 SHA-256；不保存真实凭据或完整含 token 的 AX 树。

## 清理与覆盖边界

- 关闭测试 UI；停止 8 个仅属于验收的 runtime/helper 进程。没有停止用户日常窗口。
- 删除私有测试数据库、合成模型配置、原始日志、临时 `.app` 副本与短路径源码副本；保留产品构建、8 张截图和脱敏回执。
- 本轮没有修改 Session/运行时产品代码，没有新分支/worktree；原 niuma 源码保持不变。
- 没有验证真实付费模型推理、未实现的自动 Worker/验收/交付、Windows/Linux 原生 UI。
- 实际 SQLite 外部锁竞争期间的关闭时序、文件系统竞态和所有异常断电情形未穷尽；不能将普通退出/重启测试宣称为全面崩溃恢复证明。
