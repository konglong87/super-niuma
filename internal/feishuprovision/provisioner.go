package feishuprovision

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/provisioning"
)

const defaultBaseURL = "https://open.feishu.cn"

type HTTPProvisioner struct {
	BaseURL string
	Client  *http.Client
}

func (p HTTPProvisioner) Preflight(ctx context.Context, credential provisioning.CredentialRef, spec provisioning.WorkerSpec) ([]provisioning.HealthCheck, error) {
	base := strings.TrimRight(p.BaseURL, "/")
	if base == "" {
		base = defaultBaseURL
	}
	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	token, err := p.token(ctx, client, base, credential)
	if err != nil {
		return []provisioning.HealthCheck{{Name: "token", Status: "failed", Message: err.Error()}}, err
	}
	checks := []provisioning.HealthCheck{{Name: "token", Status: "passed", Message: "tenant access token acquired"}}
	var bot struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			AppName string `json:"app_name"`
			OpenID  string `json:"open_id"`
		} `json:"data"`
	}
	if err := p.getJSON(ctx, client, base+"/open-apis/bot/v3/info", token, &bot); err != nil || bot.Code != 0 {
		if err == nil {
			err = fmt.Errorf("bot identity: %s", bot.Msg)
		}
		checks = append(checks, provisioning.HealthCheck{Name: "bot_identity", Status: "failed", Message: err.Error()})
		return checks, err
	}
	checks = append(checks, provisioning.HealthCheck{Name: "bot_identity", Status: "passed", Message: bot.Data.AppName})
	checks = append(checks, provisioning.HealthCheck{Name: "message_receive", Status: "manual", Message: "verify event subscription in Feishu console"})
	checks = append(checks, provisioning.HealthCheck{Name: "reaction_write", Status: capabilityStatus(spec.Reactions), Message: capabilityMessage("reaction scope", spec.Reactions)})
	checks = append(checks, provisioning.HealthCheck{Name: "streaming_card", Status: capabilityStatus(spec.Streaming), Message: capabilityMessage("card update scope", spec.Streaming)})
	return checks, nil
}

func capabilityStatus(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), "on") {
		return "manual"
	}
	return "skipped"
}
func capabilityMessage(name, value string) string {
	if strings.EqualFold(strings.TrimSpace(value), "on") {
		return "verify " + name + " in Feishu console"
	}
	return "disabled"
}
func (p HTTPProvisioner) token(ctx context.Context, client *http.Client, base string, c provisioning.CredentialRef) (string, error) {
	body := strings.NewReader(`{"app_id":"` + url.QueryEscape(c.AppID) + `","app_secret":"` + url.QueryEscape(c.SecretValue) + `"}`)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/open-apis/auth/v3/tenant_access_token/internal", body)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var value struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&value); err != nil {
		return "", err
	}
	if resp.StatusCode >= 300 || value.Code != 0 || value.TenantAccessToken == "" {
		return "", fmt.Errorf("token request failed: %s", value.Msg)
	}
	return value.TenantAccessToken, nil
}
func (p HTTPProvisioner) getJSON(ctx context.Context, client *http.Client, endpoint, token string, out any) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("feishu returned status %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

var _ provisioning.FeishuProvisioner = HTTPProvisioner{}
