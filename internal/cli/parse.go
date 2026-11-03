package cli

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/defaults"
	"github.com/konglong87/super-niuma/internal/promptmode"
	"github.com/konglong87/super-niuma/internal/runtimeprofile"
	"github.com/konglong87/super-niuma/internal/session"
)

// flagValue 读取 flag 的下一个参数作为值，并前移游标。
// 错误消息格式必须保持 "<flag> requires a value"，与既有输出逐字节一致。
func flagValue(args []string, i *int, flag string) (string, error) {
	*i++
	if *i >= len(args) {
		return "", fmt.Errorf("%s requires a value", flag)
	}
	return args[*i], nil
}

// readFlagFile 读取某个 flag 指向的文件。裸 os.ReadFile 的错误
// （"open x.md: no such file or directory"）不说是哪个 flag 出的问题，
// 用户拿到一个孤零零的路径无从下手。
func readFlagFile(flag, cwd, path string) ([]byte, error) {
	content, err := os.ReadFile(resolveInputPath(cwd, path))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", flag, err)
	}
	return content, nil
}

func parseArgs(args []string) (options, []string, error) {
	cwd, _ := os.Getwd()
	opts := options{
		outputFormat: "text",
		inputFormat:  "text",
		maxTurns:     defaults.MaxTurns,
		cwd:          cwd,
	}
	var rest []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-h", "--help":
			return opts, []string{"help"}, nil
		case "-v", "-V", "--version":
			return opts, []string{"version"}, nil
		case "-p", "--print":
			opts.print = true
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				opts.prompt = args[i]
			}
		case "--bare":
			opts.runtimeProfile = runtimeprofile.ProfileBare
		case "--model":
			v, err := flagValue(args, &i, "--model")
			if err != nil {
				return opts, nil, err
			}
			opts.model = v
			opts.modelExplicit = true
		case "--provider":
			v, err := flagValue(args, &i, "--provider")
			if err != nil {
				return opts, nil, err
			}
			opts.providerName = strings.TrimSpace(v)
		case "--output-format":
			v, err := flagValue(args, &i, "--output-format")
			if err != nil {
				return opts, nil, err
			}
			opts.outputFormat = v
		case "--input-format":
			v, err := flagValue(args, &i, "--input-format")
			if err != nil {
				return opts, nil, err
			}
			if v != "text" && v != "stream-json" {
				return opts, nil, errors.New("--input-format must be text or stream-json")
			}
			opts.inputFormat = v
		case "--json-schema":
			v, err := flagValue(args, &i, "--json-schema")
			if err != nil {
				return opts, nil, err
			}
			opts.jsonSchema = v
		case "--runtime-trace-output":
			v, err := flagValue(args, &i, "--runtime-trace-output")
			if err != nil {
				return opts, nil, err
			}
			opts.runtimeTraceOutput = strings.TrimSpace(v)
			if opts.runtimeTraceOutput == "" {
				return opts, nil, errors.New("--runtime-trace-output requires a non-empty path")
			}
		case "--max-parallel-read-only-tools":
			v, err := flagValue(args, &i, "--max-parallel-read-only-tools")
			if err != nil {
				return opts, nil, err
			}
			n, err := strconv.Atoi(v)
			if err != nil {
				return opts, nil, errors.New("--max-parallel-read-only-tools must be an integer")
			}
			opts.maxParallelReadOnlyTools = n
		case "--include-hook-events":
			opts.includeHookEvents = true
		case "--include-partial-messages":
			opts.includePartialMessages = true
		case "--include-stream-events", "--include-upstream-stream-events":
			opts.includeStreamEvents = true
		case "--max-turns":
			v, err := flagValue(args, &i, "--max-turns")
			if err != nil {
				return opts, nil, err
			}
			var n int
			if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n < 1 {
				return opts, nil, errors.New("--max-turns must be a positive integer")
			}
			opts.maxTurns = n
		case "--max-tokens":
			v, err := flagValue(args, &i, "--max-tokens")
			if err != nil {
				return opts, nil, err
			}
			var n int
			if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n < 1 {
				return opts, nil, errors.New("--max-tokens must be a positive integer")
			}
			opts.maxTokens = n
		case "--system-prompt":
			v, err := flagValue(args, &i, "--system-prompt")
			if err != nil {
				return opts, nil, err
			}
			opts.systemPrompt = v
		case "--system-prompt-file":
			v, err := flagValue(args, &i, "--system-prompt-file")
			if err != nil {
				return opts, nil, err
			}
			content, err := readFlagFile("--system-prompt-file", opts.cwd, v)
			if err != nil {
				return opts, nil, err
			}
			opts.systemPrompt = strings.TrimRight(string(content), "\r\n")
		case "--append-system-prompt":
			v, err := flagValue(args, &i, "--append-system-prompt")
			if err != nil {
				return opts, nil, err
			}
			opts.appendSystem = appendWithBlankLine(opts.appendSystem, v)
		case "--append-system-prompt-file":
			v, err := flagValue(args, &i, "--append-system-prompt-file")
			if err != nil {
				return opts, nil, err
			}
			content, err := readFlagFile("--append-system-prompt-file", opts.cwd, v)
			if err != nil {
				return opts, nil, err
			}
			opts.appendSystem = appendWithBlankLine(opts.appendSystem, strings.TrimRight(string(content), "\r\n"))
		case "--prompt-mode":
			v, err := flagValue(args, &i, "--prompt-mode")
			if err != nil {
				return opts, nil, err
			}
			if !promptmode.Valid(v) {
				return opts, nil, errors.New("--prompt-mode must be code or chat")
			}
			opts.promptMode = promptmode.Parse(v, promptmode.Code).String()
		case "--goal-evaluator":
			v, err := flagValue(args, &i, "--goal-evaluator")
			if err != nil {
				return opts, nil, err
			}
			value := strings.ToLower(strings.TrimSpace(v))
			if value != "" && value != "deterministic" && value != "model" {
				return opts, nil, errors.New("--goal-evaluator must be deterministic or model")
			}
			opts.goalEvaluator = value
		case "--agent":
			v, err := flagValue(args, &i, "--agent")
			if err != nil {
				return opts, nil, err
			}
			opts.agentName = strings.TrimSpace(v)
		case "--allowedTools", "--allowed-tools":
			values, next, err := collectFlagValues(args, i, "--allowedTools")
			if err != nil {
				return opts, nil, errors.New("--allowedTools requires a value")
			}
			i = next
			opts.allowedTools = append(opts.allowedTools, splitListArgs(values)...)
		case "--disallowedTools", "--disallowed-tools":
			values, next, err := collectFlagValues(args, i, "--disallowedTools")
			if err != nil {
				return opts, nil, errors.New("--disallowedTools requires a value")
			}
			i = next
			opts.deniedTools = append(opts.deniedTools, splitListArgs(values)...)
		case "--tools":
			values, next, err := collectFlagValues(args, i, "--tools")
			if err != nil {
				return opts, nil, errors.New("--tools requires a value")
			}
			i = next
			opts.toolsSpecified = true
			opts.enabledTools = splitListArgs(values)
			if len(opts.enabledTools) == 1 && opts.enabledTools[0] == "default" {
				opts.toolsSpecified = false
				opts.enabledTools = nil
			}
		case "--settings":
			v, err := flagValue(args, &i, "--settings")
			if err != nil {
				return opts, nil, err
			}
			opts.settingsInputs = append(opts.settingsInputs, v)
		case "--mcp-config":
			values, next, err := collectFlagValues(args, i, "--mcp-config")
			if err != nil {
				return opts, nil, errors.New("--mcp-config requires a value")
			}
			i = next
			opts.mcpConfigInputs = append(opts.mcpConfigInputs, values...)
		case "--strict-mcp-config":
			opts.strictMCPConfig = true
		case "--permission-mode":
			v, err := flagValue(args, &i, "--permission-mode")
			if err != nil {
				return opts, nil, err
			}
			opts.permissionMode = v
		case "--permission-prompt-tool":
			v, err := flagValue(args, &i, "--permission-prompt-tool")
			if err != nil {
				return opts, nil, err
			}
			opts.permissionPromptTool = v
		case "--delegate-permissions", "--dangerously-skip-permissions-with-classifiers", "--afk", "--enable-auto-mode":
			opts.permissionMode = "auto"
		case "--add-dir", "--addDir":
			values, next, err := collectFlagValues(args, i, "--add-dir")
			if err != nil {
				return opts, nil, errors.New("--add-dir requires a value")
			}
			i = next
			opts.additionalDirs = append(opts.additionalDirs, values...)
		case "--dangerously-skip-permissions":
			opts.skipPermissions = true
		case "--cwd":
			v, err := flagValue(args, &i, "--cwd")
			if err != nil {
				return opts, nil, err
			}
			opts.cwd = v
		case "-c", "--continue":
			opts.resume = "latest"
		case "-r", "--resume":
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				opts.resume = args[i]
			} else {
				opts.resume = "latest"
			}
		case "--resume-session-at":
			v, err := flagValue(args, &i, "--resume-session-at")
			if err != nil {
				return opts, nil, err
			}
			opts.resumeSessionAt = v
		case "--rewind-files":
			v, err := flagValue(args, &i, "--rewind-files")
			if err != nil {
				return opts, nil, err
			}
			opts.rewindFiles = v
		case "--fork-session":
			opts.forkSession = true
		case "--session-id", "--sessionId":
			v, err := flagValue(args, &i, arg)
			if err != nil {
				return opts, nil, err
			}
			if !session.IsValidID(v) {
				return opts, nil, fmt.Errorf("invalid %s: %s", arg, v)
			}
			opts.sessionID = v
		case "-n", "--name":
			v, err := flagValue(args, &i, "--name")
			if err != nil {
				return opts, nil, err
			}
			opts.sessionName = v
		case "--no-session-persistence":
			opts.noPersistence = true
		case "--bg", "--background":
			opts.background = true
		default:
			if strings.HasPrefix(arg, "-") {
				return opts, nil, fmt.Errorf("unknown flag: %s", arg)
			}
			rest = append(rest, args[i:]...)
			opts.model = config.ResolveModel(opts.cwd, opts.model)
			return opts, rest, nil
		}
	}
	opts.model = config.ResolveModel(opts.cwd, opts.model)
	return opts, rest, nil
}

func collectFlagValues(args []string, index int, flag string) ([]string, int, error) {
	var values []string
	i := index + 1
	for i < len(args) && !strings.HasPrefix(args[i], "-") {
		values = append(values, args[i])
		i++
	}
	if len(values) == 0 {
		return nil, index, fmt.Errorf("%s requires a value", flag)
	}
	return values, i - 1, nil
}

func splitListArgs(values []string) []string {
	var out []string
	for _, value := range values {
		out = append(out, splitCSVArg(value)...)
	}
	return out
}

func splitCSVArg(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}
