package main

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const supportedRegistryVersion = 1

type topologyRegistry struct {
	Version       int              `yaml:"version"`
	Document      string           `yaml:"document"`
	CoverageRoots []string         `yaml:"coverage_roots"`
	ViewRoots     []string         `yaml:"view_roots"`
	Nodes         []topologyNode   `yaml:"nodes"`
	Views         []topologyView   `yaml:"views"`
	Anchors       []topologyAnchor `yaml:"anchors"`
}

type topologyNode struct {
	ID         string   `yaml:"id"`
	Summary    string   `yaml:"summary"`
	Paths      []string `yaml:"paths"`
	Downstream []string `yaml:"downstream"`
	Tests      []string `yaml:"tests"`
}

type topologyView struct {
	ID        string   `yaml:"id"`
	Source    string   `yaml:"source"`
	Artifacts []string `yaml:"artifacts"`
	Covers    []string `yaml:"covers"`
}

type topologyAnchor struct {
	Path    string   `yaml:"path"`
	Symbols []string `yaml:"symbols"`
}

func readTopologyRegistry(registryPath string) (topologyRegistry, error) {
	data, err := os.ReadFile(registryPath)
	if err != nil {
		return topologyRegistry{}, err
	}
	var registry topologyRegistry
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&registry); err != nil {
		return topologyRegistry{}, err
	}
	return registry, nil
}

func validateTopologyRegistry(root, registryPath string, registry topologyRegistry, repoFiles []string) []error {
	var problems []error
	var documentData []byte
	if registry.Version != supportedRegistryVersion {
		problems = append(problems, fmt.Errorf("registry version is %d, want %d", registry.Version, supportedRegistryVersion))
	}
	if err := validateRepoPath(registry.Document); err != nil {
		problems = append(problems, fmt.Errorf("document path: %w", err))
	} else if !regularFileExists(root, registry.Document) {
		problems = append(problems, fmt.Errorf("document %q does not exist", registry.Document))
	} else {
		var err error
		documentData, err = os.ReadFile(filepath.Join(root, filepath.FromSlash(registry.Document)))
		if err != nil {
			problems = append(problems, fmt.Errorf("read document %q: %w", registry.Document, err))
		}
	}
	if len(registry.CoverageRoots) == 0 {
		problems = append(problems, fmt.Errorf("coverage_roots must not be empty"))
	}
	for _, coverageRoot := range registry.CoverageRoots {
		if err := validateRepoPath(coverageRoot); err != nil {
			problems = append(problems, fmt.Errorf("coverage root %q: %w", coverageRoot, err))
		} else if !anyPathMatches(repoFiles, coverageRoot) {
			problems = append(problems, fmt.Errorf("coverage root %q matches no repository file", coverageRoot))
		}
	}
	if len(registry.ViewRoots) == 0 {
		problems = append(problems, fmt.Errorf("view_roots must not be empty"))
	}
	for _, viewRoot := range registry.ViewRoots {
		if err := validateRepoPath(viewRoot); err != nil {
			problems = append(problems, fmt.Errorf("view root %q: %w", viewRoot, err))
		} else if !anyPathMatches(repoFiles, viewRoot) {
			problems = append(problems, fmt.Errorf("view root %q matches no repository file", viewRoot))
		}
	}

	nodeByID := make(map[string]topologyNode, len(registry.Nodes))
	for _, node := range registry.Nodes {
		if !validNodeID(node.ID) {
			problems = append(problems, fmt.Errorf("node id %q must match RT-[A-Z0-9-]+", node.ID))
			continue
		}
		if _, duplicate := nodeByID[node.ID]; duplicate {
			problems = append(problems, fmt.Errorf("node id %q is duplicated", node.ID))
			continue
		}
		nodeByID[node.ID] = node
		if len(documentData) > 0 && !bytes.Contains(documentData, []byte(node.ID)) {
			problems = append(problems, fmt.Errorf("document %q does not mention node %s", registry.Document, node.ID))
		}
		if strings.TrimSpace(node.Summary) == "" {
			problems = append(problems, fmt.Errorf("node %s has no summary", node.ID))
		}
		if len(node.Paths) == 0 {
			problems = append(problems, fmt.Errorf("node %s has no paths", node.ID))
		}
		if len(node.Tests) == 0 {
			problems = append(problems, fmt.Errorf("node %s has no tests", node.ID))
		}
		for _, prefix := range node.Paths {
			if err := validateRepoPath(prefix); err != nil {
				problems = append(problems, fmt.Errorf("node %s path %q: %w", node.ID, prefix, err))
				continue
			}
			if !anyPathMatches(repoFiles, prefix) {
				problems = append(problems, fmt.Errorf("node %s path %q matches no repository file", node.ID, prefix))
			}
		}
	}
	for _, node := range registry.Nodes {
		for _, downstream := range node.Downstream {
			if _, ok := nodeByID[downstream]; !ok {
				problems = append(problems, fmt.Errorf("node %s references unknown downstream node %s", node.ID, downstream))
			}
		}
	}
	for _, repoFile := range repoFiles {
		if !matchesAnyPrefix(repoFile, registry.CoverageRoots) {
			continue
		}
		if len(nodesForPath(registry, repoFile)) == 0 {
			problems = append(problems, fmt.Errorf("runtime file %q is not mapped to any RT-* node", repoFile))
		}
	}

	viewIDs := make(map[string]struct{}, len(registry.Views))
	coveredNodeIDs := make(map[string]struct{}, len(nodeByID))
	registeredViewFiles := make(map[string]struct{})
	for _, view := range registry.Views {
		if strings.TrimSpace(view.ID) == "" {
			problems = append(problems, fmt.Errorf("topology view has an empty id"))
		} else if _, duplicate := viewIDs[view.ID]; duplicate {
			problems = append(problems, fmt.Errorf("topology view id %q is duplicated", view.ID))
		} else {
			viewIDs[view.ID] = struct{}{}
		}
		if path.Ext(view.Source) != ".mmd" {
			problems = append(problems, fmt.Errorf("view %s source %q must be a .mmd file", view.ID, view.Source))
		}
		if len(documentData) > 0 && !bytes.Contains(documentData, []byte(path.Base(view.Source))) {
			problems = append(problems, fmt.Errorf("document %q does not mention view source %q", registry.Document, path.Base(view.Source)))
		}
		validateExistingPath(root, "view "+view.ID+" source", view.Source, &problems)
		registeredViewFiles[normalizeRepoPath(view.Source)] = struct{}{}
		if len(view.Artifacts) == 0 {
			problems = append(problems, fmt.Errorf("view %s has no rendered artifacts", view.ID))
		}
		sourceStem := strings.TrimSuffix(view.Source, path.Ext(view.Source))
		extensions := make(map[string]struct{}, len(view.Artifacts))
		for _, artifact := range view.Artifacts {
			validateExistingPath(root, "view "+view.ID+" artifact", artifact, &problems)
			registeredViewFiles[normalizeRepoPath(artifact)] = struct{}{}
			extensions[path.Ext(artifact)] = struct{}{}
			artifactStem := strings.TrimSuffix(artifact, path.Ext(artifact))
			if artifactStem != sourceStem {
				problems = append(problems, fmt.Errorf("view %s artifact %q does not share source stem %q", view.ID, artifact, sourceStem))
			}
		}
		for _, required := range []string{".svg", ".png", ".excalidraw"} {
			if _, ok := extensions[required]; !ok {
				problems = append(problems, fmt.Errorf("view %s is missing %s artifact", view.ID, required))
			}
		}
		for _, covered := range view.Covers {
			if _, ok := nodeByID[covered]; !ok {
				problems = append(problems, fmt.Errorf("view %s covers unknown node %s", view.ID, covered))
			} else {
				coveredNodeIDs[covered] = struct{}{}
			}
		}
	}
	for nodeID := range nodeByID {
		if _, covered := coveredNodeIDs[nodeID]; !covered {
			problems = append(problems, fmt.Errorf("node %s is not covered by any topology view", nodeID))
		}
	}
	for _, repoFile := range repoFiles {
		if !matchesAnyPrefix(repoFile, registry.ViewRoots) || !isTopologyViewFile(repoFile) {
			continue
		}
		if _, registered := registeredViewFiles[repoFile]; !registered {
			problems = append(problems, fmt.Errorf("topology view file %q is not registered", repoFile))
		}
	}

	for _, anchor := range registry.Anchors {
		if err := validateRepoPath(anchor.Path); err != nil {
			problems = append(problems, fmt.Errorf("anchor path %q: %w", anchor.Path, err))
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(anchor.Path)))
		if err != nil {
			problems = append(problems, fmt.Errorf("read anchor %q: %w", anchor.Path, err))
			continue
		}
		if len(anchor.Symbols) == 0 {
			problems = append(problems, fmt.Errorf("anchor %q has no symbols", anchor.Path))
		}
		for _, symbol := range anchor.Symbols {
			if !bytes.Contains(data, []byte(symbol)) {
				problems = append(problems, fmt.Errorf("anchor %q no longer contains symbol %q", anchor.Path, symbol))
			}
		}
	}
	if !anyPathMatches(repoFiles, registryPath) {
		problems = append(problems, fmt.Errorf("registry %q is not tracked or visible to git", registryPath))
	}
	sort.Slice(problems, func(i, j int) bool { return problems[i].Error() < problems[j].Error() })
	return problems
}

func isTopologyViewFile(repoPath string) bool {
	switch path.Ext(repoPath) {
	case ".mmd", ".svg", ".png", ".excalidraw":
		return true
	default:
		return false
	}
}

func validateExistingPath(root, label, repoPath string, problems *[]error) {
	if err := validateRepoPath(repoPath); err != nil {
		*problems = append(*problems, fmt.Errorf("%s path %q: %w", label, repoPath, err))
		return
	}
	if !regularFileExists(root, repoPath) {
		*problems = append(*problems, fmt.Errorf("%s %q does not exist", label, repoPath))
	}
}

func validateRepoPath(value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("path is empty")
	}
	if strings.Contains(value, "\\") {
		return fmt.Errorf("path must use forward slashes")
	}
	if path.IsAbs(value) {
		return fmt.Errorf("absolute paths are not allowed")
	}
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return fmt.Errorf("path escapes or names the repository root")
	}
	return nil
}

func regularFileExists(root, repoPath string) bool {
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(repoPath)))
	return err == nil && info.Mode().IsRegular()
}

func validNodeID(value string) bool {
	if !strings.HasPrefix(value, "RT-") || len(value) <= len("RT-") {
		return false
	}
	for _, char := range value[len("RT-"):] {
		if (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '-' {
			return false
		}
	}
	return true
}

func nodesForPath(registry topologyRegistry, repoPath string) []string {
	var nodes []string
	for _, node := range registry.Nodes {
		if matchesAnyPrefix(repoPath, node.Paths) {
			nodes = append(nodes, node.ID)
		}
	}
	return uniqueSorted(nodes)
}

func matchesAnyPrefix(repoPath string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if pathMatches(repoPath, prefix) {
			return true
		}
	}
	return false
}

func anyPathMatches(repoFiles []string, prefix string) bool {
	for _, repoFile := range repoFiles {
		if pathMatches(repoFile, prefix) {
			return true
		}
	}
	return false
}

func pathMatches(repoPath, prefix string) bool {
	repoPath = normalizeRepoPath(repoPath)
	prefix = normalizeRepoPath(prefix)
	if strings.HasSuffix(prefix, "/") {
		return strings.HasPrefix(repoPath, prefix)
	}
	return repoPath == prefix
}

func normalizeRepoPath(value string) string {
	return strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(value)), "./")
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
