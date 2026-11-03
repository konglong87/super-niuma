package fileedit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/konglong87/super-niuma/internal/files"
	"github.com/konglong87/super-niuma/internal/tools"
)

type Tool struct{}
type MultiTool struct{}

func New() Tool           { return Tool{} }
func NewMulti() MultiTool { return MultiTool{} }

func (Tool) Name() string { return "Edit" }

func (Tool) MaxResultSizeChars() int { return tools.ClaudeCodeDefaultDeclaredMaxResultSizeChars }

func (Tool) Description() string {
	return `Replace one exact string in an existing UTF-8 text file.

Prefer Edit over Write for targeted changes — it's safer because it only modifies
the specified text. The old_string must appear exactly once in the file
(use replace_all:true to allow multiple occurrences).

White space and indentation in old_string MUST match the file exactly.
Read the file first to copy the exact text. Trailing spaces matter.

MultiEdit is preferred when you need to make multiple edits to the same file
— it applies all changes atomically so partial failures don't corrupt the file.
Edit preserves the existing file mode. For scripts or hooks, verify the
executable bit still matches the intended entrypoint behavior after editing.`
}

func (Tool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
	  "type": "object",
	  "properties": {
	    "file_path": {"type": "string", "description": "Absolute path or path relative to the current working directory."},
	    "old_string": {"type": "string", "description": "Exact text to replace. Must occur exactly once."},
	    "new_string": {"type": "string", "description": "Replacement text."},
	    "replace_all": {"type": "boolean", "description": "Replace all occurrences instead of requiring exactly one match."}
	  },
	  "required": ["file_path", "old_string", "new_string"],
	  "additionalProperties": false
	}`)
}

func (Tool) Run(_ context.Context, input json.RawMessage, toolContext tools.Context) tools.Result {
	var params struct {
		FilePath   string `json:"file_path"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
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
	change, count, err := files.ReplaceDetailed(path, params.OldString, params.NewString, params.ReplaceAll)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	afterMode, afterModeOK := fileMode(path)
	if toolContext.FileChange != nil {
		toolContext.FileChange(tools.FileChange{
			Path:               path,
			Before:             change.Before,
			BeforeExists:       true,
			After:              change.After,
			AfterExists:        true,
			BeforeSnapshotPath: change.BeforeSnapshotPath,
			AfterSnapshotPath:  change.AfterSnapshotPath,
			BeforeMode:         beforeMode,
			AfterMode:          afterMode,
			BeforeModeKnown:    beforeModeOK,
			AfterModeKnown:     afterModeOK,
			ModeChanged:        beforeModeOK && afterModeOK && beforeMode != afterMode,
		})
	}
	return tools.Result{Content: fmt.Sprintf("Updated %s (%d replacements)", path, count)}
}

func (MultiTool) Name() string { return "MultiEdit" }

func (MultiTool) MaxResultSizeChars() int { return tools.ClaudeCodeDefaultDeclaredMaxResultSizeChars }

func (MultiTool) Description() string {
	return `Apply multiple exact string replacements to one existing UTF-8 text file atomically.

Use MultiEdit instead of sequential Edit calls when making multiple changes to the same file.
All edits are applied together — if any edit fails, none are applied, preventing partial corruption.

Each edit's old_string must match the file exactly (whitespace, indentation, trailing spaces).
Read the file first to copy the exact text for each old_string.
MultiEdit preserves the existing file mode. For scripts or hooks, verify the
executable bit still matches the intended entrypoint behavior after editing.`
}

func (MultiTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
	  "type": "object",
	  "properties": {
	    "file_path": {"type": "string", "description": "Absolute path or path relative to the current working directory."},
	    "edits": {
	      "type": "array",
	      "items": {
	        "type": "object",
	        "properties": {
	          "old_string": {"type": "string"},
	          "new_string": {"type": "string"},
	          "replace_all": {"type": "boolean"}
	        },
	        "required": ["old_string", "new_string"],
	        "additionalProperties": false
	      }
	    }
	  },
	  "required": ["file_path", "edits"],
	  "additionalProperties": false
	}`)
}

func (MultiTool) Run(_ context.Context, input json.RawMessage, toolContext tools.Context) tools.Result {
	var params struct {
		FilePath string `json:"file_path"`
		Edits    []struct {
			OldString  string `json:"old_string"`
			NewString  string `json:"new_string"`
			ReplaceAll bool   `json:"replace_all"`
		} `json:"edits"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if len(params.Edits) == 0 {
		return tools.Result{Content: "edits must contain at least one edit", IsError: true}
	}
	path, err := tools.ResolvePath(toolContext.CWD, params.FilePath)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if err := tools.EnsureWritablePathWithSandbox(toolContext.CWD, toolContext.WritableRoots, path, toolContext.Sandbox); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	beforeMode, beforeModeOK := fileMode(path)
	edits := make([]files.Edit, 0, len(params.Edits))
	for i, edit := range params.Edits {
		if edit.OldString == "" {
			return tools.Result{Content: fmt.Sprintf("edit %d old_string must not be empty", i), IsError: true}
		}
		edits = append(edits, files.Edit{OldString: edit.OldString, NewString: edit.NewString, ReplaceAll: edit.ReplaceAll})
	}
	change, replacements, err := files.MultiReplaceDetailed(path, edits)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	afterMode, afterModeOK := fileMode(path)
	if toolContext.FileChange != nil {
		toolContext.FileChange(tools.FileChange{
			Path:               path,
			Before:             change.Before,
			BeforeExists:       true,
			After:              change.After,
			AfterExists:        true,
			BeforeSnapshotPath: change.BeforeSnapshotPath,
			AfterSnapshotPath:  change.AfterSnapshotPath,
			BeforeMode:         beforeMode,
			AfterMode:          afterMode,
			BeforeModeKnown:    beforeModeOK,
			AfterModeKnown:     afterModeOK,
			ModeChanged:        beforeModeOK && afterModeOK && beforeMode != afterMode,
		})
	}
	return tools.Result{Content: fmt.Sprintf("Applied %d edits to %s", replacements, path)}
}

func fileMode(path string) (os.FileMode, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	return info.Mode().Perm(), true
}
