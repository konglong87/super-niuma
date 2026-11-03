package imagegen

import (
	"context"
	"errors"
	"strings"
	"time"
)

const (
	DefaultCompletionPollInterval = 500 * time.Millisecond
	DefaultCompletionLease        = 30 * time.Second
	DefaultCompletionRetryDelay   = 2 * time.Second
	DefaultCompletionMaxAttempts  = 10
	DefaultCompletionBatchSize    = 32

	CompletionErrorOriginNotReady    = "origin_not_ready"
	CompletionErrorDispatchFailed    = "completion_dispatch_failed"
	CompletionErrorAttemptsExhausted = "completion_attempts_exhausted"
)

var (
	ErrInvalidCompletionDispatcher = errors.New("invalid completion dispatcher configuration")
	ErrCompletionOriginNotReady    = errors.New("image completion origin is not ready")
)

// CompletionEvent is a leased durable event. The consumer must atomically
// materialize its downstream effects and mark this event sent.
type CompletionEvent struct {
	ID               uint64     `json:"id"`
	TenantID         uint64     `json:"tenant_id"`
	GenerationID     string     `json:"generation_id"`
	EventType        string     `json:"event_type"`
	OriginType       string     `json:"origin_type"`
	OriginRefJSON    string     `json:"origin_ref_json,omitempty"`
	IdempotencyKey   string     `json:"idempotency_key"`
	Status           string     `json:"status"`
	Attempts         uint       `json:"attempts"`
	NextAttemptAt    *time.Time `json:"next_attempt_at,omitempty"`
	LeaseOwner       string     `json:"lease_owner,omitempty"`
	LeaseUntil       *time.Time `json:"lease_until,omitempty"`
	LastErrorCode    string     `json:"last_error_code,omitempty"`
	LastErrorMessage string     `json:"last_error_message,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	SentAt           *time.Time `json:"sent_at,omitempty"`
}

type CompletionDispatcherRepository interface {
	ClaimDueImageCompletionEvents(context.Context, uint64, string, int, time.Time) ([]CompletionEvent, error)
	MarkImageCompletionEventRetry(context.Context, uint64, uint64, string, time.Time, string, string) error
	MarkImageCompletionEventDead(context.Context, uint64, uint64, string, string, string) error
}

type ImageCompletionConsumer interface {
	ConsumeImageCompletion(context.Context, CompletionEvent) error
}

type CompletionDispatcherConfig struct {
	Repository   CompletionDispatcherRepository
	Consumer     ImageCompletionConsumer
	TenantID     uint64
	WorkerID     string
	PollInterval time.Duration
	Lease        time.Duration
	RetryDelay   time.Duration
	MaxAttempts  uint
	BatchSize    int
	Now          func() time.Time
}

type CompletionDispatcher struct{ cfg CompletionDispatcherConfig }

type permanentCompletionError struct {
	code string
	err  error
}

func (e *permanentCompletionError) Error() string { return e.err.Error() }
func (e *permanentCompletionError) Unwrap() error { return e.err }

func PermanentCompletionError(code string, err error) error {
	if err == nil {
		err = errors.New("permanent completion error")
	}
	return &permanentCompletionError{code: strings.TrimSpace(code), err: err}
}

func NewCompletionDispatcher(cfg CompletionDispatcherConfig) *CompletionDispatcher {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = DefaultCompletionPollInterval
	}
	if cfg.Lease <= 0 {
		cfg.Lease = DefaultCompletionLease
	}
	if cfg.RetryDelay <= 0 {
		cfg.RetryDelay = DefaultCompletionRetryDelay
	}
	if cfg.MaxAttempts == 0 {
		cfg.MaxAttempts = DefaultCompletionMaxAttempts
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = DefaultCompletionBatchSize
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &CompletionDispatcher{cfg: cfg}
}

func (d *CompletionDispatcher) ProcessOnce(ctx context.Context) error {
	if err := d.validate(); err != nil {
		return err
	}
	now := d.cfg.Now().UTC()
	events, err := d.cfg.Repository.ClaimDueImageCompletionEvents(ctx, d.cfg.TenantID, strings.TrimSpace(d.cfg.WorkerID), d.cfg.BatchSize, now.Add(d.cfg.Lease))
	if err != nil {
		return err
	}
	for _, event := range events {
		if err := d.cfg.Consumer.ConsumeImageCompletion(ctx, event); err != nil {
			if transitionErr := d.handleFailure(ctx, event, now, err); transitionErr != nil {
				return transitionErr
			}
		}
	}
	return nil
}

func (d *CompletionDispatcher) Run(ctx context.Context) error {
	if err := d.validate(); err != nil {
		return err
	}
	for {
		if err := d.ProcessOnce(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		timer := time.NewTimer(d.cfg.PollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
		}
	}
}

func (d *CompletionDispatcher) validate() error {
	if d == nil || d.cfg.Repository == nil || d.cfg.Consumer == nil || d.cfg.TenantID == 0 || strings.TrimSpace(d.cfg.WorkerID) == "" {
		return ErrInvalidCompletionDispatcher
	}
	return nil
}

func (d *CompletionDispatcher) handleFailure(ctx context.Context, event CompletionEvent, now time.Time, cause error) error {
	var permanent *permanentCompletionError
	if errors.As(cause, &permanent) {
		code := permanent.code
		if code == "" {
			code = CompletionErrorDispatchFailed
		}
		return d.cfg.Repository.MarkImageCompletionEventDead(ctx, event.TenantID, event.ID, event.LeaseOwner, code, SanitizeErrorMessage(cause.Error()))
	}
	if event.Attempts >= d.cfg.MaxAttempts {
		return d.cfg.Repository.MarkImageCompletionEventDead(ctx, event.TenantID, event.ID, event.LeaseOwner, CompletionErrorAttemptsExhausted, SanitizeErrorMessage(cause.Error()))
	}
	code := CompletionErrorDispatchFailed
	if errors.Is(cause, ErrCompletionOriginNotReady) {
		code = CompletionErrorOriginNotReady
	}
	return d.cfg.Repository.MarkImageCompletionEventRetry(ctx, event.TenantID, event.ID, event.LeaseOwner, now.Add(d.cfg.RetryDelay), code, SanitizeErrorMessage(cause.Error()))
}
