package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/imagegen"
	"github.com/konglong87/super-niuma/internal/server"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	"github.com/konglong87/super-niuma/internal/telemetry"
)

func TestConfigureImageGenerationDisabledIsNoOp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(home, ".niuma"))
	gen, blobs, mediaStore, history, err := configureImageGeneration(t.TempDir(), nil)
	if err != nil || gen != nil || blobs != nil || mediaStore != nil || history != nil {
		t.Fatalf("disabled result gen=%v blobs=%v media=%v history=%v err=%v", gen, blobs, mediaStore, history, err)
	}
}

func TestConfigureImageGenerationDisabledRetainsTenantAttachmentStorage(t *testing.T) {
	t.Setenv("NIUMA_CONFIG_DIR", t.TempDir())
	repo := &mysqlstore.GormRepository{}
	gen, blobs, assets, history, err := configureImageGenerationWithResolved(t.TempDir(), config.ResolvedImageGeneration{}, repo)
	if err != nil || gen != nil || blobs == nil || assets == nil || history != nil {
		t.Fatalf("disabled tenant storage gen=%v blobs=%v assets=%v history=%v err=%v", gen, blobs, assets, history, err)
	}
}

type observedImagesProviderStub struct {
	err error
}

func (p observedImagesProviderStub) Generate(context.Context, imagegen.ProviderGenerateRequest) (imagegen.ProviderImage, error) {
	return imagegen.ProviderImage{Data: []byte("image"), MediaType: "image/png", ProviderRequestID: "request-safe"}, p.err
}

func (p observedImagesProviderStub) Edit(context.Context, imagegen.ProviderEditRequest) (imagegen.ProviderImage, error) {
	return imagegen.ProviderImage{}, p.err
}

type observedCompletionConsumerStub struct {
	err error
}

func (c observedCompletionConsumerStub) ConsumeImageCompletion(context.Context, imagegen.CompletionEvent) error {
	return c.err
}

func TestImageWorkerObservabilityEmitsNamedMetricsWithoutSensitivePayload(t *testing.T) {
	now := time.Date(2026, time.September, 2, 10, 0, 0, 0, time.UTC)
	sink := telemetry.NewMemorySink()
	ctx := telemetry.WithEmitter(context.Background(), telemetry.NewEmitter(sink))
	observer := imageWorkerObservability{WorkerID: "worker-a", Provider: "jiuan", Model: "gpt-image-2", Now: func() time.Time { return now }}
	observer.observeQueue(ctx, 7, mysqlstore.ImageGenerationQueueStats{QueueDepth: 4, Running: 2, OldestQueuedAt: timePointer(now.Add(-42 * time.Second))})
	job := imagegen.GenerationRecord{TenantID: 7, UserID: 11, SessionID: 13, GenerationID: "gen-1", BatchID: "batch-1", Attempts: 2, Provider: "jiuan", Model: "gpt-image-2", CreatedAt: now.Add(-time.Minute)}
	observer.observeClaims(ctx, []imagegen.GenerationRecord{job})
	observer.observeAttempt(ctx, job, imagegen.GenerationStatusCompleted, "", 3*time.Second)
	observer.observeAttempt(ctx, job, imagegen.GenerationStatusRetry, imagegen.ErrorClassProviderUnavailable, time.Second)
	observer.observeAttempt(ctx, job, imagegen.GenerationStatusFailed, imagegen.ErrorClassProviderRejected, time.Second)
	observer.observeLeaseLost(ctx, job)
	observer.observeStaleRecovery(ctx, 7, 2)

	provider := observedImageWorkerProvider{provider: observedImagesProviderStub{}, observer: observer}
	if _, err := provider.Generate(ctx, imagegen.ProviderGenerateRequest{Prompt: "secret-prompt", Model: "gpt-image-2"}); err != nil {
		t.Fatal(err)
	}
	consumer := observedImageCompletionConsumer{consumer: observedCompletionConsumerStub{err: errors.New("secret-prompt https://private.example")}, observer: observer}
	_ = consumer.ConsumeImageCompletion(ctx, imagegen.CompletionEvent{ID: 3, TenantID: 7, GenerationID: "gen-1", CreatedAt: now.Add(-9 * time.Second), OriginRefJSON: `{"prompt":"secret-prompt"}`})

	events := sink.Events()
	names := make(map[string]bool, len(events))
	for _, event := range events {
		names[event.Name] = true
	}
	for _, name := range []string{
		imageMetricQueueDepth, imageMetricOldestAge, imageMetricRunning, imageMetricClaims, imageMetricAttempts,
		imageMetricCompleted, imageMetricFailed, imageMetricRetries, imageMetricLeaseLost, imageMetricStaleReclaimed,
		imageMetricProviderDuration, imageMetricCompletionLag, imageMetricCompletionFailed,
	} {
		if !names[name] {
			t.Errorf("missing observability event %q: %+v", name, events)
		}
	}
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"secret-prompt", "private.example", "origin_ref_json", "api_key"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("observability leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestObservedImageWorkerRepositoryKeepsSafeClaimIdentityForAttemptTelemetry(t *testing.T) {
	repository := &observedImageWorkerRepository{}
	claimed := imagegen.GenerationRecord{
		TenantID: 7, UserID: 11, SessionID: 13, GenerationID: "gen-1", BatchID: "batch-1",
		Provider: "jiuan", Model: "gpt-image-2", Prompt: "secret-prompt", RequestJSON: `{"prompt":"secret-prompt"}`,
	}
	repository.rememberClaims([]imagegen.GenerationRecord{claimed})
	got := repository.attemptJob(imagegen.ImageGenerationAttemptFinish{TenantID: 7, GenerationID: "gen-1", AttemptNo: 2, WorkerID: "worker-a"})
	if got.TenantID != 7 || got.UserID != 11 || got.SessionID != 13 || got.BatchID != "batch-1" || got.Provider != "jiuan" || got.Model != "gpt-image-2" || got.Attempts != 2 {
		t.Fatalf("attempt identity = %+v", got)
	}
	if got.Prompt != "" || got.RequestJSON != "" {
		t.Fatalf("sensitive claim fields retained for telemetry: %+v", got)
	}
	repository.forgetClaim(got)
	if remaining := repository.attemptJob(imagegen.ImageGenerationAttemptFinish{TenantID: 7, GenerationID: "gen-1"}); remaining.UserID != 0 {
		t.Fatalf("terminal claim identity retained: %+v", remaining)
	}
}

func timePointer(value time.Time) *time.Time { return &value }

func TestConfigureChannelImageGenerationInjectsSchedulerOnlyForExactEligibleAccount(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(home, ".niuma"))
	if err := os.MkdirAll(filepath.Join(home, ".niuma"), 0o700); err != nil {
		t.Fatal(err)
	}
	settings := `{"imageGeneration":{"enabled":true,"provider":"jiuan","asyncChannelEnabled":true,"asyncChannelAccountKeys":["canary"]},"fallback":{"enabled":true,"providers":[{"name":"jiuan","type":"custom","baseURL":"https://example.test/v1","apiKey":"key"}]}}`
	if err := os.WriteFile(filepath.Join(home, ".niuma", "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := mysqlstore.NewGormRepository(nil, nil)

	eligible, err := configureChannelImageGeneration(cwd, repo, "canary")
	if err != nil {
		t.Fatal(err)
	}
	if eligible.Generator == nil || eligible.BlobStore == nil || eligible.MediaStore != repo || eligible.Scheduler == nil || !eligible.Async {
		t.Fatalf("eligible runtime = %+v", eligible)
	}

	synchronous, err := configureChannelImageGeneration(cwd, repo, "Canary")
	if err != nil {
		t.Fatal(err)
	}
	if synchronous.Generator == nil || synchronous.Scheduler != nil || synchronous.Async {
		t.Fatalf("unlisted account runtime = %+v", synchronous)
	}

	sqliteRepo, err := mysqlstore.OpenSQLiteGormRepository(context.Background(), filepath.Join(t.TempDir(), "desktop.sqlite"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sqliteRepo.Close()
	sqliteRuntime, err := configureChannelImageGeneration(cwd, sqliteRepo, "canary")
	if err != nil {
		t.Fatal(err)
	}
	if sqliteRuntime.Generator == nil || sqliteRuntime.Scheduler != nil || sqliteRuntime.Async {
		t.Fatalf("sqlite runtime must remain synchronous = %+v", sqliteRuntime)
	}
}

func TestConfigureImageWorkerRuntimeComposesAllDurableBoundaries(t *testing.T) {
	cwd := t.TempDir()
	resolved := config.ResolvedImageGeneration{
		Enabled: true, Provider: "jiuan", Model: "gpt-image-2", Quality: "auto", Size: "auto", OutputFormat: "png", Background: "auto",
		MaxImages: 1, MaxPromptChars: 8000, MaxInputBytes: 25 * 1024 * 1024, MaxConcurrent: 2, TimeoutSeconds: 180,
		Worker:  config.ResolvedImageGenerationWorker{PollIntervalMS: 500, MaxConcurrent: 2, MaxAttempts: 3, HeartbeatSeconds: 15, LeaseSeconds: 240, FinalizeTimeoutSeconds: 10, MaxQueuedPerTenant: 100, MaxQueuedPerUser: 20},
		BaseURL: "https://example.test/v1", APIKey: "secret-key",
	}
	repo := mysqlstore.NewGormRepository(nil, nil)
	ready := func(imageWorkerReadyState) error { return nil }
	runner, err := configureImageWorkerRuntime(cwd, resolved, repo, 7, "worker-a", ready)
	if err != nil {
		t.Fatal(err)
	}
	if runner == nil {
		t.Fatal("worker runtime is nil")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cancelled composed runner error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("composed worker ignored shutdown context")
	}
}

func TestConfigureImageGenerationRequiresTenantStorageWhenEnabled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(home, ".niuma"))
	if err := os.MkdirAll(filepath.Join(home, ".niuma"), 0o700); err != nil {
		t.Fatal(err)
	}
	settings := `{"imageGeneration":{"enabled":true,"provider":"jiuan","model":"gpt-image-2"},"fallback":{"enabled":true,"providers":[{"name":"jiuan","type":"custom","baseURL":"https://example.test/v1","apiKey":"key","model":"gpt-5.6-sol"}]}}`
	if err := os.WriteFile(filepath.Join(home, ".niuma", "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, err := configureImageGeneration(t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "tenant persistence storage") {
		t.Fatalf("err=%v", err)
	}
}

func TestConfigureImageGenerationWithSettingsInputsEnablesRuntime(t *testing.T) {
	_, _, _, _, err := configureImageGenerationWithSettingsInputs(t.TempDir(), []string{`{"imageGeneration":{"enabled":true,"provider":"jiuan"},"fallback":{"providers":[{"name":"jiuan","baseURL":"https://images.example.test/v1","apiKey":"test-key"}]}}`}, nil)
	if err == nil || !strings.Contains(err.Error(), "requires tenant persistence storage") {
		t.Fatalf("expected enabled image runtime to reach storage validation, got %v", err)
	}
}

func TestConfigureServerImageRuntimeWithSQLiteProvidesImageRuntime(t *testing.T) {
	t.Setenv("NIUMA_CONFIG_DIR", t.TempDir())
	repo, err := mysqlstore.OpenSQLiteGormRepository(context.Background(), filepath.Join(t.TempDir(), "desktop.sqlite"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()

	opts := &server.Options{Workspace: t.TempDir()}
	settings := `{"imageGeneration":{"enabled":true,"provider":"jiuan","model":"gpt-image-2"},"fallback":{"providers":[{"name":"jiuan","type":"custom","baseURL":"https://images.example.test/v1","apiKey":"test-key"}]}}`
	if err := configureServerImageRuntime(opts.Workspace, []string{settings}, repo, opts); err != nil {
		t.Fatal(err)
	}
	if opts.ImageGenerator == nil || opts.ImageBlobStore == nil || opts.ImageGenerationStore == nil {
		t.Fatalf("sqlite image runtime incomplete: generator=%T blob=%T history=%T", opts.ImageGenerator, opts.ImageBlobStore, opts.ImageGenerationStore)
	}
	if opts.MediaAssetStore != repo {
		t.Fatalf("sqlite media store = %T, want repository", opts.MediaAssetStore)
	}
	if len(opts.ImageCatalog) != 1 || opts.ImageCatalog[0].Provider != "jiuan" || opts.ImageCatalog[0].Model != "gpt-image-2" {
		t.Fatalf("image catalog = %+v", opts.ImageCatalog)
	}
	registered := coreRuntimeToolsWithImageGenerator(config.Settings{}, nil, "", opts.ImageGenerator)
	if findRuntimeTool(t, registered, "GenerateImage") == nil || findRuntimeTool(t, registered, "EditImage") == nil {
		t.Fatal("sqlite server image runtime did not register image tools")
	}
}
