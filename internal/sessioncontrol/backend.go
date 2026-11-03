package sessioncontrol

import (
	"fmt"
	"strings"
)

// SessionBackend selects the authoritative chat storage for one desktop
// process. The selection is intentionally process-wide and startup-only.
type SessionBackend string

const (
	SessionBackendJSONL  SessionBackend = "jsonl"
	SessionBackendSQLite SessionBackend = "sqlite"
)

func (b SessionBackend) String() string {
	return string(b)
}

func (b SessionBackend) Valid() bool {
	return b == SessionBackendJSONL || b == SessionBackendSQLite
}

// ParseSessionBackend defaults to JSONL. SQLite remains available only as an
// explicit rollback/validation mode.
func ParseSessionBackend(value string) (SessionBackend, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", string(SessionBackendJSONL):
		return SessionBackendJSONL, nil
	case string(SessionBackendSQLite):
		return SessionBackendSQLite, nil
	default:
		return "", fmt.Errorf("unsupported session backend %q; want jsonl or sqlite", value)
	}
}
