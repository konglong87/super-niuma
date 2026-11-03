package tenantpkg

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	SchemaVersion = "tenant_skill_package_v1"
	DefaultEnvDir = "NIUMA_TENANT_SKILL_PACKAGE_DIR"

	defaultMaxFiles     = 256
	defaultMaxFileBytes = 512 * 1024
	defaultMaxTotal     = 4 * 1024 * 1024
)

var (
	ErrInvalidPackage = errors.New("tenant skill package invalid")
	runtimeExts       = map[string]bool{".md": true, ".txt": true, ".json": true, ".yaml": true, ".yml": true}
	allowedExts       = map[string]bool{
		".md": true, ".txt": true, ".json": true, ".yaml": true, ".yml": true,
		".js": true, ".ts": true, ".sh": true, ".py": true,
		".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".svg": true, ".webp": true,
	}
)

type Options struct {
	MaxFiles      int
	MaxFileBytes  int64
	MaxTotalBytes int64
}

type ImportOptions struct {
	Options
	SkillKey string
}

type Package struct {
	Manifest      Manifest
	RuntimeMD     string
	ArtifactBytes []byte
	SourceFiles   []SourceFile
	PackageSHA256 string
}

type Manifest struct {
	SchemaVersion string         `json:"schema_version"`
	SkillKey      string         `json:"skill_key"`
	PackageSHA256 string         `json:"package_sha256"`
	Files         []ManifestFile `json:"files"`
	Render        RenderInfo     `json:"render"`
}

type ManifestFile struct {
	Path        string `json:"path"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
	Runtime     bool   `json:"runtime"`
	RenderOrder int    `json:"render_order,omitempty"`
}

type RenderInfo struct {
	Entrypoint       string   `json:"entrypoint"`
	RuntimeFiles     []string `json:"runtime_files"`
	RuntimeIncludes  []string `json:"runtime_includes,omitempty"`
	RuntimeExcludes  []string `json:"runtime_excludes,omitempty"`
	ExcludedPrefixes []string `json:"excluded_prefixes,omitempty"`
}

type SourceFile struct {
	Path string
	Data []byte
	Mode fs.FileMode
}

type Store struct {
	Root string
}

type StoreResult struct {
	PackageRef  string `json:"package_ref"`
	RuntimeRef  string `json:"runtime_ref"`
	ManifestRef string `json:"manifest_ref"`
	Root        string `json:"root"`
}

type packageConfig struct {
	RuntimeIncludes []string `yaml:"runtime_includes" json:"runtime_includes"`
	RuntimeExcludes []string `yaml:"runtime_excludes" json:"runtime_excludes"`
}

func (c packageConfig) hasRuntimeRules() bool {
	return len(c.RuntimeIncludes) > 0 || len(c.RuntimeExcludes) > 0
}

func DefaultRoot() string {
	if value := strings.TrimSpace(os.Getenv(DefaultEnvDir)); value != "" {
		return value
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return filepath.Join(os.TempDir(), "niuma", "tenant-skill-packages")
	}
	return filepath.Join(home, ".niuma", "tenant-skill-packages")
}

func ImportPath(source string, opts ImportOptions) (Package, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return Package{}, fmt.Errorf("%w: source path is required", ErrInvalidPackage)
	}
	info, err := os.Lstat(source)
	if err != nil {
		return Package{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return Package{}, fmt.Errorf("%w: source symlink is not allowed", ErrInvalidPackage)
	}
	if info.IsDir() {
		return ImportDirectory(source, opts)
	}
	return ImportZip(source, opts)
}

func ImportDirectory(root string, opts ImportOptions) (Package, error) {
	var files []SourceFile
	err := filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if filePath == root {
			return nil
		}
		rel, err := filepath.Rel(root, filePath)
		if err != nil {
			return err
		}
		normalized, err := normalizePackagePath(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: symlink %s is not allowed", ErrInvalidPackage, normalized)
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(filePath)
		if err != nil {
			return err
		}
		files = append(files, SourceFile{Path: normalized, Data: data, Mode: info.Mode()})
		return nil
	})
	if err != nil {
		return Package{}, err
	}
	artifact, err := zipSourceFiles(files)
	if err != nil {
		return Package{}, err
	}
	return buildPackage(files, artifact, opts)
}

func ImportZip(zipPath string, opts ImportOptions) (Package, error) {
	data, err := os.ReadFile(zipPath)
	if err != nil {
		return Package{}, err
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return Package{}, fmt.Errorf("%w: %v", ErrInvalidPackage, err)
	}
	files := make([]SourceFile, 0, len(reader.File))
	for _, file := range reader.File {
		normalized, err := normalizePackagePath(file.Name)
		if err != nil {
			return Package{}, err
		}
		mode := file.FileInfo().Mode()
		if mode&os.ModeSymlink != 0 {
			return Package{}, fmt.Errorf("%w: symlink %s is not allowed", ErrInvalidPackage, normalized)
		}
		if file.FileInfo().IsDir() {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			return Package{}, err
		}
		content, readErr := io.ReadAll(rc)
		closeErr := rc.Close()
		if readErr != nil {
			return Package{}, readErr
		}
		if closeErr != nil {
			return Package{}, closeErr
		}
		files = append(files, SourceFile{Path: normalized, Data: content, Mode: mode})
	}
	return buildPackage(files, data, opts)
}

func (s Store) Save(tenantKey, skillKey string, pkg Package) (StoreResult, error) {
	if strings.TrimSpace(s.Root) == "" {
		s.Root = DefaultRoot()
	}
	tenantKey = safeSegment(tenantKey)
	skillKey = safeSegment(skillKey)
	if tenantKey == "" || skillKey == "" {
		return StoreResult{}, fmt.Errorf("%w: tenant_key and skill_key are required", ErrInvalidPackage)
	}
	sha := strings.TrimSpace(pkg.PackageSHA256)
	if sha == "" {
		sha = pkg.Manifest.PackageSHA256
	}
	if len(sha) != 64 {
		return StoreResult{}, fmt.Errorf("%w: package sha256 is required", ErrInvalidPackage)
	}
	root := filepath.Join(s.Root, tenantKey, skillKey, sha)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return StoreResult{}, err
	}
	manifest, err := json.MarshalIndent(pkg.Manifest, "", "  ")
	if err != nil {
		return StoreResult{}, err
	}
	writes := map[string][]byte{
		"package.skill.zip": pkg.ArtifactBytes,
		"manifest.json":     manifest,
		"runtime.md":        []byte(pkg.RuntimeMD),
	}
	for name, data := range writes {
		if len(data) == 0 {
			return StoreResult{}, fmt.Errorf("%w: %s is empty", ErrInvalidPackage, name)
		}
		if err := os.WriteFile(filepath.Join(root, name), data, 0o644); err != nil {
			return StoreResult{}, err
		}
	}
	return StoreResult{
		PackageRef:  fileURI(filepath.Join(root, "package.skill.zip")),
		RuntimeRef:  fileURI(filepath.Join(root, "runtime.md")),
		ManifestRef: fileURI(filepath.Join(root, "manifest.json")),
		Root:        root,
	}, nil
}

func buildPackage(files []SourceFile, artifact []byte, opts ImportOptions) (Package, error) {
	normalized, err := validateFiles(files, opts.Options)
	if err != nil {
		return Package{}, err
	}
	if len(normalized) == 0 {
		return Package{}, fmt.Errorf("%w: package has no files", ErrInvalidPackage)
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].Path < normalized[j].Path })
	skillKey := strings.TrimSpace(opts.SkillKey)
	if skillKey == "" {
		skillKey = inferSkillKey(normalized)
	}
	if skillKey == "" {
		return Package{}, fmt.Errorf("%w: skill_key is required", ErrInvalidPackage)
	}
	config, err := parsePackageConfig(normalized)
	if err != nil {
		return Package{}, err
	}
	runtimeMD, runtimeFiles, err := renderWithConfig(normalized, config)
	if err != nil {
		return Package{}, err
	}
	shaBytes := sha256.Sum256(artifact)
	packageSHA := hex.EncodeToString(shaBytes[:])
	runtimeSet := map[string]int{}
	for i, file := range runtimeFiles {
		runtimeSet[file] = i + 1
	}
	manifestFiles := make([]ManifestFile, 0, len(normalized))
	for _, file := range normalized {
		sum := sha256.Sum256(file.Data)
		item := ManifestFile{
			Path:   file.Path,
			SHA256: hex.EncodeToString(sum[:]),
			Size:   int64(len(file.Data)),
		}
		if order, ok := runtimeSet[file.Path]; ok {
			item.Runtime = true
			item.RenderOrder = order
		}
		manifestFiles = append(manifestFiles, item)
	}
	return Package{
		Manifest: Manifest{
			SchemaVersion: SchemaVersion,
			SkillKey:      skillKey,
			PackageSHA256: packageSHA,
			Files:         manifestFiles,
			Render: RenderInfo{
				Entrypoint:       "SKILL.md",
				RuntimeFiles:     runtimeFiles,
				RuntimeIncludes:  config.RuntimeIncludes,
				RuntimeExcludes:  config.RuntimeExcludes,
				ExcludedPrefixes: []string{"assets/", "scripts/"},
			},
		},
		RuntimeMD:     runtimeMD,
		ArtifactBytes: artifact,
		SourceFiles:   normalized,
		PackageSHA256: packageSHA,
	}, nil
}

func Render(files []SourceFile) (string, []string, error) {
	config, err := parsePackageConfig(files)
	if err != nil {
		return "", nil, err
	}
	return renderWithConfig(files, config)
}

func renderWithConfig(files []SourceFile, config packageConfig) (string, []string, error) {
	byPath := map[string]SourceFile{}
	for _, file := range files {
		byPath[file.Path] = file
	}
	if _, ok := byPath["SKILL.md"]; !ok {
		return "", nil, fmt.Errorf("%w: SKILL.md is required", ErrInvalidPackage)
	}
	runtime := make([]SourceFile, 0, len(files))
	for _, file := range files {
		if !isRuntimeFile(file.Path, config) {
			continue
		}
		runtime = append(runtime, file)
	}
	sort.Slice(runtime, func(i, j int) bool {
		if runtime[i].Path == "SKILL.md" {
			return true
		}
		if runtime[j].Path == "SKILL.md" {
			return false
		}
		return runtime[i].Path < runtime[j].Path
	})
	var b strings.Builder
	var paths []string
	for i, file := range runtime {
		content := strings.TrimSpace(string(file.Data))
		if content == "" {
			continue
		}
		if i > 0 && b.Len() > 0 {
			b.WriteString("\n\n")
		}
		if file.Path != "SKILL.md" {
			b.WriteString("\n\n---\n\n")
			b.WriteString("## Package file: ")
			b.WriteString(file.Path)
			b.WriteString("\n\n")
		}
		b.WriteString(content)
		paths = append(paths, file.Path)
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return "", nil, fmt.Errorf("%w: rendered runtime is empty", ErrInvalidPackage)
	}
	return out + "\n", paths, nil
}

func parsePackageConfig(files []SourceFile) (packageConfig, error) {
	var config packageConfig
	for _, file := range files {
		if file.Path != "package.yaml" && file.Path != "package.yml" {
			continue
		}
		if err := yaml.Unmarshal(file.Data, &config); err != nil {
			return packageConfig{}, fmt.Errorf("%w: parse %s: %v", ErrInvalidPackage, file.Path, err)
		}
		var err error
		config.RuntimeIncludes, err = normalizeRuntimePatterns(config.RuntimeIncludes)
		if err != nil {
			return packageConfig{}, fmt.Errorf("%w: %s runtime_includes: %v", ErrInvalidPackage, file.Path, err)
		}
		config.RuntimeExcludes, err = normalizeRuntimePatterns(config.RuntimeExcludes)
		if err != nil {
			return packageConfig{}, fmt.Errorf("%w: %s runtime_excludes: %v", ErrInvalidPackage, file.Path, err)
		}
		return config, nil
	}
	return config, nil
}

func normalizeRuntimePatterns(patterns []string) ([]string, error) {
	out := make([]string, 0, len(patterns))
	seen := map[string]bool{}
	for _, pattern := range patterns {
		normalized := strings.TrimSpace(strings.ReplaceAll(pattern, "\\", "/"))
		if normalized == "" || normalized == "." || strings.HasPrefix(normalized, "/") || strings.Contains(normalized, ":") {
			return nil, fmt.Errorf("unsafe pattern %q", pattern)
		}
		for _, segment := range strings.Split(normalized, "/") {
			if segment == ".." {
				return nil, fmt.Errorf("unsafe pattern %q", pattern)
			}
		}
		trailingSlash := strings.HasSuffix(normalized, "/")
		normalized = strings.TrimPrefix(path.Clean(normalized), "./")
		if trailingSlash && !strings.HasSuffix(normalized, "/") {
			normalized += "/"
		}
		if normalized == "." || seen[normalized] {
			continue
		}
		seen[normalized] = true
		out = append(out, normalized)
	}
	return out, nil
}

func validateFiles(files []SourceFile, opts Options) ([]SourceFile, error) {
	if opts.MaxFiles <= 0 {
		opts.MaxFiles = defaultMaxFiles
	}
	if opts.MaxFileBytes <= 0 {
		opts.MaxFileBytes = defaultMaxFileBytes
	}
	if opts.MaxTotalBytes <= 0 {
		opts.MaxTotalBytes = defaultMaxTotal
	}
	if len(files) > opts.MaxFiles {
		return nil, fmt.Errorf("%w: too many files: %d > %d", ErrInvalidPackage, len(files), opts.MaxFiles)
	}
	seen := map[string]bool{}
	var total int64
	out := make([]SourceFile, 0, len(files))
	for _, file := range files {
		normalized, err := normalizePackagePath(file.Path)
		if err != nil {
			return nil, err
		}
		if seen[normalized] {
			return nil, fmt.Errorf("%w: duplicate file %s", ErrInvalidPackage, normalized)
		}
		seen[normalized] = true
		if file.Mode&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%w: symlink %s is not allowed", ErrInvalidPackage, normalized)
		}
		size := int64(len(file.Data))
		if size > opts.MaxFileBytes {
			return nil, fmt.Errorf("%w: file %s too large", ErrInvalidPackage, normalized)
		}
		total += size
		if total > opts.MaxTotalBytes {
			return nil, fmt.Errorf("%w: package too large", ErrInvalidPackage)
		}
		ext := strings.ToLower(path.Ext(normalized))
		if ext == "" || !allowedExts[ext] {
			return nil, fmt.Errorf("%w: file %s extension is not allowed", ErrInvalidPackage, normalized)
		}
		out = append(out, SourceFile{Path: normalized, Data: file.Data, Mode: file.Mode})
	}
	return out, nil
}

func normalizePackagePath(raw string) (string, error) {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	if raw == "" {
		return "", fmt.Errorf("%w: empty file path", ErrInvalidPackage)
	}
	if strings.HasPrefix(raw, "/") || strings.Contains(raw, ":") {
		return "", fmt.Errorf("%w: absolute path %s is not allowed", ErrInvalidPackage, raw)
	}
	cleaned := path.Clean(raw)
	if cleaned == "." || strings.HasPrefix(cleaned, "../") || cleaned == ".." || strings.Contains(cleaned, "/../") {
		return "", fmt.Errorf("%w: unsafe path %s", ErrInvalidPackage, raw)
	}
	return cleaned, nil
}

func isRuntimeFile(filePath string, config packageConfig) bool {
	if filePath == "SKILL.md" {
		return true
	}
	if filePath == "package.yaml" || filePath == "package.yml" {
		return false
	}
	if strings.HasPrefix(filePath, "assets/") || strings.HasPrefix(filePath, "scripts/") {
		return false
	}
	if !runtimeExts[strings.ToLower(path.Ext(filePath))] {
		return false
	}
	if len(config.RuntimeIncludes) > 0 && !matchesRuntimePatterns(filePath, config.RuntimeIncludes) {
		return false
	}
	if matchesRuntimePatterns(filePath, config.RuntimeExcludes) {
		return false
	}
	return true
}

func matchesRuntimePatterns(filePath string, patterns []string) bool {
	for _, pattern := range patterns {
		if pattern == filePath {
			return true
		}
		if strings.HasSuffix(pattern, "/") && strings.HasPrefix(filePath, pattern) {
			return true
		}
		if ok, _ := path.Match(pattern, filePath); ok {
			return true
		}
		if strings.Contains(pattern, "**") && doubleStarMatch(pattern, filePath) {
			return true
		}
	}
	return false
}

func doubleStarMatch(pattern, filePath string) bool {
	expr := regexp.QuoteMeta(pattern)
	expr = strings.ReplaceAll(expr, `\*\*`, `.*`)
	expr = strings.ReplaceAll(expr, `\*`, `[^/]*`)
	expr = strings.ReplaceAll(expr, `\?`, `[^/]`)
	ok, err := regexp.MatchString("^"+expr+"$", filePath)
	return err == nil && ok
}

func inferSkillKey(files []SourceFile) string {
	for _, file := range files {
		if file.Path != "SKILL.md" {
			continue
		}
		for _, line := range strings.Split(string(file.Data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(strings.ToLower(line), "name:") {
				return strings.TrimSpace(strings.TrimPrefix(line, "name:"))
			}
			if strings.HasPrefix(line, "# ") {
				return slug(strings.TrimSpace(strings.TrimPrefix(line, "# ")))
			}
		}
	}
	return ""
}

func zipSourceFiles(files []SourceFile) ([]byte, error) {
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, file := range files {
		header := &zip.FileHeader{Name: file.Path, Method: zip.Deflate}
		header.SetMode(0o644)
		writer, err := zw.CreateHeader(header)
		if err != nil {
			return nil, err
		}
		if _, err := writer.Write(file.Data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func safeSegment(value string) string {
	return slug(value)
}

func slug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func fileURI(filePath string) string {
	abs, err := filepath.Abs(filePath)
	if err != nil {
		abs = filePath
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
	return u.String()
}
