package permissions

import (
	"strings"

	"github.com/konglong87/super-niuma/internal/shellcmd"
)

// shellRule classifies one simple command. Rules are evaluated in table order,
// per command, in the order the commands appear in the script.
//
// Both the permission-prompt layer (ClassifyRequestRisk) and the Bash tool's
// hard-refusal layer (HardDenyShellReason) read this one table, so the two can
// no longer disagree about what counts as destructive — previously the two
// pattern sets had drifted and `rm -r -f /` passed the hard-refusal layer.
type shellRule struct {
	// id is a stable identifier used in tests.
	id string
	// reason is the risk reason surfaced in the permission prompt.
	reason string
	// deny, when non-empty, makes this rule an outright refusal in the Bash tool
	// and carries the message shown to the model.
	deny string
	// match reports whether cmd triggers this rule.
	match func(cmd shellcmd.Command) bool
}

// rootishTargets are the operands that turn a recursive delete or a broad
// permission change into a destructive command.
var rootishTargets = map[string]bool{
	"/": true, "*": true, "~": true, "$HOME": true, "${HOME}": true, ".": true, "..": true,
	"/*": true, "~/": true, "~/*": true,
}

func isRootishTarget(value string) bool {
	return rootishTargets[strings.TrimSpace(value)]
}

// systemCriticalTargets are absolute directories whose recursive delete, chmod or
// chown breaks the machine rather than the workspace. rootishTargets names only
// the literal everything-targets ("/", "~", "$HOME", "."), so `rm -rf /usr` and
// `chmod -R 777 /etc` reached the permission layer as ordinary commands and were
// stopped only by the writable-root check one layer below. Two layers sharing one
// blind spot is not defence in depth: widen additionalDirectories and the lower
// layer stops covering for this one.
//
// Only the directory itself matches, never its descendants. `rm -rf /Users` is
// caught; `rm -rf /Users/me/project/build` is not, because that is ordinary work
// and stays the writable-root layer's job. Matching descendants here would deny
// legitimate recursive deletes inside an additionalDirectory — docs/session_quickstart.md
// documents adding sibling project directories for exactly that purpose.
//
// The list is the darwin/linux union and is applied on both: neither /System nor
// /boot is a legitimate recursive-delete target on either platform. Keeping it
// static is what preserves the property that makes this table shareable —
// HardDenyShellReason classifies a command with no cwd and no writable roots, so
// it cannot ask "is this target outside the workspace" without changing the
// contract the permission-prompt layer relies on.
var systemCriticalTargets = map[string]bool{
	// darwin
	"/System": true, "/Library": true, "/Applications": true, "/private": true,
	"/Volumes": true, "/cores": true,
	// linux
	"/boot": true, "/proc": true, "/sys": true, "/dev": true, "/lib": true,
	"/lib64": true, "/run": true,
	// shared unix
	"/usr": true, "/bin": true, "/sbin": true, "/etc": true, "/var": true,
	"/opt": true, "/srv": true, "/root": true,
	// every user's data at once
	"/Users": true, "/home": true,
}

func isSystemCriticalTarget(value string) bool {
	trimmed := strings.TrimSpace(value)
	trimmed = strings.TrimSuffix(trimmed, "/*")
	trimmed = strings.TrimSuffix(trimmed, "/")
	return systemCriticalTargets[trimmed]
}

// isCatastrophicTarget is the single predicate behind destructive_rm, broad_chmod
// and broad_chown so the three cannot drift apart the way the two destructive
// pattern sets did before they were merged into this table.
func isCatastrophicTarget(value string) bool {
	return isRootishTarget(value) || isSystemCriticalTarget(value)
}

func nameIn(name string, names ...string) bool {
	for _, candidate := range names {
		if name == candidate {
			return true
		}
	}
	return false
}

// downloaderNames fetch content from the network.
func isDownloader(name string) bool {
	return nameIn(name, "curl", "wget", "fetch", "http", "httpie", "aria2c")
}

// codeInterpreter names execute whatever they are handed.
func isCodeInterpreter(name string) bool {
	if shellcmd.IsShellInterpreter(name) {
		return true
	}
	return nameIn(name, "python", "python2", "python3", "ruby", "perl", "php", "node", "deno", "lua")
}

func fedByDownloader(cmd shellcmd.Command) bool {
	for _, producer := range cmd.FedBy {
		if isDownloader(producer) {
			return true
		}
	}
	return false
}

// mutatingFileCommands are the commands whose operands are files they write.
func isMutatingFileCommand(name string) bool {
	return nameIn(name, "rm", "unlink", "rmdir", "mv", "cp", "tee", "install", "ln",
		"chmod", "chown", "chgrp", "truncate", "shred", "dd", "sed")
}

var shellRules = []shellRule{
	{
		id:     "destructive_rm",
		reason: "destructive filesystem command",
		deny:   "refusing destructive rm command",
		match: func(cmd shellcmd.Command) bool {
			if cmd.Name != "rm" {
				return false
			}
			recursive := shellcmd.HasShortFlag(cmd.Args, 'r') || shellcmd.HasShortFlag(cmd.Args, 'R') ||
				shellcmd.HasLongFlag(cmd.Args, "--recursive")
			force := shellcmd.HasShortFlag(cmd.Args, 'f') || shellcmd.HasLongFlag(cmd.Args, "--force")
			if !recursive || !force {
				return false
			}
			for _, operand := range shellcmd.Operands(cmd.Args) {
				if isCatastrophicTarget(operand) {
					return true
				}
			}
			return false
		},
	},
	{
		id:     "find_delete",
		reason: "destructive filesystem command",
		match: func(cmd shellcmd.Command) bool {
			return cmd.Name == "find" && (shellcmd.HasLongFlag(cmd.Args, "-delete") || hasBareArg(cmd.Args, "-delete"))
		},
	},
	{
		id:     "truncate_zero",
		reason: "destructive filesystem command",
		match: func(cmd shellcmd.Command) bool {
			if cmd.Name != "truncate" {
				return false
			}
			if shellcmd.HasLongFlag(cmd.Args, "--size") {
				value, _ := shellcmd.FlagValue(cmd.Args, "--size")
				return strings.TrimSpace(value) == "0"
			}
			value, ok := shellcmd.FlagValue(cmd.Args, "-s")
			return ok && strings.TrimSpace(value) == "0"
		},
	},
	{
		id:     "shred",
		reason: "destructive filesystem command",
		match:  func(cmd shellcmd.Command) bool { return cmd.Name == "shred" },
	},
	{
		id:     "git_reset_hard",
		reason: "destructive git command",
		deny:   "refusing git reset --hard",
		match: func(cmd shellcmd.Command) bool {
			return gitSubcommand(cmd) == "reset" && shellcmd.HasLongFlag(cmd.Args, "--hard")
		},
	},
	{
		id:     "git_clean_force",
		reason: "destructive git command",
		deny:   "refusing destructive git clean",
		match: func(cmd shellcmd.Command) bool {
			if gitSubcommand(cmd) != "clean" {
				return false
			}
			return shellcmd.HasShortFlag(cmd.Args, 'f') || shellcmd.HasShortFlag(cmd.Args, 'd') ||
				shellcmd.HasLongFlag(cmd.Args, "--force")
		},
	},
	{
		id:     "git_hook_path_override",
		reason: "git hook path override",
		deny:   "refusing git hook path override",
		match: func(cmd shellcmd.Command) bool {
			return cmd.Name == "git" && gitSetsExecutableConfigKey(cmd.Args)
		},
	},
	{
		id:     "git_force_push",
		reason: "destructive git command",
		match: func(cmd shellcmd.Command) bool {
			if gitSubcommand(cmd) != "push" {
				return false
			}
			return shellcmd.HasShortFlag(cmd.Args, 'f') ||
				shellcmd.HasLongFlag(cmd.Args, "--force", "--force-with-lease", "--force-if-includes")
		},
	},
	{
		id:     "mkfs",
		reason: "filesystem formatting command",
		deny:   "refusing filesystem formatting command",
		match: func(cmd shellcmd.Command) bool {
			return cmd.Name == "mkfs" || strings.HasPrefix(cmd.Name, "mkfs.")
		},
	},
	{
		id:     "dd_raw_device",
		reason: "raw device write",
		deny:   "refusing raw device write",
		match: func(cmd shellcmd.Command) bool {
			if cmd.Name != "dd" {
				return false
			}
			for _, arg := range cmd.Args {
				if strings.HasPrefix(strings.ToLower(arg), "of=/dev/") {
					return true
				}
			}
			return false
		},
	},
	{
		id:     "redirect_raw_device",
		reason: "raw device write",
		deny:   "refusing raw device overwrite",
		match: func(cmd shellcmd.Command) bool {
			for _, redirect := range cmd.Redirects {
				if redirect.IsWrite() && isRawDevicePath(redirect.Target) {
					return true
				}
			}
			return false
		},
	},
	{
		id:     "broad_chmod",
		reason: "broad permission change",
		deny:   "refusing broad chmod 777",
		match: func(cmd shellcmd.Command) bool {
			if cmd.Name != "chmod" || !shellcmd.HasShortFlag(cmd.Args, 'R') && !shellcmd.HasLongFlag(cmd.Args, "--recursive") {
				return false
			}
			operands := shellcmd.Operands(cmd.Args)
			mode := false
			for _, operand := range operands {
				if strings.TrimSpace(operand) == "777" {
					mode = true
					continue
				}
				if mode && isCatastrophicTarget(operand) {
					return true
				}
			}
			return false
		},
	},
	{
		id:     "broad_chown",
		reason: "broad ownership change",
		deny:   "refusing broad ownership change",
		match: func(cmd shellcmd.Command) bool {
			if cmd.Name != "chown" || !shellcmd.HasShortFlag(cmd.Args, 'R') && !shellcmd.HasLongFlag(cmd.Args, "--recursive") {
				return false
			}
			operands := shellcmd.Operands(cmd.Args)
			if len(operands) < 2 {
				return false
			}
			for _, operand := range operands[1:] {
				if isCatastrophicTarget(operand) {
					return true
				}
			}
			return false
		},
	},
	{
		id:     "privileged",
		reason: "privileged command",
		match: func(cmd shellcmd.Command) bool {
			return cmd.Privileged || nameIn(cmd.Name, "su", "sudo", "doas", "pkexec", "runas")
		},
	},
	{
		id:     "system_service",
		reason: "system service command",
		match: func(cmd shellcmd.Command) bool {
			return nameIn(cmd.Name, "systemctl", "service", "launchctl", "rc-service")
		},
	},
	{
		id:     "disk_or_mount",
		reason: "disk or mount command",
		match: func(cmd shellcmd.Command) bool {
			return nameIn(cmd.Name, "mount", "umount", "diskutil", "fdisk", "parted", "sfdisk", "gdisk")
		},
	},
	{
		id:     "download_pipe_exec",
		reason: "network download piped to code execution",
		match: func(cmd shellcmd.Command) bool {
			return isCodeInterpreter(cmd.Name) && fedByDownloader(cmd)
		},
	},
	{
		id:     "shell_eval",
		reason: "arbitrary shell evaluation",
		match: func(cmd shellcmd.Command) bool {
			if cmd.Name == "eval" {
				return true
			}
			return shellcmd.IsShellInterpreter(cmd.Name) && shellcmd.HasShortFlag(cmd.Args, 'c')
		},
	},
	{
		id:     "interpreter_inline_code",
		reason: "arbitrary interpreter execution",
		match: func(cmd shellcmd.Command) bool {
			if shellcmd.IsShellInterpreter(cmd.Name) {
				return false
			}
			if !isCodeInterpreter(cmd.Name) {
				return false
			}
			return shellcmd.HasShortFlag(cmd.Args, 'c') || shellcmd.HasShortFlag(cmd.Args, 'e')
		},
	},
	{
		id:     "package_runner",
		reason: "arbitrary package runner execution",
		match: func(cmd shellcmd.Command) bool {
			return nameIn(cmd.Name, "npx", "bunx", "tsx", "pnpx", "dlx")
		},
	},
	{
		id:     "network_policy",
		reason: "network policy command",
		match: func(cmd shellcmd.Command) bool {
			return nameIn(cmd.Name, "iptables", "ip6tables", "pfctl", "nft", "ufw", "firewall-cmd")
		},
	},
	{
		id:     "network_listener",
		reason: "network listener or tunnel",
		match: func(cmd shellcmd.Command) bool {
			switch {
			case nameIn(cmd.Name, "nc", "ncat", "netcat"):
				return shellcmd.HasShortFlag(cmd.Args, 'l') || shellcmd.HasLongFlag(cmd.Args, "--listen")
			case cmd.Name == "socat":
				return true
			case cmd.Name == "ssh":
				return shellcmd.HasShortFlag(cmd.Args, 'L') || shellcmd.HasShortFlag(cmd.Args, 'R') ||
					shellcmd.HasShortFlag(cmd.Args, 'D')
			case nameIn(cmd.Name, "ngrok", "cloudflared", "tailscale", "sshuttle"):
				return true
			}
			return false
		},
	},
	{
		id:     "credential_access",
		reason: "credential access command",
		match: func(cmd shellcmd.Command) bool {
			switch cmd.Name {
			case "security":
				operands := shellcmd.Operands(cmd.Args)
				return len(operands) > 0 && nameIn(operands[0], "find-generic-password", "find-internet-password", "dump-keychain")
			case "ssh-keygen":
				return shellcmd.HasShortFlag(cmd.Args, 'p')
			}
			return false
		},
	},
}

// sensitivePathWriteRule is evaluated after shellRules and reports the lower
// "sensitive" level rather than "high".
func sensitivePathWrite(cmd shellcmd.Command) bool {
	for _, redirect := range cmd.Redirects {
		if redirect.IsWrite() && isSensitiveQualifier(redirect.Target) {
			return true
		}
	}
	if !isMutatingFileCommand(cmd.Name) {
		return false
	}
	for _, operand := range shellcmd.Operands(cmd.Args) {
		if isSensitiveQualifier(operand) {
			return true
		}
	}
	return false
}

func hasBareArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

// gitExecutableConfigKeys name git settings whose value git runs as a program.
// Setting one reaches the same outcome as writing .git/hooks, which the default
// write protection denies — and the path-level deny cannot see it, because the
// command names a config key rather than the file it lands in.
//
//   - core.hooksPath redirects every hook to a chosen directory.
//   - core.fsmonitor is executed on ordinary git commands.
//
// Deliberately narrower than "everything git can execute": core.editor,
// core.pager, sequence.editor, diff.external and `!`-prefixed alias.* are the same
// class, but they are also set legitimately far more often. Widening this set is a
// separate decision that needs its own false-positive review.
func isGitExecutableConfigKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "core.hookspath", "core.fsmonitor":
		return true
	default:
		return false
	}
}

// gitSetsExecutableConfigKey reports whether a git invocation assigns one of those
// keys. It scans the raw args rather than Operands because the two spellings reach
// the same result through different shapes:
//
//	git config core.hooksPath <dir>      persistent, writes .git/config
//	git -c core.hooksPath=<dir> <cmd>    this invocation only, writes nothing
//
// The second form is why gitSubcommand cannot be used here: Operands drops `-c`
// and skips `key=value` words, so that command's subcommand reads as the real one
// (`status`, `commit`, …) and a rule keyed on `config` never fires.
func gitSetsExecutableConfigKey(args []string) bool {
	for i, arg := range args {
		trimmed := strings.TrimSpace(arg)
		if key, value, ok := strings.Cut(trimmed, "="); ok {
			if isGitExecutableConfigKey(key) && strings.TrimSpace(value) != "" {
				return true
			}
			continue
		}
		if !isGitExecutableConfigKey(trimmed) {
			continue
		}
		// A bare key is a read (`git config core.hooksPath` prints the value) unless
		// a value operand follows it. `--unset` and `--get` are reads too.
		if gitConfigReadsOnly(args) {
			continue
		}
		if gitConfigValueFollows(args, i) {
			return true
		}
	}
	return false
}

func gitConfigReadsOnly(args []string) bool {
	for _, arg := range args {
		switch strings.ToLower(strings.TrimSpace(arg)) {
		case "--get", "--get-all", "--get-regexp", "--get-urlmatch", "--list", "-l",
			"--unset", "--unset-all":
			return true
		}
	}
	return false
}

func gitConfigValueFollows(args []string, keyIndex int) bool {
	for _, arg := range args[keyIndex+1:] {
		trimmed := strings.TrimSpace(arg)
		if trimmed == "" || strings.HasPrefix(trimmed, "-") {
			continue
		}
		return true
	}
	return false
}

func gitSubcommand(cmd shellcmd.Command) string {
	if cmd.Name != "git" {
		return ""
	}
	for _, operand := range shellcmd.Operands(cmd.Args) {
		// Skip `-c key=value` style globals, which Operands already drops, and
		// take the first bare word as the subcommand.
		if strings.TrimSpace(operand) == "" || strings.Contains(operand, "=") {
			continue
		}
		return operand
	}
	return ""
}

func isRawDevicePath(path string) bool {
	path = strings.ToLower(strings.TrimSpace(path))
	for _, prefix := range []string{"/dev/sd", "/dev/hd", "/dev/nvme", "/dev/disk", "/dev/rdisk", "/dev/vd"} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// shellScriptCommands flattens command into simple commands. When the script
// cannot be parsed it falls back to splitting on shell separators, so a command
// crafted to defeat the parser still gets classified rather than waved through.
func shellScriptCommands(command string) []shellcmd.Command {
	script, err := shellcmd.Parse(command)
	if err == nil {
		return script.Commands
	}
	commands := append([]shellcmd.Command(nil), script.Commands...)
	for _, fragment := range splitShellFragments(command) {
		fields := skipShellKeywords(strings.Fields(fragment))
		if len(fields) == 0 {
			continue
		}
		name, rest, privileged := shellcmd.UnwrapCommand(fields)
		if name == "" {
			continue
		}
		commands = append(commands, shellcmd.Command{Name: name, Args: rest, Privileged: privileged})
	}
	return commands
}

// shellKeywords are the control-flow words the fallback tokenizer must step over
// to reach the command; the AST path never sees them as command names.
var shellKeywords = map[string]bool{
	"if": true, "then": true, "else": true, "elif": true, "fi": true,
	"for": true, "while": true, "until": true, "do": true, "done": true,
	"case": true, "esac": true, "in": true, "function": true, "select": true,
	"!": true, "{": true, "}": true, "[[": true, "]]": true,
}

func skipShellKeywords(fields []string) []string {
	for len(fields) > 0 && shellKeywords[fields[0]] {
		fields = fields[1:]
	}
	return fields
}

func splitShellFragments(command string) []string {
	replacer := strings.NewReplacer("\n", "\x00", "\r", "\x00", ";", "\x00", "&", "\x00", "|", "\x00",
		"(", "\x00", ")", "\x00", "{", "\x00", "}", "\x00", "`", "\x00")
	return strings.Split(replacer.Replace(command), "\x00")
}

// classifyShellScript returns the first rule triggered by any command in the
// script, plus a sensitive-level fallback.
func classifyShellScript(command string) RequestRisk {
	commands := shellScriptCommands(command)
	for _, cmd := range commands {
		for _, rule := range shellRules {
			if rule.match(cmd) {
				return RequestRisk{Level: "high", Reason: rule.reason}
			}
		}
	}
	for _, cmd := range commands {
		if sensitivePathWrite(cmd) {
			return RequestRisk{Level: "sensitive", Reason: "sensitive path write"}
		}
	}
	if isLikelyCredentialExfiltration(normalizeCommandForRisk(command)) {
		return RequestRisk{Level: "sensitive", Reason: "credential access or exfiltration"}
	}
	return RequestRisk{}
}

// HardDenyShellReason returns the refusal message for a shell command the Bash
// tool must not run at all, or "" when the command is merely prompt-worthy.
// It shares the rule table with ClassifyRequestRisk so the refusal layer can
// never cover less than the prompt layer claims.
func HardDenyShellReason(command string) string {
	for _, cmd := range shellScriptCommands(command) {
		for _, rule := range shellRules {
			if rule.deny == "" || !rule.match(cmd) {
				continue
			}
			return rule.deny
		}
	}
	return ""
}
