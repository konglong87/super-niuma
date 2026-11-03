package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/konglong87/super-niuma/internal/promptdump"
)

type stringListFlag []string

func (f *stringListFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *stringListFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value != "" {
		*f = append(*f, value)
	}
	return nil
}

type transcriptUsageSummary struct {
	SessionID                string `json:"session_id"`
	Path                     string `json:"path,omitempty"`
	UsageEntries             int    `json:"usage_entries"`
	InputTokens              int    `json:"input_tokens"`
	CacheCreationInputTokens int    `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int    `json:"cache_read_input_tokens"`
	OutputTokens             int    `json:"output_tokens"`
}

type diagnosticSessionOutput struct {
	promptdump.CacheSessionDiagnostics
	SkillsCatalogPosition   *int                    `json:"skills_catalog_position,omitempty"`
	SkillsCatalogHash       string                  `json:"skills_catalog_hash,omitempty"`
	SkillsCatalogBytes      int                     `json:"skills_catalog_bytes,omitempty"`
	PrefixBeforeSkillsHash  string                  `json:"prefix_before_skills_hash,omitempty"`
	PrefixThroughSkillsHash string                  `json:"prefix_through_skills_hash,omitempty"`
	TranscriptUsage         *transcriptUsageSummary `json:"transcript_usage,omitempty"`
}

type diagnosticOutput struct {
	Sessions []diagnosticSessionOutput           `json:"sessions"`
	Records  []promptdump.CacheRecordDiagnostics `json:"records,omitempty"`
}

func main() {
	dumpPath := flag.String("dump", "", "niuma prompt dump JSONL path")
	sessionID := flag.String("session", "", "only include diagnostics for this session id")
	summaryOnly := flag.Bool("summary", false, "only print session-level diagnostics")
	var transcriptPaths stringListFlag
	flag.Var(&transcriptPaths, "transcript", "niuma transcript JSONL path with usage entries; may be repeated")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: go run ./scripts/prompt-cache-diagnostics --dump <prompt-dump.jsonl> [--session <id>] [--summary] [--transcript <session.jsonl> ...]\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *dumpPath == "" || flag.NArg() != 0 {
		flag.Usage()
		os.Exit(2)
	}
	records, err := readRecords(*dumpPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read prompt dump: %v\n", err)
		os.Exit(2)
	}
	report := promptdump.AnalyzeCacheDiagnostics(records)
	output, err := buildDiagnosticOutput(report, transcriptPaths)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read transcript usage: %v\n", err)
		os.Exit(2)
	}
	output = filterDiagnosticOutput(output, *sessionID)
	if *summaryOnly {
		output.Records = nil
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(output); err != nil {
		fmt.Fprintf(os.Stderr, "write report: %v\n", err)
		os.Exit(2)
	}
}

func buildDiagnosticOutput(report promptdump.CacheDiagnosticsReport, transcriptPaths []string) (diagnosticOutput, error) {
	summaries := map[string]transcriptUsageSummary{}
	for _, path := range transcriptPaths {
		summary, err := readTranscriptUsage(path)
		if err != nil {
			return diagnosticOutput{}, err
		}
		if existing, ok := summaries[summary.SessionID]; ok {
			summary = mergeTranscriptUsage(existing, summary)
		}
		summaries[summary.SessionID] = summary
	}
	out := diagnosticOutput{
		Sessions: make([]diagnosticSessionOutput, 0, len(report.Sessions)),
		Records:  report.Records,
	}
	for _, session := range report.Sessions {
		item := diagnosticSessionOutput{CacheSessionDiagnostics: session}
		applySessionRecordDiagnostics(&item, report.Records)
		if usage, ok := summaries[session.SessionID]; ok {
			applyTranscriptUsage(&item.CacheSessionDiagnostics, usage)
			usageCopy := usage
			item.TranscriptUsage = &usageCopy
		}
		out.Sessions = append(out.Sessions, item)
	}
	return out, nil
}

func applySessionRecordDiagnostics(session *diagnosticSessionOutput, records []promptdump.CacheRecordDiagnostics) {
	for _, record := range records {
		if record.SessionID != session.SessionID || record.SkillsCatalogPosition == nil {
			continue
		}
		position := *record.SkillsCatalogPosition
		session.SkillsCatalogPosition = &position
		session.SkillsCatalogHash = record.SkillsCatalogHash
		session.SkillsCatalogBytes = record.SkillsCatalogBytes
		session.PrefixBeforeSkillsHash = record.PrefixBeforeSkillsHash
		session.PrefixThroughSkillsHash = record.PrefixThroughSkillsHash
		return
	}
}

func filterDiagnosticOutput(out diagnosticOutput, sessionID string) diagnosticOutput {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return out
	}
	filtered := diagnosticOutput{
		Sessions: []diagnosticSessionOutput{},
		Records:  []promptdump.CacheRecordDiagnostics{},
	}
	for _, session := range out.Sessions {
		if session.SessionID == sessionID {
			filtered.Sessions = append(filtered.Sessions, session)
		}
	}
	for _, record := range out.Records {
		if record.SessionID == sessionID {
			filtered.Records = append(filtered.Records, record)
		}
	}
	return filtered
}

func readTranscriptUsage(path string) (transcriptUsageSummary, error) {
	file, err := os.Open(path)
	if err != nil {
		return transcriptUsageSummary{}, err
	}
	defer file.Close()
	summary := transcriptUsageSummary{
		SessionID: strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
		Path:      path,
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1024*1024), 32*1024*1024)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry struct {
			Type                     string `json:"type"`
			InputTokens              int    `json:"input_tokens"`
			CacheCreationInputTokens int    `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int    `json:"cache_read_input_tokens"`
			OutputTokens             int    `json:"output_tokens"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return transcriptUsageSummary{}, fmt.Errorf("%s line %d: %w", path, lineNumber, err)
		}
		if entry.Type != "usage" {
			continue
		}
		summary.UsageEntries++
		summary.InputTokens += entry.InputTokens
		summary.CacheCreationInputTokens += entry.CacheCreationInputTokens
		summary.CacheReadInputTokens += entry.CacheReadInputTokens
		summary.OutputTokens += entry.OutputTokens
	}
	if err := scanner.Err(); err != nil {
		return transcriptUsageSummary{}, err
	}
	return summary, nil
}

func mergeTranscriptUsage(left, right transcriptUsageSummary) transcriptUsageSummary {
	left.UsageEntries += right.UsageEntries
	left.InputTokens += right.InputTokens
	left.CacheCreationInputTokens += right.CacheCreationInputTokens
	left.CacheReadInputTokens += right.CacheReadInputTokens
	left.OutputTokens += right.OutputTokens
	if left.Path == "" {
		left.Path = right.Path
	} else if right.Path != "" && left.Path != right.Path {
		left.Path += "," + right.Path
	}
	return left
}

func applyTranscriptUsage(diag *promptdump.CacheSessionDiagnostics, usage transcriptUsageSummary) {
	diag.ProviderCacheUsageState = promptdump.ProviderCacheUsageUnavailable
	if usage.UsageEntries == 0 {
		return
	}
	if usage.CacheReadInputTokens > 0 || usage.CacheCreationInputTokens > 0 {
		diag.ProviderCacheUsageState = promptdump.ProviderCacheUsageReported
	} else {
		diag.ProviderCacheUsageState = promptdump.ProviderCacheUsageNotReported
	}
	if usage.InputTokens > 0 && usage.CacheReadInputTokens > 0 {
		diag.ProviderCacheHitRatioInput = float64(usage.CacheReadInputTokens) / float64(usage.InputTokens)
	}
	totalInput := usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens
	if totalInput > 0 && usage.CacheReadInputTokens > 0 {
		diag.ProviderCacheHitRatioTotalInput = float64(usage.CacheReadInputTokens) / float64(totalInput)
	}
}

func readRecords(path string) ([]promptdump.Record, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var records []promptdump.Record
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1024*1024), 32*1024*1024)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var record promptdump.Record
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNumber, err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return records, nil
}
