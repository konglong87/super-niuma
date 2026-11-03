package toolpolicy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/konglong87/super-niuma/internal/gitpolicy"
	"github.com/konglong87/super-niuma/internal/shellcmd"
)

type Decision struct {
	RuleID  string
	Message string
}

func Check(toolName string, input []byte, cwd string, authorization gitpolicy.Authorization) *Decision {
	if strings.EqualFold(toolName, "Bash") {
		command := bashCommand(input)
		if violation := gitpolicy.Check(authorization, gitpolicy.Analyze(command)); violation != nil {
			return gitViolationDecision(violation)
		}
		if candidate, conflict := numberedDirectoryConflict(cwd, bashDirectoryCandidates(command)); conflict != "" {
			return numberedDirectoryDecision(candidate, conflict)
		}
		return nil
	}
	if candidate, conflict := numberedDirectoryConflict(cwd, writeDirectoryCandidates(toolName, input)); conflict != "" {
		return numberedDirectoryDecision(candidate, conflict)
	}
	return nil
}

func gitViolationDecision(violation *gitpolicy.Violation) *Decision {
	switch violation.Kind {
	case gitpolicy.ViolationForceAdd:
		return &Decision{
			RuleID:  string(violation.Kind),
			Message: "Tool blocked by Force-Add Gate: " + violation.Reason + ". Use literal pathspecs and obtain explicit current-turn authorization for every path.",
		}
	case gitpolicy.ViolationDestructive:
		return &Decision{
			RuleID:  string(violation.Kind),
			Message: "Tool blocked by Destructive Shared-State Gate: " + violation.Reason + ". Ask for confirmation for the exact destructive operation and target.",
		}
	default:
		return &Decision{
			RuleID:  string(violation.Kind),
			Message: "Tool blocked by Shared-State Authorization Gate: " + violation.Reason + ". Do not retry this Git effect, change its author, message, remote, or ref, modify permission settings, or tell the user to run it manually. Placeholders such as <message> are literal values, not wildcards. Ask the user to authorize the exact intended command in a new message, then execute only that exact command.",
		}
	}
}

func bashCommand(input []byte) string {
	var params struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(input, &params); err == nil && strings.TrimSpace(params.Command) != "" {
		return params.Command
	}
	return string(input)
}

type directoryCandidate struct {
	Path          string
	IncludesFinal bool
}

func writeDirectoryCandidates(toolName string, input []byte) []directoryCandidate {
	switch strings.ToLower(toolName) {
	case "write", "edit", "multiedit", "notebookedit":
	default:
		return nil
	}
	var payload map[string]any
	if json.Unmarshal(input, &payload) != nil {
		return nil
	}
	var candidates []directoryCandidate
	for _, key := range []string{"file_path", "notebook_path", "path"} {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			candidates = append(candidates, directoryCandidate{Path: value})
		}
	}
	return candidates
}

func bashDirectoryCandidates(command string) []directoryCandidate {
	script, _ := shellcmd.Parse(command)
	var candidates []directoryCandidate
	for _, command := range script.Commands {
		if command.Name != "mkdir" {
			continue
		}
		for i := 0; i < len(command.Args); i++ {
			arg := command.Args[i]
			if arg == "-m" || arg == "--mode" {
				i++
				continue
			}
			if strings.HasPrefix(arg, "-") {
				continue
			}
			candidates = append(candidates, directoryCandidate{Path: arg, IncludesFinal: true})
		}
	}
	return candidates
}

var numberedDirectoryRE = regexp.MustCompile(`^([0-9]{2,})[-_]`)

func numberedDirectoryConflict(cwd string, candidates []directoryCandidate) (string, string) {
	for _, candidate := range candidates {
		path, conflict := findNumberedDirectoryConflict(cwd, candidate)
		if conflict != "" {
			return path, conflict
		}
	}
	return "", ""
}

func findNumberedDirectoryConflict(cwd string, candidate directoryCandidate) (string, string) {
	path := filepath.Clean(strings.TrimSpace(candidate.Path))
	if path == "." || path == "" {
		return "", ""
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	path = filepath.Clean(path)
	limit := path
	if !candidate.IncludesFinal {
		limit = filepath.Dir(path)
	}
	for current := limit; current != filepath.Dir(current); current = filepath.Dir(current) {
		if _, err := os.Lstat(current); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return "", ""
		}
		parent := filepath.Dir(current)
		conflict := conflictingNumberedSibling(parent, filepath.Base(current))
		if conflict != "" {
			return current, filepath.Join(parent, conflict)
		}
	}
	return "", ""
}

func conflictingNumberedSibling(parent, candidate string) string {
	match := numberedDirectoryRE.FindStringSubmatch(candidate)
	if len(match) != 2 {
		return ""
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		return ""
	}
	numberedCount := 0
	conflict := ""
	prefixes := make(map[string]bool)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		siblingMatch := numberedDirectoryRE.FindStringSubmatch(entry.Name())
		if len(siblingMatch) != 2 {
			continue
		}
		numberedCount++
		if prefixes[siblingMatch[1]] {
			// Existing duplicate prefixes prove that this parent does not enforce
			// a unique sequence, so the runtime must not invent that convention.
			return ""
		}
		prefixes[siblingMatch[1]] = true
		if siblingMatch[1] == match[1] && entry.Name() != candidate {
			conflict = entry.Name()
		}
	}
	if numberedCount < 2 {
		return ""
	}
	return conflict
}

func numberedDirectoryDecision(candidate, conflict string) *Decision {
	return &Decision{
		RuleID:  "numbered_directory_collision",
		Message: fmt.Sprintf("Tool blocked by Numbered Directory Gate: %s reuses the numeric prefix of existing sibling %s. Inspect the sibling directory sequence and choose an unused prefix.", candidate, conflict),
	}
}
