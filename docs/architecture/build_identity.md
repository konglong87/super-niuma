# golang-cc Build Identity 技术方案

## 1. 目标与边界

本方案为 golang-cc 建立统一、机器可读的构建身份，使 CLI、HTTP runtime trace 和本地 runtime trace 能回答“这次结果由哪个二进制产生”。它服务于本地 Coding Agent 的评测闭环：只有把版本、revision、dirty 状态、构建时间和 Go toolchain 与延迟/成功率结果绑定，优化前后数据才可复现、可归因。

本次不修改 Agent loop、system prompt、上下文装配、模型请求、工具执行、权限、compact、并行调度或完成判定。Build Identity 只读取 Go 二进制内嵌元数据和链接期变量，不执行 Git 命令、不扫描工作区、不访问网络，也不进入 Agent 决策路径。

## 2. 拓扑与爆炸半径

- 影响节点：`RT-ENTRY`（CLI version 输出）与 `RT-OBSERVE`（runtime trace 元数据）。
- 爆炸半径：`B4_PROTOCOL`。runtime trace JSON 新增字段，属于对外机器可读协议的增量变化。
- producer：`internal/buildinfo`。
- consumer：CLI `version` 命令、本地 trace 导出、HTTP trace artifact 导出。
- persistence：无新增数据库或长期状态；trace artifact 仍按原路径写盘。
- gate：不新增 gate，不增加 turn、token、tool call 或模型延迟。
- 成本：每个进程只解析一次 `runtime/debug.ReadBuildInfo()`，结果缓存为不可变值。

## 3. 架构

新增 `internal/buildinfo`，作为构建身份的唯一事实来源：

```text
linker values ─┐
               ├─ buildinfo.Current() ─┬─ golang-cc --version
Go build info ─┘                       ├─ golang-cc version --json
                                       ├─ local runtime trace
                                       └─ HTTP runtime trace
```

`internal/cli` 不再拥有独立版本变量；构建脚本只向 `internal/buildinfo` 注入。trace producer 接收完整 `buildinfo.Info`，兼容字段 `agent_version` 与新增 `build_info.version` 从同一个值生成，避免两套版本逻辑漂移。

## 4. Schema 与字段来源

机器可读 schema 固定为 `golang-cc.build-info/v1`：

| 字段 | 类型 | 来源与语义 |
| --- | --- | --- |
| `schema_version` | string | Build Identity schema 版本 |
| `product` | string | 固定为 `golang-cc` |
| `version` | string | 链接期 `Version`；未注入时优先采用 Go module version；仍未知则为 `dev` |
| `revision` | string | 链接期 `Revision`；否则采用 Go build setting `vcs.revision`；未知为空串 |
| `dirty` | bool | 链接期或 Go build setting `vcs.modified` 的值；未知时为 false |
| `dirty_known` | bool | 区分“确认干净”和“无法判断”，避免把未知冒充干净 |
| `build_time` | string | 链接期 UTC RFC3339 构建时间；未知为空串 |
| `go_toolchain` | string | `runtime.Version()`，例如 `go1.25.0` |

来源优先级统一为：显式链接期值 > Go 内嵌 build info > 明确的未知默认值。所有字符串会去除首尾空白；无法解析的 dirty 注入值按未知处理，不静默认定为 false。

`build_time` 表示二进制实际构建时间，不复用 `vcs.time`（后者是提交时间）。`scripts/build.sh` 默认注入当前 UTC 时间；设置 `SOURCE_DATE_EPOCH` 时从该 epoch 生成，以支持可复现构建。release 脚本只计算一次并传给所有目标，保证同一次发布的多平台产物身份一致。

## 5. 兼容策略

- `golang-cc --version`、`-v`、`-V` 和 `golang-cc version` 的文本输出保持 `<version> (golang-cc)\n` 不变。
- 新增 `golang-cc version --json`；未知字段仍保留在 JSON 中，消费者无需猜测字段是否被省略。
- runtime trace 保留 `run.agent_version`，并新增 `run.build_info`。两者的 version 必须相等。
- runtime trace 顶层 `schema_version` 保持 `runtime-trace-v1`。此次是向后兼容的加字段，不修改既有字段含义。
- APG 的外部二进制指纹仍是评测可信来源；self-reported Build Identity 用于诊断与关联，不能替代 APG 对被测产物的独立校验。

## 6. 失败与安全边界

- `runtime/debug.ReadBuildInfo()` 不可用或缺字段时返回稳定的未知值，不阻止 CLI、server 或 Agent 启动。
- Build Identity 不包含环境变量、路径、命令行、API key、prompt 或用户数据。
- JSON 编码失败只可能来自固定结构；CLI 仍显式返回编码错误，不吞错。
- trace 旧消费者忽略新增字段即可继续工作；新消费者必须以 `dirty_known` 判断 dirty 是否可信。

## 7. 测试与验收矩阵

| 层级 | 验证内容 |
| --- | --- |
| 单元 | 链接期优先级、Go build settings 回退、未知值、dirty 三态、JSON 固定字段 |
| CLI | `version --json` 可解析；`--version` 与原 golden 文本保持一致 |
| Trace | `agent_version == build_info.version`；本地与 HTTP 导出均不再丢 BuildInfo |
| Build | `scripts/build.sh` 产物含 version/revision/build_time/toolchain；`SOURCE_DATE_EPOCH` 可复现 |
| 回归 | `go test ./... -count=1`、目标包 race、`go vet ./...`、`git diff --check` |
| 拓扑 | `runtime-topology-check` 以 `B4_PROTOCOL` 和 `updated` 声明通过 |
| E2E | 真实编译二进制执行 `--version` 和 `version --json`，并用真实本地 server + curl 导出 trace artifact |

验收还必须检查 diff 未触达 Agent 行为路径，并在结束后删除临时二进制、trace、日志和测试目录，避免评测垃圾持续占用本机磁盘。

## 8. 回滚

回滚只需撤销 `internal/buildinfo` 接入、构建脚本链接参数以及 trace/CLI 的增量字段。由于没有 schema migration、持久化迁移或 Agent 行为变更，旧二进制可直接替换新二进制；已有带 `build_info` 的 trace 仍可被按旧 schema 忽略未知字段读取。
