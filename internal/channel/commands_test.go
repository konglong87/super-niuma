package channel

import "testing"

func TestChannelCommandRouterPassesPlainText(t *testing.T) {
	router := NewChannelCommandRouter([]string{"ou-admin"})
	got := router.Route(CommandInput{Text: "hello", ChatType: ChatTypeP2P, ExternalUserID: "ou-user"})
	if got.Kind != CommandRoutePass || got.Invocation.Name != "" {
		t.Fatalf("route = %+v, want pass", got)
	}
}

func TestChannelCommandRouterNormalizesKnownCommands(t *testing.T) {
	router := NewChannelCommandRouter([]string{"ou-admin"})
	got := router.Route(CommandInput{Text: "  /PeRmIsSiOn   allow  ", ChatType: ChatTypeP2P, ExternalUserID: "ou-admin"})
	if got.Kind != CommandRouteBuiltin || got.Invocation.Name != CommandPermission || got.Invocation.Args != "allow" || got.Class != CommandClassControlled {
		t.Fatalf("route = %+v", got)
	}
	if got := router.Route(CommandInput{Text: "/image a sunset", ChatType: ChatTypeP2P, ExternalUserID: "ou-user"}); got.Kind != CommandRouteBuiltin || got.Invocation.Name != CommandImage || got.Invocation.Args != "a sunset" {
		t.Fatalf("image route = %+v", got)
	}
}

func TestChannelCommandRouterAppliesSecurityMatrix(t *testing.T) {
	router := NewChannelCommandRouter([]string{"ou-admin"})
	tests := []struct {
		name string
		in   CommandInput
		want CommandRouteKind
	}{
		{name: "safe DM user", in: CommandInput{Text: "/new", ChatType: ChatTypeP2P, ExternalUserID: "ou-user"}, want: CommandRouteBuiltin},
		{name: "safe group user", in: CommandInput{Text: "/status", ChatType: ChatTypeGroup, ExternalUserID: "ou-user"}, want: CommandRouteBuiltin},
		{name: "controlled DM admin", in: CommandInput{Text: "/cwd /tmp", ChatType: ChatTypeP2P, ExternalUserID: "ou-admin"}, want: CommandRouteBuiltin},
		{name: "controlled DM user", in: CommandInput{Text: "/cwd /tmp", ChatType: ChatTypeP2P, ExternalUserID: "ou-user"}, want: CommandRouteDenied},
		{name: "controlled group admin", in: CommandInput{Text: "/permission deny", ChatType: ChatTypeGroup, ExternalUserID: "ou-admin"}, want: CommandRouteDenied},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := router.Route(tc.in); got.Kind != tc.want {
				t.Fatalf("route = %+v, want kind %q", got, tc.want)
			}
		})
	}
}

func TestChannelCommandRouterSeparatesSkillCandidatesAndUnsupportedBuiltins(t *testing.T) {
	router := NewChannelCommandRouter(nil)
	if got := router.Route(CommandInput{Text: "/my-skill answer", ChatType: ChatTypeP2P}); got.Kind != CommandRouteSkill || got.Invocation.Name != "my-skill" || got.Invocation.Args != "answer" {
		t.Fatalf("skill route = %+v", got)
	}
	for _, name := range []string{"clear", "exit", "quit", "attach", "mcp", "plugins", "hooks", "init", "loop", "goal"} {
		got := router.Route(CommandInput{Text: "/" + name, ChatType: ChatTypeP2P})
		if got.Kind != CommandRouteUnsupported {
			t.Fatalf("/%s route = %+v, want unsupported", name, got)
		}
	}
}
