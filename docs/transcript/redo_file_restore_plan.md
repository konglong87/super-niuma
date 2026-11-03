# redo 文件还原方案（best-effort + tombstone 守门）

**状态：已实现（`v0.1.37-go`）。** 本文是 redo 增加「文件还原」能力的设计与决策记录。三项待定决策均按推荐选项落地，见文末 [§10 实现记录](#10-实现记录已落地)。

## 1. 背景与问题

非破坏 rewind/redo 已默认开启（见
[transcript_v2_message_graph_and_nondestructive_rewind_plan.md](transcript_v2_message_graph_and_nondestructive_rewind_plan.md)）。
其中：

- **rewind**（[`rewindConversationV2`](../../internal/session/store.go)，store.go:962）既移动对话指针（append 一条
  `branch_head`），**也还原文件**（chain 差集 → `runFileRestoreTransaction` 补偿事务）。
- **redo**（[`Store.Redo`](../../internal/session/store.go)，store.go:1027）目前**只**append 一条
  `branch_head`（reason=redo）移动对话指针，**不还原文件**。

后果：redo 回旧分支后，对话回来了，但磁盘文件仍停在 rewind 还原后的状态 —— **对话与文件分裂**。
本方案让 redo 也把文件重放成目标分支末端的状态。

## 2. 术语：tombstone（墓碑）

> **tombstone = 「本应还原的内容已经找不回来了」的可识别标记。**

这是本方案借用的比喻，不是 Go 标准术语。redo 要把某文件还原成**目标分支末端**的内容，但有时那份内容的实体（内容寻址 blob 快照）已不在，transcript 里只剩一条「它曾经存在」的记录（如仅剩内容哈希 `AfterSHA256`，却没有对应文件体）。这块「只剩记录、没有实体」的残留就是 tombstone。

碰到 tombstone **绝不能静默当作空内容去写**，否则会用空文件覆盖用户的真实文件（静默写坏文件）。必须**明确识别**它，然后报错或跳过。

### 2.1 tombstone 的两个成因

- **成因 A — turn 级去重**（[`recordFileChange`](../../internal/query/query.go)，query.go:3506；去重注释 query.go:3513）：
  一个 turn 内同一 path 的**首次**改动存完整 before+after blob；**后续**改动走
  [`supersedeFileChangeForTranscript`](../../internal/query/query.go)（query.go:3544）→ after blob 被丢、
  仅留 `AfterSHA256`、置 `BeforeSuperseded=true`。因此**turn 内被多次改的 path，其 turn 末 after 从未落盘**。
  turn 边界会重置去重表（query.go:1229），故每个 turn 的首改都是完整条。

- **成因 B — GC 回收**（[`collectWindowedReferences`](../../internal/session/store.go)，store.go:700；
  当前链窗口 store.go:715-718）：v2 GC 的保留窗口按 **CurrentChain（当前链）** 计，
  被放弃分支引用的 blob 落在窗口外 → 更早被回收。这是当时**刻意**的省空间设计。
  redo 目标恰是「非当前链」，故命中 tombstone 概率天然高于 rewind。

## 3. 核心算法：`v2FileRestores(fromChain, toChain)`（rewind/redo 共用）

抽一个共用函数，泛化今天 rewind 的「回退前链 vs 目标链」差集：

```
common     = commonPrefixLen(fromChain, toChain)   // 分叉点，见 graph.go:132
fromSuffix = fromChain[common:]   // 要离开的分支（当前链）在分叉后的节点
toSuffix   = toChain[common:]     // 要去的目标分支在分叉后的节点

还原集 =
  ① path ∈ fromSuffix 且 ∉ toSuffix
       → 还原到分叉点：fromSuffix 里该 path【最早的 before】
         （= parseFileChanges 语义，撤销放弃分支对该 path 的改动）
  ② path ∈ toSuffix
       → 还原到目标分支末端：该 path 在 toSuffix 里【最后一条】file_change 的 after
         （afterAsBefore 投影后交给 restoreFileChange）
       （②对同一 path 优先级高于①）
```

- redo：`fromChain` = 当前 active 链，`toChain` = 目标分支链。
- rewind：`fromChain` = oldChain，`toChain` = newChain。

### 3.1 rewind 复用等价性（保证既有 rewind 文件测试全绿）

rewind 时 `newChain` 是 `oldChain` 的**前缀**（rewind 目标是当前链的祖先节点）：

- `common = len(newChain)` → `toSuffix` 为空 → 只剩规则①、且无「减 toSuffix」的减法；
- `fromSuffix` = `oldChain[len(newChain):]` = 今天的 `abandoned` 集合，逐节点相同；
- 规则①按 fromSuffix 首次出现顺序取 before = 今天
  [`parseFileChanges`](../../internal/session/store.go)(abandoned)（store.go:1313）的输出，**顺序与内容逐字节相同**。

因此 rewind 走 `v2FileRestores` 后，还原集与今天完全一致，rewind 既有文件测试可全绿。

### 3.2 `afterAsBefore` 投影

[`restoreFileChange`](../../internal/session/store.go)（store.go:1550）只还原到 `Before*` 字段。
要「还原到目标分支的 after 状态」，把目标 `file_change` 的 `After*` 投影成 `Before*`：

| 目标字段（Before*） | 来源（After*） |
|---|---|
| `Before` | `After` |
| `BeforeExists` | `AfterExists` |
| `BeforeSnapshotPath` | `AfterSnapshotPath` |
| `BeforeMode` / `BeforeModeKnown` | `AfterMode` / `AfterModeKnown` |
| `BeforeIsSymlink` / `BeforeLinkTarget` | `AfterIsSymlink` / `AfterLinkTarget` |
| `BeforeIsDir` | `AfterIsDir` |
| `BeforeMetadata` | —（无 `AfterMetadata`，metadata 降级可接受） |

## 4. tombstone 判定（正确性关键）

对 toSuffix 里某 path 的**最后一条** file_change：

| 情形 | 结论 |
|---|---|
| `AfterExists=false`（删除） | 可还原（删文件，无需 blob） |
| symlink / dir | 可还原（建链 / 建目录） |
| 普通文件，有 `AfterSnapshotPath` 或非空内联 `After` | 可还原（blob 若已被 GC → `precheckRestore` 二次兜底报错，store.go:1436） |
| 普通文件，`After=""` 且无 blob，且 `AfterSHA256==""` | **真·空文件** → 写空即可 |
| 普通文件，`After=""` 且无 blob，且 `AfterSHA256!=""` | **tombstone** → 真内容没落盘（成因 A 的 lite 条），绝不能写空 |

> **实现必需项**：给 [`fileChangeSnapshot`](../../internal/session/store.go)（store.go:136）**补 `AfterSHA256`
> 字段**（JSON 里本就有，只是当前结构体没解析），用它区分「真空文件」与「tombstone」。
> 旧 transcript 无此字段 → 反序列化为空 → 按「真空文件」处理，是安全的向后兼容默认。

## 5. 为什么是 best-effort，而不是「增强②」

**best-effort + tombstone 与「增强②」不是二选一，处于不同层：**

| | best-effort + tombstone | 增强② |
|---|---|---|
| 层次 | redo **执行时**的反应 | GC **回收时**的预防 |
| 作用 | 数据没了 → 明确报错/跳过，**不写坏文件** | 让数据尽量别被删，减少碰到「没了」 |
| 关系 | 兜底，**必需**（无论如何都要做） | 优化，**可选加在兜底之上** |

- **best-effort 是必需的地基**：即便做了增强②，仍需 tombstone 兜底 —— ②只能降低发生频率，不能消灭。
- **增强②只治成因 B（GC），对成因 A（turn 去重）完全无效** —— A 的数据压根没存过，留得再久也没有。
- **最常见用法「rewind 后马上 redo」中，GC 还没跑 → 成因 B 不发生 → 增强②零收益**；此时只有成因 A 会咬。
  而在 agent 编码工具里，一个 turn 内对同一文件多次 Edit 极常见，故成因 A 并不罕见 ——
  真正能治它的是**增强①（存分支末端 after）**，但①要动 query.go 热路径、改字节输出与 golden，风险最高。

### 5.1 增强②自身的代价

- 反转当时刻意的设计（store.go:715-718 专门让放弃分支的 blob 早回收）。
- 无字节上限时 → **无界增长**：非破坏模型里分支永不删除，每条放弃分支的末 N 轮 blob 被永久钉住，
  总量随分支数单调上涨。
- 有字节上限时 → **更糟**：放弃分支会与正在工作的当前链**抢字节预算**，可能为保住已放弃分支的快照，
  反而挤掉当前工作需要的 blob。

### 5.2 结论

1. **先做 best-effort + tombstone**（安全、正确、必需，永不损坏文件，清晰报错）。
2. **不做增强②**：只治不常见的成因 B，却要付「反转刻意设计 + 增长/抢预算」的代价，性价比低。
3. 若日后 redo 还原不可靠影响体验，该补的是**增强①**（治常见的成因 A），且**单独立项、慎测**，不混入本次。

## 6. 待定决策（实现前须确认）

- **决策一 · 默认开 vs `--files` 选填**：推荐**默认开 + `--conversation-only` 逃生口**，与 rewind
  的 `--conversation-only|--files-only` 对称。心智模型「redo 是 rewind 的逆」：rewind 动了文件，redo 就该动回来。
  `--conversation-only` 保留今天的纯指针行为（也用于绕过 tombstone 中止）。
- **决策二 · 命中 tombstone 的策略**：
  - **A 严格原子（推荐）**：目标分支存在任一不可还原的 after → precheck 阶段**整体中止 redo（含指针，什么都不动）**，
    报清晰 tombstone 错误，与现有事务「全有或全无、绝不写坏文件」一致，不留分裂态。想只回对话就显式加 `--conversation-only`。
  - **B 尽力而为**：能还原的还原，tombstone path 跳过并汇总告警，指针照移，磁盘停在部分态。
- **决策三 · 前置增强**：推荐**先不做**（理由见 §5）。

## 7. 复用与事务

复用 [`runFileRestoreTransaction`](../../internal/session/store.go)（store.go:1372）：
precheck → 快照 undo → 逐个还原 → commit，任一步失败**全回滚**，磁盘与 transcript 不留半完成态。
tombstone 的识别发生在 `v2FileRestores` 构建还原集时（precheck 只兜底「blob 被 GC」这一类），
两道关合起来保证：要么文件+指针一起到目标，要么什么都不动。

redo 之后，live recorder 的 active leaf 由每个 turn 起点的 `Recorder.SyncLeafFromDisk` 自动对齐（已实现）。

## 8. 代码落点（速查）

| 关注点 | 位置 |
|---|---|
| `Store.Redo`（待加文件还原） | internal/session/store.go:1027 |
| `rewindConversationV2`（非破坏 + 文件还原样板） | internal/session/store.go:962 |
| `parseFileChanges`（每 path 最早 before） | internal/session/store.go:1313 |
| `runFileRestoreTransaction`（补偿事务） | internal/session/store.go:1372 |
| `restoreFileChange`（还原到 Before*） | internal/session/store.go:1550 |
| `precheckRestore`（缺 blob 报错 / GC 兜底） | internal/session/store.go:1436 |
| `GCFileHistory` / 当前链窗口 | internal/session/store.go:628 / 715-718 |
| `fileChangeSnapshot`（待补 `AfterSHA256`） | internal/session/store.go:136 |
| turn 去重 / lite 条 | internal/query/query.go:3506 / 3544 |
| `commonPrefixLen` / `ChainToLeaf` / `Leaves` | internal/session/graph.go:132 / 102 / 144 |
| CLI `session redo` / `/redo` 斜杠 / help 文案 | internal/cli/cmd_session.go:356、internal/cli/interactive.go:1196 / 1665 |

## 9. 验收 / 测试计划

- **单测**：
  - redo 到含文件改动的旧分支 → 文件正确重放到该分支末端；
  - 命中被回收 blob / lite 末态 → 报清晰 tombstone，补偿事务不留半完成，磁盘/transcript 不被破坏；
  - rewind 复用同一 `v2FileRestores` 后，既有 rewind 文件测试全绿（§3.1 等价性）。
  - 隔离：`t.Setenv("GOLANG_CLAUDE_CODE_CONFIG_DIR", t.TempDir())`。
- **每步质量门**：`gofmt -w`、`go build ./...`、相关包 `go test -count=1`、`go vet`、`git diff --check`；
  改 CLI 同步 golden（internal/cli/testdata/golden/help.txt）。
- **真机端到端（可选、需确认再花钱）**：隔离 config，`-p`/`-r` 跑一轮，重点验「文件确实随 redo 重放」。

## 10. 实现记录（已落地）

三项待定决策（§6）均按推荐选项实现：

- **决策一：默认开 + `--conversation-only`。** `session redo <id> <leaf>` 与 `/redo <leaf>` 默认
  还原文件+移动指针；加 `--conversation-only` 只移动指针（今天的旧行为）。与 rewind 对称。
- **决策二：严格原子中止。** 目标分支任一 path 的末端 after 不可取 → 整体中止 redo（指针也不动），
  报清晰 tombstone 错误。复用 `runFileRestoreTransaction`「全有或全无、失败全回滚」。
- **决策三：先不做前置增强。** 纯 best-effort + tombstone；未反转 turn 去重与「GC 按当前链回收」两处刻意设计。

### 10.1 落地要点

- **共享差集函数** `v2FileRestores(fromChain, toChain)`（store.go）：`commonPrefixLen` 求分叉点，
  `fromSuffix`/`toSuffix` 分别取差集后缀。规则①（仅 fromSuffix 出现的 path → 分叉点 before，
  `parseFileChanges` 语义）＋规则②（toSuffix 出现的 path → 目标分支末端 after，经 `afterAsBefore` 投影，
  ②对同 path 优先于①）。`rewindConversationV2` 已改为调用它；rewind 时 `toSuffix` 为空 → 退化为
  `parseFileChanges(abandoned)`，输出等价，既有 rewind 文件测试全绿。
- **`afterAsBefore`**（store.go）：把 `file_change` 的 `After*` 投影成 `Before*` 交给 `restoreFileChange`；
  无 after metadata → metadata 降级（可接受）。
- **tombstone 判定**：给 `fileChangeSnapshot` 补 `AfterSHA256` 字段（JSON 本有、此前未解析），用于区分
  「真·空文件」（`After==""` 且 `AfterSHA256==""`）与「lite 条被截断」（`AfterSHA256!=""` 但无 blob/内联）→
  后者报「its final content … was not retained」；blob 被 GC 一类仍由 `precheckRestore` 兜底报「reclaimed」。
- **API**：`Store.Redo` 保持纯指针（向后兼容既有测试）；新增 `Store.RedoToBranch`（默认，文件+指针，事务化，
  含 recap 失效）；二者共用内部 `redoBranch(restoreFiles)`。

### 10.2 代码落点（实际）

| 关注点 | 位置 |
|---|---|
| `v2FileRestores` / `forwardRestores` / `afterAsBefore` | internal/session/store.go（`parseFileChanges` 之后） |
| `Store.Redo`（纯指针）/ `RedoToBranch`（默认）/ `redoBranch` | internal/session/store.go |
| `rewindConversationV2` 改走 `v2FileRestores` | internal/session/store.go |
| `fileChangeSnapshot.AfterSHA256` | internal/session/store.go:136 区块 |
| CLI `session redo [--conversation-only]` | internal/cli/cmd_session.go |
| `/redo <叶id> [--conversation-only]` help + 透传 | internal/cli/interactive.go |
| 单测：redo 前向还原 / reclaimed / lite tombstone / conv-only | internal/session/redo_file_restore_v2_test.go |
| CLI 端到端：默认还原 vs `--conversation-only` | internal/cli/redo_files_cli_test.go |
