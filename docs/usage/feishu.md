# Feishu Bot 接入

本文说明如何在本机或单机部署中运行 niuma 的 Feishu Bot channel worker。它只描述当前仓库已经提供的 CLI 和脚本能力；多实例生产部署还需要自行配置进程托管、MySQL 高可用、Redis lease 和监控。

## 工作流

接入分为四步：

1. 创建并配置飞书应用。
2. 保存应用凭据，并准备 MySQL、tenant/user/account 身份。
3. 配置模型 provider 和 workspace。
4. 使用独立二进制和 `screen` 启动 worker，发送测试消息并查看日志。

## 飞书应用

可以使用仓库提供的向导创建应用：

```bash
go run ./cmd/niuma channels onboard feishu
```

命令会引导浏览器授权，并把凭据保存到本机受保护的凭据文件。应用创建后，仍需在飞书开发者后台完成应用配置、事件订阅和发布。不要把 App Secret、Verification Token 或 Encrypt Key 写入仓库。

已有应用需要补充权限时，可以使用：

```bash
go run ./cmd/niuma channels authorize feishu
```

具体权限以当前飞书开发者后台和命令输出为准。至少要确认 Bot 能接收消息、发送消息；使用 reaction、资源或交互卡片时，还要开通对应权限。

## 必需配置

`channels run` 会校验 MySQL、tenant/user/account 身份、凭据文件和 payload key。先准备全局 settings，例如：

```bash
export NIUMA_CHANNEL_SETTINGS_FILE="$HOME/.niuma/settings.json"
export NIUMA_CHANNEL_MODEL_PROVIDER="your-provider"
export NIUMA_CHANNEL_MODEL="your-model"
```

然后设置 worker 的运行身份。下面的值是示例占位符，不要直接用于生产：

```bash
export NIUMA_MYSQL_DSN='user:password@tcp(127.0.0.1:3306)/golang_cc?parseTime=true'
export NIUMA_FEISHU_CREDENTIAL_FILE="$HOME/Library/Application Support/niuma/feishu-credentials.json"
export NIUMA_CHANNEL_TENANT_ID="1"
export NIUMA_CHANNEL_ACCOUNT_ID="1"
export NIUMA_CHANNEL_ACCOUNT_KEY="feishu-main"
export NIUMA_CHANNEL_USER_ID="1"
export NIUMA_CHANNEL_PAYLOAD_KEY="<32-byte-hex-key>"
export NIUMA_CHANNEL_WORKSPACE="$PWD"
```

`NIUMA_CHANNEL_PAYLOAD_KEY` 必须在 worker 重启和升级时保持不变，否则历史 Inbox/Interaction 无法解密。生产环境应从 secret store 或受保护的环境文件注入。

如果使用群聊，默认要求消息中 @Bot；如果使用多个 Bot，必须为每个账号配置不同的 worker name，并确保 `(tenant_id, account_id)` 不重复运行。

## 启动 worker

推荐使用常驻脚本：

```bash
export NIUMA_CHANNEL_WORKER_NAME="feishu-main"

scripts/channel-worker-screen.sh start
scripts/channel-worker-screen.sh status
```

脚本会构建独立二进制，创建独立的 `screen` session、日志和权限为 `0600` 的环境文件，并在启动前执行 provider preflight。默认状态目录是：

```text
~/.niuma/channel-workers/
```

停止或重启：

```bash
NIUMA_CHANNEL_WORKER_NAME="feishu-main" scripts/channel-worker-screen.sh restart
NIUMA_CHANNEL_WORKER_NAME="feishu-main" scripts/channel-worker-screen.sh stop
```

不要用下面的方式作为常驻服务：

```bash
go run ./cmd/niuma channels run &
```

电脑重启后，使用同一个 worker name 再执行 `start` 即可从持久化环境文件恢复配置。

## 可选能力

常用开关包括：

```bash
export NIUMA_CHANNEL_STREAMING="on"
export NIUMA_CHANNEL_STREAMING_ACCOUNT_MODE="enabled"
export NIUMA_CHANNEL_REACTIONS="on"
export NIUMA_CHANNEL_QUESTIONS="on"
export NIUMA_CHANNEL_TOOL_DETAILS="summary"
```

Redis lease 可用于多进程部署：

```bash
export NIUMA_CHANNEL_REDIS_ADDR="redis://127.0.0.1:6379/0"
export NIUMA_CHANNEL_REDIS_PREFIX="niuma:channel"
```

没有配置 Redis 时，worker 使用单实例本地 lease；这不适合作为多副本协调方案。

## 检查和排查

```bash
NIUMA_CHANNEL_WORKER_NAME="feishu-main" scripts/channel-worker-screen.sh status
tail -f "$HOME/.niuma/channel-workers/feishu-main.log"
screen -r niuma-channel-feishu-main
```

常见问题：

- `provider not found`：检查 settings 中的 provider 名称和 `NIUMA_CHANNEL_MODEL_PROVIDER`。
- `tenant identity mismatch`：确认 tenant 已存在，且 `NIUMA_CHANNEL_TENANT_ID` 正确。
- `channel payload key` 错误：确认使用的是原 worker 的同一把 32 字节十六进制 key。
- 群聊没有回复：确认 Bot 已加入群，并在消息中 @Bot。
- screen 存在但状态为 `degraded`：检查对应日志、二进制和 MySQL/凭据文件路径。

## 相关文档

- [`docs/architecture/channel_worker_screen_operations.md`](../architecture/channel_worker_screen_operations.md)：常驻 worker、多 Bot、恢复和日志运维。
- [`docs/usage/profile-agent-provisioning.md`](profile-agent-provisioning.md)：通过 WebUI 配置 Profile 和 Feishu worker。
- [`docs/e2e/feishu_askuserquestion_acceptance.md`](../e2e/feishu_askuserquestion_acceptance.md)：Feishu AskUserQuestion 真实链路验收记录。
