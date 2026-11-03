package macos

import (
	"encoding/base64"
	"math"
	"testing"
)

func TestDecodeImageValidatesPNGAndBounds(t *testing.T) {
	if _, _, _, _, err := decodeImage(imagePayload()); err != nil {
		t.Fatal(err)
	}
	tests := map[string]func(map[string]any){
		"fake PNG": func(p map[string]any) { p["data"] = base64.StdEncoding.EncodeToString([]byte("png")) },
		"truncated": func(p map[string]any) {
			png := testPNG()
			p["data"] = base64.StdEncoding.EncodeToString(png[:len(png)-8])
		},
		"bad CRC": func(p map[string]any) {
			png := testPNG()
			png[len(png)-16] ^= 1
			p["data"] = base64.StdEncoding.EncodeToString(png)
		},
		"wrong mime":   func(p map[string]any) { p["media_type"] = "image/jpeg" },
		"missing mime": func(p map[string]any) { delete(p, "media_type") },
		"missing data": func(p map[string]any) { delete(p, "data") },
		"mismatch":     func(p map[string]any) { p["width"] = float64(3) },
		"zero":         func(p map[string]any) { p["width"] = float64(0) },
		"negative":     func(p map[string]any) { p["height"] = float64(-1) },
		"fractional":   func(p map[string]any) { p["width"] = 1.5 },
		"overflow":     func(p map[string]any) { p["width"] = 1e99 },
		"NaN":          func(p map[string]any) { p["width"] = math.NaN() },
		"infinity":     func(p map[string]any) { p["height"] = math.Inf(1) },
		"pixel budget": func(p map[string]any) {
			p["width"] = float64(maxImageDimension)
			p["height"] = float64(maxImageDimension)
		},
		"invalid base64": func(p map[string]any) { p["data"] = "@@@=" },
	}
	for name, edit := range tests {
		t.Run(name, func(t *testing.T) {
			p := imagePayload()
			edit(p)
			if _, _, _, _, err := decodeImage(p); err == nil {
				t.Fatal("unsafe screenshot accepted")
			}
		})
	}
}
