// Package computeruse contains the provider- and platform-neutral contract for
// Computer Use. Platform helpers implement Backend; callers must not depend on
// native windowing or input APIs through these types.
package computeruse

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

const (
	ProtocolVersion     = "computer-use.v1"
	MaxActionDurationMS = 10000
	ActionEvidenceGrace = 2 * time.Second
	MaxInputBytes       = 4096
	MaxHotkeyKeys       = 5
	MaxKeyBytes         = 32
	MaxScrollDelta      = 10000
	MaxDragDurationMS   = MaxActionDurationMS
)

// ActionExecutionTimeout separates a validated wait/drag duration from bounded
// capture and acknowledgement overhead. The caller's earlier context deadline
// always wins; this does not change any run's total budget or replay policy.
func ActionExecutionTimeout(action Action, baseline time.Duration) time.Duration {
	if (action.Kind != ActionWait && action.Kind != ActionDrag) || action.DurationMS <= 0 || action.DurationMS > MaxActionDurationMS {
		return baseline
	}
	timeout := time.Duration(action.DurationMS)*time.Millisecond + ActionEvidenceGrace
	if timeout > baseline {
		return timeout
	}
	return baseline
}

// Platform identifies the host family without exposing platform implementation
// details in the public contract.
type Platform string

const (
	PlatformUnknown Platform = "unknown"
	PlatformMacOS   Platform = "macos"
	PlatformWindows Platform = "windows"
	PlatformLinux   Platform = "linux"
)

// BackendKind identifies an execution backend. Future platforms add a backend
// implementation without changing Action or Observation.
type BackendKind string

const (
	BackendUnknown    BackendKind = "unknown"
	BackendNativeHost BackendKind = "native_host"
	BackendVirtualX11 BackendKind = "virtual_x11"
	BackendIsolated   BackendKind = "isolated"
)

type Readiness string

const (
	ReadinessUnknown            Readiness = "unknown"
	ReadinessUnavailable        Readiness = "unavailable"
	ReadinessPermissionRequired Readiness = "permission_required"
	ReadinessReady              Readiness = "ready"
	ReadinessFailed             Readiness = "failed"
)

type PermissionState string

const (
	PermissionUnknown  PermissionState = "unknown"
	PermissionRequired PermissionState = "required"
	PermissionApproved PermissionState = "approved"
	PermissionDenied   PermissionState = "denied"
)

type FocusState string

const (
	FocusUnknown     FocusState = "unknown"
	FocusFocused     FocusState = "focused"
	FocusChanged     FocusState = "changed"
	FocusUnavailable FocusState = "unavailable"
)

type CoordinateUnit string

const (
	CoordinatePixels CoordinateUnit = "pixels"
)

type CoordinateOrigin string

const (
	OriginTopLeft CoordinateOrigin = "top_left"
)

type WindowFrame struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// CoordinateSpace is expressed in image pixels while Bounds identifies the
// same display in the host's global point coordinate space. Keeping both in
// one value prevents multi-display callers from guessing negative origins or
// mixed-DPI placement.
type CoordinateSpace struct {
	DisplayID   string           `json:"display_id,omitempty"`
	Origin      CoordinateOrigin `json:"origin"`
	Unit        CoordinateUnit   `json:"unit"`
	Width       int              `json:"width"`
	Height      int              `json:"height"`
	ScaleFactor float64          `json:"scale_factor"`
	Bounds      *WindowFrame     `json:"bounds,omitempty"`
}

type WindowRef struct {
	ID          string       `json:"id,omitempty"`
	Title       string       `json:"title,omitempty"`
	OwnerPID    int32        `json:"owner_pid,omitempty"`
	BundleID    string       `json:"bundle_id,omitempty"`
	Frame       *WindowFrame `json:"frame,omitempty"`
	IsVisible   bool         `json:"is_visible,omitempty"`
	IsFrontmost bool         `json:"is_frontmost,omitempty"`
}

type Point struct {
	X int `json:"x"`
	Y int `json:"y"`
}

type MouseButton string

const (
	MouseButtonLeft  MouseButton = "left"
	MouseButtonRight MouseButton = "right"
)

func (b MouseButton) valid() bool {
	return b == MouseButtonLeft || b == MouseButtonRight
}

type MediaRef struct {
	ID        string `json:"id,omitempty"`
	URL       string `json:"url,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
}

func NewMediaRef(id, mediaType string, data []byte, width, height int) MediaRef {
	ref := MediaRef{ID: id, MediaType: mediaType, Width: width, Height: height, SizeBytes: int64(len(data))}
	if len(data) > 0 {
		hash := sha256.Sum256(data)
		ref.SHA256 = hex.EncodeToString(hash[:])
	}
	return ref
}

type Capabilities struct {
	ProtocolVersion  string            `json:"protocol_version"`
	Platform         Platform          `json:"platform"`
	Backend          BackendKind       `json:"backend"`
	CaptureReadiness Readiness         `json:"capture_readiness"`
	InputReadiness   Readiness         `json:"input_readiness"`
	FocusState       FocusState        `json:"focus_state"`
	PermissionState  PermissionState   `json:"permission_state"`
	CoordinateSpace  CoordinateSpace   `json:"coordinate_space"`
	Displays         []CoordinateSpace `json:"displays,omitempty"`
	Windows          []WindowRef       `json:"windows,omitempty"`
	TargetWindow     WindowRef         `json:"target_window,omitempty"`
	Actions          []ActionKind      `json:"actions,omitempty"`
	ImageSupported   bool              `json:"image_supported"`
	SupportsPause    bool              `json:"supports_pause"`
	SupportsStop     bool              `json:"supports_stop"`
}

func (c Capabilities) Ready() bool {
	return c.CaptureReadiness == ReadinessReady &&
		c.InputReadiness == ReadinessReady &&
		c.PermissionState == PermissionApproved
}

// ReadinessErrorCode distinguishes an OS permission transition from a helper
// or transport availability fault without exposing native diagnostics.
func (c Capabilities) ReadinessErrorCode() string {
	if c.PermissionState != PermissionApproved || c.CaptureReadiness == ReadinessPermissionRequired || c.InputReadiness == ReadinessPermissionRequired {
		return ErrorCodePermissionRequired
	}
	return ErrorCodeHelperUnavailable
}

func (c Capabilities) Supports(action ActionKind) bool {
	for _, supported := range c.Actions {
		if supported == action {
			return true
		}
	}
	return false
}

func (c Capabilities) Validate() error {
	if c.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("unsupported computer use protocol version %q", c.ProtocolVersion)
	}
	if c.Platform == "" || c.Backend == "" || c.Platform == PlatformUnknown || c.Backend == BackendUnknown {
		return errors.New("platform and backend are required")
	}
	if c.CoordinateSpace.Origin != OriginTopLeft || c.CoordinateSpace.Unit != CoordinatePixels {
		return errors.New("coordinate space must use top-left pixel coordinates")
	}
	if c.CoordinateSpace.Width < 0 || c.CoordinateSpace.Height < 0 || c.CoordinateSpace.ScaleFactor < 0 || math.IsNaN(c.CoordinateSpace.ScaleFactor) || math.IsInf(c.CoordinateSpace.ScaleFactor, 0) {
		return errors.New("coordinate space dimensions and scale factor must be non-negative")
	}
	return nil
}

type Observation struct {
	ID           string       `json:"id"`
	SessionID    string       `json:"session_id"`
	DisplayID    string       `json:"display_id,omitempty"`
	WindowID     string       `json:"window_id,omitempty"`
	Width        int          `json:"width"`
	Height       int          `json:"height"`
	ScaleFactor  float64      `json:"scale_factor"`
	Screenshot   MediaRef     `json:"screenshot"`
	ActiveWindow WindowRef    `json:"active_window,omitempty"`
	Cursor       Point        `json:"cursor"`
	Capabilities Capabilities `json:"capabilities"`
	ObservedAt   time.Time    `json:"observed_at"`
	ExpiresAt    time.Time    `json:"expires_at,omitempty"`
}

func (o Observation) Expired(now time.Time) bool {
	return !o.ExpiresAt.IsZero() && !now.Before(o.ExpiresAt)
}

type ActionKind string

const (
	ActionObserve     ActionKind = "observe"
	ActionLaunchApp   ActionKind = "launch_app"
	ActionClick       ActionKind = "click"
	ActionDoubleClick ActionKind = "double_click"
	ActionRightClick  ActionKind = "right_click"
	ActionMove        ActionKind = "move"
	ActionType        ActionKind = "type"
	ActionKey         ActionKind = "key"
	ActionHotkey      ActionKind = "hotkey"
	ActionScroll      ActionKind = "scroll"
	ActionDrag        ActionKind = "drag"
	ActionWait        ActionKind = "wait"
	ActionPause       ActionKind = "pause"
	ActionResume      ActionKind = "resume"
	ActionStop        ActionKind = "stop"
)

func (k ActionKind) IsInput() bool {
	switch k {
	case ActionClick, ActionDoubleClick, ActionRightClick, ActionMove, ActionDrag, ActionType, ActionKey, ActionHotkey, ActionScroll:
		return true
	default:
		return false
	}
}

func (k ActionKind) IsControl() bool {
	return k == ActionObserve || k == ActionWait || k == ActionPause || k == ActionResume || k == ActionStop
}

type ExpectedState struct {
	WindowID string `json:"window_id,omitempty"`
	Hash     string `json:"hash,omitempty"`
}

type Action struct {
	ID            string         `json:"id"`
	SessionID     string         `json:"session_id"`
	ObservationID string         `json:"observation_id,omitempty"`
	TargetID      TargetID       `json:"target_id,omitempty"`
	Kind          ActionKind     `json:"kind"`
	DisplayID     string         `json:"display_id,omitempty"`
	WindowID      string         `json:"window_id,omitempty"`
	Point         *Point         `json:"point,omitempty"`
	StartPoint    *Point         `json:"start_point,omitempty"`
	Button        string         `json:"button,omitempty"`
	Text          string         `json:"text,omitempty"`
	Key           string         `json:"key,omitempty"`
	Keys          []string       `json:"keys,omitempty"`
	DeltaX        int            `json:"delta_x,omitempty"`
	DeltaY        int            `json:"delta_y,omitempty"`
	DurationMS    int            `json:"duration_ms,omitempty"`
	Expected      *ExpectedState `json:"expected,omitempty"`
}

func (a Action) Validate(now time.Time, observation Observation) error {
	if strings.TrimSpace(a.ID) == "" {
		return errors.New("action id is required")
	}
	if strings.TrimSpace(a.SessionID) == "" {
		return errors.New("action session id is required")
	}
	if observation.SessionID != "" && a.SessionID != observation.SessionID {
		return errors.New("action session does not match observation session")
	}
	if !a.Kind.IsInput() && !a.Kind.IsControl() && a.Kind != ActionLaunchApp {
		return errors.New("action kind is required")
	}
	if a.Kind == ActionLaunchApp {
		if !a.TargetID.valid() {
			return errors.New("launch requires a registered target")
		}
		if a.ObservationID != "" {
			return errors.New("launch must not reference an observation")
		}
		return nil
	}
	if a.Kind.IsInput() || a.Kind == ActionWait {
		if a.ObservationID == "" {
			return errors.New("input action must reference an observation")
		}
		if a.ObservationID != observation.ID {
			return errors.New("input action references a stale observation")
		}
		if observation.Expired(now) {
			return errors.New("input action references an expired observation")
		}
	}
	if a.Kind == ActionClick || a.Kind == ActionDoubleClick || a.Kind == ActionRightClick || a.Kind == ActionMove {
		if a.Point == nil {
			return fmt.Errorf("%s requires a point", a.Kind)
		}
		if a.Point.X < 0 || a.Point.Y < 0 || a.Point.X >= observation.Width || a.Point.Y >= observation.Height {
			return fmt.Errorf("point (%d,%d) is outside observation bounds %dx%d", a.Point.X, a.Point.Y, observation.Width, observation.Height)
		}
	}
	if a.Button != "" {
		button := MouseButton(strings.ToLower(strings.TrimSpace(a.Button)))
		allowed := (a.Kind == ActionDrag && button.valid()) ||
			((a.Kind == ActionClick || a.Kind == ActionDoubleClick || a.Kind == ActionMove) && button == MouseButtonLeft) ||
			(a.Kind == ActionRightClick && button == MouseButtonRight)
		if !allowed {
			return fmt.Errorf("unsupported %s button %q", a.Kind, a.Button)
		}
	}
	if a.Kind == ActionDrag {
		if a.StartPoint == nil || a.Point == nil {
			return errors.New("drag requires start_point and point")
		}
		for name, point := range map[string]*Point{"start_point": a.StartPoint, "point": a.Point} {
			if point.X < 0 || point.Y < 0 || point.X >= observation.Width || point.Y >= observation.Height {
				return fmt.Errorf("%s (%d,%d) is outside observation bounds %dx%d", name, point.X, point.Y, observation.Width, observation.Height)
			}
		}
		button := MouseButton(a.Button)
		if button == "" {
			button = MouseButtonLeft
		}
		if !button.valid() {
			return fmt.Errorf("unsupported drag button %q", a.Button)
		}
		if a.DurationMS < 0 || a.DurationMS > MaxDragDurationMS {
			return errors.New("drag duration must not be negative")
		}
	}
	if a.Kind == ActionType && a.Text == "" {
		return errors.New("type requires text")
	}
	if a.Kind == ActionKey && strings.TrimSpace(a.Key) == "" {
		return errors.New("key requires key")
	}
	if a.Kind == ActionHotkey && len(a.Keys) == 0 {
		return errors.New("hotkey requires keys")
	}
	if a.Kind == ActionScroll && a.DeltaX == 0 && a.DeltaY == 0 {
		return errors.New("scroll requires a non-zero delta")
	}
	if a.DurationMS < 0 || a.DurationMS > MaxActionDurationMS {
		return errors.New("wait duration must not be negative")
	}

	if len(a.Text) > MaxInputBytes || len(a.Keys) > MaxHotkeyKeys || len(a.Key) > MaxKeyBytes {
		return errors.New("action input exceeds bounds")
	}
	for _, k := range a.Keys {
		if strings.TrimSpace(k) == "" || len(k) > MaxKeyBytes {
			return errors.New("invalid hotkey")
		}
	}
	if a.DeltaX < -MaxScrollDelta || a.DeltaX > MaxScrollDelta || a.DeltaY < -MaxScrollDelta || a.DeltaY > MaxScrollDelta {
		return errors.New("scroll exceeds bounds")
	}
	if a.DisplayID != "" && a.DisplayID != observation.DisplayID {
		return errors.New("action display mismatch")
	}
	if observation.WindowID != "" && a.WindowID != observation.WindowID {
		return errors.New("action window mismatch")
	}
	if a.WindowID != "" && a.WindowID != observation.WindowID {
		return errors.New("action window mismatch")
	}
	return nil
}

func (a Action) Sensitive() bool {
	return a.Kind == ActionType || a.Kind == ActionKey || a.Kind == ActionHotkey
}

func (a Action) RedactedSummary() string {
	switch a.Kind {
	case ActionType:
		return fmt.Sprintf("type(text_length=%d)", len([]rune(a.Text)))
	case ActionKey:
		return "key(redacted)"
	case ActionHotkey:
		return fmt.Sprintf("hotkey(key_count=%d)", len(a.Keys))
	default:
		return string(a.Kind)
	}
}

type VerificationStatus string

const (
	VerificationNotChecked VerificationStatus = "not_checked"
	VerificationPassed     VerificationStatus = "passed"
	VerificationFailed     VerificationStatus = "failed"
	VerificationUnknown    VerificationStatus = "unknown"
)

const (
	ErrorCodeActionFailed            = "action_failed"
	ErrorCodeScreenshotFailed        = "screenshot_failed"
	ErrorCodeInputUncertain          = "input_uncertain"
	ErrorCodeSelfTarget              = "self_target"
	ErrorCodeTargetWindowMismatch    = "target_window_mismatch"
	ErrorCodeTargetExitedAfterLaunch = "target_exited_after_launch"
	ErrorCodeUnsupportedApplication  = "unsupported_application" // deprecated compatibility code
	ErrorCodeUnsupportedTarget       = "unsupported_target"
	ErrorCodeLaunchFailed            = "launch_failed"
	ErrorCodeLaunchTimeout           = "launch_timeout"
	ErrorCodePermissionRequired      = "permission_required"
	ErrorCodeFocusChanged            = "focus_changed"
	ErrorCodeUnsupportedDisplay      = "unsupported_display"
	ErrorCodeInvalidAction           = "invalid_action"
	ErrorCodeInvalidBinding          = "invalid_action_binding"
	ErrorCodeInactive                = "inactive"
	ErrorCodeClosed                  = "closed"
	ErrorCodeInputUnavailable        = "input_unavailable"
	ErrorCodeTimeout                 = "timeout"
	ErrorCodeCanceled                = "canceled"
	ErrorCodeHelperUnavailable       = "helper_unavailable"
)

// PublicErrorCode preserves only stable, non-sensitive diagnostics across the
// native/bridge/model boundaries. Unknown helper strings are deliberately
// collapsed so private host details never reach a model or UI.
func PublicErrorCode(code string) string {
	switch strings.TrimSpace(code) {
	case ErrorCodeScreenshotFailed, ErrorCodeInputUncertain, ErrorCodeSelfTarget, ErrorCodeTargetWindowMismatch, ErrorCodeTargetExitedAfterLaunch, ErrorCodePermissionRequired, ErrorCodeFocusChanged,
		ErrorCodeUnsupportedDisplay, ErrorCodeInvalidAction, ErrorCodeInvalidBinding,
		ErrorCodeInactive, ErrorCodeClosed, ErrorCodeInputUnavailable,
		ErrorCodeUnsupportedApplication, ErrorCodeUnsupportedTarget, ErrorCodeLaunchFailed, ErrorCodeLaunchTimeout,
		ErrorCodeTimeout, ErrorCodeCanceled, ErrorCodeHelperUnavailable:
		return strings.TrimSpace(code)
	default:
		return ErrorCodeActionFailed
	}
}

type Outcome string

const (
	OutcomeNotStarted Outcome = "not_started"
	OutcomeExecuted   Outcome = "executed"
	OutcomeRejected   Outcome = "rejected"
	OutcomeFailed     Outcome = "failed"
	OutcomeUnknown    Outcome = "unknown"
)

// ComputerApplication is an allowlisted host application that Computer Use may
// launch. The model never supplies a bundle ID or filesystem path.
type ComputerApplication string

const (
	ApplicationWorkBuddy ComputerApplication = "WorkBuddy"
	WorkBuddyBundleID                        = "com.workbuddy.workbuddy"
	NiumaHostBundleID                        = "com.wails.niuma"
)

// LaunchReceipt is independent evidence that a trusted host launch request was
// accepted and that the target window was discovered and bound. It contains no
// untrusted helper error text.
type LaunchReceipt struct {
	TargetID    TargetID `json:"target_id,omitempty"`
	DisplayName string   `json:"display_name,omitempty"`
	// Application is retained for native/backend compatibility. Generic callers
	// must identify launches with TargetID and resolve it through TargetRegistry.
	Application ComputerApplication `json:"application,omitempty"`
	BundleID    string              `json:"bundle_id"`
	Window      WindowRef           `json:"window"`
	Outcome     Outcome             `json:"outcome"`
	ErrorCode   string              `json:"error_code,omitempty"`
	Duration    time.Duration       `json:"duration"`
	CompletedAt time.Time           `json:"completed_at"`
}

func (r LaunchReceipt) IsTerminal() bool {
	return r.Outcome == OutcomeExecuted || r.Outcome == OutcomeRejected || r.Outcome == OutcomeFailed || r.Outcome == OutcomeUnknown
}

// DispatchState describes input acknowledgement independently of visual evidence.
type DispatchState string

const (
	DispatchNotStarted DispatchState = "not_started"
	DispatchComplete   DispatchState = "complete"
	DispatchPartial    DispatchState = "partial"
	DispatchUnknown    DispatchState = "unknown"
)

type ActionReceipt struct {
	ActionID               string             `json:"action_id"`
	SessionID              string             `json:"session_id"`
	Platform               Platform           `json:"platform"`
	Backend                BackendKind        `json:"backend"`
	EnvironmentFingerprint string             `json:"environment_fingerprint,omitempty"`
	BeforeObservationID    string             `json:"before_observation_id,omitempty"`
	AfterObservationID     string             `json:"after_observation_id,omitempty"`
	Outcome                Outcome            `json:"outcome"`
	DispatchState          DispatchState      `json:"dispatch_state,omitempty"`
	Verification           VerificationStatus `json:"verification"`
	FocusBefore            FocusState         `json:"focus_before"`
	FocusAfter             FocusState         `json:"focus_after"`
	RedactedActionSummary  string             `json:"redacted_action_summary"`
	ErrorCode              string             `json:"error_code,omitempty"`
	ErrorMessage           string             `json:"error_message,omitempty"`
	Duration               time.Duration      `json:"duration"`
	CompletedAt            time.Time          `json:"completed_at"`
	Before                 *MediaRef          `json:"before,omitempty"`
	After                  *MediaRef          `json:"after,omitempty"`
	ActualPoint            *Point             `json:"actual_point,omitempty"`
	ActualGlobalPoint      *ScreenPoint       `json:"actual_global_point,omitempty"`
	ActiveWindowAfter      WindowRef          `json:"active_window_after,omitempty"`
}

func (r ActionReceipt) IsTerminal() bool {
	return r.Outcome == OutcomeNotStarted || r.Outcome == OutcomeExecuted || r.Outcome == OutcomeRejected || r.Outcome == OutcomeFailed || r.Outcome == OutcomeUnknown
}
