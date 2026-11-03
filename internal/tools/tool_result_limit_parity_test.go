package tools_test

import (
	"testing"

	"github.com/konglong87/super-niuma/internal/permissions"
	"github.com/konglong87/super-niuma/internal/toolresult"
	"github.com/konglong87/super-niuma/internal/tools"
	agenttool "github.com/konglong87/super-niuma/internal/tools/agent"
	"github.com/konglong87/super-niuma/internal/tools/askuserquestion"
	"github.com/konglong87/super-niuma/internal/tools/bashoutput"
	"github.com/konglong87/super-niuma/internal/tools/fileedit"
	"github.com/konglong87/super-niuma/internal/tools/fileread"
	"github.com/konglong87/super-niuma/internal/tools/filewrite"
	"github.com/konglong87/super-niuma/internal/tools/glob"
	"github.com/konglong87/super-niuma/internal/tools/ls"
	"github.com/konglong87/super-niuma/internal/tools/lsp"
	"github.com/konglong87/super-niuma/internal/tools/mcpresources"
	"github.com/konglong87/super-niuma/internal/tools/notebook"
	"github.com/konglong87/super-niuma/internal/tools/planmode"
	"github.com/konglong87/super-niuma/internal/tools/skill"
	"github.com/konglong87/super-niuma/internal/tools/task"
	"github.com/konglong87/super-niuma/internal/tools/taskoutput"
	"github.com/konglong87/super-niuma/internal/tools/todowrite"
	"github.com/konglong87/super-niuma/internal/tools/webbrowser"
	"github.com/konglong87/super-niuma/internal/tools/webfetch"
	"github.com/konglong87/super-niuma/internal/tools/websearch"
	"github.com/konglong87/super-niuma/internal/tools/workflow"
	"github.com/konglong87/super-niuma/internal/tools/worktree"
)

func TestClaudeCodeDefaultDeclaredToolResultLimits(t *testing.T) {
	items := []tools.Tool{
		glob.New(),
		webfetch.New(),
		websearch.New(),
		lsp.New(),
		taskoutput.New(),
		skill.New(),
		task.New(nil, "test-model"),
		agenttool.NewCreate(nil, "test-model", tools.NewRegistry()),
		agenttool.NewList(),
		agenttool.NewGet(),
		agenttool.NewStop(),
		agenttool.NewMessage(),
		askuserquestion.New(),
		filewrite.New(),
		fileedit.New(),
		fileedit.NewMulti(),
		planmode.NewEnter(),
		planmode.NewExit(),
		todowrite.NewRead(),
		todowrite.New(),
		worktree.New(),
		mcpresources.NewList(nil),
		mcpresources.NewRead(nil),
		bashoutput.New(),
		bashoutput.NewKillShell(),
		// AUDIT-P1-19: these declared no limit, so they slipped past the
		// truncation entirely.
		ls.New(),
		notebook.NewRead(),
		notebook.NewEdit(),
		webbrowser.New(),
		workflow.New(),
	}
	for _, item := range items {
		t.Run(item.Name(), func(t *testing.T) {
			if got := tools.EffectiveResultLimit(item, 200_000); got != toolresult.DefaultLimit {
				t.Fatalf("EffectiveResultLimit() = %d, want %d", got, toolresult.DefaultLimit)
			}
			guarded := tools.Guard(item, permissions.Policy{DefaultMode: "allow"})
			if got := tools.EffectiveResultLimit(guarded, 200_000); got != toolresult.DefaultLimit {
				t.Fatalf("EffectiveResultLimit(Guard(tool)) = %d, want %d", got, toolresult.DefaultLimit)
			}
		})
	}
}

// TestReadOptsOutOfTheResultBudgetDeliberately documents why Read is absent from
// the list above. AUDIT-P1-19 counted it among the tools missing a declared
// limit, but Read manages its own output: it caps at 2000 lines by default and
// hands back a manifest for large files, so it opts out of the shared budget on
// purpose. Declaring MaxResultSizeChars here would be dead code — the opt-out
// short-circuits first.
func TestReadOptsOutOfTheResultBudgetDeliberately(t *testing.T) {
	skipper, ok := tools.Tool(fileread.New()).(interface{ SkipToolResultBudget() bool })
	if !ok || !skipper.SkipToolResultBudget() {
		t.Fatal("Read no longer opts out of the tool result budget; it now needs MaxResultSizeChars()")
	}
	if got := tools.EffectiveResultLimit(fileread.New(), 200_000); got != 0 {
		t.Fatalf("EffectiveResultLimit(Read) = %d, want 0 (unbudgeted)", got)
	}
}
