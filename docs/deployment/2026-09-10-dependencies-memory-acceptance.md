# 2026-09-10 依赖修复与 Memory 专项验收

状态：第 1、2 项完成。历史迁移与最终发布留待 2026-09-11，不改变仓库可见性、不创建 Release/tag、不审核 demo 图片。

## 范围与架构

- 基线 `d3c0e076`；沿用已批准的候选分支，保留用户已有修改，不创建新分支/worktree。
- Go、Web、memory 验收分别提交，相关测试通过后推送；原生/非原生最终 release 验收不在今天执行。
- Go 依赖沿 RT-BOUNDARY（HTTP/WebSocket、附件）及 RT-MODEL（provider 网络）消费；不改变业务接口、schema、系统提示词或控制门。保守记为 B3_GLOBAL_RUNTIME，现有拓扑节点与边仍准确（impact none）。
- 正收益为修复已报告依赖问题；风险为传递依赖兼容性及 Go 最低版本变化。完整测试、直接消费包回归、vet、漏洞复扫验证；若失败，回滚对应独立依赖提交，不用漏洞忽略规则掩盖问题。
- Web 仅开发/构建链依赖，RT-OUTPUT 通过生成物消费；B0_LOCAL，不改变运行时 API。用 npm ci、526 项基线测试、build、typecheck、API 类型生成及全量/生产依赖 audit 验证。
- Memory 沿 RT-PROMPT -> RT-CACHE/RT-MODEL 及 RT-TOOLS -> RT-PERSIST 验证；先建立失败测试和证据，再决定是否需要修改 runtime。不会为了通过一个验收样本扩大系统提示词或权限。

## 依赖策略

优先使用公告对应的修复版本，不做全量最新升级、不使用 npm audit fix --force。

- gorilla/websocket 1.5.0 -> 1.5.3。
- x/image 0.28.0 -> 0.45.0。
- x/crypto 0.53.0 -> 0.56.0；该版本要求 Go 1.26，go.mod 下界同步为 1.26.0，构建/CI 继续固定 1.26.6。
- x/mod 0.37.0 -> 0.40.0；由模块解析器带入的必要 x/* 传递依赖一并验证。
- Vitest 最低范围提升到已修复的 4.1.11；其他开发依赖通过兼容范围内的锁文件修复，变更清单完成后记录。
- 无修复版本的弃用包公告必须区分模块存在与实际 import/调用，不能仅为清空报告而忽略整个模块。

## Memory 验收方案

使用全局 settings 中实际名称 `sensenova-glm-5.2`（模型 `glm-5.2`）。不修改全局 settings；只将这一路所需配置写入隔离的 0600 配置文件，敏感文件与原始请求证据仅保留在 0700 的本机忽略目录，不放入 `/tmp` 或 Git。

1. 新进程、新 HOME、独立项目 A/B、独立配置根，拒绝读取开发者真实记忆。
2. 真实模型写入长期项目偏好；检查实际文件、索引链接与 frontmatter，不以最终回答作为落盘证明。
3. 另起进程，用没有泄露答案的任务验证召回；检查实际发往模型的请求中包含对应记忆正文，区分主动预载与模型工具读取。
4. 项目 B、无索引条目、失效索引、忽略记忆及越界路径不应导致正文预载；只读任务前后文件哈希保持一致。
5. 固定合成数据验证候选上限、单文档/总预算和 UTF-8 边界；测量真实装配字节，而不是只检查存在截断标记。正文预算、定位标记和 prompt 包装开销分别说明。
6. 主流 coding/chat/bare 模式及 memory unit/race 回归，记录 turn、工具调用、tokens、prompt bytes、耗时及已知限制。

## 证据

本轮证据目录为 `.gstack/security-reports/2026-09-10-deps-memory/`。公开报告只记录合成场景、脱敏结果与计数，不复制真实配置、密钥或无关会话内容。完成后追加结果和下一步交接。

## Go 修复结果

- Go 1.26.6 的完整 `go test ./... -count=1`、`go vet ./...`、拓扑检查通过。
- `govulncheck ./...` 与 verbose 复扫：0 可达漏洞、0 已导入包漏洞；仍显示 GO-2026-5932（x/crypto/openpgp 弃用公告）作为未使用模块内容。`go list -deps ./...` 确认没有 openpgp 导入，没有配置扫描忽略项。
- 4 个主动升级与解析器必需的 6 个 x/* 传递升级，共 10 个模块版本变更；不改 handler、协议或 provider 逻辑。
- 本提交没有增加 prompt bytes、模型 turn、工具调用或新的 gate；现有 prompt/cache/loop 回归全部包含在全仓测试中。没有据此宣称网络延迟或模型效果有性能提升。
- `go.mod` 最低版本改为 1.26.0，实际支持/验收的安全工具链为 1.26.6，与 CI 和 `.tool-versions` 一致。旧的 Go 1.25 测试记录仅代表旧候选。

## Web 修复结果

- Vitest 及其配套包统一为 4.1.11，Redocly 1.34.20、brace-expansion 2.1.4、js-yaml 4.3.2、nanoid 3.3.18、PostCSS 8.5.28、undici 7.29.1；共 16 个包位置版本变化，均为开发链及必要传递依赖，生产依赖版本不变。
- Node 22.23.1 执行 `npm ci --ignore-scripts --legacy-peer-deps` 后，57 个文件 / 526 项测试、build、typecheck 全部通过。
- API 类型重新生成没有差异；全量与 `--omit=dev` npm audit 都为 0。没有使用 force、overrides 或 audit 忽略项。

## Memory 修复与真实验收结果

- 项目 memory 的索引、frontmatter 预览和召回正文共用已打开的 `os.Root`；
  越界链接、替换后的越界 symlink 和跨 workspace 读取均 fail closed；根内
  相对/绝对 symlink 保持兼容。单次 preview/body read 有 25 KiB 上限，内容
  仍受 200 行和 UTF-8 边界保护。
- frontmatter summary 改用已有 `yaml.v3`，支持 folded/quoted top-level
  `name`/`description`，不让 nested metadata 覆盖顶层字段；malformed/non-scalar
  summary 不参与召回。没有新增依赖、共享 prompt、权限、gate 或持久化格式。
- 单元、query/cli 相邻回归、race、全仓 `go test` 与 `go vet` 通过；拓扑检查
  以 `B2_MODE` / `updated` 声明通过。离线验收脚本单测 4 项通过。
- 使用隔离 settings 的真实 `sensenova-glm-5.2` / `glm-5.2` 跑 10 个场景：
  实际写入、冷进程召回、other-project、chat、bare、ignore、unindexed、
  missing、readonly-with-write-tools、预算均通过；全局 settings SHA 未变化。
  预算场景记录 2048 bytes 正文预算和 455 bytes read-pointer 开销。
- 真实验收证据位于 `<validation-root>/golang-cc-memory-20260910-run5`，未进入 Git；
  provider key 未出现在 prompt dump。证据是 runtime logical pre-provider
  request dump，不宣称 HTTP wire capture。

已知边界：ignore memory 仍加载 `MEMORY.md` 索引，只关闭额外正文召回；预算
不是最终请求的严格总字节上限，因为定位提示必须保留；召回仍是关键词匹配，
native slug 也不是碰撞证明。历史清理、GitHub 安全设置和最终跨平台 release
将在 2026-09-11 继续。
