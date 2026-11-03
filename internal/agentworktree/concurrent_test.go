package agentworktree

import (
	"context"
	"os/exec"
	"sync"
	"testing"
)

// batch 子代理会并发开工作树（AUDIT-P1-20）。NewSlug 只靠 time.Now().UnixNano()
// 时，同一个时钟刻度上的两个调用会拿到同一个 slug，而 CreateWithHooks 对已存在的
// 工作树是**复用**而不是报错 —— 于是两个子代理共用一棵树互相踩文件。
func TestConcurrentCreateOnTheSameGitRootAllSucceed(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	repo := initGitRepo(t)

	const workers = 8
	var wg sync.WaitGroup
	errs := make([]error, workers)
	infos := make([]Info, workers)
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			infos[i], errs[i] = Create(context.Background(), repo, NewSlug("agent"))
		}(i)
	}
	close(start)
	wg.Wait()

	t.Cleanup(func() {
		for _, info := range infos {
			if info.Path != "" {
				_ = Remove(context.Background(), info)
			}
		}
	})

	for i, err := range errs {
		if err != nil {
			t.Errorf("worker %d: %v", i, err)
		}
	}

	// 每个 worker 都必须拿到自己的工作树：NewSlug 只靠纳秒时间戳的话，同一纳秒里
	// 两个调用会拿到同一个 slug，于是两个子代理共用一棵树互相踩。
	seen := make(map[string]int, workers)
	for i, info := range infos {
		if info.Path == "" {
			continue
		}
		if prev, dup := seen[info.Path]; dup {
			t.Errorf("workers %d and %d share worktree %s", prev, i, info.Path)
			continue
		}
		seen[info.Path] = i
	}
}

// 显式传同一个 slug 的两个并发调用（resume 路径就会这样）必须都拿到同一棵树，
// 而不是一个成功一个 `git worktree add ... exit status 128`。
//
// 这是 gitRoot 互斥锁守的那条 TOCTOU：没有锁时两个调用都先看到"还没有这棵树"，
// 然后都去 `worktree add`。
func TestConcurrentCreateWithTheSameSlugReusesOneWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	repo := initGitRepo(t)

	const workers = 6
	var wg sync.WaitGroup
	errs := make([]error, workers)
	infos := make([]Info, workers)
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			infos[i], errs[i] = Create(context.Background(), repo, "shared-slug")
		}(i)
	}
	close(start)
	wg.Wait()

	t.Cleanup(func() {
		for _, info := range infos {
			if info.Path != "" {
				_ = Remove(context.Background(), info)
				break
			}
		}
	})

	for i, err := range errs {
		if err != nil {
			t.Errorf("worker %d: %v", i, err)
		}
	}
	for i, info := range infos {
		if info.Path != infos[0].Path {
			t.Errorf("worker %d got %q, worker 0 got %q; the same slug must resolve to one worktree", i, info.Path, infos[0].Path)
		}
	}
}
