package updater

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/config"
)

const (
	defaultTimeout  = 30 * time.Second
	defaultStrategy = "git-ff-only"
)

type Result struct {
	Enabled bool
	Checked bool
	Updated bool
	Skipped bool
	Reason  string
	Output  string
}

type Options struct {
	Settings config.UpdateSettings
	CWD      string
	Version  string
	Env      []string
}

type CommandRunner interface {
	Run(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = env
	return cmd.CombinedOutput()
}

func CheckOnStartup(ctx context.Context, settings *config.UpdateSettings, cwd, version string) Result {
	if isTestBinary() {
		return Result{Skipped: true, Reason: "test binary"}
	}
	opts := Options{Settings: settingsWithDefaults(settings), CWD: cwd, Version: version, Env: os.Environ()}
	if shouldSkipScheduledCheck(opts.Settings) {
		return Result{Enabled: true, Skipped: true, Reason: "scheduled check not due"}
	}
	result := Run(ctx, opts, ExecRunner{})
	recordScheduledCheck(opts.Settings, result)
	return result
}

func CheckVersionSource(ctx context.Context, settings config.UpdateSettings, currentVersion string, client *http.Client) Result {
	url := strings.TrimSpace(settings.VersionSourceURL)
	if url == "" {
		return Result{Enabled: true, Checked: true, Skipped: true, Reason: "version source URL not configured"}
	}
	timeout := defaultTimeout
	if settings.TimeoutSeconds > 0 {
		timeout = time.Duration(settings.TimeoutSeconds) * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return resultFromCommand(false, nil, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return resultFromCommand(false, nil, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resultFromCommand(false, []byte(fmt.Sprintf("version source status %d", resp.StatusCode)), fmt.Errorf("version source status %d", resp.StatusCode))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return resultFromCommand(false, nil, err)
	}
	remote := strings.TrimSpace(string(data))
	output := remote
	if currentVersion != "" && remote != "" && remote != currentVersion {
		output = fmt.Sprintf("current=%s remote=%s", currentVersion, remote)
	}
	return Result{Enabled: true, Checked: true, Updated: false, Output: output}
}

func Run(ctx context.Context, opts Options, runner CommandRunner) Result {
	settings := settingsWithDefaults(&opts.Settings)
	if !boolValue(settings.Enabled, true) {
		return Result{Enabled: false, Skipped: true, Reason: "disabled"}
	}
	if !boolValue(settings.CheckOnStartup, true) {
		return Result{Enabled: true, Skipped: true, Reason: "startup check disabled"}
	}
	if runner == nil {
		runner = ExecRunner{}
	}
	timeout := defaultTimeout
	if settings.TimeoutSeconds > 0 {
		timeout = time.Duration(settings.TimeoutSeconds) * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if strings.TrimSpace(settings.VersionSourceURL) != "" && (boolValue(settings.CheckOnly, false) || !boolValue(settings.AutoPull, true)) {
		return CheckVersionSource(ctx, settings, opts.Version, nil)
	}

	repoDir, configuredRepo := resolveRepoDir(settings.RepoDir, opts.CWD)
	if repoDir == "" {
		return Result{Enabled: true, Checked: true, Skipped: true, Reason: "repo dir not found"}
	}
	if !isGitWorktree(ctx, runner, repoDir, opts.Env) {
		return Result{Enabled: true, Checked: true, Skipped: true, Reason: "not a git worktree"}
	}
	if !configuredRepo && !isGolangCCSourceRepo(ctx, runner, repoDir, opts.Env) {
		return Result{Enabled: true, Checked: true, Skipped: true, Reason: "not niuma source repo"}
	}
	if boolValue(settings.SkipWhenDirty, true) {
		if dirty, output := gitWorktreeDirty(ctx, runner, repoDir, opts.Env); dirty {
			return Result{Enabled: true, Checked: true, Skipped: true, Reason: "dirty worktree", Output: output}
		}
	}
	if strings.TrimSpace(settings.VersionSourceURL) != "" {
		versionResult := CheckVersionSource(ctx, settings, opts.Version, nil)
		if versionResult.Skipped || versionResult.Reason != "" {
			return versionResult
		}
	}
	if boolValue(settings.CheckOnly, false) || !boolValue(settings.AutoPull, true) {
		output, err := runner.Run(ctx, repoDir, opts.Env, "git", "fetch", "--quiet")
		return resultFromCommand(false, output, err)
	}
	if command := updateCommand(settings); command != "" {
		output, err := runShell(ctx, runner, repoDir, opts.Env, command)
		return resultFromCommand(true, output, err)
	}
	switch strings.TrimSpace(settings.Strategy) {
	case "", defaultStrategy:
		fetch, fetchErr := runner.Run(ctx, repoDir, opts.Env, "git", "fetch", "--quiet")
		if fetchErr != nil {
			return resultFromCommand(false, fetch, fetchErr)
		}
		pull, pullErr := runner.Run(ctx, repoDir, opts.Env, "git", "pull", "--ff-only", "--quiet")
		return resultFromCommand(true, appendOutput(fetch, pull), pullErr)
	case "check-only":
		output, err := runner.Run(ctx, repoDir, opts.Env, "git", "fetch", "--quiet")
		return resultFromCommand(false, output, err)
	default:
		return Result{Enabled: true, Checked: true, Skipped: true, Reason: "unsupported strategy: " + settings.Strategy}
	}
}

func settingsWithDefaults(settings *config.UpdateSettings) config.UpdateSettings {
	if settings == nil {
		settings = &config.UpdateSettings{}
	}
	copy := *settings
	trueValue := true
	if copy.Enabled == nil {
		copy.Enabled = &trueValue
	}
	if copy.CheckOnStartup == nil {
		copy.CheckOnStartup = &trueValue
	}
	if copy.AutoPull == nil {
		copy.AutoPull = &trueValue
	}
	if copy.SkipWhenDirty == nil {
		copy.SkipWhenDirty = &trueValue
	}
	if strings.TrimSpace(copy.Strategy) == "" {
		copy.Strategy = defaultStrategy
	}
	if copy.TimeoutSeconds <= 0 {
		copy.TimeoutSeconds = int(defaultTimeout / time.Second)
	}
	return copy
}

func updateCommand(settings config.UpdateSettings) string {
	if strings.TrimSpace(settings.CustomCommand) != "" {
		return strings.TrimSpace(settings.CustomCommand)
	}
	return strings.TrimSpace(settings.Command)
}

func boolValue(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func resolveRepoDir(configured, cwd string) (string, bool) {
	if strings.TrimSpace(configured) != "" {
		dir := strings.TrimSpace(os.ExpandEnv(configured))
		if abs, err := filepath.Abs(dir); err == nil {
			return abs, true
		}
		return dir, true
	}
	for _, dir := range []string{cwd, executableDir()} {
		dir = strings.TrimSpace(os.ExpandEnv(dir))
		if dir == "" {
			continue
		}
		if abs, err := filepath.Abs(dir); err == nil {
			return abs, false
		}
		return dir, false
	}
	return "", false
}

func executableDir() string {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		return ""
	}
	return filepath.Dir(exe)
}

func isGitWorktree(ctx context.Context, runner CommandRunner, dir string, env []string) bool {
	output, err := runner.Run(ctx, dir, env, "git", "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(string(output)) == "true"
}

func isGolangCCSourceRepo(ctx context.Context, runner CommandRunner, dir string, env []string) bool {
	output, err := runner.Run(ctx, dir, env, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return false
	}
	root := strings.TrimSpace(string(output))
	if root == "" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	return err == nil && strings.Contains(string(data), "module github.com/konglong87/super-niuma")
}

func gitWorktreeDirty(ctx context.Context, runner CommandRunner, dir string, env []string) (bool, string) {
	output, err := runner.Run(ctx, dir, env, "git", "status", "--porcelain")
	if err != nil {
		return true, strings.TrimSpace(string(output))
	}
	return strings.TrimSpace(string(output)) != "", strings.TrimSpace(string(output))
}

func runShell(ctx context.Context, runner CommandRunner, dir string, env []string, command string) ([]byte, error) {
	if runtime.GOOS == "windows" {
		return runner.Run(ctx, dir, env, "powershell", "-NoProfile", "-Command", command)
	}
	return runner.Run(ctx, dir, env, "sh", "-c", command)
}

func resultFromCommand(updated bool, output []byte, err error) Result {
	res := Result{
		Enabled: true,
		Checked: true,
		Updated: updated && err == nil,
		Output:  strings.TrimSpace(string(output)),
	}
	if err != nil {
		res.Updated = false
		res.Skipped = true
		res.Reason = "update failed"
		if errors.Is(err, context.DeadlineExceeded) {
			res.Reason = "update timeout"
		}
		if res.Output == "" {
			res.Output = err.Error()
		} else {
			res.Output = fmt.Sprintf("%s\n%s", res.Output, err)
		}
	}
	return res
}

func shouldSkipScheduledCheck(settings config.UpdateSettings) bool {
	interval, ok := parseScheduleInterval(settings.ScheduleInterval)
	if !ok || interval <= 0 {
		return false
	}
	statePath := scheduledCheckStatePath()
	data, err := os.ReadFile(statePath)
	if err != nil {
		return false
	}
	last, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(data)))
	if err != nil || last.IsZero() {
		return false
	}
	return time.Since(last) < interval
}

func recordScheduledCheck(settings config.UpdateSettings, result Result) {
	interval, ok := parseScheduleInterval(settings.ScheduleInterval)
	if !ok || interval <= 0 || !result.Checked {
		return
	}
	statePath := scheduledCheckStatePath()
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(statePath, []byte(time.Now().UTC().Format(time.RFC3339Nano)), 0o644)
}

func parseScheduleInterval(raw string) (time.Duration, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	duration, err := time.ParseDuration(raw)
	if err == nil {
		return duration, true
	}
	switch strings.ToLower(raw) {
	case "hourly":
		return time.Hour, true
	case "daily":
		return 24 * time.Hour, true
	case "weekly":
		return 7 * 24 * time.Hour, true
	default:
		return 0, false
	}
}

func scheduledCheckStatePath() string {
	if dir := strings.TrimSpace(os.Getenv("NIUMA_UPDATE_STATE_DIR")); dir != "" {
		return filepath.Join(dir, "last_check")
	}
	path, err := config.CurrentIdentity("").GlobalStatePath("update", "last_check")
	if err != nil || path == "" {
		return filepath.Join(os.TempDir(), "niuma-update-last-check")
	}
	return path
}

func appendOutput(first, second []byte) []byte {
	first = bytes.TrimSpace(first)
	second = bytes.TrimSpace(second)
	switch {
	case len(first) == 0:
		return second
	case len(second) == 0:
		return first
	default:
		return append(append(first, '\n'), second...)
	}
}

func isTestBinary() bool {
	base := filepath.Base(os.Args[0])
	return strings.HasSuffix(base, ".test") || strings.Contains(base, ".test.")
}
