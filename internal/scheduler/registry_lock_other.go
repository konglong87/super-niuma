//go:build !unix && !windows

package scheduler

import "os"

func lockSchedulerFile(*os.File) error { return nil }
func unlockSchedulerFile(*os.File)     {}
