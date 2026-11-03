package query

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/konglong87/super-niuma/internal/gitpolicy"
	"github.com/konglong87/super-niuma/internal/shellcmd"
)

type shellIntentKind string

const (
	shellIntentUnknown                      shellIntentKind = "unknown"
	shellIntentReadOnly                     shellIntentKind = "readonly"
	shellIntentWorkspaceWrite               shellIntentKind = "workspace_write"
	shellIntentVerification                 shellIntentKind = "verification"
	shellIntentGitCommit                    shellIntentKind = "git_commit"
	shellIntentGitPush                      shellIntentKind = "git_push"
	shellIntentGitTagRead                   shellIntentKind = "git_tag_read"
	shellIntentGitTagMutate                 shellIntentKind = "git_tag_mutate"
	shellIntentGitDestructiveSharedState    shellIntentKind = "git_destructive_shared_state"
	shellIntentExternalWrite                shellIntentKind = "external_write"
	shellIntentExternalWriteDryRunOrPreview shellIntentKind = "external_write_dry_run_or_preview"
	shellIntentUnknownRisky                 shellIntentKind = "unknown_risky"
)

type shellIntent struct {
	Kind        shellIntentKind
	ReadOnly    bool
	Mutates     bool
	Destructive bool
	External    bool
	DryRun      bool
}

func classifyShellIntent(command string) shellIntent {
	lower := strings.ToLower(strings.TrimSpace(command))
	if lower == "" {
		return shellIntent{Kind: shellIntentUnknown}
	}
	git := gitpolicy.Analyze(command)
	if gitAnalysisIsDestructive(git) {
		return shellIntent{
			Kind:        shellIntentGitDestructiveSharedState,
			Mutates:     true,
			Destructive: true,
		}
	}
	if git.Has(gitpolicy.OperationPush) {
		return shellIntent{Kind: shellIntentGitPush, Mutates: true}
	}
	if git.Has(gitpolicy.OperationTag) {
		return shellIntent{Kind: shellIntentGitTagMutate, Mutates: true}
	}
	if looksLikeReadOnlyGitTagCommand(command) {
		return shellIntent{Kind: shellIntentGitTagRead, ReadOnly: true}
	}
	if git.Has(gitpolicy.OperationCommit) {
		return shellIntent{Kind: shellIntentGitCommit, Mutates: true}
	}
	if looksLikeExternalWriteCommand(command) {
		dryRun := shellCommandHasDryRunOrPreview(command)
		if dryRun {
			return shellIntent{Kind: shellIntentExternalWriteDryRunOrPreview, ReadOnly: true, External: true, DryRun: true}
		}
		return shellIntent{Kind: shellIntentExternalWrite, Mutates: true, External: true}
	}
	if looksLikeWriteLikeBashCommandString(command) {
		return shellIntent{Kind: shellIntentWorkspaceWrite, Mutates: true}
	}
	if looksLikeVerificationCommandString(command) {
		return shellIntent{Kind: shellIntentVerification, ReadOnly: true}
	}
	if looksLikeReadOnlyBashCommandString(command) {
		return shellIntent{Kind: shellIntentReadOnly, ReadOnly: true}
	}
	if looksLikeUnknownRiskyShellCommand(command) {
		return shellIntent{Kind: shellIntentUnknownRisky}
	}
	return shellIntent{Kind: shellIntentUnknown}
}

func looksLikeReadOnlyGitTagCommand(command string) bool {
	if gitpolicy.Analyze(command).Has(gitpolicy.OperationTag) {
		return false
	}
	script, _ := shellcmd.Parse(command)
	for _, parsed := range script.Commands {
		if parsed.Name != "git" {
			continue
		}
		subcommand, _, ok := gitpolicy.SplitSubcommand(parsed.Args)
		if ok && subcommand == "tag" {
			return true
		}
	}
	return false
}

func gitAnalysisIsDestructive(analysis gitpolicy.Analysis) bool {
	for _, effect := range analysis.Effects {
		if effect.Destructive {
			return true
		}
	}
	return false
}

func shellCommandHasDryRunOrPreview(command string) bool {
	lower := strings.ToLower(command)
	return strings.Contains(lower, "--dry-run") ||
		strings.Contains(lower, " dry-run") ||
		strings.Contains(lower, "dry run") ||
		strings.Contains(lower, "preview") ||
		strings.Contains(lower, "--check") ||
		strings.Contains(lower, " plan")
}

func looksLikeWriteLikeBashCommandString(command string) bool {
	return looksLikeWriteLikeBashCommand(json.RawMessage(fmt.Sprintf(`{"command":%q}`, command)))
}

func looksLikeVerificationCommandString(command string) bool {
	return looksLikeVerificationCommand(json.RawMessage(fmt.Sprintf(`{"command":%q}`, command)))
}

func looksLikeUnknownRiskyShellCommand(command string) bool {
	lower := strings.ToLower(strings.TrimSpace(command))
	if lower == "" {
		return false
	}
	fields := strings.Fields(lower)
	if len(fields) == 0 {
		return false
	}
	first := fields[0]
	if strings.HasPrefix(first, "./") || strings.HasPrefix(first, "../") || strings.HasSuffix(first, ".sh") {
		return true
	}
	if (first == "bash" || first == "sh" || first == "zsh") && len(fields) > 1 {
		if fields[1] == "-n" || fields[1] == "-c" && strings.Contains(lower, "git status") {
			return false
		}
		return true
	}
	return false
}
