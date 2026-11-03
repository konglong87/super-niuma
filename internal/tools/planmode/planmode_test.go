package planmode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/tools"
)

func TestPlanModeTools(t *testing.T) {
	tmp := t.TempDir()
	enterInput, _ := json.Marshal(map[string]string{"plan": "1. Test"})
	res := NewEnter().Run(context.Background(), enterInput, tools.Context{CWD: tmp})
	if res.IsError {
		t.Fatalf("enter = %+v", res)
	}
	state, err := Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Active || state.Plan != "1. Test" {
		t.Fatalf("state = %+v", state)
	}
	if _, err := os.Stat(filepath.Join(tmp, ".claude", "plan_mode.json")); !os.IsNotExist(err) {
		t.Fatalf("legacy plan mode file should not be written, err=%v", err)
	}
	exitInput, _ := json.Marshal(map[string]bool{"accepted": true})
	res = NewExit().Run(context.Background(), exitInput, tools.Context{CWD: tmp})
	if res.IsError {
		t.Fatalf("exit = %+v", res)
	}
	state, err = Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if state.Active {
		t.Fatalf("state = %+v", state)
	}
}

func TestPlanModeUsesConfiguredIdentityStatePath(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR_NAME", ".go-code")

	res := NewEnter().Run(context.Background(), json.RawMessage(`{"plan":"test"}`), tools.Context{CWD: tmp, WritableRoots: []string{tmp}})
	if res.IsError {
		t.Fatalf("enter = %+v", res)
	}
	if _, err := os.Stat(filepath.Join(tmp, ".go-code", "plan_mode.json")); err != nil {
		t.Fatalf("expected configured plan path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, ".niuma", "plan_mode.json")); !os.IsNotExist(err) {
		t.Fatalf("default plan path should not be written, err=%v", err)
	}
}

func TestPlanModeDescriptionsWarnAgainstReadOnlyUse(t *testing.T) {
	enterDesc := NewEnter().Description()
	for _, want := range []string{
		"writes .niuma/plan_mode.json",
		"read-only analysis",
		"do not modify files",
		"assistant response",
	} {
		if !strings.Contains(enterDesc, want) {
			t.Fatalf("enter description missing %q:\n%s", want, enterDesc)
		}
	}

	exitDesc := NewExit().Description()
	for _, want := range []string{
		"writes .niuma/plan_mode.json",
		"file writes allowed",
		"read-only",
		"no-modify",
	} {
		if !strings.Contains(exitDesc, want) {
			t.Fatalf("exit description missing %q:\n%s", want, exitDesc)
		}
	}

	enterSchema := string(NewEnter().InputSchema())
	for _, want := range []string{
		".niuma/plan_mode.json",
		"read-only",
		"no-modify",
	} {
		if !strings.Contains(enterSchema, want) {
			t.Fatalf("enter schema missing %q:\n%s", want, enterSchema)
		}
	}

	exitSchema := string(NewExit().InputSchema())
	if !strings.Contains(exitSchema, ".niuma/plan_mode.json") {
		t.Fatalf("exit schema missing state path:\n%s", exitSchema)
	}
}
