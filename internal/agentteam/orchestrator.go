package agentteam

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type MemberResult struct {
	MemberKey string
	Status    Status
	Content   string
	Evidence  []string
	Tokens    uint
	Turns     uint
	Error     string
}

type TeamRunResult struct {
	RunID   string
	TeamID  uint64
	Status  Status
	Members []MemberResult
	Final   MemberResult
	Error   string
}

type MemberRunner interface {
	Run(context.Context, Member, string) (MemberResult, error)
}

// TextSink receives coordinator text deltas without coupling Agent Team to a
// channel provider's card or transport implementation.
type TextSink func(string) error

func (s TextSink) OnText(text string) error {
	if s == nil {
		return nil
	}
	return s(text)
}

type MemberStreamer interface {
	RunStream(context.Context, Member, string, TextSink) (MemberResult, error)
}

type Orchestrator struct {
	runner   MemberRunner
	mailbox  Mailbox
	recorder RunRecorder
	events   RunEventRecorder
	now      func() time.Time
}

type runEventState struct {
	mu       sync.Mutex
	sequence uint64
}

func NewOrchestrator(runner MemberRunner, mailbox Mailbox) *Orchestrator {
	return &Orchestrator{runner: runner, mailbox: mailbox, now: time.Now}
}

func NewOrchestratorWithRecorder(runner MemberRunner, mailbox Mailbox, recorder RunRecorder, events ...RunEventRecorder) *Orchestrator {
	orchestrator := &Orchestrator{runner: runner, mailbox: mailbox, recorder: recorder, now: time.Now}
	if len(events) > 0 {
		orchestrator.events = events[0]
	}
	return orchestrator
}

func (o *Orchestrator) Run(ctx context.Context, team Team, prompt string) (TeamRunResult, error) {
	return o.RunWithOptions(ctx, team, RunOptions{}, prompt)
}

func (o *Orchestrator) RunWithOptions(ctx context.Context, team Team, options RunOptions, prompt string) (TeamRunResult, error) {
	return o.runWithOptionsAndStream(ctx, team, options, nil, prompt)
}

func (o *Orchestrator) RunWithOptionsAndStream(ctx context.Context, team Team, options RunOptions, sink TextSink, prompt string) (TeamRunResult, error) {
	return o.runWithOptionsAndStream(ctx, team, options, sink, prompt)
}

func (o *Orchestrator) runWithOptionsAndStream(ctx context.Context, team Team, options RunOptions, sink TextSink, prompt string) (TeamRunResult, error) {
	if o == nil || o.runner == nil || team.TenantID == 0 || team.ID == 0 || strings.TrimSpace(prompt) == "" {
		return TeamRunResult{}, ErrTeamNotFound
	}
	coordinator, workers, ok := teamMembers(team)
	if !ok {
		return TeamRunResult{}, ErrTeamNotFound
	}
	runID := fmt.Sprintf("team-run-%d", o.now().UnixNano())
	runCtx := ctx
	var cancel context.CancelFunc
	if team.Policy.RunTimeoutSeconds > 0 {
		runCtx, cancel = context.WithTimeout(ctx, time.Duration(team.Policy.RunTimeoutSeconds)*time.Second)
		defer cancel()
	}
	result := TeamRunResult{RunID: runID, TeamID: team.ID, Status: StatusRunning}
	eventState := &runEventState{}
	record := RunRecord{RunID: runID, TenantID: team.TenantID, TeamID: team.ID, InboxEventID: options.InboxEventID, SourceAccountID: options.SourceAccountID, ConversationID: options.ConversationID, CoordinatorMember: coordinator.Key, Status: StatusRunning, TeamEffectiveHash: team.EffectiveHash, MemberCount: uint(len(team.Members)), MaxRounds: team.Policy.MaxRounds, MaxParallelMembers: team.Policy.MaxParallelMembers, MaxTotalTokens: team.Policy.MaxTotalTokens}
	record.StartedAt = o.now()
	if o.recorder != nil {
		if err := o.recorder.Start(ctx, record); err != nil {
			return TeamRunResult{}, err
		}
	}
	o.emitEvent(ctx, eventState, RunEvent{TenantID: team.TenantID, RunID: runID, EventType: RunEventRunStarted, Status: StatusRunning, Summary: "TeamRun started"})
	finish := func(finalResult TeamRunResult) (TeamRunResult, error) {
		o.emitEvent(ctx, eventState, RunEvent{TenantID: team.TenantID, RunID: runID, EventType: RunEventRunFinished, Status: finalResult.Status, MemberKey: coordinator.Key, Summary: eventSummary(finalResult.Error, "TeamRun finished"), PayloadJSON: teamRunResultPayload(finalResult)})
		if o.recorder == nil {
			return finalResult, nil
		}
		record.Status = finalResult.Status
		record.UsedTokens = finalResult.usageTokens()
		record.UsedTurns = finalResult.usageTurns()
		record.Result = finalResult.Final.Content
		record.Error = finalResult.Error
		record.FinishedAt = o.now()
		if err := o.recorder.Finish(ctx, record); err != nil {
			return finalResult, err
		}
		return finalResult, nil
	}
	memberResults := o.runMembers(runCtx, team, workers, prompt, runID, eventState)
	result.Members = append(result.Members, memberResults...)
	partial := false
	for _, member := range memberResults {
		if member.Status != StatusCompleted {
			partial = true
		}
	}
	if runCtx.Err() != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			result.Status = StatusTimedOut
		} else {
			result.Status = StatusCancelled
		}
		result.Error = runCtx.Err().Error()
		return finish(result)
	}
	if team.Policy.MaxTotalTokens > 0 && memberResultsUsageTokens(memberResults) > team.Policy.MaxTotalTokens {
		result.Status = StatusPartial
		result.Error = fmt.Sprintf("team token budget exceeded: %d > %d", memberResultsUsageTokens(memberResults), team.Policy.MaxTotalTokens)
		return finish(result)
	}

	coordinatorPrompt := buildCoordinatorPrompt(prompt, memberResults)
	o.emitEvent(runCtx, eventState, RunEvent{TenantID: team.TenantID, RunID: runID, EventType: RunEventCoordinatorStarted, MemberKey: coordinator.Key, Status: StatusRunning, Summary: "Coordinator started"})
	var final MemberResult
	var err error
	if streamer, ok := o.runner.(MemberStreamer); ok && sink != nil {
		final, err = streamer.RunStream(runCtx, coordinator, coordinatorPrompt, sink)
	} else {
		final, err = o.runner.Run(runCtx, coordinator, coordinatorPrompt)
	}
	if err != nil {
		o.emitEvent(runCtx, eventState, RunEvent{TenantID: team.TenantID, RunID: runID, EventType: RunEventMemberFailed, MemberKey: coordinator.Key, Status: StatusFailed, Summary: err.Error()})
		finalStatus := StatusFailed
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			finalStatus = StatusTimedOut
		} else if errors.Is(runCtx.Err(), context.Canceled) {
			finalStatus = StatusCancelled
		}
		result.Status = finalStatus
		result.Error = err.Error()
		result.Final = MemberResult{MemberKey: coordinator.Key, Status: finalStatus, Error: err.Error()}
		return finish(result)
	}
	if final.MemberKey == "" {
		final.MemberKey = coordinator.Key
	}
	if final.Status == "" {
		final.Status = StatusCompleted
	}
	result.Final = final
	o.emitEvent(runCtx, eventState, RunEvent{TenantID: team.TenantID, RunID: runID, EventType: RunEventCoordinatorDone, MemberKey: coordinator.Key, Status: final.Status, Summary: eventSummary(final.Error, final.Content), PayloadJSON: memberResultPayload(final)})
	if team.Policy.MaxTotalTokens > 0 && result.usageTokens() > team.Policy.MaxTotalTokens {
		result.Status = StatusPartial
		result.Error = fmt.Sprintf("team token budget exceeded: %d > %d", result.usageTokens(), team.Policy.MaxTotalTokens)
		return finish(result)
	}
	if final.Status != StatusCompleted {
		partial = true
	}
	if partial {
		result.Status = StatusPartial
	} else {
		result.Status = StatusCompleted
	}
	return finish(result)
}

func memberResultsUsageTokens(results []MemberResult) uint {
	var total uint
	for _, result := range results {
		total += result.Tokens
	}
	return total
}

func (r TeamRunResult) usageTokens() uint {
	var total uint
	for _, member := range r.Members {
		total += member.Tokens
	}
	total += r.Final.Tokens
	return total
}

func (r TeamRunResult) usageTurns() uint {
	var total uint
	for _, member := range r.Members {
		total += member.Turns
	}
	total += r.Final.Turns
	return total
}

func teamMembers(team Team) (Member, []Member, bool) {
	var coordinator Member
	workers := make([]Member, 0, len(team.Members))
	for _, member := range team.Members {
		if member.Key == team.Policy.CoordinatorMember || member.Role == RoleCoordinator {
			if coordinator.Key != "" {
				return Member{}, nil, false
			}
			coordinator = member
			continue
		}
		workers = append(workers, member)
	}
	if coordinator.Key == "" {
		return Member{}, nil, false
	}
	sort.Slice(workers, func(i, j int) bool { return workers[i].Key < workers[j].Key })
	return coordinator, workers, true
}

func (o *Orchestrator) runMembers(ctx context.Context, team Team, members []Member, prompt, runID string, events *runEventState) []MemberResult {
	if len(members) == 0 {
		return nil
	}
	if team.Policy.Mode != ModeParallelReview || team.Policy.MaxParallelMembers <= 1 {
		result := make([]MemberResult, 0, len(members))
		for _, member := range members {
			if member.TenantID == 0 {
				member.TenantID = team.TenantID
			}
			result = append(result, o.runMember(ctx, member, prompt, runID, uint64(len(result)+1), events))
		}
		return result
	}
	workers := int(team.Policy.MaxParallelMembers)
	if workers > len(members) {
		workers = len(members)
	}
	sem := make(chan struct{}, workers)
	results := make(chan indexedMemberResult, len(members))
	var wait sync.WaitGroup
	for index, member := range members {
		wait.Add(1)
		go func(index int, member Member) {
			defer wait.Done()
			if member.TenantID == 0 {
				member.TenantID = team.TenantID
			}
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				o.emitEvent(ctx, events, RunEvent{TenantID: member.TenantID, RunID: runID, EventType: RunEventMemberFailed, MemberKey: member.Key, Status: StatusCancelled, Summary: ctx.Err().Error()})
				results <- indexedMemberResult{index: index, result: MemberResult{MemberKey: member.Key, Status: StatusCancelled, Error: ctx.Err().Error()}}
				return
			}
			result := o.runMember(ctx, member, prompt, runID, uint64(index+1), events)
			<-sem
			results <- indexedMemberResult{index: index, result: result}
		}(index, member)
	}
	wait.Wait()
	close(results)
	ordered := make([]indexedMemberResult, 0, len(members))
	for item := range results {
		ordered = append(ordered, item)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].index < ordered[j].index })
	out := make([]MemberResult, 0, len(ordered))
	for _, item := range ordered {
		out = append(out, item.result)
	}
	return out
}

type indexedMemberResult struct {
	index  int
	result MemberResult
}

func (o *Orchestrator) runMember(ctx context.Context, member Member, prompt, runID string, sequence uint64, events *runEventState) MemberResult {
	o.emitEvent(ctx, events, RunEvent{TenantID: member.TenantID, RunID: runID, EventType: RunEventMemberStarted, MemberKey: member.Key, Status: StatusRunning, Summary: "Member started"})
	result, err := o.runner.Run(ctx, member, prompt)
	if result.MemberKey == "" {
		result.MemberKey = member.Key
	}
	if err != nil {
		result.Status = StatusFailed
		if result.Error == "" {
			result.Error = err.Error()
		}
	}
	if result.Status == "" {
		result.Status = StatusCompleted
	}
	if result.Status == StatusCompleted {
		o.emitEvent(ctx, events, RunEvent{TenantID: member.TenantID, RunID: runID, EventType: RunEventMemberCompleted, MemberKey: member.Key, Status: result.Status, Summary: eventSummary(result.Error, result.Content), PayloadJSON: memberResultPayload(result)})
	} else {
		o.emitEvent(ctx, events, RunEvent{TenantID: member.TenantID, RunID: runID, EventType: RunEventMemberFailed, MemberKey: member.Key, Status: result.Status, Summary: eventSummary(result.Error, result.Content), PayloadJSON: memberResultPayload(result)})
	}
	if o.mailbox != nil {
		content := result.Content
		if result.Error != "" {
			content = result.Error
		}
		message := Message{TenantID: member.TenantID, RunID: runID, FromMember: member.Key, ToMember: "coordinator", Kind: "member_result", Sequence: sequence, IdempotencyKey: runID + ":" + member.Key, Content: content, EvidenceRef: strings.Join(result.Evidence, ","), Status: "pending"}
		mailboxStatus := StatusCompleted
		mailboxSummary := eventSummary(result.Error, content)
		if err := o.mailbox.Append(ctx, message); err != nil {
			mailboxStatus = StatusFailed
			mailboxSummary = eventSummary(err.Error(), "mailbox append failed")
		}
		o.emitEvent(ctx, events, RunEvent{TenantID: member.TenantID, RunID: runID, EventType: RunEventMailboxMessage, MemberKey: member.Key, FromMember: member.Key, ToMember: "coordinator", Status: mailboxStatus, Summary: mailboxSummary, PayloadJSON: memberResultPayload(result), ArtifactRef: strings.Join(result.Evidence, ",")})
	}
	return result
}

func (o *Orchestrator) emitEvent(ctx context.Context, state *runEventState, event RunEvent) {
	if o == nil || o.events == nil || state == nil {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	state.sequence++
	event.Sequence = state.sequence
	if event.CreatedAt.IsZero() {
		event.CreatedAt = o.now()
	}
	// Sequence assignment and publication are one ordered operation. In parallel
	// teams the next producer must not publish before this sequence is visible.
	_ = o.events.Append(ctx, event)
}

func memberResultPayload(result MemberResult) string {
	payload, err := json.Marshal(map[string]any{"member_key": result.MemberKey, "status": result.Status, "content": result.Content, "evidence": result.Evidence, "tokens": result.Tokens, "turns": result.Turns, "error": result.Error})
	if err != nil {
		return ""
	}
	return string(payload)
}

func teamRunResultPayload(result TeamRunResult) string {
	payload, err := json.Marshal(result)
	if err != nil {
		return ""
	}
	return string(payload)
}

func eventSummary(preferred, fallback string) string {
	value := strings.TrimSpace(preferred)
	if value == "" {
		value = strings.TrimSpace(fallback)
	}
	if len(value) > 1024 {
		return value[:1021] + "..."
	}
	return value
}

func buildCoordinatorPrompt(prompt string, members []MemberResult) string {
	var builder strings.Builder
	builder.WriteString(prompt)
	builder.WriteString("\n\nMember evidence:\n")
	for _, member := range members {
		builder.WriteString("- ")
		builder.WriteString(member.MemberKey)
		builder.WriteString(" [")
		builder.WriteString(string(member.Status))
		builder.WriteString("]: ")
		if member.Content != "" {
			builder.WriteString(member.Content)
		} else {
			builder.WriteString(member.Error)
		}
		builder.WriteByte('\n')
	}
	return builder.String()
}
