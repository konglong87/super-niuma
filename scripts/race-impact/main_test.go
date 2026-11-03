package main

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestSelectPackagesIncludesReverseDependencies(t *testing.T) {
	root := t.TempDir()
	packages := []packageInfo{
		{ImportPath: "example/internal/base", Dir: filepath.Join(root, "internal/base"), Imports: nil},
		{ImportPath: "example/internal/middle", Dir: filepath.Join(root, "internal/middle"), Imports: []string{"example/internal/base"}},
		{ImportPath: "example/cmd/app", Dir: filepath.Join(root, "cmd/app"), TestImports: []string{"example/internal/middle"}},
		{ImportPath: "example/internal/unrelated", Dir: filepath.Join(root, "internal/unrelated"), Imports: nil},
	}

	got, err := selectPackages(root, packages, []string{"internal/base/base.go"})
	if err != nil {
		t.Fatal(err)
	}
	want := selection{
		Mode:     modeSome,
		Packages: []string{"example/cmd/app", "example/internal/base", "example/internal/middle"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("selection = %#v, want %#v", got, want)
	}
}

func TestSelectPackagesSkipsNonGoChanges(t *testing.T) {
	root := t.TempDir()
	packages := []packageInfo{{
		ImportPath: "example/internal/base",
		Dir:        filepath.Join(root, "internal/base"),
	}}

	got, err := selectPackages(root, packages, []string{"README.md", "web/package-lock.json"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != modeNone {
		t.Fatalf("selection mode = %q, want %q", got.Mode, modeNone)
	}
}

func TestSelectPackagesFallsBackForDependencyChanges(t *testing.T) {
	root := t.TempDir()
	packages := []packageInfo{{
		ImportPath: "example/internal/base",
		Dir:        filepath.Join(root, "internal/base"),
	}}

	got, err := selectPackages(root, packages, []string{"go.sum"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != modeFull {
		t.Fatalf("selection mode = %q, want %q", got.Mode, modeFull)
	}
}

func TestSelectPackagesFallsBackForDeletedPackage(t *testing.T) {
	root := t.TempDir()
	packages := []packageInfo{{
		ImportPath: "example/internal/base",
		Dir:        filepath.Join(root, "internal/base"),
	}}

	got, err := selectPackages(root, packages, []string{"internal/removed/value.go"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != modeFull {
		t.Fatalf("selection mode = %q, want %q", got.Mode, modeFull)
	}
}

func TestPackageForFileUsesDeepestPackageDirectory(t *testing.T) {
	root := t.TempDir()
	packages := []packageInfo{
		{ImportPath: "example/internal", Dir: filepath.Join(root, "internal")},
		{ImportPath: "example/internal/deep", Dir: filepath.Join(root, "internal/deep")},
	}

	got, ok := packageForFile(root, packages, "internal/deep/value.go")
	if !ok {
		t.Fatal("package was not found")
	}
	if got != "example/internal/deep" {
		t.Fatalf("package = %q, want example/internal/deep", got)
	}
}
