package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/konglong87/super-niuma/internal/plugins"
	"gopkg.in/yaml.v3"
)

type Agent struct {
	Name                               string            `json:"name"`
	Description                        string            `json:"description,omitempty"`
	Tools                              []string          `json:"tools,omitempty"`
	DisallowedTools                    []string          `json:"disallowedTools,omitempty"`
	MCPServers                         []MCPServerSpec   `json:"mcpServers,omitempty"`
	CriticalSystemReminderExperimental string            `json:"criticalSystemReminder_EXPERIMENTAL,omitempty"`
	Skills                             []string          `json:"skills,omitempty"`
	Model                              string            `json:"model,omitempty"`
	InitialPrompt                      string            `json:"initialPrompt,omitempty"`
	MaxTurns                           int               `json:"maxTurns,omitempty"`
	Background                         bool              `json:"background,omitempty"`
	OmitGitStatus                      bool              `json:"omitGitStatus,omitempty"`
	Memory                             string            `json:"memory,omitempty"`
	Effort                             string            `json:"effort,omitempty"`
	PermissionMode                     string            `json:"permissionMode,omitempty"`
	Path                               string            `json:"path"`
	Source                             string            `json:"source,omitempty"`
	Plugin                             string            `json:"plugin,omitempty"`
	Prompt                             string            `json:"prompt,omitempty"`
	UnknownFields                      map[string]string `json:"unknown_fields,omitempty"`
}

type MCPServerSpec struct {
	Name   string         `json:"name,omitempty"`
	Config map[string]any `json:"config,omitempty"`
}

type Diagnostic struct {
	Agent   string `json:"agent"`
	Path    string `json:"path,omitempty"`
	Level   string `json:"level"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

type DoctorReport struct {
	Status      string       `json:"status"`
	Agents      int          `json:"agents"`
	Errors      int          `json:"errors"`
	Warnings    int          `json:"warnings"`
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
}

func List(cwd string) ([]Agent, error) {
	var roots []agentRoot
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		roots = append(roots, agentRoot{Path: filepath.Join(home, ".claude", "agents"), Source: "user", Priority: 10})
	}
	if pluginPaths, err := plugins.AgentPaths(cwd); err == nil {
		for _, pluginPath := range pluginPaths {
			roots = append(roots, agentRoot{Path: pluginPath.Path, Source: "plugin", Plugin: pluginPath.Plugin, Priority: 20})
		}
	}
	if cwd != "" {
		if root := nearestClaudeDir(cwd); root != "" {
			roots = append(roots, agentRoot{Path: filepath.Join(root, "agents"), Source: "project", Priority: 30})
		}
	}
	seen := map[string]agentCandidate{}
	for _, agent := range BuiltIns() {
		seen[agent.Name] = agentCandidate{Agent: agent, Priority: 0}
	}
	for _, root := range roots {
		entries, err := os.ReadDir(root.Path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			path := filepath.Join(root.Path, entry.Name())
			agent, err := parseFile(path)
			if err != nil {
				return nil, err
			}
			if agent.Name == "" {
				agent.Name = strings.TrimSuffix(entry.Name(), ".md")
			}
			agent.Source = root.Source
			agent.Plugin = root.Plugin
			current, exists := seen[agent.Name]
			if !exists || root.Priority >= current.Priority {
				seen[agent.Name] = agentCandidate{Agent: agent, Priority: root.Priority}
			}
		}
	}
	out := make([]Agent, 0, len(seen))
	for _, candidate := range seen {
		out = append(out, candidate.Agent)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out, nil
}

type agentRoot struct {
	Path     string
	Source   string
	Plugin   string
	Priority int
}

type agentCandidate struct {
	Agent    Agent
	Priority int
}

func Load(cwd, name string) (Agent, bool, error) {
	name = strings.TrimSpace(name)
	list, err := List(cwd)
	if err != nil {
		return Agent{}, false, err
	}
	for _, agent := range list {
		if agent.Name == name {
			return agent, true, nil
		}
	}
	return Agent{}, false, nil
}

func Doctor(cwd string) (DoctorReport, error) {
	list, err := List(cwd)
	if err != nil {
		return DoctorReport{}, err
	}
	report := DoctorReport{Status: "ok", Agents: len(list)}
	for _, agent := range list {
		report.Diagnostics = append(report.Diagnostics, Validate(agent)...)
	}
	for _, diagnostic := range report.Diagnostics {
		switch diagnostic.Level {
		case "error":
			report.Errors++
		case "warning":
			report.Warnings++
		}
	}
	if report.Errors > 0 {
		report.Status = "error"
	} else if report.Warnings > 0 {
		report.Status = "warning"
	}
	return report, nil
}

func Validate(agent Agent) []Diagnostic {
	var diagnostics []Diagnostic
	add := func(level, field, message string) {
		diagnostics = append(diagnostics, Diagnostic{Agent: agent.Name, Path: agent.Path, Level: level, Field: field, Message: message})
	}
	if strings.TrimSpace(agent.Name) == "" {
		add("error", "name", "agent name is empty")
	}
	if strings.TrimSpace(agent.Prompt) == "" {
		add("error", "prompt", "agent prompt body is empty")
	}
	for field := range agent.UnknownFields {
		add("warning", field, "unknown frontmatter field is ignored by runtime")
	}
	if agent.MaxTurns < 0 {
		add("error", "maxTurns", "maxTurns must be greater than or equal to 0")
	}
	if agent.PermissionMode != "" && !allowedString(agent.PermissionMode, "ask", "deny", "allow", "auto", "plan", "acceptEdits", "accept-edits", "bypassPermissions", "bypass") {
		add("error", "permissionMode", "permissionMode must be one of ask, deny, allow, auto, plan, acceptEdits, bypassPermissions")
	}
	if agent.Memory != "" && !allowedString(agent.Memory, "true", "false", "user", "project", "local", "none", "disabled") {
		add("warning", "memory", "memory should usually be true, false, user, project, local, none, or disabled")
	}
	if agent.Effort != "" && !validEffort(agent.Effort) {
		add("warning", "effort", "effort should be low, medium, normal, default, high, max, inherit, off, disabled, or a token budget")
	}
	for _, tool := range intersectStrings(agent.Tools, agent.DisallowedTools) {
		add("warning", "tools", "tool appears in both tools and disallowedTools: "+tool)
	}
	if agent.Background && strings.TrimSpace(agent.InitialPrompt) != "" {
		add("warning", "initialPrompt", "initialPrompt is only applied for main-thread agents; background agents start from Task/AgentCreate prompt")
	}
	return diagnostics
}

func parseFile(path string) (Agent, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Agent{}, err
	}
	text := string(data)
	agent := Agent{Path: path}
	if strings.HasPrefix(text, "---\n") {
		end := strings.Index(text[4:], "\n---")
		if end >= 0 {
			meta := text[4 : 4+end]
			text = strings.TrimSpace(text[4+end+4:])
			parsed, err := parseFrontmatter(meta)
			if err != nil {
				return Agent{}, fmt.Errorf("%s: %w", path, err)
			}
			agent = mergeAgent(agent, parsed)
		}
	}
	agent.Prompt = strings.TrimSpace(text)
	if agent.Description == "" {
		agent.Description = firstLine(agent.Prompt)
	}
	return agent, nil
}

type agentFrontmatter struct {
	Name                               string          `yaml:"name"`
	Description                        string          `yaml:"description"`
	Tools                              yamlStringList  `yaml:"tools"`
	DisallowedTools                    yamlStringList  `yaml:"disallowedTools"`
	DisallowedToolsSnake               yamlStringList  `yaml:"disallowed_tools"`
	MCPServers                         []MCPServerSpec `yaml:"mcpServers"`
	MCPServersSnake                    []MCPServerSpec `yaml:"mcp_servers"`
	CriticalSystemReminderExperimental string          `yaml:"criticalSystemReminder_EXPERIMENTAL"`
	Skills                             yamlStringList  `yaml:"skills"`
	Model                              string          `yaml:"model"`
	InitialPrompt                      string          `yaml:"initialPrompt"`
	InitialPromptSnake                 string          `yaml:"initial_prompt"`
	MaxTurns                           int             `yaml:"maxTurns"`
	MaxTurnsSnake                      int             `yaml:"max_turns"`
	Background                         bool            `yaml:"background"`
	OmitGitStatus                      bool            `yaml:"omitGitStatus"`
	OmitGitStatusSnake                 bool            `yaml:"omit_git_status"`
	Memory                             yamlString      `yaml:"memory"`
	Effort                             yamlString      `yaml:"effort"`
	PermissionMode                     string          `yaml:"permissionMode"`
	PermissionModeSnake                string          `yaml:"permission_mode"`
}

type yamlString string

func (s *yamlString) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		*s = yamlString(strings.TrimSpace(value.Value))
		return nil
	default:
		data, err := yaml.Marshal(value)
		if err != nil {
			return err
		}
		*s = yamlString(strings.TrimSpace(string(data)))
		return nil
	}
}

type yamlStringList []string

func (l *yamlStringList) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		*l = splitCSV(value.Value)
		return nil
	case yaml.SequenceNode:
		out := make([]string, 0, len(value.Content))
		for _, item := range value.Content {
			if item.Kind != yaml.ScalarNode {
				return fmt.Errorf("expected string list item, got YAML kind %d", item.Kind)
			}
			if trimmed := strings.TrimSpace(item.Value); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		*l = out
		return nil
	default:
		return fmt.Errorf("expected string or string list, got YAML kind %d", value.Kind)
	}
}

func (s *MCPServerSpec) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		s.Name = strings.TrimSpace(value.Value)
		return nil
	case yaml.MappingNode:
		if len(value.Content) == 2 && value.Content[0].Kind == yaml.ScalarNode {
			s.Name = strings.TrimSpace(value.Content[0].Value)
			var cfg map[string]any
			if err := value.Content[1].Decode(&cfg); err != nil {
				return err
			}
			s.Config = cfg
			return nil
		}
		var cfg map[string]any
		if err := value.Decode(&cfg); err != nil {
			return err
		}
		s.Config = cfg
		return nil
	default:
		return fmt.Errorf("expected string or mapping, got YAML kind %d", value.Kind)
	}
}

func parseFrontmatter(meta string) (Agent, error) {
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(meta), &node); err != nil {
		return Agent{}, err
	}
	var parsed agentFrontmatter
	if err := node.Decode(&parsed); err != nil {
		return Agent{}, err
	}
	unknown := unknownFrontmatterFields(&node)
	return Agent{
		Name:                               strings.TrimSpace(parsed.Name),
		Description:                        strings.TrimSpace(parsed.Description),
		Tools:                              []string(parsed.Tools),
		DisallowedTools:                    firstStringList([]string(parsed.DisallowedTools), []string(parsed.DisallowedToolsSnake)),
		MCPServers:                         firstMCPServerList(parsed.MCPServers, parsed.MCPServersSnake),
		CriticalSystemReminderExperimental: strings.TrimSpace(parsed.CriticalSystemReminderExperimental),
		Skills:                             []string(parsed.Skills),
		Model:                              strings.TrimSpace(parsed.Model),
		InitialPrompt:                      firstNonEmpty(parsed.InitialPrompt, parsed.InitialPromptSnake),
		MaxTurns:                           firstPositive(parsed.MaxTurns, parsed.MaxTurnsSnake),
		Background:                         parsed.Background,
		OmitGitStatus:                      parsed.OmitGitStatus || parsed.OmitGitStatusSnake,
		Memory:                             strings.TrimSpace(string(parsed.Memory)),
		Effort:                             strings.TrimSpace(string(parsed.Effort)),
		PermissionMode:                     firstNonEmpty(parsed.PermissionMode, parsed.PermissionModeSnake),
		UnknownFields:                      unknown,
	}, nil
}

func mergeAgent(base Agent, parsed Agent) Agent {
	parsed.Path = base.Path
	return parsed
}

func unknownFrontmatterFields(node *yaml.Node) map[string]string {
	allowed := map[string]bool{
		"name": true, "description": true, "tools": true, "disallowedTools": true, "disallowed_tools": true,
		"mcpServers": true, "mcp_servers": true, "criticalSystemReminder_EXPERIMENTAL": true,
		"skills": true, "model": true, "initialPrompt": true, "initial_prompt": true,
		"maxTurns": true, "max_turns": true, "background": true, "memory": true, "effort": true,
		"permissionMode": true, "permission_mode": true, "omitGitStatus": true, "omit_git_status": true,
	}
	if node == nil || len(node.Content) == 0 || node.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	out := map[string]string{}
	mapping := node.Content[0]
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		key := strings.TrimSpace(mapping.Content[i].Value)
		if key == "" || allowed[key] {
			continue
		}
		out[key] = scalarPreview(mapping.Content[i+1])
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func scalarPreview(node *yaml.Node) string {
	if node == nil {
		return ""
	}
	if node.Kind == yaml.ScalarNode {
		return node.Value
	}
	return node.ShortTag()
}

func splitCSV(value string) []string {
	value = strings.Trim(value, "[]")
	var out []string
	for _, item := range strings.Split(value, ",") {
		item = strings.Trim(strings.TrimSpace(item), `"'`)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func firstStringList(values ...[]string) []string {
	for _, value := range values {
		if len(value) > 0 {
			return value
		}
	}
	return nil
}

func firstMCPServerList(values ...[]MCPServerSpec) []MCPServerSpec {
	for _, value := range values {
		if len(value) > 0 {
			return value
		}
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func firstLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "#"))
		if line != "" {
			return line
		}
	}
	return ""
}

func allowedString(value string, allowed ...string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}

func validEffort(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if allowedString(value, "low", "medium", "normal", "default", "high", "max", "maximum", "inherit", "none", "off", "disabled") {
		return true
	}
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return value != ""
}

func intersectStrings(left, right []string) []string {
	seen := map[string]string{}
	for _, value := range left {
		key := strings.ToLower(strings.TrimSpace(value))
		if key != "" {
			seen[key] = strings.TrimSpace(value)
		}
	}
	var out []string
	for _, value := range right {
		key := strings.ToLower(strings.TrimSpace(value))
		if original, ok := seen[key]; ok {
			out = append(out, original)
		}
	}
	sort.Strings(out)
	return out
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
