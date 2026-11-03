package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const (
	fullSelection = "__FULL__"
	zeroSHA       = "0000000000000000000000000000000000000000"
)

type packageInfo struct {
	ImportPath   string
	Dir          string
	GoFiles      []string
	CgoFiles     []string
	Imports      []string
	TestImports  []string
	XTestImports []string
}

type selectionMode string

const (
	modeFull selectionMode = "full"
	modeNone selectionMode = "none"
	modeSome selectionMode = "some"
)

type selection struct {
	Mode     selectionMode
	Packages []string
}

func main() {
	base := flag.String("base", "", "git base revision for the changed-file range")
	flag.Parse()
	if strings.TrimSpace(*base) == "" {
		exitError(errors.New("--base is required"))
	}

	root, err := os.Getwd()
	if err != nil {
		exitError(fmt.Errorf("get working directory: %w", err))
	}

	files, err := changedFiles(root, *base)
	if err != nil {
		exitError(err)
	}
	if files == nil {
		fmt.Println(fullSelection)
		return
	}

	packages, err := loadPackages(root)
	if err != nil {
		exitError(err)
	}
	result, err := selectPackages(root, packages, files)
	if err != nil {
		exitError(err)
	}

	switch result.Mode {
	case modeFull:
		fmt.Println(fullSelection)
	case modeNone:
		return
	case modeSome:
		for _, importPath := range result.Packages {
			fmt.Println(importPath)
		}
	default:
		exitError(fmt.Errorf("unknown selection mode %q", result.Mode))
	}
}

func exitError(err error) {
	fmt.Fprintf(os.Stderr, "race-impact: %v\n", err)
	os.Exit(1)
}

func changedFiles(root, base string) ([]string, error) {
	base = strings.TrimSpace(base)
	if base == zeroSHA {
		return nil, nil
	}

	cmd := exec.Command("git", "-C", root, "diff", "--name-only", "--diff-filter=ACMRD", base+"...HEAD")
	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("git diff %s...HEAD: %s", base, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("git diff %s...HEAD: %w", base, err)
	}

	var files []string
	for _, line := range strings.Split(string(output), "\n") {
		line = filepath.ToSlash(strings.TrimSpace(line))
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

func loadPackages(root string) ([]packageInfo, error) {
	cmd := exec.Command("go", "list", "-json", "./...")
	cmd.Dir = root
	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("go list ./...: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("go list ./...: %w", err)
	}

	decoder := json.NewDecoder(strings.NewReader(string(output)))
	var packages []packageInfo
	for decoder.More() {
		var pkg packageInfo
		if err := decoder.Decode(&pkg); err != nil {
			return nil, fmt.Errorf("decode go list package: %w", err)
		}
		packages = append(packages, pkg)
	}
	return packages, nil
}

func selectPackages(root string, packages []packageInfo, files []string) (selection, error) {
	if hasGlobalDependencyChange(files) {
		return selection{Mode: modeFull}, nil
	}

	packageByImport := make(map[string]packageInfo, len(packages))
	for _, pkg := range packages {
		packageByImport[pkg.ImportPath] = pkg
	}

	changedImports := make(map[string]struct{})
	for _, file := range files {
		if !isGoSourceFile(file) {
			continue
		}
		importPath, ok := packageForFile(root, packages, file)
		if !ok {
			return selection{Mode: modeFull}, nil
		}
		changedImports[importPath] = struct{}{}
	}
	if len(changedImports) == 0 {
		return selection{Mode: modeNone}, nil
	}

	reverse := make(map[string][]string)
	for _, pkg := range packages {
		imports := append(append([]string{}, pkg.Imports...), pkg.TestImports...)
		imports = append(imports, pkg.XTestImports...)
		for _, imported := range imports {
			if _, local := packageByImport[imported]; local {
				reverse[imported] = append(reverse[imported], pkg.ImportPath)
			}
		}
	}

	impacted := make(map[string]struct{}, len(changedImports))
	queue := make([]string, 0, len(changedImports))
	for importPath := range changedImports {
		impacted[importPath] = struct{}{}
		queue = append(queue, importPath)
	}
	for len(queue) > 0 {
		importPath := queue[0]
		queue = queue[1:]
		for _, dependent := range reverse[importPath] {
			if _, seen := impacted[dependent]; seen {
				continue
			}
			impacted[dependent] = struct{}{}
			queue = append(queue, dependent)
		}
	}

	result := make([]string, 0, len(impacted))
	for importPath := range impacted {
		result = append(result, importPath)
	}
	sort.Strings(result)
	return selection{Mode: modeSome, Packages: result}, nil
}

func hasGlobalDependencyChange(files []string) bool {
	for _, file := range files {
		switch file {
		case "go.mod", "go.sum", "go.work", "go.work.sum":
			return true
		}
		if file == "vendor" || strings.HasPrefix(file, "vendor/") ||
			file == "third_party" || strings.HasPrefix(file, "third_party/") {
			return true
		}
	}
	return false
}

func isGoSourceFile(file string) bool {
	switch strings.ToLower(filepath.Ext(file)) {
	case ".go", ".c", ".cc", ".cpp", ".h", ".hh", ".s", ".asm":
		return true
	default:
		return false
	}
}

func packageForFile(root string, packages []packageInfo, file string) (string, bool) {
	absoluteFile := filepath.Join(root, filepath.FromSlash(file))
	var best packageInfo
	bestDepth := -1
	for _, pkg := range packages {
		relative, err := filepath.Rel(pkg.Dir, absoluteFile)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		depth := strings.Count(filepath.Clean(pkg.Dir), string(filepath.Separator))
		if depth > bestDepth {
			best = pkg
			bestDepth = depth
		}
	}
	if bestDepth < 0 {
		return "", false
	}
	return best.ImportPath, true
}
