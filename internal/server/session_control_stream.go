package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/sessioncontrol"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

const (
	sessionControlStateSchema      = "niuma.session-control-state.v1"
	sessionControlEventOperation   = "operation"
	sessionControlEventRun         = "run"
	sessionControlEventSession     = "session"
	sessionControlEventStreamError = "stream_error"

	defaultSessionControlStreamLimit = 200
	minSessionControlPollInterval    = 250 * time.Millisecond
	maxSessionControlPollInterval    = 2 * time.Second
)

type sessionControlStateEvent struct {
	Type          string                       `json:"-"`
	SchemaVersion string                       `json:"schema_version"`
	Cursor        string                       `json:"cursor"`
	SessionRef    string                       `json:"session_ref"`
	OperationID   string                       `json:"operation_id,omitempty"`
	RunID         uint64                       `json:"run_id,omitempty"`
	Status        sessioncontrol.SessionStatus `json:"status"`
	UpdatedAt     time.Time                    `json:"updated_at"`
	SourceEventID uint64                       `json:"-"`
}

type sessionControlStreamPolicy struct {
	minInterval time.Duration
	maxInterval time.Duration
	wait        func(context.Context, time.Duration) bool
}

func defaultSessionControlStreamPolicy() sessionControlStreamPolicy {
	return sessionControlStreamPolicy{
		minInterval: minSessionControlPollInterval,
		maxInterval: maxSessionControlPollInterval,
		wait:        waitSessionControlPoll,
	}
}

func (p sessionControlStreamPolicy) next(current time.Duration, changed bool) time.Duration {
	if changed || current < p.minInterval {
		return p.minInterval
	}
	if next := current * 2; next < p.maxInterval {
		return next
	}
	return p.maxInterval
}

func waitSessionControlPoll(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

type sessionControlStateStream struct {
	service        SessionControlService
	events         SessionControlEventService
	requestContext sessioncontrol.RequestContext
	ref            sessioncontrol.SessionRef
	snapshot       sessioncontrol.SessionSnapshot
	cursor         uint64
	limit          int
	write          func(string, sessionControlStateEvent)
	heartbeat      func()
	flush          func()
	policy         sessionControlStreamPolicy
	lastProjection string
}

func newSessionControlStateStream(service SessionControlService, events SessionControlEventService, requestContext sessioncontrol.RequestContext, ref sessioncontrol.SessionRef, snapshot sessioncontrol.SessionSnapshot, limit int) *sessionControlStateStream {
	if limit <= 0 {
		limit = defaultSessionControlStreamLimit
	}
	if limit > defaultSessionControlStreamLimit {
		limit = defaultSessionControlStreamLimit
	}
	return &sessionControlStateStream{
		service: service, events: events, requestContext: requestContext, ref: ref, snapshot: snapshot,
		limit: limit, policy: defaultSessionControlStreamPolicy(),
		write: func(string, sessionControlStateEvent) {}, heartbeat: func() {}, flush: func() {},
	}
}

func tenantSessionControlStreamHandler(opts Options) http.HandlerFunc {
	return sessionControlEndpoint(opts, func(w http.ResponseWriter, r *http.Request, requestContext sessioncontrol.RequestContext) {
		ref, ok := sessionControlRefFromRequest(w, r)
		if !ok {
			return
		}
		cursor, ok := sessionControlStreamCursor(w, r)
		if !ok {
			return
		}
		// Readback is the ownership check and recovery baseline. It must finish
		// before SSE headers make an HTTP error response impossible.
		snapshot, err := opts.SessionControl.Get(r.Context(), sessioncontrol.GetRequest{Context: requestContext, Ref: ref})
		if err != nil {
			writeSessionControlServiceError(w, err)
			return
		}
		if opts.SessionControlEvents == nil {
			writeSessionControlError(w, http.StatusServiceUnavailable, sessionControlCodeServiceUnavailable, "session control event stream is not configured")
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeSessionControlError(w, http.StatusInternalServerError, string(sessioncontrol.CodeInternal), "streaming is not supported")
			return
		}

		w.Header().Set("content-type", "text/event-stream")
		w.Header().Set("cache-control", "no-cache")
		w.Header().Set("connection", "keep-alive")
		stream := newSessionControlStateStream(opts.SessionControl, opts.SessionControlEvents, requestContext, ref, snapshot, parseLimit(r.URL.Query().Get(paramLimit)))
		stream.cursor = cursor
		stream.write = func(event string, value sessionControlStateEvent) { writeSessionControlSSE(w, event, value) }
		stream.heartbeat = func() { _, _ = io.WriteString(w, ": heartbeat\n\n") }
		stream.flush = flusher.Flush
		stream.run(r.Context())
	})
}

func sessionControlStreamCursor(w http.ResponseWriter, r *http.Request) (uint64, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("cursor"))
	if raw == "" {
		raw = strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	}
	if raw == "" {
		return 0, true
	}
	cursor, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		writeSessionControlError(w, http.StatusBadRequest, sessionControlCodeInvalidRequest, "event cursor is invalid")
		return 0, false
	}
	return cursor, true
}

func (s *sessionControlStateStream) run(ctx context.Context) {
	initial := projectSessionControlSnapshot(s.snapshot, s.cursor)
	s.emit(initial)
	if sessionControlTerminal(initial.Status) {
		return
	}

	interval := s.policy.minInterval
	for {
		changed := false
		if s.snapshot.ActiveRunID != 0 {
			events, err := s.events.ListAgentTaskEventsAfter(ctx, s.snapshot.ActiveRunID, s.cursor, s.limit)
			if err != nil {
				s.fail()
				return
			}
			for _, event := range events {
				if event.TaskID != s.snapshot.ActiveRunID || event.ID <= s.cursor {
					continue
				}
				projection := projectSessionControlTaskEvent(s.ref, event)
				if projection.Type == sessionControlEventSession {
					projection.Status = s.snapshot.Status
				}
				s.cursor = event.ID
				projection.Cursor = strconv.FormatUint(s.cursor, 10)
				changed = s.emit(projection) || changed
			}
		}

		snapshot, err := s.service.Get(ctx, sessioncontrol.GetRequest{Context: s.requestContext, Ref: s.ref})
		if err != nil {
			s.fail()
			return
		}
		s.snapshot = snapshot
		projection := projectSessionControlSnapshot(snapshot, s.cursor)
		changed = s.emit(projection) || changed
		s.flush()
		if sessionControlTerminal(snapshot.Status) {
			return
		}
		if !changed {
			s.heartbeat()
			s.flush()
		}
		interval = s.policy.next(interval, changed)
		if !s.policy.wait(ctx, interval) {
			return
		}
	}
}

func (s *sessionControlStateStream) emit(event sessionControlStateEvent) bool {
	if event.SourceEventID != 0 {
		s.write(event.Type, event)
		s.flush()
		return true
	}
	signature := fmt.Sprintf("%s|%s|%s|%d|%s", event.Type, event.SessionRef, event.OperationID, event.RunID, event.Status)
	if signature == s.lastProjection {
		return false
	}
	s.lastProjection = signature
	s.write(event.Type, event)
	s.flush()
	return true
}

func (s *sessionControlStateStream) fail() {
	s.emit(sessionControlStateEvent{
		Type: sessionControlEventStreamError, SchemaVersion: sessionControlStateSchema,
		Cursor: strconv.FormatUint(s.cursor, 10), SessionRef: s.ref.String(),
		RunID: s.snapshot.ActiveRunID, Status: s.snapshot.Status, UpdatedAt: time.Now().UTC(),
	})
}

func projectSessionControlSnapshot(snapshot sessioncontrol.SessionSnapshot, cursor uint64) sessionControlStateEvent {
	return sessionControlStateEvent{
		Type: sessionControlEventSession, SchemaVersion: sessionControlStateSchema,
		Cursor: strconv.FormatUint(cursor, 10), SessionRef: snapshot.Ref.String(), RunID: snapshot.ActiveRunID,
		Status: snapshot.Status, UpdatedAt: snapshot.UpdatedAt,
	}
}

func projectSessionControlTaskEvent(ref sessioncontrol.SessionRef, event mysqlstore.AgentTaskEvent) sessionControlStateEvent {
	projection := sessionControlStateEvent{
		Type: sessionControlEventRun, SchemaVersion: sessionControlStateSchema,
		Cursor: strconv.FormatUint(event.ID, 10), SessionRef: ref.String(), RunID: event.TaskID,
		Status: sessionControlRunEventStatus(event.EventType), UpdatedAt: event.CreatedAt,
	}
	if event.EventType == agenttasks.EventSessionControlStop {
		projection.SourceEventID = event.ID
		projection.Type = sessionControlEventOperation
		projection.Status = sessioncontrol.StatusStopped
		projection.OperationID = sessionControlOperationID(event.PayloadJSON)
	} else if event.EventType == agenttasks.EventSessionHandoff {
		projection.SourceEventID = event.ID
		projection.Type = sessionControlEventSession
	}
	return projection
}

func sessionControlRunEventStatus(eventType string) sessioncontrol.SessionStatus {
	switch eventType {
	case agenttasks.EventCompleted:
		return sessioncontrol.StatusCompleted
	case agenttasks.EventFailed:
		return sessioncontrol.StatusFailed
	case agenttasks.EventCancelled, agenttasks.EventTimeout, agenttasks.EventSessionControlStop:
		return sessioncontrol.StatusStopped
	case agenttasks.EventPermissionRequest:
		return sessioncontrol.StatusWaitingPermission
	case agenttasks.EventInputQueued, agenttasks.EventInputReordered, agenttasks.EventInputUpdated:
		return sessioncontrol.StatusQueued
	default:
		return sessioncontrol.StatusRunning
	}
}

func sessionControlOperationID(payload string) string {
	var envelope struct {
		Operation struct {
			OperationID string `json:"operation_id"`
		} `json:"operation"`
	}
	if json.Unmarshal([]byte(payload), &envelope) != nil {
		return ""
	}
	operationID := strings.TrimSpace(envelope.Operation.OperationID)
	if len(operationID) != 64 {
		return ""
	}
	for _, char := range operationID {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return ""
		}
	}
	return operationID
}

func sessionControlTerminal(status sessioncontrol.SessionStatus) bool {
	switch status {
	case sessioncontrol.StatusCompleted, sessioncontrol.StatusFailed, sessioncontrol.StatusStopped, sessioncontrol.StatusArchived:
		return true
	default:
		return false
	}
}

func writeSessionControlSSE(w io.Writer, event string, value sessionControlStateEvent) {
	payload, err := json.Marshal(value)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", value.Cursor, event, payload)
}
