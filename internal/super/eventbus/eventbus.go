// Package eventbus defines the durable notification contract. It owns neither
// task execution nor authorization: callers supply a trusted principal scope.
package eventbus

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

const (
	LocalScope              = "local"
	EventVersion            = 1
	TopicWorkOrderRecorded  = "work_order.recorded"
	TopicWorkOrderCancelled = "work_order.cancelled"
	MaxPayloadBytes         = 16 * 1024
	MaxKeyBytes             = 256
	MaxTopicBytes           = 128
	MaxJSONDepth            = 32
	MaxReadLimit            = 100
	MaxTopics               = 32
	MaxSubscriptions        = 256
	MaxRetainedEvents       = 100000 // Soft admission limit; existing cancellation obligations survive it.
	MaxAttempts             = 10
	MaxLease                = 5 * time.Minute
	MaxRetryDelay           = 24 * time.Hour
	CodeConsumerFailed      = "consumer_failed"
	CodeLeaseExpired        = "lease_expired"
	CodeUnavailable         = "unavailable"
	CodeInvalidPayload      = "invalid_payload"
)

type StartPosition string

const (
	StartBeginning StartPosition = "beginning"
	StartNow       StartPosition = "now"
)

var (
	ErrInvalidInput = errors.New("event bus invalid input")
	ErrConflict     = errors.New("event bus identity or configuration conflict")
	ErrCapacity     = errors.New("event bus admission capacity reached")
	ErrNotFound     = errors.New("event bus subscription not found")
	ErrLeaseLost    = errors.New("event bus delivery lease is no longer valid")
	ErrUnavailable  = errors.New("event bus storage unavailable")
)

type Event struct {
	Sequence     int64           `json:"sequence"`
	ID           string          `json:"id"`
	Scope        string          `json:"scope"`
	Topic        string          `json:"topic"`
	AggregateID  string          `json:"aggregate_id"`
	Version      int             `json:"version"`
	Payload      json.RawMessage `json:"payload"`
	OccurredAtMS int64           `json:"occurred_at_ms"`
}
type Subscription struct {
	Scope       string        `json:"scope"`
	Name        string        `json:"name"`
	Topics      []string      `json:"topics"`
	Start       StartPosition `json:"start"`
	MaxAttempts int           `json:"max_attempts"`
}

// DeliveryToken is an opaque capability, not a caller-selected cursor. Never log
// Token. ACK/NACK require every field to match the live, unexpired lease.
type DeliveryToken struct {
	Scope        string `json:"scope"`
	Name         string `json:"name"`
	Sequence     int64  `json:"sequence"`
	Owner        string `json:"owner"`
	Token        string `json:"token"`
	Epoch        int64  `json:"epoch"`
	LeaseUntilMS int64  `json:"lease_until_ms"`
}
type Delivery struct {
	Event   Event         `json:"event"`
	Token   DeliveryToken `json:"delivery_token"`
	Attempt int           `json:"attempt"`
}
type DeadLetter struct {
	Scope       string `json:"scope"`
	Subscriber  string `json:"subscriber"`
	Event       Event  `json:"event"`
	Attempts    int    `json:"attempts"`
	Code        string `json:"code"`
	CreatedAtMS int64  `json:"created_at_ms"`
}

// Options configures a view over the same durable store, not another authority.
// Now must be concurrency-safe. Omission uses the production UTC wall clock.
type Options struct{ Now func() time.Time }

type Store interface {
	AppendEvent(context.Context, Event) (Event, bool, error)
	RegisterSubscription(context.Context, Subscription) error
	ClaimEvent(context.Context, string, string, string, time.Duration) (Delivery, bool, error)
	AckEvent(context.Context, DeliveryToken) error
	NackEvent(context.Context, DeliveryToken, string, time.Duration) error
	ReadEvents(context.Context, string, int64, int) ([]Event, error)
	ReadDeadLetters(context.Context, string, string, int) ([]DeadLetter, error)
}
