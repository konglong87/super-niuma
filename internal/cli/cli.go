package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/konglong87/super-niuma/internal/agenteval"
	"github.com/konglong87/super-niuma/internal/agents"
	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/background"
	"github.com/konglong87/super-niuma/internal/buildinfo"
	"github.com/konglong87/super-niuma/internal/compact"
	"github.com/konglong87/super-niuma/internal/computeruse"
	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/defaults"
	"github.com/konglong87/super-niuma/internal/gitutil"
	goalpkg "github.com/konglong87/super-niuma/internal/goal"
	"github.com/konglong87/super-niuma/internal/goalcmd"
	"github.com/konglong87/super-niuma/internal/hooks"
	"github.com/konglong87/super-niuma/internal/identity"
	imagegensvc "github.com/konglong87/super-niuma/internal/imagegen"
	"github.com/konglong87/super-niuma/internal/mcp"
	"github.com/konglong87/super-niuma/internal/media"
	"github.com/konglong87/super-niuma/internal/memory"
	"github.com/konglong87/super-niuma/internal/nextsteps"
	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/permissions"
	"github.com/konglong87/super-niuma/internal/plugins"
	"github.com/konglong87/super-niuma/internal/product"
	"github.com/konglong87/super-niuma/internal/promptmode"
	"github.com/konglong87/super-niuma/internal/query"
	"github.com/konglong87/super-niuma/internal/recap"
	"github.com/konglong87/super-niuma/internal/runtimeprofile"
	"github.com/konglong87/super-niuma/internal/scheduler"
	"github.com/konglong87/super-niuma/internal/server"
	"github.com/konglong87/super-niuma/internal/session"
	"github.com/konglong87/super-niuma/internal/sessioncontrol"
	"github.com/konglong87/super-niuma/internal/sessioncontrol/runtimecompose"
	"github.com/konglong87/super-niuma/internal/skills"
	"github.com/konglong87/super-niuma/internal/slashcommands"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	"github.com/konglong87/super-niuma/internal/telemetry"
	tenantservice "github.com/konglong87/super-niuma/internal/tenant"
	"github.com/konglong87/super-niuma/internal/tools"
	agenttool "github.com/konglong87/super-niuma/internal/tools/agent"
	sessioncontroltool "github.com/konglong87/super-niuma/internal/tools/sessioncontrol"
	"github.com/konglong87/super-niuma/internal/tools/task"
	"github.com/konglong87/super-niuma/internal/tui"
	"github.com/konglong87/super-niuma/internal/updater"
)

const (
	defaultTUIResumeHistoryLimit  = 6
	configKeyRecapAwayDelay       = "recap.awayDelaySeconds"
	configKeyTUIShowThinking      = "tui.showThinking"
	configKeyTUIThinkingMode      = "tui.thinkingMode"
	configKeyWebAgentShowThinking = "webAgentUI.showThinking"
)

func versionForDisplay() string {
	return buildinfo.Current().Version
}

type options struct {
	runtimeProfile                runtimeprofile.Profile
	print                         bool
	prompt                        string
	model                         string
	modelExplicit                 bool
	providerName                  string
	outputFormat                  string
	inputFormat                   string
	jsonSchema                    string
	runtimeTraceOutput            string
	maxParallelReadOnlyTools      int
	includeHookEvents             bool
	includePartialMessages        bool
	includeStreamEvents           bool
	maxTurns                      int
	maxTokens                     int
	effort                        string
	outputStyle                   string
	language                      string
	cwd                           string
	resume                        string
	resumeSessionAt               string
	resumeContinuationEntryOffset int
	initialToolResultReplacements []query.ToolResultReplacementRecord
	rewindFiles                   string
	sessionID                     string
	sessionName                   string
	noPersistence                 bool
	forkSession                   bool
	background                    bool
	systemPrompt                  string
	appendSystem                  string
	promptMode                    string
	agentProfileKey               string
	agentProfileVersion           uint
	agentProfileSource            string
	agentProfileRequestedHash     string
	agentProfileEffectiveHash     string
	agentProfileBlockedOverrides  int
	goalEvaluator                 string
	agentName                     string
	allowedTools                  []string
	deniedTools                   []string
	enabledTools                  []string
	toolsSpecified                bool
	permissionMode                string
	permissionBypass              bool
	permissionModeExplicit        bool
	permissionPromptTool          string
	permissionPrompt              func(context.Context, tools.PermissionPromptRequest) tools.PermissionPromptResponse
	userQuestionPrompt            func(context.Context, tools.UserQuestionRequest) tools.UserQuestionResponse
	runtimePermissionMode         func() string
	additionalDirs                []string
	settingsInputs                []string
	mcpConfigInputs               []string
	strictMCPConfig               bool
	skipPermissions               bool
	agentTaskStore                agenttasks.Store
	agentTaskController           *agenttasks.Controller
	nestedAgentProgress           func(agenttasks.EventInput)
	tenantID                      uint64
	tenantUserID                  uint64
	tenantSessionID               uint64
	traceID                       string
	imageGenerator                imagegensvc.Generator
	imageBlobStore                imagegensvc.BlobStore
	mediaAssetStore               media.Store
	imageScheduler                imagegensvc.Scheduler
	asyncChannelImages            bool
	imageOriginFactory            func(tools.Context) (imagegensvc.OriginMetadata, error)
	runID                         string
	queryAttachments              []query.Attachment
	tenantContextManifest         query.TenantContextManifest
	skillProvider                 tools.SkillProvider
	disableTools                  bool
	inlineTenantSkills            []string
	inlineTenantSkillSource       string
	responseFormat                *anthropic.ResponseFormat
	desktopComputerBridge         desktopComputerBridge
	computerUseProfile            bool
	// computerUseFastPath is an explicit trusted-request opt-in; capability availability alone must not enable it.
	computerUseFastPath       bool
	computerUseService        computeruse.Service
	computerUseImageSupported bool

	// Session Control is a v2 Orchestrator-only capability. The profile gate
	// and concrete service are both required before tools are registered.
	sessionControlProfile  bool
	sessionControlService  sessioncontroltool.Service
	sessionOptionsResolved bool
}

func computerUseFastPathEnabled(opts options) bool {
	return opts.computerUseProfile && opts.computerUseFastPath
}

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	opts, rest, err := parseArgs(args)
	if err != nil {
		return err
	}
	var spec commandSpec
	var haveSpec bool
	if len(rest) > 0 {
		spec, haveSpec = lookupCommand(rest[0])
		if !haveSpec {
			return fmt.Errorf("unknown command: %s\n\nRun --help to see available commands", rest[0])
		}
	}
	runtimePolicy, err := runtimeprofile.Resolve(opts.runtimeProfile)
	if err != nil {
		return err
	}
	if err := validateRuntimeProfileCombination(opts, rest, haveSpec, spec); err != nil {
		return err
	}
	// 服务端/后台进程保留结构化 JSON 日志；交互与 CLI 路径默认安静，
	// 诊断输出要靠 NIUMA_LOG_LEVEL 显式打开。
	if !spec.structuredLogs {
		restoreLogger, err := observability.ConfigureCLILogger()
		if err != nil {
			return err
		}
		defer restoreLogger()
	}
	if runtimePolicy.RunStartupUpdate {
		runStartupUpdateForCommand(ctx, opts.cwd, haveSpec, spec, runStartupUpdate)
	}
	if strings.TrimSpace(opts.providerName) != "" {
		if err := validateSelectedProvider(&opts); err != nil {
			return err
		}
	}
	if haveSpec {
		// 隐藏命令是进程内部自调用入口，参数由调用方完全控制，不拦 --help。
		if !spec.hidden && wantsCommandHelp(rest[1:]) {
			printCommandHelp(stdout, spec)
			return nil
		}
		return spec.run(commandContext{
			ctx:    ctx,
			name:   rest[0],
			args:   rest[1:],
			opts:   opts,
			stdin:  os.Stdin,
			stdout: stdout,
			stderr: stderr,
		})
	}
	if err := resolveSessionOptions(session.DefaultStore(), &opts); err != nil {
		return err
	}
	opts.sessionOptionsResolved = true
	if opts.prompt == "" && opts.print {
		prompt, err := readPromptFromStdin(opts.inputFormat)
		if err != nil {
			return err
		}
		opts.prompt = prompt
	}
	if opts.rewindFiles != "" {
		sessionID := opts.resume
		if sessionID == "latest" || sessionID == "" {
			return errors.New("--rewind-files requires --resume <session-id>")
		}
		result, ok, err := session.DefaultStore().RewindFiles(sessionID, opts.rewindFiles)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("session not found: %s", sessionID)
		}
		return writePrettyJSON(stdout, result)
	}
	if opts.prompt == "" && !opts.print {
		return runInteractive(ctx, opts, stdout, stderr)
	}
	if opts.prompt == "" {
		return errors.New("missing prompt for print mode")
	}
	if opts.background {
		_, err := startBackgroundJob(stdout, background.Options{
			Prompt:          opts.prompt,
			CWD:             opts.cwd,
			Model:           opts.model,
			Provider:        opts.providerName,
			OutputFormat:    opts.outputFormat,
			MaxTurns:        opts.maxTurns,
			MaxTokens:       opts.maxTokens,
			Resume:          opts.resume,
			SessionID:       opts.sessionID,
			SessionName:     opts.sessionName,
			NoPersistence:   opts.noPersistence,
			SystemPrompt:    opts.systemPrompt,
			AppendSystem:    opts.appendSystem,
			AllowedTools:    opts.allowedTools,
			DeniedTools:     opts.deniedTools,
			PermissionMode:  opts.permissionMode,
			AdditionalDirs:  opts.additionalDirs,
			SkipPermissions: opts.skipPermissions,
			RuntimeProfile:  opts.runtimeProfile.String(),
			PromptMode:      opts.promptMode,
			Agent:           opts.agentName,
			ToolsSpecified:  opts.toolsSpecified,
			EnabledTools:    opts.enabledTools,
			SettingsInputs:  opts.settingsInputs,
			MCPConfigInputs: opts.mcpConfigInputs,
			StrictMCPConfig: opts.strictMCPConfig,
		}, "__background-run")
		return err
	}
	return runPrint(ctx, opts, stdout, stderr)
}

func validateRuntimeProfileCombination(opts options, rest []string, haveSpec bool, spec commandSpec) error {
	if !opts.runtimeProfile.IsBare() {
		return nil
	}
	if promptmode.Parse(opts.promptMode, promptmode.Code).IsChat() {
		return errors.New("--bare requires --prompt-mode code")
	}
	if !haveSpec || spec.hidden {
		return nil
	}
	if len(rest) > 0 && (rest[0] == "help" || rest[0] == "version") {
		return nil
	}
	return fmt.Errorf("--bare is only supported for query sessions, not %s", rest[0])
}

func startBackgroundJob(stdout io.Writer, bgOpts background.Options, command string) (background.Job, error) {
	store := background.DefaultStore()
	job, err := store.CreateWithOptions(bgOpts)
	if err != nil {
		return background.Job{}, err
	}
	if os.Getenv("NIUMA_BG_QUEUE_ONLY") == "1" {
		fmt.Fprintf(stdout, "Queued background session %s\n", job.ID)
		return job, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return background.Job{}, err
	}
	if _, err := store.Start(job.ID, executable, []string{command, job.ID}, append(os.Environ(), "NIUMA_BACKGROUND_CHILD=1")); err != nil {
		return background.Job{}, err
	}
	fmt.Fprintf(stdout, "Started background session %s\n", job.ID)
	return job, nil
}

func runStartupUpdate(ctx context.Context, cwd string) {
	cfg := config.LoadForCWD(cwd)
	result := updater.CheckOnStartup(ctx, cfg.Settings.Update, cwd, versionForDisplay())
	if result.Reason == "test binary" || (!result.Checked && result.Skipped) {
		return
	}
	attrs := []any{
		"checked", result.Checked,
		"updated", result.Updated,
		"skipped", result.Skipped,
		"reason", result.Reason,
	}
	if result.Output != "" {
		attrs = append(attrs, "output", result.Output)
	}
	// Startup update must never block the product flow; failures are diagnostic.
	if result.Skipped && strings.Contains(result.Reason, "failed") {
		observability.Error(ctx, nil, "startup_update", "internal/cli.runStartupUpdate", "niuma startup update failed", attrs...)
		return
	}
	observability.Debug(ctx, nil, "startup_update", "internal/cli.runStartupUpdate", "niuma startup update checked", attrs...)
}

func runStartupUpdateForCommand(ctx context.Context, cwd string, haveSpec bool, spec commandSpec, update func(context.Context, string)) {
	if haveSpec && spec.skipStartupUpdate {
		return
	}
	update(ctx, cwd)
}

func runPrint(ctx context.Context, opts options, stdout, stderr io.Writer) error {
	var err error
	opts.prompt, err = resolvePrintPromptWithOptions(opts, opts.prompt)
	if err != nil {
		return err
	}
	session, cleanup, err := newQuerySession(ctx, opts, nil, nil)
	if err != nil {
		return err
	}
	defer cleanup()

	switch opts.outputFormat {
	case "text":
		if opts.jsonSchema == "" {
			return session.RunText(ctx, opts.prompt, stdout)
		}
		res, err := session.Run(ctx, opts.prompt, io.Discard)
		if err != nil {
			return err
		}
		if err := validateJSONSchemaResponse(opts.cwd, opts.jsonSchema, res.Response); err != nil {
			return err
		}
		fmt.Fprintln(stdout, res.Response)
		return nil
	case "json":
		res, err := session.Run(ctx, opts.prompt, io.Discard)
		if err != nil {
			return err
		}
		if err := validateJSONSchemaResponse(opts.cwd, opts.jsonSchema, res.Response); err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(res)
	case "stream-json":
		if opts.jsonSchema != "" {
			return errors.New("--json-schema is not supported with --output-format stream-json")
		}
		return session.RunStreamJSON(ctx, opts.prompt, stdout)
	default:
		return fmt.Errorf("unsupported output format: %s", opts.outputFormat)
	}
}

func resolvePrintPrompt(cwd, prompt string) (string, error) {
	return resolvePrintPromptWithOptions(options{cwd: cwd}, prompt)
}

func resolvePrintPromptWithOptions(opts options, prompt string) (string, error) {
	resolved, ok, err := dynamicSlashPromptWithOptions(opts, prompt)
	if err != nil {
		return "", err
	}
	if ok {
		return resolved, nil
	}
	return prompt, nil
}

func readPromptFromStdin(inputFormat string) (string, error) {
	info, err := os.Stdin.Stat()
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeCharDevice != 0 {
		return "", errors.New("missing prompt for print mode")
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", err
	}
	switch inputFormat {
	case "", "text":
		return strings.TrimRight(string(data), "\r\n"), nil
	case "stream-json":
		return promptFromStreamJSON(data)
	default:
		return "", fmt.Errorf("unsupported input format: %s", inputFormat)
	}
}

func promptFromStreamJSON(data []byte) (string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var parts []string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			return "", fmt.Errorf("invalid stream-json input: %w", err)
		}
		if text := streamJSONPromptText(obj); text != "" {
			parts = append(parts, text)
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return strings.Join(parts, "\n"), nil
}

func streamJSONPromptText(obj map[string]any) string {
	if typ, _ := obj["type"].(string); typ != "" && typ != "user" && typ != "user_message" && typ != "message" {
		return ""
	}
	for _, key := range []string{"prompt", "content", "text"} {
		if text := anyText(obj[key]); text != "" {
			return text
		}
	}
	if message, ok := obj["message"].(map[string]any); ok {
		for _, key := range []string{"content", "text"} {
			if text := anyText(message[key]); text != "" {
				return text
			}
		}
	}
	return ""
}

func anyText(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case map[string]any:
		if text, _ := v["text"].(string); text != "" {
			return text
		}
		return anyText(v["content"])
	case []any:
		var parts []string
		for _, item := range v {
			if text := anyText(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func jsonSchemaText(cwd, input string) (string, error) {
	data, err := runtimeInputBytes(cwd, input)
	if err != nil {
		return "", err
	}
	if !json.Valid(data) {
		return "", errors.New("invalid --json-schema JSON")
	}
	return string(data), nil
}

func validateJSONSchemaResponse(cwd, schemaInput, response string) error {
	if schemaInput == "" {
		return nil
	}
	data, err := runtimeInputBytes(cwd, schemaInput)
	if err != nil {
		return err
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		return fmt.Errorf("invalid --json-schema JSON: %w", err)
	}
	var value any
	payload := extractJSONPayload(response)
	if err := json.Unmarshal([]byte(payload), &value); err != nil {
		return fmt.Errorf("response does not match --json-schema: invalid JSON response: %w", err)
	}
	if err := validateSchemaValue("$", schema, value); err != nil {
		return fmt.Errorf("response does not match --json-schema: %w", err)
	}
	return nil
}

func extractJSONPayload(response string) string {
	text := strings.TrimSpace(response)
	if strings.HasPrefix(text, "```") {
		lines := strings.Split(text, "\n")
		if len(lines) >= 3 && strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
			return strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
		}
	}
	return text
}

func validateSchemaValue(path string, schema map[string]any, value any) error {
	if typ, ok := schema["type"]; ok {
		if err := validateSchemaType(path, typ, value); err != nil {
			return err
		}
	}
	if enumValues, ok := schema["enum"].([]any); ok && len(enumValues) > 0 {
		for _, candidate := range enumValues {
			if fmt.Sprint(candidate) == fmt.Sprint(value) {
				return nil
			}
		}
		return fmt.Errorf("%s must be one of %v", path, enumValues)
	}
	object, isObject := value.(map[string]any)
	if required, ok := schema["required"].([]any); ok {
		if !isObject {
			return fmt.Errorf("%s must be an object for required fields", path)
		}
		for _, raw := range required {
			name, ok := raw.(string)
			if !ok {
				continue
			}
			if _, exists := object[name]; !exists {
				return fmt.Errorf("%s.%s is required", path, name)
			}
		}
	}
	if properties, ok := schema["properties"].(map[string]any); ok {
		if !isObject {
			return fmt.Errorf("%s must be an object for properties", path)
		}
		for name, rawSchema := range properties {
			child, exists := object[name]
			if !exists {
				continue
			}
			childSchema, ok := rawSchema.(map[string]any)
			if !ok {
				continue
			}
			if err := validateSchemaValue(path+"."+name, childSchema, child); err != nil {
				return err
			}
		}
	}
	if itemSchema, ok := schema["items"].(map[string]any); ok {
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s must be an array for items", path)
		}
		for i, item := range items {
			if err := validateSchemaValue(fmt.Sprintf("%s[%d]", path, i), itemSchema, item); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateSchemaType(path string, typ any, value any) error {
	if types, ok := typ.([]any); ok {
		var last error
		for _, raw := range types {
			name, _ := raw.(string)
			if name == "" {
				continue
			}
			if err := validateSingleSchemaType(path, name, value); err == nil {
				return nil
			} else {
				last = err
			}
		}
		if last != nil {
			return last
		}
		return nil
	}
	name, _ := typ.(string)
	if name == "" {
		return nil
	}
	return validateSingleSchemaType(path, name, value)
}

func validateSingleSchemaType(path, typ string, value any) error {
	ok := false
	switch typ {
	case "object":
		_, ok = value.(map[string]any)
	case "array":
		_, ok = value.([]any)
	case "string":
		_, ok = value.(string)
	case "number":
		_, ok = value.(float64)
	case "integer":
		n, isNumber := value.(float64)
		ok = isNumber && n == float64(int64(n))
	case "boolean":
		_, ok = value.(bool)
	case "null":
		ok = value == nil
	default:
		ok = true
	}
	if !ok {
		return fmt.Errorf("%s must be %s", path, typ)
	}
	return nil
}

func newQuerySession(ctx context.Context, opts options, initial []anthropic.MessageParam, recorderOverride *session.Recorder) (*query.Session, func(), error) {
	if !opts.sessionOptionsResolved {
		if err := resolveSessionOptions(session.DefaultStore(), &opts); err != nil {
			return nil, nil, err
		}
		opts.sessionOptionsResolved = true
	}
	if strings.TrimSpace(opts.runtimeTraceOutput) != "" && opts.noPersistence {
		return nil, nil, errors.New("--runtime-trace-output requires session persistence")
	}
	runtimePolicy, err := runtimeprofile.Resolve(opts.runtimeProfile)
	if err != nil {
		return nil, nil, err
	}
	cfg, err := resolveRuntimeProviderConfig(&opts)
	if err != nil {
		return nil, nil, err
	}
	model := opts.model
	maxTurns := opts.maxTurns
	var mainThreadAgent agents.Agent
	if strings.TrimSpace(opts.agentName) != "" {
		var agent agents.Agent
		var ok bool
		var err error
		if runtimePolicy.DiscoverAgents {
			agent, ok, err = agents.Load(opts.cwd, opts.agentName)
		} else {
			agent, ok = agents.BuiltIn(opts.agentName)
		}
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			if runtimePolicy.Profile.IsBare() {
				return nil, nil, fmt.Errorf("agent unavailable in --bare mode: %s (only built-in agents are supported)", opts.agentName)
			}
			return nil, nil, fmt.Errorf("agent not found: %s", opts.agentName)
		}
		mainThreadAgent = agent
		nextModel, nextMaxTurns, err := applyMainThreadAgentOptions(&cfg.Settings, opts, agent, model, maxTurns)
		if err != nil {
			return nil, nil, err
		}
		model, maxTurns = nextModel, nextMaxTurns
	}
	computerUseGuidance, cleanupComputerUse := configureDesktopComputerUse(ctx, cfg, model, &opts)
	computerUseCleanupTransferred := false
	defer func() {
		if !computerUseCleanupTransferred {
			cleanupComputerUse()
		}
	}()
	if runtimePolicy.DiscoverPlugins {
		cfg.Settings.MCPServers = mergedMCPServers(opts.cwd, cfg.Settings.MCPServers)
	}
	cfg = constrainComputerUseFallbacks(cfg, model, opts.computerUseProfile)
	client := anthropic.NewClient(cfg)
	policy := permissions.FromSettings(cfg.Settings.Permissions)
	hookRunner := hooks.New(cfg.Settings.Hooks)
	if runtimePolicy.DiscoverAgents {
		ensureLocalAgentTaskRuntime(&opts)
	}
	cleanupMCP := func() {}
	registry := tools.NewRegistry()
	if !opts.disableTools {
		toolList := coreRuntimeToolsWithOptions(cfg.Settings, client, model, opts)
		if runtimePolicy.ToolSet == runtimeprofile.ToolSetMinimal {
			toolList = bareRuntimeTools()
		}
		toolList = filterRuntimeTools(toolList, opts)
		mcpTools, cleanup := mcp.LoadTools(ctx, cfg.Settings.MCPServers)
		cleanupMCP = cleanup
		toolList = append(toolList, filterRuntimeTools(mcpTools, opts)...)
		registry = tools.NewRegistry(tools.GuardAll(policy, toolList...)...)
		if runtimePolicy.ToolSet == runtimeprofile.ToolSetFull && (!opts.toolsSpecified || containsString(opts.enabledTools, "Task")) {
			taskOptions := []task.Option{task.WithRegistry(registry), task.WithHooks(hookRunner), task.WithMaxTurns(maxTurns)}
			if opts.agentTaskStore != nil {
				taskOptions = append(taskOptions, task.WithTaskStore(opts.agentTaskStore))
			}
			if opts.agentTaskController != nil {
				taskOptions = append(taskOptions, task.WithController(opts.agentTaskController))
			}
			registry.Register(tools.Guard(task.New(client, model, taskOptions...), policy))
		}
		if runtimePolicy.ToolSet == runtimeprofile.ToolSetFull && opts.toolsSpecified && containsString(opts.enabledTools, "Agent") {
			registry.Register(tools.Guard(agenttool.NewCompatWithHooksAndCWD(client, model, registry, hookRunner, opts.cwd), policy))
		}
		if runtimePolicy.ToolSet == runtimeprofile.ToolSetFull && (!opts.toolsSpecified || containsString(opts.enabledTools, "AgentCreate")) {
			registry.Register(tools.Guard(agenttool.NewCreate(client, model, registry), policy))
		}
		if runtimePolicy.ToolSet == runtimeprofile.ToolSetFull && (!opts.toolsSpecified || containsString(opts.enabledTools, "AgentMessage")) {
			registry.Register(tools.Guard(agenttool.NewMessageWithRuntime(client, model, registry), policy))
		}
		if runtimePolicy.ToolSet == runtimeprofile.ToolSetFull && experimentalAgentTeamsEnabled() && (!opts.toolsSpecified || containsString(opts.enabledTools, "SendMessage")) {
			registry.Register(tools.Guard(agenttool.NewSendMessageWithRuntime(client, model, registry), policy))
		}
	}
	store := session.DefaultStore()
	resumeEntries, initialMessages, initialReplacements, err := loadResumeContextWithEntries(store, opts.resume, opts.resumeSessionAt)
	if err != nil {
		cleanupMCP()
		return nil, nil, err
	}
	seedResumeAgentTasksFromEntries(ctx, opts.agentTaskStore, resumeEntries)
	initialAcknowledgedAgentTasks := acknowledgedAgentTasksFromResumeEntries(resumeEntries)
	initialActiveSkillMessages := activeSkillMessagesFromResumeEntries(resumeEntries)
	initialMessages = append(initialMessages, initial...)
	initialReplacements = append(initialReplacements, opts.initialToolResultReplacements...)
	recorder := recorderOverride
	ownRecorder := false
	if recorder == nil && !opts.noPersistence {
		recorder, err = newRecorderForOptions(store, opts)
		if err != nil {
			cleanupMCP()
			return nil, nil, err
		}
		ownRecorder = true
	}
	cleanup := func() {
		// Revoke host approval before potentially slow recorder/MCP teardown.
		cleanupComputerUse()
		if ownRecorder {
			_ = recorder.Close()
		}
		if recorder != nil && strings.TrimSpace(opts.runtimeTraceOutput) != "" {
			outputPath := opts.runtimeTraceOutput
			if !filepath.IsAbs(outputPath) {
				outputPath = filepath.Join(opts.cwd, outputPath)
			}
			workers, parallelEnabled := query.EffectiveMaxParallelReadOnlyTools(opts.maxParallelReadOnlyTools)
			mode := server.RuntimeTraceReadOnlyToolModeSerial
			if parallelEnabled {
				mode = server.RuntimeTraceReadOnlyToolModeParallel
			}
			configuration := server.RuntimeTraceConfiguration{
				ReadOnlyToolMode:                  mode,
				RequestedMaxParallelReadOnlyTools: opts.maxParallelReadOnlyTools,
				EffectiveMaxParallelReadOnlyTools: workers,
			}
			if _, exportErr := server.ExportLocalRuntimeTrace([]session.Store{store}, recorder.SessionID, outputPath, configuration); exportErr != nil {
				observability.Error(ctx, nil, "runtime_trace.export_error", "cli.newQuerySession", "runtime trace export failed", "session_id", recorder.SessionID, "output_path", outputPath, "error", exportErr)
			}
		}
		cleanupMCP()
	}
	systemAddendum := ""
	if opts.appendSystem != "" {
		systemAddendum = appendWithBlankLine(systemAddendum, opts.appendSystem)
	}
	if opts.jsonSchema != "" {
		schemaText, err := jsonSchemaText(opts.cwd, opts.jsonSchema)
		if err != nil {
			cleanupMCP()
			return nil, nil, err
		}
		systemAddendum = appendWithBlankLine(systemAddendum, "Respond with a single valid JSON value matching this JSON Schema. Do not include markdown fences or explanatory text.\n\n"+schemaText)
	}
	systemAddendum = appendWithBlankLine(systemAddendum, computerUseGuidance)
	if runtimePolicy.RunHooks {
		_, _ = hookRunner.RunWithPayload(ctx, hooks.SessionStart, opts.cwd, hooks.Payload{})
	}
	runtimePermissionMode := opts.runtimePermissionMode
	if runtimePermissionMode == nil {
		mode := strings.TrimSpace(opts.permissionMode)
		if opts.skipPermissions {
			mode = "bypassPermissions"
		}
		if mode != "" {
			runtimePermissionMode = func() string { return mode }
		}
	}
	effort := firstNonEmptyString(
		strings.TrimSpace(opts.effort),
		strings.TrimSpace(os.Getenv("NIUMA_EFFORT")),
		strings.TrimSpace(os.Getenv("CLAUDE_CODE_EFFORT")),
		strings.TrimSpace(cfg.Settings.Effort),
		defaults.Effort,
	)
	querySession := query.New(client, registry, query.Options{
		Model:                         model,
		MaxTurns:                      maxTurns,
		MaxTokens:                     opts.maxTokens,
		MaxParallelReadOnlyTools:      opts.maxParallelReadOnlyTools,
		Effort:                        effort,
		ToolResultLimit:               cfg.Settings.MaxToolResultBytes,
		CWD:                           opts.cwd,
		WritableRoots:                 cfg.Settings.AdditionalDirectories,
		Recorder:                      recorder,
		InitialMessages:               initialMessages,
		InitialToolResultReplacements: initialReplacements,
		InitialAcknowledgedAgentTasks: initialAcknowledgedAgentTasks,
		InitialActiveSkillMessages:    initialActiveSkillMessages,
		SystemPrompt:                  opts.systemPrompt,
		MainThreadAgentPrompt:         mainThreadAgent.Prompt,
		InitialPrompt:                 mainThreadAgent.InitialPrompt,
		SystemAddendum:                systemAddendum,
		PromptMode:                    promptmode.Parse(opts.promptMode, promptmode.Code).String(),
		AgentProfileKey:               opts.agentProfileKey,
		AgentProfileVersion:           opts.agentProfileVersion,
		AgentProfileSource:            opts.agentProfileSource,
		AgentProfileRequestedHash:     opts.agentProfileRequestedHash,
		AgentProfileEffectiveHash:     opts.agentProfileEffectiveHash,
		AgentProfileBlockedOverrides:  opts.agentProfileBlockedOverrides,
		OutputStyle:                   opts.outputStyle,
		Language:                      opts.language,
		Hooks:                         hookRunner,
		PermissionPromptTool:          opts.permissionPromptTool,
		PermissionPrompt:              opts.permissionPrompt,
		UserQuestionPrompt:            opts.userQuestionPrompt,
		RuntimePermissionMode:         runtimePermissionMode,
		Sandbox:                       sandboxConfigFromSettings(cfg.Settings.Sandbox),
		TaskStore:                     opts.agentTaskStore,
		TaskController:                opts.agentTaskController,
		TenantID:                      opts.tenantID,
		UserID:                        opts.tenantUserID,
		TenantSessionID:               opts.tenantSessionID,
		TraceID:                       opts.traceID,
		RunID:                         opts.runID,
		ImageGenerator:                opts.imageGenerator,
		ImageBlobStore:                opts.imageBlobStore,
		MediaAssetStore:               opts.mediaAssetStore,
		ComputerUse:                   opts.computerUseService,
		ComputerUseImageSupported:     opts.computerUseImageSupported,
		ComputerUseFastPath:           computerUseFastPathEnabled(opts),
		IncludeHookEvents:             opts.includeHookEvents,
		IncludePartialMessages:        opts.includePartialMessages,
		IncludeStreamEvents:           opts.includeStreamEvents,
		NestedAgentProgress:           opts.nestedAgentProgress,
		Attachments:                   opts.queryAttachments,
		AutoCompact:                   compact.ConfigFromSettings(cfg.Settings, model),
		TenantContextManifest:         opts.tenantContextManifest,
		SkillProvider:                 opts.skillProvider,
		DisableTools:                  opts.disableTools,
		InlineTenantSkills:            opts.inlineTenantSkills,
		InlineTenantSkillSource:       opts.inlineTenantSkillSource,
		ResponseFormat:                opts.responseFormat,
		RuntimeProfile:                runtimePolicy.Profile,
		ExplicitContextRoots:          resolveRuntimeDirectories(opts.cwd, cfg.Settings.AdditionalDirectories),
	})
	computerUseCleanupTransferred = true
	return querySession, cleanup, nil
}

func resolveRuntimeDirectories(cwd string, directories []string) []string {
	resolved := make([]string, 0, len(directories))
	seen := map[string]bool{}
	for _, directory := range directories {
		directory = strings.TrimSpace(directory)
		if directory == "" {
			continue
		}
		if !filepath.IsAbs(directory) {
			directory = filepath.Join(cwd, directory)
		}
		directory = filepath.Clean(directory)
		if !seen[directory] {
			seen[directory] = true
			resolved = append(resolved, directory)
		}
	}
	return resolved
}

func applySelectedProvider(cfg *config.Config, opts *options) error {
	if cfg == nil || opts == nil || strings.TrimSpace(opts.providerName) == "" {
		return nil
	}
	selected, err := cfg.SelectProvider(opts.providerName)
	if err != nil {
		// Project config may intentionally disable fallback providers. An
		// explicit provider selection is still allowed to resolve against the
		// user's global registry so the WebUI picker and runtime do not diverge.
		global := config.LoadForCWD("")
		globalSelected, globalErr := global.SelectProvider(opts.providerName)
		if globalErr != nil {
			return err
		}
		selected = globalSelected
	}
	*cfg = selected
	if !opts.modelExplicit && selected.SelectedProviderModel != "" {
		opts.model = selected.SelectedProviderModel
	}
	return nil
}

func validateSelectedProvider(opts *options) error {
	if opts == nil {
		return nil
	}
	_, err := resolveRuntimeProviderConfig(opts)
	return err
}

// resolveRuntimeProviderConfig is the single ordering point for CLI runtime
// overrides and named-provider selection. Both UI metadata and query creation
// must use it so they report and execute the same effective model/provider.
func resolveRuntimeProviderConfig(opts *options) (config.Config, error) {
	if opts == nil {
		return config.Config{}, errors.New("runtime options are required")
	}
	cfg := config.LoadForCWD(opts.cwd)
	if err := prepareRuntimeSettings(&cfg.Settings, *opts); err != nil {
		return config.Config{}, err
	}
	cfg = cfg.WithRuntimeSettings(cfg.Settings)
	if err := applySelectedProvider(&cfg, opts); err != nil {
		return config.Config{}, err
	}
	return cfg, nil
}

func prepareRuntimeSettings(settings *config.Settings, opts options) error {
	if opts.runtimeProfile.IsBare() {
		settings.Hooks = nil
		settings.MCPServers = nil
		settings.AdditionalDirectories = nil
	}
	if err := applyRuntimeOptions(settings, opts); err != nil {
		return err
	}
	if opts.runtimeProfile.IsBare() {
		settings.Hooks = nil
	}
	return nil
}

func applyMainThreadAgentOptions(settings *config.Settings, opts options, agent agents.Agent, currentModel string, currentMaxTurns int) (string, int, error) {
	model := currentModel
	maxTurns := currentMaxTurns
	if strings.TrimSpace(agent.Model) != "" && !opts.modelExplicit {
		model = strings.TrimSpace(agent.Model)
	}
	if agent.MaxTurns > 0 {
		maxTurns = agent.MaxTurns
	}
	settings.Permissions.Allow = append(settings.Permissions.Allow, agent.Tools...)
	settings.Permissions.Deny = append(settings.Permissions.Deny, agent.DisallowedTools...)
	if strings.TrimSpace(agent.PermissionMode) != "" && strings.TrimSpace(opts.permissionMode) == "" && !opts.skipPermissions {
		mode, ok := permissions.NormalizeMode(agent.PermissionMode)
		if !ok {
			return "", 0, fmt.Errorf("invalid agent permissionMode for %s: %s", firstNonEmptyString(agent.Name, opts.agentName), agent.PermissionMode)
		}
		settings.Permissions.DefaultMode = mode
		if settings.Permissions.RuleSources == nil {
			settings.Permissions.RuleSources = map[string]string{}
		}
		settings.Permissions.RuleSources["defaultMode"] = "agent:" + firstNonEmptyString(agent.Name, opts.agentName)
	}
	return model, maxTurns, nil
}

func sandboxConfigFromSettings(settings *config.SandboxSettings) tools.SandboxConfig {
	if settings == nil {
		return tools.SandboxConfig{
			AllowUnsandboxedCommands: true,
			AutoAllowBashIfSandboxed: true,
		}
	}
	return tools.SandboxConfig{
		Enabled:                   boolValue(settings.Enabled, false),
		FailIfUnavailable:         boolValue(settings.FailIfUnavailable, false),
		AllowUnsandboxedCommands:  boolValue(settings.AllowUnsandboxedCommands, true),
		AutoAllowBashIfSandboxed:  boolValue(settings.AutoAllowBashIfSandboxed, true),
		EnabledPlatforms:          append([]string(nil), settings.EnabledPlatforms...),
		ExcludedCommands:          append([]string(nil), settings.ExcludedCommands...),
		FilesystemAllowRead:       append([]string(nil), settings.Filesystem.AllowRead...),
		FilesystemDenyRead:        append([]string(nil), settings.Filesystem.DenyRead...),
		FilesystemAllowWrite:      append([]string(nil), settings.Filesystem.AllowWrite...),
		FilesystemDenyWrite:       append([]string(nil), settings.Filesystem.DenyWrite...),
		NetworkDisabled:           boolValue(settings.Network.Disabled, false),
		NetworkAllowDomains:       append([]string(nil), settings.Network.AllowDomains...),
		NetworkDenyDomains:        append([]string(nil), settings.Network.DenyDomains...),
		NetworkProxyURL:           settings.Network.Proxy.URL,
		NetworkProxyMode:          settings.Network.Proxy.Mode,
		NetworkProxyRequired:      boolValue(settings.Network.Proxy.Required, false),
		NetworkMITMCAFile:         settings.Network.MITM.CAFile,
		NetworkMITMRequired:       boolValue(settings.Network.MITM.Required, false),
		UnixSocketDeny:            append([]string(nil), settings.UnixSockets.Deny...),
		SeccompEnabled:            boolValue(settings.Seccomp.Enabled, false),
		SeccompMode:               settings.Seccomp.Mode,
		AllowPty:                  boolValue(settings.AllowPty, false),
		EnableWeakerNestedSandbox: boolValue(settings.EnableWeakerNestedSandbox, false),
	}
}

func boolValue(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func newRecorderForOptions(store session.Store, opts options) (*session.Recorder, error) {
	if opts.noPersistence {
		return nil, nil
	}
	// v2 message-graph writing now defaults ON for new sessions; opt out via
	// NIUMA_FEATURE_TRANSCRIPT_V2=0 or featureFlags:{transcript_v2:false}.
	// Resuming always follows the schema already on disk, so files never mix.
	store.SchemaV2 = config.FeatureEnabledDefault(opts.cwd, "transcript_v2", config.FeatureContext{UserType: os.Getenv("USER_TYPE")}, true)
	var (
		recorder *session.Recorder
		err      error
	)
	if opts.sessionID != "" {
		recorder, _, err = store.OpenOrCreateRecorder(opts.cwd, opts.sessionID)
	} else if resumeID, ok, resolveErr := resumeRecorderSessionID(store, opts); resolveErr != nil {
		return nil, resolveErr
	} else if ok {
		recorder, _, err = store.OpenRecorder(resumeID)
	} else {
		recorder, err = store.NewRecorder(opts.cwd)
	}
	if err != nil {
		return nil, err
	}
	if opts.sessionName != "" {
		if err := recorder.Append(session.Entry{Type: "session", Name: opts.sessionName}); err != nil {
			_ = recorder.Close()
			return nil, err
		}
	}
	return recorder, nil
}

// resolveSessionOptions is the single CLI session-selection boundary. It runs
// before either the TUI or headless context is built, so an explicit
// --session-id consistently opens an existing transcript and creates a new one
// only when that id has not been seen before.
func resolveSessionOptions(store session.Store, opts *options) error {
	if opts == nil {
		return nil
	}
	sessionID := strings.TrimSpace(opts.sessionID)
	if sessionID == "" {
		if opts.forkSession && strings.TrimSpace(opts.resume) == "" {
			return errors.New("--fork-session requires --resume")
		}
		if strings.TrimSpace(opts.resumeSessionAt) != "" && strings.TrimSpace(opts.resume) == "" {
			return errors.New("--resume-session-at requires --resume")
		}
		return nil
	}
	if opts.noPersistence {
		return errors.New("--session-id cannot be combined with --no-session-persistence")
	}
	if strings.TrimSpace(opts.resume) != "" {
		return errors.New("--session-id cannot be combined with --resume")
	}
	if strings.TrimSpace(opts.resumeSessionAt) != "" {
		return errors.New("--session-id cannot be combined with --resume-session-at")
	}
	if opts.forkSession {
		return errors.New("--session-id cannot be combined with --fork-session")
	}
	if !session.IsValidID(sessionID) {
		return fmt.Errorf("invalid --session-id: %s", sessionID)
	}
	summary, ok, err := store.Find(sessionID)
	if err != nil {
		return err
	}
	if ok {
		opts.resume = summary.SessionID
	}
	return nil
}

func resumeRecorderSessionID(store session.Store, opts options) (string, bool, error) {
	resumeID := strings.TrimSpace(opts.resume)
	if resumeID == "" || strings.TrimSpace(opts.resumeSessionAt) != "" {
		return "", false, nil
	}
	resolved, err := resolveResumeSessionID(store, resumeID)
	if err != nil {
		return "", false, err
	}
	// --fork-session：resume 上下文照常加载，但这一轮记进新会话，
	// 原 transcript 一个字节都不动。解析仍要先跑，好让不存在的
	// session id 依旧报错而不是静默开新会话。
	if opts.forkSession {
		return "", false, nil
	}
	return resolved, true, nil
}

func resolveResumeSessionID(store session.Store, resumeID string) (string, error) {
	if resumeID == "latest" {
		summaries, err := store.List()
		if err != nil {
			return "", err
		}
		if len(summaries) == 0 {
			return "", errors.New("no sessions found")
		}
		return summaries[0].SessionID, nil
	}
	if _, ok, err := store.Find(resumeID); err != nil || !ok {
		if err != nil {
			return "", err
		}
		return "", fmt.Errorf("session not found: %s", resumeID)
	}
	return resumeID, nil
}

func doctor(stdout io.Writer) error {
	cfg := config.Load()
	globalSettingsPath, _ := config.GlobalSettingsPath()
	_, gitErr := exec.LookPath("git")
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = os.Getenv("ComSpec")
	}
	settings := map[string]any{
		"sources":         cfg.Sources,
		"mcpServers":      len(cfg.Settings.MCPServers),
		"permissionMode":  cfg.Settings.Permissions.DefaultMode,
		"permissionAllow": len(cfg.Settings.Permissions.Allow),
		"permissionDeny":  len(cfg.Settings.Permissions.Deny),
	}
	// Only present when something is actually wrong, so a healthy install keeps
	// the output it always had.
	if len(cfg.Warnings) > 0 {
		settings["configWarnings"] = cfg.Warnings
	}
	status := map[string]any{
		"version":      versionForDisplay(),
		"defaultModel": config.ResolveModel("", ""),
		"hasAPIKey":    cfg.APIKey != "",
		"baseURL":      cfg.BaseURL,
		"runtime":      "go",
		"gitAvailable": gitErr == nil,
		"shell":        shell,
		"config": map[string]any{
			"globalSettingsPath": globalSettingsPath,
			"sessionRoot":        session.DefaultStore().Root,
		},
		"settings": settings,
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(status)
}

func authCommand(args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] == "status" {
		cfg := config.Load()
		method := "none"
		if cfg.AuthToken != "" {
			method = "oauth_token"
		} else if cfg.APIKey != "" {
			method = "api_key"
		}
		return writePrettyJSON(stdout, map[string]any{
			"loggedIn":     cfg.APIKey != "" || cfg.AuthToken != "",
			"authMethod":   method,
			"apiProvider":  cfg.Provider,
			"hasAPIKey":    cfg.APIKey != "",
			"hasAuthToken": cfg.AuthToken != "",
		})
	}
	if args[0] == "login" {
		apiKey := ""
		authToken := ""
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--api-key":
				if i+1 >= len(args) {
					return errors.New("--api-key requires a value")
				}
				apiKey = args[i+1]
				i++
			case "--auth-token", "--oauth-token", "--token":
				if i+1 >= len(args) {
					return fmt.Errorf("%s requires a value", args[i])
				}
				authToken = args[i+1]
				i++
			default:
				return fmt.Errorf("unknown auth login option: %s", args[i])
			}
		}
		if apiKey == "" && authToken == "" {
			return errors.New("auth login requires --api-key or --auth-token")
		}
		if apiKey != "" && authToken != "" {
			return errors.New("use either --api-key or --auth-token, not both")
		}
		settings := config.LoadGlobalSettings()
		if apiKey != "" {
			settings.APIKey = apiKey
			settings.AuthToken = ""
		} else {
			settings.AuthToken = authToken
			settings.APIKey = ""
		}
		if err := config.SaveGlobalSettings(settings); err != nil {
			return err
		}
		if apiKey != "" {
			fmt.Fprintln(stdout, "Logged in with API key")
		} else {
			fmt.Fprintln(stdout, "Logged in with auth token")
		}
		return nil
	}
	if args[0] == "logout" {
		settings := config.LoadGlobalSettings()
		settings.APIKey = ""
		settings.AuthToken = ""
		if err := config.SaveGlobalSettings(settings); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "Logged out")
		return nil
	}
	return fmt.Errorf("unknown auth command: %s", args[0])
}

func configCommand(args []string, cwd string, stdout io.Writer) error {
	args, loadSettings, saveSettings, scoped, err := configScope(args, cwd)
	if err != nil {
		return err
	}
	if len(args) == 0 || args[0] == "list" {
		if scoped {
			return writePrettyJSON(stdout, loadSettings())
		}
		loaded := config.LoadSettings(cwd)
		return writePrettyJSON(stdout, loaded)
	}
	switch args[0] {
	case "get":
		if len(args) < 2 {
			return errors.New("config get requires a key")
		}
		settings := loadSettings()
		value, ok := getConfigValue(settings, args[1])
		if !ok {
			return fmt.Errorf("unknown config key: %s", args[1])
		}
		fmt.Fprintln(stdout, value)
		return nil
	case "set":
		if len(args) < 3 {
			return errors.New("config set requires a key and value")
		}
		settings := loadSettings()
		if err := setConfigValue(&settings, args[1], args[2]); err != nil {
			return err
		}
		if err := saveSettings(settings); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Set %s\n", args[1])
		return nil
	case "unset", "remove":
		if len(args) < 2 {
			return errors.New("config unset requires a key")
		}
		settings := loadSettings()
		if err := unsetConfigValue(&settings, args[1]); err != nil {
			return err
		}
		if err := saveSettings(settings); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Unset %s\n", args[1])
		return nil
	default:
		return fmt.Errorf("unknown config command: %s", args[0])
	}
}

func configScope(args []string, cwd string) ([]string, func() config.Settings, func(config.Settings) error, bool, error) {
	local := false
	project := false
	clean := make([]string, 0, len(args))
	for _, arg := range args {
		switch arg {
		case "--local":
			local = true
		case "--project":
			project = true
		default:
			clean = append(clean, arg)
		}
	}
	if local && project {
		return nil, nil, nil, false, errors.New("use only one of --local or --project")
	}
	if local || project {
		if err := config.EnsureProjectSettingsMaterialized(cwd); err != nil {
			return nil, nil, nil, false, err
		}
		path := config.ProjectSettingsPath(cwd, local)
		load := func() config.Settings { return config.LoadSettingsFile(path) }
		save := func(settings config.Settings) error { return config.SaveSettingsFile(path, settings) }
		return clean, load, save, true, nil
	}
	return clean, config.LoadGlobalSettings, config.SaveGlobalSettings, false, nil
}

func applyRuntimeOptions(settings *config.Settings, opts options) error {
	for _, input := range opts.settingsInputs {
		override, err := loadSettingsInput(opts.cwd, input)
		if err != nil {
			return err
		}
		config.AnnotatePermissionSources(&override, "cli-settings")
		*settings = config.MergeSettings(*settings, override)
	}
	if len(opts.mcpConfigInputs) > 0 {
		servers, err := loadMCPConfigInputs(opts.cwd, opts.mcpConfigInputs)
		if err != nil {
			return err
		}
		if opts.strictMCPConfig {
			settings.MCPServers = servers
		} else {
			*settings = config.MergeSettings(*settings, config.Settings{MCPServers: servers})
		}
	} else if opts.strictMCPConfig {
		settings.MCPServers = nil
	}
	if opts.permissionModeExplicit {
		// A request may narrow a configured bypass; explicit deny rules remain.
		settings.Permissions.Bypass = permissions.ModeGrantsBypass(opts.permissionMode)
	}
	if opts.skipPermissions || opts.permissionBypass {
		// A bypass run drops the allow/ask surface but keeps explicit deny rules:
		// CheckRequest evaluates deny before the bypass short-circuit, so a
		// deny in settings still holds under --dangerously-skip-permissions.
		deny := append(append([]string(nil), settings.Permissions.Deny...), opts.deniedTools...)
		ruleSources := settings.Permissions.RuleSources
		if ruleSources == nil {
			ruleSources = map[string]string{}
		}
		settings.Permissions = config.PermissionSettings{
			Allow:       []string{"*"},
			Deny:        deny,
			DefaultMode: "allow",
			Bypass:      true,
			RuleSources: ruleSources,
		}
		settings.Permissions.RuleSources["allow:*"] = "cli"
		settings.Permissions.RuleSources["defaultMode"] = "cli"
		for _, rule := range opts.deniedTools {
			settings.Permissions.RuleSources["deny:"+strings.TrimSpace(rule)] = "cli"
		}
	} else {
		settings.Permissions.Allow = append(settings.Permissions.Allow, opts.allowedTools...)
		settings.Permissions.Deny = append(settings.Permissions.Deny, opts.deniedTools...)
		if settings.Permissions.RuleSources == nil {
			settings.Permissions.RuleSources = map[string]string{}
		}
		for _, rule := range opts.allowedTools {
			settings.Permissions.RuleSources["allow:"+strings.TrimSpace(rule)] = "cli"
		}
		for _, rule := range opts.deniedTools {
			settings.Permissions.RuleSources["deny:"+strings.TrimSpace(rule)] = "cli"
		}
		if opts.permissionMode != "" {
			mode, ok := permissions.NormalizeMode(opts.permissionMode)
			if !ok {
				return fmt.Errorf("--permission-mode must be one of %s, got %q",
					strings.Join(permissions.AcceptedModes(), ", "), opts.permissionMode)
			}
			settings.Permissions.DefaultMode = mode
			settings.Permissions.RuleSources["defaultMode"] = "cli"
			if permissions.ModeGrantsBypass(opts.permissionMode) {
				settings.Permissions.Bypass = true
			}
		}
	}
	settings.AdditionalDirectories = append(settings.AdditionalDirectories, opts.additionalDirs...)
	return nil
}

func loadSettingsInput(cwd, input string) (config.Settings, error) {
	data, err := runtimeInputBytes(cwd, input)
	if err != nil {
		return config.Settings{}, err
	}
	var settings config.Settings
	if err := json.Unmarshal(data, &settings); err != nil {
		return config.Settings{}, fmt.Errorf("invalid --settings JSON: %w", err)
	}
	return settings, nil
}

func loadMCPConfigInputs(cwd string, inputs []string) (map[string]config.MCPServerConfig, error) {
	servers := map[string]config.MCPServerConfig{}
	for _, input := range inputs {
		loaded, err := loadMCPConfigInput(cwd, input)
		if err != nil {
			return nil, err
		}
		for name, server := range loaded {
			servers[name] = server
		}
	}
	return servers, nil
}

func loadMCPConfigInput(cwd, input string) (map[string]config.MCPServerConfig, error) {
	data, err := runtimeInputBytes(cwd, input)
	if err != nil {
		return nil, err
	}
	var wrapped struct {
		MCPServers map[string]config.MCPServerConfig `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &wrapped); err == nil && wrapped.MCPServers != nil {
		return wrapped.MCPServers, nil
	}
	var bare map[string]config.MCPServerConfig
	if err := json.Unmarshal(data, &bare); err != nil {
		return nil, fmt.Errorf("invalid --mcp-config JSON: %w", err)
	}
	return bare, nil
}

func runtimeInputBytes(cwd, input string) ([]byte, error) {
	trimmed := strings.TrimSpace(input)
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return []byte(trimmed), nil
	}
	path := resolveInputPath(cwd, input)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return data, nil
}

func resolveInputPath(cwd, path string) string {
	if filepath.IsAbs(path) || cwd == "" {
		return path
	}
	return filepath.Join(cwd, path)
}

func filterRuntimeTools(toolList []tools.Tool, opts options) []tools.Tool {
	if !opts.toolsSpecified {
		return toolList
	}
	if len(opts.enabledTools) == 0 {
		return nil
	}
	allowed := map[string]bool{}
	for _, name := range opts.enabledTools {
		allowed[name] = true
	}
	var out []tools.Tool
	for _, tool := range toolList {
		if allowed[tool.Name()] {
			out = append(out, tool)
		}
	}
	return out
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func experimentalAgentTeamsEnabled() bool {
	return firstEnvBool("NIUMA_EXPERIMENTAL_AGENT_TEAMS", "CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS")
}

func mcpCommand(ctx context.Context, args []string, cwd string, stdout io.Writer) error {
	if len(args) == 0 || args[0] == "list" {
		loaded := config.LoadSettings(cwd)
		loaded.MCPServers = mergedMCPServers(cwd, loaded.MCPServers)
		if hasArg(args, "--json") {
			return writePrettyJSON(stdout, loaded.MCPServers)
		}
		if len(loaded.MCPServers) == 0 {
			fmt.Fprintln(stdout, "No MCP servers configured")
			return nil
		}
		for name, server := range loaded.MCPServers {
			kind := server.Type
			if kind == "" {
				kind = "stdio"
			}
			target := server.Command
			if target == "" {
				target = server.URL
			}
			fmt.Fprintf(stdout, "%s\t%s\t%s\n", name, kind, target)
		}
		return nil
	}
	switch args[0] {
	case "show":
		if len(args) < 2 {
			return errors.New("mcp show requires a server name")
		}
		servers := configuredMCPServers(cwd)
		server, ok := servers[args[1]]
		if !ok {
			return fmt.Errorf("mcp server not found: %s", args[1])
		}
		return writePrettyJSON(stdout, server)
	case "add":
		name, server, err := parseMCPAddArgs(args[1:])
		if err != nil {
			return err
		}
		settings := config.LoadGlobalSettings()
		if settings.MCPServers == nil {
			settings.MCPServers = map[string]config.MCPServerConfig{}
		}
		settings.MCPServers[name] = server
		if err := config.SaveGlobalSettings(settings); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Added MCP server %s\n", name)
		return nil
	case "remove", "delete":
		if len(args) < 2 {
			return errors.New("mcp remove requires a name")
		}
		settings := config.LoadGlobalSettings()
		delete(settings.MCPServers, args[1])
		if err := config.SaveGlobalSettings(settings); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Removed MCP server %s\n", args[1])
		return nil
	case "prompts":
		return mcpPromptsCommand(ctx, args[1:], cwd, stdout)
	case "prompt":
		return mcpPromptCommand(ctx, args[1:], cwd, stdout)
	case "resources":
		return mcpResourcesCommand(ctx, args[1:], cwd, stdout)
	case "resource":
		return mcpResourceCommand(ctx, args[1:], cwd, stdout)
	case "tools":
		return mcpToolsCommand(ctx, args[1:], cwd, stdout)
	case "call":
		return mcpCallCommand(ctx, args[1:], cwd, stdout)
	default:
		return fmt.Errorf("unknown mcp command: %s", args[0])
	}
}

func mcpServeCommand(ctx context.Context, args []string, opts options, stdin io.Reader, stdout io.Writer) error {
	for _, arg := range args {
		switch arg {
		case "-d", "--debug", "--verbose":
		default:
			return fmt.Errorf("unknown mcp serve option: %s", arg)
		}
	}
	stdio := mcpserver.NewStdioServer(buildMCPServer(opts))
	return stdio.Listen(ctx, stdin, stdout)
}

func buildMCPServer(opts options) *mcpserver.MCPServer {
	srv := mcpserver.NewMCPServer(
		"niuma",
		versionForDisplay(),
		mcpserver.WithToolCapabilities(true),
		mcpserver.WithRecovery(),
	)
	srv.AddTool(mcpgo.NewToolWithRawSchema("claude_code_query", "Run one niuma query and return the assistant response.", json.RawMessage(`{
		"type":"object",
		"properties":{
			"prompt":{"type":"string","description":"User prompt to send to niuma."},
			"model":{"type":"string","description":"Optional model override."},
			"cwd":{"type":"string","description":"Optional working directory override."},
			"system_prompt":{"type":"string","description":"Optional base system prompt override."},
			"max_tokens":{"type":"integer","description":"Optional maximum output tokens per turn."}
		},
		"required":["prompt"]
	}`)), func(ctx context.Context, request mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		var params server.QueryRequest
		if err := request.BindArguments(&params); err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
		if strings.TrimSpace(params.Prompt) == "" {
			return mcpgo.NewToolResultError("prompt is required"), nil
		}
		queryOpts := opts
		if params.CWD != "" {
			queryOpts.cwd = params.CWD
		}
		if params.Model != "" {
			queryOpts.model = params.Model
		}
		if params.SystemPrompt != "" {
			queryOpts.systemPrompt = params.SystemPrompt
		}
		if params.MaxTokens > 0 {
			queryOpts.maxTokens = params.MaxTokens
		}
		querySession, cleanup, err := newQuerySession(ctx, queryOpts, nil, nil)
		if err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
		defer cleanup()
		result, err := querySession.Run(ctx, params.Prompt, io.Discard)
		if err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
		return mcpgo.NewToolResultText(result.Response), nil
	})
	srv.AddTool(mcpgo.NewToolWithRawSchema("claude_code_status", "Return niuma runtime status for the current workspace.", json.RawMessage(`{"type":"object","properties":{}}`)), func(ctx context.Context, request mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		result, err := mcpgo.NewToolResultJSON(statusPayload(opts.cwd))
		if err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
		return result, nil
	})
	srv.AddTool(mcpgo.NewToolWithRawSchema("claude_code_tools", "List built-in and configured tools available to niuma.", json.RawMessage(`{"type":"object","properties":{}}`)), func(ctx context.Context, request mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		querySession, cleanup, err := newQuerySession(ctx, opts, nil, nil)
		if err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
		defer cleanup()
		result, err := mcpgo.NewToolResultJSON(querySession.ToolDefinitions())
		if err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
		return result, nil
	})
	return srv
}

func parseMCPAddArgs(args []string) (string, config.MCPServerConfig, error) {
	if len(args) < 2 {
		return "", config.MCPServerConfig{}, errors.New("mcp add requires a name and command or --url")
	}
	name := args[0]
	server := config.MCPServerConfig{Type: "stdio"}
	var command []string
	for i := 1; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--type":
			v, err := flagValue(args, &i, "--type")
			if err != nil {
				return "", config.MCPServerConfig{}, err
			}
			server.Type = v
		case "--url":
			v, err := flagValue(args, &i, "--url")
			if err != nil {
				return "", config.MCPServerConfig{}, err
			}
			server.URL = v
		case "--header":
			i++
			if i >= len(args) {
				return "", config.MCPServerConfig{}, errors.New("--header requires key=value")
			}
			key, value, err := parseKeyValue(args[i])
			if err != nil {
				return "", config.MCPServerConfig{}, err
			}
			if server.Headers == nil {
				server.Headers = map[string]string{}
			}
			server.Headers[key] = value
		case "--env":
			i++
			if i >= len(args) {
				return "", config.MCPServerConfig{}, errors.New("--env requires key=value")
			}
			key, value, err := parseKeyValue(args[i])
			if err != nil {
				return "", config.MCPServerConfig{}, err
			}
			if server.Env == nil {
				server.Env = map[string]string{}
			}
			server.Env[key] = value
		case "--":
			command = append(command, args[i+1:]...)
			i = len(args)
		default:
			if strings.HasPrefix(arg, "-") {
				return "", config.MCPServerConfig{}, fmt.Errorf("unknown mcp add option: %s", arg)
			}
			command = append(command, args[i:]...)
			i = len(args)
		}
	}
	if server.Type == "" {
		server.Type = "stdio"
	}
	switch server.Type {
	case "stdio":
		if len(command) == 0 {
			return "", config.MCPServerConfig{}, errors.New("stdio MCP server requires a command")
		}
		server.Command = command[0]
		server.Args = append([]string(nil), command[1:]...)
	case "http", "streamable-http":
		if server.URL == "" {
			return "", config.MCPServerConfig{}, errors.New("http MCP server requires --url")
		}
		if len(command) > 0 {
			return "", config.MCPServerConfig{}, errors.New("http MCP server cannot include a command")
		}
	default:
		return "", config.MCPServerConfig{}, fmt.Errorf("unsupported MCP server type: %s", server.Type)
	}
	return name, server, nil
}

func parseKeyValue(value string) (string, string, error) {
	key, val, ok := strings.Cut(value, "=")
	if !ok || key == "" {
		return "", "", fmt.Errorf("expected key=value: %s", value)
	}
	return key, val, nil
}

func mergedMCPServers(cwd string, settingsServers map[string]config.MCPServerConfig) map[string]config.MCPServerConfig {
	merged := map[string]config.MCPServerConfig{}
	if pluginServers, err := plugins.MCPServers(cwd); err == nil {
		for name, server := range pluginServers {
			merged[name] = server
		}
	}
	for name, server := range settingsServers {
		merged[name] = server
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

func mcpPromptsCommand(ctx context.Context, args []string, cwd string, stdout io.Writer) error {
	servers := configuredMCPServers(cwd)
	if len(servers) == 0 {
		fmt.Fprintln(stdout, "No MCP servers configured")
		return nil
	}
	jsonOut := hasArg(args, "--json")
	serverFilter := firstNonFlag(args)
	if serverFilter != "" {
		if _, ok := servers[serverFilter]; !ok {
			return fmt.Errorf("mcp server not found: %s", serverFilter)
		}
	}

	type promptRecord struct {
		Server string         `json:"server"`
		Prompt mcp.PromptInfo `json:"prompt"`
	}
	var records []promptRecord
	for name, cfg := range servers {
		if serverFilter != "" && name != serverFilter {
			continue
		}
		client, err := mcp.StartConfigured(ctx, name, cfg)
		if err != nil {
			return err
		}
		prompts, err := client.ListPrompts()
		_ = client.Close()
		if err != nil {
			return err
		}
		for _, prompt := range prompts {
			records = append(records, promptRecord{Server: name, Prompt: prompt})
		}
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].Server == records[j].Server {
			return records[i].Prompt.Name < records[j].Prompt.Name
		}
		return records[i].Server < records[j].Server
	})
	if jsonOut {
		return writePrettyJSON(stdout, records)
	}
	if len(records) == 0 {
		fmt.Fprintln(stdout, "No MCP prompts found")
		return nil
	}
	for _, record := range records {
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", record.Server, record.Prompt.Name, record.Prompt.Description)
	}
	return nil
}

func mcpPromptCommand(ctx context.Context, args []string, cwd string, stdout io.Writer) error {
	jsonOut := hasArg(args, "--json")
	positionals := withoutFlags(args, "--json")
	if len(positionals) < 2 {
		return errors.New("mcp prompt requires a server name and prompt name")
	}
	serverName, promptName := positionals[0], positionals[1]
	servers := configuredMCPServers(cwd)
	cfg, ok := servers[serverName]
	if !ok {
		return fmt.Errorf("mcp server not found: %s", serverName)
	}
	arguments := map[string]string{}
	for _, arg := range positionals[2:] {
		key, value, ok := strings.Cut(arg, "=")
		if !ok || key == "" {
			return fmt.Errorf("prompt argument must be key=value: %s", arg)
		}
		arguments[key] = value
	}
	client, err := mcp.StartConfigured(ctx, serverName, cfg)
	if err != nil {
		return err
	}
	defer client.Close()
	result, err := client.GetPrompt(promptName, arguments)
	if err != nil {
		return err
	}
	if jsonOut {
		return writePrettyJSON(stdout, result)
	}
	if result.Description != "" {
		fmt.Fprintf(stdout, "%s\n\n", result.Description)
	}
	for _, message := range result.Messages {
		fmt.Fprintf(stdout, "[%s]\n%s\n\n", message.Role, promptContentText(message.Content))
	}
	return nil
}

func mcpResourcesCommand(ctx context.Context, args []string, cwd string, stdout io.Writer) error {
	servers := configuredMCPServers(cwd)
	if len(servers) == 0 {
		fmt.Fprintln(stdout, "No MCP servers configured")
		return nil
	}
	jsonOut := hasArg(args, "--json")
	serverFilter := firstNonFlag(args)
	if serverFilter != "" {
		if _, ok := servers[serverFilter]; !ok {
			return fmt.Errorf("mcp server not found: %s", serverFilter)
		}
	}
	type resourceRecord struct {
		Server   string           `json:"server"`
		Resource mcp.ResourceInfo `json:"resource"`
	}
	var records []resourceRecord
	for name, cfg := range servers {
		if serverFilter != "" && name != serverFilter {
			continue
		}
		client, err := mcp.StartConfigured(ctx, name, cfg)
		if err != nil {
			return err
		}
		resources, err := client.ListResources()
		_ = client.Close()
		if err != nil {
			return err
		}
		for _, resource := range resources {
			records = append(records, resourceRecord{Server: name, Resource: resource})
		}
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].Server == records[j].Server {
			return records[i].Resource.URI < records[j].Resource.URI
		}
		return records[i].Server < records[j].Server
	})
	if jsonOut {
		return writePrettyJSON(stdout, records)
	}
	if len(records) == 0 {
		fmt.Fprintln(stdout, "No MCP resources found")
		return nil
	}
	for _, record := range records {
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", record.Server, record.Resource.URI, record.Resource.Name)
	}
	return nil
}

func mcpToolsCommand(ctx context.Context, args []string, cwd string, stdout io.Writer) error {
	servers := configuredMCPServers(cwd)
	if len(servers) == 0 {
		fmt.Fprintln(stdout, "No MCP servers configured")
		return nil
	}
	jsonOut := hasArg(args, "--json")
	serverFilter := firstNonFlag(args)
	if serverFilter != "" {
		if _, ok := servers[serverFilter]; !ok {
			return fmt.Errorf("mcp server not found: %s", serverFilter)
		}
	}
	type toolRecord struct {
		Server string       `json:"server"`
		Tool   mcp.ToolInfo `json:"tool"`
	}
	var records []toolRecord
	for name, cfg := range servers {
		if serverFilter != "" && name != serverFilter {
			continue
		}
		client, err := mcp.StartConfigured(ctx, name, cfg)
		if err != nil {
			return err
		}
		tools, err := client.ListTools()
		_ = client.Close()
		if err != nil {
			return err
		}
		for _, tool := range tools {
			records = append(records, toolRecord{Server: name, Tool: tool})
		}
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].Server == records[j].Server {
			return records[i].Tool.Name < records[j].Tool.Name
		}
		return records[i].Server < records[j].Server
	})
	if jsonOut {
		return writePrettyJSON(stdout, records)
	}
	if len(records) == 0 {
		fmt.Fprintln(stdout, "No MCP tools found")
		return nil
	}
	for _, record := range records {
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", record.Server, record.Tool.Name, record.Tool.Description)
	}
	return nil
}

func mcpCallCommand(ctx context.Context, args []string, cwd string, stdout io.Writer) error {
	serverName, toolName, input, jsonOut, err := parseMCPCallArgs(args)
	if err != nil {
		return err
	}
	servers := configuredMCPServers(cwd)
	cfg, ok := servers[serverName]
	if !ok {
		return fmt.Errorf("mcp server not found: %s", serverName)
	}
	client, err := mcp.StartConfigured(ctx, serverName, cfg)
	if err != nil {
		return err
	}
	defer client.Close()
	output, isError, err := client.CallTool(toolName, input)
	if err != nil {
		return err
	}
	if jsonOut {
		return writePrettyJSON(stdout, map[string]any{
			"server":   serverName,
			"tool":     toolName,
			"is_error": isError,
			"output":   output,
		})
	}
	if output != "" {
		fmt.Fprintln(stdout, output)
	}
	if isError {
		return fmt.Errorf("mcp tool %s returned an error", toolName)
	}
	return nil
}

func parseMCPCallArgs(args []string) (serverName string, toolName string, input json.RawMessage, jsonOut bool, err error) {
	var positionals []string
	inputText := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOut = true
		case "--input":
			i++
			if i >= len(args) {
				return "", "", nil, false, errors.New("--input requires a JSON object")
			}
			inputText = args[i]
		default:
			positionals = append(positionals, args[i])
		}
	}
	if len(positionals) < 2 {
		return "", "", nil, false, errors.New("mcp call requires a server name and tool name")
	}
	serverName, toolName = positionals[0], positionals[1]
	if inputText != "" {
		input = json.RawMessage(inputText)
		if !json.Valid(input) {
			return "", "", nil, false, errors.New("--input must be valid JSON")
		}
		return serverName, toolName, input, jsonOut, nil
	}
	argsMap := map[string]any{}
	for _, item := range positionals[2:] {
		key, value, ok := strings.Cut(item, "=")
		if !ok || key == "" {
			return "", "", nil, false, fmt.Errorf("tool argument must be key=value: %s", item)
		}
		argsMap[key] = parseJSONishValue(value)
	}
	data, err := json.Marshal(argsMap)
	if err != nil {
		return "", "", nil, false, err
	}
	return serverName, toolName, json.RawMessage(data), jsonOut, nil
}

func parseJSONishValue(value string) any {
	var decoded any
	if json.Unmarshal([]byte(value), &decoded) == nil {
		return decoded
	}
	return value
}

func mcpResourceCommand(ctx context.Context, args []string, cwd string, stdout io.Writer) error {
	jsonOut := hasArg(args, "--json")
	positionals := withoutFlags(args, "--json")
	if len(positionals) < 2 {
		return errors.New("mcp resource requires a server name and resource uri")
	}
	serverName, uri := positionals[0], positionals[1]
	servers := configuredMCPServers(cwd)
	cfg, ok := servers[serverName]
	if !ok {
		return fmt.Errorf("mcp server not found: %s", serverName)
	}
	client, err := mcp.StartConfigured(ctx, serverName, cfg)
	if err != nil {
		return err
	}
	defer client.Close()
	contents, err := client.ReadResource(uri)
	if err != nil {
		return err
	}
	if jsonOut {
		return writePrettyJSON(stdout, contents)
	}
	for _, content := range contents {
		if content.Text != "" {
			fmt.Fprintln(stdout, content.Text)
		} else if content.Blob != "" {
			fmt.Fprintf(stdout, "[blob %s %d bytes base64]\n", content.MimeType, len(content.Blob))
		}
	}
	return nil
}

func configuredMCPServers(cwd string) map[string]config.MCPServerConfig {
	loaded := config.LoadSettings(cwd)
	return mergedMCPServers(cwd, loaded.MCPServers)
}

func firstNonFlag(args []string) string {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			return arg
		}
	}
	return ""
}

func withoutFlags(args []string, flags ...string) []string {
	flagSet := map[string]bool{}
	for _, flag := range flags {
		flagSet[flag] = true
	}
	var out []string
	for _, arg := range args {
		if flagSet[arg] {
			continue
		}
		out = append(out, arg)
	}
	return out
}

func promptContentText(content mcp.PromptContent) string {
	if content.Text != "" {
		return content.Text
	}
	data, err := json.MarshalIndent(content, "", "  ")
	if err != nil {
		return ""
	}
	return string(data)
}

func (r goalQueryRunner) RunGoalTurn(ctx context.Context, g goalpkg.Goal, prompt string, checkpointName string) (goalpkg.TurnResult, error) {
	opts := r.opts
	opts.cwd = firstNonEmptyString(g.CWD, opts.cwd)
	opts.resume = ""
	opts.sessionID = g.SessionID
	opts.sessionName = ""
	opts.noPersistence = false
	if strings.TrimSpace(g.Model) != "" {
		opts.model = g.Model
		opts.modelExplicit = true
	}
	opts.providerName = firstNonEmptyString(g.Provider, opts.providerName)
	ensureLocalAgentTaskRuntime(&opts)
	initialMessages, err := loadResumeMessages(session.DefaultStore(), g.SessionID, "")
	if err != nil {
		return goalpkg.TurnResult{Error: err}, err
	}
	recorder, err := session.DefaultStore().NewRecorderWithID(opts.cwd, g.SessionID)
	if err != nil {
		return goalpkg.TurnResult{Error: err}, err
	}
	defer func() {
		_ = recorder.Close()
	}()
	checkpoint, err := recorder.Checkpoint(checkpointName, "goal turn checkpoint")
	if err != nil {
		return goalpkg.TurnResult{Error: err}, err
	}
	querySession, cleanup, err := newQuerySession(ctx, opts, initialMessages, recorder)
	if err != nil {
		return goalpkg.TurnResult{Error: err}, err
	}
	defer cleanup()
	var out bytes.Buffer
	result, err := querySession.Run(ctx, prompt, &out)
	now := time.Now().UTC()
	evidence := goalpkg.EvidenceFromToolTraces(g.ID, goalToolEvidenceTraces(result.ToolCalls), now)
	evidence = append(evidence, goalAgentTaskEvidence(ctx, g.ID, opts.agentTaskStore, result.ToolCalls, now)...)
	turnResult := goalpkg.TurnResult{
		Response:          result.Response,
		StopReason:        result.StopReason,
		Turns:             result.Turns,
		InputTokens:       result.Usage.InputTokens,
		OutputTokens:      result.Usage.OutputTokens,
		ToolErrors:        countToolErrors(result.ToolCalls),
		Evidence:          evidence,
		CheckpointName:    checkpoint.Name,
		CheckpointCreated: true,
		Error:             err,
		ContextDone:       errors.Is(err, context.Canceled),
	}
	return turnResult, err
}

func goalToolEvidenceTraces(calls []query.ToolTrace) []goalpkg.ToolEvidenceTrace {
	if len(calls) == 0 {
		return nil
	}
	traces := make([]goalpkg.ToolEvidenceTrace, 0, len(calls))
	for _, call := range calls {
		traces = append(traces, goalpkg.ToolEvidenceTrace{
			ID:      call.ID,
			Name:    call.Name,
			Input:   call.Input,
			Output:  call.Output,
			IsError: call.IsError,
		})
	}
	return traces
}

type goalAgentTaskLister interface {
	ListAgentTasks(ctx context.Context, limit int) ([]mysqlstore.AgentTask, error)
}

func goalAgentTaskEvidence(ctx context.Context, goalID string, store agenttasks.Store, calls []query.ToolTrace, now time.Time) []goalpkg.GoalEvidence {
	if strings.TrimSpace(goalID) == "" || store == nil {
		return nil
	}
	lister, ok := any(store).(goalAgentTaskLister)
	if !ok {
		return nil
	}
	tasks, err := lister.ListAgentTasks(ctx, 20)
	if err != nil || len(tasks) == 0 {
		return nil
	}
	seen := goalAgentGetTaskIDs(calls)
	traces := make([]goalpkg.ToolEvidenceTrace, 0, len(tasks))
	for _, task := range tasks {
		if !goalAgentTaskEvidenceStatus(task.Status) || strings.TrimSpace(task.ResultJSON) == "" || seen[task.ID] {
			continue
		}
		output := goalAgentTaskEvidenceOutput(task)
		if output == "" {
			continue
		}
		traces = append(traces, goalpkg.ToolEvidenceTrace{
			ID:     fmt.Sprintf("agent_task_%d", task.ID),
			Name:   "AgentGet",
			Input:  fmt.Sprintf(`{"task_id":%d,"source":"terminal_agent_task_store"}`, task.ID),
			Output: output,
		})
	}
	return goalpkg.EvidenceFromToolTraces(goalID, traces, now)
}

func goalAgentTaskEvidenceStatus(status string) bool {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case agenttasks.StatusCompleted, agenttasks.StatusFailed, agenttasks.StatusCancelled, agenttasks.StatusTimeout, "canceled":
		return true
	default:
		return false
	}
}

func goalAgentTaskEvidenceOutput(task mysqlstore.AgentTask) string {
	resultJSON := strings.TrimSpace(task.ResultJSON)
	if resultJSON == "" || !json.Valid([]byte(resultJSON)) {
		return ""
	}
	payload := map[string]any{
		"task": map[string]any{
			"id":     task.ID,
			"status": task.Status,
		},
		"result": json.RawMessage(resultJSON),
	}
	out, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return string(out)
}

func goalAgentGetTaskIDs(calls []query.ToolTrace) map[uint64]bool {
	ids := make(map[uint64]bool)
	for _, call := range calls {
		if !strings.EqualFold(strings.TrimSpace(call.Name), "AgentGet") {
			continue
		}
		if id := goalAgentGetTaskIDFromOutput(call.Output); id != 0 {
			ids[id] = true
		}
		if id := goalAgentGetTaskIDFromInput(call.Input); id != 0 {
			ids[id] = true
		}
	}
	return ids
}

func goalAgentGetTaskIDFromOutput(output string) uint64 {
	var decoded struct {
		Task struct {
			ID uint64 `json:"id"`
		} `json:"task"`
	}
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		return 0
	}
	return decoded.Task.ID
}

func goalAgentGetTaskIDFromInput(input string) uint64 {
	var decoded struct {
		TaskID uint64 `json:"task_id"`
		ID     uint64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(input), &decoded); err != nil {
		return 0
	}
	if decoded.TaskID != 0 {
		return decoded.TaskID
	}
	return decoded.ID
}

type goalCLIClassifier struct {
	opts options
}

func (c goalCLIClassifier) ClassifyGoal(ctx context.Context, item goalpkg.Goal, prompt string) (string, error) {
	opts := c.opts
	opts.model = firstNonEmptyString(item.Model, opts.model)
	opts.prompt = ""
	opts.resume = ""
	opts.sessionID = ""
	opts.sessionName = ""
	opts.noPersistence = true
	opts.maxTurns = 1
	opts.maxTokens = 256
	opts.promptMode = promptmode.Chat.String()
	opts.toolsSpecified = true
	opts.enabledTools = nil
	querySession, cleanup, err := newQuerySession(ctx, opts, nil, nil)
	if err != nil {
		return "", err
	}
	defer cleanup()
	var out bytes.Buffer
	result, err := querySession.Run(ctx, prompt, &out)
	if result.Response != "" {
		return result.Response, err
	}
	return out.String(), err
}

func goalEvaluatorForOptions(opts options) goalpkg.Evaluator {
	if strings.EqualFold(strings.TrimSpace(opts.goalEvaluator), "model") {
		return goalpkg.ModelClassifiedEvaluator{Classifier: goalCLIClassifier{opts: opts}}
	}
	return goalpkg.DeterministicEvaluator{}
}

func countToolErrors(calls []query.ToolTrace) int {
	n := 0
	for _, call := range calls {
		if call.IsError {
			n++
		}
	}
	return n
}

func goalCommand(ctx context.Context, args []string, opts options, stdout, stderr io.Writer) error {
	_ = stderr
	if len(args) == 0 {
		err := goalStatusCommand(ctx, nil, stdout)
		if errors.Is(err, errNoActiveGoal) {
			fmt.Fprintln(stdout, goalcmd.HelpText())
			return nil
		}
		return err
	}
	switch args[0] {
	case goalcmd.CommandHelp, goalcmd.AliasHelpShort, goalcmd.AliasHelpLong:
		fmt.Fprintln(stdout, goalcmd.HelpText())
		return nil
	case goalcmd.CommandStart:
		return goalStartCommand(ctx, args[1:], opts, stdout)
	case goalcmd.CommandStatus, goalcmd.AliasInspect:
		return goalStatusCommand(ctx, args, stdout)
	case goalcmd.CommandList, goalcmd.AliasList:
		return goalListCommand(ctx, args[1:], stdout)
	case goalcmd.CommandLogs, goalcmd.AliasLogs:
		return goalLogsCommand(ctx, args[1:], stdout)
	case goalcmd.CommandStop:
		return goalTransitionCommand(ctx, args[1:], goalpkg.StatusStopped, EventTextStopped, stdout)
	case goalcmd.CommandResume:
		return goalResumeCommand(ctx, args[1:], stdout)
	case goalcmd.CommandRun:
		return goalRunCommand(ctx, args[1:], opts, stdout)
	case goalcmd.CommandUnlock:
		return goalUnlockCommand(ctx, args[1:], stdout)
	default:
		return fmt.Errorf("unknown goal command: %s; valid commands: %s; run goal help (or /goal help in the TUI) for examples; 未知 Goal 子命令：%s；%s", args[0], goalcmd.ValidCommandText(), args[0], goalcmd.HelpHintZH)
	}
}

const EventTextStopped = "goal stopped"

func goalStartCommand(ctx context.Context, args []string, opts options, stdout io.Writer) error {
	parsed, err := parseGoalStartArgs(args)
	if err != nil {
		return err
	}
	if parsed.objective == "" {
		return fmt.Errorf(`goal start requires an objective; example: goal start "check project tests and fix failures"; 中文：goal start 需要提供目标；%s`, goalcmd.HelpHintZH)
	}
	goalCWD := firstNonEmptyString(parsed.cwd, opts.cwd)
	sessionID := parsed.sessionID
	if sessionID == "" {
		recorder, err := session.DefaultStore().NewRecorder(goalCWD)
		if err != nil {
			return err
		}
		sessionID = recorder.SessionID
		if err := recorder.Append(session.Entry{Type: "session", Name: "Goal: " + shortText(parsed.objective, 80)}); err != nil {
			_ = recorder.Close()
			return err
		}
		_ = recorder.Close()
	}
	goal, err := goalpkg.DefaultStore().Create(ctx, goalpkg.CreateInput{
		Objective:   parsed.objective,
		SessionID:   sessionID,
		CWD:         goalCWD,
		Model:       firstNonEmptyString(parsed.model, opts.model),
		Provider:    opts.providerName,
		TurnBudget:  parsed.turnBudget,
		TokenBudget: parsed.tokenBudget,
	})
	if err != nil {
		return err
	}
	if parsed.jsonOut {
		return writePrettyJSON(stdout, goal)
	}
	fmt.Fprintf(stdout, "Started goal %s\nSession: %s\nStatus: %s\n", goal.ID, goal.SessionID, goal.Status)
	return nil
}

type parsedGoalStart struct {
	objective   string
	sessionID   string
	cwd         string
	model       string
	turnBudget  int
	tokenBudget int
	jsonOut     bool
}

func parseGoalStartArgs(args []string) (parsedGoalStart, error) {
	parsed := parsedGoalStart{}
	var objective []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--turn-budget":
			v, err := flagValue(args, &i, "--turn-budget")
			if err != nil {
				return parsed, err
			}
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return parsed, errors.New("--turn-budget must be a positive integer")
			}
			parsed.turnBudget = n
		case "--token-budget":
			v, err := flagValue(args, &i, "--token-budget")
			if err != nil {
				return parsed, err
			}
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return parsed, errors.New("--token-budget must be a positive integer")
			}
			parsed.tokenBudget = n
		case "--session":
			v, err := flagValue(args, &i, "--session")
			if err != nil {
				return parsed, err
			}
			parsed.sessionID = v
		case "--cwd":
			v, err := flagValue(args, &i, "--cwd")
			if err != nil {
				return parsed, err
			}
			parsed.cwd = v
		case "--model":
			v, err := flagValue(args, &i, "--model")
			if err != nil {
				return parsed, err
			}
			parsed.model = v
		case "--json":
			parsed.jsonOut = true
		default:
			objective = append(objective, args[i])
		}
	}
	parsed.objective = strings.TrimSpace(strings.Join(objective, " "))
	return parsed, nil
}

func goalStatusCommand(ctx context.Context, args []string, stdout io.Writer) error {
	jsonOut := hasArg(args, "--json")
	id := ""
	positionals := withoutFlags(args, "--json")
	if len(positionals) > 0 && (positionals[0] == goalcmd.CommandStatus || positionals[0] == goalcmd.AliasInspect) {
		positionals = positionals[1:]
	}
	if len(positionals) > 0 {
		id = positionals[0]
	}
	store := goalpkg.DefaultStore()
	goal, err := resolveGoal(ctx, store, id)
	if err != nil {
		return err
	}
	if jsonOut {
		return writePrettyJSON(stdout, goal)
	}
	printGoalSummary(stdout, goal)
	printGoalPlanSummary(ctx, stdout, store, goal.ID)
	printGoalEvidenceSummary(ctx, stdout, store, goal.ID, 3)
	return nil
}

func goalListCommand(ctx context.Context, args []string, stdout io.Writer) error {
	jsonOut := hasArg(args, "--json")
	filter := goalpkg.ListFilter{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
		case "--active":
			filter.Active = true
		case "--status":
			v, err := flagValue(args, &i, "--status")
			if err != nil {
				return err
			}
			filter.Status = goalpkg.Status(v)
			if !filter.Status.Valid() {
				return fmt.Errorf("invalid goal status: %s", v)
			}
		default:
			return fmt.Errorf("unknown goal list option: %s", args[i])
		}
	}
	goals, err := goalpkg.DefaultStore().List(ctx, filter)
	if err != nil {
		return err
	}
	if jsonOut {
		return writePrettyJSON(stdout, goals)
	}
	if len(goals) == 0 {
		fmt.Fprintln(stdout, "No goals found")
		return nil
	}
	for _, goal := range goals {
		fmt.Fprintf(stdout, "%s\t%s\tturns %d/%d\ttokens %d/%d\t%s\n",
			goal.ID, goal.Status, goal.TurnsUsed, goal.TurnBudget, goal.InputTokens+goal.OutputTokens, goal.TokenBudget, shortText(goal.Objective, 80))
	}
	return nil
}

func goalLogsCommand(ctx context.Context, args []string, stdout io.Writer) error {
	jsonOut := hasArg(args, "--json")
	limit := 50
	var id string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
		case "--limit":
			v, err := flagValue(args, &i, "--limit")
			if err != nil {
				return err
			}
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return errors.New("--limit must be a positive integer")
			}
			limit = n
		default:
			if id == "" {
				id = args[i]
			} else {
				return fmt.Errorf("unknown goal logs argument: %s", args[i])
			}
		}
	}
	store := goalpkg.DefaultStore()
	goal, err := resolveGoal(ctx, store, id)
	if err != nil {
		return err
	}
	events, err := store.ListEvents(ctx, goal.ID, limit)
	if err != nil {
		return err
	}
	if jsonOut {
		return writePrettyJSON(stdout, events)
	}
	if len(events) == 0 {
		fmt.Fprintln(stdout, "No goal events found")
		return nil
	}
	for _, event := range events {
		fmt.Fprintf(stdout, "%s\tturn=%d\t%s\tstatus=%s\t%s\n",
			event.CreatedAt.Format("2006-01-02 15:04:05"), event.Turn, event.Type, event.Status, firstNonEmptyString(event.Message, event.Reason))
	}
	printGoalEvidenceSummary(ctx, stdout, store, goal.ID, 3)
	return nil
}

func goalTransitionCommand(ctx context.Context, args []string, status goalpkg.Status, message string, stdout io.Writer) error {
	jsonOut := hasArg(args, "--json")
	positionals := withoutFlags(args, "--json")
	id := ""
	if len(positionals) > 0 {
		id = positionals[0]
	}
	store := goalpkg.DefaultStore()
	goal, err := resolveGoal(ctx, store, id)
	if err != nil {
		return err
	}
	goal.Status = status
	goal.UpdatedAt = time.Now().UTC()
	if status == goalpkg.StatusStopped {
		goal.LastReason = "stopped by user"
	}
	if err := store.Update(ctx, goal); err != nil {
		return err
	}
	eventType := goalpkg.EventStatusChanged
	if status == goalpkg.StatusStopped {
		eventType = goalpkg.EventGoalStopped
	}
	event, err := goalpkg.NewEvent(goalpkg.EventInput{GoalID: goal.ID, Type: eventType, Message: message, SessionID: goal.SessionID, Status: goal.Status, Reason: goal.LastReason})
	if err != nil {
		return err
	}
	if err := store.AppendEvent(ctx, event); err != nil {
		return err
	}
	if jsonOut {
		return writePrettyJSON(stdout, goal)
	}
	fmt.Fprintf(stdout, "Goal %s is %s\n", goal.ID, goal.Status)
	return nil
}

func goalResumeCommand(ctx context.Context, args []string, stdout io.Writer) error {
	jsonOut := hasArg(args, "--json")
	force := hasArg(args, "--force")
	positionals := withoutFlags(args, "--json", "--force")
	id := ""
	if len(positionals) > 0 {
		id = positionals[0]
	}
	store := goalpkg.DefaultStore()
	goal, err := resolveResumableGoal(ctx, store, id)
	if err != nil {
		return err
	}
	if goal.Status == goalpkg.StatusComplete {
		return fmt.Errorf("goal %s is complete and cannot be resumed", goal.ID)
	}
	if goal.Status == goalpkg.StatusFailed && !force {
		return fmt.Errorf("goal %s is failed; use --force to resume", goal.ID)
	}
	goal.Status = goalpkg.StatusActive
	goal.Error = ""
	goal.LastReason = "resumed by user"
	goal.UpdatedAt = time.Now().UTC()
	if err := store.Update(ctx, goal); err != nil {
		return err
	}
	event, err := goalpkg.NewEvent(goalpkg.EventInput{GoalID: goal.ID, Type: goalpkg.EventGoalResumed, Message: "goal resumed", SessionID: goal.SessionID, Status: goal.Status, Reason: goal.LastReason})
	if err != nil {
		return err
	}
	if err := store.AppendEvent(ctx, event); err != nil {
		return err
	}
	if jsonOut {
		return writePrettyJSON(stdout, goal)
	}
	fmt.Fprintf(stdout, "Resumed goal %s\n", goal.ID)
	return nil
}

func goalRunCommand(ctx context.Context, args []string, opts options, stdout io.Writer) error {
	once := hasArg(args, "--once")
	backgroundRun := hasArg(args, "--background") || hasArg(args, "--bg")
	jsonOut := hasArg(args, "--json")
	positionals := withoutFlags(args, "--once", "--json", "--background", "--bg")
	if len(positionals) == 0 {
		return fmt.Errorf("goal run requires a goal id; use goal list to find one; 中文：goal run 需要 goal id；%s", goalcmd.HelpHintZH)
	}
	id := positionals[0]
	if once && backgroundRun {
		return errors.New("goal run cannot combine --once and --background")
	}
	if backgroundRun {
		bgOpts := background.Options{
			Prompt:          id,
			CWD:             opts.cwd,
			Kind:            "goal",
			Model:           opts.model,
			Provider:        opts.providerName,
			GoalEvaluator:   opts.goalEvaluator,
			OutputFormat:    firstNonEmptyString(opts.outputFormat, "text"),
			MaxTurns:        opts.maxTurns,
			MaxTokens:       opts.maxTokens,
			Resume:          opts.resume,
			SessionID:       opts.sessionID,
			SessionName:     opts.sessionName,
			NoPersistence:   opts.noPersistence,
			SystemPrompt:    opts.systemPrompt,
			AppendSystem:    opts.appendSystem,
			AllowedTools:    opts.allowedTools,
			DeniedTools:     opts.deniedTools,
			PermissionMode:  opts.permissionMode,
			AdditionalDirs:  opts.additionalDirs,
			SkipPermissions: opts.skipPermissions,
		}
		if jsonOut {
			job, err := startBackgroundJob(io.Discard, bgOpts, "__goal-run")
			if err != nil {
				return err
			}
			return writePrettyJSON(stdout, job)
		}
		_, err := startBackgroundJob(stdout, bgOpts, "__goal-run")
		if err != nil {
			return err
		}
		return nil
	}
	runner := goalpkg.Runner{
		Store:     goalpkg.DefaultStore(),
		Query:     goalQueryRunner{opts: opts},
		Evaluator: goalEvaluatorForOptions(opts),
	}
	if once {
		result, err := runner.RunOnce(ctx, id)
		if err != nil {
			return err
		}
		if jsonOut {
			return writePrettyJSON(stdout, result)
		}
		if result.Event.Type == goalpkg.EventTurnFailed {
			return fmt.Errorf("goal %s turn failed: %s", result.Goal.ID, firstNonEmptyString(result.Event.Error, result.Event.Message))
		}
		fmt.Fprintf(stdout, "Ran goal %s once\n", result.Goal.ID)
		printGoalSummary(stdout, result.Goal)
		return nil
	}
	goal, err := runner.RunUntilStop(ctx, id)
	if err != nil {
		return err
	}
	if jsonOut {
		return writePrettyJSON(stdout, goal)
	}
	fmt.Fprintf(stdout, "Goal %s stopped at %s\n", goal.ID, goal.Status)
	return nil
}

func goalBackgroundRunCommand(ctx context.Context, args []string, opts options, stdout, stderr io.Writer) error {
	_ = stderr
	if len(args) < 1 {
		return errors.New("__goal-run requires a background session id")
	}
	store := background.DefaultStore()
	job, ok, err := store.MarkRunning(args[0], os.Getpid())
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("background session not found: %s", args[0])
	}
	if job.Status == "killed" {
		return nil
	}
	goalID := strings.TrimSpace(job.Prompt)
	if goalID == "" {
		err := errors.New("goal background job is missing goal id")
		_, _, _ = store.Finish(job.ID, 1, err.Error())
		return err
	}
	runOpts := opts
	runOpts.cwd = firstNonEmptyString(job.CWD, opts.cwd)
	runOpts.model = firstNonEmptyString(job.Model, opts.model)
	runOpts.modelExplicit = strings.TrimSpace(job.Model) != ""
	runOpts.providerName = firstNonEmptyString(job.Provider, opts.providerName)
	runOpts.goalEvaluator = firstNonEmptyString(job.GoalEvaluator, opts.goalEvaluator)
	runOpts.outputFormat = firstNonEmptyString(job.OutputFormat, opts.outputFormat)
	runOpts.maxTurns = job.MaxTurns
	runOpts.maxTokens = job.MaxTokens
	runOpts.resume = job.Resume
	runOpts.sessionID = job.SessionID
	runOpts.sessionName = job.SessionName
	runOpts.noPersistence = job.NoPersistence
	runOpts.systemPrompt = job.SystemPrompt
	runOpts.appendSystem = job.AppendSystem
	runOpts.allowedTools = job.AllowedTools
	runOpts.deniedTools = job.DeniedTools
	runOpts.permissionMode = job.PermissionMode
	runOpts.additionalDirs = job.AdditionalDirs
	runOpts.skipPermissions = job.SkipPermissions
	runOpts.model = config.ResolveModel(runOpts.cwd, runOpts.model)
	runner := goalpkg.Runner{
		Store:     goalpkg.DefaultStore(),
		Query:     goalQueryRunner{opts: runOpts},
		Evaluator: goalEvaluatorForOptions(runOpts),
	}
	fmt.Fprintf(stdout, "Goal background run %s started for %s\n", job.ID, goalID)
	finalGoal, err := runner.RunUntilStop(ctx, goalID)
	if err != nil {
		fmt.Fprintf(stdout, "Goal background run %s failed: %v\n", job.ID, err)
		_, _, _ = store.Finish(job.ID, 1, err.Error())
		return err
	}
	fmt.Fprintf(stdout, "Goal background run %s finished with status %s\n", job.ID, finalGoal.Status)
	if finalGoal.Status == goalpkg.StatusFailed {
		errText := firstNonEmptyString(finalGoal.Error, finalGoal.LastReason, "goal failed")
		_, _, _ = store.Finish(job.ID, 1, errText)
		return fmt.Errorf("goal %s failed: %s", finalGoal.ID, errText)
	}
	_, _, err = store.Finish(job.ID, 0, "")
	return err
}

func goalUnlockCommand(ctx context.Context, args []string, stdout io.Writer) error {
	_ = ctx
	force := hasArg(args, "--force")
	positionals := withoutFlags(args, "--force")
	if len(positionals) == 0 {
		return fmt.Errorf("goal unlock requires a goal id; 中文：goal unlock 需要 goal id；%s", goalcmd.HelpHintZH)
	}
	if !force {
		return fmt.Errorf("goal unlock requires --force; 中文：goal unlock 需要 --force；%s", goalcmd.HelpHintZH)
	}
	id := positionals[0]
	if err := goalpkg.DefaultStore().ForceUnlockGoal(id); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Unlocked goal %s\n", id)
	return nil
}

var errNoActiveGoal = errors.New("no active goal found")

func resolveGoal(ctx context.Context, store goalpkg.Store, id string) (goalpkg.Goal, error) {
	id = strings.TrimSpace(id)
	if id != "" {
		return store.Get(ctx, id)
	}
	active, err := store.List(ctx, goalpkg.ListFilter{Active: true})
	if err != nil {
		return goalpkg.Goal{}, err
	}
	if len(active) == 0 {
		return goalpkg.Goal{}, fmt.Errorf(`%w; create one with goal start "<objective>" or inspect saved goals with goal list`, errNoActiveGoal)
	}
	return active[0], nil
}

func resolveResumableGoal(ctx context.Context, store goalpkg.Store, id string) (goalpkg.Goal, error) {
	id = strings.TrimSpace(id)
	if id != "" {
		return store.Get(ctx, id)
	}
	active, err := store.List(ctx, goalpkg.ListFilter{Active: true})
	if err != nil {
		return goalpkg.Goal{}, err
	}
	if len(active) > 0 {
		return active[0], nil
	}
	all, err := store.List(ctx, goalpkg.ListFilter{})
	if err != nil {
		return goalpkg.Goal{}, err
	}
	for _, goal := range all {
		switch goal.Status {
		case goalpkg.StatusStopped, goalpkg.StatusBlocked, goalpkg.StatusFailed:
			return goal, nil
		}
	}
	return goalpkg.Goal{}, errors.New("no resumable goal found")
}

func printGoalSummary(stdout io.Writer, goal goalpkg.Goal) {
	fmt.Fprintf(stdout, "Goal %s\n", goal.ID)
	fmt.Fprintf(stdout, "Status: %s\n", goal.Status)
	fmt.Fprintf(stdout, "Session: %s\n", goal.SessionID)
	fmt.Fprintf(stdout, "Turns: %d/%d\n", goal.TurnsUsed, goal.TurnBudget)
	fmt.Fprintf(stdout, "Tokens: %d/%d\n", goal.InputTokens+goal.OutputTokens, goal.TokenBudget)
	if goal.LastCheckpoint != "" {
		fmt.Fprintf(stdout, "Last checkpoint: %s\n", goal.LastCheckpoint)
	}
	if goal.LastReason != "" {
		fmt.Fprintf(stdout, "Reason: %s\n", goal.LastReason)
	}
	if goal.LastNextAction != "" {
		fmt.Fprintf(stdout, "Next action: %s\n", goal.LastNextAction)
	}
	if goal.LastBlocker != "" {
		fmt.Fprintf(stdout, "Blocker: %s (%d)\n", goal.LastBlocker, goal.RepeatedBlockerCount)
	}
	fmt.Fprintf(stdout, "Objective: %s\n", goal.Objective)
}

func printGoalPlanSummary(ctx context.Context, stdout io.Writer, store goalpkg.Store, goalID string) {
	planStore, ok := store.(goalpkg.PlanStore)
	if !ok {
		return
	}
	plan, ok, err := planStore.GetPlan(ctx, goalID)
	if err != nil || !ok {
		return
	}
	if current, ok := cliCurrentPlanStep(plan); ok {
		fmt.Fprintf(stdout, "Current step: %s [%s]\n", current.Title, current.Status)
	}
	if len(plan.AcceptanceCriteria) > 0 {
		passed, requiredDone, requiredTotal := cliCriterionProgress(plan.AcceptanceCriteria)
		fmt.Fprintf(stdout, "Criteria: %d/%d passed", passed, len(plan.AcceptanceCriteria))
		if requiredTotal > 0 {
			fmt.Fprintf(stdout, " (required %d/%d)", requiredDone, requiredTotal)
		}
		fmt.Fprintln(stdout)
		for _, criterion := range plan.AcceptanceCriteria {
			required := "optional"
			if criterion.Required {
				required = "required"
			}
			fmt.Fprintf(stdout, "- %s [%s %s]: %s\n", criterion.ID, required, criterion.Status, criterion.Description)
		}
	}
}

func printGoalEvidenceSummary(ctx context.Context, stdout io.Writer, store goalpkg.Store, goalID string, limit int) {
	planStore, ok := store.(goalpkg.PlanStore)
	if !ok {
		return
	}
	evidence, err := planStore.ListEvidence(ctx, goalID, limit)
	if err != nil || len(evidence) == 0 {
		return
	}
	fmt.Fprintf(stdout, "Recent evidence (%d):\n", len(evidence))
	for _, item := range evidence {
		result := "fail"
		if item.Passed {
			result = "pass"
		}
		command := ""
		if strings.TrimSpace(item.Command) != "" {
			command = " - " + shortText(item.Command, 80)
		}
		fmt.Fprintf(stdout, "- %s [%s %s]: %s%s\n", item.ID, item.Type, result, shortText(item.Summary, 120), command)
	}
}

func cliCurrentPlanStep(plan goalpkg.GoalPlan) (goalpkg.GoalStep, bool) {
	if plan.CurrentStepID != "" {
		for _, step := range plan.Steps {
			if step.ID == plan.CurrentStepID {
				return step, true
			}
		}
	}
	for _, step := range plan.Steps {
		if step.Status == goalpkg.StepStatusActive {
			return step, true
		}
	}
	return goalpkg.GoalStep{}, false
}

func cliCriterionProgress(criteria []goalpkg.GoalCriterion) (passed int, requiredDone int, requiredTotal int) {
	for _, criterion := range criteria {
		done := criterion.Status == goalpkg.CriterionStatusPassed || criterion.Status == goalpkg.CriterionStatusWaived
		if done {
			passed++
		}
		if !criterion.Required {
			continue
		}
		requiredTotal++
		if done {
			requiredDone++
		}
	}
	return passed, requiredDone, requiredTotal
}

func shortText(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if limit <= 0 || len([]rune(text)) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit-3]) + "..."
}

func pluginsCommand(args []string, cwd string, stdout io.Writer) error {
	if len(args) > 0 && args[0] == "install" {
		if len(args) < 2 {
			return errors.New("plugin install requires a local plugin directory")
		}
		installOpts := plugins.InstallOptions{}
		source := args[1]
		for i := 2; i < len(args); i++ {
			switch args[i] {
			case "--name":
				v, err := flagValue(args, &i, "--name")
				if err != nil {
					return err
				}
				installOpts.Name = v
			case "--project":
				installOpts.Project = true
			case "--force":
				installOpts.Force = true
			default:
				return fmt.Errorf("unknown plugin install option: %s", args[i])
			}
		}
		manifest, err := plugins.InstallLocal(cwd, source, installOpts)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Installed plugin %s\n", manifest.Name)
		return nil
	}
	if len(args) > 0 && (args[0] == "remove" || args[0] == "uninstall" || args[0] == "delete") {
		if len(args) < 2 {
			return errors.New("plugin remove requires a plugin name")
		}
		project := hasArg(args[2:], "--project")
		if err := plugins.Uninstall(cwd, args[1], project); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Removed plugin %s\n", args[1])
		return nil
	}
	if len(args) > 0 && args[0] == "show" {
		if len(args) < 2 {
			return errors.New("plugin show requires a plugin name")
		}
		manifest, ok, err := plugins.Find(cwd, args[1])
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("plugin not found: %s", args[1])
		}
		return writePrettyJSON(stdout, manifest)
	}
	if len(args) > 0 && args[0] != "list" {
		return fmt.Errorf("unknown plugin command: %s", args[0])
	}
	list, err := plugins.List(cwd)
	if err != nil {
		return err
	}
	if hasArg(args, "--json") {
		return writePrettyJSON(stdout, list)
	}
	if len(list) == 0 {
		fmt.Fprintln(stdout, "No plugins found")
		return nil
	}
	for _, item := range list {
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", item.Name, item.Version, item.Description)
	}
	return nil
}

func tenantCommand(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("tenant requires a subcommand: migrate, agent-tasks, or skill-package")
	}
	switch args[0] {
	case "migrate", "migration", "migrations":
		return tenantMigrateCommand(args[1:], stdout)
	case "agent-tasks", "agent-task", "tasks":
		return tenantAgentTasksCommand(ctx, args[1:], stdout)
	case "skill-package", "skill-packages", "package":
		return tenantSkillPackageCommand(ctx, args[1:], stdout)
	default:
		return fmt.Errorf("unknown tenant command: %s", args[0])
	}
}

type tenantSkillPackageOptions struct {
	dsn         string
	tenantKey   string
	userID      string
	traceID     string
	skillKey    string
	name        string
	description string
	storeRoot   string
	version     uint
	enabled     *bool
	jsonOut     bool
}

type tenantSkillPackageService interface {
	RenderSkillPackage(ctx context.Context, req tenantservice.SkillPackageRequest) (tenantservice.SkillPackageResult, error)
	PublishSkillPackage(ctx context.Context, req tenantservice.SkillPackageRequest) (tenantservice.SkillPackageResult, error)
	ListSkills(ctx context.Context, enabledOnly bool, limit int) ([]mysqlstore.Skill, error)
	GetSkill(ctx context.Context, skillKey string, version uint) (mysqlstore.Skill, error)
	RollbackSkill(ctx context.Context, req tenantservice.SkillRollbackRequest) (tenantservice.SkillRollbackResult, error)
}

var newTenantSkillPackageService = func(ctx context.Context, dsn string) (tenantSkillPackageService, func() error, error) {
	repo, err := mysqlstore.OpenGormRepository(ctx, dsn, nil)
	if err != nil {
		return nil, nil, err
	}
	return tenantservice.NewService(repo, nil), repo.Close, nil
}

func tenantSkillPackageCommand(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("tenant skill-package requires import, render, publish, list, history, or rollback")
	}
	action := args[0]
	if action != "import" && action != "render" && action != "publish" && action != "list" && action != "history" && action != "rollback" {
		return fmt.Errorf("unknown tenant skill-package command: %s", action)
	}
	opts, positionals, err := parseTenantSkillPackageOptions(args[1:])
	if err != nil {
		return err
	}
	if (action == "import" || action == "render" || action == "publish") && len(positionals) < 1 {
		return errors.New("tenant skill-package requires a source path")
	}
	svc, pkgCtx, cleanup, err := openTenantSkillPackageService(ctx, opts)
	if err != nil {
		return err
	}
	defer cleanup()
	req := tenantservice.SkillPackageRequest{
		SkillKey:    opts.skillKey,
		Name:        opts.name,
		Description: opts.description,
		Version:     opts.version,
		Enabled:     opts.enabled,
		StoreRoot:   opts.storeRoot,
	}
	if len(positionals) > 0 {
		req.SourcePath = positionals[0]
	}
	var result tenantservice.SkillPackageResult
	switch action {
	case "import", "render":
		result, err = svc.RenderSkillPackage(pkgCtx, req)
	case "publish":
		result, err = svc.PublishSkillPackage(pkgCtx, req)
	case "list":
		items, listErr := svc.ListSkills(pkgCtx, false, 100)
		if listErr != nil {
			return listErr
		}
		if opts.jsonOut {
			return writePrettyJSON(stdout, map[string]any{"data": items})
		}
		for _, item := range items {
			fmt.Fprintf(stdout, "%s\tv%d\t%s\t%s\n", item.SkillKey, item.Version, item.PackageSHA256, item.PackageRef)
		}
		return nil
	case "history":
		if opts.skillKey == "" {
			return errors.New("tenant skill-package history requires --skill-key")
		}
		items, listErr := svc.ListSkills(pkgCtx, false, 200)
		if listErr != nil {
			return listErr
		}
		var filtered []mysqlstore.Skill
		for _, item := range items {
			if item.SkillKey == opts.skillKey {
				filtered = append(filtered, item)
			}
		}
		if opts.jsonOut {
			return writePrettyJSON(stdout, map[string]any{"data": filtered})
		}
		for _, item := range filtered {
			fmt.Fprintf(stdout, "%s\tv%d\t%s\t%s\n", item.SkillKey, item.Version, item.PackageSHA256, item.PackageRef)
		}
		return nil
	case "rollback":
		if opts.skillKey == "" || opts.version == 0 {
			return errors.New("tenant skill-package rollback requires --skill-key and --version")
		}
		rollback, rollbackErr := svc.RollbackSkill(pkgCtx, tenantservice.SkillRollbackRequest{SkillKey: opts.skillKey, Version: opts.version})
		if rollbackErr != nil {
			return rollbackErr
		}
		if opts.jsonOut {
			return writePrettyJSON(stdout, rollback)
		}
		fmt.Fprintf(stdout, "Rolled back %s v%d into v%d\n", rollback.SkillKey, rollback.FromVersion, rollback.Version)
		return nil
	}
	if err != nil {
		return err
	}
	if opts.jsonOut {
		return writePrettyJSON(stdout, result)
	}
	fmt.Fprintf(stdout, "%s %s sha=%s runtime_bytes=%d\n", titleWord(action), result.SkillKey, result.PackageSHA256, len(result.RuntimeMD))
	if action == "publish" {
		fmt.Fprintf(stdout, "Published version=%d package_ref=%s runtime_ref=%s\n", result.Version, result.PackageRef, result.RuntimeRef)
	}
	return nil
}

func parseTenantSkillPackageOptions(args []string) (tenantSkillPackageOptions, []string, error) {
	opts := tenantSkillPackageOptions{
		dsn:       firstEnv("NIUMA_MYSQL_DSN", "MYSQL_DSN"),
		tenantKey: firstEnv("NIUMA_TENANT_KEY", "TENANT_KEY"),
		userID:    firstEnv("NIUMA_USER_ID", "USER_ID"),
		traceID:   firstEnv("NIUMA_TRACE_ID", "TRACE_ID"),
		storeRoot: firstEnv("NIUMA_TENANT_SKILL_PACKAGE_DIR"),
	}
	var positionals []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dsn":
			v, err := flagValue(args, &i, "--dsn")
			if err != nil {
				return opts, nil, err
			}
			opts.dsn = v
		case "--tenant-key":
			v, err := flagValue(args, &i, "--tenant-key")
			if err != nil {
				return opts, nil, err
			}
			opts.tenantKey = v
		case "--user-id", "--user-key":
			v, err := flagValue(args, &i, "--user-id")
			if err != nil {
				return opts, nil, err
			}
			opts.userID = v
		case "--trace-id":
			v, err := flagValue(args, &i, "--trace-id")
			if err != nil {
				return opts, nil, err
			}
			opts.traceID = v
		case "--skill-key":
			v, err := flagValue(args, &i, "--skill-key")
			if err != nil {
				return opts, nil, err
			}
			opts.skillKey = v
		case "--name":
			v, err := flagValue(args, &i, "--name")
			if err != nil {
				return opts, nil, err
			}
			opts.name = v
		case "--description":
			v, err := flagValue(args, &i, "--description")
			if err != nil {
				return opts, nil, err
			}
			opts.description = v
		case "--store-root":
			v, err := flagValue(args, &i, "--store-root")
			if err != nil {
				return opts, nil, err
			}
			opts.storeRoot = v
		case "--version":
			v, err := flagValue(args, &i, "--version")
			if err != nil {
				return opts, nil, err
			}
			parsed, err := strconv.ParseUint(v, 10, 32)
			if err != nil || parsed == 0 {
				return opts, nil, errors.New("--version must be a positive integer")
			}
			opts.version = uint(parsed)
		case "--enabled":
			value := true
			opts.enabled = &value
		case "--disabled":
			value := false
			opts.enabled = &value
		case "--json":
			opts.jsonOut = true
		default:
			if strings.HasPrefix(args[i], "-") {
				return opts, nil, fmt.Errorf("unknown tenant skill-package option: %s", args[i])
			}
			positionals = append(positionals, args[i])
		}
	}
	if opts.dsn == "" {
		return opts, nil, errors.New("tenant skill-package requires --dsn or NIUMA_MYSQL_DSN")
	}
	if opts.tenantKey == "" {
		return opts, nil, errors.New("tenant skill-package requires --tenant-key or NIUMA_TENANT_KEY")
	}
	if opts.userID == "" {
		return opts, nil, errors.New("tenant skill-package requires --user-id or NIUMA_USER_ID")
	}
	return opts, positionals, nil
}

func openTenantSkillPackageService(ctx context.Context, opts tenantSkillPackageOptions) (tenantSkillPackageService, context.Context, func(), error) {
	svc, closeFn, err := newTenantSkillPackageService(ctx, opts.dsn)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open tenant mysql gorm repository: %w", err)
	}
	if closeFn == nil {
		closeFn = func() error { return nil }
	}
	pkgCtx := observability.WithRequestValues(ctx, opts.traceID, opts.userID, opts.tenantKey)
	return svc, pkgCtx, func() { _ = closeFn() }, nil
}

func titleWord(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

type tenantAgentTasksOptions struct {
	dsn       string
	tenantKey string
	userID    string
	traceID   string
	limit     int
	jsonOut   bool
	reason    string
}

type tenantAgentTaskService interface {
	ListAgentTasks(ctx context.Context, limit int) ([]mysqlstore.AgentTask, error)
	ListAgentTaskEvents(ctx context.Context, taskID uint64, limit int) ([]mysqlstore.AgentTaskEvent, error)
	CancelAgentTask(ctx context.Context, taskID uint64, resultJSON string) error
}

var newTenantAgentTaskService = func(ctx context.Context, dsn string) (tenantAgentTaskService, func() error, error) {
	repo, err := mysqlstore.OpenGormRepository(ctx, dsn, nil)
	if err != nil {
		return nil, nil, err
	}
	return tenantservice.NewService(repo, nil), repo.Close, nil
}

func tenantAgentTasksCommand(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("tenant agent-tasks requires list, events, or cancel")
	}
	action := args[0]
	if isUintString(action) && len(args) > 1 {
		action = args[1]
		args = append([]string{action, args[0]}, args[2:]...)
	}
	switch action {
	case "list":
		opts, _, err := parseTenantAgentTasksOptions(args[1:])
		if err != nil {
			return err
		}
		svc, taskCtx, cleanup, err := openTenantAgentTaskService(ctx, opts)
		if err != nil {
			return err
		}
		defer cleanup()
		items, err := svc.ListAgentTasks(taskCtx, opts.limit)
		if err != nil {
			return err
		}
		if opts.jsonOut {
			return writePrettyJSON(stdout, map[string]any{"data": items})
		}
		if len(items) == 0 {
			fmt.Fprintln(stdout, "No agent tasks found")
			return nil
		}
		for _, item := range items {
			fmt.Fprintf(stdout, "%d\t%s\t%s\t%s\t%s\t%s\n", item.ID, item.Status, item.AgentName, item.Model, item.StartedAt.Format("2006-01-02 15:04:05"), item.Description)
		}
		return nil
	case "events":
		opts, positionals, err := parseTenantAgentTasksOptions(args[1:])
		if err != nil {
			return err
		}
		if len(positionals) < 1 {
			return errors.New("tenant agent-tasks events requires a task id")
		}
		taskID, err := parseUintArg(positionals[0], "task id")
		if err != nil {
			return err
		}
		svc, taskCtx, cleanup, err := openTenantAgentTaskService(ctx, opts)
		if err != nil {
			return err
		}
		defer cleanup()
		items, err := svc.ListAgentTaskEvents(taskCtx, taskID, opts.limit)
		if err != nil {
			return err
		}
		if opts.jsonOut {
			return writePrettyJSON(stdout, map[string]any{"data": items})
		}
		if len(items) == 0 {
			fmt.Fprintln(stdout, "No agent task events found")
			return nil
		}
		for _, item := range items {
			fmt.Fprintf(stdout, "%d\t%d\t%s\t%s\t%s\n", item.ID, item.TaskID, item.EventType, item.CreatedAt.Format("2006-01-02 15:04:05"), item.PayloadJSON)
		}
		return nil
	case "cancel":
		opts, positionals, err := parseTenantAgentTasksOptions(args[1:])
		if err != nil {
			return err
		}
		if len(positionals) < 1 {
			return errors.New("tenant agent-tasks cancel requires a task id")
		}
		taskID, err := parseUintArg(positionals[0], "task id")
		if err != nil {
			return err
		}
		svc, taskCtx, cleanup, err := openTenantAgentTaskService(ctx, opts)
		if err != nil {
			return err
		}
		defer cleanup()
		resultJSON := agenttasks.CancelledResultJSON(agenttasks.CancelledResultOptions{
			Source: "cli",
			Reason: opts.reason,
		})
		if err := svc.CancelAgentTask(taskCtx, taskID, resultJSON); err != nil {
			return err
		}
		if opts.jsonOut {
			return writePrettyJSON(stdout, map[string]any{"id": taskID, "cancelled": true})
		}
		fmt.Fprintf(stdout, "Cancelled agent task %d\n", taskID)
		return nil
	default:
		return fmt.Errorf("unknown tenant agent-tasks command: %s", args[0])
	}
}

func parseTenantAgentTasksOptions(args []string) (tenantAgentTasksOptions, []string, error) {
	opts := tenantAgentTasksOptions{
		dsn:       firstEnv("NIUMA_MYSQL_DSN", "MYSQL_DSN"),
		tenantKey: firstEnv("NIUMA_TENANT_KEY", "TENANT_KEY"),
		userID:    firstEnv("NIUMA_USER_ID", "USER_ID"),
		traceID:   firstEnv("NIUMA_TRACE_ID", "TRACE_ID"),
		limit:     100,
	}
	var positionals []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dsn":
			v, err := flagValue(args, &i, "--dsn")
			if err != nil {
				return opts, nil, err
			}
			opts.dsn = v
		case "--tenant-key":
			v, err := flagValue(args, &i, "--tenant-key")
			if err != nil {
				return opts, nil, err
			}
			opts.tenantKey = v
		case "--user-id", "--user-key":
			v, err := flagValue(args, &i, "--user-id")
			if err != nil {
				return opts, nil, err
			}
			opts.userID = v
		case "--trace-id":
			v, err := flagValue(args, &i, "--trace-id")
			if err != nil {
				return opts, nil, err
			}
			opts.traceID = v
		case "--limit":
			v, err := flagValue(args, &i, "--limit")
			if err != nil {
				return opts, nil, err
			}
			limit, err := strconv.Atoi(v)
			if err != nil || limit < 1 {
				return opts, nil, errors.New("--limit must be a positive integer")
			}
			opts.limit = limit
		case "--json":
			opts.jsonOut = true
		case "--reason":
			v, err := flagValue(args, &i, "--reason")
			if err != nil {
				return opts, nil, err
			}
			opts.reason = v
		default:
			if strings.HasPrefix(args[i], "-") {
				return opts, nil, fmt.Errorf("unknown tenant agent-tasks option: %s", args[i])
			}
			positionals = append(positionals, args[i])
		}
	}
	if opts.dsn == "" {
		return opts, nil, errors.New("tenant agent-tasks requires --dsn or NIUMA_MYSQL_DSN")
	}
	if opts.tenantKey == "" {
		return opts, nil, errors.New("tenant agent-tasks requires --tenant-key or NIUMA_TENANT_KEY")
	}
	if opts.userID == "" {
		return opts, nil, errors.New("tenant agent-tasks requires --user-id or NIUMA_USER_ID")
	}
	return opts, positionals, nil
}

func openTenantAgentTaskService(ctx context.Context, opts tenantAgentTasksOptions) (tenantAgentTaskService, context.Context, func(), error) {
	svc, closeFn, err := newTenantAgentTaskService(ctx, opts.dsn)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open tenant mysql gorm repository: %w", err)
	}
	if closeFn == nil {
		closeFn = func() error { return nil }
	}
	taskCtx := observability.WithRequestValues(ctx, opts.traceID, opts.userID, opts.tenantKey)
	return svc, taskCtx, func() { _ = closeFn() }, nil
}

func parseUintArg(value, name string) (uint64, error) {
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || parsed == 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return parsed, nil
}

func isUintString(value string) bool {
	if value == "" {
		return false
	}
	_, err := strconv.ParseUint(value, 10, 64)
	return err == nil
}

type tenantMigrateOptions struct {
	dsn     string
	path    string
	steps   int
	all     bool
	jsonOut bool
}

func tenantMigrateCommand(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("tenant migrate requires up, down, or version")
	}
	action := args[0]
	opts, err := parseTenantMigrateOptions(args[1:])
	if err != nil {
		return err
	}
	if action != "up" && action != "down" && action != "version" {
		return fmt.Errorf("unknown tenant migrate action: %s", action)
	}
	runner, err := newTenantMigrationRunner(mysqlstore.MigrationOptions{DSN: opts.dsn, Path: opts.path})
	if err != nil {
		return err
	}
	defer func() {
		_ = runner.Close()
	}()
	switch action {
	case "up":
		if err := runner.Up(); err != nil {
			return err
		}
		if opts.jsonOut {
			return writePrettyJSON(stdout, map[string]any{"ok": true, "action": "up"})
		}
		fmt.Fprintln(stdout, "Applied MySQL tenant migrations")
	case "down":
		if opts.all {
			err = runner.DownAll()
		} else {
			err = runner.Down(opts.steps)
		}
		if err != nil {
			return err
		}
		if opts.jsonOut {
			return writePrettyJSON(stdout, map[string]any{"ok": true, "action": "down", "steps": opts.steps, "all": opts.all})
		}
		if opts.all {
			fmt.Fprintln(stdout, "Rolled back all MySQL tenant migrations")
		} else {
			fmt.Fprintf(stdout, "Rolled back %d MySQL tenant migration step(s)\n", opts.steps)
		}
	case "version":
		version, err := runner.Version()
		if err != nil {
			return err
		}
		if opts.jsonOut {
			return writePrettyJSON(stdout, version)
		}
		if !version.Applied {
			fmt.Fprintln(stdout, "No MySQL tenant migration has been applied")
			return nil
		}
		fmt.Fprintf(stdout, "MySQL tenant migration version %d dirty=%t\n", version.Version, version.Dirty)
	}
	return nil
}

func parseTenantMigrateOptions(args []string) (tenantMigrateOptions, error) {
	opts := tenantMigrateOptions{
		dsn:   firstEnv("NIUMA_MYSQL_DSN", "MYSQL_DSN"),
		path:  firstEnv("NIUMA_MIGRATIONS_PATH"),
		steps: 1,
	}
	if opts.path == "" {
		opts.path = mysqlstore.DefaultMigrationsPath
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dsn":
			v, err := flagValue(args, &i, "--dsn")
			if err != nil {
				return opts, err
			}
			opts.dsn = v
		case "--path":
			v, err := flagValue(args, &i, "--path")
			if err != nil {
				return opts, err
			}
			opts.path = v
		case "--steps":
			v, err := flagValue(args, &i, "--steps")
			if err != nil {
				return opts, err
			}
			steps, err := strconv.Atoi(v)
			if err != nil || steps < 1 {
				return opts, errors.New("--steps must be a positive integer")
			}
			opts.steps = steps
		case "--all":
			opts.all = true
		case "--json":
			opts.jsonOut = true
		default:
			return opts, fmt.Errorf("unknown tenant migrate option: %s", args[i])
		}
	}
	if opts.dsn == "" {
		return opts, errors.New("tenant migrate requires --dsn or NIUMA_MYSQL_DSN")
	}
	if opts.all && opts.steps != 1 {
		return opts, errors.New("use either --all or --steps, not both")
	}
	return opts, nil
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(product.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func parseStructuredSkillRoutes(raw string) ([]server.StructuredSkillRoute, error) {
	var routes []server.StructuredSkillRoute
	if err := json.Unmarshal([]byte(raw), &routes); err != nil {
		return nil, fmt.Errorf("parse NIUMA_STRUCTURED_SKILL_ROUTES: %w", err)
	}
	out := routes[:0]
	for _, route := range routes {
		route.SchemaName = strings.TrimSpace(route.SchemaName)
		route.SkillKey = strings.TrimSpace(route.SkillKey)
		if route.SchemaName == "" || route.SkillKey == "" {
			continue
		}
		out = append(out, route)
	}
	return out, nil
}

func telemetryExportHeaders(format string) map[string]string {
	headers := map[string]string{}
	if token := firstEnv("NIUMA_TELEMETRY_EXPORT_TOKEN", "TELEMETRY_EXPORT_TOKEN"); token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	if apiKey := firstEnv("NIUMA_TELEMETRY_EXPORT_API_KEY", "TELEMETRY_EXPORT_API_KEY", "DATADOG_API_KEY", "DD_API_KEY", "OTEL_EXPORTER_OTLP_HEADERS_API_KEY"); apiKey != "" {
		switch strings.ToLower(strings.TrimSpace(format)) {
		case "datadog", "dd":
			headers["DD-API-KEY"] = apiKey
		case "otel", "otlp", "otlp-log", "otlp-logs":
			headers["x-api-key"] = apiKey
		default:
			headers["x-api-key"] = apiKey
		}
	}
	if raw := firstEnv("NIUMA_TELEMETRY_EXPORT_HEADERS", "TELEMETRY_EXPORT_HEADERS"); raw != "" {
		var extra map[string]string
		if err := json.Unmarshal([]byte(raw), &extra); err == nil {
			for key, value := range extra {
				if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
					headers[key] = value
				}
			}
		}
	}
	return headers
}

func firstEnvInt(keys ...string) int {
	value := firstEnv(keys...)
	if value == "" {
		return 0
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return 0
	}
	return parsed
}

func firstEnvInt64(keys ...string) int64 {
	value := firstEnv(keys...)
	if value == "" {
		return 0
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0
	}
	return parsed
}

func firstEnvBool(keys ...string) bool {
	switch strings.ToLower(firstEnv(keys...)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func usageCommand(cwd string, stdout io.Writer) error {
	// Configured pricing first, so a non-Anthropic provider reports real cost
	// instead of landing in UnknownPricingModels (AUDIT-P0-15).
	usage, err := session.DefaultStore().UsageWithRates(configuredCostRates(cwd))
	if err != nil {
		return err
	}
	return writePrettyJSON(stdout, usage)
}

// configuredCostRates converts settings.modelPricing into session.Rate, including
// each model's cache tiers (AUDIT-P1-36).
func configuredCostRates(cwd string) map[string]session.Rate {
	return session.ConfiguredRates(cwd)
}

func initCommand(args []string, cwd string, stdout io.Writer) error {
	loadedSettings := config.LoadSettings(cwd)
	id := identity.FromSettings(config.IdentitySettings(loadedSettings.Settings))
	path := filepath.Join(cwd, id.GuidanceFilename)
	if _, err := os.Stat(path); err == nil {
		fmt.Fprintf(stdout, "%s already exists: %s\n", id.GuidanceFilename, path)
	} else {
		content := "# " + id.GuidanceFilename + "\n\nThis file provides guidance to " + id.ProductName + " when working in this repository.\n\n"
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Created %s\n", path)
	}
	if hasArg(args, "--settings") {
		if err := config.EnsureProjectSettingsMaterialized(cwd); err != nil {
			return err
		}
		settingsPath := config.ProjectSettingsPath(cwd, false)
		if _, err := os.Stat(settingsPath); err == nil {
			fmt.Fprintf(stdout, "settings.json already exists: %s\n", settingsPath)
			return nil
		}
		settings := config.Settings{Model: config.ResolveModel(cwd, "")}
		if err := config.SaveSettingsFile(settingsPath, settings); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Created %s\n", settingsPath)
	}
	return nil
}

func statusCommand(cwd string, stdout io.Writer) error {
	return writePrettyJSON(stdout, statusPayload(cwd))
}

func statusPayload(cwd string) map[string]any {
	cfg := config.LoadForCWD(cwd)
	runtimeDefaults := config.ResolveRuntimeDefaults(cwd)
	summaries, _ := session.DefaultStore().List()
	skillsList, _ := skills.List(cwd)
	pluginsList, _ := plugins.List(cwd)
	branch, _ := gitutil.Branch(context.Background(), cwd)
	gitStatus, _ := gitutil.StatusShort(context.Background(), cwd)
	payload := map[string]any{
		"cwd":              cwd,
		"provider":         runtimeDefaults.Provider,
		"model":            runtimeDefaults.Model,
		"runtime_defaults": runtimeDefaults,
		"baseURL":          cfg.BaseURL,
		"hasAuth":          cfg.APIKey != "" || cfg.AuthToken != "",
		"settingsSources":  cfg.Sources,
		"mcpServers":       len(cfg.Settings.MCPServers),
		"sessions":         len(summaries),
		"skills":           len(skillsList),
		"plugins":          len(pluginsList),
		"gitBranch":        branch,
		"gitDirty":         strings.TrimSpace(gitStatus) != "",
		"promptContext":    statusPromptContext(cwd),
	}
	if len(cfg.Warnings) > 0 {
		payload["configWarnings"] = cfg.Warnings
	}
	// Only present when the configured sandbox is not fully enforced here
	// (AUDIT-P1-35), so a healthy install keeps the output it always had.
	if warnings := tuiSandboxWarnings(cfg.Settings.Sandbox); len(warnings) > 0 {
		payload["sandbox"] = tuiSandboxLabel(cfg.Settings.Sandbox)
		payload["sandboxWarnings"] = warnings
	}
	return payload
}

func statusPromptContext(cwd string) map[string]any {
	mode := promptmode.Parse(os.Getenv("NIUMA_PROMPT_MODE"), promptmode.Code).String()
	docs, err := memory.LoadCode(cwd, "")
	context := map[string]any{
		"mode": mode,
		"cwd":  cwd,
	}
	if err != nil {
		context["memoryError"] = err.Error()
		return context
	}
	byType := map[string]int{}
	byTypeBytes := map[string]int{}
	documentBytes := 0
	_, promptReport := memory.PreparePromptDocuments(docs)
	var paths []string
	var documentSummary []map[string]any
	workflowSkillsSystemLeaked := false
	for _, doc := range docs {
		docType := strings.TrimSpace(doc.Type)
		if docType == "" {
			docType = "Unknown"
		}
		contentBytes := len(strings.TrimSpace(doc.Content))
		documentBytes += contentBytes
		byType[docType]++
		byTypeBytes[docType] += contentBytes
		if strings.TrimSpace(doc.Path) != "" {
			paths = append(paths, doc.Path)
		}
		if memory.IsWorkflowDocument(doc) && strings.Contains(strings.ToLower(doc.Content), "<skills_system") {
			workflowSkillsSystemLeaked = true
		}
		summary := map[string]any{
			"path":        doc.Path,
			"type":        docType,
			"bytes":       contentBytes,
			"promptBytes": statusPromptBytesForDocument(promptReport, doc),
		}
		if statusBudgetedDocument(promptReport, doc) {
			summary["budgeted"] = true
		}
		if memory.IsWorkflowDocument(doc) {
			summary["workflow"] = true
		}
		if len(doc.Paths) > 0 {
			summary["pathScoped"] = true
		}
		if len(doc.Excludes) > 0 {
			summary["excludeScoped"] = true
		}
		documentSummary = append(documentSummary, summary)
	}
	sort.Strings(paths)
	sort.Slice(documentSummary, func(i, j int) bool {
		left, _ := documentSummary[i]["path"].(string)
		right, _ := documentSummary[j]["path"].(string)
		if left == right {
			leftType, _ := documentSummary[i]["type"].(string)
			rightType, _ := documentSummary[j]["type"].(string)
			return leftType < rightType
		}
		return left < right
	})
	context["codeMemoryDocuments"] = len(docs)
	context["codeMemoryBytes"] = documentBytes
	context["codeMemoryPromptBytes"] = promptReport.PromptBytes
	context["codeMemoryByType"] = byType
	context["codeMemoryByTypeBytes"] = byTypeBytes
	context["codeMemoryPaths"] = paths
	context["codeMemoryDocumentSummary"] = documentSummary
	context["workflowRules"] = byType["Workflow"]
	context["workflowBytes"] = byTypeBytes["Workflow"]
	context["workflowBudgetBytes"] = promptReport.WorkflowBudgetBytes
	context["workflowBudgetedDocuments"] = statusWorkflowBudgetedDocuments(promptReport)
	context["memoryDocumentBudgetBytes"] = promptReport.DocumentBudgetBytes
	context["memoryPromptBudgetBytes"] = promptReport.TotalBudgetBytes
	context["memoryBudgetedDocuments"] = promptReport.BudgetedDocuments
	context["workflowSkillsSystemLeaked"] = workflowSkillsSystemLeaked
	return context
}

// statusWorkflowBudgetedDocuments counts only the workflow documents the budget
// shortened. The byte budget now covers every document type, so reporting the
// overall count under a workflow-specific key would overstate it.
func statusWorkflowBudgetedDocuments(report memory.PromptDocumentsReport) int {
	count := 0
	for _, summary := range report.Documents {
		if summary.Workflow && summary.Budgeted {
			count++
		}
	}
	return count
}

func statusPromptBytesForDocument(report memory.PromptDocumentsReport, doc memory.Document) int {
	for _, summary := range report.Documents {
		if summary.Path == doc.Path && strings.EqualFold(summary.Type, strings.TrimSpace(doc.Type)) {
			return summary.PromptBytes
		}
	}
	return len(strings.TrimSpace(doc.Content))
}

func statusBudgetedDocument(report memory.PromptDocumentsReport, doc memory.Document) bool {
	for _, summary := range report.Documents {
		if summary.Path == doc.Path && strings.EqualFold(summary.Type, strings.TrimSpace(doc.Type)) {
			return summary.Budgeted
		}
	}
	return false
}

func modelCommand(args []string, cwd string, stdout io.Writer) error {
	if len(args) == 0 || args[0] == "get" {
		fmt.Fprintln(stdout, config.ResolveModel(cwd, ""))
		return nil
	}
	switch args[0] {
	case "list":
		for _, model := range config.AvailableModels(cwd) {
			fmt.Fprintln(stdout, model)
		}
		return nil
	case "set":
		if len(args) < 2 {
			return errors.New("model set requires a model name")
		}
		settings := config.LoadGlobalSettings()
		settings.Model = args[1]
		if err := config.SaveGlobalSettings(settings); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Set model %s\n", args[1])
		return nil
	default:
		return fmt.Errorf("unknown model command: %s", args[0])
	}
}

func permissionsCommand(args []string, stdout io.Writer) error {
	settings := config.LoadGlobalSettings()
	if len(args) == 0 || args[0] == "list" {
		return writePrettyJSON(stdout, settings.Permissions)
	}
	switch args[0] {
	case "mode":
		if len(args) < 2 {
			return errors.New("permissions mode requires allow, ask, or deny")
		}
		if args[1] != "allow" && args[1] != "ask" && args[1] != "deny" {
			return fmt.Errorf("permissions mode must be one of allow, ask, deny, got %q", args[1])
		}
		settings.Permissions.DefaultMode = args[1]
	case "allow":
		if len(args) < 2 {
			return errors.New("permissions allow requires a tool pattern")
		}
		settings.Permissions.Allow = appendUnique(settings.Permissions.Allow, args[1])
	case "deny":
		if len(args) < 2 {
			return errors.New("permissions deny requires a tool pattern")
		}
		settings.Permissions.Deny = appendUnique(settings.Permissions.Deny, args[1])
	case "remove":
		if len(args) < 2 {
			return errors.New("permissions remove requires a tool pattern")
		}
		settings.Permissions.Allow = removeString(settings.Permissions.Allow, args[1])
		settings.Permissions.Deny = removeString(settings.Permissions.Deny, args[1])
	case "clear":
		target := "all"
		if len(args) > 1 {
			target = args[1]
		}
		switch target {
		case "allow":
			settings.Permissions.Allow = nil
		case "deny":
			settings.Permissions.Deny = nil
		case "all":
			settings.Permissions.Allow = nil
			settings.Permissions.Deny = nil
		default:
			return fmt.Errorf("invalid permissions clear target: %s", target)
		}
	default:
		return fmt.Errorf("unknown permissions command: %s", args[0])
	}
	if err := config.SaveGlobalSettings(settings); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "Updated permissions")
	return nil
}

func exportCommand(args []string, stdout io.Writer) error {
	format := "markdown"
	var positionals []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--format":
			i++
			if i >= len(args) {
				return errors.New("--format requires markdown or json")
			}
			format = args[i]
		case strings.HasPrefix(arg, "--format="):
			format = strings.TrimPrefix(arg, "--format=")
		default:
			positionals = append(positionals, arg)
		}
	}
	if len(positionals) < 2 {
		return errors.New("export requires a session id and output file")
	}
	summary, ok, err := session.DefaultStore().PrepareForWrite(positionals[0])
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("session not found: %s", positionals[0])
	}
	// Export the current conversation (v2: current chain), not abandoned branches.
	entries, err := session.LoadConversation(summary.Path)
	if err != nil {
		return err
	}
	var data []byte
	switch format {
	case "markdown", "md":
		data = []byte(session.ExportMarkdown(entries))
	case "json":
		data, err = json.MarshalIndent(entries, "", "  ")
		if err == nil {
			data = append(data, '\n')
		}
	default:
		return fmt.Errorf("unsupported export format: %s", format)
	}
	if err != nil {
		return err
	}
	if err := os.WriteFile(positionals[1], data, 0644); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Exported %s\n", positionals[1])
	return nil
}

func diffCommand(ctx context.Context, args []string, cwd string, stdout io.Writer) error {
	diff, err := gitutil.Diff(ctx, cwd, hasArg(args, "--stat"))
	if err != nil {
		return err
	}
	fmt.Fprint(stdout, diff)
	return nil
}

func branchCommand(ctx context.Context, cwd string, stdout io.Writer) error {
	branch, err := gitutil.Branch(ctx, cwd)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, branch)
	return nil
}

func reviewCommand(ctx context.Context, args []string, opts options, stdout, stderr io.Writer) error {
	diffOpts := gitutil.DiffOptions{Cached: hasArg(args, "--staged") || hasArg(args, "--cached")}
	stat, err := gitutil.DiffWithOptions(ctx, opts.cwd, gitutil.DiffOptions{Stat: true, Cached: diffOpts.Cached})
	if err != nil {
		return err
	}
	diff, err := gitutil.DiffWithOptions(ctx, opts.cwd, diffOpts)
	if err != nil {
		return err
	}
	if strings.TrimSpace(diff) == "" {
		return errors.New("no git diff to review")
	}
	reviewOpts := opts
	reviewOpts.print = true
	reviewOpts.prompt = buildReviewPrompt(stat, diff, reviewFocus(args))
	return runPrint(ctx, reviewOpts, stdout, stderr)
}

func buildReviewPrompt(stat, diff, focus string) string {
	var b strings.Builder
	b.WriteString("Please review the following git diff. Prioritize correctness bugs, regressions, security issues, data loss risks, and missing tests. ")
	b.WriteString("Return findings first, ordered by severity, with concrete file/line references when possible. Keep summaries brief.\n\n")
	if strings.TrimSpace(focus) != "" {
		b.WriteString("Additional review focus: ")
		b.WriteString(strings.TrimSpace(focus))
		b.WriteString("\n\n")
	}
	if strings.TrimSpace(stat) != "" {
		b.WriteString("Diff stat:\n```text\n")
		b.WriteString(strings.TrimSpace(stat))
		b.WriteString("\n```\n\n")
	}
	b.WriteString("Diff:\n```diff\n")
	b.WriteString(strings.TrimSpace(diff))
	b.WriteString("\n```")
	return b.String()
}

func reviewFocus(args []string) string {
	var words []string
	for _, arg := range args {
		switch arg {
		case "--staged", "--cached":
			continue
		default:
			if strings.HasPrefix(arg, "-") {
				continue
			}
			words = append(words, arg)
		}
	}
	return strings.Join(words, " ")
}

func completionCommand(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("completion requires shell: bash, zsh, or fish")
	}
	commands := completionCommandWords()
	switch args[0] {
	case "bash":
		fmt.Fprintf(stdout, `_golang_cc_complete() {
  COMPREPLY=($(compgen -W "%s" -- "${COMP_WORDS[1]}"))
}
complete -F _golang_cc_complete niuma
`, commands)
	case "zsh":
		fmt.Fprintf(stdout, `#compdef niuma
_arguments '1:command:(%s)'
`, commands)
	case "fish":
		for _, cmd := range strings.Fields(commands) {
			fmt.Fprintf(stdout, "complete -c niuma -f -a %s\n", cmd)
		}
	default:
		return fmt.Errorf("unsupported shell: %s", args[0])
	}
	return nil
}

func completionCommandWords() string {
	return strings.Join(commandWords(), " ")
}

func toolsCommand(ctx context.Context, opts options, stdout io.Writer) error {
	querySession, cleanup, err := newQuerySession(ctx, opts, nil, nil)
	if err != nil {
		return err
	}
	defer cleanup()
	defs := querySession.ToolDefinitions()
	return writePrettyJSON(stdout, defs)
}

func agentsCommand(args []string, cwd string, stdout io.Writer) error {
	if len(args) > 0 && args[0] == "doctor" {
		report, err := agents.Doctor(cwd)
		if err != nil {
			return err
		}
		if hasArg(args, "--json") {
			return writePrettyJSON(stdout, report)
		}
		fmt.Fprintf(stdout, "Agents: %d, errors: %d, warnings: %d\n", report.Agents, report.Errors, report.Warnings)
		if len(report.Diagnostics) == 0 {
			fmt.Fprintln(stdout, "No agent authoring issues found")
			return nil
		}
		for _, item := range report.Diagnostics {
			field := item.Field
			if field == "" {
				field = "-"
			}
			fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n", item.Level, item.Agent, field, item.Message)
		}
		return nil
	}
	if len(args) > 0 && args[0] == "show" {
		if len(args) < 2 {
			return errors.New("agents show requires an agent name")
		}
		agent, ok, err := agents.Load(cwd, args[1])
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("agent not found: %s", args[1])
		}
		return writePrettyJSON(stdout, agent)
	}
	if len(args) > 0 && args[0] != "list" {
		return fmt.Errorf("unknown agents command: %s", args[0])
	}
	list, err := agents.List(cwd)
	if err != nil {
		return err
	}
	if hasArg(args, "--json") {
		return writePrettyJSON(stdout, list)
	}
	if len(list) == 0 {
		fmt.Fprintln(stdout, "No agents found")
		return nil
	}
	for _, agent := range list {
		fmt.Fprintf(stdout, "%s\t%s\n", agent.Name, agent.Description)
	}
	return nil
}

func evalCommand(ctx context.Context, args []string, opts options, stdout io.Writer) error {
	if len(args) == 0 || args[0] != "agents" {
		return errors.New("eval requires subcommand: agents")
	}
	var dataset, output, format, profile, suiteName, providerName string
	runs := 0
	failFast := false
	jsonOut := false
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--profile":
			v, err := flagValue(args, &i, "--profile")
			if err != nil {
				return err
			}
			profile = v
		case "--suite":
			v, err := flagValue(args, &i, "--suite")
			if err != nil {
				return err
			}
			suiteName = v
		case "--dataset":
			v, err := flagValue(args, &i, "--dataset")
			if err != nil {
				return err
			}
			dataset = resolveInputPath(opts.cwd, v)
		case "--output", "-o":
			v, err := flagValue(args, &i, "--output")
			if err != nil {
				return err
			}
			output = resolveInputPath(opts.cwd, v)
		case "--format":
			v, err := flagValue(args, &i, "--format")
			if err != nil {
				return err
			}
			format = v
		case "--runs":
			v, err := flagValue(args, &i, "--runs")
			if err != nil {
				return err
			}
			parsed, parseErr := strconv.Atoi(v)
			if parseErr != nil || parsed <= 0 {
				return fmt.Errorf("--runs requires a positive integer, got %q", v)
			}
			runs = parsed
		case "--provider":
			v, err := flagValue(args, &i, "--provider")
			if err != nil {
				return err
			}
			providerName = strings.TrimSpace(v)
		case "--fail-fast":
			failFast = true
		case "--json":
			jsonOut = true
			format = "json"
		default:
			return fmt.Errorf("unknown eval agents option: %s", args[i])
		}
	}
	report, err := agenteval.Run(ctx, agenteval.Options{
		CWD:      opts.cwd,
		Dataset:  dataset,
		Output:   output,
		Format:   format,
		FailFast: failFast,
		Profile:  profile,
		Suite:    suiteName,
		Runs:     runs,
		Provider: providerName,
	})
	if jsonOut {
		encodeErr := writePrettyJSON(stdout, report)
		if err != nil {
			return err
		}
		return encodeErr
	}
	fmt.Fprintf(stdout, "Agent eval %s: %d/%d passed", report.Status, report.Passed, report.Total)
	if report.Skipped > 0 {
		fmt.Fprintf(stdout, ", %d skipped", report.Skipped)
	}
	fmt.Fprintln(stdout)
	for _, item := range report.Cases {
		fmt.Fprintf(stdout, "- %s [%s]: %s\n", item.ID, item.Type, item.Status)
		if item.Error != "" {
			fmt.Fprintf(stdout, "  error: %s\n", item.Error)
		}
	}
	if output != "" {
		fmt.Fprintf(stdout, "Report: %s\n", output)
	}
	return err
}

func compactCommand(args []string, stdout io.Writer) error {
	maxBytes := 0
	var positionals []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--max-bytes":
			v, err := flagValue(args, &i, "--max-bytes")
			if err != nil {
				return err
			}
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return errors.New("--max-bytes must be a positive integer")
			}
			maxBytes = n
		default:
			positionals = append(positionals, args[i])
		}
	}
	if len(positionals) < 1 {
		return errors.New("compact requires a session id")
	}
	summary, ok, err := session.DefaultStore().Find(positionals[0])
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("session not found: %s", positionals[0])
	}
	cwd, _ := os.Getwd()
	hookRunner := hooks.New(config.LoadForCWD(cwd).Settings.Hooks)
	_, _ = hookRunner.RunWithPayload(context.Background(), hooks.PreCompact, cwd, hooks.Payload{})
	entry, err := session.Compact(summary.Path, maxBytes)
	if err != nil {
		return err
	}
	_, _ = hookRunner.RunWithPayload(context.Background(), hooks.PostCompact, cwd, hooks.Payload{Result: entry.Content})
	fmt.Fprintf(stdout, "Compacted %s (%d bytes summary)\n", positionals[0], len(entry.Content))
	return nil
}

func compactSlashCommand(args []string, opts options, recorder *session.Recorder, stdout io.Writer) error {
	if len(args) > 0 {
		return compactCommand(args, stdout)
	}
	if recorder == nil || recorder.Path == "" {
		return errors.New("/compact has no active session")
	}
	cwd, _ := os.Getwd()
	hookRunner := hooks.New(config.LoadForCWD(cwd).Settings.Hooks)
	_, _ = hookRunner.RunWithPayload(context.Background(), hooks.PreCompact, cwd, hooks.Payload{})
	var logicalEntries []session.Entry
	if strings.TrimSpace(opts.resume) == "" {
		conv, loadErr := session.LoadConversation(recorder.Path)
		if loadErr != nil {
			return loadErr
		}
		logicalEntries = conv
	} else {
		resumeEntries, loadErr := loadResumeEntries(session.DefaultStore(), opts.resume, opts.resumeSessionAt)
		if loadErr != nil {
			return loadErr
		}
		continuationEntries, loadErr := session.Load(recorder.Path)
		if loadErr != nil {
			return loadErr
		}
		offset := opts.resumeContinuationEntryOffset
		if offset < 0 || offset > len(continuationEntries) {
			return fmt.Errorf("invalid resume transcript entry offset %d for %d entries", offset, len(continuationEntries))
		}
		// v2: take the current-chain suffix so a mid-session rewind's abandoned
		// branch is not compacted into the summary; v1 keeps the physical suffix.
		continuationSlice := continuationEntries[offset:]
		if session.IsV2Entries(continuationEntries) {
			continuationSlice = currentChainSuffix(continuationEntries, offset)
		}
		logicalEntries = append(append([]session.Entry(nil), resumeEntries...), continuationSlice...)
	}
	// Append the summary through the live recorder so a v2 file tags and chains it
	// onto the active leaf AND the recorder's in-memory leaf advances to it — the
	// next turn then continues from the summary instead of orphaning it on a branch.
	summary := session.BuildCompactSummary(logicalEntries, 0)
	if err := recorder.Append(session.Entry{Type: "compact_summary", Content: summary}); err != nil {
		return err
	}
	_, _ = hookRunner.RunWithPayload(context.Background(), hooks.PostCompact, cwd, hooks.Payload{Result: summary})
	fmt.Fprintf(stdout, "Compacted current session (%d bytes summary)\n", len(summary))
	return nil
}

func handleTUIRecapSlash(ctx context.Context, opts options, input string, recorder *session.Recorder, events chan<- tui.StreamEvent) (handled bool, exit bool, text string, err error) {
	if opts.runtimeProfile.IsBare() {
		return false, false, "", nil
	}
	fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(input), "/"))
	if len(fields) == 0 || !strings.EqualFold(fields[0], "recap") {
		return false, false, "", nil
	}
	var out bytes.Buffer
	err = recapSlashCommand(ctx, fields[1:], opts, recorder, &out)
	text = strings.TrimSpace(out.String())
	if events != nil && err == nil {
		if latest, ok := recapTextFromSlashOutput(fields[1:], text); ok {
			events <- tui.StreamEvent{Type: tui.StreamRecap, Text: latest}
		} else if text != "" {
			events <- tui.StreamEvent{Type: tui.StreamText, Text: text}
		}
	}
	return true, false, text, err
}

func recapTextFromSlashOutput(args []string, output string) (string, bool) {
	if len(args) > 0 && strings.EqualFold(args[0], "off") {
		return "", false
	}
	output = strings.TrimSpace(output)
	if strings.HasPrefix(output, "※ recap:") {
		text := strings.TrimSpace(strings.TrimPrefix(output, "※ recap:"))
		lines := strings.Split(text, "\n")
		for i, line := range lines {
			lines[i] = strings.TrimPrefix(line, "  ")
		}
		return strings.TrimSpace(strings.Join(lines, "\n")), true
	}
	return "", false
}

func recapSlashCommand(ctx context.Context, args []string, opts options, recorder *session.Recorder, stdout io.Writer) error {
	if len(args) > 1 {
		return errors.New("/recap accepts at most one argument: show or off")
	}
	if len(args) == 1 && strings.EqualFold(args[0], "off") {
		fmt.Fprintln(stdout, "Recap auto refresh is controlled by config: recap.enabled=false or recap.mode=manual.")
		return nil
	}
	_, path, err := activeRewindSession(opts, recorder)
	if err != nil {
		return err
	}
	if strings.TrimSpace(path) == "" {
		return errors.New("/recap has no active session")
	}
	if len(args) == 1 && strings.EqualFold(args[0], "show") {
		// Recap is session-wide (off the conversation chain in v2): scan the full file.
		full, err := session.Load(path)
		if err != nil {
			return err
		}
		entry, ok := recap.Latest(full)
		if !ok {
			fmt.Fprintln(stdout, "No recap yet. Run /recap to generate one.")
			return nil
		}
		fmt.Fprintln(stdout, recap.FormatForDisplay(entry.Content))
		return nil
	}
	// Generation summarizes the current conversation (v2: current chain).
	entries, err := session.LoadConversation(path)
	if err != nil {
		return err
	}
	if len(args) == 1 {
		return fmt.Errorf("unknown /recap argument: %s", args[0])
	}
	result, err := generateRecapForSession(ctx, opts, path, entries, recap.ModeManual)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, recap.FormatForDisplay(result.Text))
	return nil
}

func generateRecapForSession(ctx context.Context, opts options, path string, entries []session.Entry, source string) (recap.Result, error) {
	cfg := config.LoadForCWD(opts.cwd)
	if err := applyRuntimeOptions(&cfg.Settings, opts); err != nil {
		return recap.Result{}, err
	}
	if err := applySelectedProvider(&cfg, &opts); err != nil {
		return recap.Result{}, err
	}
	client := anthropic.NewClient(cfg)
	recapCfg := recap.ConfigFromSettings(cfg.Settings, opts.model)
	result, err := generateRecapWithTelemetry(ctx, client, entries, recapCfg, source, opts, path)
	if err != nil {
		return recap.Result{}, err
	}
	if err := recap.AppendToPath(path, result); err != nil {
		return recap.Result{}, err
	}
	return result, nil
}

func generateRecapWithTelemetry(ctx context.Context, streamer recap.Streamer, entries []session.Entry, cfg recap.Config, source string, opts options, path string) (recap.Result, error) {
	ctx = observability.WithCLISessionID(ctx, sessionIDFromPath(path))
	ctx = observability.WithRequestPurpose(ctx, "recap."+strings.ReplaceAll(source, "-", "_"))
	start := time.Now()
	cfg = cfg.WithDefaults(opts.model)
	telemetry.Emit(ctx, telemetry.Event{
		Name:         "session.recap.started",
		Category:     telemetry.CategorySession,
		Source:       "cli.recap",
		Status:       telemetry.StatusStarted,
		Model:        cfg.Model,
		ResourceType: "session",
		ResourceID:   sessionIDFromPath(path),
		Properties: map[string]any{
			"mode":                  source,
			"recent_message_window": cfg.RecentMessageWindow,
		},
	})
	result, err := recap.Generate(ctx, streamer, entries, cfg, source, opts.model)
	if err != nil {
		telemetry.Emit(ctx, telemetry.Event{
			Name:         "session.recap.failed",
			Category:     telemetry.CategorySession,
			Source:       "cli.recap",
			Status:       telemetry.StatusError,
			Model:        cfg.Model,
			DurationMS:   time.Since(start).Milliseconds(),
			ResourceType: "session",
			ResourceID:   sessionIDFromPath(path),
			Error:        err.Error(),
			Properties: map[string]any{
				"mode": source,
			},
		})
		return recap.Result{}, err
	}
	telemetry.Emit(ctx, telemetry.Event{
		Name:         "session.recap.finished",
		Category:     telemetry.CategorySession,
		Source:       "cli.recap",
		Status:       telemetry.StatusOK,
		Model:        cfg.Model,
		DurationMS:   time.Since(start).Milliseconds(),
		ResourceType: "session",
		ResourceID:   sessionIDFromPath(path),
		Properties: map[string]any{
			"mode":          source,
			"content_bytes": len([]byte(result.Text)),
		},
	})
	return result, nil
}

// runPostTurnRecap 同步生成并持久化一轮结束后的会话回顾，返回回顾文本。
// 刻意不接收外部 channel：调用方（TUI 的 cmd）才拥有投递通道的生命周期。
func runPostTurnRecap(ctx context.Context, opts options, recorder *session.Recorder) (string, error) {
	if recorder == nil || strings.TrimSpace(recorder.Path) == "" {
		return "", nil
	}
	cfg := config.LoadForCWD(opts.cwd)
	if err := applyRuntimeOptions(&cfg.Settings, opts); err != nil {
		return "", nil
	}
	if err := applySelectedProvider(&cfg, &opts); err != nil {
		return "", nil
	}
	recapCfg := recap.ConfigFromSettings(cfg.Settings, opts.model)
	if !recapCfg.PostTurnEnabled(opts.model) {
		return "", nil
	}
	path := recorder.Path
	entries, err := session.LoadConversation(path)
	if err != nil {
		telemetry.Emit(ctx, telemetry.Event{
			Name:         "session.recap.failed",
			Category:     telemetry.CategorySession,
			Source:       "cli.recap",
			Status:       telemetry.StatusError,
			Model:        recapCfg.Model,
			ResourceType: "session",
			ResourceID:   sessionIDFromPath(path),
			Error:        err.Error(),
			Properties:   map[string]any{"mode": recap.ModePostTurn},
		})
		return "", err
	}
	client := anthropic.NewClient(cfg)
	result, err := generateRecapWithTelemetry(ctx, client, entries, recapCfg, recap.ModePostTurn, opts, path)
	if err != nil {
		return "", err
	}
	appended, err := recap.AppendToPathIfCurrent(ctx, path, result)
	if err != nil {
		telemetry.Emit(ctx, telemetry.Event{
			Name:         "session.recap.failed",
			Category:     telemetry.CategorySession,
			Source:       "cli.recap",
			Status:       telemetry.StatusError,
			Model:        recapCfg.Model,
			ResourceType: "session",
			ResourceID:   sessionIDFromPath(path),
			Error:        err.Error(),
			Properties:   map[string]any{"mode": recap.ModePostTurn},
		})
		return "", err
	}
	if !appended {
		return "", nil
	}
	return result.Text, nil
}

// postTurnRecapTimeout 沿用原实现的 60s。
const postTurnRecapTimeout = 60 * time.Second

// tuiPostTurnRecapRunner 把 runPostTurnRecap 包装成 tui.AwayRecapFunc：由 TUI
// 自己持有的 channel 驱动，超时也由这里施加，调用方（TUI 的 cmd）只管收发。
func tuiPostTurnRecapRunner(opts options, recorder *session.Recorder) tui.AwayRecapFunc {
	return func(ctx context.Context, events chan<- tui.StreamEvent) error {
		callCtx, cancel := context.WithTimeout(ctx, postTurnRecapTimeout)
		defer cancel()
		text, err := runPostTurnRecap(callCtx, opts, recorder)
		if err != nil {
			return err
		}
		if strings.TrimSpace(text) != "" {
			events <- tui.StreamEvent{Type: tui.StreamRecap, Text: text}
		}
		return nil
	}
}

// nextStepSuggestionTimeout 比 recap 的 60s 短得多：超过这个时长用户早已开始
// 打字，结果不再有展示价值。
const nextStepSuggestionTimeout = 20 * time.Second

// tuiNextStepsRunner 返回 TUI 用的下一步候选生成器。它由 TUI 在自己的 cmd 里
// 同步调用（channel 由 cmd 持有），所以这里不再自己起 goroutine。
func tuiNextStepsRunner(opts options) tui.NextStepsFunc {
	return func(ctx context.Context, prompt string, result tui.QueryResult, events chan<- tui.StreamEvent) error {
		// 与 server 侧的 appendAgentTaskNextSteps 同一个顺序：先判断这轮有没有
		// 可推荐的东西，再去读配置文件——空回复本来就会让 BuildPrompt 返回空串，
		// 没必要为它先付一次 settings 读盘的成本。
		if strings.TrimSpace(result.Response) == "" {
			return nil
		}
		cfg := config.LoadForCWD(opts.cwd)
		if err := applyRuntimeOptions(&cfg.Settings, opts); err != nil {
			return err
		}
		runnerOpts := opts
		if err := applySelectedProvider(&cfg, &runnerOpts); err != nil {
			return err
		}
		// currentOpts（这里的 opts）是整个交互式会话开始时捕获的值，跟不上会话
		// 中途的换模型；result.Model 是刚结束这一轮实际用的模型，新鲜且不需要
		// 指针捕获就能拿到（指针捕获会被 bubbletea 的 detached goroutine 判成
		// -race，见 interactive.go 里 currentOpts 的注释）。有它就优先用它做
		// 档位解析。
		parentModel := runnerOpts.model
		if resultModel := strings.TrimSpace(result.Model); resultModel != "" {
			parentModel = resultModel
		}
		stepCfg := nextsteps.ConfigFromSettings(cfg.Settings, parentModel)
		if !stepCfg.Enabled {
			return nil
		}
		callCtx, cancel := context.WithTimeout(ctx, nextStepSuggestionTimeout)
		defer cancel()
		suggestions, err := nextsteps.Generate(callCtx, anthropic.NewClient(cfg), nextsteps.Input{
			UserPrompt:        prompt,
			AssistantResponse: result.Response,
			ToolNames:         toolNamesFromResult(result),
		}, stepCfg)
		if err != nil {
			// 默认开启、每轮都花 provider token 的功能，失败却没有任何可观测性——
			// 镜像 session.recap.failed 的事件形状（见 generateRecapWithTelemetry）。
			telemetry.Emit(ctx, telemetry.Event{
				Name:     "session.next_steps.failed",
				Category: telemetry.CategorySession,
				Source:   "cli.next_steps",
				Status:   telemetry.StatusError,
				Model:    stepCfg.Model,
				Error:    err.Error(),
			})
			return err
		}
		if event, ok := nextStepsEvent(suggestions); ok {
			events <- event
		}
		return nil
	}
}

// nextStepsEvent 把候选打包成事件。没有候选时返回 ok=false，调用方据此不发事件。
func nextStepsEvent(suggestions []string) (tui.StreamEvent, bool) {
	if len(suggestions) == 0 {
		return tui.StreamEvent{}, false
	}
	payload, err := json.Marshal(suggestions)
	if err != nil {
		return tui.StreamEvent{}, false
	}
	return tui.StreamEvent{Type: tui.StreamNextSteps, Payload: payload}, true
}

func toolNamesFromResult(result tui.QueryResult) []string {
	var names []string
	for _, call := range result.ToolCalls {
		if name := strings.TrimSpace(call.Name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func tuiAwayRecapDelay(opts options) time.Duration {
	cfg := config.LoadForCWD(opts.cwd)
	if err := applyRuntimeOptions(&cfg.Settings, opts); err != nil {
		return 0
	}
	recapCfg := tuiRecapConfig(cfg.Settings, opts.model)
	if !recapCfg.AwayEnabled(opts.model) {
		return 0
	}
	return time.Duration(recapCfg.AwayDelaySeconds) * time.Second
}

func tuiRecapConfig(settings config.Settings, defaultModel string) recap.Config {
	recapCfg := recap.ConfigFromSettings(settings, defaultModel)
	if settings.Recap != nil && settings.Recap.Enabled != nil && !*settings.Recap.Enabled {
		return recapCfg
	}
	recapCfg.Enabled = true
	if settings.Recap == nil || strings.TrimSpace(settings.Recap.Mode) == "" {
		recapCfg.Mode = recap.ModeAway
	}
	return recapCfg.WithDefaults(defaultModel)
}

func tuiAwayRecapRunner(opts options, recorder *session.Recorder) tui.AwayRecapFunc {
	if recorder == nil || strings.TrimSpace(recorder.Path) == "" {
		return nil
	}
	if tuiAwayRecapDelay(opts) <= 0 {
		return nil
	}
	return func(ctx context.Context, events chan<- tui.StreamEvent) error {
		cfg := config.LoadForCWD(opts.cwd)
		if err := applyRuntimeOptions(&cfg.Settings, opts); err != nil {
			return err
		}
		if err := applySelectedProvider(&cfg, &opts); err != nil {
			return err
		}
		recapCfg := tuiRecapConfig(cfg.Settings, opts.model)
		if !recapCfg.AwayEnabled(opts.model) {
			return nil
		}
		entries, err := session.LoadConversation(recorder.Path)
		if err != nil {
			telemetry.Emit(ctx, telemetry.Event{
				Name:         "session.recap.failed",
				Category:     telemetry.CategorySession,
				Source:       "cli.recap",
				Status:       telemetry.StatusError,
				Model:        recapCfg.Model,
				ResourceType: "session",
				ResourceID:   sessionIDFromPath(recorder.Path),
				Error:        err.Error(),
				Properties:   map[string]any{"mode": recap.ModeAway},
			})
			return err
		}
		client := anthropic.NewClient(cfg)
		result, err := generateRecapWithTelemetry(ctx, client, entries, recapCfg, recap.ModeAway, opts, recorder.Path)
		if err != nil {
			return err
		}
		appended, err := recap.AppendToPathIfCurrent(ctx, recorder.Path, result)
		if err != nil {
			telemetry.Emit(ctx, telemetry.Event{
				Name:         "session.recap.failed",
				Category:     telemetry.CategorySession,
				Source:       "cli.recap",
				Status:       telemetry.StatusError,
				Model:        recapCfg.Model,
				ResourceType: "session",
				ResourceID:   sessionIDFromPath(recorder.Path),
				Error:        err.Error(),
				Properties:   map[string]any{"mode": recap.ModeAway},
			})
			return err
		}
		if !appended {
			return nil
		}
		if events != nil {
			events <- tui.StreamEvent{Type: tui.StreamRecap, Text: result.Text}
		}
		return nil
	}
}

func sessionIDFromPath(path string) string {
	base := filepath.Base(strings.TrimSpace(path))
	return strings.TrimSuffix(base, filepath.Ext(base))
}

type rewindSlashMode string

const (
	rewindSlashBoth             rewindSlashMode = "both"
	rewindSlashConversationOnly rewindSlashMode = "conversation"
	rewindSlashFilesOnly        rewindSlashMode = "files"
)

type rewindCandidate struct {
	ID      string
	Preview string
}

func rewindSlashCommand(args []string, opts options, recorder *session.Recorder, stdout io.Writer) error {
	sessionID, path, err := activeRewindSession(opts, recorder)
	if err != nil {
		return err
	}
	if sessionID == "" || path == "" {
		return errors.New("/rewind has no active session")
	}
	mode := rewindSlashBoth
	var messageID string
	for _, arg := range args {
		switch arg {
		case "--conversation-only", "--conversation":
			mode = rewindSlashConversationOnly
		case "--files-only", "--code-only", "--code":
			mode = rewindSlashFilesOnly
		case "--both":
			mode = rewindSlashBoth
		default:
			if strings.HasPrefix(arg, "-") {
				return fmt.Errorf("unknown /rewind option: %s", arg)
			}
			if messageID != "" {
				return errors.New("/rewind accepts at most one message id")
			}
			messageID = arg
		}
	}
	if messageID == "" {
		return listRewindCandidates(path, stdout)
	}
	var (
		result session.RewindResult
		ok     bool
	)
	switch mode {
	case rewindSlashFilesOnly:
		result, ok, err = session.DefaultStore().RewindFiles(sessionID, messageID)
	case rewindSlashConversationOnly:
		result, ok, err = session.DefaultStore().RewindConversationToMessage(sessionID, messageID)
	default:
		result, ok, err = session.DefaultStore().RewindToMessage(sessionID, messageID)
	}
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	// The rewind appended a branch_head out-of-band (via the store). The live
	// recorder's active leaf is realigned with the transcript at the next turn's
	// start (query.Session.run → Recorder.SyncLeafFromDisk), so the continuation
	// forks from the moved leaf. See
	// docs/transcript/live_recorder_leaf_sync_after_rewind_fix.md.
	switch mode {
	case rewindSlashFilesOnly:
		fmt.Fprintf(stdout, "Rewound code to before message %s\n", messageID)
	case rewindSlashConversationOnly:
		fmt.Fprintf(stdout, "Rewound conversation to before message %s\n", messageID)
	default:
		fmt.Fprintf(stdout, "Rewound code and conversation to before message %s\n", messageID)
	}
	fmt.Fprintf(stdout, "Files restored: %d\n", result.FilesRestored)
	if result.EntriesRemoved > 0 {
		if result.TranscriptKept {
			// v2 non-destructive rewind: the abandoned branch is preserved and can
			// be redone; nothing was deleted from the transcript.
			fmt.Fprintf(stdout, "Conversation moved back %d message(s); previous branch preserved (see `session branches`, `session redo`)\n", result.EntriesRemoved)
		} else {
			fmt.Fprintf(stdout, "Transcript entries removed: %d\n", result.EntriesRemoved)
		}
	}
	return nil
}

func activeRewindSession(opts options, recorder *session.Recorder) (sessionID, path string, err error) {
	resumeID := strings.TrimSpace(opts.resume)
	if resumeID != "" {
		store := session.DefaultStore()
		var summary session.Summary
		var ok bool
		if resumeID == "latest" {
			summaries, listErr := store.List()
			if listErr != nil {
				return "", "", listErr
			}
			if len(summaries) == 0 {
				return "", "", errors.New("no sessions found")
			}
			summary = summaries[0]
		} else {
			summary, ok, err = store.Find(resumeID)
			if err != nil {
				return "", "", err
			}
			if !ok {
				return "", "", fmt.Errorf("session not found: %s", resumeID)
			}
		}
		return summary.SessionID, summary.Path, nil
	}
	if recorder == nil {
		return "", "", nil
	}
	return strings.TrimSpace(recorder.SessionID), strings.TrimSpace(recorder.Path), nil
}

func listRewindCandidates(path string, stdout io.Writer) error {
	// Offer only current-conversation messages as rewind targets (v2: current chain).
	entries, err := session.LoadConversation(path)
	if err != nil {
		return err
	}
	candidates := rewindCandidates(entries, 8)
	if len(candidates) == 0 {
		fmt.Fprintln(stdout, "Nothing to rewind to yet.")
		return nil
	}
	fmt.Fprintln(stdout, "Rewind to a previous user message:")
	for i, candidate := range candidates {
		fmt.Fprintf(stdout, "%d. %s\n", i+1, candidate.Preview)
		fmt.Fprintf(stdout, "   id: %s\n", candidate.ID)
	}
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Run /rewind <message-id> to restore code and conversation to before that message.")
	fmt.Fprintln(stdout, "Use --conversation-only or --files-only to restore only one side.")
	return nil
}

func rewindCandidates(entries []session.Entry, limit int) []rewindCandidate {
	var out []rewindCandidate
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		if entry.Type != "message" || entry.Role != "user" || strings.TrimSpace(entry.ID) == "" {
			continue
		}
		// Skip runtime-injected reminders (completion/closure-gate nudges, etc.):
		// they are recorded as user messages but are not human turns, so they must
		// not appear as rewind targets. New sessions no longer persist these; this
		// also keeps older transcripts that did from polluting the picker.
		if strings.HasPrefix(strings.TrimSpace(entry.Content), "<system-reminder>") {
			continue
		}
		out = append(out, rewindCandidate{ID: entry.ID, Preview: previewText(entry.Content, 100)})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func previewText(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return "(empty message)"
	}
	runes := []rune(text)
	if limit > 0 && len(runes) > limit {
		return string(runes[:limit-3]) + "..."
	}
	return text
}

func backgroundListCommand(stdout io.Writer) error {
	jobs, err := background.DefaultStore().List()
	if err != nil {
		return err
	}
	if len(jobs) == 0 {
		fmt.Fprintln(stdout, "No background sessions")
		return nil
	}
	for _, job := range jobs {
		pid := "-"
		if job.PID > 0 {
			pid = strconv.Itoa(job.PID)
		}
		kind := firstNonEmptyString(job.Kind, "background")
		if job.Kind == "loop" && job.IntervalSeconds > 0 {
			kind += "/" + humanLoopInterval(time.Duration(job.IntervalSeconds)*time.Second)
		}
		fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\t%s\t%s\n", job.ID, job.Status, pid, kind, job.CreatedAt.Format("2006-01-02 15:04:05"), job.Prompt)
	}
	return nil
}

func backgroundLogsCommand(args []string, stdout io.Writer) error {
	if len(args) < 1 {
		return errors.New("logs requires a background session id")
	}
	logs, ok, err := background.DefaultStore().Logs(args[0])
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("background session not found: %s", args[0])
	}
	if logs == "" {
		fmt.Fprintf(stdout, "No logs for %s\n", args[0])
		return nil
	}
	fmt.Fprint(stdout, logs)
	return nil
}

func backgroundKillCommand(args []string, stdout io.Writer) error {
	if len(args) < 1 {
		return errors.New("kill requires a background session id")
	}
	scheduleStore := scheduler.DefaultStore()
	_, _, _ = scheduleStore.Disable(args[0])
	ok, err := background.DefaultStore().Kill(args[0])
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("background session not found: %s", args[0])
	}
	fmt.Fprintf(stdout, "Killed background session %s\n", args[0])
	return nil
}

func backgroundAttachCommand(args []string, stdout io.Writer) error {
	id := ""
	wait := false
	for _, arg := range args {
		switch arg {
		case "--wait":
			wait = true
		default:
			if id != "" {
				return fmt.Errorf("unexpected attach argument: %s", arg)
			}
			id = arg
		}
	}
	if id == "" {
		return errors.New("attach requires a background session id")
	}

	store := background.DefaultStore()
	printed := 0
	headerPrinted := false
	for {
		job, ok, err := store.Find(id)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("background session not found: %s", id)
		}
		if !headerPrinted {
			pid := "-"
			if job.PID > 0 {
				pid = strconv.Itoa(job.PID)
			}
			fmt.Fprintf(stdout, "Background session %s\nStatus: %s\nPID: %s\nLog: %s\n\n", job.ID, job.Status, pid, job.LogPath)
			headerPrinted = true
		}

		logs, _, err := store.Logs(id)
		if err != nil {
			return err
		}
		if len(logs) > printed {
			fmt.Fprint(stdout, logs[printed:])
			printed = len(logs)
		}
		if !wait {
			if logs == "" {
				fmt.Fprintf(stdout, "No logs for %s\n", id)
			}
			return nil
		}
		if backgroundDone(job.Status) {
			fmt.Fprintf(stdout, "\nStatus: %s\n", job.Status)
			if job.Error != "" {
				fmt.Fprintf(stdout, "Error: %s\n", job.Error)
			}
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func backgroundRunCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) < 1 {
		return errors.New("__background-run requires a background session id")
	}
	store := background.DefaultStore()
	job, ok, err := store.MarkRunning(args[0], os.Getpid())
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("background session not found: %s", args[0])
	}
	if job.Status == "killed" {
		return nil
	}
	opts := options{
		runtimeProfile:  runtimeprofile.Profile(job.RuntimeProfile),
		print:           true,
		prompt:          job.Prompt,
		model:           job.Model,
		modelExplicit:   strings.TrimSpace(job.Model) != "",
		providerName:    job.Provider,
		outputFormat:    job.OutputFormat,
		maxTurns:        job.MaxTurns,
		maxTokens:       job.MaxTokens,
		cwd:             job.CWD,
		resume:          job.Resume,
		sessionID:       job.SessionID,
		sessionName:     job.SessionName,
		noPersistence:   job.NoPersistence,
		systemPrompt:    job.SystemPrompt,
		appendSystem:    job.AppendSystem,
		allowedTools:    job.AllowedTools,
		deniedTools:     job.DeniedTools,
		permissionMode:  job.PermissionMode,
		additionalDirs:  job.AdditionalDirs,
		skipPermissions: job.SkipPermissions,
		promptMode:      job.PromptMode,
		agentName:       job.Agent,
		toolsSpecified:  job.ToolsSpecified,
		enabledTools:    append([]string(nil), job.EnabledTools...),
		settingsInputs:  append([]string(nil), job.SettingsInputs...),
		mcpConfigInputs: append([]string(nil), job.MCPConfigInputs...),
		strictMCPConfig: job.StrictMCPConfig,
	}
	if opts.outputFormat == "" {
		opts.outputFormat = "text"
	}
	if opts.maxTurns <= 0 {
		opts.maxTurns = defaults.MaxTurns
	}
	opts.model = config.ResolveModel(opts.cwd, opts.model)
	if err := runPrint(ctx, opts, stdout, stderr); err != nil {
		_, _, _ = store.Finish(args[0], 1, err.Error())
		return err
	}
	_, _, err = store.Finish(args[0], 0, "")
	return err
}

func loopRunCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) < 1 {
		return errors.New("__loop-run requires a background session id")
	}
	store := background.DefaultStore()
	job, ok, err := store.MarkRunning(args[0], os.Getpid())
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("loop not found: %s", args[0])
	}
	if job.IntervalSeconds <= 0 {
		return fmt.Errorf("loop %s has invalid interval", job.ID)
	}
	interval := time.Duration(job.IntervalSeconds) * time.Second
	for {
		current, ok, err := store.Find(job.ID)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("loop not found: %s", job.ID)
		}
		if current.Status == "killed" {
			return nil
		}
		runNumber := current.RunCount + 1
		fmt.Fprintf(stdout, "\n[loop %s] run #%d started at %s\n", job.ID, runNumber, time.Now().Format(time.RFC3339))
		runOpts := options{
			print:           true,
			prompt:          loopRunPrompt(current.CWD, current.Prompt),
			model:           current.Model,
			modelExplicit:   strings.TrimSpace(current.Model) != "",
			providerName:    current.Provider,
			outputFormat:    firstNonEmptyString(current.OutputFormat, "text"),
			maxTurns:        current.MaxTurns,
			maxTokens:       current.MaxTokens,
			cwd:             current.CWD,
			resume:          current.Resume,
			sessionID:       current.SessionID,
			sessionName:     current.SessionName,
			noPersistence:   current.NoPersistence,
			systemPrompt:    current.SystemPrompt,
			appendSystem:    current.AppendSystem,
			allowedTools:    current.AllowedTools,
			deniedTools:     current.DeniedTools,
			permissionMode:  current.PermissionMode,
			additionalDirs:  current.AdditionalDirs,
			skipPermissions: current.SkipPermissions,
		}
		runOpts.model = config.ResolveModel(runOpts.cwd, runOpts.model)
		if err := runPrint(ctx, runOpts, stdout, stderr); err != nil {
			fmt.Fprintf(stdout, "\n[loop %s] run #%d failed: %v\n", job.ID, runNumber, err)
		} else {
			fmt.Fprintf(stdout, "\n[loop %s] run #%d completed\n", job.ID, runNumber)
		}
		next := time.Now().UTC().Add(interval)
		if _, _, err := store.RecordLoopRun(job.ID, &next); err != nil {
			return err
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func loopRunPrompt(cwd, prompt string) string {
	resolved, ok, err := slashcommands.ResolvePrompt(context.Background(), cwd, prompt)
	if err == nil && ok {
		return resolved
	}
	return prompt
}

func scheduleDaemonCommand(ctx context.Context, stdout, stderr io.Writer) error {
	_ = stdout
	_ = stderr
	store := scheduler.DefaultStore()
	runner := scheduler.Runner{Store: &store, MaxParallel: 2}
	err := runner.Run(ctx)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func scheduleRunCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) < 1 {
		return errors.New("__schedule-run requires a schedule id")
	}
	store := scheduler.DefaultStore()
	schedule, ok, err := store.Find(args[0])
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("schedule not found: %s", args[0])
	}
	if !schedule.Enabled {
		return nil
	}
	if schedule.Kind == sessioncontrol.SessionMonitorScheduleKind {
		return runSessionMonitorChild(ctx, schedule.ID)
	}
	bgStore := background.DefaultStore()
	bg, ok, err := bgStore.MarkRunning(schedule.BackgroundID, os.Getpid())
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("background session not found for schedule %s: %s", schedule.ID, schedule.BackgroundID)
	}
	runNumber := bg.RunCount + 1
	fmt.Fprintf(stdout, "\n[schedule %s] run #%d started at %s\n", schedule.ID, runNumber, time.Now().Format(time.RFC3339))
	runOpts := options{
		print:           true,
		prompt:          loopRunPrompt(schedule.CWD, schedule.Prompt),
		model:           schedule.Model,
		modelExplicit:   strings.TrimSpace(schedule.Model) != "",
		providerName:    schedule.Provider,
		outputFormat:    firstNonEmptyString(schedule.OutputFormat, "text"),
		maxTurns:        schedule.MaxTurns,
		maxTokens:       schedule.MaxTokens,
		cwd:             schedule.CWD,
		resume:          schedule.Resume,
		sessionID:       schedule.SessionID,
		sessionName:     schedule.SessionName,
		noPersistence:   schedule.NoPersistence,
		systemPrompt:    schedule.SystemPrompt,
		appendSystem:    schedule.AppendSystem,
		allowedTools:    schedule.AllowedTools,
		deniedTools:     schedule.DeniedTools,
		permissionMode:  schedule.PermissionMode,
		additionalDirs:  schedule.AdditionalDirs,
		skipPermissions: schedule.SkipPermissions,
	}
	runOpts.model = config.ResolveModel(runOpts.cwd, runOpts.model)
	if err := runPrint(ctx, runOpts, stdout, stderr); err != nil {
		fmt.Fprintf(stdout, "\n[schedule %s] run #%d failed: %v\n", schedule.ID, runNumber, err)
		return err
	}
	fmt.Fprintf(stdout, "\n[schedule %s] run #%d completed\n", schedule.ID, runNumber)
	return nil
}

var runSessionMonitorChild = func(ctx context.Context, scheduleID string) error {
	dsn := firstEnv("NIUMA_MYSQL_DSN", "MYSQL_DSN")
	if strings.TrimSpace(dsn) == "" {
		return errors.New("session monitor MySQL DSN is required")
	}
	repo, err := mysqlstore.OpenGormRepository(ctx, dsn, nil)
	if err != nil {
		return fmt.Errorf("open session monitor MySQL repository: %w", err)
	}
	defer func() { _ = repo.Close() }()
	tenantSvc := tenantservice.NewService(repo, nil)
	service, err := runtimecompose.NewReadService(tenantSvc, nil)
	if err != nil {
		return err
	}
	schedules := scheduler.DefaultStore()
	return (sessioncontrol.SessionMonitorChild{Store: repo, Sessions: service, Schedules: &schedules}).Run(ctx, scheduleID)
}

func backgroundDone(status string) bool {
	return status == "completed" || status == "failed" || status == "killed"
}

func hooksCommand(args []string, stdout io.Writer) error {
	settings := config.LoadGlobalSettings()
	if len(args) == 0 || args[0] == "list" {
		return writePrettyJSON(stdout, settings.Hooks)
	}
	if args[0] == "add" {
		if len(args) < 3 {
			return errors.New("hooks add requires an event and command")
		}
		if settings.Hooks == nil {
			settings.Hooks = map[string][]config.HookCommand{}
		}
		settings.Hooks[args[1]] = append(settings.Hooks[args[1]], config.HookCommand{Command: strings.Join(args[2:], " ")})
		if err := config.SaveGlobalSettings(settings); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "Added hook")
		return nil
	}
	if args[0] == "remove" {
		if len(args) < 3 {
			return errors.New("hooks remove requires an event and index")
		}
		index, err := strconv.Atoi(args[2])
		if err != nil || index < 0 {
			return errors.New("hooks remove index must be a non-negative integer")
		}
		hooks := settings.Hooks[args[1]]
		if index >= len(hooks) {
			return fmt.Errorf("hook index out of range: %d", index)
		}
		settings.Hooks[args[1]] = append(hooks[:index], hooks[index+1:]...)
		if len(settings.Hooks[args[1]]) == 0 {
			delete(settings.Hooks, args[1])
		}
		if err := config.SaveGlobalSettings(settings); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "Removed hook")
		return nil
	}
	if args[0] == "clear" {
		if len(args) > 1 {
			delete(settings.Hooks, args[1])
		} else {
			settings.Hooks = nil
		}
		if err := config.SaveGlobalSettings(settings); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "Cleared hooks")
		return nil
	}
	return fmt.Errorf("unknown hooks command: %s", args[0])
}

func appendUnique(items []string, item string) []string {
	for _, existing := range items {
		if existing == item {
			return items
		}
	}
	return append(items, item)
}

func removeString(items []string, item string) []string {
	out := items[:0]
	for _, existing := range items {
		if existing != item {
			out = append(out, existing)
		}
	}
	return out
}

func getConfigValue(settings config.Settings, key string) (string, bool) {
	switch key {
	case "model":
		return settings.Model, true
	case "provider":
		return settings.Provider, true
	case "permissions.defaultMode":
		return settings.Permissions.DefaultMode, true
	case configKeyRecapAwayDelay:
		if settings.Recap == nil || settings.Recap.AwayDelaySeconds == nil {
			return "", true
		}
		return strconv.Itoa(*settings.Recap.AwayDelaySeconds), true
	case "tui.resumeHistoryLimit":
		if settings.TUI == nil || settings.TUI.ResumeHistoryLimit == nil {
			return "", true
		}
		return strconv.Itoa(*settings.TUI.ResumeHistoryLimit), true
	case configKeyTUIShowThinking:
		return strconv.FormatBool(config.TUIShowThinking(settings)), true
	case configKeyTUIThinkingMode:
		return config.ResolveTUIThinkingMode(settings), true
	case configKeyWebAgentShowThinking:
		return strconv.FormatBool(config.WebAgentUIShowThinking(settings)), true
	case "maxToolResultBytes":
		return strconv.Itoa(settings.MaxToolResultBytes), true
	case "additionalDirectories":
		return strings.Join(settings.AdditionalDirectories, string(os.PathListSeparator)), true
	default:
		if strings.HasPrefix(key, "env.") {
			return settings.Env[strings.TrimPrefix(key, "env.")], true
		}
		return "", false
	}
}

func setConfigValue(settings *config.Settings, key, value string) error {
	switch key {
	case "model":
		settings.Model = value
	case "provider":
		settings.Provider = value
	case "permissions.defaultMode":
		settings.Permissions.DefaultMode = value
	case configKeyRecapAwayDelay:
		seconds, err := strconv.Atoi(value)
		if err != nil || seconds <= 0 {
			return errors.New("recap.awayDelaySeconds must be a positive integer")
		}
		if settings.Recap == nil {
			settings.Recap = &config.RecapSettings{}
		}
		settings.Recap.AwayDelaySeconds = &seconds
	case "tui.resumeHistoryLimit":
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return errors.New("tui.resumeHistoryLimit must be a non-negative integer")
		}
		if settings.TUI == nil {
			settings.TUI = &config.TUISettings{}
		}
		settings.TUI.ResumeHistoryLimit = &n
	case configKeyTUIShowThinking:
		show, err := strconv.ParseBool(value)
		if err != nil {
			return errors.New("tui.showThinking must be true or false")
		}
		if settings.TUI == nil {
			settings.TUI = &config.TUISettings{}
		}
		settings.TUI.ShowThinking = &show
	case configKeyTUIThinkingMode:
		mode, err := config.NormalizeTUIThinkingMode(value)
		if err != nil {
			return err
		}
		if settings.TUI == nil {
			settings.TUI = &config.TUISettings{}
		}
		settings.TUI.ThinkingMode = mode
	case configKeyWebAgentShowThinking:
		show, err := strconv.ParseBool(value)
		if err != nil {
			return errors.New("webAgentUI.showThinking must be true or false")
		}
		if settings.WebAgentUI == nil {
			settings.WebAgentUI = &config.WebAgentUISettings{}
		}
		settings.WebAgentUI.ShowThinking = &show
	case "maxToolResultBytes":
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return errors.New("maxToolResultBytes must be a non-negative integer")
		}
		settings.MaxToolResultBytes = n
	case "additionalDirectories":
		settings.AdditionalDirectories = appendUnique(settings.AdditionalDirectories, value)
	default:
		if strings.HasPrefix(key, "env.") {
			if settings.Env == nil {
				settings.Env = map[string]string{}
			}
			settings.Env[strings.TrimPrefix(key, "env.")] = value
			return nil
		}
		return fmt.Errorf("unsupported config key: %s", key)
	}
	return nil
}

func unsetConfigValue(settings *config.Settings, key string) error {
	switch key {
	case "model":
		settings.Model = ""
	case "provider":
		settings.Provider = ""
	case "permissions.defaultMode":
		settings.Permissions.DefaultMode = ""
	case configKeyRecapAwayDelay:
		if settings.Recap != nil {
			settings.Recap.AwayDelaySeconds = nil
		}
	case "tui.resumeHistoryLimit":
		if settings.TUI != nil {
			settings.TUI.ResumeHistoryLimit = nil
		}
	case configKeyTUIShowThinking:
		if settings.TUI != nil {
			settings.TUI.ShowThinking = nil
		}
	case configKeyTUIThinkingMode:
		if settings.TUI != nil {
			settings.TUI.ThinkingMode = ""
		}
	case configKeyWebAgentShowThinking:
		if settings.WebAgentUI != nil {
			settings.WebAgentUI.ShowThinking = nil
		}
	case "maxToolResultBytes":
		settings.MaxToolResultBytes = 0
	case "additionalDirectories":
		settings.AdditionalDirectories = nil
	default:
		switch {
		case strings.HasPrefix(key, "env."):
			delete(settings.Env, strings.TrimPrefix(key, "env."))
		case strings.HasPrefix(key, "mcpServers."):
			delete(settings.MCPServers, strings.TrimPrefix(key, "mcpServers."))
		default:
			return fmt.Errorf("unsupported config key: %s", key)
		}
	}
	return nil
}

func hasArg(args []string, target string) bool {
	for _, arg := range args {
		if arg == target {
			return true
		}
	}
	return false
}

func writePrettyJSON(stdout io.Writer, value any) error {
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(value)
}

func appendWithBlankLine(base, extra string) string {
	base = strings.TrimSpace(base)
	extra = strings.TrimSpace(extra)
	if base == "" {
		return extra
	}
	if extra == "" {
		return base
	}
	return base + "\n\n" + extra
}

func printHelp(w io.Writer) {
	fmt.Fprint(w, "niuma\n\nUsage:\n")
	fmt.Fprintf(w, "  %s [options]\n", binaryName)
	// Usage 行来自 commandTable()，和 dispatch、completion 同一份数据。
	for _, spec := range commandTable() {
		if spec.hidden {
			continue
		}
		for _, line := range spec.usage {
			fmt.Fprintf(w, "  %s %s\n", binaryName, line)
		}
	}
	fmt.Fprint(w, `
Any command also accepts --help.

Options:
  -p, --print [prompt]         Print mode, runs one headless niuma turn
      --bare                   Minimal code runtime (Read, Edit, Bash; no auto discovery)
      --bg, --background       Queue print mode as a background session
      --model <model>          Model ID (defaults to global settings.model)
      --provider <name>        Select a named provider from fallback.providers
      --output-format <fmt>    text, json, or stream-json (default text)
      --input-format <fmt>     text or stream-json stdin format (default text)
      --json-schema <schema>   Require final response JSON to match schema
      --runtime-trace-output <path>
                                Write a metadata-only runtime-trace-v1 artifact
      --max-parallel-read-only-tools <n>
                                Read-only tool workers: 0 defaults to 4; negative disables
      --include-hook-events    Include hook_start/hook_result in stream-json
      --include-partial-messages
                                Include accumulated partial_message in stream-json
      --include-stream-events   Include upstream-compatible stream_event envelopes
      --max-turns <n>          Maximum tool-use continuations (default 100)
      --max-tokens <n>         Maximum model output tokens per turn
      --system-prompt <text>   Override the base system prompt
      --system-prompt-file <file>
                                Read base system prompt from a file
      --append-system-prompt <text>
                                Append extra instructions to the system prompt
      --append-system-prompt-file <file>
                                Append extra instructions from a file
      --prompt-mode <mode>     code or chat (default code for CLI/TUI)
      --agent <name>           Run as a main-thread agent from .claude/agents
      --settings <file|json>   Merge runtime settings from a file or JSON
      --mcp-config <file|json> Load MCP servers from file(s) or JSON
      --strict-mcp-config      Use only MCP servers from --mcp-config
      --tools <list>           Comma/space-separated enabled tool names
      --allowedTools <list>    Comma-separated tool allow patterns for this run
      --disallowedTools <list> Comma-separated tool deny patterns for this run
      --permission-mode <mode> allow, ask, deny, acceptEdits, auto, plan, default,
                                or bypassPermissions. acceptEdits auto-approves
                                file edits only; bypassPermissions skips every
                                check except explicit deny rules
      --permission-prompt-tool <tool>
                                Tool to ask for headless permission approvals
      --add-dir <dir>          Additional writable directory for this run
      --dangerously-skip-permissions
                                Bypass permission policy for this run
      --cwd <dir>              Working directory for tools
  -r, --resume [session_id]    Resume context from a previous transcript
      --resume-session-at <id> Resume only through the given transcript entry id
      --rewind-files <id>      Restore files to a previous user message id and exit
  -c, --continue               Continue the latest transcript
      --session-id <uuid>      Create or continue this session (alias: --sessionId)
  -n, --name <name>            Set a display name for this session
      --no-session-persistence Do not write a transcript for this run
  -v, --version                Show version
  -h, --help                   Show help

Environment:
  NIUMA_CONFIG_DIR             Optional compatibility override for config directory
  NIUMA_LOG_LEVEL             Diagnostic log level on stderr: debug, info, warn,
                                error, dpanic, panic, fatal, silent. Unset means
                                silent for CLI runs; the server always logs JSON
  NIUMA_MYSQL_DSN             MySQL DSN for tenant migrations and agent tasks
  NIUMA_TENANT_KEY            Tenant key for tenant CLI commands
  NIUMA_USER_ID               User key for tenant CLI commands

Implemented tools:
  Task, TaskOutput, AskUserQuestion, EnterPlanMode, ExitPlanMode, ListMcpResources, ReadMcpResource, Read, Write, Edit, MultiEdit, NotebookRead, NotebookEdit, LS, Glob, Grep, LSP, TodoRead, TodoWrite, Skill, Workflow, Worktree, WebBrowser, WebFetch, WebSearch, PowerShell, Bash

`)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
