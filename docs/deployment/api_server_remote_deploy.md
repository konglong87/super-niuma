# API Server Remote Deploy

本文档说明如何在另一台机器启动 `niuma` API server，并让前端访问。

## 1. 准备项目

```bash
git clone https://github.com/konglong87/niuma.git niuma
cd niuma

go version
go mod download
go build -o bin/niuma ./cmd/niuma
```

建议 Go 版本使用项目 `go.mod` 中声明的版本或更高兼容版本。

## 2. 配置模型

在目标机器创建本地私密配置文件：

```bash
cp config/config.demo.yaml config/config.local.yaml
```

示例 `config/config.local.yaml`：

```yaml
model: gpt-5.5
provider: custom

env:
  ANTHROPIC_BASE_URL: https://ai-gateway.example.com/v1
  ANTHROPIC_API_KEY: "replace-with-your-api-key"

fallback:
  enabled: true
  providers:
    - name: glm-5.1
      type: custom
      baseURL: https://ai-gateway.example.com/v1
      apiKey: "replace-with-your-api-key"
      model: glm-5.1
    - name: deepseek-v4-pro
      type: custom
      baseURL: https://ai-gateway.example.com/v1
      apiKey: "replace-with-your-api-key"
      model: deepseek-v4-pro
```

`provider: custom` 表示按 OpenAI Chat Completions 兼容协议请求模型网关。

## 3. 可选：启用 MySQL 持久化

如果前端需要 tenant/session/message/mobile chat 持久化，先创建数据库：

```sql
CREATE DATABASE niuma DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
```

配置 DSN 并执行 migration：

```bash
export NIUMA_MYSQL_DSN='user:pass@tcp(127.0.0.1:3306)/niuma?multiStatements=true&parseTime=true&charset=utf8mb4'
./bin/niuma tenant migrate up
```

## 4. 启动 API Server

远程机器必须绑定 `0.0.0.0`，否则只允许本机访问。

**绑定到 `0.0.0.0` 时 `--auth-token` 是必填的**：非回环绑定且没有 token 时 server 会
拒绝启动（AUDIT-P1-21）。此前这个组合会静默放行全部端点，包括 `/trace`（完整会话）
和 `/prompt-dump`（完整 prompt 正文）。本机自用（`--host 127.0.0.1`）不受影响，仍可
不配 token。

```bash
export NIUMA_ENV=prod
export NIUMA_MYSQL_DSN='user:pass@tcp(127.0.0.1:3306)/niuma?multiStatements=true&parseTime=true&charset=utf8mb4'

./bin/niuma server \
  --host 0.0.0.0 \
  --port 8080 \
  --auth-token 'replace-with-a-long-random-token'
```

访问地址：

```text
http://SERVER_IP:8080
http://SERVER_IP:8080/swagger/index.html
```

## 5. Curl 验证

健康检查。`/livez` 与 `/readyz` 不需要 token，供 k8s probe 与 LB 使用；`/health` 是运维
自查端点，仍然需要 token（AUDIT-P1-22）：

```bash
# liveness：进程还在响应 HTTP 就返回 200，不探任何依赖
curl 'http://SERVER_IP:8080/livez'

# readiness：逐个探 MySQL / quota Redis / mobile Redis，任一不可用返回 503
curl -i 'http://SERVER_IP:8080/readyz'

# 运维自查（含 workspace 等信息）
curl 'http://SERVER_IP:8080/health' \
  -H 'Authorization: Bearer <your-auth-token>'
```

`/readyz` 的 `checks` 只有 `ok` / `unavailable` 两种值 —— 它不鉴权，所以不会把 DSN、
地址或连接错误原文吐给匿名调用者，真实错误只进服务端日志。

k8s probe 配置：

```yaml
livenessProbe:
  httpGet: { path: /livez, port: 8080 }
readinessProbe:
  httpGet: { path: /readyz, port: 8080 }
```

OpenAI-compatible SSE：

```bash
curl 'http://SERVER_IP:8080/v1/chat/completions' \
  -H 'Authorization: Bearer <your-auth-token>' \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "gpt-5.5",
    "messages": [{"role": "user", "content": "hi"}],
    "stream": true
  }'
```

`/query`：

```bash
curl 'http://SERVER_IP:8080/query' \
  -H 'Authorization: Bearer <your-auth-token>' \
  -H 'Content-Type: application/json' \
  -d '{"prompt":"hi","model":"gpt-5.5"}'
```

## 6. 前端请求头

普通 API 需要：

```http
Authorization: Bearer <your-auth-token>
Content-Type: application/json
```

如果使用 tenant/session 持久化，建议同时传：

```http
X-Tenant-Key: yutang
X-User-Id: user-123
X-Session-Key: session-abc
X-Trace-Id: trace-xxx
```

## 7. CORS 和生产反代

当前 API server 主要面向后端 API，不建议直接把裸 `8080` 暴露到公网。浏览器前端跨域访问时可能遇到 CORS 限制，推荐用 Nginx 做同源反向代理：

```nginx
server {
    listen 443 ssl;
    server_name app.example.com;

    location /api/ {
        proxy_pass http://127.0.0.1:8080/;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

前端访问：

```text
https://app.example.com/api/v1/chat/completions
```

Nginx 会转发到：

```text
http://127.0.0.1:8080/v1/chat/completions
```

## 8. 生产建议

- 使用 HTTPS，不要让前端直接访问裸 HTTP 公网端口。
- `--auth-token` 使用强随机字符串。
- MySQL 只允许本机或内网访问，不开放公网。
- 用 systemd、supervisor 或容器托管 API server。
- 配置日志级别方便排查：

```bash
export NIUMA_LOG_LEVEL=info
```
