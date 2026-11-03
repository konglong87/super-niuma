# golang-cc 统一命名与迁移说明

> `v0.1.58-go` 的实际修改范围、测试证据、提交/tag 和过程复盘见
> [发布归档](archive/2026-07-30_golang_cc_rename_release.md)。

## 结论

项目名、Go module、CLI 命令、发布产物、运行时展示名、新建状态目录和内部数据标识统一使用 `golang-cc`。升级不删除旧配置；运行时优先使用新名称，并在必要位置只读兼容旧配置。

项目尚未上线，因此 Redis、MySQL 默认库名、transcript schema、内部验收报告 schema 和 APG 中代表本项目的 ID 直接切换到 canonical 名称。配置目录、环境变量和 Prometheus 指标保留迁移兼容；Claude Code 兼容字段表达外部对象，不参与替换。

## 命名契约

| 范围 | 新名称 | 旧名称兼容策略 |
| --- | --- | --- |
| 项目、运行时、CLI | `golang-cc` | 不再生成旧名二进制；升级后重新安装并改用新命令 |
| Go module | `github.com/konglong87/go-e2e` | 不提供旧 module path alias |
| CLI 源码入口 | `cmd/golang-cc/main.go` | 旧目录移除 |
| 全局状态目录 | `~/.golang-cc` | 只读兼容 `~/.go-claude`；新写入落到新目录 |
| 项目配置目录 | `.golang-cc` | 读取 `.go-claude`，新配置优先；写入使用 `.golang-cc` |
| 项目指导文件 | `go-e2e.md` | 只生成和自动加载 `go-e2e.md`；外部项目可继续使用 `CLAUDE.md` fallback |
| 环境变量 | `GOLANG_CC_*` | `GOLANG_CLAUDE_CODE_*` fallback；两者同时存在时新变量优先 |
| WebUI localStorage | `golang-cc-webui.*` | 首次读取旧 `go-claude-webui.*` 后复制到新 key |
| npm package | `golang-cc-*` | 不保留旧 package 名 |
| Redis namespace | `golang-cc:tenant_quota`、`golang-cc:mobile_usage` | 不读取旧计数 |
| MySQL 默认库名 | `golang_cc`、测试库使用 `golang_cc_*` | 不迁移旧测试库 |
| transcript schema | `golang-cc.transcript.v2` | 新写入只用新 schema；旧 schema 仅只读兼容 |
| APG adapter / agent ID | `golang-cc`、`golang-cc-local` | APG 解析层暂留旧 adapter alias，不再由 suite 生成 |

显式设置 `GOLANG_CC_CONFIG_DIR` 时视为隔离运行，不再隐式合并用户目录中的旧配置。这可以保证测试、容器和多实例部署不会意外读取宿主机状态。

## 兼容例外

以下标识暂不删除，不能作为品牌残留机械删除：

- Prometheus 旧指标名在迁移期继续输出，避免现有告警和 dashboard 断流；新集成应使用文档标注的 canonical 指标。
- 旧 transcript schema `go-claude.transcript.v1/v2` 只用于读取旧文件；新 recorder 不再写旧 schema。
- `.go-claude`、旧环境变量和旧 WebUI localStorage key 继续作为迁移来源，canonical 写入只使用新名称。
- Claude Code 兼容配置、环境变量、tool 名和 transcript 字段保持原协议拼写，它们表达兼容对象，不是本项目品牌。
- 历史 changelog、审计报告和测试证据可以保留原文；运行脚本和现行文档不得继续生成旧内部 ID。

## 升级步骤

```bash
git pull
scripts/install.sh
golang-cc --version
```

部署配置应逐步把 `GOLANG_CLAUDE_CODE_*` 改为 `GOLANG_CC_*`。兼容期内旧变量仍可用，但不要同时维护两套值；如果新旧变量同时存在，以 `GOLANG_CC_*` 为准。

首次升级后检查：

```bash
golang-cc doctor
golang-cc session list
golang-cc goal status
```

确认新配置和状态可用后，也不要立即删除 `~/.go-claude`。旧目录是兼容读取源，回退旧版本时仍可能需要。

## 回滚边界

本次改名采用“新写、旧读”的单向迁移。回滚到旧二进制时，旧版本不会读取新目录中的新增状态，因此回滚前必须保留旧目录和原部署配置。代码回滚不会自动反向复制 `~/.golang-cc` 数据。
