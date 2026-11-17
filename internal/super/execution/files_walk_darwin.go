//go:build darwin

package execution

import "golang.org/x/sys/unix"

// walkDirFlags opens each ancestor of the workspace for reading; Darwin has
// no O_PATH.
const walkDirFlags = unix.O_RDONLY
