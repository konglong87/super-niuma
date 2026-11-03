package runtime

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	channelcontract "github.com/konglong87/super-niuma/internal/channel"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

const channelOutboxTargetNotReadyCode = "outbox_target_not_ready"

var errChannelOutboxTargetNotReady = errors.New("channel outbox target is not ready")

type OutboxDeliveryTargetRepository interface {
	GetChannelMessageForDelivery(context.Context, uint64, uint64, uint64) (mysqlstore.ChannelMessage, error)
}

type OutboxSequenceRepository interface {
	HasUnsentPriorChannelOutbox(context.Context, uint64, uint64, uint64, string, uint) (bool, error)
}

// Lease is the account-level fencing lease. A Redis-backed implementation is
// used in production; tests and explicitly single-instance development may use
// the local implementation.
type Lease interface {
	Acquire(context.Context, uint64, uint64, string, time.Duration) (release func(), err error)
}

type LocalLease struct{}

func (LocalLease) Acquire(context.Context, uint64, uint64, string, time.Duration) (func(), error) {
	return func() {}, nil
}

type WorkerConfig struct {
	Service           *Service
	Lease             Lease
	PollInterval      time.Duration
	LeaseTTL          time.Duration
	MaxConcurrentRuns int
}

type Worker struct {
	service *Service
	lease   Lease
	poll    time.Duration
	ttl     time.Duration
	maxRuns int
}

func NewWorker(cfg WorkerConfig) *Worker {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 250 * time.Millisecond
	}
	if cfg.LeaseTTL <= 0 {
		cfg.LeaseTTL = 30 * time.Second
	}
	if cfg.Lease == nil {
		cfg.Lease = LocalLease{}
	}
	if cfg.MaxConcurrentRuns <= 0 {
		cfg.MaxConcurrentRuns = 4
	}
	return &Worker{service: cfg.Service, lease: cfg.Lease, poll: cfg.PollInterval, ttl: cfg.LeaseTTL, maxRuns: cfg.MaxConcurrentRuns}
}

func (w *Worker) Run(ctx context.Context) error {
	if w == nil || w.service == nil || w.service.cfg.Adapter == nil {
		return errors.New("channel worker is not configured")
	}
	release, err := w.lease.Acquire(ctx, w.service.cfg.TenantID, w.service.cfg.AccountID, w.service.cfg.WorkerID, w.ttl)
	if err != nil {
		return err
	}
	defer release()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := w.service.cfg.Adapter.Start(runCtx, w.service.HandleInbound); err != nil {
		return err
	}
	defer func() { _ = w.service.cfg.Adapter.Stop(context.Background()) }()
	if err := w.service.RecoverPending(runCtx); err != nil {
		return err
	}
	ticker := time.NewTicker(w.poll)
	defer ticker.Stop()
	sem := make(chan struct{}, w.maxRuns)
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		select {
		case <-runCtx.Done():
			return nil
		case <-ticker.C:
			_ = w.service.ProcessInteractionsOnce(runCtx)
			_ = w.service.ProcessOutboxOnce(runCtx)
			_ = w.service.ProcessReactionsOnce(runCtx)
			select {
			case sem <- struct{}{}:
			default:
				continue
			}
			wg.Add(1)
			go func() { defer wg.Done(); defer func() { <-sem }(); _ = w.service.ProcessOne(runCtx) }()
		}
	}
}

func (s *Service) outboundOperation(ctx context.Context, row mysqlstore.ChannelOutbox, message channelcontract.OutboundMessage) (channelcontract.OutboundOperation, error) {
	operation := channelcontract.OutboundOperation{Operation: channelcontract.DeliveryOperation(row.Operation), Message: message}
	if operation.Operation == channelcontract.DeliveryCreate {
		return operation, nil
	}
	repository, ok := s.cfg.Repo.(OutboxDeliveryTargetRepository)
	if !ok {
		return channelcontract.OutboundOperation{}, errChannelOutboxTargetNotReady
	}
	target, err := repository.GetChannelMessageForDelivery(ctx, s.cfg.TenantID, s.cfg.AccountID, row.MessageID)
	if err != nil {
		return channelcontract.OutboundOperation{}, err
	}
	if target.Status != mysqlstore.ChannelMessageStatusSent || strings.TrimSpace(target.ExternalMessageID) == "" {
		return channelcontract.OutboundOperation{}, errChannelOutboxTargetNotReady
	}
	operation.MessageID = strings.TrimSpace(target.ExternalMessageID)
	return operation, nil
}
