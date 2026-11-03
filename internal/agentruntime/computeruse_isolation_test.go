package agentruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/konglong87/super-niuma/internal/anthropic"
	cu "github.com/konglong87/super-niuma/internal/computeruse"
	"github.com/konglong87/super-niuma/internal/tools"
	computerusetool "github.com/konglong87/super-niuma/internal/tools/computeruse"
)

// A nonnil capability that panics if invoked, to catch accidental delegation.
type undelegableComputerService struct{ cu.Service }

type computerContextSpy struct{ context tools.Context }

func (*computerContextSpy) Name() string                 { return "ContextSpy" }
func (*computerContextSpy) Description() string          { return "inspect child context" }
func (*computerContextSpy) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (s *computerContextSpy) Run(_ context.Context, _ json.RawMessage, tc tools.Context) tools.Result {
	s.context = tc
	return tools.Result{Content: "isolated"}
}

func TestRunToolClearsComputerCapabilityAndImageFlag(t *testing.T) {
	spy := &computerContextSpy{}
	parent := tools.Context{TenantID: 7, UserID: 11, SessionID: 13, ComputerUse: undelegableComputerService{}, ComputerUseImageSupported: true, SubagentDepth: 1}
	runtime := Runtime{}
	trace := runtime.runTool(context.Background(), tools.NewRegistry(spy), anthropic.ContentBlock{
		ID: "tool-1", Name: spy.Name(), Input: json.RawMessage(`{}`),
	}, parent, nil, nil, Request{CWD: t.TempDir()}, 1, "worker")
	if trace.IsError || trace.Output != "isolated" {
		t.Fatalf("trace = %+v", trace)
	}
	if spy.context.ComputerUse != nil || spy.context.ComputerUseImageSupported {
		t.Fatal("child inherited desktop authority")
	}
	if spy.context.SessionID != parent.SessionID || spy.context.SubagentDepth != parent.SubagentDepth {
		t.Fatal("unrelated identity was changed")
	}
	if parent.ComputerUse == nil || !parent.ComputerUseImageSupported {
		t.Fatal("parent authority was mutated")
	}
}

func TestSubagentRegistryAlwaysRemovesComputerUse(t *testing.T) {
	for _, allowlist := range []bool{false, true} {
		t.Run(map[bool]string{false: "default policy", true: "explicit allowlist"}[allowlist], func(t *testing.T) {
			project := t.TempDir()
			t.Setenv("HOME", t.TempDir())
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			t.Setenv("NIUMA_CONFIG_DIR", os.Getenv("CLAUDE_CONFIG_DIR"))
			request := Request{Prompt: "do it", CWD: project}
			if allowlist {
				dir := filepath.Join(project, ".claude", "agents")
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "worker.md"), []byte("---\nname: worker\ntools: [Echo, ComputerUse]\n---\nWork."), 0644); err != nil {
					t.Fatal(err)
				}
				request.SubagentType = "worker"
			}
			parentRegistry := tools.NewRegistry(echoTool{}, computerusetool.New())
			streamer := &scriptedStreamer{toolName: computerusetool.ToolName, expectedToolResult: "unknown tool: ComputerUse"}
			runtime := Runtime{Client: streamer, Registry: parentRegistry, Model: "base-model"}
			result, err := runtime.Run(context.Background(), request, tools.Context{ComputerUse: undelegableComputerService{}, ComputerUseImageSupported: true})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.ToolCalls) != 1 || !result.ToolCalls[0].IsError || result.ToolCalls[0].Output != "unknown tool: ComputerUse" {
				t.Fatalf("calls = %+v", result.ToolCalls)
			}
			if len(streamer.tools) == 0 {
				t.Fatal("no model request captured")
			}
			for _, definitions := range streamer.tools {
				for _, definition := range definitions {
					if definition.Name == computerusetool.ToolName {
						t.Fatal("subagent received ComputerUse definition")
					}
				}
			}
			if _, ok := parentRegistry.Get(computerusetool.ToolName); !ok {
				t.Fatal("parent registry mutated")
			}
		})
	}
}
