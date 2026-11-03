package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/konglong87/super-niuma/internal/super/workorder"
)

const (
	codeForbidden      = "forbidden"
	codeInvalidRequest = "invalid_request"
	codeInvalidInput   = "invalid_input"
	codeNotFound       = "not_found"
	codeConflict       = "idempotency_conflict"
	codeMethod         = "method_not_allowed"
	codeMedia          = "unsupported_media_type"
	codeBody           = "body_too_large"
	codeUnavailable    = "service_unavailable"
	codeCancelled      = "request_cancelled"
	codeTimeout        = "request_timeout"
	codeInternal       = "internal_error"
)

type response struct {
	status int
	body   any
	allow  string
	code   string
}
type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type errorBody struct {
	Error errorDetail `json:"error"`
}

func failure(code string) response {
	status, message := http.StatusInternalServerError, "Request could not be completed."
	switch code {
	case codeForbidden:
		status, message = http.StatusForbidden, "Request not allowed."
	case codeInvalidRequest:
		status, message = http.StatusBadRequest, "Invalid request shape or JSON object."
	case codeInvalidInput:
		status, message = http.StatusBadRequest, "Invalid work-order input."
	case codeNotFound:
		status, message = http.StatusNotFound, "Work order or route not found."
	case codeConflict:
		status, message = http.StatusConflict, "Idempotency key conflicts with an existing intake."
	case codeMethod:
		status, message = http.StatusMethodNotAllowed, "Method not allowed."
	case codeMedia:
		status, message = http.StatusUnsupportedMediaType, "Use application/json without content encoding."
	case codeBody:
		status, message = http.StatusRequestEntityTooLarge, "Request body exceeds the size limit."
	case codeUnavailable:
		status, message = http.StatusServiceUnavailable, "Service temporarily unavailable."
	case codeCancelled:
		status, message = http.StatusRequestTimeout, "Request was cancelled."
	case codeTimeout:
		status, message = http.StatusRequestTimeout, "Request timed out."
	}
	return response{status: status, code: code, body: errorBody{Error: errorDetail{Code: code, Message: message}}}
}

func serviceFailure(err error) response {
	switch {
	case errors.Is(err, workorder.ErrInvalidInput):
		return failure(codeInvalidInput)
	case errors.Is(err, workorder.ErrNotFound):
		return failure(codeNotFound)
	case errors.Is(err, workorder.ErrIdempotencyConflict):
		return failure(codeConflict)
	case errors.Is(err, context.Canceled):
		return failure(codeCancelled)
	case errors.Is(err, context.DeadlineExceeded):
		return failure(codeTimeout)
	case errors.Is(err, workorder.ErrUnavailable):
		return failure(codeUnavailable)
	default:
		return failure(codeInternal)
	}
}

func (h *handler) write(w http.ResponseWriter, r *http.Request, result response) {
	data, err := json.Marshal(result.body)
	if err != nil {
		result = failure(codeInternal)
		data, _ = json.Marshal(result.body)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if result.allow != "" {
		w.Header().Set("Allow", result.allow)
	}
	w.WriteHeader(result.status)
	_, err = w.Write(append(data, '\n'))
	// Raw method/path/headers/body and backend errors never enter logs.
	h.logger.DebugContext(r.Context(), "Super API request handled", "action", "http.request", "status", result.status, "code", result.code)
	if err != nil {
		h.logger.WarnContext(r.Context(), "Super API response unavailable", "action", "http.response_write_failed")
	}
}
