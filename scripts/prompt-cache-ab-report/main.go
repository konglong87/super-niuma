package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
)

type diagnosticFile struct {
	Sessions []diagnosticSession `json:"sessions"`
}

type diagnosticSession struct {
	SessionID                          string                  `json:"session_id"`
	Records                            int                     `json:"records"`
	ProviderCacheUsageState            string                  `json:"provider_cache_usage_state"`
	ProviderCacheHitRatioInput         float64                 `json:"provider_cache_hit_ratio_input,omitempty"`
	ProviderCacheHitRatioTotalInput    float64                 `json:"provider_cache_hit_ratio_total_input,omitempty"`
	SystemHashStable                   bool                    `json:"system_hash_stable"`
	CacheControlHashStable             bool                    `json:"cache_control_hash_stable"`
	ToolsHashStable                    bool                    `json:"tools_hash_stable"`
	MessagePrefixHashStable            bool                    `json:"message_prefix_hash_stable"`
	ThinkingHashStable                 bool                    `json:"thinking_hash_stable"`
	CacheableSystemBytesMax            int                     `json:"cacheable_system_bytes_max,omitempty"`
	UncachedSystemBytesMax             int                     `json:"uncached_system_bytes_max,omitempty"`
	SkillsCatalogPosition              *int                    `json:"skills_catalog_position,omitempty"`
	SkillsCatalogHash                  string                  `json:"skills_catalog_hash,omitempty"`
	SkillsCatalogBytes                 int                     `json:"skills_catalog_bytes,omitempty"`
	PrefixBeforeSkillsHash             string                  `json:"prefix_before_skills_hash,omitempty"`
	PrefixThroughSkillsHash            string                  `json:"prefix_through_skills_hash,omitempty"`
	LargestUncachedSystemBlocks        []diagnosticSystemBlock `json:"largest_uncached_system_blocks,omitempty"`
	RequestSignatureChangedFields      []string                `json:"request_signature_changed_fields,omitempty"`
	RequestSignatureChangedFieldsCount map[string]int          `json:"request_signature_changed_fields_count,omitempty"`
	TranscriptUsage                    *transcriptUsage        `json:"transcript_usage,omitempty"`
}

type diagnosticSystemBlock struct {
	Index     int    `json:"index"`
	Kind      string `json:"kind,omitempty"`
	Source    string `json:"source,omitempty"`
	TextBytes int    `json:"text_bytes"`
	TextHash  string `json:"text_hash,omitempty"`
	Stable    bool   `json:"stable"`
	SeenCount int    `json:"seen_count,omitempty"`
}

type transcriptUsage struct {
	SessionID                string `json:"session_id"`
	Path                     string `json:"path,omitempty"`
	UsageEntries             int    `json:"usage_entries"`
	InputTokens              int    `json:"input_tokens"`
	CacheCreationInputTokens int    `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int    `json:"cache_read_input_tokens"`
	OutputTokens             int    `json:"output_tokens"`
}

type sessionSnapshot struct {
	Label                              string                 `json:"label"`
	SessionID                          string                 `json:"session_id"`
	Records                            int                    `json:"records"`
	ProviderCacheUsageState            string                 `json:"provider_cache_usage_state"`
	ProviderCacheHitRatioInput         float64                `json:"provider_cache_hit_ratio_input,omitempty"`
	ProviderCacheHitRatioTotalInput    float64                `json:"provider_cache_hit_ratio_total_input,omitempty"`
	SystemHashStable                   bool                   `json:"system_hash_stable"`
	CacheControlHashStable             bool                   `json:"cache_control_hash_stable"`
	ToolsHashStable                    bool                   `json:"tools_hash_stable"`
	MessagePrefixHashStable            bool                   `json:"message_prefix_hash_stable"`
	ThinkingHashStable                 bool                   `json:"thinking_hash_stable"`
	CacheableSystemBytesMax            int                    `json:"cacheable_system_bytes_max,omitempty"`
	UncachedSystemBytesMax             int                    `json:"uncached_system_bytes_max,omitempty"`
	SkillsCatalogPosition              *int                   `json:"skills_catalog_position,omitempty"`
	SkillsCatalogHash                  string                 `json:"skills_catalog_hash,omitempty"`
	SkillsCatalogBytes                 int                    `json:"skills_catalog_bytes,omitempty"`
	PrefixBeforeSkillsHash             string                 `json:"prefix_before_skills_hash,omitempty"`
	PrefixThroughSkillsHash            string                 `json:"prefix_through_skills_hash,omitempty"`
	LargestUncachedSystemBlock         *diagnosticSystemBlock `json:"largest_uncached_system_block,omitempty"`
	RequestSignatureChangedFields      []string               `json:"request_signature_changed_fields,omitempty"`
	RequestSignatureChangedFieldsCount map[string]int         `json:"request_signature_changed_fields_count,omitempty"`
	TranscriptUsage                    *transcriptUsage       `json:"transcript_usage,omitempty"`
}

type reportDelta struct {
	ProviderCacheHitRatioInput      float64 `json:"provider_cache_hit_ratio_input"`
	ProviderCacheHitRatioTotalInput float64 `json:"provider_cache_hit_ratio_total_input"`
	InputTokens                     int     `json:"input_tokens,omitempty"`
	CacheReadInputTokens            int     `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens        int     `json:"cache_creation_input_tokens,omitempty"`
	CacheableSystemBytesMax         int     `json:"cacheable_system_bytes_max,omitempty"`
	UncachedSystemBytesMax          int     `json:"uncached_system_bytes_max,omitempty"`
	SkillsCatalogPosition           *int    `json:"skills_catalog_position,omitempty"`
	SkillsCatalogBytes              int     `json:"skills_catalog_bytes,omitempty"`
}

type abReport struct {
	Baseline  sessionSnapshot `json:"baseline"`
	Candidate sessionSnapshot `json:"candidate"`
	Delta     reportDelta     `json:"delta"`
	Signals   []string        `json:"signals,omitempty"`
	Warnings  []string        `json:"warnings,omitempty"`
}

func main() {
	baselinePath := flag.String("baseline", "", "baseline prompt-cache-diagnostics summary JSON path")
	candidatePath := flag.String("candidate", "", "candidate prompt-cache-diagnostics summary JSON path")
	baselineSession := flag.String("baseline-session", "", "baseline session id when the summary contains multiple sessions")
	candidateSession := flag.String("candidate-session", "", "candidate session id when the summary contains multiple sessions")
	baselineLabel := flag.String("baseline-label", "baseline", "label for the baseline run")
	candidateLabel := flag.String("candidate-label", "candidate", "label for the candidate run")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: go run ./scripts/prompt-cache-ab-report --baseline <baseline-summary.json> --candidate <candidate-summary.json> [flags]\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *baselinePath == "" || *candidatePath == "" || flag.NArg() != 0 {
		flag.Usage()
		os.Exit(2)
	}
	report, err := buildABReport(*baselinePath, *candidatePath, *baselineSession, *candidateSession, *baselineLabel, *candidateLabel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build A/B report: %v\n", err)
		os.Exit(2)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(report); err != nil {
		fmt.Fprintf(os.Stderr, "write report: %v\n", err)
		os.Exit(2)
	}
}

func buildABReport(baselinePath, candidatePath, baselineSessionID, candidateSessionID, baselineLabel, candidateLabel string) (abReport, error) {
	baseline, err := readDiagnosticSession(baselinePath, baselineSessionID)
	if err != nil {
		return abReport{}, fmt.Errorf("baseline: %w", err)
	}
	candidate, err := readDiagnosticSession(candidatePath, candidateSessionID)
	if err != nil {
		return abReport{}, fmt.Errorf("candidate: %w", err)
	}
	report := abReport{
		Baseline:  snapshotSession(baseline, baselineLabel),
		Candidate: snapshotSession(candidate, candidateLabel),
	}
	report.Delta = compareSnapshots(report.Baseline, report.Candidate)
	report.Signals, report.Warnings = classifyReport(report)
	return report, nil
}

func readDiagnosticSession(path, sessionID string) (diagnosticSession, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return diagnosticSession{}, err
	}
	var file diagnosticFile
	if err := json.Unmarshal(data, &file); err != nil {
		return diagnosticSession{}, err
	}
	if len(file.Sessions) == 0 {
		return diagnosticSession{}, errors.New("summary contains no sessions")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		if len(file.Sessions) != 1 {
			return diagnosticSession{}, fmt.Errorf("summary contains %d sessions; pass a session id", len(file.Sessions))
		}
		return file.Sessions[0], nil
	}
	for _, session := range file.Sessions {
		if session.SessionID == sessionID {
			return session, nil
		}
	}
	return diagnosticSession{}, fmt.Errorf("session %q not found", sessionID)
}

func snapshotSession(session diagnosticSession, label string) sessionSnapshot {
	var top *diagnosticSystemBlock
	if len(session.LargestUncachedSystemBlocks) > 0 {
		block := session.LargestUncachedSystemBlocks[0]
		top = &block
	}
	return sessionSnapshot{
		Label:                              strings.TrimSpace(label),
		SessionID:                          session.SessionID,
		Records:                            session.Records,
		ProviderCacheUsageState:            session.ProviderCacheUsageState,
		ProviderCacheHitRatioInput:         session.ProviderCacheHitRatioInput,
		ProviderCacheHitRatioTotalInput:    session.ProviderCacheHitRatioTotalInput,
		SystemHashStable:                   session.SystemHashStable,
		CacheControlHashStable:             session.CacheControlHashStable,
		ToolsHashStable:                    session.ToolsHashStable,
		MessagePrefixHashStable:            session.MessagePrefixHashStable,
		ThinkingHashStable:                 session.ThinkingHashStable,
		CacheableSystemBytesMax:            session.CacheableSystemBytesMax,
		UncachedSystemBytesMax:             session.UncachedSystemBytesMax,
		SkillsCatalogPosition:              session.SkillsCatalogPosition,
		SkillsCatalogHash:                  session.SkillsCatalogHash,
		SkillsCatalogBytes:                 session.SkillsCatalogBytes,
		PrefixBeforeSkillsHash:             session.PrefixBeforeSkillsHash,
		PrefixThroughSkillsHash:            session.PrefixThroughSkillsHash,
		LargestUncachedSystemBlock:         top,
		RequestSignatureChangedFields:      session.RequestSignatureChangedFields,
		RequestSignatureChangedFieldsCount: session.RequestSignatureChangedFieldsCount,
		TranscriptUsage:                    session.TranscriptUsage,
	}
}

func compareSnapshots(baseline, candidate sessionSnapshot) reportDelta {
	delta := reportDelta{
		ProviderCacheHitRatioInput:      candidate.ProviderCacheHitRatioInput - baseline.ProviderCacheHitRatioInput,
		ProviderCacheHitRatioTotalInput: candidate.ProviderCacheHitRatioTotalInput - baseline.ProviderCacheHitRatioTotalInput,
		CacheableSystemBytesMax:         candidate.CacheableSystemBytesMax - baseline.CacheableSystemBytesMax,
		UncachedSystemBytesMax:          candidate.UncachedSystemBytesMax - baseline.UncachedSystemBytesMax,
		SkillsCatalogBytes:              candidate.SkillsCatalogBytes - baseline.SkillsCatalogBytes,
	}
	if baseline.TranscriptUsage != nil && candidate.TranscriptUsage != nil {
		delta.InputTokens = candidate.TranscriptUsage.InputTokens - baseline.TranscriptUsage.InputTokens
		delta.CacheReadInputTokens = candidate.TranscriptUsage.CacheReadInputTokens - baseline.TranscriptUsage.CacheReadInputTokens
		delta.CacheCreationInputTokens = candidate.TranscriptUsage.CacheCreationInputTokens - baseline.TranscriptUsage.CacheCreationInputTokens
	}
	if baseline.SkillsCatalogPosition != nil && candidate.SkillsCatalogPosition != nil {
		positionDelta := *candidate.SkillsCatalogPosition - *baseline.SkillsCatalogPosition
		delta.SkillsCatalogPosition = &positionDelta
	}
	return delta
}

func classifyReport(report abReport) ([]string, []string) {
	var signals []string
	var warnings []string
	if report.Candidate.ProviderCacheHitRatioInput-report.Baseline.ProviderCacheHitRatioInput >= 0.01 {
		signals = append(signals, "provider_cache_hit_ratio_input_increased")
	} else if report.Candidate.ProviderCacheHitRatioInput-report.Baseline.ProviderCacheHitRatioInput <= -0.01 {
		warnings = append(warnings, "provider_cache_hit_ratio_input_decreased")
	}
	if report.Candidate.ProviderCacheHitRatioTotalInput-report.Baseline.ProviderCacheHitRatioTotalInput >= 0.01 {
		signals = append(signals, "provider_cache_hit_ratio_total_input_increased")
	} else if report.Candidate.ProviderCacheHitRatioTotalInput-report.Baseline.ProviderCacheHitRatioTotalInput <= -0.01 {
		warnings = append(warnings, "provider_cache_hit_ratio_total_input_decreased")
	}
	if report.Baseline.SkillsCatalogPosition != nil && report.Candidate.SkillsCatalogPosition != nil {
		switch {
		case *report.Candidate.SkillsCatalogPosition < *report.Baseline.SkillsCatalogPosition:
			signals = append(signals, "skills_catalog_moved_earlier")
		case *report.Candidate.SkillsCatalogPosition > *report.Baseline.SkillsCatalogPosition:
			warnings = append(warnings, "skills_catalog_moved_later")
		}
	}
	if report.Baseline.SkillsCatalogHash != "" && report.Candidate.SkillsCatalogHash != "" && report.Baseline.SkillsCatalogHash != report.Candidate.SkillsCatalogHash {
		warnings = append(warnings, "skills_catalog_hash_changed")
	}
	if report.Baseline.ProviderCacheUsageState != "reported" || report.Candidate.ProviderCacheUsageState != "reported" {
		warnings = append(warnings, "provider_cache_usage_not_reported_for_both_runs")
	}
	if !report.Candidate.SystemHashStable {
		warnings = append(warnings, "candidate_system_hash_unstable")
	}
	if !report.Candidate.CacheControlHashStable {
		warnings = append(warnings, "candidate_cache_control_hash_unstable")
	}
	if !report.Candidate.ToolsHashStable {
		warnings = append(warnings, "candidate_tools_hash_unstable")
	}
	if !report.Candidate.ThinkingHashStable {
		warnings = append(warnings, "candidate_thinking_hash_unstable")
	}
	if report.Baseline.Records != report.Candidate.Records {
		warnings = append(warnings, "record_count_differs")
	}
	return signals, warnings
}
