package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/provisioning"
)

type fakeRunner struct {
	output []byte
	err    error
	action string
	env    []string
}

func TestReadWorkerEnvParsesPersistedValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "copywriter.env")
	if err := os.WriteFile(path, []byte("NIUMA_CHANNEL_TENANT_ID=1\nNIUMA_CHANNEL_ACCOUNT_ID=2\nNIUMA_CHANNEL_ACCOUNT_KEY='copywriter-feishu'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	values, err := readWorkerEnv(path)
	if err != nil {
		t.Fatal(err)
	}
	if values["NIUMA_CHANNEL_ACCOUNT_KEY"] != "copywriter-feishu" || values["NIUMA_CHANNEL_ACCOUNT_ID"] != "2" {
		t.Fatalf("values=%v", values)
	}
}

func TestScreenSupervisorListDiscoversWorkersByTenant(t *testing.T) {
	stateDir := t.TempDir()
	env := "NIUMA_CHANNEL_TENANT_ID=7\nNIUMA_CHANNEL_ACCOUNT_ID=9\nNIUMA_CHANNEL_ACCOUNT_KEY='writer'\nNIUMA_CHANNEL_MODEL_PROVIDER='glm-5.1'\nNIUMA_CHANNEL_MODEL='glm-5.1'\n"
	if err := os.WriteFile(filepath.Join(stateDir, "writer.env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := &fakeRunner{output: []byte("running: niuma-channel-writer\nlog: /tmp/writer.log\npids: 4242")}
	workers, err := (ScreenSupervisor{StateDir: stateDir, Runner: fake}).List(context.Background())
	if err != nil || len(workers) != 1 {
		t.Fatalf("workers=%#v err=%v", workers, err)
	}
	if workers[0].TenantID != 7 || workers[0].AccountID != 9 || workers[0].State != provisioning.WorkerStateRunning || workers[0].PID != 4242 {
		t.Fatalf("worker=%#v", workers[0])
	}
}

func TestScreenSupervisorListUsesPersistentDefaultStateDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	stateDir := filepath.Join(home, ".niuma", "channel-workers")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	env := "NIUMA_CHANNEL_TENANT_ID=7\nNIUMA_CHANNEL_ACCOUNT_ID=9\nNIUMA_CHANNEL_ACCOUNT_KEY='writer'\n"
	if err := os.WriteFile(filepath.Join(stateDir, "writer.env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := &fakeRunner{output: []byte("running: niuma-channel-writer\nlog: /persistent/writer.log\npids: 4242")}
	workers, err := (ScreenSupervisor{Runner: fake}).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(workers) != 1 || workers[0].AccountKey != "writer" || workers[0].PID != 4242 {
		t.Fatalf("workers=%#v", workers)
	}
}

func (f *fakeRunner) Run(_ context.Context, _ string, args []string, env []string) ([]byte, error) {
	f.action = args[0]
	f.env = env
	return f.output, f.err
}

func TestScreenSupervisorMapsStatusAndStructuredEnv(t *testing.T) {
	fake := &fakeRunner{output: []byte("running: niuma-channel-writer\nlog: /tmp/writer.log\npids: 42")}
	s := ScreenSupervisor{ScriptPath: "scripts/channel-worker-screen.sh", Runner: fake}
	status, err := s.Start(context.Background(), provisioning.WorkerSpec{AccountKey: "writer", Provider: "glm-5.1", Model: "glm-5.1", SettingsRef: "/settings.json", Environment: map[string]string{"NIUMA_CHANNEL_STREAMING": "on"}})
	if err != nil || status.State != provisioning.WorkerStateRunning || status.PID != 42 || fake.action != "start" {
		t.Fatalf("status=%#v err=%v runner=%#v", status, err, fake)
	}
	joined := strings.Join(fake.env, "\n")
	if !strings.Contains(joined, "NIUMA_CHANNEL_STREAMING=on") {
		t.Fatalf("env=%s", joined)
	}
}

func TestScreenSupervisorPinsDesktopRuntimeEnvironment(t *testing.T) {
	fake := &fakeRunner{output: []byte("stopped: niuma-channel-writer")}
	s := ScreenSupervisor{
		Runner:  fake,
		BaseEnv: []string{"NIUMA_SQLITE_PATH=/desktop/current.sqlite", "NIUMA_FEISHU_CREDENTIAL_FILE=/desktop/credentials.json"},
	}
	_, err := s.Start(context.Background(), provisioning.WorkerSpec{
		AccountKey: "writer",
		Environment: map[string]string{
			"NIUMA_SQLITE_PATH":            "/worker/stale.sqlite",
			"NIUMA_FEISHU_CREDENTIAL_FILE": "/worker/stale-credentials.json",
			"NIUMA_CHANNEL_STREAMING":      "on",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(fake.env, "\n")
	if strings.Contains(joined, "stale.sqlite") || !strings.Contains(joined, "NIUMA_SQLITE_PATH=/desktop/current.sqlite") {
		t.Fatalf("sqlite environment was not pinned: %s", joined)
	}
	if !strings.Contains(joined, "NIUMA_FEISHU_CREDENTIAL_FILE=/worker/stale-credentials.json") {
		t.Fatalf("worker credential environment was not preserved: %s", joined)
	}
}
