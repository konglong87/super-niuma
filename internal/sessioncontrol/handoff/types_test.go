package handoff

import (
	"encoding/json"
	"sort"
	"testing"
	"time"
)

func TestPackageValidateAcceptsValidV1Package(t *testing.T) {
	pkg := validPackage(t)

	if err := pkg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestPackageJSONUsesExactV1FieldWhitelist(t *testing.T) {
	pkg := validPackage(t)

	encoded, err := json.Marshal(pkg)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	fields := assertJSONFields(t, encoded, []string{
		"schema", "package_id", "package_sha256", "source", "target", "objective",
		"constraints", "stage_summary", "completed", "open_items", "risks", "next_actions",
		"evidence", "budget",
	})
	assertJSONFields(t, fields["source"], []string{"ref", "cursor", "content_sha256", "captured_at"})
	assertJSONFields(t, fields["target"], []string{"ref"})
	assertJSONFields(t, fields["budget"], []string{"estimated_tokens", "limit_tokens"})

	var evidence []json.RawMessage
	if err := json.Unmarshal(fields["evidence"], &evidence); err != nil {
		t.Fatalf("Unmarshal(evidence) error = %v", err)
	}
	if len(evidence) != 1 {
		t.Fatalf("evidence count = %d, want 1", len(evidence))
	}
	evidenceFields := assertJSONFields(t, evidence[0], []string{"ref", "claim", "verification", "sha256", "verifier"})
	assertJSONFields(t, evidenceFields["verifier"], []string{"kind", "ref", "sha256"})
}

func TestPackageValidateRejectsInvalidV1Contract(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Package)
		want   ErrorCode
	}{
		{
			name: "unsupported schema",
			mutate: func(pkg *Package) {
				pkg.Schema = "niuma.session-handoff.v2"
			},
			want: CodeUnsupportedSchema,
		},
		{
			name: "missing source ref",
			mutate: func(pkg *Package) {
				pkg.Source.Ref = ""
			},
			want: CodeInvalidRef,
		},
		{
			name: "missing target ref",
			mutate: func(pkg *Package) {
				pkg.Target.Ref = ""
			},
			want: CodeInvalidRef,
		},
		{
			name: "invalid cursor",
			mutate: func(pkg *Package) {
				pkg.Source.Cursor = "timestamp:2026-09-04T00:00:00Z"
			},
			want: CodeInvalidCursor,
		},
		{
			name: "malformed source hash",
			mutate: func(pkg *Package) {
				pkg.Source.ContentSHA256 = "not-a-sha256"
			},
			want: CodeInvalidHash,
		},
		{
			name: "duplicate evidence locator",
			mutate: func(pkg *Package) {
				pkg.Evidence = append(pkg.Evidence, pkg.Evidence[0])
			},
			want: CodeDuplicateLocator,
		},
		{
			name: "verified evidence without verifier",
			mutate: func(pkg *Package) {
				pkg.Evidence[0].Verification = VerificationVerified
				pkg.Evidence[0].Verifier = Verifier{}
			},
			want: CodeInvalidEvidence,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkg := validPackage(t)
			tt.mutate(&pkg)

			err := pkg.Validate()
			if err == nil {
				t.Fatal("Validate() error = nil, want error")
			}
			if got := ErrorCodeOf(err); got != tt.want {
				t.Fatalf("ErrorCodeOf() = %q, want %q (error: %v)", got, tt.want, err)
			}
		})
	}
}

func TestPackageValidatesCanonicalCursorBySourceKind(t *testing.T) {
	tests := []struct {
		name      string
		sourceRef string
		cursor    string
		wantErr   bool
	}{
		{name: "tenant task event", sourceRef: "tenant:source-session", cursor: "task_event:1"},
		{name: "tenant message", sourceRef: "tenant:source-session", cursor: "message:27"},
		{name: "local opaque entry", sourceRef: "local:source-session", cursor: "entry:01HZ_abc-9.def"},
		{name: "local positive line", sourceRef: "local:source-session", cursor: "line:1"},
		{name: "tenant zero", sourceRef: "tenant:source-session", cursor: "task_event:0", wantErr: true},
		{name: "tenant leading zero", sourceRef: "tenant:source-session", cursor: "task_event:0001", wantErr: true},
		{name: "local line zero", sourceRef: "local:source-session", cursor: "line:0", wantErr: true},
		{name: "local line leading zero", sourceRef: "local:source-session", cursor: "line:01", wantErr: true},
		{name: "tenant rejects local prefix", sourceRef: "tenant:source-session", cursor: "entry:opaque", wantErr: true},
		{name: "local rejects tenant prefix", sourceRef: "local:source-session", cursor: "task_event:1", wantErr: true},
		{name: "tenant rejects tool trace prefix", sourceRef: "tenant:source-session", cursor: "tool_trace:1", wantErr: true},
		{name: "entry rejects whitespace", sourceRef: "local:source-session", cursor: "entry:not safe", wantErr: true},
		{name: "entry rejects slash", sourceRef: "local:source-session", cursor: "entry:not/safe", wantErr: true},
		{name: "entry rejects colon", sourceRef: "local:source-session", cursor: "entry:not:safe", wantErr: true},
		{name: "entry rejects control", sourceRef: "local:source-session", cursor: "entry:not\nsafe", wantErr: true},
		{name: "entry rejects unicode", sourceRef: "local:source-session", cursor: "entry:\u4f1a\u8bdd", wantErr: true},
		{name: "entry rejects empty ID", sourceRef: "local:source-session", cursor: "entry:", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := validPackageInput()
			input.Source.Ref = tt.sourceRef
			input.Source.Cursor = tt.cursor
			if tt.sourceRef == "local:source-session" {
				input.Evidence[0].Ref = "local:source-session#line:1"
			}

			pkg, err := BuildPackage(input)
			if tt.wantErr {
				if got := ErrorCodeOf(err); got != CodeInvalidCursor {
					t.Fatalf("ErrorCodeOf(BuildPackage()) = %q, want %q (error: %v)", got, CodeInvalidCursor, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("BuildPackage() error = %v", err)
			}
			wantPackageID := PackageIDPrefix + tt.sourceRef + ":" + tt.cursor + ":" + pkg.Source.ContentSHA256
			if pkg.PackageID != wantPackageID {
				t.Fatalf("PackageID = %q, want canonical %q", pkg.PackageID, wantPackageID)
			}
		})
	}
}

func TestPackageRejectsEvidenceOutsideStrictSourceLocatorGrammar(t *testing.T) {
	tests := []struct {
		name string
		ref  string
	}{
		{name: "arbitrary tenant suffix", ref: "tenant:source-session#task:1"},
		{name: "zero task event", ref: "tenant:source-session#task_event:0"},
		{name: "negative message", ref: "tenant:source-session#message:-1"},
		{name: "zero tool trace event", ref: "tenant:source-session#tool_trace:0:0"},
		{name: "negative tool trace ordinal", ref: "tenant:source-session#tool_trace:1:-1"},
		{name: "extra tenant locator segment", ref: "tenant:source-session#task_event:1:2"},
		{name: "control character", ref: "tenant:source-session#message:1\n"},
		{name: "foreign tenant source", ref: "tenant:other-session#message:1"},
		{name: "local locator for tenant source", ref: "local:source-session#entry:abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := validPackageInput()
			input.Evidence[0].Ref = tt.ref

			_, err := BuildPackage(input)
			if got := ErrorCodeOf(err); got != CodeInvalidEvidence {
				t.Fatalf("ErrorCodeOf(BuildPackage()) = %q, want %q (error: %v)", got, CodeInvalidEvidence, err)
			}
		})
	}
}

func TestPackageAcceptsStrictTenantEvidenceLocatorGrammar(t *testing.T) {
	for _, ref := range []string{
		"tenant:source-session#task_event:1",
		"tenant:source-session#message:2",
		"tenant:source-session#tool_trace:3:0",
		"tenant:source-session#tool_trace:3:17",
	} {
		t.Run(ref, func(t *testing.T) {
			input := validPackageInput()
			input.Evidence[0].Ref = ref
			if _, err := BuildPackage(input); err != nil {
				t.Fatalf("BuildPackage() error = %v", err)
			}
		})
	}
}

func TestPackageValidatesLocalEvidenceLocatorGrammar(t *testing.T) {
	tests := []struct {
		name    string
		locator string
		wantErr bool
	}{
		{name: "safe entry ID", locator: "entry:01HZ_abc-9.def"},
		{name: "positive line", locator: "line:1"},
		{name: "zero line", locator: "line:0", wantErr: true},
		{name: "unsafe entry traversal", locator: "entry:../secret", wantErr: true},
		{name: "empty entry", locator: "entry:", wantErr: true},
		{name: "tenant locator", locator: "message:1", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := validPackageInput()
			input.Source.Ref = "local:source-session"
			input.Source.Cursor = "line:7"
			input.Evidence[0].Ref = "local:source-session#" + tt.locator

			_, err := BuildPackage(input)
			if tt.wantErr && ErrorCodeOf(err) != CodeInvalidEvidence {
				t.Fatalf("ErrorCodeOf(BuildPackage()) = %q, want %q (error: %v)", ErrorCodeOf(err), CodeInvalidEvidence, err)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("BuildPackage() error = %v", err)
			}
		})
	}
}

func TestPackageAppliesStrictLocatorGrammarToVerifier(t *testing.T) {
	for _, ref := range []string{
		"tenant:other-session#task_event:9",
		"tenant:source-session#arbitrary:9",
		"tenant:source-session#tool_trace:9:-1",
		"tenant:source-session#message:9\n",
	} {
		t.Run(ref, func(t *testing.T) {
			input := validPackageInput()
			input.Evidence[0].Verification = VerificationVerified
			input.Evidence[0].Verifier = Verifier{
				Kind:   VerifierTest,
				Ref:    ref,
				SHA256: testSHA256,
			}

			_, err := BuildPackage(input)
			if got := ErrorCodeOf(err); got != CodeInvalidEvidence {
				t.Fatalf("ErrorCodeOf(BuildPackage()) = %q, want %q (error: %v)", got, CodeInvalidEvidence, err)
			}
		})
	}
}

func validPackage(t *testing.T) Package {
	t.Helper()
	pkg, err := BuildPackage(validPackageInput())
	if err != nil {
		t.Fatalf("BuildPackage() error = %v", err)
	}
	return pkg
}

func validPackageInput() PackageInput {
	return PackageInput{
		Source: Source{
			Ref:        "tenant:source-session",
			Cursor:     "task_event:189",
			CapturedAt: time.Date(2026, time.September, 4, 8, 30, 0, 0, time.UTC),
		},
		Target:       Target{Ref: "tenant:target-session"},
		Objective:    "Deliver the reviewed change.",
		Constraints:  []string{"Do not copy a transcript."},
		StageSummary: "Implementation is ready for validation.",
		Completed:    []string{"Contract drafted."},
		OpenItems:    []string{"Run focused tests."},
		Risks:        []string{"Source can become stale."},
		NextActions:  []string{"Validate the package."},
		Evidence: []Evidence{{
			Ref:          "tenant:source-session#task_event:188",
			Claim:        "The contract was drafted.",
			Verification: VerificationReported,
			SHA256:       testSHA256,
		}},
		Budget: Budget{EstimatedTokens: 10, LimitTokens: DefaultPackageTokenLimit},
	}
}

func assertJSONFields(t *testing.T, encoded json.RawMessage, want []string) map[string]json.RawMessage {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("Unmarshal(%s) error = %v", encoded, err)
	}
	if len(fields) != len(want) {
		t.Fatalf("JSON fields = %v, want exactly %v", fieldNames(fields), want)
	}
	for _, name := range want {
		if _, ok := fields[name]; !ok {
			t.Errorf("JSON missing approved field %q; got %v", name, fieldNames(fields))
		}
	}
	return fields
}

func fieldNames(fields map[string]json.RawMessage) []string {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

const testSHA256 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
