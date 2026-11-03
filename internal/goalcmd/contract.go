// Package goalcmd defines the user-facing Goal command contract shared by CLI
// dispatch and TUI discovery. It intentionally has no runtime dependencies.
package goalcmd

import "strings"

const (
	Name              = "goal"
	Summary           = "Run and supervise long-lived goal-mode sessions."
	HelpHintZH        = "输入 /goal help 查看中文用法"
	HelpDescriptionZH = "管理长期目标；" + HelpHintZH
	SlashDescription  = "Manage durable goals / " + HelpDescriptionZH
	SlashUsage        = "/goal start|status|inspect|list|logs|run|stop|resume|unlock|help"

	CommandStart   = "start"
	CommandStatus  = "status"
	CommandList    = "list"
	CommandLogs    = "logs"
	CommandRun     = "run"
	CommandStop    = "stop"
	CommandResume  = "resume"
	CommandUnlock  = "unlock"
	CommandHelp    = "help"
	AliasInspect   = "inspect"
	AliasList      = "ls"
	AliasLogs      = "log"
	AliasHelpShort = "-h"
	AliasHelpLong  = "--help"
)

type Command struct {
	Name    string
	Aliases []string
}

var commands = []Command{
	{Name: CommandStart},
	{Name: CommandStatus, Aliases: []string{AliasInspect}},
	{Name: CommandList, Aliases: []string{AliasList}},
	{Name: CommandLogs, Aliases: []string{AliasLogs}},
	{Name: CommandRun},
	{Name: CommandStop},
	{Name: CommandResume},
	{Name: CommandUnlock},
	{Name: CommandHelp, Aliases: []string{AliasHelpShort, AliasHelpLong}},
}

func Commands() []Command {
	out := make([]Command, len(commands))
	for i, command := range commands {
		out[i] = Command{
			Name:    command.Name,
			Aliases: append([]string(nil), command.Aliases...),
		}
	}
	return out
}

func Usage() string {
	return "goal start|status|inspect|list|logs|run|stop|resume|unlock|help"
}

func ValidCommandText() string {
	var names []string
	for _, command := range commands {
		names = append(names, command.Name)
		names = append(names, command.Aliases...)
	}
	return strings.Join(names, " ")
}

func HelpText() string {
	return `Usage:
  niuma goal start|status|inspect|list|logs|run|stop|resume|unlock|help
  /goal <command> [options]

中文说明:
  Goal 模式用于保存和持续推进长期目标，会跨轮次及进程重启保留目标、预算、检查点、计划、证据和状态。
  Goal 是命令，不是自然语言聊天前缀，也不是权限模式。TUI 用户输入 /goal help 可随时查看本帮助。

快速开始:
  goal start "检查项目测试并修复失败"
  goal run <goal-id> --background
  goal status <goal-id>
  goal logs <goal-id>

  goal start 只创建 active 状态的目标，不会开始执行。
  active 表示可运行，不表示正在运行；长期任务推荐使用 --background 在后台持续运行。

子命令:
  goal start "<目标>"             创建目标，但不开始执行
  goal status <goal-id>           查看状态、计划和最近证据（别名：inspect）
  goal list                       列出已保存的目标（别名：ls）
  goal logs <goal-id>             查看 Goal 生命周期事件（别名：log）
  goal run <goal-id> --once       只执行一轮
  goal run <goal-id> --background 在后台持续运行
  goal stop <goal-id>             停止目标
  goal resume <goal-id>           将 stopped 或 blocked 目标恢复状态为 active
  goal unlock <goal-id> --force   清除过期的本地执行锁
  goal help                       查看本帮助（别名：-h、--help）

  resume 只恢复状态，不会自动重启执行；恢复后请再次运行：
    goal resume <goal-id>
    goal run <goal-id> --background

  Goal 生命周期事件：goal logs <goal-id>
  后台任务输出：      logs <background-id>
  两种日志使用不同的 ID。

English reference:

Goal mode keeps a durable objective, budget, checkpoints, plan, evidence, and status across turns and process restarts.

Goal is a command, not a natural-language chat prefix or a permission mode.

Quick start:
  goal start "check project tests and fix failures"
  goal run <goal-id> --background
  goal status <goal-id>
  goal logs <goal-id>

goal start creates an active goal; it does not start execution.
active means runnable, not currently running. For long tasks, background execution is recommended.

Commands:
  goal status <goal-id>          Show status, plan, and recent evidence (alias: inspect)
  goal list                      List saved goals (alias: ls)
  goal logs <goal-id>            Show Goal lifecycle events (alias: log)
  goal run <goal-id> --once      Execute exactly one Goal turn
  goal run <goal-id> --background
                                 Run continuously in a background job
  goal stop <goal-id>            Move the Goal to stopped
  goal resume <goal-id>          Move a stopped or blocked Goal back to active
  goal unlock <goal-id> --force  Clear a stale local execution lock

Resume changes state but does not restart execution; run it again:
  goal resume <goal-id>
  goal run <goal-id> --background

Goal events use:        goal logs <goal-id>
Background output uses: logs <background-id>

TUI users can use the same commands with a leading slash, for example /goal help.`
}
