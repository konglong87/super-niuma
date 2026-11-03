package macos

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image/png"
	"math"
)

const (
	pngMediaType      = "image/png"
	maxImageDimension = 16384
	maxImagePixels    = 32 * 1024 * 1024
	maxPNGBytes       = 5 * 1024 * 1024
)

func integer(value any, min, max int) (int, bool) {
	n, ok := value.(float64)
	if !ok || math.IsNaN(n) || math.IsInf(n, 0) || n != math.Trunc(n) || n < float64(min) || n > float64(max) {
		return 0, false
	}
	return int(n), true
}

func decodeImage(payload map[string]any) ([]byte, string, int, int, error) {
	invalid := errors.New("helper returned invalid PNG screenshot")
	encoded, ok := payload["data"].(string)
	if !ok || len(encoded) == 0 || len(encoded) > base64.StdEncoding.EncodedLen(maxPNGBytes) || payload["media_type"] != pngMediaType {
		return nil, "", 0, 0, invalid
	}
	width, okW := integer(payload["width"], 1, maxImageDimension)
	height, okH := integer(payload["height"], 1, maxImageDimension)
	if !okW || !okH || width*height > maxImagePixels {
		return nil, "", 0, 0, invalid
	}
	data, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(data) > maxPNGBytes {
		return nil, "", 0, 0, invalid
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width != width || cfg.Height != height {
		return nil, "", 0, 0, invalid
	}
	// DecodeConfig alone accepts a truncated/corrupt IDAT stream.
	if _, err = png.Decode(bytes.NewReader(data)); err != nil {
		return nil, "", 0, 0, invalid
	}
	return data, pngMediaType, width, height, nil
}
