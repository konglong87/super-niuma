package anthropic

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	sdkanthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/product"
	"github.com/konglong87/super-niuma/internal/telemetry"
	openairesponses "github.com/openai/openai-go/v3"
	openai "github.com/sashabaranov/go-openai"
)

type Client struct {
	cfg       config.Config
	providers []providerClient
	breaker   providerBreaker
	registry  backendRegistry
}

type providerClient struct {
	name      string
	role      string
	kind      string
	endpoint  string
	model     string
	apiKey    string
	authToken string
	protocol  config.ProviderProtocol
	source    protocolSource
	backend   providerBackend
	initErr   error
	sdk       sdkanthropic.Client
	openAI    *openai.Client
}

const (
	openAIStreamCreateRateLimitRetryDelayEnv  = "NIUMA_OPENAI_RATE_LIMIT_RETRY_DELAY_MS"
	defaultOpenAIStreamCreateRateLimitDelay   = 65 * time.Second
	defaultOpenAIStreamCreateRateLimitRetries = 1
	// 瞬时故障（5xx / 408 / 409 / 网络抖动）的建流重试次数。刻意比 rate limit 宽松但
	// 退避很短：这一段只兜真正的抖动，持续性故障交给 provider fallback，别把切换拖慢
	// （AUDIT-P1-08）。
	openAIStreamCreateTransientRetries = 2
	// JSON event 截断在仅收到 reasoning summary 时允许六次额外编排尝试。
	// 其他没有任何输出的 Responses 中途流错误保持原有的一次重试预算。
	responsesJSONMidstreamRetries           = 6
	responsesOtherMidstreamRetries          = 1
	responsesRetryReasonReasoningOnlyDecode = "reasoning_only_stream_decode"
	responsesRetryOutcomeRecovered          = "recovered"
	responsesRetryOutcomeExhausted          = "exhausted"
	responsesRetryOutcomeCommittedOutput    = "committed_output"
	responsesRetryOutcomeCancelled          = "cancelled"
)

type responsesRetryAttemptContextKey struct{}

func withResponsesRetryAttempt(ctx context.Context, attempt int) context.Context {
	return context.WithValue(ctx, responsesRetryAttemptContextKey{}, attempt)
}

func responsesRetryAttempt(ctx context.Context) int {
	if ctx == nil {
		return 0
	}
	attempt, _ := ctx.Value(responsesRetryAttemptContextKey{}).(int)
	return attempt
}

// 瞬时故障的退避基数与上限，指数增长。做成变量是为了让测试压到毫秒级 —— 与
// providerTimeouts 同一个理由。
var (
	openAIStreamCreateBackoff    = 500 * time.Millisecond
	openAIStreamCreateMaxBackoff = 8 * time.Second
	responsesMidstreamBackoff    = 300 * time.Millisecond
	responsesMidstreamMaxBackoff = 5 * time.Second
)

func NewClient(cfg config.Config) *Client {
	registry := newBackendRegistry()
	primaryName := strings.TrimSpace(cfg.SelectedProvider)
	if primaryName == "" {
		primaryName = "primary"
	}
	primary := newProviderClient(primaryName, "primary", config.ProviderConfig{
		Type:      cfg.Provider,
		Protocol:  cfg.ProviderProtocol,
		BaseURL:   cfg.BaseURL,
		APIKey:    cfg.APIKey,
		AuthToken: cfg.AuthToken,
		Responses: cfg.Responses,
	}, registry)
	providers := []providerClient{primary}
	for i, provider := range cfg.FallbackProviders {
		name := provider.FallbackRouteName(i)
		fallback := newProviderClient(name, "fallback", provider, registry)
		providers = append(providers, fallback)
	}
	return &Client{cfg: cfg, providers: providers, registry: registry}
}

func newProviderClient(name, role string, provider config.ProviderConfig, registry backendRegistry) providerClient {
	kind := strings.ToLower(strings.TrimSpace(provider.Type))
	if kind == "" {
		// Legacy callers inside this protocol adapter use Messages. Product
		// configuration is validated separately and has no default provider.
		kind = "anthropic"
	}
	resolved, resolveErr := config.ResolveProviderProtocol(kind, provider.Protocol, provider.Responses)
	baseURL := strings.TrimRight(strings.TrimSpace(provider.BaseURL), "/")
	if baseURL == "" {
		resolveErr = errors.New("provider endpoint is missing; configure baseURL in ~/.niuma/settings.json")
	}
	opts := []option.RequestOption{
		option.WithoutEnvironmentDefaults(),
		option.WithBaseURL(baseURL),
		option.WithMaxRetries(2),
	}
	httpTraceClient := newHTTPTraceClient(nil)
	opts = append(opts, option.WithHTTPClient(httpTraceClient))
	if provider.APIKey != "" {
		opts = append(opts, option.WithAPIKey(provider.APIKey))
	}
	if provider.AuthToken != "" {
		opts = append(opts, option.WithAuthToken(provider.AuthToken))
	}
	openAIAuth := firstNonEmpty(provider.APIKey, provider.AuthToken)
	openAIConfig := openai.DefaultConfig(openAIAuth)
	openAIConfig.BaseURL = baseURL
	openAIConfig.HTTPClient = httpTraceClient
	client := providerClient{
		name:      name,
		role:      role,
		kind:      kind,
		endpoint:  sanitizeEndpoint(baseURL),
		model:     strings.TrimSpace(provider.Model),
		apiKey:    provider.APIKey,
		authToken: provider.AuthToken,
		protocol:  resolved.Protocol,
		source:    protocolSourceLegacyDerived,
		initErr:   resolveErr,
		sdk:       sdkanthropic.NewClient(opts...),
		openAI:    openai.NewClientWithConfig(openAIConfig),
	}
	if resolved.Explicit {
		client.source = protocolSourceExplicit
	}
	if resolveErr == nil && resolved.Explicit && resolved.Protocol == config.ProviderProtocolOpenAIResponses {
		client.backend, client.initErr = registry.newBackend(resolved.Protocol, providerBackendSpec{
			name: name, role: role, kind: kind, baseURL: baseURL, endpoint: client.endpoint,
			apiKey: provider.APIKey, authToken: provider.AuthToken,
		})
	}
	return client
}

func sanitizeEndpoint(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed.String()
}

type StreamCallbacks struct {
	OnText          func(text string) error
	OnThinking      func(text string) error
	OnSignature     func(signature string) error
	OnCitation      func(citation json.RawMessage) error
	OnConnectorText func(text string) error
}

func (c *Client) StreamMessages(ctx context.Context, req MessagesRequest, cb StreamCallbacks) (*StreamResult, error) {
	if req.MaxTokens == 0 {
		// Anthropic 要求 max_tokens > thinking.budget_tokens；OpenAI 侧
		// reasoning tokens 也算在 completion tokens 里，同样需要更大的默认值。
		req.MaxTokens = 4096
		if req.Thinking != nil && req.Thinking.Type == "enabled" && req.Thinking.BudgetTokens > 0 {
			req.MaxTokens = req.Thinking.BudgetTokens + 4096
		}
	}

	var failures []providerFailure
	providers := c.providers
	if len(providers) == 0 {
		providers = []providerClient{newProviderClient("primary", "primary", config.ProviderConfig{
			Type:      c.cfg.Provider,
			Protocol:  c.cfg.ProviderProtocol,
			BaseURL:   c.cfg.BaseURL,
			APIKey:    c.cfg.APIKey,
			AuthToken: c.cfg.AuthToken,
			Responses: c.cfg.Responses,
		}, c.registry)}
	}
	providers = orderProvidersForRequest(providers, req.Model)
	// 刚失败过的 provider 这一轮直接跳过，别再赔一整个建流超时才切到 fallback
	// （AUDIT-P1-08）。全员冷却时 cooling 返回 nil，也就是照常从队首试。
	cooling := c.breaker.cooling(providers)
	for index, provider := range providers {
		if remaining, ok := cooling[provider.name]; ok {
			err := fmt.Errorf("skipped: cooling down for another %s after a recent failure", remaining.Round(time.Second))
			failures = append(failures, providerFailure{name: provider.name, err: err})
			observability.Debug(ctx, nil, "anthropic", "StreamMessages", "provider in cooldown; skipping", "provider", provider.name, "remaining", remaining.String())
			continue
		}
		if !providerKindSupported(provider.kind) {
			err := fmt.Errorf("unsupported provider type %q", provider.kind)
			failures = append(failures, providerFailure{name: provider.name, err: err})
			continue
		}
		if provider.initErr != nil {
			failures = append(failures, providerFailure{name: provider.name, err: provider.initErr})
			continue
		}
		if provider.apiKey == "" && provider.authToken == "" {
			err := errors.New("provider credentials are missing; configure apiKey or authToken in ~/.niuma/settings.json")
			failures = append(failures, providerFailure{name: provider.name, err: err})
			continue
		}
		attemptReq := req
		if provider.model != "" {
			attemptReq.Model = provider.model
		}
		var lastRetryErr error
		var lastRetryLimit int
		for midstreamAttempt := 0; ; midstreamAttempt++ {
			responsesAttempt := provider.protocol == config.ProviderProtocolOpenAIResponses && provider.source == protocolSourceExplicit
			emitted := false
			attemptCB := callbacksMarkingEmitted(cb, &emitted)
			var callbackBuffer *responsesAttemptCallbackBuffer
			if responsesAttempt {
				callbackBuffer = newResponsesAttemptCallbackBuffer(cb)
				attemptCB = callbackBuffer.callbacks()
			}
			attemptCtx := withResponsesRetryAttempt(ctx, midstreamAttempt)
			res, err := c.streamMessagesWithProvider(attemptCtx, provider, attemptReq, attemptCB)
			if err == nil {
				if callbackBuffer != nil {
					if flushErr := callbackBuffer.flush(); flushErr != nil {
						return nil, flushErr
					}
				}
				c.breaker.reset(provider.name)
				if responsesAttempt && midstreamAttempt > 0 {
					emitResponsesRetryOutcome(ctx, provider, attemptReq, responsesRetryOutcomeRecovered, midstreamAttempt, lastRetryLimit, lastRetryErr)
				}
				if index > 0 || midstreamAttempt > 0 {
					observability.Info(ctx, nil, "anthropic", "StreamMessages", "provider stream recovered", "provider", provider.name, "attempt", index+1, "midstream_retry", midstreamAttempt)
				}
				return res, nil
			}
			committed := emitted
			if callbackBuffer != nil {
				committed = callbackBuffer.committed()
			}
			if callbackBuffer != nil && responsesStreamHasToolCommittedOutput(err) {
				if flushErr := callbackBuffer.commitAndFlush(); flushErr != nil {
					if isRetryableResponsesStreamError(err) {
						c.breaker.trip(provider.name)
					}
					return nil, flushErr
				}
				committed = true
			}
			committed = committed || responsesStreamHasCommittedOutput(err)
			retryLimit := responsesStreamRetryLimit(err, committed)
			if midstreamAttempt < retryLimit {
				lastRetryErr = err
				lastRetryLimit = retryLimit
				delay := responsesMidstreamBackoffDelay(midstreamAttempt)
				observability.Info(ctx, nil, "anthropic", "StreamMessages", "retrying Responses stream after mid-stream protocol failure", "provider", provider.name, "attempt", midstreamAttempt+1, "delay_ms", delay.Milliseconds(), "error", err)
				if sleepErr := sleepContext(ctx, delay); sleepErr != nil {
					emitResponsesRetryOutcome(ctx, provider, attemptReq, responsesRetryOutcomeCancelled, midstreamAttempt, retryLimit, err)
					return nil, sleepErr
				}
				continue
			}
			failureKind := responsesStreamFailureKindOf(err)
			switch {
			case responsesAttempt && committed && failureKind != "":
				emitResponsesRetryOutcome(ctx, provider, attemptReq, responsesRetryOutcomeCommittedOutput, midstreamAttempt, retryLimit, err)
			case responsesAttempt && (ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)):
				emitResponsesRetryOutcome(ctx, provider, attemptReq, responsesRetryOutcomeCancelled, midstreamAttempt, retryLimit, err)
			case responsesAttempt && retryLimit > 0 && midstreamAttempt >= retryLimit:
				emitResponsesRetryOutcome(ctx, provider, attemptReq, responsesRetryOutcomeExhausted, midstreamAttempt, retryLimit, err)
			}
			if isRetryableResponsesStreamError(err) {
				// Even when output has already been emitted and fallback is unsafe,
				// remember the provider failure for the next turn.
				c.breaker.trip(provider.name)
			}
			if committed || !canFallbackAfterError(ctx, err) {
				// 明确的 4xx 与调用方取消不进冷却：它们不是 provider 健康度的信号，
				// 而且这两种都在这里直接返回，重试也是同样结果。
				return nil, err
			}
			failures = append(failures, providerFailure{name: provider.name, err: err})
			c.breaker.trip(provider.name)
			if index+1 < len(providers) {
				observability.Debug(ctx, nil, "anthropic", "StreamMessages", "provider failed; trying fallback", "provider", provider.name, "nextProvider", providers[index+1].name, "error", err)
			}
			break
		}
	}
	if len(failures) == 1 && len(providers) == 1 {
		return nil, failures[0].err
	}
	return nil, formatProviderFailures(failures)
}

func orderProvidersForRequest(providers []providerClient, requestedModel string) []providerClient {
	requestedModel = strings.TrimSpace(requestedModel)
	if len(providers) <= 2 || requestedModel == "" {
		return providers
	}
	matches := make([]providerClient, 0, len(providers)-1)
	others := make([]providerClient, 0, len(providers)-1)
	for _, provider := range providers[1:] {
		if strings.TrimSpace(provider.model) == requestedModel {
			matches = append(matches, provider)
			continue
		}
		others = append(others, provider)
	}
	if len(matches) == 0 {
		return providers
	}
	ordered := make([]providerClient, 0, len(providers))
	ordered = append(ordered, providers[0])
	ordered = append(ordered, matches...)
	ordered = append(ordered, others...)
	return ordered
}

func (c *Client) streamMessagesWithProvider(ctx context.Context, provider providerClient, req MessagesRequest, cb StreamCallbacks) (*StreamResult, error) {
	if provider.source == protocolSourceExplicit && provider.protocol == config.ProviderProtocolOpenAIResponses {
		if provider.backend == nil {
			return nil, fmt.Errorf("provider %q Responses backend is not initialized", provider.name)
		}
		return provider.backend.StreamMessages(ctx, req, cb)
	}
	if providerKindOpenAI(provider.kind) {
		return c.streamOpenAIChatCompletion(ctx, provider, req, cb)
	}
	start := time.Now()
	params, err := sdkMessageParams(req)
	if err != nil {
		return nil, err
	}
	emitModelPhase(ctx, "request.build", start, telemetry.StatusOK, "", provider, req, map[string]any{
		"messages":      len(req.Messages),
		"tools":         len(req.Tools),
		"system_blocks": len(req.SystemBlocks),
	})
	streamStart := time.Now()
	trace := newHTTPPhaseTrace(ctx, streamStart, provider, req)
	stream := provider.sdk.Messages.NewStreaming(trace.context(ctx), params)
	trace.finish(time.Now())
	props := trace.properties(time.Now())
	emitModelPhase(ctx, "stream.create", streamStart, telemetry.StatusOK, "", provider, req, props)
	var message sdkanthropic.Message
	readStart := time.Now()
	var firstEventAt time.Time
	var firstDeltaAt time.Time
	events := 0
	textDeltas := 0
	thinkingDeltas := 0
	signatureDeltas := 0
	citationDeltas := 0
	for stream.Next() {
		events++
		if firstEventAt.IsZero() {
			firstEventAt = time.Now()
			emitModelPhase(ctx, "stream.first_event", readStart, telemetry.StatusOK, "", provider, req, map[string]any{"event_type": stream.Current().Type})
		}
		event := stream.Current()
		if event.Type == "content_block_delta" {
			switch event.Delta.Type {
			case "text_delta":
				textDeltas++
				if firstDeltaAt.IsZero() && event.Delta.Text != "" {
					firstDeltaAt = time.Now()
					emitModelPhase(ctx, "stream.first_delta", readStart, telemetry.StatusOK, "", provider, req, map[string]any{"delta_type": event.Delta.Type})
				}
				if event.Delta.Text != "" && cb.OnText != nil {
					if err := cb.OnText(event.Delta.Text); err != nil {
						return nil, err
					}
				}
			case "thinking_delta":
				thinkingDeltas++
				if firstDeltaAt.IsZero() && event.Delta.Thinking != "" {
					firstDeltaAt = time.Now()
					emitModelPhase(ctx, "stream.first_delta", readStart, telemetry.StatusOK, "", provider, req, map[string]any{"delta_type": event.Delta.Type})
				}
				if event.Delta.Thinking != "" && cb.OnThinking != nil {
					if err := cb.OnThinking(event.Delta.Thinking); err != nil {
						return nil, err
					}
				}
			case "signature_delta":
				signatureDeltas++
				if event.Delta.Signature != "" && cb.OnSignature != nil {
					if err := cb.OnSignature(event.Delta.Signature); err != nil {
						return nil, err
					}
				}
			case "citations_delta":
				citationDeltas++
				if cb.OnCitation != nil {
					if raw := rawCitation(event.Delta.Citation); len(raw) > 0 {
						if firstDeltaAt.IsZero() {
							firstDeltaAt = time.Now()
							emitModelPhase(ctx, "stream.first_delta", readStart, telemetry.StatusOK, "", provider, req, map[string]any{"delta_type": event.Delta.Type})
						}
						if err := cb.OnCitation(raw); err != nil {
							return nil, err
						}
					}
				}
			}
		}
		if err := message.Accumulate(event); err != nil {
			return nil, err
		}
	}
	if err := stream.Err(); err != nil {
		emitModelPhase(ctx, "stream.read", readStart, telemetry.StatusError, err.Error(), provider, req, map[string]any{
			"events":           events,
			"text_deltas":      textDeltas,
			"thinking_deltas":  thinkingDeltas,
			"signature_deltas": signatureDeltas,
			"citation_deltas":  citationDeltas,
		})
		return nil, partialStreamError(err, streamResultFromSDKMessage(message))
	}
	emitModelPhase(ctx, "stream.read", readStart, telemetry.StatusOK, "", provider, req, map[string]any{
		"events":             events,
		"text_deltas":        textDeltas,
		"thinking_deltas":    thinkingDeltas,
		"signature_deltas":   signatureDeltas,
		"citation_deltas":    citationDeltas,
		"first_event_gap_ms": durationSince(readStart, firstEventAt),
		"first_delta_gap_ms": durationSince(readStart, firstDeltaAt),
	})
	return streamResultFromSDKMessage(message), nil
}

func callbacksMarkingEmitted(cb StreamCallbacks, emitted *bool) StreamCallbacks {
	mark := func(size int) {
		if size > 0 {
			*emitted = true
		}
	}
	out := cb
	if cb.OnText != nil {
		out.OnText = func(text string) error {
			mark(len(text))
			return cb.OnText(text)
		}
	}
	if cb.OnThinking != nil {
		out.OnThinking = func(text string) error {
			mark(len(text))
			return cb.OnThinking(text)
		}
	}
	if cb.OnSignature != nil {
		out.OnSignature = func(signature string) error {
			mark(len(signature))
			return cb.OnSignature(signature)
		}
	}
	if cb.OnCitation != nil {
		out.OnCitation = func(citation json.RawMessage) error {
			mark(len(citation))
			return cb.OnCitation(citation)
		}
	}
	if cb.OnConnectorText != nil {
		out.OnConnectorText = func(text string) error {
			mark(len(text))
			return cb.OnConnectorText(text)
		}
	}
	return out
}

type responsesAttemptCallbackBuffer struct {
	downstream  StreamCallbacks
	reasoning   []string
	isCommitted bool
	flushed     bool
}

func newResponsesAttemptCallbackBuffer(cb StreamCallbacks) *responsesAttemptCallbackBuffer {
	return &responsesAttemptCallbackBuffer{downstream: cb}
}

func (b *responsesAttemptCallbackBuffer) callbacks() StreamCallbacks {
	return StreamCallbacks{
		OnThinking: func(text string) error {
			if text == "" {
				return nil
			}
			if b.flushed {
				if b.downstream.OnThinking != nil {
					return b.downstream.OnThinking(text)
				}
				return nil
			}
			b.reasoning = append(b.reasoning, text)
			return nil
		},
		OnText: func(text string) error {
			if text == "" {
				return nil
			}
			if err := b.commitAndFlush(); err != nil {
				return err
			}
			if b.downstream.OnText != nil {
				return b.downstream.OnText(text)
			}
			return nil
		},
	}
}

func (b *responsesAttemptCallbackBuffer) commitAndFlush() error {
	if b == nil {
		return nil
	}
	b.isCommitted = true
	return b.flush()
}

func (b *responsesAttemptCallbackBuffer) flush() error {
	if b == nil || b.flushed {
		return nil
	}
	b.flushed = true
	for _, delta := range b.reasoning {
		if b.downstream.OnThinking != nil {
			if err := b.downstream.OnThinking(delta); err != nil {
				b.isCommitted = true
				return err
			}
		}
	}
	return nil
}

func (b *responsesAttemptCallbackBuffer) committed() bool {
	return b != nil && b.isCommitted
}

func (c *Client) streamOpenAIChatCompletion(ctx context.Context, provider providerClient, req MessagesRequest, cb StreamCallbacks) (*StreamResult, error) {
	start := time.Now()
	params, err := openAIChatCompletionRequest(req)
	if err != nil {
		return nil, err
	}
	emitModelPhase(ctx, "request.build", start, telemetry.StatusOK, "", provider, req, map[string]any{
		"messages": len(params.Messages),
		"tools":    len(params.Tools),
	})
	stream, err := c.createOpenAIChatCompletionStream(ctx, provider, req, params)
	if err != nil && shouldRetryOpenAIWithoutReasoning(req, err) {
		// Some OpenAI-compatible gateways map reasoning_effort to a provider
		// thinking budget that is larger than the caller's max token ceiling. A
		// single retry without the optional reasoning hint preserves the caller's
		// output budget and lets the provider use its own default reasoning mode.
		retryReq := req
		retryReq.Thinking = nil
		retryParams, buildErr := openAIChatCompletionRequest(retryReq)
		if buildErr != nil {
			return nil, buildErr
		}
		observability.Info(ctx, nil, "anthropic", "StreamMessages", "retrying OpenAI-compatible request without reasoning_effort", "provider", provider.name, "model", req.Model)
		req = retryReq
		stream, err = c.createOpenAIChatCompletionStream(ctx, provider, req, retryParams)
	}
	if err != nil {
		return nil, err
	}
	defer stream.Close()

	var text strings.Builder
	toolCalls := map[int]*openAIToolAccumulator{}
	stopReason := ""
	var usage Usage
	readStart := time.Now()
	var firstChunkAt time.Time
	var firstDeltaAt time.Time
	chunks := 0
	textDeltas := 0
	toolDeltas := 0
	reasoningDeltas := 0
	for {
		event, err := recvOpenAIChunk(stream)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			emitModelPhase(ctx, "stream.read", readStart, telemetry.StatusError, err.Error(), provider, req, map[string]any{
				"chunks":           chunks,
				"text_deltas":      textDeltas,
				"tool_deltas":      toolDeltas,
				"reasoning_deltas": reasoningDeltas,
			})
			return nil, partialStreamError(err, openAIStreamResult(text.String(), toolCalls, stopReason, usage))
		}
		chunks++
		if firstChunkAt.IsZero() {
			firstChunkAt = time.Now()
			emitModelPhase(ctx, "stream.first_event", readStart, telemetry.StatusOK, "", provider, req, map[string]any{"event_type": "chat_completion_chunk"})
		}
		if event.Usage != nil {
			usage.InputTokens = event.Usage.PromptTokens
			usage.OutputTokens = event.Usage.CompletionTokens
			usage.InputTokensIncludeCacheRead = true
			// OpenAI-compatible gateways report prompt cache hits under prompt_tokens_details.
			if event.Usage.PromptTokensDetails != nil {
				usage.CacheReadInputTokens = event.Usage.PromptTokensDetails.CachedTokens
			}
			if event.Usage.CompletionTokensDetails != nil {
				usage.ReasoningOutputTokens = event.Usage.CompletionTokensDetails.ReasoningTokens
			}
		}
		for _, choice := range event.Choices {
			if choice.FinishReason != "" {
				stopReason = openAIStopReason(choice.FinishReason)
			}
			if choice.Delta.ReasoningContent != "" {
				reasoningDeltas++
				if firstDeltaAt.IsZero() {
					firstDeltaAt = time.Now()
					emitModelPhase(ctx, "stream.first_delta", readStart, telemetry.StatusOK, "", provider, req, map[string]any{"delta_type": "thinking_delta"})
				}
				if cb.OnThinking != nil {
					if err := cb.OnThinking(choice.Delta.ReasoningContent); err != nil {
						return nil, err
					}
				}
			}
			if choice.Delta.Content != "" {
				textDeltas++
				if firstDeltaAt.IsZero() {
					firstDeltaAt = time.Now()
					emitModelPhase(ctx, "stream.first_delta", readStart, telemetry.StatusOK, "", provider, req, map[string]any{"delta_type": "text_delta"})
				}
				text.WriteString(choice.Delta.Content)
				if cb.OnText != nil {
					if err := cb.OnText(choice.Delta.Content); err != nil {
						return nil, err
					}
				}
			}
			for _, toolCall := range choice.Delta.ToolCalls {
				toolDeltas++
				if firstDeltaAt.IsZero() {
					firstDeltaAt = time.Now()
					emitModelPhase(ctx, "stream.first_delta", readStart, telemetry.StatusOK, "", provider, req, map[string]any{"delta_type": "tool_call_delta"})
				}
				index := 0
				if toolCall.Index != nil {
					index = *toolCall.Index
				}
				acc := toolCalls[index]
				if acc == nil {
					acc = &openAIToolAccumulator{}
					toolCalls[index] = acc
				}
				if toolCall.ID != "" {
					acc.id = toolCall.ID
				}
				if toolCall.Function.Name != "" {
					acc.name = toolCall.Function.Name
				}
				if toolCall.Function.Arguments != "" {
					acc.arguments.WriteString(toolCall.Function.Arguments)
				}
			}
		}
	}
	emitModelPhase(ctx, "stream.read", readStart, telemetry.StatusOK, "", provider, req, map[string]any{
		"chunks":             chunks,
		"text_deltas":        textDeltas,
		"tool_deltas":        toolDeltas,
		"reasoning_deltas":   reasoningDeltas,
		"first_event_gap_ms": durationSince(readStart, firstChunkAt),
		"first_delta_gap_ms": durationSince(readStart, firstDeltaAt),
	})

	return openAIStreamResult(text.String(), toolCalls, stopReason, usage), nil
}

func shouldRetryOpenAIWithoutReasoning(req MessagesRequest, err error) bool {
	if req.Thinking == nil || strings.TrimSpace(openAIReasoningEffort(req.Thinking)) == "" {
		return false
	}
	if openAIErrorStatusCode(err) != http.StatusBadRequest {
		return false
	}
	message := strings.ToLower(openAIErrorMessage(err))
	return strings.Contains(message, "max_completion_tokens") && strings.Contains(message, "thinking_budget")
}

func (c *Client) createOpenAIChatCompletionStream(ctx context.Context, provider providerClient, req MessagesRequest, params openai.ChatCompletionRequest) (*openai.ChatCompletionStream, error) {
	var retries openAIStreamCreateRetries
	for attempt := 0; ; attempt++ {
		createStart := time.Now()
		trace := newHTTPPhaseTrace(ctx, createStart, provider, req)
		// Retry-After 只存在于响应头上，而 go-openai 的 APIError/RequestError 都只带状态码
		// 和 body。这个 capture 让 httpTraceClient 把它顺出来交给下面的重试决策。
		capture := &retryAfterCapture{}
		attemptCtx := withRetryAfterCapture(trace.context(ctx), capture)
		stream, err := provider.openAI.CreateChatCompletionStream(attemptCtx, params)
		trace.finish(time.Now())
		props := trace.properties(time.Now())
		props["attempt"] = attempt + 1
		if err == nil {
			emitModelPhase(ctx, "stream.create", createStart, telemetry.StatusOK, "", provider, req, props)
			return stream, nil
		}
		emitModelPhase(ctx, "stream.create", createStart, telemetry.StatusError, err.Error(), provider, req, props)
		delay, reason, retry := retries.next(ctx, err, capture.get())
		if !retry {
			return nil, err
		}
		observability.Info(ctx, nil, "anthropic", "StreamMessages", "retrying OpenAI-compatible stream create", "provider", provider.name, "attempt", attempt+1, "reason", reason, "delay_ms", delay.Milliseconds(), "error", err)
		if sleepErr := sleepContext(ctx, delay); sleepErr != nil {
			return nil, sleepErr
		}
	}
}

// openAIStreamCreateRetries 分开记 rate limit 和瞬时故障的重试预算：两者的退避量级差
// 三个数量级（429 可能要求等一分钟，5xx 抖动几百毫秒就够），共用一个计数器会互相饿死。
type openAIStreamCreateRetries struct {
	rateLimit int
	transient int
}

// next 决定建流失败后是否原地重试。这里只兜「同一个 provider 上的瞬时失败」，持续性
// 故障留给 provider fallback（canFallbackAfterError），两层不重复兜底。
func (r *openAIStreamCreateRetries) next(ctx context.Context, err error, retryAfter time.Duration) (time.Duration, string, bool) {
	// 调用方主动取消：立刻停，别再等一轮退避。
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return 0, "", false
	}
	// 分段超时守卫（AUDIT-P0-07）主动取消过请求，说明网关卡住了。该做的是尽快切
	// provider，原地重试只会再赔一个 responseHeader 超时。
	if errors.Is(err, errProviderTimeout) {
		return 0, "", false
	}
	status := openAIErrorStatusCode(err)
	if status == http.StatusTooManyRequests {
		if r.rateLimit >= defaultOpenAIStreamCreateRateLimitRetries {
			return 0, "", false
		}
		r.rateLimit++
		return openAIRateLimitRetryDelay(err, retryAfter), "rate_limit", true
	}
	if !openAIStreamCreateTransientError(status, err) {
		return 0, "", false
	}
	if r.transient >= openAIStreamCreateTransientRetries {
		return 0, "", false
	}
	r.transient++
	// 网关给了明确的等待时间就照办，否则指数退避。
	if retryAfter > 0 {
		return retryAfter, "transient", true
	}
	return openAIStreamCreateBackoffDelay(r.transient), "transient", true
}

// openAIStreamCreateTransientError 判断一次建流失败是否值得原地重试。
func openAIStreamCreateTransientError(status int, err error) bool {
	switch {
	case status >= http.StatusInternalServerError:
		return true
	case status == http.StatusRequestTimeout || status == http.StatusConflict:
		return true
	case status != 0:
		// 其余带明确状态码的 4xx：重试不会变好，只会白等并推迟 fallback。
		return false
	}
	// 没有 HTTP 状态码，说明连响应头都没拿到：连接重置 / DNS / 提前 EOF 这类网络抖动。
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return true
	}
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

// openAIRateLimitRetryDelay 按可信度取等待时长：测试注入的环境变量 > 响应头
// Retry-After > 错误文案里的 "retry after N" > 兜底常量。
func openAIRateLimitRetryDelay(err error, retryAfter time.Duration) time.Duration {
	if delay := envDurationMillis(openAIStreamCreateRateLimitRetryDelayEnv); delay > 0 {
		return delay
	}
	if retryAfter > 0 {
		return retryAfter
	}
	if delay, ok := retryAfterDelayFromMessage(openAIErrorMessage(err)); ok {
		return delay
	}
	return defaultOpenAIStreamCreateRateLimitDelay
}

func openAIStreamCreateBackoffDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := openAIStreamCreateBackoff << (attempt - 1)
	if delay <= 0 || delay > openAIStreamCreateMaxBackoff {
		return openAIStreamCreateMaxBackoff
	}
	return delay
}

func openAIErrorStatusCode(err error) int {
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) {
		return apiErr.HTTPStatusCode
	}
	var reqErr *openai.RequestError
	if errors.As(err, &reqErr) {
		return reqErr.HTTPStatusCode
	}
	return 0
}

func openAIErrorMessage(err error) string {
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Message
	}
	var reqErr *openai.RequestError
	if errors.As(err, &reqErr) {
		return string(reqErr.Body)
	}
	if err == nil {
		return ""
	}
	return err.Error()
}

func retryAfterDelayFromMessage(message string) (time.Duration, bool) {
	normalized := strings.ToLower(message)
	idx := strings.Index(normalized, "retry after")
	if idx < 0 {
		return 0, false
	}
	fields := strings.Fields(normalized[idx+len("retry after"):])
	if len(fields) == 0 {
		return 0, false
	}
	value, err := strconv.Atoi(strings.Trim(fields[0], ".,;:"))
	if err != nil || value <= 0 {
		return 0, false
	}
	unit := ""
	if len(fields) > 1 {
		unit = strings.Trim(fields[1], ".,;:")
	}
	if strings.HasPrefix(unit, "minute") {
		return time.Duration(value) * time.Minute, true
	}
	return time.Duration(value) * time.Second, true
}

func envDurationMillis(name string) time.Duration {
	raw := strings.TrimSpace(product.Getenv(name))
	if raw == "" {
		return 0
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0
	}
	return time.Duration(value) * time.Millisecond
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func openAIStreamResult(text string, toolCalls map[int]*openAIToolAccumulator, stopReason string, usage Usage) *StreamResult {
	content := make([]ContentBlock, 0, 1+len(toolCalls))
	if text != "" {
		content = append(content, ContentBlock{Type: "text", Text: text})
	}
	for _, index := range sortedToolCallIndexes(toolCalls) {
		acc := toolCalls[index]
		if acc == nil || acc.name == "" {
			continue
		}
		id := acc.id
		if id == "" {
			id = fmt.Sprintf("call_%d", index)
		}
		for partIndex, input := range openAIToolArgumentParts(acc.arguments.String()) {
			toolID := id
			if partIndex > 0 {
				toolID = fmt.Sprintf("%s_part_%d", id, partIndex+1)
			}
			content = append(content, ContentBlock{
				Type:  "tool_use",
				ID:    toolID,
				Name:  acc.name,
				Input: input,
			})
		}
	}
	return &StreamResult{
		Message: MessageParam{
			Role:    "assistant",
			Content: content,
		},
		StopReason: stopReason,
		Usage:      usage,
	}
}

func partialStreamError(err error, partial *StreamResult) error {
	if err == nil || streamResultEmpty(partial) {
		return err
	}
	return &PartialStreamError{Err: err, Partial: partial}
}

func streamResultEmpty(result *StreamResult) bool {
	if result == nil {
		return true
	}
	for _, block := range result.Message.Content {
		switch block.Type {
		case "text":
			if strings.TrimSpace(block.Text) != "" {
				return false
			}
		case "thinking":
			if strings.TrimSpace(block.Thinking) != "" {
				return false
			}
		case "connector_text":
			if strings.TrimSpace(block.ConnectorText) != "" {
				return false
			}
		case "tool_use":
			if block.Name != "" || len(block.Input) > 0 {
				return false
			}
		default:
			if strings.TrimSpace(block.Text) != "" || strings.TrimSpace(block.Content) != "" {
				return false
			}
		}
	}
	return true
}

func emitModelPhase(ctx context.Context, phase string, start time.Time, status string, errorMessage string, provider providerClient, req MessagesRequest, props map[string]any) {
	if start.IsZero() {
		start = time.Now()
	}
	if props == nil {
		props = map[string]any{}
	}
	props["phase"] = phase
	props["provider"] = provider.name
	props["provider_name"] = provider.name
	props["provider_role"] = provider.role
	props["provider_kind"] = provider.kind
	props["endpoint"] = provider.endpoint
	telemetry.EmitCompletedSpan(ctx, start, telemetry.Event{
		Name:       telemetry.EventModelPhase + phase,
		Category:   telemetry.CategoryModel,
		Source:     "anthropic.Client.StreamMessages",
		Status:     status,
		Model:      req.Model,
		SessionID:  req.TenantSessionID,
		Error:      errorMessage,
		Properties: props,
	})
}

type httpPhaseTraceKey struct{}

type httpTraceClient struct {
	client   *http.Client
	timeouts providerHTTPTimeouts
}

func newHTTPTraceClient(base http.RoundTripper) *httpTraceClient {
	return newHTTPTraceClientWithTimeouts(base, providerTimeouts)
}

func newHTTPTraceClientWithTimeouts(base http.RoundTripper, timeouts providerHTTPTimeouts) *httpTraceClient {
	if base == nil {
		base = newProviderTransport()
	}
	// 没有 http.Client.Timeout：总超时按「建流 / 流空闲 / 非流式 body」三段实现，
	// 见 httpclient.go 顶部说明。
	return &httpTraceClient{
		client:   &http.Client{Transport: httpTraceRoundTripper{base: base}},
		timeouts: timeouts,
	}
}

func (c *httpTraceClient) Do(req *http.Request) (*http.Response, error) {
	resp, err := c.do(req)
	// 错误响应上的 Retry-After 是唯一权威的退避时长，但 go-openai 不会把响应头带进
	// error 里，所以在这一层截下来（AUDIT-P1-08）。
	if err == nil && resp != nil && resp.StatusCode >= http.StatusBadRequest {
		// 只有 OpenAI 路径会装 capture；Anthropic 走 SDK 自己的重试，不必白解析响应头。
		if capture := retryAfterCaptureFromContext(req.Context()); capture != nil {
			capture.set(retryAfterFromHeader(resp.Header, time.Now()))
		}
	}
	return resp, err
}

func (c *httpTraceClient) do(req *http.Request) (*http.Response, error) {
	if !c.timeouts.enabled() {
		return c.client.Do(req)
	}
	ctx, cancel := context.WithCancel(req.Context())
	guard := &responseGuard{cancel: cancel}
	guard.arm(c.timeouts.responseHeader, false)
	resp, err := c.client.Do(req.WithContext(ctx))
	if err != nil {
		fired, timeout := guard.state()
		guard.close()
		if fired {
			return nil, newProviderTimeoutError("provider sent no response headers", timeout, err)
		}
		return nil, err
	}
	if isEventStreamResponse(resp) {
		// 流建立成功：总时长不设限，改为「相邻两次读之间」的空闲检测。
		guard.arm(c.timeouts.streamIdle, true)
		resp.Body = &guardedBody{body: resp.Body, guard: guard, message: "provider stream stalled"}
		return resp, nil
	}
	guard.arm(c.timeouts.responseBody, false)
	resp.Body = &guardedBody{body: resp.Body, guard: guard, message: "provider response body timed out"}
	return resp, nil
}

type httpTraceRoundTripper struct {
	base http.RoundTripper
}

func (t httpTraceRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	trace := httpPhaseTraceFromContext(req.Context())
	if trace == nil {
		return t.base.RoundTrip(req)
	}
	start := time.Now()
	resp, err := t.base.RoundTrip(req)
	if resp != nil {
		trace.captureResponse(resp)
	}
	trace.mark("http_round_trip", start, time.Now(), nil)
	return resp, err
}

type httpPhaseTrace struct {
	ctx      context.Context
	start    time.Time
	provider providerClient
	req      MessagesRequest

	mu              sync.Mutex
	getConnStart    time.Time
	dnsStart        time.Time
	connectStart    time.Time
	tlsStart        time.Time
	wroteHeadersAt  time.Time
	wroteRequestAt  time.Time
	firstResponseAt time.Time
	events          map[string]int64
	responseMeta    map[string]any
}

func newHTTPPhaseTrace(ctx context.Context, start time.Time, provider providerClient, req MessagesRequest) *httpPhaseTrace {
	if start.IsZero() {
		start = time.Now()
	}
	return &httpPhaseTrace{
		ctx:          ctx,
		start:        start,
		provider:     provider,
		req:          req,
		events:       map[string]int64{},
		responseMeta: map[string]any{},
	}
}

func (t *httpPhaseTrace) captureResponse(resp *http.Response) {
	if t == nil || resp == nil {
		return
	}
	requestID := firstNonEmpty(
		resp.Header.Get("x-request-id"),
		resp.Header.Get("request-id"),
		resp.Header.Get("openai-request-id"),
	)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.responseMeta["http_status"] = resp.StatusCode
	if contentType := strings.TrimSpace(resp.Header.Get("content-type")); contentType != "" {
		t.responseMeta["response_mime"] = contentType
	}
	if requestID != "" {
		t.responseMeta["provider_request_id"] = requestID
	}
}

func (t *httpPhaseTrace) context(ctx context.Context) context.Context {
	if t == nil {
		return ctx
	}
	clientTrace := &httptrace.ClientTrace{
		GetConn: func(_ string) {
			t.mu.Lock()
			t.getConnStart = time.Now()
			t.mu.Unlock()
		},
		GotConn: func(info httptrace.GotConnInfo) {
			now := time.Now()
			t.mu.Lock()
			start := t.getConnStart
			t.mu.Unlock()
			t.mark("http.get_conn", start, now, map[string]any{
				"reused":       info.Reused,
				"was_idle":     info.WasIdle,
				"idle_time_ms": info.IdleTime.Milliseconds(),
			})
		},
		DNSStart: func(_ httptrace.DNSStartInfo) {
			t.mu.Lock()
			t.dnsStart = time.Now()
			t.mu.Unlock()
		},
		DNSDone: func(info httptrace.DNSDoneInfo) {
			now := time.Now()
			t.mu.Lock()
			start := t.dnsStart
			t.mu.Unlock()
			props := map[string]any{"addrs": len(info.Addrs)}
			if info.Err != nil {
				props["error"] = info.Err.Error()
			}
			t.mark("http.dns", start, now, props)
		},
		ConnectStart: func(_, _ string) {
			t.mu.Lock()
			t.connectStart = time.Now()
			t.mu.Unlock()
		},
		ConnectDone: func(_, _ string, err error) {
			now := time.Now()
			t.mu.Lock()
			start := t.connectStart
			t.mu.Unlock()
			props := map[string]any{}
			if err != nil {
				props["error"] = err.Error()
			}
			t.mark("http.connect", start, now, props)
		},
		TLSHandshakeStart: func() {
			t.mu.Lock()
			t.tlsStart = time.Now()
			t.mu.Unlock()
		},
		TLSHandshakeDone: func(state tls.ConnectionState, err error) {
			now := time.Now()
			t.mu.Lock()
			start := t.tlsStart
			t.mu.Unlock()
			props := map[string]any{"version": tlsVersionLabel(state.Version), "resumed": state.DidResume}
			if err != nil {
				props["error"] = err.Error()
			}
			t.mark("http.tls", start, now, props)
		},
		WroteHeaders: func() {
			t.mu.Lock()
			t.wroteHeadersAt = time.Now()
			t.mu.Unlock()
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			now := time.Now()
			t.mu.Lock()
			headersAt := t.wroteHeadersAt
			t.mu.Unlock()
			props := map[string]any{}
			if info.Err != nil {
				props["error"] = info.Err.Error()
			}
			t.mark("http.request_send", t.start, now, props)
			t.mark("http.write_request", headersAt, now, props)
			t.mu.Lock()
			t.wroteRequestAt = now
			t.mu.Unlock()
		},
		GotFirstResponseByte: func() {
			now := time.Now()
			t.mu.Lock()
			start := t.wroteRequestAt
			t.firstResponseAt = now
			t.mu.Unlock()
			t.mark("http.wait_first_response_byte", start, now, nil)
		},
	}
	return context.WithValue(httptrace.WithClientTrace(ctx, clientTrace), httpPhaseTraceKey{}, t)
}

func httpPhaseTraceFromContext(ctx context.Context) *httpPhaseTrace {
	trace, _ := ctx.Value(httpPhaseTraceKey{}).(*httpPhaseTrace)
	return trace
}

func (t *httpPhaseTrace) mark(phase string, start time.Time, end time.Time, props map[string]any) {
	if t == nil {
		return
	}
	if start.IsZero() {
		start = end
	}
	if end.IsZero() {
		end = time.Now()
	}
	duration := end.Sub(start).Milliseconds()
	if duration < 0 {
		duration = 0
	}
	t.mu.Lock()
	t.events[phase+"_ms"] = duration
	t.mu.Unlock()
	emitModelPhase(t.ctx, phase, start, telemetry.StatusOK, "", t.provider, t.req, props)
}

func (t *httpPhaseTrace) finish(end time.Time) {
	if t == nil {
		return
	}
	t.mu.Lock()
	firstAt := t.firstResponseAt
	t.mu.Unlock()
	if firstAt.IsZero() {
		return
	}
	t.mark("http.stream_ready", firstAt, end, nil)
}

func (t *httpPhaseTrace) properties(end time.Time) map[string]any {
	if t == nil {
		return nil
	}
	if end.IsZero() {
		end = time.Now()
	}
	t.mu.Lock()
	props := make(map[string]any, len(t.events)+len(t.responseMeta)+2)
	for key, value := range t.events {
		props[key] = value
	}
	for key, value := range t.responseMeta {
		props[key] = value
	}
	wroteAt := t.wroteRequestAt
	firstAt := t.firstResponseAt
	t.mu.Unlock()
	if !wroteAt.IsZero() {
		props["sdk_after_wrote_request_ms"] = end.Sub(wroteAt).Milliseconds()
	}
	if !firstAt.IsZero() {
		props["sdk_after_first_response_byte_ms"] = end.Sub(firstAt).Milliseconds()
	}
	return props
}

func tlsVersionLabel(version uint16) string {
	switch version {
	case tls.VersionTLS10:
		return "tls1.0"
	case tls.VersionTLS11:
		return "tls1.1"
	case tls.VersionTLS12:
		return "tls1.2"
	case tls.VersionTLS13:
		return "tls1.3"
	default:
		return ""
	}
}

func durationSince(start time.Time, end time.Time) int64 {
	if start.IsZero() || end.IsZero() {
		return 0
	}
	return end.Sub(start).Milliseconds()
}

type providerFailure struct {
	name string
	err  error
}

type openAIToolAccumulator struct {
	id        string
	name      string
	arguments strings.Builder
}

// openAIStreamChunk 在 go-openai 的 chunk 结构上补一个 error 字段。go-openai 只认
// `data: {"error":` 这种「error 是整行第一个 key」的形式，而不少 OpenAI-compatible
// 网关会把错误塞进一个形状正常的 chunk（error 不是首个 key）。那种 chunk 反序列化后
// Choices 为空，读循环只看 Choices/Usage，于是错误被静默丢掉，只留下一个空响应
// （AUDIT-P1-10）。
type openAIStreamChunk struct {
	openai.ChatCompletionStreamResponse
	Error *openAIStreamChunkError `json:"error"`
}

type openAIStreamChunkError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

func (e *openAIStreamChunkError) err() error {
	if e == nil {
		return nil
	}
	message := strings.TrimSpace(e.Message)
	kind := strings.TrimSpace(e.Type)
	if message == "" && kind == "" {
		return nil
	}
	if message == "" {
		message = "unknown error"
	}
	if kind == "" {
		return fmt.Errorf("openai-compatible stream error: %s", message)
	}
	return fmt.Errorf("openai-compatible stream error: %s: %s", kind, message)
}

// recvOpenAIChunk 读一个 chunk。用 RecvRaw 而不是 Recv，是为了能看到 go-openai 没有
// 建模进 ChatCompletionStreamResponse 的 error 字段。
func recvOpenAIChunk(stream *openai.ChatCompletionStream) (openai.ChatCompletionStreamResponse, error) {
	raw, err := stream.RecvRaw()
	if err != nil {
		return openai.ChatCompletionStreamResponse{}, err
	}
	var chunk openAIStreamChunk
	if err := json.Unmarshal(raw, &chunk); err != nil {
		return openai.ChatCompletionStreamResponse{}, fmt.Errorf("failed to parse openai-compatible stream chunk: %w", err)
	}
	if chunkErr := chunk.Error.err(); chunkErr != nil {
		return openai.ChatCompletionStreamResponse{}, chunkErr
	}
	return chunk.ChatCompletionStreamResponse, nil
}

// The provider-kind classifiers live in internal/config so the pricing layer can
// group its cache-price defaults by the same kinds this client dispatches on
// (AUDIT-P1-36); a second copy of the strings here would let the two drift.
func providerKindSupported(kind string) bool {
	return config.ProviderKindAnthropic(kind) || config.ProviderKindOpenAI(kind)
}

func providerKindOpenAI(kind string) bool {
	return config.ProviderKindOpenAI(kind)
}

func canFallbackAfterError(ctx context.Context, err error) bool {
	// 超时守卫靠 cancel context 实现超时，错误链里必然含 context.Canceled。必须先把
	// 它和调用方主动取消分开，否则 provider 卡死时整条 fallback 链会被跳过 —— 而那
	// 正是 fallback 最该生效的场景。
	if errors.Is(err, errProviderTimeout) {
		return ctx.Err() == nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		return false
	}
	var responsesStreamErr *openAIResponsesStreamError
	if errors.As(err, &responsesStreamErr) {
		return responsesStreamErr.retryable
	}
	var apiErr *sdkanthropic.Error
	if errors.As(err, &apiErr) {
		raw := apiErr.RawJSON()
		return apiErr.StatusCode == http.StatusRequestTimeout ||
			apiErr.StatusCode == http.StatusConflict ||
			apiErr.StatusCode == http.StatusTooManyRequests ||
			apiErr.StatusCode >= http.StatusInternalServerError ||
			strings.Contains(raw, "overloaded_error") ||
			strings.Contains(raw, "rate_limit_error")
	}
	var openAIErr *openai.APIError
	if errors.As(err, &openAIErr) {
		return openAIErr.HTTPStatusCode == http.StatusRequestTimeout ||
			openAIErr.HTTPStatusCode == http.StatusConflict ||
			openAIErr.HTTPStatusCode == http.StatusTooManyRequests ||
			openAIErr.HTTPStatusCode >= http.StatusInternalServerError ||
			isModelAccessFallbackError(openAIErr.HTTPStatusCode, openAIErr.Message)
	}
	var responsesErr *openairesponses.Error
	if errors.As(err, &responsesErr) {
		return responsesErr.StatusCode == http.StatusRequestTimeout ||
			responsesErr.StatusCode == http.StatusConflict ||
			responsesErr.StatusCode == http.StatusTooManyRequests ||
			responsesErr.StatusCode >= http.StatusInternalServerError ||
			isModelAccessFallbackError(responsesErr.StatusCode, responsesErr.Message)
	}
	var responsesEventErr *openAIResponsesEventError
	if errors.As(err, &responsesEventErr) {
		return responsesEventErr.retryable
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

func isRetryableResponsesStreamError(err error) bool {
	var responsesStreamErr *openAIResponsesStreamError
	if errors.As(err, &responsesStreamErr) {
		return responsesStreamErr.retryable
	}
	var responsesEventErr *openAIResponsesEventError
	return errors.As(err, &responsesEventErr) && responsesEventErr.retryable
}

func responsesStreamRetryLimit(err error, committed bool) int {
	if committed {
		return 0
	}
	var responsesStreamErr *openAIResponsesStreamError
	if !errors.As(err, &responsesStreamErr) || !responsesStreamErr.retryable {
		return 0
	}
	var responsesEventErr *openAIResponsesEventError
	if errors.As(err, &responsesEventErr) {
		return 0
	}
	// Tool deltas and output items are not callbacks, so inspect the backend
	// counters explicitly before replaying a request.
	if responsesStreamErr.textDeltas != 0 || responsesStreamErr.toolDeltas != 0 || responsesStreamErr.toolCalls != 0 {
		return 0
	}
	if responsesStreamErr.failureKind == responsesJSONFailureKind && responsesStreamErr.reasoningDeltas > 0 {
		return responsesJSONMidstreamRetries
	}
	if responsesStreamErr.reasoningDeltas == 0 {
		return responsesOtherMidstreamRetries
	}
	return 0
}

func responsesStreamHasCommittedOutput(err error) bool {
	var responsesStreamErr *openAIResponsesStreamError
	return errors.As(err, &responsesStreamErr) &&
		(responsesStreamErr.textDeltas > 0 || responsesStreamErr.toolDeltas > 0 || responsesStreamErr.toolCalls > 0)
}

func responsesStreamHasToolCommittedOutput(err error) bool {
	var responsesStreamErr *openAIResponsesStreamError
	return errors.As(err, &responsesStreamErr) &&
		(responsesStreamErr.toolDeltas > 0 || responsesStreamErr.toolCalls > 0)
}

func responsesStreamFailureKindOf(err error) string {
	var responsesStreamErr *openAIResponsesStreamError
	if errors.As(err, &responsesStreamErr) {
		return responsesStreamErr.failureKind
	}
	return ""
}

func emitResponsesRetryOutcome(ctx context.Context, provider providerClient, req MessagesRequest, outcome string, attempt, limit int, err error) {
	status := telemetry.StatusError
	if outcome == responsesRetryOutcomeRecovered {
		status = telemetry.StatusOK
	}
	failureKind := responsesStreamFailureKindOf(err)
	props := map[string]any{
		"retry_attempt":     attempt,
		"retry_limit":       limit,
		"retry_outcome":     outcome,
		"failure_kind":      failureKind,
		"provider_protocol": string(config.ProviderProtocolOpenAIResponses),
	}
	var streamErr *openAIResponsesStreamError
	if errors.As(err, &streamErr) {
		streamProps := streamErr.properties()
		if requestID, _ := streamProps["provider_request_id"].(string); requestID != "" {
			props["last_failed_provider_request_id"] = requestID
		}
		for _, key := range []string{"http_status", "response_mime", "events", "reasoning_deltas", "text_deltas", "tool_deltas", "tool_calls"} {
			if value, ok := streamProps[key]; ok {
				props[key] = value
			}
		}
	}
	if responsesReasoningOnlyJSONFailure(err) {
		props["retry_reason"] = responsesRetryReasonReasoningOnlyDecode
	}
	emitModelPhase(ctx, "stream.retry", time.Now(), status, "", provider, req, props)
}

func responsesReasoningOnlyJSONFailure(err error) bool {
	var streamErr *openAIResponsesStreamError
	return errors.As(err, &streamErr) && streamErr.failureKind == responsesJSONFailureKind &&
		streamErr.reasoningDeltas > 0 && streamErr.textDeltas == 0 &&
		streamErr.toolDeltas == 0 && streamErr.toolCalls == 0
}

func responsesMidstreamBackoffDelay(retryAttempt int) time.Duration {
	delay := responsesMidstreamBackoff
	for i := 0; i < retryAttempt && delay < responsesMidstreamMaxBackoff; i++ {
		if delay > responsesMidstreamMaxBackoff/2 {
			return responsesMidstreamMaxBackoff
		}
		delay *= 2
	}
	if delay > responsesMidstreamMaxBackoff {
		return responsesMidstreamMaxBackoff
	}
	return delay
}

func isModelAccessFallbackError(status int, message string) bool {
	if status != http.StatusForbidden && status != http.StatusNotFound {
		return false
	}
	normalized := strings.ToLower(strings.TrimSpace(message))
	if normalized == "" {
		return false
	}
	for _, marker := range []string{
		"no access to model",
		"does not have access to model",
		"not have access to model",
		"model not found",
		"unknown model",
		"invalid model",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func formatProviderFailures(failures []providerFailure) error {
	if len(failures) == 0 {
		return errors.New("all providers failed")
	}
	parts := make([]string, 0, len(failures))
	for _, failure := range failures {
		if failure.err == nil {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s: %v", failure.name, failure.err))
	}
	return fmt.Errorf("all providers failed: %s", strings.Join(parts, "; "))
}

func openAIChatCompletionRequest(req MessagesRequest) (openai.ChatCompletionRequest, error) {
	params := openai.ChatCompletionRequest{
		Model:               req.Model,
		MaxCompletionTokens: req.MaxTokens,
		Messages:            make([]openai.ChatCompletionMessage, 0, len(req.Messages)+1),
		StreamOptions:       &openai.StreamOptions{IncludeUsage: true},
	}
	responseFormat, err := openAIResponseFormat(req.ResponseFormat)
	if err != nil {
		return openai.ChatCompletionRequest{}, err
	}
	params.ResponseFormat = responseFormat
	params.ReasoningEffort = openAIReasoningEffort(req.Thinking)
	if system := effectiveSystemText(req); strings.TrimSpace(system) != "" {
		params.Messages = append(params.Messages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: system,
		})
	}
	for _, message := range req.Messages {
		params.Messages = append(params.Messages, openAIChatCompletionMessages(message)...)
	}
	for _, tool := range req.Tools {
		if strings.TrimSpace(tool.Name) == "" {
			continue
		}
		schema := json.RawMessage(`{"type":"object"}`)
		if len(tool.InputSchema) != 0 {
			if !json.Valid(tool.InputSchema) {
				return openai.ChatCompletionRequest{}, fmt.Errorf("tool %s schema: invalid JSON", tool.Name)
			}
			schema = tool.InputSchema
		}
		params.Tools = append(params.Tools, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  schema,
			},
		})
	}
	return params, nil
}

// responseFormatInstructionMarker 是「JSON 输出约束已经在 system prompt 里了」的哨兵。
// internal/server 和 CLI 的 --json-schema 各自会先塞一份同样的约束，共用这句话就能在
// provider 边界上判断要不要再补一份，避免同一条指令出现两遍。
const responseFormatInstructionMarker = "Do not include markdown fences or explanatory text."

// responseFormatInstruction 把 response_format 翻译成 system prompt 里的自然语言约束。
func responseFormatInstruction(format *ResponseFormat) string {
	if format == nil {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(format.Type)) {
	case "json_object":
		return "Respond with a single valid JSON object. " + responseFormatInstructionMarker
	case "json_schema":
		if format.JSONSchema == nil {
			return "Respond with a single valid JSON value matching the requested JSON Schema. " + responseFormatInstructionMarker
		}
		var b strings.Builder
		b.WriteString("Respond with a single valid JSON value matching this JSON Schema. ")
		b.WriteString(responseFormatInstructionMarker)
		if name := strings.TrimSpace(format.JSONSchema.Name); name != "" {
			b.WriteString("\nSchema name: ")
			b.WriteString(name)
		}
		if description := strings.TrimSpace(format.JSONSchema.Description); description != "" {
			b.WriteString("\nSchema description: ")
			b.WriteString(description)
		}
		if format.JSONSchema.Strict {
			b.WriteString("\nUse strict schema adherence.")
		}
		if schema := strings.TrimSpace(string(format.JSONSchema.Schema)); schema != "" && schema != "null" && json.Valid(format.JSONSchema.Schema) {
			b.WriteString("\n\nJSON Schema:\n")
			b.WriteString(schema)
		}
		return b.String()
	default:
		return ""
	}
}

// openAIReasoningEffort 把内部的 thinking 配置映射成 OpenAI 的 reasoning_effort。两边
// 粒度不同（Anthropic 是 token 预算，OpenAI 是枚举档位），所以先认 effort 名字，认不出
// 再按预算分档。以前 OpenAI 路径完全不映射，调用方开了推理预算等于白开（AUDIT-P1-09）。
// 注意这个字段只在调用方显式配了 thinking 时才会出现在请求体里 —— 不支持推理的网关
// 不会因为这个改动突然收到未知参数。
func openAIReasoningEffort(cfg *ThinkingConfig) string {
	if cfg == nil {
		return ""
	}
	if strings.EqualFold(strings.TrimSpace(cfg.Type), "disabled") {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Effort)) {
	case "low":
		return "low"
	case "medium", "normal", "default":
		return "medium"
	case "high", "max", "maximum":
		return "high"
	}
	// effort 是裸 token 数或没给：按预算分档，档位和 thinkingBudgetTokens 对齐。
	switch {
	case cfg.BudgetTokens <= 0:
		return ""
	case cfg.BudgetTokens <= 1024:
		return "low"
	case cfg.BudgetTokens <= 2048:
		return "medium"
	default:
		return "high"
	}
}

func openAIResponseFormat(format *ResponseFormat) (*openai.ChatCompletionResponseFormat, error) {
	if format == nil {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(format.Type)) {
	case "json_object":
		return &openai.ChatCompletionResponseFormat{Type: openai.ChatCompletionResponseFormatTypeJSONObject}, nil
	case "json_schema":
		if format.JSONSchema == nil {
			return &openai.ChatCompletionResponseFormat{Type: openai.ChatCompletionResponseFormatTypeJSONSchema}, nil
		}
		schema := strings.TrimSpace(string(format.JSONSchema.Schema))
		var marshaler json.Marshaler
		if schema != "" && schema != "null" {
			if !json.Valid(format.JSONSchema.Schema) {
				return nil, errors.New("response_format json_schema.schema: invalid JSON")
			}
			marshaler = rawJSONMarshaler(format.JSONSchema.Schema)
		}
		return &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONSchema,
			JSONSchema: &openai.ChatCompletionResponseFormatJSONSchema{
				Name:        format.JSONSchema.Name,
				Description: format.JSONSchema.Description,
				Schema:      marshaler,
				Strict:      format.JSONSchema.Strict,
			},
		}, nil
	case "", "text":
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported response_format type %q", format.Type)
	}
}

type rawJSONMarshaler []byte

func (r rawJSONMarshaler) MarshalJSON() ([]byte, error) {
	if !json.Valid(r) {
		return nil, errors.New("invalid JSON")
	}
	return append([]byte(nil), r...), nil
}

func openAIChatCompletionMessages(message MessageParam) []openai.ChatCompletionMessage {
	role := strings.TrimSpace(message.Role)
	if role == "" {
		role = openai.ChatMessageRoleUser
	}
	var out []openai.ChatCompletionMessage
	var textParts []string
	var toolCalls []openai.ToolCall
	for _, block := range message.Content {
		switch block.Type {
		case "text", "":
			if block.Text != "" {
				textParts = append(textParts, block.Text)
			}
		case "image":
			if len(textParts) > 0 {
				out = append(out, openai.ChatCompletionMessage{Role: role, Content: strings.Join(textParts, "\n")})
				textParts = nil
			}
			url := ""
			if block.Source != nil {
				url = strings.TrimSpace(block.Source.URL)
				if url == "" && block.Source.Data != "" {
					mediaType := firstNonEmpty(block.Source.MediaType, "image/png")
					url = "data:" + mediaType + ";base64," + block.Source.Data
				}
			}
			if url != "" {
				out = append(out, openai.ChatCompletionMessage{
					Role: role,
					MultiContent: []openai.ChatMessagePart{{
						Type:     openai.ChatMessagePartTypeImageURL,
						ImageURL: &openai.ChatMessageImageURL{URL: url},
					}},
				})
				break
			}
			// 拿不出 URL 的 image 块以前整块消失，于是模型既看不到图也不知道有图。
			// Anthropic 路径在同样情形下会回落到 block.Text；这里对齐（AUDIT-P1-18）。
			if block.Text != "" {
				out = append(out, openai.ChatCompletionMessage{Role: role, Content: block.Text})
			}
		case "tool_use":
			toolCalls = append(toolCalls, openai.ToolCall{
				ID:   block.ID,
				Type: openai.ToolTypeFunction,
				Function: openai.FunctionCall{
					Name:      block.Name,
					Arguments: rawJSONString(block.Input),
				},
			})
		case "tool_result":
			if len(textParts) > 0 {
				out = append(out, openai.ChatCompletionMessage{Role: role, Content: strings.Join(textParts, "\n")})
				textParts = nil
			}
			out = append(out, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				Content:    block.Content,
				ToolCallID: block.ToolUseID,
			})
		default:
			if block.Text != "" {
				textParts = append(textParts, block.Text)
			}
		}
	}
	msg := openai.ChatCompletionMessage{Role: role, Content: strings.Join(textParts, "\n")}
	if role == openai.ChatMessageRoleAssistant && len(toolCalls) > 0 {
		msg.ToolCalls = toolCalls
	}
	if msg.Content != "" || len(msg.ToolCalls) > 0 || len(out) == 0 {
		out = append(out, msg)
	}
	return out
}

func rawJSONString(raw json.RawMessage) string {
	if len(raw) == 0 || !json.Valid(raw) {
		return "{}"
	}
	return string(raw)
}

func openAIStopReason(reason openai.FinishReason) string {
	switch string(reason) {
	case "tool_calls", "function_call":
		return "tool_use"
	case "stop":
		return "end_turn"
	default:
		return string(reason)
	}
}

func sortedToolCallIndexes(calls map[int]*openAIToolAccumulator) []int {
	indexes := make([]int, 0, len(calls))
	for index := range calls {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	return indexes
}

func openAIToolArguments(arguments string) json.RawMessage {
	parts := openAIToolArgumentParts(arguments)
	if len(parts) == 0 {
		return json.RawMessage(`{}`)
	}
	return parts[0]
}

func openAIToolArgumentParts(arguments string) []json.RawMessage {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" {
		return []json.RawMessage{json.RawMessage(`{}`)}
	}
	if json.Valid([]byte(trimmed)) {
		return []json.RawMessage{json.RawMessage(trimmed)}
	}
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	var parts []json.RawMessage
	for {
		var raw json.RawMessage
		err := decoder.Decode(&raw)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return []json.RawMessage{openAIToolArgumentsFallback(trimmed)}
		}
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 || !json.Valid(raw) {
			return []json.RawMessage{openAIToolArgumentsFallback(trimmed)}
		}
		parts = append(parts, append(json.RawMessage(nil), raw...))
	}
	if len(parts) > 1 {
		return parts
	}
	return []json.RawMessage{openAIToolArgumentsFallback(trimmed)}
}

func openAIToolArgumentsFallback(trimmed string) json.RawMessage {
	if trimmed == "" {
		return json.RawMessage(`{}`)
	}
	encoded, err := json.Marshal(map[string]string{"arguments": trimmed})
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

func sdkMessageParams(req MessagesRequest) (sdkanthropic.MessageNewParams, error) {
	params := sdkanthropic.MessageNewParams{
		Model:     sdkanthropic.Model(req.Model),
		MaxTokens: int64(req.MaxTokens),
		Messages:  make([]sdkanthropic.MessageParam, 0, len(req.Messages)),
	}
	if len(req.SystemBlocks) > 0 {
		params.System = sdkSystemBlocks(req.SystemBlocks)
	} else if strings.TrimSpace(req.System) != "" {
		params.System = []sdkanthropic.TextBlockParam{{Text: req.System}}
	}
	// Anthropic 的 Messages API 没有 response_format 字段，唯一能落地这个约束的地方就是
	// system prompt。以前这个字段在 Anthropic 路径上被静默丢弃，只有 server 层自己做了
	// prompt 兜底，CLI / 子代理路径完全没有（AUDIT-P1-09）。放在 provider 边界上，所有
	// 调用方就都覆盖到了；上层已经塞过同样约束时不再重复塞。
	if instruction := responseFormatInstruction(req.ResponseFormat); instruction != "" &&
		!strings.Contains(effectiveSystemText(req), responseFormatInstructionMarker) {
		params.System = append(params.System, sdkanthropic.TextBlockParam{Text: instruction})
	}
	for _, message := range req.Messages {
		params.Messages = append(params.Messages, sdkMessageParam(message))
	}
	for _, tool := range req.Tools {
		sdkTool, err := sdkToolParam(tool)
		if err != nil {
			return sdkanthropic.MessageNewParams{}, err
		}
		params.Tools = append(params.Tools, sdkTool)
	}
	if req.Thinking != nil {
		params.Thinking = sdkThinkingConfig(req.Thinking)
	}
	return params, nil
}

func sdkThinkingConfig(cfg *ThinkingConfig) sdkanthropic.ThinkingConfigParamUnion {
	if cfg == nil {
		return sdkanthropic.ThinkingConfigParamUnion{}
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Type)) {
	case "disabled":
		disabled := sdkanthropic.NewThinkingConfigDisabledParam()
		return sdkanthropic.ThinkingConfigParamUnion{OfDisabled: &disabled}
	case "adaptive":
		adaptive := sdkanthropic.ThinkingConfigAdaptiveParam{}
		if strings.EqualFold(cfg.Display, "omitted") {
			adaptive.Display = sdkanthropic.ThinkingConfigAdaptiveDisplayOmitted
		} else if strings.EqualFold(cfg.Display, "summarized") {
			adaptive.Display = sdkanthropic.ThinkingConfigAdaptiveDisplaySummarized
		}
		return sdkanthropic.ThinkingConfigParamUnion{OfAdaptive: &adaptive}
	default:
		if cfg.BudgetTokens <= 0 {
			return sdkanthropic.ThinkingConfigParamUnion{}
		}
		enabled := sdkanthropic.ThinkingConfigEnabledParam{BudgetTokens: int64(cfg.BudgetTokens)}
		if strings.EqualFold(cfg.Display, "omitted") {
			enabled.Display = sdkanthropic.ThinkingConfigEnabledDisplayOmitted
		} else if strings.EqualFold(cfg.Display, "summarized") {
			enabled.Display = sdkanthropic.ThinkingConfigEnabledDisplaySummarized
		}
		return sdkanthropic.ThinkingConfigParamUnion{OfEnabled: &enabled}
	}
}

func effectiveSystemText(req MessagesRequest) string {
	if len(req.SystemBlocks) == 0 {
		return req.System
	}
	parts := make([]string, 0, len(req.SystemBlocks))
	for _, block := range req.SystemBlocks {
		if text := strings.TrimSpace(block.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func sdkSystemBlocks(blocks []SystemBlock) []sdkanthropic.TextBlockParam {
	out := make([]sdkanthropic.TextBlockParam, 0, len(blocks))
	for _, block := range blocks {
		text := strings.TrimSpace(block.Text)
		if text == "" {
			continue
		}
		param := sdkanthropic.TextBlockParam{Text: text}
		if cache, ok := sdkCacheControl(block.CacheControl); ok {
			param.CacheControl = cache
		}
		out = append(out, param)
	}
	return out
}

func sdkCacheControl(cacheControl *CacheControl) (sdkanthropic.CacheControlEphemeralParam, bool) {
	if cacheControl == nil || strings.TrimSpace(cacheControl.Type) != "ephemeral" {
		return sdkanthropic.CacheControlEphemeralParam{}, false
	}
	cache := sdkanthropic.NewCacheControlEphemeralParam()
	switch strings.TrimSpace(cacheControl.TTL) {
	case "1h":
		cache.TTL = sdkanthropic.CacheControlEphemeralTTLTTL1h
	case "5m":
		cache.TTL = sdkanthropic.CacheControlEphemeralTTLTTL5m
	}
	if scope := strings.TrimSpace(cacheControl.Scope); scope != "" {
		cache.SetExtraFields(map[string]any{"scope": scope})
	}
	return cache, true
}

func sdkMessageParam(message MessageParam) sdkanthropic.MessageParam {
	blocks := make([]sdkanthropic.ContentBlockParamUnion, 0, len(message.Content))
	for _, block := range message.Content {
		var sdkBlock sdkanthropic.ContentBlockParamUnion
		switch block.Type {
		case "image":
			if block.Source != nil && block.Source.Data != "" {
				sdkBlock = sdkanthropic.NewImageBlockBase64(firstNonEmpty(block.Source.MediaType, "image/png"), block.Source.Data)
			} else if block.Source != nil && block.Source.URL != "" {
				sdkBlock = sdkanthropic.NewImageBlock(sdkanthropic.URLImageSourceParam{URL: block.Source.URL})
			} else {
				sdkBlock = sdkanthropic.NewTextBlock(block.Text)
			}
		case "tool_use":
			sdkBlock = sdkanthropic.NewToolUseBlock(block.ID, rawJSONValue(block.Input), block.Name)
		case "tool_result":
			sdkBlock = sdkanthropic.NewToolResultBlock(block.ToolUseID, block.Content, block.IsError)
		case "thinking":
			// thinking 块的正文在 .Thinking、签名在 .Signature，.Text 恒为空。走 default
			// 会退化成 NewTextBlock("")：既丢 signature（多轮 tool use 时 Anthropic 要求
			// 原样回传），又让 API 以 "text content blocks must be non-empty" 拒掉整个
			// 请求（AUDIT-P1-07）。没有 signature 的块（老会话日志 resume）一定会被拒，
			// 丢掉比发一个必错的块更好。
			if strings.TrimSpace(block.Thinking) == "" || strings.TrimSpace(block.Signature) == "" {
				continue
			}
			sdkBlock = sdkanthropic.NewThinkingBlock(block.Signature, block.Thinking)
		case "redacted_thinking":
			// 被安全系统打码的推理块只有密文 data，同样必须原样回传。
			if strings.TrimSpace(block.Data) == "" {
				continue
			}
			sdkBlock = sdkanthropic.NewRedactedThinkingBlock(block.Data)
		default:
			sdkBlock = sdkanthropic.NewTextBlock(block.Text)
		}
		if cache, ok := sdkCacheControl(block.CacheControl); ok {
			switch {
			case sdkBlock.OfText != nil:
				sdkBlock.OfText.CacheControl = cache
			case sdkBlock.OfToolUse != nil:
				sdkBlock.OfToolUse.CacheControl = cache
			case sdkBlock.OfToolResult != nil:
				sdkBlock.OfToolResult.CacheControl = cache
			}
		}
		blocks = append(blocks, sdkBlock)
	}
	switch message.Role {
	case "assistant":
		return sdkanthropic.NewAssistantMessage(blocks...)
	default:
		return sdkanthropic.NewUserMessage(blocks...)
	}
}

func sdkToolParam(tool ToolDefinition) (sdkanthropic.ToolUnionParam, error) {
	schema, err := sdkToolInputSchema(tool.InputSchema)
	if err != nil {
		return sdkanthropic.ToolUnionParam{}, fmt.Errorf("tool %s schema: %w", tool.Name, err)
	}
	param := sdkanthropic.ToolUnionParamOfTool(schema, tool.Name)
	if tool.Description != "" && param.OfTool != nil {
		param.OfTool.Description = sdkanthropic.String(tool.Description)
	}
	return param, nil
}

func sdkToolInputSchema(raw json.RawMessage) (sdkanthropic.ToolInputSchemaParam, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{"type":"object"}`)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return sdkanthropic.ToolInputSchemaParam{}, err
	}
	out := sdkanthropic.ToolInputSchemaParam{ExtraFields: map[string]any{}}
	if properties, ok := schema["properties"]; ok {
		out.Properties = properties
	}
	if required, ok := schema["required"].([]any); ok {
		for _, item := range required {
			if name, ok := item.(string); ok {
				out.Required = append(out.Required, name)
			}
		}
	}
	for key, value := range schema {
		switch key {
		case "type", "properties", "required":
		default:
			out.ExtraFields[key] = value
		}
	}
	return out, nil
}

func rawJSONValue(raw json.RawMessage) any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return map[string]any{}
	}
	return value
}

func streamResultFromSDKMessage(message sdkanthropic.Message) *StreamResult {
	content := make([]ContentBlock, 0, len(message.Content))
	for _, block := range message.Content {
		switch block.Type {
		case "tool_use":
			content = append(content, ContentBlock{
				Type:  "tool_use",
				ID:    block.ID,
				Name:  block.Name,
				Input: block.Input,
			})
		case "text":
			content = append(content, ContentBlock{Type: "text", Text: block.Text, Citations: rawTextCitations(block.Citations)})
		case "thinking":
			content = append(content, ContentBlock{Type: "thinking", Thinking: block.Thinking, Signature: block.Signature})
		case "redacted_thinking":
			// 不落这个分支的话打码推理块会被整块丢弃，下一轮就没法按 Anthropic 的要求回传。
			content = append(content, ContentBlock{Type: "redacted_thinking", Data: block.Data})
		}
	}
	return &StreamResult{
		Message: MessageParam{
			Role:    "assistant",
			Content: content,
		},
		StopReason: string(message.StopReason),
		Usage: Usage{
			InputTokens:                         int(message.Usage.InputTokens),
			OutputTokens:                        int(message.Usage.OutputTokens),
			CacheCreationInputTokens:            int(message.Usage.CacheCreationInputTokens),
			CacheReadInputTokens:                int(message.Usage.CacheReadInputTokens),
			CacheCreationEphemeral1hInputTokens: int(message.Usage.CacheCreation.Ephemeral1hInputTokens),
			CacheCreationEphemeral5mInputTokens: int(message.Usage.CacheCreation.Ephemeral5mInputTokens),
			CacheCreation: UsageCacheCreation{
				Ephemeral1hInputTokens: int(message.Usage.CacheCreation.Ephemeral1hInputTokens),
				Ephemeral5mInputTokens: int(message.Usage.CacheCreation.Ephemeral5mInputTokens),
			},
			ServiceTier:  string(message.Usage.ServiceTier),
			InferenceGeo: string(message.Usage.InferenceGeo),
		},
	}
}

func rawTextCitations(citations []sdkanthropic.TextCitationUnion) []json.RawMessage {
	if len(citations) == 0 {
		return nil
	}
	out := make([]json.RawMessage, 0, len(citations))
	for _, citation := range citations {
		if raw := strings.TrimSpace(citation.RawJSON()); raw != "" && json.Valid([]byte(raw)) {
			out = append(out, json.RawMessage(raw))
		}
	}
	return out
}

func rawCitation(citation sdkanthropic.CitationsDeltaCitationUnion) json.RawMessage {
	raw := strings.TrimSpace(citation.RawJSON())
	if raw == "" || !json.Valid([]byte(raw)) {
		return nil
	}
	return json.RawMessage(raw)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
