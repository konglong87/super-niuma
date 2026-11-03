package imagegen

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	service "github.com/konglong87/super-niuma/internal/imagegen"
	"github.com/konglong87/super-niuma/internal/media"
	"github.com/konglong87/super-niuma/internal/tools"
)

const onePixelPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

type stubGenerator struct {
	generated   service.Artifact
	edited      service.Artifact
	gotGenerate service.GenerateRequest
	gotEdit     service.EditRequest
}

type stubScheduler struct {
	receipt     service.JobReceipt
	generateReq service.EnqueueGenerateRequest
	editReq     service.EnqueueEditRequest
}

type staticImageProvider struct{ data []byte }

func (p staticImageProvider) Generate(context.Context, service.ProviderGenerateRequest) (service.ProviderImage, error) {
	return service.ProviderImage{Data: append([]byte(nil), p.data...), MediaType: "image/png"}, nil
}

func (p staticImageProvider) Edit(context.Context, service.ProviderEditRequest) (service.ProviderImage, error) {
	return service.ProviderImage{Data: append([]byte(nil), p.data...), MediaType: "image/png"}, nil
}

type corruptingMediaStore struct {
	*media.MemoryStore
	blobs *service.MemoryBlobStore
}

func (s corruptingMediaStore) Put(ctx context.Context, asset media.Asset) error {
	if err := s.MemoryStore.Put(ctx, asset); err != nil {
		return err
	}
	_, err := s.blobs.Put(ctx, service.PutBlobRequest{
		Policy: asset.Access, Key: asset.Original.Path, MediaType: asset.MediaType,
		Name: asset.Name, Data: bytes.NewReader([]byte("corrupted-image")),
	})
	return err
}

type artifactReturningGenerator struct {
	*service.Service
	artifact service.Artifact
}

func (g artifactReturningGenerator) Generate(context.Context, service.GenerateRequest) (service.Artifact, error) {
	return g.artifact, nil
}

func (g artifactReturningGenerator) Edit(context.Context, service.EditRequest) (service.Artifact, error) {
	return g.artifact, nil
}

func mustOnePixelPNG(t *testing.T) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(onePixelPNGBase64)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func (s *stubScheduler) EnqueueGenerate(_ context.Context, req service.EnqueueGenerateRequest) (service.JobReceipt, error) {
	s.generateReq = req
	return s.receipt, nil
}

func (s *stubScheduler) EnqueueEdit(_ context.Context, req service.EnqueueEditRequest) (service.JobReceipt, error) {
	s.editReq = req
	return s.receipt, nil
}

func (*stubScheduler) RequestCancel(context.Context, service.JobScope, string) error { return nil }

func (s *stubGenerator) Generate(_ context.Context, req service.GenerateRequest) (service.Artifact, error) {
	s.gotGenerate = req
	return s.generated, nil
}

func (s *stubGenerator) Edit(_ context.Context, req service.EditRequest) (service.Artifact, error) {
	s.gotEdit = req
	return s.edited, nil
}

func TestGenerateImageDefaultsToMetadataWithoutModelPreview(t *testing.T) {
	imageBytes := mustOnePixelPNG(t)
	gen := service.NewService(service.ServiceConfig{
		Provider: staticImageProvider{data: imageBytes}, BlobStore: service.NewMemoryBlobStore(),
		MediaStore: media.NewMemoryStore(), ProviderName: "jiuan", Model: "gpt-image-2",
	})
	var eventPayload string
	result := NewGenerate(gen).Run(context.Background(), json.RawMessage(`{"prompt":"a cat","quality":"high"}`), tools.Context{
		TenantID: 7, UserID: 11, SessionID: 13, TraceID: "trace-1",
		TaskProgress: func(event agenttasks.EventInput) { eventPayload = event.PayloadJSON },
	})

	if result.IsError || !strings.Contains(result.Content, `"url":"/tenant/media/assets/`) {
		t.Fatalf("result = %+v", result)
	}
	assertImageNotInspected(t, result)
	if strings.Contains(result.Content, onePixelPNGBase64) || strings.Contains(eventPayload, onePixelPNGBase64) || !strings.Contains(eventPayload, `"url":"/tenant/media/assets/`) {
		t.Fatalf("artifact references and model bytes were not separated: content=%s event=%s", result.Content, eventPayload)
	}
}

func TestGenerateImageArtifactPreviewUsesAuthorizedBytesForModelContext(t *testing.T) {
	imageBytes := mustOnePixelPNG(t)
	gen := service.NewService(service.ServiceConfig{
		Provider: staticImageProvider{data: imageBytes}, BlobStore: service.NewMemoryBlobStore(),
		MediaStore: media.NewMemoryStore(), ProviderName: "jiuan", Model: "gpt-image-2",
	})
	tool := NewGenerate(gen, WithArtifactPreview(true))
	var schema map[string]any
	if err := json.Unmarshal(tool.InputSchema(), &schema); err != nil {
		t.Fatal(err)
	}
	if schema["type"] != "object" {
		t.Fatalf("schema type = %v", schema["type"])
	}
	var eventPayload string
	result := tool.Run(context.Background(), json.RawMessage(`{"prompt":"a cat","quality":"high"}`), tools.Context{
		TenantID: 7, UserID: 11, SessionID: 13, TraceID: "trace-1",
		TaskProgress: func(event agenttasks.EventInput) { eventPayload = event.PayloadJSON },
	})
	if result.IsError || !strings.Contains(result.Content, `"url":"/tenant/media/assets/`) {
		t.Fatalf("result = %+v", result)
	}
	assertModelImage(t, result)
	if strings.Contains(result.Content, onePixelPNGBase64) || strings.Contains(eventPayload, onePixelPNGBase64) || !strings.Contains(eventPayload, `"url":"/tenant/media/assets/`) {
		t.Fatalf("artifact references and model bytes were not separated: content=%s event=%s", result.Content, eventPayload)
	}
}

func TestEditImageUsesAuthorizedBytesForModelContext(t *testing.T) {
	imageBytes := mustOnePixelPNG(t)
	gen := service.NewService(service.ServiceConfig{
		Provider: staticImageProvider{data: imageBytes}, BlobStore: service.NewMemoryBlobStore(),
		MediaStore: media.NewMemoryStore(), ProviderName: "jiuan", Model: "gpt-image-2",
	})
	source, err := gen.Generate(context.Background(), service.GenerateRequest{TenantID: 7, UserID: 11, SessionID: 13, Prompt: "source"})
	if err != nil {
		t.Fatal(err)
	}
	input := json.RawMessage(`{"prompt":"redraw","source_asset_id":"` + source.AssetID + `"}`)
	result := NewEdit(gen, WithArtifactPreview(true)).Run(context.Background(), input, tools.Context{TenantID: 7, UserID: 11, SessionID: 13})

	if result.IsError || !strings.Contains(result.Content, `"operation":"edit"`) || !strings.Contains(result.Content, `"source_asset_id":"`+source.AssetID+`"`) {
		t.Fatalf("result = %+v", result)
	}
	assertModelImage(t, result)
}

func assertModelImage(t *testing.T, result tools.Result) {
	t.Helper()
	if len(result.ContextMessages) != 1 || len(result.ContextMessages[0].Content) != 2 {
		t.Fatalf("context messages = %+v", result.ContextMessages)
	}
	if got := result.ContextMessages[0].Content[1].Source; got == nil || got.Type != "base64" || got.URL != "" || got.MediaType != "image/png" || got.Data != onePixelPNGBase64 {
		t.Fatalf("image context source = %+v", got)
	}
}

func TestGenerateImageWithoutArtifactReaderDoesNotSendBrowserURLToModel(t *testing.T) {
	gen := &stubGenerator{generated: service.Artifact{AssetID: "img-1", TenantID: 7, UserID: 11, SessionID: 13, MediaType: "image/png", URL: "/tenant/media/assets/img-1"}}
	result := NewGenerate(gen, WithArtifactPreview(true)).Run(context.Background(), json.RawMessage(`{"prompt":"a cat"}`), tools.Context{TenantID: 7, UserID: 11, SessionID: 13})

	assertImageUnavailableToModel(t, result)
	if gen.gotGenerate.TenantID != 7 || gen.gotGenerate.UserID != 11 || gen.gotGenerate.SessionID != 13 {
		t.Fatalf("request scope = %+v", gen.gotGenerate)
	}
}

func TestGenerateImageRejectsCrossScopeArtifactContext(t *testing.T) {
	imageBytes := mustOnePixelPNG(t)
	reader := service.NewService(service.ServiceConfig{
		Provider: staticImageProvider{data: imageBytes}, BlobStore: service.NewMemoryBlobStore(),
		MediaStore: media.NewMemoryStore(), ProviderName: "jiuan", Model: "gpt-image-2",
	})
	foreign, err := reader.Generate(context.Background(), service.GenerateRequest{TenantID: 7, UserID: 99, SessionID: 88, Prompt: "foreign"})
	if err != nil {
		t.Fatal(err)
	}
	gen := artifactReturningGenerator{Service: reader, artifact: foreign}
	events := 0
	result := NewGenerate(gen, WithArtifactPreview(true)).Run(context.Background(), json.RawMessage(`{"prompt":"a cat"}`), tools.Context{
		TenantID: 7, UserID: 11, SessionID: 13,
		TaskProgress: func(agenttasks.EventInput) { events++ },
	})

	if !result.IsError || len(result.ContextMessages) != 0 || events != 0 || !strings.Contains(result.Content, "scope") {
		t.Fatalf("cross-scope artifact result = %+v events=%d", result, events)
	}
}

func TestGenerateImageRejectsCorruptedArtifactContext(t *testing.T) {
	imageBytes := mustOnePixelPNG(t)
	blobs := service.NewMemoryBlobStore()
	gen := service.NewService(service.ServiceConfig{
		Provider: staticImageProvider{data: imageBytes}, BlobStore: blobs,
		MediaStore:   corruptingMediaStore{MemoryStore: media.NewMemoryStore(), blobs: blobs},
		ProviderName: "jiuan", Model: "gpt-image-2",
	})
	var events []agenttasks.EventInput
	result := NewGenerate(gen, WithArtifactPreview(true)).Run(context.Background(), json.RawMessage(`{"prompt":"a cat"}`), tools.Context{
		TenantID: 7, UserID: 11, SessionID: 13,
		TaskProgress: func(event agenttasks.EventInput) { events = append(events, event) },
	})

	assertImageUnavailableToModel(t, result)
	if len(events) != 1 || events[0].EventType != agenttasks.EventImageArtifact || !strings.Contains(events[0].PayloadJSON, `"url":"/tenant/media/assets/`) || strings.Contains(events[0].PayloadJSON, onePixelPNGBase64) {
		t.Fatalf("artifact event must survive model-context rejection without bytes: %+v", events)
	}
}

func assertImageUnavailableToModel(t *testing.T, result tools.Result) {
	t.Helper()
	if result.IsError || len(result.ContextMessages) != 1 {
		t.Fatalf("result = %+v", result)
	}
	for _, block := range result.ContextMessages[0].Content {
		if block.Type == "image" || (block.Source != nil && (block.Source.URL != "" || block.Source.Data != "")) {
			t.Fatalf("unreadable image was sent to model: %+v", result.ContextMessages)
		}
	}
	if !strings.Contains(strings.ToLower(result.ContextMessages[0].Content[0].Text), "unavailable") {
		t.Fatalf("missing explicit unavailable context: %+v", result.ContextMessages)
	}
}

func assertImageNotInspected(t *testing.T, result tools.Result) {
	t.Helper()
	if result.IsError || len(result.ContextMessages) != 1 || len(result.ContextMessages[0].Content) != 1 {
		t.Fatalf("result = %+v", result)
	}
	block := result.ContextMessages[0].Content[0]
	if block.Type != "text" || block.Source != nil || !strings.Contains(strings.ToLower(block.Text), "not visually inspected") {
		t.Fatalf("default artifact context = %+v", result.ContextMessages)
	}
}

func TestGenerateImageAsyncReturnsQueuedReceiptWithoutImageContext(t *testing.T) {
	acceptedAt := time.Date(2026, time.September, 3, 1, 2, 3, 0, time.UTC)
	scheduler := &stubScheduler{receipt: service.JobReceipt{GenerationID: "gen-queued", BatchID: "batch-1", Status: service.JobStatus(service.GenerationStatusQueued), AcceptedAt: acceptedAt}}
	gen := &stubGenerator{}
	var factoryContext tools.Context
	tool := NewGenerate(gen, WithScheduler(scheduler, func(tc tools.Context) (service.OriginMetadata, error) {
		factoryContext = tc
		return service.OriginMetadata{Type: service.OriginTypeChannel, RefJSON: `{"version":1,"run_id":"run-1"}`}, nil
	}))
	tc := tools.Context{
		TenantID: 7, UserID: 11, SessionID: 13, TraceID: "trace-1",
		Invocation: tools.Invocation{RunID: "run-1", ToolUseID: "tool-1", BatchID: "batch-1"},
	}
	result := tool.Run(context.Background(), json.RawMessage(`{"prompt":"a cat","model":"ignored-model","quality":"high","idempotency_key":"model-key"}`), tc)

	if result.IsError || len(result.ContextMessages) != 0 {
		t.Fatalf("async result = %+v", result)
	}
	var receipt service.JobReceipt
	if err := json.Unmarshal([]byte(result.Content), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt != scheduler.receipt {
		t.Fatalf("receipt = %+v, want %+v", receipt, scheduler.receipt)
	}
	if scheduler.generateReq.Scope != (service.JobScope{TenantID: 7, UserID: 11, SessionID: 13}) || scheduler.generateReq.Prompt != "a cat" || scheduler.generateReq.Model != "ignored-model" || scheduler.generateReq.Quality != "high" || scheduler.generateReq.TraceID != "trace-1" {
		t.Fatalf("generate request = %+v", scheduler.generateReq)
	}
	if scheduler.generateReq.BatchID != "batch-1" || scheduler.generateReq.Invocation != (service.RuntimeInvocation{RunID: "run-1", ToolUseID: "tool-1"}) || scheduler.generateReq.BusinessIdempotencyKey != "model-key" {
		t.Fatalf("trusted request identity = %+v", scheduler.generateReq)
	}
	if scheduler.generateReq.Origin.Type != service.OriginTypeChannel || scheduler.generateReq.Origin.RefJSON != `{"version":1,"run_id":"run-1"}` {
		t.Fatalf("origin = %+v", scheduler.generateReq.Origin)
	}
	if factoryContext.Invocation != tc.Invocation || gen.gotGenerate.Prompt != "" {
		t.Fatalf("factory context = %+v sync request = %+v", factoryContext.Invocation, gen.gotGenerate)
	}
}

func TestEditImageAsyncEnqueuesOwnedSourceWithoutArtifactEvent(t *testing.T) {
	scheduler := &stubScheduler{receipt: service.JobReceipt{GenerationID: "gen-edit", BatchID: "batch-edit", Status: service.JobStatus(service.GenerationStatusQueued), AcceptedAt: time.Date(2026, time.September, 3, 2, 3, 4, 0, time.UTC)}}
	events := 0
	tool := NewEdit(&stubGenerator{}, WithScheduler(scheduler, func(tools.Context) (service.OriginMetadata, error) {
		return service.OriginMetadata{Type: service.OriginTypeChannel, RefJSON: `{}`}, nil
	}))
	result := tool.Run(context.Background(), json.RawMessage(`{"prompt":"redraw","source_asset_id":"asset-1"}`), tools.Context{
		TenantID: 7, UserID: 11, SessionID: 13, TraceID: "trace-edit",
		Invocation:   tools.Invocation{RunID: "run-edit", ToolUseID: "tool-edit", BatchID: "batch-edit"},
		TaskProgress: func(agenttasks.EventInput) { events++ },
	})

	if result.IsError || len(result.ContextMessages) != 0 || events != 0 {
		t.Fatalf("async edit result = %+v events=%d", result, events)
	}
	if scheduler.editReq.SourceAssetID != "asset-1" || scheduler.editReq.Prompt != "redraw" || scheduler.editReq.BatchID != "batch-edit" || scheduler.editReq.Invocation != (service.RuntimeInvocation{RunID: "run-edit", ToolUseID: "tool-edit"}) {
		t.Fatalf("edit request = %+v", scheduler.editReq)
	}
}

func TestImageToolsRejectDisabledOrInvalidScope(t *testing.T) {
	for _, tool := range []tools.Tool{NewGenerate(nil), NewEdit(nil)} {
		result := tool.Run(context.Background(), json.RawMessage(`{"prompt":"x"}`), tools.Context{TenantID: 1, UserID: 2, SessionID: 3})
		if !result.IsError || !strings.Contains(result.Content, "disabled") {
			t.Fatalf("%s disabled result = %+v", tool.Name(), result)
		}
	}
	gen := &stubGenerator{}
	result := NewEdit(gen).Run(context.Background(), json.RawMessage(`{"prompt":"redraw","source_asset_id":"asset-1"}`), tools.Context{TenantID: 0, UserID: 2, SessionID: 3})
	if !result.IsError || !strings.Contains(result.Content, "required") {
		t.Fatalf("invalid scope result = %+v", result)
	}
}

func TestEditImageRequiresOwnedSourceAssetAndEmitsArtifactEvent(t *testing.T) {
	gen := &stubGenerator{edited: service.Artifact{AssetID: "img-2", GenerationID: "gen-2", TenantID: 7, UserID: 11, SessionID: 13, Operation: service.OperationEdit, SourceAssetID: "asset-1", MediaType: "image/png", URL: "/tenant/media/assets/img-2", Provider: "jiuan", Model: "gpt-image-2"}}
	var eventType, payload string
	tool := NewEdit(gen)
	result := tool.Run(context.Background(), json.RawMessage(`{"prompt":"redraw","source_asset_id":"asset-1","idempotency_key":"k1"}`), tools.Context{TenantID: 7, UserID: 11, SessionID: 13, TraceID: "trace-2", TaskProgress: func(event agenttasks.EventInput) {
		eventType = event.EventType
		payload = event.PayloadJSON
	}})
	if result.IsError || !strings.Contains(result.Content, `"asset_id":"img-2"`) {
		t.Fatalf("result = %+v", result)
	}
	if gen.gotEdit.TenantID != 7 || gen.gotEdit.SourceAssetID != "asset-1" || gen.gotEdit.IdempotencyKey != "k1" {
		t.Fatalf("edit request = %+v", gen.gotEdit)
	}
	assertImageNotInspected(t, result)
	if eventType != "image_artifact" || !strings.Contains(payload, `"asset_id":"img-2"`) || strings.Contains(payload, "base64") {
		t.Fatalf("artifact event = %s %s", eventType, payload)
	}
}
