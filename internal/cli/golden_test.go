package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/server"
	"github.com/konglong87/super-niuma/internal/session"
)

type goldenRun struct {
	home               string
	configDir          string
	sessionProjectsDir string
	project            string
}

func TestCLIGoldenStableCommands(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{name: "help", args: []string{"--help"}},
		{name: "version", args: []string{"--version"}},
		{name: "unknown_command", args: []string{"frobnicate"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, stderr bytes.Buffer
			err := Run(context.Background(), tc.args, &out, &stderr)
			got := out.String()
			if err != nil {
				got = strings.TrimSpace(err.Error())
			}
			assertGolden(t, tc.name+".txt", got)
		})
	}
}

func TestCLIGoldenEmptyCommandGroups(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{name: "auth_status_none", args: []string{"auth", "status"}},
		{name: "config_list_empty", args: []string{"config", "list"}},
		{name: "mcp_list_empty", args: []string{"mcp", "list"}},
		{name: "session_list_empty", args: []string{"session", "list"}},
		{name: "skills_list_empty", args: []string{"skills", "list"}},
		{name: "plugins_list_empty", args: []string{"plugin", "list"}},
		{name: "permissions_list_empty", args: []string{"permissions", "list"}},
		{name: "hooks_list_empty", args: []string{"hooks", "list"}},
		{name: "agents_list_empty", args: []string{"agents", "list"}},
		{name: "usage_empty", args: []string{"usage"}},
		{name: "model_get_default", args: []string{"model", "get"}},
		{name: "model_list", args: []string{"model", "list"}},
		{name: "background_ps_empty", args: []string{"ps"}},
		{name: "completion_zsh", args: []string{"completion", "zsh"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runGoldenCLI(t, tc.args, nil)
			assertGolden(t, tc.name+".txt", got)
		})
	}
}

func TestCLIGoldenProjectCommandGroups(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		setup func(t *testing.T, env goldenRun)
	}{
		{
			name: "config_project_list",
			args: []string{"config", "--project", "list"},
			setup: func(t *testing.T, env goldenRun) {
				mustWrite(t, filepath.Join(env.project, ".niuma", "settings.json"), `{
  "model": "project-model",
  "permissions": {
    "allow": ["Read"],
    "defaultMode": "ask"
  },
  "mcpServers": {
    "demo": {
      "type": "stdio",
      "command": "echo",
      "args": ["hello"]
    }
  }
}`)
			},
		},
		{
			name: "mcp_list_project",
			args: []string{"mcp", "list"},
			setup: func(t *testing.T, env goldenRun) {
				mustWrite(t, filepath.Join(env.project, ".claude", "settings.json"), `{"mcpServers":{"demo":{"command":"echo","args":["hello"]}}}`)
				activateSettingsFixture(t, filepath.Join(env.project, ".claude", "settings.json"))
			},
		},
		{
			name: "skills_list_project",
			args: []string{"skills", "list"},
			setup: func(t *testing.T, env goldenRun) {
				mustWrite(t, filepath.Join(env.project, ".claude", "skills", "demo", "SKILL.md"), "# Demo Skill\n\nUse demo behavior.\n")
			},
		},
		{
			name: "plugins_list_project",
			args: []string{"plugin", "list"},
			setup: func(t *testing.T, env goldenRun) {
				mustWrite(t, filepath.Join(env.project, ".claude", "plugins", "demo", ".codex-plugin", "plugin.json"), `{"name":"demo","version":"0.1.0","description":"Demo plugin"}`)
			},
		},
		{
			name: "agents_list_project",
			args: []string{"agents", "list"},
			setup: func(t *testing.T, env goldenRun) {
				mustWrite(t, filepath.Join(env.project, ".claude", "agents", "reviewer.md"), "---\nname: reviewer\ndescription: Reviews code\n---\nReview.")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runGoldenCLI(t, tc.args, tc.setup)
			assertGolden(t, tc.name+".txt", got)
		})
	}
}

func TestCLIGoldenPrintModes(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{name: "print_text", args: []string{"--no-session-persistence", "-p", "hello"}},
		{name: "print_json", args: []string{"--no-session-persistence", "--output-format", "json", "-p", "hello"}},
		{name: "print_stream_json", args: []string{"--no-session-persistence", "--output-format", "stream-json", "-p", "hello"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "print_stream_json" {
				t.Setenv("NIUMA_DETERMINISTIC_STREAM_RESULT", "true")
			}
			got := runGoldenCLI(t, tc.args, func(t *testing.T, env goldenRun) {
				startGoldenAnthropicServer(t)
			})
			assertGolden(t, tc.name+".txt", got)
		})
	}
}

func TestCLIGoldenErrorShapes(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{name: "flag_unknown", args: []string{"--definitely-unknown"}},
		{name: "server_unknown_option", args: []string{"server", "--bad"}},
		{name: "tenant_migrate_requires_dsn", args: []string{"tenant", "migrate", "up"}},
		{name: "tenant_agent_tasks_requires_dsn", args: []string{"tenant", "agent-tasks", "list"}},
		{name: "session_show_requires_id", args: []string{"session", "show"}},
		{name: "mcp_show_requires_name", args: []string{"mcp", "show"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runGoldenCLI(t, tc.args, nil)
			assertGolden(t, tc.name+".txt", got)
		})
	}
}

func TestCLIGoldenProcessErrorChannels(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		setup func(t *testing.T, env goldenRun)
	}{
		{name: "process_flag_unknown", args: []string{"--definitely-unknown"}},
		{name: "process_session_show_requires_id", args: []string{"session", "show"}},
		{name: "process_mcp_show_requires_name", args: []string{"mcp", "show"}},
		{name: "process_tenant_migrate_requires_dsn", args: []string{"tenant", "migrate", "up"}},
		{name: "process_tenant_agent_tasks_requires_dsn", args: []string{"tenant", "agent-tasks", "list"}},
		{name: "process_version_success", args: []string{"--version"}},
		{name: "process_auth_status_success", args: []string{"auth", "status"}},
		{name: "process_model_list_success", args: []string{"model", "list"}},
		{name: "process_completion_zsh_success", args: []string{"completion", "zsh"}},
		{
			name: "process_agents_list_success",
			args: []string{"agents", "list"},
			setup: func(t *testing.T, env goldenRun) {
				mustWrite(t, filepath.Join(env.project, ".claude", "agents", "reviewer.md"), "---\nname: reviewer\ndescription: Reviews code\n---\nReview.")
			},
		},
		{name: "process_permissions_list_success", args: []string{"permissions", "list"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runGoldenCLIProcess(t, tc.args, tc.setup)
			assertGolden(t, tc.name+".json", got)
		})
	}
}

func TestCLIGoldenAgentsShow(t *testing.T) {
	got := runGoldenCLI(t, []string{"agents", "show", "reviewer"}, func(t *testing.T, env goldenRun) {
		mustWrite(t, filepath.Join(env.project, ".claude", "agents", "reviewer.md"), `---
name: reviewer
description: Reviews code
tools:
  - Read
  - Grep
disallowedTools:
  - Bash
mcpServers:
  - local
  - search:
      type: http
      url: https://example.test/mcp
skills:
  - code-review
initialPrompt: /status
maxTurns: 7
background: true
memory: project
effort: high
permissionMode: ask
criticalSystemReminder_EXPERIMENTAL: Stay focused.
futureField: keep-me-visible
model: claude-sonnet
---
Review carefully.`)
	})
	got = normalizeGoldenPaths(got)
	assertGolden(t, "agents_show_project.json", got)
}

func TestCLIGoldenServerStartCanceled(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, ".claude")
	project := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(project, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	t.Setenv("NIUMA_MYSQL_DSN", "")
	t.Setenv("MYSQL_DSN", "")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	err := serverCommand(ctx, []string{"--host", "127.0.0.1", "--port", "0", "--auth-token", "golden"}, options{cwd: project}, &out)
	if err != nil {
		t.Fatal(err)
	}
	assertGolden(t, "server_start_canceled.txt", out.String())
}

func TestServerCommandDefaultsWorkspaceToCurrentDirectory(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, ".claude")
	project := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(project, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	t.Setenv("NIUMA_MYSQL_DSN", "")
	t.Setenv("MYSQL_DSN", "")
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldwd)
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	err = serverCommand(ctx, []string{"--host", "127.0.0.1", "--port", "0", "--auth-token", "golden"}, options{}, &out)
	if err != nil {
		t.Fatal(err)
	}
	gotWorkspace, err := filepath.EvalSymlinks(defaultServerWorkspace(""))
	if err != nil {
		t.Fatal(err)
	}
	wantWorkspace, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	if gotWorkspace != wantWorkspace {
		t.Fatalf("workspace = %q, want %q", gotWorkspace, wantWorkspace)
	}
	handler := server.NewHandler(server.Options{AuthToken: "token", Workspace: project}, nil)
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	req.Header.Set("authorization", "Bearer token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"workspace":"`+project+`"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCLIGoldenMutationFlows(t *testing.T) {
	const sessionID = "11111111-1111-4111-8111-111111111111"
	cases := []struct {
		name     string
		commands [][]string
		setup    func(t *testing.T, env goldenRun)
	}{
		{
			name: "auth_mutation_flow",
			commands: [][]string{
				{"auth", "login", "--api-key", "golden-key"},
				{"auth", "status"},
				{"auth", "logout"},
				{"auth", "status"},
			},
		},
		{
			name: "config_mutation_flow",
			commands: [][]string{
				{"config", "set", "model", "golden-model"},
				{"config", "get", "model"},
				{"config", "set", "maxToolResultBytes", "2048"},
				{"config", "get", "maxToolResultBytes"},
				{"config", "unset", "model"},
			},
		},
		{
			name: "mcp_mutation_flow",
			commands: [][]string{
				{"mcp", "add", "demo", "--env", "FOO=bar", "echo", "hello"},
				{"mcp", "show", "demo"},
				{"mcp", "list"},
				{"mcp", "remove", "demo"},
				{"mcp", "list"},
			},
		},
		{
			name: "session_mutation_flow",
			commands: [][]string{
				{"session", "rename", sessionID, "Golden", "Session"},
				{"session", "checkpoint", sessionID, "before-edit"},
				{"session", "rewind", sessionID, "before-edit"},
				{"session", "delete", sessionID},
			},
			setup: func(t *testing.T, env goldenRun) {
				store := session.Store{TranscriptProjectsRoot: env.sessionProjectsDir}
				recorder, err := store.NewRecorderWithID(env.project, sessionID)
				if err != nil {
					t.Fatal(err)
				}
				if err := recorder.Append(session.Entry{Type: "message", Role: "user", Content: "hello"}); err != nil {
					t.Fatal(err)
				}
				if err := recorder.Close(); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "goal_mutation_flow",
			commands: [][]string{
				{"goal", "start", "Ship", "golden", "goal", "--turn-budget", "4", "--token-budget", "1000"},
				{"goal", "list"},
				{"goal", "status"},
				{"goal", "logs", "--limit", "2"},
				{"goal", "stop"},
				{"goal", "resume"},
			},
		},
		{
			name: "plugins_mutation_flow",
			commands: [][]string{
				{"plugin", "install", "plugin-src", "--project"},
				{"plugin", "list"},
				{"plugin", "remove", "demo", "--project"},
				{"plugin", "list"},
			},
			setup: func(t *testing.T, env goldenRun) {
				mustWrite(t, filepath.Join(env.project, "plugin-src", ".codex-plugin", "plugin.json"), `{"name":"demo","version":"0.1.0","description":"Demo plugin"}`)
			},
		},
		{
			name: "skills_mutation_flow",
			commands: [][]string{
				{"skills", "marketplace-info", "--source", "skills-index.yaml"},
				{"skills", "marketplace-search", "--source", "skills-index.yaml", "--query", "market"},
				{"skills", "sync", "--source", "skills-index.yaml"},
				{"skills", "install", "--source", "skills-index.yaml", "market-install"},
				{"skills", "list"},
				{"skills", "context", "--prompt", "please use market"},
				{"skills", "feedback", "market-install", "--rating", "5", "--comment", "useful", "--target", "skill-feedback.jsonl"},
				{"skills", "show", "market-install"},
			},
			setup: func(t *testing.T, env goldenRun) {
				mustWrite(t, filepath.Join(env.project, "skills-index.yaml"), `skills:
  - name: market-sync
    description: Synced marketplace skill
    content: "# Market Sync\n\nUse synced instructions."
  - name: market-install
    description: Installed marketplace skill
    content: "# Market Install\n\nUse installed instructions."
`)
			},
		},
		{
			name: "permissions_mutation_flow",
			commands: [][]string{
				{"permissions", "mode", "ask"},
				{"permissions", "allow", "Bash:go test*"},
				{"permissions", "deny", "Write:secret.txt"},
				{"permissions", "list"},
				{"permissions", "remove", "Write:secret.txt"},
				{"permissions", "clear"},
				{"permissions", "list"},
			},
		},
		{
			name: "hooks_mutation_flow",
			commands: [][]string{
				{"hooks", "add", "PreToolUse", "printf", "ok"},
				{"hooks", "list"},
				{"hooks", "remove", "PreToolUse", "0"},
				{"hooks", "list"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runGoldenCLISequence(t, tc.commands, tc.setup)
			assertGolden(t, tc.name+".txt", got)
		})
	}
}

func TestCLIGoldenDoctor(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, ".claude")
	sessionProjectsDir := filepath.Join(home, ".niuma-legacy", "projects")
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	t.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", sessionProjectsDir)
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("CLAUDE_CODE_AUTH_TOKEN", "")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
	t.Setenv("ANTHROPIC_BASE_URL", "")
	t.Setenv("CLAUDE_CODE_MODEL", "")
	t.Chdir(project)

	var out bytes.Buffer
	if err := Run(context.Background(), []string{"doctor"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	assertGolden(t, "doctor.json", normalizeDoctorGolden(t, out.Bytes(), home, configDir))
}

func runGoldenCLISequence(t *testing.T, commands [][]string, setup func(t *testing.T, env goldenRun)) string {
	t.Helper()
	home := t.TempDir()
	configDir := filepath.Join(home, ".claude")
	sessionProjectsDir := filepath.Join(home, ".niuma-legacy", "projects")
	project := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(project, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	t.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", sessionProjectsDir)
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("CLAUDE_CODE_AUTH_TOKEN", "")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
	t.Setenv("ANTHROPIC_BASE_URL", "")
	t.Setenv("CLAUDE_CODE_MODEL", "")
	t.Setenv("NIUMA_MYSQL_DSN", "")
	t.Setenv("MYSQL_DSN", "")
	t.Chdir(project)
	env := goldenRun{home: home, configDir: configDir, sessionProjectsDir: sessionProjectsDir, project: project}
	if setup != nil {
		setup(t, env)
	}
	var transcript strings.Builder
	for _, args := range commands {
		if transcript.Len() > 0 {
			transcript.WriteString("\n")
		}
		transcript.WriteString("$ ")
		transcript.WriteString(strings.Join(args, " "))
		transcript.WriteString("\n")
		var out, stderr bytes.Buffer
		err := Run(context.Background(), args, &out, &stderr)
		got := strings.TrimSpace(strings.Join([]string{out.String(), stderr.String()}, "\n"))
		if err != nil {
			got = strings.TrimSpace(err.Error())
		}
		got = normalizeGoalGolden(got)
		transcript.WriteString(got)
		transcript.WriteString("\n")
	}
	return strings.TrimRight(transcript.String(), "\n")
}

func runGoldenCLI(t *testing.T, args []string, setup func(t *testing.T, env goldenRun)) string {
	t.Helper()
	home := t.TempDir()
	configDir := filepath.Join(home, ".claude")
	sessionProjectsDir := filepath.Join(home, ".niuma-legacy", "projects")
	project := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(project, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	t.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", sessionProjectsDir)
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("CLAUDE_CODE_AUTH_TOKEN", "")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
	t.Setenv("ANTHROPIC_BASE_URL", "")
	t.Setenv("CLAUDE_CODE_MODEL", "")
	t.Setenv("NIUMA_MYSQL_DSN", "")
	t.Setenv("MYSQL_DSN", "")
	t.Chdir(project)
	env := goldenRun{home: home, configDir: configDir, sessionProjectsDir: sessionProjectsDir, project: project}
	if setup != nil {
		setup(t, env)
	}
	var out, stderr bytes.Buffer
	err := Run(context.Background(), args, &out, &stderr)
	got := strings.TrimSpace(strings.Join([]string{out.String(), stderr.String()}, "\n"))
	if err != nil {
		got = strings.TrimSpace(err.Error())
	}
	return got
}

func runGoldenCLIProcess(t *testing.T, args []string, setup func(t *testing.T, env goldenRun)) string {
	t.Helper()
	home := t.TempDir()
	configDir := filepath.Join(home, ".claude")
	sessionProjectsDir := filepath.Join(home, ".niuma-legacy", "projects")
	project := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(project, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	t.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", sessionProjectsDir)
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("CLAUDE_CODE_AUTH_TOKEN", "")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
	t.Setenv("ANTHROPIC_BASE_URL", "")
	t.Setenv("CLAUDE_CODE_MODEL", "")
	t.Setenv("NIUMA_MYSQL_DSN", "")
	t.Setenv("MYSQL_DSN", "")
	t.Chdir(project)
	env := goldenRun{home: home, configDir: configDir, sessionProjectsDir: sessionProjectsDir, project: project}
	if setup != nil {
		setup(t, env)
	}
	var stdout, stderr bytes.Buffer
	exitCode := 0
	if err := Run(context.Background(), args, &stdout, &stderr); err != nil {
		exitCode = 1
		stderr.WriteString(err.Error())
		stderr.WriteByte('\n')
	}
	payload := map[string]any{
		"args":      args,
		"exit_code": exitCode,
		"stdout":    stdout.String(),
		"stderr":    stderr.String(),
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(payload); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func startGoldenAnthropicServer(t *testing.T) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"id":"msg_golden","type":"message","role":"assistant","model":"test","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":5,"output_tokens":0}}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello "}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"from fake model."}}`,
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
		}, "\n")))
	}))
	t.Cleanup(server.Close)
	configureTestProvider(t, server.URL, "golden-key")
}

func assertGolden(t *testing.T, name string, got string) {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(filepath.Dir(file), "testdata", "golden", name)
	if os.Getenv("NIUMA_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, []byte(strings.TrimRight(got, "\n")+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	want := strings.TrimRight(string(data), "\n")
	got = strings.TrimRight(got, "\n")
	if got != want {
		t.Fatalf("golden mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func normalizeDoctorGolden(t *testing.T, data []byte, home, configDir string) string {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decode doctor json: %v\n%s", err, data)
	}
	doc["gitAvailable"] = "<bool>"
	if cfg, ok := doc["config"].(map[string]any); ok {
		cfg["globalSettingsPath"] = strings.ReplaceAll(cfgString(cfg["globalSettingsPath"]), home, "<HOME>")
		sessionRoot := strings.ReplaceAll(cfgString(cfg["sessionRoot"]), configDir, "<CLAUDE_CONFIG_DIR>")
		sessionRoot = strings.ReplaceAll(sessionRoot, home, "<HOME>")
		cfg["sessionRoot"] = sessionRoot
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		t.Fatalf("encode normalized doctor json: %v", err)
	}
	return out.String()
}

func cfgString(value any) string {
	text, _ := value.(string)
	return text
}

func normalizeGoldenPaths(value string) string {
	value = strings.ReplaceAll(value, "\\", "/")
	if idx := strings.Index(value, "/.claude/"); idx >= 0 {
		prefix := value[:idx]
		start := strings.LastIndex(prefix, "\"")
		if start >= 0 {
			value = value[:start+1] + "<PROJECT>" + value[idx:]
		}
	}
	return value
}

func normalizeGoalGolden(value string) string {
	goalID := regexp.MustCompile(`goal_[0-9a-f]{24}`).FindString(value)
	var sessionID string
	if goalID != "" {
		for _, field := range strings.Fields(value) {
			if session.IsValidID(strings.Trim(field, ",")) && sessionID == "" {
				sessionID = strings.Trim(field, ",")
			}
		}
		value = strings.ReplaceAll(value, goalID, "<GOAL_ID>")
	}
	if goalID != "" && sessionID != "" {
		value = strings.ReplaceAll(value, sessionID, "<SESSION_ID>")
	}
	re := regexp.MustCompile(`\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}`)
	value = re.ReplaceAllString(value, "<TIME>")
	return value
}
