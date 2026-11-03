# CLI 模式使用说明

CLI（print / headless）模式适合脚本、CI、一次性自动化：给一个 prompt，拿到结果就退出，可选结构化输出。核心是 `-p, --print`。

```bash
go run ./cmd/niuma -p "查看当前目录有哪些文件"
```

每个场景统一结构：**一句话定位 → 命令 → 输出说明 → 要点**。本页不放截图，理由见 [使用说明总入口](README.md#为什么这里几乎没有截图)。

---

## 1. 一次性问答

给一个 prompt，直接输出文本结果，跑完即退出。最适合快速问答和嵌入脚本。

```bash
go run ./cmd/niuma -p "帮我总结 README.md"
```

默认输出人类可读的文本（`--output-format text`）：正文直接写到 stdout，诊断日志默认完全不写 stderr（见根 [README 诊断日志](../../README.md#诊断日志)），所以可以直接管道给别的程序。

**要点**

- 一次性、无交互，不进入 TUI。
- 会正常执行工具（读文件、跑命令），受同一套权限策略约束。
- 适合塞进 shell 别名、Makefile、git hook。

---

## 2. 结构化输出（json / stream-json）

需要程序消费结果时，用结构化输出格式。

```bash
# 只要最终结果（单个 JSON）
go run ./cmd/niuma -p "列出项目结构" --output-format json

# 流式事件（每行一个 JSON 事件）
go run ./cmd/niuma -p "读取 go.mod" --output-format stream-json
```

`json` 给一个最终结果对象；`stream-json` 逐行吐出 tool_use / tool_result / text / usage 等事件。

**要点**

- `json`：拿最终答案 + usage，适合"问一句要一句"的自动化。
- `stream-json`：拿完整事件流，适合前端渲染、日志留痕、二次编排。
- stream event 主链路有金标测试覆盖。

---

## 3. 脚本 / CI 自动化（指定 cwd）

在 CI 或脚本里对某个项目目录跑自动化时，用 `--cwd` 显式指定项目上下文。

```bash
go run ./cmd/niuma \
  --cwd /path/to/other-project \
  -p "读取项目规则并列出关键约束" \
  --output-format json
```

`--cwd` 决定加载哪个项目的 `.niuma/settings*`、`niuma.md`、skills、plugins 和 project memory。

**要点**

- `go run` 默认取进程启动目录作为项目上下文；跨项目时**必须**传 `--cwd`，否则会按当前仓库加载配置。
- 结合 `--output-format json` + `jq` 可把结果接进后续步骤。
- `--provider <name>` 可从合并后的 `fallback.providers` 精确选择 provider，选择会随 background / loop / goal 持久化。

---

## 4. 最小 runtime（--bare）

需要可预测的最小工具面和显式上下文时，用 `--bare` 关闭自动发现，只保留 `Read`、`Edit`、`Bash`。

```bash
go run ./cmd/niuma --bare -p "读取 go.mod 并总结模块信息"
```

bare 保留权限、sandbox、会话持久化以及显式 `--settings`、`--mcp-config`、`--add-dir`；它关闭 hooks、workspace memory、Git context、skills/custom agents/plugins/MCP 自动发现和启动期增强。

**要点**

- `--tools` 只能继续缩小 `Read/Edit/Bash`，不能恢复完整工具 registry。
- 支持 TUI、print、background、resume/continue；不支持 `--prompt-mode chat` 或普通顶层子命令。
- transcript 默认仍写入；需要无持久化时显式传 `--no-session-persistence`。
- 完整行为矩阵、显式输入规则和排错见 [`--bare` 使用说明](bare.md)。

---

## 5. 后台长任务（--bg / ps / logs / kill）

耗时任务丢后台跑，随时查看进度、附着输出、终止。

```bash
# 丢后台
go run ./cmd/niuma -p "跑一遍全量测试并总结失败项" --bg

# 观察与控制
go run ./cmd/niuma ps
go run ./cmd/niuma logs <id>
go run ./cmd/niuma attach <id> --wait
go run ./cmd/niuma kill <id>
```

后台任务、Goal 后台运行、Loop 定时任务共用同一套 `ps` / `logs` / `attach` / `kill` 观测控制命令。

**要点**

- `--bg` 创建后台任务并立即返回任务 id。
- `attach <id> --wait` 可阻塞等待任务结束。
- `goal run <goal-id> --background` 会创建 `kind=goal` 的后台任务，同样用这套命令观测。

---

## 6. 诊断（doctor / version / session inspect）

环境自检、版本确认、会话请求链路排查。

```bash
go run ./cmd/niuma --version
go run ./cmd/niuma version --json
go run ./cmd/niuma doctor
go run ./cmd/niuma session inspect <session-id> --json
```

`session inspect` 关联 transcript 与模型请求日志，展示每次请求的用途、模型、最终 provider、安全化 endpoint、attempt 和 fallback。

**要点**

- `--version`：保持稳定的人类可读输出；`version --json` 输出统一 Build Identity，包括 version、Git revision、dirty/dirty_known、build time 和 Go toolchain。`go run` 或未注入版本的本地构建显示 `dev`；`scripts/build.sh`、`scripts/install.sh` 和 Release 产物通过同一组 `ldflags` 注入真实构建身份（见根 [README 安装](../../README.md#安装)）。
- `doctor`：检查运行环境和配置。
- `session inspect`：新日志按 session UUID 精确关联；旧日志按时间和模型推断并标 `confidence=heuristic`，不会把当前配置冒充历史证据。加 `--log <path>` 指定日志文件。

## 7. 模型与 Provider

交互式 TUI 和 CLI 子命令都支持查看当前模型或列出当前配置可用的模型：

```bash
# 当前解析出的会话模型
niuma model get

# 当前配置的 modelOptions、主模型和 fallback provider 模型
niuma model list
```

`model list` 按配置顺序合并并去重模型 ID；没有自定义模型配置时，回退到内置模型目录。`--provider <name>` 仍用于本次会话选择命名 provider，选择 provider 后会使用该 provider 配置的模型。

---

← 返回 [使用说明总入口](README.md) · 其他模式：[TUI](tui.md) · [agent-webui](webui.md)
