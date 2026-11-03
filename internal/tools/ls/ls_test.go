package ls

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/tools"
)

func TestLSListsDirectoryAndIgnoresPatterns(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "a.txt"), []byte("a"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "skip.log"), []byte("skip"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(tmp, "dir"), 0755); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]any{"ignore": []string{"*.log"}})
	res := New().Run(context.Background(), input, tools.Context{CWD: tmp})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	if !strings.Contains(res.Content, "a.txt") || !strings.Contains(res.Content, "dir/") || strings.Contains(res.Content, "skip.log") {
		t.Fatalf("content = %s", res.Content)
	}
}

func TestLSDescriptionEncouragesPathConfirmation(t *testing.T) {
	desc := New().Description()
	for _, want := range []string{
		"confirm a directory or path segment before Read",
		"path is uncertain",
		"Do not continue listing",
		"identified the files needed to answer",
	} {
		if !strings.Contains(desc, want) {
			t.Fatalf("description missing %q:\n%s", want, desc)
		}
	}
}
