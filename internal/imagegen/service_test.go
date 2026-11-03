package imagegen

import (
	"context"
	"errors"
	"github.com/konglong87/super-niuma/internal/media"
	"strings"
	"testing"
	"time"
)

type stubProvider struct{ calls int }

func (s *stubProvider) Generate(context.Context, ProviderGenerateRequest) (ProviderImage, error) {
	s.calls++
	return ProviderImage{Data: []byte("x"), MediaType: "image/png"}, nil
}
func (s *stubProvider) Edit(context.Context, ProviderEditRequest) (ProviderImage, error) {
	s.calls++
	return ProviderImage{Data: []byte("x"), MediaType: "image/png"}, nil
}

type linkingGenerationStore struct{ assetID string }

func (s *linkingGenerationStore) FindImageGenerationByIdempotency(context.Context, uint64, uint64, uint64, string) (GenerationRecord, error) {
	return GenerationRecord{}, errors.New("not found")
}

func (s *linkingGenerationStore) CreateImageGeneration(_ context.Context, record GenerationRecord) (GenerationRecord, error) {
	return record, nil
}

func (s *linkingGenerationStore) UpdateImageGenerationStatus(context.Context, uint64, uint64, uint64, string, string, string, string, *time.Time) error {
	return nil
}

func (s *linkingGenerationStore) SetImageGenerationAsset(_ context.Context, _ uint64, _ uint64, _ uint64, _ string, assetID string) error {
	s.assetID = assetID
	return nil
}

type persistenceErrorStore struct {
	createErr    error
	updateErr    error
	updateCtxErr error
	deadline     time.Time
	hasDeadline  bool
	status       string
}

func (s *persistenceErrorStore) FindImageGenerationByIdempotency(context.Context, uint64, uint64, uint64, string) (GenerationRecord, error) {
	return GenerationRecord{}, ErrGenerationNotFound
}

func (s *persistenceErrorStore) CreateImageGeneration(_ context.Context, record GenerationRecord) (GenerationRecord, error) {
	if s.createErr != nil {
		return GenerationRecord{}, s.createErr
	}
	return record, nil
}

func (s *persistenceErrorStore) UpdateImageGenerationStatus(ctx context.Context, _ uint64, _ uint64, _ uint64, _ string, status, _, _ string, _ *time.Time) error {
	s.updateCtxErr = ctx.Err()
	s.deadline, s.hasDeadline = ctx.Deadline()
	s.status = status
	return s.updateErr
}

type failingProvider struct {
	err    error
	onCall func()
}

func (p failingProvider) Generate(context.Context, ProviderGenerateRequest) (ProviderImage, error) {
	if p.onCall != nil {
		p.onCall()
	}
	return ProviderImage{}, p.err
}

func (p failingProvider) Edit(context.Context, ProviderEditRequest) (ProviderImage, error) {
	if p.onCall != nil {
		p.onCall()
	}
	return ProviderImage{}, p.err
}

func TestServiceGeneratePersistsArtifactAndIdempotency(t *testing.T) {
	p := &stubProvider{}
	ms := media.NewMemoryStore()
	bs := NewMemoryBlobStore()
	s := NewService(ServiceConfig{Provider: p, BlobStore: bs, MediaStore: ms, ProviderName: "jiuan", Model: "gpt-image-2"})
	req := GenerateRequest{TenantID: 1, UserID: 2, SessionID: 3, Prompt: "cat", IdempotencyKey: "k"}
	a, err := s.Generate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Generate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if a.AssetID != b.AssetID || p.calls != 1 {
		t.Fatalf("a=%+v b=%+v calls=%d", a, b, p.calls)
	}
	asset, err := ms.Get(context.Background(), media.AccessPolicy{TenantID: 1, UserID: 2, SessionID: 3}, a.AssetID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bs.Open(context.Background(), media.AccessPolicy{TenantID: 1, UserID: 2, SessionID: 3}, asset.Original.Path); err != nil {
		t.Fatal(err)
	}
}

func TestServiceGeneratePersistsSelectedProvider(t *testing.T) {
	s := NewService(ServiceConfig{Provider: &stubProvider{}, BlobStore: NewMemoryBlobStore(), MediaStore: media.NewMemoryStore(), ProviderName: "default", Model: "gpt-image-2"})
	artifact, err := s.Generate(context.Background(), GenerateRequest{TenantID: 1, UserID: 2, SessionID: 3, Provider: "agnes", Model: "agnes-image-2.5-flash", Prompt: "cat"})
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Provider != "agnes" || artifact.Model != "agnes-image-2.5-flash" {
		t.Fatalf("artifact=%+v", artifact)
	}
}

func TestServiceRejectsIdempotencyReuseWithDifferentParameters(t *testing.T) {
	s := NewService(ServiceConfig{Provider: &stubProvider{}, BlobStore: NewMemoryBlobStore(), MediaStore: media.NewMemoryStore(), ProviderName: "agnes", Model: "agnes-image-2.5-flash"})
	req := GenerateRequest{TenantID: 1, UserID: 2, SessionID: 3, Prompt: "cat", Resolution: "2K", IdempotencyKey: "same"}
	if _, err := s.Generate(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	req.Resolution = "4K"
	if _, err := s.Generate(context.Background(), req); err == nil || !strings.Contains(err.Error(), "different image parameters") {
		t.Fatalf("err=%v", err)
	}
}

func TestServiceGenerateLinksGenerationToPersistedAsset(t *testing.T) {
	links := &linkingGenerationStore{}
	s := NewService(ServiceConfig{Provider: &stubProvider{}, BlobStore: NewMemoryBlobStore(), MediaStore: media.NewMemoryStore(), GenerationStore: links, ProviderName: "jiuan", Model: "gpt-image-2"})
	artifact, err := s.Generate(context.Background(), GenerateRequest{TenantID: 1, UserID: 2, SessionID: 3, Prompt: "cat"})
	if err != nil {
		t.Fatal(err)
	}
	if links.assetID != artifact.AssetID || links.assetID == "" {
		t.Fatalf("linked asset=%q artifact=%+v", links.assetID, artifact)
	}
}

func TestServiceGenerateReturnsGenerationCreateErrorBeforeCallingProvider(t *testing.T) {
	provider := &stubProvider{}
	want := errors.New("generation database unavailable")
	s := NewService(ServiceConfig{Provider: provider, BlobStore: NewMemoryBlobStore(), MediaStore: media.NewMemoryStore(), GenerationStore: &persistenceErrorStore{createErr: want}, ProviderName: "jiuan", Model: "gpt-image-2"})
	_, err := s.Generate(context.Background(), GenerateRequest{TenantID: 1, UserID: 2, SessionID: 3, Prompt: "cat"})
	if !errors.Is(err, want) || provider.calls != 0 {
		t.Fatalf("err=%v provider calls=%d", err, provider.calls)
	}
}

func TestServiceGenerateUsesDetachedBoundedContextForFailureFinalization(t *testing.T) {
	providerErr := errors.New("provider request failed")
	persistErr := errors.New("status database unavailable")
	store := &persistenceErrorStore{updateErr: persistErr}
	ctx, cancel := context.WithCancel(context.Background())
	s := NewService(ServiceConfig{Provider: failingProvider{err: providerErr, onCall: cancel}, BlobStore: NewMemoryBlobStore(), MediaStore: media.NewMemoryStore(), GenerationStore: store, ProviderName: "jiuan", Model: "gpt-image-2"})
	_, err := s.Generate(ctx, GenerateRequest{TenantID: 1, UserID: 2, SessionID: 3, Prompt: "cat"})
	if !errors.Is(err, providerErr) || !errors.Is(err, persistErr) || store.status != GenerationStatusFailed {
		t.Fatalf("err=%v status=%q", err, store.status)
	}
	if store.updateCtxErr != nil {
		t.Fatalf("finalization context error=%v", store.updateCtxErr)
	}
	if !store.hasDeadline || time.Until(store.deadline) > synchronousFinalizeTimeout || time.Until(store.deadline) <= 0 {
		t.Fatalf("finalization deadline=%v ok=%t", store.deadline, store.hasDeadline)
	}
}

func TestServiceGenerateReturnsCompletedStatusPersistenceError(t *testing.T) {
	want := errors.New("completed status database unavailable")
	store := &persistenceErrorStore{updateErr: want}
	s := NewService(ServiceConfig{Provider: &stubProvider{}, BlobStore: NewMemoryBlobStore(), MediaStore: media.NewMemoryStore(), GenerationStore: store, ProviderName: "jiuan", Model: "gpt-image-2"})
	_, err := s.Generate(context.Background(), GenerateRequest{TenantID: 1, UserID: 2, SessionID: 3, Prompt: "cat"})
	if !errors.Is(err, want) || store.status != GenerationStatusCompleted {
		t.Fatalf("err=%v status=%q", err, store.status)
	}
}

func TestServiceValidatePromptAndScope(t *testing.T) {
	s := NewService(ServiceConfig{Provider: &stubProvider{}, BlobStore: NewMemoryBlobStore(), MediaStore: media.NewMemoryStore(), MaxPromptChars: 3})
	_, err := s.Generate(context.Background(), GenerateRequest{TenantID: 1, UserID: 1, SessionID: 1, Prompt: "long"})
	if err == nil {
		t.Fatal("expected prompt validation")
	}
	_, err = s.Generate(context.Background(), GenerateRequest{Prompt: "x"})
	if err == nil {
		t.Fatal("expected scope validation")
	}
	if !errors.Is(err, ErrInvalidImageRequest) && !strings.Contains(err.Error(), "required") {
		t.Fatalf("err=%v", err)
	}
}
