package channel

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestToolCommandPreviewExtractsPrimaryInput(t *testing.T) {
	got := ToolCommandPreview("Bash", json.RawMessage(`{"command":"go test ./...","env":"ignored"}`))
	if got != "go test ./..." {
		t.Fatalf("command preview = %q", got)
	}
}

func TestToolOutputPreviewRedactsAndBounds(t *testing.T) {
	raw := "token=super-secret " + strings.Repeat("x", ToolOutputPreviewLimit+40)
	got, truncated := ToolOutputPreview(raw, false)
	if !truncated || strings.Contains(got, "super-secret") || !strings.Contains(got, "...") {
		t.Fatalf("preview=%q truncated=%v", got, truncated)
	}
}

func TestParseToolDetailsModeDefaultsToPreviewAndRejectsUnknown(t *testing.T) {
	if got, err := ParseToolDetailsMode(""); err != nil || got != ToolDetailsPreview {
		t.Fatalf("default mode=%q err=%v", got, err)
	}
	if got, err := ParseToolDetailsMode("off"); err != nil || got != ToolDetailsOff {
		t.Fatalf("off mode=%q err=%v", got, err)
	}
	if _, err := ParseToolDetailsMode("full"); err == nil {
		t.Fatal("unknown mode should fail")
	}
}
