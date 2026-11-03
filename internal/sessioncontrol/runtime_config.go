package sessioncontrol

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/permissions"
	"github.com/konglong87/super-niuma/internal/promptmode"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

// RuntimeConfig is the non-secret configuration shared by Session defaults and
// immutable Run metadata. Empty request fields inherit the Session default.
type RuntimeConfig struct {
	Model          string `json:"model,omitempty"`
	Provider       string `json:"provider,omitempty"`
	PermissionMode string `json:"permission_mode,omitempty"`
	Effort         string `json:"effort,omitempty"`
	PromptMode     string `json:"prompt_mode,omitempty"`
}

func sendRequestFingerprint(request SendRequest) string {
	sources := append([]SessionRef(nil), request.SourceRefs...)
	sort.SliceStable(sources, func(i, j int) bool { return sources[i].String() < sources[j].String() })
	return operationFingerprint(OperationSend, request.Context, struct {
		Ref            SessionRef              `json:"ref"`
		Content        string                  `json:"content"`
		Attachments    []agenttasks.Attachment `json:"attachments,omitempty"`
		SourceRefs     []SessionRef            `json:"source_refs,omitempty"`
		Provider       string                  `json:"provider,omitempty"`
		Model          string                  `json:"model,omitempty"`
		PermissionMode string                  `json:"permission_mode,omitempty"`
		Effort         string                  `json:"effort,omitempty"`
		PromptMode     string                  `json:"prompt_mode,omitempty"`
	}{request.Ref, request.Content, request.Attachments, sources, request.Provider, request.Model, request.PermissionMode, request.Effort, request.PromptMode})
}

func (r CreateRequest) RuntimeConfig() RuntimeConfig {
	return RuntimeConfig{r.Model, r.Provider, r.PermissionMode, r.Effort, r.PromptMode}
}

func (r SendRequest) RuntimeConfig() RuntimeConfig {
	return RuntimeConfig{r.Model, r.Provider, r.PermissionMode, r.Effort, r.PromptMode}
}

func (s LocalSnapshot) RuntimeConfig() RuntimeConfig {
	return RuntimeConfig{s.Model, s.Provider, s.PermissionMode, s.Effort, s.PromptMode}
}

func (s *LocalSnapshot) setRuntimeConfig(c RuntimeConfig) {
	s.Model, s.Provider, s.PermissionMode, s.Effort, s.PromptMode = c.Model, c.Provider, c.PermissionMode, c.Effort, c.PromptMode
}

func (c RuntimeConfig) WithOverrides(o RuntimeConfig) RuntimeConfig {
	if o.Model != "" {
		c.Model = o.Model
	}
	if o.Provider != "" {
		c.Provider = o.Provider
	}
	if o.PermissionMode != "" {
		c.PermissionMode = o.PermissionMode
	}
	if o.Effort != "" {
		c.Effort = o.Effort
	}
	if o.PromptMode != "" {
		c.PromptMode = o.PromptMode
	}
	return c
}

// NormalizeRuntimeConfig validates before any persistence or queue mutation.
// Permission aliases retain their spelling because auto and plan carry policy
// semantics beyond their normalized fallback decision.
func NormalizeRuntimeConfig(c RuntimeConfig) (RuntimeConfig, error) {
	c.Model, c.Provider = strings.TrimSpace(c.Model), strings.TrimSpace(c.Provider)
	c.PermissionMode = strings.TrimSpace(c.PermissionMode)
	if _, ok := permissions.NormalizeMode(c.PermissionMode); !ok {
		return RuntimeConfig{}, invalidState("permission_mode is invalid")
	}
	c.Effort = strings.ToLower(strings.TrimSpace(c.Effort))
	switch c.Effort {
	case "", "inherit", "none", "off", "disabled", "low", "medium", "normal", "default", "high", "max", "maximum":
	default:
		if budget, err := strconv.Atoi(c.Effort); err != nil || budget <= 0 {
			return RuntimeConfig{}, invalidState("effort is invalid")
		}
	}
	c.PromptMode = strings.ToLower(strings.TrimSpace(c.PromptMode))
	if c.PromptMode != "" && c.PromptMode != promptmode.Code.String() && c.PromptMode != promptmode.Chat.String() {
		return RuntimeConfig{}, invalidState("prompt_mode is invalid")
	}
	return c, nil
}

func (a *ManagedAdapter) resolveRuntimeConfig(ctx context.Context, cwd string, c RuntimeConfig) (RuntimeConfig, error) {
	c, err := NormalizeRuntimeConfig(c)
	if err != nil {
		return RuntimeConfig{}, err
	}
	if resolver, ok := a.dispatcher.(interface {
		ResolveRuntimeConfig(context.Context, string, RuntimeConfig) (RuntimeConfig, error)
	}); ok {
		return resolver.ResolveRuntimeConfig(ctx, cwd, c)
	}
	return c, nil
}

func configuredManagedSnapshot(item mysqlstore.SessionControlSession, latest *mysqlstore.AgentTask, events map[uint64][]mysqlstore.AgentTaskEvent, pendingCount int) (SessionSnapshot, error) {
	snapshot := managedSnapshot(item.Session, latest, events, pendingCount)
	var config RuntimeConfig
	if item.MetadataJSON != "" {
		if err := json.Unmarshal([]byte(item.MetadataJSON), &config); err != nil {
			return SessionSnapshot{}, err
		}
	}
	if item.Model != "" {
		config.Model = item.Model
	}
	if latest != nil && (latest.Status == agenttasks.StatusReady || latest.Status == agenttasks.StatusRunning) {
		var active RuntimeConfig
		if latest.MetadataJSON != "" {
			if err := json.Unmarshal([]byte(latest.MetadataJSON), &active); err != nil {
				return SessionSnapshot{}, err
			}
		}
		if latest.Model != "" {
			active.Model = latest.Model
		}
		config = config.WithOverrides(active)
	}
	snapshot.setRuntimeConfig(config)
	return snapshot, nil
}

func runtimeConfigMetadataJSON(cwd string, c RuntimeConfig, identity OperationIdentity) (string, error) {
	base, err := json.Marshal(struct {
		RuntimeConfig
		CWD string `json:"cwd,omitempty"`
	}{c, cwd})
	if err != nil {
		return "", err
	}
	return mergeOperationMetadataJSON(string(base), identity)
}

func isPreparedSideChat(task *mysqlstore.AgentTask) bool {
	if task == nil || task.Status != agenttasks.StatusReady || task.IdempotencyKey != "" || task.AgentName != agenttasks.AgentNameWeb {
		return false
	}
	var metadata map[string]any
	return json.Unmarshal([]byte(task.MetadataJSON), &metadata) == nil && metadata["source"] == agenttasks.SourcePendingInputSideChat
}

// Preserve only the side-chat provenance, so retries retain the original
// candidate context without carrying another operation's replay identity.
func preserveSideChatMetadata(currentJSON, previousJSON string) (string, error) {
	var previous, current map[string]json.RawMessage
	if err := json.Unmarshal([]byte(previousJSON), &previous); err != nil {
		return "", err
	}
	var source string
	if json.Unmarshal(previous["source"], &source) != nil || source != agenttasks.SourcePendingInputSideChat {
		return currentJSON, nil
	}
	if err := json.Unmarshal([]byte(currentJSON), &current); err != nil {
		return "", err
	}
	for _, key := range []string{"source", "source_pending_input_id", "web_agent_session_id", "web_agent_session_key"} {
		if value, ok := previous[key]; ok {
			current[key] = value
		}
	}
	encoded, err := json.Marshal(current)
	return string(encoded), err
}
