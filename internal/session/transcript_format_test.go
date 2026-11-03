package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadWithFormatDetectsGolangCCV1(t *testing.T) {
	path := writeTranscript(t,
		`{"type":"message","role":"user","content":"hello"}`,
		`{"type":"tool_call","tool_id":"toolu_1","tool_name":"Read","content":"{\"file_path\":\"README.md\"}"}`,
		`{"type":"content_replacement","replacements":[{"kind":"tool-result","tool_use_id":"toolu_1","replacement":"<persisted-output>preview</persisted-output>"}]}`,
		`{"type":"prompt_context","role":"system","content":"Prompt context manifest","metadata":{"mode":"code"}}`,
	)
	entries, format, err := LoadWithFormat(path)
	if err != nil {
		t.Fatal(err)
	}
	if format != TranscriptFormatGolangCCV1 || len(entries) != 4 {
		t.Fatalf("format=%s entries=%+v", format, entries)
	}
	if err := ValidateResumeFormat(format); err != nil {
		t.Fatalf("ValidateResumeFormat(%s) = %v", format, err)
	}
}

func TestLoadWithFormatDetectsGolangCCV2(t *testing.T) {
	path := writeTranscript(t,
		`{"type":"session_meta","schema":"niuma.transcript.v2","app":"niuma","schema_version":2,"session_id":"s"}`,
		`{"type":"message","schema":"niuma.transcript.v2","role":"user","content":[{"type":"text","text":"hello"}]}`,
	)
	_, format, err := LoadWithFormat(path)
	if err != nil {
		t.Fatal(err)
	}
	if format != TranscriptFormatGolangCCV2 {
		t.Fatalf("format = %s", format)
	}
	// v2 is now resumable via leaf-walk (phase B).
	if err := ValidateResumeFormat(format); err != nil {
		t.Fatalf("ValidateResumeFormat(%s) = %v", format, err)
	}
}

func TestLoadWithFormatReadsLegacyTranscriptSchema(t *testing.T) {
	path := writeTranscript(t,
		`{"type":"session_meta","schema":"niuma-legacy.transcript.v2","app":"niuma-legacy","schema_version":2,"session_id":"legacy"}`,
		`{"type":"message","schema":"niuma-legacy.transcript.v2","role":"user","content":"hello"}`,
	)
	_, format, err := LoadWithFormat(path)
	if err != nil {
		t.Fatal(err)
	}
	if format != TranscriptFormatGolangCCV2 {
		t.Fatalf("legacy format = %s, want %s", format, TranscriptFormatGolangCCV2)
	}
}

func TestLoadWithFormatDetectsClaudeCodeNative(t *testing.T) {
	path := writeTranscript(t,
		`{"type":"user","uuid":"user-1","message":{"role":"user","content":"hello"}}`,
		`{"type":"assistant","uuid":"assistant-1","parentUuid":"user-1","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}`,
	)
	_, format, err := LoadWithFormat(path)
	if err != nil {
		t.Fatal(err)
	}
	if format != TranscriptFormatClaudeCodeNative {
		t.Fatalf("format = %s", format)
	}
	if err := ValidateResumeFormat(format); err == nil || !strings.Contains(err.Error(), "Trace Viewer can inspect it read-only") {
		t.Fatalf("ValidateResumeFormat(%s) = %v", format, err)
	}
}

func TestLoadWithFormatDetectsUnknownMixedAndEmpty(t *testing.T) {
	unknownPath := writeTranscript(t, `{"type":"external_event","content":"hello"}`)
	_, format, err := LoadWithFormat(unknownPath)
	if err != nil {
		t.Fatal(err)
	}
	if format != TranscriptFormatUnknown {
		t.Fatalf("unknown format = %s", format)
	}
	if err := ValidateResumeFormat(format); err == nil || !strings.Contains(err.Error(), "unsupported transcript schema") {
		t.Fatalf("ValidateResumeFormat(%s) = %v", format, err)
	}

	mixedPath := writeTranscript(t,
		`{"type":"message","role":"user","content":"hello"}`,
		`{"type":"user","uuid":"user-1","message":{"role":"user","content":"native"}}`,
	)
	_, format, err = LoadWithFormat(mixedPath)
	if err != nil {
		t.Fatal(err)
	}
	if format != TranscriptFormatMixed {
		t.Fatalf("mixed format = %s", format)
	}

	emptyPath := writeTranscript(t)
	entries, format, err := LoadWithFormat(emptyPath)
	if err != nil {
		t.Fatal(err)
	}
	if format != TranscriptFormatEmpty || len(entries) != 0 {
		t.Fatalf("empty format=%s entries=%+v", format, entries)
	}
	if err := ValidateResumeFormat(format); err != nil {
		t.Fatalf("ValidateResumeFormat(%s) = %v", format, err)
	}
}

func writeTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	content := strings.Join(lines, "\n")
	if len(lines) > 0 {
		content += "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
