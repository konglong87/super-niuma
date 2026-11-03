package computerbridge

import (
	"context"
	"errors"
	"time"

	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

const (
	receiptHostErrorCode    = "host_operation_failed"
	receiptUnknownErrorCode = "bridge_outcome_unknown"
)

func (c *Client) Lookup(ctx context.Context, owner cu.SessionOwner) (string, error) {
	var out LookupResponse
	if err := c.data(ctx, Request{Op: OpLookup, Owner: owner}, &out); err != nil {
		return "", err
	}
	if !validID(out.SessionID) {
		return "", ErrInvalidResponse
	}
	return out.SessionID, nil
}

func (c *Client) EnsureComputerSession(ctx context.Context, owner cu.SessionOwner) (string, error) {
	return c.ensureComputerSession(ctx, owner, "")
}

// EnsureComputerSessionAttempt is idempotent for one opaque startup attempt.
// The attempt ID is never interpreted by the bridge and is never exposed in
// model-visible errors; it only lets the caller recover a response lost after
// the host created the session.
func (c *Client) EnsureComputerSessionAttempt(ctx context.Context, owner cu.SessionOwner, attemptID string) (string, error) {
	if !validID(attemptID) {
		return "", ErrInvalidRequest
	}
	return c.ensureComputerSession(ctx, owner, attemptID)
}

func (c *Client) ensureComputerSession(ctx context.Context, owner cu.SessionOwner, attemptID string) (string, error) {
	var out SessionResponse
	if err := c.data(ctx, Request{Op: OpEnsure, Owner: owner, StartupAttemptID: attemptID}, &out); err != nil {
		return "", err
	}
	if !validID(out.SessionID) || out.StartupAttemptID != attemptID {
		return "", ErrInvalidResponse
	}
	return out.SessionID, nil
}

// ResolveComputerSessionStart only asks the host about this exact startup
// attempt. It intentionally does not call Lookup and therefore cannot bind a
// different query's session after a lost Ensure response.
func (c *Client) ResolveComputerSessionStart(ctx context.Context, owner cu.SessionOwner, attemptID string) (string, error) {
	if !validID(attemptID) {
		return "", ErrInvalidRequest
	}
	var out SessionResponse
	if err := c.data(ctx, Request{Op: OpResolveEnsure, Owner: owner, StartupAttemptID: attemptID}, &out); err != nil {
		return "", err
	}
	if !validID(out.SessionID) || out.StartupAttemptID != attemptID {
		return "", ErrInvalidResponse
	}
	return out.SessionID, nil
}

func (c *Client) Capabilities(ctx context.Context, owner cu.SessionOwner, sessionID string) (cu.Capabilities, error) {
	var out cu.Capabilities
	if err := c.data(ctx, Request{Op: OpCapabilities, Owner: owner, SessionID: sessionID}, &out); err != nil {
		return cu.Capabilities{}, err
	}
	if out.Validate() != nil {
		return cu.Capabilities{}, ErrInvalidResponse
	}
	return out, nil
}

func (c *Client) LaunchTarget(ctx context.Context, owner cu.SessionOwner, sessionID string, targetID cu.TargetID) (cu.LaunchReceipt, error) {
	if targetID == "" || !validID(string(targetID)) {
		return cu.LaunchReceipt{TargetID: targetID, Outcome: cu.OutcomeRejected, ErrorCode: cu.ErrorCodeUnsupportedTarget, CompletedAt: time.Now()}, ErrInvalidRequest
	}
	response, attempted, err := c.call(ctx, Request{Op: OpLaunchApp, Owner: owner, SessionID: sessionID, TargetID: string(targetID)})
	if !attempted {
		return cu.LaunchReceipt{}, err
	}
	var out cu.LaunchReceipt
	if decodeStrict(response.Data, &out) != nil || !validLaunchReceipt(out, sessionID, string(targetID), "") {
		return cu.LaunchReceipt{TargetID: targetID, Outcome: cu.OutcomeRejected, ErrorCode: cu.ErrorCodeLaunchFailed, CompletedAt: time.Now()}, ErrInvalidResponse
	}
	if out.ErrorCode != "" {
		out.ErrorCode = cu.PublicErrorCode(out.ErrorCode)
	}
	if err != nil {
		return out, err
	}
	if out.Outcome != cu.OutcomeExecuted {
		return out, ErrRemote
	}
	return out, nil
}

func (c *Client) LaunchApp(ctx context.Context, owner cu.SessionOwner, sessionID, application string) (cu.LaunchReceipt, error) {
	if !validID(application) {
		return cu.LaunchReceipt{Application: cu.ComputerApplication(application), Outcome: cu.OutcomeRejected, ErrorCode: cu.ErrorCodeUnsupportedApplication}, ErrInvalidRequest
	}
	response, attempted, err := c.call(ctx, Request{Op: OpLaunchApp, Owner: owner, SessionID: sessionID, Application: application})
	if !attempted {
		return cu.LaunchReceipt{}, err
	}
	var out cu.LaunchReceipt
	if decodeStrict(response.Data, &out) != nil || !validLaunchReceipt(out, sessionID, "", application) {
		return cu.LaunchReceipt{Application: cu.ComputerApplication(application), Outcome: cu.OutcomeRejected, ErrorCode: cu.ErrorCodeLaunchFailed, CompletedAt: time.Now()}, ErrInvalidResponse
	}
	if out.ErrorCode != "" {
		out.ErrorCode = cu.PublicErrorCode(out.ErrorCode)
	}
	if err != nil {
		return out, err
	}
	if out.Outcome != cu.OutcomeExecuted {
		return out, ErrRemote
	}
	return out, nil
}

func (c *Client) Observe(ctx context.Context, owner cu.SessionOwner, in cu.ObserveRequest) (cu.Observation, error) {
	var out cu.Observation
	if err := c.data(ctx, Request{Op: OpObserve, Owner: owner, SessionID: in.SessionID, ObserveRequest: &in}, &out); err != nil {
		return cu.Observation{}, err
	}
	if !validObservation(out, in) {
		return cu.Observation{}, ErrInvalidResponse
	}
	return out, nil
}

// Execute never retries. Once dispatch is attempted, an invalid or absent
// acknowledgement cannot establish whether host input occurred. Return a bound
// unknown receipt AND error so callers cannot mistake a zero receipt for safety.
func (c *Client) Execute(ctx context.Context, owner cu.SessionOwner, action cu.Action) (cu.ActionReceipt, error) {
	response, attempted, err := c.call(ctx, Request{Op: OpExecute, Owner: owner, SessionID: action.SessionID, Action: &action})
	if !attempted {
		return cu.ActionReceipt{}, err
	}
	if err != nil && !errors.Is(err, ErrRemote) {
		return unknownReceipt(action), err
	}
	var receipt cu.ActionReceipt
	if decodeStrict(response.Data, &receipt) != nil || !validReceipt(receipt, action) {
		if err == nil {
			err = ErrInvalidResponse
		}
		return unknownReceipt(action), err
	}
	// A simultaneous success receipt and error is not an execution guarantee.
	if receipt.Outcome == cu.OutcomeExecuted && (err != nil || receipt.ErrorCode != "" || receipt.ErrorMessage != "") {
		return unknownReceipt(action), ErrInvalidResponse
	}
	receipt.ErrorMessage = ""
	receipt.RedactedActionSummary = action.RedactedSummary()
	if receipt.ErrorCode != "" {
		receipt.ErrorCode = cu.PublicErrorCode(receipt.ErrorCode)
	}
	if receipt.Outcome != cu.OutcomeExecuted {
		if receipt.ErrorCode == "" {
			receipt.ErrorCode = receiptHostErrorCode
		}
		return receipt, ErrRemote
	}
	return receipt, nil
}

func unknownReceipt(a cu.Action) cu.ActionReceipt {
	return cu.ActionReceipt{
		ActionID: a.ID, SessionID: a.SessionID, BeforeObservationID: a.ObservationID,
		Outcome: cu.OutcomeUnknown, Verification: cu.VerificationUnknown,
		RedactedActionSummary: a.RedactedSummary(), ErrorCode: receiptUnknownErrorCode,
	}
}

func (c *Client) Pause(ctx context.Context, owner cu.SessionOwner, id string) error {
	return c.control(ctx, owner, id, OpPause)
}
func (c *Client) Resume(ctx context.Context, owner cu.SessionOwner, id string) error {
	return c.control(ctx, owner, id, OpResume)
}
func (c *Client) Stop(ctx context.Context, owner cu.SessionOwner, id string) error {
	return c.control(ctx, owner, id, OpStop)
}
func (c *Client) control(ctx context.Context, owner cu.SessionOwner, id, op string) error {
	var out SessionResponse
	if err := c.data(ctx, Request{Op: op, Owner: owner, SessionID: id}, &out); err != nil {
		return err
	}
	if out.SessionID != id {
		return ErrInvalidResponse
	}
	return nil
}

func (c *Client) data(ctx context.Context, request Request, out any) error {
	response, _, err := c.call(ctx, request)
	if err != nil {
		return err
	}
	return decodeStrict(response.Data, out)
}

// RuntimeStatus is a read-only lifecycle probe. It never creates, resumes, or
// mutates a Computer Use session.
func (c *Client) RuntimeStatus(ctx context.Context, owner cu.SessionOwner) (RuntimeStatusResponse, error) {
	var out RuntimeStatusResponse
	if err := c.data(ctx, Request{Op: OpRuntimeStatus, Owner: owner}, &out); err != nil {
		return RuntimeStatusResponse{}, err
	}
	if !out.State.Valid() || (out.Fault != nil && (!out.Fault.State.Valid() || out.Fault.State != out.State)) {
		return RuntimeStatusResponse{}, ErrInvalidResponse
	}
	return out, nil
}

func (c *Client) StartLocal(ctx context.Context, owner cu.SessionOwner, approved bool) (SessionSnapshotResponse, error) {
	var out SessionSnapshotResponse
	if err := c.data(ctx, Request{Op: OpStartLocal, Owner: owner, Approved: approved}, &out); err != nil {
		return SessionSnapshotResponse{}, err
	}
	if err := validateSessionSnapshot(out, ""); err != nil {
		return SessionSnapshotResponse{}, err
	}
	return out, nil
}

func (c *Client) Snapshot(ctx context.Context, owner cu.SessionOwner, id string) (SessionSnapshotResponse, error) {
	if !validID(id) {
		return SessionSnapshotResponse{}, ErrInvalidRequest
	}
	var out SessionSnapshotResponse
	if err := c.data(ctx, Request{Op: OpSnapshot, Owner: owner, SessionID: id}, &out); err != nil {
		return SessionSnapshotResponse{}, err
	}
	if err := validateSessionSnapshot(out, id); err != nil {
		return SessionSnapshotResponse{}, err
	}
	return out, nil
}

func (c *Client) StopRuntime(ctx context.Context, owner cu.SessionOwner) error {
	var out SessionResponse
	if err := c.data(ctx, Request{Op: OpRuntimeStop, Owner: owner}, &out); err != nil {
		return err
	}
	return nil
}

func (c *Client) VisualCue(ctx context.Context, owner cu.SessionOwner, sessionID string) (VisualCueResponse, error) {
	if !validID(sessionID) {
		return VisualCueResponse{}, ErrInvalidRequest
	}
	var out VisualCueResponse
	if err := c.data(ctx, Request{Op: OpVisualCue, Owner: owner, SessionID: sessionID}, &out); err != nil {
		return VisualCueResponse{}, err
	}
	if !validVisualCueResponse(out, sessionID) {
		return VisualCueResponse{}, ErrInvalidResponse
	}
	return out, nil
}
