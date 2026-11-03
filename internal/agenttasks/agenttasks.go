package agenttasks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/konglong87/super-niuma/internal/compact/tokenestimate"
)

const (
	SourcePendingInputSideChat = "pending-input-side-chat"

	AgentNameWeb    = "web-agent"
	StatusReady     = "ready"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
	StatusTimeout   = "timeout"
)

const (
	SessionHandoffEventSchema   = "niuma.session-handoff-event.v1"
	SessionHandoffPackageSchema = "niuma.session-handoff.v1"
	SessionHandoffRelationType  = "handoff"
	SessionHandoffLinkStatus    = "active"
	maxSessionHandoffEventBytes = 64 * 1024
	maxSessionHandoffTextBytes  = 512
)

var ErrInvalidSessionHandoffEvent = errors.New("agent task: invalid session handoff event")

const (
	UserQuestionAnswered  = "answered"
	UserQuestionCancelled = "cancelled"
	UserQuestionExpired   = "expired"
)

const (
	EventStarted              = "started"
	EventTurnStart            = "turn_start"
	EventTextDelta            = "text_delta"
	EventThinking             = "thinking_delta"
	EventToolCall             = "tool_call"
	EventToolResult           = "tool_result"
	EventMessageStop          = "message_stop"
	EventCompleted            = "completed"
	EventFailed               = "failed"
	EventCancelled            = "cancelled"
	EventTimeout              = "timeout"
	EventMessage              = "message"
	EventCacheState           = "cache_state"
	EventUsage                = "usage"
	EventCompactSummary       = "compact_summary"
	EventPermissionRequest    = "permission_request"
	EventPermissionResolved   = "permission_resolved"
	EventUserQuestionRequest  = "user_question_request"
	EventUserQuestionResolved = "user_question_resolved"
	EventNestedProgress       = "nested_agent_progress"
	// EventFileChange reports one file the run read or wrote. Writes are derived
	// from the tools.FileChange records a tool already produces; reads are
	// derived from the path the tool was invoked with. Consumers must not infer
	// file activity from other event payloads.
	EventFileChange = "file_change"
	// EventNextSteps carries the follow-up prompt suggestions a finished turn
	// produced, for the composer to offer as one-click candidates. It is a UI
	// signal, not conversation content: consumers must not render it as a message.
	EventNextSteps      = "next_steps"
	EventInputQueued    = "input_queued"
	EventInputReordered = "input_reordered"
	EventInputUpdated   = "input_updated"
	EventInputRunning   = "input_running"
	EventInputSent      = "input_sent"
	EventInputFailed    = "input_failed"
	EventInputCancelled = "input_cancelled"
	EventInputRetried   = "input_retried"
	// EventImageArtifact records a persisted image result without embedding
	// provider bytes or credentials in the task event stream.
	EventImageArtifact = "image_artifact"
	// EventSessionHandoff attaches a validated, bounded handoff package to the
	// ready task that will consume it.
	EventSessionHandoff = "session_handoff"
	// EventSessionControlStop is the durable replay anchor written in the same
	// transaction as a Session Control cancellation.
	EventSessionControlStop = "session_control_stop"
)

type MessageInput struct {
	TenantID    uint64       `json:"tenant_id,omitempty"`
	UserID      uint64       `json:"user_id,omitempty"`
	TaskID      uint64       `json:"task_id"`
	FromAgent   string       `json:"from_agent,omitempty"`
	Content     string       `json:"content"`
	TraceID     string       `json:"trace_id,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
	DeliveredAt string       `json:"delivered_at,omitempty"`
}

// Attachment carries user-provided multimodal input through the agent runner.
// InlineData is intentionally excluded from persisted events; it is only held
// in memory until query builds the provider content block.
type Attachment struct {
	AttachmentID string `json:"attachment_id,omitempty"`
	Type         string `json:"type"`
	MediaType    string `json:"media_type,omitempty"`
	Name         string `json:"name,omitempty"`
	URL          string `json:"url,omitempty"`
	SizeBytes    int64  `json:"size_bytes,omitempty"`
	SHA256       string `json:"sha256,omitempty"`
	InlineData   string `json:"-"`
}

type TaskInput struct {
	TenantID           uint64
	UserID             uint64
	ParentSessionID    uint64
	SubagentSessionKey string
	AgentName          string
	Description        string
	Prompt             string
	Status             string
	Model              string
	ResultJSON         string
	MetadataJSON       string
	TraceID            string
	IdempotencyKey     string
}

type TaskUpdate struct {
	TenantID     uint64
	UserID       uint64
	Status       string
	ResultJSON   string
	MetadataJSON string
}

type Task struct {
	ID                 uint64    `json:"id"`
	TenantID           uint64    `json:"tenant_id,omitempty"`
	UserID             uint64    `json:"user_id,omitempty"`
	ParentSessionID    uint64    `json:"parent_session_id,omitempty"`
	SubagentSessionKey string    `json:"subagent_session_key,omitempty"`
	AgentName          string    `json:"agent_name,omitempty"`
	Description        string    `json:"description,omitempty"`
	Status             string    `json:"status"`
	Model              string    `json:"model,omitempty"`
	ResultJSON         string    `json:"result_json,omitempty"`
	MetadataJSON       string    `json:"metadata_json,omitempty"`
	TraceID            string    `json:"trace_id,omitempty"`
	StartedAt          time.Time `json:"started_at"`
	FinishedAt         time.Time `json:"finished_at,omitempty"`
}

type EventInput struct {
	TenantID    uint64
	UserID      uint64
	TaskID      uint64
	EventType   string
	PayloadJSON string
	TraceID     string
}

// HandoffLinkAndEventInput is one compound persistence command. Repositories
// must write the link and event atomically after checking target task scope.
type HandoffLinkAndEventInput struct {
	TenantID         uint64
	UserID           uint64
	TargetSessionID  uint64
	TargetTaskID     uint64
	SourceKind       string
	SourceSessionKey string
	SourceSessionID  uint64
	RelationType     string
	LinkStatus       string
	LinkMetadataJSON string
	CreatedByUserID  uint64
	PayloadJSON      string
	// HandoffIdentity is retained for source compatibility only. Durable
	// identity is always derived from DecodeSessionHandoffEvent.
	HandoffIdentity string
	TraceID         string
}

type HandoffLinkAndEventResult struct {
	LinkID   uint64
	EventID  uint64
	Replayed bool
}

type HandoffBatchInput struct {
	TenantID          uint64
	UserID            uint64
	TargetSessionID   uint64
	TargetTaskID      uint64
	OperationIdentity string
	CreatedByUserID   uint64
	TraceID           string
	Items             []HandoffLinkAndEventInput
}

type HandoffBatchResult struct {
	Items    []HandoffLinkAndEventResult
	Replayed bool
}

type HandoffRecoveryInput struct {
	TenantID          uint64
	UserID            uint64
	TargetSessionID   uint64
	TargetTaskID      uint64
	OperationIdentity string
	ExpectedSources   []string
}

type HandoffRecoveredItem struct {
	LinkID          uint64
	EventID         uint64
	SourceRef       string
	PackageID       string
	PackageSHA256   string
	SourceCursor    string
	EstimatedTokens int
}

type HandoffRecoveryResult struct {
	Found                 bool
	OperationMetadataJSON string
	Items                 []HandoffRecoveredItem
}

// DecodedSessionHandoffEvent exposes only validated lineage fields needed by
// persistence. PackageJSON is retained for immutable storage after the strict
// package decoder has rejected unknown fields and recomputed both hashes.
type DecodedSessionHandoffEvent struct {
	Schema                string
	PackageID             string
	PackageSHA256         string
	TargetTaskID          uint64
	RefreshOfPackageID    string
	RefreshOfEventID      uint64
	OperationIdentity     string
	OperationMetadataJSON string
	SourceRef             string
	SourceCursor          string
	TargetRef             string
	EstimatedTokens       int
	PackageJSON           json.RawMessage
}

type sessionHandoffEventWire struct {
	Schema             string          `json:"schema"`
	PackageID          string          `json:"package_id"`
	PackageSHA256      string          `json:"package_sha256"`
	TargetTaskID       uint64          `json:"target_task_id"`
	RefreshOfPackageID string          `json:"refresh_of_package_id,omitempty"`
	RefreshOfEventID   uint64          `json:"refresh_of_event_id,omitempty"`
	OperationIdentity  string          `json:"operation_identity,omitempty"`
	OperationMetadata  json.RawMessage `json:"operation_metadata,omitempty"`
	Package            json.RawMessage `json:"package"`
}

type sessionControlOperationMetadata struct {
	Schema      string `json:"schema"`
	Operation   string `json:"operation"`
	OperationID string `json:"operation_id"`
	KeyHash     string `json:"key_hash"`
	Fingerprint string `json:"fingerprint"`
}

type sessionHandoffSource struct {
	Ref           string    `json:"ref"`
	Cursor        string    `json:"cursor"`
	ContentSHA256 string    `json:"content_sha256"`
	CapturedAt    time.Time `json:"captured_at"`
}

type sessionHandoffTarget struct {
	Ref string `json:"ref"`
}

type sessionHandoffVerifier struct {
	Kind   string `json:"kind"`
	Ref    string `json:"ref"`
	SHA256 string `json:"sha256"`
}

type sessionHandoffEvidence struct {
	Ref          string                 `json:"ref"`
	Claim        string                 `json:"claim"`
	Verification string                 `json:"verification"`
	SHA256       string                 `json:"sha256"`
	Verifier     sessionHandoffVerifier `json:"verifier"`
}

type sessionHandoffBudget struct {
	EstimatedTokens int `json:"estimated_tokens"`
	LimitTokens     int `json:"limit_tokens"`
}

type sessionHandoffPackage struct {
	Schema        string                   `json:"schema"`
	PackageID     string                   `json:"package_id"`
	PackageSHA256 string                   `json:"package_sha256"`
	Source        sessionHandoffSource     `json:"source"`
	Target        sessionHandoffTarget     `json:"target"`
	Objective     string                   `json:"objective"`
	Constraints   []string                 `json:"constraints"`
	StageSummary  string                   `json:"stage_summary"`
	Completed     []string                 `json:"completed"`
	OpenItems     []string                 `json:"open_items"`
	Risks         []string                 `json:"risks"`
	NextActions   []string                 `json:"next_actions"`
	Evidence      []sessionHandoffEvidence `json:"evidence"`
	Budget        sessionHandoffBudget     `json:"budget"`
}

type sessionHandoffCanonicalSource struct {
	Ref           string `json:"ref"`
	Cursor        string `json:"cursor"`
	ContentSHA256 string `json:"content_sha256,omitempty"`
}

type sessionHandoffCanonicalEvidence struct {
	Ref          string                 `json:"ref"`
	Verification string                 `json:"verification"`
	SHA256       string                 `json:"sha256"`
	Verifier     sessionHandoffVerifier `json:"verifier"`
}

// EncodeSessionHandoffEvent accepts serialized package input only at the
// codec boundary, validates it completely, then emits the canonical envelope.
func EncodeSessionHandoffEvent(targetTaskID uint64, packageJSON string) (string, error) {
	return EncodeSessionHandoffEventWithRefresh(targetTaskID, packageJSON, "", 0)
}

// EncodeSessionHandoffEventWithRefresh appends optional immutable lineage to a
// newly captured package. It preserves the v1 schema and rejects incomplete or
// malformed ancestry rather than accepting arbitrary event metadata.
func EncodeSessionHandoffEventWithRefresh(targetTaskID uint64, packageJSON, refreshOfPackageID string, refreshOfEventID uint64) (string, error) {
	return EncodeSessionHandoffEventForOperation(targetTaskID, packageJSON, refreshOfPackageID, refreshOfEventID, "")
}

func EncodeSessionHandoffEventForOperation(targetTaskID uint64, packageJSON, refreshOfPackageID string, refreshOfEventID uint64, operationIdentity string) (string, error) {
	return EncodeSessionHandoffEventWithOperationMetadata(targetTaskID, packageJSON, refreshOfPackageID, refreshOfEventID, operationIdentity, "")
}

func EncodeSessionHandoffEventWithOperationMetadata(targetTaskID uint64, packageJSON, refreshOfPackageID string, refreshOfEventID uint64, operationIdentity, operationMetadataJSON string) (string, error) {
	pkg, err := decodeSessionHandoffPackage(packageJSON)
	if err != nil {
		return "", err
	}
	if !validSessionHandoffRefreshLineage(pkg.PackageID, refreshOfPackageID, refreshOfEventID) {
		return "", invalidSessionHandoffEvent("refresh lineage is invalid")
	}
	if operationIdentity != "" && !validSessionHandoffSHA(operationIdentity) {
		return "", invalidSessionHandoffEvent("operation identity is invalid")
	}
	operationMetadata, err := validateSessionControlOperationMetadata(operationMetadataJSON, operationIdentity)
	if err != nil {
		return "", err
	}
	wire := sessionHandoffEventWire{
		Schema: SessionHandoffEventSchema, PackageID: pkg.PackageID, PackageSHA256: pkg.PackageSHA256,
		TargetTaskID: targetTaskID, RefreshOfPackageID: refreshOfPackageID, RefreshOfEventID: refreshOfEventID, OperationIdentity: operationIdentity, OperationMetadata: operationMetadata, Package: json.RawMessage(packageJSON),
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return "", invalidSessionHandoffEvent("encode event")
	}
	if _, err := DecodeSessionHandoffEvent(string(encoded)); err != nil {
		return "", err
	}
	return string(encoded), nil
}

func DecodeSessionHandoffEvent(payload string) (DecodedSessionHandoffEvent, error) {
	if len(payload) == 0 || len(payload) > maxSessionHandoffEventBytes {
		return DecodedSessionHandoffEvent{}, invalidSessionHandoffEvent("event size is outside the bounded contract")
	}
	var wire sessionHandoffEventWire
	if err := decodeSessionHandoffStrict(payload, &wire); err != nil {
		return DecodedSessionHandoffEvent{}, invalidSessionHandoffEvent("event JSON does not match the typed contract")
	}
	if wire.Schema != SessionHandoffEventSchema || wire.TargetTaskID == 0 || !validSessionHandoffRefreshLineage(wire.PackageID, wire.RefreshOfPackageID, wire.RefreshOfEventID) || wire.OperationIdentity != "" && !validSessionHandoffSHA(wire.OperationIdentity) {
		return DecodedSessionHandoffEvent{}, invalidSessionHandoffEvent("event schema or target task is invalid")
	}
	operationMetadata, err := validateSessionControlOperationMetadata(string(wire.OperationMetadata), wire.OperationIdentity)
	if err != nil {
		return DecodedSessionHandoffEvent{}, err
	}
	pkg, err := decodeSessionHandoffPackage(string(wire.Package))
	if err != nil {
		return DecodedSessionHandoffEvent{}, err
	}
	if wire.PackageID != pkg.PackageID || wire.PackageSHA256 != pkg.PackageSHA256 {
		return DecodedSessionHandoffEvent{}, invalidSessionHandoffEvent("event lineage does not match package identity")
	}
	return DecodedSessionHandoffEvent{
		Schema: wire.Schema, PackageID: pkg.PackageID, PackageSHA256: pkg.PackageSHA256,
		TargetTaskID: wire.TargetTaskID, RefreshOfPackageID: wire.RefreshOfPackageID, RefreshOfEventID: wire.RefreshOfEventID, OperationIdentity: wire.OperationIdentity, OperationMetadataJSON: string(operationMetadata),
		SourceRef: pkg.Source.Ref, SourceCursor: pkg.Source.Cursor, TargetRef: pkg.Target.Ref, EstimatedTokens: pkg.Budget.EstimatedTokens,
		PackageJSON: append(json.RawMessage(nil), wire.Package...),
	}, nil
}

func validateSessionControlOperationMetadata(payload, operationIdentity string) (json.RawMessage, error) {
	if strings.TrimSpace(payload) == "" {
		return nil, nil
	}
	if operationIdentity == "" {
		return nil, invalidSessionHandoffEvent("operation metadata requires an operation identity")
	}
	var metadata sessionControlOperationMetadata
	if err := decodeSessionHandoffStrict(payload, &metadata); err != nil {
		return nil, invalidSessionHandoffEvent("operation metadata is invalid")
	}
	if metadata.Schema != "niuma.session-control-operation.v1" || metadata.Operation != "attach" && metadata.Operation != "handoff_refresh" || metadata.KeyHash != operationIdentity || !validSessionHandoffSHA(metadata.OperationID) || !validSessionHandoffSHA(metadata.KeyHash) || !validSessionHandoffSHA(metadata.Fingerprint) {
		return nil, invalidSessionHandoffEvent("operation metadata identity is invalid")
	}
	expected := sessionHandoffJSONSHA256(struct {
		Schema      string `json:"schema"`
		Operation   string `json:"operation"`
		KeyHash     string `json:"key_hash"`
		Fingerprint string `json:"fingerprint"`
	}{metadata.Schema, metadata.Operation, metadata.KeyHash, metadata.Fingerprint})
	if metadata.OperationID != expected {
		return nil, invalidSessionHandoffEvent("operation metadata hash is invalid")
	}
	canonical, err := json.Marshal(metadata)
	if err != nil {
		return nil, invalidSessionHandoffEvent("operation metadata encode failed")
	}
	return canonical, nil
}

func validSessionHandoffRefreshLineage(_ string, refreshOfPackageID string, refreshOfEventID uint64) bool {
	if refreshOfPackageID == "" && refreshOfEventID == 0 {
		return true
	}
	if refreshOfPackageID == "" || refreshOfEventID == 0 || !strings.HasPrefix(refreshOfPackageID, "handoff:") {
		return false
	}
	separator := strings.LastIndex(refreshOfPackageID, ":")
	return separator > len("handoff:") && validSessionHandoffSHA(refreshOfPackageID[separator+1:])
}

func decodeSessionHandoffPackage(payload string) (sessionHandoffPackage, error) {
	var pkg sessionHandoffPackage
	if err := decodeSessionHandoffStrict(payload, &pkg); err != nil {
		return sessionHandoffPackage{}, invalidSessionHandoffEvent("package JSON does not match the typed contract")
	}
	if pkg.Schema != SessionHandoffPackageSchema || !validSessionHandoffRef(pkg.Source.Ref, true) || !validSessionHandoffRef(pkg.Target.Ref, false) || !validSessionHandoffCursor(pkg.Source.Ref, pkg.Source.Cursor) {
		return sessionHandoffPackage{}, invalidSessionHandoffEvent("package schema or session lineage is invalid")
	}
	if !validSessionHandoffSHA(pkg.Source.ContentSHA256) || !validSessionHandoffSHA(pkg.PackageSHA256) || pkg.Budget.EstimatedTokens < 0 || pkg.Budget.EstimatedTokens > 2048 || pkg.Budget.LimitTokens <= 0 || pkg.Budget.LimitTokens > 2048 {
		return sessionHandoffPackage{}, invalidSessionHandoffEvent("package hash or budget is invalid")
	}
	if !validSessionHandoffText(pkg.Objective) || !validSessionHandoffText(pkg.StageSummary) || !validSessionHandoffStrings(pkg.Constraints) || !validSessionHandoffStrings(pkg.Completed) || !validSessionHandoffStrings(pkg.OpenItems) || !validSessionHandoffStrings(pkg.Risks) || !validSessionHandoffStrings(pkg.NextActions) || !validSessionHandoffEvidence(pkg.Source.Ref, pkg.Evidence) {
		return sessionHandoffPackage{}, invalidSessionHandoffEvent("package content is not normalized and bounded")
	}
	if pkg.PackageID != "handoff:"+pkg.Source.Ref+":"+pkg.Source.Cursor+":"+pkg.Source.ContentSHA256 {
		return sessionHandoffPackage{}, invalidSessionHandoffEvent("package ID does not match source identity")
	}
	packageBody := struct {
		Schema      string                        `json:"schema"`
		PackageID   string                        `json:"package_id"`
		Source      sessionHandoffCanonicalSource `json:"source"`
		Target      sessionHandoffTarget          `json:"target"`
		Objective   string                        `json:"objective"`
		Constraints []string                      `json:"constraints"`
		Stage       string                        `json:"stage_summary"`
		Completed   []string                      `json:"completed"`
		OpenItems   []string                      `json:"open_items"`
		Risks       []string                      `json:"risks"`
		NextActions []string                      `json:"next_actions"`
		Evidence    []sessionHandoffEvidence      `json:"evidence"`
		Budget      sessionHandoffBudget          `json:"budget"`
	}{pkg.Schema, pkg.PackageID, sessionHandoffCanonicalSource{Ref: pkg.Source.Ref, Cursor: pkg.Source.Cursor, ContentSHA256: pkg.Source.ContentSHA256}, pkg.Target, pkg.Objective, pkg.Constraints, pkg.StageSummary, pkg.Completed, pkg.OpenItems, pkg.Risks, pkg.NextActions, pkg.Evidence, pkg.Budget}
	packageBodyJSON, err := json.Marshal(packageBody)
	if err != nil || pkg.PackageSHA256 != sessionHandoffPayloadSHA256(packageBodyJSON) {
		return sessionHandoffPackage{}, invalidSessionHandoffEvent("package hash does not match")
	}
	realTokens := tokenestimate.Text(string(packageBodyJSON))
	if realTokens > pkg.Budget.EstimatedTokens || realTokens > pkg.Budget.LimitTokens || realTokens > 2048 {
		return sessionHandoffPackage{}, invalidSessionHandoffEvent("package token budget is understated or exceeded")
	}
	return pkg, nil
}

func decodeSessionHandoffStrict(payload string, target any) error {
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}

func validSessionHandoffText(value string) bool {
	return len(value) <= maxSessionHandoffTextBytes && value == strings.TrimSpace(value)
}

func validSessionHandoffStrings(values []string) bool {
	if values == nil || !sort.StringsAreSorted(values) {
		return false
	}
	for i, value := range values {
		if value == "" || !validSessionHandoffText(value) || i > 0 && value == values[i-1] {
			return false
		}
	}
	return true
}

func validSessionHandoffEvidence(sourceRef string, values []sessionHandoffEvidence) bool {
	for i, value := range values {
		validVerification := value.Verification == "reported" || value.Verification == "verified"
		if !validSessionHandoffText(value.Claim) || !validSessionHandoffSHA(value.SHA256) || !strings.HasPrefix(value.Ref, sourceRef+"#") || !validVerification {
			return false
		}
		verifierEmpty := value.Verifier.Kind == "" && value.Verifier.Ref == "" && value.Verifier.SHA256 == ""
		if value.Verification == "verified" && !validSessionHandoffVerifier(sourceRef, value.Verifier) {
			return false
		}
		if value.Verification == "reported" && !verifierEmpty && !validSessionHandoffVerifier(sourceRef, value.Verifier) {
			return false
		}
		if i > 0 {
			previous := values[i-1]
			if previous.Ref == value.Ref {
				return false
			}
			if previous.Ref > value.Ref || previous.Ref == value.Ref && (previous.SHA256 > value.SHA256 || previous.SHA256 == value.SHA256 && previous.Claim > value.Claim) {
				return false
			}
		}
	}
	return true
}

func validSessionHandoffVerifier(sourceRef string, value sessionHandoffVerifier) bool {
	switch value.Kind {
	case "tool_trace", "file_change", "test", "readback", "capability_loop":
	default:
		return false
	}
	return strings.HasPrefix(value.Ref, sourceRef+"#") && validSessionHandoffSHA(value.SHA256)
}

func validSessionHandoffRef(ref string, allowLocal bool) bool {
	kind, key, ok := strings.Cut(ref, ":")
	if !ok || key == "" || kind != "tenant" && !(allowLocal && kind == "local") {
		return false
	}
	for _, r := range key {
		if unicode.IsSpace(r) || unicode.IsControl(r) || r == '/' || r == '\\' || r == ':' || r == '#' {
			return false
		}
	}
	return true
}

func validSessionHandoffCursor(sourceRef, cursor string) bool {
	kind, _, _ := strings.Cut(sourceRef, ":")
	prefix, value, ok := strings.Cut(cursor, ":")
	if !ok {
		return false
	}
	if kind == "tenant" && prefix != "task_event" && prefix != "message" || kind == "local" && prefix != "line" && prefix != "entry" {
		return false
	}
	if prefix == "entry" {
		if value == "" || value == "." || value == ".." || len(value) > 128 {
			return false
		}
		for _, r := range value {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.') {
				return false
			}
		}
		return true
	}
	number, err := strconv.ParseUint(value, 10, 64)
	return err == nil && number > 0 && strconv.FormatUint(number, 10) == value
}

func validSessionHandoffSHA(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}

func sessionHandoffJSONSHA256(value any) string {
	encoded, _ := json.Marshal(value)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func sessionHandoffPayloadSHA256(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func invalidSessionHandoffEvent(reason string) error {
	return fmt.Errorf("%w: %s", ErrInvalidSessionHandoffEvent, reason)
}

type Event struct {
	ID          uint64    `json:"id"`
	TaskID      uint64    `json:"task_id"`
	EventType   string    `json:"event_type"`
	PayloadJSON string    `json:"payload_json,omitempty"`
	TraceID     string    `json:"trace_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type Store interface {
	CreateAgentTask(ctx context.Context, input TaskInput) (uint64, error)
	FinishAgentTask(ctx context.Context, taskID uint64, status string, resultJSON string) error
	AppendAgentTaskEvent(ctx context.Context, input EventInput) (uint64, error)
}

type CancellationChecker interface {
	IsAgentTaskCancelled(ctx context.Context, taskID uint64) (bool, error)
}
