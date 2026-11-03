package recap

import "strings"

const (
	EntryType = "recap_summary"

	ModeManual   = "manual"
	ModePostTurn = "post_turn"
	ModeAway     = "away"

	DefaultRecentMessageWindow = 30
	DefaultMaxTokens           = 512
	DefaultToolResultLimit     = 1600
	DefaultAwayDelaySeconds    = 90
)

type Config struct {
	Enabled               bool
	Mode                  string
	Model                 string
	RecentMessageWindow   int
	MaxTokens             int
	AwayDelaySeconds      int
	IncludeSessionMemory  bool
	IncludeCompactSummary bool
	ToolResultLimit       int
}

func (c Config) WithDefaults(defaultModel string) Config {
	c.Mode = strings.ToLower(strings.TrimSpace(c.Mode))
	if c.Mode == "" {
		c.Mode = ModeManual
	}
	if c.Model = strings.TrimSpace(c.Model); c.Model == "" {
		c.Model = strings.TrimSpace(defaultModel)
	}
	if c.RecentMessageWindow <= 0 {
		c.RecentMessageWindow = DefaultRecentMessageWindow
	}
	if c.MaxTokens <= 0 {
		c.MaxTokens = DefaultMaxTokens
	}
	if c.ToolResultLimit <= 0 {
		c.ToolResultLimit = DefaultToolResultLimit
	}
	if c.AwayDelaySeconds <= 0 {
		c.AwayDelaySeconds = DefaultAwayDelaySeconds
	}
	return c
}

func (c Config) PostTurnEnabled(defaultModel string) bool {
	c = c.WithDefaults(defaultModel)
	return c.Enabled && c.Mode == ModePostTurn
}

func (c Config) AwayEnabled(defaultModel string) bool {
	c = c.WithDefaults(defaultModel)
	return c.Enabled && c.Mode == ModeAway
}
