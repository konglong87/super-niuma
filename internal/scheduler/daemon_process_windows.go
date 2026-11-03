//go:build windows

package scheduler

import (
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

const schedulerDaemonStartTolerance = 30 * time.Second

func schedulerDaemonProcessMatches(pid int, meta daemonMeta) bool {
	if pid <= 0 || meta.Executable == "" || meta.StartedAt.IsZero() {
		return false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)

	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size); err != nil {
		return false
	}
	actual := filepath.Clean(windows.UTF16ToString(buffer[:size]))
	expected, err := filepath.Abs(meta.Executable)
	if err != nil || !strings.EqualFold(actual, filepath.Clean(expected)) {
		return false
	}

	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &created, &exited, &kernel, &user); err != nil {
		return false
	}
	createdAt := time.Unix(0, created.Nanoseconds()).UTC()
	delta := meta.StartedAt.UTC().Sub(createdAt)
	return delta >= 0 && delta <= schedulerDaemonStartTolerance
}
