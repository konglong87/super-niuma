package server

// @Summary List prompt templates
// @Description Success returns JSON; errors return plain text.
// @Tags tenant
// @Security ApiKeyAuth
// @Produce json,plain
// @Param search query string false "Search title, content or category"
// @Param category query string false "Exact category"
// @Param limit query int false "Maximum items (1-200, default 100)"
// @Success 200 {array} SwaggerPromptTemplate
// @Failure 401 {string} string
// @Failure 403 {string} string
// @Router /tenant/prompt-templates [get]
func swaggerPromptTemplates() {}

// @Summary Create or update a prompt template by owner and title
// @Description Success returns JSON; errors return plain text.
// @Tags tenant
// @Security ApiKeyAuth
// @Accept json
// @Produce json,plain
// @Param request body SwaggerPromptTemplateRequest true "Template; omit ID to upsert by owner/title"
// @Success 200 {object} SwaggerPromptTemplate
// @Failure 400 {string} string
// @Failure 401 {string} string
// @Failure 403 {string} string
// @Failure 404 {string} string
// @Failure 409 {string} string
// @Router /tenant/prompt-templates [post]
func swaggerCreatePromptTemplate() {}

// @Summary Update an owned prompt template
// @Description Success returns JSON; errors return plain text.
// @Tags tenant
// @Security ApiKeyAuth
// @Accept json
// @Produce json,plain
// @Param request body SwaggerPromptTemplateRequest true "Template with a nonzero ID"
// @Success 200 {object} SwaggerPromptTemplate
// @Failure 400 {string} string
// @Failure 401 {string} string
// @Failure 403 {string} string
// @Failure 404 {string} string
// @Failure 409 {string} string
// @Router /tenant/prompt-templates [patch]
func swaggerUpdatePromptTemplate() {}

// @Summary Delete an owned prompt template
// @Description Success has no body; errors return plain text.
// @Tags tenant
// @Security ApiKeyAuth
// @Produce plain
// @Param id path int true "Prompt template ID"
// @Success 204
// @Failure 400 {string} string
// @Failure 401 {string} string
// @Failure 403 {string} string
// @Failure 404 {string} string
// @Router /tenant/prompt-templates/{id} [delete]
func swaggerPromptTemplateItem() {}

// swaggerHealth godoc
// @Summary Health check
// @Tags System
// @Security ApiKeyAuth
// @Produce json
// @Success 200 {object} SwaggerHealthResponse
// @Failure 401 {object} SwaggerError
// @Router /health [get]
func swaggerHealth() {}

// swaggerLiveness godoc
// @Summary Liveness probe
// @Description 进程存活探针。不鉴权、不探任何依赖，只要 HTTP server 还在响应就返回 200，供 k8s livenessProbe 与 LB 使用。
// @Tags System
// @Produce json
// @Success 200 {object} SwaggerLivenessResponse
// @Router /livez [get]
func swaggerLiveness() {}

// swaggerReadiness godoc
// @Summary Readiness probe
// @Description 就绪探针。不鉴权，逐个探活已配置的依赖（MySQL、配额 Redis、mobile 用量 Redis），任一不可用返回 503。响应只含 ok/unavailable，不泄漏错误详情。
// @Tags System
// @Produce json
// @Success 200 {object} SwaggerReadinessResponse
// @Failure 503 {object} SwaggerReadinessResponse
// @Router /readyz [get]
func swaggerReadiness() {}

// swaggerStatus godoc
// @Summary Runtime status snapshot
// @Tags System
// @Security ApiKeyAuth
// @Produce json
// @Success 200 {object} SwaggerSnapshotResponse
// @Failure 401 {object} SwaggerError
// @Router /status [get]
func swaggerStatus() {}

// swaggerMetrics godoc
// @Summary Export Prometheus telemetry metrics
// @Tags System
// @Security ApiKeyAuth
// @Produce plain
// @Success 200 {string} string "Prometheus text format"
// @Failure 401 {object} SwaggerError
// @Router /metrics [get]
func swaggerMetrics() {}

// swaggerTools godoc
// @Summary Tool definitions snapshot
// @Tags System
// @Security ApiKeyAuth
// @Produce json
// @Success 200 {object} SwaggerSnapshotResponse
// @Failure 401 {object} SwaggerError
// @Router /tools [get]
func swaggerTools() {}

// swaggerSessions godoc
// @Summary Local session snapshot
// @Tags System
// @Security ApiKeyAuth
// @Produce json
// @Success 200 {object} SwaggerSnapshotResponse
// @Failure 401 {object} SwaggerError
// @Router /sessions [get]
func swaggerSessions() {}

// swaggerRuntimeBackgroundList godoc
// @Summary List local background jobs and loops
// @Tags Runtime
// @Security ApiKeyAuth
// @Produce json
// @Param kind query string false "Filter by background job kind, e.g. loop"
// @Param tail query int false "Log tail bytes to include"
// @Param limit query int false "Maximum jobs to return"
// @Success 200 {object} SwaggerRuntimeBackgroundListResponse
// @Failure 401 {object} SwaggerError
// @Failure 500 {object} SwaggerError
// @Router /runtime/background [get]
func swaggerRuntimeBackgroundList() {}

// swaggerRuntimeBackgroundCreateLoop godoc
// @Summary Create a local loop
// @Tags Runtime
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param request body SwaggerRuntimeLoopRequest true "Loop request"
// @Success 200 {object} RuntimeBackgroundJob
// @Failure 400 {object} SwaggerError
// @Failure 401 {object} SwaggerError
// @Failure 500 {object} SwaggerError
// @Router /runtime/background [post]
func swaggerRuntimeBackgroundCreateLoop() {}

// swaggerRuntimeBackgroundUpdateLoop godoc
// @Summary Update a local loop
// @Tags Runtime
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param id path string true "Background job id or schedule id"
// @Param request body SwaggerRuntimeLoopRequest true "Loop request"
// @Success 200 {object} RuntimeBackgroundJob
// @Failure 400 {object} SwaggerError
// @Failure 401 {object} SwaggerError
// @Failure 404 {object} SwaggerError
// @Failure 500 {object} SwaggerError
// @Router /runtime/background/{id} [patch]
func swaggerRuntimeBackgroundUpdateLoop() {}

// swaggerRuntimeBackgroundLogs godoc
// @Summary Read local background job logs
// @Tags Runtime
// @Security ApiKeyAuth
// @Produce json
// @Param id path string true "Background job id"
// @Param tail query int false "Log tail bytes"
// @Success 200 {object} SwaggerRuntimeBackgroundLogsResponse
// @Failure 401 {object} SwaggerError
// @Failure 404 {object} SwaggerError
// @Failure 500 {object} SwaggerError
// @Router /runtime/background/{id}/logs [get]
func swaggerRuntimeBackgroundLogs() {}

// swaggerRuntimeBackgroundRunNow godoc
// @Summary Run a local loop once now
// @Tags Runtime
// @Security ApiKeyAuth
// @Produce json
// @Param id path string true "Background job id or schedule id"
// @Success 200 {object} RuntimeBackgroundJob
// @Failure 401 {object} SwaggerError
// @Failure 404 {object} SwaggerError
// @Failure 500 {object} SwaggerError
// @Router /runtime/background/{id}/run [post]
func swaggerRuntimeBackgroundRunNow() {}

// swaggerRuntimeBackgroundRuns godoc
// @Summary List local loop run history
// @Tags Runtime
// @Security ApiKeyAuth
// @Produce json
// @Param id path string true "Background job id or schedule id"
// @Param limit query int false "Maximum run records"
// @Success 200 {object} SwaggerRuntimeBackgroundRunsResponse
// @Failure 401 {object} SwaggerError
// @Failure 500 {object} SwaggerError
// @Router /runtime/background/{id}/runs [get]
func swaggerRuntimeBackgroundRuns() {}

// swaggerRuntimeBackgroundEvents godoc
// @Summary Read local scheduler events
// @Tags Runtime
// @Security ApiKeyAuth
// @Produce json
// @Param offset query int false "Byte offset returned by previous response"
// @Param limit query int false "Maximum event records"
// @Success 200 {object} SwaggerRuntimeBackgroundEventsResponse
// @Failure 401 {object} SwaggerError
// @Failure 500 {object} SwaggerError
// @Router /runtime/background/events [get]
func swaggerRuntimeBackgroundEvents() {}

// swaggerRuntimeBackgroundStop godoc
// @Summary Stop a local background job or loop
// @Tags Runtime
// @Security ApiKeyAuth
// @Produce json
// @Param id path string true "Background job id or schedule id"
// @Success 200 {object} SwaggerRuntimeBackgroundStopResponse
// @Failure 401 {object} SwaggerError
// @Failure 404 {object} SwaggerError
// @Failure 500 {object} SwaggerError
// @Router /runtime/background/{id}/stop [post]
func swaggerRuntimeBackgroundStop() {}

// swaggerRuntimeSettingsGet godoc
// @Summary Read the global settings.json document
// @Description Returns the raw global settings document with secret values masked. `masked` lists the dotted paths whose values were replaced.
// @Tags Runtime
// @Security ApiKeyAuth
// @Produce json
// @Success 200 {object} GlobalSettingsResponse
// @Failure 401 {object} SwaggerError
// @Failure 500 {object} SwaggerError
// @Router /runtime/settings [get]
func swaggerRuntimeSettingsGet() {}

// swaggerRuntimeSettingsPut godoc
// @Summary Replace the global settings.json document
// @Description Accepts a full settings object. Unknown keys are preserved, but a type mismatch on a known field is rejected before the file is touched. POST behaves identically to PUT.
// @Tags Runtime
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param request body object true "Full global settings document"
// @Param If-Match header string false "Revision from GET (raw or quoted ETag); stale revisions return 409"
// @Param X-Settings-Promoted-Provider-Index header integer false "Fallback provider index used to restore promoted credentials during validation and save"
// @Success 200 {object} GlobalSettingsSaveResponse
// @Failure 400 {object} SwaggerError
// @Failure 401 {object} SwaggerError
// @Failure 409 {object} SwaggerError
// @Failure 500 {object} SwaggerError
// @Router /runtime/settings [put]
func swaggerRuntimeSettingsPut() {}

// swaggerRuntimeSettingsValidate godoc
// @Summary Validate a global settings document without saving or network requests
// @Tags Runtime
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param request body object true "Full global settings document"
// @Success 200 {object} SettingsValidationResponse
// @Failure 400 {object} SwaggerError
// @Failure 401 {object} SwaggerError
// @Router /runtime/settings/validate [post]
func swaggerRuntimeSettingsValidate() {}

// swaggerRuntimeSettingsPromoteProvider godoc
// @Summary Promote a fallback provider into the primary settings route
// @Description Returns a masked draft with the selected fallback provider copied to the top-level route. The global settings file is not changed until PUT /runtime/settings.
// @Tags Runtime
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param request body GlobalSettingsPromoteRequest true "Current settings draft and fallback provider index"
// @Success 200 {object} GlobalSettingsPromoteResponse
// @Failure 400 {object} SwaggerError
// @Failure 401 {object} SwaggerError
// @Failure 500 {object} SwaggerError
// @Router /runtime/settings/promote-provider [post]
func swaggerRuntimeSettingsPromoteProvider() {}

// swaggerRuntimeSettingsEffective godoc
// @Summary Inspect global file settings and safe server startup configuration
// @Description File settings use only the global settings.json (NIUMA_CONFIG_DIR relocation supported), without workspace or legacy discovery. File sources exclude process environment, CLI, profile and run overrides. Startup or status callback values do not represent active run configuration.
// @Tags Runtime
// @Security ApiKeyAuth
// @Produce json
// @Success 200 {object} EffectiveSettingsResponse
// @Failure 401 {object} SwaggerError
// @Failure 500 {object} SwaggerError
// @Router /runtime/settings/effective [get]
func swaggerRuntimeSettingsEffective() {}

// swaggerRuntimeSettingsTestProvider godoc
// @Summary Test provider models catalog connectivity
// @Description Restores masked credentials and probes the provider models catalog with a ten second timeout. Never saves settings, follows redirects, or performs inference. A catalog failure does not prove inference is unavailable.
// @Tags Runtime
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param request body SettingsProviderTestRequest true "Settings document and optional named provider"
// @Success 200 {object} SettingsProviderTestResponse
// @Failure 400 {object} SwaggerError
// @Failure 401 {object} SwaggerError
// @Router /runtime/settings/test-provider [post]
func swaggerRuntimeSettingsTestProvider() {}

// swaggerSettingsEnvironments godoc
// @Summary List authorized settings environments and shared global file scope
// @Tags Runtime
// @Produce json
// @Security BearerAuth
// @Param X-Tenant-Key header string true "Source tenant key"
// @Param X-User-Id header string true "Source operator user key"
// @Success 200 {object} SettingsEnvironmentsResponse
// @Failure 401 {object} SwaggerError
// @Failure 403 {object} SwaggerError
// @Router /runtime/settings/environments [get]
func swaggerSettingsEnvironments() {}

// swaggerSettingsEnvironmentResource godoc
// @Summary Use existing Profile management APIs in a preconfigured environment
// @Description Source identity must match the server allowlist and be owner/admin. Target tenant/user and database are fixed by server configuration. Resource permits tenant/agent-profiles and its existing lifecycle subpaths, tenant/agent-profile-assignment, and GET-only tenant/channel-accounts and tenant/messages. Global settings remain on the original shared runtime/settings endpoint. No chat execution or worker lifecycle routes are exposed. Responses and request bodies retain each delegated API contract.
// @Tags Runtime
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param X-Tenant-Key header string true "Source tenant key"
// @Param X-User-Id header string true "Source operator user key"
// @Param environment path string true "Preconfigured environment id"
// @Param resource path string true "Existing settings resource path, for example tenant/agent-profiles"
// @Param request body object false "Existing resource request body"
// @Success 200 {object} object
// @Failure 400 {object} SwaggerError
// @Failure 401 {object} SwaggerError
// @Failure 403 {object} SwaggerError
// @Failure 404 {object} SwaggerError
// @Failure 503 {object} SwaggerError
// @Router /runtime/settings/environments/{environment}/{resource} [get]
// @Router /runtime/settings/environments/{environment}/{resource} [post]
// @Router /runtime/settings/environments/{environment}/{resource} [put]
// @Router /runtime/settings/environments/{environment}/{resource} [patch]
// @Router /runtime/settings/environments/{environment}/{resource} [delete]
func swaggerSettingsEnvironmentResource() {}

// swaggerQuery godoc
// @Summary Run a prompt through the local query loop
// @Tags Query
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param request body QueryRequest true "Query request"
// @Success 200 {object} SwaggerQueryResult
// @Failure 400 {object} SwaggerError
// @Failure 401 {object} SwaggerError
// @Failure 500 {object} SwaggerError
// @Router /query [post]
func swaggerQuery() {}

// swaggerOpenAIModels godoc
// @Summary List OpenAI-compatible models
// @Description Lists the models this deployment can actually serve: the configured providers' models, falling back to the built-in Anthropic list only when nothing is configured. It is not a fixed `claude-*` list.
// @Tags OpenAI Compatible
// @Security ApiKeyAuth
// @Produce json
// @Success 200 {object} SwaggerOpenAIModelsResponse
// @Failure 401 {object} SwaggerError
// @Router /v1/models [get]
func swaggerOpenAIModels() {}

// swaggerAgentProviders godoc
// @Summary List configured providers
// @Description Names and default models of the providers this deployment is configured with. Credentials are never included.
// @Tags OpenAI Compatible
// @Security ApiKeyAuth
// @Produce json
// @Success 200 {object} SwaggerProviderListResponse
// @Failure 401 {object} SwaggerError
// @Router /v1/providers [get]
func swaggerAgentProviders() {}

// swaggerAgentProvisionings godoc
// @Summary Manage profile agent provisioning sessions
// @Tags Tenant Agent Provisioning
// @Security ApiKeyAuth
// @Produce json
// @Router /tenant/agent-provisionings [get]
// @Router /tenant/agent-provisionings [post]
// @Router /tenant/agent-provisionings/overview [get]
// @Router /tenant/agent-provisionings/{id} [get]
// @Router /tenant/agent-provisionings/{id}/preflight [post]
// @Router /tenant/agent-provisionings/{id}/logs [get]
// @Router /tenant/agent-provisionings/{id}/{action} [post]
func swaggerAgentProvisionings() {}

// swaggerFeishuOnboarding godoc
// @Summary Create a Feishu bot through device authorization
// @Description Starts the shared Feishu onboarding flow, exposes a redacted QR session, and persists the protected credentials after approval.
// @Tags Feishu Onboarding
// @Security ApiKeyAuth
// @Produce json
// @Router /tenant/feishu/onboarding [post]
// @Router /tenant/feishu/onboarding/cli [get]
// @Router /tenant/feishu/onboarding/cli/install [post]
// @Router /tenant/feishu/onboarding/{id} [get]
// @Router /tenant/feishu/onboarding/{id}/cancel [post]
func swaggerFeishuOnboarding() {}

// swaggerAgentProfileConversations godoc
// @Summary List conversations for an Agent Profile
// @Description Returns safe DM/group conversation summaries and Team links for a tenant-visible profile.
// @Tags Agent Profiles
// @Security ApiKeyAuth
// @Produce json
// @Param key path string true "Profile key"
// @Param version query int false "Profile version"
// @Param limit query int false "Maximum number of conversations"
// @Success 200 {object} SwaggerAgentProfileConversationCatalog
// @Failure 400 {object} SwaggerError
// @Failure 401 {object} SwaggerError
// @Router /tenant/agent-profiles/{key}/conversations [get]
func swaggerAgentProfileConversations() {}

// swaggerOpenAIChatCompletions godoc
// @Summary Create an OpenAI-compatible chat completion
// @Tags OpenAI Compatible
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Skill-Key header string false "Comma-separated tenant skill keys for structured JSON schema fast path; overrides metadata, tenant settings, env routes, and compatibility fallback"
// @Param request body OpenAIChatRequest true "OpenAI-compatible chat request"
// @Success 200 {object} SwaggerOpenAIChatResponse
// @Failure 400 {object} SwaggerError
// @Failure 401 {object} SwaggerError
// @Failure 500 {object} SwaggerError
// @Router /v1/chat/completions [post]
func swaggerOpenAIChatCompletions() {}

// swaggerTenantContext godoc
// @Summary Resolve current tenant context
// @Tags Tenant
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Success 200 {object} SwaggerTenantContext
// @Failure 400 {object} SwaggerError
// @Failure 404 {object} SwaggerError
// @Router /tenant/context [get]
func swaggerTenantContext() {}

// swaggerTenantTenantsList godoc
// @Summary List tenants
// @Tags Tenant Admin
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Current tenant key"
// @Param X-User-Id header string true "Admin user key"
// @Param limit query int false "Limit"
// @Param cursor query int false "Offset cursor returned by the previous page"
// @Param search query string false "Search tenant key, name, or status"
// @Success 200 {object} SwaggerListTenantsResponse
// @Failure 403 {object} SwaggerError
// @Router /tenant/tenants [get]
func swaggerTenantTenantsList() {}

// swaggerTenantTenantsSave godoc
// @Summary Create or update a tenant
// @Tags Tenant Admin
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Current tenant key"
// @Param X-User-Id header string true "Admin user key"
// @Param request body SwaggerTenantRequest true "Tenant request"
// @Success 200 {object} SwaggerIDResponse
// @Failure 400 {object} SwaggerError
// @Failure 403 {object} SwaggerError
// @Router /tenant/tenants [post]
func swaggerTenantTenantsSave() {}

// swaggerTenantTenantsPatch godoc
// @Summary Patch a tenant
// @Tags Tenant Admin
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Current tenant key"
// @Param X-User-Id header string true "Admin user key"
// @Param request body SwaggerTenantRequest true "Tenant request"
// @Success 200 {object} SwaggerIDResponse
// @Failure 400 {object} SwaggerError
// @Failure 403 {object} SwaggerError
// @Router /tenant/tenants [patch]
func swaggerTenantTenantsPatch() {}

// swaggerTenantTenantsArchive godoc
// @Summary Archive a tenant
// @Tags Tenant Admin
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Current tenant key"
// @Param X-User-Id header string true "Admin user key"
// @Param tenant_key query string true "Tenant key to archive"
// @Success 200 {object} SwaggerArchiveResponse
// @Failure 400 {object} SwaggerError
// @Failure 403 {object} SwaggerError
// @Router /tenant/tenants [delete]
func swaggerTenantTenantsArchive() {}

// swaggerTenantUserGet godoc
// @Summary Get current tenant user
// @Tags Tenant Users
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Success 200 {object} SwaggerTenantUser
// @Failure 400 {object} SwaggerError
// @Failure 404 {object} SwaggerError
// @Router /tenant/user [get]
func swaggerTenantUserGet() {}

// swaggerTenantUserSave godoc
// @Summary Create or update current tenant user
// @Tags Tenant Users
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param request body SwaggerTenantUserRequest true "User request"
// @Success 200 {object} SwaggerIDResponse
// @Failure 400 {object} SwaggerError
// @Router /tenant/user [post]
func swaggerTenantUserSave() {}

// swaggerTenantUserPatch godoc
// @Summary Patch current tenant user
// @Tags Tenant Users
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param request body SwaggerTenantUserRequest true "User request"
// @Success 200 {object} SwaggerIDResponse
// @Failure 400 {object} SwaggerError
// @Router /tenant/user [patch]
func swaggerTenantUserPatch() {}

// swaggerTenantUsersList godoc
// @Summary List tenant users
// @Tags Tenant Users
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "Admin user key"
// @Param limit query int false "Limit"
// @Param cursor query int false "Offset cursor returned by the previous page"
// @Param search query string false "Search user key, email, display name, role, or status"
// @Success 200 {object} SwaggerListUsersResponse
// @Failure 403 {object} SwaggerError
// @Router /tenant/users [get]
func swaggerTenantUsersList() {}

// swaggerTenantUsersSave godoc
// @Summary Create or update a tenant user
// @Tags Tenant Users
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "Admin user key"
// @Param request body SwaggerTenantUserRequest true "User request"
// @Success 200 {object} SwaggerIDResponse
// @Failure 403 {object} SwaggerError
// @Router /tenant/users [post]
func swaggerTenantUsersSave() {}

// swaggerTenantUsersPatch godoc
// @Summary Patch a tenant user
// @Tags Tenant Users
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "Admin user key"
// @Param request body SwaggerTenantUserRequest true "User request"
// @Success 200 {object} SwaggerIDResponse
// @Failure 403 {object} SwaggerError
// @Router /tenant/users [patch]
func swaggerTenantUsersPatch() {}

// swaggerTenantUsersArchive godoc
// @Summary Archive a tenant user
// @Tags Tenant Users
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "Admin user key"
// @Param user_key query string true "User key to archive"
// @Success 200 {object} SwaggerArchiveResponse
// @Failure 403 {object} SwaggerError
// @Router /tenant/users [delete]
func swaggerTenantUsersArchive() {}

// swaggerTenantMemoriesList godoc
// @Summary List tenant memories
// @Tags Tenant Memories
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param category query string false "Memory category"
// @Param limit query int false "Limit"
// @Success 200 {object} SwaggerListMemoriesResponse
// @Router /tenant/memories [get]
func swaggerTenantMemoriesList() {}

// swaggerTenantMemoriesSave godoc
// @Summary Upsert tenant memory
// @Tags Tenant Memories
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param request body SwaggerTenantMemoryRequest true "Memory request"
// @Success 200 {object} SwaggerIDResponse
// @Router /tenant/memories [post]
func swaggerTenantMemoriesSave() {}

// swaggerTenantMemoryReviewCandidates godoc
// @Summary List pending memory review candidates
// @Description Lists explicit remember and AutoMem pending candidates. Pending candidates are not injected into prompt context until approved.
// @Tags Tenant Memories
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param limit query int false "Limit"
// @Param candidate_type query string false "Candidate type: explicit_remember or automem_candidate"
// @Param risk_status query string false "Risk status, for example needs_review or low_risk"
// @Param source_session_id query int false "Source session ID"
// @Success 200 {object} SwaggerListMemoriesResponse
// @Router /tenant/memory-review/candidates [get]
func swaggerTenantMemoryReviewCandidates() {}

// swaggerTenantMemoryReview godoc
// @Summary Approve, reject, or archive a memory review candidate
// @Description Approving creates an active user memory. Reject/archive keep the candidate out of prompt context.
// @Tags Tenant Memories
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param request body SwaggerMemoryReviewRequest true "Memory review request"
// @Success 200 {object} SwaggerIDResponse
// @Failure 400 {object} SwaggerError
// @Router /tenant/memory-review/review [post]
func swaggerTenantMemoryReview() {}

// swaggerTenantAutoMemoryCandidates godoc
// @Summary List pending AutoMem candidates
// @Tags Tenant Memories
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param limit query int false "Limit"
// @Success 200 {object} SwaggerListMemoriesResponse
// @Router /tenant/automem/candidates [get]
func swaggerTenantAutoMemoryCandidates() {}

// swaggerTenantAutoMemoryReview godoc
// @Summary Approve or reject an AutoMem candidate
// @Tags Tenant Memories
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param request body SwaggerAutoMemoryReviewRequest true "AutoMem review request"
// @Success 200 {object} SwaggerIDResponse
// @Failure 400 {object} SwaggerError
// @Router /tenant/automem/review [post]
func swaggerTenantAutoMemoryReview() {}

// swaggerTenantTeamMemoryList godoc
// @Summary List tenant team memory
// @Tags Tenant Memory
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param limit query int false "Limit"
// @Success 200 {object} SwaggerListMemoriesResponse
// @Router /tenant/team-memory [get]
func swaggerTenantTeamMemoryList() {}

// swaggerTenantTeamMemorySave godoc
// @Summary Save tenant team memory
// @Tags Tenant Memory
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "Admin user key"
// @Param request body SwaggerTenantMemoryRequest true "Memory request. Category is forced to team."
// @Success 200 {object} SwaggerIDResponse
// @Failure 403 {object} SwaggerError
// @Router /tenant/team-memory [post]
func swaggerTenantTeamMemorySave() {}

// swaggerTenantManagedMemoryList godoc
// @Summary List tenant managed memory
// @Tags Tenant Memory
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param limit query int false "Limit"
// @Success 200 {object} SwaggerListMemoriesResponse
// @Router /tenant/managed-memory [get]
func swaggerTenantManagedMemoryList() {}

// swaggerTenantManagedMemorySave godoc
// @Summary Save tenant managed memory
// @Tags Tenant Memory
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "Admin user key"
// @Param request body SwaggerTenantMemoryRequest true "Memory request. Category is forced to managed."
// @Success 200 {object} SwaggerIDResponse
// @Failure 403 {object} SwaggerError
// @Router /tenant/managed-memory [post]
func swaggerTenantManagedMemorySave() {}

// swaggerLocalSkillsList godoc
// @Summary List locally installed skills
// @Description Lists skills discovered from the current workspace, user skill roots, bundled skills, MCP roots, and installed plugins. These records are read-only local state and are not tenant skill records.
// @Tags Local Skills
// @Security ApiKeyAuth
// @Produce json
// @Success 200 {object} SwaggerListLocalSkillsResponse
// @Failure 401 {string} string "Unauthorized"
// @Failure 405 {string} string "Method not allowed"
// @Failure 500 {string} string "Local discovery failed"
// @Router /local/skills [get]
func swaggerLocalSkillsList() {}

// swaggerTenantSkillsList godoc
// @Summary List tenant skills
// @Tags Tenant Skills
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param skill_key query string false "Skill key. When present, returns one skill instead of list."
// @Param version query int false "Skill version for detail lookup"
// @Param enabled query bool false "Only enabled skills"
// @Param limit query int false "Limit"
// @Success 200 {object} SwaggerListSkillsResponse
// @Router /tenant/skills [get]
func swaggerTenantSkillsList() {}

// swaggerTenantSkillsSave godoc
// @Summary Upsert tenant skill
// @Tags Tenant Skills
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "Admin user key"
// @Param request body SwaggerTenantSkillRequest true "Skill request"
// @Success 200 {object} SwaggerIDResponse
// @Failure 403 {object} SwaggerError
// @Router /tenant/skills [post]
func swaggerTenantSkillsSave() {}

// swaggerTenantSkillsRollback godoc
// @Summary Roll back tenant skill by copying a historical version into a new latest version
// @Tags Tenant Skills
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "Admin user key"
// @Param request body SwaggerSkillRollbackRequest true "Skill rollback request"
// @Success 200 {object} SwaggerSkillRollbackResponse
// @Failure 403 {object} SwaggerError
// @Router /tenant/skills/rollback [post]
func swaggerTenantSkillsRollback() {}

// swaggerTenantSkillPackageImport godoc
// @Summary Import a tenant skill package without publishing it
// @Description Validates a local source_path on the server or a base64-encoded package.skill.zip and returns deterministic runtime.md plus manifest metadata without writing a tenant skill version.
// @Tags Tenant Skills
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "Admin user key"
// @Param request body SwaggerTenantSkillPackageRequest true "Skill package request"
// @Success 200 {object} SwaggerTenantSkillPackageResult
// @Failure 403 {object} SwaggerError
// @Router /tenant/skill-packages/import [post]
func swaggerTenantSkillPackageImport() {}

// swaggerTenantSkillPackageRender godoc
// @Summary Render a tenant skill package without publishing it
// @Description Accepts a local source_path on the server or a base64-encoded package.skill.zip and returns deterministic runtime.md plus manifest metadata.
// @Tags Tenant Skills
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "Admin user key"
// @Param request body SwaggerTenantSkillPackageRequest true "Skill package request"
// @Success 200 {object} SwaggerTenantSkillPackageResult
// @Failure 403 {object} SwaggerError
// @Router /tenant/skill-packages/render [post]
func swaggerTenantSkillPackageRender() {}

// swaggerTenantSkillPackagePublish godoc
// @Summary Publish a tenant skill package as a tenant skill version
// @Description Stores package.skill.zip, manifest.json, and runtime.md in the local artifact store, then writes compiled runtime markdown and package metadata into tenant_skills.
// @Tags Tenant Skills
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "Admin user key"
// @Param request body SwaggerTenantSkillPackageRequest true "Skill package request"
// @Success 200 {object} SwaggerTenantSkillPackageResult
// @Failure 403 {object} SwaggerError
// @Router /tenant/skill-packages/publish [post]
func swaggerTenantSkillPackagePublish() {}

// swaggerTenantSkillPackageVerifyRuntime godoc
// @Summary Verify tenant skill package runtime metadata
// @Description Runs a generic structured runtime smoke for a selected tenant skill and verifies safe tenant_runtime metadata such as loaded key, version, package hash, and loaded bytes. This endpoint does not validate upper-layer business state.
// @Tags Tenant Skills
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "Admin user key"
// @Param request body SwaggerSkillPackageVerifyRequest true "Skill package runtime verify request"
// @Success 200 {object} SwaggerSkillPackageVerifyResponse
// @Failure 403 {object} SwaggerError
// @Router /tenant/skill-packages/verify-runtime [post]
func swaggerTenantSkillPackageVerifyRuntime() {}

// swaggerTenantSkillOverridesList godoc
// @Summary List tenant skill overrides
// @Tags Tenant Skills
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param skill_key query string false "Skill key. When present, returns one override instead of list."
// @Param version query int false "Skill version for detail lookup"
// @Param limit query int false "Limit"
// @Success 200 {object} SwaggerListSkillOverridesResponse
// @Router /tenant/skill-overrides [get]
func swaggerTenantSkillOverridesList() {}

// swaggerTenantSkillOverridesSave godoc
// @Summary Upsert tenant skill override
// @Tags Tenant Skills
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param request body SwaggerTenantSkillOverrideRequest true "Skill override request"
// @Success 200 {object} SwaggerIDResponse
// @Router /tenant/skill-overrides [post]
func swaggerTenantSkillOverridesSave() {}

// swaggerTenantEffectiveSkillsList godoc
// @Summary List effective tenant skills
// @Tags Tenant Skills
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param skill_key query string false "Skill key. When present, returns one effective skill instead of list."
// @Param version query int false "Skill version for detail lookup"
// @Param enabled query bool false "Only enabled skills"
// @Param limit query int false "Limit"
// @Success 200 {object} SwaggerListEffectiveSkillsResponse
// @Router /tenant/effective-skills [get]
func swaggerTenantEffectiveSkillsList() {}

// swaggerTenantDocumentsList godoc
// @Summary List tenant documents
// @Tags Tenant Documents
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param type query string false "Document type. When set and history=false, returns the active document."
// @Param history query bool false "Return document history"
// @Param limit query int false "Limit"
// @Success 200 {object} SwaggerListDocumentsResponse
// @Router /tenant/documents [get]
func swaggerTenantDocumentsList() {}

// swaggerTenantDocumentsSave godoc
// @Summary Save tenant document
// @Tags Tenant Documents
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param request body SwaggerTenantDocumentRequest true "Document request"
// @Success 200 {object} SwaggerIDResponse
// @Router /tenant/documents [post]
func swaggerTenantDocumentsSave() {}

// swaggerTenantKnowledgeDocumentsList godoc
// @Summary List tenant knowledge documents
// @Tags Tenant Knowledge
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param limit query int false "Limit"
// @Success 200 {object} SwaggerListKnowledgeDocumentsResponse
// @Router /tenant/knowledge/documents [get]
func swaggerTenantKnowledgeDocumentsList() {}

// swaggerTenantKnowledgeDocumentsSave godoc
// @Summary Save tenant knowledge document
// @Tags Tenant Knowledge
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param request body SwaggerTenantKnowledgeDocumentRequest true "Knowledge document request"
// @Success 200 {object} SwaggerIDResponse
// @Failure 400 {object} SwaggerError
// @Router /tenant/knowledge/documents [post]
func swaggerTenantKnowledgeDocumentsSave() {}

// swaggerTenantKnowledgeSearch godoc
// @Summary Search tenant knowledge chunks
// @Tags Tenant Knowledge
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param request body SwaggerTenantKnowledgeSearchRequest true "Knowledge search request"
// @Success 200 {object} SwaggerKnowledgeSearchResponse
// @Failure 400 {object} SwaggerError
// @Router /tenant/knowledge/search [post]
func swaggerTenantKnowledgeSearch() {}

// swaggerTenantProfileGet godoc
// @Summary Get tenant profile
// @Tags Tenant Profile
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param version query int false "Profile version"
// @Success 200 {object} SwaggerTenantProfile
// @Router /tenant/profile [get]
func swaggerTenantProfileGet() {}

// swaggerTenantProfileSave godoc
// @Summary Save tenant profile
// @Tags Tenant Profile
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param request body SwaggerTenantProfileRequest true "Profile request"
// @Success 200 {object} SwaggerIDResponse
// @Router /tenant/profile [post]
func swaggerTenantProfileSave() {}

// swaggerAgentProfilesList godoc
// @Summary List agent profiles
// @Tags Agent Profiles
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param status query string false "Profile status"
// @Param limit query int false "Limit"
// @Success 200 {object} map[string]interface{}
// @Router /tenant/agent-profiles [get]
func swaggerAgentProfilesList() {}

// swaggerAgentProfileGet godoc
// @Summary Get agent profile
// @Tags Agent Profiles
// @Security ApiKeyAuth
// @Produce json
// @Param key path string true "Profile key"
// @Param version query int false "Profile version"
// @Success 200 {object} SwaggerAgentProfile
// @Router /tenant/agent-profiles/{key} [get]
func swaggerAgentProfileGet() {}

// swaggerAgentProfileSave godoc
// @Summary Save agent profile draft
// @Tags Agent Profiles
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param key path string false "Profile key"
// @Param request body map[string]interface{} true "Profile draft"
// @Success 200 {object} SwaggerAgentProfile
// @Router /tenant/agent-profiles [post]
// @Router /tenant/agent-profiles/{key} [patch]
func swaggerAgentProfileSave() {}

// swaggerAgentProfileValidate godoc
// @Summary Validate agent profile
// @Tags Agent Profiles
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param key path string true "Profile key"
// @Param request body map[string]interface{} true "Profile draft"
// @Success 200 {object} map[string]interface{}
// @Router /tenant/agent-profiles/{key}/validate [post]
func swaggerAgentProfileValidate() {}

// swaggerAgentProfileEffective godoc
// @Summary Preview effective agent profile
// @Tags Agent Profiles
// @Security ApiKeyAuth
// @Produce json
// @Param key path string true "Profile key"
// @Param version query int false "Profile version"
// @Param surface query string false "Runtime surface"
// @Param language query string false "Requested language override"
// @Param output_style query string false "Requested output style override"
// @Param effort query string false "Requested effort override"
// @Param max_turns query int false "Requested max turns"
// @Param max_tokens query int false "Requested max tokens"
// @Success 200 {object} SwaggerEffectiveAgentProfile
// @Router /tenant/agent-profiles/{key}/effective [get]
func swaggerAgentProfileEffective() {}

// swaggerAgentProfilePublish godoc
// @Summary Publish agent profile
// @Tags Agent Profiles
// @Security ApiKeyAuth
// @Produce json
// @Param key path string true "Profile key"
// @Param version query int true "Profile version"
// @Success 200 {object} map[string]interface{}
// @Router /tenant/agent-profiles/{key}/publish [post]
func swaggerAgentProfilePublish() {}

// swaggerAgentProfileArchive godoc
// @Summary Archive agent profile
// @Tags Agent Profiles
// @Security ApiKeyAuth
// @Produce json
// @Param key path string true "Profile key"
// @Param version query int true "Profile version"
// @Success 200 {object} map[string]interface{}
// @Router /tenant/agent-profiles/{key}/archive [post]
func swaggerAgentProfileArchive() {}

// swaggerAgentProfileRollback godoc
// @Summary Roll back agent profile
// @Tags Agent Profiles
// @Security ApiKeyAuth
// @Produce json
// @Param key path string true "Profile key"
// @Param version query int true "Profile version"
// @Success 200 {object} SwaggerAgentProfile
// @Router /tenant/agent-profiles/{key}/rollback [post]
func swaggerAgentProfileRollback() {}

// swaggerAgentProfileBinding godoc
// @Summary Manage agent profile bot binding
// @Tags Agent Profiles
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param key path string true "Profile key"
// @Param version query int true "Profile version"
// @Param request body map[string]interface{} false "Bot binding"
// @Success 200 {object} SwaggerAgentProfileChannelBinding
// @Router /tenant/agent-profiles/{key}/bot-binding [get]
// @Router /tenant/agent-profiles/{key}/bot-binding [put]
// @Router /tenant/agent-profiles/{key}/bot-binding [delete]
func swaggerAgentProfileBinding() {}

// swaggerAgentProfileAssignment godoc
// @Summary Get or assign profile surface
// @Tags Agent Profiles
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param surface query string true "Runtime surface"
// @Param request body map[string]interface{} false "Assignment"
// @Success 200 {object} SwaggerAgentProfileAssignment
// @Router /tenant/agent-profile-assignment [get]
// @Router /tenant/agent-profile-assignment [put]
func swaggerAgentProfileAssignment() {}

// swaggerAgentTeamsList godoc
// @Summary List agent teams
// @Tags Agent Teams
// @Security ApiKeyAuth
// @Produce json
// @Param status query string false "Team status"
// @Param limit query int false "Limit"
// @Success 200 {object} map[string]interface{}
// @Router /tenant/agent-teams [get]
func swaggerAgentTeamsList() {}

// swaggerAgentTeamGet godoc
// @Summary Get agent team
// @Tags Agent Teams
// @Security ApiKeyAuth
// @Produce json
// @Param key path string true "Team key"
// @Param version query int false "Team version"
// @Success 200 {object} SwaggerAgentTeam
// @Router /tenant/agent-teams/{key} [get]
func swaggerAgentTeamGet() {}

// swaggerAgentTeamSave godoc
// @Summary Save agent team draft
// @Tags Agent Teams
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param key path string false "Team key"
// @Param request body map[string]interface{} true "Team draft"
// @Success 200 {object} SwaggerAgentTeam
// @Router /tenant/agent-teams [post]
// @Router /tenant/agent-teams/{key} [patch]
func swaggerAgentTeamSave() {}

// swaggerAgentTeamValidate godoc
// @Summary Validate agent team
// @Tags Agent Teams
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param key path string true "Team key"
// @Param version query int true "Team version"
// @Param request body map[string]interface{} true "Team graph"
// @Success 200 {object} map[string]interface{}
// @Router /tenant/agent-teams/{key}/validate [post]
func swaggerAgentTeamValidate() {}

// swaggerAgentTeamLifecycle godoc
// @Summary Publish or archive agent team
// @Tags Agent Teams
// @Security ApiKeyAuth
// @Produce json
// @Param key path string true "Team key"
// @Param version query int true "Team version"
// @Success 200 {object} map[string]interface{}
// @Router /tenant/agent-teams/{key}/publish [post]
// @Router /tenant/agent-teams/{key}/archive [post]
func swaggerAgentTeamLifecycle() {}

// swaggerAgentTeamRollback godoc
// @Summary Roll back agent team
// @Tags Agent Teams
// @Security ApiKeyAuth
// @Produce json
// @Param key path string true "Team key"
// @Param version query int true "Team version"
// @Success 200 {object} SwaggerAgentTeam
// @Router /tenant/agent-teams/{key}/rollback [post]
func swaggerAgentTeamRollback() {}

// swaggerAgentTeamMembers godoc
// @Summary Manage agent team members
// @Tags Agent Teams
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param key path string true "Team key"
// @Param version query int true "Team version"
// @Param request body []SwaggerAgentTeamMember false "Team members"
// @Success 200 {object} map[string]interface{}
// @Router /tenant/agent-teams/{key}/members [get]
// @Router /tenant/agent-teams/{key}/members [put]
func swaggerAgentTeamMembers() {}

// swaggerAgentTeamBindings godoc
// @Summary Manage agent team channel bindings
// @Tags Agent Teams
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param key path string true "Team key"
// @Param version query int true "Team version"
// @Param request body []SwaggerAgentTeamBinding false "Team bindings"
// @Success 200 {object} map[string]interface{}
// @Router /tenant/agent-teams/{key}/bindings [get]
// @Router /tenant/agent-teams/{key}/bindings [put]
func swaggerAgentTeamBindings() {}

// swaggerAgentTeamRuns godoc
// @Summary List agent team runs
// @Tags Agent Teams
// @Security ApiKeyAuth
// @Produce json
// @Param key path string true "Team key"
// @Param version query int true "Team version"
// @Param limit query int false "Limit"
// @Success 200 {object} map[string]interface{}
// @Router /tenant/agent-teams/{key}/runs [get]
func swaggerAgentTeamRuns() {}

// swaggerAgentTeamRun godoc
// @Summary Get or cancel agent team run
// @Tags Agent Teams
// @Security ApiKeyAuth
// @Produce json
// @Param key path string true "Team key"
// @Param run_id path string true "Team run id"
// @Param version query int true "Pinned team version"
// @Success 200 {object} SwaggerAgentTeamRun
// @Router /tenant/agent-teams/{key}/runs/{run_id} [get]
func swaggerAgentTeamRun() {}

// swaggerAgentTeamRunEvents godoc
// @Summary Get member events and mailbox timeline for an agent team run
// @Tags Agent Teams
// @Security ApiKeyAuth
// @Produce json
// @Param key path string true "Team key"
// @Param run_id path string true "Team run id"
// @Param version query int true "Pinned team version"
// @Param limit query int false "Limit"
// @Success 200 {object} map[string]interface{}
// @Router /tenant/agent-teams/{key}/runs/{run_id}/events [get]
func swaggerAgentTeamRunEvents() {}

// swaggerAgentTeamRunCancel godoc
// @Summary Cancel agent team run
// @Tags Agent Teams
// @Security ApiKeyAuth
// @Produce json
// @Param key path string true "Team key"
// @Param run_id path string true "Team run id"
// @Param version query int true "Pinned team version"
// @Success 200 {object} map[string]interface{}
// @Router /tenant/agent-teams/{key}/runs/{run_id}/cancel [post]
func swaggerAgentTeamRunCancel() {}

// swaggerChannelAccounts godoc
// @Summary List safe channel account metadata
// @Tags Agent Teams
// @Security ApiKeyAuth
// @Produce json
// @Param limit query int false "Limit"
// @Success 200 {object} map[string]interface{}
// @Router /tenant/channel-accounts [get]
func swaggerChannelAccounts() {}

// swaggerTenantSessionsList godoc
// @Summary List tenant sessions
// @Tags Tenant Sessions
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param limit query int false "Limit"
// @Success 200 {object} SwaggerListSessionsResponse
// @Router /tenant/sessions [get]
func swaggerTenantSessionsList() {}

// swaggerTenantSessionsSave godoc
// @Summary Upsert tenant session
// @Tags Tenant Sessions
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param request body SwaggerTenantSessionRequest true "Session request"
// @Success 200 {object} SwaggerIDResponse
// @Router /tenant/sessions [post]
func swaggerTenantSessionsSave() {}

// swaggerTenantSessionGet godoc
// @Summary Get tenant session
// @Tags Tenant Sessions
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param id path int true "Session ID"
// @Success 200 {object} SwaggerTenantSession
// @Router /tenant/sessions/{id} [get]
func swaggerTenantSessionGet() {}

// swaggerTenantSessionUpdate godoc
// @Summary Update tenant session
// @Tags Tenant Sessions
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param id path int true "Session ID"
// @Param request body SwaggerTenantSessionRequest true "Session request"
// @Success 200 {object} SwaggerIDResponse
// @Router /tenant/sessions/{id} [patch]
func swaggerTenantSessionUpdate() {}

// swaggerTenantSessionPut godoc
// @Summary Replace tenant session fields
// @Tags Tenant Sessions
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param id path int true "Session ID"
// @Param request body SwaggerTenantSessionRequest true "Session request"
// @Success 200 {object} SwaggerIDResponse
// @Router /tenant/sessions/{id} [put]
func swaggerTenantSessionPut() {}

// swaggerTenantSessionArchive godoc
// @Summary Archive tenant session
// @Tags Tenant Sessions
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param id path int true "Session ID"
// @Success 200 {object} SwaggerArchiveResponse
// @Router /tenant/sessions/{id} [delete]
func swaggerTenantSessionArchive() {}

// swaggerTenantSessionTimeline godoc
// @Summary Get tenant session full-chain timeline
// @Description Owner/admin endpoint that aggregates a session, messages, trace-linked audit logs, telemetry events, sub-agent tasks, and task events into one time-ordered timeline.
// @Tags Tenant Sessions
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "Admin user key"
// @Param id path int true "Session ID"
// @Param limit query int false "Message/task event limit"
// @Param trace_limit query int false "Audit/telemetry per-trace limit"
// @Param task_limit query int false "Sub-agent task scan limit"
// @Success 200 {object} SwaggerTenantSessionTimelineResponse
// @Router /tenant/sessions/{id}/timeline [get]
func swaggerTenantSessionTimeline() {}

// swaggerTraceUI godoc
// @Summary Open the built-in trace viewer WebUI
// @Description Serves a local WebUI that visualizes local TUI/CLI transcripts and tenant API Server timelines.
// @Tags Trace Viewer
// @Security ApiKeyAuth
// @Produce html
// @Success 200 {string} string "HTML trace viewer"
// @Router /trace [get]
func swaggerTraceUI() {}

// swaggerTraceSessionsList godoc
// @Summary List trace viewer sessions
// @Description Lists local transcript sessions by default, or tenant sessions when source=tenant and tenant headers are present.
// @Tags Trace Viewer
// @Security ApiKeyAuth
// @Produce json
// @Param source query string false "Session source: local or tenant"
// @Param limit query int false "Limit"
// @Param from query string false "Filter sessions updated at or after this RFC3339 timestamp"
// @Param to query string false "Filter sessions updated at or before this RFC3339 timestamp"
// @Param X-Tenant-Key header string false "Tenant key, required for source=tenant"
// @Param X-User-Id header string false "User key, required for source=tenant"
// @Success 200 {object} SwaggerTraceSessionsResponse
// @Router /trace/api/sessions [get]
func swaggerTraceSessionsList() {}

// swaggerTraceSessionDetail godoc
// @Summary Get a trace viewer session detail
// @Description Returns a normalized summary, spans, and events for a local transcript session or tenant session timeline.
// @Tags Trace Viewer
// @Security ApiKeyAuth
// @Produce json
// @Param id path string true "Local session UUID or tenant numeric session ID"
// @Param source query string false "Session source: local or tenant"
// @Param limit query int false "Message/task event limit"
// @Param trace_limit query int false "Audit/telemetry per-trace limit for tenant source"
// @Param task_limit query int false "Sub-agent task scan limit for tenant source"
// @Param X-Tenant-Key header string false "Tenant key, required for source=tenant"
// @Param X-User-Id header string false "User key, required for source=tenant"
// @Success 200 {object} SwaggerTraceDetailResponse
// @Router /trace/api/sessions/{id} [get]
func swaggerTraceSessionDetail() {}

// swaggerTraceSessionExport godoc
// @Summary Export a runtime trace artifact
// @Description Exports a metadata-only runtime-trace-v1 artifact for APG and offline baseline analysis.
// @Tags Trace Viewer
// @Security ApiKeyAuth
// @Produce json
// @Param id path string true "Local session UUID or tenant numeric session ID"
// @Param source query string false "Session source: local or tenant"
// @Param schema query string false "Artifact schema (runtime-trace-v1)"
// @Param X-Tenant-Key header string false "Tenant key, required for source=tenant"
// @Param X-User-Id header string false "User key, required for source=tenant"
// @Success 200 {object} RuntimeTraceArtifact
// @Failure 400 {object} SwaggerError
// @Failure 401 {object} SwaggerError
// @Failure 403 {object} SwaggerError
// @Failure 404 {object} SwaggerError
// @Failure 503 {object} SwaggerError
// @Router /trace/api/sessions/{id}/export [get]
func swaggerTraceSessionExport() {}

// swaggerPromptDumpUI godoc
// @Summary Open the built-in prompt dump viewer WebUI
// @Description Serves a local WebUI for inspecting prompt dump JSONL records, filtering by session_id, and opening full raw request details when the dump was captured with NIUMA_DUMP_PROMPT_FULL=true.
// @Tags Prompt Dump Viewer
// @Security ApiKeyAuth
// @Produce html
// @Success 200 {string} string "HTML prompt dump viewer"
// @Router /prompt-dump [get]
func swaggerPromptDumpUI() {}

// swaggerWebUIRedirect godoc
// @Summary Open the WebUI
// @Description Redirects to /webui/. Registered unconditionally so an unbuilt frontend answers with build instructions instead of a bare 404.
// @Tags WebUI
// @Security ApiKeyAuth
// @Produce html
// @Param token query string false "Auth token; accepted once and stored in a /webui/-scoped cookie"
// @Success 302 {string} string "Redirect to /webui/"
// @Failure 401 {string} string "Unauthorized"
// @Router /webui [get]
func swaggerWebUIRedirect() {}

// swaggerWebUIStatic godoc
// @Summary Serve the WebUI single-page app
// @Description Serves the Vite build from NIUMA_WEBUI_DIR, or an auto-discovered web/dist next to the working directory or executable. Unknown paths fall back to index.html for client-side routing. When no build is present, responds 503 with a page explaining how to build the frontend.
// @Tags WebUI
// @Security ApiKeyAuth
// @Produce html
// @Param path path string true "Asset path within the build, empty for index.html"
// @Param token query string false "Auth token; accepted once and stored in a /webui/-scoped cookie"
// @Success 200 {string} string "WebUI asset"
// @Failure 401 {string} string "Unauthorized"
// @Failure 405 {string} string "Method not allowed"
// @Failure 503 {string} string "Frontend not built; response body explains how to build it"
// @Router /webui/{path} [get]
func swaggerWebUIStatic() {}

// swaggerPromptDumpRecords godoc
// @Summary List prompt dump records
// @Description Reads the local prompt dump JSONL file from NIUMA_DUMP_PROMPT_JSON or /tmp/niuma-tui-prompt.jsonl. include_request=true returns full raw model requests and may expose sensitive prompt context.
// @Tags Prompt Dump Viewer
// @Security ApiKeyAuth
// @Produce json
// @Param session_id query string false "Filter by TUI session ID"
// @Param limit query int false "Maximum records to return, capped at 1000"
// @Param include_request query bool false "Include full raw request payload"
// @Success 200 {object} SwaggerPromptDumpRecordsResponse
// @Router /prompt-dump/api/records [get]
func swaggerPromptDumpRecords() {}

// swaggerTenantMessagesList godoc
// @Summary List tenant session messages
// @Tags Tenant Messages
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param session_id query int true "Session ID"
// @Param limit query int false "Limit"
// @Success 200 {object} SwaggerListMessagesResponse
// @Router /tenant/messages [get]
func swaggerTenantMessagesList() {}

// swaggerTenantMessagesSave godoc
// @Summary Upsert tenant session message
// @Tags Tenant Messages
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param request body SwaggerTenantMessageRequest true "Message request"
// @Success 200 {object} SwaggerIDResponse
// @Router /tenant/messages [post]
func swaggerTenantMessagesSave() {}

// swaggerAgentWorkspaceValidate godoc
// @Summary Validate a local Web Agent workspace path
// @Tags Agent Workspaces
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param request body SwaggerAgentWorkspaceValidateRequest true "Workspace cwd"
// @Success 200 {object} SwaggerAgentWorkspaceValidateResponse
// @Failure 400 {object} SwaggerError
// @Failure 401 {object} SwaggerError
// @Router /agent/workspaces/validate [post]
func swaggerAgentWorkspaceValidate() {}

// swaggerAgentSlashCommandsList godoc
// @Summary List Web Agent slash command suggestions
// @Tags Agent Workspaces
// @Security ApiKeyAuth
// @Produce json
// @Param cwd query string false "Workspace cwd"
// @Param prefix query string false "Slash command prefix"
// @Param limit query int false "Limit"
// @Success 200 {object} SwaggerListAgentSlashCommandsResponse
// @Failure 400 {object} SwaggerError
// @Failure 401 {object} SwaggerError
// @Router /agent/slash-commands [get]
func swaggerAgentSlashCommandsList() {}

// swaggerTenantWebAgentConversationsList godoc
// @Summary List Web Agent conversations grouped by tenant session
// @Tags Tenant Agent Tasks
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param limit query int false "Limit"
// @Success 200 {object} SwaggerListWebAgentConversationsResponse
// @Router /tenant/web-agent/conversations [get]
func swaggerTenantWebAgentConversationsList() {}

// swaggerTenantWebAgentConversationDetail godoc
// @Summary Get Web Agent conversation detail
// @Tags Tenant Agent Tasks
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param id path string true "Conversation ID, for example session:23 or legacy:13"
// @Param limit query int false "Task limit within session; legacy conversation scan limit"
// @Param event_limit query int false "Per-run event limit"
// @Success 200 {object} SwaggerWebAgentConversationDetail
// @Failure 404 {object} SwaggerError
// @Router /tenant/web-agent/conversations/{id} [get]
func swaggerTenantWebAgentConversationDetail() {}

// swaggerTenantAgentTasksList godoc
// @Summary List tenant sub-agent tasks
// @Tags Tenant Agent Tasks
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param limit query int false "Limit"
// @Success 200 {object} SwaggerListAgentTasksResponse
// @Router /tenant/agent-tasks [get]
func swaggerTenantAgentTasksList() {}

// swaggerTenantAgentTaskCreate godoc
// @Summary Create tenant sub-agent task metadata
// @Tags Tenant Agent Tasks
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param request body SwaggerCreateAgentTaskRequest true "Agent task metadata"
// @Success 200 {object} SwaggerCreateAgentTaskResponse
// @Failure 400 {object} SwaggerError
// @Router /tenant/agent-tasks [post]
func swaggerTenantAgentTaskCreate() {}

// swaggerTenantAgentTaskGet godoc
// @Summary Get tenant sub-agent task
// @Tags Tenant Agent Tasks
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param id path int true "Agent task ID"
// @Success 200 {object} SwaggerAgentTaskResponse
// @Failure 404 {object} SwaggerError
// @Router /tenant/agent-tasks/{id} [get]
func swaggerTenantAgentTaskGet() {}

// swaggerTenantAgentTaskUpdate godoc
// @Summary Update tenant sub-agent task status or metadata
// @Tags Tenant Agent Tasks
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param id path int true "Agent task ID"
// @Param request body SwaggerUpdateAgentTaskRequest true "Agent task update"
// @Success 200 {object} SwaggerUpdateAgentTaskResponse
// @Failure 400 {object} SwaggerError
// @Failure 404 {object} SwaggerError
// @Router /tenant/agent-tasks/{id} [patch]
func swaggerTenantAgentTaskUpdate() {}

// swaggerTenantAgentTaskEventsList godoc
// @Summary List tenant sub-agent task events
// @Tags Tenant Agent Tasks
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param id path int true "Agent task ID"
// @Param limit query int false "Limit"
// @Param after_id query int false "Return events with id greater than this cursor"
// @Success 200 {object} SwaggerListAgentTaskEventsResponse
// @Router /tenant/agent-tasks/{id}/events [get]
func swaggerTenantAgentTaskEventsList() {}

// swaggerTenantAgentTaskEventsStream godoc
// @Summary Stream tenant sub-agent task events over SSE
// @Description Server-Sent Events stream. Emits a `connected` event, then one event per task event as it lands (polled every 250ms), and a terminal event when the task finishes. Use `after_id` to resume without replaying.
// @Tags Tenant Agent Tasks
// @Security ApiKeyAuth
// @Produce text/event-stream
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param id path int true "Agent task ID"
// @Param limit query int false "Maximum events per poll (default 200)"
// @Param after_id query int false "Resume after this event id"
// @Success 200 {string} string "SSE stream of agent task events"
// @Failure 400 {object} SwaggerError
// @Failure 401 {object} SwaggerError
// @Failure 500 {object} SwaggerError
// @Router /tenant/agent-tasks/{id}/events/stream [get]
func swaggerTenantAgentTaskEventsStream() {}

// swaggerSessionControlSessionsList godoc
// @Summary List Session Control sessions
// @Description 返回当前认证 tenant/user 的有界会话状态。source=local 仅支持只读；响应不包含 transcript 或消息正文。
// @Tags Session Control
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param source query string true "Session namespace" Enums(tenant,local)
// @Param limit query int false "Maximum sessions"
// @Success 200 {object} SwaggerSessionControlSessionListResponse
// @Failure 400 {object} SwaggerSessionControlError
// @Failure 401 {object} SwaggerSessionControlError
// @Failure 403 {object} SwaggerSessionControlError
// @Failure 503 {object} SwaggerSessionControlError
// @Router /tenant/session-control/sessions [get]
func swaggerSessionControlSessionsList() {}

// swaggerSessionControlSessionsCreate godoc
// @Summary Create a managed Session Control session
// @Description 创建 tenant session。身份只来自认证上下文；body 不能设置 tenant、user、actor 或 trace。相同 Idempotency-Key 与相同语义返回相同 readback；不同语义返回 idempotency_conflict。
// @Tags Session Control
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param Idempotency-Key header string true "Mutation idempotency key, at most 128 bytes"
// @Param request body SwaggerSessionControlCreateRequest true "Managed session request"
// @Success 200 {object} SwaggerSessionControlOperationResponse
// @Failure 400 {object} SwaggerSessionControlError
// @Failure 401 {object} SwaggerSessionControlError
// @Failure 403 {object} SwaggerSessionControlError
// @Failure 409 {object} SwaggerSessionControlError
// @Failure 503 {object} SwaggerSessionControlError
// @Router /tenant/session-control/sessions [post]
func swaggerSessionControlSessionsCreate() {}

// swaggerSessionControlSessionGet godoc
// @Summary Read a Session Control snapshot
// @Description 返回 namespaced ref 对应的有界状态快照。Local session 是只读的；响应不包含 transcript、消息正文或私有 locator。
// @Tags Session Control
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param source path string true "Session namespace" Enums(tenant,local)
// @Param id path string true "Namespace-local session key"
// @Param include_links query bool false "Include safe relation metadata"
// @Success 200 {object} SwaggerSessionControlSessionResponse
// @Failure 400 {object} SwaggerSessionControlError
// @Failure 401 {object} SwaggerSessionControlError
// @Failure 403 {object} SwaggerSessionControlError
// @Failure 404 {object} SwaggerSessionControlError
// @Failure 503 {object} SwaggerSessionControlError
// @Router /tenant/session-control/sessions/{source}/{id} [get]
func swaggerSessionControlSessionGet() {}

// swaggerSessionConversation godoc
// @Summary Read managed conversation events
// @Description Reads ordered main-run events by session key with an exclusive cursor. Ownership is checked before returning content.
// @Tags Session Control
// @Produce json
// @Security BearerAuth
// @Param source path string true "Session source" Enums(tenant)
// @Param id path string true "Session key (not numeric database ID)"
// @Param cursor query string false "Exclusive event cursor"
// @Success 200 {object} SwaggerSessionConversationResponse
// @Failure 403 {object} SwaggerSessionControlError
// @Router /tenant/session-control/sessions/{source}/{id}/conversation [get]
func swaggerSessionConversation() {}

// swaggerSessionConversationsStream godoc
// @Summary Subscribe to managed conversations
// @Description One SSE connection carries conversation pages for up to 32 authorized session refs. Each has an independent cursor; disconnect never cancels runs. Reconnect with per-session cursors.
// @Tags Session Control
// @Accept json
// @Produce text/event-stream
// @Security BearerAuth
// @Param request body SwaggerSessionConversationSubscribe true "Session refs and cursors"
// @Success 200 {object} sessionConversationPage
// @Failure 403 {object} SwaggerSessionControlError
// @Router /tenant/session-control/conversations/stream [post]
func swaggerSessionConversationsStream() {}

// swaggerSessionControlMessage godoc
// @Summary Send a message to a managed session
// @Description 向 tenant session 发送文字和图片，支持 inline_data base64 归档为会话隔离的持久附件。Local session 写入始终返回 forbidden；会话来源关系通过 source_refs 或 attachments/Attach 接口建立。
// @Tags Session Control
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param Idempotency-Key header string true "Mutation idempotency key, at most 128 bytes"
// @Param source path string true "Must be tenant" Enums(tenant)
// @Param id path string true "Managed session key"
// @Param request body SwaggerSessionControlSendRequest true "Message request"
// @Success 200 {object} SwaggerSessionControlOperationResponse
// @Failure 400 {object} SwaggerSessionControlError
// @Failure 401 {object} SwaggerSessionControlError
// @Failure 403 {object} SwaggerSessionControlError
// @Failure 404 {object} SwaggerSessionControlError
// @Failure 409 {object} SwaggerSessionControlError
// @Failure 413 {object} SwaggerSessionControlError
// @Failure 503 {object} SwaggerSessionControlError
// @Router /tenant/session-control/sessions/{source}/{id}/messages [post]
func swaggerSessionControlMessage() {}

// swaggerSessionControlCompact godoc
// @Summary Compact a managed session transcript
// @Description 将当前 managed session 的可读对话压缩为一个持久化 compact_summary 事件。Local session 写入始终返回 forbidden；事件写入遵循当前会话事件后端，不执行双写。
// @Tags Session Control
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param Idempotency-Key header string true "Mutation idempotency key, at most 128 bytes"
// @Param source path string true "Must be tenant" Enums(tenant)
// @Param id path string true "Managed session key"
// @Param request body SwaggerSessionControlStopRequest false "Optional empty JSON object"
// @Success 200 {object} SwaggerSessionControlOperationResponse
// @Failure 400 {object} SwaggerSessionControlError
// @Failure 401 {object} SwaggerSessionControlError
// @Failure 403 {object} SwaggerSessionControlError
// @Failure 404 {object} SwaggerSessionControlError
// @Failure 409 {object} SwaggerSessionControlError
// @Failure 503 {object} SwaggerSessionControlError
// @Router /tenant/session-control/sessions/{source}/{id}/compact [post]
func swaggerSessionControlCompact() {}

// swaggerSessionControlStop godoc
// @Summary Stop a managed session run
// @Description 停止 tenant session 的活动 Run。断开 SSE 不会调用此操作；Local session 写入始终返回 forbidden。
// @Tags Session Control
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param Idempotency-Key header string true "Mutation idempotency key, at most 128 bytes"
// @Param source path string true "Must be tenant" Enums(tenant)
// @Param id path string true "Managed session key"
// @Param request body SwaggerSessionControlStopRequest false "Optional empty JSON object"
// @Success 200 {object} SwaggerSessionControlOperationResponse
// @Failure 400 {object} SwaggerSessionControlError
// @Failure 401 {object} SwaggerSessionControlError
// @Failure 403 {object} SwaggerSessionControlError
// @Failure 404 {object} SwaggerSessionControlError
// @Failure 409 {object} SwaggerSessionControlError
// @Failure 503 {object} SwaggerSessionControlError
// @Router /tenant/session-control/sessions/{source}/{id}/stop [post]
func swaggerSessionControlStop() {}

// swaggerSessionControlAttach godoc
// @Summary Attach bounded session handoff sources
// @Description 通过 Session Control Attach 将 namespaced source 的有界 handoff 包绑定到显式 target task。不会复制完整 transcript。Local target 写入始终返回 forbidden。
// @Tags Session Control
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param Idempotency-Key header string true "Mutation idempotency key, at most 128 bytes"
// @Param source path string true "Must be tenant" Enums(tenant)
// @Param id path string true "Managed target session key"
// @Param request body SwaggerSessionControlAttachRequest true "Handoff attachment request"
// @Success 200 {object} SwaggerSessionControlOperationResponse
// @Failure 400 {object} SwaggerSessionControlError
// @Failure 401 {object} SwaggerSessionControlError
// @Failure 403 {object} SwaggerSessionControlError
// @Failure 404 {object} SwaggerSessionControlError
// @Failure 409 {object} SwaggerSessionControlError
// @Failure 422 {object} SwaggerSessionControlError
// @Failure 503 {object} SwaggerSessionControlError
// @Router /tenant/session-control/sessions/{source}/{id}/attachments [post]
func swaggerSessionControlAttach() {}

// swaggerSessionControlMonitor godoc
// @Summary Schedule a managed session monitor
// @Description 为 tenant target 和 namespaced sources 建立监控。Local target 写入始终返回 forbidden；scheduler_unavailable 返回 503。
// @Tags Session Control
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param Idempotency-Key header string true "Mutation idempotency key, at most 128 bytes"
// @Param source path string true "Must be tenant" Enums(tenant)
// @Param id path string true "Managed target session key"
// @Param request body SwaggerSessionControlMonitorRequest true "Monitor request"
// @Success 200 {object} SwaggerSessionControlOperationResponse
// @Failure 400 {object} SwaggerSessionControlError
// @Failure 401 {object} SwaggerSessionControlError
// @Failure 403 {object} SwaggerSessionControlError
// @Failure 404 {object} SwaggerSessionControlError
// @Failure 409 {object} SwaggerSessionControlError
// @Failure 503 {object} SwaggerSessionControlError
// @Router /tenant/session-control/sessions/{source}/{id}/monitors [post]
func swaggerSessionControlMonitor() {}

// swaggerSessionControlEventsStream godoc
// @Summary Stream bounded Session Control state
// @Description SSE event names are operation, run, or session. data is SwaggerSessionControlStateEvent only: no content, prompt, task result, Handoff body, audit metadata, credentials, attachment metadata, source locator, or secret. Auth and target readback complete before headers. Disconnect closes polling but never cancels a Run.
// @Tags Session Control
// @Security ApiKeyAuth
// @Produce text/event-stream
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param Last-Event-ID header string false "Last durable task-event cursor"
// @Param source path string true "Session namespace" Enums(tenant,local)
// @Param id path string true "Namespace-local session key"
// @Param cursor query string false "Resume cursor; overrides Last-Event-ID"
// @Param limit query int false "Maximum task events per poll"
// @Success 200 {object} SwaggerSessionControlStateEvent
// @Failure 400 {object} SwaggerSessionControlError
// @Failure 401 {object} SwaggerSessionControlError
// @Failure 403 {object} SwaggerSessionControlError
// @Failure 404 {object} SwaggerSessionControlError
// @Failure 503 {object} SwaggerSessionControlError
// @Router /tenant/session-control/sessions/{source}/{id}/events/stream [get]
func swaggerSessionControlEventsStream() {}

// swaggerTenantAgentTaskMessage godoc
// @Summary Send a message and start the tenant sub-agent task runner
// @Tags Tenant Agent Tasks
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param id path int true "Agent task ID"
// @Param request body SwaggerAgentTaskMessageRequest true "Agent task message; content may be empty when image attachments are provided"
// @Success 200 {object} SwaggerAgentTaskMessageResponse
// @Failure 400 {object} SwaggerError
// @Failure 404 {object} SwaggerError
// @Failure 409 {object} SwaggerError
// @Failure 503 {object} SwaggerError
// @Router /tenant/agent-tasks/{id}/message [post]
func swaggerTenantAgentTaskMessage() {}

// swaggerTenantAgentTaskPendingInputs godoc
// @Summary Manage pending user inputs for a tenant agent task
// @Description Enter while a task is running queues a confirmed input. Move-up only changes order; it never starts a runner.
// @Tags Tenant Agent Tasks
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param id path int true "Agent task ID"
// @Param input_id path string false "Pending input ID"
// @Param action path string false "Pending input action"
// @Param request body SwaggerPendingInputRequest false "Pending input"
// @Success 200 {object} SwaggerPendingInputResponse
// @Success 202 {object} SwaggerPendingInputResponse
// @Failure 400 {object} SwaggerError
// @Failure 404 {object} SwaggerError
// @Failure 409 {object} SwaggerError
// @Failure 429 {object} SwaggerError
// @Router /tenant/agent-tasks/{id}/pending-inputs [get]
// @Router /tenant/agent-tasks/{id}/pending-inputs [post]
// @Router /tenant/agent-tasks/{id}/pending-inputs/{input_id} [patch]
// @Router /tenant/agent-tasks/{id}/pending-inputs/{input_id} [delete]
// @Router /tenant/agent-tasks/{id}/pending-inputs/{input_id}/{action} [post]
func swaggerTenantAgentTaskPendingInputs() {}

// swaggerTenantAgentTaskPendingInputSettings godoc
// @Summary Get or update pending input queue settings
// @Tags Tenant Agent Tasks
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param id path int true "Agent task ID"
// @Param request body SwaggerPendingInputSettingsRequest false "Queue setting"
// @Success 200 {object} map[string]bool
// @Failure 400 {object} SwaggerError
// @Router /tenant/agent-tasks/{id}/pending-input-settings [get]
// @Router /tenant/agent-tasks/{id}/pending-input-settings [patch]
func swaggerTenantAgentTaskPendingInputSettings() {}

// swaggerTenantAgentTaskPermissionResolve godoc
// @Summary Resolve a pending Web Agent permission request
// @Tags Tenant Agent Tasks
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param id path int true "Agent task ID"
// @Param request_id path string true "Permission request ID"
// @Param request body SwaggerAgentTaskPermissionRequest true "Permission decision"
// @Success 200 {object} SwaggerAgentTaskPermissionResponse
// @Failure 400 {object} SwaggerError
// @Failure 404 {object} SwaggerError
// @Failure 409 {object} SwaggerError
// @Router /tenant/agent-tasks/{id}/permissions/{request_id} [patch]
func swaggerTenantAgentTaskPermissionResolve() {}

// swaggerTenantAgentTaskQuestionAnswer godoc
// @Summary Answer a pending question and continue the original tenant agent task
// @Description Persists the answer before waking the live Run. Identical answer retries are idempotent; changed, cancelled or expired answers return 409.
// @Tags Tenant Agent Tasks
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param id path int true "Agent task ID"
// @Param request_id path string true "Question request ID"
// @Param request body SwaggerAgentTaskQuestionAnswerRequest true "Non-empty answer, at most 16384 bytes"
// @Success 200 {object} SwaggerAgentTaskQuestionAnswerResponse
// @Failure 400 {object} SwaggerError
// @Failure 401 {object} SwaggerError
// @Failure 403 {object} SwaggerError
// @Failure 404 {object} SwaggerError
// @Failure 409 {object} SwaggerError
// @Router /tenant/agent-tasks/{id}/questions/{request_id} [patch]
func swaggerTenantAgentTaskQuestionAnswer() {}

// swaggerTenantAgentTaskCancel godoc
// @Summary Cancel a running tenant sub-agent task
// @Tags Tenant Agent Tasks
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param id path int true "Agent task ID"
// @Success 200 {object} SwaggerCancelAgentTaskResponse
// @Failure 404 {object} SwaggerError
// @Failure 409 {object} SwaggerError
// @Router /tenant/agent-tasks/{id}/cancel [post]
func swaggerTenantAgentTaskCancel() {}

// swaggerTenantGoalsList godoc
// @Summary List tenant goals
// @Tags Tenant Goals
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param active query bool false "Only active goals"
// @Param status query string false "Goal status"
// @Param limit query int false "Limit"
// @Success 200 {object} SwaggerListGoalsResponse
// @Failure 400 {object} SwaggerError
// @Router /tenant/goals [get]
func swaggerTenantGoalsList() {}

// swaggerTenantGoalCreate godoc
// @Summary Create tenant goal
// @Tags Tenant Goals
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param request body SwaggerCreateGoalRequest true "Goal metadata"
// @Success 200 {object} SwaggerGoalResponse
// @Failure 400 {object} SwaggerError
// @Router /tenant/goals [post]
func swaggerTenantGoalCreate() {}

// swaggerTenantGoalGet godoc
// @Summary Get tenant goal
// @Tags Tenant Goals
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param id path string true "Goal ID"
// @Success 200 {object} SwaggerGoalResponse
// @Failure 404 {object} SwaggerError
// @Router /tenant/goals/{id} [get]
func swaggerTenantGoalGet() {}

// swaggerTenantGoalUpdate godoc
// @Summary Update tenant goal
// @Tags Tenant Goals
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param id path string true "Goal ID"
// @Param request body SwaggerUpdateGoalRequest true "Goal update"
// @Success 200 {object} SwaggerGoalResponse
// @Failure 400 {object} SwaggerError
// @Failure 404 {object} SwaggerError
// @Router /tenant/goals/{id} [patch]
func swaggerTenantGoalUpdate() {}

// swaggerTenantGoalEventsList godoc
// @Summary List tenant goal events
// @Tags Tenant Goals
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param id path string true "Goal ID"
// @Param limit query int false "Limit"
// @Success 200 {object} SwaggerListGoalEventsResponse
// @Failure 404 {object} SwaggerError
// @Router /tenant/goals/{id}/events [get]
func swaggerTenantGoalEventsList() {}

// swaggerTenantGoalPlanGet godoc
// @Summary Get tenant goal plan
// @Tags Tenant Goals
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param id path string true "Goal ID"
// @Success 200 {object} SwaggerGoalPlanResponse
// @Failure 404 {object} SwaggerError
// @Router /tenant/goals/{id}/plan [get]
func swaggerTenantGoalPlanGet() {}

// swaggerTenantGoalEvidenceList godoc
// @Summary List tenant goal evidence
// @Tags Tenant Goals
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param id path string true "Goal ID"
// @Param limit query int false "Limit"
// @Success 200 {object} SwaggerListGoalEvidenceResponse
// @Failure 404 {object} SwaggerError
// @Router /tenant/goals/{id}/evidence [get]
func swaggerTenantGoalEvidenceList() {}

// swaggerTenantGoalStop godoc
// @Summary Stop tenant goal
// @Tags Tenant Goals
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param id path string true "Goal ID"
// @Success 200 {object} SwaggerGoalResponse
// @Failure 404 {object} SwaggerError
// @Router /tenant/goals/{id}/stop [post]
func swaggerTenantGoalStop() {}

// swaggerTenantGoalResume godoc
// @Summary Resume tenant goal
// @Tags Tenant Goals
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param id path string true "Goal ID"
// @Param force query bool false "Allow failed goals to resume"
// @Success 200 {object} SwaggerGoalResponse
// @Failure 404 {object} SwaggerError
// @Failure 409 {object} SwaggerError
// @Router /tenant/goals/{id}/resume [post]
func swaggerTenantGoalResume() {}

// swaggerTenantGoalRun godoc
// @Summary Run one tenant goal turn
// @Description Runs one Goal turn through the server query runner by default. continuous=true runs a bounded synchronous loop up to max_turns while reusing Goal locks, budgets, stop control, audit, and events; detached server background execution is intentionally not implied. Defaults to deterministic evaluation; evaluator=model enables model-classified evaluation with deterministic fallback for ambiguous results. When the Goal has a valid local session_id, the API runner creates the same per-turn local transcript checkpoint used by the CLI runner before executing each turn.
// @Tags Tenant Goals
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param id path string true "Goal ID"
// @Param evaluator query string false "Goal evaluator: deterministic or model"
// @Param continuous query bool false "Run a bounded synchronous loop instead of one turn"
// @Param max_turns query int false "Maximum turns for continuous=true, default 5, max 20"
// @Success 200 {object} SwaggerRunGoalResponse
// @Failure 404 {object} SwaggerError
// @Failure 503 {object} SwaggerError
// @Router /tenant/goals/{id}/run [post]
func swaggerTenantGoalRun() {}

// swaggerTenantAuditList godoc
// @Summary List tenant audit logs
// @Tags Tenant Audit
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "Admin user key"
// @Param limit query int false "Limit"
// @Param cursor query int false "Offset cursor returned by the previous page"
// @Param search query string false "Search action, resource type, resource id, or trace id"
// @Success 200 {object} SwaggerListAuditLogsResponse
// @Failure 403 {object} SwaggerError
// @Router /tenant/audit [get]
func swaggerTenantAuditList() {}

// swaggerTenantTelemetryList godoc
// @Summary List tenant telemetry events
// @Tags Tenant Telemetry
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "Admin user key"
// @Param limit query int false "Limit"
// @Param cursor query int false "Offset cursor returned by the previous page"
// @Param search query string false "Search event name, category, source, status, trace id, resource, model, or tool"
// @Success 200 {object} SwaggerListTelemetryEventsResponse
// @Failure 403 {object} SwaggerError
// @Router /tenant/telemetry [get]
func swaggerTenantTelemetryList() {}

// swaggerTenantTelemetryRecord godoc
// @Summary Record a tenant telemetry event
// @Tags Tenant Telemetry
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param request body SwaggerTelemetryEventRequest true "Telemetry event"
// @Success 200 {object} SwaggerIDResponse
// @Failure 400 {object} SwaggerError
// @Router /tenant/telemetry [post]
func swaggerTenantTelemetryRecord() {}

// swaggerTenantQuotaConfigGet godoc
// @Summary Get tenant quota config
// @Tags Tenant Quota
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Success 200 {object} SwaggerTenantQuotaConfig
// @Failure 401 {object} SwaggerError
// @Failure 503 {object} SwaggerError
// @Router /tenant/quota/config [get]
func swaggerTenantQuotaConfigGet() {}

// swaggerTenantQuotaConfigSave godoc
// @Summary Create or update tenant quota config
// @Tags Tenant Quota
// @Security ApiKeyAuth
// @Accept json
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "Owner or admin user key"
// @Param request body SwaggerTenantQuotaConfigRequest true "Tenant quota config"
// @Success 200 {object} SwaggerTenantQuotaConfig
// @Failure 400 {object} SwaggerError
// @Failure 403 {object} SwaggerError
// @Failure 503 {object} SwaggerError
// @Router /tenant/quota/config [put]
func swaggerTenantQuotaConfigSave() {}

// swaggerTenantUsageDailyList godoc
// @Summary List tenant daily token usage
// @Tags Tenant Quota
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param limit query int false "Limit"
// @Param cursor query int false "Offset cursor returned by the previous page"
// @Param search query string false "Search source or model"
// @Success 200 {object} SwaggerListTenantUsageDailyResponse
// @Failure 401 {object} SwaggerError
// @Failure 503 {object} SwaggerError
// @Router /tenant/usage/daily [get]
func swaggerTenantUsageDailyList() {}

// swaggerTenantUsageLedgerList godoc
// @Summary List tenant usage ledger records
// @Tags Tenant Quota
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param limit query int false "Limit"
// @Param cursor query int false "Offset cursor returned by the previous page"
// @Param search query string false "Search request id, trace id, source, route, model, status, or error"
// @Success 200 {object} SwaggerListTenantUsageLedgerResponse
// @Failure 401 {object} SwaggerError
// @Failure 503 {object} SwaggerError
// @Router /tenant/usage/ledger [get]
func swaggerTenantUsageLedgerList() {}

// swaggerTenantQuotaEventsList godoc
// @Summary List tenant quota events
// @Tags Tenant Quota
// @Security ApiKeyAuth
// @Produce json
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User key"
// @Param limit query int false "Limit"
// @Param cursor query int false "Offset cursor returned by the previous page"
// @Param search query string false "Search event type, limit type, source, route, model, or trace id"
// @Success 200 {object} SwaggerListTenantQuotaEventsResponse
// @Failure 401 {object} SwaggerError
// @Failure 503 {object} SwaggerError
// @Router /tenant/quota/events [get]
func swaggerTenantQuotaEventsList() {}

// swaggerMobilePresignAttachment godoc
// @Summary Create a mobile attachment upload contract
// @Tags Mobile Chat
// @Security MobileJWT
// @Accept json
// @Produce json
// @Param request body SwaggerMobileAttachmentPresignRequest true "Attachment upload metadata"
// @Success 200 {object} SwaggerMobileAttachmentPresignResponse
// @Failure 400 {object} SwaggerError
// @Failure 401 {object} SwaggerError
// @Router /mobile/chat/attachments/presign [post]
func swaggerMobilePresignAttachment() {}

// swaggerMobileWebSocket godoc
// @Summary Open mobile chat WebSocket sync, Goal event, and control channel
// @Description Sends connected first. Clients can send subscribe with session_id for message_event fanout, subscribe_goal with goal_id for goal_event fanout, ping for pong, and cancel with session/message identifiers for active stream cancellation. Message and Goal fanout are scoped to the mobile JWT tenant/user.
// @Tags Mobile Chat
// @Security MobileJWT
// @Produce json
// @Success 101 {string} string "Switching Protocols"
// @Failure 401 {object} SwaggerError
// @Router /mobile/chat/ws [get]
func swaggerMobileWebSocket() {}

// swaggerMobileSessionsList godoc
// @Summary List mobile chat sessions
// @Tags Mobile Chat
// @Security MobileJWT
// @Produce json
// @Param limit query int false "Limit"
// @Success 200 {object} SwaggerListSessionsResponse
// @Failure 401 {object} SwaggerError
// @Router /mobile/chat/sessions [get]
func swaggerMobileSessionsList() {}

// swaggerMobileSessionCreate godoc
// @Summary Create mobile chat session
// @Tags Mobile Chat
// @Security MobileJWT
// @Accept json
// @Produce json
// @Param request body SwaggerMobileSessionRequest true "Mobile session request"
// @Success 200 {object} SwaggerSessionIDResponse
// @Router /mobile/chat/sessions [post]
func swaggerMobileSessionCreate() {}

// swaggerMobileSessionGet godoc
// @Summary Get mobile chat session
// @Tags Mobile Chat
// @Security MobileJWT
// @Produce json
// @Param id path int true "Session ID"
// @Success 200 {object} SwaggerMobileSessionDetailResponse
// @Router /mobile/chat/sessions/{id} [get]
func swaggerMobileSessionGet() {}

// swaggerMobileSessionUpdate godoc
// @Summary Update mobile chat session
// @Tags Mobile Chat
// @Security MobileJWT
// @Accept json
// @Produce json
// @Param id path int true "Session ID"
// @Param request body SwaggerMobileSessionRequest true "Mobile session request"
// @Success 200 {object} SwaggerIDResponse
// @Router /mobile/chat/sessions/{id} [patch]
func swaggerMobileSessionUpdate() {}

// swaggerMobileSessionPut godoc
// @Summary Replace mobile chat session fields
// @Tags Mobile Chat
// @Security MobileJWT
// @Accept json
// @Produce json
// @Param id path int true "Session ID"
// @Param request body SwaggerMobileSessionRequest true "Mobile session request"
// @Success 200 {object} SwaggerIDResponse
// @Router /mobile/chat/sessions/{id} [put]
func swaggerMobileSessionPut() {}

// swaggerMobileSessionArchive godoc
// @Summary Archive mobile chat session
// @Tags Mobile Chat
// @Security MobileJWT
// @Produce json
// @Param id path int true "Session ID"
// @Success 200 {object} SwaggerArchiveResponse
// @Router /mobile/chat/sessions/{id} [delete]
func swaggerMobileSessionArchive() {}

// swaggerMobileSessionBranch godoc
// @Summary Branch a mobile chat session
// @Tags Mobile Chat
// @Security MobileJWT
// @Accept json
// @Produce json
// @Param id path int true "Source session ID"
// @Param request body SwaggerMobileBranchRequest true "Branch request"
// @Success 200 {object} SwaggerMobileBranchResponse
// @Router /mobile/chat/sessions/{id}/branch [post]
func swaggerMobileSessionBranch() {}

// swaggerMobileMessagesList godoc
// @Summary List mobile chat messages
// @Tags Mobile Chat
// @Security MobileJWT
// @Produce json
// @Param id path int true "Session ID"
// @Param after_turn query int false "Return messages after this turn"
// @Param cursor query string false "Pagination cursor"
// @Param limit query int false "Limit"
// @Success 200 {object} SwaggerMobileMessageListResponse
// @Router /mobile/chat/sessions/{id}/messages [get]
func swaggerMobileMessagesList() {}

// swaggerMobileMessageStream godoc
// @Summary Send a mobile chat message and stream assistant response
// @Tags Mobile Chat
// @Security MobileJWT
// @Accept json
// @Produce json
// @Param id path int true "Session ID"
// @Param request body SwaggerMobileMessageStreamRequest true "Message stream request"
// @Success 200 {object} SwaggerSSEEvent
// @Router /mobile/chat/sessions/{id}/messages/stream [post]
func swaggerMobileMessageStream() {}

// swaggerMobileMessageCancel godoc
// @Summary Cancel a mobile assistant message
// @Tags Mobile Chat
// @Security MobileJWT
// @Produce json
// @Param id path int true "Session ID"
// @Param message_id path int true "Message ID"
// @Success 200 {object} SwaggerMobileCancelResponse
// @Router /mobile/chat/sessions/{id}/messages/{message_id}/cancel [post]
func swaggerMobileMessageCancel() {}

// swaggerMobileMessageRegenerate godoc
// @Summary Regenerate a mobile assistant message
// @Tags Mobile Chat
// @Security MobileJWT
// @Accept json
// @Produce json
// @Param id path int true "Session ID"
// @Param message_id path int true "Assistant message ID"
// @Param request body SwaggerMobileRegenerateRequest false "Regenerate request"
// @Success 200 {object} SwaggerSSEEvent
// @Router /mobile/chat/sessions/{id}/messages/{message_id}/regenerate [post]
func swaggerMobileMessageRegenerate() {}

// swaggerTenantImageGenerate godoc
// @Summary Generate an image for a tenant session
// @Tags Tenant Images
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "Session ID"
// @Param request body SwaggerImageGenerateRequest true "Image generation request"
// @Success 200 {object} SwaggerImageArtifactResponse
// @Failure 400,401,403,409,502,504 {object} SwaggerError
// @Router /tenant/sessions/{id}/images/generations [post]
func swaggerTenantImageGenerate() {}

// swaggerTenantImageEdit godoc
// @Summary Edit or redraw an image for a tenant session
// @Tags Tenant Images
// @Security BearerAuth
// @Accept mpfd
// @Produce json
// @Param id path int true "Session ID"
// @Param prompt formData string true "Edit prompt"
// @Param image formData file false "Source image"
// @Param source_asset_id formData string false "Existing source asset ID"
// @Success 200 {object} SwaggerImageArtifactResponse
// @Failure 400,401,403,409,502,504 {object} SwaggerError
// @Router /tenant/sessions/{id}/images/edits [post]
func swaggerTenantImageEdit() {}

// swaggerTenantImageHistory godoc
// @Summary List generated images for a tenant session
// @Tags Tenant Images
// @Security BearerAuth
// @Produce json
// @Param id path int true "Session ID"
// @Param limit query int false "Maximum records"
// @Success 200 {object} SwaggerImageGenerationListResponse
// @Router /tenant/sessions/{id}/images [get]
func swaggerTenantImageHistory() {}

// swaggerTenantImageCapabilities godoc
// @Summary List configured tenant image model capabilities
// @Tags Tenant Images
// @Security BearerAuth
// @Produce json
// @Success 200 {object} SwaggerImageCapabilitiesResponse
// @Router /tenant/images/capabilities [get]
func swaggerTenantImageCapabilities() {}

// swaggerTenantImageAsset godoc
// @Summary Read a tenant image asset
// @Tags Tenant Images
// @Security BearerAuth
// @Produce image/png
// @Param asset_id path string true "Asset ID"
// @Success 200 {file} file
// @Failure 401,403,404 {object} SwaggerError
// @Router /tenant/media/assets/{asset_id} [get]
func swaggerTenantImageAsset() {}

// SwaggerComputerOwnerResponse is identity resolution, not a permission grant.
type SwaggerComputerOwnerResponse struct {
	Data struct {
		TenantID  uint64 `json:"tenant_id"`
		UserID    uint64 `json:"user_id"`
		SessionID uint64 `json:"session_id"`
	} `json:"data"`
}

// swaggerComputerOwner godoc
// @Summary Resolve a managed conversation owner for native desktop approval
// @Description Available only on the authenticated desktop-local server with a private computer bridge. Rejects browser Origin. Resolves identity from server context and Session Control ownership; does not create or approve computer sessions.
// @Tags Session Control
// @Security ApiKeyAuth
// @Produce json
// @Param source path string true "Managed session namespace" Enums(tenant)
// @Param id path string true "Namespace-local session key"
// @Success 200 {object} SwaggerComputerOwnerResponse
// @Failure 400 {object} SwaggerSessionControlError
// @Failure 401 {object} SwaggerSessionControlError
// @Failure 403 {object} SwaggerSessionControlError
// @Failure 404 {object} SwaggerSessionControlError
// @Failure 503 {object} SwaggerSessionControlError
// @Router /tenant/session-control/sessions/{source}/{id}/computer-owner [get]
func swaggerComputerOwner() {}
