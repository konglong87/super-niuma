package config

import (
	"encoding/json"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"
)

type FeatureContext struct {
	UserType    string
	QuerySource string
	Mode        string
	Model       string
}

// FeatureEnabled is the single precedence point for prompt/runtime feature gates:
// explicit env vars, ant-only internal overrides, local featureFlags, then GrowthBook config/remote state.
// A feature with no explicit opinion anywhere is off.
func FeatureEnabled(cwd, name string, ctx FeatureContext) bool {
	value, _ := featureResolve(cwd, name, ctx)
	return value
}

// FeatureEnabledDefault is FeatureEnabled for gates that default ON: it returns
// the explicitly configured value when any layer (env, internal override, local
// featureFlags, GrowthBook) has an opinion, otherwise def. This lets a rolled-out
// feature default on while still honoring an explicit opt-out through the same
// channels (e.g. NIUMA_FEATURE_<NAME>=0 or featureFlags: {name: false}).
func FeatureEnabledDefault(cwd, name string, ctx FeatureContext, def bool) bool {
	if value, ok := featureResolve(cwd, name, ctx); ok {
		return value
	}
	return def
}

// featureResolve returns (value, hasOpinion): value is the resolved gate and
// hasOpinion reports whether any precedence layer explicitly set it. A globally
// disabled GrowthBook contributes no opinion (env/settings still win, and
// default-on gates stay on) rather than forcing every gate off.
func featureResolve(cwd, name string, ctx FeatureContext) (bool, bool) {
	name = normalizeFeatureName(name)
	if name == "" {
		return false, false
	}
	for _, key := range featureEnvKeys(name) {
		if value, ok := parseBoolEnv(key); ok {
			return value, true
		}
	}
	if strings.EqualFold(firstNonEmpty(ctx.UserType, os.Getenv("USER_TYPE")), "ant") {
		if value, ok := lookupInternalFeatureOverride(name, ctx); ok {
			return value, true
		}
	}
	settings := LoadSettings(cwd).Settings
	if value, ok := featureMapEnabled(settings.FeatureFlags, name, ctx); ok {
		return value, true
	}
	if settings.GrowthBook == nil {
		return false, false
	}
	if settings.GrowthBook.Enabled != nil && !*settings.GrowthBook.Enabled {
		return false, false
	}
	if value, ok := featureMapEnabled(settings.GrowthBook.Overrides, name, ctx); ok {
		return value, true
	}
	if value, ok := featureMapEnabled(remoteGrowthBookFeatures(settings.GrowthBook), name, ctx); ok {
		return value, true
	}
	if value, ok := featureMapEnabled(settings.GrowthBook.Features, name, ctx); ok {
		return value, true
	}
	return false, false
}

type remoteGrowthBookEntry struct {
	features  map[string]any
	fetchedAt time.Time
	expiresAt time.Time
}

var (
	remoteGrowthBookMu    sync.Mutex
	remoteGrowthBookCache = map[string]remoteGrowthBookEntry{}
)

func remoteGrowthBookFeatures(settings *GrowthBookSettings) map[string]any {
	if settings == nil {
		return nil
	}
	url := strings.TrimSpace(firstNonEmpty(os.Getenv("NIUMA_GROWTHBOOK_URL"), os.Getenv("CLAUDE_CODE_GROWTHBOOK_URL"), settings.URL))
	if url == "" {
		return nil
	}
	headers := mergeMap[string](settings.Headers, growthBookHeadersFromEnv())
	key := remoteGrowthBookCacheKey(url, headers)
	ttl := time.Duration(settings.RefreshIntervalSeconds) * time.Second
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	now := time.Now()
	remoteGrowthBookMu.Lock()
	if cached, ok := remoteGrowthBookCache[key]; ok && now.Before(cached.expiresAt) {
		features := mergeAnyMap(nil, cached.features)
		remoteGrowthBookMu.Unlock()
		return features
	}
	remoteGrowthBookMu.Unlock()

	features, err := fetchGrowthBookFeatures(url, headers)
	remoteGrowthBookMu.Lock()
	defer remoteGrowthBookMu.Unlock()
	if err != nil {
		if cached, ok := remoteGrowthBookCache[key]; ok {
			return mergeAnyMap(nil, cached.features)
		}
		return nil
	}
	remoteGrowthBookCache[key] = remoteGrowthBookEntry{features: features, fetchedAt: now, expiresAt: now.Add(ttl)}
	return mergeAnyMap(nil, features)
}

func fetchGrowthBookFeatures(url string, headers map[string]string) (map[string]any, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for key, value := range headers {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			req.Header.Set(key, value)
		}
	}
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, http.ErrAbortHandler
	}
	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	return extractGrowthBookFeatures(raw), nil
}

func extractGrowthBookFeatures(raw map[string]any) map[string]any {
	if features, ok := raw["features"].(map[string]any); ok {
		return features
	}
	if data, ok := raw["data"].(map[string]any); ok {
		if features, ok := data["features"].(map[string]any); ok {
			return features
		}
	}
	return raw
}

func growthBookHeadersFromEnv() map[string]string {
	token := strings.TrimSpace(firstNonEmpty(os.Getenv("NIUMA_GROWTHBOOK_TOKEN"), os.Getenv("CLAUDE_CODE_GROWTHBOOK_TOKEN")))
	if token == "" {
		return nil
	}
	return map[string]string{"Authorization": "Bearer " + token}
}

func remoteGrowthBookCacheKey(url string, headers map[string]string) string {
	if len(headers) == 0 {
		return url
	}
	data, _ := json.Marshal(headers)
	return url + "\x00" + string(data)
}

func featureEnvKeys(name string) []string {
	return []string{
		name,
		"FEATURE_" + name,
		"CLAUDE_CODE_FEATURE_" + name,
		"NIUMA_FEATURE_" + name,
	}
}

func parseBoolEnv(key string) (bool, bool) {
	value, ok := os.LookupEnv(key)
	if !ok {
		return false, false
	}
	return parseFeatureBool(value)
}

func lookupInternalFeatureOverride(name string, ctx FeatureContext) (bool, bool) {
	raw := firstNonEmpty(
		os.Getenv("CLAUDE_INTERNAL_FC_OVERRIDES"),
		os.Getenv("NIUMA_FC_OVERRIDES"),
	)
	if strings.TrimSpace(raw) == "" {
		return false, false
	}
	var values map[string]any
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return false, false
	}
	return featureMapEnabled(values, name, ctx)
}

func featureMapEnabled(features map[string]any, name string, ctx FeatureContext) (bool, bool) {
	if len(features) == 0 {
		return false, false
	}
	for key, value := range features {
		if normalizeFeatureName(key) != name {
			continue
		}
		return featureValueEnabled(value, ctx)
	}
	return false, false
}

func featureValueEnabled(value any, ctx FeatureContext) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		return parseFeatureBool(typed)
	case map[string]any:
		return featureObjectEnabled(typed, ctx)
	case map[any]any:
		converted := make(map[string]any, len(typed))
		for key, value := range typed {
			converted[strings.TrimSpace(toString(key))] = value
		}
		return featureObjectEnabled(converted, ctx)
	default:
		return false, true
	}
}

func featureObjectEnabled(obj map[string]any, ctx FeatureContext) (bool, bool) {
	if !featureTargetsMatch(obj, ctx) {
		return false, true
	}
	for _, key := range []string{"enabled", "on", "value", "defaultValue"} {
		if raw, ok := lookupObjectKey(obj, key); ok {
			if value, parsed := featureValueEnabled(raw, ctx); parsed {
				return value, true
			}
		}
	}
	return false, true
}

func featureTargetsMatch(obj map[string]any, ctx FeatureContext) bool {
	checks := []struct {
		key   string
		value string
	}{
		{key: "userTypes", value: ctx.UserType},
		{key: "userType", value: ctx.UserType},
		{key: "querySources", value: ctx.QuerySource},
		{key: "querySource", value: ctx.QuerySource},
		{key: "modes", value: ctx.Mode},
		{key: "mode", value: ctx.Mode},
		{key: "models", value: ctx.Model},
		{key: "model", value: ctx.Model},
	}
	for _, check := range checks {
		raw, ok := lookupObjectKey(obj, check.key)
		if !ok {
			continue
		}
		if !matchesAnyPattern(raw, check.value) {
			return false
		}
	}
	return true
}

func matchesAnyPattern(raw any, value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	values := featureStringList(raw)
	if len(values) == 0 {
		return false
	}
	for _, candidate := range values {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if candidate == "" {
			continue
		}
		if candidate == "*" || candidate == value {
			return true
		}
		if ok, _ := path.Match(candidate, value); ok {
			return true
		}
		if strings.HasSuffix(candidate, "*") && strings.HasPrefix(value, strings.TrimSuffix(candidate, "*")) {
			return true
		}
	}
	return false
}

func featureStringList(raw any) []string {
	switch typed := raw.(type) {
	case string:
		return strings.FieldsFunc(typed, func(r rune) bool {
			return r == ',' || r == ';' || r == '\n' || r == '\t'
		})
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, value := range typed {
			out = append(out, toString(value))
		}
		return out
	default:
		if text := toString(raw); text != "" {
			return []string{text}
		}
		return nil
	}
}

func parseFeatureBool(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on", "enabled":
		return true, true
	case "0", "false", "no", "n", "off", "disabled":
		return false, true
	default:
		return false, false
	}
}

func lookupObjectKey(obj map[string]any, key string) (any, bool) {
	want := normalizeFeatureName(key)
	for candidate, value := range obj {
		if normalizeFeatureName(candidate) == want {
			return value, true
		}
	}
	return nil, false
}

func normalizeFeatureName(name string) string {
	name = strings.ToUpper(strings.TrimSpace(name))
	name = strings.NewReplacer("-", "_", ".", "_", " ", "_").Replace(name)
	for _, prefix := range []string{
		"NIUMA_FEATURE_",
		"CLAUDE_CODE_FEATURE_",
		"FEATURE_",
	} {
		name = strings.TrimPrefix(name, prefix)
	}
	return name
}

func toString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return strings.Trim(string(data), `"`)
	}
}
