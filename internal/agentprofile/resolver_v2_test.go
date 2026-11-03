package agentprofile

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func storedProfileV2(document ProfileDocument) Profile {
	return Profile{Key: "workflow-agent", Version: 1, Scope: ScopeUserPrivate, Status: StatusPublished, Config: document}
}

func TestV2ResolverAndApplyPreserveExplicitNoTools(t *testing.T) {
	profile := storedProfileV2(textProfileV2())
	resolver := NewResolver(NewCatalog(fakeProfileSource{profiles: []Profile{profile}}), RuntimePolicy{
		AllowedTools: StringSet{"Read": {}, "Bash": {}}, AllowedSkills: StringSet{}, AllowedMCPServers: StringSet{},
		AllowWorkspace: true, AllowGit: true,
	})
	resolved, err := resolver.Resolve(context.Background(), ResolveRequest{ProfileKey: profile.Key, ProfileVersion: profile.Version})
	if err != nil {
		t.Fatal(err)
	}
	input := Apply(resolved)
	if len(resolved.Config.Capabilities.Tools.Allow) != 0 || len(input.AllowedTools) != 0 {
		t.Fatalf("noTools expanded into defaults: %+v / %+v", resolved.Config.Capabilities, input)
	}
	if resolved.Config.Identity.RoleType != RoleTypeGeneral || resolved.Config.Execution.Kind != ExecutionKindText {
		t.Fatalf("resolver dropped V2 metadata: %+v", resolved.Config)
	}
	if resolved.RequestedHash != resolved.EffectiveHash || input.PromptMode != string(PromptModeChat) {
		t.Fatalf("safe no-tool profile changed unexpectedly: %+v / %+v", resolved, input)
	}
}

func TestV2ResolverUsesExistingPolicyIntersectionAndApply(t *testing.T) {
	document := textProfileV2()
	document.Prompt = PromptPolicy{Mode: PromptModeCode, Persona: "persona", SystemAddendum: "instructions"}
	document.Identity.RoleType = RoleTypeExecutor
	document.Capabilities = CapabilityPolicy{
		Tools: ToolPolicy{Allow: []string{"Read", "Edit", "Bash"}}, Skills: []string{"safe", "private"}, MCPServers: []string{"docs", "private"},
	}
	document.Context = ContextPolicy{Workspace: true, Git: true}
	document.Execution.MaxTurns = 20
	document.Execution.MaxTokens = 8192
	document.Execution.MaxParallelReadOnlyTools = 8
	profile := storedProfileV2(document)
	policy := RuntimePolicy{
		AllowedTools: StringSet{"Read": {}, "Edit": {}}, DeniedTools: StringSet{"Edit": {}},
		AllowedSkills: StringSet{"safe": {}}, AllowedMCPServers: StringSet{"docs": {}},
		MaxTurns: 4, MaxTokens: 2048, MaxParallelWorkers: 2,
	}
	resolved, err := NewResolver(NewCatalog(fakeProfileSource{profiles: []Profile{profile}}), policy).Resolve(context.Background(), ResolveRequest{
		ProfileKey: profile.Key, Overrides: ProfileOverrides{MaxTurns: intPtr(99), MaxTokens: intPtr(99999)},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{
		"capabilities.tools.allow.Edit", "capabilities.tools.allow.Bash", "capabilities.skills.private", "capabilities.mcp_servers.private",
		"context.workspace", "context.git", "execution.max_turns", "execution.max_tokens", "execution.max_parallel_read_only_tools",
	} {
		if !resolved.HasBlocked(field) {
			t.Fatalf("policy restriction missing for %s: %+v", field, resolved.BlockedOverrides)
		}
	}
	if resolved.RequestedHash == resolved.EffectiveHash || resolved.Requested.Execution.MaxTurns != 99 {
		t.Fatalf("requested/effective snapshot not separated: %+v", resolved)
	}
	input := Apply(resolved)
	if !reflect.DeepEqual(input.AllowedTools, []string{"Read"}) || !reflect.DeepEqual(input.DeniedTools, []string{"Edit"}) ||
		input.WorkspaceContext || input.GitContext || input.MaxTurns != 4 || input.MaxTokens != 2048 || input.MaxParallelReadOnlyTools != 2 ||
		input.SystemAddendum != "persona\n\ninstructions" {
		t.Fatalf("V2 bypassed existing runtime mapper: %+v", input)
	}
	if !reflect.DeepEqual(resolved.Config.Capabilities.Skills, []string{"safe"}) || !reflect.DeepEqual(resolved.Config.Capabilities.MCPServers, []string{"docs"}) {
		t.Fatalf("skills/MCP policy intersection skipped: %+v", resolved.Config.Capabilities)
	}
	input.AllowedTools[0] = "Bash"
	if resolved.Config.Capabilities.Tools.Allow[0] != "Read" {
		t.Fatal("Apply leaked mutable policy snapshot")
	}
}

func TestV2ResolverRejectsUnsafeDocumentBeforePolicyApplication(t *testing.T) {
	for _, mutation := range []func(*ProfileDocument){
		func(d *ProfileDocument) { d.Safety.PermissionMode = PermissionModeBypass },
		func(d *ProfileDocument) { d.Safety.AllowUnsandboxedCommands = true },
		func(d *ProfileDocument) { d.Context.Workspace = true },
		func(d *ProfileDocument) { d.Identity.RoleType = "superuser" },
		func(d *ProfileDocument) { d.Execution.Kind = "shell" },
	} {
		document := textProfileV2()
		mutation(&document)
		profile := storedProfileV2(document)
		_, err := NewResolver(NewCatalog(fakeProfileSource{profiles: []Profile{profile}}), RuntimePolicy{
			AllowedTools: StringSet{}, AllowedSkills: StringSet{}, AllowedMCPServers: StringSet{},
		}).Resolve(context.Background(), ResolveRequest{ProfileKey: profile.Key})
		if !errors.Is(err, ErrInvalidProfile) {
			t.Fatalf("unsafe V2 profile reached runtime: %+v, err=%v", document, err)
		}
	}
}
