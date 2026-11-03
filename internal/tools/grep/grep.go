package grep

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/konglong87/super-niuma/internal/files"
	"github.com/konglong87/super-niuma/internal/tools"
	globtool "github.com/konglong87/super-niuma/internal/tools/glob"
)

const maxMatches = 1000
const maxResultSizeChars = 20_000
const defaultHeadLimit = 250

type Tool struct{}

func New() Tool { return Tool{} }

func (Tool) Name() string { return "Grep" }

func (Tool) ExecutionPolicy() tools.ExecutionPolicy {
	return tools.ExecutionPolicy{Concurrency: tools.ConcurrencyReadOnly}
}

func (Tool) MaxResultSizeChars() int { return maxResultSizeChars }

func (Tool) Description() string {
	return `A powerful search tool built on ripgrep-style semantics.

Usage:
- ALWAYS use Grep for search tasks. NEVER invoke grep or rg as a Bash command.
- Supports regular expressions (RE2 syntax). Literal braces need escaping in Go patterns.
- Filter files with glob (e.g. "*.js", "**/*.tsx") or type (e.g. "go", "py", "rust").
- Output modes: "files_with_matches" shows file paths (default), "content" shows matching lines, "count" shows match counts.
- content mode supports -A/-B/-C context, -n line numbers, -i case-insensitive search, head_limit and offset pagination.
- Multiline matching is available with multiline:true; content output reports the starting line of each multiline match.

For investigation, start broad searches with output_mode:"files_with_matches" or
"count" to identify candidate files, then Read only the few confirmed files or
line ranges needed. Once you have enough file/function/line evidence, stop
searching and synthesize the answer.`
}

func (Tool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
	  "$schema": "https://json-schema.org/draft/2020-12/schema",
	  "type": "object",
	  "properties": {
	    "pattern": {"description": "The regular expression pattern to search for in file contents", "type": "string"},
	    "path": {"description": "File or directory to search in (rg PATH). Defaults to current working directory.", "type": "string"},
	    "glob": {"description": "Glob pattern to filter files (e.g. \"*.js\", \"*.{ts,tsx}\") - maps to rg --glob", "type": "string"},
	    "output_mode": {"description": "Output mode: \"content\" shows matching lines (supports -A/-B/-C context, -n line numbers, head_limit), \"files_with_matches\" shows file paths (supports head_limit), \"count\" shows match counts (supports head_limit). Defaults to \"files_with_matches\".", "type": "string", "enum": ["content", "files_with_matches", "count"]},
	    "-B": {"description": "Number of lines to show before each match (rg -B). Requires output_mode: \"content\", ignored otherwise.", "type": "number"},
	    "-A": {"description": "Number of lines to show after each match (rg -A). Requires output_mode: \"content\", ignored otherwise.", "type": "number"},
	    "-C": {"description": "Alias for context.", "type": "number"},
	    "context": {"description": "Number of lines to show before and after each match (rg -C). Requires output_mode: \"content\", ignored otherwise.", "type": "number"},
	    "-n": {"description": "Show line numbers in output (rg -n). Requires output_mode: \"content\", ignored otherwise. Defaults to true.", "type": "boolean"},
	    "-i": {"description": "Case insensitive search (rg -i)", "type": "boolean"},
	    "type": {"description": "File type to search (rg --type). Common types: js, py, rust, go, java, etc. More efficient than include for standard file types.", "type": "string"},
	    "head_limit": {"description": "Limit output to first N lines/entries, equivalent to \"| head -N\". Works across all output modes: content (limits output lines), files_with_matches (limits file paths), count (limits count entries). Defaults to 250 when unspecified. Pass 0 for unlimited (use sparingly — large result sets waste context).", "type": "number"},
	    "offset": {"description": "Skip first N lines/entries before applying head_limit, equivalent to \"| tail -n +N | head -N\". Works across all output modes. Defaults to 0.", "type": "number"},
	    "multiline": {"description": "Enable multiline mode where . matches newlines and patterns can span lines (rg -U --multiline-dotall). Default: false.", "type": "boolean"}
	  },
	  "required": ["pattern"],
	  "additionalProperties": false
	}`)
}

func (Tool) Run(_ context.Context, input json.RawMessage, toolContext tools.Context) tools.Result {
	var params struct {
		Pattern         string `json:"pattern"`
		Path            string `json:"path"`
		Glob            string `json:"glob"`
		OutputMode      string `json:"output_mode"`
		Before          int    `json:"before_context"`
		After           int    `json:"after_context"`
		Context         int    `json:"context"`
		BeforeFlag      int    `json:"-B"`
		AfterFlag       int    `json:"-A"`
		ContextFlag     int    `json:"-C"`
		LineNumbers     *bool  `json:"-n"`
		CaseInsensitive bool   `json:"-i"`
		Type            string `json:"type"`
		HeadLimit       *int   `json:"head_limit"`
		Offset          int    `json:"offset"`
		Multiline       bool   `json:"multiline"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	pattern := params.Pattern
	if params.CaseInsensitive {
		pattern = "(?i:" + pattern + ")"
	}
	if params.Multiline {
		pattern = "(?s:" + pattern + ")"
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	root, err := tools.ResolvePath(toolContext.CWD, params.Path)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	fileMatches := func(string) bool { return true }
	matchers := []func(string) bool{}
	if strings.TrimSpace(params.Glob) != "" {
		matcher, err := compileGlob(params.Glob)
		if err != nil {
			return tools.Result{Content: err.Error(), IsError: true}
		}
		matchers = append(matchers, matcher)
	}
	if strings.TrimSpace(params.Type) != "" {
		matchers = append(matchers, typeMatcher(params.Type))
	}
	if len(matchers) > 0 {
		fileMatches = func(rel string) bool {
			for _, matcher := range matchers {
				if !matcher(rel) {
					return false
				}
			}
			return true
		}
	}
	if params.OutputMode == "" {
		params.OutputMode = "files_with_matches"
	}
	if params.OutputMode != "content" && params.OutputMode != "files_with_matches" && params.OutputMode != "count" {
		return tools.Result{Content: fmt.Sprintf("unsupported output_mode: %s", params.OutputMode), IsError: true}
	}
	if params.Context > 0 {
		params.Before = params.Context
		params.After = params.Context
	} else if params.ContextFlag > 0 {
		params.Before = params.ContextFlag
		params.After = params.ContextFlag
	} else {
		if params.BeforeFlag > 0 {
			params.Before = params.BeforeFlag
		}
		if params.AfterFlag > 0 {
			params.After = params.AfterFlag
		}
	}
	showLineNumbers := params.LineNumbers == nil || *params.LineNumbers

	var lines []string
	var files []string
	var countLines []string
	matchCount := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if tools.ShouldSkipSearchDir(root, path) && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		if !fileMatches(filepath.ToSlash(rel)) {
			return nil
		}
		resultLimit := maxMatches
		if params.OutputMode == "count" {
			resultLimit = -1
		}
		displayPath := displayPath(toolContext.CWD, path)
		var found []string
		var count int
		if params.Multiline {
			found, count, err = grepFileMultiline(path, displayPath, re, params.Before, params.After, resultLimit, showLineNumbers)
		} else {
			found, count, err = grepFile(path, displayPath, re, params.Before, params.After, resultLimit, showLineNumbers)
		}
		if err != nil {
			return nil
		}
		if count > 0 {
			matchCount += count
			if params.OutputMode == "count" {
				countLines = append(countLines, fmt.Sprintf("%s:%d", displayPath, count))
			}
		}
		if len(found) > 0 {
			switch params.OutputMode {
			case "content":
				lines = append(lines, found...)
			case "files_with_matches":
				files = append(files, displayPath)
			}
		}
		if len(lines) >= maxMatches || len(files) >= maxMatches {
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if params.OutputMode == "count" {
		sort.Strings(countLines)
		countLines, pageNote := paginateStrings(countLines, params.HeadLimit, params.Offset)
		if len(countLines) == 0 {
			return tools.Result{Content: "No matches found"}
		}
		content := strings.Join(countLines, "\n")
		content += fmt.Sprintf("\n\nFound %d total %s across %d %s.%s", matchCount, plural(matchCount, "occurrence"), len(countLines), plural(len(countLines), "file"), pageNote)
		return tools.Result{Content: content}
	}
	if params.OutputMode == "files_with_matches" {
		if len(files) == 0 {
			return tools.Result{Content: "No files found"}
		}
		sort.Strings(files)
		files, pageNote := paginateStrings(files, params.HeadLimit, params.Offset)
		if len(files) == 0 {
			return tools.Result{Content: "No files found"}
		}
		return tools.Result{Content: fmt.Sprintf("Found %d %s%s\n%s", len(files), plural(len(files), "file"), pageNote, strings.Join(files, "\n"))}
	}
	if len(lines) > maxMatches {
		lines = lines[:maxMatches]
	}
	lines, pageNote := paginateStrings(lines, params.HeadLimit, params.Offset)
	if len(lines) == 0 {
		return tools.Result{Content: "No matches found"}
	}
	content := strings.Join(lines, "\n")
	if pageNote != "" {
		content += "\n\n[Showing results with pagination =" + strings.TrimPrefix(pageNote, " with pagination =") + "]"
	}
	return tools.Result{Content: content}
}

func grepFile(path, displayPath string, re *regexp.Regexp, before, after, maxResults int, showLineNumbers bool) ([]string, int, error) {
	result, err := files.Grep(path, re, before, after, maxResults)
	if err != nil {
		return nil, 0, err
	}
	if result.Binary {
		return nil, 0, nil
	}
	var out []string
	for _, match := range result.Matches {
		out = append(out, formatGrepLine(displayPath, match.LineNumber, match.Line, showLineNumbers))
	}
	return out, result.MatchCount, nil
}

func grepFileMultiline(path, displayPath string, re *regexp.Regexp, before, after, maxResults int, showLineNumbers bool) ([]string, int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	if strings.ContainsRune(string(data), '\x00') {
		return nil, 0, nil
	}
	content := string(data)
	matches := re.FindAllStringIndex(content, -1)
	if maxResults < 0 {
		return nil, len(matches), nil
	}
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	starts := lineStartOffsets(lines)
	seen := map[int]bool{}
	var out []string
	for _, match := range matches {
		lineIndex := lineIndexForOffset(starts, match[0])
		start := lineIndex - before
		if start < 0 {
			start = 0
		}
		end := lineIndex + after
		if end >= len(lines) {
			end = len(lines) - 1
		}
		for i := start; i <= end; i++ {
			if seen[i] {
				continue
			}
			seen[i] = true
			out = append(out, formatGrepLine(displayPath, i+1, lines[i], showLineNumbers))
			if maxResults > 0 && len(out) >= maxResults {
				return out, len(matches), nil
			}
		}
	}
	return out, len(matches), nil
}

func formatGrepLine(path string, lineNumber int, line string, showLineNumbers bool) string {
	if showLineNumbers {
		return fmt.Sprintf("%s:%d:%s", path, lineNumber, line)
	}
	return fmt.Sprintf("%s:%s", path, line)
}

func displayPath(cwd, path string) string {
	rel, err := filepath.Rel(cwd, path)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

func lineStartOffsets(lines []string) []int {
	offsets := make([]int, 0, len(lines))
	offset := 0
	for _, line := range lines {
		offsets = append(offsets, offset)
		offset += len(line) + 1
	}
	return offsets
}

func lineIndexForOffset(starts []int, offset int) int {
	index := sort.Search(len(starts), func(i int) bool { return starts[i] > offset }) - 1
	if index < 0 {
		return 0
	}
	if index >= len(starts) {
		return len(starts) - 1
	}
	return index
}

func paginateStrings(items []string, headLimit *int, offset int) ([]string, string) {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(items) {
		return nil, paginationNote(headLimitValue(headLimit), offset)
	}
	items = items[offset:]
	limit := headLimitValue(headLimit)
	truncated := false
	if limit > 0 && len(items) > limit {
		items = items[:limit]
		truncated = true
	}
	if offset == 0 && !truncated {
		return items, ""
	}
	return items, paginationNote(limit, offset)
}

func headLimitValue(headLimit *int) int {
	if headLimit == nil {
		return defaultHeadLimit
	}
	if *headLimit < 0 {
		return defaultHeadLimit
	}
	return *headLimit
}

func paginationNote(limit, offset int) string {
	var parts []string
	if limit > 0 {
		parts = append(parts, fmt.Sprintf("limit: %d", limit))
	}
	if offset > 0 {
		parts = append(parts, fmt.Sprintf("offset: %d", offset))
	}
	if len(parts) == 0 {
		return ""
	}
	return " with pagination = " + strings.Join(parts, ", ")
}

func compileGlob(pattern string) (func(string) bool, error) {
	return globtool.Compile(pattern)
}

func typeMatcher(fileType string) func(string) bool {
	extensions := typeExtensions(strings.ToLower(strings.TrimPrefix(strings.TrimSpace(fileType), ".")))
	return func(rel string) bool {
		ext := strings.ToLower(filepath.Ext(rel))
		for _, candidate := range extensions {
			if ext == candidate {
				return true
			}
		}
		return false
	}
}

func typeExtensions(fileType string) []string {
	switch fileType {
	case "go", "golang":
		return []string{".go"}
	case "js", "javascript":
		return []string{".js", ".jsx", ".mjs", ".cjs"}
	case "ts", "typescript":
		return []string{".ts", ".tsx", ".mts", ".cts"}
	case "tsx":
		return []string{".tsx"}
	case "jsx":
		return []string{".jsx"}
	case "py", "python":
		return []string{".py"}
	case "rs", "rust":
		return []string{".rs"}
	case "java":
		return []string{".java"}
	case "md", "markdown":
		return []string{".md", ".markdown"}
	case "json":
		return []string{".json"}
	case "yaml", "yml":
		return []string{".yaml", ".yml"}
	default:
		if fileType == "" {
			return nil
		}
		return []string{"." + fileType}
	}
}

func plural(count int, singular string) string {
	if count == 1 {
		return singular
	}
	return singular + "s"
}
