//go:build !windows

package main

import (
	"errors"
	"os"
	"syscall"
)

func validatePrivateRuntimeDirectory(_ string, info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode().Perm() != 0o700 || !ok || stat.Uid != uint32(os.Geteuid()) {
		return errors.New("runtime directory must be an owner-only 0700 directory")
	}
	return nil
}
