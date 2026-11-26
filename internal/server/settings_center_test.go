package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/konglong87/super-niuma/internal/query"
)

func settingsRequest(handler http.Handler, method, path, body, token, revision string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if revision != "" {
		req.Header.Set("If-Match", revision)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestSettingsValidateRejectsInvalidRoutesWithoutWriting(t *testing.T) {
	handler, path := settingsTestHandler(t, "")
	for _, body := range []string{
		`{"provider":"invalid"}`,
		`{"provider":"anthropic","providerProtocol":"openai-responses"}`,
		`{"provider":"openai","responses":{"store":true}}`,
		`{"provider":"openai","providerProtocol":"openai-responses","responses":{"stateMode":"previous-response-id"}}`,
		`{"fallback":{"providers":[{"name":"a","type":"openai","protocol":"anthropic-messages"}]}}`,
		`{"fallback":{"providers":[{"name":"a"},{"name":"a"}]}}`,
		`{"contextLength":"bad"}`,
	} {
		rec := settingsRequest(handler, http.MethodPost, "/runtime/settings/validate", body, "", "")
		var result SettingsValidationResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || rec.Code != http.StatusOK || result.Valid || len(result.Issues) == 0 {
			t.Fatalf("body=%s status=%d result=%s", body, rec.Code, rec.Body.String())
		}
		if rec := doSettings(t, handler, http.MethodPut, "", body); rec.Code != http.StatusBadRequest {
			t.Fatalf("save accepted invalid body: %s", body)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("validation wrote settings: %v", err)
	}
	for _, body := range []string{`null`, `[]`, `{} {}`} {
		if rec := settingsRequest(handler, http.MethodPost, "/runtime/settings/validate", body, "", ""); rec.Code != http.StatusBadRequest {
			t.Fatalf("accepted non-object %s: %d", body, rec.Code)
		}
	}
}

func TestSettingsRevisionConcurrentSaveAndReadback(t *testing.T) {
	handler, _ := settingsTestHandler(t, "")
	rec := doSettings(t, handler, http.MethodGet, "", "")
	revision := decodeSettingsResponse(t, rec).Revision
	if revision == "" || rec.Header().Get("ETag") != `"`+revision+`"` {
		t.Fatal("missing ETag revision")
	}
	var wg sync.WaitGroup
	results := make(chan *httptest.ResponseRecorder, 2)
	for _, model := range []string{"a", "b"} {
		wg.Add(1)
		go func(model string) {
			defer wg.Done()
			results <- settingsRequest(handler, http.MethodPut, "/runtime/settings", `{"model":"`+model+`"}`, "", revision)
		}(model)
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	var saved GlobalSettingsSaveResponse
	for rec := range results {
		switch rec.Code {
		case http.StatusOK:
			success++
			if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil {
				t.Fatal(err)
			}
		case http.StatusConflict:
			conflict++
		default:
			t.Fatalf("unexpected status %d: %s", rec.Code, rec.Body.String())
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success=%d conflict=%d", success, conflict)
	}
	readback := decodeSettingsResponse(t, doSettings(t, handler, http.MethodGet, "", ""))
	if saved.Revision != readback.Revision || !saved.RequiresRestart {
		t.Fatalf("readback=%+v save=%+v", readback, saved)
	}
	if rec := settingsRequest(handler, http.MethodPut, "/runtime/settings", `{"model":"next"}`, "", `"`+saved.Revision+`"`); rec.Code != http.StatusOK {
		t.Fatalf("quoted ETag rejected: %s", rec.Body.String())
	}
}

func TestSettingsRestoreProviderSecretsByNameAfterReorder(t *testing.T) {
	handler, path := settingsTestHandler(t, "")
	original := `{"fallback":{"providers":[{"name":"a","apiKey":"secret-a"},{"name":"b","apiKey":"secret-b"}]},"mcpServers":{"x":{"headers":{"Authorization":"private"}}}}`
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	readback := decodeSettingsResponse(t, doSettings(t, handler, http.MethodGet, "", ""))
	if !strings.Contains(strings.Join(readback.Masked, ","), "fallback.providers.0.apiKey") {
		t.Fatalf("missing indexed path: %v", readback.Masked)
	}
	providers := readback.Doc["fallback"].(map[string]any)["providers"].([]any)
	providers[0], providers[1] = providers[1], providers[0]
	body, _ := json.Marshal(readback.Doc)
	if rec := doSettings(t, handler, http.MethodPut, "", string(body)); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	raw, _ := os.ReadFile(path)
	var stored map[string]any
	_ = json.Unmarshal(raw, &stored)
	first := stored["fallback"].(map[string]any)["providers"].([]any)[0].(map[string]any)
	if first["name"] != "b" || first["apiKey"] != "secret-b" {
		t.Fatalf("provider secret moved: %v", first)
	}
	providers[0].(map[string]any)["name"] = "renamed"
	body, _ = json.Marshal(readback.Doc)
	if rec := doSettings(t, handler, http.MethodPut, "", string(body)); rec.Code != 400 {
		t.Fatalf("renamed masked provider accepted: %s", rec.Body.String())
	}
}

func TestSettingsEffectiveUsesGlobalSourceAndStartupAllowlist(t *testing.T) {
	dir, workspace := t.TempDir(), t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"provider":"anthropic","model":"global","baseURL":"https://example.test/v1","apiKey":"private"}`), 0600); err != nil {
		t.Fatal(err)
	}
	projectDir := filepath.Join(workspace, ".niuma")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	projectPath := filepath.Join(projectDir, "settings.json")
	if err := os.WriteFile(projectPath, []byte(`{"provider":"openai","model":"workspace","providerProtocol":"openai-responses"}`), 0600); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(Options{Workspace: workspace, ProcessSettingsSnapshot: map[string]any{"model": "startup", "apiKey": "do-not-leak", "baseURL": "https://name:password@example.com/v1?token=private"}}, func(context.Context, QueryRequest) (query.Result, error) { return query.Result{}, nil })
	rec := settingsRequest(handler, http.MethodGet, "/runtime/settings/effective", "", "", "")
	var result EffectiveSettingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.FileResolved.Doc["model"] != "global" || result.FileResolved.RouteSources["model"] != filepath.Join(dir, "settings.json") {
		t.Fatalf("wrong file sources: %+v", result.FileResolved)
	}
	if result.FileResolved.RouteSources["apiKey"] != filepath.Join(dir, "settings.json") {
		t.Fatal("global credential source lost")
	}
	if result.ProcessSnapshot.Kind != "startup" || result.ProcessSnapshot.Doc["model"] != "startup" || result.ProcessSnapshot.ActiveRunConfigKnown {
		t.Fatalf("wrong snapshot: %+v", result.ProcessSnapshot)
	}
	if strings.Contains(rec.Body.String(), "private") || strings.Contains(rec.Body.String(), "do-not-leak") || strings.Contains(rec.Body.String(), "password") {
		t.Fatalf("snapshot leaked credentials: %s", rec.Body.String())
	}
}

func TestSettingsUnnamedProviderCredentialsFollowUniqueRouteIdentity(t *testing.T) {
	for _, operation := range []string{"reorder", "delete", "change-route", "ambiguous"} {
		t.Run(operation, func(t *testing.T) {
			handler, path := settingsTestHandler(t, "")
			original := `{"fallback":{"providers":[{"type":"custom","baseURL":"https://first.example/v1","model":"first","apiKey":"secret-first"},{"type":"custom","baseURL":"https://second.example/v1","model":"second","apiKey":"secret-second"}]}}`
			if operation == "ambiguous" {
				original = `{"fallback":{"providers":[{"type":"custom","model":"same","apiKey":"first"},{"type":"custom","model":"same","apiKey":"second"}]}}`
			}
			if err := os.WriteFile(path, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			doc := decodeSettingsResponse(t, doSettings(t, handler, http.MethodGet, "", "")).Doc
			fallback := doc["fallback"].(map[string]any)
			providers := fallback["providers"].([]any)
			switch operation {
			case "reorder":
				providers[0], providers[1] = providers[1], providers[0]
			case "delete":
				fallback["providers"] = providers[1:]
			case "change-route":
				providers[0].(map[string]any)["baseURL"] = "https://replacement.example/v1"
			case "ambiguous":
				fallback["providers"] = providers[1:]
			}
			body, _ := json.Marshal(doc)
			rec := doSettings(t, handler, http.MethodPut, "", string(body))
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if operation == "change-route" || operation == "ambiguous" {
				if rec.Code != http.StatusBadRequest || string(raw) != original {
					t.Fatalf("unsafe restore accepted: status=%d body=%s", rec.Code, rec.Body.String())
				}
				return
			}
			if rec.Code != http.StatusOK {
				t.Fatalf("safe reorder rejected: %s", rec.Body.String())
			}
			var stored map[string]any
			if err := json.Unmarshal(raw, &stored); err != nil {
				t.Fatal(err)
			}
			first := stored["fallback"].(map[string]any)["providers"].([]any)[0].(map[string]any)
			if first["model"] != "second" || first["apiKey"] != "secret-second" {
				t.Fatalf("credentials transferred: %v", first)
			}
		})
	}
}

func TestSettingsEffectiveMasksFileURLsAndSanitizesStartupURL(t *testing.T) {
	dir, workspace := t.TempDir(), t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", dir)
	settings := `{"baseURL":"https://user:password@example.com/v1?token=secret-query","fallback":{"providers":[{"name":"custom","baseURL":"https://other.example/v1?api_key=another-secret"}]}}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(settings), 0600); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(Options{Workspace: workspace, ProcessSettingsSnapshot: map[string]any{"baseURL": "https://user:startup-password@example.com/v1?key=startup-query#private"}}, func(context.Context, QueryRequest) (query.Result, error) { return query.Result{}, nil })
	rec := settingsRequest(handler, http.MethodGet, "/runtime/settings/effective", "", "", "")
	var result EffectiveSettingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"password", "secret-query", "another-secret", "startup-query", "private"} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Fatalf("effective response leaked %s", secret)
		}
	}
	if result.ProcessSnapshot.Doc["baseURL"] != "https://example.com/v1" {
		t.Fatalf("startup URL not sanitized: %v", result.ProcessSnapshot.Doc["baseURL"])
	}
	if result.FileResolved.Doc["baseURL"] != settingsSecretSentinel {
		t.Fatal("file URL was not masked")
	}
}

func TestSettingsValidationIgnoresProcessProviderEnvironment(t *testing.T) {
	handler, _ := settingsTestHandler(t, "")
	t.Setenv("NIUMA_PROVIDER", "custom")
	t.Setenv("CLAUDE_CODE_PROVIDER", "")
	body := `{"provider":"custom","baseURL":"https://example.test/v1","apiKey":"key","providerProtocol":"openai-responses","responses":{"stateMode":"stateless","store":false}}`
	rec := settingsRequest(handler, http.MethodPost, "/runtime/settings/validate", body, "", "")
	var result SettingsValidationResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || !result.Valid {
		t.Fatalf("valid canonical route rejected: %s", rec.Body.String())
	}
	if rec := doSettings(t, handler, http.MethodPut, "", body); rec.Code != http.StatusOK {
		t.Fatalf("valid canonical route could not save: %s", rec.Body.String())
	}
	t.Setenv("NIUMA_PROVIDER", "anthropic")
	if rec := doSettings(t, handler, http.MethodPut, "", body); rec.Code != http.StatusOK {
		t.Fatalf("process provider environment changed canonical route: %s", rec.Body.String())
	}
}

func TestSettingsMaskMalformedURLsAndURLFragments(t *testing.T) {
	doc := map[string]any{"baseURL": "https://user:bad%zz-password@example.com/v1?token=secret", "endpoint": "https://example.com/v1#fragment-secret"}
	masked := []string{}
	maskSecrets(doc, "", &masked)
	if doc["baseURL"] != settingsSecretSentinel || doc["endpoint"] != settingsSecretSentinel {
		t.Fatalf("URL credentials not masked: %v", masked)
	}
}

func TestSettingsNamedProviderRestoreUsesUniqueTrimmedName(t *testing.T) {
	draft := func() map[string]any {
		return map[string]any{"fallback": map[string]any{"providers": []any{map[string]any{"name": "route", "apiKey": settingsSecretSentinel}}}}
	}
	stored := map[string]any{"fallback": map[string]any{"providers": []any{map[string]any{"name": " route ", "apiKey": "stored"}}}}
	doc := draft()
	if err := restoreSecrets(doc, stored); err != nil {
		t.Fatalf("trimmed stable name rejected: %v", err)
	}
	if got := doc["fallback"].(map[string]any)["providers"].([]any)[0].(map[string]any)["apiKey"]; got != "stored" {
		t.Fatalf("credential not restored: %v", got)
	}
	stored["fallback"].(map[string]any)["providers"] = []any{map[string]any{"name": "route", "apiKey": "first"}, map[string]any{"name": " route ", "apiKey": "second"}}
	if err := restoreSecrets(draft(), stored); err == nil {
		t.Fatal("ambiguous stored provider names accepted")
	}
}

func TestSettingsCenterRequiresAdminToken(t *testing.T) {
	handler, _ := settingsTestHandler(t, "admin")
	for _, route := range []struct{ method, path, body string }{{"GET", "/runtime/settings/effective", ""}, {"POST", "/runtime/settings/validate", "{}"}, {"POST", "/runtime/settings/promote-provider", `{"doc":{},"provider_index":0}`}, {"POST", "/runtime/settings/test-provider", `{"doc":{}}`}} {
		for _, token := range []string{"", "mobile-jwt", "wrong"} {
			if rec := settingsRequest(handler, route.method, route.path, route.body, token, ""); rec.Code != http.StatusUnauthorized {
				t.Fatalf("%s accepted token %q", route.path, token)
			}
		}
	}
}

func TestSettingsPromoteProviderReturnsSafeDraftWithoutSaving(t *testing.T) {
	handler, path := settingsTestHandler(t, "")
	stored := `{"provider":"openai","providerProtocol":"openai-responses","baseURL":"https://primary.example/v1","apiKey":"primary-key","model":"primary-model","responses":{"stateMode":"stateless","store":false},"fallback":{"enabled":true,"providers":[{"name":"glm","type":"openai-compatible","protocol":"openai-chat-completions","baseURL":"https://glm.example/v1","apiKey":"glm-key","model":"glm-5.2","future":{"keep":true}}]},"futureRoot":{"keep":"yes"}}`
	if err := os.WriteFile(path, []byte(stored), 0600); err != nil {
		t.Fatal(err)
	}
	doc := decodeSettingsResponse(t, doSettings(t, handler, http.MethodGet, "", "")).Doc
	body, err := json.Marshal(GlobalSettingsPromoteRequest{Doc: doc, ProviderIndex: 0})
	if err != nil {
		t.Fatal(err)
	}
	rec := settingsRequest(handler, http.MethodPost, "/runtime/settings/promote-provider", string(body), "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var result GlobalSettingsPromoteResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Doc["provider"] != "openai-compatible" || result.Doc["providerProtocol"] != "openai-chat-completions" || result.Doc["model"] != "glm-5.2" {
		t.Fatalf("primary route was not promoted: %#v", result.Doc)
	}
	if result.Doc["apiKey"] != settingsSecretSentinel {
		t.Fatalf("promoted credential was not masked: %v", result.Doc["apiKey"])
	}
	if result.Doc["futureRoot"].(map[string]any)["keep"] != "yes" {
		t.Fatalf("unknown root field was lost: %#v", result.Doc["futureRoot"])
	}
	providers := result.Doc["fallback"].(map[string]any)["providers"].([]any)
	provider := providers[0].(map[string]any)
	if provider["future"].(map[string]any)["keep"] != true || provider["apiKey"] != settingsSecretSentinel {
		t.Fatalf("fallback provider was changed incorrectly: %#v", provider)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != stored {
		t.Fatalf("promotion wrote the settings file: %s", data)
	}
}

func TestSettingsPromoteProviderKeepsInheritedPrimaryCredential(t *testing.T) {
	handler, path := settingsTestHandler(t, "")
	stored := `{"provider":"openai","apiKey":"primary-key","baseURL":"https://primary.example/v1","model":"primary-model","fallback":{"providers":[{"name":"inherited","type":"openai","baseURL":"https://backup.example/v1","model":"backup-model"}]}}`
	if err := os.WriteFile(path, []byte(stored), 0600); err != nil {
		t.Fatal(err)
	}
	doc := decodeSettingsResponse(t, doSettings(t, handler, http.MethodGet, "", "")).Doc
	body, _ := json.Marshal(GlobalSettingsPromoteRequest{Doc: doc, ProviderIndex: 0})
	rec := settingsRequest(handler, http.MethodPost, "/runtime/settings/promote-provider", string(body), "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var result GlobalSettingsPromoteResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Doc["apiKey"] != settingsSecretSentinel || result.Doc["model"] != "backup-model" {
		t.Fatalf("inherited credential or model was not handled: %#v", result.Doc)
	}
}

func TestSettingsPromoteProviderSaveRestoresPromotedCredential(t *testing.T) {
	handler, path := settingsTestHandler(t, "")
	stored := `{"provider":"openai","baseURL":"https://primary.example/v1","model":"primary-model","fallback":{"providers":[{"name":"glm","type":"openai-compatible","baseURL":"https://glm.example/v1","apiKey":"glm-key","model":"glm-5.2"}]}}`
	if err := os.WriteFile(path, []byte(stored), 0600); err != nil {
		t.Fatal(err)
	}
	doc := decodeSettingsResponse(t, doSettings(t, handler, http.MethodGet, "", "")).Doc
	promoteBody, _ := json.Marshal(GlobalSettingsPromoteRequest{Doc: doc, ProviderIndex: 0})
	promoteRec := settingsRequest(handler, http.MethodPost, "/runtime/settings/promote-provider", string(promoteBody), "", "")
	var promoted GlobalSettingsPromoteResponse
	if err := json.Unmarshal(promoteRec.Body.Bytes(), &promoted); err != nil || promoteRec.Code != http.StatusOK {
		t.Fatalf("promotion=%s err=%v", promoteRec.Body.String(), err)
	}
	validateBody, _ := json.Marshal(promoted.Doc)
	validateReq := httptest.NewRequest(http.MethodPost, "/runtime/settings/validate", strings.NewReader(string(validateBody)))
	validateReq.Header.Set(settingsPromotedProviderIndexHeader, "0")
	validateRec := httptest.NewRecorder()
	handler.ServeHTTP(validateRec, validateReq)
	var validation SettingsValidationResponse
	if err := json.Unmarshal(validateRec.Body.Bytes(), &validation); err != nil || validateRec.Code != http.StatusOK || !validation.Valid {
		t.Fatalf("validation status=%d body=%s err=%v", validateRec.Code, validateRec.Body.String(), err)
	}
	saveBody, _ := json.Marshal(promoted.Doc)
	saveReq := httptest.NewRequest(http.MethodPut, "/runtime/settings", strings.NewReader(string(saveBody)))
	saveReq.Header.Set("If-Match", promoted.Revision)
	saveReq.Header.Set(settingsPromotedProviderIndexHeader, "0")
	saveRec := httptest.NewRecorder()
	handler.ServeHTTP(saveRec, saveReq)
	if saveRec.Code != http.StatusOK {
		t.Fatalf("save status=%d body=%s", saveRec.Code, saveRec.Body.String())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"apiKey": "glm-key"`) {
		t.Fatalf("saved primary credential did not come from promoted provider: %s", data)
	}
	if strings.Contains(string(data), settingsSecretSentinel) {
		t.Fatalf("masked credential was written to disk: %s", data)
	}
}

func TestSettingsPromoteProviderRejectsInvalidIndex(t *testing.T) {
	handler, path := settingsTestHandler(t, "")
	if err := os.WriteFile(path, []byte(`{"model":"primary","fallback":{"providers":[]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	rec := settingsRequest(handler, http.MethodPost, "/runtime/settings/promote-provider", `{"doc":{"fallback":{"providers":[]}},"provider_index":0}`, "", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSettingsProviderProbeRestoresCredentialAndDoesNotSave(t *testing.T) {
	handler, path := settingsTestHandler(t, "")
	for _, key := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "NIUMA_PROVIDER", "CLAUDE_CODE_PROVIDER"} {
		t.Setenv(key, "")
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer stored-key" {
			t.Errorf("unexpected probe path=%s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		writeJSON(w, map[string]any{"data": []any{}})
	}))
	defer provider.Close()
	stored := `{"provider":"openai","apiKey":"stored-key","baseURL":"` + provider.URL + `/v1"}`
	if err := os.WriteFile(path, []byte(stored), 0600); err != nil {
		t.Fatal(err)
	}
	doc := decodeSettingsResponse(t, doSettings(t, handler, http.MethodGet, "", "")).Doc
	body, _ := json.Marshal(SettingsProviderTestRequest{Doc: doc})
	rec := settingsRequest(handler, http.MethodPost, "/runtime/settings/test-provider", string(body), "", "")
	var result SettingsProviderTestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || !result.OK {
		t.Fatalf("probe=%s err=%v", rec.Body.String(), err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != stored {
		t.Fatal("probe wrote settings")
	}
}

func TestSettingsProviderProbeDoesNotFollowRedirect(t *testing.T) {
	handler, _ := settingsTestHandler(t, "")
	for _, key := range []string{"ANTHROPIC_BASE_URL", "NIUMA_PROVIDER", "CLAUDE_CODE_PROVIDER"} {
		t.Setenv(key, "")
	}
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("followed redirect") }))
	defer target.Close()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer provider.Close()
	body := `{"doc":{"provider":"openai","baseURL":"` + provider.URL + `/v1","apiKey":"secret"}}`
	rec := settingsRequest(handler, http.MethodPost, "/runtime/settings/test-provider", body, "", "")
	var result SettingsProviderTestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || result.OK || result.StatusCode != http.StatusFound {
		t.Fatalf("unexpected redirect result: %s", rec.Body.String())
	}
}

// WebUI 2.0's JSON/model editors and effective panel must agree on the file
// source, independently of workspace files and the process startup snapshot.
func TestWebUIV2SettingsReadSameGlobalFile(t *testing.T) {
	for _, relocated := range []bool{false, true} {
		name := "default-home"
		if relocated {
			name = "config-dir"
		}
		t.Run(name, func(t *testing.T) {
			home, workspace := t.TempDir(), t.TempDir()
			for _, key := range []string{"NIUMA_CONFIG_DIR", "CLAUDE_CONFIG_DIR", "NIUMA_CONFIG_DIR_NAME", "CLAUDE_CODE_CONFIG_DIR_NAME"} {
				t.Setenv(key, "")
			}
			t.Setenv("HOME", home)
			globalDir := filepath.Join(home, ".niuma")
			if relocated {
				globalDir = t.TempDir()
				t.Setenv("NIUMA_CONFIG_DIR", globalDir)
			}
			path := filepath.Join(globalDir, "settings.json")
			mustWriteServerTest(t, path, `{"provider":"anthropic","model":"global-only","baseURL":"https://example.test/v1","apiKey":"secret-must-not-leak"}`)
			mustWriteServerTest(t, filepath.Join(workspace, ".niuma", "settings.json"), `{"model":"ignored-project"}`)
			mustWriteServerTest(t, filepath.Join(workspace, "config", "config.yaml"), "model: ignored-yaml\n")
			handler := NewHandler(Options{AuthToken: "test-token", Workspace: workspace}, func(context.Context, QueryRequest) (query.Result, error) { return query.Result{}, nil })
			for _, endpoint := range []string{"/runtime/settings", "/runtime/settings/effective"} {
				if rec := settingsRequest(handler, "GET", endpoint, "", "", ""); rec.Code != http.StatusUnauthorized {
					t.Fatalf("unauthorized read: %d", rec.Code)
				}
				rec := settingsRequest(handler, "GET", endpoint, "", "test-token", "")
				if rec.Code != http.StatusOK {
					t.Fatalf("%s: %d %s", endpoint, rec.Code, rec.Body.String())
				}
				if strings.Contains(rec.Body.String(), "secret-must-not-leak") || strings.Contains(rec.Body.String(), "ignored-") {
					t.Fatalf("wrong source or secret leak: %s", rec.Body.String())
				}
				if endpoint == "/runtime/settings" {
					doc := decodeSettingsResponse(t, rec)
					if doc.Path != path || !doc.Exists || doc.Doc["model"] != "global-only" {
						t.Fatalf("editor source: %+v", doc)
					}
				} else {
					var doc EffectiveSettingsResponse
					if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
						t.Fatal(err)
					}
					if doc.FileResolved.Doc["model"] != "global-only" || doc.FileResolved.RouteSources["model"] != path {
						t.Fatalf("effective source: %+v", doc.FileResolved)
					}
				}
			}
		})
	}
}

func TestSettingsProviderProbeRunsMinimalChat(t *testing.T) {
	for _, key := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "NIUMA_PROVIDER", "CLAUDE_CODE_PROVIDER"} {
		t.Setenv(key, "")
	}
	const secret = "sk-probe-secret-value"
	cases := []struct {
		name       string
		chatStatus int
		chatBody   string
		wantOK     bool
		wantReason string
	}{
		{"ok", 200, `{"choices":[]}`, true, ""},
		{"bad key", 401, `{"error":"bad key sk-probe-secret-value"}`, false, "key_invalid"},
		{"forbidden", 403, `{}`, false, "key_invalid"},
		{"rate limited", 429, `{}`, false, "quota_or_rate_limit"},
		{"payment required", 402, `{}`, false, "quota_or_rate_limit"},
		{"quota in 403", 403, `{"error":{"code":"insufficient_quota"}}`, false, "quota_or_rate_limit"},
		{"model missing", 404, `{}`, false, "model_not_found"},
		{"model missing 400", 400, `{"error":{"message":"The model does not exist"}}`, false, "model_not_found"},
		{"server error", 500, `{}`, false, "chat_failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler, _ := settingsTestHandler(t, "")
			var chatBody map[string]any
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/models" {
					writeJSON(w, map[string]any{"data": []any{}})
					return
				}
				if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer "+secret {
					t.Errorf("unexpected chat path=%s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
				}
				_ = json.NewDecoder(r.Body).Decode(&chatBody)
				w.WriteHeader(tc.chatStatus)
				_, _ = w.Write([]byte(tc.chatBody))
			}))
			defer provider.Close()
			body := `{"doc":{"provider":"openai","model":"free-model","baseURL":"` + provider.URL + `/v1","apiKey":"` + secret + `"}}`
			rec := settingsRequest(handler, http.MethodPost, "/runtime/settings/test-provider", body, "", "")
			var result SettingsProviderTestResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
				t.Fatalf("decode: %v body=%s", err, rec.Body.String())
			}
			if result.OK != tc.wantOK || result.Reason != tc.wantReason || result.Kind != "chat_connection" {
				t.Fatalf("result=%+v", result)
			}
			if chatBody["model"] != "free-model" || chatBody["max_tokens"] != float64(1) {
				t.Fatalf("chat body=%v", chatBody)
			}
			if strings.Contains(rec.Body.String(), secret) {
				t.Fatalf("secret leaked: %s", rec.Body.String())
			}
		})
	}
}

func TestSettingsProviderProbeChatNetworkFailure(t *testing.T) {
	for _, key := range []string{"ANTHROPIC_BASE_URL", "NIUMA_PROVIDER", "CLAUDE_CODE_PROVIDER"} {
		t.Setenv(key, "")
	}
	handler, _ := settingsTestHandler(t, "")
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			writeJSON(w, map[string]any{"data": []any{}})
			return
		}
		hj, _ := w.(http.Hijacker)
		conn, _, _ := hj.Hijack()
		conn.Close()
	}))
	defer provider.Close()
	body := `{"doc":{"provider":"openai","model":"m","baseURL":"` + provider.URL + `/v1","apiKey":"k"}}`
	rec := settingsRequest(handler, http.MethodPost, "/runtime/settings/test-provider", body, "", "")
	var result SettingsProviderTestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || result.OK || result.Reason != "network" {
		t.Fatalf("result=%s err=%v", rec.Body.String(), err)
	}
}

func TestSettingsProviderProbeAnthropicChatUsesMessages(t *testing.T) {
	for _, key := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "NIUMA_PROVIDER", "CLAUDE_CODE_PROVIDER"} {
		t.Setenv(key, "")
	}
	handler, _ := settingsTestHandler(t, "")
	var hit bool
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			writeJSON(w, map[string]any{"data": []any{}})
			return
		}
		hit = r.URL.Path == "/v1/messages" && r.Header.Get("x-api-key") == "k"
		writeJSON(w, map[string]any{"type": "message"})
	}))
	defer provider.Close()
	body := `{"doc":{"provider":"anthropic","model":"m","baseURL":"` + provider.URL + `","apiKey":"k"}}`
	rec := settingsRequest(handler, http.MethodPost, "/runtime/settings/test-provider", body, "", "")
	var result SettingsProviderTestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || !result.OK || !hit {
		t.Fatalf("result=%s err=%v hit=%v", rec.Body.String(), err, hit)
	}
}

func TestSettingsProviderProbeDetectsToolSupport(t *testing.T) {
	for _, key := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "NIUMA_PROVIDER", "CLAUDE_CODE_PROVIDER"} {
		t.Setenv(key, "")
	}
	cases := []struct {
		name         string
		rejectTools  int
		rejectBody   string
		retryStatus  int
		wantOK       bool
		wantTools    string
		wantAttempts int
	}{
		{"supported", 0, "", 0, true, "supported", 1},
		{"unsupported 400", 400, `{"error":{"message":"tool calling is not supported by this model"}}`, 200, true, "unsupported", 2},
		{"unsupported 404", 404, `{"error":"No endpoints found that support tool use"}`, 200, true, "unsupported", 2},
		{"tools rejected and chat still fails", 400, `{"error":"tools not supported"}`, 401, false, "unknown", 2},
		{"plain 400 is not a tools rejection", 400, `{"error":"max_tokens too small"}`, 0, false, "unknown", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler, _ := settingsTestHandler(t, "")
			attempts := 0
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/models" {
					writeJSON(w, map[string]any{"data": []any{}})
					return
				}
				attempts++
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				_, hasTools := body["tools"]
				if attempts == 1 && !hasTools || attempts > 1 && hasTools {
					t.Errorf("attempt %d hasTools=%v", attempts, hasTools)
				}
				if hasTools && body["tool_choice"] != "auto" {
					t.Errorf("tool_choice=%v, want auto", body["tool_choice"])
				}
				switch {
				case tc.rejectTools != 0 && hasTools:
					w.WriteHeader(tc.rejectTools)
					_, _ = w.Write([]byte(tc.rejectBody))
				case tc.rejectTools != 0 && tc.retryStatus != 0:
					w.WriteHeader(tc.retryStatus)
					_, _ = w.Write([]byte(`{}`))
				default:
					writeJSON(w, map[string]any{"choices": []any{}})
				}
			}))
			defer provider.Close()
			body := `{"doc":{"provider":"openai","model":"m","baseURL":"` + provider.URL + `/v1","apiKey":"k"}}`
			rec := settingsRequest(handler, http.MethodPost, "/runtime/settings/test-provider", body, "", "")
			var result SettingsProviderTestResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.OK != tc.wantOK || result.ToolSupport != tc.wantTools || attempts != tc.wantAttempts {
				t.Fatalf("result=%+v attempts=%d", result, attempts)
			}
		})
	}
}
