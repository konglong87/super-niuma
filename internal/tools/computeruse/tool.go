// Package computeruse adapts the provider-neutral Computer Use service to the
// agent Tool interface. It contains no platform or native helper code.
package computeruse

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/computerdiag"
	cu "github.com/konglong87/super-niuma/internal/computeruse"
	"github.com/konglong87/super-niuma/internal/tools"
)

const (
	ToolName                  = "ComputerUse"
	toolObserveDiagnosticPath = computerdiag.ToolObservePath
	pngMediaType              = "image/png"
	maxPNGBytes               = 16 << 20
	maxPNGPixels              = 32 << 20

	observationScreenshotGuidance = "Fresh Computer Use observation screenshot. Reference this observation ID for one input action. Each successful input returns another fresh observation; inspect it before deciding the next action and do not issue a redundant observe unless the transition is unsettled or the result is insufficient."
	actionEvidenceGuidance        = "Computer Use post-action evidence only; not an actionable observation. A successful action also returns a fresh authoritative observation for the next input; never use this evidence image ID as observation_id. On an error or unknown outcome, stop without replaying input."
	screenshotCoordinateGuidance  = "Use full-image pixel coordinates with top-left origin, not window-relative or scaled-preview coordinates. Do not divide by scale_factor; the native backend performs that conversion."
)

type ObservationImageProvider interface {
	ObservationImage(context.Context, cu.SessionOwner, string, string) ([]byte, string, error)
}

// SessionCoordinator lets the trusted desktop integration lazily bind the first
// model ComputerUse call to the current conversation. It is intentionally an
// optional capability: local preview and test fakes still require explicit IDs.
type SessionCoordinator interface {
	EnsureComputerSession(context.Context, cu.SessionOwner) (string, error)
}

// SessionBinding exposes the already-created host session for subsequent
// actions. The model may omit session_id after the first observe; the trusted
// desktop adapter supplies the bound ID, never model text.
type SessionBinding interface {
	CurrentComputerSession(context.Context, cu.SessionOwner) (string, error)
	CurrentComputerObservation(context.Context, cu.SessionOwner) (string, error)
}

// Tool carries no authority: registry clones may safely share this value.
type Tool struct{}

func New() Tool { return Tool{} }

// atomicTurnService selects the authoritative provider path. Query wrappers may
// expose a method-shaped compatibility adapter while their underlying bridge
// is still legacy; those adapters explicitly opt out instead of causing the
// tool to attempt a fake atomic call or silently stitch fallback evidence.
func resolveAtomicTurnService(service cu.Service) (cu.TurnService, bool) {
	if gate, ok := service.(interface{ AtomicTurnAvailable() bool }); ok && !gate.AtomicTurnAvailable() {
		return nil, false
	}
	turnService, ok := service.(cu.TurnService)
	return turnService, ok
}

func (Tool) Name() string { return ToolName }

func (Tool) Description() string {
	return "Observe and operate an explicitly approved computer session using structured actions. If the requested target is closed, the first call must be action=launch_app with its registered target_id; the host atomically launches, binds, and returns the target-window observation and screenshot in the same turn. Omit session_id on that first call; the trusted host binds it. Use observe for an already running target or when fresh observation is necessary, not as an extra prerequisite to atomic launch. Each input action consumes one fresh observation. A successful input returns its receipt plus a new authoritative observation and screenshot for the next decision; inspect it instead of issuing a redundant observe unless the transition is unsettled or the result is insufficient. Use observation.id, never receipt.after_observation_id. Stop on errors or unknown outcomes; never replay input."
}

func (Tool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string"},"action":{"type":"string","enum":["observe","launch_app","click","double_click","right_click","move","drag","type","key","hotkey","scroll","wait","pause","stop"]},"target_id":{"type":"string","description":"Registered target identifier. Use action=launch_app with this ID for a closed target; launch returns its bound observation and screenshot atomically."},"application":{"type":"string","description":"Deprecated alias for target_id"},"display_id":{"type":"string"},"window_id":{"type":"string"},"observation_id":{"type":"string"},"x":{"type":"integer"},"y":{"type":"integer"},"start_x":{"type":"integer"},"start_y":{"type":"integer"},"button":{"type":"string","enum":["left","right"]},"text":{"type":"string"},"key":{"type":"string"},"keys":{"type":"array","items":{"type":"string"}},"delta_x":{"type":"integer"},"delta_y":{"type":"integer"},"duration_ms":{"type":"integer"}},"required":["action"],"additionalProperties":false}`)
}

func (Tool) ExecutionPolicy() tools.ExecutionPolicy {
	return tools.ExecutionPolicy{Concurrency: tools.ConcurrencySerial}
}

func (t Tool) Run(ctx context.Context, input json.RawMessage, tc tools.Context) tools.Result {
	if tc.SubagentDepth > 0 {
		return errorResult("capability_unavailable", "computer use is not available to subagents")
	}
	service := tc.ComputerUse
	if service == nil {
		return errorResult("capability_unavailable", "computer use service is unavailable")
	}
	if !tc.ComputerUseImageSupported {
		return errorResult("capability_unavailable", "computer use requires provider image input support")
	}
	if tc.TenantID == 0 || tc.UserID == 0 || tc.SessionID == 0 {
		return errorResult("invalid_context", "trusted tenant, user, and session context are required")
	}
	var params request
	if err := decode(input, &params); err != nil {
		return errorResult("invalid_input", "invalid computer use request")
	}
	owner := cu.SessionOwner{TenantID: tc.TenantID, UserID: tc.UserID, SessionID: tc.SessionID}
	if strings.TrimSpace(params.TargetID) == "" {
		// Legacy callers used application. Treat it only as a target alias;
		// the host registry still decides whether it is launchable.
		params.TargetID = strings.TrimSpace(params.Application)
	}
	if cu.ActionKind(params.Action) == cu.ActionLaunchApp && strings.TrimSpace(params.TargetID) == "" {
		return errorResult("invalid_input", "target_id is required for launch_app")
	}
	// Every action uses the query-owned session, never a model-invented or stale
	// session ID. Window/observation identity is still validated independently.
	if params.SessionID != "" || cu.ActionKind(params.Action) == cu.ActionLaunchApp {
		if binding, ok := service.(SessionBinding); ok {
			if boundID, bindErr := binding.CurrentComputerSession(ctx, owner); bindErr == nil && strings.TrimSpace(boundID) != "" {
				params.SessionID = boundID
			}
		}
	}
	if strings.TrimSpace(params.SessionID) == "" {
		var sessionID string
		if cu.ActionKind(params.Action) == cu.ActionObserve || cu.ActionKind(params.Action) == cu.ActionLaunchApp {
			coordinator, ok := service.(SessionCoordinator)
			if !ok {
				return errorResult("session_start_unavailable", "computer session must be started explicitly")
			}
			var startErr error
			sessionID, startErr = coordinator.EnsureComputerSession(ctx, owner)
			if startErr != nil {
				return errorResult("session_start_failed", "computer session could not be started")
			}
		} else {
			binding, ok := service.(SessionBinding)
			if !ok {
				return errorResult("invalid_input", "session_id is required for this action")
			}
			var bindErr error
			sessionID, bindErr = binding.CurrentComputerSession(ctx, owner)
			if bindErr != nil {
				return errorResult("session_binding_failed", "computer session binding is unavailable")
			}
		}
		if strings.TrimSpace(sessionID) == "" {
			return errorResult("session_start_failed", "computer session could not be started")
		}
		params.SessionID = sessionID
	}
	if cu.ActionKind(params.Action).IsInput() && strings.TrimSpace(params.ObservationID) == "" {
		binding, ok := service.(SessionBinding)
		if !ok {
			return errorResult("invalid_input", "observation_id is required for this action")
		}
		observationID, bindErr := binding.CurrentComputerObservation(ctx, owner)
		if bindErr != nil || strings.TrimSpace(observationID) == "" {
			return errorResult("observation_binding_failed", "computer observation binding is unavailable")
		}
		params.ObservationID = observationID
	}
	switch cu.ActionKind(params.Action) {
	case cu.ActionObserve:
		return t.observe(ctx, service, owner, params, tc)
	case cu.ActionLaunchApp:
		return t.launchApp(ctx, service, owner, params, tc)
	case cu.ActionPause:
		return t.control(ctx, owner, params, func() error { return service.Pause(ctx, owner, params.SessionID) })
	case cu.ActionStop:
		return t.control(ctx, owner, params, func() error { return service.Stop(ctx, owner, params.SessionID) })
	case cu.ActionClick, cu.ActionDoubleClick, cu.ActionRightClick, cu.ActionMove, cu.ActionDrag, cu.ActionType, cu.ActionKey, cu.ActionHotkey, cu.ActionScroll, cu.ActionWait:
		if turnService, ok := resolveAtomicTurnService(service); ok {
			return t.executeTurn(ctx, turnService, owner, params, tc)
		}
		// Legacy adapters remain available only when they do not expose the new
		// atomic capability. New provider/runtime paths never fall back after a
		// turn service has been selected.
		return t.execute(ctx, service, owner, params, tc)
	default:
		return errorResult("invalid_input", "unsupported computer action")
	}
}

type request struct {
	SessionID string `json:"session_id"`
	Action    string `json:"action"`
	TargetID  string `json:"target_id"`
	// Application is legacy input and is intentionally ignored by generic launch.
	Application   string   `json:"application"`
	DisplayID     string   `json:"display_id"`
	WindowID      string   `json:"window_id"`
	ObservationID string   `json:"observation_id"`
	X             *int     `json:"x"`
	Y             *int     `json:"y"`
	StartX        *int     `json:"start_x"`
	StartY        *int     `json:"start_y"`
	Button        string   `json:"button"`
	Text          string   `json:"text"`
	Key           string   `json:"key"`
	Keys          []string `json:"keys"`
	DeltaX        int      `json:"delta_x"`
	DeltaY        int      `json:"delta_y"`
	DurationMS    int      `json:"duration_ms"`
}

func (t Tool) launchApp(ctx context.Context, service cu.Service, owner cu.SessionOwner, params request, tc tools.Context) tools.Result {
	// The atomic turn capability is authoritative. Legacy launch remains only
	// for migration adapters that do not expose TurnService at all.
	if turnService, ok := resolveAtomicTurnService(service); ok {
		return t.executeTurn(ctx, turnService, owner, params, tc)
	}
	var receipt cu.LaunchReceipt
	var err error
	if launcher, ok := service.(cu.TargetLauncher); ok {
		receipt, err = launcher.LaunchTarget(ctx, owner, params.SessionID, cu.TargetID(params.TargetID))
	} else if legacy, ok := service.(cu.ApplicationLauncher); ok {
		// Compatibility path for older host adapters. The value remains an
		// opaque target identifier; no application-specific logic is here.
		receipt, err = legacy.LaunchApp(ctx, owner, params.SessionID, params.TargetID)
	} else {
		return errorResult("launch_unavailable", "computer target launcher is unavailable")
	}
	if receipt.ErrorCode != "" {
		receipt.ErrorCode = cu.PublicErrorCode(receipt.ErrorCode)
	}
	payload := map[string]any{"launch_receipt": receipt}
	if err != nil || receipt.Outcome != cu.OutcomeExecuted {
		errorCode := receipt.ErrorCode
		if errorCode == "" {
			errorCode = cu.ErrorCodeLaunchFailed
		}
		payload["error_code"] = errorCode
		payload["message"] = "computer target launch failed; inspect the launch receipt and stop"
		return tools.Result{Content: marshal(payload), IsError: true}
	}
	// Legacy adapters may still need a follow-up observation during migration.
	// This path is intentionally unreachable for the new provider/runtime route.
	params.WindowID = receipt.Window.ID
	_, result := t.captureObservation(ctx, service, owner, params)
	var boundPayload map[string]any
	if err := json.Unmarshal([]byte(result.Content), &boundPayload); err != nil || boundPayload == nil {
		boundPayload = map[string]any{"error_code": cu.ErrorCodeActionFailed, "message": "invalid launch observation result"}
		result.IsError = true
	}
	boundPayload["launch_receipt"] = receipt
	boundPayload["window_id"] = receipt.Window.ID
	boundPayload["bundle_id"] = receipt.BundleID
	result.Content = marshal(boundPayload)
	return result
}

func (t Tool) observe(ctx context.Context, service cu.Service, owner cu.SessionOwner, params request, tc tools.Context) tools.Result {
	computerdiag.Append(toolObserveDiagnosticPath, map[string]any{
		"layer":      "go_tool",
		"phase":      "observe_entry",
		"action":     params.Action,
		"fast_path":  tc.ComputerUseFastPath,
		"target_id":  params.TargetID,
		"session_id": params.SessionID,
		"window_id":  params.WindowID,
	})
	// The first fast-path observe is a target-selection boundary, not merely a
	// screenshot of whichever app happens to be frontmost. If this session
	// already has an observation, preserve normal observe semantics and never
	// relaunch the target on a later refresh.
	if tc.ComputerUseFastPath && strings.TrimSpace(params.TargetID) != "" {
		if binding, ok := service.(SessionBinding); ok {
			if current, err := binding.CurrentComputerObservation(ctx, owner); err == nil && strings.TrimSpace(current) != "" {
				computerdiag.Append(toolObserveDiagnosticPath, map[string]any{
					"layer": "go_tool", "phase": "refresh_existing_observation",
					"observation_id": current, "target_id": params.TargetID,
				})
				_, result := t.captureObservation(ctx, service, owner, params)
				return result
			}
		}

	}
	if tc.ComputerUseFastPath && strings.TrimSpace(params.TargetID) != "" {
		if turnService, ok := resolveAtomicTurnService(service); ok {
			// The first target observe is an atomic launch turn. It must not
			// observe the wrong frontmost app, then launch, then stitch a second
			// observation/image response in the tool layer.
			launchParams := params
			launchParams.Action = string(cu.ActionLaunchApp)
			return t.executeTurn(ctx, turnService, owner, launchParams, tc)
		}
	}
	_, result := t.captureObservation(ctx, service, owner, params)
	if result.IsError || !tc.ComputerUseFastPath {
		return result
	}
	// A simple target task must never make the model spend a turn clicking
	// the niuma control surface or merely observing another frontmost app.
	// On the first target observe, launch and bind the registered target in the
	// same ComputerUse turn, then return only the fresh target image.
	if strings.TrimSpace(params.TargetID) == "" {
		return result
	}
	computerdiag.Append(toolObserveDiagnosticPath, map[string]any{
		"layer": "go_tool", "phase": "launch_begin", "target_id": params.TargetID, "session_id": params.SessionID,
	})
	var receipt cu.LaunchReceipt
	var err error
	if launcher, ok := service.(cu.TargetLauncher); ok {
		receipt, err = launcher.LaunchTarget(ctx, owner, params.SessionID, cu.TargetID(params.TargetID))
	} else if legacy, ok := service.(cu.ApplicationLauncher); ok {
		receipt, err = legacy.LaunchApp(ctx, owner, params.SessionID, params.TargetID)
	} else {
		return result
	}
	if receipt.ErrorCode != "" {
		receipt.ErrorCode = cu.PublicErrorCode(receipt.ErrorCode)
	}
	computerdiag.Append(toolObserveDiagnosticPath, map[string]any{
		"layer": "go_tool", "phase": "launch_result", "target_id": params.TargetID,
		"outcome": receipt.Outcome, "error_code": receipt.ErrorCode, "window_id": receipt.Window.ID, "bundle_id": receipt.BundleID,
	})
	if err != nil || receipt.Outcome != cu.OutcomeExecuted || receipt.Window.ID == "" {
		payload := map[string]any{"launch_receipt": receipt, "error_code": receipt.ErrorCode, "message": "ComputerUse target launch failed; stop without input replay"}
		if payload["error_code"] == "" {
			payload["error_code"] = cu.ErrorCodeLaunchFailed
		}
		return tools.Result{Content: marshal(payload), IsError: true}
	}
	params.WindowID = receipt.Window.ID
	computerdiag.Append(toolObserveDiagnosticPath, map[string]any{
		"layer": "go_tool", "phase": "launch_bound_observe_begin", "target_id": params.TargetID, "window_id": params.WindowID,
	})
	_, boundResult := t.captureObservation(ctx, service, owner, params)
	computerdiag.Append(toolObserveDiagnosticPath, map[string]any{
		"layer": "go_tool", "phase": "launch_bound_observe_result", "target_id": params.TargetID,
		"window_id": params.WindowID, "is_error": boundResult.IsError,
	})
	var boundPayload map[string]any
	_ = json.Unmarshal([]byte(boundResult.Content), &boundPayload)
	boundPayload["launch_receipt"] = receipt
	boundPayload["window_id"] = receipt.Window.ID
	boundResult.Content = marshal(boundPayload)
	return boundResult
}

func (t Tool) captureObservation(ctx context.Context, service cu.Service, owner cu.SessionOwner, params request) (cu.Observation, tools.Result) {
	computerdiag.Append(toolObserveDiagnosticPath, map[string]any{
		"layer": "go_tool", "phase": "capture_observation_begin", "target_id": params.TargetID,
		"session_id": params.SessionID, "window_id": params.WindowID, "display_id": params.DisplayID,
	})
	observation, err := service.Observe(ctx, owner, cu.ObserveRequest{SessionID: params.SessionID, DisplayID: params.DisplayID, WindowID: params.WindowID})
	if err != nil {
		errorCode := cu.ErrorCodeActionFailed
		var coded interface{ Code() string }
		if errors.As(err, &coded) {
			errorCode = cu.PublicErrorCode(coded.Code())
		}
		computerdiag.Append(toolObserveDiagnosticPath, map[string]any{
			"layer": "go_tool", "phase": "capture_observation_result", "result": "error",
			"target_id": params.TargetID, "session_id": params.SessionID, "window_id": params.WindowID, "error_code": errorCode,
		})
		return cu.Observation{}, tools.Result{
			Content: marshal(map[string]any{
				"error_code": errorCode,
				"message":    "computer observation failed; stop without input replay",
			}),
			IsError: true,
		}
	}
	messages, err := observationImage(ctx, service, owner, params.SessionID, observation.ID, observationScreenshotGuidance)
	if err != nil {
		computerdiag.Append(toolObserveDiagnosticPath, map[string]any{
			"layer": "go_tool", "phase": "capture_observation_result", "result": "image_error",
			"target_id": params.TargetID, "session_id": params.SessionID, "window_id": observation.WindowID, "observation_id": observation.ID,
			"error_code": "observation_image_failed",
		})
		return cu.Observation{}, errorResult("observation_image_failed", "valid computer screenshot unavailable")
	}
	computerdiag.Append(toolObserveDiagnosticPath, map[string]any{
		"layer": "go_tool", "phase": "capture_observation_result", "result": "success",
		"target_id": params.TargetID, "session_id": params.SessionID, "window_id": observation.WindowID, "observation_id": observation.ID,
	})
	return observation, tools.Result{Content: marshal(map[string]any{"observation": observation}), ContextMessages: messages}
}

// Bound both compressed bytes and decoded pixels before allocating the image.
// A MIME label or PNG signature alone does not establish a valid screenshot.
func observationImage(ctx context.Context, service cu.Service, owner cu.SessionOwner, sessionID, observationID, guidance string) ([]anthropic.MessageParam, error) {
	provider, ok := service.(ObservationImageProvider)
	if !ok || strings.TrimSpace(observationID) == "" {
		return nil, errors.New("screenshot unavailable")
	}
	data, mediaType, err := provider.ObservationImage(ctx, owner, sessionID, observationID)
	if err != nil || mediaType != pngMediaType || len(data) == 0 || len(data) > maxPNGBytes {
		return nil, errors.New("invalid screenshot")
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width > maxPNGPixels/config.Height {
		return nil, errors.New("invalid screenshot dimensions")
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		return nil, errors.New("invalid screenshot content")
	}
	return []anthropic.MessageParam{{Role: "user", Content: []anthropic.ContentBlock{
		{Type: "text", Text: fmt.Sprintf("%s Image ID: %q. Image dimensions: %dx%d pixels. %s", guidance, observationID, config.Width, config.Height, screenshotCoordinateGuidance)},
		{Type: "image", Source: &anthropic.ContentSource{Type: "base64", MediaType: pngMediaType, Data: base64.StdEncoding.EncodeToString(data)}},
	}}}, nil
}

// Run and tool-use IDs are mandatory: neither model IDs nor a session/action
// fallback can distinguish invocations. Length-delimited JSON also avoids
// separator collisions. Include owner and batch to namespace runtime IDs.
func invocationActionID(tc tools.Context) (string, error) {
	if strings.TrimSpace(tc.Invocation.RunID) == "" || strings.TrimSpace(tc.Invocation.ToolUseID) == "" {
		return "", errors.New("trusted runtime invocation is required")
	}
	data, err := json.Marshal(struct {
		Owner      cu.SessionOwner
		Invocation tools.Invocation
	}{cu.SessionOwner{TenantID: tc.TenantID, UserID: tc.UserID, SessionID: tc.SessionID}, tc.Invocation})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("computer-%x", sha256.Sum256(data)), nil
}

func (t Tool) executeTurn(ctx context.Context, service cu.TurnService, owner cu.SessionOwner, params request, tc tools.Context) tools.Result {
	action, err := actionFromRequest(params, tc)
	if err != nil {
		return errorResult("invalid_context", "trusted runtime invocation is required")
	}
	result, turnErr := service.ExecuteTurn(ctx, owner, action)
	if err := result.Validate(); err != nil {
		return errorResult("invalid_turn_result", "computer turn result was invalid")
	}
	payload := map[string]any{
		"turn_result":    result,
		"receipt":        result.Receipt,
		"dispatch_state": result.DispatchState,
		"outcome":        result.Outcome,
		"error_code":     result.ErrorCode,
	}
	out := tools.Result{IsError: turnErr != nil || result.Outcome != cu.OutcomeExecuted}
	if result.LaunchReceipt != nil {
		payload["launch_receipt"] = result.LaunchReceipt
	}
	if result.ObservationState == cu.ObservationReady && result.Observation != nil {
		payload["observation"] = result.Observation
	}
	if result.Outcome == cu.OutcomeExecuted {
		if result.ScreenshotState == cu.ScreenshotReady {
			messages, imageErr := turnScreenshotMessages(result, observationScreenshotGuidance)
			if imageErr != nil {
				out.IsError = true
				payload["image_error_code"] = "observation_image_failed"
			} else {
				out.ContextMessages = messages
			}
		} else {
			out.IsError = true
			payload["image_error_code"] = "observation_image_failed"
		}
		if result.ObservationState != cu.ObservationReady || result.ScreenshotState != cu.ScreenshotReady {
			payload["message"] = "input was executed but same-turn observation or screenshot is unavailable; stop without replaying input"
		}
	} else if result.Outcome == cu.OutcomeUnknown {
		out.IsError = true
		payload["message"] = "computer action dispatch is unknown; input may already have been dispatched, do not replay it"
	} else {
		out.IsError = true
		payload["message"] = "computer action was not executed; observe again before deciding, do not replay the same action automatically"
	}
	if out.IsError && payload["error_code"] == "" {
		if result.ErrorCode != "" {
			payload["error_code"] = result.ErrorCode
		} else {
			payload["error_code"] = cu.ErrorCodeActionFailed
		}
	}
	out.Content = marshal(payload)
	return out
}

func actionFromRequest(params request, tc tools.Context) (cu.Action, error) {
	id, err := invocationActionID(tc)
	if err != nil {
		return cu.Action{}, err
	}
	action := cu.Action{
		ID: id, SessionID: params.SessionID, ObservationID: params.ObservationID, TargetID: cu.TargetID(params.TargetID), Kind: cu.ActionKind(params.Action),
		DisplayID: params.DisplayID, WindowID: params.WindowID, Text: params.Text, Key: params.Key,
		Keys: params.Keys, DeltaX: params.DeltaX, DeltaY: params.DeltaY, DurationMS: params.DurationMS, Button: params.Button,
	}
	if params.X != nil && params.Y != nil {
		action.Point = &cu.Point{X: *params.X, Y: *params.Y}
	}
	if params.StartX != nil && params.StartY != nil {
		action.StartPoint = &cu.Point{X: *params.StartX, Y: *params.StartY}
	}
	return action, nil
}

func turnScreenshotMessages(result cu.ComputerTurnResult, guidance string) ([]anthropic.MessageParam, error) {
	if result.Screenshot == nil || result.Observation == nil || result.ObservationState != cu.ObservationReady || result.ScreenshotState != cu.ScreenshotReady {
		return nil, errors.New("same-turn screenshot unavailable")
	}
	data := result.ScreenshotData
	if result.Screenshot.MediaType != pngMediaType || len(data) == 0 || len(data) > maxPNGBytes || result.Screenshot.SizeBytes != int64(len(data)) {
		return nil, errors.New("invalid same-turn screenshot")
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width > maxPNGPixels/config.Height || config.Width != result.Screenshot.Width || config.Height != result.Screenshot.Height {
		return nil, errors.New("invalid same-turn screenshot dimensions")
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		return nil, errors.New("invalid same-turn screenshot content")
	}
	hash := sha256.Sum256(data)
	if hex.EncodeToString(hash[:]) != result.Screenshot.SHA256 {
		return nil, errors.New("same-turn screenshot hash mismatch")
	}
	return []anthropic.MessageParam{{Role: "user", Content: []anthropic.ContentBlock{
		{Type: "text", Text: fmt.Sprintf("%s Observation ID: %q. Image dimensions: %dx%d pixels. %s", guidance, result.Observation.ID, config.Width, config.Height, screenshotCoordinateGuidance)},
		{Type: "image", Source: &anthropic.ContentSource{Type: "base64", MediaType: pngMediaType, Data: base64.StdEncoding.EncodeToString(data)}},
	}}}, nil
}

func (t Tool) execute(ctx context.Context, service cu.Service, owner cu.SessionOwner, params request, tc tools.Context) tools.Result {
	action, err := actionFromRequest(params, tc)
	if err != nil {
		return errorResult("invalid_context", "trusted runtime invocation is required")
	}
	receipt, executeErr := service.Execute(ctx, owner, action)
	// Errors can accompany an unknown outcome after input was dispatched. Never
	// discard that receipt or suggest the caller should replay the action.
	receipt.ErrorMessage = ""
	if receipt.ErrorCode != "" {
		receipt.ErrorCode = cu.PublicErrorCode(receipt.ErrorCode)
	}
	receipt.RedactedActionSummary = string(action.Kind)
	payload := map[string]any{"receipt": receipt}
	out := tools.Result{IsError: executeErr != nil || receipt.Outcome != cu.OutcomeExecuted}
	if out.IsError {
		payload["error_code"] = cu.ErrorCodeActionFailed
		if receipt.ErrorCode != "" {
			payload["error_code"] = receipt.ErrorCode
		}
		payload["message"] = "computer action failed; inspect receipt before any further action"
		if receipt.ErrorCode == cu.ErrorCodeSelfTarget {
			payload["message"] = "the action was not dispatched because the niuma control window was the target; observe again and launch or switch to the requested app before continuing"
		}
	}
	// In fast-path mode, a successful input immediately performs an authoritative
	// fresh Observe below. The receipt evidence image would be discarded, so do
	// not fetch it. Failed and unknown outcomes retain evidence-only handling.
	needsEvidenceImage := !tc.ComputerUseFastPath || executeErr != nil || receipt.Outcome != cu.OutcomeExecuted
	if receipt.DispatchState == cu.DispatchComplete && executeErr == nil && receipt.Outcome == cu.OutcomeExecuted && receipt.AfterObservationID == "" {
		needsEvidenceImage = false // recover only observation, never acknowledged input
	}
	if needsEvidenceImage {
		if receipt.AfterObservationID != "" {
			messages, imageErr := observationImage(ctx, service, owner, params.SessionID, receipt.AfterObservationID, actionEvidenceGuidance)
			if imageErr != nil {
				out.IsError = true
				payload["image_error_code"] = "observation_image_failed"
			} else {
				out.ContextMessages = messages
			}
		} else {
			out.IsError = true
			payload["image_error_code"] = "observation_image_failed"
		}
	}
	// A receipt's image is never authority for the next input. Compose a genuine
	// owner-bound Observe only after unambiguous success and valid visual evidence.
	// This reuses Controller/native focus, permission, cancellation and TTL gates;
	// it never retries input or silently promotes an evidence ID to an observation.
	if !out.IsError {
		if ctx.Err() != nil {
			out.IsError = true
			payload["error_code"] = "post_action_observation_failed"
		} else {
			observation, fresh := t.captureObservation(ctx, service, owner, params)
			if fresh.IsError {
				out.IsError = true
				payload["error_code"] = "post_action_observation_failed"
			} else {
				payload["observation"] = observation
				out.ContextMessages = fresh.ContextMessages
			}
		}
		if out.IsError {
			payload["message"] = "input was executed but fresh observation is unavailable; stop without replaying input"
		}
	}
	out.Content = marshal(payload)
	return out
}

func (t Tool) control(ctx context.Context, owner cu.SessionOwner, params request, fn func() error) tools.Result {
	if err := fn(); err != nil {
		return errorResult("control_failed", "computer session control failed")
	}
	payload := map[string]any{"session_id": params.SessionID, "action": params.Action, "status": "ok"}
	if cu.ActionKind(params.Action) == cu.ActionStop {
		now := time.Now()
		payload["receipt"] = cu.ActionReceipt{
			ActionID: "control-stop-" + params.SessionID, SessionID: params.SessionID,
			Outcome: cu.OutcomeExecuted, DispatchState: cu.DispatchComplete,
			Verification: cu.VerificationNotChecked, RedactedActionSummary: "stop", CompletedAt: now,
		}
	}
	return tools.Result{Content: marshal(payload)}
}

func decode(input json.RawMessage, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return errors.New("unexpected trailing JSON")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func marshal(value any) string {
	payload, err := json.Marshal(value)
	if err != nil {
		return `{"error_code":"internal_error"}`
	}
	return string(payload)
}

func errorResult(code, message string) tools.Result {
	return tools.Result{Content: marshal(map[string]string{"error_code": code, "message": message}), IsError: true}
}
