package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFreeModelsRoute(t *testing.T) {
	router := newRouter(Options{AuthToken: "tok"}, nil)

	unauthorized := httptest.NewRecorder()
	router.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/runtime/free-models", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 without token, got %d", unauthorized.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/runtime/free-models", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body)
	}

	var raw struct {
		SchemaVersion int              `json:"schema_version"`
		Providers     []map[string]any `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if raw.SchemaVersion != 1 || len(raw.Providers) != 7 {
		t.Fatalf("schema %d, %d providers", raw.SchemaVersion, len(raw.Providers))
	}
	for _, p := range raw.Providers {
		for _, field := range []string{"slug", "name", "base_url", "openai_compatible", "free", "requirements",
			"signup_url", "status", "verified_at", "sources", "recommended_model", "models", "usage_notes", "unverified"} {
			if _, ok := p[field]; !ok {
				t.Errorf("%v: missing field %s", p["slug"], field)
			}
		}
	}
}

func TestFreeModelsUnverifiedMatchesRawFields(t *testing.T) {
	rec := httptest.NewRecorder()
	newRouter(Options{}, nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/runtime/free-models", nil))
	var resp FreeModelsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	for _, p := range resp.Providers {
		want := p.Status != "ok" || p.OpenAICompatible != "supported" ||
			p.RecommendedModel == nil || p.RecommendedModel.ToolCalling != "supported"
		if p.Unverified != want {
			t.Errorf("%s: unverified=%v want %v", p.Slug, p.Unverified, want)
		}
	}
}

func TestFreeModelsResponseHasNoSecrets(t *testing.T) {
	rec := httptest.NewRecorder()
	newRouter(Options{}, nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/runtime/free-models", nil))
	body := strings.ToLower(rec.Body.String())
	for _, needle := range []string{"api_key", "apikey", "secret", "sk-"} {
		if strings.Contains(body, needle) {
			t.Errorf("catalog response contains %q", needle)
		}
	}
}
