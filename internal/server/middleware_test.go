package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTenantEndpointRejectsWhenTenantServiceNil(t *testing.T) {
	called := false
	h := tenantEndpoint(Options{AuthToken: ""}, nil, func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/tenant/tenants", nil))
	if called {
		t.Fatal("next handler should not run when TenantService is nil")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestTenantEndpointRejectsBadToken(t *testing.T) {
	called := false
	h := tenantEndpoint(Options{AuthToken: "secret"}, nil, func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/tenant/tenants", nil))
	if called {
		t.Fatal("next handler should not run without valid token")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}
