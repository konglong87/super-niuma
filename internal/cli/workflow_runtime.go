package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/konglong87/super-niuma/internal/agentteam/runtimecompose"
	"github.com/konglong87/super-niuma/internal/agentteam/workflow"
	"github.com/konglong87/super-niuma/internal/agentteam/workflowapp"
	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/server"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

// Workflow shares the desktop/server's repository, but not a channel worker's
// screen process, mutable options, hooks, transcript or conversation history.
func configureWorkflowRuntime(ctx context.Context, repo *mysqlstore.GormRepository, opts options, serverOpts *server.Options) (func(), error) {
	ready, err := repo.WorkflowSchemaReady(ctx)
	if err != nil {
		return nil, fmt.Errorf("probe workflow storage schema: %w", err)
	}
	if !ready {
		return func() {}, nil
	} // Legacy MySQL remains usable until an explicit migration.

	executor := runtimecompose.NewTextExecutor(func(ctx context.Context, agent workflow.Agent) (config.Config, error) {
		if err := ctx.Err(); err != nil {
			return config.Config{}, err
		}
		// The shared loader is global-only. Explicit --settings is retained for
		// isolated native acceptance and intentionally configured server instances.
		settings := config.LoadGlobalSettings()
		for _, input := range opts.settingsInputs {
			override, err := loadSettingsInput(opts.cwd, input)
			if err != nil {
				return config.Config{}, fmt.Errorf("workflow settings input is unavailable")
			}
			settings = config.MergeSettings(settings, override)
		}
		settings.Hooks = nil
		settings.MCPServers = nil
		settings.AdditionalDirectories = nil
		cfg := config.Config{}.WithRuntimeSettings(settings)
		cfg.SelectedProvider = "primary"
		if opts.providerName != "" && opts.providerName != "primary" {
			selected, err := cfg.SelectProvider(opts.providerName)
			if err != nil {
				return cfg, err
			}
			cfg = selected
		}
		if opts.modelExplicit && opts.model != "" {
			cfg.Settings.Model = opts.model
		}
		return cfg, nil
	})
	service := workflowapp.New(repo, mysqlstore.NewWorkflowStore(repo), executor, executor.Redact)
	serverOpts.WorkflowReadOnly = os.Getenv("NIUMA_WORKFLOW_READ_ONLY") == "1"
	if !serverOpts.WorkflowReadOnly {
		if err := service.Start(ctx); err != nil {
			return nil, err
		}
	}
	serverOpts.WorkflowService = service
	serverOpts.WorkflowTaskOwner = repo.WorkflowTaskOwner
	return func() {
		clean, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		_ = service.Close(clean)
	}, nil
}
