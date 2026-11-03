package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/konglong87/super-niuma/internal/config"
)

const settingsProviderTestTimeout = 10 * time.Second
const settingsProviderTestKind = "catalog_connection"

type SettingsProviderTestRequest struct {
	Doc      map[string]any `json:"doc"`
	Provider string         `json:"provider,omitempty"`
}

type SettingsProviderTestResponse struct {
	OK         bool   `json:"ok"`
	Kind       string `json:"kind"`
	StatusCode int    `json:"status_code,omitempty"`
	Message    string `json:"message"`
}

func settingsTestProviderGin(opts Options) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !authorize(c.Writer, c.Request, opts.AuthToken) {
			return
		}
		var input SettingsProviderTestRequest
		body, err := io.ReadAll(c.Request.Body)
		if err != nil || json.Unmarshal(body, &input) != nil || input.Doc == nil {
			http.Error(c.Writer, "request requires a settings doc object", http.StatusBadRequest)
			return
		}
		body, _ = json.Marshal(input.Doc)
		if issues := validateSettingsDocument(body); len(issues) > 0 {
			c.JSON(http.StatusBadRequest, SettingsValidationResponse{Issues: issues})
			return
		}
		globalSettingsMu.Lock()
		stored, err := readStoredSettingsDocument()
		if err == nil {
			err = restoreSecrets(input.Doc, stored)
		}
		globalSettingsMu.Unlock()
		if err != nil {
			http.Error(c.Writer, err.Error(), http.StatusBadRequest)
			return
		}
		body, _ = json.Marshal(input.Doc)
		var settings config.Settings
		if err := json.Unmarshal(body, &settings); err != nil {
			http.Error(c.Writer, "invalid settings", http.StatusBadRequest)
			return
		}
		// A selected disabled fallback can be probed without enabling it on disk.
		if settings.Fallback != nil {
			settings.Fallback.Enabled = nil
		}
		cfg := (config.Config{}).WithRuntimeSettings(settings)
		if input.Provider != "" {
			cfg, err = cfg.SelectProvider(input.Provider)
			if err != nil {
				http.Error(c.Writer, "selected provider is not configured", http.StatusBadRequest)
				return
			}
		}
		protocol, err := config.ResolveProviderProtocol(cfg.Provider, cfg.ProviderProtocol, cfg.Responses)
		if err != nil || (!config.ProviderKindAnthropic(cfg.Provider) && !config.ProviderKindOpenAI(cfg.Provider)) {
			http.Error(c.Writer, "resolved provider route is invalid", http.StatusBadRequest)
			return
		}
		endpoint, err := url.Parse(cfg.BaseURL)
		if err != nil || endpoint.Host == "" || (endpoint.Scheme != "https" && endpoint.Scheme != "http") || endpoint.User != nil {
			http.Error(c.Writer, "provider baseURL must be an HTTP(S) URL without user information", http.StatusBadRequest)
			return
		}
		endpoint.Path = strings.TrimRight(endpoint.Path, "/")
		if protocol.Protocol == config.ProviderProtocolAnthropicMessages && !strings.HasSuffix(endpoint.Path, "/v1") {
			endpoint.Path += "/v1"
		}
		endpoint.Path += "/models"
		ctx, cancel := context.WithTimeout(c.Request.Context(), settingsProviderTestTimeout)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			http.Error(c.Writer, "cannot construct provider probe", http.StatusBadRequest)
			return
		}
		if protocol.Protocol == config.ProviderProtocolAnthropicMessages {
			req.Header.Set("anthropic-version", "2023-06-01")
			if cfg.APIKey != "" {
				req.Header.Set("x-api-key", cfg.APIKey)
			}
			if cfg.AuthToken != "" {
				req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			}
		} else {
			credential := cfg.APIKey
			if credential == "" {
				credential = cfg.AuthToken
			}
			if credential != "" {
				req.Header.Set("Authorization", "Bearer "+credential)
			}
		}
		// Never forward credentials to a redirect target, even within the same host.
		client := &http.Client{Timeout: settingsProviderTestTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := client.Do(req)
		result := SettingsProviderTestResponse{Kind: settingsProviderTestKind, Message: "provider catalog connection failed"}
		if err == nil {
			defer resp.Body.Close()
			result.StatusCode = resp.StatusCode
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				var catalog struct {
					Data []json.RawMessage `json:"data"`
				}
				if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&catalog) == nil && catalog.Data != nil {
					result.OK, result.Message = true, "provider catalog is reachable; model inference was not tested"
				} else {
					result.Message = "provider responded without a models catalog"
				}
			} else {
				result.Message = "provider rejected the catalog request; this endpoint may not support model listing"
			}
		}
		logService(c.Request.Context(), "runtime.settings.provider_test", "server.settingsTestProviderGin", "provider catalog probe completed")
		writeJSON(c.Writer, result)
	}
}
