package bashoutput

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/konglong87/super-niuma/internal/background"
	"github.com/konglong87/super-niuma/internal/tools"
)

type KillTool struct {
	store background.Store
}

func NewKillShell() KillTool { return KillTool{store: background.DefaultStore()} }

func (KillTool) Name() string { return "KillShell" }

func (KillTool) MaxResultSizeChars() int { return tools.ClaudeCodeDefaultDeclaredMaxResultSizeChars }

func (KillTool) Description() string {
	return `Kill a background command started with Bash(run_in_background=true).

Use KillShell when a background command must stop: a dev server or watcher you
started and no longer need, a command that hangs or loops, or anything still
running when its work is done. Nothing else can stop a background command, so
leaving one running leaks a process for the rest of the session.

The outcome field says what happened: "killed" (the process was running and was
terminated), "not_running" (it had already exited, nothing to do) or
"not_found" (no background command has that id). Use BashOutput afterwards to
read whatever output the command produced before it stopped.`
}

func (KillTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
	  "type": "object",
	  "properties": {
	    "shell_id": {"type": "string", "description": "Background command id, i.e. the \"id\" field returned by Bash with run_in_background=true."}
	  },
	  "required": ["shell_id"],
	  "additionalProperties": false
	}`)
}

type killPayload struct {
	ID       string `json:"id"`
	Outcome  string `json:"outcome"`
	Status   string `json:"status,omitempty"`
	LogPath  string `json:"log_path,omitempty"`
	Message  string `json:"message"`
	ExitCode *int   `json:"exit_code,omitempty"`
}

func (t KillTool) Run(_ context.Context, input json.RawMessage, _ tools.Context) tools.Result {
	var params struct {
		ShellID string `json:"shell_id"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	id := strings.TrimSpace(params.ShellID)
	if id == "" {
		return tools.Result{Content: "shell_id is required", IsError: true}
	}
	job, outcome, err := t.store.Terminate(id)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	payload := killPayload{ID: id, Outcome: string(outcome)}
	switch outcome {
	case background.TerminateNotFound:
		payload.Message = notFoundMessage(id)
	case background.TerminateKilled:
		payload.Status = job.Status
		payload.LogPath = job.LogPath
		payload.Message = "Process terminated. Use BashOutput to read any output it produced before stopping."
	default:
		exitCode := job.ExitCode
		payload.Status = job.Status
		payload.LogPath = job.LogPath
		payload.ExitCode = &exitCode
		payload.Message = "The command was not running anymore, so nothing was killed. Use BashOutput to read its output."
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	return tools.Result{Content: string(data), IsError: outcome == background.TerminateNotFound}
}
