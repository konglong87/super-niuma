package computeruse

import "context"

// ObserveRequest keeps observation inputs platform-neutral. A backend may use
// the requested display/window as a hint, but it must return the actual target
// in Observation and Capabilities.
type ObserveRequest struct {
	SessionID string `json:"session_id"`
	DisplayID string `json:"display_id,omitempty"`
	WindowID  string `json:"window_id,omitempty"`
}

// Backend is the single execution boundary for screenshots and host input.
// Implementations must not accept raw model prompts, provider credentials, or
// unvalidated action payloads.
type Backend interface {
	Capabilities(context.Context) (Capabilities, error)
	Observe(context.Context, ObserveRequest) (Observation, error)
	Execute(context.Context, Action) (ActionReceipt, error)
	Pause(context.Context) error
	Resume(context.Context) error
	Stop(context.Context) error
	Close(context.Context) error
}

// BackendFaultReporter exposes only a stable lifecycle code for a quarantined
// backend. It never exposes native diagnostics, paths, tokens, or user data.
// Runtime supervisors may use it to distinguish a normal user pause from a
// helper transport fault without probing the backend or mutating authority.
type BackendFaultReporter interface {
	RuntimeFaultCode() string
}

// BackendLauncher is the launch boundary used by a trusted host backend.
// Controller resolves TargetID before passing the real session ID and opaque
// provider key. A cold launch must never borrow identity from an observation.
type BackendLauncher interface {
	LaunchApp(context.Context, string, string) (LaunchReceipt, error)
}

// BackendApplicationLauncher is retained as a source-compatible alias.
type BackendApplicationLauncher = BackendLauncher
