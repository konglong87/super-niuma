package computerbridge

import (
	"bytes"
	"context"
	"encoding/base64"
	"image/png"
	"strings"

	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

const pngMediaType = "image/png"

func (c *Client) ObservationImage(ctx context.Context, owner cu.SessionOwner, sessionID, observationID string) ([]byte, string, error) {
	var image ImageResponse
	if err := c.data(ctx, Request{Op: OpImage, Owner: owner, SessionID: sessionID, ObservationID: observationID}, &image); err != nil {
		return nil, "", err
	}
	if image.SessionID != sessionID || image.ObservationID != observationID {
		return nil, "", ErrInvalidResponse
	}
	return decodeImage(image)
}

// Validate compressed size and pixel count before a full PNG decode. No raw
// image bytes, remote field values, or codec errors are logged or returned.
func decodeImage(image ImageResponse) ([]byte, string, error) {
	encoded := image.ImageData
	if image.MediaType != pngMediaType || len(encoded) == 0 || len(encoded) > base64.StdEncoding.EncodedLen(MaxImageBytes) || strings.ContainsAny(encoded, "\r\n") {
		return nil, "", ErrInvalidImage
	}
	// Exact decoded length, including padding, before allocating decoded bytes.
	size := base64.StdEncoding.DecodedLen(len(encoded))
	if strings.HasSuffix(encoded, "=") {
		size--
	}
	if strings.HasSuffix(encoded, "==") {
		size--
	}
	if size <= 0 || size > MaxImageBytes {
		return nil, "", ErrInvalidImage
	}
	data, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(data) > MaxImageBytes {
		return nil, "", ErrInvalidImage
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width > MaxImagePixels/config.Height {
		return nil, "", ErrInvalidImage
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		return nil, "", ErrInvalidImage
	}
	return data, pngMediaType, nil
}
