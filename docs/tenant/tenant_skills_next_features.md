# Tenant Skills Next Features

## 已选择的下一阶段方向

下一阶段不再扩 tenant skills 的基础 runtime，而是做产品化闭环：

1. **Skill 版本历史与回滚**
   - 状态：DONE。
   - 目标：WebUI 能看到 tenant skill 历史版本，并一键回滚。
   - 价值：降低热加载配置错误导致的线上风险。
   - 验收：`POST /tenant/skills/rollback` 复制历史版本生成新最新版本；handler/service/golden 测试覆盖；runtime 下次请求读取最新 effective skill；audit log 记录 `tenant.skill.rollback`。

2. **WebUI 热加载验证按钮**
   - 状态：DONE。
   - 目标：Skills 面板中对选中 skill 执行一次只读验证，展示 effective source/version/fallback。
   - 价值：运营或管理员不需要看 telemetry 表也能确认配置是否生效。
   - 验收：Skills 面板展示版本历史，每个版本可 Validate；Validate 调用 `/tenant/effective-skills?skill_key=...&version=...` 并展示 source/version/enabled 状态。

3. **Telemetry Dashboard 按 skill 聚合**
   - 状态：DONE。
   - 目标：Observability 中按 `active_skill_source/version/fallback` 聚合 tool calls、错误和耗时。
   - 价值：发现某个 tenant skill 配置导致的失败率或慢请求。
   - 验收：Telemetry 面板按 skill/source/version/fallback 聚合事件数、错误、耗时和 tokens；前端单元测试覆盖聚合函数。

4. **移动端多端同步验收**
   - 状态：DONE。
   - 目标：把 WebSocket 多端同步加入 preprod acceptance。
   - 价值：补齐“换端登录看到历史和实时更新”的移动端体验闭环。
   - 验收：新增双设备 WebSocket 金标测试；`scripts/mobile-ws-sync-acceptance.sh` 和 `scripts/tenant-preprod-acceptance.sh` 默认执行。

## 当前已落地的验收资产

- `scripts/tenant-skills-runtime-smoke.sh`
- `scripts/tenant-chat-history-smoke.sh`
- `scripts/mobile-ws-sync-acceptance.sh`
- `scripts/tenant-preprod-acceptance.sh`
- `docs/tenant/tenant_preprod_acceptance.md`

这些脚本是后续功能上线前的固定回归入口。
