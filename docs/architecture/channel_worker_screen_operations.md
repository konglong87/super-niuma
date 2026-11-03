# Feishu Channel Worker 常驻运维约定

## 固定启动方式

Feishu/channel worker 是长连接进程。禁止使用下面这种方式作为常驻服务：

```bash
go run ./cmd/golang-cc channels run &
```

`go run` 会产生中间编译进程和子进程，终端、shell、会话结束后容易导致 worker 被回收，表现为 WebSocket 曾经连接成功但随后没有收到群消息。

统一使用：

```bash
scripts/channel-worker-screen.sh start
scripts/channel-worker-screen.sh status
scripts/channel-worker-screen.sh restart
scripts/channel-worker-screen.sh stop
```

脚本行为：

1. `go build` 生成独立二进制。
2. 每个 `GOLANG_CC_CHANNEL_WORKER_NAME` 使用独立 `screen` session。
3. 每个 worker 使用独立日志文件和 0600 env 文件。
4. `screen` 命令行不包含 app secret 或 access token。
5. `restart` 只停止同名 worker，不影响其他 bot。
6. runner 将 `--settings` 和可选的 `--provider` 显式传给二进制；二进制在建立 Feishu WebSocket 前执行 provider preflight，provider 不存在时直接退出。
7. `restart` 会按 `(tenant_id, account_id)` 清理同账号 orphan worker，并通过 account lock 防止不同 worker name 并行启动；同一 Feishu account 只允许一个 `channels run` 进程。
8. `start/restart` 等待 screen 和对应二进制子进程都出现后才返回；`status` 同时检查两者，只有 screen 没有 worker 时报告 degraded。
9. 默认状态目录为 `~/.golang-cc/channel-workers/`。env、runner、独立二进制、日志和账号锁均持久保存，不依赖 `/tmp`。
10. `stop` 只停止进程、删除可重建的 runner 并释放账号锁，不删除 env。电脑重启或进程退出后，使用同一 worker name 执行 `start` 即可从 env 恢复。
11. 如果持久目录中尚无同名 env，但旧 `/tmp/golang-cc-channel-workers/<worker>.env` 存在，脚本会以 `0600` 权限原子迁移，并在持久文件落盘后删除旧 env。持久 env 已存在时不会被旧文件覆盖。
12. 重启后遗留的账号锁只有在对应 screen、真实账号 worker 或仍在执行的启动脚本存在时才有效；被无关进程复用的旧 PID 不会阻止恢复。

## 多 bot 示例

```bash
export GOLANG_CC_MYSQL_DSN='root@tcp(127.0.0.1:3306)/golang_cc_channel_e2e?multiStatements=true&parseTime=true&loc=UTC&charset=utf8mb4'
export GOLANG_CC_CHANNEL_TENANT_ID=1
export GOLANG_CC_CHANNEL_USER_ID=1
export GOLANG_CC_CHANNEL_WORKSPACE="$PWD"
export GOLANG_CC_CHANNEL_MODEL_PROVIDER=glm-5.1
export GOLANG_CC_CHANNEL_MODEL=glm-5.1
export GOLANG_CC_CHANNEL_STREAMING=on
export GOLANG_CC_CHANNEL_STREAMING_ACCOUNT_MODE=enabled
export GOLANG_CC_CHANNEL_REACTIONS=on
# 必须复用已经部署的稳定 key；重启时不要重新生成，否则历史 Inbox/Interaction 无法解密。
export GOLANG_CC_CHANNEL_PAYLOAD_KEY='existing-32-byte-hex-key-from-secret-store'

GOLANG_CC_CHANNEL_WORKER_NAME=feishu-e2e \
GOLANG_CC_CHANNEL_ACCOUNT_ID=1 \
GOLANG_CC_CHANNEL_ACCOUNT_KEY=feishu-e2e \
GOLANG_CC_FEISHU_CREDENTIAL_FILE="$HOME/Library/Application Support/golang-cc/feishu-credentials.json" \
  scripts/channel-worker-screen.sh start

GOLANG_CC_CHANNEL_WORKER_NAME=copywriter \
GOLANG_CC_CHANNEL_ACCOUNT_ID=2 \
GOLANG_CC_CHANNEL_ACCOUNT_KEY=copywriter-feishu \
GOLANG_CC_FEISHU_CREDENTIAL_FILE="$HOME/Library/Application Support/golang-cc/feishu-copywriter-credentials.json" \
  scripts/channel-worker-screen.sh start
```

`GOLANG_CC_CHANNEL_SETTINGS_FILE` 可作为 `GOLANG_CC_CHANNEL_SETTINGS` 的等价别名。settings、provider 和 model 会写入 worker 的 0600 env 文件，并在 WebSocket 连接前做 provider preflight。
持久 env 是默认值来源；启动或重启命令显式传入的受支持环境变量优先，并在成功启动时写回 env。因此可以直接变更 provider、model 或其他 worker 配置，不需要先删除持久文件。

## 电脑重启后恢复

配置保存在 `~/.golang-cc/channel-workers/<worker>.env`，因此电脑重启不会丢失。`screen` 进程不会跨重启存活，登录后按 worker name 启动即可，不需要重新输入密钥：

```bash
GOLANG_CC_CHANNEL_WORKER_NAME=code scripts/channel-worker-screen.sh start
GOLANG_CC_CHANNEL_WORKER_NAME=copywriter scripts/channel-worker-screen.sh start
```

如果不确定进程是否已经存在，可使用 `restart`；脚本会按租户和账号清理孤儿进程并重新取得独占锁。`GOLANG_CC_CHANNEL_WORKER_STATE_DIR` 仍可覆盖默认目录，供部署系统显式指定持久卷。

## 故障排查

```bash
GOLANG_CC_CHANNEL_WORKER_NAME=copywriter scripts/channel-worker-screen.sh status
tail -f "$HOME/.golang-cc/channel-workers/copywriter.log"
screen -r golang-cc-channel-copywriter
```

看到 `WebSocket 连接成功` 只证明长连接建立；真实群组验收还必须看到入站 Inbox、TeamRun、mailbox、Outbox 和 Feishu provider receipt。

## 异步图片 worker

channel 图片异步化由独立的 image worker 负责，不占用 Feishu channel worker 的两分钟 run context。先完成 `000023_async_image_generation_jobs` migration，再为每个 tenant 启动一个或多个不同 `GOLANG_CC_IMAGE_WORKER_NAME` 的实例；同一 tenant 的任务通过 MySQL row lease 并行消费。

最小环境要求：

```bash
export GOLANG_CC_MYSQL_DSN='root@tcp(127.0.0.1:3306)/golang_cc_channel_e2e?multiStatements=true&parseTime=true&loc=UTC&charset=utf8mb4'
export GOLANG_CC_IMAGE_WORKER_TENANT_ID=1
export GOLANG_CC_IMAGE_WORKER_NAME=tenant-1-a
export GOLANG_CC_IMAGE_WORKER_SETTINGS_FILE="$HOME/.golang-cc/settings.json"
```

`settings.json` 中必须启用 `imageGeneration.enabled` 并配置 provider；worker 从服务端配置解析凭据，命令行和任务表不携带 secret。常用命令：

```bash
golang-cc --cwd "$PWD" --settings "$HOME/.golang-cc/settings.json" image-worker run
GOLANG_CC_IMAGE_WORKER_NAME=tenant-1-a scripts/image-worker-screen.sh start
GOLANG_CC_IMAGE_WORKER_NAME=tenant-1-a scripts/image-worker-screen.sh status
GOLANG_CC_IMAGE_WORKER_NAME=tenant-1-a scripts/image-worker-screen.sh restart
GOLANG_CC_IMAGE_WORKER_NAME=tenant-1-a scripts/image-worker-screen.sh stop
```

脚本生成独立 binary、screen session、0600 env 文件和日志，并等待 readiness 后才返回。`status` 会区分 stopped、starting 和 degraded；worker 启动时先清点候选数量，再按租约守卫执行历史 orphan `running` recovery 并 readback 确认结果，随后进行 tenant-scoped blob GC。队列深度、最老任务年龄、attempt/provider 延迟、retry、lease loss、stale recovery、completion lag 和 dead delivery 通过统一 telemetry 观察。
