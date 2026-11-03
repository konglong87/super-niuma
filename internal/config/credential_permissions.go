package config

import (
	"fmt"
	"os"
	"strings"
)

// Settings this process writes itself go out 0600 (see SaveSettingsFile), but
// files a user hand-edits keep whatever mode their editor gave them — commonly
// 0644, i.e. readable by every other user and process on the machine. That is
// how config/config.local.yaml ends up world-readable while holding plaintext
// API keys (AUDIT-P0-06).
//
// This warns rather than refuses: a loader that rejected such a file would break
// every existing setup that relies on one, which is a worse outcome than a
// readable file the operator has been told about.

// insecureCredentialModeMask is the set of permission bits that expose a
// credential file beyond its owner.
const insecureCredentialModeMask os.FileMode = 0o077

// CredentialPermissionWarning names one credential-bearing config file whose
// permissions let other local users read it.
type CredentialPermissionWarning struct {
	Path string
	Mode os.FileMode
}

// String renders the warning for operators. It deliberately never includes the
// credential value itself.
func (w CredentialPermissionWarning) String() string {
	return fmt.Sprintf("%s holds plaintext credentials but is mode %04o; any local user can read it. Run: chmod 600 %s",
		w.Path, w.Mode.Perm(), w.Path)
}

// CredentialPermissionWarnings reports every file on the settings search path
// that both carries a credential and is readable beyond its owner. Files without
// credentials are ignored so committed config (config/config.yaml) stays quiet.
func CredentialPermissionWarnings(cwd string) []CredentialPermissionWarning {
	var warnings []CredentialPermissionWarning
	seen := make(map[string]bool)
	for _, path := range settingsSearchPaths(cwd) {
		if seen[path] {
			continue
		}
		seen[path] = true
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		if info.Mode().Perm()&insecureCredentialModeMask == 0 {
			continue
		}
		settings, ok := readSettings(path)
		if !ok || !settingsCarryCredentials(settings) {
			continue
		}
		warnings = append(warnings, CredentialPermissionWarning{Path: path, Mode: info.Mode()})
	}
	return warnings
}

// settingsCarryCredentials reports whether the parsed settings contain a
// plaintext secret, as opposed to a reference to one.
func settingsCarryCredentials(settings Settings) bool {
	if isPlaintextSecret(settings.APIKey) || isPlaintextSecret(settings.AuthToken) {
		return true
	}
	for key, value := range settings.Env {
		if isCredentialEnvKey(key) && isPlaintextSecret(value) {
			return true
		}
	}
	if settings.Fallback == nil {
		return false
	}
	for _, provider := range settings.Fallback.Providers {
		if isPlaintextSecret(provider.APIKey) || isPlaintextSecret(provider.AuthToken) {
			return true
		}
	}
	return false
}

func isCredentialEnvKey(key string) bool {
	key = strings.ToUpper(strings.TrimSpace(key))
	for _, marker := range []string{"API_KEY", "AUTH_TOKEN", "OAUTH_TOKEN", "SECRET", "PASSWORD"} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}

// isPlaintextSecret rejects empty values and `${VAR}` indirections — those are
// documented in config.example.yaml and expose nothing on their own.
func isPlaintextSecret(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	return !(strings.HasPrefix(value, "${") && strings.HasSuffix(value, "}"))
}
