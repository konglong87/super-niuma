//go:build integration

package buildinfo_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/buildinfo"
	"github.com/konglong87/super-niuma/internal/product"
)

func TestBuildScriptInjectsReproducibleIdentity(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	binary := filepath.Join(t.TempDir(), "niuma")
	command := exec.Command("bash", "scripts/build.sh")
	command.Dir = repositoryRoot
	command.Env = append(os.Environ(),
		"GOTOOLCHAIN=local",
		"VERSION=v-build-test",
		"SOURCE_DATE_EPOCH=1786417445",
		"OUTPUT="+binary,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("scripts/build.sh failed: %v\n%s", err, output)
	}

	jsonOutput, err := exec.Command(binary, "version", "--json").Output()
	if err != nil {
		t.Fatal(err)
	}
	var identity buildinfo.Info
	if err := json.Unmarshal(jsonOutput, &identity); err != nil {
		t.Fatalf("decode build identity: %v\n%s", err, jsonOutput)
	}
	if identity.Version != "v-build-test" || identity.BuildTime != "2026-08-11T03:04:05Z" || identity.Revision == "" || !identity.DirtyKnown || identity.GoToolchain == "" {
		t.Fatalf("built identity = %+v", identity)
	}

	if identity.Product != product.Name {
		t.Fatalf("built product = %q, want %q", identity.Product, product.Name)
	}
	if strings.Contains(string(jsonOutput), repositoryRoot) {
		t.Fatalf("build identity leaked repository path: %s", jsonOutput)
	}
}
