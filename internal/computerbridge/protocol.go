// Package computerbridge proxies a trusted host computer session over an authenticated Unix socket.
// Session creation remains owned by the host coordinator; the bridge never grants OS permissions.
package computerbridge

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

const (
	CommandPath           = "/command"
	AuthorizationHeader   = "Authorization"
	BearerPrefix          = "Bearer "
	MaxRequestBytes       = 32 << 10
	MaxResponseBytes      = 24 << 20
	MaxImageBytes         = 16 << 20
	MaxImagePixels        = 32 << 20
	DefaultRequestTimeout = 15 * time.Second

	OpLookup        = "lookup"
	OpEnsure        = "ensure"
	OpResolveEnsure = "resolve_ensure"
	OpCapabilities  = "capabilities"
	OpObserve       = "observe"
	OpExecute       = "execute"
	OpExecuteTurn   = "execute_turn"
	OpPause         = "pause"
	OpResume        = "resume"
	OpStop          = "stop"
	OpImage         = "image"
	OpLaunchApp     = "launch_app"
	OpRuntimeStatus = "runtime_status"
	OpStartLocal    = "start_local"
	OpSnapshot      = "snapshot"
	OpRuntimeStop   = "runtime_stop"
	OpVisualCue     = "visual_cue"
)

// Errors never contain host error text, response bodies, tokens, or image data.
var (
	ErrInvalidConfig    = errors.New("computer bridge: invalid configuration")
	ErrInvalidRequest   = errors.New("computer bridge: invalid request")
	ErrRequestTooLarge  = errors.New("computer bridge: request exceeds limit")
	ErrTransport        = errors.New("computer bridge: request failed")
	ErrResponseTooLarge = errors.New("computer bridge: response exceeds limit")
	ErrInvalidResponse  = errors.New("computer bridge: invalid response")
	ErrRemote           = errors.New("computer bridge: host operation failed")
	ErrInvalidImage     = errors.New("computer bridge: invalid image")
)

// Service adds ephemeral image retrieval to the application service boundary.
// Implementations must not retain raw images in observations, logs or errors.
type Service interface {
	cu.Service
	ObservationImage(context.Context, cu.SessionOwner, string, string) ([]byte, string, error)
}

// TurnService is the atomic bridge path used by the provider adapter. It is
// separate from Service during migration so old host fakes remain source
// compatible while new callers can require one turn result.
type TurnService interface {
	cu.TurnService
}

// TurnScreenshotReader transfers the already-produced screenshot asset for a
// committed turn. It is not an execute/image fallback: the turn authority has
// already decided the screenshot state before this read-only transfer.
type TurnScreenshotReader interface {
	TurnScreenshot(context.Context, cu.SessionOwner, cu.ComputerTurnResult) ([]byte, string, error)
}

// SessionLookup only discovers sessions approved and bound by the host UI.
// A lookup must never create, approve, resume or reassign a session.
type SessionLookup interface {
	Lookup(context.Context, cu.SessionOwner) (string, error)
}

// SessionCoordinator lazily creates a host-owned session after the normal
// ComputerUse permission gate. It cannot mint macOS TCC approval.
type SessionCoordinator interface {
	EnsureComputerSession(context.Context, cu.SessionOwner) (string, error)
}

// SessionAttemptCoordinator binds a startup request to an opaque, query-owned
// attempt ID. It is the stronger cold-start contract used when an IPC response
// can be lost after the host has created a session.
type SessionAttemptCoordinator interface {
	EnsureComputerSessionAttempt(context.Context, cu.SessionOwner, string) (string, error)
}

// SessionAttemptResolver queries only the exact startup attempt previously sent
// by this query. It must never fall back to Lookup, which could select another
// conversation's live grant.
type SessionAttemptResolver interface {
	ResolveComputerSessionStart(context.Context, cu.SessionOwner, string) (string, error)
}

// Request is the host wire contract. Exactly one operation is sent per POST.
// The host must independently authenticate and authorize every request. Owner's
// SessionID is a conversation ID, distinct from the host SessionID below.
type Request struct {
	Op               string             `json:"op"`
	Owner            cu.SessionOwner    `json:"owner"`
	SessionID        string             `json:"session_id,omitempty"`
	StartupAttemptID string             `json:"startup_attempt_id,omitempty"`
	ObserveRequest   *cu.ObserveRequest `json:"observe_request,omitempty"`
	Action           *cu.Action         `json:"action,omitempty"`
	ObservationID    string             `json:"observation_id,omitempty"`
	Approved         bool               `json:"approved,omitempty"`
	TargetID         string             `json:"target_id,omitempty"`
	// Application is retained for the legacy adapter only. Generic launch uses TargetID.
	Application string `json:"application,omitempty"`
}

// Response wraps one operation's data, or a nonempty error. Only Execute may
// return both data and error, to preserve a validated, bound failure receipt.
// Error text is untrusted and is never propagated to callers.
type Response struct {
	Data      json.RawMessage `json:"data,omitempty"`
	Error     string          `json:"error,omitempty"`
	ErrorCode string          `json:"error_code,omitempty"`
}

// SessionResponse acknowledges controls or returns a host-owned session ID.
type SessionResponse struct {
	SessionID        string `json:"session_id"`
	StartupAttemptID string `json:"startup_attempt_id,omitempty"`
}

// RuntimeStatusResponse is a read-only lifecycle probe for the UI client. It
// carries no authority and intentionally exposes no model owner or action data.
type RuntimeStatusResponse struct {
	State         cu.RuntimeState  `json:"state"`
	Fault         *cu.RuntimeFault `json:"fault,omitempty"`
	ActiveSession bool             `json:"active_session"`
	SessionID     string           `json:"session_id,omitempty"`
}

// RuntimeStatusService is implemented by the long-lived runtime process. The
// caller must use a dedicated local supervision owner; model owners are never
// accepted for this probe.
type RuntimeStatusService interface {
	RuntimeStatus(context.Context, cu.SessionOwner) (RuntimeStatusResponse, error)
}

// RuntimeStopService is the explicit owner-only runtime teardown boundary. It
// is distinct from session Stop and is never invoked by a Wails UI shutdown.
type RuntimeStopService interface {
	StopRuntime(context.Context, cu.SessionOwner) error
}

// VisualCueReader exposes only ephemeral, non-authoritative UI hints to the
// local desktop client. It never carries input payloads or screenshot bytes.
type VisualCueReader interface {
	VisualCue(context.Context, cu.SessionOwner, string) (VisualCueResponse, error)
}

// SessionSnapshotResponse is a read-only lifecycle snapshot. It contains no
// raw image bytes; media remains a separately validated observation asset.
type SessionSnapshotResponse struct {
	SessionID    string            `json:"session_id"`
	State        cu.SessionState   `json:"state"`
	Owner        cu.SessionOwner   `json:"owner"`
	Approved     bool              `json:"approved"`
	Capabilities cu.Capabilities   `json:"capabilities"`
	Observation  *cu.Observation   `json:"observation,omitempty"`
	LastReceipt  *cu.ActionReceipt `json:"last_receipt,omitempty"`
}

// LocalSessionStarter is the narrow UI-client approval boundary. It is not a
// model permission API; the host implementation must bind it to a dedicated
// local UI owner.
type LocalSessionStarter interface {
	StartLocal(context.Context, cu.SessionOwner, bool) (SessionSnapshotResponse, error)
}

// SessionSnapshotReader provides an authoritative read-only session snapshot.
type SessionSnapshotReader interface {
	Snapshot(context.Context, cu.SessionOwner, string) (SessionSnapshotResponse, error)
}

// LookupResponse identifies an existing host-approved session.
type LookupResponse = SessionResponse

// ExecuteTurnResponse keeps the authoritative turn metadata and its optional
// inline PNG in one RPC response. The PNG is validated against the MediaRef
// hash, dimensions, MIME and the observation TTL before the client exposes it.
type ExecuteTurnResponse struct {
	Result              cu.ComputerTurnResult `json:"result"`
	ScreenshotData      string                `json:"screenshot_data,omitempty"`
	ScreenshotMediaType string                `json:"screenshot_media_type,omitempty"`
	ScreenshotExpiresAt time.Time             `json:"screenshot_expires_at,omitempty"`
}

// ImageResponse carries base64-encoded PNG bytes, not a data URL. Explicit IDs
// bind the bytes to the requested observation and approved host session.
type VisualCueResponse struct {
	Cue *cu.ActionVisualCue `json:"cue,omitempty"`
}

type ImageResponse struct {
	SessionID     string `json:"session_id"`
	ObservationID string `json:"observation_id"`
	ImageData     string `json:"image_data"`
	MediaType     string `json:"media_type"`
}
