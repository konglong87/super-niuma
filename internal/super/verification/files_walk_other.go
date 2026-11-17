//go:build !linux

package verification

import (
	"context"
	"os"
)

func walkWorkspace(ctx context.Context, _ string, anchor string, parts []string) (*os.Root, error) {
	return walkRootChain(ctx, anchor, parts)
}
