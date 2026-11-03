package computeruse

import (
	"context"
	"errors"
	"sync"
	"time"
)

// FakeBackend is a deterministic backend for contract and session tests. It
// deliberately records only action metadata and never persists sensitive text.
type FakeBackend struct {
	mu                sync.Mutex
	CapabilitiesValue Capabilities
	ObservationValue  Observation
	ReceiptValue      ActionReceipt
	ExecuteError      error
	ObserveError      error
	Paused            bool
	Stopped           bool
	Executed          []Action
}

func (b *FakeBackend) Capabilities(context.Context) (Capabilities, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.CapabilitiesValue, nil
}

func (b *FakeBackend) Observe(_ context.Context, request ObserveRequest) (Observation, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ObserveError != nil {
		return Observation{}, b.ObserveError
	}
	observation := b.ObservationValue
	if observation.SessionID == "" {
		observation.SessionID = request.SessionID
	}
	if observation.ObservedAt.IsZero() {
		observation.ObservedAt = time.Now()
	}
	return observation, nil
}

func (b *FakeBackend) Execute(_ context.Context, action Action) (ActionReceipt, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.Stopped {
		return ActionReceipt{ActionID: action.ID, SessionID: action.SessionID, Outcome: OutcomeRejected}, errors.New("fake backend stopped")
	}
	if b.Paused {
		return ActionReceipt{ActionID: action.ID, SessionID: action.SessionID, Outcome: OutcomeRejected}, errors.New("fake backend paused")
	}
	b.Executed = append(b.Executed, Action{ID: action.ID, SessionID: action.SessionID, ObservationID: action.ObservationID, Kind: action.Kind})
	if b.ExecuteError != nil {
		return b.ReceiptValue, b.ExecuteError
	}
	receipt := b.ReceiptValue
	if receipt.ActionID == "" {
		receipt.ActionID = action.ID
	}
	if receipt.SessionID == "" {
		receipt.SessionID = action.SessionID
	}
	if receipt.Outcome == "" {
		receipt.Outcome = OutcomeExecuted
	}
	return receipt, nil
}

func (b *FakeBackend) Pause(context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Paused = true
	return nil
}
func (b *FakeBackend) Resume(context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Paused = false
	return nil
}
func (b *FakeBackend) Stop(context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Stopped = true
	return nil
}
func (b *FakeBackend) Close(context.Context) error { return nil }
