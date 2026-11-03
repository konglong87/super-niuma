// Package shellcmd flattens a POSIX shell script into the list of simple
// commands it can run, using the same mvdan.cc/sh parser the sandbox already
// depends on.
//
// It exists because anchored regexes over the raw command string cannot see
// command boundaries: a pattern anchored at `(^|[;&|]\s*)` never matches the
// second line of a multi-line script, anything inside a subshell, a brace
// group, an if/for body, a heredoc, or a `bash -c` payload. Callers that need
// to reason about "what does this script actually run" should walk Script.
package shellcmd

import (
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// maxNestingDepth bounds recursion into `bash -c` payloads and heredoc bodies.
const maxNestingDepth = 4

// Redirect is one redirection attached to a command.
type Redirect struct {
	Op string
	// Target is the redirection target with quotes resolved. It is empty when
	// the target is not statically knowable.
	Target string
	// Dynamic is true when the target depends on expansion (variables, command
	// substitution) and therefore could not be resolved.
	Dynamic bool
}

// IsWrite reports whether the redirection writes to its target.
func (r Redirect) IsWrite() bool {
	return strings.Contains(r.Op, ">") || r.Op == "<>"
}

// Command is one simple command, with wrapper prefixes (sudo, env, nohup,
// timeout, …) stripped and the program reduced to its base name so that `rm`,
// `/bin/rm` and `\rm` all normalize to "rm".
type Command struct {
	// Name is the normalized program name, or "" when it is not statically
	// knowable (e.g. `$CMD -rf /`).
	Name string
	// Args are the words after the program name, with wrapper prefixes removed.
	// Each entry has quotes resolved where possible and otherwise keeps its
	// source text, so `"$HOME"` stays recognizable as `$HOME`.
	Args []string
	// Privileged is true when a sudo/doas-style prefix was stripped, or when the
	// command itself is a privilege escalation tool.
	Privileged bool
	// Redirects are the redirections attached to this command.
	Redirects []Redirect
	// FedBy holds the normalized names of commands whose output reaches this one
	// through a pipe or a process substitution argument, so a caller can tell
	// `curl url | sh` from a bare `sh`.
	FedBy []string
	// Dynamic is true when at least one argument depends on expansion.
	Dynamic bool
}

// Script is the flattened form of a shell command string.
type Script struct {
	Commands []Command
}

// Names returns the normalized names of every command in the script.
func (s Script) Names() []string {
	out := make([]string, 0, len(s.Commands))
	for _, cmd := range s.Commands {
		if cmd.Name != "" {
			out = append(out, cmd.Name)
		}
	}
	return out
}

// Parse flattens command into the simple commands it can run. A parse error is
// returned unchanged so callers can decide how to treat unparseable input;
// Script is still populated with whatever was recovered before the error.
func Parse(command string) (Script, error) {
	var s Script
	if strings.TrimSpace(command) == "" {
		return s, nil
	}
	err := s.parseInto(command, 0)
	return s, err
}

func (s *Script) parseInto(command string, depth int) error {
	if depth > maxNestingDepth {
		return nil
	}
	file, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if file == nil {
		return err
	}
	collected := map[*syntax.CallExpr]int{}
	printer := syntax.NewPrinter()

	// First pass: one Command per CallExpr, in source order.
	syntax.Walk(file, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		args, dynamic := wordValues(printer, call.Args)
		name, rest, privileged := UnwrapCommand(args)
		collected[call] = len(s.Commands)
		s.Commands = append(s.Commands, Command{
			Name:       name,
			Args:       rest,
			Privileged: privileged,
			Dynamic:    dynamic,
		})
		return true
	})

	// Second pass: attach redirections to their command, wire pipelines and
	// process substitutions, and descend into nested scripts.
	var nested []string
	syntax.Walk(file, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.Stmt:
			call, ok := n.Cmd.(*syntax.CallExpr)
			if !ok {
				return true
			}
			index, ok := collected[call]
			if !ok {
				return true
			}
			cmd := &s.Commands[index]
			for _, redir := range n.Redirs {
				op := redir.Op.String()
				if redir.Hdoc != nil && isShellInterpreter(cmd.Name) {
					// A heredoc fed to a shell is a nested script.
					if body, ok := StaticWord(redir.Hdoc); ok {
						nested = append(nested, body)
					}
				}
				target, static := StaticWord(redir.Word)
				cmd.Redirects = append(cmd.Redirects, Redirect{Op: op, Target: target, Dynamic: !static})
			}
		case *syntax.BinaryCmd:
			if n.Op != syntax.Pipe && n.Op != syntax.PipeAll {
				return true
			}
			producers := callNamesUnder(n.X, collected, s.Commands)
			for _, index := range callIndexesDirectlyUnder(n.Y, collected) {
				s.Commands[index].FedBy = append(s.Commands[index].FedBy, producers...)
			}
		case *syntax.CallExpr:
			index, ok := collected[n]
			if !ok {
				return true
			}
			// A process substitution argument feeds the command the same way a
			// pipe does: `bash <(curl url)`.
			for _, word := range n.Args {
				for _, part := range word.Parts {
					proc, ok := part.(*syntax.ProcSubst)
					if !ok {
						continue
					}
					for _, stmt := range proc.Stmts {
						s.Commands[index].FedBy = append(s.Commands[index].FedBy, callNamesUnder(stmt, collected, s.Commands)...)
					}
				}
			}
		}
		return true
	})

	// `bash -c '...'` payloads are nested scripts too.
	for _, cmd := range s.Commands {
		if payload := shellDashCPayload(cmd); payload != "" {
			nested = append(nested, payload)
		}
	}
	for _, script := range nested {
		var inner Script
		if inner.parseInto(script, depth+1) == nil {
			s.Commands = append(s.Commands, inner.Commands...)
		}
	}
	return err
}

// shellDashCPayload returns the inline script passed to a shell via -c.
func shellDashCPayload(cmd Command) string {
	if !isShellInterpreter(cmd.Name) {
		return ""
	}
	for i, arg := range cmd.Args {
		if !strings.HasPrefix(arg, "-") || strings.HasPrefix(arg, "--") {
			continue
		}
		if strings.ContainsRune(strings.TrimLeft(arg, "-"), 'c') && i+1 < len(cmd.Args) {
			return cmd.Args[i+1]
		}
	}
	return ""
}

func callNamesUnder(node syntax.Node, collected map[*syntax.CallExpr]int, commands []Command) []string {
	var out []string
	syntax.Walk(node, func(n syntax.Node) bool {
		call, ok := n.(*syntax.CallExpr)
		if !ok {
			return true
		}
		if index, ok := collected[call]; ok && commands[index].Name != "" {
			out = append(out, commands[index].Name)
		}
		return true
	})
	return out
}

// callIndexesDirectlyUnder returns the commands of node's own pipeline stage,
// without descending into a nested pipeline on the right-hand side.
func callIndexesDirectlyUnder(node syntax.Node, collected map[*syntax.CallExpr]int) []int {
	var out []int
	syntax.Walk(node, func(n syntax.Node) bool {
		call, ok := n.(*syntax.CallExpr)
		if !ok {
			return true
		}
		if index, ok := collected[call]; ok {
			out = append(out, index)
		}
		return true
	})
	return out
}

// UnwrapCommand strips wrapper prefixes that do not change what is ultimately
// executed and returns the normalized base name of the real program, the
// remaining arguments, and whether a privilege escalation prefix was stripped.
func UnwrapCommand(args []string) (string, []string, bool) {
	privileged := false
	for len(args) > 0 {
		name := normalizeProgramName(args[0])
		switch name {
		case "sudo", "doas":
			privileged = true
			args = skipPrefixFlags(args[1:], sudoValueFlags)
		case "command", "builtin", "noglob", "exec":
			args = args[1:]
		case "nohup", "setsid", "eatmydata":
			args = args[1:]
		case "env", "nice", "ionice", "stdbuf", "timeout", "xargs", "time":
			args = skipPrefixFlags(args[1:], wrapperValueFlags)
			// timeout takes a positional duration before the command; skip a
			// leading bare number so `timeout 5 rm -rf /` still resolves to rm.
			if name == "timeout" && len(args) > 0 && isNumericish(args[0]) {
				args = args[1:]
			}
		default:
			return name, args[1:], privileged
		}
	}
	return "", nil, privileged
}

// sudoValueFlags are the sudo/doas short flags that consume the next argument,
// so `sudo -u root rm -rf /` still resolves to rm rather than to root.
var sudoValueFlags = map[string]bool{
	"-u": true, "-U": true, "-g": true, "-p": true, "-C": true,
	"-h": true, "-r": true, "-t": true, "-T": true,
	"--user": true, "--group": true, "--prompt": true, "--host": true,
	"--role": true, "--type": true, "--close-from": true, "--command-timeout": true,
}

// wrapperValueFlags are the env/nice/stdbuf/xargs flags that consume the next
// argument.
var wrapperValueFlags = map[string]bool{
	"-n": true, "-u": true, "-I": true, "-L": true, "-P": true, "-d": true,
	"-o": true, "-e": true, "-i": true, "-c": true, "-k": true, "-s": true,
	"--adjustment": true, "--unset": true, "--max-procs": true, "--max-args": true,
	"--delimiter": true, "--output": true, "--input": true, "--error": true,
	"--replace": true, "--signal": true,
}

// skipPrefixFlags consumes the flags and VAR=value assignments a wrapper command
// takes before the program it runs.
func skipPrefixFlags(args []string, valueFlags map[string]bool) []string {
	for len(args) > 0 {
		arg := args[0]
		switch {
		case arg == "--":
			return args[1:]
		case strings.HasPrefix(arg, "-") && len(arg) > 1:
			args = args[1:]
			// `-u root` consumes its value; `-u=root` and bundled short flags do
			// not. Long flags with an inline `=` are self-contained.
			if strings.Contains(arg, "=") {
				continue
			}
			if valueFlags[arg] && len(args) > 0 {
				args = args[1:]
			}
		case strings.Contains(arg, "="):
			// A VAR=value assignment in front of the command (env, or the
			// implicit form `FOO=1 rm -rf /`).
			args = args[1:]
		default:
			return args
		}
	}
	return args
}

// normalizeProgramName reduces a program word to a comparable base name:
// `/bin/rm`, `\rm` and `"rm"` all become `rm`.
func normalizeProgramName(word string) string {
	word = strings.TrimSpace(word)
	// A leading backslash (or any backslash escape) only suppresses alias
	// expansion; it does not change which program runs.
	word = strings.ReplaceAll(word, `\`, "")
	if word == "" {
		return ""
	}
	return filepath.Base(word)
}

func isNumericish(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && r != '.' && r != 's' && r != 'm' && r != 'h' && r != 'd' {
			return false
		}
	}
	return true
}

func isShellInterpreter(name string) bool {
	switch name {
	case "sh", "bash", "dash", "zsh", "ksh", "fish", "ash", "busybox":
		return true
	default:
		return false
	}
}

// IsShellInterpreter reports whether name is a POSIX-style shell.
func IsShellInterpreter(name string) bool { return isShellInterpreter(name) }

func wordValues(printer *syntax.Printer, words []*syntax.Word) ([]string, bool) {
	out := make([]string, 0, len(words))
	dynamic := false
	for _, word := range words {
		if value, ok := StaticWord(word); ok {
			out = append(out, value)
			continue
		}
		dynamic = true
		out = append(out, bestEffortWord(printer, word))
	}
	return out, dynamic
}

// bestEffortWord resolves what it can and keeps the source text of the rest, so
// an expansion stays recognizable without its surrounding quotes: both `$HOME`
// and `"$HOME"` come back as `$HOME`.
func bestEffortWord(printer *syntax.Printer, word *syntax.Word) string {
	if word == nil {
		return ""
	}
	var b strings.Builder
	for _, part := range word.Parts {
		b.WriteString(bestEffortWordPart(printer, part))
	}
	return b.String()
}

func bestEffortWordPart(printer *syntax.Printer, part syntax.WordPart) string {
	switch p := part.(type) {
	case *syntax.Lit:
		return p.Value
	case *syntax.SglQuoted:
		return p.Value
	case *syntax.DblQuoted:
		var b strings.Builder
		for _, nested := range p.Parts {
			b.WriteString(bestEffortWordPart(printer, nested))
		}
		return b.String()
	default:
		var b strings.Builder
		if err := printer.Print(&b, part); err != nil {
			return ""
		}
		return b.String()
	}
}

// StaticWord resolves a word to its literal value, reporting false when the
// value depends on expansion.
func StaticWord(word *syntax.Word) (string, bool) {
	if word == nil {
		return "", false
	}
	var b strings.Builder
	for _, part := range word.Parts {
		value, ok := staticWordPart(part)
		if !ok {
			return "", false
		}
		b.WriteString(value)
	}
	return b.String(), true
}

func staticWordPart(part syntax.WordPart) (string, bool) {
	switch p := part.(type) {
	case *syntax.Lit:
		return p.Value, true
	case *syntax.SglQuoted:
		return p.Value, true
	case *syntax.DblQuoted:
		var b strings.Builder
		for _, nested := range p.Parts {
			value, ok := staticWordPart(nested)
			if !ok {
				return "", false
			}
			b.WriteString(value)
		}
		return b.String(), true
	default:
		return "", false
	}
}

// HasShortFlag reports whether args contain the given short flag letter, either
// standalone (`-f`) or bundled (`-rf`). Long flags are never matched, so
// HasShortFlag(args, 'r') does not fire on `--force`.
func HasShortFlag(args []string, letter rune) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if len(arg) < 2 || arg[0] != '-' || arg[1] == '-' {
			continue
		}
		if strings.ContainsRune(arg[1:], letter) {
			return true
		}
	}
	return false
}

// HasLongFlag reports whether args contain any of the given long flags, with or
// without an `=value` suffix.
func HasLongFlag(args []string, names ...string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		lower := strings.ToLower(arg)
		for _, name := range names {
			if lower == name || strings.HasPrefix(lower, name+"=") {
				return true
			}
		}
	}
	return false
}

// Operands returns the non-flag arguments, treating `--` as the end of flags.
func Operands(args []string) []string {
	out := make([]string, 0, len(args))
	stopFlags := false
	for _, arg := range args {
		if arg == "" {
			continue
		}
		if arg == "--" {
			stopFlags = true
			continue
		}
		if !stopFlags && strings.HasPrefix(arg, "-") {
			continue
		}
		out = append(out, arg)
	}
	return out
}

// FlagValue returns the value of a flag given either as `--name=value` or as
// `--name value` / `-n value`.
func FlagValue(args []string, names ...string) (string, bool) {
	for i, arg := range args {
		lower := strings.ToLower(arg)
		for _, name := range names {
			if lower == name {
				if i+1 < len(args) {
					return args[i+1], true
				}
				return "", true
			}
			if strings.HasPrefix(lower, name+"=") {
				return arg[len(name)+1:], true
			}
		}
	}
	return "", false
}

// String renders a command back to a readable form for error messages.
func (c Command) String() string {
	parts := make([]string, 0, len(c.Args)+2)
	if c.Privileged && c.Name != "sudo" && c.Name != "doas" {
		parts = append(parts, "sudo")
	}
	if c.Name == "" {
		parts = append(parts, "?")
	} else {
		parts = append(parts, c.Name)
	}
	parts = append(parts, c.Args...)
	return strings.Join(parts, " ")
}
