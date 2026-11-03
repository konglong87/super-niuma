package cli

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"time"

	channelruntime "github.com/konglong87/super-niuma/internal/channel/runtime"
	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/imagegen"
	"github.com/konglong87/super-niuma/internal/media"
	"github.com/konglong87/super-niuma/internal/server"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	"github.com/konglong87/super-niuma/internal/telemetry"
)

const (
	imageMetricQueueDepth       = "image_jobs_queue_depth"
	imageMetricOldestAge        = "image_jobs_oldest_age_seconds"
	imageMetricRunning          = "image_jobs_running"
	imageMetricClaims           = "image_jobs_claimed_total"
	imageMetricAttempts         = "image_jobs_attempt_total"
	imageMetricCompleted        = "image_jobs_completed_total"
	imageMetricFailed           = "image_jobs_failed_total"
	imageMetricRetries          = "image_jobs_retry_total"
	imageMetricLeaseLost        = "image_jobs_lease_lost_total"
	imageMetricStaleReclaimed   = "image_jobs_stale_reclaimed_total"
	imageMetricProviderDuration = "image_provider_duration_seconds"
	imageMetricCompletionLag    = "image_completion_delivery_lag_seconds"
	imageMetricCompletionFailed = "image_completion_delivery_failed_total"

	imageWorkerTelemetrySource = "cli.image-worker"
	imageWorkerConsumerName    = "channel"
	imageMetricKindCounter     = "counter"
	imageMetricKindGauge       = "gauge"
	imageMetricKindHistogram   = "histogram"
	imageMetricValueProperty   = "metric_value"
	imageMetricKindProperty    = "metric_kind"
)

type imageWorkerObservability struct {
	WorkerID string
	Provider string
	Model    string
	Now      func() time.Time
}

func (o imageWorkerObservability) now() time.Time {
	if o.Now != nil {
		return o.Now().UTC()
	}
	return time.Now().UTC()
}

func (o imageWorkerObservability) emit(ctx context.Context, name, status, kind string, value float64, job imagegen.GenerationRecord, duration time.Duration, properties map[string]any) {
	if properties == nil {
		properties = make(map[string]any)
	}
	properties[imageMetricKindProperty] = kind
	properties[imageMetricValueProperty] = value
	if o.WorkerID != "" {
		properties["worker_id"] = o.WorkerID
	}
	if job.BatchID != "" {
		properties["batch_id"] = job.BatchID
	}
	if job.Attempts != 0 {
		properties["attempt"] = job.Attempts
	}
	if job.Provider != "" {
		properties["provider"] = job.Provider
	} else if o.Provider != "" {
		properties["provider"] = o.Provider
	}
	telemetry.Emit(ctx, telemetry.Event{
		Name: name, Category: telemetry.CategorySystem, Source: imageWorkerTelemetrySource, Status: status,
		TenantID: job.TenantID, UserID: job.UserID, SessionID: job.SessionID,
		ResourceType: "image_generation", ResourceID: job.GenerationID,
		Model: firstNonEmptyChannel(job.Model, o.Model), DurationMS: duration.Milliseconds(), Properties: properties,
	})
}

func (o imageWorkerObservability) observeQueue(ctx context.Context, tenantID uint64, stats mysqlstore.ImageGenerationQueueStats) {
	job := imagegen.GenerationRecord{TenantID: tenantID}
	o.emit(ctx, imageMetricQueueDepth, telemetry.StatusOK, imageMetricKindGauge, float64(stats.QueueDepth), job, 0, nil)
	o.emit(ctx, imageMetricRunning, telemetry.StatusOK, imageMetricKindGauge, float64(stats.Running), job, 0, nil)
	age := time.Duration(0)
	if stats.OldestQueuedAt != nil && stats.OldestQueuedAt.Before(o.now()) {
		age = o.now().Sub(stats.OldestQueuedAt.UTC())
	}
	o.emit(ctx, imageMetricOldestAge, telemetry.StatusOK, imageMetricKindGauge, age.Seconds(), job, age, nil)
}

func (o imageWorkerObservability) observeClaims(ctx context.Context, jobs []imagegen.GenerationRecord) {
	for _, job := range jobs {
		wait := time.Duration(0)
		if !job.CreatedAt.IsZero() && job.CreatedAt.Before(o.now()) {
			wait = o.now().Sub(job.CreatedAt.UTC())
		}
		o.emit(ctx, imageMetricClaims, telemetry.StatusOK, imageMetricKindCounter, 1, job, wait, map[string]any{"queue_wait_seconds": wait.Seconds()})
	}
}

func (o imageWorkerObservability) observeAttempt(ctx context.Context, job imagegen.GenerationRecord, status, errorClass string, duration time.Duration) {
	properties := map[string]any{"outcome": status}
	if errorClass != "" {
		properties["error_class"] = errorClass
	}
	eventStatus := telemetry.StatusOK
	if status == imagegen.GenerationStatusFailed || status == imagegen.GenerationStatusDead || status == imagegen.GenerationStatusOutcomeUnknown {
		eventStatus = telemetry.StatusError
	}
	o.emit(ctx, imageMetricAttempts, eventStatus, imageMetricKindCounter, 1, job, duration, properties)
	switch status {
	case imagegen.GenerationStatusCompleted:
		o.emit(ctx, imageMetricCompleted, telemetry.StatusOK, imageMetricKindCounter, 1, job, duration, nil)
	case imagegen.GenerationStatusRetry:
		o.emit(ctx, imageMetricRetries, telemetry.StatusOK, imageMetricKindCounter, 1, job, duration, properties)
	case imagegen.GenerationStatusFailed, imagegen.GenerationStatusDead, imagegen.GenerationStatusOutcomeUnknown:
		o.emit(ctx, imageMetricFailed, telemetry.StatusError, imageMetricKindCounter, 1, job, duration, properties)
	}
}

func (o imageWorkerObservability) observeLeaseLost(ctx context.Context, job imagegen.GenerationRecord) {
	o.emit(ctx, imageMetricLeaseLost, telemetry.StatusError, imageMetricKindCounter, 1, job, 0, nil)
}

func (o imageWorkerObservability) observeStaleRecovery(ctx context.Context, tenantID uint64, count int) {
	if count <= 0 {
		return
	}
	o.emit(ctx, imageMetricStaleReclaimed, telemetry.StatusOK, imageMetricKindCounter, float64(count), imagegen.GenerationRecord{TenantID: tenantID}, 0, nil)
}

type observedImageWorkerProvider struct {
	provider imagegen.ImagesProvider
	observer imageWorkerObservability
}

func (p observedImageWorkerProvider) Generate(ctx context.Context, request imagegen.ProviderGenerateRequest) (imagegen.ProviderImage, error) {
	started := time.Now()
	result, err := p.provider.Generate(ctx, request)
	p.observe(ctx, request.Provider, request.Model, err, time.Since(started))
	return result, err
}

func (p observedImageWorkerProvider) Edit(ctx context.Context, request imagegen.ProviderEditRequest) (imagegen.ProviderImage, error) {
	started := time.Now()
	result, err := p.provider.Edit(ctx, request)
	p.observe(ctx, request.Provider, request.Model, err, time.Since(started))
	return result, err
}

func (p observedImageWorkerProvider) observe(ctx context.Context, provider, model string, err error, duration time.Duration) {
	status := telemetry.StatusOK
	outcome := imagegen.GenerationStatusCompleted
	provider = firstNonEmptyChannel(provider, p.observer.Provider)
	properties := map[string]any{"provider": provider, "outcome": outcome}
	if err != nil {
		status = telemetry.StatusError
		outcome = imagegen.ProviderErrorClass(err)
		properties["outcome"] = outcome
	}
	job := imagegen.GenerationRecord{Provider: provider, Model: firstNonEmptyChannel(model, p.observer.Model)}
	p.observer.emit(ctx, imageMetricProviderDuration, status, imageMetricKindHistogram, duration.Seconds(), job, duration, properties)
}

type observedImageCompletionConsumer struct {
	consumer imagegen.ImageCompletionConsumer
	observer imageWorkerObservability
}

func (c observedImageCompletionConsumer) ConsumeImageCompletion(ctx context.Context, event imagegen.CompletionEvent) error {
	lag := time.Duration(0)
	if !event.CreatedAt.IsZero() && event.CreatedAt.Before(c.observer.now()) {
		lag = c.observer.now().Sub(event.CreatedAt.UTC())
	}
	job := imagegen.GenerationRecord{TenantID: event.TenantID, GenerationID: event.GenerationID}
	properties := map[string]any{"consumer": imageWorkerConsumerName}
	c.observer.emit(ctx, imageMetricCompletionLag, telemetry.StatusOK, imageMetricKindHistogram, lag.Seconds(), job, lag, properties)
	err := c.consumer.ConsumeImageCompletion(ctx, event)
	if err != nil {
		c.observer.emit(ctx, imageMetricCompletionFailed, telemetry.StatusError, imageMetricKindCounter, 1, job, 0, properties)
	}
	return err
}

type observedImageWorkerRepository struct {
	*mysqlstore.GormRepository
	observer                 imageWorkerObservability
	mu                       sync.Mutex
	lastQueueObservation     time.Time
	queueObservationInterval time.Duration
	claimMu                  sync.Mutex
	claims                   map[imageWorkerClaimKey]imagegen.GenerationRecord
}

type imageWorkerClaimKey struct {
	tenantID     uint64
	generationID string
}

func (r *observedImageWorkerRepository) ClaimDueImageGenerations(ctx context.Context, tenantID uint64, workerID string, limit int, leaseUntil time.Time) ([]imagegen.GenerationRecord, error) {
	r.observeQueueIfDue(ctx, tenantID)
	jobs, err := r.GormRepository.ClaimDueImageGenerations(ctx, tenantID, workerID, limit, leaseUntil)
	if err == nil {
		r.rememberClaims(jobs)
		r.observer.observeClaims(ctx, jobs)
	}
	return jobs, err
}

func (r *observedImageWorkerRepository) rememberClaims(jobs []imagegen.GenerationRecord) {
	r.claimMu.Lock()
	defer r.claimMu.Unlock()
	if r.claims == nil {
		r.claims = make(map[imageWorkerClaimKey]imagegen.GenerationRecord)
	}
	for _, job := range jobs {
		key := imageWorkerClaimKey{tenantID: job.TenantID, generationID: job.GenerationID}
		r.claims[key] = imagegen.GenerationRecord{
			TenantID: job.TenantID, UserID: job.UserID, SessionID: job.SessionID,
			GenerationID: job.GenerationID, BatchID: job.BatchID,
			LeaseOwner: job.LeaseOwner, Attempts: job.Attempts, Provider: job.Provider, Model: job.Model,
			CreatedAt: job.CreatedAt,
		}
	}
}

func (r *observedImageWorkerRepository) attemptJob(attempt imagegen.ImageGenerationAttemptFinish) imagegen.GenerationRecord {
	r.claimMu.Lock()
	defer r.claimMu.Unlock()
	job := r.claims[imageWorkerClaimKey{tenantID: attempt.TenantID, generationID: attempt.GenerationID}]
	job.TenantID = attempt.TenantID
	job.GenerationID = attempt.GenerationID
	job.Attempts = attempt.AttemptNo
	job.LeaseOwner = attempt.WorkerID
	return job
}

func (r *observedImageWorkerRepository) forgetClaim(job imagegen.GenerationRecord) {
	r.claimMu.Lock()
	defer r.claimMu.Unlock()
	delete(r.claims, imageWorkerClaimKey{tenantID: job.TenantID, generationID: job.GenerationID})
}

func (r *observedImageWorkerRepository) observeQueueIfDue(ctx context.Context, tenantID uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.observer.now()
	interval := r.queueObservationInterval
	if interval <= 0 {
		interval = config.DefaultImageWorkerHeartbeatSeconds * time.Second
	}
	if !r.lastQueueObservation.IsZero() && now.Sub(r.lastQueueObservation) < interval {
		return
	}
	stats, err := r.GormRepository.ImageGenerationQueueStats(ctx, tenantID)
	if err != nil {
		return
	}
	r.lastQueueObservation = now
	r.observer.observeQueue(ctx, tenantID, stats)
}

func (r *observedImageWorkerRepository) FinalizeExhaustedImageGenerations(ctx context.Context, tenantID uint64, now time.Time) (int, error) {
	count, err := r.GormRepository.FinalizeExhaustedImageGenerations(ctx, tenantID, now)
	if err == nil {
		r.observer.observeStaleRecovery(ctx, tenantID, count)
	}
	return count, err
}

func (r *observedImageWorkerRepository) RenewImageGenerationLease(ctx context.Context, tenantID uint64, generationID, workerID string, leaseUntil time.Time) (bool, error) {
	cancelled, err := r.GormRepository.RenewImageGenerationLease(ctx, tenantID, generationID, workerID, leaseUntil)
	if errors.Is(err, imagegen.ErrImageGenerationLeaseLost) {
		job := r.attemptJob(imagegen.ImageGenerationAttemptFinish{TenantID: tenantID, GenerationID: generationID, WorkerID: workerID})
		r.observer.observeLeaseLost(ctx, job)
		r.forgetClaim(job)
	}
	return cancelled, err
}

func (r *observedImageWorkerRepository) TransitionClaimedImageAttempt(ctx context.Context, input imagegen.ImageGenerationAttemptTransition) (string, error) {
	status, err := r.GormRepository.TransitionClaimedImageAttempt(ctx, input)
	job := r.attemptJob(input.Attempt)
	defer r.forgetClaim(job)
	if errors.Is(err, imagegen.ErrImageGenerationLeaseLost) {
		r.observer.observeLeaseLost(ctx, job)
	} else if err == nil {
		r.observer.observeAttempt(ctx, job, status, input.Attempt.ErrorClass, time.Duration(input.Attempt.DurationMS)*time.Millisecond)
	}
	return status, err
}

func (r *observedImageWorkerRepository) CompleteClaimedImageAttempt(ctx context.Context, input imagegen.CompleteClaimedImageAttemptRequest) (string, error) {
	status, err := r.GormRepository.CompleteClaimedImageAttempt(ctx, input)
	job := r.attemptJob(input.Attempt)
	defer r.forgetClaim(job)
	if errors.Is(err, imagegen.ErrImageGenerationLeaseLost) {
		r.observer.observeLeaseLost(ctx, job)
	} else if err == nil {
		r.observer.observeAttempt(ctx, job, status, input.Attempt.ErrorClass, time.Duration(input.Attempt.DurationMS)*time.Millisecond)
	}
	return status, err
}

type channelImageGenerationRuntime struct {
	Generator  imagegen.Generator
	Scheduler  imagegen.Scheduler
	BlobStore  imagegen.BlobStore
	MediaStore media.Store
	History    server.ImageGenerationHistoryStore
	Async      bool
}

// mysqlImageGenerationStore adapts the typed MySQL repository records to the
// provider-neutral records consumed by imagegen.Service.
type mysqlImageGenerationStore struct {
	repo *mysqlstore.GormRepository
}

func (s mysqlImageGenerationStore) FindImageGenerationByIdempotency(ctx context.Context, tenantID, userID, sessionID uint64, key string) (imagegen.GenerationRecord, error) {
	record, err := s.repo.FindImageGenerationByIdempotency(ctx, tenantID, userID, sessionID, key)
	return imageGenerationRecordFromMySQL(record), err
}

func (s mysqlImageGenerationStore) CreateImageGeneration(ctx context.Context, record imagegen.GenerationRecord) (imagegen.GenerationRecord, error) {
	created, err := s.repo.CreateImageGeneration(ctx, mysqlstore.ImageGenerationInput{
		ID: record.ID, GenerationID: record.GenerationID, TenantID: record.TenantID, UserID: record.UserID, SessionID: record.SessionID,
		AssetID: record.AssetID, SourceAssetID: record.SourceAssetID, Operation: record.Operation, Status: record.Status,
		Prompt: record.Prompt, Provider: record.Provider, Model: record.Model, RequestJSON: record.RequestJSON,
		ErrorCode: record.ErrorCode, ErrorMessage: record.ErrorMessage, TraceID: record.TraceID, IdempotencyKey: record.IdempotencyKey,
		CreatedAt: record.CreatedAt, FinishedAt: record.FinishedAt,
	})
	return imageGenerationRecordFromMySQL(created), err
}

func (s mysqlImageGenerationStore) UpdateImageGenerationStatus(ctx context.Context, tenantID, userID, sessionID uint64, generationID, status, errorCode, errorMessage string, finishedAt *time.Time) error {
	return s.repo.UpdateImageGenerationStatus(ctx, tenantID, userID, sessionID, generationID, status, errorCode, errorMessage, finishedAt)
}

func (s mysqlImageGenerationStore) SetImageGenerationAsset(ctx context.Context, tenantID, userID, sessionID uint64, generationID, assetID string) error {
	return s.repo.SetImageGenerationAsset(ctx, tenantID, userID, sessionID, generationID, assetID)
}

type mysqlImageGenerationHistoryStore struct {
	repo *mysqlstore.GormRepository
}

func (s mysqlImageGenerationHistoryStore) ListImageGenerations(ctx context.Context, tenantID, userID, sessionID uint64, limit int) ([]imagegen.GenerationRecord, error) {
	rows, err := s.repo.ListImageGenerations(ctx, tenantID, userID, sessionID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]imagegen.GenerationRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, imageGenerationRecordFromMySQL(row))
	}
	return out, nil
}

func imageGenerationRecordFromMySQL(row mysqlstore.ImageGenerationRecord) imagegen.GenerationRecord {
	return imagegen.GenerationRecord{
		ID: row.ID, GenerationID: row.GenerationID, TenantID: row.TenantID, UserID: row.UserID, SessionID: row.SessionID,
		AssetID: row.AssetID, SourceAssetID: row.SourceAssetID, Operation: row.Operation, Status: row.Status,
		Prompt: row.Prompt, Provider: row.Provider, Model: row.Model, RequestJSON: row.RequestJSON,
		ErrorCode: row.ErrorCode, ErrorMessage: row.ErrorMessage, TraceID: row.TraceID, IdempotencyKey: row.IdempotencyKey,
		CreatedAt: row.CreatedAt, FinishedAt: row.FinishedAt,
	}
}

// configureImageGeneration wires the real provider and persistence boundaries
// for the HTTP server. A disabled feature is a valid no-op; an enabled but
// invalid configuration fails startup so the WebUI never advertises a broken
// image panel.
func configureImageGeneration(cwd string, repo *mysqlstore.GormRepository) (imagegen.Generator, imagegen.BlobStore, media.Store, server.ImageGenerationHistoryStore, error) {
	return configureImageGenerationWithSettingsInputs(cwd, nil, repo)
}

func configureImageGenerationWithSettingsInputs(cwd string, settingsInputs []string, repo *mysqlstore.GormRepository) (imagegen.Generator, imagegen.BlobStore, media.Store, server.ImageGenerationHistoryStore, error) {
	settings, err := resolveImageGenerationSettingsInputs(cwd, settingsInputs)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	resolved, err := config.ResolveImageGenerationWithSettings(cwd, settings)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return configureImageGenerationWithResolved(cwd, resolved, repo)
}

func resolveImageGenerationSettingsInputs(cwd string, settingsInputs []string) (config.Settings, error) {
	settings := config.LoadSettings(cwd).Settings
	for _, input := range settingsInputs {
		override, err := loadSettingsInput(cwd, input)
		if err != nil {
			return config.Settings{}, err
		}
		config.AnnotatePermissionSources(&override, "cli-settings")
		settings = config.MergeSettings(settings, override)
	}
	return settings, nil
}

func resolveImageCatalogWithSettingsInputs(cwd string, settingsInputs []string) ([]config.ResolvedImageModel, error) {
	settings, err := resolveImageGenerationSettingsInputs(cwd, settingsInputs)
	if err != nil {
		return nil, err
	}
	resolved, err := config.ResolveImageGenerationWithSettings(cwd, settings)
	if err != nil {
		return nil, err
	}
	return append([]config.ResolvedImageModel(nil), resolved.Catalog...), nil
}

func configureImageGenerationWithResolved(cwd string, resolved config.ResolvedImageGeneration, repo *mysqlstore.GormRepository) (imagegen.Generator, imagegen.BlobStore, media.Store, server.ImageGenerationHistoryStore, error) {
	if !resolved.Enabled && repo == nil {
		return nil, nil, nil, nil, nil
	}
	if repo == nil {
		return nil, nil, nil, nil, errors.New("image generation requires tenant persistence storage")
	}
	blobs, err := imagegen.NewFilesystemBlobStore(imageGenerationMediaRoot(cwd))
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if !resolved.Enabled {
		// Chat image attachments need durable storage even when generation is disabled.
		return nil, blobs, repo, nil, nil
	}
	provider := newResolvedImageProviderRegistry(resolved)
	service := imagegen.NewService(imagegen.ServiceConfig{
		Provider: provider, BlobStore: blobs, MediaStore: repo, GenerationStore: mysqlImageGenerationStore{repo: repo},
		ProviderName: resolved.Provider, Model: resolved.Model, Quality: resolved.Quality, Size: resolved.Size,
		OutputFormat: resolved.OutputFormat, Background: resolved.Background, MaxPromptChars: resolved.MaxPromptChars,
		MaxInputBytes: resolved.MaxInputBytes, Timeout: time.Duration(resolved.TimeoutSeconds) * time.Second, Enabled: resolved.Enabled,
	})
	return service, blobs, repo, mysqlImageGenerationHistoryStore{repo: repo}, nil
}

func configureChannelImageGeneration(cwd string, repo *mysqlstore.GormRepository, accountKey string) (channelImageGenerationRuntime, error) {
	resolved, err := config.ResolveImageGeneration(cwd)
	if err != nil {
		return channelImageGenerationRuntime{}, err
	}
	generator, blobs, mediaStore, history, err := configureImageGenerationWithResolved(cwd, resolved, repo)
	if err != nil {
		return channelImageGenerationRuntime{}, err
	}
	runtime := channelImageGenerationRuntime{Generator: generator, BlobStore: blobs, MediaStore: mediaStore, History: history}
	// The standalone image worker is currently MySQL-backed. Keep SQLite
	// desktop channels on the synchronous path so an accepted image request
	// never lands in a queue with no consumer.
	if !resolved.Enabled || !resolved.AsyncChannelEnabledForAccount(accountKey) || repo == nil || repo.IsSQLite() {
		return runtime, nil
	}
	runtime.Scheduler = imagegen.NewScheduler(imagegen.SchedulerConfig{
		Repository: repo, MediaStore: repo, ProviderName: resolved.Provider, Model: resolved.Model,
		Quality: resolved.Quality, Size: resolved.Size, OutputFormat: resolved.OutputFormat, Background: resolved.Background,
		MaxPromptChars: resolved.MaxPromptChars, MaxQueuedPerTenant: resolved.Worker.MaxQueuedPerTenant,
		MaxQueuedPerUser: resolved.Worker.MaxQueuedPerUser, MaxAttempts: uint(resolved.Worker.MaxAttempts),
	})
	runtime.Async = true
	return runtime, nil
}

func configureImageWorkerRuntime(cwd string, resolved config.ResolvedImageGeneration, repo *mysqlstore.GormRepository, tenantID uint64, workerID string, onReady func(imageWorkerReadyState) error) (imageWorkerRunner, error) {
	if !resolved.Enabled || repo == nil || tenantID == 0 || strings.TrimSpace(workerID) == "" {
		return nil, imagegen.ErrInvalidImageWorkerConfig
	}
	if err := resolved.Validate(); err != nil {
		return nil, err
	}
	blobs, err := imagegen.NewFilesystemBlobStore(imageGenerationMediaRoot(cwd))
	if err != nil {
		return nil, err
	}
	provider := newResolvedImageProviderRegistry(resolved)
	observer := imageWorkerObservability{WorkerID: workerID, Provider: resolved.Provider, Model: resolved.Model}
	observedRepository := &observedImageWorkerRepository{GormRepository: repo, observer: observer}
	observedProvider := observedImageWorkerProvider{provider: provider, observer: observer}
	attemptTimeout := time.Duration(resolved.TimeoutSeconds) * time.Second
	heartbeatInterval := time.Duration(resolved.Worker.HeartbeatSeconds) * time.Second
	leaseDuration := time.Duration(resolved.Worker.LeaseSeconds) * time.Second
	finalizeTimeout := time.Duration(resolved.Worker.FinalizeTimeoutSeconds) * time.Second
	executor := imagegen.NewExecutor(imagegen.ExecutorConfig{
		Repository: observedRepository, Provider: observedProvider, BlobStore: blobs, MediaStore: observedRepository, ProviderName: "",
		AttemptTimeout: attemptTimeout, HeartbeatInterval: heartbeatInterval, LeaseDuration: leaseDuration,
		FinalizeTimeout: finalizeTimeout, MaxInputBytes: resolved.MaxInputBytes,
	})
	consumer := channelruntime.NewChannelImageCompletionConsumer(observedRepository)
	observedConsumer := observedImageCompletionConsumer{consumer: consumer, observer: observer}
	dispatcher := imagegen.NewCompletionDispatcher(imagegen.CompletionDispatcherConfig{
		Repository: observedRepository,
		Consumer:   completionConsumerWithTimeout{consumer: observedConsumer, timeout: finalizeTimeout},
		TenantID:   tenantID, WorkerID: workerID + ":completion",
		PollInterval: time.Duration(resolved.Worker.PollIntervalMS) * time.Millisecond,
	})
	worker := imagegen.NewWorker(imagegen.ImageWorkerConfig{
		Repository: observedRepository, Executor: executor, CompletionRunner: dispatcher, BlobStore: blobs,
		TenantID: tenantID, WorkerID: workerID,
		PollInterval:  time.Duration(resolved.Worker.PollIntervalMS) * time.Millisecond,
		LeaseDuration: leaseDuration, MaxConcurrent: resolved.Worker.MaxConcurrent, AttemptTimeout: attemptTimeout,
		StartupTimeout: finalizeTimeout,
		OnReady: func(recovery imagegen.ImageWorkerStartupRecoveryResult) error {
			observer.observeStaleRecovery(context.Background(), tenantID, recovery.Legacy.Updated)
			if onReady == nil {
				return nil
			}
			return onReady(imageWorkerReadyState{
				LegacyCandidates: recovery.Legacy.Candidates, LegacyUpdated: recovery.Legacy.Updated, LegacyRemaining: recovery.Legacy.Remaining,
				OrphanBlobsScanned: recovery.BlobGC.Scanned, OrphanBlobsDeleted: recovery.BlobGC.Deleted, OrphanBlobDeleteFailures: recovery.BlobGC.Failed,
			})
		},
	})
	return worker, nil
}

func newResolvedImageProviderRegistry(resolved config.ResolvedImageGeneration) *imagegen.ProviderRegistry {
	providers := make(map[string]imagegen.ImagesProvider)
	entries := resolved.Providers
	if len(entries) == 0 {
		entries = []config.ResolvedImageProvider{{Name: resolved.Provider, BaseURL: resolved.BaseURL, APIKey: resolved.APIKey, AuthToken: resolved.AuthToken}}
	}
	protocolByName := make(map[string]string, len(resolved.Catalog))
	for _, model := range resolved.Catalog {
		protocolByName[strings.ToLower(strings.TrimSpace(model.Provider))] = model.ImageProtocol
	}
	for _, entry := range entries {
		protocol := entry.ImageProtocol
		if protocol == "" {
			protocol = protocolByName[strings.ToLower(strings.TrimSpace(entry.Name))]
		}
		providers[entry.Name] = imagegen.NewOpenAICompatibleImagesProvider(imagegen.ProviderConfig{
			BaseURL: entry.BaseURL, APIKey: entry.APIKey, AuthToken: entry.AuthToken, ImageProtocol: protocol,
			AllowRemoteURLs: strings.EqualFold(protocol, imagegen.ImageProtocolAgnes), RemoteImageHosts: entry.ImageResultHosts,
			Timeout: time.Duration(resolved.TimeoutSeconds) * time.Second, MaxRetries: 1,
		})
	}
	capabilities := make([]imagegen.ProviderModelCapability, 0, len(resolved.Catalog))
	for _, entry := range resolved.Catalog {
		capabilities = append(capabilities, imagegen.ProviderModelCapability{
			Provider: entry.Provider, Model: entry.Model, Operations: entry.Capability.Operations,
			Resolutions: entry.Capability.Resolutions, AspectRatios: entry.Capability.AspectRatios,
			Sizes: entry.Capability.Sizes, QualityOptions: entry.Capability.QualityOptions,
			OutputFormats: entry.Capability.OutputFormats, SupportsWatermark: entry.Capability.SupportsWatermark,
		})
	}
	return imagegen.NewProviderRegistry(resolved.Provider, providers).WithCapabilities(capabilities)
}

type completionConsumerWithTimeout struct {
	consumer imagegen.ImageCompletionConsumer
	timeout  time.Duration
}

func (c completionConsumerWithTimeout) ConsumeImageCompletion(ctx context.Context, event imagegen.CompletionEvent) error {
	timeout := c.timeout
	if timeout <= 0 {
		timeout = imagegen.DefaultImageFinalizeTimeout
	}
	deliveryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return c.consumer.ConsumeImageCompletion(deliveryCtx, event)
}

func imageGenerationMediaRoot(cwd string) string {
	root := filepath.Join(cwd, ".niuma", "media")
	if path, err := config.GlobalSettingsPath(); err == nil && strings.TrimSpace(path) != "" {
		root = filepath.Join(filepath.Dir(path), "media")
	}
	return root
}
