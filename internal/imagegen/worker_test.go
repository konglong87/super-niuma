package imagegen

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/media"
)

type executorRepositoryStub struct {
	mu sync.Mutex

	claimQueue      []GenerationRecord
	claimCalls      []imageClaimCall
	claimErr        error
	renewCalls      int
	renewCancelAt   int
	renewErrAt      int
	renewErr        error
	attempts        []ImageGenerationAttemptFinish
	retries         []scheduledImageRetry
	finals          []finalizedImageGeneration
	completions     []CompleteClaimedImageAttemptRequest
	finalizeCtxs    []error
	transitionCalls int
	completeStatus  string
	completeErr     error
	exhaustedCalls  int
	legacyCounts    []int
	legacyUpdated   int
	legacyErr       error
	legacyErrAt     int
	legacyCalls     []legacyImageReconciliationCall
	mediaBlobPaths  []string
	mediaBlobErr    error
}

type legacyImageReconciliationCall struct {
	tenantID uint64
	cutoff   time.Time
	update   bool
}

type imageClaimCall struct {
	tenantID   uint64
	workerID   string
	limit      int
	leaseUntil time.Time
}

type scheduledImageRetry struct {
	tenantID     uint64
	generationID string
	workerID     string
	next         time.Time
	class        string
	message      string
}

type finalizedImageGeneration struct {
	tenantID          uint64
	generationID      string
	workerID          string
	status            string
	errorClass        string
	errorMessage      string
	providerRequestID string
}

func (s *executorRepositoryStub) ClaimDueImageGenerations(_ context.Context, tenantID uint64, workerID string, limit int, leaseUntil time.Time) ([]GenerationRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claimCalls = append(s.claimCalls, imageClaimCall{tenantID: tenantID, workerID: workerID, limit: limit, leaseUntil: leaseUntil})
	if s.claimErr != nil {
		return nil, s.claimErr
	}
	if limit > len(s.claimQueue) {
		limit = len(s.claimQueue)
	}
	claimed := append([]GenerationRecord(nil), s.claimQueue[:limit]...)
	s.claimQueue = s.claimQueue[limit:]
	return claimed, nil
}

func (s *executorRepositoryStub) RenewImageGenerationLease(_ context.Context, _ uint64, _, _ string, _ time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.renewCalls++
	if s.renewErr != nil && (s.renewErrAt == 0 || s.renewCalls >= s.renewErrAt) {
		return false, s.renewErr
	}
	return s.renewCancelAt > 0 && s.renewCalls >= s.renewCancelAt, nil
}

func (s *executorRepositoryStub) TransitionClaimedImageAttempt(ctx context.Context, input ImageGenerationAttemptTransition) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.transitionCalls++
	s.finalizeCtxs = append(s.finalizeCtxs, ctx.Err())
	s.attempts = append(s.attempts, input.Attempt)
	if input.Status == GenerationStatusRetry {
		s.retries = append(s.retries, scheduledImageRetry{tenantID: input.Attempt.TenantID, generationID: input.Attempt.GenerationID, workerID: input.Attempt.WorkerID, next: input.NextAttemptAt, class: input.Attempt.ErrorClass, message: input.Attempt.ErrorMessage})
	} else {
		s.finals = append(s.finals, finalizedImageGeneration{tenantID: input.Attempt.TenantID, generationID: input.Attempt.GenerationID, workerID: input.Attempt.WorkerID, status: input.Status, errorClass: input.Attempt.ErrorClass, errorMessage: input.Attempt.ErrorMessage, providerRequestID: input.Attempt.ProviderRequestID})
	}
	return input.Status, nil
}

func (s *executorRepositoryStub) CompleteClaimedImageAttempt(ctx context.Context, input CompleteClaimedImageAttemptRequest) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finalizeCtxs = append(s.finalizeCtxs, ctx.Err())
	s.completions = append(s.completions, input)
	s.attempts = append(s.attempts, input.Attempt)
	if s.completeErr != nil {
		return "", s.completeErr
	}
	if s.completeStatus != "" {
		return s.completeStatus, nil
	}
	return GenerationStatusCompleted, nil
}

func (s *executorRepositoryStub) FinalizeExhaustedImageGenerations(context.Context, uint64, time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.exhaustedCalls++
	return 0, nil
}

func (s *executorRepositoryStub) CountLegacyOrphanedImageGenerations(_ context.Context, tenantID uint64, cutoff time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.legacyCalls = append(s.legacyCalls, legacyImageReconciliationCall{tenantID: tenantID, cutoff: cutoff})
	if s.legacyErr != nil && (s.legacyErrAt == 0 || len(s.legacyCalls) >= s.legacyErrAt) {
		return 0, s.legacyErr
	}
	if len(s.legacyCounts) == 0 {
		return 0, nil
	}
	count := s.legacyCounts[0]
	s.legacyCounts = s.legacyCounts[1:]
	return count, nil
}

func (s *executorRepositoryStub) FailLegacyOrphanedImageGenerations(_ context.Context, tenantID uint64, cutoff time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.legacyCalls = append(s.legacyCalls, legacyImageReconciliationCall{tenantID: tenantID, cutoff: cutoff, update: true})
	if s.legacyErr != nil && (s.legacyErrAt == 0 || len(s.legacyCalls) >= s.legacyErrAt) {
		return 0, s.legacyErr
	}
	return s.legacyUpdated, nil
}

func (s *executorRepositoryStub) ListTenantMediaBlobPaths(_ context.Context, tenantID uint64) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if tenantID == 0 {
		return nil, ErrInvalidBlob
	}
	if s.mediaBlobErr != nil {
		return nil, s.mediaBlobErr
	}
	return append([]string(nil), s.mediaBlobPaths...), nil
}

type executorProviderStub struct {
	mu           sync.Mutex
	generateReq  ProviderGenerateRequest
	editReq      ProviderEditRequest
	source       string
	deadline     time.Time
	hasDeadline  bool
	started      chan struct{}
	release      chan struct{}
	result       ProviderImage
	err          error
	calls        int
	beforeReturn func()
}

func (s *executorProviderStub) Generate(ctx context.Context, req ProviderGenerateRequest) (ProviderImage, error) {
	s.mu.Lock()
	s.calls++
	s.generateReq = req
	s.deadline, s.hasDeadline = ctx.Deadline()
	s.mu.Unlock()
	return s.respond(ctx)
}

func (s *executorProviderStub) Edit(ctx context.Context, req ProviderEditRequest) (ProviderImage, error) {
	data, err := io.ReadAll(req.Image)
	if err != nil {
		return ProviderImage{}, err
	}
	s.mu.Lock()
	s.calls++
	s.editReq = req
	s.source = string(data)
	s.deadline, s.hasDeadline = ctx.Deadline()
	s.mu.Unlock()
	return s.respond(ctx)
}

func (s *executorProviderStub) respond(ctx context.Context) (ProviderImage, error) {
	if s.started != nil {
		select {
		case s.started <- struct{}{}:
		default:
		}
	}
	if s.release != nil {
		select {
		case <-ctx.Done():
			return ProviderImage{}, ctx.Err()
		case <-s.release:
		}
	}
	if s.beforeReturn != nil {
		s.beforeReturn()
	}
	return s.result, s.err
}

func TestExecutorGenerateUsesIndependentAttemptContextAndCompletesPersistedArtifact(t *testing.T) {
	repo := &executorRepositoryStub{}
	provider := &executorProviderStub{result: ProviderImage{Data: []byte("generated"), MediaType: "image/png"}}
	blobs := NewMemoryBlobStore()
	assets := media.NewMemoryStore()
	executor := NewExecutor(ExecutorConfig{
		Repository: repo, Provider: provider, BlobStore: blobs, MediaStore: assets,
		ProviderName: "jiuan", AttemptTimeout: 250 * time.Millisecond, HeartbeatInterval: time.Hour,
	})

	// The accepting channel request is already gone. Execution has a fresh
	// worker-owned lifecycle and must not inherit that request's cancellation.
	acceptedCtx, cancelAccepted := context.WithCancel(context.Background())
	cancelAccepted()
	if acceptedCtx.Err() == nil {
		t.Fatal("accepting context should be cancelled")
	}
	job := claimedImageJob(OperationGenerate, `{"operation":"generate","prompt":"a fox","model":"gpt-image-2","quality":"high","size":"1024x1024","output_format":"png","background":"opaque"}`)
	if err := executor.ExecuteClaimed(context.Background(), job); err != nil {
		t.Fatal(err)
	}

	provider.mu.Lock()
	req, deadline, hasDeadline := provider.generateReq, provider.deadline, provider.hasDeadline
	provider.mu.Unlock()
	if req.Prompt != "a fox" || req.Quality != "high" || req.Size != "1024x1024" || req.OutputFormat != "png" || req.Background != "opaque" {
		t.Fatalf("generate request=%+v", req)
	}
	remaining := time.Until(deadline)
	if !hasDeadline || remaining <= 0 || remaining > 250*time.Millisecond {
		t.Fatalf("attempt deadline=%v ok=%t remaining=%v", deadline, hasDeadline, remaining)
	}
	if len(repo.completions) != 1 || repo.completions[0].Attempt.TenantID != 7 || repo.completions[0].Attempt.GenerationID != "gen-1" || repo.completions[0].Attempt.WorkerID != "worker-a" || repo.completions[0].EventType != CompletionEventImageCompleted {
		t.Fatalf("completions=%+v", repo.completions)
	}
	completion := repo.completions[0]
	asset, err := assets.Get(context.Background(), media.AccessPolicy{TenantID: 7, UserID: 11, SessionID: 13}, completion.AssetID)
	if err != nil {
		t.Fatal(err)
	}
	if asset.Original.Path != "tenant-7/user-11/session-13/generation-gen-1.png" {
		t.Fatalf("deterministic path=%q", asset.Original.Path)
	}
	reader, err := blobs.Open(context.Background(), asset.Access, asset.Original.Path)
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || string(data) != "generated" {
		t.Fatalf("blob=%q readErr=%v closeErr=%v", data, readErr, closeErr)
	}
	if len(repo.attempts) != 1 || repo.attempts[0].Outcome != ImageAttemptOutcomeCompleted || repo.attempts[0].AttemptNo != 1 {
		t.Fatalf("attempts=%+v", repo.attempts)
	}
}

func TestExecutorEditLoadsTenantScopedSourceAndCallsEdit(t *testing.T) {
	repo := &executorRepositoryStub{}
	provider := &executorProviderStub{result: ProviderImage{Data: []byte("edited"), MediaType: "image/jpeg"}}
	blobs := NewMemoryBlobStore()
	assets := media.NewMemoryStore()
	policy := media.AccessPolicy{TenantID: 7, UserID: 11, SessionID: 13}
	if _, err := blobs.Put(context.Background(), PutBlobRequest{Policy: policy, Key: "tenant-7/user-11/session-13/source.png", Name: "source.png", MediaType: "image/png", Data: strings.NewReader("source-bytes")}); err != nil {
		t.Fatal(err)
	}
	if err := assets.Put(context.Background(), media.Asset{AssetID: "source-1", Kind: media.KindImage, MediaType: "image/png", Name: "source.png", State: media.StateReady, TenantID: 7, UserID: 11, SessionID: 13, Original: media.Variant{Path: "tenant-7/user-11/session-13/source.png"}, Access: policy}); err != nil {
		t.Fatal(err)
	}
	executor := NewExecutor(ExecutorConfig{Repository: repo, Provider: provider, BlobStore: blobs, MediaStore: assets, ProviderName: "jiuan", HeartbeatInterval: time.Hour})
	job := claimedImageJob(OperationEdit, `{"operation":"edit","prompt":"make it blue","model":"gpt-image-2","quality":"auto","size":"auto","output_format":"jpeg","background":"auto","source_asset_id":"source-1"}`)
	job.SourceAssetID = "source-1"
	if err := executor.ExecuteClaimed(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	req, source := provider.editReq, provider.source
	provider.mu.Unlock()
	if req.Prompt != "make it blue" || req.ImageName != "source.png" || req.ImageType != "image/png" || source != "source-bytes" {
		t.Fatalf("edit request=%+v source=%q", req, source)
	}
	if len(repo.completions) != 1 {
		t.Fatalf("completions=%+v", repo.completions)
	}
}

func TestExecutorRejectsMalformedOrInconsistentNormalizedRequestsBeforeProvider(t *testing.T) {
	tests := []struct {
		name        string
		operation   string
		requestJSON string
	}{
		{name: "malformed json", operation: OperationGenerate, requestJSON: "{"},
		{name: "operation mismatch", operation: OperationGenerate, requestJSON: `{"operation":"edit","prompt":"x","model":"m","quality":"auto","size":"auto","output_format":"png","background":"auto","source_asset_id":"source-1"}`},
		{name: "unsupported option", operation: OperationGenerate, requestJSON: `{"operation":"generate","prompt":"x","model":"m","quality":"ultra","size":"auto","output_format":"png","background":"auto"}`},
		{name: "generate with source", operation: OperationGenerate, requestJSON: `{"operation":"generate","prompt":"x","model":"m","quality":"auto","size":"auto","output_format":"png","background":"auto","source_asset_id":"source-1"}`},
		{name: "edit without source", operation: OperationEdit, requestJSON: `{"operation":"edit","prompt":"x","model":"m","quality":"auto","size":"auto","output_format":"png","background":"auto"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := &executorRepositoryStub{}
			provider := &executorProviderStub{result: ProviderImage{Data: []byte("unexpected"), MediaType: "image/png"}}
			executor := NewExecutor(ExecutorConfig{Repository: repo, Provider: provider, BlobStore: NewMemoryBlobStore(), MediaStore: media.NewMemoryStore(), ProviderName: "jiuan"})
			job := claimedImageJob(test.operation, test.requestJSON)
			if err := executor.ExecuteClaimed(context.Background(), job); err != nil {
				t.Fatal(err)
			}
			if provider.calls != 0 || len(repo.finals) != 1 || repo.finals[0].status != GenerationStatusFailed || repo.finals[0].errorClass != ErrorClassInvalidJobRequest {
				t.Fatalf("calls=%d finals=%+v", provider.calls, repo.finals)
			}
		})
	}
}

func TestExecutorRenewsLeaseWhileProviderIsRunning(t *testing.T) {
	repo := &executorRepositoryStub{}
	provider := &executorProviderStub{started: make(chan struct{}, 1), release: make(chan struct{}), result: ProviderImage{Data: []byte("image"), MediaType: "image/png"}}
	executor := NewExecutor(ExecutorConfig{Repository: repo, Provider: provider, BlobStore: NewMemoryBlobStore(), MediaStore: media.NewMemoryStore(), ProviderName: "jiuan", HeartbeatInterval: 5 * time.Millisecond, AttemptTimeout: time.Second})
	done := make(chan error, 1)
	go func() {
		done <- executor.ExecuteClaimed(context.Background(), claimedImageJob(OperationGenerate, validGenerateJobJSON()))
	}()
	<-provider.started
	deadline := time.Now().Add(time.Second)
	for {
		repo.mu.Lock()
		calls := repo.renewCalls
		repo.mu.Unlock()
		if calls >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("heartbeat did not renew the lease")
		}
		time.Sleep(time.Millisecond)
	}
	close(provider.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestExecutorCancellationObservationCancelsProviderAndFinalizesCancelled(t *testing.T) {
	repo := &executorRepositoryStub{renewCancelAt: 2}
	provider := &executorProviderStub{started: make(chan struct{}, 1), release: make(chan struct{})}
	executor := NewExecutor(ExecutorConfig{Repository: repo, Provider: provider, BlobStore: NewMemoryBlobStore(), MediaStore: media.NewMemoryStore(), ProviderName: "jiuan", HeartbeatInterval: 5 * time.Millisecond, AttemptTimeout: time.Second})
	if err := executor.ExecuteClaimed(context.Background(), claimedImageJob(OperationGenerate, validGenerateJobJSON())); err != nil {
		t.Fatal(err)
	}
	if len(repo.finals) != 1 || repo.finals[0].status != GenerationStatusCancelled || repo.finals[0].errorClass != ErrorClassCallerCancelled || len(repo.completions) != 0 || len(repo.retries) != 0 {
		t.Fatalf("finals=%+v retries=%+v completions=%+v", repo.finals, repo.retries, repo.completions)
	}
	if len(repo.attempts) != 1 || repo.attempts[0].Outcome != GenerationStatusCancelled {
		t.Fatalf("attempts=%+v", repo.attempts)
	}
}

func TestExecutorPostProviderCancellationWinsOverProviderFailure(t *testing.T) {
	repo := &executorRepositoryStub{renewCancelAt: 2}
	provider := &executorProviderStub{err: providerFailure(ErrorClassProviderRejected, errors.New("rejected"), false, false)}
	executor := NewExecutor(ExecutorConfig{Repository: repo, Provider: provider, BlobStore: NewMemoryBlobStore(), MediaStore: media.NewMemoryStore(), ProviderName: "jiuan", HeartbeatInterval: time.Hour})
	if err := executor.ExecuteClaimed(context.Background(), claimedImageJob(OperationGenerate, validGenerateJobJSON())); err != nil {
		t.Fatal(err)
	}
	if len(repo.finals) != 1 || repo.finals[0].status != GenerationStatusCancelled || repo.finals[0].errorClass != ErrorClassCallerCancelled {
		t.Fatalf("finals=%+v", repo.finals)
	}
}

func TestExecutorLeaseLossStopsOldOwnerWithoutTerminalTransition(t *testing.T) {
	repo := &executorRepositoryStub{renewErr: ErrImageGenerationLeaseLost, renewErrAt: 2}
	provider := &executorProviderStub{started: make(chan struct{}, 1), release: make(chan struct{})}
	executor := NewExecutor(ExecutorConfig{Repository: repo, Provider: provider, BlobStore: NewMemoryBlobStore(), MediaStore: media.NewMemoryStore(), ProviderName: "jiuan", HeartbeatInterval: 5 * time.Millisecond, AttemptTimeout: time.Second})
	err := executor.ExecuteClaimed(context.Background(), claimedImageJob(OperationGenerate, validGenerateJobJSON()))
	if !errors.Is(err, ErrImageGenerationLeaseLost) {
		t.Fatalf("err=%v", err)
	}
	if len(repo.finals) != 0 || len(repo.retries) != 0 || len(repo.completions) != 0 {
		t.Fatalf("old owner wrote state: finals=%+v retries=%+v completions=%+v", repo.finals, repo.retries, repo.completions)
	}
}

func TestExecutorClassifiesProviderFailuresAndRetriesOnlyTypedRetryableErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus string
		wantClass  string
		wantRetry  bool
	}{
		{name: "rate limited", err: providerFailure(ErrorClassProviderRateLimited, errors.New("429"), true, false), wantStatus: GenerationStatusRetry, wantClass: ErrorClassProviderRateLimited, wantRetry: true},
		{name: "unavailable", err: providerFailure(ErrorClassProviderUnavailable, errors.New("503"), true, false), wantStatus: GenerationStatusRetry, wantClass: ErrorClassProviderUnavailable, wantRetry: true},
		{name: "rejected", err: providerFailure(ErrorClassProviderRejected, errors.New("policy"), false, false), wantStatus: GenerationStatusFailed, wantClass: ErrorClassProviderRejected},
		{name: "outcome unknown", err: providerFailure(ErrorClassProviderOutcomeUnknown, errors.New("response lost"), false, true), wantStatus: GenerationStatusOutcomeUnknown, wantClass: ErrorClassProviderOutcomeUnknown},
		{name: "provider timeout", err: providerFailure(ErrorClassProviderTimeout, ErrProviderTimeout, false, true), wantStatus: GenerationStatusOutcomeUnknown, wantClass: ErrorClassProviderTimeout},
		{name: "caller deadline", err: providerFailure(ErrorClassCallerDeadlineExceeded, context.DeadlineExceeded, false, false), wantStatus: GenerationStatusFailed, wantClass: ErrorClassCallerDeadlineExceeded},
		{name: "caller cancelled", err: providerFailure(ErrorClassCallerCancelled, context.Canceled, false, false), wantStatus: GenerationStatusFailed, wantClass: ErrorClassCallerCancelled},
		{name: "untyped", err: errors.New("local failure"), wantStatus: GenerationStatusFailed, wantClass: ErrorClassImageExecution},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			now := time.Date(2026, time.September, 2, 10, 0, 0, 0, time.UTC)
			repo := &executorRepositoryStub{}
			provider := &executorProviderStub{err: test.err}
			executor := NewExecutor(ExecutorConfig{Repository: repo, Provider: provider, BlobStore: NewMemoryBlobStore(), MediaStore: media.NewMemoryStore(), ProviderName: "jiuan", HeartbeatInterval: time.Hour, Now: func() time.Time { return now }, Jitter: func(delay time.Duration) time.Duration { return delay }})
			if err := executor.ExecuteClaimed(context.Background(), claimedImageJob(OperationGenerate, validGenerateJobJSON())); err != nil {
				t.Fatal(err)
			}
			if test.wantRetry {
				if len(repo.retries) != 1 || len(repo.finals) != 0 || repo.retries[0].class != test.wantClass {
					t.Fatalf("retries=%+v finals=%+v", repo.retries, repo.finals)
				}
			} else if len(repo.finals) != 1 || len(repo.retries) != 0 || repo.finals[0].status != test.wantStatus || repo.finals[0].errorClass != test.wantClass {
				t.Fatalf("retries=%+v finals=%+v", repo.retries, repo.finals)
			}
			if len(repo.attempts) != 1 || repo.attempts[0].Outcome != test.wantStatus || repo.attempts[0].ErrorClass != test.wantClass {
				t.Fatalf("attempts=%+v", repo.attempts)
			}
		})
	}
}

func TestExecutorUsesExactRetryBackoffWithInjectedJitter(t *testing.T) {
	now := time.Date(2026, time.September, 2, 10, 0, 0, 0, time.UTC)
	for attempt, base := range map[uint]time.Duration{1: 30 * time.Second, 2: 2 * time.Minute, 3: 5 * time.Minute} {
		t.Run(time.Duration(attempt).String(), func(t *testing.T) {
			repo := &executorRepositoryStub{}
			provider := &executorProviderStub{err: providerFailure(ErrorClassProviderUnavailable, errors.New("503"), true, false)}
			executor := NewExecutor(ExecutorConfig{Repository: repo, Provider: provider, BlobStore: NewMemoryBlobStore(), MediaStore: media.NewMemoryStore(), ProviderName: "jiuan", HeartbeatInterval: time.Hour, Now: func() time.Time { return now }, Jitter: func(delay time.Duration) time.Duration { return delay + 7*time.Second }})
			job := claimedImageJob(OperationGenerate, validGenerateJobJSON())
			job.Attempts, job.MaxAttempts = attempt, 4
			if err := executor.ExecuteClaimed(context.Background(), job); err != nil {
				t.Fatal(err)
			}
			want := now.Add(base + 7*time.Second)
			if len(repo.retries) != 1 || !repo.retries[0].next.Equal(want) {
				t.Fatalf("retry=%+v want=%v", repo.retries, want)
			}
		})
	}
}

func TestExecutorExhaustedRetryableAttemptBecomesDead(t *testing.T) {
	repo := &executorRepositoryStub{}
	provider := &executorProviderStub{err: providerFailure(ErrorClassProviderUnavailable, errors.New("503"), true, false)}
	executor := NewExecutor(ExecutorConfig{Repository: repo, Provider: provider, BlobStore: NewMemoryBlobStore(), MediaStore: media.NewMemoryStore(), ProviderName: "jiuan", HeartbeatInterval: time.Hour})
	job := claimedImageJob(OperationGenerate, validGenerateJobJSON())
	job.Attempts, job.MaxAttempts = 3, 3
	if err := executor.ExecuteClaimed(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if len(repo.retries) != 0 || len(repo.finals) != 1 || repo.finals[0].status != GenerationStatusDead {
		t.Fatalf("retries=%+v finals=%+v", repo.retries, repo.finals)
	}
}

func TestExecutorAttemptTimeoutUsesDetachedFinalizationContext(t *testing.T) {
	repo := &executorRepositoryStub{}
	provider := &executorProviderStub{release: make(chan struct{})}
	executor := NewExecutor(ExecutorConfig{Repository: repo, Provider: provider, BlobStore: NewMemoryBlobStore(), MediaStore: media.NewMemoryStore(), ProviderName: "jiuan", HeartbeatInterval: time.Hour, AttemptTimeout: 5 * time.Millisecond})
	if err := executor.ExecuteClaimed(context.Background(), claimedImageJob(OperationGenerate, validGenerateJobJSON())); err != nil {
		t.Fatal(err)
	}
	if len(repo.finals) != 1 || repo.finals[0].status != GenerationStatusOutcomeUnknown || repo.finals[0].errorClass != ErrorClassProviderTimeout {
		t.Fatalf("finals=%+v", repo.finals)
	}
	for _, ctxErr := range repo.finalizeCtxs {
		if ctxErr != nil {
			t.Fatalf("finalization inherited attempt cancellation: %v", ctxErr)
		}
	}
}

func TestExecutorWorkerLifecycleCancellationAfterCallIsOutcomeUnknown(t *testing.T) {
	repo := &executorRepositoryStub{}
	provider := &executorProviderStub{started: make(chan struct{}, 1), release: make(chan struct{})}
	executor := NewExecutor(ExecutorConfig{Repository: repo, Provider: provider, BlobStore: NewMemoryBlobStore(), MediaStore: media.NewMemoryStore(), ProviderName: "jiuan", HeartbeatInterval: time.Hour, AttemptTimeout: time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- executor.ExecuteClaimed(ctx, claimedImageJob(OperationGenerate, validGenerateJobJSON()))
	}()
	<-provider.started
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(repo.retries) != 0 || len(repo.finals) != 1 || repo.finals[0].status != GenerationStatusOutcomeUnknown || repo.finals[0].errorClass != ErrorClassProviderOutcomeUnknown {
		t.Fatalf("retries=%+v finals=%+v", repo.retries, repo.finals)
	}
	for _, ctxErr := range repo.finalizeCtxs {
		if ctxErr != nil {
			t.Fatalf("finalization inherited lifecycle cancellation: %v", ctxErr)
		}
	}
}

func TestExecutorWorkerLifecycleCancellationBeforeProviderCallSchedulesRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	repo := &executorRepositoryStub{}
	provider := &executorProviderStub{}
	executor := NewExecutor(ExecutorConfig{Repository: repo, Provider: provider, BlobStore: NewMemoryBlobStore(), MediaStore: media.NewMemoryStore(), ProviderName: "jiuan", HeartbeatInterval: time.Hour})
	if err := executor.ExecuteClaimed(ctx, claimedImageJob(OperationGenerate, validGenerateJobJSON())); err != nil {
		t.Fatal(err)
	}
	if provider.calls != 0 || len(repo.retries) != 1 || repo.retries[0].class != ErrorClassWorkerShutdown || len(repo.finals) != 0 {
		t.Fatalf("provider calls=%d retries=%+v finals=%+v", provider.calls, repo.retries, repo.finals)
	}
}

func TestExecutorLifecycleCancellationAfterProviderSuccessCompletesKnownResult(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	repo := &executorRepositoryStub{}
	provider := &executorProviderStub{result: ProviderImage{Data: []byte("generated"), MediaType: "image/png"}, beforeReturn: cancel}
	executor := NewExecutor(ExecutorConfig{Repository: repo, Provider: provider, BlobStore: NewMemoryBlobStore(), MediaStore: media.NewMemoryStore(), ProviderName: "jiuan", HeartbeatInterval: time.Hour})
	if err := executor.ExecuteClaimed(ctx, claimedImageJob(OperationGenerate, validGenerateJobJSON())); err != nil {
		t.Fatal(err)
	}
	if len(repo.completions) != 1 || len(repo.retries) != 0 || len(repo.finals) != 0 {
		t.Fatalf("completions=%+v retries=%+v finals=%+v", repo.completions, repo.retries, repo.finals)
	}
}

func TestExecutorCancellationDuringCompletionDeletesBlobAndDoesNotComplete(t *testing.T) {
	repo := &executorRepositoryStub{completeStatus: GenerationStatusCancelled}
	provider := &executorProviderStub{result: ProviderImage{Data: []byte("generated"), MediaType: "image/png"}}
	blobs := NewMemoryBlobStore()
	assets := media.NewMemoryStore()
	executor := NewExecutor(ExecutorConfig{Repository: repo, Provider: provider, BlobStore: blobs, MediaStore: assets, ProviderName: "jiuan", HeartbeatInterval: time.Hour})
	job := claimedImageJob(OperationGenerate, validGenerateJobJSON())
	if err := executor.ExecuteClaimed(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if len(repo.completions) != 1 || len(repo.attempts) != 1 || repo.attempts[0].Outcome != GenerationStatusCompleted {
		t.Fatalf("completions=%+v attempts=%+v", repo.completions, repo.attempts)
	}
	key := "tenant-7/user-11/session-13/generation-gen-1.png"
	if _, err := blobs.Open(context.Background(), media.AccessPolicy{TenantID: 7, UserID: 11, SessionID: 13}, key); !errors.Is(err, ErrBlobNotFound) {
		t.Fatalf("cancelled completion left deterministic blob visible: %v", err)
	}
}

func claimedImageJob(operation, requestJSON string) GenerationRecord {
	return GenerationRecord{
		GenerationID: "gen-1", TenantID: 7, UserID: 11, SessionID: 13,
		Operation: operation, Status: GenerationStatusRunning, Prompt: "snapshot prompt", Provider: "jiuan", Model: "gpt-image-2",
		RequestJSON: requestJSON, Attempts: 1, MaxAttempts: 3, LeaseOwner: "worker-a", OriginType: OriginTypeChannel, IdempotencyKey: "channel:fixed",
	}
}

func validGenerateJobJSON() string {
	return `{"operation":"generate","prompt":"a fox","model":"gpt-image-2","quality":"auto","size":"auto","output_format":"png","background":"auto"}`
}

func TestExecutorConfigurationIsRequired(t *testing.T) {
	job := claimedImageJob(OperationGenerate, `{"operation":"generate","prompt":"x","model":"m","quality":"auto","size":"auto","output_format":"png","background":"auto"}`)
	for name, executor := range map[string]*Executor{
		"nil":         nil,
		"empty":       NewExecutor(ExecutorConfig{}),
		"no blobs":    NewExecutor(ExecutorConfig{Repository: &executorRepositoryStub{}, Provider: &executorProviderStub{}, MediaStore: media.NewMemoryStore()}),
		"no metadata": NewExecutor(ExecutorConfig{Repository: &executorRepositoryStub{}, Provider: &executorProviderStub{}, BlobStore: NewMemoryBlobStore()}),
	} {
		t.Run(name, func(t *testing.T) {
			if err := executor.ExecuteClaimed(context.Background(), job); !errors.Is(err, ErrInvalidImageWorkerConfig) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

type claimedExecutorStub struct {
	mu             sync.Mutex
	active         int
	maxActive      int
	jobs           []GenerationRecord
	started        chan struct{}
	release        chan struct{}
	cleanupRelease chan struct{}
	contextEnded   int
}

func (s *claimedExecutorStub) ExecuteClaimed(ctx context.Context, job GenerationRecord) error {
	s.mu.Lock()
	s.active++
	if s.active > s.maxActive {
		s.maxActive = s.active
	}
	s.jobs = append(s.jobs, job)
	s.mu.Unlock()
	if s.started != nil {
		s.started <- struct{}{}
	}
	select {
	case <-ctx.Done():
		s.mu.Lock()
		s.contextEnded++
		s.mu.Unlock()
		if s.cleanupRelease != nil {
			<-s.cleanupRelease
		}
	case <-s.release:
	}
	s.mu.Lock()
	s.active--
	s.mu.Unlock()
	return nil
}

func TestWorkerClaimsOnlyConfiguredTenantWithinAvailableConcurrency(t *testing.T) {
	repo := &executorRepositoryStub{claimQueue: []GenerationRecord{
		claimedImageJob(OperationGenerate, validGenerateJobJSON()),
		claimedImageJob(OperationGenerate, validGenerateJobJSON()),
		claimedImageJob(OperationGenerate, validGenerateJobJSON()),
	}}
	for index := range repo.claimQueue {
		repo.claimQueue[index].GenerationID = fmt.Sprintf("gen-%d", index+1)
	}
	runner := &claimedExecutorStub{started: make(chan struct{}, 3), release: make(chan struct{})}
	worker := NewWorker(ImageWorkerConfig{Repository: repo, Executor: runner, TenantID: 7, WorkerID: "worker-a", PollInterval: time.Millisecond, MaxConcurrent: 2, LeaseDuration: time.Minute})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	<-runner.started
	<-runner.started
	time.Sleep(10 * time.Millisecond)
	runner.mu.Lock()
	startedBeforeRelease, maxActive := len(runner.jobs), runner.maxActive
	runner.mu.Unlock()
	if startedBeforeRelease != 2 || maxActive != 2 {
		t.Fatalf("started=%d maxActive=%d", startedBeforeRelease, maxActive)
	}
	close(runner.release)
	<-runner.started
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	repo.mu.Lock()
	claimCalls := append([]imageClaimCall(nil), repo.claimCalls...)
	repo.mu.Unlock()
	if len(claimCalls) == 0 || claimCalls[0].tenantID != 7 || claimCalls[0].workerID != "worker-a" || claimCalls[0].limit != 2 {
		t.Fatalf("claim calls=%+v", claimCalls)
	}
	for _, call := range claimCalls {
		if call.tenantID != 7 || call.workerID != "worker-a" || call.limit > 2 || call.leaseUntil.IsZero() {
			t.Fatalf("unsafe claim=%+v", call)
		}
	}
}

func TestWorkerGracefulShutdownStopsPollingAndWaitsForInFlight(t *testing.T) {
	repo := &executorRepositoryStub{claimQueue: []GenerationRecord{claimedImageJob(OperationGenerate, validGenerateJobJSON())}}
	runner := &claimedExecutorStub{started: make(chan struct{}, 1), release: make(chan struct{}), cleanupRelease: make(chan struct{})}
	worker := NewWorker(ImageWorkerConfig{Repository: repo, Executor: runner, TenantID: 7, WorkerID: "worker-a", PollInterval: time.Millisecond, MaxConcurrent: 1})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	<-runner.started
	cancel()
	deadline := time.Now().Add(time.Second)
	for {
		runner.mu.Lock()
		contextEnded := runner.contextEnded
		runner.mu.Unlock()
		if contextEnded == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker lifecycle cancellation did not reach in-flight attempt")
		}
		time.Sleep(time.Millisecond)
	}
	repo.mu.Lock()
	claimCountAfterStop := len(repo.claimCalls)
	repo.mu.Unlock()
	time.Sleep(10 * time.Millisecond)
	repo.mu.Lock()
	claimCountLater := len(repo.claimCalls)
	repo.mu.Unlock()
	if claimCountLater != claimCountAfterStop {
		t.Fatalf("worker continued polling after stop: before=%d after=%d", claimCountAfterStop, claimCountLater)
	}
	select {
	case err := <-done:
		t.Fatalf("worker returned before cancelled attempt cleanup: %v", err)
	case <-time.After(15 * time.Millisecond):
	}
	close(runner.cleanupRelease)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	runner.mu.Lock()
	contextEnded := runner.contextEnded
	runner.mu.Unlock()
	if contextEnded != 1 {
		t.Fatalf("lifecycle cancellation reached %d in-flight attempts", contextEnded)
	}
}

type imageWorkerRunnerFunc func(context.Context) error

func (f imageWorkerRunnerFunc) Run(ctx context.Context) error { return f(ctx) }

func TestImageWorkerRunsCompletionDispatcherWithSharedLifecycle(t *testing.T) {
	jobRepo := &executorRepositoryStub{}
	completionRepo := &completionRepositoryStub{}
	dispatcher := NewCompletionDispatcher(CompletionDispatcherConfig{
		Repository:   completionRepo,
		Consumer:     completionConsumerFunc(func(context.Context, CompletionEvent) error { return nil }),
		TenantID:     7,
		WorkerID:     "dispatcher-a",
		PollInterval: time.Millisecond,
	})
	worker := NewWorker(ImageWorkerConfig{Repository: jobRepo, Executor: &claimedExecutorStub{release: make(chan struct{})}, CompletionRunner: dispatcher, TenantID: 7, WorkerID: "worker-a", PollInterval: time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	deadline := time.Now().Add(time.Second)
	for {
		completionRepo.mu.Lock()
		completionClaims := completionRepo.claims
		completionRepo.mu.Unlock()
		jobRepo.mu.Lock()
		jobClaims := len(jobRepo.claimCalls)
		jobRepo.mu.Unlock()
		if completionClaims > 0 && jobClaims > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("claims generation=%d completion=%d", jobClaims, completionClaims)
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("image worker did not wait for completion dispatcher shutdown")
	}
}

func TestImageWorkerPropagatesCompletionRunnerFailure(t *testing.T) {
	want := errors.New("completion dispatcher failed")
	worker := NewWorker(ImageWorkerConfig{
		Repository: &executorRepositoryStub{}, Executor: &claimedExecutorStub{release: make(chan struct{})},
		CompletionRunner: imageWorkerRunnerFunc(func(context.Context) error { return want }),
		TenantID:         7, WorkerID: "worker-a", PollInterval: time.Millisecond,
	})
	if err := worker.Run(context.Background()); !errors.Is(err, want) {
		t.Fatalf("err=%v", err)
	}
}

func TestImageWorkerSignalsReadyOnlyAfterStartupRecoveryAndBeforeClaims(t *testing.T) {
	repo := &executorRepositoryStub{legacyCounts: []int{1, 0}, legacyUpdated: 1}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	readyCalls := 0
	worker := NewWorker(ImageWorkerConfig{
		Repository: repo, Executor: &claimedExecutorStub{release: make(chan struct{})},
		TenantID: 7, WorkerID: "worker-a", PollInterval: time.Millisecond,
		OnReady: func(result ImageWorkerStartupRecoveryResult) error {
			readyCalls++
			if result.Legacy.TenantID != 7 || result.Legacy.Updated != 1 || result.Legacy.Remaining != 0 {
				t.Fatalf("startup recovery result = %+v", result)
			}
			repo.mu.Lock()
			claimCalls := len(repo.claimCalls)
			repo.mu.Unlock()
			if claimCalls != 0 {
				t.Fatalf("worker claimed %d jobs before readiness", claimCalls)
			}
			cancel()
			return nil
		},
	})
	if err := worker.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if readyCalls != 1 {
		t.Fatalf("ready calls = %d, want 1", readyCalls)
	}
}

func TestImageWorkerReadinessFailureStopsBeforeClaims(t *testing.T) {
	repo := &executorRepositoryStub{}
	want := errors.New("write readiness")
	worker := NewWorker(ImageWorkerConfig{
		Repository: repo, Executor: &claimedExecutorStub{release: make(chan struct{})},
		TenantID: 7, WorkerID: "worker-a", PollInterval: time.Millisecond,
		OnReady: func(ImageWorkerStartupRecoveryResult) error { return want },
	})
	if err := worker.Run(context.Background()); !errors.Is(err, want) {
		t.Fatalf("Run() error = %v, want %v", err, want)
	}
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.claimCalls) != 0 {
		t.Fatalf("worker claimed jobs after readiness failure: %+v", repo.claimCalls)
	}
}

func TestWorkerRejectsIncompleteConfiguration(t *testing.T) {
	for name, worker := range map[string]*ImageWorker{
		"nil":           nil,
		"empty":         NewWorker(ImageWorkerConfig{}),
		"missing id":    NewWorker(ImageWorkerConfig{Repository: &executorRepositoryStub{}, Executor: &claimedExecutorStub{release: make(chan struct{})}, TenantID: 7}),
		"missing scope": NewWorker(ImageWorkerConfig{Repository: &executorRepositoryStub{}, Executor: &claimedExecutorStub{release: make(chan struct{})}, WorkerID: "worker-a"}),
	} {
		t.Run(name, func(t *testing.T) {
			if err := worker.Run(context.Background()); !errors.Is(err, ErrInvalidImageWorkerConfig) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestWorkerReconcileLegacyDryRunCountsCandidatesWithoutUpdatingOrExecutingProvider(t *testing.T) {
	now := time.Date(2026, time.September, 2, 10, 0, 0, 0, time.UTC)
	repo := &executorRepositoryStub{legacyCounts: []int{2}}
	worker := NewWorker(ImageWorkerConfig{
		Repository: repo,
		TenantID:   7,
		WorkerID:   "worker-a",
		Now:        func() time.Time { return now },
	})

	result, err := worker.ReconcileLegacy(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	wantCutoff := now.Add(-(DefaultImageAttemptTimeout + DefaultImageLeaseGrace))
	if result.TenantID != 7 || result.Candidates != 2 || result.Updated != 0 || result.Remaining != 2 || !result.Cutoff.Equal(wantCutoff) {
		t.Fatalf("result=%+v", result)
	}
	repo.mu.Lock()
	calls := append([]legacyImageReconciliationCall(nil), repo.legacyCalls...)
	repo.mu.Unlock()
	if len(calls) != 1 || calls[0].update || calls[0].tenantID != 7 || !calls[0].cutoff.Equal(wantCutoff) {
		t.Fatalf("calls=%+v", calls)
	}
}

func TestWorkerReconcileLegacyUsesDetachedPersistenceAndReadsBackGuardedUpdate(t *testing.T) {
	now := time.Date(2026, time.September, 2, 10, 0, 0, 0, time.UTC)
	repo := &executorRepositoryStub{legacyCounts: []int{2, 0}, legacyUpdated: 2}
	worker := NewWorker(ImageWorkerConfig{
		Repository: repo,
		TenantID:   7,
		WorkerID:   "worker-a",
		Now:        func() time.Time { return now },
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := worker.ReconcileLegacy(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Candidates != 2 || result.Updated != 2 || result.Remaining != 0 {
		t.Fatalf("result=%+v", result)
	}
	repo.mu.Lock()
	calls := append([]legacyImageReconciliationCall(nil), repo.legacyCalls...)
	repo.mu.Unlock()
	if len(calls) != 3 || calls[0].update || !calls[1].update || calls[2].update {
		t.Fatalf("calls=%+v", calls)
	}
}

func TestWorkerReconcileLegacyReportsReadbackMismatch(t *testing.T) {
	repo := &executorRepositoryStub{legacyCounts: []int{2, 1}, legacyUpdated: 2}
	worker := NewWorker(ImageWorkerConfig{Repository: repo, TenantID: 7, WorkerID: "worker-a"})

	result, err := worker.ReconcileLegacy(context.Background(), false)
	if !errors.Is(err, ErrLegacyImageReconciliationMismatch) || result.Candidates != 2 || result.Updated != 2 || result.Remaining != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestWorkerReconcileLegacyAcceptsConcurrentStarterReadback(t *testing.T) {
	repo := &executorRepositoryStub{legacyCounts: []int{2, 0}, legacyUpdated: 0}
	worker := NewWorker(ImageWorkerConfig{Repository: repo, TenantID: 7, WorkerID: "worker-a"})

	result, err := worker.ReconcileLegacy(context.Background(), false)
	if err != nil || result.Candidates != 2 || result.Updated != 0 || result.Remaining != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestWorkerReconcileLegacyReturnsReadbackError(t *testing.T) {
	readbackErr := errors.New("readback unavailable")
	repo := &executorRepositoryStub{legacyCounts: []int{1, 0}, legacyUpdated: 1, legacyErr: readbackErr, legacyErrAt: 3}
	worker := NewWorker(ImageWorkerConfig{Repository: repo, TenantID: 7, WorkerID: "worker-a"})

	result, err := worker.ReconcileLegacy(context.Background(), false)
	if !errors.Is(err, readbackErr) || result.Candidates != 1 || result.Updated != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestWorkerRunReconcilesLegacyRowsWithoutCallingProvider(t *testing.T) {
	repo := &executorRepositoryStub{legacyCounts: []int{1, 0}, legacyUpdated: 1}
	provider := &executorProviderStub{result: ProviderImage{Data: []byte("unexpected"), MediaType: "image/png"}}
	executor := NewExecutor(ExecutorConfig{
		Repository: repo, Provider: provider, BlobStore: NewMemoryBlobStore(), MediaStore: media.NewMemoryStore(), ProviderName: "jiuan", HeartbeatInterval: time.Hour,
	})
	worker := NewWorker(ImageWorkerConfig{Repository: repo, Executor: executor, TenantID: 7, WorkerID: "worker-a", PollInterval: time.Hour})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if err := worker.Run(ctx); err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	calls := provider.calls
	provider.mu.Unlock()
	if calls != 0 {
		t.Fatalf("provider calls=%d", calls)
	}
	repo.mu.Lock()
	legacyCalls := append([]legacyImageReconciliationCall(nil), repo.legacyCalls...)
	repo.mu.Unlock()
	if len(legacyCalls) != 3 || legacyCalls[0].update || !legacyCalls[1].update || legacyCalls[2].update {
		t.Fatalf("legacy calls=%+v", legacyCalls)
	}
}

type orphanBlobStoreStub struct {
	mu         sync.Mutex
	blobs      []Blob
	deleteErrs map[string]error
	deleted    []string
}

func (s *orphanBlobStoreStub) Put(context.Context, PutBlobRequest) (Blob, error) {
	return Blob{}, errors.New("not implemented")
}

func (s *orphanBlobStoreStub) Open(context.Context, media.AccessPolicy, string) (BlobReader, error) {
	return BlobReader{}, errors.New("not implemented")
}

func (s *orphanBlobStoreStub) Delete(ctx context.Context, _ media.AccessPolicy, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleted = append(s.deleted, key)
	return s.deleteErrs[key]
}

func (s *orphanBlobStoreStub) ListTenantBlobs(ctx context.Context, tenantID uint64) ([]Blob, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]Blob, 0, len(s.blobs))
	for _, blob := range s.blobs {
		if strings.HasPrefix(blob.Key, fmt.Sprintf("tenant-%d/", tenantID)) {
			result = append(result, blob)
		}
	}
	return result, nil
}

func TestWorkerCollectOrphanBlobsPreservesReferencedOriginalsDerivativesRecentAndOtherTenant(t *testing.T) {
	now := time.Date(2026, time.September, 2, 10, 0, 0, 0, time.UTC)
	old := now.Add(-2 * time.Hour)
	store := &orphanBlobStoreStub{blobs: []Blob{
		{Key: "tenant-7/user-11/session-13/original.png", CreatedAt: old},
		{Key: "tenant-7/user-11/session-13/thumb.webp", CreatedAt: old},
		{Key: "tenant-7/user-11/session-13/orphan.png", CreatedAt: old},
		{Key: "tenant-7/user-11/session-13/recent.png", CreatedAt: now.Add(-30 * time.Minute)},
		{Key: "tenant-8/user-12/session-14/other.png", CreatedAt: old},
	}}
	repo := &executorRepositoryStub{mediaBlobPaths: []string{
		"tenant-7/user-11/session-13/original.png",
		"tenant-7/user-11/session-13/thumb.webp",
	}}
	worker := NewWorker(ImageWorkerConfig{
		Repository:          repo,
		BlobStore:           store,
		TenantID:            7,
		WorkerID:            "worker-a",
		OrphanBlobRetention: time.Hour,
		Now:                 func() time.Time { return now },
	})

	result, err := worker.CollectOrphanBlobs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Scanned != 4 || result.Referenced != 2 || result.Eligible != 1 || result.Deleted != 1 || result.Failed != 0 {
		t.Fatalf("result=%+v", result)
	}
	store.mu.Lock()
	deleted := append([]string(nil), store.deleted...)
	store.mu.Unlock()
	if len(deleted) != 1 || deleted[0] != "tenant-7/user-11/session-13/orphan.png" {
		t.Fatalf("deleted=%v", deleted)
	}
}

func TestWorkerCollectOrphanBlobsContinuesAfterDeleteFailure(t *testing.T) {
	now := time.Date(2026, time.September, 2, 10, 0, 0, 0, time.UTC)
	deleteErr := errors.New("blob unavailable")
	store := &orphanBlobStoreStub{
		blobs: []Blob{
			{Key: "tenant-7/user-11/session-13/a.png", CreatedAt: now.Add(-2 * time.Hour)},
			{Key: "tenant-7/user-11/session-13/b.png", CreatedAt: now.Add(-2 * time.Hour)},
		},
		deleteErrs: map[string]error{"tenant-7/user-11/session-13/a.png": deleteErr},
	}
	worker := NewWorker(ImageWorkerConfig{Repository: &executorRepositoryStub{}, BlobStore: store, TenantID: 7, WorkerID: "worker-a", OrphanBlobRetention: time.Hour, Now: func() time.Time { return now }})

	result, err := worker.CollectOrphanBlobs(context.Background())
	if !errors.Is(err, deleteErr) || result.Eligible != 2 || result.Deleted != 1 || result.Failed != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	store.mu.Lock()
	deleted := append([]string(nil), store.deleted...)
	store.mu.Unlock()
	if len(deleted) != 2 {
		t.Fatalf("deleted=%v", deleted)
	}
}

func TestWorkerCollectOrphanBlobsStopsOnLifecycleCancellation(t *testing.T) {
	store := &orphanBlobStoreStub{blobs: []Blob{{Key: "tenant-7/user-11/session-13/orphan.png", CreatedAt: time.Now().Add(-time.Hour)}}}
	worker := NewWorker(ImageWorkerConfig{Repository: &executorRepositoryStub{}, BlobStore: store, TenantID: 7, WorkerID: "worker-a", OrphanBlobRetention: time.Minute})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := worker.CollectOrphanBlobs(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	store.mu.Lock()
	deleted := append([]string(nil), store.deleted...)
	store.mu.Unlock()
	if len(deleted) != 0 {
		t.Fatalf("deleted=%v", deleted)
	}
}

func TestWorkerCollectOrphanBlobsRejectsMalformedInventoryKeyBeforeDelete(t *testing.T) {
	now := time.Date(2026, time.September, 2, 10, 0, 0, 0, time.UTC)
	store := &orphanBlobStoreStub{blobs: []Blob{{Key: "tenant-7/user-11/session-13/../../victim.png", CreatedAt: now.Add(-2 * time.Hour)}}}
	worker := NewWorker(ImageWorkerConfig{Repository: &executorRepositoryStub{}, BlobStore: store, TenantID: 7, WorkerID: "worker-a", OrphanBlobRetention: time.Hour, Now: func() time.Time { return now }})

	result, err := worker.CollectOrphanBlobs(context.Background())
	if err != nil || result.Scanned != 0 || result.Eligible != 0 || result.Deleted != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	store.mu.Lock()
	deleted := append([]string(nil), store.deleted...)
	store.mu.Unlock()
	if len(deleted) != 0 {
		t.Fatalf("delete called for malformed key: %v", deleted)
	}
}
