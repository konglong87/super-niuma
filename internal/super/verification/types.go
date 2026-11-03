// Package verification provides bounded, read-only artifact verification.
// Policies must come from the control plane, never from the worker being verified.
package verification

import "errors"

const (
	KindNonempty     = "nonempty"
	KindTextContains = "text_contains"
	KindJSONKeys     = "json_keys"
	KindSHA256       = "sha256"

	MaxRules               = 32
	MaxVersionBytes        = 128
	MaxPathBytes           = 1024
	MaxPathDepth           = 32
	MaxExpectedBytes       = 64 << 10
	MaxKeys                = 64
	MaxKeyBytes            = 256
	DefaultMaxBytes  int64 = 1 << 20
	MaxFileBytes     int64 = 8 << 20
	MaxTotalBytes    int64 = 32 << 20
	MaxJSONDepth           = 64
	MaxJSONTokens          = 100000

	CodeOK           = "ok"
	CodeInvalidRule  = "invalid_rule"
	CodeEmpty        = "empty"
	CodeTextMissing  = "text_missing"
	CodeJSONInvalid  = "invalid_json"
	CodeKeysMissing  = "json_keys_missing"
	CodeHashMismatch = "sha256_mismatch"
	CodeSymlink      = "symlink_rejected"
	CodeNotRegular   = "not_regular"
	CodeTooLarge     = "file_too_large"
	CodeMissing      = "file_missing"
	CodeReadFailed   = "read_failed"
	CodeChanged      = "file_changed"
	CodeCancelled    = "cancelled"
)

var (
	ErrInvalidPolicy       = errors.New("verification: invalid policy")
	ErrInvalidWorkspace    = errors.New("verification: invalid workspace")
	ErrUnsupportedPlatform = errors.New("verification: secure file opening unavailable on this platform")
)

type Policy struct {
	Version string `json:"version"`
	Rules   []Rule `json:"rules"`
}

type Rule struct {
	Path     string   `json:"path"`
	Kind     string   `json:"kind"`
	Expected string   `json:"expected,omitempty"`
	Keys     []string `json:"keys,omitempty"`
	MaxBytes int64    `json:"max_bytes,omitempty"`
}

type Report struct {
	Passed       bool       `json:"passed"`
	PolicyHash   string     `json:"policy_hash"`
	Artifacts    []Artifact `json:"artifacts"`
	Checks       []Check    `json:"checks"`
	VerifiedAtMS int64      `json:"verified_at_ms"`
}

type Artifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type Check struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Passed bool   `json:"passed"`
	Code   string `json:"code"`
}
