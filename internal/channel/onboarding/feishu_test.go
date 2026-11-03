package onboarding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"testing"

	"github.com/larksuite/oapi-sdk-go/v3/scene/registration"
)

func TestOneClickFeishuAppBuildsMinimalRegistrationAndSavesCredentials(t *testing.T) {
	ctx := context.Background()
	var gotRegistration *registration.Options
	var gotURL VerificationURL
	var gotCredentials FeishuCredentials

	result, err := OneClickFeishuApp(ctx, Options{
		AppName:        "niuma",
		AppDescription: "A coding assistant",
		OnVerificationURL: func(info VerificationURL) error {
			gotURL = info
			return nil
		},
		CredentialsSink: func(_ context.Context, credentials FeishuCredentials) error {
			gotCredentials = credentials
			return nil
		},
		RegisterFunc: func(_ context.Context, opts *registration.Options) (*registration.RegisterAppResult, error) {
			gotRegistration = opts
			opts.OnQRCode(&registration.QRCodeInfo{URL: "https://example.test/verify", ExpireIn: 120})
			return &registration.RegisterAppResult{ClientID: "cli_test", ClientSecret: "secret_test"}, nil
		},
	})
	if err != nil {
		t.Fatalf("OneClickFeishuApp() error = %v", err)
	}
	if result.AppID != "cli_test" || result.AppSecret != "secret_test" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if result.VerificationURL != gotURL.URL || result.VerificationURLExpireIn != gotURL.ExpireIn {
		t.Fatalf("result did not retain verification URL lifecycle: %#v", result)
	}
	if gotCredentials.AppID != "cli_test" || gotCredentials.AppSecret != "secret_test" {
		t.Fatalf("unexpected credentials sink value: %#v", gotCredentials)
	}
	if gotRegistration == nil || !gotRegistration.CreateOnly {
		t.Fatal("expected CreateOnly registration option")
	}
	if gotRegistration.AppPreset == nil || gotRegistration.AppPreset.Name != "niuma" || gotRegistration.AppPreset.Desc != "A coding assistant" {
		t.Fatalf("unexpected app preset: %#v", gotRegistration.AppPreset)
	}
	if gotRegistration.Addons == nil {
		t.Fatal("expected app addons")
	}
	if gotRegistration.Addons.Preset == nil || *gotRegistration.Addons.Preset {
		t.Fatalf("expected minimal addons preset=false, got %#v", gotRegistration.Addons.Preset)
	}
	assertContains(t, gotRegistration.Addons.Scopes.Tenant, FeishuScopeSendAsBot)
	assertContains(t, gotRegistration.Addons.Scopes.Tenant, FeishuScopeMessage)
	assertContains(t, gotRegistration.Addons.Scopes.Tenant, FeishuScopeP2PMessageReadOnly)
	assertContains(t, gotRegistration.Addons.Scopes.Tenant, FeishuScopeGroupAtMessageReadOnly)
	assertContains(t, gotRegistration.Addons.Scopes.Tenant, FeishuScopeMessageReactionsWriteOnly)
	assertContains(t, gotRegistration.Addons.Scopes.Tenant, FeishuScopeResource)
	assertContains(t, gotRegistration.Addons.Events.Items.Tenant, FeishuEventMessageReceive)
	assertContains(t, gotRegistration.Addons.Callbacks.Items, FeishuCallbackCardAction)
	if len(gotRegistration.Addons.Scopes.Tenant) != 6 || len(gotRegistration.Addons.Events.Items.Tenant) != 1 || len(gotRegistration.Addons.Callbacks.Items) != 1 {
		t.Fatalf("registration requested more than minimal permissions: %#v", gotRegistration.Addons)
	}
}

func TestAuthorizeFeishuAppScopesTargetsExistingAppAndSavesCredentials(t *testing.T) {
	var got *registration.Options
	var saved FeishuCredentials
	result, err := AuthorizeFeishuAppScopes(context.Background(), ScopeAuthorizationOptions{
		AppID:             "cli_existing",
		Scopes:            []string{FeishuScopeMessageReactionsWriteOnly},
		OnVerificationURL: func(VerificationURL) error { return nil },
		CredentialsSink: func(_ context.Context, credentials FeishuCredentials) error {
			saved = credentials
			return nil
		},
		RegisterFunc: func(_ context.Context, opts *registration.Options) (*registration.RegisterAppResult, error) {
			got = opts
			opts.OnQRCode(&registration.QRCodeInfo{URL: "https://example.test/verify", ExpireIn: 120})
			return &registration.RegisterAppResult{ClientID: "cli_existing", ClientSecret: "secret-updated"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.AppID != "cli_existing" || got.CreateOnly || got.Addons == nil {
		t.Fatalf("registration options = %+v", got)
	}
	if len(got.Addons.Scopes.Tenant) != 1 || got.Addons.Scopes.Tenant[0] != FeishuScopeMessageReactionsWriteOnly {
		t.Fatalf("scopes = %+v", got.Addons.Scopes.Tenant)
	}
	if result.AppID != "cli_existing" || saved.AppID != "cli_existing" || saved.AppSecret != "secret-updated" {
		t.Fatalf("result=%+v saved=%+v", result, saved)
	}
}

func TestOneClickFeishuAppRequiresVerificationURLCallbackBeforeRegister(t *testing.T) {
	called := false
	_, err := OneClickFeishuApp(context.Background(), Options{
		RegisterFunc: func(context.Context, *registration.Options) (*registration.RegisterAppResult, error) {
			called = true
			return nil, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "verification URL callback") {
		t.Fatalf("expected missing callback error, got %v", err)
	}
	if called {
		t.Fatal("register function should not be called without verification URL callback")
	}
}

func TestOneClickFeishuAppReturnsVerificationURLCallbackError(t *testing.T) {
	wantErr := errors.New("display failed")
	_, err := OneClickFeishuApp(context.Background(), Options{
		OnVerificationURL: func(VerificationURL) error { return wantErr },
		RegisterFunc: func(_ context.Context, opts *registration.Options) (*registration.RegisterAppResult, error) {
			opts.OnQRCode(&registration.QRCodeInfo{URL: "https://example.test/verify", ExpireIn: 30})
			return &registration.RegisterAppResult{ClientID: "cli_test", ClientSecret: "secret_test"}, nil
		},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected callback error, got %v", err)
	}
}

func TestOneClickFeishuAppPropagatesCancellationAndRegisterError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := OneClickFeishuApp(ctx, Options{
		RegisterFunc: func(ctx context.Context, _ *registration.Options) (*registration.RegisterAppResult, error) {
			return nil, ctx.Err()
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}

	wantErr := errors.New("registration failed")
	_, err = OneClickFeishuApp(context.Background(), Options{
		OnVerificationURL: func(VerificationURL) error { return nil },
		RegisterFunc: func(context.Context, *registration.Options) (*registration.RegisterAppResult, error) {
			return nil, wantErr
		},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected register error, got %v", err)
	}
}

func TestOneClickFeishuAppDoesNotLogAppSecret(t *testing.T) {
	secret := "secret-that-must-not-be-logged"
	var output strings.Builder
	previousWriter := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previousWriter)

	_, err := OneClickFeishuApp(context.Background(), Options{
		RegisterFunc: func(_ context.Context, opts *registration.Options) (*registration.RegisterAppResult, error) {
			opts.OnQRCode(&registration.QRCodeInfo{URL: "https://example.test/verify", ExpireIn: 30})
			return &registration.RegisterAppResult{ClientID: "cli_test", ClientSecret: secret}, nil
		},
		OnVerificationURL: func(VerificationURL) error { return nil },
		CredentialsSink:   func(context.Context, FeishuCredentials) error { return nil },
	})
	if err != nil {
		t.Fatalf("OneClickFeishuApp() error = %v", err)
	}
	if strings.Contains(output.String(), secret) {
		t.Fatalf("app secret was written to logs: %q", output.String())
	}
}

func TestOneClickFeishuAppRetainsCredentialsWhenPersistenceFails(t *testing.T) {
	wantErr := errors.New("persistence failed")
	result, err := OneClickFeishuApp(context.Background(), Options{
		OnVerificationURL: func(VerificationURL) error { return nil },
		RegisterFunc: func(_ context.Context, opts *registration.Options) (*registration.RegisterAppResult, error) {
			opts.OnQRCode(&registration.QRCodeInfo{URL: "https://example.test/verify", ExpireIn: 30})
			return &registration.RegisterAppResult{ClientID: "cli_test", ClientSecret: "secret_test"}, nil
		},
		CredentialsSink: func(context.Context, FeishuCredentials) error { return wantErr },
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected persistence error, got %v", err)
	}
	if result.AppID != "cli_test" || result.AppSecret != "secret_test" {
		t.Fatalf("expected issued credentials in partial result, got %#v", result)
	}
}

func TestFeishuSecretsAreRedactedFromJSONAndFormatting(t *testing.T) {
	secret := "secret-that-must-not-leak"
	credentials := FeishuCredentials{AppID: "cli_test", AppSecret: secret}
	result := Result{AppID: "cli_test", AppSecret: secret}
	for name, value := range map[string]any{"credentials": credentials, "result": result} {
		t.Run(name, func(t *testing.T) {
			encoded, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), "AppSecret") {
				t.Fatalf("secret exposed in JSON: %s", encoded)
			}
			for format, formatted := range map[string]string{
				"plus-v":  fmt.Sprintf("%+v", value),
				"sharp-v": fmt.Sprintf("%#v", value),
			} {
				if strings.Contains(formatted, secret) {
					t.Fatalf("secret exposed in %s: %s", format, formatted)
				}
			}
		})
	}
}

func TestCredentialsSinkImplementsCredentialsStore(t *testing.T) {
	var store CredentialsStore = CredentialsSink(func(context.Context, FeishuCredentials) error { return nil })
	if store == nil {
		t.Fatal("expected credentials sink to implement CredentialsStore")
	}
}

func assertContains(t *testing.T, values []string, want string) {
	t.Helper()
	for _, value := range values {
		if value == want {
			return
		}
	}
	t.Fatalf("%q not found in %#v", want, values)
}
