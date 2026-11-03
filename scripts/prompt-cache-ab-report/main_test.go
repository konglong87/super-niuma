package main

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildABReportComparesProviderUsageAndSkillsPosition(t *testing.T) {
	dir := t.TempDir()
	baselinePath := filepath.Join(dir, "baseline.json")
	candidatePath := filepath.Join(dir, "candidate.json")
	writeFile(t, baselinePath, `{
  "sessions": [{
    "session_id": "baseline-session",
    "records": 3,
    "provider_cache_usage_state": "reported",
    "provider_cache_hit_ratio_input": 0.40,
    "provider_cache_hit_ratio_total_input": 0.25,
    "system_hash_stable": true,
    "cache_control_hash_stable": true,
    "tools_hash_stable": true,
    "message_prefix_hash_stable": true,
    "thinking_hash_stable": true,
    "cacheable_system_bytes_max": 9000,
    "uncached_system_bytes_max": 10000,
    "skills_catalog_position": 3,
    "skills_catalog_hash": "skills-hash",
    "skills_catalog_bytes": 8128,
    "largest_uncached_system_blocks": [{"index":3,"source":"skills_catalog","text_bytes":8128,"stable":true}],
    "transcript_usage": {"session_id":"baseline-session","usage_entries":3,"input_tokens":1000,"cache_read_input_tokens":400,"cache_creation_input_tokens":0}
  }]
}`)
	writeFile(t, candidatePath, `{
  "sessions": [{
    "session_id": "candidate-session",
    "records": 3,
    "provider_cache_usage_state": "reported",
    "provider_cache_hit_ratio_input": 0.55,
    "provider_cache_hit_ratio_total_input": 0.32,
    "system_hash_stable": true,
    "cache_control_hash_stable": true,
    "tools_hash_stable": true,
    "message_prefix_hash_stable": true,
    "thinking_hash_stable": true,
    "cacheable_system_bytes_max": 9000,
    "uncached_system_bytes_max": 10000,
    "skills_catalog_position": 2,
    "skills_catalog_hash": "skills-hash",
    "skills_catalog_bytes": 8128,
    "largest_uncached_system_blocks": [{"index":2,"source":"skills_catalog","text_bytes":8128,"stable":true}],
    "transcript_usage": {"session_id":"candidate-session","usage_entries":3,"input_tokens":980,"cache_read_input_tokens":539,"cache_creation_input_tokens":0}
  }]
}`)

	report, err := buildABReport(baselinePath, candidatePath, "", "", "current", "stable-prefix")
	if err != nil {
		t.Fatal(err)
	}
	if report.Baseline.Label != "current" || report.Candidate.Label != "stable-prefix" {
		t.Fatalf("labels = %q %q", report.Baseline.Label, report.Candidate.Label)
	}
	if math.Abs(report.Delta.ProviderCacheHitRatioInput-0.15) > 0.000001 {
		t.Fatalf("ratio delta = %v", report.Delta.ProviderCacheHitRatioInput)
	}
	if report.Delta.SkillsCatalogPosition == nil || *report.Delta.SkillsCatalogPosition != -1 {
		t.Fatalf("skills position delta = %v", report.Delta.SkillsCatalogPosition)
	}
	if !contains(report.Signals, "provider_cache_hit_ratio_input_increased") || !contains(report.Signals, "skills_catalog_moved_earlier") {
		t.Fatalf("signals = %+v", report.Signals)
	}
	if len(report.Warnings) != 0 {
		t.Fatalf("warnings = %+v", report.Warnings)
	}
}

func TestReadDiagnosticSessionRequiresSessionWhenAmbiguous(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "summary.json")
	writeFile(t, path, `{"sessions":[{"session_id":"a"},{"session_id":"b"}]}`)
	if _, err := readDiagnosticSession(path, ""); err == nil {
		t.Fatal("expected ambiguous summary error")
	}
	session, err := readDiagnosticSession(path, "b")
	if err != nil {
		t.Fatal(err)
	}
	if session.SessionID != "b" {
		t.Fatalf("session = %q", session.SessionID)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
