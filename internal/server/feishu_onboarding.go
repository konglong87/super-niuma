package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/konglong87/super-niuma/internal/channel/onboarding"
	"github.com/konglong87/super-niuma/internal/feishuprovision"
	"github.com/konglong87/super-niuma/internal/provisioning"
	qrcode "github.com/skip2/go-qrcode"
)

const (
	feishuOnboardingStatusStarting = "starting"
	feishuOnboardingStatusWaiting  = "waiting_for_scan"
	feishuOnboardingStatusCreated  = "created"
	feishuOnboardingStatusFailed   = "failed"
	feishuOnboardingStatusCanceled = "canceled"
)

type FeishuOnboardingRequest struct {
	ProfileKey     string `json:"profile_key"`
	AccountKey     string `json:"account_key"`
	AppName        string `json:"app_name,omitempty"`
	AppDescription string `json:"app_description,omitempty"`
	Provider       string `json:"provider,omitempty"`
	Model          string `json:"model,omitempty"`
	Streaming      string `json:"streaming,omitempty"`
	Reactions      string `json:"reactions,omitempty"`
}

type FeishuOnboardingSession struct {
	ID                    string               `json:"id"`
	Status                string               `json:"status"`
	ProfileKey            string               `json:"profile_key"`
	AccountKey            string               `json:"account_key"`
	AppID                 string               `json:"app_id,omitempty"`
	VerificationURL       string               `json:"verification_url,omitempty"`
	VerificationQRCode    string               `json:"verification_qr_code,omitempty"`
	VerificationExpiresIn int                  `json:"verification_expires_in,omitempty"`
	Record                *provisioning.Record `json:"record,omitempty"`
	Error                 string               `json:"error,omitempty"`
	CreatedAt             time.Time            `json:"created_at"`
	UpdatedAt             time.Time            `json:"updated_at"`
}

type feishuOnboardingEntry struct {
	FeishuOnboardingSession
	cancel context.CancelFunc
	tenant uint64
	user   uint64
}

// FeishuOnboardingManager bridges the shared TUI registration flow to HTTP.
// It owns no durable secrets: the provisioning service persists credentials
// through its existing credential store after the device flow completes.
type FeishuOnboardingManager struct {
	mu           sync.Mutex
	sessions     map[string]*feishuOnboardingEntry
	Provisioning ProvisioningService
	RegisterFunc onboarding.RegisterFunc
	CLI          feishuprovision.CLIInstaller
	Now          func() time.Time
}

func NewFeishuOnboardingManager(provisioningService ProvisioningService) *FeishuOnboardingManager {
	return &FeishuOnboardingManager{sessions: make(map[string]*feishuOnboardingEntry), Provisioning: provisioningService, Now: time.Now}
}

func (m *FeishuOnboardingManager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *FeishuOnboardingManager) Start(_ context.Context, tenantID, userID uint64, req FeishuOnboardingRequest) (FeishuOnboardingSession, error) {
	if tenantID == 0 || userID == 0 {
		return FeishuOnboardingSession{}, fmt.Errorf("tenant context is required")
	}
	if strings.TrimSpace(req.ProfileKey) == "" || strings.TrimSpace(req.AccountKey) == "" {
		return FeishuOnboardingSession{}, fmt.Errorf("profile_key and account_key are required")
	}
	if req.AppName == "" {
		req.AppName = strings.TrimSpace(req.AccountKey)
	}
	if req.AppDescription == "" {
		req.AppDescription = "niuma Feishu channel bot"
	}
	if req.Streaming == "" {
		req.Streaming = "on"
	}
	if req.Reactions == "" {
		req.Reactions = "on"
	}
	id, err := newOnboardingID()
	if err != nil {
		return FeishuOnboardingSession{}, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	now := m.now()
	entry := &feishuOnboardingEntry{FeishuOnboardingSession: FeishuOnboardingSession{ID: id, Status: feishuOnboardingStatusStarting, ProfileKey: req.ProfileKey, AccountKey: req.AccountKey, CreatedAt: now, UpdatedAt: now}, cancel: cancel, tenant: tenantID, user: userID}
	m.mu.Lock()
	if m.sessions == nil {
		m.sessions = make(map[string]*feishuOnboardingEntry)
	}
	m.sessions[id] = entry
	m.mu.Unlock()
	go m.run(ctx, entry, req)
	return entry.FeishuOnboardingSession, nil
}

func (m *FeishuOnboardingManager) run(ctx context.Context, entry *feishuOnboardingEntry, req FeishuOnboardingRequest) {
	result, err := onboarding.OneClickFeishuApp(ctx, onboarding.Options{
		AppName: req.AppName, AppDescription: req.AppDescription, RegisterFunc: m.RegisterFunc,
		OnVerificationURL: func(info onboarding.VerificationURL) error {
			qr, qrErr := qrcode.Encode(info.URL, qrcode.Medium, 320)
			m.mu.Lock()
			defer m.mu.Unlock()
			if qrErr != nil {
				entry.Status = feishuOnboardingStatusFailed
				entry.Error = qrErr.Error()
			} else {
				entry.Status = feishuOnboardingStatusWaiting
				entry.VerificationURL = info.URL
				entry.VerificationQRCode = "data:image/png;base64," + base64.StdEncoding.EncodeToString(qr)
				entry.VerificationExpiresIn = info.ExpireIn
			}
			entry.UpdatedAt = m.now()
			return qrErr
		},
	})
	if err != nil {
		m.mu.Lock()
		if entry.Status != feishuOnboardingStatusCanceled {
			entry.Status = feishuOnboardingStatusFailed
			entry.Error = err.Error()
			entry.UpdatedAt = m.now()
		}
		m.mu.Unlock()
		return
	}
	if ctx.Err() != nil {
		return
	}
	var record *provisioning.Record
	if m.Provisioning != nil {
		created, createErr := m.Provisioning.Create(ctx, entry.tenant, entry.user, provisioning.CreateRequest{
			ProfileKey:    req.ProfileKey,
			AccountKey:    req.AccountKey,
			CredentialRef: provisioning.CredentialRef{ID: req.AccountKey, Provider: "feishu", AppID: result.AppID, SecretValue: result.AppSecret},
			Worker:        provisioning.WorkerSpec{Supervisor: "screen", AccountKey: req.AccountKey, WorkerName: req.ProfileKey, Provider: req.Provider, Model: req.Model, SettingsRef: "global-settings", Streaming: req.Streaming, Reactions: req.Reactions, PermissionMode: "ask"},
		})
		if createErr != nil {
			m.mu.Lock()
			entry.Status = feishuOnboardingStatusFailed
			entry.Error = createErr.Error()
			entry.UpdatedAt = m.now()
			m.mu.Unlock()
			return
		}
		record = &created
	}
	m.mu.Lock()
	if entry.Status != feishuOnboardingStatusCanceled {
		entry.Status = feishuOnboardingStatusCreated
		entry.AppID = result.AppID
		entry.Record = record
		entry.UpdatedAt = m.now()
	}
	m.mu.Unlock()
}

func (m *FeishuOnboardingManager) Get(_ context.Context, tenantID uint64, id string) (FeishuOnboardingSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := m.sessions[strings.TrimSpace(id)]
	if entry == nil || entry.tenant != tenantID {
		return FeishuOnboardingSession{}, fmt.Errorf("feishu onboarding session not found")
	}
	return entry.FeishuOnboardingSession, nil
}

func (m *FeishuOnboardingManager) Cancel(_ context.Context, tenantID uint64, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := m.sessions[strings.TrimSpace(id)]
	if entry == nil || entry.tenant != tenantID {
		return fmt.Errorf("feishu onboarding session not found")
	}
	if entry.cancel != nil {
		entry.cancel()
	}
	entry.Status = feishuOnboardingStatusCanceled
	entry.UpdatedAt = m.now()
	return nil
}

func (m *FeishuOnboardingManager) CLIStatus(ctx context.Context) (provisioning.CLIAvailability, error) {
	return m.CLI.Availability(ctx)
}

func (m *FeishuOnboardingManager) InstallCLI(ctx context.Context) (provisioning.CLIAvailability, error) {
	return m.CLI.Install(ctx)
}

func newOnboardingID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate onboarding session id: %w", err)
	}
	return fmt.Sprintf("feishu-%x", buf), nil
}
