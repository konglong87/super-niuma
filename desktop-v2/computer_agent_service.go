package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/konglong87/super-niuma/internal/computerbridge"
	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

var errComputerAgentUnauthorized = errors.New("no approved computer session for this conversation")

// computerAgentService does not create or approve sessions. It borrows the UI's
// controller only when a full, non-local owner binding is already present.
// A local preview/acceptance owner (conversation=0) is intentionally invisible.
type computerAgentService struct {
	manager     *computerManager
	stopRuntime func()
}

// EnsureComputerSession is called only after the agent tool's normal
// ComputerUse permission gate. It binds an approved model owner lazily, while
// keeping local preview (owner 0/0/0) outside this path.
func (s computerAgentService) EnsureComputerSession(ctx context.Context, owner cu.SessionOwner) (string, error) {
	return s.ensureComputerSession(ctx, owner, "")
}

func (s computerAgentService) EnsureComputerSessionAttempt(ctx context.Context, owner cu.SessionOwner, attemptID string) (string, error) {
	if strings.TrimSpace(attemptID) == "" {
		return "", errComputerAgentUnauthorized
	}
	id, err := s.ensureComputerSession(ctx, owner, attemptID)
	if err == nil {
		s.manager.rememberStartAttempt(owner, attemptID, id)
	}
	return id, err
}

func (s computerAgentService) ensureComputerSession(ctx context.Context, owner cu.SessionOwner, attemptID string) (string, error) {
	if s.manager == nil || owner == computerUIBridgeOwner || !validComputerConversationOwner(owner) {
		return "", errComputerAgentUnauthorized
	}
	snapshot, err := s.manager.startOwned(ctx, ComputerSessionStartInput{Approved: true}, owner)
	if err != nil {
		startupLog("computer ensure failed: " + err.Error())
		return "", err
	}
	return snapshot.ID, nil
}

func (s computerAgentService) ResolveComputerSessionStart(ctx context.Context, owner cu.SessionOwner, attemptID string) (string, error) {
	if s.manager == nil || !validComputerConversationOwner(owner) || strings.TrimSpace(attemptID) == "" {
		return "", errComputerAgentUnauthorized
	}
	id := s.manager.resolveStartAttempt(owner, attemptID)
	if id == "" {
		return "", errors.New("computer startup attempt is not known")
	}
	s.manager.mu.Lock()
	c := s.manager.controller
	valid := c != nil && c.Session().Owns(owner) && c.Session().ID() == id &&
		c.Session().State() != cu.SessionStopped && c.Session().State() != cu.SessionFailed
	s.manager.mu.Unlock()
	if !valid {
		return "", errors.New("computer startup attempt is no longer active")
	}
	return id, nil
}

func (s computerAgentService) effectiveOwner(owner cu.SessionOwner) cu.SessionOwner {
	if owner == computerUIBridgeOwner && s.manager != nil {
		return s.manager.owner
	}
	return owner
}

func (s computerAgentService) controller(ctx context.Context, owner cu.SessionOwner, id string, lookup bool) (*cu.Controller, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	uiOwner := owner == computerUIBridgeOwner
	owner = s.effectiveOwner(owner)
	if s.manager == nil || owner.TenantID == 0 || owner.UserID == 0 || (!uiOwner && owner.SessionID == 0) {
		return nil, errComputerAgentUnauthorized
	}
	s.manager.mu.Lock()
	defer s.manager.mu.Unlock()
	c := s.manager.controller
	if c == nil {
		return nil, errComputerAgentUnauthorized
	}
	session := c.Session()
	if !session.Owns(owner) || !session.Approved() || (!lookup && session.ID() != id) || session.State() == cu.SessionStopped || session.State() == cu.SessionFailed {
		startupLog(fmt.Sprintf("computer controller denied: owner=%+v session_owner=%+v requested_session=%q actual_session=%q approved=%t state=%q lookup=%t", owner, session.Owner(), id, session.ID(), session.Approved(), session.State(), lookup))
		return nil, errComputerAgentUnauthorized
	}
	return c, nil
}
func (s computerAgentService) Lookup(ctx context.Context, owner cu.SessionOwner) (string, error) {
	c, err := s.controller(ctx, owner, "", true)
	if err != nil {
		return "", err
	}
	return c.Session().ID(), nil
}
func (s computerAgentService) Capabilities(ctx context.Context, owner cu.SessionOwner, id string) (cu.Capabilities, error) {
	if owner == computerUIBridgeOwner && id == "" && s.manager != nil {
		return s.manager.capabilities(ctx)
	}
	effectiveOwner := s.effectiveOwner(owner)
	c, err := s.controller(ctx, owner, id, false)
	if err != nil {
		return cu.Capabilities{}, err
	}
	return c.Capabilities(ctx, effectiveOwner, id)
}
func (s computerAgentService) LaunchTarget(ctx context.Context, owner cu.SessionOwner, id string, targetID cu.TargetID) (cu.LaunchReceipt, error) {
	effectiveOwner := s.effectiveOwner(owner)
	c, err := s.controller(ctx, owner, id, false)
	if err != nil {
		return cu.LaunchReceipt{TargetID: targetID, Outcome: cu.OutcomeRejected, ErrorCode: cu.ErrorCodeInactive}, err
	}
	return c.LaunchTarget(ctx, effectiveOwner, id, targetID)
}

// LaunchApp remains a compatibility adapter for pre-target clients. New model
// calls use LaunchTarget and the host registry.
func (s computerAgentService) LaunchApp(ctx context.Context, owner cu.SessionOwner, id, application string) (cu.LaunchReceipt, error) {
	return s.LaunchTarget(ctx, owner, id, cu.TargetID(application))
}
func (s computerAgentService) Observe(ctx context.Context, owner cu.SessionOwner, r cu.ObserveRequest) (cu.Observation, error) {
	effectiveOwner := s.effectiveOwner(owner)
	c, err := s.controller(ctx, owner, r.SessionID, false)
	if err != nil {
		return cu.Observation{}, err
	}
	return c.Observe(ctx, effectiveOwner, r)
}
func (s computerAgentService) ExecuteTurn(ctx context.Context, owner cu.SessionOwner, a cu.Action) (cu.ComputerTurnResult, error) {
	effectiveOwner := s.effectiveOwner(owner)
	c, err := s.controller(ctx, owner, a.SessionID, false)
	if err != nil {
		return cu.ComputerTurnResult{}, err
	}
	return c.ExecuteTurn(ctx, effectiveOwner, a)
}

// VisualCue is restricted to the local UI/supervision identities. Model
// owners never receive a polling endpoint for native UI presentation hints.
func (s computerAgentService) VisualCue(ctx context.Context, owner cu.SessionOwner, sessionID string) (computerbridge.VisualCueResponse, error) {
	if owner != computerUIBridgeOwner && owner != computerRuntimeSupervisionOwner {
		return computerbridge.VisualCueResponse{}, errComputerAgentUnauthorized
	}
	if s.manager == nil || strings.TrimSpace(sessionID) == "" {
		return computerbridge.VisualCueResponse{}, errComputerAgentUnauthorized
	}
	s.manager.mu.Lock()
	bus := s.manager.visualCues
	s.manager.mu.Unlock()
	if bus == nil {
		return computerbridge.VisualCueResponse{}, nil
	}
	cue, ok := bus.LatestVisualCue(sessionID)
	if !ok {
		return computerbridge.VisualCueResponse{}, nil
	}
	return computerbridge.VisualCueResponse{Cue: &cue}, nil
}

func (s computerAgentService) Execute(ctx context.Context, owner cu.SessionOwner, a cu.Action) (cu.ActionReceipt, error) {
	effectiveOwner := s.effectiveOwner(owner)
	c, err := s.controller(ctx, owner, a.SessionID, false)
	if err != nil {
		return cu.ActionReceipt{ActionID: a.ID, SessionID: a.SessionID, Outcome: cu.OutcomeRejected, Verification: cu.VerificationNotChecked}, err
	}
	return c.Execute(ctx, effectiveOwner, a)
}
func (s computerAgentService) Pause(ctx context.Context, owner cu.SessionOwner, id string) error {
	effectiveOwner := s.effectiveOwner(owner)
	c, err := s.controller(ctx, owner, id, false)
	if err != nil {
		return err
	}
	return c.Pause(ctx, effectiveOwner, id)
}
func (s computerAgentService) Resume(ctx context.Context, owner cu.SessionOwner, id string) error {
	effectiveOwner := s.effectiveOwner(owner)
	c, err := s.controller(ctx, owner, id, false)
	if err != nil {
		return err
	}
	return c.Resume(ctx, effectiveOwner, id)
}
func (s computerAgentService) Stop(ctx context.Context, owner cu.SessionOwner, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	effectiveOwner := s.effectiveOwner(owner)
	if s.manager == nil || (owner != computerUIBridgeOwner && !validComputerConversationOwner(owner)) {
		return errComputerAgentUnauthorized
	}
	s.manager.mu.Lock()
	c := s.manager.controller
	// Revocation clears Approved. The exact old owner may repeat Stop, but must
	// never discover/use that stopped grant or affect a replacement controller.
	if c == nil || c.Session().ID() != id || !c.Session().Owns(effectiveOwner) {
		s.manager.mu.Unlock()
		return errComputerAgentUnauthorized
	}
	session := c.Session()
	allowed := session.Approved() || session.State() == cu.SessionStopped || session.State() == cu.SessionFailed
	s.manager.mu.Unlock()
	if !allowed {
		return errComputerAgentUnauthorized
	}
	return c.Stop(ctx, effectiveOwner, id)
}

func (s computerAgentService) TurnScreenshot(ctx context.Context, owner cu.SessionOwner, result cu.ComputerTurnResult) ([]byte, string, error) {
	effectiveOwner := s.effectiveOwner(owner)
	c, err := s.controller(ctx, owner, result.SessionID, false)
	if err != nil {
		return nil, "", err
	}
	return c.TurnScreenshot(ctx, effectiveOwner, result)
}

func (s computerAgentService) ObservationImage(ctx context.Context, owner cu.SessionOwner, id, observationID string) ([]byte, string, error) {
	c, effectiveOwner, err := s.readOnlyController(ctx, owner, id)
	if err != nil {
		return nil, "", err
	}
	return c.ObservationImage(ctx, effectiveOwner, id, observationID)
}

func (s computerAgentService) readOnlyController(ctx context.Context, owner cu.SessionOwner, id string) (*cu.Controller, cu.SessionOwner, error) {
	if err := ctx.Err(); err != nil {
		return nil, cu.SessionOwner{}, err
	}
	if owner != computerRuntimeSupervisionOwner {
		effectiveOwner := s.effectiveOwner(owner)
		c, err := s.controller(ctx, owner, id, false)
		return c, effectiveOwner, err
	}
	if s.manager == nil {
		return nil, cu.SessionOwner{}, errComputerAgentUnauthorized
	}
	s.manager.mu.Lock()
	c := s.manager.controller
	s.manager.mu.Unlock()
	if c == nil || c.Session().ID() != id || !c.Session().Approved() || c.Session().State() == cu.SessionStopped || c.Session().State() == cu.SessionFailed {
		return nil, cu.SessionOwner{}, errComputerAgentUnauthorized
	}
	return c, c.Session().Owner(), nil
}

func (s computerAgentService) StartLocal(ctx context.Context, owner cu.SessionOwner, approved bool) (computerbridge.SessionSnapshotResponse, error) {
	if err := ctx.Err(); err != nil {
		return computerbridge.SessionSnapshotResponse{}, err
	}
	if owner != computerUIBridgeOwner || s.manager == nil || !approved {
		return computerbridge.SessionSnapshotResponse{}, errComputerAgentUnauthorized
	}
	snapshot, err := s.manager.startOwned(ctx, ComputerSessionStartInput{Approved: true}, s.manager.owner)
	if err != nil {
		return computerbridge.SessionSnapshotResponse{}, err
	}
	s.manager.mu.Lock()
	c := s.manager.controller
	s.manager.mu.Unlock()
	if c == nil || c.Session().ID() != snapshot.ID {
		return computerbridge.SessionSnapshotResponse{}, errComputerAgentUnauthorized
	}
	return computerBridgeSnapshot(c.Session()), nil
}

func (s computerAgentService) Snapshot(ctx context.Context, owner cu.SessionOwner, id string) (computerbridge.SessionSnapshotResponse, error) {
	if err := ctx.Err(); err != nil {
		return computerbridge.SessionSnapshotResponse{}, err
	}
	if s.manager == nil || (owner != computerUIBridgeOwner && owner != computerRuntimeSupervisionOwner) {
		return computerbridge.SessionSnapshotResponse{}, errComputerAgentUnauthorized
	}
	s.manager.mu.Lock()
	c := s.manager.controller
	s.manager.mu.Unlock()
	if c == nil || c.Session().ID() != id {
		return computerbridge.SessionSnapshotResponse{}, errComputerAgentUnauthorized
	}
	if owner == computerUIBridgeOwner && !c.Session().Owns(s.manager.owner) {
		return computerbridge.SessionSnapshotResponse{}, errComputerAgentUnauthorized
	}
	return computerBridgeSnapshot(c.Session()), nil
}

func (s computerAgentService) RuntimeStatus(ctx context.Context, owner cu.SessionOwner) (computerbridge.RuntimeStatusResponse, error) {
	if err := ctx.Err(); err != nil {
		return computerbridge.RuntimeStatusResponse{}, err
	}
	if owner != computerRuntimeSupervisionOwner || s.manager == nil {
		return computerbridge.RuntimeStatusResponse{}, errComputerAgentUnauthorized
	}
	status := s.manager.runtimeStatus()
	return computerbridge.RuntimeStatusResponse{State: status.State, Fault: status.Fault, ActiveSession: status.ActiveSession, SessionID: status.SessionID}, nil
}

func (s computerAgentService) StopRuntime(ctx context.Context, owner cu.SessionOwner) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if owner != computerRuntimeSupervisionOwner || s.manager == nil {
		return errComputerAgentUnauthorized
	}
	if s.manager.hasActiveSession() {
		return errors.New("stop the active computer session before stopping the runtime")
	}
	if err := s.manager.close(ctx); err != nil {
		return err
	}
	if s.stopRuntime != nil {
		s.stopRuntime()
	}
	return nil
}
