package files

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestSnapshotDirIgnoresClaudeConfigDirForWrites(t *testing.T) {
	home := t.TempDir()
	legacy := filepath.Join(t.TempDir(), ".claude")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", legacy)

	got, err := snapshotDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".niuma", "snapshots")
	if got != want {
		t.Fatalf("snapshotDir = %q, want %q", got, want)
	}
}

func TestInspectChunksLargeFileOnLineBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.txt")
	content := strings.Repeat("aaaa\n", 5) + strings.Repeat("bbbb\n", 5)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	manifest, err := Inspect(path, Config{ChunkSize: 15, PreviewBytes: 8})
	if err != nil {
		t.Fatal(err)
	}
	if !manifest.Large || manifest.ChunkCount != 3 {
		t.Fatalf("manifest = %+v", manifest)
	}
	if manifest.Chunks[0].LineStart != 1 || manifest.Chunks[0].LineEnd == 0 || manifest.Chunks[1].LineStart <= manifest.Chunks[0].LineEnd {
		t.Fatalf("line ranges were not annotated: %+v", manifest.Chunks)
	}
	for _, chunk := range manifest.Chunks[:len(manifest.Chunks)-1] {
		if chunk.ByteLength%5 != 0 {
			t.Fatalf("chunk was not aligned to line boundary: %+v", chunk)
		}
	}
}

func TestReadLargeFileReturnsManifestAndPreview(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\nfour\n"), 0644); err != nil {
		t.Fatal(err)
	}
	result, err := Read(path, ReadRequest{}, Config{ChunkSize: 8, PreviewBytes: 7})
	if err != nil {
		t.Fatal(err)
	}
	out := FormatManifest(result)
	if !strings.Contains(out, "Large file detected") || !strings.Contains(out, "chunk_count:") || !strings.Contains(out, "Preview byte_offset=0 bytes=7") {
		t.Fatalf("manifest output = %q", out)
	}
}

func TestReadLargeFileGoldenManifest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.txt")
	if err := os.WriteFile(path, []byte("alpha\nbravo\ncharlie\ndelta\n"), 0644); err != nil {
		t.Fatal(err)
	}
	result, err := Read(path, ReadRequest{}, Config{ChunkSize: 12, PreviewBytes: 12})
	if err != nil {
		t.Fatal(err)
	}
	got := normalizeManifestGolden(FormatManifest(result), path)
	wantBytes, err := os.ReadFile(filepath.Join("testdata", "golden", "large_manifest.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got != string(wantBytes) {
		t.Fatalf("manifest mismatch\n--- got ---\n%s\n--- want ---\n%s", got, string(wantBytes))
	}
}

func TestReadBinaryFileGoldenSummary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(path, []byte{0x00, 0x01, 0x02, 0x03, 'p', 'a', 'y', 'l', 'o', 'a', 'd'}, 0644); err != nil {
		t.Fatal(err)
	}
	result, err := Read(path, ReadRequest{}, Config{ChunkSize: 8, PreviewBytes: 4})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Manifest.Binary || result.Content != "" {
		t.Fatalf("result = %+v", result)
	}
	got := normalizeManifestGolden(FormatManifest(result), path)
	wantBytes, err := os.ReadFile(filepath.Join("testdata", "golden", "binary_manifest.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got != string(wantBytes) {
		t.Fatalf("binary manifest mismatch\n--- got ---\n%s\n--- want ---\n%s", got, string(wantBytes))
	}
}

func TestReadLinesStreamsOffsetLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lines.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0644); err != nil {
		t.Fatal(err)
	}
	result, err := Read(path, ReadRequest{LineOffset: 2, LineLimit: 1, LineNumbers: true}, Config{ChunkSize: 4})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "     2: two" {
		t.Fatalf("content = %q", result.Content)
	}
}

func TestGrepStreamsContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "grep.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\nfour\n"), 0644); err != nil {
		t.Fatal(err)
	}
	result, err := Grep(path, regexp.MustCompile("three"), 1, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	if result.MatchCount != 1 || len(result.Matches) != 3 {
		t.Fatalf("result = %+v", result)
	}
	if result.Matches[0].LineNumber != 2 || result.Matches[2].LineNumber != 4 {
		t.Fatalf("matches = %+v", result.Matches)
	}
}

func TestReplaceStreamsAcrossBufferBoundary(t *testing.T) {
	var out strings.Builder
	err := streamReplace(strings.NewReader("aaaa--needle--bbbb--needle--cccc"), &out, []byte("--needle--"), []byte("--x--"), true)
	if err != nil {
		t.Fatal(err)
	}
	if out.String() != "aaaa--x--bbbb--x--cccc" {
		t.Fatalf("data = %q", out.String())
	}
}

func TestReplaceDetailedPreservesExecutableMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "script.sh")
	if err := os.WriteFile(path, []byte("#!/usr/bin/env bash\necho old\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReplaceDetailed(path, "old", "new", false); err != nil {
		t.Fatal(err)
	}
	assertFileMode(t, path, 0755)
}

func TestMultiReplaceDetailedPreservesExecutableMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "script.sh")
	if err := os.WriteFile(path, []byte("#!/usr/bin/env bash\necho old\necho again\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := MultiReplaceDetailed(path, []Edit{
		{OldString: "old", NewString: "new"},
		{OldString: "again", NewString: "done"},
	}); err != nil {
		t.Fatal(err)
	}
	assertFileMode(t, path, 0755)
}

func TestReplaceDetailedPreservesNonExecutableMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"mode":"old"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReplaceDetailed(path, "old", "new", false); err != nil {
		t.Fatal(err)
	}
	assertFileMode(t, path, 0600)
}

func assertFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode = %04o, want %04o", got, want)
	}
}

func normalizeManifestGolden(text, path string) string {
	text = strings.ReplaceAll(text, path, "<PATH>")
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "sha256_prefix: ") {
			lines[i] = "sha256_prefix: <SHA256_PREFIX>"
		}
	}
	return strings.Join(lines, "\n")
}

func TestReplaceDetailedNotFoundIsActionable(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("hello world\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, _, err := ReplaceDetailed(p, "NOPE-not-present", "x", false)
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"was not found", p, "Re-read", "do not retry"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q: %v", want, err)
		}
	}
}

func TestMultiReplaceDetailedNotFoundIsActionable(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("hello world\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, _, err := MultiReplaceDetailed(p, []Edit{{OldString: "NOPE", NewString: "x"}})
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"edit 0", "was not found", p, "Re-read"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q: %v", want, err)
		}
	}
}
