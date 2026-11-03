package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/konglong87/super-niuma/internal/tools"
)

type Tool struct{}

func New() Tool { return Tool{} }

func (Tool) Name() string { return "LSP" }

func (Tool) MaxResultSizeChars() int { return tools.ClaudeCodeDefaultDeclaredMaxResultSizeChars }

func (Tool) Description() string {
	return `Search Go declarations and identifiers through a built-in go/parser index.

Despite the name this is NOT a language server: it does not start gopls or speak
LSP, and it performs no type resolution. It parses .go files with go/parser and
indexes what it finds. Actions:
- "symbols": declarations whose name contains the query (case-insensitive substring)
- "definition": declarations whose name equals the query exactly
- "references": identifier occurrences with a matching name — name-based only, so
  same-named symbols from other packages, local variables, methods, and struct
  fields all come back together; it cannot tell them apart
- "diagnostics": go/parser syntax errors only — no type errors, no vet, no build

Go only; no other language is supported. .git, vendor, and dot directories are
skipped. Results are limited to 50 by default (configurable via limit).
For non-Go code, or when you want literal text matches, use Grep.`
}

func (Tool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
	  "type": "object",
	  "properties": {
	    "action": {"type": "string", "enum": ["symbols", "definition", "references", "diagnostics"], "description": "Language action to run."},
	    "query": {"type": "string", "description": "Symbol name or substring for symbols/definition/references."},
	    "path": {"type": "string", "description": "Optional file or directory scope relative to cwd."},
	    "limit": {"type": "integer", "description": "Maximum results. Default 50."}
	  },
	  "required": ["action"],
	  "additionalProperties": false
	}`)
}

func (Tool) Run(_ context.Context, input json.RawMessage, toolContext tools.Context) tools.Result {
	var params struct {
		Action string `json:"action"`
		Query  string `json:"query"`
		Path   string `json:"path"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if params.Limit <= 0 {
		params.Limit = 50
	}
	root := toolContext.CWD
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	scope := root
	if strings.TrimSpace(params.Path) != "" {
		resolved, err := tools.ResolvePath(root, params.Path)
		if err != nil {
			return tools.Result{Content: err.Error(), IsError: true}
		}
		scope = resolved
	}
	index, err := buildIndex(scope)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	switch strings.ToLower(strings.TrimSpace(params.Action)) {
	case "symbols":
		return encode(symbols(index, params.Query, params.Limit))
	case "definition":
		return encode(definitions(index, params.Query, params.Limit))
	case "references":
		return encode(references(index, params.Query, params.Limit))
	case "diagnostics":
		return encode(diagnostics(index, params.Limit))
	default:
		return tools.Result{Content: "unsupported LSP action: " + params.Action, IsError: true}
	}
}

type location struct {
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

type symbol struct {
	Name     string   `json:"name"`
	Kind     string   `json:"kind"`
	Location location `json:"location"`
}

type reference struct {
	Name     string   `json:"name"`
	Location location `json:"location"`
	Context  string   `json:"context,omitempty"`
}

type diagnostic struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Message string `json:"message"`
}

type index struct {
	symbols     []symbol
	references  []reference
	diagnostics []diagnostic
}

func buildIndex(scope string) (index, error) {
	var idx index
	files, err := goFiles(scope)
	if err != nil {
		return idx, err
	}
	for _, path := range files {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.AllErrors)
		if err != nil {
			idx.diagnostics = append(idx.diagnostics, diagnosticFromError(path, err))
			file, _ = parser.ParseFile(fset, path, nil, parser.ParseComments)
		}
		if file == nil {
			continue
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.FuncDecl:
				idx.symbols = append(idx.symbols, symbol{Name: n.Name.Name, Kind: "function", Location: pos(fset, n.Name.Pos())})
			case *ast.TypeSpec:
				idx.symbols = append(idx.symbols, symbol{Name: n.Name.Name, Kind: "type", Location: pos(fset, n.Name.Pos())})
			case *ast.ValueSpec:
				kind := "variable"
				for _, name := range n.Names {
					idx.symbols = append(idx.symbols, symbol{Name: name.Name, Kind: kind, Location: pos(fset, name.Pos())})
				}
			case *ast.Ident:
				if n.Name != "_" {
					idx.references = append(idx.references, reference{Name: n.Name, Location: pos(fset, n.Pos())})
				}
			}
			return true
		})
	}
	sort.Slice(idx.symbols, func(i, j int) bool {
		if idx.symbols[i].Location.Path == idx.symbols[j].Location.Path {
			return idx.symbols[i].Location.Line < idx.symbols[j].Location.Line
		}
		return idx.symbols[i].Location.Path < idx.symbols[j].Location.Path
	})
	return idx, nil
}

func goFiles(scope string) ([]string, error) {
	info, err := os.Stat(scope)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		if strings.EqualFold(filepath.Ext(scope), ".go") {
			return []string{scope}, nil
		}
		return nil, fmt.Errorf("LSP path must be a Go file or directory: %s", scope)
	}
	var files []string
	err = filepath.WalkDir(scope, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			name := entry.Name()
			if name == ".git" || name == "vendor" || strings.HasPrefix(name, ".") && path != scope {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.EqualFold(filepath.Ext(entry.Name()), ".go") {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

func symbols(idx index, query string, limit int) []symbol {
	var out []symbol
	query = strings.ToLower(strings.TrimSpace(query))
	for _, item := range idx.symbols {
		if query == "" || strings.Contains(strings.ToLower(item.Name), query) {
			out = append(out, item)
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}

func definitions(idx index, query string, limit int) []symbol {
	var out []symbol
	query = strings.TrimSpace(query)
	for _, item := range idx.symbols {
		if item.Name == query {
			out = append(out, item)
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}

func references(idx index, query string, limit int) []reference {
	var out []reference
	query = strings.TrimSpace(query)
	for _, item := range idx.references {
		if item.Name == query {
			out = append(out, item)
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}

func diagnostics(idx index, limit int) []diagnostic {
	if len(idx.diagnostics) > limit {
		return idx.diagnostics[:limit]
	}
	return idx.diagnostics
}

func pos(fset *token.FileSet, p token.Pos) location {
	position := fset.Position(p)
	return location{Path: position.Filename, Line: position.Line, Column: position.Column}
}

func diagnosticFromError(path string, err error) diagnostic {
	if list, ok := err.(scanner.ErrorList); ok && len(list) > 0 {
		first := list[0]
		return diagnostic{Path: first.Pos.Filename, Line: first.Pos.Line, Column: first.Pos.Column, Message: first.Msg}
	}
	return diagnostic{Path: path, Message: err.Error()}
}

func encode(value any) tools.Result {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	return tools.Result{Content: string(data)}
}
