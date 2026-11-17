package phone

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Approval is one step waiting for the person.
type Approval struct {
	ID      string `json:"id"`
	Risk    Risk   `json:"risk"`
	What    string `json:"what"`   // plain-language action, e.g. tap "Delete"
	App     string `json:"app"`    // foreground package
	Intent  string `json:"intent"` // the agent's stated reason
	Expires int64  `json:"expires_ms"`
}

// Decision is the person's answer.
type Decision string

const (
	Allow Decision = "allow_once"
	Deny  Decision = "deny"
)

var (
	ErrNoApproval    = errors.New("no such pending approval")
	ErrBadDecision   = errors.New("decision must be allow_once or deny")
	ErrApprovalTaken = errors.New("an approval is already pending")
)

// Approvals holds at most one pending approval. Only the app's own screens
// call Decide; the agent has no path to it.
type Approvals struct {
	mu      sync.Mutex
	timeout time.Duration
	seq     int
	pending *Approval
	answer  chan Decision
	changed func()
}

func NewApprovals(timeout time.Duration, changed func()) *Approvals {
	if changed == nil {
		changed = func() {}
	}
	return &Approvals{timeout: timeout, changed: changed}
}

// Ask blocks until the person decides, the timeout passes (deny) or ctx ends.
func (a *Approvals) Ask(ctx context.Context, approval Approval) (Decision, error) {
	a.mu.Lock()
	if a.pending != nil {
		a.mu.Unlock()
		return Deny, ErrApprovalTaken
	}
	a.seq++
	approval.ID = fmt.Sprintf("ap-%d-%d", time.Now().UnixMilli(), a.seq)
	approval.Expires = time.Now().Add(a.timeout).UnixMilli()
	answer := make(chan Decision, 1)
	a.pending, a.answer = &approval, answer
	a.mu.Unlock()
	a.changed()
	defer func() {
		a.mu.Lock()
		if a.answer == answer {
			a.pending, a.answer = nil, nil
		}
		a.mu.Unlock()
		a.changed()
	}()
	timer := time.NewTimer(a.timeout)
	defer timer.Stop()
	var err error
	select {
	case d := <-answer:
		return d, nil
	case <-timer.C:
	case <-ctx.Done():
		err = ctx.Err()
	}
	// Close the approval before deciding the outcome, so a late Decide is
	// refused instead of reported as accepted; one that won the race counts.
	a.mu.Lock()
	if a.answer == answer {
		a.pending, a.answer = nil, nil
	}
	a.mu.Unlock()
	select {
	case d := <-answer:
		if err == nil {
			return d, nil
		}
	default:
	}
	return Deny, err
}

// Decide answers the pending approval with id. The first answer wins.
func (a *Approvals) Decide(id string, d Decision) error {
	if d != Allow && d != Deny {
		return ErrBadDecision
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pending == nil || a.pending.ID != id {
		return ErrNoApproval
	}
	select {
	case a.answer <- d:
		return nil
	default:
		return ErrNoApproval
	}
}

// Pending returns a copy of the pending approval, or nil.
func (a *Approvals) Pending() *Approval {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pending == nil {
		return nil
	}
	p := *a.pending
	return &p
}
