# niuma 文档中心

这里是 niuma 的文档入口。根目录只保留稳定入口和生成文件；专题文档按域放在子目录中，避免顶层 `docs/` 继续堆积。

## 公告与传播

| 内容 | 入口 |
| --- | --- |
| niuma 公开发布文章 | [2026-10-13 Public Release](announcements/2026-10-13-public-release.md) |
| Show HN / Reddit 发帖草稿 | [Launch draft](announcements/2026-10-13-show-hn.md) |

## 面向新用户的入口

| 我想做什么 | 推荐阅读 |
| --- | --- |
| 第一次安装并跑通一个任务 | [5 分钟上手](getting-started.md) |
| 找一个可以直接复制的任务 | [使用场景](use-cases.md) |
| 配置兼容 API 或多模型 | [Provider 配置](providers.md) |
| 连接外部工具和服务 | [MCP 扩展](mcp.md) |
| 查看常见问题 | [FAQ](faq.md) |
| 参与讨论或贡献 | [社区与贡献入口](community.md) |
| 了解公开方向 | [Roadmap](roadmap.md) |

## 快速路径

| 我想做什么 | 推荐阅读 |
| --- | --- |
| 先跑起来 | [../README.md](../README.md) |
| 看分模式、分场景的使用说明（TUI / CLI / agent-webui） | [usage/README.md](usage/README.md) |
| 在桌面 V2 创建多 Agent Team 并运行 | [Team 编排入门](usage/desktop-team-workflows.md) |
| 配置桌面宠物、背景和视觉外观 | [桌面个性化指南](usage/desktop-personalization.md) |
| 使用和验收 Computer Use | [Computer Use 使用说明](usage/computer-use.md) |
| 用最小工具和显式上下文运行 CLI/TUI | [usage/bare.md](usage/bare.md) |
| 装上并跑起来（源码安装 / 预编译产物 / 只构建） | [../README.md#安装](../README.md#安装) |
| 查会话存档：transcript / checkpoint / resume / inspect | [session_quickstart.md](session_quickstart.md) |
| 了解 TUI / CLI / API Server 运行方式 | [architecture/runtime_modes.md](architecture/runtime_modes.md)、[architecture/runtime_message_flow_and_closure.md](architecture/runtime_message_flow_and_closure.md)、[architecture/go_claude_agent_capability_boundaries.md](architecture/go_claude_agent_capability_boundaries.md)、[architecture/computer_use_integration_plan.md](architecture/computer_use_integration_plan.md)、[tui/tui_workbench_progress.md](tui/tui_workbench_progress.md)、[tui/tui_display_timeline_architecture_plan.md](tui/tui_display_timeline_architecture_plan.md)、[tui/subagent_progress_ux_fix_plan.md](tui/subagent_progress_ux_fix_plan.md) |
| 看会话体验、多模态、子代理与 TUI 的统一完善清单 | [architecture/product_experience_completion_checklist.md](architecture/product_experience_completion_checklist.md) |
| 验收 TUI 显示和工具进度 | [tui/tui_semantic_pty_acceptance_plan.md](tui/tui_semantic_pty_acceptance_plan.md)、[tui/tui_tool_progress_acceptance.md](tui/tui_tool_progress_acceptance.md) |
| 给前端或 App 调 API | [api_server.md](api_server.md)、[api/mobile_chat_p0_technical_plan.md](api/mobile_chat_p0_technical_plan.md) |
| 启用 MySQL 多租户和 tenant skill runtime | [tenant/multi_tenant_mysql.md](tenant/multi_tenant_mysql.md)、[tenant_runtime/tenant_skill_package_runtime_plan.md](tenant_runtime/tenant_skill_package_runtime_plan.md)、[tenant_runtime/ai_study_skill_self_publish_plan.md](tenant_runtime/ai_study_skill_self_publish_plan.md) |
| 看 WebUI、Trace 和观测 | [webui/webui_frontend.md](webui/webui_frontend.md)、[observability/trace_viewer.md](observability/trace_viewer.md)、[observability/runtime_performance_observability_plan.md](observability/runtime_performance_observability_plan.md) |
| 查 Agent Profile、DM、Group、Team 和 worker 术语 | [architecture/agent_profile_channel_glossary.md](architecture/agent_profile_channel_glossary.md) |
| 用 WebUI 创建 Profile-Agent 并管理 Feishu worker | [usage/profile-agent-provisioning.md](usage/profile-agent-provisioning.md) |
| 启动 Web Agent、跑滚动压测或真实 E2E | [web_agent/web_agent_scripts.md](web_agent/web_agent_scripts.md) |
| 启动和排查多 bot Feishu worker | [architecture/channel_worker_screen_operations.md](architecture/channel_worker_screen_operations.md) |
| 看 Web Agent 页面真机 E2E、截图和使用说明 | [中文](web_agent/web_agent_real_e2e_usage_zh.md)、[English](web_agent/web_agent_real_e2e_usage.md) |
| 看 Web Agent session/conversation 分层模型 | [web_agent/web_agent_session_conversation_model.md](web_agent/web_agent_session_conversation_model.md) |
| 看 Web Agent 生命周期、runner 接入和全链路修复计划 | [web_agent/web_agent_lifecycle_runner_fix_plan.md](web_agent/web_agent_lifecycle_runner_fix_plan.md) |
| 看 Web Agent 长回复流式可靠性和半截回复彻底修复方案 | [web_agent/web_agent_streaming_reliability_plan.md](web_agent/web_agent_streaming_reliability_plan.md) |
| 看 Web Agent 与 TUI 显示效果差距 | [web_agent/tui_display_parity_gap_analysis.md](web_agent/tui_display_parity_gap_analysis.md) |
| 查 bug 修复历史和回归证据 | [bugs/README.md](bugs/README.md) |
| 设计 Agent 拉练平台和评分体系 | [testing/agent_proving_ground_scoring_design.md](testing/agent_proving_ground_scoring_design.md)、[testing/agent_eval_harness.md](testing/agent_eval_harness.md) |
| 看 Goal、Loop、Session Recap | [goal_mode/README.md](goal_mode/README.md)、[architecture/loop_scheduler_design.md](architecture/loop_scheduler_design.md)、[session_recap/session_recap_plan.md](session_recap/session_recap_plan.md) |
| 排查、复现和验证 Git commit/push 授权循环 | [architecture/shared_state_git_authorization_incident_playbook.md](architecture/shared_state_git_authorization_incident_playbook.md) |
| 分析 Git commit/push 授权死循环与彻底修复架构 | [architecture/shared_state_git_authorization_loop_root_cause_and_fix_plan.md](architecture/shared_state_git_authorization_loop_root_cause_and_fix_plan.md) |
| 看 transcript schema、resume 隔离和原版 Claude Code 兼容边界 | [transcript/transcript_schema_isolation_and_resume_plan.md](transcript/transcript_schema_isolation_and_resume_plan.md) |
| 看 skills/plugins 和 subagent | [skills/skills_progressive_loading.md](skills/skills_progressive_loading.md)、[subagent_multiagent/agent_authoring_guide.md](subagent_multiagent/agent_authoring_guide.md) |
| 对照书籍学习智能体设计模式 | [agentic_patterns_learning/README.md](agentic_patterns_learning/README.md) |
| 看兼容性、差距和 TODO | [compatibility_matrix.md](compatibility_matrix.md)、[compatibility_deep_review.md](compatibility_deep_review.md)、[todo.md](todo.md) |
| 看全项目独立审计结论和修复 backlog | [audit/2026-07-25_capability_audit.md](audit/2026-07-25_capability_audit.md) |
| 回顾重要发布的范围、证据和复盘 | [archive/2026-07-30_golang_cc_rename_release.md](archive/2026-07-30_golang_cc_rename_release.md) |
| 准备把仓库公开或发布开源版本 | [deployment/open_source_release_checklist.md](deployment/open_source_release_checklist.md) |

## 分类目录

| 分类 | 内容 |
| --- | --- |
| [usage/](usage/) | 分模式（TUI / CLI / agent-webui）、分场景的使用说明，每个场景配命令、界面说明和要点。 |
| [api/](api/) | 移动端、OpenAI-compatible 调用方和 API 形态专题。主 API 文档仍保留在 [api_server.md](api_server.md)。 |
| [agentic_patterns_learning/](agentic_patterns_learning/) | 《智能体设计模式（双语版）》与 niuma 源码实践的对照学习材料。 |
| [architecture/](architecture/) | 运行模式、单条消息 runtime flow、闭环 gate、Computer Use、niuma agent 能力边界、subagent runtime、Loop scheduler、上游对齐等架构级说明。 |
| [audit/](audit/) | 全项目独立审计报告与修复 backlog（带 `file:line` 证据、稳定条目 ID、验收标准和已验证/未验证边界）。 |
| [archive/](archive/) | 已完成的重要发布归档，记录实际范围、提交/tag、验证证据、已知边界和过程复盘。 |
| [auto_memory_plan/](auto_memory_plan/) | 自动记忆（auto memory）方案。 |
| [bugs/](bugs/) | Bug 修复历史、根因证据、验证命令和剩余风险台账。 |
| [deployment/](deployment/) | 远程部署、生产上线和开源发布 checklist。 |
| [goal_mode/](goal_mode/) | Goal Mode 基线设计、API/Mobile 扩展、优化方案和进度。 |
| [manual_testing/](manual_testing/) | 人工验收和 WebUI prompt context 检查清单。 |
| [observability/](observability/) | Trace Viewer、端到端性能与质量可观测方案、telemetry/trace 可视化说明、system prompt 快照设计。 |
| [optimization/](optimization/) | prompt engineering 优化方案（v1/v2）。 |
| [prompt_logic/](prompt_logic/) | prompt 加载、session prompt 拼装、memory/cache，以及 [Claude Code 原版 vs niuma 深度差异诊断](prompt_logic/claude_code_vs_go_claude_deep_diagnosis.md)、[Claude Code 差距证据采集与修复进度](prompt_logic/prompt_gap_recovery_progress.md) 和 [Agent 修复一次成功率提升技术方案](prompt_logic/agent_repair_first_pass_success_plan.md)。 |
| [session_recap/](session_recap/) | TUI session recap 方案。 |
| [skills/](skills/) | skills/plugins 加载、运行时 frontmatter 和渐进式加载。 |
| [subagent_multiagent/](subagent_multiagent/) | subagent authoring 与 multi-agent parity。 |
| [tenant/](tenant/) | 多租户、tenant security、AI Study 接入、quota 和 tenant skill 后续专题。 |
| [tenant_runtime/](tenant_runtime/) | tenant skill package runtime、structured routing、热加载和 E2E 证据。 |
| [testing/](testing/) | MySQL E2E、agent eval harness、Agent 拉练平台评分体系。 |
| [transcript/](transcript/) | transcript schema、resume 路径隔离、原版 Claude Code 兼容和 sub-agent transcript 关联。 |
| [tui/](tui/) | TUI 交互专题，包括 [Workbench 升级进度](tui/tui_workbench_progress.md)、[产品级 UI 优化方案](tui/tui_product_ui_refinement_plan.md)、[DisplayTimeline 显示架构彻底修复方案](tui/tui_display_timeline_architecture_plan.md)、[语义连续多轮真实 PTY 验收方案](tui/tui_semantic_pty_acceptance_plan.md)、[工具进度验收流程](tui/tui_tool_progress_acceptance.md)、[transcript 底部遮挡修复方案](tui/tui_transcript_bottom_chrome_fix_plan.md)、[视觉行高遮挡修复方案](tui/tui_visual_line_height_overlap_fix_plan.md) 和 [渲染预算架构彻底修复方案](tui/tui_render_budget_architecture_fix_plan.md)。 |
| [update_strategy/](update_strategy/) | 更新策略。 |
| [webui/](webui/) | 独立 WebUI 前端工程和 Mobile Chat Lab。 |
| [web_agent/](web_agent/) | Web Agent 启动脚本、Codex-grade 产品架构、session/conversation 分层模型、真实 E2E 使用说明、生命周期/runner 修复计划、长回复流式可靠性方案、TUI 显示差距分析和截图证据。 |

## 稳定入口

| 文件 | 内容 |
| --- | --- |
| [api_server.md](api_server.md) | Gin API Server、OpenAI-compatible Chat Completions、Mobile Chat API、tenant admin、audit、telemetry、observability。API 变化必须同步这里。 |
| [compatibility_matrix.md](compatibility_matrix.md) | 行为金标矩阵、测试证据、P0/P1 backlog。 |
| [compatibility_deep_review.md](compatibility_deep_review.md) | plugins/skills、工具执行、流式对话、权限、沙箱、会话等深度核查。 |
| [todo.md](todo.md) | 当前 TODO、状态、优先级、验收标准和剩余对齐项。 |
| [session_quickstart.md](session_quickstart.md) | 一个 session = 一个 `.jsonl` transcript；checkpoint / resume / inspect / rewind / redo / branches 的图解与命令速查。根 README 直接引用这里。 |
| [web_search.md](web_search.md) | WebSearch 工具的默认端点、配置覆盖和网络边界。 |
| [deployment/open_source_release_checklist.md](deployment/open_source_release_checklist.md) | 首次公开或发布前的许可证、脱敏、历史、供应链、安装与治理检查。 |

## 其他顶层文档

这些是单次专题文档，按上面的「维护规则」本该收进对应子目录，但已被提交、外部引用和 git 历史指向，
移动的收益不抵改链接的风险。先在这里登记，以后连带相关整理一起搬（AUDIT-P1-34）。

| 文件 | 内容 |
| --- | --- |
| [loop_guard.md](loop_guard.md) | 循环熔断怎么判定"死循环"并早停；背景见 [bugs/README.md](bugs/README.md) 的 BUG-2026-07-23-001。 |
| [provider_neutrality_plan.md](provider_neutrality_plan.md) | provider 中立化清理方案：把 Anthropic 专属假设从通用路径上摘掉。 |
| [askuserquestion_interactive_design.md](askuserquestion_interactive_design.md) | AskUserQuestion 真交互设计（已实施）。 |
| [askuserquestion_interactive_plan.md](askuserquestion_interactive_plan.md) | 上一条的逐步实施计划（checkbox 形式）。 |
| [tui_inline_code_color_fix.md](tui_inline_code_color_fix.md) | TUI inline code 颜色过亮的修复方案。 |
| [tui_viewport_overlap_fix.md](tui_viewport_overlap_fix.md) | TUI 回复末尾被工具栏遮挡的根因分析与修复方案。 |
| [tui_viewport_overlap_fix_plan.md](tui_viewport_overlap_fix_plan.md) | 上一条的实施计划。 |

## 生成文件

| 文件 | 说明 |
| --- | --- |
| [swagger.json](swagger.json) | OpenAPI JSON，来自 `swag init` 自动生成。 |
| [swagger.yaml](swagger.yaml) | OpenAPI YAML，来自 `swag init` 自动生成。 |
| [docs.go](docs.go) | Swagger Go embed 文件，来自 `swag init` 自动生成。 |

## 维护规则

- 新增文档优先放到对应子目录，并在本索引补入口。
- `docs/api_server.md`、`docs/compatibility_matrix.md`、`docs/todo.md` 和 Swagger 生成文件保持稳定路径，避免破坏开发流程和外部引用。
- API 有变化时同步 [api_server.md](api_server.md)，并重新生成 Swagger。
- MySQL schema、mobile API、权限/沙箱、stream-json、prompt/cache、Goal、Loop、tenant runtime 等高风险链路变化时，同步相关专题文档和验证证据。
- 如果某项兼容能力仍未对齐所声称的边界，必须在 [todo.md](todo.md) 或兼容性文档里明确边界，不用“完成”掩盖差距。
