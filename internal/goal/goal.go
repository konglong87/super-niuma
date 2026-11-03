package goal

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/identity"
)

type Status string

const (
	StatusActive   Status = "active"
	StatusComplete Status = "complete"
	StatusBlocked  Status = "blocked"
	StatusStopped  Status = "stopped"
	StatusFailed   Status = "failed"
)

const (
	DefaultTurnBudget        = 20
	DefaultTokenBudget       = 200000
	DefaultBlockedThreshold  = 3
	DefaultRecentEventLimit  = 12
	defaultGoalIDByteLength  = 12
	defaultEventIDByteLength = 12
)

type Goal struct {
	ID                   string    `json:"id"`
	Objective            string    `json:"objective"`
	Status               Status    `json:"status"`
	SessionID            string    `json:"session_id"`
	CWD                  string    `json:"cwd"`
	Model                string    `json:"model,omitempty"`
	Provider             string    `json:"provider,omitempty"`
	TurnBudget           int       `json:"turn_budget"`
	TokenBudget          int       `json:"token_budget"`
	TurnsUsed            int       `json:"turns_used"`
	InputTokens          int       `json:"input_tokens"`
	OutputTokens         int       `json:"output_tokens"`
	LastBlocker          string    `json:"last_blocker,omitempty"`
	RepeatedBlockerCount int       `json:"repeated_blocker_count,omitempty"`
	LastCheckpoint       string    `json:"last_checkpoint,omitempty"`
	LastReason           string    `json:"last_reason,omitempty"`
	LastNextAction       string    `json:"last_next_action,omitempty"`
	Error                string    `json:"error,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type CreateInput struct {
	Objective   string
	SessionID   string
	CWD         string
	Model       string
	Provider    string
	TurnBudget  int
	TokenBudget int
	Now         time.Time
}

type ListFilter struct {
	Status Status
	Active bool
}

func (s Status) Valid() bool {
	switch s {
	case StatusActive, StatusComplete, StatusBlocked, StatusStopped, StatusFailed:
		return true
	default:
		return false
	}
}

func (s Status) Terminal() bool {
	switch s {
	case StatusComplete, StatusStopped, StatusFailed:
		return true
	default:
		return false
	}
}

func NormalizeCreateInput(input CreateInput) (CreateInput, error) {
	input.Objective = strings.TrimSpace(input.Objective)
	input.SessionID = strings.TrimSpace(input.SessionID)
	input.CWD = strings.TrimSpace(input.CWD)
	input.Model = strings.TrimSpace(input.Model)
	if input.Objective == "" {
		return input, errors.New("goal objective is required")
	}
	if input.CWD == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return input, err
		}
		input.CWD = cwd
	}
	if input.TurnBudget <= 0 {
		input.TurnBudget = DefaultTurnBudget
	}
	if input.TokenBudget <= 0 {
		input.TokenBudget = DefaultTokenBudget
	}
	if input.Now.IsZero() {
		input.Now = time.Now().UTC()
	} else {
		input.Now = input.Now.UTC()
	}
	return input, nil
}

func NewGoal(input CreateInput) (Goal, error) {
	input, err := NormalizeCreateInput(input)
	if err != nil {
		return Goal{}, err
	}
	id, err := newPrefixedID("goal", defaultGoalIDByteLength)
	if err != nil {
		return Goal{}, err
	}
	return Goal{
		ID:          id,
		Objective:   input.Objective,
		Status:      StatusActive,
		SessionID:   input.SessionID,
		CWD:         input.CWD,
		Model:       input.Model,
		Provider:    input.Provider,
		TurnBudget:  input.TurnBudget,
		TokenBudget: input.TokenBudget,
		CreatedAt:   input.Now,
		UpdatedAt:   input.Now,
	}, nil
}

func (g Goal) Validate() error {
	if strings.TrimSpace(g.ID) == "" {
		return errors.New("goal id is required")
	}
	if strings.TrimSpace(g.Objective) == "" {
		return errors.New("goal objective is required")
	}
	if !g.Status.Valid() {
		return fmt.Errorf("invalid goal status: %s", g.Status)
	}
	if g.TurnBudget <= 0 {
		return errors.New("goal turn budget must be positive")
	}
	if g.TokenBudget <= 0 {
		return errors.New("goal token budget must be positive")
	}
	return nil
}

func DefaultRoot() string {
	root, err := config.CurrentIdentity("").GlobalConfigRoot()
	if err == nil && root != "" {
		return root
	}
	return "."
}

func LegacyRoot() string {
	root, err := identity.LegacyOwnedGlobalConfigRoot()
	if err == nil {
		return root
	}
	return ""
}

func newPrefixedID(prefix string, n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(buf), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
