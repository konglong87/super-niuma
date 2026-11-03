// Package onboarding contains provider-specific account setup flows.
//
// The official registration SDK may write a tenant brand to process stdout
// while polling. This package deliberately does not redirect stdout or print
// registration data; callers needing strict output isolation should inject a
// RegisterFunc implementation instead.
package onboarding

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/larksuite/oapi-sdk-go/v3/scene/registration"
)

const (
	// FeishuScopeMessage is required by Feishu reaction create/delete APIs.
	FeishuScopeMessage = "im:message"
	// FeishuScopeSendAsBot allows outbound bot messages.
	FeishuScopeSendAsBot = "im:message:send_as_bot"
	// FeishuScopeP2PMessageReadOnly allows receiving direct messages.
	FeishuScopeP2PMessageReadOnly = "im:message.p2p_msg:readonly"
	// FeishuScopeGroupAtMessageReadOnly allows receiving group messages that mention the bot.
	FeishuScopeGroupAtMessageReadOnly = "im:message.group_at_msg:readonly"
	// FeishuScopeMessageReactionsWriteOnly allows the bot to create and remove its own message reactions.
	FeishuScopeMessageReactionsWriteOnly = "im:message.reactions:write_only"
	// FeishuScopeResource allows the bot to upload image bytes for outbound image messages.
	FeishuScopeResource = "im:resource"
	// FeishuEventMessageReceive lets the channel runtime receive bot messages.
	FeishuEventMessageReceive = "im.message.receive_v1"
	// FeishuCallbackCardAction enables secure interactive-card callbacks.
	FeishuCallbackCardAction = "card.action.trigger"
)

type VerificationURL struct {
	URL      string
	ExpireIn int
}

type FeishuCredentials struct {
	AppID     string
	AppSecret string `json:"-"`
}

const redactedSecret = "[REDACTED]"

func (credentials FeishuCredentials) String() string {
	return fmt.Sprintf("{AppID:%s AppSecret:%s}", credentials.AppID, redactedSecret)
}

func (credentials FeishuCredentials) GoString() string {
	return credentials.String()
}

// CredentialsStore is the persistence boundary for newly issued app secrets.
// Implementations should encrypt or otherwise protect AppSecret at rest.
type CredentialsStore interface {
	SaveFeishuCredentials(context.Context, FeishuCredentials) error
}

// CredentialsSink adapts a persistence callback to CredentialsStore.
type CredentialsSink func(context.Context, FeishuCredentials) error

func (sink CredentialsSink) SaveFeishuCredentials(ctx context.Context, credentials FeishuCredentials) error {
	if sink == nil {
		return nil
	}
	return sink(ctx, credentials)
}

type RegisterFunc func(context.Context, *registration.Options) (*registration.RegisterAppResult, error)

type Options struct {
	// AppName and AppDescription are used to build the SDK AppPreset.
	AppName        string
	AppDescription string

	// AppPreset allows callers to configure additional SDK-supported preset
	// fields (for example avatar URLs). AppName/AppDescription take precedence
	// when set.
	AppPreset *registration.AppPreset

	// OnVerificationURL is called as soon as Feishu issues the device URL. The
	// callback must display or otherwise relay the URL; this package never logs it
	// or the resulting app secret. A callback error aborts registration.
	OnVerificationURL func(VerificationURL) error

	// CredentialsSink is the preferred lightweight injection for tests and
	// callers that already own a secure persistence implementation.
	CredentialsSink CredentialsSink
	// CredentialsStore is an alternative explicit persistence interface.
	CredentialsStore CredentialsStore

	// RegisterFunc replaces the SDK registration function in tests. Leaving it
	// nil uses registration.RegisterApp, which waits for device authorization and
	// polls until credentials are available or ctx is cancelled.
	RegisterFunc RegisterFunc
}

type ScopeAuthorizationOptions struct {
	AppID             string
	Scopes            []string
	OnVerificationURL func(VerificationURL) error
	CredentialsSink   CredentialsSink
	CredentialsStore  CredentialsStore
	RegisterFunc      RegisterFunc
}

type Result struct {
	AppID                   string
	AppSecret               string `json:"-"`
	VerificationURL         string
	VerificationURLExpireIn int
}

func (result Result) String() string {
	return fmt.Sprintf("{AppID:%s AppSecret:%s VerificationURL:%s VerificationURLExpireIn:%d}", result.AppID, redactedSecret, result.VerificationURL, result.VerificationURLExpireIn)
}

func (result Result) GoString() string {
	return result.String()
}

// FeishuAppResult is kept as a descriptive alias for callers that prefer an
// explicit result name.
type FeishuAppResult = Result

// OneClickFeishuApp starts Feishu Device Authorization, exposes the URL to the
// caller, waits for the user to approve it, and returns the issued credentials.
func OneClickFeishuApp(ctx context.Context, opts Options) (Result, error) {
	if ctx == nil {
		return Result{}, errors.New("feishu onboarding: context is required")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if opts.OnVerificationURL == nil {
		return Result{}, errors.New("feishu onboarding: verification URL callback is required")
	}
	register := opts.RegisterFunc
	if register == nil {
		register = registration.RegisterApp
	}

	registrationCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var stateMu sync.Mutex
	var verification VerificationURL
	var callbackErr error
	registrationOpts := &registration.Options{
		AppPreset:  buildAppPreset(opts),
		Addons:     minimalAppAddons(),
		CreateOnly: true,
		OnQRCode: func(info *registration.QRCodeInfo) {
			if info == nil {
				return
			}
			if err := registrationCtx.Err(); err != nil {
				return
			}
			candidate := VerificationURL{URL: info.URL, ExpireIn: info.ExpireIn}
			stateMu.Lock()
			verification = candidate
			stateMu.Unlock()
			if err := opts.OnVerificationURL(candidate); err != nil {
				stateMu.Lock()
				if callbackErr == nil {
					callbackErr = err
				}
				stateMu.Unlock()
				cancel()
			}
		},
	}

	registered, err := register(registrationCtx, registrationOpts)
	stateMu.Lock()
	callbackFailure := callbackErr
	verificationURL := verification
	stateMu.Unlock()
	if callbackFailure != nil {
		return Result{}, fmt.Errorf("feishu onboarding: verification URL callback: %w", callbackFailure)
	}
	if err != nil {
		return Result{}, err
	}
	if registered == nil {
		return Result{}, errors.New("feishu onboarding: registration returned nil result")
	}

	result := Result{
		AppID:                   registered.ClientID,
		AppSecret:               registered.ClientSecret,
		VerificationURL:         verificationURL.URL,
		VerificationURLExpireIn: verificationURL.ExpireIn,
	}
	if err := saveCredentials(ctx, opts, FeishuCredentials{AppID: result.AppID, AppSecret: result.AppSecret}); err != nil {
		// The credentials were issued by Feishu even though persistence failed;
		// callers must persist or recover them before retrying onboarding.
		return result, fmt.Errorf("feishu onboarding: save credentials: %w", err)
	}
	return result, nil
}

// AuthorizeFeishuAppScopes incrementally grants tenant scopes to an existing
// app. AppID is passed to the official registration flow so it cannot create a
// parallel application by accident.
func AuthorizeFeishuAppScopes(ctx context.Context, opts ScopeAuthorizationOptions) (Result, error) {
	if ctx == nil {
		return Result{}, errors.New("feishu scope authorization: context is required")
	}
	if strings.TrimSpace(opts.AppID) == "" || len(opts.Scopes) == 0 {
		return Result{}, errors.New("feishu scope authorization: app id and scopes are required")
	}
	if opts.OnVerificationURL == nil {
		return Result{}, errors.New("feishu scope authorization: verification URL callback is required")
	}
	scopes := make([]string, 0, len(opts.Scopes))
	seen := map[string]struct{}{}
	for _, scope := range opts.Scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			return Result{}, errors.New("feishu scope authorization: scope must not be empty")
		}
		if _, exists := seen[scope]; exists {
			continue
		}
		seen[scope] = struct{}{}
		scopes = append(scopes, scope)
	}
	register := opts.RegisterFunc
	if register == nil {
		register = registration.RegisterApp
	}
	var verification VerificationURL
	registered, err := register(ctx, &registration.Options{
		AppID:  opts.AppID,
		Addons: &registration.AppAddons{Scopes: registration.AppAddonsScopes{Tenant: scopes}},
		OnQRCode: func(info *registration.QRCodeInfo) {
			if info == nil {
				return
			}
			verification = VerificationURL{URL: info.URL, ExpireIn: info.ExpireIn}
			_ = opts.OnVerificationURL(verification)
		},
	})
	if err != nil {
		return Result{}, err
	}
	if registered == nil || registered.ClientID != opts.AppID || registered.ClientSecret == "" {
		return Result{}, errors.New("feishu scope authorization: registration returned unexpected app credentials")
	}
	result := Result{AppID: registered.ClientID, AppSecret: registered.ClientSecret, VerificationURL: verification.URL, VerificationURLExpireIn: verification.ExpireIn}
	credentials := FeishuCredentials{AppID: result.AppID, AppSecret: result.AppSecret}
	if opts.CredentialsSink != nil {
		err = opts.CredentialsSink.SaveFeishuCredentials(ctx, credentials)
	} else if opts.CredentialsStore != nil {
		err = opts.CredentialsStore.SaveFeishuCredentials(ctx, credentials)
	}
	if err != nil {
		return result, fmt.Errorf("feishu scope authorization: save credentials: %w", err)
	}
	return result, nil
}

func buildAppPreset(opts Options) *registration.AppPreset {
	if opts.AppPreset == nil && opts.AppName == "" && opts.AppDescription == "" {
		return nil
	}
	var preset registration.AppPreset
	if opts.AppPreset != nil {
		preset = *opts.AppPreset
		preset.Avatar = append([]string(nil), opts.AppPreset.Avatar...)
	}
	if opts.AppName != "" {
		preset.Name = opts.AppName
	}
	if opts.AppDescription != "" {
		preset.Desc = opts.AppDescription
	}
	return &preset
}

func minimalAppAddons() *registration.AppAddons {
	minimal := false
	return &registration.AppAddons{
		Preset:    &minimal,
		Scopes:    registration.AppAddonsScopes{Tenant: []string{FeishuScopeMessage, FeishuScopeSendAsBot, FeishuScopeP2PMessageReadOnly, FeishuScopeGroupAtMessageReadOnly, FeishuScopeMessageReactionsWriteOnly, FeishuScopeResource}},
		Events:    registration.AppAddonsEvents{Items: registration.AppAddonsEventItems{Tenant: []string{FeishuEventMessageReceive}}},
		Callbacks: registration.AppAddonsCallbacks{Items: []string{FeishuCallbackCardAction}},
	}
}

func saveCredentials(ctx context.Context, opts Options, credentials FeishuCredentials) error {
	if opts.CredentialsSink != nil {
		return opts.CredentialsSink(ctx, credentials)
	}
	if opts.CredentialsStore != nil {
		return opts.CredentialsStore.SaveFeishuCredentials(ctx, credentials)
	}
	return nil
}
