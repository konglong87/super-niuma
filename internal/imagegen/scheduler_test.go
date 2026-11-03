package imagegen

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/media"
)

type schedulerStore struct {
	records []GenerationRecord
	calls   int
	limits  QueueLimits
}

func (s *schedulerStore) AdmitImageGeneration(_ context.Context, record GenerationRecord, limits QueueLimits) (GenerationRecord, bool, error) {
	s.calls++
	s.limits = limits
	for _, existing := range s.records {
		if existing.TenantID == record.TenantID && existing.UserID == record.UserID && existing.SessionID == record.SessionID && existing.IdempotencyKey == record.IdempotencyKey {
			return existing, false, nil
		}
	}
	s.records = append(s.records, record)
	return record, true, nil
}

func (s *schedulerStore) RequestImageGenerationCancel(context.Context, uint64, uint64, uint64, string) (bool, error) {
	return false, nil
}

func TestSchedulerEnqueueGeneratePersistsNormalizedRequestWithoutCallingProvider(t *testing.T) {
	store := &schedulerStore{}
	scheduler := NewScheduler(SchedulerConfig{Repository: store, ProviderName: "jiuan", Model: "gpt-image-2", Quality: "auto", Size: "auto", OutputFormat: "png", Background: "auto"})
	receipt, err := scheduler.EnqueueGenerate(context.Background(), EnqueueGenerateRequest{
		Scope: JobScope{TenantID: 7, UserID: 11, SessionID: 13}, Prompt: "a red fox", Quality: "HIGH",
		Origin:     OriginMetadata{Type: OriginTypeChannel, RefJSON: `{"version":1,"conversation_id":"c-1"}`},
		Invocation: RuntimeInvocation{RunID: "run-1", ToolUseID: "tool-1"}, BatchID: "batch-1", TraceID: "trace-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.GenerationID == "" || receipt.Status != GenerationStatusQueued || receipt.BatchID != "batch-1" || receipt.AcceptedAt.IsZero() {
		t.Fatalf("receipt=%+v", receipt)
	}
	if store.calls != 1 || len(store.records) != 1 {
		t.Fatalf("calls=%d records=%d", store.calls, len(store.records))
	}
	record := store.records[0]
	if record.Status != GenerationStatusQueued || record.Provider != "jiuan" || record.Model != "gpt-image-2" || record.IdempotencyKey == "" || len(record.IdempotencyKey) != 72 {
		t.Fatalf("record=%+v", record)
	}
	if strings.Contains(record.RequestJSON, "credential") || strings.Contains(record.RequestJSON, "secret") {
		t.Fatalf("request JSON contains a credential field: %s", record.RequestJSON)
	}
	var persisted map[string]any
	if err := json.Unmarshal([]byte(record.RequestJSON), &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted["quality"] != "high" || persisted["size"] != "auto" || persisted["output_format"] != "png" || persisted["background"] != "auto" {
		t.Fatalf("normalized request=%v", persisted)
	}
}

func TestSchedulerEnqueueEditAuthorizesSourceAssetAndPersistsBatchMetadata(t *testing.T) {
	assets := media.NewMemoryStore()
	if err := assets.Put(context.Background(), media.Asset{AssetID: "asset-1", Kind: media.KindImage, Name: "source.png", MediaType: "image/png", TenantID: 7, UserID: 11, SessionID: 13, Access: media.AccessPolicy{TenantID: 7, UserID: 11, SessionID: 13}}); err != nil {
		t.Fatal(err)
	}
	store := &schedulerStore{}
	scheduler := NewScheduler(SchedulerConfig{Repository: store, MediaStore: assets, ProviderName: "jiuan", Model: "gpt-image-2"})
	receipt, err := scheduler.EnqueueEdit(context.Background(), EnqueueEditRequest{
		Scope: JobScope{TenantID: 7, UserID: 11, SessionID: 13}, Prompt: "make it blue", SourceAssetID: "asset-1", BatchID: "batch-2",
		Origin: OriginMetadata{Type: OriginTypeChannel}, Invocation: RuntimeInvocation{RunID: "run-2", ToolUseID: "tool-2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.BatchID != "batch-2" || store.records[0].SourceAssetID != "asset-1" || store.records[0].BatchID != "batch-2" {
		t.Fatalf("receipt=%+v record=%+v", receipt, store.records[0])
	}
	_, err = scheduler.EnqueueEdit(context.Background(), EnqueueEditRequest{
		Scope: JobScope{TenantID: 7, UserID: 99, SessionID: 13}, Prompt: "steal it", SourceAssetID: "asset-1",
		Origin: OriginMetadata{Type: OriginTypeChannel}, Invocation: RuntimeInvocation{RunID: "run-2", ToolUseID: "tool-3"},
	})
	if !errors.Is(err, media.ErrForbidden) {
		t.Fatalf("err=%v", err)
	}
}

func TestSchedulerRequiresRuntimeInvocationAndReusesDuplicateReceipt(t *testing.T) {
	store := &schedulerStore{}
	scheduler := NewScheduler(SchedulerConfig{Repository: store, ProviderName: "jiuan", Model: "gpt-image-2"})
	request := EnqueueGenerateRequest{Scope: JobScope{TenantID: 7, UserID: 11, SessionID: 13}, Prompt: "a cat", Origin: OriginMetadata{Type: OriginTypeChannel}}
	if _, err := scheduler.EnqueueGenerate(context.Background(), request); !errors.Is(err, ErrInvalidImageRequest) {
		t.Fatalf("missing invocation err=%v", err)
	}
	request.Invocation = RuntimeInvocation{RunID: strings.Repeat("r", 512), ToolUseID: strings.Repeat("t", 512)}
	first, err := scheduler.EnqueueGenerate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := scheduler.EnqueueGenerate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.GenerationID != second.GenerationID || first.AcceptedAt != second.AcceptedAt || store.calls != 2 || len(store.records) != 1 {
		t.Fatalf("first=%+v second=%+v calls=%d records=%d", first, second, store.calls, len(store.records))
	}
}

func TestSchedulerRejectsIdempotencyReuseWithDifferentParameters(t *testing.T) {
	store := &schedulerStore{}
	scheduler := NewScheduler(SchedulerConfig{Repository: store, ProviderName: "agnes", Model: "agnes-image-2.5-flash"})
	request := EnqueueGenerateRequest{Scope: JobScope{TenantID: 7, UserID: 11, SessionID: 13}, Prompt: "cat", Provider: "agnes", Model: "agnes-image-2.5-flash", Resolution: "2K", Origin: OriginMetadata{Type: OriginTypeChannel}, Invocation: RuntimeInvocation{RunID: "run-1", ToolUseID: "tool-1"}}
	if _, err := scheduler.EnqueueGenerate(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	request.Resolution = "4K"
	if _, err := scheduler.EnqueueGenerate(context.Background(), request); err == nil || !strings.Contains(err.Error(), "different image parameters") {
		t.Fatalf("err=%v", err)
	}
}

func TestSchedulerRuntimeIdempotencyIsScopedToTenantUserAndSession(t *testing.T) {
	store := &schedulerStore{}
	scheduler := NewScheduler(SchedulerConfig{Repository: store, ProviderName: "jiuan", Model: "gpt-image-2"})
	invocation := RuntimeInvocation{RunID: "run-1", ToolUseID: "tool-1"}
	for _, scope := range []JobScope{
		{TenantID: 7, UserID: 11, SessionID: 13},
		{TenantID: 8, UserID: 11, SessionID: 13},
		{TenantID: 7, UserID: 12, SessionID: 13},
		{TenantID: 7, UserID: 11, SessionID: 14},
	} {
		if _, err := scheduler.EnqueueGenerate(context.Background(), EnqueueGenerateRequest{Scope: scope, Prompt: "a cat", Origin: OriginMetadata{Type: OriginTypeChannel}, Invocation: invocation}); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.records) != 4 {
		t.Fatalf("records=%+v", store.records)
	}
}

func TestSchedulerUsesAtomicAdmissionWithQueueLimits(t *testing.T) {
	store := &schedulerStore{}
	scheduler := NewScheduler(SchedulerConfig{Repository: store, ProviderName: "jiuan", Model: "gpt-image-2", MaxQueuedPerTenant: 2, MaxQueuedPerUser: 1})
	_, err := scheduler.EnqueueGenerate(context.Background(), EnqueueGenerateRequest{Scope: JobScope{TenantID: 7, UserID: 11, SessionID: 13}, Prompt: "a cat", Origin: OriginMetadata{Type: OriginTypeChannel}, Invocation: RuntimeInvocation{RunID: "run-1", ToolUseID: "tool-1"}})
	if err != nil || store.calls != 1 || store.limits.MaxQueuedPerTenant != 2 || store.limits.MaxQueuedPerUser != 1 {
		t.Fatalf("err=%v calls=%d limits=%+v", err, store.calls, store.limits)
	}
}

func TestSchedulerReceiptUsesPersistedCreationTime(t *testing.T) {
	now := time.Date(2026, time.September, 2, 1, 2, 3, 0, time.UTC)
	store := &schedulerStore{}
	scheduler := NewScheduler(SchedulerConfig{Repository: store, ProviderName: "jiuan", Model: "gpt-image-2", Now: func() time.Time { return now }})
	receipt, err := scheduler.EnqueueGenerate(context.Background(), EnqueueGenerateRequest{Scope: JobScope{TenantID: 7, UserID: 11, SessionID: 13}, Prompt: "cat", Origin: OriginMetadata{Type: OriginTypeChannel}, Invocation: RuntimeInvocation{RunID: "run", ToolUseID: "tool"}})
	if err != nil || !receipt.AcceptedAt.Equal(now) {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
}
