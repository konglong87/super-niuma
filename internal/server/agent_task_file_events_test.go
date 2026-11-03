package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/query"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	"github.com/konglong87/super-niuma/internal/tools"
)

// fileChangeEvents returns the decoded payloads of every file_change event the
// fake recorded, in order.
func fileChangeEvents(t *testing.T, fake *fakeTenantService) []map[string]any {
	t.Helper()
	fake.mu.Lock()
	defer fake.mu.Unlock()
	out := make([]map[string]any, 0)
	for _, event := range fake.agentTaskEvents {
		if event.EventType != agenttasks.EventFileChange {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
			t.Fatalf("file_change payload json: %v (raw=%s)", err, event.PayloadJSON)
		}
		out = append(out, payload)
	}
	return out
}

// A write-family tool carries tools.FileChange records all the way into the
// sink. Those records are the authoritative description of what changed, so the
// emitted event must reproduce them rather than let the frontend guess.
func TestAgentTaskSinkEmitsStructuredFileChangeForEdits(t *testing.T) {
	fake := &fakeTenantService{}
	sink := &agentTaskTextSink{svc: fake, taskID: 7, ctx: context.Background()}

	trace := query.ToolTrace{
		ID:   "call-1",
		Name: "Edit",
		FileChanges: []tools.FileChange{{
			Path:         "/w/app.go",
			Before:       "one\ntwo\n",
			BeforeExists: true,
			After:        "one\ntwo\nthree\nfour\n",
			AfterExists:  true,
			Source:       "edit",
		}},
	}
	if err := sink.OnToolResult(context.Background(), trace); err != nil {
		t.Fatalf("OnToolResult: %v", err)
	}

	events := fileChangeEvents(t, fake)
	if len(events) != 1 {
		t.Fatalf("file_change events = %d, want 1", len(events))
	}
	payload := events[0]
	if payload["path"] != "/w/app.go" {
		t.Fatalf("path = %v", payload["path"])
	}
	if payload["access"] != fileAccessEdited {
		t.Fatalf("access = %v, want %q", payload["access"], fileAccessEdited)
	}
	if payload["change"] != fileChangeModified {
		t.Fatalf("change = %v, want %q", payload["change"], fileChangeModified)
	}
	if payload["tool_name"] != "Edit" {
		t.Fatalf("tool_name = %v", payload["tool_name"])
	}
	if payload["source"] != "edit" {
		t.Fatalf("source = %v, want the capturing tool boundary", payload["source"])
	}
	if payload["content_available"] != true {
		t.Fatalf("content_available = %v, want true for inline content", payload["content_available"])
	}
	// 2 lines before, 4 after: the delta is counted, never guessed.
	if v, ok := payload["line_delta"].(float64); !ok || v != 2 {
		t.Fatalf("line_delta = %v, want 2", payload["line_delta"])
	}
	if v, ok := payload["before_lines"].(float64); !ok || v != 2 {
		t.Fatalf("before_lines = %v, want 2", payload["before_lines"])
	}
	if v, ok := payload["after_lines"].(float64); !ok || v != 4 {
		t.Fatalf("after_lines = %v, want 4", payload["after_lines"])
	}
	if payload["task_scoped_id"] != nil {
		t.Fatalf("unexpected key task_scoped_id in %v", payload)
	}
}

// Creation and deletion are distinguishable from a plain modification, because
// the Files tab labels them differently.
func TestAgentTaskSinkClassifiesCreateAndDelete(t *testing.T) {
	fake := &fakeTenantService{}
	sink := &agentTaskTextSink{svc: fake, taskID: 7, ctx: context.Background()}

	trace := query.ToolTrace{
		ID:   "call-1",
		Name: "Write",
		FileChanges: []tools.FileChange{
			{Path: "/w/new.txt", After: "hello\n", AfterExists: true, Source: "write"},
			{Path: "/w/gone.txt", Before: "bye\n", BeforeExists: true, Source: "bash"},
		},
	}
	if err := sink.OnToolResult(context.Background(), trace); err != nil {
		t.Fatalf("OnToolResult: %v", err)
	}

	events := fileChangeEvents(t, fake)
	if len(events) != 2 {
		t.Fatalf("file_change events = %d, want 2", len(events))
	}
	if events[0]["change"] != fileChangeCreated {
		t.Fatalf("created change = %v", events[0]["change"])
	}
	if v, ok := events[0]["line_delta"].(float64); !ok || v != 1 {
		t.Fatalf("created line_delta = %v, want 1", events[0]["line_delta"])
	}
	if events[1]["change"] != fileChangeDeleted {
		t.Fatalf("deleted change = %v", events[1]["change"])
	}
	if v, ok := events[1]["line_delta"].(float64); !ok || v != -1 {
		t.Fatalf("deleted line_delta = %v, want -1", events[1]["line_delta"])
	}
}

// Large file bodies are externalized to the snapshot store, so the change
// carries no content. Reporting line_delta 0 there would be a lie dressed as
// data; the payload must say the content was unavailable instead.
func TestAgentTaskSinkMarksExternalizedContentUnavailable(t *testing.T) {
	fake := &fakeTenantService{}
	sink := &agentTaskTextSink{svc: fake, taskID: 7, ctx: context.Background()}

	trace := query.ToolTrace{
		ID:   "call-1",
		Name: "Bash",
		FileChanges: []tools.FileChange{{
			Path:               "/w/huge.bin",
			BeforeExists:       true,
			BeforeSnapshotPath: "/snapshots/abc",
			AfterExists:        true,
			After:              "[large file snapshot stored separately]",
			AfterSnapshotPath:  "/snapshots/def",
			Source:             "bash",
		}},
	}
	if err := sink.OnToolResult(context.Background(), trace); err != nil {
		t.Fatalf("OnToolResult: %v", err)
	}

	events := fileChangeEvents(t, fake)
	if len(events) != 1 {
		t.Fatalf("file_change events = %d, want 1", len(events))
	}
	if events[0]["content_available"] != false {
		t.Fatalf("content_available = %v, want false", events[0]["content_available"])
	}
	if _, ok := events[0]["line_delta"]; ok {
		t.Fatalf("line_delta must be absent when content is externalized: %v", events[0])
	}
}

// The read side has no tools.FileChange, but the tool input names the path the
// tool was actually told to read. That is real data, not a guessed payload key.
func TestAgentTaskSinkEmitsFileChangeForReads(t *testing.T) {
	fake := &fakeTenantService{}
	sink := &agentTaskTextSink{svc: fake, taskID: 7, ctx: context.Background()}

	trace := query.ToolTrace{
		ID:    "call-1",
		Name:  "Read",
		Input: `{"file_path":"/w/README.md","offset":2}`,
	}
	if err := sink.OnToolResult(context.Background(), trace); err != nil {
		t.Fatalf("OnToolResult: %v", err)
	}

	events := fileChangeEvents(t, fake)
	if len(events) != 1 {
		t.Fatalf("file_change events = %d, want 1", len(events))
	}
	if events[0]["path"] != "/w/README.md" {
		t.Fatalf("path = %v", events[0]["path"])
	}
	if events[0]["access"] != fileAccessRead {
		t.Fatalf("access = %v, want %q", events[0]["access"], fileAccessRead)
	}
	if _, ok := events[0]["change"]; ok {
		t.Fatalf("a read must not claim a change classification: %v", events[0])
	}
}

// A read that errored out read nothing, so it must not be reported as a file
// the agent touched.
func TestAgentTaskSinkSkipsFailedReads(t *testing.T) {
	fake := &fakeTenantService{}
	sink := &agentTaskTextSink{svc: fake, taskID: 7, ctx: context.Background()}

	trace := query.ToolTrace{
		ID:      "call-1",
		Name:    "Read",
		Input:   `{"file_path":"/w/missing.md"}`,
		IsError: true,
	}
	if err := sink.OnToolResult(context.Background(), trace); err != nil {
		t.Fatalf("OnToolResult: %v", err)
	}
	if events := fileChangeEvents(t, fake); len(events) != 0 {
		t.Fatalf("failed read emitted %d file_change events, want 0: %v", len(events), events)
	}
}

// Tools that touch no files stay silent rather than emitting empty rows.
func TestAgentTaskSinkEmitsNoFileChangeForUnrelatedTools(t *testing.T) {
	fake := &fakeTenantService{}
	sink := &agentTaskTextSink{svc: fake, taskID: 7, ctx: context.Background()}

	trace := query.ToolTrace{ID: "call-1", Name: "Grep", Input: `{"pattern":"foo"}`}
	if err := sink.OnToolResult(context.Background(), trace); err != nil {
		t.Fatalf("OnToolResult: %v", err)
	}
	if events := fileChangeEvents(t, fake); len(events) != 0 {
		t.Fatalf("Grep emitted %d file_change events, want 0", len(events))
	}
}

// The non-streaming runner path reconstructs events from result.ToolCalls. It
// must produce the same file_change rows, otherwise the Files tab is empty for
// exactly the runs that did not stream.
func TestAppendAgentTaskToolEventsEmitsFileChange(t *testing.T) {
	fake := &fakeTenantService{}
	calls := []query.ToolTrace{{
		ID:   "call-1",
		Name: "Write",
		FileChanges: []tools.FileChange{{
			Path:        "/w/created.txt",
			After:       "a\nb\n",
			AfterExists: true,
			Source:      "write",
		}},
	}}

	if err := appendAgentTaskToolEvents(context.Background(), fake, 7, "trace-1", calls, nil); err != nil {
		t.Fatalf("appendAgentTaskToolEvents: %v", err)
	}

	events := fileChangeEvents(t, fake)
	if len(events) != 1 {
		t.Fatalf("file_change events = %d, want 1", len(events))
	}
	if events[0]["path"] != "/w/created.txt" || events[0]["change"] != fileChangeCreated {
		t.Fatalf("payload = %v", events[0])
	}
}

// End-to-end across the HTTP boundary: a tool that edited and read files
// produces file_change rows that the events endpoint serves with exactly the keys
// the Files tab reads. web/src/components/WebAgentPage.files.test.ts asserts the
// consuming half against these same keys, so a rename on either side fails.
func TestAgentTaskFileChangeEventsReachTheEventsEndpoint(t *testing.T) {
	fake := &fakeTenantService{
		agentTasks: []mysqlstore.AgentTask{{ID: 7, Status: agenttasks.StatusCompleted}},
	}
	calls := []query.ToolTrace{
		{
			ID:   "call-1",
			Name: "Edit",
			FileChanges: []tools.FileChange{{
				Path:         "/repo/app.go",
				Before:       "one\n",
				BeforeExists: true,
				After:        "one\ntwo\nthree\n",
				AfterExists:  true,
				Source:       "edit",
			}},
		},
		{ID: "call-2", Name: "Read", Input: `{"file_path":"/repo/README.md"}`},
	}
	if err := appendAgentTaskToolEvents(context.Background(), fake, 7, "trace-e2e", calls, nil); err != nil {
		t.Fatalf("appendAgentTaskToolEvents: %v", err)
	}

	handler := NewHandler(Options{AuthToken: "token", TenantService: fake}, nil)
	req := httptest.NewRequest(http.MethodGet, "/tenant/agent-tasks/7/events?limit=100", nil)
	req.Header.Set("authorization", "Bearer token")
	req.Header.Set("X-User-Id", "user-test")
	req.Header.Set("X-Tenant-Key", "tenant-test")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	var response struct {
		Data []struct {
			EventType   string `json:"event_type"`
			PayloadJSON string `json:"payload_json"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v (raw=%s)", err, rec.Body.String())
	}
	served := make([]map[string]any, 0, 2)
	for _, event := range response.Data {
		if event.EventType != agenttasks.EventFileChange {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
			t.Fatalf("payload json: %v (raw=%s)", err, event.PayloadJSON)
		}
		served = append(served, payload)
	}
	if len(served) != 2 {
		t.Fatalf("served %d file_change events, want 2 (body=%s)", len(served), rec.Body.String())
	}

	edited, read := served[0], served[1]
	if edited["path"] != "/repo/app.go" || edited["access"] != fileAccessEdited || edited["change"] != fileChangeModified {
		t.Fatalf("edited payload = %v", edited)
	}
	if v, ok := edited["line_delta"].(float64); !ok || v != 2 {
		t.Fatalf("edited line_delta = %v, want 2", edited["line_delta"])
	}
	if read["path"] != "/repo/README.md" || read["access"] != fileAccessRead {
		t.Fatalf("read payload = %v", read)
	}

	// The keys the frontend reads, pinned by name. The old Files tab looked for
	// payload.file / payload.path / payload.filename and insertions/deletions;
	// only "path" was ever going to exist, and nothing set it.
	for _, key := range []string{"path", "access", "change", "tool_name", "content_available", "line_delta", "before_lines", "after_lines"} {
		if _, ok := edited[key]; !ok {
			t.Fatalf("edited payload missing key %q: %v", key, edited)
		}
	}
	for _, key := range []string{"file", "filename", "insertions", "deletions"} {
		if _, ok := edited[key]; ok {
			t.Fatalf("payload carries legacy guessed key %q: %v", key, edited)
		}
	}
}

// Directories and symlinks are described by object type so the UI does not
// render a directory creation as a file edit with a bogus line count.
func TestAgentTaskSinkDescribesNonRegularObjects(t *testing.T) {
	fake := &fakeTenantService{}
	sink := &agentTaskTextSink{svc: fake, taskID: 7, ctx: context.Background()}

	trace := query.ToolTrace{
		ID:   "call-1",
		Name: "Bash",
		FileChanges: []tools.FileChange{
			{Path: "/w/sub", AfterExists: true, AfterIsDir: true, Source: "bash"},
			{Path: "/w/link", AfterExists: true, AfterIsSymlink: true, AfterLinkTarget: "/w/app.go", Source: "bash"},
		},
	}
	if err := sink.OnToolResult(context.Background(), trace); err != nil {
		t.Fatalf("OnToolResult: %v", err)
	}

	events := fileChangeEvents(t, fake)
	if len(events) != 2 {
		t.Fatalf("file_change events = %d, want 2", len(events))
	}
	if events[0]["object"] != fileObjectDir {
		t.Fatalf("dir object = %v", events[0]["object"])
	}
	if _, ok := events[0]["line_delta"]; ok {
		t.Fatalf("a directory must not report a line delta: %v", events[0])
	}
	if events[1]["object"] != fileObjectSymlink {
		t.Fatalf("symlink object = %v", events[1]["object"])
	}
}
