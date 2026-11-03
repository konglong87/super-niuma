package query

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/memory"
	"github.com/konglong87/super-niuma/internal/tools"
	"github.com/konglong87/super-niuma/internal/tools/fileread"
	"github.com/konglong87/super-niuma/internal/tools/filewrite"
)

const fileMemoryFixtureBody = "SYNTHETIC_BODY_ONLY_MEMORY_9274"
const fileMemoryDecoyBody = "UNRELATED_FILE_MUST_NOT_PRELOAD_6401"
const fileMemoryChildWorkspaceEnv = "NIUMA_TEST_FILE_MEMORY_WORKSPACE"

func TestCodingFileMemoryWriteAndColdProcess(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home, workspace := filepath.Join(base, "home"), filepath.Join(base, "project")
	for _, dir := range []string{home, workspace} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	configLink := filepath.Join(base, "home-link")
	if err := os.Symlink(home, configLink); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	config := filepath.Join(configLink, "config")
	t.Setenv("HOME", home)
	for _, key := range []string{"NIUMA_CONFIG_DIR", "CLAUDE_CONFIG_DIR"} {
		t.Setenv(key, config)
	}
	memdir := memory.ClaudeCodeProjectMemoryDir(workspace)
	session := New(&systemStreamer{}, tools.NewRegistry(filewrite.New(), fileread.New()), Options{Model: "test", CWD: workspace, PromptMode: "code", MaxTurns: 1})
	files := map[string]string{
		"topic.md":  "---\nname: Deployment policy\ndescription: deployment verification checklist\nmetadata:\n  type: project\n---\n" + fileMemoryFixtureBody + "\n",
		"decoy.md":  "---\nname: Build\ndescription: registry convention\n---\n" + fileMemoryDecoyBody + "\n",
		"MEMORY.md": "- [Build](decoy.md) - registry; [Deployment policy](topic.md) - release verification convention\n",
	}
	for _, name := range []string{"topic.md", "decoy.md", "MEMORY.md"} {
		input, _ := json.Marshal(map[string]string{"file_path": filepath.Join(memdir, name), "content": files[name]})
		result := filewrite.New().Run(context.Background(), input, tools.Context{CWD: workspace, WritableRoots: session.options.WritableRoots})
		if result.IsError {
			t.Fatalf("actual Write of %s: %s", name, result.Content)
		}
		got, err := os.ReadFile(filepath.Join(memdir, name))
		if err != nil || string(got) != files[name] {
			t.Fatalf("physical file %s: %v", name, err)
		}
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(binary, "-test.run=^TestCodingFileMemoryColdProcessChild$")
	child.Env = append(os.Environ(), fileMemoryChildWorkspaceEnv+"="+workspace)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("cold-process checks: %v\n%s", err, output)
	}
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(memdir, name))
		if err != nil || string(got) != want {
			t.Fatalf("read-only checks changed %s: %v", name, err)
		}
	}
}

func TestCodingFileMemoryColdProcessChild(t *testing.T) {
	workspace := os.Getenv(fileMemoryChildWorkspaceEnv)
	if workspace == "" {
		t.Skip("invoked only by isolated parent test")
	}
	check := func(cwd, mode, prompt string, wantBody, wantIndex bool) {
		t.Helper()
		streamer := &systemStreamer{}
		session := New(streamer, tools.NewRegistry(fileread.New()), Options{Model: "test", CWD: cwd, PromptMode: mode, MaxTurns: 1})
		if _, err := session.Run(context.Background(), prompt, io.Discard); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(struct {
			Systems  []string
			Blocks   any
			Messages any
		}{streamer.systems, streamer.blocks, streamer.messages})
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		if strings.Contains(text, fileMemoryDecoyBody) {
			t.Fatal("another link's index hint preloaded the decoy body")
		}
		if got := strings.Contains(text, fileMemoryFixtureBody); got != wantBody {
			t.Fatalf("body preload for %q: %t want %t", prompt, got, wantBody)
		}
		if got := strings.Contains(text, "[Deployment policy](topic.md)"); got != wantIndex {
			t.Fatalf("index preload for %q: %t want %t", prompt, got, wantIndex)
		}
	}
	check(workspace, "code", "deployment verification", true, true)
	check(workspace, "code", "release verification", true, true)
	check(workspace, "code", "What precautions should we take before shipping?", false, true)
	input, _ := json.Marshal(map[string]string{"file_path": filepath.Join(memory.ClaudeCodeProjectMemoryDir(workspace), "topic.md")})
	result := fileread.New().Run(context.Background(), input, tools.Context{CWD: workspace})
	if result.IsError || !strings.Contains(result.Content, fileMemoryFixtureBody) {
		t.Fatalf("Read fallback: %s", result.Content)
	}
	check(workspace, "code", "Ignore memory. deployment verification", false, true)
	check(workspace, "chat", "deployment verification", false, false)
	other := filepath.Join(filepath.Dir(workspace), "other-project")
	if err := os.MkdirAll(other, 0700); err != nil {
		t.Fatal(err)
	}
	check(other, "code", "deployment verification", false, false)
}
