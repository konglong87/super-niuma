package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

func computerOwnerTestServer(t *testing.T, handler http.HandlerFunc) int {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	_, port, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	n, _ := strconv.Atoi(port)
	return n
}
func TestComputerConversationResolutionUsesPrivateServer(t *testing.T) {
	calls := 0
	port := computerOwnerTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/tenant/session-control/sessions/tenant/alpha/computer-owner" || r.Header.Get("Authorization") != "Bearer private-test-token" {
			t.Error("wrong resolution request")
		}
		fmt.Fprint(w, `{"data":{"tenant_id":7,"user_id":11,"session_id":93}}`)
	})
	owner, err := resolveComputerConversation(context.Background(), port, "private-test-token", "tenant:alpha")
	if err != nil || owner != (cu.SessionOwner{TenantID: 7, UserID: 11, SessionID: 93}) {
		t.Fatal(owner, err)
	}
	for _, ref := range []string{"", "local:alpha", "tenant:../alpha", "tenant:alpha/../../x"} {
		if _, err := resolveComputerConversation(context.Background(), port, "private-test-token", ref); err == nil {
			t.Fatal("invalid ref accepted", ref)
		}
	}
	if calls != 1 {
		t.Fatal("untrusted refs reached server")
	}
}
func TestComputerConversationResolutionFailsClosed(t *testing.T) {
	for _, kind := range []string{"denied", "redirect", "malformed", "zero", "negative", "too-large", "trailing"} {
		t.Run(kind, func(t *testing.T) {
			port := computerOwnerTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				switch kind {
				case "denied":
					http.Error(w, "private diagnostic", http.StatusForbidden)
				case "redirect":
					http.Redirect(w, r, "http://127.0.0.1:1/token-leak", http.StatusFound)
				case "malformed":
					fmt.Fprint(w, "private diagnostic")
				case "zero":
					fmt.Fprint(w, `{"data":{"tenant_id":7,"user_id":11,"session_id":0}}`)
				case "negative":
					fmt.Fprint(w, `{"data":{"tenant_id":7,"user_id":11,"session_id":18446744073709551615}}`)
				case "too-large":
					fmt.Fprint(w, strings.Repeat(" ", computerOwnerMaxResponseBytes+1))
				case "trailing":
					fmt.Fprint(w, `{"data":{"tenant_id":7,"user_id":11,"session_id":93}} {}`)
				}
			})
			if _, err := resolveComputerConversation(context.Background(), port, "test", "tenant:alpha"); err != errComputerConversation {
				t.Fatal(err)
			}
		})
	}
}
func TestComputerConversationRequiresStopBeforeRebinding(t *testing.T) {
	ctx := context.Background()
	m, _ := testComputerManager()
	t.Cleanup(func() { _ = m.close(ctx) })
	local, err := m.start(ctx, ComputerSessionStartInput{Approved: true})
	if err != nil {
		t.Fatal(err)
	}
	owner := cu.SessionOwner{TenantID: 7, UserID: 11, SessionID: 93}
	start := func(o cu.SessionOwner) (ComputerSessionDTO, error) {
		return m.startOwnedWithLifetime(ctx, ctx, ComputerSessionStartInput{Approved: true}, o)
	}
	if _, err := start(owner); err == nil {
		t.Fatal("upgraded local approval")
	}
	if _, err := m.control(ctx, local.ID, cu.ActionStop); err != nil {
		t.Fatal(err)
	}
	approved, err := start(owner)
	if err != nil {
		t.Fatal(err)
	}
	service := computerAgentService{manager: m}
	if id, err := service.Lookup(ctx, owner); err != nil || id != approved.ID {
		t.Fatal(id, err)
	}
	foreign := owner
	foreign.SessionID++
	if _, err := start(foreign); err == nil {
		t.Fatal("reassigned active approval")
	}
	if _, err := m.start(ctx, ComputerSessionStartInput{Approved: true}); err == nil {
		t.Fatal("exposed conversation session as local preview")
	}
	if _, err := m.control(ctx, approved.ID, cu.ActionPause); err != nil {
		t.Fatal(err)
	}
	if _, err := m.control(ctx, approved.ID, cu.ActionResume); err != nil {
		t.Fatal(err)
	}
	if _, err := m.control(ctx, approved.ID, cu.ActionStop); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Lookup(ctx, owner); err == nil {
		t.Fatal("stop did not revoke")
	}
	next, err := start(foreign)
	if err != nil || next.ID == approved.ID {
		t.Fatal(next, err)
	}
	if _, err := service.Lookup(ctx, owner); err == nil {
		t.Fatal("old conversation retained grant")
	}
}
func TestStartComputerSessionResolvesOnlyAfterExplicitApproval(t *testing.T) {
	calls := 0
	port := computerOwnerTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, `{"data":{"tenant_id":7,"user_id":11,"session_id":93}}`)
	})
	m, _ := testComputerManager()
	t.Cleanup(func() { _ = m.close(context.Background()) })
	a := &app{port: port, token: "test", windowCtx: context.Background(), computerManager: m}
	if _, err := a.StartComputerSession(ComputerSessionStartInput{ConversationRef: "tenant:alpha"}); err == nil || calls != 0 {
		t.Fatal("approval not required")
	}
	started, err := a.StartComputerSession(ComputerSessionStartInput{Approved: true, ConversationRef: "tenant:alpha"})
	if err != nil || calls != 1 {
		t.Fatal(started, err)
	}
	if got := m.controller.Session().Owner(); got != (cu.SessionOwner{TenantID: 7, UserID: 11, SessionID: 93}) {
		t.Fatal(got)
	}
}
