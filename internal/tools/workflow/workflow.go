package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/procenv"
	"github.com/konglong87/super-niuma/internal/sandbox"
	"github.com/konglong87/super-niuma/internal/tools"
	"gopkg.in/yaml.v3"
)

type Tool struct{}

func New() Tool { return Tool{} }

func (Tool) Name() string { return "Workflow" }

func (Tool) MaxResultSizeChars() int { return tools.ClaudeCodeDefaultDeclaredMaxResultSizeChars }

func (Tool) Description() string {
	return `List, show, and run project workflows from .claude/workflows/*.yaml.

Use Workflow to execute predefined multi-step automation sequences:
- "list": discover available workflows
- "show": view a workflow's steps without running
- "run": execute a workflow's steps sequentially

Each step runs a shell command with sandbox checks. Use dry_run:true to preview
steps without executing. Per-step timeout defaults to 30s (configurable via timeout_ms).`
}

func (Tool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
	  "type": "object",
	  "properties": {
	    "action": {"type": "string", "enum": ["list", "show", "run"], "description": "Workflow action."},
	    "name": {"type": "string", "description": "Workflow name for show/run."},
	    "dry_run": {"type": "boolean", "description": "Return planned steps without executing them."},
	    "timeout_ms": {"type": "integer", "description": "Per-step timeout. Default 30000."}
	  },
	  "required": ["action"],
	  "additionalProperties": false
	}`)
}

func (Tool) Run(ctx context.Context, input json.RawMessage, toolContext tools.Context) tools.Result {
	var params struct {
		Action    string `json:"action"`
		Name      string `json:"name"`
		DryRun    bool   `json:"dry_run"`
		TimeoutMS int    `json:"timeout_ms"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if params.TimeoutMS <= 0 {
		params.TimeoutMS = 30000
	}
	workflows, err := loadWorkflows(toolContext.CWD)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	switch strings.ToLower(strings.TrimSpace(params.Action)) {
	case "list":
		return encode(workflowSummaries(workflows))
	case "show":
		workflow, ok := findWorkflow(workflows, params.Name)
		if !ok {
			return tools.Result{Content: "workflow not found: " + params.Name, IsError: true}
		}
		return encode(workflow)
	case "run":
		workflow, ok := findWorkflow(workflows, params.Name)
		if !ok {
			return tools.Result{Content: "workflow not found: " + params.Name, IsError: true}
		}
		return runWorkflow(ctx, toolContext, workflow, params.DryRun, params.TimeoutMS)
	default:
		return tools.Result{Content: "unsupported Workflow action: " + params.Action, IsError: true}
	}
}

type Workflow struct {
	Name        string         `json:"name" yaml:"name"`
	Description string         `json:"description,omitempty" yaml:"description,omitempty"`
	Path        string         `json:"path,omitempty" yaml:"-"`
	Steps       []WorkflowStep `json:"steps" yaml:"steps"`
}

type WorkflowStep struct {
	Name    string `json:"name,omitempty" yaml:"name,omitempty"`
	Command string `json:"command,omitempty" yaml:"command,omitempty"`
}

type workflowSummary struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Path        string `json:"path"`
	Steps       int    `json:"steps"`
}

type stepResult struct {
	Name     string `json:"name,omitempty"`
	Command  string `json:"command"`
	Output   string `json:"output,omitempty"`
	Duration int64  `json:"duration_ms,omitempty"`
	Skipped  bool   `json:"skipped,omitempty"`
}

func loadWorkflows(cwd string) ([]Workflow, error) {
	root := workflowRoot(cwd)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Workflow
	for _, entry := range entries {
		if entry.IsDir() || !isYAML(entry.Name()) {
			continue
		}
		path := filepath.Join(root, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var workflow Workflow
		if err := yaml.Unmarshal(data, &workflow); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if strings.TrimSpace(workflow.Name) == "" {
			workflow.Name = strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		}
		workflow.Path = path
		out = append(out, workflow)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func workflowRoot(cwd string) string {
	if strings.TrimSpace(cwd) == "" {
		cwd = "."
	}
	return filepath.Join(cwd, ".claude", "workflows")
}

func workflowSummaries(workflows []Workflow) []workflowSummary {
	out := make([]workflowSummary, 0, len(workflows))
	for _, workflow := range workflows {
		out = append(out, workflowSummary{Name: workflow.Name, Description: workflow.Description, Path: workflow.Path, Steps: len(workflow.Steps)})
	}
	return out
}

func findWorkflow(workflows []Workflow, name string) (Workflow, bool) {
	name = strings.TrimSpace(name)
	for _, workflow := range workflows {
		if workflow.Name == name {
			return workflow, true
		}
	}
	return Workflow{}, false
}

func runWorkflow(ctx context.Context, toolContext tools.Context, workflow Workflow, dryRun bool, timeoutMS int) tools.Result {
	cwd := toolContext.CWD
	results := make([]stepResult, 0, len(workflow.Steps))
	for _, step := range workflow.Steps {
		if strings.TrimSpace(step.Command) == "" {
			continue
		}
		result := stepResult{Name: step.Name, Command: step.Command, Skipped: dryRun}
		if err := sandbox.CheckShellCommand(cwd, toolContext.WritableRoots, step.Command, toolContext.Sandbox); err != nil {
			result.Output = err.Error()
			results = append(results, result)
			return encode(map[string]any{"workflow": workflow.Name, "ok": false, "failed_step": step.Name, "error": err.Error(), "steps": results})
		}
		if err := sandbox.CheckShellNetworkPolicy(step.Command, toolContext.Sandbox); err != nil {
			result.Output = err.Error()
			results = append(results, result)
			return encode(map[string]any{"workflow": workflow.Name, "ok": false, "failed_step": step.Name, "error": err.Error(), "steps": results})
		}
		if !dryRun {
			start := time.Now()
			ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
			spec, err := sandbox.PrepareShell(cwd, toolContext.WritableRoots, step.Command, toolContext.Sandbox, false)
			if err != nil {
				cancel()
				result.Duration = time.Since(start).Milliseconds()
				result.Output = err.Error()
				results = append(results, result)
				return encode(map[string]any{"workflow": workflow.Name, "ok": false, "failed_step": step.Name, "error": err.Error(), "steps": results})
			}
			cmd := exec.CommandContext(ctx, spec.Path, spec.Args...)
			cmd.Dir = cwd
			cmd.Env = procenv.Sanitized(spec.Env...)
			cmd.ExtraFiles = spec.ExtraFiles
			var out bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &out
			err = cmd.Run()
			closeErr := spec.Close()
			cancel()
			result.Duration = time.Since(start).Milliseconds()
			result.Output = out.String()
			results = append(results, result)
			if err != nil {
				return encode(map[string]any{"workflow": workflow.Name, "ok": false, "failed_step": step.Name, "error": err.Error(), "steps": results})
			}
			if closeErr != nil {
				return encode(map[string]any{"workflow": workflow.Name, "ok": false, "failed_step": step.Name, "error": closeErr.Error(), "steps": results})
			}
			continue
		}
		results = append(results, result)
	}
	return encode(map[string]any{"workflow": workflow.Name, "ok": true, "dry_run": dryRun, "steps": results})
}

func isYAML(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".yaml" || ext == ".yml"
}

func encode(value any) tools.Result {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	return tools.Result{Content: string(data)}
}
