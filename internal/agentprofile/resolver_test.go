package agentprofile

import (
	"context"
	"errors"
	"testing"
)

type fakeProfileSource struct {
	profiles    []Profile
	assignments map[string]ProfileAssignment
}

func (f fakeProfileSource) FindPublishedProfile(_ context.Context, _ uint64, _ uint64, key string, version uint) (Profile, error) {
	for _, profile := range f.profiles {
		if profile.Key == key && (version == 0 || profile.Version == version) && profile.Status == StatusPublished {
			return profile, nil
		}
	}
	return Profile{}, ErrProfileNotFound
}

func (f fakeProfileSource) FindAssignment(_ context.Context, _ uint64, _ uint64, surface string) (ProfileAssignment, error) {
	assignment, ok := f.assignments[surface]
	if !ok {
		return ProfileAssignment{}, ErrAssignmentNotFound
	}
	return assignment, nil
}

func TestResolverUsesSurfaceAssignmentWithoutChangingCodePath(t *testing.T) {
	copywriter := builtinCopywriter()
	source := fakeProfileSource{
		profiles: []Profile{copywriter},
		assignments: map[string]ProfileAssignment{
			SurfaceWebChat: {ProfileKey: ProfileCopywriter, ProfileVersion: copywriter.Version},
		},
	}
	resolved, err := NewResolver(NewCatalog(source), RuntimePolicy{}).Resolve(context.Background(), ResolveRequest{TenantID: 7, UserID: 11, Surface: SurfaceWebChat})
	if err != nil {
		t.Fatalf("resolve assigned profile: %v", err)
	}
	if resolved.Profile.Key != ProfileCopywriter || resolved.Source != SourceAssignment {
		t.Fatalf("resolved profile = %+v source=%q", resolved.Profile, resolved.Source)
	}
	if resolved.Config.Prompt.Mode != PromptModeChat || resolved.Config.Context.Workspace || resolved.Config.Context.Git {
		t.Fatalf("chat context leaked into effective profile: %+v", resolved.Config.Context)
	}

	_, err = NewResolver(NewCatalog(source), RuntimePolicy{}).Resolve(context.Background(), ResolveRequest{Surface: SurfaceCLI})
	if !errors.Is(err, ErrProfileNotSelected) {
		t.Fatalf("profile-less CLI resolve error = %v, want ErrProfileNotSelected", err)
	}
}

func TestResolverIntersectsCapabilitiesAndBudgets(t *testing.T) {
	profile := builtinCoder()
	profile.Config.Capabilities.Tools.Allow = []string{"Read", "Edit", "Bash"}
	profile.Config.Execution.MaxTurns = 20
	profile.Config.Execution.MaxTokens = 8192
	source := fakeProfileSource{profiles: []Profile{profile}}
	policy := RuntimePolicy{
		AllowedTools:       StringSet{"Read": {}, "Edit": {}},
		DeniedTools:        StringSet{"Edit": {}},
		MaxTurns:           4,
		MaxTokens:          2048,
		MaxParallelWorkers: 2,
		AllowWorkspace:     true,
		AllowGit:           true,
	}
	resolved, err := NewResolver(NewCatalog(source), policy).Resolve(context.Background(), ResolveRequest{
		ProfileKey: ProfileCoder,
		Overrides:  ProfileOverrides{MaxTurns: intPtr(9), MaxTokens: intPtr(4096), Effort: "medium"},
	})
	if err != nil {
		t.Fatalf("resolve capability intersection: %v", err)
	}
	if got := resolved.Config.Capabilities.Tools.Allow; len(got) != 1 || got[0] != "Read" {
		t.Fatalf("effective tools = %v, want [Read]", got)
	}
	if resolved.Config.Execution.MaxTurns != 4 || resolved.Config.Execution.MaxTokens != 2048 || resolved.Config.Execution.Effort != "medium" {
		t.Fatalf("effective budgets = %+v", resolved.Config.Execution)
	}
	for _, field := range []string{"capabilities.tools.allow.Edit", "capabilities.tools.allow.Bash", "execution.max_turns", "execution.max_tokens"} {
		if !resolved.HasBlocked(field) {
			t.Fatalf("blocked override %q missing: %+v", field, resolved.BlockedOverrides)
		}
	}
}

func TestApplierCompilesEffectiveProfileToRuntimeInput(t *testing.T) {
	profile := builtinCoder()
	effective := EffectiveProfile{
		Profile: profile,
		Config:  profile.Config,
	}
	input := Apply(effective)
	if input.PromptMode != string(PromptModeCode) || input.RuntimeProfile != "default" {
		t.Fatalf("runtime mode = %+v", input)
	}
	if input.MaxTurns != profile.Config.Execution.MaxTurns || input.MaxTokens != profile.Config.Execution.MaxTokens {
		t.Fatalf("runtime budgets = %+v", input)
	}
	if len(input.AllowedTools) == 0 || !contains(StringSet{"Read": {}}, input.AllowedTools[0]) && input.AllowedTools[0] == "" {
		t.Fatalf("runtime tools = %v", input.AllowedTools)
	}
}

func TestResolverRequiresExplicitWebUIV2OrchestratorProfile(t *testing.T) {
	resolver := NewResolver(NewCatalog(nil), RuntimePolicy{})
	if _, err := resolver.Resolve(context.Background(), ResolveRequest{Surface: SurfaceWebChat}); err != nil {
		t.Fatalf("normal web chat resolve: %v", err)
	}
	effective, err := resolver.Resolve(context.Background(), ResolveRequest{Surface: SurfaceWebUIV2Orchestrator, ProfileKey: ProfileWebUIV2Orchestrator})
	if err != nil {
		t.Fatalf("resolve orchestrator: %v", err)
	}
	if !IsWebUIV2Orchestrator(effective.Profile) {
		t.Fatalf("effective profile = %+v", effective.Profile)
	}
}

func intPtr(value int) *int { return &value }
