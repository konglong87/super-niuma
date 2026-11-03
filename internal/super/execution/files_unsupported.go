//go:build !darwin && !linux

package execution

import (
	"context"
	"os"
)

// Fail closed until equivalent per-component no-follow semantics are tested.
func secureFilesystemSupported() bool { return false }
func (f *workspaceFS) read(context.Context, string, func(context.Context) error) (string, error) {
	return "", ErrUnavailable
}
func (f *workspaceFS) write(context.Context, string, []byte, func(context.Context) error) error {
	return ErrUnavailable
}

func verifyRoot(*os.Root, string) bool { return false }
