package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	_ "github.com/konglong87/super-niuma/docs"
	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/promptmode"
	"github.com/konglong87/super-niuma/internal/query"
	"github.com/konglong87/super-niuma/internal/quota"
	"github.com/konglong87/super-niuma/internal/telemetry"
)

type OpenAIChatRequest struct {
	Model               string                `json:"model"`
	Messages            []OpenAIMessage       `json:"messages"`
	Stream              bool                  `json:"stream,omitempty"`
	Temperature         *float64              `json:"temperature,omitempty"`
	MaxTokens           int                   `json:"max_tokens,omitempty"`
	MaxCompletionTokens int                   `json:"max_completion_tokens,omitempty"`
	TopP                *float64              `json:"top_p,omitempty"`
	User                string                `json:"user,omitempty"`
	ResponseFormat      *OpenAIResponseFormat `json:"response_format,omitempty"`
	Metadata            map[string]any        `json:"metadata,omitempty"`
	ProfileID           string                `json:"profile_id,omitempty"`
	ProfileVersion      uint                  `json:"profile_version,omitempty"`
	ProfileOverrides    map[string]any        `json:"profile_overrides,omitempty"`
}

type StructuredSkillRoute struct {
	SchemaName string `json:"schema_name"`
	SkillKey   string `json:"skill_key"`
}

type StructuredSkillSelection struct {
	SkillKeys []string
	Source    string
}

type structuredSkillRouteSettings struct {
	StructuredSkillRoutes []StructuredSkillRoute `json:"structured_skill_routes"`
}

type OpenAIMessage struct {
	Role         string              `json:"role"`
	Content      json.RawMessage     `json:"content"`
	Name         string              `json:"name,omitempty"`
	ToolCallID   string              `json:"tool_call_id,omitempty"`
	ToolCalls    []OpenAIToolCall    `json:"tool_calls,omitempty"`
	FunctionCall *OpenAIFunctionCall `json:"function_call,omitempty"`
}

type OpenAIToolCall struct {
	ID       string             `json:"id,omitempty"`
	Type     string             `json:"type,omitempty"`
	Function OpenAIFunctionCall `json:"function,omitempty"`
}

type OpenAIFunctionCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type OpenAIResponseFormat struct {
	Type       string                      `json:"type,omitempty"`
	JSONSchema *OpenAIResponseFormatSchema `json:"json_schema,omitempty"`
}

type OpenAIResponseFormatSchema struct {
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Schema      json.RawMessage `json:"schema,omitempty"`
	Strict      bool            `json:"strict,omitempty"`
}

type openAIStreamChunk struct {
	ID      string               `json:"id"`
	Object  string               `json:"object"`
	Created int64                `json:"created"`
	Model   string               `json:"model"`
	Choices []openAIStreamChoice `json:"choices"`
}

type openAITenantRuntimeMetadata = query.TenantRuntimeManifest

type openAIStreamChoice struct {
	Index        int               `json:"index"`
	Delta        openAIStreamDelta `json:"delta"`
	FinishReason *string           `json:"finish_reason"`
}

type openAIStreamDelta struct {
	Role      string                 `json:"role,omitempty"`
	Content   string                 `json:"content,omitempty"`
	ToolCalls []openAIStreamToolCall `json:"tool_calls,omitempty"`
}

type openAIStreamToolCall struct {
	Index    int                  `json:"index"`
	ID       string               `json:"id,omitempty"`
	Type     string               `json:"type,omitempty"`
	Function openAIStreamFunction `json:"function,omitempty"`
}

type openAIStreamFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type openAIChatCompletionResponse struct {
	ID            string                       `json:"id"`
	Object        string                       `json:"object"`
	Created       int64                        `json:"created"`
	Model         string                       `json:"model"`
	Choices       []openAIChatChoice           `json:"choices"`
	Usage         openAIChatUsage              `json:"usage,omitempty"`
	TenantRuntime *openAITenantRuntimeMetadata `json:"tenant_runtime,omitempty"`
}

type openAIChatChoice struct {
	Index        int               `json:"index"`
	Message      openAIChatMessage `json:"message"`
	FinishReason string            `json:"finish_reason"`
}

type openAIChatMessage struct {
	Role      string           `json:"role"`
	Content   *string          `json:"content"`
	ToolCalls []OpenAIToolCall `json:"tool_calls,omitempty"`
}

type openAIChatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type openAIModelsResponse struct {
	Object string        `json:"object"`
	Data   []openAIModel `json:"data"`
}

type openAIModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

func openAIChatHandler(opts Options, queryFn QueryFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "openai_chat_completions", "server.openAIChatHandler", "handle chat completion")
		if !authorize(w, r, opts.AuthToken) {
			return
		}
		if r.Method != http.MethodPost {
			writeOpenAIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req OpenAIChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		if len(req.Messages) == 0 {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "messages is required")
			return
		}
		systemPrompt, prompt := openAIPrompt(req.Messages)
		if strings.TrimSpace(prompt) == "" {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "message content is required")
			return
		}
		if req.Stream && opts.StreamQueryFunc != nil {
			streamOpenAIQuery(w, r, req, systemPrompt, prompt, opts)
			return
		}
		if queryFn == nil {
			writeOpenAIError(w, http.StatusInternalServerError, "server_error", errMsgQueryHandlerNotConfig)
			return
		}
		selection := selectStructuredTenantSkills(r, req, opts)
		queryReq := openAIQueryRequest(req, opts.Workspace, systemPrompt, prompt, selection)
		queryReq.Attachments = openAIQueryAttachments(req.Messages)
		queryReq.Model = multimodalModelForAttachments(config.LoadForCWD(opts.Workspace).Settings, queryReq.Model, queryReq.Attachments)
		queryReq.SessionKey = requestSessionKey(r)
		reservation, reserved := reserveQueryQuota(r.Context(), opts, quota.SourceOpenAI, "/v1/chat/completions", queryReq)
		if reserved.err != nil {
			writeOpenAIQuotaError(w, reserved.err)
			return
		}
		settler := newQuotaSettler(r.Context(), opts, reservation)
		defer settler.settleOnPanic()
		result, err := runOpenAIQueryWithStructuredRetry(r.Context(), queryFn, queryReq)
		settler.settle(result, err)
		if err != nil {
			writeOpenAIErrorWithTenantRuntime(w, http.StatusInternalServerError, "server_error", err.Error(), result.TenantRuntime)
			return
		}
		if req.Stream {
			writeOpenAIStream(w, req, result)
			persistTenantQuery(r.Context(), opts.TenantService, opts.SessionTitleFunc, queryReq, result)
			return
		}
		model := result.Model
		if model == "" {
			model = req.Model
		}
		persistTenantQuery(r.Context(), opts.TenantService, opts.SessionTitleFunc, queryReq, result)
		writeOpenAITenantRuntimeHeaders(w, result.TenantRuntime)
		writeJSON(w, openAIChatCompletionResponse{
			ID:      "chatcmpl-" + strconv.FormatInt(time.Now().UnixNano(), 36),
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   model,
			Choices: []openAIChatChoice{{
				Index:        0,
				Message:      openAIChatMessage{Role: "assistant", Content: openAIContent(result.Response), ToolCalls: openAIToolCalls(result.ToolCalls)},
				FinishReason: openAIResultFinishReason(result),
			}},
			Usage: openAIChatUsage{
				PromptTokens:     result.Usage.InputTokens,
				CompletionTokens: result.Usage.OutputTokens,
				TotalTokens:      result.Usage.InputTokens + result.Usage.OutputTokens,
			},
			TenantRuntime: openAITenantRuntimeMetadataForResponse(result.TenantRuntime),
		})
	}
}

func openAIContent(content string) *string {
	if content == "" {
		return nil
	}
	return &content
}

func openAIToolCalls(traces []query.ToolTrace) []OpenAIToolCall {
	if len(traces) == 0 {
		return nil
	}
	calls := make([]OpenAIToolCall, 0, len(traces))
	for _, trace := range traces {
		id := trace.ID
		if id == "" {
			id = "call_" + trace.Name
		}
		calls = append(calls, OpenAIToolCall{
			ID:   id,
			Type: "function",
			Function: OpenAIFunctionCall{
				Name:      trace.Name,
				Arguments: trace.Input,
			},
		})
	}
	return calls
}

func openAIResultFinishReason(result query.Result) string {
	if len(result.ToolCalls) > 0 && strings.TrimSpace(result.Response) == "" {
		return "tool_calls"
	}
	return openAIFinishReason(result.StopReason)
}

func openAIModelsHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "openai_models", "server.openAIModelsHandler", "list models")
		if !authorize(w, r, opts.AuthToken) {
			return
		}
		if r.Method != http.MethodGet {
			writeOpenAIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		models := opts.Models
		if len(models) == 0 {
			models = config.ConfiguredModels(opts.Workspace)
		}
		if len(models) == 0 {
			models = config.KnownModels
		}
		data := make([]openAIModel, 0, len(models))
		for _, model := range models {
			model = strings.TrimSpace(model)
			if model == "" {
				continue
			}
			data = append(data, openAIModel{
				ID:      model,
				Object:  "model",
				Created: 0,
				OwnedBy: "niuma",
			})
		}
		writeJSON(w, openAIModelsResponse{Object: "list", Data: data})
	}
}

func agentProvidersHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "agent_providers", "server.agentProvidersHandler", "list providers")
		if !authorize(w, r, opts.AuthToken) {
			return
		}
		if r.Method != http.MethodGet {
			writeOpenAIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		data := make([]config.ProviderOption, 0, len(opts.Providers))
		for _, provider := range opts.Providers {
			name := strings.TrimSpace(provider.Name)
			if name == "" {
				continue
			}
			data = append(data, config.ProviderOption{Name: name, Model: strings.TrimSpace(provider.Model)})
		}
		writeJSON(w, map[string]any{"object": "list", "data": data})
	}
}

func openAIQueryRequest(req OpenAIChatRequest, cwd, systemPrompt, prompt string, selection StructuredSkillSelection) QueryRequest {
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = req.MaxCompletionTokens
	}
	systemPrompt = appendOpenAIPrompt(systemPrompt, openAIResponseFormatPrompt(req.ResponseFormat))
	structured := openAIStructuredOutput(req.ResponseFormat)
	return QueryRequest{
		Prompt:                  prompt,
		Model:                   req.Model,
		CWD:                     cwd,
		SystemPrompt:            systemPrompt,
		PromptMode:              promptmode.Chat.String(),
		ProfileID:               req.ProfileID,
		ProfileVersion:          req.ProfileVersion,
		ProfileOverrides:        req.ProfileOverrides,
		MaxTokens:               maxTokens,
		MaxTurns:                openAIMaxTurns(structured),
		DisableTools:            structured,
		SkipAutoTitle:           structured,
		InlineTenantSkills:      append([]string(nil), selection.SkillKeys...),
		InlineTenantSkillSource: selection.Source,
		ResponseFormat:          req.ResponseFormat,
	}
}

func openAIMaxTurns(structured bool) int {
	if structured {
		return 1
	}
	return 0
}

func runOpenAIQueryWithStructuredRetry(ctx context.Context, queryFn QueryFunc, req QueryRequest) (query.Result, error) {
	result, err := queryFn(ctx, req)
	if err == nil || !shouldRetryStructuredProviderError(req, err) {
		return result, err
	}
	emitStructuredRetryTelemetry(ctx, req, err)
	retryReq := req
	retryReq.StructuredRetryAttempt++
	retryResult, retryErr := queryFn(ctx, retryReq)
	if retryResult.TenantRuntime == nil {
		retryResult.TenantRuntime = result.TenantRuntime
	}
	return retryResult, retryErr
}

func runOpenAIStreamQueryWithStructuredRetry(ctx context.Context, queryFn StreamQueryFunc, req QueryRequest, sink io.Writer) (query.Result, error) {
	result, err := queryFn(ctx, req, sink)
	if err == nil || !shouldRetryStructuredProviderError(req, err) {
		return result, err
	}
	if streamHasPartialOutput(sink, result) {
		return result, err
	}
	emitStructuredRetryTelemetry(ctx, req, err)
	retryReq := req
	retryReq.StructuredRetryAttempt++
	retryResult, retryErr := queryFn(ctx, retryReq, sink)
	if retryResult.TenantRuntime == nil {
		retryResult.TenantRuntime = result.TenantRuntime
	}
	return retryResult, retryErr
}

func streamHasPartialOutput(sink io.Writer, result query.Result) bool {
	if strings.TrimSpace(result.Response) != "" {
		return true
	}
	writer, ok := sink.(openAIStreamWriter)
	return ok && writer.text != nil && writer.text.Len() > 0
}

func shouldRetryStructuredProviderError(req QueryRequest, err error) bool {
	if err == nil || req.StructuredRetryAttempt > 0 || !openAIStructuredOutput(req.ResponseFormat) {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "JSON response_format generation abnormal") ||
		strings.Contains(msg, "InternalError.Algo.InvalidParameter")
}

func emitStructuredRetryTelemetry(ctx context.Context, req QueryRequest, err error) {
	telemetry.Emit(ctx, telemetry.Event{
		Name:      "openai.structured_retry",
		Category:  telemetry.CategoryAPI,
		Source:    "server.openAIChatHandler",
		Status:    telemetry.StatusStarted,
		Model:     req.Model,
		SessionID: req.TenantSessionID,
		Error:     err.Error(),
		Properties: map[string]any{
			"reason":                 "provider_structured_json_error",
			"response_format":        "json_schema",
			"attempt":                req.StructuredRetryAttempt + 1,
			"tenant_skill_keys":      strings.Join(req.InlineTenantSkills, ","),
			"tenant_skill_selector":  req.InlineTenantSkillSource,
			"disable_tools":          req.DisableTools,
			"max_turns":              req.MaxTurns,
			"structured_retry_limit": 1,
		},
	})
}

func openAIStructuredOutput(format *OpenAIResponseFormat) bool {
	if format == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(format.Type)) {
	case "json_schema":
		return true
	default:
		return false
	}
}

func selectStructuredTenantSkills(r *http.Request, req OpenAIChatRequest, opts Options) StructuredSkillSelection {
	if !openAIStructuredOutput(req.ResponseFormat) {
		return StructuredSkillSelection{}
	}
	if keys := normalizeStructuredSkillKeys(splitStructuredSkillKeys(r.Header.Get("X-Tenant-Skill-Key"))); len(keys) > 0 {
		return StructuredSkillSelection{SkillKeys: keys, Source: "header"}
	}
	if keys := normalizeStructuredSkillKeys(openAIMetadataTenantSkillKeys(req.Metadata)); len(keys) > 0 {
		return StructuredSkillSelection{SkillKeys: keys, Source: "metadata"}
	}
	if keys := structuredSkillKeysForTenantSettings(r.Context(), req, opts.TenantService); len(keys) > 0 {
		return StructuredSkillSelection{SkillKeys: keys, Source: "tenant_settings"}
	}
	if keys := structuredSkillKeysForRoutes(req, opts.StructuredSkillRoutes); len(keys) > 0 {
		return StructuredSkillSelection{SkillKeys: keys, Source: "env"}
	}
	if skill := openAICompatibilitySkillName(req); skill != "" {
		return StructuredSkillSelection{SkillKeys: []string{skill}, Source: "builtin_compat"}
	}
	return StructuredSkillSelection{}
}

func structuredSkillKeysForTenantSettings(ctx context.Context, req OpenAIChatRequest, service TenantService) []string {
	if service == nil {
		return nil
	}
	resolved, err := service.ResolveContext(ctx)
	if err != nil {
		observability.Debug(ctx, nil, "tenant.structured_skill_routes.resolve_skipped", "server.structuredSkillKeysForTenantSettings", "skip tenant structured skill routes", "error", err)
		return nil
	}
	var settings structuredSkillRouteSettings
	if err := json.Unmarshal([]byte(strings.TrimSpace(resolved.Tenant.SettingsJSON)), &settings); err != nil {
		if strings.TrimSpace(resolved.Tenant.SettingsJSON) != "" {
			observability.Debug(ctx, nil, "tenant.structured_skill_routes.parse_skipped", "server.structuredSkillKeysForTenantSettings", "skip invalid tenant structured skill routes", "error", err)
		}
		return nil
	}
	return structuredSkillKeysForRoutes(req, settings.StructuredSkillRoutes)
}

func structuredSkillKeysForRoutes(req OpenAIChatRequest, routes []StructuredSkillRoute) []string {
	schemaName := openAIResponseSchemaName(req.ResponseFormat)
	if schemaName == "" {
		return nil
	}
	var keys []string
	for _, route := range routes {
		if strings.EqualFold(strings.TrimSpace(route.SchemaName), schemaName) {
			keys = append(keys, route.SkillKey)
		}
	}
	return normalizeStructuredSkillKeys(keys)
}

func openAIResponseSchemaName(format *OpenAIResponseFormat) string {
	if format == nil || format.JSONSchema == nil {
		return ""
	}
	return strings.TrimSpace(format.JSONSchema.Name)
}

func openAIMetadataTenantSkillKeys(metadata map[string]any) []string {
	if len(metadata) == 0 {
		return nil
	}
	var keys []string
	appendValue := func(value any) {
		switch typed := value.(type) {
		case string:
			keys = append(keys, splitStructuredSkillKeys(typed)...)
		case []any:
			for _, item := range typed {
				if text, ok := item.(string); ok {
					keys = append(keys, splitStructuredSkillKeys(text)...)
				}
			}
		case []string:
			for _, item := range typed {
				keys = append(keys, splitStructuredSkillKeys(item)...)
			}
		}
	}
	appendValue(metadata["tenant_skill_key"])
	appendValue(metadata["tenant_skill_keys"])
	return keys
}

func splitStructuredSkillKeys(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if key := strings.TrimSpace(part); key != "" {
			out = append(out, key)
		}
	}
	return out
}

func normalizeStructuredSkillKeys(keys []string) []string {
	out := make([]string, 0, len(keys))
	seen := map[string]bool{}
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		lower := strings.ToLower(key)
		if seen[lower] {
			continue
		}
		seen[lower] = true
		out = append(out, key)
	}
	return out
}

func openAICompatibilitySkillName(req OpenAIChatRequest) string {
	name := ""
	if req.ResponseFormat != nil && req.ResponseFormat.JSONSchema != nil {
		name = strings.TrimSpace(req.ResponseFormat.JSONSchema.Name)
	}
	text := strings.ToLower(name + "\n" + openAIMessagesText(req.Messages))
	switch {
	case strings.Contains(text, "teach_decision") || strings.Contains(text, "teach_context") || strings.Contains(text, `"skill_key":"teach"`) || strings.Contains(text, `"skill_key": "teach"`):
		return "teach"
	default:
		return ""
	}
}

func openAIMessagesText(messages []OpenAIMessage) string {
	var parts []string
	for _, message := range messages {
		if text := strings.TrimSpace(openAIMessageText(message)); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func openAIResponseFormatPrompt(format *OpenAIResponseFormat) string {
	if format == nil {
		return ""
	}
	switch strings.TrimSpace(format.Type) {
	case "json_object":
		return "Respond with a single valid JSON object. Do not include markdown fences or explanatory text."
	case "json_schema":
		if format.JSONSchema == nil {
			return "Respond with a single valid JSON value matching the requested JSON Schema. Do not include markdown fences or explanatory text."
		}
		var b strings.Builder
		b.WriteString("Respond with a single valid JSON value matching this JSON Schema. Do not include markdown fences or explanatory text.")
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
		if schema := prettyRawJSON(format.JSONSchema.Schema); schema != "" {
			b.WriteString("\n\nJSON Schema:\n")
			b.WriteString(schema)
		}
		return b.String()
	default:
		return ""
	}
}

func prettyRawJSON(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var out bytes.Buffer
	if err := json.Indent(&out, raw, "", "  "); err != nil {
		return string(raw)
	}
	return out.String()
}

func appendOpenAIPrompt(base, extra string) string {
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

func streamOpenAIQuery(w http.ResponseWriter, r *http.Request, req OpenAIChatRequest, systemPrompt, prompt string, opts Options) {
	model := req.Model
	attachments := openAIQueryAttachments(req.Messages)
	model = multimodalModelForAttachments(config.LoadForCWD(opts.Workspace).Settings, model, attachments)
	id := "chatcmpl-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	created := time.Now().Unix()
	w.Header().Set("content-type", "text/event-stream")
	w.Header().Set("cache-control", "no-cache")
	w.Header().Set("connection", "keep-alive")
	writeSSEChunk(w, openAIStreamChunk{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []openAIStreamChoice{{
			Index: 0,
			Delta: openAIStreamDelta{Role: "assistant"},
		}},
	})
	var streamed strings.Builder
	writer := openAIStreamWriter{w: w, id: id, created: created, model: model, text: &streamed}
	selection := selectStructuredTenantSkills(r, req, opts)
	queryReq := openAIQueryRequest(req, opts.Workspace, systemPrompt, prompt, selection)
	queryReq.Model = model
	queryReq.Attachments = attachments
	queryReq.SessionKey = requestSessionKey(r)
	reservation, reserved := reserveQueryQuota(r.Context(), opts, quota.SourceOpenAI, "/v1/chat/completions", queryReq)
	if reserved.err != nil {
		writeSSEError(w, id, created, model, reserved.err.Error())
		fmt.Fprint(w, "data: [DONE]\n\n")
		return
	}
	settler := newQuotaSettler(r.Context(), opts, reservation)
	defer settler.settleOnPanic()
	result, err := runOpenAIStreamQueryWithStructuredRetry(r.Context(), opts.StreamQueryFunc, queryReq, writer)
	settler.settle(result, err)
	if result.Model != "" {
		model = result.Model
		writer.model = result.Model
	}
	if err != nil {
		if metadata := openAITenantRuntimeMetadataForResponse(result.TenantRuntime); metadata != nil {
			writeSSEData(w, map[string]any{
				"id":             id,
				"object":         "tenant.runtime",
				"created":        created,
				"model":          model,
				"tenant_runtime": metadata,
			})
		}
		writeSSEError(w, id, created, model, err.Error())
		fmt.Fprint(w, "data: [DONE]\n\n")
		return
	}
	if result.Response == "" {
		result.Response = streamed.String()
	}
	for i, call := range openAIToolCalls(result.ToolCalls) {
		writeSSEChunk(w, openAIStreamChunk{
			ID:      id,
			Object:  "chat.completion.chunk",
			Created: created,
			Model:   model,
			Choices: []openAIStreamChoice{{
				Index: 0,
				Delta: openAIStreamDelta{ToolCalls: []openAIStreamToolCall{{
					Index: i,
					ID:    call.ID,
					Type:  call.Type,
					Function: openAIStreamFunction{
						Name:      call.Function.Name,
						Arguments: call.Function.Arguments,
					},
				}}},
			}},
		})
	}
	finish := openAIResultFinishReason(result)
	if metadata := openAITenantRuntimeMetadataForResponse(result.TenantRuntime); metadata != nil {
		writeSSEData(w, map[string]any{
			"id":             id,
			"object":         "tenant.runtime",
			"created":        created,
			"model":          model,
			"tenant_runtime": metadata,
		})
	}
	writeSSEChunk(w, openAIStreamChunk{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []openAIStreamChoice{{
			Index:        0,
			Delta:        openAIStreamDelta{},
			FinishReason: &finish,
		}},
	})
	fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	persistTenantQuery(r.Context(), opts.TenantService, opts.SessionTitleFunc, queryReq, result)
}

type openAIStreamWriter struct {
	w       http.ResponseWriter
	id      string
	created int64
	model   string
	text    *strings.Builder
}

func (w openAIStreamWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if w.text != nil {
		_, _ = w.text.Write(p)
	}
	writeSSEChunk(w.w, openAIStreamChunk{
		ID:      w.id,
		Object:  "chat.completion.chunk",
		Created: w.created,
		Model:   w.model,
		Choices: []openAIStreamChoice{{
			Index: 0,
			Delta: openAIStreamDelta{Content: string(p)},
		}},
	})
	return len(p), nil
}

func writeOpenAIStream(w http.ResponseWriter, req OpenAIChatRequest, result query.Result) {
	model := result.Model
	if model == "" {
		model = req.Model
	}
	id := "chatcmpl-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	created := time.Now().Unix()
	w.Header().Set("content-type", "text/event-stream")
	w.Header().Set("cache-control", "no-cache")
	w.Header().Set("connection", "keep-alive")
	writeOpenAITenantRuntimeHeaders(w, result.TenantRuntime)
	writeSSEChunk(w, openAIStreamChunk{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []openAIStreamChoice{{
			Index: 0,
			Delta: openAIStreamDelta{Role: "assistant"},
		}},
	})
	for _, r := range result.Response {
		writeSSEChunk(w, openAIStreamChunk{
			ID:      id,
			Object:  "chat.completion.chunk",
			Created: created,
			Model:   model,
			Choices: []openAIStreamChoice{{
				Index: 0,
				Delta: openAIStreamDelta{Content: string(r)},
			}},
		})
	}
	for i, call := range openAIToolCalls(result.ToolCalls) {
		writeSSEChunk(w, openAIStreamChunk{
			ID:      id,
			Object:  "chat.completion.chunk",
			Created: created,
			Model:   model,
			Choices: []openAIStreamChoice{{
				Index: 0,
				Delta: openAIStreamDelta{ToolCalls: []openAIStreamToolCall{{
					Index: i,
					ID:    call.ID,
					Type:  call.Type,
					Function: openAIStreamFunction{
						Name:      call.Function.Name,
						Arguments: call.Function.Arguments,
					},
				}}},
			}},
		})
	}
	finish := openAIResultFinishReason(result)
	if metadata := openAITenantRuntimeMetadataForResponse(result.TenantRuntime); metadata != nil {
		writeSSEData(w, map[string]any{
			"id":             id,
			"object":         "tenant.runtime",
			"created":        created,
			"model":          model,
			"tenant_runtime": metadata,
		})
	}
	writeSSEChunk(w, openAIStreamChunk{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []openAIStreamChoice{{
			Index:        0,
			Delta:        openAIStreamDelta{},
			FinishReason: &finish,
		}},
	})
	fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func openAITenantRuntimeMetadataForResponse(runtime *query.TenantRuntimeManifest) *openAITenantRuntimeMetadata {
	if runtime == nil || !runtime.HasMetadata() {
		return nil
	}
	out := openAITenantRuntimeMetadata(*runtime)
	return &out
}

func writeOpenAITenantRuntimeHeaders(w http.ResponseWriter, runtime *query.TenantRuntimeManifest) {
	if runtime == nil || !runtime.HasMetadata() {
		return
	}
	headers := map[string][]string{
		headerTenantSkillKeys:           runtime.LoadedKeys,
		"X-Tenant-Skill-Versions":       runtime.Versions,
		"X-Tenant-Skill-Package-SHA256": runtime.PackageSHA256,
		"X-Tenant-Skill-Package-Refs":   runtime.PackageRefs,
		"X-Tenant-Skill-Runtime-Refs":   runtime.RuntimeRefs,
	}
	if len(headers[headerTenantSkillKeys]) == 0 {
		headers[headerTenantSkillKeys] = runtime.SkillKeys
	}
	for key, values := range headers {
		if value := strings.Join(nonEmptyStrings(values), ","); value != "" {
			w.Header().Set(key, value)
		}
	}
	if runtime.Source != "" {
		w.Header().Set("X-Tenant-Skill-Selector", runtime.Source)
	}
	w.Header().Set("X-Tenant-Runtime-Active", strconv.FormatBool(runtime.Active))
	w.Header().Set("X-Tenant-Runtime-Resolved", strconv.FormatBool(runtime.Resolved))
}

func nonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func openAIPrompt(messages []OpenAIMessage) (string, string) {
	var systemParts []string
	var promptParts []string
	for _, message := range messages {
		content := strings.TrimSpace(openAIMessageText(message))
		if content == "" {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(message.Role))
		switch role {
		case "system", "developer":
			systemParts = append(systemParts, content)
		default:
			promptParts = append(promptParts, openAIMessagePromptPart(message, content))
		}
	}
	if len(promptParts) == 1 {
		prefix, content, ok := strings.Cut(promptParts[0], ": ")
		if ok && (prefix == "USER" || prefix == "MESSAGE") {
			return strings.Join(systemParts, "\n\n"), content
		}
	}
	return strings.Join(systemParts, "\n\n"), strings.Join(promptParts, "\n\n")
}

func openAIMessageText(message OpenAIMessage) string {
	var parts []string
	if content := strings.TrimSpace(messageContentText(message.Content)); content != "" {
		parts = append(parts, content)
	}
	if calls := openAIToolCallsText(message.ToolCalls); calls != "" {
		parts = append(parts, calls)
	}
	if message.FunctionCall != nil {
		if call := openAIFunctionCallText("FUNCTION_CALL", "", *message.FunctionCall); call != "" {
			parts = append(parts, call)
		}
	}
	return strings.Join(parts, "\n")
}

func openAIMessagePromptPart(message OpenAIMessage, content string) string {
	role := strings.ToUpper(strings.TrimSpace(message.Role))
	if role == "" {
		role = "MESSAGE"
	}
	name := strings.TrimSpace(message.Name)
	toolCallID := strings.TrimSpace(message.ToolCallID)
	if role == "TOOL" {
		if toolCallID != "" {
			role += " " + toolCallID
		}
		if name != "" {
			role += " " + name
		}
		return role + ": " + content
	}
	if name != "" {
		role += " " + name
	}
	return role + ": " + content
}

func messageContentText(raw json.RawMessage) string {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var parts []openAIContentPart
	if err := json.Unmarshal(raw, &parts); err == nil {
		var out []string
		for _, part := range parts {
			if text := part.TextContent(); text != "" {
				out = append(out, text)
			}
		}
		return strings.Join(out, "\n")
	}
	return string(raw)
}

func openAIToolCallsText(calls []OpenAIToolCall) string {
	var out []string
	for _, call := range calls {
		if text := openAIFunctionCallText("TOOL_CALL", call.ID, call.Function); text != "" {
			out = append(out, text)
		}
	}
	return strings.Join(out, "\n")
}

func openAIFunctionCallText(label, id string, call OpenAIFunctionCall) string {
	name := strings.TrimSpace(call.Name)
	args := strings.TrimSpace(call.Arguments)
	id = strings.TrimSpace(id)
	if name == "" && args == "" && id == "" {
		return ""
	}
	var head strings.Builder
	head.WriteString(label)
	if id != "" {
		head.WriteString(" ")
		head.WriteString(id)
	}
	if name != "" {
		head.WriteString(" ")
		head.WriteString(name)
	}
	if args != "" {
		head.WriteString(": ")
		head.WriteString(args)
	}
	return head.String()
}

type openAIContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	ImageURL any    `json:"image_url"`
}

func (p openAIContentPart) TextContent() string {
	switch p.Type {
	case "", "text", "input_text":
		return p.Text
	case "image_url":
		if url := imageURLString(p.ImageURL); url != "" {
			return "[image: " + url + "]"
		}
	}
	return ""
}

func openAIQueryAttachments(messages []OpenAIMessage) []QueryAttachment {
	var attachments []QueryAttachment
	for _, message := range messages {
		for _, part := range openAIContentParts(message.Content) {
			if strings.TrimSpace(part.Type) != "image_url" {
				continue
			}
			url := imageURLString(part.ImageURL)
			if url == "" {
				continue
			}
			attachments = append(attachments, QueryAttachment{
				Type:      "image",
				MediaType: imageMediaTypeFromURL(url),
				URL:       url,
			})
		}
	}
	return attachments
}

func openAIContentParts(raw json.RawMessage) []openAIContentPart {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return nil
	}
	var parts []openAIContentPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil
	}
	return parts
}

func imageMediaTypeFromURL(url string) string {
	lower := strings.ToLower(strings.TrimSpace(url))
	switch {
	case strings.HasPrefix(lower, "data:image/png"):
		return "image/png"
	case strings.HasPrefix(lower, "data:image/jpeg"), strings.HasPrefix(lower, "data:image/jpg"):
		return "image/jpeg"
	case strings.HasPrefix(lower, "data:image/webp"):
		return "image/webp"
	case strings.Contains(lower, ".png"):
		return "image/png"
	case strings.Contains(lower, ".jpg"), strings.Contains(lower, ".jpeg"):
		return "image/jpeg"
	case strings.Contains(lower, ".webp"):
		return "image/webp"
	default:
		return ""
	}
}

func imageURLString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case map[string]any:
		if url, ok := v["url"].(string); ok {
			return url
		}
	}
	return ""
}

func openAIFinishReason(stopReason string) string {
	switch stopReason {
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	default:
		return "stop"
	}
}

func writeOpenAIError(w http.ResponseWriter, status int, typ, message string) {
	writeOpenAIErrorWithTenantRuntime(w, status, typ, message, nil)
}

func writeOpenAIErrorWithTenantRuntime(w http.ResponseWriter, status int, typ, message string, runtime *query.TenantRuntimeManifest) {
	writeOpenAITenantRuntimeHeaders(w, runtime)
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	body := map[string]any{
		"error": map[string]any{
			"type":    typ,
			"message": message,
		},
	}
	if metadata := openAITenantRuntimeMetadataForResponse(runtime); metadata != nil {
		body["tenant_runtime"] = metadata
	}
	writeJSON(w, body)
}
