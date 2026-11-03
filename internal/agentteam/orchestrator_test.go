package agentteam

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

type fakeMemberRunner struct {
	mu       sync.Mutex
	calls    []string
	delay    time.Duration
	Failures map[string]bool
}

func (f *fakeMemberRunner) Run(_ context.Context, member Member, prompt string) (MemberResult, error) {
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	f.mu.Lock()
	f.calls = append(f.calls, member.Key+":"+prompt)
	f.mu.Unlock()
	if f.Failures[member.Key] {
		return MemberResult{MemberKey: member.Key, Status: StatusFailed, Error: "member failed"}, fmt.Errorf("member %s failed", member.Key)
	}
	return MemberResult{MemberKey: member.Key, Status: StatusCompleted, Content: member.Key + " result", Tokens: 10, Turns: 1}, nil
}

func TestOrchestratorRunsMembersAndCoordinatorOnlyFinal(t *testing.T) {
	runner := &fakeMemberRunner{}
	orchestrator := NewOrchestrator(runner, NewMemoryMailbox())
	team := Team{TenantID: 7, ID: 9, Key: "launch", Version: 1, Status: StatusPublished, Policy: TeamPolicy{Mode: ModeCoordinator, CoordinatorMember: "editor", MaxRounds: 2, MaxParallelMembers: 2, MaxTotalTokens: 1000}, Members: []Member{
		{TenantID: 7, Key: "editor", ProfileKey: "copywriter", ProfileVersion: 1, Role: RoleCoordinator},
		{TenantID: 7, Key: "researcher", ProfileKey: "researcher", ProfileVersion: 1, Role: RoleResearcher},
	}}

	result, err := orchestrator.Run(context.Background(), team, "prepare launch brief")
	if err != nil {
		t.Fatalf("run team: %v", err)
	}
	if result.Status != StatusCompleted || result.Final.MemberKey != "editor" || result.Final.Content == "" {
		t.Fatalf("result = %+v", result)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("runner calls = %v, want researcher + editor", runner.calls)
	}
	if result.Members[0].MemberKey == "editor" {
		t.Fatalf("coordinator should be final only, members=%+v", result.Members)
	}
}

func TestOrchestratorReturnsPartialWhenMemberFails(t *testing.T) {
	runner := &fakeMemberRunner{Failures: map[string]bool{"researcher": true}}
	orchestrator := NewOrchestrator(runner, NewMemoryMailbox())
	team := Team{TenantID: 7, ID: 9, Key: "launch", Version: 1, Status: StatusPublished, Policy: TeamPolicy{Mode: ModeParallelReview, CoordinatorMember: "editor", MaxRounds: 2, MaxParallelMembers: 2, MaxTotalTokens: 1000}, Members: []Member{
		{TenantID: 7, Key: "editor", Role: RoleCoordinator, ProfileKey: "copywriter", ProfileVersion: 1},
		{TenantID: 7, Key: "researcher", Role: RoleResearcher, ProfileKey: "researcher", ProfileVersion: 1},
		{TenantID: 7, Key: "reviewer", Role: RoleReviewer, ProfileKey: "reviewer", ProfileVersion: 1},
	}}

	result, err := orchestrator.Run(context.Background(), team, "review brief")
	if err != nil {
		t.Fatalf("partial team run should return terminal result, got error: %v", err)
	}
	if result.Status != StatusPartial || result.Final.MemberKey != "editor" {
		t.Fatalf("partial result = %+v", result)
	}
}

type recordingRunRecorder struct {
	started  RunRecord
	finished RunRecord
}

func (r *recordingRunRecorder) Start(_ context.Context, record RunRecord) error {
	r.started = record
	return nil
}

func (r *recordingRunRecorder) Finish(_ context.Context, record RunRecord) error {
	r.finished = record
	return nil
}

type recordingRunEvents struct {
	mu     sync.Mutex
	events []RunEvent
}

func (r *recordingRunEvents) Append(_ context.Context, event RunEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
	return nil
}

type failingMailbox struct {
	err error
}

func (m *failingMailbox) Append(context.Context, Message) error                   { return m.err }
func (m *failingMailbox) List(context.Context, uint64, string) ([]Message, error) { return nil, m.err }
func (m *failingMailbox) Consume(context.Context, uint64, string, string) error   { return m.err }

func TestOrchestratorPersistsMemberAndMailboxEvents(t *testing.T) {
	recorder := &recordingRunRecorder{}
	events := &recordingRunEvents{}
	orchestrator := NewOrchestratorWithRecorder(&fakeMemberRunner{}, NewMemoryMailbox(), recorder, events)
	team := Team{TenantID: 7, ID: 9, Key: "events", Version: 1, Status: StatusPublished, Policy: TeamPolicy{Mode: ModeParallelReview, CoordinatorMember: "editor", MaxRounds: 2, MaxParallelMembers: 2, MaxTotalTokens: 1000}, Members: []Member{{TenantID: 7, Key: "editor", Role: RoleCoordinator}, {TenantID: 7, Key: "researcher", Role: RoleResearcher}, {TenantID: 7, Key: "reviewer", Role: RoleReviewer}}}
	result, err := orchestrator.RunWithOptions(context.Background(), team, RunOptions{InboxEventID: 11, SourceAccountID: 12}, "event brief")
	if err != nil || result.Status != StatusCompleted {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	seen := map[string]bool{}
	for _, event := range events.events {
		seen[event.EventType] = true
		if event.EventType == RunEventMailboxMessage && event.Status != StatusCompleted {
			t.Fatalf("successful mailbox event status = %q, want %q", event.Status, StatusCompleted)
		}
	}
	for _, eventType := range []string{RunEventRunStarted, RunEventMemberStarted, RunEventMemberCompleted, RunEventMailboxMessage, RunEventCoordinatorStarted, RunEventCoordinatorDone, RunEventRunFinished} {
		if !seen[eventType] {
			t.Fatalf("missing event type %q in %+v", eventType, events.events)
		}
	}
	for index := 1; index < len(events.events); index++ {
		if events.events[index].Sequence <= events.events[index-1].Sequence {
			t.Fatalf("event sequence is not monotonic: %+v", events.events)
		}
	}
}

func TestOrchestratorRecordsMailboxAppendFailure(t *testing.T) {
	recorder := &recordingRunRecorder{}
	events := &recordingRunEvents{}
	orchestrator := NewOrchestratorWithRecorder(&fakeMemberRunner{}, &failingMailbox{err: fmt.Errorf("mailbox unavailable")}, recorder, events)
	team := Team{TenantID: 7, ID: 9, Key: "events", Version: 1, Status: StatusPublished, Policy: TeamPolicy{Mode: ModeCoordinator, CoordinatorMember: "editor", MaxRounds: 1, MaxParallelMembers: 1}, Members: []Member{{TenantID: 7, Key: "editor", Role: RoleCoordinator}, {TenantID: 7, Key: "researcher", Role: RoleResearcher}}}
	result, err := orchestrator.Run(context.Background(), team, "mailbox failure")
	if err != nil || result.Status != StatusCompleted {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	for _, event := range events.events {
		if event.EventType == RunEventMailboxMessage {
			if event.Status != StatusFailed || event.Summary != "mailbox unavailable" {
				t.Fatalf("mailbox event = %+v, want failed append diagnostic", event)
			}
			return
		}
	}
	t.Fatalf("mailbox failure event missing from %+v", events.events)
}

func TestOrchestratorMarksCoordinatorFailureTerminal(t *testing.T) {
	recorder := &recordingRunRecorder{}
	orchestrator := NewOrchestratorWithRecorder(&fakeMemberRunner{Failures: map[string]bool{"editor": true}}, NewMemoryMailbox(), recorder)
	team := Team{TenantID: 7, ID: 9, Key: "failed-coordinator", Version: 1, Status: StatusPublished, Policy: TeamPolicy{Mode: ModeCoordinator, CoordinatorMember: "editor", MaxRounds: 2, MaxTotalTokens: 1000}, Members: []Member{
		{TenantID: 7, Key: "editor", Role: RoleCoordinator},
		{TenantID: 7, Key: "researcher", Role: RoleResearcher},
	}}

	result, err := orchestrator.RunWithOptions(context.Background(), team, RunOptions{InboxEventID: 11, SourceAccountID: 12}, "fail safely")
	if err != nil {
		t.Fatalf("run team: %v", err)
	}
	if result.Status != StatusFailed {
		t.Fatalf("result status=%s, want %s", result.Status, StatusFailed)
	}
	if recorder.finished.Status != StatusFailed || recorder.finished.FinishedAt.IsZero() {
		t.Fatalf("finished record=%+v, want failed terminal record", recorder.finished)
	}
}

type streamingTeamRunner struct {
	coordinatorStreamed bool
}

func (r *streamingTeamRunner) Run(_ context.Context, member Member, _ string) (MemberResult, error) {
	return MemberResult{MemberKey: member.Key, Status: StatusCompleted, Content: member.Key + " result", Tokens: 1, Turns: 1}, nil
}

func (r *streamingTeamRunner) RunStream(_ context.Context, member Member, _ string, sink TextSink) (MemberResult, error) {
	r.coordinatorStreamed = true
	if err := sink.OnText("coordinator delta"); err != nil {
		return MemberResult{}, err
	}
	return MemberResult{MemberKey: member.Key, Status: StatusCompleted, Content: "coordinator final", Tokens: 1, Turns: 1}, nil
}

func TestOrchestratorStreamsCoordinatorWhenSinkProvided(t *testing.T) {
	runner := &streamingTeamRunner{}
	orchestrator := NewOrchestrator(runner, NewMemoryMailbox())
	team := Team{TenantID: 7, ID: 9, Key: "streaming", Version: 1, Status: StatusPublished, Policy: TeamPolicy{Mode: ModeCoordinator, CoordinatorMember: "editor", MaxRounds: 2, MaxTotalTokens: 1000}, Members: []Member{
		{TenantID: 7, Key: "editor", Role: RoleCoordinator},
		{TenantID: 7, Key: "researcher", Role: RoleResearcher},
	}}
	var deltas []string
	result, err := orchestrator.RunWithOptionsAndStream(context.Background(), team, RunOptions{}, TextSink(func(text string) error {
		deltas = append(deltas, text)
		return nil
	}), "stream brief")
	if err != nil || result.Status != StatusCompleted {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if !runner.coordinatorStreamed || len(deltas) != 1 || deltas[0] != "coordinator delta" {
		t.Fatalf("streamed=%v deltas=%v", runner.coordinatorStreamed, deltas)
	}
}

type contextAwareRunner struct{ coordinatorCalls int }

func (r *contextAwareRunner) Run(ctx context.Context, member Member, _ string) (MemberResult, error) {
	if member.Role == RoleCoordinator {
		r.coordinatorCalls++
	}
	select {
	case <-ctx.Done():
		return MemberResult{MemberKey: member.Key, Status: StatusTimedOut, Error: ctx.Err().Error()}, ctx.Err()
	default:
	}
	return MemberResult{MemberKey: member.Key, Status: StatusCompleted, Content: member.Key, Tokens: 80, Turns: 1}, nil
}

type coordinatorHeavyRunner struct{}

func (*coordinatorHeavyRunner) Run(_ context.Context, member Member, _ string) (MemberResult, error) {
	tokens := uint(10)
	if member.Role == RoleCoordinator {
		tokens = 80
	}
	return MemberResult{MemberKey: member.Key, Status: StatusCompleted, Content: member.Key, Tokens: tokens, Turns: 1}, nil
}

func TestOrchestratorDoesNotAcceptCoordinatorResultOverTokenCeiling(t *testing.T) {
	orchestrator := NewOrchestrator(&coordinatorHeavyRunner{}, NewMemoryMailbox())
	team := Team{TenantID: 7, ID: 9, Key: "coordinator-budget", Version: 1, Status: StatusPublished, Policy: TeamPolicy{Mode: ModeCoordinator, CoordinatorMember: "editor", MaxRounds: 2, MaxParallelMembers: 1, MaxTotalTokens: 50}, Members: []Member{{TenantID: 7, Key: "editor", Role: RoleCoordinator}, {TenantID: 7, Key: "researcher", Role: RoleResearcher}}}
	result, err := orchestrator.Run(context.Background(), team, "coordinator budget check")
	if err != nil {
		t.Fatalf("run team: %v", err)
	}
	if result.Status != StatusPartial || result.Final.Status != StatusCompleted || result.Error == "" {
		t.Fatalf("result=%+v", result)
	}
}

func TestOrchestratorStopsBeforeCoordinatorWhenTokenCeilingExceeded(t *testing.T) {
	runner := &contextAwareRunner{}
	orchestrator := NewOrchestrator(runner, NewMemoryMailbox())
	team := Team{TenantID: 7, ID: 9, Key: "budget", Version: 1, Status: StatusPublished, Policy: TeamPolicy{Mode: ModeCoordinator, CoordinatorMember: "editor", MaxRounds: 2, MaxParallelMembers: 1, MaxTotalTokens: 50}, Members: []Member{{TenantID: 7, Key: "editor", Role: RoleCoordinator}, {TenantID: 7, Key: "researcher", Role: RoleResearcher}}}
	result, err := orchestrator.Run(context.Background(), team, "budget check")
	if err != nil {
		t.Fatalf("run team: %v", err)
	}
	if result.Status != StatusPartial || runner.coordinatorCalls != 0 || result.Error == "" {
		t.Fatalf("result=%+v coordinatorCalls=%d", result, runner.coordinatorCalls)
	}
}

func TestOrchestratorDeadlineBecomesTimedOut(t *testing.T) {
	orchestrator := NewOrchestrator(&blockingRunner{}, NewMemoryMailbox())
	team := Team{TenantID: 7, ID: 9, Key: "timeout", Version: 1, Status: StatusPublished, Policy: TeamPolicy{Mode: ModeCoordinator, CoordinatorMember: "editor", MaxRounds: 2, MaxParallelMembers: 1, MaxTotalTokens: 1000, RunTimeoutSeconds: 1}, Members: []Member{{TenantID: 7, Key: "editor", Role: RoleCoordinator}, {TenantID: 7, Key: "researcher", Role: RoleResearcher}}}
	result, err := orchestrator.Run(context.Background(), team, "timeout check")
	if err != nil {
		t.Fatalf("run team: %v", err)
	}
	if result.Status != StatusTimedOut {
		t.Fatalf("status=%s error=%q", result.Status, result.Error)
	}
}

type blockingRunner struct{}

func (*blockingRunner) Run(ctx context.Context, member Member, _ string) (MemberResult, error) {
	<-ctx.Done()
	return MemberResult{MemberKey: member.Key, Status: StatusTimedOut, Error: ctx.Err().Error()}, ctx.Err()
}

type blockedRunEventRecorder struct {
	entered chan uint64
	release chan struct{}
	mu      sync.Mutex
	events  []RunEvent
}

func (r *blockedRunEventRecorder) Append(_ context.Context, event RunEvent) error {
	r.entered <- event.Sequence
	if event.Sequence == 1 {
		<-r.release
	}
	r.mu.Lock()
	r.events = append(r.events, event)
	r.mu.Unlock()
	return nil
}
func TestParallelEventPublicationFollowsAssignedSequence(t *testing.T) {
	recorder := &blockedRunEventRecorder{entered: make(chan uint64, 2), release: make(chan struct{})}
	orchestrator := NewOrchestratorWithRecorder(&fakeMemberRunner{}, NewMemoryMailbox(), nil, recorder)
	state := &runEventState{}
	var done sync.WaitGroup
	done.Add(1)
	go func() {
		defer done.Done()
		orchestrator.emitEvent(context.Background(), state, RunEvent{EventType: RunEventMemberStarted})
	}()
	if seq := <-recorder.entered; seq != 1 {
		t.Fatal(seq)
	}
	done.Add(1)
	go func() {
		defer done.Done()
		orchestrator.emitEvent(context.Background(), state, RunEvent{EventType: RunEventMemberCompleted})
	}()
	select {
	case seq := <-recorder.entered:
		close(recorder.release)
		done.Wait()
		t.Fatalf("sequence %d published before the prior event", seq)
	case <-time.After(10 * time.Millisecond):
	}
	close(recorder.release)
	done.Wait()
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.events) != 2 || recorder.events[0].Sequence != 1 || recorder.events[1].Sequence != 2 {
		t.Fatal(recorder.events)
	}
}
