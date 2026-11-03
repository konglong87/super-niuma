// Package native contains the platform-neutral wire protocol used by native
// Computer Use helpers. It deliberately knows nothing about macOS APIs.
package native

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	// ProtocolVersion is the first protocol version shared by the Go backend
	// and native helpers.
	ProtocolVersion = "computer-use.v1"

	// DefaultMaxFrameSize bounds the encoded JSON body, excluding the four-byte
	// length prefix. A caller can choose a smaller limit for a tighter helper
	// deployment budget.
	DefaultMaxFrameSize uint32 = 1 << 20
	framePrefixSize            = 4
)

var (
	ErrFrameTooLarge      = errors.New("native IPC frame exceeds maximum size")
	ErrMalformedFrame     = errors.New("native IPC frame is malformed")
	ErrProtocolVersion    = errors.New("native IPC protocol version mismatch")
	ErrInvalidEnvelope    = errors.New("native IPC envelope is invalid")
	ErrUnknownEnvelopeKey = errors.New("native IPC envelope contains an unknown field")
	ErrMissingEnvelopeKey = errors.New("native IPC envelope is missing a required field")
)

// Envelope is the complete request/response envelope exchanged with a native
// helper. Deadline is encoded as an RFC3339Nano JSON string by time.Time.
// Payload remains raw JSON so the platform-neutral codec does not need to know
// action-specific fields.
type Envelope struct {
	ProtocolVersion string          `json:"protocol_version"`
	RequestID       string          `json:"request_id"`
	SessionID       string          `json:"session_id"`
	ActionID        string          `json:"action_id"`
	Deadline        time.Time       `json:"deadline"`
	Command         string          `json:"command"`
	Payload         json.RawMessage `json:"payload"`
}

// Validate checks the envelope independently from framing. The expected
// version is supplied by the Codec so version negotiation cannot be bypassed
// by callers that only use ReadFrame or WriteFrame.
func (e Envelope) Validate(expectedVersion string) error {
	if expectedVersion == "" {
		expectedVersion = ProtocolVersion
	}
	if e.ProtocolVersion == "" || e.ProtocolVersion != expectedVersion {
		return fmt.Errorf("%w: got %q, want %q", ErrProtocolVersion, e.ProtocolVersion, expectedVersion)
	}
	for name, value := range map[string]string{
		"request_id": e.RequestID,
		"session_id": e.SessionID,
		"action_id":  e.ActionID,
		"command":    e.Command,
	} {
		if value == "" {
			return fmt.Errorf("%w: %s", ErrMissingEnvelopeKey, name)
		}
	}
	if e.Deadline.IsZero() {
		return fmt.Errorf("%w: deadline", ErrMissingEnvelopeKey)
	}
	if len(bytes.TrimSpace(e.Payload)) == 0 || !json.Valid(e.Payload) {
		return fmt.Errorf("%w: payload must contain valid JSON", ErrInvalidEnvelope)
	}
	return nil
}

// Codec reads and writes one length-prefixed JSON frame at a time. The length
// is a big-endian uint32 and counts only the JSON body. A Codec is safe to use
// sequentially on one stream; callers that share a writer across goroutines
// must serialize WriteFrame calls themselves.
type Codec struct {
	MaxFrameSize    uint32
	ProtocolVersion string
}

func NewCodec(maxFrameSize uint32) Codec {
	if maxFrameSize == 0 {
		maxFrameSize = DefaultMaxFrameSize
	}
	return Codec{MaxFrameSize: maxFrameSize, ProtocolVersion: ProtocolVersion}
}

func (c Codec) normalized() Codec {
	if c.MaxFrameSize == 0 {
		c.MaxFrameSize = DefaultMaxFrameSize
	}
	if c.ProtocolVersion == "" {
		c.ProtocolVersion = ProtocolVersion
	}
	return c
}

// WriteFrame validates and writes exactly one frame. Validation happens before
// writing anything, so a rejected envelope cannot leave a partial frame.
func (c Codec) WriteFrame(w io.Writer, envelope Envelope) error {
	c = c.normalized()
	if w == nil {
		return fmt.Errorf("%w: nil writer", ErrMalformedFrame)
	}
	if err := envelope.Validate(c.ProtocolVersion); err != nil {
		return err
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	if uint64(len(body)) > uint64(c.MaxFrameSize) {
		return fmt.Errorf("%w: %d bytes > %d", ErrFrameTooLarge, len(body), c.MaxFrameSize)
	}

	var prefix [framePrefixSize]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(body)))
	if err := writeAll(w, prefix[:]); err != nil {
		return fmt.Errorf("write frame length: %w", err)
	}
	if err := writeAll(w, body); err != nil {
		return fmt.Errorf("write frame body: %w", err)
	}
	return nil
}

// ReadFrame reads exactly one frame. It never allocates based on an untrusted
// length above MaxFrameSize, and it leaves any subsequent frame in the reader
// for the next call.
func (c Codec) ReadFrame(r io.Reader) (Envelope, error) {
	c = c.normalized()
	if r == nil {
		return Envelope{}, fmt.Errorf("%w: nil reader", ErrMalformedFrame)
	}
	var prefix [framePrefixSize]byte
	n, err := io.ReadFull(r, prefix[:])
	if err != nil {
		if n == 0 && errors.Is(err, io.EOF) {
			return Envelope{}, io.EOF
		}
		return Envelope{}, fmt.Errorf("%w: read frame length: %w", ErrMalformedFrame, err)
	}
	length := binary.BigEndian.Uint32(prefix[:])
	if length == 0 {
		return Envelope{}, fmt.Errorf("%w: empty frame", ErrMalformedFrame)
	}
	if length > c.MaxFrameSize {
		return Envelope{}, fmt.Errorf("%w: %d bytes > %d", ErrFrameTooLarge, length, c.MaxFrameSize)
	}

	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return Envelope{}, fmt.Errorf("%w: read frame body: %w", ErrMalformedFrame, err)
	}

	var envelope Envelope
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		if stringsContainsUnknownField(err) {
			return Envelope{}, fmt.Errorf("%w: %v", ErrUnknownEnvelopeKey, err)
		}
		return Envelope{}, fmt.Errorf("%w: decode JSON: %w", ErrMalformedFrame, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Envelope{}, fmt.Errorf("%w: multiple JSON values in one frame", ErrMalformedFrame)
		}
		return Envelope{}, fmt.Errorf("%w: trailing JSON: %w", ErrMalformedFrame, err)
	}
	if err := envelope.Validate(c.ProtocolVersion); err != nil {
		return Envelope{}, err
	}
	return envelope, nil
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if n > 0 {
			data = data[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func stringsContainsUnknownField(err error) bool {
	var syntaxErr *json.UnmarshalTypeError
	if errors.As(err, &syntaxErr) {
		return false
	}
	return strings.HasPrefix(err.Error(), "json: unknown field")
}
