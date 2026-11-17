//go:build linux

package verification

import (
	"context"
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// walkWorkspace walks "/" and every ancestor with O_PATH descriptors, which
// need only search permission: an Android app may pass through "/" (SELinux
// denies untrusted apps read on rootfs) and /data, /data/data (0771, owned by
// system) but not read them, so an os.Root chain would refuse every
// app-private workspace. Each step still rejects symlinks and non-directories
// and checks the opened inode against the one it inspected. The workspace is
// then opened as an os.Root and must be that same final inode.
func walkWorkspace(ctx context.Context, workspace, anchor string, parts []string) (*os.Root, error) {
	fd, err := unix.Open(anchor, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrInvalidWorkspace
	}
	for _, part := range parts {
		if err := ctx.Err(); err != nil {
			unix.Close(fd)
			return nil, err
		}
		next, err := openChildPath(fd, part)
		unix.Close(fd)
		if err != nil {
			return nil, fmt.Errorf("%w: %s", ErrInvalidWorkspace, failureCode(err))
		}
		fd = next
	}
	defer unix.Close(fd)
	var walked unix.Stat_t
	if err := unix.Fstat(fd, &walked); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidWorkspace, CodeReadFailed)
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidWorkspace, failureCode(err))
	}
	opened, err := root.Stat(".")
	if err != nil || !sameInode(walked, opened) {
		root.Close()
		return nil, fmt.Errorf("%w: %s", ErrInvalidWorkspace, CodeChanged)
	}
	return root, nil
}

func openChildPath(parent int, name string) (int, error) {
	var before unix.Stat_t
	if err := unix.Fstatat(parent, name, &before, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return -1, err
	}
	switch before.Mode & unix.S_IFMT {
	case unix.S_IFLNK:
		return -1, fileFailure(CodeSymlink)
	case unix.S_IFDIR:
	default:
		return -1, fileFailure(CodeNotRegular)
	}
	fd, err := unix.Openat(parent, name, unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	var opened unix.Stat_t
	if err := unix.Fstat(fd, &opened); err != nil || opened.Dev != before.Dev || opened.Ino != before.Ino {
		unix.Close(fd)
		return -1, fileFailure(CodeChanged)
	}
	return fd, nil
}

func sameInode(walked unix.Stat_t, info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && uint64(st.Dev) == uint64(walked.Dev) && uint64(st.Ino) == uint64(walked.Ino)
}
