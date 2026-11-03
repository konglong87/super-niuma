# 2026-09-09 开源就绪检查

> 2026-09-10 范围更新：维护者仅豁免 demo 图片清点与审核，文本脱敏、历史扫描和候选构建继续执行。以下记录保留为历史证据，其中“文档和 Git 历史隐私不处理”的旧决定不再代表本轮范围；最新状态见 [发布候选准备](2026-09-10-release-candidate.md)。

结论：**BLOCKED（暂不建议直接公开或发布二进制）**。首轮审查已完成；这表示发布准备存在待闭环事项，不表示项目不能开源。默认网络暴露已修复，发布包许可证已补齐，WebUI 2.0 可观测验收已通过；依赖告警和 GitHub 设置仍待闭环。文档、截图和 Git 历史隐私按维护者决定保留，不作为本轮阻塞项。

本轮只检查和记录，没有修改运行时代码、轮换凭据、重写历史、创建 tag/Release 或改变仓库可见性。

## 1. 审查基线与方法

- 目标 commit：`bdb9b6f081cc87a4a0a190ab171d3272202649bb`。
- 当前树：1,638 个 Git 跟踪文件。用户未提交文件不属于本轮候选发布树，也未纳入提交。
- 历史：fetch 所有配置远端及 tags 后，可见 103 个 refs、1,867 个提交；非浅克隆。Gitleaks 对有扫描内容的 1,853 个提交报告扫描完成，不能把这个数字当成仓库总提交数。
- Gitleaks v8.24.3：扫描 HEAD 导出树与全部可见 refs，报告全量脱敏；补充独立的凭据前缀与路径扫描，检查 10,204 个历史文本 blob 候选，约 768.5 MB 文本。
- 图片：38 张跟踪的 PNG/JPG 完成本地 OCR 和缩略总览；对明确候选放大核查。OCR 不能替代图片版权确认或逐图最终批准。
- 供应链：Go 模块图、二进制依赖图、许可证文件、npm audit、govulncheck、CI/release 工作流。
- 安全：抽查启动绑定、管理 token、Mobile JWT/dev-auth、租户身份传播、渲染 escape、渠道长连接、图像解析路径；独立复核确认问题。未向服务发送攻击载荷，也未调用外部 API 验证疑似凭据。
- 干净源码：使用已有 main 的独立本地 clone 验证，没有创建新开发分支或 worktree。
- 证据保留在 Git 忽略的 `.gstack/security-reports/2026-09-09-opensource/`，包含脱敏扫描、依赖清单和本地 OCR。原始证据不进入公开文档。

## 2. 发布前事项

### OSS-01：默认快捷重启组合允许局域网客户端冒用开发身份（已修复）

修复：`web-agent-restart.sh` 默认地址已从 `0.0.0.0` 改为 `127.0.0.1`（commit `4dd280a6`）。需要手机或局域网联调时仍可通过 `GOLANG_CC_WEB_AGENT_HOST` 显式指定。

**优先级 P1 / 安全 HIGH / VERIFIED / 置信度 9/10。**

证据：

- [web-agent-restart.sh](../../scripts/web-agent-restart.sh) 第 24 行默认 `HOST=0.0.0.0`，第 45–50 行将其传给 start。
- [web-agent-start.sh](../../scripts/web-agent-start.sh) 第 36 行默认启用 `GOLANG_CC_MOBILE_DEV_AUTH`。
- [mobile_middleware.go](../../internal/server/mobile_middleware.go) 第 25–39 行：无 JWT secret 且 dev-auth 开启时，只要求 tenant/user 头非空，然后继续执行；不检查管理 token。
- [mobile.go](../../internal/server/mobile.go) 第 135 行起直接使用该 Mobile middleware。常规管理 token 的绑定检查没有覆盖这种组合。

具体风险：维护者按文档运行默认 manual restart，未配置 Mobile JWT secret、未关闭 dev-auth，且端口可被其他机器访问时，同网段客户端可使用已知租户/用户标识访问开发身份的数据。manual 配置会复用既有本地数据库。仅换一个强管理 token，无法保护这条 dev-auth 路径。

边界：默认 start 本身绑定回环；正常 JWT 模式检查签名并由 tenant/user 约束查询。普通 `authorize` 的空 token 分支受启动绑定检查保护，不能单独报为全局鉴权绕过。没有验证真实攻击成功。

建议：保留手机调试能力，但让 LAN 调试显式选择，并使用真正认证的身份；默认回环，或禁止非回环与匿名 dev-auth 的组合。需要与现有手机调试规则一同评估，不能只改一个常量后宣称关闭全部风险。

验收：覆盖回环无 token、非回环无 token、非回环有管理 token 但 dev-auth 开启、JWT 正常/过期/错误、错误租户/用户以及手机调试成功路径。影响 `RT-ENTRY → RT-BOUNDARY → RT-PERSIST`，预计 B5；不得破坏正常隔离和会话访问。

### OSS-02：二进制发布包遗漏许可证与第三方声明（已修复）

**优先级 P1 / 发布合规 / VERIFIED / 置信度 9/10。**

[scripts/release.sh](../../scripts/release.sh) 现在会把根 [LICENSE](../../LICENSE) 和 [termenv MIT LICENSE](../../third_party/termenv/LICENSE) 复制到每个平台归档的 `THIRD_PARTY_LICENSES/termenv/`。Release workflow 增加了解包后的非空文件检查。

Go 依赖解析确认二进制包含 vendored termenv。该 MIT 许可证要求保留版权和许可声明。修复后的单平台归档已完成本地检查；本轮没有创建公开 Release，也没有检查已上传历史归档。

验证：`TARGETS=linux/amd64` 归档检查确认 `LICENSE` 与 `THIRD_PARTY_LICENSES/termenv/LICENSE` 均存在且非空；CI release job 也会重复检查。当前二进制模块依赖图包含主模块在内共 115 个模块，后续增加 vendored 或发布 Web 产物时仍需更新第三方声明。

验收：构建单个平台本地归档，列出 LICENSE/第三方版权条目，再检查默认五个平台包内容、SHA256SUMS 和版本来源。无需提前创建公开 Release。

### OSS-03：公开材料与历史隐私尚未完成清理/确认（按维护者决定暂不处理）

**优先级 P1 / 隐私与发布准备 / 部分 VERIFIED，候选需逐项复核。**

- 文本路径规则命中 21 个当前文件、100 行；包含真实验收路径，也包含合成测试路径，不能全部当成真实隐私泄露。
- 38 张图片 OCR 中有 23 张路径/账号等候选；放大检查确认以下图片含个人目录或终端账号/设备标识：
  - `docs/bugs/images/bug-2026-07-02-009-tui-md-table-system-reminder.png`
  - `docs/bugs/images/bug-2026-07-04-001-tui-stale-completed-todos-collapsed.png`
- 历史路径规则命中 1,617 个 blob，涉及 96 个路径；该数字包含历史版本和示例，不能当成 1,617 起独立泄露。
- 历史 author 有 5 组姓名/邮箱组合，其中 4 组不是 GitHub noreply，需要确认可公开范围。
- 最近配置验收计划也记录了真实本机绝对路径，属于本次公开清理范围。
- `.gitignore` 当前未覆盖根 `.env`、`.env.production`、`.golang-cc/settings.local.json` 及本地 `output/`、`memory/`；当前未确认这些文件已被跟踪，但未来误提交风险需要处理。

维护者决定本轮不清理文档、截图和 Git 历史中的个人路径/隐私；相关事实保留为公开前风险知悉项，不再作为本轮待办。

验收：候选逐项标明保留/替换/移出及理由；重新扫描发布树与全部发布 refs；检查 README 链接和截图；确认贡献者身份公开范围。仅修改 HEAD 不能清除旧提交中的内容。

## 3. 依赖扫描：需要处理，但不混淆告警与已验证漏洞

### Go

本机默认 Go 为 1.25.0，第一次扫描报告 40 个具有符号调用链的公告，其中多数是旧标准库告警。随后按 CI 固定的 **Go 1.26.6** 重新扫描，只剩以下 4 个具有扫描器调用链的公告：

| 公告 | 依赖与建议修复版本 | 独立复核 |
| --- | --- | --- |
| GO-2026-6278 | gorilla/websocket 1.5.0 → 至少 1.5.3 | Feishu SDK 实际使用该 WebSocket 客户端；客户端 mask 使用非密码学随机。公告标记 UNREVIEWED，不能外推为认证绕过或解密。扫描器列出的图片读取链不是真实 WebSocket 数据来源。 |
| GO-2026-5061 | x/image 0.28.0 → 至少 0.43.0 | 项目调用 `image.DecodeConfig`；配置读取分支提前返回，不进入公告涉及的完整像素/alpha 解码。未证明可触发。 |
| GO-2026-6222 | x/image 0.28.0 → 至少 0.45.0 | 扫描器跨过 `configOnly=true` 分支连到完整 VP8L 解码；实际路径只读取 header。拒绝作为已验证内存攻击。 |
| GO-2026-4961 | x/image 0.28.0 → 至少 0.42.0 | 同样需考虑配置读取分支，且公告只影响 32 位；默认 release 目标全为 amd64/arm64。 |

这些版本与公告命中属实，建议以独立依赖升级关闭告警，再做 Feishu 长连接及图片格式回归。不要直接修改运行时图片逻辑去“修复”不可达的完整解码分支。后续本地构建也应使用与 CI 一致的 Go，不能用旧本机工具链产物代表发布包。

### Web/npm

- Web lockfile：8 个受影响包（扫描器等级 5 high、3 moderate），均为开发依赖。
- `npm audit --omit=dev`：0 个生产依赖告警；根目录 npm audit：0。
- 涉及 Vitest/mocker、PostCSS、undici、js-yaml、brace-expansion、nanoid 等开发链路；都有可用修复。优先锁文件兼容升级与测试，不运行未经审查的强制升级。
- 6 个 Web 生产依赖条目的许可证为 MIT/ISC，lockfile 未标记生产依赖 install scripts。
- npm severity 不是本项目线上风险等级：需要攻击者可控的测试 mock、构建输入或对应工具服务暴露等前提。

## 4. 安装与治理缺口

### OSS-04：远程版本化 go install 不可用，已有文档说明

[go.mod](../../go.mod) 第 5 行使用本地 `replace`；Go 对 `go install 模块@版本` 明确拒绝包含 replace 的目标模块。[README](../../README.md) 已记录该限制。

这是安装渠道缺口，**不单独阻止源码开源**。可以选择首次开源明确支持 clone 后构建及带许可证的 release 二进制，后续再消除 replace；不能宣称远程 go install 已可用。本轮未向私有模块路径发起远程安装。

### GitHub 管理侧待核验

GitHub CLI 未登录，无法验证实际 visibility、分支保护、必需检查、Private vulnerability reporting 开关和线上 CI 状态。Git SSH fetch 可用不等于具备这些 API 权限。仓库已有 SECURITY.md、CONTRIBUTING.md、CODE_OF_CONDUCT.md、PR 模板和 CODEOWNERS，且 workflows 已有 owner 规则。

CI 与 release 使用的官方 actions 目前按版本 tag 引用，未锁 commit SHA；属于供应链加固项，未发现 `pull_request_target` 检出外部 PR 或由 PR 文本直接拼入 shell 的高危组合。建议固定 SHA 并配合更新机制，不把它报成已发生供应链攻击。

## 5. WebUI 2.0 可观测性状态

v2 已有 Trace 入口和 Run trace ID/用量/耗时展示：`InspectorDetails.tsx`、`conversationViewModel.ts`。已有后端 audit/telemetry/span 可以复用；不是从零建设。

[产品架构文档](../web_agent/go_cc_webui_v2_product_architecture.md) 第 15 节明确：独立 Session Control Prometheus 指标尚未注册。这个增强项可作为开源后的路线图；首次发布前仍需以一次 v2 真实会话验证请求、Run、模型/工具、事件、Trace 与落库的关联。之前的设置读取验收和旧 Web Agent 验收不能替代这一条 v2 全链路验证。本轮未调用外部模型补做该验收。

## 6. 已通过与验证边界

| 检查 | 结果 |
| --- | --- |
| Gitleaks HEAD + 历史 | 各 4 个命中；复核为 3 处文档替换占位符与 1 处合成测试数据。未确认真实凭据。测试值在非测试跟踪文件中为 0 次。 |
| 独立历史凭据前缀扫描 | 20 个匹配，全部属测试或占位符；非测试且非占位符候选为 0。 |
| 干净 clone Go 全仓测试 | Go 1.25.0 与 CI Go 1.26.6 均通过。 |
| go vet | 通过。 |
| 干净 clone 前端 | `npm ci --ignore-scripts --legacy-peer-deps` 后 57 个文件、526 项测试通过，生产构建通过。保留既有 bundle 大小警告；没有执行依赖安装脚本。 |
| 许可证基础文件 | Apache-2.0 根许可证、termenv MIT 许可证及社区文档存在；归档分发声明仍缺。 |
| 源码配置收敛 | 沿用上一轮已验证基线，全局/CONFIG_DIR/显式 settings 不在此轮改动。 |
| GitHub 设置/发布产物 | 未登录，无法核验管理侧；未发布归档，未改变可见性。 |

### WebUI 2.0 可观测最终验收（2026-09-09）

使用隔离 MySQL、确定性 OpenAI-compatible provider、真实 Go server、真实 SSE 和真实浏览器完成一条 WebUI 2.0 会话。Task `100` / Session `35` / trace `webui-v2-final-observability-20260909g` 的 `message=1`、`text_delta=1`、`completed=1` 事件和完成结果一致；MySQL 回查确认 task、事件和 trace 均归属同一租户/用户。Telemetry API 返回 `agent.run.started=1`、`agent.run.finished=1`、`api.request=6`，另含 query/model/output/persistence/gate/context 事件；会话详情返回 `events=6`、`runs=1`、`context=1%`、`total_tokens=104`。主 WebUI Agents 页面显示任务完成、trace、usage 和事件时间线；WebUI 2.0 会话页显示最终回复和 104 tokens。桌面 1440×950 页面无框架错误，Console 仅有 `/tenant/profile` 404（当前隔离租户未配置 Profile，为可解释的可选数据缺失，不影响会话/观测链）。`scripts/web-agent-real-e2e.sh` 的 telemetry 查询窗口已从 50 扩至 500，避免事件较多时误判链路缺失。

“未确认真实凭据”不等于证明所有历史无隐私信息；源码/符号扫描不等于专业渗透测试，也不替代素材权利和维护者的最终发布审核。

## 7. 建议执行顺序

1. 独立升级 Go/Web 的受影响依赖，执行相关模块测试、完整构建和复扫；记录不可达/平台限定告警的判断依据。
2. 确认发布包许可证（本轮已完成）和首发支持的安装方式。
3. WebUI 2.0 可观测链已完成发布前验收；保留 Prometheus 等延期增强项。
4. 核验 GitHub 设置和干净目标 commit 的 CI，维护者完成最终发布审核，再执行开源。

所有修复均应各自保持小范围、相关回归与 readback。本轮报告的 Topology impact 为 none、Blast radius 为 B0_LOCAL：只记录审查结果，未改变现有 runtime 节点或因果边。
