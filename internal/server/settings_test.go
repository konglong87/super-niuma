package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/query"
)

func settingsTestHandler(t *testing.T, authToken string) (http.Handler, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", dir)
	handler := NewHandler(Options{AuthToken: authToken}, func(context.Context, QueryRequest) (query.Result, error) {
		return query.Result{}, nil
	})
	return handler, filepath.Join(dir, "settings.json")
}

func doSettings(t *testing.T, handler http.Handler, method, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, "/runtime/settings", reader)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func decodeSettingsResponse(t *testing.T, rec *httptest.ResponseRecorder) GlobalSettingsResponse {
	t.Helper()
	var resp GlobalSettingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, rec.Body.String())
	}
	return resp
}

func TestGlobalSettingsGetMasksSecretsAndReadsGlobalFileOnly(t *testing.T) {
	handler, path := settingsTestHandler(t, "")
	if err := os.WriteFile(path, []byte(`{"model":"m1","env":{"ANTHROPIC_API_KEY":"sk-secret","FOO":"bar"}}`), 0600); err != nil {
		t.Fatal(err)
	}

	rec := doSettings(t, handler, http.MethodGet, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	resp := decodeSettingsResponse(t, rec)
	if !resp.Exists || resp.Path != path {
		t.Fatalf("exists=%v path=%q want path=%q", resp.Exists, resp.Path, path)
	}
	if resp.Doc["model"] != "m1" {
		t.Fatalf("model = %v", resp.Doc["model"])
	}
	env, _ := resp.Doc["env"].(map[string]any)
	if env["ANTHROPIC_API_KEY"] != settingsSecretSentinel {
		t.Fatalf("secret not masked: %v", env["ANTHROPIC_API_KEY"])
	}
	if env["FOO"] != "bar" {
		t.Fatalf("non-secret altered: %v", env["FOO"])
	}
	found := false
	for _, m := range resp.Masked {
		if m == "env.ANTHROPIC_API_KEY" {
			found = true
		}
	}
	if !found {
		t.Fatalf("masked list missing env.ANTHROPIC_API_KEY: %v", resp.Masked)
	}
}

func TestGlobalSettingsGetMissingFile(t *testing.T) {
	handler, _ := settingsTestHandler(t, "")
	rec := doSettings(t, handler, http.MethodGet, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	resp := decodeSettingsResponse(t, rec)
	if resp.Exists {
		t.Fatalf("exists should be false for a missing file")
	}
	if resp.Doc == nil {
		t.Fatalf("doc should be an empty object, not null")
	}
}

func TestGlobalSettingsPutPreservesUnknownFields(t *testing.T) {
	handler, path := settingsTestHandler(t, "")
	rec := doSettings(t, handler, http.MethodPut, "", `{"model":"x","futureField":{"nested":true},"count":3}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	future, ok := doc["futureField"].(map[string]any)
	if !ok || future["nested"] != true {
		t.Fatalf("unknown field not preserved: %v", doc["futureField"])
	}
	if doc["model"] != "x" {
		t.Fatalf("model not saved: %v", doc["model"])
	}
}

func TestGlobalSettingsPutPreservesOmittedUnknownFieldsOnPartialWrite(t *testing.T) {
	handler, path := settingsTestHandler(t, "")
	original := `{"model":"old","futureField":{"nested":true},"futureList":[{"value":1,"unknown":"keep"}]}`
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	rec := doSettings(t, handler, http.MethodPut, "", `{"model":"new"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var stored map[string]any
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	if stored["model"] != "new" {
		t.Fatalf("model = %v, want new", stored["model"])
	}
	if stored["futureField"].(map[string]any)["nested"] != true {
		t.Fatalf("omitted unknown object was lost: %v", stored["futureField"])
	}
	item := stored["futureList"].([]any)[0].(map[string]any)
	if item["unknown"] != "keep" {
		t.Fatalf("omitted unknown list field was lost: %v", item)
	}
}

func TestGlobalSettingsPutRejectsInvalidJSON(t *testing.T) {
	handler, path := settingsTestHandler(t, "")
	original := `{"model":"keep-me"}`
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	rec := doSettings(t, handler, http.MethodPut, "", `{"model": bad}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	data, _ := os.ReadFile(path)
	if string(data) != original {
		t.Fatalf("file was modified on invalid input: %s", data)
	}
}

func TestGlobalSettingsPutRejectsWrongType(t *testing.T) {
	handler, _ := settingsTestHandler(t, "")
	rec := doSettings(t, handler, http.MethodPut, "", `{"contextLength":"not-a-number"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestGlobalSettingsPutRestoresMaskedSecret(t *testing.T) {
	handler, path := settingsTestHandler(t, "")
	if err := os.WriteFile(path, []byte(`{"model":"old","env":{"ANTHROPIC_API_KEY":"sk-original"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	// Client echoes the masked sentinel back while changing the model.
	body := `{"model":"new","env":{"ANTHROPIC_API_KEY":"` + settingsSecretSentinel + `"}}`
	rec := doSettings(t, handler, http.MethodPut, "", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	data, _ := os.ReadFile(path)
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	env := doc["env"].(map[string]any)
	if env["ANTHROPIC_API_KEY"] != "sk-original" {
		t.Fatalf("secret not restored: %v", env["ANTHROPIC_API_KEY"])
	}
	if doc["model"] != "new" {
		t.Fatalf("model not updated: %v", doc["model"])
	}
}

func TestGlobalSettingsPutMaskedWithoutStoredValueFails(t *testing.T) {
	handler, _ := settingsTestHandler(t, "")
	body := `{"env":{"NEW_TOKEN":"` + settingsSecretSentinel + `"}}`
	rec := doSettings(t, handler, http.MethodPut, "", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestGlobalSettingsRequiresAuth(t *testing.T) {
	handler, _ := settingsTestHandler(t, "secret-token")

	if rec := doSettings(t, handler, http.MethodGet, "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: status = %d, want 401", rec.Code)
	}
	if rec := doSettings(t, handler, http.MethodGet, "wrong", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: status = %d, want 401", rec.Code)
	}
	if rec := doSettings(t, handler, http.MethodGet, "secret-token", ""); rec.Code != http.StatusOK {
		t.Fatalf("valid token: status = %d, want 200", rec.Code)
	}
}

func TestGlobalSettingsPutCreatesFileWith0600(t *testing.T) {
	handler, path := settingsTestHandler(t, "")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("precondition: file should not exist yet")
	}
	rec := doSettings(t, handler, http.MethodPut, "", `{"model":"created"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("perm = %o, want 600", perm)
	}
}
