// Package execution defines isolated, capability-limited worker execution.
// It deliberately does not reference legacy interactive Session ownership.
package execution

import (
	"context"
	"errors"
)

const (
	ToolRead        = "super_read"
	ToolWrite       = "super_write"
	MaxResultBytes  = 16384
	MaxInputBytes   = 65536
	MaxFileBytes    = 262144
	MaxTurns        = 12
	MaxOutputTokens = 8192
)

var (
	ErrUnavailable = errors.New("execution configuration unavailable")
	ErrAuthority   = errors.New("execution authority revoked")
	ErrBudget      = errors.New("execution budget exhausted")
	ErrInvalid     = errors.New("invalid execution request")
	ErrFailed      = errors.New("execution failed")
)

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Reserve is called before EACH actual model request, including tool-loop turns.
// An uncertain/absent usage report never releases its reservation.
type Budget interface {
	Reserve(context.Context, string, int, int) error
	Settle(context.Context, string, Usage, bool) error
}

type Dependency struct {
	NodeID string `json:"node_id"`
	Result string `json:"result"`
}

type Request struct {
	RunID, JobID, AttemptID                         string
	Workspace, Objective, Instruction, SystemPrompt string
	Provider, Model                                 string
	ReadPaths, WritePaths                           []string
	Dependencies                                    []Dependency
	MaxTurns, MaxOutputTokens                       int
	CheckAuthority                                  func(context.Context) error
	Budget                                          Budget
}

// WriteReceipt is produced by the trusted file tool after writing; never parsed from model text.
type WriteReceipt struct {
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	Bytes     int64  `json:"bytes"`
	AttemptID string `json:"attempt_id"`
}

// Diagnostic contains only safe enums and bounded numeric metadata. It never
// includes URLs, headers, response text, provider error strings or credentials.
type Diagnostic struct {
	BodyState      BodyState      `json:"body_state,omitempty"`
	FailureStage   FailureStage   `json:"failure_stage,omitempty"`
	CompletionKind CompletionKind `json:"completion_kind,omitempty"`
	ContentKind    ContentKind    `json:"content_kind,omitempty"`
	Code           ErrorCode      `json:"code"`
	HTTPStatus     int            `json:"http_status,omitempty"`
	ResponseBytes  int64          `json:"response_bytes,omitempty"`
	MediaType      MediaType      `json:"media_type,omitempty"`
	UsageState     UsageState     `json:"usage_state,omitempty"`
	RequestIndex   int            `json:"request_index,omitempty"`
}

type Result struct {
	Diagnostic *Diagnostic    `json:"diagnostic,omitempty"`
	Writes     []WriteReceipt `json:"writes,omitempty"`
	Text       string         `json:"text"`
	Usage      Usage          `json:"usage"`
	UsageKnown bool           `json:"usage_known"`
	ToolCalls  int            `json:"tool_calls"`
}

type Executor interface {
	Execute(context.Context, Request) (Result, error)
}
