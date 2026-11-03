package provisioning

import (
	"context"
	"testing"
)

type memoryRepo struct{ item Record }

func (m *memoryRepo) UpsertAgentProvisioning(_ context.Context, input RecordInput) (Record, error) {
	if m.item.ID == 0 {
		m.item.ID = 1
	}
	m.item = Record{ID: m.item.ID, TenantID: input.TenantID, ProfileKey: input.ProfileKey, AccountKey: input.AccountKey, CredentialRef: input.CredentialRef, Supervisor: input.Supervisor, Status: Status(input.Status), WorkerSpec: input.WorkerSpec, WorkerStatus: input.WorkerStatus, Checks: input.Checks, LastError: input.LastError}
	return m.item, nil
}
func (m *memoryRepo) GetAgentProvisioning(_ context.Context, tenantID, _ uint64, _ string) (Record, error) {
	if m.item.TenantID != tenantID {
		return Record{}, NewError(ErrNotFound, "not found")
	}
	return m.item, nil
}
func (m *memoryRepo) ListAgentProvisionings(context.Context, uint64, int) ([]Record, error) {
	if m.item.ID == 0 {
		return nil, nil
	}
	return []Record{m.item}, nil
}

type fakeCreds struct{}

func (fakeCreds) Put(context.Context, CredentialRef) (CredentialRef, error) {
	return CredentialRef{}, nil
}
func (fakeCreds) Get(context.Context, string) (CredentialRef, error) {
	return CredentialRef{ID: "cred", AppID: "app", SecretValue: "secret"}, nil
}
func (fakeCreds) Delete(context.Context, string) error { return nil }

type fakeChannels struct {
	accountID  uint64
	provider   string
	accountKey string
	appID      string
	credential string
	calls      int
}

func (f *fakeChannels) EnsureChannelAccount(_ context.Context, _ uint64, provider, accountKey, appID, credentialRef string) (uint64, error) {
	f.calls++
	f.provider, f.accountKey, f.appID, f.credential = provider, accountKey, appID, credentialRef
	if f.accountID == 0 {
		f.accountID = 41
	}
	return f.accountID, nil
}

type fakeFeishu struct{}

func (fakeFeishu) Preflight(context.Context, CredentialRef, WorkerSpec) ([]HealthCheck, error) {
	return []HealthCheck{{Name: "token", Status: "passed"}}, nil
}
func TestServicePreflightPersistsChecks(t *testing.T) {
	repo := &memoryRepo{}
	service := &Service{Repo: repo, Credentials: fakeCreds{}, Feishu: fakeFeishu{}}
	item, err := service.Create(context.Background(), 9, 2, CreateRequest{ProfileKey: "writer", AccountKey: "writer-feishu", CredentialRef: CredentialRef{ID: "cred"}})
	if err != nil {
		t.Fatal(err)
	}
	if item.WorkerSpec.WorkerName != "writer" {
		t.Fatalf("worker name = %q, want profile key", item.WorkerSpec.WorkerName)
	}
	item, err = service.Preflight(context.Background(), 9, item.ID, 2)
	if err != nil || item.Status != StatusPreflight || len(item.Checks) != 1 {
		t.Fatalf("item=%#v err=%v", item, err)
	}
}

type fakeInventory struct{}

func (fakeInventory) List(context.Context) ([]WorkerStatus, error) {
	return []WorkerStatus{{TenantID: 9, AccountID: 2, State: WorkerStateRunning, PID: 22}}, nil
}
func TestServiceOverviewIncludesUnmanagedWorkers(t *testing.T) {
	repo := &memoryRepo{}
	service := &Service{Repo: repo, Inventory: fakeInventory{}}
	result, err := service.Overview(context.Background(), 9, 100)
	if err != nil || len(result.Workers) != 1 || result.Workers[0].AccountID != 2 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

type discoveringInventory struct{}

func (discoveringInventory) List(context.Context) ([]WorkerStatus, error) {
	return []WorkerStatus{{TenantID: 9, AccountID: 2, AccountKey: "code", State: WorkerStateRunning, PID: 22}}, nil
}

func (discoveringInventory) ListWorkerSpecs(context.Context) ([]WorkerSpec, error) {
	return []WorkerSpec{
		{
			Supervisor:  "screen",
			WorkerName:  "code",
			AccountKey:  "code",
			Provider:    "provider-a",
			Model:       "model-a",
			Environment: map[string]string{"NIUMA_FEISHU_CREDENTIAL_FILE": "/home/user/feishu-credentials.json"},
		},
	}, nil
}

func TestServiceOverviewReconcilesDiscoveredWorker(t *testing.T) {
	repo := &memoryRepo{}
	service := &Service{Repo: repo, Inventory: discoveringInventory{}}
	result, err := service.Overview(context.Background(), 9, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Records) != 1 {
		t.Fatalf("records=%#v", result.Records)
	}
	record := result.Records[0]
	if record.ProfileKey != "code" || record.AccountKey != "code" || record.Status != StatusRunning {
		t.Fatalf("record=%#v", record)
	}
	if record.CredentialRef != discoveredCredentialRef {
		t.Fatalf("credential ref=%q, want %q", record.CredentialRef, discoveredCredentialRef)
	}
}

func TestServiceReconcileRepairsExistingWorkerAccount(t *testing.T) {
	repo := &memoryRepo{item: Record{
		ID:            7,
		TenantID:      9,
		ProfileKey:    "code",
		AccountKey:    "feishu-e2e",
		CredentialRef: discoveredCredentialRef,
		WorkerSpec: WorkerSpec{Environment: map[string]string{
			"NIUMA_CHANNEL_ACCOUNT_ID": "1",
		}},
	}}
	channels := &fakeChannels{}
	service := &Service{Repo: repo, Channels: channels, Credentials: fakeCreds{}}

	if err := service.Reconcile(context.Background(), 9); err != nil {
		t.Fatal(err)
	}
	if channels.calls != 1 || channels.provider != feishuProvider || channels.accountKey != "feishu-e2e" || channels.appID != "app" || channels.credential != discoveredCredentialRef {
		t.Fatalf("channel reconciliation = %+v", channels)
	}
	if got := repo.item.WorkerSpec.Environment[channelAccountIDEnv]; got != "41" {
		t.Fatalf("account id environment = %q, want 41", got)
	}
	if got := repo.item.WorkerSpec.Environment[channelAccountKeyEnv]; got != "feishu-e2e" {
		t.Fatalf("account key environment = %q, want feishu-e2e", got)
	}
}
