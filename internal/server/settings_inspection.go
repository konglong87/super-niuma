package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/konglong87/super-niuma/internal/config"
)

const settingsIssueType = "invalid_type"

type SettingsValidationResponse struct {
	Valid  bool                   `json:"valid"`
	Issues []config.SettingsIssue `json:"issues"`
}

type SettingsFileResolved struct {
	Doc                        map[string]any    `json:"doc"`
	Masked                     []string          `json:"masked"`
	Sources                    []string          `json:"sources"`
	RouteSources               map[string]string `json:"route_sources"`
	IncludesProcessEnvironment bool              `json:"includes_process_environment"`
}

type SettingsProcessSnapshot struct {
	Available            bool           `json:"available"`
	Kind                 string         `json:"kind"`
	ActiveRunConfigKnown bool           `json:"active_run_config_known"`
	Doc                  map[string]any `json:"doc"`
}

type SettingsActivation struct {
	RequiresRestart bool   `json:"requires_restart"`
	ExistingRuns    string `json:"existing_runs"`
	NewRuns         string `json:"new_runs"`
}

type EffectiveSettingsResponse struct {
	Scope           string                  `json:"scope"`
	Workspace       string                  `json:"workspace"`
	GlobalPath      string                  `json:"global_path"`
	FileResolved    SettingsFileResolved    `json:"file_resolved"`
	ProcessSnapshot SettingsProcessSnapshot `json:"process_snapshot"`
	Activation      SettingsActivation      `json:"activation"`
}

func validateSettingsDocument(body []byte) []config.SettingsIssue {
	var settings config.Settings
	if err := json.Unmarshal(body, &settings); err != nil {
		field := "$"
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) && typeErr.Field != "" {
			field = typeErr.Field
		}
		return []config.SettingsIssue{{Field: field, Code: settingsIssueType, Message: "value does not match the settings field type"}}
	}
	return config.ValidateSettings(settings)
}

func settingsValidateGin(opts Options) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !authorize(c.Writer, c.Request, opts.AuthToken) {
			return
		}
		var doc map[string]any
		input, err := io.ReadAll(c.Request.Body)
		if err != nil || json.Unmarshal(input, &doc) != nil || doc == nil {
			http.Error(c.Writer, "request body must be a JSON settings object", http.StatusBadRequest)
			return
		}
		body, _ := json.Marshal(doc)
		issues := validateSettingsDocument(body)
		if len(issues) == 0 {
			globalSettingsMu.Lock()
			oldDoc, err := readStoredSettingsDocument()
			if err == nil {
				promotedIndex, parseErr := parsePromotedProviderIndex(c.GetHeader(settingsPromotedProviderIndexHeader))
				if parseErr != nil {
					err = parseErr
				} else {
					err = restoreSettingsDraftSecrets(doc, oldDoc, promotedIndex)
				}
			}
			globalSettingsMu.Unlock()
			if err != nil {
				issues = append(issues, config.SettingsIssue{Field: "$", Code: "masked_secret", Message: err.Error()})
			}
		}
		writeJSON(c.Writer, SettingsValidationResponse{Valid: len(issues) == 0, Issues: issues})
	}
}

func readStoredSettingsDocument() (map[string]any, error) {
	raw, _, exists, err := config.ReadGlobalSettings()
	if err != nil {
		return nil, errors.New("cannot read current global settings")
	}
	doc := map[string]any{}
	if exists {
		if err := json.Unmarshal(raw, &doc); err != nil || doc == nil {
			return nil, errors.New("current global settings must contain a valid JSON object")
		}
	}
	return doc, nil
}

func settingsEffectiveGin(opts Options) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !authorize(c.Writer, c.Request, opts.AuthToken) {
			return
		}
		loaded, routeSources := config.InspectSettings(opts.Workspace)
		body, err := json.Marshal(loaded.Settings)
		if err != nil {
			http.Error(c.Writer, "cannot inspect settings", http.StatusInternalServerError)
			return
		}
		doc := map[string]any{}
		if err := json.Unmarshal(body, &doc); err != nil {
			http.Error(c.Writer, "cannot inspect settings", http.StatusInternalServerError)
			return
		}
		masked := []string{}
		maskSecrets(doc, "", &masked)
		path, err := config.GlobalSettingsPath()
		if err != nil {
			http.Error(c.Writer, "cannot locate global settings", http.StatusInternalServerError)
			return
		}
		snapshot := SettingsProcessSnapshot{Kind: "unavailable", Doc: map[string]any{}}
		if opts.ProcessSettingsSnapshot != nil {
			snapshot.Available, snapshot.Kind = true, "startup"
			snapshot.Doc = safeSettingsSnapshot(opts.ProcessSettingsSnapshot)
		} else if opts.StatusFunc != nil {
			value, err := opts.StatusFunc(c.Request.Context())
			if err == nil {
				if raw, err := json.Marshal(value); err == nil {
					var status map[string]any
					if json.Unmarshal(raw, &status) == nil {
						snapshot.Available, snapshot.Kind, snapshot.Doc = true, "status_callback", safeSettingsSnapshot(status)
					}
				}
			}
		}
		if loaded.Sources == nil {
			loaded.Sources = []string{}
		}
		c.Header("Cache-Control", "no-store")
		writeJSON(c.Writer, EffectiveSettingsResponse{
			Scope: "server_workspace", Workspace: opts.Workspace, GlobalPath: path,
			FileResolved:    SettingsFileResolved{Doc: doc, Masked: masked, Sources: loaded.Sources, RouteSources: routeSources},
			ProcessSnapshot: snapshot,
			Activation:      SettingsActivation{RequiresRestart: true, ExistingRuns: "unchanged", NewRuns: "runtime_dependent"},
		})
	}
}

func safeSettingsSnapshot(source map[string]any) map[string]any {
	doc := map[string]any{}
	for _, key := range []string{"cwd", "workspace", "provider", "model", "providerProtocol", "baseURL", "hasAuth", "settingsSources"} {
		if value, ok := source[key]; ok {
			doc[key] = value
		}
	}
	if raw, ok := doc["baseURL"].(string); ok {
		parsed, err := url.Parse(raw)
		if err != nil {
			delete(doc, "baseURL")
		} else {
			parsed.User, parsed.RawQuery, parsed.Fragment = nil, "", ""
			doc["baseURL"] = parsed.String()
		}
	}
	return doc
}

// Sensitive header values are credentials even when a custom header name does
// not contain a recognizable secret word.
func settingsSecretValue(key, path, value string) bool {
	if isSecretKey(key) || strings.HasSuffix(strings.ToLower(path), ".headers") || strings.EqualFold(path, "headers") {
		return true
	}
	if parsed, err := url.Parse(value); err == nil {
		if parsed.IsAbs() && (parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "") {
			return true
		}
	} else if strings.Contains(value, "://") {
		// Invalid percent escaping can make URL parsing fail before it exposes
		// userinfo or query credentials. Fail closed for malformed URL values.
		return true
	}
	return false
}
