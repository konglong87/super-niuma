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

	"github.com/gin-gonic/gin"
	"github.com/konglong87/super-niuma/internal/agentprofile"
	"github.com/konglong87/super-niuma/internal/agentteam/workflow"
	"github.com/konglong87/super-niuma/internal/agentteam/workflowapp"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

func registerWorkflowRoutes(router gin.IRouter, opts Options) {
	handler := tenantEndpoint(opts, nil, workflowHandler(opts))
	for _, path := range []string{"/tenant/workflows", "/tenant/workflows/templates", "/tenant/workflows/templates/:key/create", "/tenant/workflows/:key", "/tenant/workflows/:key/runs", "/tenant/workflow-runs/:id", "/tenant/workflow-runs/:id/events", "/tenant/workflow-runs/:id/events/stream", "/tenant/workflow-runs/:id/cancel", "/tenant/workflow-runs/:id/recover"} {
		router.Any(path, gin.WrapF(handler))
	}
	router.GET("/tenant/agent-types", gin.WrapF(tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"roles": agentprofile.RoleTypeDescriptors(), "execution_kinds": agentprofile.ExecutionKindDescriptors()})
	})))
}
func workflowScope(r *http.Request, opts Options) (workflow.Scope, error) {
	c, err := opts.TenantService.ResolveContext(r.Context())
	return workflow.Scope{TenantID: c.TenantID, UserID: c.UserID}, err
}
func workflowParts(path string, prefix string) []string {
	index := strings.Index(path, prefix)
	if index < 0 {
		return nil
	}
	tail := strings.Trim(path[index+len(prefix):], "/")
	if tail == "" {
		return nil
	}
	return strings.Split(tail, "/")
}
func workflowHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		service := opts.WorkflowService
		if service == nil {
			writeWorkflowError(w, nil, errors.New("workflow service is unavailable"))
			return
		}
		scope, err := workflowScope(r, opts)
		if err != nil {
			writeWorkflowError(w, service, err)
			return
		}
		if r.Method != http.MethodGet && opts.WorkflowReadOnly {
			writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"code": "workflow_read_only", "error": "Workflow execution is disabled; historical runs remain available."})
			return
		}
		if r.Method != http.MethodGet && !requireTenantRole(w, r, opts, "owner", "admin") {
			return
		}
		if strings.Contains(r.URL.Path, "/tenant/workflow-runs/") {
			workflowRunEndpoint(w, r, service, scope)
			return
		}
		parts := workflowParts(r.URL.Path, "/tenant/workflows")
		if len(parts) == 0 {
			switch r.Method {
			case http.MethodGet:
				items, err := service.List(r.Context(), scope)
				if err != nil {
					writeWorkflowError(w, service, err)
					return
				}
				writeJSON(w, map[string]any{"items": items})
			case http.MethodPost:
				var req workflowapp.SaveRequest
				if !decodeWorkflowBody(w, r, &req) {
					return
				}
				item, err := service.Save(r.Context(), scope, req)
				if err != nil {
					writeWorkflowError(w, service, err)
					return
				}
				writeJSON(w, map[string]any{"template": item})
			default:
				writeWorkflowMethod(w)
			}
			return
		}
		if parts[0] == "templates" {
			if len(parts) == 1 && r.Method == http.MethodGet {
				writeJSON(w, map[string]any{"items": workflowapp.Builtins()})
				return
			}
			if len(parts) == 3 && parts[2] == "create" && r.Method == http.MethodPost {
				item, err := service.CreateBuiltin(r.Context(), scope, parts[1])
				if err != nil {
					writeWorkflowError(w, service, err)
					return
				}
				writeJSON(w, map[string]any{"template": item})
				return
			}
			writeWorkflowMethod(w)
			return
		}
		if len(parts) == 1 && r.Method == http.MethodGet {
			version, valid := parseOptionalUint(r.URL.Query().Get("version"))
			if !valid {
				writeWorkflowError(w, service, &workflow.ValidationError{Field: "version", Message: "invalid version"})
				return
			}
			item, err := service.Get(r.Context(), scope, parts[0], version)
			if err != nil {
				writeWorkflowError(w, service, err)
				return
			}
			writeJSON(w, map[string]any{"template": item})
			return
		}
		if len(parts) == 2 && parts[1] == "runs" {
			switch r.Method {
			case http.MethodGet:
				version, valid := parseOptionalUint(r.URL.Query().Get("version"))
				if !valid {
					writeWorkflowError(w, service, &workflow.ValidationError{Field: "version", Message: "invalid version"})
					return
				}
				runs, err := service.Runs(r.Context(), scope, parts[0], version)
				if err != nil {
					writeWorkflowError(w, service, err)
					return
				}
				writeJSON(w, map[string]any{"items": runs})
			case http.MethodPost:
				var req workflowapp.StartRequest
				if !decodeWorkflowBody(w, r, &req) {
					return
				}
				run, created, err := service.StartRun(r.Context(), scope, parts[0], req, r.Header.Get("Idempotency-Key"))
				if err != nil {
					writeWorkflowError(w, service, err)
					return
				}
				status := http.StatusOK
				if created {
					status = http.StatusAccepted
				}
				writeJSONStatus(w, status, map[string]any{"run": run, "replayed": !created})
			default:
				writeWorkflowMethod(w)
			}
			return
		}
		http.NotFound(w, r)
	}
}
func workflowRunEndpoint(w http.ResponseWriter, r *http.Request, service *workflowapp.Service, scope workflow.Scope) {
	parts := workflowParts(r.URL.Path, "/tenant/workflow-runs")
	if len(parts) == 0 {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		run, err := service.Run(r.Context(), scope, id)
		if err != nil {
			writeWorkflowError(w, service, err)
			return
		}
		writeJSON(w, map[string]any{"run": run})
	case len(parts) == 2 && parts[1] == "cancel" && r.Method == http.MethodPost:
		run, err := service.Cancel(r.Context(), scope, id)
		if err != nil {
			writeWorkflowError(w, service, err)
			return
		}
		writeJSON(w, map[string]any{"run": run})
	case len(parts) == 2 && parts[1] == "recover" && r.Method == http.MethodPost:
		var req struct {
			Revision uint64 `json:"revision"`
		}
		if !decodeWorkflowBody(w, r, &req) {
			return
		}
		run, err := service.Recover(r.Context(), scope, id, req.Revision)
		if err != nil {
			writeWorkflowError(w, service, err)
			return
		}
		writeJSON(w, map[string]any{"run": run})
	case len(parts) >= 2 && parts[1] == "events" && r.Method == http.MethodGet:
		cursorText := r.URL.Query().Get("after_sequence")
		if cursorText == "" {
			cursorText = r.Header.Get("Last-Event-ID")
		}
		var after uint64
		var err error
		if cursorText != "" {
			after, err = strconv.ParseUint(cursorText, 10, 64)
			if err != nil {
				writeWorkflowError(w, service, &workflow.ValidationError{Field: "after_sequence", Message: "invalid event cursor"})
				return
			}
		}
		run, err := service.Run(r.Context(), scope, id)
		if err != nil {
			writeWorkflowError(w, service, err)
			return
		}
		if after > run.LastSequence {
			writeWorkflowError(w, service, &workflow.ValidationError{Field: "after_sequence", Message: "event cursor is ahead of this run"})
			return
		}
		if len(parts) == 3 && parts[2] == "stream" {
			streamWorkflow(w, r, service, scope, run, after)
			return
		}
		if len(parts) != 2 {
			http.NotFound(w, r)
			return
		}
		events, err := service.Events(r.Context(), scope, id, after, workflow.EventPageLimit)
		if err != nil {
			writeWorkflowError(w, service, err)
			return
		}
		cursor := after
		if len(events) > 0 {
			cursor = events[len(events)-1].Sequence
		}
		writeJSON(w, map[string]any{"events": events, "has_more": cursor < run.LastSequence, "last_sequence": cursor})
	default:
		writeWorkflowMethod(w)
	}
}
func streamWorkflow(w http.ResponseWriter, r *http.Request, service *workflowapp.Service, scope workflow.Scope, run workflow.Run, after uint64) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeWorkflowError(w, service, errors.New("streaming is unavailable"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	heartbeat := time.NewTicker(10 * time.Second)
	defer heartbeat.Stop()
	for {
		events, err := service.Events(r.Context(), scope, run.ID, after, workflow.EventPageLimit)
		if err != nil {
			return
		}
		for _, ev := range events {
			raw, err := json.Marshal(ev)
			if err != nil {
				return
			}
			if _, err = fmt.Fprintf(w, "id: %d\nevent: workflow\ndata: %s\n\n", ev.Sequence, raw); err != nil {
				return
			}
			after = ev.Sequence
		}
		if len(events) > 0 {
			flusher.Flush()
		}
		if latest, err := service.Run(r.Context(), scope, run.ID); err != nil {
			return
		} else {
			run = latest
		}
		if (workflow.Terminal(run.Status) || run.Status == workflow.Interrupted) && after >= run.LastSequence {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
func decodeWorkflowBody(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 256*1024)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		writeWorkflowError(w, nil, &workflow.ValidationError{Field: "request", Message: "invalid workflow request"})
		return false
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		writeWorkflowError(w, nil, &workflow.ValidationError{Field: "request", Message: "only one JSON request is allowed"})
		return false
	}
	return true
}
func writeWorkflowMethod(w http.ResponseWriter) {
	w.Header().Set("Allow", "GET, POST")
	writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]any{"code": "method_not_allowed", "message": "method not allowed"})
}
func writeWorkflowError(w http.ResponseWriter, service *workflowapp.Service, err error) {
	code, status, message := "workflow_unavailable", http.StatusServiceUnavailable, "Workflow service is temporarily unavailable"
	var validation *workflow.ValidationError
	switch {
	case errors.As(err, &validation):
		code, status, message = "workflow_validation", http.StatusUnprocessableEntity, validation.Error()
	case errors.Is(err, workflow.ErrNotFound) || errors.Is(err, mysqlstore.ErrNotFound):
		code, status, message = "workflow_not_found", http.StatusNotFound, "Workflow or Agent was not found in your workspace"
	case errors.Is(err, workflow.ErrConflict):
		code, status, message = "workflow_conflict", http.StatusConflict, "The run changed; refresh before trying again"
	case errors.Is(err, workflow.ErrIdempotencyConflict):
		code, status, message = "idempotency_conflict", http.StatusConflict, "This request key was already used with different input"
	case errors.Is(err, workflow.ErrCapacity):
		code, status, message = "workflow_capacity", http.StatusTooManyRequests, "Too many workflows are queued; wait or cancel a pending run"
	default:
		// Executor preflight errors are deliberately safe; do not echo DB or raw
		// provider errors merely because they implement error.
		type coded interface{ PublicCode() string }
		var c coded
		if errors.As(err, &c) {
			code = c.PublicCode()
			message = err.Error()
			status = http.StatusUnprocessableEntity
		}
	}
	if service != nil {
		message = service.Redact(message)
	} else {
		message = workflow.Redact(message)
	}
	writeJSONStatus(w, status, map[string]any{"code": code, "message": message, "error": message})
}

// The legacy message endpoint can execute an already-running task without a
// state update. Reject workflow ownership before it can reach that runner.
func rejectWorkflowTaskMutation(w http.ResponseWriter, r *http.Request, opts Options, taskID uint64) bool {
	if opts.WorkflowTaskOwner == nil {
		return false
	}
	_, owned, err := opts.WorkflowTaskOwner(r.Context(), taskID)
	if err != nil {
		writeWorkflowError(w, opts.WorkflowService, err)
		return true
	}
	if owned {
		writeJSONStatus(w, http.StatusConflict, map[string]any{"code": "workflow_owned_task", "error": "This task belongs to a Team workflow. Use the Run page to control it."})
		return true
	}
	return false
}
