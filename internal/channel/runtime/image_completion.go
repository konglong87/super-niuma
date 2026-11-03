package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	channelcontract "github.com/konglong87/super-niuma/internal/channel"
	"github.com/konglong87/super-niuma/internal/imagegen"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

const (
	legacyChannelImageOriginVersion = 1
	ChannelImageOriginVersion       = 2
)

type ChannelImageOriginInput struct {
	TenantID, AccountID, ConversationID uint64
	RunID                               string
	SessionID, UserID                   uint64
	ReplyMessageID, ThreadID            string
}

// NewChannelImageOriginMetadata is the sole encoder for channel routing
// references. It intentionally accepts identifiers only.
func NewChannelImageOriginMetadata(input ChannelImageOriginInput) (imagegen.OriginMetadata, error) {
	if input.TenantID == 0 || input.AccountID == 0 || input.ConversationID == 0 || input.SessionID == 0 || input.UserID == 0 || strings.TrimSpace(input.RunID) == "" {
		return imagegen.OriginMetadata{}, errors.New("channel image origin ownership is required")
	}
	origin := mysqlstore.ChannelImageCompletionOrigin{
		Version: ChannelImageOriginVersion, TenantID: input.TenantID, AccountID: input.AccountID,
		ConversationID: input.ConversationID, RunID: strings.TrimSpace(input.RunID), SessionID: input.SessionID, UserID: input.UserID,
		ReplyMessageID: strings.TrimSpace(input.ReplyMessageID), ThreadID: strings.TrimSpace(input.ThreadID),
	}
	encoded, err := json.Marshal(origin)
	if err != nil {
		return imagegen.OriginMetadata{}, fmt.Errorf("encode channel image origin: %w", err)
	}
	return imagegen.OriginMetadata{Type: imagegen.OriginTypeChannel, RefJSON: string(encoded)}, nil
}

type ChannelImageCompletionRepository interface {
	ResolveChannelImageCompletion(context.Context, mysqlstore.ResolveChannelImageCompletionInput) (mysqlstore.ChannelImageCompletionMaterial, error)
	MaterializeChannelImageCompletion(context.Context, mysqlstore.MaterializeChannelImageCompletionInput) error
}

type ChannelImageCompletionConsumer struct {
	repo ChannelImageCompletionRepository
}

func NewChannelImageCompletionConsumer(repo ChannelImageCompletionRepository) *ChannelImageCompletionConsumer {
	return &ChannelImageCompletionConsumer{repo: repo}
}

func (c *ChannelImageCompletionConsumer) ConsumeImageCompletion(ctx context.Context, event imagegen.CompletionEvent) error {
	if c == nil || c.repo == nil {
		return imagegen.PermanentCompletionError("invalid_consumer", errors.New("channel image completion consumer is not configured"))
	}
	origin, err := decodeChannelImageOrigin(event)
	if err != nil {
		return imagegen.PermanentCompletionError("invalid_origin", err)
	}
	lookup := mysqlstore.ResolveChannelImageCompletionInput{EventID: event.ID, TenantID: event.TenantID, GenerationID: strings.TrimSpace(event.GenerationID), WorkerID: strings.TrimSpace(event.LeaseOwner), Origin: origin}
	material, err := c.repo.ResolveChannelImageCompletion(ctx, lookup)
	if err != nil {
		switch {
		case errors.Is(err, mysqlstore.ErrChannelImageCompletionNotReady):
			return fmt.Errorf("%w: %v", imagegen.ErrCompletionOriginNotReady, err)
		case errors.Is(err, mysqlstore.ErrChannelImageCompletionOwnership):
			return imagegen.PermanentCompletionError("invalid_origin", err)
		default:
			return err
		}
	}
	request := mysqlstore.MaterializeChannelImageCompletionInput{
		ResolveChannelImageCompletionInput: lookup,
		GenerationStatus:                   material.GenerationStatus,
		SessionID:                          material.SessionID,
		ExpectedBatch:                      material.Batch,
	}
	if material.GenerationStatus == imagegen.GenerationStatusCompleted {
		request.ImageIdempotencyKey = fmt.Sprintf("generation:%s:channel:image", event.GenerationID)
		imagePayload, err := json.Marshal(channelcontract.OutboundMessage{
			Provider: channelcontract.ProviderFeishu, AccountID: material.AccountKey,
			ConversationID: fmt.Sprint(origin.ConversationID), ExternalChatID: material.ExternalChatID,
			ExternalThreadID: material.ExternalThreadID, ReplyToMessageID: origin.ReplyMessageID,
			Kind: channelcontract.MessageKindImage, IdempotencyKey: request.ImageIdempotencyKey, CorrelationID: origin.RunID,
			Attachments: []channelcontract.Attachment{{
				ID: material.AssetID, Type: "image", MediaType: material.AssetMediaType, Name: material.AssetName,
				SizeBytes: material.AssetSizeBytes, SHA256: material.AssetSHA256,
				TenantID: material.TenantID, UserID: material.UserID, SessionID: material.SessionID,
			}},
		})
		if err != nil {
			return err
		}
		request.ImagePayloadJSON = string(imagePayload)
	}
	if material.Batch.Terminal && material.Batch.BatchID != "" {
		request.SummaryIdempotencyKey = fmt.Sprintf("run:%s:session:%d:batch:%s:channel:summary", origin.RunID, material.SessionID, material.Batch.BatchID)
		request.SummaryPayloadJSON, err = channelImageBatchSummaryPayload(material, request.SummaryIdempotencyKey)
		if err != nil {
			return err
		}
	}
	return c.repo.MaterializeChannelImageCompletion(ctx, request)
}

func decodeChannelImageOrigin(event imagegen.CompletionEvent) (mysqlstore.ChannelImageCompletionOrigin, error) {
	if event.ID == 0 || event.TenantID == 0 || strings.TrimSpace(event.GenerationID) == "" || strings.TrimSpace(event.LeaseOwner) == "" || event.EventType != imagegen.CompletionEventImageTerminal || event.OriginType != imagegen.OriginTypeChannel {
		return mysqlstore.ChannelImageCompletionOrigin{}, errors.New("completion event origin is invalid")
	}
	decoder := json.NewDecoder(strings.NewReader(event.OriginRefJSON))
	decoder.DisallowUnknownFields()
	var origin mysqlstore.ChannelImageCompletionOrigin
	if err := decoder.Decode(&origin); err != nil {
		return mysqlstore.ChannelImageCompletionOrigin{}, fmt.Errorf("decode channel image origin: %w", err)
	}
	if err := ensureOriginEOF(decoder); err != nil {
		return mysqlstore.ChannelImageCompletionOrigin{}, err
	}
	if origin.AccountID == 0 || origin.ConversationID == 0 || strings.TrimSpace(origin.RunID) == "" {
		return mysqlstore.ChannelImageCompletionOrigin{}, errors.New("channel image origin version or ownership fields are invalid")
	}
	switch origin.Version {
	case legacyChannelImageOriginVersion:
	case ChannelImageOriginVersion:
		if origin.TenantID != event.TenantID || origin.SessionID == 0 || origin.UserID == 0 {
			return mysqlstore.ChannelImageCompletionOrigin{}, errors.New("channel image origin scope is invalid")
		}
	default:
		return mysqlstore.ChannelImageCompletionOrigin{}, errors.New("channel image origin version or ownership fields are invalid")
	}
	origin.RunID = strings.TrimSpace(origin.RunID)
	origin.ReplyMessageID = strings.TrimSpace(origin.ReplyMessageID)
	origin.ThreadID = strings.TrimSpace(origin.ThreadID)
	return origin, nil
}

func ensureOriginEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("channel image origin has trailing data")
		}
		return fmt.Errorf("decode channel image origin trailing data: %w", err)
	}
	return nil
}

func channelImageBatchSummaryPayload(material mysqlstore.ChannelImageCompletionMaterial, idempotencyKey string) (string, error) {
	pending := material.Batch.Total - material.Batch.Completed - material.Batch.Failed
	if pending < 0 {
		return "", errors.New("channel image batch progress is invalid")
	}
	text := fmt.Sprintf("%d/%d 已完成，%d 失败，%d 生成中", material.Batch.Completed, material.Batch.Total, material.Batch.Failed, pending)
	card, err := json.Marshal(map[string]any{
		"schema": "2.0",
		"header": map[string]any{"title": map[string]string{"tag": "plain_text", "content": "图片生成结果"}},
		"body":   map[string]any{"elements": []any{map[string]string{"tag": "markdown", "content": text}}},
	})
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(channelcontract.OutboundMessage{
		Provider: channelcontract.ProviderFeishu, AccountID: material.AccountKey,
		ConversationID: fmt.Sprint(material.Origin.ConversationID), ExternalChatID: material.ExternalChatID,
		ExternalThreadID: material.ExternalThreadID, Kind: channelcontract.MessageKindCard, Card: card,
		IdempotencyKey: idempotencyKey, CorrelationID: material.Origin.RunID,
	})
	return string(payload), err
}
