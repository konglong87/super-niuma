package skills

import (
	"archive/zip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/identity"
	"github.com/konglong87/super-niuma/internal/plugins"
	"github.com/konglong87/super-niuma/internal/product"
	"gopkg.in/yaml.v3"
)

const defaultCatalogPromptBudgetBytes = 8192

type Source string

const (
	SourceUser        Source = "user"
	SourceProject     Source = "project"
	SourcePlugin      Source = "plugin"
	SourceBundled     Source = "bundled"
	SourceMarketplace Source = "marketplace"
	SourceMCP         Source = "mcp"
	SourceTenant      Source = "tenant"
)

type Skill struct {
	Name                   string                          `json:"name"`
	LocalName              string                          `json:"local_name,omitempty"`
	Path                   string                          `json:"path"`
	Root                   string                          `json:"root,omitempty"`
	Description            string                          `json:"description,omitempty"`
	WhenToUse              string                          `json:"when_to_use,omitempty"`
	AllowedTools           []string                        `json:"allowed_tools,omitempty"`
	ArgumentHint           string                          `json:"argument_hint,omitempty"`
	Arguments              []string                        `json:"arguments,omitempty"`
	Version                string                          `json:"version,omitempty"`
	Model                  string                          `json:"model,omitempty"`
	DisableModelInvocation bool                            `json:"disable_model_invocation,omitempty"`
	UserInvocable          bool                            `json:"user_invocable"`
	ExecutionContext       string                          `json:"execution_context,omitempty"`
	Agent                  string                          `json:"agent,omitempty"`
	Effort                 string                          `json:"effort,omitempty"`
	Hooks                  map[string][]config.HookCommand `json:"hooks,omitempty"`
	Paths                  []string                        `json:"paths,omitempty"`
	Content                string                          `json:"content,omitempty"`
	PackageRef             string                          `json:"package_ref,omitempty"`
	PackageSHA256          string                          `json:"package_sha256,omitempty"`
	RuntimeRef             string                          `json:"runtime_ref,omitempty"`
	Source                 Source                          `json:"source,omitempty"`
	Plugin                 string                          `json:"plugin,omitempty"`
	Legacy                 bool                            `json:"legacy,omitempty"`
}

type discoveryRoot struct {
	path   string
	source Source
	plugin string
	legacy bool
}

type DiscoveryOptions struct {
	ExplicitRoots []string
	BundledOnly   bool
}

type MarketplaceIndex struct {
	Skills []MarketplaceSkill `json:"skills" yaml:"skills"`
}

type MarketplaceSkill struct {
	Name          string `json:"name" yaml:"name"`
	Description   string `json:"description,omitempty" yaml:"description,omitempty"`
	Version       string `json:"version,omitempty" yaml:"version,omitempty"`
	ContentSHA256 string `json:"content_sha256,omitempty" yaml:"content_sha256,omitempty"`
	Signature     string `json:"signature,omitempty" yaml:"signature,omitempty"`
	PublicKey     string `json:"public_key,omitempty" yaml:"public_key,omitempty"`
	SignatureAlg  string `json:"signature_alg,omitempty" yaml:"signature_alg,omitempty"`
	Content       string `json:"content,omitempty" yaml:"content,omitempty"`
	Path          string `json:"path,omitempty" yaml:"path,omitempty"`
}

type MarketplaceReport struct {
	Source string                   `json:"source"`
	Count  int                      `json:"count"`
	Hashed int                      `json:"hashed"`
	Signed int                      `json:"signed"`
	Items  []MarketplaceReportSkill `json:"items"`
}

type MarketplaceReportSkill struct {
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	Version       string `json:"version,omitempty"`
	ContentSHA256 string `json:"content_sha256,omitempty"`
	Signed        bool   `json:"signed"`
	SignatureAlg  string `json:"signature_alg,omitempty"`
	Path          string `json:"path,omitempty"`
	InlineContent bool   `json:"inline_content"`
}

type MarketplaceStatusReport struct {
	Source    string                   `json:"source"`
	Target    string                   `json:"target"`
	Count     int                      `json:"count"`
	Installed int                      `json:"installed"`
	Missing   int                      `json:"missing"`
	Outdated  int                      `json:"outdated"`
	Current   int                      `json:"current"`
	Items     []MarketplaceStatusSkill `json:"items"`
}

type MarketplaceStatusSkill struct {
	Name               string `json:"name"`
	Status             string `json:"status"`
	InstalledVersion   string `json:"installed_version,omitempty"`
	MarketplaceVersion string `json:"marketplace_version,omitempty"`
	InstalledSHA256    string `json:"installed_sha256,omitempty"`
	ContentSHA256      string `json:"content_sha256,omitempty"`
	Signed             bool   `json:"signed"`
	Path               string `json:"path,omitempty"`
}

type BundledCatalog struct {
	Skills []BundledCatalogSkill `json:"skills" yaml:"skills"`
}

type BundledCatalogSkill struct {
	Name          string   `json:"name" yaml:"name"`
	Description   string   `json:"description,omitempty" yaml:"description,omitempty"`
	WhenToUse     string   `json:"when_to_use,omitempty" yaml:"when_to_use,omitempty"`
	AllowedTools  []string `json:"allowed_tools,omitempty" yaml:"allowed_tools,omitempty"`
	Model         string   `json:"model,omitempty" yaml:"model,omitempty"`
	Context       string   `json:"context,omitempty" yaml:"context,omitempty"`
	Agent         string   `json:"agent,omitempty" yaml:"agent,omitempty"`
	Effort        string   `json:"effort,omitempty" yaml:"effort,omitempty"`
	ContentSHA256 string   `json:"content_sha256,omitempty" yaml:"content_sha256,omitempty"`
}

type BundledCatalogReport struct {
	Expected int                      `json:"expected"`
	Found    int                      `json:"found"`
	Missing  []string                 `json:"missing,omitempty"`
	Extra    []string                 `json:"extra,omitempty"`
	Mismatch []BundledCatalogMismatch `json:"mismatch,omitempty"`
	Matched  []string                 `json:"matched,omitempty"`
}

type BundledCatalogMismatch struct {
	Name  string `json:"name"`
	Field string `json:"field"`
	Want  string `json:"want,omitempty"`
	Got   string `json:"got,omitempty"`
}

func List(cwd string) ([]Skill, error) {
	return ListWithOptions(cwd, DiscoveryOptions{})
}

func ListWithOptions(cwd string, options DiscoveryOptions) ([]Skill, error) {
	roots := discoveryRootsWithOptions(cwd, options)
	seen := map[string]Skill{}
	for _, root := range roots {
		items, err := listRoot(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, item := range items {
			seen[item.Name] = item
		}
	}
	out := make([]Skill, 0, len(seen))
	for _, skill := range seen {
		out = append(out, skill)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func Load(cwd, name string) (Skill, bool, error) {
	return LoadWithOptions(cwd, name, DiscoveryOptions{})
}

func LoadWithOptions(cwd, name string, options DiscoveryOptions) (Skill, bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Skill{}, false, nil
	}
	list, err := ListWithOptions(cwd, options)
	if err != nil {
		return Skill{}, false, err
	}
	for _, skill := range list {
		if skill.Name != name {
			continue
		}
		data, err := os.ReadFile(skill.Path)
		if err != nil {
			return Skill{}, false, err
		}
		skill.Content = string(data)
		return skill, true, nil
	}
	return loadUniqueLocalName(list, name)
}

func discoveryRootsWithOptions(cwd string, options DiscoveryOptions) []discoveryRoot {
	if !options.BundledOnly && len(options.ExplicitRoots) == 0 {
		return discoveryRoots(cwd)
	}
	var roots []discoveryRoot
	for _, path := range bundledSkillRoots() {
		roots = append(roots, discoveryRoot{path: path, source: SourceBundled})
	}
	if options.BundledOnly {
		return roots
	}
	for _, root := range options.ExplicitRoots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		roots = append(roots,
			discoveryRoot{path: filepath.Join(root, ".claude", "skills"), source: SourceProject},
			discoveryRoot{path: filepath.Join(root, ".claude", "commands"), source: SourceProject, legacy: true},
		)
	}
	return roots
}

func Search(cwd, query string) ([]Skill, error) {
	list, err := List(cwd)
	if err != nil {
		return nil, err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return list, nil
	}
	var out []Skill
	for _, skill := range list {
		haystack := strings.ToLower(strings.Join([]string{
			skill.Name,
			skill.LocalName,
			skill.Description,
			skill.WhenToUse,
			string(skill.Source),
			skill.Plugin,
		}, "\n"))
		if strings.Contains(haystack, query) {
			out = append(out, skill)
		}
	}
	return out, nil
}

func ValidateBundledCatalog(cwd, source string) (BundledCatalogReport, error) {
	data, err := readMarketplaceSource(source)
	if err != nil {
		return BundledCatalogReport{}, err
	}
	var expected BundledCatalog
	if err := yaml.Unmarshal(data, &expected); err != nil {
		if err := json.Unmarshal(data, &expected); err != nil {
			return BundledCatalogReport{}, err
		}
	}
	list, err := List(cwd)
	if err != nil {
		return BundledCatalogReport{}, err
	}
	actual := map[string]Skill{}
	for _, skill := range list {
		if skill.Source == SourceBundled {
			actual[skill.Name] = skill
		}
	}
	report := BundledCatalogReport{Expected: len(expected.Skills), Found: len(actual)}
	expectedNames := map[string]bool{}
	for _, want := range expected.Skills {
		name := oneLine(want.Name)
		if name == "" {
			continue
		}
		expectedNames[name] = true
		got, ok := actual[name]
		if !ok {
			report.Missing = append(report.Missing, name)
			continue
		}
		mismatches := compareBundledSkill(got, want)
		report.Mismatch = append(report.Mismatch, mismatches...)
		if len(mismatches) == 0 {
			report.Matched = append(report.Matched, name)
		}
	}
	for name := range actual {
		if !expectedNames[name] {
			report.Extra = append(report.Extra, name)
		}
	}
	sort.Strings(report.Missing)
	sort.Strings(report.Extra)
	sort.Strings(report.Matched)
	sort.Slice(report.Mismatch, func(i, j int) bool {
		if report.Mismatch[i].Name == report.Mismatch[j].Name {
			return report.Mismatch[i].Field < report.Mismatch[j].Field
		}
		return report.Mismatch[i].Name < report.Mismatch[j].Name
	})
	return report, nil
}

func compareBundledSkill(got Skill, want BundledCatalogSkill) []BundledCatalogMismatch {
	var mismatch []BundledCatalogMismatch
	add := func(field, wantValue, gotValue string) {
		if strings.TrimSpace(wantValue) != "" && strings.TrimSpace(wantValue) != strings.TrimSpace(gotValue) {
			mismatch = append(mismatch, BundledCatalogMismatch{Name: got.Name, Field: field, Want: wantValue, Got: gotValue})
		}
	}
	add("description", want.Description, got.Description)
	add("when_to_use", want.WhenToUse, got.WhenToUse)
	add("model", want.Model, got.Model)
	add("context", want.Context, got.ExecutionContext)
	add("agent", want.Agent, got.Agent)
	add("effort", want.Effort, got.Effort)
	if len(want.AllowedTools) > 0 && strings.Join(want.AllowedTools, "\n") != strings.Join(got.AllowedTools, "\n") {
		mismatch = append(mismatch, BundledCatalogMismatch{Name: got.Name, Field: "allowed_tools", Want: strings.Join(want.AllowedTools, ","), Got: strings.Join(got.AllowedTools, ",")})
	}
	if strings.TrimSpace(want.ContentSHA256) != "" {
		data, err := os.ReadFile(got.Path)
		gotHash := ""
		if err == nil {
			gotHash = fmt.Sprintf("%x", sha256.Sum256(data))
		}
		if gotHash != strings.TrimSpace(want.ContentSHA256) {
			mismatch = append(mismatch, BundledCatalogMismatch{Name: got.Name, Field: "content_sha256", Want: want.ContentSHA256, Got: gotHash})
		}
	}
	return mismatch
}

func CatalogPrompt(cwd string) (string, error) {
	return CatalogPromptForPrompt(cwd, "")
}

func CatalogPromptForPrompt(cwd, prompt string) (string, error) {
	catalog, _, err := CatalogPromptForPromptWithNames(cwd, prompt)
	return catalog, err
}

func CatalogPromptForPromptWithNames(cwd, prompt string) (string, []string, error) {
	list, err := List(cwd)
	if err != nil {
		return "", nil, err
	}
	catalog, names := CatalogPromptFromSkillsWithNames(list, prompt, nil)
	return catalog, names, nil
}

func CatalogPromptForPromptExcluding(cwd, prompt string, seen map[string]bool) (string, []string, error) {
	list, err := List(cwd)
	if err != nil {
		return "", nil, err
	}
	catalog, names := CatalogPromptFromSkillsWithNames(list, prompt, seen)
	return catalog, names, nil
}

func CatalogPromptFromSkills(list []Skill, prompt string) string {
	catalog, _ := CatalogPromptFromSkillsWithNames(list, prompt, nil)
	return catalog
}

func CatalogPromptFromSkillsWithNames(list []Skill, prompt string, seen map[string]bool) (string, []string) {
	if len(list) == 0 {
		return "", nil
	}
	var visible []Skill
	for _, skill := range list {
		if skill.DisableModelInvocation || !skillMatchesPrompt(skill, prompt) {
			continue
		}
		if seen != nil && seen[strings.ToLower(skill.Name)] {
			continue
		}
		visible = append(visible, skill)
	}
	if len(visible) == 0 {
		return "", nil
	}
	var b strings.Builder
	b.WriteString("# Available Skills\n")
	b.WriteString("Only the following skill metadata is preloaded. When a skill matches the user's request or the current files/paths/tool evidence, invoking the relevant Skill tool first is a blocking requirement before taking task-specific actions or giving task-specific answers. Use the skill's exact name to load the full SKILL.md instructions. After loading SKILL.md, read referenced files only if the skill asks for them.\n")
	names := make([]string, 0, len(visible))
	for _, skill := range visible {
		names = append(names, skill.Name)
		b.WriteString("- ")
		b.WriteString(skill.Name)
		if strings.TrimSpace(skill.Description) != "" {
			b.WriteString(": ")
			b.WriteString(strings.TrimSpace(skill.Description))
		}
		if strings.TrimSpace(skill.WhenToUse) != "" {
			b.WriteString(" When to use: ")
			b.WriteString(strings.TrimSpace(skill.WhenToUse))
		}
		if skill.Legacy {
			b.WriteString(" (legacy command)")
		}
		b.WriteString("\n")
	}
	catalog := strings.TrimRight(b.String(), "\n")
	if budget := catalogPromptBudgetBytes(); budget > 0 && len(catalog) > budget {
		catalog = compactCatalogPrompt(visible, budget)
	}
	return catalog, names
}

func catalogPromptBudgetBytes() int {
	raw := strings.TrimSpace(os.Getenv("NIUMA_SKILLS_CATALOG_BUDGET_BYTES"))
	if raw == "" {
		return defaultCatalogPromptBudgetBytes
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return defaultCatalogPromptBudgetBytes
	}
	return value
}

func compactCatalogPrompt(list []Skill, budget int) string {
	var names []string
	for _, skill := range list {
		names = append(names, skill.Name)
	}
	var b strings.Builder
	b.WriteString("# Available Skills\n")
	b.WriteString("Only skill names and compact metadata are preloaded. If a skill name or compact description matches the user's request, current files, or tool evidence, call the Skill tool with the exact name before task-specific work. The Skill tool loads full SKILL.md instructions.\n")
	b.WriteString("Skill names: ")
	b.WriteString(strings.Join(names, ", "))
	b.WriteString("\n\n## Compact skill metadata\n")
	omitted := 0
	for i, skill := range list {
		line := compactSkillLine(skill, 120)
		nextLen := len(line) + 1
		if b.Len()+nextLen+80 > budget {
			omitted = len(list) - i
			break
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	if omitted > 0 {
		note := "[Budgeted catalog: " + strconv.Itoa(omitted) + " skill metadata entries omitted; exact names remain listed above.]"
		if b.Len()+len(note)+1 <= budget {
			b.WriteString(note)
		}
	}
	out := strings.TrimSpace(b.String())
	if len(out) > budget {
		out = truncateSkillCatalog(out, budget)
	}
	return out
}

func compactSkillLine(skill Skill, limit int) string {
	line := "- " + skill.Name
	details := strings.TrimSpace(skill.Description)
	if when := strings.TrimSpace(skill.WhenToUse); when != "" {
		if details != "" {
			details += " "
		}
		details += "When to use: " + when
	}
	if skill.Legacy {
		if details != "" {
			details += " "
		}
		details += "(legacy command)"
	}
	details = strings.Join(strings.Fields(details), " ")
	if details != "" {
		line += ": " + details
	}
	if limit > 0 && len(line) > limit {
		line = strings.TrimSpace(line[:limit-3]) + "..."
	}
	return line
}

func truncateSkillCatalog(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	cut := text[:limit]
	if idx := strings.LastIndex(cut, "\n"); idx > 0 {
		cut = cut[:idx]
	}
	return strings.TrimSpace(cut)
}

func skillMatchesPrompt(skill Skill, prompt string) bool {
	if len(skill.Paths) == 0 || strings.TrimSpace(prompt) == "" {
		return true
	}
	prompt = filepath.ToSlash(prompt)
	for _, pattern := range skill.Paths {
		pattern = strings.TrimSpace(filepath.ToSlash(pattern))
		if pattern == "" || pattern == "**" {
			return true
		}
		needle := strings.TrimSuffix(strings.TrimSuffix(pattern, "/**"), "/*")
		if needle != "" && strings.Contains(prompt, needle) {
			return true
		}
	}
	return false
}

func discoveryRoots(cwd string) []discoveryRoot {
	var roots []discoveryRoot
	id := config.CurrentIdentity(cwd)
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		roots = append(roots,
			discoveryRoot{path: filepath.Join(home, ".claude", "skills"), source: SourceUser},
			discoveryRoot{path: filepath.Join(home, ".claude", "commands"), source: SourceUser, legacy: true},
			discoveryRoot{path: filepath.Join(home, ".claude", "skills-marketplace"), source: SourceMarketplace},
			discoveryRoot{path: filepath.Join(home, ".claude", "mcp-skills"), source: SourceMCP},
		)
	}
	for _, globalRoot := range globalSkillDiscoveryRoots(id) {
		roots = append(roots,
			discoveryRoot{path: filepath.Join(globalRoot, "skills"), source: SourceUser},
			discoveryRoot{path: filepath.Join(globalRoot, "commands"), source: SourceUser, legacy: true},
			discoveryRoot{path: filepath.Join(globalRoot, "skills-marketplace"), source: SourceMarketplace},
			discoveryRoot{path: filepath.Join(globalRoot, "mcp-skills"), source: SourceMCP},
		)
	}
	for _, path := range bundledSkillRoots() {
		roots = append(roots, discoveryRoot{path: path, source: SourceBundled})
	}
	for _, path := range mcpSkillRoots() {
		roots = append(roots, discoveryRoot{path: path, source: SourceMCP})
	}
	if cwd != "" {
		if root := nearestClaudeDir(cwd); root != "" {
			roots = append(roots,
				discoveryRoot{path: filepath.Join(root, "skills"), source: SourceProject},
				discoveryRoot{path: filepath.Join(root, "commands"), source: SourceProject, legacy: true},
			)
		}
		legacyOwnedRoot := filepath.Join(cwd, product.LegacyConfigDirName)
		roots = append(roots,
			discoveryRoot{path: filepath.Join(legacyOwnedRoot, "skills"), source: SourceProject},
			discoveryRoot{path: filepath.Join(legacyOwnedRoot, "commands"), source: SourceProject, legacy: true},
		)
		root := id.ProjectConfigRoot(cwd)
		roots = append(roots,
			discoveryRoot{path: filepath.Join(root, "skills"), source: SourceProject},
			discoveryRoot{path: filepath.Join(root, "commands"), source: SourceProject, legacy: true},
		)
	}
	for _, path := range envSkillRoots() {
		roots = append(roots, discoveryRoot{path: path, source: SourceUser})
	}
	if manifests, err := plugins.List(cwd); err == nil {
		for _, manifest := range manifests {
			pluginName := manifest.Name
			if strings.TrimSpace(pluginName) == "" {
				pluginName = filepath.Base(manifest.Path)
			}
			if len(manifest.Skills) == 0 {
				roots = append(roots, discoveryRoot{path: filepath.Join(manifest.Path, "skills"), source: SourcePlugin, plugin: pluginName})
			} else {
				for _, skillPath := range manifest.Skills {
					roots = append(roots, discoveryRoot{path: resolvePluginPath(manifest.Path, skillPath), source: SourcePlugin, plugin: pluginName})
				}
			}
			roots = append(roots, discoveryRoot{path: filepath.Join(manifest.Path, "commands"), source: SourcePlugin, plugin: pluginName, legacy: true})
		}
	}
	return roots
}

func SyncMarketplace(targetRoot, source string) ([]Skill, error) {
	targetRoot, err := marketplaceTargetRoot(targetRoot)
	if err != nil {
		return nil, err
	}
	index, err := loadMarketplaceIndex(source)
	if err != nil {
		return nil, err
	}
	var synced []Skill
	for _, item := range index.Skills {
		skill, err := writeMarketplaceSkill(targetRoot, source, item)
		if err != nil {
			return nil, err
		}
		if skill.Name != "" {
			synced = append(synced, skill)
		}
	}
	return synced, nil
}

func InstallMarketplace(targetRoot, source, name string) (Skill, error) {
	targetRoot, err := marketplaceTargetRoot(targetRoot)
	if err != nil {
		return Skill{}, err
	}
	name = oneLine(name)
	if name == "" {
		return Skill{}, fmt.Errorf("skill name is required")
	}
	index, err := loadMarketplaceIndex(source)
	if err != nil {
		return Skill{}, err
	}
	for _, item := range index.Skills {
		if oneLine(item.Name) != name {
			continue
		}
		skill, err := writeMarketplaceSkill(targetRoot, source, item)
		if err != nil {
			return Skill{}, err
		}
		return skill, nil
	}
	return Skill{}, fmt.Errorf("marketplace skill not found: %s", name)
}

func SearchMarketplace(source, query string) ([]MarketplaceSkill, error) {
	index, err := loadMarketplaceIndex(source)
	if err != nil {
		return nil, err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	var out []MarketplaceSkill
	for _, item := range index.Skills {
		if strings.TrimSpace(item.Name) == "" {
			continue
		}
		if query == "" {
			out = append(out, item)
			continue
		}
		haystack := strings.ToLower(strings.Join([]string{item.Name, item.Description, item.Path, item.Content}, "\n"))
		if strings.Contains(haystack, query) {
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func InspectMarketplace(source string) (MarketplaceReport, error) {
	index, err := loadMarketplaceIndex(source)
	if err != nil {
		return MarketplaceReport{}, err
	}
	report := MarketplaceReport{Source: source}
	for _, item := range index.Skills {
		name := oneLine(item.Name)
		if name == "" {
			continue
		}
		signed := strings.TrimSpace(item.Signature) != "" && strings.TrimSpace(item.PublicKey) != ""
		alg := strings.TrimSpace(item.SignatureAlg)
		if signed && alg == "" {
			alg = "ed25519"
		}
		if strings.TrimSpace(item.ContentSHA256) != "" {
			report.Hashed++
		}
		if signed {
			report.Signed++
		}
		report.Items = append(report.Items, MarketplaceReportSkill{
			Name:          name,
			Description:   oneLine(item.Description),
			Version:       oneLine(item.Version),
			ContentSHA256: strings.TrimSpace(item.ContentSHA256),
			Signed:        signed,
			SignatureAlg:  alg,
			Path:          strings.TrimSpace(item.Path),
			InlineContent: strings.TrimSpace(item.Content) != "",
		})
	}
	sort.Slice(report.Items, func(i, j int) bool {
		return report.Items[i].Name < report.Items[j].Name
	})
	report.Count = len(report.Items)
	return report, nil
}

// MarketplaceStatus compares a marketplace index with the local marketplace root.
// It is intentionally metadata-first: hashes are authoritative when present,
// otherwise version metadata is used, and skills without either are marked installed.
func MarketplaceStatus(targetRoot, source string) (MarketplaceStatusReport, error) {
	targetRoot, err := marketplaceTargetRoot(targetRoot)
	if err != nil {
		return MarketplaceStatusReport{}, err
	}
	index, err := loadMarketplaceIndex(source)
	if err != nil {
		return MarketplaceStatusReport{}, err
	}
	report := MarketplaceStatusReport{Source: source, Target: targetRoot}
	for _, item := range index.Skills {
		name := oneLine(item.Name)
		if name == "" {
			continue
		}
		status := MarketplaceStatusSkill{
			Name:               name,
			Status:             "missing",
			MarketplaceVersion: oneLine(item.Version),
			ContentSHA256:      strings.TrimSpace(item.ContentSHA256),
			Signed:             strings.TrimSpace(item.Signature) != "" && strings.TrimSpace(item.PublicKey) != "",
			Path:               strings.TrimSpace(item.Path),
		}
		installedPath := filepath.Join(targetRoot, name, "SKILL.md")
		data, err := os.ReadFile(installedPath)
		if err != nil {
			report.Missing++
			report.Items = append(report.Items, status)
			continue
		}
		status.Status = "installed"
		status.InstalledSHA256 = fmt.Sprintf("%x", sha256.Sum256(data))
		meta, _ := parseFrontmatter(string(data))
		status.InstalledVersion = metaString(meta, "version")
		switch {
		case status.ContentSHA256 != "" && !strings.EqualFold(status.ContentSHA256, status.InstalledSHA256):
			status.Status = "outdated"
			report.Outdated++
		case status.MarketplaceVersion != "" && status.InstalledVersion != "" && status.MarketplaceVersion != status.InstalledVersion:
			status.Status = "outdated"
			report.Outdated++
		default:
			status.Status = "current"
			report.Current++
		}
		report.Installed++
		report.Items = append(report.Items, status)
	}
	sort.Slice(report.Items, func(i, j int) bool {
		return report.Items[i].Name < report.Items[j].Name
	})
	report.Count = len(report.Items)
	return report, nil
}

func marketplaceTargetRoot(targetRoot string) (string, error) {
	if strings.TrimSpace(targetRoot) == "" {
		path, err := config.CurrentIdentity("").GlobalStatePath("skills-marketplace")
		if err != nil {
			return "", err
		}
		targetRoot = path
	}
	if err := os.MkdirAll(targetRoot, 0755); err != nil {
		return "", err
	}
	return targetRoot, nil
}

func globalSkillDiscoveryRoots(id identity.Identity) []string {
	seen := map[string]bool{}
	var roots []string
	add := func(path string) {
		path = strings.TrimSpace(filepath.Clean(path))
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		roots = append(roots, path)
	}
	if legacyRoot, err := identity.LegacyGlobalConfigRoot(); err == nil {
		add(legacyRoot)
	}
	if legacyOwnedRoot, err := identity.LegacyOwnedGlobalConfigRoot(); err == nil {
		add(legacyOwnedRoot)
	}
	if defaultRoot, err := identity.Default().GlobalConfigRoot(); err == nil {
		add(defaultRoot)
	}
	if ownedRoot, err := id.GlobalConfigRoot(); err == nil {
		add(ownedRoot)
	}
	return roots
}

func loadMarketplaceIndex(source string) (MarketplaceIndex, error) {
	data, err := readMarketplaceSource(source)
	if err != nil {
		return MarketplaceIndex{}, err
	}
	var index MarketplaceIndex
	if err := yaml.Unmarshal(data, &index); err != nil {
		if err := json.Unmarshal(data, &index); err != nil {
			return MarketplaceIndex{}, err
		}
	}
	return index, nil
}

func writeMarketplaceSkill(targetRoot, source string, item MarketplaceSkill) (Skill, error) {
	name := oneLine(item.Name)
	if name == "" {
		return Skill{}, nil
	}
	content := item.Content
	if strings.TrimSpace(content) == "" && strings.TrimSpace(item.Path) != "" {
		sourcePath := item.Path
		if !filepath.IsAbs(sourcePath) && !isHTTPSource(source) {
			sourcePath = filepath.Join(filepath.Dir(source), sourcePath)
		}
		raw, err := readMarketplaceSource(sourcePath)
		if err != nil {
			return Skill{}, err
		}
		content = string(raw)
	}
	if strings.TrimSpace(content) == "" {
		content = "# " + name
	}
	if wantHash := strings.TrimSpace(item.ContentSHA256); wantHash != "" {
		gotHash := fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
		if !strings.EqualFold(gotHash, wantHash) {
			return Skill{}, fmt.Errorf("marketplace skill %s content_sha256 mismatch: want %s got %s", name, wantHash, gotHash)
		}
	}
	if err := verifyMarketplaceSignature(name, []byte(content), item); err != nil {
		return Skill{}, err
	}
	if !strings.HasPrefix(strings.TrimSpace(content), "---") {
		var meta strings.Builder
		fmt.Fprintf(&meta, "---\nname: %s\n", name)
		if strings.TrimSpace(item.Description) != "" {
			fmt.Fprintf(&meta, "description: %s\n", oneLine(item.Description))
		}
		if strings.TrimSpace(item.Version) != "" {
			fmt.Fprintf(&meta, "version: %s\n", oneLine(item.Version))
		}
		meta.WriteString("---\n")
		content = meta.String() + strings.TrimSpace(content) + "\n"
	}
	dest := filepath.Join(targetRoot, name)
	if err := os.MkdirAll(dest, 0755); err != nil {
		return Skill{}, err
	}
	path := filepath.Join(dest, "SKILL.md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return Skill{}, err
	}
	return Skill{Name: name, Version: oneLine(item.Version), Path: path, Source: SourceMarketplace}, nil
}

func verifyMarketplaceSignature(name string, content []byte, item MarketplaceSkill) error {
	signature := strings.TrimSpace(item.Signature)
	publicKey := strings.TrimSpace(item.PublicKey)
	if signature == "" && publicKey == "" {
		return nil
	}
	if signature == "" || publicKey == "" {
		return fmt.Errorf("marketplace skill %s signature and public_key must be provided together", name)
	}
	alg := strings.ToLower(strings.TrimSpace(item.SignatureAlg))
	if alg == "" {
		alg = "ed25519"
	}
	if alg != "ed25519" {
		return fmt.Errorf("marketplace skill %s unsupported signature_alg: %s", name, item.SignatureAlg)
	}
	key, err := base64.StdEncoding.DecodeString(publicKey)
	if err != nil {
		return fmt.Errorf("marketplace skill %s public_key is not base64: %w", name, err)
	}
	sig, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return fmt.Errorf("marketplace skill %s signature is not base64: %w", name, err)
	}
	if len(key) != ed25519.PublicKeySize {
		return fmt.Errorf("marketplace skill %s public_key must be %d bytes", name, ed25519.PublicKeySize)
	}
	if !ed25519.Verify(ed25519.PublicKey(key), content, sig) {
		return fmt.Errorf("marketplace skill %s signature verification failed", name)
	}
	return nil
}

func PackageSkill(sourceDir, outputPath string) error {
	sourceDir = strings.TrimSpace(sourceDir)
	outputPath = strings.TrimSpace(outputPath)
	if sourceDir == "" {
		return fmt.Errorf("skill source directory is required")
	}
	info, err := os.Stat(sourceDir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("skill source must be a directory: %s", sourceDir)
	}
	if _, err := os.Stat(filepath.Join(sourceDir, "SKILL.md")); err != nil {
		return fmt.Errorf("SKILL.md is required in %s: %w", sourceDir, err)
	}
	if outputPath == "" {
		outputPath = filepath.Clean(sourceDir) + ".skill.zip"
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return err
	}
	out, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer out.Close()
	zw := zip.NewWriter(out)
	err = filepath.WalkDir(sourceDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = rel
		header.Method = zip.Deflate
		writer, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(writer, in)
		closeErr := in.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if closeErr := zw.Close(); err == nil {
		err = closeErr
	}
	return err
}

func readMarketplaceSource(source string) ([]byte, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil, fmt.Errorf("marketplace source is required")
	}
	if isHTTPSource(source) {
		res, err := http.Get(source)
		if err != nil {
			return nil, err
		}
		defer res.Body.Close()
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			return nil, fmt.Errorf("marketplace source returned HTTP %d", res.StatusCode)
		}
		return io.ReadAll(io.LimitReader(res.Body, 5*1024*1024))
	}
	return os.ReadFile(source)
}

func isHTTPSource(source string) bool {
	return strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://")
}

func listRoot(root discoveryRoot) ([]Skill, error) {
	if !root.legacy {
		if data, err := os.ReadFile(filepath.Join(root.path, "SKILL.md")); err == nil {
			localName := filepath.Base(root.path)
			meta, _ := parseFrontmatter(string(data))
			if name := metaString(meta, "name"); name != "" {
				localName = name
			}
			name := invocationName(root.plugin, localName)
			skill := SkillFromContent(name, localName, string(data))
			skill.Path = filepath.Join(root.path, "SKILL.md")
			skill.Root = root.path
			skill.Source = root.source
			skill.Plugin = root.plugin
			skill.Content = ""
			return []Skill{skill}, nil
		}
	}
	entries, err := os.ReadDir(root.path)
	if err != nil {
		return nil, err
	}
	var out []Skill
	for _, entry := range entries {
		path := filepath.Join(root.path, entry.Name())
		var skillFile, localName string
		switch {
		case entry.IsDir():
			skillFile = filepath.Join(path, "SKILL.md")
			localName = entry.Name()
		case root.legacy && strings.EqualFold(filepath.Ext(entry.Name()), ".md"):
			skillFile = path
			localName = strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		default:
			continue
		}
		data, err := os.ReadFile(skillFile)
		if err != nil {
			continue
		}
		meta, _ := parseFrontmatter(string(data))
		if name := metaString(meta, "name"); name != "" {
			localName = name
		}
		name := invocationName(root.plugin, localName)
		skill := SkillFromContent(name, localName, string(data))
		skill.Path = skillFile
		skill.Root = filepath.Dir(skillFile)
		skill.Source = root.source
		skill.Plugin = root.plugin
		skill.Legacy = root.legacy
		skill.Content = ""
		out = append(out, skill)
	}
	return out, nil
}

func SkillFromContent(name, localName, content string) Skill {
	meta, body := parseFrontmatter(content)
	if localName = strings.TrimSpace(localName); localName == "" {
		localName = strings.TrimSpace(name)
	}
	if metaName := metaString(meta, "name"); metaName != "" && !strings.Contains(name, ":") {
		localName = metaName
		name = metaName
	}
	description := metaString(meta, "description")
	if description == "" {
		description = firstParagraph(body)
	}
	userInvocable := true
	if value, ok := metaBoolAny(meta, "user-invocable", "user_invocable"); ok {
		userInvocable = value
	}
	return Skill{
		Name:                   strings.TrimSpace(name),
		LocalName:              localName,
		Description:            description,
		WhenToUse:              firstNonEmpty(metaString(meta, "when_to_use"), metaString(meta, "whenToUse")),
		AllowedTools:           firstNonEmptyList(metaStringList(meta, "allowed-tools"), metaStringList(meta, "allowed_tools"), metaStringList(meta, "allowedTools")),
		ArgumentHint:           firstNonEmpty(metaString(meta, "argument-hint"), metaString(meta, "argument_hint"), metaString(meta, "argumentHint")),
		Arguments:              metaStringList(meta, "arguments"),
		Version:                metaString(meta, "version"),
		Model:                  metaString(meta, "model"),
		DisableModelInvocation: metaBoolDefaultAny(meta, false, "disable-model-invocation", "disable_model_invocation", "disableModelInvocation"),
		UserInvocable:          userInvocable,
		ExecutionContext:       metaString(meta, "context"),
		Agent:                  metaString(meta, "agent"),
		Effort:                 metaString(meta, "effort"),
		Hooks:                  metaHooks(meta, "hooks"),
		Paths:                  metaStringList(meta, "paths"),
		Content:                content,
	}
}

func loadUniqueLocalName(list []Skill, localName string) (Skill, bool, error) {
	var matches []Skill
	for _, skill := range list {
		if skill.LocalName == localName {
			matches = append(matches, skill)
		}
	}
	if len(matches) != 1 {
		return Skill{}, false, nil
	}
	data, err := os.ReadFile(matches[0].Path)
	if err != nil {
		return Skill{}, false, err
	}
	matches[0].Content = string(data)
	return matches[0], true, nil
}

func invocationName(pluginName, localName string) string {
	localName = strings.TrimSpace(localName)
	pluginName = strings.TrimSpace(pluginName)
	if pluginName == "" {
		return localName
	}
	return pluginName + ":" + localName
}

func parseFrontmatter(content string) (map[string]any, string) {
	meta := map[string]any{}
	content = strings.TrimPrefix(content, "\ufeff")
	if !strings.HasPrefix(content, "---\n") && !strings.HasPrefix(content, "---\r\n") {
		return meta, content
	}
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	rest := strings.TrimPrefix(normalized, "---\n")
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		if strings.HasSuffix(rest, "\n---") {
			end = len(rest) - len("\n---")
		} else {
			return meta, content
		}
	}
	header := rest[:end]
	bodyStart := end + len("\n---\n")
	if bodyStart > len(rest) {
		bodyStart = len(rest)
	}
	body := rest[bodyStart:]
	if err := yaml.Unmarshal([]byte(header), &meta); err != nil {
		return map[string]any{}, body
	}
	return meta, body
}

func metaString(meta map[string]any, key string) string {
	value, ok := meta[key]
	if !ok {
		return ""
	}
	switch v := value.(type) {
	case string:
		return oneLine(v)
	case fmt.Stringer:
		return oneLine(v.String())
	case int, int64, float64, bool:
		return oneLine(fmt.Sprint(v))
	default:
		return ""
	}
}

func metaStringList(meta map[string]any, key string) []string {
	value, ok := meta[key]
	if !ok || value == nil {
		return nil
	}
	switch v := value.(type) {
	case string:
		return splitList(v)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			text := oneLine(fmt.Sprint(item))
			if text != "" {
				out = append(out, text)
			}
		}
		return out
	case []string:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if text := oneLine(item); text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func metaBool(meta map[string]any, key string) (bool, bool) {
	value, ok := meta[key]
	if !ok {
		return false, false
	}
	switch v := value.(type) {
	case bool:
		return v, true
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "yes", "1", "on":
			return true, true
		case "false", "no", "0", "off":
			return false, true
		}
	}
	return false, false
}

func metaBoolDefault(meta map[string]any, key string, fallback bool) bool {
	if value, ok := metaBool(meta, key); ok {
		return value
	}
	return fallback
}

func metaBoolAny(meta map[string]any, keys ...string) (bool, bool) {
	for _, key := range keys {
		if value, ok := metaBool(meta, key); ok {
			return value, true
		}
	}
	return false, false
}

func metaBoolDefaultAny(meta map[string]any, fallback bool, keys ...string) bool {
	if value, ok := metaBoolAny(meta, keys...); ok {
		return value
	}
	return fallback
}

func metaHooks(meta map[string]any, key string) map[string][]config.HookCommand {
	value, ok := meta[key]
	if !ok || value == nil {
		return nil
	}
	raw, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	out := map[string][]config.HookCommand{}
	for event, commands := range raw {
		event = oneLine(event)
		if event == "" {
			continue
		}
		for _, command := range hookCommands(commands) {
			out[event] = append(out[event], command)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func hookCommands(value any) []config.HookCommand {
	switch v := value.(type) {
	case string:
		if cmd := strings.TrimSpace(v); cmd != "" {
			return []config.HookCommand{{Command: cmd}}
		}
	case []any:
		var out []config.HookCommand
		for _, item := range v {
			out = append(out, hookCommands(item)...)
		}
		return out
	case map[string]any:
		if cmd := metaString(v, "command"); cmd != "" {
			return []config.HookCommand{
				{
					Command: cmd,
					Matcher: firstNonEmpty(metaString(v, "matcher"), metaString(v, "matches")),
					Tool:    metaString(v, "tool"),
					Tools:   firstNonEmptyList(metaStringList(v, "tools"), metaStringList(v, "tool_names"), metaStringList(v, "toolNames")),
				},
			}
		}
	}
	return nil
}

func splitList(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '\n'
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if text := oneLine(field); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstNonEmptyList(values ...[]string) []string {
	for _, value := range values {
		if len(value) > 0 {
			return value
		}
	}
	return nil
}

func oneLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func envSkillRoots() []string {
	return envRoots("CLAUDE_SKILL_PATHS", "NIUMA_SKILL_PATHS")
}

func bundledSkillRoots() []string {
	return envRoots("CLAUDE_BUNDLED_SKILLS_PATHS", "NIUMA_BUNDLED_SKILLS_PATHS")
}

func mcpSkillRoots() []string {
	return envRoots("CLAUDE_MCP_SKILL_PATHS", "NIUMA_MCP_SKILL_PATHS")
}

func envRoots(names ...string) []string {
	var roots []string
	for _, name := range names {
		for _, item := range filepath.SplitList(strings.TrimSpace(os.Getenv(name))) {
			item = strings.TrimSpace(item)
			if item != "" {
				roots = append(roots, item)
			}
		}
	}
	return roots
}

func nearestClaudeDir(cwd string) string {
	dir, err := filepath.Abs(cwd)
	if err != nil {
		dir = cwd
	}
	for {
		candidate := filepath.Join(dir, ".claude")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func firstParagraph(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	for _, sep := range []string{"\n\n", "\r\n\r\n"} {
		if idx := strings.Index(content, sep); idx >= 0 {
			content = content[:idx]
			break
		}
	}
	lines := strings.Split(content, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(strings.TrimPrefix(lines[i], "#"))
	}
	return strings.TrimSpace(strings.Join(lines, " "))
}

func resolvePluginPath(pluginRoot, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(pluginRoot, path)
}

func Validate(cwd string) error {
	list, err := List(cwd)
	if err != nil {
		return err
	}
	for _, skill := range list {
		if strings.TrimSpace(skill.Name) == "" {
			return fmt.Errorf("skill with empty name at %s", skill.Path)
		}
		if strings.TrimSpace(skill.Description) == "" {
			return fmt.Errorf("skill %s is missing description", skill.Name)
		}
	}
	return nil
}
