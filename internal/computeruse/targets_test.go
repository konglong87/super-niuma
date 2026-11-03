package computeruse

import (
	"context"
	"testing"
)

func TestStaticTargetRegistryResolvesTrustedMetadata(t *testing.T) {
	registry, err := NewStaticTargetRegistry(ApplicationTarget{
		ID:          "calculator",
		DisplayName: "Calculator",
		Launch:      LaunchPolicy{ProviderKey: "com.apple.calculator"},
		Window:      WindowPolicy{BundleID: "com.apple.calculator", RequireVisible: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	target, err := registry.Resolve("calculator")
	if err != nil {
		t.Fatal(err)
	}
	if target.Launch.ProviderKey != "com.apple.calculator" || target.Window.BundleID != "com.apple.calculator" {
		t.Fatalf("resolved target=%+v", target)
	}
	if _, err := registry.Resolve("com.apple.safari"); err == nil {
		t.Fatal("unregistered target resolved")
	}
}

func TestStaticTargetRegistryRejectsInvalidOrDuplicateTargets(t *testing.T) {
	if _, err := NewStaticTargetRegistry(ApplicationTarget{ID: "calculator"}); err == nil {
		t.Fatal("invalid target accepted")
	}
	registry, err := NewStaticTargetRegistry(ApplicationTarget{
		ID: "editor", DisplayName: "Editor", Launch: LaunchPolicy{ProviderKey: "com.example.editor"}, Window: WindowPolicy{BundleID: "com.example.editor"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(ApplicationTarget{
		ID: "editor", DisplayName: "Editor 2", Launch: LaunchPolicy{ProviderKey: "com.example.editor2"}, Window: WindowPolicy{BundleID: "com.example.editor2"},
	}); err == nil {
		t.Fatal("duplicate target accepted")
	}
}

// Embed unused Backend operations: this contract only launches, before any
// observation exists, so its identity cannot be borrowed from capture state.
type sessionLaunchBackend struct {
	Backend
	sessionID   string
	providerKey string
}

func (b *sessionLaunchBackend) LaunchApp(_ context.Context, sessionID, providerKey string) (LaunchReceipt, error) {
	b.sessionID, b.providerKey = sessionID, providerKey
	return LaunchReceipt{Outcome: OutcomeExecuted, BundleID: providerKey,
		Window: WindowRef{ID: "fixture-window", OwnerPID: 42, BundleID: providerKey, IsVisible: true}}, nil
}

func TestControllerColdLaunchCarriesAuthorizedSession(t *testing.T) {
	const targetID TargetID = "fixture"
	const providerKey = "com.example.fixture"
	session, owner, _ := newTestSession(t, false)
	registry, err := NewStaticTargetRegistry(ApplicationTarget{
		ID: targetID, DisplayName: "Fixture", Launch: LaunchPolicy{ProviderKey: providerKey},
		Window: WindowPolicy{BundleID: providerKey, RequireVisible: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	backend := &sessionLaunchBackend{}
	controller, err := NewControllerWithRegistry(session, backend, registry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.LaunchTarget(context.Background(), owner, "forged-session", targetID); err == nil {
		t.Fatal("forged session reached launcher")
	}
	if backend.sessionID != "" {
		t.Fatal("unauthorized launch reached backend")
	}
	receipt, err := controller.LaunchTarget(context.Background(), owner, session.ID(), targetID)
	if err != nil || receipt.Outcome != OutcomeExecuted {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	if backend.sessionID != session.ID() || backend.providerKey != providerKey {
		t.Fatalf("launch binding session=%q key=%q", backend.sessionID, backend.providerKey)
	}
}
