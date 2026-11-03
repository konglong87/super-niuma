package promptcache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"github.com/konglong87/super-niuma/internal/anthropic"
)

type Report struct {
	Initialized         bool   `json:"initialized"`
	TextHash            string `json:"text_hash"`
	CacheControlHash    string `json:"cache_control_hash"`
	TextChanged         bool   `json:"text_changed"`
	CacheControlChanged bool   `json:"cache_control_changed"`
	BreaksCache         bool   `json:"breaks_cache"`
}

type Tracker struct {
	previousTextHash         string
	previousCacheControlHash string
	initialized              bool
}

type RequestReport struct {
	Initialized       bool   `json:"initialized"`
	SignatureHash     string `json:"signature_hash"`
	ModelHash         string `json:"model_hash"`
	SystemHash        string `json:"system_hash"`
	CacheControlHash  string `json:"cache_control_hash"`
	ToolsHash         string `json:"tools_hash"`
	MessagePrefixHash string `json:"message_prefix_hash"`
	ThinkingHash      string `json:"thinking_hash"`
	ModelChanged      bool   `json:"model_changed"`
	SystemChanged     bool   `json:"system_changed"`
	CacheChanged      bool   `json:"cache_changed"`
	ToolsChanged      bool   `json:"tools_changed"`
	MessageChanged    bool   `json:"message_changed"`
	ThinkingChanged   bool   `json:"thinking_changed"`
	BreaksCache       bool   `json:"breaks_cache"`
}

type RequestTracker struct {
	previousRequestFingerprint requestFingerprint
	initialized                bool
}

type requestFingerprint struct {
	SignatureHash     string
	ModelHash         string
	SystemHash        string
	CacheControlHash  string
	ToolsHash         string
	MessagePrefixHash string
	ThinkingHash      string
}

func (t *Tracker) Observe(blocks []anthropic.SystemBlock) Report {
	textHash, cacheHash := HashBlocks(blocks)
	report := Report{
		Initialized:      t.initialized,
		TextHash:         textHash,
		CacheControlHash: cacheHash,
	}
	if t.initialized {
		report.TextChanged = textHash != t.previousTextHash
		report.CacheControlChanged = cacheHash != t.previousCacheControlHash
		report.BreaksCache = report.TextChanged || report.CacheControlChanged
	}
	t.previousTextHash = textHash
	t.previousCacheControlHash = cacheHash
	t.initialized = true
	return report
}

func (t *RequestTracker) Observe(req anthropic.MessagesRequest, thinkingConfig any) RequestReport {
	fingerprint := RequestFingerprint(req, thinkingConfig)
	report := RequestReport{
		Initialized:       t.initialized,
		SignatureHash:     fingerprint.SignatureHash,
		ModelHash:         fingerprint.ModelHash,
		SystemHash:        fingerprint.SystemHash,
		CacheControlHash:  fingerprint.CacheControlHash,
		ToolsHash:         fingerprint.ToolsHash,
		MessagePrefixHash: fingerprint.MessagePrefixHash,
		ThinkingHash:      fingerprint.ThinkingHash,
	}
	if t.initialized {
		report.ModelChanged = fingerprint.ModelHash != t.previousRequestFingerprint.ModelHash
		report.SystemChanged = fingerprint.SystemHash != t.previousRequestFingerprint.SystemHash
		report.CacheChanged = fingerprint.CacheControlHash != t.previousRequestFingerprint.CacheControlHash
		report.ToolsChanged = fingerprint.ToolsHash != t.previousRequestFingerprint.ToolsHash
		report.MessageChanged = fingerprint.MessagePrefixHash != t.previousRequestFingerprint.MessagePrefixHash
		report.ThinkingChanged = fingerprint.ThinkingHash != t.previousRequestFingerprint.ThinkingHash
		report.BreaksCache = report.ModelChanged || report.SystemChanged || report.CacheChanged || report.ToolsChanged || report.MessageChanged || report.ThinkingChanged
	}
	t.previousRequestFingerprint = fingerprint
	t.initialized = true
	return report
}

func RequestFingerprint(req anthropic.MessagesRequest, thinkingConfig any) requestFingerprint {
	systemHash, cacheHash := HashBlocks(systemBlocksForRequest(req))
	modelHash := sum(strings.TrimSpace(req.Model))
	toolsHash := hashTools(req.Tools)
	messagePrefixHash := hashMessagePrefix(req.Messages)
	thinkingHash := hashJSON(thinkingConfig)
	signatureHash := hashJSON(map[string]string{
		"cache_control":  cacheHash,
		"message_prefix": messagePrefixHash,
		"model":          modelHash,
		"system":         systemHash,
		"thinking":       thinkingHash,
		"tools":          toolsHash,
	})
	return requestFingerprint{
		SignatureHash:     signatureHash,
		ModelHash:         modelHash,
		SystemHash:        systemHash,
		CacheControlHash:  cacheHash,
		ToolsHash:         toolsHash,
		MessagePrefixHash: messagePrefixHash,
		ThinkingHash:      thinkingHash,
	}
}

func HashBlocks(blocks []anthropic.SystemBlock) (string, string) {
	textParts := make([]string, 0, len(blocks))
	cacheParts := make([]map[string]string, 0, len(blocks))
	for _, block := range blocks {
		textParts = append(textParts, strings.TrimSpace(block.Text))
		if block.CacheControl == nil {
			cacheParts = append(cacheParts, nil)
			continue
		}
		cacheParts = append(cacheParts, map[string]string{
			"type":  strings.TrimSpace(block.CacheControl.Type),
			"ttl":   strings.TrimSpace(block.CacheControl.TTL),
			"scope": strings.TrimSpace(block.CacheControl.Scope),
		})
	}
	cacheBytes, _ := json.Marshal(cacheParts)
	return sum(strings.Join(textParts, "\n\n")), sum(string(cacheBytes))
}

func systemBlocksForRequest(req anthropic.MessagesRequest) []anthropic.SystemBlock {
	if len(req.SystemBlocks) > 0 {
		return req.SystemBlocks
	}
	if strings.TrimSpace(req.System) == "" {
		return nil
	}
	return []anthropic.SystemBlock{{Type: "text", Text: req.System}}
}

func hashTools(tools []anthropic.ToolDefinition) string {
	type toolSignature struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		InputSchema string `json:"input_schema,omitempty"`
	}
	signatures := make([]toolSignature, 0, len(tools))
	for _, tool := range tools {
		signatures = append(signatures, toolSignature{
			Name:        strings.TrimSpace(tool.Name),
			Description: strings.TrimSpace(tool.Description),
			InputSchema: strings.TrimSpace(string(tool.InputSchema)),
		})
	}
	sort.Slice(signatures, func(i, j int) bool {
		return signatures[i].Name < signatures[j].Name
	})
	return hashJSON(signatures)
}

func hashMessagePrefix(messages []anthropic.MessageParam) string {
	const prefixMessages = 2
	limit := len(messages)
	if limit > prefixMessages {
		limit = prefixMessages
	}
	return hashJSON(messages[:limit])
}

func hashJSON(value any) string {
	if value == nil {
		return sum("")
	}
	data, err := json.Marshal(value)
	if err != nil {
		return sum("")
	}
	return sum(string(data))
}

func sum(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}
