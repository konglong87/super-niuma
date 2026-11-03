// Package super defines shared, side-effect-free Super application composition.
package super

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const (
	StateDirectoryName = ".super-niuma"
	DatabaseName       = "work-orders.db"
	StateDirectoryEnv  = "SUPER_NIUMA_STATE_DIR"
)

// StateDirectory resolves isolated Super state without creating or inspecting
// files. An explicit invalid override fails closed; it never falls back to the
// user's real state. Neither legacy Desktop configuration nor Sessions are read.
func StateDirectory() (string, error) {
	directory, overridden := os.LookupEnv(StateDirectoryEnv)
	if !overridden {
		home, err := os.UserHomeDir()
		if err != nil || !filepath.IsAbs(home) {
			return "", errors.New("invalid Super state directory")
		}
		directory = filepath.Join(home, StateDirectoryName)
	}
	if !filepath.IsAbs(directory) || strings.ContainsRune(directory, 0) {
		return "", errors.New("invalid Super state directory")
	}
	return filepath.Clean(directory), nil
}
