//go:build !unix && !windows

package scheduler

import "os"

func lockDaemonFileExclusive(*os.File) error { return nil }

func unlockDaemonFile(*os.File) {}
