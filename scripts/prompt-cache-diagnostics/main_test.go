package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/promptdump"
)

func TestBuildDiagnosticOutputAppliesTranscriptUsage(t *testing.T) {
	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "session-a.jsonl")
	transcript := `{"type":"message","role":"user","content":"hello"}
{"type":"usage","model":"glm-5.1","input_tokens":100,"cache_creation_input_tokens":20,"cache_read_input_tokens":30,"output_tokens":5}
`
	if err := os.WriteFile(transcriptPath, []byte(transcript), 0600); err != nil {
		t.Fatal(err)
	}
	report := promptdump.CacheDiagnosticsReport{
		Sessions: []promptdump.CacheSessionDiagnostics{{
			SessionID:               "session-a",
			ProviderCacheUsageState: promptdump.ProviderCacheUsageUnavailable,
		}},
		Records: []promptdump.CacheRecordDiagnostics{{
			SessionID:               "session-a",
			SkillsCatalogPosition:   intPtr(3),
			SkillsCatalogHash:       "skills-hash",
			SkillsCatalogBytes:      8128,
			PrefixBeforeSkillsHash:  "before-hash",
			PrefixThroughSkillsHash: "through-hash",
		}},
	}
	out, err := buildDiagnosticOutput(report, []string{transcriptPath})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(out.Sessions))
	}
	got := out.Sessions[0]
	if got.ProviderCacheUsageState != promptdump.ProviderCacheUsageReported {
		t.Fatalf("provider state = %q, want reported", got.ProviderCacheUsageState)
	}
	if got.TranscriptUsage == nil || got.TranscriptUsage.InputTokens != 100 || got.TranscriptUsage.CacheCreationInputTokens != 20 || got.TranscriptUsage.CacheReadInputTokens != 30 {
		t.Fatalf("transcript usage = %+v", got.TranscriptUsage)
	}
	if got.ProviderCacheHitRatioInput != 0.3 {
		t.Fatalf("ratio input = %v, want 0.3", got.ProviderCacheHitRatioInput)
	}
	if got.ProviderCacheHitRatioTotalInput != 0.2 {
		t.Fatalf("ratio total input = %v, want 0.2", got.ProviderCacheHitRatioTotalInput)
	}
	if got.SkillsCatalogPosition == nil || *got.SkillsCatalogPosition != 3 || got.SkillsCatalogHash != "skills-hash" || got.SkillsCatalogBytes != 8128 {
		t.Fatalf("skills diagnostics = position %v hash %q bytes %d", got.SkillsCatalogPosition, got.SkillsCatalogHash, got.SkillsCatalogBytes)
	}
}

func TestBuildDiagnosticOutputMarksTranscriptUsageNotReported(t *testing.T) {
	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "session-a.jsonl")
	transcript := `{"type":"usage","model":"glm-5.1","input_tokens":100,"output_tokens":5}
`
	if err := os.WriteFile(transcriptPath, []byte(transcript), 0600); err != nil {
		t.Fatal(err)
	}
	report := promptdump.CacheDiagnosticsReport{
		Sessions: []promptdump.CacheSessionDiagnostics{{
			SessionID:               "session-a",
			ProviderCacheUsageState: promptdump.ProviderCacheUsageUnavailable,
		}},
	}
	out, err := buildDiagnosticOutput(report, []string{transcriptPath})
	if err != nil {
		t.Fatal(err)
	}
	if got := out.Sessions[0].ProviderCacheUsageState; got != promptdump.ProviderCacheUsageNotReported {
		t.Fatalf("provider state = %q, want not_reported", got)
	}
}

func TestFilterDiagnosticOutputBySession(t *testing.T) {
	out := diagnosticOutput{
		Sessions: []diagnosticSessionOutput{
			{CacheSessionDiagnostics: promptdump.CacheSessionDiagnostics{SessionID: "session-a"}},
			{CacheSessionDiagnostics: promptdump.CacheSessionDiagnostics{SessionID: "session-b"}},
		},
		Records: []promptdump.CacheRecordDiagnostics{
			{SessionID: "session-a", Turn: 1},
			{SessionID: "session-b", Turn: 1},
			{SessionID: "session-a", Turn: 2},
		},
	}
	filtered := filterDiagnosticOutput(out, "session-a")
	if len(filtered.Sessions) != 1 || filtered.Sessions[0].SessionID != "session-a" {
		t.Fatalf("sessions = %+v", filtered.Sessions)
	}
	if len(filtered.Records) != 2 || filtered.Records[0].SessionID != "session-a" || filtered.Records[1].Turn != 2 {
		t.Fatalf("records = %+v", filtered.Records)
	}
}

func TestDiagnosticOutputOmitsRecordsWhenSummaryOnlyClearsRecords(t *testing.T) {
	out := diagnosticOutput{
		Sessions: []diagnosticSessionOutput{{CacheSessionDiagnostics: promptdump.CacheSessionDiagnostics{SessionID: "session-a"}}},
		Records:  nil,
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `],"records"`) {
		t.Fatalf("summary output should omit records: %s", encoded)
	}
}

func intPtr(v int) *int {
	return &v
}
