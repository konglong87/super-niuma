package server

import (
	"context"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/net/websocket"
)

type mobileWSMessage struct {
	Type         string `json:"type"`
	Event        string `json:"event,omitempty"`
	SessionID    uint64 `json:"session_id,omitempty"`
	MessageID    uint64 `json:"message_id,omitempty"`
	MessageKey   string `json:"message_key,omitempty"`
	GoalID       string `json:"goal_id,omitempty"`
	GoalEvent    any    `json:"goal_event,omitempty"`
	GoalPlan     any    `json:"goal_plan,omitempty"`
	GoalEvidence any    `json:"goal_evidence,omitempty"`
	StepID       string `json:"step_id,omitempty"`
	StepStatus   string `json:"step_status,omitempty"`
	Status       string `json:"status,omitempty"`
	Delta        string `json:"delta,omitempty"`
	Error        string `json:"error,omitempty"`
	TenantKey    string `json:"tenant_key,omitempty"`
	UserID       string `json:"user_id,omitempty"`
	DeviceID     string `json:"device_id,omitempty"`
	Timestamp    string `json:"timestamp,omitempty"`
}

func mobileWebSocketGin(opts Options) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := mobileClaimsFromGin(c)
		websocket.Handler(func(conn *websocket.Conn) {
			handleMobileWebSocket(c.Request.Context(), conn, opts, claims)
		}).ServeHTTP(c.Writer, c.Request)
	}
}

func handleMobileWebSocket(ctx context.Context, conn *websocket.Conn, opts Options, claims mobileClaims) {
	wsCtx, stop := context.WithCancel(ctx)
	outbound := make(chan mobileWSMessage, 64)
	writeDone := make(chan struct{})
	go mobileWSWriteLoop(wsCtx, conn, outbound, writeDone)
	defer func() {
		stop()
		_ = conn.Close()
		<-writeDone
	}()

	subscriptions := make(map[uint64]*mobileWSSubscription)
	goalSubscriptions := make(map[string]*mobileWSSubscription)
	defer func() {
		if opts.MobileStreams != nil {
			for _, sub := range subscriptions {
				opts.MobileStreams.Unsubscribe(sub)
			}
			for _, sub := range goalSubscriptions {
				opts.MobileStreams.UnsubscribeGoal(sub)
			}
		}
	}()
	// Send an explicit connection event first so mobile clients can correlate
	// the socket with their JWT identity before issuing control messages.
	if !mobileWSEnqueue(wsCtx, outbound, mobileWSMessage{
		Type:      "connected",
		Status:    "ok",
		TenantKey: claims.TenantKey,
		UserID:    claims.UserID,
		DeviceID:  claims.DeviceID,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}) {
		return
	}
	for {
		var msg mobileWSMessage
		if err := websocket.JSON.Receive(conn, &msg); err != nil {
			return
		}
		if wsCtx.Err() != nil {
			return
		}
		if err := handleMobileWSMessage(wsCtx, opts, claims, msg, outbound, subscriptions, goalSubscriptions); err != nil {
			return
		}
	}
}

func mobileWSWriteLoop(ctx context.Context, conn *websocket.Conn, outbound <-chan mobileWSMessage, done chan<- struct{}) {
	defer close(done)
	for {
		select {
		case msg := <-outbound:
			_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if err := websocket.JSON.Send(conn, msg); err != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

func mobileWSEnqueue(ctx context.Context, outbound chan<- mobileWSMessage, msg mobileWSMessage) bool {
	select {
	case outbound <- msg:
		return true
	case <-ctx.Done():
		return false
	}
}

func mobileWSForwardSubscription(ctx context.Context, outbound chan<- mobileWSMessage, sub *mobileWSSubscription) {
	if sub == nil {
		return
	}
	for {
		select {
		case msg, ok := <-sub.ch:
			if !ok {
				return
			}
			if !mobileWSEnqueue(ctx, outbound, msg) {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

func handleMobileWSMessage(ctx context.Context, opts Options, claims mobileClaims, msg mobileWSMessage, outbound chan<- mobileWSMessage, subscriptions map[uint64]*mobileWSSubscription, goalSubscriptions map[string]*mobileWSSubscription) error {
	switch strings.ToLower(strings.TrimSpace(msg.Type)) {
	case "ping":
		if !mobileWSEnqueue(ctx, outbound, mobileWSMessage{Type: "pong", Status: "ok", Timestamp: time.Now().UTC().Format(time.RFC3339)}) {
			return context.Canceled
		}
		return nil
	case "subscribe":
		if msg.SessionID == 0 {
			if !mobileWSEnqueue(ctx, outbound, mobileWSMessage{Type: "error", Status: "bad_request", Error: "session_id is required", TenantKey: claims.TenantKey}) {
				return context.Canceled
			}
			return nil
		}
		if subscriptions[msg.SessionID] == nil && opts.MobileStreams != nil {
			// Subscriptions are scoped by tenant/user/session so another mobile
			// principal cannot observe or cancel a session by guessing ids.
			sub := opts.MobileStreams.Subscribe(claims, msg.SessionID)
			subscriptions[msg.SessionID] = sub
			go mobileWSForwardSubscription(ctx, outbound, sub)
		}
		if !mobileWSEnqueue(ctx, outbound, mobileWSMessage{Type: "subscribed", Status: "ok", SessionID: msg.SessionID}) {
			return context.Canceled
		}
		return nil
	case "subscribe_goal":
		goalID := strings.TrimSpace(msg.GoalID)
		if goalID == "" {
			if !mobileWSEnqueue(ctx, outbound, mobileWSMessage{Type: "error", Status: "bad_request", Error: "goal_id is required", TenantKey: claims.TenantKey}) {
				return context.Canceled
			}
			return nil
		}
		if goalSubscriptions[goalID] == nil && opts.MobileStreams != nil {
			sub := opts.MobileStreams.SubscribeGoal(claims, goalID)
			goalSubscriptions[goalID] = sub
			go mobileWSForwardSubscription(ctx, outbound, sub)
		}
		if !mobileWSEnqueue(ctx, outbound, mobileWSMessage{Type: "subscribed_goal", Status: "ok", GoalID: goalID}) {
			return context.Canceled
		}
		return nil
	case "cancel":
		cancelled := false
		if opts.MobileStreams != nil {
			cancelled = opts.MobileStreams.CancelFor(claims, msg.SessionID, msg.MessageID, msg.MessageKey)
		}
		status := "not_found"
		if cancelled {
			status = "cancelled"
		}
		if !mobileWSEnqueue(ctx, outbound, mobileWSMessage{Type: "cancel_ack", Status: status, SessionID: msg.SessionID, MessageID: msg.MessageID, MessageKey: msg.MessageKey}) {
			return context.Canceled
		}
		return nil
	default:
		if !mobileWSEnqueue(ctx, outbound, mobileWSMessage{Type: "error", Status: "unsupported", Error: "unsupported mobile websocket message type", TenantKey: claims.TenantKey}) {
			return context.Canceled
		}
		return nil
	}
}
