package powershell

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	"github.com/konglong87/super-niuma/internal/procenv"
	"github.com/konglong87/super-niuma/internal/sandbox"
	"github.com/konglong87/super-niuma/internal/tools"
)

type Tool struct{}

const maxCommandOutputBytes = 200 * 1024
const maxResultSizeChars = 30_000

func New() Tool { return Tool{} }

func (Tool) Name() string { return "PowerShell" }

func (Tool) MaxResultSizeChars() int { return maxResultSizeChars }

func (Tool) Description() string {
	return `Run a PowerShell command in the current working directory with static write-path safety checks.

Use PowerShell on Windows when Bash is unavailable or when you need Windows-specific
commands (registry, services, WMI, etc.). OS sandboxing for PowerShell is refused
when sandbox strict mode is enabled.

Output is limited to 200 KB. Commands timeout at 30s by default (configurable via timeout_ms).
If the exact tail or full output matters, rerun with narrower filters or redirect output to a file in the workspace/configured writable directory, then inspect that file with Read offset/limit.
For cross-platform commands, prefer Bash instead.`
}

func (Tool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
	  "type": "object",
	  "properties": {
	    "command": {"type": "string", "description": "PowerShell command to run."},
	    "description": {"type": "string", "description": "Short description of what the command does."},
	    "timeout_ms": {"type": "integer", "description": "Optional timeout in milliseconds. Default 30000."},
	    "dangerouslyDisableSandbox": {"type": "boolean", "description": "Run without OS sandbox only when sandbox.allowUnsandboxedCommands permits it."}
	  },
	  "required": ["command"],
	  "additionalProperties": false
	}`)
}

func (Tool) Run(ctx context.Context, input json.RawMessage, toolContext tools.Context) tools.Result {
	var params struct {
		Command                    string `json:"command"`
		Description                string `json:"description"`
		TimeoutMS                  int    `json:"timeout_ms"`
		DangerouslyDisableSandbox  bool   `json:"dangerouslyDisableSandbox"`
		DangerouslyDisableSandbox2 bool   `json:"dangerously_disable_sandbox"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if params.TimeoutMS <= 0 {
		params.TimeoutMS = 30000
	}
	if err := sandbox.CheckPowerShellCommand(toolContext.CWD, toolContext.WritableRoots, params.Command, toolContext.Sandbox); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	spec, err := sandbox.PreparePowerShell(params.Command, toolContext.Sandbox, params.DangerouslyDisableSandbox || params.DangerouslyDisableSandbox2)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	defer spec.Close()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(params.TimeoutMS)*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, spec.Path, spec.Args...)
	cmd.Dir = toolContext.CWD
	cmd.Env = commandEnv(tools.EnvironmentWithSkillRuntime(spec.Env, toolContext.ActiveSkill))
	cmd.ExtraFiles = spec.ExtraFiles
	output := &limitedBuffer{limit: maxCommandOutputBytes}
	cmd.Stdout = output
	cmd.Stderr = output
	err = cmd.Run()
	text := output.String()
	if ctx.Err() == context.DeadlineExceeded {
		return tools.Result{Content: fmt.Sprintf("PowerShell command timed out after %dms\n%s", params.TimeoutMS, text), IsError: true}
	}
	if err != nil {
		return tools.Result{Content: fmt.Sprintf("%s\n%s", err.Error(), text), IsError: true}
	}
	if text == "" {
		text = "(no output — the command exited successfully with no output)"
	}
	return tools.Result{Content: text}
}

// commandEnv strips credential-shaped variables before handing the environment
// to PowerShell. On Windows this is the primary shell, so the leak this closes
// (AUDIT-P1-16) was total there.
func commandEnv(extra []string) []string {
	return procenv.Sanitized(extra...)
}

type limitedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		return len(p), nil
	}
	remaining := b.limit - b.buf.Len()
	if remaining > 0 {
		if len(p) <= remaining {
			_, _ = b.buf.Write(p)
		} else {
			_, _ = b.buf.Write(p[:remaining])
			b.truncated = true
		}
	} else if len(p) > 0 {
		b.truncated = true
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	text := b.buf.String()
	if b.truncated {
		text += fmt.Sprintf("\n[output truncated after %d bytes]", b.limit)
	}
	return text
}
