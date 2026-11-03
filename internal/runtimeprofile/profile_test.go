package runtimeprofile

import "testing"

func TestResolveProfiles(t *testing.T) {
	tests := []struct {
		name    string
		profile Profile
		want    Policy
	}{
		{
			name: "zero value is default",
			want: Policy{
				Profile:                  ProfileDefault,
				ToolSet:                  ToolSetFull,
				DiscoverWorkspaceContext: true,
				DiscoverSkills:           true,
				DiscoverAgents:           true,
				DiscoverPlugins:          true,
				DiscoverMCP:              true,
				RunHooks:                 true,
				RunStartupUpdate:         true,
				RunBackgroundEnrichment:  true,
				AllowAutoMemoryRoot:      true,
			},
		},
		{
			name:    "bare disables automatic contributors",
			profile: ProfileBare,
			want: Policy{
				Profile: ProfileBare,
				ToolSet: ToolSetMinimal,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Resolve(test.profile)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("Resolve(%q) = %+v, want %+v", test.profile, got, test.want)
			}
		})
	}
}

func TestResolveRejectsUnknownProfile(t *testing.T) {
	if _, err := Resolve(Profile("minimal")); err == nil {
		t.Fatal("Resolve accepted an unknown runtime profile")
	}
}

func TestBareBuiltinToolsReturnsIndependentSlice(t *testing.T) {
	first := BareBuiltinTools()
	first[0] = "Task"
	second := BareBuiltinTools()
	if second[0] != BareToolRead {
		t.Fatalf("BareBuiltinTools leaked mutation: %v", second)
	}
}
