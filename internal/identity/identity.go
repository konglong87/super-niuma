package identity

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/konglong87/super-niuma/internal/product"
)

const (
	defaultProductName          = product.Name
	defaultProductKey           = product.ProductKey
	defaultConfigDirName        = product.ConfigDirName
	defaultGuidanceFilename     = product.GuidanceFilename
	defaultLegacyGuidanceFile   = "CLAUDE.md"
	defaultWorkflowFallbackFile = "AGENTS.md"
)

var DefaultProductIdentity = Settings{
	ProductName:      defaultProductName,
	ProductKey:       defaultProductKey,
	ConfigDirName:    defaultConfigDirName,
	GuidanceFilename: defaultGuidanceFilename,
}

// Settings contains optional product identity overrides loaded from settings.
type Settings struct {
	ProductName      string `json:"productName,omitempty" yaml:"productName,omitempty"`
	ProductKey       string `json:"productKey,omitempty" yaml:"productKey,omitempty"`
	ConfigDirName    string `json:"configDirName,omitempty" yaml:"configDirName,omitempty"`
	GuidanceFilename string `json:"guidanceFilename,omitempty" yaml:"guidanceFilename,omitempty"`
}

// Identity centralizes niuma-owned names so new files and prompts do not
// hard-code product branding across packages.
type Identity struct {
	ProductName          string
	ProductKey           string
	ConfigDirName        string
	GuidanceFilename     string
	LegacyGuidanceFile   string
	WorkflowFallbackFile string
}

func Default() Identity {
	return FromSettings(Settings{})
}

func FromSettings(settings Settings) Identity {
	id := Identity{
		ProductName:          DefaultProductIdentity.ProductName,
		ProductKey:           DefaultProductIdentity.ProductKey,
		ConfigDirName:        DefaultProductIdentity.ConfigDirName,
		GuidanceFilename:     DefaultProductIdentity.GuidanceFilename,
		LegacyGuidanceFile:   defaultLegacyGuidanceFile,
		WorkflowFallbackFile: defaultWorkflowFallbackFile,
	}
	id.ProductName = firstNonEmpty(product.Getenv(product.ProductNameEnv), settings.ProductName, id.ProductName)
	id.ProductKey = firstNonEmpty(product.Getenv(product.ProductKeyEnv), settings.ProductKey, id.ProductKey)
	id.ConfigDirName = firstNonEmpty(product.Getenv(product.ConfigDirNameEnv), settings.ConfigDirName, id.ConfigDirName)
	id.GuidanceFilename = firstNonEmpty(product.Getenv(product.GuidanceFilenameEnv), settings.GuidanceFilename, id.GuidanceFilename)
	if err := id.Validate(); err != nil {
		return Identity{
			ProductName:          DefaultProductIdentity.ProductName,
			ProductKey:           DefaultProductIdentity.ProductKey,
			ConfigDirName:        DefaultProductIdentity.ConfigDirName,
			GuidanceFilename:     DefaultProductIdentity.GuidanceFilename,
			LegacyGuidanceFile:   defaultLegacyGuidanceFile,
			WorkflowFallbackFile: defaultWorkflowFallbackFile,
		}
	}
	return id
}

func (id Identity) GlobalConfigRoot() (string, error) {
	if root := strings.TrimSpace(product.Getenv(product.ConfigDirEnv)); root != "" {
		return filepath.Clean(root), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, id.ConfigDirName), nil
}

// LegacyOwnedGlobalConfigRoot returns the previous product-owned state root.
// It is read-only compatibility input; new state must be written under
// GlobalConfigRoot.
func LegacyOwnedGlobalConfigRoot() (string, error) {
	if root := strings.TrimSpace(product.Getenv(product.ConfigDirEnv)); root != "" {
		return "", nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, product.LegacyConfigDirName), nil
}

func (id Identity) GlobalStatePath(rel ...string) (string, error) {
	root, err := id.GlobalConfigRoot()
	if err != nil {
		return "", err
	}
	parts := append([]string{root}, rel...)
	return filepath.Join(parts...), nil
}

func (id Identity) ProjectConfigRoot(cwd string) string {
	dir, err := filepath.Abs(cwd)
	if err != nil {
		dir = cwd
	}
	if root := FindNearestConfigDir(dir, id.ConfigDirName); root != "" {
		return root
	}
	return filepath.Join(dir, id.ConfigDirName)
}

func (id Identity) ProjectStatePath(cwd string, rel ...string) string {
	parts := append([]string{id.ProjectConfigRoot(cwd)}, rel...)
	return filepath.Join(parts...)
}

func FindNearestConfigDir(start, name string) string {
	dir := start
	for {
		candidate := filepath.Join(dir, name)
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

func LegacyGlobalConfigRoot() (string, error) {
	if root := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); root != "" {
		return filepath.Clean(root), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude"), nil
}

func (id Identity) Validate() error {
	if strings.TrimSpace(id.ProductName) == "" {
		return fmt.Errorf("product name is empty")
	}
	if strings.TrimSpace(id.ProductKey) == "" {
		return fmt.Errorf("product key is empty")
	}
	if err := validateRelativeName(id.ConfigDirName, "config dir name"); err != nil {
		return err
	}
	if err := validateRelativeName(id.GuidanceFilename, "guidance filename"); err != nil {
		return err
	}
	if strings.ContainsAny(id.GuidanceFilename, `/\`) {
		return fmt.Errorf("guidance filename must not contain path separators")
	}
	return nil
}

func validateRelativeName(value, label string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("%s is empty", label)
	}
	if filepath.IsAbs(value) || value == "." || value == ".." || strings.Contains(value, "..") {
		return fmt.Errorf("%s must be a relative name without path traversal", label)
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
