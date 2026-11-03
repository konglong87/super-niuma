package channelbridge

import (
	"context"
	"encoding/json"
	"github.com/konglong87/super-niuma/internal/super/control"
	"github.com/konglong87/super-niuma/internal/super/router"
	"strings"
	"testing"
)

func TestConfigurationUsesSnakeCaseAndHealthDoesNotExposeSecrets(t *testing.T) {
	var a Account
	if err := json.Unmarshal([]byte(`{"id":"account","provider":"webhook","enabled":true,"secret_env":"SECRET_REFERENCE","app_id_env":"APP_ID_REFERENCE","app_secret_env":"APP_SECRET_REFERENCE","bot_id_env":"BOT_REFERENCE","webhook_url":"https://example.invalid/hook"}`), &a); err != nil {
		t.Fatal(err)
	}
	if a.SecretEnv != "SECRET_REFERENCE" || a.AppIDEnv != "APP_ID_REFERENCE" || a.AppSecretEnv != "APP_SECRET_REFERENCE" || a.BotIDEnv != "BOT_REFERENCE" || a.WebhookURL == "" {
		t.Fatalf("snake_case config=%+v", a)
	}
	b, err := New(Options{Accounts: func() []Account { return []Account{a} }, Handler: handleFunc(func(context.Context, router.Envelope) (router.Decision, error) { return router.Decision{}, nil }), Getenv: func(string) string { return fixtureSecret }})
	if err != nil {
		t.Fatal(err)
	}
	list := b.Health()
	if len(list) != 1 || list[0].Status != HealthConfigured {
		t.Fatalf("%+v", list)
	}
	data, _ := json.Marshal(list)
	for _, secret := range []string{fixtureSecret, "SECRET_REFERENCE", "APP_ID_REFERENCE", "APP_SECRET_REFERENCE", "BOT_REFERENCE", a.WebhookURL} {
		if strings.Contains(string(data), secret) {
			t.Fatal("health secret disclosure")
		}
	}
	var dto webhookBody
	if err := json.Unmarshal([]byte(`{"event_id":"event","message_id":"message","chat":"chat","thread":"","sender":"sender","text":"/run ok"}`), &dto); err != nil || dto.EventID != "event" || dto.MessageID != "message" {
		t.Fatal("snake_case webhook DTO")
	}
}
func TestHealthShowsAdmissionDegradationAndNeverRawSDKErrors(t *testing.T) {
	a := &fakeAdapter{}
	b := feishuBridge(t, a, handleFunc(func(context.Context, router.Envelope) (router.Decision, error) { return router.Decision{}, nil }))
	_ = a.inbound(context.Background(), incoming())
	h := b.Health()
	if len(h) != 1 || h[0].Status != HealthDegraded || h[0].Code != "bot_identity_unknown" || h[0].Provider != control.ProviderFeishu {
		t.Fatalf("%+v", h)
	}
	if err := b.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b.Health()[0].Status != HealthClosed {
		t.Fatal("closed transport called ready")
	}
}
