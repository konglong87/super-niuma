package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/konglong87/super-niuma/internal/buildinfo"
)

func TestMain(m *testing.M) {
	buildinfo.Version = "test-version"
	root, err := os.MkdirTemp("", "niuma-cli-transcripts-")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", filepath.Join(root, "projects")); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(root)
	os.Exit(code)
}

func TestVersionCommandJSONUsesUnifiedBuildInfo(t *testing.T) {
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"version", "--json"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var got buildinfo.Info
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("version --json output is not JSON: %v\n%s", err, out.String())
	}
	if got.SchemaVersion != buildinfo.SchemaVersion || got.Product != buildinfo.Product || got.Version != "test-version" || got.GoToolchain == "" {
		t.Fatalf("version --json = %+v", got)
	}
}

func TestVersionCommandRejectsUnknownOptions(t *testing.T) {
	err := Run(context.Background(), []string{"version", "--wat"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("version --wat must fail")
	}
}
