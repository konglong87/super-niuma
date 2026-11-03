package promptdump

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/konglong87/super-niuma/internal/promptcache"
)

const (
	ProviderCacheUsageReported    = "reported"
	ProviderCacheUsageNotReported = "not_reported"
	ProviderCacheUsageUnavailable = "unavailable"
)

type CacheDiagnosticsReport struct {
	Sessions []CacheSessionDiagnostics `json:"sessions"`
	Records  []CacheRecordDiagnostics  `json:"records"`
}

type CacheSessionDiagnostics struct {
	SessionID                          string                       `json:"session_id"`
	Records                            int                          `json:"records"`
	ProviderCacheUsageState            string                       `json:"provider_cache_usage_state"`
	ProviderCacheHitRatioInput         float64                      `json:"provider_cache_hit_ratio_input,omitempty"`
	ProviderCacheHitRatioTotalInput    float64                      `json:"provider_cache_hit_ratio_total_input,omitempty"`
	SystemHashStable                   bool                         `json:"system_hash_stable"`
	CacheControlHashStable             bool                         `json:"cache_control_hash_stable"`
	ToolsHashStable                    bool                         `json:"tools_hash_stable"`
	MessagePrefixHashStable            bool                         `json:"message_prefix_hash_stable"`
	ThinkingHashStable                 bool                         `json:"thinking_hash_stable"`
	CacheableSystemBytesMax            int                          `json:"cacheable_system_bytes_max,omitempty"`
	UncachedSystemBytesMax             int                          `json:"uncached_system_bytes_max,omitempty"`
	LargestUncachedSystemBlocks        []CacheDiagnosticSystemBlock `json:"largest_uncached_system_blocks,omitempty"`
	RequestSignatureChangedFields      []string                     `json:"request_signature_changed_fields,omitempty"`
	RequestSignatureChangedFieldsCount map[string]int               `json:"request_signature_changed_fields_count,omitempty"`
}

type CacheRecordDiagnostics struct {
	SessionID                         string                        `json:"session_id,omitempty"`
	Turn                              int                           `json:"turn,omitempty"`
	CacheableSystemBytes              int                           `json:"cacheable_system_bytes"`
	UncachedSystemBytes               int                           `json:"uncached_system_bytes"`
	SkillsCatalogPosition             *int                          `json:"skills_catalog_position,omitempty"`
	SkillsCatalogHash                 string                        `json:"skills_catalog_hash,omitempty"`
	SkillsCatalogBytes                int                           `json:"skills_catalog_bytes,omitempty"`
	PrefixBeforeSkillsHash            string                        `json:"prefix_before_skills_hash,omitempty"`
	PrefixThroughSkillsHash           string                        `json:"prefix_through_skills_hash,omitempty"`
	CacheControlBlockCount            int                           `json:"cache_control_block_count"`
	MessageCacheMarkerCount           int                           `json:"message_cache_marker_count"`
	LargestUncachedSystemBlocks       []CacheDiagnosticSystemBlock  `json:"largest_uncached_system_blocks,omitempty"`
	LargestMessageBlocks              []CacheDiagnosticMessageBlock `json:"largest_message_blocks,omitempty"`
	RequestSignatureHash              string                        `json:"request_signature_hash,omitempty"`
	RequestSignatureDeltaFromPrevious CacheSignatureDelta           `json:"request_signature_delta_from_previous"`
}

type CacheDiagnosticSystemBlock struct {
	Index           int    `json:"index"`
	Kind            string `json:"kind,omitempty"`
	Source          string `json:"source,omitempty"`
	TextBytes       int    `json:"text_bytes"`
	TextHash        string `json:"text_hash,omitempty"`
	HasCacheControl bool   `json:"has_cache_control,omitempty"`
	Stable          bool   `json:"stable"`
	SeenCount       int    `json:"seen_count,omitempty"`
}

type CacheDiagnosticMessageBlock struct {
	MessageIndex    int    `json:"message_index"`
	Role            string `json:"role,omitempty"`
	BlockIndex      int    `json:"block_index"`
	Type            string `json:"type,omitempty"`
	Bytes           int    `json:"bytes"`
	HasCacheControl bool   `json:"has_cache_control,omitempty"`
	ToolName        string `json:"tool_name,omitempty"`
	ToolUseID       string `json:"tool_use_id,omitempty"`
}

type CacheSignatureDelta struct {
	Initialized           bool     `json:"initialized"`
	BreaksCache           bool     `json:"breaks_cache"`
	ChangedFields         []string `json:"changed_fields,omitempty"`
	PreviousSignatureHash string   `json:"previous_signature_hash,omitempty"`
	SignatureHash         string   `json:"signature_hash,omitempty"`
	ModelChanged          bool     `json:"model_changed,omitempty"`
	SystemChanged         bool     `json:"system_changed,omitempty"`
	CacheControlChanged   bool     `json:"cache_control_changed,omitempty"`
	ToolsChanged          bool     `json:"tools_changed,omitempty"`
	MessagePrefixChanged  bool     `json:"message_prefix_changed,omitempty"`
	ThinkingChanged       bool     `json:"thinking_changed,omitempty"`
}

type cacheSignatureParts struct {
	SignatureHash     string
	ModelHash         string
	SystemHash        string
	CacheControlHash  string
	ToolsHash         string
	MessagePrefixHash string
	ThinkingHash      string
}

func AnalyzeCacheDiagnostics(records []Record) CacheDiagnosticsReport {
	report := CacheDiagnosticsReport{
		Sessions: []CacheSessionDiagnostics{},
		Records:  make([]CacheRecordDiagnostics, len(records)),
	}
	if len(records) == 0 {
		return report
	}
	groups := map[string][]int{}
	for i, record := range records {
		sessionID := diagnosticsSessionID(record.SessionID)
		groups[sessionID] = append(groups[sessionID], i)
	}
	for sessionID, indices := range groups {
		session, perRecord := analyzeCacheDiagnosticsSession(sessionID, records, indices)
		report.Sessions = append(report.Sessions, session)
		for i, index := range indices {
			report.Records[index] = perRecord[i]
		}
	}
	sort.Slice(report.Sessions, func(i, j int) bool {
		return report.Sessions[i].SessionID < report.Sessions[j].SessionID
	})
	return report
}

func analyzeCacheDiagnosticsSession(sessionID string, records []Record, indices []int) (CacheSessionDiagnostics, []CacheRecordDiagnostics) {
	session := CacheSessionDiagnostics{
		SessionID:               sessionID,
		Records:                 len(indices),
		ProviderCacheUsageState: ProviderCacheUsageUnavailable,
	}
	perRecord := make([]CacheRecordDiagnostics, len(indices))
	blockSeen := countSystemBlockOccurrences(records, indices)
	var previous cacheSignatureParts
	initialized := false
	stable := cacheStability{}
	changedFieldCounts := map[string]int{}
	for i, index := range indices {
		record := records[index]
		signature := cacheSignature(record)
		stable.observe(signature)
		diag := buildCacheRecordDiagnostics(record, blockSeen, len(indices), signature, previous, initialized)
		perRecord[i] = diag
		if diag.CacheableSystemBytes > session.CacheableSystemBytesMax {
			session.CacheableSystemBytesMax = diag.CacheableSystemBytes
		}
		if diag.UncachedSystemBytes > session.UncachedSystemBytesMax {
			session.UncachedSystemBytesMax = diag.UncachedSystemBytes
		}
		for _, field := range diag.RequestSignatureDeltaFromPrevious.ChangedFields {
			changedFieldCounts[field]++
		}
		previous = signature
		initialized = true
	}
	session.SystemHashStable = stable.systemStable()
	session.CacheControlHashStable = stable.cacheStable()
	session.ToolsHashStable = stable.toolsStable()
	session.MessagePrefixHashStable = stable.messageStable()
	session.ThinkingHashStable = stable.thinkingStable()
	session.LargestUncachedSystemBlocks = largestUncachedSystemBlocks(records[indices[0]], blockSeen, len(indices), 5)
	session.RequestSignatureChangedFieldsCount = changedFieldCounts
	session.RequestSignatureChangedFields = sortedChangedFields(changedFieldCounts)
	if len(session.RequestSignatureChangedFieldsCount) == 0 {
		session.RequestSignatureChangedFieldsCount = nil
	}
	return session, perRecord
}

type cacheStability struct {
	system map[string]struct{}
	cache  map[string]struct{}
	tools  map[string]struct{}
	msg    map[string]struct{}
	think  map[string]struct{}
}

func (s *cacheStability) observe(parts cacheSignatureParts) {
	addStableHash(&s.system, parts.SystemHash)
	addStableHash(&s.cache, parts.CacheControlHash)
	addStableHash(&s.tools, parts.ToolsHash)
	addStableHash(&s.msg, parts.MessagePrefixHash)
	addStableHash(&s.think, parts.ThinkingHash)
}

func (s cacheStability) systemStable() bool   { return len(s.system) <= 1 }
func (s cacheStability) cacheStable() bool    { return len(s.cache) <= 1 }
func (s cacheStability) toolsStable() bool    { return len(s.tools) <= 1 }
func (s cacheStability) messageStable() bool  { return len(s.msg) <= 1 }
func (s cacheStability) thinkingStable() bool { return len(s.think) <= 1 }

func addStableHash(set *map[string]struct{}, value string) {
	if *set == nil {
		*set = map[string]struct{}{}
	}
	(*set)[value] = struct{}{}
}

func buildCacheRecordDiagnostics(record Record, blockSeen map[string]int, sessionRecords int, signature, previous cacheSignatureParts, initialized bool) CacheRecordDiagnostics {
	cacheableSystemBytes, uncachedSystemBytes, cacheControlBlocks := systemByteDiagnostics(record.SystemBlocks)
	skills := skillsCatalogDiagnostics(record.SystemBlocks)
	messageMarkers := 0
	for _, msg := range record.MessagesSummary {
		for _, block := range msg.Blocks {
			if block.HasCacheControl {
				messageMarkers++
			}
		}
	}
	return CacheRecordDiagnostics{
		SessionID:                         diagnosticsSessionID(record.SessionID),
		Turn:                              record.Turn,
		CacheableSystemBytes:              cacheableSystemBytes,
		UncachedSystemBytes:               uncachedSystemBytes,
		SkillsCatalogPosition:             skills.Position,
		SkillsCatalogHash:                 skills.Hash,
		SkillsCatalogBytes:                skills.Bytes,
		PrefixBeforeSkillsHash:            skills.PrefixBeforeHash,
		PrefixThroughSkillsHash:           skills.PrefixThroughHash,
		CacheControlBlockCount:            cacheControlBlocks,
		MessageCacheMarkerCount:           messageMarkers,
		LargestUncachedSystemBlocks:       largestUncachedSystemBlocks(record, blockSeen, sessionRecords, 5),
		LargestMessageBlocks:              largestMessageBlocks(record, 5),
		RequestSignatureHash:              signature.SignatureHash,
		RequestSignatureDeltaFromPrevious: cacheSignatureDelta(signature, previous, initialized),
	}
}

func systemByteDiagnostics(blocks []SystemBlockSummary) (cacheableBytes int, uncachedBytes int, cacheControlBlocks int) {
	for _, block := range blocks {
		if block.HasCacheControl {
			cacheableBytes += block.TextBytes
			cacheControlBlocks++
			continue
		}
		uncachedBytes += block.TextBytes
	}
	return cacheableBytes, uncachedBytes, cacheControlBlocks
}

type skillsCatalogDiagnostic struct {
	Position          *int
	Hash              string
	Bytes             int
	PrefixBeforeHash  string
	PrefixThroughHash string
}

func skillsCatalogDiagnostics(blocks []SystemBlockSummary) skillsCatalogDiagnostic {
	for i, block := range blocks {
		if !isSkillsCatalogBlock(block) {
			continue
		}
		position := block.Index
		if position < 0 {
			position = i
		}
		return skillsCatalogDiagnostic{
			Position:          &position,
			Hash:              block.TextHash,
			Bytes:             block.TextBytes,
			PrefixBeforeHash:  systemPrefixHash(blocks[:i]),
			PrefixThroughHash: systemPrefixHash(blocks[:i+1]),
		}
	}
	return skillsCatalogDiagnostic{}
}

func isSkillsCatalogBlock(block SystemBlockSummary) bool {
	return strings.EqualFold(strings.TrimSpace(block.Source), "skills_catalog") || strings.EqualFold(strings.TrimSpace(block.Kind), "skills_catalog")
}

func systemPrefixHash(blocks []SystemBlockSummary) string {
	out := make([]systemPrefixBlock, 0, len(blocks))
	for _, block := range blocks {
		out = append(out, systemPrefixBlock{
			Index:           block.Index,
			Kind:            block.Kind,
			Source:          block.Source,
			TextHash:        block.TextHash,
			HasCacheControl: block.HasCacheControl,
			CacheType:       block.CacheType,
			CacheTTL:        block.CacheTTL,
			CacheScope:      block.CacheScope,
		})
	}
	return hashJSONValue(out)
}

type systemPrefixBlock struct {
	Index           int    `json:"index"`
	Kind            string `json:"kind,omitempty"`
	Source          string `json:"source,omitempty"`
	TextHash        string `json:"text_hash,omitempty"`
	HasCacheControl bool   `json:"has_cache_control,omitempty"`
	CacheType       string `json:"cache_type,omitempty"`
	CacheTTL        string `json:"cache_ttl,omitempty"`
	CacheScope      string `json:"cache_scope,omitempty"`
}

func largestUncachedSystemBlocks(record Record, seen map[string]int, sessionRecords int, limit int) []CacheDiagnosticSystemBlock {
	var blocks []CacheDiagnosticSystemBlock
	for _, block := range record.SystemBlocks {
		if block.HasCacheControl {
			continue
		}
		key := systemBlockKey(block)
		blocks = append(blocks, CacheDiagnosticSystemBlock{
			Index:           block.Index,
			Kind:            block.Kind,
			Source:          block.Source,
			TextBytes:       block.TextBytes,
			TextHash:        block.TextHash,
			HasCacheControl: block.HasCacheControl,
			Stable:          sessionRecords > 0 && seen[key] == sessionRecords,
			SeenCount:       seen[key],
		})
	}
	sort.Slice(blocks, func(i, j int) bool {
		if blocks[i].TextBytes == blocks[j].TextBytes {
			return blocks[i].Index < blocks[j].Index
		}
		return blocks[i].TextBytes > blocks[j].TextBytes
	})
	if limit > 0 && len(blocks) > limit {
		blocks = blocks[:limit]
	}
	return blocks
}

func largestMessageBlocks(record Record, limit int) []CacheDiagnosticMessageBlock {
	var blocks []CacheDiagnosticMessageBlock
	for messageIndex, msg := range record.MessagesSummary {
		for blockIndex, block := range msg.Blocks {
			size := block.TextBytes + block.ThinkingBytes + block.ConnectorTextBytes + block.ContentBytes + block.InputBytes
			if size == 0 {
				continue
			}
			blocks = append(blocks, CacheDiagnosticMessageBlock{
				MessageIndex:    messageIndex,
				Role:            msg.Role,
				BlockIndex:      blockIndex,
				Type:            block.Type,
				Bytes:           size,
				HasCacheControl: block.HasCacheControl,
				ToolName:        block.ToolName,
				ToolUseID:       block.ToolUseID,
			})
		}
	}
	sort.Slice(blocks, func(i, j int) bool {
		if blocks[i].Bytes == blocks[j].Bytes {
			if blocks[i].MessageIndex == blocks[j].MessageIndex {
				return blocks[i].BlockIndex < blocks[j].BlockIndex
			}
			return blocks[i].MessageIndex < blocks[j].MessageIndex
		}
		return blocks[i].Bytes > blocks[j].Bytes
	})
	if limit > 0 && len(blocks) > limit {
		blocks = blocks[:limit]
	}
	return blocks
}

func cacheSignatureDelta(current, previous cacheSignatureParts, initialized bool) CacheSignatureDelta {
	delta := CacheSignatureDelta{
		Initialized:   initialized,
		SignatureHash: current.SignatureHash,
	}
	if !initialized {
		return delta
	}
	delta.PreviousSignatureHash = previous.SignatureHash
	delta.ModelChanged = current.ModelHash != previous.ModelHash
	delta.SystemChanged = current.SystemHash != previous.SystemHash
	delta.CacheControlChanged = current.CacheControlHash != previous.CacheControlHash
	delta.ToolsChanged = current.ToolsHash != previous.ToolsHash
	delta.MessagePrefixChanged = current.MessagePrefixHash != previous.MessagePrefixHash
	delta.ThinkingChanged = current.ThinkingHash != previous.ThinkingHash
	if delta.ModelChanged {
		delta.ChangedFields = append(delta.ChangedFields, "model")
	}
	if delta.SystemChanged {
		delta.ChangedFields = append(delta.ChangedFields, "system")
	}
	if delta.CacheControlChanged {
		delta.ChangedFields = append(delta.ChangedFields, "cache_control")
	}
	if delta.ToolsChanged {
		delta.ChangedFields = append(delta.ChangedFields, "tools")
	}
	if delta.MessagePrefixChanged {
		delta.ChangedFields = append(delta.ChangedFields, "message_prefix")
	}
	if delta.ThinkingChanged {
		delta.ChangedFields = append(delta.ChangedFields, "thinking")
	}
	delta.BreaksCache = len(delta.ChangedFields) > 0
	return delta
}

func cacheSignature(record Record) cacheSignatureParts {
	if record.Request != nil {
		fp := promptcache.RequestFingerprint(*record.Request, record.Request.Thinking)
		return cacheSignatureParts{
			SignatureHash:     fp.SignatureHash,
			ModelHash:         fp.ModelHash,
			SystemHash:        fp.SystemHash,
			CacheControlHash:  fp.CacheControlHash,
			ToolsHash:         fp.ToolsHash,
			MessagePrefixHash: fp.MessagePrefixHash,
			ThinkingHash:      fp.ThinkingHash,
		}
	}
	systemHash := firstNonEmpty(record.SystemHash, hashJSONValue(record.SystemBlocks))
	cacheHash := hashJSONValue(cacheControlSummary(record.SystemBlocks, record.MessagesSummary))
	toolsHash := hashJSONValue(toolSignatureSummary(record.ToolsSummary))
	messageHash := hashJSONValue(messagePrefixSummary(record.MessagesSummary))
	thinkingHash := hashJSONValue(thinkingSummary(record.MessagesSummary))
	modelHash := hashString(strings.TrimSpace(record.Model))
	signatureHash := hashJSONValue(map[string]string{
		"cache_control":  cacheHash,
		"message_prefix": messageHash,
		"model":          modelHash,
		"system":         systemHash,
		"thinking":       thinkingHash,
		"tools":          toolsHash,
	})
	return cacheSignatureParts{
		SignatureHash:     signatureHash,
		ModelHash:         modelHash,
		SystemHash:        systemHash,
		CacheControlHash:  cacheHash,
		ToolsHash:         toolsHash,
		MessagePrefixHash: messageHash,
		ThinkingHash:      thinkingHash,
	}
}

func cacheControlSummary(blocks []SystemBlockSummary, messages []MessageSummary) any {
	type systemCache struct {
		Index int    `json:"index"`
		Type  string `json:"type,omitempty"`
		TTL   string `json:"ttl,omitempty"`
		Scope string `json:"scope,omitempty"`
	}
	type messageCache struct {
		MessageIndex int  `json:"message_index"`
		BlockIndex   int  `json:"block_index"`
		Present      bool `json:"present"`
	}
	out := struct {
		System  []systemCache  `json:"system"`
		Message []messageCache `json:"message"`
	}{}
	for _, block := range blocks {
		out.System = append(out.System, systemCache{Index: block.Index, Type: block.CacheType, TTL: block.CacheTTL, Scope: block.CacheScope})
	}
	for messageIndex, msg := range messages {
		for blockIndex, block := range msg.Blocks {
			if block.HasCacheControl {
				out.Message = append(out.Message, messageCache{MessageIndex: messageIndex, BlockIndex: blockIndex, Present: true})
			}
		}
	}
	return out
}

func toolSignatureSummary(tools []ToolSummary) []ToolSummary {
	out := append([]ToolSummary(nil), tools...)
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func messagePrefixSummary(messages []MessageSummary) []MessageSummary {
	limit := len(messages)
	if limit > 2 {
		limit = 2
	}
	return messages[:limit]
}

func thinkingSummary(messages []MessageSummary) any {
	type block struct {
		MessageIndex  int `json:"message_index"`
		BlockIndex    int `json:"block_index"`
		ThinkingBytes int `json:"thinking_bytes"`
	}
	var out []block
	for messageIndex, msg := range messages {
		for blockIndex, item := range msg.Blocks {
			if item.ThinkingBytes > 0 {
				out = append(out, block{MessageIndex: messageIndex, BlockIndex: blockIndex, ThinkingBytes: item.ThinkingBytes})
			}
		}
	}
	return out
}

func countSystemBlockOccurrences(records []Record, indices []int) map[string]int {
	seen := map[string]int{}
	for _, index := range indices {
		for _, block := range records[index].SystemBlocks {
			seen[systemBlockKey(block)]++
		}
	}
	return seen
}

func systemBlockKey(block SystemBlockSummary) string {
	parts := []string{
		strings.TrimSpace(block.Kind),
		strings.TrimSpace(block.Source),
		strings.TrimSpace(block.TextHash),
	}
	return strings.Join(parts, "|")
}

func sortedChangedFields(counts map[string]int) []string {
	out := make([]string, 0, len(counts))
	for field := range counts {
		out = append(out, field)
	}
	sort.Strings(out)
	return out
}

func diagnosticsSessionID(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "(missing)"
	}
	return sessionID
}

func hashJSONValue(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return hashString("")
	}
	return hashString(string(data))
}
