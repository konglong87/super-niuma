package bash

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/background"
	"github.com/konglong87/super-niuma/internal/files"
	"github.com/konglong87/super-niuma/internal/procenv"
	"github.com/konglong87/super-niuma/internal/repair"
	"github.com/konglong87/super-niuma/internal/sandbox"
	"github.com/konglong87/super-niuma/internal/tools"
)

type Tool struct{}

const maxCommandOutputBytes = 200 * 1024
const maxResultSizeChars = 30_000
const defaultTimeoutMS = 120_000

func New() Tool { return Tool{} }

func (Tool) Name() string { return "Bash" }

func (Tool) MaxResultSizeChars() int { return maxResultSizeChars }

func (Tool) Description() string {
	return `Run a shell command in the current working directory and return combined stdout/stderr.

Use Bash for: running build/test tools, package managers (npm/go/pip), git operations,
starting dev servers, and terminal utilities that cannot be done with dedicated tools.
Prefer other tools over Bash: use Read for reading files, Write/Edit for changing files,
Glob for finding files, Grep for searching file contents, and LS for listing directories.
Do not use Bash for cat/head/tail, grep/rg, find/ls, sed/awk, or echo/cat heredocs
when a dedicated tool can do the same job.
Repository-wide read-only audits are an exception: Bash is appropriate for
aggregate facts such as git status/log/tag, find/wc, directory-count loops, and
read-only rg pipelines. Use dedicated tools again for focused file reads and edits.
Repair/release verification is also an exception for read-only shell facts:
git status --short --branch, git diff --name-status, git diff --summary,
git rev-parse, git ls-remote --tags, and git show --raw --format=short are
appropriate after edits or tag/version work.

Command planning:
- Quote paths that contain spaces with double quotes.
- Use absolute paths or keep the working directory stable; avoid cd unless the user
  asks for it or the command genuinely requires it.
- If independent commands are all likely to succeed, issue multiple Bash tool calls
  in the same assistant message so they can run in parallel. If commands depend on
  each other, chain them with && in one command.
- Do not use sleep as a substitute for checking real state. If waiting for a
  background command started with run_in_background, poll it with BashOutput
  instead of reading its log file; do not poll in a sleep loop.
- Do not run destructive git commands or skip hooks/signing unless the user
  explicitly requested that exact operation.

Output is limited to 200 KB. Commands timeout at 120s by default (configurable via timeout_ms or timeout).
If the exact tail or full output matters, rerun with narrower filters or redirect output to a file in the workspace/configured writable directory, then inspect that file with Read offset/limit.
When you need a long-running process and do not need the result immediately, set run_in_background=true. Do not add '&' yourself when using run_in_background; the command keeps running and the returned id is what BashOutput polls for new output and KillShell stops (/logs also works from the CLI).

If a command fails, check stderr in the output for the actual error. Common failures:
command not found (install missing dependency), permission denied (check file modes),
network timeout (retry with longer timeout or check connectivity).`
}

func (Tool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
	  "type": "object",
	  "properties": {
	    "command": {"type": "string", "description": "Shell command to run. Use full paths when possible. For multi-line scripts, use && or ; to chain commands. Prefer simple commands over complex pipes."},
	    "description": {"type": "string", "description": "Short description of what the command does."},
	    "timeout_ms": {"type": "integer", "description": "Optional timeout in milliseconds. Default 120000."},
	    "timeout": {"type": "integer", "description": "Optional timeout in milliseconds. Claude Code compatible alias for timeout_ms."},
	    "run_in_background": {"type": "boolean", "description": "Set to true to run this command in the background. The tool returns a background session id and log_path immediately; use Read on log_path later when available."},
	    "verification": {
	      "type": "object",
	      "description": "Optional structured repair probe metadata. Foreground commands only.",
	      "properties": {
	        "probe_id": {"type": "string"},
	        "phase": {"type": "string", "enum": ["baseline", "post_change"]},
	        "purpose": {"type": "string"},
	        "targets": {"type": "array", "items": {"type": "string"}, "minItems": 1},
	        "expect_exit": {"type": "string", "enum": ["zero", "nonzero"]}
	      },
	      "required": ["probe_id", "phase", "targets", "expect_exit"],
	      "additionalProperties": false
	    },
	    "dangerouslyDisableSandbox": {"type": "boolean", "description": "Run without OS sandbox only when sandbox.allowUnsandboxedCommands permits it."}
	  },
	  "required": ["command"],
	  "additionalProperties": false
	}`)
}

func (Tool) Run(ctx context.Context, input json.RawMessage, toolContext tools.Context) tools.Result {
	var params struct {
		Command                    string                      `json:"command"`
		Description                string                      `json:"description"`
		TimeoutMS                  int                         `json:"timeout_ms"`
		Timeout                    int                         `json:"timeout"`
		RunInBackground            bool                        `json:"run_in_background"`
		Verification               *repair.VerificationRequest `json:"verification"`
		DangerouslyDisableSandbox  bool                        `json:"dangerouslyDisableSandbox"`
		DangerouslyDisableSandbox2 bool                        `json:"dangerously_disable_sandbox"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if params.Verification != nil {
		if err := params.Verification.Validate(); err != nil {
			return tools.Result{Content: err.Error(), IsError: true}
		}
		if params.RunInBackground {
			return tools.Result{Content: "verification metadata requires a foreground command", IsError: true}
		}
	}
	if params.TimeoutMS <= 0 && params.Timeout > 0 {
		params.TimeoutMS = params.Timeout
	}
	if params.TimeoutMS <= 0 {
		params.TimeoutMS = defaultTimeoutMS
	}
	destructive := dangerousCommandDecision(params.Command)
	if destructive.Reason != "" {
		return tools.Result{Content: destructive.Reason, IsError: true}
	}
	if destructive.Overridden() {
		// Report through the existing PermissionAudit callback rather than emitting
		// telemetry here: no tools package depends on internal/telemetry, and the
		// runtime already routes this callback to a permission.decision event plus a
		// transcript entry, so the override shows up in session inspect and the
		// Trace Viewer like any other authorization decision.
		reportDestructiveOverride(toolContext, params.Command, destructive.SuppressedReason)
	}
	if err := sandbox.CheckShellCommand(toolContext.CWD, toolContext.WritableRoots, params.Command, toolContext.Sandbox); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if err := sandbox.CheckShellNetworkPolicy(params.Command, toolContext.Sandbox); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(params.TimeoutMS)*time.Millisecond)
	defer cancel()

	spec, err := sandbox.PrepareShell(toolContext.CWD, toolContext.WritableRoots, params.Command, toolContext.Sandbox, params.DangerouslyDisableSandbox || params.DangerouslyDisableSandbox2)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if params.RunInBackground {
		return runInBackground(params.Command, params.Description, params.TimeoutMS, spec, toolContext)
	}
	defer spec.Close()
	cmd := exec.CommandContext(ctx, spec.Path, spec.Args...)
	cmd.Dir = toolContext.CWD
	cmd.Env = commandEnv(tools.EnvironmentWithSkillRuntime(spec.Env, toolContext.ActiveSkill))
	cmd.ExtraFiles = spec.ExtraFiles
	output := &limitedBuffer{limit: maxCommandOutputBytes}
	cmd.Stdout = output
	cmd.Stderr = output
	// Capture the writable roots before the command runs so create/modify/
	// delete/rename/chmod/symlink changes made by the shell (or scripts it
	// invokes) produce recoverable file_change events, even on failed exit.
	capture := beginFileTracking(toolContext)
	err = cmd.Run()
	text := output.String()
	notice := emitFileChanges(toolContext, capture)
	verification := repair.EvaluateShellCommand(params.Command, params.Verification, shellExitCode(err))
	if err != nil {
		if _, processExit := err.(*exec.ExitError); !processExit {
			verification.MatchedExpectation = false
		}
	}
	var verificationResult *repair.VerificationResult
	if params.Verification != nil {
		verificationResult = &verification
	}
	if ctx.Err() == context.DeadlineExceeded {
		if verificationResult != nil {
			verificationResult.MatchedExpectation = false
		}
		return tools.Result{Content: fmt.Sprintf("command timed out after %dms\n%s%s", params.TimeoutMS, text, notice), IsError: true, Verification: verificationResult}
	}
	if err != nil {
		return tools.Result{Content: fmt.Sprintf("%s\n%s%s", err.Error(), text, notice), IsError: true, Verification: verificationResult}
	}
	if text == "" {
		text = "(no output — the command exited successfully with no output)"
	}
	return tools.Result{Content: text + notice, Verification: verificationResult}
}

func shellExitCode(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return 1
}

// beginFileTracking records the before-state of the writable roots. It returns
// nil when tracking is disabled, no callback is registered, or there is no root
// to scan; nil captures are safe to pass to emitFileChanges.
func beginFileTracking(toolContext tools.Context) *files.Capture {
	if toolContext.FileChange == nil || !fileTrackingEnabled() {
		return nil
	}
	roots := captureRoots(toolContext)
	if len(roots) == 0 {
		return nil
	}
	capture, err := files.NewCapture(files.CaptureConfig{Roots: roots})
	if err != nil {
		return nil
	}
	return capture
}

// emitFileChanges diffs the captured roots and forwards each recoverable change
// to the FileChange callback. It returns a human-readable notice describing any
// files skipped because their before-content exceeded the capture budget, so a
// degraded capture is never silently reported as a complete one.
func emitFileChanges(toolContext tools.Context, capture *files.Capture) string {
	if capture == nil || toolContext.FileChange == nil {
		if capture != nil {
			capture.Discard()
		}
		return ""
	}
	changes, skipped, err := capture.Changes()
	if err != nil {
		capture.Discard()
		return ""
	}
	for _, ch := range changes {
		toolContext.FileChange(tools.FileChange{
			Path:               ch.Path,
			Before:             ch.Before,
			BeforeExists:       ch.BeforeExists,
			After:              ch.After,
			AfterExists:        ch.AfterExists,
			BeforeSnapshotPath: ch.BeforeSnapshotPath,
			AfterSnapshotPath:  ch.AfterSnapshotPath,
			BeforeMode:         ch.BeforeMode,
			AfterMode:          ch.AfterMode,
			BeforeModeKnown:    ch.BeforeModeKnown,
			AfterModeKnown:     ch.AfterModeKnown,
			ModeChanged:        ch.ModeChanged,
			BeforeIsSymlink:    ch.BeforeIsSymlink,
			AfterIsSymlink:     ch.AfterIsSymlink,
			BeforeLinkTarget:   ch.BeforeLinkTarget,
			AfterLinkTarget:    ch.AfterLinkTarget,
			BeforeIsDir:        ch.BeforeIsDir,
			AfterIsDir:         ch.AfterIsDir,
			BeforeMetadata:     ch.BeforeMetadata,
			Source:             "bash",
		})
	}
	if len(skipped) > 0 {
		return fmt.Sprintf("\n[file-change tracking skipped %d file(s) exceeding the capture budget; rewind may not restore them]", len(skipped))
	}
	return ""
}

func captureRoots(toolContext tools.Context) []string {
	roots := make([]string, 0, len(toolContext.WritableRoots)+1)
	if strings.TrimSpace(toolContext.CWD) != "" {
		roots = append(roots, toolContext.CWD)
	}
	roots = append(roots, toolContext.WritableRoots...)
	return roots
}

func fileTrackingEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("NIUMA_BASH_FILE_TRACKING"))) {
	case "0", "false", "off", "no":
		return false
	default:
		return true
	}
}

func runInBackground(command, description string, timeoutMS int, spec sandbox.ShellSpec, toolContext tools.Context) tools.Result {
	store := background.DefaultStore()
	prompt := description
	if prompt == "" {
		prompt = command
	}
	job, err := store.CreateWithOptions(background.Options{
		Prompt: prompt,
		CWD:    toolContext.CWD,
		Kind:   "bash",
	})
	if err != nil {
		_ = spec.Close()
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if err := os.MkdirAll(filepath.Dir(job.LogPath), 0755); err != nil {
		_ = spec.Close()
		return tools.Result{Content: err.Error(), IsError: true}
	}
	logFile, err := os.OpenFile(job.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		_ = spec.Close()
		return tools.Result{Content: err.Error(), IsError: true}
	}
	bgCtx := context.Background()
	var cancel context.CancelFunc
	if timeoutMS > 0 {
		bgCtx, cancel = context.WithTimeout(bgCtx, time.Duration(timeoutMS)*time.Millisecond)
	} else {
		bgCtx, cancel = context.WithCancel(bgCtx)
	}
	cmd := exec.CommandContext(bgCtx, spec.Path, spec.Args...)
	cmd.Dir = toolContext.CWD
	cmd.Env = commandEnv(tools.EnvironmentWithSkillRuntime(spec.Env, toolContext.ActiveSkill))
	cmd.ExtraFiles = spec.ExtraFiles
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		cancel()
		_ = logFile.Close()
		_ = spec.Close()
		_, _, _ = store.Finish(job.ID, 1, err.Error())
		return tools.Result{Content: err.Error(), IsError: true}
	}
	started, ok, err := store.MarkRunning(job.ID, cmd.Process.Pid)
	if err != nil || !ok {
		cancel()
		_ = cmd.Process.Kill()
		_ = logFile.Close()
		_ = spec.Close()
		if err == nil {
			err = fmt.Errorf("background session not found: %s", job.ID)
		}
		return tools.Result{Content: err.Error(), IsError: true}
	}
	go func() {
		defer cancel()
		waitErr := cmd.Wait()
		closeErr := logFile.Close()
		specErr := spec.Close()
		exitCode := 0
		errText := ""
		if waitErr != nil {
			exitCode = 1
			errText = waitErr.Error()
			if exitErr, ok := waitErr.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			}
		}
		if bgCtx.Err() == context.DeadlineExceeded {
			errText = fmt.Sprintf("command timed out after %dms", timeoutMS)
			if exitCode == 0 {
				exitCode = 1
			}
		}
		if closeErr != nil && errText == "" {
			errText = closeErr.Error()
			exitCode = 1
		}
		if specErr != nil && errText == "" {
			errText = specErr.Error()
			exitCode = 1
		}
		_, _, _ = store.Finish(job.ID, exitCode, errText)
	}()
	payload := map[string]any{
		"background":   true,
		"id":           started.ID,
		"status":       started.Status,
		"pid":          started.PID,
		"log_path":     started.LogPath,
		"instructions": "The command is running in the background. Do not assume it has completed. Poll it with BashOutput (bash_id=" + started.ID + "), which returns only the output added since your last call and whether it is still running; do not Read log_path for this, it replays the whole log every time. Stop it with KillShell when it is no longer needed. If neither tool is available, use /logs " + started.ID + " or wait for the background completion notification in the TUI.",
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	return tools.Result{Content: string(data)}
}

var nonInteractiveCommandEnv = []string{
	"GIT_EDITOR=true",
	"GIT_TERMINAL_PROMPT=0",
}

// commandEnv is the sanitised parent environment plus the non-interactive git
// settings. Secret stripping lives in procenv so hooks, PowerShell, Workflow,
// the WebBrowser adapter and stdio MCP servers share one list (AUDIT-P1-16).
func commandEnv(extra []string) []string {
	overrides := make([]string, 0, len(nonInteractiveCommandEnv)+len(extra))
	overrides = append(overrides, nonInteractiveCommandEnv...)
	overrides = append(overrides, extra...)
	return procenv.Sanitized(overrides...)
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
