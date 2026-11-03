package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	channelcontract "github.com/konglong87/super-niuma/internal/channel"
	"github.com/konglong87/super-niuma/internal/imagegen"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

type imageCompletionRepositoryStub struct {
	materialize mysqlstore.ChannelImageCompletionMaterial
	resolveErr  error
	requests    []mysqlstore.MaterializeChannelImageCompletionInput
}

func (s *imageCompletionRepositoryStub) ResolveChannelImageCompletion(_ context.Context, input mysqlstore.ResolveChannelImageCompletionInput) (mysqlstore.ChannelImageCompletionMaterial, error) {
	if s.resolveErr != nil {
		return mysqlstore.ChannelImageCompletionMaterial{}, s.resolveErr
	}
	material := s.materialize
	material.EventID = input.EventID
	material.TenantID = input.TenantID
	material.GenerationID = input.GenerationID
	material.WorkerID = input.WorkerID
	material.Origin = input.Origin
	return material, nil
}

func (s *imageCompletionRepositoryStub) MaterializeChannelImageCompletion(_ context.Context, input mysqlstore.MaterializeChannelImageCompletionInput) error {
	s.requests = append(s.requests, input)
	return nil
}

func TestChannelImageCompletionBuildsScopedDeterministicImageDelivery(t *testing.T) {
	repo := &imageCompletionRepositoryStub{materialize: mysqlstore.ChannelImageCompletionMaterial{
		AccountKey: "art-bot", ExternalChatID: "oc-1", ExternalThreadID: "thread-1",
		GenerationStatus: imagegen.GenerationStatusCompleted,
		UserID:           11, SessionID: 13, AssetID: "asset-1", AssetMediaType: "image/png", AssetName: "result.png", AssetSizeBytes: 123, AssetSHA256: "abc",
	}}
	consumer := NewChannelImageCompletionConsumer(repo)
	event := imagegen.CompletionEvent{ID: 5, TenantID: 7, GenerationID: "gen-1", EventType: imagegen.CompletionEventImageCompleted, OriginType: imagegen.OriginTypeChannel, OriginRefJSON: `{"version":1,"account_id":3,"conversation_id":9,"run_id":"run-1","reply_message_id":"om-source"}`, Status: imagegen.CompletionDeliveryStatusSending, Attempts: 1, LeaseOwner: "dispatcher-a"}

	if err := consumer.ConsumeImageCompletion(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if len(repo.requests) != 1 {
		t.Fatalf("requests=%+v", repo.requests)
	}
	request := repo.requests[0]
	if request.ImageIdempotencyKey != "generation:gen-1:channel:image" || request.EventID != 5 || request.WorkerID != "dispatcher-a" {
		t.Fatalf("request=%+v", request)
	}
	var outbound channelcontract.OutboundMessage
	if err := json.Unmarshal([]byte(request.ImagePayloadJSON), &outbound); err != nil {
		t.Fatal(err)
	}
	if outbound.Provider != channelcontract.ProviderFeishu || outbound.AccountID != "art-bot" || outbound.ExternalChatID != "oc-1" || outbound.ExternalThreadID != "thread-1" || outbound.ReplyToMessageID != "om-source" || outbound.Kind != channelcontract.MessageKindImage || outbound.IdempotencyKey != request.ImageIdempotencyKey || outbound.CorrelationID != "run-1" {
		t.Fatalf("outbound=%+v", outbound)
	}
	if len(outbound.Attachments) != 1 || outbound.Attachments[0].ID != "asset-1" || outbound.Attachments[0].TenantID != 7 || outbound.Attachments[0].UserID != 11 || outbound.Attachments[0].SessionID != 13 || outbound.Attachments[0].URL != "" {
		t.Fatalf("attachments=%+v", outbound.Attachments)
	}
	if request.SummaryPayloadJSON != "" || request.SummaryIdempotencyKey != "" {
		t.Fatalf("unexpected summary=%+v", request)
	}
}

func TestChannelImageOriginV2RoundTripsStrictCompletionScope(t *testing.T) {
	metadata, err := NewChannelImageOriginMetadata(ChannelImageOriginInput{
		TenantID: 7, AccountID: 3, ConversationID: 9, RunID: "run-1", SessionID: 13, UserID: 11,
		ReplyMessageID: "om-source", ThreadID: "thread-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	event := imagegen.CompletionEvent{ID: 5, TenantID: 7, GenerationID: "gen-1", EventType: imagegen.CompletionEventImageTerminal, OriginType: metadata.Type, OriginRefJSON: metadata.RefJSON, LeaseOwner: "dispatcher-a"}
	origin, err := decodeChannelImageOrigin(event)
	if err != nil {
		t.Fatal(err)
	}
	if origin.Version != ChannelImageOriginVersion || origin.TenantID != 7 || origin.AccountID != 3 || origin.ConversationID != 9 || origin.RunID != "run-1" || origin.SessionID != 13 || origin.UserID != 11 || origin.ReplyMessageID != "om-source" || origin.ThreadID != "thread-1" {
		t.Fatalf("origin=%+v", origin)
	}
	event.OriginRefJSON = strings.TrimSuffix(metadata.RefJSON, "}") + `,"prompt":"secret"}`
	if _, err := decodeChannelImageOrigin(event); err == nil {
		t.Fatal("origin with undeclared prompt field was accepted")
	}
}

func TestChannelImageCompletionRejectsMalformedOrUnownedOrigin(t *testing.T) {
	tests := []struct {
		name    string
		event   imagegen.CompletionEvent
		repoErr error
	}{
		{name: "wrong origin type", event: imagegen.CompletionEvent{ID: 1, TenantID: 7, GenerationID: "gen", EventType: imagegen.CompletionEventImageCompleted, OriginType: imagegen.OriginTypeDirect, OriginRefJSON: `{}`, LeaseOwner: "owner"}},
		{name: "unsupported version", event: imagegen.CompletionEvent{ID: 1, TenantID: 7, GenerationID: "gen", EventType: imagegen.CompletionEventImageCompleted, OriginType: imagegen.OriginTypeChannel, OriginRefJSON: `{"version":2,"account_id":3,"conversation_id":9,"run_id":"run"}`, LeaseOwner: "owner"}},
		{name: "missing ownership", event: imagegen.CompletionEvent{ID: 1, TenantID: 7, GenerationID: "gen", EventType: imagegen.CompletionEventImageCompleted, OriginType: imagegen.OriginTypeChannel, OriginRefJSON: `{"version":1,"account_id":3,"run_id":"run"}`, LeaseOwner: "owner"}},
		{name: "database ownership mismatch", event: imagegen.CompletionEvent{ID: 1, TenantID: 7, GenerationID: "gen", EventType: imagegen.CompletionEventImageCompleted, OriginType: imagegen.OriginTypeChannel, OriginRefJSON: `{"version":1,"account_id":3,"conversation_id":9,"run_id":"run"}`, LeaseOwner: "owner"}, repoErr: mysqlstore.ErrChannelImageCompletionOwnership},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := &imageCompletionRepositoryStub{resolveErr: test.repoErr}
			err := NewChannelImageCompletionConsumer(repo).ConsumeImageCompletion(context.Background(), test.event)
			if err == nil || !strings.Contains(err.Error(), "origin") || len(repo.requests) != 0 {
				t.Fatalf("err=%v requests=%+v", err, repo.requests)
			}
			var permanent interface{ Unwrap() error }
			if !errors.As(err, &permanent) {
				t.Fatalf("error is not classified as permanent: %T", err)
			}
		})
	}
}

func TestChannelImageCompletionRetriesUntilOriginFinalCardIsReady(t *testing.T) {
	repo := &imageCompletionRepositoryStub{resolveErr: mysqlstore.ErrChannelImageCompletionNotReady}
	event := imagegen.CompletionEvent{ID: 5, TenantID: 7, GenerationID: "gen-1", EventType: imagegen.CompletionEventImageCompleted, OriginType: imagegen.OriginTypeChannel, OriginRefJSON: `{"version":1,"account_id":3,"conversation_id":9,"run_id":"run-1"}`, LeaseOwner: "dispatcher-a"}
	err := NewChannelImageCompletionConsumer(repo).ConsumeImageCompletion(context.Background(), event)
	if !errors.Is(err, imagegen.ErrCompletionOriginNotReady) || len(repo.requests) != 0 {
		t.Fatalf("err=%v requests=%+v", err, repo.requests)
	}
}

func TestChannelImageCompletionAddsOneDeterministicTerminalBatchSummary(t *testing.T) {
	repo := &imageCompletionRepositoryStub{materialize: mysqlstore.ChannelImageCompletionMaterial{
		AccountKey: "art-bot", ExternalChatID: "oc-1", UserID: 11, SessionID: 13,
		GenerationStatus: imagegen.GenerationStatusCompleted,
		AssetID:          "asset-1", AssetMediaType: "image/png", AssetName: "result.png",
		Batch: mysqlstore.ImageGenerationBatchProgress{BatchID: "batch-1", Total: 4, Completed: 2, Failed: 2, Terminal: true},
	}}
	event := imagegen.CompletionEvent{ID: 5, TenantID: 7, GenerationID: "gen-1", EventType: imagegen.CompletionEventImageCompleted, OriginType: imagegen.OriginTypeChannel, OriginRefJSON: `{"version":1,"account_id":3,"conversation_id":9,"run_id":"run-1"}`, LeaseOwner: "dispatcher-a"}
	if err := NewChannelImageCompletionConsumer(repo).ConsumeImageCompletion(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	request := repo.requests[0]
	if request.SummaryIdempotencyKey != "run:run-1:session:13:batch:batch-1:channel:summary" {
		t.Fatalf("summary key=%q", request.SummaryIdempotencyKey)
	}
	var summary channelcontract.OutboundMessage
	if err := json.Unmarshal([]byte(request.SummaryPayloadJSON), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.Kind != channelcontract.MessageKindCard || summary.IdempotencyKey != request.SummaryIdempotencyKey || !strings.Contains(string(summary.Card), "2/4 已完成，2 失败，0 生成中") {
		t.Fatalf("summary=%+v card=%s", summary, summary.Card)
	}
}

func TestChannelImageCompletionFailureSkipsImageAndReconcilesTerminalBatch(t *testing.T) {
	repo := &imageCompletionRepositoryStub{materialize: mysqlstore.ChannelImageCompletionMaterial{
		AccountKey: "art-bot", ExternalChatID: "oc-1", GenerationStatus: imagegen.GenerationStatusFailed,
		UserID: 11, SessionID: 13,
		Batch: mysqlstore.ImageGenerationBatchProgress{BatchID: "reused", Total: 2, Completed: 1, Failed: 1, Terminal: true},
	}}
	event := imagegen.CompletionEvent{ID: 6, TenantID: 7, GenerationID: "gen-failed", EventType: imagegen.CompletionEventImageTerminal, OriginType: imagegen.OriginTypeChannel, OriginRefJSON: `{"version":1,"account_id":3,"conversation_id":9,"run_id":"run-1"}`, LeaseOwner: "dispatcher-a"}
	if err := NewChannelImageCompletionConsumer(repo).ConsumeImageCompletion(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	request := repo.requests[0]
	if request.ImageIdempotencyKey != "" || request.ImagePayloadJSON != "" || request.SummaryIdempotencyKey != "run:run-1:session:13:batch:reused:channel:summary" || request.GenerationStatus != imagegen.GenerationStatusFailed {
		t.Fatalf("request=%+v", request)
	}
}

func TestChannelImageCompletionSummaryKeyScopesReusedBatchByRunAndSession(t *testing.T) {
	keys := make([]string, 0, 2)
	for _, test := range []struct {
		runID     string
		sessionID uint64
	}{
		{runID: "run-a", sessionID: 13},
		{runID: "run-b", sessionID: 14},
	} {
		repo := &imageCompletionRepositoryStub{materialize: mysqlstore.ChannelImageCompletionMaterial{
			AccountKey: "art-bot", ExternalChatID: "oc-1", GenerationStatus: imagegen.GenerationStatusFailed,
			UserID: 11, SessionID: test.sessionID,
			Batch: mysqlstore.ImageGenerationBatchProgress{BatchID: "reused", Total: 1, Failed: 1, Terminal: true},
		}}
		event := imagegen.CompletionEvent{ID: 6, TenantID: 7, GenerationID: "gen-failed", EventType: imagegen.CompletionEventImageTerminal, OriginType: imagegen.OriginTypeChannel, OriginRefJSON: `{"version":1,"account_id":3,"conversation_id":9,"run_id":"` + test.runID + `"}`, LeaseOwner: "dispatcher-a"}
		if err := NewChannelImageCompletionConsumer(repo).ConsumeImageCompletion(context.Background(), event); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, repo.requests[0].SummaryIdempotencyKey)
	}
	if keys[0] == keys[1] || len(keys[0]) > 255 || len(keys[1]) > 255 {
		t.Fatalf("keys=%q", keys)
	}
}
