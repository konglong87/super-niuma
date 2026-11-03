package execution

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/query"
	"github.com/konglong87/super-niuma/internal/runtimeprofile"
	"github.com/konglong87/super-niuma/internal/telemetry"
	"github.com/konglong87/super-niuma/internal/tools"
)

// Runner never owns scheduling, background tasks, or interactive session state.
// resolve must return a trusted configuration, not load the worker workspace.
type Runner struct {
	resolve   func(context.Context) (config.Config, error)
	logger    *slog.Logger
	newClient func(config.Config) (query.MessageStreamer, error)
}

func New(resolve func(context.Context) (config.Config, error), logger *slog.Logger) *Runner {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Runner{resolve: resolve, logger: logger, newClient: newHTTPStreamer}
}

func (r *Runner) Preflight(ctx context.Context, provider, model string) error {
	if ctx == nil {
		return ErrInvalid
	}
	_, err := r.RouteFingerprint(ctx, provider, model)
	return err
}

func (r *Runner) configuration(ctx context.Context, provider, model string) (config.Config, error) {
	if err := ctx.Err(); err != nil {
		return config.Config{}, err
	}
	if (provider != "" && !validID(provider)) || !validID(model) {
		return config.Config{}, ErrUnavailable
	}
	if r == nil || r.resolve == nil || r.newClient == nil {
		return config.Config{}, ErrUnavailable
	}
	cfg, err := r.resolve(ctx)
	if err != nil {
		return config.Config{}, safeError(ctx, ErrUnavailable)
	}
	// Select only a named route from the explicit resolver snapshot. Selection
	// never means trying the other routes if this one fails.
	if provider != "" && provider != cfg.SelectedProvider && !(cfg.SelectedProvider == "" && provider == cfg.Provider) {
		cfg, err = cfg.SelectProvider(provider)
		if err != nil {
			return config.Config{}, ErrUnavailable
		}
	}
	if cfg.SelectedProviderModel != "" && cfg.SelectedProviderModel != model {
		return config.Config{}, ErrUnavailable
	}
	cfg.FallbackProviders = nil
	cfg.Settings.Model = model
	if err := validateConfig(cfg); err != nil {
		return config.Config{}, err
	}
	return cfg, safeError(ctx, nil)
}

func (r *Runner) Execute(ctx context.Context, request Request) (result Result, retErr error) {
	if ctx == nil {
		return result, ErrInvalid
	}
	prompt, system, err := validateRequest(request)
	if err != nil {
		return result, err
	}
	// Copy mutable inputs before creating this attempt's isolated capabilities.
	request.ReadPaths = append([]string(nil), request.ReadPaths...)
	request.WritePaths = append([]string(nil), request.WritePaths...)
	runCtx, cancel := context.WithCancel(telemetry.WithEmitter(observability.WithoutLogging(ctx), telemetry.NewEmitter()))
	defer cancel()
	state := &attemptState{request: request, cancel: cancel, logger: rLogger(r)}
	state.log(actionAttemptStart, codeOK)
	defer func() { state.log(actionAttemptEnd, errorCode(retErr)) }()
	if err := state.authorize(runCtx); err != nil {
		return result, err
	}
	cfg, err := r.configuration(runCtx, request.Provider, request.Model)
	if err != nil {
		return result, err
	}
	fs, err := openWorkspace(request)
	if err != nil {
		return result, err
	}
	defer fs.Close()
	client, err := r.newClient(cfg)
	if err != nil {
		return result, ErrUnavailable
	}
	if transport, ok := client.(*httpStreamer); ok {
		transport.authorize = state.authorize
	}
	metered := &meteredStreamer{inner: client, state: state, usageKnown: true}
	registry := newRegistry(fs, state)
	sess := query.New(metered, registry, query.Options{
		RuntimeProfile:       runtimeprofile.ProfileBare,
		OverrideSystemPrompt: system, PromptMode: "chat", Effort: "off",
		Model: request.Model, MaxTurns: request.MaxTurns, MaxTokens: request.MaxOutputTokens,
		RunID: request.AttemptID, CWD: request.Workspace,
		MaxParallelReadOnlyTools: -1,
		// No hooks, recorder, tasks, MCP, attachments, discovery, compact or skills.
	})
	writer := &boundedWriter{state: state}
	qr, runErr := sess.RunWithCallbacks(runCtx, prompt, writer, query.RunCallbacks{
		OnToolCall: func(event query.ToolCallEvent) error {
			if err := state.authorize(runCtx); err != nil {
				return err
			}
			if _, ok := registry.Get(event.Name); !ok {
				return state.fail(ErrAuthority)
			}
			state.toolCalls++
			if state.toolCalls > maxToolCalls {
				return state.fail(ErrFailed)
			}
			return nil
		},
	})
	result.Usage, result.UsageKnown, result.ToolCalls = metered.usage, metered.usageKnown, state.toolCalls
	result.Writes = append([]WriteReceipt(nil), state.writes...)
	if state.err != nil {
		return result, state.err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if runErr != nil {
		return result, ErrFailed
	}
	if err := state.authorize(runCtx); err != nil {
		return result, err
	}
	if qr.PendingInteraction != nil || qr.StopReason != stopComplete || strings.TrimSpace(qr.Response) == "" ||
		!utf8.ValidString(qr.Response) || len(qr.Response) > MaxResultBytes {
		return result, ErrFailed
	}
	result.Text = qr.Response
	return result, nil
}

func rLogger(r *Runner) *slog.Logger {
	if r != nil && r.logger != nil {
		return r.logger
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func safeError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func errorCode(err error) string {
	switch {
	case err == nil:
		return codeOK
	case errors.Is(err, context.Canceled):
		return codeCancelled
	case errors.Is(err, context.DeadlineExceeded):
		return codeDeadline
	case errors.Is(err, ErrAuthority):
		return codeAuthority
	case errors.Is(err, ErrBudget):
		return codeBudget
	case errors.Is(err, ErrInvalid):
		return codeInvalid
	case errors.Is(err, ErrUnavailable):
		return codeUnavailable
	default:
		return codeFailed
	}
}

var _ Executor = (*Runner)(nil)
var _ tools.Tool = (*fileTool)(nil)
