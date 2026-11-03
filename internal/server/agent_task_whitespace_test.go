package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/agenttasks"
)

func TestAgentTaskSinkPreservesThinkingAndTextWhitespace(t *testing.T) {
	chunks := []string{"The", " user", " ", "just", " said", " 测试01", ".\n\n", "```go\n", "\t", "return", " ", "true\n", "```\n", ""}
	for _, eventType := range []string{agenttasks.EventThinking, agenttasks.EventTextDelta} {
		t.Run(eventType, func(t *testing.T) {
			fake := &fakeTenantService{}
			var output strings.Builder
			sink := &agentTaskTextSink{svc: fake, taskID: 7, ctx: context.Background(), text: &output}
			for _, chunk := range chunks {
				if eventType == agenttasks.EventThinking {
					if err := sink.OnThinking(context.Background(), chunk); err != nil {
						t.Fatal(err)
					}
				} else if _, err := sink.Write([]byte(chunk)); err != nil {
					t.Fatal(err)
				}
			}
			if len(fake.agentTaskEvents) != len(chunks)-1 {
				t.Fatalf("events = %d, want every nonempty fragment", len(fake.agentTaskEvents))
			}
			var restored strings.Builder
			for i, event := range fake.agentTaskEvents {
				var payload struct {
					Content string `json:"content"`
				}
				if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
					t.Fatal(err)
				}
				if event.EventType != eventType || payload.Content != chunks[i] {
					t.Fatalf("fragment %d = %q, want %q", i, payload.Content, chunks[i])
				}
				restored.WriteString(payload.Content)
			}
			if restored.String() != strings.Join(chunks, "") {
				t.Fatal("persisted fragments lost whitespace")
			}
			if eventType == agenttasks.EventTextDelta && output.String() != restored.String() {
				t.Fatal("final output differs from persisted events")
			}
		})
	}
}
