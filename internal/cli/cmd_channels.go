package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/konglong87/super-niuma/internal/agentteam"
	"github.com/konglong87/super-niuma/internal/anthropic"
	channelcontract "github.com/konglong87/super-niuma/internal/channel"
	"github.com/konglong87/super-niuma/internal/channel/feishu"
	"github.com/konglong87/super-niuma/internal/channel/onboarding"
	channelruntime "github.com/konglong87/super-niuma/internal/channel/runtime"
	"github.com/konglong87/super-niuma/internal/imagegen"
	"github.com/konglong87/super-niuma/internal/media"
	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/query"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	tenantservice "github.com/konglong87/super-niuma/internal/tenant"
	"github.com/konglong87/super-niuma/internal/tools"
)

func channelsCommand(ctx context.Context, args []string, opts options, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("channels requires onboard feishu, authorize feishu, or run")
	}
	switch args[0] {
	case "onboard":
		if len(args) < 2 || args[1] != "feishu" {
			return errors.New("usage: channels onboard feishu")
		}
		return onboardFeishu(ctx, args[2:], stdout)
	case "run":
		if len(args) != 1 {
			return errors.New("usage: channels run")
		}
		return runChannelsWorker(ctx, opts, stdout)
	case "authorize":
		if len(args) < 2 || args[1] != "feishu" {
			return errors.New("usage: channels authorize feishu [--scope <scope>]")
		}
		return authorizeFeishu(ctx, args[2:], stdout)
	default:
		return fmt.Errorf("unknown channels command: %s", args[0])
	}
}

func authorizeFeishu(ctx context.Context, args []string, stdout io.Writer) error {
	scopes, err := parseFeishuAuthorizationScopes(args)
	if err != nil {
		return err
	}
	path := channelCredentialPath()
	store := onboarding.FileCredentialsStore{Path: path}
	credentials, err := store.LoadFeishuCredentials()
	if err != nil {
		return err
	}
	result, err := onboarding.AuthorizeFeishuAppScopes(ctx, onboarding.ScopeAuthorizationOptions{
		AppID:  credentials.AppID,
		Scopes: scopes,
		OnVerificationURL: func(info onboarding.VerificationURL) error {
			_, writeErr := fmt.Fprintf(stdout, "请在浏览器确认飞书应用增量权限：%s\n", info.URL)
			return writeErr
		},
		CredentialsStore: store,
	})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "飞书应用权限已更新：AppID=%s，scope=%s\n", result.AppID, strings.Join(scopes, ","))
	return err
}

func parseFeishuAuthorizationScopes(args []string) ([]string, error) {
	if len(args) == 0 {
		return []string{onboarding.FeishuScopeMessage, onboarding.FeishuScopeMessageReactionsWriteOnly, onboarding.FeishuScopeResource}, nil
	}
	scopes := make([]string, 0, len(args)/2)
	for index := 0; index < len(args); index++ {
		if args[index] != "--scope" || index+1 >= len(args) {
			return nil, errors.New("usage: channels authorize feishu [--scope <scope>]")
		}
		index++
		scope := strings.TrimSpace(args[index])
		if scope == "" {
			return nil, errors.New("--scope requires a non-empty value")
		}
		scopes = append(scopes, scope)
	}
	return scopes, nil
}

func onboardFeishu(ctx context.Context, args []string, stdout io.Writer) error {
	name, description := "niuma", "niuma Feishu channel bot"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--name":
			if i+1 >= len(args) {
				return errors.New("--name requires a value")
			}
			i++
			name = args[i]
		case "--description":
			if i+1 >= len(args) {
				return errors.New("--description requires a value")
			}
			i++
			description = args[i]
		default:
			return fmt.Errorf("unknown channels onboard option: %s", args[i])
		}
	}
	path := channelCredentialPath()
	result, err := onboarding.OneClickFeishuApp(ctx, onboarding.Options{AppName: name, AppDescription: description, OnVerificationURL: func(url onboarding.VerificationURL) error {
		_, err := fmt.Fprintf(stdout, "请在浏览器打开并授权飞书应用：%s\n", url.URL)
		return err
	}, CredentialsStore: onboarding.FileCredentialsStore{Path: path}})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "飞书应用已创建：AppID=%s\n凭据已保存到 %s（权限 0600）。请在飞书开发者后台发布应用后运行 channels run。\n", result.AppID, path)
	return err
}

func runChannelsWorker(ctx context.Context, opts options, stdout io.Writer) error {
	sqlitePath := strings.TrimSpace(firstEnv("NIUMA_SQLITE_PATH"))
	dsn := firstEnv("NIUMA_MYSQL_DSN", "MYSQL_DSN")
	if sqlitePath == "" && dsn == "" {
		return errors.New("channels run requires NIUMA_SQLITE_PATH or NIUMA_MYSQL_DSN")
	}
	tenantID, err := requiredUintEnv("NIUMA_CHANNEL_TENANT_ID")
	if err != nil {
		return err
	}
	accountID, err := requiredUintEnv("NIUMA_CHANNEL_ACCOUNT_ID")
	if err != nil {
		return err
	}
	userID, err := requiredUintEnv("NIUMA_CHANNEL_USER_ID")
	if err != nil {
		return err
	}
	accountKey := firstEnv("NIUMA_CHANNEL_ACCOUNT_KEY", "FEISHU_ACCOUNT_KEY")
	if accountKey == "" {
		return errors.New("channels run requires NIUMA_CHANNEL_ACCOUNT_KEY")
	}
	creds, err := (onboarding.FileCredentialsStore{Path: channelCredentialPath()}).LoadFeishuCredentials()
	if err != nil {
		return err
	}
	var repo *mysqlstore.GormRepository
	if sqlitePath != "" {
		repo, err = mysqlstore.OpenSQLiteGormRepository(ctx, sqlitePath, nil)
	} else {
		repo, err = mysqlstore.OpenGormRepository(ctx, dsn, nil)
	}
	if err != nil {
		return err
	}
	defer repo.Close()
	tenants, err := repo.ListTenants(ctx, 500)
	if err != nil {
		return fmt.Errorf("channels run tenant lookup failed: %w", err)
	}
	var tenantRecord mysqlstore.Tenant
	for _, candidate := range tenants {
		if candidate.ID == tenantID {
			tenantRecord = candidate
			break
		}
	}
	if tenantRecord.ID != tenantID {
		return fmt.Errorf("channels run tenant identity mismatch: tenant_id=%d", tenantID)
	}
	userRecord, err := repo.GetUser(ctx, tenantID, userID)
	if err != nil {
		return fmt.Errorf("channels run user identity failed: %w", err)
	}
	codec, err := channelPayloadCodec()
	if err != nil {
		return err
	}
	baseOpts := opts
	if provider := firstEnv("NIUMA_CHANNEL_MODEL_PROVIDER", "NIUMA_PROVIDER"); provider != "" {
		baseOpts.providerName = provider
	}
	if model := firstEnv("NIUMA_CHANNEL_MODEL", "CLAUDE_CODE_MODEL"); model != "" {
		baseOpts.model = model
	}
	imageRuntime, imageErr := configureChannelImageGeneration(baseOpts.cwd, repo, accountKey)
	if imageErr != nil {
		return fmt.Errorf("channels run image provider preflight failed: %w", imageErr)
	}
	imageGenerator, imageBlobStore, imageMediaStore := imageRuntime.Generator, imageRuntime.BlobStore, imageRuntime.MediaStore
	baseOpts.imageScheduler = imageRuntime.Scheduler
	baseOpts.asyncChannelImages = imageRuntime.Async
	observability.Info(ctx, nil, "channel.image.async_eligibility", "cli.runChannelsWorker", "channel image execution mode resolved",
		"tenant_id", tenantID, "account_id", accountID, "async_enabled", imageRuntime.Async)
	resolveAttachment := func(ctx context.Context, attachment channelcontract.Attachment) ([]byte, error) {
		if attachment.TenantID != tenantID {
			return nil, errors.New("image attachment tenant does not match worker")
		}
		return resolveChannelImageAttachment(ctx, imageMediaStore, imageBlobStore, attachment)
	}
	adapter := feishu.New(feishu.Config{AccountID: accountKey, AppID: creds.AppID, AppSecret: creds.AppSecret, ResolveAttachment: resolveAttachment})
	runtimeConfig, err := resolveRuntimeProviderConfig(&baseOpts)
	if err != nil {
		return fmt.Errorf("channels run provider preflight failed: %w", err)
	}
	effectiveProvider := strings.TrimSpace(runtimeConfig.SelectedProvider)
	if effectiveProvider == "" {
		effectiveProvider = strings.TrimSpace(runtimeConfig.Provider)
	}
	effectiveModel := strings.TrimSpace(baseOpts.model)
	if effectiveModel == "" {
		effectiveModel = strings.TrimSpace(runtimeConfig.SelectedProviderModel)
	}
	if _, err := fmt.Fprintf(stdout, "provider preflight: provider=%s model=%s settings=%s\n", effectiveProvider, effectiveModel, strings.Join(runtimeConfig.Sources, ",")); err != nil {
		return err
	}
	adminOpenIDs := splitChannelList(firstEnv("NIUMA_CHANNEL_ADMIN_OPEN_IDS", "CHANNEL_ADMIN_OPEN_IDS"))
	permissionBroker := channelruntime.NewPermissionBroker(channelruntime.PermissionBrokerConfig{TenantID: tenantID, AccountID: accountID, RuntimeFingerprint: channelruntime.DefaultFingerprint, RuntimeFingerprintVersion: channelruntime.DefaultRuntimeVersion, Repo: repo, Sender: adapter, AdminOpenIDs: adminOpenIDs})
	questionBroker := channelruntime.NewQuestionBroker(channelruntime.QuestionBrokerConfig{TenantID: tenantID, AccountID: accountID, RuntimeFingerprint: channelruntime.DefaultFingerprint, RuntimeFingerprintVersion: channelruntime.DefaultRuntimeVersion, Repo: repo, PayloadCodec: codec})
	questionsEnabled, err := channelQuestionsEnabled()
	if err != nil {
		return err
	}
	if !questionsEnabled {
		questionBroker = nil
	}
	runner := cliChannelRunner{opts: baseOpts, repo: repo, permissionBroker: permissionBroker, imageGenerator: imageGenerator, imageScheduler: baseOpts.imageScheduler, asyncChannelImages: baseOpts.asyncChannelImages}
	teamService := tenantservice.NewService(repo, nil)
	global, err := channelcontract.ParseStreamingGlobalMode(channelStreamingGlobalSetting())
	if err != nil {
		return err
	}
	accountMode, err := channelcontract.ParseStreamingAccountMode(channelStreamingAccountSetting())
	if err != nil {
		return err
	}
	toolDetailsMode, err := channelcontract.ParseToolDetailsMode(channelToolDetailsSetting())
	if err != nil {
		return err
	}
	decision := channelcontract.EvaluateStreamingCard(global, accountMode, adapter.Capabilities().SupportsStreamingCard())
	workspaceRoots := splitChannelList(firstEnv("NIUMA_CHANNEL_WORKSPACE_ROOTS", "CHANNEL_WORKSPACE_ROOTS"))
	if len(workspaceRoots) == 0 && strings.TrimSpace(baseOpts.cwd) != "" {
		workspaceRoots = []string{baseOpts.cwd}
	}
	resolvedWorkspace, err := channelcontract.ResolveWorkspace(baseOpts.cwd, workspaceRoots)
	if err != nil {
		return fmt.Errorf("channels run workspace validation failed: %w", err)
	}
	baseOpts.cwd = resolvedWorkspace
	baseOpts.imageGenerator = imageGenerator
	workspaceRoots = normalizeChannelWorkspaceRoots(workspaceRoots)
	permissionMode := firstEnv("NIUMA_CHANNEL_PERMISSION_MODE", "CHANNEL_PERMISSION_MODE")
	if permissionMode == "" {
		permissionMode = channelruntime.PermissionModeAsk
	}
	if permissionMode != channelruntime.PermissionModeAsk && permissionMode != channelruntime.PermissionModeAllow && permissionMode != channelruntime.PermissionModeDeny {
		return fmt.Errorf("invalid channel permission mode %q; use ask, allow, or deny", permissionMode)
	}
	commandRouter := channelcontract.NewChannelCommandRouter(adminOpenIDs)
	var reactions *channelruntime.ReactionReconciler
	if channelEnvEnabled(firstEnv("NIUMA_CHANNEL_REACTIONS", "CHANNEL_REACTIONS")) && adapter.Capabilities().Reactions {
		reactions = channelruntime.NewReactionReconciler(channelruntime.ReactionConfig{TenantID: tenantID, AccountID: accountID, WorkerID: channelruntime.DefaultWorkerID, Repo: repo, Adapter: adapter})
	}
	teamRuntime, teamRouter, err := newChannelTeamRuntime(ctx, repo, teamService, adapter, baseOpts, tenantID, accountID, userID, tenantRecord.TenantKey, userRecord.UserKey, accountKey, baseOpts.cwd, permissionMode, baseOpts.model)
	if err != nil {
		_, _ = fmt.Fprintf(stdout, "warning: Team runtime disabled: %v\n", err)
		teamRuntime = nil
		teamRouter = agentteam.NewRouter(nil, nil)
	}
	svc := channelruntime.New(channelruntime.Config{TenantID: tenantID, AccountID: accountID, AccountKey: accountKey, Repo: repo, Adapter: adapter, Runner: runner, TenantStore: repo, PayloadCodec: codec, Policy: channelruntime.Policy{AllowDM: true, AllowGroups: true, RequireMentionInGroups: true}, UserResolver: func(context.Context, channelcontract.InboundMessage) (uint64, error) { return userID, nil }, SessionResolver: func(ctx context.Context, tenant, user uint64, scope channelcontract.Scope) (uint64, error) {
		conversation, lookupErr := repo.GetChannelConversationByScope(ctx, tenant, accountID, scope.Hash[:])
		if lookupErr == nil && conversation.SessionID != 0 {
			return conversation.SessionID, nil
		}
		if lookupErr != nil && !errors.Is(lookupErr, mysqlstore.ErrNotFound) {
			return 0, lookupErr
		}
		return repo.UpsertSession(ctx, mysqlstore.SessionInput{TenantID: tenant, UserID: user, SessionKey: "channel:" + scope.HashHex(), Title: "Feishu " + scope.ChatID, Status: "active", Model: baseOpts.model, CWD: baseOpts.cwd})
	}, Streaming: decision, CommandRouter: commandRouter, WorkspaceRoots: workspaceRoots, Model: baseOpts.model, DefaultWorkspace: baseOpts.cwd, DefaultPermissionMode: permissionMode, Reactions: reactions, PermissionBroker: permissionBroker, QuestionBroker: questionBroker, TeamRouter: &teamRouter, TeamDispatch: teamDispatchFunc(teamRuntime), ImageGenerator: imageGenerator, ImageScheduler: baseOpts.imageScheduler, AsyncChannelImages: baseOpts.asyncChannelImages, RenderFinalCard: func(state channelcontract.CardState) ([]byte, error) {
		return feishu.RenderFinalCard(state, feishu.CardOptions{ToolDetailsMode: toolDetailsMode})
	}, RenderStreamingCard: func(state channelcontract.CardState) ([]byte, error) {
		return feishu.RenderStreamingCard(state, feishu.CardOptions{ToolDetailsMode: toolDetailsMode})
	}, RenderTimelineCards: channelTimelineCardRenderer(toolDetailsMode)})
	workerCfg := channelruntime.WorkerConfig{Service: svc}
	if redisAddr := firstEnv("NIUMA_CHANNEL_REDIS_ADDR", "CHANNEL_REDIS_ADDR"); redisAddr != "" {
		workerCfg.Lease = channelruntime.NewRedisLeaseFromAddr(redisAddr, firstEnv("NIUMA_CHANNEL_REDIS_PASSWORD", "CHANNEL_REDIS_PASSWORD"), 0, firstEnv("NIUMA_CHANNEL_REDIS_PREFIX", "CHANNEL_REDIS_PREFIX"))
	} else {
		_, _ = fmt.Fprintln(stdout, "warning: channel Redis lease is not configured; using single-instance local lease")
	}
	worker := channelruntime.NewWorker(workerCfg)
	if _, err := fmt.Fprintf(stdout, "Feishu channel worker starting (streaming=%v, reason=%s, tool_details=%s, questions=%v)\n", decision.Enabled, decision.Reason, toolDetailsMode, questionsEnabled); err != nil {
		return err
	}
	return worker.Run(ctx)
}

func channelStreamingGlobalSetting() string {
	return firstEnv("NIUMA_CHANNEL_STREAMING_CARD_MODE", "NIUMA_CHANNEL_STREAMING", "NIUMA_CHANNEL_STREAMING_CARD_MODE", "NIUMA_CHANNEL_STREAMING", "CHANNEL_STREAMING_CARD_MODE", "CHANNEL_STREAMING")
}

func channelStreamingAccountSetting() string {
	return firstEnv("NIUMA_CHANNEL_STREAMING_ACCOUNT_MODE", "NIUMA_CHANNEL_STREAMING_ACCOUNT", "NIUMA_CHANNEL_STREAMING_ACCOUNT_MODE", "NIUMA_CHANNEL_STREAMING_ACCOUNT", "CHANNEL_STREAMING_ACCOUNT_MODE", "CHANNEL_STREAMING_ACCOUNT")
}

func channelToolDetailsSetting() string {
	return firstEnv("NIUMA_CHANNEL_TOOL_DETAILS", "CHANNEL_TOOL_DETAILS")
}

func channelTimelineCardRenderer(mode channelcontract.ToolDetailsMode) func(channelcontract.CardState) ([]channelcontract.RenderedCardPage, error) {
	return func(state channelcontract.CardState) ([]channelcontract.RenderedCardPage, error) {
		return feishu.RenderTimelineCards(state, feishu.CardOptions{ToolDetailsMode: mode})
	}
}

func channelQuestionsEnabled() (bool, error) {
	value := strings.TrimSpace(firstEnv("NIUMA_CHANNEL_QUESTIONS", "CHANNEL_QUESTIONS"))
	if value == "" {
		return true, nil
	}
	switch strings.ToLower(value) {
	case "1", "true", "yes", "on", "enabled":
		return true, nil
	case "0", "false", "no", "off", "disabled":
		return false, nil
	default:
		return false, fmt.Errorf("invalid channel questions mode %q; use on or off", value)
	}
}

type cliChannelRunner struct {
	opts               options
	repo               *mysqlstore.GormRepository
	permissionBroker   *channelruntime.PermissionBroker
	imageGenerator     imagegen.Generator
	imageScheduler     imagegen.Scheduler
	asyncChannelImages bool
}

func (r cliChannelRunner) optionsForRun(input channelcontract.RunInput) options {
	opts := r.opts
	opts.tenantID = input.TenantID
	opts.tenantUserID = input.UserID
	opts.tenantSessionID = input.TenantSessionID
	if r.imageGenerator != nil {
		opts.imageGenerator = r.imageGenerator
	}
	if r.imageScheduler != nil {
		opts.imageScheduler = r.imageScheduler
	}
	if r.asyncChannelImages {
		opts.asyncChannelImages = true
	}
	opts.runID = input.RunID
	opts.prompt = input.Prompt
	if r.asyncChannelImages {
		opts.imageOriginFactory = func(tools.Context) (imagegen.OriginMetadata, error) {
			return channelruntime.NewChannelImageOriginMetadata(channelruntime.ChannelImageOriginInput{
				TenantID: input.TenantID, AccountID: input.AccountID, ConversationID: input.ConversationID,
				RunID: input.RunID, SessionID: input.TenantSessionID, UserID: input.UserID,
				ReplyMessageID: input.ReplyToMessageID, ThreadID: input.Scope.ThreadID,
			})
		}
	}
	applyChannelRunControls(&opts, input)
	applyChannelPermissionPrompt(&opts, input, r.permissionBroker)
	applyChannelQuestionPrompt(&opts)
	return opts
}

type channelToolCollector struct {
	tools        []channelcontract.ToolProgress
	byID         map[string]int
	timeline     []channelcontract.TimelineEntry
	timelineByID map[string]int
}

func newChannelToolCollector() *channelToolCollector {
	return &channelToolCollector{byID: make(map[string]int), timelineByID: make(map[string]int)}
}

func (c *channelToolCollector) Write(data []byte) (int, error) {
	if c == nil || len(data) == 0 {
		return len(data), nil
	}
	text := string(data)
	if len(c.timeline) > 0 && c.timeline[len(c.timeline)-1].Kind == channelcontract.TimelineText {
		c.timeline[len(c.timeline)-1].Text += text
	} else {
		c.timeline = append(c.timeline, channelcontract.TimelineEntry{Kind: channelcontract.TimelineText, Text: text})
	}
	return len(data), nil
}

func (c *channelToolCollector) onCall(event query.ToolCallEvent) {
	if c == nil || event.Name == "" {
		return
	}
	id := event.ID
	if id == "" {
		id = fmt.Sprintf("%s:%d", event.Name, len(c.tools)+1)
	}
	tool := channelcontract.ToolProgress{ID: id, Name: event.Name, Status: channelcontract.ToolStatusRunning, Command: channelcontract.ToolCommandPreview(event.Name, event.Input)}
	c.byID[id] = len(c.tools)
	c.tools = append(c.tools, tool)
	c.timelineByID[id] = len(c.timeline)
	c.timeline = append(c.timeline, channelcontract.TimelineEntry{ID: "tool:" + id, Kind: channelcontract.TimelineTool, Tool: tool})
}

func (c *channelToolCollector) onResult(trace query.ToolTrace) {
	if c == nil || trace.Name == "" {
		return
	}
	id := trace.ID
	if id == "" {
		id = fmt.Sprintf("%s:%d", trace.Name, len(c.tools)+1)
	}
	if index, ok := c.byID[id]; ok {
		output, truncated := channelcontract.ToolOutputPreview(trace.Output, trace.IsError)
		tool := &c.tools[index]
		tool.Status = channelToolStatus(trace.IsError)
		tool.Command = firstNonEmptyChannel(tool.Command, channelcontract.ToolCommandPreview(trace.Name, []byte(trace.Input)))
		tool.OutputPreview = output
		tool.IsError = trace.IsError
		tool.OutputTruncated = truncated
		if timelineIndex, exists := c.timelineByID[id]; exists {
			c.timeline[timelineIndex].Tool = *tool
		}
		return
	}
	c.onCall(query.ToolCallEvent{ID: id, Name: trace.Name, Input: []byte(trace.Input)})
	c.onResult(trace)
}

func (c *channelToolCollector) snapshot() []channelcontract.ToolProgress {
	if c == nil {
		return nil
	}
	return append([]channelcontract.ToolProgress(nil), c.tools...)
}

func (c *channelToolCollector) timelineSnapshot() []channelcontract.TimelineEntry {
	if c == nil {
		return nil
	}
	return append([]channelcontract.TimelineEntry(nil), c.timeline...)
}

func channelToolStatus(isError bool) string {
	if isError {
		return channelcontract.ToolStatusFailed
	}
	return channelcontract.ToolStatusComplete
}

func firstNonEmptyChannel(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (r cliChannelRunner) Run(ctx context.Context, input channelcontract.RunInput) (channelcontract.RunResult, error) {
	opts := r.optionsForRun(input)
	initial := []anthropic.MessageParam{}
	if r.repo != nil {
		messages, err := r.repo.ListMessages(ctx, input.TenantID, input.UserID, input.TenantSessionID, 200)
		if err == nil {
			for _, m := range messages {
				if m.Content != "" {
					initial = append(initial, anthropic.MessageParam{Role: m.Role, Content: []anthropic.ContentBlock{{Type: "text", Text: m.Content}}})
				}
			}
		}
	}
	session, cleanup, err := newQuerySession(ctx, opts, initial, nil)
	if err != nil {
		return channelcontract.RunResult{}, err
	}
	defer cleanup()
	collector := newChannelToolCollector()
	callbacks := query.RunCallbacks{
		OnToolCall:   func(event query.ToolCallEvent) error { collector.onCall(event); return nil },
		OnToolResult: func(trace query.ToolTrace) error { collector.onResult(trace); return nil },
	}
	var result query.Result
	if input.Resume != nil {
		result, err = session.RunWithResume(ctx, query.ResumeInput{AssistantMessage: input.Resume.AssistantMessage, ToolResult: input.Resume.ToolResult}, collector, callbacks)
	} else {
		result, err = session.RunWithCallbacks(ctx, input.Prompt, collector, callbacks)
	}
	if r.asyncChannelImages {
		auditChannelImageToolReceipts(ctx, r.repo, input, result.ToolCalls)
	}
	if err != nil {
		return attachChannelTimeline(channelRunResultOnError(result, collector.snapshot(), err, r.asyncChannelImages), collector.timelineSnapshot()), err
	}
	return attachChannelTimeline(channelRunResultWithAsyncImages(result, collector.snapshot(), r.asyncChannelImages), collector.timelineSnapshot()), nil
}

func attachChannelTimeline(result channelcontract.RunResult, timeline []channelcontract.TimelineEntry) channelcontract.RunResult {
	var text strings.Builder
	for _, entry := range timeline {
		if entry.Kind == channelcontract.TimelineText {
			text.WriteString(entry.Text)
		}
	}
	if text.String() == result.FinalText {
		result.Timeline = append([]channelcontract.TimelineEntry(nil), timeline...)
		return result
	}
	safe := make([]channelcontract.TimelineEntry, 0, len(timeline)+1)
	for _, entry := range timeline {
		if entry.Kind != channelcontract.TimelineText {
			safe = append(safe, entry)
		}
	}
	if result.FinalText != "" {
		safe = append(safe, channelcontract.TimelineEntry{Kind: channelcontract.TimelineText, Text: result.FinalText})
	}
	result.Timeline = safe
	return result
}
func (r cliChannelRunner) RunStream(ctx context.Context, input channelcontract.RunInput, sink channelcontract.DeltaSink) (channelcontract.RunResult, error) {
	opts := r.optionsForRun(input)
	initial := []anthropic.MessageParam{}
	if r.repo != nil {
		messages, err := r.repo.ListMessages(ctx, input.TenantID, input.UserID, input.TenantSessionID, 200)
		if err == nil {
			for _, message := range messages {
				if message.Content != "" {
					initial = append(initial, anthropic.MessageParam{Role: message.Role, Content: []anthropic.ContentBlock{{Type: "text", Text: message.Content}}})
				}
			}
		}
	}
	session, cleanup, err := newQuerySession(ctx, opts, initial, nil)
	if err != nil {
		return channelcontract.RunResult{}, err
	}
	defer cleanup()
	writer := streamDeltaWriter{ctx: ctx, sink: sink}
	callbacks := channelStreamCallbacks(ctx, sink)
	collector := newChannelToolCollector()
	callbacks.OnToolCall = func(event query.ToolCallEvent) error {
		collector.onCall(event)
		return sink.OnDelta(ctx, channelcontract.Delta{
			Kind:        channelcontract.DeltaTool,
			ToolID:      event.ID,
			ToolName:    event.Name,
			ToolStatus:  channelcontract.ToolStatusRunning,
			ToolCommand: channelcontract.ToolCommandPreview(event.Name, event.Input),
		})
	}
	callbacks.OnToolResult = func(trace query.ToolTrace) error {
		collector.onResult(trace)
		status := channelcontract.ToolStatusComplete
		if trace.IsError {
			status = channelcontract.ToolStatusFailed
		}
		output, truncated := channelcontract.ToolOutputPreview(trace.Output, trace.IsError)
		return sink.OnDelta(ctx, channelcontract.Delta{
			Kind:          channelcontract.DeltaTool,
			ToolID:        trace.ID,
			ToolName:      trace.Name,
			ToolStatus:    status,
			ToolCommand:   channelcontract.ToolCommandPreview(trace.Name, []byte(trace.Input)),
			ToolOutput:    output,
			ToolIsError:   trace.IsError,
			ToolTruncated: truncated,
		})
	}
	var result query.Result
	if input.Resume != nil {
		result, err = session.RunWithResume(ctx, query.ResumeInput{AssistantMessage: input.Resume.AssistantMessage, ToolResult: input.Resume.ToolResult}, &writer, callbacks)
	} else {
		result, err = session.RunWithCallbacks(ctx, input.Prompt, &writer, callbacks)
	}
	if r.asyncChannelImages {
		auditChannelImageToolReceipts(ctx, r.repo, input, result.ToolCalls)
	}
	if err != nil {
		return channelRunResultOnError(result, collector.snapshot(), err, r.asyncChannelImages), err
	}
	return channelRunResultWithAsyncImages(result, collector.snapshot(), r.asyncChannelImages), nil
}

func channelRunResult(result query.Result, tools []channelcontract.ToolProgress) channelcontract.RunResult {
	return channelRunResultWithAsyncImages(result, tools, false)
}

func channelRunResultWithAsyncImages(result query.Result, toolProgress []channelcontract.ToolProgress, asyncImages bool) channelcontract.RunResult {
	out := channelcontract.RunResult{FinalText: result.Response, TurnID: result.SessionID, Tools: toolProgress, Attachments: channelImageAttachments(result.ToolCalls)}
	if asyncImages {
		if acknowledgement, ok := channelImageJobAcknowledgement(result.ToolCalls); ok {
			out.FinalText = acknowledgement
		}
	}
	if result.PendingInteraction != nil {
		pending := result.PendingInteraction
		out.PendingInteraction = &channelcontract.InteractionQuestion{ID: pending.ID, Kind: pending.Kind, Question: pending.Question, Choices: append([]string(nil), pending.Choices...), ToolUseID: pending.ToolUseID, ToolName: pending.ToolName, ToolInput: append([]byte(nil), pending.ToolInput...), AssistantMessage: pending.AssistantMessage}
	}
	return out
}

func channelRunResultOnError(result query.Result, toolProgress []channelcontract.ToolProgress, runErr error, asyncImages bool) channelcontract.RunResult {
	out := channelRunResultWithAsyncImages(result, toolProgress, asyncImages)
	if !safeChannelPartialText(out.FinalText, runErr) {
		out.FinalText = "执行失败，请稍后重试。"
	}
	return out
}

func safeChannelPartialText(text string, runErr error) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	errorText := ""
	if runErr != nil {
		errorText = strings.TrimSpace(runErr.Error())
	}
	return errorText == "" || !strings.Contains(text, errorText)
}

func channelImageJobAcknowledgement(traces []query.ToolTrace) (string, bool) {
	accepted := make([]string, 0)
	seen := make(map[string]struct{})
	rejected := 0
	imageCalls := 0
	for _, trace := range traces {
		if trace.Name != "GenerateImage" && trace.Name != "EditImage" {
			continue
		}
		imageCalls++
		if trace.IsError {
			rejected++
			continue
		}
		receipt, ok := decodeChannelImageReceipt(trace.Output)
		if !ok {
			rejected++
			continue
		}
		if _, exists := seen[receipt.GenerationID]; !exists {
			seen[receipt.GenerationID] = struct{}{}
			accepted = append(accepted, receipt.GenerationID)
		}
	}
	if imageCalls == 0 {
		return "", false
	}
	receipts := make([]imagegen.JobReceipt, 0, len(accepted))
	for _, generationID := range accepted {
		receipts = append(receipts, imagegen.JobReceipt{GenerationID: generationID})
	}
	return channelcontract.ImageJobAcknowledgement(receipts, rejected), true
}

type channelImageAuditRepository interface {
	InsertAuditLog(context.Context, mysqlstore.AuditLogInput) (uint64, error)
}

func auditChannelImageToolReceipts(ctx context.Context, repo channelImageAuditRepository, input channelcontract.RunInput, traces []query.ToolTrace) {
	if repo == nil {
		return
	}
	for _, trace := range traces {
		if trace.IsError || (trace.Name != "GenerateImage" && trace.Name != "EditImage") {
			continue
		}
		receipt, ok := decodeChannelImageReceipt(trace.Output)
		if !ok {
			continue
		}
		metadata, marshalErr := json.Marshal(map[string]any{
			"generation_id": receipt.GenerationID,
			"batch_id":      receipt.BatchID,
			"run_id":        input.RunID,
			"session_id":    input.TenantSessionID,
		})
		if marshalErr != nil {
			continue
		}
		_, auditErr := repo.InsertAuditLog(ctx, mysqlstore.AuditLogInput{
			TenantID: input.TenantID, ActorUserID: input.UserID, Action: channelruntime.ChannelImageAuditEnqueued,
			ResourceType: "image_generation", ResourceID: receipt.GenerationID, MetadataJSON: string(metadata), TraceID: observability.TraceID(ctx),
		})
		if auditErr != nil {
			observability.Error(ctx, nil, "channel.image.audit_failed", "cli.channel", "channel image audit write failed", "tenant_id", input.TenantID, "user_id", input.UserID, "session_id", input.TenantSessionID, "generation_id", receipt.GenerationID)
			continue
		}
		observability.Info(ctx, nil, channelruntime.ChannelImageAuditEnqueued, "cli.channel", "channel image job accepted", "tenant_id", input.TenantID, "user_id", input.UserID, "session_id", input.TenantSessionID, "generation_id", receipt.GenerationID, "batch_id", receipt.BatchID)
	}
}

func decodeChannelImageReceipt(raw string) (imagegen.JobReceipt, bool) {
	var receipt imagegen.JobReceipt
	if json.Unmarshal([]byte(strings.TrimSpace(raw)), &receipt) != nil || strings.TrimSpace(receipt.GenerationID) == "" || strings.TrimSpace(string(receipt.Status)) == "" || receipt.AcceptedAt.IsZero() {
		return imagegen.JobReceipt{}, false
	}
	return receipt, true
}

func channelImageAttachments(traces []query.ToolTrace) []channelcontract.Attachment {
	attachments := make([]channelcontract.Attachment, 0)
	for _, trace := range traces {
		if trace.IsError || (trace.Name != "GenerateImage" && trace.Name != "EditImage") {
			continue
		}
		artifact, ok := decodeChannelImageArtifact(trace.Output)
		if !ok {
			continue
		}
		attachments = append(attachments, channelcontract.Attachment{ID: artifact.AssetID, Type: "image", MediaType: artifact.MediaType, Name: artifact.Name, URL: artifact.URL, SizeBytes: artifact.SizeBytes, SHA256: artifact.SHA256, TenantID: artifact.TenantID, UserID: artifact.UserID, SessionID: artifact.SessionID})
	}
	return attachments
}

func decodeChannelImageArtifact(raw string) (imagegen.Artifact, bool) {
	var artifact imagegen.Artifact
	raw = strings.TrimSpace(raw)
	if json.Unmarshal([]byte(raw), &artifact) == nil && artifact.AssetID != "" {
		return artifact, artifact.TenantID != 0 && artifact.UserID != 0 && artifact.SessionID != 0
	}
	start, end := strings.IndexByte(raw, '{'), strings.LastIndexByte(raw, '}')
	if start < 0 || end <= start || json.Unmarshal([]byte(raw[start:end+1]), &artifact) != nil || artifact.AssetID == "" {
		return imagegen.Artifact{}, false
	}
	return artifact, artifact.TenantID != 0 && artifact.UserID != 0 && artifact.SessionID != 0
}

func resolveChannelImageAttachment(ctx context.Context, store media.Store, blobs imagegen.BlobStore, attachment channelcontract.Attachment) ([]byte, error) {
	if store == nil || blobs == nil {
		return nil, errors.New("image storage is not configured")
	}
	if attachment.TenantID == 0 || attachment.UserID == 0 || attachment.SessionID == 0 || strings.TrimSpace(attachment.ID) == "" {
		return nil, errors.New("image attachment scope is required")
	}
	asset, err := store.Get(ctx, media.AccessPolicy{TenantID: attachment.TenantID, UserID: attachment.UserID, SessionID: attachment.SessionID}, attachment.ID)
	if err != nil {
		return nil, err
	}
	key := strings.TrimSpace(asset.Original.Path)
	if key == "" {
		return nil, errors.New("image asset blob path is missing")
	}
	reader, err := blobs.Open(ctx, media.AccessPolicy{TenantID: attachment.TenantID, UserID: attachment.UserID, SessionID: attachment.SessionID}, key)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func applyChannelRunControls(opts *options, input channelcontract.RunInput) {
	if opts == nil {
		return
	}
	if strings.TrimSpace(input.WorkspaceRealpath) != "" {
		opts.cwd = input.WorkspaceRealpath
	}
	if strings.TrimSpace(input.PermissionMode) != "" {
		opts.permissionMode = input.PermissionMode
		opts.permissionBypass = strings.EqualFold(input.PermissionMode, channelruntime.PermissionModeAllow)
		opts.skipPermissions = false
	}
}

func applyChannelPermissionPrompt(opts *options, input channelcontract.RunInput, broker *channelruntime.PermissionBroker) {
	if opts == nil || broker == nil {
		return
	}
	opts.permissionPrompt = func(ctx context.Context, request tools.PermissionPromptRequest) tools.PermissionPromptResponse {
		decision := broker.Request(ctx, channelcontract.PermissionRequest{ConversationID: input.ConversationID, RunID: input.RunID, UserID: input.UserID, ExternalUserID: input.ExternalUserID, ExternalChatID: input.ExternalChatID, ReplyToMessageID: input.ReplyToMessageID, ChatType: input.ChatType, ToolName: request.ToolName, Reason: request.Reason, Request: request.Request})
		return tools.PermissionPromptResponse{Allowed: decision.Allowed, Destination: decision.Destination, Decision: decision.Decision, Reason: decision.Reason, Rule: request.Rule}
	}
}

func applyChannelQuestionPrompt(opts *options) {
	if opts == nil {
		return
	}
	opts.userQuestionPrompt = func(context.Context, tools.UserQuestionRequest) tools.UserQuestionResponse {
		return tools.UserQuestionResponse{Pending: true}
	}
}

func splitChannelList(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' || r == '\n' })
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if field = strings.TrimSpace(field); field != "" {
			out = append(out, field)
		}
	}
	return out
}

func channelEnvEnabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on", "enabled":
		return true
	default:
		return false
	}
}

func normalizeChannelWorkspaceRoots(roots []string) []string {
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		if resolved, err := channelcontract.ResolveWorkspace(root, []string{root}); err == nil {
			out = append(out, resolved)
		}
	}
	return out
}

type streamDeltaWriter struct {
	ctx  context.Context
	sink channelcontract.DeltaSink
}

// channelStreamCallbacks opts query.Session into live text delivery. Without
// OnTextAmended, the query layer buffers all text until the turn is accepted,
// which defeats the Feishu typewriter card even when the feature flag is on.
func channelStreamCallbacks(ctx context.Context, sink channelcontract.DeltaSink) query.RunCallbacks {
	return query.RunCallbacks{OnTextAmended: func(streamed, final string) error {
		return sink.OnDelta(ctx, channelcontract.Delta{Kind: channelcontract.DeltaTextAmend, PreviousText: streamed, Text: final})
	}}
}

func (w *streamDeltaWriter) Write(data []byte) (int, error) {
	if err := w.sink.OnDelta(w.ctx, channelcontract.Delta{Kind: channelcontract.DeltaText, Text: string(data)}); err != nil {
		return 0, err
	}
	return len(data), nil
}

func channelCredentialPath() string {
	if value := firstEnv("NIUMA_FEISHU_CREDENTIAL_FILE", "FEISHU_CREDENTIAL_FILE"); value != "" {
		return value
	}
	dir, _ := os.UserConfigDir()
	return filepath.Join(dir, "niuma", "feishu-credentials.json")
}
func requiredUintEnv(keys ...string) (uint64, error) {
	if len(keys) == 0 {
		return 0, errors.New("channels run requires an environment variable")
	}
	key := keys[0]
	value := firstEnv(keys...)
	if value == "" {
		return 0, fmt.Errorf("channels run requires %s", key)
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || parsed == 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return parsed, nil
}
func channelPayloadCodec() (channelruntime.PayloadCodec, error) {
	raw := firstEnv("NIUMA_CHANNEL_PAYLOAD_KEY", "CHANNEL_PAYLOAD_KEY")
	if raw == "" {
		sqlitePath := strings.TrimSpace(firstEnv("NIUMA_SQLITE_PATH"))
		tenantID := firstEnv("NIUMA_CHANNEL_TENANT_ID")
		accountID := firstEnv("NIUMA_CHANNEL_ACCOUNT_ID")
		if sqlitePath == "" || tenantID == "" || accountID == "" {
			return nil, errors.New("channels run requires NIUMA_CHANNEL_PAYLOAD_KEY (32-byte hex key)")
		}
		sum := sha256.Sum256([]byte(sqlitePath + "\x00" + tenantID + "\x00" + accountID))
		raw = hex.EncodeToString(sum[:])
	}
	key, err := hex.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("channel payload key must be hex: %w", err)
	}
	codec, err := channelruntime.NewAESGCMCodec(key, firstEnv("NIUMA_CHANNEL_PAYLOAD_KEY_VERSION", "CHANNEL_PAYLOAD_KEY_VERSION"))
	if err != nil {
		return nil, err
	}
	return codec, nil
}
