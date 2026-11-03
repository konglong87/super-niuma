//go:build !unix

package files

// Non-unix platforms do not expose POSIX ownership. Ownership is simply not a
// captured capability there, so restore reports nothing (no fake success).
func captureOwner(path string) (int, int, bool) { return 0, 0, false }

func applyOwner(path string, uid, gid int) string { return "" }
