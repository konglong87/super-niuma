# WebUI 2.0 设置中心

## 目标与架构

将设置抽屉升级为可直接访问的全页设置中心，采用已确认原型的六个分类：通用、智能体、Profile、大模型、全局 Settings JSON、生效配置。会话本身的操作保留在会话菜单中。

大模型与 JSON 是同一个全局文档的两个编辑视图，共享内存草稿、语法状态、校验、版本和保存流程；无效 JSON 不丢失，不允许旧表单覆盖无效草稿。保存沿用 `/runtime/settings`，保留未知字段与未修改的掩码密钥。带 revision 的条件写防止设置中心之间互相覆盖；旧客户端保持兼容。

Profile 复用既有编辑、校验、发布、版本、分配 API。智能体页面管理已接入 Profile 的入口使用哪个已发布版本，Profile 页面管理定义。全局服务凭据鉴权和租户 Profile 权限边界不混用。

设置路由与聊天共用应用壳，聊天保持挂载以保留滚动、附件草稿及 SSE 订阅。进入设置隐藏聊天交互；返回恢复原会话。设置分类有独立 URL，刷新与浏览器后退可用；未保存修改有离开提示，密钥草稿只放内存。

## 配置事实与边界

`/status` 的现有回调按请求读取配置，不能宣称是进程启动快照或活跃 Run 快照。CLI 通过同一运行时路由解析器捕获启动快照，包含命令行模型、命名 Provider 和 `--settings` 覆盖。无法解析时保留设置修复入口，不生成虚假的启动快照。生效页区分文件解析结果、服务启动快照（不可用时明确标注状态回调）和会话/Task 已持久化的路由配置。来源由后端的配置加载路径提供；未记录的来源标为未知，不由前端猜测。保存不触发自动重启，不更改活跃 Run。

- WebUI v2 托管会话尚未接入 Profile 入口分配，不能把 `web_chat` 分配视为当前 v2 Run 的执行身份；页面明确显示此边界。本轮不扩展 Session Control Profile 协议。
- Profile 目录按所选设置环境读取原数据库和授权租户；screen 是 worker 的进程托管方式，不是 Profile 的配置来源。本机 coder/copywriter 位于 `golang_cc_channel_e2e` 的 `yutang`，网页聊天位于 `golang_cc_web_agent_real_e2e` 的 `webui-local`。设置中心通过[预配置环境选择器](webui_v2_settings_environments.md)手动切换，服务器复用独立 tenant service，不合并目录、不复制记录、不修改聊天 identity/SSE。
- 条件保存可防止同一服务中携带 revision 的 API 并发写互相覆盖；不宣称提供跨进程或外部编辑器的文件锁。Provider 掩码按唯一身份恢复，重排/删除不会错配密钥，身份含糊时返回校验问题。
- 连接测试仅主动请求模型目录，不进行付费推理，也不等价于完成真实对话。配置生效与实际 fallback 仍需结合重启、当前 Run 和 Trace 核对。
- 归档 Profile 可查看历史及复制为新草稿；已有后端禁止读取归档版本进行回滚，因此对应回滚按钮禁用并给出提示。

## 影响与验证

- Topology impact: updated（补充配置管理边界与消费方）。RT-BOUNDARY/RT-WIRING/RT-PERSIST/RT-OUTPUT/RT-SESSION-CONTROL；Blast radius: B5_SHARED_STATE，原因是全局文件写入、Profile 既有发布和绑定操作。
- 正收益：配置只有一份，保留旧接口能力，减少错误路由和并发覆盖。潜在副作用：严格路由校验可能拒绝已有非法组合；错误定位和只读预览提供修复路径。
- 校验保护 provider/protocol 路由一致性与 JSON 文档完整性，不增加模型 turn/token。只有主动连接测试使用网络，不在页面刷新或 SSE 上重复探测。
- 验证：前端视图同步、无效草稿、掩码、并发冲突、路由返回和多会话订阅；后端鉴权、负向校验、条件保存、readback、来源；真实浏览器桌面与移动布局、JSON 编辑以及 Profile 操作。全仓 Go、前端构建、拓扑检查、Swagger 与 diff 检查。
- 回滚：回滚应用提交；新增响应字段和接口保持旧版读写客户端兼容，不删除会话/Profile 数据。真实设置写入验收使用备份与读回，不更改用户的模型路由。

## 验收记录（2026-09-07）

1. 六类设置页面、聊天共存路由、配置后端、共享编辑器和 Profile API 集成已实现。未保存草稿跨分类保留，离开/后退保护与设置页精确会话搜索回归已覆盖。
2. 真实 curl 在隔离配置目录验证鉴权、路由校验、条件保存、409 冲突、保存回读、未知字段、掩码 Provider 重排、HTTP 模型目录和文件/启动快照区别。没有修改用户全局模型或密钥。
3. 真实浏览器验证模型表单到 JSON、JSON 到模型表单、非法 JSON 禁止保存并保留原文、校验、保存及整页刷新回读。
4. 真实 MySQL Profile 草稿创建、校验、发布、智能体目录同步和归档已完成；验收 Profile 已归档。没有修改原有 Profile、机器人绑定或入口分配。入口分配的成功回读、权限错误、部分失败和版本刷新由组件测试验证。
5. 桌面 1344px、移动 390px 布局、深浅主题和移动导航实测；修复长配置值撑高整页、移动来源字段裁剪以及旧组件固定浅色样式。
6. 全仓 Go、前端测试、类型检查/生产构建、Swagger、拓扑与 diff 检查作为提交验证。后端启动路由另覆盖 CLI model/named Provider/settings/workspace 回归。
7. 真实会话 Task 90 在运行期间进入设置，读取当前 Run 路由后返回，提交 AskUserQuestion 回答，收到最终回复。MySQL 回读为 `completed`，包含 `thinking_delta`、`text_delta`、提问/回答、工具和完成事件；往返设置后未发送输入草稿仍保留，验收后已清空。

## 2026-09-09 默认来源收敛

JSON/模型表单仍使用 `/runtime/settings`；生效配置的 `InspectSettings` 与 runtime 共用只含全局文件的搜索入口。默认 `~/.golang-cc/settings.json`，保留 `GOLANG_CC_CONFIG_DIR` 和显式 `--settings`；项目/旧全局/YAML 不再参与默认加载。启动快照及会话/Run 配置语义不变。详见 [设计与边界](../superpowers/specs/2026-09-09-global-settings-default-only-design.md)。
