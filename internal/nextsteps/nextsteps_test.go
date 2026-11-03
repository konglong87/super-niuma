package nextsteps

import (
	"strings"
	"testing"
)

func TestBuildPromptCarriesConversationTail(t *testing.T) {
	prompt := BuildPrompt(Input{
		UserPrompt:        "帮我改 parse.go",
		AssistantResponse: "已经改好了 parse.go 的解析逻辑。",
		ToolNames:         []string{"Read", "Edit", "Edit"},
	}, 3)
	for _, want := range []string{"帮我改 parse.go", "已经改好了 parse.go 的解析逻辑。", "Read", "Edit"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q\n---\n%s", want, prompt)
		}
	}
}

// 工具名去重，否则连续 10 次 Edit 会把提示词灌满噪音。
func TestBuildPromptDedupesToolNames(t *testing.T) {
	prompt := BuildPrompt(Input{
		UserPrompt:        "改代码",
		AssistantResponse: "好了",
		ToolNames:         []string{"Edit", "Edit", "Edit"},
	}, 3)
	if got := strings.Count(prompt, "Edit"); got != 1 {
		t.Errorf("tool name Edit appears %d times, want 1\n---\n%s", got, prompt)
	}
}

func TestBuildPromptStatesRequestedCount(t *testing.T) {
	if prompt := BuildPrompt(Input{UserPrompt: "a", AssistantResponse: "b"}, 2); !strings.Contains(prompt, "2") {
		t.Errorf("prompt does not state the requested count\n---\n%s", prompt)
	}
}

// 没有 assistant 回复就无从推断下一步，不该发请求。
func TestBuildPromptReturnsEmptyWithoutResponse(t *testing.T) {
	for _, in := range []Input{
		{},
		{UserPrompt: "只有提问"},
		{AssistantResponse: "   "},
	} {
		if got := BuildPrompt(in, 3); got != "" {
			t.Errorf("BuildPrompt(%+v) = %q, want empty", in, got)
		}
	}
}
