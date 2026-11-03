package computeruse

import (
	"sync"
	"time"
)

const (
	visualCueTTL       = 1200 * time.Millisecond
	visualCueHiddenTTL = visualCueTTL
)

// VisualCuePhase describes a best-effort visual indication. It is deliberately
// separate from ComputerTurnResult: a cue never grants authority, changes a
// receipt, or authorizes replay.
type VisualCuePhase string

const (
	VisualCuePreparing  VisualCuePhase = "preparing"
	VisualCueDispatched VisualCuePhase = "dispatched"
	VisualCueUnknown    VisualCuePhase = "unknown"
	VisualCueHidden     VisualCuePhase = "hidden"
)

// ScreenPoint is a global display point in the native screen coordinate space.
// It is distinct from Point, which is an observation-image pixel coordinate.
type ScreenPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// ActionVisualCue is an ephemeral, non-authoritative signal for a native UI
// indicator. It carries only the already-authorized action identity and target
// location; it never carries input text, credentials, or screenshot bytes.
type ActionVisualCue struct {
	SessionID     string         `json:"session_id"`
	TurnID        string         `json:"turn_id,omitempty"`
	ActionID      string         `json:"action_id"`
	Sequence      uint64         `json:"sequence,omitempty"`
	ActionKind    ActionKind     `json:"action_kind"`
	Phase         VisualCuePhase `json:"phase"`
	DispatchState DispatchState  `json:"dispatch_state"`
	AppBundleID   string         `json:"app_bundle_id,omitempty"`
	WindowID      string         `json:"window_id,omitempty"`
	DisplayID     string         `json:"display_id,omitempty"`
	ImagePoint    *Point         `json:"image_point,omitempty"`
	GlobalPoint   *ScreenPoint   `json:"global_point,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	ExpiresAt     time.Time      `json:"expires_at"`
}

func (c ActionVisualCue) expired(now time.Time) bool {
	return !c.ExpiresAt.IsZero() && !now.Before(c.ExpiresAt)
}

func (c ActionVisualCue) valid() bool {
	if c.SessionID == "" || c.ActionID == "" || c.Phase == "" || c.CreatedAt.IsZero() || c.ExpiresAt.IsZero() || !c.ExpiresAt.After(c.CreatedAt) {
		return false
	}
	if c.Phase == VisualCueHidden {
		return true
	}
	if c.ActionKind == "" {
		return false
	}
	if c.Phase == VisualCuePreparing && c.DispatchState != DispatchNotStarted {
		return false
	}
	if c.Phase == VisualCueDispatched && c.DispatchState != DispatchComplete {
		return false
	}
	if c.Phase == VisualCueUnknown && c.DispatchState != DispatchPartial && c.DispatchState != DispatchUnknown {
		return false
	}
	return c.ImagePoint != nil || c.GlobalPoint != nil
}

// VisualCuePublisher receives ephemeral UI hints. Implementations must be
// non-blocking from the action path and must not call back into Controller.
type VisualCuePublisher interface {
	PublishVisualCue(ActionVisualCue)
}

// VisualCueHider is an optional control-plane extension used when Stop or a
// pre-dispatch rejection must immediately remove an ephemeral marker.
type VisualCueHider interface {
	HideVisualCue(sessionID, actionID, turnID string)
}

type visualCueSubscription struct {
	channel chan ActionVisualCue
}

// VisualCueBus distributes visual hints independently from the committed turn
// EventBus. A slow subscriber is closed on overflow instead of delaying input.
type VisualCueBus struct {
	mu          sync.Mutex
	nextID      uint64
	subscribers map[uint64]*visualCueSubscription
	latest      ActionVisualCue
}

func NewVisualCueBus() *VisualCueBus {
	return &VisualCueBus{subscribers: make(map[uint64]*visualCueSubscription)}
}

func (b *VisualCueBus) SubscribeVisualCues(buffer int) (<-chan ActionVisualCue, func()) {
	if buffer <= 0 {
		buffer = 1
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.subscribers == nil {
		b.subscribers = make(map[uint64]*visualCueSubscription)
	}
	b.nextID++
	id := b.nextID
	subscription := &visualCueSubscription{channel: make(chan ActionVisualCue, buffer)}
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

func (b *VisualCueBus) PublishVisualCue(cue ActionVisualCue) {
	if !cue.valid() || cue.expired(time.Now()) {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.latest = cloneVisualCue(cue)
	for id, subscription := range b.subscribers {
		select {
		case subscription.channel <- cloneVisualCue(cue):
		default:
			delete(b.subscribers, id)
			close(subscription.channel)
		}
	}
}

// LatestVisualCue returns the most recent non-expired cue for one session.
// It is a read-only bridge projection; it never grants authority or replays an
// action. A caller can safely poll this from a UI client.
func (b *VisualCueBus) LatestVisualCue(sessionID string) (ActionVisualCue, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.latest.SessionID != sessionID || b.latest.expired(time.Now()) {
		return ActionVisualCue{}, false
	}
	return cloneVisualCue(b.latest), true
}

func (b *VisualCueBus) HideVisualCue(sessionID, actionID, turnID string) {
	now := time.Now()
	b.PublishVisualCue(ActionVisualCue{
		SessionID: sessionID, ActionID: actionID, TurnID: turnID,
		Phase: VisualCueHidden, CreatedAt: now, ExpiresAt: now.Add(visualCueHiddenTTL),
	})
}

func cloneVisualCue(cue ActionVisualCue) ActionVisualCue {
	if cue.ImagePoint != nil {
		point := *cue.ImagePoint
		cue.ImagePoint = &point
	}
	if cue.GlobalPoint != nil {
		point := *cue.GlobalPoint
		cue.GlobalPoint = &point
	}
	return cue
}

func visualCueAction(kind ActionKind) bool {
	switch kind {
	case ActionClick, ActionDoubleClick, ActionRightClick, ActionMove:
		return true
	default:
		return false
	}
}

func visualCuePoint(action Action, receipt ActionReceipt) *Point {
	if receipt.ActualPoint != nil {
		point := *receipt.ActualPoint
		return &point
	}
	if action.Point == nil {
		return nil
	}
	point := *action.Point
	return &point
}

func visualCueGlobalPoint(action Action, before Observation, receipt ActionReceipt) *ScreenPoint {
	if receipt.ActualGlobalPoint != nil {
		point := *receipt.ActualGlobalPoint
		return &point
	}
	imagePoint := visualCuePoint(action, receipt)
	if imagePoint == nil {
		return nil
	}
	bounds := before.Capabilities.CoordinateSpace.Bounds
	scale := before.Capabilities.CoordinateSpace.ScaleFactor
	if bounds == nil || scale <= 0 {
		return nil
	}
	return &ScreenPoint{X: bounds.X + float64(imagePoint.X)/scale, Y: bounds.Y + float64(imagePoint.Y)/scale}
}

// NewPreparingActionVisualCue creates a non-authoritative marker before the
// backend dispatches. Native guards still decide whether input is sent; a
// later hidden/unknown/dispatched cue corrects this hint.
func NewPreparingActionVisualCue(turnID string, sequence uint64, action Action, before Observation, now time.Time) (ActionVisualCue, bool) {
	if !visualCueAction(action.Kind) || action.Point == nil {
		return ActionVisualCue{}, false
	}
	point := *action.Point
	cue := ActionVisualCue{
		SessionID: action.SessionID, TurnID: turnID, ActionID: action.ID,
		Sequence: sequence, ActionKind: action.Kind, Phase: VisualCuePreparing,
		DispatchState: DispatchNotStarted, AppBundleID: before.ActiveWindow.BundleID,
		WindowID: before.WindowID, DisplayID: before.DisplayID, ImagePoint: &point,
		CreatedAt: now, ExpiresAt: now.Add(visualCueTTL),
	}
	if bounds := before.Capabilities.CoordinateSpace.Bounds; bounds != nil {
		scale := before.Capabilities.CoordinateSpace.ScaleFactor
		if scale > 0 {
			cue.GlobalPoint = &ScreenPoint{X: bounds.X + float64(point.X)/scale, Y: bounds.Y + float64(point.Y)/scale}
		}
	}
	return cue, cue.valid()
}

// NewActionVisualCue derives a cue from the same action, observation and
// receipt used by the turn. Rejected/not-started actions intentionally produce
// no cue; partial/unknown dispatch produces a warning-state cue and is never a
// success animation.
func NewActionVisualCue(turnID string, sequence uint64, action Action, before Observation, receipt ActionReceipt, now time.Time) (ActionVisualCue, bool) {
	if !visualCueAction(action.Kind) {
		return ActionVisualCue{}, false
	}
	imagePoint := visualCuePoint(action, receipt)
	if imagePoint == nil {
		return ActionVisualCue{}, false
	}
	phase := VisualCueUnknown
	if receipt.Outcome == OutcomeExecuted && receipt.DispatchState == DispatchComplete {
		phase = VisualCueDispatched
	} else if receipt.Outcome != OutcomeUnknown || (receipt.DispatchState != DispatchPartial && receipt.DispatchState != DispatchUnknown) {
		return ActionVisualCue{}, false
	}
	cue := ActionVisualCue{
		SessionID:     action.SessionID,
		TurnID:        turnID,
		ActionID:      action.ID,
		Sequence:      sequence,
		ActionKind:    action.Kind,
		Phase:         phase,
		DispatchState: receipt.DispatchState,
		AppBundleID:   before.ActiveWindow.BundleID,
		WindowID:      before.WindowID,
		DisplayID:     before.DisplayID,
		ImagePoint:    imagePoint,
		CreatedAt:     now,
		ExpiresAt:     now.Add(visualCueTTL),
	}
	cue.GlobalPoint = visualCueGlobalPoint(action, before, receipt)
	return cue, cue.valid()
}
