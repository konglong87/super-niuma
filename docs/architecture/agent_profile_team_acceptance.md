# Agent Profile / Team 验收记录

更新时间：2026-08-25

## 已通过的自动化 Gate

| Gate | 证据 | 结果 |
| --- | --- | --- |
| Profile domain/runtime | `go test ./internal/agentprofile ./internal/query ./internal/cli`；profile-less code 路径测试；chat/code context boundary 测试 | PASS |
| Team policy/runtime | `go test ./internal/agentteam -count=1`；nested policy 解码、bot loop、mailbox 幂等、coordinator-only、token ceiling、timeout | PASS |
| API/security | `go test ./internal/server ./internal/tenant -count=1`；Bearer、tenant scope、invalid draft、TeamRun pinned version 负向路径 | PASS |
| Persistence contract | `go test ./internal/storage/mysql -count=1`；migration/GORM 字段、唯一键、tenant scope、run/mailbox repository | PASS |
| WebUI unit/build | `npm --prefix web run test`（150 passed）；`npm --prefix web run build` | PASS |
| WebUI browser smoke | Playwright 9 passed，1 live test 因 API Server 未启动跳过；桌面和 390x844 移动视口检查 Profile/Team 工作台、Builder、导航和无重叠 | PASS |

## 本轮关键不变量

- Profile 只允许 published version 进入 runtime；chat profile 不能开启 workspace/git；bypassPermissions 和 unsandboxed command 被拒绝。
- Team policy 同时兼容历史 flat JSON 和 WebUI nested `orchestration`、`trigger`、`authorization`、`output` JSON。
- Team 成员/群组绑定替换会先归档旧 active 行，避免 unique key 冲突和旧 bot 残留路由。
- TeamRun 路径读取必须携带 `team_key + team_version`；跨 Team 访问返回 404。
- TeamRun 在 coordinator 前执行 member token ceiling；超限收敛为 partial，不再启动 coordinator。
- Team timeout 收敛为 `timed_out`；recorder 写入 started/finished timestamps 和使用量。
- Team validator 拒绝缺少 tenant/actor authorization、缺少 bot account、coordinator-only 与 member cards 冲突的配置。
- Router 忽略 bot 普通消息、`internal_only` 外部消息和不匹配的 group/thread/account；mailbox 使用 tenant/run/idempotency key 幂等。
- WebUI 仅展示安全 channel account metadata，不接收或保存凭据。

## 未完成的真实环境 Gate

以下 Gate 需要外部依赖，当前环境未宣称通过：

- `GOLANG_CC_MYSQL_E2E_DSN` 未配置，尚未执行隔离 MySQL migration、真实 readback、并发 publish 和 assignment 读回。
- 没有可用的真实模型 provider 配置，未做 create profile -> query -> session/trace metadata 的真实 provider 链路。
- 没有测试用飞书应用和稳定群组，未做单 bot、同群多 bot、Outbox receipt/readback 和重启恢复。
- coder Team workspace lock / 多 writer 真实文件写入尚未接入；V1 仍应视为显式 deferred capability。

替代证据：domain/repository/API deterministic tests、fake Team runner、fake channel router、WebUI mock E2E 和浏览器 desktop/mobile smoke。

## Topology impact

`Topology impact: updated`。本轮只复用并加固已登记的 `RT-BOUNDARY`、`RT-SUBAGENT`、`RT-PERSIST` 和 `RT-OUTPUT` 节点，没有新增 runtime package 或未登记因果边：

- `RT-BOUNDARY -> RT-PERSIST`：TeamRun pinned version、tenant scope、run path validation。
- `RT-SUBAGENT -> RT-PERSIST`：Team budget/timeout terminal state、started/finished usage metadata。
- `RT-OUTPUT`：WebUI Profile/Team workbench、run/replay and responsive smoke。

回滚路径：关闭 profile/team surface assignment 和 channel Team binding；保留历史 profile/team/run 数据，不 drop migration，不改变 profile-less CLI/TUI code path。

## 验收命令

```bash
go test ./internal/agentprofile ./internal/agentteam ./internal/channel/... ./internal/tenant ./internal/storage/mysql ./internal/server ./internal/query ./internal/cli -count=1
go test ./... -count=1
git diff --check
go run ./scripts/runtime-topology-check --base HEAD --working-tree --impact updated --blast-radius B4_PROTOCOL --reason "agent profile/team acceptance hardening reuses registered boundary, subagent, persistence, and output nodes"
npm --prefix web run test
npm --prefix web run build
npm --prefix web run test:e2e
```
