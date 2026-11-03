# go-e2e 公开发布：用 Go 构建一个本地优先的 Coding Agent

> 发布版本：[`v0.3.4`](https://github.com/konglong87/go-e2e/releases/tag/v0.3.4)

我把 go-e2e 推进到了一个可以公开下载、配置和运行的阶段。
它是一个用 Go 编写的本地优先开源 Coding Agent：你可以从 Desktop、CLI、TUI 或 WebUI 发起任务，让 Agent 检查项目、调用工具、修改代码、执行命令并报告验证结果。

## 这不是一个只返回文本的聊天窗口

一个 Coding Agent 的价值不只是回答问题，而是完成一条可验证的任务闭环：

```text
自然语言任务
  → 理解与规划
  → 文件、命令和外部工具
  → 代码修改
  → 测试与验证
  → 结果交付
```

go-e2e 围绕这条闭环提供了共享 Runtime Core，并把能力适配到多个入口：

- Desktop：模型设置、会话管理和可视化工作台；
- CLI / TUI：终端交互、脚本和一次性任务；
- WebUI：浏览器工作台和服务端部署；
- MCP / Skills：连接外部工具、封装团队工作流；
- Provider：连接兼容 API 和不同模型服务；
- SQLite / MySQL：本地优先，也支持私有化部署。

## 5 分钟上手

### 下载桌面端

从 [GitHub Releases](https://github.com/konglong87/go-e2e/releases/latest) 下载对应平台安装包。

### 从源码运行 CLI

```bash
git clone https://github.com/konglong87/go-e2e.git
cd go-e2e
go run ./cmd/go-e2e --version
go run ./cmd/go-e2e -p "分析当前项目结构，列出最值得先改进的三个地方"
```

然后在设置页或 `~/.golang-cc/settings.json` 中配置模型 Provider、协议、地址、模型和凭据。
完整步骤见 [5 分钟上手](../getting-started.md)。

## 第一次使用建议

先使用一个只读、范围较小、结果容易验证的任务：

```text
分析当前项目的 README 和测试结构，不要修改文件，最后给出：
1. 关键模块；
2. 你引用的文件路径；
3. 最值得先做的三个改进；
4. 尚未验证的边界。
```

跑通后，再尝试“定位 Bug → 修改代码 → 补测试 → 执行验证”的连续任务。
更多可复制任务见 [使用场景](../use-cases.md)。

## 为什么开源

这个项目既是一个可使用的 Coding Agent，也是一个可以阅读和扩展的 Go Agent runtime 实践：

- 想使用：直接下载 Release；
- 想学习：从 Runtime Core、Provider、Skills、MCP 和 Tool loop 开始阅读；
- 想扩展：提交 Provider、MCP、Skills、文档或测试改进；
- 想反馈：告诉我第一次安装、配置和运行时卡在哪里。

## 当前边界

项目仍处于 `0.x` 快速迭代阶段。运行 Agent 需要自行配置模型服务和 API 凭据；未配置 Apple Developer ID 的 macOS 构建可能需要手动允许打开；不同模型 Provider 的工具调用、多模态和上下文能力可能不同。

这次公开发布的目标不是宣称“所有环境都已经完美”，而是把安装入口、文档、限制和反馈路径公开，让真实用户参与下一轮改进。

欢迎在 [GitHub Discussions](https://github.com/konglong87/go-e2e/discussions) 分享第一次运行结果，也欢迎提交 [Issue](https://github.com/konglong87/go-e2e/issues) 和 PR。
