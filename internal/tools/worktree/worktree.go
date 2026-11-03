package worktree

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/tools"
)

type Tool struct{}

func New() Tool { return Tool{} }

func (Tool) Name() string { return "Worktree" }

func (Tool) MaxResultSizeChars() int { return tools.ClaudeCodeDefaultDeclaredMaxResultSizeChars }

func (Tool) Description() string {
	return `Manage git worktrees for isolated coding branches: list, add, and remove.

Use Worktree to create isolated working directories on separate branches without
affecting the main working tree. This is useful for parallel development or
testing changes in isolation. Git commands timeout at 60 seconds.`
}

func (Tool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
	  "type": "object",
	  "properties": {
	    "action": {"type": "string", "enum": ["list", "add", "remove"], "description": "Worktree action."},
	    "path": {"type": "string", "description": "Worktree path for add/remove."},
	    "branch": {"type": "string", "description": "Branch name for add."},
	    "base": {"type": "string", "description": "Optional base commit/branch for add."},
	    "force": {"type": "boolean", "description": "Use --force for remove."}
	  },
	  "required": ["action"],
	  "additionalProperties": false
	}`)
}

func (Tool) Run(ctx context.Context, input json.RawMessage, toolContext tools.Context) tools.Result {
	var params struct {
		Action string `json:"action"`
		Path   string `json:"path"`
		Branch string `json:"branch"`
		Base   string `json:"base"`
		Force  bool   `json:"force"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	switch strings.ToLower(strings.TrimSpace(params.Action)) {
	case "list":
		return runGit(ctx, toolContext.CWD, "worktree", "list", "--porcelain")
	case "add":
		if strings.TrimSpace(params.Path) == "" || strings.TrimSpace(params.Branch) == "" {
			return tools.Result{Content: "path and branch are required for worktree add", IsError: true}
		}
		args := []string{"worktree", "add", "-b", params.Branch, params.Path}
		if strings.TrimSpace(params.Base) != "" {
			args = append(args, params.Base)
		}
		return runGit(ctx, toolContext.CWD, args...)
	case "remove":
		if strings.TrimSpace(params.Path) == "" {
			return tools.Result{Content: "path is required for worktree remove", IsError: true}
		}
		args := []string{"worktree", "remove"}
		if params.Force {
			args = append(args, "--force")
		}
		args = append(args, params.Path)
		return runGit(ctx, toolContext.CWD, args...)
	default:
		return tools.Result{Content: "unsupported Worktree action: " + params.Action, IsError: true}
	}
}

func runGit(ctx context.Context, cwd string, args ...string) tools.Result {
	if strings.TrimSpace(cwd) == "" {
		cwd = "."
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = cwd
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	text := strings.TrimSpace(out.String())
	if ctx.Err() == context.DeadlineExceeded {
		return tools.Result{Content: fmt.Sprintf("git command timed out\n%s", text), IsError: true}
	}
	if err != nil {
		return tools.Result{Content: fmt.Sprintf("%s\n%s", err.Error(), text), IsError: true}
	}
	if text == "" {
		text = "(ok)"
	}
	return tools.Result{Content: text}
}
