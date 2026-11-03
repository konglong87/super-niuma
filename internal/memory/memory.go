package memory

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/identity"
	"github.com/konglong87/super-niuma/internal/product"
	"github.com/konglong87/super-niuma/internal/session"
	"gopkg.in/yaml.v3"
)

const (
	projectMemoryFrontmatterDelimiter = "---"
	defaultWorkflowPromptBudgetBytes  = 4096
	defaultProjectMemoryRecallFiles   = 4
	defaultProjectMemoryPreviewLines  = 12
	defaultProjectMemoryMaxBytes      = 25 * 1024
	defaultProjectMemoryMaxLines      = 200
	// defaultDocumentPromptBudgetBytes caps a single memory document. Before
	// this existed only Type=="Workflow" was capped, so one oversized
	// CLAUDE.md, user memory or managed policy went into every prompt in full.
	defaultDocumentPromptBudgetBytes = 16 * 1024
	// defaultTotalPromptBudgetBytes caps all memory documents together, so the
	// prompt does not scale with the number of guidance files on disk.
	defaultTotalPromptBudgetBytes = 64 * 1024
)

type Document struct {
	Path     string   `json:"path"`
	Content  string   `json:"content"`
	Type     string   `json:"type,omitempty"`
	Paths    []string `json:"paths,omitempty"`
	Excludes []string `json:"excludes,omitempty"`
	Parent   string   `json:"parent,omitempty"`
}

type PromptDocumentsReport struct {
	OriginalBytes       int                     `json:"original_bytes,omitempty"`
	PromptBytes         int                     `json:"prompt_bytes,omitempty"`
	BudgetedDocuments   int                     `json:"budgeted_documents,omitempty"`
	WorkflowBudgetBytes int                     `json:"workflow_budget_bytes,omitempty"`
	DocumentBudgetBytes int                     `json:"document_budget_bytes,omitempty"`
	TotalBudgetBytes    int                     `json:"total_budget_bytes,omitempty"`
	Documents           []PromptDocumentSummary `json:"documents,omitempty"`
}

type PromptDocumentSummary struct {
	Path          string `json:"path,omitempty"`
	Type          string `json:"type,omitempty"`
	OriginalBytes int    `json:"original_bytes,omitempty"`
	PromptBytes   int    `json:"prompt_bytes,omitempty"`
	Budgeted      bool   `json:"budgeted,omitempty"`
	Workflow      bool   `json:"workflow,omitempty"`
	PathScoped    bool   `json:"path_scoped,omitempty"`
	ExcludeScoped bool   `json:"exclude_scoped,omitempty"`
}

func Load(cwd string) ([]Document, error) {
	return LoadCode(cwd, "")
}

type LoadCodeOptions struct {
	DisableProjectAgentsFallback bool
	Scope                        LoadScope
	DiscoveryMode                DiscoveryMode
	ExplicitRoots                []string
}

type DiscoveryMode string

const (
	DiscoveryAuto     DiscoveryMode = "auto"
	DiscoveryExplicit DiscoveryMode = "explicit"
)

type LoadScope string

const (
	LoadScopeAll       LoadScope = "all"
	LoadScopeWorkspace LoadScope = "workspace"
)

func LoadForScope(cwd string, scope LoadScope) ([]Document, error) {
	return LoadCodeWithOptions(cwd, "", LoadCodeOptions{Scope: scope})
}

func LoadCode(cwd, prompt string) ([]Document, error) {
	return LoadCodeWithOptions(cwd, prompt, LoadCodeOptions{})
}

func LoadCodeWithOptions(cwd, prompt string, opts LoadCodeOptions) ([]Document, error) {
	loadedSettings := config.LoadSettings(cwd)
	id := identity.FromSettings(config.IdentitySettings(loadedSettings.Settings))
	if opts.DiscoveryMode == DiscoveryExplicit {
		return loadExplicitCodeRoots(prompt, opts.ExplicitRoots, id.GuidanceFilename)
	}
	if opts.DiscoveryMode != "" && opts.DiscoveryMode != DiscoveryAuto {
		return nil, fmt.Errorf("unknown memory discovery mode: %q", string(opts.DiscoveryMode))
	}
	scope, err := normalizeLoadScope(opts.Scope)
	if err != nil {
		return nil, err
	}
	loader := documentLoader{files: turnFileCandidates(cwd, prompt), seen: map[string]bool{}}
	var docs []Document
	if scope == LoadScopeAll {
		managed, err := loadManaged(&loader)
		if err != nil {
			return nil, err
		}
		docs = append(docs, managed...)
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			loaded, err := loader.load(filepath.Join(home, ".claude", "CLAUDE.md"), "User", true, "")
			if err != nil {
				return nil, err
			}
			docs = append(docs, loaded...)
		}
	}
	for _, dir := range projectDirs(cwd) {
		projectGuidance, err := loadProjectGuidanceDocument(&loader, dir, id, !opts.DisableProjectAgentsFallback)
		if err != nil {
			return nil, err
		}
		docs = append(docs, projectGuidance...)
		dotClaude, err := loader.load(filepath.Join(dir, ".claude", "CLAUDE.md"), "Project", false, "")
		if err != nil {
			return nil, err
		}
		docs = append(docs, dotClaude...)
		rules, err := filepath.Glob(filepath.Join(dir, ".claude", "rules", "*.md"))
		if err != nil {
			return nil, err
		}
		for _, path := range rules {
			loaded, err := loader.load(path, "Project", false, "")
			if err != nil {
				return nil, err
			}
			docs = append(docs, loaded...)
		}
		loaded, err := loader.load(filepath.Join(dir, "CLAUDE.local.md"), "Local", false, "")
		if err != nil {
			return nil, err
		}
		docs = append(docs, loaded...)
		workflowDocs, err := loadWorkflowDocuments(&loader, dir)
		if err != nil {
			return nil, err
		}
		docs = append(docs, workflowDocs...)
	}
	if scope == LoadScopeAll {
		team, err := loadConfigDirDocuments(&loader, "Team", []string{
			filepath.Join("team", "CLAUDE.md"),
			filepath.Join("team", "TEAM.md"),
			filepath.Join("memory", "team.md"),
			"TEAM.md",
		})
		if err != nil {
			return nil, err
		}
		docs = append(docs, team...)
		auto, err := loadConfigDirDocuments(&loader, "Auto", []string{
			filepath.Join("memory", "auto.md"),
			filepath.Join("memory", "AUTOMEM.md"),
			"AUTOMEM.md",
		})
		if err != nil {
			return nil, err
		}
		docs = append(docs, auto...)
	}
	projectMemory, err := loadClaudeCodeProjectMemory(cwd, prompt)
	if err != nil {
		return nil, err
	}
	docs = append(docs, projectMemory...)
	return docs, nil
}

func loadExplicitCodeRoots(prompt string, roots []string, guidanceFilename string) ([]Document, error) {
	var docs []Document
	seenRoots := map[string]bool{}
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		abs = filepath.Clean(abs)
		if seenRoots[abs] {
			continue
		}
		seenRoots[abs] = true
		loader := documentLoader{files: turnFileCandidates(abs, prompt), seen: map[string]bool{}, rootBoundary: abs}
		for _, candidate := range []struct {
			path string
			typ  string
		}{
			{filepath.Join(abs, guidanceFilename), "Project"},
			{filepath.Join(abs, "CLAUDE.md"), "Project"},
			{filepath.Join(abs, ".claude", "CLAUDE.md"), "Project"},
		} {
			loaded, err := loader.load(candidate.path, candidate.typ, false, "")
			if err != nil {
				return nil, err
			}
			docs = append(docs, loaded...)
		}
		rules, err := filepath.Glob(filepath.Join(abs, ".claude", "rules", "*.md"))
		if err != nil {
			return nil, err
		}
		for _, path := range rules {
			loaded, err := loader.load(path, "Project", false, "")
			if err != nil {
				return nil, err
			}
			docs = append(docs, loaded...)
		}
	}
	return docs, nil
}

func normalizeLoadScope(scope LoadScope) (LoadScope, error) {
	switch scope {
	case "", LoadScopeAll:
		return LoadScopeAll, nil
	case LoadScopeWorkspace:
		return LoadScopeWorkspace, nil
	default:
		return "", fmt.Errorf("unknown memory load scope: %q", scope)
	}
}

func IsWorkflowDocument(doc Document) bool {
	return strings.EqualFold(strings.TrimSpace(doc.Type), "Workflow")
}

func SystemAddendum(cwd string) string {
	return SystemAddendumForPrompt(cwd, "")
}

func SystemAddendumForPrompt(cwd, prompt string) string {
	docs, err := LoadCode(cwd, prompt)
	if err != nil || len(docs) == 0 {
		return ""
	}
	return SystemAddendumFromDocuments(docs)
}

func SystemAddendumFromDocuments(docs []Document) string {
	promptDocs, _ := PreparePromptDocuments(docs)
	return SystemAddendumFromPreparedDocuments(promptDocs)
}

func SystemAddendumFromPreparedDocuments(docs []Document) string {
	if len(docs) == 0 {
		return ""
	}
	var parts []string
	for _, doc := range docs {
		description := promptDocumentTypeLabel(doc.Type)
		if description != "" {
			description = " (" + description + ")"
		}
		parts = append(parts, "## "+doc.Path+description+"\n"+strings.TrimSpace(doc.Content))
	}
	return "# Memory\nThe following guidance is available for this workspace. Follow it as durable user/project guidance unless it conflicts with higher-priority instructions in the current conversation. Each recorded rule is scoped by the situation it was written for — judge whether the current situation is what the rule was meant for before applying it. If two rules here contradict each other, or a rule conflicts with the user's current request, say so explicitly and state which one you are following instead of silently picking one.\n\n" + strings.Join(parts, "\n\n")
}

// PreparePromptDocuments applies the prompt byte budgets. Documents arrive in
// priority order, so the total budget is spent from the front: the first
// documents keep their content and later ones shrink to a pointer once the
// budget runs out. Every truncation is marked, so the model can tell that it is
// looking at an excerpt and knows which file to read for the rest.
func PreparePromptDocuments(docs []Document) ([]Document, PromptDocumentsReport) {
	workflowBudget := workflowPromptBudgetBytes()
	documentBudget := documentPromptBudgetBytes()
	totalBudget := totalPromptBudgetBytes()
	remaining := totalBudget
	promptDocs := make([]Document, 0, len(docs))
	report := PromptDocumentsReport{WorkflowBudgetBytes: workflowBudget, DocumentBudgetBytes: documentBudget, TotalBudgetBytes: totalBudget}
	for _, doc := range docs {
		promptDoc := doc
		content := strings.TrimSpace(doc.Content)
		originalBytes := len(content)
		isWorkflow := IsWorkflowDocument(doc)
		if isWorkflow && workflowBudget > 0 && originalBytes > workflowBudget {
			content = compactWorkflowContentForPrompt(doc, workflowBudget)
		}
		limit := documentBudget
		if totalBudget > 0 && (limit <= 0 || remaining < limit) {
			limit = remaining
		}
		if (documentBudget > 0 || totalBudget > 0) && (limit <= 0 || len(content) > limit) {
			content = truncateDocumentForPrompt(doc.Path, content, limit, originalBytes)
		}
		promptDoc.Content = content
		promptBytes := len(content)
		budgeted := promptBytes < originalBytes
		if totalBudget > 0 {
			remaining = maxInt(0, remaining-promptBytes)
		}
		report.OriginalBytes += originalBytes
		report.PromptBytes += promptBytes
		if budgeted {
			report.BudgetedDocuments++
		}
		report.Documents = append(report.Documents, PromptDocumentSummary{
			Path:          doc.Path,
			Type:          firstNonEmpty(strings.TrimSpace(doc.Type), "Unknown"),
			OriginalBytes: originalBytes,
			PromptBytes:   promptBytes,
			Budgeted:      budgeted,
			Workflow:      isWorkflow,
			PathScoped:    len(doc.Paths) > 0,
			ExcludeScoped: len(doc.Excludes) > 0,
		})
		promptDocs = append(promptDocs, promptDoc)
	}
	return promptDocs, report
}

func workflowPromptBudgetBytes() int {
	return budgetBytesFromEnv("NIUMA_WORKFLOW_PROMPT_BUDGET_BYTES", defaultWorkflowPromptBudgetBytes)
}

func documentPromptBudgetBytes() int {
	return budgetBytesFromEnv("NIUMA_MEMORY_DOCUMENT_BUDGET_BYTES", defaultDocumentPromptBudgetBytes)
}

func totalPromptBudgetBytes() int {
	return budgetBytesFromEnv("NIUMA_MEMORY_PROMPT_BUDGET_BYTES", defaultTotalPromptBudgetBytes)
}

// budgetBytesFromEnv reads a byte budget; 0 disables the budget.
func budgetBytesFromEnv(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

// memoryTruncationMarker is the visible signal that guidance was cut. It is
// deliberately literal: the model must not treat an excerpt as the whole file.
const memoryTruncationMarker = "[Memory truncated for prompt budget]"

// truncateDocumentForPrompt keeps the opening of a document and appends a marker
// naming the file to read. A limit that leaves no room for content collapses the
// document to the marker alone, which keeps the reference without the bytes.
func truncateDocumentForPrompt(path, content string, limit, originalBytes int) string {
	notice := memoryTruncationMarker + " " + strconv.Itoa(originalBytes) + " bytes total"
	if strings.TrimSpace(path) != "" {
		notice += "; read " + path + " before relying on rules not shown here."
	} else {
		notice += "."
	}
	kept := truncateAtLineBoundary(content, limit-len(notice)-2)
	if kept == "" {
		return notice
	}
	return kept + "\n\n" + notice
}

func compactWorkflowContentForPrompt(doc Document, budget int) string {
	content := strings.TrimSpace(doc.Content)
	if budget <= 0 || len(content) <= budget {
		return content
	}
	header := strings.Join([]string{
		"Large workflow guidance has been budgeted for the initial prompt.",
		"Full file: " + doc.Path,
		"Original bytes: " + strconv.Itoa(len(content)),
		"Read the full file before making changes that may depend on detailed project rules.",
	}, "\n")
	index := workflowHeadingIndex(content, 1200)
	directives := workflowDirectiveExcerpt(content, 2200)
	var parts []string
	parts = append(parts, header)
	if index != "" {
		parts = append(parts, "## Section index\n"+index)
	}
	if directives != "" {
		parts = append(parts, "## High-priority rule excerpts\n"+directives)
	}
	if len(parts) == 1 {
		parts = append(parts, "## Opening excerpt\n"+truncateAtLineBoundary(content, maxInt(0, budget-len(header)-64)))
	}
	out := strings.TrimSpace(strings.Join(parts, "\n\n"))
	truncatedNote := "\n\n[Budgeted workflow guidance truncated.]"
	if len(out) > budget {
		out = truncateAtLineBoundary(out, budget-len(truncatedNote))
		out = strings.TrimSpace(out) + truncatedNote
	}
	return strings.TrimSpace(out)
}

func workflowHeadingIndex(content string, limit int) string {
	var lines []string
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			lines = append(lines, trimmed)
		}
	}
	return joinLinesWithinLimit(lines, limit)
}

func workflowDirectiveExcerpt(content string, limit int) string {
	markers := []string{
		"必须", "不得", "不能", "不要", "禁止", "默认", "优先", "需要", "应",
		"MUST", "NEVER", "ALWAYS", "DO NOT", "REQUIRED", "SHOULD",
	}
	var lines []string
	inFence := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence || trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		upper := strings.ToUpper(trimmed)
		for _, marker := range markers {
			if strings.Contains(trimmed, marker) || strings.Contains(upper, marker) {
				lines = append(lines, trimmed)
				break
			}
		}
	}
	return joinLinesWithinLimit(lines, limit)
}

func joinLinesWithinLimit(lines []string, limit int) string {
	if limit <= 0 || len(lines) == 0 {
		return ""
	}
	var out []string
	used := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		next := len(line)
		if used > 0 {
			next++
		}
		if used+next > limit {
			break
		}
		out = append(out, line)
		used += next
	}
	return strings.Join(out, "\n")
}

// truncateAtLineBoundary cuts text to at most limit bytes, preferring a line
// boundary and never splitting a rune -- a byte cut through CJK guidance writes
// replacement characters into the prompt.
func truncateAtLineBoundary(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 {
		return ""
	}
	if len(text) <= limit {
		return text
	}
	cut := text[:limit]
	if idx := strings.LastIndex(cut, "\n"); idx > 0 {
		return strings.TrimSpace(cut[:idx])
	}
	for len(cut) > 0 {
		if r, size := utf8.DecodeLastRuneInString(cut); r == utf8.RuneError && size <= 1 {
			cut = cut[:len(cut)-1]
			continue
		}
		break
	}
	return strings.TrimSpace(cut)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func promptDocumentTypeLabel(docType string) string {
	switch docType {
	case "ClaudeCodeProjectMemory":
		return "ProjectMemory"
	default:
		return docType
	}
}

func LoadAgent(cwd, agentName, scope string) ([]Document, error) {
	agentName = sanitizeAgentName(agentName)
	if agentName == "" {
		return nil, nil
	}
	loader := documentLoader{seen: map[string]bool{}}
	var docs []Document
	for _, candidate := range agentMemoryCandidates(cwd, agentName, scope) {
		loaded, err := loader.load(candidate.path, candidate.typ, candidate.external, "")
		if err != nil {
			return nil, err
		}
		docs = append(docs, loaded...)
	}
	return docs, nil
}

func agentMemoryCandidates(cwd, agentName, scope string) []struct {
	path     string
	typ      string
	external bool
} {
	scope = strings.ToLower(strings.TrimSpace(scope))
	if scope == "" || scope == "false" || scope == "none" || scope == "off" {
		return nil
	}
	includeUser := scope == "true" || scope == "all" || scope == "user"
	includeProject := scope == "true" || scope == "all" || scope == "project"
	includeLocal := scope == "true" || scope == "all" || scope == "local"
	var out []struct {
		path     string
		typ      string
		external bool
	}
	if includeUser {
		if dir := configDir(); dir != "" {
			out = append(out, struct {
				path     string
				typ      string
				external bool
			}{path: filepath.Join(dir, "agent-memory", agentName, "MEMORY.md"), typ: "AgentUser", external: true})
		}
	}
	for _, dir := range projectDirs(cwd) {
		if includeProject {
			out = append(out, struct {
				path     string
				typ      string
				external bool
			}{path: filepath.Join(dir, ".claude", "agent-memory", agentName, "MEMORY.md"), typ: "AgentProject", external: false})
		}
		if includeLocal {
			out = append(out, struct {
				path     string
				typ      string
				external bool
			}{path: filepath.Join(dir, ".claude", "agent-memory-local", agentName, "MEMORY.md"), typ: "AgentLocal", external: false})
		}
	}
	return out
}

func sanitizeAgentName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	name = filepath.Base(filepath.Clean(name))
	name = strings.Trim(name, ".")
	if name == "" || name == string(os.PathSeparator) {
		return ""
	}
	return name
}

type documentLoader struct {
	files        []string
	seen         map[string]bool
	rootBoundary string
}

func loadManaged(loader *documentLoader) ([]Document, error) {
	var docs []Document
	for _, path := range managedMemoryPaths() {
		loaded, err := loader.load(path, "Managed", true, "")
		if err != nil {
			return nil, err
		}
		docs = append(docs, loaded...)
	}
	return docs, nil
}

func managedMemoryPaths() []string {
	var out []string
	if raw := strings.TrimSpace(product.Getenv("NIUMA_MANAGED_MEMORY")); raw != "" {
		out = append(out, splitPathList(raw)...)
	}
	if raw := strings.TrimSpace(os.Getenv("CLAUDE_CODE_MANAGED_MEMORY")); raw != "" {
		out = append(out, splitPathList(raw)...)
	}
	out = append(out, filepath.Join(string(os.PathSeparator), "etc", "claude-code", "CLAUDE.md"))
	return out
}

func loadConfigDirDocuments(loader *documentLoader, typ string, rels []string) ([]Document, error) {
	configDir := configDir()
	if configDir == "" {
		return nil, nil
	}
	var docs []Document
	for _, rel := range rels {
		loaded, err := loader.load(filepath.Join(configDir, rel), typ, true, "")
		if err != nil {
			return nil, err
		}
		docs = append(docs, loaded...)
	}
	return docs, nil
}

func loadProjectGuidanceDocument(loader *documentLoader, dir string, id identity.Identity, includeAgentsFallback bool) ([]Document, error) {
	guidancePath := filepath.Join(dir, id.GuidanceFilename)
	if _, err := os.Stat(guidancePath); err == nil {
		return loader.load(guidancePath, "Project", false, "")
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	claudePath := filepath.Join(dir, id.LegacyGuidanceFile)
	if _, err := os.Stat(claudePath); err == nil {
		return loader.load(claudePath, "Project", false, "")
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if !includeAgentsFallback {
		return nil, nil
	}
	return loader.load(filepath.Join(dir, "AGENTS.md"), "Workflow", false, "")
}

func loadWorkflowDocuments(loader *documentLoader, dir string) ([]Document, error) {
	var docs []Document
	for _, rel := range []string{
		"SKILL.md",
		"WORKFLOW.md",
		"CONTRACT.md",
		filepath.Join("references", "README.md"),
		filepath.Join("references", "WORKFLOW.md"),
		filepath.Join("docs", "WORKFLOW.md"),
	} {
		loaded, err := loader.load(filepath.Join(dir, rel), "Workflow", false, "")
		if err != nil {
			return nil, err
		}
		docs = append(docs, loaded...)
	}
	workflowFiles, err := filepath.Glob(filepath.Join(dir, ".claude", "workflows", "*.md"))
	if err != nil {
		return nil, err
	}
	for _, path := range workflowFiles {
		loaded, err := loader.load(path, "Workflow", false, "")
		if err != nil {
			return nil, err
		}
		docs = append(docs, loaded...)
	}
	return docs, nil
}

func configDir() string {
	if dir := strings.TrimSpace(product.Getenv("NIUMA_CONFIG_DIR")); dir != "" {
		return dir
	}
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, identity.Default().ConfigDirName)
}

func loadClaudeCodeProjectMemory(cwd, prompt string) ([]Document, error) {
	configDir := configDir()
	if configDir == "" || strings.TrimSpace(cwd) == "" {
		return nil, nil
	}
	for _, slug := range claudeCodeProjectMemorySlugs(cwd) {
		docs, err := loadClaudeCodeProjectMemoryPath(filepath.Join(configDir, "projects", slug, "memory", "MEMORY.md"), prompt)
		if err != nil || len(docs) > 0 {
			return docs, err
		}
	}
	return nil, nil
}

func ClaudeCodeProjectMemoryDir(cwd string) string {
	configDir := configDir()
	if configDir == "" || strings.TrimSpace(cwd) == "" {
		return ""
	}
	for _, slug := range claudeCodeProjectMemorySlugs(cwd) {
		if strings.TrimSpace(slug) != "" {
			return filepath.Join(configDir, "projects", slug, "memory")
		}
	}
	return ""
}

func loadClaudeCodeProjectMemoryPath(path, prompt string) ([]Document, error) {
	memoryDir := filepath.Dir(path)
	root, err := os.OpenRoot(memoryDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer root.Close()
	if !memoryPathWithinDir(memoryDir, path) {
		return nil, nil
	}
	data, err := readProjectMemoryFile(root, filepath.Base(path))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	content := limitProjectMemoryContent(string(data))
	if strings.TrimSpace(content) == "" {
		return nil, nil
	}
	docs := []Document{{
		Path:    path,
		Content: strings.TrimSpace(stripHTMLComments(content)),
		Type:    "ClaudeCodeProjectMemory",
	}}
	if strings.TrimSpace(prompt) == "" || promptIgnoresMemory(prompt) {
		return docs, nil
	}
	return append(docs, recallProjectMemoryFilesFromRoot(root, content, prompt)...), nil
}

func recallProjectMemoryFiles(memoryDir, indexContent, prompt string) []Document {
	root, err := os.OpenRoot(memoryDir)
	if err != nil {
		return nil
	}
	defer root.Close()
	return recallProjectMemoryFilesFromRoot(root, indexContent, prompt)
}

// Keep file resolution anchored to the opened directory even if a link changes
// between preview/scoring and the final read.
func recallProjectMemoryFilesFromRoot(root *os.Root, indexContent, prompt string) []Document {
	memoryDir := root.Name()
	queryTokens := memoryRecallTokens(prompt)
	if len(queryTokens) == 0 {
		return nil
	}
	type candidate struct {
		path  string
		score int
	}
	var candidates []candidate
	for relative, hints := range projectMemoryIndexHints(indexContent) {
		path := filepath.Join(memoryDir, relative)
		data, err := readMemoryPreview(root, relative, defaultProjectMemoryPreviewLines)
		if err != nil {
			continue
		}
		name, description := memoryFrontmatterSummary(string(data))
		// Index hints cannot turn malformed or missing metadata into eligible
		// memory. Such files remain available through the model's Read path.
		if strings.TrimSpace(name) == "" && strings.TrimSpace(description) == "" {
			continue
		}
		// Repeated aliases may improve relevance but must not multiply evidence.
		score := 0
		for _, hint := range hints {
			score = max(score, memoryRecallCandidateScore(queryTokens, name, description, hint))
		}
		if score >= recallMinimumScore {
			candidates = append(candidates, candidate{path: path, score: score})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].path < candidates[j].path
	})
	if len(candidates) > defaultProjectMemoryRecallFiles {
		candidates = candidates[:defaultProjectMemoryRecallFiles]
	}
	docs := make([]Document, 0, len(candidates))
	for _, item := range candidates {
		relative, err := filepath.Rel(memoryDir, item.path)
		if err != nil {
			continue
		}
		data, err := readProjectMemoryFile(root, relative)
		if err != nil {
			continue
		}
		content := limitProjectMemoryContent(string(data))
		if strings.TrimSpace(content) == "" {
			continue
		}
		docs = append(docs, Document{
			Path:    item.path,
			Content: strings.TrimSpace(stripHTMLComments(content)),
			Type:    "ClaudeCodeProjectMemoryRecall",
			Parent:  filepath.Join(memoryDir, "MEMORY.md"),
		})
	}
	return docs
}

func memoryPathWithinDir(memoryDir, path string) bool {
	_, err := resolvedMemoryRelativePath(memoryDir, path)
	return err == nil
}

func resolvedMemoryRelativePath(memoryDir, path string) (string, error) {
	resolvedDir, err := filepath.EvalSymlinks(memoryDir)
	if err != nil {
		return "", err
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(resolvedDir, resolvedPath)
	if err != nil {
		return "", err
	}
	if !filepath.IsLocal(relative) {
		return "", os.ErrPermission
	}
	return relative, nil
}

func openProjectMemoryFile(root *os.Root, relative string) (*os.File, error) {
	file, err := root.Open(relative)
	if err == nil {
		return file, nil
	}
	// os.Root rejects absolute symlinks even inside the root. Resolve these
	// to a relative name for compatibility, but keep the final open rooted:
	// replacing a link or directory after resolution cannot escape the root.
	resolved, resolveErr := resolvedMemoryRelativePath(root.Name(), filepath.Join(root.Name(), relative))
	if resolveErr != nil {
		return nil, err
	}
	return root.Open(resolved)
}

func readProjectMemoryFile(root *os.Root, relative string) ([]byte, error) {
	file, err := openProjectMemoryFile(root, relative)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	// The extra rune bytes let the existing truncator choose a UTF-8 boundary.
	return io.ReadAll(io.LimitReader(file, defaultProjectMemoryMaxBytes+utf8.UTFMax))
}

func readMemoryPreview(root *os.Root, relative string, maxLines int) (string, error) {
	file, err := openProjectMemoryFile(root, relative)
	if err != nil {
		return "", err
	}
	defer file.Close()
	scanner := bufio.NewScanner(io.LimitReader(file, defaultProjectMemoryMaxBytes))
	scanner.Buffer(make([]byte, 1024), 64*1024)
	lines := make([]string, 0, maxLines)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if len(lines) == maxLines || (len(lines) > 1 && isProjectMemoryFrontmatterDelimiter(scanner.Text())) {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return strings.Join(lines, "\n"), nil
}

// Closing fences must occupy an unindented line. An indented fence can be
// content of a YAML block scalar; suffixes such as ---key are not fences.
func isProjectMemoryFrontmatterDelimiter(line string) bool {
	return strings.TrimRight(line, " \t\r") == projectMemoryFrontmatterDelimiter
}

func memoryFrontmatterSummary(content string) (name, description string) {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(normalized, projectMemoryFrontmatterDelimiter+"\n") {
		return "", ""
	}
	lines := strings.Split(normalized, "\n")
	for end := 1; end < len(lines); end++ {
		if !isProjectMemoryFrontmatterDelimiter(lines[end]) {
			continue
		}
		var summary struct {
			Name        string `yaml:"name"`
			Description string `yaml:"description"`
		}
		if err := yaml.Unmarshal([]byte(strings.Join(lines[1:end], "\n")), &summary); err != nil {
			return "", ""
		}
		return strings.TrimSpace(summary.Name), strings.TrimSpace(summary.Description)
	}
	return "", ""
}

func promptIgnoresMemory(prompt string) bool {
	lower := strings.ToLower(prompt)
	for _, phrase := range []string{"ignore memory", "ignore all memory", "do not use memory", "don't use memory", "不使用记忆", "忽略记忆"} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

func claudeCodeProjectMemorySlugs(cwd string) []string {
	var slugs []string
	if native := claudeCodeNativeProjectSlug(cwd); native != "" {
		slugs = append(slugs, native)
	}
	if goSlug := session.ProjectSlug(cwd); goSlug != "" && !stringInSlice(slugs, goSlug) {
		slugs = append(slugs, goSlug)
	}
	return slugs
}

func claudeCodeNativeProjectSlug(cwd string) string {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		abs = cwd
	}
	abs = filepath.ToSlash(filepath.Clean(abs))
	if abs == "." || abs == "" {
		return ""
	}
	replacer := strings.NewReplacer("/", "-", ":", "", " ", "-")
	return replacer.Replace(abs)
}

func stringInSlice(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func limitProjectMemoryContent(content string) string {
	if len(content) > defaultProjectMemoryMaxBytes {
		content = truncateAtLineBoundary(content, defaultProjectMemoryMaxBytes)
	}
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(content, "\n")
	if len(lines) > defaultProjectMemoryMaxLines {
		lines = lines[:defaultProjectMemoryMaxLines]
	}
	return strings.Join(lines, "\n")
}

func splitPathList(raw string) []string {
	var out []string
	for _, item := range strings.Split(raw, string(os.PathListSeparator)) {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func (l *documentLoader) load(path, typ string, includeExternal bool, parent string) ([]Document, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	key := filepath.Clean(abs)
	if strings.TrimSpace(l.rootBoundary) != "" && !sameResolvedTree(l.rootBoundary, key) {
		return nil, nil
	}
	if l.seen[key] {
		return nil, nil
	}
	l.seen[key] = true
	doc, ok, err := read(key, typ)
	if err != nil || !ok {
		return nil, err
	}
	doc.Parent = parent
	if !l.matches(doc) {
		return nil, nil
	}
	var out []Document
	out = append(out, doc)
	for _, include := range includePaths(doc.Content) {
		resolved := resolveInclude(key, include)
		if resolved == "" {
			continue
		}
		includeRoot := filepath.Dir(key)
		if strings.TrimSpace(l.rootBoundary) != "" {
			includeRoot = l.rootBoundary
		}
		if !includeExternal && !sameTree(includeRoot, resolved) {
			continue
		}
		children, err := l.load(resolved, typ, includeExternal, key)
		if err != nil {
			return nil, err
		}
		out = append(out, children...)
	}
	return out, nil
}

// matches applies the `paths:` / `excludes:` frontmatter to this turn's file
// set. An empty file set means the turn's scope is unknown, which is not
// evidence of a mismatch, so scoped documents still load. A file matching any
// exclude drops the document.
func (l *documentLoader) matches(doc Document) bool {
	if len(l.files) == 0 {
		return true
	}
	for _, pattern := range doc.Excludes {
		if l.anyFileMatches(pattern) {
			return false
		}
	}
	if len(doc.Paths) == 0 {
		return true
	}
	for _, pattern := range doc.Paths {
		if l.anyFileMatches(pattern) {
			return true
		}
	}
	return false
}

func (l *documentLoader) anyFileMatches(pattern string) bool {
	for _, file := range l.files {
		if pathMatchesPattern(pattern, file) {
			return true
		}
	}
	return false
}

func read(path, typ string) (Document, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Document{}, false, nil
		}
		return Document{}, false, err
	}
	content, paths, excludes := parseFrontmatter(string(data))
	content = strings.TrimSpace(stripHTMLComments(content))
	if strings.EqualFold(strings.TrimSpace(typ), "Workflow") {
		content = stripWorkflowSkillsSystem(content)
	}
	return Document{Path: path, Content: strings.TrimSpace(content), Type: typ, Paths: paths, Excludes: excludes}, true, nil
}

func stripWorkflowSkillsSystem(content string) string {
	text := strings.TrimSpace(content)
	if text == "" {
		return ""
	}
	re := regexp.MustCompile(`(?is)<skills_system\b[^>]*>.*?</skills_system>`)
	text = re.ReplaceAllString(text, "")
	return strings.TrimSpace(text)
}

func projectDirs(cwd string) []string {
	if cwd == "" {
		return nil
	}
	dir, err := filepath.Abs(cwd)
	if err != nil {
		dir = cwd
	}
	var dirs []string
	for {
		dirs = append([]string{dir}, dirs...)
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return dirs
}

func parseFrontmatter(content string) (string, []string, []string) {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return content, nil, nil
	}
	end := strings.Index(normalized[4:], "\n---")
	if end < 0 {
		return content, nil, nil
	}
	header := normalized[4 : 4+end]
	body := normalized[4+end:]
	if strings.HasPrefix(body, "\n---") {
		body = strings.TrimPrefix(body, "\n---")
	}
	body = strings.TrimPrefix(body, "\n")
	var paths, excludes []string
	var activeKey string
	for _, line := range strings.Split(header, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "-") && activeKey != "" {
			values := parseFrontmatterListValue(strings.TrimSpace(strings.TrimPrefix(trimmed, "-")))
			if activeKey == "paths" {
				paths = append(paths, values...)
			} else {
				excludes = append(excludes, values...)
			}
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			activeKey = ""
			continue
		}
		key = strings.TrimSpace(key)
		switch key {
		case "paths":
			activeKey = "paths"
			paths = append(paths, parseFrontmatterListValue(value)...)
		case "exclude", "excludes":
			activeKey = "excludes"
			excludes = append(excludes, parseFrontmatterListValue(value)...)
		default:
			activeKey = ""
		}
	}
	return body, paths, excludes
}

func parseFrontmatterListValue(value string) []string {
	value = strings.Trim(strings.TrimSpace(value), "[]")
	if value == "" {
		return nil
	}
	var out []string
	for _, item := range strings.Split(value, ",") {
		item = strings.Trim(strings.TrimSpace(item), `"'`)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

var includePattern = regexp.MustCompile(`(?:^|\s)@((?:[^\s\\]|\\ )+)`)

func includePaths(content string) []string {
	var out []string
	inCode := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inCode = !inCode
			continue
		}
		if inCode {
			continue
		}
		for _, match := range includePattern.FindAllStringSubmatch(line, -1) {
			if len(match) < 2 {
				continue
			}
			path := strings.ReplaceAll(match[1], `\ `, " ")
			if idx := strings.Index(path, "#"); idx >= 0 {
				path = path[:idx]
			}
			path = strings.TrimSpace(path)
			if path != "" {
				out = append(out, path)
			}
		}
	}
	return out
}

func resolveInclude(parent, include string) string {
	include = strings.TrimSpace(include)
	switch {
	case include == "":
		return ""
	case strings.HasPrefix(include, "~/"):
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return ""
		}
		return filepath.Join(home, strings.TrimPrefix(include, "~/"))
	case filepath.IsAbs(include):
		return include
	default:
		return filepath.Join(filepath.Dir(parent), include)
	}
}

func sameTree(root, path string) bool {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != "..")
}

func sameResolvedTree(root, path string) bool {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	return sameTree(resolvedRoot, resolvedPath)
}

func stripHTMLComments(content string) string {
	re := regexp.MustCompile(`(?s)<!--.*?-->`)
	return re.ReplaceAllString(content, "")
}
