package provisioning

import (
	"context"
	"fmt"
	"strings"
)

const (
	discoveredCredentialRef = "feishu-default"
	feishuProvider          = "feishu"
	channelAccountIDEnv     = "NIUMA_CHANNEL_ACCOUNT_ID"
	channelAccountKeyEnv    = "NIUMA_CHANNEL_ACCOUNT_KEY"
)

type RecordInput struct {
	ID                                                        uint64
	TenantID                                                  uint64
	ProfileKey, AccountKey, CredentialRef, Supervisor, Status string
	WorkerSpec                                                WorkerSpec
	WorkerStatus                                              WorkerStatus
	Checks                                                    []HealthCheck
	LastError                                                 *ProvisionError
	UserID                                                    uint64
}
type Repository interface {
	UpsertAgentProvisioning(context.Context, RecordInput) (Record, error)
	GetAgentProvisioning(context.Context, uint64, uint64, string) (Record, error)
	ListAgentProvisionings(context.Context, uint64, int) ([]Record, error)
}
type Service struct {
	Repo        Repository
	Credentials CredentialStore
	Channels    ChannelAccountStore
	Feishu      FeishuProvisioner
	Supervisor  WorkerSupervisor
	Inventory   WorkerInventory
	Providers   ProviderCatalog
}
type CreateRequest struct {
	ProfileKey    string        `json:"profile_key"`
	AccountKey    string        `json:"account_key"`
	CredentialRef CredentialRef `json:"credential"`
	SecretValue   string        `json:"secret_value,omitempty"`
	Worker        WorkerSpec    `json:"worker"`
}

func (s *Service) Create(ctx context.Context, tenantID, userID uint64, req CreateRequest) (Record, error) {
	if tenantID == 0 || userID == 0 || strings.TrimSpace(req.ProfileKey) == "" || strings.TrimSpace(req.AccountKey) == "" {
		return Record{}, NewError(ErrInvalidInput, "profile_key, account_key and tenant context are required")
	}
	if req.Worker.Supervisor == "" {
		req.Worker.Supervisor = "screen"
	}
	if strings.TrimSpace(req.Worker.WorkerName) == "" {
		req.Worker.WorkerName = strings.TrimSpace(req.ProfileKey)
	}
	if req.SecretValue == "" {
		req.SecretValue = req.CredentialRef.SecretValue
	}
	if req.SecretValue != "" {
		if s.Credentials == nil {
			return Record{}, NewError(ErrInvalidInput, "credential store is not configured")
		}
		req.CredentialRef.SecretValue = req.SecretValue
		stored, err := s.Credentials.Put(ctx, req.CredentialRef)
		if err != nil {
			return Record{}, NewError(ErrInvalidInput, err.Error())
		}
		req.CredentialRef = stored
		if pathProvider, ok := s.Credentials.(CredentialPathProvider); ok {
			if req.Worker.Environment == nil {
				req.Worker.Environment = map[string]string{}
			}
			req.Worker.Environment["NIUMA_FEISHU_CREDENTIAL_FILE"] = pathProvider.Path(stored.ID)
		}
	}
	if req.Worker.Environment == nil {
		req.Worker.Environment = map[string]string{}
	}
	req.Worker.Environment["NIUMA_CHANNEL_TENANT_ID"] = fmt.Sprint(tenantID)
	req.Worker.Environment["NIUMA_CHANNEL_USER_ID"] = fmt.Sprint(userID)
	if s.Channels != nil {
		accountID, err := s.Channels.EnsureChannelAccount(ctx, tenantID, feishuProvider, req.AccountKey, req.CredentialRef.AppID, req.CredentialRef.ID)
		if err != nil {
			return Record{}, NewError(ErrInvalidInput, err.Error())
		}
		setChannelAccountEnvironment(req.Worker.Environment, accountID, req.AccountKey)
	}
	return s.Repo.UpsertAgentProvisioning(ctx, RecordInput{TenantID: tenantID, ProfileKey: req.ProfileKey, AccountKey: req.AccountKey, CredentialRef: req.CredentialRef.ID, Supervisor: req.Worker.Supervisor, Status: string(StatusDraft), WorkerSpec: req.Worker, UserID: userID})
}
func (s *Service) List(ctx context.Context, tenantID uint64, limit int) ([]Record, error) {
	records, err := s.Repo.ListAgentProvisionings(ctx, tenantID, limit)
	if err != nil {
		return nil, err
	}
	return s.reconcileRecords(ctx, records)
}

// Reconcile repairs durable channel account rows and worker wiring for one
// tenant. It is safe to run repeatedly during startup and settings reads.
func (s *Service) Reconcile(ctx context.Context, tenantID uint64) error {
	if tenantID == 0 {
		return NewError(ErrInvalidInput, "tenant context is required")
	}
	records, err := s.Repo.ListAgentProvisionings(ctx, tenantID, 500)
	if err != nil {
		return err
	}
	_, err = s.reconcileRecords(ctx, records)
	return err
}

// ReconcileAll is used by shared MySQL services where startup has no single
// tenant scope. Repositories that do not expose tenant enumeration simply
// rely on the per-tenant List/WorkerAction paths.
func (s *Service) ReconcileAll(ctx context.Context) error {
	inventory, ok := s.Repo.(TenantInventory)
	if !ok {
		return nil
	}
	tenantIDs, err := inventory.ListTenantIDs(ctx, 5000)
	if err != nil {
		return err
	}
	for _, tenantID := range tenantIDs {
		if err := s.Reconcile(ctx, tenantID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) reconcileRecords(ctx context.Context, records []Record) ([]Record, error) {
	out := make([]Record, 0, len(records))
	for _, record := range records {
		reconciled, err := s.reconcileRecord(ctx, record)
		if err != nil {
			return nil, err
		}
		out = append(out, reconciled)
	}
	return out, nil
}

func (s *Service) reconcileRecord(ctx context.Context, record Record) (Record, error) {
	if s.Channels == nil {
		return record, nil
	}
	appID := ""
	credentialRef := strings.TrimSpace(record.CredentialRef)
	if s.Credentials != nil && credentialRef != "" {
		credential, err := s.Credentials.Get(ctx, credentialRef)
		if err == nil {
			appID = strings.TrimSpace(credential.AppID)
		} else if ctx.Err() != nil {
			return record, ctx.Err()
		}
	}
	accountID, err := s.Channels.EnsureChannelAccount(ctx, record.TenantID, feishuProvider, record.AccountKey, appID, credentialRef)
	if err != nil {
		return record, NewError(ErrInvalidInput, err.Error())
	}
	if record.WorkerSpec.Environment == nil {
		record.WorkerSpec.Environment = map[string]string{}
	}
	beforeID := record.WorkerSpec.Environment[channelAccountIDEnv]
	beforeKey := record.WorkerSpec.Environment[channelAccountKeyEnv]
	setChannelAccountEnvironment(record.WorkerSpec.Environment, accountID, record.AccountKey)
	if beforeID != record.WorkerSpec.Environment[channelAccountIDEnv] || beforeKey != record.WorkerSpec.Environment[channelAccountKeyEnv] {
		return s.save(ctx, record, 0)
	}
	return record, nil
}

func setChannelAccountEnvironment(environment map[string]string, accountID uint64, accountKey string) {
	environment[channelAccountIDEnv] = fmt.Sprint(accountID)
	environment[channelAccountKeyEnv] = strings.TrimSpace(accountKey)
}
func (s *Service) Overview(ctx context.Context, tenantID uint64, limit int) (Overview, error) {
	records, err := s.List(ctx, tenantID, limit)
	if err != nil {
		return Overview{}, err
	}
	workers := []WorkerStatus{}
	if s.Inventory != nil {
		all, inventoryErr := s.Inventory.List(ctx)
		if inventoryErr != nil {
			return Overview{}, inventoryErr
		}
		for _, worker := range all {
			if worker.TenantID == tenantID {
				workers = append(workers, worker)
			}
		}
	}
	if inventory, ok := s.Inventory.(WorkerSpecInventory); ok {
		specs, inventoryErr := inventory.ListWorkerSpecs(ctx)
		if inventoryErr != nil {
			return Overview{}, inventoryErr
		}
		records, err = s.reconcileDiscoveredWorkers(ctx, tenantID, records, workers, specs)
		if err != nil {
			return Overview{}, err
		}
	}
	return Overview{Records: records, Workers: workers}, nil
}

func (s *Service) reconcileDiscoveredWorkers(ctx context.Context, tenantID uint64, records []Record, workers []WorkerStatus, specs []WorkerSpec) ([]Record, error) {
	known := make(map[string]bool, len(records))
	for _, record := range records {
		known[record.AccountKey] = true
	}
	for _, spec := range specs {
		if strings.TrimSpace(spec.AccountKey) == "" || known[spec.AccountKey] {
			continue
		}
		observed := observedWorkerForSpec(spec, workers)
		status := statusForWorker(observed.State)
		record, err := s.Repo.UpsertAgentProvisioning(ctx, RecordInput{
			TenantID:      tenantID,
			ProfileKey:    spec.WorkerName,
			AccountKey:    spec.AccountKey,
			CredentialRef: discoveredCredentialRef,
			Supervisor:    spec.Supervisor,
			Status:        string(status),
			WorkerSpec:    spec,
			WorkerStatus:  observed,
		})
		if err != nil {
			return nil, err
		}
		record, err = s.reconcileRecord(ctx, record)
		if err != nil {
			return nil, err
		}
		known[record.AccountKey] = true
		records = append(records, record)
	}
	return records, nil
}

func observedWorkerForSpec(spec WorkerSpec, workers []WorkerStatus) WorkerStatus {
	for _, worker := range workers {
		if worker.AccountKey == spec.AccountKey {
			return worker
		}
	}
	return WorkerStatus{AccountKey: spec.AccountKey, State: WorkerStateUnknown}
}

func statusForWorker(state WorkerState) Status {
	switch state {
	case WorkerStateRunning:
		return StatusRunning
	case WorkerStateDegraded:
		return StatusDegraded
	case WorkerStateStopped:
		return StatusStopped
	case WorkerStateFailed:
		return StatusFailed
	default:
		return StatusDraft
	}
}
func (s *Service) Get(ctx context.Context, tenantID, id uint64, key string) (Record, error) {
	record, err := s.Repo.GetAgentProvisioning(ctx, tenantID, id, key)
	if err != nil {
		return Record{}, err
	}
	return s.reconcileRecord(ctx, record)
}
func (s *Service) Preflight(ctx context.Context, tenantID, id, userID uint64) (Record, error) {
	item, err := s.Get(ctx, tenantID, id, "")
	if err != nil {
		return item, err
	}
	if s.Feishu == nil || s.Credentials == nil {
		return item, NewError(ErrPreflight, "Feishu preflight is not configured")
	}
	credential, err := s.Credentials.Get(ctx, item.CredentialRef)
	if err != nil {
		return item, err
	}
	checks, err := s.Feishu.Preflight(ctx, credential, item.WorkerSpec)
	item.Checks = checks
	if err != nil {
		item.LastError = NewError(ErrPreflight, err.Error())
		item.Status = StatusFailed
	} else {
		item.Status = StatusPreflight
	}
	return s.save(ctx, item, userID)
}
func (s *Service) WorkerAction(ctx context.Context, tenantID, id, userID uint64, action string) (Record, error) {
	item, err := s.Get(ctx, tenantID, id, "")
	if err != nil {
		return item, err
	}
	if s.Supervisor == nil {
		return item, NewError(ErrSupervisor, "worker supervisor is not configured")
	}
	var status WorkerStatus
	switch action {
	case "start":
		status, err = s.Supervisor.Start(ctx, item.WorkerSpec)
	case "restart":
		status, err = s.Supervisor.Restart(ctx, item.WorkerSpec)
	case "stop":
		status, err = s.Supervisor.Stop(ctx, item.WorkerSpec)
	case "status":
		status, err = s.Supervisor.Status(ctx, item.WorkerSpec)
	default:
		return item, NewError(ErrInvalidInput, "unknown worker action")
	}
	item.WorkerStatus = status
	if err != nil {
		item.Status = StatusFailed
		item.LastError = NewError(ErrSupervisor, err.Error())
	} else if status.State == WorkerStateRunning {
		item.Status = StatusRunning
		item.LastError = nil
	} else {
		item.Status = Status(status.State)
	}
	return s.save(ctx, item, userID)
}

func (s *Service) Logs(ctx context.Context, tenantID, id uint64, tail int) (string, error) {
	item, err := s.Get(ctx, tenantID, id, "")
	if err != nil {
		return "", err
	}
	if s.Supervisor == nil {
		return "", NewError(ErrSupervisor, "worker supervisor is not configured")
	}
	return s.Supervisor.Logs(ctx, item.WorkerSpec, tail)
}
func (s *Service) save(ctx context.Context, item Record, userID uint64) (Record, error) {
	return s.Repo.UpsertAgentProvisioning(ctx, RecordInput{ID: item.ID, TenantID: item.TenantID, ProfileKey: item.ProfileKey, AccountKey: item.AccountKey, CredentialRef: item.CredentialRef, Supervisor: item.Supervisor, Status: string(item.Status), WorkerSpec: item.WorkerSpec, WorkerStatus: item.WorkerStatus, Checks: item.Checks, LastError: item.LastError, UserID: userID})
}
