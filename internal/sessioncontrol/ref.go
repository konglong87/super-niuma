package sessioncontrol

import (
	"fmt"
	"strings"
	"unicode"
)

type Source string

const (
	SourceTenant Source = "tenant"
	SourceLocal  Source = "local"
)

type RefErrorCode string

const CodeInvalidRef RefErrorCode = "invalid_ref"

type RefError struct {
	Code    RefErrorCode
	Message string
}

func (e *RefError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

type SessionRef struct {
	Source Source
	Key    string
}

func ParseRef(raw string) (SessionRef, error) {
	raw = strings.TrimSpace(raw)
	namespace, key, ok := strings.Cut(raw, ":")
	if !ok || namespace == "" || key == "" {
		return SessionRef{}, invalidRef("session ref must be <tenant|local>:<key>")
	}
	var source Source
	switch strings.ToLower(namespace) {
	case string(SourceTenant):
		source = SourceTenant
	case string(SourceLocal):
		source = SourceLocal
	default:
		return SessionRef{}, invalidRef("unknown session ref namespace")
	}
	for _, r := range key {
		if unicode.IsSpace(r) || unicode.IsControl(r) || r == '/' || r == '\\' || (r == ':' && !strings.HasPrefix(key, "channel:")) {
			return SessionRef{}, invalidRef("session ref key contains an invalid character")
		}
	}
	return SessionRef{Source: source, Key: key}, nil
}

func (r SessionRef) String() string {
	return string(r.Source) + ":" + r.Key
}

func invalidRef(message string) error {
	return &RefError{Code: CodeInvalidRef, Message: message}
}
