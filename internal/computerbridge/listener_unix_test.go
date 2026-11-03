//go:build unix

package computerbridge

import (
	"context"
	"errors"
	"os"
	"syscall"
	"testing"
)

type listenerOwnerInfo struct {
	os.FileInfo
	stat *syscall.Stat_t
}

func (i listenerOwnerInfo) Sys() any { return i.stat }

func TestListenerRootOwnership(t *testing.T) {
	root := listenerTestRoot(t)
	info, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if listenerOwnedByUser(listenerOwnerInfo{FileInfo: info, stat: &syscall.Stat_t{Uid: uint32(os.Geteuid() + 1)}}) {
		t.Fatal("accepted foreign owner")
	}
	// Exercise real chown when privileged; the helper assertion above also
	// covers the foreign-owner predicate in ordinary unprivileged CI.
	if os.Geteuid() == 0 {
		if err := os.Chown(root, 1, -1); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Chown(root, 0, -1) }()
		l, err := StartListener(context.Background(), root, &listenerTestHost{})
		if l != nil {
			_ = l.Close()
			t.Fatal("accepted foreign root")
		}
		if !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("foreign root error: %v", err)
		}
	}
}
