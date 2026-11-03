package outputstyle

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/konglong87/super-niuma/internal/plugins"
	"gopkg.in/yaml.v3"
)

const DefaultName = "default"

type Style struct {
	Name                   string `json:"name"`
	Description            string `json:"description,omitempty"`
	Prompt                 string `json:"prompt"`
	Source                 string `json:"source"`
	Plugin                 string `json:"plugin,omitempty"`
	KeepCodingInstructions bool   `json:"keep_coding_instructions"`
	ForceForPlugin         bool   `json:"force_for_plugin,omitempty"`
}

func Resolve(cwd, selected string) (*Style, error) {
	selected = strings.TrimSpace(selected)
	if selected == "" {
		selected = DefaultName
	}
	if strings.EqualFold(selected, DefaultName) {
		selected = DefaultName
	}
	styles, err := All(cwd)
	if err != nil {
		return nil, err
	}
	if forced := forcedPluginStyle(styles); forced != nil {
		return forced, nil
	}
	if strings.EqualFold(selected, DefaultName) {
		return nil, nil
	}
	if style, ok := styles[selected]; ok {
		copy := style
		return &copy, nil
	}
	return nil, nil
}

func All(cwd string) (map[string]Style, error) {
	styles := builtIns()
	for _, root := range roots(cwd) {
		items, err := loadDir(root.path, root.source)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			styles[item.Name] = item
		}
	}
	pluginStyles, err := pluginStyles(cwd)
	if err != nil {
		return nil, err
	}
	for _, item := range pluginStyles {
		styles[item.Name] = item
	}
	return styles, nil
}

func forcedPluginStyle(styles map[string]Style) *Style {
	var names []string
	for name, style := range styles {
		if style.Source == "plugin" && style.ForceForPlugin {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	style := styles[names[0]]
	return &style
}

type root struct {
	path   string
	source string
}

func roots(cwd string) []root {
	var out []root
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		out = append(out, root{path: filepath.Join(home, ".claude", "output-styles"), source: "userSettings"})
	}
	if project := nearestClaudeDir(cwd); project != "" {
		out = append(out, root{path: filepath.Join(project, "output-styles"), source: "projectSettings"})
	}
	return out
}

func loadDir(dir, source string) ([]Style, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	var out []Style
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			continue
		}
		style, ok, err := loadFile(filepath.Join(dir, entry.Name()), source)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, style)
		}
	}
	return out, nil
}

func pluginStyles(cwd string) ([]Style, error) {
	paths, err := plugins.OutputStylePaths(cwd)
	if err != nil {
		return nil, err
	}
	var out []Style
	for _, source := range paths {
		items, err := loadPluginPath(source.Path, source.Plugin)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	return out, nil
}

func loadPluginPath(path, plugin string) ([]Style, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if info.IsDir() {
		items, err := loadDir(path, "plugin")
		if err != nil {
			return nil, err
		}
		for i := range items {
			items[i] = namespacePluginStyle(items[i], plugin)
		}
		return items, nil
	}
	if !strings.EqualFold(filepath.Ext(path), ".md") {
		return nil, nil
	}
	style, ok, err := loadFile(path, "plugin")
	if err != nil || !ok {
		return nil, err
	}
	return []Style{namespacePluginStyle(style, plugin)}, nil
}

func namespacePluginStyle(style Style, plugin string) Style {
	plugin = strings.TrimSpace(plugin)
	style.Plugin = plugin
	style.Source = "plugin"
	if plugin != "" && !strings.Contains(style.Name, ":") {
		style.Name = plugin + ":" + style.Name
	}
	return style
}

func loadFile(path, source string) (Style, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Style{}, false, nil
		}
		return Style{}, false, err
	}
	meta, body := parseFrontmatter(string(data))
	name := metaString(meta, "name")
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	description := metaString(meta, "description")
	if description == "" {
		description = firstParagraph(body)
	}
	keepCoding := true
	if value, ok := metaBoolAny(meta, "keep-coding-instructions", "keep_coding_instructions", "keepCodingInstructions"); ok {
		keepCoding = value
	}
	forceForPlugin, _ := metaBoolAny(meta, "force-for-plugin", "force_for_plugin", "forceForPlugin")
	return Style{
		Name:                   name,
		Description:            description,
		Prompt:                 strings.TrimSpace(body),
		Source:                 source,
		KeepCodingInstructions: keepCoding,
		ForceForPlugin:         forceForPlugin,
	}, true, nil
}

func builtIns() map[string]Style {
	return map[string]Style{
		"Explanatory": {
			Name:                   "Explanatory",
			Source:                 "built-in",
			Description:            "Claude explains its implementation choices and codebase patterns",
			KeepCodingInstructions: true,
			Prompt: `You are an interactive CLI tool that helps users with software engineering tasks. In addition to software engineering tasks, you should provide educational insights about the codebase along the way.

You should be clear and educational, providing helpful explanations while remaining focused on the task. Balance educational content with task completion. When providing insights, you may exceed typical length constraints, but remain focused and relevant.

# Explanatory Style Active
## Insights
Before and after writing code, provide brief educational explanations about implementation choices. Keep insights specific to the codebase or the code you just wrote, not generic programming concepts.`,
		},
		"Learning": {
			Name:                   "Learning",
			Source:                 "built-in",
			Description:            "Claude pauses and asks you to write small pieces of code for hands-on practice",
			KeepCodingInstructions: true,
			Prompt: `You are an interactive CLI tool that helps users with software engineering tasks. In addition to software engineering tasks, help users learn more about the codebase through hands-on practice and educational insights.

You should be collaborative and encouraging. Balance task completion with learning by requesting user input for meaningful design decisions while handling routine implementation yourself.

# Learning Style Active
Ask the human to contribute small code pieces for meaningful design decisions when that would help them learn. Add a TODO(human) marker before asking, then wait for their contribution.`,
		},
	}
}

func nearestClaudeDir(cwd string) string {
	if strings.TrimSpace(cwd) == "" {
		return ""
	}
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
	case bool, int, int64, float64:
		return oneLine(strings.TrimSpace(toString(v)))
	default:
		return ""
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
		case "true", "yes", "1":
			return true, true
		case "false", "no", "0":
			return false, true
		}
	}
	return false, false
}

func metaBoolAny(meta map[string]any, keys ...string) (bool, bool) {
	for _, key := range keys {
		if value, ok := metaBool(meta, key); ok {
			return value, true
		}
	}
	return false, false
}

func firstParagraph(content string) string {
	for _, part := range strings.Split(strings.TrimSpace(content), "\n\n") {
		part = oneLine(strings.Trim(part, "# \t\r\n"))
		if part != "" {
			return part
		}
	}
	return ""
}

func oneLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func toString(value any) string {
	switch v := value.(type) {
	case bool:
		if v {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return ""
	}
}
