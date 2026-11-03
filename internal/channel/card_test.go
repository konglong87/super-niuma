package channel

import "testing"

func TestCardStateTerminal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status CardStatus
		want   bool
	}{
		{status: CardRunning, want: false},
		{status: CardCompleted, want: true},
		{status: CardCancelled, want: true},
		{status: CardInterrupted, want: true},
		{status: CardFailed, want: true},
	}
	for _, tt := range tests {
		if got := tt.status.Terminal(); got != tt.want {
			t.Fatalf("%s.Terminal() = %v, want %v", tt.status, got, tt.want)
		}
	}
}

func TestCardStatusValid(t *testing.T) {
	t.Parallel()

	for _, status := range []CardStatus{CardRunning, CardCompleted, CardCancelled, CardInterrupted, CardFailed} {
		if !status.Valid() {
			t.Fatalf("%q should be valid", status)
		}
	}
	for _, status := range []CardStatus{"", "unknown"} {
		if status.Valid() {
			t.Fatalf("%q should be invalid", status)
		}
	}
}

func TestCanTransitionCardStateRejectsLateDelta(t *testing.T) {
	t.Parallel()

	if !CanTransitionCardState(CardRunning, CardCompleted) {
		t.Fatal("running -> completed should be allowed")
	}
	if CanTransitionCardState(CardCompleted, CardRunning) {
		t.Fatal("completed -> running should be rejected")
	}
	if CanTransitionCardState(CardCancelled, CardCompleted) {
		t.Fatal("cancelled -> completed should be rejected")
	}
	if CanTransitionCardState(CardCompleted, CardCompleted) != true {
		t.Fatal("completed -> completed should be an idempotent no-op")
	}
	if CanTransitionCardState(CardCancelled, CardCancelled) != true {
		t.Fatal("cancelled -> cancelled should be an idempotent no-op")
	}
	if CanTransitionCardState(CardInterrupted, CardInterrupted) != true {
		t.Fatal("interrupted -> interrupted should be an idempotent no-op")
	}
	if CanTransitionCardState(CardFailed, CardFailed) != true {
		t.Fatal("failed -> failed should be an idempotent no-op")
	}
	if CanTransitionCardState(CardCompleted, CardFailed) {
		t.Fatal("completed -> failed should be rejected")
	}
	if CanTransitionCardState(CardStatus("unknown"), CardRunning) {
		t.Fatal("unknown -> running should be rejected")
	}
	if CanTransitionCardState(CardRunning, CardStatus("unknown")) {
		t.Fatal("running -> unknown should be rejected")
	}
	if !CanTransitionCardState(CardStatus(""), CardRunning) {
		t.Fatal("empty -> running should initialize a zero CardState")
	}
	if CanTransitionCardState(CardStatus(""), CardCompleted) {
		t.Fatal("empty -> completed should be rejected")
	}
}
