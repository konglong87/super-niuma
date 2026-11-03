package provisioning

import "context"

type CredentialStore interface {
	Put(ctx context.Context, ref CredentialRef) (CredentialRef, error)
	Get(ctx context.Context, id string) (CredentialRef, error)
	Delete(ctx context.Context, id string) error
}

type CredentialPathProvider interface{ Path(id string) string }

type ProviderCatalog interface {
	List(ctx context.Context) ([]ProviderOption, error)
	Resolve(ctx context.Context, name, model string) (ProviderOption, error)
}

// TenantInventory lets startup reconciliation cover every tenant without
// coupling provisioning to the concrete SQL repository.
type TenantInventory interface {
	ListTenantIDs(context.Context, int) ([]uint64, error)
}

// ChannelAccountStore lets provisioning create the durable provider account
// without coupling this package to a concrete SQL repository.
type ChannelAccountStore interface {
	EnsureChannelAccount(ctx context.Context, tenantID uint64, provider, accountKey, appID, credentialRef string) (uint64, error)
}

type FeishuProvisioner interface {
	Preflight(ctx context.Context, credential CredentialRef, spec WorkerSpec) ([]HealthCheck, error)
}

type CLIAvailability struct {
	ProjectCLI bool   `json:"project_cli"`
	LarkCLI    bool   `json:"lark_cli"`
	Message    string `json:"message,omitempty"`
}

type WorkerSupervisor interface {
	Start(ctx context.Context, spec WorkerSpec) (WorkerStatus, error)
	Restart(ctx context.Context, spec WorkerSpec) (WorkerStatus, error)
	Stop(ctx context.Context, spec WorkerSpec) (WorkerStatus, error)
	Status(ctx context.Context, spec WorkerSpec) (WorkerStatus, error)
	Logs(ctx context.Context, spec WorkerSpec, tail int) (string, error)
}

type WorkerInventory interface {
	List(context.Context) ([]WorkerStatus, error)
}

// WorkerSpecInventory exposes non-secret persisted worker wiring so a
// provisioning service can reconcile workers created outside the UI.
type WorkerSpecInventory interface {
	ListWorkerSpecs(context.Context) ([]WorkerSpec, error)
}

type AuditRecorder interface {
	Record(ctx context.Context, event string, session ProvisioningSession) error
}
