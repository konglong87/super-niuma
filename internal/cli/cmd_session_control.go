package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/query"
	"github.com/konglong87/super-niuma/internal/scheduler"
	"github.com/konglong87/super-niuma/internal/server"
	"github.com/konglong87/super-niuma/internal/session"
	control "github.com/konglong87/super-niuma/internal/sessioncontrol"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	tenantservice "github.com/konglong87/super-niuma/internal/tenant"
)

// SessionControlService is the direct, transport-neutral managed-session port.
// The CLI deliberately never calls the HTTP API for these operations.
type SessionControlService interface {
	Create(context.Context, control.CreateRequest) (control.OperationResult, error)
	List(context.Context, control.ListRequest) ([]control.SessionSnapshot, error)
	Get(context.Context, control.GetRequest) (control.SessionSnapshot, error)
	Send(context.Context, control.SendRequest) (control.OperationResult, error)
	Stop(context.Context, control.StopRequest) (control.OperationResult, error)
	Attach(context.Context, control.AttachRequest) (control.OperationResult, error)
	Monitor(context.Context, control.MonitorRequest) (control.OperationResult, error)
}

// SessionControlRuntime is the production composition seam. Server/CLI startup
// must derive Context from trusted runtime configuration, never command flags.
type SessionControlRuntime struct {
	Service SessionControlService
	Context control.RequestContext
	CWD     string
	Close   func() error
}

// newSessionControlRuntime is replaced by the production composition once the
// Session Control runtime owns its concrete storage/controller dependencies.
// Keeping the default fail-closed prevents an accidental local fallback.
var newSessionControlRuntime = productionSessionControlRuntime

type sessionControlCLIOptionsKey struct{}

func withSessionControlCLIOptions(ctx context.Context, opts options) context.Context {
	return context.WithValue(ctx, sessionControlCLIOptionsKey{}, opts)
}

func productionSessionControlRuntime(ctx context.Context) (SessionControlRuntime, error) {
	dsn := firstEnv("NIUMA_MYSQL_DSN", "MYSQL_DSN")
	tenantKey := firstEnv("NIUMA_TENANT_KEY", "TENANT_KEY")
	userKey := firstEnv("NIUMA_USER_ID", "USER_ID")
	if dsn == "" || tenantKey == "" || userKey == "" {
		return SessionControlRuntime{}, fmt.Errorf("managed session control is unavailable: MySQL DSN, tenant key, and user key are required")
	}
	repo, err := mysqlstore.OpenGormRepository(ctx, dsn, nil)
	if err != nil {
		return SessionControlRuntime{}, fmt.Errorf("open managed session storage: %w", err)
	}
	closeOnError := func(err error) (SessionControlRuntime, error) {
		_ = repo.Close()
		return SessionControlRuntime{}, err
	}
	tenantSvc := tenantservice.NewService(repo, nil)
	trustedCtx := observability.WithRequestValues(ctx, sessionControlTraceID(), userKey, tenantKey)
	trustedCtx, resolved, err := tenantSvc.ResolveContextOnce(trustedCtx)
	if err != nil {
		return closeOnError(fmt.Errorf("resolve managed session identity: %w", err))
	}
	opts, _ := trustedCtx.Value(sessionControlCLIOptionsKey{}).(options)
	allowedCWDRoots, err := resolveAllowedCWDRoots(opts.cwd)
	if err != nil {
		return closeOnError(err)
	}
	controller := agenttasks.NewController()
	serverOpts := server.Options{
		Workspace: opts.cwd, TenantService: tenantSvc, AgentTaskStore: tenantSvc,
		PendingInputQueue: repo, AgentTaskController: controller,
		SessionControlEnableLocalRead: firstEnvBool("NIUMA_SESSION_CONTROL_LOCAL_READ"),
		SessionControlCWDValidator: func(cwd string) (string, error) {
			return resolveRequestCWD(cwd, allowedCWDRoots)
		},
	}
	scheduleStore := scheduler.DefaultStore()
	executable, err := os.Executable()
	if err != nil {
		return closeOnError(fmt.Errorf("resolve scheduler executable: %w", err))
	}
	serverOpts.SessionMonitor = control.NewSessionMonitor(control.SessionMonitorDependencies{
		Store: tenantSvc, Schedules: &scheduleStore,
		EnsureDaemon: func() error {
			pid, _, ensureErr := scheduleStore.EnsureDaemon(executable, os.Environ())
			if ensureErr != nil {
				return ensureErr
			}
			if pid <= 0 {
				return fmt.Errorf("scheduler daemon returned no pid")
			}
			return nil
		},
	})
	serverOpts.StreamQueryFunc = func(runCtx context.Context, req server.QueryRequest, sink io.Writer) (query.Result, error) {
		queryOpts := opts
		if req.CWD != "" {
			queryOpts.cwd = req.CWD
		}
		if req.Model != "" {
			queryOpts.model = req.Model
		}
		if req.Provider != "" {
			queryOpts.providerName = req.Provider
		}
		queryOpts.tenantID = req.TenantID
		queryOpts.tenantUserID = req.UserID
		queryOpts.tenantSessionID = req.TenantSessionID
		queryOpts.traceID = req.TraceID
		queryOpts.agentTaskStore = tenantSvc
		queryOpts.agentTaskController = controller
		querySession, cleanup, queryErr := newQuerySession(runCtx, queryOpts, req.InitialMessages, nil)
		if queryErr != nil {
			return query.Result{}, queryErr
		}
		defer cleanup()
		return querySession.Run(runCtx, req.Prompt, sink)
	}
	server.PrepareSessionControlRuntime(trustedCtx, &serverOpts, func(runCtx context.Context, req server.QueryRequest) (query.Result, error) {
		return serverOpts.StreamQueryFunc(runCtx, req, io.Discard)
	})
	service, err := server.NewSessionControlService(serverOpts, func(runCtx context.Context, req server.QueryRequest) (query.Result, error) {
		return serverOpts.StreamQueryFunc(runCtx, req, io.Discard)
	})
	if err != nil {
		return closeOnError(fmt.Errorf("compose managed session control: %w", err))
	}
	return SessionControlRuntime{
		Service: service,
		Context: control.RequestContext{TenantID: resolved.TenantID, UserID: resolved.UserID, ActorUserID: resolved.UserID, TraceID: observability.TraceID(trustedCtx)},
		CWD:     opts.cwd,
		Close:   repo.Close,
	}, nil
}

func sessionControlTraceID() string {
	data := make([]byte, 12)
	if _, err := rand.Read(data); err == nil {
		return "session-control-" + hex.EncodeToString(data)
	}
	return fmt.Sprintf("session-control-%d", time.Now().UnixNano())
}

func managedSessionRuntime(ctx context.Context) (SessionControlRuntime, error) {
	runtime, err := newSessionControlRuntime(ctx)
	if err != nil {
		return SessionControlRuntime{}, err
	}
	if runtime.Service == nil || runtime.Context.TenantID == 0 || runtime.Context.UserID == 0 || runtime.Context.ActorUserID == 0 {
		if runtime.Close != nil {
			_ = runtime.Close()
		}
		return SessionControlRuntime{}, fmt.Errorf("managed session control is unavailable: trusted runtime identity is not configured")
	}
	return runtime, nil
}

func closeManagedSessionRuntime(runtime SessionControlRuntime) {
	if runtime.Close != nil {
		_ = runtime.Close()
	}
}

func sessionManagedListCommand(ctx context.Context, args []string, stdout io.Writer) error {
	source, err := oneFlagValue(args, "--source")
	if err != nil {
		return err
	}
	jsonOutput := hasArg(args, "--json")
	switch source {
	case "local":
		if jsonOutput {
			summaries, err := session.DefaultStore().List()
			if err != nil {
				return err
			}
			return writePrettyJSON(stdout, summaries)
		}
		return sessionListCommand(session.DefaultStore(), "", stdout)
	case "tenant":
		runtime, err := managedSessionRuntime(ctx)
		if err != nil {
			return err
		}
		defer closeManagedSessionRuntime(runtime)
		items, err := runtime.Service.List(ctx, control.ListRequest{Context: runtime.Context, Source: control.SourceTenant, Limit: sessionListLimit(args)})
		if err != nil {
			return err
		}
		return writeManagedSessionList(stdout, items, jsonOutput)
	case "all":
		runtime, err := managedSessionRuntime(ctx)
		if err != nil {
			return err
		}
		defer closeManagedSessionRuntime(runtime)
		tenantItems, err := runtime.Service.List(ctx, control.ListRequest{Context: runtime.Context, Source: control.SourceTenant, Limit: sessionListLimit(args)})
		if err != nil {
			return err
		}
		localItems, err := session.DefaultStore().List()
		if err != nil {
			return err
		}
		if jsonOutput {
			return writePrettyJSON(stdout, map[string]any{"tenant": tenantItems, "local": localItems})
		}
		if err := writeManagedSessionList(stdout, tenantItems, false); err != nil {
			return err
		}
		return sessionListCommand(session.DefaultStore(), "", stdout)
	default:
		return fmt.Errorf("session list --source must be local, tenant, or all")
	}
}

func sessionManagedCreateCommand(ctx context.Context, args []string, stdout io.Writer) error {
	runtime, err := managedSessionRuntime(ctx)
	if err != nil {
		return err
	}
	defer closeManagedSessionRuntime(runtime)
	key, _ := optionalFlagValue(args, "--key")
	title, _ := optionalFlagValue(args, "--title")
	model, _ := optionalFlagValue(args, "--model")
	initialText, _ := optionalFlagValue(args, "--initial-text")
	result, err := runtime.Service.Create(ctx, control.CreateRequest{Context: runtime.Context, SessionKey: key, Title: title, Model: model, CWD: runtime.CWD, InitialText: initialText, IdempotencyKey: sessionIdempotencyKey(args)})
	if err != nil {
		return err
	}
	return writeManagedOperation(stdout, result, hasArg(args, "--json"))
}

func sessionManagedGetCommand(ctx context.Context, args []string, stdout io.Writer) error {
	runtime, err := managedSessionRuntime(ctx)
	if err != nil {
		return err
	}
	defer closeManagedSessionRuntime(runtime)
	ref, err := sessionRefArg(args, "session get requires tenant:<key> or local:<id>")
	if err != nil {
		return err
	}
	item, err := runtime.Service.Get(ctx, control.GetRequest{Context: runtime.Context, Ref: ref})
	if err != nil {
		return err
	}
	if hasArg(args, "--json") {
		return writePrettyJSON(stdout, item)
	}
	return writeManagedSnapshot(stdout, item)
}

func sessionManagedSendCommand(ctx context.Context, args []string, stdout io.Writer) error {
	runtime, err := managedSessionRuntime(ctx)
	if err != nil {
		return err
	}
	defer closeManagedSessionRuntime(runtime)
	positional := sessionPositionals(args)
	if len(positional) < 2 {
		return fmt.Errorf("session send requires tenant:<key> and message text")
	}
	ref, err := managedMutationRef(positional[0])
	if err != nil {
		return err
	}
	result, err := runtime.Service.Send(ctx, control.SendRequest{Context: runtime.Context, Ref: ref, Content: strings.Join(positional[1:], " "), IdempotencyKey: sessionIdempotencyKey(args)})
	if err != nil {
		return err
	}
	return writeManagedOperation(stdout, result, hasArg(args, "--json"))
}

func sessionManagedStopCommand(ctx context.Context, args []string, stdout io.Writer) error {
	runtime, err := managedSessionRuntime(ctx)
	if err != nil {
		return err
	}
	defer closeManagedSessionRuntime(runtime)
	ref, err := sessionManagedRefArg(args, "session stop requires tenant:<key>")
	if err != nil {
		return err
	}
	result, err := runtime.Service.Stop(ctx, control.StopRequest{Context: runtime.Context, Ref: ref, IdempotencyKey: sessionIdempotencyKey(args)})
	if err != nil {
		return err
	}
	return writeManagedOperation(stdout, result, hasArg(args, "--json"))
}

func sessionManagedAttachCommand(ctx context.Context, args []string, stdout io.Writer) error {
	runtime, err := managedSessionRuntime(ctx)
	if err != nil {
		return err
	}
	defer closeManagedSessionRuntime(runtime)
	target, err := sessionManagedRefArg(args, "session attach requires tenant:<target>")
	if err != nil {
		return err
	}
	sources, err := sessionRefsAfterFlag(args, "--from")
	if err != nil || len(sources) == 0 {
		if err != nil {
			return err
		}
		return fmt.Errorf("session attach requires --from <source-ref>")
	}
	taskID, err := sessionUintFlag(args, "--target-task-id")
	if err != nil {
		return err
	}
	window, err := sessionIntFlag(args, "--context-window")
	if err != nil {
		return err
	}
	result, err := runtime.Service.Attach(ctx, control.AttachRequest{Context: runtime.Context, Target: target, TargetTaskID: taskID, TargetContextWindowTokens: window, Sources: sources, RelationType: valueOrDefault(args, "--relation", "handoff"), IdempotencyKey: sessionIdempotencyKey(args)})
	if err != nil {
		return err
	}
	return writeManagedOperation(stdout, result, hasArg(args, "--json"))
}

func sessionManagedMonitorCommand(ctx context.Context, args []string, stdout io.Writer) error {
	runtime, err := managedSessionRuntime(ctx)
	if err != nil {
		return err
	}
	defer closeManagedSessionRuntime(runtime)
	target, err := sessionManagedRefArg(args, "session monitor requires tenant:<key>")
	if err != nil {
		return err
	}
	every, err := oneFlagValue(args, "--every")
	if err != nil {
		return fmt.Errorf("session monitor requires --every <duration>")
	}
	duration, err := time.ParseDuration(every)
	if err != nil || duration <= 0 || duration%time.Second != 0 {
		return fmt.Errorf("--every must be a positive whole-second duration")
	}
	channel, err := oneFlagValue(args, "--channel")
	if err != nil || channel != "feishu" {
		return fmt.Errorf("session monitor requires --channel feishu")
	}
	sources, err := sessionRefsAfterFlag(args, "--from")
	if err != nil {
		return err
	}
	result, err := runtime.Service.Monitor(ctx, control.MonitorRequest{Context: runtime.Context, Target: target, Sources: sources, IntervalSeconds: int(duration / time.Second), Channel: channel, IdempotencyKey: sessionIdempotencyKey(args)})
	if err != nil {
		return err
	}
	return writeManagedOperation(stdout, result, hasArg(args, "--json"))
}

func sessionRefArg(args []string, message string) (control.SessionRef, error) {
	positional := sessionPositionals(args)
	if len(positional) == 0 {
		return control.SessionRef{}, fmt.Errorf("%s", message)
	}
	return control.ParseRef(positional[0])
}
func sessionManagedRefArg(args []string, message string) (control.SessionRef, error) {
	ref, err := sessionRefArg(args, message)
	if err != nil {
		return ref, err
	}
	if ref.Source != control.SourceTenant {
		return control.SessionRef{}, fmt.Errorf("managed session mutations require a tenant: ref")
	}
	return ref, nil
}
func managedMutationRef(raw string) (control.SessionRef, error) {
	ref, err := control.ParseRef(raw)
	if err != nil {
		return ref, err
	}
	if ref.Source != control.SourceTenant {
		return control.SessionRef{}, fmt.Errorf("managed session mutations require a tenant: ref")
	}
	return ref, nil
}
func sessionRefsAfterFlag(args []string, flag string) ([]control.SessionRef, error) {
	values := valuesAfterFlag(args, flag)
	refs := make([]control.SessionRef, 0, len(values))
	for _, value := range values {
		ref, err := control.ParseRef(value)
		if err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, nil
}
func sessionPositionals(args []string) []string {
	var values []string
	needsValue := false
	for _, arg := range args {
		if needsValue {
			needsValue = false
			continue
		}
		switch arg {
		case "--idempotency-key", "--source", "--key", "--title", "--model", "--initial-text", "--from", "--target-task-id", "--context-window", "--relation", "--every", "--channel", "--limit":
			needsValue = true
		default:
			if !strings.HasPrefix(arg, "--") {
				values = append(values, arg)
			}
		}
	}
	return values
}
func sessionIdempotencyKey(args []string) string {
	if value, ok := optionalFlagValue(args, "--idempotency-key"); ok && strings.TrimSpace(value) != "" {
		return value
	}
	data := make([]byte, 16)
	if _, err := rand.Read(data); err == nil {
		return hex.EncodeToString(data)
	}
	return fmt.Sprintf("cli-%d", time.Now().UnixNano())
}
func optionalFlagValue(args []string, flag string) (string, bool) {
	values := valuesAfterFlag(args, flag)
	if len(values) == 0 {
		return "", false
	}
	return values[len(values)-1], true
}
func oneFlagValue(args []string, flag string) (string, error) {
	value, ok := optionalFlagValue(args, flag)
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s requires a value", flag)
	}
	return value, nil
}
func valueOrDefault(args []string, flag, fallback string) string {
	value, ok := optionalFlagValue(args, flag)
	if !ok {
		return fallback
	}
	return value
}
func sessionIntFlag(args []string, flag string) (int, error) {
	value, err := oneFlagValue(args, flag)
	if err != nil {
		return 0, err
	}
	var parsed int
	if _, err := fmt.Sscan(value, &parsed); err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", flag)
	}
	return parsed, nil
}
func sessionUintFlag(args []string, flag string) (uint64, error) {
	value, err := oneFlagValue(args, flag)
	if err != nil {
		return 0, err
	}
	var parsed uint64
	if _, err := fmt.Sscan(value, &parsed); err != nil || parsed == 0 {
		return 0, fmt.Errorf("%s must be a positive integer", flag)
	}
	return parsed, nil
}
func sessionListLimit(args []string) int {
	value, ok := optionalFlagValue(args, "--limit")
	if !ok {
		return 0
	}
	var limit int
	if _, err := fmt.Sscan(value, &limit); err != nil || limit < 1 {
		return 0
	}
	return limit
}

func writeManagedOperation(stdout io.Writer, result control.OperationResult, jsonOutput bool) error {
	if jsonOutput {
		return writePrettyJSON(stdout, result)
	}
	_, err := fmt.Fprintf(stdout, "%s status=%s ref=%s run_id=%d schedule_id=%s audit_id=%d\n", result.OperationID, result.Session.Status, result.Session.Ref.String(), result.RunID, result.ScheduleID, result.AuditID)
	return err
}
func writeManagedSnapshot(stdout io.Writer, item control.SessionSnapshot) error {
	_, err := fmt.Fprintf(stdout, "status=%s ref=%s run_id=%d\n", item.Status, item.Ref.String(), item.ActiveRunID)
	return err
}
func writeManagedSessionList(stdout io.Writer, items []control.SessionSnapshot, jsonOutput bool) error {
	if jsonOutput {
		return writePrettyJSON(stdout, items)
	}
	for _, item := range items {
		if err := writeManagedSnapshot(stdout, item); err != nil {
			return err
		}
	}
	return nil
}
