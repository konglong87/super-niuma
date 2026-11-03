// Package workorder owns explicit intake only, independently of execution sessions.
package workorder

import (
	"context"
	"errors"
	"time"
)

type Status string
type StageStatus string

const (
	StatusRecorded         Status      = "recorded"
	StatusCancelled        Status      = "cancelled"
	NotStarted             StageStatus = "not_started"
	MaxTitleBytes                      = 200
	MaxObjectiveBytes                  = 8000
	MaxAcceptanceCriteria              = 20
	MaxCriterionBytes                  = 1000
	MaxWorkspaceBytes                  = 4096
	MaxIdempotencyKeyBytes             = 128
	ListLimit                          = 100
)

var (
	ErrInvalidInput        = errors.New("invalid work-order input")
	ErrNotFound            = errors.New("work order not found")
	ErrIdempotencyConflict = errors.New("idempotency key conflicts with an existing intake")
	ErrUnavailable         = errors.New("work-order service unavailable")
)

type CreateInput struct {
	Title              string   `json:"title"`
	Objective          string   `json:"objective"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	Workspace          string   `json:"workspace,omitempty"`
}

type WorkOrder struct {
	ID                 string      `json:"id"`
	Title              string      `json:"title"`
	Objective          string      `json:"objective"`
	AcceptanceCriteria []string    `json:"acceptance_criteria"`
	Workspace          string      `json:"workspace"`
	Status             Status      `json:"status"`
	ExecutionStatus    StageStatus `json:"execution_status"`
	VerificationStatus StageStatus `json:"verification_status"`
	DeliveryStatus     StageStatus `json:"delivery_status"`
	CreatedAt          time.Time   `json:"created_at"`
	UpdatedAt          time.Time   `json:"updated_at"`
}

// Repository owns atomic persistence: Create and Cancel must commit their event
// and aggregate together. A retry returns the current (possibly cancelled)
// aggregate, not a reconstructed original. List is bounded by ListLimit.
type Repository interface {
	Create(context.Context, string, WorkOrder) (WorkOrder, bool, error)
	Get(context.Context, string) (WorkOrder, error)
	List(context.Context, int) ([]WorkOrder, error)
	Cancel(context.Context, string, time.Time) (WorkOrder, error)
}
