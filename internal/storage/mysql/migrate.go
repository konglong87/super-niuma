package mysql

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	_ "github.com/go-sql-driver/mysql"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/mysql"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

const DefaultMigrationsPath = "migrations/mysql"

type MigrationOptions struct {
	DSN  string
	Path string
}

type MigrationVersion struct {
	Version uint `json:"version"`
	Dirty   bool `json:"dirty"`
	Applied bool `json:"applied"`
}

type Migrator struct {
	migrate *migrate.Migrate
}

func NewMigrator(opts MigrationOptions) (*Migrator, error) {
	sourceURL, err := SourceURL(opts.Path)
	if err != nil {
		return nil, err
	}
	databaseURL, err := DatabaseURL(opts.DSN)
	if err != nil {
		return nil, err
	}
	m, err := migrate.New(sourceURL, databaseURL)
	if err != nil {
		return nil, err
	}
	return &Migrator{migrate: m}, nil
}

func SourceURL(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = DefaultMigrationsPath
	}
	if strings.Contains(path, "://") {
		return path, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve migrations path: %w", err)
	}
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}).String(), nil
}

func DatabaseURL(dsn string) (string, error) {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return "", errors.New("mysql dsn is required")
	}
	if strings.Contains(dsn, "://") {
		return dsn, nil
	}
	return "mysql://" + dsn, nil
}

func (m *Migrator) Up() error {
	if m == nil || m.migrate == nil {
		return errors.New("migrator is not initialized")
	}
	if err := m.migrate.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}

func (m *Migrator) Down(steps int) error {
	if m == nil || m.migrate == nil {
		return errors.New("migrator is not initialized")
	}
	if steps < 1 {
		return errors.New("down steps must be a positive integer")
	}
	if err := m.migrate.Steps(-steps); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}

func (m *Migrator) DownAll() error {
	if m == nil || m.migrate == nil {
		return errors.New("migrator is not initialized")
	}
	if err := m.migrate.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}

func (m *Migrator) Version() (MigrationVersion, error) {
	if m == nil || m.migrate == nil {
		return MigrationVersion{}, errors.New("migrator is not initialized")
	}
	version, dirty, err := m.migrate.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return MigrationVersion{Applied: false}, nil
	}
	if err != nil {
		return MigrationVersion{}, err
	}
	return MigrationVersion{Version: version, Dirty: dirty, Applied: true}, nil
}

func (m *Migrator) Close() error {
	if m == nil || m.migrate == nil {
		return nil
	}
	sourceErr, databaseErr := m.migrate.Close()
	return errors.Join(sourceErr, databaseErr)
}
