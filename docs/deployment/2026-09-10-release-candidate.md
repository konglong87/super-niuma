# 2026-09-10 发布候选准备

状态：**候选源码与本地归档验证通过；正式发布仍为 BLOCKED，未创建 Release 或改变仓库可见性**。

通过验证的源码 commit：`5136b6ad60e6f218844880ca1fcf9dc98f93b975`。后续仅补充本报告的提交不是这批二进制的来源，产物身份始终绑定该源码 commit。

## 范围与架构

- 使用已批准分支 `dev-c/open-source-candidate-20260910`，起点 `d1b5e0ee11face6e140623fc4336ab2167961222`，不另建开发分支或 worktree。
- 图片由维护者确认为 demo，本轮不清点、不审核、不修改，不把豁免写成扫描通过。
- 测试夹具、Swagger 示例及文档中的个人目录、邮箱和真实网络报错地址改为合成值。文档中替换后的路径是脱敏定位示例，不是原始机器上的字面路径；原始证据不随公开文档分发。
- 保留公开模块名、GitHub 维护者身份，以及隔离/限流测试刻意使用的私网地址。
- `.tool-versions` 从 Go 1.26.5 对齐 CI 的 1.26.6，Node 保持 22.23.1。验收脚本改为按本次 session 查找 transcript，消除个人目录硬编码；缺失与重复匹配有独立测试。
- `.gitignore` 防止本地环境配置、根目录 memory/output 误入提交；不删除本地数据，不忽略通用 SQL/JSONL 测试资源。
- Topology impact: `none`；Blast radius: `B0_LOCAL`。涉及 RT-BOUNDARY 的 Swagger 示例与 RT-OUTPUT 测试数据，不改变 API 字段、运行时节点、因果边、prompt、memory、持久化或权限。
- 正收益是减少公开示例中的个人环境信息；风险是夹具期望漂移、生成文件不同步或忽略规则过宽，通过 Go/Web 回归、生成差异和 ignore 正反例验证。回滚仅 revert 本轮提交，不回滚用户工作区。

## 验证与证据

本轮独立证据目录为 Git 忽略的 `.gstack/security-reports/2026-09-10-candidate/`，目录权限 0700。原始扫描输出只留本机，公开报告仅记录脱敏分类与计数。

- fetch 全部已配置远端与 tags 后，扫描可见 refs。未获取的服务端隐藏 refs、其他人的 fork/clone 不在证明范围内。
- Gitleaks 扫描完整 Git 历史与候选文本树；TruffleHog 独立复核，关闭在线凭据验证，不向 provider 发送疑似凭据。
- 未命中不等于证明没有秘密；只有扫描退出码、完整性检查和命中复核均完成后才能记录结果。
- Go/Web 测试、Swagger 和 TypeScript 生成、拓扑检查、依赖复扫与候选 archive 检查结果完成后追加。
- 用户已有 `.superpowers/` 修改和未跟踪文件不纳入候选提交。图片不参与本轮审查。

### 已完成结果

扫描基线为 `d1b5e0ee` 的全部可见历史，以及本轮待提交文本树；原始 refs 清单与文件哈希清单留在本机证据目录。扫描器没有开启在线凭据验证。

| 检查 | 结果与边界 |
| --- | --- |
| 历史覆盖 | 非浅克隆，104 refs、1,875 commits；导出 10,714 个去重文本 blob、1,875 个 commit 元数据对象、37 个 annotated tag 对象，共 790,345,799 bytes；按扩展名排除图片，不读取其内容 |
| Gitleaks v8.24.3 原生 Git 扫描 | 完成；1,861 个有扫描内容的提交，4 命中，均为文档占位符或合成测试数据；不把该数字冒充总提交数 |
| Gitleaks 完整对象补扫 | 完成，26 命中：12 次历史文档占位符、3 次历史合成测试值、11 次 `go.sum` 中 credentials 模块的 h1 校验值；无已确认真实密钥 |
| TruffleHog v3.97.4 Git 扫描 | 完成，7 命中：4 个测试 URL、1 个文档示例 URL、2 个被 Box 检测器误识别的验收 Run/interaction ID |
| TruffleHog 完整对象补扫 | 完成，19 命中，均为上述示例/测试 URL 的历史版本或验收 ID；无新增真实凭据证据 |
| 待提交文本树 | 1,599 个文本文件；Gitleaks 4 项、TruffleHog 7 项，与上述分类相同；已识别的个人 home/slug、个人邮箱、真实网络报错 IP 已替换 |
| 历史隐私 | 3,554 个对象有路径或已知个人邮箱规则候选（含示例，不等于泄露次数）；1,794 个 author、1,809 个 committer 仍属个人/其他邮箱类别。没有执行历史重写 |
| Go 回归 | `go test ./... -count=1` 在 Go 1.26.5 与 1.26.6 均通过；Go 1.26.6 `go vet ./...` 通过，修改的 Go 文件 gofmt 检查无输出 |
| Web 回归 | Node 22.23.1：57 文件、526 测试通过，生产 build 通过；保留既有 bundle 大小提示 |
| 生成物 | 执行 swag 生成与 `npm run generate:api-types`；三个 Swagger 生成文件和 TypeScript 类型仅 cwd 示例变化，无 API 字段契约改变 |
| 辅助检查 | transcript 查找 3 测试、ignore 6 正例/7 反例、拓扑检查、`git diff --check`、离线验收 200 项均通过 |
| Go 漏洞复扫 | Go 1.26.6 govulncheck 仍列出 4 个有符号调用链的公告：GO-2026-4961、GO-2026-5061、GO-2026-6222、GO-2026-6278；不能把 JSON 模式退出码 0 当作零告警 |
| npm 复扫 | 8 个开发依赖告警（5 high / 3 moderate）；`--omit=dev` 为 0。未混入未经回归的依赖升级 |
| GitHub 管理设置 | `gh auth status` 未登录，无法核验保护规则、Private vulnerability reporting 或线上 CI；SSH 推送权限不能替代 API 验证 |

本阶段待办为依赖告警处置、历史身份/文本的明确公开或迁移决定、GitHub 管理侧核验、干净 commit 归档与跨平台运行证据；归档现已按下文完成，其余限制仍保留。普通扫描无法证明所有业务文本均无隐私，历史候选也不能仅凭规则命中就自动删除。

### 首次干净构建及发现

源码提交 `4ee588d7623f0c346c62f5246d0799db3c203fd2` 已推送候选分支。独立 detached clone 的 Go 1.26.6 全仓测试、vet、Node 22.23.1 的 526 项 Web 测试和生产构建均通过。提交后的文本树再次双工具复扫，命中仍为已复核的 4/7 项。

默认五平台均可构建，但此批归档 **不合格、不得发布**：Python tarfile 完整枚举发现 macOS tar 自动写入 `._*` AppleDouble 条目，且默认 archive ownership 使用宿主身份。普通 macOS `tar -t` 会隐藏 AppleDouble 条目，不能作为完整文件白名单检查的唯一证据。

修复集中在构建/打包层，不更改 runtime：build 默认 `-trimpath`；tar 使用 ustar、固定 UID/GID 0 与 root/空 owner，bsdtar 显式禁用扩展属性/ACL/flags 和 AppleDouble；zip 使用 `-X`。新增真实 tar/zip、合成编译器的五平台打包回归，macOS 主动注入合成 xattr，断言文件白名单、所有权、无额外 metadata 和 SHA256SUMS。该回归及 transcript 查找回归接入 offline acceptance。修复后必须从新的干净 commit 重建，不能仅手工删除旧归档的条目后声称源码已修复。

### 修复后干净候选验收

- 固定源码 `5136b6ad60e6f218844880ca1fcf9dc98f93b975`，版本标识 `0.0.0-rc.20260910.5136b6ad`（仅 linker 字符串，没有创建 tag）。
- 使用上述独立 clone 的 detached checkout，构建前后 `git status --porcelain` 均为空；未复用工作区二进制。Go 1.26.6 全仓测试及 202 项离线验收在该 commit 再次通过。
- 五个目标 `darwin/arm64`、`darwin/amd64`、`linux/amd64`、`linux/arm64`、`windows/amd64` 均完成构建及归档验证。Go/Node 版本与前述固定版本一致，Web 源码/lockfile 相比通过干净测试的首个候选未变。
- Python tarfile/zipfile 完整枚举逐包核验，无路径穿越、符号链接、多余文件、AppleDouble、PAX 或 zip 扩展元数据；每包四个文件：二进制、README、根 LICENSE、termenv LICENSE，文档/许可证字节与源码一致，五个 SHA256 全部匹配。
- 各二进制 `go version -m` 确认 Go 1.26.6、CGO=0、正确 GOOS/GOARCH、trimpath、完整 revision 和 `vcs.modified=false`；额外字节搜索未发现已识别的个人 home 前缀。此次证明来源可追溯，不声称多次打包字节完全相同。
- 原生 macOS/arm64 执行 `--version`，结果与候选版本一致；使用空 HOME/独立配置目录执行 `doctor`，API key 为 false、settings sources 为空，不读取开发者全局配置。
- 真实原生 server 绑定随机回环端口，用 curl 检查 `/livez`、`/readyz` 为 200；`/health` 无 token/错误 token 为 401、正确 token 为 200。server 已停止，未调用模型或连接测试数据库；这不是 MySQL/SSE/多租户全链路验收。
- 干净源码 `go install ./cmd/golang-cc` 到隔离 GOBIN 成功；没有声称远程版本化 go install 已可用。
- 候选提交后的最终 Git 双工具扫描覆盖 105 refs、1,877 commits；Gitleaks 4 项、TruffleHog 7 项，与已复核分类一致，无新命中。此前完整对象补扫提供祖先文件版本与元数据补充证据。
- 合格归档、校验和、五份 buildinfo、原生 smoke 和测试记录位于本机忽略目录 `.gstack/security-reports/2026-09-10-candidate/`；合格包子目录为 `archives-5136b6ad/`。`archives-4ee588d7/` 仅保留失败证据，不可发布。
- 非原生平台尚未运行二进制，GNU tar 分支也需 Linux CI 验证；不能用交叉编译成功替代这些证据。GitHub CLI 未登录，远端 CI 结果尚未核验。

下一步按顺序：独立处理 Go/Web 依赖告警并回归；维护者决定历史身份/业务文本的公开范围或新仓库迁移方案；核验 GitHub 安全设置与跨平台 CI；最后从最终批准的源码 commit 重新执行发布闸门。本轮无需且未进行任何图片审核。

## 干净构建与归档验收

1. 冻结并提交经过验证的候选源码，只推送当前候选分支，不创建或推送 tag。
2. 从该 commit 独立 clone、detached checkout；不是 worktree，不复用脏工作区产物。
3. 按 `.tool-versions` 与 CI 实际配置核对工具版本；有不一致则显式记录，不能把其他版本的测试当成固定版本验收。
4. 使用既有 `scripts/release.sh` 构建默认五个目标，独立指定新的 `DIST_DIR`；通过 `GOFLAGS=-trimpath` 去除编译路径。冻结 VERSION、REVISION 和 SOURCE_DATE_EPOCH，实际检查源码干净，不伪造 DIRTY。
5. 逐包核对路径安全、预期文件白名单、LICENSE/第三方许可证内容、SHA256SUMS、Go build metadata 和目标架构。原生平台执行版本及隔离配置 smoke；非原生平台运行验证需对应 CI。
6. 归档证据绑定被构建 commit；之后修改源码必须重新构建，不能把前一 commit 的产物冒充新版本。

## Git 历史与最终发布

普通候选分支仍继承全部祖先，修改 HEAD 不会删除旧文本或 author/committer 身份。将原仓库设为公开也会公开其他现存分支/tag，不能仅凭候选分支干净就公开原仓库。

历史处理在此仅制定方案，不执行重写或 force push：

1. 先确认对外 refs、需替换的文本/身份映射及可保留的贡献者归属。凭据若确认真实，先轮换再清理；不得尝试用扫描器登录验证。
2. 在受限持久目录保存 mirror 备份和 refs 清单，验证可恢复。备份本身包含原历史，不上传公开位置。
3. 仅在副本中使用成熟历史过滤工具，生成旧新 commit 映射，复扫所有拟公开 refs，检查文件树、tag、签名失效和许可证归属。
4. 由维护者单独批准迁移范围和切换方式。可选择新公开仓库的清洁快照，或协调协作者后替换原仓库历史；两者均不是当前分支的一次普通 push。
5. 完成候选 CI、漏洞复扫、GitHub 私密漏洞报告/保护规则/安全联系方式核验后，再决定 READY。远程版本化 `go install` 的本地 replace 限制继续明确告知，不声称已支持。

本轮不改变仓库可见性，不创建 Release，不推送 tag，不重写任何共享历史。
