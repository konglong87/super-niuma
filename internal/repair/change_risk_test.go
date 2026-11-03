package repair

import "testing"

func TestAnalyzeReplacementDetectsDefinitionDeletion(t *testing.T) {
	oldText := "BUNDLE_ROOT=\"${NIUMA_SKILL_DIR:?}\"\nprintf '%s' \"$BUNDLE_ROOT\"\n"
	newText := "printf '%s' \"$BUNDLE_ROOT\"\n"
	deleted := AnalyzeReplacement(oldText, newText)
	if len(deleted) != 1 || deleted[0].Name != "BUNDLE_ROOT" {
		t.Fatalf("deleted definitions = %+v", deleted)
	}
	if got := AnalyzeReplacement(oldText, oldText); len(got) != 0 {
		t.Fatalf("unchanged definition reported as deletion: %+v", got)
	}
}
