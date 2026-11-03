package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

type agentTeamSaveRequest struct {
	TeamKey       string          `json:"team_key,omitempty"`
	Scope         string          `json:"scope,omitempty"`
	DisplayName   string          `json:"display_name"`
	Description   string          `json:"description,omitempty"`
	TeamVersion   uint            `json:"team_version,omitempty"`
	Status        string          `json:"status,omitempty"`
	SchemaVersion uint            `json:"schema_version,omitempty"`
	Policy        json.RawMessage `json:"policy"`
}

type agentTeamMemberRequest struct {
	TenantID              uint64 `json:"tenant_id,omitempty"`
	TeamID                uint64 `json:"team_id,omitempty"`
	MemberKey             string `json:"member_key"`
	ProfileID             uint64 `json:"profile_id"`
	Role                  string `json:"role"`
	AccountID             uint64 `json:"account_id,omitempty"`
	ToolPolicyJSON        string `json:"tool_policy_json,omitempty"`
	WorkspacePolicyJSON   string `json:"workspace_policy_json,omitempty"`
	ExecutionOverrideJSON string `json:"execution_override_json,omitempty"`
	Status                string `json:"status,omitempty"`
}

type agentTeamBindingRequest struct {
	TenantID         uint64 `json:"tenant_id,omitempty"`
	TeamID           uint64 `json:"team_id,omitempty"`
	Provider         string `json:"provider"`
	AccountID        uint64 `json:"account_id"`
	ExternalChatID   string `json:"external_chat_id"`
	ExternalThreadID string `json:"external_thread_id,omitempty"`
	TriggerPolicy    string `json:"trigger_policy"`
	Status           string `json:"status,omitempty"`
}

type agentTeamValidateRequest struct {
	Policy   json.RawMessage           `json:"policy"`
	Members  []agentTeamMemberRequest  `json:"members"`
	Bindings []agentTeamBindingRequest `json:"bindings"`
}

func tenantAgentTeamsHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		service, ok := optionalAgentTeamService(opts)
		if !ok {
			writeTenantError(w, http.StatusServiceUnavailable, "agent team service is not configured")
			return
		}
		key := agentTeamPathKey(r)
		switch r.Method {
		case http.MethodGet:
			if key == "" {
				items, err := service.ListAgentTeams(r.Context(), strings.TrimSpace(r.URL.Query().Get("status")), parseLimit(r.URL.Query().Get("limit")))
				if err != nil {
					writeTenantServiceError(w, err)
					return
				}
				writeJSON(w, map[string]any{"data": items})
				return
			}
			version, valid := parseOptionalUint(r.URL.Query().Get("version"))
			if !valid {
				writeTenantError(w, http.StatusBadRequest, "version must be an unsigned integer")
				return
			}
			item, err := service.GetAgentTeam(r.Context(), key, version)
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			writeJSON(w, item)
		case http.MethodPost, http.MethodPatch:
			if !requireTenantRole(w, r, opts, "owner", "admin") {
				return
			}
			var req agentTeamSaveRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeTenantError(w, http.StatusBadRequest, err.Error())
				return
			}
			if key != "" && req.TeamKey == "" {
				req.TeamKey = key
			}
			input, err := agentTeamInput(req)
			if err != nil {
				writeTenantError(w, http.StatusBadRequest, err.Error())
				return
			}
			item, err := service.SaveAgentTeam(r.Context(), input)
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			recordTenantAudit(r.Context(), opts.TenantService, "tenant.agent_team.update", "agent_team", item.ID)
			writeJSON(w, item)
		default:
			writeTenantError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})
}

func tenantAgentTeamValidateHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, []string{"owner", "admin"}, func(w http.ResponseWriter, r *http.Request) {
		service, ok := optionalAgentTeamService(opts)
		if !ok {
			writeTenantError(w, http.StatusServiceUnavailable, "agent team service is not configured")
			return
		}
		if r.Method != http.MethodPost {
			writeTenantError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req agentTeamValidateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeTenantError(w, http.StatusBadRequest, err.Error())
			return
		}
		teamInput, err := agentTeamInput(agentTeamSaveRequest{TeamKey: agentTeamPathKey(r), DisplayName: "validation", Policy: req.Policy})
		if err != nil {
			writeTenantError(w, http.StatusBadRequest, err.Error())
			return
		}
		version, versionOK := parseOptionalUint(r.URL.Query().Get("version"))
		if !versionOK || version == 0 {
			writeTenantError(w, http.StatusBadRequest, "team version is required")
			return
		}
		teamInput.TeamVersion = version
		members := make([]mysqlstore.AgentTeamMemberInput, 0, len(req.Members))
		for _, member := range req.Members {
			members = append(members, mysqlstore.AgentTeamMemberInput{MemberKey: member.MemberKey, ProfileID: member.ProfileID, Role: member.Role, AccountID: member.AccountID, ToolPolicyJSON: member.ToolPolicyJSON, WorkspacePolicyJSON: member.WorkspacePolicyJSON, ExecutionOverrideJSON: member.ExecutionOverrideJSON, Status: member.Status})
		}
		bindings := make([]mysqlstore.AgentTeamBindingInput, 0, len(req.Bindings))
		for _, binding := range req.Bindings {
			bindings = append(bindings, mysqlstore.AgentTeamBindingInput{Provider: binding.Provider, AccountID: binding.AccountID, ExternalChatID: binding.ExternalChatID, ExternalThreadID: binding.ExternalThreadID, TriggerPolicy: binding.TriggerPolicy, Status: binding.Status})
		}
		report, err := service.ValidateAgentTeam(r.Context(), teamInput, members, bindings)
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		writeJSON(w, report)
	})
}

func tenantAgentTeamLifecycleHandler(opts Options, action string) http.HandlerFunc {
	return tenantEndpoint(opts, []string{"owner", "admin"}, func(w http.ResponseWriter, r *http.Request) {
		service, ok := optionalAgentTeamService(opts)
		if !ok {
			writeTenantError(w, http.StatusServiceUnavailable, "agent team service is not configured")
			return
		}
		if r.Method != http.MethodPost {
			writeTenantError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		key := agentTeamPathKey(r)
		version, valid := parseOptionalUint(r.URL.Query().Get("version"))
		if key == "" || !valid || version == 0 {
			writeTenantError(w, http.StatusBadRequest, "team key and version are required")
			return
		}
		var err error
		switch action {
		case "publish":
			err = service.PublishAgentTeam(r.Context(), key, version)
		case "archive":
			err = service.ArchiveAgentTeam(r.Context(), key, version)
		default:
			writeTenantError(w, http.StatusBadRequest, "unsupported team action")
			return
		}
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		recordTenantAudit(r.Context(), opts.TenantService, "tenant.agent_team."+action, "agent_team", key+"@"+strconv.FormatUint(uint64(version), 10))
		writeJSON(w, map[string]any{"team_key": key, "team_version": version, "status": action})
	})
}

func tenantAgentTeamRollbackHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, []string{"owner", "admin"}, func(w http.ResponseWriter, r *http.Request) {
		service, ok := optionalAgentTeamService(opts)
		if !ok {
			writeTenantError(w, http.StatusServiceUnavailable, "agent team service is not configured")
			return
		}
		if r.Method != http.MethodPost {
			writeTenantError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		key := agentTeamPathKey(r)
		version, valid := parseOptionalUint(r.URL.Query().Get("version"))
		if key == "" || !valid || version == 0 {
			writeTenantError(w, http.StatusBadRequest, "team key and version are required")
			return
		}
		item, err := service.RollbackAgentTeam(r.Context(), key, version)
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		recordTenantAudit(r.Context(), opts.TenantService, "tenant.agent_team.rollback", "agent_team", item.ID)
		writeJSON(w, item)
	})
}

func tenantAgentTeamMembersHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, []string{"owner", "admin"}, func(w http.ResponseWriter, r *http.Request) {
		service, ok := optionalAgentTeamService(opts)
		if !ok {
			writeTenantError(w, http.StatusServiceUnavailable, "agent team service is not configured")
			return
		}
		key := agentTeamPathKey(r)
		version, valid := parseOptionalUint(r.URL.Query().Get("version"))
		if key == "" || !valid || version == 0 {
			writeTenantError(w, http.StatusBadRequest, "team key and version are required")
			return
		}
		switch r.Method {
		case http.MethodGet:
			items, err := service.ListAgentTeamMembers(r.Context(), key, version, parseLimit(r.URL.Query().Get("limit")))
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			writeJSON(w, map[string]any{"data": items})
		case http.MethodPut:
			var req []agentTeamMemberRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeTenantError(w, http.StatusBadRequest, err.Error())
				return
			}
			inputs := make([]mysqlstore.AgentTeamMemberInput, 0, len(req))
			for _, member := range req {
				inputs = append(inputs, mysqlstore.AgentTeamMemberInput{MemberKey: member.MemberKey, ProfileID: member.ProfileID, Role: member.Role, AccountID: member.AccountID, ToolPolicyJSON: member.ToolPolicyJSON, WorkspacePolicyJSON: member.WorkspacePolicyJSON, ExecutionOverrideJSON: member.ExecutionOverrideJSON, Status: member.Status})
			}
			items, err := service.ReplaceAgentTeamMembers(r.Context(), key, version, inputs)
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			recordTenantAudit(r.Context(), opts.TenantService, "tenant.agent_team.member_changed", "agent_team", key)
			writeJSON(w, map[string]any{"data": items})
		default:
			writeTenantError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})
}

func tenantAgentTeamBindingsHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, []string{"owner", "admin"}, func(w http.ResponseWriter, r *http.Request) {
		service, ok := optionalAgentTeamService(opts)
		if !ok {
			writeTenantError(w, http.StatusServiceUnavailable, "agent team service is not configured")
			return
		}
		key := agentTeamPathKey(r)
		version, valid := parseOptionalUint(r.URL.Query().Get("version"))
		if key == "" || !valid || version == 0 {
			writeTenantError(w, http.StatusBadRequest, "team key and version are required")
			return
		}
		switch r.Method {
		case http.MethodGet:
			items, err := service.ListAgentTeamBindings(r.Context(), key, version, parseLimit(r.URL.Query().Get("limit")))
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			writeJSON(w, map[string]any{"data": items})
		case http.MethodPut:
			var req []agentTeamBindingRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeTenantError(w, http.StatusBadRequest, err.Error())
				return
			}
			inputs := make([]mysqlstore.AgentTeamBindingInput, 0, len(req))
			for _, binding := range req {
				inputs = append(inputs, mysqlstore.AgentTeamBindingInput{Provider: binding.Provider, AccountID: binding.AccountID, ExternalChatID: binding.ExternalChatID, ExternalThreadID: binding.ExternalThreadID, TriggerPolicy: binding.TriggerPolicy, Status: binding.Status})
			}
			items, err := service.ReplaceAgentTeamBindings(r.Context(), key, version, inputs)
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			recordTenantAudit(r.Context(), opts.TenantService, "tenant.agent_team.binding_changed", "agent_team", key)
			writeJSON(w, map[string]any{"data": items})
		default:
			writeTenantError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})
}

func tenantAgentTeamRunsHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		service, ok := optionalAgentTeamService(opts)
		if !ok {
			writeTenantError(w, http.StatusServiceUnavailable, "agent team service is not configured")
			return
		}
		key := agentTeamPathKey(r)
		version, valid := parseOptionalUint(r.URL.Query().Get("version"))
		if key == "" || !valid || version == 0 {
			writeTenantError(w, http.StatusBadRequest, "team key and version are required")
			return
		}
		items, err := service.ListAgentTeamRuns(r.Context(), key, version, parseLimit(r.URL.Query().Get("limit")))
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		writeJSON(w, map[string]any{"data": items})
	})
}

func tenantAgentTeamRunTimelineHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		service, ok := optionalAgentTeamService(opts)
		if !ok {
			writeTenantError(w, http.StatusServiceUnavailable, "agent team service is not configured")
			return
		}
		if r.Method != http.MethodGet {
			writeTenantError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		teamKey := agentTeamPathKey(r)
		runID := agentTeamRunPathID(r)
		version, versionOK := parseOptionalUint(r.URL.Query().Get("version"))
		if teamKey == "" || !versionOK || version == 0 || runID == "" {
			writeTenantError(w, http.StatusBadRequest, "team key, version and run_id are required")
			return
		}
		limit := parseLimit(r.URL.Query().Get("limit"))
		events, err := service.ListAgentTeamRunEvents(r.Context(), teamKey, version, runID, limit)
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		mailbox, err := service.ListAgentTeamMailbox(r.Context(), teamKey, version, runID, limit)
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		writeJSON(w, map[string]any{"run_id": runID, "events": events, "mailbox": mailbox})
	})
}

func tenantAgentTeamRunHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		service, ok := optionalAgentTeamService(opts)
		if !ok {
			writeTenantError(w, http.StatusServiceUnavailable, "agent team service is not configured")
			return
		}
		runID := strings.TrimSpace(r.URL.Query().Get("run_id"))
		if runID == "" {
			runID = agentTeamRunPathID(r)
		}
		if runID == "" {
			writeTenantError(w, http.StatusBadRequest, "run_id is required")
			return
		}
		teamKey := agentTeamPathKey(r)
		var pinnedTeamID uint64
		if teamKey != "" {
			version, versionOK := parseOptionalUint(r.URL.Query().Get("version"))
			if !versionOK || version == 0 {
				writeTenantError(w, http.StatusBadRequest, "team version is required")
				return
			}
			team, teamErr := service.GetAgentTeam(r.Context(), teamKey, version)
			if teamErr != nil {
				writeTenantError(w, http.StatusNotFound, "team not found")
				return
			}
			pinnedTeamID = team.ID
		}
		if r.Method == http.MethodPost && strings.HasSuffix(strings.TrimRight(r.URL.Path, "/"), "/cancel") {
			if !requireTenantRole(w, r, opts, "owner", "admin") {
				return
			}
			if pinnedTeamID != 0 {
				item, err := service.GetAgentTeamRun(r.Context(), runID)
				if err != nil || item.TeamID != pinnedTeamID {
					writeTenantError(w, http.StatusNotFound, "team run not found")
					return
				}
			}
			if err := service.CancelAgentTeamRun(r.Context(), runID); err != nil {
				writeTenantServiceError(w, err)
				return
			}
			recordTenantAudit(r.Context(), opts.TenantService, "tenant.agent_team.run_cancelled", "agent_team_run", runID)
			writeJSON(w, map[string]any{"run_id": runID, "status": "cancelled"})
			return
		}
		if r.Method != http.MethodGet {
			writeTenantError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		item, err := service.GetAgentTeamRun(r.Context(), runID)
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		if pinnedTeamID != 0 && pinnedTeamID != item.TeamID {
			writeTenantError(w, http.StatusNotFound, "team run not found")
			return
		}
		writeJSON(w, item)
	})
}

func tenantChannelAccountsHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, []string{"owner", "admin"}, func(w http.ResponseWriter, r *http.Request) {
		service, ok := optionalAgentTeamService(opts)
		if !ok {
			writeTenantError(w, http.StatusServiceUnavailable, "agent team service is not configured")
			return
		}
		if r.Method != http.MethodGet {
			writeTenantError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		items, err := service.ListChannelAccounts(r.Context(), parseLimit(r.URL.Query().Get("limit")))
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		for i := range items {
			items[i].CredentialRef = ""
		}
		writeJSON(w, map[string]any{"data": items})
	})
}

func agentTeamInput(req agentTeamSaveRequest) (mysqlstore.AgentTeamInput, error) {
	if strings.TrimSpace(req.TeamKey) == "" || strings.TrimSpace(req.DisplayName) == "" || len(req.Policy) == 0 || !json.Valid(req.Policy) {
		return mysqlstore.AgentTeamInput{}, &badProfileRequestError{message: "team_key, display_name and valid policy are required"}
	}
	return mysqlstore.AgentTeamInput{TeamKey: strings.TrimSpace(req.TeamKey), Scope: strings.TrimSpace(req.Scope), DisplayName: strings.TrimSpace(req.DisplayName), Description: req.Description, TeamVersion: req.TeamVersion, Status: req.Status, SchemaVersion: req.SchemaVersion, PolicyJSON: string(req.Policy)}, nil
}

func agentTeamPathKey(r *http.Request) string {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	for i, part := range parts {
		if part == "agent-teams" && i+1 < len(parts) {
			next := parts[i+1]
			if next != "" && next != "versions" && next != "validate" && next != "publish" && next != "archive" && next != "rollback" && next != "members" && next != "bindings" && next != "runs" && next != "effective" {
				return next
			}
		}
	}
	return strings.TrimSpace(r.URL.Query().Get("team_key"))
}

func agentTeamRunPathID(r *http.Request) string {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	for i, part := range parts {
		if part == "runs" && i+1 < len(parts) && parts[i+1] != "cancel" {
			return parts[i+1]
		}
	}
	return ""
}
