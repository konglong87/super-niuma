package handoff

import (
	"sort"
	"strings"
)

// Precedence states the source adapter's explicit selection policy. For an
// objective, source task description precedes the latest user instruction;
// hard constraints and structured status/facts are selected before weaker
// candidates. Ties use locator and capped text, never capture order.
type Precedence uint8

const (
	PrecedenceHardConstraint   Precedence = 10
	PrecedenceTaskDescription  Precedence = 20
	PrecedenceUserInstruction  Precedence = 30
	PrecedenceStructuredStatus Precedence = 40
	PrecedenceStructuredFact   Precedence = 50
)

type Candidate struct {
	Locator    string
	Text       string
	Precedence Precedence
}

type FactKind string

const (
	FactCompleted  FactKind = "completed"
	FactOpenItem   FactKind = "open_item"
	FactRisk       FactKind = "risk"
	FactNextAction FactKind = "next_action"
)

type FactCandidate struct {
	Locator    string
	Text       string
	Kind       FactKind
	Precedence Precedence
}

// SourceSnapshot is the only extractor input. Its adapters must already have
// removed transcript bodies and raw tool output; this package never reads I/O.
type SourceSnapshot struct {
	Source      Source
	Target      Target
	Objectives  []Candidate
	Constraints []Candidate
	Stages      []Candidate
	Facts       []FactCandidate
	Evidence    []Evidence
	Budget      Budget
}

// Extract derives a bounded package from explicit structured candidates. It
// caps individual prose before constructing package fields and keeps evidence
// locators even when their display claims are shortened.
func Extract(snapshot SourceSnapshot) (Package, error) {
	objective := selectCandidate(snapshot.Objectives)
	stage := selectCandidate(snapshot.Stages)
	constraints := candidateTexts(snapshot.Constraints)
	completed, openItems, risks, nextActions := factTexts(snapshot.Facts)
	evidence := make([]Evidence, len(snapshot.Evidence))
	for i, item := range snapshot.Evidence {
		item.Claim = capText(item.Claim)
		evidence[i] = item
	}
	return BuildPackage(PackageInput{
		Source:       snapshot.Source,
		Target:       snapshot.Target,
		Objective:    objective,
		Constraints:  constraints,
		StageSummary: stage,
		Completed:    completed,
		OpenItems:    openItems,
		Risks:        risks,
		NextActions:  nextActions,
		Evidence:     evidence,
		Budget:       snapshot.Budget,
	})
}

func selectCandidate(candidates []Candidate) string {
	if len(candidates) == 0 {
		return ""
	}
	normalized := normalizeCandidates(candidates)
	return normalized[0].Text
}

func candidateTexts(candidates []Candidate) []string {
	normalized := normalizeCandidates(candidates)
	texts := make([]string, 0, len(normalized))
	for _, candidate := range normalized {
		texts = append(texts, candidate.Text)
	}
	return normalizeStrings(texts)
}

func factTexts(facts []FactCandidate) (completed, openItems, risks, nextActions []string) {
	normalized := append([]FactCandidate(nil), facts...)
	for i := range normalized {
		normalized[i].Text = capText(normalized[i].Text)
		if normalized[i].Precedence == 0 {
			normalized[i].Precedence = PrecedenceStructuredFact
		}
	}
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i].Kind != normalized[j].Kind {
			return normalized[i].Kind < normalized[j].Kind
		}
		if normalized[i].Precedence != normalized[j].Precedence {
			return normalized[i].Precedence < normalized[j].Precedence
		}
		if normalized[i].Locator != normalized[j].Locator {
			return normalized[i].Locator < normalized[j].Locator
		}
		return normalized[i].Text < normalized[j].Text
	})
	for _, fact := range normalized {
		switch fact.Kind {
		case FactCompleted:
			completed = append(completed, fact.Text)
		case FactOpenItem:
			openItems = append(openItems, fact.Text)
		case FactRisk:
			risks = append(risks, fact.Text)
		case FactNextAction:
			nextActions = append(nextActions, fact.Text)
		}
	}
	return normalizeStrings(completed), normalizeStrings(openItems), normalizeStrings(risks), normalizeStrings(nextActions)
}

func normalizeCandidates(candidates []Candidate) []Candidate {
	normalized := append([]Candidate(nil), candidates...)
	for i := range normalized {
		normalized[i].Text = capText(normalized[i].Text)
	}
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i].Precedence != normalized[j].Precedence {
			return normalized[i].Precedence < normalized[j].Precedence
		}
		if normalized[i].Locator != normalized[j].Locator {
			return normalized[i].Locator < normalized[j].Locator
		}
		return normalized[i].Text < normalized[j].Text
	})
	return normalized
}

func normalizeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = capText(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	sort.Strings(normalized)
	return normalized
}

func normalizeEvidence(evidence []Evidence) []Evidence {
	normalized := append([]Evidence(nil), evidence...)
	for i := range normalized {
		normalized[i].Claim = capText(normalized[i].Claim)
	}
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i].Ref != normalized[j].Ref {
			return normalized[i].Ref < normalized[j].Ref
		}
		if normalized[i].SHA256 != normalized[j].SHA256 {
			return normalized[i].SHA256 < normalized[j].SHA256
		}
		return normalized[i].Claim < normalized[j].Claim
	})
	return normalized
}

func normalizeText(text string) string {
	return capText(text)
}

func capText(text string) string {
	text = strings.TrimSpace(text)
	if len(text) <= MaxCandidateBytes {
		return text
	}
	end := MaxCandidateBytes
	for end > 0 && (text[end]&0xc0) == 0x80 {
		end--
	}
	return text[:end]
}
