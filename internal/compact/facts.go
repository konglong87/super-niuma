package compact

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/capabilityloop"
)

type Facts struct {
	Files                  []string `json:"files,omitempty"`
	URLs                   []string `json:"urls,omitempty"`
	Commands               []string `json:"commands,omitempty"`
	Tools                  []string `json:"tools,omitempty"`
	Errors                 []string `json:"errors,omitempty"`
	Constraints            []string `json:"constraints,omitempty"`
	CapabilityEvidence     []string `json:"capability_evidence,omitempty"`
	CapabilityAssumptions  []string `json:"capability_assumptions,omitempty"`
	CapabilityUnknowns     []string `json:"capability_unknowns,omitempty"`
	CapabilityVerification []string `json:"capability_verification,omitempty"`
	CapabilityRisks        []string `json:"capability_risks,omitempty"`
	CapabilityNextActions  []string `json:"capability_next_actions,omitempty"`
	CapabilityFollowUpIDs  []string `json:"capability_follow_up_ids,omitempty"`
	CapabilityResolved     []string `json:"capability_resolved,omitempty"`
	CapabilitySupersedes   []string `json:"capability_supersedes,omitempty"`
	CapabilityArtifacts    []string `json:"capability_artifacts,omitempty"`
}

var (
	pathPattern    = regexp.MustCompile(`(?:^|[\s"'(])((?:\.{1,2}/|/)?[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.@+-]+)+)(?::\d+)?`)
	urlPattern     = regexp.MustCompile(`https?://[^\s<>"')]+`)
	commandPattern = regexp.MustCompile(`(?:^|[\s` + "`" + `$])((?:go|git|npm|pnpm|yarn|bun|python|python3|node|curl|make|docker|kubectl|rg|grep|sed|awk)\s+[^\n;]+)`)
)

func ExtractFacts(messages []anthropic.MessageParam) Facts {
	var facts Facts
	for _, message := range messages {
		for _, block := range message.Content {
			text := blockText(block)
			facts.Files = append(facts.Files, matches(pathPattern, text)...)
			facts.URLs = append(facts.URLs, matches(urlPattern, text)...)
			facts.Commands = append(facts.Commands, matches(commandPattern, text)...)
			mergeCapabilityFacts(&facts, extractCapabilityFacts(text))
			if block.Type == "tool_use" && strings.TrimSpace(block.Name) != "" {
				facts.Tools = append(facts.Tools, strings.TrimSpace(block.Name))
			}
			if block.Type == "tool_result" && block.IsError {
				facts.Errors = append(facts.Errors, trimLine(block.Content, 240))
			}
			lower := strings.ToLower(text)
			if strings.Contains(lower, "must ") || strings.Contains(lower, "do not ") || strings.Contains(lower, "don't ") || strings.Contains(lower, "不要") || strings.Contains(lower, "必须") {
				facts.Constraints = append(facts.Constraints, trimLine(text, 240))
			}
		}
	}
	facts.Files = uniqueSortedLimit(facts.Files, 40)
	facts.URLs = uniqueSortedLimit(facts.URLs, 20)
	facts.Commands = uniqueSortedLimit(facts.Commands, 30)
	facts.Tools = uniqueSortedLimit(facts.Tools, 30)
	facts.Errors = uniquePreserveLimit(facts.Errors, 20)
	facts.Constraints = uniquePreserveLimit(facts.Constraints, 20)
	facts.CapabilityEvidence = uniquePreserveLimit(facts.CapabilityEvidence, 12)
	facts.CapabilityAssumptions = uniquePreserveLimit(facts.CapabilityAssumptions, 8)
	facts.CapabilityUnknowns = uniquePreserveLimit(facts.CapabilityUnknowns, 8)
	facts.CapabilityVerification = uniquePreserveLimit(facts.CapabilityVerification, 8)
	facts.CapabilityRisks = uniquePreserveLimit(facts.CapabilityRisks, 8)
	facts.CapabilityNextActions = uniquePreserveLimit(facts.CapabilityNextActions, 8)
	facts.CapabilityFollowUpIDs = uniquePreserveLimit(facts.CapabilityFollowUpIDs, 8)
	facts.CapabilityResolved = uniquePreserveLimit(facts.CapabilityResolved, 8)
	facts.CapabilitySupersedes = uniquePreserveLimit(facts.CapabilitySupersedes, 8)
	facts.CapabilityArtifacts = uniquePreserveLimit(facts.CapabilityArtifacts, 12)
	return facts
}

func (f Facts) Empty() bool {
	return len(f.Files)+len(f.URLs)+len(f.Commands)+len(f.Tools)+len(f.Errors)+len(f.Constraints)+len(f.CapabilityEvidence)+len(f.CapabilityAssumptions)+len(f.CapabilityUnknowns)+len(f.CapabilityVerification)+len(f.CapabilityRisks)+len(f.CapabilityNextActions)+len(f.CapabilityFollowUpIDs)+len(f.CapabilityResolved)+len(f.CapabilitySupersedes)+len(f.CapabilityArtifacts) == 0
}

func (f Facts) Markdown() string {
	if f.Empty() {
		return "No hard facts were extracted."
	}
	sections := []struct {
		name  string
		items []string
	}{
		{"Files", f.Files},
		{"URLs", f.URLs},
		{"Commands", f.Commands},
		{"Tools", f.Tools},
		{"Errors", f.Errors},
		{"Constraints", f.Constraints},
		{"Capability Loop Evidence", f.CapabilityEvidence},
		{"Capability Loop Assumptions", f.CapabilityAssumptions},
		{"Capability Loop Unknowns", f.CapabilityUnknowns},
		{"Capability Loop Verification", f.CapabilityVerification},
		{"Capability Loop Risks", f.CapabilityRisks},
		{"Capability Loop Next Actions", f.CapabilityNextActions},
		{"Capability Loop Follow Up IDs", f.CapabilityFollowUpIDs},
		{"Capability Loop Resolved Follow Ups", f.CapabilityResolved},
		{"Capability Loop Supersedes", f.CapabilitySupersedes},
		{"Capability Loop Artifacts", f.CapabilityArtifacts},
	}
	var out strings.Builder
	for _, section := range sections {
		if len(section.items) == 0 {
			continue
		}
		out.WriteString(section.name)
		out.WriteString(":\n")
		for _, item := range section.items {
			out.WriteString("- ")
			out.WriteString(item)
			out.WriteString("\n")
		}
	}
	return strings.TrimSpace(out.String())
}

func extractCapabilityFacts(text string) Facts {
	var facts Facts
	mergeCapabilityFacts(&facts, extractCapabilityFactsFromJSON(text))
	mergeCapabilityFacts(&facts, extractCapabilityFactsFromSummaryBlock(text))
	mergeCapabilityFacts(&facts, extractCapabilityFactsFromMarkdownSections(text))
	for _, line := range strings.Split(text, "\n") {
		normalized := strings.TrimSpace(line)
		idx := strings.Index(strings.ToLower(normalized), capabilityloop.LineMarker)
		if idx < 0 {
			addCapabilityFollowUpGateFacts(&facts, normalized)
			continue
		}
		rest := strings.TrimSpace(normalized[idx+len(capabilityloop.LineMarker):])
		if artifactIndex := strings.Index(strings.ToLower(rest), "artifacts:"); artifactIndex >= 0 {
			addCapabilityArtifacts(&facts, rest[artifactIndex+len("artifacts:"):])
			rest = strings.TrimSpace(strings.TrimRight(rest[:artifactIndex], "; "))
		}
		for _, part := range strings.Split(rest, "|") {
			addCapabilityFact(&facts, part)
		}
	}
	return facts
}

func extractCapabilityFactsFromMarkdownSections(text string) Facts {
	var facts Facts
	section := ""
	for _, line := range strings.Split(text, "\n") {
		normalized := strings.TrimSpace(line)
		if normalized == "" {
			continue
		}
		lower := strings.ToLower(normalized)
		if strings.HasSuffix(lower, ":") {
			switch strings.TrimSuffix(lower, ":") {
			case "capability loop evidence":
				section = "evidence"
			case "capability loop assumptions":
				section = "assumptions"
			case "capability loop unknowns":
				section = "unknowns"
			case "capability loop verification":
				section = "verification"
			case "capability loop risks":
				section = "risks"
			case "capability loop next actions":
				section = "next_action"
			case "capability loop follow up ids":
				section = "follow_up_id"
			case "capability loop resolved follow ups":
				section = "resolved_follow_up"
			case "capability loop supersedes":
				section = "supersedes_evidence_id"
			case "capability loop artifacts":
				section = "artifacts"
			default:
				section = ""
			}
			continue
		}
		if section == "" {
			continue
		}
		if strings.HasPrefix(normalized, "- ") || strings.HasPrefix(normalized, "* ") {
			value := strings.TrimSpace(normalized[2:])
			if section == "artifacts" {
				addCapabilityArtifacts(&facts, value)
				continue
			}
			addCapabilityFact(&facts, section+": "+value)
		}
	}
	return facts
}

func extractCapabilityFactsFromSummaryBlock(text string) Facts {
	var facts Facts
	lines := strings.Split(text, "\n")
	inBlock := false
	for _, line := range lines {
		normalized := strings.TrimSpace(line)
		lower := strings.ToLower(normalized)
		if strings.Contains(lower, "capability loop summary preserved from full output") {
			inBlock = true
			continue
		}
		if !inBlock {
			continue
		}
		if normalized == "" {
			continue
		}
		if strings.HasPrefix(lower, "preview (first ") || strings.Contains(lower, "</persisted-output>") {
			break
		}
		if strings.HasPrefix(normalized, "- ") || strings.HasPrefix(normalized, "* ") {
			addCapabilityFact(&facts, strings.TrimSpace(normalized[2:]))
			continue
		}
		if strings.Contains(normalized, ":") {
			addCapabilityFact(&facts, normalized)
		}
	}
	return facts
}

// extractCapabilityFactsFromJSON walks every candidate rather than stopping at
// the first that decodes, because facts accumulate instead of being chosen; the
// unique* passes in ExtractFacts collapse anything a candidate contributes twice.
func extractCapabilityFactsFromJSON(text string) Facts {
	var facts Facts
	for _, candidate := range capabilityloop.JSONCandidates(text) {
		var decoded any
		if err := json.Unmarshal([]byte(candidate), &decoded); err != nil {
			continue
		}
		walkCapabilityJSON(decoded, &facts)
	}
	return facts
}

func walkCapabilityJSON(value any, facts *Facts) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if strings.EqualFold(key, capabilityloop.Key) {
				addCapabilityJSON(child, facts)
				addCapabilityArtifactsFromJSON(typed, facts)
				continue
			}
			walkCapabilityJSON(child, facts)
		}
	case []any:
		for _, child := range typed {
			walkCapabilityJSON(child, facts)
		}
	}
}

func addCapabilityJSON(value any, facts *Facts) {
	loop, ok := value.(map[string]any)
	if !ok {
		return
	}
	appendStrings := func(key string, dst *[]string) {
		raw, ok := loop[key]
		if !ok {
			return
		}
		switch typed := raw.(type) {
		case string:
			if trimmed := trimLine(typed, 240); trimmed != "" {
				if capabilityloop.IsPlaceholder(trimmed) {
					return
				}
				*dst = append(*dst, trimmed)
			}
		case []any:
			for _, item := range typed {
				if value, ok := item.(string); ok {
					if trimmed := trimLine(value, 240); trimmed != "" {
						if capabilityloop.IsPlaceholder(trimmed) {
							continue
						}
						*dst = append(*dst, trimmed)
					}
				}
			}
		}
	}
	appendStrings("evidence", &facts.CapabilityEvidence)
	appendStrings("assumptions", &facts.CapabilityAssumptions)
	appendStrings("unknowns", &facts.CapabilityUnknowns)
	appendStrings("verification", &facts.CapabilityVerification)
	appendStrings("risks", &facts.CapabilityRisks)
	appendStrings("next_action", &facts.CapabilityNextActions)
	appendStrings("follow_up_id", &facts.CapabilityFollowUpIDs)
	appendStrings("resolved_follow_up", &facts.CapabilityResolved)
	appendStrings("supersedes_evidence_id", &facts.CapabilitySupersedes)
	appendStrings("supersedes_evidence_ids", &facts.CapabilitySupersedes)
}

func addCapabilityFact(facts *Facts, part string) {
	part = strings.TrimSpace(part)
	if part == "" {
		return
	}
	idx := strings.Index(part, ":")
	if idx < 0 {
		return
	}
	key := strings.ToLower(strings.TrimSpace(part[:idx]))
	value := trimLine(part[idx+1:], 240)
	if value == "" || capabilityloop.IsPlaceholder(value) {
		return
	}
	// Three surfaces feed this: the canonical payload field names, the follow-up
	// gate labels (capabilityloop.FollowUpField*, single-sourced from the emitter),
	// and space-separated spellings. The last group has no emitter and is
	// deliberate leniency: unlike the other parsers, which read machine-authored
	// text, a compact summary is written by the model and may spell a field in
	// prose.
	switch key {
	case "evidence":
		facts.CapabilityEvidence = append(facts.CapabilityEvidence, value)
	case "assumptions":
		facts.CapabilityAssumptions = append(facts.CapabilityAssumptions, value)
	case "unknowns", capabilityloop.FollowUpFieldUnknown:
		facts.CapabilityUnknowns = append(facts.CapabilityUnknowns, value)
	case "verification", capabilityloop.FollowUpFieldVerification:
		facts.CapabilityVerification = append(facts.CapabilityVerification, value)
	case "risks", capabilityloop.FollowUpFieldRisk:
		facts.CapabilityRisks = append(facts.CapabilityRisks, value)
	case "next_action", "next action", capabilityloop.FollowUpFieldNextAction:
		facts.CapabilityNextActions = append(facts.CapabilityNextActions, value)
	case "follow_up_id", "follow up id":
		facts.CapabilityFollowUpIDs = append(facts.CapabilityFollowUpIDs, value)
	case "resolved_follow_up", "resolved follow up", "resolved follow-up":
		facts.CapabilityResolved = append(facts.CapabilityResolved, value)
	// One id per entry: query reads CapabilitySupersedes[0] as the singular id and
	// the rest as the plural slice, so a comma-joined value would become one id
	// that matches no follow-up. Evidence ids never contain a comma.
	case "supersedes_evidence_id", "supersedes_evidence_ids", "supersedes evidence id", "supersedes":
		for _, id := range strings.Split(value, ",") {
			if id = strings.TrimSpace(id); id != "" {
				facts.CapabilitySupersedes = append(facts.CapabilitySupersedes, id)
			}
		}
	case "artifacts":
		addCapabilityArtifacts(facts, value)
	}
}

func addCapabilityFollowUpGateFacts(facts *Facts, line string) {
	if !strings.Contains(strings.ToLower(line), "pending_follow_up:") {
		return
	}
	idx := strings.Index(line, ";")
	if idx < 0 || idx+1 >= len(line) {
		return
	}
	for _, part := range strings.Split(line[idx+1:], "|") {
		addCapabilityFact(facts, part)
	}
}

func mergeCapabilityFacts(dst *Facts, src Facts) {
	dst.CapabilityEvidence = append(dst.CapabilityEvidence, src.CapabilityEvidence...)
	dst.CapabilityAssumptions = append(dst.CapabilityAssumptions, src.CapabilityAssumptions...)
	dst.CapabilityUnknowns = append(dst.CapabilityUnknowns, src.CapabilityUnknowns...)
	dst.CapabilityVerification = append(dst.CapabilityVerification, src.CapabilityVerification...)
	dst.CapabilityRisks = append(dst.CapabilityRisks, src.CapabilityRisks...)
	dst.CapabilityNextActions = append(dst.CapabilityNextActions, src.CapabilityNextActions...)
	dst.CapabilityFollowUpIDs = append(dst.CapabilityFollowUpIDs, src.CapabilityFollowUpIDs...)
	dst.CapabilityResolved = append(dst.CapabilityResolved, src.CapabilityResolved...)
	dst.CapabilitySupersedes = append(dst.CapabilitySupersedes, src.CapabilitySupersedes...)
	dst.CapabilityArtifacts = append(dst.CapabilityArtifacts, src.CapabilityArtifacts...)
}

func addCapabilityArtifactsFromJSON(raw map[string]any, facts *Facts) {
	for _, key := range []string{"session_id", "transcript_path", "output_file", "worktree_path", "worktree_branch"} {
		value, ok := raw[key].(string)
		if !ok {
			continue
		}
		addCapabilityArtifact(facts, key, value)
	}
}

func addCapabilityArtifacts(facts *Facts, text string) {
	for _, part := range strings.Split(text, "|") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), ":")
		if !ok {
			continue
		}
		addCapabilityArtifact(facts, key, value)
	}
}

func addCapabilityArtifact(facts *Facts, key, value string) {
	key = strings.ToLower(strings.TrimSpace(key))
	switch key {
	case "session_id", "transcript_path", "output_file", "worktree_path", "worktree_branch":
	default:
		return
	}
	value = trimLine(value, 240)
	if value == "" {
		return
	}
	facts.CapabilityArtifacts = append(facts.CapabilityArtifacts, key+": "+value)
}

func blockText(block anthropic.ContentBlock) string {
	parts := []string{block.Text, block.Thinking, block.ConnectorText, block.Content, block.Name, block.ToolUseID}
	if len(block.Input) > 0 {
		var pretty any
		if json.Unmarshal(block.Input, &pretty) == nil {
			if data, err := json.Marshal(pretty); err == nil {
				parts = append(parts, string(data))
			}
		} else {
			parts = append(parts, string(block.Input))
		}
	}
	return strings.Join(parts, "\n")
}

func matches(re *regexp.Regexp, text string) []string {
	raw := re.FindAllStringSubmatch(text, -1)
	out := make([]string, 0, len(raw))
	for _, m := range raw {
		value := m[len(m)-1]
		value = strings.Trim(value, " \t\n\r\"'`()[]{}<>;")
		value = strings.TrimSuffix(value, ".")
		if strings.HasPrefix(value, "go and ") {
			continue
		}
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func trimLine(text string, max int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if len([]rune(text)) <= max {
		return text
	}
	runes := []rune(text)
	return string(runes[:max]) + "..."
}

// uniqueSortedLimit keeps the most recently mentioned unique values and renders
// them in stable sorted order. Truncation must happen before sorting, not after:
// sorting first and slicing the head dropped facts purely because their path
// sorted late in the alphabet, so a session touching `web/**` late could lose
// every file it had just edited while keeping `.github/**` from turn one.
// ExtractFacts walks messages oldest-first, so the tail of `values` is the most
// recent evidence.
func uniqueSortedLimit(values []string, limit int) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for i := len(values) - 1; i >= 0; i-- {
		value := strings.TrimSpace(values[i])
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	sort.Strings(out)
	return out
}

func uniquePreserveLimit(values []string, limit int) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}
