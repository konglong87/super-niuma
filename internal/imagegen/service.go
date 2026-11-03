package imagegen

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/konglong87/super-niuma/internal/media"
)

var (
	ErrInvalidImageRequest     = errors.New("invalid image request")
	ErrImageGenerationDisabled = errors.New("image generation disabled")
	ErrGenerationInProgress    = errors.New("image generation in progress")
	ErrGenerationNotFound      = errors.New("image generation not found")
)

const synchronousFinalizeTimeout = 10 * time.Second

var errTerminalStatusPersistence = errors.New("terminal image generation status persistence failed")

type GenerateRequest struct {
	TenantID, UserID, SessionID                                                                                        uint64
	Provider, Prompt, Model, Quality, Size, Resolution, AspectRatio, OutputFormat, Background, IdempotencyKey, TraceID string
	Watermark                                                                                                          *bool
}
type EditRequest struct {
	TenantID, UserID, SessionID                                                                                                                                                   uint64
	Provider, Prompt, Model, Quality, Size, Resolution, AspectRatio, OutputFormat, Background, SourceAssetID, SourceName, SourceType, MaskName, MaskType, IdempotencyKey, TraceID string
	Watermark                                                                                                                                                                     *bool
	Source, Mask                                                                                                                                                                  io.Reader
}

type GenerationStore interface {
	FindImageGenerationByIdempotency(context.Context, uint64, uint64, uint64, string) (GenerationRecord, error)
	CreateImageGeneration(context.Context, GenerationRecord) (GenerationRecord, error)
	UpdateImageGenerationStatus(context.Context, uint64, uint64, uint64, string, string, string, string, *time.Time) error
}

// GenerationAssetLinker attaches the durable media asset after provider output
// has been persisted. It is optional for lightweight stores but required for
// generation history to resolve an image after restart.
type GenerationAssetLinker interface {
	SetImageGenerationAsset(context.Context, uint64, uint64, uint64, string, string) error
}

type ServiceConfig struct {
	Provider                                ImagesProvider
	BlobStore                               BlobStore
	MediaStore                              media.Store
	GenerationStore                         GenerationStore
	ProviderName, Model                     string
	Quality, Size, OutputFormat, Background string
	MaxPromptChars                          int
	MaxInputBytes                           int64
	Timeout                                 time.Duration
	Enabled                                 bool
}
type Service struct {
	cfg          ServiceConfig
	mu           sync.Mutex
	idem         map[string]Artifact
	idemRequests map[string]string
}

func NewService(cfg ServiceConfig) *Service {
	if cfg.MaxPromptChars <= 0 {
		cfg.MaxPromptChars = 8000
	}
	if cfg.MaxInputBytes <= 0 {
		cfg.MaxInputBytes = 25 * 1024 * 1024
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 180 * time.Second
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
	return &Service{cfg: cfg, idem: make(map[string]Artifact), idemRequests: make(map[string]string)}
}

func (s *Service) Generate(ctx context.Context, req GenerateRequest) (Artifact, error) {
	if s.cfg.Provider == nil {
		return Artifact{}, ErrImageGenerationDisabled
	}
	if s.cfg.BlobStore == nil {
		return Artifact{}, fmt.Errorf("%w: blob store unavailable", ErrInvalidImageRequest)
	}
	if err := s.validate(req.TenantID, req.UserID, req.SessionID, req.Prompt, req.Model, req.Quality, req.Size, req.Resolution, req.AspectRatio, req.OutputFormat, req.Background); err != nil {
		return Artifact{}, err
	}
	model := first(req.Model, s.cfg.Model)
	quality := first(req.Quality, s.cfg.Quality)
	size := first(req.Size, s.cfg.Size)
	format := first(req.OutputFormat, s.cfg.OutputFormat)
	bg := first(req.Background, s.cfg.Background)
	providerName := first(req.Provider, s.cfg.ProviderName)
	key := scopeIdem(req.TenantID, req.UserID, req.SessionID, req.IdempotencyKey)
	requestJSON := normalizedRequestJSON(OperationGenerate, providerName, req.Prompt, model, quality, size, req.Resolution, req.AspectRatio, format, bg, "", req.Watermark)
	if a, ok, conflict := s.getIdem(key, requestJSON); conflict {
		return Artifact{}, fmt.Errorf("%w: idempotency key was reused with different image parameters", ErrInvalidImageRequest)
	} else if ok {
		return a, nil
	}
	if a, ok, conflict := s.lookupPersisted(ctx, req.TenantID, req.UserID, req.SessionID, req.IdempotencyKey, requestJSON); conflict {
		return Artifact{}, fmt.Errorf("%w: idempotency key was reused with different image parameters", ErrInvalidImageRequest)
	} else if ok {
		s.setIdem(key, requestJSON, a)
		return a, nil
	}
	genID := newID("gen")
	if err := s.createRecord(ctx, GenerationRecord{GenerationID: genID, TenantID: req.TenantID, UserID: req.UserID, SessionID: req.SessionID, Operation: OperationGenerate, Status: GenerationStatusRunning, Prompt: req.Prompt, Provider: providerName, Model: model, RequestJSON: requestJSON, IdempotencyKey: req.IdempotencyKey, TraceID: req.TraceID, CreatedAt: time.Now().UTC()}); err != nil {
		return Artifact{}, fmt.Errorf("create image generation: %w", err)
	}
	callCtx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	out, err := s.cfg.Provider.Generate(callCtx, ProviderGenerateRequest{Provider: providerName, Prompt: req.Prompt, Model: model, Quality: quality, Size: size, Resolution: req.Resolution, AspectRatio: req.AspectRatio, OutputFormat: format, Background: bg, Watermark: req.Watermark})
	if err != nil {
		return s.fail(ctx, req.TenantID, req.UserID, req.SessionID, genID, err)
	}
	a, err := s.persist(ctx, genID, req.TenantID, req.UserID, req.SessionID, OperationGenerate, "", req.Prompt, providerName, model, format, out, req.IdempotencyKey)
	if err != nil {
		if errors.Is(err, errTerminalStatusPersistence) {
			return Artifact{}, err
		}
		return Artifact{}, s.failErr(ctx, req.TenantID, req.UserID, req.SessionID, genID, err)
	}
	s.setIdem(key, requestJSON, a)
	return a, nil
}

func (s *Service) Edit(ctx context.Context, req EditRequest) (Artifact, error) {
	if s.cfg.Provider == nil {
		return Artifact{}, ErrImageGenerationDisabled
	}
	if s.cfg.BlobStore == nil {
		return Artifact{}, fmt.Errorf("%w: blob store unavailable", ErrInvalidImageRequest)
	}
	if err := s.validate(req.TenantID, req.UserID, req.SessionID, req.Prompt, req.Model, req.Quality, req.Size, req.Resolution, req.AspectRatio, req.OutputFormat, req.Background); err != nil {
		return Artifact{}, err
	}
	model := first(req.Model, s.cfg.Model)
	quality := first(req.Quality, s.cfg.Quality)
	size := first(req.Size, s.cfg.Size)
	format := first(req.OutputFormat, s.cfg.OutputFormat)
	bg := first(req.Background, s.cfg.Background)
	providerName := first(req.Provider, s.cfg.ProviderName)
	idemKey := scopeIdem(req.TenantID, req.UserID, req.SessionID, req.IdempotencyKey)
	requestJSON := normalizedRequestJSON(OperationEdit, providerName, req.Prompt, model, quality, size, req.Resolution, req.AspectRatio, format, bg, req.SourceAssetID, req.Watermark)
	if a, ok, conflict := s.getIdem(idemKey, requestJSON); conflict {
		return Artifact{}, fmt.Errorf("%w: idempotency key was reused with different image parameters", ErrInvalidImageRequest)
	} else if ok {
		return a, nil
	}
	if a, ok, conflict := s.lookupPersisted(ctx, req.TenantID, req.UserID, req.SessionID, req.IdempotencyKey, requestJSON); conflict {
		return Artifact{}, fmt.Errorf("%w: idempotency key was reused with different image parameters", ErrInvalidImageRequest)
	} else if ok {
		s.setIdem(idemKey, requestJSON, a)
		return a, nil
	}
	if (req.Source == nil) == (strings.TrimSpace(req.SourceAssetID) == "") {
		return Artifact{}, fmt.Errorf("%w: exactly one source is required", ErrInvalidImageRequest)
	}
	var source io.Reader = req.Source
	sourceAsset := req.SourceAssetID
	if source == nil {
		if s.cfg.MediaStore == nil || s.cfg.BlobStore == nil {
			return Artifact{}, fmt.Errorf("%w: source asset unavailable", ErrInvalidImageRequest)
		}
		asset, err := s.cfg.MediaStore.Get(ctx, media.AccessPolicy{TenantID: req.TenantID, UserID: req.UserID, SessionID: req.SessionID}, req.SourceAssetID)
		if err != nil {
			return Artifact{}, err
		}
		key := asset.Original.Path
		if key == "" {
			key = asset.Original.URL
		}
		br, err := s.cfg.BlobStore.Open(ctx, media.AccessPolicy{TenantID: req.TenantID, UserID: req.UserID, SessionID: req.SessionID}, key)
		if err != nil {
			return Artifact{}, err
		}
		defer br.Close()
		source = br
		if req.SourceName == "" {
			req.SourceName = asset.Name
		}
		if req.SourceType == "" {
			req.SourceType = asset.MediaType
		}
	}
	if req.SourceType != "" && !strings.HasPrefix(strings.ToLower(req.SourceType), "image/") {
		return Artifact{}, fmt.Errorf("%w: source must be an image", ErrInvalidImageRequest)
	}
	data, err := io.ReadAll(io.LimitReader(source, s.cfg.MaxInputBytes+1))
	if err != nil {
		return Artifact{}, err
	}
	if int64(len(data)) > s.cfg.MaxInputBytes {
		return Artifact{}, fmt.Errorf("%w: input too large", ErrInvalidImageRequest)
	}
	genID := newID("gen")
	requestJSON = normalizedRequestJSON(OperationEdit, providerName, req.Prompt, model, quality, size, req.Resolution, req.AspectRatio, format, bg, sourceAsset, req.Watermark)
	if err := s.createRecord(ctx, GenerationRecord{GenerationID: genID, TenantID: req.TenantID, UserID: req.UserID, SessionID: req.SessionID, Operation: OperationEdit, Status: GenerationStatusRunning, Prompt: req.Prompt, Provider: providerName, Model: model, RequestJSON: requestJSON, SourceAssetID: sourceAsset, IdempotencyKey: req.IdempotencyKey, TraceID: req.TraceID, CreatedAt: time.Now().UTC()}); err != nil {
		return Artifact{}, fmt.Errorf("create image generation: %w", err)
	}
	callCtx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	out, err := s.cfg.Provider.Edit(callCtx, ProviderEditRequest{Provider: providerName, Prompt: req.Prompt, Model: model, Quality: quality, Size: size, Resolution: req.Resolution, AspectRatio: req.AspectRatio, OutputFormat: format, Background: bg, Watermark: req.Watermark, Image: bytes.NewReader(data), ImageName: req.SourceName, ImageType: req.SourceType, Mask: req.Mask, MaskName: req.MaskName, MaskType: req.MaskType})
	if err != nil {
		return s.fail(ctx, req.TenantID, req.UserID, req.SessionID, genID, err)
	}
	a, err := s.persist(ctx, genID, req.TenantID, req.UserID, req.SessionID, OperationEdit, sourceAsset, req.Prompt, providerName, model, format, out, req.IdempotencyKey)
	if err != nil {
		if errors.Is(err, errTerminalStatusPersistence) {
			return Artifact{}, err
		}
		return Artifact{}, s.failErr(ctx, req.TenantID, req.UserID, req.SessionID, genID, err)
	}
	s.setIdem(idemKey, requestJSON, a)
	return a, nil
}

func (s *Service) validate(t, u, se uint64, prompt, model, q, size, resolution, ratio, format, bg string) error {
	if t == 0 || u == 0 || se == 0 || strings.TrimSpace(prompt) == "" {
		return fmt.Errorf("%w: tenant, user, session and prompt are required", ErrInvalidImageRequest)
	}
	if len([]rune(prompt)) > s.cfg.MaxPromptChars {
		return fmt.Errorf("%w: prompt too long", ErrInvalidImageRequest)
	}
	for n, v := range map[string]string{"quality": first(q, s.cfg.Quality), "size": first(size, s.cfg.Size), "output_format": first(format, s.cfg.OutputFormat), "background": first(bg, s.cfg.Background)} {
		if !validOption(n, v) {
			return fmt.Errorf("%w: unsupported %s", ErrInvalidImageRequest, n)
		}
	}
	if resolution != "" && !validResolution(resolution) {
		return fmt.Errorf("%w: unsupported resolution", ErrInvalidImageRequest)
	}
	if ratio != "" && !validAspectRatio(ratio) {
		return fmt.Errorf("%w: unsupported aspect_ratio", ErrInvalidImageRequest)
	}
	return nil
}

func validResolution(value string) bool {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "1K", "2K", "3K", "4K":
		return true
	default:
		return false
	}
}

func validAspectRatio(value string) bool {
	switch strings.TrimSpace(value) {
	case "1:1", "3:4", "4:3", "16:9", "9:16", "2:3", "3:2", "21:9":
		return true
	default:
		return false
	}
}
func validOption(n, v string) bool {
	switch n {
	case "quality":
		return v == "auto" || v == "low" || v == "medium" || v == "high"
	case "size":
		return v == "auto" || v == "1024x1024" || v == "1536x1024" || v == "1024x1536"
	case "output_format":
		return v == "png" || v == "jpeg" || v == "webp"
	case "background":
		return v == "auto" || v == "transparent" || v == "opaque"
	}
	return false
}
func (s *Service) persist(ctx context.Context, genID string, t, u, se uint64, op, source, prompt, provider, model, format string, out ProviderImage, idem string) (Artifact, error) {
	if len(out.Data) == 0 {
		return Artifact{}, fmt.Errorf("%w: provider returned no bytes", ErrInvalidImageRequest)
	}
	mt := out.MediaType
	if mt == "" {
		mt = "image/" + format
	}
	if !strings.HasPrefix(mt, "image/") {
		return Artifact{}, fmt.Errorf("%w: unsupported media type", ErrInvalidImageRequest)
	}
	id := newID("img")
	ext := format
	if ext == "jpeg" {
		ext = "jpg"
	}
	key := fmt.Sprintf("tenant-%d/user-%d/session-%d/asset-%s.%s", t, u, se, id, ext)
	blob, err := s.cfg.BlobStore.Put(ctx, PutBlobRequest{Policy: media.AccessPolicy{TenantID: t, UserID: u, SessionID: se}, Key: key, MediaType: mt, Name: id + "." + ext, Data: bytes.NewReader(out.Data)})
	if err != nil {
		return Artifact{}, err
	}
	w, h := 0, 0
	if cfg, _, e := image.DecodeConfig(bytes.NewReader(out.Data)); e == nil {
		w, h = cfg.Width, cfg.Height
	}
	asset := media.Asset{AssetID: id, Kind: media.KindImage, MediaType: mt, Name: blob.Name, SizeBytes: blob.SizeBytes, SHA256: blob.SHA256, State: media.StateReady, TenantID: t, UserID: u, SessionID: se, Original: media.Variant{Path: key, URL: "/tenant/media/assets/" + id, MediaType: mt, SizeBytes: blob.SizeBytes, SHA256: blob.SHA256}, Access: media.AccessPolicy{TenantID: t, UserID: u, SessionID: se}}
	if s.cfg.MediaStore != nil {
		if err := s.cfg.MediaStore.Put(ctx, asset); err != nil {
			return Artifact{}, err
		}
	}
	if linker, ok := s.cfg.GenerationStore.(GenerationAssetLinker); ok {
		if err := linker.SetImageGenerationAsset(ctx, t, u, se, genID, id); err != nil {
			return Artifact{}, err
		}
	}
	now := time.Now().UTC()
	if err := s.updateTerminalStatus(ctx, t, u, se, genID, GenerationStatusCompleted, "", "", &now); err != nil {
		return Artifact{}, fmt.Errorf("%w: %w", errTerminalStatusPersistence, err)
	}
	return Artifact{AssetID: id, GenerationID: genID, TenantID: t, UserID: u, SessionID: se, Operation: op, SourceAssetID: source, MediaType: mt, Name: blob.Name, Width: w, Height: h, SizeBytes: blob.SizeBytes, SHA256: blob.SHA256, URL: asset.Original.URL, Provider: provider, Model: model, CreatedAt: now}, nil
}
func (s *Service) createRecord(ctx context.Context, r GenerationRecord) error {
	if s.cfg.GenerationStore != nil {
		_, err := s.cfg.GenerationStore.CreateImageGeneration(ctx, r)
		return err
	}
	return nil
}
func (s *Service) updateTerminalStatus(ctx context.Context, t, u, se uint64, id, status, code, msg string, at *time.Time) error {
	if s.cfg.GenerationStore != nil {
		finalizeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), synchronousFinalizeTimeout)
		defer cancel()
		return s.cfg.GenerationStore.UpdateImageGenerationStatus(finalizeCtx, t, u, se, id, status, code, SanitizeErrorMessage(msg), at)
	}
	return nil
}
func (s *Service) fail(ctx context.Context, t, u, se uint64, id string, err error) (Artifact, error) {
	return Artifact{}, s.failErr(ctx, t, u, se, id, err)
}
func (s *Service) failErr(ctx context.Context, t, u, se uint64, id string, err error) error {
	code := "image_provider_error"
	if errors.Is(err, ErrProviderTimeout) || errors.Is(err, context.DeadlineExceeded) {
		code = "image_provider_timeout"
	}
	now := time.Now().UTC()
	if persistErr := s.updateTerminalStatus(ctx, t, u, se, id, GenerationStatusFailed, code, err.Error(), &now); persistErr != nil {
		return errors.Join(err, fmt.Errorf("persist failed image generation: %w", persistErr))
	}
	return err
}
func (s *Service) getIdem(k, requestJSON string) (Artifact, bool, bool) {
	if k == "" {
		return Artifact{}, false, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.idem[k]
	return a, ok, ok && s.idemRequests[k] != requestJSON
}
func (s *Service) setIdem(k, requestJSON string, a Artifact) {
	if k != "" {
		s.mu.Lock()
		s.idem[k] = a
		s.idemRequests[k] = requestJSON
		s.mu.Unlock()
	}
}

func (s *Service) lookupPersisted(ctx context.Context, t, u, se uint64, key, requestJSON string) (Artifact, bool, bool) {
	if s.cfg.GenerationStore == nil || strings.TrimSpace(key) == "" || s.cfg.MediaStore == nil {
		return Artifact{}, false, false
	}
	r, err := s.cfg.GenerationStore.FindImageGenerationByIdempotency(ctx, t, u, se, key)
	if err != nil || r.Status != GenerationStatusCompleted || r.AssetID == "" {
		return Artifact{}, false, false
	}
	if strings.TrimSpace(r.RequestJSON) != "" && r.RequestJSON != requestJSON {
		return Artifact{}, false, true
	}
	asset, err := s.cfg.MediaStore.Get(ctx, media.AccessPolicy{TenantID: t, UserID: u, SessionID: se}, r.AssetID)
	if err != nil {
		return Artifact{}, false, false
	}
	return Artifact{AssetID: asset.AssetID, GenerationID: r.GenerationID, TenantID: t, UserID: u, SessionID: se, Operation: r.Operation, SourceAssetID: r.SourceAssetID, MediaType: asset.MediaType, Name: asset.Name, SizeBytes: asset.SizeBytes, SHA256: asset.SHA256, URL: asset.Original.URL, Provider: r.Provider, Model: r.Model, CreatedAt: r.CreatedAt}, true, false
}

func normalizedRequestJSON(operation, provider, prompt, model, quality, size, resolution, ratio, format, background, sourceAssetID string, watermark *bool) string {
	payload, _ := json.Marshal(normalizedJobRequest{Operation: operation, Provider: strings.TrimSpace(provider), Prompt: prompt, Model: strings.TrimSpace(model), Quality: strings.ToLower(strings.TrimSpace(quality)), Size: strings.ToLower(strings.TrimSpace(size)), Resolution: strings.ToUpper(strings.TrimSpace(resolution)), AspectRatio: strings.TrimSpace(ratio), Watermark: watermark, OutputFormat: strings.ToLower(strings.TrimSpace(format)), Background: strings.ToLower(strings.TrimSpace(background)), SourceAssetID: strings.TrimSpace(sourceAssetID)})
	return string(payload)
}
func scopeIdem(t, u, se uint64, k string) string {
	if strings.TrimSpace(k) == "" {
		return ""
	}
	return fmt.Sprintf("%d/%d/%d/%s", t, u, se, k)
}
func first(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return strings.ToLower(strings.TrimSpace(a))
	}
	return b
}
func newID(prefix string) string {
	b := make([]byte, 12)
	if _, e := rand.Read(b); e != nil {
		return prefix + "-" + fmt.Sprint(time.Now().UnixNano())
	}
	return prefix + "-" + hex.EncodeToString(b)
}
