# v0.1.58-go 统一命名发布归档

## 归档信息

| 项目 | 值 |
| --- | --- |
| 发布内容 | 内部产品身份统一为 `golang-cc` |
| 发布日期 | 2026-07-30 |
| 归档日期 | 2026-07-31 |
| 主仓库提交 | `23480b3e` (`refactor: unify product identity as golang-cc`) |
| 主仓库 tag | `v0.1.58-go` |
| APG 配套提交 | `0b6cb38` (`refactor: align golang-cc agent identity`) |
| 合并方式 | 两个仓库均从已验证的 `dev-c/` 分支 `--ff-only` 合并到 `main` |
| 发布状态 | 两个 `main` 和主仓库 tag 均已推送；本地验收服务已停止 |

本文件记录本次发布已经发生的事实，作为后续审计和复盘入口。长期命名契约、迁移方式和回滚边界以
[统一命名与迁移说明](../product_rename_golang_cc.md)为准。

## 背景与决策

项目此前同时出现 `golang-claude-code`、`go-claude` 和 `golang-cc` 三组内部身份，影响 CLI、配置、
Redis、MySQL、transcript、WebUI、脚本和 APG。项目尚未正式上线，没有生产 Redis/MySQL 历史数据，
因此本次选择直接统一内部 canonical identity，避免继续积累双写和多套命名成本。

最终选择 `golang-cc`，原因如下：

- 与现有 Go module `github.com/konglong87/go-e2e` 一致。
- 名称短，适合作为 CLI、二进制、目录、环境变量前缀和内部 namespace。
- 不把项目限定为 Claude Code 复刻；项目目标是通用 Agent runtime。
- 新电脑从 canonical 仓库安装时只接触 `golang-cc`，旧名称仅承担迁移兼容职责。

## 架构原则

### 单一 canonical identity

`internal/product` 集中定义产品名、二进制名、配置目录、环境变量前缀、Redis namespace 和 transcript
schema。调用方优先复用常量和统一环境变量解析，不再各自维护产品字面量。

### 新写旧读

新产生的数据和状态只使用 `golang-cc`：

- CLI 和发布产物：`golang-cc`
- 全局及项目状态：`~/.golang-cc`、`.golang-cc`
- 环境变量：`GOLANG_CC_*`
- Redis：`golang-cc:tenant_quota`、`golang-cc:mobile_usage`
- MySQL 默认库：`golang_cc`，测试库使用 `golang_cc_*`
- transcript：`golang-cc.transcript.v2`
- WebUI localStorage：`golang-cc-webui.*`
- APG adapter / agent ID：`golang-cc`、`golang-cc-local`

旧配置目录、旧环境变量、旧 transcript schema、旧 WebUI localStorage key 和 APG adapter alias 保留读取
兼容。canonical 配置与旧配置同时存在时，canonical 配置优先。Redis/MySQL 测试数据不迁移。

显式设置 `GOLANG_CC_CONFIG_DIR` 时视为隔离运行，不隐式读取宿主机旧配置，避免测试、容器和多实例部署
发生配置串扰。

### 外部协议边界

以下名称表达外部对象，不属于本项目内部品牌，保持不变：

- Claude Code 兼容协议、配置字段、tool 名和导入格式
- `.claude`、`CLAUDE.md` 与 `CLAUDE_CODE_*`
- Anthropic/provider/model 等第三方协议概念

### 可观测兼容

Prometheus 同时输出 canonical `golang_cc_*` 和 legacy `golang_claude_code_*` 指标。新集成使用 canonical
指标，legacy 指标用于兼容既有 dashboard 和告警，不代表运行时仍使用旧产品身份。

## 实际修改范围

主仓库提交涉及 `352 files changed, 3629 insertions(+), 2438 deletions(-)`，主要覆盖：

- CLI 入口从 `cmd/golang-claude-code` 调整为 `cmd/golang-cc`。
- CI、release、build、install、Web Agent 和验收脚本统一二进制与环境变量名称。
- 配置、identity、session、goal、memory、skills、plugins 和 updater 切换 canonical 状态目录。
- Redis、MySQL 默认测试库、transcript schema 和 metadata app 切换 canonical identity。
- WebUI package、页面展示、localStorage 和浏览器测试统一命名并支持一次性旧 key 迁移。
- Swagger 入口、README、部署文档、使用文档和现行设计文档同步更新。
- 新增 `internal/product` 作为内部产品身份的单一事实源。

APG 配套提交涉及 `34 files changed, 542 insertions(+), 446 deletions(-)`，主要覆盖：

- canonical adapter `golang-cc` 和 agent ID `golang-cc-local`。
- canonical model/root 环境变量 `APG_GOLANG_CC_MODEL`、`GOLANG_CC_ROOT`。
- suite、fixture、检查脚本和报告中的当前 ID 统一。
- isolated HOME 优先读取 `~/.golang-cc/settings.json`，旧目录作为 fallback。
- 保留 `go-claude` adapter alias 和旧环境变量 fallback，避免已有调用立即失效。

## 验证证据

### 确定性与静态门禁

| 验证 | 结果 |
| --- | --- |
| 主仓库 `go test ./... -count=1` | 通过 |
| APG `go test ./... -count=1` | 通过 |
| 主仓库 `go vet ./...` | 通过 |
| `scripts/offline-acceptance.sh` | 194 checks passed |
| `scripts/build.sh` | 成功生成 `bin/golang-cc` |
| 两个仓库 `git diff --check` | 通过 |
| 异常大删除审计 | 无命中 |
| 禁止旧内部标识残留审计 | 无命中；仅保留明确兼容项 |

### Web 与浏览器

| 验证 | 结果 |
| --- | --- |
| Vitest | 15 files / 141 tests passed |
| TypeScript typecheck | 通过 |
| Web production build | 通过 |
| Biome lint | 退出成功；保留 125 条既有 React hooks warning |
| Playwright mock/smoke | 9 passed / 1 live skipped |
| Playwright live API | 1 passed，真实调用本地 mobile chat API |
| 手机真机 | 用户确认可用，随后授权合并 `main` |

Vite 仍报告单个压缩后 chunk 超过 500 kB。该告警和 125 条 hooks warning 不由本次改名引入，本次未扩大
范围处理；后续应作为独立 Web 工程治理任务评估。

### 真实依赖与模型

| 验证 | 结果 |
| --- | --- |
| APG real-model smoke | `deepseek-v4-flash`，`golang-cc-local` 1/1 passed |
| 真实 server `/query` | 返回预期 marker，model 为 `deepseek-v4-flash` |
| 真实 MySQL E2E | migration、tenant/service、server、mobile SSE、goal、knowledge、pool 通过 |
| 真实 Redis E2E | quota/mobile usage 通过，确认 canonical key 前缀，测试 key 已清理 |
| transcript | 写入 `~/.golang-cc/projects`，schema/app 均为 `golang-cc` |
| telemetry | canonical 指标正常输出，legacy 指标继续兼容输出 |

关键命令如下，凭据仍由本机安全配置提供，未写入仓库：

```bash
GOLANG_CC_MYSQL_E2E_DATABASE=golang_cc_identity_e2e \
  scripts/tenant-mysql-e2e.sh

GOLANG_CC_REDIS_E2E_ADDR=127.0.0.1:6379 \
  go test ./internal/quota ./internal/server -run 'TestRedisE2E' -count=1 -v

# 在 agent-proving-ground 仓库执行
APG_GOLANG_CC_MODEL=deepseek-v4-flash \
  go run ./cmd/apg run \
  --suite suites/local-golang-cc-smoke.yaml \
  --agent golang-cc-local \
  --output /tmp/golang-cc-real-smoke.json
```

## 发布闭环

1. 主仓库分支 `dev-c/memory-lint-plan` 推送到远端。
2. APG 分支 `dev-c/golang-cc-identity` 推送到远端。
3. 两个分支均在测试通过且用户确认真机可用后，以 `--ff-only` 合并到各自 `main`。
4. 主仓库创建既有格式的轻量 tag `v0.1.58-go`。
5. 主仓库 `main + tag` 原子推送；APG `main` 推送。
6. 远端 ref 核验：主仓库 `main` 和 tag 指向 `23480b3e`，APG `main` 指向 `0b6cb38`。
7. 本地 18087 验收服务收到 `SIGTERM` 后退出，端口确认释放。

## 过程复盘

批量替换期间，一次不安全的批处理曾临时截断以下 5 个文件：

- `AGENTS.md`
- `internal/session/store.go`
- `internal/session/media_test.go`
- `scripts/agent-capability-full-release-acceptance.sh`
- `scripts/tui-tool-progress-acceptance.sh`

这些文件已从 Git 完整恢复，并重放必要修改；全量测试、diff 检查和异常删除审计随后全部通过。该事件没有进入
最终提交的损坏状态，但说明“全局替换”只能用于发现候选和机械变更，不能跳过结构化边界与 diff 审计。

后续同类任务至少保留以下门禁：

```bash
git diff --check
git diff --numstat | awk '$2 > 20 && $2 > ($1 * 2 + 10) {print}'
go test ./... -count=1
```

对 Redis、MySQL、schema、环境变量和外部协议分别维护 allowlist，不将兼容标识误删，也不把外部 Claude
Code 概念误判为本项目品牌残留。

## 已知边界与回滚

- `.go-claude`、旧环境变量、旧 transcript schema、旧 WebUI key、APG adapter alias 和 legacy metrics
  暂不删除；它们只承担兼容职责。
- 当前本地 checkout 目录仍可能叫 `golang-claude-code`，目录名不参与运行时 identity；新 clone 默认使用
  canonical 仓库名 `golang-cc`。
- 项目未上线，因此没有迁移旧 Redis/MySQL 测试数据，也没有生产数据迁移验证。
- 回滚到 `v0.1.57-go` 不会让旧版本读取 `~/.golang-cc` 中的新状态。回滚前必须保留旧目录和旧部署配置，
  代码回滚不会自动执行反向数据复制。

## 相关入口

- [统一命名与迁移说明](../product_rename_golang_cc.md)
- [Changelog](../../CHANGELOG.md)
- [文档中心](../README.md)
- [APG 仓库](https://github.com/konglong87/agent-proving-ground)
