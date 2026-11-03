package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	_ "github.com/konglong87/super-niuma/docs"
	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/promptmode"
	tenantservice "github.com/konglong87/super-niuma/internal/tenant"
)

func tenantContextHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_context", "server.tenantContextHandler", "resolve tenant context")
		if !authorize(w, r, opts.AuthToken) {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if opts.TenantService == nil {
			writeTenantError(w, http.StatusServiceUnavailable, errMsgTenantStorageNotConfig)
			return
		}
		resolved, err := opts.TenantService.ResolveContext(r.Context())
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		writeJSON(w, resolved)
	}
}

func tenantTenantsHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, []string{"owner", "admin"}, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_tenants", "server.tenantTenantsHandler", "handle tenant admin lifecycle")
		switch r.Method {
		case http.MethodGet:
			page, err := opts.TenantService.ListTenantsPage(r.Context(), tenantListOptions(r))
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			writeJSON(w, page)
		case http.MethodPost, http.MethodPatch:
			var req tenantservice.TenantRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeTenantError(w, http.StatusBadRequest, err.Error())
				return
			}
			if strings.TrimSpace(req.TenantKey) == "" {
				writeTenantError(w, http.StatusBadRequest, errMsgTenantKeyRequired)
				return
			}
			id, err := opts.TenantService.SaveTenant(r.Context(), req)
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			recordTenantAudit(r.Context(), opts.TenantService, "tenant.tenant.admin_save", "tenant", id)
			writeJSON(w, map[string]any{"id": id})
		case http.MethodDelete:
			tenantKey := strings.TrimSpace(r.URL.Query().Get("tenant_key"))
			if tenantKey == "" {
				writeTenantError(w, http.StatusBadRequest, errMsgTenantKeyRequired)
				return
			}
			if err := opts.TenantService.ArchiveTenant(r.Context(), tenantKey); err != nil {
				writeTenantServiceError(w, err)
				return
			}
			recordTenantAudit(r.Context(), opts.TenantService, "tenant.tenant.archive", "tenant", tenantKey)
			writeJSON(w, map[string]any{"archived": true})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func tenantUserHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_user", "server.tenantUserHandler", "handle tenant user")
		switch r.Method {
		case http.MethodGet:
			user, err := opts.TenantService.GetCurrentUser(r.Context())
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			writeJSON(w, user)
		case http.MethodPost, http.MethodPatch:
			var req tenantservice.UserRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeTenantError(w, http.StatusBadRequest, err.Error())
				return
			}
			id, err := opts.TenantService.SaveCurrentUser(r.Context(), req)
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			recordTenantAudit(r.Context(), opts.TenantService, "tenant.user.save", "user", id)
			writeJSON(w, map[string]any{"id": id})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func tenantUsersHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, []string{"owner", "admin"}, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_users", "server.tenantUsersHandler", "handle tenant users")
		switch r.Method {
		case http.MethodGet:
			page, err := opts.TenantService.ListTenantUsersPage(r.Context(), tenantListOptions(r))
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			writeJSON(w, page)
		case http.MethodPost, http.MethodPatch:
			var req tenantservice.UserRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeTenantError(w, http.StatusBadRequest, err.Error())
				return
			}
			if strings.TrimSpace(req.UserKey) == "" {
				writeTenantError(w, http.StatusBadRequest, errMsgUserKeyRequired)
				return
			}
			id, err := opts.TenantService.SaveTenantUser(r.Context(), req)
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			recordTenantAudit(r.Context(), opts.TenantService, "tenant.user.admin_save", "user", id)
			writeJSON(w, map[string]any{"id": id})
		case http.MethodDelete:
			userKey := strings.TrimSpace(r.URL.Query().Get("user_key"))
			if userKey == "" {
				writeTenantError(w, http.StatusBadRequest, errMsgUserKeyRequired)
				return
			}
			if err := opts.TenantService.ArchiveTenantUser(r.Context(), userKey); err != nil {
				writeTenantServiceError(w, err)
				return
			}
			recordTenantAudit(r.Context(), opts.TenantService, "tenant.user.archive", "user", userKey)
			writeJSON(w, map[string]any{"archived": true})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func tenantMemoriesHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_memories", "server.tenantMemoriesHandler", "handle tenant memories")
		switch r.Method {
		case http.MethodGet:
			items, err := opts.TenantService.ListMemories(r.Context(), r.URL.Query().Get("category"), parseLimit(r.URL.Query().Get(paramLimit)))
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			writeJSON(w, map[string]any{"data": items})
		case http.MethodPost:
			var req tenantservice.MemoryRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeTenantError(w, http.StatusBadRequest, err.Error())
				return
			}
			if strings.TrimSpace(req.MemoryKey) == "" || strings.TrimSpace(req.Content) == "" {
				writeTenantError(w, http.StatusBadRequest, "memory_key and content are required")
				return
			}
			id, err := opts.TenantService.UpsertMemory(r.Context(), req)
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			recordTenantAudit(r.Context(), opts.TenantService, "tenant.memory.upsert", "memory", id)
			writeJSON(w, map[string]any{"id": id})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func tenantAutoMemoryCandidatesHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_automem_candidates", "server.tenantAutoMemoryCandidatesHandler", "handle auto memory candidates")
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		items, err := opts.TenantService.ListAutoMemoryCandidates(r.Context(), parseLimit(r.URL.Query().Get(paramLimit)))
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		writeJSON(w, map[string]any{"data": items})
	})
}

func tenantMemoryReviewCandidatesHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_memory_review_candidates", "server.tenantMemoryReviewCandidatesHandler", "handle memory review candidates")
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		items, err := opts.TenantService.ListMemoryReviewCandidatesFiltered(r.Context(), memoryReviewListOptions(r))
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		writeJSON(w, map[string]any{"data": items})
	})
}

func tenantMemoryReviewHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_memory_review", "server.tenantMemoryReviewHandler", "review memory candidate")
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req tenantservice.MemoryReviewRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeTenantError(w, http.StatusBadRequest, err.Error())
			return
		}
		id, err := opts.TenantService.ReviewMemoryCandidate(r.Context(), req)
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		recordTenantAudit(r.Context(), opts.TenantService, "tenant.memory.review", "memory", id)
		writeJSON(w, map[string]any{"id": id})
	})
}

func tenantAutoMemoryReviewHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_automem_review", "server.tenantAutoMemoryReviewHandler", "review auto memory candidate")
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req tenantservice.AutoMemoryReviewRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeTenantError(w, http.StatusBadRequest, err.Error())
			return
		}
		id, err := opts.TenantService.ReviewAutoMemory(r.Context(), req)
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		recordTenantAudit(r.Context(), opts.TenantService, "tenant.automem.review", "memory", id)
		writeJSON(w, map[string]any{"id": id})
	})
}

func tenantScopedMemoryHandler(opts Options, category string) http.HandlerFunc {
	category = strings.TrimSpace(category)
	return func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_"+category+"_memory", "server.tenantScopedMemoryHandler", "handle scoped tenant memory")
		if !authorize(w, r, opts.AuthToken) {
			return
		}
		if opts.TenantService == nil {
			writeTenantError(w, http.StatusServiceUnavailable, errMsgTenantStorageNotConfig)
			return
		}
		switch r.Method {
		case http.MethodGet:
			items, err := opts.TenantService.ListMemories(r.Context(), category, parseLimit(r.URL.Query().Get(paramLimit)))
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			writeJSON(w, map[string]any{"data": items})
		case http.MethodPost:
			if !requireTenantRole(w, r, opts, "owner", "admin") {
				return
			}
			var req tenantservice.MemoryRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeTenantError(w, http.StatusBadRequest, err.Error())
				return
			}
			if strings.TrimSpace(req.Content) == "" {
				writeTenantError(w, http.StatusBadRequest, errMsgContentRequired)
				return
			}
			req.Category = category
			if strings.TrimSpace(req.MemoryKey) == "" {
				req.MemoryKey = scopedMemoryKey(category, req.Content)
			}
			if strings.TrimSpace(req.Source) == "" {
				req.Source = "tenant-api"
			}
			id, err := opts.TenantService.UpsertMemory(r.Context(), req)
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			recordTenantAudit(r.Context(), opts.TenantService, "tenant."+category+"_memory.upsert", category+"_memory", id)
			writeJSON(w, map[string]any{"id": id})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

func scopedMemoryKey(category, content string) string {
	sum := sha256.Sum256([]byte(category + "\x00" + content))
	return category + "." + hex.EncodeToString(sum[:])[:16]
}

func tenantSkillsHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_skills", "server.tenantSkillsHandler", "handle tenant skills")
		switch r.Method {
		case http.MethodGet:
			skillKey := querySkillKey(r)
			if skillKey != "" {
				version, ok := parseOptionalUint(r.URL.Query().Get(paramVersion))
				if !ok {
					writeTenantError(w, http.StatusBadRequest, "version must be an unsigned integer")
					return
				}
				item, err := opts.TenantService.GetSkill(r.Context(), skillKey, version)
				if err != nil {
					writeTenantServiceError(w, err)
					return
				}
				writeJSON(w, item)
				return
			}
			enabledOnly, ok := parseOptionalBool(r.URL.Query().Get(paramEnabled))
			if !ok {
				writeTenantError(w, http.StatusBadRequest, "enabled must be a boolean")
				return
			}
			items, err := opts.TenantService.ListSkills(r.Context(), enabledOnly, parseLimit(r.URL.Query().Get(paramLimit)))
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			writeJSON(w, map[string]any{"data": items})
		case http.MethodPost:
			if !requireTenantRole(w, r, opts, "owner", "admin") {
				return
			}
			var req tenantservice.SkillRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeTenantError(w, http.StatusBadRequest, err.Error())
				return
			}
			if strings.TrimSpace(req.SkillKey) == "" || strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.ContentMD) == "" {
				writeTenantError(w, http.StatusBadRequest, "skill_key, name and content_md are required")
				return
			}
			id, err := opts.TenantService.UpsertSkill(r.Context(), req)
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			recordTenantAudit(r.Context(), opts.TenantService, "tenant.skill.upsert", "skill", id)
			writeJSON(w, map[string]any{"id": id})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func tenantSkillRollbackHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_skill_rollback", "server.tenantSkillRollbackHandler", "rollback tenant skill")
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !requireTenantRole(w, r, opts, "owner", "admin") {
			return
		}
		var req tenantservice.SkillRollbackRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeTenantError(w, http.StatusBadRequest, err.Error())
			return
		}
		if strings.TrimSpace(req.SkillKey) == "" || req.Version == 0 {
			writeTenantError(w, http.StatusBadRequest, "skill_key and version are required")
			return
		}
		result, err := opts.TenantService.RollbackSkill(r.Context(), req)
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		recordTenantAudit(r.Context(), opts.TenantService, "tenant.skill.rollback", "skill", result.ID)
		writeJSON(w, result)
	})
}

func tenantSkillPackageRenderHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_skill_package_render", "server.tenantSkillPackageRenderHandler", "render tenant skill package")
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !requireTenantRole(w, r, opts, "owner", "admin") {
			return
		}
		var req tenantservice.SkillPackageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeTenantError(w, http.StatusBadRequest, err.Error())
			return
		}
		result, err := opts.TenantService.RenderSkillPackage(r.Context(), req)
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		writeJSON(w, result)
	})
}

func tenantSkillPackagePublishHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_skill_package_publish", "server.tenantSkillPackagePublishHandler", "publish tenant skill package")
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !requireTenantRole(w, r, opts, "owner", "admin") {
			return
		}
		var req tenantservice.SkillPackageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeTenantError(w, http.StatusBadRequest, err.Error())
			return
		}
		result, err := opts.TenantService.PublishSkillPackage(r.Context(), req)
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		recordTenantAudit(r.Context(), opts.TenantService, "tenant.skill_package.publish", "skill", result.ID)
		writeJSON(w, result)
	})
}

func tenantSkillPackageVerifyRuntimeHandler(opts Options, queryFn QueryFunc) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_skill_package_verify_runtime", "server.tenantSkillPackageVerifyRuntimeHandler", "verify tenant skill package runtime")
		if queryFn == nil {
			writeTenantError(w, http.StatusServiceUnavailable, errMsgQueryHandlerNotConfig)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !requireTenantRole(w, r, opts, "owner", "admin") {
			return
		}
		var req SkillPackageVerifyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeTenantError(w, http.StatusBadRequest, err.Error())
			return
		}
		if strings.TrimSpace(req.SkillKey) == "" || strings.TrimSpace(req.SchemaName) == "" {
			writeTenantError(w, http.StatusBadRequest, "skill_key and schema_name are required")
			return
		}
		queryReq := skillPackageVerifyQueryRequest(req, opts.Workspace)
		result, err := queryFn(r.Context(), queryReq)
		metadata := openAITenantRuntimeMetadataForResponse(result.TenantRuntime)
		ok, verifyErr := verifyTenantRuntimeMetadata(req, metadata)
		resp := SkillPackageVerifyResponse{
			OK:            ok && err == nil,
			TraceID:       observability.TraceID(r.Context()),
			TenantRuntime: metadata,
		}
		if err != nil {
			resp.Error = err.Error()
		}
		if verifyErr != "" {
			resp.OK = false
			if resp.Error == "" {
				resp.Error = verifyErr
			} else {
				resp.Error += "; " + verifyErr
			}
		}
		status := http.StatusOK
		if !resp.OK {
			status = http.StatusBadGateway
		}
		writeJSONStatus(w, status, resp)
	})
}

func skillPackageVerifyQueryRequest(req SkillPackageVerifyRequest, workspace string) QueryRequest {
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		prompt = "Verify that the selected tenant skill runtime is loaded. Return a minimal JSON object."
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = config.ResolveModel(workspace, "")
	}
	schemaName := strings.TrimSpace(req.SchemaName)
	schema := json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}`)
	return QueryRequest{
		Prompt:                  prompt,
		Model:                   model,
		CWD:                     workspace,
		PromptMode:              promptmode.Chat.String(),
		MaxTurns:                1,
		DisableTools:            true,
		SkipAutoTitle:           true,
		InlineTenantSkills:      []string{strings.TrimSpace(req.SkillKey)},
		InlineTenantSkillSource: "verify_runtime",
		ResponseFormat: &OpenAIResponseFormat{
			Type: "json_schema",
			JSONSchema: &OpenAIResponseFormatSchema{
				Name:   schemaName,
				Schema: schema,
				Strict: true,
			},
		},
	}
}

func verifyTenantRuntimeMetadata(req SkillPackageVerifyRequest, runtime *openAITenantRuntimeMetadata) (bool, string) {
	if runtime == nil || !runtime.HasMetadata() {
		return false, "tenant_runtime metadata is missing"
	}
	skillKey := strings.TrimSpace(req.SkillKey)
	if !stringSliceContains(runtime.LoadedKeys, skillKey) {
		return false, "tenant_runtime.loaded_keys does not include " + skillKey
	}
	if req.ExpectedVersion > 0 && !stringSliceContains(runtime.Versions, strconv.FormatUint(uint64(req.ExpectedVersion), 10)) {
		return false, "tenant_runtime.versions does not include expected version"
	}
	if expected := strings.TrimSpace(req.ExpectedPackageSHA256); expected != "" && !stringSliceContains(runtime.PackageSHA256, expected) {
		return false, "tenant_runtime.package_sha256 does not include expected hash"
	}
	if runtime.Bytes <= 0 {
		return false, "tenant_runtime.bytes must be greater than 0"
	}
	return true, ""
}

func stringSliceContains(values []string, want string) bool {
	want = strings.TrimSpace(want)
	if want == "" {
		return false
	}
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}
	return false
}

func tenantSkillOverridesHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_skill_overrides", "server.tenantSkillOverridesHandler", "handle tenant skill overrides")
		switch r.Method {
		case http.MethodGet:
			skillKey := querySkillKey(r)
			if skillKey != "" {
				version, ok := parseOptionalUint(r.URL.Query().Get(paramVersion))
				if !ok {
					writeTenantError(w, http.StatusBadRequest, "version must be an unsigned integer")
					return
				}
				item, err := opts.TenantService.GetSkillOverride(r.Context(), skillKey, version)
				if err != nil {
					writeTenantServiceError(w, err)
					return
				}
				writeJSON(w, item)
				return
			}
			items, err := opts.TenantService.ListSkillOverrides(r.Context(), parseLimit(r.URL.Query().Get(paramLimit)))
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			writeJSON(w, map[string]any{"data": items})
		case http.MethodPost:
			var req tenantservice.SkillOverrideRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeTenantError(w, http.StatusBadRequest, err.Error())
				return
			}
			if strings.TrimSpace(req.SkillKey) == "" {
				writeTenantError(w, http.StatusBadRequest, "skill_key is required")
				return
			}
			id, err := opts.TenantService.UpsertSkillOverride(r.Context(), req)
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			recordTenantAudit(r.Context(), opts.TenantService, "tenant.skill_override.upsert", "skill_override", id)
			writeJSON(w, map[string]any{"id": id})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func tenantEffectiveSkillsHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_effective_skills", "server.tenantEffectiveSkillsHandler", "handle effective tenant skills")
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		skillKey := querySkillKey(r)
		if skillKey != "" {
			version, ok := parseOptionalUint(r.URL.Query().Get(paramVersion))
			if !ok {
				writeTenantError(w, http.StatusBadRequest, "version must be an unsigned integer")
				return
			}
			item, err := opts.TenantService.GetEffectiveSkill(r.Context(), skillKey, version)
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			writeJSON(w, item)
			return
		}
		enabledOnly, ok := parseOptionalBool(r.URL.Query().Get(paramEnabled))
		if !ok {
			writeTenantError(w, http.StatusBadRequest, "enabled must be a boolean")
			return
		}
		items, err := opts.TenantService.ListEffectiveSkills(r.Context(), enabledOnly, parseLimit(r.URL.Query().Get(paramLimit)))
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		writeJSON(w, map[string]any{"data": items})
	})
}

func tenantDocumentsHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_documents", "server.tenantDocumentsHandler", "handle tenant documents")
		switch r.Method {
		case http.MethodGet:
			docType := r.URL.Query().Get("type")
			history, ok := parseOptionalBool(r.URL.Query().Get("history"))
			if !ok {
				writeTenantError(w, http.StatusBadRequest, "history must be a boolean")
				return
			}
			if history || strings.TrimSpace(docType) == "" {
				items, err := opts.TenantService.ListDocuments(r.Context(), docType, parseLimit(r.URL.Query().Get(paramLimit)))
				if err != nil {
					writeTenantServiceError(w, err)
					return
				}
				writeJSON(w, map[string]any{"data": items})
				return
			}
			doc, err := opts.TenantService.GetActiveDocument(r.Context(), docType)
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			writeJSON(w, doc)
		case http.MethodPost:
			var req tenantservice.DocumentRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeTenantError(w, http.StatusBadRequest, err.Error())
				return
			}
			if strings.TrimSpace(req.DocType) == "" {
				writeTenantError(w, http.StatusBadRequest, "doc_type is required")
				return
			}
			id, err := opts.TenantService.SaveDocument(r.Context(), req)
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			recordTenantAudit(r.Context(), opts.TenantService, "tenant.document.save", "document", id)
			writeJSON(w, map[string]any{"id": id})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func tenantKnowledgeDocumentsHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_knowledge_documents", "server.tenantKnowledgeDocumentsHandler", "handle tenant knowledge documents")
		switch r.Method {
		case http.MethodGet:
			items, err := opts.TenantService.ListKnowledgeDocuments(r.Context(), parseLimit(r.URL.Query().Get(paramLimit)))
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			writeJSON(w, map[string]any{"data": items})
		case http.MethodPost:
			var req tenantservice.KnowledgeDocumentRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeTenantError(w, http.StatusBadRequest, err.Error())
				return
			}
			if strings.TrimSpace(req.Content) == "" {
				writeTenantError(w, http.StatusBadRequest, errMsgContentRequired)
				return
			}
			id, err := opts.TenantService.SaveKnowledgeDocument(r.Context(), req)
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			recordTenantAudit(r.Context(), opts.TenantService, "tenant.knowledge.save", "knowledge_document", id)
			writeJSON(w, map[string]any{"id": id})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func tenantKnowledgeSearchHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_knowledge_search", "server.tenantKnowledgeSearchHandler", "handle tenant knowledge search")
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req tenantservice.KnowledgeSearchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeTenantError(w, http.StatusBadRequest, err.Error())
			return
		}
		if strings.TrimSpace(req.Query) == "" {
			writeTenantError(w, http.StatusBadRequest, "query is required")
			return
		}
		items, err := opts.TenantService.SearchKnowledgeChunks(r.Context(), req)
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		writeJSON(w, map[string]any{"data": items})
	})
}

func tenantProfileHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_profile", "server.tenantProfileHandler", "handle tenant profile")
		switch r.Method {
		case http.MethodGet:
			version, ok := parseOptionalUint(r.URL.Query().Get(paramVersion))
			if !ok {
				writeTenantError(w, http.StatusBadRequest, "version must be an unsigned integer")
				return
			}
			profile, err := opts.TenantService.GetProfile(r.Context(), version)
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			writeJSON(w, profile)
		case http.MethodPost:
			var req tenantservice.ProfileRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeTenantError(w, http.StatusBadRequest, err.Error())
				return
			}
			if strings.TrimSpace(req.ProfileJSON) == "" {
				writeTenantError(w, http.StatusBadRequest, "profile_json is required")
				return
			}
			id, err := opts.TenantService.SaveProfile(r.Context(), req)
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			recordTenantAudit(r.Context(), opts.TenantService, "tenant.profile.save", "profile", id)
			writeJSON(w, map[string]any{"id": id})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}
