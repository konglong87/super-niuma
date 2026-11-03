package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	control "github.com/konglong87/super-niuma/internal/sessioncontrol"
)

func TestSessionManagedCommandsRequireTrustedRuntimeAndNamespacedTenantRefs(t *testing.T) {
	var out bytes.Buffer
	if err := sessionCommandWithContext(context.Background(), []string{"send", "tenant:demo", "hello"}, &out); err == nil || !strings.Contains(err.Error(), "managed session control is unavailable") {
		t.Fatalf("unconfigured managed command error = %v", err)
	}

	old := newSessionControlRuntime
	t.Cleanup(func() { newSessionControlRuntime = old })
	recorded := &cliSessionControlFake{}
	newSessionControlRuntime = func(context.Context) (SessionControlRuntime, error) {
		return SessionControlRuntime{Service: recorded, Context: control.RequestContext{TenantID: 7, UserID: 11, ActorUserID: 11}, CWD: "/workspace/project"}, nil
	}

	for _, args := range [][]string{{"send", "bare", "hello"}, {"send", "local:local-1", "hello"}, {"stop", "local:local-1"}} {
		out.Reset()
		if err := sessionCommandWithContext(context.Background(), args, &out); err == nil {
			t.Fatalf("%v unexpectedly succeeded", args)
		}
	}

	out.Reset()
	if err := sessionCommandWithContext(context.Background(), []string{"send", "tenant:demo", "hello", "--idempotency-key", "send-1"}, &out); err != nil {
		t.Fatalf("managed send: %v", err)
	}
	if recorded.send.Context.TenantID != 7 || recorded.send.Context.UserID != 11 || recorded.send.Ref.String() != "tenant:demo" || recorded.send.IdempotencyKey != "send-1" {
		t.Fatalf("send request = %+v", recorded.send)
	}

	out.Reset()
	if err := sessionCommandWithContext(context.Background(), []string{"create", "--key", "created", "--initial-text", "Start now", "--idempotency-key", "create-1"}, &out); err != nil {
		t.Fatalf("managed create: %v", err)
	}
	if recorded.create.CWD != "/workspace/project" || recorded.create.InitialText != "Start now" {
		t.Fatalf("create request = %+v", recorded.create)
	}
}

func TestSessionListDefaultRemainsLocalWithoutManagedRuntime(t *testing.T) {
	old := newSessionControlRuntime
	t.Cleanup(func() { newSessionControlRuntime = old })
	newSessionControlRuntime = func(context.Context) (SessionControlRuntime, error) { return SessionControlRuntime{}, context.Canceled }
	var out bytes.Buffer
	if err := sessionCommandWithContext(context.Background(), []string{"list"}, &out); err != nil {
		t.Fatalf("legacy local list failed: %v", err)
	}
	if err := sessionCommandWithContext(context.Background(), []string{"list", "--source", "tenant"}, &out); err == nil {
		t.Fatal("tenant list unexpectedly ignored unavailable runtime")
	}
}

type cliSessionControlFake struct {
	create control.CreateRequest
	send   control.SendRequest
}

func (f *cliSessionControlFake) Create(_ context.Context, request control.CreateRequest) (control.OperationResult, error) {
	f.create = request
	return control.OperationResult{}, nil
}
func (f *cliSessionControlFake) List(context.Context, control.ListRequest) ([]control.SessionSnapshot, error) {
	return nil, nil
}
func (f *cliSessionControlFake) Get(context.Context, control.GetRequest) (control.SessionSnapshot, error) {
	return control.SessionSnapshot{}, nil
}
func (f *cliSessionControlFake) Send(_ context.Context, request control.SendRequest) (control.OperationResult, error) {
	f.send = request
	return control.OperationResult{Session: control.SessionSnapshot{Ref: request.Ref}}, nil
}
func (f *cliSessionControlFake) Stop(context.Context, control.StopRequest) (control.OperationResult, error) {
	return control.OperationResult{}, nil
}
func (f *cliSessionControlFake) Attach(context.Context, control.AttachRequest) (control.OperationResult, error) {
	return control.OperationResult{}, nil
}
func (f *cliSessionControlFake) Monitor(context.Context, control.MonitorRequest) (control.OperationResult, error) {
	return control.OperationResult{}, nil
}
