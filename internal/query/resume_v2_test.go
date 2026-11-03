package query

import (
	"testing"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/session"
)

// v2 message-graph resume goes through session.CurrentChain (leaf-walk) before
// the shared converter, exactly as the CLI resume loaders do.
func resumeV2(entries []session.Entry) []anthropicMessageView {
	return summarizeMessages(MessagesFromTranscript(session.CurrentChain(entries)))
}

type anthropicMessageView struct {
	role  string
	kinds []string // per-block "type[:id]" summary
	texts []string
}

func summarizeMessages(msgs []anthropic.MessageParam) []anthropicMessageView {
	out := make([]anthropicMessageView, 0, len(msgs))
	for _, m := range msgs {
		view := anthropicMessageView{role: m.Role}
		for _, b := range m.Content {
			switch b.Type {
			case "text":
				view.kinds = append(view.kinds, "text")
				view.texts = append(view.texts, b.Text)
			case "tool_use":
				view.kinds = append(view.kinds, "tool_use:"+b.ID)
			case "tool_result":
				view.kinds = append(view.kinds, "tool_result:"+b.ToolUseID)
			default:
				view.kinds = append(view.kinds, b.Type)
			}
		}
		out = append(out, view)
	}
	return out
}

func TestResumeV2ExcludesAbandonedBranch(t *testing.T) {
	// A -> B(user) -> C(asst "old"); rewind to B; then C'(asst "new") from B.
	entries := []session.Entry{
		{Type: session.EntryTypeSessionMeta, Schema: session.SchemaV2, ID: "meta"},
		{Type: "message", Role: "user", Schema: session.SchemaV2, ID: "A"},
		{Type: "message", Role: "user", Schema: session.SchemaV2, ID: "B", ParentID: "A"},
		{Type: "message", Role: "assistant", Schema: session.SchemaV2, ID: "C", ParentID: "B", Content: "old"},
		{Type: session.EntryTypeBranchHead, Schema: session.SchemaV2, LeafID: "B", Reason: session.BranchHeadReasonRewind},
		{Type: "message", Role: "assistant", Schema: session.SchemaV2, ID: "Cprime", ParentID: "B", Content: "new"},
	}
	views := resumeV2(entries)
	// Expect user(A), user(B), assistant("new"). The abandoned "old" must be gone.
	var texts []string
	for _, v := range views {
		texts = append(texts, v.texts...)
	}
	for _, txt := range texts {
		if txt == "old" {
			t.Fatalf("abandoned branch message leaked into resume: %v", texts)
		}
	}
	if len(views) == 0 || views[len(views)-1].role != "assistant" || len(views[len(views)-1].texts) == 0 || views[len(views)-1].texts[0] != "new" {
		t.Fatalf("resume tip = %+v, want assistant \"new\"", views)
	}
}

func TestResumeV2PreservesParallelToolPairing(t *testing.T) {
	// One assistant turn issues two tool_use, followed by their two tool_result.
	entries := []session.Entry{
		{Type: session.EntryTypeSessionMeta, Schema: session.SchemaV2, ID: "meta"},
		{Type: "message", Role: "user", Schema: session.SchemaV2, ID: "u", Content: "go"},
		{Type: "tool_call", Schema: session.SchemaV2, ID: "tc1", ParentID: "u", ToolID: "toolu_read", ToolName: "Read", Content: "{}"},
		{Type: "tool_call", Schema: session.SchemaV2, ID: "tc2", ParentID: "tc1", ToolID: "toolu_grep", ToolName: "Grep", Content: "{}"},
		{Type: "tool_result", Schema: session.SchemaV2, ID: "tr1", ParentID: "tc2", ToolID: "toolu_read", Content: "readout"},
		{Type: "tool_result", Schema: session.SchemaV2, ID: "tr2", ParentID: "tr1", ToolID: "toolu_grep", Content: "grepout"},
	}
	views := resumeV2(entries)
	// Every tool_use must be answered by a matching tool_result; no dangling.
	uses := map[string]bool{}
	results := map[string]bool{}
	for _, v := range views {
		for _, k := range v.kinds {
			if len(k) > 9 && k[:9] == "tool_use:" {
				uses[k[9:]] = true
			}
			if len(k) > 12 && k[:12] == "tool_result:" {
				results[k[12:]] = true
			}
		}
	}
	for id := range uses {
		if !results[id] {
			t.Fatalf("tool_use %s has no matching tool_result; views=%+v", id, views)
		}
	}
	if !uses["toolu_read"] || !uses["toolu_grep"] {
		t.Fatalf("expected both tool_use blocks; views=%+v", views)
	}
}

func TestResumeV2LinearMatchesV1(t *testing.T) {
	// v1 linear transcript.
	v1 := []session.Entry{
		{Type: "message", Role: "user", Content: "hi"},
		{Type: "tool_call", ID: "c1", ToolID: "toolu_1", ToolName: "Read", Content: "{}"},
		{Type: "tool_result", ID: "r1", ToolID: "toolu_1", Content: "ok"},
		{Type: "message", Role: "assistant", Content: "done"},
	}
	// Same conversation as a linear v2 graph.
	v2 := []session.Entry{
		{Type: session.EntryTypeSessionMeta, Schema: session.SchemaV2, ID: "meta"},
		{Type: "message", Role: "user", Schema: session.SchemaV2, ID: "u", Content: "hi"},
		{Type: "tool_call", Schema: session.SchemaV2, ID: "c1", ParentID: "u", ToolID: "toolu_1", ToolName: "Read", Content: "{}"},
		{Type: "tool_result", Schema: session.SchemaV2, ID: "r1", ParentID: "c1", ToolID: "toolu_1", Content: "ok"},
		{Type: "message", Role: "assistant", Schema: session.SchemaV2, ID: "a", ParentID: "r1", Content: "done"},
	}
	got1 := summarizeMessages(MessagesFromTranscript(session.CurrentChain(v1)))
	got2 := resumeV2(v2)
	if len(got1) != len(got2) {
		t.Fatalf("message count differs: v1=%d v2=%d", len(got1), len(got2))
	}
	for i := range got1 {
		if got1[i].role != got2[i].role {
			t.Fatalf("role mismatch at %d: v1=%s v2=%s", i, got1[i].role, got2[i].role)
		}
		if len(got1[i].kinds) != len(got2[i].kinds) {
			t.Fatalf("block count mismatch at %d: v1=%v v2=%v", i, got1[i].kinds, got2[i].kinds)
		}
		for j := range got1[i].kinds {
			if got1[i].kinds[j] != got2[i].kinds[j] {
				t.Fatalf("block mismatch at %d/%d: v1=%s v2=%s", i, j, got1[i].kinds[j], got2[i].kinds[j])
			}
		}
	}
}
