package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/konglong87/super-niuma/internal/agentprofile"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

type agentProfileSaveRequest struct {
	ProfileKey     string          `json:"profile_key,omitempty"`
	Scope          string          `json:"scope,omitempty"`
	DisplayName    string          `json:"display_name"`
	Description    string          `json:"description,omitempty"`
	ProfileVersion uint            `json:"profile_version,omitempty"`
	Status         string          `json:"status,omitempty"`
	Config         json.RawMessage `json:"config"`
}

type agentProfileBindingRequest struct {
	ProfileVersion uint   `json:"profile_version"`
	AccountID      uint64 `json:"account_id"`
	Provider       string `json:"provider"`
	BindingKey     string `json:"binding_key"`
}

func tenantAgentProfilesHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		service, ok := optionalAgentProfileService(opts)
		if !ok {
			writeTenantError(w, http.StatusServiceUnavailable, "agent profile service is not configured")
			return
		}
		key := agentProfilePathKey(r)
		switch r.Method {
		case http.MethodGet:
			if key == "" {
				items, err := service.ListAgentProfiles(r.Context(), strings.TrimSpace(r.URL.Query().Get("status")), parseLimit(r.URL.Query().Get("limit")))
				if err != nil {
					writeTenantServiceError(w, err)
					return
				}
				writeJSON(w, map[string]any{"data": items})
				return
			}
			version, ok := parseOptionalUint(r.URL.Query().Get("version"))
			if !ok {
				writeTenantError(w, http.StatusBadRequest, "version must be an unsigned integer")
				return
			}
			item, err := service.GetAgentProfile(r.Context(), key, version)
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			writeJSON(w, item)
		case http.MethodPost, http.MethodPatch:
			if !requireTenantRole(w, r, opts, "owner", "admin") {
				return
			}
			var req agentProfileSaveRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeTenantError(w, http.StatusBadRequest, err.Error())
				return
			}
			if key != "" && req.ProfileKey == "" {
				req.ProfileKey = key
			}
			input, err := agentProfileInput(req)
			if err != nil {
				writeTenantError(w, http.StatusBadRequest, err.Error())
				return
			}
			item, err := service.SaveAgentProfile(r.Context(), input)
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			recordTenantAudit(r.Context(), opts.TenantService, "tenant.agent_profile.update", "agent_profile", item.ID)
			writeJSON(w, item)
		default:
			writeTenantError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})
}

func tenantAgentProfileValidateHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, []string{"owner", "admin"}, func(w http.ResponseWriter, r *http.Request) {
		service, ok := optionalAgentProfileService(opts)
		if !ok {
			writeTenantError(w, http.StatusServiceUnavailable, "agent profile service is not configured")
			return
		}
		if r.Method != http.MethodPost {
			writeTenantError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req agentProfileSaveRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeTenantError(w, http.StatusBadRequest, err.Error())
			return
		}
		if req.ProfileKey == "" {
			req.ProfileKey = agentProfilePathKey(r)
		}
		input, err := agentProfileInput(req)
		if err != nil {
			writeTenantError(w, http.StatusBadRequest, err.Error())
			return
		}
		report, err := service.ValidateAgentProfile(r.Context(), input)
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		writeJSON(w, report)
	})
}

func tenantAgentProfileEffectiveHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		service, ok := optionalAgentProfileService(opts)
		if !ok {
			writeTenantError(w, http.StatusServiceUnavailable, "agent profile service is not configured")
			return
		}
		if r.Method != http.MethodGet {
			writeTenantError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		key := agentProfilePathKey(r)
		if key == "" {
			writeTenantError(w, http.StatusBadRequest, "profile key is required")
			return
		}
		version, valid := parseOptionalUint(r.URL.Query().Get("version"))
		if !valid {
			writeTenantError(w, http.StatusBadRequest, "version must be an unsigned integer")
			return
		}
		request := agentprofile.ResolveRequest{Surface: strings.TrimSpace(r.URL.Query().Get("surface")), ProfileKey: key, ProfileVersion: version}
		if request.Surface == "" {
			request.Surface = agentprofile.SurfaceWebChat
		}
		request.Overrides.Language = strings.TrimSpace(r.URL.Query().Get("language"))
		request.Overrides.OutputStyle = strings.TrimSpace(r.URL.Query().Get("output_style"))
		request.Overrides.Effort = strings.TrimSpace(r.URL.Query().Get("effort"))
		if raw := strings.TrimSpace(r.URL.Query().Get("max_turns")); raw != "" {
			value, parseErr := strconv.Atoi(raw)
			if parseErr != nil || value <= 0 {
				writeTenantError(w, http.StatusBadRequest, "max_turns must be a positive integer")
				return
			}
			request.Overrides.MaxTurns = &value
		}
		if raw := strings.TrimSpace(r.URL.Query().Get("max_tokens")); raw != "" {
			value, parseErr := strconv.Atoi(raw)
			if parseErr != nil || value <= 0 {
				writeTenantError(w, http.StatusBadRequest, "max_tokens must be a positive integer")
				return
			}
			request.Overrides.MaxTokens = &value
		}
		effective, err := service.ResolveAgentProfile(r.Context(), request)
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		writeJSON(w, effective)
	})
}

func tenantAgentProfilePublishHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, []string{"owner", "admin"}, func(w http.ResponseWriter, r *http.Request) {
		service, ok := optionalAgentProfileService(opts)
		if !ok {
			writeTenantError(w, http.StatusServiceUnavailable, "agent profile service is not configured")
			return
		}
		if r.Method != http.MethodPost {
			writeTenantError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		key := agentProfilePathKey(r)
		version, valid := parseOptionalUint(r.URL.Query().Get("version"))
		if key == "" || !valid || version == 0 {
			writeTenantError(w, http.StatusBadRequest, "profile key and version are required")
			return
		}
		if err := service.PublishAgentProfile(r.Context(), key, version); err != nil {
			writeTenantServiceError(w, err)
			return
		}
		recordTenantAudit(r.Context(), opts.TenantService, "tenant.agent_profile.publish", "agent_profile", key+"@"+strconv.FormatUint(uint64(version), 10))
		writeJSON(w, map[string]any{"profile_key": key, "profile_version": version, "status": "published"})
	})
}

func tenantAgentProfileArchiveHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, []string{"owner", "admin"}, func(w http.ResponseWriter, r *http.Request) {
		service, ok := optionalAgentProfileService(opts)
		if !ok {
			writeTenantError(w, http.StatusServiceUnavailable, "agent profile service is not configured")
			return
		}
		if r.Method != http.MethodPost {
			writeTenantError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		key := agentProfilePathKey(r)
		version, valid := parseOptionalUint(r.URL.Query().Get("version"))
		if key == "" || !valid || version == 0 {
			writeTenantError(w, http.StatusBadRequest, "profile key and version are required")
			return
		}
		if err := service.ArchiveAgentProfile(r.Context(), key, version); err != nil {
			writeTenantServiceError(w, err)
			return
		}
		recordTenantAudit(r.Context(), opts.TenantService, "tenant.agent_profile.archive", "agent_profile", key+"@"+strconv.FormatUint(uint64(version), 10))
		writeJSON(w, map[string]any{"profile_key": key, "profile_version": version, "status": "archived"})
	})
}

func tenantAgentProfileRollbackHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, []string{"owner", "admin"}, func(w http.ResponseWriter, r *http.Request) {
		service, ok := optionalAgentProfileService(opts)
		if !ok {
			writeTenantError(w, http.StatusServiceUnavailable, "agent profile service is not configured")
			return
		}
		if r.Method != http.MethodPost {
			writeTenantError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		key := agentProfilePathKey(r)
		version, valid := parseOptionalUint(r.URL.Query().Get("version"))
		if key == "" || !valid || version == 0 {
			writeTenantError(w, http.StatusBadRequest, "profile key and version are required")
			return
		}
		item, err := service.RollbackAgentProfile(r.Context(), key, version)
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		recordTenantAudit(r.Context(), opts.TenantService, "tenant.agent_profile.rollback", "agent_profile", item.ID)
		writeJSON(w, item)
	})
}

func tenantAgentProfileBindingHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, []string{"owner", "admin"}, func(w http.ResponseWriter, r *http.Request) {
		service, ok := optionalAgentProfileService(opts)
		if !ok {
			writeTenantError(w, http.StatusServiceUnavailable, "agent profile service is not configured")
			return
		}
		key := agentProfilePathKey(r)
		version, valid := parseOptionalUint(r.URL.Query().Get("version"))
		if key == "" || !valid || version == 0 {
			writeTenantError(w, http.StatusBadRequest, "profile key and version are required")
			return
		}
		switch r.Method {
		case http.MethodGet:
			item, err := service.GetAgentProfileChannelBinding(r.Context(), key, version)
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			writeJSON(w, item)
		case http.MethodPut:
			var req agentProfileBindingRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeTenantError(w, http.StatusBadRequest, err.Error())
				return
			}
			if req.ProfileVersion == 0 {
				req.ProfileVersion = version
			}
			item, err := service.UpsertAgentProfileChannelBinding(r.Context(), key, version, mysqlstore.AgentProfileChannelBindingInput{AccountID: req.AccountID, Provider: req.Provider, BindingKey: req.BindingKey, Status: "active"})
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			recordTenantAudit(r.Context(), opts.TenantService, "tenant.agent_profile.binding_changed", "agent_profile", item.ID)
			writeJSON(w, item)
		case http.MethodDelete:
			if err := service.ArchiveAgentProfileChannelBinding(r.Context(), key, version); err != nil {
				writeTenantServiceError(w, err)
				return
			}
			recordTenantAudit(r.Context(), opts.TenantService, "tenant.agent_profile.binding_archive", "agent_profile", key+"@"+strconv.FormatUint(uint64(version), 10))
			writeJSON(w, map[string]any{"archived": true})
		default:
			writeTenantError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})
}

func tenantAgentProfileConversationsHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		service, ok := optionalAgentProfileService(opts)
		if !ok {
			writeTenantError(w, http.StatusServiceUnavailable, "agent profile service is not configured")
			return
		}
		if r.Method != http.MethodGet {
			writeTenantError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		key := agentProfilePathKey(r)
		if key == "" {
			writeTenantError(w, http.StatusBadRequest, "profile key is required")
			return
		}
		version, valid := parseOptionalUint(r.URL.Query().Get("version"))
		if !valid {
			writeTenantError(w, http.StatusBadRequest, "version must be an unsigned integer")
			return
		}
		item, err := service.ListAgentProfileConversations(r.Context(), key, version, parseLimit(r.URL.Query().Get("limit")))
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		writeJSON(w, item)
	})
}

func tenantAgentProfileAssignmentHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		service, ok := optionalAgentProfileService(opts)
		if !ok {
			writeTenantError(w, http.StatusServiceUnavailable, "agent profile service is not configured")
			return
		}
		switch r.Method {
		case http.MethodGet:
			item, err := service.GetAgentProfileAssignment(r.Context(), strings.TrimSpace(r.URL.Query().Get("surface")))
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			writeJSON(w, item)
		case http.MethodPut:
			var req mysqlstore.AgentProfileAssignmentInput
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeTenantError(w, http.StatusBadRequest, err.Error())
				return
			}
			if req.UserID != 0 {
				resolved, resolveErr := opts.TenantService.ResolveContext(r.Context())
				if resolveErr != nil {
					writeTenantServiceError(w, resolveErr)
					return
				}
				if req.UserID != resolved.UserID && !requireTenantRole(w, r, opts, "owner", "admin") {
					return
				}
			}
			item, err := service.UpsertAgentProfileAssignment(r.Context(), req)
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			recordTenantAudit(r.Context(), opts.TenantService, "tenant.agent_profile.assign", "agent_profile_assignment", item.ID)
			writeJSON(w, item)
		default:
			writeTenantError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})
}

func agentProfileInput(req agentProfileSaveRequest) (mysqlstore.AgentProfileInput, error) {
	if strings.TrimSpace(req.ProfileKey) == "" || strings.TrimSpace(req.DisplayName) == "" || len(req.Config) == 0 || !json.Valid(req.Config) {
		return mysqlstore.AgentProfileInput{}, &badProfileRequestError{message: "profile_key, display_name and valid config are required"}
	}
	return mysqlstore.AgentProfileInput{ProfileKey: strings.TrimSpace(req.ProfileKey), Scope: strings.TrimSpace(req.Scope), DisplayName: strings.TrimSpace(req.DisplayName), Description: req.Description, ProfileVersion: req.ProfileVersion, Status: req.Status, ConfigJSON: string(req.Config)}, nil
}

type badProfileRequestError struct{ message string }

func (e *badProfileRequestError) Error() string { return e.message }

func agentProfilePathKey(r *http.Request) string {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	for i, part := range parts {
		if part == "agent-profiles" && i+1 < len(parts) {
			next := parts[i+1]
			if next != "" && next != "versions" && next != "validate" && next != "publish" && next != "archive" && next != "rollback" && next != "bot-binding" && next != "effective" {
				return next
			}
		}
	}
	return strings.TrimSpace(r.URL.Query().Get("profile_key"))
}
