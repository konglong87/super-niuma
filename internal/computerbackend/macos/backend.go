// Package macos isolates the native helper from the Computer Use contract.
package macos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/konglong87/super-niuma/internal/computerbackend/native"
	"github.com/konglong87/super-niuma/internal/computerdiag"
	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

const (
	commandRequestPermissions = "request_permissions"
	commandReadiness          = "readiness"
	commandObserve            = "observe"
	commandLaunchApp          = "launch_app"
	commandExecute            = "execute"
	commandPause              = "pause"
	commandResume             = "resume"
	commandStop               = "stop"
	commandShutdown           = "shutdown"
	defaultTimeout            = 10 * time.Second
	maxTimeout                = 60 * time.Second
	maxFrameBytes             = 8 * 1024 * 1024
	maxActions                = 4096
	helperInactiveCode        = "inactive"
	helperFocusChangedCode    = "focus_changed"
	selfTargetCode            = cu.ErrorCodeSelfTarget
	// Match the native geometry validity tolerance for point/pixel rounding.
	geometryScaleTolerance = 0.01
)

type Config struct {
	HelperPath string
	HelperArgs []string
	HelperEnv  []string
	// HostBundleID identifies the owning desktop UI for self-target safety.
	// Empty keeps the backend platform-neutral in isolated tests.
	HostBundleID string
	// Base RPC timeout; bounded wait/drag durations add evidence grace when needed.
	RequestTimeout         time.Duration
	RequestHostPermissions func()
	// CheckHostPermissions is the signed host's TCC source of truth. Production
	// must not trust the nested helper's own TCC identity.
	CheckHostPermissions func() (captureAllowed, inputAllowed bool)
	// AllowedProviderKeys are resolved by the host target registry. The backend
	// refuses direct launches when a key was not explicitly provisioned.
	AllowedProviderKeys []string
	MaxFrameBytes       int
	Now                 func() time.Time
	batchDriver         inputBatchDriver // private test seam
	mouseDriver         mouseDriver      // package-private test seam; production always uses host CG
}

func (b *Backend) selfTargetRejected(obs cu.Observation, action cu.Action) bool {
	if strings.TrimSpace(b.config.HostBundleID) == "" || action.WindowID != "" ||
		obs.ActiveWindow.BundleID != b.config.HostBundleID {
		return false
	}
	if action.Kind == cu.ActionHotkey {
		if len(action.Keys) == 2 {
			first, second := strings.ToLower(strings.TrimSpace(action.Keys[0])), strings.ToLower(strings.TrimSpace(action.Keys[1]))
			return !((first == "command" || first == "cmd" || first == "meta") && second == "space") &&
				!((second == "command" || second == "cmd" || second == "meta") && first == "space")
		}
		return true
	}
	if action.Kind == cu.ActionClick || action.Kind == cu.ActionDoubleClick || action.Kind == cu.ActionRightClick {
		// A click outside the active app window can be the Dock/Spotlight launch
		// step. A click inside the owning window is a self-target and is blocked.
		if action.Point == nil || obs.ActiveWindow.Frame == nil || obs.Capabilities.CoordinateSpace.Bounds == nil || obs.ScaleFactor <= 0 {
			return true
		}
		space := obs.Capabilities.CoordinateSpace.Bounds
		globalX := space.X + float64(action.Point.X)/obs.ScaleFactor
		globalY := space.Y + float64(action.Point.Y)/obs.ScaleFactor
		frame := obs.ActiveWindow.Frame
		return globalX >= frame.X && globalX <= frame.X+frame.Width && globalY >= frame.Y && globalY <= frame.Y+frame.Height
	}
	return true
}

type Backend struct {
	mu                      sync.Mutex // state only, never held across IPC
	operation               chan struct{}
	process                 *native.Process
	mouseBroker             *mouseBroker
	config                  Config
	requestCounter          atomic.Uint64
	closed, paused, stopped bool
	failed                  bool
	epoch                   uint64
	images                  map[string]imageData
	observation             cu.Observation
	actions                 map[string]struct{}
	capabilities            cu.Capabilities
	targetWindowID          string
	targetBinding           *cu.TargetBinding
}
type imageData struct {
	data      []byte
	mediaType string
}

func decodeActualPoint(raw any) (cu.Point, bool) {
	fields, ok := raw.(map[string]any)
	if !ok || len(fields) != 2 {
		return cu.Point{}, false
	}
	x, okX := integer(fields["x"], 0, maxImageDimension)
	y, okY := integer(fields["y"], 0, maxImageDimension)
	if !okX || !okY {
		return cu.Point{}, false
	}
	return cu.Point{X: x, Y: y}, true
}

func decodeGlobalPoint(raw any) (cu.ScreenPoint, bool) {
	fields, ok := raw.(map[string]any)
	if !ok || len(fields) != 2 {
		return cu.ScreenPoint{}, false
	}
	x, okX := fields["x"].(float64)
	y, okY := fields["y"].(float64)
	if !okX || !okY || math.IsNaN(x) || math.IsNaN(y) || math.IsInf(x, 0) || math.IsInf(y, 0) {
		return cu.ScreenPoint{}, false
	}
	return cu.ScreenPoint{X: x, Y: y}, true
}

func decodeWindowRef(raw any) (cu.WindowRef, bool) {
	data, err := json.Marshal(raw)
	if err != nil {
		return cu.WindowRef{}, false
	}
	var window cu.WindowRef
	if err := json.Unmarshal(data, &window); err != nil || window.ID == "" {
		return cu.WindowRef{}, false
	}
	return window, true
}

// decodeFrame requires all four fields; absent/null origins must not silently
// become zero. Negative global origins are valid, negative dimensions are not.
func decodeFrame(raw any) (*cu.WindowFrame, bool) {
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, false
	}
	var frame struct{ X, Y, Width, Height *float64 }
	if json.Unmarshal(data, &frame) != nil {
		return nil, false
	}
	for _, v := range []*float64{frame.X, frame.Y, frame.Width, frame.Height} {
		if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
			return nil, false
		}
	}
	if *frame.Width <= 0 || *frame.Height <= 0 {
		return nil, false
	}
	return &cu.WindowFrame{X: *frame.X, Y: *frame.Y, Width: *frame.Width, Height: *frame.Height}, true
}

// Capture geometry comes only from this response, never from readiness caches.
func decodeCaptureGeometry(result map[string]any, display string, scale float64, width, height int) (cu.CoordinateSpace, error) {
	invalid := errors.New("invalid screenshot geometry metadata")
	raw, ok := result["coordinate_space"].(map[string]any)
	if !ok {
		return cu.CoordinateSpace{}, invalid
	}
	data, err := json.Marshal(raw)
	var space cu.CoordinateSpace
	if err != nil || json.Unmarshal(data, &space) != nil {
		return cu.CoordinateSpace{}, invalid
	}
	bounds, valid := decodeFrame(raw["bounds"])
	if !valid || space.DisplayID != display || space.Origin != cu.OriginTopLeft || space.Unit != cu.CoordinatePixels ||
		space.Width != width || space.Height != height || space.ScaleFactor != scale {
		return cu.CoordinateSpace{}, invalid
	}
	xScale, yScale := float64(width)/bounds.Width, float64(height)/bounds.Height
	if math.IsNaN(xScale) || math.IsInf(xScale, 0) || math.IsNaN(yScale) || math.IsInf(yScale, 0) ||
		math.Abs(xScale-scale) >= geometryScaleTolerance || math.Abs(yScale-scale) >= geometryScaleTolerance {
		return cu.CoordinateSpace{}, invalid
	}
	space.Bounds = bounds
	return space, nil
}

func decodeCaptureTarget(result map[string]any, requested string) (cu.WindowRef, error) {
	invalid := errors.New("invalid screenshot target metadata")
	windowID := ""
	if raw, exists := result["window_id"]; exists {
		var ok bool
		windowID, ok = raw.(string)
		if !ok {
			return cu.WindowRef{}, invalid
		}
	}
	if windowID != requested {
		return cu.WindowRef{}, invalid
	}
	if windowID == "" {
		return cu.WindowRef{}, nil
	}
	target, ok := decodeWindowRef(result["target_window"])
	raw, object := result["target_window"].(map[string]any)
	if !ok || !object || target.ID != windowID {
		return cu.WindowRef{}, invalid
	}
	frame, valid := decodeFrame(raw["frame"])
	if !valid {
		return cu.WindowRef{}, invalid
	}
	target.Frame = frame
	return target, nil
}

type helperResponse struct {
	OK            *bool            `json:"ok"`
	Outcome       cu.Outcome       `json:"outcome"`
	Result        map[string]any   `json:"result"`
	ErrorCode     string           `json:"error_code"`
	DispatchState cu.DispatchState `json:"dispatch_state"`
	// Intentionally do not decode or surface arbitrary helper error_message.
}
type rejection struct{ code string }

func (e *rejection) Error() string { return "computer helper rejected request: " + e.code }
func (e *rejection) Code() string  { return e.code }

// helperFault is a stable, redacted transport fault. It deliberately does not
// expose native process errors or paths across the Computer Use boundary.
type helperFault struct{}

func (*helperFault) Error() string { return "computer helper unavailable" }
func (*helperFault) Code() string  { return cu.ErrorCodeHelperUnavailable }

func classifyObserveFault(b *Backend, err error) error {
	if err == nil {
		return nil
	}
	var rejected *rejection
	if errors.As(err, &rejected) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	b.invalidate()
	return &helperFault{}
}

// Native input needs no model credentials, agent sockets or loader overrides.
// Apply the same allowlist to explicit env overrides, not just inherited env.
func helperEnvironment(extra []string) []string {
	allowed := map[string]bool{"HOME": true, "USER": true, "LOGNAME": true, "TMPDIR": true, "LANG": true, "LC_CTYPE": true, "__CF_USER_TEXT_ENCODING": true,
		// Opt-in local acceptance fault injection; absent from normal environments.
		"NIUMA_TEST_HELPER_HOLD_AFTER_BATCH_MS": true,
		// Explicit, owner-only native diagnostics. The helper remains silent by
		// default and never inherits arbitrary process environment variables.
		"NIUMA_COMPUTER_DIAGNOSTICS_PATH": true}
	values := map[string]string{"PATH": "/usr/bin:/bin:/usr/sbin:/sbin"}
	for _, entry := range append(os.Environ(), extra...) {
		k, v, ok := strings.Cut(entry, "=")
		if ok && allowed[k] {
			values[k] = v
		}
	}
	env := make([]string, 0, len(values))
	for k, v := range values {
		env = append(env, k+"="+v)
	}
	return env
}

func New(ctx context.Context, c Config) (*Backend, error) {
	if strings.TrimSpace(c.HelperPath) == "" {
		return nil, errors.New("macOS computer helper path is required")
	}
	if c.RequestTimeout <= 0 {
		c.RequestTimeout = defaultTimeout
	}
	if c.RequestTimeout > maxTimeout {
		return nil, errors.New("helper request timeout exceeds maximum")
	}
	if c.MaxFrameBytes <= 0 {
		c.MaxFrameBytes = maxFrameBytes
	}
	if c.MaxFrameBytes > maxFrameBytes {
		return nil, errors.New("helper frame limit exceeds maximum")
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	// Request from the signed desktop host as well as the nested helper. macOS
	// attributes TCC prompts to the process making the request; keeping this
	// in the host process makes the visible niuma entry the source of truth.
	if c.RequestHostPermissions != nil {
		c.RequestHostPermissions()
	}
	driver := c.mouseDriver
	if driver == nil {
		driver = newHostMouseDriver()
	}
	batchDriver := c.batchDriver
	if batchDriver == nil {
		batchDriver = newHostInputBatchDriver()
	}
	broker, childFiles, err := newMouseBroker(driver, batchDriver)
	if err != nil {
		return nil, err
	}
	args := append(append([]string(nil), c.HelperArgs...), mouseBrokerArgument)
	p, err := native.StartProcess(ctx, c.HelperPath, args, helperEnvironment(c.HelperEnv), native.NewCodec(uint32(c.MaxFrameBytes)),
		native.WithExtraFiles(childFiles...), native.WithAbortHook(func() { _ = broker.close() }))
	for _, file := range childFiles {
		_ = file.Close()
	}
	if err != nil {
		_ = broker.close()
		<-broker.done
		return nil, err
	}
	b := &Backend{process: p, mouseBroker: broker, config: c, operation: make(chan struct{}, 1), images: map[string]imageData{}, actions: map[string]struct{}{}}
	if _, err = b.Capabilities(ctx); err != nil {
		_ = b.Close(context.Background())
		return nil, err
	}
	return b, nil
}

func (b *Backend) request(ctx context.Context, command, sessionID, actionID string, payload map[string]any) (helperResponse, error) {
	return b.requestWithTimeout(ctx, b.config.RequestTimeout, command, sessionID, actionID, payload)
}

func (b *Backend) requestWithTimeout(ctx context.Context, timeout time.Duration, command, sessionID, actionID string, payload map[string]any) (helperResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	deadline, _ := ctx.Deadline() // wall-clock deadline, including the caller's limit
	requestID := fmt.Sprintf("request-%d", b.requestCounter.Add(1))
	if sessionID == "" {
		sessionID = "host"
	}
	if actionID == "" {
		actionID = requestID
	}
	if payload == nil {
		payload = map[string]any{}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return helperResponse{}, &rejection{"invalid_payload"}
	}
	response, err := b.process.Call(ctx, native.Envelope{ProtocolVersion: native.ProtocolVersion, RequestID: requestID, SessionID: sessionID, ActionID: actionID, Deadline: deadline, Command: command, Payload: data})
	if err != nil {
		return helperResponse{}, err
	}
	var result helperResponse
	if json.Unmarshal(response.Payload, &result) != nil || result.OK == nil {
		b.process.Abort(errors.New("invalid helper response"))
		return result, errors.New("invalid helper response")
	}
	switch result.DispatchState {
	case "", cu.DispatchNotStarted, cu.DispatchComplete, cu.DispatchPartial, cu.DispatchUnknown:
	default:
		result.DispatchState = cu.DispatchUnknown
		b.process.Abort(errors.New("invalid helper dispatch state"))
		return result, errors.New("invalid helper dispatch state")
	}
	if command == commandExecute && result.Outcome == cu.OutcomeExecuted && (result.DispatchState == cu.DispatchPartial || result.DispatchState == cu.DispatchUnknown) {
		b.process.Abort(errors.New("contradictory helper dispatch state"))
		return result, errors.New("contradictory helper dispatch state")
	}
	if command == commandObserve || command == commandLaunchApp || command == commandExecute {
		diagnosticCode := ""
		if result.ErrorCode != "" {
			diagnosticCode = cu.PublicErrorCode(result.ErrorCode)
		}
		computerdiag.Append(computerdiag.GoMismatchPath, map[string]any{
			"layer": "go_backend", "phase": "helper_result", "command": command,
			"request_id": requestID, "action_id": actionID, "session_id": sessionID,
			"window_id": payload["window_id"], "generation": payload["generation"],
			"outcome": result.Outcome, "error_code": diagnosticCode, "dispatch_state": result.DispatchState,
		})
	}
	switch result.Outcome {
	case cu.OutcomeExecuted:
		if !*result.OK {
			err = errors.New("invalid helper success")
		}
	case cu.OutcomeRejected, cu.OutcomeNotStarted:
		if *result.OK {
			err = errors.New("invalid helper rejection")
		} else {
			code := result.ErrorCode
			if code == "" {
				code = "not_dispatched"
			}
			return result, &rejection{code: code}
		}
	case cu.OutcomeUnknown, cu.OutcomeFailed:
		return result, errors.New("helper could not confirm action outcome")
	default:
		err = errors.New("invalid helper outcome")
	}
	if err != nil {
		b.process.Abort(err)
	}
	return result, err
}

func (b *Backend) requestPermissions(ctx context.Context) error {
	// A production host checker owns the TCC decision. Never ask the nested
	// helper to request its own grant: macOS may attribute that prompt to a
	// different identity and make a revoked host permission look approved.
	if b.config.CheckHostPermissions != nil {
		return nil
	}
	// Legacy test/fake configurations retain the old probe contract.
	if _, err := b.request(ctx, commandRequestPermissions, "", "", nil); err != nil {
		return err
	}
	return nil
}

func (b *Backend) hostCaptureAllowed() bool {
	if b.config.CheckHostPermissions == nil {
		return true
	}
	captureAllowed, _ := b.config.CheckHostPermissions()
	return captureAllowed
}

func (b *Backend) hostInputAllowed() bool {
	if b.config.CheckHostPermissions == nil {
		return true
	}
	_, inputAllowed := b.config.CheckHostPermissions()
	return inputAllowed
}

// RuntimeFaultCode reports only the stable helper lifecycle state needed by
// the long-lived runtime supervisor. It is intentionally read-only and
// redacted; native error text remains inside diagnostics.
func (b *Backend) RuntimeFaultCode() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failed {
		return cu.ErrorCodeHelperUnavailable
	}
	return ""
}

func (b *Backend) Capabilities(ctx context.Context) (cu.Capabilities, error) {
	b.mu.Lock()
	closed, failed := b.closed, b.failed
	b.mu.Unlock()
	if closed {
		return cu.Capabilities{}, &rejection{"closed"}
	}
	if failed {
		return cu.Capabilities{}, &helperFault{}
	}
	if err := b.requestPermissions(ctx); err != nil {
		return cu.Capabilities{}, err
	}
	response, err := b.request(ctx, commandReadiness, "", "", nil)
	if err != nil {
		return cu.Capabilities{}, err
	}
	data, _ := json.Marshal(response.Result)
	var caps cu.Capabilities
	if err = json.Unmarshal(data, &caps); err != nil {
		return caps, errors.New("invalid helper capabilities")
	}
	// Overlay the signed host's current TCC state. The helper response is
	// useful for geometry/window metadata but is not permission authority.
	if b.config.CheckHostPermissions != nil {
		captureAllowed, inputAllowed := b.config.CheckHostPermissions()
		if !captureAllowed {
			caps.CaptureReadiness = cu.ReadinessPermissionRequired
		}
		if !inputAllowed {
			caps.InputReadiness = cu.ReadinessPermissionRequired
		}
		if !captureAllowed || !inputAllowed {
			caps.PermissionState = cu.PermissionRequired
		}
	}
	caps.ProtocolVersion = cu.ProtocolVersion
	caps.Platform = cu.PlatformMacOS
	caps.Backend = cu.BackendNativeHost
	caps.Actions = []cu.ActionKind{cu.ActionClick, cu.ActionDoubleClick, cu.ActionRightClick, cu.ActionMove, cu.ActionDrag, cu.ActionType, cu.ActionKey, cu.ActionHotkey, cu.ActionScroll, cu.ActionWait}
	caps.CoordinateSpace.Origin = cu.OriginTopLeft
	caps.CoordinateSpace.Unit = cu.CoordinatePixels
	if err = caps.Validate(); err != nil {
		return cu.Capabilities{}, err
	}
	b.mu.Lock()
	b.capabilities = caps
	b.mu.Unlock()
	return caps.Clone(), nil
}

// LaunchApp launches the trusted provider key resolved by the host target
// registry. The model never supplies this value directly; the controller
// passes only a registry-owned opaque key.
func (b *Backend) LaunchApp(ctx context.Context, sessionID, providerKey string) (receipt cu.LaunchReceipt, resultErr error) {
	started := b.config.Now()
	receipt = cu.LaunchReceipt{BundleID: providerKey, Outcome: cu.OutcomeRejected}
	finish := func(err error) (cu.LaunchReceipt, error) {
		receipt.CompletedAt = b.config.Now()
		receipt.Duration = receipt.CompletedAt.Sub(started)
		if err != nil {
			if receipt.ErrorCode == "" {
				receipt.ErrorCode = cu.ErrorCodeLaunchFailed
			}
			receipt.ErrorCode = cu.PublicErrorCode(receipt.ErrorCode)
		}
		return receipt, err
	}
	if strings.TrimSpace(sessionID) == "" {
		receipt.ErrorCode = cu.ErrorCodeInactive
		return finish(&rejection{cu.ErrorCodeInactive})
	}
	if strings.TrimSpace(providerKey) == "" || len(providerKey) > 256 || !b.allowedProviderKey(providerKey) {
		receipt.ErrorCode = cu.ErrorCodeUnsupportedTarget
		return finish(&rejection{cu.ErrorCodeUnsupportedTarget})
	}
	if !b.hostCaptureAllowed() {
		receipt.ErrorCode = cu.ErrorCodePermissionRequired
		return finish(&rejection{cu.ErrorCodePermissionRequired})
	}
	epoch, err := b.acquire(ctx)
	if err != nil {
		return finish(err)
	}
	defer func() { <-b.operation }()
	response, err := b.request(ctx, commandLaunchApp, sessionID, "", map[string]any{
		"target_id":    providerKey,
		"display_name": providerKey,
		"bundle_id":    providerKey,
		"generation":   epoch,
	})
	if err != nil {
		var rejected *rejection
		var call *native.CallError
		knownRejection := errors.As(err, &rejected) && response.ErrorCode != cu.ErrorCodeInputUncertain &&
			response.DispatchState != cu.DispatchUnknown && response.DispatchState != cu.DispatchPartial
		knownUnsent := errors.As(err, &call) && !call.MayHaveRun
		if !knownRejection && !knownUnsent {
			// Once launch dispatch may have happened, a missing or ambiguous
			// acknowledgement is never proof the application was not started.
			receipt.Outcome = cu.OutcomeUnknown
			receipt.ErrorCode = cu.ErrorCodeInputUncertain
			if response.ErrorCode != "" {
				receipt.ErrorCode = cu.PublicErrorCode(response.ErrorCode)
			}
			b.invalidate()
		} else if knownRejection {
			receipt.ErrorCode = cu.PublicErrorCode(rejected.Code())
		}
		if receipt.ErrorCode == "" && (errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded)) {
			receipt.ErrorCode = cu.ErrorCodeLaunchTimeout
		}
		return finish(err)
	}
	if bundleID, ok := response.Result["bundle_id"].(string); ok {
		receipt.BundleID = bundleID
	}
	window, ok := decodeWindowRef(response.Result["window"])
	if !ok || receipt.BundleID == "" || window.BundleID != receipt.BundleID || window.OwnerPID <= 0 {
		receipt.Outcome = cu.OutcomeUnknown
		receipt.ErrorCode = cu.ErrorCodeLaunchFailed
		b.invalidate()
		return finish(errors.New("invalid launch metadata"))
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.validEpoch(epoch) {
		receipt.ErrorCode = cu.ErrorCodeInactive
		return finish(&rejection{cu.ErrorCodeInactive})
	}
	binding, bindingErr := cu.NewTargetBinding(window, epoch, receipt.CompletedAt)
	if bindingErr != nil {
		receipt.ErrorCode = cu.ErrorCodeTargetWindowMismatch
		return finish(bindingErr)
	}
	b.targetWindowID = window.ID
	b.targetBinding = &binding
	receipt.Window = window
	receipt.Outcome = cu.OutcomeExecuted
	return finish(nil)
}

func (b *Backend) allowedProviderKey(key string) bool {
	for _, allowed := range b.config.AllowedProviderKeys {
		if key == allowed {
			return true
		}
	}
	return false
}

func (b *Backend) acquire(ctx context.Context) (uint64, error) {
	select {
	case b.operation <- struct{}{}:
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failed {
		<-b.operation
		return 0, &helperFault{}
	}
	if b.closed || b.stopped || b.paused {
		<-b.operation
		return 0, &rejection{"inactive"}
	}
	if err := ctx.Err(); err != nil {
		<-b.operation
		return 0, err
	}
	return b.epoch, nil
}
func (b *Backend) validEpoch(epoch uint64) bool {
	return !b.closed && !b.stopped && !b.paused && b.epoch == epoch
}

func (b *Backend) Observe(ctx context.Context, req cu.ObserveRequest) (cu.Observation, error) {
	if !b.hostCaptureAllowed() {
		return cu.Observation{}, &rejection{"permission_required"}
	}
	epoch, err := b.acquire(ctx)
	if err != nil {
		return cu.Observation{}, err
	}
	defer func() { <-b.operation }()
	if req.SessionID == "" {
		return cu.Observation{}, &rejection{"unsupported_target"}
	}
	b.mu.Lock()
	if req.WindowID == "" && b.targetWindowID != "" {
		req.WindowID = b.targetWindowID
	}
	b.mu.Unlock()
	id := fmt.Sprintf("observation-%d", b.requestCounter.Add(1))
	response, err := b.request(ctx, commandObserve, req.SessionID, "", map[string]any{"display_id": req.DisplayID, "window_id": req.WindowID, "observation_id": id, "generation": epoch})
	if err != nil {
		errorCode := "backend_observe_failed"
		var rejected *rejection
		if errors.As(err, &rejected) {
			errorCode = rejected.Code()
		}
		computerdiag.Append(computerdiag.GoMismatchPath, map[string]any{
			"layer": "go_backend", "phase": "observe_request_error", "observation_id": id,
			"session_id": req.SessionID, "window_id": req.WindowID, "display_id": req.DisplayID, "error_code": cu.PublicErrorCode(errorCode),
		})
		return cu.Observation{}, classifyObserveFault(b, err)
	}
	data, mediaType, width, height, err := decodeImage(response.Result)
	if err != nil {
		return cu.Observation{}, classifyObserveFault(b, err)
	}
	display, ok := response.Result["display_id"].(string)
	scale, okScale := response.Result["scale_factor"].(float64)
	if !ok || display == "" || !okScale || math.IsNaN(scale) || math.IsInf(scale, 0) || scale <= 0 || scale > 8 || (req.DisplayID != "" && req.DisplayID != display) {
		return cu.Observation{}, classifyObserveFault(b, errors.New("invalid screenshot coordinate metadata"))
	}
	coordinateSpace, err := decodeCaptureGeometry(response.Result, display, scale, width, height)
	if err != nil {
		return cu.Observation{}, classifyObserveFault(b, err)
	}
	targetWindow, err := decodeCaptureTarget(response.Result, req.WindowID)
	if err != nil {
		return cu.Observation{}, classifyObserveFault(b, err)
	}
	if targetWindow.ID != "" {
		// The window origin can shift by a few pixels during launch animation
		// or Dock placement between the target_window lookup and the capture.
		// Size must match; origin drift is expected and is reconciled to the
		// capture's coordinate space, which is the authoritative position for
		// the screenshot we actually took.
		if targetWindow.Frame.Width != coordinateSpace.Bounds.Width || targetWindow.Frame.Height != coordinateSpace.Bounds.Height {
			computerdiag.Append(computerdiag.GoMismatchPath, map[string]any{
				"layer": "go_backend", "phase": "capture_geometry_mismatch", "window_id": targetWindow.ID,
				"target_width": targetWindow.Frame.Width, "target_height": targetWindow.Frame.Height,
				"capture_width": coordinateSpace.Bounds.Width, "capture_height": coordinateSpace.Bounds.Height,
				"error_code": cu.ErrorCodeTargetWindowMismatch,
			})
			return cu.Observation{}, &rejection{cu.ErrorCodeTargetWindowMismatch}
		}
		targetWindow.Frame = coordinateSpace.Bounds
	}
	b.mu.Lock()
	bound := b.targetBinding
	b.mu.Unlock()
	if bound != nil && !bound.Matches(targetWindow) {
		computerdiag.Append(computerdiag.GoMismatchPath, map[string]any{
			"layer": "go_backend", "phase": "target_binding_mismatch", "observation_id": id,
			"bound_window": map[string]any{
				"id": bound.Window.ID, "owner_pid": bound.Window.OwnerPID, "bundle_id": bound.Window.BundleID,
			},
			"current_window": map[string]any{
				"id": targetWindow.ID, "owner_pid": targetWindow.OwnerPID, "bundle_id": targetWindow.BundleID,
			},
			"error_code": cu.ErrorCodeTargetWindowMismatch,
		})
		return cu.Observation{}, &rejection{cu.ErrorCodeTargetWindowMismatch}
	}
	now := b.config.Now()
	// Use the helper's actual snapshot expiry, not this capture RPC's timeout.
	// Requiring its bounded metadata fails closed with stale/unmatched helpers.
	expiryText, okExpiry := response.Result["observation_expires_at"].(string)
	expires, expiryErr := time.Parse(time.RFC3339Nano, expiryText)
	if !okExpiry || expiryErr != nil || !expires.After(now) || expires.After(now.Add(cu.DefaultObservationTTL)) {
		return cu.Observation{}, classifyObserveFault(b, errors.New("invalid observation expiration metadata"))
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.validEpoch(epoch) {
		return cu.Observation{}, &rejection{"inactive"}
	}
	caps := b.capabilities.Clone()
	activeWindow, _ := decodeWindowRef(response.Result["active_window"])
	caps.CoordinateSpace = coordinateSpace
	// A display capture explicitly clears the preceding window target.
	caps.TargetWindow = targetWindow
	obs := cu.Observation{ID: id, SessionID: req.SessionID, DisplayID: display, WindowID: req.WindowID, ActiveWindow: activeWindow, Width: width, Height: height, ScaleFactor: scale, Screenshot: cu.NewMediaRef(id, mediaType, data, width, height), Capabilities: caps, ObservedAt: now, ExpiresAt: expires}
	b.images = map[string]imageData{id: {data: data, mediaType: mediaType}}
	b.observation = obs.Clone()
	b.capabilities = caps.Clone()
	return obs, nil
}

func (b *Backend) ObservationImage(ctx context.Context, id string) ([]byte, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	image, ok := b.images[id]
	if !ok {
		return nil, "", errors.New("observation image unavailable")
	}
	return append([]byte(nil), image.data...), image.mediaType, nil
}

func (b *Backend) Execute(ctx context.Context, action cu.Action) (receipt cu.ActionReceipt, resultErr error) {
	started := b.config.Now()
	receipt = cu.ActionReceipt{ActionID: action.ID, SessionID: action.SessionID, Platform: cu.PlatformMacOS, Backend: cu.BackendNativeHost, BeforeObservationID: action.ObservationID, Outcome: cu.OutcomeRejected, Verification: cu.VerificationNotChecked, RedactedActionSummary: action.RedactedSummary()}
	finish := func(err error) (cu.ActionReceipt, error) {
		receipt.CompletedAt = b.config.Now()
		receipt.Duration = receipt.CompletedAt.Sub(started)
		if err != nil {
			receipt.ErrorMessage = "computer action did not complete"
			var rejected *rejection
			var coded interface{ Code() string }
			if errors.As(err, &rejected) {
				receipt.ErrorCode = cu.PublicErrorCode(rejected.Code())
			} else if errors.As(err, &coded) {
				receipt.ErrorCode = cu.PublicErrorCode(coded.Code())
			} else if receipt.ErrorCode == "" {
				receipt.ErrorCode = cu.ErrorCodeActionFailed
			}
		}
		return receipt, err
	}
	if !b.hostCaptureAllowed() || !b.hostInputAllowed() {
		return finish(&rejection{"permission_required"})
	}
	epoch, err := b.acquire(ctx)
	if err != nil {
		return finish(err)
	}
	defer func() { <-b.operation }()
	b.mu.Lock()
	obs := b.observation
	_, duplicate := b.actions[action.ID]
	valid := b.validEpoch(epoch) && !duplicate && len(b.actions) < maxActions && action.SessionID == obs.SessionID && action.ObservationID == obs.ID && obs.ID != "" && (action.DisplayID == "" || action.DisplayID == obs.DisplayID) && (action.WindowID == "" || action.WindowID == obs.WindowID)
	if valid {
		b.actions[action.ID] = struct{}{}
	}
	b.mu.Unlock()
	if !valid {
		return finish(&rejection{"invalid_action_binding"})
	}
	if err = action.Validate(b.config.Now(), obs); err != nil {
		return finish(&rejection{"invalid_action"})
	}
	b.mu.Lock()
	b.observation = cu.Observation{} // consume before dispatch, including rejection
	b.mu.Unlock()
	executionTimeout := cu.ActionExecutionTimeout(action, b.config.RequestTimeout)
	payload := map[string]any{"generation": epoch, "kind": action.Kind, "observation_id": action.ObservationID, "display_id": obs.DisplayID, "window_id": action.WindowID, "text": action.Text, "key": action.Key, "keys": action.Keys, "delta_x": action.DeltaX, "delta_y": action.DeltaY, "duration_ms": action.DurationMS}
	if action.Point != nil {
		payload["x"] = action.Point.X
		payload["y"] = action.Point.Y
	}
	if action.StartPoint != nil {
		payload["start_x"] = action.StartPoint.X
		payload["start_y"] = action.StartPoint.Y
	}
	if action.Button != "" {
		payload["button"] = action.Button
	}
	if b.selfTargetRejected(obs, action) {
		return finish(&rejection{selfTargetCode})
	}

	if action.Kind == cu.ActionDrag {
		// Serialize authorization with epoch changes. Broker never calls back
		// into Backend, preserving the lock order Backend.mu -> broker.mu.
		b.mu.Lock()
		if !b.validEpoch(epoch) || b.mouseBroker == nil {
			b.mu.Unlock()
			return finish(&rejection{"inactive"})
		}
		token, authorizeErr := b.mouseBroker.authorize(ctx, action, epoch, executionTimeout)
		b.mu.Unlock()
		if authorizeErr != nil {
			return finish(&rejection{mouseBrokerInputUnavailable})
		}
		payload["drag_token"] = token
		defer func() {
			lease := b.mouseBroker.finish(token)
			// Cleanup never upgrades a receipt. A claimed success without a
			// completed broker gesture, or rejection after down, is uncertain.
			uncertain := (receipt.Outcome == cu.OutcomeExecuted && !lease.complete) ||
				(receipt.Outcome == cu.OutcomeRejected && lease.pressed) || lease.err != nil
			if uncertain {
				receipt.Outcome, receipt.Verification = cu.OutcomeUnknown, cu.VerificationUnknown
				receipt.AfterObservationID, receipt.After = "", nil
				receipt.ErrorMessage = "computer action did not complete"
				if resultErr == nil {
					resultErr = errors.New("drag outcome unknown")
				}
				b.invalidate()
			}
		}()
	}
	if isBatchAction(action.Kind) {
		b.mu.Lock()
		if !b.validEpoch(epoch) || b.mouseBroker == nil {
			b.mu.Unlock()
			return finish(&rejection{"inactive"})
		}
		token, count, authorizeErr := b.mouseBroker.authorizeBatch(ctx, action, obs, executionTimeout)
		b.mu.Unlock()
		if authorizeErr != nil {
			return finish(&rejection{mouseBrokerInputUnavailable})
		}
		payload[inputBatchTokenField], payload[inputBatchCountField] = token, count
		defer func() {
			batch := b.mouseBroker.finishBatch(token)
			if (receipt.Outcome == cu.OutcomeExecuted && !batch.complete) || (receipt.Outcome == cu.OutcomeRejected && batch.started) {
				receipt.Outcome, receipt.Verification = cu.OutcomeUnknown, cu.VerificationUnknown
				receipt.AfterObservationID, receipt.After = "", nil
				receipt.ErrorMessage = "computer action did not complete"
				if resultErr == nil {
					resultErr = errors.New("input batch outcome unknown")
				}
				b.invalidate()
			}
		}()
	}

	response, err := b.requestWithTimeout(ctx, executionTimeout, commandExecute, action.SessionID, action.ID, payload)
	if point, ok := decodeActualPoint(response.Result["actual_point"]); ok {
		receipt.ActualPoint = &point
	}
	if point, ok := decodeGlobalPoint(response.Result["global_point"]); ok {
		receipt.ActualGlobalPoint = &point
	}
	receipt.DispatchState = response.DispatchState
	if err != nil {
		var rejected *rejection
		var call *native.CallError
		if !errors.As(err, &rejected) && !(errors.As(err, &call) && !call.MayHaveRun) {
			receipt.Outcome = cu.OutcomeUnknown
			receipt.Verification = cu.VerificationUnknown
			// Surface the native error code so callers can distinguish a transient
			// focus change (recoverable via fresh Observe) from a fatal helper
			// failure. finish() only overwrites an empty code.
			if response.ErrorCode != "" {
				receipt.ErrorCode = cu.PublicErrorCode(response.ErrorCode)
			} else {
				receipt.ErrorCode = cu.ErrorCodeHelperUnavailable
			}
			// A locally requested Pause/Stop invalidated this generation and the
			// native executor acknowledged interruption after releasing input.
			// Do not kill it before the queued control acknowledgement arrives.
			// This exception never turns partial input into success or replay.
			if !b.cooperativeInterruption(epoch, response) {
				b.invalidate()
			}
		}
		return finish(err)
	}
	if response.Outcome == cu.OutcomeExecuted && response.DispatchState == cu.DispatchComplete && response.ErrorCode != "" {
		receipt.Outcome, receipt.Verification = cu.OutcomeExecuted, cu.VerificationUnknown
		receipt.ErrorCode = cu.PublicErrorCode(response.ErrorCode)
		b.mu.Lock()
		b.observation = cu.Observation{}
		b.mu.Unlock()
		return finish(nil)
	}
	data, mediaType, width, height, err := decodeImage(response.Result)
	if err != nil || width != obs.Width || height != obs.Height || response.Result["display_id"] != obs.DisplayID || response.Result["scale_factor"] != obs.ScaleFactor {
		receipt.Outcome = cu.OutcomeUnknown
		receipt.Verification = cu.VerificationUnknown
		if response.DispatchState == cu.DispatchComplete {
			receipt.Outcome, receipt.Verification = cu.OutcomeExecuted, cu.VerificationUnknown
			receipt.ErrorCode = cu.ErrorCodeScreenshotFailed
			b.mu.Lock()
			b.observation = cu.Observation{}
			b.mu.Unlock()
			return finish(nil)
		}
		// Alive is not proof of complete input. Legacy/invalid uncertain
		// responses remain terminal and can never be replayed under another ID.
		b.invalidate()
		return finish(errors.New("invalid after screenshot"))
	}
	if activeWindow, ok := decodeWindowRef(response.Result["active_window"]); ok {
		receipt.ActiveWindowAfter = activeWindow
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.validEpoch(epoch) {
		receipt.Outcome = cu.OutcomeUnknown
		receipt.Verification = cu.VerificationUnknown
		return finish(errors.New("action interrupted"))
	}
	afterID := fmt.Sprintf("observation-after-%d", b.requestCounter.Add(1))
	b.images[afterID] = imageData{data: data, mediaType: mediaType}
	b.observation = cu.Observation{} // fresh observe required before another action
	receipt.Outcome = cu.OutcomeExecuted
	receipt.AfterObservationID = afterID
	after := cu.NewMediaRef(afterID, mediaType, data, width, height)
	receipt.After = &after
	return finish(nil)
}

func (b *Backend) cooperativeInterruption(epoch uint64, response helperResponse) bool {
	if response.OK == nil || *response.OK || response.Outcome != cu.OutcomeUnknown || response.ErrorCode != helperInactiveCode {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return !b.closed && b.paused && b.epoch > epoch
}

func (b *Backend) quarantine() {
	b.mu.Lock()
	b.mouseBroker.revoke()
	b.paused = true
	b.failed = true
	b.epoch++
	b.observation = cu.Observation{}
	b.mu.Unlock()
}

func (b *Backend) invalidate() {
	b.quarantine()
	b.process.Abort(errors.New("action outcome unknown; new helper required"))
}
func (b *Backend) Pause(ctx context.Context) error  { return b.control(ctx, commandPause) }
func (b *Backend) Resume(ctx context.Context) error { return b.control(ctx, commandResume) }
func (b *Backend) Stop(ctx context.Context) error   { return b.control(ctx, commandStop) }
func (b *Backend) control(ctx context.Context, command string) error {
	b.mu.Lock()
	if b.closed || (b.stopped && command != commandStop) || (b.failed && command != commandStop) {
		b.mu.Unlock()
		return &rejection{"inactive"}
	}
	if command == commandStop && b.stopped {
		b.mu.Unlock()
		return nil
	}
	quarantined := b.failed
	b.mouseBroker.revoke()
	b.epoch++
	epoch := b.epoch
	b.paused = true
	b.observation = cu.Observation{}
	if command == commandStop {
		b.stopped = true
		b.targetWindowID = ""
		b.targetBinding = nil
		b.images = map[string]imageData{}
	}
	b.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, native.ShutdownGrace)
	defer cancel()
	if command == commandStop && quarantined {
		// Quarantine already revoked/released host input and aborted this
		// exact helper. A dead transport cannot ACK another Stop; confirm
		// termination instead, never restore authority or replay anything.
		return b.process.Wait(ctx)
	}
	_, err := b.request(ctx, command, "", "", map[string]any{"generation": epoch})
	b.mu.Lock()
	if command == commandResume && err == nil && b.epoch == epoch && !b.stopped && !b.closed {
		b.paused = false
	}
	b.mu.Unlock()
	if err != nil {
		b.process.Abort(errors.New("helper control failed"))
	}
	return err
}
func (b *Backend) Close(ctx context.Context) error {
	b.mu.Lock()
	first := !b.closed
	b.mouseBroker.revoke()
	b.closed = true
	b.stopped = true
	b.paused = true
	b.epoch++
	b.images = map[string]imageData{}
	b.targetWindowID = ""
	b.targetBinding = nil
	b.observation = cu.Observation{}
	b.mu.Unlock()
	var cleanupErr error
	if b.mouseBroker != nil {
		cleanupErr = b.mouseBroker.close()
	}
	if first {
		closeCtx, cancel := context.WithTimeout(ctx, native.ShutdownGrace)
		_, err := b.request(closeCtx, commandShutdown, "", "", nil)
		cancel()
		if err != nil {
			b.process.Abort(errors.New("helper shutdown failed"))
		}
	}
	return errors.Join(b.process.Wait(ctx), cleanupErr)
}

var _ cu.Backend = (*Backend)(nil)
var _ interface {
	ObservationImage(context.Context, string) ([]byte, string, error)
} = (*Backend)(nil)
