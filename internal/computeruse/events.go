package computeruse

import "sync"

type EventKind string

const (
	EventSessionStarted  EventKind = "computer_session_started"
	EventObservation     EventKind = "computer_observation"
	EventActionRequested EventKind = "computer_action_requested"
	EventActionFinished  EventKind = "computer_action_finished"
	EventPermission      EventKind = "computer_permission"
	EventPaused          EventKind = "computer_paused"
	EventResumed         EventKind = "computer_resumed"
	EventStopped         EventKind = "computer_stopped"
	EventFailed          EventKind = "computer_failed"
)

// TurnPublisher receives the same authoritative ComputerTurnResult that is
// returned to the provider. Implementations must not mutate or reconstruct it.
type TurnPublisher interface {
	PublishTurn(ComputerTurnResult)
}

type Event struct {
	Kind          EventKind      `json:"kind"`
	SessionID     string         `json:"session_id"`
	ActionSummary string         `json:"redacted_action_summary,omitempty"`
	Receipt       *ActionReceipt `json:"receipt,omitempty"`
	State         SessionState   `json:"state,omitempty"`
}

type turnSubscription struct {
	channel chan ComputerTurnResult
}

// EventBus is a read-only observation channel for committed runtime turns. It
// never executes actions and never changes session authority. Subscribers that
// stop draining are closed on overflow rather than blocking the turn commit.
type EventBus struct {
	mu          sync.Mutex
	nextID      uint64
	subscribers map[uint64]*turnSubscription
}

func NewEventBus() *EventBus {
	return &EventBus{subscribers: make(map[uint64]*turnSubscription)}
}

func (b *EventBus) Subscribe(buffer int) (<-chan ComputerTurnResult, func()) {
	if buffer <= 0 {
		buffer = 1
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.subscribers == nil {
		b.subscribers = make(map[uint64]*turnSubscription)
	}
	b.nextID++
	id := b.nextID
	subscription := &turnSubscription{channel: make(chan ComputerTurnResult, buffer)}
	b.subscribers[id] = subscription
	cancel := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if current, ok := b.subscribers[id]; ok && current == subscription {
			delete(b.subscribers, id)
			close(subscription.channel)
		}
	}
	return subscription.channel, cancel
}

func (b *EventBus) PublishTurn(result ComputerTurnResult) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, subscription := range b.subscribers {
		select {
		case subscription.channel <- cloneTurnResult(result):
		default:
			delete(b.subscribers, id)
			close(subscription.channel)
		}
	}
}
