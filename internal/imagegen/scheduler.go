package imagegen

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/media"
)

var (
	ErrImageQueueFull             = errors.New("image generation queue is full")
	ErrImageManualRetryNotAllowed = errors.New("image generation cannot be manually retried from its current state")
)

const defaultImageJobMaxAttempts uint = 3

// SchedulerRepository is the narrow durable boundary required at acceptance
// time. Its enqueue implementation owns the database uniqueness guard.
type SchedulerRepository interface {
	AdmitImageGeneration(context.Context, GenerationRecord, QueueLimits) (GenerationRecord, bool, error)
	RequestImageGenerationCancel(context.Context, uint64, uint64, uint64, string) (bool, error)
}

type SchedulerConfig struct {
	Repository                              SchedulerRepository
	MediaStore                              media.Store
	ProviderName, Model                     string
	Quality, Size, OutputFormat, Background string
	MaxPromptChars                          int
	MaxQueuedPerTenant                      int
	MaxQueuedPerUser                        int
	MaxAttempts                             uint
	Now                                     func() time.Time
}

type EnqueueGenerateRequest struct {
	Scope                                    JobScope
	Provider, Prompt, Model, Quality, Size   string
	Resolution, AspectRatio                  string
	Watermark                                *bool
	OutputFormat, Background                 string
	BatchID, TraceID, BusinessIdempotencyKey string
	Origin                                   OriginMetadata
	Invocation                               RuntimeInvocation
}

type EnqueueEditRequest struct {
	Scope                                    JobScope
	Provider, Prompt, Model, Quality, Size   string
	Resolution, AspectRatio                  string
	Watermark                                *bool
	OutputFormat, Background, SourceAssetID  string
	BatchID, TraceID, BusinessIdempotencyKey string
	Origin                                   OriginMetadata
	Invocation                               RuntimeInvocation
}

type normalizedJobRequest struct {
	Operation     string `json:"operation"`
	Provider      string `json:"provider,omitempty"`
	Prompt        string `json:"prompt"`
	Model         string `json:"model"`
	Quality       string `json:"quality"`
	Size          string `json:"size"`
	Resolution    string `json:"resolution,omitempty"`
	AspectRatio   string `json:"aspect_ratio,omitempty"`
	Watermark     *bool  `json:"watermark,omitempty"`
	OutputFormat  string `json:"output_format"`
	Background    string `json:"background"`
	SourceAssetID string `json:"source_asset_id,omitempty"`
}

type imageScheduler struct{ cfg SchedulerConfig }

func NewScheduler(cfg SchedulerConfig) Scheduler {
	if cfg.MaxPromptChars <= 0 {
		cfg.MaxPromptChars = 8000
	}
	if cfg.Model == "" {
		cfg.Model = "gpt-image-2"
	}
	if cfg.Quality == "" {
		cfg.Quality = "auto"
	}
	if cfg.Size == "" {
		cfg.Size = "auto"
	}
	if cfg.OutputFormat == "" {
		cfg.OutputFormat = "png"
	}
	if cfg.Background == "" {
		cfg.Background = "auto"
	}
	if cfg.MaxAttempts == 0 {
		cfg.MaxAttempts = defaultImageJobMaxAttempts
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &imageScheduler{cfg: cfg}
}

func (s *imageScheduler) EnqueueGenerate(ctx context.Context, req EnqueueGenerateRequest) (JobReceipt, error) {
	normalized, err := s.normalize(req.Scope, req.Provider, req.Prompt, req.Model, req.Quality, req.Size, req.Resolution, req.AspectRatio, req.Watermark, req.OutputFormat, req.Background, OperationGenerate, "")
	if err != nil {
		return JobReceipt{}, err
	}
	return s.enqueue(ctx, req.Scope, normalized, req.BatchID, req.TraceID, req.BusinessIdempotencyKey, req.Origin, req.Invocation)
}

func (s *imageScheduler) EnqueueEdit(ctx context.Context, req EnqueueEditRequest) (JobReceipt, error) {
	if strings.TrimSpace(req.SourceAssetID) == "" {
		return JobReceipt{}, fmt.Errorf("%w: source asset is required", ErrInvalidImageRequest)
	}
	if s.cfg.MediaStore == nil {
		return JobReceipt{}, fmt.Errorf("%w: source asset store unavailable", ErrInvalidImageRequest)
	}
	asset, err := s.cfg.MediaStore.Get(ctx, media.AccessPolicy{TenantID: req.Scope.TenantID, UserID: req.Scope.UserID, SessionID: req.Scope.SessionID}, strings.TrimSpace(req.SourceAssetID))
	if err != nil {
		return JobReceipt{}, err
	}
	if !strings.HasPrefix(strings.ToLower(asset.MediaType), "image/") {
		return JobReceipt{}, fmt.Errorf("%w: source must be an image", ErrInvalidImageRequest)
	}
	normalized, err := s.normalize(req.Scope, req.Provider, req.Prompt, req.Model, req.Quality, req.Size, req.Resolution, req.AspectRatio, req.Watermark, req.OutputFormat, req.Background, OperationEdit, asset.AssetID)
	if err != nil {
		return JobReceipt{}, err
	}
	return s.enqueue(ctx, req.Scope, normalized, req.BatchID, req.TraceID, req.BusinessIdempotencyKey, req.Origin, req.Invocation)
}

func (s *imageScheduler) RequestCancel(ctx context.Context, scope JobScope, generationID string) error {
	if s.cfg.Repository == nil || !validJobScope(scope) || strings.TrimSpace(generationID) == "" {
		return fmt.Errorf("%w: scheduler scope and generation id are required", ErrInvalidImageRequest)
	}
	_, err := s.cfg.Repository.RequestImageGenerationCancel(ctx, scope.TenantID, scope.UserID, scope.SessionID, strings.TrimSpace(generationID))
	return err
}

func (s *imageScheduler) enqueue(ctx context.Context, scope JobScope, normalized normalizedJobRequest, batchID, traceID, businessKey string, origin OriginMetadata, invocation RuntimeInvocation) (JobReceipt, error) {
	if s.cfg.Repository == nil {
		return JobReceipt{}, fmt.Errorf("%w: scheduler repository unavailable", ErrInvalidImageRequest)
	}
	if !validJobScope(scope) || strings.TrimSpace(invocation.RunID) == "" || strings.TrimSpace(invocation.ToolUseID) == "" {
		return JobReceipt{}, fmt.Errorf("%w: scope and runtime invocation are required", ErrInvalidImageRequest)
	}
	if !validOrigin(origin) {
		return JobReceipt{}, fmt.Errorf("%w: invalid job origin", ErrInvalidImageRequest)
	}
	key := RuntimeIdempotencyKey(invocation)
	requestJSON, err := json.Marshal(normalized)
	if err != nil {
		return JobReceipt{}, fmt.Errorf("normalize image job request: %w", err)
	}
	now := s.cfg.Now().UTC()
	record := GenerationRecord{
		GenerationID: newID("gen"), TenantID: scope.TenantID, UserID: scope.UserID, SessionID: scope.SessionID,
		Operation: normalized.Operation, Status: GenerationStatusQueued, Prompt: normalized.Prompt, Provider: first(normalized.Provider, strings.TrimSpace(s.cfg.ProviderName)), Model: normalized.Model,
		RequestJSON: string(requestJSON), SourceAssetID: normalized.SourceAssetID, IdempotencyKey: key, BatchID: strings.TrimSpace(batchID), OriginType: origin.Type,
		OriginRefJSON: origin.RefJSON, ToolUseID: strings.TrimSpace(invocation.ToolUseID), MaxAttempts: s.cfg.MaxAttempts, CreatedAt: now, UpdatedAt: now, TraceID: strings.TrimSpace(traceID),
	}
	// Business keys may aid caller diagnostics, but runtime identity is the sole
	// durable uniqueness key and is intentionally never replaced by model input.
	_ = strings.TrimSpace(businessKey)
	persisted, created, err := s.cfg.Repository.AdmitImageGeneration(ctx, record, QueueLimits{MaxQueuedPerTenant: s.cfg.MaxQueuedPerTenant, MaxQueuedPerUser: s.cfg.MaxQueuedPerUser})
	if err != nil {
		return JobReceipt{}, err
	}
	if !created && strings.TrimSpace(persisted.RequestJSON) != strings.TrimSpace(record.RequestJSON) {
		return JobReceipt{}, fmt.Errorf("%w: idempotency key was reused with different image parameters", ErrInvalidImageRequest)
	}
	return receiptFromRecord(persisted), nil
}

func (s *imageScheduler) normalize(scope JobScope, provider, prompt, model, quality, size, resolution, ratio string, watermark *bool, format, background, operation, sourceAssetID string) (normalizedJobRequest, error) {
	if !validJobScope(scope) || strings.TrimSpace(prompt) == "" {
		return normalizedJobRequest{}, fmt.Errorf("%w: tenant, user, session and prompt are required", ErrInvalidImageRequest)
	}
	if len([]rune(prompt)) > s.cfg.MaxPromptChars {
		return normalizedJobRequest{}, fmt.Errorf("%w: prompt too long", ErrInvalidImageRequest)
	}
	normalized := normalizedJobRequest{Operation: operation, Provider: first(provider, s.cfg.ProviderName), Prompt: prompt, Model: first(model, s.cfg.Model), Quality: first(quality, s.cfg.Quality), Size: first(size, s.cfg.Size), Resolution: resolution, AspectRatio: ratio, Watermark: watermark, OutputFormat: first(format, s.cfg.OutputFormat), Background: first(background, s.cfg.Background), SourceAssetID: sourceAssetID}
	for name, value := range map[string]string{"quality": normalized.Quality, "size": normalized.Size, "output_format": normalized.OutputFormat, "background": normalized.Background} {
		if !validOption(name, value) {
			return normalizedJobRequest{}, fmt.Errorf("%w: unsupported %s", ErrInvalidImageRequest, name)
		}
	}
	if resolution != "" && !validResolution(resolution) {
		return normalizedJobRequest{}, fmt.Errorf("%w: unsupported resolution", ErrInvalidImageRequest)
	}
	if ratio != "" && !validAspectRatio(ratio) {
		return normalizedJobRequest{}, fmt.Errorf("%w: unsupported aspect_ratio", ErrInvalidImageRequest)
	}
	return normalized, nil
}

func validJobScope(scope JobScope) bool {
	return scope.TenantID != 0 && scope.UserID != 0 && scope.SessionID != 0
}

func validOrigin(origin OriginMetadata) bool {
	switch origin.Type {
	case OriginTypeDirect, OriginTypeChannel, OriginTypeAgentTask:
		return strings.TrimSpace(origin.RefJSON) == "" || json.Valid([]byte(origin.RefJSON))
	default:
		return false
	}
}

func RuntimeIdempotencyKey(invocation RuntimeInvocation) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(invocation.RunID) + "\x00" + strings.TrimSpace(invocation.ToolUseID)))
	return OriginTypeChannel + ":" + hex.EncodeToString(sum[:])
}

func receiptFromRecord(record GenerationRecord) JobReceipt {
	return JobReceipt{GenerationID: record.GenerationID, BatchID: record.BatchID, Status: JobStatus(record.Status), AcceptedAt: record.CreatedAt}
}
