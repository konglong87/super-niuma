package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/agentteam/workflow"
	"github.com/konglong87/super-niuma/internal/agentteam/workflowapp"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	tenantservice "github.com/konglong87/super-niuma/internal/tenant"
)

func workflowHTTPTest(t *testing.T, executor workflow.Executor, readOnly ...bool) (*httptest.Server, *workflowapp.Service, workflow.Scope) {
	t.Helper()
	repo, err := mysqlstore.OpenSQLiteGormRepository(context.Background(), filepath.Join(t.TempDir(), "workflow.sqlite"), nil)
	if err != nil {
		t.Fatal(err)
	}
	tenant, err := repo.UpsertTenant(context.Background(), mysqlstore.TenantInput{TenantKey: "workflow-http", Name: "workflow-http"})
	if err != nil {
		t.Fatal(err)
	}
	user, err := repo.EnsureUser(context.Background(), tenant, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetUserRole(context.Background(), tenant, user, "owner"); err != nil {
		t.Fatal(err)
	}
	store := mysqlstore.NewWorkflowStore(repo)
	service := workflowapp.New(repo, store, executor, nil)
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	isReadOnly := len(readOnly) > 0 && readOnly[0]
	handler := NewHandler(Options{WorkflowReadOnly: isReadOnly, AuthToken: "offline-auth", TenantService: tenantservice.NewService(repo, nil), AgentTaskStore: tenantservice.NewService(repo, nil), WorkflowService: service, WorkflowTaskOwner: repo.WorkflowTaskOwner}, nil)
	server := httptest.NewServer(handler)
	t.Cleanup(func() {
		server.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		_ = service.Close(ctx)
		_ = repo.Close()
	})
	return server, service, workflow.Scope{TenantID: tenant, UserID: user}
}
func workflowRequest(t *testing.T, server *httptest.Server, method, path, body, key, user string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer offline-auth")
	req.Header.Set("X-Tenant-Key", "workflow-http")
	if user == "" {
		user = "owner"
	}
	req.Header.Set("X-User-Id", user)
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, raw
}
func workflowHTTPBuiltin(t *testing.T, server *httptest.Server) workflowapp.Template {
	t.Helper()
	status, raw := workflowRequest(t, server, http.MethodPost, "/tenant/workflows/templates/research-review/create", "{}", "", "")
	if status != http.StatusOK {
		t.Fatalf("template %d %s", status, raw)
	}
	var result struct {
		Template workflowapp.Template `json:"template"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result.Template
}
func TestWorkflowHTTPActualStoreRunEventsAndIdempotency(t *testing.T) {
	server, service, scope := workflowHTTPTest(t, workflow.ExecutorFunc(func(_ context.Context, in workflow.Input, sink workflow.Sink) (workflow.Result, error) {
		sink(workflow.Event{Type: workflow.EventPhase, Text: "Generating"})
		return workflow.Result{Status: workflow.Completed, Output: "Artifact " + in.NodeID, Usage: workflow.Usage{InputTokens: 3, OutputTokens: 4}}, nil
	}))
	item := workflowHTTPBuiltin(t, server)
	body := fmt.Sprintf(`{"version":%d,"input":"Analyze these notes"}`, item.Version)
	path := "/tenant/workflows/" + item.Key + "/runs"
	status, raw := workflowRequest(t, server, http.MethodPost, path, body, "stable-http-request", "")
	if status != http.StatusAccepted {
		t.Fatalf("start %d %s", status, raw)
	}
	var result struct {
		Run      workflow.Run `json:"run"`
		Replayed bool         `json:"replayed"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	id := result.Run.ID
	until := time.Now().Add(4 * time.Second)
	var done workflow.Run
	for time.Now().Before(until) {
		done, _ = service.Run(context.Background(), scope, id)
		if workflow.Terminal(done.Status) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if done.Status != workflow.Completed || len(done.Artifacts) != 3 {
		t.Fatal(done)
	}
	status, raw = workflowRequest(t, server, http.MethodPost, path, body, "stable-http-request", "")
	if status != http.StatusOK {
		t.Fatalf("replay %d %s", status, raw)
	}
	_ = json.Unmarshal(raw, &result)
	if !result.Replayed || result.Run.ID != id {
		t.Fatal(result)
	}
	status, raw = workflowRequest(t, server, http.MethodGet, "/tenant/workflow-runs/"+id+"/events?after_sequence=1", "", "", "")
	if status != http.StatusOK {
		t.Fatalf("events %d %s", status, raw)
	}
	var page struct {
		Events []workflow.Event `json:"events"`
	}
	_ = json.Unmarshal(raw, &page)
	if len(page.Events) == 0 || page.Events[0].Sequence != 2 {
		t.Fatal(string(raw))
	}
	status, raw = workflowRequest(t, server, http.MethodGet, "/tenant/workflow-runs/"+id+"/events/stream?after_sequence=1", "", "", "")
	if status != http.StatusOK || !strings.Contains(string(raw), "event: workflow") || !strings.Contains(string(raw), "run_completed") {
		t.Fatalf("SSE %d %s", status, raw)
	}
	status, raw = workflowRequest(t, server, http.MethodPost, path, fmt.Sprintf(`{"version":%d,"input":"changed"}`, item.Version), "stable-http-request", "")
	if status != http.StatusConflict {
		t.Fatalf("conflict %d %s", status, raw)
	}
}
func TestWorkflowHTTPOwnedTaskCannotBeMutatedByLegacyRoutes(t *testing.T) {
	started := make(chan workflow.Input, 1)
	server, service, scope := workflowHTTPTest(t, workflow.ExecutorFunc(func(ctx context.Context, in workflow.Input, _ workflow.Sink) (workflow.Result, error) {
		started <- in
		<-ctx.Done()
		return workflow.Result{}, ctx.Err()
	}))
	item := workflowHTTPBuiltin(t, server)
	status, raw := workflowRequest(t, server, http.MethodPost, "/tenant/workflows/"+item.Key+"/runs", fmt.Sprintf(`{"version":%d,"input":"wait for cancellation"}`, item.Version), "cancel-http-request", "")
	if status != http.StatusAccepted {
		t.Fatalf("%d %s", status, raw)
	}
	var in workflow.Input
	select {
	case in = <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("Agent not started")
	}
	for _, request := range []struct{ method, suffix, body string }{{http.MethodPatch, "", `{"status":"completed"}`}, {http.MethodPost, "/message", `{"content":"inject"}`}, {http.MethodPost, "/cancel", `{}`}} {
		status, raw = workflowRequest(t, server, request.method, fmt.Sprintf("/tenant/agent-tasks/%d%s", in.TaskID, request.suffix), request.body, "", "")
		if status != http.StatusConflict {
			t.Fatalf("legacy %s returned %d: %s", request.suffix, status, raw)
		}
	}
	status, raw = workflowRequest(t, server, http.MethodPost, "/tenant/workflow-runs/"+in.RunID+"/cancel", "{}", "", "")
	if status != http.StatusOK {
		t.Fatalf("cancel %d %s", status, raw)
	}
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		r, _ := service.Run(context.Background(), scope, in.RunID)
		if r.Status == workflow.Cancelled {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("cancel did not settle")
}
func TestWorkflowHTTPRejectsMalformedUnknownAndForeignRequests(t *testing.T) {
	server, _, _ := workflowHTTPTest(t, workflow.ExecutorFunc(func(context.Context, workflow.Input, workflow.Sink) (workflow.Result, error) {
		return workflow.Result{Status: workflow.Completed, Output: "done"}, nil
	}))
	item := workflowHTTPBuiltin(t, server)
	for _, body := range []string{`{}`, `{"name":"bad","unknown":true}`, `{} {}`} {
		status, _ := workflowRequest(t, server, http.MethodPost, "/tenant/workflows", body, "", "")
		if status != http.StatusUnprocessableEntity {
			t.Fatal(status, body)
		}
	}
	status, raw := workflowRequest(t, server, http.MethodGet, "/tenant/workflow-runs/missing", "", "", "")
	if status != http.StatusNotFound {
		t.Fatalf("%d %s", status, raw)
	}
	status, raw = workflowRequest(t, server, http.MethodPost, "/tenant/agent-teams/"+item.Key+"/publish?version=1", "{}", "", "")
	if status != http.StatusBadRequest {
		t.Fatalf("legacy V2 publish %d %s", status, raw)
	}
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/tenant/workflows", nil)
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatal(response.StatusCode)
	}
}

func TestWorkflowReadOnlyKeepsHistoryAndRejectsMutations(t *testing.T) {
	server, _, _ := workflowHTTPTest(t, workflow.ExecutorFunc(func(context.Context, workflow.Input, workflow.Sink) (workflow.Result, error) {
		return workflow.Result{}, nil
	}), true)
	status, raw := workflowRequest(t, server, http.MethodGet, "/tenant/workflows", "", "", "")
	if status != http.StatusOK {
		t.Fatalf("read history %d %s", status, raw)
	}
	status, raw = workflowRequest(t, server, http.MethodPost, "/tenant/workflows/templates/research-review/create", "{}", "", "")
	if status != http.StatusServiceUnavailable || !strings.Contains(string(raw), "workflow_read_only") {
		t.Fatalf("mutation %d %s", status, raw)
	}
}
