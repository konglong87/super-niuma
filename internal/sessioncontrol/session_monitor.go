package sessioncontrol

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	channelcontract "github.com/konglong87/super-niuma/internal/channel"
	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/scheduler"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

const (
	SessionMonitorScheduleKind       = "session-monitor"
	SessionMonitorToolSessionGet     = "SessionGet"
	SessionMonitorToolChannelReport  = "ChannelReport"
	SessionMonitorReportWindow       = 24 * time.Hour
	SessionMonitorMinIntervalSeconds = 10
	SessionMonitorMaxIntervalSeconds = 30 * 24 * 60 * 60
	sessionMonitorMetadataVersion    = 1
	sessionMonitorPrompt             = "session monitor"
	sessionMonitorRelationObserved   = "observed"
)

type SessionMonitorMetadata struct {
	Version                  int      `json:"version"`
	ControllerTargetRef      string   `json:"controller_target_ref"`
	ObservedSourceRefs       []string `json:"observed_source_refs"`
	FrequencySeconds         int      `json:"frequency_seconds"`
	Channel                  string   `json:"channel"`
	ConfigurationFingerprint string   `json:"configuration_fingerprint"`
	ScheduleID               string   `json:"schedule_id,omitempty"`
	LastObservedCursor       string   `json:"last_observed_cursor,omitempty"`
	LastObservedStatus       string   `json:"last_observed_status,omitempty"`
}

type SessionMonitorStore interface {
	ResolveSessionMonitorTarget(context.Context, string, string) (mysqlstore.SessionMonitorTarget, error)
	GetSessionControlSessionByKey(context.Context, string) (mysqlstore.SessionControlSession, error)
	GetSessionMonitorLink(context.Context, uint64, string, string) (mysqlstore.SessionLink, error)
	ApplySessionMonitorConfiguration(context.Context, mysqlstore.SessionMonitorConfigurationInput) (mysqlstore.SessionMonitorConfigurationResult, error)
	GetSessionControlAuditByKeyHash(context.Context, string, string) (mysqlstore.AuditLog, error)
}

type SessionMonitorDependencies struct {
	Store        SessionMonitorStore
	Schedules    *scheduler.Store
	EnsureDaemon func() error
}

type SessionMonitor struct{ deps SessionMonitorDependencies }

var _ MonitorPort = (*SessionMonitor)(nil)
var _ MonitorRecoveryPort = (*SessionMonitor)(nil)

func NewSessionMonitor(deps SessionMonitorDependencies) *SessionMonitor {
	return &SessionMonitor{deps: deps}
}

func (m *SessionMonitor) RecoverMonitor(ctx context.Context, request MonitorRequest, identity OperationIdentity) (RecoveredOperation, error) {
	target, anchor, err := m.preflight(ctx, request)
	if err != nil {
		return RecoveredOperation{}, err
	}
	if err := m.ensureDaemon(); err != nil {
		return RecoveredOperation{}, err
	}
	historicalIdentity := false
	audit, auditErr := m.deps.Store.GetSessionControlAuditByKeyHash(ctx, "session_control."+string(OperationMonitor), identity.KeyHash)
	if auditErr == nil {
		stored, metadataErr := operationMetadataFromJSON(audit.MetadataJSON)
		if metadataErr != nil {
			return RecoveredOperation{}, metadataErr
		}
		if metadataErr := validateRecoveredMetadata(identity, stored); metadataErr != nil {
			return RecoveredOperation{}, metadataErr
		}
		historicalIdentity = true
	} else if !errors.Is(auditErr, mysqlstore.ErrNotFound) {
		return RecoveredOperation{}, normalizeMonitorError(auditErr)
	}
	link, err := m.deps.Store.GetSessionMonitorLink(ctx, target.SessionID, string(anchor.Source), anchor.Key)
	if errors.Is(err, mysqlstore.ErrNotFound) {
		return RecoveredOperation{}, nil
	}
	if err != nil {
		return RecoveredOperation{}, normalizeMonitorError(err)
	}
	metadata, err := decodeSessionMonitorMetadata(link.MetadataJSON)
	if err != nil {
		return RecoveredOperation{}, err
	}
	storedKeyHash, storedFingerprint, ok := strings.Cut(metadata.ConfigurationFingerprint, ".")
	if !ok {
		return RecoveredOperation{}, invalidState("session monitor configuration fingerprint is invalid")
	}
	if !historicalIdentity && storedKeyHash == identity.KeyHash && storedFingerprint != identity.Fingerprint {
		return RecoveredOperation{}, &ServiceError{Code: CodeIdempotencyConflict, Message: "idempotency key was used for a different request"}
	}
	if !historicalIdentity && storedKeyHash != identity.KeyHash && storedFingerprint != identity.Fingerprint {
		return RecoveredOperation{}, nil
	}
	if metadata.ScheduleID == "" {
		return RecoveredOperation{}, nil
	}
	_, resolvedScheduleID, projectionErr := m.ensureAuthoritativeProjection(ctx, target.SessionID, anchor, link.ID)
	if projectionErr != nil {
		return RecoveredOperation{}, schedulerUnavailable(projectionErr)
	}
	metadata.ScheduleID = resolvedScheduleID
	if err := m.ensureDaemon(); err != nil {
		return RecoveredOperation{}, err
	}
	return RecoveredOperation{Found: true, Metadata: identity.Metadata(), Target: request.Target, Result: OperationResult{ScheduleID: metadata.ScheduleID, LinkIDs: []uint64{link.ID}}}, nil
}

func (m *SessionMonitor) Monitor(ctx context.Context, request MonitorRequest) (OperationResult, error) {
	target, anchor, err := m.preflight(ctx, request)
	if err != nil {
		return OperationResult{}, err
	}
	identity := request.ReplayIdentity
	if err := validateOperationIdentity(identity); err != nil {
		return OperationResult{}, err
	}
	// A failed request must not leave an enabled monitor that can begin sending
	// later when another process happens to revive the scheduler.
	if err := m.ensureDaemon(); err != nil {
		return OperationResult{}, err
	}
	metadata := monitorMetadata(request, identity)
	raw, err := encodeSessionMonitorMetadata(metadata)
	if err != nil {
		return OperationResult{}, err
	}
	configuration, err := m.deps.Store.ApplySessionMonitorConfiguration(ctx, mysqlstore.SessionMonitorConfigurationInput{
		SessionMonitorLinkInput: mysqlstore.SessionMonitorLinkInput{
			TenantID: request.Context.TenantID, UserID: request.Context.UserID, TargetSessionID: target.SessionID,
			SourceKind: string(anchor.Source), SourceSessionKey: anchor.Key, RelationType: sessionMonitorRelationObserved,
			MetadataJSON: raw, CreatedByUserID: request.Context.ActorUserID,
		},
		ConfigurationKeyHash: identity.KeyHash, ConfigurationFingerprint: identity.Fingerprint,
	})
	if err != nil {
		return OperationResult{}, normalizeMonitorError(err)
	}
	if configuration.Conflict {
		return OperationResult{}, &ServiceError{Code: CodeIdempotencyConflict, Message: "idempotency key was used for a different request"}
	}
	link := configuration.Link
	latest, scheduleID, err := m.ensureAuthoritativeProjection(ctx, target.SessionID, anchor, link.ID)
	if err != nil {
		return OperationResult{}, schedulerUnavailable(err)
	}
	if err := m.ensureDaemon(); err != nil {
		return OperationResult{}, err
	}
	return OperationResult{Session: SessionSnapshot{Ref: request.Target}, ScheduleID: scheduleID, LinkIDs: []uint64{latest.ID}}, nil
}

func (m *SessionMonitor) preflight(ctx context.Context, request MonitorRequest) (mysqlstore.SessionMonitorTarget, SessionRef, error) {
	if m == nil || m.deps.Store == nil || m.deps.Schedules == nil || strings.TrimSpace(m.deps.Schedules.Root) == "" {
		return mysqlstore.SessionMonitorTarget{}, SessionRef{}, &ServiceError{Code: CodeSchedulerUnavailable, Message: "session monitor runtime is unavailable"}
	}
	if strings.TrimSpace(request.Channel) != string(channelcontract.ProviderFeishu) {
		return mysqlstore.SessionMonitorTarget{}, SessionRef{}, &ServiceError{Code: CodeSchedulerUnavailable, Message: "session monitor channel is unavailable"}
	}
	if request.IntervalSeconds < SessionMonitorMinIntervalSeconds || request.IntervalSeconds > SessionMonitorMaxIntervalSeconds {
		return mysqlstore.SessionMonitorTarget{}, SessionRef{}, invalidState("session monitor interval is outside the supported range")
	}
	if len(request.Sources) == 0 {
		return mysqlstore.SessionMonitorTarget{}, SessionRef{}, invalidState("at least one monitor source is required")
	}
	target, err := m.deps.Store.ResolveSessionMonitorTarget(ctx, request.Target.Key, request.Channel)
	if err != nil {
		return mysqlstore.SessionMonitorTarget{}, SessionRef{}, schedulerUnavailable(err)
	}
	sources := canonicalMonitorSources(request.Sources)
	for _, source := range sources {
		if source.Source != SourceTenant {
			return mysqlstore.SessionMonitorTarget{}, SessionRef{}, &ServiceError{Code: CodeForbidden, Message: "session monitor source is not available to the scheduler"}
		}
		if _, err := m.deps.Store.GetSessionControlSessionByKey(ctx, source.Key); err != nil {
			return mysqlstore.SessionMonitorTarget{}, SessionRef{}, normalizeMonitorError(err)
		}
	}
	return target, request.Target, nil
}

func (m *SessionMonitor) ensureDaemon() error {
	if m.deps.EnsureDaemon == nil {
		return nil
	}
	if err := m.deps.EnsureDaemon(); err != nil {
		return schedulerUnavailable(err)
	}
	return nil
}

func (m *SessionMonitor) ensureAuthoritativeProjection(ctx context.Context, targetSessionID uint64, anchor SessionRef, linkID uint64) (mysqlstore.SessionLink, string, error) {
	projectionID := sessionMonitorScheduleID(linkID)
	var latest mysqlstore.SessionLink
	projection, err := m.deps.Schedules.EnsureProjectionFromAuthority(projectionID, func() (scheduler.Options, error) {
		link, getErr := m.deps.Store.GetSessionMonitorLink(ctx, targetSessionID, string(anchor.Source), anchor.Key)
		if getErr != nil {
			return scheduler.Options{}, normalizeMonitorError(getErr)
		}
		metadata, decodeErr := decodeSessionMonitorMetadata(link.MetadataJSON)
		if decodeErr != nil {
			return scheduler.Options{}, decodeErr
		}
		if metadata.ScheduleID != projectionID {
			return scheduler.Options{}, invalidState("session monitor schedule identity is invalid")
		}
		latest = link
		return sessionMonitorScheduleOptions(metadata), nil
	})
	if err != nil {
		return mysqlstore.SessionLink{}, "", err
	}
	return latest, projection.ID, nil
}

func sessionMonitorScheduleOptions(metadata SessionMonitorMetadata) scheduler.Options {
	return scheduler.Options{Prompt: sessionMonitorPrompt, Kind: SessionMonitorScheduleKind, Spec: monitorCronSpec(metadata.FrequencySeconds), IntervalSeconds: metadata.FrequencySeconds, OutputFormat: "json", NoPersistence: true, AllowedTools: []string{SessionMonitorToolSessionGet, SessionMonitorToolChannelReport}}
}

func sessionMonitorScheduleID(linkID uint64) string {
	return fmt.Sprintf("session-monitor-%d", linkID)
}

func monitorMetadata(request MonitorRequest, identity OperationIdentity) SessionMonitorMetadata {
	sources := canonicalMonitorSources(request.Sources)
	refs := make([]string, 0, len(sources))
	for _, source := range sources {
		refs = append(refs, source.String())
	}
	return SessionMonitorMetadata{Version: sessionMonitorMetadataVersion, ControllerTargetRef: request.Target.String(), ObservedSourceRefs: refs, FrequencySeconds: request.IntervalSeconds, Channel: strings.TrimSpace(request.Channel), ConfigurationFingerprint: identity.KeyHash + "." + identity.Fingerprint}
}

func canonicalMonitorSources(sources []SessionRef) []SessionRef {
	out := append([]SessionRef(nil), sources...)
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

func monitorCronSpec(seconds int) string {
	d := time.Duration(seconds) * time.Second
	if seconds > 0 && d%time.Hour == 0 {
		return fmt.Sprintf("@every %dh", int(d/time.Hour))
	}
	if seconds > 0 && d%time.Minute == 0 {
		return fmt.Sprintf("@every %dm", int(d/time.Minute))
	}
	return fmt.Sprintf("@every %ds", seconds)
}

func encodeSessionMonitorMetadata(metadata SessionMonitorMetadata) (string, error) {
	if metadata.Version != sessionMonitorMetadataVersion || metadata.ControllerTargetRef == "" || len(metadata.ObservedSourceRefs) == 0 || metadata.FrequencySeconds <= 0 || metadata.Channel == "" || metadata.ConfigurationFingerprint == "" {
		return "", invalidState("session monitor metadata is invalid")
	}
	keyHash, fingerprint, ok := strings.Cut(metadata.ConfigurationFingerprint, ".")
	if !ok || !validOperationHash(keyHash) || !validOperationHash(fingerprint) || metadata.Channel != string(channelcontract.ProviderFeishu) {
		return "", invalidState("session monitor metadata is invalid")
	}
	target, err := ParseRef(metadata.ControllerTargetRef)
	if err != nil || target.Source != SourceTenant {
		return "", invalidState("session monitor target metadata is invalid")
	}
	for _, sourceRef := range metadata.ObservedSourceRefs {
		source, parseErr := ParseRef(sourceRef)
		if parseErr != nil || source.Source != SourceTenant {
			return "", invalidState("session monitor source metadata is invalid")
		}
	}
	raw, err := json.Marshal(metadata)
	return string(raw), err
}

func decodeSessionMonitorMetadata(raw string) (SessionMonitorMetadata, error) {
	var metadata SessionMonitorMetadata
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&metadata); err != nil {
		return SessionMonitorMetadata{}, invalidState("session monitor metadata is invalid")
	}
	if _, err := encodeSessionMonitorMetadata(metadata); err != nil {
		return SessionMonitorMetadata{}, err
	}
	return metadata, nil
}

func normalizeMonitorError(err error) error {
	if err == nil {
		return nil
	}
	var serviceErr *ServiceError
	if errors.As(err, &serviceErr) {
		return serviceErr
	}
	if errors.Is(err, mysqlstore.ErrNotFound) {
		return &ServiceError{Code: CodeNotFound, Message: "session monitor resource was not found", Cause: err}
	}
	return &ServiceError{Code: CodeInternal, Message: "session monitor dependency failed", Cause: err}
}

func schedulerUnavailable(err error) error {
	return &ServiceError{Code: CodeSchedulerUnavailable, Message: "session monitor scheduler or channel is unavailable", Cause: err}
}

func firstMonitorError(err error, fallback string) error {
	if err != nil {
		return err
	}
	return errors.New(fallback)
}

type SessionMonitorGetPort interface {
	Get(context.Context, GetRequest) (SessionSnapshot, error)
}

type SessionMonitorChildStore interface {
	ResolveSessionMonitorByScheduleID(context.Context, string) (mysqlstore.SessionMonitorRecord, error)
	CommitSessionMonitorObservation(context.Context, mysqlstore.SessionMonitorObservationInput) (mysqlstore.SessionMonitorObservationResult, error)
}

type SessionMonitorChild struct {
	Store     SessionMonitorChildStore
	Sessions  SessionMonitorGetPort
	Schedules *scheduler.Store
	Now       func() time.Time
}

func (c SessionMonitorChild) Run(ctx context.Context, scheduleID string) error {
	if c.Store == nil || c.Sessions == nil || c.Schedules == nil || strings.TrimSpace(scheduleID) == "" {
		return errors.New("session monitor child is not configured")
	}
	var record mysqlstore.SessionMonitorRecord
	var metadata SessionMonitorMetadata
	if _, err := c.Schedules.EnsureProjectionFromAuthority(scheduleID, func() (scheduler.Options, error) {
		current, resolveErr := c.Store.ResolveSessionMonitorByScheduleID(ctx, scheduleID)
		if resolveErr != nil {
			return scheduler.Options{}, resolveErr
		}
		currentMetadata, decodeErr := decodeSessionMonitorMetadata(current.Link.MetadataJSON)
		if decodeErr != nil || currentMetadata.ScheduleID != scheduleID || sessionMonitorScheduleID(current.Link.ID) != scheduleID {
			return scheduler.Options{}, firstMonitorError(decodeErr, "session monitor schedule identity mismatch")
		}
		record, metadata = current, currentMetadata
		return sessionMonitorScheduleOptions(currentMetadata), nil
	}); err != nil {
		return err
	}
	requestContext := RequestContext{TenantID: record.Link.TenantID, UserID: record.Link.UserID, ActorUserID: record.Link.UserID, TraceID: observability.TraceID(ctx)}
	trustedCtx := observability.WithRequestValues(ctx, requestContext.TraceID, record.UserKey, record.TenantKey)
	refs := append([]string{metadata.ControllerTargetRef}, metadata.ObservedSourceRefs...)
	summaries := make([]monitorStateSummary, 0, len(refs))
	for _, rawRef := range refs {
		ref, parseErr := ParseRef(rawRef)
		if parseErr != nil {
			return parseErr
		}
		snapshot, getErr := c.Sessions.Get(trustedCtx, GetRequest{Context: requestContext, Ref: ref})
		if getErr != nil {
			return getErr
		}
		summaries = append(summaries, monitorStateSummary{Ref: snapshot.Ref.String(), Status: snapshot.Status, ActiveRunID: snapshot.ActiveRunID, UpdatedAt: snapshot.UpdatedAt.UTC()})
	}
	rawSummary, err := json.Marshal(summaries)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(rawSummary)
	cursor := hex.EncodeToString(hash[:])
	status := aggregateMonitorStatus(summaries)
	now := time.Now().UTC()
	if c.Now != nil {
		now = c.Now().UTC()
	}
	reportDue := metadata.LastObservedCursor != cursor || !record.Link.UpdatedAt.After(now.Add(-SessionMonitorReportWindow))
	if !reportDue {
		return nil
	}
	previous := record.Link.MetadataJSON
	metadata.LastObservedCursor = cursor
	metadata.LastObservedStatus = status
	next, err := encodeSessionMonitorMetadata(metadata)
	if err != nil {
		return err
	}
	outbound := channelcontract.OutboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: record.AccountKey, ConversationID: fmt.Sprint(record.ConversationID), ExternalChatID: record.ExternalChatID, ExternalThreadID: record.ExternalThreadID, Kind: channelcontract.MessageKindText, Text: boundedMonitorReport(summaries), IdempotencyKey: monitorDeliveryKey(record.Link.ID, cursor, now), CorrelationID: scheduleID}
	payload, err := json.Marshal(outbound)
	if err != nil {
		return err
	}
	_, err = c.Store.CommitSessionMonitorObservation(trustedCtx, mysqlstore.SessionMonitorObservationInput{TenantID: record.Link.TenantID, UserID: record.Link.UserID, LinkID: record.Link.ID, ExpectedMetadataJSON: previous, MetadataJSON: next, AccountID: record.AccountID, ConversationID: record.ConversationID, IdempotencyKey: outbound.IdempotencyKey, PayloadJSON: string(payload), Deliver: true})
	return err
}

type monitorStateSummary struct {
	Ref         string        `json:"ref"`
	Status      SessionStatus `json:"status"`
	ActiveRunID uint64        `json:"active_run_id,omitempty"`
	UpdatedAt   time.Time     `json:"updated_at,omitempty"`
}

func aggregateMonitorStatus(items []monitorStateSummary) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, item.Ref+"="+string(item.Status))
	}
	return strings.Join(parts, ",")
}

func boundedMonitorReport(items []monitorStateSummary) string {
	const maxReportBytes = 4096
	text := "Session monitor update\n"
	for _, item := range items {
		line := fmt.Sprintf("%s: %s\n", item.Ref, item.Status)
		if len(text)+len(line) > maxReportBytes {
			break
		}
		text += line
	}
	return strings.TrimSpace(text)
}

func monitorDeliveryKey(linkID uint64, cursor string, now time.Time) string {
	return fmt.Sprintf("session-monitor:%d:%s:%d", linkID, cursor[:12], now.Unix()/int64(SessionMonitorReportWindow/time.Second))
}
