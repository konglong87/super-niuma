# 5 分钟上手牛马 Niuma

这份文档带你完成最短闭环：安装、配置模型、给牛马一个有明确验收条件的任务，并检查实际结果。

## 1. 选择安装方式

Niuma 是一个本地优先的开源 AI 执行工作台。它可在后台推进长任务，但实际运行时长受设备、模型服务、权限与预算约束；遇到阻塞时应暂停并交接，而不是保证永不停止。

### 桌面端（推荐日常使用）

从 [GitHub Releases](https://github.com/konglong87/super-niuma/releases/latest) 下载对应平台的安装包：

- macOS Apple Silicon：`arm64`
- macOS Intel：`amd64`
- Windows：安装程序
- Linux x86_64：桌面端压缩包

macOS 未签名构建首次打开时，按照系统提示在“隐私与安全性”中允许打开。正式签名状态以对应 Release Notes 为准。

### 源码运行（推荐开发和二次开发）

```bash
git clone https://github.com/konglong87/super-niuma.git
cd super-niuma
go run ./cmd/niuma --version
```

源码最低要求 Go 1.26+。CI / Release 使用 `.tool-versions` 中固定的 Go 版本。

## 2. 配置模型 Provider

打开桌面端设置页，或编辑：

```text
~/.niuma/settings.json
```

最小的兼容 API 配置如下：

```json
{
  "provider": "custom",
  "providerProtocol": "openai-chat-completions",
  "baseURL": "https://model.example.com/v1",
  "apiKey": "replace-me",
  "model": "model-id"
}
```

字段说明：

| 字段 | 含义 |
| --- | --- |
| `provider` | Provider 标识；自定义兼容服务可使用 `custom`。 |
| `providerProtocol` | API 协议适配器，例如 `openai-chat-completions`。 |
| `baseURL` | 模型服务的 API 根地址。 |
| `apiKey` | 模型服务凭据；不要提交到 Git。 |
| `model` | 服务端实际可用的模型 ID。 |

更多 Provider 约定见 [Provider 配置](providers.md)。

## 3. 执行第一条任务

CLI 一次性任务：

```bash
go run ./cmd/niuma -p "检查当前项目结构，并列出最值得先做的三个改进"
```

启动 TUI：

```bash
go run ./cmd/niuma
```

继续同一会话时使用已有的 session ID：

```bash
go run ./cmd/niuma \
  --session-id 11111111-1111-4111-8111-111111111111 \
  -p "记住刚才的结论，并继续检查测试覆盖率"
```

## 4. 确认 Agent 真的完成了工作

第一次体验建议使用可验证、范围较小的任务，例如：

```text
分析当前项目的 README 和测试结构，提出三个改进建议；不要修改文件，最后给出引用的文件路径。
```

确认结果至少包含：

- 实际检查过的文件或目录；
- 使用过的工具或命令；
- 清晰的结论；
- 未完成项或限制；
- 如果修改了代码，给出测试命令和结果。

## 5. 常见问题

- 模型没有响应：先检查 `baseURL`、`apiKey`、`model` 和协议是否匹配。
- macOS 无法打开：检查“系统设置 → 隐私与安全性”，或使用源码 CLI 先验证配置。
- 想接入外部工具：阅读 [MCP 扩展](mcp.md)。
- 想把能力封装成可复用任务：阅读 [Skills 文档](usage/README.md)。
- 遇到 Bug：先搜索 [Issues](https://github.com/konglong87/super-niuma/issues)，再提交最小复现。

## 下一步

- [使用场景](use-cases.md)
- [Provider 配置](providers.md)
- [MCP 扩展](mcp.md)
- [FAQ](faq.md)
- [贡献指南](../CONTRIBUTING.md)
