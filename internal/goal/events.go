package goal

import (
	"strings"
	"time"
)

type EventType string

const (
	EventGoalStarted   EventType = "goal_started"
	EventGoalStopped   EventType = "goal_stopped"
	EventGoalResumed   EventType = "goal_resumed"
	EventTurnStarted   EventType = "turn_started"
	EventTurnFinished  EventType = "turn_finished"
	EventTurnFailed    EventType = "turn_failed"
	EventStatusChanged EventType = "status_changed"
)

type Event struct {
	ID           string    `json:"id"`
	GoalID       string    `json:"goal_id"`
	Type         EventType `json:"type"`
	Message      string    `json:"message,omitempty"`
	SessionID    string    `json:"session_id,omitempty"`
	Turn         int       `json:"turn,omitempty"`
	InputTokens  int       `json:"input_tokens,omitempty"`
	OutputTokens int       `json:"output_tokens,omitempty"`
	DurationMS   int64     `json:"duration_ms,omitempty"`
	Checkpoint   string    `json:"checkpoint,omitempty"`
	Status       Status    `json:"status,omitempty"`
	Reason       string    `json:"reason,omitempty"`
	NextAction   string    `json:"next_action,omitempty"`
	BlockerKey   string    `json:"blocker_key,omitempty"`
	Error        string    `json:"error,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type EventInput struct {
	GoalID       string
	Type         EventType
	Message      string
	SessionID    string
	Turn         int
	InputTokens  int
	OutputTokens int
	DurationMS   int64
	Checkpoint   string
	Status       Status
	Reason       string
	NextAction   string
	BlockerKey   string
	Error        string
	Now          time.Time
}

func NewEvent(input EventInput) (Event, error) {
	id, err := newPrefixedID("evt", defaultEventIDByteLength)
	if err != nil {
		return Event{}, err
	}
	now := input.Now
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	return Event{
		ID:           id,
		GoalID:       strings.TrimSpace(input.GoalID),
		Type:         input.Type,
		Message:      strings.TrimSpace(input.Message),
		SessionID:    strings.TrimSpace(input.SessionID),
		Turn:         input.Turn,
		InputTokens:  input.InputTokens,
		OutputTokens: input.OutputTokens,
		DurationMS:   input.DurationMS,
		Checkpoint:   strings.TrimSpace(input.Checkpoint),
		Status:       input.Status,
		Reason:       strings.TrimSpace(input.Reason),
		NextAction:   strings.TrimSpace(input.NextAction),
		BlockerKey:   strings.TrimSpace(input.BlockerKey),
		Error:        strings.TrimSpace(input.Error),
		CreatedAt:    now,
	}, nil
}
