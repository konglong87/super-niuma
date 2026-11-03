package agentworktree

import (
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// gitRootLocks 给每个 git root 一把互斥锁。
//
// `git worktree add` / `worktree remove` 都要写 gitRoot 的 `.git/worktrees` 和索引，
// 而 git 用 `index.lock` 独占它们：两个 batch 子代理同时开工作树时，其中一个会拿到
// `exit status 128`，从调用方看就是子代理随机开不起来（AUDIT-P1-20）。
//
// 串行化的范围刻意只覆盖 git 元数据写入这几秒，不覆盖子代理的实际工作 ——
// batch 并发本身是调用方真实想要的，要串的只有这一小段。
//
// 只管进程内：同一个仓库上跑两个 niuma 进程仍然会撞 git 自己的锁，那种情况下
// git 的报错就是正确答案，本包不做跨进程协调。
var gitRootLocks sync.Map // map[string]*sync.Mutex

func lockGitRoot(root string) func() {
	value, _ := gitRootLocks.LoadOrStore(root, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// slugCounter 让同一进程内的 slug 一定互不相同。
//
// 只用 time.Now().UnixNano() 是不够的：两个 goroutine 落在同一个时钟刻度上就会
// 拿到同一个 slug，而 CreateWithHooks 对已存在的工作树是复用而不是报错，于是两个
// 子代理会共用一棵树互相踩 —— 正是这条要防的东西。
var slugCounter atomic.Uint64

func newSlugSuffix() string {
	return strconv.FormatInt(time.Now().UnixNano(), 36) + "-" + strconv.FormatUint(slugCounter.Add(1), 36)
}
