package lsp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/tools"
)

func TestLSPDefinitionsAndReferences(t *testing.T) {
	tmp := t.TempDir()
	source := `package sample

type Server struct{}

func NewServer() Server { return Server{} }

func use() { _ = NewServer() }
`
	if err := os.WriteFile(filepath.Join(tmp, "sample.go"), []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	res := New().Run(context.Background(), json.RawMessage(`{"action":"definition","query":"NewServer"}`), tools.Context{CWD: tmp})
	if res.IsError {
		t.Fatal(res.Content)
	}
	if !json.Valid([]byte(res.Content)) {
		t.Fatalf("invalid json: %s", res.Content)
	}
	if res.Content == "[]" {
		t.Fatalf("expected definition, got %s", res.Content)
	}

	res = New().Run(context.Background(), json.RawMessage(`{"action":"references","query":"NewServer"}`), tools.Context{CWD: tmp})
	if res.IsError {
		t.Fatal(res.Content)
	}
	var refs []reference
	if err := json.Unmarshal([]byte(res.Content), &refs); err != nil {
		t.Fatal(err)
	}
	if len(refs) < 2 {
		t.Fatalf("refs = %+v", refs)
	}
}

func TestLSPDiagnostics(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "broken.go"), []byte("package broken\nfunc broken("), 0644); err != nil {
		t.Fatal(err)
	}
	res := New().Run(context.Background(), json.RawMessage(`{"action":"diagnostics"}`), tools.Context{CWD: tmp})
	if res.IsError {
		t.Fatal(res.Content)
	}
	if res.Content == "[]" {
		t.Fatalf("expected diagnostic, got %s", res.Content)
	}
}

// TestDescriptionStatesActualCapability locks AUDIT-P1-15: the name "LSP" promises
// a language server. There is none — this is an in-process go/parser index with
// name-based reference matching — and the description has to say so.
func TestDescriptionStatesActualCapability(t *testing.T) {
	description := New().Description()
	for _, want := range []string{"not a language server", "go/parser", "no type resolution", "Go only"} {
		if !strings.Contains(strings.ToLower(description), strings.ToLower(want)) {
			t.Errorf("Description() does not disclose %q:\n%s", want, description)
		}
	}
}
