// Package imagegen exposes the shared image service as agent runtime tools.
package imagegen

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/anthropic"
	service "github.com/konglong87/super-niuma/internal/imagegen"
	"github.com/konglong87/super-niuma/internal/tools"
)

type OriginFactory func(tools.Context) (service.OriginMetadata, error)

type Option func(*toolOptions)

type toolOptions struct {
	scheduler       service.Scheduler
	originFactory   OriginFactory
	artifactPreview bool
}

// WithScheduler enables asynchronous execution for channel-owned tools. The
// factory supplies trusted routing metadata without exposing it to model input.
func WithScheduler(scheduler service.Scheduler, originFactory OriginFactory) Option {
	return func(options *toolOptions) {
		options.scheduler = scheduler
		options.originFactory = originFactory
	}
}

// WithArtifactPreview controls whether a generated artifact is read back and
// sent to the chat model as validated base64 image content. It is opt-in
// because image generation does not imply that the selected chat model has
// vision capability.
func WithArtifactPreview(enabled bool) Option {
	return func(options *toolOptions) {
		options.artifactPreview = enabled
	}
}

type GenerateTool struct {
	generator       service.Generator
	scheduler       service.Scheduler
	originFactory   OriginFactory
	artifactPreview bool
}

type EditTool struct {
	generator       service.Generator
	scheduler       service.Scheduler
	originFactory   OriginFactory
	artifactPreview bool
}

func NewGenerate(generator service.Generator, options ...Option) GenerateTool {
	configured := resolveOptions(options)
	return GenerateTool{generator: generator, scheduler: configured.scheduler, originFactory: configured.originFactory, artifactPreview: configured.artifactPreview}
}

func NewEdit(generator service.Generator, options ...Option) EditTool {
	configured := resolveOptions(options)
	return EditTool{generator: generator, scheduler: configured.scheduler, originFactory: configured.originFactory, artifactPreview: configured.artifactPreview}
}

func (GenerateTool) Name() string { return "GenerateImage" }
func (EditTool) Name() string     { return "EditImage" }

func (GenerateTool) Description() string {
	return "Generate an image with the configured image model and persist it as a tenant-scoped asset. Returns an asset reference that can be displayed or edited later."
}

func (EditTool) Description() string {
	return "Edit or redraw an existing tenant-scoped image asset using a prompt. The source_asset_id must belong to the current tenant, user, and session."
}

func (GenerateTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string"},"provider":{"type":"string"},"model":{"type":"string"},"quality":{"type":"string","enum":["auto","low","medium","high"]},"size":{"type":"string","enum":["auto","1024x1024","1536x1024","1024x1536"]},"resolution":{"type":"string","enum":["1K","2K","3K","4K"]},"aspect_ratio":{"type":"string","enum":["1:1","3:4","4:3","16:9","9:16","2:3","3:2","21:9"]},"output_format":{"type":"string","enum":["png","jpeg","webp"]},"background":{"type":"string","enum":["auto","transparent","opaque"]},"watermark":{"type":"boolean"},"idempotency_key":{"type":"string"}},"required":["prompt"],"additionalProperties":false}`)
}

func (EditTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string"},"source_asset_id":{"type":"string"},"provider":{"type":"string"},"model":{"type":"string"},"quality":{"type":"string","enum":["auto","low","medium","high"]},"size":{"type":"string","enum":["auto","1024x1024","1536x1024","1024x1536"]},"resolution":{"type":"string","enum":["1K","2K","3K","4K"]},"aspect_ratio":{"type":"string","enum":["1:1","3:4","4:3","16:9","9:16","2:3","3:2","21:9"]},"output_format":{"type":"string","enum":["png","jpeg","webp"]},"background":{"type":"string","enum":["auto","transparent","opaque"]},"watermark":{"type":"boolean"},"idempotency_key":{"type":"string"}},"required":["prompt","source_asset_id"],"additionalProperties":false}`)
}

type imageParams struct {
	Prompt         string `json:"prompt"`
	Provider       string `json:"provider"`
	Model          string `json:"model"`
	Quality        string `json:"quality"`
	Size           string `json:"size"`
	Resolution     string `json:"resolution"`
	AspectRatio    string `json:"aspect_ratio"`
	OutputFormat   string `json:"output_format"`
	Background     string `json:"background"`
	Watermark      *bool  `json:"watermark"`
	IdempotencyKey string `json:"idempotency_key"`
}

func (t GenerateTool) Run(ctx context.Context, input json.RawMessage, tc tools.Context) tools.Result {
	if t.generator == nil && t.scheduler == nil {
		return tools.Result{Content: service.ErrImageGenerationDisabled.Error(), IsError: true}
	}
	if err := validateScope(tc); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	var p imageParams
	if err := json.Unmarshal(input, &p); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if t.scheduler != nil {
		origin, err := asyncOrigin(t.originFactory, tc)
		if err != nil {
			return tools.Result{Content: err.Error(), IsError: true}
		}
		receipt, err := t.scheduler.EnqueueGenerate(ctx, service.EnqueueGenerateRequest{
			Scope:    service.JobScope{TenantID: tc.TenantID, UserID: tc.UserID, SessionID: tc.SessionID},
			Provider: p.Provider, Prompt: p.Prompt, Model: p.Model, Quality: p.Quality, Size: p.Size, Resolution: p.Resolution, AspectRatio: p.AspectRatio, Watermark: p.Watermark, OutputFormat: p.OutputFormat, Background: p.Background,
			BatchID: tc.Invocation.BatchID, TraceID: tc.TraceID, BusinessIdempotencyKey: p.IdempotencyKey, Origin: origin,
			Invocation: service.RuntimeInvocation{RunID: tc.Invocation.RunID, ToolUseID: tc.Invocation.ToolUseID},
		})
		if err != nil {
			return tools.Result{Content: err.Error(), IsError: true}
		}
		return receiptResult(receipt)
	}
	a, err := t.generator.Generate(ctx, service.GenerateRequest{TenantID: tc.TenantID, UserID: tc.UserID, SessionID: tc.SessionID, Provider: p.Provider, Prompt: p.Prompt, Model: p.Model, Quality: p.Quality, Size: p.Size, Resolution: p.Resolution, AspectRatio: p.AspectRatio, Watermark: p.Watermark, OutputFormat: p.OutputFormat, Background: p.Background, IdempotencyKey: p.IdempotencyKey, TraceID: tc.TraceID})
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	return artifactResult(ctx, t.Name(), t.generator, a, tc, t.artifactPreview)
}

func (t EditTool) Run(ctx context.Context, input json.RawMessage, tc tools.Context) tools.Result {
	if t.generator == nil && t.scheduler == nil {
		return tools.Result{Content: service.ErrImageGenerationDisabled.Error(), IsError: true}
	}
	if err := validateScope(tc); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	var p struct {
		imageParams
		SourceAssetID string `json:"source_asset_id"`
	}
	if err := json.Unmarshal(input, &p); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if strings.TrimSpace(p.SourceAssetID) == "" {
		return tools.Result{Content: "source_asset_id is required", IsError: true}
	}
	if t.scheduler != nil {
		origin, err := asyncOrigin(t.originFactory, tc)
		if err != nil {
			return tools.Result{Content: err.Error(), IsError: true}
		}
		receipt, err := t.scheduler.EnqueueEdit(ctx, service.EnqueueEditRequest{
			Scope:    service.JobScope{TenantID: tc.TenantID, UserID: tc.UserID, SessionID: tc.SessionID},
			Provider: p.Provider, Prompt: p.Prompt, Model: p.Model, Quality: p.Quality, Size: p.Size, Resolution: p.Resolution, AspectRatio: p.AspectRatio, Watermark: p.Watermark, OutputFormat: p.OutputFormat, Background: p.Background, SourceAssetID: p.SourceAssetID,
			BatchID: tc.Invocation.BatchID, TraceID: tc.TraceID, BusinessIdempotencyKey: p.IdempotencyKey, Origin: origin,
			Invocation: service.RuntimeInvocation{RunID: tc.Invocation.RunID, ToolUseID: tc.Invocation.ToolUseID},
		})
		if err != nil {
			return tools.Result{Content: err.Error(), IsError: true}
		}
		return receiptResult(receipt)
	}
	a, err := t.generator.Edit(ctx, service.EditRequest{TenantID: tc.TenantID, UserID: tc.UserID, SessionID: tc.SessionID, Provider: p.Provider, Prompt: p.Prompt, Model: p.Model, Quality: p.Quality, Size: p.Size, Resolution: p.Resolution, AspectRatio: p.AspectRatio, Watermark: p.Watermark, OutputFormat: p.OutputFormat, Background: p.Background, SourceAssetID: p.SourceAssetID, IdempotencyKey: p.IdempotencyKey, TraceID: tc.TraceID})
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	return artifactResult(ctx, t.Name(), t.generator, a, tc, t.artifactPreview)
}

func resolveOptions(options []Option) toolOptions {
	configured := toolOptions{}
	for _, option := range options {
		if option != nil {
			option(&configured)
		}
	}
	return configured
}

func asyncOrigin(factory OriginFactory, tc tools.Context) (service.OriginMetadata, error) {
	if strings.TrimSpace(tc.Invocation.RunID) == "" || strings.TrimSpace(tc.Invocation.ToolUseID) == "" || strings.TrimSpace(tc.Invocation.BatchID) == "" {
		return service.OriginMetadata{}, fmt.Errorf("runtime invocation and batch are required")
	}
	if factory == nil {
		return service.OriginMetadata{}, fmt.Errorf("image job origin is not configured")
	}
	return factory(tc)
}

func receiptResult(receipt service.JobReceipt) tools.Result {
	payload, err := json.Marshal(receipt)
	if err != nil {
		return tools.Result{Content: fmt.Sprintf("marshal image job receipt: %v", err), IsError: true}
	}
	return tools.Result{Content: string(payload)}
}

func validateScope(tc tools.Context) error {
	if tc.TenantID == 0 || tc.UserID == 0 || tc.SessionID == 0 {
		return fmt.Errorf("tenant, user and session are required")
	}
	return nil
}

func artifactResult(ctx context.Context, toolName string, generator service.Generator, artifact service.Artifact, tc tools.Context, preview bool) tools.Result {
	if artifact.TenantID != tc.TenantID || artifact.UserID != tc.UserID || artifact.SessionID != tc.SessionID || strings.TrimSpace(artifact.AssetID) == "" {
		return tools.Result{Content: "image artifact scope does not match current tool context", IsError: true}
	}
	payload, err := json.Marshal(artifact)
	if err != nil {
		return tools.Result{Content: fmt.Sprintf("marshal image artifact: %v", err), IsError: true}
	}
	if tc.TaskProgress != nil {
		tc.TaskProgress(agenttasks.EventInput{TenantID: tc.TenantID, UserID: tc.UserID, EventType: agenttasks.EventImageArtifact, PayloadJSON: string(payload), TraceID: tc.TraceID})
	}
	message := artifactContextMessage(ctx, toolName, generator, artifact, tc, preview)
	return tools.Result{Content: string(payload), ContextMessages: []anthropic.MessageParam{message}}
}

func artifactContextMessage(ctx context.Context, toolName string, generator service.Generator, artifact service.Artifact, tc tools.Context, preview bool) anthropic.MessageParam {
	text := fmt.Sprintf("Image artifact returned by %s: %s.", toolName, artifact.AssetID)
	if !preview {
		return artifactTextMessage(text + " The image was generated successfully but was not visually inspected.")
	}
	reader, ok := generator.(service.ArtifactContentReader)
	if !ok {
		return unavailableArtifactMessage(text)
	}
	content, err := reader.ReadArtifactContent(ctx, service.ArtifactContentRequest{
		TenantID: tc.TenantID, UserID: tc.UserID, SessionID: tc.SessionID, AssetID: artifact.AssetID,
	})
	if err != nil {
		return unavailableArtifactMessage(text)
	}
	return anthropic.MessageParam{Role: "user", Content: []anthropic.ContentBlock{
		{Type: "text", Text: text},
		{Type: "image", Source: &anthropic.ContentSource{Type: "base64", MediaType: content.MediaType, Data: base64.StdEncoding.EncodeToString(content.Data)}},
	}}
}

func unavailableArtifactMessage(text string) anthropic.MessageParam {
	return artifactTextMessage(text + " Image content is unavailable to the model and was not visually inspected.")
}

func artifactTextMessage(text string) anthropic.MessageParam {
	return anthropic.MessageParam{Role: "user", Content: []anthropic.ContentBlock{{Type: "text", Text: text}}}
}
