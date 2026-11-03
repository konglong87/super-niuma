package plugins

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/identity"
	"github.com/konglong87/super-niuma/internal/product"
)

type Manifest struct {
	Name         string                            `json:"name"`
	Version      string                            `json:"version,omitempty"`
	Description  string                            `json:"description,omitempty"`
	Author       string                            `json:"author,omitempty"`
	Skills       []string                          `json:"skills,omitempty"`
	Agents       []string                          `json:"agents,omitempty"`
	OutputStyles []string                          `json:"outputStyles,omitempty"`
	MCPServers   map[string]config.MCPServerConfig `json:"mcpServers,omitempty"`
	Path         string                            `json:"path,omitempty"`
}

type OutputStylePath struct {
	Plugin string
	Path   string
}

type AgentPath struct {
	Plugin string
	Path   string
}

type InstallOptions struct {
	Name    string
	Project bool
	Force   bool
}

func List(cwd string) ([]Manifest, error) {
	var roots []string
	var standaloneRoots []string
	id := config.CurrentIdentity(cwd)
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		claudeRoot := filepath.Join(home, ".claude")
		roots = append(roots, filepath.Join(claudeRoot, "plugins"))
		standaloneRoots = append(standaloneRoots, claudeRoot)
	}
	if legacyRoot, err := identity.LegacyGlobalConfigRoot(); err == nil && legacyRoot != "" {
		roots = append(roots, filepath.Join(legacyRoot, "plugins"))
		standaloneRoots = append(standaloneRoots, legacyRoot)
	}
	if legacyOwnedRoot, err := identity.LegacyOwnedGlobalConfigRoot(); err == nil && legacyOwnedRoot != "" {
		roots = append(roots, filepath.Join(legacyOwnedRoot, "plugins"))
		standaloneRoots = append(standaloneRoots, legacyOwnedRoot)
	}
	if ownedRoot, err := id.GlobalConfigRoot(); err == nil && ownedRoot != "" {
		roots = append(roots, filepath.Join(ownedRoot, "plugins"))
		standaloneRoots = append(standaloneRoots, ownedRoot)
	}
	if cwd != "" {
		if root := nearestClaudeDir(cwd); root != "" {
			roots = append(roots, filepath.Join(root, "plugins"))
		}
		roots = append(roots, filepath.Join(cwd, product.LegacyConfigDirName, "plugins"))
		roots = append(roots, filepath.Join(id.ProjectConfigRoot(cwd), "plugins"))
	}
	seen := map[string]Manifest{}
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			manifest, ok := readManifest(filepath.Join(root, entry.Name()))
			if !ok {
				continue
			}
			if manifest.Name == "" {
				manifest.Name = entry.Name()
			}
			seen[manifest.Name] = manifest
		}
	}
	for _, root := range standaloneRoots {
		manifests, err := listStandalonePluginManifests(root)
		if err != nil {
			return nil, err
		}
		for _, manifest := range manifests {
			seen[manifest.Name] = manifest
		}
	}
	out := make([]Manifest, 0, len(seen))
	for _, manifest := range seen {
		out = append(out, manifest)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func listStandalonePluginManifests(root string) ([]Manifest, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Manifest
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "plugins" {
			continue
		}
		path := filepath.Join(root, entry.Name())
		manifest, ok := readManifest(path)
		if !ok {
			continue
		}
		if manifest.Name == "" {
			manifest.Name = entry.Name()
		}
		out = append(out, manifest)
	}
	return out, nil
}

func Find(cwd, name string) (Manifest, bool, error) {
	manifests, err := List(cwd)
	if err != nil {
		return Manifest{}, false, err
	}
	for _, manifest := range manifests {
		if manifest.Name == name {
			return manifest, true, nil
		}
	}
	return Manifest{}, false, nil
}

func InstallLocal(cwd, source string, opts InstallOptions) (Manifest, error) {
	sourceAbs, err := filepath.Abs(source)
	if err != nil {
		return Manifest{}, err
	}
	info, err := os.Stat(sourceAbs)
	if err != nil {
		return Manifest{}, err
	}
	if !info.IsDir() {
		return Manifest{}, fmt.Errorf("plugin source must be a directory: %s", source)
	}
	manifest, ok := LoadManifest(sourceAbs)
	if !ok {
		return Manifest{}, fmt.Errorf("plugin manifest not found in %s", source)
	}
	name := opts.Name
	if name == "" {
		name = manifest.Name
	}
	if name == "" {
		name = filepath.Base(sourceAbs)
	}
	if err := validatePluginName(name); err != nil {
		return Manifest{}, err
	}
	root, err := installRoot(cwd, opts.Project)
	if err != nil {
		return Manifest{}, err
	}
	dest := filepath.Join(root, name)
	if _, err := os.Stat(dest); err == nil {
		if !opts.Force {
			return Manifest{}, fmt.Errorf("plugin already installed: %s", name)
		}
		if err := os.RemoveAll(dest); err != nil {
			return Manifest{}, err
		}
	} else if !os.IsNotExist(err) {
		return Manifest{}, err
	}
	if err := copyDir(sourceAbs, dest); err != nil {
		return Manifest{}, err
	}
	installed, ok := LoadManifest(dest)
	if !ok {
		return Manifest{}, errors.New("installed plugin manifest disappeared")
	}
	if installed.Name == "" {
		installed.Name = name
	}
	installed.Path = dest
	return installed, nil
}

func Uninstall(cwd, name string, project bool) error {
	if err := validatePluginName(name); err != nil {
		return err
	}
	root, err := installRoot(cwd, project)
	if err != nil {
		return err
	}
	dest := filepath.Join(root, name)
	if _, err := os.Stat(dest); os.IsNotExist(err) {
		return fmt.Errorf("plugin not installed: %s", name)
	} else if err != nil {
		return err
	}
	return os.RemoveAll(dest)
}

func SkillDirs(cwd string) ([]string, error) {
	manifests, err := List(cwd)
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, manifest := range manifests {
		if len(manifest.Skills) == 0 {
			defaultDir := filepath.Join(manifest.Path, "skills")
			if info, err := os.Stat(defaultDir); err == nil && info.IsDir() {
				dirs = append(dirs, defaultDir)
			}
			continue
		}
		for _, skillPath := range manifest.Skills {
			dirs = append(dirs, resolvePluginPath(manifest.Path, skillPath))
		}
	}
	return dirs, nil
}

func AgentDirs(cwd string) ([]string, error) {
	paths, err := AgentPaths(cwd)
	if err != nil {
		return nil, err
	}
	dirs := make([]string, 0, len(paths))
	for _, path := range paths {
		dirs = append(dirs, path.Path)
	}
	return dirs, nil
}

func AgentPaths(cwd string) ([]AgentPath, error) {
	manifests, err := List(cwd)
	if err != nil {
		return nil, err
	}
	var dirs []AgentPath
	for _, manifest := range manifests {
		if len(manifest.Agents) == 0 {
			defaultDir := filepath.Join(manifest.Path, "agents")
			if info, err := os.Stat(defaultDir); err == nil && info.IsDir() {
				dirs = append(dirs, AgentPath{Plugin: manifest.Name, Path: defaultDir})
			}
			continue
		}
		for _, agentPath := range manifest.Agents {
			dirs = append(dirs, AgentPath{Plugin: manifest.Name, Path: resolvePluginPath(manifest.Path, agentPath)})
		}
	}
	return dirs, nil
}

func OutputStylePaths(cwd string) ([]OutputStylePath, error) {
	manifests, err := List(cwd)
	if err != nil {
		return nil, err
	}
	var paths []OutputStylePath
	for _, manifest := range manifests {
		if len(manifest.OutputStyles) == 0 {
			defaultDir := filepath.Join(manifest.Path, "output-styles")
			if info, err := os.Stat(defaultDir); err == nil && info.IsDir() {
				paths = append(paths, OutputStylePath{Plugin: manifest.Name, Path: defaultDir})
			}
			continue
		}
		for _, stylePath := range manifest.OutputStyles {
			paths = append(paths, OutputStylePath{Plugin: manifest.Name, Path: resolvePluginPath(manifest.Path, stylePath)})
		}
	}
	return paths, nil
}

func MCPServers(cwd string) (map[string]config.MCPServerConfig, error) {
	manifests, err := List(cwd)
	if err != nil {
		return nil, err
	}
	servers := map[string]config.MCPServerConfig{}
	for _, manifest := range manifests {
		for name, server := range manifest.MCPServers {
			servers[name] = server
		}
	}
	if len(servers) == 0 {
		return nil, nil
	}
	return servers, nil
}

func Validate(cwd string) error {
	manifests, err := List(cwd)
	if err != nil {
		return err
	}
	for _, manifest := range manifests {
		if err := ValidateManifest(manifest); err != nil {
			return err
		}
	}
	return nil
}

func ValidateManifest(manifest Manifest) error {
	name := strings.TrimSpace(manifest.Name)
	if name == "" {
		name = filepath.Base(manifest.Path)
	}
	if err := validatePluginName(name); err != nil {
		return err
	}
	for _, skillPath := range manifest.Skills {
		dir := resolvePluginPath(manifest.Path, skillPath)
		info, err := os.Stat(dir)
		if err != nil {
			return fmt.Errorf("plugin %s skill path %s: %w", name, skillPath, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("plugin %s skill path is not a directory: %s", name, skillPath)
		}
	}
	for _, agentPath := range manifest.Agents {
		dir := resolvePluginPath(manifest.Path, agentPath)
		info, err := os.Stat(dir)
		if err != nil {
			return fmt.Errorf("plugin %s agent path %s: %w", name, agentPath, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("plugin %s agent path is not a directory: %s", name, agentPath)
		}
	}
	for _, stylePath := range manifest.OutputStyles {
		path := resolvePluginPath(manifest.Path, stylePath)
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("plugin %s output style path %s: %w", name, stylePath, err)
		}
		if info.IsDir() {
			continue
		}
		if !strings.EqualFold(filepath.Ext(path), ".md") {
			return fmt.Errorf("plugin %s output style path must be a directory or .md file: %s", name, stylePath)
		}
	}
	for serverName, server := range manifest.MCPServers {
		if strings.TrimSpace(serverName) == "" {
			return fmt.Errorf("plugin %s has an empty MCP server name", name)
		}
		if strings.TrimSpace(server.URL) == "" && strings.TrimSpace(server.Command) == "" {
			return fmt.Errorf("plugin %s MCP server %s must set url or command", name, serverName)
		}
	}
	return nil
}

func resolvePluginPath(pluginRoot, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(pluginRoot, path)
}

func LoadManifest(pluginDir string) (Manifest, bool) {
	candidates := []string{
		filepath.Join(pluginDir, ".claude-plugin", "plugin.json"),
		filepath.Join(pluginDir, ".codex-plugin", "plugin.json"),
		filepath.Join(pluginDir, "plugin.json"),
	}
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var manifest Manifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			continue
		}
		manifest.Path = pluginDir
		return manifest, true
	}
	return Manifest{}, false
}

func readManifest(pluginDir string) (Manifest, bool) {
	return LoadManifest(pluginDir)
}

func installRoot(cwd string, project bool) (string, error) {
	id := config.CurrentIdentity(cwd)
	if project {
		path := id.ProjectStatePath(cwd, "plugins")
		return path, os.MkdirAll(path, 0755)
	}
	path, err := id.GlobalStatePath("plugins")
	if err != nil {
		return "", err
	}
	return path, os.MkdirAll(path, 0755)
}

func validatePluginName(name string) error {
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name {
		return fmt.Errorf("invalid plugin name: %s", name)
	}
	return nil
}

func copyDir(source, dest string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dest, 0755)
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		target := filepath.Join(dest, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(source, dest string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
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
