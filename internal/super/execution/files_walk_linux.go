//go:build linux

package execution

import "golang.org/x/sys/unix"

// walkDirFlags opens "/" and each ancestor of the workspace with O_PATH,
// which needs only search permission. An Android app may pass through "/"
// (SELinux denies untrusted apps read on rootfs) and /data, /data/data (mode
// 0771, owned by system) but not read them, so O_RDONLY would refuse every
// app-private workspace. O_PATH still honours O_NOFOLLOW and O_DIRECTORY, and
// fstat on the result feeds the same inode comparison.
const walkDirFlags = unix.O_PATH
