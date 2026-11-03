package session

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// TranscriptEvent is the transport-neutral projection of a canonical JSONL
// session event. Session-control and other consumers can map it to their own
// event DTO without making the transcript package depend on those packages.
type TranscriptEvent struct {
	Sequence    uint64
	TaskID      uint64
	EventType   string
	PayloadJSON string
	TraceID     string
	CreatedAt   time.Time
	Source      string
	Surface     string
	Channel     string
	Scope       string
	TenantKey   string
	UserKey     string
}

// AppendEvent appends one canonical event while assigning its sequence under
// the same cross-process transcript lock used by normal Recorder writes.
func (r *Recorder) AppendEvent(event TranscriptEvent) (uint64, error) {
	if r == nil {
		return 0, fmt.Errorf("transcript recorder is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.encoder == nil {
		return 0, fmt.Errorf("transcript recorder is closed")
	}

	var sequence uint64
	err := r.withLock(func() error {
		entries, err := Load(r.Path)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.Sequence > sequence {
				sequence = entry.Sequence
			}
		}
		sequence++
		payload := strings.TrimSpace(event.PayloadJSON)
		if payload == "" {
			payload = "{}"
		}
		if !json.Valid([]byte(payload)) {
			return fmt.Errorf("transcript event payload is not valid JSON")
		}
		rawPayload := json.RawMessage(payload)
		entry := Entry{
			ID:        "event-" + strconv.FormatUint(sequence, 10),
			Sequence:  sequence,
			TaskID:    event.TaskID,
			EventType: strings.TrimSpace(event.EventType),
			TraceID:   strings.TrimSpace(event.TraceID),
			Source:    strings.TrimSpace(event.Source),
			Surface:   strings.TrimSpace(event.Surface),
			Channel:   strings.TrimSpace(event.Channel),
			Scope:     strings.TrimSpace(event.Scope),
			TenantKey: strings.TrimSpace(event.TenantKey),
			UserKey:   strings.TrimSpace(event.UserKey),
			Type:      EntryTypeSessionEvent,
			Metadata:  rawPayload,
			Timestamp: event.CreatedAt.UTC(),
		}
		if entry.EventType == "" {
			return fmt.Errorf("transcript event type is required")
		}
		if entry.Timestamp.IsZero() {
			entry.Timestamp = time.Now().UTC()
		}
		if r.schema != "" {
			return r.appendV2(entry)
		}
		return r.encoder.Encode(entry)
	})
	if err != nil {
		return 0, err
	}
	return sequence, nil
}

// LoadEvents returns canonical events in append sequence order. It reads raw
// transcript order deliberately: session events are an append-only log and
// must not be filtered through the message-graph active-chain projection.
func LoadEvents(path string, after uint64, limit int) ([]TranscriptEvent, error) {
	entries, err := Load(path)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = len(entries)
	}
	out := make([]TranscriptEvent, 0, limit)
	for _, entry := range entries {
		if entry.Type != EntryTypeSessionEvent || entry.Sequence <= after {
			continue
		}
		payload := strings.TrimSpace(string(entry.Metadata))
		if payload == "" {
			payload = "{}"
		}
		if !json.Valid([]byte(payload)) {
			return nil, fmt.Errorf("transcript event %d has invalid JSON payload", entry.Sequence)
		}
		out = append(out, TranscriptEvent{
			Sequence:    entry.Sequence,
			TaskID:      entry.TaskID,
			EventType:   entry.EventType,
			PayloadJSON: payload,
			TraceID:     entry.TraceID,
			CreatedAt:   entry.Timestamp,
			Source:      entry.Source,
			Surface:     entry.Surface,
			Channel:     entry.Channel,
			Scope:       entry.Scope,
			TenantKey:   entry.TenantKey,
			UserKey:     entry.UserKey,
		})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}
