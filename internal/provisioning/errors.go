package provisioning

import "fmt"

type ErrorCode string

const (
	ErrInvalidTransition ErrorCode = "invalid_transition"
	ErrInvalidInput      ErrorCode = "invalid_input"
	ErrNotFound          ErrorCode = "not_found"
	ErrConflict          ErrorCode = "conflict"
	ErrPreflight         ErrorCode = "preflight_failed"
	ErrSupervisor        ErrorCode = "supervisor_failed"
)

type ProvisionError struct {
	Code      ErrorCode `json:"code"`
	Message   string    `json:"message"`
	Retryable bool      `json:"retryable"`
}

func (e *ProvisionError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}
func NewError(code ErrorCode, message string) *ProvisionError {
	return &ProvisionError{Code: code, Message: message}
}
