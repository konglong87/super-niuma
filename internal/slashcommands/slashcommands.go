package slashcommands

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/goalcmd"
	"github.com/konglong87/super-niuma/internal/identity"
	"github.com/konglong87/super-niuma/internal/skills"
)

type Command struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Source      string `json:"source,omitempty"`
}

type DiscoveryOptions struct {
	ExplicitRoots []string
	Bare          bool
}

func List(cwd, prefix string) ([]Command, error) {
	return ListWithOptions(cwd, prefix, DiscoveryOptions{})
}

func ListWithOptions(cwd, prefix string, options DiscoveryOptions) ([]Command, error) {
	prefix = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(prefix), "/"))
	seen := map[string]Command{}
	loadedSettings := config.LoadSettings(cwd)
	id := identity.FromSettings(config.IdentitySettings(loadedSettings.Settings))
	for _, command := range BuiltinsForIdentity(id) {
		if options.Bare && !bareBuiltinCommands[strings.ToLower(command.Name)] {
			continue
		}
		seen[strings.ToLower(command.Name)] = command
	}
	skillOptions := skills.DiscoveryOptions{}
	if options.Bare {
		skillOptions = skills.DiscoveryOptions{ExplicitRoots: options.ExplicitRoots}
		if len(options.ExplicitRoots) == 0 {
			skillOptions.BundledOnly = true
		}
	}
	list, err := skills.ListWithOptions(cwd, skillOptions)
	if err != nil {
		return nil, err
	}
	for _, item := range list {
		if !item.UserInvocable {
			continue
		}
		source := "skill"
		if item.Legacy {
			source = "custom"
		} else if strings.TrimSpace(item.Plugin) != "" {
			source = "plugin skill"
		} else if strings.TrimSpace(string(item.Source)) != "" {
			source = string(item.Source)
		}
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		seen[strings.ToLower(name)] = Command{
			Name:        name,
			Description: item.Description,
			Source:      source,
		}
		if strings.TrimSpace(item.Plugin) != "" {
			localName := strings.TrimSpace(item.LocalName)
			if localName != "" && localName != name {
				seen[strings.ToLower(localName)] = Command{
					Name:        localName,
					Description: item.Description,
					Source:      source,
				}
			}
		}
	}
	out := make([]Command, 0, len(seen))
	for _, command := range seen {
		name := strings.TrimPrefix(strings.ToLower(command.Name), "/")
		if prefix == "" || strings.HasPrefix(name, prefix) {
			out = append(out, command)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		left := sortKey(out[i])
		right := sortKey(out[j])
		if left == right {
			return out[i].Name < out[j].Name
		}
		return left < right
	})
	return out, nil
}

var bareBuiltinCommands = map[string]bool{
	"help": true, "clear": true, "status": true, "tools": true,
	"sessions": true, "resume": true, "model": true, "permissions": true,
	"usage": true, "compact": true, "rewind": true, "checkpoint": true,
	"branches": true, "redo": true, "ps": true, "logs": true,
	"attach": true, "exit": true, "quit": true,
	"thinking": true,
}

func BareBuiltinAllowed(name string) bool {
	return bareBuiltinCommands[strings.ToLower(strings.TrimSpace(name))]
}

func Builtins() []Command {
	return BuiltinsForIdentity(identity.Default())
}

func BuiltinsForIdentity(id identity.Identity) []Command {
	return []Command{
		{Name: "help", Description: "Show slash command help", Source: "builtin"},
		{Name: "clear", Description: "Clear the terminal screen", Source: "builtin"},
		{Name: "init", Description: "Initialize " + id.GuidanceFilename + " with project guidance", Source: "builtin"},
		{Name: "status", Description: "Show current project and runtime status", Source: "builtin"},
		{Name: "tools", Description: "List available tools", Source: "builtin"},
		{Name: "sessions", Description: "List and manage sessions", Source: "builtin"},
		{Name: "resume", Description: "Resume a previous session", Source: "builtin"},
		{Name: "agent-tasks", Description: "List, inspect, or cancel agent tasks", Source: "builtin"},
		{Name: "mcp", Description: "Manage MCP servers, prompts, resources, and tools", Source: "builtin"},
		{Name: "skills", Description: "List, show, install, and inspect skills", Source: "builtin"},
		{Name: "plugins", Description: "List, show, install, and remove plugins", Source: "builtin"},
		{Name: "model", Description: "Get, set, or list model configuration", Source: "builtin"},
		{Name: "permissions", Description: "Inspect or update permission rules", Source: "builtin"},
		{Name: "hooks", Description: "Inspect or update hook commands", Source: "builtin"},
		{Name: "usage", Description: "Show token and cost usage", Source: "builtin"},
		{Name: "thinking", Description: "Collapse, expand, or hide TUI Thinking details", Source: "builtin"},
		{Name: "diff", Description: "Show git diff for the current worktree", Source: "builtin"},
		{Name: "branch", Description: "Show current git branch", Source: "builtin"},
		{Name: "review", Description: "Run a code review prompt", Source: "builtin"},
		{Name: "loop", Description: "Run a prompt or slash command on a recurring interval", Source: "builtin"},
		{Name: goalcmd.Name, Description: goalcmd.SlashDescription, Source: "builtin"},
		{Name: "compact", Description: "Compact the current or selected session", Source: "builtin"},
		{Name: "recap", Description: "Generate or show the session recap", Source: "builtin"},
		{Name: "rewind", Description: "Restore code and/or conversation to a previous user message", Source: "builtin"},
		{Name: "checkpoint", Description: "Alias for rewind", Source: "builtin"},
		{Name: "branches", Description: "List or compare branches of a v2 message-graph session", Source: "builtin"},
		{Name: "redo", Description: "Switch back to a previously abandoned branch", Source: "builtin"},
		{Name: "ps", Description: "List background sessions", Source: "builtin"},
		{Name: "logs", Description: "Show background session logs", Source: "builtin"},
		{Name: "attach", Description: "Attach to a background session", Source: "builtin"},
		{Name: "exit", Description: "Exit the TUI", Source: "builtin"},
		{Name: "quit", Description: "Exit the TUI", Source: "builtin"},
	}
}

func ResolvePrompt(ctx context.Context, cwd, input string) (string, bool, error) {
	return ResolvePromptWithOptions(ctx, cwd, input, DiscoveryOptions{})
}

func ResolvePromptWithOptions(ctx context.Context, cwd, input string, options DiscoveryOptions) (string, bool, error) {
	_ = ctx
	name, args, ok := ParseInvocation(input)
	if !ok {
		return "", false, nil
	}
	loadedSettings := config.LoadSettings(cwd)
	id := identity.FromSettings(config.IdentitySettings(loadedSettings.Settings))
	if strings.EqualFold(name, "init") {
		return InitPrompt(id, config.InitDetailLevel(loadedSettings.Settings)), true, nil
	}
	skillOptions := skills.DiscoveryOptions{}
	if options.Bare {
		skillOptions = skills.DiscoveryOptions{ExplicitRoots: options.ExplicitRoots}
		if len(options.ExplicitRoots) == 0 {
			skillOptions.BundledOnly = true
		}
	}
	skill, found, err := skills.LoadWithOptions(cwd, name, skillOptions)
	if err != nil || !found {
		return "", found, err
	}
	if !skill.UserInvocable {
		return "", false, fmt.Errorf("slash command /%s is not user-invocable", name)
	}
	content := SlashCommandContent(skill.Content, args)
	var b strings.Builder
	b.WriteString("The user invoked a niuma slash command.\n\n")
	b.WriteString("Command: /")
	b.WriteString(name)
	if strings.TrimSpace(args) != "" {
		b.WriteString("\nArguments: ")
		b.WriteString(args)
	}
	if strings.TrimSpace(skill.Description) != "" {
		b.WriteString("\nDescription: ")
		b.WriteString(skill.Description)
	}
	if skill.Legacy {
		b.WriteString("\nSource: legacy .claude/commands markdown command")
	} else {
		b.WriteString("\nSource: skill")
	}
	b.WriteString("\n\nFollow these command instructions exactly:\n\n")
	b.WriteString(content)
	return b.String(), true, nil
}

func ParseInvocation(input string) (name string, args string, ok bool) {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "/") {
		return "", "", false
	}
	body := strings.TrimSpace(strings.TrimPrefix(trimmed, "/"))
	if body == "" {
		return "", "", false
	}
	name = body
	if idx := strings.IndexFunc(body, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' }); idx >= 0 {
		name = body[:idx]
		args = strings.TrimSpace(body[idx:])
	}
	return name, args, name != ""
}

func SlashCommandContent(content, args string) string {
	content = strings.TrimSpace(StripFrontmatter(content))
	args = strings.TrimSpace(args)
	if args == "" {
		return content
	}
	replacer := strings.NewReplacer(
		"$ARGUMENTS", args,
		"{{ARGUMENTS}}", args,
		"{{arguments}}", args,
	)
	return replacer.Replace(content)
}

func StripFrontmatter(content string) string {
	normalized := strings.ReplaceAll(strings.TrimPrefix(content, "\ufeff"), "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return content
	}
	rest := strings.TrimPrefix(normalized, "---\n")
	if end := strings.Index(rest, "\n---\n"); end >= 0 {
		return rest[end+len("\n---\n"):]
	}
	if strings.HasSuffix(rest, "\n---") {
		return ""
	}
	return content
}

func InitPrompt(id identity.Identity, detailLevel string) string {
	return strings.Join([]string{
		"Analyze this repository and create or improve `" + id.GuidanceFilename + "` for future " + id.ProductName + " sessions in this workspace.",
		"",
		"Requirements:",
		"1. Read the existing repository evidence first: README, manifest files, build/test/lint configuration, CI configuration, existing `" + id.GuidanceFilename + "`, `" + id.LegacyGuidanceFile + "`, `" + id.WorkflowFallbackFile + "`, `.claude/rules/`, `.cursor/rules/`, `.cursorrules`, and `.github/copilot-instructions.md` when present.",
		"2. Write a concise agent operating guide, not a README summary. Include only durable instructions that future " + id.ProductName + " sessions need to avoid mistakes: project-specific working rules, non-obvious commands and verification gates, architecture constraints, safety/permissions/data/compatibility boundaries, and unusual coding or testing conventions.",
		"3. Detail level: " + initDetailRequirement(detailLevel),
		"4. Do not mirror source code, list every file or function, include generic development advice, copy obvious commands directly discoverable from manifests without context, or invent sections.",
		"5. If `" + id.GuidanceFilename + "` already exists, read it first and make the smallest useful improvement instead of overwriting it blindly.",
		"6. If `" + id.GuidanceFilename + "` does not exist but `" + id.LegacyGuidanceFile + "` exists, preserve only applicable project guidance in the new `" + id.GuidanceFilename + "`; do not create or restore any older product-specific guidance filename.",
		"7. Prefer this section shape when useful: Project Goal, Working Rules, Key Entrypoints, Commands, Verification Gate, Safety / Permissions, Known Boundaries. Omit sections that would be empty or generic.",
		"8. Keep the result concise. Prefer references such as `@docs/path.md` for long or frequently changing material.",
		"",
		"Start the file with exactly this header:",
		"",
		"```md",
		"# " + id.GuidanceFilename,
		"",
		"This file provides guidance to " + id.ProductName + " when working in this repository.",
		"```",
	}, "\n")
}

func initDetailRequirement(detailLevel string) string {
	switch strings.ToLower(strings.TrimSpace(detailLevel)) {
	case "minimal":
		return "`minimal` - do not add Key Entrypoints or type/function summaries; focus on working rules, non-obvious commands, verification gates, and known boundaries."
	case "detailed":
		return "`detailed` - include a compact Core API surface when the project has stable public types, handlers, commands, exported functions, or module relationships that future agents are likely to misunderstand; still do not list every function or mirror source code."
	default:
		return "`balanced` - include a short Key Entrypoints or Core API surface section only when stable entrypoints encode behavior future agents may otherwise misunderstand. Keep each item to path, responsibility, and non-obvious behavior."
	}
}

func sortKey(command Command) string {
	sourceRank := "2"
	switch command.Source {
	case "builtin":
		sourceRank = "0"
	case "custom":
		sourceRank = "1"
	}
	return sourceRank + ":" + strings.ToLower(command.Name)
}
