package channel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const RuntimeFingerprintVersion = 1

// RuntimeFingerprintInput contains the runtime boundaries that determine
// whether a channel execution context can be safely reused. Required fields
// are validated as supplied; WorkspaceRealpath is preserved byte-for-byte.
// Field order and JSON tags are part of the fingerprint contract.
type RuntimeFingerprintInput struct {
	AgentKind                   string `json:"agent_kind"`
	PromptMode                  string `json:"prompt_mode"`
	ModelProvider               string `json:"model_provider"`
	ModelPolicy                 string `json:"model_policy"`
	WorkspaceRealpath           string `json:"workspace_realpath"`
	PermissionMode              string `json:"permission_mode"`
	SandboxPolicy               string `json:"sandbox_policy"`
	AccessPolicyDigest          string `json:"access_policy_digest"`
	ResourceScopeDigest         string `json:"resource_scope_digest"`
	AttachmentPolicyShapeDigest string `json:"attachment_policy_shape_digest"`
}

type runtimeFingerprintEnvelope struct {
	Version int `json:"version"`
	RuntimeFingerprintInput
}

// RuntimeFingerprint returns a stable lowercase SHA-256 fingerprint of the
// validated runtime input and the current canonical format version.
func RuntimeFingerprint(input RuntimeFingerprintInput) (string, error) {
	if err := validateRuntimeFingerprintInput(input); err != nil {
		return "", err
	}

	payload, err := json.Marshal(runtimeFingerprintEnvelope{
		Version:                 RuntimeFingerprintVersion,
		RuntimeFingerprintInput: input,
	})
	if err != nil {
		return "", fmt.Errorf("marshal runtime fingerprint input: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func validateRuntimeFingerprintInput(input RuntimeFingerprintInput) error {
	fields := []struct {
		name     string
		value    string
		required bool
	}{
		{name: "agent_kind", value: input.AgentKind, required: true},
		{name: "prompt_mode", value: input.PromptMode, required: true},
		{name: "model_provider", value: input.ModelProvider, required: true},
		{name: "model_policy", value: input.ModelPolicy, required: true},
		{name: "workspace_realpath", value: input.WorkspaceRealpath},
		{name: "permission_mode", value: input.PermissionMode, required: true},
		{name: "sandbox_policy", value: input.SandboxPolicy, required: true},
		{name: "access_policy_digest", value: input.AccessPolicyDigest, required: true},
		{name: "resource_scope_digest", value: input.ResourceScopeDigest, required: true},
		{name: "attachment_policy_shape_digest", value: input.AttachmentPolicyShapeDigest, required: true},
	}
	for _, field := range fields {
		if !utf8.ValidString(field.value) {
			return fmt.Errorf("runtime fingerprint field %s contains invalid UTF-8", field.name)
		}
		if !field.required {
			continue
		}
		if field.value == "" {
			return fmt.Errorf("runtime fingerprint field %s is required", field.name)
		}
		if strings.TrimSpace(field.value) != field.value {
			return fmt.Errorf("runtime fingerprint field %s must not have leading or trailing whitespace", field.name)
		}
	}
	if input.WorkspaceRealpath != "" {
		if !filepath.IsAbs(input.WorkspaceRealpath) {
			return fmt.Errorf("runtime fingerprint field workspace_realpath must be absolute")
		}
		if filepath.Clean(input.WorkspaceRealpath) != input.WorkspaceRealpath {
			return fmt.Errorf("runtime fingerprint field workspace_realpath must be canonical")
		}
	}
	return nil
}

// StoredRuntimeFingerprintVersion is the version callers should persist and
// read back alongside a fingerprint. During rolling deployments, resume or
// session creation must be rejected when the stored version does not match.
func StoredRuntimeFingerprintVersion() int {
	return RuntimeFingerprintVersion
}
