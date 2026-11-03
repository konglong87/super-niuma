# Feishu AskUserQuestion 真实验收记录

> 2026-09-16 开源脱敏：Run、interaction、平台消息标识替换为关联一致的
> demo 值，仅用于说明数据关系，不可作为真实查询参数。原始记录保留在私有历史，
> 合成问答、计数和验收结果不变。

日期：2026-08-24
环境：本机 MySQL、Redis、真实 Feishu 长连接、真实 `jiuan-responses-gpt-5.6sol` provider
数据库：`golang_cc_channel_e2e`
Schema：`14`，`dirty=0`

## 1. 验收结果

通过。真实 worker 完成了：

```text
inbound -> query AskUserQuestion -> channel_interactions.pending
        -> Run waiting_input -> Feishu question card sent
        -> answer CAS -> worker restart recovery
        -> exact tool_result resume -> final Feishu card sent
```

最终验证 Run：`acceptance_demo_1`
Session：`35`
最终状态：`completed`
最终卡片：`om_xdemo_2`

## 2. 关键读回

### 问题卡片

- interaction：`acceptance_demo_3`
- status：`pending -> answered`
- question：`你喜欢咖啡吗？`
- choices：`喜欢`、`不喜欢`
- question card：`om_xdemo_4`
- outbox：`interaction:acceptance_demo_3:prompt`，`sent`
- Run：`waiting_input -> queued -> completed`

### 最终卡片

```text
很高兴你喜欢咖啡！☕ 有什么我可以帮你的吗？
```

- final outbox：`run:acceptance_demo_1:final`，`sent`
- `channel_interactions`：该测试 run 无 pending interaction
- `tenant_session_messages`：session `35` 新增 assistant turn `1`

## 3. 多轮恢复验证

另一个真实旅游任务 Run `acceptance_demo_5` 连续完成了 4 次问答恢复，产生 4 张真实 Feishu 等待卡片，验证了：

- 每次 resume 都创建新的 interaction，而不是重复消费旧 interaction；
- 旧 interaction 保持 `answered`，新 interaction 才进入 `pending`；
- worker 重启后 `ListAnsweredChannelInteractions` 能恢复原 Run；
- 每次问题卡片都真实发送成功。

该任务因真实 provider 持续进行旅游偏好澄清而保持等待，随后在验收库中标记为测试取消，不作为最终完成案例。

## 4. 真实问题与修复

验收初期暴露了两个只有真实环境才能发现的问题：

1. interaction 查询使用 `Take()` 同时手写 `LIMIT 1`，真实 MySQL 生成 `LIMIT 1 LIMIT 1` 语法错误；已删除手写 LIMIT，并补了真实 GORM CRUD 验证。
2. `RecoverPending` claim 后 Inbox 已是 `processing`，`MarkInboxProcessing` 只接受 `queued/retry`，lease 过期后无法接管；已允许 `processing + expired lease` CAS 接管，并补回归测试。

另一次合成入站使用不存在的 Feishu `open_message_id`，平台正确返回 `99992354`；改为非 reply 出站后，真实 Feishu create message 成功。这是验收夹具问题，不是产品路径错误。

## 5. UI 操作边界

本次桌面 Feishu 客户端在验收时被 macOS 锁屏，浏览器也没有登录态，因此按钮点击动作通过同一个服务端 `AnswerChannelInteraction` CAS 入口注入等价的 button answer；问题卡片发送、worker、真实 provider、加密 checkpoint、Run 恢复和最终 Feishu 卡片均为真实链路。按钮 UI 本身已由 `internal/channel/feishu` renderer/adapter 单测覆盖；解锁桌面后可补一次人工点击验收。
