package handoff

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/sessioncontrol"
	"github.com/konglong87/super-niuma/internal/storage/mysql"
	"github.com/konglong87/super-niuma/internal/tenant"
)

func TestTenantHandoffWebSourceCapturesMessageEventsAndCompletedAnswer(t *testing.T) {
	store := webTenantSourceStore()
	store.events = append(store.events, mysql.AgentTaskEvent{ID: 20, TaskID: 12, EventType: agenttasks.EventTextDelta, PayloadJSON: `{"content":"intermediate text must not become a fact"}`})
	snapshot, err := NewTenantSourceReader(store).Capture(context.Background(), handoffRequestContext(), tenantSourceRef())
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Target = Target{Ref: "tenant:target"}
	pkg, err := Extract(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Objective != "Remember the code ALPHA-SSE-OK." || !reflect.DeepEqual(pkg.Completed, []string{"The code is ALPHA-SSE-OK."}) {
		t.Fatalf("web conversation not captured: objective=%q completed=%q", pkg.Objective, pkg.Completed)
	}
	for _, evidence := range pkg.Evidence {
		if evidence.Verification != VerificationReported {
			t.Fatalf("conversation content must remain reported: %+v", evidence)
		}
	}
	encoded, err := EncodeSessionHandoffEvent(pkg, 42)
	if err != nil {
		t.Fatalf("new context must pass the existing event gate: %v", err)
	}
	if _, err := agenttasks.DecodeSessionHandoffEvent(encoded); err != nil {
		t.Fatalf("new context rejected by runtime decoder: %v", err)
	}
	if strings.Contains(encoded, "intermediate text must not become a fact") {
		t.Fatal("streamed transcript was copied into package")
	}
}

func TestTenantHandoffWebSourceIgnoresStreamNoiseAndFitsPackageBudget(t *testing.T) {
	store := webTenantSourceStore()
	for index := 0; index < 88; index++ {
		store.events = append(store.events, mysql.AgentTaskEvent{
			ID:          uint64(100 + index),
			TaskID:      12,
			EventType:   agenttasks.EventTextDelta,
			PayloadJSON: `{"content":"` + strings.Repeat("stream fragment ", 20) + `"}`,
		})
	}
	store.events = append(store.events,
		mysql.AgentTaskEvent{ID: 200, TaskID: 12, EventType: agenttasks.EventUsage, PayloadJSON: `{"status":"recorded"}`},
		mysql.AgentTaskEvent{ID: 201, TaskID: 12, EventType: agenttasks.EventToolResult, PayloadJSON: `{"tool_name":"Read","success":true}`},
		mysql.AgentTaskEvent{ID: 202, TaskID: 12, EventType: agenttasks.EventFileChange, PayloadJSON: `{"path":"internal/sessioncontrol/managed.go","status":"read"}`},
	)

	snapshot, err := NewTenantSourceReader(store).Capture(context.Background(), handoffRequestContext(), tenantSourceRef())
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Target = Target{Ref: "tenant:target"}
	pkg, err := Extract(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	result, err := CompressPackage(context.Background(), pkg, nil, 13_654)
	if err != nil {
		t.Fatalf("bounded source package exceeded its unchanged budget: %v", err)
	}
	if result.Budget.EstimatedTokens > DefaultPackageTokenLimit {
		t.Fatalf("estimated tokens = %d, limit = %d", result.Budget.EstimatedTokens, DefaultPackageTokenLimit)
	}
	if pkg.Objective != "Remember the code ALPHA-SSE-OK." || !reflect.DeepEqual(pkg.Completed, []string{"The code is ALPHA-SSE-OK."}) {
		t.Fatalf("conversation facts lost: objective=%q completed=%q", pkg.Objective, pkg.Completed)
	}
	refs := make(map[string]bool, len(pkg.Evidence))
	for _, evidence := range pkg.Evidence {
		refs[evidence.Ref] = true
		if strings.Contains(evidence.Ref, "task_event:1") && evidence.Ref != "tenant:source#task_event:17" && evidence.Ref != "tenant:source#task_event:18" {
			t.Fatalf("stream evidence leaked into protected package: %s", evidence.Ref)
		}
	}
	for _, ref := range []string{"tenant:source#task_event:17", "tenant:source#task_event:18", "tenant:source#tool_trace:201:0", "tenant:source#task_event:202"} {
		if !refs[ref] {
			t.Fatalf("meaningful evidence %q was not retained: %#v", ref, pkg.Evidence)
		}
	}
}

func TestTenantHandoffWebSourceBudgetsRichEvidenceWithLongSessionRefs(t *testing.T) {
	store := webTenantSourceStore()
	sourceKey := "source-" + strings.Repeat("a", 89)
	targetKey := "target-" + strings.Repeat("b", 89)
	store.session.SessionKey = sourceKey
	store.tasks[0].SubagentSessionKey = sourceKey
	for index := 0; index < tenantHandoffMessageLimit; index++ {
		store.messages = append(store.messages, mysql.Message{
			ID:        uint64(index + 1),
			SessionID: store.session.ID,
			Role:      "user",
			Content:   "historical user instruction " + strconv.Itoa(index+1),
			TurnIndex: uint(index + 1),
		})
	}
	for index := 0; index < 40; index++ {
		store.events = append(store.events, mysql.AgentTaskEvent{
			ID:          uint64(100 + index),
			TaskID:      12,
			EventType:   agenttasks.EventToolResult,
			PayloadJSON: `{"tool_name":"Read","success":true,"path":"internal/sessioncontrol/handoff/tenant_source.go"}`,
		})
	}
	sourceRef := sessioncontrol.SessionRef{Source: sessioncontrol.SourceTenant, Key: sourceKey}
	snapshot, err := NewTenantSourceReader(store).Capture(context.Background(), handoffRequestContext(), sourceRef)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Target = Target{Ref: (sessioncontrol.SessionRef{Source: sessioncontrol.SourceTenant, Key: targetKey}).String()}
	pkg, err := Extract(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PlanBudget(pkg, 100_000); err != nil {
		t.Fatalf("rich source package exceeds its protected budget: %v", err)
	}
	if pkg.Budget.EstimatedTokens > DefaultPackageTokenLimit {
		t.Fatalf("estimated tokens = %d, limit = %d", pkg.Budget.EstimatedTokens, DefaultPackageTokenLimit)
	}
	worstCase := snapshot
	worstCase.Target = Target{Ref: SourceKindTenant + ":" + strings.Repeat(string(tenantHandoffBudgetTargetKeyRune), tenantHandoffSessionKeyMaxRunes)}
	worstCasePackage, err := Extract(worstCase)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PlanBudget(worstCasePackage, 100_000); err != nil {
		t.Fatalf("selected evidence exceeds the database-length target bound: %v", err)
	}
	if pkg.Objective != "Remember the code ALPHA-SSE-OK." || !reflect.DeepEqual(pkg.Completed, []string{"The code is ALPHA-SSE-OK."}) {
		t.Fatalf("mandatory conversation anchors lost: objective=%q completed=%q", pkg.Objective, pkg.Completed)
	}
	refs := make(map[string]bool, len(pkg.Evidence))
	for _, evidence := range snapshot.Evidence {
		refs[evidence.Ref] = true
	}
	for _, locator := range []string{
		tenantEventLocator(sourceRef, store.events[0]),
		tenantEventLocator(sourceRef, store.events[1]),
		tenantLocator(sourceRef, CursorPrefixMessage, tenantHandoffMessageLimit),
		tenantEventLocator(sourceRef, store.events[len(store.events)-1]),
	} {
		if !refs[locator] {
			t.Fatalf("required recent evidence %q was dropped: %#v", locator, snapshot.Evidence)
		}
	}
	if snapshot.Source.Cursor != "task_event:139" {
		t.Fatalf("source cursor = %q, want full source progress task_event:139", snapshot.Source.Cursor)
	}
}

func TestTenantHandoffWebSourceCapsConversationCandidatesBeforeExtraction(t *testing.T) {
	store := webTenantSourceStore()
	for index, key := range []string{"content", "response"} {
		payload, err := json.Marshal(map[string]string{key: strings.Repeat("界", MaxCandidateBytes)})
		if err != nil {
			t.Fatal(err)
		}
		store.events[index].PayloadJSON = string(payload)
	}
	snapshot, err := NewTenantSourceReader(store).Capture(context.Background(), handoffRequestContext(), tenantSourceRef())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Objectives) != 1 || len(snapshot.Facts) != 1 {
		t.Fatalf("bounded request/answer candidates missing: objectives=%d facts=%d", len(snapshot.Objectives), len(snapshot.Facts))
	}
	for _, candidate := range snapshot.Objectives {
		if candidate.Text != capText(strings.Repeat("界", MaxCandidateBytes)) || len(candidate.Text) > MaxCandidateBytes || !utf8.ValidString(candidate.Text) {
			t.Fatalf("objective was not bounded before extraction: bytes=%d", len(candidate.Text))
		}
	}
	for _, fact := range snapshot.Facts {
		if fact.Text != capText(strings.Repeat("界", MaxCandidateBytes)) || len(fact.Text) > MaxCandidateBytes || !utf8.ValidString(fact.Text) {
			t.Fatalf("answer was not bounded before extraction: bytes=%d", len(fact.Text))
		}
	}
	assertSourceSnapshotExtractable(t, snapshot)
}

func TestTenantHandoffWebSourceConversationMutationInvalidatesEvidenceAndSourceHash(t *testing.T) {
	for index, key := range []string{"content", "response"} {
		t.Run(key, func(t *testing.T) {
			store := webTenantSourceStore()
			payload, _ := json.Marshal(map[string]string{key: strings.Repeat("a", MaxCandidateBytes) + "before"})
			store.events[index].PayloadJSON = string(payload)
			reader := NewTenantSourceReader(store)
			before, err := reader.Capture(context.Background(), handoffRequestContext(), tenantSourceRef())
			if err != nil {
				t.Fatal(err)
			}
			before.Target = Target{Ref: "tenant:target"}
			beforePackage, err := Extract(before)
			if err != nil {
				t.Fatal(err)
			}
			payload, _ = json.Marshal(map[string]string{key: strings.Repeat("a", MaxCandidateBytes) + "after"})
			store.events[index].PayloadJSON = string(payload)
			store.allEvents = append([]mysql.AgentTaskEvent(nil), store.events...)
			after, err := reader.Capture(context.Background(), handoffRequestContext(), tenantSourceRef())
			if err != nil {
				t.Fatal(err)
			}
			after.Target = before.Target
			afterPackage, err := Extract(after)
			if err != nil {
				t.Fatal(err)
			}
			if beforePackage.Source.Cursor != afterPackage.Source.Cursor || beforePackage.Source.ContentSHA256 == afterPackage.Source.ContentSHA256 {
				t.Fatalf("same-cursor content mutation must alter source hash: before=%+v after=%+v", beforePackage.Source, afterPackage.Source)
			}
			evidence := before.Evidence[index]
			if _, err := reader.Resolve(context.Background(), handoffRequestContext(), tenantSourceRef(), evidence.Ref, evidence.SHA256); ErrorCodeOf(err) != CodeInvalidHash {
				t.Fatalf("mutated conversation evidence accepted: %v", err)
			}
		})
	}
}

func TestTenantHandoffWebSourceResolverPreservesLegacyConversationHashes(t *testing.T) {
	// These fixtures use the pre-content canonical event formula with payload {}.
	for _, test := range []struct {
		index int
		field string
		hash  string
	}{
		{0, "content", "da0e16787779deabe2436ceb62bb25a48de4c6ffe24611c006655731dc33c683"},
		{1, "response", "1e05f535ab0da2c0dab8eb4fe56467ba44f82883c25cdf57603c9a5091bac2f3"},
	} {
		t.Run(test.field, func(t *testing.T) {
			store := webTenantSourceStore()
			store.allEvents = append([]mysql.AgentTaskEvent(nil), store.events...)
			reader := NewTenantSourceReader(store)
			locator := tenantEventLocator(tenantSourceRef(), store.events[test.index])
			resolved, err := reader.Resolve(context.Background(), handoffRequestContext(), tenantSourceRef(), locator, test.hash)
			if err != nil || resolved.SHA256 != test.hash || resolved.Verification != VerificationReported {
				t.Fatalf("legacy conversation evidence readback = %+v, %v", resolved, err)
			}
			// Legacy evidence retains its weaker body guarantee; new captures are
			// protected by the full-content mutation regression above.
			payload, _ := json.Marshal(map[string]string{test.field: "body changed after legacy capture"})
			store.allEvents[test.index].PayloadJSON = string(payload)
			if _, err := reader.Resolve(context.Background(), handoffRequestContext(), tenantSourceRef(), locator, test.hash); err != nil {
				t.Fatalf("legacy metadata-only guarantee changed: %v", err)
			}
			store.allEvents[test.index].PayloadJSON = `{"status":"changed metadata"}`
			if _, err := reader.Resolve(context.Background(), handoffRequestContext(), tenantSourceRef(), locator, test.hash); ErrorCodeOf(err) != CodeInvalidHash {
				t.Fatalf("legacy metadata change was accepted: %v", err)
			}
			store.allEvents[test.index].PayloadJSON = `{}`
			store.allEvents[test.index].EventType = agenttasks.EventToolResult
			if _, err := reader.Resolve(context.Background(), handoffRequestContext(), tenantSourceRef(), locator, test.hash); ErrorCodeOf(err) != CodeInvalidHash {
				t.Fatalf("legacy conversation hash accepted for verified tool event: %v", err)
			}
		})
	}
}

func TestTenantHandoffWebSourceExcludesForeignAndSubagentConversation(t *testing.T) {
	store := webTenantSourceStore()
	store.tasks = append(store.tasks,
		mysql.AgentTask{ID: 13, ParentSessionID: 99, AgentName: agenttasks.AgentNameWeb, SubagentSessionKey: "source"},
		mysql.AgentTask{ID: 14, ParentSessionID: 41, AgentName: "researcher", SubagentSessionKey: "child"},
		mysql.AgentTask{ID: 15, ParentSessionID: 41, AgentName: agenttasks.AgentNameWeb, SubagentSessionKey: "child"},
	)
	for _, taskID := range []uint64{13, 14, 15, 999} {
		store.events = append(store.events,
			mysql.AgentTaskEvent{ID: 100 + taskID, TaskID: taskID, EventType: agenttasks.EventMessage, PayloadJSON: `{"content":"foreign instruction"}`},
			mysql.AgentTaskEvent{ID: 200 + taskID, TaskID: taskID, EventType: agenttasks.EventCompleted, PayloadJSON: `{"response":"foreign answer"}`},
		)
	}
	snapshot, err := NewTenantSourceReader(store).Capture(context.Background(), handoffRequestContext(), tenantSourceRef())
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(snapshot)
	if strings.Contains(string(encoded), "foreign instruction") || strings.Contains(string(encoded), "foreign answer") {
		t.Fatal("foreign or subagent conversation copied into source snapshot")
	}
	snapshot.Target = Target{Ref: "tenant:target"}
	pkg, err := Extract(snapshot)
	if err != nil || pkg.Objective != "Remember the code ALPHA-SSE-OK." {
		t.Fatalf("main source objective lost: %q, %v", pkg.Objective, err)
	}
}

func TestTenantHandoffWebSourceSelectsLatestMessageAndRetainsTitleFallback(t *testing.T) {
	store := webTenantSourceStore()
	store.events = append([]mysql.AgentTaskEvent{{ID: 30, TaskID: 12, EventType: agenttasks.EventMessage, PayloadJSON: `{"content":"latest request"}`}}, store.events...)
	reader := NewTenantSourceReader(store)
	snapshot, err := reader.Capture(context.Background(), handoffRequestContext(), tenantSourceRef())
	if err != nil {
		t.Fatal(err)
	}
	if selectCandidate(snapshot.Objectives) != "latest request" {
		t.Fatalf("objective=%q, want latest user request", selectCandidate(snapshot.Objectives))
	}
	store.events = []mysql.AgentTaskEvent{{ID: 31, TaskID: 12, EventType: agenttasks.EventMessage, PayloadJSON: `{"content":""}`}}
	snapshot, err = reader.Capture(context.Background(), handoffRequestContext(), tenantSourceRef())
	if err != nil || selectCandidate(snapshot.Objectives) != store.session.Title {
		t.Fatalf("empty-message fallback=%q, %v", selectCandidate(snapshot.Objectives), err)
	}
}

func webTenantSourceStore() *tenantSourceStoreFake {
	return &tenantSourceStoreFake{
		resolved: tenant.Context{TenantID: 7, UserID: 11}, owner: handoffRequestContext(),
		session: mysql.Session{ID: 41, SessionKey: "source", Title: "Source chat"},
		tasks:   []mysql.AgentTask{{ID: 12, ParentSessionID: 41, SubagentSessionKey: "source", AgentName: agenttasks.AgentNameWeb, Description: "Source chat", Status: agenttasks.StatusCompleted}},
		events: []mysql.AgentTaskEvent{
			{ID: 17, TaskID: 12, EventType: agenttasks.EventMessage, PayloadJSON: `{"content":"Remember the code ALPHA-SSE-OK."}`},
			{ID: 18, TaskID: 12, EventType: agenttasks.EventCompleted, PayloadJSON: `{"response":"The code is ALPHA-SSE-OK."}`},
		},
	}
}

func TestTenantHandoffSourceCapturesBoundedStructuredSnapshot(t *testing.T) {
	store := &tenantSourceStoreFake{
		resolved: tenant.Context{TenantID: 7, UserID: 11},
		owner:    handoffRequestContext(),
		session:  mysql.Session{ID: 41, SessionKey: "source", Title: "Investigate handoff"},
		messages: []mysql.Message{{ID: 9, Role: "user", Content: "Find the current failure.", CreatedAt: time.Unix(9, 0)}},
		tasks:    []mysql.AgentTask{{ID: 12, ParentSessionID: 41, Description: "Repair the import path", Status: agenttasks.StatusRunning}},
		events: []mysql.AgentTaskEvent{{
			ID:          17,
			TaskID:      12,
			EventType:   agenttasks.EventToolResult,
			PayloadJSON: `{"tool_name":"go test","success":true,"summary":"target package passed","output":"raw secret tool output"}`,
		}},
	}
	reader := NewTenantSourceReader(store)

	snapshot, err := reader.Capture(context.Background(), handoffRequestContext(), tenantSourceRef())
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	if snapshot.Source.Cursor != "task_event:17" {
		t.Fatalf("cursor = %q, want task_event:17", snapshot.Source.Cursor)
	}
	if store.resolveCalls != 0 {
		t.Fatalf("legacy ResolveContext() calls = %d, want 0", store.resolveCalls)
	}
	if store.resolveOnceCalls != 1 || store.unboundReads != 0 {
		t.Fatalf("ResolveContextOnce/bound reads = %d/%d, want 1/0", store.resolveOnceCalls, store.unboundReads)
	}
	for method, scopes := range store.readScopes {
		if len(scopes) == 0 {
			t.Fatalf("%s did not receive a scoped read", method)
		}
		for _, scope := range scopes {
			if scope != handoffRequestContext() {
				t.Fatalf("%s scope = %+v, want %+v", method, scope, handoffRequestContext())
			}
		}
	}
	if len(snapshot.Evidence) != 2 {
		t.Fatalf("evidence = %#v, want task/message/event records", snapshot.Evidence)
	}
	foundToolTrace := false
	for _, item := range snapshot.Evidence {
		if item.Ref == "" || item.SHA256 == "" {
			t.Fatalf("unstable evidence item = %#v", item)
		}
		if item.Ref == "tenant:source#tool_trace:17:0" && item.Verification != VerificationVerified {
			t.Fatalf("tool evidence = %#v, want verified", item)
		}
		foundToolTrace = foundToolTrace || item.Ref == "tenant:source#tool_trace:17:0"
	}
	if !foundToolTrace {
		t.Fatalf("evidence = %#v, want opaque tool trace locator", snapshot.Evidence)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == "" || containsAny(string(encoded), "raw secret tool output", "messages", "tool_output") {
		t.Fatalf("snapshot leaked a raw source record: %s", encoded)
	}
	if _, found := reflect.TypeOf(SourceSnapshot{}).FieldByName("Messages"); found {
		t.Fatal("handoff snapshot must not expose a raw message list")
	}
	if _, found := reflect.TypeOf(SourceSnapshot{}).FieldByName("ToolOutput"); found {
		t.Fatal("handoff snapshot must not expose raw tool output")
	}
	assertSourceSnapshotExtractable(t, snapshot)
}

func TestTenantHandoffSourceResolverReauthorizesAndRevalidatesHash(t *testing.T) {
	store := &tenantSourceStoreFake{
		resolved:    tenant.Context{TenantID: 7, UserID: 11},
		owner:       handoffRequestContext(),
		session:     mysql.Session{ID: 41, SessionKey: "source"},
		messages:    []mysql.Message{{ID: 9, Role: "user", Content: "Inspect status."}},
		allMessages: []mysql.Message{{ID: 9, Role: "user", Content: "Inspect status."}},
	}
	reader := NewTenantSourceReader(store)
	snapshot, err := reader.Capture(context.Background(), handoffRequestContext(), tenantSourceRef())
	if err != nil {
		t.Fatal(err)
	}
	evidence := snapshot.Evidence[0]
	store.messages = nil
	store.resetReadCalls()

	resolved, err := reader.Resolve(context.Background(), handoffRequestContext(), tenantSourceRef(), evidence.Ref, evidence.SHA256)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.Ref != evidence.Ref || resolved.SHA256 != evidence.SHA256 || containsAny(resolved.Claim, "Inspect status.") {
		t.Fatalf("resolved = %#v", resolved)
	}
	if store.listCalls != 0 || store.exactMessageCalls != 1 {
		t.Fatalf("resolver used list/window reads: list=%d exact_message=%d", store.listCalls, store.exactMessageCalls)
	}
	if _, err := reader.Resolve(context.Background(), handoffRequestContext(), tenantSourceRef(), evidence.Ref, "bad"); ErrorCodeOf(err) != CodeInvalidHash {
		t.Fatalf("Resolve(bad hash) error = %v", err)
	}

	store.allMessages[0] = mysql.Message{ID: 9, Role: "user", Content: "Changed after capture."}
	if _, err := reader.Resolve(context.Background(), handoffRequestContext(), tenantSourceRef(), evidence.Ref, evidence.SHA256); ErrorCodeOf(err) != CodeInvalidHash {
		t.Fatalf("Resolve(changed record) error = %v", err)
	}
}

func TestTenantHandoffSourceRejectsForeignAndMissingRecords(t *testing.T) {
	foreign := &tenantSourceStoreFake{resolved: tenant.Context{TenantID: 8, UserID: 11}}
	if _, err := NewTenantSourceReader(foreign).Capture(context.Background(), handoffRequestContext(), tenantSourceRef()); !serviceErrorHasCode(err, sessioncontrol.CodeForbidden) {
		t.Fatalf("foreign capture error = %v", err)
	}

	missing := &tenantSourceStoreFake{resolved: tenant.Context{TenantID: 7, UserID: 11}, getSessionErr: mysql.ErrNotFound}
	if _, err := NewTenantSourceReader(missing).Capture(context.Background(), handoffRequestContext(), tenantSourceRef()); !serviceErrorHasCode(err, sessioncontrol.CodeNotFound) {
		t.Fatalf("missing capture error = %v", err)
	}
}

func TestTenantHandoffSourceResolverRejectsCrossUserExactRecord(t *testing.T) {
	store := &tenantSourceStoreFake{
		resolved:    tenant.Context{TenantID: 7, UserID: 11},
		owner:       handoffRequestContext(),
		session:     mysql.Session{ID: 41, SessionKey: "source"},
		allMessages: []mysql.Message{{ID: 99, SessionID: 41, Role: "user", Content: "belongs to user 12"}},
		recordOwners: map[string]sessioncontrol.RequestContext{
			"message:99": {TenantID: 7, UserID: 12, ActorUserID: 12},
		},
	}
	_, err := NewTenantSourceReader(store).Resolve(context.Background(), handoffRequestContext(), tenantSourceRef(), "tenant:source#message:99", strings.Repeat("a", 64))
	if !serviceErrorHasCode(err, sessioncontrol.CodeNotFound) {
		t.Fatalf("cross-user exact lookup error = %v", err)
	}
}

func TestTenantHandoffSourceResolverReadsOldTaskEventAndToolTraceExactly(t *testing.T) {
	store := &tenantSourceStoreFake{
		resolved: tenant.Context{TenantID: 7, UserID: 11},
		owner:    handoffRequestContext(),
		session:  mysql.Session{ID: 41, SessionKey: "source"},
		tasks:    []mysql.AgentTask{{ID: 12, ParentSessionID: 41, Status: agenttasks.StatusCompleted}},
		events: []mysql.AgentTaskEvent{
			{ID: 16, TaskID: 12, EventType: agenttasks.EventFileChange, PayloadJSON: `{"path":"internal/a.go","status":"written"}`},
			{ID: 17, TaskID: 12, EventType: agenttasks.EventToolResult, PayloadJSON: `{"tool_name":"go test","success":true}`},
		},
		allEvents: []mysql.AgentTaskEvent{
			{ID: 16, TaskID: 12, EventType: agenttasks.EventFileChange, PayloadJSON: `{"path":"internal/a.go","status":"written"}`},
			{ID: 17, TaskID: 12, EventType: agenttasks.EventToolResult, PayloadJSON: `{"tool_name":"go test","success":true}`},
		},
	}
	reader := NewTenantSourceReader(store)
	snapshot, err := reader.Capture(context.Background(), handoffRequestContext(), tenantSourceRef())
	if err != nil {
		t.Fatal(err)
	}
	store.events = nil
	store.resetReadCalls()
	for _, evidence := range snapshot.Evidence {
		resolved, resolveErr := reader.Resolve(context.Background(), handoffRequestContext(), tenantSourceRef(), evidence.Ref, evidence.SHA256)
		if resolveErr != nil || resolved.Ref != evidence.Ref {
			t.Fatalf("Resolve(%q) = %+v, %v", evidence.Ref, resolved, resolveErr)
		}
	}
	if store.listCalls != 0 || store.exactEventCalls != 1 || store.exactToolCalls != 1 {
		t.Fatalf("exact resolver calls: lists=%d event=%d tool=%d", store.listCalls, store.exactEventCalls, store.exactToolCalls)
	}
}

func handoffRequestContext() sessioncontrol.RequestContext {
	return sessioncontrol.RequestContext{TenantID: 7, UserID: 11, ActorUserID: 11, TraceID: "trace-handoff"}
}

func tenantSourceRef() sessioncontrol.SessionRef {
	return sessioncontrol.SessionRef{Source: sessioncontrol.SourceTenant, Key: "source"}
}

func serviceErrorHasCode(err error, want sessioncontrol.ServiceErrorCode) bool {
	var serviceErr *sessioncontrol.ServiceError
	return errors.As(err, &serviceErr) && serviceErr.Code == want
}

func assertSourceSnapshotExtractable(t *testing.T, snapshot SourceSnapshot) {
	t.Helper()
	snapshot.Target = Target{Ref: "tenant:target"}
	if _, err := Extract(snapshot); err != nil {
		t.Fatalf("captured snapshot is not a valid package input: %v", err)
	}
}

func containsAny(value string, forbidden ...string) bool {
	for _, item := range forbidden {
		if len(item) > 0 && strings.Contains(value, item) {
			return true
		}
	}
	return false
}

type tenantSourceStoreFake struct {
	resolved          tenant.Context
	owner             sessioncontrol.RequestContext
	session           mysql.Session
	messages          []mysql.Message
	tasks             []mysql.AgentTask
	events            []mysql.AgentTaskEvent
	getSessionErr     error
	resolveCalls      int
	resolveOnceCalls  int
	unboundReads      int
	readScopes        map[string][]sessioncontrol.RequestContext
	listCalls         int
	exactMessageCalls int
	exactEventCalls   int
	exactToolCalls    int
	allMessages       []mysql.Message
	allEvents         []mysql.AgentTaskEvent
	recordOwners      map[string]sessioncontrol.RequestContext
}

func (f *tenantSourceStoreFake) ResolveContext(context.Context) (tenant.Context, error) {
	f.resolveCalls++
	return f.resolved, nil
}

type tenantSourceBoundContextKey struct{}

func (f *tenantSourceStoreFake) ResolveContextOnce(ctx context.Context) (context.Context, tenant.Context, error) {
	f.resolveOnceCalls++
	return context.WithValue(ctx, tenantSourceBoundContextKey{}, true), f.resolved, nil
}

func (f *tenantSourceStoreFake) GetSessionByKey(ctx context.Context, scope sessioncontrol.RequestContext, _ string) (mysql.Session, error) {
	f.observeBound(ctx)
	f.recordScope("session", scope)
	if f.getSessionErr != nil {
		return mysql.Session{}, f.getSessionErr
	}
	return f.session, nil
}
func (f *tenantSourceStoreFake) ListRecentMessages(ctx context.Context, scope sessioncontrol.RequestContext, _ uint64, _ int) ([]mysql.Message, error) {
	f.observeBound(ctx)
	f.recordScope("messages", scope)
	f.listCalls++
	if err := f.authorize(scope); err != nil {
		return nil, err
	}
	return append([]mysql.Message(nil), f.messages...), nil
}
func (f *tenantSourceStoreFake) ListLatestAgentTasksForSessions(ctx context.Context, scope sessioncontrol.RequestContext, _ []uint64) ([]mysql.AgentTask, error) {
	f.observeBound(ctx)
	f.recordScope("tasks", scope)
	f.listCalls++
	if err := f.authorize(scope); err != nil {
		return nil, err
	}
	return append([]mysql.AgentTask(nil), f.tasks...), nil
}
func (f *tenantSourceStoreFake) ListAgentTaskEventsForTasksComplete(ctx context.Context, scope sessioncontrol.RequestContext, _ []uint64) ([]mysql.AgentTaskEvent, error) {
	f.observeBound(ctx)
	f.recordScope("events", scope)
	f.listCalls++
	if err := f.authorize(scope); err != nil {
		return nil, err
	}
	return append([]mysql.AgentTaskEvent(nil), f.events...), nil
}

func (f *tenantSourceStoreFake) GetMessage(ctx context.Context, scope sessioncontrol.RequestContext, sessionID, messageID uint64) (mysql.Message, error) {
	f.observeBound(ctx)
	f.recordScope("message_exact", scope)
	f.exactMessageCalls++
	if err := f.authorizeRecord(scope, "message:"+strconv.FormatUint(messageID, 10)); err != nil {
		return mysql.Message{}, err
	}
	for _, message := range f.allMessages {
		if message.ID == messageID && (message.SessionID == 0 || message.SessionID == sessionID) {
			return message, nil
		}
	}
	return mysql.Message{}, mysql.ErrNotFound
}

func (f *tenantSourceStoreFake) GetTaskEvent(ctx context.Context, scope sessioncontrol.RequestContext, _ uint64, eventID uint64) (mysql.AgentTaskEvent, error) {
	f.observeBound(ctx)
	f.recordScope("event_exact", scope)
	f.exactEventCalls++
	if err := f.authorizeRecord(scope, "task_event:"+strconv.FormatUint(eventID, 10)); err != nil {
		return mysql.AgentTaskEvent{}, err
	}
	for _, event := range f.allEvents {
		if event.ID == eventID {
			return event, nil
		}
	}
	return mysql.AgentTaskEvent{}, mysql.ErrNotFound
}

func (f *tenantSourceStoreFake) GetToolTraceRecord(ctx context.Context, scope sessioncontrol.RequestContext, _ uint64, eventID uint64, ordinal int) (mysql.AgentTaskEvent, error) {
	f.observeBound(ctx)
	f.recordScope("tool_exact", scope)
	f.exactToolCalls++
	if ordinal != 0 {
		return mysql.AgentTaskEvent{}, mysql.ErrNotFound
	}
	if err := f.authorizeRecord(scope, "tool_trace:"+strconv.FormatUint(eventID, 10)+":0"); err != nil {
		return mysql.AgentTaskEvent{}, err
	}
	for _, event := range f.allEvents {
		if event.ID == eventID && event.EventType == agenttasks.EventToolResult {
			return event, nil
		}
	}
	return mysql.AgentTaskEvent{}, mysql.ErrNotFound
}

func (f *tenantSourceStoreFake) authorize(scope sessioncontrol.RequestContext) error {
	if f.owner.TenantID != 0 && (scope.TenantID != f.owner.TenantID || scope.UserID != f.owner.UserID) {
		return mysql.ErrNotFound
	}
	return nil
}

func (f *tenantSourceStoreFake) authorizeRecord(scope sessioncontrol.RequestContext, key string) error {
	if owner, ok := f.recordOwners[key]; ok && (scope.TenantID != owner.TenantID || scope.UserID != owner.UserID) {
		return mysql.ErrNotFound
	}
	return f.authorize(scope)
}

func (f *tenantSourceStoreFake) recordScope(method string, scope sessioncontrol.RequestContext) {
	if f.readScopes == nil {
		f.readScopes = make(map[string][]sessioncontrol.RequestContext)
	}
	f.readScopes[method] = append(f.readScopes[method], scope)
}

func (f *tenantSourceStoreFake) observeBound(ctx context.Context) {
	if bound, _ := ctx.Value(tenantSourceBoundContextKey{}).(bool); !bound {
		f.unboundReads++
	}
}

func (f *tenantSourceStoreFake) resetReadCalls() {
	f.listCalls = 0
	f.exactMessageCalls = 0
	f.exactEventCalls = 0
	f.exactToolCalls = 0
}
