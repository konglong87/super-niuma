package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/config"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

const (
	imageWorkerDSNEnv       = "NIUMA_MYSQL_DSN"
	imageWorkerTenantIDEnv  = "NIUMA_IMAGE_WORKER_TENANT_ID"
	imageWorkerNameEnv      = "NIUMA_IMAGE_WORKER_NAME"
	imageWorkerReadyFileEnv = "NIUMA_IMAGE_WORKER_READY_FILE"
)

type imageWorkerRunner interface {
	Run(context.Context) error
}

type imageWorkerReadyState struct {
	Ready                    bool      `json:"ready"`
	TenantID                 uint64    `json:"tenant_id"`
	WorkerID                 string    `json:"worker_id"`
	PID                      int       `json:"pid"`
	Provider                 string    `json:"provider"`
	Model                    string    `json:"model"`
	LegacyCandidates         int       `json:"legacy_candidates"`
	LegacyUpdated            int       `json:"legacy_updated"`
	LegacyRemaining          int       `json:"legacy_remaining"`
	OrphanBlobsScanned       int       `json:"orphan_blobs_scanned"`
	OrphanBlobsDeleted       int       `json:"orphan_blobs_deleted"`
	OrphanBlobDeleteFailures int       `json:"orphan_blob_delete_failures"`
	ObservedAt               time.Time `json:"observed_at"`
}

type imageWorkerCommandDependencies struct {
	getenv         func(string) string
	resolve        func(string, []string) (config.ResolvedImageGeneration, error)
	openRepository func(context.Context, string) (io.Closer, error)
	build          func(string, config.ResolvedImageGeneration, io.Closer, uint64, string, func(imageWorkerReadyState) error) (imageWorkerRunner, error)
	writeReady     func(string, imageWorkerReadyState) error
	removeReady    func(string) error
	workerID       func(uint64) string
}

func imageWorkerCommand(ctx context.Context, args []string, opts options, stdout io.Writer) error {
	return imageWorkerCommandWithDependencies(ctx, args, opts, stdout, defaultImageWorkerCommandDependencies())
}

func defaultImageWorkerCommandDependencies() imageWorkerCommandDependencies {
	return imageWorkerCommandDependencies{
		getenv:  os.Getenv,
		resolve: resolveImageGenerationForOptions,
		openRepository: func(ctx context.Context, dsn string) (io.Closer, error) {
			return mysqlstore.OpenGormRepository(ctx, dsn, nil)
		},
		build: func(cwd string, resolved config.ResolvedImageGeneration, handle io.Closer, tenantID uint64, workerID string, onReady func(imageWorkerReadyState) error) (imageWorkerRunner, error) {
			repo, ok := handle.(*mysqlstore.GormRepository)
			if !ok || repo == nil {
				return nil, errors.New("image worker repository has an unsupported type")
			}
			return configureImageWorkerRuntime(cwd, resolved, repo, tenantID, workerID, onReady)
		},
		writeReady:  writeImageWorkerReadyFile,
		removeReady: removeImageWorkerReadyFile,
		workerID:    defaultImageWorkerID,
	}
}

func resolveImageGenerationForOptions(cwd string, settingsInputs []string) (config.ResolvedImageGeneration, error) {
	settings := config.LoadSettings(cwd).Settings
	for _, input := range settingsInputs {
		override, err := loadSettingsInput(cwd, input)
		if err != nil {
			return config.ResolvedImageGeneration{}, err
		}
		config.AnnotatePermissionSources(&override, "cli-settings")
		settings = config.MergeSettings(settings, override)
	}
	return config.ResolveImageGenerationWithSettings(cwd, settings)
}

func imageWorkerCommandWithDependencies(ctx context.Context, args []string, opts options, stdout io.Writer, deps imageWorkerCommandDependencies) (runErr error) {
	if len(args) == 0 {
		return errors.New("image-worker requires run")
	}
	if len(args) != 1 || args[0] != "run" {
		return errors.New("usage: image-worker run")
	}
	if deps.getenv == nil || deps.resolve == nil || deps.openRepository == nil || deps.build == nil || deps.writeReady == nil || deps.removeReady == nil || deps.workerID == nil {
		return errors.New("image worker command dependencies are incomplete")
	}
	dsn := strings.TrimSpace(deps.getenv(imageWorkerDSNEnv))
	if dsn == "" {
		return fmt.Errorf("image-worker run requires %s", imageWorkerDSNEnv)
	}
	tenantID, err := parseImageWorkerTenantID(deps.getenv(imageWorkerTenantIDEnv))
	if err != nil {
		return err
	}
	resolved, err := deps.resolve(opts.cwd, opts.settingsInputs)
	if err != nil {
		return fmt.Errorf("image worker preflight failed: %w", err)
	}
	if !resolved.Enabled {
		return errors.New("image generation is disabled; set imageGeneration.enabled=true")
	}
	workerID := deps.getenv(imageWorkerNameEnv)
	if err := validateImageWorkerName(workerID); err != nil {
		return err
	}
	readyPath := strings.TrimSpace(deps.getenv(imageWorkerReadyFileEnv))
	if readyPath != "" {
		if err := deps.removeReady(readyPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove stale image worker readiness: %w", err)
		}
		defer func() {
			if err := deps.removeReady(readyPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				runErr = errors.Join(runErr, fmt.Errorf("remove image worker readiness: %w", err))
			}
		}()
	}
	repository, err := deps.openRepository(ctx, dsn)
	if err != nil {
		return fmt.Errorf("image worker MySQL preflight failed: %w", err)
	}
	defer func() {
		if err := repository.Close(); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("close image worker repository: %w", err))
		}
	}()
	onReady := func(state imageWorkerReadyState) error {
		state.Ready = true
		state.TenantID = tenantID
		state.WorkerID = workerID
		state.PID = os.Getpid()
		state.Provider = resolved.Provider
		state.Model = resolved.Model
		state.ObservedAt = time.Now().UTC()
		if readyPath != "" {
			if err := deps.writeReady(readyPath, state); err != nil {
				return fmt.Errorf("write image worker readiness: %w", err)
			}
		}
		_, err := fmt.Fprintf(stdout, "image worker ready: tenant_id=%d worker_id=%s provider=%s model=%s\n", tenantID, workerID, resolved.Provider, resolved.Model)
		return err
	}
	runner, err := deps.build(opts.cwd, resolved, repository, tenantID, workerID, onReady)
	if err != nil {
		return fmt.Errorf("image worker composition failed: %w", err)
	}
	runCtx, cancelRun := independentImageWorkerContext(ctx)
	defer cancelRun()
	if err := runner.Run(runCtx); err != nil {
		if ctx.Err() != nil && errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
	return nil
}

func validateImageWorkerName(value string) error {
	if value == "" || len(value) > 64 {
		return errors.New("NIUMA_IMAGE_WORKER_NAME must be 1 to 64 characters using only A-Za-z0-9_.@-")
	}
	for _, char := range []byte(value) {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' || char == '.' || char == '@' || char == '-' {
			continue
		}
		return errors.New("NIUMA_IMAGE_WORKER_NAME must be 1 to 64 characters using only A-Za-z0-9_.@-")
	}
	return nil
}

func parseImageWorkerTenantID(value string) (uint64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("image-worker run requires %s", imageWorkerTenantIDEnv)
	}
	tenantID, err := strconv.ParseUint(value, 10, 64)
	if err != nil || tenantID == 0 {
		return 0, fmt.Errorf("%s must be a positive integer", imageWorkerTenantIDEnv)
	}
	return tenantID, nil
}

func independentImageWorkerContext(parent context.Context) (context.Context, context.CancelFunc) {
	base := context.WithoutCancel(parent)
	ctx, cancel := context.WithCancel(base)
	stop := context.AfterFunc(parent, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

func defaultImageWorkerID(tenantID uint64) string {
	hostname, _ := os.Hostname()
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		hostname = "localhost"
	}
	workerID := fmt.Sprintf("image-%d-%s-%d", tenantID, hostname, os.Getpid())
	if len(workerID) > 255 {
		workerID = workerID[:255]
	}
	return workerID
}

func writeImageWorkerReadyFile(path string, state imageWorkerReadyState) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("image worker readiness path is required")
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, append(encoded, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(temporary, 0o600); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func removeImageWorkerReadyFile(path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	return os.Remove(path)
}
