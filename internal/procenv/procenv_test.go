package procenv

import (
	"slices"
	"testing"
)

func TestIsSecretKeyMatchesCredentialShapes(t *testing.T) {
	secrets := []string{
		"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL",
		"CLAUDE_CODE_OAUTH_TOKEN", "NIUMA_PROVIDER",
		// The three the old exact-name denylist let through.
		"GITHUB_TOKEN", "AWS_SECRET_ACCESS_KEY", "OPENAI_API_KEY",
		// Shapes nobody enumerated.
		"GH_TOKEN", "NPM_TOKEN", "STRIPE_SECRET_KEY", "MY_DB_PASSWORD",
		"GOOGLE_APPLICATION_CREDENTIALS", "SSH_PRIVATE_KEY", "MYSQL_PASSWD",
		"TOKEN", "PASSWORD", "SECRET", "openai_api_key",
	}
	for _, key := range secrets {
		if !IsSecretKey(key) {
			t.Errorf("IsSecretKey(%q) = false, want true", key)
		}
	}

	// A child process needs these; an allowlist would have taken them away.
	keep := []string{
		"PATH", "HOME", "USER", "SHELL", "LANG", "TMPDIR", "TERM", "TZ", "PWD",
		"GOPATH", "GOCACHE", "GOMODCACHE", "GOFLAGS", "GOROOT", "CI",
		"NODE_ENV", "SSH_AUTH_SOCK", "GIT_EDITOR", "VIRTUAL_ENV", "",
	}
	for _, key := range keep {
		if IsSecretKey(key) {
			t.Errorf("IsSecretKey(%q) = true, want false", key)
		}
	}
}

func TestSanitizedStripsSecretsKeepsOrdinaryVars(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-secret")
	t.Setenv("GITHUB_TOKEN", "ghp-secret")
	t.Setenv("GOPATH", "/keep/gopath")

	env := Sanitized()
	for _, item := range env {
		if item == "ANTHROPIC_API_KEY=sk-secret" || item == "GITHUB_TOKEN=ghp-secret" {
			t.Errorf("Sanitized() leaked %q", item)
		}
	}
	if !slices.Contains(env, "GOPATH=/keep/gopath") {
		t.Error("Sanitized() dropped GOPATH")
	}
}

func TestSanitizedAppliesExtraVerbatim(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "from-parent")

	// A caller passing a credential deliberately — an MCP server's configured
	// env — is not the leak this guards against.
	env := Sanitized("GITHUB_TOKEN=configured-on-purpose", "EXTRA=1")
	if !slices.Contains(env, "GITHUB_TOKEN=configured-on-purpose") {
		t.Error("extra must pass through unfiltered")
	}
	if !slices.Contains(env, "EXTRA=1") {
		t.Error("extra entry missing")
	}
}

func TestUpsertReplacesRatherThanDuplicates(t *testing.T) {
	env := Upsert([]string{"A=1", "B=2"}, "A=3")
	if !slices.Equal(env, []string{"A=3", "B=2"}) {
		t.Fatalf("Upsert = %v", env)
	}
	if got := Upsert([]string{"A=1"}, "novalue"); !slices.Equal(got, []string{"A=1"}) {
		t.Fatalf("Upsert with malformed entry = %v", got)
	}
}
