package main

import (
	"context"
	"fmt"
	"github.com/konglong87/super-niuma/internal/computerbridge"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	localServiceReadinessTimeout   = 15 * time.Second
	localServicePollInterval       = 100 * time.Millisecond
	localServiceStartupMaxAttempts = 3
	localServiceStartupRetryDelay  = 500 * time.Millisecond
)

type localServiceState string

const (
	localServiceStopped  localServiceState = "stopped"
	localServiceStarting localServiceState = "starting"
	localServiceReady    localServiceState = "ready"
	localServiceFailed   localServiceState = "failed"
)

type LocalServiceStatus struct {
	State string `json:"state"`
	PID   int    `json:"pid,omitempty"`
	Port  int    `json:"port"`
	Error string `json:"error,omitempty"`
}

type localServiceConfig struct {
	computerBridge *computerbridge.Config
	executable     string
	workspace      string
	port           int
	token          string
	env            []string
	dir            string
	logPath        string
}

type localServiceProcess struct {
	cmd            *exec.Cmd
	done           chan struct{}
	log            *os.File
	expectedExit   bool
	startupAttempt int
}

type localServiceController struct {
	mu              sync.Mutex
	operationMu     sync.Mutex
	config          localServiceConfig
	process         *localServiceProcess
	state           localServiceState
	lastError       string
	startupAttempts int
}

func (a *app) RestartLocalService() error {
	a.mu.Lock()
	service := a.service
	a.mu.Unlock()
	if service == nil {
		return fmt.Errorf("local service is not configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), localServiceReadinessTimeout+5*time.Second)
	defer cancel()
	if err := service.restart(ctx); err != nil {
		startupLog("restart local service: " + err.Error())
		return err
	}
	return nil
}

func (a *app) GetLocalServiceStatus() LocalServiceStatus {
	a.mu.Lock()
	service := a.service
	a.mu.Unlock()
	if service == nil {
		return LocalServiceStatus{State: string(localServiceFailed), Port: a.port, Error: "local service is not configured"}
	}
	return service.status()
}

func (s *localServiceController) setSessionBackend(backend string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value := desktopSessionBackendEnv + "=" + backend
	for index, env := range s.config.env {
		if strings.HasPrefix(env, desktopSessionBackendEnv+"=") {
			s.config.env[index] = value
			return
		}
	}
	s.config.env = append(s.config.env, value)
}

func newLocalServiceController(config localServiceConfig) *localServiceController {
	return &localServiceController{
		config: config,
		state:  localServiceStopped,
	}
}

func (s *localServiceController) start() error {
	s.operationMu.Lock()
	defer s.operationMu.Unlock()

	s.mu.Lock()
	if s.process == nil {
		s.startupAttempts = 0
	}
	s.mu.Unlock()
	return s.startLocked()
}

func (s *localServiceController) startLocked() error {
	s.mu.Lock()
	if s.process != nil {
		s.mu.Unlock()
		return nil
	}
	s.state = localServiceStarting
	s.lastError = ""
	s.startupAttempts++
	startupAttempt := s.startupAttempts
	config := s.config
	s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(config.logPath), 0o700); err != nil {
		return s.failStart(fmt.Errorf("create local service log directory: %w", err))
	}
	serverLog, err := os.OpenFile(config.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return s.failStart(fmt.Errorf("open local service log: %w", err))
	}

	cmd := exec.Command(
		config.executable,
		"--cwd", config.workspace,
		"server",
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(config.port),
		"--auth-token", config.token,
		"--desktop-local",
	)
	cmd.Env = append(os.Environ(), config.env...)
	// Never inherit a stale marker from the launching shell.
	cleanEnv := cmd.Env[:0]
	for _, value := range cmd.Env {
		if !strings.HasPrefix(value, computerbridge.LaunchFDEnv+"=") {
			cleanEnv = append(cleanEnv, value)
		}
	}
	cmd.Env = cleanEnv
	if config.computerBridge != nil {
		launchFile, err := computerbridge.NewLaunchFile(*config.computerBridge)
		if err != nil {
			_ = serverLog.Close()
			return s.failStart(fmt.Errorf("prepare computer bridge launch: %w", err))
		}
		defer launchFile.Close()
		cmd.ExtraFiles = []*os.File{launchFile}
		cmd.Env = append(cmd.Env, computerbridge.LaunchFDEnv+"="+computerbridge.LaunchFD)
	}
	cmd.Dir = config.dir
	cmd.Stdout = serverLog
	cmd.Stderr = serverLog
	if err := cmd.Start(); err != nil {
		_ = serverLog.Close()
		return s.failStart(fmt.Errorf("start local service: %w", err))
	}

	process := &localServiceProcess{
		cmd:            cmd,
		done:           make(chan struct{}),
		log:            serverLog,
		startupAttempt: startupAttempt,
	}
	s.mu.Lock()
	s.process = process
	s.state = localServiceStarting
	s.mu.Unlock()

	startupLog(fmt.Sprintf("server started pid=%d port=%d attempt=%d", cmd.Process.Pid, config.port, startupAttempt))
	go s.waitForExit(process)
	go s.observeReadiness(process)
	return nil
}

func (s *localServiceController) stop(ctx context.Context) error {
	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	return s.stopLocked(ctx)
}

func (s *localServiceController) stopLocked(ctx context.Context) error {
	s.mu.Lock()
	process := s.process
	if process == nil {
		s.state = localServiceStopped
		s.lastError = ""
		s.mu.Unlock()
		return nil
	}
	process.expectedExit = true
	s.mu.Unlock()

	if err := process.cmd.Process.Signal(os.Interrupt); err != nil {
		startupLog(fmt.Sprintf("stop local service pid=%d: %v", process.cmd.Process.Pid, err))
	}

	select {
	case <-process.done:
	case <-ctx.Done():
		_ = process.cmd.Process.Kill()
		<-process.done
		return ctx.Err()
	}
	return nil
}

func (s *localServiceController) restart(ctx context.Context) error {
	s.operationMu.Lock()
	defer s.operationMu.Unlock()

	if err := s.stopLocked(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	s.startupAttempts = 0
	s.mu.Unlock()
	if err := s.startLocked(); err != nil {
		return err
	}

	readinessCtx, cancel := context.WithTimeout(ctx, localServiceReadinessTimeout)
	defer cancel()
	if !s.waitForReady(readinessCtx) {
		err := fmt.Errorf("local service did not become ready on port %d", s.config.port)
		s.markFailed(err)
		return err
	}
	s.markReady()
	return nil
}

func (s *localServiceController) status() LocalServiceStatus {
	s.mu.Lock()
	defer s.mu.Unlock()

	status := LocalServiceStatus{
		State: string(s.state),
		Port:  s.config.port,
		Error: s.lastError,
	}
	if s.process != nil && s.process.cmd.Process != nil {
		status.PID = s.process.cmd.Process.Pid
	}
	return status
}

func (s *localServiceController) waitForExit(process *localServiceProcess) {
	err := process.cmd.Wait()
	_ = process.log.Close()

	retry := false
	s.mu.Lock()
	if s.process == process {
		s.process = nil
		if process.expectedExit {
			s.state = localServiceStopped
			s.lastError = ""
		} else {
			s.state = localServiceFailed
			s.lastError = processExitMessage(err)
			retry = process.startupAttempt < localServiceStartupMaxAttempts
		}
	}
	s.mu.Unlock()
	close(process.done)
	startupLog(fmt.Sprintf("server exited pid=%d err=%v attempt=%d", process.cmd.Process.Pid, err, process.startupAttempt))
	if retry {
		startupLog(fmt.Sprintf("server startup retry scheduled attempt=%d", process.startupAttempt+1))
		go s.retryStart()
	}
}

func (s *localServiceController) retryStart() {
	timer := time.NewTimer(localServiceStartupRetryDelay)
	defer timer.Stop()
	<-timer.C

	s.operationMu.Lock()
	defer s.operationMu.Unlock()

	s.mu.Lock()
	shouldRetry := s.process == nil && s.state == localServiceFailed && s.startupAttempts < localServiceStartupMaxAttempts
	s.mu.Unlock()
	if !shouldRetry {
		return
	}

	if err := s.startLocked(); err != nil {
		startupLog("server startup retry failed: " + err.Error())
	}
}

func (s *localServiceController) observeReadiness(process *localServiceProcess) {
	ctx, cancel := context.WithTimeout(context.Background(), localServiceReadinessTimeout)
	defer cancel()
	if s.waitForReady(ctx) {
		s.mu.Lock()
		if s.process == process {
			s.state = localServiceReady
			s.lastError = ""
		}
		s.mu.Unlock()
		startupLog(fmt.Sprintf("server ready port=%d", s.config.port))
		return
	}

	s.mu.Lock()
	if s.process == process {
		s.state = localServiceFailed
		s.lastError = fmt.Sprintf("local service did not become ready on port %d", s.config.port)
	}
	s.mu.Unlock()
	startupLog(fmt.Sprintf("server not ready port=%d", s.config.port))
}

func (s *localServiceController) waitForReady(ctx context.Context) bool {
	ticker := time.NewTicker(localServicePollInterval)
	defer ticker.Stop()
	for {
		if s.serverEndpointReady(ctx, "/health") && s.serverEndpointReady(ctx, "/readyz") {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}

func (s *localServiceController) serverEndpointReady(ctx context.Context, path string) bool {
	reqCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d%s", s.config.port, path), nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+s.config.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (s *localServiceController) failStart(err error) error {
	s.markFailed(err)
	return err
}

func (s *localServiceController) markReady() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.process != nil {
		s.state = localServiceReady
		s.lastError = ""
	}
}

func (s *localServiceController) markFailed(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = localServiceFailed
	s.lastError = err.Error()
}

func processExitMessage(err error) string {
	if err == nil {
		return "local service exited unexpectedly"
	}
	return err.Error()
}
