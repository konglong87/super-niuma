package server

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/konglong87/super-niuma/internal/provisioning"
)

type provisioningHandlerService struct{}

func (provisioningHandlerService) Create(context.Context, uint64, uint64, provisioning.CreateRequest) (provisioning.Record, error) {
	return provisioning.Record{ID: 1, Status: provisioning.StatusDraft}, nil
}
func (provisioningHandlerService) List(context.Context, uint64, int) ([]provisioning.Record, error) {
	return []provisioning.Record{{ID: 1}}, nil
}
func (provisioningHandlerService) Get(context.Context, uint64, uint64, string) (provisioning.Record, error) {
	return provisioning.Record{ID: 1}, nil
}
func (provisioningHandlerService) Preflight(context.Context, uint64, uint64, uint64) (provisioning.Record, error) {
	return provisioning.Record{ID: 1, Status: provisioning.StatusPreflight}, nil
}
func (provisioningHandlerService) WorkerAction(context.Context, uint64, uint64, uint64, string) (provisioning.Record, error) {
	return provisioning.Record{ID: 1, Status: provisioning.StatusRunning}, nil
}
func (provisioningHandlerService) Logs(context.Context, uint64, uint64, int) (string, error) {
	return "", nil
}
func (provisioningHandlerService) Overview(context.Context, uint64, int) (provisioning.Overview, error) {
	return provisioning.Overview{}, nil
}
func TestProvisioningHandlerRequiresService(t *testing.T) {
	handler := NewHandler(Options{AuthToken: "token"}, nil)
	req := httptest.NewRequest("GET", "/tenant/agent-provisionings", nil)
	req.Header.Set("Authorization", "Bearer token")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != 503 {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}
