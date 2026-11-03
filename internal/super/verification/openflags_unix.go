//go:build unix

package verification

import (
	"os"
	"syscall"
)

// NOFOLLOW rejects a leaf symlink swapped in after Lstat; NONBLOCK prevents a
// malicious regular-file-to-FIFO swap from hanging the read-only verifier.
const secureReadFlags = os.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
const secureOpenSupported = true
