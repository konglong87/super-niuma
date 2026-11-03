package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const defaultRegistryPath = "docs/architecture/runtime_topology.yaml"

type cliOptions struct {
	repoRoot    string
	registry    string
	base        string
	head        string
	workingTree bool
	impact      string
	blastRadius string
	reason      string
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	opts, err := parseFlags(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	root, err := resolveRepoRoot(opts.repoRoot)
	if err != nil {
		fmt.Fprintf(stderr, "resolve repository root: %v\n", err)
		return 2
	}
	registryPath := opts.registry
	if !filepath.IsAbs(registryPath) {
		registryPath = filepath.Join(root, filepath.FromSlash(registryPath))
	}
	registryPath, err = filepath.Abs(registryPath)
	if err != nil {
		fmt.Fprintf(stderr, "resolve registry path: %v\n", err)
		return 2
	}
	registryRelative, err := filepath.Rel(root, registryPath)
	if err != nil {
		fmt.Fprintf(stderr, "resolve registry relative path: %v\n", err)
		return 2
	}
	registryRelative = filepath.ToSlash(registryRelative)

	registry, err := readTopologyRegistry(registryPath)
	if err != nil {
		fmt.Fprintf(stderr, "read topology registry: %v\n", err)
		return 1
	}
	repoFiles, err := listRepoFiles(root)
	if err != nil {
		fmt.Fprintf(stderr, "list repository files: %v\n", err)
		return 2
	}
	if problems := validateTopologyRegistry(root, registryRelative, registry, repoFiles); len(problems) > 0 {
		printProblems(stderr, problems)
		return 1
	}

	fmt.Fprintf(stdout, "runtime topology registry: valid (%d nodes, %d views)\n", len(registry.Nodes), len(registry.Views))
	if strings.TrimSpace(opts.base) == "" {
		fmt.Fprintln(stdout, "runtime topology impact: static validation only (no --base supplied)")
		return 0
	}

	var changed []string
	if opts.workingTree {
		changed, err = gitWorkingTreeChangedFiles(root, opts.base)
	} else {
		changed, err = gitChangedFiles(root, opts.base, opts.head)
	}
	if err != nil {
		fmt.Fprintf(stderr, "read changed files: %v\n", err)
		return 2
	}
	declaration := impactDeclaration{
		Impact:      opts.impact,
		BlastRadius: opts.blastRadius,
		Reason:      opts.reason,
	}
	eventDeclaration, pullRequestEvent := declarationFromGitHubEvent()
	declaration = fillDeclaration(declaration, eventDeclaration)
	if declaration.incomplete() && !pullRequestEvent {
		messages, logErr := gitCommitMessages(root, opts.base, opts.head)
		if logErr != nil {
			fmt.Fprintf(stderr, "read commit topology declaration: %v\n", logErr)
			return 2
		}
		declaration = fillDeclaration(declaration, parseImpactDeclaration(messages))
	}
	declaration = normalizeDeclaration(declaration)

	report, problems := evaluateTopologyImpact(registryRelative, registry, changed, declaration)
	if len(report.AffectedNodes) > 0 {
		fmt.Fprintf(stdout, "runtime topology affected nodes: %s\n", strings.Join(report.AffectedNodes, ", "))
		fmt.Fprintf(stdout, "runtime topology blast radius: %s\n", declaration.BlastRadius)
		fmt.Fprintf(stdout, "runtime topology impact: %s\n", declaration.Impact)
		for _, testCommand := range report.RecommendedTests {
			fmt.Fprintf(stdout, "runtime topology recommended test: %s\n", testCommand)
		}
	} else {
		fmt.Fprintln(stdout, "runtime topology affected nodes: none")
	}
	if len(problems) > 0 {
		printProblems(stderr, problems)
		return 1
	}
	return 0
}

func parseFlags(args []string, stderr io.Writer) (cliOptions, error) {
	var opts cliOptions
	set := flag.NewFlagSet("runtime-topology-check", flag.ContinueOnError)
	set.SetOutput(stderr)
	set.StringVar(&opts.repoRoot, "repo-root", "", "repository root; defaults to git rev-parse --show-toplevel")
	set.StringVar(&opts.registry, "registry", defaultRegistryPath, "topology registry path relative to the repository root")
	set.StringVar(&opts.base, "base", "", "base Git revision for impact validation; omit for static validation")
	set.StringVar(&opts.head, "head", "HEAD", "head Git revision for impact validation")
	set.BoolVar(&opts.workingTree, "working-tree", false, "compare the base revision with the current index, working tree, and untracked files")
	set.StringVar(&opts.impact, "impact", "", "topology impact declaration: updated or none")
	set.StringVar(&opts.blastRadius, "blast-radius", "", "blast radius declaration: B0_LOCAL through B5_SHARED_STATE")
	set.StringVar(&opts.reason, "reason", "", "reason for the topology impact declaration")
	set.Usage = func() {
		fmt.Fprintln(stderr, "Usage: go run ./scripts/runtime-topology-check [flags]")
		set.PrintDefaults()
	}
	if err := set.Parse(args); err != nil {
		return cliOptions{}, err
	}
	if set.NArg() != 0 {
		set.Usage()
		return cliOptions{}, errors.New("unexpected positional arguments")
	}
	if opts.workingTree && strings.TrimSpace(opts.base) == "" {
		return cliOptions{}, errors.New("--working-tree requires --base")
	}
	return opts, nil
}

func resolveRepoRoot(explicit string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		return filepath.Abs(explicit)
	}
	command := exec.Command("git", "rev-parse", "--show-toplevel")
	output, err := command.Output()
	if err != nil {
		return "", err
	}
	return filepath.Abs(strings.TrimSpace(string(output)))
}

func listRepoFiles(root string) ([]string, error) {
	command := exec.Command("git", "-C", root, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	output, err := command.Output()
	if err != nil {
		return nil, err
	}
	return splitNULPaths(output), nil
}

func gitChangedFiles(root, base, head string) ([]string, error) {
	rangeSpec := strings.TrimSpace(base) + "..." + strings.TrimSpace(head)
	command := exec.Command("git", "-C", root, "diff", "--name-only", "--diff-filter=ACMRTUXBD", "-z", rangeSpec)
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, commandError(command, output, err)
	}
	return splitNULPaths(output), nil
}

func gitWorkingTreeChangedFiles(root, base string) ([]string, error) {
	diffCommand := exec.Command("git", "-C", root, "diff", "--name-only", "--diff-filter=ACMRTUXBD", "-z", strings.TrimSpace(base), "--")
	diffOutput, err := diffCommand.CombinedOutput()
	if err != nil {
		return nil, commandError(diffCommand, diffOutput, err)
	}
	untrackedCommand := exec.Command("git", "-C", root, "ls-files", "--others", "--exclude-standard", "-z")
	untrackedOutput, err := untrackedCommand.CombinedOutput()
	if err != nil {
		return nil, commandError(untrackedCommand, untrackedOutput, err)
	}
	return uniqueSorted(append(splitNULPaths(diffOutput), splitNULPaths(untrackedOutput)...)), nil
}

func gitCommitMessages(root, base, head string) (string, error) {
	rangeSpec := strings.TrimSpace(base) + ".." + strings.TrimSpace(head)
	command := exec.Command("git", "-C", root, "log", "--format=%B%n", rangeSpec)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", commandError(command, output, err)
	}
	return string(output), nil
}

func commandError(command *exec.Cmd, output []byte, err error) error {
	if len(output) == 0 {
		return fmt.Errorf("%s: %w", strings.Join(command.Args, " "), err)
	}
	return fmt.Errorf("%s: %w: %s", strings.Join(command.Args, " "), err, strings.TrimSpace(string(output)))
}

func splitNULPaths(output []byte) []string {
	parts := strings.Split(string(output), "\x00")
	paths := make([]string, 0, len(parts))
	for _, item := range parts {
		item = normalizeRepoPath(item)
		if item != "" {
			paths = append(paths, item)
		}
	}
	return uniqueSorted(paths)
}

func declarationFromGitHubEvent() (impactDeclaration, bool) {
	eventPath := strings.TrimSpace(os.Getenv("GITHUB_EVENT_PATH"))
	if eventPath == "" {
		return impactDeclaration{}, false
	}
	data, err := os.ReadFile(eventPath)
	if err != nil {
		return impactDeclaration{}, false
	}
	var event struct {
		PullRequest *struct {
			Body string `json:"body"`
		} `json:"pull_request"`
	}
	if json.Unmarshal(data, &event) != nil {
		return impactDeclaration{}, false
	}
	if event.PullRequest == nil {
		return impactDeclaration{}, false
	}
	return parseImpactDeclaration(event.PullRequest.Body), true
}

func printProblems(writer io.Writer, problems []error) {
	for _, problem := range problems {
		fmt.Fprintf(writer, "ERROR: %v\n", problem)
	}
}
