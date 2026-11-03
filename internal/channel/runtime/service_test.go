package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/agentteam"
	"github.com/konglong87/super-niuma/internal/anthropic"
	channelcontract "github.com/konglong87/super-niuma/internal/channel"
	feishuchannel "github.com/konglong87/super-niuma/internal/channel/feishu"
	"github.com/konglong87/super-niuma/internal/imagegen"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

var _ OutboxSequenceRepository = (Repository)(nil)

func TestHandleInboundDispatchesTeamBeforeNormalConversationFlow(t *testing.T) {
	router := agentteam.NewRouter([]agentteam.Binding{{TenantID: 9, TeamID: 21, TeamVersion: 3, Provider: "feishu", AccountKey: "bot-a", ExternalChatID: "oc-team", Trigger: agentteam.TriggerMention}}, nil)
	repo := &fakeRepo{}
	reactionRepo := &teamReactionRepoFake{}
	var dispatched TeamDispatchInput
	service := New(Config{TenantID: 9, AccountID: 4, AccountKey: "bot-a", Repo: repo, Adapter: &fakeAdapter{}, PayloadCodec: JSONCodec{}, TeamRouter: &router, TeamDispatch: func(_ context.Context, input TeamDispatchInput) (TeamDispatchResult, error) {
		dispatched = input
		return TeamDispatchResult{FinalText: "team answer"}, nil
	}, Reactions: NewReactionReconciler(ReactionConfig{TenantID: 9, AccountID: 4, WorkerID: "test-worker", Repo: reactionRepo, Adapter: &fakeAdapter{}}), UserResolver: func(context.Context, channelcontract.InboundMessage) (uint64, error) {
		return 42, nil
	}, SessionResolver: func(context.Context, uint64, uint64, channelcontract.Scope) (uint64, error) {
		return 99, nil
	}})
	disposition := service.HandleInbound(context.Background(), channelcontract.InboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: "bot-a", EventID: "evt-team", ExternalMessageID: "om-team", ExternalConversationID: "oc-team", ExternalUserID: "ou-user", ChatType: channelcontract.ChatTypeGroup, MentionedBot: true, Text: "开始协作"})
	if !disposition.Ack || disposition.Retryable || len(repo.inbox) != 1 || dispatched.Route.TeamID != 0 {
		t.Fatalf("disposition=%+v inbox=%+v dispatched=%+v", disposition, repo.inbox, dispatched)
	}
	if err := service.ProcessOne(context.Background()); err != nil {
		t.Fatalf("team process: %v", err)
	}
	if dispatched.Route.TeamID != 21 || dispatched.Route.TeamVersion != 3 {
		t.Fatalf("dispatched route=%+v", dispatched.Route)
	}
	if repo.finished[repo.run.ID] != mysqlstore.ChannelRunStatusCompleted || len(repo.outbox) != 1 {
		t.Fatalf("finished=%+v outbox=%+v", repo.finished, repo.outbox)
	}
	if err := service.ProcessReactionsOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := service.ProcessReactionsOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if reactionRepo.row.CurrentEmoji != string(channelcontract.ReactionDone) {
		t.Fatalf("terminal reaction=%+v, want DONE", reactionRepo.row)
	}
}

type fakeRepo struct {
	outboxMu        sync.Mutex
	inbox           []mysqlstore.ChannelInboxEvent
	conv            mysqlstore.ChannelConversation
	conversationErr error
	run             mysqlstore.ChannelRun
	messages        []mysqlstore.ChannelMessage
	outbox          []mysqlstore.ChannelOutbox
	outboxReady     chan struct{}
	sentExternal    string
	busy            bool
	rotations       int
	cancelled       bool
	finished        map[string]string
	imageRecord     mysqlstore.ImageGenerationRecord
	imageGetErr     error
	imageGetScope   imagegen.JobScope
	imageRetry      imagegen.GenerationRecord
	imageRetryErr   error
	imageRetryInput imagegen.ManualRetryRequest
	audits          []mysqlstore.AuditLogInput
}

func (f *fakeRepo) ClaimInboxEvent(_ context.Context, input mysqlstore.ChannelInboxEventInput) (mysqlstore.ChannelInboxEvent, bool, error) {
	for _, event := range f.inbox {
		if event.ProviderEventID == input.ProviderEventID {
			return event, false, nil
		}
	}
	event := mysqlstore.ChannelInboxEvent{ID: uint64(len(f.inbox) + 1), TenantID: input.TenantID, AccountID: input.AccountID, ProviderEventID: input.ProviderEventID, Status: mysqlstore.ChannelInboxStatusReceived}
	f.inbox = append(f.inbox, event)
	return event, true, nil
}
func (f *fakeRepo) MarkInboxQueued(_ context.Context, _, eventID, conversationID uint64, scopeHash []byte) error {
	for i := range f.inbox {
		if f.inbox[i].ID == eventID {
			f.inbox[i].ConversationID = conversationID
			f.inbox[i].ScopeHash = scopeHash
			f.inbox[i].Status = mysqlstore.ChannelInboxStatusQueued
		}
	}
	return nil
}
func (f *fakeRepo) MarkInboxProcessing(_ context.Context, _, eventID uint64, _ string, _ time.Time) error {
	for i := range f.inbox {
		if f.inbox[i].ID == eventID {
			f.inbox[i].Status = mysqlstore.ChannelInboxStatusProcessing
		}
	}
	return nil
}
func (f *fakeRepo) MarkInboxProcessed(_ context.Context, _, eventID uint64) error {
	for i := range f.inbox {
		if f.inbox[i].ID == eventID {
			f.inbox[i].Status = mysqlstore.ChannelInboxStatusProcessed
		}
	}
	return nil
}
func (f *fakeRepo) MarkInboxRetry(context.Context, uint64, uint64, time.Time, string, string) error {
	return nil
}
func (f *fakeRepo) MarkInboxFailed(context.Context, uint64, uint64, string, string) error { return nil }
func (f *fakeRepo) MarkInboxIgnored(context.Context, uint64, uint64, string, string) error {
	return nil
}
func (f *fakeRepo) GetOrCreateChannelIdentity(_ context.Context, input mysqlstore.ChannelIdentityInput) (mysqlstore.ChannelIdentity, error) {
	return mysqlstore.ChannelIdentity{TenantID: input.TenantID, AccountID: input.AccountID, ExternalUserID: input.ExternalUserID, UserID: input.UserID}, nil
}
func (f *fakeRepo) GetOrCreateChannelConversation(_ context.Context, input mysqlstore.ChannelConversationInput) (mysqlstore.ChannelConversation, error) {
	if f.conv.ID == 0 {
		f.conv = mysqlstore.ChannelConversation{ID: 7, TenantID: input.TenantID, AccountID: input.AccountID, ExternalChatID: input.ExternalChatID, ExternalThreadID: input.ExternalThreadID, ScopeKey: input.ScopeKey, ScopeHash: input.ScopeHash, SessionID: input.SessionID, RuntimeFingerprint: input.RuntimeFingerprint, RuntimeFingerprintVersion: input.RuntimeFingerprintVersion, WorkspaceRealpath: input.WorkspaceRealpath, PermissionMode: input.PermissionMode, ChatType: input.ChatType}
	}
	return f.conv, nil
}
func (f *fakeRepo) GetChannelConversationByScope(_ context.Context, tenantID, accountID uint64, _ []byte) (mysqlstore.ChannelConversation, error) {
	if f.conversationErr != nil {
		return mysqlstore.ChannelConversation{}, f.conversationErr
	}
	if f.conv.ID == 0 || f.conv.TenantID != tenantID || f.conv.AccountID != accountID {
		return mysqlstore.ChannelConversation{}, mysqlstore.ErrNotFound
	}
	return f.conv, nil
}
func (f *fakeRepo) UpdateChannelConversationControls(_ context.Context, input mysqlstore.ChannelConversationControlsInput) error {
	if input.WorkspaceRealpath != "" {
		f.conv.WorkspaceRealpath = input.WorkspaceRealpath
	}
	if input.PermissionMode != "" {
		f.conv.PermissionMode = input.PermissionMode
	}
	return nil
}
func (f *fakeRepo) RotateChannelConversationSession(_ context.Context, input mysqlstore.ChannelSessionRotationInput) (uint64, error) {
	f.rotations++
	f.conv.SessionID = 1000 + input.InboxEventID
	return f.conv.SessionID, nil
}
func (f *fakeRepo) RequestCancelActiveChannelRun(context.Context, uint64, uint64, uint64) (bool, error) {
	f.cancelled = true
	return true, nil
}
func (f *fakeRepo) GetImageGeneration(_ context.Context, tenantID, userID, sessionID uint64, generationID string) (mysqlstore.ImageGenerationRecord, error) {
	f.imageGetScope = imagegen.JobScope{TenantID: tenantID, UserID: userID, SessionID: sessionID}
	if f.imageGetErr != nil {
		return mysqlstore.ImageGenerationRecord{}, f.imageGetErr
	}
	if f.imageRecord.GenerationID != generationID || f.imageRecord.TenantID != tenantID || f.imageRecord.UserID != userID || f.imageRecord.SessionID != sessionID {
		return mysqlstore.ImageGenerationRecord{}, mysqlstore.ErrNotFound
	}
	return f.imageRecord, nil
}
func (f *fakeRepo) RetryImageGeneration(_ context.Context, input imagegen.ManualRetryRequest) (imagegen.GenerationRecord, bool, error) {
	f.imageRetryInput = input
	return f.imageRetry, f.imageRetryErr == nil, f.imageRetryErr
}
func (f *fakeRepo) InsertAuditLog(_ context.Context, input mysqlstore.AuditLogInput) (uint64, error) {
	f.audits = append(f.audits, input)
	return uint64(len(f.audits)), nil
}
func (f *fakeRepo) CreateChannelRun(_ context.Context, input mysqlstore.ChannelRunInput) (mysqlstore.ChannelRun, error) {
	f.run = mysqlstore.ChannelRun{ID: input.ID, TenantID: input.TenantID, AccountID: input.AccountID, ConversationID: input.ConversationID, SessionID: input.SessionID, ScopeHash: input.ScopeHash, RuntimeFingerprint: input.RuntimeFingerprint, RuntimeFingerprintVersion: input.RuntimeFingerprintVersion, Status: input.Status}
	return f.run, nil
}
func (f *fakeRepo) AttachRunInputs(context.Context, uint64, string, []mysqlstore.ChannelRunInputEvent) error {
	return nil
}
func (f *fakeRepo) ClaimConversationRun(context.Context, uint64, uint64, uint64, string) (mysqlstore.ChannelRun, error) {
	if f.busy {
		return mysqlstore.ChannelRun{}, mysqlstore.ErrChannelRunBusy
	}
	return f.run, nil
}
func (f *fakeRepo) HeartbeatChannelRun(context.Context, uint64, uint64, string, string, uint64) error {
	return nil
}
func (f *fakeRepo) FinishChannelRun(_ context.Context, _, _ uint64, runID, status, _, _ string) error {
	if f.finished == nil {
		f.finished = map[string]string{}
	}
	f.finished[runID] = status
	return nil
}
func (f *fakeRepo) CreateChannelMessage(_ context.Context, input mysqlstore.ChannelMessageInput) (mysqlstore.ChannelMessage, error) {
	message := mysqlstore.ChannelMessage{ID: uint64(len(f.messages) + 11), TenantID: input.TenantID, AccountID: input.AccountID, ConversationID: input.ConversationID, RunID: input.RunID, ExternalMessageID: input.ExternalMessageID, IdempotencyKey: input.IdempotencyKey, ContentJSON: input.ContentJSON, Status: input.Status}
	f.messages = append(f.messages, message)
	return message, nil
}
func (f *fakeRepo) CreateOutboxMessage(_ context.Context, input mysqlstore.ChannelOutboxInput) (mysqlstore.ChannelOutbox, error) {
	f.outboxMu.Lock()
	defer f.outboxMu.Unlock()
	out := mysqlstore.ChannelOutbox{ID: uint64(len(f.outbox) + 1), TenantID: input.TenantID, AccountID: input.AccountID, ConversationID: input.ConversationID, MessageID: input.MessageID, Operation: input.Operation, SequenceNo: input.SequenceNo, ChunkCount: input.ChunkCount, IdempotencyKey: input.IdempotencyKey, PayloadJSON: input.PayloadJSON, Status: input.Status}
	f.outbox = append(f.outbox, out)
	if f.outboxReady != nil {
		select {
		case f.outboxReady <- struct{}{}:
		default:
		}
	}
	return out, nil
}
func (f *fakeRepo) ClaimDueOutbox(context.Context, uint64, uint64, string, int, time.Time) ([]mysqlstore.ChannelOutbox, error) {
	f.outboxMu.Lock()
	defer f.outboxMu.Unlock()
	return append([]mysqlstore.ChannelOutbox(nil), f.outbox...), nil
}
func (f *fakeRepo) HasUnsentPriorChannelOutbox(_ context.Context, tenantID, accountID, conversationID uint64, runID string, sequenceNo uint) (bool, error) {
	f.outboxMu.Lock()
	defer f.outboxMu.Unlock()
	for _, row := range f.outbox {
		if row.TenantID == tenantID && row.AccountID == accountID && row.ConversationID == conversationID && row.RunID == runID && row.SequenceNo < sequenceNo && row.Status != mysqlstore.ChannelOutboxStatusSent && row.Status != mysqlstore.ChannelOutboxStatusDead {
			return true, nil
		}
	}
	return false, nil
}
func (f *fakeRepo) MarkOutboxSent(context.Context, uint64, uint64, uint64) error { return nil }
func (f *fakeRepo) MarkOutboxRetry(context.Context, uint64, uint64, uint64, time.Time, string, string) error {
	return nil
}
func (f *fakeRepo) MarkOutboxDeliveryUnknown(context.Context, uint64, uint64, uint64, string, string) error {
	return nil
}
func (f *fakeRepo) MarkChannelMessageSent(_ context.Context, _, _, _ uint64, externalMessageID string) error {
	f.sentExternal = externalMessageID
	return nil
}
func (f *fakeRepo) MarkOutboxDead(context.Context, uint64, uint64, uint64, string, string) error {
	return nil
}
func (f *fakeRepo) RequeueStrandedChannelRuns(context.Context, uint64, uint64) error { return nil }
func (f *fakeRepo) ClaimDueInbox(context.Context, uint64, uint64, string, int, time.Time) ([]mysqlstore.ChannelInboxEvent, error) {
	return nil, nil
}
func (f *fakeRepo) GetQueuedChannelRun(context.Context, uint64, uint64, uint64) (mysqlstore.ChannelRun, error) {
	if f.run.ID == "" {
		return mysqlstore.ChannelRun{}, mysqlstore.ErrNotFound
	}
	return f.run, nil
}

func TestTeamFailureUsesCommonErrorReaction(t *testing.T) {
	router := agentteam.NewRouter([]agentteam.Binding{{TenantID: 9, TeamID: 21, TeamVersion: 3, Provider: "feishu", AccountKey: "bot-a", ExternalChatID: "oc-team", Trigger: agentteam.TriggerMention}}, nil)
	repo := &fakeRepo{}
	reactionRepo := &teamReactionRepoFake{}
	service := New(Config{TenantID: 9, AccountID: 4, AccountKey: "bot-a", Repo: repo, Adapter: &fakeAdapter{}, PayloadCodec: JSONCodec{}, TeamRouter: &router, TeamDispatch: func(context.Context, TeamDispatchInput) (TeamDispatchResult, error) {
		return TeamDispatchResult{Status: agentteam.StatusFailed, Error: "team failed"}, nil
	}, Reactions: NewReactionReconciler(ReactionConfig{TenantID: 9, AccountID: 4, WorkerID: "test-worker", Repo: reactionRepo, Adapter: &fakeAdapter{}}), UserResolver: func(context.Context, channelcontract.InboundMessage) (uint64, error) {
		return 42, nil
	}, SessionResolver: func(context.Context, uint64, uint64, channelcontract.Scope) (uint64, error) {
		return 99, nil
	}})
	msg := channelcontract.InboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: "bot-a", EventID: "evt-team-fail", ExternalMessageID: "om-team-fail", ExternalConversationID: "oc-team", ExternalUserID: "ou-user", ChatType: channelcontract.ChatTypeGroup, MentionedBot: true, Text: "开始协作"}
	if disposition := service.HandleInbound(context.Background(), msg); !disposition.Ack {
		t.Fatalf("disposition=%+v", disposition)
	}
	if err := service.ProcessOne(context.Background()); err != nil {
		t.Fatalf("team process: %v", err)
	}
	if err := service.ProcessReactionsOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := service.ProcessReactionsOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if repo.finished[repo.run.ID] != mysqlstore.ChannelRunStatusFailed || reactionRepo.row.CurrentEmoji != string(channelcontract.ReactionError) {
		t.Fatalf("finished=%+v reaction=%+v", repo.finished, reactionRepo.row)
	}
}

func TestTeamDispatchReceivesCommonStreamingSink(t *testing.T) {
	router := agentteam.NewRouter([]agentteam.Binding{{TenantID: 9, TeamID: 21, TeamVersion: 3, Provider: "feishu", AccountKey: "bot-a", ExternalChatID: "oc-team", Trigger: agentteam.TriggerMention}}, nil)
	repo := &fakeRepo{}
	adapter := &fakeAdapter{}
	var received bool
	service := New(Config{TenantID: 9, AccountID: 4, AccountKey: "bot-a", Repo: repo, Adapter: adapter, PayloadCodec: JSONCodec{}, TeamRouter: &router, Streaming: channelcontract.StreamingDecision{Enabled: true}, TeamDispatch: func(ctx context.Context, input TeamDispatchInput) (TeamDispatchResult, error) {
		if input.Stream == nil {
			return TeamDispatchResult{}, errors.New("team stream sink missing")
		}
		received = true
		if err := input.Stream.OnDelta(ctx, channelcontract.Delta{Kind: channelcontract.DeltaText, Text: "partial"}); err != nil {
			return TeamDispatchResult{}, err
		}
		return TeamDispatchResult{FinalText: "final", Status: agentteam.StatusCompleted}, nil
	}, UserResolver: func(context.Context, channelcontract.InboundMessage) (uint64, error) {
		return 42, nil
	}, SessionResolver: func(context.Context, uint64, uint64, channelcontract.Scope) (uint64, error) {
		return 99, nil
	}})
	msg := channelcontract.InboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: "bot-a", EventID: "evt-team-stream", ExternalMessageID: "om-team-stream", ExternalConversationID: "oc-team", ExternalUserID: "ou-user", ChatType: channelcontract.ChatTypeGroup, MentionedBot: true, Text: "开始协作"}
	if disposition := service.HandleInbound(context.Background(), msg); !disposition.Ack {
		t.Fatalf("disposition=%+v", disposition)
	}
	if err := service.ProcessOne(context.Background()); err != nil {
		t.Fatalf("team process: %v", err)
	}
	if !received || adapter.streams == nil || len(adapter.streams.updates) == 0 || len(repo.outbox) != 0 {
		t.Fatalf("received=%v streams=%+v outbox=%+v", received, adapter.streams, repo.outbox)
	}
}

type teamReactionRepoFake struct {
	row mysqlstore.ChannelReaction
}

func (f *teamReactionRepoFake) UpsertChannelReactionDesired(_ context.Context, input mysqlstore.ChannelReactionInput) error {
	if f.row.ID == 0 {
		f.row.ID = 1
	}
	f.row.TenantID = input.TenantID
	f.row.AccountID = input.AccountID
	f.row.ConversationID = input.ConversationID
	f.row.ProviderMessageID = input.ProviderMessageID
	f.row.DesiredEmoji = input.DesiredEmoji
	f.row.Status = mysqlstore.ChannelReactionStatusPending
	return nil
}

func (f *teamReactionRepoFake) ClaimDueChannelReactions(_ context.Context, _, _ uint64, workerID string, _ int, leaseUntil time.Time) ([]mysqlstore.ChannelReaction, error) {
	if f.row.Status != mysqlstore.ChannelReactionStatusPending && f.row.Status != mysqlstore.ChannelReactionStatusRetry {
		return nil, nil
	}
	f.row.Status = mysqlstore.ChannelReactionStatusReconciling
	f.row.LeaseOwner = workerID
	f.row.LeaseUntil = leaseUntil
	return []mysqlstore.ChannelReaction{f.row}, nil
}

func (f *teamReactionRepoFake) MarkChannelReactionApplied(_ context.Context, _, _ uint64, _ uint64, _, emoji, reactionID string) error {
	f.row.CurrentEmoji = emoji
	f.row.ReactionID = reactionID
	f.row.Status = mysqlstore.ChannelReactionStatusApplied
	f.row.LeaseOwner = ""
	return nil
}

func (f *teamReactionRepoFake) MarkChannelReactionDeleted(_ context.Context, _, _ uint64, _ uint64, _ string) error {
	f.row.CurrentEmoji = ""
	f.row.ReactionID = ""
	f.row.Status = mysqlstore.ChannelReactionStatusPending
	return nil
}

func (f *teamReactionRepoFake) MarkChannelReactionRetry(_ context.Context, _, _ uint64, _ uint64, _ string, _ time.Time, _, _ string) error {
	f.row.Status = mysqlstore.ChannelReactionStatusRetry
	return nil
}

type fakeAdapter struct {
	delivered       []channelcontract.OutboundOperation
	deliveredSignal chan struct{}
	fail            bool
	streams         *fakeCardStream
	streamList      []*fakeCardStream
	openAttempts    []channelcontract.OutboundMessage
	failOpenAt      map[int]bool
}

func (f *fakeAdapter) CreateReaction(_ context.Context, _ string, _ channelcontract.ReactionEmoji) (string, error) {
	return "reaction-1", nil
}

func (f *fakeAdapter) DeleteReaction(context.Context, string, string) error { return nil }

type fakeCardStream struct {
	opened     []channelcontract.OutboundMessage
	updates    []json.RawMessage
	closed     bool
	failUpdate bool
}

func (f *fakeAdapter) OpenCardStream(_ context.Context, msg channelcontract.OutboundMessage) (channelcontract.CardStreamReceipt, channelcontract.CardStream, error) {
	attempt := len(f.openAttempts)
	f.openAttempts = append(f.openAttempts, msg)
	if f.failOpenAt[attempt] {
		return channelcontract.CardStreamReceipt{}, nil, errors.New("stream open failed")
	}
	stream := &fakeCardStream{}
	if len(f.streamList) == 0 && f.streams != nil {
		stream = f.streams
	}
	stream.opened = append(stream.opened, msg)
	f.streamList = append(f.streamList, stream)
	if f.streams == nil {
		f.streams = stream
	}
	return channelcontract.CardStreamReceipt{MessageID: fmt.Sprintf("om_stream_%d", len(f.streamList))}, stream, nil
}
func (f *fakeCardStream) Update(_ context.Context, card json.RawMessage) error {
	f.updates = append(f.updates, append(json.RawMessage(nil), card...))
	if f.failUpdate {
		return errors.New("stream update failed")
	}
	return nil
}
func (f *fakeCardStream) Close(context.Context) error { f.closed = true; return nil }

func (f *fakeAdapter) Provider() channelcontract.Provider                          { return channelcontract.ProviderFeishu }
func (f *fakeAdapter) AccountID() string                                           { return "acct" }
func (f *fakeAdapter) Start(context.Context, channelcontract.InboundHandler) error { return nil }
func (f *fakeAdapter) Stop(context.Context) error                                  { return nil }
func (f *fakeAdapter) Deliver(_ context.Context, op channelcontract.OutboundOperation) (channelcontract.DeliveryReceipt, error) {
	f.delivered = append(f.delivered, op)
	if f.deliveredSignal != nil {
		select {
		case f.deliveredSignal <- struct{}{}:
		default:
		}
	}
	if f.fail {
		return channelcontract.DeliveryReceipt{}, errors.New("temporary")
	}
	return channelcontract.DeliveryReceipt{MessageID: "om_1"}, nil
}
func (f *fakeAdapter) ResolveThread(context.Context, channelcontract.InboundMessage) (channelcontract.ThreadResolution, error) {
	return channelcontract.ThreadResolution{}, nil
}
func (f *fakeAdapter) Capabilities() channelcontract.Capabilities {
	return channelcontract.Capabilities{FinalCard: true}
}
func (f *fakeAdapter) Health(context.Context) channelcontract.Health {
	return channelcontract.Health{Status: channelcontract.HealthReady}
}

type fakeRunner struct{}

func (fakeRunner) Run(context.Context, channelcontract.RunInput) (channelcontract.RunResult, error) {
	return channelcontract.RunResult{FinalText: "answer"}, nil
}

type imageRunner struct{}

func (imageRunner) Run(context.Context, channelcontract.RunInput) (channelcontract.RunResult, error) {
	return channelcontract.RunResult{FinalText: "image ready", Attachments: []channelcontract.Attachment{{ID: "asset-1", Type: "image", MediaType: "image/png", Name: "asset.png", TenantID: 9, UserID: 42, SessionID: 99}}}, nil
}

func (imageRunner) RunStream(ctx context.Context, input channelcontract.RunInput, _ channelcontract.DeltaSink) (channelcontract.RunResult, error) {
	return imageRunner{}.Run(ctx, input)
}

type partialImageErrorRunner struct{}

func (partialImageErrorRunner) Run(context.Context, channelcontract.RunInput) (channelcontract.RunResult, error) {
	return channelcontract.RunResult{FinalText: "第一张图片已经完成。", Attachments: []channelcontract.Attachment{{ID: "asset-partial", Type: "image", TenantID: 9, UserID: 42, SessionID: 99}}}, errors.New("provider secret token")
}

func (partialImageErrorRunner) RunStream(ctx context.Context, input channelcontract.RunInput, _ channelcontract.DeltaSink) (channelcontract.RunResult, error) {
	return partialImageErrorRunner{}.Run(ctx, input)
}

type commandImageGenerator struct{}

func (commandImageGenerator) Generate(context.Context, imagegen.GenerateRequest) (imagegen.Artifact, error) {
	return imagegen.Artifact{AssetID: "asset-command", TenantID: 9, UserID: 42, SessionID: 99, Operation: imagegen.OperationGenerate, MediaType: "image/png", Name: "command.png"}, nil
}

func (commandImageGenerator) Edit(context.Context, imagegen.EditRequest) (imagegen.Artifact, error) {
	return imagegen.Artifact{AssetID: "asset-command-edit", TenantID: 9, UserID: 42, SessionID: 99, Operation: imagegen.OperationEdit, MediaType: "image/png", Name: "command-edit.png"}, nil
}

type commandImageScheduler struct {
	receipt       imagegen.JobReceipt
	generateInput imagegen.EnqueueGenerateRequest
	editInput     imagegen.EnqueueEditRequest
	cancelScope   imagegen.JobScope
	cancelID      string
	err           error
}

func (s *commandImageScheduler) EnqueueGenerate(_ context.Context, input imagegen.EnqueueGenerateRequest) (imagegen.JobReceipt, error) {
	s.generateInput = input
	return s.receipt, s.err
}

func (s *commandImageScheduler) EnqueueEdit(_ context.Context, input imagegen.EnqueueEditRequest) (imagegen.JobReceipt, error) {
	s.editInput = input
	return s.receipt, s.err
}

func (s *commandImageScheduler) RequestCancel(_ context.Context, scope imagegen.JobScope, generationID string) error {
	s.cancelScope = scope
	s.cancelID = generationID
	return s.err
}

type countingRunner struct {
	calls int
	input channelcontract.RunInput
}

func (r *countingRunner) Run(_ context.Context, input channelcontract.RunInput) (channelcontract.RunResult, error) {
	r.calls++
	r.input = input
	return channelcontract.RunResult{FinalText: "model answer"}, nil
}

type cancelableRunner struct {
	started   chan struct{}
	cancelled chan struct{}
}

type failingRunner struct{}

func (failingRunner) Run(context.Context, channelcontract.RunInput) (channelcontract.RunResult, error) {
	return channelcontract.RunResult{}, errors.New("provider unavailable")
}
func (failingRunner) RunStream(ctx context.Context, input channelcontract.RunInput, _ channelcontract.DeltaSink) (channelcontract.RunResult, error) {
	return failingRunner{}.Run(ctx, input)
}

func (r *cancelableRunner) Run(ctx context.Context, _ channelcontract.RunInput) (channelcontract.RunResult, error) {
	close(r.started)
	<-ctx.Done()
	close(r.cancelled)
	return channelcontract.RunResult{}, ctx.Err()
}
func (r *cancelableRunner) RunStream(ctx context.Context, input channelcontract.RunInput, _ channelcontract.DeltaSink) (channelcontract.RunResult, error) {
	return r.Run(ctx, input)
}
func (r *countingRunner) RunStream(ctx context.Context, input channelcontract.RunInput, _ channelcontract.DeltaSink) (channelcontract.RunResult, error) {
	return r.Run(ctx, input)
}

type streamingRunner struct{}

type sevenToolRunner struct{}

func sevenToolResult() channelcontract.RunResult {
	result := channelcontract.RunResult{FinalText: "done"}
	for index := 0; index < 7; index++ {
		result.Tools = append(result.Tools, channelcontract.ToolProgress{ID: fmt.Sprintf("tool-%d", index+1), Name: "Read", Status: channelcontract.ToolStatusComplete, Command: fmt.Sprintf("file-%d.go", index+1)})
	}
	return result
}

func (sevenToolRunner) Run(context.Context, channelcontract.RunInput) (channelcontract.RunResult, error) {
	return sevenToolResult(), nil
}

func (sevenToolRunner) RunStream(ctx context.Context, _ channelcontract.RunInput, sink channelcontract.DeltaSink) (channelcontract.RunResult, error) {
	for _, tool := range sevenToolResult().Tools {
		_ = sink.OnDelta(ctx, channelcontract.Delta{Kind: channelcontract.DeltaTool, ToolID: tool.ID, ToolName: tool.Name, ToolStatus: channelcontract.ToolStatusRunning, ToolCommand: tool.Command})
		_ = sink.OnDelta(ctx, channelcontract.Delta{Kind: channelcontract.DeltaTool, ToolID: tool.ID, ToolName: tool.Name, ToolStatus: channelcontract.ToolStatusComplete, ToolCommand: tool.Command})
	}
	_ = sink.OnDelta(ctx, channelcontract.Delta{Kind: channelcontract.DeltaText, Text: "done"})
	return sevenToolResult(), nil
}

func (streamingRunner) Run(context.Context, channelcontract.RunInput) (channelcontract.RunResult, error) {
	return channelcontract.RunResult{FinalText: "final"}, nil
}
func (streamingRunner) RunStream(ctx context.Context, _ channelcontract.RunInput, sink channelcontract.DeltaSink) (channelcontract.RunResult, error) {
	_ = sink.OnDelta(ctx, channelcontract.Delta{Kind: channelcontract.DeltaText, Text: "hello"})
	_ = sink.OnDelta(ctx, channelcontract.Delta{Kind: channelcontract.DeltaText, Text: " world"})
	return channelcontract.RunResult{FinalText: "hello world"}, nil
}

type toolDetailsRunner struct{}

type pendingQuestionRunner struct{}

type sevenToolQuestionRunner struct{}

func (sevenToolQuestionRunner) Run(context.Context, channelcontract.RunInput) (channelcontract.RunResult, error) {
	result := sevenToolResult()
	result.FinalText = ""
	result.PendingInteraction = &channelcontract.InteractionQuestion{Question: "Which path?", Choices: []string{"A", "B"}}
	return result, nil
}

func (sevenToolQuestionRunner) RunStream(ctx context.Context, input channelcontract.RunInput, sink channelcontract.DeltaSink) (channelcontract.RunResult, error) {
	result, err := sevenToolQuestionRunner{}.Run(ctx, input)
	for _, tool := range result.Tools {
		_ = sink.OnDelta(ctx, channelcontract.Delta{Kind: channelcontract.DeltaTool, ToolID: tool.ID, ToolName: tool.Name, ToolStatus: tool.Status, ToolCommand: tool.Command})
	}
	return result, err
}

func (pendingQuestionRunner) Run(context.Context, channelcontract.RunInput) (channelcontract.RunResult, error) {
	return channelcontract.RunResult{PendingInteraction: &channelcontract.InteractionQuestion{Question: "Which path?", Choices: []string{"A", "B"}}}, nil
}

func (pendingQuestionRunner) RunStream(ctx context.Context, input channelcontract.RunInput, _ channelcontract.DeltaSink) (channelcontract.RunResult, error) {
	return pendingQuestionRunner{}.Run(ctx, input)
}

type resumeCaptureRunner struct {
	input channelcontract.RunInput
}

func (r *resumeCaptureRunner) Run(_ context.Context, input channelcontract.RunInput) (channelcontract.RunResult, error) {
	r.input = input
	if input.Resume == nil {
		return channelcontract.RunResult{}, errors.New("resume input missing")
	}
	return channelcontract.RunResult{FinalText: "resumed answer"}, nil
}

func (r *resumeCaptureRunner) RunStream(ctx context.Context, input channelcontract.RunInput, _ channelcontract.DeltaSink) (channelcontract.RunResult, error) {
	return r.Run(ctx, input)
}

type pendingServiceRepo struct {
	fakeRepo
	interactions *interactionRepoFake
}

func (r *pendingServiceRepo) CreateChannelInteraction(ctx context.Context, input mysqlstore.ChannelInteractionInput) (mysqlstore.ChannelInteraction, error) {
	return r.interactions.CreateChannelInteraction(ctx, input)
}
func (r *pendingServiceRepo) GetChannelInteraction(ctx context.Context, tenantID, accountID uint64, id string) (mysqlstore.ChannelInteraction, error) {
	return r.interactions.GetChannelInteraction(ctx, tenantID, accountID, id)
}
func (r *pendingServiceRepo) FindChannelInteractionByNonce(ctx context.Context, tenantID, accountID uint64, nonce []byte) (mysqlstore.ChannelInteraction, error) {
	return r.interactions.FindChannelInteractionByNonce(ctx, tenantID, accountID, nonce)
}
func (r *pendingServiceRepo) FindPendingChannelInteraction(ctx context.Context, tenantID, accountID, conversationID, userID uint64, chatID string, now time.Time) (mysqlstore.ChannelInteraction, error) {
	return r.interactions.FindPendingChannelInteraction(ctx, tenantID, accountID, conversationID, userID, chatID, now)
}
func (r *pendingServiceRepo) AnswerChannelInteraction(ctx context.Context, tenantID, accountID uint64, id string, nonce []byte, userID uint64, fingerprint string, version int, answer []byte, now time.Time) error {
	return r.interactions.AnswerChannelInteraction(ctx, tenantID, accountID, id, nonce, userID, fingerprint, version, answer, now)
}
func (r *pendingServiceRepo) MarkChannelRunWaitingInput(_ context.Context, _, _ uint64, runID string) error {
	if r.finished == nil {
		r.finished = map[string]string{}
	}
	r.finished[runID] = mysqlstore.ChannelRunStatusWaitingInput
	return nil
}
func (r *pendingServiceRepo) QueueWaitingChannelRun(_ context.Context, _, _ uint64, runID string) error {
	if r.finished == nil {
		r.finished = map[string]string{}
	}
	r.finished[runID] = mysqlstore.ChannelRunStatusQueued
	return nil
}

func (toolDetailsRunner) Run(context.Context, channelcontract.RunInput) (channelcontract.RunResult, error) {
	return channelcontract.RunResult{FinalText: "failed answer", Tools: []channelcontract.ToolProgress{{ID: "tool-1", Name: "Bash", Status: channelcontract.ToolStatusFailed, Command: "docker compose up", OutputPreview: "no configuration file provided", IsError: true}}}, errors.New("command failed")
}

func (toolDetailsRunner) RunStream(context.Context, channelcontract.RunInput, channelcontract.DeltaSink) (channelcontract.RunResult, error) {
	return toolDetailsRunner{}.Run(context.Background(), channelcontract.RunInput{})
}
func (fakeRunner) RunStream(context.Context, channelcontract.RunInput, channelcontract.DeltaSink) (channelcontract.RunResult, error) {
	return fakeRunner{}.Run(context.Background(), channelcontract.RunInput{})
}

func TestServiceClaimsBeforeAckAndDeduplicates(t *testing.T) {
	repo := &fakeRepo{}
	svc := New(Config{TenantID: 9, AccountID: 3, AccountKey: "acct", Repo: repo, Adapter: &fakeAdapter{}, Runner: fakeRunner{}, UserResolver: func(context.Context, channelcontract.InboundMessage) (uint64, error) { return 42, nil }, SessionResolver: func(context.Context, uint64, uint64, channelcontract.Scope) (uint64, error) { return 99, nil }, PayloadCodec: JSONCodec{}})
	msg := channelcontract.InboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: "acct", EventID: "evt-1", ExternalMessageID: "om-1", ExternalConversationID: "oc-1", ExternalUserID: "ou-1", ChatType: channelcontract.ChatTypeP2P, Text: "hello"}
	if got := svc.HandleInbound(context.Background(), msg); !got.Ack || got.Retryable {
		t.Fatalf("first disposition = %+v", got)
	}
	if len(repo.inbox) != 1 || repo.inbox[0].Status != mysqlstore.ChannelInboxStatusQueued {
		t.Fatalf("inbox = %+v", repo.inbox)
	}
	if got := svc.HandleInbound(context.Background(), msg); !got.Ack || got.Retryable {
		t.Fatalf("duplicate disposition = %+v", got)
	}
	if len(repo.inbox) != 1 {
		t.Fatalf("duplicate created inbox: %+v", repo.inbox)
	}
}

func TestServicePersistsPendingQuestionAndDoesNotCompleteRun(t *testing.T) {
	interactionRepo := &interactionRepoFake{}
	repo := &pendingServiceRepo{fakeRepo: fakeRepo{}, interactions: interactionRepo}
	broker := NewQuestionBroker(QuestionBrokerConfig{TenantID: 9, AccountID: 3, RuntimeFingerprint: DefaultFingerprint, RuntimeFingerprintVersion: DefaultRuntimeVersion, Repo: interactionRepo, Now: time.Now})
	svc := New(Config{TenantID: 9, AccountID: 3, AccountKey: "acct", Repo: repo, Adapter: &fakeAdapter{}, Runner: pendingQuestionRunner{}, QuestionBroker: broker, RenderFinalCard: func(state channelcontract.CardState) ([]byte, error) {
		return json.Marshal(map[string]any{"status": state.Status, "question": state.Question})
	}, UserResolver: func(context.Context, channelcontract.InboundMessage) (uint64, error) { return 42, nil }, SessionResolver: func(context.Context, uint64, uint64, channelcontract.Scope) (uint64, error) { return 99, nil }, PayloadCodec: JSONCodec{}})
	msg := channelcontract.InboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: "acct", EventID: "evt-question", ExternalMessageID: "om-question", ExternalConversationID: "oc-1", ExternalUserID: "ou-1", ChatType: channelcontract.ChatTypeP2P, Text: "ask"}
	if d := svc.HandleInbound(context.Background(), msg); !d.Ack {
		t.Fatal(d)
	}
	if err := svc.ProcessOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if repo.finished[repo.run.ID] != mysqlstore.ChannelRunStatusWaitingInput || len(repo.outbox) != 1 || !strings.Contains(repo.outbox[0].PayloadJSON, "waiting_input") {
		t.Fatalf("finished=%+v outbox=%+v interaction=%+v", repo.finished, repo.outbox, interactionRepo.row)
	}
}

func TestTimelineWaitingInputPaginatesToolsAndKeepsQuestionOnLatestPage(t *testing.T) {
	interactionRepo := &interactionRepoFake{}
	repo := &pendingServiceRepo{fakeRepo: fakeRepo{}, interactions: interactionRepo}
	broker := NewQuestionBroker(QuestionBrokerConfig{TenantID: 9, AccountID: 3, RuntimeFingerprint: DefaultFingerprint, RuntimeFingerprintVersion: DefaultRuntimeVersion, Repo: interactionRepo, Now: time.Now})
	render := func(state channelcontract.CardState) ([]channelcontract.RenderedCardPage, error) {
		return feishuchannel.RenderTimelineCards(state, feishuchannel.CardOptions{})
	}
	svc := New(Config{TenantID: 9, AccountID: 3, AccountKey: "acct", Repo: repo, Adapter: &fakeAdapter{}, Runner: sevenToolQuestionRunner{}, QuestionBroker: broker, RenderTimelineCards: render, PayloadCodec: JSONCodec{}, UserResolver: func(context.Context, channelcontract.InboundMessage) (uint64, error) { return 42, nil }, SessionResolver: func(context.Context, uint64, uint64, channelcontract.Scope) (uint64, error) { return 99, nil }})
	msg := channelcontract.InboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: "acct", EventID: "evt-question-pages", ExternalMessageID: "om-question-pages", ExternalConversationID: "oc-1", ExternalUserID: "ou-1", ChatType: channelcontract.ChatTypeP2P, Text: "ask"}
	if disposition := svc.HandleInbound(context.Background(), msg); !disposition.Ack {
		t.Fatal(disposition)
	}
	if err := svc.ProcessOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repo.messages) != 2 || len(repo.outbox) != 2 {
		t.Fatalf("messages=%d outbox=%d, want 2/2", len(repo.messages), len(repo.outbox))
	}
	if strings.Contains(repo.outbox[0].PayloadJSON, "Which path?") || !strings.Contains(repo.outbox[1].PayloadJSON, "Which path?") {
		t.Fatalf("question was not isolated to latest page: %+v", repo.outbox)
	}
	for index, row := range repo.outbox {
		want := fmt.Sprintf("interaction:%s:prompt:page:%d", interactionRepo.row.ID, index)
		if row.IdempotencyKey != want {
			t.Fatalf("page %d key = %q, want %q", index, row.IdempotencyKey, want)
		}
	}
}

func TestServiceFailsClosedWhenQuestionInteractionIsDisabled(t *testing.T) {
	repo := &fakeRepo{}
	svc := New(Config{TenantID: 9, AccountID: 3, AccountKey: "acct", Repo: repo, Adapter: &fakeAdapter{}, Runner: pendingQuestionRunner{}, RenderFinalCard: func(state channelcontract.CardState) ([]byte, error) {
		return json.Marshal(map[string]string{"status": string(state.Status), "text": state.Text})
	}, UserResolver: func(context.Context, channelcontract.InboundMessage) (uint64, error) { return 42, nil }, SessionResolver: func(context.Context, uint64, uint64, channelcontract.Scope) (uint64, error) { return 99, nil }, PayloadCodec: JSONCodec{}})
	msg := channelcontract.InboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: "acct", EventID: "evt-disabled-question", ExternalMessageID: "om-disabled-question", ExternalConversationID: "oc-1", ExternalUserID: "ou-1", ChatType: channelcontract.ChatTypeP2P, Text: "ask"}
	if d := svc.HandleInbound(context.Background(), msg); !d.Ack {
		t.Fatal(d)
	}
	if err := svc.ProcessOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if repo.finished[repo.run.ID] != mysqlstore.ChannelRunStatusFailed || len(repo.outbox) != 1 || !strings.Contains(repo.outbox[0].PayloadJSON, "未启用交互提问") {
		t.Fatalf("finished=%+v outbox=%+v", repo.finished, repo.outbox)
	}
}

func TestServiceRoutesQuestionButtonToOriginalRunWithoutInbox(t *testing.T) {
	interactionRepo := &interactionRepoFake{}
	now := time.Now().UTC()
	broker := NewQuestionBroker(QuestionBrokerConfig{TenantID: 9, AccountID: 3, RuntimeFingerprint: DefaultFingerprint, RuntimeFingerprintVersion: DefaultRuntimeVersion, Repo: interactionRepo, Now: func() time.Time { return now }})
	question, err := broker.Create(context.Background(), channelcontract.InteractionCreateInput{TenantID: 9, AccountID: 3, ConversationID: 7, RunID: "run-question", SessionID: 11, ExternalChatID: "oc-1", ExternalThreadID: "", ExternalUserID: "ou-user", UserID: 42, ScopeHash: []byte("scope"), RuntimeFingerprint: DefaultFingerprint, RuntimeFingerprintVersion: DefaultRuntimeVersion, Question: channelcontract.InteractionQuestion{Question: "Which path?", Choices: []string{"A", "B"}, ToolUseID: "toolu-q", AssistantMessage: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "tool_use", ID: "toolu-q", Name: "AskUserQuestion"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	repo := &pendingServiceRepo{fakeRepo: fakeRepo{run: mysqlstore.ChannelRun{ID: "run-question", TenantID: 9, AccountID: 3, ConversationID: 7, SessionID: 11, Status: mysqlstore.ChannelRunStatusWaitingInput, RuntimeFingerprint: DefaultFingerprint, RuntimeFingerprintVersion: DefaultRuntimeVersion}}, interactions: interactionRepo}
	svc := New(Config{TenantID: 9, AccountID: 3, AccountKey: "acct", Repo: repo, Adapter: &fakeAdapter{}, QuestionBroker: broker, UserResolver: func(context.Context, channelcontract.InboundMessage) (uint64, error) { return 42, nil }, PayloadCodec: JSONCodec{}})
	callback := channelcontract.InboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: "acct", EventID: "evt-question-answer", ExternalMessageID: "om-question-answer", ExternalConversationID: "oc-1", ExternalUserID: "ou-user", ChatType: channelcontract.ChatTypeP2P, CardAction: &channelcontract.CardAction{Token: question.Token, Action: channelcontract.InteractionActionAnswer, ChoiceID: "1"}}
	if disposition := svc.HandleInbound(context.Background(), callback); !disposition.Ack || disposition.ErrorCode != "" {
		t.Fatalf("disposition=%+v", disposition)
	}
	if len(repo.inbox) != 0 || repo.finished["run-question"] != mysqlstore.ChannelRunStatusQueued || len(svc.queue) != 1 || svc.queue[0].Resume == nil {
		t.Fatalf("inbox=%+v finished=%+v queue=%+v", repo.inbox, repo.finished, svc.queue)
	}
}

func TestServiceRoutesQuestionTextToOriginalRunAndConsumesInbox(t *testing.T) {
	interactionRepo := &interactionRepoFake{}
	broker := NewQuestionBroker(QuestionBrokerConfig{TenantID: 9, AccountID: 3, RuntimeFingerprint: DefaultFingerprint, RuntimeFingerprintVersion: DefaultRuntimeVersion, Repo: interactionRepo, Now: time.Now})
	if _, err := broker.Create(context.Background(), channelcontract.InteractionCreateInput{TenantID: 9, AccountID: 3, ConversationID: 7, RunID: "run-text", SessionID: 11, ExternalChatID: "oc-1", ExternalUserID: "ou-user", UserID: 42, ScopeHash: []byte("scope"), RuntimeFingerprint: DefaultFingerprint, RuntimeFingerprintVersion: DefaultRuntimeVersion, Question: channelcontract.InteractionQuestion{Question: "Which path?", Choices: []string{"A", "B"}}}); err != nil {
		t.Fatal(err)
	}
	repo := &pendingServiceRepo{fakeRepo: fakeRepo{conv: mysqlstore.ChannelConversation{ID: 7, TenantID: 9, AccountID: 3, ExternalChatID: "oc-1", ScopeHash: []byte("scope"), SessionID: 11}, run: mysqlstore.ChannelRun{ID: "run-text", TenantID: 9, AccountID: 3, ConversationID: 7, SessionID: 11, Status: mysqlstore.ChannelRunStatusWaitingInput, RuntimeFingerprint: DefaultFingerprint, RuntimeFingerprintVersion: DefaultRuntimeVersion}}, interactions: interactionRepo}
	svc := New(Config{TenantID: 9, AccountID: 3, AccountKey: "acct", Repo: repo, Adapter: &fakeAdapter{}, QuestionBroker: broker, UserResolver: func(context.Context, channelcontract.InboundMessage) (uint64, error) { return 42, nil }, SessionResolver: func(context.Context, uint64, uint64, channelcontract.Scope) (uint64, error) { return 11, nil }, PayloadCodec: JSONCodec{}})
	msg := channelcontract.InboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: "acct", EventID: "evt-question-text", ExternalMessageID: "om-question-text", ExternalConversationID: "oc-1", ExternalUserID: "ou-user", ChatType: channelcontract.ChatTypeP2P, Text: "B"}
	if disposition := svc.HandleInbound(context.Background(), msg); !disposition.Ack || disposition.ErrorCode != "" {
		t.Fatalf("disposition=%+v", disposition)
	}
	if len(repo.inbox) != 1 || repo.inbox[0].Status != mysqlstore.ChannelInboxStatusProcessed || repo.finished["run-text"] != mysqlstore.ChannelRunStatusQueued || len(svc.queue) != 1 || svc.queue[0].Resume == nil {
		t.Fatalf("inbox=%+v finished=%+v queue=%+v", repo.inbox, repo.finished, svc.queue)
	}
}

func TestServiceResumePassesRealToolResultToRunner(t *testing.T) {
	interactionRepo := &interactionRepoFake{}
	broker := NewQuestionBroker(QuestionBrokerConfig{TenantID: 9, AccountID: 3, RuntimeFingerprint: DefaultFingerprint, RuntimeFingerprintVersion: DefaultRuntimeVersion, Repo: interactionRepo, Now: time.Now})
	question, err := broker.Create(context.Background(), channelcontract.InteractionCreateInput{TenantID: 9, AccountID: 3, ConversationID: 7, RunID: "run-resume", SessionID: 11, ExternalChatID: "oc-1", ExternalUserID: "ou-user", UserID: 42, ScopeHash: []byte("scope"), RuntimeFingerprint: DefaultFingerprint, RuntimeFingerprintVersion: DefaultRuntimeVersion, Question: channelcontract.InteractionQuestion{Question: "Which path?", Choices: []string{"A", "B"}, ToolUseID: "toolu-q", AssistantMessage: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "tool_use", ID: "toolu-q", Name: "AskUserQuestion"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	runner := &resumeCaptureRunner{}
	repo := &pendingServiceRepo{fakeRepo: fakeRepo{run: mysqlstore.ChannelRun{ID: "run-resume", TenantID: 9, AccountID: 3, ConversationID: 7, SessionID: 11, Status: mysqlstore.ChannelRunStatusWaitingInput, RuntimeFingerprint: DefaultFingerprint, RuntimeFingerprintVersion: DefaultRuntimeVersion}}, interactions: interactionRepo}
	svc := New(Config{TenantID: 9, AccountID: 3, AccountKey: "acct", Repo: repo, Adapter: &fakeAdapter{}, Runner: runner, QuestionBroker: broker, UserResolver: func(context.Context, channelcontract.InboundMessage) (uint64, error) { return 42, nil }, PayloadCodec: JSONCodec{}})
	callback := channelcontract.InboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: "acct", EventID: "evt-resume", ExternalMessageID: "om-resume", ExternalConversationID: "oc-1", ExternalUserID: "ou-user", ChatType: channelcontract.ChatTypeP2P, CardAction: &channelcontract.CardAction{Token: question.Token, Action: channelcontract.InteractionActionAnswer, ChoiceID: "1"}}
	if d := svc.HandleInbound(context.Background(), callback); !d.Ack {
		t.Fatal(d)
	}
	if err := svc.ProcessOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runner.input.Resume == nil || runner.input.Resume.ToolResult.Content != "B" || runner.input.Resume.ToolResult.ToolUseID != "toolu-q" {
		t.Fatalf("resume input=%+v", runner.input.Resume)
	}
}

func TestServiceRejectsUnmentionedGroup(t *testing.T) {
	repo := &fakeRepo{}
	svc := New(Config{TenantID: 9, AccountID: 3, AccountKey: "acct", Repo: repo, Adapter: &fakeAdapter{}, Runner: fakeRunner{}, Policy: Policy{RequireMentionInGroups: true}, PayloadCodec: JSONCodec{}})
	msg := channelcontract.InboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: "acct", EventID: "evt-1", ExternalMessageID: "om-1", ExternalConversationID: "oc-1", ExternalUserID: "ou-1", ChatType: channelcontract.ChatTypeGroup, Text: "hello"}
	got := svc.HandleInbound(context.Background(), msg)
	if !got.Ack || got.ErrorCode != "mention_required" {
		t.Fatalf("disposition = %+v", got)
	}
	if len(repo.inbox) != 0 {
		t.Fatalf("unauthorized event persisted: %+v", repo.inbox)
	}
}

func TestWorkerRunsAndEnqueuesFinalCard(t *testing.T) {
	repo := &fakeRepo{}
	adapter := &fakeAdapter{}
	svc := New(Config{TenantID: 9, AccountID: 3, AccountKey: "acct", Repo: repo, Adapter: adapter, Runner: fakeRunner{}, UserResolver: func(context.Context, channelcontract.InboundMessage) (uint64, error) { return 42, nil }, SessionResolver: func(context.Context, uint64, uint64, channelcontract.Scope) (uint64, error) { return 99, nil }, PayloadCodec: JSONCodec{}})
	msg := channelcontract.InboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: "acct", EventID: "evt-1", ExternalMessageID: "om-1", ExternalConversationID: "oc-1", ExternalUserID: "ou-1", ChatType: channelcontract.ChatTypeP2P, Text: "hello"}
	if d := svc.HandleInbound(context.Background(), msg); !d.Ack {
		t.Fatal(d)
	}
	if err := svc.ProcessOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repo.outbox) != 1 {
		t.Fatalf("outbox = %+v", repo.outbox)
	}
	var outbound channelcontract.OutboundMessage
	if err := json.Unmarshal([]byte(repo.outbox[0].PayloadJSON), &outbound); err != nil {
		t.Fatal(err)
	}
	if outbound.Kind != channelcontract.MessageKindCard || outbound.ExternalChatID != "oc-1" {
		t.Fatalf("outbound = %+v", outbound)
	}
}

func TestWorkerEnqueuesImageAttachmentAfterFinalCard(t *testing.T) {
	repo := &fakeRepo{}
	svc := New(Config{TenantID: 9, AccountID: 3, AccountKey: "acct", Repo: repo, Adapter: &fakeAdapter{}, Runner: imageRunner{}, UserResolver: func(context.Context, channelcontract.InboundMessage) (uint64, error) { return 42, nil }, SessionResolver: func(context.Context, uint64, uint64, channelcontract.Scope) (uint64, error) { return 99, nil }, PayloadCodec: JSONCodec{}})
	msg := channelcontract.InboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: "acct", EventID: "evt-image", ExternalMessageID: "om-image", ExternalConversationID: "oc-1", ExternalUserID: "ou-1", ChatType: channelcontract.ChatTypeP2P, Text: "generate image"}
	if d := svc.HandleInbound(context.Background(), msg); !d.Ack {
		t.Fatal(d)
	}
	if err := svc.ProcessOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repo.outbox) != 2 {
		t.Fatalf("outbox = %+v", repo.outbox)
	}
	var imageMessage channelcontract.OutboundMessage
	if err := json.Unmarshal([]byte(repo.outbox[1].PayloadJSON), &imageMessage); err != nil {
		t.Fatal(err)
	}
	if imageMessage.Kind != channelcontract.MessageKindImage || len(imageMessage.Attachments) != 1 || imageMessage.Attachments[0].ID != "asset-1" || repo.outbox[1].SequenceNo != 2 {
		t.Fatalf("image outbound = %+v outbox=%+v", imageMessage, repo.outbox[1])
	}
}

func TestWorkerPreservesSafePartialTextAndAttachmentWhenRunnerErrors(t *testing.T) {
	repo := &fakeRepo{}
	svc := New(Config{TenantID: 9, AccountID: 3, AccountKey: "acct", Repo: repo, Adapter: &fakeAdapter{}, Runner: partialImageErrorRunner{}, UserResolver: func(context.Context, channelcontract.InboundMessage) (uint64, error) { return 42, nil }, SessionResolver: func(context.Context, uint64, uint64, channelcontract.Scope) (uint64, error) { return 99, nil }, PayloadCodec: JSONCodec{}})
	msg := channelcontract.InboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: "acct", EventID: "evt-partial-image", ExternalMessageID: "om-partial-image", ExternalConversationID: "oc-1", ExternalUserID: "ou-1", ChatType: channelcontract.ChatTypeP2P, Text: "generate images"}
	if d := svc.HandleInbound(context.Background(), msg); !d.Ack {
		t.Fatal(d)
	}
	if err := svc.ProcessOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repo.outbox) != 2 || !strings.Contains(repo.outbox[0].PayloadJSON, "第一张图片已经完成") || strings.Contains(repo.outbox[0].PayloadJSON, "provider secret token") {
		t.Fatalf("outbox=%+v", repo.outbox)
	}
	var imageMessage channelcontract.OutboundMessage
	if err := json.Unmarshal([]byte(repo.outbox[1].PayloadJSON), &imageMessage); err != nil {
		t.Fatal(err)
	}
	if len(imageMessage.Attachments) != 1 || imageMessage.Attachments[0].ID != "asset-partial" {
		t.Fatalf("image message=%+v", imageMessage)
	}
}

func TestServiceImageCommandGeneratesAndEnqueuesAsset(t *testing.T) {
	repo := &fakeRepo{}
	svc := New(Config{TenantID: 9, AccountID: 3, AccountKey: "acct", Repo: repo, Adapter: &fakeAdapter{}, Runner: fakeRunner{}, ImageGenerator: commandImageGenerator{}, CommandRouter: channelcontract.NewChannelCommandRouter(nil), UserResolver: func(context.Context, channelcontract.InboundMessage) (uint64, error) { return 42, nil }, SessionResolver: func(context.Context, uint64, uint64, channelcontract.Scope) (uint64, error) { return 99, nil }, PayloadCodec: JSONCodec{}})
	msg := channelcontract.InboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: "acct", EventID: "evt-image-command", ExternalMessageID: "om-image-command", ExternalConversationID: "oc-1", ExternalUserID: "ou-1", ChatType: channelcontract.ChatTypeP2P, Text: "/image a sunset"}
	if d := svc.HandleInbound(context.Background(), msg); !d.Ack {
		t.Fatal(d)
	}
	if err := svc.ProcessOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repo.outbox) != 2 {
		t.Fatalf("outbox = %+v", repo.outbox)
	}
	var outbound channelcontract.OutboundMessage
	if err := json.Unmarshal([]byte(repo.outbox[1].PayloadJSON), &outbound); err != nil {
		t.Fatal(err)
	}
	if outbound.Kind != channelcontract.MessageKindImage || outbound.Attachments[0].ID != "asset-command" {
		t.Fatalf("outbound = %+v", outbound)
	}
}

func TestImageCommandAsyncGenerateEnqueuesReceiptWithoutAttachment(t *testing.T) {
	repo := &fakeRepo{}
	scheduler := &commandImageScheduler{receipt: imagegen.JobReceipt{GenerationID: "gen-queued", BatchID: "run-image", Status: imagegen.JobStatus(imagegen.GenerationStatusQueued), AcceptedAt: time.Date(2026, time.September, 3, 1, 2, 3, 0, time.UTC)}}
	service := New(Config{TenantID: 9, AccountID: 3, Repo: repo, ImageGenerator: commandImageGenerator{}, ImageScheduler: scheduler, AsyncChannelImages: true})
	item, run := imageCommandWorkItem()
	result, err := service.executeImageCommand(context.Background(), item, run, "a sunset")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Attachments) != 0 || !strings.Contains(result.Text, "已受理 1 个") || !strings.Contains(result.Text, "gen-queued") {
		t.Fatalf("command result=%+v", result)
	}
	if scheduler.generateInput.Scope != (imagegen.JobScope{TenantID: 9, UserID: 42, SessionID: 99}) || scheduler.generateInput.Invocation.RunID != "run-image" || scheduler.generateInput.Invocation.ToolUseID == "" {
		t.Fatalf("generate input=%+v", scheduler.generateInput)
	}
	assertChannelImageOrigin(t, scheduler.generateInput.Origin, run, item)
	if len(repo.audits) != 1 || repo.audits[0].Action != ChannelImageAuditEnqueued || strings.Contains(repo.audits[0].MetadataJSON, "a sunset") {
		t.Fatalf("audits=%+v", repo.audits)
	}
}

func TestImageCommandAsyncEditEnqueuesOwnedSource(t *testing.T) {
	repo := &fakeRepo{}
	scheduler := &commandImageScheduler{receipt: imagegen.JobReceipt{GenerationID: "gen-edit", Status: imagegen.JobStatus(imagegen.GenerationStatusQueued), AcceptedAt: time.Now()}}
	service := New(Config{TenantID: 9, AccountID: 3, Repo: repo, ImageScheduler: scheduler, AsyncChannelImages: true})
	item, run := imageCommandWorkItem()
	result, err := service.executeImageCommand(context.Background(), item, run, "edit asset-1 redraw privately")
	if err != nil || !strings.Contains(result.Text, "gen-edit") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if scheduler.editInput.SourceAssetID != "asset-1" || scheduler.editInput.Prompt != "redraw privately" {
		t.Fatalf("edit input=%+v", scheduler.editInput)
	}
}

func TestImageStatusUsesExactScopeAndHidesPromptAndErrors(t *testing.T) {
	repo := &fakeRepo{imageRecord: mysqlstore.ImageGenerationRecord{
		GenerationID: "gen-1", TenantID: 9, UserID: 42, SessionID: 99, Operation: imagegen.OperationGenerate,
		Status: imagegen.GenerationStatusFailed, Prompt: "private prompt", ErrorMessage: "secret token", ErrorCode: "provider raw error",
	}}
	service := New(Config{TenantID: 9, AccountID: 3, Repo: repo, ImageScheduler: &commandImageScheduler{}, AsyncChannelImages: true})
	item, run := imageCommandWorkItem()
	result, err := service.executeImageCommand(context.Background(), item, run, "status gen-1")
	if err != nil || repo.imageGetScope != (imagegen.JobScope{TenantID: 9, UserID: 42, SessionID: 99}) {
		t.Fatalf("result=%+v scope=%+v err=%v", result, repo.imageGetScope, err)
	}
	if !strings.Contains(result.Text, "gen-1") || !strings.Contains(result.Text, imagegen.GenerationStatusFailed) || strings.Contains(result.Text, "private prompt") || strings.Contains(result.Text, "secret token") || strings.Contains(result.Text, "provider raw error") {
		t.Fatalf("unsafe status=%q", result.Text)
	}
}

func TestImageStatusDoesNotRevealCrossScopeGeneration(t *testing.T) {
	repo := &fakeRepo{imageRecord: mysqlstore.ImageGenerationRecord{GenerationID: "gen-other", TenantID: 9, UserID: 77, SessionID: 88, Prompt: "private"}}
	service := New(Config{TenantID: 9, AccountID: 3, Repo: repo, ImageScheduler: &commandImageScheduler{}, AsyncChannelImages: true})
	item, run := imageCommandWorkItem()
	result, err := service.executeImageCommand(context.Background(), item, run, "status gen-other")
	if err != nil || !strings.Contains(result.Text, "未找到") || strings.Contains(result.Text, "private") {
		t.Fatalf("cross-scope result=%+v err=%v", result, err)
	}
}

func TestImageCancelUsesSchedulerExactScopeAndWritesPromptFreeAudit(t *testing.T) {
	repo := &fakeRepo{imageRecord: mysqlstore.ImageGenerationRecord{GenerationID: "gen-1", TenantID: 9, UserID: 42, SessionID: 99, Status: imagegen.GenerationStatusCancelled}}
	scheduler := &commandImageScheduler{}
	service := New(Config{TenantID: 9, AccountID: 3, Repo: repo, ImageScheduler: scheduler, AsyncChannelImages: true})
	item, run := imageCommandWorkItem()
	result, err := service.executeImageCommand(context.Background(), item, run, "cancel gen-1")
	if err != nil || scheduler.cancelScope != (imagegen.JobScope{TenantID: 9, UserID: 42, SessionID: 99}) || scheduler.cancelID != "gen-1" {
		t.Fatalf("result=%+v scope=%+v id=%q err=%v", result, scheduler.cancelScope, scheduler.cancelID, err)
	}
	if len(repo.audits) != 1 || repo.audits[0].Action != ChannelImageAuditCancelled || strings.Contains(repo.audits[0].MetadataJSON, "prompt") {
		t.Fatalf("audits=%+v", repo.audits)
	}
}

func TestImageRetryRejectsInvalidStateWithoutLeakingSource(t *testing.T) {
	repo := &fakeRepo{imageRetryErr: imagegen.ErrImageManualRetryNotAllowed}
	service := New(Config{TenantID: 9, AccountID: 3, Repo: repo, ImageScheduler: &commandImageScheduler{}, AsyncChannelImages: true})
	item, run := imageCommandWorkItem()
	result, err := service.executeImageCommand(context.Background(), item, run, "retry gen-running")
	if err != nil || !strings.Contains(result.Text, "不允许重试") || strings.Contains(result.Text, "secret") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestImageRetryCreatesLinkedGenerationWithRuntimeIdentity(t *testing.T) {
	repo := &fakeRepo{imageRetry: imagegen.GenerationRecord{GenerationID: "gen-retry", RetryOfGenerationID: "gen-failed", Status: imagegen.GenerationStatusQueued, CreatedAt: time.Now()}}
	service := New(Config{TenantID: 9, AccountID: 3, Repo: repo, ImageScheduler: &commandImageScheduler{}, AsyncChannelImages: true})
	item, run := imageCommandWorkItem()
	result, err := service.executeImageCommand(context.Background(), item, run, "retry gen-failed")
	if err != nil || !strings.Contains(result.Text, "gen-retry") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	input := repo.imageRetryInput
	if input.Scope != (imagegen.JobScope{TenantID: 9, UserID: 42, SessionID: 99}) || input.SourceGenerationID != "gen-failed" || input.GenerationID == "" || input.IdempotencyKey == "" || input.Origin.Type != imagegen.OriginTypeChannel {
		t.Fatalf("retry input=%+v", input)
	}
	if len(repo.audits) != 1 || repo.audits[0].Action != ChannelImageAuditRetried || !strings.Contains(repo.audits[0].MetadataJSON, "gen-failed") || !strings.Contains(repo.audits[0].MetadataJSON, "gen-retry") {
		t.Fatalf("audits=%+v", repo.audits)
	}
}

func imageCommandWorkItem() (workItem, mysqlstore.ChannelRun) {
	run := mysqlstore.ChannelRun{ID: "run-image", TenantID: 9, AccountID: 3, ConversationID: 7, SessionID: 99}
	return workItem{
		UserID: 42, Run: run, Scope: channelcontract.Scope{ThreadID: "thread-1"},
		Message: channelcontract.InboundMessage{ExternalMessageID: "om-1", ExternalConversationID: "oc-1", ExternalUserID: "ou-1"},
	}, run
}

func assertChannelImageOrigin(t *testing.T, metadata imagegen.OriginMetadata, run mysqlstore.ChannelRun, item workItem) {
	t.Helper()
	var origin mysqlstore.ChannelImageCompletionOrigin
	if err := json.Unmarshal([]byte(metadata.RefJSON), &origin); err != nil {
		t.Fatal(err)
	}
	if metadata.Type != imagegen.OriginTypeChannel || origin.Version != ChannelImageOriginVersion || origin.TenantID != run.TenantID || origin.AccountID != run.AccountID || origin.ConversationID != run.ConversationID || origin.RunID != run.ID || origin.SessionID != run.SessionID || origin.UserID != item.UserID || origin.ReplyMessageID != item.Message.ExternalMessageID || origin.ThreadID != item.Scope.ThreadID {
		t.Fatalf("origin=%+v", origin)
	}
}

func TestServiceNewCommandRotatesPersistentSessionWithoutCallingModel(t *testing.T) {
	repo := &fakeRepo{}
	runner := &countingRunner{}
	svc := New(Config{TenantID: 9, AccountID: 3, AccountKey: "acct", Repo: repo, Adapter: &fakeAdapter{}, Runner: runner, CommandRouter: channelcontract.NewChannelCommandRouter([]string{"ou-admin"}), UserResolver: func(context.Context, channelcontract.InboundMessage) (uint64, error) { return 42, nil }, SessionResolver: func(context.Context, uint64, uint64, channelcontract.Scope) (uint64, error) { return 99, nil }, PayloadCodec: JSONCodec{}})
	msg := channelcontract.InboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: "acct", EventID: "evt-new", ExternalMessageID: "om-new", ExternalConversationID: "oc-1", ExternalUserID: "ou-admin", ChatType: channelcontract.ChatTypeP2P, Text: "/new"}
	if d := svc.HandleInbound(context.Background(), msg); !d.Ack {
		t.Fatal(d)
	}
	if err := svc.ProcessOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runner.calls != 0 || repo.rotations != 1 || repo.conv.SessionID == 99 {
		t.Fatalf("runner calls=%d rotations=%d conversation=%+v", runner.calls, repo.rotations, repo.conv)
	}
	if len(repo.outbox) != 1 || !strings.Contains(repo.outbox[0].PayloadJSON, "1001") {
		t.Fatalf("outbox = %+v", repo.outbox)
	}
}

func TestServiceStatusReportsPersistentControlsWithoutCallingModel(t *testing.T) {
	repo := &fakeRepo{}
	runner := &countingRunner{}
	svc := New(Config{TenantID: 9, AccountID: 3, AccountKey: "acct", Repo: repo, Adapter: &fakeAdapter{}, Runner: runner, CommandRouter: channelcontract.NewChannelCommandRouter(nil), UserResolver: func(context.Context, channelcontract.InboundMessage) (uint64, error) { return 42, nil }, SessionResolver: func(context.Context, uint64, uint64, channelcontract.Scope) (uint64, error) { return 99, nil }, PayloadCodec: JSONCodec{}})
	msg := channelcontract.InboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: "acct", EventID: "evt-status", ExternalMessageID: "om-status", ExternalConversationID: "oc-1", ExternalUserID: "ou-user", ChatType: channelcontract.ChatTypeP2P, Text: "/status"}
	if d := svc.HandleInbound(context.Background(), msg); !d.Ack {
		t.Fatal(d)
	}
	repo.conv.WorkspaceRealpath = "/workspace"
	repo.conv.PermissionMode = "deny"
	if err := svc.ProcessOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runner.calls != 0 || len(repo.outbox) != 1 {
		t.Fatalf("runner calls=%d outbox=%+v", runner.calls, repo.outbox)
	}
	for _, want := range []string{"99", "/workspace", "deny"} {
		if !strings.Contains(repo.outbox[0].PayloadJSON, want) {
			t.Fatalf("status payload missing %q: %s", want, repo.outbox[0].PayloadJSON)
		}
	}
}

func TestServiceCommandFailureProducesExplicitFailureCard(t *testing.T) {
	repo := &fakeRepo{conversationErr: errors.New("conversation lookup failed")}
	svc := New(Config{TenantID: 9, AccountID: 3, AccountKey: "acct", Repo: repo, Adapter: &fakeAdapter{}, Runner: &countingRunner{}, CommandRouter: channelcontract.NewChannelCommandRouter(nil), UserResolver: func(context.Context, channelcontract.InboundMessage) (uint64, error) { return 42, nil }, SessionResolver: func(context.Context, uint64, uint64, channelcontract.Scope) (uint64, error) { return 99, nil }, PayloadCodec: JSONCodec{}})
	msg := channelcontract.InboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: "acct", EventID: "evt-command-error", ExternalMessageID: "om-command-error", ExternalConversationID: "oc-1", ExternalUserID: "ou-user", ChatType: channelcontract.ChatTypeP2P, Text: "/status"}
	if disposition := svc.HandleInbound(context.Background(), msg); !disposition.Ack {
		t.Fatalf("disposition = %+v", disposition)
	}
	if err := svc.ProcessOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repo.outbox) != 1 || !strings.Contains(repo.outbox[0].PayloadJSON, "命令执行失败") {
		t.Fatalf("outbox = %+v", repo.outbox)
	}
	if repo.finished[repo.run.ID] != mysqlstore.ChannelRunStatusFailed {
		t.Fatalf("finished = %+v", repo.finished)
	}
}

func TestServiceStopCancelsActiveRunAndPersistsCancelledTerminalState(t *testing.T) {
	repo := &fakeRepo{}
	runner := &cancelableRunner{started: make(chan struct{}), cancelled: make(chan struct{})}
	svc := New(Config{TenantID: 9, AccountID: 3, AccountKey: "acct", Repo: repo, Adapter: &fakeAdapter{}, Runner: runner, CommandRouter: channelcontract.NewChannelCommandRouter(nil), UserResolver: func(context.Context, channelcontract.InboundMessage) (uint64, error) { return 42, nil }, SessionResolver: func(context.Context, uint64, uint64, channelcontract.Scope) (uint64, error) { return 99, nil }, PayloadCodec: JSONCodec{}})
	first := channelcontract.InboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: "acct", EventID: "evt-run", ExternalMessageID: "om-run", ExternalConversationID: "oc-1", ExternalUserID: "ou-user", ChatType: channelcontract.ChatTypeP2P, Text: "long task"}
	if d := svc.HandleInbound(context.Background(), first); !d.Ack {
		t.Fatal(d)
	}
	firstRunID := repo.run.ID
	done := make(chan error, 1)
	go func() { done <- svc.ProcessOne(context.Background()) }()
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("runner did not start")
	}
	stop := channelcontract.InboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: "acct", EventID: "evt-stop", ExternalMessageID: "om-stop", ExternalConversationID: "oc-1", ExternalUserID: "ou-user", ChatType: channelcontract.ChatTypeP2P, Text: "/stop"}
	if d := svc.HandleInbound(context.Background(), stop); !d.Ack {
		t.Fatal(d)
	}
	select {
	case <-runner.cancelled:
	case <-time.After(time.Second):
		t.Fatal("active runner was not cancelled")
	}
	if err := <-done; err != nil {
		t.Fatalf("cancelled run returned error: %v", err)
	}
	if !repo.cancelled || repo.finished[firstRunID] != mysqlstore.ChannelRunStatusCancelled {
		t.Fatalf("cancelled=%v finished=%+v run=%s", repo.cancelled, repo.finished, firstRunID)
	}
}

func TestServiceCWDAndPermissionCommandsPersistConversationControls(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	repo := &fakeRepo{}
	svc := New(Config{TenantID: 9, AccountID: 3, AccountKey: "acct", Repo: repo, Adapter: &fakeAdapter{}, Runner: &countingRunner{}, CommandRouter: channelcontract.NewChannelCommandRouter([]string{"ou-admin"}), WorkspaceRoots: []string{root}, DefaultWorkspace: root, DefaultPermissionMode: PermissionModeAsk, UserResolver: func(context.Context, channelcontract.InboundMessage) (uint64, error) { return 42, nil }, SessionResolver: func(context.Context, uint64, uint64, channelcontract.Scope) (uint64, error) { return 99, nil }, PayloadCodec: JSONCodec{}})
	commands := []struct {
		event string
		text  string
	}{
		{event: "evt-cwd", text: "/cwd child"},
		{event: "evt-permission", text: "/permission allow"},
	}
	for _, command := range commands {
		msg := channelcontract.InboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: "acct", EventID: command.event, ExternalMessageID: "om-" + command.event, ExternalConversationID: "oc-1", ExternalUserID: "ou-admin", ChatType: channelcontract.ChatTypeP2P, Text: command.text}
		if d := svc.HandleInbound(context.Background(), msg); !d.Ack {
			t.Fatal(d)
		}
		if err := svc.ProcessOne(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	wantWorkspace, _ := filepath.EvalSymlinks(child)
	if repo.conv.WorkspaceRealpath != wantWorkspace || repo.conv.PermissionMode != PermissionModeAllow {
		t.Fatalf("conversation controls = %+v", repo.conv)
	}
}

func TestServiceRunsOnlyUserInvocableSkillSlashCommands(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	skillDir := filepath.Join(project, ".claude", "skills", "channel-probe")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: channel-probe\ndescription: probe\nuser-invocable: true\n---\nRun channel probe for $ARGUMENTS."), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := &fakeRepo{}
	runner := &countingRunner{}
	svc := New(Config{TenantID: 9, AccountID: 3, AccountKey: "acct", Repo: repo, Adapter: &fakeAdapter{}, Runner: runner, CommandRouter: channelcontract.NewChannelCommandRouter(nil), DefaultWorkspace: project, UserResolver: func(context.Context, channelcontract.InboundMessage) (uint64, error) { return 42, nil }, SessionResolver: func(context.Context, uint64, uint64, channelcontract.Scope) (uint64, error) { return 99, nil }, PayloadCodec: JSONCodec{}})
	msg := channelcontract.InboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: "acct", EventID: "evt-skill", ExternalMessageID: "om-skill", ExternalConversationID: "oc-1", ExternalUserID: "ou-user", ChatType: channelcontract.ChatTypeP2P, Text: "/channel-probe evidence"}
	if d := svc.HandleInbound(context.Background(), msg); !d.Ack {
		t.Fatal(d)
	}
	if err := svc.ProcessOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runner.calls != 1 || !strings.Contains(runner.input.Prompt, "Run channel probe for evidence") {
		t.Fatalf("calls=%d prompt=%q", runner.calls, runner.input.Prompt)
	}
}

func TestServiceRoutesCardActionToPermissionBrokerWithoutCreatingInbox(t *testing.T) {
	callbackRepo := &permissionRepoFake{}
	sender := &permissionSenderFake{sent: make(chan struct{})}
	broker := NewPermissionBroker(PermissionBrokerConfig{TenantID: 9, AccountID: 3, RuntimeFingerprint: DefaultFingerprint, RuntimeFingerprintVersion: DefaultRuntimeVersion, Repo: callbackRepo, Sender: sender, Timeout: time.Second, AdminOpenIDs: []string{"ou-owner"}})
	decisionCh := make(chan channelcontract.PermissionDecision, 1)
	go func() {
		decisionCh <- broker.Request(context.Background(), channelcontract.PermissionRequest{ConversationID: 7, RunID: "run-1", UserID: 42, ExternalUserID: "ou-owner", ExternalChatID: "oc-1", ChatType: channelcontract.ChatTypeP2P, ToolName: "Bash"})
	}()
	<-sender.sent
	repo := &fakeRepo{}
	svc := New(Config{TenantID: 9, AccountID: 3, AccountKey: "acct", Repo: repo, Adapter: &fakeAdapter{}, PermissionBroker: broker, UserResolver: func(context.Context, channelcontract.InboundMessage) (uint64, error) { return 42, nil }, PayloadCodec: JSONCodec{}})
	callback := channelcontract.InboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: "acct", EventID: "evt-action", ExternalMessageID: "om-permission", ExternalConversationID: "oc-1", ExternalUserID: "ou-owner", ChatType: channelcontract.ChatTypeP2P, CardAction: &channelcontract.CardAction{Token: sender.token, Action: channelcontract.PermissionActionApproveOnce}}
	if disposition := svc.HandleInbound(context.Background(), callback); !disposition.Ack || disposition.ErrorCode != "" {
		t.Fatalf("disposition=%+v", disposition)
	}
	if decision := <-decisionCh; !decision.Allowed {
		t.Fatalf("decision=%+v", decision)
	}
	if len(repo.inbox) != 0 {
		t.Fatalf("callback created chat inbox row: %+v", repo.inbox)
	}
}

func TestServiceRuntimeFailureProducesVisibleFinalCard(t *testing.T) {
	repo := &fakeRepo{}
	svc := New(Config{TenantID: 9, AccountID: 3, AccountKey: "acct", Repo: repo, Adapter: &fakeAdapter{}, Runner: failingRunner{}, UserResolver: func(context.Context, channelcontract.InboundMessage) (uint64, error) { return 42, nil }, SessionResolver: func(context.Context, uint64, uint64, channelcontract.Scope) (uint64, error) { return 99, nil }, PayloadCodec: JSONCodec{}, RenderFinalCard: func(state channelcontract.CardState) ([]byte, error) {
		return json.Marshal(map[string]string{"status": string(state.Status), "text": state.Text})
	}})
	msg := channelcontract.InboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: "acct", EventID: "evt-fail", ExternalMessageID: "om-fail", ExternalConversationID: "oc-1", ExternalUserID: "ou-user", ChatType: channelcontract.ChatTypeP2P, Text: "fail"}
	if d := svc.HandleInbound(context.Background(), msg); !d.Ack {
		t.Fatal(d)
	}
	runID := repo.run.ID
	if err := svc.ProcessOne(context.Background()); err != nil {
		t.Fatalf("failure should be converted to a durable visible result: %v", err)
	}
	if repo.finished[runID] != mysqlstore.ChannelRunStatusFailed || len(repo.outbox) != 1 || !strings.Contains(repo.outbox[0].PayloadJSON, string(channelcontract.CardFailed)) {
		t.Fatalf("finished=%+v outbox=%+v", repo.finished, repo.outbox)
	}
}

func TestOutboxDeliveryFailureIsRetried(t *testing.T) {
	repo := &fakeRepo{outbox: []mysqlstore.ChannelOutbox{{ID: 4, TenantID: 9, AccountID: 3, Operation: mysqlstore.ChannelMessageOperationCreate, PayloadJSON: `{"provider":"feishu","account_id":"acct","external_chat_id":"oc-1","kind":"card","card":{"schema":"2.0"}}`}}}
	adapter := &fakeAdapter{fail: true}
	svc := New(Config{TenantID: 9, AccountID: 3, AccountKey: "acct", Repo: repo, Adapter: adapter, PayloadCodec: JSONCodec{}})
	if err := svc.ProcessOutboxOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(adapter.delivered) != 1 {
		t.Fatalf("deliveries = %d", len(adapter.delivered))
	}
}

func TestOutboxDeliveryPersistsProviderReceipt(t *testing.T) {
	repo := &fakeRepo{outbox: []mysqlstore.ChannelOutbox{{ID: 5, MessageID: 11, TenantID: 9, AccountID: 3, Operation: mysqlstore.ChannelMessageOperationCreate, PayloadJSON: `{"provider":"feishu","account_id":"acct","external_chat_id":"oc-1","kind":"card","card":{"schema":"2.0"}}`}}}
	svc := New(Config{TenantID: 9, AccountID: 3, AccountKey: "acct", Repo: repo, Adapter: &fakeAdapter{}, PayloadCodec: JSONCodec{}})
	if err := svc.ProcessOutboxOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if repo.sentExternal != "om_1" {
		t.Fatalf("external receipt = %q", repo.sentExternal)
	}
}

func TestBusyRunIsRequeuedForLaterProcessing(t *testing.T) {
	repo := &fakeRepo{busy: true}
	svc := New(Config{TenantID: 9, AccountID: 3, AccountKey: "acct", Repo: repo, Adapter: &fakeAdapter{}, Runner: fakeRunner{}, PayloadCodec: JSONCodec{}, UserResolver: func(context.Context, channelcontract.InboundMessage) (uint64, error) { return 42, nil }, SessionResolver: func(context.Context, uint64, uint64, channelcontract.Scope) (uint64, error) { return 99, nil }})
	msg := channelcontract.InboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: "acct", EventID: "evt-busy", ExternalMessageID: "om-busy", ExternalConversationID: "oc-busy", ExternalUserID: "ou-busy", ChatType: channelcontract.ChatTypeP2P, Text: "hello"}
	if disposition := svc.HandleInbound(context.Background(), msg); !disposition.Ack {
		t.Fatalf("disposition = %+v", disposition)
	}
	if err := svc.ProcessOne(context.Background()); !errors.Is(err, mysqlstore.ErrChannelRunBusy) {
		t.Fatalf("busy error = %v", err)
	}
	repo.busy = false
	if err := svc.ProcessOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repo.outbox) != 1 {
		t.Fatalf("outbox = %+v", repo.outbox)
	}
}

type timeoutRunner struct{}

func (timeoutRunner) Run(ctx context.Context, _ channelcontract.RunInput) (channelcontract.RunResult, error) {
	<-ctx.Done()
	return channelcontract.RunResult{}, ctx.Err()
}
func (timeoutRunner) RunStream(ctx context.Context, input channelcontract.RunInput, _ channelcontract.DeltaSink) (channelcontract.RunResult, error) {
	return timeoutRunner{}.Run(ctx, input)
}

func TestRunTimeoutStopsStuckModelExecution(t *testing.T) {
	repo := &fakeRepo{}
	svc := New(Config{TenantID: 9, AccountID: 3, AccountKey: "acct", Repo: repo, Adapter: &fakeAdapter{}, Runner: timeoutRunner{}, RunTimeout: 10 * time.Millisecond, PayloadCodec: JSONCodec{}, UserResolver: func(context.Context, channelcontract.InboundMessage) (uint64, error) { return 42, nil }, SessionResolver: func(context.Context, uint64, uint64, channelcontract.Scope) (uint64, error) { return 99, nil }})
	msg := channelcontract.InboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: "acct", EventID: "evt-timeout", ExternalMessageID: "om-timeout", ExternalConversationID: "oc-timeout", ExternalUserID: "ou-timeout", ChatType: channelcontract.ChatTypeP2P, Text: "timeout"}
	if disposition := svc.HandleInbound(context.Background(), msg); !disposition.Ack {
		t.Fatalf("disposition = %+v", disposition)
	}
	runID := repo.run.ID
	if err := svc.ProcessOne(context.Background()); err != nil {
		t.Fatalf("timeout should produce a visible failure result: %v", err)
	}
	if repo.finished[runID] != mysqlstore.ChannelRunStatusFailed || len(repo.outbox) != 1 {
		t.Fatalf("finished=%+v outbox=%+v", repo.finished, repo.outbox)
	}
}

type emptyRunner struct{}

func (emptyRunner) Run(context.Context, channelcontract.RunInput) (channelcontract.RunResult, error) {
	return channelcontract.RunResult{}, nil
}
func (emptyRunner) RunStream(context.Context, channelcontract.RunInput, channelcontract.DeltaSink) (channelcontract.RunResult, error) {
	return channelcontract.RunResult{}, nil
}

func TestEmptyModelResultProducesVisibleFailureCard(t *testing.T) {
	repo := &fakeRepo{}
	svc := New(Config{TenantID: 9, AccountID: 3, AccountKey: "acct", Repo: repo, Adapter: &fakeAdapter{}, Runner: emptyRunner{}, PayloadCodec: JSONCodec{}, UserResolver: func(context.Context, channelcontract.InboundMessage) (uint64, error) { return 42, nil }, SessionResolver: func(context.Context, uint64, uint64, channelcontract.Scope) (uint64, error) { return 99, nil }})
	msg := channelcontract.InboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: "acct", EventID: "evt-empty", ExternalMessageID: "om-empty", ExternalConversationID: "oc-empty", ExternalUserID: "ou-empty", ChatType: channelcontract.ChatTypeP2P, Text: "empty"}
	if disposition := svc.HandleInbound(context.Background(), msg); !disposition.Ack {
		t.Fatalf("disposition = %+v", disposition)
	}
	if err := svc.ProcessOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repo.outbox) != 1 || !strings.Contains(repo.outbox[0].PayloadJSON, "模型未返回可显示内容") {
		t.Fatalf("outbox = %+v", repo.outbox)
	}
}

func TestStreamingRunOpensUpdatesAndClosesCardStream(t *testing.T) {
	repo := &fakeRepo{}
	adapter := &fakeAdapter{}
	svc := New(Config{TenantID: 9, AccountID: 3, AccountKey: "acct", Repo: repo, Adapter: adapter, Runner: streamingRunner{}, Streaming: channelcontract.StreamingDecision{Enabled: true}, PayloadCodec: JSONCodec{}, UserResolver: func(context.Context, channelcontract.InboundMessage) (uint64, error) { return 42, nil }, SessionResolver: func(context.Context, uint64, uint64, channelcontract.Scope) (uint64, error) { return 99, nil }})
	msg := channelcontract.InboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: "acct", EventID: "evt-stream", ExternalMessageID: "om-stream", ExternalConversationID: "oc-stream", ExternalUserID: "ou-stream", ChatType: channelcontract.ChatTypeP2P, Text: "stream"}
	if disposition := svc.HandleInbound(context.Background(), msg); !disposition.Ack {
		t.Fatalf("disposition = %+v", disposition)
	}
	if err := svc.ProcessOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if adapter.streams == nil || len(adapter.streams.opened) != 1 || len(adapter.streams.updates) == 0 || !adapter.streams.closed {
		t.Fatalf("stream = %+v", adapter.streams)
	}
}

func TestTimelineCardStreamOpensOverflowPageAndUpdatesEarlierTool(t *testing.T) {
	adapter := &fakeAdapter{}
	render := func(state channelcontract.CardState) ([]channelcontract.RenderedCardPage, error) {
		return feishuchannel.RenderTimelineCards(state, feishuchannel.CardOptions{CallbackToken: "opaque-token"})
	}
	sink := newStreamSink(Config{Adapter: adapter, RenderTimelineCards: render, StreamMinInterval: time.Nanosecond, StreamMinChars: 1, Now: time.Now}, channelcontract.OutboundMessage{
		Provider: channelcontract.ProviderFeishu, AccountID: "acct", ExternalChatID: "oc-1", ReplyToMessageID: "om-source", Kind: channelcontract.MessageKindCard, CorrelationID: "run-pages",
	})
	ctx := context.Background()
	for index := 0; index < 7; index++ {
		if err := sink.OnDelta(ctx, channelcontract.Delta{Kind: channelcontract.DeltaTool, ToolID: fmt.Sprintf("tool-%d", index+1), ToolName: "Read", ToolStatus: channelcontract.ToolStatusRunning, ToolCommand: fmt.Sprintf("file-%d.go", index+1)}); err != nil {
			t.Fatal(err)
		}
	}
	if len(adapter.streamList) != 2 {
		t.Fatalf("opened streams = %d, want 2", len(adapter.streamList))
	}
	if hasCardElement(t, latestStreamCard(adapter.streamList[0]), "controls") {
		t.Fatal("first page retained controls after overflow page opened")
	}
	if !hasCardElement(t, latestStreamCard(adapter.streamList[1]), "controls") {
		t.Fatal("latest page missing controls")
	}
	firstUpdates := len(adapter.streamList[0].updates)
	secondUpdates := len(adapter.streamList[1].updates)
	if err := sink.OnDelta(ctx, channelcontract.Delta{Kind: channelcontract.DeltaTool, ToolID: "tool-1", ToolName: "Read", ToolStatus: channelcontract.ToolStatusComplete, ToolOutput: "done"}); err != nil {
		t.Fatal(err)
	}
	if len(adapter.streamList[0].updates) != firstUpdates+1 {
		t.Fatalf("first page updates = %d, want %d", len(adapter.streamList[0].updates), firstUpdates+1)
	}
	if len(adapter.streamList[1].updates) != secondUpdates {
		t.Fatalf("unaffected second page updates = %d, want %d", len(adapter.streamList[1].updates), secondUpdates)
	}
	sink.Finalize(ctx, "done", channelcontract.CardCompleted)
	if got := runtimeCardHeaderTitle(t, latestStreamCard(adapter.streamList[0])); got != "已完成 · 1/2" {
		t.Fatalf("first terminal page title = %q", got)
	}
	if got := runtimeCardHeaderTitle(t, latestStreamCard(adapter.streamList[1])); got != "已完成 · 2/2" {
		t.Fatalf("second terminal page title = %q", got)
	}
	if !adapter.streamList[0].closed || !adapter.streamList[1].closed {
		t.Fatalf("page streams were not all closed: %+v", adapter.streamList)
	}
}

func TestTimelineCardStreamReconcilesPagesRemovedByTextAmendment(t *testing.T) {
	adapter := &fakeAdapter{}
	render := func(state channelcontract.CardState) ([]channelcontract.RenderedCardPage, error) {
		return feishuchannel.RenderTimelineCards(state, feishuchannel.CardOptions{CallbackToken: "opaque-token"})
	}
	sink := newStreamSink(Config{Adapter: adapter, RenderTimelineCards: render, StreamMinInterval: time.Nanosecond, StreamMinChars: 1, Now: time.Now}, channelcontract.OutboundMessage{
		Provider: channelcontract.ProviderFeishu, AccountID: "acct", ExternalChatID: "oc-1", ReplyToMessageID: "om-source", Kind: channelcontract.MessageKindCard, CorrelationID: "run-contract",
	})
	longText := strings.Repeat("需要被替换的长文本。", 5000)
	if err := sink.OnDelta(context.Background(), channelcontract.Delta{Kind: channelcontract.DeltaText, Text: longText}); err != nil {
		t.Fatal(err)
	}
	if len(adapter.streamList) < 2 {
		t.Fatalf("opened streams = %d, want at least 2", len(adapter.streamList))
	}
	openedPages := len(adapter.streamList)
	if err := sink.OnDelta(context.Background(), channelcontract.Delta{Kind: channelcontract.DeltaTextAmend, PreviousText: longText, Text: "短文本"}); err != nil {
		t.Fatal(err)
	}
	if len(adapter.streamList) != openedPages {
		t.Fatalf("opened streams after contraction = %d, want %d", len(adapter.streamList), openedPages)
	}
	for index, stream := range adapter.streamList {
		card := latestStreamCard(stream)
		if strings.Contains(string(card), "需要被替换的长文本") {
			t.Fatalf("page %d retained amended text", index)
		}
		if got := runtimeCardHeaderTitle(t, card); got != fmt.Sprintf("处理中 · 第 %d 段", index+1) {
			t.Fatalf("page %d title = %q", index, got)
		}
	}
	if !strings.Contains(string(latestStreamCard(adapter.streamList[openedPages-1])), "此段内容已合并到前面的卡片") {
		t.Fatal("last opened page was not reconciled after contraction")
	}
	if hasCardElement(t, latestStreamCard(adapter.streamList[0]), "controls") {
		t.Fatal("first page retained controls after contraction")
	}
	if !hasCardElement(t, latestStreamCard(adapter.streamList[openedPages-1]), "controls") {
		t.Fatal("latest opened page missing controls after contraction")
	}
	pages := sink.Finalize(context.Background(), "短文本", channelcontract.CardCompleted)
	if len(pages) != openedPages {
		t.Fatalf("terminal pages = %d, want %d", len(pages), openedPages)
	}
	for index, stream := range adapter.streamList {
		if got := runtimeCardHeaderTitle(t, latestStreamCard(stream)); got != fmt.Sprintf("已完成 · %d/%d", index+1, openedPages) {
			t.Fatalf("page %d terminal title = %q", index, got)
		}
		if !stream.closed {
			t.Fatalf("page %d stream was not closed", index)
		}
	}
}

func TestTimelineCardStreamMovesQuestionControlsToLatestPageAfterContraction(t *testing.T) {
	adapter := &fakeAdapter{}
	render := func(state channelcontract.CardState) ([]channelcontract.RenderedCardPage, error) {
		return feishuchannel.RenderTimelineCards(state, feishuchannel.CardOptions{})
	}
	sink := newStreamSink(Config{Adapter: adapter, RenderTimelineCards: render, StreamMinInterval: time.Nanosecond, StreamMinChars: 1, Now: time.Now}, channelcontract.OutboundMessage{
		Provider: channelcontract.ProviderFeishu, AccountID: "acct", ExternalChatID: "oc-1", ReplyToMessageID: "om-source", Kind: channelcontract.MessageKindCard, CorrelationID: "run-question-contraction",
	})
	longText := strings.Repeat("等待确认的长文本。", 5000)
	if err := sink.OnDelta(context.Background(), channelcontract.Delta{Kind: channelcontract.DeltaText, Text: longText}); err != nil {
		t.Fatal(err)
	}
	if len(adapter.streamList) < 2 {
		t.Fatalf("opened streams = %d, want at least 2", len(adapter.streamList))
	}
	openedPages := len(adapter.streamList)
	if err := sink.OnDelta(context.Background(), channelcontract.Delta{Kind: channelcontract.DeltaTextAmend, PreviousText: longText, Text: "请选择"}); err != nil {
		t.Fatal(err)
	}
	pages := sink.FinalizeQuestion(context.Background(), channelcontract.InteractionQuestion{ID: "question-1", Token: "question-token", Question: "继续吗？", Choices: []string{"继续", "停止"}})
	if len(pages) != openedPages {
		t.Fatalf("question pages = %d, want %d", len(pages), openedPages)
	}
	if hasCardElement(t, latestStreamCard(adapter.streamList[0]), "question_choice_0") {
		t.Fatal("earlier page retained question controls")
	}
	latest := latestStreamCard(adapter.streamList[openedPages-1])
	if !hasCardElement(t, latest, "question_choice_0") || !strings.Contains(string(latest), "question-token") {
		t.Fatal("latest page missing usable question controls")
	}
}

func TestTimelineCardStreamDoesNotOpenLaterPageAfterEarlierOpenFailure(t *testing.T) {
	adapter := &fakeAdapter{failOpenAt: map[int]bool{0: true}}
	render := func(state channelcontract.CardState) ([]channelcontract.RenderedCardPage, error) {
		return feishuchannel.RenderTimelineCards(state, feishuchannel.CardOptions{})
	}
	sink := newStreamSink(Config{Adapter: adapter, RenderTimelineCards: render, StreamMinInterval: time.Nanosecond, StreamMinChars: 1, Now: time.Now}, channelcontract.OutboundMessage{
		Provider: channelcontract.ProviderFeishu, AccountID: "acct", ExternalChatID: "oc-1", ReplyToMessageID: "om-source", Kind: channelcontract.MessageKindCard, CorrelationID: "run-open-failure",
	})
	for index := 0; index < 7; index++ {
		if err := sink.OnDelta(context.Background(), channelcontract.Delta{Kind: channelcontract.DeltaTool, ToolID: fmt.Sprintf("tool-%d", index+1), ToolName: "Read", ToolStatus: channelcontract.ToolStatusRunning}); err != nil {
			t.Fatal(err)
		}
	}
	if len(adapter.openAttempts) != 1 {
		t.Fatalf("open attempts = %d, want only failed page 0 attempt", len(adapter.openAttempts))
	}
	if len(adapter.streamList) != 0 {
		t.Fatalf("opened later streams = %d, want 0", len(adapter.streamList))
	}
}

func TestTimelineFinalCreatesOneOutboxPerPageWhenStreamingDisabled(t *testing.T) {
	repo := &fakeRepo{}
	render := func(state channelcontract.CardState) ([]channelcontract.RenderedCardPage, error) {
		return feishuchannel.RenderTimelineCards(state, feishuchannel.CardOptions{})
	}
	svc := New(Config{TenantID: 9, AccountID: 3, AccountKey: "acct", Repo: repo, Adapter: &fakeAdapter{}, Runner: sevenToolRunner{}, RenderTimelineCards: render, PayloadCodec: JSONCodec{}, UserResolver: func(context.Context, channelcontract.InboundMessage) (uint64, error) { return 42, nil }, SessionResolver: func(context.Context, uint64, uint64, channelcontract.Scope) (uint64, error) { return 99, nil }})
	msg := channelcontract.InboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: "acct", EventID: "evt-pages", ExternalMessageID: "om-pages", ExternalConversationID: "oc-pages", ExternalUserID: "ou-pages", ChatType: channelcontract.ChatTypeP2P, Text: "pages"}
	if disposition := svc.HandleInbound(context.Background(), msg); !disposition.Ack {
		t.Fatalf("disposition = %+v", disposition)
	}
	if err := svc.ProcessOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repo.messages) != 2 || len(repo.outbox) != 2 {
		t.Fatalf("messages=%d outbox=%d, want 2/2", len(repo.messages), len(repo.outbox))
	}
	for index := 0; index < 2; index++ {
		wantKey := fmt.Sprintf("run:%s:timeline:page:%d", repo.run.ID, index)
		if repo.messages[index].IdempotencyKey != wantKey || repo.outbox[index].IdempotencyKey != wantKey {
			t.Fatalf("page %d keys = %q/%q, want %q", index, repo.messages[index].IdempotencyKey, repo.outbox[index].IdempotencyKey, wantKey)
		}
		if repo.outbox[index].SequenceNo != uint(index+1) || repo.outbox[index].ChunkCount != 2 {
			t.Fatalf("page %d sequence = %d/%d", index, repo.outbox[index].SequenceNo, repo.outbox[index].ChunkCount)
		}
	}
}

func TestTimelineFinalUsesUpdateOutboxForDegradedOpenedPage(t *testing.T) {
	repo := &fakeRepo{}
	adapter := &fakeAdapter{streams: &fakeCardStream{failUpdate: true}}
	render := func(state channelcontract.CardState) ([]channelcontract.RenderedCardPage, error) {
		return feishuchannel.RenderTimelineCards(state, feishuchannel.CardOptions{})
	}
	svc := New(Config{TenantID: 9, AccountID: 3, AccountKey: "acct", Repo: repo, Adapter: adapter, Runner: sevenToolRunner{}, Streaming: channelcontract.StreamingDecision{Enabled: true}, StreamMinInterval: time.Nanosecond, StreamMinChars: 1, RenderTimelineCards: render, PayloadCodec: JSONCodec{}, UserResolver: func(context.Context, channelcontract.InboundMessage) (uint64, error) { return 42, nil }, SessionResolver: func(context.Context, uint64, uint64, channelcontract.Scope) (uint64, error) { return 99, nil }})
	msg := channelcontract.InboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: "acct", EventID: "evt-degraded-page", ExternalMessageID: "om-degraded-page", ExternalConversationID: "oc-pages", ExternalUserID: "ou-pages", ChatType: channelcontract.ChatTypeP2P, Text: "pages"}
	if disposition := svc.HandleInbound(context.Background(), msg); !disposition.Ack {
		t.Fatalf("disposition = %+v", disposition)
	}
	if err := svc.ProcessOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(adapter.streamList) != 2 || len(repo.messages) != 2 {
		t.Fatalf("streams=%d messages=%d, want 2/2", len(adapter.streamList), len(repo.messages))
	}
	if len(repo.outbox) != 1 || repo.outbox[0].Operation != mysqlstore.ChannelMessageOperationUpdate || repo.outbox[0].MessageID != repo.messages[0].ID {
		t.Fatalf("degraded page outbox = %+v", repo.outbox)
	}
	if repo.messages[0].ExternalMessageID == "" || repo.messages[1].ExternalMessageID == "" {
		t.Fatalf("stream receipts not persisted: %+v", repo.messages)
	}
}

func latestStreamCard(stream *fakeCardStream) json.RawMessage {
	if len(stream.updates) > 0 {
		return stream.updates[len(stream.updates)-1]
	}
	return stream.opened[len(stream.opened)-1].Card
}

func hasCardElement(t *testing.T, card json.RawMessage, elementID string) bool {
	t.Helper()
	var decoded struct {
		Body struct {
			Elements []map[string]interface{} `json:"elements"`
		} `json:"body"`
	}
	if err := json.Unmarshal(card, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, element := range decoded.Body.Elements {
		if element["element_id"] == elementID {
			return true
		}
	}
	return false
}

func runtimeCardHeaderTitle(t *testing.T, card json.RawMessage) string {
	t.Helper()
	var decoded struct {
		Header struct {
			Title struct {
				Content string `json:"content"`
			} `json:"title"`
		} `json:"header"`
	}
	if err := json.Unmarshal(card, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded.Header.Title.Content
}

func TestFailedRunFinalCardRetainsToolDetails(t *testing.T) {
	repo := &fakeRepo{}
	var rendered channelcontract.CardState
	svc := New(Config{TenantID: 9, AccountID: 3, AccountKey: "acct", Repo: repo, Adapter: &fakeAdapter{}, Runner: toolDetailsRunner{}, PayloadCodec: JSONCodec{}, UserResolver: func(context.Context, channelcontract.InboundMessage) (uint64, error) { return 42, nil }, SessionResolver: func(context.Context, uint64, uint64, channelcontract.Scope) (uint64, error) { return 99, nil }, RenderFinalCard: func(state channelcontract.CardState) ([]byte, error) {
		rendered = state
		return []byte(`{"schema":"2.0"}`), nil
	}})
	msg := channelcontract.InboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: "acct", EventID: "evt-tool-fail", ExternalMessageID: "om-tool-fail", ExternalConversationID: "oc-tool-fail", ExternalUserID: "ou-1", ChatType: channelcontract.ChatTypeP2P, Text: "run"}
	if disposition := svc.HandleInbound(context.Background(), msg); !disposition.Ack {
		t.Fatal(disposition)
	}
	if err := svc.ProcessOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(rendered.Tools) != 1 || rendered.Tools[0].Command != "docker compose up" || rendered.Tools[0].IsError != true {
		t.Fatalf("rendered state = %+v", rendered)
	}
}

func TestStreamingFinalUpdateFailureFallsBackToFinalOutbox(t *testing.T) {
	repo := &fakeRepo{}
	adapter := &fakeAdapter{}
	adapter.streams = &fakeCardStream{failUpdate: true}
	svc := New(Config{TenantID: 9, AccountID: 3, AccountKey: "acct", Repo: repo, Adapter: adapter, Runner: streamingRunner{}, Streaming: channelcontract.StreamingDecision{Enabled: true}, PayloadCodec: JSONCodec{}, UserResolver: func(context.Context, channelcontract.InboundMessage) (uint64, error) { return 42, nil }, SessionResolver: func(context.Context, uint64, uint64, channelcontract.Scope) (uint64, error) { return 99, nil }})
	msg := channelcontract.InboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: "acct", EventID: "evt-stream-fail", ExternalMessageID: "om-stream-fail", ExternalConversationID: "oc-stream-fail", ExternalUserID: "ou-stream-fail", ChatType: channelcontract.ChatTypeP2P, Text: "stream fail"}
	if disposition := svc.HandleInbound(context.Background(), msg); !disposition.Ack {
		t.Fatalf("disposition = %+v", disposition)
	}
	if err := svc.ProcessOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repo.outbox) != 1 {
		t.Fatalf("expected final fallback outbox, got %+v", repo.outbox)
	}
}
