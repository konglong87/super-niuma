package background

import (
	"os"
	"path/filepath"
	"sync"
)

// processLocks serializes registry writers inside this process. It is keyed by
// lock path rather than held on Store because Store is a value type that
// callers build ad hoc (`background.Store{Root: root}`), so a mutex field would
// be a fresh mutex per copy and would guard nothing.
var (
	processLocksMu sync.Mutex
	processLocks   = map[string]*sync.Mutex{}
)

func processLock(path string) *sync.Mutex {
	processLocksMu.Lock()
	defer processLocksMu.Unlock()
	mu, ok := processLocks[path]
	if !ok {
		mu = &sync.Mutex{}
		processLocks[path] = mu
	}
	return mu
}

// lockPath is deliberately a file of its own instead of the registry itself:
// save replaces the registry with a rename, so a lock taken on the registry
// would end up held on a discarded inode while the next writer locks the new
// one, and the two would not exclude each other.
func (s Store) lockPath() string {
	return filepath.Join(s.Root, "background_sessions.lock")
}

// withRegistryLock runs fn with exclusive ownership of the registry. It must
// wrap the whole read-modify-write cycle, not just the write: List/mutate/save
// is a lost-update race the moment two writers interleave, and locking only the
// save half would still lose one of them.
//
// The file lock is what makes this work across processes, which is the case
// that matters: the registry lives under ~/.niuma and is shared by every
// niuma process on the machine. On unix the file lock alone would also
// order goroutines within this process, since flock attaches to the open file
// description and two OpenFile calls therefore contend. The mutex is kept
// because on !unix there is no file lock at all (see lock_other.go), and there
// it is the only thing standing between two goroutines and a lost update.
func (s Store) withRegistryLock(fn func() error) error {
	path := s.lockPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	mu := processLock(path)
	mu.Lock()
	defer mu.Unlock()

	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := lockFileExclusive(file); err != nil {
		return err
	}
	defer unlockFile(file)
	return fn()
}
