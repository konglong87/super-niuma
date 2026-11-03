//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package imagegen

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/konglong87/super-niuma/internal/media"
)

const secureDirectoryFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC | unix.O_NOFOLLOW

func filesystemBlobStoreSecureSupported() bool { return true }

func filesystemPut(root string, req PutBlobRequest) (Blob, error) {
	parentFD, name, closeParent, err := openSecureBlobParent(root, req.Key, true)
	if err != nil {
		return Blob{}, err
	}
	defer closeParent()
	if err := rejectUnsafeFinalEntry(parentFD, name); err != nil {
		return Blob{}, err
	}
	temporary, temporaryName, err := createSecureTemporary(parentFD)
	if err != nil {
		return Blob{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = unix.Unlinkat(parentFD, temporaryName, 0)
		}
	}()
	size, digest, err := hashReader(temporary, req.Data)
	if chmodErr := unix.Fchmod(int(temporary.Fd()), 0o640); err == nil {
		err = chmodErr
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return Blob{}, err
	}
	if err := unix.Renameat(parentFD, temporaryName, parentFD, name); err != nil {
		return Blob{}, secureFilesystemError(err)
	}
	committed = true
	return Blob{Key: req.Key, MediaType: strings.TrimSpace(req.MediaType), Name: strings.TrimSpace(req.Name), SizeBytes: size, SHA256: digest, CreatedAt: time.Now().UTC()}, nil
}

func filesystemOpen(root string, _ media.AccessPolicy, key string) (BlobReader, error) {
	parentFD, name, closeParent, err := openSecureBlobParent(root, key, false)
	if err != nil {
		return BlobReader{}, err
	}
	defer closeParent()
	fd, err := unix.Openat(parentFD, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return BlobReader{}, secureFilesystemError(err)
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		_ = unix.Close(fd)
		return BlobReader{}, ErrBlobForbidden
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return BlobReader{}, err
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return BlobReader{}, ErrBlobForbidden
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		_ = file.Close()
		return BlobReader{}, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return BlobReader{}, err
	}
	return BlobReader{ReadCloser: file, Blob: Blob{Key: key, Name: filepath.Base(key), MediaType: mime.TypeByExtension(filepath.Ext(key)), SizeBytes: info.Size(), SHA256: fmt.Sprintf("%x", hash.Sum(nil)), CreatedAt: info.ModTime().UTC()}}, nil
}

func filesystemDelete(root string, _ media.AccessPolicy, key string) error {
	parentFD, name, closeParent, err := openSecureBlobParent(root, key, false)
	if err != nil {
		return err
	}
	defer closeParent()
	if err := rejectUnsafeFinalEntry(parentFD, name); err != nil {
		return err
	}
	return secureFilesystemError(unix.Unlinkat(parentFD, name, 0))
}

func filesystemListTenantBlobs(ctx context.Context, root string, tenantID uint64) ([]Blob, error) {
	tenantRoot := filepath.Join(root, fmt.Sprintf("tenant-%d", tenantID))
	if info, err := os.Lstat(tenantRoot); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrBlobForbidden
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	blobs := make([]Blob, 0)
	err := filepath.WalkDir(tenantRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if strings.HasPrefix(entry.Name(), ".blob-") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(entry.Name(), ".blob-") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(relative)
		if err := validateTenantKey(tenantID, key); err != nil {
			return err
		}
		blobs = append(blobs, Blob{Key: key, Name: entry.Name(), MediaType: mime.TypeByExtension(filepath.Ext(key)), SizeBytes: info.Size(), CreatedAt: info.ModTime().UTC()})
		return nil
	})
	if os.IsNotExist(err) {
		return []Blob{}, nil
	}
	if err != nil {
		return nil, err
	}
	sort.Slice(blobs, func(i, j int) bool { return blobs[i].Key < blobs[j].Key })
	return blobs, nil
}

func openSecureBlobParent(root, key string, create bool) (int, string, func(), error) {
	rootFD, err := unix.Open(root, secureDirectoryFlags, 0)
	if err != nil {
		return -1, "", nil, secureFilesystemError(err)
	}
	parts := strings.Split(key, "/")
	currentFD := rootFD
	for _, part := range parts[:len(parts)-1] {
		nextFD, err := unix.Openat(currentFD, part, secureDirectoryFlags, 0)
		if errors.Is(err, unix.ENOENT) && create {
			if mkdirErr := unix.Mkdirat(currentFD, part, 0o750); mkdirErr != nil && !errors.Is(mkdirErr, unix.EEXIST) {
				if currentFD != rootFD {
					_ = unix.Close(currentFD)
				}
				_ = unix.Close(rootFD)
				return -1, "", nil, secureFilesystemError(mkdirErr)
			}
			nextFD, err = unix.Openat(currentFD, part, secureDirectoryFlags, 0)
		}
		if err != nil {
			if currentFD != rootFD {
				_ = unix.Close(currentFD)
			}
			_ = unix.Close(rootFD)
			return -1, "", nil, secureFilesystemError(err)
		}
		if currentFD != rootFD {
			_ = unix.Close(currentFD)
		}
		currentFD = nextFD
	}
	_ = unix.Close(rootFD)
	return currentFD, parts[len(parts)-1], func() { _ = unix.Close(currentFD) }, nil
}

func rejectUnsafeFinalEntry(parentFD int, name string) error {
	fd, err := unix.Openat(parentFD, name, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return secureFilesystemError(err)
	}
	defer unix.Close(fd)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return secureFilesystemError(err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return ErrBlobForbidden
	}
	return nil
}

func createSecureTemporary(parentFD int) (*os.File, string, error) {
	for attempt := 0; attempt < 16; attempt++ {
		name := fmt.Sprintf(".blob-%d-%d-%d", os.Getpid(), time.Now().UnixNano(), attempt)
		fd, err := unix.Openat(parentFD, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o640)
		if errors.Is(err, unix.EEXIST) {
			continue
		}
		if err != nil {
			return nil, "", secureFilesystemError(err)
		}
		file := os.NewFile(uintptr(fd), name)
		if file == nil {
			_ = unix.Close(fd)
			return nil, "", ErrBlobForbidden
		}
		return file, name, nil
	}
	return nil, "", fmt.Errorf("create image blob temporary file: %w", unix.EEXIST)
}

func secureFilesystemError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, unix.ENOENT) {
		return ErrBlobNotFound
	}
	if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) {
		return ErrBlobForbidden
	}
	return err
}
