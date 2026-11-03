package runtime

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	channelcontract "github.com/konglong87/super-niuma/internal/channel"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

type fakeLease struct {
	acquired atomic.Int32
	released atomic.Int32
}

type emittingAdapter struct {
	fakeAdapter
	message channelcontract.InboundMessage
}

type blockingRunner struct {
	started chan struct{}
	release chan struct{}
}

type updateDeliveryRepo struct {
	*fakeRepo
	message mysqlstore.ChannelMessage
	retries int
}

type sequenceGateRepo struct {
	*fakeRepo
	priorPending bool
}

func (r *sequenceGateRepo) HasUnsentPriorChannelOutbox(context.Context, uint64, uint64, uint64, string, uint) (bool, error) {
	return r.priorPending, nil
}

func (r *sequenceGateRepo) MarkOutboxRetry(ctx context.Context, tenantID, accountID, id uint64, next time.Time, code, message string) error {
	if id == 1 {
		r.priorPending = true
	}
	return r.fakeRepo.MarkOutboxRetry(ctx, tenantID, accountID, id, next, code, message)
}

func (r *updateDeliveryRepo) GetChannelMessageForDelivery(context.Context, uint64, uint64, uint64) (mysqlstore.ChannelMessage, error) {
	return r.message, nil
}

func (r *updateDeliveryRepo) MarkOutboxRetry(ctx context.Context, tenantID, accountID, id uint64, next time.Time, code, message string) error {
	r.retries++
	return r.fakeRepo.MarkOutboxRetry(ctx, tenantID, accountID, id, next, code, message)
}

func (r *blockingRunner) Run(ctx context.Context, input channelcontract.RunInput) (channelcontract.RunResult, error) {
	close(r.started)
	select {
	case <-r.release:
		return channelcontract.RunResult{FinalText: "done"}, nil
	case <-ctx.Done():
		return channelcontract.RunResult{}, ctx.Err()
	}
}
func (r *blockingRunner) RunStream(ctx context.Context, input channelcontract.RunInput, _ channelcontract.DeltaSink) (channelcontract.RunResult, error) {
	return r.Run(ctx, input)
}

func (a *emittingAdapter) Start(_ context.Context, handler channelcontract.InboundHandler) error {
	if disposition := handler(context.Background(), a.message); !disposition.Ack || disposition.Retryable {
		return context.Canceled
	}
	return nil
}

func (l *fakeLease) Acquire(context.Context, uint64, uint64, string, time.Duration) (func(), error) {
	l.acquired.Add(1)
	return func() { l.released.Add(1) }, nil
}

func TestWorkerStartsAdapterAndReleasesLeaseOnShutdown(t *testing.T) {
	adapter := &fakeAdapter{}
	lease := &fakeLease{}
	svc := New(Config{TenantID: 1, AccountID: 2, AccountKey: "acct", Repo: &fakeRepo{}, Adapter: adapter, PayloadCodec: JSONCodec{}})
	worker := NewWorker(WorkerConfig{Service: svc, Lease: lease, PollInterval: time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	time.Sleep(5 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
	if lease.acquired.Load() != 1 || lease.released.Load() != 1 {
		t.Fatalf("lease acquired=%d released=%d", lease.acquired.Load(), lease.released.Load())
	}
}

func TestWorkerProcessesInboundToFinalOutbox(t *testing.T) {
	repo := &fakeRepo{outboxReady: make(chan struct{}, 1)}
	adapter := &emittingAdapter{message: channelcontract.InboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: "acct", EventID: "evt-worker", ExternalMessageID: "om-worker", ExternalConversationID: "oc-worker", ExternalUserID: "ou-worker", ChatType: channelcontract.ChatTypeP2P, Text: "hello"}}
	svc := New(Config{TenantID: 1, AccountID: 2, AccountKey: "acct", Repo: repo, Adapter: adapter, Runner: fakeRunner{}, PayloadCodec: JSONCodec{}, UserResolver: func(context.Context, channelcontract.InboundMessage) (uint64, error) { return 4, nil }, SessionResolver: func(context.Context, uint64, uint64, channelcontract.Scope) (uint64, error) { return 5, nil }})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- NewWorker(WorkerConfig{Service: svc, Lease: &fakeLease{}, PollInterval: time.Millisecond}).Run(ctx)
	}()
	select {
	case <-repo.outboxReady:
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatal("worker did not enqueue final card")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(repo.inbox) != 1 || repo.inbox[0].Status != "processed" {
		t.Fatalf("inbox = %+v", repo.inbox)
	}
}

func TestWorkerDeliversExistingOutboxWhileRunIsBlocked(t *testing.T) {
	repo := &fakeRepo{outbox: []mysqlstore.ChannelOutbox{{ID: 90, MessageID: 11, TenantID: 1, AccountID: 2, Operation: mysqlstore.ChannelMessageOperationCreate, PayloadJSON: `{"provider":"feishu","account_id":"acct","external_chat_id":"oc-1","kind":"card","card":{"schema":"2.0"}}`}}}
	adapter := &emittingAdapter{
		fakeAdapter: fakeAdapter{deliveredSignal: make(chan struct{}, 1)},
		message:     channelcontract.InboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: "acct", EventID: "evt-blocked", ExternalMessageID: "om-blocked", ExternalConversationID: "oc-1", ExternalUserID: "ou-1", ChatType: channelcontract.ChatTypeP2P, Text: "blocked"},
	}
	runner := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	svc := New(Config{TenantID: 1, AccountID: 2, AccountKey: "acct", Repo: repo, Adapter: adapter, Runner: runner, PayloadCodec: JSONCodec{}, UserResolver: func(context.Context, channelcontract.InboundMessage) (uint64, error) { return 4, nil }, SessionResolver: func(context.Context, uint64, uint64, channelcontract.Scope) (uint64, error) { return 5, nil }})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- NewWorker(WorkerConfig{Service: svc, Lease: &fakeLease{}, PollInterval: time.Millisecond, MaxConcurrentRuns: 2}).Run(ctx)
	}()
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("run did not start")
	}
	select {
	case <-adapter.deliveredSignal:
	case <-time.After(time.Second):
		t.Fatal("outbox was blocked by model run")
	}
	close(runner.release)
	cancel()
	<-done
}

func TestWorkerResolvesPlatformMessageIDForBatchSummaryUpdate(t *testing.T) {
	base := &fakeRepo{outbox: []mysqlstore.ChannelOutbox{{ID: 91, MessageID: 41, TenantID: 1, AccountID: 2, Operation: mysqlstore.ChannelMessageOperationUpdate, PayloadJSON: `{"provider":"feishu","account_id":"acct","external_chat_id":"oc-1","kind":"card","card":{"schema":"2.0"}}`}}}
	repo := &updateDeliveryRepo{fakeRepo: base, message: mysqlstore.ChannelMessage{ID: 41, TenantID: 1, AccountID: 2, Status: mysqlstore.ChannelMessageStatusSent, ExternalMessageID: "om-final"}}
	adapter := &fakeAdapter{}
	svc := New(Config{TenantID: 1, AccountID: 2, AccountKey: "acct", Repo: repo, Adapter: adapter, PayloadCodec: JSONCodec{}})
	if err := svc.ProcessOutboxOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(adapter.delivered) != 1 || adapter.delivered[0].Operation != channelcontract.DeliveryUpdate || adapter.delivered[0].MessageID != "om-final" || repo.retries != 0 {
		t.Fatalf("delivered=%+v retries=%d", adapter.delivered, repo.retries)
	}
}

func TestWorkerRetriesBatchSummaryUntilOriginMessageIsSent(t *testing.T) {
	base := &fakeRepo{outbox: []mysqlstore.ChannelOutbox{{ID: 92, MessageID: 41, TenantID: 1, AccountID: 2, Operation: mysqlstore.ChannelMessageOperationUpdate, PayloadJSON: `{"provider":"feishu","account_id":"acct","external_chat_id":"oc-1","kind":"card","card":{"schema":"2.0"}}`}}}
	repo := &updateDeliveryRepo{fakeRepo: base, message: mysqlstore.ChannelMessage{ID: 41, TenantID: 1, AccountID: 2, Status: mysqlstore.ChannelMessageStatusPending}}
	adapter := &fakeAdapter{}
	svc := New(Config{TenantID: 1, AccountID: 2, AccountKey: "acct", Repo: repo, Adapter: adapter, PayloadCodec: JSONCodec{}})
	if err := svc.ProcessOutboxOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(adapter.delivered) != 0 || repo.retries != 1 {
		t.Fatalf("delivered=%+v retries=%d", adapter.delivered, repo.retries)
	}
}

func TestWorkerDefersLaterTimelinePageWhilePriorPageIsPending(t *testing.T) {
	base := &fakeRepo{outbox: []mysqlstore.ChannelOutbox{
		{ID: 1, TenantID: 1, AccountID: 2, ConversationID: 9, RunID: "run-pages", SequenceNo: 1, Operation: mysqlstore.ChannelMessageOperationCreate, PayloadJSON: `{"provider":"feishu","account_id":"acct","external_chat_id":"oc-1","kind":"card","card":{"schema":"2.0"}}`},
		{ID: 2, TenantID: 1, AccountID: 2, ConversationID: 9, RunID: "run-pages", SequenceNo: 2, Operation: mysqlstore.ChannelMessageOperationCreate, PayloadJSON: `{"provider":"feishu","account_id":"acct","external_chat_id":"oc-1","kind":"card","card":{"schema":"2.0"}}`},
	}}
	repo := &sequenceGateRepo{fakeRepo: base}
	adapter := &fakeAdapter{fail: true}
	svc := New(Config{TenantID: 1, AccountID: 2, AccountKey: "acct", Repo: repo, Adapter: adapter, PayloadCodec: JSONCodec{}})
	if err := svc.ProcessOutboxOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(adapter.delivered) != 1 {
		t.Fatalf("provider deliveries = %+v, want only the first page attempted", adapter.delivered)
	}
}

var _ channelcontract.Adapter = (*fakeAdapter)(nil)
