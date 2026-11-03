// Package bashoutput implements the two tools that make a background Bash
// command observable and controllable: BashOutput drains its output
// incrementally, KillShell stops it. Without them a background command can
// only be watched by re-reading its whole log file and can never be stopped
// (AUDIT-P1-13).
package bashoutput

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/konglong87/super-niuma/internal/background"
	"github.com/konglong87/super-niuma/internal/tools"
)

type Tool struct {
	store background.Store
}

func New() Tool { return Tool{store: background.DefaultStore()} }

func (Tool) Name() string { return "BashOutput" }

func (Tool) MaxResultSizeChars() int { return tools.ClaudeCodeDefaultDeclaredMaxResultSizeChars }

func (Tool) Description() string {
	return `Read new output from a background command started with Bash(run_in_background=true).

Use BashOutput whenever you need to know how a background command is doing:
it returns only the output produced since your previous BashOutput call for
that command, plus whether the process is still running. This is the correct
way to poll a background command — do not Read its log file for this, because
Read replays the whole log every time and wastes context, and do not use sleep
loops to wait.

Each call returns at most a bounded chunk of output. When more_output is true,
call BashOutput again to get the rest. When running is false the command has
finished and exit_code / error describe how it ended; there is nothing more to
poll after the remaining output is drained.

To stop a background command that is still running, use KillShell.`
}

func (Tool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
	  "type": "object",
	  "properties": {
	    "bash_id": {"type": "string", "description": "Background command id, i.e. the \"id\" field returned by Bash with run_in_background=true."}
	  },
	  "required": ["bash_id"],
	  "additionalProperties": false
	}`)
}

type outputPayload struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	Running      bool   `json:"running"`
	Output       string `json:"output"`
	MoreOutput   bool   `json:"more_output,omitempty"`
	LogRestarted bool   `json:"log_restarted,omitempty"`
	ExitCode     *int   `json:"exit_code,omitempty"`
	Error        string `json:"error,omitempty"`
	LogPath      string `json:"log_path,omitempty"`
	Instructions string `json:"instructions,omitempty"`
}

func (t Tool) Run(_ context.Context, input json.RawMessage, _ tools.Context) tools.Result {
	var params struct {
		BashID string `json:"bash_id"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	id := strings.TrimSpace(params.BashID)
	if id == "" {
		return tools.Result{Content: "bash_id is required", IsError: true}
	}
	chunk, ok, err := t.store.ReadNewOutput(id)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if !ok {
		return tools.Result{Content: notFoundMessage(id), IsError: true}
	}
	job := chunk.Job
	payload := outputPayload{
		ID:           job.ID,
		Status:       job.Status,
		Running:      chunk.Running,
		Output:       chunk.Text,
		MoreOutput:   chunk.MoreOutput,
		LogRestarted: chunk.Restarted,
		Error:        job.Error,
		LogPath:      job.LogPath,
	}
	if !chunk.Running {
		exitCode := job.ExitCode
		payload.ExitCode = &exitCode
	}
	switch {
	case chunk.MoreOutput:
		payload.Instructions = "More output is already buffered; call BashOutput again with the same bash_id to read the rest."
	case chunk.Running && chunk.Text == "":
		payload.Instructions = "No new output yet and the command is still running. Do the next useful thing and call BashOutput again later, or use KillShell to stop it."
	case chunk.Running:
		payload.Instructions = "The command is still running; call BashOutput again later for more output, or use KillShell to stop it."
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	return tools.Result{Content: string(data)}
}

func notFoundMessage(id string) string {
	return fmt.Sprintf("background command not found: %s (use the \"id\" returned by Bash with run_in_background=true)", id)
}
