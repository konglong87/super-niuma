# WebSearch 配置

golang-cc 内置 `WebSearch` 工具，模型发起 `tool_use: WebSearch` 时会由本地 Go runtime 发起 HTTP 请求并解析搜索结果。它不是 Claude Code 官方托管搜索服务，而是兼容同名工具的本地实现。

## 默认搜索端点

国内网络环境下 DuckDuckGo 经常超时或不可达，因此项目默认配置使用 Bing 中国：

```yaml
webSearch:
  endpoint: https://cn.bing.com/search
```

该配置已写入：

- `config/config.yaml`
- `config/config.dev.yaml`
- `config/config.prod.yaml`
- `config/config.demo.yaml`
- `config/config.example.yaml`

本地开发如需覆盖，可在未提交的 `config/config.local.yaml` 中配置同名字段。

## 覆盖优先级

`WebSearch` 端点优先级如下：

1. 环境变量 `GOLANG_CC_WEBSEARCH_URL`（旧 `GO_CLAUDE_CODE_WEBSEARCH_URL` 继续兼容）
2. 配置文件 `webSearch.endpoint`
3. 内置 fallback `https://duckduckgo.com/html/`

示例：

```bash
GOLANG_CC_WEBSEARCH_URL="https://www.so.com/s" go run ./cmd/golang-cc -p "搜索 Go 1.25 release notes"
```

## 端点要求

当前实现会把搜索词作为 `q` 参数追加到端点，例如：

```text
https://cn.bing.com/search?q=golang
```

因此可直接使用的搜索端点应支持 `q` 查询参数，并返回包含普通 `<a href="...">` 链接的 HTML。`https://cn.bing.com/search` 和 `https://www.so.com/s` 都符合这个形式。

百度、搜狗等搜索入口虽然国内可访问，但默认参数名通常不是 `q`，例如百度常用 `wd`、搜狗常用 `query`；不改代码时不适合作为 `webSearch.endpoint` 直接配置。

## 沙箱与网络策略

`WebSearch` 受统一网络沙箱约束：

- `sandbox.network.disabled` 会禁止联网搜索。
- `sandbox.network.allowDomains` / `denyDomains` 会约束搜索端点和返回结果域名。
- `sandbox.network.proxy` 可要求或禁用代理。
- `sandbox.network.mitm` 可要求注入 MITM CA。

如果搜索超时，优先检查当前网络是否能访问配置端点，以及沙箱是否限制了该域名。
