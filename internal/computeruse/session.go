package computeruse

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/google/uuid"
)

type SessionState string

const (
	SessionPendingApproval  SessionState = "pending_approval"
	SessionReady            SessionState = "ready"
	SessionPaused           SessionState = "paused"
	SessionNeedsObservation SessionState = "needs_observation"
	SessionStopped          SessionState = "stopped"
	SessionFailed           SessionState = "failed"
	DefaultMaxActions                    = 30
	DefaultObservationTTL                = 120 * time.Second
)

type SessionOwner struct {
	TenantID uint64 `json:"tenant_id"`
	UserID   uint64 `json:"user_id"`
	// SessionID binds an agent capability to its conversation, not just its user.
	SessionID uint64 `json:"session_id"`
}

type SessionOptions struct {
	ID             string
	Owner          SessionOwner
	Capabilities   Capabilities
	MaxActions     int
	ObservationTTL time.Duration
	Now            func() time.Time
}

// ComputerSession is the authorization/state authority. It never runs native
// code under its mutex; a Controller serializes dispatch separately so Stop can
// revoke authorization even while a backend is blocked.
type sessionError struct {
	code string
	text string
}

func (e *sessionError) Error() string { return e.text }
func (e *sessionError) Code() string  { return e.code }

func sessionFailure(code, text string) error {
	return &sessionError{code: code, text: text}
}

func capabilitiesFailure(c Capabilities) error {
	if c.ReadinessErrorCode() == ErrorCodePermissionRequired {
		return sessionFailure(ErrorCodePermissionRequired, "computer backend permission is required")
	}
	return sessionFailure(ErrorCodeHelperUnavailable, "computer helper is unavailable")
}

type ComputerSession struct {
	mu             sync.RWMutex
	id             string
	owner          SessionOwner
	capabilities   Capabilities
	state          SessionState
	approved       bool
	maxActions     int
	actionCount    int
	observationTTL time.Duration
	now            func() time.Time
	observation    Observation
	hasObservation bool
	observations   map[string]struct{}
	actions        map[string]ActionKind
	receipts       map[string]ActionReceipt
	lastReceipt    *ActionReceipt
	turnSequence   uint64
	currentTurn    *ComputerTurnResult
	committedTurns map[string]ComputerTurnResult // bounded consumed actions; no PNG bytes
	inFlight       string
	uncertain      bool
}

func NewComputerSession(o SessionOptions) (*ComputerSession, error) {
	if o.Owner.TenantID == 0 || o.Owner.UserID == 0 {
		return nil, errors.New("computer session owner is required")
	}
	if o.ID == "" {
		o.ID = uuid.NewString()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.MaxActions <= 0 {
		o.MaxActions = DefaultMaxActions
	}
	if o.ObservationTTL <= 0 {
		o.ObservationTTL = DefaultObservationTTL
	}
	if err := o.Capabilities.Validate(); err != nil {
		return nil, err
	}
	return &ComputerSession{id: o.ID, owner: o.Owner, capabilities: cloneCapabilities(o.Capabilities), state: SessionPendingApproval,
		maxActions: o.MaxActions, observationTTL: o.ObservationTTL, now: o.Now, observations: map[string]struct{}{}, actions: map[string]ActionKind{}, receipts: map[string]ActionReceipt{}}, nil
}
func (s *ComputerSession) ID() string               { return s.id }
func (s *ComputerSession) Owner() SessionOwner      { return s.owner }
func (s *ComputerSession) Owns(o SessionOwner) bool { return s.owner == o }
func (s *ComputerSession) State() SessionState      { s.mu.RLock(); defer s.mu.RUnlock(); return s.state }
func (s *ComputerSession) Approved() bool           { s.mu.RLock(); defer s.mu.RUnlock(); return s.approved }
func (s *ComputerSession) ActionCount() int         { s.mu.RLock(); defer s.mu.RUnlock(); return s.actionCount }
func (s *ComputerSession) Capabilities() Capabilities {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneCapabilities(s.capabilities)
}

// UpdateCapabilities refreshes dynamic host inventory without changing the
// session's approval or lifecycle state. Display/window targets are ephemeral
// Window Server resources; they must not remain frozen at session creation.
func (s *ComputerSession) UpdateCapabilities(c Capabilities) error {
	if err := c.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == SessionStopped || s.state == SessionFailed {
		return errors.New("computer session is terminal")
	}
	s.capabilities = cloneCapabilities(c)
	// A live TCC/readiness regression revokes the ability to observe or input
	// immediately. Keep approval as an ownership record, but require an
	// explicit Resume after the host reports ready again; never auto-resume or
	// replay the last observation/action across a permission transition.
	if !c.Ready() && s.approved && s.state != SessionPendingApproval {
		s.state = SessionPaused
		s.hasObservation = false
	}
	return nil
}
func (s *ComputerSession) Approve(o SessionOwner) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.owner != o {
		return errors.New("computer session ownership mismatch")
	}
	if s.state != SessionPendingApproval {
		return fmt.Errorf("cannot approve session in state %q", s.state)
	}
	if !s.capabilities.Ready() {
		return capabilitiesFailure(s.capabilities)
	}
	s.approved = true
	s.state = SessionReady
	return nil
}
func (s *ComputerSession) Pause() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == SessionStopped || s.state == SessionFailed {
		return errors.New("computer session is terminal")
	}
	s.state = SessionPaused
	s.hasObservation = false
	return nil
}
func (s *ComputerSession) InputUncertain() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.uncertain
}

func (s *ComputerSession) Resume(o SessionOwner) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.owner != o {
		return errors.New("computer session ownership mismatch")
	}
	if !s.approved || s.state == SessionStopped || s.state == SessionFailed || s.state == SessionPendingApproval {
		return sessionFailure(ErrorCodeInactive, "computer session cannot resume")
	}
	if s.uncertain {
		return sessionFailure(ErrorCodeInputUncertain, "computer action outcome is uncertain")
	}
	if !s.capabilities.Ready() {
		return capabilitiesFailure(s.capabilities)
	}
	if s.state != SessionPaused {
		return sessionFailure(ErrorCodeInactive, "computer session cannot resume")
	}
	s.state = SessionNeedsObservation
	s.hasObservation = false
	return nil
}
func (s *ComputerSession) Stop(o SessionOwner) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.owner != o {
		return errors.New("computer session ownership mismatch")
	}
	s.state = SessionStopped
	s.approved = false
	s.hasObservation = false
	return nil
}
func (s *ComputerSession) CanObserve() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.canObserveLocked()
}
func (s *ComputerSession) canObserveLocked() error {
	if !s.approved || s.state == SessionStopped || s.state == SessionFailed || s.state == SessionPendingApproval {
		return sessionFailure(ErrorCodeInactive, "computer observation is not authorized")
	}
	if !s.capabilities.Ready() {
		return capabilitiesFailure(s.capabilities)
	}
	if s.inFlight != "" {
		return errors.New("computer action is in flight")
	}
	return nil
}
func (s *ComputerSession) SetObservation(o Observation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.canObserveLocked(); err != nil {
		return err
	}
	if o.SessionID != s.id || o.ID == "" {
		return errors.New("observation identity mismatch")
	}
	if _, seen := s.observations[o.ID]; seen {
		return errors.New("observation must be fresh")
	}
	if err := o.Capabilities.Validate(); err != nil {
		return err
	}
	if !o.Capabilities.Ready() || o.Width <= 0 || o.Height <= 0 {
		return errors.New("observation backend is not ready")
	}
	now := s.now()
	if o.ObservedAt.IsZero() {
		o.ObservedAt = now
	}
	if o.ObservedAt.After(now) {
		return errors.New("observation timestamp is in the future")
	}
	maxExpiry := o.ObservedAt.Add(s.observationTTL)
	if o.ExpiresAt.IsZero() || o.ExpiresAt.After(maxExpiry) {
		o.ExpiresAt = maxExpiry
	}
	if o.Expired(now) {
		return errors.New("observation has expired")
	}
	s.observations[o.ID] = struct{}{}
	s.observation = cloneObservation(o)
	s.hasObservation = true
	s.capabilities = cloneCapabilities(o.Capabilities)
	if s.state == SessionNeedsObservation {
		s.state = SessionReady
	}
	return nil
}
func (s *ComputerSession) CurrentObservation() (Observation, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneObservation(s.observation), s.hasObservation
}
func (s *ComputerSession) ValidateAction(a Action) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.validateActionLocked(a)
}
func (s *ComputerSession) validateActionLocked(a Action) error {
	if a.SessionID != s.id {
		return errors.New("action session mismatch")
	}
	if s.uncertain {
		return sessionFailure(ErrorCodeInputUncertain, "computer action outcome is uncertain")
	}
	if !s.approved || s.state == SessionStopped || s.state == SessionFailed || s.state == SessionPendingApproval {
		return sessionFailure(ErrorCodeInactive, "computer session is not ready")
	}
	if !s.capabilities.Ready() {
		return capabilitiesFailure(s.capabilities)
	}
	if s.state != SessionReady {
		return sessionFailure(ErrorCodeInactive, "computer session is not ready")
	}
	if s.inFlight != "" {
		return errors.New("computer action is in flight")
	}
	if _, seen := s.actions[a.ID]; seen {
		return errors.New("computer action id already consumed; do not replay")
	}
	if s.actionCount >= s.maxActions {
		return errors.New("computer action budget exceeded")
	}
	if a.Kind == ActionLaunchApp {
		// Launch is the one provider action that may establish the first
		// observation. The trusted target registry is resolved by Controller;
		// the session only reserves the action ID before dispatch.
		return a.Validate(s.now(), Observation{})
	}
	if !a.Kind.IsInput() && a.Kind != ActionWait {
		return errors.New("action must use the control plane")
	}
	if !s.capabilities.Supports(a.Kind) {
		return errors.New("computer action is not supported")
	}
	if !s.hasObservation {
		return errors.New("action requires a fresh observation")
	}
	return a.Validate(s.now(), s.observation)
}

// BeginAction atomically consumes the action ID and observation BEFORE dispatch.
// This reservation also covers unknown outcomes; observing again never resets it.
func (s *ComputerSession) BeginAction(a Action) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validateActionLocked(a); err != nil {
		return err
	}
	s.actions[a.ID] = a.Kind
	s.actionCount++
	s.inFlight = a.ID
	s.hasObservation = false
	return nil
}
func (s *ComputerSession) RecordReceipt(r ActionReceipt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.SessionID != s.id || r.ActionID == "" || r.ActionID != s.inFlight {
		return errors.New("receipt does not match the in-flight action")
	}
	if !r.IsTerminal() {
		return errors.New("receipt outcome is not terminal")
	}
	r = cloneReceipt(r)
	s.lastReceipt = &r
	s.receipts[r.ActionID] = r
	s.inFlight = ""
	if r.Outcome == OutcomeUnknown {
		s.uncertain = true
	}
	// Revocation always wins over a late success/failure/unknown response.
	if s.state == SessionStopped || s.state == SessionFailed || s.state == SessionPaused {
		return nil
	}
	s.state = SessionNeedsObservation
	if r.Outcome == OutcomeUnknown {
		s.state = SessionFailed // cannot bypass uncertainty by changing action_id
	}
	if r.Outcome == OutcomeFailed {
		s.state = SessionPaused
	}
	return nil
}

// NextTurnSequence reserves a monotonically increasing sequence for a new
// provider turn. Reserving the sequence before dispatch lets runtime layers
// correlate late/unknown responses without reusing a prior turn identity.
func (s *ComputerSession) NextTurnSequence() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.turnSequence++
	return s.turnSequence
}

// RecordTurnResult commits the already-validated, authoritative turn result.
// It is deliberately separate from RecordReceipt so Controller can persist the
// receipt before observation or screenshot work and commit the completed turn
// only after those optional artifacts have been resolved.
func (s *ComputerSession) RecordTurnResult(result ComputerTurnResult) error {
	if err := result.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if result.SessionID != s.id {
		return errors.New("turn result session mismatch")
	}
	if _, exists := s.committedTurns[result.ActionID]; exists {
		return errors.New("action turn is already committed; do not replay")
	}
	if receipt, exists := s.receipts[result.ActionID]; exists && !reflect.DeepEqual(receipt, result.Receipt) {
		return errors.New("committed action receipt is immutable")
	}
	if s.currentTurn != nil && result.Sequence <= s.currentTurn.Sequence {
		return errors.New("turn result sequence is not increasing")
	}
	if result.Sequence > s.turnSequence {
		s.turnSequence = result.Sequence
	}
	copy := cloneTurnResult(result)
	s.currentTurn = &copy
	if _, consumed := s.actions[result.ActionID]; consumed {
		if s.committedTurns == nil {
			s.committedTurns = make(map[string]ComputerTurnResult)
		}
		// Keep authoritative metadata for duplicate/lost-ACK reads, not a
		// screenshot history. At most maxActions can ever enter this map.
		metadata := result
		metadata.ScreenshotData = nil
		s.committedTurns[result.ActionID] = cloneTurnResult(metadata)
	}
	receipt := cloneReceipt(result.Receipt)
	s.lastReceipt = &receipt
	s.receipts[receipt.ActionID] = receipt
	return nil
}

func (s *ComputerSession) committedTurn(id string) (ComputerTurnResult, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result, ok := s.committedTurns[id]
	return cloneTurnResult(result), ok
}

func (s *ComputerSession) consumedActionKind(id string) (ActionKind, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	kind, ok := s.actions[id]
	return kind, ok
}

func (s *ComputerSession) CurrentTurnResult() (ComputerTurnResult, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.currentTurn == nil {
		return ComputerTurnResult{}, false
	}
	return cloneTurnResult(*s.currentTurn), true
}

func (s *ComputerSession) LastCommittedReceipt() (ActionReceipt, bool) {
	return s.LastReceipt()
}

func (s *ComputerSession) LastReceipt() (ActionReceipt, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.lastReceipt == nil {
		return ActionReceipt{}, false
	}
	return cloneReceipt(*s.lastReceipt), true
}
func (s *ComputerSession) Receipt(id string) (ActionReceipt, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.receipts[id]
	return cloneReceipt(r), ok
}

// Clone returns a detached snapshot, including nested geometry pointers.
func (c Capabilities) Clone() Capabilities { return cloneCapabilities(c) }

// Clone returns an observation whose metadata can be mutated independently.
func (o Observation) Clone() Observation { return cloneObservation(o) }

func cloneFrame(frame *WindowFrame) *WindowFrame {
	if frame == nil {
		return nil
	}
	copy := *frame
	return &copy
}
func cloneCoordinateSpace(space CoordinateSpace) CoordinateSpace {
	space.Bounds = cloneFrame(space.Bounds)
	return space
}
func cloneWindowRef(window WindowRef) WindowRef {
	window.Frame = cloneFrame(window.Frame)
	return window
}
func cloneCapabilities(c Capabilities) Capabilities {
	c.CoordinateSpace = cloneCoordinateSpace(c.CoordinateSpace)
	c.Actions = append([]ActionKind(nil), c.Actions...)
	c.Displays = append([]CoordinateSpace(nil), c.Displays...)
	for i := range c.Displays {
		c.Displays[i] = cloneCoordinateSpace(c.Displays[i])
	}
	c.Windows = append([]WindowRef(nil), c.Windows...)
	for i := range c.Windows {
		c.Windows[i] = cloneWindowRef(c.Windows[i])
	}
	c.TargetWindow = cloneWindowRef(c.TargetWindow)
	return c
}
func cloneObservation(o Observation) Observation {
	o.Capabilities = cloneCapabilities(o.Capabilities)
	o.ActiveWindow = cloneWindowRef(o.ActiveWindow)
	return o
}
func cloneReceipt(r ActionReceipt) ActionReceipt {
	r.ActiveWindowAfter = cloneWindowRef(r.ActiveWindowAfter)
	if r.Before != nil {
		v := *r.Before
		r.Before = &v
	}
	if r.After != nil {
		v := *r.After
		r.After = &v
	}
	if r.ActualPoint != nil {
		v := *r.ActualPoint
		r.ActualPoint = &v
	}
	return r
}
func cloneTurnResult(result ComputerTurnResult) ComputerTurnResult {
	// Raw screenshot bytes are an in-memory provider transfer only; session
	// snapshots retain the MediaRef and never persist pixels.
	result.ScreenshotData = nil
	result.Receipt = cloneReceipt(result.Receipt)
	if result.LaunchReceipt != nil {
		launch := *result.LaunchReceipt
		launch.Window = cloneWindowRef(launch.Window)
		result.LaunchReceipt = &launch
	}
	if result.Observation != nil {
		observation := cloneObservation(*result.Observation)
		result.Observation = &observation
	}
	if result.Screenshot != nil {
		screenshot := *result.Screenshot
		result.Screenshot = &screenshot
	}
	return result
}
