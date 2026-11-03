package main

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	impactUpdated = "updated"
	impactNone    = "none"

	blastRadiusB0 = "B0_LOCAL"
	blastRadiusB1 = "B1_SCENARIO"
	blastRadiusB2 = "B2_MODE"
	blastRadiusB3 = "B3_GLOBAL_RUNTIME"
	blastRadiusB4 = "B4_PROTOCOL"
	blastRadiusB5 = "B5_SHARED_STATE"
)

var validBlastRadii = map[string]struct{}{
	blastRadiusB0: {},
	blastRadiusB1: {},
	blastRadiusB2: {},
	blastRadiusB3: {},
	blastRadiusB4: {},
	blastRadiusB5: {},
}

type impactDeclaration struct {
	Impact      string
	BlastRadius string
	Reason      string
}

type impactReport struct {
	AffectedNodes    []string
	RecommendedTests []string
}

func evaluateTopologyImpact(registryPath string, registry topologyRegistry, changed []string, declaration impactDeclaration) (impactReport, []error) {
	changedSet := make(map[string]struct{}, len(changed))
	for _, changedPath := range changed {
		changedSet[normalizeRepoPath(changedPath)] = struct{}{}
	}
	var problems []error
	var affectedNodes []string
	for changedPath := range changedSet {
		if !matchesAnyPrefix(changedPath, registry.CoverageRoots) {
			continue
		}
		nodes := nodesForPath(registry, changedPath)
		if len(nodes) == 0 {
			problems = append(problems, fmt.Errorf("changed runtime path %q is not mapped to an RT-* node", changedPath))
			continue
		}
		affectedNodes = append(affectedNodes, nodes...)
	}
	affectedNodes = uniqueSorted(affectedNodes)

	for _, view := range registry.Views {
		_, sourceChanged := changedSet[normalizeRepoPath(view.Source)]
		if sourceChanged {
			for _, artifact := range view.Artifacts {
				if _, ok := changedSet[normalizeRepoPath(artifact)]; !ok {
					problems = append(problems, fmt.Errorf("view %s source changed without regenerated artifact %q", view.ID, artifact))
				}
			}
		}
		for _, artifact := range view.Artifacts {
			if _, artifactChanged := changedSet[normalizeRepoPath(artifact)]; artifactChanged && !sourceChanged {
				problems = append(problems, fmt.Errorf("view %s artifact %q changed without its Mermaid source", view.ID, artifact))
			}
		}
	}

	var recommendedTests []string
	for _, affectedNode := range affectedNodes {
		for _, node := range registry.Nodes {
			if node.ID == affectedNode {
				recommendedTests = append(recommendedTests, node.Tests...)
				break
			}
		}
	}
	report := impactReport{
		AffectedNodes:    affectedNodes,
		RecommendedTests: uniqueSorted(recommendedTests),
	}
	if len(affectedNodes) == 0 {
		return report, problems
	}
	declaration = normalizeDeclaration(declaration)
	if declaration.Impact != impactUpdated && declaration.Impact != impactNone {
		problems = append(problems, fmt.Errorf("runtime changes require Topology-Impact: updated or none"))
	}
	if _, ok := validBlastRadii[declaration.BlastRadius]; !ok {
		problems = append(problems, fmt.Errorf("runtime changes require a valid Blast-Radius declaration"))
	}
	if utf8.RuneCountInString(declaration.Reason) < 8 {
		problems = append(problems, fmt.Errorf("runtime changes require a concrete Topology-Reason of at least 8 characters"))
	}

	topologySourceChanged := false
	semanticSources := []string{registryPath, registry.Document}
	for _, view := range registry.Views {
		semanticSources = append(semanticSources, view.Source)
	}
	for _, source := range semanticSources {
		if _, ok := changedSet[normalizeRepoPath(source)]; ok {
			topologySourceChanged = true
			break
		}
	}
	if declaration.Impact == impactUpdated && !topologySourceChanged {
		problems = append(problems, fmt.Errorf("Topology-Impact is updated, but no registry, document, or Mermaid source changed"))
	}
	if declaration.Impact == impactNone && topologySourceChanged {
		problems = append(problems, fmt.Errorf("topology sources changed, so Topology-Impact must be updated instead of none"))
	}
	return report, problems
}

func parseImpactDeclaration(text string) impactDeclaration {
	var declaration impactDeclaration
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "-*"))
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" || strings.HasPrefix(value, "<!--") {
			continue
		}
		switch normalizeDeclarationKey(key) {
		case "topologyimpact":
			if declaration.Impact == "" {
				declaration.Impact = value
			}
		case "blastradius":
			if declaration.BlastRadius == "" {
				declaration.BlastRadius = value
			}
		case "topologyreason":
			if declaration.Reason == "" {
				declaration.Reason = value
			}
		}
	}
	return normalizeDeclaration(declaration)
}

func normalizeDeclarationKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer("-", "", "_", "", " ", "")
	return replacer.Replace(value)
}

func normalizeDeclaration(declaration impactDeclaration) impactDeclaration {
	declaration.Impact = strings.ToLower(strings.TrimSpace(declaration.Impact))
	declaration.BlastRadius = strings.ToUpper(strings.TrimSpace(declaration.BlastRadius))
	declaration.Reason = strings.TrimSpace(declaration.Reason)
	return declaration
}

func fillDeclaration(primary, fallback impactDeclaration) impactDeclaration {
	if strings.TrimSpace(primary.Impact) == "" {
		primary.Impact = fallback.Impact
	}
	if strings.TrimSpace(primary.BlastRadius) == "" {
		primary.BlastRadius = fallback.BlastRadius
	}
	if strings.TrimSpace(primary.Reason) == "" {
		primary.Reason = fallback.Reason
	}
	return normalizeDeclaration(primary)
}

func (declaration impactDeclaration) incomplete() bool {
	declaration = normalizeDeclaration(declaration)
	return declaration.Impact == "" || declaration.BlastRadius == "" || declaration.Reason == ""
}
