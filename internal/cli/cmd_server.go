package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/agentprofile"
	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/agenttasks/memstore"
	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/computerbridge"
	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/credentials"
	"github.com/konglong87/super-niuma/internal/defaults"
	"github.com/konglong87/super-niuma/internal/feishuprovision"
	"github.com/konglong87/super-niuma/internal/nextsteps"
	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/permissions"
	"github.com/konglong87/super-niuma/internal/promptmode"
	"github.com/konglong87/super-niuma/internal/prompttemplate"
	"github.com/konglong87/super-niuma/internal/provisioning"
	"github.com/konglong87/super-niuma/internal/query"
	"github.com/konglong87/super-niuma/internal/quota"
	"github.com/konglong87/super-niuma/internal/scheduler"
	"github.com/konglong87/super-niuma/internal/server"
	"github.com/konglong87/super-niuma/internal/session"
	"github.com/konglong87/super-niuma/internal/sessioncontrol"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	"github.com/konglong87/super-niuma/internal/supervisor"
	"github.com/konglong87/super-niuma/internal/telemetry"
	tenantservice "github.com/konglong87/super-niuma/internal/tenant"
	"github.com/konglong87/super-niuma/internal/tools"
	sessioncontroltool "github.com/konglong87/super-niuma/internal/tools/sessioncontrol"
)

// newServerSessionControlService is the sole server-to-tool composition seam;
// HTTP handlers and the Orchestrator profile receive the same service instance.
var newServerSessionControlService = func(opts server.Options) sessioncontroltool.Service { return opts.SessionControl }

func serverCommand(ctx context.Context, args []string, opts options, stdout io.Writer) error {
	serverOpts := server.Options{Host: "127.0.0.1", Port: 0, Workspace: defaultServerWorkspace(opts.cwd)}
	desktopLocal := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--host":
			v, err := flagValue(args, &i, "--host")
			if err != nil {
				return err
			}
			serverOpts.Host = v
		case "--port":
			v, err := flagValue(args, &i, "--port")
			if err != nil {
				return err
			}
			port, err := strconv.Atoi(v)
			if err != nil || port < 0 || port > 65535 {
				return fmt.Errorf("--port must be a number between 0 and 65535, got %q", v)
			}
			serverOpts.Port = port
		case "--auth-token":
			v, err := flagValue(args, &i, "--auth-token")
			if err != nil {
				return err
			}
			serverOpts.AuthToken = v
		case "--workspace":
			v, err := flagValue(args, &i, "--workspace")
			if err != nil {
				return err
			}
			serverOpts.Workspace = v
		case "--desktop-local":
			desktopLocal = true
		default:
			return fmt.Errorf("unknown server option: %s", args[i])
		}
	}
	bridgeConfig, bridgeErr := computerbridge.ConsumeLaunchConfig(desktopLocal && serverOpts.AuthToken != "")
	if bridgeErr != nil {
		return bridgeErr
	}
	if bridgeConfig != nil {
		bridge, err := computerbridge.NewClient(*bridgeConfig)
		if err != nil {
			return err
		}
		opts.desktopComputerBridge = bridge
	}
	serverOpts.DesktopComputerOwnerLookup = bridgeConfig != nil && desktopLocal && serverOpts.AuthToken != ""
	// 绑定安全检查排在最前面：Run 里也有同样的检查，但那时 MySQL 连接已经建好、
	// "Starting server on ..." 也已经打出去了，紧跟一条拒绝启动读起来自相矛盾。
	if err := server.ValidateBind(serverOpts); err != nil {
		return err
	}
	environments, closeEnvironments, environmentErr := configuredSettingsEnvironments(ctx)
	if environmentErr != nil {
		return environmentErr
	}
	defer closeEnvironments()
	serverOpts.SettingsEnvironments = environments
	serverOpts.StatusFunc = func(context.Context) (any, error) {
		return statusPayload(serverOpts.Workspace), nil
	}
	// Snapshot the same CLI overrides and named route used by query creation.
	// A broken default must still allow the settings center to repair the file.
	startupSnapshot, snapshotErr := serverStartupSettingsSnapshot(opts, serverOpts.Workspace)
	if snapshotErr != nil {
		observability.Warn(ctx, nil, "server.settings.snapshot", "cli.serverCommand", "startup provider configuration could not be resolved; startup snapshot unavailable")
	} else {
		serverOpts.ProcessSettingsSnapshot = startupSnapshot
	}
	serverOpts.Models = serverModelCatalog(serverOpts.Workspace)
	serverOpts.Providers = serverProviderCatalog(serverOpts.Workspace)
	serverOpts.SessionsFunc = func(context.Context) (any, error) {
		return session.DefaultStore().List()
	}
	serverOpts.ToolsFunc = func(ctx context.Context) (any, error) {
		toolsOpts := opts
		toolsOpts.imageGenerator = serverOpts.ImageGenerator
		querySession, cleanup, err := newQuerySession(ctx, toolsOpts, nil, nil)
		if err != nil {
			return nil, err
		}
		defer cleanup()
		return querySession.ToolDefinitions(), nil
	}
	sessionBackend := sessioncontrol.SessionBackend("")
	if os.Getenv("NIUMA_DESKTOP_MODE") == "1" || desktopLocal {
		var err error
		sessionBackend, err = sessioncontrol.ParseSessionBackend(os.Getenv("NIUMA_DESKTOP_SESSION_BACKEND"))
		if err != nil {
			return err
		}
		serverOpts.SessionBackend = sessionBackend
	}
	tenantStorageMode := "disabled"
	if sqlitePath := strings.TrimSpace(os.Getenv("NIUMA_SQLITE_PATH")); sqlitePath != "" {
		repo, err := mysqlstore.OpenSQLiteGormRepository(ctx, sqlitePath, nil)
		if err != nil {
			return fmt.Errorf("open desktop sqlite repository: %w", err)
		}
		defer func() { _ = repo.Close() }()
		tenantSvc := tenantservice.NewService(repo, nil)
		tenantKey := firstEnv("NIUMA_TENANT_KEY", "TENANT_KEY")
		if tenantKey == "" {
			tenantKey = "webui-local"
		}
		userKey := firstEnv("NIUMA_USER_ID", "USER_ID")
		if userKey == "" {
			userKey = "webui-local-user"
		}
		tenantID, err := repo.UpsertTenant(ctx, mysqlstore.TenantInput{TenantKey: tenantKey, Name: "Local Desktop"})
		if err != nil {
			return fmt.Errorf("initialize desktop tenant: %w", err)
		}
		userID, err := repo.EnsureUser(ctx, tenantID, userKey)
		if err != nil {
			return fmt.Errorf("initialize desktop user: %w", err)
		}
		if serverOpts.DesktopComputerOwnerLookup {
			serverOpts.DesktopComputerOwnerResolver = func(requestCtx context.Context) (context.Context, sessioncontrol.RequestContext, error) {
				trustedCtx := observability.WithRequestValues(requestCtx, observability.TraceID(requestCtx), userKey, tenantKey)
				bound, resolved, err := tenantSvc.ResolveContextOnce(trustedCtx)
				return bound, sessioncontrol.RequestContext{TenantID: resolved.TenantID, UserID: resolved.UserID, ActorUserID: resolved.UserID}, err
			}
		}
		if err := repo.SetUserRole(ctx, tenantID, userID, mysqlstore.DesktopLocalUserRole); err != nil {
			return fmt.Errorf("initialize desktop user role: %w", err)
		}
		serverOpts.TenantService = tenantSvc
		serverOpts.PromptTemplateService = prompttemplate.NewService(repo)
		workflowCleanup, workflowErr := configureWorkflowRuntime(ctx, repo, opts, &serverOpts)
		if workflowErr != nil {
			return fmt.Errorf("configure workflow runtime: %w", workflowErr)
		}
		defer workflowCleanup()
		serverOpts.AgentTaskStore = tenantSvc
		serverOpts.PendingInputQueue = repo
		serverOpts.SessionControlEvents = tenantSvc
		serverOpts.AgentTaskReaperStore = repo
		serverOpts.SettingsDatabase = "sqlite"
		serverOpts.ReadinessProbes = append(serverOpts.ReadinessProbes, server.ReadinessProbe{
			Name: "sqlite",
			Check: func(ctx context.Context) error {
				_, err := repo.ListTenants(ctx, 1)
				return err
			},
		})
		workerSupervisor := supervisor.ScreenSupervisor{
			ScriptPath: filepath.Join(serverOpts.Workspace, "scripts", "channel-worker-screen.sh"),
			BaseEnv: []string{
				"NIUMA_SQLITE_PATH=" + sqlitePath,
				"NIUMA_FEISHU_CREDENTIAL_FILE=" + channelCredentialPath(),
			},
		}
		provisioningService := &provisioning.Service{
			Repo: repo, Channels: repo,
			Credentials: credentials.FileStore{Dir: filepath.Dir(channelCredentialPath()), LegacyPath: channelCredentialPath()},
			Feishu:      feishuprovision.HTTPProvisioner{}, Supervisor: workerSupervisor, Inventory: workerSupervisor,
		}
		if err := provisioningService.Reconcile(ctx, tenantID); err != nil {
			return fmt.Errorf("reconcile desktop channel accounts: %w", err)
		}
		serverOpts.ProvisioningService = provisioningService
		serverOpts.FeishuOnboarding = server.NewFeishuOnboardingManager(provisioningService)
		tenantStorageMode = "sqlite"
		if err := configureServerImageRuntime(serverOpts.Workspace, opts.settingsInputs, repo, &serverOpts); err != nil {
			return err
		}
	} else if dsn := firstEnv("NIUMA_MYSQL_DSN", "MYSQL_DSN"); dsn != "" {
		serverOpts.SettingsDatabase = serverSettingsDatabase(dsn)
		repo, err := mysqlstore.OpenGormRepository(ctx, dsn, nil)
		if err != nil {
			return fmt.Errorf("open tenant mysql gorm repository: %w", err)
		}
		defer func() {
			_ = repo.Close()
		}()
		tenantSvc := tenantservice.NewService(repo, nil)
		serverOpts.TenantService = tenantSvc
		serverOpts.PromptTemplateService = prompttemplate.NewService(repo)
		workflowCleanup, workflowErr := configureWorkflowRuntime(ctx, repo, opts, &serverOpts)
		if workflowErr != nil {
			return fmt.Errorf("configure workflow runtime: %w", workflowErr)
		}
		defer workflowCleanup()
		scheduleStore := scheduler.DefaultStore()
		executable, executableErr := os.Executable()
		if executableErr != nil {
			return fmt.Errorf("resolve scheduler executable: %w", executableErr)
		}
		serverOpts.SessionMonitor = sessioncontrol.NewSessionMonitor(sessioncontrol.SessionMonitorDependencies{
			Store: tenantSvc, Schedules: &scheduleStore,
			EnsureDaemon: func() error {
				pid, _, err := scheduleStore.EnsureDaemon(executable, os.Environ())
				if err != nil {
					return err
				}
				if pid <= 0 {
					return fmt.Errorf("scheduler daemon returned no pid")
				}
				return nil
			},
		})
		if err := configureServerImageRuntime(serverOpts.Workspace, opts.settingsInputs, repo, &serverOpts); err != nil {
			return err
		}
		workerSupervisor := supervisor.ScreenSupervisor{ScriptPath: filepath.Join(serverOpts.Workspace, "scripts", "channel-worker-screen.sh")}
		provisioningService := &provisioning.Service{
			Repo: repo, Channels: repo,
			Credentials: credentials.FileStore{Dir: filepath.Dir(channelCredentialPath()), LegacyPath: channelCredentialPath()},
			Feishu:      feishuprovision.HTTPProvisioner{}, Supervisor: workerSupervisor, Inventory: workerSupervisor,
		}
		if err := provisioningService.ReconcileAll(ctx); err != nil {
			return fmt.Errorf("reconcile tenant channel accounts: %w", err)
		}
		serverOpts.ProvisioningService = provisioningService
		serverOpts.FeishuOnboarding = server.NewFeishuOnboardingManager(provisioningService)
		serverOpts.AgentTaskStore = tenantSvc
		serverOpts.PendingInputQueue = repo
		serverOpts.SessionControlEvents = tenantSvc
		// reaper 直接用 repo：它要跨租户扫 running 任务，再按行归属写回。
		serverOpts.AgentTaskReaperStore = repo
		// /readyz 的 MySQL 探活。用一条 LIMIT 1 的索引查询而不是纯 Ping：连接池
		// 活着但库被删/权限被收也必须算 not ready（AUDIT-P1-22）。
		serverOpts.ReadinessProbes = append(serverOpts.ReadinessProbes, server.ReadinessProbe{
			Name: "mysql",
			Check: func(ctx context.Context) error {
				_, err := repo.ListTenants(ctx, 1)
				return err
			},
		})
		tenantStorageMode = "mysql"
	} else {
		serverOpts.AgentTaskStore = memstore.New()
		tenantStorageMode = "memory"
	}
	if serverOpts.SessionBackend == sessioncontrol.SessionBackendJSONL && serverOpts.TenantService != nil {
		var fallback server.SessionConversationEventReader
		if reader, ok := serverOpts.TenantService.(server.SessionConversationEventReader); ok {
			fallback = reader
		}
		serverOpts.SessionEvents = server.NewJSONLSessionEventStoreWithFallback(session.DefaultStore(), fallback)
		serverOpts.SessionControlEvents = server.NewSessionControlEventReader(serverOpts)
	}
	serverOpts.AgentTaskController = agenttasks.NewController()
	if raw := firstEnv("NIUMA_STRUCTURED_SKILL_ROUTES", "STRUCTURED_SKILL_ROUTES"); raw != "" {
		routes, err := parseStructuredSkillRoutes(raw)
		if err != nil {
			return err
		}
		serverOpts.StructuredSkillRoutes = routes
	}
	serverOpts.MobileJWTSecret = firstEnv("NIUMA_MOBILE_JWT_SECRET", "MOBILE_JWT_SECRET")
	serverOpts.MobileDevAuth = firstEnvBool("NIUMA_MOBILE_DEV_AUTH", "MOBILE_DEV_AUTH")
	serverOpts.WebUIDir = firstEnv("NIUMA_WEBUI_DIR", "WEBUI_DIR")
	serverOpts.SessionControlEnableLocalRead = firstEnvBool("NIUMA_SESSION_CONTROL_LOCAL_READ")
	serverOpts.AgentTaskRunTimeout = time.Duration(firstEnvInt("NIUMA_AGENT_TASK_RUN_TIMEOUT_SECONDS", "AGENT_TASK_RUN_TIMEOUT_SECONDS")) * time.Second
	serverOpts.AgentTaskIdleTimeout = time.Duration(firstEnvInt("NIUMA_AGENT_TASK_IDLE_TIMEOUT_SECONDS", "AGENT_TASK_IDLE_TIMEOUT_SECONDS")) * time.Second
	serverOpts.AgentTaskMaxConcurrentRuns = firstEnvInt("NIUMA_AGENT_TASK_MAX_CONCURRENT_RUNS", "AGENT_TASK_MAX_CONCURRENT_RUNS")
	serverOpts.MobilePolicy = server.MobilePolicy{
		AllowedModels:       splitCSVArg(firstEnv("NIUMA_MOBILE_ALLOWED_MODELS", "MOBILE_ALLOWED_MODELS")),
		RateLimitPerMinute:  firstEnvInt("NIUMA_MOBILE_RATE_LIMIT_PER_MINUTE", "MOBILE_RATE_LIMIT_PER_MINUTE"),
		DailyMessageQuota:   firstEnvInt("NIUMA_MOBILE_DAILY_MESSAGE_QUOTA", "MOBILE_DAILY_MESSAGE_QUOTA"),
		DailyTokenQuota:     firstEnvInt("NIUMA_MOBILE_DAILY_TOKEN_QUOTA", "MOBILE_DAILY_TOKEN_QUOTA"),
		MaxAttachmentBytes:  firstEnvInt64("NIUMA_MOBILE_MAX_ATTACHMENT_BYTES", "MOBILE_MAX_ATTACHMENT_BYTES"),
		AllowedAttachments:  splitCSVArg(firstEnv("NIUMA_MOBILE_ALLOWED_ATTACHMENTS", "MOBILE_ALLOWED_ATTACHMENTS")),
		MaxAttachmentsCount: firstEnvInt("NIUMA_MOBILE_MAX_ATTACHMENTS", "MOBILE_MAX_ATTACHMENTS"),
	}
	serverOpts.MobileUploadBaseURL = firstEnv("NIUMA_MOBILE_UPLOAD_BASE_URL", "MOBILE_UPLOAD_BASE_URL")
	if bucket := firstEnv("NIUMA_MOBILE_S3_BUCKET", "MOBILE_S3_BUCKET"); bucket != "" {
		signer, err := server.NewS3MobileUploadSigner(server.MobileS3UploadConfig{
			Endpoint:      firstEnv("NIUMA_MOBILE_S3_ENDPOINT", "MOBILE_S3_ENDPOINT"),
			Region:        firstEnv("NIUMA_MOBILE_S3_REGION", "MOBILE_S3_REGION"),
			Bucket:        bucket,
			AccessKey:     firstEnv("NIUMA_MOBILE_S3_ACCESS_KEY", "MOBILE_S3_ACCESS_KEY", "AWS_ACCESS_KEY_ID"),
			SecretKey:     firstEnv("NIUMA_MOBILE_S3_SECRET_KEY", "MOBILE_S3_SECRET_KEY", "AWS_SECRET_ACCESS_KEY"),
			SessionToken:  firstEnv("NIUMA_MOBILE_S3_SESSION_TOKEN", "MOBILE_S3_SESSION_TOKEN", "AWS_SESSION_TOKEN"),
			Prefix:        firstEnv("NIUMA_MOBILE_S3_PREFIX", "MOBILE_S3_PREFIX"),
			PublicBaseURL: firstEnv("NIUMA_MOBILE_S3_PUBLIC_BASE_URL", "MOBILE_S3_PUBLIC_BASE_URL", "NIUMA_MOBILE_UPLOAD_BASE_URL", "MOBILE_UPLOAD_BASE_URL"),
			UsePathStyle:  firstEnvBool("NIUMA_MOBILE_S3_PATH_STYLE", "MOBILE_S3_PATH_STYLE"),
			PresignTTL:    time.Duration(firstEnvInt("NIUMA_MOBILE_S3_PRESIGN_TTL_SECONDS", "MOBILE_S3_PRESIGN_TTL_SECONDS")) * time.Second,
		})
		if err != nil {
			return fmt.Errorf("configure mobile s3 upload signer: %w", err)
		}
		serverOpts.MobileUploadSigner = signer
	}
	// Redis is the multi-instance quota backend; the file store remains the
	// local-dev fallback when no shared counter service is configured.
	if redisAddr := firstEnv("NIUMA_MOBILE_REDIS_ADDR", "MOBILE_REDIS_ADDR"); redisAddr != "" {
		store := server.NewRedisMobileUsageStoreFromAddr(
			redisAddr,
			firstEnv("NIUMA_MOBILE_REDIS_PASSWORD", "MOBILE_REDIS_PASSWORD"),
			firstEnvInt("NIUMA_MOBILE_REDIS_DB", "MOBILE_REDIS_DB"),
			firstEnv("NIUMA_MOBILE_REDIS_PREFIX", "MOBILE_REDIS_PREFIX"),
			time.Now,
		)
		if err := pingRedisAtStartup(ctx, "mobile usage", redisAddr, store.Ping); err != nil {
			return err
		}
		serverOpts.MobileUsageStore = store
		serverOpts.ReadinessProbes = append(serverOpts.ReadinessProbes, server.ReadinessProbe{Name: "mobile_redis", Check: store.Ping})
	} else if usagePath := firstEnv("NIUMA_MOBILE_USAGE_STORE_PATH", "MOBILE_USAGE_STORE_PATH"); usagePath != "" {
		serverOpts.MobileUsageStore = server.NewMobileUsageFileStore(usagePath, time.Now)
	}
	if redisAddr := firstEnv("NIUMA_QUOTA_REDIS_ADDR", "QUOTA_REDIS_ADDR"); redisAddr != "" {
		store := quota.NewRedisStoreFromAddr(
			redisAddr,
			firstEnv("NIUMA_QUOTA_REDIS_PASSWORD", "QUOTA_REDIS_PASSWORD"),
			firstEnvInt("NIUMA_QUOTA_REDIS_DB", "QUOTA_REDIS_DB"),
			firstEnv("NIUMA_QUOTA_REDIS_PREFIX", "QUOTA_REDIS_PREFIX"),
			time.Now,
		)
		if err := pingRedisAtStartup(ctx, "quota", redisAddr, store.Ping); err != nil {
			return err
		}
		serverOpts.QuotaStore = store
		serverOpts.ReadinessProbes = append(serverOpts.ReadinessProbes, server.ReadinessProbe{Name: "quota_redis", Check: store.Ping})
	}
	serverOpts.QueryRateLimitPerMinute = firstEnvInt("NIUMA_QUERY_RATE_LIMIT_PER_MINUTE", "QUERY_RATE_LIMIT_PER_MINUTE")
	if exportURL := firstEnv("NIUMA_TELEMETRY_EXPORT_URL", "TELEMETRY_EXPORT_URL"); exportURL != "" {
		format := firstEnv("NIUMA_TELEMETRY_EXPORT_FORMAT", "TELEMETRY_EXPORT_FORMAT")
		service := firstEnv("NIUMA_TELEMETRY_EXPORT_SERVICE", "TELEMETRY_EXPORT_SERVICE")
		headers := telemetryExportHeaders(format)
		serverOpts.TelemetrySinks = append(serverOpts.TelemetrySinks, telemetry.NewVendorHTTPSink(exportURL, format, service, headers))
	}
	validateRequestCWD, err := newServerRequestCWDValidator(serverOpts.Workspace, desktopLocal)
	if err != nil {
		return err
	}
	serverOpts.SessionControlCWDValidator = func(cwd string) (string, error) {
		return validateRequestCWD(cwd)
	}
	serverOpts.SessionControlRouteResolver = func(cwd, provider, model string) (string, string, error) {
		routeOpts := opts
		if cwd != "" {
			routeOpts.cwd = cwd
		}
		if provider != "" {
			routeOpts.providerName = provider
		}
		if model != "" {
			routeOpts.model, routeOpts.modelExplicit = model, true
		}
		cfg, err := resolveRuntimeProviderConfig(&routeOpts)
		if err != nil {
			return "", "", err
		}
		if _, err := config.ResolveProviderProtocol(cfg.Provider, cfg.ProviderProtocol, cfg.Responses); err != nil {
			return "", "", err
		}
		if routeOpts.model == "" {
			routeOpts.model = config.ResolveModel(routeOpts.cwd, "")
		}
		return cfg.SelectedProvider, routeOpts.model, nil
	}
	serverOpts.SessionControlConfigResolver = func(cwd string, requested sessioncontrol.RuntimeConfig) (sessioncontrol.RuntimeConfig, error) {
		return resolveServerSessionRuntimeConfig(opts, cwd, requested)
	}
	if _, _, err := serverOpts.SessionControlRouteResolver(serverOpts.Workspace, "", ""); err != nil {
		observability.Warn(ctx, nil, "server.provider.preflight", "cli.serverCommand", "default provider route is invalid; configure a valid route before creating a conversation")
	}
	runServerQuery := func(ctx context.Context, req server.QueryRequest, textSink io.Writer) (query.Result, error) {
		queryOpts := opts
		if err := applyServerSessionRuntimeOptions(&queryOpts, req); err != nil {
			return query.Result{}, err
		}
		queryOpts.queryAttachments = serverQueryAttachments(req.Attachments)
		explicitProfileRequested := req.ProfileID != "" || req.ProfileVersion > 0 || len(req.ProfileOverrides) > 0
		profileApplied := false
		if queryOpts.promptMode == "" {
			queryOpts.promptMode = serverDefaultPromptMode().String()
		}
		if req.CWD != "" {
			// 请求体里的 cwd 直接决定服务端工具的执行目录。校验放在这里而不是各个
			// handler 里，是因为 runServerQuery 是所有入口（/query、
			// /v1/chat/completions、agent task、mobile）唯一的收敛点（AUDIT-P1-27）。
			resolved, err := validateRequestCWD(req.CWD)
			if err != nil {
				return query.Result{}, err
			}
			queryOpts.cwd = resolved
		}
		if req.Model != "" {
			queryOpts.model, queryOpts.modelExplicit = req.Model, true
		}
		if req.Provider != "" {
			queryOpts.providerName = req.Provider
		}
		if req.SystemPrompt != "" {
			queryOpts.systemPrompt = req.SystemPrompt
		}
		if req.PromptMode != "" {
			queryOpts.promptMode = promptmode.Parse(req.PromptMode, promptmode.Code).String()
		}
		if req.MaxTokens > 0 {
			queryOpts.maxTokens = req.MaxTokens
		}
		if req.MaxTurns > 0 {
			queryOpts.maxTurns = req.MaxTurns
		}
		if req.TraceID != "" {
			queryOpts.traceID = req.TraceID
			ctx = observability.WithTraceID(ctx, req.TraceID)
		}
		if req.TenantID != 0 {
			queryOpts.tenantID = req.TenantID
		}
		if req.UserID != 0 {
			queryOpts.tenantUserID = req.UserID
		}
		if req.TenantSessionID != 0 {
			queryOpts.tenantSessionID = req.TenantSessionID
		}
		if eventSink, ok := textSink.(server.AgentTaskEventSink); ok {
			queryOpts.permissionPrompt = func(promptCtx context.Context, permissionReq tools.PermissionPromptRequest) tools.PermissionPromptResponse {
				response, err := eventSink.OnPermissionRequest(promptCtx, permissionReq)
				if err != nil {
					return tools.PermissionPromptResponse{Allowed: false, Reason: err.Error(), Decision: "deny"}
				}
				return response
			}
			// 把 Task 子代理进度接到 sink,使 web-agent 也能像 TUI 一样落 nested_agent_progress 事件。
			queryOpts.nestedAgentProgress = func(ev agenttasks.EventInput) {
				_ = eventSink.OnNestedAgentProgress(ctx, ev)
			}
		}
		applyServerUserQuestionPrompt(&queryOpts, textSink)
		queryOpts.disableTools = req.DisableTools
		// The server owns provider resolution and persistence construction; the
		// CLI runtime only forwards the already-authorized capability to tools.
		queryOpts.imageGenerator = serverOpts.ImageGenerator
		queryOpts.imageBlobStore = serverOpts.ImageBlobStore
		queryOpts.mediaAssetStore = serverOpts.MediaAssetStore
		queryOpts.inlineTenantSkills = append([]string(nil), req.InlineTenantSkills...)
		queryOpts.inlineTenantSkillSource = req.InlineTenantSkillSource
		queryOpts.responseFormat = serverResponseFormatToAnthropic(req.ResponseFormat)
		queryOpts.agentTaskStore = serverOpts.AgentTaskStore
		queryOpts.agentTaskController = serverOpts.AgentTaskController
		queryOpts.sessionControlService = newServerSessionControlService(serverOpts)
		if !req.DisableTenantPersistence {
			runtime, err := prepareServerTenantRuntime(ctx, serverOpts.TenantService, req, queryOpts.cwd)
			if err != nil {
				return query.Result{}, err
			}
			if runtime.enabled {
				queryOpts.agentTaskStore = serverOpts.AgentTaskStore
				queryOpts.tenantID = runtime.tenantID
				queryOpts.tenantUserID = runtime.userID
				queryOpts.tenantSessionID = runtime.sessionID
				queryOpts.traceID = runtime.traceID
				queryOpts.skillProvider = server.TenantSkillProvider{Service: serverOpts.TenantService}
				profileRequested := req.ProfileID != "" || req.ProfileVersion > 0 || len(req.ProfileOverrides) > 0 || req.ProfileSurface != ""
				desktopProfileDefault := tenantStorageMode == "sqlite" && !profileRequested
				if profileRequested || desktopProfileDefault {
					profileService, ok := serverOpts.TenantService.(server.AgentProfileService)
					if !ok {
						return query.Result{}, fmt.Errorf("agent profile service is not configured")
					}
					overrides, err := profileOverridesFromMap(req.ProfileOverrides)
					if err != nil {
						return query.Result{}, err
					}
					surface := req.ProfileSurface
					if surface == "" {
						if promptmode.Parse(req.PromptMode, promptmode.Code).IsChat() {
							surface = agentprofile.SurfaceWebChat
						} else {
							surface = agentprofile.SurfaceTenantAgent
						}
					}
					effective, err := profileService.ResolveAgentProfile(ctx, agentprofile.ResolveRequest{Surface: surface, ProfileKey: req.ProfileID, ProfileVersion: req.ProfileVersion, Overrides: overrides})
					if err != nil {
						// The resolver owns builtin and assignment fallback semantics.
						// Propagate explicit profile failures without duplicating a
						// fallback that could bypass its validation and policy.
						return query.Result{}, err
					}
					if err := applyAgentProfileToOptions(&queryOpts, effective); err != nil {
						return query.Result{}, err
					}
					profileApplied = true
					req.ProfileID = effective.ProfileKey
					req.ProfileVersion = effective.ProfileVersion
					req.ProfileSource = effective.Source
					req.ProfileRequestedHash = effective.RequestedHash
					req.ProfileEffectiveHash = effective.EffectiveHash
					req.ProfileBlockedOverrides = len(effective.BlockedOverrides)
					// Request-level scalar fields remain the highest-precedence compatibility override.
					if req.Model != "" {
						queryOpts.model, queryOpts.modelExplicit = req.Model, true
					}
					if req.Provider != "" {
						queryOpts.providerName = req.Provider
					}
					if req.PromptMode != "" {
						queryOpts.promptMode = promptmode.Parse(req.PromptMode, promptmode.Code).String()
					}
					if req.MaxTokens > 0 {
						queryOpts.maxTokens = req.MaxTokens
					}
					if req.MaxTurns > 0 {
						queryOpts.maxTurns = req.MaxTurns
					}
				}
				mode := promptmode.Parse(queryOpts.promptMode, promptmode.Code)
				var tenantContext server.TenantPromptContext
				var err error
				if mode.IsChat() {
					tenantContext, err = server.BuildTenantContextAddendumForPrompt(ctx, serverOpts.TenantService, req.Prompt)
				} else {
					tenantContext, err = server.BuildTenantCodeMemoryAddendum(ctx, serverOpts.TenantService)
				}
				if err != nil {
					return query.Result{}, err
				}
				tenantContext.Manifest.Resolved = true
				queryOpts.appendSystem = appendWithBlankLine(queryOpts.appendSystem, tenantContext.Text)
				queryOpts.tenantContextManifest = tenantContext.Manifest
			}
		}
		if explicitProfileRequested && !profileApplied {
			return query.Result{}, fmt.Errorf("agent profile requires tenant runtime context")
		}
		querySession, cleanup, err := newQuerySession(ctx, queryOpts, req.InitialMessages, nil)
		if err != nil {
			return query.Result{}, err
		}
		defer cleanup()
		if eventSink, ok := textSink.(server.AgentTaskEventSink); ok {
			return querySession.RunWithCallbacks(ctx, req.Prompt, textSink, query.SinkCallbacks(ctx, eventSink))
		}
		return querySession.Run(ctx, req.Prompt, textSink)
	}
	serverOpts.SessionTitleFunc = func(ctx context.Context, prompt, response string) (string, error) {
		titleCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		titlePrompt := "Generate a concise session title for this conversation. Return only the title, no quotes, no punctuation-only decoration, maximum 8 words.\n\nUser:\n" +
			prompt + "\n\nAssistant:\n" + response
		result, err := runServerQuery(titleCtx, server.QueryRequest{
			Prompt:       titlePrompt,
			SystemPrompt: "You generate short, human-readable conversation titles. Return only one concise title.",
			// MaxTokens 必须保持 < 1024：meta 调用与主循环共用 newQuerySession 会拿到
			// Options.Effort，靠 ThinkingConfigFromEffort 的预算钳制保证不开思考
			// （回归测试 TestCurrentThinkingConfigNilForSmallMaxTokensMetaCalls）。
			MaxTokens:                32,
			DisableTenantPersistence: true,
		}, io.Discard)
		if err != nil {
			return "", err
		}
		return result.Response, nil
	}
	serverOpts.NextStepsFunc = func(ctx context.Context, prompt, response string, toolNames []string, model, provider string, count int) ([]string, error) {
		stepPrompt := nextsteps.BuildPrompt(nextsteps.Input{UserPrompt: prompt, AssistantResponse: response, ToolNames: toolNames}, count)
		if stepPrompt == "" {
			return nil, nil
		}
		result, err := runServerQuery(ctx, server.QueryRequest{
			Prompt:   stepPrompt,
			Model:    model,
			Provider: provider,
			// MaxTokens 必须保持 < 1024：meta 调用与主循环共用 newQuerySession 会拿到
			// Options.Effort，靠 ThinkingConfigFromEffort 的预算钳制保证不开思考
			// （回归测试 TestCurrentThinkingConfigNilForSmallMaxTokensMetaCalls）。
			MaxTokens:                nextsteps.DefaultMaxTokens,
			DisableTools:             true,
			DisableTenantPersistence: true,
			SkipAutoTitle:            true,
		}, io.Discard)
		if err != nil {
			return nil, err
		}
		return nextsteps.Parse(result.Response, count), nil
	}
	// Web next-step generation is on: the main task reaches `completed` first,
	// and the WebUI reads any late `next_steps` event back through the existing
	// REST event cursor after the SSE stream closes. The field stays an explicit
	// opt-in because delivery depends on that client contract; embedders without
	// such a client should leave it off.
	serverOpts.NextStepsWebEnabled = true
	serverOpts.StreamQueryFunc = runServerQuery
	serverOpts.SessionControlRunDetached = true
	if serverOpts.TenantService != nil {
		server.PrepareSessionControlRuntime(ctx, &serverOpts, func(ctx context.Context, req server.QueryRequest) (query.Result, error) {
			return runServerQuery(ctx, req, io.Discard)
		})
		sessionControlService, err := server.NewSessionControlService(serverOpts, func(ctx context.Context, req server.QueryRequest) (query.Result, error) {
			return runServerQuery(ctx, req, io.Discard)
		})
		if err != nil {
			return fmt.Errorf("configure session control: %w", err)
		}
		serverOpts.SessionControl = sessionControlService
	}
	fmt.Fprintf(stdout, "Starting server on %s:%d\n", serverOpts.Host, serverOpts.Port)
	fmt.Fprintf(stdout, "Tenant storage: %s\n", tenantStorageMode)
	if serverOpts.SessionBackend.Valid() {
		fmt.Fprintf(stdout, "Session backend: %s\n", serverOpts.SessionBackend)
	}
	fmt.Fprintf(stdout, "Mobile auth: %s\n", serverMobileAuthMode(serverOpts))
	return server.Run(ctx, serverOpts, func(ctx context.Context, req server.QueryRequest) (query.Result, error) {
		return runServerQuery(ctx, req, io.Discard)
	})
}

func configureServerImageRuntime(cwd string, settingsInputs []string, repo *mysqlstore.GormRepository, serverOpts *server.Options) error {
	if serverOpts == nil {
		return fmt.Errorf("server image runtime options are required")
	}
	imageGenerator, imageBlobStore, imageMediaStore, imageHistoryStore, err := configureImageGenerationWithSettingsInputs(cwd, settingsInputs, repo)
	if err != nil {
		return fmt.Errorf("configure image generation: %w", err)
	}
	imageCatalog, err := resolveImageCatalogWithSettingsInputs(cwd, settingsInputs)
	if err != nil {
		return fmt.Errorf("resolve image capabilities: %w", err)
	}
	serverOpts.ImageGenerator = imageGenerator
	serverOpts.ImageBlobStore = imageBlobStore
	serverOpts.MediaAssetStore = imageMediaStore
	serverOpts.ImageGenerationStore = imageHistoryStore
	serverOpts.ImageCatalog = imageCatalog
	return nil
}

func applyServerUserQuestionPrompt(opts *options, sink any) {
	questions, ok := sink.(server.AgentTaskUserQuestionSink)
	if !ok {
		return
	}
	opts.userQuestionPrompt = func(ctx context.Context, request tools.UserQuestionRequest) tools.UserQuestionResponse {
		response, err := questions.OnUserQuestion(ctx, request)
		if err != nil {
			return tools.UserQuestionResponse{Error: "User question could not be answered: " + err.Error()}
		}
		return response
	}
}

func applyServerSessionRuntimeOptions(opts *options, req server.QueryRequest) error {
	validated, err := sessioncontrol.NormalizeRuntimeConfig(sessioncontrol.RuntimeConfig{PermissionMode: req.PermissionMode, Effort: req.Effort})
	if err != nil {
		return err
	}
	if validated.PermissionMode != "" {
		opts.permissionMode = validated.PermissionMode
		opts.permissionModeExplicit = true
		opts.skipPermissions = false
		opts.permissionBypass = permissions.ModeGrantsBypass(validated.PermissionMode)
	}
	if validated.Effort != "" {
		opts.effort = validated.Effort
	}
	return nil
}

func serverStartupSettingsSnapshot(base options, workspace string) (map[string]any, error) {
	base.cwd = workspace
	cfg, err := resolveRuntimeProviderConfig(&base)
	if err != nil {
		return nil, err
	}
	if base.model == "" {
		base.model = config.ResolveModel(base.cwd, "")
	}
	return map[string]any{
		"provider": cfg.Provider, "model": base.model,
		"providerProtocol": cfg.ProviderProtocol, "baseURL": cfg.BaseURL,
		"settingsSources": append([]string(nil), cfg.Sources...),
	}, nil
}

func resolveServerSessionRuntimeConfig(base options, cwd string, requested sessioncontrol.RuntimeConfig) (sessioncontrol.RuntimeConfig, error) {
	resolved, err := sessioncontrol.NormalizeRuntimeConfig(requested)
	if err != nil {
		return sessioncontrol.RuntimeConfig{}, err
	}
	if cwd != "" {
		base.cwd = cwd
	}
	if resolved.Provider != "" {
		base.providerName = resolved.Provider
	}
	if resolved.Model != "" {
		base.model, base.modelExplicit = resolved.Model, true
	}
	if err := applyServerSessionRuntimeOptions(&base, server.QueryRequest{PermissionMode: resolved.PermissionMode, Effort: resolved.Effort}); err != nil {
		return sessioncontrol.RuntimeConfig{}, err
	}
	cfg, err := resolveRuntimeProviderConfig(&base)
	if err != nil {
		return sessioncontrol.RuntimeConfig{}, err
	}
	if _, err := config.ResolveProviderProtocol(cfg.Provider, cfg.ProviderProtocol, cfg.Responses); err != nil {
		return sessioncontrol.RuntimeConfig{}, err
	}
	resolved.Provider, resolved.Model = cfg.SelectedProvider, base.model
	if resolved.Model == "" {
		resolved.Model = config.ResolveModel(base.cwd, "")
	}
	if resolved.PermissionMode == "" {
		resolved.PermissionMode = permissions.FromSettings(cfg.Settings.Permissions).DefaultMode
		if cfg.Settings.Permissions.Bypass {
			resolved.PermissionMode = permissions.ModeBypassPermissions
		}
	}
	if resolved.Effort == "" {
		resolved.Effort = firstNonEmptyString(base.effort, os.Getenv("NIUMA_EFFORT"), os.Getenv("CLAUDE_CODE_EFFORT"), cfg.Settings.Effort, defaults.Effort)
	}
	if resolved.PromptMode == "" {
		resolved.PromptMode = promptmode.Code.String()
	}
	return sessioncontrol.NormalizeRuntimeConfig(resolved)
}

func serverQueryAttachments(attachments []server.QueryAttachment) []query.Attachment {
	if len(attachments) == 0 {
		return nil
	}
	out := make([]query.Attachment, 0, len(attachments))
	for _, attachment := range attachments {
		out = append(out, query.Attachment{
			ID:         attachment.AttachmentID,
			Type:       attachment.Type,
			MediaType:  attachment.MediaType,
			Name:       attachment.Name,
			URL:        attachment.URL,
			SizeBytes:  attachment.SizeBytes,
			SHA256:     attachment.SHA256,
			InlineData: attachment.InlineData,
		})
	}
	return out
}

// serverModelCatalog is the discovery contract exposed to WebUI clients. The
// project config remains authoritative for runtime execution, but the picker
// must also expose models configured globally so a project-local default (such
// as the built-in Claude config) cannot hide a user's global provider models.
func serverModelCatalog(workspace string) []string {
	return mergeModelLists(config.AvailableModels(""), config.AvailableModels(workspace))
}

func serverProviderCatalog(workspace string) []config.ProviderOption {
	result := make([]config.ProviderOption, 0)
	seen := make(map[string]bool)
	for _, options := range [][]config.ProviderOption{config.ConfiguredProviders(""), config.ConfiguredProviders(workspace)} {
		for _, option := range options {
			name := strings.TrimSpace(option.Name)
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			result = append(result, config.ProviderOption{Name: name, Model: strings.TrimSpace(option.Model)})
		}
	}
	return result
}

func mergeModelLists(lists ...[]string) []string {
	result := make([]string, 0)
	seen := make(map[string]bool)
	for _, list := range lists {
		for _, model := range list {
			model = strings.TrimSpace(model)
			if model == "" || seen[model] {
				continue
			}
			seen[model] = true
			result = append(result, model)
		}
	}
	return result
}

// pingRedisAtStartup 让「配了 Redis 但连不上」在启动时就报错。
//
// go-redis 的 NewClient 是懒连接，配错地址照样构造成功，于是此前要等到第一条真实
// 请求才会暴露 —— 而那时配额路径会走 fail-open/fail-closed 分支，两种都不是运维
// 想要的结果。MySQL 侧本来就在 OpenGormRepository 里 Ping 过，这里把 Redis 拉齐
// （AUDIT-P1-22）。
func pingRedisAtStartup(ctx context.Context, label, addr string, ping func(context.Context) error) error {
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ping(pingCtx); err != nil {
		return fmt.Errorf("connect %s redis at %s: %w", label, addr, err)
	}
	return nil
}

// serverAllowedCWDRootsEnv 覆盖服务端允许的工具执行根目录，多个用系统路径分隔符隔开。
const serverAllowedCWDRootsEnv = "NIUMA_SERVER_ALLOWED_CWD_ROOTS"

// resolveAllowedCWDRoots 计算请求体 cwd 的允许根目录。
//
// 默认只有 workspace 一个根：server 的 workspace 就是它被启动时所在的项目，
// 「让远端调用者指定任意目录」从来不是需求，而是 AUDIT-P1-27 记的攻击面。
// 需要多根（比如一台机器托管多个仓库）时用环境变量显式列出。
func resolveAllowedCWDRoots(workspace string) ([]string, error) {
	raw := strings.TrimSpace(os.Getenv(serverAllowedCWDRootsEnv))
	candidates := []string{workspace}
	if raw != "" {
		candidates = strings.Split(raw, string(os.PathListSeparator))
	}
	var roots []string
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		abs, err := filepath.Abs(candidate)
		if err != nil {
			return nil, fmt.Errorf("resolve allowed cwd root %q: %w", candidate, err)
		}
		roots = append(roots, canonicalPath(abs))
	}
	return roots, nil
}

func newServerRequestCWDValidator(workspace string, desktopLocal bool) (func(string) (string, error), error) {
	if desktopLocal {
		return func(requested string) (string, error) {
			return server.ValidateWorkspaceCWDPath(requested)
		}, nil
	}
	allowedCWDRoots, err := resolveAllowedCWDRoots(workspace)
	if err != nil {
		return nil, err
	}
	return func(requested string) (string, error) {
		return resolveRequestCWD(requested, allowedCWDRoots)
	}, nil
}

// resolveRequestCWD 校验请求指定的 cwd 落在允许根目录之内，返回规范化后的路径。
func resolveRequestCWD(requested string, roots []string) (string, error) {
	if len(roots) == 0 {
		// workspace 为空且没配环境变量：无从约束，保持原行为而不是假装拦住了。
		return requested, nil
	}
	abs, err := filepath.Abs(strings.TrimSpace(requested))
	if err != nil {
		return "", fmt.Errorf("cwd %q is not a valid path: %w", requested, err)
	}
	resolved := canonicalPath(abs)
	for _, root := range roots {
		if pathWithinRoot(resolved, root) {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("cwd %q is outside the allowed workspace roots %v; set %s to widen them",
		requested, roots, serverAllowedCWDRootsEnv)
}

// canonicalPath 先解符号链接再 Clean，否则 workspace 内一个指向 /etc 的软链就能
// 把整棵允许子树撑开。路径还不存在时退回 Clean 后的绝对路径。
func canonicalPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(path)
}

func pathWithinRoot(path, root string) bool {
	if path == root {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func defaultServerWorkspace(cwd string) string {
	workspace := strings.TrimSpace(cwd)
	if workspace != "" {
		return workspace
	}
	if current, err := os.Getwd(); err == nil {
		return current
	}
	return ""
}

func serverDefaultPromptMode() promptmode.Mode {
	return promptmode.Parse(firstEnv("NIUMA_SERVER_DEFAULT_PROMPT_MODE", "CLAUDE_CODE_SERVER_DEFAULT_PROMPT_MODE"), promptmode.Code)
}

func serverMobileAuthMode(opts server.Options) string {
	switch {
	case strings.TrimSpace(opts.MobileJWTSecret) != "":
		return "jwt"
	case opts.MobileDevAuth:
		return "dev-auth"
	default:
		return "disabled"
	}
}

func serverResponseFormatToAnthropic(format *server.OpenAIResponseFormat) *anthropic.ResponseFormat {
	if format == nil {
		return nil
	}
	out := &anthropic.ResponseFormat{Type: format.Type}
	if format.JSONSchema != nil {
		out.JSONSchema = &anthropic.ResponseFormatSchema{
			Name:        format.JSONSchema.Name,
			Description: format.JSONSchema.Description,
			Schema:      append(json.RawMessage(nil), format.JSONSchema.Schema...),
			Strict:      format.JSONSchema.Strict,
		}
	}
	return out
}

type serverTenantRuntime struct {
	enabled   bool
	tenantID  uint64
	userID    uint64
	sessionID uint64
	traceID   string
}

func prepareServerTenantRuntime(ctx context.Context, svc server.TenantService, req server.QueryRequest, cwd string) (serverTenantRuntime, error) {
	runtime := serverTenantRuntime{traceID: observability.TraceID(ctx)}
	if svc == nil || !serverTenantPersistenceRequested(ctx) {
		return runtime, nil
	}
	resolved, err := svc.ResolveContext(ctx)
	if err != nil {
		return runtime, err
	}
	runtime.enabled = true
	runtime.tenantID = resolved.TenantID
	runtime.userID = resolved.UserID
	if req.TenantSessionID != 0 {
		runtime.sessionID = req.TenantSessionID
		return runtime, nil
	}
	sessionKey := strings.TrimSpace(req.SessionKey)
	if sessionKey == "" {
		sessionKey = runtime.traceID
	}
	if sessionKey == "" {
		sessionKey = "query"
	}
	sessionID, err := svc.UpsertSession(ctx, tenantservice.SessionRequest{
		SessionKey: sessionKey,
		Title:      titleFromPromptForCLI(req.Prompt),
		Status:     "active",
		Model:      strings.TrimSpace(req.Model),
		CWD:        cwd,
	})
	if err != nil {
		return runtime, err
	}
	runtime.sessionID = sessionID
	return runtime, nil
}

func serverTenantPersistenceRequested(ctx context.Context) bool {
	return observability.TenantKey(ctx) != observability.DefaultTenantKey && observability.UserID(ctx) != observability.DefaultUserID
}

func titleFromPromptForCLI(prompt string) string {
	title := strings.Join(strings.Fields(prompt), " ")
	if len([]rune(title)) <= 80 {
		return title
	}
	return string([]rune(title)[:77]) + "..."
}

type tenantMigrationRunner interface {
	Up() error
	Down(steps int) error
	DownAll() error
	Version() (mysqlstore.MigrationVersion, error)
	Close() error
}

var newTenantMigrationRunner = func(opts mysqlstore.MigrationOptions) (tenantMigrationRunner, error) {
	return mysqlstore.NewMigrator(opts)
}
