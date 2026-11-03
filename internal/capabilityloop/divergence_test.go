package capabilityloop

import (
	"strings"
	"testing"
)

// TODO-092 decision (b): every <capability_loop> pair becomes a candidate, not
// just the first one. Consumers take the first candidate that *parses*, so a
// malformed leading pair must not discard a valid later payload.
func TestJSONCandidatesTakesEveryTagPair(t *testing.T) {
	content := strings.Join([]string{
		"<capability_loop>",
		`{"capability_loop":{"evidence":[`, // deliberately truncated / unparseable
		"</capability_loop>",
		"noise between payloads",
		"<capability_loop>",
		`{"capability_loop":{"evidence":["MULTI_TAG_EVIDENCE: second pair is the valid one."]}}`,
		"</capability_loop>",
	}, "\n")

	loop, _, ok := FromToolResult(content)
	if !ok {
		t.Fatalf("second tag pair should parse, got ok=false")
	}
	if len(loop.Evidence) != 1 || !strings.Contains(loop.Evidence[0], "MULTI_TAG_EVIDENCE") {
		t.Fatalf("unexpected evidence: %#v", loop.Evidence)
	}
}

// TODO-092 decision (a): tag matching is ASCII-case-insensitive, so a sub-agent
// that capitalizes the tag still hands its evidence over.
func TestJSONCandidatesMatchesTagsCaseInsensitively(t *testing.T) {
	content := strings.Join([]string{
		"prose before the payload",
		"<Capability_Loop>",
		`{"capability_loop":{"evidence":["MIXED_CASE_TAG_EVIDENCE: capitalized tag still parses."]}}`,
		"</CAPABILITY_LOOP>",
	}, "\n")

	loop, _, ok := FromToolResult(content)
	if !ok {
		t.Fatalf("mixed-case tag should parse, got ok=false")
	}
	if len(loop.Evidence) != 1 || !strings.Contains(loop.Evidence[0], "MIXED_CASE_TAG_EVIDENCE") {
		t.Fatalf("unexpected evidence: %#v", loop.Evidence)
	}
}

// TODO-092 decision (a), implementation half: the offsets returned for the tag
// body must index into the original content. A ToLower-then-Index pair drifts as
// soon as the content holds a rune whose lowercase form has a different byte
// length ("İ" lowercases to two runes), which slices the payload apart.
// The trailing "{...}" denies the outermost-brace fallback, so the tag body is
// the only candidate that can carry the payload.
func TestJSONCandidatesSurvivesWidthChangingLowercase(t *testing.T) {
	content := "İİİ prose header\n<capability_loop>\n" +
		`{"capability_loop":{"evidence":["WIDTH_SHIFT_EVIDENCE: offsets stayed on the original bytes."]}}` +
		"\n</capability_loop>\ntrailing prose {\"unrelated\": 1}"

	loop, _, ok := FromToolResult(content)
	if !ok {
		t.Fatalf("payload after a width-changing rune should parse, got ok=false")
	}
	if len(loop.Evidence) != 1 || !strings.Contains(loop.Evidence[0], "WIDTH_SHIFT_EVIDENCE") {
		t.Fatalf("unexpected evidence: %#v", loop.Evidence)
	}
}

// TODO-092 decision (d): the un-tagged fallback must cover both a JSON document
// root (including an array root, which the outermost-brace slice mangles) and
// JSON embedded in prose (which the whole-content attempt cannot parse).
func TestJSONCandidatesFallbackCoversArrayRootAndEmbeddedJSON(t *testing.T) {
	arrayRoot := `[{"capability_loop":{"evidence":["ARRAY_ROOT_EVIDENCE: array root still parses."]}}]`
	loop, _, ok := FromToolResult(arrayRoot)
	if !ok {
		t.Fatalf("array-root payload should parse, got ok=false")
	}
	if len(loop.Evidence) != 1 || !strings.Contains(loop.Evidence[0], "ARRAY_ROOT_EVIDENCE") {
		t.Fatalf("unexpected array-root evidence: %#v", loop.Evidence)
	}

	embedded := `sub-agent said: {"capability_loop":{"evidence":["EMBEDDED_EVIDENCE: prose-wrapped JSON still parses."]}} -- end`
	loop, _, ok = FromToolResult(embedded)
	if !ok {
		t.Fatalf("prose-embedded payload should parse, got ok=false")
	}
	if len(loop.Evidence) != 1 || !strings.Contains(loop.Evidence[0], "EMBEDDED_EVIDENCE") {
		t.Fatalf("unexpected embedded evidence: %#v", loop.Evidence)
	}
}

// TODO-094: the persisted-summary reader must recover the supersede relation the
// follow-up gate needs, including the plural key and the comma-joined form that
// TaskHint itself emits. Without this, an already-resolved follow-up silently
// goes back to pending.
func TestPersistedSummaryRecoversSupersedeRelation(t *testing.T) {
	content := strings.Join([]string{
		"<persisted-output>",
		"Output too large (999999 bytes). Full output saved to: /tmp/x.txt",
		"",
		"Capability loop summary preserved from full output:",
		"- evidence: SUMMARY_EVIDENCE: inspected the superseded result.",
		"- verification: SUMMARY_VERIFICATION: focused acceptance passed.",
		"- resolved_follow_up: SUMMARY_RESOLVED: old next_action handled.",
		"- supersedes_evidence_id: tool:toolu_first,tool:toolu_second",
		"- supersedes_evidence_ids: tool:toolu_third",
		"",
		"Preview (first 2000 bytes):",
		"</persisted-output>",
	}, "\n")

	loop, _, ok := FromToolResult(content)
	if !ok {
		t.Fatalf("persisted summary should parse, got ok=false")
	}
	if loop.ResolvedFollowUp != "SUMMARY_RESOLVED: old next_action handled." {
		t.Fatalf("resolved_follow_up not recovered: %q", loop.ResolvedFollowUp)
	}
	ids := agentCapabilitySupersededEvidenceIDs(loop)
	want := []string{"tool:toolu_first", "tool:toolu_second", "tool:toolu_third"}
	if len(ids) != len(want) {
		t.Fatalf("superseded ids = %#v, want %#v", ids, want)
	}
	for i, id := range want {
		if ids[i] != id {
			t.Fatalf("superseded ids = %#v, want %#v", ids, want)
		}
	}
	if !agentCapabilityResolutionSignal(loop) {
		t.Fatalf("recovered summary should carry a resolution signal: %#v", loop)
	}
}
