# WebUI 2.0 对话修复

目标：修复 Provider 路由冲突、会话 key 被当作数字 ID、历史读取错误被隐藏，并接通会话事件的历史和 SSE 消费。设置管理页面及正式指标系统不在本期。

架构：配置沿用 config 解析；会话控制继续负责授权、幂等及 detached Run。新增会话事件读接口按 tenant/user/session 限定查询，复用 Task events。前端采用应用级订阅与纯事件归并，界面切换不取消任务。密钥不进入会话 metadata。

Topology impact: updated。涉及 RT-WIRING、RT-BOUNDARY、RT-SESSION-CONTROL、RT-PERSIST、RT-OUTPUT。配置 B3，新增 SSE/API B4，会话写入最高 B5_SHARED_STATE。现有状态 SSE 保留，内容读取独立，不扩大 Local read 权限。

正收益：默认全局路由可运行，历史错误可见，实时内容可续传。风险：配置继承行为变化、重复或漏读事件、慢客户端占用连接。验证：配置组合回归、租户负向测试、事件分页/重放测试、真实 MySQL/curl、浏览器多会话切换、全量 Go 测试及 Web build。连接使用取消和空闲退避，保留已有错误/耗时日志，不新增指标平台。

回滚：按独立提交回退应用和新内容入口，保留已有会话与 Task events。不可通过删除数据、放宽权限或吞错恢复。

## 完成状态（2026-09-06）

- 修复跨 Provider 合并时遗留协议、Responses 和端点凭据的问题；同 Provider 的仅 model 覆盖保持原行为，显式冲突继续报错。仓库默认配置继承用户 settings，Anthropic 示例独立存放。
- 新会话支持选择 Provider、model、cwd，命名 Provider 写入现有 session metadata 并用于后续 Run；拒绝在发送或排队时更换已绑定 Provider。服务启动检查默认路由，创建/执行前检查所选路由；不修改用户密钥。
- 新内容 API 用 Session key 做授权，使用授权快照中的数字 ID 查询主对话 Task events。前端不再请求错误的 timeline 地址，也不再吞掉历史加载错误。
- 应用级单连接订阅当前会话和活跃会话，按各自游标恢复，消息归并按 event ID 去重。支持文字、思考、工具、权限请求和执行失败显示；切换会话不取消 Run，草稿在当前页面内按会话保留。
- 模型上下文历史按当前会话查询，避免被其他会话最近 200 个任务挤出。旧 Task SSE、状态 SSE、Mobile envelope 均保持原接口。

## 验证结果

- `go test ./... -count=1` 通过；增加 Provider 持久化/拒绝更换、配置组合、HTTP 授权、存储查询作用域及 tenant 身份传递回归。
- Web 42 个测试文件、336 个测试通过；生产 build、Swagger/API 类型生成、拓扑检查和 `git diff --check` 通过。
- 本地真实服务 `18087` 使用隔离数据库 `golang_cc_web_agent_real_e2e`，保留用户报告的 Session 7。读回其 Run 14/15 的用户消息与失败正文，并确认新版页面显示失败。
- Session 8/9 的命名 Provider 已落库；Run 16/17/18 与并发 Run 20/21 已完成，实际得到模型回复。二轮对话验证上下文记忆，A/B 并发验证消息隔离；独立游标重连无已消费事件重放，非所属用户读取被拒绝。
- Playwright 验证刷新历史、A/B 草稿、Provider/model 联动、桌面和 390px 手机视图，无页面异常或横向溢出。证据位于本机 `~/.golang-cc/webui-repair/`，未纳入仓库。

## 当前边界

- Agent Profile 选择与版本固定、设置管理入口不在这次对话修复中，仍待后续实现。
- 命名 Provider 会固定名称；选择“服务端默认”且 settings 未指定命名 Provider 时，后续请求仍按当时的默认路由解析。模型与 cwd 已保存，但不快照密钥及整份 Provider 配置。
- 单连接最多实时订阅 32 个会话，当前选中会话优先；超过上限的后台会话仅通过列表刷新看到状态，选中后补拉历史。前端列表每 5 秒刷新。
- 聚合 SSE 当前复用按会话授权和事件查询，尚未批量合并数据库查询；空闲退避到 2 秒，不代表做过高并发容量认证。没有新增连接数/查询量监控平台。
- 工具和权限映射有测试覆盖，真实模型验收使用纯文本请求，未声明真实工具执行与权限审批全链路已验收。草稿仅页面内保留，刷新不持久化。
- 未修改共享 system prompt、工具 schema、模型循环或 token/cache 策略；未做跨 Provider 的延迟及 token A/B 基准，不将这次可用性修复表述为性能优化。
