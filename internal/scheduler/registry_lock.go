package scheduler

import (
	"os"
	"path/filepath"
	"sync"
)

var (
	schedulerLocksMu sync.Mutex
	schedulerLocks   = map[string]*sync.Mutex{}
)

func schedulerProcessLock(path string) *sync.Mutex {
	schedulerLocksMu.Lock()
	defer schedulerLocksMu.Unlock()
	lock := schedulerLocks[path]
	if lock == nil {
		lock = &sync.Mutex{}
		schedulerLocks[path] = lock
	}
	return lock
}

func (s *Store) withRegistryLock(run func() error) error {
	path := filepath.Join(s.Root, "schedules.lock")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	processLock := schedulerProcessLock(path)
	processLock.Lock()
	defer processLock.Unlock()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := lockSchedulerFile(file); err != nil {
		return err
	}
	defer unlockSchedulerFile(file)
	return run()
}
