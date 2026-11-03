package computeruse

import (
	"testing"
	"time"
)

func TestEventBusPublishesCommittedTurnWithoutReconstruction(t *testing.T) {
	bus := NewEventBus()
	updates, cancel := bus.Subscribe(1)
	defer cancel()
	result := validTurnResultForTest()
	result.Sequence = 7
	result.StartedAt = time.Now().Add(-time.Second)
	result.CompletedAt = time.Now()

	bus.PublishTurn(result)
	select {
	case got := <-updates:
		if got.TurnID != result.TurnID || got.Receipt.ActionID != result.Receipt.ActionID || got.Sequence != result.Sequence {
			t.Fatalf("event = %+v, want %+v", got, result)
		}
	case <-time.After(time.Second):
		t.Fatal("turn event was not published")
	}
}
