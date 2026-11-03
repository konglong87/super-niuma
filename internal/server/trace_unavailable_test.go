package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTraceAPITenantSourceWithoutTenantServiceReturnsEmptyList(t *testing.T) {
	handler := NewHandler(Options{AuthToken: "token"}, nil)

	req := httptest.NewRequest(http.MethodGet, "/trace/api/sessions?source=tenant", nil)
	req.Header.Set("authorization", "Bearer token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"data":[]`) || !strings.Contains(rec.Body.String(), "tenant trace source is not configured") {
		t.Fatalf("sessions status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/trace/api/sessions/5?source=tenant", nil)
	req.Header.Set("authorization", "Bearer token")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "tenant trace source is not configured") {
		t.Fatalf("detail status=%d body=%s", rec.Code, rec.Body.String())
	}
}
