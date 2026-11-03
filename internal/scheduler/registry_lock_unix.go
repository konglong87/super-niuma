//go:build unix

package scheduler

import (
	"errors"
	"os"
	"syscall"
)

func lockSchedulerFile(file *os.File) error {
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

func unlockSchedulerFile(file *os.File) { _ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN) }
