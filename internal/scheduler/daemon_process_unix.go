//go:build unix

package scheduler

import (
	"os/exec"
	"strconv"
	"strings"
)

func schedulerDaemonProcessMatches(pid int, _ daemonMeta) bool {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "__schedule-daemon")
}
