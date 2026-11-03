package main

import (
	"context"
	"encoding/base64"
	"errors"
	"reflect"
	"testing"
	"time"

	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

const (
	previewTestObservationID = "model-preview-observation"
	previewTestMediaType     = "image/png"
	previewTestImage         = "existing cached image"
)

// Reuse the established fake backend, but make any capture/capability refresh
// fail. Image hooks simulate authority changes while cached bytes are read.
type previewComputerBackend struct {
	cu.FakeBackend
	t         *testing.T
	reads     []string
	readImage func(context.Context) ([]byte, string, error)
}

func (b *previewComputerBackend) Observe(context.Context, cu.ObserveRequest) (cu.Observation, error) {
	b.t.Error("preview captured the desktop")
	return cu.Observation{}, errors.New("unexpected capture")
}

func (b *previewComputerBackend) Capabilities(context.Context) (cu.Capabilities, error) {
	b.t.Error("preview refreshed capabilities")
	return cu.Capabilities{}, errors.New("unexpected capability refresh")
}

func (b *previewComputerBackend) ObservationImage(ctx context.Context, id string) ([]byte, string, error) {
	b.reads = append(b.reads, id)
	if b.readImage != nil {
		return b.readImage(ctx)
	}
	return []byte(previewTestImage), previewTestMediaType, nil
}

func newPreviewComputerManager(t *testing.T) (*computerManager, *previewComputerBackend) {
	t.Helper()
	m, _ := testComputerManager()
	if _, err := m.start(context.Background(), ComputerSessionStartInput{Approved: true}); err != nil {
		t.Fatal(err)
	}
	s := m.controller.Session()
	b := &previewComputerBackend{FakeBackend: cu.FakeBackend{CapabilitiesValue: s.Capabilities()}, t: t}
	// Deliberately keep the manager's local owner different from the model's
	// conversation owner to verify the controller's actual owner is used.
	modelOwner := cu.SessionOwner{TenantID: 4, UserID: 5, SessionID: 6}
	s, err := cu.NewComputerSession(cu.SessionOptions{Owner: modelOwner, Capabilities: s.Capabilities()})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Approve(modelOwner); err != nil {
		t.Fatal(err)
	}
	c, err := cu.NewController(s, b)
	if err != nil {
		t.Fatal(err)
	}
	m.controller, m.backend = c, b
	if err := s.SetObservation(cu.Observation{
		ID: previewTestObservationID, SessionID: s.ID(), Width: 100, Height: 100,
		Capabilities: s.Capabilities(), ObservedAt: time.Now(), Screenshot: cu.MediaRef{ID: "model-image"},
	}); err != nil {
		t.Fatal(err)
	}
	return m, b
}

func requireUnavailablePreview(t *testing.T, got ComputerObservationDTO, err error) {
	t.Helper()
	if err == nil || !reflect.DeepEqual(got, ComputerObservationDTO{}) {
		t.Fatalf("unavailable preview leaked evidence: %+v, %v", got, err)
	}
}

func TestComputerPreviewReadOnlyCurrentModelObservation(t *testing.T) {
	m, b := newPreviewComputerManager(t)
	s := m.controller.Session()
	before := computerSnapshot(s)
	controller := m.controller
	a := &app{computerManager: m, windowCtx: context.Background()}
	for range 3 {
		got, err := a.GetComputerPreview(s.ID())
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got.Observation, *before.Observation) ||
			got.ImageData != base64.StdEncoding.EncodeToString([]byte(previewTestImage)) || got.MediaType != previewTestMediaType {
			t.Fatalf("incorrect current preview: %+v", got)
		}
	}
	if !reflect.DeepEqual(b.reads, []string{previewTestObservationID, previewTestObservationID, previewTestObservationID}) {
		t.Fatalf("read something other than the current image: %v", b.reads)
	}
	if !reflect.DeepEqual(computerSnapshot(s), before) || m.controller != controller || !s.Approved() || s.ActionCount() != 0 || b.Paused || b.Stopped {
		t.Fatal("preview changed model authority, state, observation, receipt, or freshness")
	}
}

func TestComputerPreviewMissingSessionDoesNotInitializeBackend(t *testing.T) {
	m, count := testComputerManager()
	got, err := m.preview(context.Background(), "missing")
	requireUnavailablePreview(t, got, err)
	if *count != 0 || m.backend != nil || m.controller != nil {
		t.Fatal("preview initialized backend or session")
	}
}

func TestComputerPreviewRejectsUnavailableSessionBeforeImageRead(t *testing.T) {
	for _, scenario := range []string{"wrong-session", "stopped", "paused", "needs-observation", "no-observation", "unapproved", "permission-revoked", "replaced", "closed", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			m, b := newPreviewComputerManager(t)
			s := m.controller.Session()
			id := s.ID()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch scenario {
			case "wrong-session":
				id = "different-session"
			case "stopped":
				if err := m.controller.Stop(ctx, s.Owner(), id); err != nil {
					t.Fatal(err)
				}
			case "paused":
				if err := s.Pause(); err != nil {
					t.Fatal(err)
				}
			case "needs-observation":
				if err := s.Pause(); err != nil {
					t.Fatal(err)
				}
				if err := s.Resume(s.Owner()); err != nil {
					t.Fatal(err)
				}
			case "no-observation", "unapproved":
				fresh, err := cu.NewComputerSession(cu.SessionOptions{Owner: s.Owner(), Capabilities: s.Capabilities()})
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "no-observation" {
					if err := fresh.Approve(fresh.Owner()); err != nil {
						t.Fatal(err)
					}
				}
				m.controller, err = cu.NewController(fresh, b)
				if err != nil {
					t.Fatal(err)
				}
				id = fresh.ID()
			case "permission-revoked":
				revokePreviewPermission(t, s)
			case "replaced":
				replacement, _ := newPreviewComputerManager(t)
				m.controller, m.backend = replacement.controller, replacement.backend
			case "closed":
				if err := m.close(ctx); err != nil {
					t.Fatal(err)
				}
			case "canceled":
				cancel()
			}
			got, err := m.preview(ctx, id)
			requireUnavailablePreview(t, got, err)
			if len(b.reads) != 0 {
				t.Fatal("unavailable session accessed cached image")
			}
		})
	}
}

func revokePreviewPermission(t *testing.T, s *cu.ComputerSession) {
	t.Helper()
	caps := s.Capabilities()
	caps.PermissionState = cu.PermissionRequired
	caps.CaptureReadiness = cu.ReadinessPermissionRequired
	if err := s.UpdateCapabilities(caps); err != nil {
		t.Fatal(err)
	}
}

func TestComputerPreviewDiscardsImageWhenAuthorityChangesDuringRead(t *testing.T) {
	for _, scenario := range []string{"stopped", "observation-changed", "permission-revoked", "controller-replaced", "session-replaced", "closed", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			m, b := newPreviewComputerManager(t)
			s := m.controller.Session()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			b.readImage = func(context.Context) ([]byte, string, error) {
				switch scenario {
				case "stopped":
					if _, err := m.control(ctx, s.ID(), cu.ActionStop); err != nil {
						t.Fatal(err)
					}
				case "observation-changed":
					o, _ := s.CurrentObservation()
					o.ID = "new-model-observation"
					if err := s.SetObservation(o); err != nil {
						t.Fatal(err)
					}
				case "permission-revoked":
					revokePreviewPermission(t, s)
				case "controller-replaced":
					c, err := cu.NewController(s, b)
					if err != nil {
						t.Fatal(err)
					}
					m.mu.Lock()
					m.controller = c
					m.mu.Unlock()
				case "session-replaced":
					replacement, _ := newPreviewComputerManager(t)
					m.mu.Lock()
					m.controller, m.backend = replacement.controller, replacement.backend
					m.mu.Unlock()
				case "closed":
					if err := m.close(ctx); err != nil {
						t.Fatal(err)
					}
				case "canceled":
					cancel()
				}
				return []byte(previewTestImage), previewTestMediaType, nil
			}
			got, err := m.preview(ctx, s.ID())
			requireUnavailablePreview(t, got, err)
			if len(b.reads) != 1 {
				t.Fatalf("preview retried an invalidated image: %v", b.reads)
			}
		})
	}
}

func TestComputerPreviewReadFailureDoesNotPauseOrMutateAuthority(t *testing.T) {
	readErr := errors.New("cached image unavailable or permission revoked")
	for _, scenario := range []string{"read-error", "empty-image", "missing-media-type"} {
		t.Run(scenario, func(t *testing.T) {
			m, b := newPreviewComputerManager(t)
			s := m.controller.Session()
			before := computerSnapshot(s)
			b.readImage = func(context.Context) ([]byte, string, error) {
				switch scenario {
				case "read-error":
					return []byte(previewTestImage), previewTestMediaType, readErr
				case "empty-image":
					return nil, previewTestMediaType, nil
				default:
					return []byte(previewTestImage), "", nil
				}
			}
			got, err := m.preview(context.Background(), s.ID())
			requireUnavailablePreview(t, got, err)
			if scenario == "read-error" && !errors.Is(err, readErr) {
				t.Fatalf("lost reader error: %v", err)
			}
			if !reflect.DeepEqual(computerSnapshot(s), before) || !s.Approved() || b.Paused || b.Stopped {
				t.Fatal("preview failure mutated model authority")
			}
		})
	}
}

func TestComputerPreviewDoesNotRefreshExpiredObservation(t *testing.T) {
	m, b := newPreviewComputerManager(t)
	now := time.Unix(100, 0)
	s, err := cu.NewComputerSession(cu.SessionOptions{
		Owner: m.controller.Session().Owner(), Capabilities: b.CapabilitiesValue,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Approve(s.Owner()); err != nil {
		t.Fatal(err)
	}
	if err := s.SetObservation(cu.Observation{
		ID: previewTestObservationID, SessionID: s.ID(), Width: 100, Height: 100,
		Capabilities: s.Capabilities(), ObservedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	m.controller, err = cu.NewController(s, b)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := s.CurrentObservation()
	now = before.ExpiresAt.Add(time.Second)
	got, err := m.preview(context.Background(), s.ID())
	if err != nil || !reflect.DeepEqual(got.Observation, before) || !got.Observation.Expired(now) {
		t.Fatalf("displaying existing evidence refreshed its action lifetime: %+v, %v", got, err)
	}
	after, _ := s.CurrentObservation()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("preview refreshed model observation")
	}
}

func TestComputerPreviewStopDoesNotWaitForBlockedImageRead(t *testing.T) {
	m, b := newPreviewComputerManager(t)
	s := m.controller.Session()
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	b.readImage = func(context.Context) ([]byte, string, error) {
		close(entered)
		<-release
		return []byte(previewTestImage), previewTestMediaType, nil
	}
	type previewResult struct {
		value ComputerObservationDTO
		err   error
	}
	done := make(chan previewResult, 1)
	go func() {
		got, err := m.preview(context.Background(), s.ID())
		done <- previewResult{value: got, err: err}
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("image read never started")
	}
	stopped := make(chan error, 1)
	go func() {
		_, err := m.control(context.Background(), s.ID(), cu.ActionStop)
		stopped <- err
	}()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("preview blocked Stop")
	}
	// Unblock the image read without closing release twice in deferred cleanup.
	release <- struct{}{}
	select {
	case result := <-done:
		requireUnavailablePreview(t, result.value, result.err)
	case <-time.After(time.Second):
		t.Fatal("preview did not discard stopped image")
	}
}

func TestComputerPreviewUnavailableWithoutImageReader(t *testing.T) {
	m, b := newPreviewComputerManager(t)
	s := m.controller.Session()
	before := computerSnapshot(s)
	backend := &cu.FakeBackend{CapabilitiesValue: b.CapabilitiesValue}
	c, err := cu.NewController(s, backend)
	if err != nil {
		t.Fatal(err)
	}
	m.controller, m.backend = c, backend
	got, err := m.preview(context.Background(), s.ID())
	requireUnavailablePreview(t, got, err)
	if !reflect.DeepEqual(computerSnapshot(s), before) {
		t.Fatal("missing image reader changed model authority")
	}
}

func TestComputerPreviewWithoutWindowContextIsUnavailable(t *testing.T) {
	m, b := newPreviewComputerManager(t)
	a := &app{computerManager: m}
	got, err := a.GetComputerPreview(m.controller.Session().ID())
	requireUnavailablePreview(t, got, err)
	if len(b.reads) != 0 {
		t.Fatal("preview read an image without an active window context")
	}
}

func TestComputerPreviewCanReadNewModelObservationWhilePaused(t *testing.T) {
	m, _ := newPreviewComputerManager(t)
	s := m.controller.Session()
	o, _ := s.CurrentObservation()
	if err := s.Pause(); err != nil {
		t.Fatal(err)
	}
	// The model can explicitly observe while paused. Only that new current
	// evidence is previewable, not the observation invalidated by Pause.
	o.ID = "model-observed-while-paused"
	if err := s.SetObservation(o); err != nil {
		t.Fatal(err)
	}
	before := computerSnapshot(s)
	got, err := m.preview(context.Background(), s.ID())
	if err != nil || got.Observation.ID != o.ID || !reflect.DeepEqual(computerSnapshot(s), before) || s.State() != cu.SessionPaused {
		t.Fatalf("preview changed or rejected current paused-model evidence: %+v, %v", got, err)
	}
}

const previewTestAfterObservationID = "model-action-after"

func executePreviewTestAction(t *testing.T, m *computerManager, b *previewComputerBackend, actionID string) cu.ActionReceipt {
	t.Helper()
	s := m.controller.Session()
	o, current := s.CurrentObservation()
	if !current {
		t.Fatal("test action requires current observation")
	}
	caps := s.Capabilities()
	caps.Actions = []cu.ActionKind{cu.ActionWait}
	if err := s.UpdateCapabilities(caps); err != nil {
		t.Fatal(err)
	}
	r, err := m.controller.Execute(context.Background(), s.Owner(), cu.Action{
		ID: actionID, SessionID: s.ID(), ObservationID: o.ID, Kind: cu.ActionWait,
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func newAfterPreviewComputerManager(t *testing.T) (*computerManager, *previewComputerBackend, cu.ActionReceipt) {
	t.Helper()
	m, b := newPreviewComputerManager(t)
	b.ReceiptValue = cu.ActionReceipt{
		Outcome: cu.OutcomeExecuted, AfterObservationID: previewTestAfterObservationID,
		After:             &cu.MediaRef{ID: previewTestAfterObservationID, MediaType: previewTestMediaType, Width: 120, Height: 80},
		ActiveWindowAfter: cu.WindowRef{ID: "after-window"},
	}
	r := executePreviewTestAction(t, m, b, "preview-action")
	return m, b, r
}

func TestComputerPreviewAfterActionIsDisplayOnly(t *testing.T) {
	m, b, receipt := newAfterPreviewComputerManager(t)
	s := m.controller.Session()
	before, actionCount := computerSnapshot(s), s.ActionCount()
	consumed, current := s.CurrentObservation()
	if current || before.State != cu.SessionNeedsObservation {
		t.Fatal("executed action did not consume observation")
	}
	for range 3 {
		got, err := m.preview(context.Background(), s.ID())
		if err != nil {
			t.Fatal(err)
		}
		o := got.Observation
		if o.ID != receipt.AfterObservationID || o.SessionID != s.ID() || o.Width != receipt.After.Width || o.Height != receipt.After.Height ||
			!reflect.DeepEqual(o.Screenshot, *receipt.After) || !reflect.DeepEqual(o.ActiveWindow, receipt.ActiveWindowAfter) ||
			!o.ObservedAt.Equal(receipt.CompletedAt) || !o.Expired(time.Now()) || o.Capabilities.Ready() ||
			got.ImageData != base64.StdEncoding.EncodeToString([]byte(previewTestImage)) || got.MediaType != previewTestMediaType {
			t.Fatalf("incorrect display-only after metadata: %+v", got)
		}
		if err := s.ValidateAction(cu.Action{ID: "forbidden-preview-action", SessionID: s.ID(), ObservationID: o.ID, Kind: cu.ActionWait}); err == nil {
			t.Fatal("after-action preview granted action authority")
		}
	}
	after, current := s.CurrentObservation()
	if current || !reflect.DeepEqual(after, consumed) || !reflect.DeepEqual(computerSnapshot(s), before) ||
		s.ActionCount() != actionCount || !s.Approved() || b.Paused || b.Stopped || len(b.Executed) != 1 {
		t.Fatal("after preview mutated session authority or freshness")
	}
	if !reflect.DeepEqual(b.reads, []string{previewTestAfterObservationID, previewTestAfterObservationID, previewTestAfterObservationID}) {
		t.Fatalf("wrong after-image reads: %v", b.reads)
	}
}

func TestComputerPreviewRejectsInvalidAfterReceiptMetadata(t *testing.T) {
	m, _, valid := newAfterPreviewComputerManager(t)
	s := m.controller.Session()
	consumed, _ := s.CurrentObservation()
	for _, scenario := range []string{"unknown", "rejected", "failed", "wrong-session", "missing-action", "missing-after", "missing-id", "mismatched-id", "missing-dimensions", "missing-media", "missing-time", "stale-before", "stale-time", "reused-before-id"} {
		t.Run(scenario, func(t *testing.T) {
			r := valid
			after := *valid.After
			r.After = &after
			switch scenario {
			case "unknown":
				r.Outcome = cu.OutcomeUnknown
			case "rejected":
				r.Outcome = cu.OutcomeRejected
			case "failed":
				r.Outcome = cu.OutcomeFailed
			case "wrong-session":
				r.SessionID = "different-session"
			case "missing-action":
				r.ActionID = ""
			case "missing-after":
				r.After = nil
			case "missing-id":
				r.AfterObservationID = ""
			case "mismatched-id":
				r.After.ID = "different-image"
			case "missing-dimensions":
				r.After.Width = 0
			case "missing-media":
				r.After.MediaType = ""
			case "missing-time":
				r.CompletedAt = time.Time{}
			case "stale-before":
				r.BeforeObservationID = "older-observation"
			case "stale-time":
				r.CompletedAt = consumed.ObservedAt.Add(-time.Second)
			case "reused-before-id":
				r.AfterObservationID, r.After.ID = consumed.ID, consumed.ID
			}
			got, ok := computerPreviewReceiptObservation(s.ID(), consumed, r)
			if ok || !reflect.DeepEqual(got, cu.Observation{}) {
				t.Fatalf("accepted invalid receipt evidence: %+v", got)
			}
		})
	}
}

func TestComputerPreviewAfterReceiptInvalidation(t *testing.T) {
	for _, scenario := range []string{"stopped", "paused", "permission-revoked", "stale-receipt", "latest-unknown", "latest-mismatch"} {
		t.Run(scenario, func(t *testing.T) {
			m, b, _ := newAfterPreviewComputerManager(t)
			s := m.controller.Session()
			switch scenario {
			case "stopped":
				if err := s.Stop(s.Owner()); err != nil {
					t.Fatal(err)
				}
			case "paused":
				if err := s.Pause(); err != nil {
					t.Fatal(err)
				}
			case "permission-revoked":
				revokePreviewPermission(t, s)
			default:
				o, _ := s.CurrentObservation()
				o.ID = "new-model-observation"
				if err := s.SetObservation(o); err != nil {
					t.Fatal(err)
				}
				if scenario == "stale-receipt" {
					if err := s.Pause(); err != nil {
						t.Fatal(err)
					}
					if err := s.Resume(s.Owner()); err != nil {
						t.Fatal(err)
					}
				} else {
					if scenario == "latest-unknown" {
						b.ReceiptValue.Outcome = cu.OutcomeUnknown
					} else {
						b.ReceiptValue.AfterObservationID = "mismatched-after"
					}
					executePreviewTestAction(t, m, b, "next-preview-action")
				}
			}
			before := computerSnapshot(s)
			got, err := m.preview(context.Background(), s.ID())
			requireUnavailablePreview(t, got, err)
			if len(b.reads) != 0 || !reflect.DeepEqual(computerSnapshot(s), before) {
				t.Fatal("invalid receipt preview read evidence or mutated session")
			}
		})
	}
}

func TestComputerPreviewRechecksAfterEvidenceSourceDuringRead(t *testing.T) {
	for _, scenario := range []string{"stop", "permission-revoked", "replacement", "new-current-same-image-id", "new-receipt-same-image-id"} {
		t.Run(scenario, func(t *testing.T) {
			m, b, receipt := newAfterPreviewComputerManager(t)
			s := m.controller.Session()
			b.readImage = func(ctx context.Context) ([]byte, string, error) {
				switch scenario {
				case "stop":
					if _, err := m.control(ctx, s.ID(), cu.ActionStop); err != nil {
						t.Fatal(err)
					}
				case "permission-revoked":
					revokePreviewPermission(t, s)
				case "replacement":
					c, err := cu.NewController(s, b)
					if err != nil {
						t.Fatal(err)
					}
					m.mu.Lock()
					m.controller = c
					m.mu.Unlock()
				default:
					o, _ := s.CurrentObservation()
					o.ID = receipt.AfterObservationID
					if scenario == "new-receipt-same-image-id" {
						o.ID = "next-action-observation"
					}
					if err := s.SetObservation(o); err != nil {
						t.Fatal(err)
					}
					if scenario == "new-receipt-same-image-id" {
						executePreviewTestAction(t, m, b, "second-preview-action")
					}
				}
				return []byte(previewTestImage), previewTestMediaType, nil
			}
			got, err := m.preview(context.Background(), s.ID())
			requireUnavailablePreview(t, got, err)
			if len(b.reads) != 1 {
				t.Fatal("preview retried changed after evidence")
			}
		})
	}
}
