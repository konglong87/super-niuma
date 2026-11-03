package ls

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/konglong87/super-niuma/internal/tools"
)

type Tool struct{}

func New() Tool { return Tool{} }

func (Tool) Name() string { return "LS" }

func (Tool) ExecutionPolicy() tools.ExecutionPolicy {
	return tools.ExecutionPolicy{Concurrency: tools.ConcurrencyReadOnly}
}

func (Tool) MaxResultSizeChars() int { return tools.ClaudeCodeDefaultDeclaredMaxResultSizeChars }

func (Tool) Description() string {
	return `List files and directories in a path, marking directories with a trailing slash.

Use LS to explore directory structure before reading or editing files.
Results are limited to 1000 entries. Use the ignore parameter to hide
common noise (e.g., ignore: ["*.pyc", ".git"]).

Use LS to confirm a directory or path segment before Read when the exact file
path is uncertain. Do not continue listing once Grep or Glob has already
identified the files needed to answer.

For finding files by pattern across directories, use Glob instead.
For searching file contents, use Grep instead.`
}

func (Tool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
	  "type": "object",
	  "properties": {
	    "path": {"type": "string", "description": "Directory path to list. Defaults to current working directory."},
	    "ignore": {"type": "array", "items": {"type": "string"}, "description": "Optional glob patterns for entry names to hide."}
	  },
	  "additionalProperties": false
	}`)
}

func (Tool) Run(_ context.Context, input json.RawMessage, toolContext tools.Context) tools.Result {
	var params struct {
		Path   string   `json:"path"`
		Ignore []string `json:"ignore"`
	}
	if len(input) > 0 {
		if err := json.Unmarshal(input, &params); err != nil {
			return tools.Result{Content: err.Error(), IsError: true}
		}
	}
	path, err := tools.ResolvePath(toolContext.CWD, params.Path)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	var out []string
	for _, entry := range entries {
		name := entry.Name()
		if ignored(name, params.Ignore) {
			continue
		}
		if entry.IsDir() {
			name += "/"
		}
		out = append(out, name)
		if len(out) >= 1000 {
			out = append(out, fmt.Sprintf("... truncated after %d entries", len(out)))
			break
		}
	}
	if len(out) == 0 {
		return tools.Result{Content: "(empty)"}
	}
	return tools.Result{Content: strings.Join(out, "\n")}
}

func ignored(name string, patterns []string) bool {
	for _, pattern := range patterns {
		ok, err := filepath.Match(pattern, name)
		if err == nil && ok {
			return true
		}
	}
	return false
}
