package server

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/promptdump"
)

const (
	defaultPromptDumpViewerPath = "/tmp/niuma-tui-prompt.jsonl"
	defaultPromptDumpLimit      = 200
	maxPromptDumpLimit          = 1000
)

type promptDumpAPIResponse struct {
	Path        string                     `json:"path"`
	Exists      bool                       `json:"exists"`
	SizeBytes   int64                      `json:"size_bytes,omitempty"`
	GeneratedAt time.Time                  `json:"generated_at"`
	Filter      promptDumpFilter           `json:"filter"`
	Sessions    []promptDumpSessionSummary `json:"sessions"`
	Records     []promptDumpRecordView     `json:"records"`
	Warning     string                     `json:"warning,omitempty"`
}

type promptDumpFilter struct {
	SessionID      string `json:"session_id,omitempty"`
	Limit          int    `json:"limit"`
	IncludeRequest bool   `json:"include_request"`
}

type promptDumpSessionSummary struct {
	SessionID           string                             `json:"session_id"`
	Records             int                                `json:"records"`
	FirstTimestamp      string                             `json:"first_timestamp,omitempty"`
	LastTimestamp       string                             `json:"last_timestamp,omitempty"`
	MaxTurn             int                                `json:"max_turn,omitempty"`
	Models              []string                           `json:"models,omitempty"`
	Scopes              []string                           `json:"scopes,omitempty"`
	PromptModes         []string                           `json:"prompt_modes,omitempty"`
	SystemHashes        []string                           `json:"system_hashes,omitempty"`
	SystemBytesMax      int                                `json:"system_bytes_max,omitempty"`
	ToolCountMax        int                                `json:"tool_count_max,omitempty"`
	CacheControlBlocks  int                                `json:"cache_control_blocks,omitempty"`
	RawRequestRecords   int                                `json:"raw_request_records,omitempty"`
	TraceUsageAvailable bool                               `json:"trace_usage_available"`
	TraceSummary        traceSummary                       `json:"trace_summary,omitempty"`
	CacheDiagnostics    promptdump.CacheSessionDiagnostics `json:"cache_diagnostics"`
}

type promptDumpRecordView struct {
	LineNumber         int                               `json:"line_number"`
	Timestamp          string                            `json:"timestamp,omitempty"`
	SessionID          string                            `json:"session_id,omitempty"`
	TenantSessionID    uint64                            `json:"tenant_session_id,omitempty"`
	ParentSessionID    uint64                            `json:"parent_session_id,omitempty"`
	TaskID             uint64                            `json:"task_id,omitempty"`
	Turn               int                               `json:"turn"`
	Scope              string                            `json:"scope,omitempty"`
	QuerySource        string                            `json:"query_source,omitempty"`
	PromptMode         string                            `json:"prompt_mode,omitempty"`
	PromptProfile      string                            `json:"prompt_profile,omitempty"`
	AgentName          string                            `json:"agent_name,omitempty"`
	AgentMode          string                            `json:"agent_mode,omitempty"`
	Model              string                            `json:"model"`
	MaxTokens          int                               `json:"max_tokens"`
	SystemBytes        int                               `json:"system_bytes"`
	SystemHash         string                            `json:"system_hash,omitempty"`
	SystemBlockCount   int                               `json:"system_block_count"`
	MessageCount       int                               `json:"message_count"`
	ToolCount          int                               `json:"tool_count"`
	CacheControlBlocks int                               `json:"cache_control_blocks"`
	CacheControlBytes  int                               `json:"cache_control_bytes"`
	RuntimeStatus      promptdump.RuntimeStatusSummary   `json:"runtime_status"`
	CompactState       promptdump.CompactState           `json:"compact_state"`
	ToolResultStats    promptdump.ToolResultStats        `json:"tool_result_stats"`
	RequestRedaction   promptdump.RequestRedaction       `json:"request_redaction"`
	SystemBlocks       []promptdump.SystemBlockSummary   `json:"system_blocks_summary,omitempty"`
	MessagesSummary    []promptdump.MessageSummary       `json:"messages_summary,omitempty"`
	ToolsSummary       []promptdump.ToolSummary          `json:"tools_summary,omitempty"`
	ContextManifest    any                               `json:"context_manifest,omitempty"`
	CacheDiagnostics   promptdump.CacheRecordDiagnostics `json:"cache_diagnostics"`
	Request            *anthropic.MessagesRequest        `json:"request,omitempty"`
}

type promptDumpSessionAccumulator struct {
	summary      promptDumpSessionSummary
	models       map[string]struct{}
	scopes       map[string]struct{}
	modes        map[string]struct{}
	systemHashes map[string]struct{}
}

type promptDumpRawRecord struct {
	lineNumber int
	record     promptdump.Record
}

func promptDumpUIHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "prompt_dump_ui", "server.promptDumpUIHandler", "serve prompt dump ui")
		if !requireLocalClient(w, r) {
			return
		}
		if !authorizeViewerShell(w, r, opts.AuthToken) {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(promptDumpHTML))
	}
}

func promptDumpAPIRecordsHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "prompt_dump_records", "server.promptDumpAPIRecordsHandler", "list prompt dump records")
		// 完整 prompt 正文：本机直连 + Authorization 头，两道都要过。
		if !requireLocalClient(w, r) {
			return
		}
		if !authorize(w, r, opts.AuthToken) {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		resp, err := buildPromptDumpAPIResponse(r)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, os.ErrNotExist) {
				status = http.StatusOK
			} else if errors.Is(err, errPromptDumpBadRequest) {
				status = http.StatusBadRequest
			}
			writeTenantError(w, status, err.Error())
			return
		}
		writeJSON(w, resp)
	}
}

var errPromptDumpBadRequest = errors.New("bad prompt dump request")

func buildPromptDumpAPIResponse(r *http.Request) (promptDumpAPIResponse, error) {
	filter := promptDumpFilter{
		SessionID:      strings.TrimSpace(r.URL.Query().Get("session_id")),
		Limit:          parsePromptDumpLimit(r.URL.Query().Get("limit")),
		IncludeRequest: parsePromptDumpBool(r.URL.Query().Get("include_request")),
	}
	path := promptDumpViewerPath()
	resp := promptDumpAPIResponse{
		Path:        path,
		GeneratedAt: time.Now().UTC(),
		Filter:      filter,
		Sessions:    []promptDumpSessionSummary{},
		Records:     []promptDumpRecordView{},
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			resp.Warning = "prompt dump file does not exist; start TUI with NIUMA_DUMP_PROMPT_JSON and NIUMA_DUMP_PROMPT_FULL=true"
			return resp, nil
		}
		return resp, err
	}
	if info.IsDir() {
		return resp, fmt.Errorf("%w: prompt dump path is a directory", errPromptDumpBadRequest)
	}
	resp.Exists = true
	resp.SizeBytes = info.Size()
	records, sessions, err := readPromptDumpFile(path, filter)
	if err != nil {
		return resp, err
	}
	resp.Records = records
	resp.Sessions = sessions
	return resp, nil
}

func promptDumpViewerPath() string {
	if path := strings.TrimSpace(os.Getenv(promptdump.PathEnv)); path != "" {
		return path
	}
	return defaultPromptDumpViewerPath
}

func parsePromptDumpLimit(raw string) int {
	limit, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || limit <= 0 {
		return defaultPromptDumpLimit
	}
	if limit > maxPromptDumpLimit {
		return maxPromptDumpLimit
	}
	return limit
}

func parsePromptDumpBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func readPromptDumpFile(path string, filter promptDumpFilter) ([]promptDumpRecordView, []promptDumpSessionSummary, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	var rawRecords []promptDumpRawRecord
	sessions := map[string]*promptDumpSessionAccumulator{}
	lineNumber := 0
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, nil, readErr
		}
		line = strings.TrimSpace(line)
		if line != "" {
			lineNumber++
			var record promptdump.Record
			if err := json.Unmarshal([]byte(line), &record); err != nil {
				return nil, nil, fmt.Errorf("parse prompt dump %q line %d: %w", path, lineNumber, err)
			}
			rawRecords = append(rawRecords, promptDumpRawRecord{lineNumber: lineNumber, record: record})
			updatePromptDumpSessionSummary(sessions, record)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
	}
	cacheSessions, cacheRecords := promptDumpCacheDiagnostics(rawRecords)
	var records []promptDumpRecordView
	for _, raw := range rawRecords {
		if promptDumpRecordMatches(raw.record, filter) && len(records) < filter.Limit {
			records = append(records, promptDumpRecordViewFromRecord(raw.lineNumber, raw.record, filter.IncludeRequest, cacheRecords[raw.lineNumber]))
		}
	}
	out := make([]promptDumpSessionSummary, 0, len(sessions))
	for _, acc := range sessions {
		acc.summary.Models = sortedPromptDumpSet(acc.models)
		acc.summary.Scopes = sortedPromptDumpSet(acc.scopes)
		acc.summary.PromptModes = sortedPromptDumpSet(acc.modes)
		acc.summary.SystemHashes = sortedPromptDumpSet(acc.systemHashes)
		acc.summary.CacheDiagnostics = cacheSessions[acc.summary.SessionID]
		if detail, err := buildLocalTraceDetail(localTraceStores(), acc.summary.SessionID); err == nil {
			acc.summary.TraceUsageAvailable = detail.Summary.TotalTokens > 0 || detail.Summary.CacheReadTokens > 0 || detail.Summary.CacheCreationTokens > 0
			acc.summary.TraceSummary = detail.Summary
			applyProviderCacheUsageDiagnostics(&acc.summary.CacheDiagnostics, detail.Summary)
		}
		out = append(out, acc.summary)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].LastTimestamp == out[j].LastTimestamp {
			return out[i].SessionID < out[j].SessionID
		}
		return out[i].LastTimestamp > out[j].LastTimestamp
	})
	return records, out, nil
}

func promptDumpCacheDiagnostics(rawRecords []promptDumpRawRecord) (map[string]promptdump.CacheSessionDiagnostics, map[int]promptdump.CacheRecordDiagnostics) {
	records := make([]promptdump.Record, 0, len(rawRecords))
	lineNumbers := make([]int, 0, len(rawRecords))
	for _, raw := range rawRecords {
		records = append(records, raw.record)
		lineNumbers = append(lineNumbers, raw.lineNumber)
	}
	report := promptdump.AnalyzeCacheDiagnostics(records)
	sessionDiagnostics := map[string]promptdump.CacheSessionDiagnostics{}
	for _, session := range report.Sessions {
		sessionDiagnostics[session.SessionID] = session
	}
	recordDiagnostics := map[int]promptdump.CacheRecordDiagnostics{}
	for i, diag := range report.Records {
		if i < len(lineNumbers) {
			recordDiagnostics[lineNumbers[i]] = diag
		}
	}
	return sessionDiagnostics, recordDiagnostics
}

func applyProviderCacheUsageDiagnostics(diag *promptdump.CacheSessionDiagnostics, summary traceSummary) {
	diag.ProviderCacheUsageState = promptdump.ProviderCacheUsageUnavailable
	if summary.InputTokens == 0 && summary.CacheCreationTokens == 0 && summary.CacheReadTokens == 0 {
		return
	}
	if summary.CacheReadTokens > 0 || summary.CacheCreationTokens > 0 {
		diag.ProviderCacheUsageState = promptdump.ProviderCacheUsageReported
	} else {
		diag.ProviderCacheUsageState = promptdump.ProviderCacheUsageNotReported
	}
	if summary.InputTokens > 0 && summary.CacheReadTokens > 0 {
		diag.ProviderCacheHitRatioInput = float64(summary.CacheReadTokens) / float64(summary.InputTokens)
	}
	totalInput := summary.InputTokens + summary.CacheCreationTokens + summary.CacheReadTokens
	if totalInput > 0 && summary.CacheReadTokens > 0 {
		diag.ProviderCacheHitRatioTotalInput = float64(summary.CacheReadTokens) / float64(totalInput)
	}
}

func promptDumpRecordMatches(record promptdump.Record, filter promptDumpFilter) bool {
	if filter.SessionID != "" && record.SessionID != filter.SessionID {
		return false
	}
	return true
}

func promptDumpRecordViewFromRecord(lineNumber int, record promptdump.Record, includeRequest bool, cacheDiagnostics promptdump.CacheRecordDiagnostics) promptDumpRecordView {
	cacheBlocks, cacheBytes := promptDumpCacheControlStats(record)
	view := promptDumpRecordView{
		LineNumber:         lineNumber,
		Timestamp:          record.Timestamp,
		SessionID:          record.SessionID,
		TenantSessionID:    record.TenantSessionID,
		ParentSessionID:    record.ParentSessionID,
		TaskID:             record.TaskID,
		Turn:               record.Turn,
		Scope:              record.Scope,
		QuerySource:        record.QuerySource,
		PromptMode:         record.PromptMode,
		PromptProfile:      record.PromptProfile,
		AgentName:          record.AgentName,
		AgentMode:          record.AgentMode,
		Model:              record.Model,
		MaxTokens:          record.MaxTokens,
		SystemBytes:        record.SystemBytes,
		SystemHash:         record.SystemHash,
		SystemBlockCount:   record.SystemBlockCount,
		MessageCount:       record.MessageCount,
		ToolCount:          record.ToolCount,
		CacheControlBlocks: cacheBlocks,
		CacheControlBytes:  cacheBytes,
		RuntimeStatus:      record.RuntimeStatus,
		CompactState:       record.CompactState,
		ToolResultStats:    record.ToolResultStats,
		RequestRedaction:   record.RequestRedaction,
		SystemBlocks:       record.SystemBlocks,
		MessagesSummary:    record.MessagesSummary,
		ToolsSummary:       record.ToolsSummary,
		ContextManifest:    record.ContextManifest,
		CacheDiagnostics:   cacheDiagnostics,
	}
	if includeRequest {
		view.Request = record.Request
	}
	return view
}

func updatePromptDumpSessionSummary(sessions map[string]*promptDumpSessionAccumulator, record promptdump.Record) {
	sessionID := strings.TrimSpace(record.SessionID)
	if sessionID == "" {
		sessionID = "(missing)"
	}
	acc := sessions[sessionID]
	if acc == nil {
		acc = &promptDumpSessionAccumulator{
			summary:      promptDumpSessionSummary{SessionID: sessionID},
			models:       map[string]struct{}{},
			scopes:       map[string]struct{}{},
			modes:        map[string]struct{}{},
			systemHashes: map[string]struct{}{},
		}
		sessions[sessionID] = acc
	}
	acc.summary.Records++
	if acc.summary.FirstTimestamp == "" || record.Timestamp < acc.summary.FirstTimestamp {
		acc.summary.FirstTimestamp = record.Timestamp
	}
	if record.Timestamp > acc.summary.LastTimestamp {
		acc.summary.LastTimestamp = record.Timestamp
	}
	if record.Turn > acc.summary.MaxTurn {
		acc.summary.MaxTurn = record.Turn
	}
	if record.SystemBytes > acc.summary.SystemBytesMax {
		acc.summary.SystemBytesMax = record.SystemBytes
	}
	if record.ToolCount > acc.summary.ToolCountMax {
		acc.summary.ToolCountMax = record.ToolCount
	}
	cacheBlocks, _ := promptDumpCacheControlStats(record)
	acc.summary.CacheControlBlocks += cacheBlocks
	if record.Request != nil {
		acc.summary.RawRequestRecords++
	}
	addPromptDumpSet(acc.models, record.Model)
	addPromptDumpSet(acc.scopes, firstNonEmptyTrace(record.Scope, "main"))
	addPromptDumpSet(acc.modes, record.PromptMode)
	addPromptDumpSet(acc.systemHashes, record.SystemHash)
}

func promptDumpCacheControlStats(record promptdump.Record) (int, int) {
	blocks := 0
	bytes := 0
	for _, block := range record.SystemBlocks {
		if block.HasCacheControl {
			blocks++
			bytes += block.TextBytes
		}
	}
	for _, msg := range record.MessagesSummary {
		for _, block := range msg.Blocks {
			if block.HasCacheControl {
				blocks++
				bytes += block.TextBytes + block.ContentBytes + block.InputBytes
			}
		}
	}
	return blocks, bytes
}

func addPromptDumpSet(set map[string]struct{}, value string) {
	value = strings.TrimSpace(value)
	if value != "" {
		set[value] = struct{}{}
	}
}

func sortedPromptDumpSet(set map[string]struct{}) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

//go:embed prompt_dump_viewer.html
var promptDumpHTML string
