// tool_summary.go 把工具的原始入参与输出压成一行人类可读摘要。

package tui

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

const (
	toolKeyOutputMaxLines  = 100
	toolKeyOutputMaxChars  = 12000
	toolKeyOutputFullLines = 1000
	toolKeyOutputFullChars = 100000
)

type toolResultDisplayMode uint8

const (
	toolResultDisplaySummary toolResultDisplayMode = iota
	toolResultDisplayKeyOutput
)

func toolResultDisplayModeFor(item toolActivityItem) toolResultDisplayMode {
	if !strings.EqualFold(strings.TrimSpace(item.ToolName), "bash") || item.Status == "running" {
		return toolResultDisplaySummary
	}
	if _, ok := gatePreflightSummary(item.RawOutput); ok {
		return toolResultDisplaySummary
	}
	command := bashCommandFromInput(item.RawInput)
	if isKeyOutputGitCommand(command) {
		return toolResultDisplayKeyOutput
	}
	return toolResultDisplaySummary
}

func bashCommandFromInput(raw string) string {
	var params struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(raw), &params); err == nil {
		return strings.TrimSpace(params.Command)
	}
	return strings.TrimSpace(raw)
}

func isKeyOutputGitCommand(command string) bool {
	command = strings.TrimSpace(command)
	if command == "" {
		return false
	}
	// Handle common shell prefixes without attempting to parse arbitrary shell.
	for {
		fields := strings.Fields(command)
		if len(fields) == 0 {
			return false
		}
		if strings.Contains(fields[0], "=") && !strings.HasPrefix(fields[0], "git") {
			command = strings.TrimSpace(strings.TrimPrefix(command, fields[0]))
			continue
		}
		if fields[0] == "cd" {
			if i := strings.Index(command, "&&"); i >= 0 {
				command = strings.TrimSpace(command[i+2:])
				continue
			}
		}
		if fields[0] != "git" {
			return false
		}
		for i := 1; i < len(fields); i++ {
			switch fields[i] {
			case "-C":
				i++
			case "--no-pager", "--paginate":
			default:
				return fields[i] == "diff" || fields[i] == "show" || fields[i] == "status" || fields[i] == "log"
			}
		}
		return false
	}
}

type toolActivityItem struct {
	ToolName       string
	ToolID         string
	Status         string
	Detail         string
	Result         string
	RawInput       string
	RawOutput      string
	GateRule       string
	GateTitle      string
	GateReason     string
	GateNextAction string
	Started        time.Time
	Elapsed        time.Duration
	IsError        bool
}

// toolInputSummary produces a semantic one-line summary of a tool's input parameters.
func toolInputSummary(toolName, rawInput string) string {
	if rawInput == "" {
		return ""
	}
	var params map[string]any
	if err := json.Unmarshal([]byte(rawInput), &params); err != nil {
		return truncate(oneLine(rawInput), 60)
	}
	switch toolName {
	case "Read":
		filePath, _ := params["file_path"].(string)
		if filePath == "" {
			return ""
		}
		base := filepath.Base(filePath)
		offset, _ := params["offset"].(float64)
		limit, _ := params["limit"].(float64)
		if offset > 0 && limit > 0 {
			return fmt.Sprintf("%s:%d-%d", base, int(offset), int(offset+limit-1))
		}
		if offset > 0 {
			return fmt.Sprintf("%s:%d", base, int(offset))
		}
		return base
	case "Grep":
		pattern, _ := params["pattern"].(string)
		path, _ := params["path"].(string)
		if pattern == "" {
			return ""
		}
		if path != "" {
			return fmt.Sprintf("%s in %s", pattern, filepath.Base(path))
		}
		return pattern
	case "Bash":
		command, _ := params["command"].(string)
		if command == "" {
			return ""
		}
		return truncate(oneLine(command), 60)
	case "Edit":
		filePath, _ := params["file_path"].(string)
		if filePath == "" {
			return ""
		}
		return filepath.Base(filePath)
	case "MultiEdit":
		filePath, _ := params["file_path"].(string)
		if filePath == "" {
			return ""
		}
		edits, _ := params["edits"].([]any)
		if len(edits) > 0 {
			return fmt.Sprintf("%s · %d 处修改", filepath.Base(filePath), len(edits))
		}
		return filepath.Base(filePath)
	case "Write":
		filePath, _ := params["file_path"].(string)
		if filePath == "" {
			return ""
		}
		return filepath.Base(filePath)
	case "Glob":
		pattern, _ := params["pattern"].(string)
		if pattern == "" {
			return ""
		}
		return pattern
	case "TodoWrite":
		return todoSummaryFromJSON(rawInput, "update todos")
	case "TodoRead":
		return "read todos"
	case "Task":
		description, _ := params["description"].(string)
		if description != "" {
			return truncate(oneLine(description), 60)
		}
		prompt, _ := params["prompt"].(string)
		if prompt != "" {
			return truncate(oneLine(prompt), 60)
		}
		return ""
	case "AgentCreate":
		description, _ := params["description"].(string)
		if description != "" {
			return truncate(oneLine(description), 60)
		}
		prompt, _ := params["prompt"].(string)
		if prompt != "" {
			return truncate(oneLine(prompt), 60)
		}
		return ""
	case "AgentGet", "AgentMessage", "AgentStop":
		agentID, _ := params["agent_id"].(string)
		if agentID != "" {
			return agentID
		}
		return ""
	case "AskUserQuestion":
		question, _ := params["question"].(string)
		if question == "" {
			return ""
		}
		return truncateDisplay(oneLine(question), 60)
	default:
		// For unknown tools, try to extract a "description" or "prompt" field
		// before falling back to raw JSON truncation
		if desc, ok := params["description"].(string); ok && desc != "" {
			return truncate(oneLine(desc), 60)
		}
		if prompt, ok := params["prompt"].(string); ok && prompt != "" {
			return truncate(oneLine(prompt), 60)
		}
		return truncate(oneLine(rawInput), 60)
	}
}

func todoSummaryFromJSON(raw, fallback string) string {
	var wrapped struct {
		Todos []todoItem `json:"todos"`
	}
	if err := json.Unmarshal([]byte(raw), &wrapped); err == nil && wrapped.Todos != nil {
		return summarizeTodos(wrapped.Todos, fallback)
	}
	var list []todoItem
	if err := json.Unmarshal([]byte(raw), &list); err == nil {
		return summarizeTodos(list, fallback)
	}
	return fallback
}

func summarizeTodos(items []todoItem, fallback string) string {
	if len(items) == 0 {
		return fallback
	}
	for _, item := range items {
		if normalizeTodoStatus(item.Status) == "in_progress" && strings.TrimSpace(todoDisplayText(item)) != "" {
			return fmt.Sprintf("%d todos: %s", len(items), truncate(oneLine(todoDisplayText(item)), 42))
		}
	}
	completed := 0
	for _, item := range items {
		if normalizeTodoStatus(item.Status) == "completed" {
			completed++
		}
	}
	return fmt.Sprintf("%d todos, %d completed", len(items), completed)
}

// toolResultSummary produces a short one-line summary of a tool's output.
func toolResultSummary(toolName, rawOutput string) string {
	return toolResultSummaryForStatus(toolName, rawOutput, false)
}

func toolResultSummaryForStatus(toolName, rawOutput string, isError bool) string {
	if rawOutput == "" {
		if toolName == "Bash" && isError {
			return "failed"
		}
		return ""
	}
	if summary, ok := gatePreflightSummary(rawOutput); ok {
		return summary
	}
	if isError && toolName != "Bash" {
		return errorResultSummary(rawOutput)
	}
	switch toolName {
	case "Grep":
		lines := strings.Split(rawOutput, "\n")
		matches := 0
		for _, line := range lines {
			if strings.TrimSpace(line) != "" {
				matches++
			}
		}
		if matches == 0 {
			return "no matches"
		}
		return fmt.Sprintf("%d matches", matches)
	case "Read":
		lines := strings.Split(rawOutput, "\n")
		return fmt.Sprintf("%d lines", len(lines))
	case "Bash":
		return bashResultSummary(rawOutput, isError)
	case "Edit":
		if strings.Contains(rawOutput, "error") || strings.Contains(rawOutput, "not found") {
			return "error"
		}
		return "ok"
	case "Glob":
		lines := strings.Split(rawOutput, "\n")
		count := 0
		for _, line := range lines {
			if strings.TrimSpace(line) != "" {
				count++
			}
		}
		if count == 0 {
			return "no files"
		}
		return fmt.Sprintf("%d files", count)
	case "TodoWrite":
		if match := todoSavedPattern.FindStringSubmatch(rawOutput); len(match) == 2 {
			return "saved " + match[1] + " todos"
		}
		if match := todoResultSummaryPattern.FindStringSubmatch(rawOutput); len(match) == 2 {
			if strings.Contains(strings.ToLower(rawOutput), "state was cleared") {
				return "cleared " + match[1] + " todos"
			}
			return "updated " + match[1] + " todos"
		}
		return ""
	case "TodoRead":
		return todoSummaryFromJSON(rawOutput, "")
	case "Task", "Agent", "AgentCreate", "AgentGet", "AgentMessage", "AgentStop":
		return capabilityLoopToolResultSummary(rawOutput)
	case "AskUserQuestion":
		if answer, ok := strings.CutPrefix(rawOutput, "User answered: "); ok {
			return "User answered: " + truncateDisplay(oneLine(answer), 40)
		}
		return ""
	default:
		return ""
	}
}

func errorResultSummary(rawOutput string) string {
	lower := strings.ToLower(rawOutput)
	switch {
	case strings.Contains(lower, "not found"), strings.Contains(lower, "no such file"):
		return "未找到目标"
	case strings.Contains(lower, "outside the current workspace"):
		return "写入位置不在当前工作区"
	case strings.Contains(lower, "permission denied"):
		return "没有权限"
	case strings.Contains(lower, "old_string"), strings.Contains(lower, "does not match"):
		return "没有找到要替换的原文"
	case strings.Contains(lower, "user input required"):
		return "等待用户输入"
	default:
		return "操作未完成"
	}
}

func bashResultSummary(rawOutput string, isError bool) string {
	rawOutput = strings.TrimSpace(rawOutput)
	if isError {
		if summary := commandFailureSummary(rawOutput); summary != "" {
			return summary
		}
	}
	if match := bashExitPattern.FindStringSubmatch(rawOutput); len(match) == 2 {
		return "exit " + match[1]
	}
	if isError {
		return "命令未完成"
	}
	return "exit 0"
}

func commandFailureSummary(rawOutput string) string {
	lower := strings.ToLower(rawOutput)
	switch {
	case strings.Contains(lower, "<gate-preflight"):
		if summary, ok := gatePreflightSummary(rawOutput); ok {
			return summary
		}
	case strings.Contains(lower, "pre-commit scope gate"):
		return "提交前需要先确认改动范围"
	case strings.Contains(lower, "shared-state git gate"):
		return "Git 操作前需要先确认当前状态"
	case strings.Contains(lower, "no changes added to commit"):
		return "没有已暂存的改动"
	case strings.Contains(lower, "outside the current workspace"):
		return "写入位置不在当前工作区"
	case strings.Contains(lower, "permission denied"):
		return "没有权限"
	case strings.Contains(lower, "command not found"):
		return "命令不存在"
	case strings.Contains(lower, "no such file or directory"):
		return "未找到文件或目录"
	default:
		return ""
	}
	return ""
}

// toolColorMap maps tool names to ANSI color codes for visual distinction.
var toolColorMap = map[string]string{
	"Read":         "69",  // blue — reading
	"Grep":         "183", // purple — searching
	"Glob":         "183", // purple — searching
	"Bash":         "220", // yellow — executing
	"Edit":         "114", // green — modifying
	"Write":        "114", // green — writing
	"MultiEdit":    "114", // green — modifying
	"Task":         "180", // cyan — sub-agent task
	"AgentCreate":  "180", // cyan — sub-agent
	"AgentGet":     "180", // cyan — sub-agent
	"AgentMessage": "180", // cyan — sub-agent
	"AgentStop":    "180", // cyan — sub-agent
}

// toolNameStyle returns a lipgloss style with the tool's assigned color.
func toolNameStyle(toolName string) lipgloss.Style {
	color, ok := toolColorMap[toolName]
	if !ok {
		return statusStyle
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Bold(true)
}

// toolStatusIcon returns a status icon for the tool activity item.
func toolStatusIcon(item toolActivityItem) string {
	switch item.Status {
	case "running":
		return "⋯"
	case "done":
		return "✓"
	case "blocked":
		return "!"
	case "interrupted":
		return "!"
	case "error":
		return "✗"
	default:
		return ""
	}
}

func toolStatusLabel(item toolActivityItem) string {
	switch strings.ToLower(strings.TrimSpace(item.Status)) {
	case "running", "started":
		return "进行中"
	case "done", "completed", "success":
		return "完成"
	case "blocked":
		return "已拦截"
	case "interrupted":
		return "已中断"
	case "error", "failed":
		return "失败"
	case "":
		if item.IsError {
			return "失败"
		}
		return "等待中"
	default:
		return strings.TrimSpace(item.Status)
	}
}

func toolDisplayName(toolName string) string {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "read":
		return "查看文件"
	case "grep":
		return "搜索内容"
	case "glob", "ls":
		return "查找文件"
	case "bash", "powershell":
		return "运行命令"
	case "edit", "multiedit":
		return "编辑文件"
	case "write":
		return "写入文件"
	case "todowrite":
		return "更新任务"
	case "todoread":
		return "查看任务"
	case "task", "agent", "agentcreate":
		return "子任务"
	case "agentget":
		return "查看子任务"
	case "agentmessage":
		return "发送子任务消息"
	case "agentstop":
		return "停止子任务"
	case "":
		return "工具"
	default:
		return toolName
	}
}

func toolDisplayDetail(item toolActivityItem) string {
	detail := strings.TrimSpace(item.Detail)
	if detail == "" {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(item.ToolName)) {
	case "todowrite", "todoread":
		return "任务：" + friendlyTodoText(detail)
	case "bash", "powershell":
		return "命令：" + detail
	case "read", "write", "edit", "multiedit":
		return "文件：" + detail
	case "grep":
		return "搜索：" + detail
	case "glob", "ls":
		return "范围：" + detail
	default:
		return detail
	}
}

func toolResultPrefix(item toolActivityItem) string {
	status := strings.ToLower(strings.TrimSpace(item.Status))
	if status == "blocked" {
		return "已拦截："
	}
	if status == "interrupted" {
		return "已中断："
	}
	if item.IsError || status == "error" || status == "failed" {
		return "未完成："
	}
	return "完成："
}

func friendlyToolResult(toolName, result string) string {
	result = strings.TrimSpace(result)
	if result == "" {
		return ""
	}
	switch strings.ToLower(result) {
	case "failed":
		return "操作未完成"
	case "not found":
		return "未找到目标"
	case "permission denied":
		return "没有权限"
	}
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "askuserquestion":
		if answer, ok := strings.CutPrefix(result, "User answered: "); ok {
			return answer
		}
		return result
	case "todowrite", "todoread":
		return friendlyTodoText(result)
	case "bash", "powershell":
		return friendlyCommandResult(result)
	case "read":
		return friendlyCountResult(result, "lines", "读取 %s 行", result)
	case "grep":
		if strings.EqualFold(result, "no matches") {
			return "未找到匹配"
		}
		return friendlyCountResult(result, "matches", "找到 %s 处匹配", result)
	case "glob", "ls":
		if strings.EqualFold(result, "no files") {
			return "未找到文件"
		}
		return friendlyCountResult(result, "files", "找到 %s 个文件", result)
	case "edit", "write", "multiedit":
		if strings.EqualFold(result, "ok") {
			return "修改完成"
		}
		if strings.EqualFold(result, "error") {
			return "修改未完成"
		}
	}
	return friendlyTodoText(result)
}

func friendlyCommandResult(result string) string {
	lower := strings.ToLower(strings.TrimSpace(result))
	switch lower {
	case "exit 0":
		return "命令成功"
	case "failed":
		return "运行失败"
	}
	if match := bashExitPattern.FindStringSubmatch(result); len(match) == 2 {
		return "命令退出码 " + match[1]
	}
	return result
}

func friendlyCountResult(result, unit, format, fallback string) string {
	fields := strings.Fields(strings.TrimSpace(result))
	if len(fields) >= 2 && strings.EqualFold(fields[1], unit) {
		return fmt.Sprintf(format, fields[0])
	}
	return fallback
}

func friendlyTodoText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	fields := strings.Fields(text)
	if len(fields) >= 3 {
		action := strings.ToLower(fields[0])
		count := fields[1]
		noun := strings.ToLower(strings.TrimSuffix(fields[2], ","))
		if noun == "todo" || noun == "todos" {
			switch action {
			case "saved":
				return "已保存 " + count + " 个任务"
			case "updated":
				return "已更新 " + count + " 个任务"
			case "cleared":
				return "已清空 " + count + " 个任务"
			}
		}
	}
	if strings.Contains(text, " todos: ") {
		parts := strings.SplitN(text, " todos: ", 2)
		return parts[0] + " 个任务：" + parts[1]
	}
	if strings.Contains(text, " todos, ") {
		text = strings.ReplaceAll(text, " todos, ", " 个任务，")
		text = strings.ReplaceAll(text, " completed", " 个已完成")
	}
	return text
}
