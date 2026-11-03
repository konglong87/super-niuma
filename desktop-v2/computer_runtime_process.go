package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/konglong87/super-niuma/internal/computerbridge"
)

const (
	computerRuntimeProcessFlag = "--computer-runtime-process"
	computerRuntimeDataDirFlag = "--computer-runtime-data-dir"
	computerRuntimeConfigFD    = 3
	computerRuntimeProtocol    = "computer-runtime.v1"
	computerRuntimeStartWait   = 15 * time.Second
	computerRuntimeStopGrace   = 2 * time.Second
	computerRuntimeKillGrace   = 500 * time.Millisecond
)

type computerRuntimeHandshake struct {
	ProtocolVersion string `json:"protocol_version"`
	PID             int    `json:"pid"`
	BinarySHA256    string `json:"binary_sha256"`
	SocketPath      string `json:"socket_path"`
	Token           string `json:"token"`
}

type computerRuntimeProcess struct {
	cmd             *exec.Cmd
	config          computerbridge.Config
	client          *computerbridge.Client
	done            chan error
	exitDone        chan struct{}
	exitOnce        sync.Once
	waitMonitorOnce sync.Once
	exitMu          sync.Mutex
	exitErr         error

	stopMu  sync.Mutex
	stopErr error
}

func (p *computerRuntimeProcess) Config() computerbridge.Config {
	if p == nil {
		return computerbridge.Config{}
	}
	return p.config
}

func (p *computerRuntimeProcess) Client() *computerbridge.Client {
	if p == nil {
		return nil
	}
	return p.client
}

func (p *computerRuntimeProcess) startWaitMonitor() {
	if p == nil || p.done == nil {
		return
	}
	p.waitMonitorOnce.Do(func() {
		go func() { p.recordExit(<-p.done) }()
	})
}

func (p *computerRuntimeProcess) recordExit(err error) {
	if p == nil {
		return
	}
	p.exitOnce.Do(func() {
		p.exitMu.Lock()
		p.exitErr = normalizeRuntimeProcessExit(err)
		p.exitMu.Unlock()
		close(p.exitDone)
	})
}

func (p *computerRuntimeProcess) wait(ctx context.Context) error {
	if p == nil || p.exitDone == nil {
		return nil
	}
	select {
	case <-p.exitDone:
		p.exitMu.Lock()
		err := p.exitErr
		p.exitMu.Unlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *computerRuntimeProcess) Stop(ctx context.Context) error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// Stop is serialized, but it is intentionally not one-shot. A caller can
	// time out while the worker is still draining; a later explicit Stop must
	// be able to retry and converge instead of being blocked by a cached error.
	p.stopMu.Lock()
	defer p.stopMu.Unlock()

	if p.exited() {
		return nil
	}

	var bridgeErr error
	stopCtx, cancel := context.WithTimeout(context.Background(), computerRuntimeStopGrace)
	if p.client != nil {
		// The bridge is a control plane, not the process owner. If it is
		// already gone, fall back to the authenticated process handle and
		// confirm exit; never leave a worker alive because Stop RPC failed.
		bridgeErr = p.client.StopRuntime(stopCtx, computerRuntimeSupervisionOwner)
	}
	cancel()

	// The process handle is authoritative for teardown. Do not let a short
	// caller deadline prevent the bounded native shutdown and final kill.
	interruptErr := p.cmd.Process.Signal(os.Interrupt)
	if interruptErr != nil && !errors.Is(interruptErr, os.ErrProcessDone) {
		if p.waitWithGrace(computerRuntimeKillGrace); p.exited() {
			return nil
		}
		return errors.Join(bridgeErr, interruptErr)
	}
	if p.waitWithGrace(computerRuntimeStopGrace); p.exited() {
		return nil
	}

	// A runtime that ignores or fails to process SIGINT must not survive an
	// explicit Stop. SIGKILL is used only after the graceful bounded phase;
	// it never changes an already-recorded action receipt or authorizes replay.
	killErr := p.cmd.Process.Kill()
	if killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
		if p.waitWithGrace(computerRuntimeKillGrace); p.exited() {
			return nil
		}
		return errors.Join(bridgeErr, killErr)
	}
	if waitErr := p.waitWithGrace(computerRuntimeKillGrace); !p.exited() {
		return errors.Join(bridgeErr, waitErr)
	}
	return nil
}

func (p *computerRuntimeProcess) exited() bool {
	if p == nil || p.exitDone == nil {
		return true
	}
	select {
	case <-p.exitDone:
		return true
	default:
		return false
	}
}

func (p *computerRuntimeProcess) exitError() error {
	if p == nil {
		return nil
	}
	p.exitMu.Lock()
	defer p.exitMu.Unlock()
	return p.exitErr
}

func (p *computerRuntimeProcess) waitWithGrace(grace time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	return p.wait(ctx)
}

func normalizeRuntimeProcessExit(err error) error {
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 0 {
		return nil
	}
	return err
}

func startDesktopComputerRuntime(dataDir string) (*RuntimeSupervisor, error) {
	if !filepath.IsAbs(dataDir) || filepath.Clean(dataDir) != dataDir || strings.ContainsRune(dataDir, 0) {
		return nil, errors.New("runtime parent directory must be an absolute clean path")
	}
	runtimeRoot := filepath.Join(dataDir, "computer-runtime")
	if err := ensurePrivateDirectory(runtimeRoot); err != nil {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate desktop runtime executable: %w", err)
	}
	binarySHA256, err := fileSHA256(executable)
	if err != nil {
		return nil, fmt.Errorf("hash desktop runtime executable: %w", err)
	}
	readConfig, writeConfig, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create runtime handshake pipe: %w", err)
	}
	cmd := exec.Command(executable, computerRuntimeProcessFlag, computerRuntimeDataDirFlag, runtimeRoot)
	cmd.ExtraFiles = []*os.File{writeConfig}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.Env = appendRuntimeProcessEnv(os.Environ())
	if err := cmd.Start(); err != nil {
		_ = readConfig.Close()
		_ = writeConfig.Close()
		return nil, fmt.Errorf("start standalone computer runtime: %w", err)
	}
	_ = writeConfig.Close()

	handshake, err := readRuntimeHandshake(readConfig, computerRuntimeStartWait)
	_ = readConfig.Close()
	if err != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		return nil, err
	}
	if handshake.PID != cmd.Process.Pid || handshake.ProtocolVersion != computerRuntimeProtocol || handshake.BinarySHA256 != binarySHA256 {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		return nil, errors.New("standalone computer runtime identity mismatch")
	}
	client, err := computerbridge.NewClient(computerbridge.Config{
		SocketPath:     handshake.SocketPath,
		Token:          handshake.Token,
		RequestTimeout: computerbridge.DefaultRequestTimeout,
	})
	if err != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		return nil, fmt.Errorf("validate standalone computer runtime endpoint: %w", err)
	}
	probeCtx, cancel := context.WithTimeout(context.Background(), computerRuntimeStartWait)
	defer cancel()
	if _, err := client.RuntimeStatus(probeCtx, computerRuntimeSupervisionOwner); err != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		return nil, fmt.Errorf("probe standalone computer runtime: %w", err)
	}
	process := &computerRuntimeProcess{cmd: cmd, config: clientConfig(client, handshake), client: client, done: make(chan error, 1), exitDone: make(chan struct{})}
	go func() { process.done <- cmd.Wait() }()
	process.startWaitMonitor()
	return newProcessComputerRuntime(process), nil
}

func clientConfig(client *computerbridge.Client, handshake computerRuntimeHandshake) computerbridge.Config {
	// Client intentionally does not expose its token. The parent already has
	// the validated handshake, so keep the exact in-memory config here.
	return computerbridge.Config{SocketPath: handshake.SocketPath, Token: handshake.Token, RequestTimeout: computerbridge.DefaultRequestTimeout}
}

func readRuntimeHandshake(file *os.File, timeout time.Duration) (computerRuntimeHandshake, error) {
	if file == nil {
		return computerRuntimeHandshake{}, errors.New("runtime handshake pipe is unavailable")
	}
	result := make(chan struct {
		handshake computerRuntimeHandshake
		err       error
	}, 1)
	go func() {
		var handshake computerRuntimeHandshake
		err := json.NewDecoder(bufio.NewReader(io.LimitReader(file, 16<<10))).Decode(&handshake)
		result <- struct {
			handshake computerRuntimeHandshake
			err       error
		}{handshake, err}
	}()
	select {
	case value := <-result:
		if value.err != nil {
			return computerRuntimeHandshake{}, fmt.Errorf("read standalone computer runtime handshake: %w", value.err)
		}
		if !filepath.IsAbs(value.handshake.SocketPath) || strings.ContainsRune(value.handshake.SocketPath, 0) || value.handshake.PID <= 0 || value.handshake.BinarySHA256 == "" || value.handshake.Token == "" {
			return computerRuntimeHandshake{}, errors.New("invalid standalone computer runtime handshake")
		}
		return value.handshake, nil
	case <-time.After(timeout):
		return computerRuntimeHandshake{}, errors.New("standalone computer runtime handshake timed out")
	}
}

func appendRuntimeProcessEnv(env []string) []string {
	filtered := make([]string, 0, len(env)+1)
	for _, value := range env {
		if strings.HasPrefix(value, "NIUMA_COMPUTER_RUNTIME_CONFIG_FD=") {
			continue
		}
		filtered = append(filtered, value)
	}
	return filtered
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func ensurePrivateDirectory(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsRune(path, 0) {
		return errors.New("runtime directory must be an absolute clean path")
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("runtime directory must not be a symlink")
	}
	return validatePrivateRuntimeDirectory(path, info)
}

func parseComputerRuntimeProcessArgs(args []string) (string, bool, error) {
	found := false
	dataDir := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case computerRuntimeProcessFlag:
			if found {
				return "", true, errors.New("duplicate computer runtime process flag")
			}
			found = true
		case computerRuntimeDataDirFlag:
			if i+1 >= len(args) || dataDir != "" || !filepath.IsAbs(args[i+1]) {
				return "", found, errors.New("computer runtime data directory must be absolute")
			}
			i++
			dataDir = args[i]
		default:
			if found {
				return "", true, fmt.Errorf("unexpected computer runtime argument")
			}
		}
	}
	if !found {
		return "", false, nil
	}
	if dataDir == "" {
		return "", true, errors.New("computer runtime data directory is required")
	}
	return dataDir, true, nil
}

func runComputerRuntimeProcess(dataDir string) error {
	if err := ensurePrivateDirectory(dataDir); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := newComputerManagerWithRuntime(ctx, nil)
	stopRuntime := make(chan struct{}, 1)
	listener, err := startDesktopComputerBridgeWithRuntimeStop(ctx, dataDir, manager, func() {
		select {
		case stopRuntime <- struct{}{}:
		default:
		}
	})
	if err != nil {
		return err
	}
	defer listener.Close()
	config := listener.Config()
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	binarySHA256, err := fileSHA256(executable)
	if err != nil {
		return err
	}
	configFile := os.NewFile(uintptr(computerRuntimeConfigFD), "computer-runtime-config")
	if configFile == nil {
		return errors.New("computer runtime handshake fd is unavailable")
	}
	handshake := computerRuntimeHandshake{ProtocolVersion: computerRuntimeProtocol, PID: os.Getpid(), BinarySHA256: binarySHA256, SocketPath: config.SocketPath, Token: config.Token}
	if err := json.NewEncoder(configFile).Encode(handshake); err != nil {
		_ = configFile.Close()
		return err
	}
	_ = configFile.Close()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	select {
	case <-signals:
	case <-stopRuntime:
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), computerRequestTimeout)
	defer stopCancel()
	return manager.close(stopCtx)
}
