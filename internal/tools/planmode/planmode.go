package planmode

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/tools"
)

type State struct {
	Active bool   `json:"active"`
	Plan   string `json:"plan,omitempty"`
}

type EnterTool struct{}
type ExitTool struct{}

func NewEnter() EnterTool { return EnterTool{} }
func NewExit() ExitTool   { return ExitTool{} }

func (EnterTool) Name() string { return "EnterPlanMode" }

func (EnterTool) MaxResultSizeChars() int { return tools.ClaudeCodeDefaultDeclaredMaxResultSizeChars }

func (EnterTool) Description() string {
	return `Enter plan mode and store a draft implementation plan without changing project files.

Use EnterPlanMode when you need to outline an approach before implementing.
EnterPlanMode writes .niuma/plan_mode.json. The agent will not make project
file changes while in plan mode — it only proposes steps for user review.

Do not use EnterPlanMode for read-only analysis, review-only work, or requests
that say "do not modify files". In those cases, provide the plan or checklist
in the assistant response instead of calling this tool.`
}
func (EnterTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
	  "type": "object",
	  "properties": {
	    "plan": {"type": "string", "description": "Draft plan to save to .niuma/plan_mode.json. Do not use for read-only or no-modify tasks."}
	  },
	  "required": ["plan"],
	  "additionalProperties": false
	}`)
}
func (EnterTool) Run(_ context.Context, input json.RawMessage, toolContext tools.Context) tools.Result {
	var params struct {
		Plan string `json:"plan"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if params.Plan == "" {
		return tools.Result{Content: "plan is required", IsError: true}
	}
	if err := save(toolContext, State{Active: true, Plan: params.Plan}); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	return tools.Result{Content: "Entered plan mode"}
}

func (ExitTool) Name() string { return "ExitPlanMode" }

func (ExitTool) MaxResultSizeChars() int { return tools.ClaudeCodeDefaultDeclaredMaxResultSizeChars }

func (ExitTool) Description() string {
	return `Exit plan mode after the user has accepted the plan.

Use ExitPlanMode with accepted:true when the user approves the plan and wants
to proceed with implementation. Use accepted:false if the plan needs revision.
ExitPlanMode writes .niuma/plan_mode.json and should only be used when plan
mode was entered with file writes allowed. Do not use it for read-only or
no-modify tasks.`
}
func (ExitTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
	  "type": "object",
	  "properties": {
	    "accepted": {"type": "boolean", "description": "Whether the plan was accepted. This updates .niuma/plan_mode.json."}
	  },
	  "required": ["accepted"],
	  "additionalProperties": false
	}`)
}
func (ExitTool) Run(_ context.Context, input json.RawMessage, toolContext tools.Context) tools.Result {
	var params struct {
		Accepted bool `json:"accepted"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if !params.Accepted {
		return tools.Result{Content: "Plan was not accepted; remaining in plan mode", IsError: true}
	}
	if err := save(toolContext, State{Active: false}); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	return tools.Result{Content: "Exited plan mode"}
}

func save(toolContext tools.Context, state State) error {
	path := planModePath(toolContext.CWD)
	if err := tools.EnsureWritablePathWithSandbox(toolContext.CWD, toolContext.WritableRoots, path, toolContext.Sandbox); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
		return err
	}
	return nil
}

func Load(cwd string) (State, error) {
	data, err := os.ReadFile(planModePath(cwd))
	if err != nil {
		if os.IsNotExist(err) {
			return State{}, nil
		}
		return State{}, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, fmt.Errorf("parse plan mode state: %w", err)
	}
	return state, nil
}

func planModePath(cwd string) string {
	return config.CurrentIdentity(cwd).ProjectStatePath(cwd, "plan_mode.json")
}
