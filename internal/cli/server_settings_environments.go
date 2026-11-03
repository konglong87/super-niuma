package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"
	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/server"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	tenantservice "github.com/konglong87/super-niuma/internal/tenant"
)

const settingsEnvironmentsFileEnv = "NIUMA_SETTINGS_ENVIRONMENTS_FILE"
const settingsEnvironmentsFileName = "settings-environments.json"
const settingsEnvironmentMaxBytes = 1 << 20

var settingsEnvironmentID = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

type settingsEnvironmentConfig struct {
	ID               string `json:"id"`
	Label            string `json:"label"`
	DSNFile          string `json:"mysql_dsn_file"`
	TenantKey        string `json:"tenant_key"`
	UserID           string `json:"user_id"`
	AllowedTenantKey string `json:"allowed_tenant_key"`
	AllowedUserID    string `json:"allowed_user_id"`
}

type settingsEnvironmentOpener func(context.Context, string) (server.TenantService, func(), error)

func openSettingsEnvironment(ctx context.Context, dsn string) (server.TenantService, func(), error) {
	repo, err := mysqlstore.OpenGormRepository(ctx, dsn, nil)
	if err != nil {
		return nil, nil, err
	}
	return tenantservice.NewService(repo, nil), func() { _ = repo.Close() }, nil
}

func serverSettingsDatabase(dsn string) string {
	parsed, err := drivermysql.ParseDSN(mysqlstore.DriverDSN(dsn))
	if err != nil {
		return ""
	}
	return parsed.DBName
}

func configuredSettingsEnvironments(ctx context.Context) ([]server.SettingsEnvironment, func(), error) {
	path := strings.TrimSpace(os.Getenv(settingsEnvironmentsFileEnv))
	if path == "" {
		global, err := config.GlobalSettingsPath()
		if err != nil {
			return nil, func() {}, err
		}
		path = filepath.Join(filepath.Dir(global), settingsEnvironmentsFileName)
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return nil, func() {}, nil
		}
	}
	return loadSettingsEnvironments(ctx, path, openSettingsEnvironment)
}

func readSettingsEnvironmentFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > settingsEnvironmentMaxBytes {
		return nil, errors.New("settings environment file is unavailable or too large")
	}
	if info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("settings environment files must be private (0600)")
	}
	return os.ReadFile(path)
}

func loadSettingsEnvironments(ctx context.Context, path string, open settingsEnvironmentOpener) ([]server.SettingsEnvironment, func(), error) {
	var closers []func()
	cleanup := func() {
		for _, close := range closers {
			close()
		}
	}
	raw, err := readSettingsEnvironmentFile(path)
	if err != nil {
		return nil, cleanup, err
	}
	var configs []settingsEnvironmentConfig
	if json.Unmarshal(raw, &configs) != nil {
		return nil, cleanup, errors.New("settings environments must be a JSON array")
	}
	seen := map[string]bool{server.SettingsCurrentEnvironment: true}
	var environments []server.SettingsEnvironment
	for _, item := range configs {
		if !settingsEnvironmentID.MatchString(item.ID) || seen[item.ID] || strings.TrimSpace(item.Label) == "" || item.TenantKey == "" || item.UserID == "" || item.AllowedTenantKey == "" || item.AllowedUserID == "" {
			cleanup()
			return nil, func() {}, errors.New("settings environment requires a unique id, label and explicit source/target identities")
		}
		seen[item.ID] = true
		dsnPath := item.DSNFile
		if !filepath.IsAbs(dsnPath) {
			dsnPath = filepath.Join(filepath.Dir(path), dsnPath)
		}
		dsnRaw, err := readSettingsEnvironmentFile(dsnPath)
		if err != nil {
			cleanup()
			return nil, func() {}, fmt.Errorf("settings environment %s: %w", item.ID, err)
		}
		dsn := strings.TrimSpace(string(dsnRaw))
		database := serverSettingsDatabase(dsn)
		if database == "" {
			cleanup()
			return nil, func() {}, fmt.Errorf("settings environment %s: invalid database connection", item.ID)
		}
		// Bound startup latency and keep a failed secondary connection from taking
		// the current chat environment down. No migrations or worker lifecycle run.
		parsed, _ := drivermysql.ParseDSN(mysqlstore.DriverDSN(dsn))
		parsed.Timeout = 3 * time.Second
		openCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		service, close, openErr := open(openCtx, parsed.FormatDSN())
		cancel()
		if openErr != nil {
			observability.Warn(ctx, nil, "settings.environment.unavailable", "cli.loadSettingsEnvironments", "settings database unavailable", "environment", item.ID)
		} else if close != nil {
			closers = append(closers, close)
		}
		environments = append(environments, server.SettingsEnvironment{ID: item.ID, Label: item.Label, Database: database, TenantKey: item.TenantKey, UserID: item.UserID, AllowedTenantKey: item.AllowedTenantKey, AllowedUserID: item.AllowedUserID, Service: service})
	}
	return environments, cleanup, nil
}
