package config

import (
	"strconv"
	"testing"
)

// mergeNextStepsSettings shipped with the NextSteps field silently missing
// from mergeSettings once already (settings.json's nextSteps block was a
// no-op until that got fixed). Its only current guard is a server-package
// test that happens to exercise LoadSettings end-to-end -- a refactor there
// could silently drop coverage. This table test pins the merge function
// itself, next to the other mergeXSettings tests.
func TestMergeNextStepsSettings(t *testing.T) {
	trueVal := true
	falseVal := false
	baseCount := 3
	overrideCount := 5

	cases := []struct {
		name       string
		base       *NextStepsSettings
		override   *NextStepsSettings
		wantNil    bool
		wantEnable *bool
		wantModel  string
		wantCount  *int
	}{
		{
			name:       "override nil returns base unchanged",
			base:       &NextStepsSettings{Enabled: &trueVal, Model: "base-model", Count: &baseCount},
			override:   nil,
			wantEnable: &trueVal,
			wantModel:  "base-model",
			wantCount:  &baseCount,
		},
		{
			name:     "both nil returns nil",
			base:     nil,
			override: nil,
			wantNil:  true,
		},
		{
			name:       "base nil initializes from override",
			base:       nil,
			override:   &NextStepsSettings{Enabled: &falseVal, Model: "override-model", Count: &overrideCount},
			wantEnable: &falseVal,
			wantModel:  "override-model",
			wantCount:  &overrideCount,
		},
		{
			name:       "absent override Enabled must not clobber a set base",
			base:       &NextStepsSettings{Enabled: &trueVal},
			override:   &NextStepsSettings{},
			wantEnable: &trueVal,
		},
		{
			name:       "override Enabled wins when explicitly set",
			base:       &NextStepsSettings{Enabled: &trueVal},
			override:   &NextStepsSettings{Enabled: &falseVal},
			wantEnable: &falseVal,
		},
		{
			name:      "override Model wins when set",
			base:      &NextStepsSettings{Model: "base-model"},
			override:  &NextStepsSettings{Model: "override-model"},
			wantModel: "override-model",
		},
		{
			name:      "empty override Model does not clobber base Model",
			base:      &NextStepsSettings{Model: "base-model"},
			override:  &NextStepsSettings{},
			wantModel: "base-model",
		},
		{
			name:      "override Count wins when set",
			base:      &NextStepsSettings{Count: &baseCount},
			override:  &NextStepsSettings{Count: &overrideCount},
			wantCount: &overrideCount,
		},
		{
			name:      "nil override Count does not clobber base Count",
			base:      &NextStepsSettings{Count: &baseCount},
			override:  &NextStepsSettings{},
			wantCount: &baseCount,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			merged := mergeNextStepsSettings(tc.base, tc.override)
			if tc.wantNil {
				if merged != nil {
					t.Fatalf("merged = %+v, want nil", merged)
				}
				return
			}
			if merged == nil {
				t.Fatal("merged = nil, want a non-nil result")
			}
			if !boolPtrEqual(merged.Enabled, tc.wantEnable) {
				t.Errorf("Enabled = %v, want %v", boolPtrString(merged.Enabled), boolPtrString(tc.wantEnable))
			}
			if merged.Model != tc.wantModel {
				t.Errorf("Model = %q, want %q", merged.Model, tc.wantModel)
			}
			if !intPtrEqual(merged.Count, tc.wantCount) {
				t.Errorf("Count = %v, want %v", intPtrString(merged.Count), intPtrString(tc.wantCount))
			}
		})
	}
}

func boolPtrEqual(a, b *bool) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func boolPtrString(p *bool) string {
	if p == nil {
		return "<nil>"
	}
	if *p {
		return "true"
	}
	return "false"
}

func intPtrEqual(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func intPtrString(p *int) string {
	if p == nil {
		return "<nil>"
	}
	return strconv.Itoa(*p)
}
