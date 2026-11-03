package skilllint

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"mvdan.cc/sh/v3/syntax"
)

type Rule string

const (
	RuleShellCrossFenceVariable   Rule = "shell_cross_fence_variable"
	RuleInlineBashSourcePath      Rule = "inline_bash_source_path"
	RuleEmptyScanVacuousSuccess   Rule = "empty_scan_vacuous_success"
	RuleNonSelfContainedShell     Rule = "non_self_contained_shell_fence"
	RuleUnstableSkillRelativePath Rule = "unstable_skill_relative_path"
	RuleShellParseError           Rule = "shell_parse_error"
)

type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

type Finding struct {
	Rule     Rule     `json:"rule"`
	Severity Severity `json:"severity"`
	Path     string   `json:"path"`
	Line     int      `json:"line"`
	Variable string   `json:"variable,omitempty"`
	Message  string   `json:"message"`
}

type Report struct {
	Documents int       `json:"documents"`
	Errors    int       `json:"errors"`
	Warnings  int       `json:"warnings"`
	Findings  []Finding `json:"findings"`
}

type shellFence struct {
	body string
	line int
}

var shellLanguages = map[string]bool{
	"bash": true, "sh": true, "shell": true, "zsh": true,
}

var stableShellVariables = map[string]bool{
	"HOME": true, "PATH": true, "PWD": true, "OLDPWD": true, "TMPDIR": true,
	"USER": true, "SHELL": true, "CI": true,
	"NIUMA_SKILL_DIR": true, "CLAUDE_SKILL_DIR": true,
	"?": true, "#": true, "@": true, "*": true, "!": true, "$": true,
}

func CheckPaths(paths []string) (Report, error) {
	files, err := collectSkillFiles(paths)
	if err != nil {
		return Report{}, err
	}
	report := Report{Documents: len(files), Findings: make([]Finding, 0)}
	for _, path := range files {
		findings, err := checkFile(path)
		if err != nil {
			return Report{}, err
		}
		report.Findings = append(report.Findings, findings...)
	}
	sort.Slice(report.Findings, func(i, j int) bool {
		left, right := report.Findings[i], report.Findings[j]
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		if left.Line != right.Line {
			return left.Line < right.Line
		}
		if left.Rule != right.Rule {
			return left.Rule < right.Rule
		}
		return left.Variable < right.Variable
	})
	for _, finding := range report.Findings {
		if finding.Severity == SeverityError {
			report.Errors++
		} else {
			report.Warnings++
		}
	}
	return report, nil
}

func collectSkillFiles(paths []string) ([]string, error) {
	seen := map[string]bool{}
	var files []string
	for _, input := range paths {
		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}
		abs, err := filepath.Abs(input)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(abs)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			if !seen[abs] {
				seen[abs] = true
				files = append(files, abs)
			}
			continue
		}
		err = filepath.WalkDir(abs, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				if entry.Name() == ".git" || entry.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.EqualFold(entry.Name(), "SKILL.md") && !seen[path] {
				seen[path] = true
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(files)
	return files, nil
}

func checkFile(path string) ([]Finding, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Skill %q: %w", path, err)
	}
	fences := shellFences(source)
	priorAssignments := map[string]bool{}
	var findings []Finding
	for _, fence := range fences {
		file, parseErr := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(fence.body), path)
		if parseErr != nil {
			findings = append(findings, Finding{Rule: RuleShellParseError, Severity: SeverityError, Path: path, Line: fence.line, Message: parseErr.Error()})
			continue
		}
		assigned, referenced, lines := shellVariables(file, fence.line)
		if referenced["BASH_SOURCE"] {
			findings = append(findings, Finding{
				Rule: RuleInlineBashSourcePath, Severity: SeverityError, Path: path, Line: lines["BASH_SOURCE"], Variable: "BASH_SOURCE",
				Message: "BASH_SOURCE is not a stable Skill path in an inline shell tool call; use NIUMA_SKILL_DIR",
			})
		}
		for variable := range referenced {
			if assigned[variable] || stableShellVariables[variable] || isPositionalParameter(variable) {
				continue
			}
			if priorAssignments[variable] {
				findings = append(findings, Finding{
					Rule: RuleShellCrossFenceVariable, Severity: SeverityError, Path: path, Line: lines[variable], Variable: variable,
					Message: "variable is assigned only in an earlier shell fence, but each shell tool call starts a fresh process",
				})
				continue
			}
			if strings.ToUpper(variable) == variable && variable != "BASH_SOURCE" {
				findings = append(findings, Finding{
					Rule: RuleNonSelfContainedShell, Severity: SeverityWarning, Path: path, Line: lines[variable], Variable: variable,
					Message: "shell fence reads an undeclared variable; define it in this fence or use a documented runtime environment variable",
				})
			}
		}
		if vacuousScanRisk(fence.body) {
			findings = append(findings, Finding{
				Rule: RuleEmptyScanVacuousSuccess, Severity: SeverityWarning, Path: path, Line: fence.line,
				Message: "scan/count comparison has no explicit non-empty assertion; an empty target set may report success",
			})
		}
		if unstableSkillPathRisk(fence.body) {
			findings = append(findings, Finding{
				Rule: RuleUnstableSkillRelativePath, Severity: SeverityWarning, Path: path, Line: fence.line,
				Message: "Skill path is derived from inline shell location or CWD; use NIUMA_SKILL_DIR for Skill-owned assets",
			})
		}
		for variable := range assigned {
			priorAssignments[variable] = true
		}
	}
	return deduplicate(findings), nil
}

func shellFences(source []byte) []shellFence {
	document := goldmark.New().Parser().Parse(text.NewReader(source))
	var fences []shellFence
	_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		block, ok := node.(*ast.FencedCodeBlock)
		if !entering || !ok {
			return ast.WalkContinue, nil
		}
		language := strings.ToLower(string(block.Language(source)))
		if !shellLanguages[language] {
			return ast.WalkContinue, nil
		}
		line := 1
		if block.Lines().Len() > 0 {
			line = bytes.Count(source[:block.Lines().At(0).Start], []byte{'\n'}) + 1
		}
		fences = append(fences, shellFence{body: string(block.Lines().Value(source)), line: line})
		return ast.WalkContinue, nil
	})
	return fences
}

func shellVariables(file *syntax.File, baseLine int) (map[string]bool, map[string]bool, map[string]int) {
	assigned := map[string]bool{}
	referenced := map[string]bool{}
	lines := map[string]int{}
	syntax.Walk(file, func(node syntax.Node) bool {
		switch node := node.(type) {
		case *syntax.Assign:
			if node.Name != nil {
				assigned[node.Name.Value] = true
			}
		case *syntax.WordIter:
			if node.Name != nil {
				assigned[node.Name.Value] = true
			}
		case *syntax.ParamExp:
			if node.Param != nil {
				name := node.Param.Value
				referenced[name] = true
				if _, ok := lines[name]; !ok {
					lines[name] = baseLine + int(node.Pos().Line()) - 1
				}
			}
		}
		return true
	})
	return assigned, referenced, lines
}

var (
	countComparisonRE = regexp.MustCompile(`(?i)(?:total|count|covered)[^\n]*(?:-eq|==)|(?:-eq|==)[^\n]*(?:total|count|covered)`)
	nonemptyGuardRE   = regexp.MustCompile(`(?i)(?:total|count)[^\n]*(?:-gt\s+0|-ne\s+0|!=\s*0)|(?:-gt\s+0|-ne\s+0|!=\s*0)[^\n]*(?:total|count)`)
	unstablePathRE    = regexp.MustCompile(`(?i)(?:dirname\s+[^\n]*(?:\$0|BASH_SOURCE)|(?:^|\n)\s*[A-Z_]*(?:ROOT|DIR)\s*=\s*["']?\$\(pwd\))`)
)

func vacuousScanRisk(body string) bool {
	lower := strings.ToLower(body)
	scans := strings.Contains(lower, "find ") || strings.Contains(lower, "rg ") || strings.Contains(lower, "grep ") || strings.Contains(lower, "for ")
	return scans && countComparisonRE.MatchString(body) && !nonemptyGuardRE.MatchString(body)
}

func unstableSkillPathRisk(body string) bool {
	return unstablePathRE.MatchString(body)
}

func isPositionalParameter(variable string) bool {
	if variable == "" {
		return false
	}
	for _, char := range variable {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func deduplicate(findings []Finding) []Finding {
	seen := map[string]bool{}
	out := findings[:0]
	for _, finding := range findings {
		key := fmt.Sprintf("%s\x00%d\x00%s\x00%s", finding.Rule, finding.Line, finding.Variable, finding.Message)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, finding)
	}
	return out
}
