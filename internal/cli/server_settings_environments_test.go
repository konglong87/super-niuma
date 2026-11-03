package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/server"
)

func TestSettingsEnvironmentLoaderKeepsCredentialsPrivateAndClosesConnections(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "environments.json")
	if err := os.WriteFile(filepath.Join(dir, "channel.dsn"), []byte("operator:secret@tcp(127.0.0.1:3306)/channel_db?parseTime=true"), 0600); err != nil {
		t.Fatal(err)
	}
	config := `[{"id":"channel","label":"Worker","mysql_dsn_file":"channel.dsn","tenant_key":"target","user_id":"target-user","allowed_tenant_key":"source","allowed_user_id":"operator"}]`
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	closed := false
	open := func(_ context.Context, dsn string) (server.TenantService, func(), error) {
		if !strings.Contains(dsn, "operator:secret@") || !strings.Contains(dsn, "timeout=3s") {
			t.Fatal("connection or timeout lost")
		}
		return nil, func() { closed = true }, nil
	}
	items, cleanup, err := loadSettingsEnvironments(context.Background(), path, open)
	if err != nil || len(items) != 1 || items[0].Database != "channel_db" || items[0].AllowedUserID != "operator" {
		t.Fatalf("load: %+v %v", items, err)
	}
	cleanup()
	if !closed {
		t.Fatal("connection was not closed")
	}
	failed := func(context.Context, string) (server.TenantService, func(), error) {
		return nil, nil, errors.New("credential-secret-error")
	}
	items, cleanup, err = loadSettingsEnvironments(context.Background(), path, failed)
	defer cleanup()
	if err != nil || len(items) != 1 || items[0].Service != nil {
		t.Fatalf("secondary failure should keep primary available: %v", err)
	}
	if err := os.Chmod(filepath.Join(dir, "channel.dsn"), 0644); err != nil {
		t.Fatal(err)
	}
	_, _, err = loadSettingsEnvironments(context.Background(), path, open)
	if err == nil || !strings.Contains(err.Error(), "0600") {
		t.Fatal("public credential file accepted")
	}
}

func TestSettingsEnvironmentLoaderRejectsAmbiguousConfiguration(t *testing.T) {
	for _, raw := range []string{`{}`, `[{"id":"../other"}]`, `[{"id":"current","label":"override"}]`, `[{"id":"channel","label":"Worker","tenant_key":"target","user_id":"user"}]`} {
		t.Run(raw, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings-environments.json")
			if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			_, _, err := loadSettingsEnvironments(context.Background(), path, func(context.Context, string) (server.TenantService, func(), error) {
				t.Fatal("must not open a connection")
				return nil, nil, nil
			})
			if err == nil {
				t.Fatal("invalid environment accepted")
			}
		})
	}
}
