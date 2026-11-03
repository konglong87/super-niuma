package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/konglong87/super-niuma/internal/goalcmd"
	"github.com/konglong87/super-niuma/internal/product"
)

// binaryName 是 usage 行里印的可执行文件名。
const binaryName = product.BinaryName

// commandContext 把所有 handler 可能需要的东西打包，让 commandSpec.run
// 能有统一签名 —— 这是把 dispatch / help / completion 三份清单收敛成一份的前提。
type commandContext struct {
	ctx    context.Context
	name   string
	args   []string
	opts   options
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

// commandSpec 描述一个顶层子命令。
//
// 这张表是唯一事实来源：dispatch、`--help` 文本、shell completion 词表
// 全部从它派生。以前它们是三份手写清单，必然漂移（completion 曾经缺
// goal/goals/transcript/eval）。新增命令时只改这里。
type commandSpec struct {
	// name 是规范名，aliases 是等价写法。
	name    string
	aliases []string
	// summary 是一句话说明，出现在 `<cmd> --help` 里。
	summary string
	// usage 是 usage 行（不含 "niuma " 前缀），
	// 同时出现在全局 help 和 `<cmd> --help` 里。
	usage []string
	// hidden 的命令不进 help / completion，也不拦 --help：
	// 它们是进程内部自调用的入口，参数由调用方完全控制。
	hidden bool
	// structuredLogs 表示该命令保留服务端那套 JSON 结构化日志。
	// 其余命令走 CLI 安静模式（见 observability.NewCLILogger）。
	structuredLogs    bool
	skipStartupUpdate bool
	// help lets a complex command replace the generic renderer with shared guidance.
	help func(io.Writer)
	run  func(commandContext) error
}

func commandTable() []commandSpec {
	return []commandSpec{
		{
			name:    "auth",
			summary: "Inspect or change stored API credentials.",
			usage:   []string{"auth status|login|logout"},
			run:     func(c commandContext) error { return authCommand(c.args, c.stdout) },
		},
		{
			name:    "config",
			summary: "Read and write settings.json values.",
			usage:   []string{"config [--project|--local] list|get|set|unset"},
			run:     func(c commandContext) error { return configCommand(c.args, c.opts.cwd, c.stdout) },
		},
		{
			name:              "memory",
			summary:           "Check runtime guidance documents for broken local links.",
			usage:             []string{"memory lint [--scope workspace|all] [--json]"},
			skipStartupUpdate: true,
			run:               func(c commandContext) error { return memoryCommand(c.args, c.opts.cwd, c.stdout) },
		},
		{
			name:    "mcp",
			summary: "Manage MCP servers and call their tools, resources and prompts.",
			usage:   []string{"mcp list|show|add|remove|serve|tools|call|resources|resource|prompts|prompt"},
			run: func(c commandContext) error {
				if len(c.args) > 0 && c.args[0] == "serve" {
					return mcpServeCommand(c.ctx, c.args[1:], c.opts, c.stdin, c.stdout)
				}
				return mcpCommand(c.ctx, c.args, c.opts.cwd, c.stdout)
			},
		},
		{
			name:    "session",
			aliases: []string{"sessions"},
			summary: "Inspect and manage saved session transcripts.",
			usage:   []string{"session list [--source local|tenant|all]|create|get <tenant:key|local:id>|send <tenant:key> <text>|stop <tenant:key>|attach <tenant:target> --from <ref>|monitor <tenant:key> --every <duration> --channel feishu|locate|show|inspect|search|rename|delete|clear|checkpoint|rewind|branches|redo|fork|gc"},
			run: func(c commandContext) error {
				return sessionCommandWithContext(withSessionControlCLIOptions(c.ctx, c.opts), c.args, c.stdout)
			},
		},
		{
			name:    "transcript",
			summary: "Import transcripts recorded by other Claude Code runtimes.",
			usage:   []string{"transcript import-claude-code <path> [--cwd <dir>]"},
			run:     func(c commandContext) error { return transcriptCommand(c.args, c.opts.cwd, c.stdout) },
		},
		{
			name:    goalcmd.Name,
			aliases: []string{"goals"},
			summary: goalcmd.Summary,
			usage:   []string{goalcmd.Usage()},
			help: func(w io.Writer) {
				fmt.Fprintln(w, goalcmd.HelpText())
			},
			run: func(c commandContext) error { return goalCommand(c.ctx, c.args, c.opts, c.stdout, c.stderr) },
		},
		{
			name:    "skills",
			summary: "List, lint, install and package skills.",
			usage:   []string{"skills list|show|lint|install|sync|marketplace-search|marketplace-info|marketplace-status|context|feedback|watch|validate-bundled|package"},
			run:     func(c commandContext) error { return skillsCommand(c.ctx, c.args, c.opts.cwd, c.stdout) },
		},
		{
			name:    "plugin",
			aliases: []string{"plugins"},
			summary: "List and install plugins.",
			usage:   []string{"plugin list|show|install|remove"},
			run:     func(c commandContext) error { return pluginsCommand(c.args, c.opts.cwd, c.stdout) },
		},
		{
			name:           "server",
			summary:        "Start the local HTTP server.",
			usage:          []string{"server --port <port>"},
			structuredLogs: true,
			run:            func(c commandContext) error { return serverCommand(c.ctx, c.args, c.opts, c.stdout) },
		},
		{
			name:    "channels",
			summary: "Onboard Feishu and run the independent channel worker.",
			usage:   []string{"channels onboard feishu|run"},
			run:     func(c commandContext) error { return channelsCommand(c.ctx, c.args, c.opts, c.stdout) },
		},
		{
			name:              "image-worker",
			summary:           "Run the independent durable image generation worker.",
			usage:             []string{"image-worker run"},
			structuredLogs:    true,
			skipStartupUpdate: true,
			run:               func(c commandContext) error { return imageWorkerCommand(c.ctx, c.args, c.opts, c.stdout) },
		},
		{
			name:    "tenant",
			summary: "Run tenant migrations and manage tenant-scoped agent tasks and skill packages.",
			usage: []string{
				"tenant migrate up|down|version --dsn <mysql_dsn>",
				"tenant agent-tasks list|events|cancel",
				"tenant skill-package import|render|publish|list|history|rollback",
			},
			run: func(c commandContext) error { return tenantCommand(c.ctx, c.args, c.stdout) },
		},
		{
			name:    "usage",
			aliases: []string{"cost"},
			summary: "Show token usage and cost for recorded sessions.",
			usage:   []string{"usage"},
			run:     func(c commandContext) error { return usageCommand(c.opts.cwd, c.stdout) },
		},
		{
			name:    "init",
			summary: "Scaffold CLAUDE.md and optional project settings.",
			usage:   []string{"init [--settings]"},
			run:     func(c commandContext) error { return initCommand(c.args, c.opts.cwd, c.stdout) },
		},
		{
			name:    "status",
			summary: "Print the resolved configuration for the current directory.",
			usage:   []string{"status"},
			run:     func(c commandContext) error { return statusCommand(c.opts.cwd, c.stdout) },
		},
		{
			name:    "model",
			summary: "Show or change the default model.",
			usage:   []string{"model get|set|list"},
			run:     func(c commandContext) error { return modelCommand(c.args, c.opts.cwd, c.stdout) },
		},
		{
			name:    "image",
			summary: "Generate or edit images with the configured image provider.",
			usage:   []string{"image generate|edit --prompt <text> [--image <path>] [--out <path>]"},
			run:     func(c commandContext) error { return imageCommand(c.args, c.opts.cwd, c.stdout) },
		},
		{
			name:    "permissions",
			summary: "Show or edit the global permission policy.",
			usage:   []string{"permissions list|mode|allow|deny|remove|clear"},
			run:     func(c commandContext) error { return permissionsCommand(c.args, c.stdout) },
		},
		{
			name:    "export",
			summary: "Export a session transcript to Markdown or JSON.",
			usage:   []string{"export <session_id> <output_file> [--format markdown|json]"},
			run:     func(c commandContext) error { return exportCommand(c.args, c.stdout) },
		},
		{
			name:    "diff",
			summary: "Show the working tree diff.",
			usage:   []string{"diff [--stat]"},
			run:     func(c commandContext) error { return diffCommand(c.ctx, c.args, c.opts.cwd, c.stdout) },
		},
		{
			name:    "branch",
			summary: "Show the current git branch.",
			usage:   []string{"branch"},
			run:     func(c commandContext) error { return branchCommand(c.ctx, c.opts.cwd, c.stdout) },
		},
		{
			name:    "review",
			summary: "Ask the model to review the current diff.",
			usage:   []string{"review [--staged]"},
			run:     func(c commandContext) error { return reviewCommand(c.ctx, c.args, c.opts, c.stdout, c.stderr) },
		},
		{
			name:    "ps",
			summary: "List background sessions.",
			usage:   []string{"ps"},
			run:     func(c commandContext) error { return backgroundListCommand(c.stdout) },
		},
		{
			name:    "logs",
			summary: "Print the output of a background session.",
			usage:   []string{"logs <background_id>"},
			run:     func(c commandContext) error { return backgroundLogsCommand(c.args, c.stdout) },
		},
		{
			name:    "kill",
			summary: "Stop a background session.",
			usage:   []string{"kill <background_id>"},
			run:     func(c commandContext) error { return backgroundKillCommand(c.args, c.stdout) },
		},
		{
			name:    "attach",
			summary: "Follow a background session's output.",
			usage:   []string{"attach <background_id> [--wait]"},
			run:     func(c commandContext) error { return backgroundAttachCommand(c.args, c.stdout) },
		},
		{
			name:    "completion",
			summary: "Print a shell completion script.",
			usage:   []string{"completion <bash|zsh|fish>"},
			run:     func(c commandContext) error { return completionCommand(c.args, c.stdout) },
		},
		{
			name:    "tools",
			summary: "List the tool definitions sent to the model.",
			usage:   []string{"tools"},
			run:     func(c commandContext) error { return toolsCommand(c.ctx, c.opts, c.stdout) },
		},
		{
			name:    "hooks",
			summary: "Show or edit configured hooks.",
			usage:   []string{"hooks list|add|remove|clear"},
			run:     func(c commandContext) error { return hooksCommand(c.args, c.stdout) },
		},
		{
			name:    "agents",
			summary: "List and validate agents defined under .claude/agents.",
			usage:   []string{"agents list|show|doctor"},
			run:     func(c commandContext) error { return agentsCommand(c.args, c.opts.cwd, c.stdout) },
		},
		{
			name:    "eval",
			summary: "Run the agent evaluation suites.",
			usage:   []string{"eval agents [--profile local|live|live-agent-api|anthropic-thinking|golden-probe] [--suite default|golden] [--dataset file] [--runs N] [--provider name] [--output report.json|report.md]"},
			run:     func(c commandContext) error { return evalCommand(c.ctx, c.args, c.opts, c.stdout) },
		},
		{
			name:    "compact",
			summary: "Compact a stored session transcript.",
			usage:   []string{"compact <session_id> [--max-bytes N]"},
			run:     func(c commandContext) error { return compactCommand(c.args, c.stdout) },
		},
		{
			name:    "doctor",
			summary: "Report configuration, credentials and runtime health as JSON.",
			usage:   []string{"doctor"},
			run:     func(c commandContext) error { return doctor(c.stdout) },
		},
		{
			name:    "help",
			summary: "Show global help.",
			usage:   []string{"help"},
			run: func(c commandContext) error {
				printHelp(c.stdout)
				return nil
			},
		},
		{
			name:    "version",
			summary: "Print the binary version.",
			usage:   []string{"version [--json]"},
			run: func(c commandContext) error {
				return versionCommand(c.args, c.stdout)
			},
		},
		{
			name:           "__background-run",
			hidden:         true,
			structuredLogs: true,
			run:            func(c commandContext) error { return backgroundRunCommand(c.ctx, c.args, c.stdout, c.stderr) },
		},
		{
			name:           "__goal-run",
			hidden:         true,
			structuredLogs: true,
			run: func(c commandContext) error {
				return goalBackgroundRunCommand(c.ctx, c.args, c.opts, c.stdout, c.stderr)
			},
		},
		{
			name:           "__loop-run",
			hidden:         true,
			structuredLogs: true,
			run:            func(c commandContext) error { return loopRunCommand(c.ctx, c.args, c.stdout, c.stderr) },
		},
		{
			name:           "__schedule-daemon",
			hidden:         true,
			structuredLogs: true,
			run:            func(c commandContext) error { return scheduleDaemonCommand(c.ctx, c.stdout, c.stderr) },
		},
		{
			name:           "__schedule-run",
			hidden:         true,
			structuredLogs: true,
			run:            func(c commandContext) error { return scheduleRunCommand(c.ctx, c.args, c.stdout, c.stderr) },
		},
	}
}

// lookupCommand 按规范名或别名查表。
func lookupCommand(name string) (commandSpec, bool) {
	for _, spec := range commandTable() {
		if spec.name == name {
			return spec, true
		}
		for _, alias := range spec.aliases {
			if alias == name {
				return spec, true
			}
		}
	}
	return commandSpec{}, false
}

// commandWords 返回 completion 词表：所有可见命令的规范名与别名，按表序。
func commandWords() []string {
	var words []string
	for _, spec := range commandTable() {
		if spec.hidden {
			continue
		}
		words = append(words, spec.name)
		words = append(words, spec.aliases...)
	}
	return words
}

// wantsCommandHelp 判断子命令参数里是否请求了帮助。
// 用户对任何子命令做最标准的反射动作（加 --help）都应该得到帮助而不是报错。
func wantsCommandHelp(args []string) bool {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			return true
		}
	}
	return false
}

func printCommandHelp(w io.Writer, spec commandSpec) {
	if spec.help != nil {
		spec.help(w)
		return
	}
	fmt.Fprintln(w, "Usage:")
	for _, line := range spec.usage {
		fmt.Fprintf(w, "  %s %s\n", binaryName, line)
	}
	if strings.TrimSpace(spec.summary) != "" {
		fmt.Fprintf(w, "\n%s\n", spec.summary)
	}
	if len(spec.aliases) > 0 {
		fmt.Fprintf(w, "\nAliases: %s\n", strings.Join(spec.aliases, ", "))
	}
	fmt.Fprintf(w, "\nRun \"%s --help\" for global options.\n", binaryName)
}
