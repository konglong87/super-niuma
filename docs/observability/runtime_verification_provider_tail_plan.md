# Runtime 验证终态与 Provider 长尾闭环方案

状态：已完成
日期：2026-08-11

## 1. 目标与边界

本阶段解决两个已经由 60-run APG A/B 暴露的问题：

1. runtime trace 把任何历史测试失败永久聚合成 `tests_passed=false`，导致两个最终通过的用例被稳定误判为未验证；
2. 并行实验只证明 model wall 主导 p95 回退，尚未把 model wall 继续分解到 provider phase 的 p50/p95。

本阶段只修改观测、评测消费者、报告和文档。禁止修改 Agent loop、prompt、上下文装配、provider 请求行为、工具调度、权限、compact 或完成 gate。

## 2. 架构与影响评估

```text
runtime events
  -> traceQualitySignals（历史过程 + 最终验证终态）
  -> runtime-trace-v1 quality
  -> golang-cc baseline / APG benchmark
  -> verified rate + provider phase percentile
  -> keep / investigate / rollback 决策
```

- Topology nodes：`RT-OBSERVE`。
- Causal consumers：local/tenant trace、`runtime-trace-baseline`、APG benchmark、性能方案与 TODO。
- Blast radius：`B4_PROTOCOL`。只增加向后兼容字段；旧消费者可忽略，新消费者对旧 artifact 回退到旧口径。
- Protected invariants：历史测试失败不能被抹掉；最终验证必须来自已完成的测试调用；无测试任务继续由 completion evidence 判定；Agent 执行和模型上下文保持不变。
- 正收益：消除恢复成功场景的 verified 假阴性；直接量化 DNS/connect/TLS/TTFB/stream read 长尾。
- 潜在负作用：若“最终测试”定义过宽，可能把较窄测试覆盖误当全量验证。通过同时暴露 attempts、failed attempts、recovered failure 和 final pass，避免隐藏覆盖范围；APG task scorer 仍是任务成功真相。
- Token/turn/tool/latency：全部不变；只在导出和离线报告阶段聚合已有事件/span。
- 回滚：消费者恢复读取 `tests_run/tests_passed`，新增字段按未知 JSON 字段忽略。

现有 topology 节点和因果边已经覆盖这条链路，不新增 runtime 节点、持久化路径或控制面，因此 topology impact 为 `none`。

## 3. 验证终态语义

`tests_passed` 保持兼容但改为“最后一个已完成测试调用是否成功”。同时增加：

- `test_attempts`：已完成测试调用数；
- `failed_test_attempts`：失败测试调用数；
- `test_failure_recovered`：曾失败且最终测试成功；
- `final_verification_passed`：completion evidence 成立，且未运行测试或最终测试成功。

未完成的最后一次测试不能算通过。APG 和本仓 baseline 优先读取 `final_verification_passed`；旧 artifact 没有该字段时继续使用旧公式，保证历史报告可读。

## 4. Provider 长尾报告

APG benchmark 在 before/after 两侧按 phase 名称统计 count、work p50 和 work p95，至少覆盖：

- `model.phase.request.build`
- `model.phase.http.dns/connect/tls`
- `model.phase.http.wait_first_response_byte`
- `model.phase.stream.read`

phase span 存在父子重叠，因此这些数字用于归因，不能相加宣称等于 model wall。报告必须按相同 suite、case、model、runtime configuration 和 Build Identity 配对。

## 5. 清理策略

1. 先使用 APG checksummed cleanup plan/apply 回收未引用 runtime HOME；
2. Console daemon 必须处于停止或 stale 状态；
3. 只回收 terminal canceled job，且目录内不得存在 canonical report；
4. completed job 的 report、workspace、manifest 和显式 artifact 保留；SQLite 索引是可重建缓存，但本阶段不靠删除索引制造空间收益；
5. apply 后回查目标不存在、canonical report 数量与 SHA-256 清单不变，并记录 cleanup audit。

## 6. 实施与验收

1. 红测证明“失败测试 -> 成功测试”当前仍为未通过。
2. 实现终态字段与旧 artifact 兼容消费。
3. APG benchmark 增加 phase percentile，并用真实 60-run report 重算。
4. 清理 canceled Console jobs 和可回收 runtime HOME，保留审计记录。
5. 同步性能结论、TODO 和陈旧审计摘要。
6. 执行直接包测试、race、两个仓库全量测试、APG P0 dry-run、sensitive scan、topology check、`git diff --check`。

## 7. 实施结果

- runtime quality 已同时输出 `test_attempts`、`failed_test_attempts`、`test_failure_recovered` 和 `final_verification_passed`；Trace WebUI、Swagger、前端生成类型、baseline 与 APG benchmark 已同步，旧 artifact 保持 fallback。
- APG benchmark 已输出 provider phase count/p50/p95 和 before/after delta，并用 Agent Build Identity 阻止不同构建产物被当成同一 A/B 对。
- 历史 60-run report 重算后，serial -> parallel 的 task p95 为 `36854 -> 43286ms`，model p95 为 `35915 -> 42145ms`，tool p95 为 `1305 -> 1264ms`。最大 phase 变化来自 `model.phase.stream.read` p95 `5094 -> 6352ms`；TTFB p95 仅 `1499 -> 1574ms`，connect/TLS 和 request build 不构成主要瓶颈。
- 旧 60 个 metadata-only artifact 没有原始事件，不能无损反算新增终态，所以原文件仍显示 24/30 verified；这不应伪造改写。APG task oracle 两侧仍是 30/30，假阴性固定来自 `go-retry-policy` 与 `go-string-normalize` 的修复后成功场景。
- APG checksummed cleanup 删除两个无 canonical report 的 canceled Console job，回收 `41,657,089` bytes；清理前后 214 个 canonical report 的路径和逐文件 SHA-256 完全相同。剩余约 996 MiB Console 内容是受保护证据，不按体积误删。

最终决策：保留本次观测与评测修复；不提高并行度，不修改 Agent/provider 运行行为。下一轮性能实验必须先建立受控 provider 长尾样本，并继续用 task success、verified、token、turn、tool call 和 p95 共同判断。
