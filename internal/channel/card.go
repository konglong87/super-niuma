package channel

import (
	"encoding/json"
	"fmt"
)

type CardStatus string

type ToolDetailsMode string

const (
	ToolStatusRunning  = "running"
	ToolStatusComplete = "complete"
	ToolStatusFailed   = "failed"
)

const (
	ToolDetailsPreview ToolDetailsMode = "preview"
	ToolDetailsOff     ToolDetailsMode = "off"
)

func ParseToolDetailsMode(value string) (ToolDetailsMode, error) {
	if value == "" {
		return ToolDetailsPreview, nil
	}
	mode := ToolDetailsMode(value)
	if mode != ToolDetailsPreview && mode != ToolDetailsOff {
		return "", fmt.Errorf("invalid channel tool details mode %q; use off or preview", value)
	}
	return mode, nil
}

func (m ToolDetailsMode) Normalize() ToolDetailsMode {
	if m == ToolDetailsOff {
		return ToolDetailsOff
	}
	return ToolDetailsPreview
}

const (
	CardRunning      CardStatus = "running"
	CardWaitingInput CardStatus = "waiting_input"
	CardCompleted    CardStatus = "completed"
	CardCancelled    CardStatus = "cancelled"
	CardInterrupted  CardStatus = "interrupted"
	CardFailed       CardStatus = "failed"
)

func (s CardStatus) Terminal() bool {
	switch s {
	case CardCompleted, CardCancelled, CardInterrupted, CardFailed:
		return true
	default:
		return false
	}
}

func (s CardStatus) Valid() bool {
	switch s {
	case CardRunning, CardWaitingInput, CardCompleted, CardCancelled, CardInterrupted, CardFailed:
		return true
	default:
		return false
	}
}

func CanTransitionCardState(from, to CardStatus) bool {
	if from == "" {
		return to == CardRunning
	}
	if from.Terminal() {
		return from == to
	}
	if from != CardRunning && from != CardWaitingInput {
		return false
	}
	switch to {
	case CardRunning, CardWaitingInput, CardCompleted, CardCancelled, CardInterrupted, CardFailed:
		return true
	default:
		return false
	}
}

type ToolProgress struct {
	ID              string `json:"id,omitempty"`
	Name            string `json:"name"`
	Status          string `json:"status"`
	Command         string `json:"command,omitempty"`
	OutputPreview   string `json:"output_preview,omitempty"`
	IsError         bool   `json:"is_error,omitempty"`
	OutputTruncated bool   `json:"output_truncated,omitempty"`
}

// TimelineEntryKind identifies a display event without coupling it to Feishu.
type TimelineEntryKind string

const (
	TimelineText   TimelineEntryKind = "text"
	TimelineTool   TimelineEntryKind = "tool"
	TimelineNotice TimelineEntryKind = "notice"
)

// TimelineEntry preserves accepted channel output in production order. Tool
// results update the matching tool entry instead of appending a second event.
type TimelineEntry struct {
	ID   string            `json:"id,omitempty"`
	Kind TimelineEntryKind `json:"kind"`
	Text string            `json:"text,omitempty"`
	Tool ToolProgress      `json:"tool,omitempty"`
}

type CardState struct {
	RunID         string
	Status        CardStatus
	Text          string
	Tools         []ToolProgress
	Timeline      []TimelineEntry
	Question      *InteractionQuestion
	RenderVersion uint64
	// MinimumPageCount keeps already-opened provider messages addressable when
	// a streaming text amendment makes the current timeline shorter.
	MinimumPageCount int
}

// RenderedCardPage is one stable provider message in a paged card response.
type RenderedCardPage struct {
	Index int
	Card  json.RawMessage
}

type CardRenderer interface {
	RenderFinal(CardState) (json.RawMessage, error)
	RenderStreaming(CardState) (json.RawMessage, error)
}
