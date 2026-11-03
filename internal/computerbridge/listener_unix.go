//go:build unix

package computerbridge

import (
	"os"
	"syscall"
)

const listenerUnixSupported = true

func listenerOwnedByUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && uint64(stat.Uid) == uint64(os.Geteuid())
}

// The effective user and root are trusted. A sticky shared directory prevents
// other users from replacing our owned child even though they can create peers.
func listenerTrustedAncestor(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || (stat.Uid != 0 && uint64(stat.Uid) != uint64(os.Geteuid())) {
		return false
	}
	return info.Mode().Perm()&0022 == 0 || info.Mode()&os.ModeSticky != 0
}
