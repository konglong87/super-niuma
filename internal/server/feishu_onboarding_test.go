package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/provisioning"
	"github.com/larksuite/oapi-sdk-go/v3/scene/registration"
)

type onboardingProvisioningStub struct {
	created provisioning.CreateRequest
}

func (s *onboardingProvisioningStub) Create(_ context.Context, _, _ uint64, request provisioning.CreateRequest) (provisioning.Record, error) {
	s.created = request
	return provisioning.Record{ID: 41, ProfileKey: request.ProfileKey, AccountKey: request.AccountKey, Status: provisioning.StatusDraft}, nil
}

func (*onboardingProvisioningStub) List(context.Context, uint64, int) ([]provisioning.Record, error) {
	return nil, nil
}

func (*onboardingProvisioningStub) Get(context.Context, uint64, uint64, string) (provisioning.Record, error) {
	return provisioning.Record{}, nil
}

func (*onboardingProvisioningStub) Preflight(context.Context, uint64, uint64, uint64) (provisioning.Record, error) {
	return provisioning.Record{}, nil
}

func (*onboardingProvisioningStub) WorkerAction(context.Context, uint64, uint64, uint64, string) (provisioning.Record, error) {
	return provisioning.Record{}, nil
}

func (*onboardingProvisioningStub) Logs(context.Context, uint64, uint64, int) (string, error) {
	return "", nil
}

func (*onboardingProvisioningStub) Overview(context.Context, uint64, int) (provisioning.Overview, error) {
	return provisioning.Overview{}, nil
}

func waitForOnboarding(t *testing.T, manager *FeishuOnboardingManager, tenantID uint64, id string, predicate func(FeishuOnboardingSession) bool) FeishuOnboardingSession {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		session, err := manager.Get(context.Background(), tenantID, id)
		if err == nil && predicate(session) {
			return session
		}
		time.Sleep(10 * time.Millisecond)
	}
	session, _ := manager.Get(context.Background(), tenantID, id)
	t.Fatalf("session did not reach expected state: %+v", session)
	return FeishuOnboardingSession{}
}

func TestFeishuOnboardingManagerCreatesQRCodeAndDraft(t *testing.T) {
	stub := &onboardingProvisioningStub{}
	manager := NewFeishuOnboardingManager(stub)
	manager.RegisterFunc = func(_ context.Context, opts *registration.Options) (*registration.RegisterAppResult, error) {
		opts.OnQRCode(&registration.QRCodeInfo{URL: "https://example.test/feishu/verify", ExpireIn: 90})
		return &registration.RegisterAppResult{ClientID: "cli_desktop", ClientSecret: "secret_desktop"}, nil
	}

	session, err := manager.Start(context.Background(), 7, 9, FeishuOnboardingRequest{ProfileKey: "support-agent", AccountKey: "support-bot", Provider: "openai", Model: "fixture-model"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	created := waitForOnboarding(t, manager, 7, session.ID, func(item FeishuOnboardingSession) bool { return item.Status == feishuOnboardingStatusCreated })
	if created.AppID != "cli_desktop" || created.Record == nil || created.Record.ID != 41 {
		t.Fatalf("created session = %+v", created)
	}
	if !strings.HasPrefix(created.VerificationQRCode, "data:image/png;base64,") {
		t.Fatalf("verification QR = %q", created.VerificationQRCode)
	}
	if stub.created.CredentialRef.AppID != "cli_desktop" || stub.created.CredentialRef.SecretValue != "secret_desktop" {
		t.Fatalf("credential request = %+v", stub.created.CredentialRef)
	}
}

func TestFeishuOnboardingManagerCancelStopsRegistration(t *testing.T) {
	manager := NewFeishuOnboardingManager(nil)
	registrationStopped := make(chan struct{})
	manager.RegisterFunc = func(ctx context.Context, opts *registration.Options) (*registration.RegisterAppResult, error) {
		opts.OnQRCode(&registration.QRCodeInfo{URL: "https://example.test/feishu/verify", ExpireIn: 30})
		<-ctx.Done()
		close(registrationStopped)
		return nil, ctx.Err()
	}

	session, err := manager.Start(context.Background(), 7, 9, FeishuOnboardingRequest{ProfileKey: "support-agent", AccountKey: "support-bot"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitForOnboarding(t, manager, 7, session.ID, func(item FeishuOnboardingSession) bool { return item.Status == feishuOnboardingStatusWaiting })
	if err := manager.Cancel(context.Background(), 7, session.ID); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	waitForOnboarding(t, manager, 7, session.ID, func(item FeishuOnboardingSession) bool { return item.Status == feishuOnboardingStatusCanceled })
	select {
	case <-registrationStopped:
	case <-time.After(time.Second):
		t.Fatal("registration did not stop after cancellation")
	}
}

func TestFeishuOnboardingManagerRequiresTenantAndAccount(t *testing.T) {
	manager := NewFeishuOnboardingManager(nil)
	if _, err := manager.Start(context.Background(), 0, 9, FeishuOnboardingRequest{ProfileKey: "agent", AccountKey: "account"}); err == nil {
		t.Fatal("expected tenant validation error")
	}
	if _, err := manager.Start(context.Background(), 7, 9, FeishuOnboardingRequest{ProfileKey: "agent"}); err == nil {
		t.Fatal("expected account validation error")
	}
}
