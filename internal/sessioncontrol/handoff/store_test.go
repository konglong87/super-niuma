package handoff

import (
	"context"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/sessioncontrol"
	"github.com/konglong87/super-niuma/internal/storage/mysql"
	"github.com/konglong87/super-niuma/internal/tenant"
)

func TestSessionHandoffEventPayloadRoundTripRejectsTamperedPackage(t *testing.T) {
	pkg, err := BuildPackage(PackageInput{
		Source:    Source{Ref: "tenant:source", Cursor: "task_event:11"},
		Target:    Target{Ref: "tenant:target"},
		Objective: "Continue the repair",
		Budget:    Budget{LimitTokens: DefaultPackageTokenLimit},
	})
	if err != nil {
		t.Fatal(err)
	}

	payload, err := EncodeSessionHandoffEvent(pkg, 42)
	if err != nil {
		t.Fatalf("EncodeSessionHandoffEvent() error = %v", err)
	}
	decoded, err := DecodeSessionHandoffEvent(payload)
	if err != nil {
		t.Fatalf("DecodeSessionHandoffEvent() error = %v", err)
	}
	if decoded.Package.PackageSHA256 != pkg.PackageSHA256 || decoded.TargetTaskID != 42 {
		t.Fatalf("decoded payload = %#v", decoded)
	}

	tampered := strings.Replace(payload, "Continue the repair", "tampered", 1)
	if _, err := DecodeSessionHandoffEvent(tampered); ErrorCodeOf(err) != CodeInvalidHash {
		t.Fatalf("DecodeSessionHandoffEvent(tampered) error = %v, want invalid hash", err)
	}
	if _, err := DecodeSessionHandoffEvent(strings.Replace(payload, `"schema":"niuma.session-handoff-event.v1"`, `"schema":"unknown"`, 1)); ErrorCodeOf(err) != CodeUnsupportedSchema {
		t.Fatalf("DecodeSessionHandoffEvent(unknown schema) error = %v", err)
	}
	if _, err := DecodeSessionHandoffEvent(strings.TrimSuffix(payload, "}") + `,"untyped":true}`); ErrorCodeOf(err) != CodeInvalidPackage {
		t.Fatalf("DecodeSessionHandoffEvent(untyped) error = %v", err)
	}
}

func TestHandoffStoreRejectsForeignScopeBeforePersistence(t *testing.T) {
	pkg, err := BuildPackage(PackageInput{Source: Source{Ref: "tenant:source", Cursor: "task_event:11"}, Target: Target{Ref: "tenant:target"}, Objective: "Continue", Budget: Budget{LimitTokens: DefaultPackageTokenLimit}})
	if err != nil {
		t.Fatal(err)
	}
	store := &handoffStoreFake{identity: tenant.Context{TenantID: 8, UserID: 11}}
	_, err = NewStore(store).Persist(context.Background(), PersistRequest{Context: sessioncontrol.RequestContext{TenantID: 7, UserID: 11, ActorUserID: 11}, TargetSessionID: 41, TargetTaskID: 42, Package: pkg})
	if !serviceErrorHasCode(err, sessioncontrol.CodeForbidden) || store.calls != 0 {
		t.Fatalf("Persist() error = %v, calls = %d", err, store.calls)
	}
}

func TestHandoffStoreMapsTargetTaskStateRace(t *testing.T) {
	pkg, err := BuildPackage(PackageInput{Source: Source{Ref: "tenant:source", Cursor: "task_event:11"}, Target: Target{Ref: "tenant:target"}, Objective: "Continue", Budget: Budget{LimitTokens: DefaultPackageTokenLimit}})
	if err != nil {
		t.Fatal(err)
	}
	store := &handoffStoreFake{persistErr: mysql.ErrInvalidState}
	_, err = NewStore(store).Persist(context.Background(), PersistRequest{Context: sessioncontrol.RequestContext{TenantID: 7, UserID: 11, ActorUserID: 11}, TargetSessionID: 41, TargetTaskID: 42, Package: pkg})
	if !serviceErrorHasCode(err, sessioncontrol.CodeInvalidState) {
		t.Fatalf("Persist() error = %v, want invalid_state", err)
	}
}

type handoffStoreFake struct {
	identity   tenant.Context
	calls      int
	input      agenttasks.HandoffLinkAndEventInput
	persistErr error
}

func (f *handoffStoreFake) ResolveContext(context.Context) (tenant.Context, error) {
	if f.identity.TenantID == 0 {
		return tenant.Context{TenantID: 7, UserID: 11}, nil
	}
	return f.identity, nil
}

func (f *handoffStoreFake) CreateHandoffLinkAndEvent(_ context.Context, input agenttasks.HandoffLinkAndEventInput) (agenttasks.HandoffLinkAndEventResult, error) {
	f.calls++
	f.input = input
	if f.persistErr != nil {
		return agenttasks.HandoffLinkAndEventResult{}, f.persistErr
	}
	return agenttasks.HandoffLinkAndEventResult{LinkID: 101, EventID: 202}, nil
}

func TestHandoffStorePersistsWithOneAtomicOperation(t *testing.T) {
	pkg, err := BuildPackage(PackageInput{
		Source:    Source{Ref: "tenant:source", Cursor: "task_event:11"},
		Target:    Target{Ref: "tenant:target"},
		Objective: "Continue the repair",
		Budget:    Budget{LimitTokens: DefaultPackageTokenLimit},
	})
	if err != nil {
		t.Fatal(err)
	}
	store := &handoffStoreFake{}
	persistence := NewStore(store)

	result, err := persistence.Persist(context.Background(), PersistRequest{
		Context:         sessioncontrol.RequestContext{TenantID: 7, UserID: 11, ActorUserID: 11},
		TargetSessionID: 41,
		TargetTaskID:    42,
		Package:         pkg,
	})
	if err != nil {
		t.Fatalf("Persist() error = %v", err)
	}
	if result.LinkID != 101 || result.EventID != 202 || store.calls != 1 {
		t.Fatalf("result = %#v, atomic calls = %d", result, store.calls)
	}
}
