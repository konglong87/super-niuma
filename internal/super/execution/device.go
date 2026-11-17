package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"

	"github.com/konglong87/super-niuma/internal/permissions"
	"github.com/konglong87/super-niuma/internal/tools"
)

// Phone tools. They exist only when a Request carries a Device.
const (
	ToolScreen = "super_screen"
	ToolTap    = "super_tap"
	ToolType   = "super_type"
	ToolSwipe  = "super_swipe"
	ToolKey    = "super_key"
	ToolLaunch = "super_launch"

	// MaxDeviceTurns bounds model requests for an attempt that operates a
	// phone; each screen step usually costs one request.
	MaxDeviceTurns = 40
	// MaxDeviceResultBytes bounds what one device action may return to the
	// model. The device keeps its screen text within it.
	MaxDeviceResultBytes = 24 << 10
	maxDeviceTextBytes   = 4 << 10
	maxDeviceIntentBytes = 400
)

// DeviceAction is one action the model asked for, already validated for
// shape. Ref names an element of the latest screen the device returned.
type DeviceAction struct {
	Kind      string `json:"kind"`
	Ref       int    `json:"ref,omitempty"`
	Text      string `json:"text,omitempty"`
	Submit    bool   `json:"submit,omitempty"`
	Direction string `json:"direction,omitempty"`
	Key       string `json:"key,omitempty"`
	App       string `json:"app,omitempty"`
	Intent    string `json:"intent,omitempty"`
}

// Device is the phone an attempt may operate. Execution checks authority
// before every action and routes it; it never reads the screen itself. The
// implementation owns app scope, risk policy and the person's approvals,
// and returns the screen after the action settles.
type Device interface {
	Act(ctx context.Context, action DeviceAction) (string, error)
}

// DeviceRefusal is a refused action the model may recover from: a rule, an
// app outside the task's scope, a stale element, or the person saying no.
// Reason is shown to the model as the tool result.
type DeviceRefusal struct{ Reason string }

func (r DeviceRefusal) Error() string { return "device refused: " + r.Reason }

// ErrDeviceStopped means the person stopped the task; the attempt ends as
// revoked authority and no further action runs.
var ErrDeviceStopped = errors.New("device stopped by the person")

type deviceToolSpec struct {
	name, kind, description, schema string
}

var deviceTools = []deviceToolSpec{
	{ToolScreen, "screen", "Read the current screen: the foreground app and its elements as numbered lines `[ref] kind \"label\" @x,y flags`. Use the refs with the other phone tools.",
		`{"type":"object","properties":{},"additionalProperties":false}`},
	{ToolTap, "tap", "Tap the element with this ref on the latest screen. Returns the screen after it settles.",
		`{"type":"object","properties":{"ref":{"type":"integer","minimum":0},"intent":{"type":"string","description":"One sentence on why, shown to the person if approval is needed."}},"required":["ref","intent"],"additionalProperties":false}`},
	{ToolType, "type", "Replace the text of the editable element with this ref. submit presses the keyboard's action key afterwards.",
		`{"type":"object","properties":{"ref":{"type":"integer","minimum":0},"text":{"type":"string"},"submit":{"type":"boolean"},"intent":{"type":"string"}},"required":["ref","text","intent"],"additionalProperties":false}`},
	{ToolSwipe, "swipe", "Scroll the scrollable element with this ref, or the screen when ref is omitted, in a direction.",
		`{"type":"object","properties":{"ref":{"type":"integer","minimum":0},"direction":{"type":"string","enum":["up","down","left","right"]},"intent":{"type":"string"}},"required":["direction","intent"],"additionalProperties":false}`},
	{ToolKey, "key", "Press a system key.",
		`{"type":"object","properties":{"key":{"type":"string","enum":["back","home","enter"]},"intent":{"type":"string"}},"required":["key","intent"],"additionalProperties":false}`},
	{ToolLaunch, "launch", "Open an app by its package name. Only the task's apps may be opened.",
		`{"type":"object","properties":{"app":{"type":"string"},"intent":{"type":"string"}},"required":["app","intent"],"additionalProperties":false}`},
}

func deviceTool(name string) bool {
	for _, spec := range deviceTools {
		if spec.name == name {
			return true
		}
	}
	return false
}

// attemptTool reports whether name is a tool an attempt may ever register.
func attemptTool(name string) bool { return name == ToolRead || name == ToolWrite || deviceTool(name) }

type phoneTool struct {
	spec   deviceToolSpec
	device Device
	state  *attemptState
}

func (t *phoneTool) Name() string                 { return t.spec.name }
func (t *phoneTool) Description() string          { return t.spec.description }
func (t *phoneTool) InputSchema() json.RawMessage { return json.RawMessage(t.spec.schema) }
func (t *phoneTool) SkipToolResultBudget() bool   { return true }

// Run validates the arguments, checks authority, and hands the action to the
// device. Malformed arguments and refusals come back to the model as errors
// without ending the attempt; a stop or a device failure ends it.
func (t *phoneTool) Run(ctx context.Context, raw json.RawMessage, _ tools.Context) tools.Result {
	if err := t.state.authorize(ctx); err != nil {
		t.state.log(actionToolEnd, errorCode(err))
		return tools.Result{Content: "super tool rejected: " + errorCode(err), IsError: true}
	}
	t.state.log(actionToolStart, codeOK)
	action, ok := decodeDeviceAction(t.spec.kind, raw)
	if !ok {
		t.state.log(actionToolEnd, codeInvalid)
		return tools.Result{Content: "invalid arguments for " + t.spec.name + "; follow its input schema", IsError: true}
	}
	content, err := t.device.Act(ctx, action)
	var refusal DeviceRefusal
	switch {
	case err == nil:
	case errors.As(err, &refusal):
		t.state.log(actionToolEnd, codeAuthority)
		return tools.Result{Content: refusal.Reason, IsError: true}
	case errors.Is(err, ErrDeviceStopped):
		t.state.fail(ErrAuthority)
		t.state.log(actionToolEnd, codeAuthority)
		return tools.Result{Content: "the person stopped the task", IsError: true}
	default:
		t.state.fail(safeError(ctx, ErrFailed))
		t.state.log(actionToolEnd, codeFailed)
		return tools.Result{Content: "device action failed", IsError: true}
	}
	if len(content) > MaxDeviceResultBytes || !utf8.ValidString(content) {
		t.state.fail(ErrFailed)
		t.state.log(actionToolEnd, codeFailed)
		return tools.Result{Content: "device result too large or invalid", IsError: true}
	}
	t.state.log(actionToolEnd, codeOK)
	return tools.Result{Content: content}
}

func decodeDeviceAction(kind string, raw json.RawMessage) (DeviceAction, bool) {
	var in struct {
		Ref       *int   `json:"ref"`
		Text      string `json:"text"`
		Submit    bool   `json:"submit"`
		Direction string `json:"direction"`
		Key       string `json:"key"`
		App       string `json:"app"`
		Intent    string `json:"intent"`
	}
	if len(raw) > maxResponseBytes || !utf8.Valid(raw) {
		return DeviceAction{}, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&in) != nil || decoder.Decode(new(any)) != io.EOF {
		return DeviceAction{}, false
	}
	if len(in.Text) > maxDeviceTextBytes || len(in.Intent) > maxDeviceIntentBytes || len(in.App) > maxPathBytes {
		return DeviceAction{}, false
	}
	a := DeviceAction{Kind: kind, Text: in.Text, Submit: in.Submit, Direction: in.Direction, Key: in.Key, App: in.App, Intent: in.Intent}
	if in.Ref != nil {
		if *in.Ref < 0 {
			return DeviceAction{}, false
		}
		a.Ref = *in.Ref
	}
	switch kind {
	case "screen":
		return a, in.Ref == nil && in.Text == "" && in.Direction == "" && in.Key == "" && in.App == ""
	case "tap":
		return a, in.Ref != nil
	case "type":
		return a, in.Ref != nil
	case "swipe":
		if in.Ref == nil {
			a.Ref = -1
		}
		return a, in.Direction == "up" || in.Direction == "down" || in.Direction == "left" || in.Direction == "right"
	case "key":
		return a, in.Key == "back" || in.Key == "home" || in.Key == "enter"
	case "launch":
		return a, in.App != ""
	}
	return DeviceAction{}, false
}

func registerDeviceTools(registry *tools.Registry, device Device, state *attemptState) {
	if device == nil {
		return
	}
	for _, spec := range deviceTools {
		tool := &phoneTool{spec: spec, device: device, state: state}
		registry.Register(tools.Guard(tool, permissions.Policy{Allow: []string{tool.Name()}, DefaultMode: permissions.ModeDeny}))
	}
}
