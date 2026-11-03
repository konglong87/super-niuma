package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/konglong87/super-niuma/internal/identity"
	"github.com/konglong87/super-niuma/internal/product"
	"gopkg.in/yaml.v3"
)

const DefaultShowThinking = true

var ErrProviderRouteNotConfigured = errors.New("provider route is not configured: set provider, baseURL, and apiKey or authToken in ~/.niuma/settings.json")

const (
	TUIThinkingModeFull    = "full"
	TUIThinkingModeSummary = "summary"
	TUIThinkingModeHidden  = "hidden"
)

// KnownModels intentionally stays empty. Model IDs belong to the configured
// provider and must never silently imply a vendor or hosted endpoint.
var KnownModels = []string{}

type Config struct {
	APIKey                string
	AuthToken             string
	BaseURL               string
	Provider              string
	ProviderProtocol      ProviderProtocol
	Responses             *ResponsesProviderSettings
	FallbackProviders     []ProviderConfig
	Settings              Settings
	Sources               []string
	SelectedProvider      string
	SelectedProviderModel string
	ConfigurationError    string
	// Warnings are non-fatal problems found while loading, e.g. a credential
	// file other local users can read. Callers surface them; loading succeeded.
	Warnings []string
}

func (cfg Config) SelectProvider(name string) (Config, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return cfg, nil
	}
	available := make([]string, 0, len(cfg.FallbackProviders))
	for i, provider := range cfg.FallbackProviders {
		providerName := strings.TrimSpace(provider.Name)
		if providerName != "" {
			available = append(available, providerName)
		}
		if providerName != name {
			continue
		}
		cfg.Provider = provider.Type
		cfg.ProviderProtocol = provider.Protocol
		cfg.Responses = cloneResponsesProviderSettings(provider.Responses)
		cfg.BaseURL = provider.BaseURL
		cfg.APIKey = provider.APIKey
		cfg.AuthToken = provider.AuthToken
		cfg.SelectedProvider = providerName
		cfg.SelectedProviderModel = strings.TrimSpace(provider.Model)
		cfg.FallbackProviders = append(append([]ProviderConfig(nil), cfg.FallbackProviders[:i]...), cfg.FallbackProviders[i+1:]...)
		return cfg, nil
	}
	sort.Strings(available)
	return Config{}, fmt.Errorf("provider %q not found; available providers: %s", name, strings.Join(available, ", "))
}

func Load() Config {
	cwd, _ := os.Getwd()
	return LoadForCWD(cwd)
}

func LoadForCWD(cwd string) Config {
	loaded := LoadSettings(cwd)
	cfg := Config{
		APIKey:            loaded.Settings.APIKey,
		AuthToken:         loaded.Settings.AuthToken,
		BaseURL:           strings.TrimRight(strings.TrimSpace(loaded.Settings.BaseURL), "/"),
		Provider:          strings.TrimSpace(loaded.Settings.Provider),
		ProviderProtocol:  normalizeProviderProtocol(loaded.Settings.ProviderProtocol),
		Responses:         cloneResponsesProviderSettings(loaded.Settings.Responses),
		FallbackProviders: fallbackProviders(loaded.Settings.Fallback, loaded.Settings.APIKey, loaded.Settings.AuthToken),
		Settings:          loaded.Settings,
		Sources:           loaded.Sources,
		Warnings:          credentialPermissionMessages(cwd),
	}
	if err := cfg.ValidateProviderRoute(); err != nil {
		cfg.ConfigurationError = err.Error()
	}
	return cfg
}

func ValidateProviderConfig(provider ProviderConfig) error {
	if strings.TrimSpace(provider.Type) == "" {
		return ErrProviderRouteNotConfigured
	}
	if strings.TrimSpace(provider.BaseURL) == "" {
		return fmt.Errorf("provider route is not configured: baseURL is required for provider %q", provider.Type)
	}
	if strings.TrimSpace(provider.APIKey) == "" && strings.TrimSpace(provider.AuthToken) == "" {
		return fmt.Errorf("provider route is not configured: apiKey or authToken is required for provider %q", provider.Type)
	}
	if _, err := ResolveProviderProtocol(provider.Type, provider.Protocol, provider.Responses); err != nil {
		return fmt.Errorf("provider route is not configured: %w", err)
	}
	return nil
}

// ValidateProviderRoute returns a configuration error before an adapter is
// created. Adapters intentionally keep their SDK-specific defaults; runtime
// callers must use this gate so an empty route cannot silently become Anthropic.
func (cfg Config) ValidateProviderRoute() error {
	return ValidateProviderConfig(ProviderConfig{
		Type:      cfg.Provider,
		Protocol:  cfg.ProviderProtocol,
		Responses: cfg.Responses,
		BaseURL:   cfg.BaseURL,
		APIKey:    cfg.APIKey,
		AuthToken: cfg.AuthToken,
	})
}

func credentialPermissionMessages(cwd string) []string {
	warnings := CredentialPermissionWarnings(cwd)
	if len(warnings) == 0 {
		return nil
	}
	messages := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		messages = append(messages, warning.String())
	}
	return messages
}

func (cfg Config) WithRuntimeSettings(settings Settings) Config {
	cfg.Settings = settings
	previousProvider := strings.TrimSpace(cfg.Provider)
	nextProvider := strings.TrimSpace(settings.Provider)
	if nextProvider != "" && previousProvider != "" &&
		NormalizeProviderKind(previousProvider) != NormalizeProviderKind(nextProvider) {
		cfg.BaseURL = ""
		cfg.APIKey = ""
		cfg.AuthToken = ""
		cfg.ProviderProtocol = ""
		cfg.Responses = nil
	}
	if settings.APIKey != "" {
		cfg.APIKey = settings.APIKey
	}
	if settings.AuthToken != "" {
		cfg.AuthToken = settings.AuthToken
	}
	if provider := strings.TrimSpace(settings.Provider); provider != "" {
		cfg.Provider = provider
	}
	if settings.BaseURL != "" {
		cfg.BaseURL = strings.TrimRight(strings.TrimSpace(settings.BaseURL), "/")
	}
	if settings.ProviderProtocol != "" {
		cfg.ProviderProtocol = normalizeProviderProtocol(settings.ProviderProtocol)
		cfg.Responses = cloneResponsesProviderSettings(settings.Responses)
	}
	cfg.FallbackProviders = fallbackProviders(settings.Fallback, cfg.APIKey, cfg.AuthToken)
	if err := cfg.ValidateProviderRoute(); err != nil {
		cfg.ConfigurationError = err.Error()
	} else {
		cfg.ConfigurationError = ""
	}
	return cfg
}

func DefaultModel() string {
	return ""
}

func ResolveModel(cwd, explicit string) string {
	if explicit != "" {
		return explicit
	}
	loaded := LoadSettings(cwd)
	if loaded.Model != "" {
		return loaded.Model
	}
	return DefaultModel()
}

// ModelPrice is a per-million-token cost for a model, used to make cost
// estimation provider-neutral.
// The three cache fields are optional; leaving one at zero means "derive it from
// Input", which only works for providers whose cache discount is known (see
// session.CacheMultipliersForProviderKind). Set them explicitly for any other
// provider — that is the escape hatch AUDIT-P1-36 was filed for.
type ModelPrice struct {
	Input        float64 `json:"input,omitempty" yaml:"input,omitempty"`
	Output       float64 `json:"output,omitempty" yaml:"output,omitempty"`
	CacheRead    float64 `json:"cacheRead,omitempty" yaml:"cacheRead,omitempty"`
	CacheWrite5m float64 `json:"cacheWrite5m,omitempty" yaml:"cacheWrite5m,omitempty"`
	CacheWrite1h float64 `json:"cacheWrite1h,omitempty" yaml:"cacheWrite1h,omitempty"`
}

// ProviderKindAnthropic reports whether kind speaks Anthropic's Messages API. An
// empty kind is Anthropic, matching the client's own default.
func ProviderKindAnthropic(kind string) bool {
	switch NormalizeProviderKind(kind) {
	case "anthropic", "anthropic-compatible":
		return true
	default:
		return false
	}
}

// ProviderKindOpenAI reports whether kind speaks OpenAI's chat-completions API.
func ProviderKindOpenAI(kind string) bool {
	switch NormalizeProviderKind(kind) {
	case "custom", "openai", "openai-compatible", "openai-chat-completions":
		return true
	default:
		return false
	}
}

// NormalizeProviderKind canonicalises a provider type string, defaulting an
// unset one to "anthropic".
func NormalizeProviderKind(kind string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		return "anthropic"
	}
	return kind
}

// ProviderKindForModel reports which configured provider serves model, as its
// normalized provider kind. found is false when no configured provider declares
// the model, in which case callers must not assume a kind — guessing one is how
// a third-party backend ends up priced like Anthropic (AUDIT-P1-36).
func ProviderKindForModel(cwd, model string) (kind string, found bool) {
	model = strings.TrimSpace(model)
	if model == "" {
		return "", false
	}
	cfg := LoadForCWD(cwd)
	if model == strings.TrimSpace(cfg.SelectedProviderModel) || model == ResolveModel(cwd, "") {
		return NormalizeProviderKind(cfg.Provider), true
	}
	for _, provider := range cfg.FallbackProviders {
		if model == strings.TrimSpace(provider.Model) {
			return NormalizeProviderKind(provider.Type), true
		}
	}
	return "", false
}

// ModelPricing returns the configured model→price overrides (per million
// tokens), merged across settings sources. Empty when unset.
func ModelPricing(cwd string) map[string]ModelPrice {
	return LoadForCWD(cwd).Settings.ModelPricing
}

// SubagentModelTiers returns the configured tier→model overrides for subagents
// (e.g. {"haiku": "glm-4-flash"}), merged across settings sources. Empty when
// unset. Used to point tier-aliased subagents (like Explore's "haiku") at a
// cheaper model under a non-Anthropic provider instead of inheriting the
// session model.
func SubagentModelTiers(cwd string) map[string]string {
	return LoadForCWD(cwd).Settings.SubagentModelTiers
}

// ConfiguredModels returns the models actually configured for cwd: the resolved
// primary model followed by each fallback provider's model, de-duplicated in order.
// Returns an empty slice when nothing is configured.
func ConfiguredModels(cwd string) []string {
	cfg := LoadForCWD(cwd)
	ordered := make([]string, 0, len(cfg.FallbackProviders)+1)
	seen := make(map[string]bool)
	add := func(model string) {
		model = strings.TrimSpace(model)
		if model == "" || seen[model] {
			return
		}
		seen[model] = true
		ordered = append(ordered, model)
	}
	add(ResolveModel(cwd, ""))
	for _, provider := range cfg.FallbackProviders {
		add(provider.Model)
	}
	return ordered
}

// AvailableModels returns only model IDs explicitly configured by the user or
// exposed by a configured provider.
func AvailableModels(cwd string) []string {
	cfg := LoadForCWD(cwd)
	configuredModels := ConfiguredModels(cwd)
	ordered := make([]string, 0, len(cfg.Settings.ModelOptions)+len(configuredModels))
	seen := make(map[string]bool)
	add := func(model string) {
		model = strings.TrimSpace(model)
		if model == "" || seen[model] {
			return
		}
		seen[model] = true
		ordered = append(ordered, model)
	}
	for _, model := range cfg.Settings.ModelOptions {
		add(model)
	}

	configured := len(cfg.Settings.ModelOptions) > 0 || strings.TrimSpace(cfg.Settings.Model) != "" || len(cfg.FallbackProviders) > 0
	if configured {
		for _, model := range configuredModels {
			add(model)
		}
		return ordered
	}
	return ordered
}

// ProviderOption pairs a configured provider's name with the model it serves so
// the UI can let the user select a specific provider even when several providers
// expose the same model.
type ProviderOption struct {
	Name  string `json:"name"`
	Model string `json:"model"`
}

// ConfiguredProviders returns the named providers configured for cwd. Unlike
// ConfiguredModels it keeps one entry per provider name (deduping by name, not by
// model), so two providers serving the same model stay distinguishable.
func ConfiguredProviders(cwd string) []ProviderOption {
	cfg := LoadForCWD(cwd)
	options := make([]ProviderOption, 0, len(cfg.FallbackProviders))
	seen := make(map[string]bool)
	for _, provider := range cfg.FallbackProviders {
		name := strings.TrimSpace(provider.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		options = append(options, ProviderOption{Name: name, Model: strings.TrimSpace(provider.Model)})
	}
	return options
}

func ResolveMultimodalModel(cwd, modality, explicit string) string {
	if explicit != "" {
		return explicit
	}
	primary := ResolveModel(cwd, "")
	settings := LoadSettings(cwd).Settings
	return MultimodalModelFor(settings, primary, modality)
}

func MultimodalModelFor(settings Settings, primaryModel, modality string) string {
	primaryModel = strings.TrimSpace(primaryModel)
	if settings.Multimodal == nil {
		return primaryModel
	}
	if settings.Multimodal.Enabled != nil && !*settings.Multimodal.Enabled {
		return primaryModel
	}
	normalized := normalizeModality(modality)
	if model := strings.TrimSpace(settings.Multimodal.Models[normalized]); model != "" {
		return model
	}
	if model := strings.TrimSpace(settings.Multimodal.DefaultModel); model != "" {
		return model
	}
	return primaryModel
}

func normalizeModality(modality string) string {
	value := strings.ToLower(strings.TrimSpace(modality))
	switch value {
	case "img", "picture", "photo", "jpeg", "jpg", "png", "gif", "webp", "image/png", "image/jpeg", "image/webp":
		return "image"
	case "movie", "mp4", "mov", "video/mp4", "video/quicktime":
		return "video"
	case "voice", "sound", "speech", "mp3", "wav", "m4a", "audio/mpeg", "audio/wav", "audio/m4a":
		return "audio"
	case "document", "doc", "pdf", "text", "application/pdf", "text/plain":
		return "file"
	default:
		return value
	}
}

type PermissionSettings struct {
	Allow                 []string          `json:"allow,omitempty" yaml:"allow,omitempty"`
	Deny                  []string          `json:"deny,omitempty" yaml:"deny,omitempty"`
	Ask                   []string          `json:"ask,omitempty" yaml:"ask,omitempty"`
	AlwaysAsk             []string          `json:"alwaysAsk,omitempty" yaml:"alwaysAsk,omitempty"`
	DefaultMode           string            `json:"defaultMode,omitempty" yaml:"defaultMode,omitempty"`
	AdditionalDirectories []string          `json:"additionalDirectories,omitempty" yaml:"additionalDirectories,omitempty"`
	Source                string            `json:"source,omitempty" yaml:"source,omitempty"`
	Preference            string            `json:"preference,omitempty" yaml:"preference,omitempty"`
	Bypass                bool              `json:"-" yaml:"-"`
	RuleSources           map[string]string `json:"-" yaml:"-"`
}

type SandboxSettings struct {
	Enabled                   *bool                     `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	FailIfUnavailable         *bool                     `json:"failIfUnavailable,omitempty" yaml:"failIfUnavailable,omitempty"`
	AllowUnsandboxedCommands  *bool                     `json:"allowUnsandboxedCommands,omitempty" yaml:"allowUnsandboxedCommands,omitempty"`
	AutoAllowBashIfSandboxed  *bool                     `json:"autoAllowBashIfSandboxed,omitempty" yaml:"autoAllowBashIfSandboxed,omitempty"`
	EnabledPlatforms          []string                  `json:"enabledPlatforms,omitempty" yaml:"enabledPlatforms,omitempty"`
	ExcludedCommands          []string                  `json:"excludedCommands,omitempty" yaml:"excludedCommands,omitempty"`
	Filesystem                SandboxFilesystemSettings `json:"filesystem,omitempty" yaml:"filesystem,omitempty"`
	Network                   SandboxNetworkSettings    `json:"network,omitempty" yaml:"network,omitempty"`
	UnixSockets               SandboxUnixSocketSettings `json:"unixSockets,omitempty" yaml:"unixSockets,omitempty"`
	Seccomp                   SandboxSeccompSettings    `json:"seccomp,omitempty" yaml:"seccomp,omitempty"`
	AllowPty                  *bool                     `json:"allowPty,omitempty" yaml:"allowPty,omitempty"`
	EnableWeakerNestedSandbox *bool                     `json:"enableWeakerNestedSandbox,omitempty" yaml:"enableWeakerNestedSandbox,omitempty"`
}

type SandboxFilesystemSettings struct {
	AllowRead  []string `json:"allowRead,omitempty" yaml:"allowRead,omitempty"`
	DenyRead   []string `json:"denyRead,omitempty" yaml:"denyRead,omitempty"`
	AllowWrite []string `json:"allowWrite,omitempty" yaml:"allowWrite,omitempty"`
	DenyWrite  []string `json:"denyWrite,omitempty" yaml:"denyWrite,omitempty"`
}

type SandboxNetworkSettings struct {
	Disabled     *bool                       `json:"disabled,omitempty" yaml:"disabled,omitempty"`
	AllowDomains []string                    `json:"allowDomains,omitempty" yaml:"allowDomains,omitempty"`
	DenyDomains  []string                    `json:"denyDomains,omitempty" yaml:"denyDomains,omitempty"`
	Proxy        SandboxNetworkProxySettings `json:"proxy,omitempty" yaml:"proxy,omitempty"`
	MITM         SandboxNetworkMITMSettings  `json:"mitm,omitempty" yaml:"mitm,omitempty"`
}

type SandboxNetworkProxySettings struct {
	URL      string `json:"url,omitempty" yaml:"url,omitempty"`
	Mode     string `json:"mode,omitempty" yaml:"mode,omitempty"`
	Required *bool  `json:"required,omitempty" yaml:"required,omitempty"`
}

type SandboxNetworkMITMSettings struct {
	CAFile   string `json:"caFile,omitempty" yaml:"caFile,omitempty"`
	Required *bool  `json:"required,omitempty" yaml:"required,omitempty"`
}

type SandboxUnixSocketSettings struct {
	Deny []string `json:"deny,omitempty" yaml:"deny,omitempty"`
}

type SandboxSeccompSettings struct {
	Enabled *bool  `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	Mode    string `json:"mode,omitempty" yaml:"mode,omitempty"`
}

type MCPServerConfig struct {
	Type    string            `json:"type,omitempty" yaml:"type,omitempty"`
	Command string            `json:"command,omitempty" yaml:"command,omitempty"`
	Args    []string          `json:"args,omitempty" yaml:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
	URL     string            `json:"url,omitempty" yaml:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`
}

type ProviderConfig struct {
	Name             string                     `json:"name,omitempty" yaml:"name,omitempty"`
	Type             string                     `json:"type,omitempty" yaml:"type,omitempty"`
	Protocol         ProviderProtocol           `json:"protocol,omitempty" yaml:"protocol,omitempty"`
	ImageProtocol    string                     `json:"imageProtocol,omitempty" yaml:"imageProtocol,omitempty"`
	ImageResultHosts []string                   `json:"imageResultHosts,omitempty" yaml:"imageResultHosts,omitempty"`
	BaseURL          string                     `json:"baseURL,omitempty" yaml:"baseURL,omitempty"`
	APIKey           string                     `json:"apiKey,omitempty" yaml:"apiKey,omitempty"`
	AuthToken        string                     `json:"authToken,omitempty" yaml:"authToken,omitempty"`
	Model            string                     `json:"model,omitempty" yaml:"model,omitempty"`
	Responses        *ResponsesProviderSettings `json:"responses,omitempty" yaml:"responses,omitempty"`

	apiKeySet    bool
	authTokenSet bool
}

// FallbackRouteName is the runtime identity used for operator assertions and
// provider telemetry. ordinal is the zero-based position in the original chain.
func (p ProviderConfig) FallbackRouteName(ordinal int) string {
	if name := strings.TrimSpace(p.Name); name != "" {
		return name
	}
	return fmt.Sprintf("fallback-%d", ordinal+1)
}

type FallbackSettings struct {
	Enabled   *bool            `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	Providers []ProviderConfig `json:"providers,omitempty" yaml:"providers,omitempty"`
}

type UpdateSettings struct {
	Enabled          *bool  `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	CheckOnStartup   *bool  `json:"checkOnStartup,omitempty" yaml:"checkOnStartup,omitempty"`
	CheckOnly        *bool  `json:"checkOnly,omitempty" yaml:"checkOnly,omitempty"`
	AutoPull         *bool  `json:"autoPull,omitempty" yaml:"autoPull,omitempty"`
	SkipWhenDirty    *bool  `json:"skipWhenDirty,omitempty" yaml:"skipWhenDirty,omitempty"`
	Strategy         string `json:"strategy,omitempty" yaml:"strategy,omitempty"`
	RepoDir          string `json:"repoDir,omitempty" yaml:"repoDir,omitempty"`
	Command          string `json:"command,omitempty" yaml:"command,omitempty"`
	CustomCommand    string `json:"customCommand,omitempty" yaml:"customCommand,omitempty"`
	VersionSourceURL string `json:"versionSourceURL,omitempty" yaml:"versionSourceURL,omitempty"`
	ScheduleInterval string `json:"scheduleInterval,omitempty" yaml:"scheduleInterval,omitempty"`
	TimeoutSeconds   int    `json:"timeoutSeconds,omitempty" yaml:"timeoutSeconds,omitempty"`
}

type MultimodalSettings struct {
	Enabled      *bool             `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	DefaultModel string            `json:"defaultModel,omitempty" yaml:"defaultModel,omitempty"`
	Models       map[string]string `json:"models,omitempty" yaml:"models,omitempty"`
}

type AutoCompactSettings struct {
	Enabled               *bool              `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	DefaultThresholdRatio *float64           `json:"defaultThresholdRatio,omitempty" yaml:"defaultThresholdRatio,omitempty"`
	PreserveRecentRounds  *int               `json:"preserveRecentRounds,omitempty" yaml:"preserveRecentRounds,omitempty"`
	SummaryModel          *string            `json:"summaryModel,omitempty" yaml:"summaryModel,omitempty"`
	MaxSummaryTokens      *int               `json:"maxSummaryTokens,omitempty" yaml:"maxSummaryTokens,omitempty"`
	CooldownTurns         *int               `json:"cooldownTurns,omitempty" yaml:"cooldownTurns,omitempty"`
	MaxFailures           *int               `json:"maxFailures,omitempty" yaml:"maxFailures,omitempty"`
	ModelContext          map[string]int     `json:"modelContext,omitempty" yaml:"modelContext,omitempty"`
	ModelThresholdRatio   map[string]float64 `json:"modelThresholdRatio,omitempty" yaml:"modelThresholdRatio,omitempty"`
}

type RecapSettings struct {
	Enabled               *bool  `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	Mode                  string `json:"mode,omitempty" yaml:"mode,omitempty"`
	Model                 string `json:"model,omitempty" yaml:"model,omitempty"`
	RecentMessageWindow   *int   `json:"recentMessageWindow,omitempty" yaml:"recentMessageWindow,omitempty"`
	MaxTokens             *int   `json:"maxTokens,omitempty" yaml:"maxTokens,omitempty"`
	AwayDelaySeconds      *int   `json:"awayDelaySeconds,omitempty" yaml:"awayDelaySeconds,omitempty"`
	IncludeSessionMemory  *bool  `json:"includeSessionMemory,omitempty" yaml:"includeSessionMemory,omitempty"`
	IncludeCompactSummary *bool  `json:"includeCompactSummary,omitempty" yaml:"includeCompactSummary,omitempty"`
}

type NextStepsSettings struct {
	Enabled *bool  `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	Model   string `json:"model,omitempty" yaml:"model,omitempty"`
	Count   *int   `json:"count,omitempty" yaml:"count,omitempty"`
}

type TUISettings struct {
	ResumeHistoryLimit *int   `json:"resumeHistoryLimit,omitempty" yaml:"resumeHistoryLimit,omitempty"`
	ShowThinking       *bool  `json:"showThinking,omitempty" yaml:"showThinking,omitempty"`
	ThinkingMode       string `json:"thinkingMode,omitempty" yaml:"thinkingMode,omitempty"`
}

type WebAgentUISettings struct {
	ShowThinking *bool `json:"showThinking,omitempty" yaml:"showThinking,omitempty"`
}

func ResolveShowThinking(value *bool) bool {
	return value == nil || *value
}

func TUIShowThinking(settings Settings) bool {
	if settings.TUI == nil {
		return DefaultShowThinking
	}
	return ResolveShowThinking(settings.TUI.ShowThinking)
}

func NormalizeTUIThinkingMode(value string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(value))
	switch mode {
	case TUIThinkingModeFull, TUIThinkingModeSummary, TUIThinkingModeHidden:
		return mode, nil
	default:
		return "", fmt.Errorf("tui thinking mode must be full, summary, or hidden")
	}
}

func ResolveTUIThinkingMode(settings Settings) string {
	if settings.TUI != nil && strings.TrimSpace(settings.TUI.ThinkingMode) != "" {
		if mode, err := NormalizeTUIThinkingMode(settings.TUI.ThinkingMode); err == nil {
			return mode
		}
	}
	if !TUIShowThinking(settings) {
		return TUIThinkingModeHidden
	}
	return TUIThinkingModeFull
}

func WebAgentUIShowThinking(settings Settings) bool {
	if settings.WebAgentUI == nil {
		return DefaultShowThinking
	}
	return ResolveShowThinking(settings.WebAgentUI.ShowThinking)
}

type WebSearchSettings struct {
	Endpoint string `json:"endpoint,omitempty" yaml:"endpoint,omitempty"`
}

type InitSettings struct {
	DetailLevel string `json:"detailLevel,omitempty" yaml:"detailLevel,omitempty"`
}

const (
	ImageProtocolOpenAI    = "openai-images"
	ImageProtocolSenseNova = "sensenova-images"
	ImageProtocolAgnes     = "agnes-images"

	DefaultImageGenerationModel            = "gpt-image-2"
	DefaultImageGenerationPreviewInContext = false
	DefaultImageGenerationQuality          = "auto"
	DefaultImageGenerationSize             = "auto"
	DefaultImageGenerationOutputFormat     = "png"
	DefaultImageGenerationBackground       = "auto"
	DefaultImageGenerationMaxImages        = 1
	DefaultImageGenerationMaxPromptChars   = 8000
	DefaultImageGenerationMaxInputBytes    = int64(25 * 1024 * 1024)
	DefaultImageGenerationMaxConcurrent    = 2
	DefaultImageGenerationTimeoutSeconds   = 180
	DefaultImageGenerationRetentionDays    = 0

	DefaultImageWorkerPollIntervalMS     = 500
	DefaultImageWorkerMaxConcurrent      = 2
	DefaultImageWorkerMaxAttempts        = 3
	DefaultImageWorkerHeartbeatSeconds   = 15
	DefaultImageWorkerLeaseGraceSeconds  = 60
	DefaultImageWorkerFinalizeTimeout    = 10
	DefaultImageWorkerMaxQueuedPerTenant = 100
	DefaultImageWorkerMaxQueuedPerUser   = 20
)

type ImageGenerationWorkerSettings struct {
	PollIntervalMS         int `json:"pollIntervalMs,omitempty" yaml:"pollIntervalMs,omitempty"`
	MaxConcurrent          int `json:"maxConcurrent,omitempty" yaml:"maxConcurrent,omitempty"`
	MaxAttempts            int `json:"maxAttempts,omitempty" yaml:"maxAttempts,omitempty"`
	HeartbeatSeconds       int `json:"heartbeatSeconds,omitempty" yaml:"heartbeatSeconds,omitempty"`
	LeaseSeconds           int `json:"leaseSeconds,omitempty" yaml:"leaseSeconds,omitempty"`
	FinalizeTimeoutSeconds int `json:"finalizeTimeoutSeconds,omitempty" yaml:"finalizeTimeoutSeconds,omitempty"`
	MaxQueuedPerTenant     int `json:"maxQueuedPerTenant,omitempty" yaml:"maxQueuedPerTenant,omitempty"`
	MaxQueuedPerUser       int `json:"maxQueuedPerUser,omitempty" yaml:"maxQueuedPerUser,omitempty"`
}

// ImageModelCapability describes the provider-specific options exposed to
// trusted callers and the WebUI. Values are declarative; adapters own wire
// protocol mapping and must not accept arbitrary extra request fields.
type ImageModelCapability struct {
	Operations        []string `json:"operations,omitempty" yaml:"operations,omitempty"`
	Resolutions       []string `json:"resolutions,omitempty" yaml:"resolutions,omitempty"`
	AspectRatios      []string `json:"aspectRatios,omitempty" yaml:"aspectRatios,omitempty"`
	Sizes             []string `json:"sizes,omitempty" yaml:"sizes,omitempty"`
	QualityOptions    []string `json:"qualityOptions,omitempty" yaml:"qualityOptions,omitempty"`
	OutputFormats     []string `json:"outputFormats,omitempty" yaml:"outputFormats,omitempty"`
	ResponseModes     []string `json:"responseModes,omitempty" yaml:"responseModes,omitempty"`
	SupportsMask      bool     `json:"supportsMask,omitempty" yaml:"supportsMask,omitempty"`
	SupportsWatermark bool     `json:"supportsWatermark,omitempty" yaml:"supportsWatermark,omitempty"`
	MaxInputImages    int      `json:"maxInputImages,omitempty" yaml:"maxInputImages,omitempty"`
}

type ImageModelCatalogEntry struct {
	Provider string `json:"provider,omitempty" yaml:"provider,omitempty"`
	Model    string `json:"model,omitempty" yaml:"model,omitempty"`
	Label    string `json:"label,omitempty" yaml:"label,omitempty"`
	ImageModelCapability
}

type ResolvedImageModel struct {
	Provider      string
	Model         string
	Label         string
	ImageProtocol string
	Capability    ImageModelCapability
}

type ResolvedImageGenerationWorker struct {
	PollIntervalMS         int
	MaxConcurrent          int
	MaxAttempts            int
	HeartbeatSeconds       int
	LeaseSeconds           int
	FinalizeTimeoutSeconds int
	MaxQueuedPerTenant     int
	MaxQueuedPerUser       int
}

// ImageGenerationSettings controls the project-native Images API integration.
// The provider field references an entry in fallback.providers; credentials and
// endpoint are deliberately not duplicated here.
type ImageGenerationSettings struct {
	Enabled                 *bool                          `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	PreviewInContext        *bool                          `json:"previewInContext,omitempty" yaml:"previewInContext,omitempty"`
	Provider                string                         `json:"provider,omitempty" yaml:"provider,omitempty"`
	Model                   string                         `json:"model,omitempty" yaml:"model,omitempty"`
	DefaultProvider         string                         `json:"defaultProvider,omitempty" yaml:"defaultProvider,omitempty"`
	DefaultModel            string                         `json:"defaultModel,omitempty" yaml:"defaultModel,omitempty"`
	Catalog                 []ImageModelCatalogEntry       `json:"catalog,omitempty" yaml:"catalog,omitempty"`
	Quality                 string                         `json:"quality,omitempty" yaml:"quality,omitempty"`
	Size                    string                         `json:"size,omitempty" yaml:"size,omitempty"`
	OutputFormat            string                         `json:"outputFormat,omitempty" yaml:"outputFormat,omitempty"`
	Background              string                         `json:"background,omitempty" yaml:"background,omitempty"`
	MaxImages               int                            `json:"maxImages,omitempty" yaml:"maxImages,omitempty"`
	MaxPromptChars          int                            `json:"maxPromptChars,omitempty" yaml:"maxPromptChars,omitempty"`
	MaxInputBytes           int64                          `json:"maxInputBytes,omitempty" yaml:"maxInputBytes,omitempty"`
	MaxConcurrent           int                            `json:"maxConcurrent,omitempty" yaml:"maxConcurrent,omitempty"`
	TimeoutSeconds          int                            `json:"timeoutSeconds,omitempty" yaml:"timeoutSeconds,omitempty"`
	RetentionDays           int                            `json:"retentionDays,omitempty" yaml:"retentionDays,omitempty"`
	AsyncChannelEnabled     *bool                          `json:"asyncChannelEnabled,omitempty" yaml:"asyncChannelEnabled,omitempty"`
	AsyncChannelAccountKeys []string                       `json:"asyncChannelAccountKeys,omitempty" yaml:"asyncChannelAccountKeys,omitempty"`
	Worker                  *ImageGenerationWorkerSettings `json:"worker,omitempty" yaml:"worker,omitempty"`
}

// ResolvedImageGeneration is the validated, runtime-ready image configuration.
// API credentials are kept in memory only and must never be serialized.
type ResolvedImageGeneration struct {
	Enabled                 bool
	PreviewInContext        bool
	Provider                string
	Model                   string
	Quality                 string
	Size                    string
	OutputFormat            string
	Background              string
	MaxImages               int
	MaxPromptChars          int
	MaxInputBytes           int64
	MaxConcurrent           int
	TimeoutSeconds          int
	RetentionDays           int
	BaseURL                 string
	APIKey                  string
	AuthToken               string
	Protocol                ProviderProtocol
	Catalog                 []ResolvedImageModel
	Providers               []ResolvedImageProvider
	AsyncChannelEnabled     bool
	AsyncChannelAccountKeys []string
	Worker                  ResolvedImageGenerationWorker
}

type ResolvedImageProvider struct {
	Name             string
	BaseURL          string
	APIKey           string
	AuthToken        string
	ImageProtocol    string
	ImageResultHosts []string
}

// ResolveImageGeneration loads and validates image settings for cwd. Disabled
// configuration is a valid no-op and does not require a provider.
func ResolveImageGeneration(cwd string) (ResolvedImageGeneration, error) {
	return ResolveImageGenerationWithSettings(cwd, LoadSettings(cwd).Settings)
}

// ResolveImageGenerationWithSettings validates image generation against an
// already merged settings snapshot. Callers that accept explicit runtime
// settings must merge them before calling this function so the supplied
// snapshot takes precedence over ambient project/global configuration.
func ResolveImageGenerationWithSettings(cwd string, settings Settings) (ResolvedImageGeneration, error) {
	image := settings.ImageGeneration
	resolved := ResolvedImageGeneration{
		Enabled:          image != nil && ResolveImageGenerationEnabled(image.Enabled),
		PreviewInContext: DefaultImageGenerationPreviewInContext,
		Model:            DefaultImageGenerationModel,
		Quality:          DefaultImageGenerationQuality,
		Size:             DefaultImageGenerationSize,
		OutputFormat:     DefaultImageGenerationOutputFormat,
		Background:       DefaultImageGenerationBackground,
		MaxImages:        DefaultImageGenerationMaxImages,
		MaxPromptChars:   DefaultImageGenerationMaxPromptChars,
		MaxInputBytes:    DefaultImageGenerationMaxInputBytes,
		MaxConcurrent:    DefaultImageGenerationMaxConcurrent,
		TimeoutSeconds:   DefaultImageGenerationTimeoutSeconds,
		RetentionDays:    DefaultImageGenerationRetentionDays,
		Worker: ResolvedImageGenerationWorker{
			PollIntervalMS:         DefaultImageWorkerPollIntervalMS,
			MaxConcurrent:          DefaultImageWorkerMaxConcurrent,
			MaxAttempts:            DefaultImageWorkerMaxAttempts,
			HeartbeatSeconds:       DefaultImageWorkerHeartbeatSeconds,
			FinalizeTimeoutSeconds: DefaultImageWorkerFinalizeTimeout,
			MaxQueuedPerTenant:     DefaultImageWorkerMaxQueuedPerTenant,
			MaxQueuedPerUser:       DefaultImageWorkerMaxQueuedPerUser,
		},
	}
	if image != nil {
		resolved.PreviewInContext = ResolveImageGenerationEnabled(image.PreviewInContext)
		resolved.AsyncChannelEnabled = ResolveImageGenerationEnabled(image.AsyncChannelEnabled)
		resolved.AsyncChannelAccountKeys = append([]string(nil), image.AsyncChannelAccountKeys...)
		resolved.Provider = strings.TrimSpace(firstNonEmpty(image.DefaultProvider, image.Provider))
		if resolved.Provider == "" && len(image.Catalog) > 0 {
			resolved.Provider = strings.TrimSpace(image.Catalog[0].Provider)
		}
		if strings.TrimSpace(image.Model) != "" {
			resolved.Model = strings.TrimSpace(image.Model)
		}
		if strings.TrimSpace(image.DefaultModel) != "" {
			resolved.Model = strings.TrimSpace(image.DefaultModel)
		}
		if resolved.Model == DefaultImageGenerationModel && len(image.Catalog) > 0 && strings.TrimSpace(image.DefaultModel) == "" && strings.TrimSpace(image.Model) == "" {
			resolved.Model = strings.TrimSpace(image.Catalog[0].Model)
		}
		if strings.TrimSpace(image.Quality) != "" {
			resolved.Quality = strings.ToLower(strings.TrimSpace(image.Quality))
		}
		if strings.TrimSpace(image.Size) != "" {
			resolved.Size = strings.ToLower(strings.TrimSpace(image.Size))
		}
		if strings.TrimSpace(image.OutputFormat) != "" {
			resolved.OutputFormat = strings.ToLower(strings.TrimSpace(image.OutputFormat))
		}
		if strings.TrimSpace(image.Background) != "" {
			resolved.Background = strings.ToLower(strings.TrimSpace(image.Background))
		}
		if image.MaxImages != 0 {
			resolved.MaxImages = image.MaxImages
		}
		if image.MaxPromptChars != 0 {
			resolved.MaxPromptChars = image.MaxPromptChars
		}
		if image.MaxInputBytes != 0 {
			resolved.MaxInputBytes = image.MaxInputBytes
		}
		if image.MaxConcurrent != 0 {
			resolved.MaxConcurrent = image.MaxConcurrent
		}
		if image.TimeoutSeconds != 0 {
			resolved.TimeoutSeconds = image.TimeoutSeconds
		}
		if image.RetentionDays != 0 {
			resolved.RetentionDays = image.RetentionDays
		}
		applyImageWorkerSettings(&resolved.Worker, image.Worker)
	}
	if resolved.Worker.LeaseSeconds == 0 {
		resolved.Worker.LeaseSeconds = resolved.TimeoutSeconds + DefaultImageWorkerLeaseGraceSeconds
	}
	if !resolved.Enabled {
		return resolved, nil
	}
	if err := validateResolvedImageGeneration(resolved); err != nil {
		return ResolvedImageGeneration{}, err
	}
	if err := validateImageCatalog(image); err != nil {
		return ResolvedImageGeneration{}, err
	}
	// Chat fallback can be disabled by a project profile while a global image
	// provider remains intentionally enabled. Resolve the provider definition
	// directly from merged settings instead of the chat-only filtered list.
	var provider *ProviderConfig
	if settings.Fallback != nil {
		for i := range settings.Fallback.Providers {
			candidate := settings.Fallback.Providers[i]
			candidate.Name = strings.TrimSpace(candidate.Name)
			candidate.BaseURL = strings.TrimRight(strings.TrimSpace(candidate.BaseURL), "/")
			candidate.APIKey = strings.TrimSpace(candidate.APIKey)
			candidate.AuthToken = strings.TrimSpace(candidate.AuthToken)
			if strings.EqualFold(candidate.Name, resolved.Provider) {
				provider = &candidate
				break
			}
		}
	}
	if provider == nil {
		return ResolvedImageGeneration{}, fmt.Errorf("image generation provider %q not found", resolved.Provider)
	}
	if strings.TrimSpace(provider.BaseURL) == "" {
		return ResolvedImageGeneration{}, fmt.Errorf("image generation provider %q endpoint missing", resolved.Provider)
	}
	if strings.TrimSpace(provider.APIKey) == "" && strings.TrimSpace(provider.AuthToken) == "" {
		return ResolvedImageGeneration{}, fmt.Errorf("image generation provider %q credential missing", resolved.Provider)
	}
	resolved.BaseURL = strings.TrimRight(strings.TrimSpace(provider.BaseURL), "/")
	resolved.APIKey = provider.APIKey
	resolved.AuthToken = provider.AuthToken
	resolved.Protocol = provider.Protocol
	resolved.Catalog = resolveImageCatalog(image, settings.Fallback.Providers, resolved.Provider, resolved.Model)
	if len(resolved.Catalog) == 0 {
		resolved.Catalog = []ResolvedImageModel{{Provider: resolved.Provider, Model: resolved.Model, ImageProtocol: imageProtocolForProvider(*provider)}}
	}
	providerNames := make([]string, 0, len(resolved.Catalog))
	seenProviders := map[string]struct{}{}
	for _, entry := range resolved.Catalog {
		name := strings.TrimSpace(entry.Provider)
		key := strings.ToLower(name)
		if name == "" {
			continue
		}
		if _, ok := seenProviders[key]; ok {
			continue
		}
		seenProviders[key] = struct{}{}
		providerNames = append(providerNames, name)
	}
	for _, name := range providerNames {
		for _, candidate := range settings.Fallback.Providers {
			candidate.Name = strings.TrimSpace(candidate.Name)
			if !strings.EqualFold(candidate.Name, name) {
				continue
			}
			candidate.BaseURL = strings.TrimRight(strings.TrimSpace(candidate.BaseURL), "/")
			candidate.APIKey = strings.TrimSpace(candidate.APIKey)
			candidate.AuthToken = strings.TrimSpace(candidate.AuthToken)
			if candidate.BaseURL == "" || (candidate.APIKey == "" && candidate.AuthToken == "") {
				return ResolvedImageGeneration{}, fmt.Errorf("image generation provider %q endpoint or credential missing", name)
			}
			resolved.Providers = append(resolved.Providers, ResolvedImageProvider{Name: candidate.Name, BaseURL: candidate.BaseURL, APIKey: candidate.APIKey, AuthToken: candidate.AuthToken, ImageProtocol: imageProtocolForProvider(candidate), ImageResultHosts: append([]string(nil), candidate.ImageResultHosts...)})
			break
		}
	}
	return resolved, nil
}

func imageProtocolForProvider(provider ProviderConfig) string {
	protocol := strings.ToLower(strings.TrimSpace(provider.ImageProtocol))
	if protocol == "" {
		return ImageProtocolOpenAI
	}
	return protocol
}

func resolveImageCatalog(settings *ImageGenerationSettings, providers []ProviderConfig, defaultProvider, defaultModel string) []ResolvedImageModel {
	if settings == nil || len(settings.Catalog) == 0 {
		return nil
	}
	providerProtocols := make(map[string]string, len(providers))
	for _, provider := range providers {
		name := strings.TrimSpace(provider.Name)
		if name != "" {
			providerProtocols[strings.ToLower(name)] = imageProtocolForProvider(provider)
		}
	}
	resolved := make([]ResolvedImageModel, 0, len(settings.Catalog))
	for _, entry := range settings.Catalog {
		provider := strings.TrimSpace(entry.Provider)
		model := strings.TrimSpace(entry.Model)
		if provider == "" {
			provider = defaultProvider
		}
		if model == "" {
			model = defaultModel
		}
		if _, ok := providerProtocols[strings.ToLower(provider)]; !ok {
			continue
		}
		resolved = append(resolved, ResolvedImageModel{Provider: provider, Model: model, Label: strings.TrimSpace(entry.Label), ImageProtocol: providerProtocols[strings.ToLower(provider)], Capability: cloneImageCapability(entry.ImageModelCapability)})
	}
	return resolved
}

func cloneImageCapability(capability ImageModelCapability) ImageModelCapability {
	capability.Operations = append([]string(nil), capability.Operations...)
	capability.Resolutions = append([]string(nil), capability.Resolutions...)
	capability.AspectRatios = append([]string(nil), capability.AspectRatios...)
	capability.Sizes = append([]string(nil), capability.Sizes...)
	capability.QualityOptions = append([]string(nil), capability.QualityOptions...)
	capability.OutputFormats = append([]string(nil), capability.OutputFormats...)
	capability.ResponseModes = append([]string(nil), capability.ResponseModes...)
	return capability
}

func ResolveImageGenerationEnabled(value *bool) bool {
	return value != nil && *value
}

func (r ResolvedImageGeneration) AsyncChannelEnabledForAccount(accountKey string) bool {
	if !r.AsyncChannelEnabled || strings.TrimSpace(accountKey) == "" {
		return false
	}
	if len(r.AsyncChannelAccountKeys) == 0 {
		return true
	}
	for _, candidate := range r.AsyncChannelAccountKeys {
		if candidate == accountKey {
			return true
		}
	}
	return false
}

func applyImageWorkerSettings(resolved *ResolvedImageGenerationWorker, settings *ImageGenerationWorkerSettings) {
	if resolved == nil || settings == nil {
		return
	}
	if settings.PollIntervalMS != 0 {
		resolved.PollIntervalMS = settings.PollIntervalMS
	}
	if settings.MaxConcurrent != 0 {
		resolved.MaxConcurrent = settings.MaxConcurrent
	}
	if settings.MaxAttempts != 0 {
		resolved.MaxAttempts = settings.MaxAttempts
	}
	if settings.HeartbeatSeconds != 0 {
		resolved.HeartbeatSeconds = settings.HeartbeatSeconds
	}
	if settings.LeaseSeconds != 0 {
		resolved.LeaseSeconds = settings.LeaseSeconds
	}
	if settings.FinalizeTimeoutSeconds != 0 {
		resolved.FinalizeTimeoutSeconds = settings.FinalizeTimeoutSeconds
	}
	if settings.MaxQueuedPerTenant != 0 {
		resolved.MaxQueuedPerTenant = settings.MaxQueuedPerTenant
	}
	if settings.MaxQueuedPerUser != 0 {
		resolved.MaxQueuedPerUser = settings.MaxQueuedPerUser
	}
}

func validateResolvedImageGeneration(value ResolvedImageGeneration) error {
	switch value.Quality {
	case "auto", "low", "medium", "high":
	default:
		return fmt.Errorf("image generation quality %q is invalid", value.Quality)
	}
	switch value.Size {
	case "auto", "1024x1024", "1536x1024", "1024x1536":
	default:
		return fmt.Errorf("image generation size %q is invalid", value.Size)
	}
	switch value.OutputFormat {
	case "png", "jpeg", "webp":
	default:
		return fmt.Errorf("image generation outputFormat %q is invalid", value.OutputFormat)
	}
	switch value.Background {
	case "auto", "transparent", "opaque":
	default:
		return fmt.Errorf("image generation background %q is invalid", value.Background)
	}
	if value.MaxImages < 1 {
		return fmt.Errorf("image generation maxImages must be positive")
	}
	if value.MaxPromptChars < 1 {
		return fmt.Errorf("image generation maxPromptChars must be positive")
	}
	if value.MaxInputBytes < 1 {
		return fmt.Errorf("image generation maxInputBytes must be positive")
	}
	if value.MaxConcurrent < 1 {
		return fmt.Errorf("image generation maxConcurrent must be positive")
	}
	if value.TimeoutSeconds < 1 {
		return fmt.Errorf("image generation timeoutSeconds must be positive")
	}
	if value.RetentionDays < 0 {
		return fmt.Errorf("image generation retentionDays cannot be negative")
	}
	if err := validateResolvedImageWorker(value.Worker, value.TimeoutSeconds); err != nil {
		return err
	}
	for _, accountKey := range value.AsyncChannelAccountKeys {
		if accountKey == "" || strings.TrimSpace(accountKey) != accountKey {
			return fmt.Errorf("image generation asyncChannelAccountKeys must contain non-empty exact account keys")
		}
	}
	return nil
}

func validateImageCatalog(settings *ImageGenerationSettings) error {
	if settings == nil || len(settings.Catalog) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(settings.Catalog))
	for i, entry := range settings.Catalog {
		provider := strings.TrimSpace(entry.Provider)
		model := strings.TrimSpace(entry.Model)
		if provider == "" || model == "" {
			return fmt.Errorf("image generation catalog entry %d requires provider and model", i)
		}
		key := strings.ToLower(provider) + "\x00" + strings.ToLower(model)
		if _, ok := seen[key]; ok {
			return fmt.Errorf("image generation catalog has duplicate provider/model %q/%q", provider, model)
		}
		seen[key] = struct{}{}
		for _, operation := range entry.Operations {
			switch strings.ToLower(strings.TrimSpace(operation)) {
			case "generate", "edit", "compose":
			default:
				return fmt.Errorf("image generation catalog operation %q is invalid", operation)
			}
		}
		for _, resolution := range entry.Resolutions {
			value := strings.ToUpper(strings.TrimSpace(resolution))
			switch value {
			case "1K", "2K", "3K", "4K":
			default:
				return fmt.Errorf("image generation catalog resolution %q is invalid", resolution)
			}
		}
	}
	return nil
}

func validateResolvedImageWorker(worker ResolvedImageGenerationWorker, attemptTimeoutSeconds int) error {
	checks := []struct {
		name  string
		value int
	}{
		{name: "pollIntervalMs", value: worker.PollIntervalMS},
		{name: "maxConcurrent", value: worker.MaxConcurrent},
		{name: "maxAttempts", value: worker.MaxAttempts},
		{name: "heartbeatSeconds", value: worker.HeartbeatSeconds},
		{name: "leaseSeconds", value: worker.LeaseSeconds},
		{name: "finalizeTimeoutSeconds", value: worker.FinalizeTimeoutSeconds},
		{name: "maxQueuedPerTenant", value: worker.MaxQueuedPerTenant},
		{name: "maxQueuedPerUser", value: worker.MaxQueuedPerUser},
	}
	for _, check := range checks {
		if check.value < 1 {
			return fmt.Errorf("image generation worker %s must be positive", check.name)
		}
	}
	if worker.HeartbeatSeconds >= worker.LeaseSeconds {
		return fmt.Errorf("image generation worker heartbeatSeconds must be less than leaseSeconds")
	}
	if worker.LeaseSeconds < attemptTimeoutSeconds {
		return fmt.Errorf("image generation worker leaseSeconds must be at least timeoutSeconds")
	}
	return nil
}

// Validate checks image option values without resolving a provider.
func (s ImageGenerationSettings) Validate() error {
	resolved := ResolvedImageGeneration{
		PreviewInContext:        ResolveImageGenerationEnabled(s.PreviewInContext),
		Quality:                 firstNonEmpty(strings.ToLower(strings.TrimSpace(s.Quality)), DefaultImageGenerationQuality),
		Size:                    firstNonEmpty(strings.ToLower(strings.TrimSpace(s.Size)), DefaultImageGenerationSize),
		OutputFormat:            firstNonEmpty(strings.ToLower(strings.TrimSpace(s.OutputFormat)), DefaultImageGenerationOutputFormat),
		Background:              firstNonEmpty(strings.ToLower(strings.TrimSpace(s.Background)), DefaultImageGenerationBackground),
		MaxImages:               s.MaxImages,
		MaxPromptChars:          s.MaxPromptChars,
		MaxInputBytes:           s.MaxInputBytes,
		MaxConcurrent:           s.MaxConcurrent,
		TimeoutSeconds:          s.TimeoutSeconds,
		RetentionDays:           s.RetentionDays,
		AsyncChannelEnabled:     ResolveImageGenerationEnabled(s.AsyncChannelEnabled),
		AsyncChannelAccountKeys: append([]string(nil), s.AsyncChannelAccountKeys...),
		Worker: ResolvedImageGenerationWorker{
			PollIntervalMS:         DefaultImageWorkerPollIntervalMS,
			MaxConcurrent:          DefaultImageWorkerMaxConcurrent,
			MaxAttempts:            DefaultImageWorkerMaxAttempts,
			HeartbeatSeconds:       DefaultImageWorkerHeartbeatSeconds,
			FinalizeTimeoutSeconds: DefaultImageWorkerFinalizeTimeout,
			MaxQueuedPerTenant:     DefaultImageWorkerMaxQueuedPerTenant,
			MaxQueuedPerUser:       DefaultImageWorkerMaxQueuedPerUser,
		},
	}
	if resolved.MaxImages == 0 {
		resolved.MaxImages = DefaultImageGenerationMaxImages
	}
	if resolved.MaxPromptChars == 0 {
		resolved.MaxPromptChars = DefaultImageGenerationMaxPromptChars
	}
	if resolved.MaxInputBytes == 0 {
		resolved.MaxInputBytes = DefaultImageGenerationMaxInputBytes
	}
	if resolved.MaxConcurrent == 0 {
		resolved.MaxConcurrent = DefaultImageGenerationMaxConcurrent
	}
	if resolved.TimeoutSeconds == 0 {
		resolved.TimeoutSeconds = DefaultImageGenerationTimeoutSeconds
	}
	applyImageWorkerSettings(&resolved.Worker, s.Worker)
	if resolved.Worker.LeaseSeconds == 0 {
		resolved.Worker.LeaseSeconds = resolved.TimeoutSeconds + DefaultImageWorkerLeaseGraceSeconds
	}
	return validateResolvedImageGeneration(resolved)
}

func (r ResolvedImageGeneration) Validate() error {
	return validateResolvedImageGeneration(r)
}

type Settings struct {
	ComputerUse        *ComputerUseSettings       `json:"computerUse,omitempty" yaml:"computerUse,omitempty"`
	Model              string                     `json:"model,omitempty" yaml:"model,omitempty"`
	ModelOptions       []string                   `json:"modelOptions,omitempty" yaml:"modelOptions,omitempty"`
	SubagentModelTiers map[string]string          `json:"subagentModelTiers,omitempty" yaml:"subagentModelTiers,omitempty"`
	ModelPricing       map[string]ModelPrice      `json:"modelPricing,omitempty" yaml:"modelPricing,omitempty"`
	Provider           string                     `json:"provider,omitempty" yaml:"provider,omitempty"`
	ProviderProtocol   ProviderProtocol           `json:"providerProtocol,omitempty" yaml:"providerProtocol,omitempty"`
	Responses          *ResponsesProviderSettings `json:"responses,omitempty" yaml:"responses,omitempty"`
	BaseURL            string                     `json:"baseURL,omitempty" yaml:"baseURL,omitempty"`
	APIKey             string                     `json:"apiKey,omitempty" yaml:"apiKey,omitempty"`
	AuthToken          string                     `json:"authToken,omitempty" yaml:"authToken,omitempty"`
	ContextLength      int                        `json:"contextLength,omitempty" yaml:"context_length,omitempty"`
	OutputStyle        string                     `json:"outputStyle,omitempty" yaml:"outputStyle,omitempty"`
	Language           string                     `json:"language,omitempty" yaml:"language,omitempty"`
	// Effort enables extended thinking / reasoning for the main query loop.
	// An empty value uses the runtime default (high); "off"/"none" disables
	// it; "low"/"medium"/"high"/"max" pick a budget tier; a bare number is a
	// raw token budget. Skills that declare their own effort override this per
	// invocation.
	Effort                string                     `json:"effort,omitempty" yaml:"effort,omitempty"`
	Env                   map[string]string          `json:"env,omitempty" yaml:"env,omitempty"`
	FeatureFlags          map[string]any             `json:"featureFlags,omitempty" yaml:"featureFlags,omitempty"`
	GrowthBook            *GrowthBookSettings        `json:"growthbook,omitempty" yaml:"growthbook,omitempty"`
	Permissions           PermissionSettings         `json:"permissions,omitempty" yaml:"permissions,omitempty"`
	Sandbox               *SandboxSettings           `json:"sandbox,omitempty" yaml:"sandbox,omitempty"`
	Fallback              *FallbackSettings          `json:"fallback,omitempty" yaml:"fallback,omitempty"`
	Update                *UpdateSettings            `json:"update,omitempty" yaml:"update,omitempty"`
	Multimodal            *MultimodalSettings        `json:"multimodal,omitempty" yaml:"multimodal,omitempty"`
	AutoCompact           *AutoCompactSettings       `json:"autoCompact,omitempty" yaml:"autoCompact,omitempty"`
	Recap                 *RecapSettings             `json:"recap,omitempty" yaml:"recap,omitempty"`
	NextSteps             *NextStepsSettings         `json:"nextSteps,omitempty" yaml:"nextSteps,omitempty"`
	TUI                   *TUISettings               `json:"tui,omitempty" yaml:"tui,omitempty"`
	WebAgentUI            *WebAgentUISettings        `json:"webAgentUI,omitempty" yaml:"webAgentUI,omitempty"`
	WebSearch             *WebSearchSettings         `json:"webSearch,omitempty" yaml:"webSearch,omitempty"`
	ImageGeneration       *ImageGenerationSettings   `json:"imageGeneration,omitempty" yaml:"imageGeneration,omitempty"`
	Init                  *InitSettings              `json:"init,omitempty" yaml:"init,omitempty"`
	Identity              *identity.Settings         `json:"identity,omitempty" yaml:"identity,omitempty"`
	MCPServers            map[string]MCPServerConfig `json:"mcpServers,omitempty" yaml:"mcpServers,omitempty"`
	Hooks                 map[string][]HookCommand   `json:"hooks,omitempty" yaml:"hooks,omitempty"`
	AdditionalDirectories []string                   `json:"additionalDirectories,omitempty" yaml:"additionalDirectories,omitempty"`
	MaxToolResultBytes    int                        `json:"maxToolResultBytes,omitempty" yaml:"maxToolResultBytes,omitempty"`
	FileHistory           *FileHistorySettings       `json:"fileHistory,omitempty" yaml:"fileHistory,omitempty"`
}

// FileHistorySettings bounds the retention of the file-content blob store,
// independently of conversation transcripts. All fields are optional pointers so
// an unset config falls back to defaults (see ResolvedFileHistory).
type FileHistorySettings struct {
	// MaxTurns keeps file history for the most recent N turns (checkpoint-delimited).
	MaxTurns *int `json:"maxTurns,omitempty" yaml:"maxTurns,omitempty"`
	// MaxAgeDays additionally drops history older than N days. 0 disables.
	MaxAgeDays *int `json:"maxAgeDays,omitempty" yaml:"maxAgeDays,omitempty"`
	// MaxBytes is a hard cap on total file-history bytes; oldest is evicted first.
	MaxBytes *int64 `json:"maxBytes,omitempty" yaml:"maxBytes,omitempty"`
}

// File-history retention defaults: turn-count is the primary window, a byte cap
// is the safety net, and age is off by default (a compliance-only lever).
const (
	DefaultFileHistoryMaxTurns   = 20
	DefaultFileHistoryMaxAgeDays = 0
	DefaultFileHistoryMaxBytes   = int64(2) << 30 // 2 GiB
)

// ResolvedFileHistory returns the effective retention window, applying defaults
// for any unset field.
func (s Settings) ResolvedFileHistory() (maxTurns, maxAgeDays int, maxBytes int64) {
	maxTurns = DefaultFileHistoryMaxTurns
	maxAgeDays = DefaultFileHistoryMaxAgeDays
	maxBytes = DefaultFileHistoryMaxBytes
	if s.FileHistory != nil {
		if s.FileHistory.MaxTurns != nil {
			maxTurns = *s.FileHistory.MaxTurns
		}
		if s.FileHistory.MaxAgeDays != nil {
			maxAgeDays = *s.FileHistory.MaxAgeDays
		}
		if s.FileHistory.MaxBytes != nil {
			maxBytes = *s.FileHistory.MaxBytes
		}
	}
	return maxTurns, maxAgeDays, maxBytes
}

func (s *Settings) UnmarshalYAML(value *yaml.Node) error {
	type alias Settings
	var raw struct {
		alias             `yaml:",inline"`
		ModelOptionsSnake []string `yaml:"model_options,omitempty"`
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	*s = Settings(raw.alias)
	s.ModelOptions = mergeStringList(s.ModelOptions, raw.ModelOptionsSnake)
	return nil
}

type GrowthBookSettings struct {
	Enabled                *bool             `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	URL                    string            `json:"url,omitempty" yaml:"url,omitempty"`
	Headers                map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`
	RefreshIntervalSeconds int               `json:"refreshIntervalSeconds,omitempty" yaml:"refreshIntervalSeconds,omitempty"`
	Features               map[string]any    `json:"features,omitempty" yaml:"features,omitempty"`
	Overrides              map[string]any    `json:"overrides,omitempty" yaml:"overrides,omitempty"`
}

type HookCommand struct {
	Command string   `json:"command" yaml:"command"`
	Matcher string   `json:"matcher,omitempty" yaml:"matcher,omitempty"`
	Tool    string   `json:"tool,omitempty" yaml:"tool,omitempty"`
	Tools   []string `json:"tools,omitempty" yaml:"tools,omitempty"`
}

func (h *HookCommand) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		h.Command = text
		return nil
	}
	type alias HookCommand
	var raw alias
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*h = HookCommand(raw)
	return nil
}

func (h *HookCommand) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		h.Command = value.Value
		return nil
	}
	type alias HookCommand
	var raw alias
	if err := value.Decode(&raw); err != nil {
		return err
	}
	*h = HookCommand(raw)
	return nil
}

type LoadedSettings struct {
	Settings
	Sources []string `json:"sources,omitempty"`
}

func LoadSettings(cwd string) LoadedSettings {
	var loaded LoadedSettings
	for _, path := range settingsSearchPaths(cwd) {
		settings, ok := readSettings(path)
		if !ok {
			continue
		}
		annotatePermissionSources(&settings, permissionSourceForPath(cwd, path))
		loaded.Settings = mergeSettings(loaded.Settings, settings)
		loaded.Sources = append(loaded.Sources, path)
	}
	return loaded
}

func AnnotatePermissionSources(settings *Settings, source string) {
	annotatePermissionSources(settings, source)
}

func GlobalSettingsPath() (string, error) {
	return identity.Default().GlobalStatePath("settings.json")
}

func SaveGlobalSettings(settings Settings) error {
	_, err := GlobalSettingsPath()
	if err != nil {
		return err
	}
	data, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	var next map[string]any
	if err := json.Unmarshal(data, &next); err != nil {
		return err
	}
	if previous, _, exists, readErr := ReadGlobalSettings(); readErr != nil {
		return readErr
	} else if exists {
		var old map[string]any
		if err := json.Unmarshal(previous, &old); err != nil || old == nil {
			return fmt.Errorf("global settings.json must contain a JSON object")
		}
		next = PreserveUnknownJSONFields(old, next)
	}
	data, err = json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	_, err = WriteGlobalSettingsRaw(append(data, '\n'))
	return err
}

// PreserveUnknownJSONFields copies fields unknown to the typed Settings schema
// from old into next. Known fields intentionally remain controlled by next, so
// callers can remove optional settings while forward-compatible fields survive
// a read-modify-write cycle.
func PreserveUnknownJSONFields(old, next map[string]any) map[string]any {
	return preserveUnknownJSONFields(old, next, reflect.TypeOf(Settings{}))
}

func preserveUnknownJSONFields(old, next map[string]any, schema reflect.Type) map[string]any {
	out := make(map[string]any, len(next)+len(old))
	for key, value := range next {
		out[key] = value
	}
	schema = indirectJSONType(schema)
	known := jsonFieldSchema(schema)
	for key, value := range old {
		current, exists := out[key]
		fieldType, knownField := known[key]
		if !knownField {
			if !exists {
				out[key] = value
			} else if merged, ok := mergeUnknownJSONValue(value, current, nil); ok {
				out[key] = merged
			}
			continue
		}
		if exists {
			if merged, ok := mergeUnknownJSONValue(value, current, fieldType); ok {
				out[key] = merged
			}
		}
	}
	return out
}

func mergeUnknownJSONValue(old, next any, schema reflect.Type) (any, bool) {
	schema = indirectJSONType(schema)
	switch oldValue := old.(type) {
	case map[string]any:
		nextValue, ok := next.(map[string]any)
		if !ok {
			return next, false
		}
		if schema != nil && schema.Kind() == reflect.Map {
			elementType := schema.Elem()
			for key, oldChild := range oldValue {
				nextChild, exists := nextValue[key]
				if !exists {
					continue
				}
				if merged, ok := mergeUnknownJSONValue(oldChild, nextChild, elementType); ok {
					nextValue[key] = merged
				}
			}
			return nextValue, true
		}
		if schema != nil && schema.Kind() != reflect.Struct {
			return next, false
		}
		return preserveUnknownJSONFields(oldValue, nextValue, schema), true
	case []any:
		nextValue, ok := next.([]any)
		if !ok {
			return next, false
		}
		elementType := reflect.Type(nil)
		if schema != nil && schema.Kind() == reflect.Slice {
			elementType = schema.Elem()
		}
		for index := 0; index < len(oldValue) && index < len(nextValue); index++ {
			if merged, ok := mergeUnknownJSONValue(oldValue[index], nextValue[index], elementType); ok {
				nextValue[index] = merged
			}
		}
		return nextValue, true
	default:
		return next, false
	}
}

func indirectJSONType(value reflect.Type) reflect.Type {
	for value != nil && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) {
		value = value.Elem()
	}
	return value
}

func jsonFieldSchema(schema reflect.Type) map[string]reflect.Type {
	schema = indirectJSONType(schema)
	if schema == nil || schema.Kind() != reflect.Struct {
		return nil
	}
	fields := make(map[string]reflect.Type)
	for index := 0; index < schema.NumField(); index++ {
		field := schema.Field(index)
		if field.PkgPath != "" {
			continue
		}
		tag := field.Tag.Get("json")
		name := strings.Split(tag, ",")[0]
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		fields[name] = field.Type
	}
	return fields
}

// ReadGlobalSettings reads only the canonical global settings document. Legacy
// product files are intentionally ignored; migration must be explicit.
func ReadGlobalSettings() (data []byte, path string, ok bool, err error) {
	path, err = GlobalSettingsPath()
	if err != nil {
		return nil, "", false, err
	}
	data, err = os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, path, false, nil
		}
		return nil, path, false, err
	}
	return data, path, true, nil
}

// WriteGlobalSettingsRaw atomically writes raw JSON bytes to the global
// settings.json file, creating the config directory if needed. The bytes are
// written to a temp file in the same directory and renamed into place, so a
// crash mid-write cannot leave a truncated config behind. It returns the path
// that was written.
func WriteGlobalSettingsRaw(data []byte) (string, error) {
	path, err := GlobalSettingsPath()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".settings-*.json.tmp")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return "", err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return "", err
	}
	return path, nil
}

func SaveSettingsFile(path string, settings Settings) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0600)
}

func LoadSettingsFile(path string) Settings {
	settings, ok := readSettings(path)
	if !ok {
		return Settings{}
	}
	return settings
}

func ProjectSettingsPath(cwd string, local bool) string {
	return projectSettingsPathForIdentity(cwd, local, CurrentIdentity(cwd))
}

func CurrentIdentity(cwd string) identity.Identity {
	loadedSettings := LoadOwnedSettings(cwd)
	return identity.FromSettings(IdentitySettings(loadedSettings.Settings))
}

func projectSettingsPathForIdentity(cwd string, local bool, id identity.Identity) string {
	name := "settings.json"
	if local {
		name = "settings.local.json"
	}
	// The previous default is a migration source, never a write target. Custom
	// directory names remain supported, but an old generated identity must not
	// pin new writes to .niuma-legacy forever.
	if id.ConfigDirName == product.LegacyConfigDirName {
		id.ConfigDirName = identity.Default().ConfigDirName
	}
	return id.ProjectStatePath(cwd, name)
}

func LoadGlobalSettings() Settings {
	return LoadSettings("").Settings
}

func settingsSearchPaths(cwd string) []string {
	// 默认配置只来自全局文件；显式 --settings 仍由 CLI 单独合并。
	if path, err := GlobalSettingsPath(); err == nil && path != "" {
		return []string{path}
	}
	return nil

}

func LoadOwnedSettings(cwd string) LoadedSettings {
	var loaded LoadedSettings
	for _, path := range ownedSettingsSearchPaths(cwd) {
		settings, ok := readSettings(path)
		if !ok {
			continue
		}
		annotatePermissionSources(&settings, permissionSourceForPath(cwd, path))
		loaded.Settings = mergeSettings(loaded.Settings, settings)
		loaded.Sources = append(loaded.Sources, path)
	}
	return loaded
}

func ownedSettingsSearchPaths(cwd string) []string {
	// 身份解析与默认 runtime 共用同一来源，避免重新引入项目配置。
	return settingsSearchPaths(cwd)

}

func appendUniquePath(paths []string, path string) []string {
	clean := filepath.Clean(path)
	for _, existing := range paths {
		if filepath.Clean(existing) == clean {
			return paths
		}
	}
	return append(paths, path)
}

func EnsureProjectSettingsMaterialized(cwd string) error {
	if err := ensureProjectSettingsMaterialized(cwd, false); err != nil {
		return err
	}
	return ensureProjectSettingsMaterialized(cwd, true)
}

func ensureProjectSettingsMaterialized(cwd string, local bool) error {
	if strings.TrimSpace(cwd) == "" {
		return nil
	}
	target := ProjectSettingsPath(cwd, local)
	if _, err := os.Stat(target); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	dir, err := filepath.Abs(cwd)
	if err != nil {
		dir = cwd
	}
	name := "settings.json"
	if local {
		name = "settings.local.json"
	}
	var settings Settings
	found := false
	// Claude-compatible project settings remain the lowest-precedence source;
	// the previous product-owned directory overrides them, matching LoadSettings.
	for _, dirName := range []string{".claude", product.LegacyConfigDirName} {
		legacyDir := findNearestConfigDir(dir, dirName)
		if legacyDir == "" {
			continue
		}
		next, ok := readSettings(filepath.Join(legacyDir, name))
		if !ok {
			continue
		}
		settings = mergeSettings(settings, next)
		found = true
	}
	if !found {
		return nil
	}
	normalizeMaterializedIdentity(&settings)
	return SaveSettingsFile(target, settings)
}

func normalizeMaterializedIdentity(settings *Settings) {
	if settings == nil || settings.Identity == nil {
		return
	}
	defaultID := identity.Default()
	if strings.TrimSpace(settings.Identity.ConfigDirName) == "" ||
		settings.Identity.ConfigDirName == ".claude" ||
		settings.Identity.ConfigDirName == product.LegacyConfigDirName {
		settings.Identity.ConfigDirName = defaultID.ConfigDirName
	}
	if strings.TrimSpace(settings.Identity.ProductName) == "" {
		settings.Identity.ProductName = defaultID.ProductName
	}
	if strings.TrimSpace(settings.Identity.ProductKey) == "" {
		settings.Identity.ProductKey = defaultID.ProductKey
	}
	if strings.TrimSpace(settings.Identity.GuidanceFilename) == "" {
		settings.Identity.GuidanceFilename = defaultID.GuidanceFilename
	}
}

func LoadProjectConfigSettings(cwd string) Settings {
	// 禁止 ResolveModel 绕过全局入口自动加载项目 YAML；显式文件解析不变。
	return Settings{}

	/* 2026-09-09：按单一默认配置来源方案停用，保留旧逻辑供查阅。
	var settings Settings
	for _, path := range projectConfigSearchPaths(cwd) {
		next, ok := readSettings(path)
		if !ok {
			continue
		}
		annotatePermissionSources(&next, permissionSourceForPath(cwd, path))
		settings = mergeSettings(settings, next)
	}
	return settings
	*/
}

func IdentitySettings(settings Settings) identity.Settings {
	if settings.Identity == nil {
		return identity.Settings{}
	}
	return *settings.Identity
}

func projectConfigSearchPaths(cwd string) []string {
	if cwd == "" {
		return nil
	}
	dir, err := filepath.Abs(cwd)
	if err != nil {
		dir = cwd
	}
	configDir := findNearestProjectConfigDir(dir)
	if configDir == "" {
		configDir = filepath.Join(dir, "config")
	}
	paths := []string{filepath.Join(configDir, "config.yaml")}
	if env := configEnvironment(); env != "" {
		paths = append(paths, filepath.Join(configDir, "config."+env+".yaml"))
	}
	paths = append(paths, filepath.Join(configDir, "config.local.yaml"))
	return paths
}

func findNearestProjectConfigDir(start string) string {
	dir := start
	for {
		candidate := filepath.Join(dir, "config")
		if info, err := os.Stat(filepath.Join(candidate, "config.yaml")); err == nil && !info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func configEnvironment() string {
	for _, key := range []string{"NIUMA_ENV", "CLAUDE_CODE_ENV", "APP_ENV", "GO_ENV"} {
		value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
		switch value {
		case "dev", "test", "prod":
			return value
		}
	}
	return ""
}

func findNearestClaudeDir(start string) string {
	dir := start
	for {
		candidate := filepath.Join(dir, ".claude")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func findNearestGolangCCDir(start string) string {
	return findNearestConfigDir(start, identity.Default().ConfigDirName)
}

func findNearestConfigDir(start, name string) string {
	dir := start
	for {
		candidate := filepath.Join(dir, name)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func readSettings(path string) (Settings, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Settings{}, false
	}
	var settings Settings
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml":
		err = yaml.Unmarshal(data, &settings)
	default:
		err = json.Unmarshal(data, &settings)
	}
	if err != nil {
		return Settings{}, false
	}
	return settings, true
}

func (p *ProviderConfig) UnmarshalJSON(data []byte) error {
	var raw struct {
		Name             *string                    `json:"name"`
		Type             *string                    `json:"type"`
		Protocol         *ProviderProtocol          `json:"protocol"`
		ImageProtocol    *string                    `json:"imageProtocol"`
		ImageResultHosts []string                   `json:"imageResultHosts"`
		BaseURL          *string                    `json:"baseURL"`
		APIKey           *string                    `json:"apiKey"`
		AuthToken        *string                    `json:"authToken"`
		Model            *string                    `json:"model"`
		Responses        *ResponsesProviderSettings `json:"responses"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	p.applyRaw(raw.Name, raw.Type, raw.Protocol, raw.ImageProtocol, raw.ImageResultHosts, raw.BaseURL, raw.APIKey, raw.AuthToken, raw.Model, raw.Responses)
	return nil
}

func (p *ProviderConfig) UnmarshalYAML(value *yaml.Node) error {
	var raw struct {
		Name             *string                    `yaml:"name"`
		Type             *string                    `yaml:"type"`
		Protocol         *ProviderProtocol          `yaml:"protocol"`
		ImageProtocol    *string                    `yaml:"imageProtocol"`
		ImageResultHosts []string                   `yaml:"imageResultHosts"`
		BaseURL          *string                    `yaml:"baseURL"`
		APIKey           *string                    `yaml:"apiKey"`
		AuthToken        *string                    `yaml:"authToken"`
		Model            *string                    `yaml:"model"`
		Responses        *ResponsesProviderSettings `yaml:"responses"`
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	p.applyRaw(raw.Name, raw.Type, raw.Protocol, raw.ImageProtocol, raw.ImageResultHosts, raw.BaseURL, raw.APIKey, raw.AuthToken, raw.Model, raw.Responses)
	return nil
}

func (p *ProviderConfig) applyRaw(name, kind *string, protocol *ProviderProtocol, imageProtocol *string, imageResultHosts []string, baseURL, apiKey, authToken, model *string, responses *ResponsesProviderSettings) {
	if name != nil {
		p.Name = *name
	}
	if kind != nil {
		p.Type = *kind
	}
	if protocol != nil {
		p.Protocol = normalizeProviderProtocol(*protocol)
	}
	if imageProtocol != nil {
		p.ImageProtocol = strings.ToLower(strings.TrimSpace(*imageProtocol))
	}
	if imageResultHosts != nil {
		p.ImageResultHosts = append([]string(nil), imageResultHosts...)
	}
	if baseURL != nil {
		p.BaseURL = *baseURL
	}
	if apiKey != nil {
		p.APIKey = *apiKey
		p.apiKeySet = true
	}
	if authToken != nil {
		p.AuthToken = *authToken
		p.authTokenSet = true
	}
	if model != nil {
		p.Model = *model
	}
	if responses != nil {
		p.Responses = cloneResponsesProviderSettings(responses)
	}
}

func InitDetailLevel(settings Settings) string {
	value := ""
	if settings.Init != nil {
		value = settings.Init.DetailLevel
	}
	return normalizeInitDetailLevel(firstNonEmpty(os.Getenv("NIUMA_INIT_DETAIL_LEVEL"), value))
}

func normalizeInitDetailLevel(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "minimal", "balanced", "detailed":
		return normalized
	default:
		return "balanced"
	}
}

func MergeSettings(base, override Settings) Settings {
	return mergeSettings(base, override)
}

func mergeSettings(base, override Settings) Settings {
	// A provider switch starts a new route; protocol-specific state and endpoint
	// credentials from the previous route must not leak into its replacement.
	if base.Provider != "" && override.Provider != "" && NormalizeProviderKind(override.Provider) != NormalizeProviderKind(base.Provider) {
		base.Model, base.ProviderProtocol, base.Responses = "", "", nil
		base.BaseURL, base.APIKey, base.AuthToken = "", "", ""
	}
	if override.ProviderProtocol != "" && normalizeProviderProtocol(override.ProviderProtocol) != normalizeProviderProtocol(base.ProviderProtocol) {
		base.Responses = nil
	}
	if override.Model != "" {
		base.Model = override.Model
	}
	base.ModelOptions = mergeStringList(base.ModelOptions, override.ModelOptions)
	if override.Provider != "" {
		base.Provider = override.Provider
	}
	if override.ProviderProtocol != "" {
		base.ProviderProtocol = normalizeProviderProtocol(override.ProviderProtocol)
	}
	base.Responses = mergeResponsesProviderSettings(base.Responses, override.Responses)
	if override.BaseURL != "" {
		base.BaseURL = override.BaseURL
	}
	if override.APIKey != "" {
		base.APIKey = override.APIKey
	}
	if override.AuthToken != "" {
		base.AuthToken = override.AuthToken
	}
	if override.ContextLength != 0 {
		base.ContextLength = override.ContextLength
	}
	if override.OutputStyle != "" {
		base.OutputStyle = override.OutputStyle
	}
	if override.Language != "" {
		base.Language = override.Language
	}
	if override.Effort != "" {
		base.Effort = override.Effort
	}
	if override.MaxToolResultBytes != 0 {
		base.MaxToolResultBytes = override.MaxToolResultBytes
	}
	base.SubagentModelTiers = mergeMap(base.SubagentModelTiers, override.SubagentModelTiers)
	base.ModelPricing = mergeMap(base.ModelPricing, override.ModelPricing)
	base.Env = mergeMap(base.Env, override.Env)
	base.FeatureFlags = mergeAnyMap(base.FeatureFlags, override.FeatureFlags)
	base.GrowthBook = mergeGrowthBookSettings(base.GrowthBook, override.GrowthBook)
	base.Identity = mergeIdentitySettings(base.Identity, override.Identity)
	base.MCPServers = mergeMap(base.MCPServers, override.MCPServers)
	base.Hooks = mergeHookMap(base.Hooks, override.Hooks)
	base.AdditionalDirectories = append(base.AdditionalDirectories, override.AdditionalDirectories...)
	base.Permissions.Allow = append(base.Permissions.Allow, override.Permissions.Allow...)
	base.Permissions.Deny = append(base.Permissions.Deny, override.Permissions.Deny...)
	base.Permissions.Ask = append(base.Permissions.Ask, override.Permissions.Ask...)
	base.Permissions.AlwaysAsk = append(base.Permissions.AlwaysAsk, override.Permissions.AlwaysAsk...)
	base.Permissions.AdditionalDirectories = append(base.Permissions.AdditionalDirectories, override.Permissions.AdditionalDirectories...)
	base.AdditionalDirectories = append(base.AdditionalDirectories, override.Permissions.AdditionalDirectories...)
	base.Permissions.RuleSources = mergeMap(base.Permissions.RuleSources, override.Permissions.RuleSources)
	if override.Permissions.DefaultMode != "" {
		base.Permissions.DefaultMode = override.Permissions.DefaultMode
	}
	if override.Permissions.Source != "" {
		base.Permissions.Source = override.Permissions.Source
	}
	if override.Permissions.Preference != "" {
		base.Permissions.Preference = override.Permissions.Preference
	}
	base.Sandbox = mergeSandboxSettings(base.Sandbox, override.Sandbox)
	base.Fallback = mergeFallbackSettings(base.Fallback, override.Fallback)
	base.Update = mergeUpdateSettings(base.Update, override.Update)
	base.Multimodal = mergeMultimodalSettings(base.Multimodal, override.Multimodal)
	base.ComputerUse = mergeComputerUseSettings(base.ComputerUse, override.ComputerUse)
	base.AutoCompact = mergeAutoCompactSettings(base.AutoCompact, override.AutoCompact)
	base.Recap = mergeRecapSettings(base.Recap, override.Recap)
	base.TUI = mergeTUISettings(base.TUI, override.TUI)
	base.WebAgentUI = mergeWebAgentUISettings(base.WebAgentUI, override.WebAgentUI)
	base.WebSearch = mergeWebSearchSettings(base.WebSearch, override.WebSearch)
	base.ImageGeneration = mergeImageGenerationSettings(base.ImageGeneration, override.ImageGeneration)
	base.Init = mergeInitSettings(base.Init, override.Init)
	base.FileHistory = mergeFileHistorySettings(base.FileHistory, override.FileHistory)
	base.NextSteps = mergeNextStepsSettings(base.NextSteps, override.NextSteps)
	return base
}

func mergeImageGenerationSettings(base, override *ImageGenerationSettings) *ImageGenerationSettings {
	if override == nil {
		return base
	}
	if base == nil {
		copy := *override
		copy.Catalog = cloneImageCatalog(override.Catalog)
		if override.Enabled != nil {
			enabled := *override.Enabled
			copy.Enabled = &enabled
		}
		if override.PreviewInContext != nil {
			enabled := *override.PreviewInContext
			copy.PreviewInContext = &enabled
		}
		if override.AsyncChannelEnabled != nil {
			enabled := *override.AsyncChannelEnabled
			copy.AsyncChannelEnabled = &enabled
		}
		if override.AsyncChannelAccountKeys != nil {
			copy.AsyncChannelAccountKeys = append([]string{}, override.AsyncChannelAccountKeys...)
		}
		if override.Worker != nil {
			worker := *override.Worker
			copy.Worker = &worker
		}
		return &copy
	}
	if override.Enabled != nil {
		enabled := *override.Enabled
		base.Enabled = &enabled
	}
	if override.PreviewInContext != nil {
		enabled := *override.PreviewInContext
		base.PreviewInContext = &enabled
	}
	if strings.TrimSpace(override.Provider) != "" {
		base.Provider = override.Provider
	}
	if strings.TrimSpace(override.Model) != "" {
		base.Model = override.Model
	}
	if strings.TrimSpace(override.DefaultProvider) != "" {
		base.DefaultProvider = override.DefaultProvider
	}
	if strings.TrimSpace(override.DefaultModel) != "" {
		base.DefaultModel = override.DefaultModel
	}
	if override.Catalog != nil {
		base.Catalog = cloneImageCatalog(override.Catalog)
	}
	if strings.TrimSpace(override.Quality) != "" {
		base.Quality = override.Quality
	}
	if strings.TrimSpace(override.Size) != "" {
		base.Size = override.Size
	}
	if strings.TrimSpace(override.OutputFormat) != "" {
		base.OutputFormat = override.OutputFormat
	}
	if strings.TrimSpace(override.Background) != "" {
		base.Background = override.Background
	}
	if override.MaxImages != 0 {
		base.MaxImages = override.MaxImages
	}
	if override.MaxPromptChars != 0 {
		base.MaxPromptChars = override.MaxPromptChars
	}
	if override.MaxInputBytes != 0 {
		base.MaxInputBytes = override.MaxInputBytes
	}
	if override.MaxConcurrent != 0 {
		base.MaxConcurrent = override.MaxConcurrent
	}
	if override.TimeoutSeconds != 0 {
		base.TimeoutSeconds = override.TimeoutSeconds
	}
	if override.RetentionDays != 0 {
		base.RetentionDays = override.RetentionDays
	}
	if override.AsyncChannelEnabled != nil {
		enabled := *override.AsyncChannelEnabled
		base.AsyncChannelEnabled = &enabled
	}
	if override.AsyncChannelAccountKeys != nil {
		base.AsyncChannelAccountKeys = append([]string{}, override.AsyncChannelAccountKeys...)
	}
	base.Worker = mergeImageGenerationWorkerSettings(base.Worker, override.Worker)
	return base
}

func cloneImageCatalog(catalog []ImageModelCatalogEntry) []ImageModelCatalogEntry {
	if catalog == nil {
		return nil
	}
	copy := make([]ImageModelCatalogEntry, len(catalog))
	for i, entry := range catalog {
		copy[i] = entry
		copy[i].ImageModelCapability = cloneImageCapability(entry.ImageModelCapability)
	}
	return copy
}

func mergeImageGenerationWorkerSettings(base, override *ImageGenerationWorkerSettings) *ImageGenerationWorkerSettings {
	if override == nil {
		return base
	}
	if base == nil {
		copy := *override
		return &copy
	}
	if override.PollIntervalMS != 0 {
		base.PollIntervalMS = override.PollIntervalMS
	}
	if override.MaxConcurrent != 0 {
		base.MaxConcurrent = override.MaxConcurrent
	}
	if override.MaxAttempts != 0 {
		base.MaxAttempts = override.MaxAttempts
	}
	if override.HeartbeatSeconds != 0 {
		base.HeartbeatSeconds = override.HeartbeatSeconds
	}
	if override.LeaseSeconds != 0 {
		base.LeaseSeconds = override.LeaseSeconds
	}
	if override.FinalizeTimeoutSeconds != 0 {
		base.FinalizeTimeoutSeconds = override.FinalizeTimeoutSeconds
	}
	if override.MaxQueuedPerTenant != 0 {
		base.MaxQueuedPerTenant = override.MaxQueuedPerTenant
	}
	if override.MaxQueuedPerUser != 0 {
		base.MaxQueuedPerUser = override.MaxQueuedPerUser
	}
	return base
}

// mergeNextStepsSettings 之前漏掉了：NextSteps 字段没有并入 mergeSettings，
// 导致 settings.json 里配的 nextSteps.enabled/model/count 经过 LoadSettings
// 合并后总是被丢掉，opt-out 开关形同虚设。补上后和 FileHistory 同一个口径：
// 只覆盖 override 里显式设置的字段。
func mergeNextStepsSettings(base, override *NextStepsSettings) *NextStepsSettings {
	if override == nil {
		return base
	}
	if base == nil {
		base = &NextStepsSettings{}
	}
	if override.Enabled != nil {
		base.Enabled = override.Enabled
	}
	if override.Model != "" {
		base.Model = override.Model
	}
	if override.Count != nil {
		base.Count = override.Count
	}
	return base
}

func mergeFileHistorySettings(base, override *FileHistorySettings) *FileHistorySettings {
	if override == nil {
		return base
	}
	if base == nil {
		base = &FileHistorySettings{}
	}
	if override.MaxTurns != nil {
		base.MaxTurns = override.MaxTurns
	}
	if override.MaxAgeDays != nil {
		base.MaxAgeDays = override.MaxAgeDays
	}
	if override.MaxBytes != nil {
		base.MaxBytes = override.MaxBytes
	}
	return base
}

func mergeInitSettings(base, override *InitSettings) *InitSettings {
	if override == nil {
		return base
	}
	var merged InitSettings
	if base != nil {
		merged = *base
	}
	if override.DetailLevel != "" {
		merged.DetailLevel = override.DetailLevel
	}
	return &merged
}

func mergeIdentitySettings(base, override *identity.Settings) *identity.Settings {
	if override == nil {
		return base
	}
	var merged identity.Settings
	if base != nil {
		merged = *base
	}
	if override.ProductName != "" {
		merged.ProductName = override.ProductName
	}
	if override.ProductKey != "" {
		merged.ProductKey = override.ProductKey
	}
	if override.ConfigDirName != "" {
		merged.ConfigDirName = override.ConfigDirName
	}
	if override.GuidanceFilename != "" {
		merged.GuidanceFilename = override.GuidanceFilename
	}
	return &merged
}

func mergeStringList(base, override []string) []string {
	if len(override) == 0 {
		return base
	}
	seen := map[string]bool{}
	result := make([]string, 0, len(base)+len(override))
	for _, value := range append(base, override...) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func annotatePermissionSources(settings *Settings, source string) {
	source = strings.TrimSpace(source)
	if source == "" {
		return
	}
	hasPermissions := len(settings.Permissions.Allow) > 0 ||
		len(settings.Permissions.Deny) > 0 ||
		len(settings.Permissions.Ask) > 0 ||
		len(settings.Permissions.AlwaysAsk) > 0 ||
		len(settings.Permissions.AdditionalDirectories) > 0 ||
		strings.TrimSpace(settings.Permissions.DefaultMode) != "" ||
		strings.TrimSpace(settings.Permissions.Source) != "" ||
		strings.TrimSpace(settings.Permissions.Preference) != ""
	if !hasPermissions {
		return
	}
	if settings.Permissions.RuleSources == nil {
		settings.Permissions.RuleSources = map[string]string{}
	}
	for _, rule := range settings.Permissions.Allow {
		settings.Permissions.RuleSources[permissionRuleSourceKey("allow", rule)] = source
	}
	for _, rule := range settings.Permissions.Deny {
		settings.Permissions.RuleSources[permissionRuleSourceKey("deny", rule)] = source
	}
	for _, rule := range settings.Permissions.Ask {
		settings.Permissions.RuleSources[permissionRuleSourceKey("alwaysAsk", rule)] = source
		settings.Permissions.RuleSources[permissionRuleSourceKey("ask", rule)] = source
	}
	for _, rule := range settings.Permissions.AlwaysAsk {
		settings.Permissions.RuleSources[permissionRuleSourceKey("alwaysAsk", rule)] = source
	}
	if strings.TrimSpace(settings.Permissions.DefaultMode) != "" {
		settings.Permissions.RuleSources[permissionRuleSourceKey("defaultMode", "")] = source
	}
}

func permissionRuleSourceKey(kind, rule string) string {
	if strings.TrimSpace(rule) == "" {
		return strings.TrimSpace(kind)
	}
	return strings.TrimSpace(kind) + ":" + strings.TrimSpace(rule)
}

func permissionSourceForPath(cwd, path string) string {
	path = filepath.Clean(path)
	base := filepath.Base(path)
	parent := filepath.Base(filepath.Dir(path))
	ownedDir := identity.Default().ConfigDirName
	legacyOwnedDir := product.LegacyConfigDirName
	if base == "settings.json" {
		if globalPath, err := GlobalSettingsPath(); err == nil && filepath.Clean(path) == filepath.Clean(globalPath) {
			return "user"
		}
	}
	switch {
	case (parent == ownedDir || parent == legacyOwnedDir) && base == "settings.json":
		if home, err := os.UserHomeDir(); err == nil {
			globalPath, _ := GlobalSettingsPath()
			if filepath.Clean(path) == filepath.Clean(globalPath) ||
				filepath.Clean(path) == filepath.Join(home, ownedDir, "settings.json") ||
				filepath.Clean(path) == filepath.Join(home, legacyOwnedDir, "settings.json") {
				return "user"
			}
		}
		return "project"
	case (parent == ownedDir || parent == legacyOwnedDir) && base == "settings.local.json":
		return "local"
	case base == "settings.json":
		return "project"
	case base == "settings.local.json":
		return "local"
	case parent == ".claude" && base == "settings.json":
		return "project"
	case parent == ".claude" && base == "settings.local.json":
		return "local"
	case strings.HasPrefix(base, "config.") || base == "config.yaml" || base == "config.yml":
		if strings.Contains(base, ".local.") {
			return "config-local"
		}
		if strings.Contains(base, ".dev.") || strings.Contains(base, ".test.") || strings.Contains(base, ".prod.") {
			return strings.TrimSuffix(strings.TrimPrefix(base, "config."), filepath.Ext(base))
		}
		return "config"
	default:
		return "settings"
	}
}

func mergeSandboxSettings(base, override *SandboxSettings) *SandboxSettings {
	if override == nil {
		return base
	}
	if base == nil {
		copy := *override
		copy.EnabledPlatforms = append([]string(nil), override.EnabledPlatforms...)
		copy.ExcludedCommands = append([]string(nil), override.ExcludedCommands...)
		copy.Filesystem.AllowRead = append([]string(nil), override.Filesystem.AllowRead...)
		copy.Filesystem.DenyRead = append([]string(nil), override.Filesystem.DenyRead...)
		copy.Filesystem.AllowWrite = append([]string(nil), override.Filesystem.AllowWrite...)
		copy.Filesystem.DenyWrite = append([]string(nil), override.Filesystem.DenyWrite...)
		copy.Network.AllowDomains = append([]string(nil), override.Network.AllowDomains...)
		copy.Network.DenyDomains = append([]string(nil), override.Network.DenyDomains...)
		copy.UnixSockets.Deny = append([]string(nil), override.UnixSockets.Deny...)
		return &copy
	}
	if override.Enabled != nil {
		base.Enabled = override.Enabled
	}
	if override.FailIfUnavailable != nil {
		base.FailIfUnavailable = override.FailIfUnavailable
	}
	if override.AllowUnsandboxedCommands != nil {
		base.AllowUnsandboxedCommands = override.AllowUnsandboxedCommands
	}
	if override.AutoAllowBashIfSandboxed != nil {
		base.AutoAllowBashIfSandboxed = override.AutoAllowBashIfSandboxed
	}
	if override.AllowPty != nil {
		base.AllowPty = override.AllowPty
	}
	if override.EnableWeakerNestedSandbox != nil {
		base.EnableWeakerNestedSandbox = override.EnableWeakerNestedSandbox
	}
	if override.Network.Disabled != nil {
		base.Network.Disabled = override.Network.Disabled
	}
	if override.Network.Proxy.URL != "" {
		base.Network.Proxy.URL = override.Network.Proxy.URL
	}
	if override.Network.Proxy.Mode != "" {
		base.Network.Proxy.Mode = override.Network.Proxy.Mode
	}
	if override.Network.Proxy.Required != nil {
		base.Network.Proxy.Required = override.Network.Proxy.Required
	}
	if override.Network.MITM.CAFile != "" {
		base.Network.MITM.CAFile = override.Network.MITM.CAFile
	}
	if override.Network.MITM.Required != nil {
		base.Network.MITM.Required = override.Network.MITM.Required
	}
	if override.Seccomp.Enabled != nil {
		base.Seccomp.Enabled = override.Seccomp.Enabled
	}
	if override.Seccomp.Mode != "" {
		base.Seccomp.Mode = override.Seccomp.Mode
	}
	base.EnabledPlatforms = append(base.EnabledPlatforms, override.EnabledPlatforms...)
	base.ExcludedCommands = append(base.ExcludedCommands, override.ExcludedCommands...)
	base.Filesystem.AllowRead = append(base.Filesystem.AllowRead, override.Filesystem.AllowRead...)
	base.Filesystem.DenyRead = append(base.Filesystem.DenyRead, override.Filesystem.DenyRead...)
	base.Filesystem.AllowWrite = append(base.Filesystem.AllowWrite, override.Filesystem.AllowWrite...)
	base.Filesystem.DenyWrite = append(base.Filesystem.DenyWrite, override.Filesystem.DenyWrite...)
	base.Network.AllowDomains = append(base.Network.AllowDomains, override.Network.AllowDomains...)
	base.Network.DenyDomains = append(base.Network.DenyDomains, override.Network.DenyDomains...)
	base.UnixSockets.Deny = append(base.UnixSockets.Deny, override.UnixSockets.Deny...)
	return base
}

func mergeFallbackSettings(base, override *FallbackSettings) *FallbackSettings {
	if override == nil {
		return base
	}
	if base == nil {
		copy := *override
		copy.Providers = cloneProviders(override.Providers)
		return &copy
	}
	if override.Enabled != nil {
		base.Enabled = override.Enabled
	}
	if override.Providers != nil {
		// An explicitly decoded empty list is common in project config files
		// that only want to set fallback.enabled or other unrelated fields. Keep
		// global provider definitions unless the caller disables the fallback
		// feature; otherwise a project-local [] silently breaks global provider
		// references such as imageGeneration.provider.
		if len(override.Providers) > 0 {
			base.Providers = mergeFallbackProviders(base.Providers, override.Providers)
		}
	}
	return base
}

func mergeFallbackProviders(base, override []ProviderConfig) []ProviderConfig {
	if override == nil {
		return cloneProviders(base)
	}
	if len(override) == 0 {
		return []ProviderConfig{}
	}
	matched := make(map[string]ProviderConfig, len(base))
	for _, provider := range base {
		if key := fallbackProviderMergeKey(provider); key != "" {
			matched[key] = provider
		}
	}
	providers := make([]ProviderConfig, 0, len(override))
	for _, provider := range override {
		next := provider
		if baseProvider, ok := matched[fallbackProviderMergeKey(provider)]; ok {
			next = mergeFallbackProviderAuth(next, baseProvider)
		}
		providers = append(providers, next)
	}
	return providers
}

func fallbackProviderMergeKey(provider ProviderConfig) string {
	if name := strings.ToLower(strings.TrimSpace(provider.Name)); name != "" {
		return "name:" + name
	}
	model := strings.ToLower(strings.TrimSpace(provider.Model))
	baseURL := strings.TrimRight(strings.ToLower(strings.TrimSpace(provider.BaseURL)), "/")
	if model != "" && baseURL != "" {
		return "model-url:" + model + "|" + baseURL
	}
	return ""
}

func mergeFallbackProviderAuth(provider, base ProviderConfig) ProviderConfig {
	if !provider.apiKeySet {
		provider.APIKey = base.APIKey
		provider.apiKeySet = base.apiKeySet
	}
	if !provider.authTokenSet {
		provider.AuthToken = base.AuthToken
		provider.authTokenSet = base.authTokenSet
	}
	return provider
}

func mergeAutoCompactSettings(base, override *AutoCompactSettings) *AutoCompactSettings {
	if override == nil {
		return base
	}
	if base == nil {
		copy := *override
		copy.ModelContext = mergeMap(map[string]int(nil), override.ModelContext)
		copy.ModelThresholdRatio = mergeMap(map[string]float64(nil), override.ModelThresholdRatio)
		return &copy
	}
	if override.Enabled != nil {
		base.Enabled = override.Enabled
	}
	if override.DefaultThresholdRatio != nil {
		base.DefaultThresholdRatio = override.DefaultThresholdRatio
	}
	if override.PreserveRecentRounds != nil {
		base.PreserveRecentRounds = override.PreserveRecentRounds
	}
	if override.SummaryModel != nil {
		base.SummaryModel = override.SummaryModel
	}
	if override.MaxSummaryTokens != nil {
		base.MaxSummaryTokens = override.MaxSummaryTokens
	}
	if override.CooldownTurns != nil {
		base.CooldownTurns = override.CooldownTurns
	}
	if override.MaxFailures != nil {
		base.MaxFailures = override.MaxFailures
	}
	base.ModelContext = mergeMap(base.ModelContext, override.ModelContext)
	base.ModelThresholdRatio = mergeMap(base.ModelThresholdRatio, override.ModelThresholdRatio)
	return base
}

func mergeRecapSettings(base, override *RecapSettings) *RecapSettings {
	if override == nil {
		return base
	}
	if base == nil {
		copy := *override
		return &copy
	}
	if override.Enabled != nil {
		base.Enabled = override.Enabled
	}
	if override.Mode != "" {
		base.Mode = override.Mode
	}
	if override.Model != "" {
		base.Model = override.Model
	}
	if override.RecentMessageWindow != nil {
		base.RecentMessageWindow = override.RecentMessageWindow
	}
	if override.MaxTokens != nil {
		base.MaxTokens = override.MaxTokens
	}
	if override.AwayDelaySeconds != nil {
		base.AwayDelaySeconds = override.AwayDelaySeconds
	}
	if override.IncludeSessionMemory != nil {
		base.IncludeSessionMemory = override.IncludeSessionMemory
	}
	if override.IncludeCompactSummary != nil {
		base.IncludeCompactSummary = override.IncludeCompactSummary
	}
	return base
}

func mergeTUISettings(base, override *TUISettings) *TUISettings {
	if override == nil {
		return base
	}
	if base == nil {
		copy := *override
		return &copy
	}
	if override.ResumeHistoryLimit != nil {
		base.ResumeHistoryLimit = override.ResumeHistoryLimit
	}
	if override.ShowThinking != nil {
		base.ShowThinking = override.ShowThinking
	}
	if strings.TrimSpace(override.ThinkingMode) != "" {
		base.ThinkingMode = override.ThinkingMode
	}
	return base
}

func mergeWebAgentUISettings(base, override *WebAgentUISettings) *WebAgentUISettings {
	if override == nil {
		return base
	}
	if base == nil {
		copy := *override
		return &copy
	}
	if override.ShowThinking != nil {
		base.ShowThinking = override.ShowThinking
	}
	return base
}

func mergeUpdateSettings(base, override *UpdateSettings) *UpdateSettings {
	if override == nil {
		return base
	}
	if base == nil {
		copy := *override
		return &copy
	}
	if override.Enabled != nil {
		base.Enabled = override.Enabled
	}
	if override.CheckOnStartup != nil {
		base.CheckOnStartup = override.CheckOnStartup
	}
	if override.CheckOnly != nil {
		base.CheckOnly = override.CheckOnly
	}
	if override.AutoPull != nil {
		base.AutoPull = override.AutoPull
	}
	if override.SkipWhenDirty != nil {
		base.SkipWhenDirty = override.SkipWhenDirty
	}
	if override.Strategy != "" {
		base.Strategy = override.Strategy
	}
	if override.RepoDir != "" {
		base.RepoDir = override.RepoDir
	}
	if override.Command != "" {
		base.Command = override.Command
	}
	if override.CustomCommand != "" {
		base.CustomCommand = override.CustomCommand
	}
	if override.VersionSourceURL != "" {
		base.VersionSourceURL = override.VersionSourceURL
	}
	if override.ScheduleInterval != "" {
		base.ScheduleInterval = override.ScheduleInterval
	}
	if override.TimeoutSeconds > 0 {
		base.TimeoutSeconds = override.TimeoutSeconds
	}
	return base
}

func mergeMultimodalSettings(base, override *MultimodalSettings) *MultimodalSettings {
	if override == nil {
		return base
	}
	if base == nil {
		copy := *override
		copy.Models = mergeMap[string](nil, override.Models)
		return &copy
	}
	if override.Enabled != nil {
		base.Enabled = override.Enabled
	}
	if override.DefaultModel != "" {
		base.DefaultModel = override.DefaultModel
	}
	base.Models = mergeMap(base.Models, override.Models)
	return base
}

func mergeWebSearchSettings(base, override *WebSearchSettings) *WebSearchSettings {
	if override == nil {
		return base
	}
	if base == nil {
		copy := *override
		return &copy
	}
	if strings.TrimSpace(override.Endpoint) != "" {
		base.Endpoint = override.Endpoint
	}
	return base
}

func mergeGrowthBookSettings(base, override *GrowthBookSettings) *GrowthBookSettings {
	if override == nil {
		return base
	}
	if base == nil {
		copy := *override
		copy.Headers = cloneStringMap(override.Headers)
		copy.Features = mergeAnyMap(nil, override.Features)
		copy.Overrides = mergeAnyMap(nil, override.Overrides)
		return &copy
	}
	if override.Enabled != nil {
		base.Enabled = override.Enabled
	}
	if override.URL != "" {
		base.URL = override.URL
	}
	if override.Headers != nil {
		base.Headers = cloneStringMap(override.Headers)
	}
	if override.RefreshIntervalSeconds > 0 {
		base.RefreshIntervalSeconds = override.RefreshIntervalSeconds
	}
	base.Features = mergeAnyMap(base.Features, override.Features)
	base.Overrides = mergeAnyMap(base.Overrides, override.Overrides)
	return base
}

func providerAuthConfigured(provider ProviderConfig) bool {
	return provider.apiKeySet || provider.authTokenSet || provider.APIKey != "" || provider.AuthToken != ""
}

func fallbackProviders(settings *FallbackSettings, apiKey, authToken string) []ProviderConfig {
	if settings == nil || len(settings.Providers) == 0 {
		return nil
	}
	if settings.Enabled != nil && !*settings.Enabled {
		return nil
	}
	providers := make([]ProviderConfig, 0, len(settings.Providers))
	for _, provider := range settings.Providers {
		authConfigured := providerAuthConfigured(provider)
		provider.Name = strings.TrimSpace(provider.Name)
		provider.Type = strings.TrimSpace(provider.Type)
		provider.Protocol = normalizeProviderProtocol(provider.Protocol)
		provider.BaseURL = strings.TrimRight(strings.TrimSpace(provider.BaseURL), "/")
		provider.APIKey = strings.TrimSpace(provider.APIKey)
		provider.AuthToken = strings.TrimSpace(provider.AuthToken)
		provider.Model = strings.TrimSpace(provider.Model)
		if !authConfigured {
			provider.APIKey = apiKey
			provider.AuthToken = authToken
		}
		providers = append(providers, provider)
	}
	return providers
}

func cloneProviders(providers []ProviderConfig) []ProviderConfig {
	if providers == nil {
		return nil
	}
	cloned := append([]ProviderConfig(nil), providers...)
	for i := range cloned {
		cloned[i].Responses = cloneResponsesProviderSettings(cloned[i].Responses)
	}
	return cloned
}

func cloneStringMap(values map[string]string) map[string]string {
	return mergeMap[string](nil, values)
}

func mergeHookMap(base, override map[string][]HookCommand) map[string][]HookCommand {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := make(map[string][]HookCommand, len(base)+len(override))
	for k, v := range base {
		out[k] = append([]HookCommand(nil), v...)
	}
	for k, v := range override {
		out[k] = append(out[k], v...)
	}
	return out
}

func mergeMap[T any](base, override map[string]T) map[string]T {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := make(map[string]T, len(base)+len(override))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		out[k] = v
	}
	return out
}

func mergeAnyMap(base, override map[string]any) map[string]any {
	return mergeMap(base, override)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
