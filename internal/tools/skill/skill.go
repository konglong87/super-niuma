package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/skills"
	"github.com/konglong87/super-niuma/internal/tools"
)

type MessageStreamer interface {
	StreamMessages(ctx context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error)
}

type Tool struct {
	client MessageStreamer
	model  string
}

func New(args ...any) Tool {
	var tool Tool
	if len(args) > 0 {
		if client, ok := args[0].(MessageStreamer); ok {
			tool.client = client
		}
	}
	if len(args) > 1 {
		if model, ok := args[1].(string); ok {
			tool.model = model
		}
	}
	return tool
}

func (Tool) Name() string { return "Skill" }

func (Tool) MaxResultSizeChars() int { return tools.ClaudeCodeDefaultDeclaredMaxResultSizeChars }

func (Tool) Description() string {
	if claudeCompatiblePromptProfile() {
		return `Execute a skill within the main conversation

When users ask you to perform tasks, check if any of the available skills match. Skills provide specialized capabilities and domain knowledge.

When users reference a "slash command" or "/<something>" (e.g., "/commit", "/review-pr"), they are referring to a skill. Use this tool to invoke it.

How to invoke:
- Use this tool with the skill name and optional arguments
- Examples:
  - ` + "`skill: \"pdf\"`" + ` - invoke the pdf skill
  - ` + "`skill: \"commit\", args: \"-m 'Fix bug'\"`" + ` - invoke with arguments
  - ` + "`skill: \"review-pr\", args: \"123\"`" + ` - invoke with arguments
  - ` + "`skill: \"ms-office-suite:pdf\"`" + ` - invoke using fully qualified name

Important:
- Available skills are listed in system-reminder messages in the conversation
- When a skill matches the user's request, this is a BLOCKING REQUIREMENT: invoke the relevant Skill tool BEFORE generating any other response about the task
- NEVER mention a skill without actually calling this tool
- Do not invoke a skill that is already running
- Do not use this tool for built-in CLI commands (like /help, /clear, etc.)
- If you see a <command-name> tag in the current conversation turn, the skill has ALREADY been loaded - follow the instructions directly instead of calling this tool again`
	}
	return `Load a tenant or local skill's SKILL.md instructions by name.

Use Skill to activate a named skill's instructions. The skill content becomes
part of the conversation context, guiding subsequent tool usage and behavior.
When a listed skill matches the user's request or the current task evidence,
call this tool before taking task-specific actions or giving task-specific
answers.
Skills with execution_context "fork" run in an isolated model call.`
}

func (Tool) InputSchema() json.RawMessage {
	if claudeCompatiblePromptProfile() {
		return json.RawMessage(`{
		  "$schema": "https://json-schema.org/draft/2020-12/schema",
		  "type": "object",
		  "properties": {
		    "skill": {"description": "The skill name. E.g., \"commit\", \"review-pr\", or \"pdf\"", "type": "string"},
		    "args": {"description": "Optional arguments for the skill", "type": "string"}
		  },
		  "required": ["skill"],
		  "additionalProperties": false
		}`)
	}
	return json.RawMessage(`{
	  "type": "object",
	  "properties": {
	    "name": {"type": "string", "description": "Skill name to load."},
	    "prompt": {"type": "string", "description": "Optional prompt for forked skill execution."}
	  },
	  "required": ["name"],
	  "additionalProperties": false
	}`)
}

func (t Tool) Run(ctx context.Context, input json.RawMessage, toolContext tools.Context) tools.Result {
	var params struct {
		Name   string `json:"name"`
		Prompt string `json:"prompt"`
		Skill  string `json:"skill"`
		Args   string `json:"args"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	name := firstNonEmpty(params.Skill, params.Name)
	prompt := firstNonEmpty(params.Args, params.Prompt)
	if strings.TrimSpace(name) == "" {
		return tools.Result{Content: "skill name is required", IsError: true}
	}
	skill, ok, err := loadSkill(ctx, toolContext, name)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if !ok {
		return tools.Result{Content: fmt.Sprintf("skill %q not found", name), IsError: true}
	}
	if strings.EqualFold(skill.ExecutionContext, "fork") && t.client != nil {
		return t.runForked(ctx, skill, prompt)
	}
	content := strings.TrimSpace(skill.Content)
	resultContent := fmt.Sprintf("Skill %s loaded. Its instructions have been added to the conversation context.", skill.Name)
	if claudeCompatiblePromptProfile() {
		resultContent = fmt.Sprintf("Launching skill: %s", skill.Name)
	}
	return tools.Result{
		Content: resultContent,
		ContextMessages: []anthropic.MessageParam{{
			Role:    "user",
			Content: []anthropic.ContentBlock{{Type: "text", Text: "<system-reminder>\nSkill " + skill.Name + " instructions are now active. Follow them for subsequent work unless they conflict with higher-priority instructions. Each shell tool call starts a fresh process, so shell-local variables and functions do not persist. When NIUMA_SKILL_DIR is set, use it as the stable directory for this filesystem-backed Skill.\n\n" + content + "\n</system-reminder>"}},
		}},
	}
}

func loadSkill(ctx context.Context, toolContext tools.Context, name string) (skills.Skill, bool, error) {
	if toolContext.SkillProvider != nil {
		skill, ok, err := toolContext.SkillProvider.GetTenantSkill(ctx, name)
		if err != nil || ok {
			return skill, ok, err
		}
	}
	return skills.Load(toolContext.CWD, name)
}

func (t Tool) runForked(ctx context.Context, skill skills.Skill, prompt string) tools.Result {
	model := t.model
	if strings.TrimSpace(skill.Model) != "" && skill.Model != "inherit" {
		model = skill.Model
	}
	if strings.TrimSpace(model) == "" {
		model = "claude-sonnet-4-6"
	}
	if strings.TrimSpace(prompt) == "" {
		prompt = "Execute this skill and return the result."
	}
	var out strings.Builder
	res, err := t.client.StreamMessages(ctx, anthropic.MessagesRequest{
		Model:     model,
		MaxTokens: 4096,
		System:    "You are executing a Claude Code skill in an isolated forked context.\n\n" + skill.Content,
		Messages: []anthropic.MessageParam{{
			Role:    "user",
			Content: []anthropic.ContentBlock{{Type: "text", Text: prompt}},
		}},
	}, anthropic.StreamCallbacks{OnText: func(text string) error {
		out.WriteString(text)
		return nil
	}})
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if out.Len() == 0 {
		for _, block := range res.Message.Content {
			if block.Type == "text" {
				out.WriteString(block.Text)
			}
		}
	}
	return tools.Result{Content: out.String()}
}

func claudeCompatiblePromptProfile() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("NIUMA_PROMPT_PROFILE"))) {
	case "claude-compatible", "claude-compatible-strict":
		return true
	default:
		return false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
