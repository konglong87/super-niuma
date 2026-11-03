package cli

import (
	"testing"

	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/permissions"
	"github.com/konglong87/super-niuma/internal/server"
)

func TestSessionRuntimeOptionsHonorPermissionAndEffortWithoutDefaultExpansion(t *testing.T) {
	for _, mode := range []string{permissions.ModeAsk, permissions.ModeDeny, permissions.ModeAllow, permissions.ModeAcceptEdits, permissions.ModeBypassPermissions} {
		t.Run(mode, func(t *testing.T) {
			opts := options{skipPermissions: true, permissionBypass: true, effort: "low"}
			if err := applyServerSessionRuntimeOptions(&opts, server.QueryRequest{PermissionMode: mode, Effort: "high"}); err != nil {
				t.Fatal(err)
			}
			settings := config.Settings{Permissions: config.PermissionSettings{Bypass: true, Deny: []string{"Bash(rm *)"}, AlwaysAsk: []string{"Write"}}}
			if err := applyRuntimeOptions(&settings, opts); err != nil {
				t.Fatal(err)
			}
			policy := permissions.FromSettings(settings.Permissions)
			if opts.effort != "high" || policy.Bypass != permissions.ModeGrantsBypass(mode) || len(policy.Deny) != 1 {
				t.Fatalf("opts=%+v policy=%+v", opts.effort, policy)
			}
			if mode == permissions.ModeAllow && policy.Check("Write").Allowed {
				t.Fatal("allow bypassed alwaysAsk rules")
			}
			if mode == permissions.ModeAsk && policy.DefaultMode != permissions.ModeAsk {
				t.Fatal("ask was not applied")
			}
		})
	}
	opts := options{permissionMode: permissions.ModeAsk, effort: "low"}
	if err := applyServerSessionRuntimeOptions(&opts, server.QueryRequest{}); err != nil {
		t.Fatal(err)
	}
	if opts.permissionMode != permissions.ModeAsk || opts.effort != "low" || opts.permissionModeExplicit {
		t.Fatal("omitted settings changed runtime defaults")
	}
}
