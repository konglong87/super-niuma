package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/files"
)

func TestDefaultStoreUsesGolangCCTranscriptNamespace(t *testing.T) {
	home := t.TempDir()
	legacyClaudeRoot := filepath.Join(t.TempDir(), ".claude")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", legacyClaudeRoot)
	t.Setenv("NIUMA_CONFIG_DIR", "")
	t.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", "")

	store := DefaultStore()
	wantConfigRoot := filepath.Join(home, ".niuma")
	wantProjectsRoot := filepath.Join(wantConfigRoot, "projects")
	if store.Root != wantConfigRoot {
		t.Fatalf("Root = %q, want %q", store.Root, wantConfigRoot)
	}
	if store.TranscriptProjectsRoot != wantProjectsRoot {
		t.Fatalf("TranscriptProjectsRoot = %q, want %q", store.TranscriptProjectsRoot, wantProjectsRoot)
	}

	recorder, err := store.NewRecorderWithID("/tmp/example", "11111111-1111-4111-8111-111111111111")
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(recorder.Path, wantProjectsRoot+string(os.PathSeparator)) {
		t.Fatalf("recorder path = %q, want under %q", recorder.Path, wantProjectsRoot)
	}
	if strings.HasPrefix(recorder.Path, legacyClaudeRoot+string(os.PathSeparator)) {
		t.Fatalf("recorder path = %q, must not be controlled by CLAUDE_CONFIG_DIR %q", recorder.Path, legacyClaudeRoot)
	}
}

func TestOpenOrCreateRecorderReopensExistingV2History(t *testing.T) {
	store := Store{TranscriptProjectsRoot: t.TempDir(), SchemaV2: true}
	const id = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const cwd = "/workspace/session-replay"

	first, created, err := store.OpenOrCreateRecorder(cwd, id)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("first OpenOrCreateRecorder call must create the session")
	}
	for _, entry := range []Entry{
		{Type: "message", Role: "user", Content: "first prompt"},
		{Type: "tool_call", ToolID: "tool-1", ToolName: "Read", Content: `{"path":"notes.md"}`},
		{Type: "tool_result", ToolID: "tool-1", Content: "tool history"},
		{Type: "compact_summary", Content: "compact history"},
	} {
		if err := first.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, created, err := store.OpenOrCreateRecorder(cwd, id)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("second OpenOrCreateRecorder call must reopen the existing session")
	}
	if second.SessionID != id {
		t.Fatalf("session id = %q, want %q", second.SessionID, id)
	}
	if err := second.Append(Entry{Type: "message", Role: "assistant", Content: "continued answer"}); err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}

	summary, ok, err := store.Find(id)
	if err != nil || !ok {
		t.Fatalf("Find() ok=%v err=%v", ok, err)
	}
	entries, err := LoadConversation(summary.Path)
	if err != nil {
		t.Fatal(err)
	}
	text := transcriptContent(entries)
	for _, want := range []string{"first prompt", "tool history", "compact history", "continued answer"} {
		if !strings.Contains(text, want) {
			t.Fatalf("reopened history missing %q:\n%s", want, text)
		}
	}
}

func TestRecorderSerializesConcurrentSessionAppends(t *testing.T) {
	store := Store{TranscriptProjectsRoot: t.TempDir(), SchemaV2: true}
	const id = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	const appendCount = 40

	first, created, err := store.OpenOrCreateRecorder("/workspace/concurrent", id)
	if err != nil || !created {
		t.Fatalf("first OpenOrCreateRecorder created=%v err=%v", created, err)
	}
	second, created, err := store.OpenOrCreateRecorder("/workspace/concurrent", id)
	if err != nil || created {
		t.Fatalf("second OpenOrCreateRecorder created=%v err=%v", created, err)
	}
	defer first.Close()
	defer second.Close()

	errs := make(chan error, appendCount*2)
	var wg sync.WaitGroup
	for i := 0; i < appendCount; i++ {
		for _, recorder := range []*Recorder{first, second} {
			wg.Add(1)
			go func(recorder *Recorder) {
				defer wg.Done()
				errs <- recorder.Append(Entry{Type: "message", Role: "assistant", Content: "concurrent"})
			}(recorder)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	summary, ok, err := store.Find(id)
	if err != nil || !ok {
		t.Fatalf("Find() ok=%v err=%v", ok, err)
	}
	entries, err := Load(summary.Path)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(entries); got != 1+appendCount*2 {
		t.Fatalf("entry count = %d, want %d", got, 1+appendCount*2)
	}
	if chain := CurrentChain(entries); len(chain) != appendCount*2 {
		t.Fatalf("current chain length = %d, want %d", len(chain), appendCount*2)
	}
}

func TestRecorderSerializesConcurrentProcesses(t *testing.T) {
	root := t.TempDir()
	store := Store{TranscriptProjectsRoot: root, SchemaV2: true}
	const id = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	recorder, created, err := store.OpenOrCreateRecorder("/workspace/process", id)
	if err != nil || !created {
		t.Fatalf("initial OpenOrCreateRecorder created=%v err=%v", created, err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	const processCount = 8
	commands := make([]*exec.Cmd, 0, processCount)
	for i := 0; i < processCount; i++ {
		cmd := exec.Command(os.Args[0], "-test.run=TestRecorderConcurrentProcessHelper")
		cmd.Env = append(os.Environ(),
			"NIUMA_SESSION_HELPER=1",
			"NIUMA_SESSION_ROOT="+root,
			"NIUMA_SESSION_ID="+id,
		)
		commands = append(commands, cmd)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
	}
	for _, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("helper process failed: %v", err)
		}
	}

	summary, ok, err := store.Find(id)
	if err != nil || !ok {
		t.Fatalf("Find() ok=%v err=%v", ok, err)
	}
	entries, err := Load(summary.Path)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(entries); got != 1+processCount {
		t.Fatalf("entry count = %d, want %d", got, 1+processCount)
	}
	if got := len(CurrentChain(entries)); got != processCount {
		t.Fatalf("current chain length = %d, want %d", got, processCount)
	}
}

func TestRecorderConcurrentProcessHelper(t *testing.T) {
	if os.Getenv("NIUMA_SESSION_HELPER") != "1" {
		return
	}
	store := Store{
		TranscriptProjectsRoot: os.Getenv("NIUMA_SESSION_ROOT"),
		SchemaV2:               true,
	}
	recorder, _, err := store.OpenOrCreateRecorder("/workspace/process", os.Getenv("NIUMA_SESSION_ID"))
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{Type: "message", Role: "assistant", Content: "process"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
}

func transcriptContent(entries []Entry) string {
	var parts []string
	for _, entry := range entries {
		parts = append(parts, entry.Content)
	}
	return strings.Join(parts, "\n")
}

func TestDefaultStoreReadsLegacyTranscriptNamespace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_CONFIG_DIR", "")
	t.Setenv("NIUMA_CONFIG_DIR", "")
	t.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", "")
	t.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", "")

	legacyStore := Store{Root: filepath.Join(home, ".niuma-legacy")}
	recorder, err := legacyStore.NewRecorderWithID("/tmp/legacy", "33333333-3333-4333-8333-333333333333")
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{Type: "message", Role: "user", Content: "legacy session"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	store := DefaultStore()
	found, ok, err := store.Find(recorder.SessionID)
	if err != nil || !ok {
		t.Fatalf("Find() ok=%v err=%v", ok, err)
	}
	if found.Path != recorder.Path || found.Title != "legacy session" {
		t.Fatalf("legacy summary = %+v", found)
	}
}

func TestDefaultStoreCopiesLegacyTranscriptBeforeMutation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_CONFIG_DIR", "")
	t.Setenv("NIUMA_CONFIG_DIR", "")
	t.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", "")
	t.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", "")

	legacyStore := Store{Root: filepath.Join(home, ".niuma-legacy")}
	recorder, err := legacyStore.NewRecorderWithID("/tmp/legacy-write", "44444444-4444-4444-8444-444444444444")
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{Type: "message", Role: "user", Content: "legacy original"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	legacyPath := recorder.Path
	legacyBefore, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}

	store := DefaultStore()
	if ok, err := store.Rename(recorder.SessionID, "canonical title"); err != nil || !ok {
		t.Fatalf("Rename() ok=%v err=%v", ok, err)
	}
	summary, ok, err := store.Find(recorder.SessionID)
	if err != nil || !ok {
		t.Fatalf("Find() ok=%v err=%v", ok, err)
	}
	wantRoot := filepath.Join(home, ".niuma", "projects")
	if !strings.HasPrefix(summary.Path, wantRoot+string(os.PathSeparator)) {
		t.Fatalf("mutated transcript path = %q, want under %q", summary.Path, wantRoot)
	}
	legacyAfter, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(legacyAfter) != string(legacyBefore) {
		t.Fatalf("legacy transcript was modified\nbefore: %s\nafter: %s", legacyBefore, legacyAfter)
	}
	if summary.Title != "canonical title" {
		t.Fatalf("canonical title = %q", summary.Title)
	}
}

func TestDefaultStoreCopiesLegacyTranscriptBeforeResume(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_CONFIG_DIR", "")
	t.Setenv("NIUMA_CONFIG_DIR", "")
	t.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", "")
	t.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", "")

	legacyStore := Store{Root: filepath.Join(home, ".niuma-legacy")}
	recorder, err := legacyStore.NewRecorderWithID("/tmp/legacy-resume", "55555555-5555-4555-8555-555555555555")
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{Type: "message", Role: "user", Content: "legacy original"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	legacyBefore, err := os.ReadFile(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}

	resumed, ok, err := DefaultStore().OpenRecorder(recorder.SessionID)
	if err != nil || !ok {
		t.Fatalf("OpenRecorder() ok=%v err=%v", ok, err)
	}
	if err := resumed.Append(Entry{Type: "message", Role: "assistant", Content: "canonical continuation"}); err != nil {
		t.Fatal(err)
	}
	if err := resumed.Close(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resumed.Path, string(os.PathSeparator)+".niuma"+string(os.PathSeparator)) {
		t.Fatalf("resumed path = %q", resumed.Path)
	}
	legacyAfter, err := os.ReadFile(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(legacyAfter) != string(legacyBefore) {
		t.Fatalf("legacy transcript was modified during resume")
	}
}

func TestDefaultStoreUsesConfiguredIdentityConfigDirName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_CONFIG_DIR", "")
	t.Setenv("NIUMA_CONFIG_DIR_NAME", ".go-code")
	t.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", "")

	store := DefaultStore()
	wantRoot := filepath.Join(home, ".go-code")
	if store.Root != wantRoot {
		t.Fatalf("Root = %q, want %q", store.Root, wantRoot)
	}
	if store.TranscriptProjectsRoot != filepath.Join(wantRoot, "projects") {
		t.Fatalf("TranscriptProjectsRoot = %q", store.TranscriptProjectsRoot)
	}
}

func TestDefaultStoreUsesGoClaudeConfigDirForTranscriptProjects(t *testing.T) {
	configRoot := filepath.Join(t.TempDir(), "go-config")
	t.Setenv("NIUMA_CONFIG_DIR", configRoot)
	t.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", "")
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(t.TempDir(), "legacy-claude"))

	store := DefaultStore()
	wantProjectsRoot := filepath.Join(configRoot, "projects")
	if store.Root != configRoot {
		t.Fatalf("Root = %q, want %q", store.Root, configRoot)
	}
	if store.TranscriptProjectsRoot != wantProjectsRoot {
		t.Fatalf("TranscriptProjectsRoot = %q, want %q", store.TranscriptProjectsRoot, wantProjectsRoot)
	}
}

func TestDefaultStoreTranscriptProjectsDirIsDirectProjectsRoot(t *testing.T) {
	configRoot := filepath.Join(t.TempDir(), "go-config")
	projectsRoot := filepath.Join(t.TempDir(), "custom-projects")
	t.Setenv("NIUMA_CONFIG_DIR", configRoot)
	t.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", projectsRoot)

	store := DefaultStore()
	if store.Root != configRoot {
		t.Fatalf("Root = %q, want %q", store.Root, configRoot)
	}
	if store.TranscriptProjectsRoot != projectsRoot {
		t.Fatalf("TranscriptProjectsRoot = %q, want %q", store.TranscriptProjectsRoot, projectsRoot)
	}

	recorder, err := store.NewRecorderWithID("/tmp/direct-root", "22222222-2222-4222-8222-222222222222")
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	wantDir := filepath.Join(projectsRoot, ProjectSlug("/tmp/direct-root"))
	if filepath.Dir(recorder.Path) != wantDir {
		t.Fatalf("recorder dir = %q, want %q", filepath.Dir(recorder.Path), wantDir)
	}
	if strings.Contains(recorder.Path, string(os.PathSeparator)+"projects"+string(os.PathSeparator)+"projects"+string(os.PathSeparator)) {
		t.Fatalf("recorder path contains projects/projects: %q", recorder.Path)
	}
}

func TestRecorderWritesJSONL(t *testing.T) {
	store := Store{Root: t.TempDir()}
	recorder, err := store.NewRecorder("/tmp/example")
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{Type: "message", Role: "user", Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{Type: "tool_result", ToolID: "toolu_1", ToolName: "Read", Content: "ok"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{Type: "content_replacement", Replacements: []ReplacementRecord{{
		Kind:        "tool-result",
		ToolUseID:   "toolu_1",
		Replacement: "<persisted-output>preview</persisted-output>",
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{Type: "usage", Model: "claude-sonnet-4-5-20250929", InputTokens: 10, OutputTokens: 5}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	entries, err := Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Fatalf("entries = %d, want 4", len(entries))
	}
	if entries[0].Role != "user" || entries[1].ToolName != "Read" {
		t.Fatalf("entries = %+v", entries)
	}
	if len(entries[2].Replacements) != 1 || entries[2].Replacements[0].ToolUseID != "toolu_1" {
		t.Fatalf("content replacement entry = %+v", entries[2])
	}
	if filepath.Base(recorder.Path) != recorder.SessionID+".jsonl" {
		t.Fatalf("path = %s session = %s", recorder.Path, recorder.SessionID)
	}
	found, ok, err := store.Find(recorder.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || found.Path != recorder.Path {
		t.Fatalf("found=%+v ok=%v", found, ok)
	}
	if found.Title != "hello" {
		t.Fatalf("title = %q", found.Title)
	}
	usage, err := store.Usage()
	if err != nil {
		t.Fatal(err)
	}
	if usage.InputTokens != 10 || usage.OutputTokens != 5 || usage.TotalTokens != 15 {
		t.Fatalf("usage = %+v", usage)
	}
	if usage.Models["claude-sonnet-4-5-20250929"].CostUSD <= 0 || usage.CostUSD <= 0 {
		t.Fatalf("usage cost = %+v", usage)
	}
	ok, err = store.Delete(recorder.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("delete returned false")
	}
	summaries, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 0 {
		t.Fatalf("summaries after delete = %+v", summaries)
	}
}

func TestFindTranscriptUnderReturnsExactNestedSession(t *testing.T) {
	root := t.TempDir()
	wantID := "11111111-1111-4111-8111-111111111111"
	wantPath := filepath.Join(root, "project-a", wantID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(wantPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wantPath, []byte(`{"type":"message","role":"user","content":"target"}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "unrelated.jsonl"), []byte("not-json\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, wantID+"-suffix.jsonl"), []byte("not-json\n"), 0600); err != nil {
		t.Fatal(err)
	}

	got, ok, err := findTranscriptUnder(root, wantID)
	if err != nil || !ok {
		t.Fatalf("findTranscriptUnder() ok=%v err=%v", ok, err)
	}
	if got.Path != wantPath || got.SessionID != wantID || got.Title != "target" {
		t.Fatalf("summary = %+v", got)
	}
}

func TestFindTranscriptUnderReturnsNewestDuplicateSession(t *testing.T) {
	root := t.TempDir()
	const sessionID = "11111111-1111-4111-8111-111111111111"
	oldPath := filepath.Join(root, "a-old", sessionID+".jsonl")
	newPath := filepath.Join(root, "z-new", sessionID+".jsonl")
	for path, content := range map[string]string{oldPath: "old", newPath: "new"} {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`{"type":"message","role":"user","content":"`+content+`"}`+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	oldTime := time.Date(2026, 7, 15, 1, 0, 0, 0, time.UTC)
	newTime := oldTime.Add(time.Hour)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newPath, newTime, newTime); err != nil {
		t.Fatal(err)
	}

	got, ok, err := findTranscriptUnder(root, sessionID)
	if err != nil || !ok {
		t.Fatalf("findTranscriptUnder() ok=%v err=%v", ok, err)
	}
	if got.Path != newPath || got.Title != "new" || !got.ModTime.Equal(newTime) {
		t.Fatalf("summary = %+v, want newest path %q", got, newPath)
	}
}

func TestFindTranscriptUnderHandlesMissingRootAndSession(t *testing.T) {
	const sessionID = "11111111-1111-4111-8111-111111111111"
	for _, root := range []string{filepath.Join(t.TempDir(), "missing"), t.TempDir()} {
		got, ok, err := findTranscriptUnder(root, sessionID)
		if err != nil || ok || got.Path != "" {
			t.Fatalf("findTranscriptUnder(%q) = %+v, %v, %v", root, got, ok, err)
		}
	}
}

func TestFindTranscriptUnderRejectsUnsafeSessionIDs(t *testing.T) {
	for _, sessionID := range []string{"", "   ", "../session", `..\session`, "session/id", `session\id`, "session..id"} {
		if _, ok, err := findTranscriptUnder(t.TempDir(), sessionID); err == nil || ok {
			t.Fatalf("findTranscriptUnder(%q) ok=%v err=%v, want invalid id error", sessionID, ok, err)
		}
	}
}

func BenchmarkStoreFindBySessionID(b *testing.B) {
	root := b.TempDir()
	const targetID = "11111111-1111-4111-8111-111111111111"
	for i := 0; i < 1000; i++ {
		path := filepath.Join(root, "project", fmt.Sprintf("decoy-%04d.jsonl", i))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("not-json\n"), 0600); err != nil {
			b.Fatal(err)
		}
	}
	target := filepath.Join(root, "project", targetID+".jsonl")
	if err := os.WriteFile(target, []byte(`{"type":"message","role":"user","content":"target"}`+"\n"), 0600); err != nil {
		b.Fatal(err)
	}
	store := Store{TranscriptProjectsRoot: root}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok, err := store.Find(targetID); err != nil || !ok {
			b.Fatalf("Find() ok=%v err=%v", ok, err)
		}
	}
}

func TestListForCWDOnlyReturnsCurrentProjectSessions(t *testing.T) {
	store := Store{Root: t.TempDir()}
	current, err := store.NewRecorderWithID("/tmp/current-project", "11111111-1111-4111-8111-111111111111")
	if err != nil {
		t.Fatal(err)
	}
	if err := current.Append(Entry{Type: "message", Role: "user", Content: "current"}); err != nil {
		t.Fatal(err)
	}
	if err := current.Close(); err != nil {
		t.Fatal(err)
	}
	other, err := store.NewRecorderWithID("/tmp/other-project", "22222222-2222-4222-8222-222222222222")
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Append(Entry{Type: "message", Role: "user", Content: "other"}); err != nil {
		t.Fatal(err)
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}

	summaries, err := store.ListForCWD("/tmp/current-project")
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 || summaries[0].SessionID != current.SessionID {
		t.Fatalf("summaries = %+v", summaries)
	}
}

func TestUsageTracksPromptCacheTokens(t *testing.T) {
	store := Store{Root: t.TempDir()}
	recorder, err := store.NewRecorder("/tmp/cache")
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{
		Type:                                "usage",
		Model:                               "claude-sonnet-4-5-20250929",
		InputTokens:                         10,
		CacheCreationInputTokens:            20,
		CacheReadInputTokens:                30,
		CacheCreationEphemeral1hInputTokens: 12,
		CacheCreationEphemeral5mInputTokens: 8,
		OutputTokens:                        5,
	}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	usage, err := store.Usage()
	if err != nil {
		t.Fatal(err)
	}
	if usage.InputTokens != 60 || usage.CacheCreationInputTokens != 20 || usage.CacheReadInputTokens != 30 || usage.CacheCreationEphemeral1hInputTokens != 12 || usage.CacheCreationEphemeral5mInputTokens != 8 || usage.TotalTokens != 65 {
		t.Fatalf("usage = %+v", usage)
	}
	model := usage.Models["claude-sonnet-4-5-20250929"]
	if model.InputTokens != 60 || model.CacheCreationInputTokens != 20 || model.CacheReadInputTokens != 30 || model.CacheCreationEphemeral1hInputTokens != 12 || model.CacheCreationEphemeral5mInputTokens != 8 || model.TotalTokens != 65 {
		t.Fatalf("model usage = %+v", model)
	}
}

func TestRecorderWithIDAndName(t *testing.T) {
	store := Store{Root: t.TempDir()}
	id := "11111111-1111-4111-8111-111111111111"
	recorder, err := store.NewRecorderWithID("/tmp/example", id)
	if err != nil {
		t.Fatal(err)
	}
	if recorder.SessionID != id || filepath.Base(recorder.Path) != id+".jsonl" {
		t.Fatalf("recorder = %+v", recorder)
	}
	if err := recorder.Append(Entry{Type: "session", Name: "Demo Session"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{Type: "message", Role: "user", Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	found, ok, err := store.Find(id)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || found.Title != "Demo Session" {
		t.Fatalf("found = %+v ok=%v", found, ok)
	}
	if _, err := store.NewRecorderWithID("/tmp/example", "not-a-uuid"); err == nil {
		t.Fatal("expected invalid id error")
	}
	ok, err = store.Rename(id, "Renamed")
	if err != nil || !ok {
		t.Fatalf("rename ok=%v err=%v", ok, err)
	}
	found, ok, err = store.Find(id)
	if err != nil || !ok || found.Title != "Renamed" {
		t.Fatalf("renamed found=%+v ok=%v err=%v", found, ok, err)
	}
	results, err := store.Search("renamed")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].SessionID != id {
		t.Fatalf("results = %+v", results)
	}
}

func TestSessionSearchContent(t *testing.T) {
	store := Store{Root: t.TempDir()}
	recorder, err := store.NewRecorder(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{Type: "message", Role: "user", Content: "please inspect the router"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{Type: "tool_call", ToolName: "Grep", Content: "gin router"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	results, err := store.Search("router")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].SessionID != recorder.SessionID {
		t.Fatalf("results = %+v", results)
	}
	results, err = store.Search("missing")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("results = %+v", results)
	}
}

func TestProjectSlug(t *testing.T) {
	got := ProjectSlug("/Users/example/my repo")
	if got != "Users-example-my-repo" {
		t.Fatalf("ProjectSlug() = %q", got)
	}
}

func TestExportMarkdown(t *testing.T) {
	md := ExportMarkdown([]Entry{
		{Type: "message", Role: "user", Content: "hello"},
		{Type: "tool_call", ToolName: "Read", Content: `{"file_path":"README.md"}`},
		{Type: "recap_summary", Content: "本次会话目标：测试 recap"},
	})
	if !strings.Contains(md, "## User") || !strings.Contains(md, "Tool Call: Read") || !strings.Contains(md, "※ recap:") {
		t.Fatalf("markdown = %s", md)
	}
}

func TestRecorderAutoIDsRecapSummary(t *testing.T) {
	store := Store{Root: t.TempDir()}
	recorder, err := store.NewRecorder(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{Type: "recap_summary", Content: "recap"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ID == "" {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestCompactAppendsSummary(t *testing.T) {
	store := Store{Root: t.TempDir()}
	recorder, err := store.NewRecorder(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{Type: "message", Role: "user", Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	entry, err := Compact(recorder.Path, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Type != "compact_summary" || !strings.Contains(entry.Content, "hello") {
		t.Fatalf("entry = %+v", entry)
	}
	entries, err := Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	if entries[len(entries)-1].Type != "compact_summary" {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestCheckpointAndRewindRestoresFileChanges(t *testing.T) {
	store := Store{Root: t.TempDir()}
	project := t.TempDir()
	target := filepath.Join(project, "note.txt")
	if err := os.WriteFile(target, []byte("before"), 0644); err != nil {
		t.Fatal(err)
	}
	recorder, err := store.NewRecorderWithID(project, "44444444-4444-4444-8444-444444444444")
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{Type: "message", Role: "user", Content: "start"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.Checkpoint(recorder.SessionID, "before-edit"); err != nil || !ok {
		t.Fatalf("checkpoint ok=%v err=%v", ok, err)
	}
	fileChange := `{"path":"` + target + `","before":"before","before_exists":true,"after":"after","after_exists":true}`
	recorder, err = store.NewRecorderWithID(project, recorder.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{Type: "file_change", Content: fileChange}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{Type: "message", Role: "assistant", Content: "changed"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("after"), 0644); err != nil {
		t.Fatal(err)
	}
	result, ok, err := store.Rewind(recorder.SessionID, "before-edit")
	if err != nil || !ok {
		t.Fatalf("rewind ok=%v err=%v", ok, err)
	}
	if result.FilesRestored != 1 || result.EntriesRemoved != 2 {
		t.Fatalf("result = %+v", result)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "before" {
		t.Fatalf("data = %q", data)
	}
	entries, err := Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 || entries[1].Type != "checkpoint" || entries[2].Type != "rewind" {
		t.Fatalf("entries = %+v", entries)
	}
}

// A turn that edits the same file several times records one recoverable
// file_change (turn-start state) followed by lite superseded entries carrying no
// body. Rewind must restore the turn-start state and ignore the lite entries.
func TestRewindIgnoresSupersededLiteFileChanges(t *testing.T) {
	store := Store{Root: t.TempDir()}
	project := t.TempDir()
	target := filepath.Join(project, "note.txt")
	if err := os.WriteFile(target, []byte("v1"), 0644); err != nil {
		t.Fatal(err)
	}
	recorder, err := store.NewRecorderWithID(project, "55555555-5555-4555-8555-555555555555")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recorder.Checkpoint("turn-start", ""); err != nil {
		t.Fatal(err)
	}
	// First edit: recoverable, holds the turn-start body "v1".
	recoverable := `{"path":"` + target + `","before":"v1","before_exists":true,"after":"v2","after_exists":true}`
	if err := recorder.Append(Entry{Type: "file_change", Content: recoverable}); err != nil {
		t.Fatal(err)
	}
	// Later edits same turn: lite, superseded, no recoverable body.
	lite := `{"path":"` + target + `","before_exists":true,"after_exists":true,"before_superseded":true,"before_sha256":"deadbeef","after_sha256":"cafe"}`
	if err := recorder.Append(Entry{Type: "file_change", Content: lite}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{Type: "file_change", Content: lite}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	// Working file has drifted to the final intermediate state.
	if err := os.WriteFile(target, []byte("v4"), 0644); err != nil {
		t.Fatal(err)
	}

	result, ok, err := store.Rewind(recorder.SessionID, "turn-start")
	if err != nil || !ok {
		t.Fatalf("rewind ok=%v err=%v", ok, err)
	}
	// Only the one recoverable path is restored despite three file_change entries.
	if result.FilesRestored != 1 {
		t.Fatalf("FilesRestored = %d, want 1", result.FilesRestored)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "v1" {
		t.Fatalf("file restored to %q, want turn-start %q", data, "v1")
	}
}

func TestCheckpointAndRewindRestoresFileChangeSnapshotPath(t *testing.T) {
	store := Store{Root: t.TempDir()}
	project := t.TempDir()
	target := filepath.Join(project, "large.txt")
	snapshot := filepath.Join(project, "snapshot.txt")
	if err := os.WriteFile(target, []byte("after"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(snapshot, []byte("before from snapshot"), 0644); err != nil {
		t.Fatal(err)
	}
	recorder, err := store.NewRecorderWithID(project, "14141414-1414-4414-8414-141414141414")
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{Type: "message", Role: "user", Content: "start"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.Checkpoint(recorder.SessionID, "before-large-edit"); err != nil || !ok {
		t.Fatalf("checkpoint ok=%v err=%v", ok, err)
	}
	change, _ := json.Marshal(fileChangeSnapshot{Path: target, BeforeExists: true, BeforeSnapshotPath: snapshot, After: "after", AfterExists: true})
	recorder, err = store.NewRecorderWithID(project, recorder.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{Type: "file_change", Content: string(change)}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.Rewind(recorder.SessionID, "before-large-edit"); err != nil || !ok {
		t.Fatalf("rewind ok=%v err=%v", ok, err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "before from snapshot" {
		t.Fatalf("data = %q", data)
	}
}

func TestRewindRestoresOriginalFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes are not supported on Windows")
	}
	tests := []struct {
		name            string
		beforeMode      os.FileMode
		beforeModeKnown bool
		useSnapshot     bool
		before          string
		currentMode     os.FileMode
	}{
		{name: "inline content", beforeMode: 0755, beforeModeKnown: true, before: "inline before", currentMode: 0644},
		{name: "snapshot content", beforeMode: 0710, beforeModeKnown: true, useSnapshot: true, before: "snapshot before", currentMode: 0600},
		{name: "zero permissions", beforeMode: 0000, beforeModeKnown: true, before: "zero mode before", currentMode: 0644},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := Store{Root: t.TempDir()}
			project := t.TempDir()
			target := filepath.Join(project, "mode.txt")
			if err := os.WriteFile(target, []byte("after"), tt.currentMode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(target, tt.currentMode); err != nil {
				t.Fatal(err)
			}
			recorder, err := store.NewRecorderWithID(project, "20202020-2020-4020-8020-202020202020")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := recorder.Checkpoint("before-mode", ""); err != nil {
				t.Fatal(err)
			}

			change := map[string]any{
				"path":              target,
				"before":            tt.before,
				"before_exists":     true,
				"after":             "after",
				"after_exists":      true,
				"before_mode":       uint32(tt.beforeMode),
				"before_mode_known": tt.beforeModeKnown,
				"after_mode":        uint32(tt.currentMode),
				"mode_changed":      true,
			}
			if tt.useSnapshot {
				snapshot := filepath.Join(project, "mode.snapshot")
				if err := os.WriteFile(snapshot, []byte(tt.before), 0600); err != nil {
					t.Fatal(err)
				}
				change["before_snapshot_path"] = snapshot
			}
			data, err := json.Marshal(change)
			if err != nil {
				t.Fatal(err)
			}
			if err := recorder.Append(Entry{Type: "file_change", Content: string(data)}); err != nil {
				t.Fatal(err)
			}
			if err := recorder.Close(); err != nil {
				t.Fatal(err)
			}

			result, ok, err := store.Rewind(recorder.SessionID, "before-mode")
			if err != nil || !ok {
				t.Fatalf("rewind ok=%v err=%v", ok, err)
			}
			if result.FilesRestored != 1 {
				t.Fatalf("files restored = %d, want 1", result.FilesRestored)
			}
			info, err := os.Stat(target)
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != tt.beforeMode {
				t.Fatalf("file mode = %04o, want %04o", got, tt.beforeMode)
			}
			if tt.beforeMode == 0 {
				if err := os.Chmod(target, 0600); err != nil {
					t.Fatal(err)
				}
			}
			assertFileContent(t, target, tt.before)
		})
	}
}

func TestRewindOldFileChangeWithoutModePreservesCurrentMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes are not supported on Windows")
	}
	store := Store{Root: t.TempDir()}
	project := t.TempDir()
	target := filepath.Join(project, "legacy.txt")
	if err := os.WriteFile(target, []byte("after"), 0600); err != nil {
		t.Fatal(err)
	}
	recorder, err := store.NewRecorderWithID(project, "21212121-2121-4121-8121-212121212121")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recorder.Checkpoint("legacy-mode", ""); err != nil {
		t.Fatal(err)
	}
	change := `{"path":"` + target + `","before":"before","before_exists":true,"after":"after","after_exists":true}`
	if err := recorder.Append(Entry{Type: "file_change", Content: change}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	if _, ok, err := store.Rewind(recorder.SessionID, "legacy-mode"); err != nil || !ok {
		t.Fatalf("rewind ok=%v err=%v", ok, err)
	}
	assertFileContent(t, target, "before")
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("legacy file mode = %04o, want 0600", got)
	}
}

func writeThreeStagedFiles(t *testing.T, dir string) (a, b, c string) {
	t.Helper()
	a = filepath.Join(dir, "a.txt")
	b = filepath.Join(dir, "b.txt")
	c = filepath.Join(dir, "c.txt")
	for _, p := range []string{a, b, c} {
		if err := os.WriteFile(p, []byte(filepath.Base(p)+"-now"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return a, b, c
}

func TestRestoreTransactionRollsBackOnFileFailure(t *testing.T) {
	dir := t.TempDir()
	a, b, c := writeThreeStagedFiles(t, dir)
	changes := []fileChangeSnapshot{
		{Path: a, Before: "a.txt-old", BeforeExists: true, BeforeMode: 0644, BeforeModeKnown: true},
		{Path: b, Before: "b.txt-old", BeforeExists: true, BeforeMode: 0644, BeforeModeKnown: true},
		{Path: c, Before: "c.txt-old", BeforeExists: true, BeforeMode: 0644, BeforeModeKnown: true},
	}
	orig := applyRestore
	applyRestore = func(change fileChangeSnapshot) ([]string, error) {
		if change.Path == c {
			return nil, errors.New("injected apply failure")
		}
		return orig(change)
	}
	defer func() { applyRestore = orig }()

	n, _, err := runFileRestoreTransaction(changes, nil)
	if err == nil {
		t.Fatal("expected apply failure")
	}
	if n != 0 {
		t.Fatalf("filesRestored = %d, want 0 on failure", n)
	}
	// Every already-applied file is rolled back to its pre-rewind state.
	assertFileContent(t, a, "a.txt-now")
	assertFileContent(t, b, "b.txt-now")
	assertFileContent(t, c, "c.txt-now")
}

func TestRestoreTransactionRollsBackOnCommitFailure(t *testing.T) {
	dir := t.TempDir()
	a, b, c := writeThreeStagedFiles(t, dir)
	changes := []fileChangeSnapshot{
		{Path: a, Before: "a.txt-old", BeforeExists: true, BeforeMode: 0644, BeforeModeKnown: true},
		{Path: b, Before: "b.txt-old", BeforeExists: true, BeforeMode: 0644, BeforeModeKnown: true},
		{Path: c, Before: "c.txt-old", BeforeExists: true, BeforeMode: 0644, BeforeModeKnown: true},
	}
	n, _, err := runFileRestoreTransaction(changes, func() error {
		return errors.New("injected commit failure")
	})
	if err == nil {
		t.Fatal("expected commit failure")
	}
	if n != 0 {
		t.Fatalf("filesRestored = %d, want 0 on commit failure", n)
	}
	// Files applied before the commit must be rolled back so workspace and
	// transcript stay consistent.
	assertFileContent(t, a, "a.txt-now")
	assertFileContent(t, b, "b.txt-now")
	assertFileContent(t, c, "c.txt-now")
}

func TestRestoreTransactionCommitsWhenAllSucceed(t *testing.T) {
	dir := t.TempDir()
	a, b, _ := writeThreeStagedFiles(t, dir)
	changes := []fileChangeSnapshot{
		{Path: a, Before: "a.txt-old", BeforeExists: true, BeforeMode: 0644, BeforeModeKnown: true},
		{Path: b, Before: "b.txt-old", BeforeExists: true, BeforeMode: 0644, BeforeModeKnown: true},
	}
	committed := false
	n, _, err := runFileRestoreTransaction(changes, func() error {
		committed = true
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 2 || !committed {
		t.Fatalf("n=%d committed=%v", n, committed)
	}
	assertFileContent(t, a, "a.txt-old")
	assertFileContent(t, b, "b.txt-old")
}

func TestRewindMissingSnapshotAbortsWithoutPartialRestore(t *testing.T) {
	store := Store{Root: t.TempDir()}
	project := t.TempDir()
	fileA := filepath.Join(project, "a.txt")
	fileB := filepath.Join(project, "b.txt")
	if err := os.WriteFile(fileA, []byte("A-now"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileB, []byte("B-now"), 0644); err != nil {
		t.Fatal(err)
	}
	recorder, err := store.NewRecorderWithID(project, "54545454-5454-4454-8454-545454545454")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recorder.Checkpoint("before-multi", ""); err != nil {
		t.Fatal(err)
	}
	changeA, _ := json.Marshal(fileChangeSnapshot{Path: fileA, Before: "A-old", BeforeExists: true, AfterExists: true, BeforeMode: 0644, BeforeModeKnown: true})
	// fileB references a snapshot that does not exist, making restore infeasible.
	changeB, _ := json.Marshal(fileChangeSnapshot{Path: fileB, BeforeExists: true, AfterExists: true, BeforeSnapshotPath: filepath.Join(project, "missing-snapshot.bin"), BeforeMode: 0644, BeforeModeKnown: true})
	if err := recorder.Append(Entry{Type: "file_change", Content: string(changeA)}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{Type: "file_change", Content: string(changeB)}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{Type: "message", Role: "assistant", Content: "done"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := store.Rewind(recorder.SessionID, "before-multi"); err == nil {
		t.Fatal("expected rewind to fail when a snapshot is missing")
	}
	// Neither file may be partially restored.
	assertFileContent(t, fileA, "A-now")
	assertFileContent(t, fileB, "B-now")
	// Transcript must be untouched (no truncation, no rewind entry).
	after, err := Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("transcript changed on failed rewind: before=%d after=%d", len(before), len(after))
	}
}

func TestRewindRestoresIntoMissingParentDirectory(t *testing.T) {
	store := Store{Root: t.TempDir()}
	project := t.TempDir()
	target := filepath.Join(project, "nested", "deep", "file.txt")
	recorder, err := store.NewRecorderWithID(project, "57575757-5757-4757-8757-575757575757")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recorder.Checkpoint("before-nested", ""); err != nil {
		t.Fatal(err)
	}
	change, _ := json.Marshal(fileChangeSnapshot{
		Path:            target,
		Before:          "recreated",
		BeforeExists:    true,
		AfterExists:     false,
		BeforeMode:      0644,
		BeforeModeKnown: true,
	})
	if err := recorder.Append(Entry{Type: "file_change", Content: string(change)}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.Rewind(recorder.SessionID, "before-nested"); err != nil || !ok {
		t.Fatalf("rewind ok=%v err=%v", ok, err)
	}
	assertFileContent(t, target, "recreated")
}

func TestRewindInlineRestoreIsAtomicOnFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permission semantics differ on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory write permissions")
	}
	store := Store{Root: t.TempDir()}
	project := t.TempDir()
	sub := filepath.Join(project, "sub")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(sub, "file.txt")
	if err := os.WriteFile(target, []byte("current"), 0644); err != nil {
		t.Fatal(err)
	}
	recorder, err := store.NewRecorderWithID(project, "56565656-5656-4656-8656-565656565656")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recorder.Checkpoint("before-atomic", ""); err != nil {
		t.Fatal(err)
	}
	change, _ := json.Marshal(fileChangeSnapshot{
		Path:            target,
		Before:          "restored",
		BeforeExists:    true,
		AfterExists:     true,
		BeforeMode:      0644,
		BeforeModeKnown: true,
	})
	if err := recorder.Append(Entry{Type: "file_change", Content: string(change)}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	// A read-only parent directory makes the atomic temp-file create fail. An
	// atomic restore must surface the error and leave the original untouched
	// rather than truncating it in place.
	if err := os.Chmod(sub, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(sub, 0755)
	if _, _, err := store.Rewind(recorder.SessionID, "before-atomic"); err == nil {
		t.Fatal("expected restore to fail in a read-only directory")
	}
	if err := os.Chmod(sub, 0755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "current" {
		t.Fatalf("original file must be preserved on failure, got %q", data)
	}
}

func TestGCSnapshotsReclaimsOrphansButKeepsReachable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(t.TempDir(), "legacy"))
	snapDir, err := files.SnapshotDir()
	if err != nil {
		t.Fatal(err)
	}
	shared, err := files.StoreSnapshotContentAddressed(snapDir, strings.NewReader("shared-blob"))
	if err != nil {
		t.Fatal(err)
	}
	onlyA, err := files.StoreSnapshotContentAddressed(snapDir, strings.NewReader("only-a-blob"))
	if err != nil {
		t.Fatal(err)
	}

	store := Store{Root: filepath.Join(home, ".niuma-legacy")}
	project := filepath.Join(home, "proj")
	writeRefs := func(id string, paths ...string) string {
		rec, err := store.NewRecorderWithID(project, id)
		if err != nil {
			t.Fatal(err)
		}
		for i, path := range paths {
			change, _ := json.Marshal(fileChangeSnapshot{Path: filepath.Join(project, fmt.Sprintf("f%d.bin", i)), BeforeExists: true, BeforeSnapshotPath: path})
			if err := rec.Append(Entry{Type: "file_change", Content: string(change)}); err != nil {
				t.Fatal(err)
			}
		}
		if err := rec.Close(); err != nil {
			t.Fatal(err)
		}
		return rec.SessionID
	}
	// Session A references both onlyA and the shared blob; session B references
	// only the shared blob.
	sessionA := writeRefs("a1a1a1a1-a1a1-4a1a-8a1a-a1a1a1a1a1a1", onlyA, shared)
	_ = writeRefs("b2b2b2b2-b2b2-4b2b-8b2b-b2b2b2b2b2b2", shared)

	if _, err := store.Delete(sessionA); err != nil {
		t.Fatal(err)
	}
	// onlyA was referenced only by the deleted session -> reclaimed.
	if _, err := os.Stat(onlyA); !os.IsNotExist(err) {
		t.Fatalf("orphaned blob should be reclaimed, err=%v", err)
	}
	// shared is still referenced by session B -> must survive.
	if _, err := os.Stat(shared); err != nil {
		t.Fatalf("shared blob referenced by surviving session must be kept: %v", err)
	}
}

func TestRewindRestoresFileMtime(t *testing.T) {
	store := Store{Root: t.TempDir()}
	project := t.TempDir()
	target := filepath.Join(project, "timed.txt")
	if err := os.WriteFile(target, []byte("after"), 0644); err != nil {
		t.Fatal(err)
	}
	recorder, err := store.NewRecorderWithID(project, "55555555-5555-4555-8555-555555555555")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recorder.Checkpoint("before-mtime", ""); err != nil {
		t.Fatal(err)
	}
	want := time.Date(2021, 6, 7, 8, 9, 10, 0, time.UTC)
	change, _ := json.Marshal(fileChangeSnapshot{
		Path:            target,
		Before:          "before",
		BeforeExists:    true,
		AfterExists:     true,
		BeforeMode:      0644,
		BeforeModeKnown: true,
		BeforeMetadata:  &files.Metadata{ModTimeKnown: true, ModTimeUnixNano: want.UnixNano()},
	})
	if err := recorder.Append(Entry{Type: "file_change", Content: string(change)}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.Rewind(recorder.SessionID, "before-mtime"); err != nil || !ok {
		t.Fatalf("rewind ok=%v err=%v", ok, err)
	}
	assertFileContent(t, target, "before")
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.ModTime().Unix() != want.Unix() {
		t.Fatalf("mtime = %v, want %v", info.ModTime().Unix(), want.Unix())
	}
}

func TestRewindReportsMetadataDegradation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX ownership on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root can chown; degradation path not exercised")
	}
	store := Store{Root: t.TempDir()}
	project := t.TempDir()
	target := filepath.Join(project, "owned.txt")
	if err := os.WriteFile(target, []byte("after"), 0644); err != nil {
		t.Fatal(err)
	}
	recorder, err := store.NewRecorderWithID(project, "58585858-5858-4858-8858-585858585858")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recorder.Checkpoint("before-owner", ""); err != nil {
		t.Fatal(err)
	}
	// uid/gid 0 cannot be restored by a non-root process; content must still be
	// restored and the ownership failure surfaced as a degradation.
	change, _ := json.Marshal(fileChangeSnapshot{
		Path:            target,
		Before:          "before",
		BeforeExists:    true,
		AfterExists:     true,
		BeforeMode:      0644,
		BeforeModeKnown: true,
		BeforeMetadata:  &files.Metadata{OwnerKnown: true, UID: 0, GID: 0},
	})
	if err := recorder.Append(Entry{Type: "file_change", Content: string(change)}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	result, ok, err := store.Rewind(recorder.SessionID, "before-owner")
	if err != nil || !ok {
		t.Fatalf("rewind ok=%v err=%v", ok, err)
	}
	assertFileContent(t, target, "before")
	if len(result.MetadataDegraded) == 0 {
		t.Fatalf("expected observable metadata degradation, got none")
	}
}

func TestRewindRemovesCreatedDirectory(t *testing.T) {
	store := Store{Root: t.TempDir()}
	project := t.TempDir()
	dir := filepath.Join(project, "created-dir")
	inner := filepath.Join(dir, "inner.txt")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inner, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	recorder, err := store.NewRecorderWithID(project, "5a5a5a5a-5a5a-4a5a-8a5a-5a5a5a5a5a5a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recorder.Checkpoint("before-mkdir", ""); err != nil {
		t.Fatal(err)
	}
	dirChange, _ := json.Marshal(fileChangeSnapshot{Path: dir, BeforeExists: false, AfterExists: true, AfterIsDir: true})
	fileChange, _ := json.Marshal(fileChangeSnapshot{Path: inner, BeforeExists: false, AfterExists: true})
	if err := recorder.Append(Entry{Type: "file_change", Content: string(dirChange)}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{Type: "file_change", Content: string(fileChange)}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.Rewind(recorder.SessionID, "before-mkdir"); err != nil || !ok {
		t.Fatalf("rewind ok=%v err=%v", ok, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("created directory should be removed, err=%v", err)
	}
}

func TestRewindRestoresDeletedDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX directory modes differ on Windows")
	}
	store := Store{Root: t.TempDir()}
	project := t.TempDir()
	dir := filepath.Join(project, "gone-dir")
	// The directory was deleted after the checkpoint; workspace no longer has it.
	recorder, err := store.NewRecorderWithID(project, "5b5b5b5b-5b5b-4b5b-8b5b-5b5b5b5b5b5b")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recorder.Checkpoint("before-rmdir", ""); err != nil {
		t.Fatal(err)
	}
	change, _ := json.Marshal(fileChangeSnapshot{Path: dir, BeforeExists: true, BeforeIsDir: true, BeforeMode: 0750, BeforeModeKnown: true, AfterExists: false})
	if err := recorder.Append(Entry{Type: "file_change", Content: string(change)}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.Rewind(recorder.SessionID, "before-rmdir"); err != nil || !ok {
		t.Fatalf("rewind ok=%v err=%v", ok, err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("directory should be restored: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("restored object is not a directory")
	}
	if info.Mode().Perm() != 0750 {
		t.Fatalf("directory mode = %04o, want 0750", info.Mode().Perm())
	}
}

func TestRewindRemovesCreatedSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	store := Store{Root: t.TempDir()}
	project := t.TempDir()
	target := filepath.Join(project, "target.txt")
	if err := os.WriteFile(target, []byte("t"), 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(project, "link.txt")
	if err := os.Symlink("target.txt", link); err != nil {
		t.Fatal(err)
	}
	recorder, err := store.NewRecorderWithID(project, "31313131-3131-4131-8131-313131313131")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recorder.Checkpoint("before-symlink", ""); err != nil {
		t.Fatal(err)
	}
	change, _ := json.Marshal(fileChangeSnapshot{Path: link, BeforeExists: false, AfterExists: true, AfterIsSymlink: true, AfterLinkTarget: "target.txt"})
	if err := recorder.Append(Entry{Type: "file_change", Content: string(change)}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.Rewind(recorder.SessionID, "before-symlink"); err != nil || !ok {
		t.Fatalf("rewind ok=%v err=%v", ok, err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("symlink should be removed, lstat err=%v", err)
	}
}

func TestRewindRestoresDeletedSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	store := Store{Root: t.TempDir()}
	project := t.TempDir()
	link := filepath.Join(project, "link.txt")
	// The link was deleted after the checkpoint; the workspace no longer has it.
	recorder, err := store.NewRecorderWithID(project, "32323232-3232-4232-8232-323232323232")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recorder.Checkpoint("before-delete-symlink", ""); err != nil {
		t.Fatal(err)
	}
	change, _ := json.Marshal(fileChangeSnapshot{Path: link, BeforeExists: true, BeforeIsSymlink: true, BeforeLinkTarget: "target.txt", AfterExists: false})
	if err := recorder.Append(Entry{Type: "file_change", Content: string(change)}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.Rewind(recorder.SessionID, "before-delete-symlink"); err != nil || !ok {
		t.Fatalf("rewind ok=%v err=%v", ok, err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("symlink should be restored: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("restored object is not a symlink: %v", info.Mode())
	}
	got, err := os.Readlink(link)
	if err != nil || got != "target.txt" {
		t.Fatalf("readlink = %q err=%v", got, err)
	}
}

func TestRewindRestoresMultipleFileChanges(t *testing.T) {
	store := Store{Root: t.TempDir()}
	project := t.TempDir()
	existing := filepath.Join(project, "existing.txt")
	created := filepath.Join(project, "created.txt")
	if err := os.WriteFile(existing, []byte("before"), 0644); err != nil {
		t.Fatal(err)
	}
	recorder, err := store.NewRecorderWithID(project, "55555555-5555-4555-8555-555555555555")
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{Type: "message", Role: "user", Content: "start"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.Checkpoint(recorder.SessionID, "before-multi"); err != nil || !ok {
		t.Fatalf("checkpoint ok=%v err=%v", ok, err)
	}
	recorder, err = store.NewRecorderWithID(project, recorder.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	changes := []string{
		`{"path":"` + existing + `","before":"before","before_exists":true,"after":"after","after_exists":true}`,
		`{"path":"` + created + `","before":"","before_exists":false,"after":"new","after_exists":true}`,
	}
	for _, change := range changes {
		if err := recorder.Append(Entry{Type: "file_change", Content: change}); err != nil {
			t.Fatal(err)
		}
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existing, []byte("after"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(created, []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}
	result, ok, err := store.Rewind(recorder.SessionID, "before-multi")
	if err != nil || !ok {
		t.Fatalf("rewind ok=%v err=%v", ok, err)
	}
	if result.FilesRestored != 2 || result.EntriesRemoved != 2 {
		t.Fatalf("result = %+v", result)
	}
	data, err := os.ReadFile(existing)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "before" {
		t.Fatalf("existing = %q", data)
	}
	if _, err := os.Stat(created); !os.IsNotExist(err) {
		t.Fatalf("created file still exists or stat failed: %v", err)
	}
}

func TestCheckpointRewindRestoresEarliestBeforeStateForRepeatedFileChanges(t *testing.T) {
	store, recorder, target, messageID := newRepeatedFileChangeSession(t, true)

	result, ok, err := store.Rewind(recorder.SessionID, "auto-"+messageID)
	if err != nil || !ok {
		t.Fatalf("rewind ok=%v err=%v", ok, err)
	}
	if result.FilesRestored != 1 {
		t.Fatalf("files restored = %d, want 1", result.FilesRestored)
	}
	assertFileContent(t, target, "original")

	entries, err := Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Type != "checkpoint" || entries[1].Type != "rewind" {
		t.Fatalf("entries after rewind = %+v", entries)
	}
}

func TestMessageRewindRepeatedFileChangesUsesTargetBoundary(t *testing.T) {
	tests := []struct {
		name               string
		rewind             func(Store, string, string) (RewindResult, bool, error)
		wantContent        string
		wantTranscriptKept bool
		wantFilesRestored  int
	}{
		{
			name:              "files and conversation",
			rewind:            Store.RewindToMessage,
			wantContent:       "original",
			wantFilesRestored: 1,
		},
		{
			name:               "files only",
			rewind:             Store.RewindFiles,
			wantContent:        "original",
			wantTranscriptKept: true,
			wantFilesRestored:  1,
		},
		{
			name:        "conversation only",
			rewind:      Store.RewindConversationToMessage,
			wantContent: "33",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, recorder, target, messageID := newRepeatedFileChangeSession(t, true)

			result, ok, err := tt.rewind(store, recorder.SessionID, messageID)
			if err != nil || !ok {
				t.Fatalf("rewind ok=%v err=%v", ok, err)
			}
			if result.FilesRestored != tt.wantFilesRestored {
				t.Fatalf("files restored = %d, want %d", result.FilesRestored, tt.wantFilesRestored)
			}
			if result.TranscriptKept != tt.wantTranscriptKept {
				t.Fatalf("transcript kept = %v, want %v", result.TranscriptKept, tt.wantTranscriptKept)
			}
			assertFileContent(t, target, tt.wantContent)
		})
	}
}

func TestRewindRemovesFileCreatedThenModified(t *testing.T) {
	store, recorder, target, messageID := newRepeatedFileChangeSession(t, false)

	result, ok, err := store.RewindToMessage(recorder.SessionID, messageID)
	if err != nil || !ok {
		t.Fatalf("rewind ok=%v err=%v", ok, err)
	}
	if result.FilesRestored != 1 {
		t.Fatalf("files restored = %d, want 1", result.FilesRestored)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("created file still exists or stat failed: %v", err)
	}
}

func newRepeatedFileChangeSession(t *testing.T, initiallyExists bool) (Store, *Recorder, string, string) {
	t.Helper()
	store := Store{Root: t.TempDir()}
	project := t.TempDir()
	target := filepath.Join(project, "repeated.txt")
	messageID := "msg-repeated"
	recorder, err := store.NewRecorderWithID(project, "19191919-1919-4919-8919-191919191919")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recorder.Checkpoint("auto-"+messageID, messageID); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{ID: messageID, Type: "message", Role: "user", Content: "edit repeatedly"}); err != nil {
		t.Fatal(err)
	}

	changes := []fileChangeSnapshot{
		{Path: target, Before: "original", BeforeExists: initiallyExists, After: "11", AfterExists: true},
		{Path: target, Before: "11", BeforeExists: true, After: "22", AfterExists: true},
		{Path: target, Before: "22", BeforeExists: true, After: "33", AfterExists: true},
	}
	for _, change := range changes {
		data, err := json.Marshal(change)
		if err != nil {
			t.Fatal(err)
		}
		if err := recorder.Append(Entry{Type: "file_change", Content: string(data)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := recorder.Append(Entry{Type: "message", Role: "assistant", Content: "changed"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("33"), 0644); err != nil {
		t.Fatal(err)
	}
	return store, recorder, target, messageID
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("file content = %q, want %q", data, want)
	}
}

func TestRewindFilesKeepsTranscript(t *testing.T) {
	store := Store{Root: t.TempDir()}
	project := t.TempDir()
	target := filepath.Join(project, "note.txt")
	if err := os.WriteFile(target, []byte("before"), 0644); err != nil {
		t.Fatal(err)
	}
	recorder, err := store.NewRecorderWithID(project, "77777777-7777-4777-8777-777777777777")
	if err != nil {
		t.Fatal(err)
	}
	messageID := "msg-1"
	if _, err := recorder.Checkpoint("auto-"+messageID, messageID); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{ID: messageID, Type: "message", Role: "user", Content: "edit"}); err != nil {
		t.Fatal(err)
	}
	change := `{"path":"` + target + `","before":"before","before_exists":true,"after":"after","after_exists":true}`
	if err := recorder.Append(Entry{Type: "file_change", Content: change}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("after"), 0644); err != nil {
		t.Fatal(err)
	}
	result, ok, err := store.RewindFiles(recorder.SessionID, messageID)
	if err != nil || !ok {
		t.Fatalf("rewind files ok=%v err=%v", ok, err)
	}
	if !result.TranscriptKept || result.FilesRestored != 1 || result.EntriesRemoved != 0 {
		t.Fatalf("result = %+v", result)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "before" {
		t.Fatalf("data = %q", data)
	}
	entries, err := Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 4 || entries[len(entries)-1].Type != "rewind" {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestRewindFilesInvalidatesLatestRecap(t *testing.T) {
	store := Store{Root: t.TempDir()}
	project := t.TempDir()
	target := filepath.Join(project, "note.txt")
	if err := os.WriteFile(target, []byte("before"), 0644); err != nil {
		t.Fatal(err)
	}
	recorder, err := store.NewRecorderWithID(project, "17171717-1717-4717-8717-171717171717")
	if err != nil {
		t.Fatal(err)
	}
	messageID := "msg-1"
	if _, err := recorder.Checkpoint("auto-"+messageID, messageID); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{ID: messageID, Type: "message", Role: "user", Content: "edit"}); err != nil {
		t.Fatal(err)
	}
	change := `{"path":"` + target + `","before":"before","before_exists":true,"after":"after","after_exists":true}`
	if err := recorder.Append(Entry{Type: "file_change", Content: change}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{Type: "recap_summary", Role: "system", Content: "old recap"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("after"), 0644); err != nil {
		t.Fatal(err)
	}
	result, ok, err := store.RewindFiles(recorder.SessionID, messageID)
	if err != nil || !ok {
		t.Fatalf("rewind files ok=%v err=%v", ok, err)
	}
	if !result.TranscriptKept || result.EntriesRemoved != 0 {
		t.Fatalf("result = %+v", result)
	}
	entries, err := Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 2 || entries[len(entries)-1].Type != "recap_summary" || !strings.Contains(entries[len(entries)-1].Content, "invalidated") {
		t.Fatalf("entries = %+v", entries)
	}
	var meta map[string]any
	if err := json.Unmarshal(entries[len(entries)-1].Metadata, &meta); err != nil {
		t.Fatal(err)
	}
	if meta["status"] != "invalidated" || meta["source"] != "rewind" {
		t.Fatalf("metadata = %+v", meta)
	}
}

func TestRewindConversationToMessageDoesNotRestoreFiles(t *testing.T) {
	store := Store{Root: t.TempDir()}
	project := t.TempDir()
	target := filepath.Join(project, "note.txt")
	if err := os.WriteFile(target, []byte("before"), 0644); err != nil {
		t.Fatal(err)
	}
	recorder, err := store.NewRecorderWithID(project, "78787878-7878-4787-8787-787878787878")
	if err != nil {
		t.Fatal(err)
	}
	messageID := "msg-1"
	if _, err := recorder.Checkpoint("auto-"+messageID, messageID); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{ID: messageID, Type: "message", Role: "user", Content: "edit"}); err != nil {
		t.Fatal(err)
	}
	change := `{"path":"` + target + `","before":"before","before_exists":true,"after":"after","after_exists":true}`
	if err := recorder.Append(Entry{Type: "file_change", Content: change}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{Type: "message", Role: "assistant", Content: "changed"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("after"), 0644); err != nil {
		t.Fatal(err)
	}
	result, ok, err := store.RewindConversationToMessage(recorder.SessionID, messageID)
	if err != nil || !ok {
		t.Fatalf("rewind conversation ok=%v err=%v", ok, err)
	}
	if result.FilesRestored != 0 || result.EntriesRemoved != 3 {
		t.Fatalf("result = %+v", result)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "after" {
		t.Fatalf("data = %q", data)
	}
	entries, err := Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Type != "checkpoint" || entries[1].Type != "rewind" || entries[1].Name != messageID {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestRewindConversationRemovesRecapAfterTarget(t *testing.T) {
	store := Store{Root: t.TempDir()}
	project := t.TempDir()
	recorder, err := store.NewRecorderWithID(project, "18181818-1818-4818-8818-181818181818")
	if err != nil {
		t.Fatal(err)
	}
	messageID := "msg-1"
	if _, err := recorder.Checkpoint("auto-"+messageID, messageID); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{ID: messageID, Type: "message", Role: "user", Content: "edit"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{Type: "message", Role: "assistant", Content: "changed"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{Type: "recap_summary", Role: "system", Content: "stale recap"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	result, ok, err := store.RewindConversationToMessage(recorder.SessionID, messageID)
	if err != nil || !ok {
		t.Fatalf("rewind conversation ok=%v err=%v", ok, err)
	}
	if result.EntriesRemoved != 3 {
		t.Fatalf("result = %+v", result)
	}
	entries, err := Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Type == "recap_summary" {
			t.Fatalf("stale recap was not removed: %+v", entries)
		}
	}
}

func TestRewindToMessageWithoutCheckpointRemovesTargetMessage(t *testing.T) {
	store := Store{Root: t.TempDir()}
	project := t.TempDir()
	target := filepath.Join(project, "note.txt")
	if err := os.WriteFile(target, []byte("before"), 0644); err != nil {
		t.Fatal(err)
	}
	recorder, err := store.NewRecorderWithID(project, "79797979-7979-4797-8797-797979797979")
	if err != nil {
		t.Fatal(err)
	}
	messageID := "msg-legacy"
	if err := recorder.Append(Entry{Type: "message", Role: "assistant", Content: "before target"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{ID: messageID, Type: "message", Role: "user", Content: "legacy edit"}); err != nil {
		t.Fatal(err)
	}
	change := `{"path":"` + target + `","before":"before","before_exists":true,"after":"after","after_exists":true}`
	if err := recorder.Append(Entry{Type: "file_change", Content: change}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{Type: "message", Role: "assistant", Content: "changed"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("after"), 0644); err != nil {
		t.Fatal(err)
	}
	result, ok, err := store.RewindToMessage(recorder.SessionID, messageID)
	if err != nil || !ok {
		t.Fatalf("rewind message ok=%v err=%v", ok, err)
	}
	if result.FilesRestored != 1 || result.EntriesRemoved != 3 {
		t.Fatalf("result = %+v", result)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "before" {
		t.Fatalf("data = %q", data)
	}
	entries, err := Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Content != "before target" || entries[1].Type != "rewind" {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestForkCopiesEntriesThroughCheckpoint(t *testing.T) {
	store := Store{Root: t.TempDir()}
	project := t.TempDir()
	recorder, err := store.NewRecorderWithID(project, "66666666-6666-4666-8666-666666666666")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []Entry{
		{Type: "message", Role: "user", Content: "before"},
		{Type: "checkpoint", Name: "keep"},
		{Type: "message", Role: "assistant", Content: "after"},
	} {
		if err := recorder.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	result, ok, err := store.Fork(recorder.SessionID, "keep", "Forked Session")
	if err != nil || !ok {
		t.Fatalf("fork ok=%v err=%v", ok, err)
	}
	if result.SourceSessionID != recorder.SessionID || result.Checkpoint != "keep" || result.EntriesCopied != 4 {
		t.Fatalf("result = %+v", result)
	}
	entries, err := Load(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 || entries[0].Content != "before" || entries[1].Type != "checkpoint" || entries[2].Type != "fork" || entries[3].Name != "Forked Session" {
		t.Fatalf("entries = %+v", entries)
	}
	for _, entry := range entries {
		if entry.Content == "after" {
			t.Fatalf("fork copied entries after checkpoint: %+v", entries)
		}
	}
}

func TestLatestForProjectPicksNewestTranscript(t *testing.T) {
	root := t.TempDir()
	store := Store{TranscriptProjectsRoot: root}
	cwd := "/Users/example/demo"
	dir := filepath.Join(root, ProjectSlug(cwd))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	older := filepath.Join(dir, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa.jsonl")
	newer := filepath.Join(dir, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb.jsonl")
	for _, p := range []string{older, newer} {
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(older, past, past); err != nil {
		t.Fatal(err)
	}
	summary, ok, err := store.LatestForProject(cwd)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if summary.SessionID != "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb" {
		t.Fatalf("session id = %q", summary.SessionID)
	}
	if summary.Path != newer {
		t.Fatalf("path = %q", summary.Path)
	}
}

func TestLatestForProjectNoSessions(t *testing.T) {
	store := Store{TranscriptProjectsRoot: t.TempDir()}
	_, ok, err := store.LatestForProject("/Users/example/empty")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected ok=false for empty project")
	}
}
