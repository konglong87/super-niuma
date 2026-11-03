// Package sessioncontrol exposes the bounded WebUI v2 Session Control tool
// surface. It intentionally contains no persistence, authorization policy, or
// runtime construction: those capabilities remain owned by the injected service.
package sessioncontrol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	control "github.com/konglong87/super-niuma/internal/sessioncontrol"
	"github.com/konglong87/super-niuma/internal/tools"
)

// Service is the narrow transport port shared by the seven adapters.
type Service interface {
	Create(context.Context, control.CreateRequest) (control.OperationResult, error)
	List(context.Context, control.ListRequest) ([]control.SessionSnapshot, error)
	Get(context.Context, control.GetRequest) (control.SessionSnapshot, error)
	Send(context.Context, control.SendRequest) (control.OperationResult, error)
	Stop(context.Context, control.StopRequest) (control.OperationResult, error)
	Attach(context.Context, control.AttachRequest) (control.OperationResult, error)
	Monitor(context.Context, control.MonitorRequest) (control.OperationResult, error)
}

// New returns the whole v2-only surface. Registration is deliberately owned by
// CLI/server profile wiring; constructing these tools alone never opts a profile in.
func New(service Service) []tools.Tool {
	return []tools.Tool{
		createTool{toolBase: toolBase{service: service}}, listTool{toolBase: toolBase{service: service}}, getTool{toolBase: toolBase{service: service}},
		sendTool{toolBase: toolBase{service: service}}, stopTool{toolBase: toolBase{service: service}}, attachTool{toolBase: toolBase{service: service}}, monitorTool{toolBase: toolBase{service: service}},
	}
}

type toolBase struct{ service Service }

func (toolBase) MaxResultSizeChars() int { return tools.ClaudeCodeDefaultDeclaredMaxResultSizeChars }

func (b toolBase) requestContext(tc tools.Context) (control.RequestContext, error) {
	if b.service == nil {
		return control.RequestContext{}, fmt.Errorf("session control service is unavailable")
	}
	if tc.TenantID == 0 || tc.UserID == 0 {
		return control.RequestContext{}, fmt.Errorf("trusted tenant runtime context is required")
	}
	return control.RequestContext{TenantID: tc.TenantID, UserID: tc.UserID, ActorUserID: tc.UserID, TraceID: tc.TraceID}, nil
}

func decode(input json.RawMessage, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return fmt.Errorf("unexpected trailing JSON")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func tenantMutationRef(raw string) (control.SessionRef, error) {
	ref, err := control.ParseRef(raw)
	if err != nil {
		return control.SessionRef{}, err
	}
	if ref.Source != control.SourceTenant {
		return control.SessionRef{}, fmt.Errorf("managed session mutations require a tenant: ref")
	}
	return ref, nil
}

func parseRefs(raw []string) ([]control.SessionRef, error) {
	refs := make([]control.SessionRef, 0, len(raw))
	for _, value := range raw {
		ref, err := control.ParseRef(value)
		if err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

func result(value any, err error) tools.Result {
	if err != nil {
		code := string(control.CodeInvalidState)
		var serviceErr *control.ServiceError
		var refErr *control.RefError
		if errors.As(err, &refErr) {
			code = string(refErr.Code)
		}
		if errors.As(err, &serviceErr) {
			code = string(serviceErr.Code)
		}
		payload, _ := json.Marshal(map[string]string{"error_code": code})
		return tools.Result{Content: string(payload), IsError: true}
	}
	payload, marshalErr := json.Marshal(value)
	if marshalErr != nil {
		return tools.Result{Content: `{"error_code":"internal_error"}`, IsError: true}
	}
	return tools.Result{Content: string(payload)}
}

type createTool struct{ toolBase }

func (createTool) Name() string { return "SessionCreate" }
func (createTool) Description() string {
	return "Create a tenant-managed session and return its bounded status snapshot."
}
func (createTool) InputSchema() json.RawMessage {
	return schema(`{"session_key":{"type":"string"},"title":{"type":"string"},"model":{"type":"string"},"initial_text":{"type":"string"},"idempotency_key":{"type":"string"}}`, `"idempotency_key"`)
}
func (t createTool) Run(ctx context.Context, input json.RawMessage, tc tools.Context) tools.Result {
	var params struct {
		SessionKey     string `json:"session_key"`
		Title          string `json:"title"`
		Model          string `json:"model"`
		InitialText    string `json:"initial_text"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := decode(input, &params); err != nil {
		return result(nil, err)
	}
	rc, err := t.requestContext(tc)
	if err != nil {
		return result(nil, err)
	}
	if strings.TrimSpace(params.IdempotencyKey) == "" {
		return result(nil, fmt.Errorf("idempotency_key is required"))
	}
	return result(t.service.Create(ctx, control.CreateRequest{Context: rc, SessionKey: params.SessionKey, Title: params.Title, Model: params.Model, CWD: tc.CWD, InitialText: params.InitialText, IdempotencyKey: params.IdempotencyKey}))
}

type listTool struct{ toolBase }

func (listTool) Name() string { return "SessionList" }
func (listTool) Description() string {
	return "List bounded session snapshots for one explicit source."
}
func (listTool) InputSchema() json.RawMessage {
	return schema(`{"source":{"type":"string","enum":["tenant","local"]},"limit":{"type":"integer","minimum":1,"maximum":100}}`, `"source"`)
}
func (t listTool) Run(ctx context.Context, input json.RawMessage, tc tools.Context) tools.Result {
	var params struct {
		Source string `json:"source"`
		Limit  int    `json:"limit"`
	}
	if err := decode(input, &params); err != nil {
		return result(nil, err)
	}
	rc, err := t.requestContext(tc)
	if err != nil {
		return result(nil, err)
	}
	if params.Source != string(control.SourceTenant) && params.Source != string(control.SourceLocal) {
		return result(nil, fmt.Errorf("source must be tenant or local"))
	}
	if params.Limit < 0 || params.Limit > 100 {
		return result(nil, fmt.Errorf("limit must be between 1 and 100"))
	}
	return result(t.service.List(ctx, control.ListRequest{Context: rc, Source: control.Source(params.Source), Limit: params.Limit}))
}

type getTool struct{ toolBase }

func (getTool) Name() string { return "SessionGet" }
func (getTool) Description() string {
	return "Read a bounded session snapshot. Transcript content and Handoff package prose are unavailable."
}
func (getTool) InputSchema() json.RawMessage { return schema(`{"ref":{"type":"string"}}`, `"ref"`) }
func (t getTool) Run(ctx context.Context, input json.RawMessage, tc tools.Context) tools.Result {
	var params struct {
		Ref string `json:"ref"`
	}
	if err := decode(input, &params); err != nil {
		return result(nil, err)
	}
	rc, err := t.requestContext(tc)
	if err != nil {
		return result(nil, err)
	}
	ref, err := control.ParseRef(params.Ref)
	if err != nil {
		return result(nil, err)
	}
	return result(t.service.Get(ctx, control.GetRequest{Context: rc, Ref: ref}))
}

type sendTool struct{ toolBase }

func (sendTool) Name() string { return "SessionSend" }
func (sendTool) Description() string {
	return "Send one message to a tenant-managed session using an idempotency key."
}
func (sendTool) InputSchema() json.RawMessage {
	return schema(`{"ref":{"type":"string"},"content":{"type":"string"},"idempotency_key":{"type":"string"}}`, `"ref","content","idempotency_key"`)
}
func (t sendTool) Run(ctx context.Context, input json.RawMessage, tc tools.Context) tools.Result {
	var params struct {
		Ref            string `json:"ref"`
		Content        string `json:"content"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := decode(input, &params); err != nil {
		return result(nil, err)
	}
	rc, err := t.requestContext(tc)
	if err != nil {
		return result(nil, err)
	}
	ref, err := tenantMutationRef(params.Ref)
	if err != nil {
		return result(nil, err)
	}
	if strings.TrimSpace(params.IdempotencyKey) == "" {
		return result(nil, fmt.Errorf("idempotency_key is required"))
	}
	return result(t.service.Send(ctx, control.SendRequest{Context: rc, Ref: ref, Content: params.Content, IdempotencyKey: params.IdempotencyKey}))
}

type stopTool struct{ toolBase }

func (stopTool) Name() string { return "SessionStop" }
func (stopTool) Description() string {
	return "Stop a tenant-managed session and return terminal readback."
}
func (stopTool) InputSchema() json.RawMessage {
	return schema(`{"ref":{"type":"string"},"idempotency_key":{"type":"string"}}`, `"ref","idempotency_key"`)
}
func (t stopTool) Run(ctx context.Context, input json.RawMessage, tc tools.Context) tools.Result {
	var params struct {
		Ref            string `json:"ref"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := decode(input, &params); err != nil {
		return result(nil, err)
	}
	rc, err := t.requestContext(tc)
	if err != nil {
		return result(nil, err)
	}
	ref, err := tenantMutationRef(params.Ref)
	if err != nil {
		return result(nil, err)
	}
	if strings.TrimSpace(params.IdempotencyKey) == "" {
		return result(nil, fmt.Errorf("idempotency_key is required"))
	}
	return result(t.service.Stop(ctx, control.StopRequest{Context: rc, Ref: ref, IdempotencyKey: params.IdempotencyKey}))
}

type attachTool struct{ toolBase }

func (attachTool) Name() string { return "SessionAttach" }
func (attachTool) Description() string {
	return "Attach authorized, bounded source context to a tenant-managed target task."
}
func (attachTool) InputSchema() json.RawMessage {
	return schema(`{"target":{"type":"string"},"target_task_id":{"type":"integer","minimum":1},"target_context_window_tokens":{"type":"integer","minimum":1},"sources":{"type":"array","minItems":1,"items":{"type":"string"}},"relation_type":{"type":"string"},"idempotency_key":{"type":"string"}}`, `"target","target_task_id","target_context_window_tokens","sources","idempotency_key"`)
}
func (t attachTool) Run(ctx context.Context, input json.RawMessage, tc tools.Context) tools.Result {
	var params struct {
		Target                    string   `json:"target"`
		TargetTaskID              uint64   `json:"target_task_id"`
		TargetContextWindowTokens int      `json:"target_context_window_tokens"`
		Sources                   []string `json:"sources"`
		RelationType              string   `json:"relation_type"`
		IdempotencyKey            string   `json:"idempotency_key"`
	}
	if err := decode(input, &params); err != nil {
		return result(nil, err)
	}
	rc, err := t.requestContext(tc)
	if err != nil {
		return result(nil, err)
	}
	target, err := tenantMutationRef(params.Target)
	if err != nil {
		return result(nil, err)
	}
	sources, err := parseRefs(params.Sources)
	if err != nil {
		return result(nil, err)
	}
	if strings.TrimSpace(params.IdempotencyKey) == "" {
		return result(nil, fmt.Errorf("idempotency_key is required"))
	}
	if params.TargetTaskID == 0 || params.TargetContextWindowTokens <= 0 || len(sources) == 0 {
		return result(nil, fmt.Errorf("attach request is incomplete"))
	}
	return result(t.service.Attach(ctx, control.AttachRequest{Context: rc, Target: target, TargetTaskID: params.TargetTaskID, TargetContextWindowTokens: params.TargetContextWindowTokens, Sources: sources, RelationType: params.RelationType, IdempotencyKey: params.IdempotencyKey}))
}

type monitorTool struct{ toolBase }

func (monitorTool) Name() string { return "SessionMonitor" }
func (monitorTool) Description() string {
	return "Create or update a durable monitor for a tenant-managed session."
}
func (monitorTool) InputSchema() json.RawMessage {
	properties := fmt.Sprintf(`{"target":{"type":"string"},"sources":{"type":"array","minItems":1,"items":{"type":"string"}},"interval_seconds":{"type":"integer","minimum":%d,"maximum":%d},"channel":{"type":"string","enum":["feishu"]},"idempotency_key":{"type":"string"}}`, control.SessionMonitorMinIntervalSeconds, control.SessionMonitorMaxIntervalSeconds)
	return schema(properties, `"target","sources","interval_seconds","channel","idempotency_key"`)
}
func (t monitorTool) Run(ctx context.Context, input json.RawMessage, tc tools.Context) tools.Result {
	var params struct {
		Target          string   `json:"target"`
		Sources         []string `json:"sources"`
		IntervalSeconds int      `json:"interval_seconds"`
		Channel         string   `json:"channel"`
		IdempotencyKey  string   `json:"idempotency_key"`
	}
	if err := decode(input, &params); err != nil {
		return result(nil, err)
	}
	rc, err := t.requestContext(tc)
	if err != nil {
		return result(nil, err)
	}
	target, err := tenantMutationRef(params.Target)
	if err != nil {
		return result(nil, err)
	}
	sources, err := parseRefs(params.Sources)
	if err != nil {
		return result(nil, err)
	}
	if strings.TrimSpace(params.IdempotencyKey) == "" {
		return result(nil, fmt.Errorf("idempotency_key is required"))
	}
	if len(sources) == 0 || params.IntervalSeconds < control.SessionMonitorMinIntervalSeconds || params.IntervalSeconds > control.SessionMonitorMaxIntervalSeconds || params.Channel != "feishu" {
		return result(nil, fmt.Errorf("monitor request is invalid"))
	}
	return result(t.service.Monitor(ctx, control.MonitorRequest{Context: rc, Target: target, Sources: sources, IntervalSeconds: params.IntervalSeconds, Channel: params.Channel, IdempotencyKey: params.IdempotencyKey}))
}

func schema(properties, required string) json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":` + properties + `,"required":[` + required + `],"additionalProperties":false}`)
}
