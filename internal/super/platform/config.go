// Package platform is the Super composition root. It neither opens nor mutates
// legacy conversation/session storage. Secrets are resolved outside its DTOs.
package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/konglong87/super-niuma/internal/super/channelbridge"
	"github.com/konglong87/super-niuma/internal/super/control"
	"github.com/konglong87/super-niuma/internal/super/router"
)

const (
	ConfigVersion  = 1
	ConfigFileName = "platform.json"
	maxConfigBytes = 256 * 1024
	maxProfiles    = 32
)

type Profile struct {
	ID      string        `json:"id"`
	Name    string        `json:"name"`
	Enabled bool          `json:"enabled"`
	Scopes  []string      `json:"scopes"`
	Grant   control.Grant `json:"grant"`
}
type Config struct {
	Accounts       []channelbridge.Account `json:"accounts,omitempty"`
	Bindings       []router.Binding        `json:"bindings,omitempty"`
	WebhookAddress string                  `json:"webhook_address,omitempty"`
	Version        int                     `json:"version"`
	Workers        int                     `json:"workers"`
	Profiles       []Profile               `json:"profiles"`
}

type configStore struct {
	path string
	mu   sync.Mutex
}

func decodeConfig(data []byte) (Config, error) {
	var c Config
	if len(data) == 0 || len(data) > maxConfigBytes {
		return c, control.ErrInvalid
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Config{}, control.ErrInvalid
	}
	if dec.Decode(new(any)) != io.EOF {
		return Config{}, control.ErrInvalid
	}
	if c.Version != ConfigVersion || c.Workers < 1 || c.Workers > control.MaxWorkers || len(c.Profiles) > maxProfiles {
		return Config{}, control.ErrInvalid
	}
	ids := map[string]bool{}
	for i := range c.Profiles {
		p := &c.Profiles[i]
		if !control.ValidID(p.ID) || ids[p.ID] || p.Name == "" || len(p.Name) > 128 || len(p.Scopes) == 0 || len(p.Scopes) > 64 {
			return Config{}, control.ErrInvalid
		}
		ids[p.ID] = true
		for _, s := range p.Scopes {
			if !control.ValidID(s) {
				return Config{}, control.ErrInvalid
			}
		}
		if p.Enabled {
			root, err := filepath.EvalSymlinks(p.Grant.Workspace)
			if err != nil {
				return Config{}, control.ErrInvalid
			}
			p.Grant.Workspace = root
		}
		p.Grant.ChannelPolicyHash = control.Digest(struct {
			Accounts []channelbridge.Account
			Bindings []router.Binding
		}{c.Accounts, c.Bindings})
		p.Grant.ProfileID = p.ID
		p.Grant.Revision = "draft"
		if err := control.ValidateGrant(p.Grant); err != nil {
			return Config{}, err
		}
		p.Grant.Revision = control.Digest(p.Grant)
	}

	if c.WebhookAddress != "" {
		host, port, err := net.SplitHostPort(c.WebhookAddress)
		n, e := strconv.Atoi(port)
		if err != nil || e != nil || n < 0 || n > 65535 || (host != "127.0.0.1" && host != "::1") {
			return Config{}, control.ErrInvalid
		}
	}
	if len(c.Accounts) > channelbridge.MaxAccounts || len(c.Bindings) > router.MaxBindings {
		return Config{}, control.ErrInvalid
	}
	accounts := map[string]channelbridge.Account{}
	for _, a := range c.Accounts {
		if !control.ValidID(a.ID) || accounts[a.ID].ID != "" || (a.Provider != control.ProviderWebhook && a.Provider != control.ProviderFeishu) {
			return Config{}, control.ErrInvalid
		}
		accounts[a.ID] = a
	}
	bound := map[string]bool{}
	for _, b := range c.Bindings {
		if !control.ValidID(b.ID) || bound[b.ID] || b.Scope != b.Owner || !control.ValidID(b.Scope) || !ids[b.ProfileID] || b.Sender == "" || b.Chat == "" {
			return Config{}, control.ErrInvalid
		}
		bound[b.ID] = true
		a, ok := accounts[b.Account]
		if !ok || a.Provider != b.Provider {
			return Config{}, control.ErrInvalid
		}
		allowed := false
		for _, p := range c.Profiles {
			if p.ID == b.ProfileID {
				for _, scope := range p.Scopes {
					if scope == b.Scope {
						allowed = true
					}
				}
			}
		}
		if !allowed {
			return Config{}, control.ErrDenied
		}
	}
	return c, nil
}
func (s *configStore) load() (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.Lstat(s.path)
	if err != nil {
		return Config{}, err
	}
	if !f.Mode().IsRegular() || f.Mode().Perm()&0077 != 0 || f.Size() > maxConfigBytes {
		return Config{}, control.ErrDenied
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return Config{}, control.ErrUnavailable
	}
	return decodeConfig(data)
}
func (s *configStore) save(text string) (Config, error) {
	c, err := decodeConfig([]byte(text))
	if err != nil {
		return c, err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return Config{}, control.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Dir(s.path)
	f, err := os.Lstat(dir)
	if err != nil || !f.IsDir() || f.Mode().Perm()&0077 != 0 {
		return Config{}, control.ErrDenied
	}
	if old, err := os.Lstat(s.path); err == nil {
		if !old.Mode().IsRegular() || old.Mode().Perm()&0077 != 0 {
			return Config{}, control.ErrDenied
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Config{}, control.ErrUnavailable
	}
	file, err := os.CreateTemp(dir, ".platform-*")
	if err != nil {
		return Config{}, control.ErrUnavailable
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	defer file.Close()
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	if err == nil {
		err = file.Close()
	}
	if err == nil {
		err = os.Rename(tmp, s.path)
	}
	if err != nil {
		return Config{}, control.ErrUnavailable
	}
	if d, err := os.Open(dir); err == nil {
		err = d.Sync()
		_ = d.Close()
		if err != nil {
			return Config{}, control.ErrUnavailable
		}
	}
	return c, nil
}
func (s *configStore) Current(ctx context.Context, p control.Principal, g control.Grant) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	c, err := s.load()
	g.SourceBindingID = ""
	g.SourceBindingHash = ""
	if err != nil {
		return control.ErrDenied
	}
	for _, profile := range c.Profiles {
		if profile.ID != g.ProfileID || !profile.Enabled || control.Digest(profile.Grant) != control.Digest(g) {
			continue
		}
		for _, scope := range profile.Scopes {
			if scope == p.Scope && p.Owner == p.Scope {
				return nil
			}
		}
	}
	return control.ErrDenied
}
