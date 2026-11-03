package server

import (
	"context"
	"fmt"
	"github.com/konglong87/super-niuma/internal/sessioncontrol"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	"net/http"
	"strconv"
)

const (
	conversationPageLimit         = 200
	conversationSubscriptionLimit = 32
	conversationSchema            = "niuma.session-conversation.v1"
)

type sessionConversationPage struct {
	Schema  string                      `json:"schema_version"`
	Session sessionControlSessionDTO    `json:"session"`
	Events  []mysqlstore.AgentTaskEvent `json:"events"`
	Cursor  string                      `json:"cursor"`
	HasMore bool                        `json:"has_more"`
}

func readSessionConversation(ctx context.Context, opts Options, scope sessioncontrol.RequestContext, ref sessioncontrol.SessionRef, cursor uint64) (sessionConversationPage, error) {
	if ref.Source != sessioncontrol.SourceTenant {
		return sessionConversationPage{}, &sessioncontrol.ServiceError{Code: sessioncontrol.CodeForbidden, Message: "conversation events require a managed session"}
	}
	// Perform ownership readback before querying any content, including reconnects.
	snapshot, err := opts.SessionControl.Get(ctx, sessioncontrol.GetRequest{Context: scope, Ref: ref})
	if err != nil {
		return sessionConversationPage{}, err
	}
	var events []mysqlstore.AgentTaskEvent
	if opts.SessionEvents != nil {
		events, err = opts.SessionEvents.ListSessionEvents(ctx, scope.TenantID, scope.UserID, snapshot.ID, snapshot.CWD, cursor, conversationPageLimit)
	} else {
		reader, ok := opts.TenantService.(SessionConversationEventReader)
		if !ok {
			return sessionConversationPage{}, fmt.Errorf("conversation event reader unavailable")
		}
		events, err = reader.ListSessionConversationEvents(ctx, snapshot.ID, cursor, conversationPageLimit)
	}
	if err != nil {
		return sessionConversationPage{}, err
	}
	if events == nil {
		events = []mysqlstore.AgentTaskEvent{}
	}
	for _, event := range events {
		if event.ID > cursor {
			cursor = event.ID
		}
	}
	return sessionConversationPage{Schema: conversationSchema, Session: newSessionControlSessionDTO(snapshot), Events: events, Cursor: strconv.FormatUint(cursor, 10), HasMore: len(events) == conversationPageLimit}, nil
}

func tenantSessionConversationHandler(opts Options) http.HandlerFunc {
	return sessionControlEndpoint(opts, func(w http.ResponseWriter, r *http.Request, scope sessioncontrol.RequestContext) {
		ref, ok := sessionControlRefFromRequest(w, r)
		if !ok {
			return
		}
		cursor, ok := sessionControlStreamCursor(w, r)
		if !ok {
			return
		}
		page, err := readSessionConversation(r.Context(), opts, scope, ref, cursor)
		if err != nil {
			writeSessionControlServiceError(w, err)
			return
		}
		writeJSON(w, sessionControlDataEnvelope[sessionConversationPage]{Data: page})
	})
}

type conversationSubscription struct {
	Ref    string `json:"ref"`
	Cursor string `json:"cursor"`
}
type conversationSubscribeRequest struct {
	Sessions []conversationSubscription `json:"sessions"`
}

// One connection carries bounded pages for each session. Reconnect supplies a
// separate cursor per session; a busy run cannot advance another run's cursor.
func tenantSessionConversationsStreamHandler(opts Options) http.HandlerFunc {
	return sessionControlEndpoint(opts, func(w http.ResponseWriter, r *http.Request, scope sessioncontrol.RequestContext) {
		var input conversationSubscribeRequest
		if !decodeSessionControlBody(w, r, &input, false) {
			return
		}
		if len(input.Sessions) == 0 || len(input.Sessions) > conversationSubscriptionLimit {
			writeSessionControlError(w, http.StatusBadRequest, sessionControlCodeInvalidRequest, "invalid subscription count")
			return
		}
		refs := make([]sessioncontrol.SessionRef, len(input.Sessions))
		cursors := make([]uint64, len(refs))
		pages := make([]sessionConversationPage, len(refs))
		seen := make(map[string]bool)
		for i, sub := range input.Sessions {
			ref, err := sessioncontrol.ParseRef(sub.Ref)
			cursor, cursorErr := strconv.ParseUint(sub.Cursor, 10, 64)
			if err != nil || cursorErr != nil || seen[sub.Ref] {
				writeSessionControlError(w, http.StatusBadRequest, sessionControlCodeInvalidRequest, "invalid subscription")
				return
			}
			seen[sub.Ref] = true
			refs[i], cursors[i] = ref, cursor
			pages[i], err = readSessionConversation(r.Context(), opts, scope, ref, cursor)
			if err != nil {
				writeSessionControlServiceError(w, err)
				return
			}
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeSessionControlError(w, http.StatusInternalServerError, string(sessioncontrol.CodeInternal), "streaming unsupported")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		policy := defaultSessionControlStreamPolicy()
		interval := policy.minInterval
		for {
			changed := false
			for i, page := range pages {
				writeAgentTaskSSE(w, "conversation", page)
				cursors[i], _ = strconv.ParseUint(page.Cursor, 10, 64)
				changed = changed || len(page.Events) > 0
			}
			flusher.Flush()
			interval = policy.next(interval, changed)
			if !policy.wait(r.Context(), interval) {
				return
			}
			for i, ref := range refs {
				page, err := readSessionConversation(r.Context(), opts, scope, ref, cursors[i])
				if err != nil {
					writeAgentTaskSSE(w, "error", map[string]string{"error": "conversation stream unavailable"})
					flusher.Flush()
					return
				}
				pages[i] = page
			}
			if r.Context().Err() != nil {
				return
			}
		}
	})
}
