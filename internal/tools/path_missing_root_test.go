package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/tools"
	"github.com/konglong87/super-niuma/internal/tools/filewrite"
)

func TestWriteCreatesMissingRootThroughSymlinkParent(t *testing.T) {
	for _, alias := range []bool{false, true} {
		name := "canonical"
		if alias {
			name = "symlink-parent"
		}
		t.Run(name, func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			workspace := filepath.Join(base, "workspace")
			physical := filepath.Join(base, "config")
			for _, dir := range []string{workspace, physical} {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			config := physical
			if alias {
				config = filepath.Join(base, "config-link")
				if err := os.Symlink(physical, config); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			}
			root := filepath.Join(config, "projects", "fixture", "memory")
			target := filepath.Join(root, "topic.md")
			const body = "synthetic durable memory"
			input, _ := json.Marshal(map[string]string{"file_path": target, "content": body})
			result := filewrite.New().Run(context.Background(), input, tools.Context{CWD: workspace, WritableRoots: []string{root}})
			if result.IsError {
				t.Fatalf("first write under declared memory root: %s", result.Content)
			}
			data, err := os.ReadFile(target)
			if err != nil || string(data) != body {
				t.Fatalf("physical file: %q, %v", data, err)
			}
			outside := filepath.Join(base, "outside")
			if err := os.MkdirAll(outside, 0700); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(root, "escape")
			if err := os.Symlink(outside, link); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
			input, _ = json.Marshal(map[string]string{"file_path": filepath.Join(link, "private.md"), "content": body})
			rejected := filewrite.New().Run(context.Background(), input, tools.Context{CWD: workspace, WritableRoots: []string{root}})
			if !rejected.IsError || !strings.Contains(rejected.Content, "outside") {
				t.Fatalf("escape was not denied: %+v", rejected)
			}
			if _, err := os.Stat(filepath.Join(outside, "private.md")); !os.IsNotExist(err) {
				t.Fatalf("outside file was created: %v", err)
			}
		})
	}
}
