// Package procenv builds the environment handed to child processes.
//
// Every place that spawns a subprocess used to pass the parent environment
// through untouched, except the Bash tool, which stripped a short hard-coded
// list of provider variables. Hooks, PowerShell, Workflow, the WebBrowser
// adapter and stdio MCP servers all leaked whatever credentials happened to be
// exported (AUDIT-P1-16). Hooks in particular can come from a project-level
// .claude/settings.json, so cloning a repository was enough to hand an attacker
// every key in the environment.
//
// This package is the single sanitiser they all share.
package procenv

import (
	"os"
	"strings"
)

// providerSecretEnvKeys are exact names stripped regardless of shape. Beyond the
// credentials themselves this covers provider routing (base URL, model, provider
// selection), which a child process has no business inheriting or overriding.
var providerSecretEnvKeys = map[string]struct{}{
	"ANTHROPIC_API_KEY":       {},
	"ANTHROPIC_AUTH_TOKEN":    {},
	"ANTHROPIC_BASE_URL":      {},
	"CLAUDE_CODE_AUTH_TOKEN":  {},
	"CLAUDE_CODE_OAUTH_TOKEN": {},
	"CLAUDE_CODE_MODEL":       {},
	"CLAUDE_CODE_PROVIDER":    {},
	"NIUMA_PROVIDER":          {},
	"TOKEN":                   {},
	"PASSWORD":                {},
	"SECRET":                  {},
}

// A name-shaped denylist rather than an allowlist of permitted variables: child
// processes legitimately need PATH, HOME, GOPATH, GOCACHE, CI and whatever else
// a hook or workflow step depends on, so enumerating what may pass is not
// workable. Matching on shape instead catches GITHUB_TOKEN, AWS_SECRET_ACCESS_KEY
// and OPENAI_API_KEY — all of which used to slip through the exact-name list —
// along with credentials that do not exist yet.
var (
	secretKeySubstrings = []string{"SECRET", "PASSWORD", "PASSWD", "CREDENTIAL"}
	secretKeySuffixes   = []string{"_TOKEN", "_KEY"}
)

// IsSecretKey reports whether an environment variable name looks like a credential.
func IsSecretKey(key string) bool {
	key = strings.ToUpper(strings.TrimSpace(key))
	if key == "" {
		return false
	}
	if _, found := providerSecretEnvKeys[key]; found {
		return true
	}
	for _, fragment := range secretKeySubstrings {
		if strings.Contains(key, fragment) {
			return true
		}
	}
	for _, suffix := range secretKeySuffixes {
		if strings.HasSuffix(key, suffix) {
			return true
		}
	}
	return false
}

// Sanitized returns the parent environment with credential-shaped variables
// removed, then applies extra on top.
//
// extra is applied verbatim and is never filtered: it carries values the caller
// set deliberately — an MCP server's configured env, a sandbox spec — where a
// credential is the point rather than an accident.
func Sanitized(extra ...string) []string {
	parent := os.Environ()
	env := make([]string, 0, len(parent)+len(extra))
	for _, item := range parent {
		key, _, ok := strings.Cut(item, "=")
		if !ok || IsSecretKey(key) {
			continue
		}
		env = Upsert(env, item)
	}
	for _, item := range extra {
		env = Upsert(env, item)
	}
	return env
}

// Upsert replaces any existing entry for value's key, otherwise appends it.
func Upsert(env []string, value string) []string {
	key, _, ok := strings.Cut(value, "=")
	if !ok || strings.TrimSpace(key) == "" {
		return env
	}
	for i := len(env) - 1; i >= 0; i-- {
		existing, _, valid := strings.Cut(env[i], "=")
		if valid && existing == key {
			env[i] = value
			return env
		}
	}
	return append(env, value)
}
