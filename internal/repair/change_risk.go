package repair

import (
	"regexp"
	"sort"
	"strings"
)

type ChangeRisk string

const (
	ChangeRiskDefinitionDeletion ChangeRisk = "definition_deletion"
)

type DeletedDefinition struct {
	Name string     `json:"name"`
	Risk ChangeRisk `json:"risk"`
}

var definitionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?m)^\s*(?:export\s+|readonly\s+|local\s+|declare(?:\s+-[A-Za-z]+)?\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=`),
	regexp.MustCompile(`(?m)^\s*(?:function\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*\(\s*\)\s*\{`),
	regexp.MustCompile(`(?m)^\s*func\s+(?:\([^\n)]*\)\s*)?([A-Za-z_][A-Za-z0-9_]*)\s*\(`),
	regexp.MustCompile(`(?m)^\s*(?:type|const|var)\s+([A-Za-z_][A-Za-z0-9_]*)\b`),
}

// AnalyzeReplacement returns definitions present in oldText but absent from
// newText. It is deliberately conservative: only explicit language-level or
// shell definitions are considered, never arbitrary prose or config keys.
func AnalyzeReplacement(oldText, newText string) []DeletedDefinition {
	oldDefinitions := definitionNames(oldText)
	newDefinitions := definitionNames(newText)
	var deleted []DeletedDefinition
	for name := range oldDefinitions {
		if newDefinitions[name] || definitionStillPresent(newText, name) {
			continue
		}
		deleted = append(deleted, DeletedDefinition{Name: name, Risk: ChangeRiskDefinitionDeletion})
	}
	sort.Slice(deleted, func(i, j int) bool { return deleted[i].Name < deleted[j].Name })
	return deleted
}

func definitionNames(text string) map[string]bool {
	out := map[string]bool{}
	for _, pattern := range definitionPatterns {
		for _, match := range pattern.FindAllStringSubmatch(text, -1) {
			if len(match) > 1 && strings.TrimSpace(match[1]) != "" {
				out[match[1]] = true
			}
		}
	}
	return out
}

func definitionStillPresent(text, name string) bool {
	quoted := regexp.QuoteMeta(name)
	patterns := []string{
		`(?m)^\s*(?:export\s+|readonly\s+|local\s+|declare(?:\s+-[A-Za-z]+)?\s+)?` + quoted + `\s*=`,
		`(?m)^\s*(?:function\s+)?` + quoted + `\s*\(\s*\)\s*\{`,
		`(?m)^\s*func\s+(?:\([^\n)]*\)\s*)?` + quoted + `\s*\(`,
		`(?m)^\s*(?:type|const|var)\s+` + quoted + `\b`,
	}
	for _, pattern := range patterns {
		if regexp.MustCompile(pattern).MatchString(text) {
			return true
		}
	}
	return false
}
