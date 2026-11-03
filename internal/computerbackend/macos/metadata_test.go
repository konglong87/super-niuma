package macos

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

const metadataModePrefix = "metadata-"

func captureGeometryFixture() map[string]any {
	return map[string]any{"display_id": "1", "origin": "top_left", "unit": "pixels", "width": 2, "height": 2, "scale_factor": 2,
		"bounds": map[string]any{"x": -100, "y": -20, "width": 1, "height": 1}}
}

func targetMetadataFixture() map[string]any {
	return map[string]any{"id": "window-1", "owner_pid": 42, "frame": map[string]any{"x": -100, "y": -20, "width": 1, "height": 1}}
}

type metadataMutation func(map[string]any)

// The same negative cases drive both real framed helper IPC and table assertions.
func geometryMutations() map[string]metadataMutation {
	return map[string]metadataMutation{
		"missing":               func(r map[string]any) { delete(r, "coordinate_space") },
		"null":                  func(r map[string]any) { r["coordinate_space"] = nil },
		"wrong-type":            func(r map[string]any) { r["coordinate_space"] = "invalid" },
		"display":               geometryField("display_id", "other"),
		"origin":                geometryField("origin", "bottom_left"),
		"unit":                  geometryField("unit", "points"),
		"width":                 geometryField("width", 3),
		"fractional-width":      geometryField("width", 2.5),
		"height":                geometryField("height", 3),
		"scale":                 geometryField("scale_factor", 1),
		"null-scale":            geometryField("scale_factor", nil),
		"bounds-missing":        func(r map[string]any) { delete(r["coordinate_space"].(map[string]any), "bounds") },
		"bounds-null":           geometryField("bounds", nil),
		"bounds-type":           geometryField("bounds", []any{1, 2}),
		"origin-missing":        func(r map[string]any) { delete(r["coordinate_space"].(map[string]any)["bounds"].(map[string]any), "x") },
		"origin-null":           boundsField("y", nil),
		"origin-type":           boundsField("x", "zero"),
		"bounds-zero":           boundsField("width", 0),
		"bounds-negative":       boundsField("height", -1),
		"bounds-x-scale":        boundsField("width", 2),
		"bounds-y-scale":        boundsField("height", 2),
		"bounds-overflow-ratio": boundsField("width", math.SmallestNonzeroFloat64),
	}
}
func geometryField(key string, value any) metadataMutation {
	return func(r map[string]any) { r["coordinate_space"].(map[string]any)[key] = value }
}
func boundsField(key string, value any) metadataMutation {
	return func(r map[string]any) { r["coordinate_space"].(map[string]any)["bounds"].(map[string]any)[key] = value }
}
func targetMutations() map[string]metadataMutation {
	return map[string]metadataMutation{
		"window-missing":  func(r map[string]any) { delete(r, "window_id") },
		"window-empty":    func(r map[string]any) { r["window_id"] = "" },
		"window-mismatch": func(r map[string]any) { r["window_id"] = "window-2" },
		"window-type":     func(r map[string]any) { r["window_id"] = 1 },
		"window-null":     func(r map[string]any) { r["window_id"] = nil },
		"target-missing":  func(r map[string]any) { delete(r, "target_window") },
		"target-null":     func(r map[string]any) { r["target_window"] = nil },
		"target-type":     func(r map[string]any) { r["target_window"] = "invalid" },
		"target-id":       func(r map[string]any) { r["target_window"].(map[string]any)["id"] = "window-2" },
		"frame-missing":   func(r map[string]any) { delete(r["target_window"].(map[string]any), "frame") },
		"frame-null":      func(r map[string]any) { r["target_window"].(map[string]any)["frame"] = nil },
		// Origin (x/y) drift is accepted: a window can shift position between
		// the target_window lookup and capture during launch animation. Only
		// size mismatches are rejected.
		"frame-mismatch-width":  targetFrameField("width", 2),
		"frame-mismatch-height": targetFrameField("height", 2),
		"frame-zero":            targetFrameField("width", 0),
		"frame-negative":        targetFrameField("height", -1),
		"frame-origin-null":     targetFrameField("x", nil),
		"frame-origin-missing":  func(r map[string]any) { delete(r["target_window"].(map[string]any)["frame"].(map[string]any), "y") },
	}
}
func targetFrameField(key string, value any) metadataMutation {
	return func(r map[string]any) { r["target_window"].(map[string]any)["frame"].(map[string]any)[key] = value }
}
func mutateCaptureFixture(mode string, result map[string]any) {
	if !strings.HasPrefix(mode, metadataModePrefix) {
		return
	}
	name := strings.TrimPrefix(mode, metadataModePrefix)
	if mutate, ok := geometryMutations()[name]; ok {
		mutate(result)
		return
	}
	result["window_id"] = "window-1"
	result["target_window"] = targetMetadataFixture()
	if mutate, ok := targetMutations()[name]; ok {
		mutate(result)
	}
}

func TestObserveRejectsInvalidCaptureMetadata(t *testing.T) {
	for _, group := range []struct {
		cases  map[string]metadataMutation
		window string
	}{
		{geometryMutations(), ""}, {targetMutations(), "window-1"},
	} {
		for name := range group.cases {
			t.Run(name, func(t *testing.T) {
				b := newTestBackend(t, metadataModePrefix+name, time.Second, "")
				obs, err := b.Observe(context.Background(), cu.ObserveRequest{SessionID: "session-1", WindowID: group.window})
				if err == nil || obs.ID != "" {
					t.Fatalf("invalid metadata accepted: %+v, %v", obs, err)
				}
				b.mu.Lock()
				defer b.mu.Unlock()
				if b.observation.ID != "" || len(b.images) != 0 {
					t.Fatal("invalid metadata published actionable observation/image")
				}
			})
		}
	}
}

func TestObserveRejectsUnexpectedWindowInDisplayCapture(t *testing.T) {
	b := newTestBackend(t, metadataModePrefix+"unexpected-window", time.Second, "")
	if _, err := b.Observe(context.Background(), cu.ObserveRequest{SessionID: "session-1"}); err == nil {
		t.Fatal("display capture accepted a window target")
	}
}

func TestObserveUsesCaptureGeometryAndClearsCachedTarget(t *testing.T) {
	b := newTestBackend(t, "", time.Second, "")
	stale := &cu.WindowFrame{X: 900, Y: 800, Width: 300, Height: 200}
	b.mu.Lock()
	b.capabilities.CoordinateSpace.Bounds = stale
	b.capabilities.Displays = []cu.CoordinateSpace{{DisplayID: "1", Bounds: stale}}
	b.capabilities.TargetWindow = cu.WindowRef{ID: "old-window", Frame: stale}
	b.mu.Unlock()
	obs := observeTest(t, b)
	if *obs.Capabilities.CoordinateSpace.Bounds != (cu.WindowFrame{X: -100, Y: -20, Width: 1, Height: 1}) {
		t.Fatal("capture inherited cached bounds")
	}
	if obs.WindowID != "" || obs.Capabilities.TargetWindow != (cu.WindowRef{}) {
		t.Fatal("old target retained")
	}
	obs.Capabilities.CoordinateSpace.Bounds.X = 999
	obs.Capabilities.Displays[0].Bounds.X = 999
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.observation.Capabilities.CoordinateSpace.Bounds.X != -100 || b.capabilities.CoordinateSpace.Bounds.X != -100 || b.capabilities.Displays[0].Bounds.X != 900 {
		t.Fatal("returned metadata aliases backend snapshots")
	}
}

func TestObserveAcceptsFreshDisplayNotInCachedInventory(t *testing.T) {
	b := newTestBackend(t, "", time.Second, "")
	b.mu.Lock()
	b.capabilities.CoordinateSpace = cu.CoordinateSpace{DisplayID: "old", Bounds: &cu.WindowFrame{X: 900}}
	b.capabilities.Displays = []cu.CoordinateSpace{{DisplayID: "old", Bounds: &cu.WindowFrame{X: 800}}}
	b.mu.Unlock()
	obs := observeTest(t, b)
	if obs.Capabilities.CoordinateSpace.DisplayID != "1" || obs.Capabilities.CoordinateSpace.Bounds.X != -100 {
		t.Fatal("new display inherited old geometry")
	}
}

func TestCaptureFrameRejectsNonFiniteNumbers(t *testing.T) {
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		for _, field := range []string{"x", "y", "width", "height"} {
			frame := map[string]any{"x": 0, "y": 0, "width": 1, "height": 1}
			frame[field] = value
			if _, ok := decodeFrame(frame); ok {
				t.Fatalf("accepted %s=%v", field, value)
			}
		}
	}
}
