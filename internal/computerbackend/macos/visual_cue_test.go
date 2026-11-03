package macos

import (
	"testing"

	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

func TestDecodeNativeVisualCuePointsRejectsMalformedValues(t *testing.T) {
	point, ok := decodeActualPoint(map[string]any{"x": float64(12), "y": float64(34)})
	if !ok || point != (cu.Point{X: 12, Y: 34}) {
		t.Fatalf("actual point = %+v ok=%v", point, ok)
	}
	global, ok := decodeGlobalPoint(map[string]any{"x": -12.5, "y": 34.25})
	if !ok || global != (cu.ScreenPoint{X: -12.5, Y: 34.25}) {
		t.Fatalf("global point = %+v ok=%v", global, ok)
	}
	for _, raw := range []map[string]any{
		{"x": float64(1.5), "y": float64(2)},
		{"x": float64(1), "y": "2"},
		{"x": float64(1), "y": float64(2), "z": float64(3)},
	} {
		if _, ok := decodeActualPoint(raw); ok {
			t.Fatalf("malformed actual point accepted: %+v", raw)
		}
	}
}
