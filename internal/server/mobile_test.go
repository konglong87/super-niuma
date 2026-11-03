package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"golang.org/x/net/websocket"

	"github.com/konglong87/super-niuma/internal/goal"
	"github.com/konglong87/super-niuma/internal/query"
	"github.com/konglong87/super-niuma/internal/quota"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	"github.com/konglong87/super-niuma/internal/telemetry"
	tenantservice "github.com/konglong87/super-niuma/internal/tenant"
)

func TestMobileChatRequiresJWT(t *testing.T) {
	handler := NewHandler(Options{MobileJWTSecret: "secret", TenantService: &fakeTenantService{}}, nil)
	req := httptest.NewRequest(http.MethodGet, "/mobile/chat/sessions", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMobileChatDevAuthUsesHeaderIdentity(t *testing.T) {
	fake := &fakeTenantService{sessions: []mysqlstore.Session{{ID: 5, SessionKey: "s1", Title: "First", Status: "active"}}}
	handler := NewHandler(Options{MobileDevAuth: true, TenantService: fake}, nil)
	req := httptest.NewRequest(http.MethodGet, "/mobile/chat/sessions", nil)
	req.Header.Set("X-Tenant-Key", "yutang")
	req.Header.Set("X-User-Id", "web-user")
	req.Header.Set("X-Device-Id", "web-browser")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || fake.lastTenant != "yutang" || fake.lastContextUser != "web-user" {
		t.Fatalf("status=%d body=%s fake=%+v", rec.Code, rec.Body.String(), fake)
	}
}

func TestMobileWebSocketPingSubscribeAndCancel(t *testing.T) {
	secret := "mobile-secret"
	streams := NewMobileStreamRegistry()
	cancelled := make(chan struct{})
	streams.Register(5, 12, "msg-1", func() { close(cancelled) })
	handler := NewHandler(Options{MobileJWTSecret: secret, MobileStreams: streams}, nil)
	server := httptest.NewServer(handler)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/mobile/chat/ws"
	origin, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := websocket.NewConfig(wsURL, origin.String())
	if err != nil {
		t.Fatal(err)
	}
	cfg.Header.Set("Authorization", "Bearer "+mobileJWT(t, secret, mobileClaims{TenantKey: "yutang", UserID: "user-test", DeviceID: "ios-test", ExpiresAt: time.Now().Add(time.Hour).Unix()}))
	conn, err := websocket.DialConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	var msg mobileWSMessage
	if err := websocket.JSON.Receive(conn, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Type != "connected" || msg.TenantKey != "yutang" || msg.UserID != "user-test" {
		t.Fatalf("connected msg = %+v", msg)
	}
	if err := websocket.JSON.Send(conn, mobileWSMessage{Type: "ping"}); err != nil {
		t.Fatal(err)
	}
	if err := websocket.JSON.Receive(conn, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Type != "pong" || msg.Status != "ok" {
		t.Fatalf("pong msg = %+v", msg)
	}
	if err := websocket.JSON.Send(conn, mobileWSMessage{Type: "subscribe", SessionID: 5}); err != nil {
		t.Fatal(err)
	}
	if err := websocket.JSON.Receive(conn, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Type != "subscribed" || msg.SessionID != 5 {
		t.Fatalf("subscribe msg = %+v", msg)
	}
	if err := websocket.JSON.Send(conn, mobileWSMessage{Type: "cancel", SessionID: 5, MessageID: 12, MessageKey: "msg-1"}); err != nil {
		t.Fatal(err)
	}
	if err := websocket.JSON.Receive(conn, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Type != "cancel_ack" || msg.Status != "cancelled" {
		t.Fatalf("cancel msg = %+v", msg)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("cancel callback was not invoked")
	}
}

func TestMobileWebSocketReceivesSubscribedStreamEvents(t *testing.T) {
	secret := "mobile-secret"
	streams := NewMobileStreamRegistry()
	fake := &fakeTenantService{
		sessions:  []mysqlstore.Session{{ID: 5, SessionKey: "s1", Status: "active"}},
		messageID: 90,
	}
	handler := NewHandler(Options{
		MobileJWTSecret: secret,
		MobileStreams:   streams,
		TenantService:   fake,
		StreamQueryFunc: func(ctx context.Context, req QueryRequest, textSink io.Writer) (query.Result, error) {
			_, _ = textSink.Write([]byte("hi"))
			return query.Result{Response: "hi"}, nil
		},
	}, nil)
	server := httptest.NewServer(handler)
	defer server.Close()

	conn := mobileWebSocketConn(t, server.URL, secret, mobileClaims{
		TenantKey: "yutang",
		UserID:    "user-test",
		DeviceID:  "ios-second",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	})
	defer conn.Close()

	var msg mobileWSMessage
	if err := websocket.JSON.Receive(conn, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Type != "connected" {
		t.Fatalf("connected msg = %+v", msg)
	}
	if err := websocket.JSON.Send(conn, mobileWSMessage{Type: "subscribe", SessionID: 5}); err != nil {
		t.Fatal(err)
	}
	if err := websocket.JSON.Receive(conn, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Type != "subscribed" || msg.SessionID != 5 {
		t.Fatalf("subscribe msg = %+v", msg)
	}

	req := mobileRequest(t, http.MethodPost, "/mobile/chat/sessions/5/messages/stream", `{"content":"hello","message_key":"client-ws-1"}`, secret)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	seen := map[string]mobileWSMessage{}
	for len(seen) < 3 {
		if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		if err := websocket.JSON.Receive(conn, &msg); err != nil {
			t.Fatal(err)
		}
		if msg.Type != "message_event" {
			continue
		}
		seen[msg.Event] = msg
	}
	if seen["message_start"].Status != mobileMessageStreaming || seen["delta"].Delta != "hi" || seen["message_stop"].Status != mobileMessageCompleted {
		t.Fatalf("events = %+v", seen)
	}
}

func TestMobileWebSocketMultiDeviceReceivesSubscribedStreamEvents(t *testing.T) {
	secret := "mobile-secret"
	streams := NewMobileStreamRegistry()
	fake := &fakeTenantService{
		sessions:  []mysqlstore.Session{{ID: 5, SessionKey: "s1", Status: "active"}},
		messageID: 90,
	}
	handler := NewHandler(Options{
		MobileJWTSecret: secret,
		MobileStreams:   streams,
		TenantService:   fake,
		StreamQueryFunc: func(ctx context.Context, req QueryRequest, textSink io.Writer) (query.Result, error) {
			_, _ = textSink.Write([]byte("hi"))
			return query.Result{Response: "hi"}, nil
		},
	}, nil)
	server := httptest.NewServer(handler)
	defer server.Close()

	deviceA := mobileWebSocketConn(t, server.URL, secret, mobileClaims{TenantKey: "yutang", UserID: "user-test", DeviceID: "ios-a", ExpiresAt: time.Now().Add(time.Hour).Unix()})
	defer deviceA.Close()
	deviceB := mobileWebSocketConn(t, server.URL, secret, mobileClaims{TenantKey: "yutang", UserID: "user-test", DeviceID: "ios-b", ExpiresAt: time.Now().Add(time.Hour).Unix()})
	defer deviceB.Close()
	mobileWSSubscribeSession(t, deviceA, 5)
	mobileWSSubscribeSession(t, deviceB, 5)

	req := mobileRequest(t, http.MethodPost, "/mobile/chat/sessions/5/messages/stream", `{"content":"hello","message_key":"client-ws-multi"}`, secret)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	for label, conn := range map[string]*websocket.Conn{"device-a": deviceA, "device-b": deviceB} {
		seen := mobileWSReadMessageEvents(t, conn, 3)
		if seen["message_start"].Status != mobileMessageStreaming || seen["delta"].Delta != "hi" || seen["message_stop"].Status != mobileMessageCompleted {
			t.Fatalf("%s events = %+v", label, seen)
		}
	}
}

func TestMobileWebSocketReceivesSubscribedGoalEvents(t *testing.T) {
	secret := "mobile-secret"
	now := time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC)
	streams := NewMobileStreamRegistry()
	fake := &fakeTenantService{
		goals: []goal.Goal{{
			ID:          "goal_ws",
			Objective:   "stream goal status",
			Status:      goal.StatusActive,
			CWD:         "/workspace",
			TurnBudget:  3,
			TokenBudget: 1000,
			CreatedAt:   now,
			UpdatedAt:   now,
		}},
	}
	handler := NewHandler(Options{
		AuthToken:       "token",
		MobileJWTSecret: secret,
		MobileStreams:   streams,
		TenantService:   fake,
	}, nil)
	server := httptest.NewServer(handler)
	defer server.Close()

	conn := mobileWebSocketConn(t, server.URL, secret, mobileClaims{
		TenantKey: "yutang",
		UserID:    "user-test",
		DeviceID:  "ios-goal",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	})
	defer conn.Close()

	var msg mobileWSMessage
	if err := websocket.JSON.Receive(conn, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Type != "connected" {
		t.Fatalf("connected msg = %+v", msg)
	}
	if err := websocket.JSON.Send(conn, mobileWSMessage{Type: "subscribe_goal", GoalID: "goal_ws"}); err != nil {
		t.Fatal(err)
	}
	if err := websocket.JSON.Receive(conn, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Type != "subscribed_goal" || msg.GoalID != "goal_ws" || msg.Status != "ok" {
		t.Fatalf("subscribe goal msg = %+v", msg)
	}

	req := tenantGoalRequest(http.MethodPost, "/tenant/goals/goal_ws/stop", "")
	req.Header.Set("X-Device-Id", "ios-goal")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := websocket.JSON.Receive(conn, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Type != "goal_event" || msg.Event != string(goal.EventGoalStopped) || msg.GoalID != "goal_ws" || msg.Status != string(goal.StatusStopped) {
		t.Fatalf("goal event msg = %+v", msg)
	}
	if msg.TenantKey != "yutang" || msg.UserID != "user-test" || msg.DeviceID != "ios-goal" {
		t.Fatalf("goal event identity = %+v", msg)
	}
	if msg.GoalEvent == nil {
		t.Fatalf("goal event payload missing: %+v", msg)
	}
}

func TestMobileStreamRegistryGoalBroadcastIsScopedToPrincipal(t *testing.T) {
	streams := NewMobileStreamRegistry()
	allowed := streams.SubscribeGoal(mobileClaims{TenantKey: "yutang", UserID: "user-a"}, "goal_scope")
	blocked := streams.SubscribeGoal(mobileClaims{TenantKey: "yutang", UserID: "user-b"}, "goal_scope")
	defer streams.UnsubscribeGoal(allowed)
	defer streams.UnsubscribeGoal(blocked)

	streams.BroadcastGoalFor(mobileClaims{TenantKey: "yutang", UserID: "user-a"}, goal.Event{
		GoalID: "goal_scope",
		Type:   goal.EventStatusChanged,
		Status: goal.StatusBlocked,
	})
	select {
	case msg := <-allowed.ch:
		if msg.Type != "goal_event" || msg.Event != string(goal.EventStatusChanged) || msg.Status != string(goal.StatusBlocked) {
			t.Fatalf("allowed msg = %+v", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("allowed subscriber did not receive goal event")
	}
	select {
	case msg := <-blocked.ch:
		t.Fatalf("cross-user subscriber received goal event: %+v", msg)
	default:
	}
}

func TestMobileStreamRegistryGoalPlanEvidenceBroadcasts(t *testing.T) {
	streams := NewMobileStreamRegistry()
	allowed := streams.SubscribeGoal(mobileClaims{TenantKey: "yutang", UserID: "user-a"}, "goal_plan")
	blocked := streams.SubscribeGoal(mobileClaims{TenantKey: "yutang", UserID: "user-b"}, "goal_plan")
	defer streams.UnsubscribeGoal(allowed)
	defer streams.UnsubscribeGoal(blocked)

	streams.BroadcastGoalPlanFor(mobileClaims{TenantKey: "yutang", UserID: "user-a"}, goal.GoalPlan{
		GoalID:        "goal_plan",
		Version:       1,
		CurrentStepID: "step_verify",
		Steps: []goal.GoalStep{{
			ID:     "step_verify",
			Title:  "Verify",
			Status: goal.StepStatusActive,
		}},
	})
	streams.BroadcastGoalEvidenceFor(mobileClaims{TenantKey: "yutang", UserID: "user-a"}, goal.GoalEvidence{
		ID:     "ev_test",
		GoalID: "goal_plan",
		Type:   goal.EvidenceTypeTest,
		Passed: true,
	})

	seen := map[string]mobileWSMessage{}
	for len(seen) < 2 {
		select {
		case msg := <-allowed.ch:
			seen[msg.Event] = msg
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for plan/evidence broadcasts: %+v", seen)
		}
	}
	if seen[mobileGoalPlanUpdatedEvent].GoalPlan == nil || seen[mobileGoalPlanUpdatedEvent].StepID != "step_verify" || seen[mobileGoalPlanUpdatedEvent].StepStatus != string(goal.StepStatusActive) {
		t.Fatalf("plan msg = %+v", seen[mobileGoalPlanUpdatedEvent])
	}
	if seen[mobileGoalEvidenceAddedEvent].GoalEvidence == nil || seen[mobileGoalEvidenceAddedEvent].Status != mobileGoalEvidenceStatusAdded {
		t.Fatalf("evidence msg = %+v", seen[mobileGoalEvidenceAddedEvent])
	}
	select {
	case msg := <-blocked.ch:
		t.Fatalf("cross-user subscriber received goal plan/evidence event: %+v", msg)
	default:
	}
}

func TestMobileStreamRegistryCancelIsScopedToPrincipal(t *testing.T) {
	streams := NewMobileStreamRegistry()
	cancelled := make(chan struct{})
	streams.RegisterFor(mobileClaims{TenantKey: "yutang", UserID: "user-a"}, 5, 12, "msg-1", func() { close(cancelled) })
	if streams.CancelFor(mobileClaims{TenantKey: "yutang", UserID: "user-b"}, 5, 12, "msg-1") {
		t.Fatal("cross-user cancel should be rejected")
	}
	select {
	case <-cancelled:
		t.Fatal("cancel callback fired for the wrong principal")
	default:
	}
	if !streams.CancelFor(mobileClaims{TenantKey: "yutang", UserID: "user-a"}, 5, 12, "msg-1") {
		t.Fatal("same principal cancel should succeed")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("cancel callback was not invoked")
	}
}

func TestMobileChatRejectsExpiredJWT(t *testing.T) {
	secret := "secret"
	handler := NewHandler(Options{MobileJWTSecret: secret, TenantService: &fakeTenantService{}}, nil)
	req := httptest.NewRequest(http.MethodGet, "/mobile/chat/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+mobileJWT(t, secret, mobileClaims{TenantKey: "yutang", UserID: "user-test", ExpiresAt: time.Now().Add(-time.Minute).Unix()}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMobileChatRejectsUnsupportedJWTAlgorithm(t *testing.T) {
	secret := "secret"
	handler := NewHandler(Options{MobileJWTSecret: secret, TenantService: &fakeTenantService{}}, nil)
	req := httptest.NewRequest(http.MethodGet, "/mobile/chat/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+mobileJWTWithAlgorithm(t, secret, "HS512", mobileClaims{TenantKey: "yutang", UserID: "user-test", ExpiresAt: time.Now().Add(time.Hour).Unix()}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMobileChatSessionLifecycle(t *testing.T) {
	secret := "secret"
	fake := &fakeTenantService{
		sessionID: 9,
		sessions:  []mysqlstore.Session{{ID: 5, SessionKey: "s1", Title: "First", Status: "active"}},
		messageRows: []mysqlstore.Message{
			{ID: 1, SessionID: 5, TurnIndex: 1, Role: "user", Content: "hello"},
			{ID: 2, SessionID: 5, TurnIndex: 2, Role: "recap", Content: "old recap", Model: "recap-small"},
			{ID: 3, SessionID: 5, TurnIndex: 3, Role: "recap", Content: "latest recap", Model: "recap-small"},
		},
	}
	handler := NewHandler(Options{MobileJWTSecret: secret, TenantService: fake}, nil)

	req := mobileRequest(t, http.MethodPost, "/mobile/chat/sessions", `{"title":"New Chat"}`, secret)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || fake.lastSession.Title != "New Chat" || fake.lastTenant != "yutang" {
		t.Fatalf("status=%d body=%s fake=%+v", rec.Code, rec.Body.String(), fake)
	}

	req = mobileRequest(t, http.MethodGet, "/mobile/chat/sessions?limit=2", "", secret)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"session_key":"s1"`) || fake.lastLimit != 2 {
		t.Fatalf("status=%d body=%s fake=%+v", rec.Code, rec.Body.String(), fake)
	}

	req = mobileRequest(t, http.MethodGet, "/mobile/chat/sessions/5", "", secret)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"latest_recap"`) || !strings.Contains(rec.Body.String(), `"content":"latest recap"`) {
		t.Fatalf("status=%d body=%s fake=%+v", rec.Code, rec.Body.String(), fake)
	}

	req = mobileRequest(t, http.MethodPatch, "/mobile/chat/sessions/5", `{"title":"Renamed"}`, secret)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || fake.lastSessionID != 5 || fake.lastSession.Title != "Renamed" {
		t.Fatalf("status=%d body=%s fake=%+v", rec.Code, rec.Body.String(), fake)
	}

	req = mobileRequest(t, http.MethodDelete, "/mobile/chat/sessions/5", "", secret)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || fake.archivedSessionID != 5 {
		t.Fatalf("status=%d body=%s fake=%+v", rec.Code, rec.Body.String(), fake)
	}
}

func TestMobileChatMessageStreamEvents(t *testing.T) {
	secret := "secret"
	sink := telemetry.NewMemorySink()
	fake := &fakeTenantService{
		sessions:    []mysqlstore.Session{{ID: 5, SessionKey: "s1", Title: "First", Status: "active"}},
		messageRows: []mysqlstore.Message{{ID: 1, SessionID: 5, TurnIndex: 2, Role: "assistant", Content: "old"}},
		messageID:   88,
	}
	handler := NewHandler(Options{
		MobileJWTSecret: secret,
		TenantService:   fake,
		TelemetrySinks:  []telemetry.Sink{sink},
		StreamQueryFunc: func(ctx context.Context, req QueryRequest, textSink io.Writer) (query.Result, error) {
			if req.Prompt != "hello" || req.Model != "model-a" {
				t.Fatalf("query req = %+v", req)
			}
			if req.PromptMode != "chat" {
				t.Fatalf("prompt mode = %q", req.PromptMode)
			}
			_, _ = textSink.Write([]byte("hi"))
			return query.Result{
				Response: "hi",
				Model:    "model-a",
				Usage: query.Usage{
					InputTokens:              12,
					OutputTokens:             3,
					CacheCreationInputTokens: 2,
					CacheReadInputTokens:     4,
				},
			}, nil
		},
	}, nil)

	req := mobileRequest(t, http.MethodPost, "/mobile/chat/sessions/5/messages/stream", `{"content":"hello","model":"model-a","message_key":"client-msg-1"}`, secret)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, body)
	}
	for _, want := range []string{`event: message_start`, `"type":"delta"`, `"delta":"hi"`, `event: message_stop`, `"status":"completed"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in %s", want, body)
		}
	}
	if len(fake.messages) != 3 {
		t.Fatalf("messages = %+v", fake.messages)
	}
	if fake.messages[0].Role != "user" || fake.messages[1].Role != "assistant" || fake.messages[2].Content != "hi" || fake.messages[2].TurnIndex != 4 {
		t.Fatalf("messages = %+v", fake.messages)
	}
	if fake.messages[2].InputTokens != 12 || fake.messages[2].OutputToken != 3 {
		t.Fatalf("usage tokens not persisted: %+v", fake.messages[2])
	}
	if !strings.Contains(fake.messages[2].ContentJSON, `"status":"completed"`) || !strings.Contains(fake.messages[2].ContentJSON, `"device_id":"ios-test"`) {
		t.Fatalf("content json = %s", fake.messages[2].ContentJSON)
	}
	events := sink.Events()
	for _, want := range []string{
		"mobile.phase.request.bind.finished",
		"mobile.phase.session.load.finished",
		"mobile.phase.user_message.persist.finished",
		"mobile.phase.assistant_placeholder.persist.finished",
		"mobile.phase.sse.message_start.finished",
		"mobile.phase.query.run.finished",
		"mobile.phase.assistant_message.persist.finished",
		"mobile.phase.sse.message_stop.finished",
	} {
		if !telemetryEventsContain(events, want) {
			t.Fatalf("missing telemetry %q in %+v", want, events)
		}
	}
}

func TestMobileChatMessageStreamWritesExplicitRememberPending(t *testing.T) {
	secret := "secret"
	fake := &fakeTenantService{
		sessions:    []mysqlstore.Session{{ID: 5, SessionKey: "s1", Title: "First", Status: "active"}},
		messageRows: []mysqlstore.Message{{ID: 1, SessionID: 5, TurnIndex: 2, Role: "assistant", Content: "old"}},
		messageID:   88,
		memoryID:    99,
	}
	handler := NewHandler(Options{
		MobileJWTSecret: secret,
		TenantService:   fake,
		StreamQueryFunc: func(ctx context.Context, req QueryRequest, textSink io.Writer) (query.Result, error) {
			_, _ = textSink.Write([]byte("ok"))
			return query.Result{Response: "ok", Model: "model-a"}, nil
		},
	}, nil)

	req := mobileRequest(t, http.MethodPost, "/mobile/chat/sessions/5/messages/stream", `{"content":"请记住我喜欢简洁中文回答","model":"model-a","message_key":"client-msg-remember"}`, secret)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(fake.memoriesWritten) != 1 {
		t.Fatalf("memories = %+v", fake.memoriesWritten)
	}
	if fake.memoriesWritten[0].Category != "explicit_pending" || fake.memoriesWritten[0].Source != "explicit-user-remember-pending" {
		t.Fatalf("memory = %+v", fake.memoriesWritten[0])
	}
}

func TestMobileChatDevAuthRecordsTenantTelemetry(t *testing.T) {
	fake := &fakeTenantService{
		sessions:  []mysqlstore.Session{{ID: 5, SessionKey: "s1", Title: "First", Status: "active"}},
		messageID: 88,
	}
	handler := NewHandler(Options{
		MobileDevAuth: true,
		TenantService: fake,
		StreamQueryFunc: func(ctx context.Context, req QueryRequest, textSink io.Writer) (query.Result, error) {
			_, _ = textSink.Write([]byte("hi"))
			return query.Result{Response: "hi", Model: "model-a"}, nil
		},
	}, nil)

	req := httptest.NewRequest(http.MethodPost, "/mobile/chat/sessions/5/messages/stream", strings.NewReader(`{"content":"hello","model":"model-a","message_key":"client-msg-1"}`))
	req.Header.Set("X-Tenant-Key", "yutang")
	req.Header.Set("X-User-Id", "web-user")
	req.Header.Set("X-Device-Id", "web-browser")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !telemetryEventsContain(fake.telemetryRecords, "mobile.phase.query.run.finished") {
		t.Fatalf("missing tenant telemetry phase in %+v", fake.telemetryRecords)
	}
}

func TestMobileChatGeneratesTitleAsync(t *testing.T) {
	secret := "secret"
	updated := make(chan tenantTitleUpdate, 1)
	fake := &fakeTenantService{
		sessions: []mysqlstore.Session{{ID: 5, SessionKey: "s1", Status: "active"}},
		updateSessionHook: func(req tenantservice.SessionRequest) {
			updated <- tenantTitleUpdate{title: req.Title}
		},
	}
	handler := NewHandler(Options{
		MobileJWTSecret: secret,
		TenantService:   fake,
		StreamQueryFunc: func(ctx context.Context, req QueryRequest, textSink io.Writer) (query.Result, error) {
			_, _ = textSink.Write([]byte("answer"))
			return query.Result{Response: "answer"}, nil
		},
		SessionTitleFunc: func(ctx context.Context, prompt, response string) (string, error) {
			if prompt != "hello" || response != "answer" {
				t.Fatalf("title args prompt=%q response=%q", prompt, response)
			}
			return "Short Title", nil
		},
	}, nil)

	req := mobileRequest(t, http.MethodPost, "/mobile/chat/sessions/5/messages/stream", `{"content":"hello"}`, secret)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	select {
	case got := <-updated:
		if got.title != "Short Title" {
			t.Fatalf("title update = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for async title update")
	}
}

func TestMobileChatSkipsTitleWhenSessionAlreadyNamed(t *testing.T) {
	secret := "secret"
	fake := &fakeTenantService{
		sessions: []mysqlstore.Session{{ID: 5, SessionKey: "s1", Title: "Named", Status: "active"}},
	}
	handler := NewHandler(Options{
		MobileJWTSecret: secret,
		TenantService:   fake,
		StreamQueryFunc: func(ctx context.Context, req QueryRequest, textSink io.Writer) (query.Result, error) {
			_, _ = textSink.Write([]byte("answer"))
			return query.Result{Response: "answer"}, nil
		},
		SessionTitleFunc: func(ctx context.Context, prompt, response string) (string, error) {
			t.Fatal("title function should not run for named sessions")
			return "", nil
		},
	}, nil)

	req := mobileRequest(t, http.MethodPost, "/mobile/chat/sessions/5/messages/stream", `{"content":"hello"}`, secret)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMobileChatEnforcesModelPolicy(t *testing.T) {
	secret := "secret"
	fake := &fakeTenantService{
		sessions: []mysqlstore.Session{{ID: 5, SessionKey: "s1", Status: "active"}},
	}
	handler := NewHandler(Options{
		MobileJWTSecret: secret,
		TenantService:   fake,
		StreamQueryFunc: func(ctx context.Context, req QueryRequest, textSink io.Writer) (query.Result, error) {
			t.Fatal("stream should not run for forbidden model")
			return query.Result{}, nil
		},
		MobilePolicy: MobilePolicy{AllowedModels: []string{"allowed-model"}},
	}, nil)

	req := mobileRequest(t, http.MethodPost, "/mobile/chat/sessions/5/messages/stream", `{"content":"hello","model":"blocked-model"}`, secret)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || len(fake.messages) != 0 {
		t.Fatalf("status=%d body=%s messages=%+v", rec.Code, rec.Body.String(), fake.messages)
	}
}

func TestMobileChatEnforcesClaimPolicyAndQuota(t *testing.T) {
	secret := "secret"
	fake := &fakeTenantService{
		sessions: []mysqlstore.Session{{ID: 5, SessionKey: "s1", Status: "active"}},
	}
	handler := NewHandler(Options{
		MobileJWTSecret: secret,
		TenantService:   fake,
		StreamQueryFunc: func(ctx context.Context, req QueryRequest, textSink io.Writer) (query.Result, error) {
			_, _ = textSink.Write([]byte("ok"))
			return query.Result{Response: "ok"}, nil
		},
	}, nil)
	claims := mobileClaims{
		TenantKey:         "yutang",
		UserID:            "quota-user",
		ExpiresAt:         time.Now().Add(time.Hour).Unix(),
		AllowedModels:     []string{"model-a"},
		DailyMessageQuota: 1,
	}

	req := mobileRequestWithClaims(t, http.MethodPost, "/mobile/chat/sessions/5/messages/stream", `{"content":"hello","model":"model-a"}`, secret, claims)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = mobileRequestWithClaims(t, http.MethodPost, "/mobile/chat/sessions/5/messages/stream", `{"content":"again","model":"model-a"}`, secret, claims)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("second status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = mobileRequestWithClaims(t, http.MethodPost, "/mobile/chat/sessions/5/messages/stream", `{"content":"bad","model":"model-b"}`, secret, mobileClaims{
		TenantKey:     "yutang",
		UserID:        "other-user",
		ExpiresAt:     time.Now().Add(time.Hour).Unix(),
		AllowedModels: []string{"model-a"},
	})
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("model status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = mobileRequestWithClaims(t, http.MethodPost, "/mobile/chat/sessions/5/messages/stream", `{"content":"too many tokens"}`, secret, mobileClaims{
		TenantKey:       "yutang",
		UserID:          "token-user",
		ExpiresAt:       time.Now().Add(time.Hour).Unix(),
		DailyTokenQuota: 1,
	})
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("token status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMobileChatEnforcesRateLimit(t *testing.T) {
	secret := "secret"
	fake := &fakeTenantService{
		sessions: []mysqlstore.Session{{ID: 5, SessionKey: "s1", Status: "active"}},
	}
	handler := NewHandler(Options{
		MobileJWTSecret: secret,
		TenantService:   fake,
		StreamQueryFunc: func(ctx context.Context, req QueryRequest, textSink io.Writer) (query.Result, error) {
			_, _ = textSink.Write([]byte("ok"))
			return query.Result{Response: "ok"}, nil
		},
		MobilePolicy: MobilePolicy{RateLimitPerMinute: 1},
	}, nil)

	req := mobileRequest(t, http.MethodPost, "/mobile/chat/sessions/5/messages/stream", `{"content":"hello"}`, secret)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = mobileRequest(t, http.MethodPost, "/mobile/chat/sessions/5/messages/stream", `{"content":"again"}`, secret)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMobileChatReleasesClaimQuotaWhenTenantQuotaRejects(t *testing.T) {
	secret := "secret"
	fake := &fakeTenantService{
		sessions:        []mysqlstore.Session{{ID: 5, SessionKey: "s1", Status: "active"}},
		quotaReserveErr: quota.ErrDailyTokenLimitExceeded,
	}
	handler := NewHandler(Options{
		MobileJWTSecret: secret,
		TenantService:   fake,
		StreamQueryFunc: func(ctx context.Context, req QueryRequest, textSink io.Writer) (query.Result, error) {
			return query.Result{Response: "ok"}, nil
		},
	}, nil)
	claims := mobileClaims{
		TenantKey:         "yutang",
		UserID:            "quota-release-user",
		ExpiresAt:         time.Now().Add(time.Hour).Unix(),
		DailyMessageQuota: 1,
	}

	req := mobileRequestWithClaims(t, http.MethodPost, "/mobile/chat/sessions/5/messages/stream", `{"content":"blocked"}`, secret, claims)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("first status=%d body=%s", rec.Code, rec.Body.String())
	}

	fake.quotaReserveErr = nil
	req = mobileRequestWithClaims(t, http.MethodPost, "/mobile/chat/sessions/5/messages/stream", `{"content":"allowed"}`, secret, claims)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("second status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMobileChatReleasesQuotaWhenPersistFailsAfterTenantReservation(t *testing.T) {
	secret := "secret"
	persistErr := errors.New("persist failed")
	fake := &fakeTenantService{
		tenantID:         1,
		userID:           2,
		sessions:         []mysqlstore.Session{{ID: 5, SessionKey: "s1", Status: "active"}},
		quotaConfig:      mysqlstore.QuotaConfig{TenantID: 1, QuotaEnabled: true},
		upsertMessageErr: persistErr,
	}
	handler := NewHandler(Options{
		MobileJWTSecret: secret,
		TenantService:   fake,
		StreamQueryFunc: func(ctx context.Context, req QueryRequest, textSink io.Writer) (query.Result, error) {
			t.Fatal("stream should not run when message persistence fails")
			return query.Result{}, nil
		},
	}, nil)
	claims := mobileClaims{
		TenantKey:         "yutang",
		UserID:            "quota-persist-user",
		ExpiresAt:         time.Now().Add(time.Hour).Unix(),
		DailyMessageQuota: 1,
	}

	req := mobileRequestWithClaims(t, http.MethodPost, "/mobile/chat/sessions/5/messages/stream", `{"content":"first"}`, secret, claims)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("first status=%d body=%s", rec.Code, rec.Body.String())
	}
	if fake.lastQuotaReservation.RequestID == "" {
		t.Fatalf("quota reservation was not captured")
	}
	if fake.lastQuotaUsage.Estimated != true {
		t.Fatalf("quota usage = %+v", fake.lastQuotaUsage)
	}

	fake.upsertMessageErr = nil
	handler = NewHandler(Options{
		MobileJWTSecret: secret,
		TenantService:   fake,
		StreamQueryFunc: func(ctx context.Context, req QueryRequest, textSink io.Writer) (query.Result, error) {
			_, _ = textSink.Write([]byte("ok"))
			return query.Result{Response: "ok"}, nil
		},
	}, nil)
	req = mobileRequestWithClaims(t, http.MethodPost, "/mobile/chat/sessions/5/messages/stream", `{"content":"second"}`, secret, claims)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("second status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMobileChatStreamsAttachments(t *testing.T) {
	secret := "secret"
	fake := &fakeTenantService{
		sessions: []mysqlstore.Session{{ID: 5, SessionKey: "s1", Status: "active"}},
	}
	handler := NewHandler(Options{
		MobileJWTSecret: secret,
		TenantService:   fake,
		StreamQueryFunc: func(ctx context.Context, req QueryRequest, textSink io.Writer) (query.Result, error) {
			if !strings.Contains(req.Prompt, "Attached context") || !strings.Contains(req.Prompt, "https://cdn.example.test/image.png") || !strings.Contains(req.Prompt, "voice transcript") {
				t.Fatalf("prompt = %q", req.Prompt)
			}
			_, _ = textSink.Write([]byte("ok"))
			return query.Result{Response: "ok"}, nil
		},
	}, nil)

	body := `{"content":"describe these","attachments":[{"type":"image","media_type":"image/png","name":"image.png","url":"https://cdn.example.test/image.png","size_bytes":1024},{"type":"voice","media_type":"audio/m4a","name":"voice.m4a","url":"https://cdn.example.test/voice.m4a","transcript":"voice transcript"}]}`
	req := mobileRequest(t, http.MethodPost, "/mobile/chat/sessions/5/messages/stream", body, secret)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(fake.messages) < 1 || !strings.Contains(fake.messages[0].ContentJSON, `"attachments"`) || !strings.Contains(fake.messages[0].ContentJSON, `"type":"image"`) || !strings.Contains(fake.messages[0].ContentJSON, `"transcript":"voice transcript"`) {
		t.Fatalf("messages = %+v", fake.messages)
	}
}

func TestMobileChatRoutesMultimodalModel(t *testing.T) {
	project := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, "config"))
	mustWriteServerTest(t, filepath.Join(project, "config", "settings.json"), `{"multimodal":{"enabled":true,"models":{"image":"vision-mobile"}}}`)
	secret := "secret"
	fake := &fakeTenantService{
		sessions: []mysqlstore.Session{{ID: 5, SessionKey: "s1", Status: "active", Model: "text-model"}},
	}
	handler := NewHandler(Options{
		Workspace:       project,
		MobileJWTSecret: secret,
		TenantService:   fake,
		StreamQueryFunc: func(ctx context.Context, req QueryRequest, textSink io.Writer) (query.Result, error) {
			if req.Model != "vision-mobile" {
				t.Fatalf("model = %q", req.Model)
			}
			if len(req.Attachments) != 1 || req.Attachments[0].Type != "image" {
				t.Fatalf("attachments = %+v", req.Attachments)
			}
			_, _ = textSink.Write([]byte("ok"))
			return query.Result{Response: "ok", Model: req.Model}, nil
		},
	}, nil)

	body := `{"content":"describe","attachments":[{"type":"image","media_type":"image/png","name":"image.png","url":"https://cdn.example.test/image.png","size_bytes":1024}]}`
	req := mobileRequest(t, http.MethodPost, "/mobile/chat/sessions/5/messages/stream", body, secret)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(fake.messages) < 2 || fake.messages[0].Model != "vision-mobile" || fake.messages[1].Model != "vision-mobile" {
		t.Fatalf("messages = %+v", fake.messages)
	}
}

func TestMobileChatRejectsInvalidAttachments(t *testing.T) {
	secret := "secret"
	fake := &fakeTenantService{
		sessions: []mysqlstore.Session{{ID: 5, SessionKey: "s1", Status: "active"}},
	}
	handler := NewHandler(Options{
		MobileJWTSecret: secret,
		TenantService:   fake,
		StreamQueryFunc: func(ctx context.Context, req QueryRequest, textSink io.Writer) (query.Result, error) {
			t.Fatal("stream should not run for invalid attachments")
			return query.Result{}, nil
		},
		MobilePolicy: MobilePolicy{MaxAttachmentBytes: 4, AllowedAttachments: []string{"image"}},
	}, nil)

	req := mobileRequest(t, http.MethodPost, "/mobile/chat/sessions/5/messages/stream", `{"attachments":[{"type":"image","url":"https://cdn.example.test/big.png","size_bytes":5}]}`, secret)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMobileChatMessageStreamFailureEvents(t *testing.T) {
	secret := "secret"
	fake := &fakeTenantService{
		sessions:  []mysqlstore.Session{{ID: 5, SessionKey: "s1", Title: "First", Status: "active"}},
		messageID: 88,
	}
	handler := NewHandler(Options{
		MobileJWTSecret: secret,
		TenantService:   fake,
		StreamQueryFunc: func(ctx context.Context, req QueryRequest, textSink io.Writer) (query.Result, error) {
			_, _ = textSink.Write([]byte("partial"))
			return query.Result{}, errors.New("provider unavailable")
		},
	}, nil)

	req := mobileRequest(t, http.MethodPost, "/mobile/chat/sessions/5/messages/stream", `{"content":"hello","message_key":"client-msg-err"}`, secret)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, body)
	}
	for _, want := range []string{`event: message_start`, `"delta":"partial"`, `event: error`, `"status":"failed"`, `"error":"provider unavailable"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in %s", want, body)
		}
	}
	if len(fake.messages) != 3 || !fake.messages[2].IsError || !strings.Contains(fake.messages[2].ContentJSON, `"status":"failed"`) {
		t.Fatalf("messages = %+v", fake.messages)
	}
}

func TestMobileChatMessageListSupportsAfterTurn(t *testing.T) {
	secret := "secret"
	fake := &fakeTenantService{
		sessions: []mysqlstore.Session{{ID: 5, SessionKey: "s1", Title: "First", Status: "active"}},
		messageRows: []mysqlstore.Message{
			{ID: 1, SessionID: 5, TurnIndex: 1, Role: "user", Content: "old"},
			{ID: 2, SessionID: 5, TurnIndex: 2, Role: "assistant", Content: "old answer"},
			{ID: 3, SessionID: 5, TurnIndex: 3, Role: "user", Content: "new"},
		},
	}
	handler := NewHandler(Options{MobileJWTSecret: secret, TenantService: fake}, nil)
	req := mobileRequest(t, http.MethodGet, "/mobile/chat/sessions/5/messages?after_turn=2&limit=1", "", secret)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, body)
	}
	if !strings.Contains(body, `"turn_index":3`) || strings.Contains(body, `"turn_index":2`) {
		t.Fatalf("body=%s", body)
	}
}

func TestMobileChatMessageListSupportsCursor(t *testing.T) {
	secret := "secret"
	fake := &fakeTenantService{
		sessions: []mysqlstore.Session{{ID: 5, SessionKey: "s1", Title: "First", Status: "active"}},
		messageRows: []mysqlstore.Message{
			{ID: 1, SessionID: 5, TurnIndex: 1, Role: "user", Content: "old"},
			{ID: 2, SessionID: 5, TurnIndex: 2, Role: "assistant", Content: "old answer"},
			{ID: 3, SessionID: 5, TurnIndex: 3, Role: "user", Content: "new"},
		},
	}
	handler := NewHandler(Options{MobileJWTSecret: secret, TenantService: fake}, nil)
	req := mobileRequest(t, http.MethodGet, "/mobile/chat/sessions/5/messages?cursor=1&limit=1", "", secret)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, body)
	}
	if !strings.Contains(body, `"turn_index":2`) || !strings.Contains(body, `"next_cursor":"2"`) || !strings.Contains(body, `"has_more":true`) {
		t.Fatalf("body=%s", body)
	}
}

func TestMobileChatMessageKeyIsIdempotent(t *testing.T) {
	secret := "secret"
	calls := 0
	fake := &fakeTenantService{
		sessions: []mysqlstore.Session{{ID: 5, SessionKey: "s1", Status: "active"}},
	}
	handler := NewHandler(Options{
		MobileJWTSecret: secret,
		TenantService:   fake,
		StreamQueryFunc: func(ctx context.Context, req QueryRequest, textSink io.Writer) (query.Result, error) {
			calls++
			_, _ = textSink.Write([]byte("ok"))
			return query.Result{Response: "ok"}, nil
		},
	}, nil)

	for i := 0; i < 2; i++ {
		req := mobileRequest(t, http.MethodPost, "/mobile/chat/sessions/5/messages/stream", `{"content":"hello","message_key":"client-stable-1"}`, secret)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d status=%d body=%s", i, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"message_key":"client-stable-1"`) {
			t.Fatalf("body=%s", rec.Body.String())
		}
	}
	if calls != 1 {
		t.Fatalf("stream calls = %d", calls)
	}
	if len(fake.messages) != 3 {
		t.Fatalf("messages = %+v", fake.messages)
	}
}

func TestMobileChatCancelMessageMarksCancelled(t *testing.T) {
	secret := "secret"
	fake := &fakeTenantService{
		sessions: []mysqlstore.Session{{ID: 5, SessionKey: "s1", Status: "active"}},
		messageRows: []mysqlstore.Message{
			{ID: 12, SessionID: 5, TurnIndex: 2, Role: "assistant", Content: "partial", ContentJSON: mobileMetadataJSON(mobileClaims{DeviceID: "ios-test"}, "msg-cancel", mobileMessageStreaming)},
		},
	}
	handler := NewHandler(Options{MobileJWTSecret: secret, TenantService: fake}, nil)
	req := mobileRequest(t, http.MethodPost, "/mobile/chat/sessions/5/messages/12/cancel", `{}`, secret)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(fake.messages) != 1 || !fake.messages[0].IsError || !strings.Contains(fake.messages[0].ContentJSON, `"status":"cancelled"`) {
		t.Fatalf("messages = %+v", fake.messages)
	}
}

func TestMobileChatRegeneratesAssistantMessage(t *testing.T) {
	secret := "secret"
	fake := &fakeTenantService{
		sessions: []mysqlstore.Session{{ID: 5, SessionKey: "s1", Status: "active"}},
		messageRows: []mysqlstore.Message{
			{ID: 1, SessionID: 5, TurnIndex: 1, Role: "user", Content: "hello"},
			{ID: 2, SessionID: 5, TurnIndex: 2, Role: "assistant", Content: "old", Model: "model-a"},
		},
	}
	handler := NewHandler(Options{
		MobileJWTSecret: secret,
		TenantService:   fake,
		StreamQueryFunc: func(ctx context.Context, req QueryRequest, textSink io.Writer) (query.Result, error) {
			if req.Prompt != "hello" || req.Model != "model-a" {
				t.Fatalf("query req = %+v", req)
			}
			_, _ = textSink.Write([]byte("new"))
			return query.Result{Response: "new", Model: "model-a"}, nil
		},
	}, nil)
	req := mobileRequest(t, http.MethodPost, "/mobile/chat/sessions/5/messages/2/regenerate", `{}`, secret)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"delta":"new"`) || len(fake.messages) != 2 || fake.messages[1].TurnIndex != 3 || fake.messages[1].Content != "new" {
		t.Fatalf("body=%s messages=%+v", rec.Body.String(), fake.messages)
	}
}

func TestMobileChatBranchesConversation(t *testing.T) {
	secret := "secret"
	fake := &fakeTenantService{
		sessionID: 77,
		sessions:  []mysqlstore.Session{{ID: 5, SessionKey: "s1", Title: "Source", Status: "active", Model: "model-a", CWD: "/tmp/work"}},
		messageRows: []mysqlstore.Message{
			{ID: 1, SessionID: 5, TurnIndex: 1, Role: "user", Content: "hello"},
			{ID: 2, SessionID: 5, TurnIndex: 2, Role: "assistant", Content: "answer"},
			{ID: 3, SessionID: 5, TurnIndex: 3, Role: "user", Content: "later"},
		},
	}
	handler := NewHandler(Options{MobileJWTSecret: secret, TenantService: fake}, nil)
	req := mobileRequest(t, http.MethodPost, "/mobile/chat/sessions/5/branch", `{"until_turn":2,"title":"Forked"}`, secret)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"copied_messages":2`) || fake.lastSession.Title != "Forked" || fake.lastSession.Model != "model-a" {
		t.Fatalf("body=%s session=%+v", rec.Body.String(), fake.lastSession)
	}
	if len(fake.messages) != 2 || fake.messages[0].SessionID != 77 || fake.messages[1].Content != "answer" {
		t.Fatalf("messages = %+v", fake.messages)
	}
}

func manyMessageRows(sessionID uint64, count int) []mysqlstore.Message {
	rows := make([]mysqlstore.Message, 0, count)
	for i := 0; i < count; i++ {
		rows = append(rows, mysqlstore.Message{
			ID:        uint64(i + 1),
			SessionID: sessionID,
			TurnIndex: uint(i + 1),
			Role:      "user",
			Content:   "msg",
		})
	}
	return rows
}

// TODO-115：fork 一个超过 500 条消息的会话，不能静默只复制前 500 条还报成功。
// 根因是 normalizeLimit 把 ListMessages(…, 1000) 静默夹到 500。
func TestMobileChatBranchCopiesEveryMessageBeyondTheReadClamp(t *testing.T) {
	secret := "secret"
	fake := &fakeTenantService{
		sessionID:   77,
		sessions:    []mysqlstore.Session{{ID: 5, SessionKey: "s1", Title: "Source", Status: "active", Model: "model-a"}},
		messageRows: manyMessageRows(5, 620),
	}
	handler := NewHandler(Options{MobileJWTSecret: secret, TenantService: fake}, nil)
	req := mobileRequest(t, http.MethodPost, "/mobile/chat/sessions/5/branch", `{}`, secret)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"copied_messages":620`) {
		t.Fatalf("branch silently truncated: body=%s", rec.Body.String())
	}
	if len(fake.messages) != 620 {
		t.Fatalf("copied %d messages, want 620", len(fake.messages))
	}
	// 分支点也不能来自被截断的列表：untilTurn 缺省时要取真正的最大轮次。
	if !strings.Contains(rec.Body.String(), `"until_turn":620`) {
		t.Fatalf("branch point came from the truncated list: body=%s", rec.Body.String())
	}
}

// 超过显式上限时要响亮地失败，而不是悄悄少复制几条。
func TestMobileChatBranchRefusesSessionsBeyondTheForkCap(t *testing.T) {
	secret := "secret"
	fake := &fakeTenantService{
		sessionID:   77,
		sessions:    []mysqlstore.Session{{ID: 5, SessionKey: "s1", Status: "active"}},
		messageRows: manyMessageRows(5, mysqlstore.MaxForkedMessages+1),
	}
	handler := NewHandler(Options{MobileJWTSecret: secret, TenantService: fake}, nil)
	req := mobileRequest(t, http.MethodPost, "/mobile/chat/sessions/5/branch", `{}`, secret)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(fake.committedSessions) != 0 {
		t.Fatalf("refused fork still created a session: %+v", fake.committedSessions)
	}
}

// until_message_id 指向第 500 条之后的消息时，那条消息是存在的，不能报 404。
func TestMobileChatBranchFindsMessageBeyondTheReadClamp(t *testing.T) {
	secret := "secret"
	fake := &fakeTenantService{
		sessionID:   77,
		sessions:    []mysqlstore.Session{{ID: 5, SessionKey: "s1", Status: "active"}},
		messageRows: manyMessageRows(5, 620),
	}
	handler := NewHandler(Options{MobileJWTSecret: secret, TenantService: fake}, nil)
	req := mobileRequest(t, http.MethodPost, "/mobile/chat/sessions/5/branch", `{"until_message_id":600}`, secret)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"until_turn":600`) || !strings.Contains(rec.Body.String(), `"copied_messages":600`) {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

// TODO-116：同一个静默夹取还有一处更糟的后果 —— mobileNextTurns 从被截断的列表算
// 下一个 turn_index，在超过 500 条的会话里会算出一个已经存在的轮次，而消息表的
// 唯一键是 (session_id, turn_index) 且写入走 ON DUPLICATE KEY UPDATE，
// 于是新消息会覆盖掉一条已有消息。
func TestMobileChatNextTurnDoesNotOverwriteExistingMessages(t *testing.T) {
	secret := "secret"
	fake := &fakeTenantService{
		sessions:    []mysqlstore.Session{{ID: 5, SessionKey: "s1", Status: "active", Model: "model-a"}},
		messageRows: manyMessageRows(5, 620),
	}
	handler := NewHandler(Options{
		MobileJWTSecret: secret,
		TenantService:   fake,
		StreamQueryFunc: func(ctx context.Context, req QueryRequest, textSink io.Writer) (query.Result, error) {
			_, _ = textSink.Write([]byte("ok"))
			return query.Result{Response: "ok", Model: "model-a"}, nil
		},
	}, nil)
	req := mobileRequest(t, http.MethodPost, "/mobile/chat/sessions/5/messages/stream", `{"content":"hi"}`, secret)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(fake.messages) == 0 {
		t.Fatal("no message was written")
	}
	for _, message := range fake.messages {
		if message.TurnIndex <= 620 {
			t.Fatalf("new message reused turn %d, overwriting an existing row", message.TurnIndex)
		}
	}
}

// longConversationRows 造一段 user/assistant 交替的长会话：奇数轮是 user，偶数轮是
// assistant，ID 与 turn_index 同号。这样第 600 条既是 assistant（可以 regenerate），
// 前一条第 599 条又是它对应的 user 消息 —— 两条都落在 normalizeLimit 的 500 之外。
func longConversationRows(sessionID uint64, count int) []mysqlstore.Message {
	rows := make([]mysqlstore.Message, 0, count)
	for i := 1; i <= count; i++ {
		role := "assistant"
		if i%2 == 1 {
			role = "user"
		}
		rows = append(rows, mysqlstore.Message{
			ID:        uint64(i),
			SessionID: sessionID,
			TurnIndex: uint(i),
			Role:      role,
			Content:   fmt.Sprintf("msg-%d", i),
			Model:     "model-a",
		})
	}
	return rows
}

// TODO-117：cancel 走的是「按 id 取一条」，不是「列一页」。原先它先
// ListMessages(…, 1000) 再 mobileFindMessageByID，而 normalizeLimit 把 limit 静默
// 夹到 500，于是在 620 条消息的会话里取消第 600 条 —— 一条**真实存在**的消息 ——
// 会得到 404 message not found。
func TestMobileChatCancelMessageBeyondTheReadClamp(t *testing.T) {
	secret := "secret"
	fake := &fakeTenantService{
		sessions:    []mysqlstore.Session{{ID: 5, SessionKey: "s1", Status: "active"}},
		messageRows: longConversationRows(5, 620),
	}
	handler := NewHandler(Options{MobileJWTSecret: secret, TenantService: fake}, nil)
	req := mobileRequest(t, http.MethodPost, "/mobile/chat/sessions/5/messages/600/cancel", `{}`, secret)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(fake.messages) != 1 || !fake.messages[0].IsError || !strings.Contains(fake.messages[0].ContentJSON, `"status":"cancelled"`) {
		t.Fatalf("messages = %+v", fake.messages)
	}
	// 取消要写回被取消那一条自己的轮次，不能写到别的轮次上去。
	if fake.messages[0].TurnIndex != 600 {
		t.Fatalf("cancel wrote turn %d, want 600", fake.messages[0].TurnIndex)
	}
}

// TODO-117：regenerate 同样是定点查询。它要两条真实存在的消息 —— 目标 assistant
// 和它前面那条 user —— 而在 620 条消息的会话里两条都在夹取之外，于是报 404
// message not found。
//
// 断言 Prompt 是有意的：只修目标那一步的话状态码会变成 200，但提示会静默变成夹取
// 窗口里最后一条 user 消息（第 499 条），也就是拿错的提示重新生成。只看状态码的
// 测试抓不到这一层。
func TestMobileChatRegenerateMessageBeyondTheReadClamp(t *testing.T) {
	secret := "secret"
	fake := &fakeTenantService{
		sessions:    []mysqlstore.Session{{ID: 5, SessionKey: "s1", Status: "active"}},
		messageRows: longConversationRows(5, 620),
	}
	handler := NewHandler(Options{
		MobileJWTSecret: secret,
		TenantService:   fake,
		StreamQueryFunc: func(ctx context.Context, req QueryRequest, textSink io.Writer) (query.Result, error) {
			// 重新生成用的提示必须是第 599 条那条 user 消息，不能是别的。
			if req.Prompt != "msg-599" {
				t.Fatalf("query req = %+v", req)
			}
			_, _ = textSink.Write([]byte("new"))
			return query.Result{Response: "new", Model: "model-a"}, nil
		},
	}, nil)
	req := mobileRequest(t, http.MethodPost, "/mobile/chat/sessions/5/messages/600/regenerate", `{}`, secret)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"delta":"new"`) {
		t.Fatalf("body=%s", rec.Body.String())
	}
	if len(fake.messages) == 0 {
		t.Fatal("no message was written")
	}
	for _, message := range fake.messages {
		if message.TurnIndex <= 620 {
			t.Fatalf("regenerated message reused turn %d, overwriting an existing row", message.TurnIndex)
		}
	}
}

// TODO-119：会话详情求的是「最新 recap」，但读取是 ORDER BY turn_index ASC 且被
// normalizeLimit 夹到 500，所以它求的其实是「最旧 500 条里的最新」—— 长会话里新
// 生成的 recap 根本不出现。
func TestMobileGetSessionReturnsRecapBeyondTheReadClamp(t *testing.T) {
	secret := "secret"
	rows := longConversationRows(5, 620)
	rows[599] = mysqlstore.Message{ID: 600, SessionID: 5, TurnIndex: 600, Role: "recap", Content: "fresh recap", Model: "model-a"}
	fake := &fakeTenantService{
		sessions:    []mysqlstore.Session{{ID: 5, SessionKey: "s1", Status: "active"}},
		messageRows: rows,
	}
	handler := NewHandler(Options{MobileJWTSecret: secret, TenantService: fake}, nil)
	req := mobileRequest(t, http.MethodGet, "/mobile/chat/sessions/5", "", secret)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"content":"fresh recap"`) {
		t.Fatalf("latest recap came from the oldest 500 messages: body=%s", rec.Body.String())
	}
}

// 一个会话里有多条 recap 时，取的必须是轮次最大的那条，而不是最先读到的那条。
func TestMobileGetSessionReturnsTheNewestRecap(t *testing.T) {
	secret := "secret"
	rows := longConversationRows(5, 620)
	rows[549] = mysqlstore.Message{ID: 550, SessionID: 5, TurnIndex: 550, Role: "recap", Content: "stale recap", Model: "model-a"}
	rows[609] = mysqlstore.Message{ID: 610, SessionID: 5, TurnIndex: 610, Role: "recap", Content: "newest recap", Model: "model-a"}
	fake := &fakeTenantService{
		sessions:    []mysqlstore.Session{{ID: 5, SessionKey: "s1", Status: "active"}},
		messageRows: rows,
	}
	handler := NewHandler(Options{MobileJWTSecret: secret, TenantService: fake}, nil)
	req := mobileRequest(t, http.MethodGet, "/mobile/chat/sessions/5", "", secret)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"content":"newest recap"`) {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

// keyedMessage 造一条带 mobile 元数据的消息 —— message_key 埋在 content_json 的
// $.mobile.message_key 下，和 mobileMetadataJSON 的写法一致。
func keyedMessage(id uint64, turn uint, role, content, messageKey, status string) mysqlstore.Message {
	return mysqlstore.Message{
		ID:          id,
		SessionID:   5,
		TurnIndex:   turn,
		Role:        role,
		Content:     content,
		Model:       "model-a",
		ContentJSON: mobileMetadataJSON(mobileClaims{DeviceID: "ios-test"}, messageKey, status),
	}
}

// TODO-118：message_key 的幂等重放。上一次尝试已经写下 user(621)/assistant(622)
// 两条，客户端拿同一个 key 重试时必须命中 622 那条并回放，而不是重新跑一次。
//
// 原先这里是 ListMessages(…, 1000) + mobileFindAssistantByKey，而 normalizeLimit
// 把 limit 静默夹到 500 —— 622 在窗口外，于是重试**找不到**已有记录：重新跑一次
// 查询、重新写一条消息、重新计一次量。这是这一族夹取 bug 里唯一花钱且有副作用的
// 一处，所以断言的是「查询没有被再跑一次、消息没有被再写一次」，不是状态码。
func TestMobileChatStreamReplaysMessageKeyBeyondTheReadClamp(t *testing.T) {
	secret := "secret"
	rows := longConversationRows(5, 620)
	rows = append(rows,
		keyedMessage(621, 621, "user", "hello", "replay-1", mobileMessageCompleted),
		keyedMessage(622, 622, "assistant", "already answered", "replay-1", mobileMessageCompleted),
	)
	fake := &fakeTenantService{
		sessions:    []mysqlstore.Session{{ID: 5, SessionKey: "s1", Status: "active"}},
		messageRows: rows,
	}
	streamCalls := 0
	handler := NewHandler(Options{
		MobileJWTSecret: secret,
		TenantService:   fake,
		StreamQueryFunc: func(ctx context.Context, req QueryRequest, textSink io.Writer) (query.Result, error) {
			streamCalls++
			_, _ = textSink.Write([]byte("charged again"))
			return query.Result{Response: "charged again", Model: "model-a"}, nil
		},
	}, nil)
	req := mobileRequest(t, http.MethodPost, "/mobile/chat/sessions/5/messages/stream", `{"content":"hello","message_key":"replay-1"}`, secret)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	// 重复执行：状态码看不出来，只有调用计数看得出来。
	if streamCalls != 0 {
		t.Fatalf("stream ran %d more times on replay, want 0", streamCalls)
	}
	// 重复写入：重放不得往消息表里再加任何一行。
	if len(fake.messages) != 0 {
		t.Fatalf("replay wrote %d messages, want 0: %+v", len(fake.messages), fake.messages)
	}
	if !strings.Contains(rec.Body.String(), `"delta":"already answered"`) {
		t.Fatalf("replay did not return the existing answer: body=%s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"message_id":622`) {
		t.Fatalf("replay returned the wrong message: body=%s", rec.Body.String())
	}
}

// TODO-118：上一次尝试写下了 user 消息但没写完 assistant（流中断）。重试必须复用
// 那条 user 消息的轮次，不能再写一条 user，也不能把新的 assistant 写到一个已经
// 存在的轮次上。
//
// 后半句是这一处自己的坑：轮次原先取自 mobileMaxTurn(existingMessages)，而
// existingMessages 就是那个被夹到 500 的列表 —— 算出来的「下一轮」是 501，而消息表
// 的唯一键是 (session_id, turn_index) 且写入走 ON DUPLICATE KEY UPDATE，于是新
// assistant 直接**覆盖掉第 501 条已有消息**（TODO-116 的同一族后果）。最大轮次必须
// 问数据库。
func TestMobileChatStreamResumesExistingUserMessageBeyondTheReadClamp(t *testing.T) {
	secret := "secret"
	rows := longConversationRows(5, 620)
	rows = append(rows, keyedMessage(621, 621, "user", "hello", "resume-1", mobileMessageCompleted))
	fake := &fakeTenantService{
		sessions:    []mysqlstore.Session{{ID: 5, SessionKey: "s1", Status: "active"}},
		messageRows: rows,
	}
	streamCalls := 0
	handler := NewHandler(Options{
		MobileJWTSecret: secret,
		TenantService:   fake,
		StreamQueryFunc: func(ctx context.Context, req QueryRequest, textSink io.Writer) (query.Result, error) {
			streamCalls++
			_, _ = textSink.Write([]byte("finished"))
			return query.Result{Response: "finished", Model: "model-a"}, nil
		},
	}, nil)
	req := mobileRequest(t, http.MethodPost, "/mobile/chat/sessions/5/messages/stream", `{"content":"hello","message_key":"resume-1"}`, secret)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	// assistant 没写完，所以这一次**应该**跑一次查询 —— 但只跑一次。
	if streamCalls != 1 {
		t.Fatalf("stream ran %d times, want 1", streamCalls)
	}
	// 不得再写一条 user 消息：那条已经在 621 了。
	for _, message := range fake.messages {
		if message.Role == "user" {
			t.Fatalf("resume wrote a duplicate user message: %+v", message)
		}
	}
	if len(fake.messages) == 0 {
		t.Fatal("no assistant message was written")
	}
	// 新 assistant 必须落在 620/621 之后，不能覆盖任何已有轮次。
	for _, message := range fake.messages {
		if message.TurnIndex <= 621 {
			t.Fatalf("assistant reused turn %d, overwriting an existing row", message.TurnIndex)
		}
	}
}

// TODO-118：regenerate 的幂等重放，与 stream 同一个夹取。前一批的教训是 regenerate
// 有多个查询、只修一个会留下静默错误，所以这一处必须单独有测试。
func TestMobileChatRegenerateReplaysMessageKeyBeyondTheReadClamp(t *testing.T) {
	secret := "secret"
	rows := longConversationRows(5, 620)
	rows = append(rows, keyedMessage(621, 621, "assistant", "already regenerated", "regen-1", mobileMessageCompleted))
	fake := &fakeTenantService{
		sessions:    []mysqlstore.Session{{ID: 5, SessionKey: "s1", Status: "active"}},
		messageRows: rows,
	}
	streamCalls := 0
	handler := NewHandler(Options{
		MobileJWTSecret: secret,
		TenantService:   fake,
		StreamQueryFunc: func(ctx context.Context, req QueryRequest, textSink io.Writer) (query.Result, error) {
			streamCalls++
			_, _ = textSink.Write([]byte("charged again"))
			return query.Result{Response: "charged again", Model: "model-a"}, nil
		},
	}, nil)
	req := mobileRequest(t, http.MethodPost, "/mobile/chat/sessions/5/messages/600/regenerate", `{"message_key":"regen-1"}`, secret)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if streamCalls != 0 {
		t.Fatalf("regenerate ran %d more times on replay, want 0", streamCalls)
	}
	if len(fake.messages) != 0 {
		t.Fatalf("replay wrote %d messages, want 0: %+v", len(fake.messages), fake.messages)
	}
	if !strings.Contains(rec.Body.String(), `"delta":"already regenerated"`) {
		t.Fatalf("replay did not return the existing answer: body=%s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"message_id":621`) {
		t.Fatalf("replay returned the wrong message: body=%s", rec.Body.String())
	}
}

// TODO-118：把 migration 里生成列的取值路径和 Go 这边写入 message_key 的位置钉在一起。
//
// 这是本次改动里唯一一条**全绿也可能已经坏掉**的缝。生成列的路径写在 000010 的 SQL
// 里，message_key 的写入位置在 mobileMetadataJSONWithAttachments 里，两边没有任何共享
// 定义 —— 只是我照着抄了一遍。路径要是写错（漏掉 $.mobile 这一层是最顺手的错法，
// 因为字段本来就叫 message_key），生成列在每一行上都是 NULL，所有幂等查询都漏判，
// 也就是这个 bug 原样回来：而上面那些 handler 测试走的是夹具，sqlmock 只钉 SQL 形状，
// 两边都看不见。
func TestMobileMessageKeyMigrationPathMatchesTheWriter(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "migrations", "mysql", "000010_session_message_mobile_key.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	matches := regexp.MustCompile(`JSON_EXTRACT\(content_json, '([^']+)'\)`).FindStringSubmatch(string(data))
	if matches == nil {
		t.Fatal("migration has no JSON_EXTRACT(content_json, '<path>') expression")
	}
	path := matches[1]

	// 真实写入路径产生的 JSON，不是手写的样本。
	var payload any
	if err := json.Unmarshal([]byte(mobileMetadataJSON(mobileClaims{DeviceID: "ios-test"}, "replay-1", mobileMessageCompleted)), &payload); err != nil {
		t.Fatal(err)
	}
	segments := strings.Split(path, ".")
	if len(segments) == 0 || segments[0] != "$" {
		t.Fatalf("path %q does not start at the document root", path)
	}
	// 照着 MySQL 的 JSON 路径走一遍：走不到就是 SQL NULL，生成列永远为空。
	for _, segment := range segments[1:] {
		object, ok := payload.(map[string]any)
		if !ok {
			t.Fatalf("path %q walks into a non-object at %q", path, segment)
		}
		payload, ok = object[segment]
		if !ok {
			t.Fatalf("path %q resolves to SQL NULL: the writer produces no %q", path, segment)
		}
	}
	if payload != "replay-1" {
		t.Fatalf("path %q resolves to %#v, want the message key", path, payload)
	}
}

// TODO-106：fork 中途失败不得留下一个只复制了一半消息的分支会话。
// 修复前 handler 先 UpsertSession 建会话再逐条 UpsertMessage，第 2 条失败时
// 会话行已经落库，committedSessions 里就会留下那一行。
func TestMobileChatBranchLeavesNoSessionWhenMessageCopyFails(t *testing.T) {
	secret := "secret"
	fake := &fakeTenantService{
		sessionID: 77,
		sessions:  []mysqlstore.Session{{ID: 5, SessionKey: "s1", Title: "Source", Status: "active", Model: "model-a"}},
		messageRows: []mysqlstore.Message{
			{ID: 1, SessionID: 5, TurnIndex: 1, Role: "user", Content: "hello"},
			{ID: 2, SessionID: 5, TurnIndex: 2, Role: "assistant", Content: "answer"},
			{ID: 3, SessionID: 5, TurnIndex: 3, Role: "user", Content: "later"},
		},
		upsertMessageErrAfter: 1,
	}
	handler := NewHandler(Options{MobileJWTSecret: secret, TenantService: fake}, nil)
	req := mobileRequest(t, http.MethodPost, "/mobile/chat/sessions/5/branch", `{"until_turn":3}`, secret)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("expected failure, status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(fake.committedSessions) != 0 {
		t.Fatalf("half-written branch session left behind: %+v", fake.committedSessions)
	}
	if len(fake.messages) != 0 {
		t.Fatalf("half-copied messages left behind: %+v", fake.messages)
	}
}

func TestMobileChatPresignsAttachment(t *testing.T) {
	secret := "secret"
	handler := NewHandler(Options{
		MobileJWTSecret:     secret,
		TenantService:       &fakeTenantService{},
		MobileUploadBaseURL: "https://uploads.example.test/mobile",
		MobilePolicy:        MobilePolicy{AllowedAttachments: []string{"image"}, MaxAttachmentBytes: 1024},
	}, nil)
	req := mobileRequest(t, http.MethodPost, "/mobile/chat/attachments/presign", `{"type":"image","media_type":"image/png","name":"screenshot.png","size_bytes":512,"sha256":"abc"}`, secret)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, body)
	}
	if !strings.Contains(body, `"attachment_id"`) || !strings.Contains(body, `"upload_url":"https://uploads.example.test/mobile/yutang/user-test/`) || !strings.Contains(body, `"type":"image"`) {
		t.Fatalf("body=%s", body)
	}
}

func TestMobileChatPresignsAttachmentWithS3Signer(t *testing.T) {
	secret := "secret"
	signer, err := NewS3MobileUploadSigner(MobileS3UploadConfig{
		Endpoint:      "https://s3.example.test",
		Region:        "us-test-1",
		Bucket:        "mobile-bucket",
		AccessKey:     "test-access",
		SecretKey:     "test-secret",
		Prefix:        "uploads",
		PublicBaseURL: "https://cdn.example.test/mobile",
		UsePathStyle:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(Options{
		MobileJWTSecret:    secret,
		TenantService:      &fakeTenantService{},
		MobileUploadSigner: signer,
		MobilePolicy:       MobilePolicy{AllowedAttachments: []string{"image"}, MaxAttachmentBytes: 1024},
	}, nil)
	req := mobileRequest(t, http.MethodPost, "/mobile/chat/attachments/presign", `{"type":"image","media_type":"image/png","name":"screenshot.png","size_bytes":512,"sha256":"abc"}`, secret)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, body)
	}
	for _, want := range []string{
		`"object_key":"uploads/yutang/user-test/`,
		`"upload_url":"https://s3.example.test/mobile-bucket/uploads/yutang/user-test/`,
		`X-Amz-Signature`,
		`"url":"https://cdn.example.test/mobile/uploads/yutang/user-test/`,
		`"x-amz-meta-sha256":"abc"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in %s", want, body)
		}
	}
}

func TestMobileUsageFileStorePersistsQuota(t *testing.T) {
	path := t.TempDir() + "/usage.json"
	now := func() time.Time { return time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC) }
	policy := mobilePolicyForClaims(MobilePolicy{DailyMessageQuota: 1}, mobileClaims{})
	store := NewMobileUsageFileStore(path, now)
	if err := store.Reserve("tenant:user", policy, 1); err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	reloaded := NewMobileUsageFileStore(path, now)
	if err := reloaded.Reserve("tenant:user", policy, 1); !errors.Is(err, errMobileQuotaExceeded) {
		t.Fatalf("second reserve err = %v", err)
	}
}

func TestRedisMobileUsageStoreEnforcesSharedQuota(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	defer client.Close()
	now := func() time.Time { return time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC) }
	policy := mobilePolicyForClaims(MobilePolicy{RateLimitPerMinute: 2, DailyMessageQuota: 2, DailyTokenQuota: 10}, mobileClaims{})
	storeA := NewRedisMobileUsageStore(client, "test:mobile", now)
	storeB := NewRedisMobileUsageStore(client, "test:mobile", now)
	if err := storeA.Reserve("tenant:user", policy, 3); err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	storeA.AddTokens("tenant:user", 2)
	if err := storeB.Reserve("tenant:user", policy, 3); err != nil {
		t.Fatalf("second reserve: %v", err)
	}
	if err := storeA.Reserve("tenant:user", policy, 1); !errors.Is(err, errMobileRateLimited) {
		t.Fatalf("third reserve err = %v", err)
	}
	quotaPolicy := mobilePolicyForClaims(MobilePolicy{DailyTokenQuota: 5}, mobileClaims{})
	if err := storeB.Reserve("tenant:other", quotaPolicy, 6); !errors.Is(err, errMobileQuotaExceeded) {
		t.Fatalf("token quota err = %v", err)
	}
}

func TestRedisMobileUsageStoreDefaultPrefixRemainsCompatible(t *testing.T) {
	store := NewRedisMobileUsageStore(nil, "", nil)
	if store.keyPrefix != defaultMobileRedisKeyPrefix {
		t.Fatalf("default key prefix = %q, want %q", store.keyPrefix, defaultMobileRedisKeyPrefix)
	}
}

func TestRedisE2EMobileUsageDefaultPrefixUsesCanonicalProductName(t *testing.T) {
	addr := os.Getenv("NIUMA_REDIS_E2E_ADDR")
	if addr == "" {
		t.Skip("set NIUMA_REDIS_E2E_ADDR to run the real Redis namespace check")
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	store := NewRedisMobileUsageStore(client, "", func() time.Time { return now })
	userKey := fmt.Sprintf("identity-e2e:%d", now.UnixNano())
	keys := store.keys(userKey, now)
	t.Cleanup(func() { _ = client.Del(ctx, keys...).Err() })
	policy := mobilePolicyForClaims(MobilePolicy{RateLimitPerMinute: 1, DailyMessageQuota: 1, DailyTokenQuota: 10}, mobileClaims{})
	if err := store.Reserve(userKey, policy, 1); err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		if !strings.HasPrefix(key, "niuma:mobile_usage:") {
			t.Fatalf("Redis key = %q", key)
		}
		if exists, err := client.Exists(ctx, key).Result(); err != nil || exists != 1 {
			t.Fatalf("Redis key %q exists=%d err=%v", key, exists, err)
		}
	}
}

func TestMobileBodySummaryAttrsAreRedacted(t *testing.T) {
	attrs := attrsMap(mobileBodySummaryAttrs([]byte(`{"content":"secret chat text","model":"model-a","attachments":[{"type":"image","url":"https://cdn.example.test/private.png"}]}`), false))
	if attrs["model"] != "model-a" || attrs["attachment_count"] != 1 || attrs["has_content"] != true {
		t.Fatalf("attrs = %+v", attrs)
	}
	if _, ok := attrs["content"]; ok {
		t.Fatalf("content leaked in attrs: %+v", attrs)
	}
	if _, ok := attrs["url"]; ok {
		t.Fatalf("attachment url leaked in attrs: %+v", attrs)
	}
	if chars, ok := attrs["content_chars"].(int); !ok || chars == 0 {
		t.Fatalf("content chars missing: %+v", attrs)
	}
}

func TestMobileAccessLogAttrsIncludeRequestIdentity(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/mobile/chat/sessions/5/messages?after_turn=1", nil)
	c.Params = gin.Params{{Key: "id", Value: "5"}}
	c.Status(http.StatusAccepted)
	now := time.Now()
	attrs := attrsMap(mobileAccessLogFinishAttrs(c, mobileClaims{TenantKey: "yutang", TenantID: 1, UserID: "user-test", DeviceID: "ios-test"}, now, now.Add(25*time.Millisecond)))
	if attrs["tenant_key"] != "yutang" || attrs["tenant_id"] != uint64(1) || attrs["user_id"] != "user-test" || attrs["device_id"] != "ios-test" {
		t.Fatalf("identity attrs = %+v", attrs)
	}
	if attrs["session_id"] != uint64(5) || attrs["status"] != http.StatusAccepted {
		t.Fatalf("request attrs = %+v", attrs)
	}
	if attrs["phase"] != "finish" || attrs["started_at"] == "" || attrs["ended_at"] == "" {
		t.Fatalf("timing attrs = %+v", attrs)
	}
}

func mobileRequest(t *testing.T, method, path, body, secret string) *http.Request {
	t.Helper()
	return mobileRequestWithClaims(t, method, path, body, secret, mobileClaims{TenantKey: "yutang", UserID: "user-test", DeviceID: "ios-test", ExpiresAt: time.Now().Add(time.Hour).Unix()})
}

func mobileRequestWithClaims(t *testing.T, method, path, body, secret string, claims mobileClaims) *http.Request {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Authorization", "Bearer "+mobileJWT(t, secret, claims))
	return req
}

func mobileWebSocketConn(t *testing.T, serverURL, secret string, claims mobileClaims) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(serverURL, "http") + "/mobile/chat/ws"
	origin, err := url.Parse(serverURL)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := websocket.NewConfig(wsURL, origin.String())
	if err != nil {
		t.Fatal(err)
	}
	cfg.Header.Set("Authorization", "Bearer "+mobileJWT(t, secret, claims))
	conn, err := websocket.DialConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

func mobileWSSubscribeSession(t *testing.T, conn *websocket.Conn, sessionID uint64) {
	t.Helper()
	var msg mobileWSMessage
	if err := websocket.JSON.Receive(conn, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Type != "connected" {
		t.Fatalf("connected msg = %+v", msg)
	}
	if err := websocket.JSON.Send(conn, mobileWSMessage{Type: "subscribe", SessionID: sessionID}); err != nil {
		t.Fatal(err)
	}
	if err := websocket.JSON.Receive(conn, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Type != "subscribed" || msg.SessionID != sessionID {
		t.Fatalf("subscribe msg = %+v", msg)
	}
}

func mobileWSReadMessageEvents(t *testing.T, conn *websocket.Conn, count int) map[string]mobileWSMessage {
	t.Helper()
	seen := map[string]mobileWSMessage{}
	for len(seen) < count {
		if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		var msg mobileWSMessage
		if err := websocket.JSON.Receive(conn, &msg); err != nil {
			t.Fatal(err)
		}
		if msg.Type != "message_event" {
			continue
		}
		seen[msg.Event] = msg
	}
	return seen
}

type tenantTitleUpdate struct {
	title string
}

func mobileJWT(t *testing.T, secret string, claims mobileClaims) string {
	t.Helper()
	return mobileJWTWithAlgorithm(t, secret, "HS256", claims)
}

func mobileJWTWithAlgorithm(t *testing.T, secret, algorithm string, claims mobileClaims) string {
	t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": algorithm, "typ": "JWT"})
	payload, _ := json.Marshal(claims)
	head := base64.RawURLEncoding.EncodeToString(header)
	body := base64.RawURLEncoding.EncodeToString(payload)
	input := head + "." + body
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(input))
	return input + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func attrsMap(attrs []any) map[string]any {
	out := make(map[string]any, len(attrs)/2)
	for i := 0; i+1 < len(attrs); i += 2 {
		key, ok := attrs[i].(string)
		if !ok {
			continue
		}
		out[key] = attrs[i+1]
	}
	return out
}

func telemetryEventsContain(events []telemetry.Event, name string) bool {
	for _, event := range events {
		if event.Name == name {
			return true
		}
	}
	return false
}
