package imagegen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/konglong87/super-niuma/internal/media"
)

const (
	DefaultImageAttemptTimeout    = 180 * time.Second
	DefaultImageHeartbeatInterval = 15 * time.Second
	DefaultImageFinalizeTimeout   = 10 * time.Second
	DefaultImageLeaseGrace        = 60 * time.Second
	DefaultImageMaxInputBytes     = 25 * 1024 * 1024
	DefaultImageMaxOutputBytes    = 60 * 1024 * 1024
	DefaultImageWorkerPoll        = 500 * time.Millisecond
	DefaultImageWorkerConcurrency = 2

	ImageAttemptOutcomeCompleted = GenerationStatusCompleted

	ErrorClassInvalidJobRequest  = "invalid_job_request"
	ErrorClassImageExecution     = "image_execution_failed"
	ErrorClassImagePersistence   = "image_persistence_failed"
	ErrorClassWorkerShutdown     = "worker_shutdown"
	ErrorClassWorkerLeaseExpired = "worker_lease_expired"
)

var (
	ErrInvalidImageWorkerConfig = errors.New("invalid image worker configuration")
	ErrImageGenerationLeaseLost = errors.New("image generation lease lost")
	errImageJobCancelled        = errors.New("image generation cancellation requested")
	errImageAttemptTimeout      = errors.New("image generation attempt timeout")
)

// ImageGenerationAttemptFinish is the provider-neutral attempt ledger update.
// WorkerID fences it to the attempt created by the claiming worker.
type ImageGenerationAttemptFinish struct {
	TenantID          uint64
	GenerationID      string
	AttemptNo         uint
	WorkerID          string
	FinishedAt        time.Time
	DurationMS        uint64
	ProviderRequestID string
	Outcome           string
	ErrorClass        string
	ErrorMessage      string
}

// CompleteImageGenerationRequest carries the minimum owner-fenced success
// transition. The repository owns generation linkage and completion outbox
// creation in one transaction.
type CompleteImageGenerationRequest struct {
	TenantID     uint64
	GenerationID string
	WorkerID     string
	AssetID      string
	EventType    string
}

type ImageGenerationAttemptTransition struct {
	Attempt       ImageGenerationAttemptFinish
	Status        string
	NextAttemptAt time.Time
}

type CompleteClaimedImageAttemptRequest struct {
	Attempt   ImageGenerationAttemptFinish
	AssetID   string
	EventType string
}

// ImageWorkerRepository is the durable boundary shared by Executor and
// Worker. Every update of a running generation carries its lease owner.
type ImageWorkerRepository interface {
	ClaimDueImageGenerations(context.Context, uint64, string, int, time.Time) ([]GenerationRecord, error)
	FinalizeExhaustedImageGenerations(context.Context, uint64, time.Time) (int, error)
	RenewImageGenerationLease(context.Context, uint64, string, string, time.Time) (cancelRequested bool, err error)
	TransitionClaimedImageAttempt(context.Context, ImageGenerationAttemptTransition) (string, error)
	CompleteClaimedImageAttempt(context.Context, CompleteClaimedImageAttemptRequest) (string, error)
}

type ExecutorConfig struct {
	Repository        ImageWorkerRepository
	Provider          ImagesProvider
	BlobStore         BlobStore
	MediaStore        media.Store
	ProviderName      string
	AttemptTimeout    time.Duration
	HeartbeatInterval time.Duration
	LeaseDuration     time.Duration
	FinalizeTimeout   time.Duration
	MaxInputBytes     int64
	MaxOutputBytes    int64
	Now               func() time.Time
	Jitter            func(time.Duration) time.Duration
}

type Executor struct{ cfg ExecutorConfig }

type ClaimedImageExecutor interface {
	ExecuteClaimed(context.Context, GenerationRecord) error
}

// Runner is a lifecycle-only sidecar port. ImageWorker uses it for completion
// dispatch without importing channel materialization packages.
type Runner interface {
	Run(context.Context) error
}

type ImageWorkerConfig struct {
	Repository                ImageWorkerRepository
	Executor                  ClaimedImageExecutor
	CompletionRunner          Runner
	BlobStore                 BlobStore
	TenantID                  uint64
	WorkerID                  string
	PollInterval              time.Duration
	LeaseDuration             time.Duration
	MaxConcurrent             int
	AttemptTimeout            time.Duration
	LegacyReconciliationGrace time.Duration
	StartupTimeout            time.Duration
	OrphanBlobRetention       time.Duration
	OrphanBlobGrace           time.Duration
	Now                       func() time.Time
	OnError                   func(error)
	OnReady                   func(ImageWorkerStartupRecoveryResult) error
}

type ImageWorker struct{ cfg ImageWorkerConfig }

func NewExecutor(cfg ExecutorConfig) *Executor {
	if cfg.AttemptTimeout <= 0 {
		cfg.AttemptTimeout = DefaultImageAttemptTimeout
	}
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = DefaultImageHeartbeatInterval
	}
	if cfg.LeaseDuration <= 0 {
		cfg.LeaseDuration = cfg.AttemptTimeout + DefaultImageLeaseGrace
	}
	if cfg.FinalizeTimeout <= 0 {
		cfg.FinalizeTimeout = DefaultImageFinalizeTimeout
	}
	if cfg.MaxInputBytes <= 0 {
		cfg.MaxInputBytes = DefaultImageMaxInputBytes
	}
	if cfg.MaxOutputBytes <= 0 {
		cfg.MaxOutputBytes = DefaultImageMaxOutputBytes
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Jitter == nil {
		cfg.Jitter = imageRetryJitter
	}
	return &Executor{cfg: cfg}
}

func NewWorker(cfg ImageWorkerConfig) *ImageWorker {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = DefaultImageWorkerPoll
	}
	if cfg.LeaseDuration <= 0 {
		cfg.LeaseDuration = DefaultImageAttemptTimeout + DefaultImageLeaseGrace
	}
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = DefaultImageWorkerConcurrency
	}
	if cfg.AttemptTimeout <= 0 {
		cfg.AttemptTimeout = DefaultImageAttemptTimeout
	}
	if cfg.LegacyReconciliationGrace <= 0 {
		cfg.LegacyReconciliationGrace = DefaultImageLeaseGrace
	}
	if cfg.StartupTimeout <= 0 {
		cfg.StartupTimeout = DefaultImageStartupTimeout
	}
	if cfg.OrphanBlobRetention <= 0 {
		cfg.OrphanBlobRetention = DefaultImageOrphanBlobRetention
	}
	if cfg.OrphanBlobGrace <= 0 {
		cfg.OrphanBlobGrace = DefaultImageOrphanBlobGrace
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &ImageWorker{cfg: cfg}
}

// Run stops claiming when ctx ends, then waits for every claimed attempt to
// finish. Attempt cancellation remains explicit through the repository flag;
// process shutdown does not silently turn accepted jobs into cancellations.
func (w *ImageWorker) Run(ctx context.Context) error {
	if w == nil || w.cfg.Repository == nil || w.cfg.Executor == nil || w.cfg.TenantID == 0 || strings.TrimSpace(w.cfg.WorkerID) == "" {
		return ErrInvalidImageWorkerConfig
	}
	if ctx.Err() != nil {
		return nil
	}
	recovery, err := w.RunStartupRecovery(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	if w.cfg.OnReady != nil {
		if err := w.cfg.OnReady(recovery); err != nil {
			return err
		}
	}
	runCtx, cancelRun := context.WithCancel(ctx)
	var completionWG sync.WaitGroup
	var completionErr <-chan error
	if w.cfg.CompletionRunner != nil {
		errCh := make(chan error, 1)
		completionErr = errCh
		completionWG.Add(1)
		go func() {
			defer completionWG.Done()
			errCh <- w.cfg.CompletionRunner.Run(runCtx)
		}()
	}
	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()
	semaphore := make(chan struct{}, w.cfg.MaxConcurrent)
	var inFlight sync.WaitGroup
	defer func() {
		cancelRun()
		inFlight.Wait()
		completionWG.Wait()
	}()

	for {
		select {
		case <-runCtx.Done():
			return nil
		case err := <-completionErr:
			if err == nil {
				if runCtx.Err() != nil {
					return nil
				}
				return errors.New("image completion runner stopped unexpectedly")
			}
			return err
		default:
		}
		available := cap(semaphore) - len(semaphore)
		if available > 0 {
			if _, err := w.cfg.Repository.FinalizeExhaustedImageGenerations(runCtx, w.cfg.TenantID, w.cfg.Now().UTC()); err != nil {
				if runCtx.Err() != nil {
					return nil
				}
				return err
			}
			jobs, err := w.cfg.Repository.ClaimDueImageGenerations(runCtx, w.cfg.TenantID, strings.TrimSpace(w.cfg.WorkerID), available, w.cfg.Now().UTC().Add(w.cfg.LeaseDuration))
			if err != nil {
				if runCtx.Err() != nil {
					return nil
				}
				return err
			}
			for _, job := range jobs {
				semaphore <- struct{}{}
				inFlight.Add(1)
				go func(claimed GenerationRecord) {
					defer inFlight.Done()
					defer func() { <-semaphore }()
					if err := w.cfg.Executor.ExecuteClaimed(runCtx, claimed); err != nil && w.cfg.OnError != nil {
						w.cfg.OnError(err)
					}
				}(job)
			}
		}
		select {
		case <-runCtx.Done():
			return nil
		case err := <-completionErr:
			if err == nil {
				if runCtx.Err() != nil {
					return nil
				}
				return errors.New("image completion runner stopped unexpectedly")
			}
			return err
		case <-ticker.C:
		}
	}
}

func (e *Executor) ExecuteClaimed(ctx context.Context, job GenerationRecord) error {
	if err := e.validateConfig(); err != nil {
		return err
	}
	startedAt := e.cfg.Now().UTC()
	if err := validateClaimedImageJob(job, e.cfg.ProviderName); err != nil {
		return e.finishFailure(ctx, job, startedAt, err, ErrorClassInvalidJobRequest, false, "")
	}

	attemptBase, cancelAttempt := context.WithCancelCause(ctx)
	defer cancelAttempt(nil)
	attemptCtx, cancelTimeout := context.WithTimeoutCause(attemptBase, e.cfg.AttemptTimeout, errImageAttemptTimeout)
	defer cancelTimeout()

	renewCtx, cancelRenew := e.newFinalizeContext(ctx)
	cancelRequested, err := e.renewLease(renewCtx, job)
	cancelRenew()
	if err != nil {
		if ctx.Err() != nil {
			return e.finishLifecycleShutdown(ctx, job, startedAt, false, err)
		}
		return err
	}
	if cancelRequested {
		return e.finishFailure(ctx, job, startedAt, errImageJobCancelled, ErrorClassCallerCancelled, true, "")
	}

	heartbeatCtx, stopHeartbeat := context.WithCancel(context.Background())
	heartbeatDone := make(chan error, 1)
	go e.heartbeat(heartbeatCtx, job, cancelAttempt, heartbeatDone)

	request, err := decodeClaimedImageRequest(job)
	if err != nil {
		stopHeartbeat()
		<-heartbeatDone
		return e.finishFailure(ctx, job, startedAt, err, ErrorClassInvalidJobRequest, false, "")
	}
	if attemptCtx.Err() != nil {
		stopHeartbeat()
		<-heartbeatDone
		return e.finishLifecycleShutdown(ctx, job, startedAt, false, attemptCtx.Err())
	}
	output, callErr := e.callProvider(attemptCtx, job, request)
	stopHeartbeat()
	heartbeatErr := <-heartbeatDone
	if heartbeatErr != nil {
		return heartbeatErr
	}
	if errors.Is(context.Cause(attemptBase), errImageJobCancelled) {
		return e.finishFailure(ctx, job, startedAt, errImageJobCancelled, ErrorClassCallerCancelled, true, "")
	}
	if ctx.Err() != nil && callErr != nil {
		return e.finishLifecycleShutdown(ctx, job, startedAt, true, callErr)
	}
	renewCtx, cancelRenew = e.newFinalizeContext(ctx)
	cancelRequested, renewErr := e.renewLease(renewCtx, job)
	cancelRenew()
	if renewErr != nil {
		return renewErr
	}
	if cancelRequested {
		return e.finishFailure(ctx, job, startedAt, errImageJobCancelled, ErrorClassCallerCancelled, true, output.ProviderRequestID)
	}
	if callErr != nil {
		class := ProviderErrorClass(callErr)
		requestID := ProviderErrorRequestID(callErr)
		if errors.Is(context.Cause(attemptCtx), errImageAttemptTimeout) {
			class = ErrorClassProviderTimeout
			callErr = providerFailure(class, callErr, false, true)
		}
		return e.finishFailure(ctx, job, startedAt, callErr, class, false, requestID)
	}
	persistCtx := attemptCtx
	cancelPersist := func() {}
	if ctx.Err() != nil {
		persistCtx, cancelPersist = e.newFinalizeContext(ctx)
	}
	defer cancelPersist()
	artifact, err := e.persistOutput(persistCtx, job, request, output)
	if err != nil {
		return e.finishFailure(ctx, job, startedAt, err, ErrorClassImagePersistence, false, output.ProviderRequestID)
	}
	finish := e.attemptFinish(job, startedAt, ImageAttemptOutcomeCompleted, "", "", output.ProviderRequestID)
	finalizeCtx, cancelFinalize := e.newFinalizeContext(ctx)
	defer cancelFinalize()
	status, err := e.cfg.Repository.CompleteClaimedImageAttempt(finalizeCtx, CompleteClaimedImageAttemptRequest{
		Attempt: finish, AssetID: artifact.AssetID, EventType: CompletionEventImageTerminal,
	})
	if err != nil {
		return err
	}
	if status == GenerationStatusCancelled {
		return e.deleteCancelledBlob(finalizeCtx, job, artifact)
	}
	return nil
}

func (e *Executor) validateConfig() error {
	if e == nil || e.cfg.Repository == nil || e.cfg.Provider == nil || e.cfg.BlobStore == nil || e.cfg.MediaStore == nil {
		return ErrInvalidImageWorkerConfig
	}
	return nil
}

func validateClaimedImageJob(job GenerationRecord, providerName string) error {
	if err := job.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidImageRequest, err)
	}
	if job.Status != GenerationStatusRunning || strings.TrimSpace(job.LeaseOwner) == "" || job.Attempts == 0 || job.MaxAttempts == 0 {
		return fmt.Errorf("%w: job must carry a running lease and attempt", ErrInvalidImageRequest)
	}
	if configured := strings.TrimSpace(providerName); configured != "" && configured != strings.TrimSpace(job.Provider) {
		return fmt.Errorf("%w: configured provider does not match job snapshot", ErrInvalidImageRequest)
	}
	return nil
}

func decodeClaimedImageRequest(job GenerationRecord) (normalizedJobRequest, error) {
	var request normalizedJobRequest
	decoder := json.NewDecoder(strings.NewReader(job.RequestJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return normalizedJobRequest{}, fmt.Errorf("%w: decode normalized request: %v", ErrInvalidImageRequest, err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return normalizedJobRequest{}, fmt.Errorf("%w: normalized request has trailing data", ErrInvalidImageRequest)
	}
	if request.Operation != job.Operation || strings.TrimSpace(request.Prompt) == "" || strings.TrimSpace(request.Model) == "" {
		return normalizedJobRequest{}, fmt.Errorf("%w: normalized request does not match job", ErrInvalidImageRequest)
	}
	for name, value := range map[string]string{"quality": request.Quality, "size": request.Size, "output_format": request.OutputFormat, "background": request.Background} {
		if !validOption(name, value) {
			return normalizedJobRequest{}, fmt.Errorf("%w: unsupported %s", ErrInvalidImageRequest, name)
		}
	}
	switch request.Operation {
	case OperationGenerate:
		if strings.TrimSpace(request.SourceAssetID) != "" || strings.TrimSpace(job.SourceAssetID) != "" {
			return normalizedJobRequest{}, fmt.Errorf("%w: generate job cannot have a source", ErrInvalidImageRequest)
		}
	case OperationEdit:
		if strings.TrimSpace(request.SourceAssetID) == "" || request.SourceAssetID != job.SourceAssetID {
			return normalizedJobRequest{}, fmt.Errorf("%w: edit source does not match job", ErrInvalidImageRequest)
		}
	default:
		return normalizedJobRequest{}, fmt.Errorf("%w: unsupported operation", ErrInvalidImageRequest)
	}
	return request, nil
}

func (e *Executor) callProvider(ctx context.Context, job GenerationRecord, request normalizedJobRequest) (ProviderImage, error) {
	if request.Operation == OperationGenerate {
		return e.cfg.Provider.Generate(ctx, ProviderGenerateRequest{Provider: request.Provider, Prompt: request.Prompt, Model: request.Model, Quality: request.Quality, Size: request.Size, Resolution: request.Resolution, AspectRatio: request.AspectRatio, OutputFormat: request.OutputFormat, Background: request.Background, Watermark: request.Watermark})
	}
	policy := media.AccessPolicy{TenantID: job.TenantID, UserID: job.UserID, SessionID: job.SessionID}
	asset, err := e.cfg.MediaStore.Get(ctx, policy, request.SourceAssetID)
	if err != nil {
		return ProviderImage{}, err
	}
	if !strings.HasPrefix(strings.ToLower(asset.MediaType), "image/") {
		return ProviderImage{}, fmt.Errorf("%w: source must be an image", ErrInvalidImageRequest)
	}
	key := firstNonBlank(asset.Original.Path, asset.Original.URL)
	reader, err := e.cfg.BlobStore.Open(ctx, policy, key)
	if err != nil {
		return ProviderImage{}, err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, e.cfg.MaxInputBytes+1))
	if err != nil {
		return ProviderImage{}, err
	}
	if int64(len(data)) > e.cfg.MaxInputBytes {
		return ProviderImage{}, fmt.Errorf("%w: input too large", ErrInvalidImageRequest)
	}
	return e.cfg.Provider.Edit(ctx, ProviderEditRequest{
		Provider: request.Provider, Prompt: request.Prompt, Model: request.Model, Quality: request.Quality, Size: request.Size, Resolution: request.Resolution, AspectRatio: request.AspectRatio, OutputFormat: request.OutputFormat, Background: request.Background, Watermark: request.Watermark,
		Image: bytes.NewReader(data), ImageName: asset.Name, ImageType: asset.MediaType,
	})
}

func (e *Executor) heartbeat(ctx context.Context, job GenerationRecord, cancelAttempt context.CancelCauseFunc, done chan<- error) {
	ticker := time.NewTicker(e.cfg.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			done <- nil
			return
		case <-ticker.C:
			renewCtx, cancelRenew := e.newFinalizeContext(ctx)
			cancelRequested, err := e.renewLease(renewCtx, job)
			cancelRenew()
			if err != nil {
				cancelAttempt(err)
				done <- err
				return
			}
			if cancelRequested {
				cancelAttempt(errImageJobCancelled)
				done <- nil
				return
			}
		}
	}
}

func (e *Executor) renewLease(ctx context.Context, job GenerationRecord) (bool, error) {
	return e.cfg.Repository.RenewImageGenerationLease(ctx, job.TenantID, job.GenerationID, job.LeaseOwner, e.cfg.Now().UTC().Add(e.cfg.LeaseDuration))
}

func (e *Executor) finishFailure(ctx context.Context, job GenerationRecord, startedAt time.Time, executionErr error, class string, cancelled bool, requestID string) error {
	status := GenerationStatusFailed
	if class == "" {
		class = ProviderErrorClass(executionErr)
	}
	var failure *ProviderFailure
	if errors.As(executionErr, &failure) {
		switch {
		case failure.OutcomeUnknown:
			status = GenerationStatusOutcomeUnknown
		case failure.Retryable && job.Attempts >= job.MaxAttempts:
			status = GenerationStatusDead
		case failure.Retryable:
			status = GenerationStatusRetry
		}
	}
	if cancelled {
		status = GenerationStatusCancelled
	}
	if class == "" {
		class = ErrorClassImageExecution
	}
	message := SanitizeErrorMessage(executionErr.Error())
	finish := e.attemptFinish(job, startedAt, status, class, message, requestID)
	finalizeCtx, cancelFinalize := e.newFinalizeContext(ctx)
	defer cancelFinalize()
	transition := ImageGenerationAttemptTransition{Attempt: finish, Status: status}
	if status == GenerationStatusRetry {
		delay := e.cfg.Jitter(imageRetryDelay(job.Attempts))
		if delay < 0 {
			delay = 0
		}
		transition.NextAttemptAt = e.cfg.Now().UTC().Add(delay)
	}
	_, err := e.cfg.Repository.TransitionClaimedImageAttempt(finalizeCtx, transition)
	return err
}

func (e *Executor) finishLifecycleShutdown(ctx context.Context, job GenerationRecord, startedAt time.Time, uncertain bool, executionErr error) error {
	if executionErr == nil {
		executionErr = context.Canceled
	}
	status := GenerationStatusRetry
	class := ErrorClassWorkerShutdown
	if uncertain {
		status = GenerationStatusOutcomeUnknown
		class = ErrorClassProviderOutcomeUnknown
	}
	message := SanitizeErrorMessage(executionErr.Error())
	finish := e.attemptFinish(job, startedAt, status, class, message, ProviderErrorRequestID(executionErr))
	transition := ImageGenerationAttemptTransition{Attempt: finish, Status: status}
	if status == GenerationStatusRetry {
		delay := e.cfg.Jitter(imageRetryDelay(job.Attempts))
		if delay < 0 {
			delay = 0
		}
		transition.NextAttemptAt = e.cfg.Now().UTC().Add(delay)
	}
	finalizeCtx, cancelFinalize := e.newFinalizeContext(ctx)
	defer cancelFinalize()
	_, err := e.cfg.Repository.TransitionClaimedImageAttempt(finalizeCtx, transition)
	return err
}

func (e *Executor) attemptFinish(job GenerationRecord, startedAt time.Time, outcome, class, message, requestID string) ImageGenerationAttemptFinish {
	finishedAt := e.cfg.Now().UTC()
	duration := finishedAt.Sub(startedAt)
	if duration < 0 {
		duration = 0
	}
	return ImageGenerationAttemptFinish{TenantID: job.TenantID, GenerationID: job.GenerationID, AttemptNo: job.Attempts, WorkerID: job.LeaseOwner, FinishedAt: finishedAt, DurationMS: uint64(duration / time.Millisecond), ProviderRequestID: requestID, Outcome: outcome, ErrorClass: class, ErrorMessage: message}
}

func (e *Executor) newFinalizeContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), e.cfg.FinalizeTimeout)
}
