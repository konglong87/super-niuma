# Web Agent Scripts

本文记录 `/webui/agent` 本地启动、滚动消息测试和真实端到端全生命周期测试脚本。核心原则：

- `scripts/web-agent-start.sh manual` 用真实 provider 配置启动手工验收，并复用历史 WebUI 数据。
- `scripts/web-agent-start.sh e2e` 用真实 provider 配置启动隔离 E2E 数据。
- `scripts/web-agent-scroll-smoke.sh` 用 deterministic stub provider 只做 UI 滚动/打字机压力测试。
- `scripts/web-agent-real-e2e.sh` 跑真实 provider、真实 Go server、真实 MySQL、真实浏览器渲染和 DB 回查。
- 不要把滚动 stub 的结果当作真实模型验收结果。

## 真实 Web Agent 启动

```bash
# 手工验收：复用历史数据库和租户。
scripts/web-agent-start.sh manual

# 手工验收重启（省略参数时默认为 manual）。
scripts/web-agent-restart.sh manual

# 隔离 E2E：使用独立数据库和测试身份。
scripts/web-agent-start.sh e2e
```

`manual` profile 默认使用 `golang_cc_webui_local` / `yutang` / `feishu-e2e-user`；`e2e` profile 默认使用
`golang_cc_web_agent_real_e2e` / `webui-local` / `webui-local-user`。启动日志会打印实际 profile、数据库、
租户和用户。如果显式提供的 MySQL DSN 数据库名与 profile 不一致，启动会直接失败，避免会话列表静默切库。

`manual` profile 默认行为：

- 构建 `web/dist`。
- 创建或复用 MySQL 库 `golang_cc_webui_local`。
- 执行 `tenant migrate up`。
- 初始化或复用 `yutang / feishu-e2e-user`。
- 启动 Go server：

```text
http://127.0.0.1:18087/webui/agent?token=test-token
```

该脚本不会设置 stub provider。模型 provider 来自环境变量或项目配置，例如 `config/config.local.yaml`。

`/v1/models` 的页面可选模型列表来自当前 workspace 加载到的 `modelOptions`。加载顺序沿用 golang-cc 配置链路，先读 owned global settings（默认 `~/.golang-cc/settings.json`，或 `GOLANG_CC_CONFIG_DIR/settings.json`），再读 legacy 项目 `.claude/settings*.json`、golang-cc 项目 `.golang-cc/settings*.json` 或 configured identity dir 下的 settings，以及 `config/*.yaml`；列表会按顺序合并并去重，golang-cc 项目配置会覆盖 legacy 项目配置。没有配置 `modelOptions` 时，接口回退到内置 `config.KnownModels`。

全局模型列表示例：

```json
{
  "model": "gpt-5.5",
  "modelOptions": [
    "gpt-5.5",
    "claude-sonnet-4-5-20250929",
    "claude-opus-4-8"
  ]
}
```

Web Agent Thinking 正文展示使用独立的全局 UI 配置，默认完整展示。支持 `full`、`summary`、`hidden` 三种模式：

```json
{
  "webAgentUI": {
    "thinkingMode": "summary"
  }
}
```

`full` 直接展示 Thinking 正文；`summary` 在消息级摘要卡片中折叠，点击“展开思考”查看原文，再点“收起思考”恢复摘要；`hidden` 不在正文展示 Thinking 或占位块。页面运行时的 Thinking 选择器可以立即切换三态。未配置 `thinkingMode` 时兼容旧 `webAgentUI.showThinking=true/false`，分别映射为 `full/hidden`。

这些设置只控制 Web Agent renderer。服务端仍会生成和持久化 `thinking_delta`，Trace、审计、SSE 和模型推理行为不变。页面已将连续 `thinking_delta` 聚合为带回合/phase 锚点的一条记录，并展示可计算的耗时、行数；无法可靠归因的 token 明确显示为“token 未单独统计”，不做估算。折叠状态按 `session_id + turn + phase` 保存在当前页面 UI 中，切换模式时保留，重新加载后恢复默认折叠；provider 级 phase token 归因、状态指标和跨刷新持久化仍在后续清单中。TUI 使用单独的 `tui.thinkingMode`，两者互不影响。

项目本地追加模型示例，YAML 中也兼容 `model_options` 写法：

```yaml
# config/config.local.yaml
model: gpt-5.5
modelOptions:
  - gpt-5.5
  - glm-5.1
```

常用覆盖：

```bash
GOLANG_CC_WEB_AGENT_PROFILE=manual \
GOLANG_CC_WEB_AGENT_PORT=18090 \
GOLANG_CC_WEB_AGENT_DB=golang_cc_web_agent_manual \
GOLANG_CC_WEB_AGENT_MODEL=glm-5.1 \
scripts/web-agent-start.sh manual
```

高可用兜底配置：

```bash
GOLANG_CC_AGENT_TASK_RUN_TIMEOUT_SECONDS=1200 \
GOLANG_CC_AGENT_TASK_IDLE_TIMEOUT_SECONDS=120 \
GOLANG_CC_AGENT_TASK_MAX_CONCURRENT_RUNS=16 \
scripts/web-agent-start.sh manual
```

- `GOLANG_CC_AGENT_TASK_RUN_TIMEOUT_SECONDS`：单个 Web Agent task 总运行超时，默认 1200 秒。
- `GOLANG_CC_AGENT_TASK_IDLE_TIMEOUT_SECONDS`：流式回复中连续无新输出的 idle 超时，默认 120 秒。
- `GOLANG_CC_AGENT_TASK_MAX_CONCURRENT_RUNS`：同时在跑的 task runner 上限，默认 16。
  超限的请求返回 `503` + `Retry-After: 5`，task 原样留在 `ready` 可重试 —— 不排队，避免把压力
  转成内存占用。
- 进程重启会让在跑的 runner 失联，DB 里的 task 停在 `running`；服务端启动时以及每
  `clamp(run_timeout/4, 1min, 10min)` 会扫一轮，把超过总运行超时仍是 `running` 的 task 置
  `failed`，`result_json` 里 `source=stale_task_reaper` / `reason=runner_lost` 说明是失联而非正常完成。
- provider 中途挂起时，服务端会自动把 task 写成 `failed` 并追加 failed event，避免页面长期停在右下角“取消”。
- 用户主动点击取消仍会写成 `cancelled`，不会和 idle timeout 混淆。

## 滚动消息专用 Stub 后端

```bash
scripts/web-agent-scroll-smoke.sh
```

默认行为：

- 启动 `scripts/web-agent-scroll-provider.mjs` 在 `127.0.0.1:19087`。
- 将 Web Agent server 指向这个 OpenAI-compatible stub provider。
- 使用模型名 `scroll-stub`。
- 启动 Web Agent：

```text
http://127.0.0.1:18088/webui/agent?token=test-token
```

适用场景：

- 消息满屏后的自动滚动。
- 用户消息右侧、AI 回复左侧的气泡布局。
- 长文本流式 `text_delta` 打字机效果。
- composer `Send / Cancel / sending / cancelling` 状态机视觉检查。

不适用场景：

- 真实模型能力验收。
- provider 配置验收。
- 真实 token/usage 结算验收。

滚动 stub 输出固定包含：

```text
scroll stub: Web Agent message overflow test
```

看到这个前缀时，说明当前是 UI 压测链路，不是真实模型链路。

## 真实端到端全生命周期测试

```bash
scripts/web-agent-real-e2e.sh
```

覆盖范围：

- 启动真实 Web Agent server。
- MySQL migration 和默认身份初始化。
- `POST /agent/workspaces/validate` 验证真实 cwd/git root。
- `POST /tenant/agent-tasks` 创建 `ready` task。
- `POST /tenant/agent-tasks/{id}/message` 触发真实 runner。
- 轮询 task 到 `completed`。
- 验证事件流至少包含 `message`、`text_delta`、`completed`。
- 直查 MySQL `tenant_agent_tasks` 和 `tenant_agent_task_events`。
- Playwright 打开 `/webui/agent?token=...`，检查页面 200、真实回复可见、无已知 stub 文本、console 无 error/warning。
- 保存截图到 `/tmp/web-agent-real-e2e-*.png`。

通过输出示例：

```text
web-agent-real-e2e ok
url: http://127.0.0.1:18087/webui/agent?token=test-token
task: 1
model: glm-5.1
response: 真实后端连通测试已成功完成，系统运行正常。
events: message=1 text_delta=8 completed=1
screenshot: /tmp/web-agent-real-e2e-1710000000000.png
```

常用覆盖：

```bash
GOLANG_CC_WEB_AGENT_PORT=18091 \
GOLANG_CC_WEB_AGENT_DB=golang_cc_web_agent_real_ci \
GOLANG_CC_WEB_AGENT_MODEL=glm-5.1 \
GOLANG_CC_WEB_AGENT_E2E_PROMPT='请用一句中文回复：真实后端连通测试。不要使用英文。' \
GOLANG_CC_WEB_AGENT_E2E_EXPECTED='真实后端' \
scripts/web-agent-real-e2e.sh
```

如果已有 server 在目标端口运行，并且你只想验证现有 server：

```bash
GOLANG_CC_WEB_AGENT_E2E_AUTO_START=false \
GOLANG_CC_WEB_AGENT_PORT=18087 \
scripts/web-agent-real-e2e.sh
```

自动启动模式下，如果目标端口已经被占用，脚本会拒绝复用旧进程。需要隔离运行时请设置一个空闲的 `GOLANG_CC_WEB_AGENT_PORT`。

## 真实链路和 Stub 链路判定

| 现象 | 判定 |
| --- | --- |
| 回复包含 `web agent browser flow ok` | 旧 stub provider，不是真实模型链路。 |
| 回复包含 `scroll stub:` | 滚动 UI stub provider，不是真实模型链路。 |
| task `result_json.model` 是真实模型名，例如 `glm-5.1` | 真实 provider 链路。 |
| DB 有 `message`、多条 `text_delta`、`completed`，页面能看到同一回复 | API + runner + SSE/事件 + UI 渲染闭环。 |
| `/tenant/agent-tasks` 返回 `tenant storage is not configured` | server 没有配置 `GOLANG_CC_MYSQL_DSN`，不是前端问题。 |
| Web Agent UI 验收出现 `get tenant ai-study ... not found` | 浏览器复用了其他项目 identity；保持 Web Agent 专用测试库并恢复 `webui-local / webui-local-user`，不要切换到 `ai-study` 数据库。详见[打字机回复自动跟随最新内容方案](../pending-fixes/web-agent-follow-latest.md)。 |
| 打字机回复增长但页面停在回复开头 | 子组件可见文本增长没有触发父页面滚动；按真实消息高度自动跟随的实现和验收见[打字机回复自动跟随最新内容方案](../pending-fixes/web-agent-follow-latest.md)。 |
| AI 回复一部分后右下角一直是“取消” | 优先检查 task 是否仍是 `running` 且长时间没有新 `text_delta`。当前服务端有 idle timeout 兜底，超过配置时间会自动落 `failed`。 |
| task 自动变成 `failed` 且 result/error 包含 `idle timeout` | provider/runner 在流式中途挂住，没有返回 completed/error；这是底层模型链路可用性问题，不是纯 UI 问题。 |

真实验收时必须使用 `scripts/web-agent-real-e2e.sh` 或手动完成同等覆盖，不要只看页面是否打开。
