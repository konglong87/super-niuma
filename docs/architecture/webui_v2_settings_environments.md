# 设置中心环境切换

## 架构与范围

用户手动选择设置环境，聊天的 identity、React Query 缓存和 SSE 不变。每个环境的设置编辑器独立挂载，切换前检查未保存修改，写入期间禁止切换。环境标识由服务端白名单发布，浏览器不能提交 DSN 或指定任意数据库。

复用 Gin Profile handlers、tenant.Service 与 GORM repository：启动时从独立的环境清单读取连接引用，为每个环境注册独立 tenant service。只挂载设置、Profile、分配、渠道账号只读和消息只读接口，不启动新的 Agent runtime、reaper 或 worker。源用户必须匹配清单授权并通过主服务 owner/admin 检查，目标 tenant/user 固定在服务端；目标写入继续遵循已有角色校验和审计。

当前本机两环境的全局配置共用同一个 settings.json，因此统一沿原服务设置接口及条件写锁操作。环境切换只改变租户数据连接；配置页面显示实际文件路径与共享影响范围。生效页仍明确区分管理服务启动快照与 worker 的运行配置，不声称读取 screen 进程内存。

## 影响与验证

- Topology impact: updated；RT-ENTRY / RT-BOUNDARY / RT-PERSIST / RT-OUTPUT；Blast radius: B5_SHARED_STATE。新增预配置环境到既有 Profile 服务的管理入口。
- 正收益：原库原记录直接显示和管理，无复制、迁移或 worker 重启。成本：每个预配置数据库一个连接池、打开设置时一次环境目录读取；不新增 SSE、不进行模型调用。
- 风险与 gate：错误环境写入由固定目标身份、源身份白名单、owner/admin、切换确认和 busy 禁用保护；失联返回明确错误，不回退到别的数据库。
- 验证：未授权/未知环境/路径逃逸拒绝，目标身份不可伪造，两个库相同 ID 不串读，真实 Profile 与绑定回读，设置切换不改变聊天 identity/草稿/SSE；桌面/移动 UI；Go/前端测试、Swagger、拓扑及 diff 检查。
- 回滚：撤销环境清单配置或回滚应用提交，原数据库、Profile 和 worker 保持原样。

## 进度

2026-09-07 已完成实现与本机验收：

- `go test ./... -count=1`、最终 `go test ./internal/server -count=1`、环境路由/连接加载 race 测试通过；前端全量 56 个文件、522 项测试通过；typecheck、生产 build、Swagger/TS API 类型生成及 topology/diff 检查通过。
- 本次改动文件的 Biome error 检查通过。全仓 lint 仍有原有 `SessionSidebar.tsx` 的 `autoFocus` 错误和既存 hooks warnings，本次未扩大清理范围。
- Web 服务由 `scripts/web-agent-restart.sh e2e` 在 screen `webui-v2-environments` 启动，18087 可访问。原 coder/copywriter screen 未重启；两个 Profile 仍为原 ID 2/1、published v1，绑定仍是 `feishu-e2e` / `copywriter-feishu`。
- 真实 curl 验证目录、源身份拒绝（404）、token 拒绝（401）、Profile 和绑定回读。渠道库创建私有验收 Profile `settings-env-e2e-20260907`，网页修复保存为 v2 后归档；Web 库同 key 计数为 0，源/目标审计已回读。验收 v1/v2 保留为 archived 审计记录。
- Edge 桌面、390x844 移动端均完成切换，移动 documentWidth=390 无横向溢出；文案 Profile 真实对话目录显示 2 个会话，选中私聊加载 2 条正文，无消息读取错误。最终页面无 console error。复用现有 CUA Playwright，未安装额外浏览器依赖。
- 真实聊天发送后进入设置、切换渠道环境、返回同一 Session，助手回复正常且未发送草稿保留。自动化回归进一步断言原订阅 AbortSignal 不被终止、请求仍用原聊天身份。
- 未保存 JSON 的拒绝/同意切换与 busy 保护已自动化覆盖。真实浏览器已触发确认框，但确认框处理受浏览器工具 CDP 阻塞，未把该取消路径列为浏览器验收通过；未保存或覆盖用户全局配置。
- 端到端验收发现存量/部分字段 Profile 会使旧编辑器白屏，已复用结构检查保护 hydrate，原始 JSON 保留并明确报错，阻止未修复保存；新增回归并完成网页修复保存。绑定/归档绑定操作也接入 busy，写入期间不能切环境。

## 本机配置

默认清单位于 `~/.golang-cc/settings-environments.json`，也可用 `GOLANG_CC_SETTINGS_ENVIRONMENTS_FILE` 指定独立路径。清单内容：

```json
[
  {
    "id": "channel",
    "label": "渠道 Worker",
    "mysql_dsn_file": "webui-settings-environments/channel.dsn",
    "tenant_key": "yutang",
    "user_id": "feishu-e2e-user",
    "allowed_tenant_key": "webui-local",
    "allowed_user_id": "webui-local-user"
  }
]
```

连接文件只保存 DSN，例如 `root@tcp(127.0.0.1:3306)/golang_cc_channel_e2e?parseTime=true&loc=UTC&charset=utf8mb4`。两文件均设为 0600，凭据不进入 Git、临时目录或浏览器。默认清单配置后，通过 `scripts/web-agent-restart.sh e2e` 启动原 Web 环境即可显示两个选项，不需要重启 screen 中的 coder/copywriter。

页面默认选择当前 Web 环境；移动端在设置导航中切换，内容顶部始终显示所选环境。切换放弃未保存草稿需确认，保存中禁用选择器。渠道 Profile 对话在本环境只读查看，不提供会打开主库相同 Session ID 的跳转。生效配置展示管理服务快照，不能读取现有 worker 的进程内快照。Profile 分配对 WebUI 2.0 Session Control 执行的接入仍属于已有未完成边界，本次不声称改变聊天 Profile。
