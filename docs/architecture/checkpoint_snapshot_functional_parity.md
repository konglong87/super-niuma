# Claude Code 与 Go Claude Checkpoint/Snapshot 功能对齐说明

## 1. 文档目的

本文说明原版 Claude Code 与 Go Claude 在 checkpoint、snapshot 和 rewind 方面的功能语义、存储模型、当前对齐程度及已知问题。

本项目追求的是**用户可观察行为和恢复能力对齐**，不是 TypeScript 类型、文件布局或内部算法的逐行复刻。因此，只要 Go 版能够稳定提供相同的回退点、文件恢复、对话恢复和会话续接能力，底层采用不同的数据模型是可以接受的。

本文只记录已经通过源码确认的事实。尚未实现的修复统一标记为“待修复”，不把方案描述成已完成功能。

## 2. 结论摘要

### 2.1 总体结论

原版 Claude Code 使用 `FileHistorySnapshot` 维护“某条用户消息开始处理前，各追踪文件对应哪个备份版本”。Go Claude 没有复刻该类型，而是在 transcript 中记录自动 checkpoint 和逐次 `file_change` 事件。

两种实现都试图提供以下用户语义：

1. 每条用户请求开始前建立可回退锚点。
2. 文件工具修改文件时记录修改前状态。
3. 用户可以恢复文件、恢复对话，或者同时恢复两者。
4. 恢复到某条用户消息时，目标状态是“处理该条用户请求之前”的状态。
5. 没有文件修改的纯对话轮次不需要复制文件内容。

Go Claude 的功能入口和主要数据采集链路已经存在。2026-07-15 先完成两个已确认 P0 正确性修复（同一文件连续修改选择最早 `before`、恢复 `before_mode` 权限位），随后按计划完成 TODO-053～TODO-057：

- **TODO-053**：Bash/脚本/外部进程通过有界工作区差分统一产生可恢复 `file_change`（create/modify/delete/rename/chmod/symlink，含失败退出与额外 writable root）。
- **TODO-056**：inline 与 snapshot 恢复统一走原子 `atomicReplace`，故障下不截断目标。
- **TODO-054**：多文件恢复与 transcript 提交组成事务，故障注入下全部提交或全部回滚。
- **TODO-055**：capability-aware 元数据（symlink/目录/mtime/owner/xattr）恢复与明确降级（ACL 未覆盖，如实标注）。
- **TODO-057**：内容寻址 snapshot 去重、8KiB 外置阈值、可达性 GC 与 session 删除孤儿回收。

因此当前结论是：**核心 rewind 能力、P0 文件恢复语义、Bash/外部工具修改捕获、跨文件事务性、原子恢复、capability-aware 元数据与 snapshot 容量治理均已对齐并有回归、race、故障注入和基准测试证据。** 仍存在明确标注的边界（后台 Bash 命令、忽略目录/超预算文件、ACL、显式 rename 成对关系、<8KiB inline 内容逐条写入），不据此宣称 100% 覆盖所有外部写入与全部文件系统元数据。

## 3. 术语与验收边界

### 3.1 Snapshot

在原版 Claude Code 中，snapshot 是底层文件历史对象：

```text
FileHistorySnapshot
├── messageId
├── trackedFileBackups[filePath]
│   ├── backupFileName
│   ├── version
│   └── backupTime
└── timestamp
```

它表达的是：在关联用户消息开始处理前，每个已追踪文件应对应哪个备份版本。

### 3.2 Checkpoint

Checkpoint 是面向用户和恢复流程的“可回退点”概念。原版没有必须被 Go 版逐字复刻的独立 `Checkpoint` 文件历史类型，可以将其理解为：

```text
Checkpoint = 对话回退坐标 + 文件状态索引 + 可恢复的实体内容
```

原版 `/checkpoint` 是 `/rewind` 的命令别名。Go Claude 则真实存在 `type=checkpoint` 的 transcript entry。两者内部命名不同，不影响功能验收。

### 3.3 功能对齐的判定标准

功能对齐应由输入和结果判断，而不是由内部类型名判断：

| 场景 | 预期结果 |
| --- | --- |
| 纯对话，无文件修改 | 可以回退对话，不产生不必要的文件副本 |
| 单轮修改一个已有文件 | 回退后恢复该轮开始前的内容和权限 |
| 单轮创建文件 | 回退后删除该文件 |
| 单轮删除文件 | 回退后恢复文件内容和权限 |
| 多轮连续修改同一文件 | 可恢复到任意用户消息开始前的准确版本 |
| 多文件修改 | 每个文件都恢复到同一目标时刻 |
| conversation-only | 只改变对话，不修改工作区 |
| files-only | 只恢复文件，保留 transcript，并使过期 recap 失效 |
| code and conversation | 文件与对话同时回到目标消息之前 |
| resume 后 rewind | 重启进程后仍能读取历史并正确恢复 |
| 大文件 | 不依赖把完整内容无限制嵌入 transcript，也能恢复 |

## 4. 原版 Claude Code 的实现语义

### 4.1 每轮创建文件历史 snapshot

在启用 file checkpointing 的交互会话中，原版在处理用户请求前调用 `fileHistoryMakeSnapshot`。第一次尚未追踪文件时，snapshot 的 `trackedFileBackups` 可以为空。

因此“创建 snapshot”和“复制文件”是两件不同的事：

```text
用户消息到达
  -> 创建 snapshot 元数据
  -> 如果没有追踪文件，不创建实体备份
  -> AI 仅回复文字时，不产生文件备份
```

这里的“每轮”存在配置边界：禁用 file checkpointing、SDK 未开启对应选项或特定非交互模式时，不保证创建文件 snapshot。

### 4.2 第一次修改文件

文件工具真正写入前，`fileHistoryTrackEdit` 备份旧状态，并把备份信息回填到最近的 snapshot。对于原本不存在的新文件，备份名使用 `null` 表达“目标状态中不应存在该文件”。

### 4.3 后续 snapshot 与版本复用

文件进入追踪集合后，下一轮 snapshot 会检查当前文件是否相对最新备份发生变化：

- 文件变化：创建下一版本备份。
- 文件未变化：复用已有备份引用。
- 文件已删除：记录不存在状态。

因此版本号不应被理解为“每轮必然增加”；没有变化时可以复用旧版本。

### 4.4 Transcript 与实体备份

原版使用两套相互关联的存储：

```text
Transcript JSONL
  保存消息链和 file-history-snapshot 索引元数据

File-history 目录
  保存被 snapshot 引用的实体文件内容
```

索引负责回答“目标消息对应哪个版本”，实体备份负责提供真实内容。两者缺一不可。

### 4.5 回退语义示例

假设 `demo.txt` 的变化为：

```text
初始状态：original
用户 U1 后：11
用户 U2 后：22
用户 U3 后：33
```

正确恢复结果为：

| 目标 | `demo.txt` 应恢复为 |
| --- | --- |
| U1 开始前 | `original` |
| U2 开始前 | `11` |
| U3 开始前 | `22` |
| 当前状态 | `33` |

## 5. Go Claude 的实际实现

### 5.1 自动 checkpoint

Go Claude 在 query 接收用户输入后、写入用户 message entry 前，创建：

```text
type: checkpoint
name: auto-<message-id>
content: <message-id>
```

随后写入带相同 message ID 的用户消息。这个顺序使 checkpoint 位于该轮用户消息和工具修改之前。

### 5.2 文件变化事件

`Edit`、`MultiEdit`、`Write` 等工具完成修改后，通过 `FileChange` callback 写入 transcript：

```text
type: file_change
content:
  path
  before
  before_exists
  after
  after_exists
  before_snapshot_path
  after_snapshot_path
  before_mode
  after_mode
  before_mode_known
  after_mode_known
  mode_changed
```

小文件通常把修改前后内容直接记录在事件中。大文件由 `internal/files` 生成实体 snapshot，事件中记录 snapshot 路径。

### 5.3 三种 rewind 模式

| 用户操作 | Go 方法 | 当前设计语义 |
| --- | --- | --- |
| 文件和对话一起回退 | `RewindToMessage` | 恢复文件并截断目标之后的 transcript |
| 只恢复文件 | `RewindFiles` | 恢复文件，保留 transcript，追加 rewind 事件 |
| 只恢复对话 | `RewindConversationToMessage` | 截断 transcript，不修改文件 |

`/rewind` 无参数时列出用户消息候选；`/checkpoint` 当前也路由到相同 slash command 处理逻辑。

### 5.4 Go 版与原版的结构映射

| 功能职责 | 原版 Claude Code | Go Claude | 是否允许不同 |
| --- | --- | --- | --- |
| 用户消息前的恢复锚点 | `FileHistorySnapshot.messageId` | `checkpoint auto-<messageID>` | 是 |
| 某时刻文件状态索引 | `trackedFileBackups` | checkpoint 后的 `file_change` 范围 | 是 |
| 小文件历史内容 | file-history 备份文件 | transcript 中的 `before/after` | 是，但需控制体积 |
| 大文件历史内容 | file-history 备份文件 | snapshot 文件路径 | 是 |
| 文件不存在状态 | `backupFileName=null` | `before_exists=false` | 是 |
| 文件权限恢复 | 备份后 `chmod` | 内容恢复后应用 `before_mode` | 是 |
| 同一文件多轮版本定位 | 目标 snapshot 直接引用版本 | 从事件范围推导 | 是，但结果必须准确 |
| 文件与对话分离恢复 | rewind UI/流程 | 三个独立 Store 方法 | 是 |

## 6. 已修复问题与剩余边界

### 6.1 已修复 P0：同一文件连续修改时恢复版本错误

原实现从事件尾部向前扫描，并用 `seen[path]` 保证每个文件只处理一次。这会选择回退范围中**最后一次修改的 before 状态**，而正确结果应是**最早一次修改的 before 状态**。

示例：

```text
file_change 1: original -> 11
file_change 2: 11       -> 22
file_change 3: 22       -> 33
```

回退到三次修改之前：

```text
原算法：倒序首先读到 change 3，恢复为 22
正确结果：使用 change 1 的 before，恢复为 original
```

影响范围：

- `Rewind(sessionID, checkpoint)`。
- `RewindToMessage(sessionID, messageID)`。
- `RewindFiles(sessionID, messageID)`。
- 同一 query turn 内多次调用 `Edit` 修改同一路径。
- 跨多个用户回合持续修改同一路径。

修复后，`restoreFileChanges` 先按 transcript 正序归并每个路径，保留该路径第一次出现的完整 `before` 状态，再按稳定路径顺序执行一次恢复。以下测试锁定行为：

- `TestCheckpointRewindRestoresEarliestBeforeStateForRepeatedFileChanges`。
- `TestMessageRewindRepeatedFileChangesUsesTargetBoundary`。
- `TestRewindRemovesFileCreatedThenModified`。

### 6.2 已修复 P0：文件权限位未恢复

`tools.FileChange` 原本已经记录 `BeforeMode`、`AfterMode` 和 `ModeChanged`，但 session 层没有解码和应用这些字段。修复后，`fileChangeSnapshot` 与该 JSON 协议保持一致，inline 内容和 `before_snapshot_path` 成功恢复后都会应用有效的 `BeforeMode`。

可能结果：

- 可执行脚本回退内容后仍不可执行。
- 原本不可执行的文件可能保留错误的 executable bit。
- 内容看似恢复成功，但工作区语义没有完整恢复。

新 transcript 使用 `before_mode_known` 区分“明确记录的合法 `0000`”与“没有 mode 数据”。旧 transcript 没有 known 标志但带非零 `before_mode` 时仍恢复该权限；完全没有 mode 时跳过 chmod，不需要 migration，也不会错误设置为 `0000`。`TestRewindRestoresOriginalFileMode` 覆盖 inline、snapshot 和 `0000` 路径，`TestRewindOldFileChangeWithoutModePreservesCurrentMode` 锁定旧记录的零值行为。

### 6.3 已修复 P1：跨文件事务性恢复（TODO-054）

多文件恢复与 transcript 写入现在通过 `runFileRestoreTransaction` 组成单一“要么全部提交、要么全部回滚”的单元，四个阶段：

1. **预检**：遍历所有变化，验证引用的 snapshot 可读、目标目录（若已存在）可写；任一不可行则直接返回错误，不触碰工作区。
2. **快照当前状态**：为每个目标记录当前内容/权限/存在性/symlink（大文件溢出到临时文件），构成 rollback journal。
3. **应用恢复**：逐个恢复；任一失败立即用 journal 逆序回滚所有已应用文件。
4. **提交 transcript**：全部文件恢复成功后才重写/追加 transcript；transcript 提交失败同样回滚所有文件。

因此不再出现“前面文件已改、后面文件未改”的静默半回退，也不会出现“文件已回退但 transcript 未更新”的两侧不一致。失败时 `filesRestored` 为 0 并返回带路径上下文的明确错误。

故障注入测试：`TestRewindMissingSnapshotAbortsWithoutPartialRestore`（snapshot 缺失，预检阶段中止，两文件与 transcript 均不变）、`TestRestoreTransactionRollsBackOnFileFailure`（第 N 个文件失败，已应用文件全部回滚）、`TestRestoreTransactionRollsBackOnCommitFailure`（transcript 提交失败，文件全部回滚）、`TestRestoreTransactionCommitsWhenAllSucceed`（全成功提交）。`applyRestore` 作为测试注入点注入磁盘写/chmod 类失败。

### 6.4 已修复 P0：Bash 和外部工具变化捕获（TODO-053）

Go Claude 的可靠恢复依赖所有能修改工作区的工具都产生准确 `file_change`。`Edit`、`MultiEdit`、`Write` 早已接入；Bash、脚本和外部进程现在通过 `internal/files/capture.go` 的有界工作区差分统一捕获。

工作方式：Bash 在命令执行边界对 CWD 与额外 writable root 建立 before 快照，命令结束（含失败退出、脚本间接写入）后再扫描一次并 diff，产生 `file_change` 并复用现有协议。已验收行为：

- shell 重定向、`sh script.sh` 间接写入、`rm`、`mv`（拆分为源 delete + 目标 create）、`chmod`、`ln -s` symlink 创建。
- 命令以非零码退出后仍捕获已经发生的变化。
- 额外 writable root 内的文件修改。
- 只读命令不产生任何 `file_change`。

有界与降级设计（满足“控制扫描范围/内存/磁盘”约束）：

- 只扫描显式 writable root，绝不遍历整个文件系统。
- 默认跳过 `.git`、`.hg`、`.svn`、`node_modules`、`.venv`、`venv`、`__pycache__`、`.mypy_cache`、`.pytest_cache`。
- 小文件内联捕获、较大文件复制进 snapshot store、超过字节预算的文件报告为 skipped 并在工具结果附降级提示，绝不发出空 `before` 导致 rewind 把文件清空。
- diff 结束后回收未被任何变化引用的 before-snapshot，避免孤儿对象。

session 层 `restoreFileChange` 已能恢复 symlink 对象类型（`before_is_symlink`/`before_link_target`）。

明确剩余边界（不谎报完整）：

- `run_in_background` 异步命令的文件变化当前不捕获。
- 忽略目录内、超字节预算的文件不追踪。
- 目录对象本身的创建/删除/权限恢复归入 TODO-055。
- 可通过 `GOLANG_CLAUDE_CODE_BASH_FILE_TRACKING=0` 关闭捕获。

### 6.5 已修复 P1：capability-aware 文件系统元数据和对象类型（TODO-055）

`FileChange` 协议已扩展为 capability-aware：`internal/files/metadata.go` 定义 `Metadata`，每个能力带独立 known 标志，区分“未捕获/平台不支持/权限不足”与真正的零值。

已覆盖的对象类型与元数据：

- **symlink**：恢复链接本身及目标（§6.4）。
- **directory**：capture 与恢复目录的创建/删除/权限；恢复顺序按路径排序（父目录先建、子对象先删）保证正确性。
- **mtime**：`os.Chtimes` 恢复普通文件与目录修改时间（symlink 无可移植的 no-follow setter，跳过）。
- **owner/group（uid/gid）**：unix 通过 `Lstat.Sys()` 捕获、`Lchown` 恢复；非特权进程的 EPERM 记为可观测降级，绝不假装成功。
- **extended attributes**：linux/darwin 通过 `x/sys/unix` 的 `Listxattr/Getxattr/Setxattr` 捕获与恢复；不支持的文件系统/平台返回明确降级。

降级可观测性：无法恢复的元数据汇总到 `RewindResult.MetadataDegraded`，内容与权限仍照常恢复，绝不把“只恢复了内容”报告成“完整成功”。旧 transcript 无 metadata 字段时指针为 nil，跳过且无降级噪声（向后兼容）。

平台设计：`metadata_unix.go` / `metadata_other.go` 提供 owner 能力，`metadata_xattr_supported.go`（linux||darwin）/ `metadata_xattr_other.go` 提供 xattr 能力；非支持平台把对应能力标记为“未捕获”。

明确剩余边界（不谎报）：

- **ACL**：当前不捕获，也不恢复；视为未覆盖能力（有 ACL 的对象只保证 POSIX mode + owner + xattr），后续如实现应同样走 capability + 降级模型。
- **rename**：以 delete + create 对建模，语义上能恢复到目标状态，但不显式记录“同一对象改名”的成对关系。
- symlink 的 mtime 不恢复（无可移植接口）。

测试：`internal/files/metadata_test.go`（mtime 恢复、xattr capture/restore 往返、非特权 chown 降级、能力标志）；`internal/session/store_test.go`（`TestRewindRestoresFileMtime`、`TestRewindReportsMetadataDegradation`、`TestRewindRemovesCreatedDirectory`、`TestRewindRestoresDeletedDirectory`）；`internal/tools/bash`（`TestBashCapturesDirectoryCreate`）。

### 6.6 已修复 P1：内嵌内容恢复原子替换（TODO-056）

内嵌 `before` 与 `before_snapshot_path` 现在共享单一原子写入 helper `atomicReplace`：创建同目录临时文件、`io.Copy` 写入、`Sync`、`Chmod` 应用目标 mode、`Close`、`Rename` 覆盖目标。任何一步失败都会删除临时文件并保持原目标不变，进程中断或写失败不再截断目标文件。

权限解析由 `restoreTargetMode` 统一：显式 `before_mode`（含合法 `0000`）优先；旧 transcript 无 mode 数据时保留当前文件权限，不强制默认值。

`TestRewindInlineRestoreIsAtomicOnFailure` 用只读父目录注入 temp 创建失败，断言 restore 报错且原文件保持 `current` 不被截断；`TestRewindRestoresIntoMissingParentDirectory` 覆盖父目录缺失时的重建；`0000` mode 与 legacy 无 mode 路径由既有测试锁定。

### 6.7 已修复 P2：Transcript 与 snapshot 容量治理（TODO-057）

已建立内容寻址的统一 snapshot store 与基于可达性的 GC（`internal/files/snapshotstore.go`）：

- **内容寻址去重**：blob 以 sha256 内容哈希命名（`sha256-<hash>.bin`），相同内容只存一份。大文件编辑（`persistSnapshot`）与 Bash 捕获的被引用 before-content 都走此 store。`TestSnapshotStoreDedupsIdenticalContent` 证明 1000 次相同内容只产生 1 个 blob。
- **transcript 增长受控**：Bash 捕获对超过 8KiB 的 before-content 外置到内容寻址 store，transcript 条目只保留短引用而非完整内容（`TestCaptureExternalizesLargeBeforeContent`）；相同内容跨编辑去重为单个 blob，避免 transcript/store 随文件大小线性膨胀。
- **引用生命周期与 GC**：`Store.GCSnapshots` 从所有存活 transcript 的 `file_change` 快照引用计算可达集合，`files.GCSnapshots` 只删除不可达 blob，绝不回收仍可用于 rewind 的对象（`TestGCSnapshotsKeepsReachableRemovesOrphans`）。
- **session 删除回收孤儿**：`Store.Delete` 在删除 transcript 后运行 GC，回收该 session 独占的 blob；被存活 session 共享的 blob 保留（`TestGCSnapshotsReclaimsOrphansButKeepsReachable`）。

明确剩余边界（不谎报）：小于 8KiB 阈值的 inline before/after 仍按条目写入 JSONL，随编辑次数增长（单条受阈值限制，未做逐条内容去重）；`Edit`/`Write` 工具的小文件 before/after 仍 inline。基准数据见 `BenchmarkSnapshotStoreRepeatedIdenticalContent`。

## 7. 功能完整性状态

| 能力 | 当前状态 | 说明 |
| --- | --- | --- |
| 用户消息前自动 checkpoint | 已实现 | query 进入时创建自动 checkpoint |
| 纯对话 checkpoint | 已实现 | 不产生文件副本，仅保留恢复锚点 |
| 单次小文件内容恢复 | 已实现 | transcript 保存 before 内容 |
| 新文件回退删除 | 已实现并有测试 | `before_exists=false` 时删除 |
| 多个不同文件恢复 | 已实现并有测试 | 每个路径恢复一次 |
| 大文件 snapshot 恢复 | 已实现并有测试 | 通过 `before_snapshot_path` 恢复 |
| conversation-only | 已实现并有测试 | 不恢复文件 |
| files-only | 已实现并有测试 | 保留 transcript，并使 recap 失效 |
| 文件与对话同时回退 | 已实现并有测试 | 截断 transcript 并恢复文件 |
| 同一文件连续多次修改 | **已修复并有测试** | 正序选择同路径最早 change 的 before |
| 文件权限恢复 | **已修复并有测试** | inline 与 snapshot 恢复后应用有效 before mode |
| Bash/外部工具修改捕获 | **已修复并有测试** | Bash/脚本/外部进程通过有界工作区差分统一产生 file_change；后台命令、忽略目录、超预算文件为明确边界 |
| 多文件失败原子性 | **已修复并有测试** | 多文件恢复与 transcript 组成事务，故障注入下全部提交或全部回滚 |
| 完整文件系统元数据 | **已修复并有测试（ACL 除外）** | symlink/目录/mtime/owner/xattr 已 capability-aware 恢复并降级；ACL 未覆盖、rename 以 delete+create 建模 |
| 小文件原子恢复 | **已修复并有测试** | inline 与 snapshot 共享 atomicReplace（temp+sync+chmod+rename），故障注入下原文件不变 |
| Transcript 容量治理 | **已修复并有测试** | 内容寻址去重 + 8KiB 外置阈值 + 可达性 GC + session 删除回收孤儿；inline 小内容逐条写入为剩余边界 |

## 8. 对齐原则与后续约束

1. 不要求引入原版 `FileHistorySnapshot`，除非事件模型无法可靠满足功能语义。
2. 不以类型名、JSON 字段名、目录结构不同判定不兼容。
3. 必须以目标消息前的工作区内容、存在性和权限作为验收结果。
4. 所有修复先写失败测试，避免只修示例而没有锁住协议。
5. `files-only`、`conversation-only` 和 combined rewind 必须共享一致的边界计算，避免三套逻辑漂移。
6. 不隐藏未覆盖的外部修改；无法追踪时应明确能力边界，而不是声称 100% 对齐。
7. 修复方案见 [Checkpoint Rewind Correctness Implementation Plan](../superpowers/plans/2026-07-15-checkpoint-rewind-correctness-fix-plan.md)。

## 9. TODO 实施清单（已完成）

| TODO | 优先级 | 状态 | 完成定义摘要 |
| --- | --- | --- | --- |
| TODO-053 | P0 | 已完成 | Bash/脚本/外部工具的 create、modify、delete、rename、chmod 和 symlink 均产生可恢复变化记录，端到端 `Rewind` 通过；后台命令/忽略目录/超预算文件为明确边界 |
| TODO-056 | P1 | 已完成 | inline 与 snapshot 内容都通过 `atomicReplace` 原子替换恢复，故障注入时原文件不变 |
| TODO-054 | P1 | 已完成 | 多文件与 transcript 形成统一提交边界，故障注入下不出现半回退（`runFileRestoreTransaction`） |
| TODO-055 | P1 | 已完成（ACL 除外） | capability-aware 元数据覆盖 symlink、目录、mtime、owner、xattr 与明确降级；ACL 未覆盖、rename 以 delete+create 建模 |
| TODO-057 | P2 | 已完成 | 内容寻址去重 + 8KiB 外置阈值 + 可达性 GC + session 删除孤儿回收；inline 小内容逐条写入为剩余边界 |

顺序依据：TODO-053 是当前用户可见的恢复能力缺口；TODO-056 是 TODO-054 的单文件原子写基础；事务层稳定后再扩大元数据对象类型；容量治理最后建立在稳定的 snapshot 协议之上。

## 10. 源码证据索引

### 原版 Claude Code

- `src/utils/fileHistory.ts`：`FileHistorySnapshot`、track edit、make snapshot、rewind。
- `src/utils/sessionStorage.ts`：`file-history-snapshot` transcript 写入与恢复链构建。
- `src/screens/REPL.tsx`：用户请求开始时创建 snapshot。
- `src/commands/rewind/index.ts`：`checkpoint` 命令别名。

### Go Claude

- `internal/query/query.go`：自动 checkpoint、用户消息写入、`file_change` 记录。
- `internal/tools/tool.go`：`FileChange` 协议（含 symlink/dir 对象类型与 `files.Metadata`）。
- `internal/tools/fileedit/fileedit.go`：Edit/MultiEdit 文件变化采集。
- `internal/tools/filewrite/filewrite.go`：Write 文件变化采集。
- `internal/tools/bash/bash.go`：Bash 执行边界的有界工作区差分捕获（TODO-053）。
- `internal/files/capture.go`：工作区 before/after 差分、metadata/dir 捕获、外置阈值。
- `internal/files/metadata*.go`：capability-aware mtime/owner/xattr 捕获与恢复（TODO-055）。
- `internal/files/snapshotstore.go`：内容寻址 snapshot 去重与可达性 GC（TODO-057）。
- `internal/files/largefile.go`：大文件 snapshot（去重后走内容寻址 store）。
- `internal/session/store.go`：checkpoint、三种 rewind、事务式恢复、原子替换、元数据应用、snapshot GC。
- `internal/session/store_test.go`：session rewind、事务、元数据、GC 测试。
- `internal/cli/cli.go`：`/rewind` 与 `/checkpoint` 用户入口。
