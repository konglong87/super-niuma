package sandbox

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/konglong87/super-niuma/internal/shellcmd"
	"github.com/konglong87/super-niuma/internal/tools"
	"mvdan.cc/sh/v3/syntax"
)

// CheckShellCommand validates the write targets a shell command names, using the
// same path policy as the file-editing tools.
//
// cfg is required rather than optional on purpose. This check used to call
// tools.EnsureWritablePath, which enforces the workspace boundary but none of the
// default write protection, so Bash and PowerShell could write
// .niuma/settings.json and .git/hooks while Write/Edit refused — a
// self-privilege-escalation and persistence path through the one tool that is
// hardest to constrain. Threading cfg through makes the protected call the only
// call available.
func CheckShellCommand(cwd string, writableRoots []string, command string, cfg tools.SandboxConfig) error {
	return checkShellCommand(cwd, writableRoots, command, cfg, 0)
}

func CheckShellNetworkPolicy(command string, cfg tools.SandboxConfig) error {
	if !cfg.NetworkDisabled && !cfg.NetworkProxyRequired && !cfg.NetworkMITMRequired {
		return nil
	}
	file, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return fmt.Errorf("parse shell command: %w", err)
	}
	var firstErr error
	syntax.Walk(file, func(node syntax.Node) bool {
		if node == nil || firstErr != nil {
			return firstErr == nil
		}
		call, ok := node.(*syntax.CallExpr)
		if !ok {
			return true
		}
		args := literalArgs(call.Args)
		name, rest := unwrapCommand(args)
		if name == "" {
			return true
		}
		if cfg.NetworkDisabled && shellNetworkCommand(name) {
			firstErr = fmt.Errorf("network command %s is blocked because sandbox.network.disabled is true", name)
			return false
		}
		if cfg.NetworkProxyRequired {
			if shellRawNetworkCommand(name) {
				firstErr = fmt.Errorf("network command %s is blocked because sandbox.network.proxy.required requires proxy-aware HTTP(S) tooling", name)
				return false
			}
			if proxyBypassFlag(name, rest) {
				firstErr = fmt.Errorf("network command %s disables proxy settings under sandbox.network.proxy.required", name)
				return false
			}
		}
		if cfg.NetworkMITMRequired && tlsBypassFlag(name, rest) {
			firstErr = fmt.Errorf("network command %s disables TLS verification under sandbox.network.mitm.required", name)
			return false
		}
		return true
	})
	return firstErr
}

func checkShellCommand(cwd string, writableRoots []string, command string, cfg tools.SandboxConfig, depth int) error {
	if depth > 4 {
		return fmt.Errorf("nested shell command depth exceeded")
	}
	file, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return fmt.Errorf("parse shell command: %w", err)
	}
	var firstErr error
	checkPath := func(path string) {
		if firstErr != nil || !isPathCandidate(path) {
			return
		}
		if isNullDevicePath(path) {
			return
		}
		resolved, err := tools.ResolvePath(cwd, path)
		if err != nil {
			firstErr = err
			return
		}
		if err := tools.EnsureWritablePathWithSandbox(cwd, writableRoots, resolved, cfg); err != nil {
			firstErr = err
		}
	}
	syntax.Walk(file, func(node syntax.Node) bool {
		if node == nil || firstErr != nil {
			return firstErr == nil
		}
		switch n := node.(type) {
		case *syntax.Redirect:
			if isWriteRedirect(n.Op.String()) && n.Word != nil {
				path, ok := shellcmd.StaticWord(n.Word)
				if !ok {
					firstErr = fmt.Errorf("dynamic write redirection is not allowed")
					return false
				}
				checkPath(path)
			}
		case *syntax.CallExpr:
			args := literalArgs(n.Args)
			restWords := []*syntax.Word(nil)
			if len(n.Args) > 1 {
				restWords = n.Args[1:]
			}
			if mutatingCommandName(args) != "" && hasDynamicShellWord(restWords) {
				firstErr = fmt.Errorf("dynamic path in mutating shell command is not allowed")
				return false
			}
			if nested := nestedShellCommand(args); nested != "" {
				if err := checkShellCommand(cwd, writableRoots, nested, cfg, depth+1); err != nil {
					firstErr = err
					return false
				}
			}
			for _, path := range mutatingCommandPaths(args) {
				checkPath(path)
			}
		}
		return firstErr == nil
	})
	return firstErr
}

func shellNetworkCommand(name string) bool {
	switch name {
	case "curl", "wget", "http", "httpie", "fetch", "nc", "ncat", "netcat", "socat", "telnet", "ssh", "scp", "sftp", "rsync", "git", "npm", "pnpm", "yarn", "pip", "pip3", "go":
		return true
	default:
		return false
	}
}

func shellRawNetworkCommand(name string) bool {
	switch name {
	case "nc", "ncat", "netcat", "socat", "telnet", "ssh", "scp", "sftp", "rsync":
		return true
	default:
		return false
	}
}

func proxyBypassFlag(name string, args []string) bool {
	for i, arg := range args {
		lower := strings.ToLower(strings.TrimSpace(arg))
		if lower == "" {
			continue
		}
		switch {
		case lower == "--noproxy" || strings.HasPrefix(lower, "--noproxy=") || lower == "--no-proxy" || strings.HasPrefix(lower, "--no-proxy="):
			return true
		case lower == "--proxy" || lower == "-x":
			if i+1 < len(args) && strings.TrimSpace(args[i+1]) == "" {
				return true
			}
		case lower == "--proxy=" || lower == "-x=":
			return true
		case name == "git" && (lower == "http.proxy=" || lower == "-c" && i+1 < len(args) && strings.EqualFold(args[i+1], "http.proxy=")):
			return true
		}
	}
	return false
}

func tlsBypassFlag(name string, args []string) bool {
	for i, arg := range args {
		lower := strings.ToLower(strings.TrimSpace(arg))
		switch {
		case name == "curl" && (lower == "-k" || lower == "--insecure"):
			return true
		case name == "wget" && lower == "--no-check-certificate":
			return true
		case name == "git" && (lower == "http.sslverify=false" || lower == "-c" && i+1 < len(args) && strings.EqualFold(args[i+1], "http.sslVerify=false")):
			return true
		case (name == "npm" || name == "pnpm" || name == "yarn") && strings.Contains(lower, "strict-ssl=false"):
			return true
		}
	}
	return false
}

func mutatingCommandName(args []string) string {
	name, _ := unwrapCommand(args)
	switch name {
	case "rm", "unlink", "rmdir", "mkdir", "touch", "install", "tee", "mv", "cp", "ln", "chmod", "chown", "chgrp", "sed":
		return name
	default:
		return ""
	}
}

func hasDynamicShellWord(words []*syntax.Word) bool {
	for _, word := range words {
		if _, ok := shellcmd.StaticWord(word); !ok {
			return true
		}
	}
	return false
}

func literalArgs(words []*syntax.Word) []string {
	out := make([]string, 0, len(words))
	for _, word := range words {
		value, ok := shellcmd.StaticWord(word)
		if !ok {
			value = word.Lit()
		}
		out = append(out, value)
	}
	return out
}

func isWriteRedirect(op string) bool {
	return strings.Contains(op, ">") || op == "<>"
}

func mutatingCommandPaths(args []string) []string {
	name, rest := unwrapCommand(args)
	if name == "" {
		return nil
	}
	paths := nonFlagArgs(rest)
	switch name {
	case "rm", "unlink", "rmdir", "mkdir", "touch", "install", "tee", "ln":
		return paths
	case "mv":
		return paths
	case "cp":
		if len(paths) == 0 {
			return nil
		}
		return []string{paths[len(paths)-1]}
	case "chmod", "chown", "chgrp":
		if len(paths) <= 1 {
			return nil
		}
		return paths[1:]
	case "sed":
		if !hasInPlaceFlag(rest) || len(paths) == 0 {
			return nil
		}
		return []string{paths[len(paths)-1]}
	default:
		return nil
	}
}

func nestedShellCommand(args []string) string {
	name, rest := unwrapCommand(args)
	switch name {
	case "sh", "bash", "dash", "zsh", "ksh":
	default:
		return ""
	}
	for i := 0; i < len(rest); i++ {
		arg := rest[i]
		if arg == "--" {
			continue
		}
		if !strings.HasPrefix(arg, "-") {
			continue
		}
		if strings.Contains(strings.TrimLeft(arg, "-"), "c") && i+1 < len(rest) {
			return rest[i+1]
		}
	}
	return ""
}

// unwrapCommand delegates to shellcmd so the sandbox strips the same wrapper
// prefixes as the risk classifier — previously it only knew sudo/env and let
// `nohup rm ...` or `timeout 5 rm ...` past the mutating-path checks.
func unwrapCommand(args []string) (string, []string) {
	name, rest, _ := shellcmd.UnwrapCommand(args)
	return name, rest
}

func nonFlagArgs(args []string) []string {
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

func hasInPlaceFlag(args []string) bool {
	for _, arg := range args {
		if arg == "-i" || strings.HasPrefix(arg, "-i") || arg == "--in-place" || strings.HasPrefix(arg, "--in-place=") {
			return true
		}
	}
	return false
}

func isPathCandidate(path string) bool {
	if path == "" || path == "-" {
		return false
	}
	if strings.ContainsAny(path, "$`") || strings.HasPrefix(path, "<(") || strings.HasPrefix(path, ">(") {
		return false
	}
	return true
}

func isNullDevicePath(path string) bool {
	return filepath.Clean(path) == "/dev/null"
}
