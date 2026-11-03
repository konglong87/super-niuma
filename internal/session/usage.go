package session

import (
	"fmt"
	"strings"
)

// TurnUsage is the provider-neutral usage contract for one model turn.
// InputTokens is the uncached input tier; cache counters are disjoint from it.
// The contract deliberately keeps provenance so renderers and quota consumers
// do not silently mix estimated and provider-reported values.
type TurnUsage struct {
	SessionID                string `json:"session_id,omitempty"`
	Turn                     int    `json:"turn"`
	Provider                 string `json:"provider,omitempty"`
	Model                    string `json:"model,omitempty"`
	InputTokens              int    `json:"input_tokens"`
	CacheReadInputTokens     int    `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens int    `json:"cache_creation_input_tokens,omitempty"`
	CacheCreationEphemeral5m int    `json:"cache_creation_ephemeral_5m_input_tokens,omitempty"`
	CacheCreationEphemeral1h int    `json:"cache_creation_ephemeral_1h_input_tokens,omitempty"`
	OutputTokens             int    `json:"output_tokens"`
	Estimated                bool   `json:"estimated"`
	Source                   string `json:"source,omitempty"`
}

// NewTurnUsage converts the legacy reported shape, whose InputTokens includes
// cache tiers, into the disjoint TurnUsage contract used by new consumers.
func NewTurnUsage(sessionID string, turn int, provider, model string, reported ReportedUsage, estimated bool, source string) TurnUsage {
	cacheCreationTotal := reported.CacheCreationInputTokens
	if reported.CacheCreation5mTokens != 0 || reported.CacheCreation1hTokens != 0 {
		cacheCreationTotal = 0
	}
	uncached := reported.InputTokens - reported.CacheReadInputTokens - reported.CacheCreationInputTokens
	if uncached < 0 {
		uncached = 0
	}
	return TurnUsage{
		SessionID:                sessionID,
		Turn:                     turn,
		Provider:                 provider,
		Model:                    model,
		InputTokens:              uncached,
		CacheReadInputTokens:     reported.CacheReadInputTokens,
		CacheCreationInputTokens: cacheCreationTotal,
		CacheCreationEphemeral5m: reported.CacheCreation5mTokens,
		CacheCreationEphemeral1h: reported.CacheCreation1hTokens,
		OutputTokens:             reported.OutputTokens,
		Estimated:                estimated,
		Source:                   source,
	}
}

// TotalTokens returns the total number of disjoint input and output tokens.
func (u TurnUsage) TotalTokens() int {
	cacheCreation := u.CacheCreationInputTokens
	if u.CacheCreationEphemeral5m > 0 || u.CacheCreationEphemeral1h > 0 {
		cacheCreation = u.CacheCreationEphemeral5m + u.CacheCreationEphemeral1h
	}
	return nonNegative(u.InputTokens) + nonNegative(u.CacheReadInputTokens) + nonNegative(cacheCreation) + nonNegative(u.OutputTokens)
}

// Add combines counters for the same session/turn/provider/model. A later
// provider-reported update supersedes an earlier estimate's provenance.
func (u TurnUsage) Add(other TurnUsage) (TurnUsage, error) {
	if err := validateTurnUsageIdentity(u, other); err != nil {
		return TurnUsage{}, err
	}
	result := u
	result.InputTokens += other.InputTokens
	result.CacheReadInputTokens += other.CacheReadInputTokens
	result.CacheCreationInputTokens += other.CacheCreationInputTokens
	result.CacheCreationEphemeral5m += other.CacheCreationEphemeral5m
	result.CacheCreationEphemeral1h += other.CacheCreationEphemeral1h
	result.OutputTokens += other.OutputTokens
	if !other.Estimated || strings.TrimSpace(result.Source) == "" {
		result.Estimated = other.Estimated
	}
	if strings.TrimSpace(other.Source) != "" {
		result.Source = other.Source
	}
	return result, nil
}

func validateTurnUsageIdentity(a, b TurnUsage) error {
	if a.SessionID != "" && b.SessionID != "" && a.SessionID != b.SessionID {
		return fmt.Errorf("turn usage session mismatch: %q != %q", a.SessionID, b.SessionID)
	}
	if a.Turn != 0 && b.Turn != 0 && a.Turn != b.Turn {
		return fmt.Errorf("turn usage turn mismatch: %d != %d", a.Turn, b.Turn)
	}
	if a.Provider != "" && b.Provider != "" && a.Provider != b.Provider {
		return fmt.Errorf("turn usage provider mismatch: %q != %q", a.Provider, b.Provider)
	}
	if a.Model != "" && b.Model != "" && a.Model != b.Model {
		return fmt.Errorf("turn usage model mismatch: %q != %q", a.Model, b.Model)
	}
	return nil
}

func nonNegative(value int) int {
	if value < 0 {
		return 0
	}
	return value
}
