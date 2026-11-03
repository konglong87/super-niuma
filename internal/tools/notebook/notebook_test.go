package notebook

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/tools"
)

func TestNotebookReadAndEdit(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "demo.ipynb")
	if err := os.WriteFile(path, []byte(`{
	  "cells": [
	    {"cell_type": "markdown", "metadata": {}, "source": ["# Title\n"]},
	    {"cell_type": "code", "metadata": {}, "execution_count": null, "outputs": [{"output_type":"stream"}], "source": ["print('hi')\n"]}
	  ],
	  "metadata": {},
	  "nbformat": 4,
	  "nbformat_minor": 5
	}`), 0600); err != nil {
		t.Fatal(err)
	}

	readInput, _ := json.Marshal(map[string]any{"file_path": "demo.ipynb"})
	res := NewRead().Run(context.Background(), readInput, tools.Context{CWD: tmp})
	if res.IsError || !strings.Contains(res.Content, "Cell 1 [code]") || !strings.Contains(res.Content, "Outputs: 1") {
		t.Fatalf("read = %+v", res)
	}

	editInput, _ := json.Marshal(map[string]any{
		"file_path":  "demo.ipynb",
		"cell_index": 1,
		"new_source": "print('bye')\n",
	})
	res = NewEdit().Run(context.Background(), editInput, tools.Context{CWD: tmp})
	if res.IsError {
		t.Fatalf("edit = %+v", res)
	}
	readInput, _ = json.Marshal(map[string]any{"file_path": "demo.ipynb", "cell_index": 1})
	res = NewRead().Run(context.Background(), readInput, tools.Context{CWD: tmp})
	if res.IsError || !strings.Contains(res.Content, "print('bye')") {
		t.Fatalf("read edited = %+v", res)
	}
}

// TestNotebookEditPreservesUnknownFields locks AUDIT-P1-15: editing one cell used
// to drop the nbformat 4.5 mandatory cell `id` plus every key the struct did not
// name, silently producing a schema-invalid notebook.
func TestNotebookEditPreservesUnknownFields(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "demo.ipynb")
	original := `{
	  "cells": [
	    {"cell_type": "code", "id": "abc12345", "metadata": {"tags": ["keep"]}, "execution_count": 3,
	     "outputs": [{"output_type": "stream", "name": "stdout", "text": ["hi\n"]}],
	     "source": ["print('hi')\n"], "attachments": {"img.png": {"image/png": "AAA"}}},
	    {"cell_type": "markdown", "id": "def67890", "metadata": {}, "source": ["# Title\n"]}
	  ],
	  "metadata": {"kernelspec": {"name": "python3"}},
	  "nbformat": 4,
	  "nbformat_minor": 5,
	  "extra_top_level": {"vendor": "custom"}
	}`
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}

	editInput, _ := json.Marshal(map[string]any{
		"file_path": "demo.ipynb", "cell_index": 0, "new_source": "print('bye')\n",
	})
	if res := NewEdit().Run(context.Background(), editInput, tools.Context{CWD: tmp, WritableRoots: []string{tmp}}); res.IsError {
		t.Fatalf("edit failed: %s", res.Content)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	if got["extra_top_level"] == nil {
		t.Error("top-level unknown key extra_top_level was dropped")
	}
	cells, _ := got["cells"].([]any)
	if len(cells) != 2 {
		t.Fatalf("cells = %d, want 2", len(cells))
	}
	edited, _ := cells[0].(map[string]any)
	if edited["id"] != "abc12345" {
		t.Errorf("edited cell id = %v, want abc12345 (nbformat 4.5 requires it)", edited["id"])
	}
	if edited["attachments"] == nil {
		t.Error("edited cell unknown key attachments was dropped")
	}
	if fmt.Sprint(edited["execution_count"]) != "3" {
		t.Errorf("execution_count = %v, want 3", edited["execution_count"])
	}
	if src := fmt.Sprint(edited["source"]); !strings.Contains(src, "print('bye')") {
		t.Errorf("source not updated: %v", edited["source"])
	}
	untouched, _ := cells[1].(map[string]any)
	if untouched["id"] != "def67890" {
		t.Errorf("untouched cell id = %v, want def67890", untouched["id"])
	}
}
