package todowrite

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/tools"
)

type Tool struct{}
type ReadTool struct{}

type Todo struct {
	ID         string `json:"id,omitempty"`
	Content    string `json:"content"`
	ActiveForm string `json:"activeForm,omitempty"`
	Status     string `json:"status"`
	Priority   string `json:"priority,omitempty"`
}

func New() Tool         { return Tool{} }
func NewRead() ReadTool { return ReadTool{} }

func (ReadTool) Name() string { return "TodoRead" }

func (ReadTool) MaxResultSizeChars() int { return tools.ClaudeCodeDefaultDeclaredMaxResultSizeChars }

func (ReadTool) Description() string {
	return `Read the current task todo list for the session.

Use TodoRead to check progress on multi-step work. Returns the list of todos
with their IDs, content, status (pending/in_progress/completed), and priority.`
}

func (ReadTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
	  "type": "object",
	  "properties": {},
	  "additionalProperties": false
	}`)
}

func (ReadTool) Run(_ context.Context, input json.RawMessage, toolContext tools.Context) tools.Result {
	if len(input) > 0 {
		var params map[string]any
		if err := json.Unmarshal(input, &params); err != nil {
			return tools.Result{Content: err.Error(), IsError: true}
		}
	}
	path := todoPath(toolContext.CWD)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return tools.Result{Content: "[]"}
		}
		return tools.Result{Content: err.Error(), IsError: true}
	}
	return tools.Result{Content: string(data)}
}

func (Tool) Name() string { return "TodoWrite" }

func (Tool) MaxResultSizeChars() int { return tools.ClaudeCodeDefaultDeclaredMaxResultSizeChars }

func (Tool) Description() string {
	return `Create or replace the current task todo list for the session.

Use TodoWrite for multi-step work to track progress. Keep at most one item
in_progress at a time. Each todo has content, optional activeForm for the
present-continuous in-progress label, status (pending/in_progress/completed),
and optional priority (low/medium/high). TodoWrite writes .niuma/todos.json.

Only use TodoWrite when file writes are allowed and the user has not requested
read-only analysis, review-only work, or "do not modify files". For read-only
or no-modify tasks, use TodoRead to inspect an existing list, or keep the
checklist in your assistant response instead of calling this tool.`
}

func (Tool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
	  "type": "object",
	  "properties": {
	    "todos": {
	      "type": "array",
	      "description": "Complete replacement todo list. This writes .niuma/todos.json; do not use for read-only or no-modify tasks.",
	      "items": {
	        "type": "object",
	        "properties": {
	          "id": {"type": "string"},
	          "content": {"type": "string"},
	          "activeForm": {"type": "string", "description": "Optional present-continuous form shown while this todo is in_progress, e.g. Running tests."},
	          "status": {"type": "string", "enum": ["pending", "in_progress", "completed"]},
	          "priority": {"type": "string", "enum": ["low", "medium", "high"]}
	        },
	        "required": ["content", "status"],
	        "additionalProperties": false
	      }
	    }
	  },
	  "required": ["todos"],
	  "additionalProperties": false
	}`)
}

func (Tool) Run(_ context.Context, input json.RawMessage, toolContext tools.Context) tools.Result {
	var params struct {
		Todos []Todo `json:"todos"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	inProgress := 0
	completed := 0
	for i := range params.Todos {
		if params.Todos[i].Content == "" {
			return tools.Result{Content: fmt.Sprintf("todo %d content is required", i), IsError: true}
		}
		if params.Todos[i].ActiveForm != "" && strings.TrimSpace(params.Todos[i].ActiveForm) == "" {
			return tools.Result{Content: fmt.Sprintf("todo %d activeForm is blank", i), IsError: true}
		}
		if !validStatus(params.Todos[i].Status) {
			return tools.Result{Content: fmt.Sprintf("todo %d has invalid status %q", i, params.Todos[i].Status), IsError: true}
		}
		if params.Todos[i].Status == "in_progress" {
			inProgress++
		}
		if params.Todos[i].Status == "completed" {
			completed++
		}
		if !validPriority(params.Todos[i].Priority) {
			return tools.Result{Content: fmt.Sprintf("todo %d has invalid priority %q", i, params.Todos[i].Priority), IsError: true}
		}
		if params.Todos[i].ID == "" {
			params.Todos[i].ID = fmt.Sprintf("todo-%d", i+1)
		}
	}
	if inProgress > 1 {
		return tools.Result{Content: "only one todo can be in_progress at a time", IsError: true}
	}
	path := todoPath(toolContext.CWD)
	if err := tools.EnsureWritablePathWithSandbox(toolContext.CWD, toolContext.WritableRoots, path, toolContext.Sandbox); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	todosToPersist := params.Todos
	allCompleted := len(params.Todos) > 0 && completed == len(params.Todos)
	if allCompleted {
		todosToPersist = []Todo{}
	}
	data, err := json.MarshalIndent(todosToPersist, "", "  ")
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	pending := len(params.Todos) - inProgress - completed
	return tools.Result{Content: todoWriteSuccessMessage(len(params.Todos), pending, inProgress, completed, allCompleted, path)}
}

func todoWriteSuccessMessage(total, pending, inProgress, completed int, allCompleted bool, path string) string {
	message := "Todos have been modified successfully. Ensure that you continue to use the todo list to track your progress. Please proceed with the current tasks if applicable."
	message += fmt.Sprintf("\n\nSummary: %d total, %d pending, %d in_progress, %d completed.", total, pending, inProgress, completed)
	if allCompleted {
		message += " All todos are completed, so the persisted session todo state was cleared to avoid carrying stale completed tasks into future turns."
	}
	if path != "" {
		message += "\nState file: " + path
	}
	return message
}

func validStatus(status string) bool {
	switch status {
	case "pending", "in_progress", "completed":
		return true
	default:
		return false
	}
}

func validPriority(priority string) bool {
	switch priority {
	case "", "low", "medium", "high":
		return true
	default:
		return false
	}
}

func todoPath(cwd string) string {
	return config.CurrentIdentity(cwd).ProjectStatePath(cwd, "todos.json")
}

func Load(cwd string) ([]Todo, error) {
	data, err := os.ReadFile(todoPath(cwd))
	if err != nil {
		if os.IsNotExist(err) {
			return []Todo{}, nil
		}
		return nil, err
	}
	var todos []Todo
	if err := json.Unmarshal(data, &todos); err != nil {
		return nil, fmt.Errorf("parse todo state: %w", err)
	}
	return todos, nil
}
