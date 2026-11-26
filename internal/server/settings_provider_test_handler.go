package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/config"
)

const settingsProviderTestTimeout = 10 * time.Second
const settingsProviderTestKind = "catalog_connection"
const settingsProviderChatKind = "chat_connection"

type SettingsProviderTestRequest struct {
	Doc      map[string]any `json:"doc"`
	Provider string         `json:"provider,omitempty"`
}

type SettingsProviderTestResponse struct {
	OK         bool   `json:"ok"`
	Kind       string `json:"kind"`
	StatusCode int    `json:"status_code,omitempty"`
	Message    string `json:"message"`
	// Reason classifies a failure: key_invalid, quota_or_rate_limit, model_not_found,
	// network, chat_failed or catalog_failed. Empty on success.
	Reason string `json:"reason,omitempty"`
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
		basePath := endpoint.Path
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
		result := SettingsProviderTestResponse{Kind: settingsProviderTestKind, Message: "模型目录连接失败，请检查网络和 Base URL。", Reason: "network"}
		if err != nil && isTimeoutError(err) {
			result.Message = "连接超时，请检查网络或 Base URL。"
		}
		if err == nil {
			defer resp.Body.Close()
			result.StatusCode = resp.StatusCode
			result.Reason = "catalog_failed"
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				var catalog struct {
					Data []json.RawMessage `json:"data"`
				}
				if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&catalog) == nil && catalog.Data != nil {
					result.OK, result.Message, result.Reason = true, "模型目录可访问", ""
				} else {
					result.Message = "供应商返回的内容不是模型目录，请检查 Base URL。"
				}
			} else if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
				result.Reason, result.Message = "key_invalid", "Key 错误或没有权限（HTTP "+strconv.Itoa(resp.StatusCode)+"），请检查后重试。"
			} else {
				result.Message = "供应商拒绝了模型目录请求；该接口可能不支持列出模型。"
			}
		}
		if result.OK {
			model := strings.TrimSpace(cfg.SelectedProviderModel)
			if model == "" {
				model = strings.TrimSpace(settings.Model)
			}
			if model == "" {
				result.Message = "模型目录可访问；未配置模型名称，未测试对话。"
			} else {
				result = probeProviderChat(ctx, client, cfg, protocol.Protocol, endpoint, basePath, model)
			}
		}
		logService(c.Request.Context(), "runtime.settings.provider_test", "server.settingsTestProviderGin", "provider catalog probe completed")
		writeJSON(c.Writer, result)
	}
}

func isTimeoutError(err error) bool {
	var netErr net.Error
	return errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout())
}

// probeProviderChat 用配置的模型发一次最小对话，验证 Key 和模型能真正对话。
// 结果里只含状态码和中文提示，绝不回显上游响应体或密钥。
func probeProviderChat(ctx context.Context, client *http.Client, cfg config.Config, protocol config.ProviderProtocol, endpoint *url.URL, basePath, model string) SettingsProviderTestResponse {
	result := SettingsProviderTestResponse{Kind: settingsProviderChatKind}
	chatURL := *endpoint
	var payload map[string]any
	switch protocol {
	case config.ProviderProtocolAnthropicMessages:
		chatURL.Path = basePath + "/messages"
		payload = map[string]any{"model": model, "max_tokens": 1, "messages": []map[string]string{{"role": "user", "content": "hi"}}}
	case config.ProviderProtocolOpenAIResponses:
		chatURL.Path = basePath + "/responses"
		payload = map[string]any{"model": model, "max_output_tokens": 16, "input": "hi", "store": false}
	default:
		chatURL.Path = basePath + "/chat/completions"
		payload = map[string]any{"model": model, "max_tokens": 1, "messages": []map[string]string{{"role": "user", "content": "hi"}}}
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, chatURL.String(), bytes.NewReader(body))
	if err != nil {
		result.Reason, result.Message = "chat_failed", "无法构造对话测试请求。"
		return result
	}
	req.Header.Set("Content-Type", "application/json")
	if protocol == config.ProviderProtocolAnthropicMessages {
		req.Header.Set("anthropic-version", "2023-06-01")
		if cfg.APIKey != "" {
			req.Header.Set("x-api-key", cfg.APIKey)
		}
		if cfg.AuthToken != "" {
			req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
		}
	} else if credential := firstNonEmpty(cfg.APIKey, cfg.AuthToken); credential != "" {
		req.Header.Set("Authorization", "Bearer "+credential)
	}
	resp, err := client.Do(req)
	if err != nil {
		result.Reason = "network"
		if isTimeoutError(err) {
			result.Message = "模型目录可访问，但对话请求超时，请稍后重试。"
		} else {
			result.Message = "模型目录可访问，但对话请求网络不通，请检查网络。"
		}
		return result
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	result.StatusCode = resp.StatusCode
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		result.OK, result.Message = true, "对话测试成功：Key 和模型可用（未验证工具和看图能力）"
		return result
	}
	message := strings.ToLower(string(raw))
	code := strconv.Itoa(resp.StatusCode)
	switch {
	case anthropic.IsQuotaExhaustedError(resp.StatusCode, message) || resp.StatusCode == http.StatusTooManyRequests:
		result.Reason, result.Message = "quota_or_rate_limit", "Key 有效，但触发限流或额度已用完（HTTP "+code+"），请稍后重试或检查额度。"
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		result.Reason, result.Message = "key_invalid", "Key 错误或没有该模型的权限（HTTP "+code+"），请检查后重试。"
	case resp.StatusCode == http.StatusNotFound || ((resp.StatusCode == http.StatusBadRequest) && containsAny(message, "model not found", "unknown model", "invalid model", "does not exist")):
		result.Reason, result.Message = "model_not_found", "模型不存在或当前 Key 无法使用该模型（HTTP "+code+"），请检查模型名称。"
	default:
		result.Reason, result.Message = "chat_failed", "模型目录可访问，但对话请求失败（HTTP "+code+"）。"
	}
	return result
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func containsAny(s string, markers ...string) bool {
	for _, m := range markers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}
