package computerbridge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"image"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func testPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 2, 3))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
func testImage(t *testing.T) ImageResponse {
	return ImageResponse{SessionID: "host-session", ObservationID: "observation-a", MediaType: pngMediaType, ImageData: base64.StdEncoding.EncodeToString(testPNG(t))}
}
func TestObservationImageProtocol(t *testing.T) {
	expected := testPNG(t)
	c := newUnixClient(t, 0, func(w http.ResponseWriter, r *http.Request) {
		req := readRequest(t, r)
		if req.Op != OpImage || req.SessionID != "host-session" || req.ObservationID != "observation-a" || req.Owner != testOwner() {
			t.Errorf("image binding lost: %+v", req)
		}
		writeData(t, w, testImage(t))
	})
	data, media, err := c.ObservationImage(context.Background(), testOwner(), "host-session", "observation-a")
	if err != nil || media != pngMediaType || !bytes.Equal(data, expected) {
		t.Fatalf("image decode: bytes=%d media=%q err=%v", len(data), media, err)
	}
}
func TestObservationImageInvalidFields(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*ImageResponse)
		want   error
	}{
		{"wrong session", func(i *ImageResponse) { i.SessionID = "other" }, ErrInvalidResponse},
		{"wrong observation", func(i *ImageResponse) { i.ObservationID = "other" }, ErrInvalidResponse},
		{"empty", func(i *ImageResponse) { i.ImageData = "" }, ErrInvalidImage},
		{"MIME", func(i *ImageResponse) { i.MediaType = "image/jpeg" }, ErrInvalidImage},
		{"base64", func(i *ImageResponse) { i.ImageData = "secret-invalid-image" }, ErrInvalidImage},
		{"noncanonical", func(i *ImageResponse) { i.ImageData = "Zh==" }, ErrInvalidImage},
		{"newline", func(i *ImageResponse) { i.ImageData += "\n" }, ErrInvalidImage},
		{"data URL", func(i *ImageResponse) { i.ImageData = "data:image/png;base64," + i.ImageData }, ErrInvalidImage},
		{"not PNG", func(i *ImageResponse) {
			i.ImageData = base64.StdEncoding.EncodeToString([]byte("secret-invalid-image"))
		}, ErrInvalidImage},
		{"truncated PNG", func(i *ImageResponse) {
			p := testPNG(t)
			i.ImageData = base64.StdEncoding.EncodeToString(p[:len(p)-15])
		}, ErrInvalidImage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := testImage(t)
			tc.mutate(&value)
			c := newUnixClient(t, 0, func(w http.ResponseWriter, r *http.Request) { writeData(t, w, value) })
			data, media, err := c.ObservationImage(context.Background(), testOwner(), "host-session", "observation-a")
			if data != nil || media != "" || !errors.Is(err, tc.want) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("invalid image: bytes=%d media=%q err=%v", len(data), media, err)
			}
		})
	}
	t.Run("wrong image field", func(t *testing.T) {
		c := newUnixClient(t, 0, func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"session_id": "host-session", "observation_id": "observation-a", "media_type": pngMediaType, "data": testImage(t).ImageData}})
		})
		if _, _, err := c.ObservationImage(context.Background(), testOwner(), "host-session", "observation-a"); !errors.Is(err, ErrInvalidResponse) {
			t.Fatalf("wrong image field accepted: %v", err)
		}
	})
}
func TestImageCompressedBounds(t *testing.T) {
	for _, n := range []int{MaxImageBytes, MaxImageBytes + 1} {
		t.Run(stringSize(n), func(t *testing.T) {
			// Appending an ancillary chunk before IEND makes a genuinely valid PNG at
			// the byte limit, rather than relying on the decoder ignoring trailing data.
			p := testPNG(t)
			chunk := make([]byte, n-len(p))
			binary.BigEndian.PutUint32(chunk[:4], uint32(len(chunk)-12))
			copy(chunk[4:8], "teSt")
			binary.BigEndian.PutUint32(chunk[len(chunk)-4:], crc32.ChecksumIEEE(chunk[4:len(chunk)-4]))
			bounded := append(append(append([]byte{}, p[:len(p)-12]...), chunk...), p[len(p)-12:]...)
			value := testImage(t)
			value.ImageData = base64.StdEncoding.EncodeToString(bounded)
			data, media, err := decodeImage(value)
			if n == MaxImageBytes {
				if err != nil || media != pngMediaType || len(data) != n {
					t.Fatalf("exact compressed limit: bytes=%d err=%v", len(data), err)
				}
			} else if data != nil || media != "" || !errors.Is(err, ErrInvalidImage) {
				t.Fatalf("oversized compressed image: %v", err)
			}
		})
	}
}
func stringSize(n int) string {
	if n == MaxImageBytes {
		return "exact"
	}
	return "over"
}
func TestImagePixelBound(t *testing.T) {
	p := testPNG(t)
	// IHDR contains width/height in the first eight data bytes. Correct its CRC
	// so DecodeConfig succeeds, exercising the pixel guard before full decoding.
	binary.BigEndian.PutUint32(p[16:20], MaxImagePixels)
	binary.BigEndian.PutUint32(p[20:24], 2)
	binary.BigEndian.PutUint32(p[29:33], crc32.ChecksumIEEE(p[12:29]))
	value := testImage(t)
	value.ImageData = base64.StdEncoding.EncodeToString(p)
	if _, _, err := decodeImage(value); !errors.Is(err, ErrInvalidImage) {
		t.Fatalf("pixel bound: %v", err)
	}
}

func TestExecuteBodyReadTimeoutIsUnknown(t *testing.T) {
	c := newUnixClient(t, 25*time.Millisecond, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":`)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	receipt, err := c.Execute(context.Background(), testOwner(), testAction())
	assertUnknown(t, receipt, err)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("body timeout: %v", err)
	}
}
