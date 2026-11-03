package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/konglong87/super-niuma/internal/provisioning"
)

type ProvisioningService interface {
	Create(ctx context.Context, tenantID, userID uint64, req provisioning.CreateRequest) (provisioning.Record, error)
	List(ctx context.Context, tenantID uint64, limit int) ([]provisioning.Record, error)
	Get(ctx context.Context, tenantID, id uint64, key string) (provisioning.Record, error)
	Preflight(ctx context.Context, tenantID, id, userID uint64) (provisioning.Record, error)
	WorkerAction(ctx context.Context, tenantID, id, userID uint64, action string) (provisioning.Record, error)
	Logs(ctx context.Context, tenantID, id uint64, tail int) (string, error)
	Overview(ctx context.Context, tenantID uint64, limit int) (provisioning.Overview, error)
}

func tenantAgentProvisioningOverviewHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		service := opts.ProvisioningService
		if service == nil {
			writeTenantError(w, http.StatusServiceUnavailable, "provisioning service is not configured")
			return
		}
		resolved, err := opts.TenantService.ResolveContext(r.Context())
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		if r.Method != http.MethodGet {
			writeTenantError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		result, err := service.Overview(r.Context(), resolved.TenantID, parseLimit(r.URL.Query().Get("limit")))
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		writeJSON(w, result)
	})
}

func tenantAgentProvisioningsHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, []string{"owner", "admin"}, func(w http.ResponseWriter, r *http.Request) {
		service := opts.ProvisioningService
		if service == nil {
			writeTenantError(w, http.StatusServiceUnavailable, "provisioning service is not configured")
			return
		}
		resolved, err := opts.TenantService.ResolveContext(r.Context())
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		switch r.Method {
		case http.MethodGet:
			items, err := service.List(r.Context(), resolved.TenantID, parseLimit(r.URL.Query().Get("limit")))
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			writeJSON(w, map[string]any{"data": items})
		case http.MethodPost:
			var req provisioning.CreateRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeTenantError(w, http.StatusBadRequest, err.Error())
				return
			}
			item, err := service.Create(r.Context(), resolved.TenantID, resolved.UserID, req)
			if err != nil {
				writeTenantError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, item)
		default:
			writeTenantError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})
}

func tenantAgentProvisioningLogsHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		service := opts.ProvisioningService
		if service == nil {
			writeTenantError(w, http.StatusServiceUnavailable, "provisioning service is not configured")
			return
		}
		resolved, err := opts.TenantService.ResolveContext(r.Context())
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		if r.Method != http.MethodGet {
			writeTenantError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) < 3 {
			writeTenantError(w, http.StatusBadRequest, "provisioning id is required")
			return
		}
		id, parseErr := strconv.ParseUint(parts[len(parts)-2], 10, 64)
		if parseErr != nil || id == 0 {
			writeTenantError(w, http.StatusBadRequest, "provisioning id must be a positive integer")
			return
		}
		logs, err := service.Logs(r.Context(), resolved.TenantID, id, parseLimit(r.URL.Query().Get("tail")))
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		writeJSON(w, map[string]any{"data": logs})
	})
}

func tenantAgentProvisioningHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		service := opts.ProvisioningService
		if service == nil {
			writeTenantError(w, http.StatusServiceUnavailable, "provisioning service is not configured")
			return
		}
		resolved, err := opts.TenantService.ResolveContext(r.Context())
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		id, parseErr := strconv.ParseUint(strings.TrimSpace(pathTail(r.URL.Path, "agent-provisionings")), 10, 64)
		if parseErr != nil || id == 0 {
			writeTenantError(w, http.StatusBadRequest, "provisioning id must be a positive integer")
			return
		}
		if r.Method != http.MethodGet {
			writeTenantError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		item, err := service.Get(r.Context(), resolved.TenantID, id, "")
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		writeJSON(w, item)
	})
}

func tenantAgentProvisioningActionHandler(opts Options, fixed string) http.HandlerFunc {
	return tenantEndpoint(opts, []string{"owner", "admin"}, func(w http.ResponseWriter, r *http.Request) {
		service := opts.ProvisioningService
		if service == nil {
			writeTenantError(w, http.StatusServiceUnavailable, "provisioning service is not configured")
			return
		}
		resolved, err := opts.TenantService.ResolveContext(r.Context())
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		if r.Method != http.MethodPost {
			writeTenantError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) < 3 {
			writeTenantError(w, http.StatusBadRequest, "provisioning id is required")
			return
		}
		id, parseErr := strconv.ParseUint(parts[len(parts)-2], 10, 64)
		if parseErr != nil || id == 0 {
			writeTenantError(w, http.StatusBadRequest, "provisioning id must be a positive integer")
			return
		}
		action := fixed
		if fixed == "worker" {
			action = parts[len(parts)-1]
			if action != "start" && action != "restart" && action != "stop" && action != "status" {
				writeTenantError(w, http.StatusBadRequest, "unsupported worker action")
				return
			}
		}
		if action == "preflight" {
			item, err := service.Preflight(r.Context(), resolved.TenantID, id, resolved.UserID)
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			writeJSON(w, item)
			return
		}
		item, err := service.WorkerAction(r.Context(), resolved.TenantID, id, resolved.UserID, action)
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		writeJSON(w, item)
	})
}

func pathTail(path, marker string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] == marker && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}
