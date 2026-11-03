package telemetry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/konglong87/super-niuma/internal/observability"
)

const (
	SchemaVersionRuntimeTraceV1 = "runtime-trace-v1"

	PropertySchemaVersion = "schema_version"
	PropertySpanID        = "span_id"
	PropertyParentSpanID  = "parent_span_id"
	PropertyTurnIndex     = "turn_index"
	PropertyStartedAt     = "started_at"

	EventQueryRun       = "query.run"
	EventModelRequest   = "model.request"
	EventToolExecution  = "tool.execution"
	EventModelPhase     = "model.phase."
	EventHookExecution  = "hook.execution"
	EventPermissionWait = "permission.wait"
	EventCompact        = "context.compact"
	EventGateEvaluation = "gate.evaluation"
	EventPersistence    = "session.persistence"
	EventRenderer       = "output.renderer"

	SpanPhaseStarted   = "started"
	SpanPhaseFinished  = "finished"
	SpanStartedSuffix  = ".started"
	SpanFinishedSuffix = ".finished"

	MaxSpanIdentityLength = 128

	CategoryAPI        = "api"
	CategoryModel      = "model"
	CategoryTool       = "tool"
	CategoryPermission = "permission"
	CategorySandbox    = "sandbox"
	CategorySession    = "session"
	CategoryMobile     = "mobile"
	CategoryAgent      = "agent"
	CategorySystem     = "system"

	StatusStarted = "started"
	StatusOK      = "ok"
	StatusError   = "error"
	StatusDenied  = "denied"
	StatusBlocked = "blocked"
)

type Event struct {
	ID                                  uint64         `json:"id,omitempty"`
	SchemaVersion                       string         `json:"schema_version,omitempty"`
	Name                                string         `json:"name"`
	Category                            string         `json:"category,omitempty"`
	Source                              string         `json:"source,omitempty"`
	Status                              string         `json:"status,omitempty"`
	TraceID                             string         `json:"trace_id,omitempty"`
	SpanID                              string         `json:"span_id,omitempty"`
	ParentSpanID                        string         `json:"parent_span_id,omitempty"`
	TurnIndex                           int            `json:"turn_index,omitempty"`
	TenantKey                           string         `json:"tenant_key,omitempty"`
	UserKey                             string         `json:"user_key,omitempty"`
	TenantID                            uint64         `json:"tenant_id,omitempty"`
	UserID                              uint64         `json:"user_id,omitempty"`
	SessionID                           uint64         `json:"session_id,omitempty"`
	CLISessionID                        string         `json:"cli_session_id,omitempty"`
	RequestPurpose                      string         `json:"request_purpose,omitempty"`
	ResourceType                        string         `json:"resource_type,omitempty"`
	ResourceID                          string         `json:"resource_id,omitempty"`
	Model                               string         `json:"model,omitempty"`
	ToolName                            string         `json:"tool_name,omitempty"`
	DurationMS                          int64          `json:"duration_ms,omitempty"`
	InputTokens                         int            `json:"input_tokens,omitempty"`
	OutputTokens                        int            `json:"output_tokens,omitempty"`
	CacheCreationInputTokens            int            `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens                int            `json:"cache_read_input_tokens,omitempty"`
	CacheCreationEphemeral1hInputTokens int            `json:"cache_creation_ephemeral_1h_input_tokens,omitempty"`
	CacheCreationEphemeral5mInputTokens int            `json:"cache_creation_ephemeral_5m_input_tokens,omitempty"`
	Error                               string         `json:"error,omitempty"`
	Properties                          map[string]any `json:"properties,omitempty"`
	StartedAt                           time.Time      `json:"started_at,omitempty,omitzero"`
	OccurredAt                          time.Time      `json:"occurred_at,omitempty"`
}

type Sink interface {
	Emit(ctx context.Context, event Event) error
}

type SinkFunc func(ctx context.Context, event Event) error

func (f SinkFunc) Emit(ctx context.Context, event Event) error {
	return f(ctx, event)
}

type Emitter struct {
	mu    sync.RWMutex
	sinks []Sink
	now   func() time.Time
}

func NewEmitter(sinks ...Sink) *Emitter {
	return &Emitter{sinks: compactSinks(sinks), now: time.Now}
}

func (e *Emitter) Emit(ctx context.Context, event Event) {
	if e == nil {
		return
	}
	event = Normalize(ctx, event)
	e.mu.RLock()
	sinks := append([]Sink(nil), e.sinks...)
	e.mu.RUnlock()
	for _, sink := range sinks {
		if sink == nil {
			continue
		}
		if err := sink.Emit(ctx, event); err != nil {
			observability.Error(ctx, nil, "telemetry.emit_error", "telemetry.Emitter.Emit", "telemetry sink failed",
				"event_name", event.Name,
				"sink", fmt.Sprintf("%T", sink),
				"error", err,
			)
		}
	}
}

func (e *Emitter) SetSinks(sinks ...Sink) {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.sinks = compactSinks(sinks)
	e.mu.Unlock()
}

func (e *Emitter) Sinks() []Sink {
	if e == nil {
		return nil
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return append([]Sink(nil), e.sinks...)
}

type contextKey string

const (
	emitterContextKey contextKey = "telemetry_emitter"
	spanContextKey    contextKey = "telemetry_span"
)

type spanContext struct {
	SpanID    string
	TurnIndex int
}

var defaultEmitter = NewEmitter(LoggerSink{})

func DefaultEmitter() *Emitter {
	return defaultEmitter
}

func SetDefaultEmitter(emitter *Emitter) func() {
	if emitter == nil {
		emitter = NewEmitter()
	}
	previous := defaultEmitter
	defaultEmitter = emitter
	return func() {
		defaultEmitter = previous
	}
}

func WithEmitter(ctx context.Context, emitter *Emitter) context.Context {
	if emitter == nil {
		return ctx
	}
	return context.WithValue(ctx, emitterContextKey, emitter)
}

func FromContext(ctx context.Context) *Emitter {
	if ctx != nil {
		if emitter, ok := ctx.Value(emitterContextKey).(*Emitter); ok && emitter != nil {
			return emitter
		}
	}
	return defaultEmitter
}

func Emit(ctx context.Context, event Event) {
	FromContext(ctx).Emit(ctx, event)
}

type Span struct {
	ctx    context.Context
	event  Event
	start  time.Time
	once   *sync.Once
	legacy bool
}

func Start(ctx context.Context, event Event) Span {
	if ctx == nil {
		ctx = context.Background()
	}
	start := time.Now().UTC()
	event.Status = firstNonEmpty(event.Status, StatusStarted)
	event.OccurredAt = start
	Emit(ctx, event)
	return Span{ctx: ctx, event: event, start: start, once: &sync.Once{}, legacy: true}
}

// StartSpan emits the start event and returns a context that parents nested spans.
func StartSpan(ctx context.Context, event Event) (context.Context, Span) {
	if ctx == nil {
		ctx = context.Background()
	}
	start := time.Now().UTC()
	event.SchemaVersion = firstNonEmpty(event.SchemaVersion, SchemaVersionRuntimeTraceV1)
	event.SpanID = firstNonEmpty(event.SpanID, newSpanID())
	event.ParentSpanID = firstNonEmpty(event.ParentSpanID, SpanIDFromContext(ctx))
	if event.TurnIndex <= 0 {
		event.TurnIndex = TurnIndexFromContext(ctx)
	}
	event.Name = spanEventName(event.Name, SpanPhaseStarted)
	event.Status = StatusStarted
	event.StartedAt = start
	event.OccurredAt = start
	Emit(ctx, event)
	childCtx := context.WithValue(ctx, spanContextKey, spanContext{SpanID: event.SpanID, TurnIndex: event.TurnIndex})
	return childCtx, Span{ctx: childCtx, event: event, start: start, once: &sync.Once{}}
}

func (s Span) Finish(status string, err error, attrs map[string]any) {
	if !s.legacy {
		s.FinishEvent(Event{Status: status, Error: errorString(err), Properties: attrs})
		return
	}
	finish := func() {
		event := s.event
		event.Status = firstNonEmpty(status, StatusOK)
		event.DurationMS = time.Since(s.start).Milliseconds()
		event.StartedAt = s.start
		event.OccurredAt = time.Now().UTC()
		if err != nil {
			event.Status = StatusError
			event.Error = err.Error()
		}
		if len(attrs) > 0 {
			event.Properties = attrs
		}
		Emit(s.ctx, event)
	}
	if s.once == nil {
		finish()
		return
	}
	s.once.Do(finish)
}

// FinishEvent completes a native span while allowing finish-time metrics such
// as token usage to stay in their typed top-level Event fields.
func (s Span) FinishEvent(event Event) {
	finish := func() {
		now := time.Now().UTC()
		event.Name = spanEventName(firstNonEmpty(event.Name, s.event.Name), SpanPhaseFinished)
		event.Category = firstNonEmpty(event.Category, s.event.Category)
		event.Source = firstNonEmpty(event.Source, s.event.Source)
		event.Status = firstNonEmpty(event.Status, StatusOK)
		event.TraceID = firstNonEmpty(event.TraceID, s.event.TraceID)
		event.SchemaVersion = s.event.SchemaVersion
		event.SpanID = s.event.SpanID
		event.ParentSpanID = s.event.ParentSpanID
		event.TurnIndex = s.event.TurnIndex
		event.TenantKey = firstNonEmpty(event.TenantKey, s.event.TenantKey)
		event.UserKey = firstNonEmpty(event.UserKey, s.event.UserKey)
		if event.TenantID == 0 {
			event.TenantID = s.event.TenantID
		}
		if event.UserID == 0 {
			event.UserID = s.event.UserID
		}
		if event.SessionID == 0 {
			event.SessionID = s.event.SessionID
		}
		event.CLISessionID = firstNonEmpty(event.CLISessionID, s.event.CLISessionID)
		event.RequestPurpose = firstNonEmpty(event.RequestPurpose, s.event.RequestPurpose)
		event.ResourceType = firstNonEmpty(event.ResourceType, s.event.ResourceType)
		event.ResourceID = firstNonEmpty(event.ResourceID, s.event.ResourceID)
		event.Model = firstNonEmpty(event.Model, s.event.Model)
		event.ToolName = firstNonEmpty(event.ToolName, s.event.ToolName)
		event.StartedAt = s.start
		event.OccurredAt = now
		if event.DurationMS == 0 {
			event.DurationMS = now.Sub(s.start).Milliseconds()
		}
		if event.Error != "" {
			event.Status = StatusError
		}
		event.Properties = mergeProperties(s.event.Properties, event.Properties)
		Emit(s.ctx, event)
	}
	if s.once == nil {
		finish()
		return
	}
	s.once.Do(finish)
}

// EmitCompletedSpan records a completed child interval when instrumentation
// already owns a reliable start timestamp but cannot retain a Span handle.
func EmitCompletedSpan(ctx context.Context, startedAt time.Time, event Event) {
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now().UTC()
	if startedAt.IsZero() {
		startedAt = now
	} else {
		startedAt = startedAt.UTC()
	}
	event.SchemaVersion = SchemaVersionRuntimeTraceV1
	event.SpanID = newSpanID()
	event.ParentSpanID = SpanIDFromContext(ctx)
	if event.TurnIndex <= 0 {
		event.TurnIndex = TurnIndexFromContext(ctx)
	}
	event.Name = spanEventName(event.Name, SpanPhaseFinished)
	event.Status = firstNonEmpty(event.Status, StatusOK)
	event.StartedAt = startedAt
	event.OccurredAt = now
	if event.DurationMS == 0 && !now.Before(startedAt) {
		event.DurationMS = now.Sub(startedAt).Milliseconds()
	}
	Emit(ctx, event)
}

func SpanIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	span, _ := ctx.Value(spanContextKey).(spanContext)
	return strings.TrimSpace(span.SpanID)
}

func TurnIndexFromContext(ctx context.Context) int {
	if ctx == nil {
		return 0
	}
	span, _ := ctx.Value(spanContextKey).(spanContext)
	if span.TurnIndex < 0 {
		return 0
	}
	return span.TurnIndex
}

func WithTurnIndex(ctx context.Context, turnIndex int) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if turnIndex < 0 {
		turnIndex = 0
	}
	return context.WithValue(ctx, spanContextKey, spanContext{SpanID: SpanIDFromContext(ctx), TurnIndex: turnIndex})
}

func Normalize(ctx context.Context, event Event) Event {
	event = RestoreTraceContext(event)
	event.Name = strings.TrimSpace(event.Name)
	if event.Name == "" {
		event.Name = "unknown"
	}
	event.Category = strings.TrimSpace(event.Category)
	event.Source = strings.TrimSpace(event.Source)
	event.Status = strings.TrimSpace(event.Status)
	event.TraceID = firstNonEmpty(event.TraceID, observability.TraceID(ctx))
	event.TenantKey = firstNonEmpty(event.TenantKey, observability.TenantKey(ctx))
	event.UserKey = firstNonEmpty(event.UserKey, observability.UserID(ctx))
	event.CLISessionID = firstNonEmpty(event.CLISessionID, observability.CLISessionID(ctx))
	event.RequestPurpose = firstNonEmpty(event.RequestPurpose, observability.RequestPurpose(ctx))
	event.ResourceType = strings.TrimSpace(event.ResourceType)
	event.ResourceID = strings.TrimSpace(event.ResourceID)
	event.Model = strings.TrimSpace(event.Model)
	event.ToolName = strings.TrimSpace(event.ToolName)
	event.Error = truncate(strings.TrimSpace(event.Error), 1024)
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	} else {
		event.OccurredAt = event.OccurredAt.UTC()
	}
	event.Properties = traceProperties(event, SanitizeProperties(event.Properties))
	return event
}

// RestoreTraceContext hydrates fields persisted inside properties_json without
// applying write-time sanitization to legacy event properties.
func RestoreTraceContext(event Event) Event {
	propertySchema := traceStringProperty(event.Properties, PropertySchemaVersion)
	event.SchemaVersion = firstNonEmpty(event.SchemaVersion, propertySchema)
	if event.SchemaVersion == SchemaVersionRuntimeTraceV1 {
		event = hydrateTraceProperties(event)
	} else {
		event.SpanID = ""
		event.ParentSpanID = ""
		event.TurnIndex = 0
		event.StartedAt = time.Time{}
	}
	event.SchemaVersion = strings.TrimSpace(event.SchemaVersion)
	event.SpanID = strings.TrimSpace(event.SpanID)
	event.ParentSpanID = strings.TrimSpace(event.ParentSpanID)
	if event.TurnIndex < 0 {
		event.TurnIndex = 0
	}
	if !event.StartedAt.IsZero() {
		event.StartedAt = event.StartedAt.UTC()
	}
	return event
}

func ValidateRuntimeTraceEvent(event Event) error {
	propertySchema := traceStringProperty(event.Properties, PropertySchemaVersion)
	schema := firstNonEmpty(event.SchemaVersion, propertySchema)
	hasTraceFields := strings.TrimSpace(event.SpanID) != "" || strings.TrimSpace(event.ParentSpanID) != "" || event.TurnIndex != 0 || !event.StartedAt.IsZero() ||
		traceStringProperty(event.Properties, PropertySpanID) != "" || traceStringProperty(event.Properties, PropertyParentSpanID) != "" ||
		traceIntProperty(event.Properties, PropertyTurnIndex) != 0 || traceStringProperty(event.Properties, PropertyStartedAt) != ""
	if schema == "" {
		if hasTraceFields {
			return fmt.Errorf("schema_version is required for runtime trace fields")
		}
		return nil
	}
	if schema != SchemaVersionRuntimeTraceV1 {
		return fmt.Errorf("unsupported telemetry schema_version %q", schema)
	}
	if event.TurnIndex < 0 || traceIntProperty(event.Properties, PropertyTurnIndex) < 0 {
		return fmt.Errorf("turn_index must be non-negative")
	}
	event.SchemaVersion = schema
	event = RestoreTraceContext(event)
	if event.SpanID == "" {
		return fmt.Errorf("span_id is required for %s", SchemaVersionRuntimeTraceV1)
	}
	if err := validateSpanIdentity(PropertySpanID, event.SpanID); err != nil {
		return err
	}
	if event.ParentSpanID != "" {
		if err := validateSpanIdentity(PropertyParentSpanID, event.ParentSpanID); err != nil {
			return err
		}
		if event.ParentSpanID == event.SpanID {
			return fmt.Errorf("parent_span_id must differ from span_id")
		}
	}
	return nil
}

func validateSpanIdentity(field, value string) error {
	value = strings.TrimSpace(value)
	if len(value) > MaxSpanIdentityLength {
		return fmt.Errorf("%s exceeds %d bytes", field, MaxSpanIdentityLength)
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("._:-", r) {
			continue
		}
		return fmt.Errorf("%s contains unsupported characters", field)
	}
	return nil
}

func newSpanID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err == nil {
		return hex.EncodeToString(value[:])
	}
	return fmt.Sprintf("%016x%016x", uint64(time.Now().UnixNano()), spanIDFallback.Add(1))
}

var spanIDFallback atomic.Uint64

func spanEventName(name, phase string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "unknown"
	}
	name = strings.TrimSuffix(strings.TrimSuffix(name, SpanStartedSuffix), SpanFinishedSuffix)
	return name + "." + phase
}

func mergeProperties(base, extra map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(extra))
	for key, value := range base {
		out[key] = value
	}
	for key, value := range extra {
		out[key] = value
	}
	return out
}

func hydrateTraceProperties(event Event) Event {
	event.SchemaVersion = firstNonEmpty(event.SchemaVersion, traceStringProperty(event.Properties, PropertySchemaVersion))
	event.SpanID = firstNonEmpty(event.SpanID, traceStringProperty(event.Properties, PropertySpanID))
	event.ParentSpanID = firstNonEmpty(event.ParentSpanID, traceStringProperty(event.Properties, PropertyParentSpanID))
	if event.TurnIndex <= 0 {
		event.TurnIndex = traceIntProperty(event.Properties, PropertyTurnIndex)
	}
	if event.StartedAt.IsZero() {
		if raw := traceStringProperty(event.Properties, PropertyStartedAt); raw != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
				event.StartedAt = parsed.UTC()
			}
		}
	}
	return event
}

func traceProperties(event Event, properties map[string]any) map[string]any {
	if event.SchemaVersion == "" && event.SpanID == "" && event.ParentSpanID == "" && event.TurnIndex == 0 && event.StartedAt.IsZero() {
		return properties
	}
	out := properties
	if out == nil {
		out = make(map[string]any, 5)
	}
	if event.SchemaVersion != "" {
		out[PropertySchemaVersion] = event.SchemaVersion
	}
	if event.SpanID != "" {
		out[PropertySpanID] = event.SpanID
	}
	if event.ParentSpanID != "" {
		out[PropertyParentSpanID] = event.ParentSpanID
	}
	if event.TurnIndex > 0 {
		out[PropertyTurnIndex] = event.TurnIndex
	}
	if !event.StartedAt.IsZero() {
		out[PropertyStartedAt] = event.StartedAt.UTC().Format(time.RFC3339Nano)
	}
	return out
}

func traceStringProperty(properties map[string]any, key string) string {
	value, ok := properties[key]
	if !ok || value == nil {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

func traceIntProperty(properties map[string]any, key string) int {
	value, ok := properties[key]
	if !ok || value == nil {
		return 0
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, _ := strconv.Atoi(typed.String())
		return parsed
	case string:
		parsed, _ := strconv.Atoi(strings.TrimSpace(typed))
		return parsed
	default:
		return 0
	}
}

func MarshalProperties(properties map[string]any) string {
	properties = SanitizeProperties(properties)
	if len(properties) == 0 {
		return ""
	}
	data, err := json.Marshal(properties)
	if err != nil {
		safe := make(map[string]any, len(properties))
		for key, value := range properties {
			if _, valueErr := json.Marshal(value); valueErr == nil {
				safe[key] = value
			}
		}
		data, err = json.Marshal(safe)
		if err != nil {
			return ""
		}
	}
	return string(data)
}

func SanitizeProperties(properties map[string]any) map[string]any {
	if len(properties) == 0 {
		return nil
	}
	out := make(map[string]any, len(properties))
	for key, value := range properties {
		cleanKey := strings.TrimSpace(key)
		if cleanKey == "" || sensitiveKey(cleanKey) {
			continue
		}
		out[cleanKey] = sanitizeValue(value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func sensitiveKey(key string) bool {
	lower := strings.ToLower(key)
	// This is a numeric context-size estimate, not an authentication token.
	// Keep the exception exact so arbitrary token-bearing keys remain filtered.
	if lower == "estimated_tokens" {
		return false
	}
	for _, marker := range []string{"api_key", "apikey", "authorization", "bearer", "password", "secret", "token", "jwt", "prompt", "content", "transcript", "private_url"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func sanitizeValue(value any) any {
	switch v := value.(type) {
	case nil:
		return nil
	case string:
		return truncate(v, 512)
	case error:
		return truncate(v.Error(), 512)
	case map[string]any:
		return SanitizeProperties(v)
	case map[string]string:
		out := make(map[string]any, len(v))
		for key, value := range v {
			if !sensitiveKey(key) {
				out[key] = truncate(value, 512)
			}
		}
		return out
	default:
		return v
	}
}

func truncate(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "...(truncated)"
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func compactSinks(sinks []Sink) []Sink {
	out := make([]Sink, 0, len(sinks))
	for _, sink := range sinks {
		if sink != nil {
			out = append(out, sink)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
