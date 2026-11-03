package provisioning

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

type Status string

const (
	StatusDraft      Status = "draft"
	StatusValidating Status = "validating"
	StatusPublished  Status = "published"
	StatusPreflight  Status = "preflight"
	StatusStarting   Status = "starting"
	StatusRunning    Status = "running"
	StatusDegraded   Status = "degraded"
	StatusStopped    Status = "stopped"
	StatusFailed     Status = "failed"
)

type WorkerState string

const (
	WorkerStateUnknown  WorkerState = "unknown"
	WorkerStateStarting WorkerState = "starting"
	WorkerStateRunning  WorkerState = "running"
	WorkerStateDegraded WorkerState = "degraded"
	WorkerStateStopped  WorkerState = "stopped"
	WorkerStateFailed   WorkerState = "failed"
)

type ProvisioningSession struct {
	ID             string          `json:"id"`
	TenantKey      string          `json:"tenant_key"`
	ProfileKey     string          `json:"profile_key"`
	AccountKey     string          `json:"account_key"`
	Status         Status          `json:"status"`
	Credential     CredentialRef   `json:"credential"`
	Worker         WorkerSpec      `json:"worker"`
	ObservedWorker WorkerStatus    `json:"observed_worker"`
	Checks         []HealthCheck   `json:"checks,omitempty"`
	LastError      *ProvisionError `json:"last_error,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

type Record struct {
	ID            uint64          `json:"id"`
	TenantID      uint64          `json:"tenant_id"`
	ProfileKey    string          `json:"profile_key"`
	AccountKey    string          `json:"account_key"`
	CredentialRef string          `json:"credential_ref"`
	Supervisor    string          `json:"supervisor"`
	Status        Status          `json:"status"`
	WorkerSpec    WorkerSpec      `json:"worker"`
	WorkerStatus  WorkerStatus    `json:"observed_worker"`
	Checks        []HealthCheck   `json:"checks,omitempty"`
	LastError     *ProvisionError `json:"last_error,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

func NewSession(tenantKey, profileKey, accountKey string) ProvisioningSession {
	now := time.Now().UTC()
	return ProvisioningSession{ID: idempotencyDigest(tenantKey, profileKey, accountKey), TenantKey: strings.TrimSpace(tenantKey), ProfileKey: strings.TrimSpace(profileKey), AccountKey: strings.TrimSpace(accountKey), Status: StatusDraft, CreatedAt: now, UpdatedAt: now}
}

func (s ProvisioningSession) IdempotencyKey() string {
	return idempotencyDigest(s.TenantKey, s.ProfileKey, s.AccountKey)
}

func (s *ProvisioningSession) Transition(next Status) error {
	if s == nil {
		return NewError(ErrInvalidTransition, "session is nil")
	}
	if s.Status == next {
		return nil
	}
	allowed := map[Status]map[Status]bool{
		StatusDraft:      {StatusValidating: true},
		StatusValidating: {StatusDraft: true, StatusPublished: true, StatusFailed: true},
		StatusPublished:  {StatusPreflight: true, StatusDraft: true},
		StatusPreflight:  {StatusPublished: true, StatusStarting: true, StatusFailed: true},
		StatusStarting:   {StatusRunning: true, StatusDegraded: true, StatusFailed: true, StatusStopped: true},
		StatusRunning:    {StatusDegraded: true, StatusStopped: true, StatusFailed: true},
		StatusDegraded:   {StatusRunning: true, StatusStopped: true, StatusFailed: true},
		StatusStopped:    {StatusStarting: true, StatusPublished: true},
		StatusFailed:     {StatusDraft: true, StatusPreflight: true, StatusStarting: true, StatusStopped: true},
	}
	if !allowed[s.Status][next] {
		return NewError(ErrInvalidTransition, fmt.Sprintf("cannot transition from %s to %s", s.Status, next))
	}
	s.Status, s.UpdatedAt = next, time.Now().UTC()
	return nil
}

func (s ProvisioningSession) Redacted() ProvisioningSession {
	s.Credential.SecretValue = ""
	s.Worker.PayloadKey = ""
	return s
}

type WorkerSpec struct {
	Supervisor     string `json:"supervisor"`
	AccountKey     string `json:"account_key"`
	Workspace      string `json:"workspace"`
	SettingsRef    string `json:"settings_ref"`
	Provider       string `json:"provider"`
	Model          string `json:"model"`
	Streaming      string `json:"streaming"`
	Reactions      string `json:"reactions"`
	PermissionMode string `json:"permission_mode"`
	PayloadKeyRef  string `json:"payload_key_ref"`
	PayloadKey     string `json:"-"`
	// Environment contains non-secret worker wiring such as the SQLite path,
	// account id and credential file location. Secret values are intentionally
	// never placed here; the worker derives its payload key locally.
	Environment map[string]string `json:"environment,omitempty"`
	WorkerName  string            `json:"worker_name,omitempty"`
}

type WorkerStatus struct {
	TenantID   uint64      `json:"tenant_id,omitempty"`
	AccountID  uint64      `json:"account_id,omitempty"`
	AccountKey string      `json:"account_key,omitempty"`
	State      WorkerState `json:"state"`
	PID        int         `json:"pid,omitempty"`
	Screen     string      `json:"screen,omitempty"`
	LogPath    string      `json:"log_path,omitempty"`
	Provider   string      `json:"provider,omitempty"`
	Model      string      `json:"model,omitempty"`
	ObservedAt time.Time   `json:"observed_at"`
	Message    string      `json:"message,omitempty"`
}

type Overview struct {
	Records []Record       `json:"records"`
	Workers []WorkerStatus `json:"workers"`
}

func (w WorkerStatus) Healthy() bool {
	return w.State == WorkerStateRunning && w.PID > 0 && !w.ObservedAt.IsZero()
}

type HealthCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type CredentialRef struct {
	ID            string `json:"id"`
	Provider      string `json:"provider"`
	AppID         string `json:"app_id,omitempty"`
	SecretValue   string `json:"secret_value,omitempty"`
	SecretPresent bool   `json:"secret_present"`
}

type ProviderOption struct {
	Name  string `json:"name"`
	Model string `json:"model"`
}

func idempotencyDigest(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write([]byte(strings.TrimSpace(part)))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:24]
}
