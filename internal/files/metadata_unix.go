//go:build unix

package files

import (
	"fmt"
	"os"
	"syscall"
)

// captureOwner reads uid/gid without following symlinks.
func captureOwner(path string) (int, int, bool) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, 0, false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return int(st.Uid), int(st.Gid), true
}

// applyOwner restores uid/gid via Lchown. A permission error is reported as an
// observable degradation instead of a hard failure, because non-root processes
// cannot change ownership and must not pretend the metadata was restored.
func applyOwner(path string, uid, gid int) string {
	if err := os.Lchown(path, uid, gid); err != nil {
		return fmt.Sprintf("owner uid=%d gid=%d: %v", uid, gid, err)
	}
	return ""
}
