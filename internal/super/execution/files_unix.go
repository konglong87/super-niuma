//go:build darwin || linux

package execution

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

func secureFilesystemSupported() bool { return true }

// Verify the opened root against a component-wise no-follow walk. The final
// inode comparison closes the lstat/OpenRoot race at the workspace boundary.
func verifyRoot(root *os.Root, name string) bool {
	current, err := os.Open("/")
	if err != nil {
		return false
	}
	for _, component := range strings.Split(strings.TrimPrefix(name, "/"), "/") {
		fd, err := unix.Openat(int(current.Fd()), component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		current.Close()
		if err != nil {
			return false
		}
		current = os.NewFile(uintptr(fd), component)
	}
	defer current.Close()
	bound, err := root.Open(".")
	if err != nil {
		return false
	}
	defer bound.Close()
	a, err := current.Stat()
	if err != nil {
		return false
	}
	b, err := bound.Stat()
	return err == nil && os.SameFile(a, b)
}

// Walk one component at a time, starting at the bound os.Root descriptor.
// Root.OpenFile alone follows internal symlinks in intermediate components;
// O_NOFOLLOW at EVERY openat prevents that in-root authorization bypass too.
func (f *workspaceFS) parent(ctx context.Context, name string, create bool, check func(context.Context) error) (*os.File, string, error) {
	current, err := f.root.Open(".")
	if err != nil {
		return nil, "", ErrFailed
	}
	parts := strings.Split(name, "/")
	for _, part := range parts[:len(parts)-1] {
		if err := check(ctx); err != nil {
			current.Close()
			return nil, "", err
		}
		fd, err := unix.Openat(int(current.Fd()), part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if errors.Is(err, unix.ENOENT) && create {
			if err := check(ctx); err != nil {
				current.Close()
				return nil, "", err
			}
			if err = unix.Mkdirat(int(current.Fd()), part, 0700); err != nil && !errors.Is(err, unix.EEXIST) {
				current.Close()
				return nil, "", ErrFailed
			}
			fd, err = unix.Openat(int(current.Fd()), part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		}
		if err != nil {
			current.Close()
			return nil, "", ErrAuthority
		}
		next := os.NewFile(uintptr(fd), part)
		if !sameDevice(current, next) {
			next.Close()
			current.Close()
			return nil, "", ErrAuthority
		}
		current.Close()
		current = next
	}
	return current, parts[len(parts)-1], nil
}
func sameDevice(a, b *os.File) bool {
	var sa, sb unix.Stat_t
	return unix.Fstat(int(a.Fd()), &sa) == nil && unix.Fstat(int(b.Fd()), &sb) == nil && sa.Dev == sb.Dev
}
func regular(fd int) bool {
	var s unix.Stat_t
	return unix.Fstat(fd, &s) == nil && s.Mode&unix.S_IFMT == unix.S_IFREG && s.Nlink == 1
}
func (f *workspaceFS) read(ctx context.Context, name string, check func(context.Context) error) (string, error) {
	parent, leaf, err := f.parent(ctx, name, false, check)
	if err != nil {
		return "", err
	}
	defer parent.Close()
	if err := check(ctx); err != nil {
		return "", err
	}
	fd, err := unix.Openat(int(parent.Fd()), leaf, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", ErrAuthority
	}
	file := os.NewFile(uintptr(fd), leaf)
	defer file.Close()
	if !sameDevice(parent, file) {
		return "", ErrAuthority
	}
	info, err := file.Stat()
	if err != nil {
		return "", ErrFailed
	}
	if info.IsDir() {
		return f.list(ctx, file, name, check)
	}
	if !regular(fd) || info.Size() > MaxFileBytes {
		return "", ErrAuthority
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxFileBytes+1))
	if err != nil || len(data) > MaxFileBytes || !utf8.Valid(data) {
		return "", ErrFailed
	}
	if err := check(ctx); err != nil {
		return "", err
	}
	return string(data), nil
}
func (f *workspaceFS) list(ctx context.Context, dir *os.File, name string, check func(context.Context) error) (string, error) {
	entries, err := dir.ReadDir(maxDirectoryEntries + 1)
	if err != nil && err != io.EOF {
		return "", ErrFailed
	}
	if len(entries) > maxDirectoryEntries {
		return "", ErrFailed
	}
	type entry struct {
		Path string `json:"path"`
		Kind string `json:"kind"`
	}
	out := make([]entry, 0, len(entries))
	for _, e := range entries {
		candidate := path.Join(name, e.Name())
		if !f.permitted(candidate, false) {
			continue
		}
		var stat unix.Stat_t
		if unix.Fstatat(int(dir.Fd()), e.Name(), &stat, unix.AT_SYMLINK_NOFOLLOW) != nil {
			return "", ErrFailed
		}
		kind := ""
		switch stat.Mode & unix.S_IFMT {
		case unix.S_IFREG:
			if stat.Nlink == 1 {
				kind = "file"
			}
		case unix.S_IFDIR:
			kind = "directory"
		}
		if kind != "" {
			out = append(out, entry{candidate, kind})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	data, err := json.Marshal(struct {
		Entries []entry `json:"entries"`
	}{out})
	if err != nil || len(data) > MaxFileBytes {
		return "", ErrFailed
	}
	if err := check(ctx); err != nil {
		return "", err
	}
	return string(data), nil
}
func safeDestination(parent *os.File, name string) bool {
	var stat unix.Stat_t
	err := unix.Fstatat(int(parent.Fd()), name, &stat, unix.AT_SYMLINK_NOFOLLOW)
	return errors.Is(err, unix.ENOENT) || (err == nil && stat.Mode&unix.S_IFMT == unix.S_IFREG && stat.Nlink == 1)
}
func (f *workspaceFS) write(ctx context.Context, name string, data []byte, check func(context.Context) error) error {
	parent, leaf, err := f.parent(ctx, name, true, check)
	if err != nil {
		return err
	}
	defer parent.Close()
	if !safeDestination(parent, leaf) {
		return ErrAuthority
	}
	if err := check(ctx); err != nil {
		return err
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return ErrFailed
	}
	temp := ".super-write-" + hex.EncodeToString(nonce)
	fd, err := unix.Openat(int(parent.Fd()), temp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return ErrFailed
	}
	file := os.NewFile(uintptr(fd), temp)
	defer file.Close()
	defer unix.Unlinkat(int(parent.Fd()), temp, 0)
	if _, err := file.Write(data); err != nil {
		return ErrFailed
	}
	if err := file.Sync(); err != nil {
		return ErrFailed
	}
	if err := check(ctx); err != nil {
		return err
	}
	if !safeDestination(parent, leaf) {
		return ErrAuthority
	}
	// Replace the directory entry, never truncate an existing inode. A racing
	// symlink/hardlink cannot redirect a write to its target.
	if err := unix.Renameat(int(parent.Fd()), temp, int(parent.Fd()), leaf); err != nil {
		return ErrFailed
	}
	if err := parent.Sync(); err != nil {
		return ErrFailed
	}
	return nil
}
