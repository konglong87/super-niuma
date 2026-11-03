//go:build unix

package background

import (
	"errors"
	"os"
	"syscall"
)

// lockFileExclusive blocks until it holds an exclusive advisory lock on file.
// EINTR is retried because a blocking flock is interruptible by any signal the
// process happens to receive while waiting.
func lockFileExclusive(file *os.File) error {
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

func unlockFile(file *os.File) {
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
