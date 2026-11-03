package channel

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

const (
	maxInboundErrorCodeBytes = 128
	defaultInboundIgnoreCode = "policy_denied"
)

var (
	errInvalidInboundDisposition = errors.New("channel: invalid inbound disposition")
	errInvalidInboundErrorCode   = errors.New("channel: invalid inbound error code")
)

type InboundDisposition struct {
	Ack       bool
	Retryable bool
	ErrorCode string
}

// Validate ensures exactly one disposition is selected and error codes are safe,
// bounded machine-readable identifiers rather than provider text or secrets.
func (d InboundDisposition) Validate() error {
	if d.Ack == d.Retryable {
		return errInvalidInboundDisposition
	}
	if d.Ack && d.ErrorCode == "" {
		return nil
	}
	if !isSafeInboundErrorCode(d.ErrorCode) {
		return errInvalidInboundErrorCode
	}
	return nil
}

func isSafeInboundErrorCode(code string) bool {
	if code == "" || len(code) > maxInboundErrorCodeBytes {
		return false
	}
	if !isInboundErrorCodeAlphaNumeric(code[0]) {
		return false
	}
	for i := 1; i < len(code); i++ {
		value := code[i]
		if isInboundErrorCodeAlphaNumeric(value) || value == '_' || value == '.' || value == '-' {
			continue
		}
		return false
	}
	return true
}

func isInboundErrorCodeAlphaNumeric(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= '0' && value <= '9'
}

func AcceptInbound() InboundDisposition {
	return InboundDisposition{Ack: true}
}

func IgnoreInbound(code string) InboundDisposition {
	if code == "" {
		code = defaultInboundIgnoreCode
	}
	return InboundDisposition{Ack: true, ErrorCode: code}
}

func RetryInbound(code string) InboundDisposition {
	return InboundDisposition{Retryable: true, ErrorCode: code}
}

type InboundHandler func(context.Context, InboundMessage) InboundDisposition

type OutboundOperation struct {
	Operation DeliveryOperation
	Message   OutboundMessage
	MessageID string
}

type DeliveryReceipt struct {
	MessageID   string
	ChatID      string
	DeliveredAt time.Time
	RequestID   string
}

type ThreadSource string

const (
	ThreadSourceEvent            ThreadSource = "event"
	ThreadSourceRawMessageLookup ThreadSource = "raw_message_lookup"
	ThreadSourceNone             ThreadSource = "none"
)

func (s ThreadSource) Valid() bool {
	switch s {
	case ThreadSourceEvent, ThreadSourceRawMessageLookup, ThreadSourceNone:
		return true
	default:
		return false
	}
}

type ThreadResolution struct {
	ThreadID string
	Source   ThreadSource
}

type HealthStatus string

const (
	HealthStarting HealthStatus = "starting"
	HealthReady    HealthStatus = "ready"
	HealthDegraded HealthStatus = "degraded"
	HealthFailed   HealthStatus = "failed"
)

func (s HealthStatus) Valid() bool {
	switch s {
	case HealthStarting, HealthReady, HealthDegraded, HealthFailed:
		return true
	default:
		return false
	}
}

type Health struct {
	Status      HealthStatus
	ConnectedAt time.Time
	LastError   string
}

type Adapter interface {
	Provider() Provider
	AccountID() string
	Start(context.Context, InboundHandler) error
	Stop(context.Context) error
	Deliver(context.Context, OutboundOperation) (DeliveryReceipt, error)
	ResolveThread(context.Context, InboundMessage) (ThreadResolution, error)
	Capabilities() Capabilities
	Health(context.Context) Health
}

type CardStreamReceipt struct {
	MessageID string
	ChatID    string
}

type CardStream interface {
	Update(context.Context, json.RawMessage) error
	Close(context.Context) error
}

// StreamingAdapter is optional so providers without card update semantics can
// keep implementing Adapter and use the final-card fallback.
type StreamingAdapter interface {
	OpenCardStream(context.Context, OutboundMessage) (CardStreamReceipt, CardStream, error)
}
