package scheduler

import (
	"os"
	"path/filepath"
	"sync"
)

var (
	daemonLocksMu sync.Mutex
	daemonLocks   = map[string]*sync.Mutex{}
)

func daemonProcessLock(path string) *sync.Mutex {
	daemonLocksMu.Lock()
	defer daemonLocksMu.Unlock()
	if lock, ok := daemonLocks[path]; ok {
		return lock
	}
	lock := &sync.Mutex{}
	daemonLocks[path] = lock
	return lock
}

func (s *Store) daemonLockPath() string {
	return filepath.Join(s.Root, "scheduler", "daemon.lock")
}

// withDaemonLock protects the complete check-restart-start-publish sequence.
// The dedicated file is never replaced, unlike daemon PID/meta files, so its
// advisory lock remains valid while the daemon identity is published.
func (s *Store) withDaemonLock(fn func() error) error {
	path := s.daemonLockPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	lock := daemonProcessLock(path)
	lock.Lock()
	defer lock.Unlock()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := lockDaemonFileExclusive(file); err != nil {
		return err
	}
	defer unlockDaemonFile(file)
	return fn()
}
