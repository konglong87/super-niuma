//go:build !unix && !windows

package scheduler

func schedulerDaemonProcessMatches(int, daemonMeta) bool { return false }
