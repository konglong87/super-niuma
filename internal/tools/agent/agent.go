package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/agentruntime"
	"github.com/konglong87/super-niuma/internal/agents"
	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/agentworktree"
	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/hooks"
	"github.com/konglong87/super-niuma/internal/session"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	"github.com/konglong87/super-niuma/internal/toolresult"
	"github.com/konglong87/super-niuma/internal/tools"
)

type MessageStreamer = agentruntime.MessageStreamer

type listStore interface {
	ListAgentTasks(ctx context.Context, limit int) ([]mysqlstore.AgentTask, error)
}

type eventListStore interface {
	ListAgentTaskEvents(ctx context.Context, taskID uint64, limit int) ([]mysqlstore.AgentTaskEvent, error)
}

const agentResultContentPreviewBytes = toolresult.PreviewSizeBytes

type cancelStore interface {
	CancelAgentTask(ctx context.Context, taskID uint64, resultJSON string) error
}

type CreateTool struct {
	runtime agentruntime.Runtime
}

type CompatTool struct {
	runtime agentruntime.Runtime
	cwd     string
}

func NewCompat(client MessageStreamer, model string, registry *tools.Registry) CompatTool {
	return CompatTool{runtime: agentruntime.Runtime{Client: client, Model: model, Registry: registry}}
}

func NewCompatWithHooks(client MessageStreamer, model string, registry *tools.Registry, hookRunner hooks.Runner) CompatTool {
	return CompatTool{runtime: agentruntime.Runtime{Client: client, Model: model, Registry: registry, Hooks: hookRunner}}
}

func NewCompatWithHooksAndCWD(client MessageStreamer, model string, registry *tools.Registry, hookRunner hooks.Runner, cwd string) CompatTool {
	return CompatTool{runtime: agentruntime.Runtime{Client: client, Model: model, Registry: registry, Hooks: hookRunner}, cwd: cwd}
}

func (t CompatTool) Name() string { return "Agent" }

func (t CompatTool) MaxResultSizeChars() int {
	return tools.ClaudeCodeDefaultDeclaredMaxResultSizeChars
}

func (t CompatTool) Description() string {
	return `Launch a new agent to handle complex, multi-step tasks autonomously.

The Agent tool provides a Claude Code compatible tool surface and launches
specialized sub-agents that autonomously handle complex tasks. Each agent type
has specific capabilities and tools available to it. Use it for complex
searches, independent analysis, multi-file investigations, or parallel work
whose result can be summarized back to the user. If subagent_type is omitted,
the general-purpose agent is used.

Available agent types:
` + t.availableAgentTypesDescription() + `

` + t.whenNotToUseDescription() + `

Usage notes:
- Always include a short description, around 3-5 words.
- Launch multiple agents concurrently in one assistant message when the work is
  independent.
- If the user explicitly asks you to run agents in parallel, send one assistant
  message with multiple Agent tool use content blocks rather than launching
  them across separate turns.
- Foreground is the default. Use it when the result is needed before you can
  proceed.
- Set run_in_background=true only when you have genuinely independent work to
  continue. When an agent runs in the background, you will be notified when it
  completes. Do not sleep, poll, proactively check progress, or guess the
  result.
- If SendMessage is available, use it to continue a previously spawned agent by
  task id or unique agent name/description; the resumed agent keeps its prior
  context. If SendMessage is not available, start a fresh Agent invocation with
  a complete task description.
- The result returned by a sub-agent is not automatically visible to the user;
  summarize it clearly in your own final response.
- Each Agent invocation starts with fresh context. Provide a complete task
  description every time; do not assume the agent has seen this conversation.
- The agent's findings should generally be trusted, but you own the synthesis
  and the final answer to the user.
- Tell the agent whether it should write code or only research, because it does
  not know the user's intent unless you say so.
- If you want research only, explicitly say that it must not modify files. If
  you expect implementation, say exactly what may be changed and what success
  criteria should be checked.
- If an agent description says it should be used proactively, use your
  judgement and invoke it without waiting for the user to ask explicitly.
- You can set isolation="worktree" to run the agent in a temporary git worktree.
  The worktree is cleaned up if the agent makes no changes; if changes are made,
  the result includes the worktree path and branch.

Writing the prompt:
Write a complete brief.
Brief the agent like a capable colleague who just joined the task. It has not
seen this conversation unless you include the relevant context. Explain the
goal, why it matters, known facts, ruled-out paths, relevant files/functions/
line numbers, exact scope, and expected output format. If you need a short
response, say so explicitly.

For lookups, hand over the exact search term, file path, or command shape. For
investigations, hand over the question and the evidence you already have rather
than prescribing brittle steps that may become dead weight when the premise is
wrong.

Terse command-style prompts produce shallow work. Never delegate understanding
with vague prompts like "based on your findings, fix it" or "based on the
research, implement it". Write prompts that prove you understood the task.

Good brief checklist:
- Objective and why it matters.
- Known facts, file paths, functions, line numbers, and prior command output.
- Ruled-out paths and assumptions that should not be repeated.
- Whether the agent should only research or may write code.
- Expected output format and length. Prefer Summary, Evidence, Assumptions,
  Unknowns, Verification, Risks, and Next action when the result will drive
  parent planning. Ask for those exact headings, one concise bullet under each
  heading, and "None observed" or an explicit unverified reason instead of
  omitted headings.
- How the result will be used by the parent thread.`
}

func (t CompatTool) whenNotToUseDescription() string {
	readAvailable := t.compatToolAvailable("Read")
	grepAvailable := t.compatToolAvailable("Grep")
	globAvailable := t.compatToolAvailable("Glob")

	filePathTool := "a direct file-reading or file-search tool"
	switch {
	case readAvailable && globAvailable:
		filePathTool = "Read or Glob"
	case readAvailable:
		filePathTool = "Read"
	case globAvailable:
		filePathTool = "Glob"
	}

	symbolSearchTool := "a direct search tool"
	switch {
	case grepAvailable && globAvailable:
		symbolSearchTool = "Grep or Glob"
	case grepAvailable:
		symbolSearchTool = "Grep"
	case globAvailable:
		symbolSearchTool = "Glob"
	}

	knownFilesTool := "direct file-reading tools"
	if readAvailable {
		knownFilesTool = "Read"
	}

	return `When NOT to use Agent:
- If you know the exact file path, use ` + filePathTool + ` directly.
- If you are searching for a specific symbol or class name, try ` + symbolSearchTool + `
  first.
- If the answer depends on only one to three known files, use ` + knownFilesTool + ` directly.
- Do not use Agent for tasks unrelated to the available agent descriptions.`
}

func (t CompatTool) compatToolAvailable(name string) bool {
	if t.runtime.Registry == nil {
		return false
	}
	_, ok := t.runtime.Registry.Get(name)
	return ok
}

func (t CompatTool) availableAgentTypesDescription() string {
	lines := []string{
		"- general-purpose: General-purpose agent for researching complex questions, searching for code, and executing multi-step tasks. Use it when a keyword/file search may require several attempts or when independent context synthesis is useful. (Tools: All tools)",
	}
	cwd := strings.TrimSpace(t.cwd)
	if cwd == "" {
		return strings.Join(lines, "\n")
	}
	list, err := agents.List(cwd)
	if err != nil {
		return strings.Join(lines, "\n")
	}
	for _, agent := range list {
		name := strings.TrimSpace(agent.Name)
		if name == "" || strings.EqualFold(name, "general-purpose") {
			continue
		}
		description := strings.TrimSpace(agent.Description)
		if description == "" {
			description = "Specialized agent"
		}
		lines = append(lines, "- "+name+": "+oneLine(description)+" (Tools: "+agentToolsDescription(agent)+")")
	}
	return strings.Join(lines, "\n")
}

func agentToolsDescription(agent agents.Agent) string {
	hasAllowlist := len(agent.Tools) > 0
	hasDenylist := len(agent.DisallowedTools) > 0
	switch {
	case hasAllowlist && hasDenylist:
		denied := map[string]bool{}
		for _, tool := range agent.DisallowedTools {
			denied[strings.TrimSpace(tool)] = true
		}
		var allowed []string
		for _, tool := range agent.Tools {
			tool = strings.TrimSpace(tool)
			if tool != "" && !denied[tool] {
				allowed = append(allowed, tool)
			}
		}
		if len(allowed) == 0 {
			return "None"
		}
		return strings.Join(allowed, ", ")
	case hasAllowlist:
		return strings.Join(agent.Tools, ", ")
	case hasDenylist:
		return "All tools except " + strings.Join(agent.DisallowedTools, ", ")
	default:
		return "All tools"
	}
}

func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func (t CompatTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"description":{"description":"A short (3-5 word) description of the task","type":"string"},"prompt":{"description":"Complete task brief for the agent. Include goal, known facts, ruled-out paths, relevant files/functions/line numbers, exact scope, and expected output. Ask for exact headings Summary, Evidence, Assumptions, Unknowns, Verification, Risks, and Next action when the result will drive parent planning; require one concise bullet under each heading, using 'None observed' or an explicit unverified reason instead of omitting headings.","type":"string"},"subagent_type":{"description":"The type of specialized agent to use for this task. Omit to use the default general-purpose agent; do not pass 'default'.","type":"string"},"model":{"description":"Optional model override for this agent. Takes precedence over the agent definition's model frontmatter. If omitted, uses the agent definition's model, or inherits from the parent.","type":"string","enum":["sonnet","opus","haiku"]},"run_in_background":{"description":"Set to true to run this agent in the background. You will be notified when it completes.","type":"boolean"},"isolation":{"description":"Isolation mode. \"worktree\" creates a temporary git worktree so the agent works on an isolated copy of the repo.","type":"string","enum":["worktree"]}},"required":["description","prompt"],"additionalProperties":false}`)
}

func (t CompatTool) Run(ctx context.Context, input json.RawMessage, toolContext tools.Context) tools.Result {
	var params struct {
		Description     string `json:"description"`
		Prompt          string `json:"prompt"`
		SubagentType    string `json:"subagent_type"`
		Provider        string `json:"provider"`
		Model           string `json:"model"`
		Effort          string `json:"effort"`
		MaxOutputTokens int    `json:"max_output_tokens"`
		MaxTurns        int    `json:"max_turns"`
		TimeoutMS       int    `json:"timeout_ms"`
		RunInBackground bool   `json:"run_in_background"`
		Isolation       string `json:"isolation"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if strings.TrimSpace(params.Description) == "" {
		return tools.Result{Content: "description is required", IsError: true}
	}
	if strings.TrimSpace(params.Prompt) == "" {
		return tools.Result{Content: "prompt is required", IsError: true}
	}
	runtime := t.runtime
	if runtime.TaskStore == nil {
		runtime.TaskStore = toolContext.TaskStore
	}
	if params.RunInBackground && runtime.TaskStore == nil {
		return tools.Result{Content: "background Agent requires task store", IsError: true}
	}
	worktreeInfo := agentworktree.Info{}
	cwd := toolContext.CWD
	if isolation := strings.TrimSpace(params.Isolation); isolation != "" {
		if !strings.EqualFold(isolation, "worktree") {
			return tools.Result{Content: "unsupported Agent isolation: " + isolation, IsError: true}
		}
		var err error
		worktreeInfo, err = agentworktree.CreateWithHooks(ctx, toolContext.CWD, agentworktree.NewSlug("agent"), runtime.Hooks)
		if err != nil {
			return tools.Result{Content: err.Error(), IsError: true}
		}
		cwd = worktreeInfo.Path
	}
	request := agentruntime.Request{
		Description:        params.Description,
		Prompt:             promptWithWorktreeNotice(params.Prompt, toolContext.CWD, worktreeInfo),
		SubagentType:       params.SubagentType,
		Provider:           params.Provider,
		Model:              params.Model,
		Effort:             params.Effort,
		MaxTokens:          params.MaxOutputTokens,
		MaxTurns:           params.MaxTurns,
		TimeoutMS:          params.TimeoutMS,
		CWD:                cwd,
		TenantID:           toolContext.TenantID,
		UserID:             toolContext.UserID,
		ParentSessionID:    toolContext.SessionID,
		TraceID:            toolContext.TraceID,
		WorktreePath:       worktreeInfo.Path,
		WorktreeBranch:     worktreeInfo.Branch,
		WorktreeHeadCommit: worktreeInfo.HeadCommit,
		WorktreeGitRoot:    worktreeInfo.GitRoot,
		WorktreeHookBased:  worktreeInfo.HookBased,
	}
	if params.RunInBackground {
		result, err := runtime.RunBackground(ctx, request, toolContext)
		if err != nil {
			cleanupCompatWorktree(ctx, worktreeInfo)
			return tools.Result{Content: err.Error(), IsError: true}
		}
		out := map[string]any{
			"task_id":         result.TaskID,
			"status":          result.Status,
			"background":      result.Background,
			"agent_name":      result.AgentName,
			"model":           result.Model,
			"session_id":      result.SessionID,
			"output_file":     result.OutputFile,
			"permission_mode": result.PermissionMode,
		}
		return jsonResult(out)
	}
	result, err := runtime.Run(ctx, request, toolContext)
	if err != nil {
		cleanupCompatWorktree(ctx, worktreeInfo)
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if result.WorktreePath != "" {
		content := agentruntime.ResultWithCapabilityLoopDecisionContext(result)
		return tools.Result{Content: strings.TrimSpace(content + "\n\nworktreePath: " + result.WorktreePath + "\nworktreeBranch: " + result.WorktreeBranch)}
	}
	return tools.Result{Content: agentruntime.ResultWithCapabilityLoopDecisionContext(result)}
}

func promptWithWorktreeNotice(prompt, parentCWD string, info agentworktree.Info) string {
	if strings.TrimSpace(info.Path) == "" {
		return prompt
	}
	kind := "isolated git worktree"
	if info.HookBased {
		kind = "isolated worktree"
	}
	notice := fmt.Sprintf("You are operating in an %s at %s. The parent agent is working in %s. Treat paths from the parent context as referring to the parent working directory and translate them to this worktree root before editing. Your changes stay in this worktree and do not affect the parent's files.", kind, info.Path, strings.TrimSpace(parentCWD))
	return strings.TrimSpace(prompt) + "\n\n" + notice
}

func cleanupCompatWorktree(ctx context.Context, info agentworktree.Info) {
	if strings.TrimSpace(info.Path) == "" {
		return
	}
	_, _ = agentworktree.CleanupIfUnchanged(context.WithoutCancel(ctx), info)
}

func NewCreate(client MessageStreamer, model string, registry *tools.Registry) CreateTool {
	return CreateTool{runtime: agentruntime.Runtime{Client: client, Model: model, Registry: registry}}
}

func (t CreateTool) Name() string { return "AgentCreate" }

func (t CreateTool) MaxResultSizeChars() int {
	return tools.ClaudeCodeDefaultDeclaredMaxResultSizeChars
}

func (t CreateTool) Description() string {
	return `Create a background sub-agent and return its task handle.

	Use AgentCreate for long-running or parallel work that should not block the main agent.
	The sub-agent runs independently — use AgentGet to check progress and AgentMessage to
	send additional instructions. For synchronous sub-agent tasks, use the Task tool instead.

	Each background sub-agent starts as a fresh conversation with only the delegated
	prompt plus configured agent instructions/memory/skills; it does not automatically
	see the parent conversation or previous tool results. Write a complete brief:
	goal, why it matters, known facts, ruled-out paths, relevant files/functions/line
	numbers, exact scope, and expected output format. When the result will drive
	parent planning, ask for Summary, Evidence, Assumptions, Unknowns, Verification,
	Risks, and Next action. Ask for those exact headings, one concise bullet under
	each heading, and "None observed" or an explicit unverified reason instead of
	omitted headings.

	For the default background agent, omit subagent_type. Only set subagent_type
	when the user or available agent list names a real specialized agent type;
	do not set it to mode names such as "subagent" or "teammate".

	After AgentCreate returns, do not fabricate or predict the result. Until AgentGet
	or runtime task status shows completion, report status only. The parent agent must
	synthesize completed agent results before answering the user.`
}

func (t CreateTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"description":{"type":"string"},"prompt":{"type":"string","description":"Complete brief for the fresh background sub-agent: goal, known facts, ruled-out paths, relevant files/functions/line numbers, exact scope, and expected output. Ask for exact headings Summary, Evidence, Assumptions, Unknowns, Verification, Risks, and Next action when the result will drive parent planning; require one concise bullet under each heading, using 'None observed' or an explicit unverified reason instead of omitting headings. Do not delegate understanding with vague prompts like 'based on your findings, fix it'."},"subagent_type":{"type":"string","description":"Optional specialized agent type name. Omit for the default background agent. Do not set this to mode names such as 'subagent' or 'teammate'; use mode for that."},"mode":{"type":"string","enum":["subagent","teammate"]},"timeout_ms":{"type":"integer"}},"required":["prompt"],"additionalProperties":false}`)
}

func (t CreateTool) Run(ctx context.Context, input json.RawMessage, toolContext tools.Context) tools.Result {
	var params struct {
		Description     string `json:"description"`
		Prompt          string `json:"prompt"`
		SubagentType    string `json:"subagent_type"`
		Provider        string `json:"provider"`
		Model           string `json:"model"`
		Effort          string `json:"effort"`
		MaxOutputTokens int    `json:"max_output_tokens"`
		MaxTurns        int    `json:"max_turns"`
		Mode            string `json:"mode"`
		TimeoutMS       int    `json:"timeout_ms"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if strings.TrimSpace(params.Prompt) == "" {
		return tools.Result{Content: "prompt is required", IsError: true}
	}
	mode, err := parseMode(params.Mode)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	runtime := t.runtime
	if runtime.TaskStore == nil {
		runtime.TaskStore = toolContext.TaskStore
	}
	if runtime.TaskStore == nil {
		return tools.Result{Content: "AgentCreate requires task store", IsError: true}
	}
	result, err := runtime.RunBackground(ctx, agentruntime.Request{
		Description:     params.Description,
		Prompt:          params.Prompt,
		SubagentType:    params.SubagentType,
		Provider:        params.Provider,
		Model:           params.Model,
		Effort:          params.Effort,
		MaxTokens:       params.MaxOutputTokens,
		MaxTurns:        params.MaxTurns,
		AgentMode:       mode,
		TimeoutMS:       params.TimeoutMS,
		CWD:             toolContext.CWD,
		TenantID:        toolContext.TenantID,
		UserID:          toolContext.UserID,
		ParentSessionID: toolContext.SessionID,
		TraceID:         toolContext.TraceID,
	}, toolContext)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	return jsonResult(map[string]any{
		"task_id":         result.TaskID,
		"status":          result.Status,
		"background":      result.Background,
		"agent_mode":      result.AgentMode,
		"agent_name":      result.AgentName,
		"model":           result.Model,
		"session_id":      result.SessionID,
		"output_file":     result.OutputFile,
		"permission_mode": result.PermissionMode,
	})
}

type ListTool struct{}

func NewList() ListTool { return ListTool{} }

func (ListTool) Name() string { return "AgentList" }

func (ListTool) MaxResultSizeChars() int { return tools.ClaudeCodeDefaultDeclaredMaxResultSizeChars }

func (ListTool) Description() string {
	return `List recent sub-agent tasks.

Use AgentList to see what sub-agents are running or have recently completed.
Returns task IDs, statuses, and descriptions. Use AgentGet for detailed info on a specific task.`
}

func (ListTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"limit":{"type":"integer","minimum":1,"maximum":100}},"additionalProperties":false}`)
}

func (ListTool) Run(ctx context.Context, input json.RawMessage, toolContext tools.Context) tools.Result {
	var params struct {
		Limit int `json:"limit"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	store, ok := toolContext.TaskStore.(listStore)
	if !ok {
		return tools.Result{Content: "AgentList requires task listing store", IsError: true}
	}
	items, err := store.ListAgentTasks(ctx, normalizeLimit(params.Limit))
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	return jsonResult(map[string]any{"tasks": items})
}

type GetTool struct{}

func NewGet() GetTool { return GetTool{} }

func (GetTool) Name() string { return "AgentGet" }

func (GetTool) MaxResultSizeChars() int { return tools.ClaudeCodeDefaultDeclaredMaxResultSizeChars }

func (GetTool) Description() string {
	return `Get a sub-agent task by task_id.

	Use AgentGet to check the status, progress, and output of a specific sub-agent task.
	Returns task details, progress summary, and a sanitized final result when available.
	Sub-agent tool call inputs and outputs are omitted from AgentGet so the parent context
	receives findings, not the sub-agent's full sidechain trace.
	When result.capability_loop is present, treat its evidence, assumptions, unknowns,
	verification, risks, and next_action as structured parent decision context for your
	next step; carry those fields forward before finalizing, retrying, or recovering.
	If the task is still running, treat the output as status only; do not infer or invent
	final findings until the task is completed.`
}

func (GetTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"task_id":{"type":"integer"}},"required":["task_id"],"additionalProperties":false}`)
}

func (GetTool) Run(ctx context.Context, input json.RawMessage, toolContext tools.Context) tools.Result {
	taskID, err := taskIDFromInput(input)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	store, ok := toolContext.TaskStore.(listStore)
	if !ok {
		return tools.Result{Content: "AgentGet requires task listing store", IsError: true}
	}
	items, err := store.ListAgentTasks(ctx, 1000)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	for _, item := range items {
		if item.ID == taskID {
			out := map[string]any{"task": agentTaskForModel(item, toolContext.TaskController)}
			if eventStore, ok := toolContext.TaskStore.(eventListStore); ok {
				events, err := eventStore.ListAgentTaskEvents(ctx, taskID, 200)
				if err != nil {
					return tools.Result{Content: err.Error(), IsError: true}
				}
				out["progress_summary"] = summarizeEvents(events)
			}
			if result, ok := agentTaskResultForModel(item.ResultJSON); ok {
				out["result"] = result
			}
			return jsonResult(out)
		}
	}
	return tools.Result{Content: fmt.Sprintf("agent task not found: %d", taskID), IsError: true}
}

func agentTaskForModel(task mysqlstore.AgentTask, controller *agenttasks.Controller) map[string]any {
	out := map[string]any{
		"id":          task.ID,
		"agent_name":  task.AgentName,
		"description": task.Description,
		"status":      task.Status,
		"model":       task.Model,
		"started_at":  task.StartedAt,
	}
	if !task.FinishedAt.IsZero() {
		out["finished_at"] = task.FinishedAt
	}
	if task.SubagentSessionKey != "" {
		out["session_id"] = task.SubagentSessionKey
	}
	if task.TraceID != "" {
		out["trace_id"] = task.TraceID
	}
	if strings.TrimSpace(strings.ToLower(task.Status)) == agenttasks.StatusRunning && controller != nil {
		if controller.Active(task.ID) {
			out["process_attachment"] = "attached_to_current_process"
		} else {
			out["process_attachment"] = "not_attached_to_current_process"
			out["process_attachment_note"] = "This task may be running in another process or interrupted; check progress instead of waiting indefinitely."
		}
	}
	return out
}

func agentTaskResultForModel(resultJSON string) (map[string]any, bool) {
	resultJSON = strings.TrimSpace(resultJSON)
	if resultJSON == "" {
		return nil, false
	}
	var result agentruntime.Result
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
		return map[string]any{
			"parse_error": "stored result_json is not a sub-agent result object",
		}, true
	}
	out := map[string]any{}
	if result.Content != "" {
		addAgentResultContentForModel(out, result.Content)
	}
	if result.CapabilityLoop != nil {
		out["capability_loop"] = result.CapabilityLoop
	}
	if result.AgentName != "" {
		out["agent_name"] = result.AgentName
	}
	if result.Model != "" {
		out["model"] = result.Model
	}
	if result.Status != "" {
		out["status"] = result.Status
	}
	if result.AgentMode != "" {
		out["agent_mode"] = result.AgentMode
	}
	if result.Effort != "" {
		out["effort"] = result.Effort
	}
	if result.PermissionMode != "" {
		out["permission_mode"] = result.PermissionMode
	}
	if result.SessionID != "" {
		out["session_id"] = result.SessionID
	}
	if result.TranscriptPath != "" {
		out["transcript_path"] = result.TranscriptPath
	}
	if result.OutputFile != "" {
		out["output_file"] = result.OutputFile
	}
	if result.WorktreePath != "" {
		out["worktree_path"] = result.WorktreePath
	}
	if result.WorktreeBranch != "" {
		out["worktree_branch"] = result.WorktreeBranch
	}
	if result.Turns > 0 {
		out["turns"] = result.Turns
	}
	if result.TaskID > 0 {
		out["task_id"] = result.TaskID
	}
	if len(result.ToolCalls) > 0 {
		out["tool_call_count"] = len(result.ToolCalls)
		out["tool_calls_omitted"] = "Sub-agent tool call inputs and outputs are omitted from AgentGet; use progress_summary and transcript_path for audit details."
	}
	if result.Usage.InputTokens != 0 || result.Usage.OutputTokens != 0 || result.Usage.CacheReadInputTokens != 0 || result.Usage.CacheCreationInputTokens != 0 {
		out["usage"] = result.Usage
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

func addAgentResultContentForModel(out map[string]any, content string) {
	if len(content) <= toolresult.DefaultLimit {
		out["content"] = content
		return
	}
	preview := content
	if len(preview) > agentResultContentPreviewBytes {
		preview = preview[:agentResultContentPreviewBytes]
	}
	out["content_preview"] = preview
	out["content_truncated"] = true
	out["content_bytes"] = len(content)
	out["content_note"] = "Sub-agent result content is too large to inline in AgentGet; use output_file for the complete result when needed."
}

type StopTool struct{}

func NewStop() StopTool { return StopTool{} }

func (StopTool) Name() string { return "AgentStop" }

func (StopTool) MaxResultSizeChars() int { return tools.ClaudeCodeDefaultDeclaredMaxResultSizeChars }

func (StopTool) Description() string {
	return `Stop a running sub-agent task.

Use AgentStop to cancel a sub-agent that is taking too long or producing unwanted results.
This is a hard stop — the sub-agent will not produce further output after being stopped.`
}

func (StopTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"task_id":{"type":"integer"},"reason":{"type":"string"}},"required":["task_id"],"additionalProperties":false}`)
}

func (StopTool) Run(ctx context.Context, input json.RawMessage, toolContext tools.Context) tools.Result {
	var params struct {
		TaskID uint64 `json:"task_id"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if params.TaskID == 0 {
		return tools.Result{Content: "task_id is required", IsError: true}
	}
	cancelled := false
	if toolContext.TaskController != nil {
		cancelled = toolContext.TaskController.Cancel(params.TaskID)
	}
	if store, ok := toolContext.TaskStore.(cancelStore); ok {
		payload := cancelledResultJSONForStop(ctx, toolContext, params.TaskID, params.Reason)
		if err := store.CancelAgentTask(ctx, params.TaskID, string(payload)); err != nil {
			return tools.Result{Content: err.Error(), IsError: true}
		}
		cancelled = true
	}
	if !cancelled {
		return tools.Result{Content: "agent task is not running in this process", IsError: true}
	}
	return jsonResult(map[string]any{"task_id": params.TaskID, "cancelled": true})
}

func cancelledResultJSONForStop(ctx context.Context, toolContext tools.Context, taskID uint64, reason string) []byte {
	result := agentruntime.Result{
		Content: "Cancelled: sub-agent task cancelled",
		Status:  agenttasks.StatusCancelled,
		TaskID:  taskID,
	}
	reason = strings.TrimSpace(reason)
	if reason != "" {
		result.Content += ": " + reason
	}
	if store, ok := toolContext.TaskStore.(listStore); ok {
		if tasks, err := store.ListAgentTasks(ctx, 1000); err == nil {
			for _, task := range tasks {
				if task.ID != taskID {
					continue
				}
				result.AgentName = task.AgentName
				result.Model = task.Model
				result.SessionID = task.SubagentSessionKey
				if existing, ok := existingAgentResult(task.ResultJSON); ok {
					result = mergeCancelledResult(result, existing)
				}
				break
			}
		}
	}
	if text := partialTextFromEvents(ctx, toolContext.TaskStore, taskID); text != "" {
		result.Content = text + "\n\n" + result.Content
	}
	result = agentruntime.EnsureCapabilityLoop(result, agenttasks.StatusCancelled)
	data, err := json.Marshal(result)
	if err != nil {
		payload, _ := json.Marshal(map[string]any{"source": "agent_tool", "cancelled": true, "reason": reason})
		return payload
	}
	return data
}

func existingAgentResult(resultJSON string) (agentruntime.Result, bool) {
	resultJSON = strings.TrimSpace(resultJSON)
	if resultJSON == "" {
		return agentruntime.Result{}, false
	}
	var result agentruntime.Result
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
		return agentruntime.Result{}, false
	}
	return result, true
}

func mergeCancelledResult(base, existing agentruntime.Result) agentruntime.Result {
	if strings.TrimSpace(existing.Content) != "" {
		base.Content = existing.Content + "\n\n" + base.Content
	}
	if base.AgentName == "" {
		base.AgentName = existing.AgentName
	}
	if base.Model == "" {
		base.Model = existing.Model
	}
	if base.SessionID == "" {
		base.SessionID = existing.SessionID
	}
	if base.TranscriptPath == "" {
		base.TranscriptPath = existing.TranscriptPath
	}
	if base.OutputFile == "" {
		base.OutputFile = existing.OutputFile
	}
	if base.WorktreePath == "" {
		base.WorktreePath = existing.WorktreePath
	}
	if base.WorktreeBranch == "" {
		base.WorktreeBranch = existing.WorktreeBranch
	}
	if !base.WorktreeHookBased {
		base.WorktreeHookBased = existing.WorktreeHookBased
	}
	if base.Turns == 0 {
		base.Turns = existing.Turns
	}
	return base
}

func partialTextFromEvents(ctx context.Context, taskStore any, taskID uint64) string {
	eventStore, ok := taskStore.(eventListStore)
	if !ok {
		return ""
	}
	events, err := eventStore.ListAgentTaskEvents(ctx, taskID, 200)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, event := range events {
		if event.EventType != agenttasks.EventTextDelta {
			continue
		}
		text := strings.TrimSpace(payloadString(event.PayloadJSON, "text"))
		if text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(text)
		if b.Len() >= 1200 {
			break
		}
	}
	return truncate(b.String(), 1200)
}

type MessageTool struct {
	runtime agentruntime.Runtime
}

func NewMessage() MessageTool { return MessageTool{} }

func NewMessageWithRuntime(client MessageStreamer, model string, registry *tools.Registry) MessageTool {
	return MessageTool{runtime: agentruntime.Runtime{Client: client, Model: model, Registry: registry}}
}

type SendMessageTool struct {
	message MessageTool
}

func NewSendMessageWithRuntime(client MessageStreamer, model string, registry *tools.Registry) SendMessageTool {
	return SendMessageTool{message: NewMessageWithRuntime(client, model, registry)}
}

func (SendMessageTool) Name() string { return "SendMessage" }

func (SendMessageTool) MaxResultSizeChars() int {
	return tools.ClaudeCodeDefaultDeclaredMaxResultSizeChars
}

func (SendMessageTool) Description() string {
	return `Send a plain text message to a niuma background sub-agent task.

This compatibility tool is exposed only when experimental agent teams are
enabled. Prefer the task_id returned by Agent or AgentCreate as the "to" value.
You may also use a unique agent_name or description from Agent/AgentCreate
results; ambiguous names are rejected instead of guessing. The message is
delivered to a running task, or resumes a terminal task from its niuma
transcript when that transcript is available.

Supported input is a plain text message. Broadcasts, structured swarm protocol
messages, UDS peers, and bridge peers are not implemented in niuma yet.`
}

func (SendMessageTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"to":{"type":"string","description":"Recipient task id returned by Agent or AgentCreate. Accepted task id forms include \"1\", \"#1\", \"task:1\", or \"task_id:1\". A unique agent_name or description from recent agent tasks is also accepted; ambiguous names are rejected."},"summary":{"type":"string","description":"Optional short preview for UI parity with Claude Code."},"message":{"type":"string","description":"Plain text message content."}},"required":["to","message"],"additionalProperties":false}`)
}

func (t SendMessageTool) Run(ctx context.Context, input json.RawMessage, toolContext tools.Context) tools.Result {
	var params struct {
		To      string          `json:"to"`
		Summary string          `json:"summary"`
		Message json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	var message string
	if err := json.Unmarshal(params.Message, &message); err != nil {
		return tools.Result{Content: "SendMessage currently supports plain text message only; structured swarm protocol messages are not implemented", IsError: true}
	}
	taskID, err := sendMessageRecipientTaskID(ctx, params.To, toolContext.TaskStore)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	inner, _ := json.Marshal(map[string]any{
		"task_id":    taskID,
		"content":    strings.TrimSpace(message),
		"from_agent": "coordinator",
	})
	return t.message.Run(ctx, inner, toolContext)
}

func sendMessageRecipientTaskID(ctx context.Context, to string, taskStore any) (uint64, error) {
	taskID, err := sendMessageTaskID(to)
	if err == nil {
		return taskID, nil
	}
	resolved, resolveErr := sendMessageTaskIDByName(ctx, to, taskStore)
	if resolveErr != nil {
		return 0, resolveErr
	}
	return resolved, nil
}

func sendMessageTaskID(to string) (uint64, error) {
	trimmed := strings.TrimSpace(to)
	if trimmed == "" {
		return 0, fmt.Errorf("to is required")
	}
	if trimmed == "*" {
		return 0, fmt.Errorf("SendMessage broadcast requires the full swarm/team mailbox protocol, which is not implemented in niuma yet; send to a concrete task_id or unique agent_name/description")
	}
	if strings.Contains(trimmed, "@") {
		return 0, fmt.Errorf("SendMessage to must be a bare task_id or agent name, not %q", to)
	}
	lower := strings.ToLower(trimmed)
	for _, prefix := range []string{"task_id:", "task:", "#"} {
		if strings.HasPrefix(lower, prefix) {
			trimmed = strings.TrimSpace(trimmed[len(prefix):])
			break
		}
	}
	taskID, err := strconv.ParseUint(trimmed, 10, 64)
	if err != nil || taskID == 0 {
		return 0, fmt.Errorf("SendMessage to must be a task_id returned by Agent/AgentCreate; got %q", to)
	}
	return taskID, nil
}

func sendMessageTaskIDByName(ctx context.Context, to string, taskStore any) (uint64, error) {
	name := strings.TrimSpace(to)
	if name == "" || name == "*" || strings.Contains(name, "@") {
		return 0, fmt.Errorf("SendMessage to must be a task_id returned by Agent/AgentCreate; got %q", to)
	}
	store, ok := taskStore.(listStore)
	if !ok {
		return 0, fmt.Errorf("SendMessage to must be a task_id returned by Agent/AgentCreate; got %q", to)
	}
	tasks, err := store.ListAgentTasks(ctx, 1000)
	if err != nil {
		return 0, err
	}
	matches := matchingSendMessageTasks(tasks, name)
	switch len(matches) {
	case 1:
		return matches[0].ID, nil
	case 0:
		return 0, fmt.Errorf("SendMessage to must be a task_id returned by Agent/AgentCreate or a unique agent_name/description; got %q", to)
	default:
		var ids []string
		for _, task := range matches {
			ids = append(ids, fmt.Sprintf("%d", task.ID))
		}
		return 0, fmt.Errorf("SendMessage recipient %q is ambiguous; matching task_ids: %s", to, strings.Join(ids, ", "))
	}
}

func matchingSendMessageTasks(tasks []mysqlstore.AgentTask, name string) []mysqlstore.AgentTask {
	normalized := strings.ToLower(strings.TrimSpace(name))
	if normalized == "" {
		return nil
	}
	var matches []mysqlstore.AgentTask
	seen := map[uint64]bool{}
	for _, task := range tasks {
		if task.ID == 0 || seen[task.ID] {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(task.AgentName), normalized) || strings.EqualFold(strings.TrimSpace(task.Description), normalized) {
			matches = append(matches, task)
			seen[task.ID] = true
		}
	}
	return matches
}

func (MessageTool) Name() string { return "AgentMessage" }

func (MessageTool) MaxResultSizeChars() int { return tools.ClaudeCodeDefaultDeclaredMaxResultSizeChars }

func (MessageTool) Description() string {
	return `Send a message to a running sub-agent task.

	Use AgentMessage to provide additional instructions or context to a running sub-agent
	without restarting it. The message is delivered as a user message in the sub-agent's conversation.
	Messages should clarify scope, add concrete facts, or answer a blocker; avoid sending
	vague instructions that make the agent infer missing context.`
}

func (MessageTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"task_id":{"type":"integer"},"content":{"type":"string"},"from_agent":{"type":"string"}},"required":["task_id","content"],"additionalProperties":false}`)
}

func (t MessageTool) Run(ctx context.Context, input json.RawMessage, toolContext tools.Context) tools.Result {
	var params struct {
		TaskID    uint64 `json:"task_id"`
		Content   string `json:"content"`
		FromAgent string `json:"from_agent"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	params.Content = strings.TrimSpace(params.Content)
	if params.TaskID == 0 || params.Content == "" {
		return tools.Result{Content: "task_id and content are required", IsError: true}
	}
	if toolContext.TaskStore == nil {
		return tools.Result{Content: "AgentMessage requires task store", IsError: true}
	}
	if store, ok := toolContext.TaskStore.(listStore); ok {
		task, found, err := findAgentTask(ctx, store, params.TaskID)
		if err != nil {
			return tools.Result{Content: err.Error(), IsError: true}
		}
		if !found {
			return tools.Result{Content: fmt.Sprintf("agent task not found: %d", params.TaskID), IsError: true}
		}
		if task.Status != agenttasks.StatusRunning {
			if result, ok := t.resumeTerminalTask(ctx, task, params, toolContext); ok {
				return result
			}
			return tools.Result{Content: fmt.Sprintf("AgentMessage can only send to running sub-agent tasks; task %d is %s. Use AgentGet to inspect the final state, or create a new AgentCreate task with the needed context.", params.TaskID, task.Status), IsError: true}
		}
	}
	payload, _ := json.Marshal(agenttasks.MessageInput{
		TaskID:    params.TaskID,
		FromAgent: strings.TrimSpace(params.FromAgent),
		Content:   params.Content,
		TraceID:   toolContext.TraceID,
	})
	id, err := toolContext.TaskStore.AppendAgentTaskEvent(ctx, agenttasks.EventInput{
		TenantID:    toolContext.TenantID,
		UserID:      toolContext.UserID,
		TaskID:      params.TaskID,
		EventType:   agenttasks.EventMessage,
		PayloadJSON: string(payload),
		TraceID:     toolContext.TraceID,
	})
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	return jsonResult(map[string]any{"task_id": params.TaskID, "message_id": id, "sent": true})
}

func (t MessageTool) resumeTerminalTask(ctx context.Context, task mysqlstore.AgentTask, params struct {
	TaskID    uint64 `json:"task_id"`
	Content   string `json:"content"`
	FromAgent string `json:"from_agent"`
}, toolContext tools.Context) (tools.Result, bool) {
	if t.runtime.Client == nil || t.runtime.Registry == nil || toolContext.TaskStore == nil {
		return tools.Result{}, false
	}
	entries, transcriptPath, ok := loadSubagentTranscript(task)
	if !ok {
		return tools.Result{}, false
	}
	runtime := t.runtime
	if runtime.TaskStore == nil {
		runtime.TaskStore = toolContext.TaskStore
	}
	if runtime.TaskStore == nil {
		return tools.Result{}, false
	}
	prompt := "Agent message from " + firstNonEmptyAgentString(strings.TrimSpace(params.FromAgent), "parent") + ":\n" + strings.TrimSpace(params.Content)
	worktreeResume := resolveResumableWorktree(task)
	worktreeInfo := worktreeResume.Info
	cwd := toolContext.CWD
	if worktreeInfo.Path != "" {
		cwd = worktreeInfo.Path
	}
	result, err := runtime.RunBackground(ctx, agentruntime.Request{
		Description:                   resumedAgentDescription(task),
		Prompt:                        prompt,
		InitialMessages:               subagentMessagesFromTranscript(entries),
		InitialToolResultReplacements: subagentToolResultReplacementsFromTranscript(entries),
		SubagentType:                  subagentTypeForResume(task),
		CWD:                           cwd,
		TenantID:                      toolContext.TenantID,
		UserID:                        toolContext.UserID,
		ParentSessionID:               toolContext.SessionID,
		TraceID:                       toolContext.TraceID,
		WorktreePath:                  worktreeInfo.Path,
		WorktreeBranch:                worktreeInfo.Branch,
		WorktreeHeadCommit:            worktreeInfo.HeadCommit,
		WorktreeGitRoot:               worktreeInfo.GitRoot,
	}, toolContext)
	if err != nil {
		return tools.Result{Content: fmt.Sprintf("failed to resume sub-agent task %d from transcript %s: %v", params.TaskID, transcriptPath, err), IsError: true}, true
	}
	out := map[string]any{
		"task_id":              result.TaskID,
		"resumed_from_task_id": params.TaskID,
		"status":               result.Status,
		"background":           result.Background,
		"agent_name":           result.AgentName,
		"model":                result.Model,
		"session_id":           result.SessionID,
		"output_file":          result.OutputFile,
		"transcript_path":      result.TranscriptPath,
		"source_transcript":    transcriptPath,
		"sent":                 true,
		"resumed":              true,
	}
	if worktreeInfo.Path != "" {
		out["worktree_resume"] = "retained"
		out["worktree_path"] = worktreeInfo.Path
		if worktreeInfo.Branch != "" {
			out["worktree_branch"] = worktreeInfo.Branch
		}
	} else if len(worktreeResume.MissingPaths) > 0 {
		out["worktree_resume"] = "fallback_parent_cwd"
		out["worktree_fallback_reason"] = "previous retained worktree no longer exists; resumed in parent cwd"
		out["previous_worktree_path"] = worktreeResume.MissingPaths[0]
	}
	return jsonResult(out), true
}

type worktreeResumeResolution struct {
	Info         agentworktree.Info
	MissingPaths []string
}

func resolveResumableWorktree(task mysqlstore.AgentTask) worktreeResumeResolution {
	var missing []string
	for _, info := range worktreeInfoCandidates(task) {
		if strings.TrimSpace(info.Path) == "" {
			continue
		}
		if stat, err := os.Stat(info.Path); err == nil && stat.IsDir() {
			now := time.Now()
			_ = os.Chtimes(info.Path, now, now)
			return worktreeResumeResolution{Info: info}
		}
		missing = append(missing, info.Path)
	}
	return worktreeResumeResolution{MissingPaths: missing}
}

func worktreeInfoCandidates(task mysqlstore.AgentTask) []agentworktree.Info {
	var out []agentworktree.Info
	var result agentruntime.Result
	if json.Unmarshal([]byte(strings.TrimSpace(task.ResultJSON)), &result) == nil {
		out = append(out, agentworktree.Info{
			Path:      strings.TrimSpace(result.WorktreePath),
			Branch:    strings.TrimSpace(result.WorktreeBranch),
			HookBased: result.WorktreeHookBased,
		})
	}
	var metadata struct {
		WorktreePath       string `json:"worktree_path"`
		WorktreeBranch     string `json:"worktree_branch"`
		WorktreeHeadCommit string `json:"worktree_head_commit"`
		WorktreeGitRoot    string `json:"worktree_git_root"`
		HookBased          bool   `json:"hook_based"`
		WorktreeHookBased  bool   `json:"worktree_hook_based"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(task.MetadataJSON)), &metadata) == nil {
		out = append(out, agentworktree.Info{
			Path:       strings.TrimSpace(metadata.WorktreePath),
			Branch:     strings.TrimSpace(metadata.WorktreeBranch),
			HeadCommit: strings.TrimSpace(metadata.WorktreeHeadCommit),
			GitRoot:    strings.TrimSpace(metadata.WorktreeGitRoot),
			HookBased:  metadata.HookBased || metadata.WorktreeHookBased,
		})
	}
	return out
}

func loadSubagentTranscript(task mysqlstore.AgentTask) ([]session.Entry, string, bool) {
	candidates := subagentTranscriptCandidates(task)
	for _, path := range candidates {
		entries, format, err := session.LoadWithFormat(path)
		if err != nil || session.ValidateResumeFormat(format) != nil || len(entries) == 0 {
			continue
		}
		// A v2 subagent transcript is a graph: resume from its current chain, not
		// raw file order. Identity for v1 transcripts.
		return session.CurrentChain(entries), path, true
	}
	return nil, "", false
}

func subagentTranscriptCandidates(task mysqlstore.AgentTask) []string {
	seen := map[string]bool{}
	var out []string
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" || seen[path] {
			return
		}
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			seen[path] = true
			out = append(out, path)
		}
	}
	var result agentruntime.Result
	if json.Unmarshal([]byte(strings.TrimSpace(task.ResultJSON)), &result) == nil {
		add(result.TranscriptPath)
	}
	if key := strings.TrimSpace(task.SubagentSessionKey); key != "" {
		if summary, ok, err := session.DefaultStore().Find(key); err == nil && ok {
			add(summary.Path)
		}
	}
	return out
}

func subagentMessagesFromTranscript(entries []session.Entry) []anthropic.MessageParam {
	var messages []anthropic.MessageParam
	for _, entry := range entries {
		switch entry.Type {
		case "message":
			if entry.Role == "user" || entry.Role == "assistant" {
				messages = append(messages, anthropic.MessageParam{
					Role:    entry.Role,
					Content: []anthropic.ContentBlock{{Type: "text", Text: entry.Content}},
				})
			}
		case "thinking", "redacted_thinking":
			messages = append(messages, anthropic.MessageParam{
				Role: "assistant",
				Content: []anthropic.ContentBlock{{
					Type:      entry.Type,
					Thinking:  entry.Content,
					Signature: entry.Signature,
				}},
			})
		case "tool_call":
			if strings.TrimSpace(entry.ToolID) == "" {
				continue
			}
			messages = append(messages, anthropic.MessageParam{
				Role: "assistant",
				Content: []anthropic.ContentBlock{{
					Type:  "tool_use",
					ID:    entry.ToolID,
					Name:  entry.ToolName,
					Input: json.RawMessage(entry.Content),
				}},
			})
		case "tool_result":
			if strings.TrimSpace(entry.ToolID) == "" {
				continue
			}
			messages = append(messages, anthropic.MessageParam{
				Role: "user",
				Content: []anthropic.ContentBlock{{
					Type:      "tool_result",
					ToolUseID: entry.ToolID,
					Content:   entry.Content,
					IsError:   entry.IsError,
				}},
			})
		case "compact_summary":
			messages = []anthropic.MessageParam{{
				Role:    "user",
				Content: []anthropic.ContentBlock{{Type: "text", Text: "Conversation summary so far:\n" + entry.Content}},
			}}
		}
	}
	return messages
}

func subagentToolResultReplacementsFromTranscript(entries []session.Entry) []toolresult.ReplacementRecord {
	var out []toolresult.ReplacementRecord
	for _, entry := range entries {
		if entry.Type != "content_replacement" {
			continue
		}
		for _, replacement := range entry.Replacements {
			kind := strings.TrimSpace(replacement.Kind)
			toolUseID := strings.TrimSpace(replacement.ToolUseID)
			if kind == "tool-result" && toolUseID != "" && replacement.Replacement != "" {
				out = append(out, toolresult.ReplacementRecord{
					Kind:        kind,
					ToolUseID:   toolUseID,
					Replacement: replacement.Replacement,
				})
			}
		}
	}
	return out
}

func resumedAgentDescription(task mysqlstore.AgentTask) string {
	description := strings.TrimSpace(task.Description)
	if description == "" {
		description = strings.TrimSpace(task.AgentName)
	}
	if description == "" {
		description = "resumed sub-agent"
	}
	return "resumed from task " + strconv.FormatUint(task.ID, 10) + ": " + description
}

func subagentTypeForResume(task mysqlstore.AgentTask) string {
	name := strings.TrimSpace(task.AgentName)
	if strings.EqualFold(name, "general-purpose") {
		return ""
	}
	return name
}

func firstNonEmptyAgentString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func findAgentTask(ctx context.Context, store listStore, taskID uint64) (mysqlstore.AgentTask, bool, error) {
	items, err := store.ListAgentTasks(ctx, 1000)
	if err != nil {
		return mysqlstore.AgentTask{}, false, err
	}
	for _, item := range items {
		if item.ID == taskID {
			return item, true, nil
		}
	}
	return mysqlstore.AgentTask{}, false, nil
}

func taskIDFromInput(input json.RawMessage) (uint64, error) {
	var raw map[string]any
	if err := json.Unmarshal(input, &raw); err != nil {
		return 0, err
	}
	value, ok := raw["task_id"]
	if !ok {
		return 0, fmt.Errorf("task_id is required")
	}
	switch typed := value.(type) {
	case float64:
		if typed <= 0 {
			return 0, fmt.Errorf("task_id is required")
		}
		return uint64(typed), nil
	case string:
		id, err := strconv.ParseUint(strings.TrimSpace(typed), 10, 64)
		if err != nil || id == 0 {
			return 0, fmt.Errorf("task_id is required")
		}
		return id, nil
	default:
		return 0, fmt.Errorf("task_id is required")
	}
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return 20
	}
	if limit > 100 {
		return 100
	}
	return limit
}

func parseMode(mode string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "subagent":
		return "subagent", nil
	case "teammate", "in_process", "in-process", "inprocess":
		return "teammate", nil
	default:
		return "", fmt.Errorf("mode must be subagent or teammate")
	}
}

func summarizeEvents(events []mysqlstore.AgentTaskEvent) map[string]any {
	summary := map[string]any{
		"events":       len(events),
		"turns":        0,
		"messages":     0,
		"tool_calls":   0,
		"tool_results": 0,
		"status":       "",
	}
	for _, event := range events {
		switch event.EventType {
		case agenttasks.EventStarted:
			summary["started"] = true
			mergePayload(summary, event.PayloadJSON, "agent_name", "model", "description")
		case agenttasks.EventTurnStart:
			summary["turns"] = intValue(summary["turns"]) + 1
		case agenttasks.EventMessage:
			summary["messages"] = intValue(summary["messages"]) + 1
		case agenttasks.EventTextDelta:
			if text := payloadString(event.PayloadJSON, "text"); text != "" {
				summary["latest_text"] = truncate(text, 160)
			}
		case agenttasks.EventToolCall:
			summary["tool_calls"] = intValue(summary["tool_calls"]) + 1
		case agenttasks.EventToolResult:
			summary["tool_results"] = intValue(summary["tool_results"]) + 1
		case agenttasks.EventCompleted:
			summary["status"] = agenttasks.StatusCompleted
		case agenttasks.EventFailed:
			summary["status"] = agenttasks.StatusFailed
			mergePayload(summary, event.PayloadJSON, "error")
		case agenttasks.EventCancelled:
			summary["status"] = agenttasks.StatusCancelled
		case agenttasks.EventTimeout:
			summary["status"] = agenttasks.StatusTimeout
			mergePayload(summary, event.PayloadJSON, "error", "stop_reason")
		}
	}
	if summary["status"] == "" && len(events) > 0 {
		summary["status"] = agenttasks.StatusRunning
	}
	return summary
}

func mergePayload(out map[string]any, payload string, keys ...string) {
	var decoded map[string]any
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		return
	}
	for _, key := range keys {
		if value, ok := decoded[key]; ok {
			out[key] = value
		}
	}
}

func payloadString(payload, key string) string {
	var decoded map[string]any
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		return ""
	}
	value, _ := decoded[key].(string)
	return value
}

func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func truncate(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func jsonResult(value any) tools.Result {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	return tools.Result{Content: string(data)}
}
