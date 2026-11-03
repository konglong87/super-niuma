package session

import (
	"os"
	"path/filepath"
	"sync"
)

var (
	transcriptLocksMu sync.Mutex
	transcriptLocks   = map[string]*sync.Mutex{}
)

func transcriptProcessLock(path string) *sync.Mutex {
	transcriptLocksMu.Lock()
	defer transcriptLocksMu.Unlock()
	lock := transcriptLocks[path]
	if lock == nil {
		lock = &sync.Mutex{}
		transcriptLocks[path] = lock
	}
	return lock
}

func transcriptLockPath(path string) string {
	return path + ".lock"
}

func withTranscriptLock(path string, fn func() error) error {
	lockPath := transcriptLockPath(path)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0755); err != nil {
		return err
	}
	processLock := transcriptProcessLock(filepath.Clean(lockPath))
	processLock.Lock()
	defer processLock.Unlock()

	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := lockTranscriptFile(file); err != nil {
		return err
	}
	defer unlockTranscriptFile(file)
	return fn()
}

func withTranscriptLockValue[T any](path string, fn func() (T, error)) (T, error) {
	var zero T
	var value T
	err := withTranscriptLock(path, func() error {
		var err error
		value, err = fn()
		return err
	})
	if err != nil {
		return zero, err
	}
	return value, nil
}
