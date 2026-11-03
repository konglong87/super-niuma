package sessioncontrol

import (
	"context"
	"github.com/konglong87/super-niuma/internal/pendinginput"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	"testing"
)

func TestRuntimeConfigValidationRejectsUnknownOptions(t *testing.T) {
	for _, value := range []RuntimeConfig{{PermissionMode: "yes"}, {Effort: "super"}, {Effort: "-1"}, {PromptMode: "custom"}} {
		if _, err := NormalizeRuntimeConfig(value); err == nil {
			t.Fatalf("accepted %+v", value)
		}
	}
}

func TestRuntimeRecoverConfiguredQueueUsesAuditFingerprint(t *testing.T) {
	ctx := context.Background()
	request := SendRequest{Context: managedRequestContext(), Ref: tenantRef("alpha"), Content: "next", Model: "model-a", Provider: "provider-a", PermissionMode: "ask", Effort: "high", PromptMode: "code", IdempotencyKey: "queue-key"}
	identity, err := newOperationIdentity(OperationSend, request.Context, request.IdempotencyKey, sendRequestFingerprint(request))
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := mergeOperationMetadataJSON(`{}`, identity)
	if err != nil {
		t.Fatal(err)
	}
	store := &runtimeStoreFake{session: mysqlstore.SessionControlSession{Session: mysqlstore.Session{ID: 41, SessionKey: "alpha"}}, auditByKey: mysqlstore.AuditLog{ID: 1, MetadataJSON: metadata}}
	queue := pendinginput.NewMemoryQueue()
	if _, err := queue.Add(ctx, pendinginput.NewInput{Scope: pendinginput.Scope{TenantID: request.Context.TenantID, UserID: request.Context.UserID, SessionID: "41", BaseTaskID: 51}, ClientInputID: identity.KeyHash, Content: request.Content}); err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(RuntimeDependencies{Store: store, PendingInputs: queue})
	if recovered, err := runtime.RecoverSend(ctx, request, identity); err != nil || !recovered.Found || recovered.Result.RunID != 51 {
		t.Fatalf("recovered=%+v err=%v", recovered, err)
	}
	changed := request
	changed.Effort = "low"
	changedIdentity, _ := newOperationIdentity(OperationSend, changed.Context, changed.IdempotencyKey, sendRequestFingerprint(changed))
	if _, err := runtime.RecoverSend(ctx, changed, changedIdentity); err == nil {
		t.Fatal("changed configuration reused queued key")
	}
	store.auditByKey = mysqlstore.AuditLog{}
	if _, err := runtime.RecoverSend(ctx, request, identity); err == nil {
		t.Fatal("unaudited configuration replay was guessed")
	}
	items, err := queue.List(ctx, pendinginput.Scope{TenantID: request.Context.TenantID, UserID: request.Context.UserID, SessionID: "41"})
	if err != nil || len(items) != 1 {
		t.Fatalf("queue was changed: %+v %v", items, err)
	}
}

func TestRuntimeConfigParticipatesInCreateAndSendFingerprint(t *testing.T) {
	values := []RuntimeConfig{{}, {Model: "changed"}, {Provider: "changed"}, {PermissionMode: "deny"}, {Effort: "low"}, {PromptMode: "chat"}}
	for _, operation := range []Operation{OperationCreate, OperationSend} {
		seen := map[string]bool{}
		for _, value := range values {
			fixture := newServiceFixture()
			var err error
			if operation == OperationCreate {
				_, err = fixture.service.Create(context.Background(), CreateRequest{Context: testRequestContext(), SessionKey: "target", Model: value.Model, Provider: value.Provider, PermissionMode: value.PermissionMode, Effort: value.Effort, PromptMode: value.PromptMode, IdempotencyKey: "same-key"})
			} else {
				_, err = fixture.service.Send(context.Background(), SendRequest{Context: testRequestContext(), Ref: tenantRef("target"), Content: "same", Model: value.Model, Provider: value.Provider, PermissionMode: value.PermissionMode, Effort: value.Effort, PromptMode: value.PromptMode, IdempotencyKey: "same-key"})
			}
			if err != nil {
				t.Fatal(err)
			}
			fingerprint := fixture.recovery.got[operation].Fingerprint
			if fingerprint == "" || seen[fingerprint] {
				t.Fatalf("%s config %+v did not change fingerprint", operation, value)
			}
			seen[fingerprint] = true
		}
	}
}
