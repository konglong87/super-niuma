package memorylint

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCheckFilesFindsOnlyMissingLocalLinks(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "CLAUDE.md")
	mustWrite(t, filepath.Join(dir, "docs", "exists.md"), "ok")
	mustWrite(t, source, strings.Join([]string{
		"[exists](docs/exists.md)",
		"[missing](docs/missing.md)",
		"![missing image](images/missing.png)",
		"[external](https://example.com/a.md)",
		"[anchor](#heading)",
		"`[inline code](ignored.md)`",
		"```md",
		"[fenced code](ignored.md)",
		"```",
		"<!-- [comment](ignored.md) -->",
	}, "\n"))

	report, err := CheckFiles([]string{source, source})
	if err != nil {
		t.Fatal(err)
	}
	if report.Documents != 1 {
		t.Fatalf("documents = %d, want 1", report.Documents)
	}
	if len(report.Findings) != 2 {
		t.Fatalf("findings = %+v, want 2", report.Findings)
	}
	if report.Findings[0].Line != 2 || report.Findings[0].Target != "docs/missing.md" {
		t.Fatalf("first finding = %+v", report.Findings[0])
	}
	if report.Findings[1].Line != 3 || report.Findings[1].Target != "images/missing.png" {
		t.Fatalf("second finding = %+v", report.Findings[1])
	}
}

func TestCheckFilesHandlesCommonMarkDestinations(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "rules.md")
	for _, name := range []string{"space file.md", "foo(bar).md", "&file.md", "afile.md", "percent%20literal.md", "reference.md"} {
		mustWrite(t, filepath.Join(dir, name), "ok")
	}
	mustWrite(t, source, strings.Join([]string{
		"[space](space%20file.md \"title\")",
		"[angle](<space file.md>)",
		`[escaped](foo\(bar\).md)`,
		"[entity](&amp;file.md)",
		"[numeric entity](&#97;file.md)",
		"[single percent decode](percent%2520literal.md)",
		"[query and fragment](reference.md?raw=1#top)",
		"[reference][ref]",
		"",
		"[ref]: reference.md",
	}, "\n"))

	report, err := CheckFiles([]string{source})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 0 {
		t.Fatalf("unexpected findings: %+v", report.Findings)
	}
}

func TestCheckFilesPreservesLinkLineNumbers(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "rules.md")
	mustWrite(t, source, strings.Join([]string{
		"# title",
		"[**emphasis**](emphasis.md)",
		"[](empty.md)",
		"[multi",
		"line](multiline.md)",
	}, "\n"))

	report, err := CheckFiles([]string{source})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 3 {
		t.Fatalf("findings = %+v, want 3", report.Findings)
	}
	// Empty labels have no text segment, so their documented fallback is the
	// first line of the containing paragraph.
	wantLines := map[string]int{"emphasis.md": 2, "empty.md": 2, "multiline.md": 4}
	for _, finding := range report.Findings {
		if want := wantLines[finding.Target]; finding.Line != want {
			t.Errorf("%s line = %d, want %d", finding.Target, finding.Line, want)
		}
	}
}

func TestCheckFilesMasksFrontmatterAndPreservesBodyLine(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
	}{
		{name: "lf", content: "---\ntitle: '[hidden](hidden.md)'\n---\n[body](missing.md)\n"},
		{name: "crlf", content: "---\r\ntitle: '[hidden](hidden.md)'\r\n---\r\n[body](missing.md)\r\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "rules.md")
			mustWrite(t, source, tc.content)
			report, err := CheckFiles([]string{source})
			if err != nil {
				t.Fatal(err)
			}
			if len(report.Findings) != 1 || report.Findings[0].Target != "missing.md" || report.Findings[0].Line != 4 {
				t.Fatalf("findings = %+v", report.Findings)
			}
		})
	}
}

func TestMaskFrontmatterLeavesUnclosedInputUntouched(t *testing.T) {
	source := []byte("---\ntitle: '[hidden](hidden.md)'\nbody\n")
	if got := maskFrontmatterPreserveLines(source); string(got) != string(source) {
		t.Fatalf("unclosed frontmatter changed:\n%s", got)
	}
}

func TestCheckFilesChecksContentAfterProjectMemoryLimit(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "MEMORY.md")
	lines := make([]string, 201)
	for i := range 200 {
		lines[i] = "plain text"
	}
	lines[200] = "[tail](missing.md)"
	mustWrite(t, source, strings.Join(lines, "\n"))

	report, err := CheckFiles([]string{source})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 1 || report.Findings[0].Line != 201 {
		t.Fatalf("findings = %+v", report.Findings)
	}
}

func TestCheckFilesClassifiesFileErrors(t *testing.T) {
	t.Run("source read error", func(t *testing.T) {
		_, err := checkFiles([]string{"source.md"}, fileOps{
			readFile: func(string) ([]byte, error) { return nil, fs.ErrPermission },
			stat:     func(string) (fs.FileInfo, error) { t.Fatal("stat called"); return nil, nil },
		})
		if !errors.Is(err, fs.ErrPermission) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("target permission error", func(t *testing.T) {
		_, err := checkFiles([]string{"source.md"}, fileOps{
			readFile: func(string) ([]byte, error) { return []byte("[target](target.md)"), nil },
			stat:     func(string) (fs.FileInfo, error) { return nil, fs.ErrPermission },
		})
		if !errors.Is(err, fs.ErrPermission) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("external targets do not stat", func(t *testing.T) {
		statCalls := 0
		report, err := checkFiles([]string{"source.md"}, fileOps{
			readFile: func(string) ([]byte, error) {
				return []byte(`[web](https://example.com) [mail](mailto:a@example.com) [file](file:///tmp/a) [network](//host/a) [unc](%5C%5Chost%5Cshare%5Ca.md)`), nil
			},
			stat: func(string) (fs.FileInfo, error) { statCalls++; return nil, fs.ErrNotExist },
		})
		if err != nil {
			t.Fatal(err)
		}
		if statCalls != 0 || len(report.Findings) != 0 {
			t.Fatalf("stat calls = %d, findings = %+v", statCalls, report.Findings)
		}
	})
}

func TestCheckFilesDeduplicatesAndSortsFindings(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.md")
	b := filepath.Join(dir, "b.md")
	mustWrite(t, a, "[one](z.md) [duplicate](z.md)\n")
	mustWrite(t, b, "[two](x.md)\n")

	report, err := CheckFiles([]string{b, a})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 2 {
		t.Fatalf("findings = %+v", report.Findings)
	}
	if report.Findings[0].Path != a || report.Findings[0].Target != "z.md" || report.Findings[1].Path != b {
		t.Fatalf("findings not stable: %+v", report.Findings)
	}
}

func TestCheckFilesReturnsStableEmptyJSON(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "clean.md")
	mustWrite(t, source, "plain text")
	report, err := CheckFiles([]string{source})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"findings":[]`) {
		t.Fatalf("json = %s", data)
	}
}

func TestNormalizeLocalTargetAbsoluteAndNetworkPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix network-path ordering assertion")
	}
	dir := t.TempDir()
	if _, local, err := normalizeLocalTarget(dir, []byte("//host/path.md")); err != nil || local {
		t.Fatalf("network path local=%v err=%v", local, err)
	}
	if _, local, err := normalizeLocalTarget(dir, []byte(`\\\\host\share\path.md`)); err != nil || local {
		t.Fatalf("UNC path local=%v err=%v", local, err)
	}
	if _, local, err := normalizeLocalTarget(dir, []byte(`%5C%5Chost%5Cshare%5Cpath.md`)); err != nil || local {
		t.Fatalf("encoded UNC path local=%v err=%v", local, err)
	}
	target := filepath.Join(dir, "file with space.md")
	resolved, local, err := normalizeLocalTarget(dir, []byte(strings.ReplaceAll(target, " ", "%20")+"?raw=1#top"))
	if err != nil || !local || resolved != target {
		t.Fatalf("resolved=%q local=%v err=%v, want %q", resolved, local, err, target)
	}
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
