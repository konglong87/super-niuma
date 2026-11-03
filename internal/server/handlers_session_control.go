package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/sessioncontrol"
	"github.com/konglong87/super-niuma/internal/tenant"
)

const maxSessionControlIdempotencyKeyBytes = 128

const (
	sessionControlCodeInvalidRequest         = "invalid_request"
	sessionControlCodeInvalidRef             = "invalid_ref"
	sessionControlCodeUnauthorized           = "unauthorized"
	sessionControlCodeServiceUnavailable     = "service_unavailable"
	sessionControlCodeIdempotencyKeyRequired = "idempotency_key_required"
	sessionControlCodeIdempotencyKeyTooLong  = "idempotency_key_too_long"
)

type sessionControlCreateDTO struct {
	SessionKey     string `json:"session_key,omitempty"`
	Title          string `json:"title,omitempty"`
	Model          string `json:"model,omitempty"`
	Provider       string `json:"provider,omitempty"`
	PermissionMode string `json:"permission_mode,omitempty"`
	Effort         string `json:"effort,omitempty"`
	PromptMode     string `json:"prompt_mode,omitempty"`
	CWD            string `json:"cwd,omitempty"`
	InitialText    string `json:"initial_text,omitempty"`
}

type sessionControlSendDTO struct {
	Content        string                       `json:"content"`
	Provider       string                       `json:"provider,omitempty"`
	Model          string                       `json:"model,omitempty"`
	PermissionMode string                       `json:"permission_mode,omitempty"`
	Effort         string                       `json:"effort,omitempty"`
	PromptMode     string                       `json:"prompt_mode,omitempty"`
	Attachments    []agentTaskAttachmentRequest `json:"attachments,omitempty"`
	SourceRefs     []string                     `json:"source_refs,omitempty"`
}

type sessionControlStopDTO struct{}

type sessionControlAttachDTO struct {
	TargetTaskID              uint64   `json:"target_task_id"`
	TargetContextWindowTokens int      `json:"target_context_window_tokens"`
	Sources                   []string `json:"sources"`
	RelationType              string   `json:"relation_type"`
}

type sessionControlMonitorDTO struct {
	Sources         []string `json:"sources"`
	IntervalSeconds int      `json:"interval_seconds"`
	Channel         string   `json:"channel"`
}

type sessionControlLinkDTO struct {
	ID           uint64    `json:"id"`
	Target       string    `json:"target"`
	Source       string    `json:"source"`
	RelationType string    `json:"relation_type"`
	CreatedAt    time.Time `json:"created_at"`
}

type sessionControlSessionDTO struct {
	ID             uint64                         `json:"id,omitempty"`
	Provider       string                         `json:"provider,omitempty"`
	Channel        *sessioncontrol.SessionChannel `json:"channel,omitempty"`
	PermissionMode string                         `json:"permission_mode,omitempty"`
	Effort         string                         `json:"effort,omitempty"`
	PromptMode     string                         `json:"prompt_mode,omitempty"`
	Ref            string                         `json:"ref"`
	Source         sessioncontrol.Source          `json:"source"`
	Title          string                         `json:"title"`
	Status         sessioncontrol.SessionStatus   `json:"status"`
	UpdatedAt      time.Time                      `json:"updated_at"`
	ShortID        string                         `json:"short_id"`
	Model          string                         `json:"model,omitempty"`
	CWD            string                         `json:"cwd,omitempty"`
	ActiveRunID    uint64                         `json:"active_run_id,omitempty"`
	ReadOnly       bool                           `json:"read_only,omitempty"`
	Links          []sessionControlLinkDTO        `json:"links,omitempty"`
}

type sessionControlHandoffSourceDTO struct {
	Source              string                                   `json:"source"`
	Success             bool                                     `json:"success"`
	ErrorCode           string                                   `json:"error_code,omitempty"`
	PackageID           string                                   `json:"package_id,omitempty"`
	PackageSHA256Prefix string                                   `json:"package_sha256_prefix,omitempty"`
	CursorPrefix        string                                   `json:"cursor_prefix,omitempty"`
	EstimatedTokens     int                                      `json:"estimated_tokens,omitempty"`
	LinkID              uint64                                   `json:"link_id,omitempty"`
	EventID             uint64                                   `json:"event_id,omitempty"`
	CandidateRemovals   []sessioncontrol.HandoffCandidateRemoval `json:"candidate_removals,omitempty"`
}

type sessionControlHandoffDTO struct {
	SourceResults   []sessionControlHandoffSourceDTO `json:"source_results,omitempty"`
	EstimatedTokens int                              `json:"estimated_tokens,omitempty"`
	TargetTaskID    uint64                           `json:"target_task_id,omitempty"`
	Stale           bool                             `json:"stale"`
	Replayed        bool                             `json:"replayed"`
}

type sessionControlOperationDTO struct {
	OperationID string                    `json:"operation_id"`
	Replayed    bool                      `json:"replayed"`
	Session     sessionControlSessionDTO  `json:"session"`
	RunID       uint64                    `json:"run_id,omitempty"`
	ScheduleID  string                    `json:"schedule_id,omitempty"`
	LinkIDs     []uint64                  `json:"link_ids,omitempty"`
	Handoff     *sessionControlHandoffDTO `json:"handoff,omitempty"`
	AuditID     uint64                    `json:"audit_id,omitempty"`
	ErrorCode   string                    `json:"error_code,omitempty"`
}

type sessionControlDataEnvelope[T any] struct {
	Data T `json:"data"`
}

type sessionControlErrorEnvelope struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func tenantSessionControlSessionsHandler(opts Options) http.HandlerFunc {
	return sessionControlEndpoint(opts, func(w http.ResponseWriter, r *http.Request, requestContext sessioncontrol.RequestContext) {
		switch r.Method {
		case http.MethodGet:
			source, err := parseSessionControlSource(r.URL.Query().Get("source"))
			if err != nil {
				writeSessionControlError(w, http.StatusBadRequest, sessionControlCodeInvalidRef, err.Error())
				return
			}
			items, err := opts.SessionControl.List(r.Context(), sessioncontrol.ListRequest{Context: requestContext, Source: source, Limit: parseLimit(r.URL.Query().Get(paramLimit))})
			if err != nil {
				writeSessionControlServiceError(w, err)
				return
			}
			out := make([]sessionControlSessionDTO, 0, len(items))
			for _, item := range items {
				out = append(out, newSessionControlSessionDTO(item))
			}
			writeJSON(w, sessionControlDataEnvelope[[]sessionControlSessionDTO]{Data: out})
		case http.MethodPost:
			key, ok := requireSessionControlIdempotencyKey(w, r)
			if !ok {
				return
			}
			var body sessionControlCreateDTO
			if !decodeSessionControlBody(w, r, &body, false) {
				return
			}
			if body.CWD == "" {
				body.CWD = opts.Workspace
			}
			if strings.TrimSpace(body.CWD) != "" {
				if opts.SessionControlCWDValidator == nil {
					writeSessionControlError(w, http.StatusBadRequest, sessionControlCodeInvalidRequest, "session cwd is not allowed")
					return
				}
				resolved, resolveErr := opts.SessionControlCWDValidator(body.CWD)
				if resolveErr != nil {
					writeSessionControlError(w, http.StatusBadRequest, sessionControlCodeInvalidRequest, "session cwd is not allowed")
					return
				}
				body.CWD = resolved
			}
			if opts.SessionControlRouteResolver != nil {
				provider, model, err := opts.SessionControlRouteResolver(body.CWD, body.Provider, body.Model)
				if err != nil {
					writeSessionControlError(w, http.StatusBadRequest, sessionControlCodeInvalidRequest, "provider route is invalid")
					return
				}
				body.Provider, body.Model = provider, model
			}
			result, err := opts.SessionControl.Create(r.Context(), sessioncontrol.CreateRequest{Context: requestContext, SessionKey: body.SessionKey, Title: body.Title, Model: body.Model, Provider: body.Provider, PermissionMode: body.PermissionMode, Effort: body.Effort, PromptMode: body.PromptMode, CWD: body.CWD, InitialText: body.InitialText, IdempotencyKey: key})
			writeSessionControlOperation(w, result, err)
		}
	})
}

func tenantSessionControlSessionHandler(opts Options) http.HandlerFunc {
	return sessionControlEndpoint(opts, func(w http.ResponseWriter, r *http.Request, requestContext sessioncontrol.RequestContext) {
		ref, ok := sessionControlRefFromRequest(w, r)
		if !ok {
			return
		}
		item, err := opts.SessionControl.Get(r.Context(), sessioncontrol.GetRequest{Context: requestContext, Ref: ref, IncludeLinks: parseBoolQuery(r.URL.Query().Get("include_links"))})
		if err != nil {
			writeSessionControlServiceError(w, err)
			return
		}
		writeJSON(w, sessionControlDataEnvelope[sessionControlSessionDTO]{Data: newSessionControlSessionDTO(item)})
	})
}

func tenantSessionControlMessageHandler(opts Options) http.HandlerFunc {
	return sessionControlMutationEndpoint(opts, func(w http.ResponseWriter, r *http.Request, requestContext sessioncontrol.RequestContext, ref sessioncontrol.SessionRef, key string) {
		var body sessionControlSendDTO
		if !decodeSessionControlBody(w, r, &body, false) {
			return
		}
		sources, ok := parseSessionControlRefs(w, body.SourceRefs)
		if !ok {
			return
		}
		attachments, err := prepareSessionControlAttachments(r.Context(), opts, requestContext, ref, body.Attachments)
		if err != nil {
			var serviceErr *sessioncontrol.ServiceError
			if errors.As(err, &serviceErr) {
				writeSessionControlServiceError(w, err)
			} else {
				writeSessionControlError(w, http.StatusBadRequest, sessionControlCodeInvalidRequest, err.Error())
			}
			return
		}
		result, err := opts.SessionControl.Send(r.Context(), sessioncontrol.SendRequest{Context: requestContext, Ref: ref, Content: body.Content, Model: body.Model, Provider: body.Provider, PermissionMode: body.PermissionMode, Effort: body.Effort, PromptMode: body.PromptMode, Attachments: attachments, SourceRefs: sources, IdempotencyKey: key})
		writeSessionControlOperation(w, result, err)
	})
}

func tenantSessionControlCompactHandler(opts Options) http.HandlerFunc {
	return sessionControlMutationEndpoint(opts, func(w http.ResponseWriter, r *http.Request, requestContext sessioncontrol.RequestContext, ref sessioncontrol.SessionRef, key string) {
		service, ok := opts.SessionControl.(SessionControlCompactService)
		if !ok {
			writeSessionControlError(w, http.StatusServiceUnavailable, sessionControlCodeServiceUnavailable, "session control compact is not configured")
			return
		}
		result, err := service.Compact(r.Context(), sessioncontrol.CompactRequest{
			Context: requestContext, Ref: ref, IdempotencyKey: key,
		})
		writeSessionControlOperation(w, result, err)
	})
}

func tenantSessionControlStopHandler(opts Options) http.HandlerFunc {
	return sessionControlMutationEndpoint(opts, func(w http.ResponseWriter, r *http.Request, requestContext sessioncontrol.RequestContext, ref sessioncontrol.SessionRef, key string) {
		var body sessionControlStopDTO
		if !decodeSessionControlBody(w, r, &body, true) {
			return
		}
		result, err := opts.SessionControl.Stop(r.Context(), sessioncontrol.StopRequest{Context: requestContext, Ref: ref, IdempotencyKey: key})
		writeSessionControlOperation(w, result, err)
	})
}

func tenantSessionControlAttachHandler(opts Options) http.HandlerFunc {
	return sessionControlMutationEndpoint(opts, func(w http.ResponseWriter, r *http.Request, requestContext sessioncontrol.RequestContext, ref sessioncontrol.SessionRef, key string) {
		var body sessionControlAttachDTO
		if !decodeSessionControlBody(w, r, &body, false) {
			return
		}
		sources, ok := parseSessionControlRefs(w, body.Sources)
		if !ok {
			return
		}
		result, err := opts.SessionControl.Attach(r.Context(), sessioncontrol.AttachRequest{Context: requestContext, Target: ref, TargetTaskID: body.TargetTaskID, TargetContextWindowTokens: body.TargetContextWindowTokens, Sources: sources, RelationType: body.RelationType, IdempotencyKey: key})
		writeSessionControlOperation(w, result, err)
	})
}

func tenantSessionControlMonitorHandler(opts Options) http.HandlerFunc {
	return sessionControlMutationEndpoint(opts, func(w http.ResponseWriter, r *http.Request, requestContext sessioncontrol.RequestContext, ref sessioncontrol.SessionRef, key string) {
		var body sessionControlMonitorDTO
		if !decodeSessionControlBody(w, r, &body, false) {
			return
		}
		sources, ok := parseSessionControlRefs(w, body.Sources)
		if !ok {
			return
		}
		result, err := opts.SessionControl.Monitor(r.Context(), sessioncontrol.MonitorRequest{Context: requestContext, Target: ref, Sources: sources, IntervalSeconds: body.IntervalSeconds, Channel: body.Channel, IdempotencyKey: key})
		writeSessionControlOperation(w, result, err)
	})
}

func sessionControlEndpoint(opts Options, next func(http.ResponseWriter, *http.Request, sessioncontrol.RequestContext)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if opts.AuthToken != "" && !authorizeHeaderToken(r, opts.AuthToken) {
			writeSessionControlError(w, http.StatusUnauthorized, sessionControlCodeUnauthorized, "unauthorized")
			return
		}
		if opts.TenantService == nil || opts.SessionControl == nil {
			writeSessionControlError(w, http.StatusServiceUnavailable, sessionControlCodeServiceUnavailable, "session control is not configured")
			return
		}
		resolved, err := opts.TenantService.ResolveContext(r.Context())
		if err != nil {
			writeSessionControlTenantError(w, err)
			return
		}
		next(w, r, sessioncontrol.RequestContext{TenantID: resolved.TenantID, UserID: resolved.UserID, ActorUserID: resolved.UserID, TraceID: observability.TraceID(r.Context())})
	}
}

func sessionControlMutationEndpoint(opts Options, next func(http.ResponseWriter, *http.Request, sessioncontrol.RequestContext, sessioncontrol.SessionRef, string)) http.HandlerFunc {
	return sessionControlEndpoint(opts, func(w http.ResponseWriter, r *http.Request, requestContext sessioncontrol.RequestContext) {
		ref, ok := sessionControlRefFromRequest(w, r)
		if !ok {
			return
		}
		key, ok := requireSessionControlIdempotencyKey(w, r)
		if !ok {
			return
		}
		if ref.Source == sessioncontrol.SourceLocal {
			writeSessionControlError(w, http.StatusForbidden, string(sessioncontrol.CodeForbidden), "local sessions are read-only")
			return
		}
		next(w, r, requestContext, ref, key)
	})
}

func sessionControlRefFromRequest(w http.ResponseWriter, r *http.Request) (sessioncontrol.SessionRef, bool) {
	source, id, ok := sessionControlPathRef(r.URL.Path)
	if !ok {
		writeSessionControlError(w, http.StatusBadRequest, sessionControlCodeInvalidRef, "session ref path is invalid")
		return sessioncontrol.SessionRef{}, false
	}
	ref, err := sessioncontrol.ParseRef(source + ":" + id)
	if err != nil {
		writeSessionControlError(w, http.StatusBadRequest, sessionControlCodeInvalidRef, "session ref is invalid")
		return sessioncontrol.SessionRef{}, false
	}
	return ref, true
}

func sessionControlPathRef(path string) (string, string, bool) {
	const prefix = "/tenant/session-control/sessions/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if len(parts) < 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func parseSessionControlSource(raw string) (sessioncontrol.Source, error) {
	switch sessioncontrol.Source(strings.ToLower(strings.TrimSpace(raw))) {
	case sessioncontrol.SourceTenant:
		return sessioncontrol.SourceTenant, nil
	case sessioncontrol.SourceLocal:
		return sessioncontrol.SourceLocal, nil
	default:
		return "", fmt.Errorf("source must be tenant or local")
	}
}

func parseSessionControlRefs(w http.ResponseWriter, values []string) ([]sessioncontrol.SessionRef, bool) {
	refs := make([]sessioncontrol.SessionRef, 0, len(values))
	for _, value := range values {
		ref, err := sessioncontrol.ParseRef(value)
		if err != nil {
			writeSessionControlError(w, http.StatusBadRequest, sessionControlCodeInvalidRef, "source session ref is invalid")
			return nil, false
		}
		refs = append(refs, ref)
	}
	return refs, true
}

func requireSessionControlIdempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		writeSessionControlError(w, http.StatusBadRequest, sessionControlCodeIdempotencyKeyRequired, "Idempotency-Key header is required")
		return "", false
	}
	if len([]byte(key)) > maxSessionControlIdempotencyKeyBytes {
		writeSessionControlError(w, http.StatusBadRequest, sessionControlCodeIdempotencyKeyTooLong, "Idempotency-Key header exceeds 128 bytes")
		return "", false
	}
	return key, true
}

func decodeSessionControlBody(w http.ResponseWriter, r *http.Request, target any, optional bool) bool {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		var limitErr *http.MaxBytesError
		if errors.As(err, &limitErr) {
			writeRequestBodyTooLarge(w, limitErr.Limit)
			return false
		}
		if optional && errors.Is(err, io.EOF) {
			return true
		}
		writeSessionControlError(w, http.StatusBadRequest, sessionControlCodeInvalidRequest, "request body is invalid")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeSessionControlError(w, http.StatusBadRequest, sessionControlCodeInvalidRequest, "request body must contain one JSON value")
		return false
	}
	return true
}

func parseBoolQuery(value string) bool {
	parsed, _ := strconv.ParseBool(strings.TrimSpace(value))
	return parsed
}

func writeSessionControlOperation(w http.ResponseWriter, result sessioncontrol.OperationResult, err error) {
	if err != nil {
		writeSessionControlServiceError(w, err)
		return
	}
	writeJSON(w, sessionControlDataEnvelope[sessionControlOperationDTO]{Data: newSessionControlOperationDTO(result)})
}

func writeSessionControlServiceError(w http.ResponseWriter, err error) {
	var serviceErr *sessioncontrol.ServiceError
	if !errors.As(err, &serviceErr) {
		var refErr *sessioncontrol.RefError
		if errors.As(err, &refErr) {
			writeSessionControlError(w, http.StatusBadRequest, sessionControlCodeInvalidRef, refErr.Message)
			return
		}
		var localErr *sessioncontrol.LocalSessionError
		if errors.As(err, &localErr) {
			writeSessionControlError(w, http.StatusNotFound, string(localErr.Code), localErr.Message)
			return
		}
		writeSessionControlError(w, http.StatusInternalServerError, string(sessioncontrol.CodeInternal), "session control request failed")
		return
	}
	status := http.StatusInternalServerError
	switch serviceErr.Code {
	case sessioncontrol.CodeForbidden:
		status = http.StatusForbidden
	case sessioncontrol.CodeNotFound:
		status = http.StatusNotFound
	case sessioncontrol.CodeInvalidState, sessioncontrol.CodeIdempotencyConflict, sessioncontrol.CodeHandoffStale:
		status = http.StatusConflict
	case sessioncontrol.CodeBudgetExceeded:
		status = http.StatusUnprocessableEntity
	case sessioncontrol.CodeSchedulerUnavailable:
		status = http.StatusServiceUnavailable
	}
	message := strings.TrimSpace(serviceErr.Message)
	if message == "" {
		message = string(serviceErr.Code)
	}
	writeSessionControlError(w, status, string(serviceErr.Code), message)
}

func writeSessionControlTenantError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, tenant.ErrForbidden):
		writeSessionControlError(w, http.StatusForbidden, string(sessioncontrol.CodeForbidden), "tenant access forbidden")
	case errors.Is(err, tenant.ErrMissingTenantKey), errors.Is(err, tenant.ErrMissingUserID), errors.Is(err, tenant.ErrInvalidRequest):
		writeSessionControlError(w, http.StatusBadRequest, sessionControlCodeInvalidRequest, err.Error())
	default:
		writeSessionControlError(w, http.StatusInternalServerError, string(sessioncontrol.CodeInternal), "tenant context resolution failed")
	}
}

func writeSessionControlError(w http.ResponseWriter, status int, code, message string) {
	writeJSONStatus(w, status, sessionControlErrorEnvelope{Error: message, Code: code})
}

func newSessionControlSessionDTO(item sessioncontrol.SessionSnapshot) sessionControlSessionDTO {
	links := make([]sessionControlLinkDTO, 0, len(item.Links))
	for _, link := range item.Links {
		links = append(links, sessionControlLinkDTO{ID: link.ID, Target: link.Target.String(), Source: link.Source.String(), RelationType: link.RelationType, CreatedAt: link.CreatedAt})
	}
	return sessionControlSessionDTO{
		ID: item.ID, Provider: item.Provider,
		Channel:        item.Channel,
		PermissionMode: item.PermissionMode, Effort: item.Effort, PromptMode: item.PromptMode,
		Ref: item.Ref.String(), Source: item.Ref.Source, Title: item.Title, Status: item.Status,
		UpdatedAt: item.UpdatedAt, ShortID: sessionControlShortID(item.Ref.Key), Model: item.Model,
		CWD: item.CWD, ActiveRunID: item.ActiveRunID, ReadOnly: item.ReadOnly, Links: links,
	}
}

func newSessionControlOperationDTO(item sessioncontrol.OperationResult) sessionControlOperationDTO {
	result := sessionControlOperationDTO{
		OperationID: item.OperationID, Replayed: item.Replayed, Session: newSessionControlSessionDTO(item.Session),
		RunID: item.RunID, ScheduleID: item.ScheduleID, LinkIDs: item.LinkIDs, AuditID: item.AuditID, ErrorCode: item.ErrorCode,
	}
	if len(item.Handoff.SourceResults) > 0 || item.Handoff.EstimatedTokens != 0 || item.Handoff.TargetTaskID != 0 || item.Handoff.Stale || item.Handoff.Replayed {
		handoff := sessionControlHandoffDTO{EstimatedTokens: item.Handoff.EstimatedTokens, TargetTaskID: item.Handoff.TargetTaskID, Stale: item.Handoff.Stale, Replayed: item.Handoff.Replayed}
		for _, source := range item.Handoff.SourceResults {
			handoff.SourceResults = append(handoff.SourceResults, sessionControlHandoffSourceDTO{
				Source: source.Source.String(), Success: source.Success, ErrorCode: source.ErrorCode, PackageID: source.PackageID,
				PackageSHA256Prefix: source.PackageSHA256Prefix, CursorPrefix: source.CursorPrefix, EstimatedTokens: source.EstimatedTokens,
				LinkID: source.LinkID, EventID: source.EventID, CandidateRemovals: source.CandidateRemovals,
			})
		}
		result.Handoff = &handoff
	}
	return result
}

func sessionControlShortID(key string) string {
	const maxRunes = 12
	runes := []rune(key)
	if len(runes) <= maxRunes {
		return key
	}
	return string(runes[:maxRunes])
}
