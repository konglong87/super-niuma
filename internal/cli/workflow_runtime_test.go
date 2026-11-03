package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/agentteam/workflow"
	"github.com/konglong87/super-niuma/internal/agentteam/workflowapp"
	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/promptdump"
	"github.com/konglong87/super-niuma/internal/server"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

func TestWorkflowProductionCompositionUsesIsolatedTextSessions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(root, "global"))
	t.Setenv("NIUMA_WORKFLOW_READ_ONLY", "")
	t.Setenv(promptdump.PathEnv, "")
	const credential = "offline-workflow-credential-sentinel"
	marker := filepath.Join(root, "hook-must-not-run")
	var mu sync.Mutex
	var prompts, systems []string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected protocol endpoint %s", r.URL.Path)
		}
		var body struct {
			Model    string `json:"model"`
			Tools    []any  `json:"tools"`
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if len(body.Tools) != 0 || body.Model != "offline-workflow-model" {
			t.Errorf("unexpected model/tools: %+v", body)
		}
		var user, system string
		for _, message := range body.Messages {
			var text string
			_ = json.Unmarshal(message.Content, &text)
			if message.Role == "system" || message.Role == "developer" {
				system += text
			}
			if message.Role == "user" {
				user += text
			}
		}
		if strings.Contains(user, credential) || strings.Contains(system, credential) {
			t.Error("credential reached prompt")
		}
		mu.Lock()
		prompts = append(prompts, user)
		systems = append(systems, system)
		number := len(prompts)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		chunk := map[string]any{"id": "offline", "object": "chat.completion.chunk", "model": body.Model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": fmt.Sprintf("offline output %d %s", number, credential)}, "finish_reason": nil}}}
		raw, _ := json.Marshal(chunk)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
		stop := map[string]any{"id": "offline", "model": body.Model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 20, "total_tokens": 30}}
		raw, _ = json.Marshal(stop)
		_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", raw)
	}))
	defer provider.Close()
	settings := map[string]any{"provider": "openai", "providerProtocol": config.ProviderProtocolOpenAIChatCompletions, "baseURL": provider.URL + "/v1", "apiKey": credential, "model": "offline-workflow-model", "hooks": map[string]any{"SessionStart": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "touch " + marker}}}}}}
	raw, _ := json.Marshal(settings)
	settingsPath := filepath.Join(root, "settings.json")
	if err := os.WriteFile(settingsPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	repo, err := mysqlstore.OpenSQLiteGormRepository(context.Background(), filepath.Join(root, "local.sqlite"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	tenant, err := repo.UpsertTenant(context.Background(), mysqlstore.TenantInput{TenantKey: "offline", Name: "offline"})
	if err != nil {
		t.Fatal(err)
	}
	user, err := repo.EnsureUser(context.Background(), tenant, "offline")
	if err != nil {
		t.Fatal(err)
	}
	scope := workflow.Scope{TenantID: tenant, UserID: user}
	var opts server.Options
	cleanup, err := configureWorkflowRuntime(context.Background(), repo, options{cwd: root, settingsInputs: []string{settingsPath}}, &opts)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	item, err := opts.WorkflowService.CreateBuiltin(context.Background(), scope, workflowapp.TemplateSequential)
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := opts.WorkflowService.StartRun(context.Background(), scope, item.Key, workflowapp.StartRequest{Version: item.Version, Input: "Analyze supplied notes " + credential}, "offline-production-key")
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		run, err = opts.WorkflowService.Run(context.Background(), scope, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if workflow.Terminal(run.Status) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if run.Status != workflow.Completed || len(run.Artifacts) != 3 || run.Usage.Total() != 90 {
		t.Fatalf("actual production composition did not finish: %+v", run)
	}
	encoded, _ := json.Marshal(run)
	events, _ := opts.WorkflowService.Events(context.Background(), scope, run.ID, 0, 200)
	encodedEvents, _ := json.Marshal(events)
	if strings.Contains(string(encoded), credential) || strings.Contains(string(encodedEvents), credential) {
		t.Fatal("credential persisted in workflow state/events")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("lifecycle hook executed", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(prompts) != 3 {
		t.Fatal(prompts)
	}
	if !strings.Contains(prompts[1], "offline output 1") || !strings.Contains(prompts[2], "offline output 2") {
		t.Fatal("dependency output not passed")
	}
	for _, system := range systems {
		if strings.Contains(system, "offline output") {
			t.Fatal("upstream output polluted system instructions")
		}
	}
	if systems[0] == systems[1] || systems[1] == systems[2] {
		t.Fatal("Agent prompts were not isolated")
	}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && (strings.HasSuffix(path, ".jsonl") || strings.HasSuffix(path, ".transcript")) {
			t.Errorf("raw transcript unexpectedly persisted: %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
