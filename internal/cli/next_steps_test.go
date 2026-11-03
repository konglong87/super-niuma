package cli

import (
	"encoding/json"
	"testing"

	"github.com/konglong87/super-niuma/internal/tui"
)

func TestNextStepsEventCarriesSuggestions(t *testing.T) {
	event, ok := nextStepsEvent([]string{"跑一遍测试", "补单元测试"})
	if !ok {
		t.Fatal("nextStepsEvent reported no event for two suggestions")
	}
	if event.Type != tui.StreamNextSteps {
		t.Fatalf("Type = %q, want %q", event.Type, tui.StreamNextSteps)
	}
	var got []string
	if err := json.Unmarshal(event.Payload, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "跑一遍测试" {
		t.Fatalf("payload = %q, want the two suggestions", got)
	}
}

// 没有候选就不该造事件——避免下游收到一个空面板信号。
func TestNextStepsEventReportsNothingForEmptySuggestions(t *testing.T) {
	if _, ok := nextStepsEvent(nil); ok {
		t.Error("nextStepsEvent reported an event for zero suggestions")
	}
}

func TestToolNamesFromResultSkipsBlanks(t *testing.T) {
	got := toolNamesFromResult(tui.QueryResult{ToolCalls: []tui.ToolCall{
		{Name: "Read"}, {Name: "Edit"}, {Name: "   "},
	}})
	if len(got) != 2 || got[0] != "Read" || got[1] != "Edit" {
		t.Fatalf("got %q, want [Read Edit]", got)
	}
}
