package cli

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/konglong87/super-niuma/internal/memory"
	"github.com/konglong87/super-niuma/internal/memorylint"
)

type memoryLintOptions struct {
	scope memory.LoadScope
	json  bool
}

type memoryLintOutput struct {
	Scope     memory.LoadScope     `json:"scope"`
	Documents int                  `json:"documents"`
	Findings  []memorylint.Finding `json:"findings"`
}

func memoryCommand(args []string, cwd string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: memory lint [--scope workspace|all] [--json]")
	}
	if args[0] != "lint" {
		return fmt.Errorf("unknown memory command: %s", args[0])
	}
	opts, err := parseMemoryLintArgs(args[1:])
	if err != nil {
		return err
	}
	docs, err := memory.LoadForScope(cwd, opts.scope)
	if err != nil {
		return err
	}
	paths := uniqueDocumentPaths(docs)
	report, err := memorylint.CheckFiles(paths)
	if err != nil {
		return err
	}
	output := memoryLintOutput{Scope: opts.scope, Documents: report.Documents, Findings: report.Findings}
	if opts.json {
		if err := writePrettyJSON(stdout, output); err != nil {
			return err
		}
	} else if err := writeMemoryLintHuman(stdout, output); err != nil {
		return err
	}
	if len(report.Findings) > 0 {
		return fmt.Errorf("memory lint found %d broken link(s)", len(report.Findings))
	}
	return nil
}

func writeMemoryLintHuman(stdout io.Writer, output memoryLintOutput) error {
	for _, finding := range output.Findings {
		if _, err := fmt.Fprintf(stdout, "%s:%d  %s  target does not exist: %s\n", finding.Path, finding.Line, finding.Kind, finding.Target); err != nil {
			return err
		}
	}
	if len(output.Findings) == 0 {
		_, err := fmt.Fprintf(stdout, "memory lint: no broken links (%d documents, scope=%s)\n", output.Documents, output.Scope)
		return err
	}
	_, err := fmt.Fprintf(stdout, "memory lint: %d broken link(s) in %d document(s), scope=%s\n", len(output.Findings), output.Documents, output.Scope)
	return err
}

func parseMemoryLintArgs(args []string) (memoryLintOptions, error) {
	opts := memoryLintOptions{scope: memory.LoadScopeWorkspace}
	var scopeSet, jsonSet bool
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--scope":
			if scopeSet {
				return opts, errors.New("--scope may only be specified once")
			}
			if i+1 >= len(args) {
				return opts, errors.New("--scope requires a value")
			}
			i++
			scopeSet = true
			switch args[i] {
			case string(memory.LoadScopeWorkspace):
				opts.scope = memory.LoadScopeWorkspace
			case string(memory.LoadScopeAll):
				opts.scope = memory.LoadScopeAll
			default:
				return opts, fmt.Errorf("--scope must be workspace or all, got %q", args[i])
			}
		case "--json":
			if jsonSet {
				return opts, errors.New("--json may only be specified once")
			}
			jsonSet = true
			opts.json = true
		default:
			if strings.HasPrefix(args[i], "-") {
				return opts, fmt.Errorf("unknown flag: %s", args[i])
			}
			return opts, fmt.Errorf("memory lint does not accept positional argument: %s", args[i])
		}
	}
	return opts, nil
}

func uniqueDocumentPaths(docs []memory.Document) []string {
	seen := make(map[string]struct{}, len(docs))
	paths := make([]string, 0, len(docs))
	for _, doc := range docs {
		path := filepath.Clean(strings.TrimSpace(doc.Path))
		if path == "." || path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	return paths
}
