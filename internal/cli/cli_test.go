package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/konglong87/super-niuma/internal/agents"
	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/agenttasks/memstore"
	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/background"
	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/defaults"
	goalpkg "github.com/konglong87/super-niuma/internal/goal"
	"github.com/konglong87/super-niuma/internal/goalcmd"
	"github.com/konglong87/super-niuma/internal/memory"
	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/promptmode"
	"github.com/konglong87/super-niuma/internal/query"
	"github.com/konglong87/super-niuma/internal/recap"
	"github.com/konglong87/super-niuma/internal/runtimeprofile"
	"github.com/konglong87/super-niuma/internal/scheduler"
	"github.com/konglong87/super-niuma/internal/server"
	"github.com/konglong87/super-niuma/internal/session"
	"github.com/konglong87/super-niuma/internal/sessiondiag"
	"github.com/konglong87/super-niuma/internal/slashcommands"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	"github.com/konglong87/super-niuma/internal/telemetry"
	tenantservice "github.com/konglong87/super-niuma/internal/tenant"
	"github.com/konglong87/super-niuma/internal/tools"
	"github.com/konglong87/super-niuma/internal/tui"
)

func setTestConfigRoot(t *testing.T, root string) {
	t.Helper()
	t.Setenv("NIUMA_CONFIG_DIR", root)
	t.Setenv("CLAUDE_CONFIG_DIR", root)
}

// Configure the isolated owned settings file, never upstream process credentials.
func configureTestProvider(t *testing.T, endpoint, key string) {
	t.Helper()
	settings := config.LoadGlobalSettings()
	settings.Provider = "anthropic-compatible"
	settings.ProviderProtocol = config.ProviderProtocolAnthropicMessages
	settings.BaseURL = endpoint
	settings.APIKey = key
	if settings.Model == "" {
		settings.Model = "test-model"
	}
	if err := config.SaveGlobalSettings(settings); err != nil {
		t.Fatal(err)
	}
}

func TestScheduleRunDispatchesSessionMonitorChildWithoutGenericQuery(t *testing.T) {
	root := t.TempDir()
	setTestConfigRoot(t, root)
	bgStore := background.Store{Root: root}
	bg, err := bgStore.CreateWithOptions(background.Options{Prompt: "session monitor", Kind: "session-monitor", IntervalSeconds: 300})
	if err != nil {
		t.Fatal(err)
	}
	scheduleStore := scheduler.Store{Root: root}
	schedule, err := scheduleStore.Create(scheduler.Options{Prompt: "session monitor", Kind: "session-monitor", Spec: "@every 5m", IntervalSeconds: 300}, bg)
	if err != nil {
		t.Fatal(err)
	}
	previous := runSessionMonitorChild
	defer func() { runSessionMonitorChild = previous }()
	called := ""
	runSessionMonitorChild = func(_ context.Context, scheduleID string) error {
		called = scheduleID
		return nil
	}
	if err := scheduleRunCommand(context.Background(), []string{schedule.ID}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if called != schedule.ID {
		t.Fatalf("monitor child schedule ID = %q, want %q", called, schedule.ID)
	}
}

func TestAuthStatus(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	configureTestProvider(t, "https://model.example.test", "key")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"auth", "status"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"loggedIn": true`) || !strings.Contains(out.String(), `"api_key"`) {
		t.Fatalf("output = %s", out.String())
	}
}

func TestEnsureLocalAgentTaskRuntimeDefaultsToMemoryStore(t *testing.T) {
	var opts options
	ensureLocalAgentTaskRuntime(&opts)
	if _, ok := opts.agentTaskStore.(*memstore.Store); !ok {
		t.Fatalf("agentTaskStore = %T", opts.agentTaskStore)
	}
	if opts.agentTaskController == nil {
		t.Fatal("expected local task controller")
	}

	existingStore := memstore.New()
	existingController := agenttasks.NewController()
	opts = options{agentTaskStore: existingStore, agentTaskController: existingController}
	ensureLocalAgentTaskRuntime(&opts)
	if opts.agentTaskStore != existingStore {
		t.Fatalf("store was overwritten: %T", opts.agentTaskStore)
	}
	if opts.agentTaskController != existingController {
		t.Fatal("controller was overwritten")
	}
}

func TestAuthLoginLogout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	setTestConfigRoot(t, t.TempDir())
	t.Setenv("NIUMA_CONFIG_DIR_NAME", ".niuma-auth-test")
	t.Setenv("CLAUDE_CODE_MODEL", "")
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"auth", "login", "--api-key", "secret"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Run(context.Background(), []string{"auth", "status"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"loggedIn": true`) {
		t.Fatalf("output = %s", out.String())
	}
	out.Reset()
	if err := Run(context.Background(), []string{"auth", "logout"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
}

func TestAuthLoginWritesGolangCCGlobalSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_MODEL", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")

	var out bytes.Buffer
	if err := Run(context.Background(), []string{"auth", "login", "--api-key", "secret"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	golangCCSettings := filepath.Join(home, ".niuma", "settings.json")
	settings := config.LoadSettingsFile(golangCCSettings)
	if settings.APIKey != "secret" {
		t.Fatal("global apiKey was not saved")
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("legacy settings should not be written, err=%v", err)
	}
}

func TestAuthLoginWithAuthToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_MODEL", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"auth", "login", "--auth-token", "oauth-secret"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "auth token") {
		t.Fatalf("output = %s", out.String())
	}
	out.Reset()
	if err := Run(context.Background(), []string{"auth", "status"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"authMethod": "oauth_token"`) || !strings.Contains(out.String(), `"hasAuthToken": true`) {
		t.Fatalf("output = %s", out.String())
	}
}

func TestDoctorCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_MODEL", "")
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"doctor"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"runtime": "go"`) || !strings.Contains(out.String(), `"sessionRoot"`) {
		t.Fatalf("output = %s", out.String())
	}
}

func TestStatusLoadsGoClaudeGlobalAuth(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_MODEL", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	mustWrite(t, filepath.Join(home, ".niuma", "settings.json"), `{
	  "apiKey": "global-key"
	}`)

	var out bytes.Buffer
	if err := Run(context.Background(), []string{"--cwd", project, "status"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	var payload struct {
		HasAuth         bool     `json:"hasAuth"`
		SettingsSources []string `json:"settingsSources"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("decode status: %v\n%s", err, out.String())
	}
	if !payload.HasAuth {
		t.Fatalf("hasAuth = false, output = %s", out.String())
	}
	wantSource := filepath.Join(home, ".niuma", "settings.json")
	if strings.Join(payload.SettingsSources, ",") != wantSource {
		t.Fatalf("settingsSources = %+v, want %s", payload.SettingsSources, wantSource)
	}
}

func TestParseGoalEvaluatorFlag(t *testing.T) {
	opts, _, err := parseArgs([]string{"--goal-evaluator", "model", "goal", "run", "goal_123", "--once"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.goalEvaluator != "model" {
		t.Fatalf("goal evaluator = %q", opts.goalEvaluator)
	}
	if _, _, err := parseArgs([]string{"--goal-evaluator", "bogus", "goal", "run", "goal_123"}); err == nil || !strings.Contains(err.Error(), "--goal-evaluator") {
		t.Fatalf("expected invalid goal evaluator error, got %v", err)
	}
}

func TestParseArgsProvider(t *testing.T) {
	opts, _, err := parseArgs([]string{"--provider", "selected", "--model", "explicit-model", "-p", "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.providerName != "selected" || opts.model != "explicit-model" || !opts.modelExplicit {
		t.Fatalf("opts = %+v", opts)
	}
	if _, _, err := parseArgs([]string{"--provider"}); err == nil || !strings.Contains(err.Error(), "requires a value") {
		t.Fatalf("missing provider error = %v", err)
	}
}

func TestApplySelectedProviderUsesProviderModelUnlessModelExplicit(t *testing.T) {
	cfg := config.Config{FallbackProviders: []config.ProviderConfig{{Name: "selected", Type: "custom", BaseURL: "https://selected.example.test/v1", APIKey: "key", Model: "provider-model"}}}
	opts := options{providerName: "selected"}
	if err := applySelectedProvider(&cfg, &opts); err != nil {
		t.Fatal(err)
	}
	if opts.model != "provider-model" || cfg.BaseURL != "https://selected.example.test/v1" {
		t.Fatalf("opts=%+v cfg=%+v", opts, cfg)
	}

	cfg = config.Config{FallbackProviders: []config.ProviderConfig{{Name: "selected", Model: "provider-model"}}}
	opts = options{providerName: "selected", model: "explicit-model", modelExplicit: true}
	if err := applySelectedProvider(&cfg, &opts); err != nil {
		t.Fatal(err)
	}
	if opts.model != "explicit-model" {
		t.Fatalf("model = %q", opts.model)
	}
}

func TestValidateSelectedProviderUsesRuntimeSettings(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	setTestConfigRoot(t, t.TempDir())

	opts := options{
		cwd:          project,
		providerName: "runtime-provider",
		settingsInputs: []string{`{
			"fallback": {
				"enabled": true,
				"providers": [{
					"name": "runtime-provider",
					"type": "custom",
					"protocol": "openai-responses",
					"baseURL": "https://responses.example.test/v1",
					"apiKey": "test-key",
					"model": "responses-model"
				}]
			}
		}`},
	}
	err := validateSelectedProvider(&opts)
	if err != nil {
		t.Fatalf("runtime settings provider selection failed: %v", err)
	}
	if opts.model != "responses-model" {
		t.Fatalf("selected provider model = %q", opts.model)
	}
}

func TestEvalAgentsCommandRunsDefaultSuite(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_MODEL", "")
	setTestConfigRoot(t, t.TempDir())
	outFile := filepath.Join(project, "eval-report.json")
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"--cwd", project, "eval", "agents", "--json", "--output", outFile}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"suite_id": "niuma-agent-eval-p1"`) || !strings.Contains(out.String(), `"failed": 0`) {
		t.Fatalf("output = %s", out.String())
	}
	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"status": "passed"`) || !strings.Contains(string(data), `"trace_observability"`) {
		t.Fatalf("report = %s", string(data))
	}
}

func TestEvalAgentsCommandRunsGoldenSuite(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_MODEL", "")
	setTestConfigRoot(t, t.TempDir())
	outFile := filepath.Join(project, "golden-report.json")
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"--cwd", project, "eval", "agents", "--suite", "golden", "--json", "--output", outFile}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"suite_id": "niuma-golden-workflows"`) || !strings.Contains(out.String(), `"failed": 0`) {
		t.Fatalf("output = %s", out.String())
	}
	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"git_conflict_recovery"`) || !strings.Contains(string(data), `"status": "passed"`) {
		t.Fatalf("report = %s", string(data))
	}
}

func TestEvalAgentsCommandSupportsLiveProfileSkip(t *testing.T) {
	t.Setenv("NIUMA_EVAL_LIVE_API_BASE", "")
	t.Setenv("NIUMA_EVAL_LIVE_API_BASE", "")
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"eval", "agents", "--profile", "live"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Agent eval skipped") || !strings.Contains(out.String(), "1 skipped") {
		t.Fatalf("output = %s", out.String())
	}
}

func TestTenantMigrateUpUsesDSNEnv(t *testing.T) {
	t.Setenv("NIUMA_MYSQL_DSN", "user:pass@tcp(127.0.0.1:3306)/claude")
	t.Setenv("MYSQL_DSN", "")
	fake := &fakeTenantMigrationRunner{}
	var captured mysqlstore.MigrationOptions
	old := newTenantMigrationRunner
	newTenantMigrationRunner = func(opts mysqlstore.MigrationOptions) (tenantMigrationRunner, error) {
		captured = opts
		return fake, nil
	}
	defer func() { newTenantMigrationRunner = old }()

	var out bytes.Buffer
	if err := Run(context.Background(), []string{"tenant", "migrate", "up", "--path", "migrations/mysql"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if fake.up != 1 || fake.closed != 1 {
		t.Fatalf("fake runner = %+v", fake)
	}
	if captured.DSN != "user:pass@tcp(127.0.0.1:3306)/claude" || captured.Path != "migrations/mysql" {
		t.Fatalf("captured = %+v", captured)
	}
	if !strings.Contains(out.String(), "Applied MySQL tenant migrations") {
		t.Fatalf("output = %s", out.String())
	}
}

func TestTenantMigrateDownStepsJSON(t *testing.T) {
	t.Setenv("NIUMA_MYSQL_DSN", "")
	t.Setenv("MYSQL_DSN", "")
	fake := &fakeTenantMigrationRunner{}
	old := newTenantMigrationRunner
	newTenantMigrationRunner = func(opts mysqlstore.MigrationOptions) (tenantMigrationRunner, error) {
		return fake, nil
	}
	defer func() { newTenantMigrationRunner = old }()

	var out bytes.Buffer
	if err := Run(context.Background(), []string{"tenant", "migrate", "down", "--dsn", "mysql://user:pass@tcp(localhost:3306)/claude", "--steps", "2", "--json"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if fake.downSteps != 2 || fake.downAll != 0 {
		t.Fatalf("fake runner = %+v", fake)
	}
	if !strings.Contains(out.String(), `"action": "down"`) || !strings.Contains(out.String(), `"steps": 2`) {
		t.Fatalf("output = %s", out.String())
	}
}

func TestTenantMigrateVersionJSON(t *testing.T) {
	t.Setenv("NIUMA_MYSQL_DSN", "")
	t.Setenv("MYSQL_DSN", "")
	fake := &fakeTenantMigrationRunner{version: mysqlstore.MigrationVersion{Version: 1, Applied: true}}
	old := newTenantMigrationRunner
	newTenantMigrationRunner = func(opts mysqlstore.MigrationOptions) (tenantMigrationRunner, error) {
		return fake, nil
	}
	defer func() { newTenantMigrationRunner = old }()

	var out bytes.Buffer
	if err := Run(context.Background(), []string{"tenant", "migrate", "version", "--dsn", "dsn", "--json"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if fake.versionCalls != 1 {
		t.Fatalf("fake runner = %+v", fake)
	}
	if !strings.Contains(out.String(), `"version": 1`) || !strings.Contains(out.String(), `"applied": true`) {
		t.Fatalf("output = %s", out.String())
	}
}

func TestTenantMigrateRequiresDSN(t *testing.T) {
	t.Setenv("NIUMA_MYSQL_DSN", "")
	t.Setenv("MYSQL_DSN", "")
	var out bytes.Buffer
	err := Run(context.Background(), []string{"tenant", "migrate", "up"}, &out, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "requires --dsn") {
		t.Fatalf("err = %v", err)
	}
}

func TestTenantAgentTasksListEventsCancel(t *testing.T) {
	t.Setenv("NIUMA_MYSQL_DSN", "dsn")
	t.Setenv("NIUMA_TENANT_KEY", "yutang")
	t.Setenv("NIUMA_USER_ID", "user-1")
	fake := &fakeTenantAgentTaskService{
		tasks: []mysqlstore.AgentTask{{
			ID:          41,
			AgentName:   "reviewer",
			Description: "review code",
			Status:      agenttasks.StatusRunning,
			Model:       "claude",
		}},
		events: []mysqlstore.AgentTaskEvent{{
			ID:        90,
			TaskID:    41,
			EventType: agenttasks.EventStarted,
		}},
	}
	old := newTenantAgentTaskService
	newTenantAgentTaskService = func(ctx context.Context, dsn string) (tenantAgentTaskService, func() error, error) {
		if dsn != "dsn" {
			t.Fatalf("dsn = %q", dsn)
		}
		return fake, func() error {
			fake.closed++
			return nil
		}, nil
	}
	defer func() { newTenantAgentTaskService = old }()

	var out bytes.Buffer
	if err := Run(context.Background(), []string{"tenant", "agent-tasks", "list", "--limit", "5"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if fake.lastLimit != 5 || !strings.Contains(out.String(), "reviewer") {
		t.Fatalf("list output=%s fake=%+v", out.String(), fake)
	}
	out.Reset()
	if err := Run(context.Background(), []string{"tenant", "agent-tasks", "events", "41", "--json"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if fake.lastTaskID != 41 || !strings.Contains(out.String(), `"event_type": "started"`) {
		t.Fatalf("events output=%s fake=%+v", out.String(), fake)
	}
	out.Reset()
	if err := Run(context.Background(), []string{"tenant", "agent-tasks", "cancel", "41", "--reason", "test"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if fake.cancelledTaskID != 41 || !strings.Contains(fake.cancelPayload, `"reason":"test"`) || !strings.Contains(fake.cancelPayload, `"capability_loop"`) || !strings.Contains(out.String(), "Cancelled agent task 41") {
		t.Fatalf("cancel output=%s fake=%+v", out.String(), fake)
	}
	if fake.closed != 3 {
		t.Fatalf("closed=%d", fake.closed)
	}
}

func TestTenantSkillPackageRenderCommand(t *testing.T) {
	t.Setenv("NIUMA_MYSQL_DSN", "dsn")
	t.Setenv("NIUMA_TENANT_KEY", "yutang")
	t.Setenv("NIUMA_USER_ID", "admin")
	fake := &fakeTenantSkillPackageService{}
	old := newTenantSkillPackageService
	newTenantSkillPackageService = func(ctx context.Context, dsn string) (tenantSkillPackageService, func() error, error) {
		if dsn != "dsn" {
			t.Fatalf("dsn = %q", dsn)
		}
		return fake, func() error {
			fake.closed++
			return nil
		}, nil
	}
	defer func() { newTenantSkillPackageService = old }()

	var out bytes.Buffer
	err := Run(context.Background(), []string{"tenant", "skill-package", "render", "/tmp/teach", "--skill-key", "teach-v2", "--name", "Teach V2", "--json"}, &out, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if fake.renderCalls != 1 || fake.lastReq.SourcePath != "/tmp/teach" || fake.lastReq.SkillKey != "teach-v2" || fake.closed != 1 {
		t.Fatalf("fake = %+v", fake)
	}
	if !strings.Contains(out.String(), `"skill_key": "teach-v2"`) || !strings.Contains(out.String(), `"package_sha256": "sha-render"`) {
		t.Fatalf("output = %s", out.String())
	}
}

func TestTenantSkillPackageListHistoryRollbackCommandsDoNotRequireSourcePath(t *testing.T) {
	t.Setenv("NIUMA_MYSQL_DSN", "dsn")
	t.Setenv("NIUMA_TENANT_KEY", "yutang")
	t.Setenv("NIUMA_USER_ID", "admin")
	fake := &fakeTenantSkillPackageService{
		skills: []mysqlstore.Skill{
			{SkillKey: "teach-v2", Version: 1, PackageSHA256: "sha-v1", PackageRef: "file:///pkg-v1.zip"},
			{SkillKey: "teach-v2", Version: 2, PackageSHA256: "sha-v2", PackageRef: "file:///pkg-v2.zip"},
		},
	}
	old := newTenantSkillPackageService
	newTenantSkillPackageService = func(ctx context.Context, dsn string) (tenantSkillPackageService, func() error, error) {
		return fake, func() error {
			fake.closed++
			return nil
		}, nil
	}
	defer func() { newTenantSkillPackageService = old }()

	var out bytes.Buffer
	if err := Run(context.Background(), []string{"tenant", "skill-package", "list"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "teach-v2\tv1\tsha-v1") {
		t.Fatalf("list output = %s", out.String())
	}
	out.Reset()
	if err := Run(context.Background(), []string{"tenant", "skill-package", "history", "--skill-key", "teach-v2"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "teach-v2\tv2\tsha-v2") {
		t.Fatalf("history output = %s", out.String())
	}
	out.Reset()
	if err := Run(context.Background(), []string{"tenant", "skill-package", "rollback", "--skill-key", "teach-v2", "--version", "1"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Rolled back teach-v2 v1 into v2") || fake.closed != 3 {
		t.Fatalf("rollback output=%s fake=%+v", out.String(), fake)
	}
}

func TestConfigSetGet(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_MODEL", "")
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"--cwd", project, "config", "set", "model", "test-model"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Run(context.Background(), []string{"--cwd", project, "config", "get", "model"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "test-model" {
		t.Fatalf("model = %q", out.String())
	}
	out.Reset()
	if err := Run(context.Background(), []string{"--cwd", project, "config", "set", "provider", "custom"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Run(context.Background(), []string{"--cwd", project, "config", "get", "provider"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "custom" {
		t.Fatalf("provider = %q", out.String())
	}
	out.Reset()
	if err := Run(context.Background(), []string{"--cwd", project, "config", "set", "env.TEST_KEY", "value"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Run(context.Background(), []string{"--cwd", project, "config", "unset", "env.TEST_KEY"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Run(context.Background(), []string{"--cwd", project, "config", "list"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "TEST_KEY") {
		t.Fatalf("output = %s", out.String())
	}
	out.Reset()
	if err := Run(context.Background(), []string{"--cwd", project, "config", "set", "maxToolResultBytes", "1234"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), []string{"--cwd", project, "config", "set", "additionalDirectories", "/tmp/extra"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Run(context.Background(), []string{"--cwd", project, "config", "get", "maxToolResultBytes"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "1234" {
		t.Fatalf("output = %s", out.String())
	}
	out.Reset()
	if err := Run(context.Background(), []string{"--cwd", project, "config", "get", "additionalDirectories"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "/tmp/extra") {
		t.Fatalf("output = %s", out.String())
	}
	out.Reset()
	if err := Run(context.Background(), []string{"--cwd", project, "config", "set", "tui.resumeHistoryLimit", "2"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Run(context.Background(), []string{"--cwd", project, "config", "get", "tui.resumeHistoryLimit"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "2" {
		t.Fatalf("output = %s", out.String())
	}
	for _, key := range []string{"tui.showThinking", "webAgentUI.showThinking"} {
		out.Reset()
		if err := Run(context.Background(), []string{"--cwd", project, "config", "set", key, "false"}, &out, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		out.Reset()
		if err := Run(context.Background(), []string{"--cwd", project, "config", "get", key}, &out, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(out.String()) != "false" {
			t.Fatalf("%s = %q", key, out.String())
		}
	}
	out.Reset()
	if err := Run(context.Background(), []string{"--cwd", project, "config", "set", "tui.thinkingMode", "summary"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Run(context.Background(), []string{"--cwd", project, "config", "get", "tui.thinkingMode"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "summary" {
		t.Fatalf("tui.thinkingMode = %q", out.String())
	}
	if err := Run(context.Background(), []string{"--cwd", project, "config", "set", "tui.thinkingMode", "invalid"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("invalid tui.thinkingMode was accepted")
	}
	out.Reset()
	if err := Run(context.Background(), []string{"--cwd", project, "config", "set", configKeyRecapAwayDelay, "120"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Run(context.Background(), []string{"--cwd", project, "config", "get", configKeyRecapAwayDelay}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "120" {
		t.Fatalf("%s = %q", configKeyRecapAwayDelay, out.String())
	}
	settings := config.LoadGlobalSettings()
	if settings.Recap == nil || settings.Recap.AwayDelaySeconds == nil || *settings.Recap.AwayDelaySeconds != 120 {
		t.Fatalf("recap settings = %+v", settings.Recap)
	}
	out.Reset()
	if err := Run(context.Background(), []string{"--cwd", project, "config", "unset", configKeyRecapAwayDelay}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Run(context.Background(), []string{"--cwd", project, "config", "get", configKeyRecapAwayDelay}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Fatalf("unset %s = %q", configKeyRecapAwayDelay, out.String())
	}
}

func TestConfigSetRejectsInvalidRecapAwayDelay(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, value := range []string{"0", "-1", "later"} {
		err := Run(context.Background(), []string{"config", "set", configKeyRecapAwayDelay, value}, &bytes.Buffer{}, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "must be a positive integer") {
			t.Fatalf("value %q error = %v", value, err)
		}
	}
}

type fakeTenantMigrationRunner struct {
	up           int
	downSteps    int
	downAll      int
	versionCalls int
	closed       int
	version      mysqlstore.MigrationVersion
}

func (f *fakeTenantMigrationRunner) Up() error {
	f.up++
	return nil
}

func (f *fakeTenantMigrationRunner) Down(steps int) error {
	f.downSteps = steps
	return nil
}

func (f *fakeTenantMigrationRunner) DownAll() error {
	f.downAll++
	return nil
}

func (f *fakeTenantMigrationRunner) Version() (mysqlstore.MigrationVersion, error) {
	f.versionCalls++
	return f.version, nil
}

func (f *fakeTenantMigrationRunner) Close() error {
	f.closed++
	return nil
}

func TestConfigLocalAndProjectScopes(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_MODEL", "")
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"--cwd", project, "config", "--project", "set", "model", "project-model"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Run(context.Background(), []string{"--cwd", project, "config", "--local", "set", "model", "local-model"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	projectData, err := os.ReadFile(filepath.Join(project, ".niuma", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	localData, err := os.ReadFile(filepath.Join(project, ".niuma", "settings.local.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(projectData), "project-model") || !strings.Contains(string(localData), "local-model") {
		t.Fatalf("project=%s local=%s", projectData, localData)
	}
}

func TestMCPList(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, ".claude"))
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_MODEL", "")
	mustWrite(t, filepath.Join(project, ".claude", "settings.json"), `{"mcpServers":{"test":{"command":"echo"}}}`)
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"--cwd", project, "mcp", "list"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "test") || !strings.Contains(out.String(), "echo") {
		t.Fatalf("output = %s", out.String())
	}
}

func TestMCPAddRemove(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_MODEL", "")
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"mcp", "add", "demo", "echo", "hello"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Run(context.Background(), []string{"mcp", "list"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "demo") || !strings.Contains(out.String(), "echo") {
		t.Fatalf("output = %s", out.String())
	}
	out.Reset()
	if err := Run(context.Background(), []string{"mcp", "show", "demo"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"command": "echo"`) || !strings.Contains(out.String(), `"hello"`) {
		t.Fatalf("output = %s", out.String())
	}
	out.Reset()
	if err := Run(context.Background(), []string{"mcp", "add", "web", "--type", "http", "--url", "https://example.test/mcp", "--header", "x-test=yes"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Run(context.Background(), []string{"mcp", "list", "--json"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"web"`) || !strings.Contains(out.String(), `"url": "https://example.test/mcp"`) || !strings.Contains(out.String(), `"x-test": "yes"`) {
		t.Fatalf("output = %s", out.String())
	}
	out.Reset()
	if err := Run(context.Background(), []string{"mcp", "remove", "demo"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Run(context.Background(), []string{"mcp", "list"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "demo") {
		t.Fatalf("output = %s", out.String())
	}
}

func TestMCPListIncludesPluginServer(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_MODEL", "")
	mustWrite(t, filepath.Join(project, ".claude", "plugins", "demo", ".codex-plugin", "plugin.json"), `{
	  "name": "demo",
	  "mcpServers": {"plugin-server": {"type": "http", "url": "https://example.test/mcp"}}
	}`)
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"--cwd", project, "mcp", "list", "--json"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "plugin-server") || !strings.Contains(out.String(), "https://example.test/mcp") {
		t.Fatalf("output = %s", out.String())
	}
}

func TestPluginInstallRemoveCommand(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	source := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_MODEL", "")
	mustWrite(t, filepath.Join(source, ".codex-plugin", "plugin.json"), `{"name":"demo"}`)
	mustWrite(t, filepath.Join(source, "skills", "demo", "SKILL.md"), "# Demo\n")

	var out bytes.Buffer
	if err := Run(context.Background(), []string{"--cwd", project, "plugin", "install", source, "--project"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Installed plugin demo") {
		t.Fatalf("output = %s", out.String())
	}
	out.Reset()
	if err := Run(context.Background(), []string{"--cwd", project, "plugin", "list"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "demo") {
		t.Fatalf("output = %s", out.String())
	}
	out.Reset()
	if err := Run(context.Background(), []string{"--cwd", project, "plugin", "remove", "demo", "--project"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Removed plugin demo") {
		t.Fatalf("output = %s", out.String())
	}
}

func TestMCPPromptCommands(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, ".claude"))
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_MODEL", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		var result any = map[string]any{}
		switch req.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2024-11-05"}
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
			return
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{"name": "echo", "description": "Echo"}}}
		case "tools/call":
			var params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			if err := json.Unmarshal(req.Params, &params); err != nil {
				t.Fatal(err)
			}
			result = map[string]any{"content": []map[string]any{{"type": "text", "text": params.Name + " " + fmt.Sprint(params.Arguments["text"])}}}
		case "prompts/list":
			result = map[string]any{"prompts": []map[string]any{{"name": "review", "description": "Review code"}}}
		case "prompts/get":
			result = map[string]any{
				"description": "Review code",
				"messages": []map[string]any{{
					"role":    "user",
					"content": map[string]any{"type": "text", "text": "Review this code"},
				}},
			}
		case "resources/list":
			result = map[string]any{"resources": []map[string]any{{"uri": "file://demo", "name": "Demo"}}}
		case "resources/read":
			result = map[string]any{"contents": []map[string]any{{"uri": "file://demo", "text": "resource text"}}}
		default:
			t.Fatalf("unexpected method %s", req.Method)
		}
		data, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": json.RawMessage(data)})
	}))
	defer server.Close()
	mustWrite(t, filepath.Join(project, ".claude", "settings.json"), `{"mcpServers":{"srv":{"type":"http","url":"`+server.URL+`"}}}`)

	var out bytes.Buffer
	if err := Run(context.Background(), []string{"--cwd", project, "mcp", "prompts", "--json"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"server": "srv"`) || !strings.Contains(out.String(), `"name": "review"`) {
		t.Fatalf("output = %s", out.String())
	}

	out.Reset()
	if err := Run(context.Background(), []string{"--cwd", project, "mcp", "prompt", "srv", "review", "topic=code"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Review code") || !strings.Contains(out.String(), "Review this code") {
		t.Fatalf("output = %s", out.String())
	}

	out.Reset()
	if err := Run(context.Background(), []string{"--cwd", project, "mcp", "resources"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "file://demo") || !strings.Contains(out.String(), "Demo") {
		t.Fatalf("output = %s", out.String())
	}

	out.Reset()
	if err := Run(context.Background(), []string{"--cwd", project, "mcp", "resource", "srv", "file://demo"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "resource text") {
		t.Fatalf("output = %s", out.String())
	}

	out.Reset()
	if err := Run(context.Background(), []string{"--cwd", project, "mcp", "tools"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "echo") || !strings.Contains(out.String(), "Echo") {
		t.Fatalf("output = %s", out.String())
	}

	out.Reset()
	if err := Run(context.Background(), []string{"--cwd", project, "mcp", "call", "srv", "echo", "text=hi"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "echo hi") {
		t.Fatalf("output = %s", out.String())
	}

	out.Reset()
	if err := Run(context.Background(), []string{"--cwd", project, "mcp", "call", "srv", "echo", "--input", `{"text":"json-hi"}`, "--json"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"tool": "echo"`) || !strings.Contains(out.String(), "json-hi") {
		t.Fatalf("output = %s", out.String())
	}
}

func TestMCPServeListsClaudeCodeTools(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1.0.0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	stdio := mcpserver.NewStdioServer(buildMCPServer(options{cwd: t.TempDir(), model: "test", maxTurns: 1}))
	if err := stdio.Listen(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	if !strings.Contains(body, `"name":"claude_code_query"`) || !strings.Contains(body, `"name":"claude_code_status"`) {
		t.Fatalf("mcp output = %s", body)
	}
}

func TestSubcommandKeepsOwnFlags(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, ".claude"))
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_MODEL", "")
	mustWrite(t, filepath.Join(project, ".claude", "settings.json"), `{"mcpServers":{"test":{"command":"echo"}}}`)
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"--cwd", project, "mcp", "list", "--json"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"test"`) {
		t.Fatalf("output = %s", out.String())
	}
}

func TestSkillsList(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_MODEL", "")
	skillDir := filepath.Join(project, ".claude", "skills", "demo")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Demo\n\nInstructions"), 0644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"--cwd", project, "skills", "list"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "demo") || !strings.Contains(out.String(), "Demo") {
		t.Fatalf("output = %s", out.String())
	}
	out.Reset()
	if err := Run(context.Background(), []string{"--cwd", project, "skills", "show", "demo"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "# Demo") || !strings.Contains(out.String(), "Instructions") {
		t.Fatalf("output = %s", out.String())
	}
}

func TestSkillsLintReportsCrossFenceShellStateWithoutFailingCommand(t *testing.T) {
	project := t.TempDir()
	skillPath := filepath.Join(project, ".claude", "skills", "demo", "SKILL.md")
	mustWrite(t, skillPath, "```bash\nROOT=\"$(pwd)\"\n```\n\n```bash\nprintf '%s' \"$ROOT\"\n```\n")
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"--cwd", project, "skills", "lint", skillPath}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "shell_cross_fence_variable") || !strings.Contains(text, "report only") {
		t.Fatalf("unexpected lint output: %s", text)
	}
}

func TestSkillsMarketplaceStatus(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_MODEL", "")
	target := filepath.Join(home, ".niuma", "skills-marketplace")
	source := filepath.Join(t.TempDir(), "index.yaml")
	body := "---\nname: current\nversion: 1.0.0\n---\n# Current\n"
	oldBody := "---\nname: outdated\nversion: 1.0.0\n---\n# Old\n"
	newBody := "---\nname: outdated\nversion: 2.0.0\n---\n# New\n"
	mustWrite(t, filepath.Join(target, "current", "SKILL.md"), body)
	mustWrite(t, filepath.Join(target, "outdated", "SKILL.md"), oldBody)
	mustWrite(t, source, fmt.Sprintf(`skills:
  - name: current
    version: 1.0.0
    content_sha256: %s
  - name: outdated
    version: 2.0.0
    content_sha256: %s
  - name: missing
    version: 1.0.0
`, fmt.Sprintf("%x", sha256.Sum256([]byte(body))), fmt.Sprintf("%x", sha256.Sum256([]byte(newBody)))))
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"--cwd", project, "skills", "marketplace-status", "--source", source}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "Marketplace status: 3 skill(s), 1 current, 1 outdated, 1 missing") ||
		!strings.Contains(text, "current\tcurrent") ||
		!strings.Contains(text, "missing\tmissing") ||
		!strings.Contains(text, "install: skills install --source "+source+" missing") ||
		!strings.Contains(text, "outdated\toutdated") ||
		!strings.Contains(text, "update: skills install --source "+source+" outdated") {
		t.Fatalf("output = %s", out.String())
	}

	out.Reset()
	if err := Run(context.Background(), []string{"--cwd", project, "skills", "marketplace-status", "--source", source, "--outdated-only"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "missing\tmissing") || !strings.Contains(out.String(), "outdated\toutdated") {
		t.Fatalf("outdated-only output = %s", out.String())
	}
}

func TestSkillsMarketplaceSearchShowsInstallHint(t *testing.T) {
	source := filepath.Join(t.TempDir(), "index.yaml")
	mustWrite(t, source, `skills:
  - name: go-review
    description: Review Go code
    version: 1.2.3
    content_sha256: abc
    content: "# Go Review"
`)
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"skills", "marketplace-search", "--source", source, "--query", "review"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "Marketplace search: 1 skill(s)") ||
		!strings.Contains(text, "go-review\tv1.2.3\tinline\tverified:hash\tReview Go code") ||
		!strings.Contains(text, "install: skills install --source "+source+" go-review") {
		t.Fatalf("output = %s", text)
	}
}

func TestPluginList(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_MODEL", "")
	manifest := filepath.Join(project, ".claude", "plugins", "demo", ".codex-plugin", "plugin.json")
	mustWrite(t, manifest, `{"name":"demo","version":"0.1.0","description":"Demo plugin"}`)
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"--cwd", project, "plugin", "list"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "demo") || !strings.Contains(out.String(), "Demo plugin") {
		t.Fatalf("output = %s", out.String())
	}
	out.Reset()
	if err := Run(context.Background(), []string{"--cwd", project, "plugin", "show", "demo"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"name": "demo"`) || !strings.Contains(out.String(), `"path"`) {
		t.Fatalf("output = %s", out.String())
	}
}

func TestSessionShow(t *testing.T) {
	root := t.TempDir()
	setTestConfigRoot(t, root)
	t.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", filepath.Join(root, "projects"))
	recorder, err := session.DefaultStore().NewRecorder(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{Type: "message", Role: "user", Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"session", "show", recorder.SessionID}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "hello") {
		t.Fatalf("output = %s", out.String())
	}
	out.Reset()
	if err := Run(context.Background(), []string{"session", "locate", recorder.SessionID, "--json"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var located session.Summary
	if err := json.Unmarshal(out.Bytes(), &located); err != nil {
		t.Fatalf("locate output is not summary JSON: %v\n%s", err, out.String())
	}
	if located.SessionID != recorder.SessionID || located.Path != recorder.Path {
		t.Fatalf("located = %+v", located)
	}
	if strings.Contains(out.String(), `"content": "hello"`) || strings.Contains(out.String(), `"title"`) || strings.Contains(out.String(), "hello") {
		t.Fatalf("locate must not print transcript content or derived titles: %s", out.String())
	}
	out.Reset()
	if err := Run(context.Background(), []string{"session", "locate", recorder.SessionID}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), recorder.SessionID+"\t"+recorder.Path+"\n"; got != want {
		t.Fatalf("locate text = %q, want %q", got, want)
	}
	t.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", t.TempDir())
	if err := Run(context.Background(), []string{"session", "locate"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "no sessions found for project") {
		t.Fatalf("locate without id should fall back to latest-for-project and report empty project, got %v", err)
	}
	t.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", filepath.Join(root, "projects"))
	unknownID := "99999999-9999-4999-8999-999999999999"
	if err := Run(context.Background(), []string{"session", "locate", unknownID}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil || err.Error() != "session not found: "+unknownID {
		t.Fatalf("unknown locate id error = %v", err)
	}
	out.Reset()
	if err := Run(context.Background(), []string{"session", "list"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), recorder.SessionID) || !strings.Contains(out.String(), "hello") {
		t.Fatalf("output = %s", out.String())
	}
	out.Reset()
	if err := Run(context.Background(), []string{"session", "rename", recorder.SessionID, "Renamed", "Session"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Run(context.Background(), []string{"session", "checkpoint", recorder.SessionID, "before", "change"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "before change") {
		t.Fatalf("output = %s", out.String())
	}
	out.Reset()
	if err := Run(context.Background(), []string{"session", "search", "renamed"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), recorder.SessionID) || !strings.Contains(out.String(), "Renamed Session") {
		t.Fatalf("output = %s", out.String())
	}
	out.Reset()
	if err := Run(context.Background(), []string{"session", "rewind", recorder.SessionID, "before", "change"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"checkpoint": "before change"`) {
		t.Fatalf("output = %s", out.String())
	}
	out.Reset()
	if err := Run(context.Background(), []string{"session", "fork", recorder.SessionID, "before", "change", "--name", "Forked"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"source_session_id": "`+recorder.SessionID+`"`) || !strings.Contains(out.String(), `"checkpoint": "before change"`) {
		t.Fatalf("output = %s", out.String())
	}
	out.Reset()
	if err := Run(context.Background(), []string{"session", "delete", recorder.SessionID}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Run(context.Background(), []string{"session", "list"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), recorder.SessionID) {
		t.Fatalf("output = %s", out.String())
	}
}

func TestSessionInspectJSON(t *testing.T) {
	root := t.TempDir()
	setTestConfigRoot(t, root)
	t.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", filepath.Join(root, "projects"))
	recorder, err := session.DefaultStore().NewRecorder(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{Type: "usage", Model: "test-model"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"session", "inspect", recorder.SessionID, "--json"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var report sessiondiag.Report
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.SessionID != recorder.SessionID || report.SessionFile != recorder.Path || len(report.Models) != 1 || report.Models[0] != "test-model" {
		t.Fatalf("report = %+v", report)
	}
	if err := Run(context.Background(), []string{"session", "inspect"}, io.Discard, &bytes.Buffer{}); err == nil {
		t.Fatal("expected missing session id error")
	}
}

func TestCompletionIncludesTopLevelCommands(t *testing.T) {
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"completion", "bash"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"tenant", "tools", "hooks", "agents", "plugins"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("completion missing %s: %s", want, out.String())
		}
	}
}

func TestUsageCommand(t *testing.T) {
	root := t.TempDir()
	setTestConfigRoot(t, root)
	t.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", filepath.Join(root, "projects"))
	recorder, err := session.DefaultStore().NewRecorder(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{Type: "usage", Model: "claude-sonnet-4-5-20250929", InputTokens: 7, OutputTokens: 3}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"usage"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"total_tokens": 10`) || !strings.Contains(out.String(), `"cost_usd"`) {
		t.Fatalf("output = %s", out.String())
	}
}

func TestExportCommand(t *testing.T) {
	root := t.TempDir()
	setTestConfigRoot(t, root)
	t.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", filepath.Join(root, "projects"))
	recorder, err := session.DefaultStore().NewRecorder(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{Type: "message", Role: "user", Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(t.TempDir(), "session.md")
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"export", recorder.SessionID, outPath}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "hello") {
		t.Fatalf("export = %s", data)
	}
	jsonPath := filepath.Join(t.TempDir(), "session.json")
	out.Reset()
	if err := Run(context.Background(), []string{"export", recorder.SessionID, jsonPath, "--format", "json"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"content": "hello"`) {
		t.Fatalf("export json = %s", data)
	}
}

func TestInitAndStatusCommands(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_MODEL", "")
	setTestConfigRoot(t, filepath.Join(home, ".claude"))
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"--cwd", project, "init", "--settings"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(project, "niuma.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(project, ".niuma", "settings.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(project, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Fatalf("legacy CLAUDE.md should not be created, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(project, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("legacy .claude/settings.json should not be created, err=%v", err)
	}
	out.Reset()
	if err := Run(context.Background(), []string{"--cwd", project, "status"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"cwd": "`+project+`"`) {
		t.Fatalf("output = %s", out.String())
	}
}

func TestInitCommandUsesConfiguredIdentity(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_MODEL", "")
	mustWrite(t, filepath.Join(home, ".niuma", "settings.json"), `{
	  "identity": {
	    "productName": "agentx",
	    "configDirName": ".agentx",
	    "guidanceFilename": "agentx.md"
	  }
	}`)

	var out bytes.Buffer
	if err := Run(context.Background(), []string{"--cwd", project, "init", "--settings"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	guidance, err := os.ReadFile(filepath.Join(project, "agentx.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(guidance), "guidance to agentx") {
		t.Fatalf("guidance file = %s", guidance)
	}
	if _, err := os.Stat(filepath.Join(project, ".agentx", "settings.json")); err != nil {
		t.Fatal(err)
	}
	for _, filename := range []string{"niuma.md", "niuma.md", "niuma-legacy.md"} {
		if _, err := os.Stat(filepath.Join(project, filename)); !os.IsNotExist(err) {
			t.Fatalf("default guidance file should not be created, filename=%s err=%v", filename, err)
		}
	}
	if _, err := os.Stat(filepath.Join(project, ".niuma", "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("default project settings should not be created, err=%v", err)
	}
}

func TestStatusCommandReportsPromptContextMemoryForCWD(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(t.TempDir(), "huyu")
	configDir := filepath.Join(home, ".claude")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_MODEL", "")
	setTestConfigRoot(t, configDir)
	mustWrite(t, filepath.Join(project, "CLAUDE.md"), "project rule")
	mustWrite(t, filepath.Join(project, "WORKFLOW.md"), strings.Join([]string{
		"workflow rule",
		"<skills_system>",
		"large skill catalog",
		"</skills_system>",
	}, "\n"))
	nativeSlug := strings.NewReplacer("/", "-", ":", "", " ", "-").Replace(filepath.ToSlash(filepath.Clean(project)))
	memoryPath := filepath.Join(configDir, "projects", nativeSlug, "memory", "MEMORY.md")
	mustWrite(t, memoryPath, "project memory index")

	var out bytes.Buffer
	if err := Run(context.Background(), []string{"--cwd", project, "status"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		`"promptContext"`,
		`"mode": "code"`,
		`"codeMemoryDocuments": 3`,
		`"codeMemoryBytes"`,
		`"codeMemoryPromptBytes"`,
		`"Project": 1`,
		`"Workflow": 1`,
		`"codeMemoryByTypeBytes"`,
		`"codeMemoryDocumentSummary"`,
		`"promptBytes"`,
		`"workflowRules": 1`,
		`"workflowBytes"`,
		`"workflowBudgetBytes"`,
		`"workflowBudgetedDocuments"`,
		`"workflowSkillsSystemLeaked": false`,
		`"ClaudeCodeProjectMemory": 1`,
		memoryPath,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("status missing %q:\n%s", want, text)
		}
	}
}

func TestModelCommand(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, "config"))
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_MODEL", "")
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"--cwd", project, "model", "set", "test-model"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Run(context.Background(), []string{"--cwd", project, "model", "get"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "test-model" {
		t.Fatalf("output = %s", out.String())
	}
	out.Reset()
	if err := Run(context.Background(), []string{"--cwd", project, "model", "list"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "test-model" {
		t.Fatalf("output = %s", out.String())
	}

	project = t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, "config"))
	mustWrite(t, filepath.Join(project, "config", "settings.json"), `{"model":"primary-model","fallback":{"enabled":true,"providers":[{"name":"provider","type":"custom","model":"provider-model"}]}}`)
	out.Reset()
	if err := Run(context.Background(), []string{"--cwd", project, "model", "list"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(out.String()), "primary-model\nprovider-model"; got != want {
		t.Fatalf("configured model list = %q, want %q", got, want)
	}
}

func TestPermissionsCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_MODEL", "")
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"permissions", "mode", "ask"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), []string{"permissions", "deny", "Bash"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Run(context.Background(), []string{"permissions", "list"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"defaultMode": "ask"`) || !strings.Contains(out.String(), `"Bash"`) {
		t.Fatalf("output = %s", out.String())
	}
	out.Reset()
	if err := Run(context.Background(), []string{"permissions", "remove", "Bash"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Run(context.Background(), []string{"permissions", "list"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), `"Bash"`) {
		t.Fatalf("output = %s", out.String())
	}
	if err := Run(context.Background(), []string{"permissions", "allow", "Read"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Run(context.Background(), []string{"permissions", "clear", "allow"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Run(context.Background(), []string{"permissions", "list"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), `"Read"`) {
		t.Fatalf("output = %s", out.String())
	}
}

func TestCompletionCommand(t *testing.T) {
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"completion", "fish"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "complete -c niuma") {
		t.Fatalf("output = %s", out.String())
	}
}

func TestBuildReviewPrompt(t *testing.T) {
	prompt := buildReviewPrompt("a.go | 2 +-", "diff --git a/a.go b/a.go\n+return nil", reviewFocus([]string{"--staged", "focus", "tests"}))
	if !strings.Contains(prompt, "Diff stat") || !strings.Contains(prompt, "```diff") || !strings.Contains(prompt, "Additional review focus: focus tests") {
		t.Fatalf("prompt = %s", prompt)
	}
	if strings.Contains(reviewFocus([]string{"--cached", "--unknown", "security"}), "--") {
		t.Fatal("reviewFocus kept flag text")
	}
}

func TestToolsCommand(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"tools"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"name": "Read"`) || !strings.Contains(out.String(), `"name": "Bash"`) || !strings.Contains(out.String(), `"name": "NotebookRead"`) || !strings.Contains(out.String(), `"name": "LS"`) || !strings.Contains(out.String(), `"name": "MultiEdit"`) || !strings.Contains(out.String(), `"name": "TodoRead"`) {
		t.Fatalf("output = %s", out.String())
	}
}

func TestInteractiveSlashCommands(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var out bytes.Buffer
	handled, exit, err := handleInteractiveSlash(context.Background(), options{cwd: t.TempDir(), model: "test-model", maxTurns: 1}, "/help", &out, &bytes.Buffer{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	help := out.String()
	for _, want := range []string{"Slash commands / 斜杠命令:", "/status", "查看当前项目和运行状态", "/init", "初始化或优化 niuma.md", "/mcp", "管理 MCP 服务", goalcmd.SlashUsage, goalcmd.HelpDescriptionZH} {
		if !strings.Contains(help, want) {
			t.Fatalf("help output missing %q:\n%s", want, help)
		}
	}
	if !handled || exit {
		t.Fatalf("handled=%v exit=%v output=%s", handled, exit, out.String())
	}
	out.Reset()
	handled, exit, err = handleInteractiveSlash(context.Background(), options{cwd: t.TempDir(), model: "test-model", maxTurns: 1}, "/clear", &out, &bytes.Buffer{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !handled || exit || !strings.Contains(out.String(), "\033[H") {
		t.Fatalf("handled=%v exit=%v output=%q", handled, exit, out.String())
	}
	out.Reset()
	handled, exit, err = handleInteractiveSlash(context.Background(), options{cwd: t.TempDir(), model: "test-model", maxTurns: 1}, "/exit", &out, &bytes.Buffer{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !exit {
		t.Fatalf("handled=%v exit=%v", handled, exit)
	}
}

func TestInteractiveRewindSlashListsCandidates(t *testing.T) {
	root := t.TempDir()
	setTestConfigRoot(t, root)
	project := t.TempDir()
	recorder, err := session.DefaultStore().NewRecorder(project)
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	if _, err := recorder.Checkpoint("auto-msg-1", "msg-1"); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{ID: "msg-1", Type: "message", Role: "user", Content: "first prompt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := recorder.Checkpoint("auto-msg-2", "msg-2"); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{ID: "msg-2", Type: "message", Role: "user", Content: "second prompt"}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	handled, exit, err := handleInteractiveSlash(context.Background(), options{cwd: project, model: "test-model", maxTurns: 1}, "/rewind", &out, &bytes.Buffer{}, recorder)
	if err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !handled || exit || !strings.Contains(text, "Rewind to a previous user message") || !strings.Contains(text, "second prompt") || !strings.Contains(text, "id: msg-2") {
		t.Fatalf("handled=%v exit=%v output=%s", handled, exit, text)
	}
}

func TestInteractiveRewindSlashRestoresConversationOnly(t *testing.T) {
	root := t.TempDir()
	setTestConfigRoot(t, root)
	project := t.TempDir()
	recorder, err := session.DefaultStore().NewRecorder(project)
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	if _, err := recorder.Checkpoint("auto-msg-1", "msg-1"); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{ID: "msg-1", Type: "message", Role: "user", Content: "first prompt"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{ID: "assistant-1", Type: "message", Role: "assistant", Content: "after first"}); err != nil {
		t.Fatal(err)
	}
	if _, err := recorder.Checkpoint("auto-msg-2", "msg-2"); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{ID: "msg-2", Type: "message", Role: "user", Content: "second prompt"}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	handled, exit, err := handleInteractiveSlash(context.Background(), options{cwd: project, model: "test-model", maxTurns: 1}, "/rewind msg-1 --conversation-only", &out, &bytes.Buffer{}, recorder)
	if err != nil {
		t.Fatal(err)
	}
	if !handled || exit || !strings.Contains(out.String(), "Rewound conversation to before message msg-1") {
		t.Fatalf("handled=%v exit=%v output=%s", handled, exit, out.String())
	}
	entries, err := session.Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Type != "checkpoint" || entries[1].Type != "rewind" || entries[1].Name != "msg-1" {
		t.Fatalf("entries after rewind = %+v", entries)
	}
}

func TestInteractiveRewindSlashUsesResumedSession(t *testing.T) {
	root := t.TempDir()
	setTestConfigRoot(t, root)
	project := t.TempDir()
	resumed, err := session.DefaultStore().NewRecorder(project)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	if _, err := resumed.Checkpoint("auto-resumed-msg", "resumed-msg"); err != nil {
		t.Fatal(err)
	}
	if err := resumed.Append(session.Entry{ID: "resumed-msg", Type: "message", Role: "user", Content: "resumed prompt"}); err != nil {
		t.Fatal(err)
	}
	current, err := session.DefaultStore().NewRecorder(project)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()

	var out bytes.Buffer
	handled, exit, err := handleInteractiveSlash(context.Background(), options{cwd: project, model: "test-model", maxTurns: 1, resume: resumed.SessionID}, "/rewind", &out, &bytes.Buffer{}, current)
	if err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !handled || exit || !strings.Contains(text, "resumed prompt") || strings.Contains(text, current.SessionID) {
		t.Fatalf("handled=%v exit=%v output=%s", handled, exit, text)
	}
}

func TestTUIRewindCandidateProviderUsesActiveSession(t *testing.T) {
	root := t.TempDir()
	project := t.TempDir()
	setTestConfigRoot(t, root)
	store := session.DefaultStore()
	recorder, err := store.NewRecorderWithID(project, "11111111-1111-4111-8111-111111111111")
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	if err := recorder.Append(session.Entry{Type: "message", Role: "user", ID: "msg-1", Content: "first prompt"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{Type: "message", Role: "user", ID: "msg-2", Content: "second prompt"}); err != nil {
		t.Fatal(err)
	}
	opts := options{cwd: project}
	provider := tuiRewindCandidateProvider(&opts, recorder)
	candidates, err := provider(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || candidates[0].ID != "msg-2" || candidates[1].Preview != "first prompt" {
		t.Fatalf("candidates = %+v", candidates)
	}
}

func TestInteractiveGoalSlashCommands(t *testing.T) {
	root := t.TempDir()
	setTestConfigRoot(t, root)
	project := t.TempDir()
	var out bytes.Buffer
	handled, exit, err := handleInteractiveSlash(context.Background(), options{cwd: project, model: "test-model", maxTurns: 1}, "/goal start ship slash goal", &out, &bytes.Buffer{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !handled || exit || !strings.Contains(out.String(), "Started goal goal_") {
		t.Fatalf("handled=%v exit=%v output=%s", handled, exit, out.String())
	}
	out.Reset()
	handled, exit, err = handleInteractiveSlash(context.Background(), options{cwd: project, model: "test-model", maxTurns: 1}, "/goal status", &out, &bytes.Buffer{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !handled || exit || !strings.Contains(out.String(), "Status: active") || !strings.Contains(out.String(), "ship slash goal") {
		t.Fatalf("handled=%v exit=%v output=%s", handled, exit, out.String())
	}
}

func TestBareGoalWithoutActiveGoalShowsBeginnerHelp(t *testing.T) {
	setTestConfigRoot(t, t.TempDir())
	var out bytes.Buffer
	handled, exit, err := handleInteractiveSlash(context.Background(), options{}, "/goal", &out, &bytes.Buffer{}, nil)
	if err != nil || !handled || exit {
		t.Fatalf("handled=%v exit=%v err=%v", handled, exit, err)
	}
	if !strings.Contains(out.String(), "Quick start:") || !strings.Contains(out.String(), "/goal help") {
		t.Fatalf("unexpected help:\n%s", out.String())
	}
}

func TestBareGoalWithActiveGoalStillShowsStatus(t *testing.T) {
	setTestConfigRoot(t, t.TempDir())
	if _, err := goalpkg.DefaultStore().Create(context.Background(), goalpkg.CreateInput{Objective: "keep status shortcut"}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	handled, exit, err := handleInteractiveSlash(context.Background(), options{}, "/goal", &out, &bytes.Buffer{}, nil)
	if err != nil || !handled || exit {
		t.Fatalf("handled=%v exit=%v err=%v", handled, exit, err)
	}
	if !strings.Contains(out.String(), "Objective: keep status shortcut") {
		t.Fatalf("unexpected status:\n%s", out.String())
	}
}

func TestInteractiveGoalHelpVariantsShareBeginnerHelp(t *testing.T) {
	setTestConfigRoot(t, t.TempDir())
	var baseline string
	for _, input := range []string{"/goal help", "/goal -h", "/goal --help"} {
		var out bytes.Buffer
		handled, exit, err := handleInteractiveSlash(context.Background(), options{}, input, &out, &bytes.Buffer{}, nil)
		if err != nil || !handled || exit {
			t.Fatalf("input=%q handled=%v exit=%v err=%v", input, handled, exit, err)
		}
		if baseline == "" {
			baseline = out.String()
		} else if out.String() != baseline {
			t.Fatalf("input %q returned different help:\n%s\n--- baseline ---\n%s", input, out.String(), baseline)
		}
	}
}

func TestGoalInspectAliasesStatus(t *testing.T) {
	setTestConfigRoot(t, t.TempDir())
	created, err := goalpkg.DefaultStore().Create(context.Background(), goalpkg.CreateInput{Objective: "inspect alias"})
	if err != nil {
		t.Fatal(err)
	}
	var statusOut, inspectOut bytes.Buffer
	if err := goalCommand(context.Background(), []string{"status", created.ID}, options{}, &statusOut, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := goalCommand(context.Background(), []string{"inspect", created.ID}, options{}, &inspectOut, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if inspectOut.String() != statusOut.String() {
		t.Fatalf("inspect differs from status:\n%s\n---\n%s", inspectOut.String(), statusOut.String())
	}
}

func TestGoalErrorsProvideRecovery(t *testing.T) {
	setTestConfigRoot(t, t.TempDir())
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{name: "missing objective", args: []string{"start"}, want: []string{"requires an objective", "goal start", "需要提供目标", goalcmd.HelpHintZH}},
		{name: "unknown subcommand", args: []string{"wat"}, want: []string{"unknown goal command", "goal help", "status", "未知 Goal 子命令", goalcmd.HelpHintZH}},
		{name: "missing run id", args: []string{"run"}, want: []string{"requires a goal id", "goal list", "需要 goal id", goalcmd.HelpHintZH}},
		{name: "missing unlock id", args: []string{"unlock", "--force"}, want: []string{"requires a goal id", "需要 goal id", goalcmd.HelpHintZH}},
		{name: "explicit status without goal", args: []string{"status"}, want: []string{"no active goal found", "goal start", "goal list"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := goalCommand(context.Background(), tc.args, options{}, &bytes.Buffer{}, &bytes.Buffer{})
			if err == nil {
				t.Fatalf("%v must fail", tc.args)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("%v error %q is missing %q", tc.args, err, want)
				}
			}
		})
	}
}

func TestGoalStatusAndLogsShowPlanEvidenceSummary(t *testing.T) {
	root := t.TempDir()
	project := t.TempDir()
	setTestConfigRoot(t, root)
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"goal", "start", "ship cli goal", "--turn-budget", "4", "--token-budget", "1000"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	goals, err := goalpkg.DefaultStore().List(context.Background(), goalpkg.ListFilter{Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(goals) != 1 {
		t.Fatalf("goals = %+v output=%s", goals, out.String())
	}
	goals[0].LastNextAction = "run focused verification"
	if err := goalpkg.DefaultStore().Update(context.Background(), goals[0]); err != nil {
		t.Fatal(err)
	}
	plan := goalpkg.GoalPlan{
		GoalID:        goals[0].ID,
		Version:       1,
		CurrentStepID: "step_verify",
		Steps: []goalpkg.GoalStep{{
			ID:     "step_verify",
			Title:  "Verify CLI output",
			Status: goalpkg.StepStatusActive,
		}},
		AcceptanceCriteria: []goalpkg.GoalCriterion{{
			ID:          "crit_tests",
			Description: "Tests pass",
			Required:    true,
			Status:      goalpkg.CriterionStatusPassed,
		}, {
			ID:          "crit_docs",
			Description: "Docs updated",
			Required:    true,
			Status:      goalpkg.CriterionStatusPending,
		}},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := goalpkg.DefaultStore().SavePlan(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	for _, item := range []goalpkg.GoalEvidence{{
		ID:        "ev_tests",
		GoalID:    goals[0].ID,
		Type:      goalpkg.EvidenceTypeTest,
		Summary:   "go test passed",
		Command:   "go test ./internal/cli -run Goal -count=1",
		Passed:    true,
		CreatedAt: time.Now().UTC(),
	}, {
		ID:        "ev_docs",
		GoalID:    goals[0].ID,
		Type:      goalpkg.EvidenceTypeDoc,
		Summary:   "todo updated",
		Passed:    true,
		CreatedAt: time.Now().UTC(),
	}} {
		if err := goalpkg.DefaultStore().AppendEvidence(context.Background(), item); err != nil {
			t.Fatal(err)
		}
	}
	out.Reset()
	if err := Run(context.Background(), []string{"--cwd", project, "goal", "status", goals[0].ID}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	status := out.String()
	for _, want := range []string{"Next action: run focused verification", "Current step: Verify CLI output [active]", "Criteria: 1/2 passed (required 1/2)", "crit_docs [required pending]", "Recent evidence (2):", "ev_tests [test pass]"} {
		if !strings.Contains(status, want) {
			t.Fatalf("status missing %q:\n%s", want, status)
		}
	}
	out.Reset()
	if err := Run(context.Background(), []string{"--cwd", project, "goal", "logs", goals[0].ID, "--limit", "2"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	logs := out.String()
	if !strings.Contains(logs, "goal_started") || !strings.Contains(logs, "Recent evidence (2):") || !strings.Contains(logs, "ev_docs [doc pass]") {
		t.Fatalf("logs = %s", logs)
	}
}

func TestListTUISlashCommandsIncludesGoal(t *testing.T) {
	project := t.TempDir()
	all, err := listTUISlashCommands(project, "go")
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range all {
		if command.Name == "goal" && command.Source == "builtin" {
			return
		}
	}
	t.Fatalf("goal slash command missing: %+v", all)
}

func TestListTUISlashCommandsIncludesRecap(t *testing.T) {
	project := t.TempDir()
	all, err := listTUISlashCommands(project, "re")
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range all {
		if command.Name == "recap" && command.Source == "builtin" {
			return
		}
	}
	t.Fatalf("recap slash command missing: %+v", all)
}

func TestGoalRunOnceRecordsCheckpointUsageAndEvents(t *testing.T) {
	root := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", root)
	setTestConfigRoot(t, root)
	t.Setenv("CLAUDE_CODE_MODEL", "")
	t.Chdir(project)
	startGoldenAnthropicServer(t)
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"goal", "start", "finish", "once", "--turn-budget", "2", "--token-budget", "1000"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	goals, err := goalpkg.DefaultStore().List(context.Background(), goalpkg.ListFilter{Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(goals) != 1 {
		t.Fatalf("goals = %+v output=%s", goals, out.String())
	}
	out.Reset()
	if err := Run(context.Background(), []string{"--cwd", project, "goal", "run", goals[0].ID, "--once"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	updated, err := goalpkg.DefaultStore().Get(context.Background(), goals[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.TurnsUsed != 1 || updated.InputTokens != 5 || updated.OutputTokens != 2 || updated.LastCheckpoint != "goal:"+updated.ID+":turn:1" {
		t.Fatalf("updated = %+v", updated)
	}
	summary, ok, err := session.DefaultStore().Find(updated.SessionID)
	if err != nil || !ok {
		t.Fatalf("Find session ok=%v err=%v", ok, err)
	}
	entries, err := session.Load(summary.Path)
	if err != nil {
		t.Fatal(err)
	}
	var foundGoalCheckpoint bool
	for _, entry := range entries {
		if entry.Type == "checkpoint" && entry.Name == updated.LastCheckpoint {
			foundGoalCheckpoint = true
			break
		}
	}
	if !foundGoalCheckpoint {
		t.Fatalf("goal checkpoint not found in entries: %+v", entries)
	}
	events, err := goalpkg.DefaultStore().ListEvents(context.Background(), updated.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) < 3 || events[len(events)-1].Type != goalpkg.EventTurnFinished {
		t.Fatalf("events = %+v", events)
	}
}

func TestGoalToolEvidenceTracesMapsQueryToolCalls(t *testing.T) {
	traces := goalToolEvidenceTraces([]query.ToolTrace{{
		ID:      "toolu_fail",
		Name:    "Bash",
		Input:   `{"command":"go test ./..."}`,
		Output:  "FAIL",
		IsError: true,
	}})
	evidence := goalpkg.EvidenceFromToolTraces("goal_test", traces, time.Now().UTC())
	if len(evidence) != 1 || evidence[0].Type != goalpkg.EvidenceTypeTest || evidence[0].Passed {
		t.Fatalf("evidence = %+v", evidence)
	}
}

func TestGoalAgentTaskEvidenceCollectsTerminalCapabilityLoopWithoutAgentGet(t *testing.T) {
	store := memstore.New()
	ctx := context.Background()
	taskID, err := store.CreateAgentTask(ctx, agenttasks.TaskInput{
		AgentName:   "reviewer",
		Description: "Inspect failing task",
		Status:      agenttasks.StatusRunning,
	})
	if err != nil {
		t.Fatal(err)
	}
	resultJSON := `{
		"status":"failed",
		"capability_loop":{
			"evidence":["sub-agent found the provider 403 fallback gap"],
			"assumptions":["fallback provider is configured"],
			"unknowns":["whether retry state was reset"],
			"verification":["run deepseek-v4-flash fallback acceptance"],
			"risks":["parent may otherwise finish without this blocker"],
			"next_action":"patch provider fallback then rerun acceptance"
		}
	}`
	if err := store.FinishAgentTask(ctx, taskID, agenttasks.StatusFailed, resultJSON); err != nil {
		t.Fatal(err)
	}

	evidence := goalAgentTaskEvidence(ctx, "goal_agent", store, nil, time.Date(2026, 7, 4, 1, 2, 3, 0, time.UTC))
	if len(evidence) != 1 {
		t.Fatalf("evidence = %+v", evidence)
	}
	item := evidence[0]
	if item.ID != "ev_agent_task_1" || item.Passed || !strings.Contains(item.Summary, "TaskStore failed partial evidence") || !strings.Contains(item.Summary, "provider 403 fallback gap") || !strings.Contains(item.Summary, "patch provider fallback") {
		t.Fatalf("item = %+v", item)
	}
	var payload struct {
		EvidenceSource  string `json:"evidence_source"`
		AgentTaskID     uint64 `json:"agent_task_id"`
		AgentStatus     string `json:"agent_status"`
		PartialEvidence bool   `json:"partial_evidence"`
		CapabilityLoop  struct {
			Evidence   []string `json:"evidence"`
			NextAction string   `json:"next_action"`
		} `json:"capability_loop"`
	}
	if err := json.Unmarshal(item.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.EvidenceSource != "terminal_agent_task_store" || payload.AgentTaskID != taskID || payload.AgentStatus != agenttasks.StatusFailed || !payload.PartialEvidence || len(payload.CapabilityLoop.Evidence) != 1 || payload.CapabilityLoop.NextAction == "" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestGoalAgentTaskEvidenceSkipsTaskAlreadyReadByAgentGet(t *testing.T) {
	store := memstore.New()
	ctx := context.Background()
	taskID, err := store.CreateAgentTask(ctx, agenttasks.TaskInput{Status: agenttasks.StatusRunning})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinishAgentTask(ctx, taskID, agenttasks.StatusFailed, `{
		"status":"failed",
		"capability_loop":{"evidence":["already read"],"next_action":"none"}
	}`); err != nil {
		t.Fatal(err)
	}

	evidence := goalAgentTaskEvidence(ctx, "goal_agent", store, []query.ToolTrace{{
		Name:   "AgentGet",
		Input:  fmt.Sprintf(`{"task_id":%d}`, taskID),
		Output: fmt.Sprintf(`{"task":{"id":%d,"status":"failed"},"result":{"status":"failed","capability_loop":{"evidence":["already read"],"next_action":"none"}}}`, taskID),
	}}, time.Now().UTC())
	if len(evidence) != 0 {
		t.Fatalf("evidence = %+v", evidence)
	}
}

func TestGoalStartWithCWDCreatesSessionInGoalCWD(t *testing.T) {
	root := t.TempDir()
	base := t.TempDir()
	defaultProject := filepath.Join(base, "default")
	goalProject := filepath.Join(base, "goal")
	if err := os.MkdirAll(defaultProject, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(goalProject, 0755); err != nil {
		t.Fatal(err)
	}
	setTestConfigRoot(t, root)
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"--cwd", defaultProject, "goal", "start", "cwd", "target", "--cwd", goalProject}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	goals, err := goalpkg.DefaultStore().List(context.Background(), goalpkg.ListFilter{Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(goals) != 1 || goals[0].CWD != goalProject {
		t.Fatalf("goals = %+v", goals)
	}
	summary, ok, err := session.DefaultStore().Find(goals[0].SessionID)
	if err != nil || !ok {
		t.Fatalf("Find session ok=%v err=%v", ok, err)
	}
	wantSlug := session.ProjectSlug(goalProject)
	if !strings.Contains(summary.Path, wantSlug) {
		t.Fatalf("session path %q does not contain goal project slug %q", summary.Path, wantSlug)
	}
	if strings.Contains(summary.Path, session.ProjectSlug(defaultProject)) {
		t.Fatalf("session path %q used default project slug", summary.Path)
	}
}

func TestGoalRunOnceFailedTurnReturnsCLIError(t *testing.T) {
	root := t.TempDir()
	setTestConfigRoot(t, root)
	t.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", filepath.Join(root, "projects"))
	store := goalpkg.DefaultStore()
	goal, err := store.Create(context.Background(), goalpkg.CreateInput{
		Objective: "missing session",
		SessionID: "11111111-1111-4111-8111-111111111111",
		CWD:       t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = Run(context.Background(), []string{"goal", "run", goal.ID, "--once"}, &out, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "turn failed") {
		t.Fatalf("err=%v output=%s", err, out.String())
	}
	updated, err := store.Get(context.Background(), goal.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != goalpkg.StatusActive || updated.TurnsUsed != 1 {
		t.Fatalf("updated = %+v", updated)
	}
	events, err := store.ListEvents(context.Background(), goal.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) < 3 || events[len(events)-1].Type != goalpkg.EventTurnFailed {
		t.Fatalf("events = %+v", events)
	}
}

func TestGoalUnlockRequiresForceAndClearsLock(t *testing.T) {
	root := t.TempDir()
	setTestConfigRoot(t, root)
	store := goalpkg.DefaultStore()
	goal, err := store.Create(context.Background(), goalpkg.CreateInput{Objective: "unlock cli", CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := store.LockGoal(context.Background(), goal.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"goal", "unlock", goal.ID}, &out, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("expected --force error, got err=%v out=%s", err, out.String())
	}
	out.Reset()
	if err := Run(context.Background(), []string{"goal", "unlock", goal.ID, "--force"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Unlocked goal "+goal.ID) {
		t.Fatalf("out = %s", out.String())
	}
	if unlock2, err := store.LockGoal(context.Background(), goal.ID); err != nil {
		t.Fatalf("lock after force unlock err = %v", err)
	} else {
		unlock2()
	}
}

func TestLoopSlashCommandQueuesRecurringBackgroundJob(t *testing.T) {
	root := t.TempDir()
	setTestConfigRoot(t, root)
	t.Setenv("NIUMA_BG_QUEUE_ONLY", "1")
	var out bytes.Buffer
	handled, exit, err := handleInteractiveSlash(context.Background(), options{cwd: t.TempDir(), model: "test-model", maxTurns: 1}, "/loop 5m check deploy", &out, &bytes.Buffer{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !handled || exit || !strings.Contains(out.String(), "Queued loop") || !strings.Contains(out.String(), "every 5 minutes") {
		t.Fatalf("handled=%v exit=%v output=%s", handled, exit, out.String())
	}
	jobs, err := background.DefaultStore().List()
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Kind != "loop" || jobs[0].IntervalSeconds != 300 || jobs[0].Prompt != "check deploy" {
		t.Fatalf("jobs = %+v", jobs)
	}
	scheduleStore := scheduler.DefaultStore()
	schedules, err := scheduleStore.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(schedules) != 1 || schedules[0].BackgroundID != jobs[0].ID || schedules[0].Spec != "@every 5m" || !schedules[0].Enabled {
		t.Fatalf("schedules = %+v", schedules)
	}
}

func TestTUIBackgroundWatcherStartsAtCurrentSchedulerEventOffset(t *testing.T) {
	root := t.TempDir()
	cwd := t.TempDir()
	setTestConfigRoot(t, root)
	bgStore := background.DefaultStore()
	job, err := bgStore.CreateWithOptions(background.Options{Prompt: "drink water", CWD: cwd, Kind: "loop", IntervalSeconds: 600})
	if err != nil {
		t.Fatal(err)
	}
	scheduleStore := scheduler.DefaultStore()
	schedule, err := scheduleStore.Create(scheduler.Options{Prompt: "drink water", CWD: cwd, Kind: "loop", Spec: "@every 10m", IntervalSeconds: 600}, job)
	if err != nil {
		t.Fatal(err)
	}
	if err := scheduleStore.AppendEvent(scheduler.Event{Type: "run_finished", ScheduleID: schedule.ID, BackgroundID: job.ID, Prompt: "old run", CWD: cwd, Status: "completed", RunCount: 1}); err != nil {
		t.Fatal(err)
	}
	watcher := tuiBackgroundWatcher(cwd)
	updates, err := watcher(context.Background(), map[string]tui.BackgroundSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	for _, update := range updates {
		if update.ID == job.ID && !update.Silent {
			t.Fatalf("first watcher poll should not surface historical scheduler event: %+v", updates)
		}
	}
	eventSnapshot := tui.BackgroundSnapshot{}
	for _, update := range updates {
		if update.ID == "__scheduler_events__" {
			eventSnapshot.EventOffset = int64(update.LogSize)
		}
	}
	if eventSnapshot.EventOffset <= 0 {
		t.Fatalf("missing scheduler event baseline update: %+v", updates)
	}
	finished := time.Now().UTC()
	if _, _, err := bgStore.RecordLoopRun(job.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := scheduleStore.AppendEvent(scheduler.Event{Type: "run_finished", ScheduleID: schedule.ID, BackgroundID: job.ID, Prompt: "new run", CWD: cwd, Status: "completed", RunCount: 2, CreatedAt: finished}); err != nil {
		t.Fatal(err)
	}
	updates, err = watcher(context.Background(), map[string]tui.BackgroundSnapshot{"__scheduler_events__": eventSnapshot, job.ID: {RunCount: 1, Status: "running"}})
	if err != nil {
		t.Fatal(err)
	}
	visible := false
	for _, update := range updates {
		if update.ID == job.ID && !update.Silent && update.Prompt == "new run" {
			visible = true
		}
	}
	if !visible {
		t.Fatalf("new scheduler event should be visible after baseline: %+v", updates)
	}
}

func TestTUIBackgroundWatcherSurfacesBashJobUpdates(t *testing.T) {
	root := t.TempDir()
	cwd := t.TempDir()
	setTestConfigRoot(t, root)
	bgStore := background.DefaultStore()
	job, err := bgStore.CreateWithOptions(background.Options{Prompt: "npm dev server", CWD: cwd, Kind: "bash"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(job.LogPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(job.LogPath, []byte("boot"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := bgStore.MarkRunning(job.ID, 12345); err != nil || !ok {
		t.Fatalf("MarkRunning ok=%v err=%v", ok, err)
	}
	watcher := tuiBackgroundWatcher(cwd)
	updates, err := watcher(context.Background(), map[string]tui.BackgroundSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	for _, update := range updates {
		if update.ID == job.ID && !update.Silent {
			t.Fatalf("first watcher poll should silently baseline bash job: %+v", updates)
		}
	}

	if err := os.WriteFile(job.LogPath, []byte("boot\nready"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := bgStore.Finish(job.ID, 0, ""); err != nil || !ok {
		t.Fatalf("Finish ok=%v err=%v", ok, err)
	}
	updates, err = watcher(context.Background(), map[string]tui.BackgroundSnapshot{
		job.ID: {Status: "running", LogSize: len("boot")},
	})
	if err != nil {
		t.Fatal(err)
	}
	visible := false
	for _, update := range updates {
		if update.ID == job.ID && update.Kind == "bash" && !update.Silent && update.Status == "completed" && strings.Contains(update.LogTail, "ready") {
			visible = true
		}
	}
	if !visible {
		t.Fatalf("bash job completion should be visible after baseline: %+v", updates)
	}
}

func TestBackgroundKillDisablesLoopSchedule(t *testing.T) {
	root := t.TempDir()
	setTestConfigRoot(t, root)
	t.Setenv("NIUMA_BG_QUEUE_ONLY", "1")
	var out bytes.Buffer
	if _, _, err := handleInteractiveSlash(context.Background(), options{cwd: t.TempDir(), model: "test-model", maxTurns: 1}, "/loop 5m check deploy", &out, &bytes.Buffer{}, nil); err != nil {
		t.Fatal(err)
	}
	jobs, err := background.DefaultStore().List()
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("jobs = %+v", jobs)
	}
	out.Reset()
	if err := backgroundKillCommand([]string{jobs[0].ID}, &out); err != nil {
		t.Fatal(err)
	}
	scheduleStore := scheduler.DefaultStore()
	schedule, ok, err := scheduleStore.Find(jobs[0].ID)
	if err != nil || !ok {
		t.Fatalf("Find schedule ok=%v err=%v", ok, err)
	}
	if schedule.Enabled {
		t.Fatalf("schedule should be disabled: %+v", schedule)
	}
}

func TestParseLoopArgs(t *testing.T) {
	tests := []struct {
		raw     string
		seconds int
		prompt  string
		hasNote bool
	}{
		{raw: "5m /babysit-prs", seconds: 300, prompt: "/babysit-prs"},
		{raw: "check the deploy every 20m", seconds: 1200, prompt: "check the deploy"},
		{raw: "run tests every 5 minutes", seconds: 300, prompt: "run tests"},
		{raw: "check every PR", seconds: 600, prompt: "check every PR"},
		{raw: "1 提示喝水", seconds: 60, prompt: "提示喝水", hasNote: true},
		{raw: "30s quick check", seconds: 60, prompt: "quick check", hasNote: true},
	}
	for _, tt := range tests {
		interval, prompt, note, err := parseLoopArgs(tt.raw)
		if err != nil {
			t.Fatalf("%q err=%v", tt.raw, err)
		}
		if int(interval.Seconds()) != tt.seconds || prompt != tt.prompt || (note != "") != tt.hasNote {
			t.Fatalf("%q => interval=%s prompt=%q note=%q", tt.raw, interval, prompt, note)
		}
	}
}

func TestInteractiveSlashHelpUsesConfiguredGuidanceFilename(t *testing.T) {
	project := t.TempDir()
	configDir := filepath.Join(t.TempDir(), "config")
	t.Setenv("NIUMA_CONFIG_DIR", configDir)
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	mustWrite(t, filepath.Join(configDir, "settings.json"), `{
	  "identity": {"guidanceFilename": "agentx.md"}
	}`)

	var out bytes.Buffer
	handled, exit, err := handleInteractiveSlash(
		context.Background(),
		options{cwd: project, model: "test-model", maxTurns: 1},
		"/help",
		&out,
		&bytes.Buffer{},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !handled || exit {
		t.Fatalf("handled=%v exit=%v output=%s", handled, exit, out.String())
	}
	if !strings.Contains(out.String(), "初始化或优化 agentx.md") {
		t.Fatalf("help output did not use configured guidance filename:\n%s", out.String())
	}
	if strings.Contains(out.String(), "初始化或优化 niuma.md") {
		t.Fatalf("help output used the canonical filename despite configured identity:\n%s", out.String())
	}
}

func TestLoopRunPromptResolvesSlashCommand(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	commandDir := filepath.Join(project, ".claude", "commands")
	if err := os.MkdirAll(commandDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(commandDir, "babysit-prs.md"), []byte("Watch PRs for $ARGUMENTS."), 0644); err != nil {
		t.Fatal(err)
	}
	prompt := loopRunPrompt(project, "/babysit-prs deploy")
	if !strings.Contains(prompt, "Command: /babysit-prs") || !strings.Contains(prompt, "Watch PRs for deploy.") {
		t.Fatalf("prompt = %s", prompt)
	}
	if got := loopRunPrompt(project, "/missing-command deploy"); got != "/missing-command deploy" {
		t.Fatalf("missing command prompt = %q", got)
	}
	if got := loopRunPrompt(project, "/init"); !strings.Contains(got, "create or improve `niuma.md`") {
		t.Fatalf("init command prompt = %q", got)
	}
}

func TestResolvePrintPromptResolvesSlashCommand(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())

	prompt, err := resolvePrintPrompt(project, "/init")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "create or improve `niuma.md`") || strings.Contains(prompt, "Command: /init") {
		t.Fatalf("print prompt did not resolve /init:\n%s", prompt)
	}

	prompt, err = resolvePrintPrompt(project, "plain prompt")
	if err != nil {
		t.Fatal(err)
	}
	if prompt != "plain prompt" {
		t.Fatalf("plain prompt = %q", prompt)
	}

	prompt, err = resolvePrintPrompt(project, "/missing-command deploy")
	if err != nil {
		t.Fatal(err)
	}
	if prompt != "/missing-command deploy" {
		t.Fatalf("missing slash prompt = %q", prompt)
	}
}

func TestDynamicSlashPromptLoadsUserInvocableSkillAndCommand(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	commandDir := filepath.Join(project, ".claude", "commands")
	if err := os.MkdirAll(commandDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(commandDir, "verify.md"), []byte("---\ndescription: Verify command\n---\nRun verification for $ARGUMENTS."), 0644); err != nil {
		t.Fatal(err)
	}
	prompt, ok, err := dynamicSlashPrompt(project, "/verify all packages")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || !strings.Contains(prompt, "Command: /verify") || !strings.Contains(prompt, "Run verification for all packages.") || strings.Contains(prompt, "---") {
		t.Fatalf("dynamic command prompt ok=%v prompt=%s", ok, prompt)
	}

	skillDir := filepath.Join(project, ".claude", "skills", "office-hours")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\ndescription: Office hours\n---\nAsk focused questions about {{arguments}}."), 0644); err != nil {
		t.Fatal(err)
	}
	prompt, ok, err = dynamicSlashPrompt(project, "/office-hours product idea")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || !strings.Contains(prompt, "Source: skill") || !strings.Contains(prompt, "Ask focused questions about product idea.") {
		t.Fatalf("dynamic skill prompt ok=%v prompt=%s", ok, prompt)
	}
}

func TestDynamicSlashPromptLoadsStandalonePluginSkillLocalName(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_MODEL", "")
	pluginRoot := filepath.Join(home, ".claude", "mattpocock-skills")
	mustWrite(t, filepath.Join(pluginRoot, ".claude-plugin", "plugin.json"), `{
	  "name":"mattpocock-skills",
	  "skills":["./skills/productivity/teach"]
	}`)
	mustWrite(t, filepath.Join(pluginRoot, "skills", "productivity", "teach", "SKILL.md"), `---
name: teach
description: Teach a concept
disable-model-invocation: true
---
# Teach

Teach $ARGUMENTS.`)

	prompt, ok, err := dynamicSlashPrompt(project, "/teach Go generics")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || !strings.Contains(prompt, "Command: /teach") || !strings.Contains(prompt, "Teach Go generics.") {
		t.Fatalf("prompt missing standalone plugin skill content:\n%s", prompt)
	}
}

func TestDynamicSlashPromptRejectsNonUserInvocableSkill(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	skillDir := filepath.Join(project, ".claude", "skills", "hidden")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\ndescription: Hidden\nuser-invocable: false\n---\nHidden body"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := dynamicSlashPrompt(project, "/hidden"); err == nil {
		t.Fatal("expected non-user-invocable slash command error")
	}
}

func TestListTUISlashCommandsIncludesBuiltinsSkillsAndCustomCommands(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(project, ".claude", "commands"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".claude", "commands", "verify.md"), []byte("---\ndescription: Verify project\n---\nVerify $ARGUMENTS."), 0644); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(project, ".claude", "skills", "office-hours")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\ndescription: Product brainstorm\n---\nBrainstorm."), 0644); err != nil {
		t.Fatal(err)
	}

	all, err := listTUISlashCommands(project, "")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	for _, command := range all {
		names[command.Name] = command.Source
	}
	for _, command := range slashcommands.Builtins() {
		if names[command.Name] != "builtin" {
			t.Fatalf("builtin slash command %s source = %q, all=%+v", command.Name, names[command.Name], all)
		}
	}
	for name, source := range map[string]string{"verify": "custom", "office-hours": "project"} {
		if names[name] != source {
			t.Fatalf("command %s source = %q, all=%+v", name, names[name], all)
		}
	}

	filtered, err := listTUISlashCommands(project, "off")
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].Name != "office-hours" {
		t.Fatalf("filtered commands = %+v", filtered)
	}
}

func TestListTUISlashCommandsIncludesStandalonePluginSkillLocalNames(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_MODEL", "")
	pluginRoot := filepath.Join(home, ".claude", "mattpocock-skills")
	mustWrite(t, filepath.Join(pluginRoot, ".claude-plugin", "plugin.json"), `{
	  "name":"mattpocock-skills",
	  "skills":["./skills/productivity/teach"]
	}`)
	mustWrite(t, filepath.Join(pluginRoot, "skills", "productivity", "teach", "SKILL.md"), `---
name: teach
description: Teach a concept
disable-model-invocation: true
---
# Teach`)

	filtered, err := listTUISlashCommands(project, "t")
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range filtered {
		if command.Name == "teach" && command.Source == "plugin skill" {
			return
		}
	}
	t.Fatalf("teach slash command missing from %+v", filtered)
}

func TestSessionListShowsReadableRecentConversation(t *testing.T) {
	root := t.TempDir()
	setTestConfigRoot(t, root)
	t.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", filepath.Join(root, "projects"))
	store := session.DefaultStore()
	recorder, err := store.NewRecorderWithID("/tmp/project-one", "11111111-1111-4111-8111-111111111111")
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{Type: "message", Role: "user", Content: "please inspect the auth flow"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{Type: "message", Role: "assistant", Content: "I found the latest issue in the callback handler"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := sessionCommand([]string{"list"}, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"1. please inspect the auth flow", "id: 11111111-1111-4111-8111-111111111111", "recent: assistant: I found the latest issue", "project: /tmp/project"} {
		if !strings.Contains(text, want) {
			t.Fatalf("session list missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "\t") {
		t.Fatalf("session list should not use hard-to-read tabular output:\n%s", text)
	}
}

func TestSessionListShowsRecentSlashCommandText(t *testing.T) {
	root := t.TempDir()
	setTestConfigRoot(t, root)
	t.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", filepath.Join(root, "projects"))
	store := session.DefaultStore()
	recorder, err := store.NewRecorderWithID("/tmp/project-command", "33333333-3333-4333-8333-333333333333")
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{Type: "message", Role: "user", Content: "ordinary question"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{Type: "message", Role: "user", Content: "/goal status"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := sessionCommand([]string{"list"}, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "recent: user: /goal status") {
		t.Fatalf("session list should expose recent slash command text:\n%s", text)
	}
}

func TestResumeSessionItemsOnlyShowsCurrentProject(t *testing.T) {
	root := t.TempDir()
	store := session.Store{Root: root}
	currentID := "44444444-4444-4444-8444-444444444444"
	current, err := store.NewRecorderWithID("/tmp/current-app", currentID)
	if err != nil {
		t.Fatal(err)
	}
	if err := current.Append(session.Entry{Type: "message", Role: "user", Content: "current app design"}); err != nil {
		t.Fatal(err)
	}
	if err := current.Close(); err != nil {
		t.Fatal(err)
	}
	other, err := store.NewRecorderWithID("/tmp/other-app", "55555555-5555-4555-8555-555555555555")
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Append(session.Entry{Type: "message", Role: "user", Content: "other project"}); err != nil {
		t.Fatal(err)
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}

	items, err := resumeSessionItems(store, "/tmp/current-app", nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != currentID || !strings.Contains(items[0].Preview, "current app design") {
		t.Fatalf("items = %+v", items)
	}
}

func TestResumeSessionItemsExcludesCurrentSessionBeforeLimit(t *testing.T) {
	root := t.TempDir()
	store := session.Store{Root: root}
	project := "/tmp/resume-filter-app"
	olderID := "11111111-1111-4111-8111-111111111111"
	older, err := store.NewRecorderWithID(project, olderID)
	if err != nil {
		t.Fatal(err)
	}
	if err := older.Append(session.Entry{Type: "message", Role: "user", Content: "older session"}); err != nil {
		t.Fatal(err)
	}
	if err := older.Close(); err != nil {
		t.Fatal(err)
	}

	currentID := "22222222-2222-4222-8222-222222222222"
	current, err := store.NewRecorderWithID(project, currentID)
	if err != nil {
		t.Fatal(err)
	}
	if err := current.Append(session.Entry{Type: "message", Role: "user", Content: "current session"}); err != nil {
		t.Fatal(err)
	}
	if err := current.Close(); err != nil {
		t.Fatal(err)
	}

	items, err := resumeSessionItems(store, project, []string{currentID}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != olderID {
		t.Fatalf("items = %+v, want only older session", items)
	}
}

func TestResumeSessionItemsExcludesRecorderAndLogicalResumeSessions(t *testing.T) {
	root := t.TempDir()
	store := session.Store{Root: root}
	project := "/tmp/resume-active-sessions"
	ids := []string{
		"11111111-1111-4111-8111-111111111111",
		"22222222-2222-4222-8222-222222222222",
		"33333333-3333-4333-8333-333333333333",
	}
	for _, id := range ids {
		recorder, err := store.NewRecorderWithID(project, id)
		if err != nil {
			t.Fatal(err)
		}
		if err := recorder.Append(session.Entry{Type: "message", Role: "user", Content: id}); err != nil {
			t.Fatal(err)
		}
		if err := recorder.Close(); err != nil {
			t.Fatal(err)
		}
	}

	items, err := resumeSessionItems(store, project, ids[:2], 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != ids[2] {
		t.Fatalf("items = %+v, want only %s", items, ids[2])
	}
}

func TestInteractiveResumeCommandConfirmsSelectedSession(t *testing.T) {
	root := t.TempDir()
	store := session.Store{Root: root}
	id := "22222222-2222-4222-8222-222222222222"
	recorder, err := store.NewRecorderWithID("/tmp/project-two", id)
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{Type: "message", Role: "user", Content: "continue writing tests"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := interactiveResumeCommand(store, id, nil, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"Resumed session " + id, "Recent: user: continue writing tests", "Next message will use this transcript as context."} {
		if !strings.Contains(text, want) {
			t.Fatalf("resume confirmation missing %q:\n%s", want, text)
		}
	}
}

func TestInteractiveResumeCommandDoesNotResumeCurrentSession(t *testing.T) {
	root := t.TempDir()
	store := session.Store{Root: root}
	id := "77777777-7777-4777-8777-777777777777"
	recorder, err := store.NewRecorderWithID("/tmp/current-project", id)
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()

	var out bytes.Buffer
	if err := interactiveResumeCommand(store, id, []string{id}, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "Session is already active") || strings.Contains(got, "Resumed session") {
		t.Fatalf("unexpected current-session response:\n%s", got)
	}
}

type captureMessagesStreamer struct {
	requests []anthropic.MessagesRequest
}

func (s *captureMessagesStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	if cb.OnText != nil {
		cb.OnText("ok")
	}
	return &anthropic.StreamResult{
		Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "ok"}}},
		Usage:   anthropic.Usage{InputTokens: 1, OutputTokens: 1},
	}, nil
}

func TestInteractiveResumeSelectionInjectsHistoryIntoNextTurn(t *testing.T) {
	root := t.TempDir()
	setTestConfigRoot(t, root)
	store := session.Store{Root: root}
	resumeID := "66666666-6666-4666-8666-666666666666"
	resumed, err := store.NewRecorderWithID("/tmp/app-design", resumeID)
	if err != nil {
		t.Fatal(err)
	}
	if err := resumed.Append(session.Entry{Type: "message", Role: "user", Content: "请设计一个高级感 app 界面"}); err != nil {
		t.Fatal(err)
	}
	if err := resumed.Append(session.Entry{Type: "message", Role: "assistant", Content: "高级感 app 需要克制色彩、清晰层级和精致动效"}); err != nil {
		t.Fatal(err)
	}
	if err := resumed.Close(); err != nil {
		t.Fatal(err)
	}
	current, err := store.NewRecorderWithID("/tmp/app-design", "77777777-7777-4777-8777-777777777777")
	if err != nil {
		t.Fatal(err)
	}
	if err := current.Append(session.Entry{Type: "message", Role: "user", Content: "1+1="}); err != nil {
		t.Fatal(err)
	}
	if err := current.Append(session.Entry{Type: "message", Role: "assistant", Content: "2"}); err != nil {
		t.Fatal(err)
	}
	defer current.Close()

	activeResumeMessages, err := loadResumeMessages(store, resumeID, "")
	if err != nil {
		t.Fatal(err)
	}
	initial := append([]anthropic.MessageParam(nil), activeResumeMessages...)

	streamer := &captureMessagesStreamer{}
	querySession := query.New(streamer, tools.NewRegistry(), query.Options{
		Model:           "test-model",
		MaxTurns:        1,
		MaxTokens:       64,
		CWD:             "/tmp/app-design",
		Recorder:        current,
		InitialMessages: initial,
	})
	if _, err := querySession.Run(context.Background(), "继续", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(streamer.requests) != 1 {
		t.Fatalf("requests = %d", len(streamer.requests))
	}
	text := messagesRequestText(streamer.requests[0].Messages)
	for _, want := range []string{"请设计一个高级感 app 界面", "高级感 app 需要克制色彩", "继续"} {
		if !strings.Contains(text, want) {
			t.Fatalf("model request missing resumed context %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "1+1=") {
		t.Fatalf("model request should not include pre-resume current session context:\n%s", text)
	}
}

func TestResumeContinuationMessagesIncludeOnlyPostResumeEntries(t *testing.T) {
	root := t.TempDir()
	store := session.Store{Root: root}
	recorder, err := store.NewRecorderWithID("/tmp/resume-continuation", "88888888-8888-4888-8888-888888888888")
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	if err := recorder.Append(session.Entry{Type: "message", Role: "user", Content: "before resume"}); err != nil {
		t.Fatal(err)
	}

	offset, err := transcriptEntryCount(recorder)
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{Type: "message", Role: "user", Content: "after resume question"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{Type: "message", Role: "assistant", Content: "after resume answer"}); err != nil {
		t.Fatal(err)
	}

	messages, _, err := resumeContinuationContext(recorder, offset)
	if err != nil {
		t.Fatal(err)
	}
	text := messagesRequestText(messages)
	if strings.Contains(text, "before resume") {
		t.Fatalf("continuation includes pre-resume entry:\n%s", text)
	}
	for _, want := range []string{"after resume question", "after resume answer"} {
		if !strings.Contains(text, want) {
			t.Fatalf("continuation missing %q:\n%s", want, text)
		}
	}
}

func TestCompactResumedSessionExcludesPreResumeRecorderEntries(t *testing.T) {
	root := t.TempDir()
	setTestConfigRoot(t, root)
	store := session.DefaultStore()
	project := "/tmp/resume-compact-boundary"
	resumeID := "99999999-9999-4999-8999-999999999999"
	resumed, err := store.NewRecorderWithID(project, resumeID)
	if err != nil {
		t.Fatal(err)
	}
	if err := resumed.Append(session.Entry{Type: "message", Role: "user", Content: "resume-source-marker"}); err != nil {
		t.Fatal(err)
	}
	if err := resumed.Close(); err != nil {
		t.Fatal(err)
	}

	current, err := store.NewRecorderWithID(project, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	if err := current.Append(session.Entry{Type: "message", Role: "user", Content: "pre-resume-secret"}); err != nil {
		t.Fatal(err)
	}
	offset, err := transcriptEntryCount(current)
	if err != nil {
		t.Fatal(err)
	}
	if err := current.Append(session.Entry{Type: "message", Role: "assistant", Content: "post-resume-marker"}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := compactSlashCommand(nil, options{resume: resumeID, resumeContinuationEntryOffset: offset}, current, &out); err != nil {
		t.Fatal(err)
	}
	entries, err := session.Load(current.Path)
	if err != nil {
		t.Fatal(err)
	}
	summary := entries[len(entries)-1]
	if summary.Type != "compact_summary" {
		t.Fatalf("last entry = %+v", summary)
	}
	if strings.Contains(summary.Content, "pre-resume-secret") {
		t.Fatalf("compact summary leaked pre-resume content:\n%s", summary.Content)
	}
	for _, want := range []string{"resume-source-marker", "post-resume-marker"} {
		if !strings.Contains(summary.Content, want) {
			t.Fatalf("compact summary missing %q:\n%s", want, summary.Content)
		}
	}
}

func TestCompactSlashCommandKeepsTheRecentConversation(t *testing.T) {
	root := t.TempDir()
	setTestConfigRoot(t, root)
	recorder, err := session.DefaultStore().NewRecorderWithID("/tmp/compact-recency", "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	// Comfortably past the default 12KB summary budget so the summary must drop
	// something, and it must be the oldest turns that go.
	const turns = 400
	for i := 1; i <= turns; i++ {
		if err := recorder.Append(session.Entry{
			Type:    "message",
			Role:    "user",
			Content: fmt.Sprintf("turn-%03d %s", i, strings.Repeat("x", 60)),
		}); err != nil {
			t.Fatal(err)
		}
	}

	var out bytes.Buffer
	if err := compactSlashCommand(nil, options{}, recorder, &out); err != nil {
		t.Fatal(err)
	}
	entries, err := session.Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	summary := entries[len(entries)-1]
	if summary.Type != "compact_summary" {
		t.Fatalf("last entry = %+v, want compact_summary", summary)
	}
	if !strings.Contains(summary.Content, fmt.Sprintf("turn-%03d", turns)) {
		t.Fatalf("/compact dropped the newest turn; it must keep the recent conversation, not the oldest:\n%s", tailOf(summary.Content, 400))
	}
	if strings.Contains(summary.Content, "turn-001") {
		t.Fatalf("/compact kept the oldest turn instead of recent context:\n%s", tailOf(summary.Content, 400))
	}
}

func tailOf(text string, n int) string {
	if len(text) <= n {
		return text
	}
	return "..." + text[len(text)-n:]
}

func messagesRequestText(messages []anthropic.MessageParam) string {
	var parts []string
	for _, message := range messages {
		for _, block := range message.Content {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func TestConfigureTUILoggerWritesToFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tui.log")
	t.Setenv("NIUMA_TUI_LOG_PATH", path)
	t.Setenv("NIUMA_LOG_LEVEL", "debug")
	restore := configureTUILogger()
	defer restore()

	observability.Debug(context.Background(), nil, "tui.test", "cli.TestConfigureTUILoggerWritesToFile", "tui debug line")
	_ = observability.DefaultZapLogger().Sync()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "tui debug line") {
		t.Fatalf("log file = %s", data)
	}
}

func TestTelemetryExportHeadersSupportVendorAPIKeysAndExtraHeaders(t *testing.T) {
	t.Setenv("NIUMA_TELEMETRY_EXPORT_TOKEN", "bearer-token")
	t.Setenv("NIUMA_TELEMETRY_EXPORT_API_KEY", "vendor-key")
	t.Setenv("NIUMA_TELEMETRY_EXPORT_HEADERS", `{"X-Scope":"tenant","x-api-key":"override-key"}`)

	headers := telemetryExportHeaders("datadog")
	if headers["Authorization"] != "Bearer bearer-token" {
		t.Fatalf("authorization = %q", headers["Authorization"])
	}
	if headers["DD-API-KEY"] != "vendor-key" {
		t.Fatalf("datadog api key = %q", headers["DD-API-KEY"])
	}
	if headers["X-Scope"] != "tenant" {
		t.Fatalf("extra header = %+v", headers)
	}

	headers = telemetryExportHeaders("otel")
	if headers["x-api-key"] != "override-key" {
		t.Fatalf("otel api key override = %+v", headers)
	}
}

func TestParseStructuredSkillRoutes(t *testing.T) {
	routes, err := parseStructuredSkillRoutes(`[{"schema_name":" teach_decision_v1 ","skill_key":" teach-v2 "},{"schema_name":"","skill_key":"ignored"}]`)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 || routes[0].SchemaName != "teach_decision_v1" || routes[0].SkillKey != "teach-v2" {
		t.Fatalf("routes = %+v", routes)
	}
	if _, err := parseStructuredSkillRoutes(`not-json`); err == nil {
		t.Fatal("expected invalid JSON error")
	}
}

func TestResumeLatestOptionsAndMessages(t *testing.T) {
	opts, _, err := parseArgs([]string{"-c", "-p", "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.resume != "latest" || opts.prompt != "hello" {
		t.Fatalf("opts = %+v", opts)
	}
	opts, _, err = parseArgs([]string{"--resume", "-p", "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.resume != "latest" || opts.prompt != "hello" {
		t.Fatalf("opts = %+v", opts)
	}

	root := t.TempDir()
	setTestConfigRoot(t, root)
	recorder, err := session.DefaultStore().NewRecorder(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{Type: "message", Role: "user", Content: "previous prompt"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	messages, err := loadResumeMessages(session.DefaultStore(), "latest", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[0].Content[0].Text != "previous prompt" || messages[1].Role != "assistant" || messages[1].Content[0].Text != "No response requested." {
		t.Fatalf("messages = %+v", messages)
	}
}

func TestLoadResumeMessagesRejectsUnsupportedTranscriptSchema(t *testing.T) {
	root := t.TempDir()
	store := session.Store{Root: root}
	sessionID := "11111111-1111-4111-8111-111111111111"
	projectDir := filepath.Join(root, "projects", session.ProjectSlug("/tmp/native"))
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(projectDir, sessionID+".jsonl")
	native := strings.Join([]string{
		`{"type":"user","uuid":"user-1","message":{"role":"user","content":"native prompt"}}`,
		`{"type":"assistant","uuid":"assistant-1","parentUuid":"user-1","message":{"role":"assistant","content":[{"type":"text","text":"native response"}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(native), 0600); err != nil {
		t.Fatal(err)
	}

	messages, err := loadResumeMessages(store, sessionID, "")
	if err == nil || !strings.Contains(err.Error(), "claude_code_native") {
		t.Fatalf("messages=%+v err=%v", messages, err)
	}
}

func TestSeedResumeAgentTasksFromAgentCreateOutputFile(t *testing.T) {
	ctx := context.Background()
	outputFile := filepath.Join(t.TempDir(), "agent.output")
	const marker = "AGENT_RESUME_OUTPUT_FILE_MARKER: restored"
	if err := os.WriteFile(outputFile, []byte(marker), 0600); err != nil {
		t.Fatal(err)
	}
	entries := []session.Entry{{
		Type:     "tool_call",
		ToolID:   "toolu_agent_create_resume",
		ToolName: "AgentCreate",
		Content:  `{"prompt":"reply marker","description":"resume missing store probe"}`,
	}, {
		Type:     "tool_result",
		ToolID:   "toolu_agent_create_resume",
		ToolName: "AgentCreate",
		Content:  fmt.Sprintf(`{"agent_name":"general-purpose","model":"model","output_file":%q,"session_id":"resume-subagent","status":"running","task_id":7}`, outputFile),
	}}
	store := memstore.New()
	seedResumeAgentTasksFromEntries(ctx, store, entries)
	tasks, err := store.ListAgentTasks(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("tasks = %+v", tasks)
	}
	task := tasks[0]
	if task.ID != 7 || task.Status != agenttasks.StatusCompleted || task.Description != "resume missing store probe" || task.SubagentSessionKey != "resume-subagent" {
		t.Fatalf("task = %+v", task)
	}
	if !strings.Contains(task.ResultJSON, marker) || !strings.Contains(task.ResultJSON, outputFile) {
		t.Fatalf("result_json = %s", task.ResultJSON)
	}
}

func TestSeedResumeAgentTasksPreservesFailedAgentCreateStatusWithOutputFile(t *testing.T) {
	ctx := context.Background()
	outputFile := filepath.Join(t.TempDir(), "agent.failed.output")
	const marker = "AGENT_RESUME_FAILED_OUTPUT_FILE_MARKER: provider stream failed"
	if err := os.WriteFile(outputFile, []byte(marker), 0600); err != nil {
		t.Fatal(err)
	}
	state := fmt.Sprintf(`{"status":"failed","result":{"content":%q,"status":"failed","output_file":%q}}`, marker, outputFile)
	if err := os.WriteFile(outputFile+".state.json", []byte(state), 0600); err != nil {
		t.Fatal(err)
	}
	entries := []session.Entry{{
		Type:     "tool_call",
		ToolID:   "toolu_agent_create_failed_resume",
		ToolName: "AgentCreate",
		Content:  `{"prompt":"fail deliberately","description":"failed resume probe"}`,
	}, {
		Type:     "tool_result",
		ToolID:   "toolu_agent_create_failed_resume",
		ToolName: "AgentCreate",
		Content:  fmt.Sprintf(`{"agent_name":"general-purpose","model":"model","output_file":%q,"session_id":"failed-subagent","status":"running","task_id":11}`, outputFile),
	}}
	store := memstore.New()
	seedResumeAgentTasksFromEntries(ctx, store, entries)
	tasks, err := store.ListAgentTasks(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("tasks = %+v", tasks)
	}
	task := tasks[0]
	if task.ID != 11 || task.Status != agenttasks.StatusFailed || task.Description != "failed resume probe" || task.SubagentSessionKey != "failed-subagent" {
		t.Fatalf("task = %+v", task)
	}
	if task.FinishedAt.IsZero() {
		t.Fatalf("finished_at is zero for failed task: %+v", task)
	}
	if !strings.Contains(task.ResultJSON, marker) || !strings.Contains(task.ResultJSON, outputFile) {
		t.Fatalf("result_json = %s", task.ResultJSON)
	}
	if !strings.Contains(task.ResultJSON, `"status":"failed"`) {
		t.Fatalf("result_json missing failed status = %s", task.ResultJSON)
	}
}

func TestSeedResumeAgentTasksMarksEmptyRunningOutputAsFailed(t *testing.T) {
	ctx := context.Background()
	outputFile := filepath.Join(t.TempDir(), "agent.empty.output")
	if err := os.WriteFile(outputFile, nil, 0600); err != nil {
		t.Fatal(err)
	}
	entries := []session.Entry{{
		Type:     "tool_call",
		ToolID:   "toolu_agent_create_empty_resume",
		ToolName: "AgentCreate",
		Content:  `{"prompt":"keep running","description":"empty running resume probe"}`,
	}, {
		Type:     "tool_result",
		ToolID:   "toolu_agent_create_empty_resume",
		ToolName: "AgentCreate",
		Content:  fmt.Sprintf(`{"agent_name":"general-purpose","model":"model","output_file":%q,"session_id":"empty-subagent","status":"running","task_id":12}`, outputFile),
	}}
	store := memstore.New()
	seedResumeAgentTasksFromEntries(ctx, store, entries)
	tasks, err := store.ListAgentTasks(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("tasks = %+v", tasks)
	}
	task := tasks[0]
	if task.ID != 12 || task.Status != agenttasks.StatusFailed || task.Description != "empty running resume probe" || task.SubagentSessionKey != "empty-subagent" {
		t.Fatalf("task = %+v", task)
	}
	if task.FinishedAt.IsZero() {
		t.Fatalf("finished_at is zero for interrupted task: %+v", task)
	}
	for _, want := range []string{"interrupted", "output_file is empty", outputFile, `"status":"failed"`} {
		if !strings.Contains(task.ResultJSON, want) {
			t.Fatalf("result_json missing %q = %s", want, task.ResultJSON)
		}
	}
}

func TestSeedResumeAgentTasksFromAgentStopOverridesCreate(t *testing.T) {
	ctx := context.Background()
	outputFile := filepath.Join(t.TempDir(), "agent.output")
	if err := os.WriteFile(outputFile, []byte("should not stay completed"), 0600); err != nil {
		t.Fatal(err)
	}
	entries := []session.Entry{{
		Type:     "tool_call",
		ToolID:   "toolu_agent_create_resume",
		ToolName: "AgentCreate",
		Content:  `{"prompt":"sleep","description":"cancel resume probe"}`,
	}, {
		Type:     "tool_result",
		ToolID:   "toolu_agent_create_resume",
		ToolName: "AgentCreate",
		Content:  fmt.Sprintf(`{"agent_name":"general-purpose","model":"model","output_file":%q,"session_id":"resume-subagent","status":"running","task_id":9}`, outputFile),
	}, {
		Type:     "tool_call",
		ToolID:   "toolu_agent_stop_resume",
		ToolName: "AgentStop",
		Content:  `{"task_id":9,"reason":"test cancel"}`,
	}, {
		Type:     "tool_result",
		ToolID:   "toolu_agent_stop_resume",
		ToolName: "AgentStop",
		Content:  `{"cancelled":true,"task_id":9}`,
	}}
	store := memstore.New()
	seedResumeAgentTasksFromEntries(ctx, store, entries)
	tasks, err := store.ListAgentTasks(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("tasks = %+v", tasks)
	}
	task := tasks[0]
	if task.ID != 9 || task.Status != agenttasks.StatusCancelled || task.Description != "cancel resume probe" {
		t.Fatalf("task = %+v", task)
	}
	if !strings.Contains(task.ResultJSON, "cancelled before completion") || !strings.Contains(task.ResultJSON, "test cancel") {
		t.Fatalf("result_json = %s", task.ResultJSON)
	}
	cancelled, err := store.IsAgentTaskCancelled(ctx, 9)
	if err != nil {
		t.Fatal(err)
	}
	if !cancelled {
		t.Fatalf("cancelled flag was not restored")
	}
}

func TestAcknowledgedAgentTasksFromResumeEntries(t *testing.T) {
	entries := []session.Entry{{
		Type:     "tool_result",
		ToolName: "AgentGet",
		Content:  `{"task":{"id":7,"status":"completed"},"result":{"content":"done","status":"completed"}}`,
	}, {
		Type:     "tool_result",
		ToolName: "AgentGet",
		Content:  `{"task":{"id":8,"status":"running"},"result":{"content":"still running","status":"running"}}`,
	}, {
		Type:     "tool_result",
		ToolName: "AgentGet",
		Content:  `{"task":{"id":9,"status":"failed"},"result":{"content":"failed","status":"failed"}}`,
	}, {
		Type:     "tool_result",
		ToolName: "AgentGet",
		Content:  `{"task":{"id":7,"status":"completed"},"result":{"content":"duplicate","status":"completed"}}`,
	}}
	got := acknowledgedAgentTasksFromResumeEntries(entries)
	if !reflect.DeepEqual(got, []uint64{7, 9}) {
		t.Fatalf("acknowledged ids = %+v", got)
	}
}

func TestActiveSkillMessagesFromResumeEntries(t *testing.T) {
	skillContext := "<system-reminder>\nSkill resume-skill instructions are now active. Follow them.\n\nRESUME_SKILL_ACTIVE\n</system-reminder>"
	entries := []session.Entry{{
		Type:    "message",
		Role:    "user",
		Content: skillContext,
	}, {
		Type:    "compact_summary",
		Content: "summary after skill load",
	}, {
		Type:    "message",
		Role:    "user",
		Content: "ordinary user message mentioning Skill but not a system reminder",
	}, {
		Type:    "message",
		Role:    "user",
		Content: skillContext,
	}}

	got := activeSkillMessagesFromResumeEntries(entries)
	if len(got) != 1 {
		t.Fatalf("active skill messages = %+v", got)
	}
	if got[0].Role != "user" || len(got[0].Content) != 1 || got[0].Content[0].Text != skillContext {
		t.Fatalf("active skill message = %+v", got[0])
	}
}

func TestTUIResumeInitialMessagesReportsRecovery(t *testing.T) {
	root := t.TempDir()
	setTestConfigRoot(t, root)
	t.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", filepath.Join(root, "projects"))
	store := session.DefaultStore()
	recorder, err := store.NewRecorder(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{Type: "message", Role: "user", Content: "inspect"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{Type: "tool_result", ToolID: "toolu_orphan", ToolName: "Read", Content: "orphan"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{Type: "tool_call", ToolID: "toolu_missing", ToolName: "Read", Content: `{"file_path":"README.md"}`}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	messages, err := tuiResumeInitialMessages(store, options{resume: "latest"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Role != "status" || !strings.Contains(messages[0].Content, "Recovered an interrupted tool turn") || !strings.Contains(messages[0].Content, "synthetic tool_result") || !strings.Contains(messages[0].Content, "orphaned tool_result") {
		t.Fatalf("messages = %+v", messages)
	}
}

func TestTUIResumeInitialMessagesIncludesLatestRecap(t *testing.T) {
	root := t.TempDir()
	setTestConfigRoot(t, root)
	t.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", filepath.Join(root, "projects"))
	store := session.DefaultStore()
	recorder, err := store.NewRecorder(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{Type: "message", Role: "user", Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{Type: "recap_summary", Role: "system", Content: "本次会话目标：验证 resume recap"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	messages, err := tuiResumeInitialMessages(store, options{resume: "latest"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[1].Role != "recap" || !strings.Contains(messages[1].Content, "验证 resume recap") {
		t.Fatalf("messages = %+v", messages)
	}
}

func TestTUIResumeInitialMessagesIncludesRecentHistory(t *testing.T) {
	root := t.TempDir()
	setTestConfigRoot(t, root)
	t.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", filepath.Join(root, "projects"))
	store := session.DefaultStore()
	recorder, err := store.NewRecorder(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 8; i++ {
		if err := recorder.Append(session.Entry{Type: "message", Role: "user", Content: fmt.Sprintf("user-%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := recorder.Append(session.Entry{Type: "checkpoint", Name: "auto-user-8", Content: "user-8"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	messages, err := tuiResumeInitialMessages(store, options{resume: "latest"}, defaultTUIResumeHistoryLimit)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1+defaultTUIResumeHistoryLimit {
		t.Fatalf("messages = %+v", messages)
	}
	if strings.Contains(messages[1].Content, "user-1") || !strings.Contains(messages[1].Content, "user-3") || !strings.Contains(messages[len(messages)-1].Content, "user-8") {
		t.Fatalf("history messages = %+v", messages)
	}
	history, err := tuiResumeHistoryMessages(store, "latest", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0].Content != "user-7" || history[1].Content != "user-8" {
		t.Fatalf("history = %+v", history)
	}
}

func TestTUIResumeHistoryIncludesThinkingWithOriginalTurnAndPhase(t *testing.T) {
	base := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)
	entries := []session.Entry{
		{Type: "message", Role: "user", Content: "q1", Timestamp: base},
		{Type: "thinking", Role: "assistant", Content: "reasoning one", Timestamp: base.Add(time.Second)},
		{Type: "message", Role: "assistant", Content: "a1", Timestamp: base.Add(2 * time.Second)},
		{Type: "message", Role: "user", Content: "q2", Timestamp: base.Add(3 * time.Second)},
		{Type: "thinking", Role: "assistant", Content: "reasoning two phase one", Timestamp: base.Add(4 * time.Second)},
		{Type: "tool_call", ToolName: "Read", Timestamp: base.Add(5 * time.Second)},
		{Type: "thinking", Role: "assistant", Content: "reasoning two phase two", Timestamp: base.Add(6 * time.Second)},
		{Type: "message", Role: "assistant", Content: "a2", Timestamp: base.Add(7 * time.Second)},
	}

	history := recentTranscriptUIMessages(entries, 2)
	var thinking []tui.InitialMessage
	for _, item := range history {
		if item.Role == "thinking" {
			thinking = append(thinking, item)
		}
	}
	if len(thinking) != 2 {
		t.Fatalf("thinking history = %+v", history)
	}
	if thinking[0].Turn != 2 || thinking[0].Phase != 1 || thinking[1].Turn != 2 || thinking[1].Phase != 2 {
		t.Fatalf("thinking anchors = %+v", thinking)
	}
	if thinking[0].CreatedAt != base.Add(4*time.Second) || thinking[1].CreatedAt != base.Add(6*time.Second) {
		t.Fatalf("thinking timestamps = %+v", thinking)
	}
}

func TestTranscriptThinkingDetailsIncludesAllCompletedTurns(t *testing.T) {
	entries := []session.Entry{
		{Type: "message", Role: "user", Content: "q1"},
		{Type: "thinking", Role: "assistant", Content: "old reasoning"},
		{Type: "message", Role: "assistant", Content: "a1"},
		{Type: "message", Role: "user", Content: "q2"},
		{Type: "thinking", Role: "assistant", Content: "incomplete reasoning"},
	}
	details := transcriptThinkingDetails(entries)
	if len(details) != 1 || details[0].Turn != 1 || details[0].Content != "old reasoning" {
		t.Fatalf("thinking details = %+v", details)
	}
}

func TestRecapSlashShowDisplaysLatestRecap(t *testing.T) {
	root := t.TempDir()
	setTestConfigRoot(t, root)
	t.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", filepath.Join(root, "projects"))
	store := session.DefaultStore()
	recorder, err := store.NewRecorder(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{Type: "recap_summary", Role: "system", Content: "本次会话目标：展示 recap"}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := recapSlashCommand(context.Background(), []string{"show"}, options{cwd: t.TempDir()}, recorder, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "※ recap:") || !strings.Contains(out.String(), "展示 recap") {
		t.Fatalf("output = %s", out.String())
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
}

type fakeRecapStreamer struct {
	text string
}

func (f fakeRecapStreamer) StreamMessages(_ context.Context, _ anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	text := f.text
	if text == "" {
		text = "本次会话目标：秘密正文\n已完成：测试\n下一步：提交"
	}
	if cb.OnText != nil {
		_ = cb.OnText(text)
	}
	return &anthropic.StreamResult{}, nil
}

func TestGenerateRecapWithTelemetryEmitsMetadataOnly(t *testing.T) {
	var events []telemetry.Event
	emitter := telemetry.NewEmitter(telemetry.SinkFunc(func(_ context.Context, event telemetry.Event) error {
		events = append(events, event)
		return nil
	}))
	ctx := telemetry.WithEmitter(context.Background(), emitter)
	_, err := generateRecapWithTelemetry(ctx, fakeRecapStreamer{}, []session.Entry{
		{Type: "message", Role: "user", Content: "hello"},
	}, recap.Config{MaxTokens: 64}, recap.ModeManual, options{model: "recap-model"}, filepath.Join(t.TempDir(), "session-1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Name != "session.recap.started" || events[1].Name != "session.recap.finished" {
		t.Fatalf("events = %+v", events)
	}
	raw, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "秘密正文") {
		t.Fatalf("telemetry leaked recap content: %s", string(raw))
	}
}

func TestTUIAwayRecapDelayFromConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	project := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, "config"))
	mustWrite(t, filepath.Join(project, "config", "settings.json"), `{"recap":{"awayDelaySeconds":10}}`)
	delay := tuiAwayRecapDelay(options{cwd: project, model: "recap-model"})
	if delay != 10*time.Second {
		t.Fatalf("delay = %s", delay)
	}
}

func TestTUIRecapConfigDefaultsToAway(t *testing.T) {
	cfg := tuiRecapConfig(config.Settings{}, "recap-model")
	if !cfg.Enabled || cfg.Mode != recap.ModeAway || cfg.AwayDelaySeconds != recap.DefaultAwayDelaySeconds {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestTUIRecapConfigRespectsCustomDelayOnly(t *testing.T) {
	delay := 7
	cfg := tuiRecapConfig(config.Settings{Recap: &config.RecapSettings{AwayDelaySeconds: &delay}}, "recap-model")
	if !cfg.Enabled || cfg.Mode != recap.ModeAway || cfg.AwayDelaySeconds != delay {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestTUIRecapConfigRespectsExplicitDisable(t *testing.T) {
	enabled := false
	cfg := tuiRecapConfig(config.Settings{Recap: &config.RecapSettings{Enabled: &enabled}}, "recap-model")
	if cfg.AwayEnabled("recap-model") {
		t.Fatalf("away recap should be disabled: %+v", cfg)
	}
}

func TestTUIRecapConfigRespectsExplicitModes(t *testing.T) {
	for _, mode := range []string{recap.ModeManual, recap.ModePostTurn, recap.ModeAway} {
		cfg := tuiRecapConfig(config.Settings{Recap: &config.RecapSettings{Mode: mode}}, "recap-model")
		if cfg.Mode != mode {
			t.Fatalf("mode %q became %+v", mode, cfg)
		}
		if mode == recap.ModeAway && !cfg.AwayEnabled("recap-model") {
			t.Fatalf("mode %q should enable away recap: %+v", mode, cfg)
		}
		if mode != recap.ModeAway && cfg.AwayEnabled("recap-model") {
			t.Fatalf("mode %q should not enable away recap: %+v", mode, cfg)
		}
	}
}

func TestSessionRecapSmokeFlow(t *testing.T) {
	root := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	setTestConfigRoot(t, root)
	t.Setenv("CLAUDE_CODE_MODEL", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("CLAUDE_CODE_AUTH_TOKEN", "")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
	t.Setenv("NIUMA_MYSQL_DSN", "")
	t.Setenv("MYSQL_DSN", "")
	t.Chdir(project)

	const recapProbe = "RECAPPROBE_SHOULD_NOT_LEAK"
	var (
		mu     sync.Mutex
		bodies []string
		calls  int
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		calls++
		call := calls
		bodies = append(bodies, string(body))
		mu.Unlock()
		text := "assistant response"
		if call == 2 {
			text = "本次会话目标：" + recapProbe + "\n已完成：生成 recap。\n下一步：恢复会话验证上下文。"
		}
		writeAnthropicTextStream(t, w, text)
	}))
	defer server.Close()
	configureTestProvider(t, server.URL, "smoke-key")

	sessionID := "88888888-8888-4888-8888-888888888888"
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"--cwd", project, "--session-id", sessionID, "-p", "第一轮：生成 session recap smoke 数据"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	recapOut := bytes.Buffer{}
	handled, exit, err := handleInteractiveSlash(context.Background(), options{cwd: project, model: "claude-sonnet-4-6", resume: sessionID}, "/recap", &recapOut, &bytes.Buffer{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !handled || exit || !strings.Contains(recapOut.String(), "※ recap:") || !strings.Contains(recapOut.String(), recapProbe) {
		t.Fatalf("recap output handled=%v exit=%v output=%s", handled, exit, recapOut.String())
	}

	store := session.DefaultStore()
	summary, ok, err := store.Find(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("session not found: %s", sessionID)
	}
	entries, err := session.Load(summary.Path)
	if err != nil {
		t.Fatal(err)
	}
	foundRecap := false
	for _, entry := range entries {
		if entry.Type == "recap_summary" && strings.Contains(entry.Content, recapProbe) {
			foundRecap = true
		}
	}
	if !foundRecap {
		t.Fatalf("transcript missing recap_summary with probe: %+v", entries)
	}

	initial, err := tuiResumeInitialMessages(store, options{resume: sessionID}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(initial) != 2 || initial[1].Role != "recap" || !strings.Contains(initial[1].Content, recapProbe) {
		t.Fatalf("resume initial messages missing recap: %+v", initial)
	}

	out.Reset()
	if err := Run(context.Background(), []string{"--cwd", project, "--resume", sessionID, "--no-session-persistence", "-p", "第二轮：继续验证"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	captured := append([]string(nil), bodies...)
	mu.Unlock()
	if len(captured) < 3 {
		t.Fatalf("captured requests = %d: %+v", len(captured), captured)
	}
	resumeRequest := captured[len(captured)-1]
	if !strings.Contains(resumeRequest, "第一轮：生成 session recap smoke 数据") || !strings.Contains(resumeRequest, "第二轮：继续验证") {
		t.Fatalf("resume request missing conversation context:\n%s", resumeRequest)
	}
	if strings.Contains(resumeRequest, recapProbe) {
		t.Fatalf("recap leaked into resumed model request:\n%s", resumeRequest)
	}
}

func writeAnthropicTextStream(t *testing.T, w http.ResponseWriter, text string) {
	t.Helper()
	w.Header().Set("content-type", "text/event-stream")
	payload := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_smoke","type":"message","role":"assistant","model":"test","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":5,"output_tokens":0}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":` + strconv.Quote(text) + `}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":2}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")
	_, _ = w.Write([]byte(payload))
}

func TestSystemPromptOptions(t *testing.T) {
	opts, _, err := parseArgs([]string{"--system-prompt", "base", "--append-system-prompt", "extra", "--append-system-prompt", "more", "--max-tokens", "123", "-p", "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.systemPrompt != "base" || opts.appendSystem != "extra\n\nmore" || opts.maxTokens != 123 {
		t.Fatalf("opts = %+v", opts)
	}
}

func TestAgentOptionParsesMainThreadAgent(t *testing.T) {
	opts, _, err := parseArgs([]string{"--agent", "reviewer", "-p", "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.agentName != "reviewer" {
		t.Fatalf("agentName = %q", opts.agentName)
	}
}

func TestApplyMainThreadAgentOptions(t *testing.T) {
	settings := config.Settings{}
	opts := options{agentName: "reviewer"}
	agent := agents.Agent{
		Name:            "reviewer",
		Tools:           []string{"Read"},
		DisallowedTools: []string{"Bash"},
		Model:           "agent-model",
		MaxTurns:        7,
		PermissionMode:  "deny",
	}
	model, maxTurns, err := applyMainThreadAgentOptions(&settings, opts, agent, "base-model", 3)
	if err != nil {
		t.Fatal(err)
	}
	if model != "agent-model" || maxTurns != 7 || strings.Join(settings.Permissions.Allow, ",") != "Read" || strings.Join(settings.Permissions.Deny, ",") != "Bash" || settings.Permissions.DefaultMode != "deny" {
		t.Fatalf("model=%q maxTurns=%d permissions=%+v", model, maxTurns, settings.Permissions)
	}

	settings = config.Settings{}
	opts = options{agentName: "reviewer", modelExplicit: true, permissionMode: "ask"}
	model, _, err = applyMainThreadAgentOptions(&settings, opts, agent, "explicit-model", 3)
	if err != nil {
		t.Fatal(err)
	}
	if model != "explicit-model" || settings.Permissions.DefaultMode != "" {
		t.Fatalf("explicit overrides model=%q permissions=%+v", model, settings.Permissions)
	}
}

func TestServerDefaultPromptModeFromEnv(t *testing.T) {
	t.Setenv("NIUMA_SERVER_DEFAULT_PROMPT_MODE", "chat")
	if got := serverDefaultPromptMode(); got != promptmode.Chat {
		t.Fatalf("prompt mode = %q", got)
	}
	t.Setenv("NIUMA_SERVER_DEFAULT_PROMPT_MODE", "invalid")
	if got := serverDefaultPromptMode(); got != promptmode.Code {
		t.Fatalf("invalid prompt mode fallback = %q", got)
	}
}

func TestParseArgsDefaultMaxTurns(t *testing.T) {
	opts, _, err := parseArgs([]string{"-p", "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.maxTurns != defaults.MaxTurns {
		t.Fatalf("maxTurns = %d, want %d", opts.maxTurns, defaults.MaxTurns)
	}
}

func TestRuntimeSettingsMCPAndToolsOptions(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "system.txt"), []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "append.txt"), []byte("extra\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "settings.json"), []byte(`{
		"permissions": {"allow": ["Read"]},
		"additionalDirectories": ["/tmp/settings-extra"],
		"mcpServers": {"settings": {"type": "stdio", "command": "settings-cmd"}}
	}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "mcp.json"), []byte(`{
		"mcpServers": {"flag": {"type": "stdio", "command": "flag-cmd"}}
	}`), 0600); err != nil {
		t.Fatal(err)
	}
	opts, _, err := parseArgs([]string{
		"--cwd", project,
		"--system-prompt-file", "system.txt",
		"--append-system-prompt-file", "append.txt",
		"--settings", "settings.json",
		"--mcp-config", "mcp.json", `{"inline":{"type":"http","url":"https://example.test/mcp"}}`,
		"--tools", "Read,Bash", "Edit",
		"-p", "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if opts.systemPrompt != "base" || opts.appendSystem != "extra" {
		t.Fatalf("prompts = %q / %q", opts.systemPrompt, opts.appendSystem)
	}
	if strings.Join(opts.enabledTools, ",") != "Read,Bash,Edit" {
		t.Fatalf("enabled tools = %+v", opts.enabledTools)
	}
	settings := config.Settings{MCPServers: map[string]config.MCPServerConfig{
		"base": {Type: "stdio", Command: "base-cmd"},
	}}
	if err := applyRuntimeOptions(&settings, opts); err != nil {
		t.Fatal(err)
	}
	if strings.Join(settings.Permissions.Allow, ",") != "Read" {
		t.Fatalf("permissions = %+v", settings.Permissions)
	}
	if _, ok := settings.MCPServers["base"]; !ok {
		t.Fatalf("base MCP server was not preserved: %+v", settings.MCPServers)
	}
	if settings.MCPServers["settings"].Command != "settings-cmd" ||
		settings.MCPServers["flag"].Command != "flag-cmd" ||
		settings.MCPServers["inline"].URL != "https://example.test/mcp" {
		t.Fatalf("mcp servers = %+v", settings.MCPServers)
	}
	filtered := filterRuntimeTools([]tools.Tool{namedTool("Read"), namedTool("Write"), namedTool("Edit")}, opts)
	if len(filtered) != 2 || filtered[0].Name() != "Read" || filtered[1].Name() != "Edit" {
		t.Fatalf("filtered = %+v", filtered)
	}

	opts.strictMCPConfig = true
	settings = config.Settings{MCPServers: map[string]config.MCPServerConfig{
		"base": {Type: "stdio", Command: "base-cmd"},
	}}
	if err := applyRuntimeOptions(&settings, opts); err != nil {
		t.Fatal(err)
	}
	if _, ok := settings.MCPServers["base"]; ok || settings.MCPServers["flag"].Command != "flag-cmd" {
		t.Fatalf("strict mcp servers = %+v", settings.MCPServers)
	}
}

func TestExplicitAgentToolRegistersClaudeCompatibleSurface(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	agentDir := filepath.Join(project, ".claude", "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "reviewer.md"), []byte("---\nname: reviewer\ndescription: Independent code review\ntools: [Read, Grep]\n---\nReview carefully."), 0644); err != nil {
		t.Fatal(err)
	}

	session, cleanup, err := newQuerySession(context.Background(), options{
		cwd:            project,
		model:          "test-model",
		maxTurns:       1,
		toolsSpecified: true,
		enabledTools:   []string{"Agent", "Grep", "Read"},
		noPersistence:  true,
		agentTaskStore: memstore.New(),
		permissionMode: "allow",
		promptMode:     promptmode.Code.String(),
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	var names []string
	for _, def := range session.ToolDefinitions() {
		names = append(names, def.Name)
	}
	if len(names) != 3 || !containsString(names, "Agent") || !containsString(names, "Grep") || !containsString(names, "Read") {
		t.Fatalf("tool names = %v", names)
	}
	var agentDescription string
	for _, def := range session.ToolDefinitions() {
		if def.Name == "Agent" {
			agentDescription = def.Description
			break
		}
	}
	if !strings.Contains(agentDescription, "- reviewer: Independent code review (Tools: Read, Grep)") {
		t.Fatalf("Agent description did not list workspace agent:\n%s", agentDescription)
	}
	for _, forbidden := range []string{"Task", "AgentCreate", "AgentGet", "AgentMessage", "SendMessage", "LS"} {
		if containsString(names, forbidden) {
			t.Fatalf("compat surface unexpectedly exposed %s: %v", forbidden, names)
		}
	}
}

func TestSendMessageRequiresExperimentalAgentTeams(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("NIUMA_EXPERIMENTAL_AGENT_TEAMS", "")
	t.Setenv("CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS", "")

	session, cleanup, err := newQuerySession(context.Background(), options{
		cwd:            project,
		model:          "test-model",
		maxTurns:       1,
		toolsSpecified: true,
		enabledTools:   []string{"Agent", "Grep", "Read", "SendMessage"},
		noPersistence:  true,
		agentTaskStore: memstore.New(),
		permissionMode: "allow",
		promptMode:     promptmode.Code.String(),
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	var names []string
	for _, def := range session.ToolDefinitions() {
		names = append(names, def.Name)
	}
	if containsString(names, "SendMessage") {
		t.Fatalf("SendMessage exposed without opt-in: %v", names)
	}
}

func TestSendMessageRegistersWithExperimentalAgentTeams(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("NIUMA_EXPERIMENTAL_AGENT_TEAMS", "1")

	session, cleanup, err := newQuerySession(context.Background(), options{
		cwd:            project,
		model:          "test-model",
		maxTurns:       1,
		toolsSpecified: true,
		enabledTools:   []string{"Agent", "Grep", "Read", "SendMessage"},
		noPersistence:  true,
		agentTaskStore: memstore.New(),
		permissionMode: "allow",
		promptMode:     promptmode.Code.String(),
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	var names []string
	for _, def := range session.ToolDefinitions() {
		names = append(names, def.Name)
	}
	for _, want := range []string{"Agent", "Grep", "Read", "SendMessage"} {
		if !containsString(names, want) {
			t.Fatalf("tool names = %v, missing %s", names, want)
		}
	}
	if containsString(names, "AgentMessage") {
		t.Fatalf("explicit SendMessage compat surface should not also expose AgentMessage: %v", names)
	}
}

func TestRuntimeSettingsDisableFallbackBeforeQueryClientBuild(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "primary unavailable", http.StatusServiceUnavailable)
	}))
	defer primary.Close()
	fallbackAttempts := 0
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackAttempts++
		writeAnthropicTextStream(t, w, "fallback-ok")
	}))
	defer fallback.Close()
	t.Setenv("ANTHROPIC_BASE_URL", primary.URL)
	t.Setenv("ANTHROPIC_API_KEY", "primary-key")
	mustWrite(t, filepath.Join(project, "config", "config.yaml"), `
fallback:
  enabled: true
  providers:
    - name: fallback
      type: anthropic
      baseURL: `+fallback.URL+`
      apiKey: fallback-key
`)

	activateSettingsFixture(t, filepath.Join(project, "config", "config.yaml"))
	session, cleanup, err := newQuerySession(context.Background(), options{
		cwd:            project,
		model:          "claude-sonnet-4-6",
		maxTurns:       1,
		noPersistence:  true,
		disableTools:   true,
		settingsInputs: []string{`{"fallback":{"enabled":false}}`},
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := session.RunText(context.Background(), "hi", io.Discard); err == nil {
		t.Fatal("expected primary provider error")
	}
	if fallbackAttempts != 0 {
		t.Fatalf("fallback attempts = %d, want 0", fallbackAttempts)
	}
}

func TestSelectedProviderBecomesPrimaryBeforeQueryClientBuild(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	primaryAttempts := 0
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryAttempts++
		http.Error(w, "primary must not be called", http.StatusInternalServerError)
	}))
	defer primary.Close()
	selectedAttempts := 0
	selected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		selectedAttempts++
		var payload struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Model != "selected-model" {
			t.Fatalf("model = %q", payload.Model)
		}
		writeAnthropicTextStream(t, w, "selected-ok")
	}))
	defer selected.Close()
	t.Setenv("ANTHROPIC_BASE_URL", primary.URL)
	t.Setenv("ANTHROPIC_API_KEY", "primary-key")
	mustWrite(t, filepath.Join(project, "config", "config.yaml"), `
fallback:
  enabled: true
  providers:
    - name: selected
      type: anthropic
      baseURL: `+selected.URL+`
      apiKey: selected-key
      model: selected-model
`)

	activateSettingsFixture(t, filepath.Join(project, "config", "config.yaml"))
	session, cleanup, err := newQuerySession(context.Background(), options{
		cwd:           project,
		providerName:  "selected",
		maxTurns:      1,
		noPersistence: true,
		disableTools:  true,
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := session.RunText(context.Background(), "hi", io.Discard); err != nil {
		t.Fatal(err)
	}
	if primaryAttempts != 0 || selectedAttempts != 1 {
		t.Fatalf("primary attempts = %d, selected attempts = %d", primaryAttempts, selectedAttempts)
	}
}

func TestManualRecapUsesSelectedProviderAndPurpose(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	primaryAttempts := 0
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryAttempts++
		http.Error(w, "primary must not be called", http.StatusInternalServerError)
	}))
	defer primary.Close()
	selectedAttempts := 0
	selected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		selectedAttempts++
		writeAnthropicTextStream(t, w, "recap-ok")
	}))
	defer selected.Close()
	t.Setenv("ANTHROPIC_BASE_URL", primary.URL)
	t.Setenv("ANTHROPIC_API_KEY", "primary-key")
	mustWrite(t, filepath.Join(project, "config", "config.yaml"), `
fallback:
  providers:
    - name: selected
      type: anthropic
      baseURL: `+selected.URL+`
      apiKey: selected-key
      model: selected-model
`)
	activateSettingsFixture(t, filepath.Join(project, "config", "config.yaml"))
	path := filepath.Join(project, "session-id.jsonl")
	mustWrite(t, path, "")
	sink := telemetry.NewMemorySink()
	ctx := telemetry.WithEmitter(context.Background(), telemetry.NewEmitter(sink))
	_, err := generateRecapForSession(ctx, options{cwd: project, providerName: "selected", model: "selected-model"}, path, []session.Entry{{Type: "message", Role: "user", Content: "hello"}}, recap.ModeManual)
	if err != nil {
		t.Fatal(err)
	}
	if primaryAttempts != 0 || selectedAttempts != 1 {
		t.Fatalf("primary=%d selected=%d", primaryAttempts, selectedAttempts)
	}
	for _, event := range sink.Events() {
		if strings.HasPrefix(event.Name, "model.phase.") && (event.CLISessionID != "session-id" || event.RequestPurpose != "recap.manual") {
			t.Fatalf("event = %+v", event)
		}
	}
}

func TestBackgroundPersistsSelectedProviderModel(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_BG_QUEUE_ONLY", "1")
	mustWrite(t, filepath.Join(home, ".niuma", "settings.json"), `{
  "model": "default-model",
  "fallback": {
    "enabled": true,
    "providers": [{
      "name": "selected",
      "type": "custom",
      "baseURL": "https://selected.example.test/v1",
      "apiKey": "selected-key",
      "model": "selected-model"
    }]
  }
}`)

	var out bytes.Buffer
	if err := Run(context.Background(), []string{"--cwd", project, "--provider", "selected", "--background", "-p", "hello"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	jobs, err := (background.Store{Root: filepath.Join(home, ".niuma")}).List()
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Provider != "selected" || jobs[0].Model != "selected-model" {
		t.Fatalf("jobs = %+v", jobs)
	}
}

func TestTUIWelcomeInfoReflectsRuntimeConfig(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_MODEL", "")
	setTestConfigRoot(t, filepath.Join(home, ".claude"))
	t.Setenv("ANTHROPIC_BASE_URL", "https://ai-gateway.example.test/v1")
	configDir := filepath.Join(project, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(`
provider: custom
model: gpt-test
context_length: 251000
permissions:
  defaultMode: ask
tui:
  showThinking: false
  thinkingMode: summary
sandbox:
  enabled: true
  seccomp:
    enabled: true
  network:
    denyDomains:
      - example.com
  unixSockets:
    deny:
      - /var/run/docker.sock
mcpServers:
  local:
    type: stdio
    command: mcp-local
`), 0600); err != nil {
		t.Fatal(err)
	}
	activateSettingsFixture(t, filepath.Join(configDir, "config.yaml"))
	opts, _, err := parseArgs([]string{"--cwd", project, "--dangerously-skip-permissions"})
	if err != nil {
		t.Fatal(err)
	}
	info := tuiWelcomeInfo(opts)
	if info.Model != "gpt-test" || info.Provider != "custom/openai-compatible" || info.PermissionMode != "bypass" || info.ContextLength != 251000 {
		t.Fatalf("info = %+v", info)
	}
	if info.ShowThinking == nil || *info.ShowThinking {
		t.Fatalf("showThinking = %#v", info.ShowThinking)
	}
	if info.ThinkingMode != config.TUIThinkingModeSummary {
		t.Fatalf("thinkingMode = %q", info.ThinkingMode)
	}
	// This config asks for two things macOS does not deliver, so the label must
	// name both rather than reading as enforced: denyDomains without
	// network.disabled only filters the built-in HTTP tools (AUDIT-P1-35), and
	// seccomp is Linux-only (AUDIT-P1-37). The expectation here used to be
	// "on/seccomp/network/sockets" — both halves of that were the lie.
	wantLabel := "on/sockets/degraded:seccomp,network.domains"
	wantGaps := []string{"is ignored on macos", "not domain-filtered"}
	if runtime.GOOS != "darwin" {
		t.Skipf("label expectation is darwin-specific; running on %s", runtime.GOOS)
	}
	if info.Sandbox != wantLabel || info.MCPServers != 1 || !strings.Contains(info.ToolSummary, "+mcp") {
		t.Fatalf("info = %+v", info)
	}
	if len(info.SandboxWarnings) != len(wantGaps) {
		t.Fatalf("SandboxWarnings = %+v, want %d gaps", info.SandboxWarnings, len(wantGaps))
	}
	for i, want := range wantGaps {
		if !strings.Contains(info.SandboxWarnings[i], want) {
			t.Fatalf("SandboxWarnings[%d] = %q, want it to mention %q", i, info.SandboxWarnings[i], want)
		}
	}
}

func TestTUIRuntimeContextUsesExplicitModelContextWindow(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, "config"))
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_MODEL", "")
	setTestConfigRoot(t, filepath.Join(project, "config"))
	mustWrite(t, filepath.Join(project, "config", "settings.json"), `{"model":"glm-5.1","autoCompact":{"modelContext":{"glm":200000}}}`)

	opts, _, err := parseArgs([]string{"--cwd", project})
	if err != nil {
		t.Fatal(err)
	}
	ctx := tuiRuntimeContext(opts, 3)
	if ctx.ContextWindow != 200000 {
		t.Fatalf("ContextWindow = %d, want 200000", ctx.ContextWindow)
	}
	if ctx.ToolCount != 3 {
		t.Fatalf("ToolCount = %d, want 3", ctx.ToolCount)
	}
}

func TestTUIRuntimeContextOmitsContextWindowWithoutModelContext(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_MODEL", "")
	setTestConfigRoot(t, filepath.Join(home, ".claude"))
	mustWrite(t, filepath.Join(project, "config", "config.yaml"), `
model: glm-5.1
context_length: 200000
`)

	opts, _, err := parseArgs([]string{"--cwd", project})
	if err != nil {
		t.Fatal(err)
	}
	ctx := tuiRuntimeContext(opts, 0)
	if ctx.ContextWindow != 0 {
		t.Fatalf("ContextWindow = %d, want 0", ctx.ContextWindow)
	}
}

func TestTUIQueryResultMapsLastInputTokens(t *testing.T) {
	result := tuiQueryResult(query.Result{
		Response: "answer",
		Model:    "glm-5.1",
		Usage: query.Usage{
			InputTokens:     739507,
			OutputTokens:    3202,
			LastInputTokens: 150000,
		},
	}, options{}, 0)
	if result.Usage.InputTokens != 739507 {
		t.Fatalf("InputTokens = %d, want 739507", result.Usage.InputTokens)
	}
	if result.Usage.LastInputTokens != 150000 {
		t.Fatalf("LastInputTokens = %d, want 150000", result.Usage.LastInputTokens)
	}
}

func TestTUIWelcomeInfoIncludesActiveGoal(t *testing.T) {
	root := t.TempDir()
	project := t.TempDir()
	setTestConfigRoot(t, root)
	created, err := goalpkg.DefaultStore().Create(context.Background(), goalpkg.CreateInput{
		Objective:   "show tui goal",
		CWD:         project,
		TurnBudget:  5,
		TokenBudget: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	created.TurnsUsed = 2
	created.InputTokens = 30
	created.OutputTokens = 12
	created.LastNextAction = "inspect evidence before final answer"
	if err := goalpkg.DefaultStore().Update(context.Background(), created); err != nil {
		t.Fatal(err)
	}
	plan := goalpkg.GoalPlan{
		GoalID:        created.ID,
		Version:       1,
		CurrentStepID: "step_verify",
		Steps: []goalpkg.GoalStep{{
			ID:     "step_verify",
			Title:  "Verify TUI goal context",
			Status: goalpkg.StepStatusActive,
		}},
		AcceptanceCriteria: []goalpkg.GoalCriterion{{
			ID:          "crit_tests",
			Description: "Tests pass",
			Required:    true,
			Status:      goalpkg.CriterionStatusPassed,
		}, {
			ID:          "crit_docs",
			Description: "Docs updated",
			Required:    true,
			Status:      goalpkg.CriterionStatusPending,
		}},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := goalpkg.DefaultStore().SavePlan(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if err := goalpkg.DefaultStore().AppendEvidence(context.Background(), goalpkg.GoalEvidence{
		ID:        "ev_tests",
		GoalID:    created.ID,
		Type:      goalpkg.EvidenceTypeTest,
		Summary:   "go test ./internal/cli passed",
		Passed:    true,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	partial := goalpkg.EvidenceFromToolTraces(created.ID, []goalpkg.ToolEvidenceTrace{{
		ID:   "toolu_agent_get",
		Name: "AgentGet",
		Output: `{
			"task":{"id":42,"status":"failed"},
			"result":{
				"status":"failed",
				"capability_loop":{
					"evidence":["failed sub-agent found partial root cause"],
					"risks":["retry may repeat the same provider error"],
					"next_action":"retry with narrower scope"
				}
			}
		}`,
	}}, time.Now().UTC())
	if len(partial) != 1 {
		t.Fatalf("partial evidence = %+v", partial)
	}
	if err := goalpkg.DefaultStore().AppendEvidence(context.Background(), partial[0]); err != nil {
		t.Fatal(err)
	}
	info := tuiWelcomeInfo(options{cwd: project})
	if !strings.Contains(info.Goal, "active") || !strings.Contains(info.Goal, "turns 2/5") || !strings.Contains(info.Goal, "tokens 42/1000") {
		t.Fatalf("goal label = %q", info.Goal)
	}
	if info.GoalStep != "Verify TUI goal context" || info.GoalCriteria != "1/2 passed req 1/2" || !strings.Contains(info.GoalEvidence, "source agent_get") || !strings.Contains(info.GoalEvidence, "AgentGet failed partial evidence") || !strings.Contains(info.GoalEvidence, "failed sub-agent found partial root cause") || info.GoalNextAction != "inspect evidence before final answer" {
		t.Fatalf("goal context = %+v", info)
	}
}

func TestReloadInteractiveOptionsUpdatesConfigModelUnlessExplicit(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_MODEL", "")
	setTestConfigRoot(t, filepath.Join(home, ".claude"))
	configDir := filepath.Join(project, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeModel := func(model string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte("provider: custom\nmodel: "+model+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		activateSettingsFixture(t, filepath.Join(configDir, "config.yaml"))
	}

	writeModel("gpt-5.5")
	opts, _, err := parseArgs([]string{"--cwd", project})
	if err != nil {
		t.Fatal(err)
	}
	oldWelcome := tuiWelcomeInfo(opts)
	oldWelcome.SessionID = "11111111-1111-4111-8111-111111111111"
	if oldWelcome.Model != "gpt-5.5" {
		t.Fatalf("old welcome = %+v", oldWelcome)
	}

	writeModel("glm-5.1")
	reloaded, nextWelcome, changed := reloadInteractiveOptions(opts, "ask", oldWelcome)
	if !changed || reloaded.model != "glm-5.1" || nextWelcome.Model != "glm-5.1" || nextWelcome.SessionID != oldWelcome.SessionID {
		t.Fatalf("reload changed=%v opts=%+v welcome=%+v", changed, reloaded, nextWelcome)
	}
	if got := tuiConfigReloadText(oldWelcome, nextWelcome); !strings.Contains(got, "model gpt-5.5 → glm-5.1") {
		t.Fatalf("reload text = %q", got)
	}

	explicit, _, err := parseArgs([]string{"--cwd", project, "--model", "locked-model"})
	if err != nil {
		t.Fatal(err)
	}
	explicitWelcome := tuiWelcomeInfo(explicit)
	explicitWelcome.SessionID = "22222222-2222-4222-8222-222222222222"
	writeModel("another-model")
	reloaded, nextWelcome, changed = reloadInteractiveOptions(explicit, "ask", explicitWelcome)
	if changed || reloaded.model != "locked-model" || nextWelcome.Model != "locked-model" || nextWelcome.SessionID != explicitWelcome.SessionID {
		t.Fatalf("explicit reload changed=%v opts=%+v welcome=%+v", changed, reloaded, nextWelcome)
	}
}

func TestReloadInteractiveOptionsKeepsRuntimeSelectedProviderModel(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_MODEL", "")
	setTestConfigRoot(t, filepath.Join(home, ".niuma"))

	configDir := filepath.Join(project, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte("provider: custom\nmodel: claude-sonnet-4-6\n"), 0600); err != nil {
		t.Fatal(err)
	}

	opts := options{
		cwd:          project,
		providerName: "responses-provider",
		settingsInputs: []string{`{
			"fallback": {
				"enabled": true,
				"providers": [{
					"name": "responses-provider",
					"type": "custom",
					"protocol": "openai-responses",
					"baseURL": "https://responses.example.test/v1",
					"apiKey": "test-key",
					"model": "gpt-5.6sol"
				}]
			}
		}`},
	}
	if err := validateSelectedProvider(&opts); err != nil {
		t.Fatal(err)
	}
	oldWelcome := tuiWelcomeInfoWithPermissionMode(opts, "allow")
	oldWelcome.SessionID = "33333333-3333-4333-8333-333333333333"
	if oldWelcome.Model != "gpt-5.6sol" {
		t.Fatalf("initial welcome model = %q", oldWelcome.Model)
	}

	reloaded, nextWelcome, changed := reloadInteractiveOptions(opts, "allow", oldWelcome)
	if changed {
		t.Fatalf("runtime provider reload reported a false change: %s", tuiConfigReloadText(oldWelcome, nextWelcome))
	}
	if reloaded.model != "gpt-5.6sol" || nextWelcome.Model != "gpt-5.6sol" {
		t.Fatalf("reload model options/welcome = %q/%q", reloaded.model, nextWelcome.Model)
	}
	if nextWelcome.SessionID != oldWelcome.SessionID {
		t.Fatalf("reload session id = %q, want %q", nextWelcome.SessionID, oldWelcome.SessionID)
	}
}

func TestSessionControlOptions(t *testing.T) {
	root := t.TempDir()
	setTestConfigRoot(t, root)
	t.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", filepath.Join(root, "projects"))
	project := t.TempDir()
	id := "22222222-2222-4222-8222-222222222222"
	opts, _, err := parseArgs([]string{"--cwd", project, "--session-id", id, "--name", "Named Session", "-p", "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.sessionID != id || opts.sessionName != "Named Session" {
		t.Fatalf("opts = %+v", opts)
	}
	recorder, err := newRecorderForOptions(session.DefaultStore(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if recorder.SessionID != id {
		t.Fatalf("session id = %s", recorder.SessionID)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	summary, ok, err := session.DefaultStore().Find(id)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || summary.Title != "Named Session" {
		t.Fatalf("summary = %+v ok=%v", summary, ok)
	}

	opts.noPersistence = true
	recorder, err = newRecorderForOptions(session.DefaultStore(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if recorder != nil {
		t.Fatalf("recorder = %+v, want nil", recorder)
	}
	if _, _, err := parseArgs([]string{"--session-id", "bad", "-p", "hello"}); err == nil {
		t.Fatal("expected invalid session id error")
	}
}

func TestSessionIDAliasAndResolutionConflicts(t *testing.T) {
	const id = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	opts, _, err := parseArgs([]string{"--sessionId", id, "-p", "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.sessionID != id {
		t.Fatalf("sessionId alias parsed as %q, want %q", opts.sessionID, id)
	}

	for name, opts := range map[string]options{
		"resume":         {sessionID: id, resume: id},
		"resume-at":      {sessionID: id, resumeSessionAt: "entry"},
		"fork":           {sessionID: id, forkSession: true},
		"no-persistence": {sessionID: id, noPersistence: true},
	} {
		t.Run(name, func(t *testing.T) {
			if err := resolveSessionOptions(session.Store{}, &opts); err == nil {
				t.Fatalf("expected --session-id conflict for %s", name)
			}
		})
	}
	if err := resolveSessionOptions(session.Store{}, &options{forkSession: true}); err == nil {
		t.Fatal("--fork-session without --resume must fail")
	}
	if err := resolveSessionOptions(session.Store{}, &options{resumeSessionAt: "entry"}); err == nil {
		t.Fatal("--resume-session-at without --resume must fail")
	}
}

func TestSessionIDResolvesExistingTranscriptBeforeQueryContext(t *testing.T) {
	root := t.TempDir()
	setTestConfigRoot(t, root)
	t.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", filepath.Join(root, "projects"))
	store := session.DefaultStore()
	project := t.TempDir()
	const id = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	recorder, err := store.NewRecorderWithID(project, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{Type: "message", Role: "user", Content: "previous session-id context"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	opts, _, err := parseArgs([]string{"--cwd", project, "--session-id", id, "-p", "next"})
	if err != nil {
		t.Fatal(err)
	}
	if err := resolveSessionOptions(store, &opts); err != nil {
		t.Fatal(err)
	}
	if opts.resume != id {
		t.Fatalf("resolved resume = %q, want %q", opts.resume, id)
	}
	messages, err := loadResumeMessages(store, opts.resume, opts.resumeSessionAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) < 1 || messages[0].Content[0].Text != "previous session-id context" {
		t.Fatalf("resolved session context = %+v", messages)
	}
	recorder, err = newRecorderForOptions(store, opts)
	if err != nil {
		t.Fatal(err)
	}
	if recorder.SessionID != id {
		t.Fatalf("recorder session id = %q, want %q", recorder.SessionID, id)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSessionIDReplaysAcrossIndependentCLIInvocations(t *testing.T) {
	root := t.TempDir()
	setTestConfigRoot(t, root)
	t.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", filepath.Join(root, "projects"))
	project := t.TempDir()
	const id = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"

	var requests []string
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, string(body))
		mu.Unlock()
		writeAnthropicTextStream(t, w, "assistant")
	}))
	defer server.Close()
	if err := config.SaveGlobalSettings(config.Settings{
		Provider: "anthropic",
		BaseURL:  server.URL,
		APIKey:   "test-key",
	}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := Run(context.Background(), []string{"--cwd", project, "--session-id", id, "-p", "first independent turn"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), []string{"--cwd", project, "--session-id", id, "-p", "second independent turn"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("model requests = %d, want 2", len(requests))
	}
	if !strings.Contains(requests[1], "first independent turn") || !strings.Contains(requests[1], "second independent turn") {
		t.Fatalf("second invocation did not replay the first turn:\n%s", requests[1])
	}
	summary, ok, err := session.DefaultStore().Find(id)
	if err != nil || !ok {
		t.Fatalf("Find() ok=%v err=%v", ok, err)
	}
	entries, err := session.LoadConversation(summary.Path)
	if err != nil {
		t.Fatal(err)
	}
	var transcript strings.Builder
	for _, entry := range entries {
		transcript.WriteString(entry.Content)
		transcript.WriteByte('\n')
	}
	if !strings.Contains(transcript.String(), "second independent turn") {
		t.Fatalf("second invocation was not appended to the same transcript: %+v", entries)
	}
}

func TestResumeUsesExistingSessionRecorderByDefault(t *testing.T) {
	root := t.TempDir()
	setTestConfigRoot(t, root)
	t.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", filepath.Join(root, "projects"))
	store := session.DefaultStore()
	project := t.TempDir()
	resumeID := "33333333-3333-4333-8333-333333333333"
	resumed, err := store.NewRecorderWithID(project, resumeID)
	if err != nil {
		t.Fatal(err)
	}
	if err := resumed.Append(session.Entry{Type: "message", Role: "user", Content: "old prompt"}); err != nil {
		t.Fatal(err)
	}
	if err := resumed.Close(); err != nil {
		t.Fatal(err)
	}
	summary, ok, err := store.Find(resumeID)
	if err != nil || !ok {
		t.Fatalf("Find resume ok=%v err=%v", ok, err)
	}

	otherProject := t.TempDir()
	opts, _, err := parseArgs([]string{"--cwd", otherProject, "--resume", resumeID})
	if err != nil {
		t.Fatal(err)
	}
	recorder, err := newRecorderForOptions(store, opts)
	if err != nil {
		t.Fatal(err)
	}
	if recorder.SessionID != resumeID || recorder.Path != summary.Path {
		t.Fatalf("recorder session/path = %s %s, want %s %s", recorder.SessionID, recorder.Path, resumeID, summary.Path)
	}
	if err := recorder.Append(session.Entry{Type: "message", Role: "user", Content: "continued prompt"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := session.Load(summary.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[1].Content != "continued prompt" {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestResumeSessionAtKeepsNewRecorderTimeline(t *testing.T) {
	root := t.TempDir()
	setTestConfigRoot(t, root)
	t.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", filepath.Join(root, "projects"))
	store := session.DefaultStore()
	project := t.TempDir()
	resumeID := "44444444-4444-4444-8444-444444444444"
	resumed, err := store.NewRecorderWithID(project, resumeID)
	if err != nil {
		t.Fatal(err)
	}
	if err := resumed.Append(session.Entry{ID: "msg-1", Type: "message", Role: "user", Content: "old prompt"}); err != nil {
		t.Fatal(err)
	}
	if err := resumed.Close(); err != nil {
		t.Fatal(err)
	}
	opts, _, err := parseArgs([]string{"--cwd", project, "--resume", resumeID, "--resume-session-at", "msg-1"})
	if err != nil {
		t.Fatal(err)
	}
	recorder, err := newRecorderForOptions(store, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	if recorder.SessionID == resumeID {
		t.Fatalf("resume-session-at should create a branch recorder, got original session %s", recorder.SessionID)
	}
}

func TestSessionStatusFromInteractiveSlash(t *testing.T) {
	cases := []struct {
		input  string
		output string
		want   string
	}{
		{input: "/compact", output: "Compacted current session (123 bytes summary)", want: "Compacted current session (123 bytes summary)"},
		{input: "/checkpoint before-edit", output: "Rewound to checkpoint before-edit", want: "Rewound to checkpoint before-edit"},
		{input: "/rewind before-edit", output: "", want: "rewind selected"},
		{input: "/resume sess-1", output: "", want: "resumed sess-1"},
		{input: "/status", output: "ok", want: ""},
	}
	for _, tc := range cases {
		if got := sessionStatusFromInteractiveSlash(tc.input, tc.output); got != tc.want {
			t.Fatalf("sessionStatusFromInteractiveSlash(%q, %q) = %q, want %q", tc.input, tc.output, got, tc.want)
		}
	}
}

func TestPrintInputFormatAndStdinPrompt(t *testing.T) {
	opts, _, err := parseArgs([]string{"--input-format", "stream-json", "-p", "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.inputFormat != "stream-json" {
		t.Fatalf("input format = %q", opts.inputFormat)
	}
	got, err := promptFromStreamJSON([]byte(
		`{"type":"user","message":{"content":[{"type":"text","text":"hello"},{"type":"text","text":"world"}]}}` + "\n" +
			`{"type":"assistant","message":{"content":"ignored"}}` + "\n" +
			`{"prompt":"again"}` + "\n",
	))
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello\nworld\nagain" {
		t.Fatalf("prompt = %q", got)
	}

	oldStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString("from stdin\n"); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdin = r
	defer func() {
		os.Stdin = oldStdin
		_ = r.Close()
	}()
	got, err = readPromptFromStdin("text")
	if err != nil {
		t.Fatal(err)
	}
	if got != "from stdin" {
		t.Fatalf("stdin prompt = %q", got)
	}
}

func TestJSONSchemaValidation(t *testing.T) {
	opts, _, err := parseArgs([]string{"--json-schema", `{"type":"object"}`, "-p", "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.jsonSchema == "" {
		t.Fatal("json schema was not parsed")
	}
	schema := `{"type":"object","required":["name","items"],"properties":{"name":{"type":"string"},"items":{"type":"array","items":{"type":"integer"}}}}`
	if err := validateJSONSchemaResponse("", schema, "```json\n{\"name\":\"demo\",\"items\":[1,2]}\n```"); err != nil {
		t.Fatal(err)
	}
	if err := validateJSONSchemaResponse("", schema, `{"name":"demo","items":["bad"]}`); err == nil {
		t.Fatal("expected schema validation error")
	}
	if _, err := jsonSchemaText("", `{"type":"object"}`); err != nil {
		t.Fatal(err)
	}
}

func TestParseArgsRuntimeTraceOutput(t *testing.T) {
	opts, _, err := parseArgs([]string{"--runtime-trace-output", "artifacts/run.json", "-p", "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.runtimeTraceOutput != "artifacts/run.json" {
		t.Fatalf("runtime trace output = %q", opts.runtimeTraceOutput)
	}
	if _, _, err := parseArgs([]string{"--runtime-trace-output"}); err == nil {
		t.Fatal("expected missing runtime trace output path error")
	}
}

func TestRuntimeTraceOutputCleanupExportsRecordedArtifact(t *testing.T) {
	root := t.TempDir()
	transcripts := filepath.Join(root, "projects")
	t.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", transcripts)
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(root, "config"))
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	cwd := t.TempDir()
	recorder, err := session.DefaultStore().NewRecorderWithID(cwd, "22222222-2222-4222-8222-222222222222")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 11, 3, 0, 0, 0, time.UTC)
	if err := session.NewRuntimeSpanSink(recorder).Emit(t.Context(), telemetry.Event{
		SchemaVersion: telemetry.SchemaVersionRuntimeTraceV1,
		Name:          telemetry.EventQueryRun + telemetry.SpanFinishedSuffix,
		Status:        telemetry.StatusOK,
		TraceID:       "trace-cli-export",
		SpanID:        "query-cli-export",
		StartedAt:     base,
		OccurredAt:    base.Add(15 * time.Millisecond),
		DurationMS:    15,
	}); err != nil {
		t.Fatal(err)
	}
	_, cleanup, err := newQuerySession(t.Context(), options{
		runtimeProfile:           runtimeprofile.ProfileBare,
		cwd:                      cwd,
		maxTurns:                 1,
		disableTools:             true,
		promptMode:               promptmode.Code.String(),
		runtimeTraceOutput:       filepath.Join("artifacts", "trace.json"),
		maxParallelReadOnlyTools: -1,
	}, nil, recorder)
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(cwd, "artifacts", "trace.json"))
	if err != nil {
		t.Fatal(err)
	}
	var artifact struct {
		SchemaVersion string `json:"schema_version"`
		Run           struct {
			SessionID  string `json:"session_id"`
			DurationMS int64  `json:"duration_ms"`
		} `json:"run"`
		Configuration server.RuntimeTraceConfiguration `json:"configuration"`
	}
	if err := json.Unmarshal(data, &artifact); err != nil {
		t.Fatal(err)
	}
	if artifact.SchemaVersion != telemetry.SchemaVersionRuntimeTraceV1 || artifact.Run.SessionID != recorder.SessionID || artifact.Run.DurationMS != 15 {
		t.Fatalf("artifact = %+v", artifact)
	}
	if artifact.Configuration.ReadOnlyToolMode != server.RuntimeTraceReadOnlyToolModeSerial || artifact.Configuration.RequestedMaxParallelReadOnlyTools != -1 || artifact.Configuration.EffectiveMaxParallelReadOnlyTools != 1 {
		t.Fatalf("artifact configuration = %+v", artifact.Configuration)
	}
}

func TestParseArgsMaxParallelReadOnlyTools(t *testing.T) {
	opts, _, err := parseArgs([]string{"--max-parallel-read-only-tools", "8", "-p", "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.maxParallelReadOnlyTools != 8 {
		t.Fatalf("max parallel read-only tools = %d", opts.maxParallelReadOnlyTools)
	}
	disabled, _, err := parseArgs([]string{"--max-parallel-read-only-tools", "-1", "-p", "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if disabled.maxParallelReadOnlyTools != -1 {
		t.Fatalf("disabled max parallel read-only tools = %d", disabled.maxParallelReadOnlyTools)
	}
	if _, _, err := parseArgs([]string{"--max-parallel-read-only-tools", "many"}); err == nil {
		t.Fatal("expected invalid max parallel read-only tools error")
	}
}

func TestRuntimePermissionAndDirectoryOptions(t *testing.T) {
	opts, _, err := parseArgs([]string{
		"--allowedTools", "Read,Bash:*",
		"--disallowedTools", "Write",
		"--permission-mode", "deny",
		"--add-dir", "/tmp/extra",
		"-p", "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	settings := config.Settings{}
	if err := applyRuntimeOptions(&settings, opts); err != nil {
		t.Fatal(err)
	}
	if strings.Join(settings.Permissions.Allow, ",") != "Read,Bash:*" || strings.Join(settings.Permissions.Deny, ",") != "Write" || settings.Permissions.DefaultMode != "deny" {
		t.Fatalf("permissions = %+v", settings.Permissions)
	}
	if len(settings.AdditionalDirectories) != 1 || settings.AdditionalDirectories[0] != "/tmp/extra" {
		t.Fatalf("additional dirs = %+v", settings.AdditionalDirectories)
	}

	opts, _, err = parseArgs([]string{"--dangerously-skip-permissions", "--permission-mode", "deny", "-p", "hello"})
	if err != nil {
		t.Fatal(err)
	}
	settings = config.Settings{Permissions: config.PermissionSettings{Deny: []string{"Bash"}, DefaultMode: "deny"}}
	if err := applyRuntimeOptions(&settings, opts); err != nil {
		t.Fatal(err)
	}
	// A bypass run keeps explicit deny rules: CheckRequest evaluates deny before
	// the bypass short-circuit.
	if strings.Join(settings.Permissions.Deny, ",") != "Bash" || settings.Permissions.DefaultMode != "allow" || len(settings.Permissions.Allow) != 1 || settings.Permissions.Allow[0] != "*" || !settings.Permissions.Bypass {
		t.Fatalf("skip permissions settings = %+v", settings.Permissions)
	}

	opts, _, err = parseArgs([]string{"--permission-mode", "acceptEdits", "-p", "hello"})
	if err != nil {
		t.Fatal(err)
	}
	settings = config.Settings{}
	if err := applyRuntimeOptions(&settings, opts); err != nil {
		t.Fatal(err)
	}
	if settings.Permissions.DefaultMode != "acceptEdits" || settings.Permissions.Bypass {
		t.Fatalf("acceptEdits settings = %+v", settings.Permissions)
	}

	opts, _, err = parseArgs([]string{"--permission-mode", "bypassPermissions", "-p", "hello"})
	if err != nil {
		t.Fatal(err)
	}
	settings = config.Settings{}
	if err := applyRuntimeOptions(&settings, opts); err != nil {
		t.Fatal(err)
	}
	if settings.Permissions.DefaultMode != "bypassPermissions" || !settings.Permissions.Bypass {
		t.Fatalf("bypassPermissions settings = %+v", settings.Permissions)
	}
	settings = config.Settings{}
	if err := applyRuntimeOptions(&settings, options{permissionMode: "allow"}); err != nil {
		t.Fatal(err)
	}
	if settings.Permissions.Bypass || settings.Permissions.DefaultMode != "allow" {
		t.Fatalf("plain allow should not bypass classifier prompts: %+v", settings.Permissions)
	}
	settings = config.Settings{Permissions: config.PermissionSettings{Deny: []string{"Bash"}, DefaultMode: "deny"}}
	if err := applyRuntimeOptions(&settings, options{permissionMode: "allow", permissionBypass: true}); err != nil {
		t.Fatal(err)
	}
	if !settings.Permissions.Bypass || strings.Join(settings.Permissions.Deny, ",") != "Bash" || len(settings.Permissions.Allow) != 1 || settings.Permissions.Allow[0] != "*" || settings.Permissions.DefaultMode != "allow" {
		t.Fatalf("interactive allow should bypass prompts but keep deny rules: %+v", settings.Permissions)
	}

	opts, _, err = parseArgs([]string{"--permission-mode", "plan", "-p", "hello"})
	if err != nil {
		t.Fatal(err)
	}
	settings = config.Settings{}
	if err := applyRuntimeOptions(&settings, opts); err != nil {
		t.Fatal(err)
	}
	if settings.Permissions.DefaultMode != "ask" {
		t.Fatalf("plan settings = %+v", settings.Permissions)
	}

	opts, _, err = parseArgs([]string{"--delegate-permissions", "-p", "hello"})
	if err != nil {
		t.Fatal(err)
	}
	settings = config.Settings{}
	if err := applyRuntimeOptions(&settings, opts); err != nil {
		t.Fatal(err)
	}
	if settings.Permissions.DefaultMode != "allow" {
		t.Fatalf("delegate permissions settings = %+v", settings.Permissions)
	}
}

func TestInteractivePermissionModeSwitchesAndBypassStaysBypass(t *testing.T) {
	mode := newInteractivePermissionMode(options{permissionMode: "ask"})
	if got := mode.current(); got != "ask" {
		t.Fatalf("mode = %s", got)
	}
	next, err := mode.switchNext("ask")
	if err != nil {
		t.Fatal(err)
	}
	if next != "allow" || mode.current() != "allow" {
		t.Fatalf("mode = %s current=%s", next, mode.current())
	}
	next, err = mode.switchNext("allow")
	if err != nil {
		t.Fatal(err)
	}
	if next != "deny" || mode.current() != "deny" {
		t.Fatalf("mode = %s current=%s", next, mode.current())
	}

	bypass := newInteractivePermissionMode(options{skipPermissions: true})
	next, err = bypass.switchNext("bypass")
	if err != nil {
		t.Fatal(err)
	}
	if next != "bypass" || bypass.current() != "bypass" {
		t.Fatalf("bypass mode = %s current=%s", next, bypass.current())
	}
}

func TestApplyInteractivePermissionAllowBypassesPrompts(t *testing.T) {
	opts := options{}
	applyInteractivePermissionMode(&opts, "allow")
	if opts.permissionMode != "allow" || !opts.permissionBypass {
		t.Fatalf("opts = %+v", opts)
	}
	settings := config.Settings{Permissions: config.PermissionSettings{DefaultMode: "ask", Deny: []string{"Bash"}}}
	if err := applyRuntimeOptions(&settings, opts); err != nil {
		t.Fatal(err)
	}
	if !settings.Permissions.Bypass || settings.Permissions.DefaultMode != "allow" || strings.Join(settings.Permissions.Allow, ",") != "*" {
		t.Fatalf("settings = %+v", settings.Permissions)
	}

	opts = options{}
	applyInteractivePermissionMode(&opts, "ask")
	if opts.permissionMode != "ask" || opts.permissionBypass {
		t.Fatalf("ask opts = %+v", opts)
	}
}

func TestCompactCommand(t *testing.T) {
	root := t.TempDir()
	setTestConfigRoot(t, root)
	recorder, err := session.DefaultStore().NewRecorder(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{Type: "message", Role: "user", Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"compact", recorder.SessionID, "--max-bytes", "64"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	entries, err := session.Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	if entries[len(entries)-1].Type != "compact_summary" {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestHooksCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_MODEL", "")
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"hooks", "add", "PreToolUse", "echo", "hi"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Run(context.Background(), []string{"hooks", "list"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "PreToolUse") || !strings.Contains(out.String(), "echo hi") {
		t.Fatalf("output = %s", out.String())
	}
	out.Reset()
	if err := Run(context.Background(), []string{"hooks", "remove", "PreToolUse", "0"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Run(context.Background(), []string{"hooks", "list"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "echo hi") {
		t.Fatalf("output = %s", out.String())
	}
	if err := Run(context.Background(), []string{"hooks", "add", "PostToolUse", "echo", "bye"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Run(context.Background(), []string{"hooks", "clear"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Run(context.Background(), []string{"hooks", "list"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "PostToolUse") {
		t.Fatalf("output = %s", out.String())
	}
}

func TestAgentsCommand(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_MODEL", "")
	agentPath := filepath.Join(project, ".claude", "agents", "reviewer.md")
	mustWrite(t, agentPath, "---\nname: reviewer\ndescription: Reviews code\n---\nReview.")
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"--cwd", project, "agents", "list"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "reviewer") || !strings.Contains(out.String(), "Reviews code") {
		t.Fatalf("output = %s", out.String())
	}
	out.Reset()
	if err := Run(context.Background(), []string{"--cwd", project, "agents", "show", "reviewer"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"name": "reviewer"`) || !strings.Contains(out.String(), `"prompt": "Review."`) {
		t.Fatalf("output = %s", out.String())
	}
}

func TestAgentsDoctorCommand(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_MODEL", "")
	agentPath := filepath.Join(project, ".claude", "agents", "bad.md")
	mustWrite(t, agentPath, "---\nname: bad\npermissionMode: root\nfutureField: keep\n---\n")
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"--cwd", project, "agents", "doctor"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"Agents: 4", "error\tbad\tprompt", "error\tbad\tpermissionMode", "warning\tbad\tfutureField"} {
		if !strings.Contains(text, want) {
			t.Fatalf("doctor output missing %q:\n%s", want, text)
		}
	}
	out.Reset()
	if err := Run(context.Background(), []string{"--cwd", project, "agents", "doctor", "--json"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"status": "error"`) || !strings.Contains(out.String(), `"diagnostics"`) {
		t.Fatalf("json output = %s", out.String())
	}
}

func TestBackgroundCommands(t *testing.T) {
	root := t.TempDir()
	project := t.TempDir()
	setTestConfigRoot(t, root)
	t.Setenv("NIUMA_BG_QUEUE_ONLY", "1")

	var out bytes.Buffer
	if err := Run(context.Background(), []string{"--cwd", project, "-p", "hello", "--bg"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(out.String())
	if len(fields) == 0 || !strings.HasPrefix(fields[len(fields)-1], "bg_") {
		t.Fatalf("output = %s", out.String())
	}
	id := fields[len(fields)-1]

	out.Reset()
	if err := Run(context.Background(), []string{"ps"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), id) || !strings.Contains(out.String(), "queued") || !strings.Contains(out.String(), "hello") {
		t.Fatalf("output = %s", out.String())
	}

	out.Reset()
	if err := Run(context.Background(), []string{"logs", id}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "No logs") {
		t.Fatalf("output = %s", out.String())
	}

	out.Reset()
	if err := Run(context.Background(), []string{"kill", id}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Killed background session "+id) {
		t.Fatalf("output = %s", out.String())
	}

	out.Reset()
	if err := Run(context.Background(), []string{"ps"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), id) || !strings.Contains(out.String(), "killed") {
		t.Fatalf("output = %s", out.String())
	}
}

func TestGoalRunBackgroundQueuesGoalJob(t *testing.T) {
	root := t.TempDir()
	project := t.TempDir()
	setTestConfigRoot(t, root)
	t.Setenv("NIUMA_BG_QUEUE_ONLY", "1")

	var out bytes.Buffer
	if err := Run(context.Background(), []string{"--cwd", project, "--goal-evaluator", "model", "goal", "run", "goal_abc", "--background"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Queued background session") {
		t.Fatalf("output = %s", out.String())
	}
	jobs, err := background.DefaultStore().List()
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Kind != "goal" || jobs[0].Prompt != "goal_abc" || jobs[0].CWD != project || jobs[0].GoalEvaluator != "model" {
		t.Fatalf("jobs = %+v", jobs)
	}
	out.Reset()
	if err := Run(context.Background(), []string{"ps"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "goal") || !strings.Contains(out.String(), "goal_abc") {
		t.Fatalf("ps output = %s", out.String())
	}
}

func TestGoalRunBackgroundJSONQueuesCleanJSON(t *testing.T) {
	root := t.TempDir()
	setTestConfigRoot(t, root)
	t.Setenv("NIUMA_BG_QUEUE_ONLY", "1")

	var out bytes.Buffer
	if err := Run(context.Background(), []string{"--goal-evaluator", "model", "goal", "run", "goal_json", "--background", "--json"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var job background.Job
	if err := json.Unmarshal(out.Bytes(), &job); err != nil {
		t.Fatalf("json output = %s err=%v", out.String(), err)
	}
	if job.Kind != "goal" || job.Prompt != "goal_json" || job.GoalEvaluator != "model" {
		t.Fatalf("job = %+v", job)
	}
}

func TestGoalRunRejectsOnceAndBackground(t *testing.T) {
	var out bytes.Buffer
	err := Run(context.Background(), []string{"goal", "run", "goal_abc", "--once", "--background"}, &out, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "cannot combine") {
		t.Fatalf("err = %v", err)
	}
}

func TestBackgroundAttachCommand(t *testing.T) {
	root := t.TempDir()
	setTestConfigRoot(t, root)
	store := background.DefaultStore()
	job, err := store.Create("attach me", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(job.LogPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(job.LogPath, []byte("background output\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Finish(job.ID, 0, ""); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := Run(context.Background(), []string{"attach", job.ID}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Status: completed") || !strings.Contains(out.String(), "background output") {
		t.Fatalf("output = %s", out.String())
	}

	out.Reset()
	if err := Run(context.Background(), []string{"attach", job.ID, "--wait"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Status: completed") || !strings.Contains(out.String(), "background output") {
		t.Fatalf("output = %s", out.String())
	}
}

type namedTool string

type fakeTenantAgentTaskService struct {
	tasks           []mysqlstore.AgentTask
	events          []mysqlstore.AgentTaskEvent
	lastLimit       int
	lastTaskID      uint64
	cancelledTaskID uint64
	cancelPayload   string
	closed          int
}

func (f *fakeTenantAgentTaskService) ListAgentTasks(ctx context.Context, limit int) ([]mysqlstore.AgentTask, error) {
	f.lastLimit = limit
	return f.tasks, nil
}

func (f *fakeTenantAgentTaskService) ListAgentTaskEvents(ctx context.Context, taskID uint64, limit int) ([]mysqlstore.AgentTaskEvent, error) {
	f.lastTaskID = taskID
	f.lastLimit = limit
	return f.events, nil
}

func (f *fakeTenantAgentTaskService) CancelAgentTask(ctx context.Context, taskID uint64, resultJSON string) error {
	f.cancelledTaskID = taskID
	f.cancelPayload = resultJSON
	return nil
}

type fakeTenantSkillPackageService struct {
	renderCalls  int
	publishCalls int
	closed       int
	lastReq      tenantservice.SkillPackageRequest
	skills       []mysqlstore.Skill
}

func (f *fakeTenantSkillPackageService) RenderSkillPackage(ctx context.Context, req tenantservice.SkillPackageRequest) (tenantservice.SkillPackageResult, error) {
	f.renderCalls++
	f.lastReq = req
	return tenantservice.SkillPackageResult{
		SkillKey:      req.SkillKey,
		Name:          req.Name,
		PackageSHA256: "sha-render",
		RuntimeMD:     "# Runtime\n",
	}, nil
}

func (f *fakeTenantSkillPackageService) PublishSkillPackage(ctx context.Context, req tenantservice.SkillPackageRequest) (tenantservice.SkillPackageResult, error) {
	f.publishCalls++
	f.lastReq = req
	return tenantservice.SkillPackageResult{
		ID:            44,
		SkillKey:      req.SkillKey,
		Name:          req.Name,
		Version:       1,
		PackageSHA256: "sha-publish",
		PackageRef:    "file:///pkg.zip",
		RuntimeRef:    "file:///runtime.md",
		RuntimeMD:     "# Runtime\n",
	}, nil
}

func (f *fakeTenantSkillPackageService) ListSkills(ctx context.Context, enabledOnly bool, limit int) ([]mysqlstore.Skill, error) {
	return f.skills, nil
}

func (f *fakeTenantSkillPackageService) GetSkill(ctx context.Context, skillKey string, version uint) (mysqlstore.Skill, error) {
	for _, item := range f.skills {
		if item.SkillKey == skillKey && (version == 0 || item.Version == version) {
			return item, nil
		}
	}
	return mysqlstore.Skill{}, mysqlstore.ErrNotFound
}

func (f *fakeTenantSkillPackageService) RollbackSkill(ctx context.Context, req tenantservice.SkillRollbackRequest) (tenantservice.SkillRollbackResult, error) {
	return tenantservice.SkillRollbackResult{ID: 10, SkillKey: req.SkillKey, FromVersion: req.Version, Version: req.Version + 1}, nil
}

func (n namedTool) Name() string        { return string(n) }
func (n namedTool) Description() string { return string(n) }
func (n namedTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (n namedTool) Run(context.Context, json.RawMessage, tools.Context) tools.Result {
	return tools.Result{Content: string(n)}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestFlagValue(t *testing.T) {
	args := []string{"--source", "abc"}
	i := 0
	v, err := flagValue(args, &i, "--source")
	if err != nil || v != "abc" || i != 1 {
		t.Fatalf("got v=%q i=%d err=%v", v, i, err)
	}
	i = 0
	if _, err := flagValue([]string{"--source"}, &i, "--source"); err == nil || err.Error() != "--source requires a value" {
		t.Fatalf("expected requires-a-value error, got %v", err)
	}
}

// workflowBudgetedDocuments used to report every budgeted document, including
// non-workflow ones, because the byte budget only applied to workflows.
func TestStatusContextSeparatesWorkflowAndMemoryBudgets(t *testing.T) {
	report := memory.PromptDocumentsReport{
		BudgetedDocuments:   2,
		WorkflowBudgetBytes: 4096,
		DocumentBudgetBytes: 16384,
		TotalBudgetBytes:    65536,
		Documents: []memory.PromptDocumentSummary{
			{Path: "AGENTS.md", Type: "Workflow", Workflow: true, Budgeted: true},
			{Path: "CLAUDE.md", Type: "Project", Budgeted: true},
		},
	}
	if got := statusWorkflowBudgetedDocuments(report); got != 1 {
		t.Fatalf("workflowBudgetedDocuments = %d, want 1", got)
	}
}

// Install an explicitly parsed fixture into an isolated global file. YAML
// fixtures continue to exercise parsing, without relying on implicit discovery.
func activateSettingsFixture(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	settings := config.LoadSettingsFile(path)
	t.Setenv("NIUMA_CONFIG_DIR", t.TempDir())
	if err := config.SaveGlobalSettings(settings); err != nil {
		t.Fatal(err)
	}
}
