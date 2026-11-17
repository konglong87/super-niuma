package platform

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/super/channelbridge"
	"github.com/konglong87/super-niuma/internal/super/control"
	"github.com/konglong87/super-niuma/internal/super/execution"
	"github.com/konglong87/super-niuma/internal/super/router"
	"github.com/konglong87/super-niuma/internal/super/sqlitestore"
)

func revisions(t *testing.T, c Config) map[string]string {
	t.Helper()
	b, _ := json.Marshal(c)
	decoded, err := decodeConfig(b)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, p := range decoded.Profiles {
		out[p.ID] = p.Grant.Revision
	}
	return out
}

// Editing another profile's binding or an unused account must not revoke a
// profile's in-flight runs; editing an account its own target uses must.
func TestProfileChannelPolicyIsScopedToProfile(t *testing.T) {
	var c Config
	if err := json.Unmarshal([]byte(exampleConfig()), &c); err != nil {
		t.Fatal(err)
	}
	other := c.Profiles[0]
	other.ID, other.Name, other.Scopes = "routed", "routed", []string{"alice"}
	c.Profiles[0].Grant.Targets = append(c.Profiles[0].Grant.Targets, control.Target{ID: "hook", Provider: control.ProviderWebhook, Account: "own", Chat: "ops"})
	c.Profiles = append(c.Profiles, other)
	c.Accounts = []channelbridge.Account{{ID: "own", Provider: control.ProviderWebhook}, {ID: "chat", Provider: control.ProviderWebhook}}
	c.Bindings = []router.Binding{{ID: "b1", Provider: control.ProviderWebhook, Account: "chat", Chat: "c1", Sender: "u1", Scope: "alice", Owner: "alice", ProfileID: "routed", Enabled: true}}
	before := revisions(t, c)

	c.Bindings[0].Sender = "u2"
	c.Accounts[1].WebhookURL = "https://example.invalid/hook"
	after := revisions(t, c)
	if after["default"] != before["default"] || after["routed"] == before["routed"] {
		t.Fatalf("binding/account edit scope wrong: before=%v after=%v", before, after)
	}
	c.Accounts[0].WebhookURL = "https://example.invalid/own"
	if revisions(t, c)["default"] == after["default"] {
		t.Fatal("editing an account used by the profile's target did not change its grant")
	}
}

func TestChannelEnvFallsBackToPrivateSecretsFile(t *testing.T) {
	dir := t.TempDir()
	s := &Service{config: &configStore{path: filepath.Join(dir, ConfigFileName)}}
	path := filepath.Join(dir, ChannelSecretsFileName)
	if err := os.WriteFile(path, []byte("# feishu\nexport SUPER_TEST_APP_ID=\"cli_123\"\nSUPER_TEST_SECRET=s3cr3t\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := s.channelEnv("SUPER_TEST_APP_ID"); got != "cli_123" {
		t.Fatalf("quoted export value=%q", got)
	}
	t.Setenv("SUPER_TEST_SECRET", "from-env")
	if got := s.channelEnv("SUPER_TEST_SECRET"); got != "from-env" {
		t.Fatalf("process environment must win, got %q", got)
	}
	if got := s.channelEnv("SUPER_TEST_MISSING"); got != "" {
		t.Fatalf("missing key=%q", got)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if got := s.channelEnv("SUPER_TEST_APP_ID"); got != "" {
		t.Fatal("world-readable secrets file accepted")
	}
}

// Saving an enabled profile without a provider pins the route selected now,
// so later interactive model switches cannot retarget or revoke the grant.
func TestSavePinsCurrentModelRoute(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := sqlitestore.Open(context.Background(), filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	s := New(store, dir, nil)
	t.Cleanup(s.Close)
	s.runner = execution.New(func(context.Context) (config.Config, error) {
		return config.Config{Provider: "anthropic", SelectedProvider: "work-route", BaseURL: "https://model.invalid", APIKey: "fixture-key", Settings: config.Settings{Model: "fixture-model"}}, nil
	}, nil)
	var c Config
	if err = json.Unmarshal([]byte(exampleConfig()), &c); err != nil {
		t.Fatal(err)
	}
	c.Profiles[0].Enabled = true
	c.Profiles[0].Grant.Workspace = dir
	c.Profiles[0].Grant.Model = "fixture-model"
	b, _ := json.Marshal(c)
	if _, err = s.Save(context.Background(), string(b)); err != nil {
		t.Fatal(err)
	}
	saved, err := s.config.load()
	if err != nil {
		t.Fatal(err)
	}
	g := saved.Profiles[0].Grant
	want, _ := s.runner.RouteFingerprint(context.Background(), "work-route", "fixture-model")
	if g.Provider != "work-route" || g.RouteHash == "" || g.RouteHash != want {
		t.Fatalf("provider=%q route=%q want=%q", g.Provider, g.RouteHash, want)
	}
}
