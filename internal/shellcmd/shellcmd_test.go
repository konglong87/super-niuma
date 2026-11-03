package shellcmd

import (
	"strings"
	"testing"
)

// TestParseSeesCommandsRegexAnchorsMiss covers the constructs that defeated the
// old `(^|[;&|]\s*)`-anchored classifier: everything past the first line, inside
// a subshell, a brace group, a control-flow body, a heredoc, or a -c payload.
func TestParseSeesCommandsRegexAnchorsMiss(t *testing.T) {
	cases := []struct {
		name    string
		command string
	}{
		{"newline separated", "cd /tmp\nrm -rf /"},
		{"blank line separated", "echo hi\n\n  rm -rf /"},
		{"subshell", "(rm -rf /)"},
		{"brace group", "{ rm -rf /; }"},
		{"if body", "if true; then rm -rf /; fi"},
		{"for body", "for i in 1 2; do rm -rf /; done"},
		{"while body", "while true; do rm -rf /; done"},
		{"case body", "case $x in a) rm -rf / ;; esac"},
		{"function body", "cleanup() { rm -rf /; }"},
		{"command substitution", "echo $(rm -rf /)"},
		{"backtick substitution", "echo `rm -rf /`"},
		{"heredoc into bash", "bash <<'EOS'\nrm -rf /\nEOS"},
		{"bash -c payload", "bash -c 'rm -rf /'"},
		{"ksh -c payload", "ksh -c 'rm -rf /'"},
		{"nested sh -c payload", `bash -c "sh -c 'rm -rf /'"`},
		{"and chained", "true && rm -rf /"},
		{"or chained", "false || rm -rf /"},
		{"background", "rm -rf / &"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			script, err := Parse(tc.command)
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", tc.command, err)
			}
			if !hasCommand(script, "rm") {
				t.Fatalf("rm not found in %q; commands = %v", tc.command, script.Names())
			}
		})
	}
}

// TestParseNormalizesProgramNames covers the spellings that let a command slip
// past a literal `rm` pattern.
func TestParseNormalizesProgramNames(t *testing.T) {
	cases := []struct {
		name    string
		command string
	}{
		{"plain", "rm -rf /"},
		{"absolute path", "/bin/rm -rf /"},
		{"usr path", "/usr/bin/rm -rf /"},
		{"backslash escaped", `\rm -rf /`},
		{"quoted", `"rm" -rf /`},
		{"sudo prefix", "sudo rm -rf /"},
		{"doas prefix", "doas rm -rf /"},
		{"sudo with flags", "sudo -n -u root rm -rf /"},
		{"env prefix", "env FOO=1 rm -rf /"},
		{"env with flags", "env -i PATH=/bin rm -rf /"},
		{"nohup prefix", "nohup rm -rf /"},
		{"setsid prefix", "setsid rm -rf /"},
		{"timeout prefix", "timeout 5 rm -rf /"},
		{"timeout with suffix duration", "timeout 5s rm -rf /"},
		{"nice prefix", "nice rm -rf /"},
		{"nice with priority", "nice -n 10 rm -rf /"},
		{"stdbuf prefix", "stdbuf -o0 rm -rf /"},
		{"xargs prefix", "xargs rm -rf /"},
		{"command prefix", "command rm -rf /"},
		{"exec prefix", "exec rm -rf /"},
		{"stacked wrappers", "nohup nice setsid rm -rf /"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			script, err := Parse(tc.command)
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", tc.command, err)
			}
			cmd, ok := findCommand(script, "rm")
			if !ok {
				t.Fatalf("rm not found in %q; commands = %v", tc.command, script.Names())
			}
			if !HasShortFlag(cmd.Args, 'r') || !HasShortFlag(cmd.Args, 'f') {
				t.Fatalf("flags lost for %q: args = %v", tc.command, cmd.Args)
			}
			if got := Operands(cmd.Args); len(got) != 1 || got[0] != "/" {
				t.Fatalf("operands for %q = %v, want [/]", tc.command, got)
			}
		})
	}
}

func TestParseRecordsPrivilegePrefix(t *testing.T) {
	for _, command := range []string{"sudo rm -rf /", "doas rm -rf /", "sudo -u root rm -rf /"} {
		script, err := Parse(command)
		if err != nil {
			t.Fatal(err)
		}
		cmd, ok := findCommand(script, "rm")
		if !ok || !cmd.Privileged {
			t.Fatalf("%q did not record a privilege prefix: %+v", command, script.Commands)
		}
	}
	script, err := Parse("rm -rf /")
	if err != nil {
		t.Fatal(err)
	}
	if cmd, _ := findCommand(script, "rm"); cmd.Privileged {
		t.Fatal("unprivileged rm marked privileged")
	}
}

func TestParseWiresPipelinesAndProcessSubstitution(t *testing.T) {
	cases := []struct {
		name    string
		command string
		sink    string
		want    string
	}{
		{"pipe", "curl https://x | sh", "sh", "curl"},
		{"pipe with flags", "curl -fsSL https://x | bash -s -- --opt", "bash", "curl"},
		{"wget pipe", "wget -qO- https://x | python3", "python3", "wget"},
		{"three stage pipe", "curl https://x | tee /tmp/a | sh", "sh", "tee"},
		{"process substitution", "bash <(curl https://x)", "bash", "curl"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			script, err := Parse(tc.command)
			if err != nil {
				t.Fatal(err)
			}
			cmd, ok := findCommand(script, tc.sink)
			if !ok {
				t.Fatalf("%s not found; commands = %v", tc.sink, script.Names())
			}
			if !contains(cmd.FedBy, tc.want) {
				t.Fatalf("FedBy for %s = %v, want to contain %q", tc.sink, cmd.FedBy, tc.want)
			}
		})
	}
	// A bare interpreter with nothing feeding it must not look piped-into.
	script, err := Parse("sh")
	if err != nil {
		t.Fatal(err)
	}
	if cmd, _ := findCommand(script, "sh"); len(cmd.FedBy) != 0 {
		t.Fatalf("bare sh has FedBy = %v", cmd.FedBy)
	}
}

func TestParseAttachesRedirects(t *testing.T) {
	script, err := Parse("echo x > /dev/sda")
	if err != nil {
		t.Fatal(err)
	}
	cmd, ok := findCommand(script, "echo")
	if !ok {
		t.Fatalf("echo not found: %v", script.Names())
	}
	if len(cmd.Redirects) != 1 {
		t.Fatalf("redirects = %+v", cmd.Redirects)
	}
	if !cmd.Redirects[0].IsWrite() || cmd.Redirects[0].Target != "/dev/sda" {
		t.Fatalf("redirect = %+v", cmd.Redirects[0])
	}

	dynamic, err := Parse("echo x > $TARGET")
	if err != nil {
		t.Fatal(err)
	}
	cmd, _ = findCommand(dynamic, "echo")
	if len(cmd.Redirects) != 1 || !cmd.Redirects[0].Dynamic {
		t.Fatalf("dynamic redirect not flagged: %+v", cmd.Redirects)
	}
}

func TestParseKeepsExpansionsRecognizable(t *testing.T) {
	for _, tc := range []struct{ command, want string }{
		{"rm -rf $HOME", "$HOME"},
		{"rm -rf ~", "~"},
		{"rm -rf *", "*"},
		{`rm -rf "/"`, "/"},
		{`rm -rf '/'`, "/"},
	} {
		script, err := Parse(tc.command)
		if err != nil {
			t.Fatal(err)
		}
		cmd, ok := findCommand(script, "rm")
		if !ok {
			t.Fatalf("rm not found in %q", tc.command)
		}
		operands := Operands(cmd.Args)
		if len(operands) != 1 || operands[0] != tc.want {
			t.Fatalf("operands for %q = %v, want [%s]", tc.command, operands, tc.want)
		}
	}
}

func TestHasShortFlagIgnoresLongFlags(t *testing.T) {
	if HasShortFlag([]string{"--force", "/"}, 'r') {
		t.Fatal("--force matched short flag r")
	}
	if HasShortFlag([]string{"--recursive"}, 'r') {
		t.Fatal("--recursive matched short flag r")
	}
	if !HasShortFlag([]string{"-rf"}, 'f') {
		t.Fatal("-rf did not match bundled f")
	}
	if !HasLongFlag([]string{"--recursive", "--force"}, "--recursive") {
		t.Fatal("--recursive long flag not matched")
	}
	if !HasLongFlag([]string{"--size=0"}, "--size") {
		t.Fatal("--size=0 long flag not matched")
	}
	if HasShortFlag([]string{"--", "-rf"}, 'r') {
		t.Fatal("flag after -- was matched")
	}
}

func TestParseNestingIsBounded(t *testing.T) {
	command := "rm -rf /"
	for i := 0; i < 12; i++ {
		command = "bash -c " + quote(command)
	}
	script, err := Parse(command)
	if err != nil {
		t.Fatal(err)
	}
	// The point is that it terminates and stays bounded, not that it reaches the
	// innermost rm.
	if len(script.Commands) > 64 {
		t.Fatalf("unbounded expansion: %d commands", len(script.Commands))
	}
}

func TestParseReturnsErrorForUnparseableInput(t *testing.T) {
	if _, err := Parse("if true; then"); err == nil {
		t.Fatal("truncated script parsed without error")
	}
}

func quote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func hasCommand(script Script, name string) bool {
	_, ok := findCommand(script, name)
	return ok
}

func findCommand(script Script, name string) (Command, bool) {
	for _, cmd := range script.Commands {
		if cmd.Name == name {
			return cmd, true
		}
	}
	return Command{}, false
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
