package cli

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/goalcmd"
	"github.com/konglong87/super-niuma/internal/permissions"
	"github.com/konglong87/super-niuma/internal/session"
)

// AUDIT-P1-31：dispatch switch、help 字符串、completion 词表曾经是三份手写
// 清单，实测 `completion zsh` 缺 goal/goals/transcript/eval。它们现在都从
// commandTable() 派生，这组测试锁住这个约束。
func TestCommandTableDrivesHelpAndCompletion(t *testing.T) {
	var help bytes.Buffer
	printHelp(&help)
	helpText := help.String()
	completion := " " + completionCommandWords() + " "

	for _, spec := range commandTable() {
		if spec.hidden {
			continue
		}
		for _, name := range append([]string{spec.name}, spec.aliases...) {
			if !strings.Contains(completion, " "+name+" ") {
				t.Errorf("completion word list is missing %q", name)
			}
			if _, ok := lookupCommand(name); !ok {
				t.Errorf("lookupCommand(%q) failed for a table entry", name)
			}
		}
		if len(spec.usage) == 0 {
			t.Errorf("visible command %q has no usage line", spec.name)
		}
		for _, line := range spec.usage {
			if !strings.Contains(helpText, "  "+binaryName+" "+line) {
				t.Errorf("global help is missing usage line for %q: %s", spec.name, line)
			}
		}
	}

	for _, word := range strings.Fields(completionCommandWords()) {
		spec, ok := lookupCommand(word)
		if !ok {
			t.Errorf("completion advertises %q, which does not dispatch", word)
			continue
		}
		if spec.hidden {
			t.Errorf("completion advertises hidden command %q", word)
		}
	}
}

// 审计实测缺失的四个，单列出来当回归哨兵。
func TestCompletionCoversPreviouslyMissingCommands(t *testing.T) {
	completion := " " + completionCommandWords() + " "
	for _, name := range []string{"goal", "goals", "transcript", "eval"} {
		if !strings.Contains(completion, " "+name+" ") {
			t.Errorf("completion is missing %q", name)
		}
	}
}

func TestHiddenCommandsStayOutOfHelpAndCompletion(t *testing.T) {
	var help bytes.Buffer
	printHelp(&help)
	completion := " " + completionCommandWords() + " "
	for _, spec := range commandTable() {
		if !spec.hidden {
			continue
		}
		if strings.Contains(help.String(), spec.name) {
			t.Errorf("hidden command %q leaked into help", spec.name)
		}
		if strings.Contains(completion, " "+spec.name+" ") {
			t.Errorf("hidden command %q leaked into completion", spec.name)
		}
	}
}

// AUDIT-P1-31：`session --help` 以前报 `unknown session command: --help`。
// 用户对任何子命令做最标准的反射动作都必须拿到帮助。
func TestEveryVisibleCommandAnswersHelp(t *testing.T) {
	for _, spec := range commandTable() {
		if spec.hidden {
			continue
		}
		for _, flag := range []string{"--help", "-h"} {
			t.Run(spec.name+flag, func(t *testing.T) {
				var out, errOut bytes.Buffer
				if err := Run(context.Background(), []string{spec.name, flag}, &out, &errOut); err != nil {
					t.Fatalf("%s %s returned error: %v", spec.name, flag, err)
				}
				got := out.String()
				if !strings.HasPrefix(got, "Usage:\n") {
					t.Fatalf("%s %s did not print usage:\n%s", spec.name, flag, got)
				}
				if !strings.Contains(got, binaryName+" "+spec.usage[0]) {
					t.Fatalf("%s %s help lacks its usage line:\n%s", spec.name, flag, got)
				}
			})
		}
	}
}

func TestSubcommandHelpWorksAfterASubcommandWord(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := Run(context.Background(), []string{"session", "show", "--help"}, &out, &errOut); err != nil {
		t.Fatalf("session show --help returned error: %v", err)
	}
	if !strings.Contains(out.String(), "niuma session ") {
		t.Fatalf("unexpected help output:\n%s", out.String())
	}
}

func TestAliasHelpMentionsCanonicalName(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := Run(context.Background(), []string{"sessions", "--help"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Aliases: sessions") {
		t.Fatalf("alias help should name its aliases:\n%s", out.String())
	}
}

func TestGoalTopLevelHelpUsesSharedBeginnerHelp(t *testing.T) {
	for _, args := range [][]string{{"goal", "--help"}, {"goal", "-h"}, {"goals", "--help"}} {
		var out, errOut bytes.Buffer
		if err := Run(context.Background(), args, &out, &errOut); err != nil {
			t.Fatalf("%v returned error: %v", args, err)
		}
		if out.String() != goalcmd.HelpText()+"\n" {
			t.Fatalf("%v returned non-shared help:\n%s", args, out.String())
		}
	}
}

func TestUnknownCommandKeepsItsGuidance(t *testing.T) {
	var out, errOut bytes.Buffer
	err := Run(context.Background(), []string{"frobnicate"}, &out, &errOut)
	if err == nil {
		t.Fatal("unknown command must fail")
	}
	if !strings.Contains(err.Error(), "unknown command: frobnicate") ||
		!strings.Contains(err.Error(), "Run --help to see available commands") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// AUDIT-P1-28（错误质量）：Go 标准库的错误不该原样漏给用户。
func TestFlagErrorsNameTheFlagAndTheValidValues(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "server_port",
			args: []string{"server", "--port", "abc"},
			want: []string{"--port", "abc"},
		},
		{
			name: "system_prompt_file",
			args: []string{"-p", "hi", "--system-prompt-file", "definitely-missing.md"},
			want: []string{"--system-prompt-file"},
		},
		{
			name: "append_system_prompt_file",
			args: []string{"-p", "hi", "--append-system-prompt-file", "definitely-missing.md"},
			want: []string{"--append-system-prompt-file"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			err := Run(context.Background(), tc.args, &out, &errOut)
			if err == nil {
				t.Fatalf("%v must fail", tc.args)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q must mention %q", err, want)
				}
			}
			if strings.Contains(err.Error(), "strconv.Atoi") {
				t.Fatalf("stdlib error leaked to the user: %v", err)
			}
		})
	}
}

func TestInvalidPermissionModeListsTheValidValues(t *testing.T) {
	var out, errOut bytes.Buffer
	err := Run(context.Background(), []string{"-p", "hi", "--permission-mode", "wat"}, &out, &errOut)
	if err == nil {
		t.Fatal("an invalid permission mode must fail")
	}
	for _, mode := range permissions.AcceptedModes() {
		if !strings.Contains(err.Error(), mode) {
			t.Fatalf("error %q must list the accepted mode %q", err, mode)
		}
	}
}

func TestAcceptedModesAreAllNormalizable(t *testing.T) {
	for _, mode := range permissions.AcceptedModes() {
		if _, ok := permissions.NormalizeMode(mode); !ok {
			t.Fatalf("AcceptedModes advertises %q, which NormalizeMode rejects", mode)
		}
	}
}

func TestPermissionsModeCommandListsTheValidValues(t *testing.T) {
	var out bytes.Buffer
	err := permissionsCommand([]string{"mode", "wat"}, &out)
	if err == nil {
		t.Fatal("permissions mode wat must fail")
	}
	for _, mode := range []string{"allow", "ask", "deny"} {
		if !strings.Contains(err.Error(), mode) {
			t.Fatalf("error %q must list %q", err, mode)
		}
	}
}

// AUDIT-P1-31：--fork-session 以前只影响 TUI 顶部的一个显示字符串。
// 现在它真的分叉：resume 上下文照常加载，但这一轮记进新会话。
func TestForkSessionRecordsIntoANewSession(t *testing.T) {
	store := session.Store{Root: t.TempDir()}
	recorder, err := store.NewRecorder(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{Type: "user", Role: "user", Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	existing := recorder.SessionID
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	id, reuse, err := resumeRecorderSessionID(store, options{resume: existing})
	if err != nil {
		t.Fatal(err)
	}
	if !reuse || id != existing {
		t.Fatalf("plain --resume must append to the original session, got (%q, %v)", id, reuse)
	}

	id, reuse, err = resumeRecorderSessionID(store, options{resume: existing, forkSession: true})
	if err != nil {
		t.Fatal(err)
	}
	if reuse || id != "" {
		t.Fatalf("--fork-session must not reuse the original recorder, got (%q, %v)", id, reuse)
	}

	// 分叉也不能吞掉 "会话不存在"：解析仍要先跑。
	if _, _, err := resumeRecorderSessionID(store, options{resume: "nope", forkSession: true}); err == nil {
		t.Fatal("--fork-session must still reject an unknown session id")
	}
}

// AUDIT-P1-28：干净环境下第一次运行，stderr 只能有一行人话。
func TestFirstRunWithoutAPIKeyPrintsOneHumanLine(t *testing.T) {
	var runErr error
	var stdout, stderr bytes.Buffer
	captured := captureProcessStderr(t, func() {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("NIUMA_LOG_LEVEL", "")
		t.Setenv("LOG_LEVEL", "")
		t.Setenv("ANTHROPIC_API_KEY", "")
		t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
		t.Setenv("CLAUDE_CODE_AUTH_TOKEN", "")
		t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
		t.Setenv("ANTHROPIC_BASE_URL", "")
		t.Chdir(t.TempDir())
		runErr = Run(context.Background(), []string{"-p", "hi"}, &stdout, &stderr)
	})
	if runErr == nil {
		t.Fatal("a run without credentials must fail")
	}
	// main.go 把这个 error 原样写到 stderr，所以它就是用户看到的全部输出。
	got := strings.TrimRight(captured+stderr.String()+runErr.Error(), "\n")
	assertGolden(t, "first_run_no_api_key.txt", got)
	if strings.Count(got, "\n") != 0 {
		t.Fatalf("first-run failure must be a single line, got:\n%s", got)
	}
}

// 模型请求真的失败时（审计里那 19 行的来源），stderr 默认仍必须干净。
func TestFailingModelRequestKeepsStderrQuiet(t *testing.T) {
	var runErr error
	var stdout, stderr bytes.Buffer
	captured := captureProcessStderr(t, func() {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("NIUMA_LOG_LEVEL", "")
		t.Setenv("LOG_LEVEL", "")
		t.Chdir(t.TempDir())
		startFailingAnthropicServer(t)
		runErr = Run(context.Background(), []string{"-p", "hi"}, &stdout, &stderr)
	})
	if runErr == nil {
		t.Fatal("a 401 from the model API must fail the run")
	}
	if captured != "" {
		t.Fatalf("CLI stderr must stay quiet by default, got:\n%s", captured)
	}
}

func TestFailingModelRequestWithLogLevelErrorHasNoStacktrace(t *testing.T) {
	var stdout, stderr bytes.Buffer
	captured := captureProcessStderr(t, func() {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("LOG_LEVEL", "")
		t.Setenv("NIUMA_LOG_LEVEL", "error")
		t.Chdir(t.TempDir())
		startFailingAnthropicServer(t)
		_ = Run(context.Background(), []string{"-p", "hi"}, &stdout, &stderr)
	})
	if !strings.Contains(captured, `"level":"error"`) {
		t.Fatalf("an explicit error level must still emit diagnostics, got:\n%s", captured)
	}
	if strings.Contains(captured, "stacktrace") {
		t.Fatalf("diagnostics must not carry Go stacktraces, got:\n%s", captured)
	}
}

func TestUnknownLogLevelIsReportedNotIgnored(t *testing.T) {
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("NIUMA_LOG_LEVEL", "fatalx")
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"status"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("an unknown NIUMA_LOG_LEVEL must be reported")
	}
	if !strings.Contains(err.Error(), "fatalx") || !strings.Contains(err.Error(), "silent") {
		t.Fatalf("error must name the bad value and list the valid ones: %v", err)
	}
}

// server 与后台进程保留结构化 JSON 日志，安静化只针对交互/CLI 路径。
func TestServerAndDaemonCommandsKeepStructuredLogs(t *testing.T) {
	want := map[string]bool{
		"server":            true,
		"image-worker":      true,
		"__background-run":  true,
		"__goal-run":        true,
		"__loop-run":        true,
		"__schedule-daemon": true,
		"__schedule-run":    true,
	}
	for _, spec := range commandTable() {
		if spec.structuredLogs != want[spec.name] {
			t.Errorf("command %q structuredLogs = %v, want %v", spec.name, spec.structuredLogs, want[spec.name])
		}
	}
}

func startFailingAnthropicServer(t *testing.T) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`))
	}))
	t.Cleanup(server.Close)
	configureTestProvider(t, server.URL, "test-key")
}

// captureProcessStderr 换掉真正的 os.Stderr。zap 在 Build 时绑定 sink，
// 所以必须把被测代码整段包进来 —— 这也正是 CLI 日志的实际去处。
func captureProcessStderr(t *testing.T, fn func()) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stderr
	os.Stderr = writer
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, reader)
		done <- buf.String()
	}()
	fn()
	os.Stderr = original
	_ = writer.Close()
	out := <-done
	_ = reader.Close()
	return out
}
