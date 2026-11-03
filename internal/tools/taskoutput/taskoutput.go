package taskoutput

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/konglong87/super-niuma/internal/tools"
)

type Tool struct{}

func New() Tool { return Tool{} }

func (Tool) Name() string { return "TaskOutput" }

func (Tool) MaxResultSizeChars() int { return tools.ClaudeCodeDefaultDeclaredMaxResultSizeChars }

func (Tool) Description() string {
	return `Return a structured result from a delegated task or sub-agent.

Use TaskOutput to signal task completion with a clear status and summary.
Status must be "complete", "failed", or "blocked". Include a files list
when the task produced or modified specific files. This helps the parent
agent understand what was accomplished without reading full output.`
}

func (Tool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
	  "type": "object",
	  "properties": {
	    "status": {"type": "string", "enum": ["complete", "failed", "blocked"]},
	    "summary": {"type": "string"},
	    "files": {"type": "array", "items": {"type": "string"}}
	  },
	  "required": ["status", "summary"],
	  "additionalProperties": true
	}`)
}

func (Tool) Run(_ context.Context, input json.RawMessage, _ tools.Context) tools.Result {
	var params struct {
		Status  string   `json:"status"`
		Summary string   `json:"summary"`
		Files   []string `json:"files"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	params.Status = strings.TrimSpace(params.Status)
	params.Summary = strings.TrimSpace(params.Summary)
	if params.Status == "" || params.Summary == "" {
		return tools.Result{Content: "status and summary are required", IsError: true}
	}
	out, err := json.MarshalIndent(params, "", "  ")
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	return tools.Result{Content: string(out), IsError: params.Status == "failed" || params.Status == "blocked"}
}
