package platform

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/super/channelbridge"
	"github.com/konglong87/super-niuma/internal/super/control"
	"github.com/konglong87/super-niuma/internal/super/router"
)

type platformSender struct{ service *Service }

func (m *platformSender) Send(ctx context.Context, target control.Target, id, text string) (control.Receipt, error) {
	if target.Provider == control.ProviderDesktop {
		return (control.DesktopSender{}).Send(ctx, target, id, text)
	}
	m.service.channelMu.RLock()
	b := m.service.channels
	m.service.channelMu.RUnlock()
	if b == nil {
		return control.Receipt{}, &control.SendError{Code: "transport_unavailable", DefinitelyNotAccepted: true}
	}
	return b.Send(ctx, target, id, text)
}
func (s *Service) closeChannels() {
	s.channelLife.Lock()
	defer s.channelLife.Unlock()
	s.closeChannelsLocked()
}
func (s *Service) closeChannelsLocked() {
	s.channelMu.Lock()
	bridge, server := s.channels, s.channelServer
	s.channels, s.channelServer = nil, nil
	s.webhookAddress = ""
	s.channelMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if server != nil {
		if err := server.Shutdown(ctx); err != nil {
			_ = server.Close()
		}
	}
	if bridge != nil {
		_ = bridge.Close(ctx)
	}
}
func (s *Service) reloadChannels() {
	s.channelLife.Lock()
	defer s.channelLife.Unlock()
	s.closeChannelsLocked()
	c, err := s.config.load()
	if err != nil {
		return
	}
	if len(c.Accounts) == 0 {
		return
	}
	inbound := router.New(s.Engine, func() []router.Binding {
		cfg, err := s.config.load()
		if err != nil {
			return nil
		}
		return cfg.Bindings
	}, s.Grant, s.logger)
	bridge, err := channelbridge.New(channelbridge.Options{Getenv: s.channelEnv, Accounts: func() []channelbridge.Account {
		cfg, err := s.config.load()
		if err != nil {
			return nil
		}
		return cfg.Accounts
	}, Handler: inbound, Logger: s.logger, AuthorizeTarget: func(ctx context.Context, target control.Target) error {
		cfg, err := s.config.load()
		if err != nil {
			return control.ErrDenied
		}
		for _, p := range cfg.Profiles {
			if !p.Enabled {
				continue
			}
			for _, t := range p.Grant.Targets {
				if control.Digest(t) == control.Digest(target) {
					return nil
				}
			}
		}
		return control.ErrDenied
	}})
	if err != nil {
		s.setChannelCode("channel_configuration_invalid")
		return
	}
	s.mu.Lock()
	ctx, closed := s.runCtx, s.closed
	s.mu.Unlock()
	if closed || ctx == nil {
		_ = bridge.Close(context.Background())
		return
	}
	if err = bridge.Start(ctx); err != nil {
		_ = bridge.Close(context.Background())
		s.setChannelCode("channel_credentials_or_startup_failed")
		return
	}
	var server *http.Server
	address := ""
	if c.WebhookAddress != "" {
		listener, err := net.Listen("tcp", c.WebhookAddress)
		if err != nil {
			_ = bridge.Close(context.Background())
			s.setChannelCode("webhook_bind_failed")
			return
		}
		address = listener.Addr().String()
		server = &http.Server{Handler: bridge.WebhookHandler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 * 1024}
		go func() {
			if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
				s.logger.Warn("Super webhook stopped", "action", "channel.listener", "code", "listener_failed")
			}
		}()
	}
	s.channelMu.Lock()
	s.channels = bridge
	s.channelServer = server
	s.webhookAddress = address
	s.channelCode = "transports_started_not_live_acceptance"
	s.channelMu.Unlock()
}

// ChannelSecretsFileName holds channel credentials for Desktop launches from
// Finder/Dock, which do not inherit shell environment variables. It is a
// private (0600) KEY=VALUE file next to platform.json, never in config or Git.
const ChannelSecretsFileName = "channel-secrets.env"

const maxChannelSecretsBytes = 64 * 1024

// channelEnv resolves a credential reference: the process environment first,
// then the private channel secrets file. Insecure files are ignored.
func (s *Service) channelEnv(name string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	path := filepath.Join(filepath.Dir(s.config.path), ChannelSecretsFileName)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > maxChannelSecretsBytes {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "export "))
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.HasPrefix(line, "#") || strings.TrimSpace(key) != name {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		return value
	}
	return ""
}
func (s *Service) setChannelCode(code string) {
	s.channelMu.Lock()
	s.channelCode = code
	s.channelMu.Unlock()
	s.logger.Warn("Super transport unavailable", "action", "channel.configure", "code", code)
}
