package channel

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var ErrWorkspaceOutsideRoots = errors.New("channel: workspace is outside configured roots")

func ResolveWorkspace(candidate string, roots []string) (string, error) {
	if strings.TrimSpace(candidate) == "" {
		return "", errors.New("channel: workspace path is required")
	}
	if len(roots) == 0 {
		return "", ErrWorkspaceOutsideRoots
	}
	realCandidate, err := existingRealDirectory(candidate)
	if err != nil {
		return "", err
	}
	for _, root := range roots {
		realRoot, rootErr := existingRealDirectory(root)
		if rootErr != nil {
			continue
		}
		relative, relErr := filepath.Rel(realRoot, realCandidate)
		if relErr == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return realCandidate, nil
		}
	}
	return "", ErrWorkspaceOutsideRoots
}

func existingRealDirectory(path string) (string, error) {
	absolute, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return "", fmt.Errorf("channel: resolve workspace: %w", err)
	}
	realpath, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("channel: resolve workspace symlinks: %w", err)
	}
	info, err := os.Stat(realpath)
	if err != nil {
		return "", fmt.Errorf("channel: stat workspace: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("channel: workspace is not a directory")
	}
	return filepath.Clean(realpath), nil
}
