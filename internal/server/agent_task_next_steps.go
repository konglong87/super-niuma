package server

import (
	"context"
	"sync"
	"time"

	"github.com/konglong87/super-niuma/internal/nextsteps"
	"github.com/konglong87/super-niuma/internal/observability"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	"github.com/konglong87/super-niuma/internal/telemetry"
)

const (
	// next-steps generation is optional work and must have an independent,
	// bounded concurrency budget from detached agent runs.
	defaultAgentTaskNextStepsWorkers = 4
	defaultAgentTaskNextStepsQueue   = 16
)

// agentTaskNextStepsDispatcher owns the lifetime of asynchronous suggestion
// generation. The function fields keep the handler dependent on a small,
// injectable contract and make deterministic tests possible.
type agentTaskNextStepsDispatcher struct {
	enqueue            func(agentTaskNextStepsJob) bool
	reserve            func() bool
	releaseReservation func()
	enqueueReserved    func(agentTaskNextStepsJob) bool
	stop               func(context.Context)
	reservationTimeout time.Duration
}

type agentTaskNextStepsJob struct {
	task      mysqlstore.AgentTask
	cwd       string
	source    string
	traceID   string
	userID    string
	tenantKey string
	emitter   *telemetry.Emitter
	prompt    string
	response  string
	model     string
	provider  string
	toolNames []string
	cfg       nextsteps.Config
}

// newAgentTaskNextStepsDispatcher starts the production-sized fixed worker pool.
func newAgentTaskNextStepsDispatcher(ctx context.Context, opts Options) *agentTaskNextStepsDispatcher {
	return newAgentTaskNextStepsDispatcherWithLimits(ctx, opts, defaultAgentTaskNextStepsWorkers, defaultAgentTaskNextStepsQueue)
}

// newAgentTaskNextStepsDispatcherWithLimits is intentionally unexported so
// tests can exercise queue and worker boundaries without runtime knobs.
func newAgentTaskNextStepsDispatcherWithLimits(ctx context.Context, opts Options, workers, queueSize int) *agentTaskNextStepsDispatcher {
	if workers <= 0 {
		workers = defaultAgentTaskNextStepsWorkers
	}
	if queueSize <= 0 {
		queueSize = defaultAgentTaskNextStepsQueue
	}

	lifecycleCtx, cancel := context.WithCancel(ctx)
	queue := make(chan agentTaskNextStepsJob, queueSize)
	var workersWG sync.WaitGroup
	var stateMu sync.Mutex
	var stopOnce sync.Once
	stopped := false
	stopping := false
	reserved := 0
	reservationChanged := make(chan struct{})
	dispatcher := &agentTaskNextStepsDispatcher{reservationTimeout: agentTaskNextStepsTimeout}

	workersWG.Add(workers)
	for i := 0; i < workers; i++ {
		goSafe(lifecycleCtx, "server.agentTaskNextSteps.worker", nil, func() {
			defer workersWG.Done()
			for {
				select {
				case <-lifecycleCtx.Done():
					return
				case job, ok := <-queue:
					if !ok {
						return
					}
					if lifecycleCtx.Err() != nil {
						return
					}
					jobCtx, jobCancel := context.WithTimeout(lifecycleCtx, agentTaskNextStepsTimeout)
					jobCtx = observability.WithRequestValues(jobCtx, job.traceID, job.userID, job.tenantKey)
					jobCtx = telemetry.WithEmitter(jobCtx, job.emitter)
					done := make(chan struct{})
					goSafe(jobCtx, "server.appendAgentTaskNextSteps", map[string]any{"task_id": job.task.ID}, func() {
						defer close(done)
						appendAgentTaskNextStepsWithConfig(jobCtx, opts, job.task, job.cwd, job.source, job.traceID, job.prompt, job.response, job.provider, job.toolNames, job.cfg)
					})
					<-done
					jobCancel()
				}
			}
		})
	}

	dispatcher.reserve = func() bool {
		stateMu.Lock()
		defer stateMu.Unlock()
		if stopped || stopping || lifecycleCtx.Err() != nil || len(queue)+reserved >= queueSize {
			return false
		}
		reserved++
		return true
	}
	dispatcher.enqueueReserved = func(job agentTaskNextStepsJob) bool {
		stateMu.Lock()
		defer stateMu.Unlock()
		if reserved <= 0 {
			return false
		}
		reserved--
		close(reservationChanged)
		reservationChanged = make(chan struct{})
		if stopped || lifecycleCtx.Err() != nil {
			return false
		}
		select {
		case queue <- job:
			return true
		default:
			return false
		}
	}
	dispatcher.releaseReservation = func() {
		stateMu.Lock()
		defer stateMu.Unlock()
		if reserved > 0 {
			reserved--
			close(reservationChanged)
			reservationChanged = make(chan struct{})
		}
	}
	dispatcher.enqueue = func(job agentTaskNextStepsJob) bool {
		stateMu.Lock()
		defer stateMu.Unlock()
		if stopped || lifecycleCtx.Err() != nil {
			return false
		}
		select {
		case queue <- job:
			return true
		default:
			observability.Info(ctx, nil, "agent.next_steps.rejected", "server.agentTaskNextStepsDispatcher", "next-step job queue is full", "task_id", job.task.ID)
			return false
		}
	}
	dispatcher.stop = func(stopCtx context.Context) {
		stopOnce.Do(func() {
			if stopCtx == nil {
				stopCtx = context.Background()
			}
			stateMu.Lock()
			stopping = true
			wait := dispatcher.reservationTimeout
			if wait <= 0 {
				wait = agentTaskNextStepsTimeout
			}
			deadline := time.NewTimer(wait)
			timedOut := false
			for reserved > 0 {
				changed := reservationChanged
				stateMu.Unlock()
				select {
				case <-changed:
				case <-deadline.C:
					timedOut = true
				case <-stopCtx.Done():
					timedOut = true
				}
				stateMu.Lock()
				if timedOut {
					reserved = 0
					break
				}
			}
			if !deadline.Stop() {
				select {
				case <-deadline.C:
				default:
				}
			}
			stopped = true
			cancel()
			close(queue)
			stateMu.Unlock()
		})
		workersWG.Wait()
	}
	return dispatcher
}
