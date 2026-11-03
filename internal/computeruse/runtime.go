package computeruse

import "time"

// RuntimeState is the lifecycle state of the long-lived Computer Use runtime,
// independent from any Wails window or panel client. A runtime in a terminal
// state must not be treated as an authority for new input.
type RuntimeState string

const (
	RuntimeStarting RuntimeState = "starting"
	RuntimeReady    RuntimeState = "ready"
	RuntimeDegraded RuntimeState = "degraded"
	RuntimeStopping RuntimeState = "stopping"
	RuntimeStopped  RuntimeState = "stopped"
	RuntimeFaulted  RuntimeState = "faulted"
)

func (s RuntimeState) Valid() bool {
	switch s {
	case RuntimeStarting, RuntimeReady, RuntimeDegraded, RuntimeStopping, RuntimeStopped, RuntimeFaulted:
		return true
	default:
		return false
	}
}

const (
	RuntimeFaultWorkerExited       = "worker_exited"
	RuntimeFaultBridgeDisconnected = "bridge_disconnected"
	RuntimeFaultHelperUnavailable  = ErrorCodeHelperUnavailable
	RuntimeFaultPermissionRevoked  = "permission_revoked"
	RuntimeFaultHelperUncertain    = "helper_outcome_uncertain"
	RuntimeFaultStopFailed         = "stop_failed"
)

// RuntimeFault is a stable, redacted description of a runtime lifecycle fault.
// It intentionally contains no native error text, paths, tokens, or credentials.
type RuntimeFault struct {
	State       RuntimeState `json:"state"`
	ErrorCode   string       `json:"error_code"`
	Recoverable bool         `json:"recoverable"`
	SessionID   string       `json:"session_id,omitempty"`
	ActionID    string       `json:"action_id,omitempty"`
	OccurredAt  time.Time    `json:"occurred_at"`
}

// RuntimeStatus is a read-only lifecycle snapshot. It never grants authority
// and is safe to expose to a disconnected UI client or provider adapter.
type RuntimeStatus struct {
	State         RuntimeState  `json:"state"`
	Fault         *RuntimeFault `json:"fault,omitempty"`
	ActiveSession bool          `json:"active_session"`
	SessionID     string        `json:"session_id,omitempty"`
}
