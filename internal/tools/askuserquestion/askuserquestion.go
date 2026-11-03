package askuserquestion

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/konglong87/super-niuma/internal/tools"
)

type Tool struct{}

func New() Tool { return Tool{} }

func (Tool) Name() string { return "AskUserQuestion" }

func (Tool) MaxResultSizeChars() int { return tools.ClaudeCodeDefaultDeclaredMaxResultSizeChars }

func (Tool) Description() string {
	return `Ask the user a concise question and wait for their answer.

Use AskUserQuestion when you genuinely cannot proceed without user input:
ambiguous requirements, multiple valid approaches, or destructive actions needing confirmation.
Do NOT use this for questions you can answer from the codebase or user's request.

Whenever you do ask the user a question - including casual or follow-up questions -
ask it through this tool instead of writing it as plain text with numbered options.
Provide choices when there are clear options for the user to select.

In interactive mode the user picks a choice (or types a custom answer) in a dialog;
in non-interactive mode this records the question and stops for user input.`
}

func (Tool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
	  "type": "object",
	  "properties": {
	    "question": {"type": "string"},
	    "choices": {"type": "array", "items": {"type": "string"}}
	  },
	  "required": ["question"],
	  "additionalProperties": false
	}`)
}

func (Tool) Run(ctx context.Context, input json.RawMessage, tc tools.Context) tools.Result {
	var params struct {
		Question string   `json:"question"`
		Choices  []string `json:"choices"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	question := strings.TrimSpace(params.Question)
	if question == "" {
		return tools.Result{Content: "question is required", IsError: true}
	}
	choices := make([]string, 0, len(params.Choices))
	for _, choice := range params.Choices {
		if c := strings.TrimSpace(choice); c != "" {
			choices = append(choices, c)
		}
	}
	if tc.UserQuestion != nil {
		resp := tc.UserQuestion(ctx, tools.UserQuestionRequest{Question: question, Choices: choices, ToolUseID: tc.Invocation.ToolUseID})
		if message := strings.TrimSpace(resp.Error); message != "" {
			return tools.Result{Content: message, IsError: true}
		}
		if answer := strings.TrimSpace(resp.Answer); resp.Answered && answer != "" {
			return tools.Result{Content: "User answered: " + answer}
		}
		if resp.Pending {
			return tools.Result{Interaction: &tools.InteractionRequest{
				ID:       resp.InteractionID,
				Kind:     "user_question",
				Question: question,
				Choices:  append([]string(nil), choices...),
			}}
		}
	}
	var b strings.Builder
	b.WriteString("User input required: ")
	b.WriteString(question)
	for _, choice := range choices {
		b.WriteString("\n- ")
		b.WriteString(choice)
	}
	return tools.Result{Content: b.String(), IsError: true}
}
