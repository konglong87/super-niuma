package memory

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestMemoryFrontmatterSummaryUsesTopLevelYAMLFields(t *testing.T) {
	tests := []struct {
		name, content, wantName, wantDescription string
	}{
		{
			name: "folded description",
			content: "---\nname: Deployment\n" +
				"description: >-\n  release verification\n  and rollback\nmetadata:\n  type: project\n---\nbody",
			wantName: "Deployment", wantDescription: "release verification and rollback",
		},
		{
			name: "nested fields do not override",
			content: "---\nname: Top level\ndescription: Public convention\n" +
				"metadata:\n  name: Nested name\n  description: Nested description\n---\nbody",
			wantName: "Top level", wantDescription: "Public convention",
		},
		{
			name:     "quoted scalar and comment",
			content:  "---\nname: \"Deploy: checklist\" # note\ndescription: 'It''s durable'\n---\nbody",
			wantName: "Deploy: checklist", wantDescription: "It's durable",
		},
		{name: "malformed YAML", content: "---\nname: [broken\ndescription: release\n---\nbody"},
		{name: "non scalar name", content: "---\nname: [release]\ndescription: rollback\n---\nbody"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, description := memoryFrontmatterSummary(tt.content)
			if name != tt.wantName || description != tt.wantDescription {
				t.Fatalf("summary = (%q, %q), want (%q, %q)", name, description, tt.wantName, tt.wantDescription)
			}
		})
	}
}

func TestProjectMemoryIndexRejectsSymlinkOutsideItsDirectory(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "private.md")
	mustWrite(t, outside, "OUTSIDE_INDEX_BODY")
	if err := os.Symlink(outside, filepath.Join(dir, "MEMORY.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	docs, err := loadClaudeCodeProjectMemoryPath(filepath.Join(dir, "MEMORY.md"), "private")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 0 {
		t.Fatal("an index symlink exposed content outside the memory directory")
	}
}

func TestProjectMemoryRecallCapsCandidatesAndRejectsUnindexedOrMissingFiles(t *testing.T) {
	dir := t.TempDir()
	var index strings.Builder
	for _, name := range []string{"a", "b", "c", "d", "e", "f"} {
		index.WriteString("- [" + name + "](" + name + ".md)\n")
		mustWrite(t, filepath.Join(dir, name+".md"), "---\nname: deploy\ndescription: release verification\n---\nBODY_"+name)
	}
	index.WriteString("- [missing](missing.md)\n- [duplicate](a.md)\n")
	mustWrite(t, filepath.Join(dir, "unindexed.md"), "---\nname: deploy\ndescription: release verification\n---\nUNINDEXED")
	docs := recallProjectMemoryFiles(dir, index.String(), "deploy verification")
	if len(docs) != defaultProjectMemoryRecallFiles {
		t.Fatalf("recalled %d files, want %d", len(docs), defaultProjectMemoryRecallFiles)
	}
	for i, doc := range docs {
		want := filepath.Join(dir, string(rune('a'+i))+".md")
		if doc.Path != want || strings.Contains(doc.Content, "UNINDEXED") {
			t.Fatalf("unexpected recall at index %d: %s", i, doc.Path)
		}
	}
}

func TestProjectMemoryRecallLimitsLargeBodyAndKeepsUTF8(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "large.md"), "---\nname: deploy\ndescription: verification\n---\n"+
		strings.Repeat("中文内容 ", 12000)+"TAIL_MUST_NOT_LOAD")
	docs := recallProjectMemoryFiles(dir, "- [large](large.md)", "deploy")
	if len(docs) != 1 || len(docs[0].Content) > 25*1024 || !utf8.ValidString(docs[0].Content) {
		t.Fatalf("large recall not bounded/rune-safe: %d documents", len(docs))
	}
	if strings.Contains(docs[0].Content, "TAIL_MUST_NOT_LOAD") {
		t.Fatal("large recall included the full body")
	}
}

func TestProjectMemoryRecallDoesNotShareFilesAcrossWorkspaces(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", configDir)
	workspaceA, workspaceB := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
	dirA := ClaudeCodeProjectMemoryDir(workspaceA)
	mustWrite(t, filepath.Join(dirA, "MEMORY.md"), "- [deploy](deploy.md)")
	mustWrite(t, filepath.Join(dirA, "deploy.md"), "---\nname: deploy\ndescription: verification\n---\nPROJECT_A_ONLY")
	docs, err := loadClaudeCodeProjectMemory(workspaceB, "deploy verification")
	if err != nil || len(docs) != 0 {
		t.Fatalf("workspace B received A's memory: %d docs, err=%v", len(docs), err)
	}
}

func TestProjectMemoryPreservesInRootSymlinks(t *testing.T) {
	for _, absolute := range []bool{false, true} {
		name := "relative"
		if absolute {
			name = "absolute"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			mustWrite(t, filepath.Join(dir, "index.md"), "- [deploy](linked/deploy.md)")
			mustWrite(t, filepath.Join(dir, "entries", "deploy.md"),
				"---\nname: deploy\ndescription: verification\n---\nIN_ROOT_BODY")
			for link, target := range map[string]string{"MEMORY.md": "index.md", "linked": "entries"} {
				if absolute {
					target = filepath.Join(dir, target)
				}
				if err := os.Symlink(target, filepath.Join(dir, link)); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			}
			docs, err := loadClaudeCodeProjectMemoryPath(filepath.Join(dir, "MEMORY.md"), "deploy")
			if err != nil || len(docs) != 2 || !strings.Contains(docs[1].Content, "IN_ROOT_BODY") {
				t.Fatalf("in-root links lost: docs=%v err=%v", docs, err)
			}
		})
	}
}

func TestProjectMemoryReadRejectsLinkReplacedAfterPreview(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "safe.md"), "---\nname: deploy\n---\nSAFE")
	link := filepath.Join(dir, "linked.md")
	if err := os.Symlink("safe.md", link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if _, err := readMemoryPreview(root, "linked.md", defaultProjectMemoryPreviewLines); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "private.md")
	mustWrite(t, outside, "OUTSIDE_BODY")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if data, err := readProjectMemoryFile(root, "linked.md"); err == nil || len(data) != 0 {
		t.Fatalf("replaced link was read: %q, err=%v", data, err)
	}
}

func TestProjectMemoryReadStaysWithOpenedRootAfterRename(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "js" {
		t.Skip("open directory rename is unavailable or not pinned on this platform")
	}
	dir := filepath.Join(t.TempDir(), "memory")
	mustWrite(t, filepath.Join(dir, "entry.md"), "ORIGINAL_ROOT")
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := os.Rename(dir, dir+"-moved"); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "entry.md"), "REPLACEMENT_ROOT")
	data, err := readProjectMemoryFile(root, "entry.md")
	if err != nil || string(data) != "ORIGINAL_ROOT" {
		t.Fatalf("read followed replacement directory: %q, err=%v", data, err)
	}
}

func TestProjectMemoryRecallRequiresCompleteFrontmatterDelimiter(t *testing.T) {
	cases := []struct {
		name, closing string
		want          bool
	}{
		{"exact", "---", true},
		{"trailing whitespace", "--- \t", true},
		{"suffix", "---not-a-delimiter", false},
		{"extra dash", "----", false},
		{"mapping key", "---: value", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mustWrite(t, filepath.Join(dir, "note.md"), "---\nname: Release\ndescription: durable policy\n"+tc.closing+"\nbody")
			docs := recallProjectMemoryFiles(dir, "- [Note](note.md) - deployment verification", "deployment verification")
			if got := len(docs) == 1; got != tc.want {
				t.Fatalf("closing delimiter %q: recalled=%v, want=%v", tc.closing, got, tc.want)
			}
		})
	}
}

func TestProjectMemoryPreviewKeepsIndentedDelimiterInScalar(t *testing.T) {
	dir := t.TempDir()
	body := "---\nname: Release\ndescription: |-\n  preflight\n  ---\n  rollback\nmetadata:\n  type: project\n---\nBODY"
	mustWrite(t, filepath.Join(dir, "note.md"), body)
	name, description := memoryFrontmatterSummary(body)
	if name != "Release" || description != "preflight\n---\nrollback" {
		t.Fatalf("summary=(%q, %q), want a complete literal block", name, description)
	}
	docs := recallProjectMemoryFiles(dir, "- [Note](note.md)", "rollback")
	if len(docs) != 1 || !strings.Contains(docs[0].Content, "BODY") {
		t.Fatal("preview truncated the indented scalar before its relevant metadata")
	}
}
