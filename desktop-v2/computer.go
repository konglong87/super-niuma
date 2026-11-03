package main

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	macbackend "github.com/konglong87/super-niuma/internal/computerbackend/macos"
	"github.com/konglong87/super-niuma/internal/computerbridge"
	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

const (
	computerHelperName     = "computer-helper-macos"
	computerHelperEnv      = "NIUMA_COMPUTER_HELPER"
	computerRequestTimeout = 10 * time.Second
	// This identity is local to the Wails-only control surface. It is NOT a
	// tenant runtime credential and must never be injected into a model Query.
	localComputerTenantID uint64 = 1
	localComputerUserID   uint64 = 1
)

// This non-conversation owner is used only for the local, read-only lifecycle
// probe between the Wails client and the standalone runtime process.
var computerRuntimeSupervisionOwner = cu.SessionOwner{TenantID: 1, UserID: 1, SessionID: 1}

// The UI bridge owner is not a conversation owner and is accepted only by the
// standalone runtime's local control adapter. It never appears in model tool
// requests or grants model authority.
var computerUIBridgeOwner = cu.SessionOwner{TenantID: 1, UserID: 1, SessionID: 2}

type ComputerSessionStartInput struct {
	Approved        bool   `json:"approved"`
	ConversationRef string `json:"conversation_ref,omitempty"`
}
type ComputerCapabilitiesDTO struct {
	Capabilities cu.Capabilities `json:"capabilities"`
	Available    bool            `json:"available"`
	ErrorCode    string          `json:"error_code,omitempty"`
	ErrorMessage string          `json:"error_message,omitempty"`
}
type ComputerObservationDTO struct {
	Observation cu.Observation `json:"observation"`
	ImageData   string         `json:"image_data,omitempty"`
	MediaType   string         `json:"media_type,omitempty"`
}
type ComputerSessionDTO struct {
	ID           string            `json:"session_id"`
	State        cu.SessionState   `json:"state"`
	Owner        cu.SessionOwner   `json:"owner"`
	OwnerKind    string            `json:"owner_kind"`
	Capabilities cu.Capabilities   `json:"capabilities"`
	Observation  *cu.Observation   `json:"observation,omitempty"`
	LastReceipt  *cu.ActionReceipt `json:"last_receipt,omitempty"`
}

type ComputerRuntimeFaultDTO struct {
	State       cu.RuntimeState `json:"state"`
	ErrorCode   string          `json:"error_code"`
	Recoverable bool            `json:"recoverable"`
	SessionID   string          `json:"session_id,omitempty"`
	ActionID    string          `json:"action_id,omitempty"`
}

type ComputerRuntimeStatusDTO struct {
	State         cu.RuntimeState          `json:"state"`
	Fault         *ComputerRuntimeFaultDTO `json:"fault,omitempty"`
	ActiveSession bool                     `json:"active_session"`
	SessionID     string                   `json:"session_id,omitempty"`
	Available     bool                     `json:"available"`
	ErrorCode     string                   `json:"error_code,omitempty"`
}

type computerBackendFactory func(context.Context) (cu.Backend, error)

const computerTargetWorkBuddy cu.TargetID = "workbuddy"

// desktopComputerTargets is the fixture registry for the local desktop app.
// The Computer Use core remains target-agnostic; production integrations can
// replace this registry without changing the controller, tool, or native host.
func desktopComputerTargets() *cu.StaticTargetRegistry {
	registry, err := cu.NewStaticTargetRegistry(
		cu.ApplicationTarget{
			ID:          computerTargetWorkBuddy,
			DisplayName: "WorkBuddy",
			Launch:      cu.LaunchPolicy{ProviderKey: "com.workbuddy.workbuddy"},
			Window:      cu.WindowPolicy{BundleID: "com.workbuddy.workbuddy", RequireVisible: true},
		},
		cu.ApplicationTarget{
			ID:          "calculator",
			DisplayName: "Calculator",
			Launch:      cu.LaunchPolicy{ProviderKey: "com.apple.calculator"},
			Window:      cu.WindowPolicy{BundleID: "com.apple.calculator", RequireVisible: true},
		},
		cu.ApplicationTarget{
			ID:          "textedit",
			DisplayName: "TextEdit",
			Launch:      cu.LaunchPolicy{ProviderKey: "com.apple.TextEdit"},
			Window:      cu.WindowPolicy{BundleID: "com.apple.TextEdit", RequireVisible: true},
		},
	)
	if err != nil {
		panic(err)
	}
	return registry
}

// Platform construction is injected; control/snapshot logic depends only on
// the domain controller. Windows will supply a factory, not a second manager.
type computerManager struct {
	mu                  sync.Mutex
	backend             cu.Backend
	controller          *cu.Controller
	owner               cu.SessionOwner
	factory             computerBackendFactory
	runtimeCtx          context.Context
	events              *cu.EventBus
	visualCues          *cu.VisualCueBus
	remote              *computerbridge.Client
	remoteOwner         cu.SessionOwner
	runtimeStopFallback func(context.Context) error
	remoteSession       string
	attemptsMu          sync.Mutex
	attempts            map[computerStartAttemptKey]string
}

type computerStartAttemptKey struct {
	owner     cu.SessionOwner
	attemptID string
}

func newRemoteComputerManager(client *computerbridge.Client, fallback ...func(context.Context) error) *computerManager {
	var runtimeStopFallback func(context.Context) error
	if len(fallback) > 0 {
		runtimeStopFallback = fallback[0]
	}
	return &computerManager{
		owner:               cu.SessionOwner{TenantID: localComputerTenantID, UserID: localComputerUserID},
		remote:              client,
		remoteOwner:         computerUIBridgeOwner,
		runtimeStopFallback: runtimeStopFallback,
		visualCues:          cu.NewVisualCueBus(),
		attempts:            make(map[computerStartAttemptKey]string),
	}
}

func newComputerManager() *computerManager {
	return newComputerManagerWithRuntime(context.Background(), cu.NewEventBus())
}

func newComputerManagerWithRuntime(runtimeCtx context.Context, events *cu.EventBus) *computerManager {
	if runtimeCtx == nil {
		runtimeCtx = context.Background()
	}
	if events == nil {
		events = cu.NewEventBus()
	}
	return &computerManager{owner: cu.SessionOwner{TenantID: localComputerTenantID, UserID: localComputerUserID}, factory: newComputerBackend, runtimeCtx: runtimeCtx, events: events, visualCues: cu.NewVisualCueBus(), attempts: make(map[computerStartAttemptKey]string)}
}

func (m *computerManager) activeSessionID() string {
	if m == nil {
		return ""
	}
	if m.remote != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		status, err := m.remote.RuntimeStatus(ctx, computerRuntimeSupervisionOwner)
		if err != nil || !status.ActiveSession {
			return ""
		}
		return status.SessionID
	}
	m.mu.Lock()
	controller := m.controller
	m.mu.Unlock()
	if controller == nil {
		return ""
	}
	state := controller.Session().State()
	if state == cu.SessionStopped || state == cu.SessionFailed {
		return ""
	}
	return controller.Session().ID()
}

func (m *computerManager) hasActiveSession() bool {
	if m == nil {
		return false
	}
	if m.remote != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		status, err := m.remote.RuntimeStatus(ctx, computerRuntimeSupervisionOwner)
		return err == nil && status.ActiveSession
	}
	m.mu.Lock()
	controller := m.controller
	m.mu.Unlock()
	if controller == nil {
		return false
	}
	state := controller.Session().State()
	return state != cu.SessionStopped && state != cu.SessionFailed
}

const computerRuntimeStatusUnavailable = "runtime_status_unavailable"

func publicComputerRuntimeFaultCode(code string) string {
	code = strings.TrimSpace(code)
	if code == "" {
		return ""
	}
	switch code {
	case cu.RuntimeFaultWorkerExited, cu.RuntimeFaultBridgeDisconnected, cu.RuntimeFaultHelperUnavailable,
		cu.RuntimeFaultPermissionRevoked, cu.RuntimeFaultHelperUncertain, cu.RuntimeFaultStopFailed,
		computerRuntimeStatusUnavailable:
		return code
	default:
		return computerRuntimeStatusUnavailable
	}
}

func computerRuntimeFaultDTO(fault *cu.RuntimeFault) *ComputerRuntimeFaultDTO {
	if fault == nil {
		return nil
	}
	return &ComputerRuntimeFaultDTO{
		State:       fault.State,
		ErrorCode:   publicComputerRuntimeFaultCode(fault.ErrorCode),
		Recoverable: fault.Recoverable,
		SessionID:   fault.SessionID,
		ActionID:    fault.ActionID,
	}
}

func computerRuntimeStatusDTO(status cu.RuntimeStatus, available bool, errorCode string) ComputerRuntimeStatusDTO {
	return ComputerRuntimeStatusDTO{
		State:         status.State,
		Fault:         computerRuntimeFaultDTO(status.Fault),
		ActiveSession: status.ActiveSession,
		SessionID:     status.SessionID,
		Available:     available,
		ErrorCode:     publicComputerRuntimeFaultCode(errorCode),
	}
}

func (m *computerManager) runtimeStatusForUI(ctx context.Context) ComputerRuntimeStatusDTO {
	if m == nil {
		return ComputerRuntimeStatusDTO{State: cu.RuntimeFaulted, Available: false, ErrorCode: computerRuntimeStatusUnavailable}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if m.remote != nil {
		status, err := m.remote.RuntimeStatus(ctx, computerRuntimeSupervisionOwner)
		if err == nil {
			return ComputerRuntimeStatusDTO{
				State:         status.State,
				Fault:         computerRuntimeFaultDTO(status.Fault),
				ActiveSession: status.ActiveSession,
				SessionID:     status.SessionID,
				Available:     true,
			}
		}
		m.mu.Lock()
		sessionID := m.remoteSession
		m.mu.Unlock()
		if errors.Is(err, computerbridge.ErrTransport) {
			status := cu.RuntimeStatus{
				State:         cu.RuntimeFaulted,
				ActiveSession: sessionID != "",
				SessionID:     sessionID,
				Fault: &cu.RuntimeFault{
					State:       cu.RuntimeFaulted,
					ErrorCode:   cu.RuntimeFaultBridgeDisconnected,
					Recoverable: false,
					SessionID:   sessionID,
					OccurredAt:  time.Now(),
				},
			}
			return computerRuntimeStatusDTO(status, false, cu.RuntimeFaultBridgeDisconnected)
		}
		faultStatus := cu.RuntimeStatus{
			State:         cu.RuntimeFaulted,
			ActiveSession: sessionID != "",
			SessionID:     sessionID,
			Fault: &cu.RuntimeFault{
				State:       cu.RuntimeFaulted,
				ErrorCode:   computerRuntimeStatusUnavailable,
				Recoverable: false,
				SessionID:   sessionID,
				OccurredAt:  time.Now(),
			},
		}
		return computerRuntimeStatusDTO(faultStatus, false, computerRuntimeStatusUnavailable)
	}
	return computerRuntimeStatusDTO(m.runtimeStatus(), true, "")
}

func (m *computerManager) runtimeStatus() cu.RuntimeStatus {
	status := cu.RuntimeStatus{State: cu.RuntimeReady}
	if m == nil {
		return status
	}
	m.mu.Lock()
	controller, backend := m.controller, m.backend
	m.mu.Unlock()
	if controller == nil {
		return status
	}
	session := controller.Session()
	state := session.State()
	if state != cu.SessionStopped {
		status.ActiveSession = state != cu.SessionFailed
		status.SessionID = session.ID()
	}
	switch state {
	case cu.SessionPaused:
		status.State = cu.RuntimeDegraded
		// A user-requested Pause is a normal control state, not a helper fault.
		// A quarantined backend reports a stable helper fault without requiring
		// a new IPC probe or changing authority.
		if reporter, ok := backend.(cu.BackendFaultReporter); ok {
			if code := cu.PublicErrorCode(reporter.RuntimeFaultCode()); code == cu.ErrorCodeHelperUnavailable {
				status.Fault = &cu.RuntimeFault{State: status.State, ErrorCode: code, Recoverable: true, SessionID: session.ID(), OccurredAt: time.Now()}
			}
		}
		if status.Fault == nil {
			if caps := session.Capabilities(); !caps.Ready() {
				code := cu.RuntimeFaultPermissionRevoked
				if caps.ReadinessErrorCode() != cu.ErrorCodePermissionRequired {
					code = cu.RuntimeFaultHelperUnavailable
				}
				status.Fault = &cu.RuntimeFault{State: status.State, ErrorCode: code, Recoverable: true, SessionID: session.ID(), OccurredAt: time.Now()}
			}
		}
	case cu.SessionFailed:
		status.State = cu.RuntimeFaulted
		code := cu.RuntimeFaultHelperUncertain
		if !session.InputUncertain() {
			code = cu.RuntimeFaultHelperUnavailable
		}
		status.Fault = &cu.RuntimeFault{State: status.State, ErrorCode: code, Recoverable: false, SessionID: session.ID(), OccurredAt: time.Now()}
	}
	return status
}

func (m *computerManager) runtimeContext() context.Context {
	if m.runtimeCtx != nil {
		return m.runtimeCtx
	}
	return context.Background()
}

func (m *computerManager) rememberStartAttempt(owner cu.SessionOwner, attemptID, sessionID string) {
	if strings.TrimSpace(attemptID) == "" || strings.TrimSpace(sessionID) == "" {
		return
	}
	m.attemptsMu.Lock()
	if m.attempts == nil {
		m.attempts = make(map[computerStartAttemptKey]string)
	}
	m.attempts[computerStartAttemptKey{owner: owner, attemptID: attemptID}] = sessionID
	m.attemptsMu.Unlock()
}

func (m *computerManager) resolveStartAttempt(owner cu.SessionOwner, attemptID string) string {
	m.attemptsMu.Lock()
	id := m.attempts[computerStartAttemptKey{owner: owner, attemptID: attemptID}]
	m.attemptsMu.Unlock()
	return id
}
func newComputerBackend(ctx context.Context) (cu.Backend, error) {
	if runtime.GOOS != "darwin" {
		return nil, errors.New("Computer Use backend is unavailable on this platform")
	}
	path, err := locateComputerHelper()
	if err != nil {
		return nil, err
	}
	return macbackend.New(ctx, macbackend.Config{
		HelperPath:             path,
		HostBundleID:           cu.NiumaHostBundleID,
		RequestTimeout:         computerRequestTimeout,
		RequestHostPermissions: macbackend.RequestHostPermissions,
		CheckHostPermissions:   macbackend.CheckHostPermissions,
		AllowedProviderKeys:    []string{"com.workbuddy.workbuddy", "com.apple.calculator", "com.apple.TextEdit"},
	})
}
func locateComputerHelper() (string, error) {
	if value := strings.TrimSpace(os.Getenv(computerHelperEnv)); value != "" {
		if !filepath.IsAbs(value) {
			return "", errors.New("computer helper path must be absolute")
		}
		return value, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	candidates := []string{
		filepath.Join(filepath.Dir(executable), "..", "Helpers", "ComputerHelper.app", "Contents", "MacOS", computerHelperName),
		filepath.Join(filepath.Dir(executable), "..", "Helpers", computerHelperName),
	}
	for _, path := range candidates {
		if stat, statErr := os.Stat(path); statErr == nil && !stat.IsDir() {
			return path, nil
		}
	}
	return "", errors.New("computer helper is not bundled")
}
func (m *computerManager) ensureBackendLocked(ctx context.Context) (cu.Backend, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.backend != nil {
		return m.backend, nil
	}
	// Request cancellation may deny this client, but only the supervisor owns
	// the helper process lifetime. A UI capabilities probe can be the first
	// caller and must not attach a reused helper to its window context.
	lifetime := m.runtimeContext()
	if err := lifetime.Err(); err != nil {
		return nil, err
	}
	b, err := m.factory(lifetime)
	if err != nil {
		return nil, err
	}
	m.backend = b
	return b, nil
}
func (m *computerManager) capabilities(ctx context.Context) (cu.Capabilities, error) {
	if m.remote != nil {
		m.mu.Lock()
		id := m.remoteSession
		m.mu.Unlock()
		return m.remote.Capabilities(ctx, m.remoteOwner, id)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	caps, err := m.readCapabilitiesLocked(ctx)
	if err != nil {
		return cu.Capabilities{}, err
	}
	if m.controller != nil {
		s := m.controller.Session()
		if s.State() != cu.SessionStopped && s.State() != cu.SessionFailed {
			if err := s.UpdateCapabilities(caps); err != nil {
				return cu.Capabilities{}, err
			}
		}
	}
	return caps, nil
}
func (m *computerManager) start(ctx context.Context, in ComputerSessionStartInput) (ComputerSessionDTO, error) {
	return m.startOwned(ctx, in, m.owner)
}

func (m *computerManager) startOwned(ctx context.Context, in ComputerSessionStartInput, owner cu.SessionOwner) (ComputerSessionDTO, error) {
	return m.startOwnedWithLifetime(ctx, m.runtimeContext(), in, owner)
}

// The host owns the helper lifetime; approval belongs to the live caller.
func (m *computerManager) startWithLifetime(ctx, lifetime context.Context, in ComputerSessionStartInput) (ComputerSessionDTO, error) {
	return m.startOwnedWithLifetime(ctx, lifetime, in, cu.SessionOwner{TenantID: localComputerTenantID, UserID: localComputerUserID})
}

// Ownership is immutable for a controller. Never upgrade local preview approval
// or silently move a still-live capability to a newly selected conversation.
func (m *computerManager) startOwnedWithLifetime(ctx, lifetime context.Context, in ComputerSessionStartInput, owner cu.SessionOwner) (ComputerSessionDTO, error) {
	if m.remote != nil {
		if owner != m.owner {
			return ComputerSessionDTO{}, errors.New("managed conversation sessions are provider-owned")
		}
		if err := ctx.Err(); err != nil {
			return ComputerSessionDTO{}, err
		}
		if err := lifetime.Err(); err != nil {
			return ComputerSessionDTO{}, err
		}
		snapshot, err := m.remote.StartLocal(ctx, m.remoteOwner, in.Approved)
		if err != nil {
			return ComputerSessionDTO{}, err
		}
		m.mu.Lock()
		m.remoteSession = snapshot.SessionID
		m.mu.Unlock()
		return computerSnapshotFromBridge(snapshot), nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ComputerSessionDTO{}, err
	}
	if err := lifetime.Err(); err != nil {
		return ComputerSessionDTO{}, err
	}
	if m.controller != nil {
		s := m.controller.Session()
		if s.State() != cu.SessionStopped && s.State() != cu.SessionFailed {
			if !s.Owns(owner) {
				return ComputerSessionDTO{}, errors.New("stop the current computer session before authorizing another conversation")
			}
			if in.Approved && s.State() == cu.SessionPendingApproval {
				if err := s.Approve(owner); err != nil {
					return ComputerSessionDTO{}, err
				}
			}
			if _, err := m.refreshSessionCapabilitiesLocked(ctx, s); err != nil {
				return ComputerSessionDTO{}, err
			}
			return computerSnapshot(s), nil
		}
		// A stopped helper must not be reused for a newly approved session.
		if err := m.controller.Close(ctx); err != nil {
			return ComputerSessionDTO{}, err
		}
		m.backend = nil
		m.controller = nil
	}
	b, err := m.ensureBackendLocked(lifetime)
	if err != nil {
		return ComputerSessionDTO{}, err
	}
	caps, err := m.readCapabilitiesLocked(ctx)
	if err != nil {
		return ComputerSessionDTO{}, err
	}
	// readCapabilitiesLocked may replace a crashed helper; bind the new
	// controller to the backend that actually answered the probe.
	b = m.backend
	if err := ctx.Err(); err != nil {
		return ComputerSessionDTO{}, err
	}
	s, err := cu.NewComputerSession(cu.SessionOptions{Owner: owner, Capabilities: caps})
	if err != nil {
		return ComputerSessionDTO{}, err
	}
	c, err := cu.NewControllerWithRegistry(s, b, desktopComputerTargets())
	if err != nil {
		return ComputerSessionDTO{}, err
	}
	m.controller = c
	if m.events != nil {
		c.SetTurnPublisher(m.events)
	}
	if m.visualCues != nil {
		c.SetVisualCuePublisher(m.visualCues)
	}
	if in.Approved {
		if err := s.Approve(owner); err != nil {
			return ComputerSessionDTO{}, err
		}
	}
	return computerSnapshot(s), nil
}
func (m *computerManager) refreshSessionCapabilitiesLocked(ctx context.Context, s *cu.ComputerSession) (cu.Capabilities, error) {
	caps, err := m.readCapabilitiesLocked(ctx)
	if err != nil {
		return cu.Capabilities{}, err
	}
	if err := s.UpdateCapabilities(caps); err != nil {
		return cu.Capabilities{}, err
	}
	return caps, nil
}

// readCapabilitiesLocked retries once with a fresh helper when the current
// helper transport has died. The session object and approval remain intact,
// but no observation or action is replayed; callers must observe again.
func (m *computerManager) readCapabilitiesLocked(ctx context.Context) (cu.Capabilities, error) {
	b, err := m.ensureBackendLocked(ctx)
	if err != nil {
		return cu.Capabilities{}, err
	}
	caps, err := b.Capabilities(ctx)
	if err == nil || ctx.Err() != nil {
		return caps, err
	}
	if replaceErr := m.replaceBackendLocked(ctx); replaceErr != nil {
		return cu.Capabilities{}, err
	}
	return m.backend.Capabilities(ctx)
}

func (m *computerManager) replaceBackendLocked(ctx context.Context) error {
	if m.backend != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), computerRequestTimeout)
		_ = m.backend.Close(closeCtx)
		cancel()
	}
	m.backend = nil
	b, err := m.ensureBackendLocked(ctx)
	if err != nil {
		return err
	}
	if m.controller != nil {
		// A replacement helper has no trustworthy observation/action state.
		// Revoke the old observation before exposing the new controller; the
		// caller must observe again and no input is replayed.
		_ = m.controller.Session().Pause()
		c, err := cu.NewControllerWithRegistry(m.controller.Session(), b, desktopComputerTargets())
		if err != nil {
			return err
		}
		m.controller = c
		if m.events != nil {
			c.SetTurnPublisher(m.events)
		}
		if m.visualCues != nil {
			c.SetVisualCuePublisher(m.visualCues)
		}
	}
	return nil
}

func (m *computerManager) active(id string) (*cu.Controller, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.controller == nil || m.controller.Session().ID() != id {
		return nil, errors.New("computer session not found")
	}
	return m.controller, nil
}
func computerBridgeSnapshot(s *cu.ComputerSession) computerbridge.SessionSnapshotResponse {
	if s == nil {
		return computerbridge.SessionSnapshotResponse{}
	}
	result := computerbridge.SessionSnapshotResponse{SessionID: s.ID(), State: s.State(), Owner: s.Owner(), Approved: s.Approved(), Capabilities: s.Capabilities()}
	if o, ok := s.CurrentObservation(); ok {
		result.Observation = &o
	}
	if r, ok := s.LastReceipt(); ok {
		result.LastReceipt = &r
	}
	return result
}

func computerSnapshotFromBridge(s computerbridge.SessionSnapshotResponse) ComputerSessionDTO {
	ownerKind := "managed_conversation"
	if s.Owner == (cu.SessionOwner{TenantID: localComputerTenantID, UserID: localComputerUserID}) {
		ownerKind = "local_preview"
	}
	return ComputerSessionDTO{ID: s.SessionID, State: s.State, Owner: s.Owner, OwnerKind: ownerKind, Capabilities: s.Capabilities, Observation: s.Observation, LastReceipt: s.LastReceipt}
}

func computerSnapshot(s *cu.ComputerSession) ComputerSessionDTO {
	owner := s.Owner()
	ownerKind := "managed_conversation"
	if owner.TenantID == localComputerTenantID && owner.UserID == localComputerUserID && owner.SessionID == 0 {
		ownerKind = "local_preview"
	}
	result := ComputerSessionDTO{ID: s.ID(), State: s.State(), Owner: owner, OwnerKind: ownerKind, Capabilities: s.Capabilities()}
	if o, ok := s.CurrentObservation(); ok {
		result.Observation = &o
	}
	if r, ok := s.LastReceipt(); ok {
		result.LastReceipt = &r
	}
	return result
}
func (m *computerManager) observe(ctx context.Context, id string) (ComputerObservationDTO, error) {
	return m.observeTarget(ctx, id, cu.ObserveRequest{SessionID: id})
}

func (m *computerManager) observeTarget(ctx context.Context, id string, request cu.ObserveRequest) (ComputerObservationDTO, error) {
	if m.remote != nil {
		request.SessionID = id
		o, err := m.remote.Observe(ctx, m.remoteOwner, request)
		if err != nil {
			return ComputerObservationDTO{}, err
		}
		data, mediaType, err := m.remote.ObservationImage(ctx, m.remoteOwner, id, o.ID)
		if err != nil {
			_ = m.remote.Pause(ctx, m.remoteOwner, id)
			return ComputerObservationDTO{}, err
		}
		return ComputerObservationDTO{Observation: o, ImageData: base64.StdEncoding.EncodeToString(data), MediaType: mediaType}, nil
	}
	c, err := m.active(id)
	if err != nil {
		return ComputerObservationDTO{}, err
	}
	request.SessionID = id
	o, err := c.Observe(ctx, c.Session().Owner(), request)
	if err != nil {
		return ComputerObservationDTO{}, err
	}
	data, mediaType, err := c.ObservationImage(ctx, c.Session().Owner(), id, o.ID)
	if err != nil {
		_ = c.Pause(ctx, c.Session().Owner(), id)
		return ComputerObservationDTO{}, err
	}
	return ComputerObservationDTO{Observation: o, ImageData: base64.StdEncoding.EncodeToString(data), MediaType: mediaType}, nil
}
func (m *computerManager) launchTarget(ctx context.Context, id string, targetID cu.TargetID) (cu.LaunchReceipt, error) {
	if m.remote != nil {
		return m.remote.LaunchTarget(ctx, m.remoteOwner, id, targetID)
	}
	c, err := m.active(id)
	if err != nil {
		return cu.LaunchReceipt{TargetID: targetID, Outcome: cu.OutcomeRejected, ErrorCode: cu.ErrorCodeInactive}, err
	}
	return c.LaunchTarget(ctx, c.Session().Owner(), id, targetID)
}

func (m *computerManager) executeTurn(ctx context.Context, action cu.Action) (cu.ComputerTurnResult, error) {
	if m.remote != nil {
		return m.remote.ExecuteTurn(ctx, m.remoteOwner, action)
	}
	c, err := m.active(action.SessionID)
	if err != nil {
		return cu.ComputerTurnResult{}, err
	}
	return c.ExecuteTurn(ctx, c.Session().Owner(), action)
}

func (m *computerManager) execute(ctx context.Context, action cu.Action) (cu.ActionReceipt, error) {
	if m.remote != nil {
		return m.remote.Execute(ctx, m.remoteOwner, action)
	}
	c, err := m.active(action.SessionID)
	if err != nil {
		return cu.ActionReceipt{ActionID: action.ID, SessionID: action.SessionID, Outcome: cu.OutcomeRejected, Verification: cu.VerificationNotChecked}, err
	}
	return c.Execute(ctx, c.Session().Owner(), action)
}

func (m *computerManager) observationImage(ctx context.Context, id, observationID string) ([]byte, string, error) {
	if m.remote != nil {
		return m.remote.ObservationImage(ctx, m.remoteOwner, id, observationID)
	}
	c, err := m.active(id)
	if err != nil {
		return nil, "", err
	}
	return c.ObservationImage(ctx, c.Session().Owner(), id, observationID)
}

func (m *computerManager) snapshot(ctx context.Context, id string) (ComputerSessionDTO, error) {
	if m.remote != nil {
		snapshot, err := m.remote.Snapshot(ctx, m.remoteOwner, id)
		if err != nil {
			return ComputerSessionDTO{}, err
		}
		return computerSnapshotFromBridge(snapshot), nil
	}
	c, err := m.active(id)
	if err != nil {
		return ComputerSessionDTO{}, err
	}
	return computerSnapshot(c.Session()), nil
}

func (m *computerManager) activeSnapshot(ctx context.Context) (ComputerSessionDTO, error) {
	if m.remote != nil {
		status, err := m.remote.RuntimeStatus(ctx, computerRuntimeSupervisionOwner)
		if err != nil || !status.ActiveSession || status.SessionID == "" {
			if err != nil {
				return ComputerSessionDTO{}, err
			}
			return ComputerSessionDTO{}, errors.New("computer session not found")
		}
		snapshot, err := m.remote.Snapshot(ctx, computerRuntimeSupervisionOwner, status.SessionID)
		if err != nil {
			return ComputerSessionDTO{}, err
		}
		return computerSnapshotFromBridge(snapshot), nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.controller == nil {
		return ComputerSessionDTO{}, errors.New("computer session not found")
	}
	return computerSnapshot(m.controller.Session()), nil
}

func (m *computerManager) control(ctx context.Context, id string, kind cu.ActionKind) (ComputerSessionDTO, error) {
	if m.remote != nil {
		var err error
		switch kind {
		case cu.ActionPause:
			err = m.remote.Pause(ctx, m.remoteOwner, id)
		case cu.ActionResume:
			err = m.remote.Resume(ctx, m.remoteOwner, id)
		case cu.ActionStop:
			err = m.remote.Stop(ctx, m.remoteOwner, id)
		default:
			err = errors.New("unsupported computer control")
		}
		snapshot, snapshotErr := m.remote.Snapshot(ctx, m.remoteOwner, id)
		if err != nil {
			return ComputerSessionDTO{}, err
		}
		if snapshotErr != nil {
			return ComputerSessionDTO{}, snapshotErr
		}
		return computerSnapshotFromBridge(snapshot), nil
	}
	c, err := m.active(id)
	if err != nil {
		return ComputerSessionDTO{}, err
	}
	switch kind {
	case cu.ActionPause:
		err = c.Pause(ctx, c.Session().Owner(), id)
	case cu.ActionResume:
		err = c.Resume(ctx, c.Session().Owner(), id)
	case cu.ActionStop:
		err = c.Stop(ctx, c.Session().Owner(), id)
	default:
		err = errors.New("unsupported computer control")
	}
	return computerSnapshot(c.Session()), err
}
func (m *computerManager) stopRuntime(ctx context.Context) error {
	if m == nil || m.remote == nil {
		return errors.New("standalone computer runtime is unavailable")
	}
	if err := m.remote.StopRuntime(ctx, computerRuntimeSupervisionOwner); err == nil {
		return nil
	} else if m.runtimeStopFallback != nil && errors.Is(err, computerbridge.ErrTransport) {
		if fallbackErr := m.runtimeStopFallback(ctx); fallbackErr == nil {
			return nil
		} else {
			return errors.Join(err, fallbackErr)
		}
	} else {
		return err
	}
}

func (m *computerManager) close(ctx context.Context) error {
	if m.remote != nil {
		return nil
	}
	m.mu.Lock()
	c, b := m.controller, m.backend
	m.controller = nil
	m.backend = nil
	m.mu.Unlock()
	if c != nil {
		return c.Close(ctx)
	}
	if b != nil {
		return b.Close(ctx)
	}
	return nil
}
func (a *app) GetComputerCapabilities() ComputerCapabilitiesDTO {
	caps, err := a.computer().capabilities(a.windowContext())
	if err != nil {
		return ComputerCapabilitiesDTO{ErrorCode: "capability_unavailable", ErrorMessage: err.Error()}
	}
	return ComputerCapabilitiesDTO{Capabilities: caps, Available: true}
}

// GetComputerRuntimeStatus is read-only and fail-closed. It never creates a
// session, refreshes an observation, or grants input authority. A disconnected
// standalone bridge is reported as a stable lifecycle fault instead of leaking
// transport details into the UI.
func (a *app) GetComputerRuntimeStatus() ComputerRuntimeStatusDTO {
	return a.computer().runtimeStatusForUI(context.Background())
}

// StopComputerRuntime is an explicit, owner-only emergency teardown control for
// the local desktop client. It is the UI counterpart of the process-handle
// fallback used when the standalone bridge is unavailable; it never replays or
// dispatches an input action.
func (a *app) StopComputerRuntime() ComputerRuntimeStatusDTO {
	manager := a.computer()
	ctx := context.Background()
	var err error
	if manager.remote != nil {
		err = manager.stopRuntime(ctx)
	} else {
		a.mu.Lock()
		runtime := a.computerRuntime
		a.mu.Unlock()
		if runtime == nil {
			err = errors.New("computer runtime is unavailable")
		} else {
			err = runtime.Stop(ctx)
		}
	}
	if err == nil {
		return ComputerRuntimeStatusDTO{State: cu.RuntimeStopped, Available: false}
	}
	status := manager.runtimeStatusForUI(ctx)
	status.State = cu.RuntimeFaulted
	status.Available = false
	status.ErrorCode = cu.RuntimeFaultStopFailed
	status.Fault = &ComputerRuntimeFaultDTO{State: cu.RuntimeFaulted, ErrorCode: cu.RuntimeFaultStopFailed, Recoverable: false, SessionID: status.SessionID}
	return status
}
func (a *app) StartComputerSession(in ComputerSessionStartInput) (ComputerSessionDTO, error) {
	ctx := a.windowContext()
	if in.ConversationRef == "" {
		return a.computer().start(ctx, in)
	}
	if !in.Approved {
		return ComputerSessionDTO{}, errors.New("explicit conversation approval is required")
	}
	owner, err := resolveComputerConversation(ctx, a.port, a.token, in.ConversationRef)
	if err != nil {
		return ComputerSessionDTO{}, err
	}
	return a.computer().startOwned(ctx, in, owner)
}

// GetComputerSession reads the same controller used by the model runtime. It
// must not capture, access image bytes, refresh observation freshness, or revoke
// a grant. Stopped sessions remain readable until replaced by an approved start.
func (a *app) GetComputerSession(id string) (ComputerSessionDTO, error) {
	return a.computer().snapshot(context.Background(), id)
}

// GetActiveComputerSession is read-only authoritative discovery for sessions
// created by the model bridge. It never initializes a backend, captures a
// screenshot, refreshes observation freshness, or guesses an owner.
func (a *app) GetActiveComputerSession() (ComputerSessionDTO, error) {
	return a.computer().activeSnapshot(context.Background())
}
func (a *app) ObserveComputerSession(id string) (ComputerObservationDTO, error) {
	return a.computer().observe(a.windowContext(), id)
}
func (a *app) PauseComputerSession(id string) (ComputerSessionDTO, error) {
	return a.computer().control(a.windowContext(), id, cu.ActionPause)
}
func (a *app) ResumeComputerSession(id string) (ComputerSessionDTO, error) {
	return a.computer().control(a.windowContext(), id, cu.ActionResume)
}
func (a *app) StopComputerSession(id string) (ComputerSessionDTO, error) {
	return a.computer().control(a.windowContext(), id, cu.ActionStop)
}
func (a *app) GetComputerActionReceipt(id, actionID string) (cu.ActionReceipt, error) {
	snapshot, err := a.computer().snapshot(context.Background(), id)
	if err != nil {
		return cu.ActionReceipt{}, err
	}
	if snapshot.LastReceipt == nil || snapshot.LastReceipt.ActionID != actionID {
		return cu.ActionReceipt{}, errors.New("computer receipt not found")
	}
	return *snapshot.LastReceipt, nil
}
func (a *app) runtime() *RuntimeSupervisor {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.computerRuntime == nil {
		a.computerRuntime = newComputerRuntime(nil)
	}
	return a.computerRuntime
}

func (a *app) computer() *computerManager {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.computerManager == nil {
		if a.providerRuntime != nil {
			if client := a.providerRuntime.BridgeClient(); client != nil {
				a.providerRuntime.StartVisualCueOverlay()
				fallback := func(ctx context.Context) error {
					a.mu.Lock()
					runtime := a.providerRuntime
					a.mu.Unlock()
					if runtime == nil {
						return errors.New("standalone computer runtime is unavailable")
					}
					return runtime.Stop(ctx)
				}
				a.computerManager = newRemoteComputerManager(client, fallback)
				return a.computerManager
			}
		}
		if a.computerRuntime == nil {
			a.computerRuntime = newComputerRuntime(nil)
		}
		a.computerRuntime.StartVisualCueOverlay()
		a.computerManager = a.computerRuntime.Manager()
	}
	return a.computerManager
}
