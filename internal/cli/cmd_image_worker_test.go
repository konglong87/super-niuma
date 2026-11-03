package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/config"
)

type imageWorkerTestRepository struct {
	closed bool
}

func (r *imageWorkerTestRepository) Close() error {
	r.closed = true
	return nil
}

type imageWorkerRunnerFunc func(context.Context) error

func (f imageWorkerRunnerFunc) Run(ctx context.Context) error { return f(ctx) }

func imageWorkerTestDependencies() imageWorkerCommandDependencies {
	return imageWorkerCommandDependencies{
		getenv: func(key string) string {
			if key == imageWorkerNameEnv {
				return "worker-test"
			}
			return ""
		},
		resolve: func(string, []string) (config.ResolvedImageGeneration, error) {
			return config.ResolvedImageGeneration{Enabled: true}, nil
		},
		openRepository: func(context.Context, string) (io.Closer, error) {
			return &imageWorkerTestRepository{}, nil
		},
		build: func(string, config.ResolvedImageGeneration, io.Closer, uint64, string, func(imageWorkerReadyState) error) (imageWorkerRunner, error) {
			return imageWorkerRunnerFunc(func(context.Context) error { return nil }), nil
		},
		writeReady:  func(string, imageWorkerReadyState) error { return nil },
		removeReady: func(string) error { return nil },
		workerID:    func(uint64) string { return "worker-test" },
	}
}

func TestImageWorkerCommandRejectsInvalidInvocationBeforeOpeningRepository(t *testing.T) {
	tests := []struct {
		name string
		args []string
		env  map[string]string
		want string
	}{
		{name: "missing run", args: nil, env: map[string]string{}, want: "image-worker requires run"},
		{name: "unknown action", args: []string{"start"}, env: map[string]string{}, want: "usage: image-worker run"},
		{name: "missing dsn", args: []string{"run"}, env: map[string]string{"NIUMA_IMAGE_WORKER_TENANT_ID": "7"}, want: "NIUMA_MYSQL_DSN"},
		{name: "missing tenant", args: []string{"run"}, env: map[string]string{"NIUMA_MYSQL_DSN": "dsn"}, want: "NIUMA_IMAGE_WORKER_TENANT_ID"},
		{name: "invalid tenant", args: []string{"run"}, env: map[string]string{"NIUMA_MYSQL_DSN": "dsn", "NIUMA_IMAGE_WORKER_TENANT_ID": "tenant-a"}, want: "positive integer"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			deps := imageWorkerTestDependencies()
			deps.getenv = func(key string) string { return test.env[key] }
			opened := false
			deps.openRepository = func(context.Context, string) (io.Closer, error) {
				opened = true
				return &imageWorkerTestRepository{}, nil
			}
			err := imageWorkerCommandWithDependencies(context.Background(), test.args, options{cwd: t.TempDir()}, &bytes.Buffer{}, deps)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
			if opened {
				t.Fatal("repository opened before preflight completed")
			}
		})
	}
}

func TestImageWorkerCommandRejectsInvalidWorkerNameBeforeOpeningRepository(t *testing.T) {
	for _, name := range []string{"bad/name", `bad"name`, ""} {
		t.Run(name, func(t *testing.T) {
			deps := imageWorkerTestDependencies()
			deps.getenv = func(key string) string {
				return map[string]string{imageWorkerDSNEnv: "dsn", imageWorkerTenantIDEnv: "7", imageWorkerNameEnv: name}[key]
			}
			opened := false
			deps.openRepository = func(context.Context, string) (io.Closer, error) {
				opened = true
				return &imageWorkerTestRepository{}, nil
			}
			err := imageWorkerCommandWithDependencies(context.Background(), []string{"run"}, options{cwd: t.TempDir()}, &bytes.Buffer{}, deps)
			if err == nil || !strings.Contains(err.Error(), "IMAGE_WORKER_NAME") {
				t.Fatalf("error = %v, want worker name validation", err)
			}
			if opened {
				t.Fatal("repository opened for invalid worker name")
			}
		})
	}
}

func TestImageWorkerCommandRejectsDisabledAndInvalidConfigurationBeforeOpeningRepository(t *testing.T) {
	tests := []struct {
		name    string
		resolve func(string, []string) (config.ResolvedImageGeneration, error)
		want    string
	}{
		{name: "disabled", resolve: func(string, []string) (config.ResolvedImageGeneration, error) {
			return config.ResolvedImageGeneration{}, nil
		}, want: "image generation is disabled"},
		{name: "invalid", resolve: func(string, []string) (config.ResolvedImageGeneration, error) {
			return config.ResolvedImageGeneration{}, errors.New("invalid worker lease")
		}, want: "image worker preflight failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			deps := imageWorkerTestDependencies()
			deps.getenv = func(key string) string {
				return map[string]string{"NIUMA_MYSQL_DSN": "dsn", "NIUMA_IMAGE_WORKER_TENANT_ID": "7"}[key]
			}
			deps.resolve = test.resolve
			opened := false
			deps.openRepository = func(context.Context, string) (io.Closer, error) {
				opened = true
				return &imageWorkerTestRepository{}, nil
			}
			err := imageWorkerCommandWithDependencies(context.Background(), []string{"run"}, options{cwd: t.TempDir()}, &bytes.Buffer{}, deps)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
			if opened {
				t.Fatal("repository opened for a disabled or invalid worker")
			}
		})
	}
}

func TestImageWorkerCommandWiresRepositoryUsesIndependentLifecycleAndCleansUp(t *testing.T) {
	deps := imageWorkerTestDependencies()
	repo := &imageWorkerTestRepository{}
	resolved := config.ResolvedImageGeneration{Enabled: true, Provider: "jiuan", Model: "gpt-image-2"}
	var mu sync.Mutex
	var openedDSN string
	var builtRepo io.Closer
	var builtTenant uint64
	var builtWorkerID string
	var readyStates []imageWorkerReadyState
	var removed []string
	started := make(chan struct{})
	deps.getenv = func(key string) string {
		return map[string]string{
			"NIUMA_MYSQL_DSN":               "mysql-secret-dsn",
			"NIUMA_IMAGE_WORKER_TENANT_ID":  "7",
			"NIUMA_IMAGE_WORKER_NAME":       "image-canary",
			"NIUMA_IMAGE_WORKER_READY_FILE": "/tmp/image-worker-test.ready",
		}[key]
	}
	deps.resolve = func(string, []string) (config.ResolvedImageGeneration, error) { return resolved, nil }
	deps.openRepository = func(_ context.Context, dsn string) (io.Closer, error) {
		openedDSN = dsn
		return repo, nil
	}
	deps.writeReady = func(path string, state imageWorkerReadyState) error {
		mu.Lock()
		defer mu.Unlock()
		if path != "/tmp/image-worker-test.ready" {
			t.Fatalf("ready path = %q", path)
		}
		readyStates = append(readyStates, state)
		return nil
	}
	deps.removeReady = func(path string) error {
		mu.Lock()
		defer mu.Unlock()
		removed = append(removed, path)
		return nil
	}
	deps.build = func(_ string, got config.ResolvedImageGeneration, gotRepo io.Closer, tenantID uint64, workerID string, onReady func(imageWorkerReadyState) error) (imageWorkerRunner, error) {
		if !reflect.DeepEqual(got, resolved) {
			t.Fatalf("resolved = %+v, want %+v", got, resolved)
		}
		builtRepo, builtTenant, builtWorkerID = gotRepo, tenantID, workerID
		return imageWorkerRunnerFunc(func(runCtx context.Context) error {
			if _, hasDeadline := runCtx.Deadline(); hasDeadline {
				t.Fatal("worker lifecycle inherited caller deadline")
			}
			if err := onReady(imageWorkerReadyState{TenantID: tenantID, WorkerID: workerID, Ready: true}); err != nil {
				return err
			}
			close(started)
			<-runCtx.Done()
			return runCtx.Err()
		}), nil
	}

	parent, cancel := context.WithTimeout(context.Background(), time.Hour)
	done := make(chan error, 1)
	go func() {
		done <- imageWorkerCommandWithDependencies(parent, []string{"run"}, options{cwd: t.TempDir()}, &bytes.Buffer{}, deps)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("clean shutdown error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}

	mu.Lock()
	defer mu.Unlock()
	if openedDSN != "mysql-secret-dsn" || builtRepo != repo || builtTenant != 7 || builtWorkerID != "image-canary" {
		t.Fatalf("wiring dsn=%q repo=%p tenant=%d worker=%q", openedDSN, builtRepo, builtTenant, builtWorkerID)
	}
	if !repo.closed || len(readyStates) != 1 || readyStates[0].TenantID != 7 || !readyStates[0].Ready || len(removed) != 2 {
		t.Fatalf("cleanup repo=%+v ready=%+v removed=%+v", repo, readyStates, removed)
	}
}

func TestImageWorkerCommandUsesExplicitSettingsFileOverAmbientConfiguration(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(home, ".niuma"))
	if err := os.MkdirAll(filepath.Join(home, ".niuma"), 0o700); err != nil {
		t.Fatal(err)
	}
	ambient := `{"imageGeneration":{"enabled":true,"provider":"ambient","model":"ambient-model"},"fallback":{"enabled":true,"providers":[{"name":"ambient","baseURL":"https://ambient.invalid/v1"}]}}`
	if err := os.WriteFile(filepath.Join(home, ".niuma", "settings.json"), []byte(ambient), 0o600); err != nil {
		t.Fatal(err)
	}
	suppliedPath := filepath.Join(t.TempDir(), "worker settings.json")
	supplied := `{"imageGeneration":{"enabled":true,"provider":"supplied","model":"supplied-model"},"fallback":{"enabled":true,"providers":[{"name":"supplied","baseURL":"https://supplied.example/v1","apiKey":"supplied-key"}]}}`
	if err := os.WriteFile(suppliedPath, []byte(supplied), 0o600); err != nil {
		t.Fatal(err)
	}
	deps := defaultImageWorkerCommandDependencies()
	deps.getenv = func(key string) string {
		return map[string]string{
			imageWorkerDSNEnv:      "mysql://dsn-secret",
			imageWorkerTenantIDEnv: "7",
			imageWorkerNameEnv:     "worker-explicit-settings",
		}[key]
	}
	var got config.ResolvedImageGeneration
	deps.openRepository = func(context.Context, string) (io.Closer, error) { return &imageWorkerTestRepository{}, nil }
	deps.build = func(_ string, resolved config.ResolvedImageGeneration, _ io.Closer, _ uint64, _ string, _ func(imageWorkerReadyState) error) (imageWorkerRunner, error) {
		got = resolved
		return imageWorkerRunnerFunc(func(context.Context) error { return nil }), nil
	}
	var output bytes.Buffer
	err := imageWorkerCommandWithDependencies(context.Background(), []string{"run"}, options{cwd: cwd, settingsInputs: []string{suppliedPath}}, &output, deps)
	if err != nil {
		t.Fatalf("explicit settings worker error = %v", err)
	}
	if got.Provider != "supplied" || got.Model != "supplied-model" || got.BaseURL != "https://supplied.example/v1" || got.APIKey != "supplied-key" {
		t.Fatalf("resolved settings = %+v", got)
	}
}

func TestImageWorkerCommandExplicitSettingsPreflightRejectsSuppliedInvalidProvider(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(home, ".niuma"))
	if err := os.MkdirAll(filepath.Join(home, ".niuma"), 0o700); err != nil {
		t.Fatal(err)
	}
	ambient := `{"imageGeneration":{"enabled":true,"provider":"ambient"},"fallback":{"providers":[{"name":"ambient","baseURL":"https://ambient.example/v1","apiKey":"ambient-key"}]}}`
	if err := os.WriteFile(filepath.Join(home, ".niuma", "settings.json"), []byte(ambient), 0o600); err != nil {
		t.Fatal(err)
	}
	suppliedPath := filepath.Join(t.TempDir(), "invalid settings.json")
	supplied := `{"imageGeneration":{"enabled":true,"provider":"supplied"},"fallback":{"providers":[{"name":"supplied","baseURL":"https://supplied.example/v1"}]}}`
	if err := os.WriteFile(suppliedPath, []byte(supplied), 0o600); err != nil {
		t.Fatal(err)
	}
	deps := defaultImageWorkerCommandDependencies()
	deps.getenv = func(key string) string {
		return map[string]string{imageWorkerDSNEnv: "mysql://dsn", imageWorkerTenantIDEnv: "7"}[key]
	}
	deps.openRepository = func(context.Context, string) (io.Closer, error) {
		t.Fatal("repository opened after supplied preflight failure")
		return nil, nil
	}
	err := imageWorkerCommandWithDependencies(context.Background(), []string{"run"}, options{cwd: cwd, settingsInputs: []string{suppliedPath}}, &bytes.Buffer{}, deps)
	if err == nil || !strings.Contains(err.Error(), "credential missing") {
		t.Fatalf("error = %v, want supplied provider credential failure", err)
	}
}

func TestImageWorkerScreenScriptStartStatusStopWithoutSecretsInArgv(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	script, err := filepath.Abs(filepath.Join(root, "scripts", "image-worker-screen.sh"))
	if err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	fakeBin := t.TempDir()
	fakeScreenState := filepath.Join(stateDir, "fake-screen")
	fakeGoFail := "0"
	settingsPath := filepath.Join(stateDir, "stable config", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"imageGeneration":{"enabled":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fakeGo := `#!/bin/sh
set -eu
output=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then output="$2"; shift 2; continue; fi
  shift
done
if [ "${FAKE_GO_FAIL:-0}" = 1 ]; then exit 42; fi
cat >"$output" <<'EOF'
#!/bin/sh
set -eu
printf '%s\n' "$@" >"${FAKE_SCREEN_STATE}.argv"
printf '{"ready":true,"tenant_id":%s,"worker_id":"%s"}\n' "$NIUMA_IMAGE_WORKER_TENANT_ID" "$NIUMA_IMAGE_WORKER_NAME" >"$NIUMA_IMAGE_WORKER_READY_FILE"
trap 'exit 0' TERM INT
while :; do sleep 1; done
EOF
chmod 700 "$output"
`
	fakeScreen := `#!/bin/sh
set -eu
state="${FAKE_SCREEN_STATE}"
case "${1:-}" in
  -ls)
    if [ -f "$state" ]; then
      read pid name <"$state"
      if kill -0 "$pid" 2>/dev/null; then printf '\t%s.%s\n' "$pid" "$name"; exit 0; fi
    fi
    exit 1
    ;;
  -dmS)
    name="$2"; shift 2
    "$@" &
    printf '%s %s\n' "$!" "$name" >"$state"
    ;;
  -S)
    if [ -f "$state" ]; then read pid name <"$state"; kill "$pid" 2>/dev/null || true; rm -f "$state"; fi
    ;;
  *) exit 2 ;;
esac
`
	for name, content := range map[string]string{"go": fakeGo, "screen": fakeScreen} {
		if err := os.WriteFile(filepath.Join(fakeBin, name), []byte(content), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	env := append(os.Environ(),
		"PATH="+fakeBin+":/usr/bin:/bin",
		"FAKE_SCREEN_STATE="+fakeScreenState,
		"NIUMA_IMAGE_WORKER_STATE_DIR="+stateDir,
		"NIUMA_IMAGE_WORKER_NAME=canary",
		"NIUMA_IMAGE_WORKER_TENANT_ID=7",
		"NIUMA_MYSQL_DSN=mysql://top-secret",
		"NIUMA_IMAGE_WORKER_SETTINGS_FILE="+settingsPath,
	)
	run := func(action string) (string, error) {
		command := exec.Command("/bin/bash", script, action)
		command.Dir = root
		command.Env = append(append([]string(nil), env...), "FAKE_GO_FAIL="+fakeGoFail)
		output, runErr := command.CombinedOutput()
		return string(output), runErr
	}
	t.Cleanup(func() { _, _ = run("stop") })
	if output, err := run("start"); err != nil {
		t.Fatalf("start error = %v, output = %s", err, output)
	}
	if output, err := run("status"); err != nil || !strings.Contains(output, "ready: niuma-image-canary") {
		t.Fatalf("status error = %v, output = %s", err, output)
	}
	envFile := filepath.Join(stateDir, "canary.env")
	info, err := os.Stat(envFile)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("env mode = %o, want 600", info.Mode().Perm())
	}
	fakeGoFail = "1"
	if output, err := run("restart"); err == nil {
		t.Fatalf("restart with failed candidate build unexpectedly succeeded: %s", output)
	}
	if output, err := run("status"); err != nil || !strings.Contains(output, "ready: niuma-image-canary") {
		t.Fatalf("failed restart did not preserve healthy worker: error=%v output=%s", err, output)
	}
	fakeGoFail = "0"
	argv, err := os.ReadFile(fakeScreenState + ".argv")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(argv), "top-secret") || !strings.Contains(string(argv), settingsPath) || !strings.Contains(string(argv), "image-worker\nrun") {
		t.Fatalf("unsafe or incomplete worker argv: %q", argv)
	}
	if output, err := run("stop"); err != nil || !strings.Contains(output, "stopped: niuma-image-canary") {
		t.Fatalf("stop error = %v, output = %s", err, output)
	}
	if _, err := os.Stat(envFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("env file remains after stop: %v", err)
	}
}

func TestImageWorkerScreenScriptRejectsMissingTenantBeforeBuild(t *testing.T) {
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "image-worker-screen.sh"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("/bin/bash", script, "start")
	command.Env = append(os.Environ(), "NIUMA_IMAGE_WORKER_NAME=worker-test", "NIUMA_MYSQL_DSN=mysql://secret", "NIUMA_IMAGE_WORKER_TENANT_ID=")
	output, runErr := command.CombinedOutput()
	if runErr == nil || !strings.Contains(string(output), "NIUMA_IMAGE_WORKER_TENANT_ID") {
		t.Fatalf("error = %v, output = %s", runErr, output)
	}
}

func TestImageWorkerScreenScriptRejectsInvalidWorkerNamesBeforeTouchingState(t *testing.T) {
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "image-worker-screen.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bad/name", `bad"name`, ""} {
		t.Run(name, func(t *testing.T) {
			stateDir := filepath.Join(t.TempDir(), "state")
			command := exec.Command("/bin/bash", script, "start")
			command.Env = append(os.Environ(),
				"NIUMA_IMAGE_WORKER_NAME="+name,
				"NIUMA_IMAGE_WORKER_STATE_DIR="+stateDir,
				"NIUMA_MYSQL_DSN=mysql://secret",
				"NIUMA_IMAGE_WORKER_TENANT_ID=7",
			)
			output, runErr := command.CombinedOutput()
			if runErr == nil || !strings.Contains(string(output), "IMAGE_WORKER_NAME") {
				t.Fatalf("error = %v, output = %s", runErr, output)
			}
			if _, statErr := os.Stat(stateDir); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("invalid worker name touched state dir: %v", statErr)
			}
		})
	}
}
