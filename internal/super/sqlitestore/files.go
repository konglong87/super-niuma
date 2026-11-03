package sqlitestore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const (
	directoryMode = 0700
	databaseMode  = 0600
	maxPathBytes  = 4096
)

var errOpen = errors.New("cannot open private work-order store")

// The direct parent must be private. Refuse insecure existing directories rather
// than chmod a possibly shared directory. Ancestor aliases (e.g. macOS /var)
// are resolved; database, direct-parent, and sidecar symlinks are refused.
func preparePath(path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || len(path) > maxPathBytes || strings.ContainsRune(path, 0) {
		return "", errOpen
	}
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, directoryMode); err != nil {
		return "", errOpen
	}
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || info.Mode().Perm() != directoryMode {
		return "", errOpen
	}
	parent, err = filepath.EvalSymlinks(parent)
	if err != nil {
		return "", errOpen
	}
	path = filepath.Join(parent, filepath.Base(path))
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, databaseMode)
	if err == nil {
		err = file.Close()
	} else if errors.Is(err, os.ErrExist) {
		err = nil
	}
	if err != nil {
		return "", errOpen
	}
	if err := checkPrivateFile(path); err != nil {
		return "", err
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if err := checkPrivateFile(path + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", errOpen
		}
	}
	return path, nil
}

func checkPrivateFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != databaseMode {
		return errOpen
	}
	return nil
}
