package cli

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/imagegen"
	"github.com/konglong87/super-niuma/internal/tools"
)

type wiringImageGenerator struct {
	generateCalls int
	readCalls     int
}

func (g *wiringImageGenerator) Generate(context.Context, imagegen.GenerateRequest) (imagegen.Artifact, error) {
	g.generateCalls++
	return imagegen.Artifact{AssetID: "asset-sync", GenerationID: "gen-sync", TenantID: 7, UserID: 11, SessionID: 13}, nil
}
func (*wiringImageGenerator) Edit(context.Context, imagegen.EditRequest) (imagegen.Artifact, error) {
	return imagegen.Artifact{}, nil
}

func (g *wiringImageGenerator) ReadArtifactContent(context.Context, imagegen.ArtifactContentRequest) (imagegen.ArtifactContent, error) {
	g.readCalls++
	return imagegen.ArtifactContent{MediaType: "image/png", Data: []byte("image")}, nil
}

type wiringImageScheduler struct {
	generateCalls int
	request       imagegen.EnqueueGenerateRequest
}

func (s *wiringImageScheduler) EnqueueGenerate(_ context.Context, request imagegen.EnqueueGenerateRequest) (imagegen.JobReceipt, error) {
	s.generateCalls++
	s.request = request
	return imagegen.JobReceipt{GenerationID: "gen-async", BatchID: request.BatchID, Status: imagegen.JobStatus(imagegen.GenerationStatusQueued), AcceptedAt: time.Date(2026, time.September, 3, 1, 2, 3, 0, time.UTC)}, nil
}

func (*wiringImageScheduler) EnqueueEdit(context.Context, imagegen.EnqueueEditRequest) (imagegen.JobReceipt, error) {
	return imagegen.JobReceipt{}, nil
}

func (*wiringImageScheduler) RequestCancel(context.Context, imagegen.JobScope, string) error {
	return nil
}

func TestCoreRuntimeToolsRegistersImageToolsOnlyWithInjectedGenerator(t *testing.T) {
	without := coreRuntimeTools(config.Settings{}, nil, "")
	for _, tool := range without {
		if tool.Name() == "GenerateImage" || tool.Name() == "EditImage" {
			t.Fatalf("image tool %s registered without generator", tool.Name())
		}
	}
	with := coreRuntimeToolsWithImageGenerator(config.Settings{}, nil, "", &wiringImageGenerator{})
	seen := map[string]bool{}
	for _, tool := range with {
		seen[tool.Name()] = true
	}
	if !seen["GenerateImage"] || !seen["EditImage"] {
		t.Fatalf("image tools missing from injected runtime: %+v", seen)
	}
}

func TestCoreRuntimeToolsImageArtifactPreviewFollowsMergedSettings(t *testing.T) {
	generator := &wiringImageGenerator{}
	generate := findRuntimeTool(t, coreRuntimeToolsWithImageGenerator(config.Settings{}, nil, "", generator), "GenerateImage")
	result := generate.Run(context.Background(), json.RawMessage(`{"prompt":"default"}`), tools.Context{TenantID: 7, UserID: 11, SessionID: 13})
	if result.IsError || generator.readCalls != 0 || len(result.ContextMessages) != 1 || len(result.ContextMessages[0].Content) != 1 {
		t.Fatalf("default preview result=%+v read_calls=%d", result, generator.readCalls)
	}

	enabled := true
	settings := config.Settings{ImageGeneration: &config.ImageGenerationSettings{PreviewInContext: &enabled}}
	generate = findRuntimeTool(t, coreRuntimeToolsWithImageGenerator(settings, nil, "", generator), "GenerateImage")
	result = generate.Run(context.Background(), json.RawMessage(`{"prompt":"preview"}`), tools.Context{TenantID: 7, UserID: 11, SessionID: 13})
	if result.IsError || generator.readCalls != 1 || len(result.ContextMessages) != 1 || len(result.ContextMessages[0].Content) != 2 {
		t.Fatalf("enabled preview result=%+v read_calls=%d", result, generator.readCalls)
	}
	source := result.ContextMessages[0].Content[1].Source
	if source == nil || source.Type != "base64" || source.MediaType != "image/png" || source.Data == "" || source.URL != "" {
		t.Fatalf("enabled preview source=%+v", source)
	}
}

func TestChannelImageToolsRemainSynchronousUnlessAsyncModeIsExplicit(t *testing.T) {
	generator := &wiringImageGenerator{}
	scheduler := &wiringImageScheduler{}
	registered := coreRuntimeToolsWithOptions(config.Settings{}, nil, "", options{
		imageGenerator:     generator,
		imageScheduler:     scheduler,
		asyncChannelImages: false,
		imageOriginFactory: func(tools.Context) (imagegen.OriginMetadata, error) {
			return imagegen.OriginMetadata{Type: imagegen.OriginTypeChannel, RefJSON: `{"version":2}`}, nil
		},
	})
	generate := findRuntimeTool(t, registered, "GenerateImage")
	result := generate.Run(context.Background(), json.RawMessage(`{"prompt":"sync"}`), tools.Context{TenantID: 7, UserID: 11, SessionID: 13})
	if result.IsError || generator.generateCalls != 1 || scheduler.generateCalls != 0 {
		t.Fatalf("result=%+v generator_calls=%d scheduler_calls=%d", result, generator.generateCalls, scheduler.generateCalls)
	}
}

func TestChannelImageToolsUseSchedulerOnlyWhenAsyncModeEnabled(t *testing.T) {
	generator := &wiringImageGenerator{}
	scheduler := &wiringImageScheduler{}
	registered := coreRuntimeToolsWithOptions(config.Settings{}, nil, "", options{
		imageGenerator:     generator,
		imageScheduler:     scheduler,
		asyncChannelImages: true,
		imageOriginFactory: func(tools.Context) (imagegen.OriginMetadata, error) {
			return imagegen.OriginMetadata{Type: imagegen.OriginTypeChannel, RefJSON: `{"version":2,"run_id":"run-1"}`}, nil
		},
	})
	generate := findRuntimeTool(t, registered, "GenerateImage")
	result := generate.Run(context.Background(), json.RawMessage(`{"prompt":"async"}`), tools.Context{
		TenantID: 7, UserID: 11, SessionID: 13,
		Invocation: tools.Invocation{RunID: "run-1", ToolUseID: "tool-1", BatchID: "batch-1"},
	})
	if result.IsError || scheduler.generateCalls != 1 || generator.generateCalls != 0 {
		t.Fatalf("result=%+v generator_calls=%d scheduler_calls=%d", result, generator.generateCalls, scheduler.generateCalls)
	}
	if scheduler.request.Invocation != (imagegen.RuntimeInvocation{RunID: "run-1", ToolUseID: "tool-1"}) || scheduler.request.BatchID != "batch-1" {
		t.Fatalf("scheduler request=%+v", scheduler.request)
	}
}

func findRuntimeTool(t *testing.T, registered []tools.Tool, name string) tools.Tool {
	t.Helper()
	for _, tool := range registered {
		if tool.Name() == name {
			return tool
		}
	}
	t.Fatalf("tool %s not registered", name)
	return nil
}
