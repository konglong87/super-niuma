package tenantpkg

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportDirectoryRendersDeterministicRuntimeAndManifest(t *testing.T) {
	root := t.TempDir()
	writePackageFile(t, root, "SKILL.md", "# Teach V2\n\nUse tenant instructions.")
	writePackageFile(t, root, "docs/runtime.md", "Runtime detail.")
	writePackageFile(t, root, "examples/decision.json", `{"ok":true}`)
	writePackageFile(t, root, "assets/logo.svg", "<svg></svg>")
	writePackageFile(t, root, "scripts/build.sh", "echo build")

	pkg, err := ImportDirectory(root, ImportOptions{SkillKey: "teach-v2"})
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Manifest.SchemaVersion != SchemaVersion {
		t.Fatalf("schema_version = %q", pkg.Manifest.SchemaVersion)
	}
	if pkg.Manifest.SkillKey != "teach-v2" {
		t.Fatalf("skill_key = %q", pkg.Manifest.SkillKey)
	}
	if len(pkg.PackageSHA256) != 64 || pkg.Manifest.PackageSHA256 != pkg.PackageSHA256 {
		t.Fatalf("package sha mismatch: %q manifest=%q", pkg.PackageSHA256, pkg.Manifest.PackageSHA256)
	}
	if !strings.Contains(pkg.RuntimeMD, "# Teach V2") || !strings.Contains(pkg.RuntimeMD, "## Package file: docs/runtime.md") {
		t.Fatalf("runtime missing expected content:\n%s", pkg.RuntimeMD)
	}
	if strings.Contains(pkg.RuntimeMD, "logo.svg") || strings.Contains(pkg.RuntimeMD, "echo build") {
		t.Fatalf("runtime included excluded asset/script content:\n%s", pkg.RuntimeMD)
	}
	if got := strings.Join(pkg.Manifest.Render.RuntimeFiles, ","); got != "SKILL.md,docs/runtime.md,examples/decision.json" {
		t.Fatalf("runtime files = %s", got)
	}
}

func TestImportDirectoryRespectsPackageRuntimeIncludesAndExcludes(t *testing.T) {
	root := t.TempDir()
	writePackageFile(t, root, "SKILL.md", "# Teach V2\n\nUse tenant instructions.")
	writePackageFile(t, root, "package.yaml", `runtime_includes:
  - runtime.md
  - docs/*.md
  - examples/decision.json
runtime_excludes:
  - docs/release-notes.md
`)
	writePackageFile(t, root, "runtime.md", "Runtime contract.")
	writePackageFile(t, root, "README.md", "Authoring notes that should stay out.")
	writePackageFile(t, root, "docs/contract.md", "Calibration contract.")
	writePackageFile(t, root, "docs/release-notes.md", "Release notes should stay out.")
	writePackageFile(t, root, "examples/decision.json", `{"decision":"calibration"}`)
	writePackageFile(t, root, "examples/large.json", `{"large":true}`)

	pkg, err := ImportDirectory(root, ImportOptions{SkillKey: "teach-v2"})
	if err != nil {
		t.Fatal(err)
	}
	wantFiles := "SKILL.md,docs/contract.md,examples/decision.json,runtime.md"
	if got := strings.Join(pkg.Manifest.Render.RuntimeFiles, ","); got != wantFiles {
		t.Fatalf("runtime files = %s want %s", got, wantFiles)
	}
	for _, want := range []string{"# Teach V2", "Runtime contract.", "Calibration contract.", `{"decision":"calibration"}`} {
		if !strings.Contains(pkg.RuntimeMD, want) {
			t.Fatalf("runtime missing %q:\n%s", want, pkg.RuntimeMD)
		}
	}
	for _, blocked := range []string{"Authoring notes", "Release notes", `"large":true`, "runtime_includes"} {
		if strings.Contains(pkg.RuntimeMD, blocked) {
			t.Fatalf("runtime included blocked content %q:\n%s", blocked, pkg.RuntimeMD)
		}
	}
	if got := strings.Join(pkg.Manifest.Render.RuntimeIncludes, ","); got != "runtime.md,docs/*.md,examples/decision.json" {
		t.Fatalf("runtime includes = %s", got)
	}
	if got := strings.Join(pkg.Manifest.Render.RuntimeExcludes, ","); got != "docs/release-notes.md" {
		t.Fatalf("runtime excludes = %s", got)
	}
}

func TestImportDirectoryExcludesPackageManifestFromRuntime(t *testing.T) {
	root := t.TempDir()
	writePackageFile(t, root, "SKILL.md", "# Teach V2\n\nUse tenant instructions.")
	writePackageFile(t, root, "package.yaml", "description: package metadata\n")
	writePackageFile(t, root, "runtime.md", "Runtime contract.")

	pkg, err := ImportDirectory(root, ImportOptions{SkillKey: "teach-v2"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(pkg.Manifest.Render.RuntimeFiles, ","); got != "SKILL.md,runtime.md" {
		t.Fatalf("runtime files = %s", got)
	}
	if strings.Contains(pkg.RuntimeMD, "package metadata") {
		t.Fatalf("runtime included package manifest:\n%s", pkg.RuntimeMD)
	}
}

func TestImportDirectoryRejectsUnsafeRuntimePatterns(t *testing.T) {
	root := t.TempDir()
	writePackageFile(t, root, "SKILL.md", "# Bad\n")
	writePackageFile(t, root, "package.yaml", "runtime_includes:\n  - ../secret.md\n")

	_, err := ImportDirectory(root, ImportOptions{SkillKey: "bad"})
	if err == nil || !strings.Contains(err.Error(), "runtime_includes") {
		t.Fatalf("expected unsafe runtime pattern error, got %v", err)
	}
}

func TestImportZipRejectsUnsafePathsAndSymlinks(t *testing.T) {
	for _, tc := range []struct {
		name string
		file string
		mode os.FileMode
	}{
		{name: "dotdot", file: "../SKILL.md", mode: 0o644},
		{name: "absolute", file: "/tmp/SKILL.md", mode: 0o644},
		{name: "symlink", file: "SKILL.md", mode: os.ModeSymlink | 0o777},
		{name: "extension", file: "secret.pem", mode: 0o644},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "pkg.zip")
			writeZip(t, path, []zipEntry{{Name: tc.file, Body: "x", Mode: tc.mode}})
			_, err := ImportZip(path, ImportOptions{SkillKey: "bad"})
			if err == nil || !strings.Contains(err.Error(), ErrInvalidPackage.Error()) {
				t.Fatalf("expected invalid package error, got %v", err)
			}
		})
	}
}

func TestStoreSaveWritesPackageManifestAndRuntime(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "src")
	if err := os.Mkdir(source, 0o755); err != nil {
		t.Fatal(err)
	}
	writePackageFile(t, source, "SKILL.md", "# Review\n\nReview code.")
	pkg, err := ImportDirectory(source, ImportOptions{SkillKey: "review"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := (Store{Root: filepath.Join(root, "store")}).Save("tenant-a", "review", pkg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result.PackageRef, "file://") || !strings.HasPrefix(result.RuntimeRef, "file://") {
		t.Fatalf("refs = %+v", result)
	}
	for _, name := range []string{"package.skill.zip", "manifest.json", "runtime.md"} {
		if _, err := os.Stat(filepath.Join(result.Root, name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(result.Root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.PackageSHA256 != pkg.PackageSHA256 {
		t.Fatalf("manifest sha = %q want %q", manifest.PackageSHA256, pkg.PackageSHA256)
	}
}

func writePackageFile(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

type zipEntry struct {
	Name string
	Body string
	Mode os.FileMode
}

func writeZip(t *testing.T, path string, entries []zipEntry) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.Name, Method: zip.Deflate}
		header.SetMode(entry.Mode)
		writer, err := zw.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(entry.Body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}
