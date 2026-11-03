package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmbeddedConsoleAndSecurityBoundaries(t *testing.T) {
	handler := protectHost("127.0.0.1:7391", securityHeaders(staticHandler()))
	for _, test := range []struct {
		path, method, host string
		code               int
	}{
		{"/", "GET", "127.0.0.1:7391", 200}, {"/assets/app.js", "GET", "127.0.0.1:7391", 200},
		{"/assets/app.css", "HEAD", "127.0.0.1:7391", 200}, {"/ui/", "GET", "127.0.0.1:7391", 404},
		{"/", "POST", "127.0.0.1:7391", 405}, {"/", "GET", "attacker.example", 403},
	} {
		request := httptest.NewRequest(test.method, "http://"+test.host+test.path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.code {
			t.Fatalf("%s %s: %d", test.method, test.path, response.Code)
		}
		if test.code == 200 && !strings.Contains(response.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
			t.Fatal("missing CSP")
		}
	}
}

func TestRoutingNeverCleansOrRedirectsAmbiguousAPIPaths(t *testing.T) {
	for _, path := range []string{"/api/super/v1//status", "/api/super/v1/../status", "/api/super/v1/status/"} {
		called := false
		api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			if r.URL.Path != path {
				t.Fatalf("path rewritten: %q", r.URL.Path)
			}
			http.NotFound(w, r)
		})
		response := httptest.NewRecorder()
		routeHandler(api, staticHandler()).ServeHTTP(response, httptest.NewRequest("GET", "http://127.0.0.1:7391"+path, nil))
		if !called || response.Code != http.StatusNotFound || response.Header().Get("Location") != "" {
			t.Fatalf("ambiguous API alias was normalized: path=%q status=%d called=%v", path, response.Code, called)
		}
	}
}
