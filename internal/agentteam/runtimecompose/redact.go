package runtimecompose

import (
	"regexp"
	"sort"
	"strings"
	"sync"
)

const Redacted = "[REDACTED]"

// Redactor holds credentials in memory only. The zero value is ready for use.
// Register and Redact may be called concurrently by API preflight and attempts.
type Redactor struct {
	mu      sync.RWMutex
	secrets []string
}

var credentialShapes = []*regexp.Regexp{
	regexp.MustCompile(`(?is)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`(?i)\b(?:sk|rk|pk)-(?:[a-z0-9_-]{8,})`),
	regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9_]{10,}|github_pat_[A-Za-z0-9_]{10,}|AKIA[A-Z0-9]{16}|ASIA[A-Z0-9]{16}|xox[baprs]-[A-Za-z0-9-]+)\b`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`),
	regexp.MustCompile(`(?i)(?:bearer|basic)\s+[A-Za-z0-9_+/=.~-]+`),
	regexp.MustCompile(`(?i)(?:["']?(?:api[_-]?key|auth[_-]?token|access[_-]?token|refresh[_-]?token|client[_-]?secret|password|passwd|authorization)["']?\s*[:=]\s*)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;}]+)`),
	regexp.MustCompile(`(?i)https?://[^\s/@]+:[^\s/@]+@`),
}

func (r *Redactor) Register(values ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := make(map[string]bool, len(r.secrets)+len(values))
	for _, s := range r.secrets {
		seen[s] = true
	}
	for _, s := range values {
		if s != "" && !seen[s] {
			r.secrets = append(r.secrets, s)
			seen[s] = true
		}
	}
	// Replace overlapping credentials longest-first, never revealing a suffix.
	sort.Slice(r.secrets, func(i, j int) bool { return len(r.secrets[i]) > len(r.secrets[j]) })
}

func (r *Redactor) Redact(text string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	// Match against the original string in one pass. Repeated ReplaceAll would
	// accidentally modify our replacement when a short credential is registered.
	if len(r.secrets) > 0 {
		pairs := make([]string, 0, 2*len(r.secrets))
		for _, secret := range r.secrets {
			pairs = append(pairs, secret, Redacted)
		}
		text = strings.NewReplacer(pairs...).Replace(text)
	}
	for _, shape := range credentialShapes {
		text = shape.ReplaceAllString(text, Redacted)
	}
	return text
}
