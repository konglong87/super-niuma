package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/config"
	providerstate "github.com/konglong87/super-niuma/internal/provider"
	"github.com/konglong87/super-niuma/internal/telemetry"
	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

const (
	responsesEventOutputTextDelta           = "response.output_text.delta"
	responsesEventReasoningSummaryTextDelta = "response.reasoning_summary_text.delta"
	responsesEventFunctionArgumentsDelta    = "response.function_call_arguments.delta"
	responsesEventFunctionArgumentsDone     = "response.function_call_arguments.done"
	responsesEventOutputItemAdded           = "response.output_item.added"
	responsesEventOutputItemDone            = "response.output_item.done"
	responsesEventCompleted                 = "response.completed"
	responsesEventIncomplete                = "response.incomplete"
	responsesEventFailed                    = "response.failed"
	responsesEventError                     = "error"
	responsesReplayMessageIDPrefix          = "msg_golang_cc_replay_"
	responsesContinuationBlockType          = "provider_continuation"
)

type openAIResponsesBackend struct {
	client   openai.Client
	provider providerClientMetadata
}

const (
	responsesStreamFailureKind = "responses_stream"
	responsesJSONFailureKind   = "responses_sse_json_decode"
)

// openAIResponsesStreamError preserves the provider protocol boundary while
// keeping the original decoder/network error available through errors.Is/As.
// The counters are safe diagnostics only; raw SSE data is intentionally not
// retained because it can contain prompt or tool payloads.
type openAIResponsesStreamError struct {
	err               error
	failureKind       string
	retryable         bool
	events            int
	textDeltas        int
	toolDeltas        int
	toolCalls         int
	reasoningDeltas   int
	firstEvent        bool
	firstDelta        bool
	providerRequestID string
	httpStatus        int
	responseMIME      string
}

func (e *openAIResponsesStreamError) Error() string {
	if e == nil || e.err == nil {
		return "OpenAI Responses stream failed"
	}
	return e.err.Error()
}

func (e *openAIResponsesStreamError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func (e *openAIResponsesStreamError) properties() map[string]any {
	if e == nil {
		return nil
	}
	props := map[string]any{
		"events":           e.events,
		"text_deltas":      e.textDeltas,
		"tool_deltas":      e.toolDeltas,
		"tool_calls":       e.toolCalls,
		"reasoning_deltas": e.reasoningDeltas,
		"first_event_seen": e.firstEvent,
		"first_delta_seen": e.firstDelta,
		"failure_kind":     e.failureKind,
		"retryable":        e.retryable,
	}
	if e.providerRequestID != "" {
		props["provider_request_id"] = e.providerRequestID
	}
	if e.httpStatus != 0 {
		props["http_status"] = e.httpStatus
	}
	if e.responseMIME != "" {
		props["response_mime"] = e.responseMIME
	}
	return props
}

func newOpenAIResponsesStreamError(err error, state *openAIResponsesStreamState, firstEventAt, firstDeltaAt time.Time, responseMeta map[string]any) error {
	if err == nil {
		return nil
	}
	kind := responsesStreamFailureKind
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) || errors.Is(err, io.ErrUnexpectedEOF) {
		kind = responsesJSONFailureKind
	}
	retryable := !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, errProviderTimeout)
	var eventErr *openAIResponsesEventError
	if errors.As(err, &eventErr) {
		retryable = eventErr.retryable
	}
	wrapped := &openAIResponsesStreamError{err: err, failureKind: kind, retryable: retryable}
	if state != nil {
		wrapped.events = state.events
		wrapped.textDeltas = state.textDeltas
		wrapped.toolDeltas = state.toolDeltas
		wrapped.toolCalls = len(state.toolOrder)
		wrapped.reasoningDeltas = state.reasonDeltas
	}
	wrapped.firstEvent = !firstEventAt.IsZero()
	wrapped.firstDelta = !firstDeltaAt.IsZero()
	wrapped.providerRequestID, _ = responseMeta["provider_request_id"].(string)
	wrapped.httpStatus, _ = responseMeta["http_status"].(int)
	wrapped.responseMIME, _ = responseMeta["response_mime"].(string)
	return wrapped
}

type providerClientMetadata struct {
	name     string
	role     string
	kind     string
	endpoint string
}

func newOpenAIResponsesBackend(spec providerBackendSpec) (providerBackend, error) {
	auth := firstNonEmpty(spec.apiKey, spec.authToken)
	if strings.TrimSpace(auth) == "" {
		return nil, errors.New("OpenAI Responses API key or auth token is required")
	}
	httpClient := responsesSSEClient{inner: newHTTPTraceClient(nil)}
	client := openai.NewClient(
		option.WithBaseURL(spec.baseURL),
		option.WithAPIKey(auth),
		option.WithHTTPClient(httpClient),
	)
	return &openAIResponsesBackend{
		client: client,
		provider: providerClientMetadata{
			name: spec.name, role: spec.role, kind: spec.kind, endpoint: spec.endpoint,
		},
	}, nil
}

func (*openAIResponsesBackend) Protocol() config.ProviderProtocol {
	return config.ProviderProtocolOpenAIResponses
}

func (*openAIResponsesBackend) Capabilities() providerCapabilities {
	return providerCapabilities{
		capabilityFunctionCalling:    true,
		capabilityReasoningSummary:   true,
		capabilityPreviousResponseID: false,
	}
}

func (b *openAIResponsesBackend) StreamMessages(ctx context.Context, req MessagesRequest, cb StreamCallbacks) (*StreamResult, error) {
	start := time.Now()
	params, err := openAIResponsesRequest(req)
	if err != nil {
		return nil, err
	}
	b.emitPhase(ctx, "request.build", start, telemetry.StatusOK, "", req, map[string]any{
		"messages": len(req.Messages),
		"items":    len(params.Input.OfInputItemList),
		"tools":    len(params.Tools),
	})

	streamStart := time.Now()
	traceProvider := providerClient{
		name: b.provider.name, role: b.provider.role, kind: b.provider.kind, endpoint: b.provider.endpoint,
	}
	trace := newHTTPPhaseTrace(ctx, streamStart, traceProvider, req)
	stream := b.client.Responses.NewStreaming(trace.context(ctx), params)
	defer stream.Close()

	state := newOpenAIResponsesStreamState()
	readStart := time.Now()
	var firstEventAt time.Time
	var firstDeltaAt time.Time
	for stream.Next() {
		event := stream.Current()
		state.events++
		if firstEventAt.IsZero() {
			firstEventAt = time.Now()
			trace.finish(firstEventAt)
			b.emitPhase(ctx, "stream.create", streamStart, telemetry.StatusOK, "", req, trace.properties(firstEventAt))
			b.emitPhase(ctx, "stream.first_event", readStart, telemetry.StatusOK, "", req,
				responsesStreamPhaseProperties(trace, firstEventAt, map[string]any{"event_type": event.Type}))
		}
		delta, err := state.consume(event, cb)
		if err != nil {
			return nil, partialStreamError(err, b.result(req, state))
		}
		if delta && firstDeltaAt.IsZero() {
			firstDeltaAt = time.Now()
			b.emitPhase(ctx, "stream.first_delta", readStart, telemetry.StatusOK, "", req,
				responsesStreamPhaseProperties(trace, firstDeltaAt, map[string]any{"delta_type": event.Type}))
		}
	}
	streamErr := stream.Err()
	if firstEventAt.IsZero() {
		trace.finish(time.Now())
		status := telemetry.StatusOK
		errorMessage := ""
		if streamErr != nil {
			status = telemetry.StatusError
			errorMessage = streamErr.Error()
		}
		b.emitPhase(ctx, "stream.create", streamStart, status, errorMessage, req, trace.properties(time.Now()))
	}
	if streamErr != nil {
		streamErr = newOpenAIResponsesStreamError(streamErr, state, firstEventAt, firstDeltaAt, trace.properties(time.Now()))
		props := responsesStreamPhaseProperties(trace, time.Now(), state.phaseProperties(readStart, firstEventAt, firstDeltaAt))
		for key, value := range streamErr.(*openAIResponsesStreamError).properties() {
			props[key] = value
		}
		b.emitPhase(ctx, "stream.read", readStart, telemetry.StatusError, streamErr.Error(), req, props)
		return nil, partialStreamError(streamErr, b.result(req, state))
	}
	if state.terminalErr != nil {
		terminalErr := newOpenAIResponsesStreamError(state.terminalErr, state, firstEventAt, firstDeltaAt, trace.properties(time.Now()))
		props := responsesStreamPhaseProperties(trace, time.Now(), state.phaseProperties(readStart, firstEventAt, firstDeltaAt))
		for key, value := range terminalErr.(*openAIResponsesStreamError).properties() {
			props[key] = value
		}
		b.emitPhase(ctx, "stream.read", readStart, telemetry.StatusError, terminalErr.Error(), req, props)
		return nil, partialStreamError(terminalErr, b.result(req, state))
	}
	b.emitPhase(ctx, "stream.read", readStart, telemetry.StatusOK, "", req,
		responsesStreamPhaseProperties(trace, time.Now(), state.phaseProperties(readStart, firstEventAt, firstDeltaAt)))
	return b.result(req, state), nil
}

func responsesStreamPhaseProperties(trace *httpPhaseTrace, at time.Time, props map[string]any) map[string]any {
	if props == nil {
		props = map[string]any{}
	}
	traceProps := trace.properties(at)
	for _, key := range []string{"provider_request_id", "http_status", "response_mime"} {
		if value, ok := traceProps[key]; ok {
			props[key] = value
		}
	}
	return props
}

func (b *openAIResponsesBackend) result(req MessagesRequest, state *openAIResponsesStreamState) *StreamResult {
	result := state.result()
	if len(state.opaqueItems) == 0 {
		return result
	}
	continuation := providerstate.Continuation{
		Version:     providerstate.ContinuationVersion,
		Protocol:    config.ProviderProtocolOpenAIResponses,
		Provider:    b.provider.name,
		EndpointID:  providerstate.EndpointID(b.provider.endpoint),
		Model:       req.Model,
		OpaqueItems: append([]providerstate.OpaqueItem(nil), state.opaqueItems...),
	}
	result.Message.Content = append([]ContentBlock{{Type: responsesContinuationBlockType, Continuation: &continuation}}, result.Message.Content...)
	return result
}

func (b *openAIResponsesBackend) emitPhase(ctx context.Context, phase string, start time.Time, status, errorMessage string, req MessagesRequest, props map[string]any) {
	if props == nil {
		props = map[string]any{}
	}
	props["phase"] = phase
	props["provider"] = b.provider.name
	props["provider_name"] = b.provider.name
	props["provider_role"] = b.provider.role
	props["provider_kind"] = b.provider.kind
	props["provider_protocol"] = string(config.ProviderProtocolOpenAIResponses)
	props["endpoint"] = b.provider.endpoint
	if attempt := responsesRetryAttempt(ctx); attempt > 0 {
		props["retry_attempt"] = attempt
	}
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

type openAIResponsesToolAccumulator struct {
	index     int64
	itemID    string
	callID    string
	name      string
	arguments strings.Builder
}

type openAIResponsesStreamState struct {
	text         strings.Builder
	textItemID   string
	reasoning    strings.Builder
	tools        map[string]*openAIResponsesToolAccumulator
	toolOrder    []string
	usage        Usage
	stopReason   string
	terminalErr  error
	events       int
	textDeltas   int
	toolDeltas   int
	reasonDeltas int
	opaqueItems  []providerstate.OpaqueItem
}

func newOpenAIResponsesStreamState() *openAIResponsesStreamState {
	return &openAIResponsesStreamState{tools: make(map[string]*openAIResponsesToolAccumulator)}
}

func (s *openAIResponsesStreamState) consume(event responses.ResponseStreamEventUnion, cb StreamCallbacks) (bool, error) {
	switch event.Type {
	case responsesEventOutputTextDelta:
		if event.Delta == "" {
			return false, nil
		}
		if s.textItemID == "" {
			s.textItemID = event.ItemID
		}
		s.textDeltas++
		s.text.WriteString(event.Delta)
		if cb.OnText != nil {
			return true, cb.OnText(event.Delta)
		}
		return true, nil
	case responsesEventReasoningSummaryTextDelta:
		if event.Delta == "" {
			return false, nil
		}
		s.reasonDeltas++
		s.reasoning.WriteString(event.Delta)
		if cb.OnThinking != nil {
			return true, cb.OnThinking(event.Delta)
		}
		return true, nil
	case responsesEventOutputItemAdded, responsesEventOutputItemDone:
		if event.Item.Type == "message" && s.textItemID == "" {
			s.textItemID = firstNonEmpty(event.ItemID, event.Item.ID)
		} else if event.Item.Type == "function_call" {
			acc := s.tool(firstNonEmpty(event.ItemID, event.Item.ID), event.OutputIndex)
			acc.applyItem(event.Item)
		}
	case responsesEventFunctionArgumentsDelta:
		if event.Delta != "" {
			s.toolDeltas++
			s.tool(event.ItemID, event.OutputIndex).arguments.WriteString(event.Delta)
			return true, nil
		}
	case responsesEventFunctionArgumentsDone:
		acc := s.tool(event.ItemID, event.OutputIndex)
		if event.Name != "" {
			acc.name = event.Name
		}
		if event.Arguments != "" {
			acc.arguments.Reset()
			acc.arguments.WriteString(event.Arguments)
		}
	case responsesEventCompleted:
		s.applyResponse(event.Response)
		if s.stopReason == "" {
			s.stopReason = "end_turn"
		}
	case responsesEventIncomplete:
		s.applyResponse(event.Response)
		s.stopReason = responsesIncompleteStopReason(event.Response.IncompleteDetails.Reason)
	case responsesEventFailed:
		s.applyResponse(event.Response)
		s.terminalErr = responseTerminalError(event.Response)
	case responsesEventError:
		s.terminalErr = newOpenAIResponsesEventError(event.Code, event.Message)
	}
	return false, nil
}

func (s *openAIResponsesStreamState) tool(itemID string, index int64) *openAIResponsesToolAccumulator {
	key := strings.TrimSpace(itemID)
	if key == "" {
		key = fmt.Sprintf("output-%d", index)
	}
	if acc := s.tools[key]; acc != nil {
		return acc
	}
	// Some Responses-compatible gateways put call_id, rather than the output
	// item's id, in function argument events. Treat both identifiers as aliases
	// so one upstream function call cannot become two local tool executions.
	for _, existingKey := range s.toolOrder {
		acc := s.tools[existingKey]
		if acc == nil {
			continue
		}
		if key == strings.TrimSpace(acc.itemID) || key == strings.TrimSpace(acc.callID) {
			s.tools[key] = acc
			return acc
		}
	}
	acc := &openAIResponsesToolAccumulator{index: index, itemID: itemID}
	s.tools[key] = acc
	s.toolOrder = append(s.toolOrder, key)
	return acc
}

func (a *openAIResponsesToolAccumulator) applyItem(item responses.ResponseOutputItemUnion) {
	if item.ID != "" {
		a.itemID = item.ID
	}
	if item.CallID != "" {
		a.callID = item.CallID
	}
	if item.Name != "" {
		a.name = item.Name
	}
	if args := item.Arguments.OfString; args != "" {
		a.arguments.Reset()
		a.arguments.WriteString(args)
	}
}

func (s *openAIResponsesStreamState) applyResponse(response responses.Response) {
	s.usage = Usage{
		InputTokens:                 int(response.Usage.InputTokens),
		OutputTokens:                int(response.Usage.OutputTokens),
		CacheReadInputTokens:        int(response.Usage.InputTokensDetails.CachedTokens),
		ReasoningOutputTokens:       int(response.Usage.OutputTokensDetails.ReasoningTokens),
		InputTokensIncludeCacheRead: true,
		ServiceTier:                 string(response.ServiceTier),
	}
	for index, item := range response.Output {
		if item.Type == "message" && s.textItemID == "" {
			s.textItemID = item.ID
		}
		if item.Type == "reasoning" && item.ID != "" && item.EncryptedContent != "" {
			s.opaqueItems = append(s.opaqueItems, providerstate.OpaqueItem{
				Type: item.Type, ID: item.ID, EncryptedContent: item.EncryptedContent,
			})
			continue
		}
		if item.Type != "function_call" {
			continue
		}
		acc := s.tool(item.ID, int64(index))
		acc.applyItem(item)
	}
}

func (s *openAIResponsesStreamState) result() *StreamResult {
	content := make([]ContentBlock, 0, 2+len(s.tools))
	if s.reasoning.Len() > 0 {
		content = append(content, ContentBlock{Type: "thinking", Thinking: s.reasoning.String()})
	}
	if s.text.Len() > 0 {
		content = append(content, ContentBlock{Type: "text", ID: s.textItemID, Text: s.text.String()})
	}
	toolBlocks := s.toolBlocks()
	content = append(content, toolBlocks...)
	stopReason := s.stopReason
	if len(toolBlocks) > 0 && stopReason != "max_tokens" && stopReason != "content_filter" {
		stopReason = "tool_use"
	}
	return &StreamResult{Message: MessageParam{Role: "assistant", Content: content}, StopReason: stopReason, Usage: s.usage}
}

func (s *openAIResponsesStreamState) toolBlocks() []ContentBlock {
	blocks := make([]ContentBlock, 0, len(s.toolOrder))
	byCallID := make(map[string]int, len(s.toolOrder))
	for _, key := range s.toolOrder {
		acc := s.tools[key]
		if acc == nil || strings.TrimSpace(acc.name) == "" {
			continue
		}
		arguments := json.RawMessage(acc.arguments.String())
		argumentsValid := json.Valid(arguments)
		if !argumentsValid {
			arguments = json.RawMessage(`{}`)
		}
		callID := firstNonEmpty(acc.callID, acc.itemID, key)
		block := ContentBlock{Type: "tool_use", ID: callID, Name: acc.name, Input: arguments}
		if index, duplicate := byCallID[callID]; duplicate {
			// A call_id is the execution identity. Some compatible gateways emit
			// the same call through multiple item/index paths; never let that
			// become duplicate local tool execution. Prefer the richer arguments.
			if argumentsValid && len(arguments) > len(blocks[index].Input) {
				blocks[index] = block
			}
			continue
		}
		byCallID[callID] = len(blocks)
		blocks = append(blocks, block)
	}
	return blocks
}

func (s *openAIResponsesStreamState) phaseProperties(readStart, firstEventAt, firstDeltaAt time.Time) map[string]any {
	return map[string]any{
		"events":             s.events,
		"text_deltas":        s.textDeltas,
		"tool_deltas":        s.toolDeltas,
		"reasoning_deltas":   s.reasonDeltas,
		"first_event_gap_ms": durationSince(readStart, firstEventAt),
		"first_delta_gap_ms": durationSince(readStart, firstDeltaAt),
	}
}

type openAIResponsesEventError struct {
	code      string
	message   string
	retryable bool
}

func (e *openAIResponsesEventError) Error() string {
	return fmt.Sprintf("OpenAI Responses stream error %s: %s", e.code, e.message)
}

func newOpenAIResponsesEventError(code, message string) error {
	code = strings.TrimSpace(code)
	message = strings.TrimSpace(message)
	if message == "" {
		message = "response failed"
	}
	normalized := strings.ToLower(code + " " + message)
	retryable := false
	for _, marker := range []string{"server_error", "internal_error", "overloaded", "rate_limit", "timeout", "temporarily unavailable"} {
		if strings.Contains(normalized, marker) {
			retryable = true
			break
		}
	}
	return &openAIResponsesEventError{code: code, message: message, retryable: retryable}
}

func responseTerminalError(response responses.Response) error {
	message := strings.TrimSpace(response.Error.Message)
	return newOpenAIResponsesEventError(string(response.Error.Code), message)
}

func responsesIncompleteStopReason(reason string) string {
	switch reason {
	case "max_output_tokens":
		return "max_tokens"
	case "content_filter":
		return "content_filter"
	default:
		return "incomplete"
	}
}

func openAIResponsesRequest(req MessagesRequest) (responses.ResponseNewParams, error) {
	input, err := openAIResponsesInput(req.Messages)
	if err != nil {
		return responses.ResponseNewParams{}, err
	}
	params := responses.ResponseNewParams{
		Model:           shared.ResponsesModel(req.Model),
		MaxOutputTokens: param.NewOpt(int64(req.MaxTokens)),
		Store:           param.NewOpt(false),
		Input:           responses.ResponseNewParamsInputUnion{OfInputItemList: input},
	}
	instructions := effectiveSystemText(req)
	if instructions != "" {
		params.Instructions = param.NewOpt(instructions)
	}
	if effort := openAIReasoningEffort(req.Thinking); effort != "" {
		params.Reasoning.Effort = shared.ReasoningEffort(effort)
		params.Reasoning.Summary = shared.ReasoningSummaryAuto
		params.Include = []responses.ResponseIncludable{responses.ResponseIncludableReasoningEncryptedContent}
	}
	if len(req.Tools) > 0 {
		params.Tools = make([]responses.ToolUnionParam, 0, len(req.Tools))
		for _, tool := range req.Tools {
			schema := map[string]any{"type": "object"}
			if len(tool.InputSchema) > 0 {
				if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
					return responses.ResponseNewParams{}, fmt.Errorf("tool %s schema: invalid JSON", tool.Name)
				}
			}
			function := responses.ToolParamOfFunction(tool.Name, schema, false)
			if function.OfFunction != nil && tool.Description != "" {
				function.OfFunction.Description = param.NewOpt(tool.Description)
			}
			params.Tools = append(params.Tools, function)
		}
	}
	if err := applyOpenAIResponsesFormat(&params, req.ResponseFormat); err != nil {
		return responses.ResponseNewParams{}, err
	}
	return params, nil
}

func openAIResponsesInput(messages []MessageParam) ([]responses.ResponseInputItemUnionParam, error) {
	items := make([]responses.ResponseInputItemUnionParam, 0, len(messages))
	for messageIndex, message := range messages {
		role := responses.EasyInputMessageRole(strings.ToLower(strings.TrimSpace(message.Role)))
		if role != responses.EasyInputMessageRoleUser && role != responses.EasyInputMessageRoleAssistant && role != responses.EasyInputMessageRoleSystem && role != responses.EasyInputMessageRoleDeveloper {
			return nil, fmt.Errorf("unsupported Responses message role %q", message.Role)
		}
		var inputContent responses.ResponseInputMessageContentListParam
		var outputContent []responses.ResponseOutputMessageContentUnionParam
		outputMessageID := ""
		outputSegment := 0
		flush := func() {
			if role == responses.EasyInputMessageRoleAssistant {
				if len(outputContent) == 0 {
					return
				}
				messageID := outputMessageID
				if messageID == "" {
					messageID = fmt.Sprintf("%s%d_%d", responsesReplayMessageIDPrefix, messageIndex, outputSegment)
				}
				items = append(items, responses.ResponseInputItemParamOfOutputMessage(
					outputContent, messageID, responses.ResponseOutputMessageStatusCompleted,
				))
				outputContent = nil
				outputMessageID = ""
				outputSegment++
				return
			}
			if len(inputContent) == 0 {
				return
			}
			items = append(items, responses.ResponseInputItemParamOfMessage(inputContent, role))
			inputContent = nil
		}
		for _, block := range message.Content {
			switch block.Type {
			case "text":
				if role == responses.EasyInputMessageRoleAssistant {
					outputContent = append(outputContent, responses.ResponseOutputMessageContentUnionParam{
						OfOutputText: &responses.ResponseOutputTextParam{Text: block.Text},
					})
					if outputMessageID == "" {
						outputMessageID = strings.TrimSpace(block.ID)
					}
				} else {
					inputContent = append(inputContent, responses.ResponseInputContentParamOfInputText(block.Text))
				}
			case "image":
				if role != responses.EasyInputMessageRoleUser {
					return nil, fmt.Errorf("Responses input images require user role")
				}
				imageURL, err := responsesImageURL(block.Source)
				if err != nil {
					return nil, err
				}
				image := responses.ResponseInputContentParamOfInputImage(responses.ResponseInputImageDetailAuto)
				image.OfInputImage.ImageURL = param.NewOpt(imageURL)
				inputContent = append(inputContent, image)
			case "tool_use":
				flush()
				arguments := string(block.Input)
				if !json.Valid(block.Input) {
					return nil, fmt.Errorf("tool call %s input: invalid JSON", block.Name)
				}
				items = append(items, responses.ResponseInputItemParamOfFunctionCall(arguments, block.ID, block.Name))
			case "tool_result":
				flush()
				items = append(items, responses.ResponseInputItemParamOfFunctionCallOutput(block.ToolUseID, block.Content))
			case responsesContinuationBlockType:
				flush()
				if block.Continuation == nil {
					continue
				}
				for _, opaque := range block.Continuation.OpaqueItems {
					if opaque.Type != "reasoning" {
						continue
					}
					item := responses.ResponseInputItemParamOfReasoning(opaque.ID, nil)
					item.OfReasoning.EncryptedContent = param.NewOpt(opaque.EncryptedContent)
					items = append(items, item)
				}
			case "thinking", "redacted_thinking":
				// Reasoning is not converted to visible text. Stateless opaque reasoning
				// replay is reserved for the continuation envelope.
			default:
				if block.Text != "" {
					if role == responses.EasyInputMessageRoleAssistant {
						outputContent = append(outputContent, responses.ResponseOutputMessageContentUnionParam{
							OfOutputText: &responses.ResponseOutputTextParam{Text: block.Text},
						})
						if outputMessageID == "" {
							outputMessageID = strings.TrimSpace(block.ID)
						}
					} else {
						inputContent = append(inputContent, responses.ResponseInputContentParamOfInputText(block.Text))
					}
				}
			}
		}
		flush()
	}
	return items, nil
}

func responsesImageURL(source *ContentSource) (string, error) {
	if source == nil {
		return "", errors.New("Responses image source is required")
	}
	if strings.TrimSpace(source.URL) != "" {
		return source.URL, nil
	}
	if source.Data == "" || source.MediaType == "" {
		return "", errors.New("Responses image source requires url or media_type/data")
	}
	return "data:" + source.MediaType + ";base64," + source.Data, nil
}

func applyOpenAIResponsesFormat(params *responses.ResponseNewParams, format *ResponseFormat) error {
	if format == nil || strings.TrimSpace(format.Type) == "" || format.Type == "text" {
		return nil
	}
	switch format.Type {
	case "json_object":
		params.Text.Format = responses.ResponseFormatTextConfigUnionParam{OfJSONObject: &shared.ResponseFormatJSONObjectParam{}}
		return nil
	case "json_schema":
		if format.JSONSchema == nil || len(format.JSONSchema.Schema) == 0 {
			return errors.New("response format json_schema requires schema")
		}
		var schema map[string]any
		if err := json.Unmarshal(format.JSONSchema.Schema, &schema); err != nil {
			return errors.New("response format json_schema: invalid JSON")
		}
		name := strings.TrimSpace(format.JSONSchema.Name)
		if name == "" {
			name = "response"
		}
		params.Text.Format = responses.ResponseFormatTextConfigParamOfJSONSchema(name, schema)
		if params.Text.Format.OfJSONSchema != nil {
			params.Text.Format.OfJSONSchema.Strict = param.NewOpt(format.JSONSchema.Strict)
			if format.JSONSchema.Description != "" {
				params.Text.Format.OfJSONSchema.Description = param.NewOpt(format.JSONSchema.Description)
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported response format type %q", format.Type)
	}
}
