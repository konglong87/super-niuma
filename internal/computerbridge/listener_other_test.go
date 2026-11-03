//go:build !unix

package computerbridge

import (
	"context"
	"errors"
	"testing"
)

func TestListenerUnsupportedPlatform(t *testing.T) {
	l, err := StartListener(context.Background(), t.TempDir(), &listenerTestHost{})
	if l != nil || !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("expected unsupported platform, got %v", err)
	}
}
