package server

import (
	"encoding/json"
	"net/http"
	"strings"
)

func tenantFeishuOnboardingHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, []string{"owner", "admin"}, func(w http.ResponseWriter, r *http.Request) {
		service := opts.FeishuOnboarding
		if service == nil {
			writeTenantError(w, http.StatusServiceUnavailable, "Feishu onboarding is not configured")
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
		var request FeishuOnboardingRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeTenantError(w, http.StatusBadRequest, err.Error())
			return
		}
		session, err := service.Start(r.Context(), resolved.TenantID, resolved.UserID, request)
		if err != nil {
			writeTenantError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, session)
	})
}

func tenantFeishuOnboardingCLISHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		service := opts.FeishuOnboarding
		if service == nil {
			writeTenantError(w, http.StatusServiceUnavailable, "Feishu onboarding is not configured")
			return
		}
		if _, err := opts.TenantService.ResolveContext(r.Context()); err != nil {
			writeTenantServiceError(w, err)
			return
		}
		if r.Method != http.MethodGet {
			writeTenantError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		result, err := service.CLIStatus(r.Context())
		if err != nil {
			writeTenantError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, result)
	})
}

func tenantFeishuOnboardingCLIInstallHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, []string{"owner", "admin"}, func(w http.ResponseWriter, r *http.Request) {
		service := opts.FeishuOnboarding
		if service == nil {
			writeTenantError(w, http.StatusServiceUnavailable, "Feishu onboarding is not configured")
			return
		}
		if _, err := opts.TenantService.ResolveContext(r.Context()); err != nil {
			writeTenantServiceError(w, err)
			return
		}
		if r.Method != http.MethodPost {
			writeTenantError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		result, err := service.InstallCLI(r.Context())
		if err != nil {
			writeTenantError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, result)
	})
}

func tenantFeishuOnboardingSessionHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		service := opts.FeishuOnboarding
		if service == nil {
			writeTenantError(w, http.StatusServiceUnavailable, "Feishu onboarding is not configured")
			return
		}
		resolved, err := opts.TenantService.ResolveContext(r.Context())
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		id := onboardingSessionID(r.URL.Path)
		if id == "" {
			writeTenantError(w, http.StatusBadRequest, "onboarding session id is required")
			return
		}
		if r.Method == http.MethodPost && strings.HasSuffix(strings.TrimRight(r.URL.Path, "/"), "/cancel") {
			if err := service.Cancel(r.Context(), resolved.TenantID, id); err != nil {
				writeTenantError(w, http.StatusNotFound, err.Error())
				return
			}
			writeJSON(w, map[string]bool{"ok": true})
			return
		}
		if r.Method != http.MethodGet {
			writeTenantError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		session, err := service.Get(r.Context(), resolved.TenantID, id)
		if err != nil {
			writeTenantError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, session)
	})
}

func onboardingSessionID(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for index, part := range parts {
		if part == "feishu" && index+2 < len(parts) && parts[index+1] == "onboarding" {
			return parts[index+2]
		}
	}
	return ""
}
