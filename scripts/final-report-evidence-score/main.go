package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

type report struct {
	OK              bool              `json:"ok"`
	Score           int               `json:"score"`
	RequiredScore   int               `json:"required_score"`
	RunLog          string            `json:"run_log"`
	ScoredBytes     int               `json:"scored_bytes"`
	ScoredScope     string            `json:"scored_scope"`
	Checks          map[string]bool   `json:"checks"`
	Missing         []string          `json:"missing,omitempty"`
	Anchors         []string          `json:"anchors,omitempty"`
	FunctionAnchors []string          `json:"function_anchors,omitempty"`
	SectionAnchors  map[string]string `json:"section_anchors,omitempty"`
	ForbiddenHits   []string          `json:"forbidden_hits,omitempty"`
}

var (
	fileAnchorRE           = regexp.MustCompile("(?:^|[\\s\"'`([{<])(?:[./~\\w-]+/)?[\\w.-]+\\.(?:jsonl?|ya?ml|tsx|jsx|go|ts|js|py|md|toml|sh|sql)(?::\\d+)?")
	cmdAnchorRE            = regexp.MustCompile(`\b(?:go test|go run|git diff --check|bash\s+scripts/|scripts/[^\s"']+\.sh|node\s+|npm\s+test|pnpm\s+test|pytest|curl\s+|make\s+)`)
	toolArtifactAnchorRE   = regexp.MustCompile(`\b(?:Grep|Read|Task|TodoWrite|LS|Glob)\b|tool_result|transcript|prompt dump|request dump|\.jsonl|\.output|acceptance`)
	markdownSectionStartRE = regexp.MustCompile(`^\s*#{1,6}\s+`)
	markdownHeadingRE      = regexp.MustCompile(`^\s*(#{1,6})\s+`)
)

func main() {
	runLog := flag.String("run-log", "", "combined final-report run log to score")
	minScore := flag.Int("min-score", 10, "minimum score required for ok=true")
	requireFilesCSV := flag.String("require-files", "internal/query/query.go,internal/tools/task/task.go,internal/tools/agent/agent.go,internal/agentruntime/runtime.go", "comma-separated file anchors required in the report")
	requireTermsCSV := flag.String("require-terms", "runtimeStatusText,runtimeTodoStatus,runtimePlanStatus,runtimeAgentTaskStatus,subagentToolPlanningStatus,Tool.Description,Tool.Run,runSingle,AgentCreate,AgentGet", "comma-separated implementation/function terms required in the report")
	forbidCSV := flag.String("forbid-text", "未能提供可靠函数/行号,未完全闭环,尚未完全闭环,没有读取对应测试文件,无法可靠给出函数级", "comma-separated text snippets that fail the scorecard if present")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: go run ./scripts/final-report-evidence-score --run-log <path> [flags]\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *runLog == "" || flag.NArg() != 0 {
		flag.Usage()
		os.Exit(2)
	}
	data, err := os.ReadFile(*runLog)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read run log: %v\n", err)
		os.Exit(2)
	}
	rep := scoreRunLog(*runLog, string(data), *minScore, splitCSV(*requireFilesCSV), splitCSV(*requireTermsCSV), splitCSV(*forbidCSV))
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(rep); err != nil {
		fmt.Fprintf(os.Stderr, "write score report: %v\n", err)
		os.Exit(2)
	}
	if !rep.OK {
		os.Exit(1)
	}
}

func scoreRunLog(path, text string, minScore int, requiredFiles, requiredTerms, forbidden []string) report {
	scoredText, scoredScope := finalReportText(text)
	checks := map[string]bool{}
	var missing []string
	var forbiddenHits []string

	require := func(name string, ok bool) {
		checks[name] = ok
		if !ok {
			missing = append(missing, name)
		}
	}

	for _, want := range requiredFiles {
		require("file:"+want, containsFold(scoredText, want))
	}
	for _, want := range requiredTerms {
		require("term:"+want, containsFold(scoredText, want))
	}
	for _, forbiddenText := range forbidden {
		if forbiddenText == "" {
			continue
		}
		if strings.Contains(scoredText, forbiddenText) {
			forbiddenHits = append(forbiddenHits, forbiddenText)
		}
	}

	sections := map[string][]string{
		"evidence":     {"evidence", "证据"},
		"unknowns":     {"unknowns", "unknown", "未知", "无法证明"},
		"verification": {"verification", "verified", "验证", "测试"},
		"risks":        {"risks", "risk", "风险"},
		"next_action":  {"next action", "next_action", "下一步"},
	}
	sectionAnchors := map[string]string{}
	for section, labels := range sections {
		block := sectionBlock(scoredText, labels)
		ok := false
		anchor := ""
		if block != "" {
			switch section {
			case "evidence":
				anchor, ok = anchoredBlockLine(block, false)
			case "verification":
				anchor, ok = anchoredBlockLine(block, true)
			default:
				anchor, ok = substantiveBlockLine(block, labels)
			}
		}
		require("section:"+section, ok)
		if ok {
			sectionAnchors[section] = strings.TrimSpace(anchor)
		}
	}

	anchors := extractFileAnchors(scoredText)
	functionAnchors := extractFunctionAnchors(scoredText)
	require("anchored_file_count>=4", len(anchors) >= 4)
	require("function_anchor_count>=6", len(functionAnchors) >= 6)
	require("verification_has_command_or_dump", verificationHasActionAnchor(scoredText))
	require("has_no_forbidden_text", len(forbiddenHits) == 0)

	score := 0
	for _, ok := range checks {
		if ok {
			score++
		}
	}
	ok := score >= minScore && len(missing) == 0 && len(forbiddenHits) == 0
	sort.Strings(missing)
	sort.Strings(forbiddenHits)
	return report{
		OK:              ok,
		Score:           score,
		RequiredScore:   minScore,
		RunLog:          path,
		ScoredBytes:     len(scoredText),
		ScoredScope:     scoredScope,
		Checks:          checks,
		Missing:         missing,
		Anchors:         anchors,
		FunctionAnchors: functionAnchors,
		SectionAnchors:  sectionAnchors,
		ForbiddenHits:   forbiddenHits,
	}
}

func finalReportText(text string) (string, string) {
	var visible []string
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, `{"level":`) {
			break
		}
		visible = append(visible, line)
	}
	scoped := strings.TrimSpace(strings.Join(visible, "\n"))
	if scoped != "" {
		return scoped, "visible_output_before_json_logs"
	}
	return text, "full_log_fallback"
}

func sectionBlock(text string, labels []string) string {
	allSectionLabels := []string{"evidence", "unknowns", "unknown", "verification", "verified", "risks", "risk", "next action", "next_action", "证据", "未知", "无法证明", "验证", "测试", "风险", "下一步"}
	lines := strings.Split(text, "\n")
	start := -1
	for i, line := range lines {
		if !markdownSectionStartRE.MatchString(line) {
			continue
		}
		normalized := strings.ToLower(line)
		for _, label := range labels {
			if !isASCII(label) {
				continue
			}
			if containsSectionLabel(normalized, label) {
				start = i
				break
			}
		}
		if start >= 0 {
			break
		}
	}
	if start < 0 {
		for i, line := range lines {
			if !markdownSectionStartRE.MatchString(line) {
				continue
			}
			normalized := strings.ToLower(line)
			for _, label := range labels {
				if isASCII(label) {
					continue
				}
				if containsSectionLabel(normalized, label) {
					start = i
					break
				}
			}
			if start >= 0 {
				break
			}
		}
	}
	for i, line := range lines {
		if start >= 0 {
			break
		}
		normalized := strings.ToLower(line)
		if lineHasInlineSectionLabel(normalized, labels) {
			start = i
			break
		}
	}
	if start < 0 {
		return ""
	}
	startLevel := markdownHeadingLevel(lines[start])
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		normalized := strings.ToLower(lines[i])
		headingLevel := markdownHeadingLevel(lines[i])
		if startLevel > 0 && headingLevel > 0 {
			if headingLevel <= startLevel || headingHasSectionLabel(normalized, allSectionLabels) {
				end = i
				break
			}
			continue
		}
		if (startLevel == 0 && headingLevel > 0) || lineHasInlineSectionLabel(normalized, allSectionLabels) {
			end = i
			break
		}
	}
	return strings.Join(lines[start:end], "\n")
}

func markdownHeadingLevel(line string) int {
	match := markdownHeadingRE.FindStringSubmatch(line)
	if len(match) != 2 {
		return 0
	}
	return len(match[1])
}

func headingHasSectionLabel(line string, labels []string) bool {
	if !markdownSectionStartRE.MatchString(line) {
		return false
	}
	for _, label := range labels {
		if containsSectionLabel(line, label) {
			return true
		}
	}
	return false
}

func containsSectionLabel(line, label string) bool {
	label = strings.ToLower(label)
	if isASCII(label) {
		return strings.Contains(line, label)
	}
	trimmed := strings.TrimSpace(markdownSectionStartRE.ReplaceAllString(line, ""))
	return trimmed == label || strings.HasSuffix(trimmed, " "+label) || strings.HasSuffix(trimmed, label+":")
}

func lineHasInlineSectionLabel(line string, labels []string) bool {
	for _, label := range labels {
		label = strings.ToLower(label)
		if isASCII(label) {
			if strings.Contains(line, label+":") || strings.HasPrefix(line, label+" ") {
				return true
			}
			continue
		}
		if strings.Contains(line, label+"：") || strings.Contains(line, label+":") {
			return true
		}
	}
	return false
}

func isASCII(text string) bool {
	for _, r := range text {
		if r > 127 {
			return false
		}
	}
	return true
}

func anchoredBlockLine(block string, allowToolArtifact bool) (string, bool) {
	for _, line := range strings.Split(block, "\n") {
		if len(extractFileAnchors(line)) > 0 || cmdAnchorRE.MatchString(line) {
			return line, true
		}
		if allowToolArtifact && toolArtifactAnchorRE.MatchString(line) {
			return line, true
		}
	}
	return "", false
}

func substantiveBlockLine(block string, labels []string) (string, bool) {
	for _, line := range strings.Split(block, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || markdownSectionStartRE.MatchString(trimmed) {
			continue
		}
		normalized := strings.ToLower(trimmed)
		if containsAny(normalized, labels...) && len([]rune(trimmed)) < 20 {
			continue
		}
		if len([]rune(trimmed)) >= 20 {
			return line, true
		}
	}
	return "", false
}

func verificationHasActionAnchor(text string) bool {
	block := sectionBlock(text, []string{"verification", "verified", "验证", "测试"})
	if block == "" {
		return false
	}
	_, ok := anchoredBlockLine(block, true)
	return ok
}

func extractFileAnchors(text string) []string {
	var out []string
	for _, match := range fileAnchorRE.FindAllString(text, -1) {
		anchor := strings.Trim(match, " \t\n\r\"'`([{<")
		out = appendUnique(out, anchor)
	}
	sort.Strings(out)
	if len(out) > 32 {
		return out[:32]
	}
	return out
}

func extractFunctionAnchors(text string) []string {
	candidates := []string{
		"runtimeStatusText",
		"runtimeTodoStatus",
		"runtimePlanStatus",
		"runtimeAgentTaskStatus",
		"subagentToolPlanningStatus",
		"Tool.Description",
		"Tool.Run",
		"runSingle",
		"AgentCreate",
		"AgentGet",
		"agentTaskResultForModel",
		"buildRuntimeStatus",
	}
	var out []string
	for _, candidate := range candidates {
		if containsFold(text, candidate) {
			out = append(out, candidate)
		}
	}
	sort.Strings(out)
	return out
}

func splitCSV(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func containsFold(text, needle string) bool {
	return strings.Contains(strings.ToLower(text), strings.ToLower(needle))
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, strings.ToLower(needle)) {
			return true
		}
	}
	return false
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
