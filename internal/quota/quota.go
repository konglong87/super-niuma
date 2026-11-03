package quota

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/konglong87/super-niuma/internal/session"
)

const (
	SourceMobile = "mobile"
	SourceQuery  = "query"
	SourceOpenAI = "openai"

	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
	StatusRejected  = "rejected"
)

var (
	ErrRateLimited               = errors.New("tenant quota qps limit exceeded")
	ErrDailyTokenLimitExceeded   = errors.New("tenant daily token quota exceeded")
	ErrDailyMessageLimitExceeded = errors.New("tenant daily message quota exceeded")
	ErrConcurrentLimitExceeded   = errors.New("tenant concurrent request limit exceeded")
	ErrInvalidConfig             = errors.New("tenant quota config invalid")
)

type Config struct {
	TenantID              uint64    `json:"tenant_id"`
	QuotaEnabled          bool      `json:"quota_enabled"`
	QPSLimit              *uint64   `json:"qps_limit,omitempty"`
	DailyTokenLimit       *uint64   `json:"daily_token_limit,omitempty"`
	DailyMessageLimit     *uint64   `json:"daily_message_limit,omitempty"`
	MaxConcurrentRequests *uint64   `json:"max_concurrent_requests,omitempty"`
	Timezone              string    `json:"timezone"`
	ReserveOutputTokens   uint64    `json:"reserve_output_tokens"`
	Status                string    `json:"status"`
	UpdatedByUserID       uint64    `json:"updated_by_user_id,omitempty"`
	CreatedAt             time.Time `json:"created_at,omitempty"`
	UpdatedAt             time.Time `json:"updated_at,omitempty"`
}

type ConfigInput struct {
	TenantID              uint64
	QuotaEnabled          bool
	QPSLimit              *uint64
	DailyTokenLimit       *uint64
	DailyMessageLimit     *uint64
	MaxConcurrentRequests *uint64
	Timezone              string
	ReserveOutputTokens   uint64
	Status                string
	UpdatedByUserID       uint64
}

type ReserveRequest struct {
	RequestID            string
	TenantID             uint64
	UserID               uint64
	TenantKey            string
	UserKey              string
	Source               string
	Route                string
	Model                string
	Provider             string
	Turn                 int
	UsageSource          string
	SessionID            uint64
	TraceID              string
	EstimatedInputTokens uint64
	ReservedOutputTokens uint64
	StartedAt            time.Time
}

type Reservation struct {
	RequestID            string
	TenantID             uint64
	UserID               uint64
	Source               string
	Route                string
	Model                string
	Provider             string
	Turn                 int
	UsageSource          string
	SessionID            uint64
	TraceID              string
	QuotaEnabled         bool
	UsageDate            time.Time
	ReservedInputTokens  uint64
	ReservedOutputTokens uint64
	StartedAt            time.Time
}

type Usage struct {
	InputTokens              uint64 `json:"input_tokens,omitempty"`
	OutputTokens             uint64 `json:"output_tokens,omitempty"`
	CacheCreationInputTokens uint64 `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     uint64 `json:"cache_read_input_tokens,omitempty"`
	CacheCreationEphemeral1h uint64 `json:"cache_creation_ephemeral_1h_input_tokens,omitempty"`
	CacheCreationEphemeral5m uint64 `json:"cache_creation_ephemeral_5m_input_tokens,omitempty"`
	Estimated                bool   `json:"estimated,omitempty"`
}

// UsageFromTurnUsage adapts the disjoint provider-neutral contract to quota's
// legacy whole-input accounting. Cache tiers remain separately available for
// reporting while InputTokens preserves the quota reservation invariant.
func UsageFromTurnUsage(turn session.TurnUsage) Usage {
	cacheCreate := turn.CacheCreationInputTokens
	if turn.CacheCreationEphemeral5m > 0 || turn.CacheCreationEphemeral1h > 0 {
		cacheCreate = turn.CacheCreationEphemeral5m + turn.CacheCreationEphemeral1h
	}
	return Usage{
		InputTokens:              uint64(maxInt(turn.InputTokens) + maxInt(turn.CacheReadInputTokens) + maxInt(cacheCreate)),
		OutputTokens:             uint64(maxInt(turn.OutputTokens)),
		CacheCreationInputTokens: uint64(maxInt(cacheCreate)),
		CacheReadInputTokens:     uint64(maxInt(turn.CacheReadInputTokens)),
		CacheCreationEphemeral1h: uint64(maxInt(turn.CacheCreationEphemeral1h)),
		CacheCreationEphemeral5m: uint64(maxInt(turn.CacheCreationEphemeral5m)),
		Estimated:                turn.Estimated,
	}
}

func maxInt(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

func (u Usage) TotalTokens() uint64 {
	return u.InputTokens + u.OutputTokens
}

type LedgerInput struct {
	RequestID                string
	TenantID                 uint64
	UserID                   uint64
	SessionID                uint64
	TraceID                  string
	Source                   string
	Route                    string
	Model                    string
	Provider                 string
	Turn                     int
	UsageSource              string
	Status                   string
	Estimated                bool
	ReservedInputTokens      uint64
	ReservedOutputTokens     uint64
	InputTokens              uint64
	OutputTokens             uint64
	CacheReadInputTokens     uint64
	CacheCreationInputTokens uint64
	CacheCreationEphemeral1h uint64
	CacheCreationEphemeral5m uint64
	TotalTokens              uint64
	ErrorCode                string
	ErrorMessage             string
	StartedAt                time.Time
	FinishedAt               *time.Time
}

type UsageDailyDelta struct {
	TenantID                 uint64
	UsageDate                time.Time
	Source                   string
	Model                    string
	RequestCount             uint64
	MessageCount             uint64
	InputTokens              uint64
	OutputTokens             uint64
	CacheReadInputTokens     uint64
	CacheCreationInputTokens uint64
	CacheCreationEphemeral1h uint64
	CacheCreationEphemeral5m uint64
	TotalTokens              uint64
	RejectedCount            uint64
}

type UsageDaily struct {
	ID                       uint64    `json:"id,omitempty"`
	TenantID                 uint64    `json:"tenant_id"`
	UsageDate                string    `json:"usage_date"`
	Source                   string    `json:"source"`
	Model                    string    `json:"model"`
	RequestCount             uint64    `json:"request_count"`
	MessageCount             uint64    `json:"message_count"`
	InputTokens              uint64    `json:"input_tokens"`
	OutputTokens             uint64    `json:"output_tokens"`
	CacheReadInputTokens     uint64    `json:"cache_read_input_tokens"`
	CacheCreationInputTokens uint64    `json:"cache_creation_input_tokens"`
	TotalTokens              uint64    `json:"total_tokens"`
	RejectedCount            uint64    `json:"rejected_count"`
	UpdatedAt                time.Time `json:"updated_at,omitempty"`
}

type Ledger struct {
	ID                       uint64    `json:"id"`
	RequestID                string    `json:"request_id"`
	TenantID                 uint64    `json:"tenant_id"`
	UserID                   uint64    `json:"user_id,omitempty"`
	SessionID                uint64    `json:"session_id,omitempty"`
	TraceID                  string    `json:"trace_id,omitempty"`
	Source                   string    `json:"source"`
	Route                    string    `json:"route,omitempty"`
	Model                    string    `json:"model,omitempty"`
	Provider                 string    `json:"provider,omitempty"`
	Turn                     int       `json:"turn,omitempty"`
	UsageSource              string    `json:"usage_source,omitempty"`
	Status                   string    `json:"status"`
	Estimated                bool      `json:"estimated"`
	ReservedInputTokens      uint64    `json:"reserved_input_tokens"`
	ReservedOutputTokens     uint64    `json:"reserved_output_tokens"`
	InputTokens              uint64    `json:"input_tokens"`
	OutputTokens             uint64    `json:"output_tokens"`
	CacheReadInputTokens     uint64    `json:"cache_read_input_tokens"`
	CacheCreationInputTokens uint64    `json:"cache_creation_input_tokens"`
	CacheCreationEphemeral1h uint64    `json:"cache_creation_ephemeral_1h_input_tokens"`
	CacheCreationEphemeral5m uint64    `json:"cache_creation_ephemeral_5m_input_tokens"`
	TotalTokens              uint64    `json:"total_tokens"`
	ErrorCode                string    `json:"error_code,omitempty"`
	ErrorMessage             string    `json:"error_message,omitempty"`
	StartedAt                time.Time `json:"started_at"`
	FinishedAt               time.Time `json:"finished_at,omitempty"`
	CreatedAt                time.Time `json:"created_at,omitempty"`
	UpdatedAt                time.Time `json:"updated_at,omitempty"`
}

type Event struct {
	ID           uint64    `json:"id,omitempty"`
	TenantID     uint64    `json:"tenant_id"`
	UserID       uint64    `json:"user_id,omitempty"`
	RequestID    string    `json:"request_id,omitempty"`
	EventType    string    `json:"event_type"`
	LimitType    string    `json:"limit_type,omitempty"`
	LimitValue   uint64    `json:"limit_value,omitempty"`
	CurrentValue uint64    `json:"current_value,omitempty"`
	Source       string    `json:"source,omitempty"`
	Route        string    `json:"route,omitempty"`
	Model        string    `json:"model,omitempty"`
	TraceID      string    `json:"trace_id,omitempty"`
	MetadataJSON string    `json:"metadata_json,omitempty"`
	CreatedAt    time.Time `json:"created_at,omitempty"`
}

type ListFilter struct {
	From   time.Time
	To     time.Time
	Source string
	Model  string
	Limit  int
	Cursor uint64
	Search string
}

type CounterStore interface {
	Reserve(ctx context.Context, cfg Config, req ReserveRequest, usageDate time.Time) error
	Settle(ctx context.Context, reservation Reservation, usage Usage) error
}

type MemoryStore struct {
	mu      sync.Mutex
	now     func() time.Time
	tenants map[uint64]*tenantCounters
}

type tenantCounters struct {
	secondWindow int64
	secondCount  uint64
	day          string
	messages     uint64
	tokens       uint64
	concurrent   uint64
}

func NewMemoryStore(now func() time.Time) *MemoryStore {
	if now == nil {
		now = time.Now
	}
	return &MemoryStore{now: now, tenants: make(map[uint64]*tenantCounters)}
}

func (s *MemoryStore) Reserve(_ context.Context, cfg Config, req ReserveRequest, usageDate time.Time) error {
	if s == nil || !cfg.QuotaEnabled {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	counters := s.counters(cfg.TenantID)
	now := s.now()
	second := now.Unix()
	if counters.secondWindow != second {
		counters.secondWindow = second
		counters.secondCount = 0
	}
	day := usageDate.Format("2006-01-02")
	if counters.day != day {
		counters.day = day
		counters.messages = 0
		counters.tokens = 0
	}
	reservedTokens := req.EstimatedInputTokens + req.ReservedOutputTokens
	if cfg.QPSLimit != nil && counters.secondCount >= *cfg.QPSLimit {
		return ErrRateLimited
	}
	if cfg.DailyMessageLimit != nil && counters.messages+1 > *cfg.DailyMessageLimit {
		return ErrDailyMessageLimitExceeded
	}
	if cfg.DailyTokenLimit != nil && counters.tokens+reservedTokens > *cfg.DailyTokenLimit {
		return ErrDailyTokenLimitExceeded
	}
	if cfg.MaxConcurrentRequests != nil && counters.concurrent >= *cfg.MaxConcurrentRequests {
		return ErrConcurrentLimitExceeded
	}
	counters.secondCount++
	counters.messages++
	counters.tokens += reservedTokens
	counters.concurrent++
	return nil
}

func (s *MemoryStore) Settle(_ context.Context, reservation Reservation, usage Usage) error {
	if s == nil || !reservation.QuotaEnabled {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	counters := s.counters(reservation.TenantID)
	if counters.concurrent > 0 {
		counters.concurrent--
	}
	reserved := reservation.ReservedInputTokens + reservation.ReservedOutputTokens
	actual := usage.TotalTokens()
	if actual < reserved {
		counters.tokens -= minUint64(counters.tokens, reserved-actual)
	} else if actual > reserved {
		counters.tokens += actual - reserved
	}
	return nil
}

func (s *MemoryStore) counters(tenantID uint64) *tenantCounters {
	item := s.tenants[tenantID]
	if item == nil {
		item = &tenantCounters{}
		s.tenants[tenantID] = item
	}
	return item
}

func NormalizeConfig(input ConfigInput) (Config, error) {
	cfg := Config{
		TenantID:              input.TenantID,
		QuotaEnabled:          input.QuotaEnabled,
		QPSLimit:              cloneLimit(input.QPSLimit),
		DailyTokenLimit:       cloneLimit(input.DailyTokenLimit),
		DailyMessageLimit:     cloneLimit(input.DailyMessageLimit),
		MaxConcurrentRequests: cloneLimit(input.MaxConcurrentRequests),
		Timezone:              strings.TrimSpace(input.Timezone),
		ReserveOutputTokens:   input.ReserveOutputTokens,
		Status:                strings.TrimSpace(input.Status),
		UpdatedByUserID:       input.UpdatedByUserID,
	}
	if cfg.TenantID == 0 {
		return Config{}, fmt.Errorf("%w: tenant id is required", ErrInvalidConfig)
	}
	for _, limit := range []*uint64{cfg.QPSLimit, cfg.DailyTokenLimit, cfg.DailyMessageLimit, cfg.MaxConcurrentRequests} {
		if limit != nil && *limit == 0 {
			return Config{}, fmt.Errorf("%w: limits must be positive or null", ErrInvalidConfig)
		}
	}
	if cfg.Timezone == "" {
		cfg.Timezone = "UTC"
	}
	if _, err := time.LoadLocation(cfg.Timezone); err != nil {
		return Config{}, fmt.Errorf("%w: invalid timezone %q", ErrInvalidConfig, cfg.Timezone)
	}
	if cfg.ReserveOutputTokens == 0 {
		cfg.ReserveOutputTokens = 4096
	}
	if cfg.Status == "" {
		cfg.Status = "active"
	}
	if cfg.Status != "active" && cfg.Status != "disabled" {
		return Config{}, fmt.Errorf("%w: unsupported status %q", ErrInvalidConfig, cfg.Status)
	}
	return cfg, nil
}

func UsageDate(now time.Time, timezone string) (time.Time, error) {
	if timezone == "" {
		timezone = "UTC"
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, err
	}
	local := now.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc), nil
}

func DefaultConfig(tenantID uint64) Config {
	return Config{
		TenantID:            tenantID,
		QuotaEnabled:        false,
		Timezone:            "UTC",
		ReserveOutputTokens: 4096,
		Status:              "active",
	}
}

func RejectionEventType(err error) string {
	switch {
	case errors.Is(err, ErrRateLimited):
		return "quota.rejected.qps"
	case errors.Is(err, ErrDailyTokenLimitExceeded):
		return "quota.rejected.daily_tokens"
	case errors.Is(err, ErrDailyMessageLimitExceeded):
		return "quota.rejected.daily_messages"
	case errors.Is(err, ErrConcurrentLimitExceeded):
		return "quota.rejected.concurrent"
	default:
		return "quota.rejected"
	}
}

func RejectionLimitType(err error) string {
	switch {
	case errors.Is(err, ErrRateLimited):
		return "qps"
	case errors.Is(err, ErrDailyTokenLimitExceeded):
		return "daily_tokens"
	case errors.Is(err, ErrDailyMessageLimitExceeded):
		return "daily_messages"
	case errors.Is(err, ErrConcurrentLimitExceeded):
		return "concurrent"
	default:
		return "unknown"
	}
}

func cloneLimit(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func minUint64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}
