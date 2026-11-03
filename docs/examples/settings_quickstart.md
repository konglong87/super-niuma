# 全局配置

niuma 默认只读取 `~/.niuma/settings.json`。WebUI 2.0 和桌面端设置中心读取、校验、保存的也是同一个文件。
配置目录保留旧路径是为了升级兼容，产品和可执行文件名均为 `niuma`。

## 最小配置

```json
{
  "provider": "openai-compatible",
  "providerProtocol": "openai-chat-completions",
  "baseURL": "https://model.example.com/v1",
  "apiKey": "replace-me",
  "model": "model-id"
}
```

地址、密钥和模型 ID 必须替换为你自己选择的服务配置。没有内置的默认模型或默认服务地址。
完整示例见 [settings.example.json](settings.example.json)，备用路由默认关闭。

首次创建文件后限制访问权限：

```bash
chmod 600 ~/.niuma/settings.json
niuma doctor
niuma -p "你好"
```

不要把真实密钥提交进仓库。设置中心会遮蔽凭据，保存时保留未改动的密钥，并检查文件版本以避免覆盖其他进程的新修改。

## 配置边界

- 不会自动扫描项目中的 `settings*.json` 或 `config/*.yaml`。
- 不会读取其他产品的模型配置、API Key 或 OAuth Token。
- 兼容外部项目指导文件格式，例如 `CLAUDE.md`、`AGENTS.md` 和 memory。这是文件上下文兼容，不是模型配置兼容。
- `--model`、`--provider` 和显式 `--settings` 是单次运行覆盖，不会更改全局文件。`--settings` 接受 JSON 文件或 JSON 字符串，不会隐式加载 YAML。
- 历史配置根目录环境变量仅为兼容保留。常规部署不设置它们，使用统一的 `~/.niuma/settings.json`。
- 保存不会改变正在执行的 Run；新请求读取新配置，部分服务启动配置需要重启。

## 字段

| 字段 | 用途 |
| --- | --- |
| `provider` | 适配类型，例如 `openai-compatible` 或 `custom` |
| `providerProtocol` | `openai-chat-completions`、`openai-responses` 或兼容适配层的 `anthropic-messages` |
| `baseURL` | 服务 API 根地址，不填写具体的 `/chat/completions` 或 `/responses` 路径 |
| `apiKey` / `authToken` | 你选择的服务凭据 |
| `model` | 该服务支持的模型 ID |
| `modelOptions` | 模型选择器候选项 |
| `fallback.providers` | 命名路由；每项配置独立的类型、协议、地址、凭据和模型 |
| `subagentModelTiers` | 旧档位别名到模型 ID 的显式映射；未配置时继承会话模型 |
| `modelPricing` | 每百万 token 的价格；未知模型不虚构价格 |
| `permissions` | 工具的允许、询问、拒绝规则 |
| `appearance` / `pet` | 背景皮肤与 3D 宠物偏好 |

## Responses 协议

使用服务提供的 Responses API 时，显式设置：

```json
{
  "provider": "custom",
  "providerProtocol": "openai-responses",
  "baseURL": "https://model.example.com/v1",
  "apiKey": "replace-me",
  "model": "model-id",
  "responses": {
    "stateMode": "stateless",
    "store": false
  }
}
```

协议由配置决定，不会探测或静默切换。当前支持无状态模式；`previous-response-id` 与 `store: true` 不可用。

## 子代理与用量

模型档位只是兼容别名，不会自动跳转到某厂商模型。例如：

```json
{
  "subagentModelTiers": {
    "haiku": "your-fast-model",
    "sonnet": "your-main-model",
    "opus": "your-large-model"
  }
}
```

映射值是模型 ID，不是 provider 的名称；对应模型必须在主路由或备用路由中可用。
`modelPricing` 可填写 `input`、`output`、`cacheRead`、`cacheWrite5m`、`cacheWrite1h`，按所选服务的实际价格配置。

`autoCompact`、`recap`、`nextSteps` 等辅助功能可能调用模型并产生用量。需要控制成本时显式设置其模型与开关。
全部字段以 `internal/config.Settings` 为准。
