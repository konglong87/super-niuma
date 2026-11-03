// Package computeracceptance provides an isolated, in-memory target for real
// computer-input acceptance. It neither generates input nor controls a desktop.
package computeracceptance

import "time"

const (
	MaxEvents       = 512
	MaxBatchEvents  = 8
	MaxRequestBytes = 128 << 10
	MaxTextBytes    = 4096
	maxLabelBytes   = 128
	targetCount     = 8
	maxCoordinate   = 1e7
)

type EventType string

const (
	Click             EventType = "click"
	DoubleClick       EventType = "dblclick"
	ContextMenu       EventType = "contextmenu"
	MouseMove         EventType = "mousemove"
	MouseDown         EventType = "mousedown"
	MouseUp           EventType = "mouseup"
	Wheel             EventType = "wheel"
	Scroll            EventType = "scroll"
	KeyDown           EventType = "keydown"
	KeyUp             EventType = "keyup"
	BeforeInput       EventType = "beforeinput"
	Input             EventType = "input"
	CompositionStart  EventType = "compositionstart"
	CompositionUpdate EventType = "compositionupdate"
	CompositionEnd    EventType = "compositionend"
	Focus             EventType = "focus"
	Blur              EventType = "blur"
)

type TargetID string

const (
	ClickTarget       TargetID = "click-target"
	DoubleClickTarget TargetID = "double-click-target"
	ContextMenuTarget TargetID = "context-menu-target"
	MouseMoveTarget   TargetID = "mouse-move-target"
	ScrollTarget      TargetID = "scroll-target"
	TextTarget        TargetID = "text-target"
	DragSourceTarget  TargetID = "drag-source"
	DragDropTarget    TargetID = "drag-drop-target"
)

// Point and Rect use CSS pixels, not screenshot/device pixels.
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type Rect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type Modifiers struct {
	Alt   bool `json:"alt"`
	Ctrl  bool `json:"ctrl"`
	Meta  bool `json:"meta"`
	Shift bool `json:"shift"`
}

// DOMEvent is browser-reported evidence, not cryptographically attested input.
// IsTrusted preserves event.isTrusted; untrusted events are never upgraded.
// Position indicates mouse/wheel coordinate members exist, not that the event
// came from a pointing device (keyboard-activated clicks may contain zeros).
type DOMEvent struct {
	Type        EventType `json:"type"`
	Target      TargetID  `json:"target"`
	IsTrusted   bool      `json:"isTrusted"`
	TimeStamp   float64   `json:"timeStamp"`
	Position    bool      `json:"position"`
	Client      Point     `json:"client"`
	Screen      Point     `json:"screen"`
	Button      int       `json:"button"`
	Buttons     int       `json:"buttons"`
	Detail      int       `json:"detail"`
	Key         string    `json:"key"`
	Code        string    `json:"code"`
	Modifiers   Modifiers `json:"modifiers"`
	Repeat      bool      `json:"repeat"`
	IsComposing bool      `json:"isComposing"`
	InputType   string    `json:"inputType"`
	Data        string    `json:"data"`
	Value       string    `json:"value"`
	Truncated   bool      `json:"truncated"`
	ScrollTop   float64   `json:"scrollTop"`
	ScrollLeft  float64   `json:"scrollLeft"`
	DeltaX      float64   `json:"deltaX"`
	DeltaY      float64   `json:"deltaY"`
	DeltaMode   int       `json:"deltaMode"`
}

// Event adds server-owned ordering and receipt time to a DOM event.
type Event struct {
	DOMEvent
	Sequence   uint64    `json:"sequence"`
	ReceivedAt time.Time `json:"receivedAt"`
}

type VisualViewport struct {
	OffsetLeft float64 `json:"offsetLeft"`
	OffsetTop  float64 `json:"offsetTop"`
	PageLeft   float64 `json:"pageLeft"`
	PageTop    float64 `json:"pageTop"`
	Width      float64 `json:"width"`
	Height     float64 `json:"height"`
	Scale      float64 `json:"scale"`
}

// Calibration is an actual trusted mouse event's client/screen coordinate pair.
// It is cleared by the page when window/viewport geometry changes. Screen
// target estimates are clientCenter + (Screen - Client); they are NOT measured
// target screen positions and NOT a universal screenshot-pixel conversion.
type Calibration struct {
	Client    Point   `json:"client"`
	Screen    Point   `json:"screen"`
	TimeStamp float64 `json:"timeStamp"`
}

type TargetGeometry struct {
	ID     TargetID `json:"id"`
	Rect   Rect     `json:"rect"`
	Center Point    `json:"center"`
}

type Geometry struct {
	WindowScreen               Point            `json:"windowScreen"`
	InnerWidth                 float64          `json:"innerWidth"`
	InnerHeight                float64          `json:"innerHeight"`
	OuterWidth                 float64          `json:"outerWidth"`
	OuterHeight                float64          `json:"outerHeight"`
	DevicePixelRatio           float64          `json:"devicePixelRatio"`
	PageScroll                 Point            `json:"pageScroll"`
	ScreenWidth                float64          `json:"screenWidth"`
	ScreenHeight               float64          `json:"screenHeight"`
	AvailableScreen            Rect             `json:"availableScreen"`
	AvailableScreenOriginKnown bool             `json:"availableScreenOriginKnown"`
	VisualViewport             *VisualViewport  `json:"visualViewport"`
	Calibration                *Calibration     `json:"calibration"`
	Targets                    []TargetGeometry `json:"targets"`
}

type PageState struct {
	InputValue     string  `json:"inputValue"`
	InputTruncated bool    `json:"inputTruncated"`
	SelectionStart int     `json:"selectionStart"` // UTF-16 offsets, like the DOM.
	SelectionEnd   int     `json:"selectionEnd"`
	ScrollTop      float64 `json:"scrollTop"`
	ScrollLeft     float64 `json:"scrollLeft"`
	DragCompleted  bool    `json:"dragCompleted"`
}

// Report is the POST /events wire format. Geometry and State are required;
// an empty event batch is a geometry/state heartbeat, not an input action.
type Report struct {
	Events        []DOMEvent `json:"events"`
	Geometry      *Geometry  `json:"geometry"`
	State         *PageState `json:"state"`
	ClientDropped uint64     `json:"clientDropped"`
}

// Snapshot is a detached, concurrency-safe copy. Dropped counts overwritten
// server events; ClientDropped counts events the bounded browser queue dropped.
// Reports/UpdatedAt allow a harness to wait for readiness/fresh geometry.
type Snapshot struct {
	Events        []Event   `json:"events"`
	Geometry      *Geometry `json:"geometry"`
	State         PageState `json:"state"`
	TotalEvents   uint64    `json:"totalEvents"`
	Dropped       uint64    `json:"dropped"`
	ClientDropped uint64    `json:"clientDropped"`
	Reports       uint64    `json:"reports"`
	UpdatedAt     time.Time `json:"updatedAt"`
}
