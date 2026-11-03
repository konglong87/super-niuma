# Agent Eval Harness

Agent Eval Harness 是 Go Claude 的智能体回归评测入口，用于在不依赖真实模型和外部数据库的情况下，重复验证 agent 主链路是否仍然可用、可观测、可解释。

当前 P1 默认采用 deterministic local 模式，并提供 opt-in live profiles：

- 使用内置 fake model，避免 CI 或本地开发被真实 provider、网络和 API key 阻塞。
- 走真实 `query.Session`、tool loop、skills、Task sub-agent runtime、auto compact、permission policy 和 telemetry 链路。
- 产出 JSON 或 Markdown 报告，可作为 CI artifact 或本地回归证据。
- 默认数据集覆盖普通对话、工具调用、skills、sub-agent、subagent/multi-agent parity、multi-agent E2E、auto compact、权限边界和 trace/telemetry 证据。
- `--profile live` 可对已经启动的真实 API Server 做最小 live 健康检查，覆盖 `/health`、tenant sessions、mobile sessions 和 trace tenant sessions；未配置 live base URL 时返回 `skipped`，不影响本地和 CI 默认回归。
- `--profile live-agent-api` 可对已经启动的真实 API Server 执行 sub-agent task 管理 E2E，覆盖 `/tenant/agent-tasks` 创建、消息投递、事件回放、取消、状态读取和列表回查；配置 MySQL DSN 时会额外直查 `tenant_agent_tasks` / `tenant_agent_task_events` 持久化结果。
- `--profile anthropic-thinking` 可对真实 Anthropic provider 发起最小 thinking 请求，验证 `effort -> thinking config`、thinking content 和 proprietary signature；未配置 Anthropic auth 时返回 `skipped`。

## 运行方式

```bash
go run ./cmd/golang-cc eval agents --json
```

真实 API Server live profile：

```bash
export GOLANG_CLAUDE_CODE_EVAL_LIVE_API_BASE=http://127.0.0.1:18080
export GOLANG_CLAUDE_CODE_EVAL_LIVE_AUTH_TOKEN=test-token
export GOLANG_CLAUDE_CODE_EVAL_LIVE_TENANT_KEY=yutang
export GOLANG_CLAUDE_CODE_EVAL_LIVE_USER_KEY=eval-user

go run ./cmd/golang-cc eval agents --profile live --json
```

如果 `GOLANG_CLAUDE_CODE_EVAL_LIVE_API_BASE` 未设置，命令会输出 `status=skipped`，用于明确区分“没有 live 环境”和“live 环境失败”。

真实 API Server sub-agent task live profile：

```bash
export GOLANG_CLAUDE_CODE_EVAL_LIVE_API_BASE=http://127.0.0.1:18080
export GOLANG_CLAUDE_CODE_EVAL_LIVE_AUTH_TOKEN=test-token
export GOLANG_CLAUDE_CODE_EVAL_LIVE_TENANT_KEY=yutang
export GOLANG_CLAUDE_CODE_EVAL_LIVE_USER_KEY=eval-user

# 可选：开启 MySQL 直查持久化校验；不设置时 HTTP E2E 仍会运行，报告会标注 mysql_verification skipped。
export GOLANG_CLAUDE_CODE_EVAL_LIVE_MYSQL_DSN='user:pass@tcp(127.0.0.1:3306)/golang_cc_e2e?parseTime=true'

go run ./cmd/golang-cc eval agents --profile live-agent-api --json
```

该 profile 会创建一个 `live-e2e` agent task，追加 coordinator message，读取 started/message events，取消运行中的 task，再确认 task 状态变为 `cancelled`、events 包含 `cancelled`、列表接口能回查该 task。配置 MySQL DSN 时，还会直接查询 `tenant_agent_tasks` 的 `status/agent_name/trace_id` 和 `tenant_agent_task_events` 的 `started/message/cancelled`。

真实 Anthropic thinking live profile：

```bash
export ANTHROPIC_API_KEY=sk-ant-...
export CLAUDE_CODE_MODEL=claude-sonnet-4-5-20250929

go run ./cmd/golang-cc eval agents --profile anthropic-thinking --json
```

该 profile 会发送 `effort=low` 映射出的 enabled thinking config，并检查返回中是否存在 thinking 内容和 signature。没有 `ANTHROPIC_API_KEY` / `ANTHROPIC_AUTH_TOKEN` 时会输出 `status=skipped`。

输出报告到文件：

```bash
go run ./cmd/golang-cc eval agents \
  --output reports/agent-eval.json \
  --format json

go run ./cmd/golang-cc eval agents \
  --output reports/agent-eval.md \
  --format markdown
```

使用自定义数据集：

```bash
go run ./cmd/golang-cc eval agents \
  --dataset eval/agents.json \
  --output reports/agent-eval.json
```

## 数据集格式

```json
{
  "id": "custom-agent-eval",
  "description": "Custom deterministic eval suite",
  "cases": [
    {
      "id": "chat_basic",
      "type": "query",
      "prompt": "Say hello from eval.",
      "expect_contains": ["hello"]
    },
    {
      "id": "tool_read",
      "type": "tool",
      "prompt": "Read eval_target.txt.",
      "expect_contains": ["EVAL_TARGET"],
      "expect_tool_calls": ["Read"]
    }
  ]
}
```

支持的 P1 case type：

| Type | 覆盖链路 |
| --- | --- |
| `query` | 普通 query loop、模型响应、usage、telemetry。 |
| `tool` | assistant tool_use -> 本地 tool -> tool_result -> 下一轮模型响应。 |
| `skill` | `Skill` tool 加载本地 `.claude/skills/<name>/SKILL.md`。 |
| `subagent` | `Task` tool、sub-agent runtime、agent task store 和 nested lifecycle events。 |
| `subagent_parity` | agent schema/context sources、background Task、SubagentStart/Stop hooks、AgentCreate/List/Get/Stop 和 agent task trace evidence。 |
| `multiagent_e2e` | 真实 query loop 顺序调用 `AgentCreate`、`AgentMessage`、`AgentGet`、`AgentStop`，验证后台 agent、消息事件、进度查询、取消和最终汇总文本。 |
| `compact` | auto compact 触发、summary 模型请求、压缩后继续响应。 |
| `permission` | permission policy deny、工具错误回传、query 继续闭环。 |
| `trace` | request trace id、model/tool/query telemetry 事件和 report evidence。 |

## 报告字段

JSON 报告包含：

- `suite_id`、`status`、`started_at`、`finished_at`、`duration_ms`
- `total`、`passed`、`failed`、`skipped`
- 每个 case 的 `checks` 和 `evidence`
- evidence 中包含 response、tool calls、session id、transcript path、usage、telemetry event names、trace id、agent task 数等
- API Server live profile 的 evidence 包含请求 URL、HTTP status 和响应 body preview，便于把 WebUI/API Server 当前状态作为人工排查证据。
- live-agent-api profile 的 evidence 包含 task id、trace id、各 HTTP 步骤 status/body preview、事件类型列表、取消是否命中当前进程，以及可选 MySQL 直查事件列表。
- Anthropic thinking live profile 的 evidence 包含 model、base URL、thinking config、content block types、usage 和 stop reason。

Markdown 报告用于人工快速浏览；JSON 报告用于 CI 或后续 WebUI/Eval Dashboard 消费。

## 当前边界

- P1 默认是 deterministic local eval，不访问真实 LLM provider。
- live profile 只验证已经运行的 API Server endpoint 可达性和基本鉴权/tenant/mobile/trace 链路，不主动创建会话、不调用真实 LLM，也不替代 WebUI live E2E。
- `live-agent-api` profile 会通过真实 API 创建和取消 sub-agent task 管理记录，但不会启动真实模型 sub-agent runtime；MySQL 直查依赖 `GOLANG_CLAUDE_CODE_EVAL_LIVE_MYSQL_DSN` / `GOLANG_CLAUDE_CODE_MYSQL_DSN` / `MYSQL_DSN`，未配置时只跳过直查，不影响 HTTP E2E。
- `anthropic-thinking` profile 会调用真实 Anthropic provider，依赖有效账号、模型权限和网络；它验证 provider 接受 thinking config 并返回 thinking/signature，不替代完整长对话 resume 签名回放测试。
- 当前评分器是规则型：检查文本、工具调用、权限结果、telemetry/trace 证据、sub-agent/compact evidence、multi-agent parity 的 hooks/background/context 注入证据，以及 multi-agent E2E 的 AgentCreate/AgentMessage/AgentGet/AgentStop 全链路证据。模型裁判型评分后续可扩展。

## 验证命令

subagent / multi-agent 统一验收入口：

```bash
scripts/subagent-multiagent-acceptance.sh
```

该脚本会生成 `reports/subagent-multiagent/<timestamp>/eval-*.json`，默认运行 Go 全量测试、deterministic local eval 和 `git diff --check`；配置 live API base 或 Anthropic auth 时自动追加对应 live profiles。

```bash
go test ./internal/agenteval -count=1
go test ./internal/cli -run TestEvalAgentsCommandRunsDefaultSuite -count=1
go test ./internal/agenteval ./internal/cli -run 'TestRunLiveProfile|TestEvalAgentsCommand' -count=1
go test ./internal/agenteval -run 'TestRunLiveAgentAPIProfile' -count=1
go test ./... -count=1
git diff --check
```

有真实 Anthropic auth 时再运行：

```bash
go run ./cmd/golang-cc eval agents --profile anthropic-thinking --json
```
