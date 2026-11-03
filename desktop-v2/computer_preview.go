package main

import (
	"context"
	"encoding/base64"
	"errors"
	"reflect"

	macbackend "github.com/konglong87/super-niuma/internal/computerbackend/macos"
	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

var errComputerPreviewUnavailable = errors.New("computer preview unavailable")

// A nil receipt identifies current-observation evidence; a non-nil receipt
// identifies display-only after-action evidence, never action authority.
type computerPreviewEvidence struct {
	observation cu.Observation
	receipt     *cu.ActionReceipt
}

// preview reads only existing current/after-action images. It must not observe,
// refresh capabilities/freshness, pause, or otherwise change model authority.
func (m *computerManager) preview(ctx context.Context, id string) (ComputerObservationDTO, error) {
	if m.remote != nil {
		return m.remotePreview(ctx, id)
	}
	if ctx == nil {
		return ComputerObservationDTO{}, errComputerPreviewUnavailable
	}
	if err := ctx.Err(); err != nil {
		return ComputerObservationDTO{}, err
	}
	m.mu.Lock()
	c, backend := m.controller, m.backend
	evidence, ok := computerPreviewEvidenceFor(c, id)
	m.mu.Unlock()
	if !ok || !computerPreviewHostPermissionsAvailable(backend) {
		return ComputerObservationDTO{}, errComputerPreviewUnavailable
	}

	// Do not hold the manager lock across backend IO: Stop/replacement must
	// remain available even if the cached-image reader is slow or blocked.
	data, mediaType, err := c.ObservationImage(ctx, c.Session().Owner(), id, evidence.observation.ID)
	if err != nil {
		return ComputerObservationDTO{}, err
	}
	if len(data) == 0 || mediaType == "" {
		return ComputerObservationDTO{}, errComputerPreviewUnavailable
	}
	result := ComputerObservationDTO{Observation: evidence.observation, ImageData: base64.StdEncoding.EncodeToString(data), MediaType: mediaType}
	if !computerPreviewHostPermissionsAvailable(backend) {
		return ComputerObservationDTO{}, errComputerPreviewUnavailable
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ComputerObservationDTO{}, err
	}
	if m.controller != c {
		return ComputerObservationDTO{}, errComputerPreviewUnavailable
	}
	current, ok := computerPreviewEvidenceFor(c, id)
	if !ok || !reflect.DeepEqual(current, evidence) {
		return ComputerObservationDTO{}, errComputerPreviewUnavailable
	}
	return result, nil
}

func (m *computerManager) remotePreview(ctx context.Context, id string) (ComputerObservationDTO, error) {
	if ctx == nil {
		return ComputerObservationDTO{}, errComputerPreviewUnavailable
	}
	if err := ctx.Err(); err != nil {
		return ComputerObservationDTO{}, err
	}
	snapshot, err := m.remote.Snapshot(ctx, m.remoteOwner, id)
	imageOwner := m.remoteOwner
	if err != nil {
		snapshot, err = m.remote.Snapshot(ctx, computerRuntimeSupervisionOwner, id)
		imageOwner = computerRuntimeSupervisionOwner
	}
	if err != nil || snapshot.Observation == nil || snapshot.State == cu.SessionStopped || snapshot.State == cu.SessionFailed || !snapshot.Approved {
		return ComputerObservationDTO{}, errComputerPreviewUnavailable
	}
	data, mediaType, err := m.remote.ObservationImage(ctx, imageOwner, id, snapshot.Observation.ID)
	if err != nil || len(data) == 0 || mediaType == "" {
		return ComputerObservationDTO{}, errComputerPreviewUnavailable
	}
	return ComputerObservationDTO{Observation: *snapshot.Observation, ImageData: base64.StdEncoding.EncodeToString(data), MediaType: mediaType}, nil
}

func computerPreviewEvidenceFor(c *cu.Controller, id string) (computerPreviewEvidence, bool) {
	if c == nil {
		return computerPreviewEvidence{}, false
	}
	s := c.Session()
	state := s.State()
	if s.ID() != id || !s.Approved() || state == cu.SessionStopped || state == cu.SessionFailed || !s.Capabilities().Ready() {
		return computerPreviewEvidence{}, false
	}
	observation, current := s.CurrentObservation()
	if current {
		return computerPreviewEvidence{observation: observation}, true
	}
	if state != cu.SessionNeedsObservation {
		return computerPreviewEvidence{}, false
	}
	receipt, ok := s.LastReceipt()
	if !ok {
		return computerPreviewEvidence{}, false
	}
	// CurrentObservation retains the consumed observation metadata when its
	// bool is false. Bind to it only to reject receipts predating newer model
	// observations, never to restore the consumed action authority.
	after, ok := computerPreviewReceiptObservation(id, observation, receipt)
	if !ok {
		return computerPreviewEvidence{}, false
	}
	return computerPreviewEvidence{observation: after, receipt: &receipt}, true
}

func computerPreviewReceiptObservation(id string, consumed cu.Observation, receipt cu.ActionReceipt) (cu.Observation, bool) {
	if receipt.SessionID != id || receipt.ActionID == "" || receipt.Outcome != cu.OutcomeExecuted ||
		receipt.After == nil || receipt.AfterObservationID == "" || receipt.After.ID != receipt.AfterObservationID ||
		receipt.After.Width <= 0 || receipt.After.Height <= 0 || receipt.After.MediaType == "" || receipt.CompletedAt.IsZero() ||
		consumed.SessionID != id || consumed.ID == "" || receipt.BeforeObservationID != consumed.ID ||
		receipt.AfterObservationID == consumed.ID || receipt.CompletedAt.Before(consumed.ObservedAt) {
		return cu.Observation{}, false
	}
	// No capabilities or fresh action lifetime: this is DTO display metadata
	// only, and is never installed in the session with SetObservation.
	return cu.Observation{
		ID: receipt.AfterObservationID, SessionID: id,
		Width: receipt.After.Width, Height: receipt.After.Height, Screenshot: *receipt.After,
		ActiveWindow: receipt.ActiveWindowAfter, ObservedAt: receipt.CompletedAt, ExpiresAt: receipt.CompletedAt,
	}, true
}

func computerPreviewHostPermissionsAvailable(backend cu.Backend) bool {
	if backend == nil {
		return false
	}
	// The native cached-image reader does not probe TCC. Use the existing
	// non-prompting host check rather than Capabilities, which can request
	// permissions or refresh backend state. Other backends retain their own
	// image-reader checks plus the session capability guard above.
	if _, native := backend.(*macbackend.Backend); native {
		captureAllowed, inputAllowed := macbackend.CheckHostPermissions()
		return captureAllowed && inputAllowed
	}
	return true
}

// GetComputerPreview exposes existing evidence without granting a new model
// observation or extending its action lifetime.
func (a *app) GetComputerPreview(id string) (ComputerObservationDTO, error) {
	return a.computer().preview(a.windowContext(), id)
}
