package verification

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/konglong87/super-niuma/internal/super"
)

const policyHashFormat = "super-verification-v1"

// ValidatePolicy rejects ambiguous paths and vacuous policies. A semantic rule
// is text_contains, json_keys, or sha256 with an independently trusted expected value.
// MaxBytes == 0 means DefaultMaxBytes; negative or excessive budgets are invalid.
func ValidatePolicy(p Policy) error {
	if p.Version == "" || len(p.Version) > MaxVersionBytes || strings.TrimSpace(p.Version) != p.Version || unsafeText(p.Version) {
		return invalid("version")
	}
	if len(p.Rules) == 0 || len(p.Rules) > MaxRules {
		return invalid("rule count")
	}
	seen := make(map[string]bool, len(p.Rules))
	var total int64
	semantic := false
	for i, r := range p.Rules {
		if !validPath(r.Path) {
			return invalid(fmt.Sprintf("rule %d path", i))
		}
		key := strings.ToLower(r.Path)
		if seen[key] {
			return invalid("duplicate path")
		}
		seen[key] = true
		if r.MaxBytes < 0 || r.MaxBytes > MaxFileBytes {
			return invalid("file budget")
		}
		total += effectiveMax(r)
		if total > MaxTotalBytes {
			return invalid("total byte budget")
		}
		switch r.Kind {
		case KindNonempty:
			if r.Expected != "" || len(r.Keys) != 0 {
				return invalid("nonempty arguments")
			}
		case KindTextContains:
			if strings.TrimSpace(r.Expected) == "" || !utf8.ValidString(r.Expected) || len(r.Expected) > MaxExpectedBytes || int64(len(r.Expected)) > effectiveMax(r) || len(r.Keys) != 0 {
				return invalid("text arguments")
			}
			semantic = true
		case KindJSONKeys:
			if r.Expected != "" || len(r.Keys) == 0 || len(r.Keys) > MaxKeys {
				return invalid("json arguments")
			}
			keys := make(map[string]bool, len(r.Keys))
			for _, key := range r.Keys {
				if strings.TrimSpace(key) == "" || len(key) > MaxKeyBytes || unsafeText(key) || keys[key] {
					return invalid("json keys")
				}
				keys[key] = true
			}
			semantic = true
		case KindSHA256:
			if len(r.Expected) != sha256.Size*2 || len(r.Keys) != 0 {
				return invalid("sha256 arguments")
			}
			if _, err := hex.DecodeString(r.Expected); err != nil {
				return invalid("sha256 encoding")
			}
			semantic = true
		default:
			return invalid("rule kind")
		}
	}
	if !semantic {
		return invalid("at least one semantic assertion required")
	}
	return nil
}

// PolicyHash returns a canonical SHA-256 digest, or "" for an invalid policy.
// Rule/key order, an explicit default size, and SHA hex case do not change it.
// Caller-owned slices are never sorted or otherwise modified.
func PolicyHash(p Policy) string {
	if ValidatePolicy(p) != nil {
		return ""
	}
	canonical := Policy{Version: p.Version, Rules: make([]Rule, len(p.Rules))}
	for i, r := range p.Rules {
		r.MaxBytes = effectiveMax(r)
		r.Keys = append([]string(nil), r.Keys...)
		sort.Strings(r.Keys)
		if r.Kind == KindSHA256 {
			r.Expected = strings.ToLower(r.Expected)
		}
		canonical.Rules[i] = r
	}
	sort.Slice(canonical.Rules, func(i, j int) bool { return canonical.Rules[i].Path < canonical.Rules[j].Path })
	data, _ := json.Marshal(struct {
		Format string `json:"format"`
		Policy Policy `json:"policy"`
	}{policyHashFormat, canonical})
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func effectiveMax(r Rule) int64 {
	if r.MaxBytes == 0 {
		return DefaultMaxBytes
	}
	return r.MaxBytes
}
func invalid(reason string) error { return fmt.Errorf("%w: %s", ErrInvalidPolicy, reason) }
func unsafeText(s string) bool {
	return !utf8.ValidString(s) || strings.IndexFunc(s, unicode.IsControl) >= 0
}
func validPath(name string) bool {
	if name == "" || len(name) > MaxPathBytes || name == "." || path.IsAbs(name) || path.Clean(name) != name || unsafeText(name) || strings.ContainsAny(name, "\\:") {
		return false
	}
	parts := strings.Split(name, "/")
	if len(parts) > MaxPathDepth {
		return false
	}
	for _, part := range parts {
		if part == ".." || strings.TrimSpace(part) != part || strings.HasSuffix(part, ".") || reservedComponent(part) {
			return false
		}
	}
	return true
}
func reservedComponent(part string) bool {
	part = strings.ToLower(part)
	return part == ".git" || part == super.StateDirectoryName || part == super.DatabaseName || strings.HasPrefix(part, super.DatabaseName+"-") || strings.HasPrefix(part, super.DatabaseName+".")
}
