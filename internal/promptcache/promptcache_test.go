package promptcache

import (
	"encoding/json"
	"testing"

	"github.com/konglong87/super-niuma/internal/anthropic"
)

func TestTrackerDetectsTextAndCacheControlChanges(t *testing.T) {
	blocks := []anthropic.SystemBlock{
		{Text: "static", CacheControl: &anthropic.CacheControl{Type: "ephemeral", TTL: "1h"}},
		{Text: "dynamic", CacheControl: &anthropic.CacheControl{Type: "ephemeral", TTL: "5m"}},
	}
	var tracker Tracker
	first := tracker.Observe(blocks)
	if first.Initialized || first.BreaksCache {
		t.Fatalf("first = %+v", first)
	}
	second := tracker.Observe(blocks)
	if !second.Initialized || second.BreaksCache || second.TextChanged || second.CacheControlChanged {
		t.Fatalf("second = %+v", second)
	}
	changedText := append([]anthropic.SystemBlock(nil), blocks...)
	changedText[1].Text = "different"
	third := tracker.Observe(changedText)
	if !third.BreaksCache || !third.TextChanged || third.CacheControlChanged {
		t.Fatalf("third = %+v", third)
	}
	changedCache := append([]anthropic.SystemBlock(nil), changedText...)
	changedCache[1].CacheControl = &anthropic.CacheControl{Type: "ephemeral", TTL: "1h"}
	fourth := tracker.Observe(changedCache)
	if !fourth.BreaksCache || fourth.TextChanged || !fourth.CacheControlChanged {
		t.Fatalf("fourth = %+v", fourth)
	}
	changedScope := append([]anthropic.SystemBlock(nil), changedCache...)
	changedScope[1].CacheControl = &anthropic.CacheControl{Type: "ephemeral", TTL: "1h", Scope: "global"}
	fifth := tracker.Observe(changedScope)
	if !fifth.BreaksCache || fifth.TextChanged || !fifth.CacheControlChanged {
		t.Fatalf("fifth = %+v", fifth)
	}
}

func TestRequestTrackerDetectsCacheSafeParamChanges(t *testing.T) {
	req := anthropic.MessagesRequest{
		Model: "model-a",
		SystemBlocks: []anthropic.SystemBlock{{
			Text:         "system",
			CacheControl: &anthropic.CacheControl{Type: "ephemeral", TTL: "1h", Scope: "global"},
		}},
		Messages: []anthropic.MessageParam{{Role: "user", Content: []anthropic.ContentBlock{{Type: "text", Text: "hi"}}}},
		Tools: []anthropic.ToolDefinition{{
			Name:        "Echo",
			Description: "echo",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}},
	}
	var tracker RequestTracker
	first := tracker.Observe(req, map[string]string{"effort": "low"})
	if first.Initialized || first.BreaksCache {
		t.Fatalf("first = %+v", first)
	}
	second := tracker.Observe(req, map[string]string{"effort": "low"})
	if !second.Initialized || second.BreaksCache {
		t.Fatalf("second = %+v", second)
	}

	modelReq := req
	modelReq.Model = "model-b"
	modelReport := tracker.Observe(modelReq, map[string]string{"effort": "low"})
	if !modelReport.BreaksCache || !modelReport.ModelChanged {
		t.Fatalf("modelReport = %+v", modelReport)
	}

	toolReq := req
	toolReq.Tools = append([]anthropic.ToolDefinition(nil), req.Tools...)
	toolReq.Tools[0].InputSchema = json.RawMessage(`{"type":"object","required":["text"]}`)
	toolReport := tracker.Observe(toolReq, map[string]string{"effort": "low"})
	if !toolReport.BreaksCache || !toolReport.ModelChanged || !toolReport.ToolsChanged {
		t.Fatalf("toolReport = %+v", toolReport)
	}

	messageReq := req
	messageReq.Messages = []anthropic.MessageParam{{Role: "user", Content: []anthropic.ContentBlock{{Type: "text", Text: "different"}}}}
	messageReport := tracker.Observe(messageReq, map[string]string{"effort": "low"})
	if !messageReport.BreaksCache || !messageReport.ToolsChanged || !messageReport.MessageChanged {
		t.Fatalf("messageReport = %+v", messageReport)
	}

	thinkingReport := tracker.Observe(req, map[string]string{"effort": "high"})
	if !thinkingReport.BreaksCache || !thinkingReport.MessageChanged || !thinkingReport.ThinkingChanged {
		t.Fatalf("thinkingReport = %+v", thinkingReport)
	}
}
