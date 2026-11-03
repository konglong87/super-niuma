// Package runtime contains the provider-neutral channel coordinator. Provider
// adapters only normalize events and deliver outbox operations; this package
// owns durable ingress, scope isolation and conversation execution.
package runtime

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/konglong87/super-niuma/internal/agentteam"
	channelcontract "github.com/konglong87/super-niuma/internal/channel"
	"github.com/konglong87/super-niuma/internal/imagegen"
	"github.com/konglong87/super-niuma/internal/observability"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

const (
	DefaultFingerprint     = "channel-runtime-v1"
	DefaultWorkerID        = "channel-worker-local"
	DefaultOutboxBatchSize = 32
	DefaultOutboxLease     = 30 * time.Second
	DefaultRetryDelay      = 2 * time.Second
	DefaultRunTimeout      = 2 * time.Minute
	DefaultRuntimeVersion  = 1
	maxErrorMessageBytes   = 1024
)

type Repository interface {
	OutboxSequenceRepository
	ClaimInboxEvent(context.Context, mysqlstore.ChannelInboxEventInput) (mysqlstore.ChannelInboxEvent, bool, error)
	MarkInboxQueued(context.Context, uint64, uint64, uint64, []byte) error
	MarkInboxProcessing(context.Context, uint64, uint64, string, time.Time) error
	MarkInboxProcessed(context.Context, uint64, uint64) error
	MarkInboxRetry(context.Context, uint64, uint64, time.Time, string, string) error
	MarkInboxFailed(context.Context, uint64, uint64, string, string) error
	MarkInboxIgnored(context.Context, uint64, uint64, string, string) error
	GetOrCreateChannelIdentity(context.Context, mysqlstore.ChannelIdentityInput) (mysqlstore.ChannelIdentity, error)
	GetOrCreateChannelConversation(context.Context, mysqlstore.ChannelConversationInput) (mysqlstore.ChannelConversation, error)
	CreateChannelRun(context.Context, mysqlstore.ChannelRunInput) (mysqlstore.ChannelRun, error)
	AttachRunInputs(context.Context, uint64, string, []mysqlstore.ChannelRunInputEvent) error
	ClaimConversationRun(context.Context, uint64, uint64, uint64, string) (mysqlstore.ChannelRun, error)
	HeartbeatChannelRun(context.Context, uint64, uint64, string, string, uint64) error
	FinishChannelRun(context.Context, uint64, uint64, string, string, string, string) error
	CreateChannelMessage(context.Context, mysqlstore.ChannelMessageInput) (mysqlstore.ChannelMessage, error)
	CreateOutboxMessage(context.Context, mysqlstore.ChannelOutboxInput) (mysqlstore.ChannelOutbox, error)
	ClaimDueOutbox(context.Context, uint64, uint64, string, int, time.Time) ([]mysqlstore.ChannelOutbox, error)
	MarkOutboxSent(context.Context, uint64, uint64, uint64) error
	MarkOutboxRetry(context.Context, uint64, uint64, uint64, time.Time, string, string) error
	MarkOutboxDeliveryUnknown(context.Context, uint64, uint64, uint64, string, string) error
	MarkOutboxDead(context.Context, uint64, uint64, uint64, string, string) error
}

type RecoveryRepository interface {
	RequeueStrandedChannelRuns(context.Context, uint64, uint64) error
	ClaimDueInbox(context.Context, uint64, uint64, string, int, time.Time) ([]mysqlstore.ChannelInboxEvent, error)
	GetQueuedChannelRun(context.Context, uint64, uint64, uint64) (mysqlstore.ChannelRun, error)
}

type WaitingInputRepository interface {
	MarkChannelRunWaitingInput(context.Context, uint64, uint64, string) error
	QueueWaitingChannelRun(context.Context, uint64, uint64, string) error
}

type MessageDeliveryRepository interface {
	MarkChannelMessageSent(context.Context, uint64, uint64, uint64, string) error
}

var _ Repository = (*mysqlstore.GormRepository)(nil)

type PayloadCodec interface {
	Encode(any) ([]byte, error)
	Decode([]byte, any) error
}

type payloadVersioner interface{ Version() string }

// JSONCodec is suitable only when the database payload column is protected by
// an equivalent storage encryption layer. Production deployments should inject
// an envelope-encrypting codec.
type JSONCodec struct{}

func (JSONCodec) Encode(v any) ([]byte, error) { return json.Marshal(v) }
func (JSONCodec) Decode(b []byte, v any) error { return json.Unmarshal(b, v) }

type Policy struct {
	AllowDM                bool
	AllowGroups            bool
	RequireMentionInGroups bool
}

// TeamDispatchInput is the durable channel context handed to a Team
// execution strategy after the common ingress path has created the Inbox,
// Conversation and ChannelRun records.
type TeamDispatchInput struct {
	Route   agentteam.RouteResult
	Message channelcontract.InboundMessage
	Inbox   mysqlstore.ChannelInboxEvent
	Run     mysqlstore.ChannelRun
	Scope   channelcontract.Scope
	UserID  uint64
	Stream  channelcontract.DeltaSink
}

type TeamDispatchResult struct {
	FinalText string
	Status    agentteam.Status
	Error     string
}

type TeamDispatchFunc func(context.Context, TeamDispatchInput) (TeamDispatchResult, error)

type Config struct {
	TenantID                  uint64
	AccountID                 uint64
	AccountKey                string
	Repo                      Repository
	Adapter                   channelcontract.Adapter
	Runner                    channelcontract.ConversationRunner
	PayloadCodec              PayloadCodec
	Policy                    Policy
	UserResolver              func(context.Context, channelcontract.InboundMessage) (uint64, error)
	SessionResolver           func(context.Context, uint64, uint64, channelcontract.Scope) (uint64, error)
	TenantStore               TenantStore
	RuntimeFingerprint        string
	RuntimeFingerprintVersion int
	WorkerID                  string
	Streaming                 channelcontract.StreamingDecision
	RenderFinalCard           func(channelcontract.CardState) ([]byte, error)
	RenderStreamingCard       func(channelcontract.CardState) ([]byte, error)
	RenderTimelineCards       func(channelcontract.CardState) ([]channelcontract.RenderedCardPage, error)
	StreamMinInterval         time.Duration
	StreamMinChars            int
	Now                       func() time.Time
	OutboxBatchSize           int
	OutboxLease               time.Duration
	RunTimeout                time.Duration
	CommandRouter             *channelcontract.ChannelCommandRouter
	WorkspaceRoots            []string
	Model                     string
	DefaultWorkspace          string
	DefaultPermissionMode     string
	Reactions                 *ReactionReconciler
	PermissionBroker          *PermissionBroker
	QuestionBroker            *QuestionBroker
	TeamRouter                *agentteam.Router
	TeamDispatch              TeamDispatchFunc
	ImageGenerator            imagegen.Generator
	ImageScheduler            imagegen.Scheduler
	AsyncChannelImages        bool
}

type Service struct {
	cfg        Config
	mu         sync.Mutex
	queue      []workItem
	activeMu   sync.Mutex
	activeRuns map[uint64]*activeRun
}

type activeRun struct {
	runID     string
	cancel    context.CancelFunc
	requested bool
}

type TenantStore interface {
	UpsertMessage(context.Context, mysqlstore.MessageInput) (uint64, error)
	ListMessages(context.Context, uint64, uint64, uint64, int) ([]mysqlstore.Message, error)
}

type SessionTurnStore interface {
	MaxMessageTurn(context.Context, uint64, uint64, uint64) (uint, error)
}

type workItem struct {
	Inbox        mysqlstore.ChannelInboxEvent
	Message      channelcontract.InboundMessage
	Scope        channelcontract.Scope
	Run          mysqlstore.ChannelRun
	UserID       uint64
	Resume       *channelcontract.ResumeInput
	SkipInbox    bool
	InboxClaimed bool
	TeamRoute    *agentteam.RouteResult
}

func New(cfg Config) *Service {
	if cfg.PayloadCodec == nil {
		cfg.PayloadCodec = JSONCodec{}
	}
	if cfg.RuntimeFingerprint == "" {
		cfg.RuntimeFingerprint = DefaultFingerprint
	}
	if cfg.RuntimeFingerprintVersion == 0 {
		cfg.RuntimeFingerprintVersion = DefaultRuntimeVersion
	}
	if cfg.WorkerID == "" {
		cfg.WorkerID = DefaultWorkerID
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.OutboxBatchSize <= 0 {
		cfg.OutboxBatchSize = DefaultOutboxBatchSize
	}
	if cfg.OutboxLease <= 0 {
		cfg.OutboxLease = DefaultOutboxLease
	}
	if cfg.RunTimeout <= 0 {
		cfg.RunTimeout = DefaultRunTimeout
	}
	if !cfg.Policy.AllowDM && !cfg.Policy.AllowGroups {
		cfg.Policy.AllowDM = true
		cfg.Policy.AllowGroups = true
	}
	return &Service{cfg: cfg, activeRuns: map[uint64]*activeRun{}}
}

func (s *Service) handleQuestionCallback(ctx context.Context, msg channelcontract.InboundMessage) channelcontract.InboundDisposition {
	if s.cfg.QuestionBroker == nil || s.cfg.UserResolver == nil {
		return channelcontract.IgnoreInbound("question_callback_unconfigured")
	}
	userID, err := s.cfg.UserResolver(ctx, msg)
	if err != nil {
		return channelcontract.IgnoreInbound("question_callback_identity_failed")
	}
	resume, err := s.cfg.QuestionBroker.Answer(ctx, channelcontract.InteractionAnswer{Token: msg.CardAction.Token, ChoiceID: msg.CardAction.ChoiceID, Source: channelcontract.InteractionAnswerButton, ExternalChatID: msg.ExternalConversationID, ExternalUserID: msg.ExternalUserID, UserID: userID})
	if err != nil {
		return channelcontract.IgnoreInbound("question_callback_rejected")
	}
	if err := s.queueInteractionResume(ctx, msg, userID, resume); err != nil {
		return channelcontract.RetryInbound("question_resume_queue_failed")
	}
	observability.Info(ctx, nil, "channel.interaction.answered", "channel.runtime", "channel question answered", "interaction_id", resume.Interaction.ID, "run_id", resume.RunID, "source", channelcontract.InteractionAnswerButton)
	return channelcontract.AcceptInbound()
}

func (s *Service) queueInteractionResume(ctx context.Context, msg channelcontract.InboundMessage, userID uint64, resume channelcontract.InteractionResume) error {
	transitioner, ok := s.cfg.Repo.(WaitingInputRepository)
	if !ok {
		return errors.New("repository does not support waiting input")
	}
	if err := transitioner.QueueWaitingChannelRun(ctx, s.cfg.TenantID, s.cfg.AccountID, resume.RunID); err != nil {
		return err
	}
	scope, err := channelcontract.NewScope(channelcontract.ProviderFeishu, s.cfg.AccountKey, msg.ExternalConversationID, resume.ExternalThreadID)
	if err != nil {
		return err
	}
	run := mysqlstore.ChannelRun{ID: resume.RunID, TenantID: s.cfg.TenantID, AccountID: s.cfg.AccountID, ConversationID: resume.ConversationID, SessionID: resume.SessionID, ScopeHash: scope.Hash[:], RuntimeFingerprint: s.cfg.RuntimeFingerprint, RuntimeFingerprintVersion: s.cfg.RuntimeFingerprintVersion, Status: mysqlstore.ChannelRunStatusQueued}
	s.mu.Lock()
	s.queue = append(s.queue, workItem{Message: msg, Scope: scope, Run: run, UserID: userID, Resume: &resume.Resume, SkipInbox: true})
	s.mu.Unlock()
	return nil
}

func (s *Service) HandleInbound(ctx context.Context, msg channelcontract.InboundMessage) channelcontract.InboundDisposition {
	if err := s.validateInbound(msg); err != nil {
		return channelcontract.IgnoreInbound(err.Error())
	}
	if msg.CardAction != nil {
		if msg.CardAction.Action == channelcontract.InteractionActionAnswer {
			return s.handleQuestionCallback(ctx, msg)
		}
		if s.cfg.PermissionBroker == nil || s.cfg.UserResolver == nil {
			return channelcontract.IgnoreInbound("permission_callback_unconfigured")
		}
		userID, err := s.cfg.UserResolver(ctx, msg)
		if err != nil {
			return channelcontract.IgnoreInbound("permission_callback_identity_failed")
		}
		if msg.ChatType != channelcontract.ChatTypeP2P || !s.cfg.PermissionBroker.Resolve(ctx, msg.CardAction.Token, msg.CardAction.Action, msg.ExternalUserID, msg.ExternalConversationID, msg.ExternalMessageID, userID) {
			return channelcontract.IgnoreInbound("permission_callback_rejected")
		}
		return channelcontract.AcceptInbound()
	}
	var teamRoute *agentteam.RouteResult
	if s.cfg.TeamRouter != nil {
		if route, ok := s.cfg.TeamRouter.Route(agentteam.InboundEvent{Provider: string(msg.Provider), AccountKey: msg.AccountID, ExternalConversationID: msg.ExternalConversationID, ExternalThreadID: msg.ExternalThreadID, ExternalUserID: msg.ExternalUserID, ChatType: string(msg.ChatType), Text: msg.Text, MentionedBot: msg.MentionedBot}); ok {
			teamRoute = &route
		}
	}
	if teamRoute == nil && msg.ChatType == channelcontract.ChatTypeGroup && s.cfg.Policy.RequireMentionInGroups && !msg.MentionedBot {
		return channelcontract.IgnoreInbound("mention_required")
	}
	if msg.ChatType == channelcontract.ChatTypeP2P && !s.cfg.Policy.AllowDM {
		return channelcontract.IgnoreInbound("dm_disabled")
	}
	if msg.ChatType == channelcontract.ChatTypeGroup && !s.cfg.Policy.AllowGroups {
		return channelcontract.IgnoreInbound("group_disabled")
	}
	thread := msg.ExternalThreadID
	if s.cfg.Adapter != nil {
		resolved, err := s.cfg.Adapter.ResolveThread(ctx, msg)
		if err != nil {
			return channelcontract.RetryInbound("thread_lookup_failed")
		}
		thread = resolved.ThreadID
	}
	scope, err := channelcontract.NewScope(channelcontract.ProviderFeishu, s.cfg.AccountKey, msg.ExternalConversationID, thread)
	if err != nil {
		return channelcontract.IgnoreInbound("invalid_scope")
	}
	payload, err := s.cfg.PayloadCodec.Encode(msg)
	if err != nil {
		return channelcontract.RetryInbound("payload_encode_failed")
	}
	hash := sha256.Sum256(payload)
	keyVersion := ""
	if versioned, ok := s.cfg.PayloadCodec.(payloadVersioner); ok {
		keyVersion = versioned.Version()
	}
	event, created, err := s.cfg.Repo.ClaimInboxEvent(ctx, mysqlstore.ChannelInboxEventInput{TenantID: s.cfg.TenantID, AccountID: s.cfg.AccountID, ProviderEventID: msg.EventID, ProviderMessageID: msg.ExternalMessageID, ScopeHash: scope.Hash[:], PayloadSHA256: hash[:], PayloadCiphertext: payload, PayloadKeyVersion: keyVersion, Authorized: true, Status: mysqlstore.ChannelInboxStatusReceived, ReceivedAt: ptrTime(s.cfg.Now())})
	if err != nil {
		return channelcontract.RetryInbound("inbox_claim_failed")
	}
	if !created {
		if event.Status == mysqlstore.ChannelInboxStatusQueued || event.Status == mysqlstore.ChannelInboxStatusProcessed || event.Status == mysqlstore.ChannelInboxStatusIgnored {
			return channelcontract.AcceptInbound()
		}
		return channelcontract.RetryInbound("inbox_pending")
	}
	if s.cfg.UserResolver == nil || s.cfg.SessionResolver == nil {
		return channelcontract.RetryInbound("runtime_resolver_unconfigured")
	}
	userID, err := s.cfg.UserResolver(ctx, msg)
	if err != nil {
		return channelcontract.RetryInbound("identity_resolve_failed")
	}
	sessionID, err := s.cfg.SessionResolver(ctx, s.cfg.TenantID, userID, scope)
	if err != nil {
		return channelcontract.RetryInbound("session_resolve_failed")
	}
	if _, err = s.cfg.Repo.GetOrCreateChannelIdentity(ctx, mysqlstore.ChannelIdentityInput{TenantID: s.cfg.TenantID, AccountID: s.cfg.AccountID, ExternalUserID: msg.ExternalUserID, UserID: userID}); err != nil {
		return channelcontract.RetryInbound("identity_persist_failed")
	}
	conversation, err := s.cfg.Repo.GetOrCreateChannelConversation(ctx, mysqlstore.ChannelConversationInput{TenantID: s.cfg.TenantID, AccountID: s.cfg.AccountID, ExternalChatID: msg.ExternalConversationID, ExternalThreadID: thread, ThreadIDSource: mysqlstore.ChannelThreadIDSourceEvent, ScopeKey: scope.Key, ScopeHash: scope.Hash[:], ExternalUserID: msg.ExternalUserID, ChatType: string(msg.ChatType), SessionID: sessionID, RuntimeFingerprint: s.cfg.RuntimeFingerprint, RuntimeFingerprintVersion: s.cfg.RuntimeFingerprintVersion, WorkspaceRealpath: s.cfg.DefaultWorkspace, PermissionMode: s.cfg.DefaultPermissionMode, Status: mysqlstore.ChannelConversationStatusActive, LastInboundAt: ptrTime(s.cfg.Now())})
	if err != nil {
		return channelcontract.RetryInbound("conversation_persist_failed")
	}
	if s.cfg.CommandRouter != nil {
		route := s.cfg.CommandRouter.Route(channelcontract.CommandInput{Text: msg.Text, ChatType: msg.ChatType, ExternalUserID: msg.ExternalUserID})
		if route.Kind == channelcontract.CommandRouteBuiltin && route.Invocation.Name == channelcontract.CommandStop {
			if commandRepo, ok := s.cfg.Repo.(CommandRepository); ok {
				_, _ = commandRepo.RequestCancelActiveChannelRun(ctx, s.cfg.TenantID, s.cfg.AccountID, conversation.ID)
			}
			s.cancelActiveRun(conversation.ID)
		}
	}
	if s.cfg.QuestionBroker != nil && strings.TrimSpace(msg.Text) != "" && strings.TrimSpace(msg.Text) != "/stop" {
		if pending, findErr := s.cfg.QuestionBroker.FindPending(ctx, conversation.ID, userID, msg.ExternalConversationID, s.cfg.Now()); findErr == nil {
			resume, answerErr := s.cfg.QuestionBroker.Answer(ctx, channelcontract.InteractionAnswer{InteractionID: pending.ID, Answer: msg.Text, Source: channelcontract.InteractionAnswerText, ExternalChatID: msg.ExternalConversationID, ExternalUserID: msg.ExternalUserID, UserID: userID})
			if answerErr != nil {
				return channelcontract.IgnoreInbound("question_text_answer_rejected")
			}
			if queueErr := s.queueInteractionResume(ctx, msg, userID, resume); queueErr != nil {
				return channelcontract.RetryInbound("question_resume_queue_failed")
			}
			observability.Info(ctx, nil, "channel.interaction.answered", "channel.runtime", "channel question answered", "interaction_id", resume.Interaction.ID, "run_id", resume.RunID, "source", channelcontract.InteractionAnswerText)
			if err := s.cfg.Repo.MarkInboxProcessed(ctx, s.cfg.TenantID, event.ID); err != nil {
				return channelcontract.RetryInbound("question_inbox_finalize_failed")
			}
			return channelcontract.AcceptInbound()
		} else if !errors.Is(findErr, mysqlstore.ErrNotFound) {
			return channelcontract.RetryInbound("question_lookup_failed")
		}
	}
	if s.cfg.TenantStore != nil {
		if _, err = s.cfg.TenantStore.UpsertMessage(ctx, mysqlstore.MessageInput{TenantID: s.cfg.TenantID, UserID: userID, SessionID: sessionID, TurnIndex: uint(event.ID * 2), Role: "user", Content: msg.Text}); err != nil {
			return channelcontract.RetryInbound("user_message_persist_failed")
		}
	}
	runID := newID()
	run, err := s.cfg.Repo.CreateChannelRun(ctx, mysqlstore.ChannelRunInput{ID: runID, TenantID: s.cfg.TenantID, AccountID: s.cfg.AccountID, ConversationID: conversation.ID, ScopeHash: scope.Hash[:], SessionID: sessionID, Status: mysqlstore.ChannelRunStatusQueued, RuntimeFingerprint: s.cfg.RuntimeFingerprint, RuntimeFingerprintVersion: s.cfg.RuntimeFingerprintVersion})
	if err != nil {
		return channelcontract.RetryInbound("run_persist_failed")
	}
	if err = s.cfg.Repo.AttachRunInputs(ctx, s.cfg.TenantID, run.ID, []mysqlstore.ChannelRunInputEvent{{TenantID: s.cfg.TenantID, RunID: run.ID, InboxEventID: event.ID, SequenceNo: 1}}); err != nil {
		return channelcontract.RetryInbound("run_input_persist_failed")
	}
	if err = s.cfg.Repo.MarkInboxQueued(ctx, s.cfg.TenantID, event.ID, conversation.ID, scope.Hash[:]); err != nil {
		return channelcontract.RetryInbound("inbox_queue_failed")
	}
	s.mu.Lock()
	s.queue = append(s.queue, workItem{Inbox: event, Message: msg, Scope: scope, Run: run, UserID: userID, TeamRoute: teamRoute})
	s.mu.Unlock()
	return channelcontract.AcceptInbound()
}

func (s *Service) ProcessOne(ctx context.Context) error {
	s.mu.Lock()
	if len(s.queue) == 0 {
		s.mu.Unlock()
		return nil
	}
	item := s.queue[0]
	s.queue = s.queue[1:]
	s.mu.Unlock()
	if !item.SkipInbox && !item.InboxClaimed {
		if err := s.cfg.Repo.MarkInboxProcessing(ctx, s.cfg.TenantID, item.Inbox.ID, s.cfg.WorkerID, s.cfg.Now().Add(s.cfg.OutboxLease)); err != nil {
			s.requeue(item)
			return err
		}
	}
	run, err := s.cfg.Repo.ClaimConversationRun(ctx, s.cfg.TenantID, s.cfg.AccountID, item.Run.ConversationID, s.cfg.WorkerID)
	if err != nil {
		if errors.Is(err, mysqlstore.ErrChannelRunBusy) {
			if !item.SkipInbox && !item.InboxClaimed {
				_ = s.cfg.Repo.MarkInboxQueued(ctx, s.cfg.TenantID, item.Inbox.ID, item.Run.ConversationID, item.Scope.Hash[:])
			}
			s.requeue(item)
			return err
		}
		if !item.SkipInbox && !item.InboxClaimed {
			_ = s.cfg.Repo.MarkInboxRetry(ctx, s.cfg.TenantID, item.Inbox.ID, s.cfg.Now().Add(DefaultRetryDelay), "run_claim_failed", safeError(err))
		}
		return err
	}
	if s.cfg.Reactions != nil {
		if reactionErr := s.cfg.Reactions.SetDesired(ctx, run.ConversationID, item.Message.ExternalMessageID, channelcontract.ReactionTyping); reactionErr == nil {
			_ = s.cfg.Reactions.ProcessOne(ctx)
		}
	}
	command, commandError := commandExecution{}, error(nil)
	if item.TeamRoute != nil {
		// Team commands are strategy input, not channel-runtime commands. Keep
		// them on the same durable run so reaction, outbox and terminal state
		// remain identical to ordinary channel messages.
		command = commandExecution{Handled: true, Prompt: item.Message.Text}
	} else if item.Resume == nil {
		command, commandError = s.executeCommand(ctx, item, run)
	}
	if commandError != nil {
		command = commandExecution{Handled: true, Text: "命令执行失败，请稍后重试。"}
	}
	workspace, permissionMode := s.cfg.DefaultWorkspace, s.cfg.DefaultPermissionMode
	if commandRepo, ok := s.cfg.Repo.(CommandRepository); ok {
		if current, currentErr := commandRepo.GetChannelConversationByScope(ctx, s.cfg.TenantID, s.cfg.AccountID, item.Scope.Hash[:]); currentErr == nil {
			workspace, permissionMode = current.WorkspaceRealpath, current.PermissionMode
		}
	}
	input := channelcontract.RunInput{TenantID: s.cfg.TenantID, AccountID: s.cfg.AccountID, UserID: item.UserID, TenantSessionID: run.SessionID, ConversationID: run.ConversationID, RunID: run.ID, Scope: item.Scope, RuntimeFingerprint: run.RuntimeFingerprint, Prompt: command.Prompt, Model: s.cfg.Model, WorkspaceRealpath: workspace, PermissionMode: permissionMode, ExternalUserID: item.Message.ExternalUserID, ExternalChatID: item.Message.ExternalConversationID, ReplyToMessageID: item.Message.ExternalMessageID, ChatType: item.Message.ChatType, Resume: item.Resume}
	runCtx := ctx
	cancel := func() {}
	if s.cfg.RunTimeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, s.cfg.RunTimeout)
	}
	defer cancel()
	var result channelcontract.RunResult
	runErrorCode := ""
	runErrorMessage := ""
	streamMessage := channelcontract.OutboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: s.cfg.AccountKey, ConversationID: fmt.Sprint(run.ConversationID), ExternalChatID: item.Message.ExternalConversationID, ExternalThreadID: item.Scope.ThreadID, ReplyToMessageID: item.Message.ExternalMessageID, Kind: channelcontract.MessageKindCard, CorrelationID: run.ID}
	var stream *streamSink
	if item.TeamRoute != nil {
		s.registerActiveRun(run.ConversationID, run.ID, cancel)
		defer s.unregisterActiveRun(run.ConversationID, run.ID)
		if s.cfg.Streaming.Enabled {
			stream = newStreamSink(s.cfg, streamMessage)
		}
		if s.cfg.TeamDispatch == nil {
			result = channelcontract.RunResult{FinalText: "执行失败，请稍后重试。"}
			runErrorCode = "team_dispatch_unconfigured"
			runErrorMessage = "team dispatch is not configured"
		} else {
			teamResult, teamErr := s.cfg.TeamDispatch(runCtx, TeamDispatchInput{Route: *item.TeamRoute, Message: item.Message, Inbox: item.Inbox, Run: run, Scope: item.Scope, UserID: item.UserID, Stream: stream})
			result = channelcontract.RunResult{FinalText: teamResult.FinalText}
			if teamResult.Status != "" && teamResult.Status != agentteam.StatusCompleted {
				runErrorCode = "team_run_" + strings.ToLower(string(teamResult.Status))
				runErrorMessage = safeError(errors.New(teamResult.Error))
			}
			err = teamErr
		}
	} else if commandError != nil {
		result = channelcontract.RunResult{FinalText: command.Text, Attachments: command.Attachments}
		runErrorCode = "command_failed"
		runErrorMessage = safeError(commandError)
	} else if command.Handled {
		result = channelcontract.RunResult{FinalText: command.Text, Attachments: command.Attachments}
	} else if s.cfg.Runner == nil {
		result = channelcontract.RunResult{FinalText: "执行失败，请稍后重试。"}
		runErrorCode = "runner_unconfigured"
		runErrorMessage = "runner is not configured"
	} else if s.cfg.Streaming.Enabled {
		s.registerActiveRun(run.ConversationID, run.ID, cancel)
		defer s.unregisterActiveRun(run.ConversationID, run.ID)
		stream = newStreamSink(s.cfg, streamMessage)
		if stream.adapter != nil {
			result, err = s.cfg.Runner.RunStream(runCtx, input, stream)
		} else {
			result, err = s.cfg.Runner.Run(runCtx, input)
		}
	} else {
		s.registerActiveRun(run.ConversationID, run.ID, cancel)
		defer s.unregisterActiveRun(run.ConversationID, run.ID)
		result, err = s.cfg.Runner.Run(runCtx, input)
	}
	cancelled := err != nil && errors.Is(err, context.Canceled) && s.activeRunCancellationRequested(run.ConversationID, run.ID)
	if err != nil {
		if cancelled {
			result.FinalText = "任务已停止。"
		} else {
			if !safePartialRunText(result.FinalText, err) {
				result.FinalText = "执行失败，请稍后重试。"
			}
			runErrorCode = "runtime_failed"
			runErrorMessage = safeError(err)
		}
	}
	if result.PendingInteraction != nil {
		if s.cfg.QuestionBroker != nil {
			return s.processPendingInteraction(ctx, item, run, input, result, stream)
		}
		result.FinalText = "当前渠道未启用交互提问，请直接提供你的选择。"
		result.PendingInteraction = nil
		runErrorCode = "question_interaction_disabled"
		runErrorMessage = "channel question interaction is disabled"
	}
	cardStatus := channelcontract.CardCompleted
	runStatus := mysqlstore.ChannelRunStatusCompleted
	if runErrorCode != "" {
		cardStatus = channelcontract.CardFailed
		runStatus = mysqlstore.ChannelRunStatusFailed
	}
	if cancelled {
		cardStatus = channelcontract.CardCancelled
		runStatus = mysqlstore.ChannelRunStatusCancelled
		runErrorCode = "cancelled_by_user"
		runErrorMessage = ""
	}
	if strings.TrimSpace(result.FinalText) == "" {
		result.FinalText = "模型未返回可显示内容，请稍后重试。"
		cardStatus = channelcontract.CardFailed
		runStatus = mysqlstore.ChannelRunStatusFailed
		runErrorCode = "empty_model_response"
	}
	var finalPages []channelcontract.RenderedCardPage
	if stream != nil {
		finalPages = stream.Finalize(ctx, result.FinalText, cardStatus)
	}
	if s.cfg.TenantStore != nil {
		turnIndex := uint(item.Inbox.ID*2 + 1)
		if item.SkipInbox {
			maxer, ok := s.cfg.TenantStore.(SessionTurnStore)
			if !ok {
				return s.finishFailure(ctx, item, run, "assistant_turn_index_unconfigured", errors.New("tenant store does not support max message turn"))
			}
			maxTurn, turnErr := maxer.MaxMessageTurn(ctx, s.cfg.TenantID, item.UserID, run.SessionID)
			if turnErr != nil {
				return s.finishFailure(ctx, item, run, "assistant_turn_index_failed", turnErr)
			}
			turnIndex = maxTurn + 1
		}
		if _, err = s.cfg.TenantStore.UpsertMessage(ctx, mysqlstore.MessageInput{TenantID: s.cfg.TenantID, UserID: item.UserID, SessionID: run.SessionID, TurnIndex: turnIndex, Role: "assistant", Content: result.FinalText}); err != nil {
			return s.finishFailure(ctx, item, run, "assistant_message_persist_failed", err)
		}
	}
	if len(finalPages) == 0 {
		finalPages, err = s.renderCardPages(channelcontract.CardState{RunID: run.ID, Status: cardStatus, Text: result.FinalText, Tools: result.Tools, Timeline: result.Timeline})
		if err != nil {
			return s.finishFailure(ctx, item, run, "card_render_failed", err)
		}
	}
	if err := s.persistFinalCardPages(ctx, item, run, finalPages, stream); err != nil {
		return s.finishFailure(ctx, item, run, "timeline_pages_persist_failed", err)
	}
	if err := s.enqueueImageOutbox(ctx, item, run, result.Attachments, len(finalPages)+1); err != nil {
		return s.finishFailure(ctx, item, run, "image_outbox_persist_failed", err)
	}
	_ = s.cfg.Repo.FinishChannelRun(ctx, s.cfg.TenantID, s.cfg.AccountID, run.ID, runStatus, runErrorCode, runErrorMessage)
	s.setTerminalReaction(ctx, item, runStatus)
	return s.markInboxProcessed(ctx, item)
}

func (s *Service) renderCardPages(state channelcontract.CardState) ([]channelcontract.RenderedCardPage, error) {
	if s.cfg.RenderTimelineCards != nil {
		return s.cfg.RenderTimelineCards(state)
	}
	card, err := s.renderCard(state)
	if err != nil {
		return nil, err
	}
	return []channelcontract.RenderedCardPage{{Index: 0, Card: card}}, nil
}

func (s *Service) persistFinalCardPages(ctx context.Context, item workItem, run mysqlstore.ChannelRun, pages []channelcontract.RenderedCardPage, stream *streamSink) error {
	return s.persistCardPages(ctx, item, run, pages, stream, func(pageIndex int) string {
		if s.cfg.RenderTimelineCards == nil {
			return "run:" + run.ID + ":final"
		}
		return timelinePageKey(run.ID, pageIndex)
	})
}

func (s *Service) persistCardPages(ctx context.Context, item workItem, run mysqlstore.ChannelRun, pages []channelcontract.RenderedCardPage, stream *streamSink, pageKey func(int) string) error {
	deliveries := map[int]streamPageDelivery{}
	if stream != nil {
		for _, delivery := range stream.PageDeliveries() {
			deliveries[delivery.Index] = delivery
		}
	}
	for index, page := range pages {
		pageIndex := page.Index
		if pageIndex < 0 {
			pageIndex = index
		}
		key := pageKey(pageIndex)
		outbound := channelcontract.OutboundMessage{
			Provider: channelcontract.ProviderFeishu, AccountID: s.cfg.AccountKey, ConversationID: fmt.Sprint(run.ConversationID),
			ExternalChatID: item.Message.ExternalConversationID, ExternalThreadID: item.Scope.ThreadID, ReplyToMessageID: item.Message.ExternalMessageID,
			Kind: channelcontract.MessageKindCard, Card: page.Card, IdempotencyKey: key, CorrelationID: run.ID,
		}
		payload, err := json.Marshal(outbound)
		if err != nil {
			return err
		}
		delivery := deliveries[pageIndex]
		status := mysqlstore.ChannelMessageStatusPending
		externalMessageID := ""
		var sentAt *time.Time
		if delivery.Opened && strings.TrimSpace(delivery.MessageID) != "" {
			status = mysqlstore.ChannelMessageStatusSent
			externalMessageID = delivery.MessageID
			sentAt = ptrTime(s.cfg.Now())
		}
		message, err := s.cfg.Repo.CreateChannelMessage(ctx, mysqlstore.ChannelMessageInput{
			TenantID: s.cfg.TenantID, AccountID: s.cfg.AccountID, ConversationID: run.ConversationID, RunID: run.ID,
			Direction: mysqlstore.ChannelMessageDirectionOutbound, Operation: mysqlstore.ChannelMessageOperationCreate,
			ExternalMessageID: externalMessageID, IdempotencyKey: key, ContentJSON: string(payload), Status: status, SentAt: sentAt,
		})
		if err != nil {
			return err
		}
		if delivery.Opened && !delivery.Degraded && externalMessageID != "" {
			continue
		}
		operation := mysqlstore.ChannelMessageOperationCreate
		if externalMessageID != "" {
			operation = mysqlstore.ChannelMessageOperationUpdate
		}
		if _, err := s.cfg.Repo.CreateOutboxMessage(ctx, mysqlstore.ChannelOutboxInput{
			TenantID: s.cfg.TenantID, AccountID: s.cfg.AccountID, ConversationID: run.ConversationID, RunID: run.ID,
			MessageID: message.ID, Operation: operation, SequenceNo: uint(index + 1), ChunkCount: uint(len(pages)),
			IdempotencyKey: key, PayloadJSON: string(payload), Status: mysqlstore.ChannelOutboxStatusPending, NextAttemptAt: ptrTime(s.cfg.Now()),
		}); err != nil {
			return err
		}
	}
	return nil
}

func safePartialRunText(text string, runErr error) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	errorText := ""
	if runErr != nil {
		errorText = strings.TrimSpace(runErr.Error())
	}
	return errorText == "" || !strings.Contains(text, errorText)
}

func (s *Service) enqueueImageOutbox(ctx context.Context, item workItem, run mysqlstore.ChannelRun, attachments []channelcontract.Attachment, sequenceStart int) error {
	for index, attachment := range attachments {
		if attachment.Type != "image" || strings.TrimSpace(attachment.ID) == "" || attachment.TenantID != s.cfg.TenantID || attachment.UserID != item.UserID || attachment.SessionID != run.SessionID {
			return errors.New("image attachment scope or metadata is invalid")
		}
		outbound := channelcontract.OutboundMessage{
			Provider: channelcontract.ProviderFeishu, AccountID: s.cfg.AccountKey,
			ConversationID: fmt.Sprint(run.ConversationID), ExternalChatID: item.Message.ExternalConversationID,
			ExternalThreadID: item.Scope.ThreadID, ReplyToMessageID: item.Message.ExternalMessageID,
			Kind: channelcontract.MessageKindImage, Attachments: []channelcontract.Attachment{attachment},
			IdempotencyKey: fmt.Sprintf("run:%s:image:%s", run.ID, attachment.ID), CorrelationID: run.ID,
		}
		payload, err := json.Marshal(outbound)
		if err != nil {
			return err
		}
		message, err := s.cfg.Repo.CreateChannelMessage(ctx, mysqlstore.ChannelMessageInput{TenantID: s.cfg.TenantID, AccountID: s.cfg.AccountID, ConversationID: run.ConversationID, RunID: run.ID, Direction: mysqlstore.ChannelMessageDirectionOutbound, Operation: mysqlstore.ChannelMessageOperationCreate, IdempotencyKey: outbound.IdempotencyKey, ContentJSON: string(payload), Status: mysqlstore.ChannelMessageStatusPending})
		if err != nil {
			return err
		}
		if _, err := s.cfg.Repo.CreateOutboxMessage(ctx, mysqlstore.ChannelOutboxInput{TenantID: s.cfg.TenantID, AccountID: s.cfg.AccountID, ConversationID: run.ConversationID, RunID: run.ID, MessageID: message.ID, Operation: mysqlstore.ChannelMessageOperationCreate, SequenceNo: uint(sequenceStart + index), ChunkCount: uint(len(attachments) + sequenceStart - 1), IdempotencyKey: outbound.IdempotencyKey, PayloadJSON: string(payload), Status: mysqlstore.ChannelOutboxStatusPending, NextAttemptAt: ptrTime(s.cfg.Now())}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) processPendingInteraction(ctx context.Context, item workItem, run mysqlstore.ChannelRun, input channelcontract.RunInput, result channelcontract.RunResult, stream *streamSink) error {
	if s.cfg.QuestionBroker == nil {
		return s.finishFailure(ctx, item, run, "question_broker_unconfigured", errors.New("question broker is not configured"))
	}
	transitioner, ok := s.cfg.Repo.(WaitingInputRepository)
	if !ok {
		return s.finishFailure(ctx, item, run, "waiting_input_unsupported", errors.New("repository does not support waiting input"))
	}
	question, err := s.cfg.QuestionBroker.Create(ctx, channelcontract.InteractionCreateInput{
		TenantID:                  input.TenantID,
		AccountID:                 s.cfg.AccountID,
		ConversationID:            input.ConversationID,
		RunID:                     input.RunID,
		SessionID:                 input.TenantSessionID,
		ExternalChatID:            input.ExternalChatID,
		ExternalThreadID:          input.Scope.ThreadID,
		ExternalUserID:            input.ExternalUserID,
		UserID:                    input.UserID,
		ScopeHash:                 append([]byte(nil), input.Scope.Hash[:]...),
		RuntimeFingerprint:        input.RuntimeFingerprint,
		RuntimeFingerprintVersion: s.cfg.RuntimeFingerprintVersion,
		Question:                  *result.PendingInteraction,
	})
	if err != nil {
		return s.finishFailure(ctx, item, run, "interaction_persist_failed", err)
	}
	if err := transitioner.MarkChannelRunWaitingInput(ctx, s.cfg.TenantID, s.cfg.AccountID, run.ID); err != nil {
		return s.finishFailure(ctx, item, run, "run_waiting_input_failed", err)
	}
	observability.Info(ctx, nil, "channel.interaction.created", "channel.runtime", "channel question waiting", "interaction_id", question.ID, "run_id", run.ID, "kind", question.Kind)
	var pages []channelcontract.RenderedCardPage
	if stream != nil {
		pages = stream.FinalizeQuestion(ctx, question)
	}
	if len(pages) == 0 {
		pages, err = s.renderCardPages(channelcontract.CardState{RunID: run.ID, Status: channelcontract.CardWaitingInput, Question: &question, Tools: result.Tools, Timeline: result.Timeline})
		if err != nil {
			return s.finishFailure(ctx, item, run, "card_render_failed", err)
		}
	}
	if err := s.persistCardPages(ctx, item, run, pages, stream, func(pageIndex int) string {
		if s.cfg.RenderTimelineCards == nil {
			return "interaction:" + question.ID + ":prompt"
		}
		return fmt.Sprintf("interaction:%s:prompt:page:%d", question.ID, pageIndex)
	}); err != nil {
		return s.finishFailure(ctx, item, run, "interaction_pages_persist_failed", err)
	}
	s.setTerminalReaction(ctx, item, mysqlstore.ChannelRunStatusWaitingInput)
	return s.markInboxProcessed(ctx, item)
}

func (s *Service) markInboxProcessed(ctx context.Context, item workItem) error {
	if item.SkipInbox || item.Inbox.ID == 0 {
		return nil
	}
	return s.cfg.Repo.MarkInboxProcessed(ctx, s.cfg.TenantID, item.Inbox.ID)
}

func (s *Service) ProcessReactionsOnce(ctx context.Context) error {
	if s == nil || s.cfg.Reactions == nil {
		return nil
	}
	return s.cfg.Reactions.ProcessOne(ctx)
}

func (s *Service) ProcessInteractionsOnce(ctx context.Context) error {
	if s == nil {
		return nil
	}
	expiry, ok := s.cfg.Repo.(InteractionExpiryRepository)
	if !ok {
		return nil
	}
	expired, err := expiry.ExpirePendingChannelInteractions(ctx, s.cfg.TenantID, s.cfg.AccountID, s.cfg.Now())
	if err != nil {
		return err
	}
	for _, interaction := range expired {
		_ = s.cfg.Repo.FinishChannelRun(ctx, s.cfg.TenantID, s.cfg.AccountID, interaction.RunID, mysqlstore.ChannelRunStatusInterrupted, "user_input_timeout", "user question expired")
	}
	return nil
}

func (s *Service) setTerminalReaction(ctx context.Context, item workItem, runStatus string) {
	if s.cfg.Reactions == nil {
		return
	}
	if runStatus == mysqlstore.ChannelRunStatusWaitingInput {
		return
	}
	emoji := channelcontract.ReactionDone
	if runStatus != mysqlstore.ChannelRunStatusCompleted {
		emoji = channelcontract.ReactionError
	}
	_ = s.cfg.Reactions.SetDesired(ctx, item.Run.ConversationID, item.Message.ExternalMessageID, emoji)
}

func (s *Service) registerActiveRun(conversationID uint64, runID string, cancel context.CancelFunc) {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	s.activeRuns[conversationID] = &activeRun{runID: runID, cancel: cancel}
}

func (s *Service) unregisterActiveRun(conversationID uint64, runID string) {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	if current := s.activeRuns[conversationID]; current != nil && current.runID == runID {
		delete(s.activeRuns, conversationID)
	}
}

func (s *Service) cancelActiveRun(conversationID uint64) bool {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	current := s.activeRuns[conversationID]
	if current == nil {
		return false
	}
	current.requested = true
	current.cancel()
	return true
}

func (s *Service) activeRunCancellationRequested(conversationID uint64, runID string) bool {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	current := s.activeRuns[conversationID]
	return current != nil && current.runID == runID && current.requested
}

func (s *Service) requeue(item workItem) {
	s.mu.Lock()
	s.queue = append([]workItem{item}, s.queue...)
	s.mu.Unlock()
}

// finalTextSink keeps the streaming contract provider-neutral. Incremental
// provider updates are deliberately persisted by the outbox layer in a later
// renderer extension; the first release still guarantees a durable final card.
type finalTextSink struct{}

func (*finalTextSink) OnDelta(context.Context, channelcontract.Delta) error { return nil }

// RecoverPending repopulates the in-memory dispatch queue after a worker
// restart. Durable Inbox rows remain the source of truth; this method is safe
// to call repeatedly because the repository lease claim is fencing-aware.
func (s *Service) RecoverPending(ctx context.Context) error {
	recovery, ok := s.cfg.Repo.(RecoveryRepository)
	if !ok {
		return errors.New("repository does not support inbox recovery")
	}
	if err := recovery.RequeueStrandedChannelRuns(ctx, s.cfg.TenantID, s.cfg.AccountID); err != nil {
		return err
	}
	rows, err := recovery.ClaimDueInbox(ctx, s.cfg.TenantID, s.cfg.AccountID, s.cfg.WorkerID, s.cfg.OutboxBatchSize, s.cfg.Now().Add(s.cfg.OutboxLease))
	if err != nil {
		return err
	}
	for _, row := range rows {
		var msg channelcontract.InboundMessage
		if err := s.cfg.PayloadCodec.Decode(row.PayloadCiphertext, &msg); err != nil {
			_ = s.cfg.Repo.MarkInboxFailed(ctx, s.cfg.TenantID, row.ID, "payload_decode_failed", safeError(err))
			continue
		}
		thread := msg.ExternalThreadID
		scope, err := channelcontract.NewScope(channelcontract.ProviderFeishu, s.cfg.AccountKey, msg.ExternalConversationID, thread)
		if err != nil {
			_ = s.cfg.Repo.MarkInboxFailed(ctx, s.cfg.TenantID, row.ID, "invalid_scope", safeError(err))
			continue
		}
		run, err := recovery.GetQueuedChannelRun(ctx, s.cfg.TenantID, s.cfg.AccountID, row.ConversationID)
		if err != nil {
			_ = s.cfg.Repo.MarkInboxRetry(ctx, s.cfg.TenantID, row.ID, s.cfg.Now().Add(DefaultRetryDelay), "run_not_found", safeError(err))
			continue
		}
		userID := uint64(0)
		if s.cfg.UserResolver != nil {
			userID, err = s.cfg.UserResolver(ctx, msg)
			if err != nil {
				_ = s.cfg.Repo.MarkInboxRetry(ctx, s.cfg.TenantID, row.ID, s.cfg.Now().Add(DefaultRetryDelay), "identity_resolve_failed", safeError(err))
				continue
			}
		}
		s.mu.Lock()
		s.queue = append(s.queue, workItem{Inbox: row, Message: msg, Scope: scope, Run: run, UserID: userID, InboxClaimed: true})
		s.mu.Unlock()
	}
	if s.cfg.QuestionBroker != nil {
		if interactions, ok := s.cfg.Repo.(InteractionRecoveryRepository); ok {
			answered, err := interactions.ListAnsweredChannelInteractions(ctx, s.cfg.TenantID, s.cfg.AccountID, s.cfg.OutboxBatchSize)
			if err != nil {
				return err
			}
			transitioner, canTransition := s.cfg.Repo.(WaitingInputRepository)
			for _, stored := range answered {
				if !canTransition {
					break
				}
				resume, resumeErr := s.cfg.QuestionBroker.ResumeStored(ctx, stored)
				if resumeErr != nil {
					continue
				}
				if err := transitioner.QueueWaitingChannelRun(ctx, s.cfg.TenantID, s.cfg.AccountID, resume.RunID); err != nil {
					continue
				}
				scope, scopeErr := channelcontract.NewScope(channelcontract.ProviderFeishu, s.cfg.AccountKey, resume.ExternalChatID, resume.ExternalThreadID)
				if scopeErr != nil {
					continue
				}
				s.mu.Lock()
				s.queue = append(s.queue, workItem{Message: channelcontract.InboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: s.cfg.AccountKey, ExternalConversationID: resume.ExternalChatID, ExternalThreadID: resume.ExternalThreadID, ExternalUserID: resume.ExternalUserID, ChatType: channelcontract.ChatTypeP2P}, Scope: scope, Run: mysqlstore.ChannelRun{ID: resume.RunID, TenantID: s.cfg.TenantID, AccountID: s.cfg.AccountID, ConversationID: resume.ConversationID, SessionID: resume.SessionID, ScopeHash: scope.Hash[:], RuntimeFingerprint: s.cfg.RuntimeFingerprint, RuntimeFingerprintVersion: s.cfg.RuntimeFingerprintVersion, Status: mysqlstore.ChannelRunStatusQueued}, UserID: resume.UserID, Resume: &resume.Resume, SkipInbox: true})
				s.mu.Unlock()
			}
		}
	}
	return nil
}

func (s *Service) ProcessOutboxOnce(ctx context.Context) error {
	if s.cfg.Adapter == nil {
		return errors.New("adapter is not configured")
	}
	rows, err := s.cfg.Repo.ClaimDueOutbox(ctx, s.cfg.TenantID, s.cfg.AccountID, s.cfg.WorkerID, s.cfg.OutboxBatchSize, s.cfg.Now().Add(s.cfg.OutboxLease))
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.SequenceNo > 1 && strings.TrimSpace(row.RunID) != "" {
			blocked, sequenceErr := s.cfg.Repo.HasUnsentPriorChannelOutbox(ctx, s.cfg.TenantID, s.cfg.AccountID, row.ConversationID, row.RunID, row.SequenceNo)
			if sequenceErr != nil || blocked {
				code := "prior_outbox_pending"
				if sequenceErr != nil {
					code = "prior_outbox_check_failed"
				}
				_ = s.cfg.Repo.MarkOutboxRetry(ctx, s.cfg.TenantID, s.cfg.AccountID, row.ID, s.cfg.Now().Add(DefaultRetryDelay), code, safeError(sequenceErr))
				continue
			}
		}
		var msg channelcontract.OutboundMessage
		if err := json.Unmarshal([]byte(row.PayloadJSON), &msg); err != nil {
			_ = s.cfg.Repo.MarkOutboxDead(ctx, s.cfg.TenantID, s.cfg.AccountID, row.ID, "invalid_payload", safeError(err))
			continue
		}
		operation, err := s.outboundOperation(ctx, row, msg)
		if err != nil {
			_ = s.cfg.Repo.MarkOutboxRetry(ctx, s.cfg.TenantID, s.cfg.AccountID, row.ID, s.cfg.Now().Add(DefaultRetryDelay), channelOutboxTargetNotReadyCode, safeError(err))
			continue
		}
		receipt, err := s.cfg.Adapter.Deliver(ctx, operation)
		if err != nil {
			_ = s.cfg.Repo.MarkOutboxRetry(ctx, s.cfg.TenantID, s.cfg.AccountID, row.ID, s.cfg.Now().Add(DefaultRetryDelay), "provider_delivery_failed", safeError(err))
			continue
		}
		if delivered, ok := s.cfg.Repo.(MessageDeliveryRepository); ok && receipt.MessageID != "" {
			if err := delivered.MarkChannelMessageSent(ctx, s.cfg.TenantID, s.cfg.AccountID, row.MessageID, receipt.MessageID); err != nil {
				_ = s.cfg.Repo.MarkOutboxDeliveryUnknown(ctx, s.cfg.TenantID, s.cfg.AccountID, row.ID, "receipt_persist_failed", safeError(err))
				continue
			}
		}
		if err := s.cfg.Repo.MarkOutboxSent(ctx, s.cfg.TenantID, s.cfg.AccountID, row.ID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) validateInbound(msg channelcontract.InboundMessage) error {
	if msg.Provider != channelcontract.ProviderFeishu || msg.AccountID != s.cfg.AccountKey {
		return errors.New("provider_or_account_mismatch")
	}
	if msg.EventID == "" || msg.ExternalConversationID == "" || msg.ExternalUserID == "" {
		return errors.New("inbound_identity_missing")
	}
	if !msg.ChatType.Valid() {
		return errors.New("chat_type_invalid")
	}
	return nil
}
func (s *Service) renderCard(state channelcontract.CardState) ([]byte, error) {
	if s.cfg.RenderFinalCard != nil {
		return s.cfg.RenderFinalCard(state)
	}
	return json.Marshal(map[string]any{"schema": "2.0", "header": map[string]any{"title": map[string]string{"tag": "plain_text", "content": "已完成"}}, "body": map[string]any{"elements": []any{map[string]string{"tag": "markdown", "content": state.Text}}}})
}
func (s *Service) finishFailure(ctx context.Context, item workItem, run mysqlstore.ChannelRun, code string, err error) error {
	safe := safeError(err)
	_ = s.cfg.Repo.FinishChannelRun(ctx, s.cfg.TenantID, s.cfg.AccountID, run.ID, mysqlstore.ChannelRunStatusFailed, code, safe)
	_ = s.cfg.Repo.MarkInboxFailed(ctx, s.cfg.TenantID, item.Inbox.ID, code, safe)
	s.setTerminalReaction(ctx, item, mysqlstore.ChannelRunStatusFailed)
	return err
}
func ptrTime(v time.Time) *time.Time { return &v }
func safeError(err error) string {
	if err == nil {
		return ""
	}
	text := strings.TrimSpace(err.Error())
	if len(text) > maxErrorMessageBytes {
		text = text[:maxErrorMessageBytes]
	}
	return text
}
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		fallback := sha256.Sum256([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
		return "run-" + hex.EncodeToString(fallback[:8])
	}
	return hex.EncodeToString(b[:])
}
