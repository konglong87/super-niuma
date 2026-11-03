# Provider 配置

go-e2e 把模型接入抽象为 Provider。你可以使用项目支持的协议适配器，也可以连接提供兼容 API 的模型服务。

## 最小配置

```json
{
  "provider": "custom",
  "providerProtocol": "openai-chat-completions",
  "baseURL": "https://model.example.com/v1",
  "apiKey": "replace-me",
  "model": "model-id"
}
```

配置文件默认位置：

```text
~/.golang-cc/settings.json
```

桌面端也可以在设置中心维护同一份全局配置。

## 选择协议

| 场景 | `providerProtocol` 示例 | 说明 |
| --- | --- | --- |
| OpenAI-compatible Chat Completions | `openai-chat-completions` | 适用于暴露兼容 `/v1` Chat API 的服务。 |
| Anthropic Messages | `anthropic-messages` | 适用于使用 Messages 协议的服务；具体能力取决于服务端。 |
| OpenAI Responses | `openai-responses` | 适用于支持 Responses API 的服务；请确认模型和工具能力。 |

协议名称和可用字段以当前代码和配置校验为准；如果服务商只兼容部分 API，优先用最小文本请求验证。

## 安全建议

- 不要把真实 `apiKey` 写入仓库、Issue、截图或日志。
- 使用专用、可撤销、低权限的测试凭据。
- 远程部署时限制服务监听地址和网络访问范围。
- 发现凭据泄露时立即撤销，不要只删除 Git 工作区中的文件。

## 排障顺序

1. `baseURL` 是否包含正确的 API 根路径；
2. `providerProtocol` 是否与服务端协议一致；
3. `model` 是否是服务端实际可用的模型 ID；
4. API Key 是否有效且有对应模型权限；
5. 先执行只读任务，确认文本对话正常，再启用工具或多模态能力；
6. 如果仍然失败，提交脱敏后的请求协议、错误信息、版本和运行平台。

相关入口：

- [5 分钟上手](getting-started.md)
- [WebUI 使用说明](usage/webui.md)
- [API Server](api_server.md)
