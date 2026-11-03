package native

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"
)

func TestCodecRoundTripPreservesEnvelope(t *testing.T) {
	codec := NewCodec(1024)
	want := testEnvelope()

	var stream bytes.Buffer
	if err := codec.WriteFrame(&stream, want); err != nil {
		t.Fatalf("WriteFrame() error = %v", err)
	}
	got, err := codec.ReadFrame(&stream)
	if err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}
	if got.ProtocolVersion != want.ProtocolVersion || got.RequestID != want.RequestID ||
		got.SessionID != want.SessionID || got.ActionID != want.ActionID ||
		!got.Deadline.Equal(want.Deadline) || got.Command != want.Command ||
		!bytes.Equal(got.Payload, want.Payload) {
		t.Fatalf("ReadFrame() = %#v, want %#v", got, want)
	}
}

func TestCodecReads粘连FramesIndependently(t *testing.T) {
	codec := NewCodec(1024)
	first := testEnvelope()
	second := testEnvelope()
	second.RequestID = "request-2"
	second.ActionID = "action-2"

	var stream bytes.Buffer
	if err := codec.WriteFrame(&stream, first); err != nil {
		t.Fatal(err)
	}
	if err := codec.WriteFrame(&stream, second); err != nil {
		t.Fatal(err)
	}
	gotFirst, err := codec.ReadFrame(&stream)
	if err != nil {
		t.Fatalf("first ReadFrame() error = %v", err)
	}
	gotSecond, err := codec.ReadFrame(&stream)
	if err != nil {
		t.Fatalf("second ReadFrame() error = %v", err)
	}
	if gotFirst.RequestID != first.RequestID || gotSecond.RequestID != second.RequestID {
		t.Fatalf("got request IDs %q and %q, want %q and %q", gotFirst.RequestID, gotSecond.RequestID, first.RequestID, second.RequestID)
	}
	if _, err := codec.ReadFrame(&stream); !errors.Is(err, io.EOF) {
		t.Fatalf("third ReadFrame() error = %v, want io.EOF", err)
	}
}

func TestCodecRejectsIllegalJSON(t *testing.T) {
	codec := NewCodec(1024)
	body := []byte(`{"protocol_version":"computer-use.v1","request_id":`)
	var stream bytes.Buffer
	writeRawFrame(t, &stream, body)

	_, err := codec.ReadFrame(&stream)
	if !errors.Is(err, ErrMalformedFrame) {
		t.Fatalf("ReadFrame() error = %v, want ErrMalformedFrame", err)
	}
}

func TestCodecRejectsUnknownEnvelopeField(t *testing.T) {
	codec := NewCodec(1024)
	body := validEnvelopeJSON(t)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	fields["unexpected"] = json.RawMessage(`true`)
	body, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	var stream bytes.Buffer
	writeRawFrame(t, &stream, body)

	_, err = codec.ReadFrame(&stream)
	if !errors.Is(err, ErrUnknownEnvelopeKey) {
		t.Fatalf("ReadFrame() error = %v, want ErrUnknownEnvelopeKey", err)
	}
}

func TestCodecRejectsProtocolVersionMismatch(t *testing.T) {
	codec := NewCodec(1024)
	bad := testEnvelope()
	bad.ProtocolVersion = "computer-use.v0"

	var stream bytes.Buffer
	if err := codec.WriteFrame(&stream, bad); !errors.Is(err, ErrProtocolVersion) {
		t.Fatalf("WriteFrame() error = %v, want ErrProtocolVersion", err)
	}

	body := validEnvelopeJSON(t)
	body = bytes.Replace(body, []byte(`computer-use.v1`), []byte(`computer-use.v0`), 1)
	writeRawFrame(t, &stream, body)
	_, err := codec.ReadFrame(&stream)
	if !errors.Is(err, ErrProtocolVersion) {
		t.Fatalf("ReadFrame() error = %v, want ErrProtocolVersion", err)
	}
}

func TestCodecRejectsOversizedFrameBeforeAllocatingBody(t *testing.T) {
	codec := NewCodec(32)
	var stream bytes.Buffer
	var prefix [framePrefixSize]byte
	binary.BigEndian.PutUint32(prefix[:], codec.MaxFrameSize+1)
	stream.Write(prefix[:])

	_, err := codec.ReadFrame(&stream)
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("ReadFrame() error = %v, want ErrFrameTooLarge", err)
	}
	if stream.Len() != 0 {
		t.Fatalf("ReadFrame() consumed oversized body bytes: remaining=%d", stream.Len())
	}
}

func TestCodecAcceptsFrameAtExactLimit(t *testing.T) {
	want := testEnvelope()
	body, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	codec := NewCodec(uint32(len(body)))

	var stream bytes.Buffer
	if err := codec.WriteFrame(&stream, want); err != nil {
		t.Fatalf("WriteFrame() error = %v", err)
	}
	got, err := codec.ReadFrame(&stream)
	if err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}
	if got.RequestID != want.RequestID {
		t.Fatalf("ReadFrame() request_id = %q, want %q", got.RequestID, want.RequestID)
	}
}

func TestCodecRejectsOversizedFrameOnWrite(t *testing.T) {
	codec := NewCodec(128)
	want := testEnvelope()
	want.Payload = json.RawMessage(`{"data":"this payload intentionally exceeds the configured frame limit"}`)

	var stream bytes.Buffer
	if err := codec.WriteFrame(&stream, want); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("WriteFrame() error = %v, want ErrFrameTooLarge", err)
	}
	if stream.Len() != 0 {
		t.Fatalf("WriteFrame() wrote a partial frame: %d bytes", stream.Len())
	}
}

func TestCodecRejectsTruncatedPrefixAndBody(t *testing.T) {
	codec := NewCodec(1024)
	for name, data := range map[string][]byte{
		"truncated prefix": {0, 0},
		"truncated body":   {0, 3, '{'},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := codec.ReadFrame(bytes.NewReader(data))
			if !errors.Is(err, ErrMalformedFrame) {
				t.Fatalf("ReadFrame() error = %v, want ErrMalformedFrame", err)
			}
		})
	}
}

func testEnvelope() Envelope {
	return Envelope{
		ProtocolVersion: ProtocolVersion,
		RequestID:       "request-1",
		SessionID:       "session-1",
		ActionID:        "action-1",
		Deadline:        time.Date(2026, 9, 24, 12, 0, 0, 123456789, time.UTC),
		Command:         "observe",
		Payload:         json.RawMessage(`{"include_cursor":true}`),
	}
}

func validEnvelopeJSON(t *testing.T) []byte {
	t.Helper()
	body, err := json.Marshal(testEnvelope())
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func writeRawFrame(t *testing.T, stream *bytes.Buffer, body []byte) {
	t.Helper()
	var prefix [framePrefixSize]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(body)))
	stream.Write(prefix[:])
	stream.Write(body)
}
