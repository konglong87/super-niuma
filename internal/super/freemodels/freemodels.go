// Package freemodels embeds the build-time snapshot of free model providers
// that Desktop offers to new users. The snapshot is generated from the
// free-AI-free-token data set, whose entries cite each provider's official
// pages. It never contains API keys, and v1 has no online update path: the
// catalog only changes when a new snapshot is synced and the binary rebuilt.
package freemodels

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
)

// SchemaVersion is the only snapshot schema this package understands.
const SchemaVersion = 1

//go:embed catalog.json
var snapshot []byte

type Catalog struct {
	SchemaVersion int        `json:"schema_version"`
	GeneratedAt   string     `json:"generated_at"`
	Providers     []Provider `json:"providers"`
}

type Provider struct {
	Slug              string       `json:"slug"`
	Name              string       `json:"name"`
	BaseURL           string       `json:"base_url"`
	OpenAICompatible  string       `json:"openai_compatible"`
	AnthropicEndpoint string       `json:"anthropic_endpoint,omitempty"`
	Free              Free         `json:"free"`
	Requirements      Requirements `json:"requirements"`
	SignupURL         string       `json:"signup_url"`
	Status            string       `json:"status"`
	StatusNote        string       `json:"status_note,omitempty"`
	VerifiedAt        string       `json:"verified_at"`
	Sources           []Source     `json:"sources"`
	RecommendedModel  *Model       `json:"recommended_model"`
	Models            []Model      `json:"models"`
	UsageNotes        []string     `json:"usage_notes"`
}

type Free struct {
	Type string `json:"type"`
	Note string `json:"note,omitempty"`
}

type Requirements struct {
	CreditCard string `json:"credit_card"`
	RealName   string `json:"real_name"`
	Region     string `json:"region"`
	Other      string `json:"other,omitempty"`
}

type Source struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type Model struct {
	ID                  string   `json:"id"`
	Capabilities        []string `json:"capabilities"`
	PendingCapabilities []string `json:"pending_capabilities"`
	ToolCalling         string   `json:"tool_calling"`
	Context             string   `json:"context,omitempty"`
	RPM                 int      `json:"rpm,omitempty"`
	RPD                 int      `json:"rpd,omitempty"`
	TPM                 int      `json:"tpm,omitempty"`
	TPD                 int      `json:"tpd,omitempty"`
}

// Unverified reports whether the UI must label the provider "未核实": the
// provider record is still pending, its OpenAI compatibility is not confirmed,
// or there is no recommended model confirmed to support tool calling. The raw
// fields stay available so the UI can say which one is missing.
func (p Provider) Unverified() bool {
	return p.Status != "ok" ||
		p.OpenAICompatible != "supported" ||
		p.RecommendedModel == nil ||
		p.RecommendedModel.ToolCalling != "supported"
}

// Parse strictly validates and decodes a catalog snapshot. Unknown extra
// fields are ignored so the data set can grow without breaking older builds.
func Parse(data []byte) (*Catalog, error) {
	var c Catalog
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("freemodels: decode catalog: %w", err)
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

var (
	loadOnce sync.Once
	loaded   *Catalog
	loadErr  error
)

// Load returns the embedded catalog. The result is shared; callers must not
// modify it.
func Load() (*Catalog, error) {
	loadOnce.Do(func() { loaded, loadErr = Parse(snapshot) })
	return loaded, loadErr
}

// SignupHosts returns the lower-case host names of every provider signup_url,
// for allowing those hosts to be opened from the Desktop "open external link"
// bridge. Ports are not included.
func SignupHosts() (map[string]struct{}, error) {
	c, err := Load()
	if err != nil {
		return nil, err
	}
	hosts := make(map[string]struct{}, len(c.Providers))
	for _, p := range c.Providers {
		u, err := url.Parse(p.SignupURL)
		if err != nil {
			return nil, fmt.Errorf("freemodels: provider %q: %w", p.Slug, err)
		}
		hosts[strings.ToLower(u.Hostname())] = struct{}{}
	}
	return hosts, nil
}

var (
	openAICompat = []string{"supported", "unverified", "unsupported"}
	freeTypes    = []string{"permanent", "daily", "one_time", "promo", "unknown"}
	requirement  = []string{"required", "none", "unknown"}
	statuses     = []string{"ok", "pending"}
	toolCalling  = []string{"supported", "pending", "unsupported"}
)

func (c *Catalog) validate() error {
	if c.SchemaVersion != SchemaVersion {
		return fmt.Errorf("freemodels: schema_version %d is not supported (want %d)", c.SchemaVersion, SchemaVersion)
	}
	if len(c.Providers) == 0 {
		return fmt.Errorf("freemodels: catalog has no providers")
	}
	seen := map[string]bool{}
	for i, p := range c.Providers {
		if err := p.validate(); err != nil {
			return fmt.Errorf("freemodels: provider #%d (%q): %w", i, p.Slug, err)
		}
		if seen[p.Slug] {
			return fmt.Errorf("freemodels: duplicate provider slug %q", p.Slug)
		}
		seen[p.Slug] = true
	}
	return nil
}

func (p Provider) validate() error {
	if strings.TrimSpace(p.Slug) == "" {
		return fmt.Errorf("slug is required")
	}
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if err := requireHTTPS("base_url", p.BaseURL); err != nil {
		return err
	}
	if err := requireHTTPS("signup_url", p.SignupURL); err != nil {
		return err
	}
	if p.AnthropicEndpoint != "" {
		if err := requireHTTPS("anthropic_endpoint", p.AnthropicEndpoint); err != nil {
			return err
		}
	}
	for _, s := range p.Sources {
		if err := requireHTTPS("sources url", s.URL); err != nil {
			return err
		}
	}
	checks := []struct {
		field, value string
		allowed      []string
	}{
		{"openai_compatible", p.OpenAICompatible, openAICompat},
		{"free.type", p.Free.Type, freeTypes},
		{"requirements.credit_card", p.Requirements.CreditCard, requirement},
		{"requirements.real_name", p.Requirements.RealName, requirement},
		{"requirements.region", p.Requirements.Region, requirement},
		{"status", p.Status, statuses},
	}
	for _, c := range checks {
		if err := oneOf(c.field, c.value, c.allowed); err != nil {
			return err
		}
	}
	for _, m := range p.Models {
		if err := m.validate(); err != nil {
			return fmt.Errorf("model %q: %w", m.ID, err)
		}
	}
	if p.RecommendedModel != nil {
		if err := p.RecommendedModel.validate(); err != nil {
			return fmt.Errorf("recommended_model %q: %w", p.RecommendedModel.ID, err)
		}
	}
	return nil
}

func (m Model) validate() error {
	if strings.TrimSpace(m.ID) == "" {
		return fmt.Errorf("id is required")
	}
	return oneOf("tool_calling", m.ToolCalling, toolCalling)
}

func oneOf(field, value string, allowed []string) error {
	for _, a := range allowed {
		if value == a {
			return nil
		}
	}
	return fmt.Errorf("%s %q is not one of %s", field, value, strings.Join(allowed, "|"))
}

func requireHTTPS(field, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return fmt.Errorf("%s %q must be an https URL with a host", field, raw)
	}
	return nil
}
