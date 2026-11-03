package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/pendinginput"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	tenantservice "github.com/konglong87/super-niuma/internal/tenant"
)

type pendingInputRequest struct {
	ClientInputID string                          `json:"client_input_id"`
	Content       string                          `json:"content"`
	Direction     string                          `json:"direction,omitempty"`
	Attachments   []pendingInputAttachmentRequest `json:"attachments,omitempty"`
}

type pendingInputAttachmentRequest struct {
	AttachmentID string `json:"attachment_id,omitempty"`
	Type         string `json:"type"`
	MediaType    string `json:"media_type,omitempty"`
	Name         string `json:"name,omitempty"`
	URL          string `json:"url,omitempty"`
	SizeBytes    int64  `json:"size_bytes,omitempty"`
	SHA256       string `json:"sha256,omitempty"`
	InlineData   string `json:"inline_data,omitempty"`
}

type pendingInputPatchRequest struct {
	Content   *string `json:"content"`
	Direction *string `json:"direction"`
}

type pendingInputSettingsRequest struct {
	Enabled *bool `json:"enabled"`
}

func tenantPendingInputsHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		if opts.PendingInputQueue == nil {
			writeTenantError(w, http.StatusServiceUnavailable, "pending input queue is not configured")
			return
		}
		taskID, inputID, action, ok := pendingInputPath(r.URL.Path)
		if !ok || taskID == 0 {
			writeTenantError(w, http.StatusBadRequest, "agent task id is required")
			return
		}
		task, scope, err := pendingInputScope(r, opts, taskID)
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		if action == "pending-input-settings" {
			handlePendingInputSettings(w, r, opts, task, scope)
			return
		}
		if inputID == "" {
			handlePendingInputCollection(w, r, opts, task, scope)
			return
		}
		handlePendingInputItem(w, r, opts, task, scope, inputID, action)
	})
}

func handlePendingInputCollection(w http.ResponseWriter, r *http.Request, opts Options, task mysqlstore.AgentTask, scope pendinginput.Scope) {
	switch r.Method {
	case http.MethodGet:
		items, err := opts.PendingInputQueue.List(r.Context(), scope)
		if err != nil {
			writePendingInputError(w, err)
			return
		}
		writeJSON(w, map[string]any{"data": items})
	case http.MethodPost:
		var req pendingInputRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeTenantError(w, http.StatusBadRequest, err.Error())
			return
		}
		attachments, err := pendingInputAttachments(req.Attachments)
		if err != nil {
			writeTenantError(w, http.StatusBadRequest, err.Error())
			return
		}
		item, err := opts.PendingInputQueue.Add(r.Context(), pendinginput.NewInput{
			Scope:         scope,
			ClientInputID: firstNonEmptyString(req.ClientInputID, newWebAgentTraceID()),
			Content:       req.Content,
			Direction:     req.Direction,
			Attachments:   attachments,
		})
		if err != nil {
			writePendingInputError(w, err)
			return
		}
		appendPendingInputEventWithOptions(r, opts, scope.BaseTaskID, agenttasks.EventInputQueued, item)
		if task.Status != agenttasks.StatusRunning && opts.pendingInputCoordinator != nil {
			opts.pendingInputCoordinator.trigger(r.Context(), scope)
		}
		writeJSONStatus(w, http.StatusAccepted, map[string]any{"data": item})
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func pendingInputAttachments(items []pendingInputAttachmentRequest) ([]agenttasks.Attachment, error) {
	if len(items) == 0 {
		return nil, nil
	}
	if len(items) > maxAgentTaskAttachments {
		return nil, fmt.Errorf("too many attachments: max %d", maxAgentTaskAttachments)
	}
	out := make([]agenttasks.Attachment, 0, len(items))
	for _, item := range items {
		attachment, err := normalizeAgentTaskAttachment(agentTaskAttachmentRequest{AttachmentID: item.AttachmentID, Type: item.Type, MediaType: item.MediaType, Name: item.Name, URL: item.URL, SizeBytes: item.SizeBytes, SHA256: item.SHA256, InlineData: item.InlineData})
		if err != nil {
			return nil, err
		}
		out = append(out, attachment)
	}
	return out, nil
}

func handlePendingInputItem(w http.ResponseWriter, r *http.Request, opts Options, task mysqlstore.AgentTask, scope pendinginput.Scope, inputID, action string) {
	_ = task
	if !pendingInputExists(r.Context(), opts.PendingInputQueue, scope, inputID) {
		writeTenantError(w, http.StatusNotFound, "pending input not found")
		return
	}
	switch action {
	case "move-up":
		if r.Method != http.MethodPost {
			writeTenantError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		item, err := opts.PendingInputQueue.MoveUp(r.Context(), inputID)
		if err != nil {
			writePendingInputError(w, err)
			return
		}
		appendPendingInputEventWithOptions(r, opts, scope.BaseTaskID, agenttasks.EventInputReordered, item)
		writeJSON(w, map[string]any{"data": item})
	case "direction":
		if r.Method != http.MethodPatch {
			writeTenantError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req pendingInputPatchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeTenantError(w, http.StatusBadRequest, err.Error())
			return
		}
		if req.Direction == nil {
			writeTenantError(w, http.StatusBadRequest, "direction is required")
			return
		}
		item, err := opts.PendingInputQueue.Update(r.Context(), inputID, pendinginput.UpdateInput{Direction: req.Direction})
		if err != nil {
			writePendingInputError(w, err)
			return
		}
		appendPendingInputEventWithOptions(r, opts, scope.BaseTaskID, agenttasks.EventInputUpdated, item)
		writeJSON(w, map[string]any{"data": item})
	case "retry":
		if r.Method != http.MethodPost {
			writeTenantError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		item, err := opts.PendingInputQueue.Retry(r.Context(), inputID)
		if err != nil {
			writePendingInputError(w, err)
			return
		}
		appendPendingInputEventWithOptions(r, opts, scope.BaseTaskID, agenttasks.EventInputRetried, item)
		if task.Status != agenttasks.StatusRunning && opts.pendingInputCoordinator != nil {
			opts.pendingInputCoordinator.trigger(r.Context(), scope)
		}
		writeJSON(w, map[string]any{"data": item})
	case "side-chat":
		if r.Method != http.MethodPost {
			writeTenantError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		handlePendingInputSideChat(w, r, opts, task, scope, inputID)
	case "":
		handlePendingInputMutation(w, r, opts, scope, inputID)
	default:
		writeTenantError(w, http.StatusNotFound, "pending input action not found")
	}
}

func pendingInputExists(ctx context.Context, queue pendinginput.Queue, scope pendinginput.Scope, id string) bool {
	items, err := queue.List(ctx, scope)
	if err != nil {
		return false
	}
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}

func handlePendingInputMutation(w http.ResponseWriter, r *http.Request, opts Options, scope pendinginput.Scope, inputID string) {
	switch r.Method {
	case http.MethodPatch:
		var req pendingInputPatchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeTenantError(w, http.StatusBadRequest, err.Error())
			return
		}
		item, err := opts.PendingInputQueue.Update(r.Context(), inputID, pendinginput.UpdateInput{Content: req.Content, Direction: req.Direction})
		if err != nil {
			writePendingInputError(w, err)
			return
		}
		appendPendingInputEventWithOptions(r, opts, scope.BaseTaskID, agenttasks.EventInputUpdated, item)
		writeJSON(w, map[string]any{"data": item})
	case http.MethodDelete:
		if err := opts.PendingInputQueue.Cancel(r.Context(), inputID); err != nil {
			writePendingInputError(w, err)
			return
		}
		appendPendingInputEventWithOptions(r, opts, scope.BaseTaskID, agenttasks.EventInputCancelled, map[string]any{"input_id": inputID})
		writeJSON(w, map[string]any{"id": inputID, "status": pendinginput.StatusCancelled})
	default:
		w.Header().Set("Allow", "PATCH, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func handlePendingInputSettings(w http.ResponseWriter, r *http.Request, opts Options, task mysqlstore.AgentTask, scope pendinginput.Scope) {
	queue := opts.PendingInputQueue
	switch r.Method {
	case http.MethodGet:
		enabled, err := queue.QueueEnabled(r.Context(), scope)
		if err != nil {
			writePendingInputError(w, err)
			return
		}
		writeJSON(w, map[string]any{"enabled": enabled})
	case http.MethodPatch:
		var req pendingInputSettingsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Enabled == nil {
			writeTenantError(w, http.StatusBadRequest, "enabled is required")
			return
		}
		if err := queue.SetQueueEnabled(r.Context(), scope, *req.Enabled); err != nil {
			writePendingInputError(w, err)
			return
		}
		if *req.Enabled && task.Status != agenttasks.StatusRunning && opts.pendingInputCoordinator != nil {
			opts.pendingInputCoordinator.trigger(r.Context(), scope)
		}
		writeJSON(w, map[string]any{"enabled": *req.Enabled})
	default:
		w.Header().Set("Allow", "GET, PATCH")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func handlePendingInputSideChat(w http.ResponseWriter, r *http.Request, opts Options, task mysqlstore.AgentTask, scope pendinginput.Scope, inputID string) {
	items, err := opts.PendingInputQueue.List(r.Context(), scope)
	if err != nil {
		writePendingInputError(w, err)
		return
	}
	var item *pendinginput.PendingInput
	for i := range items {
		if items[i].ID == inputID {
			copy := items[i]
			item = &copy
			break
		}
	}
	if item == nil {
		writeTenantError(w, http.StatusNotFound, "pending input not found")
		return
	}
	if opts.TenantService == nil || scope.BaseTaskID == 0 {
		writeTenantError(w, http.StatusServiceUnavailable, "side chat requires tenant session storage")
		return
	}
	if existing, ok := existingPendingInputSideChat(r.Context(), opts.TenantService, item.ID); ok {
		writeJSON(w, map[string]any{"session_id": existing.ParentSessionID, "task_id": existing.ID, "source_pending_input_id": item.ID})
		return
	}
	content := composePendingInputContent(*item)
	metadataValues := parseAgentTaskJSONMap(task.MetadataJSON)
	delete(metadataValues, "continuation_of_task_id")
	delete(metadataValues, "pending_input_id")
	delete(metadataValues, "pending_input_attempt")
	delete(metadataValues, "pending_input_base_task_id")
	metadataValues["source"] = agenttasks.SourcePendingInputSideChat
	metadataValues["source_pending_input_id"] = item.ID
	metadata, _ := json.Marshal(metadataValues)
	result, err := opts.TenantService.ForkSession(r.Context(), tenantservice.ForkSessionRequest{
		Session:  tenantservice.SessionRequest{SessionKey: fmt.Sprintf("pending-side-chat-%s", item.ID), Title: "Pending input side chat", Status: "active", Model: task.Model, CWD: agentTaskStringValue(metadataValues["cwd"]), MetadataJSON: string(metadata)},
		Messages: []tenantservice.MessageRequest{{Role: "user", Content: content, TurnIndex: 1}},
	})
	if err != nil {
		writeTenantServiceError(w, err)
		return
	}
	metadataValues["web_agent_session_id"] = result.SessionID
	task.TenantID, task.UserID = scope.TenantID, scope.UserID
	attachments, err := cloneSessionControlAttachments(r.Context(), opts, task, result.SessionID, item.Attachments)
	if err != nil {
		writeTenantServiceError(w, err)
		return
	}
	metadataValues["web_agent_session_key"] = fmt.Sprintf("pending-side-chat-%s", item.ID)
	taskMetadata, _ := json.Marshal(metadataValues)
	traceID := firstNonEmptyString(task.TraceID, newWebAgentTraceID())
	taskID, err := opts.TenantService.CreateAgentTask(r.Context(), agenttasks.TaskInput{
		ParentSessionID: result.SessionID,
		AgentName:       firstNonEmptyString(task.AgentName, "web-agent"),
		Description:     "Pending input side chat",
		Prompt:          content,
		Status:          agenttasks.StatusReady,
		Model:           task.Model,
		MetadataJSON:    string(taskMetadata),
		TraceID:         traceID,
	})
	if err != nil {
		writeTenantServiceError(w, err)
		return
	}
	messagePayload, err := json.Marshal(agenttasks.MessageInput{TaskID: taskID, FromAgent: "webui", Content: content, TraceID: traceID, Attachments: attachments})
	if err != nil {
		writeTenantError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := appendAgentTaskEventByID(r.Context(), opts, taskID, agenttasks.EventInput{TaskID: taskID, EventType: agenttasks.EventMessage, PayloadJSON: string(messagePayload), TraceID: traceID}); err != nil {
		_ = opts.TenantService.FinishAgentTask(context.WithoutCancel(r.Context()), taskID, agenttasks.StatusFailed, agentTaskEventPayload(map[string]any{"source": "pending-input-side-chat", "error_code": "side_chat_message_failed"}))
		writeTenantServiceError(w, err)
		return
	}
	writeJSON(w, map[string]any{"session_id": result.SessionID, "task_id": taskID, "source_pending_input_id": item.ID})
}

func existingPendingInputSideChat(ctx context.Context, svc TenantService, inputID string) (mysqlstore.AgentTask, bool) {
	items, err := svc.ListAgentTasks(ctx, 200)
	if err != nil {
		return mysqlstore.AgentTask{}, false
	}
	for _, item := range items {
		metadata := parseAgentTaskJSONMap(item.MetadataJSON)
		if agentTaskStringValue(metadata["source"]) == "pending-input-side-chat" && agentTaskStringValue(metadata["source_pending_input_id"]) == inputID {
			return item, true
		}
	}
	return mysqlstore.AgentTask{}, false
}

func composePendingInputContent(item pendinginput.PendingInput) string {
	if strings.TrimSpace(item.Direction) == "" {
		return item.Content
	}
	return "[方向]\n" + item.Direction + "\n\n[原消息]\n" + item.Content
}

func pendingInputScope(r *http.Request, opts Options, taskID uint64) (mysqlstore.AgentTask, pendinginput.Scope, error) {
	task, err := opts.TenantService.GetAgentTask(r.Context(), taskID)
	if err != nil {
		return mysqlstore.AgentTask{}, pendinginput.Scope{}, err
	}
	identity, err := opts.TenantService.ResolveContext(r.Context())
	if err != nil {
		return mysqlstore.AgentTask{}, pendinginput.Scope{}, err
	}
	scope := pendingInputScopeForTask(task)
	if scope.TenantID == 0 {
		scope.TenantID = identity.TenantID
	}
	if scope.UserID == 0 {
		scope.UserID = identity.UserID
	}
	return task, scope, nil
}

func pendingInputPath(path string) (taskID uint64, inputID, action string, ok bool) {
	raw := strings.TrimPrefix(path, "/tenant/agent-tasks/")
	if raw == path {
		return 0, "", "", false
	}
	parts := strings.Split(strings.Trim(raw, "/"), "/")
	if len(parts) < 2 || parts[1] != "pending-inputs" {
		if len(parts) == 2 && parts[1] == "pending-input-settings" {
			taskID, err := strconv.ParseUint(parts[0], 10, 64)
			return taskID, "", "pending-input-settings", err == nil
		}
		return 0, "", "", false
	}
	taskID, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return 0, "", "", false
	}
	if len(parts) == 2 {
		return taskID, "", "", true
	}
	inputID = parts[2]
	if len(parts) >= 4 {
		action = parts[3]
	}
	return taskID, inputID, action, inputID != ""
}

func appendPendingInputEvent(r *http.Request, svc TenantService, taskID uint64, eventType string, payload any) {
	appendPendingInputEventContext(r.Context(), svc, taskID, eventType, payload)
}

func appendPendingInputEventWithOptions(r *http.Request, opts Options, taskID uint64, eventType string, payload any) {
	appendPendingInputEventContextWithOptions(r.Context(), opts, taskID, eventType, payload)
}

func appendPendingInputEventContext(ctx context.Context, svc TenantService, taskID uint64, eventType string, payload any) {
	appendPendingInputEventContextWithOptions(ctx, Options{TenantService: svc}, taskID, eventType, payload)
}

func appendPendingInputEventContextWithOptions(ctx context.Context, opts Options, taskID uint64, eventType string, payload any) {
	if opts.TenantService == nil || taskID == 0 {
		return
	}
	data, err := json.Marshal(sanitizePendingInputEventPayload(payload))
	if err != nil {
		return
	}
	_, _ = appendAgentTaskEventByID(ctx, opts, taskID, agenttasks.EventInput{TaskID: taskID, EventType: eventType, PayloadJSON: string(data)})
}

func sanitizePendingInputEventPayload(payload any) map[string]any {
	out := map[string]any{}
	switch value := payload.(type) {
	case pendinginput.PendingInput:
		out["input_id"] = value.ID
		out["status"] = value.Status
		out["sequence"] = value.Sequence
		out["attempt"] = value.Attempt
		if value.DispatchedTaskID > 0 {
			out["dispatched_task_id"] = value.DispatchedTaskID
		}
		if value.ErrorCode != "" {
			out["error_code"] = value.ErrorCode
		}
	case *pendinginput.PendingInput:
		if value != nil {
			return sanitizePendingInputEventPayload(*value)
		}
	case map[string]any:
		for _, key := range []string{"input_id", "status", "sequence", "attempt", "dispatched_task_id", "error_code"} {
			if item, ok := value[key]; ok {
				out[key] = item
			}
		}
	}
	return out
}

func writePendingInputError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, pendinginput.ErrInvalid):
		writeTenantError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, pendinginput.ErrNotFound):
		writeTenantError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, pendinginput.ErrQueueFull):
		writeTenantError(w, http.StatusTooManyRequests, err.Error())
	case errors.Is(err, pendinginput.ErrQueueDisabled), errors.Is(err, pendinginput.ErrConflict):
		writeTenantError(w, http.StatusConflict, err.Error())
	default:
		writeTenantServiceError(w, err)
	}
}
