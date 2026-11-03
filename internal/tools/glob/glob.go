package glob

import (
	"context"
	"encoding/json"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/konglong87/super-niuma/internal/tools"
)

const maxResults = 1000

type Tool struct{}

func New() Tool { return Tool{} }

func (Tool) Name() string { return "Glob" }

func (Tool) ExecutionPolicy() tools.ExecutionPolicy {
	return tools.ExecutionPolicy{Concurrency: tools.ConcurrencyReadOnly}
}

func (Tool) MaxResultSizeChars() int { return tools.ClaudeCodeDefaultDeclaredMaxResultSizeChars }

func (Tool) Description() string {
	return `Find files by glob pattern. Supports *, ?, and ** across directories.

Use Glob to discover file paths before reading them, or to find files matching
a pattern. Results are sorted by modification time (newest first).

Results are limited to 1000 entries. Common patterns:
- **/*.go — all Go files recursively
- src/**/*.ts — all TypeScript files under src/
- *.json — JSON files in current directory only

Use Glob to verify uncertain paths before Read. Prefer narrow patterns and the
limit parameter when you only need a few candidate files.

For searching file CONTENTS, use Grep instead. For listing directories, use LS.`
}

func (Tool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
	  "type": "object",
	  "properties": {
	    "pattern": {"type": "string", "description": "Glob pattern such as **/*.go."},
	    "path": {"type": "string", "description": "Directory to search. Defaults to current working directory."},
	    "limit": {"type": "integer", "description": "Maximum number of files to return. Default 1000."}
	  },
	  "required": ["pattern"],
	  "additionalProperties": false
	}`)
}

func (Tool) Run(_ context.Context, input json.RawMessage, toolContext tools.Context) tools.Result {
	var params struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
		Limit   int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if params.Pattern == "" {
		return tools.Result{Content: "pattern is required", IsError: true}
	}
	root, err := tools.ResolvePath(toolContext.CWD, params.Path)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	matcher, err := Compile(params.Pattern)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	limit := params.Limit
	if limit <= 0 || limit > maxResults {
		limit = maxResults
	}

	type match struct {
		path    string
		modTime int64
	}
	var matches []match
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
		rel = filepath.ToSlash(rel)
		if matcher(rel) {
			info, err := d.Info()
			if err != nil {
				return nil
			}
			matches = append(matches, match{path: path, modTime: info.ModTime().UnixNano()})
		}
		return nil
	})
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if len(matches) == 0 {
		return tools.Result{Content: "No files found"}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].modTime == matches[j].modTime {
			return matches[i].path < matches[j].path
		}
		return matches[i].modTime > matches[j].modTime
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		out = append(out, match.path)
	}
	return tools.Result{Content: strings.Join(out, "\n")}
}

func Compile(pattern string) (func(string) bool, error) {
	re, err := regexp.Compile("^" + globToRegex(filepath.ToSlash(pattern)) + "$")
	if err != nil {
		return nil, err
	}
	return re.MatchString, nil
}

func globToRegex(pattern string) string {
	var out strings.Builder
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		switch ch {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				if i+2 < len(pattern) && pattern[i+2] == '/' {
					out.WriteString("(?:.*/)?")
					i += 2
				} else {
					out.WriteString(".*")
					i++
				}
			} else {
				out.WriteString("[^/]*")
			}
		case '?':
			out.WriteString("[^/]")
		default:
			out.WriteString(regexp.QuoteMeta(string(ch)))
		}
	}
	return out.String()
}
