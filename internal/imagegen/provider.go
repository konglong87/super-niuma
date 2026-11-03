package imagegen

import (
	"context"
	"fmt"
	"io"
	"strings"
)

const (
	ImageProtocolOpenAI    = "openai-images"
	ImageProtocolSenseNova = "sensenova-images"
	ImageProtocolAgnes     = "agnes-images"
)

// Generator is the provider-neutral image service used by runtime tools and
// HTTP handlers. Implementations enforce persistence and tenant ownership.
type Generator interface {
	Generate(context.Context, GenerateRequest) (Artifact, error)
	Edit(context.Context, EditRequest) (Artifact, error)
}

// ArtifactContentReader is an optional capability for consumers that must send
// a persisted image to a model. Implementations authorize and validate the
// asset before returning bounded bytes; browser URLs are not model inputs.
type ArtifactContentReader interface {
	ReadArtifactContent(context.Context, ArtifactContentRequest) (ArtifactContent, error)
}

type ArtifactContentRequest struct {
	TenantID  uint64
	UserID    uint64
	SessionID uint64
	AssetID   string
}

type ArtifactContent struct {
	MediaType string
	Data      []byte
}

// Scheduler accepts durable image jobs without performing provider work.
// Runtime integrations use it for asynchronous image generation while the
// existing Generator remains the synchronous compatibility contract.
type Scheduler interface {
	EnqueueGenerate(context.Context, EnqueueGenerateRequest) (JobReceipt, error)
	EnqueueEdit(context.Context, EnqueueEditRequest) (JobReceipt, error)
	RequestCancel(context.Context, JobScope, string) error
}

type ProviderGenerateRequest struct {
	Provider, Prompt, Model, Quality, Size, Resolution, AspectRatio, OutputFormat, Background string
	Watermark                                                                                 *bool
}

type ProviderEditRequest struct {
	Provider, Prompt, Model, Quality, Size, Resolution, AspectRatio, OutputFormat, Background string
	Watermark                                                                                 *bool
	Image                                                                                     io.Reader
	ImageName, ImageType                                                                      string
	Mask                                                                                      io.Reader
	MaskName, MaskType                                                                        string
}

type ProviderImage struct {
	Data              []byte
	MediaType         string
	URL               string
	ProviderRequestID string
}

type ImagesProvider interface {
	Generate(context.Context, ProviderGenerateRequest) (ProviderImage, error)
	Edit(context.Context, ProviderEditRequest) (ProviderImage, error)
}

// ProviderRegistry routes a normalized request to the configured provider.
// It keeps provider selection out of handlers and preserves the existing
// ImagesProvider contract for callers that use one default provider.
type ProviderRegistry struct {
	defaultName string
	providers   map[string]ImagesProvider
	models      map[string]ProviderModelCapability
}

type ProviderModelCapability struct {
	Provider, Model                                                             string
	Operations, Resolutions, AspectRatios, Sizes, QualityOptions, OutputFormats []string
	SupportsWatermark                                                           bool
}

func NewProviderRegistry(defaultName string, providers map[string]ImagesProvider) *ProviderRegistry {
	copy := make(map[string]ImagesProvider, len(providers))
	for name, provider := range providers {
		if provider != nil {
			copy[strings.ToLower(strings.TrimSpace(name))] = provider
		}
	}
	return &ProviderRegistry{defaultName: strings.TrimSpace(defaultName), providers: copy}
}

func (r *ProviderRegistry) WithCapabilities(capabilities []ProviderModelCapability) *ProviderRegistry {
	if r == nil {
		return r
	}
	r.models = make(map[string]ProviderModelCapability, len(capabilities))
	for _, capability := range capabilities {
		r.models[modelCapabilityKey(capability.Provider, capability.Model)] = capability
	}
	return r
}

func modelCapabilityKey(provider, model string) string {
	return strings.ToLower(strings.TrimSpace(provider)) + "\x00" + strings.ToLower(strings.TrimSpace(model))
}

func (r *ProviderRegistry) validate(provider, model, operation, resolution, ratio, size, quality, format string, watermark *bool) error {
	if r == nil || len(r.models) == 0 {
		return nil
	}
	provider = firstNonBlank(provider, r.defaultName)
	capability, ok := r.models[modelCapabilityKey(provider, model)]
	if !ok {
		return fmt.Errorf("%w: unsupported image provider/model", ErrProviderResponse)
	}
	checks := []struct {
		name, value string
		allowed     []string
	}{{"operation", operation, capability.Operations}, {"resolution", resolution, capability.Resolutions}, {"aspect_ratio", ratio, capability.AspectRatios}, {"size", size, capability.Sizes}, {"quality", quality, capability.QualityOptions}, {"output_format", format, capability.OutputFormats}}
	for _, check := range checks {
		if strings.TrimSpace(check.value) == "" || len(check.allowed) == 0 {
			continue
		}
		matched := false
		for _, allowed := range check.allowed {
			if strings.EqualFold(strings.TrimSpace(allowed), strings.TrimSpace(check.value)) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%w: unsupported %s", ErrProviderResponse, check.name)
		}
	}
	if watermark != nil && !capability.SupportsWatermark {
		return fmt.Errorf("%w: watermark is unsupported", ErrProviderResponse)
	}
	return nil
}

func (r *ProviderRegistry) resolve(name string) (ImagesProvider, error) {
	if r == nil {
		return nil, ErrProviderResponse
	}
	provider := r.providers[strings.ToLower(strings.TrimSpace(firstNonBlank(name, r.defaultName)))]
	if provider == nil {
		return nil, ErrProviderResponse
	}
	return provider, nil
}

func (r *ProviderRegistry) Generate(ctx context.Context, request ProviderGenerateRequest) (ProviderImage, error) {
	if err := r.validate(request.Provider, request.Model, OperationGenerate, request.Resolution, request.AspectRatio, request.Size, request.Quality, request.OutputFormat, request.Watermark); err != nil {
		return ProviderImage{}, err
	}
	provider, err := r.resolve(request.Provider)
	if err != nil {
		return ProviderImage{}, err
	}
	return provider.Generate(ctx, request)
}

func (r *ProviderRegistry) Edit(ctx context.Context, request ProviderEditRequest) (ProviderImage, error) {
	if err := r.validate(request.Provider, request.Model, OperationEdit, request.Resolution, request.AspectRatio, request.Size, request.Quality, request.OutputFormat, request.Watermark); err != nil {
		return ProviderImage{}, err
	}
	provider, err := r.resolve(request.Provider)
	if err != nil {
		return ProviderImage{}, err
	}
	return provider.Edit(ctx, request)
}
