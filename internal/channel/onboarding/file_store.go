package onboarding

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// FileCredentialsStore is a local-development fallback. It enforces owner-only
// permissions and is intentionally explicit so production can replace it with
// KMS/Vault without changing the onboarding flow.
type FileCredentialsStore struct{ Path string }

func (s FileCredentialsStore) SaveFeishuCredentials(_ context.Context, credentials FeishuCredentials) error {
	if s.Path == "" {
		return errors.New("credentials file path is required")
	}
	if credentials.AppID == "" || credentials.AppSecret == "" {
		return errors.New("feishu credentials are incomplete")
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	payload, err := json.Marshal(struct {
		AppID     string `json:"app_id"`
		AppSecret string `json:"app_secret"`
	}{credentials.AppID, credentials.AppSecret})
	if err != nil {
		return err
	}
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, s.Path)
}

func (s FileCredentialsStore) LoadFeishuCredentials() (FeishuCredentials, error) {
	if s.Path == "" {
		return FeishuCredentials{}, errors.New("credentials file path is required")
	}
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return FeishuCredentials{}, err
	}
	var value struct {
		AppID     string `json:"app_id"`
		AppSecret string `json:"app_secret"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return FeishuCredentials{}, err
	}
	if value.AppID == "" || value.AppSecret == "" {
		return FeishuCredentials{}, errors.New("feishu credentials file is incomplete")
	}
	return FeishuCredentials{AppID: value.AppID, AppSecret: value.AppSecret}, nil
}
