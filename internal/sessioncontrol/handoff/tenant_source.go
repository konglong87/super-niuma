package handoff

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/sessioncontrol"
	"github.com/konglong87/super-niuma/internal/storage/mysql"
	"github.com/konglong87/super-niuma/internal/tenant"
)

const (
	tenantHandoffMessageLimit        = 32
	tenantHandoffSessionKeyMaxRunes  = 128
	tenantEventContentField          = "content"
	tenantEventResponseField         = "response"
	tenantHandoffBudgetTargetKeyRune = '<'
)

// TenantSourceStore is deliberately limited to the source records handoff can
// use. The reader immediately turns these rows into bounded facts and hashes;
// it never exposes a generic transcript read API.
type TenantSourceStore interface {
	ResolveContextOnce(context.Context) (context.Context, tenant.Context, error)
	GetSessionByKey(context.Context, sessioncontrol.RequestContext, string) (mysql.Session, error)
	ListRecentMessages(context.Context, sessioncontrol.RequestContext, uint64, int) ([]mysql.Message, error)
	ListLatestAgentTasksForSessions(context.Context, sessioncontrol.RequestContext, []uint64) ([]mysql.AgentTask, error)
	ListAgentTaskEventsForTasksComplete(context.Context, sessioncontrol.RequestContext, []uint64) ([]mysql.AgentTaskEvent, error)
	GetMessage(context.Context, sessioncontrol.RequestContext, uint64, uint64) (mysql.Message, error)
	GetTaskEvent(context.Context, sessioncontrol.RequestContext, uint64, uint64) (mysql.AgentTaskEvent, error)
	GetToolTraceRecord(context.Context, sessioncontrol.RequestContext, uint64, uint64, int) (mysql.AgentTaskEvent, error)
}

type EventStore interface {
	ListAgentTaskEventsForTasksComplete(context.Context, sessioncontrol.RequestContext, []uint64) ([]mysql.AgentTaskEvent, error)
	GetTaskEvent(context.Context, sessioncontrol.RequestContext, uint64, uint64) (mysql.AgentTaskEvent, error)
}

type TenantSourceReader struct {
	store      TenantSourceStore
	eventStore EventStore
}

var _ TenantSourceStore = (*sessioncontrol.TenantManagedStore)(nil)

func NewTenantSourceReader(store TenantSourceStore) *TenantSourceReader {
	return &TenantSourceReader{store: store}
}

func (r *TenantSourceReader) SetEventStore(store EventStore) {
	if r != nil {
		r.eventStore = store
	}
}

// ResolvedEvidence is bounded, structured evidence suitable for a later
// explicit expansion. It is never a transcript or a tool-result payload.
type ResolvedEvidence struct {
	Ref          string       `json:"ref"`
	Claim        string       `json:"claim"`
	Verification Verification `json:"verification"`
	SHA256       string       `json:"sha256"`
}

func (r *TenantSourceReader) Capture(ctx context.Context, request sessioncontrol.RequestContext, ref sessioncontrol.SessionRef) (SourceSnapshot, error) {
	if err := validateReaderRequest(request, ref, sessioncontrol.SourceTenant); err != nil {
		return SourceSnapshot{}, err
	}
	if r == nil || r.store == nil {
		return SourceSnapshot{}, &Error{Code: CodeInvalidPackage, Message: "tenant source store is required"}
	}
	bound, err := r.authorize(ctx, request)
	if err != nil {
		return SourceSnapshot{}, err
	}
	return r.captureAuthorized(bound, request, ref)
}

func (r *TenantSourceReader) Resolve(ctx context.Context, request sessioncontrol.RequestContext, ref sessioncontrol.SessionRef, locator, expectedSHA256 string) (ResolvedEvidence, error) {
	if err := validateReaderRequest(request, ref, sessioncontrol.SourceTenant); err != nil {
		return ResolvedEvidence{}, err
	}
	if r == nil || r.store == nil {
		return ResolvedEvidence{}, &Error{Code: CodeInvalidPackage, Message: "tenant source store is required"}
	}
	bound, err := r.authorize(ctx, request)
	if err != nil {
		return ResolvedEvidence{}, err
	}
	storedSession, err := r.store.GetSessionByKey(bound, request, ref.Key)
	if err != nil {
		return ResolvedEvidence{}, normalizeTenantSourceError(err)
	}
	if storedSession.ID == 0 || storedSession.SessionKey != ref.Key {
		return ResolvedEvidence{}, tenantEvidenceNotFound()
	}
	kind, recordID, ordinal, err := parseTenantEvidenceLocator(ref, locator)
	if err != nil {
		return ResolvedEvidence{}, err
	}
	var evidence Evidence
	var legacySHA256 string
	switch kind {
	case CursorPrefixMessage:
		message, readErr := r.store.GetMessage(bound, request, storedSession.ID, recordID)
		if readErr != nil || message.ID != recordID || message.SessionID != 0 && message.SessionID != storedSession.ID {
			return ResolvedEvidence{}, normalizeExactTenantError(readErr)
		}
		evidence = evidenceForTenantMessage(locator, message)
	case CursorPrefixTaskEvent:
		var eventReader EventStore = r.store
		if r.eventStore != nil {
			eventReader = r.eventStore
		}
		event, readErr := eventReader.GetTaskEvent(bound, request, storedSession.ID, recordID)
		if readErr != nil || event.ID != recordID {
			return ResolvedEvidence{}, normalizeExactTenantError(readErr)
		}
		evidence = evidenceForTenantEvent(locator, event)
		// Older conversation evidence authenticated metadata only. Preserve its
		// original readback contract without extending this fallback to verified evidence.
		if event.EventType == agenttasks.EventMessage || event.EventType == agenttasks.EventCompleted {
			legacySHA256 = tenantEventRecordHash(event, false)
		}
	case CursorPrefixToolTrace:
		event, readErr := r.store.GetToolTraceRecord(bound, request, storedSession.ID, recordID, ordinal)
		if readErr != nil || event.ID != recordID || event.EventType != agenttasks.EventToolResult {
			return ResolvedEvidence{}, normalizeExactTenantError(readErr)
		}
		evidence = evidenceForTenantEvent(locator, event)
	default:
		return ResolvedEvidence{}, tenantEvidenceNotFound()
	}
	if evidence.SHA256 != expectedSHA256 {
		if legacySHA256 == "" || expectedSHA256 != legacySHA256 {
			return ResolvedEvidence{}, &Error{Code: CodeInvalidHash, Message: "evidence hash does not match its current source record"}
		}
		evidence.SHA256 = expectedSHA256
	}
	return ResolvedEvidence{Ref: evidence.Ref, Claim: evidence.Claim, Verification: evidence.Verification, SHA256: evidence.SHA256}, nil
}

func (r *TenantSourceReader) authorize(ctx context.Context, request sessioncontrol.RequestContext) (context.Context, error) {
	bound, identity, err := r.store.ResolveContextOnce(ctx)
	if err != nil {
		return ctx, normalizeTenantSourceError(err)
	}
	if identity.TenantID != request.TenantID || identity.UserID != request.UserID {
		return ctx, &sessioncontrol.ServiceError{Code: sessioncontrol.CodeForbidden, Message: "handoff source is outside the authenticated tenant scope"}
	}
	return bound, nil
}

func (r *TenantSourceReader) captureAuthorized(ctx context.Context, request sessioncontrol.RequestContext, ref sessioncontrol.SessionRef) (SourceSnapshot, error) {
	storedSession, err := r.store.GetSessionByKey(ctx, request, ref.Key)
	if err != nil {
		return SourceSnapshot{}, normalizeTenantSourceError(err)
	}
	if storedSession.ID == 0 || storedSession.SessionKey != ref.Key {
		return SourceSnapshot{}, &sessioncontrol.ServiceError{Code: sessioncontrol.CodeNotFound, Message: "handoff source session was not found"}
	}
	messages, err := r.store.ListRecentMessages(ctx, request, storedSession.ID, tenantHandoffMessageLimit)
	if err != nil {
		return SourceSnapshot{}, normalizeTenantSourceError(err)
	}
	tasks, err := r.store.ListLatestAgentTasksForSessions(ctx, request, []uint64{storedSession.ID})
	if err != nil {
		return SourceSnapshot{}, normalizeTenantSourceError(err)
	}
	tasks = ownedTasks(tasks, storedSession.ID)
	taskIDs := make([]uint64, 0, len(tasks))
	for _, task := range tasks {
		taskIDs = append(taskIDs, task.ID)
	}
	var eventReader EventStore = r.store
	if r.eventStore != nil {
		eventReader = r.eventStore
	}
	events, err := eventReader.ListAgentTaskEventsForTasksComplete(ctx, request, taskIDs)
	if err != nil {
		return SourceSnapshot{}, normalizeTenantSourceError(err)
	}
	snapshot := tenantSnapshot(ref, storedSession, messages, tasks, ownedEvents(events, taskIDs))
	if snapshot.Source.Cursor == "" {
		return SourceSnapshot{}, &Error{Code: CodeUnsupportedSource, Message: "tenant source has no selected message or task event cursor"}
	}
	return snapshot, nil
}

func tenantSnapshot(ref sessioncontrol.SessionRef, storedSession mysql.Session, messages []mysql.Message, tasks []mysql.AgentTask, events []mysql.AgentTaskEvent) SourceSnapshot {
	snapshot := SourceSnapshot{
		Source:     Source{Ref: ref.String()},
		Objectives: []Candidate{{Locator: tenantLocator(ref, "session", storedSession.ID), Text: storedSession.Title, Precedence: PrecedenceTaskDescription}},
		Budget:     Budget{LimitTokens: DefaultPackageTokenLimit},
	}
	selected := make([]typedCursor, 0, len(messages)+len(events))
	messageEvidence := make([]tenantHandoffEvidenceCandidate, 0, len(messages))
	for _, message := range messages {
		if message.SessionID != 0 && message.SessionID != storedSession.ID {
			continue
		}
		locator := tenantLocator(ref, "message", message.ID)
		if strings.EqualFold(strings.TrimSpace(message.Role), "user") {
			snapshot.Objectives = append(snapshot.Objectives, Candidate{Locator: locator, Text: message.Content, Precedence: PrecedenceUserInstruction})
		}
		messageEvidence = append(messageEvidence, tenantHandoffEvidenceCandidate{id: message.ID, evidence: evidenceForTenantMessage(locator, message)})
		selected = append(selected, typedCursor{kind: CursorPrefixMessage, id: message.ID})
	}
	mainWebTasks := make(map[uint64]bool, len(tasks))
	for _, task := range tasks {
		locator := tenantLocator(ref, "task", task.ID)
		snapshot.Objectives = append(snapshot.Objectives, Candidate{Locator: locator, Text: task.Description, Precedence: PrecedenceTaskDescription})
		snapshot.Stages = append(snapshot.Stages, Candidate{Locator: locator, Text: task.Status, Precedence: PrecedenceStructuredStatus})
		mainWebTasks[task.ID] = task.ParentSessionID == storedSession.ID && task.AgentName == agenttasks.AgentNameWeb && (task.SubagentSessionKey == "" || task.SubagentSessionKey == storedSession.SessionKey)
	}
	for _, event := range events {
		selected = append(selected, typedCursor{kind: CursorPrefixTaskEvent, id: event.ID})
	}
	snapshot.Source.Cursor = greatestCursor(selected)
	anchors, verifiedEvents := tenantHandoffEventCandidates(events, mainWebTasks)
	for _, event := range anchors {
		appendTenantHandoffEvent(&snapshot, ref, event, mainWebTasks[event.TaskID])
	}
	selectTenantHandoffEvidence(&snapshot, messageEvidence, verifiedEvents, ref)
	return snapshot
}

// tenantHandoffEventCandidates separates transport progress from durable facts.
// The latest Web request and answer are mandatory context anchors. Other event
// types are eligible only when their source type carries an explicit verifier.
func tenantHandoffEventCandidates(events []mysql.AgentTaskEvent, mainWebTasks map[uint64]bool) (anchors, verified []mysql.AgentTaskEvent) {
	ordered := append([]mysql.AgentTaskEvent(nil), events...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].ID > ordered[j].ID })
	selectedIDs := make(map[uint64]struct{}, 2)
	for _, eventType := range []string{agenttasks.EventMessage, agenttasks.EventCompleted} {
		for _, event := range ordered {
			if mainWebTasks[event.TaskID] && event.EventType == eventType {
				anchors = append(anchors, event)
				selectedIDs[event.ID] = struct{}{}
				break
			}
		}
	}
	for _, event := range ordered {
		if _, selected := selectedIDs[event.ID]; selected {
			continue
		}
		verification, _ := tenantEventVerification(event.EventType)
		if verification == VerificationVerified {
			verified = append(verified, event)
		}
	}
	return anchors, verified
}

func appendTenantHandoffEvent(snapshot *SourceSnapshot, ref sessioncontrol.SessionRef, event mysql.AgentTaskEvent, mainWebTask bool) {
	locator := tenantEventLocator(ref, event)
	snapshot.Evidence = append(snapshot.Evidence, evidenceForTenantEvent(locator, event))
	snapshot.Facts = append(snapshot.Facts, factsForTenantEvent(locator, event, mainWebTask)...)
	if mainWebTask && event.EventType == agenttasks.EventMessage {
		if content := eventSafeString(event.PayloadJSON, tenantEventContentField); content != "" {
			// Web task descriptions are conversation titles. The latest bounded
			// user request is the objective; it remains untrusted source context.
			snapshot.Objectives = []Candidate{{Locator: locator, Text: content, Precedence: PrecedenceUserInstruction}}
		}
	}
}

// selectTenantHandoffEvidence balances recent user history with verified
// runtime facts. Evidence is protected by the package budget planner, so it is
// admitted only after measuring the canonical package that would result.
type tenantHandoffEvidenceCandidate struct {
	id       uint64
	evidence Evidence
}

func selectTenantHandoffEvidence(snapshot *SourceSnapshot, messages []tenantHandoffEvidenceCandidate, verifiedEvents []mysql.AgentTaskEvent, ref sessioncontrol.SessionRef) {
	sort.SliceStable(messages, func(i, j int) bool { return messages[i].id > messages[j].id })
	for index := 0; index < len(messages) || index < len(verifiedEvents); index++ {
		if index < len(messages) && !appendTenantEvidenceWithinBudget(snapshot, messages[index].evidence) {
			return
		}
		if index < len(verifiedEvents) {
			evidence := evidenceForTenantEvent(tenantEventLocator(ref, verifiedEvents[index]), verifiedEvents[index])
			if !appendTenantEvidenceWithinBudget(snapshot, evidence) {
				return
			}
		}
	}
}

func appendTenantEvidenceWithinBudget(snapshot *SourceSnapshot, evidence Evidence) bool {
	candidate := *snapshot
	candidate.Evidence = append(append([]Evidence(nil), snapshot.Evidence...), evidence)
	// tenant_sessions.session_key is VARCHAR(128). encoding/json expands '<' to
	// six ASCII bytes, making this target at least as expensive to estimate as
	// every database-valid managed target key of the same character length.
	candidate.Target = Target{Ref: SourceKindTenant + ":" + strings.Repeat(string(tenantHandoffBudgetTargetKeyRune), tenantHandoffSessionKeyMaxRunes)}
	pkg, err := Extract(candidate)
	if err != nil || pkg.Budget.EstimatedTokens > candidate.Budget.LimitTokens {
		return false
	}
	snapshot.Evidence = candidate.Evidence
	return true
}

func tenantEventLocator(ref sessioncontrol.SessionRef, event mysql.AgentTaskEvent) string {
	if event.EventType == agenttasks.EventToolResult {
		return ref.String() + "#" + CursorPrefixToolTrace + ":" + strconv.FormatUint(event.ID, 10) + ":0"
	}
	return tenantLocator(ref, CursorPrefixTaskEvent, event.ID)
}

func evidenceForTenantMessage(locator string, message mysql.Message) Evidence {
	return Evidence{Ref: locator, Claim: "user instruction recorded", Verification: VerificationReported, SHA256: canonicalRecordHash(struct {
		ID      uint64 `json:"id"`
		Role    string `json:"role"`
		Content string `json:"content"`
		Turn    uint   `json:"turn"`
	}{message.ID, message.Role, message.Content, message.TurnIndex})}
}

func evidenceForTenantEvent(locator string, event mysql.AgentTaskEvent) Evidence {
	verification, verifier := tenantEventVerification(event.EventType)
	return Evidence{Ref: locator, Claim: tenantEventClaim(event), Verification: verification, SHA256: tenantEventRecordHash(event, true), Verifier: verifierForEvent(locator, verification, verifier, event)}
}

func tenantEventRecordHash(event mysql.AgentTaskEvent, includeConversation bool) string {
	return canonicalRecordHash(struct {
		ID      uint64 `json:"id"`
		TaskID  uint64 `json:"task_id"`
		Type    string `json:"type"`
		Payload string `json:"payload"`
	}{event.ID, event.TaskID, event.EventType, canonicalEventPayload(event, includeConversation)})
}

func verifierForEvent(locator string, verification Verification, kind VerifierKind, event mysql.AgentTaskEvent) Verifier {
	if verification != VerificationVerified {
		return Verifier{}
	}
	return Verifier{Kind: kind, Ref: locator, SHA256: canonicalRecordHash(struct {
		ID   uint64 `json:"id"`
		Type string `json:"type"`
	}{event.ID, event.EventType})}
}

func tenantEventVerification(eventType string) (Verification, VerifierKind) {
	switch strings.TrimSpace(eventType) {
	case agenttasks.EventToolResult:
		return VerificationVerified, VerifierToolTrace
	case agenttasks.EventFileChange:
		return VerificationVerified, VerifierFileChange
	default:
		if strings.Contains(strings.ToLower(eventType), "capability") {
			return VerificationVerified, VerifierCapabilityLoop
		}
		return VerificationReported, ""
	}
}

func tenantEventClaim(event mysql.AgentTaskEvent) string {
	if name := eventSafeString(event.PayloadJSON, "tool_name", "name", "action"); name != "" {
		return capText(event.EventType + ": " + name)
	}
	return capText(event.EventType + " recorded")
}

func factsForTenantEvent(locator string, event mysql.AgentTaskEvent, mainWebTask bool) []FactCandidate {
	var facts []FactCandidate
	if mainWebTask && event.EventType == agenttasks.EventCompleted {
		if response := eventSafeString(event.PayloadJSON, tenantEventResponseField); response != "" {
			facts = append(facts, FactCandidate{Locator: locator, Text: response, Kind: FactCompleted, Precedence: PrecedenceStructuredFact})
		}
	}
	return facts
}

func canonicalEventPayload(event mysql.AgentTaskEvent, includeConversation bool) string {
	var values map[string]any
	if json.Unmarshal([]byte(event.PayloadJSON), &values) != nil {
		return ""
	}
	allowed := map[string]string{}
	for _, key := range []string{"tool_name", "name", "action", "status", "success", "path", "kind"} {
		if value, ok := values[key]; ok {
			allowed[key] = fmt.Sprint(value)
		}
	}
	// Hash the full selected field, including bytes beyond the displayed cap, so
	// evidence readback detects source changes without persisting a transcript.
	var contentField string
	switch event.EventType {
	case agenttasks.EventMessage:
		contentField = tenantEventContentField
	case agenttasks.EventCompleted:
		contentField = tenantEventResponseField
	}
	if includeConversation && contentField != "" {
		if content, ok := values[contentField].(string); ok {
			allowed[contentField] = content
		}
	}
	encoded, _ := json.Marshal(allowed)
	return string(encoded)
}

func eventSafeString(payload string, keys ...string) string {
	var values map[string]any
	if json.Unmarshal([]byte(payload), &values) != nil {
		return ""
	}
	for _, key := range keys {
		if value, ok := values[key].(string); ok {
			return capText(value)
		}
	}
	return ""
}

func tenantLocator(ref sessioncontrol.SessionRef, kind string, id uint64) string {
	return ref.String() + "#" + kind + ":" + strconv.FormatUint(id, 10)
}

func parseTenantEvidenceLocator(ref sessioncontrol.SessionRef, locator string) (string, uint64, int, error) {
	if !validEvidenceRef(ref.String(), locator) {
		return "", 0, 0, tenantEvidenceNotFound()
	}
	source, typed, ok := strings.Cut(locator, "#")
	if !ok || source != ref.String() {
		return "", 0, 0, tenantEvidenceNotFound()
	}
	kind, value, ok := strings.Cut(typed, ":")
	if !ok {
		return "", 0, 0, tenantEvidenceNotFound()
	}
	if kind == CursorPrefixToolTrace {
		eventValue, ordinalValue, found := strings.Cut(value, ":")
		if !found {
			return "", 0, 0, tenantEvidenceNotFound()
		}
		eventID, parseErr := strconv.ParseUint(eventValue, 10, 64)
		ordinal, ordinalErr := strconv.Atoi(ordinalValue)
		if parseErr != nil || ordinalErr != nil || eventID == 0 || ordinal < 0 {
			return "", 0, 0, tenantEvidenceNotFound()
		}
		return kind, eventID, ordinal, nil
	}
	if kind != CursorPrefixMessage && kind != CursorPrefixTaskEvent {
		return "", 0, 0, tenantEvidenceNotFound()
	}
	recordID, parseErr := strconv.ParseUint(value, 10, 64)
	if parseErr != nil || recordID == 0 {
		return "", 0, 0, tenantEvidenceNotFound()
	}
	return kind, recordID, 0, nil
}

func normalizeExactTenantError(err error) error {
	if err == nil {
		return tenantEvidenceNotFound()
	}
	normalized := normalizeTenantSourceError(err)
	var serviceErr *sessioncontrol.ServiceError
	if errors.As(normalized, &serviceErr) && serviceErr.Code == sessioncontrol.CodeForbidden {
		return serviceErr
	}
	return tenantEvidenceNotFound()
}

func tenantEvidenceNotFound() error {
	return &sessioncontrol.ServiceError{Code: sessioncontrol.CodeNotFound, Message: "handoff evidence was not found in the authorized source"}
}

type typedCursor struct {
	kind string
	id   uint64
}

func greatestCursor(values []typedCursor) string {
	sort.Slice(values, func(i, j int) bool {
		if values[i].id != values[j].id {
			return values[i].id > values[j].id
		}
		return values[i].kind > values[j].kind
	})
	if len(values) == 0 {
		return ""
	}
	return values[0].kind + ":" + strconv.FormatUint(values[0].id, 10)
}

func ownedTasks(tasks []mysql.AgentTask, sessionID uint64) []mysql.AgentTask {
	out := make([]mysql.AgentTask, 0, len(tasks))
	for _, task := range tasks {
		if task.ID != 0 && task.ParentSessionID == sessionID {
			out = append(out, task)
		}
	}
	return out
}

func ownedEvents(events []mysql.AgentTaskEvent, taskIDs []uint64) []mysql.AgentTaskEvent {
	allowed := make(map[uint64]struct{}, len(taskIDs))
	for _, id := range taskIDs {
		allowed[id] = struct{}{}
	}
	out := make([]mysql.AgentTaskEvent, 0, len(events))
	for _, event := range events {
		if _, ok := allowed[event.TaskID]; ok {
			out = append(out, event)
		}
	}
	return out
}

func canonicalRecordHash(value any) string {
	payload, _ := json.Marshal(value)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func validateReaderRequest(request sessioncontrol.RequestContext, ref sessioncontrol.SessionRef, source sessioncontrol.Source) error {
	if request.TenantID == 0 || request.UserID == 0 || request.ActorUserID == 0 {
		return &sessioncontrol.ServiceError{Code: sessioncontrol.CodeInvalidState, Message: "tenant, user, and actor user IDs are required"}
	}
	parsed, err := sessioncontrol.ParseRef(ref.String())
	if err != nil || parsed != ref {
		return &sessioncontrol.ServiceError{Code: sessioncontrol.CodeInvalidState, Message: "handoff source ref must be canonical"}
	}
	if ref.Source != source {
		return &sessioncontrol.ServiceError{Code: sessioncontrol.CodeForbidden, Message: "handoff source kind is not authorized for this reader"}
	}
	return nil
}

func normalizeTenantSourceError(err error) error {
	if err == nil {
		return nil
	}
	var serviceErr *sessioncontrol.ServiceError
	if errors.As(err, &serviceErr) {
		return serviceErr
	}
	if errors.Is(err, mysql.ErrNotFound) {
		return &sessioncontrol.ServiceError{Code: sessioncontrol.CodeNotFound, Message: "handoff source record was not found", Cause: err}
	}
	return err
}
