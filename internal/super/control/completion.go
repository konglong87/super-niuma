package control

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/konglong87/super-niuma/internal/super/execution"
	"github.com/konglong87/super-niuma/internal/super/verification"
)

const verificationJobID = "super.verify"

func (e *Engine) verify(ctx context.Context, r Run, owner string) (bool, error) {
	claimed, err := e.mutate(ctx, r.Scope, r.ID, func(n *Run) error {
		if e.authorized(ctx, *n) != nil {
			return ErrDenied
		}
		if !allSucceeded(*n) || n.VerificationClaim.Token != "" {
			return ErrConflict
		}
		n.Status = Verifying
		n.VerificationClaim = e.newClaim(*n, verificationJobID, owner, n.VerificationClaim.Epoch)
		return nil
	})
	if errors.Is(err, ErrConflict) || errors.Is(err, ErrResourceBusy) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	c := claimed.VerificationClaim
	vc, cancel := context.WithTimeout(ctx, time.Duration(claimed.Grant.TimeoutMS)*time.Millisecond)
	defer cancel()
	var report verification.Report
	verifyErr := e.check(vc, c)
	if verifyErr == nil {
		report, verifyErr = e.Verifier.Verify(vc, claimed.Grant.Workspace, claimed.Grant.Verification)
	}
	finishCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	_, err = e.mutate(finishCtx, r.Scope, r.ID, func(n *Run) error {
		if !claimEqual(n.VerificationClaim, c) {
			return ErrConflict
		}
		if n.VerificationClaim.UntilMS <= e.now() {
			n.Status = NeedsAttention
			n.SafeCode = "verification_lease_lost"
			return nil
		}
		n.VerificationClaim = Claim{Epoch: c.Epoch}
		n.VerificationAttemptID = c.Token
		if n.Status == Cancelling {
			n.Status = Cancelled
			n.SafeCode = "cancelled"
			return nil
		}
		if ctx.Err() != nil {
			// Shutdown interrupted a read-only verifier. Release its claim and
			// stay Verifying so it independently re-verifies after restart.
			return nil
		}
		if e.authorized(finishCtx, *n) != nil {
			n.Status = Cancelled
			n.SafeCode = "authority_revoked"
			return nil
		}
		if verifyErr != nil || report.PolicyHash != verification.PolicyHash(n.Grant.Verification) {
			n.Status = NeedsAttention
			n.SafeCode = "verification_unavailable"
			return nil
		}
		if report.Passed && !freshArtifacts(*n, report) {
			report.Passed = false
			n.SafeCode = "artifact_provenance_mismatch"
		}
		n.Verification = &report
		if !report.Passed {
			// Revise only the artifact writers and whatever consumed their
			// results; read-only analyses stay valid and are not paid for again.
			revise := revisionJobs(*n)
			retry := len(revise) > 0
			for i := range revise {
				if n.Jobs[i].Attempts >= MaxAttempts {
					retry = false
				}
			}
			if retry {
				for i := range revise {
					n.Jobs[i].Status = JobQueued
				}
				n.Status = Queued
				n.SafeCode = "verification_revision_required"
			} else {
				n.Status = Failed
				n.SafeCode = "verification_failed"
			}
			return nil
		}
		n.Status = DeliveryPending
		n.SafeCode = ""
		n.Notices = []Notice{}
		for _, t := range n.Grant.Targets {
			n.Notices = append(n.Notices, Notice{ID: "notice." + Digest([]string{n.ID, t.ID, n.GrantHash})[:40], Target: t, Status: DeliveryQueued})
		}
		return nil
	})
	e.logger().Info("Super independent verification settled", "action", "verification.settled", "run_id", r.ID, "passed", verifyErr == nil && report.Passed && err == nil)
	return true, err
}

func (e *Engine) deliver(ctx context.Context, r Run, n Notice, owner string) (bool, error) {
	claimed, err := e.mutate(ctx, r.Scope, r.ID, func(root *Run) error {
		if root.Status != DeliveryPending || root.Verification == nil || !root.Verification.Passed || e.authorized(ctx, *root) != nil {
			return ErrDenied
		}
		for i := range root.Notices {
			v := &root.Notices[i]
			if v.ID != n.ID {
				continue
			}
			if (v.Status != DeliveryQueued && v.Status != DeliveryRetry) || v.RetryAtMS > e.now() {
				return ErrConflict
			}
			v.Status = DeliverySending
			v.Attempts++
			v.Claim = e.newClaim(*root, "super.delivery."+v.Target.ID, owner, v.Claim.Epoch)
			return nil
		}
		return ErrNotFound
	})
	if errors.Is(err, ErrConflict) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var claim Claim
	for _, v := range claimed.Notices {
		if v.ID == n.ID {
			claim = v.Claim
		}
	}
	sendCtx, cancel := context.WithTimeout(ctx, time.Duration(e.leaseMS()/2)*time.Millisecond)
	defer cancel()
	var receipt Receipt
	var sendErr error
	if sendErr = e.check(sendCtx, claim); sendErr == nil {
		receipt, sendErr = e.Sender.Send(sendCtx, n.Target, n.ID, fmt.Sprintf("Super Niuma 工单 %s 已通过所配置的独立验收规则。请在 Desktop 核对证据与交付范围。", r.ID))
	} else if ctx.Err() != nil {
		// Shutdown before anything was sent: nothing left this process.
		sendErr = &SendError{Code: SafeInterrupted, Retryable: true, DefinitelyNotAccepted: true}
	} else {
		sendErr = &SendError{Code: "authority_revoked_before_send", DefinitelyNotAccepted: true}
	}
	finishCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	_, err = e.mutate(finishCtx, r.Scope, r.ID, func(root *Run) error {
		for i := range root.Notices {
			v := &root.Notices[i]
			if v.ID != n.ID {
				continue
			}
			if v.Status != DeliverySending || !claimEqual(v.Claim, claim) {
				return ErrConflict
			}
			if v.Claim.UntilMS <= e.now() {
				v.Status = DeliveryUnknown
				v.SafeCode = "delivery_outcome_unknown"
				root.Status = NeedsAttention
				return nil
			}
			if sendErr == nil && receipt.MessageID != "" && receipt.DeliveredAtMS > 0 && (n.Target.Provider == ProviderDesktop || receipt.Chat == n.Target.Chat) {
				v.Status = DeliverySent
				v.Receipt = &receipt
				v.SafeCode = ""
			} else {
				var typed *SendError
				if errors.As(sendErr, &typed) && typed.DefinitelyNotAccepted {
					v.SafeCode = typed.Code
					if typed.Retryable && v.Attempts < MaxAttempts && root.Status != Cancelling {
						v.Status = DeliveryRetry
						v.RetryAtMS = e.now() + int64(v.Attempts*v.Attempts)*1000
					} else {
						v.Status = DeliveryDead
						if root.Status != Cancelling {
							root.Status = NeedsAttention
						}
					}
				}
				if v.Status == DeliverySending {
					v.Status = DeliveryUnknown
					v.SafeCode = "delivery_outcome_unknown"
					root.Status = NeedsAttention
				}
			}
			// Keep the settled claim as immutable receipt provenance. Status gates
			// its authority; retaining it also preserves the next retry epoch.
			break
		}
		if root.Status == Cancelling {
			for _, other := range root.Notices {
				if other.Status == DeliverySending {
					return nil
				}
			}
			root.Status = Cancelled
			return nil
		}
		sent := true
		for _, v := range root.Notices {
			if v.Status != DeliverySent {
				sent = false
			}
		}
		if sent && root.Status == DeliveryPending {
			root.Status = Delivered
		}
		return nil
	})
	e.logger().Info("Super delivery settled", "action", "delivery.settled", "run_id", r.ID, "target_id", n.Target.ID, "accepted", sendErr == nil && err == nil)
	return true, err
}

// revisionJobs returns the indexes of jobs that write artifacts plus every job
// that transitively depends on one of them.
func revisionJobs(r Run) map[int]bool {
	out := map[int]bool{}
	for i, j := range r.Jobs {
		if len(j.Spec.WritePaths) > 0 {
			out[i] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for i, j := range r.Jobs {
			if out[i] {
				continue
			}
			for _, d := range j.Spec.DependsOn {
				if k := jobIndex(r, d); k >= 0 && out[k] {
					out[i] = true
					changed = true
					break
				}
			}
		}
	}
	return out
}

// verificationFindings turns a failed independent report into trusted revision
// guidance (paths, rule kinds and codes only; never file or model content).
func verificationFindings(report *verification.Report) []execution.VerificationFinding {
	if report == nil || report.Passed {
		return nil
	}
	var out []execution.VerificationFinding
	for _, c := range report.Checks {
		if !c.Passed {
			out = append(out, execution.VerificationFinding{Path: c.Path, Kind: c.Kind, Code: c.Code})
		}
	}
	if len(out) == 0 {
		// Every rule passed but the files were not produced by the current
		// attempt's trusted writes (stale or foreign artifacts).
		for _, a := range report.Artifacts {
			out = append(out, execution.VerificationFinding{Path: a.Path, Code: "artifact_not_written_this_attempt"})
		}
	}
	return out
}

type FileVerifier struct{}

func (FileVerifier) Verify(ctx context.Context, workspace string, p verification.Policy) (verification.Report, error) {
	return verification.Verify(ctx, workspace, p)
}

type DesktopSender struct{}

func (DesktopSender) Send(ctx context.Context, t Target, id, text string) (Receipt, error) {
	if ctx.Err() != nil {
		// The local receipt is written synchronously; nothing was accepted.
		return Receipt{}, &SendError{Code: SafeInterrupted, Retryable: true, DefinitelyNotAccepted: true}
	}
	if t.Provider != ProviderDesktop {
		return Receipt{}, &SendError{Code: "provider_unconfigured", DefinitelyNotAccepted: true}
	}
	return Receipt{MessageID: id, DeliveredAtMS: time.Now().UnixMilli()}, nil
}

// A passing assertion is not enough: each candidate must match a write receipt
// from a successful current attempt. A worker's natural-language manifest is
// never a source of authority, and unchanged old files cannot self-certify.
func freshArtifacts(r Run, report verification.Report) bool {
	if len(report.Artifacts) == 0 {
		return false
	}
	for _, a := range report.Artifacts {
		matched := false
		for _, j := range r.Jobs {
			if j.Status != JobSucceeded {
				continue
			}
			for _, w := range j.Result.Writes {
				if w.Path == a.Path && w.SHA256 == a.SHA256 && w.Bytes == a.Bytes && w.AttemptID == j.Claim.Token {
					matched = true
				}
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

// DeliverRun is used by the durable notice subscriber. False means there is
// still an outbox obligation; NACK retains the event for a bounded later retry.
func (e *Engine) DeliverRun(ctx context.Context, scope, id, owner string) (bool, error) {
	r, err := e.Store.GetRun(ctx, scope, id)
	if err != nil {
		return false, err
	}
	if IsTerminal(r.Status) {
		return true, nil
	}
	if r.Status != DeliveryPending {
		return false, ErrConflict
	}
	for _, n := range r.Notices {
		if (n.Status == DeliveryQueued || n.Status == DeliveryRetry) && n.RetryAtMS <= e.now() {
			_, err = e.deliver(ctx, r, n, owner)
			if err != nil {
				return false, err
			}
			r, err = e.Store.GetRun(ctx, scope, id)
			if err != nil {
				return false, err
			}
		}
	}
	return IsTerminal(r.Status), nil
}
