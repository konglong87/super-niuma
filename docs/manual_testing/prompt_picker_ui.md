# 常用提示词 Picker UI 优化

日期：2026-09-16

## 架构与范围

- Topology impact: updated
- Blast radius: B1_SCENARIO
- Topology reason: 既有 RT-OUTPUT 中 WebUI 2.0 / desktop-v2 与 legacy Web Agent
  共用 PromptPicker 展示和模态交互；同步登记 legacy WebUI 消费者；
  既有 RT-BOUNDARY -> RT-PERSIST 数据链保持不变，
  不新增 API、持久化、授权 gate、模型调用或跨模块节点/因果边。
- producer 为原 prompt-template API，consumer 为 Picker；选择仍通过
  `onSelect(content)` 回调交给 Composer 写入草稿，非空草稿继续追加，不自动发送。
- 不修改 Settings 管理页、Wails bridge、数据库及 tenant/user 隔离规则。
- 用户追加要求 legacy WebUI 也支持后，`/webui/agent` Composer 显式接入
  相同 API 和 Picker，不读取、迁移或覆盖任何旧提示词存储。相同后端、tenant/user
  在两套 Web 页面看到同一模板目录；desktop-v2 使用独立的本地 SQLite，
  不与 WebUI 或 legacy WebUI 跨库同步。

## 实施

1. Composer 入口仅保留 Lucide Star，固定 32px，保留 tooltip、aria-label 和禁用状态。
2. Picker 通过 portal 挂到当前 `.webui2-page` 或 `.web-agent-page` 下，继承主题并脱离 Composer 的
   input/textarea/label 样式作用域；使用原生 dialog top layer，保持背景 inert。
3. 列表、编辑、删除确认三种互斥视图，避免表单叠加拉长弹窗。列表含内容预览、
   分类、置顶、编辑及删除；编辑含标题、正文、分类、排序、置顶和底部操作栏。
4. 列表/表单主体滚动，标题和保存栏留在可视区域；窄屏减少边距，长文本换行。
5. 保存/删除期间拒绝重复提交和关闭；失败保留表单或删除对象并显示错误；
   空列表、无搜索结果、加载和请求失败重试分别呈现。
6. 搜索短延迟合并请求，忽略旧响应；关闭恢复入口焦点，Tab 在弹窗内循环，
   Escape 从编辑/确认返回列表，再关闭；脏编辑沿既有原生确认方式防误丢。
7. 透明的 Composer 文件 input 改为 `display: none`，防止覆盖缩小后的星标。
   仍由现有附件菜单程序触发文件选择，不改变上传和粘贴路径。
8. 旧版 Composer 通过 identity prop 与 onSelect 回调接线，保留草稿空白、缩进、
   换行；发送中/取消中禁用，运行中仍可准备下一条草稿。不绑定 sendDisabled，
   否则空草稿会错误禁用入口。旧版主题语义 token 在 Picker 作用域映射到 v2。
9. 独立 div 承担编辑区滚动，fieldset 只承担批量禁用，避免短窗口中原生 fieldset
   滚动未能露出最后一行置顶选项；保存栏不参与滚动。
10. legacy WebUI 的 GET/HEAD `/webui`、`/webui/`、`/webui/agent` 和尾斜杠路径
    继续由现有 WebUI 路由处理；API、POST、未知路径及 Trace 仍原样转发，
    不吞掉鉴权错误。

## 收益与回归边界

- 收益：入口不占用文字宽度，控件不再被 Composer 样式拉伸，操作层级清晰。
- 潜在负作用：portal 与原生 dialog 改变焦点/事件生命周期；通过键盘、附件菜单、
  Composer 空/非空填充和多视口浏览器测试验证，不以纯 jsdom 推断视觉结果。
- 成本：复用已有依赖，无额外模型 tokens/turns/tool calls；新增图标、CSS 和局部
  React 视图代码。失败仍走原 API，不记录提示词正文。
- 回滚：回退本 UI 提交并重建 WebUI/desktop-v2，无需数据库迁移或删除用户模板。

## 验证矩阵

- 单元测试：图标入口、禁用、portal 脱离 Composer、加载/错误/空状态、请求竞态、
  新增/编辑/置顶/删除、重复提交、失败保留草稿、脏编辑确认、身份切换、
  选择到 Composer 的空输入/已有输入及不自动发送。
- 浏览器：1440x960、1024x768、Pixel 5，以及 375x480 短窗口；截图覆盖列表、
  空状态、编辑、删除确认、中英文、深色主题、长标题/分类及无横向溢出。
- 键盘：打开聚焦搜索，编辑聚焦标题，删除默认聚焦取消，Escape/Tab/Shift+Tab，
  关闭恢复星标焦点；附件菜单仍触发真实 filechooser。
- 运行前端 typecheck、全量 Vitest、Go 全量测试、desktop-v2/WebUI 生产构建、
  `git diff --check` 及 runtime topology check。
- 浏览器 CRUD 使用隔离的 Playwright API fixture，不修改手工验收用户的数据；
  本次 UI 优化不重新声称完成 Wails 原生进程重启/真实模型发送验收。

## 本地预览

WebUI 2.0 使用 `/webui/v2?token=test-token`（本地 18087 端口）。
`/webui/agent` 是原版 Web Agent 入口，现也提供同一个星标入口。

desktop-v2 构建以 `--outDir dist-desktop-check` 隔离验证，避免根路径
`/assets` 的桌面 bundle 覆盖本地 server 所需 `/webui/assets` 的 WebUI bundle。

## 执行结果

- Go 1.25：`GOTOOLCHAIN=local go test ./... -count=1` 通过。
- `npm run typecheck`、desktop-v2 隔离构建、WebUI 生产构建通过。
- 全量 Vitest：58 文件 / 539 用例通过。与浏览器/构建同时执行的一轮中，已有
  `WebUIV2App` invalid-deep-link 用例的瞬时 `Loading sessions` 断言曾失败；
  单独重跑全量通过，本次未改动该旧用例。
- Playwright：新增 2 场景 x 3 视口项目，6/6 通过，包含真实 filechooser 回归；
  截图已人工查看，桌面编辑器与移动短窗口保存栏无溢出。
- topology checker：18 节点 / 4 图有效，受影响节点仅 RT-OUTPUT。
- 本地 18087 服务返回更新后的 `/webui/assets` bundle，已在 Edge 打开新版页面。
- 构建仍有现存的大于 500 kB bundle 提示，未在本 UI 任务中做跨模块拆包。

## 原版接入增量验收

- Go 1.25 全量测试通过；前端全量 Vitest 58 文件 / 545 用例通过。
- Playwright 9/9：新版原有 6 项与旧版新增 3 视口测试通过。
  旧版覆盖空/非空草稿、新增/编辑/删除/置顶、深色主题及短窗口保存栏。
- 旧版 1024px 默认打开侧栏时需先用 Toggle left rail 收起；这是原有侧栏交互，
  本次未重构页面导航。375x480 可正常滚动到置顶选项并保存。
- 本轮不把 API fixture 浏览器验证等同于 Wails 原生真实模型发送或重启落库验收。

## Desktop-v2 原生验收

- desktop-v2 macOS arm64 包通过 Wails production build；全量 Go 测试再次通过。
- 原生操作使用既有 `GOLANG_CC_DESKTOP_CONFIG_DIR` 指向
  `desktop-v2/build/prompt-picker-acceptance`，只指定项目 workspace，不修改默认配置文件；
  runtime 使用 desktop-v2 默认 SQLite。验收端口为 18190。
- go-e2e：新建验收会话，在星标弹窗新增 `UI acceptance v2 2026-09-16`，
  选中后观察草稿 `Reply only: {{value}}`，改为 `Reply only: PROMPT_PICKER_OK`
  并点击发送；收到 `PROMPT_PICKER_OK`（gpt-5.5，界面显示 3s）。
  退出并重启应用，进入同一会话，历史消息和模板均存在。
- legacy WebUI 的浏览器/API fixture 验收仍保留在上文；旧 native host 已删除，
  因此不再把 legacy WebUI 的原生启动、重启或独立 SQLite 作为当前发布验收项。
- go-e2e 原生启动落到 `/` 时仍显示已有的“此会话链接无效”状态，
  点击侧栏会话或“返回会话列表”可以进入；此既有首页路由问题不在本次修复范围。
  后续已由 [2026-09-16 首次入口修复](desktop_v2_entry.md) 处理并完成
  desktop-v2 原生重启验收；
  本段保留作为修复前的事实记录。

## Review 修复：未选会话时的模板填入

- 问题：原版 Composer 在没有选中会话时也能填入模板，但新建/选择会话后，
  既有 draft hydration 会用该会话的草稿覆盖临时内容，导致模板编辑静默丢失。
- 方案：不改变会话草稿的隔离和恢复逻辑。共享 Picker 新增可选
  `selectionDisabledReason`，由 Composer 根据 `hasSelectedTask` 传入现有本地化提示；
  无会话时只禁用列表的填入按钮，保留搜索、新增、编辑、置顶、删除。
  禁用原因同时提供 title 和 aria-description；会话选中后即恢复填入。
  Picker 不依赖 session/draft 内部状态，v2 默认调用方式保持不变。
- Topology impact: none
- Blast radius: B1_SCENARIO
- Topology reason: 仅改变既有 RT-OUTPUT 消费者的填入前置条件，
  RT-BOUNDARY / RT-PERSIST 接口、数据结构和因果边不变。
- 保护的不变量：模板不能进入一个即将在会话初始化时被覆盖的无归属草稿。
  代价是 legacy WebUI 需先创建/选择会话才能填入；模板管理不受限制，
  恢复路径就是选择会话。
  无新增请求、模型 token、turn、tool call 或持久化成本。
- 回归矩阵：无会话时回调不执行且管理可用，选中会话后恢复填入，
  弹窗打开期间会话失效时同步禁用，正常空/非空草稿与失败重试维持原行为；
  三视口浏览器执行无会话创建模板、新建会话、填入与追加、编辑变量、
  页面刷新恢复草稿并断言未发送消息。v2 共享 Picker 用例一并回归。
- 观测使用 UI 回调、浏览器请求计数及 localStorage readback，不记录模板正文。
  回滚仅需撤销本次 UI 修复并重建前端，无数据库迁移。
- 执行结果：新增 2 条单元回归先在修复前失败，修复后通过；Go 1.25 全量测试、
  typecheck、全量 Vitest（58 文件 / 547 用例）、Playwright（9/9）、
  desktop-v2 前端隔离构建、WebUI 生产构建、diff check 与 topology check
  均通过。初次浏览器执行因桌面页有两个同名 New Session 按钮产生选择器歧义，
  定位到会话区入口后全量场景通过；没有修改产品导航行为。
- 本轮浏览器使用隔离 API fixture，验证前端完整会话流程和草稿 readback；
  未重新打包 Wails 原生应用，也未重复原生重启/真实模型发送，
  上一节原生验收结果不能视为本次修复后二进制的验收结果。
  构建仍有既有 bundle 大于 500 kB 的警告，本次不做拆包重构。
