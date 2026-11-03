package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/konglong87/super-niuma/internal/promptmode"
)

// newQuerySession is the single wiring point for the compactor on every
// in-process query path: the CLI itself, `server` (StreamQueryFunc in
// cmd_server.go builds its sessions here), tenant goals (which run through that
// same StreamQueryFunc), and the scheduler (whose ChildProcessExecutor re-execs
// this binary and lands back in the CLI path). Nothing asserts that
// `AutoCompact:` line exists, so deleting it would leave every test green while
// silently removing context-overflow protection from all four paths.
//
// AUDIT-P0-08.
func newCompactWiringSession(t *testing.T, cwd string) bool {
	t.Helper()
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	session, cleanup, err := newQuerySession(context.Background(), options{
		cwd:           cwd,
		model:         "test-model",
		maxTurns:      1,
		disableTools:  true,
		noPersistence: true,
		promptMode:    promptmode.Code.String(),
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	return session.AutoCompactEnabled()
}

func TestNewQuerySessionWiresAutoCompactByDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if !newCompactWiringSession(t, t.TempDir()) {
		t.Fatal("newQuerySession produced a session with no compactor; CLI / server / goal / scheduler long sessions have no context-overflow protection")
	}
}

// The wiring must still honor an explicit opt-out, so the default-on change
// cannot be mistaken for ignoring configuration.
func TestNewQuerySessionHonorsExplicitAutoCompactDisable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := t.TempDir()
	settingsDir := filepath.Join(project, ".claude")
	t.Setenv("NIUMA_CONFIG_DIR", settingsDir)
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.json"), []byte(`{"autoCompact":{"enabled":false}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if newCompactWiringSession(t, project) {
		t.Fatal("explicit autoCompact.enabled=false was ignored")
	}
}
