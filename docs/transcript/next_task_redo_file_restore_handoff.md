# 待启动任务交接：redo 带文件还原（best-effort + tombstone 守门）

> 日期：2026-07-23
> 用途：**回顾/衔接用**。这是「为 redo 增加文件还原」这一尚未开始的任务的交接提示词，
> 记录于 transcript v2 收尾（`v0.1.36-go`）之后。开新会话时可直接复制下方代码块。
> 背景与设计索引见本目录 [README.md](README.md)。

```
【承接上次会话】

项目：/path/to/golang-cc（用 Go 实现的通用 Agent runtime）
当前 main=49ab49b4，最新 tag v0.1.36-go。transcript v2 消息图 + 非破坏性 rewind 已
默认开启并全链路验收/发布完毕。

上次工作进展：
- 已完成：P2 阶段 A–F（v2 消息图、leaf-walk resume、非破坏 rewind/redo、branches/
  compare、messageId 键控文件历史、原版 CC import）已实现并【默认开启】；两处 bug 修复
  （闸门 <system-reminder> 不再落盘/不进 rewind 候选；/rewind 后不分叉→turn 起点系统性
  重算 recorder leaf）；真机端到端验收通过、go test ./... 全绿；文档已整理（docs/transcript/README.md）。
- 当前状态：无进行中任务。唯一待办＝本次目标：redo 带文件还原。

本次任务：为 redo 增加【文件还原】能力（best-effort + tombstone 守门）
现状：Store.Redo（internal/session/store.go:1027）只 append 一条 branch_head 移动
对话指针，【不还原文件】。所以 redo 回旧分支后，对话回来了但磁盘文件仍停在 rewind 还原后
的状态（分裂态）。目标是让 redo 也把文件重放成目标分支末端状态。

关键信息：
- 相关文件：
  · internal/session/store.go —— Store.Redo(1027)、rewindConversationV2(962，rewind 的
    非破坏+文件还原样板)、parseFileChanges(1313)、runFileRestoreTransaction(1372，补偿事务)、
    restoreFileChange(1550，【把文件还原到 change.Before】)、precheckRestore(1436，缺 blob
    时返回“…file history reclaimed…”tombstone 错误)、GCFileHistory(628)、fileChangeSnapshot 结构。
  · internal/session/graph.go —— CurrentChain/ChainToLeaf/ActiveLeaf/Leaves/commonPrefixLen(132)。
  · internal/cli/cmd_session.go —— sessionRedoCommand；internal/cli/interactive.go —— /redo 斜杠。
  · internal/session/rewind_v2_test.go / file_history_v2_test.go —— 测试样板（v2Store、recordTurn、
    mkBlob、fileChangeJSON、fileSize）。

- 技术要点（务必按此实现）：
  1) 两链差集算法（泛化 rewind 的“回退前链 vs 目标链”）：
     common = commonPrefixLen(fromChain=当前链, toChain=目标分支链)；fromSuffix/toSuffix = 各自
     common 之后的部分。文件还原集合 =
       · 【仅在 fromSuffix 出现的 path】→ 还原到分叉点状态 = 该 path 在 fromSuffix 里最早的
         before（parseFileChanges 语义）；
       · 【在 toSuffix 出现的 path】→ 还原到目标分支该 path 的【最终 after】。
     建议抽一个 v2FileRestores(fromChain, toChain) []fileChangeSnapshot，让 rewind 与 redo 共用。
  2) restoreFileChange 只还原到 snapshot 的 Before* 字段。要“还原到目标分支的 after 状态”，需把
     目标 file_change 的 After* 投影成 Before*（afterAsBefore：Before=After、BeforeExists=AfterExists、
     BeforeSnapshotPath=AfterSnapshotPath、BeforeMode/…=After 对应字段；无 AfterMetadata，metadata
     降级可接受）。
  3) 【核心难点 → 决定 best-effort + tombstone】目标分支的“最终 after”未必可取：
     · P1-4 turn 级去重：一个 turn 内同一 path 只有【首次】改动存完整 after blob，后续是轻量条
       （无 after blob）→ 分支末端 after 可能没落盘；
     · GCFileHistory 保留窗口按【当前链】turn 计，被放弃分支的 blob 会更早回收 → redo 命中 tombstone
       概率高于 rewind。
     因此：能还原的还原；after 内容不可取（blob 缺失/被回收/轻量条无 after）时【明确报 tombstone 错误
     或跳过并汇总告警，绝不静默截断/写坏文件】。复用 precheckRestore 的可读性预检 + runFileRestoreTransaction
     的补偿事务（全有或全无、失败回滚）。
  4) 复用补偿事务 runFileRestoreTransaction（precheck→快照 undo→逐个还原→提交，失败全回滚）。
  5) CLI/交互：决定 redo 文件还原是【默认开】（与 rewind 对称，推荐）还是【--files 选填】；同步
     session redo / /redo 输出文案与（如需）golden。redo 之后 recorder leaf 由 turn 起点系统性
     SyncLeafFromDisk 自动对齐（已实现，无需再补）。

- 可选前置增强（先评估要不要做，做了能显著降低 redo 命中 tombstone 的概率）：
  · 让“分支末端的 after”至少存一份 blob（放宽 turn-dedup 对末次 after 的丢弃）；
  · “旧分支只要还存在就不 GC 其 blob”（GCFileHistory 可配置保留被引用分支）。
  若不做前置增强，则纯 best-effort + tombstone；两条路请先呈现权衡再定。

验收要求（每步）：gofmt -w、go build ./...、相关包 go test -count=1、go vet、git diff --check；
改 CLI 记得同步 golden（internal/cli/testdata/golden/help.txt）；测试用
t.Setenv("GOLANG_CLAUDE_CODE_CONFIG_DIR", t.TempDir()) 隔离。要有：
- 单测：redo 到含文件改动的旧分支 → 文件正确重放到该分支末端；命中被回收 blob → 报清晰
  tombstone、补偿事务不留半完成、磁盘/transcript 不被破坏；rewind 复用同一 v2FileRestores 后
  既有 rewind 文件测试仍全绿。
- 真机端到端复验（可选、需我确认再花钱）：隔离 config（从 ~/.go-claude/settings.json 复制
  sensenova-deepseek-v4-flash provider、勿回显/提交明文 key）+ -p/-r 跑一轮，注意 -p 每轮新进程
  经 OpenRecorder 已对齐 leaf，真机主要验“文件确实随 redo 重放”。

工作流与 Git 规范：从 main 新开分支（如 feature/redo-file-restore）开发；提交身份
konglong87 <developer@example.com>、中文 commit、【绝对禁止任何 AI 署名/co-author/水印】；
遵守架构分层（Types→Config→Repo→Service→Runtime→UI）；密钥禁止明文入库。
完成后落文档到 docs/transcript/（并在 docs/transcript/README.md 索引里补一行），更新 CHANGELOG，
按需打 v0.1.37-go tag。

请先呈现方案与权衡（默认开 vs --files；是否做前置增强），与我确认后再实现。
```
