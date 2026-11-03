package main

import (
	"context"
	"github.com/konglong87/super-niuma/internal/computerbridge"
	"os"
	"path/filepath"
)

const desktopComputerBridgeDir = "computer-bridge"

func startDesktopComputerBridge(ctx context.Context, dataDir string, manager *computerManager) (*computerbridge.Listener, error) {
	return startDesktopComputerBridgeWithRuntimeStop(ctx, dataDir, manager, nil)
}

func startDesktopComputerBridgeWithRuntimeStop(ctx context.Context, dataDir string, manager *computerManager, stopRuntime func()) (*computerbridge.Listener, error) {
	root := filepath.Join(dataDir, desktopComputerBridgeDir)
	if err := os.Mkdir(root, 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	// StartListener verifies ownership, mode and no symlink components. Do not
	// chmod existing user directories or silently replace an insecure endpoint.
	return computerbridge.StartListener(ctx, root, computerAgentService{manager: manager, stopRuntime: stopRuntime})
}
