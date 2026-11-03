package sessioncontrol

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	control "github.com/konglong87/super-niuma/internal/sessioncontrol"
	"github.com/konglong87/super-niuma/internal/tools"
)

func TestToolsExposeExactlySevenIndependentSessionSurfaces(t *testing.T) {
	items := New(fakeService{})
	want := []string{"SessionCreate", "SessionList", "SessionGet", "SessionSend", "SessionStop", "SessionAttach", "SessionMonitor"}
	if len(items) != len(want) {
		t.Fatalf("tool count = %d, want %d", len(items), len(want))
	}
	for i, item := range items {
		if item.Name() != want[i] {
			t.Fatalf("tool %d = %q, want %q", i, item.Name(), want[i])
		}
		var schema map[string]any
		if err := json.Unmarshal(item.InputSchema(), &schema); err != nil || schema["additionalProperties"] != false {
			t.Fatalf("%s schema must reject unknown fields: %s", item.Name(), item.InputSchema())
		}
		if strings.Contains(string(item.InputSchema()), "tenant_id") || strings.Contains(string(item.InputSchema()), "user_id") {
			t.Fatalf("%s schema accepts caller identity: %s", item.Name(), item.InputSchema())
		}
	}
}

func TestSessionSendDerivesTrustedIdentityAndRejectsBareOrLocalRefs(t *testing.T) {
	svc := &recordingService{}
	tool := New(svc)[3]
	ctx := tools.Context{TenantID: 7, UserID: 11, TraceID: "trace-1"}
	for _, input := range []string{
		`{"ref":"bare","content":"hello","idempotency_key":"send-1"}`,
		`{"ref":"local:local-1","content":"hello","idempotency_key":"send-1"}`,
		`{"ref":"tenant:tenant-1","content":"hello"}`,
		`{"ref":"tenant:tenant-1","content":"hello","idempotency_key":"send-1","tenant_id":99}`,
	} {
		result := tool.Run(context.Background(), json.RawMessage(input), ctx)
		if !result.IsError {
			t.Fatalf("Run(%s) unexpectedly succeeded: %+v", input, result)
		}
	}

	result := tool.Run(context.Background(), json.RawMessage(`{"ref":"tenant:tenant-1","content":"hello","idempotency_key":"send-1"}`), ctx)
	if result.IsError {
		t.Fatalf("valid send failed: %s", result.Content)
	}
	if svc.send.Context != (control.RequestContext{TenantID: 7, UserID: 11, ActorUserID: 11, TraceID: "trace-1"}) {
		t.Fatalf("trusted context = %+v", svc.send.Context)
	}
	if svc.send.Ref.String() != "tenant:tenant-1" || svc.send.IdempotencyKey != "send-1" {
		t.Fatalf("send request = %+v", svc.send)
	}
}

func TestSessionGetReturnsSnapshotWithoutTranscriptInput(t *testing.T) {
	tool := New(fakeService{})[2]
	result := tool.Run(context.Background(), json.RawMessage(`{"ref":"tenant:tenant-1","include_transcript":true}`), tools.Context{TenantID: 7, UserID: 11})
	if !result.IsError {
		t.Fatal("SessionGet accepted a transcript request")
	}
}

func TestSessionCreateForwardsInitialRunFields(t *testing.T) {
	svc := &recordingService{}
	tool := New(svc)[0]
	result := tool.Run(context.Background(), json.RawMessage(`{"session_key":"tenant-1","title":"Plan","model":"model-1","initial_text":"Start now","idempotency_key":"create-1"}`), tools.Context{CWD: "/workspace/project", TenantID: 7, UserID: 11})
	if result.IsError {
		t.Fatalf("valid create failed: %s", result.Content)
	}
	if svc.create.CWD != "/workspace/project" || svc.create.InitialText != "Start now" {
		t.Fatalf("create request = %+v", svc.create)
	}
	result = tool.Run(context.Background(), json.RawMessage(`{"session_key":"tenant-2","cwd":"/outside","idempotency_key":"create-2"}`), tools.Context{CWD: "/workspace/project", TenantID: 7, UserID: 11})
	if !result.IsError {
		t.Fatal("SessionCreate accepted a model-controlled cwd")
	}
}

func TestSessionMonitorSchemaMatchesRuntimeBounds(t *testing.T) {
	tool := New(fakeService{})[6]
	var schema struct {
		Required   []string `json:"required"`
		Properties map[string]struct {
			Minimum  int `json:"minimum"`
			Maximum  int `json:"maximum"`
			MinItems int `json:"minItems"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(tool.InputSchema(), &schema); err != nil {
		t.Fatal(err)
	}
	if !contains(schema.Required, "sources") || schema.Properties["sources"].MinItems != 1 {
		t.Fatalf("sources contract = required:%v schema:%+v", schema.Required, schema.Properties["sources"])
	}
	interval := schema.Properties["interval_seconds"]
	if interval.Minimum != control.SessionMonitorMinIntervalSeconds || interval.Maximum != control.SessionMonitorMaxIntervalSeconds {
		t.Fatalf("interval contract = %+v", interval)
	}
}

func contains(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

type fakeService struct{}

func (fakeService) Create(context.Context, control.CreateRequest) (control.OperationResult, error) {
	return control.OperationResult{}, nil
}
func (fakeService) List(context.Context, control.ListRequest) ([]control.SessionSnapshot, error) {
	return nil, nil
}
func (fakeService) Get(context.Context, control.GetRequest) (control.SessionSnapshot, error) {
	return control.SessionSnapshot{}, nil
}
func (fakeService) Send(context.Context, control.SendRequest) (control.OperationResult, error) {
	return control.OperationResult{}, nil
}
func (fakeService) Stop(context.Context, control.StopRequest) (control.OperationResult, error) {
	return control.OperationResult{}, nil
}
func (fakeService) Attach(context.Context, control.AttachRequest) (control.OperationResult, error) {
	return control.OperationResult{}, nil
}
func (fakeService) Monitor(context.Context, control.MonitorRequest) (control.OperationResult, error) {
	return control.OperationResult{}, nil
}

type recordingService struct {
	fakeService
	create control.CreateRequest
	send   control.SendRequest
}

func (s *recordingService) Create(_ context.Context, request control.CreateRequest) (control.OperationResult, error) {
	s.create = request
	return control.OperationResult{Session: control.SessionSnapshot{Ref: control.SessionRef{Source: control.SourceTenant, Key: request.SessionKey}}}, nil
}

func (s *recordingService) Send(_ context.Context, request control.SendRequest) (control.OperationResult, error) {
	s.send = request
	return control.OperationResult{Session: control.SessionSnapshot{Ref: request.Ref}}, nil
}
