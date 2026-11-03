package filewrite

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/konglong87/super-niuma/internal/tools"
)

type Tool struct{}

func New() Tool { return Tool{} }

func (Tool) Name() string { return "Write" }

func (Tool) MaxResultSizeChars() int { return tools.ClaudeCodeDefaultDeclaredMaxResultSizeChars }

func (Tool) Description() string {
	return `Write a complete UTF-8 text file. This replaces the file if it already exists.

Prefer Edit for small changes to existing files — Write replaces the entire file,
which is more error-prone for partial modifications. Use Write when:
- Creating a new file
- The file needs a complete rewrite (more than ~40% changed)
- The file is small enough to reproduce in full

When replacing an existing script, hook, or entrypoint file, verify the file mode
and executable bit after writing.

The file path is relative to the current working directory.
Always verify the file path before writing — writing to the wrong path
can silently overwrite important files.`
}

func (Tool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
	  "type": "object",
	  "properties": {
	    "file_path": {"type": "string", "description": "Absolute path or path relative to the current working directory."},
	    "content": {"type": "string", "description": "Full file contents to write."}
	  },
	  "required": ["file_path", "content"],
	  "additionalProperties": false
	}`)
}

func (Tool) Run(_ context.Context, input json.RawMessage, toolContext tools.Context) tools.Result {
	var params struct {
		FilePath string `json:"file_path"`
		Content  string `json:"content"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	path, err := tools.ResolvePath(toolContext.CWD, params.FilePath)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if err := tools.EnsureWritablePathWithSandbox(toolContext.CWD, toolContext.WritableRoots, path, toolContext.Sandbox); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	beforeMode, beforeModeOK := fileMode(path)
	before, beforeExists, err := readExisting(path)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if err := os.WriteFile(path, []byte(params.Content), 0644); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	afterMode, afterModeOK := fileMode(path)
	if toolContext.FileChange != nil {
		toolContext.FileChange(tools.FileChange{
			Path:            path,
			Before:          before,
			BeforeExists:    beforeExists,
			After:           params.Content,
			AfterExists:     true,
			BeforeMode:      beforeMode,
			AfterMode:       afterMode,
			BeforeModeKnown: beforeModeOK,
			AfterModeKnown:  afterModeOK,
			ModeChanged:     beforeModeOK && afterModeOK && beforeMode != afterMode,
		})
	}
	return tools.Result{Content: fmt.Sprintf("Wrote %s", path)}
}

func readExisting(path string) (string, bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return string(data), true, nil
}

func fileMode(path string) (os.FileMode, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	return info.Mode().Perm(), true
}
