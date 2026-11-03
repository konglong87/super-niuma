package server

import (
	"context"
	"encoding/json"
	"github.com/konglong87/super-niuma/internal/agenttasks"
	cu "github.com/konglong87/super-niuma/internal/computeruse"
	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/session"
	"github.com/konglong87/super-niuma/internal/sessioncontrol"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	tenantservice "github.com/konglong87/super-niuma/internal/tenant"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestComputerOwnerResolvesAuthenticatedManagedConversation(t *testing.T) {
	ref := sessioncontrol.SessionRef{Source: sessioncontrol.SourceTenant, Key: "alpha"}
	service := &sessionControlServiceFake{snapshot: sessioncontrol.SessionSnapshot{ID: 93, Ref: ref}}
	handler := NewHandler(Options{AuthToken: "token", SessionControl: service, DesktopComputerOwnerLookup: true, DesktopComputerOwnerResolver: computerTestOwnerResolver}, nil)
	req := httptest.NewRequest(http.MethodGet, "/tenant/session-control/sessions/tenant/alpha/computer-owner", nil)
	req.Header.Set("Authorization", "Bearer token")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	var envelope struct {
		Data cu.SessionOwner `json:"data"`
	}
	if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &envelope) != nil || envelope.Data != (cu.SessionOwner{TenantID: 7, UserID: 11, SessionID: 93}) {
		t.Fatalf("%d %s", res.Code, res.Body.String())
	}
	if service.getRequest.Context.TenantID != 7 || service.getRequest.Context.UserID != 11 || service.getRequest.Ref != ref {
		t.Fatalf("untrusted lookup: %+v", service.getRequest)
	}
	if len(service.calls) != 1 || service.calls[0] != "get" {
		t.Fatal("owner lookup must not create or mutate")
	}
}

func TestComputerOwnerRejectsUntrustedOrUnavailableResolution(t *testing.T) {
	for _, name := range []string{"disabled", "no-token", "bad-token", "origin", "local", "query", "zero-id", "wrong-ref", "readonly", "archived", "foreign", "no-service", "no-actor"} {
		t.Run(name, func(t *testing.T) {
			service := &sessionControlServiceFake{snapshot: sessioncontrol.SessionSnapshot{ID: 93, Ref: sessioncontrol.SessionRef{Source: sessioncontrol.SourceTenant, Key: "alpha"}}}
			tenant := &sessionControlTenantService{fakeTenantService: &fakeTenantService{tenantID: 7, userID: 11}}
			opts := Options{AuthToken: "token", TenantService: tenant, SessionControl: service, DesktopComputerOwnerLookup: true, DesktopComputerOwnerResolver: computerTestOwnerResolver}
			path := "/tenant/session-control/sessions/tenant/alpha/computer-owner"
			switch name {
			case "disabled":
				opts.DesktopComputerOwnerLookup = false
			case "no-token":
				opts.AuthToken = ""
			case "local":
				path = "/tenant/session-control/sessions/local/alpha/computer-owner"
			case "query":
				path += "?tenant_id=999&user_id=999"
			case "zero-id":
				service.snapshot.ID = 0
			case "wrong-ref":
				service.snapshot.Ref.Key = "other"
			case "readonly":
				service.snapshot.ReadOnly = true
			case "archived":
				service.snapshot.Status = sessioncontrol.StatusArchived
			case "foreign":
				service.err = &sessioncontrol.ServiceError{Code: sessioncontrol.CodeForbidden, Message: "denied"}
			case "no-service":
				opts.SessionControl = nil
			case "no-actor":
				opts.DesktopComputerOwnerResolver = nil
			}
			req := sessionControlRequest(http.MethodGet, path, "")
			if name == "bad-token" {
				req.Header.Set("Authorization", "Bearer forged")
			}
			if name == "origin" {
				req.Header.Set("Origin", "http://localhost")
			}
			res := httptest.NewRecorder()
			NewHandler(opts, nil).ServeHTTP(res, req)
			if res.Code < 400 {
				t.Fatalf("accepted: %s", res.Body.String())
			}
		})
	}
}

func computerTestOwnerResolver(ctx context.Context) (context.Context, sessioncontrol.RequestContext, error) {
	return ctx, sessioncontrol.RequestContext{TenantID: 7, UserID: 11, ActorUserID: 11}, nil
}

// Real store composition catches the context-vs-numeric-scope mismatch that
// fake SessionControl.Get cannot: TenantManagedStore resolves identity again.
func TestComputerOwnerRealJSONLCompositionWithoutIdentityHeaders(t *testing.T) {
	const tenantKey, userKey = "computer-owner-test", "desktop-owner"
	ctx := observability.WithRequestValues(context.Background(), "owner-test", userKey, tenantKey)
	repo, err := mysqlstore.OpenSQLiteGormRepository(ctx, filepath.Join(t.TempDir(), "desktop.sqlite"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	tenantID, err := repo.UpsertTenant(ctx, mysqlstore.TenantInput{TenantKey: tenantKey, Name: "Computer test"})
	if err != nil {
		t.Fatal(err)
	}
	userID, err := repo.EnsureUser(ctx, tenantID, userKey)
	if err != nil {
		t.Fatal(err)
	}
	tenantSvc := tenantservice.NewService(repo, nil)
	opts := Options{AuthToken: "test-token", TenantService: tenantSvc, AgentTaskStore: tenantSvc,
		PendingInputQueue: repo, AgentTaskController: agenttasks.NewController(), SessionEvents: NewJSONLSessionEventStore(session.Store{TranscriptProjectsRoot: t.TempDir()}), DesktopComputerOwnerLookup: true}
	opts.SessionControlEvents = NewSessionControlEventReader(opts)
	opts.DesktopComputerOwnerResolver = func(requestCtx context.Context) (context.Context, sessioncontrol.RequestContext, error) {
		bound, resolved, err := tenantSvc.ResolveContextOnce(observability.WithRequestValues(requestCtx, "owner-test", userKey, tenantKey))
		return bound, sessioncontrol.RequestContext{TenantID: resolved.TenantID, UserID: resolved.UserID, ActorUserID: resolved.UserID}, err
	}
	service, err := NewSessionControlService(opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	opts.SessionControl = service
	created, err := service.Create(ctx, sessioncontrol.CreateRequest{Context: sessioncontrol.RequestContext{TenantID: tenantID, UserID: userID, ActorUserID: userID}, SessionKey: "owner-acceptance", IdempotencyKey: "owner-acceptance-create"})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(opts, nil)
	for _, forged := range []bool{false, true} {
		request := httptest.NewRequest(http.MethodGet, "/tenant/session-control/sessions/tenant/owner-acceptance/computer-owner", nil)
		request.Header.Set("Authorization", "Bearer test-token")
		if forged {
			request.Header.Set("X-Tenant-Key", "untrusted")
			request.Header.Set("X-User-Id", "untrusted")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		var envelope struct {
			Data cu.SessionOwner `json:"data"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &envelope) != nil || envelope.Data != (cu.SessionOwner{TenantID: tenantID, UserID: userID, SessionID: created.Session.ID}) {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	}
}
