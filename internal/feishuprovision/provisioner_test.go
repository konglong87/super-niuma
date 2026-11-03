package feishuprovision

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/konglong87/super-niuma/internal/provisioning"
)

func TestHTTPProvisionerPreflight(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "tenant_access_token": "tenant-token"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]string{"app_name": "Writer Bot", "open_id": "ou_x"}})
	}))
	defer server.Close()
	checks, err := (HTTPProvisioner{BaseURL: server.URL}).Preflight(context.Background(), provisioning.CredentialRef{AppID: "cli_x", SecretValue: "secret"}, provisioning.WorkerSpec{Streaming: "on", Reactions: "on"})
	if err != nil || len(checks) < 4 {
		t.Fatalf("checks=%#v err=%v", checks, err)
	}
}
