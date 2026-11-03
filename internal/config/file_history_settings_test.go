package config

import "testing"

func TestResolvedFileHistoryDefaults(t *testing.T) {
	var s Settings
	mt, ma, mb := s.ResolvedFileHistory()
	if mt != DefaultFileHistoryMaxTurns || ma != DefaultFileHistoryMaxAgeDays || mb != DefaultFileHistoryMaxBytes {
		t.Fatalf("unset settings must yield defaults, got turns=%d age=%d bytes=%d", mt, ma, mb)
	}
}

func TestResolvedFileHistoryOverridesOnlySetFields(t *testing.T) {
	turns := 5
	s := Settings{FileHistory: &FileHistorySettings{MaxTurns: &turns}}
	mt, ma, mb := s.ResolvedFileHistory()
	if mt != 5 {
		t.Fatalf("maxTurns override = %d, want 5", mt)
	}
	// Unset fields keep defaults.
	if ma != DefaultFileHistoryMaxAgeDays || mb != DefaultFileHistoryMaxBytes {
		t.Fatalf("unset fields must keep defaults, got age=%d bytes=%d", ma, mb)
	}
}

func TestMergeFileHistorySettingsOverrideWins(t *testing.T) {
	baseTurns, overrideTurns := 10, 3
	base := &FileHistorySettings{MaxTurns: &baseTurns}
	override := &FileHistorySettings{MaxTurns: &overrideTurns}
	merged := mergeFileHistorySettings(base, override)
	if merged == nil || merged.MaxTurns == nil || *merged.MaxTurns != 3 {
		t.Fatalf("override maxTurns must win, got %+v", merged)
	}
}
