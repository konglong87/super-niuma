package memorylint

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

const FindingKindBrokenLink = "broken_link"

type Finding struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Target string `json:"target"`
}

type Report struct {
	Documents int       `json:"documents"`
	Findings  []Finding `json:"findings"`
}

type fileOps struct {
	readFile func(string) ([]byte, error)
	stat     func(string) (fs.FileInfo, error)
}

func CheckFiles(paths []string) (Report, error) {
	return checkFiles(paths, fileOps{readFile: os.ReadFile, stat: os.Stat})
}

func checkFiles(paths []string, ops fileOps) (Report, error) {
	normalized, err := normalizeAndSortPaths(paths)
	if err != nil {
		return Report{}, err
	}
	report := Report{Documents: len(normalized), Findings: make([]Finding, 0)}
	markdown := goldmark.New()
	for _, path := range normalized {
		findings, err := checkFile(path, ops, markdown)
		if err != nil {
			return Report{}, err
		}
		report.Findings = append(report.Findings, findings...)
	}
	report.Findings = sortAndDeduplicateFindings(report.Findings)
	return report, nil
}

func normalizeAndSortPaths(paths []string) ([]string, error) {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("resolve source path %q: %w", path, err)
		}
		abs = filepath.Clean(abs)
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		out = append(out, abs)
	}
	sort.Strings(out)
	return out, nil
}

func checkFile(path string, ops fileOps, markdown goldmark.Markdown) ([]Finding, error) {
	source, err := ops.readFile(path)
	if err != nil {
		return nil, fmt.Errorf("read memory source %q: %w", path, err)
	}
	source = maskFrontmatterPreserveLines(source)
	document := markdown.Parser().Parse(text.NewReader(source))
	findings := make([]Finding, 0)
	err = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		var destination []byte
		switch node := node.(type) {
		case *ast.Link:
			destination = node.Destination
		case *ast.Image:
			destination = node.Destination
		default:
			return ast.WalkContinue, nil
		}
		target := string(destination)
		line := nodeLine(node, source)
		resolved, local, err := normalizeLocalTarget(filepath.Dir(path), destination)
		if err != nil {
			return ast.WalkStop, fmt.Errorf("parse link target %q at %s:%d: %w", target, path, line, err)
		}
		if !local {
			return ast.WalkContinue, nil
		}
		_, err = ops.stat(resolved)
		switch {
		case err == nil:
			return ast.WalkContinue, nil
		case errors.Is(err, fs.ErrNotExist):
			findings = append(findings, Finding{
				Kind:   FindingKindBrokenLink,
				Path:   path,
				Line:   line,
				Target: target,
			})
			return ast.WalkContinue, nil
		default:
			return ast.WalkStop, fmt.Errorf("stat link target %q from %s:%d: %w", resolved, path, line, err)
		}
	})
	if err != nil {
		return nil, err
	}
	return findings, nil
}

func maskFrontmatterPreserveLines(source []byte) []byte {
	normalized := bytes.ReplaceAll(source, []byte("\r\n"), []byte("\n"))
	if !bytes.HasPrefix(normalized, []byte("---\n")) {
		return source
	}
	end := bytes.Index(normalized[4:], []byte("\n---"))
	if end < 0 {
		return source
	}
	masked := append([]byte(nil), normalized...)
	closingEnd := 4 + end + len("\n---")
	for i := 0; i < closingEnd; i++ {
		if masked[i] != '\n' {
			masked[i] = ' '
		}
	}
	return masked
}

func normalizeLocalTarget(sourceDir string, destination []byte) (string, bool, error) {
	decoded := util.UnescapePunctuations(destination)
	decoded = util.ResolveNumericReferences(decoded)
	decoded = util.ResolveEntityNames(decoded)
	raw := string(decoded)
	if raw == "" || isNetworkPath(raw) {
		return "", false, nil
	}
	abs := filepath.IsAbs(raw)
	if abs && runtime.GOOS == "windows" {
		path, err := parseWindowsAbsoluteTarget(raw)
		return path, err == nil, err
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", false, err
	}
	if !abs && (parsed.Scheme != "" || parsed.Host != "") {
		return "", false, nil
	}
	if parsed.Path == "" {
		return "", false, nil
	}
	path, err := url.PathUnescape(parsed.EscapedPath())
	if err != nil {
		return "", false, err
	}
	if isNetworkPath(path) {
		return "", false, nil
	}
	path = filepath.FromSlash(path)
	if abs {
		return filepath.Clean(path), true, nil
	}
	return filepath.Clean(filepath.Join(sourceDir, path)), true, nil
}

func isNetworkPath(path string) bool {
	return strings.HasPrefix(path, "//") || strings.HasPrefix(path, `\\`)
}

func parseWindowsAbsoluteTarget(raw string) (string, error) {
	volume := filepath.VolumeName(raw)
	slashed := filepath.ToSlash(raw)
	volumeSlashed := filepath.ToSlash(volume)
	rest := strings.TrimPrefix(slashed, volumeSlashed)
	parsed, err := url.Parse(rest)
	if err != nil {
		return "", err
	}
	path, err := url.PathUnescape(parsed.EscapedPath())
	if err != nil {
		return "", err
	}
	return filepath.Clean(volume + filepath.FromSlash(path)), nil
}

func nodeLine(node ast.Node, source []byte) int {
	if start, ok := firstTextSegmentStart(node); ok {
		return lineAtOffset(source, start)
	}
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		if parent.Type() != ast.TypeBlock || parent.Lines() == nil || parent.Lines().Len() == 0 {
			continue
		}
		return lineAtOffset(source, parent.Lines().At(0).Start)
	}
	return 1
}

func firstTextSegmentStart(node ast.Node) (int, bool) {
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		if textNode, ok := child.(*ast.Text); ok {
			return textNode.Segment.Start, true
		}
		if start, ok := firstTextSegmentStart(child); ok {
			return start, true
		}
	}
	return 0, false
}

func lineAtOffset(source []byte, offset int) int {
	if offset < 0 {
		offset = 0
	}
	if offset > len(source) {
		offset = len(source)
	}
	return bytes.Count(source[:offset], []byte{'\n'}) + 1
}

func sortAndDeduplicateFindings(findings []Finding) []Finding {
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Path != findings[j].Path {
			return findings[i].Path < findings[j].Path
		}
		if findings[i].Line != findings[j].Line {
			return findings[i].Line < findings[j].Line
		}
		return findings[i].Target < findings[j].Target
	})
	out := make([]Finding, 0, len(findings))
	for _, finding := range findings {
		if len(out) > 0 && out[len(out)-1] == finding {
			continue
		}
		out = append(out, finding)
	}
	return out
}
