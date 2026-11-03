package computerbridge

import (
	"encoding/json"
	"io"
	"os"
)

// Only the descriptor number enters the child environment. The bridge secret
// travels through an inherited pipe, never argv, logs, or a disk file.
const LaunchFDEnv = "NIUMA_COMPUTER_BRIDGE_FD"
const LaunchFD = "3"
const maxLaunchBytes = 8192

type launchConfig struct {
	SocketPath string `json:"socket_path"`
	Token      string `json:"token"`
}

func NewLaunchFile(cfg Config) (*os.File, error) {
	if _, err := NewClient(cfg); err != nil {
		return nil, err
	}
	data, err := json.Marshal(launchConfig{SocketPath: cfg.SocketPath, Token: cfg.Token})
	if err != nil || len(data) > maxLaunchBytes {
		return nil, ErrInvalidConfig
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, ErrInvalidConfig
	}
	// The fixed size is below the supported desktop platform pipe capacity.
	_, err = writer.Write(data)
	closeErr := writer.Close()
	if err != nil || closeErr != nil {
		reader.Close()
		return nil, ErrInvalidConfig
	}
	return reader, nil
}

// ConsumeLaunchConfig clears the marker before any tools/subprocesses start and
// closes the descriptor immediately. Non-desktop servers never acquire a grant.
func ConsumeLaunchConfig(desktopLocal bool) (*Config, error) {
	marker, exists := os.LookupEnv(LaunchFDEnv)
	_ = os.Unsetenv(LaunchFDEnv)
	if !exists {
		return nil, nil
	}
	if marker != LaunchFD {
		return nil, ErrInvalidConfig
	}
	file := os.NewFile(3, "computer-bridge-launch")
	if file == nil {
		return nil, ErrInvalidConfig
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		return nil, ErrInvalidConfig
	}
	if !desktopLocal {
		return nil, ErrInvalidConfig
	}
	return readLaunchConfig(file)
}
func readLaunchConfig(reader io.Reader) (*Config, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxLaunchBytes+1))
	if err != nil || len(data) > maxLaunchBytes {
		return nil, ErrInvalidConfig
	}
	var wire launchConfig
	if decodeStrict(data, &wire) != nil {
		return nil, ErrInvalidConfig
	}
	cfg := Config{SocketPath: wire.SocketPath, Token: wire.Token}
	if _, err := NewClient(cfg); err != nil {
		return nil, ErrInvalidConfig
	}
	return &cfg, nil
}
