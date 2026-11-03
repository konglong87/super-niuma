package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// testHomeDir 是本包 fixture 与 golden 统一假设的 home 前缀。welcome header 和 mode hint
// 都经过 abbreviateHome()，它读 os.UserHomeDir()（即 $HOME），所以断言 `工作区 ~/GolandProjects/...`
// 只在 $HOME 恰好等于该前缀时成立。不 pin 就变成「只在原作者机器上通过」的测试：CI 的
// runner home 是 /home/runner，路径不再被缩写成 ~，5 个测试直接失败。
const testHomeDir = "/Users/example"

func TestMain(m *testing.M) {
	// 只影响本测试进程，让 abbreviateHome 的结果与 fixture/golden 一致。
	os.Setenv("HOME", testHomeDir)
	os.Setenv("USERPROFILE", testHomeDir) // Windows 上 os.UserHomeDir() 读这个
	os.Exit(m.Run())
}

func TestModelSubmitRunsPrompt(t *testing.T) {
	var gotPrompt string
	model := NewModel(context.Background(), Options{
		Run: func(ctx context.Context, prompt string) (string, error) {
			gotPrompt = prompt
			return "answer", nil
		},
	})
	model.textarea.SetValue("hello")
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected command")
	}
	if !model.busy || len(model.messages) != 1 || model.messages[0].role != "user" {
		t.Fatalf("model after submit = %+v", model)
	}
	model = runTestCommand(t, model, cmd)
	if gotPrompt != "hello" {
		t.Fatalf("prompt = %q", gotPrompt)
	}
	if model.busy || len(model.messages) != 2 || model.messages[1].content != "answer" {
		t.Fatalf("model after response = %+v", model)
	}
}

func TestNormalizeBareInteractiveCommand(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"exit", "/exit"},
		{" EXIT ", "/exit"},
		{"退出", "/exit"},
		{"quit", "/quit"},
		{"QuIt", "/quit"},
		{"compact", "/compact"},
		{" COMPACT ", "/compact"},
		{"压缩", "/compact"},
		{"/exit", "/exit"},
		{"/compact", "/compact"},
		{"exit now", "exit now"},
		{"请退出", "请退出"},
		{"compact current session", "compact current session"},
		{"帮我压缩一下", "帮我压缩一下"},
	}
	for _, tt := range tests {
		if got := normalizeBareInteractiveCommand(tt.input); got != tt.want {
			t.Fatalf("normalizeBareInteractiveCommand(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestModelBareExitAliasesQuitWithoutRunning(t *testing.T) {
	for _, input := range []string{"exit", "退出"} {
		t.Run(input, func(t *testing.T) {
			var ran bool
			model := NewModel(context.Background(), Options{
				Run: func(ctx context.Context, prompt string) (string, error) {
					ran = true
					return "answer", nil
				},
			})
			model.textarea.SetValue(input)
			updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
			model = updated.(Model)
			if ran || cmd == nil || model.busy || len(model.messages) != 0 {
				t.Fatalf("bare exit alias should quit without running: ran=%v cmd=%v model=%+v", ran, cmd, model)
			}
		})
	}
}

func TestModelBareCompactAliasesRunCompactSlash(t *testing.T) {
	for _, input := range []string{"compact", "压缩"} {
		t.Run(input, func(t *testing.T) {
			var gotPrompt string
			model := NewModel(context.Background(), Options{
				Run: func(ctx context.Context, prompt string) (string, error) {
					gotPrompt = prompt
					return "compact output", nil
				},
			})
			model.textarea.SetValue(input)
			updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
			model = updated.(Model)
			if cmd == nil {
				t.Fatal("expected command")
			}
			if !model.busy || len(model.messages) != 1 || model.messages[0].content != input {
				t.Fatalf("model should show original input while running alias: %+v", model)
			}
			model = runTestCommand(t, model, cmd)
			if gotPrompt != "/compact" {
				t.Fatalf("prompt = %q, want /compact", gotPrompt)
			}
			if model.busy || len(model.messages) != 2 || model.messages[1].content != "compact output" {
				t.Fatalf("model after compact alias response = %+v", model)
			}
		})
	}
}

func TestModelBareCommandSentencesRemainPrompts(t *testing.T) {
	for _, input := range []string{"exit now", "请退出", "compact current session", "帮我压缩一下"} {
		t.Run(input, func(t *testing.T) {
			var gotPrompt string
			model := NewModel(context.Background(), Options{
				Run: func(ctx context.Context, prompt string) (string, error) {
					gotPrompt = prompt
					return "answer", nil
				},
			})
			model.textarea.SetValue(input)
			updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
			model = updated.(Model)
			if cmd == nil {
				t.Fatal("expected command")
			}
			model = runTestCommand(t, model, cmd)
			if gotPrompt != input {
				t.Fatalf("prompt = %q, want %q", gotPrompt, input)
			}
		})
	}
}

func TestModelCtrlCStopsCurrentTurnBeforeExit(t *testing.T) {
	started := make(chan context.Context, 1)
	model := NewModel(context.Background(), Options{
		Run: func(ctx context.Context, prompt string) (string, error) {
			started <- ctx
			<-ctx.Done()
			return "", ctx.Err()
		},
	})
	model.textarea.SetValue("hello")
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil || !model.busy || model.turnCancel == nil {
		t.Fatalf("expected busy model with cancel: cmd=%v model=%+v", cmd, model)
	}

	runCmd := firstBlockingTestCommand(t, cmd)
	done := make(chan tea.Msg, 1)
	go func() { done <- runCmd() }()
	ctx := <-started
	updated, stopCmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	model = updated.(Model)
	if stopCmd != nil || !model.busy || !model.turnStopping {
		t.Fatalf("first ctrl+c should stop only: cmd=%v busy=%v stopping=%v", stopCmd, model.busy, model.turnStopping)
	}
	if ctx.Err() == nil {
		t.Fatal("turn context was not cancelled")
	}
	if !strings.Contains(model.View(), "Stopping...") || !strings.Contains(model.View(), "ctrl+c stop/exit") {
		t.Fatalf("view missing stop status:\n%s", model.View())
	}

	updated, quitCmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	model = updated.(Model)
	if quitCmd == nil {
		t.Fatal("second ctrl+c should quit")
	}

	updated, _ = model.Update(<-done)
	model = updated.(Model)
	if model.busy || model.turnCancel != nil || model.turnStopping {
		t.Fatalf("response should clear turn state: %+v", model)
	}
}

func TestModelShowsUsagePanelFromResult(t *testing.T) {
	model := NewModel(context.Background(), Options{
		RunResult: func(ctx context.Context, prompt string) (QueryResult, error) {
			return QueryResult{
				Response:   "answer",
				Model:      "test-model",
				StopReason: "end_turn",
				Turns:      2,
				SessionID:  "sess-1",
				Usage: Usage{
					InputTokens:              12,
					OutputTokens:             4,
					CacheCreationInputTokens: 3,
					CacheReadInputTokens:     5,
					Speed:                    "fast",
				},
				ToolCalls: []ToolCall{{ID: "toolu_1", Name: "Read"}, {ID: "toolu_2", Name: "Bash", IsError: true}},
				Context:   RuntimeContext{CWD: "/workspace", MaxTurns: 3, MaxTokens: 4096, ToolCount: 10, ContextWindow: 200000},
			}, nil
		},
	})
	model.textarea.SetValue("hello")
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected command")
	}
	model = runTestCommand(t, model, cmd)
	view := model.View()
	for _, want := range []string{"Usage", "turns=2/3", "tokens in/out=12/4", "ctx=0.0% 12/200000", "cache create/read=3/5 session_hit=41.7%", "执行了 2 次操作 · 1 次未完成 · 最近使用 运行命令"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
	for _, notWant := range []string{"model=test-model", "max_tokens=4096", "speed=fast", "stop=end_turn", "session=sess-1", "cwd=/workspace"} {
		if strings.Contains(view, notWant) {
			t.Fatalf("quiet usage should not repeat context %q:\n%s", notWant, view)
		}
	}
}

func TestModelUsagePanelAccumulatesAcrossSession(t *testing.T) {
	results := []QueryResult{
		{
			Response:   "first",
			Model:      "test-model",
			StopReason: "end_turn",
			Turns:      1,
			SessionID:  "sess-1",
			Usage: Usage{
				InputTokens:              10,
				OutputTokens:             3,
				CacheCreationInputTokens: 2,
				CacheReadInputTokens:     4,
			},
			ToolCalls: []ToolCall{{ID: "toolu_1", Name: "Read"}},
			Context:   RuntimeContext{MaxTurns: 10, ToolCount: 27, ContextWindow: 100},
		},
		{
			Response:   "second",
			Model:      "test-model",
			StopReason: "end_turn",
			Turns:      2,
			SessionID:  "sess-1",
			Usage: Usage{
				InputTokens:                         7,
				OutputTokens:                        5,
				CacheCreationEphemeral1hInputTokens: 1,
				CacheReadInputTokens:                6,
			},
			ToolCalls: []ToolCall{{ID: "toolu_2", Name: "Bash", IsError: true}, {ID: "toolu_3", Name: "Edit"}},
			Context:   RuntimeContext{MaxTurns: 10, ToolCount: 27, ContextWindow: 100},
		},
	}
	index := 0
	model := NewModel(context.Background(), Options{
		RunResult: func(ctx context.Context, prompt string) (QueryResult, error) {
			result := results[index]
			index++
			return result, nil
		},
	})
	for _, prompt := range []string{"hello", "again"} {
		model.textarea.SetValue(prompt)
		updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
		model = updated.(Model)
		if cmd == nil {
			t.Fatal("expected command")
		}
		model = runTestCommand(t, model, cmd)
	}
	view := model.View()
	for _, want := range []string{"Usage", "turns=3/10", "tokens in/out=17/8", "ctx=7.0% 7/100", "cache create/read=3/10 session_hit=58.8%", "执行了 3 次操作 · 1 次未完成 · 最近使用 编辑文件"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing cumulative %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "session=sess-1") {
		t.Fatalf("quiet cumulative usage should not repeat session context:\n%s", view)
	}
	if strings.Contains(view, "ctx=17.0% 17/100") {
		t.Fatalf("context usage should use last turn input, not cumulative input:\n%s", view)
	}
}

func TestModelUsagePanelShowsNonDefaultStopReason(t *testing.T) {
	model := NewModel(context.Background(), Options{})
	model.usage = usagePanel{
		InputTokens:  10,
		OutputTokens: 3,
		StopReason:   "max_tokens",
	}
	view := model.usageView()
	if !strings.Contains(view, "stop=max_tokens") {
		t.Fatalf("usage view should keep non-default stop reason:\n%s", view)
	}
	meta := model.responseMeta(QueryResult{StopReason: "max_tokens"})
	if !strings.Contains(meta, "stop=max_tokens") {
		t.Fatalf("response meta should keep non-default stop reason: %s", meta)
	}
	if meta := model.responseMeta(QueryResult{StopReason: "end_turn"}); strings.Contains(meta, "stop=end_turn") {
		t.Fatalf("response meta should suppress normal stop reason: %s", meta)
	}
}

func TestUsagePanelContextUsesLastModelRequestInput(t *testing.T) {
	model := NewModel(context.Background(), Options{
		RunResult: func(ctx context.Context, prompt string) (QueryResult, error) {
			return QueryResult{
				Response: "answer",
				Model:    "glm-5.1",
				Usage: Usage{
					InputTokens:     739507,
					OutputTokens:    3202,
					LastInputTokens: 150000,
				},
				Context: RuntimeContext{ContextWindow: 200000},
			}, nil
		},
	})
	model.textarea.SetValue("hi")
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected command")
	}
	model = runTestCommand(t, model, cmd)

	view := model.View()
	for _, want := range []string{"tokens in/out=739507/3202", "ctx=75.0% 150000/200000"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
	for _, bad := range []string{"ctx=369.8% 739507/200000", "739507/200000"} {
		if strings.Contains(view, bad) {
			t.Fatalf("context usage should use last model request input, not cumulative input:\n%s", view)
		}
	}
}

func TestRenderMarkdownTableDrawsStableRules(t *testing.T) {
	input := strings.Join([]string{
		"| # | 变更内容 | 涉及表 |",
		"|---|---|---|",
		"| 1 | `card_questions` 新增字段 | card_questions |",
		"| 2 | patients 新增 purchased_oximeter | patients |",
	}, "\n")
	got := stripANSI(renderMarkdownForWidth(input, 96))
	for _, want := range []string{
		"┌─────┬",
		"│ # ",
		"│ 变更内容",
		"│ card_questions",
		"purchased_oximeter",
		"└─────┴",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered table missing %q:\n%s", want, got)
		}
	}
	for _, bad := range []string{"|---|---|---|", "+-----+", "GOCLAUDETABLE"} {
		if strings.Contains(got, bad) {
			t.Fatalf("markdown table leaked raw/placeholder %q:\n%s", bad, got)
		}
	}
	if count := strings.Count(got, "├"); count < 2 {
		t.Fatalf("table should draw row separators between body rows, got %d:\n%s", count, got)
	}
}

func TestRenderMarkdownTableWrapsLongCellsWithoutTruncation(t *testing.T) {
	input := strings.Join([]string{
		"| 列 A | 列 B |",
		"|---|---|",
		"| 数据1 | 这是一段非常长的中文内容需要完整显示不能被截断并且应该自动换行 |",
	}, "\n")
	got := stripANSI(renderMarkdownForWidth(input, 36))
	for _, want := range []string{
		"┌",
		"┬",
		"└",
		"这是一段非常",
		"长的中文内容",
		"需要完整显示",
		"不能被截断",
		"应该自动换行",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("wrapped table missing %q:\n%s", want, got)
		}
	}
	for _, bad := range []string{"…", "...", "+---+", "GOCLAUDETABLE"} {
		if strings.Contains(got, bad) {
			t.Fatalf("wrapped table should not truncate or leak %q:\n%s", bad, got)
		}
	}
	if count := strings.Count(got, "├"); count != 1 {
		t.Fatalf("single body row should only have header separator, got %d:\n%s", count, got)
	}
}

func TestRenderMarkdownTableDrawsSeparatorsBetweenWrappedBodyRows(t *testing.T) {
	input := strings.Join([]string{
		"| 优先级 | 任务 | 原因 |",
		"|---|---|---|",
		"| P0 | 补齐5-skills英文版（10篇） | 双语是铁律，整个章节缺英文很严重 |",
		"| P1 | 充实6个骨架角色（admin/hr/sales/teacher/finance/planner） | 用户点进去看到空壳体验很差 |",
		"| P2 | 补齐roles/resources/4-advanced-topics英文版 | 双语完整性 |",
	}, "\n")
	got := stripANSI(renderMarkdownForWidth(input, 90))
	for _, want := range []string{"优先级", "补齐5-skills英文版", "admin/hr/sales", "双语完整性"} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered priority table missing %q:\n%s", want, got)
		}
	}
	if count := strings.Count(got, "├"); count < 3 {
		t.Fatalf("three body rows should include header and body separators, got %d:\n%s", count, got)
	}
	lines := strings.Split(got, "\n")
	for i, line := range lines {
		if strings.Contains(line, "teacher/finance/planner") {
			if i == 0 || strings.HasPrefix(lines[i-1], "├") {
				t.Fatalf("wrapped continuation should not be preceded by an internal row separator:\n%s", got)
			}
			return
		}
	}
	t.Fatalf("wrapped continuation line not found:\n%s", got)
}

func TestRenderMarkdownLongNarrativeTableUsesGroupedList(t *testing.T) {
	input := strings.Join([]string{
		"| # | 问题 | 优先级 | 说明 |",
		"|---|---|---|---|",
		"| 4 | CHANGELOG 滞后8个版本 | P1->暂缓 | gitignore 排除不入库，补齐意义有限 |",
		"| 7 | gitignore 半吊子状态 | P2 | 已 track 但应 ignore 的文件需 git rm --cached，改动面广 |",
	}, "\n")
	got := stripANSI(renderMarkdownForWidth(input, 96))
	for _, want := range []string{
		"• 4 · CHANGELOG 滞后8个版本",
		"优先级: P1->暂缓",
		"说明: gitignore 排除不入库",
		"• 7 · gitignore 半吊子状态",
		"git rm --cached",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("grouped narrative table missing %q:\n%s", want, got)
		}
	}
	for _, bad := range []string{"┌", "┬", "└", "GOCLAUDETABLE"} {
		if strings.Contains(got, bad) {
			t.Fatalf("long narrative table should avoid heavy grid %q:\n%s", bad, got)
		}
	}
	if strings.Contains(got, "• #") {
		t.Fatalf("grouped table row numbers must not render as raw markdown headings:\n%s", got)
	}
}

func TestRenderMarkdownGroupedListUsesDedicatedContrastStyles(t *testing.T) {
	input := strings.Join([]string{
		"| # | 问题 | 优先级 | 说明 |",
		"|---|---|---|---|",
		"| 4 | CHANGELOG 滞后8个版本且缺少正式发布记录 | P1 | 需要补齐发布记录并核对每个版本的变更范围 |",
		"| 7 | 需要清理历史文档并补充验证报告 | P2 | 相关文件较多，应该分阶段处理并保留回滚记录 |",
	}, "\n")
	rendered := renderMarkdownForWidth(input, 96)
	plain := stripANSI(rendered)
	title := "• 4 · CHANGELOG 滞后8个版本且缺少正式发布记录"
	detail := "  优先级: P1"
	if !strings.Contains(plain, title) || !strings.Contains(plain, detail) {
		t.Fatalf("grouped list content missing:\n%s", plain)
	}
	if got, want := markdownListTitleStyle.GetForeground(), tuiBodyColor; got != want {
		t.Fatalf("grouped list title foreground = %v, want %v", got, want)
	}
	if got, want := markdownListDetailStyle.GetForeground(), lipgloss.Color("246"); got != want {
		t.Fatalf("grouped list detail foreground = %v, want %v", got, want)
	}
	if markdownListDetailStyle.GetForeground() == statusStyle.GetForeground() {
		t.Fatalf("grouped list detail should not reuse status style foreground")
	}
}

func TestRenderMarkdownShortFourColumnTableStaysGrid(t *testing.T) {
	input := strings.Join([]string{
		"| 名称 | 状态 | 数量 | 类型 |",
		"|---|---|---|---|",
		"| api | ok | 2 | core |",
		"| web | warn | 1 | ui |",
	}, "\n")
	got := stripANSI(renderMarkdownForWidth(input, 96))
	for _, want := range []string{"┌", "┬", "│ 名称", "│ api", "│ web", "└"} {
		if !strings.Contains(got, want) {
			t.Fatalf("short four-column table should stay as grid, missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "• #api") || strings.Contains(got, "状态: ok") {
		t.Fatalf("short four-column data table should not become grouped list:\n%s", got)
	}
}

func TestRenderMarkdownTableRowsStartAtSameColumnAfterParagraph(t *testing.T) {
	input := strings.Join([]string{
		"Markdown 表格显示测试：",
		"",
		"基础表格",
		"",
		"| 姓名 | 年龄 | 城市 |",
		"|---|---:|---|",
		"| 张三 | 28 | 北京 |",
		"| 李四 | 35 | 上海 |",
	}, "\n")
	got := stripANSI(renderMarkdownForWidth(input, 100))
	for _, line := range strings.Split(got, "\n") {
		trimmed := strings.TrimLeft(line, " ")
		if trimmed == "" {
			continue
		}
		switch []rune(trimmed)[0] {
		case '┌', '├', '└', '│':
			if line != trimmed {
				t.Fatalf("table line should not inherit paragraph indentation:\nline=%q\nrendered:\n%s", line, got)
			}
		}
	}
}

func TestRenderMarkdownTableDoesNotRewriteFencedCode(t *testing.T) {
	input := strings.Join([]string{
		"```",
		"| a | b |",
		"|---|---|",
		"| 1 | 2 |",
		"```",
	}, "\n")
	got := stripANSI(renderMarkdownForWidth(input, 80))
	if !strings.Contains(got, "|---|---|") {
		t.Fatalf("fenced markdown table should stay literal:\n%s", got)
	}
}

func TestNormalizeMarkdownHeadingsAddsMissingSpace(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"三级中文缺空格", "###总结", "### 总结"},
		{"四级数字缺空格", "####2. 模块间文件体积不均匀", "#### 2. 模块间文件体积不均匀"},
		{"已有空格保持不变", "### 总结", "### 总结"},
		{"单井号不误伤", "#1 是第一条", "#1 是第一条"},
		{"颜色值不误伤", "#FF0000 红色", "#FF0000 红色"},
		{"七个井号不是标题", "#######x", "#######x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeMarkdownHeadings(tc.in); got != tc.want {
				t.Fatalf("normalizeMarkdownHeadings(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeMarkdownHeadingsSkipsFencedCode(t *testing.T) {
	input := strings.Join([]string{
		"###总结",
		"```",
		"###代码块里的字面内容",
		"```",
	}, "\n")
	got := normalizeMarkdownHeadings(input)
	if !strings.Contains(got, "### 总结") {
		t.Fatalf("fence 外的标题应补空格:\n%s", got)
	}
	if !strings.Contains(got, "###代码块里的字面内容") {
		t.Fatalf("fence 内的内容必须保持原样:\n%s", got)
	}
}

func TestRenderMarkdownHeadingWithoutSpaceStillRendersAsHeading(t *testing.T) {
	got := stripANSI(renderMarkdownForWidth("###总结", 80))
	if strings.Contains(got, "###") {
		t.Fatalf("标题标记应被识别消费，而不是字面显示 ###:\n%s", got)
	}
	if !strings.Contains(got, "总结") {
		t.Fatalf("标题文字应保留:\n%s", got)
	}
}

func TestRenderMarkdownHorizontalRuleUsesSolidLine(t *testing.T) {
	got := stripANSI(renderMarkdownForWidth("上文\n\n---\n\n下文", 80))
	if !strings.Contains(got, "─") {
		t.Fatalf("水平线应渲染为 box-drawing 实线:\n%s", got)
	}
	if strings.Contains(got, "--------") {
		t.Fatalf("水平线不应再渲染为 ASCII 连字符:\n%s", got)
	}
}

func TestRenderMarkdownPreservesSoftLineBreaksInMultiLineBlocks(t *testing.T) {
	// A file tree emitted with single newlines and no code fence must keep each
	// entry on its own line instead of collapsing into one word-wrapped paragraph.
	input := strings.Join([]string{
		"docs/pending-fixes/git-conflict-and-transcript-locating/",
		"├── git-conflict-analysis.md # Git 冲突卡死根因分析与改进方案",
		"└── transcript-locating-reflection.md # Transcript 定位过程反思与优化方案",
	}, "\n")
	got := stripANSI(renderMarkdownForWidth(input, 96))
	lines := strings.Split(got, "\n")
	find := func(substr string) int {
		for i, l := range lines {
			if strings.Contains(l, substr) {
				return i
			}
		}
		return -1
	}
	dir := find("git-conflict-and-transcript-locating/")
	branch := find("├──")
	leaf := find("└──")
	if dir < 0 || branch < 0 || leaf < 0 {
		t.Fatalf("expected all three tree lines present:\n%s", got)
	}
	if dir == branch || branch == leaf || dir == leaf {
		t.Fatalf("tree entries collapsed onto shared lines (dir=%d branch=%d leaf=%d):\n%s", dir, branch, leaf, got)
	}
}

func TestRenderMarkdownUsesReadableClaudeLikeColors(t *testing.T) {
	rendered := renderMarkdownForWidth(strings.Join([]string{
		"收到，测试 Markdown 格式演示：",
		"",
		"- 列表项 1",
		"- **列表项 2**",
		"",
		"`行内代码`",
		"",
		"```go",
		"// 代码块示例",
		"func main() {}",
		"```",
	}, "\n"), 96)
	for _, want := range []string{
		"\x1b[38;5;253m",
		"\x1b[38;5;255;1m",
		"\x1b[38;5;81;48;5;236m",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered markdown missing readable color %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "\x1b[38;5;180;48;5;236m") {
		t.Fatalf("inline code should use restrained link-path color instead of warm high-emphasis color:\n%s", rendered)
	}
	if strings.Contains(rendered, "\x1b[38;5;244m收到") || strings.Contains(rendered, "\x1b[38;5;240m收到") {
		t.Fatalf("body text should not render with muted dark colors:\n%s", rendered)
	}
}

func TestRenderMarkdownUntypedDirectoryCodeBlockAvoidsErrorRedBackground(t *testing.T) {
	rendered := renderMarkdownForWidth(strings.Join([]string{
		"### 三、目录结构（6大主线）",
		"",
		"```",
		"0-start-here/          → 入门认知（AI是什么、焦虑、学习路径）",
		"1-understand-ai/       → AI原理（LLM基础、思维机制、Agent入门、工程范式）",
		"2-choose-tools/        → 工具选择（矩阵、Claude/DeepSeek/ChatGPT等详细指南）",
		"3-ai-agents/           → AI Agent专题（类型、框架、MCP、安全治理）",
		"4-advanced-topics/     → 进阶主题（RAG、微调、部署、MCP、世界模型等30篇）",
		"5-skills/              → 技能包（Agent设计模式42章、开发/研究/规划/效率技能）",
		"roles/                 → 行业案例（程序员、设计师、教师、Vibe Coding等15+角色）",
		"prompts/               → 提示词库（系统提示词、写作/编程/学习/分析场景）",
		"resources/             → 外部资源（AI工具、生成式AI、专业资源、外部链接）",
		"```",
	}, "\n"), 150)
	plain := stripANSI(rendered)
	for _, want := range []string{"0-start-here/", "入门认知", "resources/", "外部资源"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("rendered code block missing %q:\n%s", want, plain)
		}
	}
	for _, bad := range []string{"\x1b[48;5;203m", "\x1b[48;5;196m", "\x1b[48;5;9m"} {
		if strings.Contains(rendered, bad) {
			t.Fatalf("untyped directory code block should not use red error background %q:\n%s", bad, rendered)
		}
	}
}

func TestModelNewlineShortcutsInsertNewlineAndViewShowsHint(t *testing.T) {
	var ran bool
	model := NewModel(context.Background(), Options{
		Run: func(ctx context.Context, prompt string) (string, error) {
			ran = true
			return "answer", nil
		},
	})
	model.textarea.SetValue("line one")
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	model = updated.(Model)
	if ran || model.busy {
		t.Fatalf("alt+enter should insert newline without submitting: ran=%v busy=%v", ran, model.busy)
	}
	if !strings.Contains(model.textarea.Value(), "\n") {
		t.Fatalf("value = %q", model.textarea.Value())
	}
	if !strings.Contains(model.View(), "ctrl+j newline") || !strings.Contains(model.View(), "enter send") {
		t.Fatalf("view missing newline hint:\n%s", model.View())
	}
	if cmd == nil {
		t.Fatal("expected textarea command")
	}

	model.textarea.SetValue("line two")
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	model = updated.(Model)
	if !strings.Contains(model.textarea.Value(), "\n") {
		t.Fatalf("ctrl+j value = %q", model.textarea.Value())
	}
}

func TestModelCtrlJExpandsInputWithoutScrollingInputContentAway(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Run: func(ctx context.Context, prompt string) (string, error) {
			return "answer", nil
		},
	})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 14})
	model = updated.(Model)
	for i := 0; i < 8; i++ {
		model.messages = append(model.messages, message{role: "assistant", content: "message " + strconv.Itoa(i)})
	}
	model.setTextareaValue("first line")
	oldInputHeight := model.textarea.Height()
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	model = updated.(Model)
	view := model.View()
	if !strings.Contains(model.textarea.Value(), "first line\n") || !strings.Contains(view, "first line") {
		t.Fatalf("input content should remain visible after ctrl+j:\nvalue=%q\nview=%s", model.textarea.Value(), view)
	}
	if model.textarea.Height() <= oldInputHeight {
		t.Fatalf("textarea did not expand: old=%d new=%d", oldInputHeight, model.textarea.Height())
	}
}

func TestModelPastesClipboardImageAttachment(t *testing.T) {
	var gotPrompt string
	model := NewModel(context.Background(), Options{
		ImportClipboard: func(ctx context.Context) (Attachment, bool, error) {
			return Attachment{Type: "image", MediaType: "image/png", Name: "clip.png", Path: "/tmp/clip.png", SizeBytes: 2048}, true, nil
		},
		Run: func(ctx context.Context, prompt string) (string, error) {
			gotPrompt = prompt
			return "answer", nil
		},
	})
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	model = updated.(Model)
	if cmd != nil || len(model.attachments) != 1 {
		t.Fatalf("attachments=%+v cmd=%v", model.attachments, cmd)
	}
	if got := model.textarea.Value(); got != "[Image #1]" {
		t.Fatalf("input after paste = %q", got)
	}
	if view := model.View(); !strings.Contains(view, "Attachments") || !strings.Contains(view, "[Image #1]") || !strings.Contains(view, "clip.png") || !strings.Contains(view, "2.0KB") {
		t.Fatalf("view missing attachment tray:\n%s", view)
	}
	model.textarea.SetValue("describe [Image #1]")
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected query command")
	}
	model = runTestCommand(t, model, cmd)
	if !strings.Contains(gotPrompt, "describe [Image #1]") || !strings.Contains(gotPrompt, "Attached context") || !strings.Contains(gotPrompt, "/tmp/clip.png") {
		t.Fatalf("prompt = %q", gotPrompt)
	}
	transcript := renderTranscriptForTest(model)
	if len(model.attachments) != 0 || !strings.Contains(transcript, "1 image(s): [Image #1]") {
		t.Fatalf("attachments/transcript after submit: attachments=%+v transcript=%s view=%s", model.attachments, transcript, model.View())
	}
}

func TestModelRunsStructuredImageAttachment(t *testing.T) {
	var gotPrompt string
	var gotAttachments []Attachment
	model := NewModel(context.Background(), Options{
		ImportClipboard: func(ctx context.Context) (Attachment, bool, error) {
			return Attachment{Type: "image", MediaType: "image/png", Name: "clip.png", Path: "/tmp/clip.png", SizeBytes: 2048}, true, nil
		},
		RunWithAttachments: func(ctx context.Context, prompt string, attachments []Attachment) (QueryResult, error) {
			gotPrompt = prompt
			gotAttachments = append([]Attachment(nil), attachments...)
			return QueryResult{Response: "answer"}, nil
		},
	})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	model = updated.(Model)
	model.textarea.SetValue("describe [Image #1]")
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected query command")
	}
	model = runTestCommand(t, model, cmd)
	if gotPrompt != "describe [Image #1]" {
		t.Fatalf("prompt = %q", gotPrompt)
	}
	if len(gotAttachments) != 1 || gotAttachments[0].ID != 1 || gotAttachments[0].Path != "/tmp/clip.png" {
		t.Fatalf("attachments = %+v", gotAttachments)
	}
}

func TestModelShowsClipboardImageHint(t *testing.T) {
	model := NewModel(context.Background(), Options{
		DetectClipboardImage: func(ctx context.Context) (bool, error) { return true, nil },
		Run: func(ctx context.Context, prompt string) (string, error) {
			return "answer", nil
		},
	})
	cmd := model.checkClipboardImage()
	if cmd == nil {
		t.Fatal("expected clipboard check command")
	}
	model = runTestCommand(t, model, cmd)
	if !strings.Contains(model.View(), "Image in clipboard") || !strings.Contains(model.View(), "ctrl+v to paste") {
		t.Fatalf("view missing clipboard hint:\n%s", model.View())
	}
}

func TestModelInputHidesLineNumbers(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Run: func(ctx context.Context, prompt string) (string, error) {
			return "answer", nil
		},
	})
	view := model.View()
	if !strings.Contains(view, "> Ask niuma") {
		t.Fatalf("view missing prompt without line number:\n%s", view)
	}
	if strings.Contains(view, ">   1 Ask niuma") {
		t.Fatalf("view still shows textarea line number:\n%s", view)
	}
}

func TestModelInputAvoidsBlackCursorLineBackground(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Run: func(ctx context.Context, prompt string) (string, error) {
			return "answer", nil
		},
	})
	model.textarea.SetValue("visible text")
	view := model.textarea.View()
	for _, disallowed := range []string{"\x1b[40m", "\x1b[48;5;0m", "\x1b[48;2;0;0;0m"} {
		if strings.Contains(view, disallowed) {
			t.Fatalf("textarea contains black background escape %q:\n%q", disallowed, view)
		}
	}
	if !strings.Contains(view, "visible text") {
		t.Fatalf("textarea missing input text:\n%s", view)
	}
}

func TestModelInputBoxShowsRulesOnWelcome(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Run: func(ctx context.Context, prompt string) (string, error) {
			return "answer", nil
		},
	})
	view := model.View()
	if count := strings.Count(view, "────────────────"); count < 2 {
		t.Fatalf("welcome input should render top and bottom rules, got %d:\n%s", count, view)
	}
	if !strings.Contains(view, "> Ask niuma") {
		t.Fatalf("welcome input missing prompt:\n%s", view)
	}
}

func TestModelDefersInitialRenderUntilWindowSize(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Welcome: WelcomeInfo{PermissionMode: "ask"},
		Run: func(ctx context.Context, prompt string) (string, error) {
			return "answer", nil
		},
		SwitchPermissionMode: func(current string) (string, error) {
			return "allow", nil
		},
		WaitForInitialWindowSize: true,
	})
	if view := model.View(); view != "" {
		t.Fatalf("initial render before WindowSize should be empty to avoid stale startup frames:\n%s", view)
	}

	updated, cmd := model.Update(tea.WindowSizeMsg{Width: 190, Height: 46})
	model = updated.(Model)
	if cmd != nil {
		t.Fatal("first WindowSize must not clear terminal content outside the TUI")
	}
	view := stripANSI(model.View())
	if count := strings.Count(view, "╭─ niuma"); count != 1 {
		t.Fatalf("fallback welcome should render once after WindowSize, count=%d:\n%s", count, view)
	}
	if count := strings.Count(view, "> Ask niuma"); count != 1 {
		t.Fatalf("fallback welcome should render one input prompt after WindowSize, count=%d:\n%s", count, view)
	}
	if !strings.Contains(view, "权限 ask  shift+tab 切换权限模式") {
		t.Fatalf("fallback welcome after WindowSize missing permission strip:\n%s", view)
	}
}

func TestModelInputExpandsToShowAllUserText(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Run: func(ctx context.Context, prompt string) (string, error) {
			return "answer", nil
		},
	})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model = updated.(Model)
	lines := []string{"line one", "line two", "line three", "line four", "line five"}
	model.setTextareaValue(strings.Join(lines, "\n"))
	view := model.View()
	for _, line := range lines {
		if !strings.Contains(view, line) {
			t.Fatalf("expanded input missing %q:\n%s", line, view)
		}
	}
	if model.textarea.Height() < len(lines) {
		t.Fatalf("textarea height = %d, want at least %d", model.textarea.Height(), len(lines))
	}
}

func TestModelExpandedInputDoesNotHideTranscriptHeader(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Welcome: WelcomeInfo{Version: "test-version", Model: "gpt-test"},
		Run: func(ctx context.Context, prompt string) (string, error) {
			return "answer", nil
		},
	})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 18})
	model = updated.(Model)
	model.transcriptPrintedHeader = false
	model.transcriptPrintedCount = 0
	model.messages = append(model.messages, message{role: "assistant", content: "answer"})
	model.setTextareaValue(strings.Join([]string{"line one", "line two", "line three", "line four"}, "\n"))
	view := model.View()
	transcript := renderTranscriptForTest(model)
	if !strings.Contains(transcript, "niuma") || !strings.Contains(transcript, "gpt-test") || !strings.Contains(transcript, "Session") {
		t.Fatalf("expanded input should keep header in transcript:\n%s", transcript)
	}
	if strings.Contains(model.viewport.View(), "██████╗  ██████╗") {
		t.Fatalf("header should not be inside scrollable viewport:\n%s", model.viewport.View())
	}
	for _, line := range []string{"line one", "line four"} {
		if !strings.Contains(view, line) {
			t.Fatalf("expanded input missing %q:\n%s", line, view)
		}
	}
}

func TestModelRendersStartupWelcome(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Welcome: WelcomeInfo{
			Version:        "test-version",
			Model:          "gpt-test",
			Provider:       "custom/openai-compatible",
			ContextLength:  251000,
			CWD:            "/workspace/project",
			SessionID:      "11111111-1111-4111-8111-111111111111",
			PermissionMode: "ask",
			Sandbox:        "on/seccomp",
			ToolSummary:    "27+mcp",
			MCPServers:     2,
		},
		Run: func(ctx context.Context, prompt string) (string, error) {
			return "answer", nil
		},
	})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 28})
	model = updated.(Model)
	model.transcriptPrintedHeader = false
	model.transcriptPrintedCount = 0
	view := renderTranscriptForTest(model)
	firstLine := strings.SplitN(view, "\n", 2)[0]
	if strings.TrimSpace(firstLine) != "" {
		t.Fatalf("welcome should start with top padding:\n%s", view)
	}
	for _, want := range []string{"niuma test-version", "Quick start", "Session", "╭────────────╮", "●      ●", "gpt-test", "工作区 /workspace/project", "ready · tools 27+mcp · sandbox on/seccomp", "id 11111111-1111-4111-8111-111111111111"} {
		if !strings.Contains(view, want) {
			t.Fatalf("welcome missing %q:\n%s", want, view)
		}
	}
	for _, notWant := range []string{"Welcome back!", "custom/openai-compatible", "Runtime", "ctx 251k", "mcp 2", "permissions ask"} {
		if strings.Contains(view, notWant) {
			t.Fatalf("welcome should keep startup card concise and omit %q:\n%s", notWant, view)
		}
	}
	if strings.Contains(view, "██████") {
		t.Fatalf("welcome should keep product header compact without large wordmark:\n%s", view)
	}
	if strings.Count(view, "niuma test-version") != 1 {
		t.Fatalf("welcome should avoid duplicate product title:\n%s", view)
	}
	if !strings.Contains(view, "\n  ╭") {
		t.Fatalf("welcome should render as an indented product card:\n%s", view)
	}
	if lines := strings.Split(strings.TrimSpace(view), "\n"); len(lines) > 16 {
		t.Fatalf("welcome header should stay within product first-screen budget, got %d lines:\n%s", len(lines), view)
	}
}

func TestModelRendersCompactWelcomeSessionID(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Welcome: WelcomeInfo{
			Model:     "gpt-test",
			CWD:       "/workspace/project",
			SessionID: "11111111-1111-4111-8111-111111111111",
		},
	})
	model.width = 80
	view := stripANSI(model.headerView(true))
	for _, want := range []string{"工作区    /workspace/project", "会话     11111111-1111-4111-8111-111111111111"} {
		if !strings.Contains(view, want) {
			t.Fatalf("compact welcome missing %q:\n%s", want, view)
		}
	}
	if strings.Index(view, "工作区") > strings.Index(view, "会话") {
		t.Fatalf("compact session should render after workspace:\n%s", view)
	}
}

func TestProductWelcomeStateKeepsNewSessionReady(t *testing.T) {
	if got := productWelcomeState(WelcomeInfo{SessionStatus: "new"}); got != "ready" {
		t.Fatalf("new session state = %q, want ready", got)
	}
	if got := productWelcomeState(WelcomeInfo{SessionStatus: "Compacted current session"}); got != "active" {
		t.Fatalf("active session state = %q, want active", got)
	}
	if got := productWelcomeState(WelcomeInfo{Resume: "sess-1", SessionStatus: "new"}); got != "resume" {
		t.Fatalf("resume session state = %q, want resume", got)
	}
}

func TestProductWelcomeContextLinesFitContentWidth(t *testing.T) {
	info := WelcomeInfo{
		Model:    "deterministic-tui-harness",
		Provider: "local/fake",
		CWD:      "/Users/example/GolandProjects/anything-ai",
	}
	lines := []string{
		productWelcomeModelLine(info, 27),
		productWelcomeWorkspaceLine(info, 27),
	}
	for _, line := range lines {
		if got := runewidth.StringWidth(line); got > 27 {
			t.Fatalf("welcome context line width=%d > 27: %q", got, line)
		}
	}
}

func TestModelCompactWelcomeWrapsSessionIDWithoutTruncation(t *testing.T) {
	sessionID := "b692c129-c693-4afe-a74e-4b499bc0ebe9"
	model := NewModel(context.Background(), Options{
		Welcome: WelcomeInfo{
			Model:     "gpt-test",
			CWD:       "/workspace/project",
			SessionID: sessionID,
		},
	})
	model.width = 40
	view := stripANSI(model.headerView(true))
	if strings.Contains(view, "...") {
		t.Fatalf("compact welcome should wrap long fields instead of truncating:\n%s", view)
	}
	for _, want := range []string{"会话", "b692c129-c693-4afe", "4b499bc0ebe9"} {
		if !strings.Contains(view, want) {
			t.Fatalf("compact welcome missing wrapped session id part %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "project\n工作区") {
		t.Fatalf("compact title should not wrap workspace onto a lonely title line:\n%s", view)
	}
	for _, line := range strings.Split(view, "\n") {
		if got := runewidth.StringWidth(line); got > model.width {
			t.Fatalf("compact welcome line width=%d > %d:\n%s\nfull view:\n%s", got, model.width, line, view)
		}
	}
}

func TestModelWelcomeShowsPermissionSwitchMode(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Welcome: WelcomeInfo{PermissionMode: "ask"},
		SwitchPermissionMode: func(string) (string, error) {
			return "allow", nil
		},
	})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 28})
	model = updated.(Model)
	view := stripANSI(model.View())
	if !strings.Contains(view, "shift+tab perm=ask") {
		t.Fatalf("welcome controls should show current permission mode:\n%s", view)
	}
	if !strings.Contains(view, "权限 ask  shift+tab 切换权限模式") {
		t.Fatalf("welcome permission strip should show current permission mode:\n%s", view)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	model = updated.(Model)
	view = stripANSI(model.View())
	if !strings.Contains(view, "shift+tab perm=allow") {
		t.Fatalf("welcome controls should update permission mode after shift+tab:\n%s", view)
	}
	if !strings.Contains(view, ">> 危险权限 allow  shift+tab 切换权限模式") {
		t.Fatalf("welcome permission strip should update after shift+tab:\n%s", view)
	}
	viewport := stripANSI(model.viewport.View())
	if !strings.Contains(viewport, ">> 危险权限 allow  shift+tab 切换权限模式") {
		t.Fatalf("welcome viewport should update permission strip after shift+tab:\n%s", viewport)
	}
	if strings.Contains(viewport, "权限 ask  shift+tab 切换权限模式") {
		t.Fatalf("welcome viewport should not keep stale ask permission strip after shift+tab:\n%s", viewport)
	}
}

func TestProductWelcomeDangerMascotBlinks(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Welcome: WelcomeInfo{Model: "gpt-test", PermissionMode: "allow"},
		Run:     func(ctx context.Context, p string) (string, error) { return "", nil },
	})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 28})
	model = updated.(Model)

	model.now = func() time.Time { return time.UnixMilli(0) } // ◆ frame
	on := stripANSI(model.headerView(true))
	if !strings.Contains(on, "│  ◆      ◆  │") {
		t.Fatalf("danger mascot should show ◆ eyes on the lit frame:\n%s", on)
	}
	model.now = func() time.Time { return time.UnixMilli(500) } // ◇ frame
	off := stripANSI(model.headerView(true))
	if !strings.Contains(off, "│  ◇      ◇  │") {
		t.Fatalf("danger mascot should show ◇ eyes on the dim frame:\n%s", off)
	}
}

func TestWelcomeMascotAnimatesOnlyInDanger(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Welcome: WelcomeInfo{Model: "gpt-test", PermissionMode: "ask"},
		Run:     func(ctx context.Context, p string) (string, error) { return "", nil },
	})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 28})
	model = updated.(Model)

	if model.shouldAnimateMascot() {
		t.Fatal("default (ask) welcome must not animate the mascot")
	}
	if cmd := model.startMascotTick(); cmd != nil || model.mascotTicking {
		t.Fatal("startMascotTick must be a no-op outside danger mode")
	}

	model.welcome.PermissionMode = "allow"
	if !model.shouldAnimateMascot() {
		t.Fatal("danger (allow) welcome should animate the mascot")
	}
	if cmd := model.startMascotTick(); cmd == nil || !model.mascotTicking {
		t.Fatal("startMascotTick should start the blink loop in danger mode")
	}
	// Already running: must not schedule a second overlapping loop.
	if cmd := model.startMascotTick(); cmd != nil {
		t.Fatal("startMascotTick must not start a second loop while one is running")
	}
}

func TestProductWelcomeCapsVeryWideCardWidth(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Welcome: WelcomeInfo{
			Model:          "gpt-test",
			CWD:            "/workspace/project",
			SessionID:      "11111111-1111-4111-8111-111111111111",
			PermissionMode: "ask",
			Sandbox:        "off",
			ToolSummary:    "31",
		},
	})
	model.width = 190
	view := stripANSI(model.headerView(true))
	maxWidth := 0
	for _, line := range strings.Split(view, "\n") {
		maxWidth = max(maxWidth, runewidth.StringWidth(line))
	}
	if maxWidth > 134 {
		t.Fatalf("wide welcome should cap around 70%% terminal width, got max line width %d:\n%s", maxWidth, view)
	}
	if maxWidth < 100 {
		t.Fatalf("wide welcome should remain readable, got max line width %d:\n%s", maxWidth, view)
	}
}

func TestModelUsesCompactViewportForWelcome(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Welcome: WelcomeInfo{
			Version:        "test-version",
			Model:          "gpt-test",
			Provider:       "custom/openai-compatible",
			ContextLength:  251000,
			CWD:            "/workspace/project",
			PermissionMode: "ask",
			Sandbox:        "off",
			ToolSummary:    "27",
		},
		Run: func(ctx context.Context, prompt string) (string, error) {
			return "answer", nil
		},
	})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 160, Height: 60})
	model = updated.(Model)
	if model.viewport.Height >= model.normalViewportHeight() {
		t.Fatalf("welcome viewport height = %d, normal = %d", model.viewport.Height, model.normalViewportHeight())
	}

	model.messages = append(model.messages, message{role: "user", content: "hello"})
	model.refreshViewport()
	if model.viewport.Height >= model.normalViewportHeight() {
		t.Fatalf("short conversation viewport height = %d, normal = %d", model.viewport.Height, model.normalViewportHeight())
	}
}

func TestModelRendersWelcomeWithInitialStatus(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Welcome:         WelcomeInfo{Version: "test-version", Model: "gpt-test"},
		InitialMessages: []InitialMessage{{Role: "status", Content: "Recovered interrupted transcript state."}},
		Run: func(ctx context.Context, prompt string) (string, error) {
			return "answer", nil
		},
	})
	view := renderTranscriptForTest(model)
	if !strings.Contains(view, "test-version") || !strings.Contains(view, "Recovered interrupted transcript state.") {
		t.Fatalf("transcript missing welcome or initial status:\n%s", view)
	}
}

func TestModelInitialTranscriptFlushKeepsRecapCompactInLiveViewport(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Welcome: WelcomeInfo{Version: "test-version", Model: "gpt-test", Resume: "sess-1"},
		InitialMessages: []InitialMessage{
			{Role: "status", Content: "Resumed sess-1."},
			{Role: "recap", Content: "本次会话目标：验证 recap spacing"},
		},
		Run: func(ctx context.Context, prompt string) (string, error) {
			return "answer", nil
		},
	})
	updated, cmd := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected initial transcript flush command")
	}
	if !strings.Contains(model.viewport.View(), "验证 recap spacing") {
		t.Fatalf("initial recap should remain as compact live context:\n%s", model.viewport.View())
	}
	if model.viewport.Height > lineCount(model.viewport.View())+2 {
		t.Fatalf("initial recap viewport should stay compact: height=%d view=%q", model.viewport.Height, model.viewport.View())
	}
}

func TestModelKeepsHeaderInTranscriptAfterConversationStarts(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Welcome: WelcomeInfo{Version: "test-version", Model: "gpt-test"},
		Run: func(ctx context.Context, prompt string) (string, error) {
			return "answer", nil
		},
	})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model = updated.(Model)
	model.transcriptPrintedHeader = false
	model.transcriptPrintedCount = 0
	model.messages = append(model.messages, message{role: "user", content: "hello"})
	model.refreshViewport()
	view := renderTranscriptForTest(model)
	if !strings.Contains(view, "niuma") || !strings.Contains(view, "gpt-test") || !strings.Contains(view, "Session") {
		t.Fatalf("transcript header should remain after conversation starts:\n%s", view)
	}
	if strings.Contains(model.viewport.View(), "██████") {
		t.Fatalf("header should not live inside scrollable history:\n%s", model.viewport.View())
	}
	if !strings.Contains(view, "› hello") {
		t.Fatalf("conversation view missing prompt:\n%s", view)
	}
}

func TestModelConversationLayoutHasInputRulesAndResponseMeta(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Welcome: WelcomeInfo{Version: "test-version", Model: "gpt-test", PermissionMode: "bypass"},
		RunResult: func(ctx context.Context, prompt string) (QueryResult, error) {
			return QueryResult{
				Response:   "answer",
				Model:      "gpt-test",
				StopReason: "end_turn",
				Turns:      1,
				Usage:      Usage{InputTokens: 10, OutputTokens: 3, CacheCreationInputTokens: 4, CacheCreationEphemeral1hInputTokens: 5, CacheReadInputTokens: 6},
				Context:    RuntimeContext{MaxTurns: 10, ToolCount: 27},
			}, nil
		},
	})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	model = updated.(Model)
	model.now = func() time.Time { return time.Date(2027, 10, 1, 14, 9, 0, 0, time.Local) }
	model.textarea.SetValue("hello")
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected command")
	}
	model = runTestCommand(t, model, cmd)
	transcript := renderTranscriptForTest(model)
	view := model.View()
	for _, want := range []string{"niuma", "› hello", "answer", "* Responded", "time=2027年10月1号 下午2点9分", "cache create/read=9/6 turn_hit=60.0%"} {
		if !strings.Contains(transcript, want) {
			t.Fatalf("conversation transcript missing %q:\n%s", want, transcript)
		}
	}
	if strings.Contains(transcript, "stop=end_turn") {
		t.Fatalf("conversation transcript should suppress normal stop reason:\n%s", transcript)
	}
	for _, want := range []string{"turns=1/10", "tokens in/out=10/3", "cache create/read=9/6 session_hit=60.0%", "─", "status  Ready", "controls"} {
		if !strings.Contains(view, want) {
			t.Fatalf("conversation layout missing %q:\n%s", want, view)
		}
	}
	for _, notWant := range []string{"model=gpt-test", "stop=end_turn", "safety  permissions=bypass active"} {
		if strings.Contains(view, notWant) {
			t.Fatalf("quiet conversation layout should not repeat %q:\n%s", notWant, view)
		}
	}
	if strings.Count(transcript, "● niuma") != 1 {
		t.Fatalf("assistant marker should render once for one response:\n%s", transcript)
	}
}

func TestModelKeepsSubmittedPromptVisibleUntilTranscriptPrints(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Welcome: WelcomeInfo{Version: "test-version", Model: "gpt-test"},
		Run: func(ctx context.Context, prompt string) (string, error) {
			return "answer", nil
		},
	})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model = updated.(Model)
	model.textarea.SetValue("hello")
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected transcript flush and run command")
	}
	view := stripANSI(model.View())
	if !strings.Contains(view, "› hello") {
		t.Fatalf("submitted prompt should stay visible until transcript print commits:\n%s", view)
	}
	if !strings.Contains(view, "niuma is preparing context") {
		t.Fatalf("submitted prompt frame should keep running status:\n%s", view)
	}
	if model.transcriptPrintedCount != 0 || model.transcriptPrintedSeq != 0 {
		t.Fatalf("transcript should not be marked printed before print command runs: count=%d seq=%d", model.transcriptPrintedCount, model.transcriptPrintedSeq)
	}
	if model.transcriptCommittingSeq == 0 {
		t.Fatal("transcript commit should be in flight to prevent duplicate flushes")
	}
}

func TestModelDoesNotQueueDuplicateHeaderWhileTranscriptPrintIsInFlight(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Welcome: WelcomeInfo{Version: "test-version", Model: "gpt-test"},
		Run: func(ctx context.Context, prompt string) (string, error) {
			return "answer", nil
		},
	})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model = updated.(Model)
	model.textarea.SetValue("hello")
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected first transcript flush command")
	}
	if !model.transcriptCommittingHeader {
		t.Fatal("first transcript flush should mark header as in flight")
	}
	model.busy = false
	model.appendDisplayMessage("assistant", "answer", "")
	model.markDisplayTurnDone()
	second := model.pendingTranscriptBlocks()
	joined := strings.Join(second, "\n\n")
	if strings.Contains(joined, "niuma test-version") {
		t.Fatalf("second flush should not queue a duplicate transcript header:\n%s", joined)
	}
	if strings.Contains(joined, "› hello") {
		t.Fatalf("second flush should not requeue the submitted prompt while first flush is in flight:\n%s", joined)
	}
	if !strings.Contains(joined, "answer") {
		t.Fatalf("second flush should still queue the assistant response:\n%s", joined)
	}
}

func TestModelSilentBackgroundUpdateDoesNotFlushWelcomeHeader(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Welcome: WelcomeInfo{Version: "test-version", Model: "gpt-test"},
		Run: func(ctx context.Context, prompt string) (string, error) {
			return "answer", nil
		},
	})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model = updated.(Model)

	// A silent scheduler-events update lands at startup (matches the real
	// __scheduler_events__ bookkeeping entry). It appends no visible message,
	// so it must not trigger a transcript flush; otherwise the welcome header
	// gets committed to scrollback while the live welcome still renders it,
	// producing two identical headers.
	updated, _ = model.Update(backgroundPollMsg{updates: []BackgroundUpdate{
		{ID: "__scheduler_events__", LogSize: 3288, Silent: true},
	}})
	model = updated.(Model)

	// transcriptPrintedHeader / transcriptCommittingHeader flip only when a
	// transcript flush runs; both staying false proves no flush was scheduled,
	// so the welcome header is never tea.Println'd into scrollback.
	if model.transcriptPrintedHeader {
		t.Fatal("silent background update must not commit the welcome header to scrollback")
	}
	if model.transcriptCommittingHeader {
		t.Fatal("silent background update must not start a transcript flush")
	}
	if view := stripANSI(model.View()); strings.Count(view, "niuma test-version") != 1 {
		t.Fatalf("live welcome should remain the single header, got count=%d:\n%s", strings.Count(view, "niuma test-version"), view)
	}
}

func TestModelVisibleBackgroundUpdateStillFlushes(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Welcome: WelcomeInfo{Version: "test-version", Model: "gpt-test"},
		Run: func(ctx context.Context, prompt string) (string, error) {
			return "answer", nil
		},
	})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model = updated.(Model)

	updated, cmd := model.Update(backgroundPollMsg{updates: []BackgroundUpdate{
		{ID: "job-1", Status: "completed", LogTail: "done"},
	}})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("a visible background update should still schedule a transcript flush")
	}
	if !model.transcriptPrintedHeader {
		t.Fatal("a visible background update should flush the header into scrollback")
	}
}

func TestModelNaturalScrollbackMovesCompletedMessagesOutOfLiveView(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Welcome: WelcomeInfo{Version: "test-version", Model: "gpt-test"},
		Run: func(ctx context.Context, prompt string) (string, error) {
			return "answer", nil
		},
	})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model = updated.(Model)
	model.textarea.SetValue("hello")
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected command")
	}
	model = runTestCommand(t, model, cmd)
	transcript := renderTranscriptForTest(model)
	plainTranscript := stripANSI(transcript)
	for _, want := range []string{"niuma", "› hello", "answer"} {
		if !strings.Contains(plainTranscript, want) {
			t.Fatalf("transcript missing %q:\n%s", want, plainTranscript)
		}
	}
	userIndex := strings.Index(plainTranscript, "› hello")
	if userIndex < 0 {
		t.Fatalf("transcript missing user message:\n%s", plainTranscript)
	}
	assistantIndex := strings.Index(plainTranscript[userIndex:], "\n\n● niuma")
	if assistantIndex < 0 {
		t.Fatalf("user and assistant blocks should be separated by a blank line:\n%s", plainTranscript)
	}
	if !strings.HasSuffix(transcript, "\n") {
		t.Fatalf("transcript print should end with a newline gap:\n%q", transcript)
	}
	view := model.View()
	for _, notWant := range []string{"> hello", "answer", "██████╗  ██████╗"} {
		if strings.Contains(view, notWant) {
			t.Fatalf("completed transcript content should not stay in live view %q:\n%s", notWant, view)
		}
	}
	for _, notWant := range []string{"hello", "answer"} {
		if strings.Contains(model.viewport.View(), notWant) {
			t.Fatalf("viewport should not keep printed transcript content %q:\n%s", notWant, model.viewport.View())
		}
	}
}

func TestModelTurnCompleteDoesNotCreateLargeTranscriptGap(t *testing.T) {
	model := NewModel(context.Background(), Options{Run: func(context.Context, string) (string, error) { return "", nil }})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 72, Height: 24})
	model = updated.(Model)
	model.transcriptPrintedHeader = true
	model.transcriptPrintedCount = 0
	model.usage = usagePanel{
		Model:        "glm-5.1",
		Turns:        4,
		MaxTurns:     100,
		InputTokens:  172748,
		OutputTokens: 2435,
		CacheRead:    77824,
	}
	finalSentence := "TAIL_SENTENCE_MUST_STAY_VISIBLE"
	model.messages = append(model.messages, message{role: "assistant", content: strings.Join([]string{
		"孔龙，这个项目叫 Anything-AI，核心目的是系统性 AI 知识索引。",
		"具体来说：",
		"- 对抗碎片化信息",
		"- 消除 AI 焦虑",
		"- 提供实用指南",
		"- 双语知识库",
		"项目结构按学习阶段组织：",
		"- 0-start-here — AI认知入门",
		"- 2-choose-tools — 工具选择矩阵",
		"- 5-skills — AI技能包",
		"- roles/ — 各行业角色案例",
		finalSentence,
	}, "\n")})

	blocks := model.pendingTranscriptBlocks()
	if len(blocks) != 1 {
		t.Fatalf("pending blocks = %d, want 1: %#v", len(blocks), blocks)
	}
	guard := model.transcriptBottomGuardLinesFor(transcriptFlushTurnComplete)
	output := formatTranscriptOutput(blocks, guard)
	cleanOutput := stripANSI(output)
	for _, want := range []string{"Anything-AI", "5-skills", "roles/", "TAIL_SENTENCE_MUST_STAY_VISIBLE"} {
		if !strings.Contains(cleanOutput, want) {
			t.Fatalf("transcript output missing %q:\n%s", want, cleanOutput)
		}
	}
	trailingNewlines := len(output) - len(strings.TrimRight(output, "\n"))
	if trailingNewlines < guard+1 {
		t.Fatalf("trailing newlines = %d, want at least transcript newline + guard %d:\n%q", trailingNewlines, guard, output)
	}
	if guard != transcriptBottomGuardMinSpacer {
		t.Fatalf("turn-complete guard = %d, want compact spacer %d", guard, transcriptBottomGuardMinSpacer)
	}
	lines := strings.Split(cleanOutput, "\n")
	tailLine := -1
	for i, line := range lines {
		if strings.Contains(line, finalSentence) {
			tailLine = i
			break
		}
	}
	if tailLine < 0 {
		t.Fatalf("missing final tail line:\n%s", output)
	}
	linesAfterTail := len(lines) - tailLine - 1
	if linesAfterTail > 2 {
		t.Fatalf("turn-complete transcript has large trailing gap after tail: lines=%d\n%s", linesAfterTail, output)
	}
}

func TestModelUserSubmitDoesNotCreateLargeTranscriptGap(t *testing.T) {
	model := NewModel(context.Background(), Options{Run: func(context.Context, string) (string, error) { return "", nil }})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 56, Height: 24})
	model = updated.(Model)
	model.transcriptPrintedHeader = true
	model.usage = usagePanel{
		Model:        "glm-5.1",
		Turns:        65,
		MaxTurns:     100,
		InputTokens:  8317668,
		OutputTokens: 96155,
		CacheCreate:  3834368,
		ToolCalls:    77,
		ToolErrors:   17,
		LastTool:     "Bash",
	}
	model.messages = append(model.messages, message{role: "user", content: "好的谢谢，你可以休息了"})

	guard := model.transcriptBottomGuardLinesFor(transcriptFlushUserSubmit)
	output := formatTranscriptOutput(model.pendingTranscriptBlocks(), guard)
	trailingNewlines := len(output) - len(strings.TrimRight(output, "\n"))
	if guard != transcriptBottomGuardMinSpacer {
		t.Fatalf("user-submit guard = %d, want compact spacer %d", guard, transcriptBottomGuardMinSpacer)
	}
	if trailingNewlines > 2 {
		t.Fatalf("user-submit transcript has large trailing gap: newlines=%d output=%q", trailingNewlines, output)
	}
}

func TestModelSubmittingAfterCompletedAssistantDoesNotReuseLiveHistory(t *testing.T) {
	oldAnswer := "previous assistant answer should stay only in scrollback"
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	model = updated.(Model)
	model.transcriptPrintedHeader = true
	model.messages = []message{{role: "assistant", content: oldAnswer}}
	model.transcriptPrintedCount = len(model.messages)
	model.usage = usagePanel{Model: "glm-5.1", Turns: 1, InputTokens: 100, OutputTokens: 10}
	model.refreshViewport()

	model.textarea.SetValue("next prompt")
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected submit command")
	}
	view := stripANSI(model.View())
	viewport := stripANSI(model.viewport.View())
	if strings.Contains(view, oldAnswer) || strings.Contains(viewport, oldAnswer) {
		t.Fatalf("completed assistant must not be reused in live layer:\nview=%s\nviewport=%s", view, viewport)
	}
	if !strings.Contains(view, "niuma is preparing context") {
		t.Fatalf("submit should show current running state, not old history:\n%s", view)
	}
	output := formatTranscriptOutput(model.pendingTranscriptBlocks(), model.transcriptBottomGuardLinesFor(transcriptFlushUserSubmit))
	trailingNewlines := len(output) - len(strings.TrimRight(output, "\n"))
	if trailingNewlines > 2 {
		t.Fatalf("next prompt transcript has large trailing gap: newlines=%d output=%q", trailingNewlines, output)
	}
}

func TestModelCompletedAssistantDoesNotReturnToLiveViewAfterRecap(t *testing.T) {
	finalRisk := "核心风险：广度铺开了，深度和可持续性是最大挑战。"
	finalAdvice := "一句话建议：先挑3-5个最有价值的方向做深，再逐步扩展。"
	answer := strings.Join([]string{
		"## 优点",
		"结构清晰，工程化成熟。",
		"## 缺点",
		"内容深度不均匀，反馈闭环不足。",
		"## 总结",
		finalRisk,
		finalAdvice,
	}, "\n\n")
	model := NewModel(context.Background(), Options{Run: func(context.Context, string) (string, error) { return "", nil }})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 18})
	model = updated.(Model)
	updated, _ = model.Update(responseMsg{result: QueryResult{
		Response: answer,
		Model:    "glm-5.1",
		Turns:    3,
		Usage: Usage{
			InputTokens:  127190,
			OutputTokens: 1644,
		},
	}})
	model = updated.(Model)
	updated, _ = model.Update(awayRecapMsg{generation: model.recapGeneration, text: "本轮已经完成项目评价，下一步可以拆分 CLAUDE.md。"})
	model = updated.(Model)

	view := stripANSI(model.View())
	viewport := stripANSI(model.viewport.View())
	for _, want := range []string{finalRisk, finalAdvice} {
		transcript := renderTranscriptForTest(model)
		if !strings.Contains(transcript, want) {
			t.Fatalf("completed assistant should remain in transcript, missing %q:\n%s", want, transcript)
		}
		if strings.Contains(view, want) {
			t.Fatalf("completed assistant should not return to live view, found %q:\n%s", want, view)
		}
		if strings.Contains(viewport, want) {
			t.Fatalf("viewport should not keep completed assistant after recap, found %q:\n%s", want, viewport)
		}
	}
	if !strings.Contains(view, "※recap:") || !strings.Contains(view, "本轮已经完成项目评价") {
		t.Fatalf("recap should remain visible without returning completed assistant:\n%s", view)
	}
}

func TestModelCompletedAssistantStaysOutOfLiveViewWithCJKSoftWrappedLines(t *testing.T) {
	finalSentence := "下一步的重点不是继续扩骨架，而是把已有骨架填实、把用户从入口到实战的路径打通。"
	answer := strings.Join([]string{
		"## 一句话总结",
		"**骨架和工程化是顶级水平，内容填充和用户体验是当前瓶颈。** 项目像一座规划很好的城市——路网、分区、基础设施都到位了，但有些街区还是空地，有些商铺还没开业。" + finalSentence,
	}, "\n\n")
	model := NewModel(context.Background(), Options{Run: func(context.Context, string) (string, error) { return "", nil }})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 96, Height: 13})
	model = updated.(Model)
	updated, _ = model.Update(responseMsg{result: QueryResult{
		Response: answer,
		Model:    "glm-5.1",
		Turns:    3,
		Usage: Usage{
			InputTokens:              108178,
			OutputTokens:             1579,
			CacheReadInputTokens:     42240,
			CacheCreationInputTokens: 0,
		},
		ToolCalls: []ToolCall{{Name: "Bash"}},
		Context:   RuntimeContext{ToolCount: 32},
	}})
	model = updated.(Model)
	updated, _ = model.Update(awayRecapMsg{generation: model.recapGeneration, text: "刚完成对 anything-ai 项目的全面优缺点评估。"})
	model = updated.(Model)

	view := stripANSI(model.View())
	viewport := stripANSI(model.viewport.View())
	transcript := renderTranscriptForTest(model)
	if !strings.Contains(transcript, "路径打通。") {
		t.Fatalf("completed assistant transcript should contain full CJK final sentence:\n%s", transcript)
	}
	if strings.Contains(view, "路径打通。") || strings.Contains(viewport, "路径打通。") {
		t.Fatalf("completed assistant should not occupy live view after flush:\nview=%s\nviewport=%s", view, viewport)
	}
	assertViewFitsTerminal(t, model)
}

func TestModelPrintedAssistantDoesNotAnchorLiveViewport(t *testing.T) {
	lines := []string{
		"first line should scroll away",
		"second line should scroll away",
		"third line should scroll away",
		"fourth line should scroll away",
		"fifth line should scroll away",
		"sixth line should scroll away",
		"seventh line should scroll away",
		"eighth line should scroll away",
		"final tail sentence must stay visible",
	}
	model := NewModel(context.Background(), Options{Run: func(context.Context, string) (string, error) { return "", nil }})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	model = updated.(Model)
	model.messages = []message{
		{role: "assistant", content: strings.Join(lines, "\n")},
		{role: "recap", content: "internal recap should not be the final screen"},
	}
	model.transcriptPrintedHeader = true
	model.transcriptPrintedCount = len(model.messages)
	model.stickToBottom = false
	model.viewport.YOffset = 0
	model.refreshViewport()

	viewport := stripANSI(model.viewport.View())
	for _, notWant := range []string{"final tail sentence must stay visible", "first line should scroll away"} {
		if strings.Contains(viewport, notWant) {
			t.Fatalf("printed assistant should not anchor live viewport, found %q:\n%s", notWant, viewport)
		}
	}
}

func TestModelTranscriptBottomGuardDoesNotPolluteMessageContent(t *testing.T) {
	model := NewModel(context.Background(), Options{Run: func(context.Context, string) (string, error) { return "", nil }})
	model.transcriptPrintedHeader = true
	original := "final answer"
	model.messages = append(model.messages, message{role: "assistant", content: original})

	_ = formatTranscriptOutput(model.pendingTranscriptBlocks(), model.transcriptBottomGuardLinesFor(transcriptFlushTurnComplete))
	if got := model.messages[0].content; got != original {
		t.Fatalf("message content mutated by transcript output = %q, want %q", got, original)
	}
}

func TestModelBottomGuardDoesNotScaleWithWrappedUsage(t *testing.T) {
	model := NewModel(context.Background(), Options{Run: func(context.Context, string) (string, error) { return "", nil }})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 56, Height: 24})
	model = updated.(Model)
	model.usage = usagePanel{
		Model:        "glm-5.1",
		Turns:        14,
		MaxTurns:     100,
		InputTokens:  227226,
		OutputTokens: 3202,
		CacheCreate:  12000,
		CacheRead:    88000,
		ToolCalls:    15,
		ToolCount:    27,
		LastTool:     "Read",
		StopReason:   "end_turn",
		SessionID:    "8719a008-b2a8-43bc-8f51-418b0c148f5d",
		CWD:          "/Users/example/GolandProjects/niuma",
	}

	usageLines := visualLineCount(model.usageView(), model.contentWidth())
	if usageLines < 2 {
		t.Fatalf("usage should wrap in narrow layout:\n%s", model.usageView())
	}
	got := model.transcriptBottomGuardLinesFor(transcriptFlushTurnComplete)
	if got != transcriptBottomGuardMinSpacer {
		t.Fatalf("bottom guard lines = %d, want compact spacer %d", got, transcriptBottomGuardMinSpacer)
	}
}

func TestModelBottomChromeWrapsLongUsageAndControls(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Welcome: WelcomeInfo{PermissionMode: "allow"},
		Run:     func(context.Context, string) (string, error) { return "", nil },
		SwitchPermissionMode: func(string) (string, error) {
			return "ask", nil
		},
	})
	model.width = 80
	model.messages = []message{{role: "user", content: "already chatting"}}
	model.transcriptPrintedHeader = true
	model.transcriptPrintedCount = len(model.messages)
	model.usage = usagePanel{
		Turns:         5,
		MaxTurns:      100,
		InputTokens:   187977,
		OutputTokens:  1404,
		LastInput:     47363,
		ContextWindow: 200000,
		CacheRead:     78208,
		ToolCalls:     11,
		ToolCount:     32,
		ToolErrors:    3,
		LastTool:      "Bash",
	}

	usage := stripANSI(model.usageView())
	controls := stripANSI(model.modeHintView("Ready"))
	for name, view := range map[string]string{"usage": usage, "controls": controls} {
		if strings.Contains(view, "...") {
			t.Fatalf("%s should wrap instead of truncating:\n%s", name, view)
		}
		for _, line := range strings.Split(view, "\n") {
			if got := runewidth.StringWidth(line); got > model.width {
				t.Fatalf("%s line width=%d > %d:\n%s\nfull view:\n%s", name, got, model.width, line, view)
			}
		}
	}
	for _, want := range []string{"执行了 11 次操作 · 3 次未完成 · 最近使用 运行命令", "cache create/read=0/78208 session_hit=41.6%", "ctrl+u clear", "/exit to quit", "ctrl+v paste image"} {
		if !strings.Contains(usage+"\n"+controls, want) {
			t.Fatalf("bottom chrome missing %q:\nusage:\n%s\ncontrols:\n%s", want, usage, controls)
		}
	}
}

func TestVisualLineCountCountsCJKSoftWraps(t *testing.T) {
	text := "下一步的重点不是继续扩骨架，而是把已有骨架填实、把用户从入口到实战的路径打通。"
	if logical := lineCount(text); logical != 1 {
		t.Fatalf("logical line count = %d, want 1", logical)
	}
	if visual := visualLineCount(text, 20); visual <= 1 {
		t.Fatalf("visual line count should include CJK soft wraps, got %d", visual)
	}
	styled := statusStyle.Render(text)
	if visual := visualLineCount(styled, 20); visual <= 1 {
		t.Fatalf("visual line count should ignore ANSI while counting wraps, got %d", visual)
	}
}

func TestModelStreamingTranscriptFlushesOnlyWhenTurnCompletes(t *testing.T) {
	events := make(chan StreamEvent, 2)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	model.streamingActive = true
	model.transcriptPrintedHeader = true
	model.transcriptPrintedCount = 0
	updated, cmd := model.Update(streamEventMsg{event: StreamEvent{Type: StreamText, Text: "partial"}, ch: events})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected wait command")
	}
	if blocks := model.pendingTranscriptBlocks(); len(blocks) != 0 {
		t.Fatalf("partial streaming text should not be ready for transcript: %#v", blocks)
	}
	if !strings.Contains(model.View(), "partial") {
		t.Fatalf("partial streaming text should remain live:\n%s", model.View())
	}
	updated, cmd = model.Update(streamClosedMsg{})
	model = updated.(Model)
	if cmd != nil || model.transcriptPrintedCount != 0 {
		t.Fatalf("stream close should not finalize transcript before response: cmd=%v count=%d", cmd, model.transcriptPrintedCount)
	}
	updated, cmd = model.Update(responseMsg{})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected transcript print command after response completes stream turn")
	}
	if model.transcriptPrintedCount != len(model.messages) {
		t.Fatalf("streaming transcript count=%d messages=%d", model.transcriptPrintedCount, len(model.messages))
	}
}

func TestModelStreamingCompletionSyncsFinalMarkdownTable(t *testing.T) {
	events := make(chan StreamEvent, 2)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	model = updated.(Model)
	model.busy = true
	model.streamingActive = true

	flattened := "目前包含 39 个技能，分 5 个模块： |模块 |技能数 |覆盖场景 ||-----|-----|-----|"
	updated, _ = model.Update(streamEventMsg{event: StreamEvent{Type: StreamText, Text: flattened}, ch: events})
	model = updated.(Model)

	final := strings.Join([]string{
		"目前包含 **39 个技能**，分 5 个模块：",
		"",
		"| 模块 | 技能数 | 覆盖场景 |",
		"|------|--------|---------|",
		"| 需求洞察 | 9个 | 调研、头脑风暴、优先级排序、MVP拆解 |",
		"| 方案设计 | 7个 | PRD/BRD/MRD文档、原型、技术方案 |",
	}, "\n")
	updated, cmd := model.Update(responseMsg{result: QueryResult{Response: final}})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected transcript flush after final response")
	}
	if len(model.messages) == 0 || model.messages[len(model.messages)-1].content != final {
		t.Fatalf("latest assistant message was not synced to final response:\n%#v", model.messages)
	}
	seg := latestDisplaySegmentForTest(t, model, displaySegmentAssistantText)
	if seg.content != final {
		t.Fatalf("assistant display segment was not synced:\n%s", seg.content)
	}
	rendered := stripANSI(model.renderDisplaySegment(seg, true, false))
	for _, want := range []string{"┌", "│ 模块", "│ 需求洞察", "└"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("final markdown table did not render as table, missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "||-----") {
		t.Fatalf("flattened streaming table leaked into final rendering:\n%s", rendered)
	}
}

func TestModelStreamingMarkdownTableHidesIncompleteRows(t *testing.T) {
	events := make(chan StreamEvent, 8)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	model = updated.(Model)
	model.busy = true
	model.streamingActive = true

	first := strings.Join([]string{
		"这是 **super-pm** 的模块概览：",
		"",
		"| 模块 | 技能数 | 覆盖 |",
		"|---|---|---|",
	}, "\n")
	updated, _ = model.Update(streamEventMsg{event: StreamEvent{Type: StreamText, Text: first}, ch: events})
	model = updated.(Model)
	live := stripANSI(model.liveTranscriptView())
	if strings.Contains(live, "| 模块 |") || strings.Contains(live, "┌") {
		t.Fatalf("streaming table header should wait for a body row:\n%s", live)
	}
	if !strings.Contains(live, "模块概览") {
		t.Fatalf("non-table streaming text should remain visible:\n%s", live)
	}

	updated, _ = model.Update(streamEventMsg{event: StreamEvent{Type: StreamText, Text: "\n| 需求洞察 | 9 个 | 调研、头脑风暴、MVP 等 |\n"}, ch: events})
	model = updated.(Model)
	live = stripANSI(model.liveTranscriptView())
	for _, want := range []string{"┌", "│ 模块", "│ 需求洞察", "└"} {
		if !strings.Contains(live, want) {
			t.Fatalf("streaming table with one complete row should render %q:\n%s", want, live)
		}
	}

	updated, _ = model.Update(streamEventMsg{event: StreamEvent{Type: StreamText, Text: "| 方案设计 | 8 个"}, ch: events})
	model = updated.(Model)
	live = stripANSI(model.liveTranscriptView())
	if strings.Contains(live, "方案设计") {
		t.Fatalf("partial streaming table row should be hidden until complete:\n%s", live)
	}

	updated, _ = model.Update(streamEventMsg{event: StreamEvent{Type: StreamText, Text: " | PRD、原型、技术对接 |\n"}, ch: events})
	model = updated.(Model)
	live = stripANSI(model.liveTranscriptView())
	if !strings.Contains(live, "方案设计") {
		t.Fatalf("completed streaming table row should become visible:\n%s", live)
	}
	if count := strings.Count(live, "niuma"); count != 1 {
		t.Fatalf("streaming table should stay in one assistant block, count=%d\n%s", count, live)
	}
}

func TestFormatChineseResponseTime(t *testing.T) {
	tests := []struct {
		name string
		at   time.Time
		want string
	}{
		{name: "midnight", at: time.Date(2027, 10, 1, 0, 9, 0, 0, time.Local), want: "2027年10月1号 上午12点9分"},
		{name: "morning", at: time.Date(2027, 10, 1, 9, 9, 0, 0, time.Local), want: "2027年10月1号 上午9点9分"},
		{name: "noon", at: time.Date(2027, 10, 1, 12, 9, 0, 0, time.Local), want: "2027年10月1号 下午12点9分"},
		{name: "afternoon", at: time.Date(2027, 10, 1, 14, 9, 0, 0, time.Local), want: "2027年10月1号 下午2点9分"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatChineseResponseTime(tt.at); got != tt.want {
				t.Fatalf("formatChineseResponseTime() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestModelHeaderAndLongHistoryStayInTranscript(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Welcome: WelcomeInfo{Version: "test-version", Model: "gpt-test"},
		Run: func(ctx context.Context, prompt string) (string, error) {
			return "answer", nil
		},
	})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 14})
	model = updated.(Model)
	model.transcriptPrintedHeader = false
	model.transcriptPrintedCount = 0
	for i := 0; i < 40; i++ {
		model.messages = append(model.messages, message{role: "assistant", content: "message " + strconv.Itoa(i)})
	}
	model.refreshViewportAtBottom()
	transcript := renderTranscriptForTest(model)
	if !strings.Contains(transcript, "niuma") || !strings.Contains(transcript, "gpt-test") || !strings.Contains(transcript, "Session") {
		t.Fatalf("header should stay in transcript with long history:\n%s", transcript)
	}
	if strings.Contains(model.viewport.View(), "██████╗  ██████╗") {
		t.Fatalf("header should not be part of scrollable history:\n%s", model.viewport.View())
	}
	if !strings.Contains(transcript, "message 39") {
		t.Fatalf("expected latest message in transcript:\n%s", transcript)
	}
}

func TestModelRendersGoClaudeAssistantLabel(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Run: func(ctx context.Context, prompt string) (string, error) {
			return "answer", nil
		},
	})
	model.messages = append(model.messages, message{role: "assistant", content: "hello"})
	model.refreshViewport()
	view := renderTranscriptForTest(model)
	if !strings.Contains(view, "niuma") {
		t.Fatalf("view missing niuma label:\n%s", view)
	}
	if strings.Contains(view, "\nClaude\n") {
		t.Fatalf("view still contains old assistant label:\n%s", view)
	}
}

func TestModelRendersAssistantMarkdown(t *testing.T) {
	model := NewModel(context.Background(), Options{
		RunResult: func(ctx context.Context, prompt string) (QueryResult, error) {
			return QueryResult{Response: strings.Join([]string{
				"## 搜索工具对比",
				"",
				"| 工具 | 状态 | 特点 |",
				"|---|---|---|",
				"| **anysearch** | ✅ 可用 | 多源并行 |",
				"| **WebSearch** | ❌ 超时 | DuckDuckGo 慢 |",
				"",
				"**结论：优先用 anysearch。**",
			}, "\n")}, nil
		},
	})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	model = updated.(Model)
	model.textarea.SetValue("search")
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected command")
	}
	model = runTestCommand(t, model, cmd)
	view := stripANSI(renderTranscriptForTest(model))
	for _, want := range []string{"搜索工具对比", "anysearch", "WebSearch", "结论：优先用 anysearch。"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing rendered markdown text %q:\n%s", want, view)
		}
	}
	for _, raw := range []string{"## 搜索工具对比", "**anysearch**", "**WebSearch**", "**结论", "|---|---|---|"} {
		if strings.Contains(view, raw) {
			t.Fatalf("view still contains raw markdown %q:\n%s", raw, view)
		}
	}
}

func TestRenderMarkdownCleansANSIOnlyBlankLines(t *testing.T) {
	dirty := "\x1b[38;5;253m   \x1b[0m\n正文\n\x1b[38;5;253m \t  \x1b[0m"
	cleaned := cleanANSIWhitespaceLines(dirty)
	if strings.Contains(cleaned, "\x1b[38;5;253m   ") || strings.Contains(cleaned, "\x1b[38;5;253m \t") {
		t.Fatalf("ANSI-only whitespace should be removed:\n%q", cleaned)
	}
	if !strings.Contains(cleaned, "正文") {
		t.Fatalf("visible content should be preserved:\n%q", cleaned)
	}
	if strings.TrimSpace(stripANSI(cleaned)) != "正文" {
		t.Fatalf("cleaned visible text mismatch:\n%q", cleaned)
	}
}

func TestModelRendersRichMarkdownInlineSpanStyles(t *testing.T) {
	rendered := renderMarkdownForWidth(strings.Join([]string{
		"## 文字颜色",
		"",
		`<span style="color:red">红色文字</span>`,
		`<span style="color:#FF6B6B;font-weight:bold">自定义红色</span>`,
		`<span style="color:orange;text-decoration:line-through">删除线橙色</span>`,
		`<span style="color:blue;font-style:italic">斜体蓝色</span>`,
		`<font color="green">绿色文字</font>`,
	}, "\n"), 100)
	plain := stripANSI(rendered)
	for _, want := range []string{"文字颜色", "红色文字", "自定义红色", "删除线橙色", "斜体蓝色", "绿色文字"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("rich markdown missing %q:\nrendered=%s\nplain=%s", want, rendered, plain)
		}
	}
	for _, raw := range []string{"<span", "</span>", "<font", "</font>", "style=", "color="} {
		if strings.Contains(plain, raw) {
			t.Fatalf("rich markdown leaked raw html %q:\nrendered=%s\nplain=%s", raw, rendered, plain)
		}
	}
	if !strings.Contains(rendered, "\x1b[") {
		t.Fatalf("rich markdown should include ANSI styling:\n%s", rendered)
	}
}

func TestModelRichMarkdownUnsupportedSpanStyleFallsBackToText(t *testing.T) {
	rendered := renderMarkdownForWidth(`<span style="font-size:48px">普通文字</span>`, 80)
	plain := stripANSI(rendered)
	if strings.Contains(plain, "<span") || strings.Contains(plain, "style=") || !strings.Contains(plain, "普通文字") {
		t.Fatalf("unsupported rich span should degrade to text:\nrendered=%s\nplain=%s", rendered, plain)
	}
}

func TestModelMainChatScrollKeysScrollViewport(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Run: func(ctx context.Context, prompt string) (string, error) {
			return "answer", nil
		},
	})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	model = updated.(Model)
	model.transcriptPrintedHeader = true
	model.transcriptPrintedCount = 0
	// Use a single long message so viewport content exceeds viewport height
	model.messages = append(model.messages, message{role: "assistant", content: strings.Repeat("line\n", 30)})
	model.refreshViewportAtBottom()
	startOffset := model.viewport.YOffset

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	model = updated.(Model)
	if model.viewport.YOffset >= startOffset {
		t.Fatalf("page up should scroll viewport up: start=%d now=%d", startOffset, model.viewport.YOffset)
	}
}

func TestModelMainChatMouseWheelScrollViewport(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Run: func(ctx context.Context, prompt string) (string, error) {
			return "answer", nil
		},
	})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	model = updated.(Model)
	model.transcriptPrintedHeader = true
	model.transcriptPrintedCount = 0
	model.messages = append(model.messages, message{role: "assistant", content: strings.Repeat("line\n", 30)})
	model.refreshViewportAtBottom()
	startOffset := model.viewport.YOffset

	updated, _ = model.Update(tea.MouseMsg{
		Type:   tea.MouseWheelUp,
		Button: tea.MouseButtonWheelUp,
		Action: tea.MouseActionPress,
	})
	model = updated.(Model)
	if model.viewport.YOffset >= startOffset {
		t.Fatalf("mouse wheel up should scroll viewport up: start=%d now=%d", startOffset, model.viewport.YOffset)
	}
}

func TestTUIDefaultKeepsMouseTrackingDisabledForNativeCopy(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Run: func(ctx context.Context, prompt string) (string, error) {
			return "answer", nil
		},
	})
	if model.mouseTracking {
		t.Fatal("mouse tracking should default off so terminal-native copy works")
	}
	if !strings.Contains(model.View(), "mouse=copy") {
		t.Fatalf("view should show copy-friendly mouse mode:\n%s", model.View())
	}
}

func TestModelCtrlOTogglesMouseTrackingMode(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Run: func(ctx context.Context, prompt string) (string, error) {
			return "answer", nil
		},
	})

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	model = updated.(Model)
	if !model.mouseTracking || cmd == nil {
		t.Fatalf("expected mouse tracking enabled with command: tracking=%v cmd=%v", model.mouseTracking, cmd)
	}
	if _, ok := cmd().(tea.Msg); !ok {
		t.Fatal("mouse enable command should return a Bubble Tea message")
	}
	if !strings.Contains(model.View(), "mouse=scroll") {
		t.Fatalf("view should show scroll mouse mode:\n%s", model.View())
	}

	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	model = updated.(Model)
	if model.mouseTracking || cmd == nil {
		t.Fatalf("expected mouse tracking disabled with command: tracking=%v cmd=%v", model.mouseTracking, cmd)
	}
	if _, ok := cmd().(tea.Msg); !ok {
		t.Fatal("mouse disable command should return a Bubble Tea message")
	}
	if !strings.Contains(model.View(), "mouse=copy") {
		t.Fatalf("view should show copy-friendly mouse mode:\n%s", model.View())
	}
}

func TestModelShowsInitialResumeStatus(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Run: func(ctx context.Context, prompt string) (string, error) {
			return "answer", nil
		},
		InitialMessages: []InitialMessage{{Role: "status", Content: "Recovered interrupted transcript state."}},
	})
	transcript := renderTranscriptForTest(model)
	if !strings.Contains(transcript, "Status") || !strings.Contains(transcript, "Recovered interrupted transcript state.") {
		t.Fatalf("transcript missing resume status:\n%s", transcript)
	}
}

func TestModelShowsAndCompletesSlashSuggestions(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Run: func(ctx context.Context, prompt string) (string, error) {
			return "answer", nil
		},
		SlashCommands: func(ctx context.Context, prefix string) ([]SlashCommand, error) {
			var out []SlashCommand
			for _, command := range []SlashCommand{
				{Name: "help", Description: "Show help", Source: "builtin"},
				{Name: "status", Description: "Show status", Source: "builtin"},
			} {
				if prefix == "" || strings.HasPrefix(command.Name, prefix) {
					out = append(out, command)
				}
			}
			return out, nil
		},
	})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	model = updated.(Model)
	view := model.View()
	if !strings.Contains(view, "Slash commands") || !strings.Contains(view, "/help") || !strings.Contains(view, "/status") {
		t.Fatalf("view missing slash suggestions:\n%s", view)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	model = updated.(Model)
	view = model.View()
	if strings.Contains(view, "/help") || !strings.Contains(view, "/status") {
		t.Fatalf("filtered view =\n%s", view)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(Model)
	if got := model.textarea.Value(); got != "/status " {
		t.Fatalf("completed value = %q", got)
	}
}

func TestModelSlashSuggestionsCanScrollToBuiltinResume(t *testing.T) {
	commands := []SlashCommand{
		{Name: "attach", Source: "builtin"},
		{Name: "branch", Source: "builtin"},
		{Name: "clear", Source: "builtin"},
		{Name: "compact", Source: "builtin"},
		{Name: "diff", Source: "builtin"},
		{Name: "exit", Source: "builtin"},
		{Name: "goal", Source: "builtin"},
		{Name: "help", Source: "builtin"},
		{Name: "hooks", Source: "builtin"},
		{Name: "logs", Source: "builtin"},
		{Name: "resume", Source: "builtin"},
		{Name: "status", Source: "builtin"},
	}
	model := NewModel(context.Background(), Options{
		Run: func(ctx context.Context, prompt string) (string, error) {
			return "answer", nil
		},
		SlashCommands: func(ctx context.Context, prefix string) ([]SlashCommand, error) {
			if prefix != "" {
				t.Fatalf("prefix = %q, want empty for bare slash", prefix)
			}
			return commands, nil
		},
	})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	model = updated.(Model)
	if len(model.slashSuggestions) != len(commands) {
		t.Fatalf("slash suggestions loaded %d commands, want %d", len(model.slashSuggestions), len(commands))
	}
	initialView := model.View()
	if !strings.Contains(initialView, "/attach") || !strings.Contains(initialView, "... 4 more") {
		t.Fatalf("initial slash view should show first window and more marker:\n%s", initialView)
	}
	for model.slashSuggestions[model.slashSelected].Name != "resume" {
		updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
		model = updated.(Model)
	}
	view := model.View()
	if !strings.Contains(view, "/resume") || !strings.Contains(view, "... 3 before") {
		t.Fatalf("slash suggestions should scroll to /resume:\n%s", view)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(Model)
	if got := model.textarea.Value(); got != "/resume " {
		t.Fatalf("completed value = %q", got)
	}
}

func TestModelEnterCompletesPartialSlashCommand(t *testing.T) {
	var ran bool
	model := NewModel(context.Background(), Options{
		Run: func(ctx context.Context, prompt string) (string, error) {
			ran = true
			return "answer", nil
		},
		SlashCommands: func(ctx context.Context, prefix string) ([]SlashCommand, error) {
			return []SlashCommand{{Name: "status", Description: "Show status", Source: "builtin"}}, nil
		},
	})
	model.textarea.SetValue("/sta")
	model.updateSlashSuggestions()
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if ran || cmd != nil || model.busy {
		t.Fatalf("partial slash enter should complete without running: ran=%v cmd=%v busy=%v", ran, cmd, model.busy)
	}
	if got := model.textarea.Value(); got != "/status " {
		t.Fatalf("completed value = %q", got)
	}
}

func TestModelEnterRunsExactSlashCommand(t *testing.T) {
	var gotPrompt string
	model := NewModel(context.Background(), Options{
		Run: func(ctx context.Context, prompt string) (string, error) {
			gotPrompt = prompt
			return "answer", nil
		},
		SlashCommands: func(ctx context.Context, prefix string) ([]SlashCommand, error) {
			return []SlashCommand{{Name: "status", Description: "Show status", Source: "builtin"}}, nil
		},
	})
	model.textarea.SetValue("/status")
	model.updateSlashSuggestions()
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected slash command submit cmd")
	}
	model = runTestCommand(t, model, cmd)
	if gotPrompt != "/status" {
		t.Fatalf("prompt = %q", gotPrompt)
	}
	if model.busy {
		t.Fatalf("model still busy: %+v", model)
	}
}

func TestModelResumeSlashOpensPickerAndSubmitsSelection(t *testing.T) {
	var gotPrompt string
	model := NewModel(context.Background(), Options{
		Run: func(ctx context.Context, prompt string) (string, error) {
			gotPrompt = prompt
			return "resumed", nil
		},
		ResumeSessions: func(ctx context.Context) ([]ResumeSession, error) {
			return []ResumeSession{
				{ID: "session-1", Title: "First topic", Preview: "user: older", CWD: "/tmp/one", Updated: time.Date(2026, 5, 26, 16, 37, 0, 0, time.UTC)},
				{ID: "session-2", Title: "Second topic", Preview: "assistant: latest answer", CWD: "/tmp/two", Updated: time.Date(2026, 5, 26, 16, 38, 0, 0, time.UTC)},
			}, nil
		},
	})
	model.textarea.SetValue("/resume")
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil || !model.resumePickerActive() || !model.mouseTracking {
		t.Fatalf("expected resume picker with temporary mouse tracking: cmd=%v model=%+v", cmd, model)
	}
	view := model.View()
	if !strings.Contains(view, "Resume sessions") || !strings.Contains(view, "First topic") || !strings.Contains(view, "assistant: latest answer") {
		t.Fatalf("resume picker view missing readable sessions:\n%s", view)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	if model.resumeSelected != 1 {
		t.Fatalf("resumeSelected = %d", model.resumeSelected)
	}
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil || model.resumePickerActive() || model.mouseTracking {
		t.Fatalf("expected selected resume submitted and temporary mouse disabled: cmd=%v picker=%v mouse=%v", cmd, model.resumePickerActive(), model.mouseTracking)
	}
	model = runTestCommand(t, model, cmd)
	if gotPrompt != "/resume session-2" {
		t.Fatalf("prompt = %q", gotPrompt)
	}
	transcript := renderTranscriptForTest(model)
	if !strings.Contains(transcript, "resumed") {
		t.Fatalf("transcript missing response:\n%s", transcript)
	}
}

func TestModelResumePickerMouseWheelAndDoubleClick(t *testing.T) {
	var gotPrompt string
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	model := NewModel(context.Background(), Options{
		Run: func(ctx context.Context, prompt string) (string, error) {
			gotPrompt = prompt
			return "ok", nil
		},
		ResumeSessions: func(ctx context.Context) ([]ResumeSession, error) {
			return []ResumeSession{
				{ID: "session-1", Title: "First"},
				{ID: "session-2", Title: "Second"},
			}, nil
		},
	})
	model.now = func() time.Time { return now }
	model.textarea.SetValue("/resume")
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)

	updated, _ = model.Update(tea.MouseMsg{Type: tea.MouseWheelDown, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	model = updated.(Model)
	if model.resumeSelected != 1 {
		t.Fatalf("resumeSelected after wheel = %d", model.resumeSelected)
	}
	rowY := model.resumePickerStartY() + 2
	updated, _ = model.Update(tea.MouseMsg{Type: tea.MouseLeft, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, Y: rowY})
	model = updated.(Model)
	now = now.Add(200 * time.Millisecond)
	updated, cmd := model.Update(tea.MouseMsg{Type: tea.MouseLeft, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, Y: rowY})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected double-click submit command")
	}
	model = runTestCommand(t, model, cmd)
	if gotPrompt != "/resume session-2" {
		t.Fatalf("prompt = %q", gotPrompt)
	}
}

func TestModelRewindSlashOpensPickerAndSubmitsSelection(t *testing.T) {
	var gotPrompt string
	model := NewModel(context.Background(), Options{
		Run: func(ctx context.Context, prompt string) (string, error) {
			gotPrompt = prompt
			return "rewound", nil
		},
		RewindCandidates: func(ctx context.Context) ([]RewindCandidate, error) {
			return []RewindCandidate{
				{ID: "msg-1", Preview: "first prompt", Mode: "message"},
				{ID: "msg-2", Preview: "second prompt", Mode: "message"},
			}, nil
		},
	})
	model.textarea.SetValue("/rewind")
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd != nil || !model.rewindPickerActive() || model.busy {
		t.Fatalf("expected rewind picker: cmd=%v picker=%v busy=%v", cmd, model.rewindPickerActive(), model.busy)
	}
	view := model.View()
	if !strings.Contains(view, "Rewind messages") || !strings.Contains(view, "first prompt") || !strings.Contains(view, "msg-2") {
		t.Fatalf("rewind picker view missing candidates:\n%s", view)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	if model.rewindSelected != 1 {
		t.Fatalf("rewindSelected = %d", model.rewindSelected)
	}
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil || model.rewindPickerActive() {
		t.Fatalf("expected selected rewind submitted: cmd=%v picker=%v", cmd, model.rewindPickerActive())
	}
	model = runTestCommand(t, model, cmd)
	if gotPrompt != "/rewind msg-2" {
		t.Fatalf("prompt = %q", gotPrompt)
	}
	transcript := renderTranscriptForTest(model)
	if !strings.Contains(transcript, "rewound") {
		t.Fatalf("transcript missing response:\n%s", transcript)
	}
}

func TestModelCheckpointSlashUsesRewindPickerCommandName(t *testing.T) {
	var gotPrompt string
	model := NewModel(context.Background(), Options{
		Run: func(ctx context.Context, prompt string) (string, error) {
			gotPrompt = prompt
			return "checkpointed", nil
		},
		RewindCandidates: func(ctx context.Context) ([]RewindCandidate, error) {
			return []RewindCandidate{{ID: "msg-1", Preview: "first prompt"}}, nil
		},
	})
	model.textarea.SetValue("/checkpoint")
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected checkpoint picker submit command")
	}
	model = runTestCommand(t, model, cmd)
	if gotPrompt != "/checkpoint msg-1" {
		t.Fatalf("prompt = %q", gotPrompt)
	}
}

func TestModelRewindPickerEscCloses(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Run: func(ctx context.Context, prompt string) (string, error) { return "ok", nil },
		RewindCandidates: func(ctx context.Context) ([]RewindCandidate, error) {
			return []RewindCandidate{{ID: "msg-1", Preview: "first prompt"}}, nil
		},
	})
	model.textarea.SetValue("/rewind")
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.rewindPickerActive() || strings.Contains(model.View(), "Rewind messages") {
		t.Fatalf("rewind picker should be closed:\n%s", model.View())
	}
}

func runTestCommand(t *testing.T, model Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		return model
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, child := range batch {
			model = runTestCommand(t, model, child)
		}
		return model
	}
	if children, ok := commandSliceForTest(msg); ok {
		for _, child := range children {
			model = runTestCommand(t, model, child)
		}
		return model
	}
	updated, next := model.Update(msg)
	model = updated.(Model)
	if next != nil {
		model = runTestCommand(t, model, next)
	}
	return model
}

func renderTranscriptForTest(model Model) string {
	parts := []string{model.transcriptHeaderView()}
	for _, msg := range model.messages {
		rendered := strings.TrimRight(model.renderMessage(msg), "\n")
		if rendered != "" {
			parts = append(parts, rendered)
		}
	}
	return formatTranscriptBlocks(parts)
}

func latestDisplaySegmentForTest(t *testing.T, model Model, kind displaySegmentKind) displaySegment {
	t.Helper()
	for i := len(model.displayTimeline.segments) - 1; i >= 0; i-- {
		if model.displayTimeline.segments[i].kind == kind {
			return model.displayTimeline.segments[i]
		}
	}
	t.Fatalf("missing display segment kind %s in %#v", kind, model.displayTimeline.segments)
	return displaySegment{}
}

func firstBlockingTestCommand(t *testing.T, cmd tea.Cmd) tea.Cmd {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected command")
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		if children, ok := commandSliceForTest(msg); ok {
			if len(children) == 0 {
				t.Fatal("sequence did not contain commands")
			}
			return children[len(children)-1]
		}
		return func() tea.Msg { return msg }
	}
	if len(batch) >= 2 {
		return batch[0]
	}
	t.Fatal("batch did not contain a response command")
	return nil
}

func commandSliceForTest(msg tea.Msg) ([]tea.Cmd, bool) {
	value := reflect.ValueOf(msg)
	if !value.IsValid() || value.Kind() != reflect.Slice {
		return nil, false
	}
	cmdType := reflect.TypeOf((tea.Cmd)(nil))
	if !value.Type().Elem().AssignableTo(cmdType) {
		return nil, false
	}
	out := make([]tea.Cmd, 0, value.Len())
	for i := 0; i < value.Len(); i++ {
		if cmd, ok := value.Index(i).Interface().(tea.Cmd); ok {
			out = append(out, cmd)
		}
	}
	return out, true
}

func TestModelStreamsPromptOutput(t *testing.T) {
	events := make(chan StreamEvent, 2)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	// Streaming text renders are throttled to one per interval; advance the
	// pinned clock between deltas so both render and the view reflects the full
	// accumulated text (the real flow's turn-completion refresh does the same).
	fixed := time.Unix(1_000, 0)
	model.now = func() time.Time { return fixed }
	updated, cmd := model.Update(streamEventMsg{event: StreamEvent{Type: StreamText, Text: "he"}, ch: events})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected next wait command")
	}
	fixed = fixed.Add(streamRenderThrottleInterval + time.Millisecond)
	updated, _ = model.Update(streamEventMsg{event: StreamEvent{Type: StreamText, Text: "llo"}, ch: events})
	model = updated.(Model)
	if !strings.Contains(model.View(), "hello") {
		t.Fatalf("view = %s", model.View())
	}
}

func TestModelStreamsUsagePanel(t *testing.T) {
	events := make(chan StreamEvent, 2)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	updated, cmd := model.Update(streamEventMsg{
		event: StreamEvent{Type: StreamUsage, Result: &QueryResult{
			Model:   "stream-model",
			Usage:   Usage{InputTokens: 8, OutputTokens: 2},
			Context: RuntimeContext{ToolCount: 6},
		}},
		ch: events,
	})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected next wait command")
	}
	view := model.View()
	if !strings.Contains(view, "model=stream-model") || !strings.Contains(view, "tokens in/out=8/2") {
		t.Fatalf("view = %s", view)
	}
}

func TestUsageViewWrapsLongSessionDetails(t *testing.T) {
	model := NewModel(context.Background(), Options{Run: func(context.Context, string) (string, error) { return "", nil }})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 72, Height: 24})
	model = updated.(Model)
	model.usage = usagePanel{
		Model:        "glm-5.1",
		Turns:        14,
		MaxTurns:     100,
		InputTokens:  227226,
		OutputTokens: 3202,
		CacheCreate:  12000,
		CacheRead:    88000,
		ToolCalls:    15,
		ToolCount:    27,
		LastTool:     "Read",
		StopReason:   "end_turn",
		SessionID:    "8719a008-b2a8-43bc-8f51-418b0c148f5d",
		CWD:          "/Users/example/GolandProjects/niuma",
	}
	view := model.usageView()
	if lineCount(view) < 2 {
		t.Fatalf("usage view should wrap long details:\n%s", view)
	}
	for _, want := range []string{"Usage", "tokens in/out=227226/3202", "cache create/read=12000/88000 session_hit=38.7%", "执行了 15 次操作 · 最近使用 查看文件", "cwd=/Users/example/GolandProjects/niuma"} {
		if !strings.Contains(view, want) {
			t.Fatalf("usage view missing %q:\n%s", want, view)
		}
	}
	for _, notWant := range []string{"stop=end_turn", "session=8719a008-b2a8-43bc-8f51-418b0c148f5d"} {
		if strings.Contains(view, notWant) {
			t.Fatalf("usage view should suppress %q:\n%s", notWant, view)
		}
	}
}

func TestUsageViewSuppressesModelAndCwdWhenSameAsHeader(t *testing.T) {
	// The per-turn usage line should not repeat model/cwd that the session
	// header already shows; that duplication is BUG-2026-07-03-001.
	model := NewModel(context.Background(), Options{
		Welcome: WelcomeInfo{Model: "glm-5.1", CWD: "/Users/example/GolandProjects/niuma"},
		Run:     func(context.Context, string) (string, error) { return "", nil },
	})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	model = updated.(Model)
	model.usage = usagePanel{
		Model:        "glm-5.1",
		Turns:        2,
		InputTokens:  100,
		OutputTokens: 10,
		CWD:          "/Users/example/GolandProjects/niuma",
	}
	view := stripANSI(model.usageView())
	for _, notWant := range []string{"model=glm-5.1", "cwd=/Users/example/GolandProjects/niuma"} {
		if strings.Contains(view, notWant) {
			t.Fatalf("per-turn usage should not repeat session-level %q (already in header):\n%s", notWant, view)
		}
	}
	if !strings.Contains(view, "tokens in/out=100/10") {
		t.Fatalf("usage should still show per-turn tokens:\n%s", view)
	}
}

func TestUsageViewKeepsModelWhenHeaderModelDiffers(t *testing.T) {
	// When the actual response model differs from the header model, it is
	// informative (not redundant) and must remain visible.
	model := NewModel(context.Background(), Options{
		Welcome: WelcomeInfo{Model: "glm-5.1"},
		Run:     func(context.Context, string) (string, error) { return "", nil },
	})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	model = updated.(Model)
	model.usage = usagePanel{Model: "gpt-5.5", Turns: 1, InputTokens: 8, OutputTokens: 2}
	view := stripANSI(model.usageView())
	if !strings.Contains(view, "model=gpt-5.5") {
		t.Fatalf("usage should show model when it differs from header:\n%s", view)
	}
}

func TestModelAppliesConfigReloadEvent(t *testing.T) {
	events := make(chan StreamEvent, 2)
	model := NewModel(context.Background(), Options{
		Welcome:   WelcomeInfo{Version: "test-version", Model: "gpt-5.5", Provider: "custom/openai-compatible"},
		RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil },
	})
	model.busy = true
	next := WelcomeInfo{Version: "test-version", Model: "glm-5.1", Provider: "custom/openai-compatible"}
	updated, cmd := model.Update(streamEventMsg{
		event: StreamEvent{Type: StreamConfigReload, Welcome: &next},
		ch:    events,
	})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected next wait command")
	}
	view := model.View()
	if !strings.Contains(view, "glm-5.1") || !strings.Contains(view, "↻ Config · model gpt-5.5 → glm-5.1") {
		t.Fatalf("view missing config reload update:\n%s", view)
	}
}

func TestModelShowsSessionStatusFromConfigReload(t *testing.T) {
	events := make(chan StreamEvent, 2)
	model := NewModel(context.Background(), Options{
		Welcome:   WelcomeInfo{Model: "glm-5.1", Resume: "sess-1"},
		RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil },
	})
	model.busy = true
	next := WelcomeInfo{Model: "glm-5.1", Resume: "sess-1", SessionStatus: "Compacted current session (123 bytes summary)"}
	updated, _ := model.Update(streamEventMsg{
		event: StreamEvent{Type: StreamConfigReload, Welcome: &next},
		ch:    events,
	})
	model = updated.(Model)
	view := stripANSI(model.modeHintView("Ready"))
	for _, want := range []string{"session  resume=sess-1", "state=Compacted current session"} {
		if !strings.Contains(view, want) {
			t.Fatalf("mode hint missing session status %q:\n%s", want, view)
		}
	}
}

func TestModelSessionResumeEventClearsCurrentConversation(t *testing.T) {
	events := make(chan StreamEvent, 2)
	model := NewModel(context.Background(), Options{
		Welcome:   WelcomeInfo{Model: "glm-5.1"},
		RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil },
	})
	model.busy = true
	model.messages = append(model.messages,
		message{role: "user", content: "1+1="},
		message{role: "assistant", content: "2"},
	)
	model.usage = usagePanel{Model: "glm-5.1", InputTokens: 100}
	next := WelcomeInfo{Model: "glm-5.1", Resume: "99dec7cb-e5e4-4ea6-a88e-d1ccc4f17553"}
	updated, cmd := model.Update(streamEventMsg{
		event: StreamEvent{Type: StreamSessionResume, Text: "Resumed session 99dec7cb-e5e4-4ea6-a88e-d1ccc4f17553", Welcome: &next},
		ch:    events,
	})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected next wait command")
	}
	view := model.View()
	transcript := renderTranscriptForTest(model)
	if strings.Contains(view, "1+1=") || strings.Contains(view, "\n2\n") || strings.Contains(view, "Usage") {
		t.Fatalf("resume should clear prior conversation and usage:\n%s", view)
	}
	if !strings.Contains(transcript, "Resumed session 99dec7cb") || !strings.Contains(transcript, "恢复     99dec7cb") {
		t.Fatalf("transcript missing resume status:\n%s", transcript)
	}
}

func TestModelSessionResumeEventShowsRecentHistory(t *testing.T) {
	events := make(chan StreamEvent, 2)
	model := NewModel(context.Background(), Options{
		Welcome:   WelcomeInfo{Model: "glm-5.1"},
		RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil },
	})
	next := WelcomeInfo{Model: "glm-5.1", Resume: "99dec7cb-e5e4-4ea6-a88e-d1ccc4f17553"}
	updated, cmd := model.Update(streamEventMsg{
		event: StreamEvent{
			Type:    StreamSessionResume,
			Text:    "Resumed session 99dec7cb-e5e4-4ea6-a88e-d1ccc4f17553",
			Welcome: &next,
			History: []InitialMessage{
				{Role: "user", Content: "previous question"},
				{Role: "assistant", Content: "line one\nline two\nline three"},
			},
		},
		ch: events,
	})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected next wait command")
	}
	transcript := renderTranscriptForTest(model)
	for _, want := range []string{"previous question", "line one", "line two", "line three"} {
		if !strings.Contains(transcript, want) {
			t.Fatalf("transcript missing resume history %q:\n%s", want, transcript)
		}
	}
	if !model.transcriptPrintedHeader || model.transcriptPrintedCount != len(model.messages) {
		t.Fatalf("resume history should be marked printed, header=%v count=%d messages=%d", model.transcriptPrintedHeader, model.transcriptPrintedCount, len(model.messages))
	}
}

func TestModelStreamsRecapEventUpdatesBottomRecap(t *testing.T) {
	events := make(chan StreamEvent, 2)
	model := NewModel(context.Background(), Options{
		RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil },
		InitialMessages: []InitialMessage{
			{Role: "recap", Content: "old recap"},
		},
	})
	updated, cmd := model.Update(streamEventMsg{
		event: StreamEvent{Type: StreamRecap, Text: "本次会话目标：实现 recap\n下一步：测试"},
		ch:    events,
	})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected next wait command")
	}
	view := renderTranscriptForTest(model)
	if !strings.Contains(view, "※recap:") || !strings.Contains(view, "本次会话目标：实现 recap") || strings.Contains(view, "old recap") {
		t.Fatalf("transcript missing updated recap:\n%s", view)
	}
}

func TestRenderRecapUsesInlineClaudeStyle(t *testing.T) {
	out := stripANSI(renderRecapForWidth("本次会话目标：实现 recap\n已完成：测试\n下一步：继续", 120))
	if !strings.Contains(out, "※recap: 本次会话目标：实现 recap") {
		t.Fatalf("recap should start inline: %q", out)
	}
	if strings.Contains(out, "※ recap:\n") || strings.Contains(out, "\n  已完成") {
		t.Fatalf("recap should not render as a dark multiline block: %q", out)
	}
	if !strings.Contains(out, "已完成：测试  下一步：继续") {
		t.Fatalf("recap should compact short lines: %q", out)
	}
}

func TestModelRecapAppendsWhenPreviousRecapAlreadyPrinted(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Run: func(context.Context, string) (string, error) { return "ok", nil },
		InitialMessages: []InitialMessage{
			{Role: "recap", Content: "old recap"},
		},
	})
	model.transcriptPrintedHeader = true
	model.transcriptPrintedCount = len(model.messages)
	model.applyRecap(StreamEvent{Type: StreamRecap, Text: "new recap"})
	if len(model.messages) != 2 || model.messages[1].role != "recap" {
		t.Fatalf("expected printed recap to append a new live recap: %#v", model.messages)
	}
	if blocks := model.pendingTranscriptBlocks(); len(blocks) != 0 {
		t.Fatalf("recap should stay out of transcript flush blocks: %#v", blocks)
	}
	if msg, ok := model.latestRecapMessage(); !ok || !strings.Contains(msg.content, "new recap") {
		t.Fatalf("latest recap = %#v ok=%v", msg, ok)
	}
}

func TestModelTriggersAwayRecapAfterIdle(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	called := 0
	model := NewModel(context.Background(), Options{
		Run: func(context.Context, string) (string, error) { return "", nil },
		RunAwayRecap: func(_ context.Context, events chan<- StreamEvent) error {
			called++
			events <- StreamEvent{Type: StreamRecap, Text: "本次会话目标：idle recap"}
			return nil
		},
		AwayRecapDelay: 5 * time.Second,
	})
	model.now = func() time.Time { return now }
	model.lastActivity = now
	model.awayRecapArmed = true

	if cmd := model.maybeStartAwayRecap(now.Add(4 * time.Second)); cmd != nil {
		t.Fatal("away recap started before idle delay")
	}
	if called != 0 || model.awayRecapRunning {
		t.Fatalf("called=%d running=%v", called, model.awayRecapRunning)
	}

	awayCmd := model.maybeStartAwayRecap(now.Add(6 * time.Second))
	if awayCmd == nil || !model.awayRecapRunning {
		t.Fatalf("away recap did not start: running=%v", model.awayRecapRunning)
	}
	msg := awayCmd().(awayRecapMsg)
	updated, _ := model.Update(msg)
	model = updated.(Model)
	transcript := renderTranscriptForTest(model)
	if called != 1 || model.awayRecapRunning || !strings.Contains(transcript, "idle recap") {
		t.Fatalf("called=%d running=%v transcript=%s view=%s", called, model.awayRecapRunning, transcript, model.View())
	}
}

func TestModelActivityCancelsRunningAwayRecapAndDropsLateResult(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	started := make(chan struct{})
	model := NewModel(context.Background(), Options{
		Run: func(context.Context, string) (string, error) { return "", nil },
		RunAwayRecap: func(ctx context.Context, events chan<- StreamEvent) error {
			close(started)
			<-ctx.Done()
			// A provider may finish concurrently with cancellation. The generation
			// guard must reject the result even if the runner still returns text.
			events <- StreamEvent{Type: StreamRecap, Text: "stale recap must be dropped"}
			return nil
		},
		AwayRecapDelay: 5 * time.Second,
	})
	model.now = func() time.Time { return now }
	model.awayRecapArmed = true
	model.awayRecapArmedAt = now

	cmd := model.maybeStartAwayRecap(now.Add(6 * time.Second))
	if cmd == nil {
		t.Fatal("expected away recap command")
	}
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	<-started

	model.noteActivity()
	var msg tea.Msg
	select {
	case msg = <-result:
	case <-time.After(time.Second):
		t.Fatal("running away recap was not cancelled by user activity")
	}
	updated, _ := model.Update(msg)
	model = updated.(Model)
	if _, ok := model.latestRecapMessage(); ok {
		t.Fatalf("stale away recap reached the model: %#v", model.messages)
	}
}

func TestModelAwayRecapShowsAfterCompletedTurnWithUsage(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	model := NewModel(context.Background(), Options{
		Run:            func(context.Context, string) (string, error) { return "", nil },
		RunAwayRecap:   func(context.Context, chan<- StreamEvent) error { return nil },
		AwayRecapDelay: 5 * time.Second,
	})
	model.now = func() time.Time { return now }

	updated, cmd := model.Update(responseMsg{result: QueryResult{
		Response: "done",
		Model:    "recap-model",
		Usage:    Usage{InputTokens: 10, OutputTokens: 2},
	}})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected completed assistant transcript flush")
	}
	if !model.awayRecapArmed {
		t.Fatal("away recap should be armed after assistant completion")
	}

	updated, cmd = model.Update(awayRecapMsg{generation: model.recapGeneration, text: "本次会话目标：验证 away recap\n下一步：继续测试"})
	model = updated.(Model)
	if cmd != nil {
		t.Fatalf("away recap should stay in live view, not terminal transcript cmd: %#v", cmd)
	}
	view := stripANSI(model.View())
	if !strings.Contains(view, "※recap:") || !strings.Contains(view, "验证 away recap") {
		t.Fatalf("away recap should remain visible after usage exists:\n%s", view)
	}
}

func TestModelAwayRecapEmptyResponseDoesNotDisturbAssistant(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Run:            func(context.Context, string) (string, error) { return "", nil },
		RunAwayRecap:   func(context.Context, chan<- StreamEvent) error { return nil },
		AwayRecapDelay: 5 * time.Second,
	})
	updated, cmd := model.Update(responseMsg{result: QueryResult{
		Response: "assistant answer with table\n\n| 模块 | 数量 |\n|---|---|\n| 需求洞察 | 9 |",
		Model:    "agnes-2.0-flash",
		Usage:    Usage{InputTokens: 10, OutputTokens: 2},
	}})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected completed assistant transcript flush")
	}
	before := stripANSI(model.View())

	updated, cmd = model.Update(awayRecapMsg{generation: model.recapGeneration, text: "   \n\t"})
	model = updated.(Model)
	if cmd != nil {
		t.Fatalf("empty away recap should not trigger transcript flush: %#v", cmd)
	}
	after := stripANSI(model.View())
	if before != after {
		t.Fatalf("empty away recap should not disturb assistant view\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if _, ok := model.latestLiveRecapMessage(); ok {
		t.Fatalf("empty away recap should not create a live recap message: %#v", model.messages)
	}
}

func TestModelArmsAwayRecapFromAssistantCompletion(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	called := 0
	model := NewModel(context.Background(), Options{
		Run: func(context.Context, string) (string, error) { return "", nil },
		RunAwayRecap: func(_ context.Context, events chan<- StreamEvent) error {
			called++
			events <- StreamEvent{Type: StreamRecap, Text: "completion recap"}
			return nil
		},
		AwayRecapDelay: 5 * time.Second,
	})
	model.now = func() time.Time { return now }
	model.lastActivity = now.Add(-time.Minute)

	updated, _ := model.Update(responseMsg{result: QueryResult{Response: "done"}})
	model = updated.(Model)
	if !model.awayRecapArmed || !model.awayRecapArmedAt.Equal(now) {
		t.Fatalf("away recap not armed from completion: armed=%v armedAt=%s", model.awayRecapArmed, model.awayRecapArmedAt)
	}
	if cmd := model.maybeStartAwayRecap(now.Add(4 * time.Second)); cmd != nil {
		t.Fatal("away recap started before completion-based delay")
	}
	if cmd := model.maybeStartAwayRecap(now.Add(5 * time.Second)); cmd == nil {
		t.Fatal("away recap did not start at completion-based delay")
	}
	if called != 0 {
		t.Fatalf("away command should not run until invoked, called=%d", called)
	}
}

func TestModelAwayRecapWaitsWhenTUIIsNotIdle(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	tests := []struct {
		name  string
		setup func(*Model)
	}{
		{name: "busy", setup: func(m *Model) { m.busy = true }},
		{name: "permission prompt", setup: func(m *Model) {
			ch := make(chan PermissionDecision, 1)
			m.pendingPermission = &pendingPermission{reply: ch}
		}},
		{name: "resume picker", setup: func(m *Model) {
			m.resumePicker = []ResumeSession{{ID: "session-1"}}
		}},
		{name: "rewind picker", setup: func(m *Model) {
			m.rewindPicker = []RewindCandidate{{ID: "turn-1"}}
		}},
		{name: "non empty input", setup: func(m *Model) {
			m.textarea.SetValue("next")
		}},
		{name: "slash suggestions", setup: func(m *Model) {
			m.slashSuggestions = []SlashCommand{{Name: "help"}}
		}},
		{name: "slash error", setup: func(m *Model) {
			m.slashErr = errors.New("slash failed")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := NewModel(context.Background(), Options{
				Run:            func(context.Context, string) (string, error) { return "", nil },
				RunAwayRecap:   func(context.Context, chan<- StreamEvent) error { return nil },
				AwayRecapDelay: 5 * time.Second,
			})
			model.lastActivity = now
			model.awayRecapArmed = true
			model.awayRecapArmedAt = now
			tt.setup(&model)
			if cmd := model.maybeStartAwayRecap(now.Add(6 * time.Second)); cmd != nil {
				t.Fatal("away recap should wait while TUI is not idle")
			}
		})
	}
}

func TestModelStreamsToolActivityInlineOnly(t *testing.T) {
	events := make(chan StreamEvent, 2)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	// Pin the clock so the running tool's animated spinner is deterministic; a
	// zero time renders the first braille frame.
	model.now = func() time.Time { return time.Time{} }
	model.messages = append(model.messages, message{role: "assistant", content: "answer"})
	updated, cmd := model.Update(streamEventMsg{event: StreamEvent{Type: StreamToolStart, ToolName: "Read", ToolID: "toolu_1"}, ch: events})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected next wait command")
	}
	view := model.View()
	if !strings.Contains(view, "niuma") || !strings.Contains(view, "answer") || !strings.Contains(view, "查看文件") || !strings.Contains(view, progressSpinnerFrames[0]) {
		t.Fatalf("view missing running inline tool:\n%s", view)
	}
	if strings.Contains(view, "\nTools") {
		t.Fatalf("tool activity should not render in the legacy bottom panel:\n%s", view)
	}

	updated, _ = model.Update(streamEventMsg{event: StreamEvent{Type: StreamToolResult, ToolName: "Read", ToolID: "toolu_1", Output: "read 12 lines"}, ch: events})
	model = updated.(Model)
	updated, _ = model.Update(streamEventMsg{event: StreamEvent{Type: StreamUsage, Result: &QueryResult{Model: "m", ToolCalls: []ToolCall{{ID: "toolu_1", Name: "Read"}}}}, ch: events})
	model = updated.(Model)
	view = model.View()
	if !strings.Contains(view, "✓") || !strings.Contains(view, "文件：read 12 lines") || !strings.Contains(view, "完成：读取 1 行") {
		t.Fatalf("view missing completed inline tool activity:\n%s", view)
	}
	if strings.Contains(view, "\nTools") {
		t.Fatalf("completed tool activity should not render in the legacy panel:\n%s", view)
	}
	updated, _ = model.Update(streamEventMsg{event: StreamEvent{Type: StreamFinished}, ch: events})
	model = updated.(Model)
	if len(model.toolActivity) != 0 {
		t.Fatalf("completed tool activity should be archived: %+v", model.toolActivity)
	}
}

func TestRunningEllipsisAnimatesAtConstantWidth(t *testing.T) {
	// Frame 0 (not yet ticking) renders the plain ellipsis.
	if got := runningEllipsis(0); got != "..." {
		t.Fatalf("frame 0 = %q, want ...", got)
	}
	seen := map[string]bool{}
	for f := 1; f <= 9; f++ {
		e := runningEllipsis(f)
		if runewidth.StringWidth(e) != 3 {
			t.Fatalf("frame %d width = %d, want constant 3 (%q)", f, runewidth.StringWidth(e), e)
		}
		seen[e] = true
	}
	for _, want := range []string{
		"." + runningEllipsisBlank + runningEllipsisBlank,
		".." + runningEllipsisBlank,
		"...",
	} {
		if !seen[want] {
			t.Fatalf("cycle missing frame %q; saw %v", want, seen)
		}
	}
	// The braille-blank placeholder must survive TrimSpace so the fields after
	// the status on the bottom line never shift.
	oneDot := "." + runningEllipsisBlank + runningEllipsisBlank
	if strings.TrimSpace(oneDot) != oneDot {
		t.Fatal("ellipsis placeholder must not be stripped by TrimSpace")
	}
}

func TestAnimateStatusEllipsisOnlyTouchesTrailingDots(t *testing.T) {
	const status = "niuma is preparing context..."
	if got := animateStatusEllipsis(status, 0); got != status {
		t.Fatalf("frame 0 must be the plain status, got %q", got)
	}
	got := animateStatusEllipsis(status, 1)
	if !strings.HasPrefix(got, "niuma is preparing context.") {
		t.Fatalf("prefix changed: %q", got)
	}
	if runewidth.StringWidth(got) != runewidth.StringWidth(status) {
		t.Fatalf("animated width %d != original %d", runewidth.StringWidth(got), runewidth.StringWidth(status))
	}
	// A status without a trailing ellipsis is untouched.
	if got := animateStatusEllipsis("Ready", 5); got != "Ready" {
		t.Fatalf("non-ellipsis status changed: %q", got)
	}
}

func TestModelStatusEllipsisAnimatesInView(t *testing.T) {
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	model.width = 100
	model.height = 24
	model.runningStatus = "niuma is preparing context..."
	model.refreshViewportAtBottom()

	// Frame 0 renders the plain ellipsis (matches every non-animating render).
	if v := stripANSI(model.View()); !strings.Contains(v, "niuma is preparing context...") {
		t.Fatalf("frame 0 should show the plain ellipsis:\n%s", v)
	}

	// One animation tick lights a single dot with an invisible placeholder that
	// survives the render/trim pipeline on both the header and status line.
	updated, _ := model.Update(spinnerTickMsg{})
	model = updated.(Model)
	v := stripANSI(model.View())
	if !strings.Contains(v, "niuma is preparing context."+runningEllipsisBlank) {
		t.Fatalf("frame 1 should show one lit dot plus a blank slot:\n%s", v)
	}
	if strings.Contains(v, "niuma is preparing context...") {
		t.Fatalf("frame 1 should no longer show three solid dots:\n%s", v)
	}
}

func TestModelKeepsToolFirstDisplayOrderAfterUsage(t *testing.T) {
	events := make(chan StreamEvent, 4)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	model.width = 100
	model.height = 32
	model.transcriptPrintedCount = len(model.messages)
	model.refreshViewportAtBottom()

	updated, _ := model.Update(streamEventMsg{event: StreamEvent{Type: StreamToolStart, ToolName: "Read", ToolID: "toolu_1", Output: `{"file_path":"README.md"}`}, ch: events})
	model = updated.(Model)
	beforeText := stripANSI(model.liveTranscriptView())
	beforeToolLine := lineIndexContaining(beforeText, "查看文件")
	if beforeToolLine < 0 {
		t.Fatalf("view missing running tool:\n%s", beforeText)
	}

	updated, _ = model.Update(streamEventMsg{event: StreamEvent{Type: StreamToolResult, ToolName: "Read", ToolID: "toolu_1", Output: "read 12 lines"}, ch: events})
	model = updated.(Model)
	updated, _ = model.Update(streamEventMsg{event: StreamEvent{Type: StreamText, Text: "工具完成后的解释"}, ch: events})
	model = updated.(Model)
	beforeUsage := stripANSI(model.liveTranscriptView())
	if got := lineIndexContaining(beforeUsage, "查看文件"); got != beforeToolLine {
		t.Fatalf("tool line moved before usage: got %d want %d\n%s", got, beforeToolLine, beforeUsage)
	}
	answerLine := lineIndexContaining(beforeUsage, "工具完成后的解释")
	if answerLine <= beforeToolLine {
		t.Fatalf("assistant text should stay after earlier tool block: tool=%d answer=%d\n%s", beforeToolLine, answerLine, beforeUsage)
	}

	updated, _ = model.Update(streamEventMsg{event: StreamEvent{Type: StreamUsage, Result: &QueryResult{Model: "m", ToolCalls: []ToolCall{{ID: "toolu_1", Name: "Read"}}}}, ch: events})
	model = updated.(Model)
	afterUsage := stripANSI(model.liveTranscriptView())
	if got := lineIndexContaining(afterUsage, "查看文件"); got != beforeToolLine {
		t.Fatalf("tool line moved after usage: got %d want %d\nbefore:\n%s\nafter:\n%s", got, beforeToolLine, beforeUsage, afterUsage)
	}
	if got := lineIndexContaining(afterUsage, "工具完成后的解释"); got <= beforeToolLine {
		t.Fatalf("assistant text moved before earlier tool block after usage: tool=%d answer=%d\n%s", beforeToolLine, got, afterUsage)
	}
}

func TestModelKeepsAssistantFirstDisplayOrderAfterUsage(t *testing.T) {
	events := make(chan StreamEvent, 4)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	model.width = 100
	model.height = 32
	model.transcriptPrintedCount = len(model.messages)
	model.refreshViewportAtBottom()

	updated, _ := model.Update(streamEventMsg{event: StreamEvent{Type: StreamText, Text: "先解释再查文件"}, ch: events})
	model = updated.(Model)
	updated, _ = model.Update(streamEventMsg{event: StreamEvent{Type: StreamToolStart, ToolName: "Read", ToolID: "toolu_1", Output: `{"file_path":"README.md"}`}, ch: events})
	model = updated.(Model)
	updated, _ = model.Update(streamEventMsg{event: StreamEvent{Type: StreamToolResult, ToolName: "Read", ToolID: "toolu_1", Output: "read 12 lines"}, ch: events})
	model = updated.(Model)

	beforeUsage := stripANSI(model.liveTranscriptView())
	toolLine := lineIndexContaining(beforeUsage, "查看文件")
	if toolLine < 0 || strings.Contains(beforeUsage, "先解释再查文件") {
		t.Fatalf("completed assistant should be committed before live tool view:\n%s", beforeUsage)
	}
	assistantLine := -1
	for _, seg := range model.displayTimeline.segments {
		if seg.kind == displaySegmentAssistantText {
			assistantLine = lineIndexContaining(stripANSI(model.renderDisplaySegment(seg, true, false)), "先解释再查文件")
		}
	}
	if assistantLine < 0 {
		t.Fatal("committed assistant text missing from timeline transcript")
	}

	updated, _ = model.Update(streamEventMsg{event: StreamEvent{Type: StreamUsage, Result: &QueryResult{Model: "m", ToolCalls: []ToolCall{{ID: "toolu_1", Name: "Read"}}}}, ch: events})
	model = updated.(Model)
	afterUsage := stripANSI(model.liveTranscriptView())
	if strings.Contains(afterUsage, "先解释再查文件") {
		t.Fatalf("committed assistant reappeared after usage:\n%s", afterUsage)
	}
	if got := lineIndexContaining(afterUsage, "查看文件"); got != toolLine {
		t.Fatalf("tool line moved after usage: got %d want %d\nbefore:\n%s\nafter:\n%s", got, toolLine, beforeUsage, afterUsage)
	}
}

func TestModelDoesNotSplitAssistantWhenTextArrivesAfterUsage(t *testing.T) {
	events := make(chan StreamEvent, 8)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	model.streamingActive = true
	model.width = 120
	model.height = 36
	model.transcriptPrintedHeader = true
	model.transcriptPrintedCount = len(model.messages)
	model.refreshViewportAtBottom()

	firstChunk := strings.Join([]string{
		"项目结构按学习阶段组织：",
		"",
		"| 目录 | 内容 |",
		"|---|---|",
		"| `roles/` | 按角色分类（程序员、内容创作者等） |",
		"",
	}, "\n")
	lateChunk := "| `prompts/` | 提示词库 |\n\n核心理念：实践出真知。"

	updated, _ := model.Update(streamEventMsg{event: StreamEvent{Type: StreamText, Text: firstChunk}, ch: events})
	model = updated.(Model)
	updated, _ = model.Update(streamEventMsg{event: StreamEvent{Type: StreamUsage, Result: &QueryResult{
		Model: "glm-5.1",
		Usage: Usage{InputTokens: 20, OutputTokens: 5},
	}}, ch: events})
	model = updated.(Model)
	updated, _ = model.Update(streamEventMsg{event: StreamEvent{Type: StreamText, Text: lateChunk}, ch: events})
	model = updated.(Model)

	live := stripANSI(model.liveTranscriptView())
	if count := strings.Count(live, "niuma"); count != 1 {
		t.Fatalf("live assistant split after usage: niuma count=%d\n%s", count, live)
	}
	if !strings.Contains(live, "roles/") || !strings.Contains(live, "prompts/") {
		t.Fatalf("live assistant missing table rows after late text:\n%s", live)
	}

	updated, cmd := model.Update(streamEventMsg{event: StreamEvent{Type: StreamFinished}, ch: events})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected delayed transcript commit command")
	}
	if live := stripANSI(model.liveTranscriptView()); strings.Contains(live, "prompts/") || strings.Contains(live, "核心理念") {
		t.Fatalf("finished turn should leave live layer before terminal print:\n%s", live)
	}
	commit, ok := cmd().(transcriptFlushCommitMsg)
	if !ok {
		t.Fatalf("expected transcriptFlushCommitMsg from delayed command")
	}
	output := stripANSI(commit.output)
	if count := strings.Count(output, "niuma"); count != 1 {
		t.Fatalf("transcript assistant split after finished: niuma count=%d\n%s", count, output)
	}
	if !strings.Contains(output, "roles/") || !strings.Contains(output, "prompts/") || !strings.Contains(output, "核心理念") {
		t.Fatalf("transcript output missing late assistant content:\n%s", output)
	}
}

func TestModelDoesNotRepeatAssistantHeaderAroundSubAgentProgress(t *testing.T) {
	events := make(chan StreamEvent, 8)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	model.streamingActive = true
	model.width = 120
	model.height = 36
	model.transcriptPrintedHeader = true
	model.transcriptPrintedCount = len(model.messages)
	model.refreshViewportAtBottom()

	updated, _ := model.Update(streamEventMsg{event: StreamEvent{Type: StreamText, Text: "先说明：本轮会触发 sub-agent。"}, ch: events})
	model = updated.(Model)
	updated, _ = model.Update(streamEventMsg{event: StreamEvent{
		Type:    StreamNestedProgress,
		Event:   "completed",
		TaskID:  77,
		Payload: json.RawMessage(`{"turns":1,"tool_calls":1,"duration_ms":42,"session_id":"subagent-terminal-acceptance"}`),
	}, ch: events})
	model = updated.(Model)
	updated, _ = model.Update(streamEventMsg{event: StreamEvent{Type: StreamText, Text: "后续总结：sub-agent 块不能让 assistant 标题重复。"}, ch: events})
	model = updated.(Model)

	live := stripANSI(model.liveTranscriptView())
	if count := strings.Count(live, "niuma"); count != 1 {
		t.Fatalf("live assistant header repeated around sub-agent: count=%d\n%s", count, live)
	}
	firstLine := lineIndexContaining(live, "先说明")
	agentLine := lineIndexContaining(live, "Sub-agents")
	secondLine := lineIndexContaining(live, "后续总结")
	if firstLine >= 0 || agentLine < 0 || secondLine < 0 || !(agentLine < secondLine) {
		t.Fatalf("live timeline should retain agent -> second assistant after first commit: first=%d agent=%d second=%d\n%s", firstLine, agentLine, secondLine, live)
	}

	updated, _ = model.Update(streamEventMsg{event: StreamEvent{Type: StreamUsage, Result: &QueryResult{Model: "m"}}, ch: events})
	model = updated.(Model)
	updated, cmd := model.Update(streamEventMsg{event: StreamEvent{Type: StreamFinished}, ch: events})
	model = updated.(Model)
	commit, ok := cmd().(transcriptFlushCommitMsg)
	if !ok {
		t.Fatalf("expected transcriptFlushCommitMsg from delayed command")
	}
	output := stripANSI(commit.output)
	if count := strings.Count(output, "niuma"); count != 1 {
		t.Fatalf("transcript assistant header repeated around sub-agent: count=%d\n%s", count, output)
	}
	firstLine = lineIndexContaining(output, "先说明")
	agentLine = lineIndexContaining(output, "Sub-agents")
	secondLine = lineIndexContaining(output, "后续总结")
	if firstLine >= 0 || agentLine < 0 || secondLine < 0 || !(agentLine < secondLine) {
		t.Fatalf("transcript tail should contain agent -> second assistant after first phase commit: first=%d agent=%d second=%d\n%s", firstLine, agentLine, secondLine, output)
	}
}

func TestRunPromptStreamQueuesFinishedAfterBufferedEvents(t *testing.T) {
	events := make(chan StreamEvent, 8)
	model := NewModel(context.Background(), Options{RunStream: func(_ context.Context, _ string, events chan<- StreamEvent) error {
		events <- StreamEvent{Type: StreamText, Text: "first"}
		events <- StreamEvent{Type: StreamUsage, Result: &QueryResult{Model: "m"}}
		events <- StreamEvent{Type: StreamText, Text: "late"}
		return nil
	}})

	msg := model.runPromptStream(context.Background(), "prompt", events)()
	if _, ok := msg.(streamProducerDoneMsg); !ok {
		t.Fatalf("stream producer should return no visible response message, got %#v", msg)
	}
	var got []StreamEventType
	for event := range events {
		got = append(got, event.Type)
	}
	want := []StreamEventType{StreamText, StreamUsage, StreamText, StreamFinished}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stream events order = %v, want %v", got, want)
	}
}

func TestModelKeepsAssistantToolAssistantTimelineOrder(t *testing.T) {
	events := make(chan StreamEvent, 8)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	model.streamingActive = true
	model.width = 120
	model.height = 32
	model.transcriptPrintedHeader = true
	model.transcriptPrintedCount = len(model.messages)
	model.refreshViewportAtBottom()

	updated, _ := model.Update(streamEventMsg{event: StreamEvent{Type: StreamText, Text: "先说明：我需要查一下项目。"}, ch: events})
	model = updated.(Model)
	updated, _ = model.Update(streamEventMsg{event: StreamEvent{Type: StreamToolStart, ToolName: "Read", ToolID: "toolu_1", Output: `{"file_path":"README.md"}`}, ch: events})
	model = updated.(Model)
	updated, _ = model.Update(streamEventMsg{event: StreamEvent{Type: StreamToolResult, ToolName: "Read", ToolID: "toolu_1", Output: "read 12 lines"}, ch: events})
	model = updated.(Model)
	updated, _ = model.Update(streamEventMsg{event: StreamEvent{Type: StreamText, Text: "查完之后的总结必须在工具下面。"}, ch: events})
	model = updated.(Model)

	live := stripANSI(model.liveTranscriptView())
	firstLine := lineIndexContaining(live, "先说明")
	toolLine := lineIndexContaining(live, "查看文件")
	secondLine := lineIndexContaining(live, "查完之后")
	if firstLine >= 0 || toolLine < 0 || secondLine < 0 || !(toolLine < secondLine) {
		t.Fatalf("live timeline should retain tool -> second assistant after first commit: first=%d tool=%d second=%d\n%s", firstLine, toolLine, secondLine, live)
	}

	updated, _ = model.Update(streamEventMsg{event: StreamEvent{Type: StreamUsage, Result: &QueryResult{Model: "m", ToolCalls: []ToolCall{{ID: "toolu_1", Name: "Read"}}}}, ch: events})
	model = updated.(Model)
	model.busy = false
	model.streamingActive = false
	transcript := stripANSI(strings.Join(model.pendingTranscriptBlocks(), "\n"))
	firstLine = lineIndexContaining(transcript, "先说明")
	toolLine = lineIndexContaining(transcript, "查看文件")
	secondLine = lineIndexContaining(transcript, "查完之后")
	if firstLine >= 0 || toolLine < 0 || secondLine < 0 || !(toolLine < secondLine) {
		t.Fatalf("transcript tail should retain tool -> second assistant after first commit: first=%d tool=%d second=%d\n%s", firstLine, toolLine, secondLine, transcript)
	}
}

func TestModelFlushesCompletedTurnInLiveDisplayOrder(t *testing.T) {
	events := make(chan StreamEvent, 4)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	model.width = 100
	model.height = 32
	model.transcriptPrintedCount = len(model.messages)
	model.refreshViewportAtBottom()

	updated, _ := model.Update(streamEventMsg{event: StreamEvent{Type: StreamToolStart, ToolName: "Read", ToolID: "toolu_1", Output: `{"file_path":"README.md"}`}, ch: events})
	model = updated.(Model)
	updated, _ = model.Update(streamEventMsg{event: StreamEvent{Type: StreamToolResult, ToolName: "Read", ToolID: "toolu_1", Output: "read 12 lines"}, ch: events})
	model = updated.(Model)
	updated, _ = model.Update(streamEventMsg{event: StreamEvent{Type: StreamText, Text: "解释工具结果"}, ch: events})
	model = updated.(Model)
	updated, _ = model.Update(streamEventMsg{event: StreamEvent{Type: StreamUsage, Result: &QueryResult{Model: "m", ToolCalls: []ToolCall{{ID: "toolu_1", Name: "Read"}}}}, ch: events})
	model = updated.(Model)
	model.busy = false
	model.streamingActive = false

	blocks := model.pendingTranscriptBlocks()
	joined := stripANSI(strings.Join(blocks, "\n"))
	toolLine := lineIndexContaining(joined, "查看文件")
	answerLine := lineIndexContaining(joined, "解释工具结果")
	if toolLine < 0 || answerLine < 0 || toolLine >= answerLine {
		t.Fatalf("completed transcript should preserve live display order: tool=%d answer=%d\n%s", toolLine, answerLine, joined)
	}
}

func TestModelClearsLiveBlocksAfterTranscriptFlush(t *testing.T) {
	events := make(chan StreamEvent, 4)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	model.streamingActive = true
	model.width = 100
	model.height = 32
	model.transcriptPrintedCount = len(model.messages)
	model.refreshViewportAtBottom()

	updated, _ := model.Update(streamEventMsg{event: StreamEvent{Type: StreamText, Text: "unique live answer"}, ch: events})
	model = updated.(Model)
	if !strings.Contains(stripANSI(model.liveTranscriptView()), "unique live answer") {
		t.Fatalf("live answer missing before flush:\n%s", stripANSI(model.liveTranscriptView()))
	}
	updated, _ = model.Update(streamEventMsg{event: StreamEvent{Type: StreamUsage, Result: &QueryResult{Model: "m"}}, ch: events})
	model = updated.(Model)
	model.busy = false
	model.streamingActive = false
	blocks := model.pendingTranscriptBlocks()
	if !strings.Contains(stripANSI(strings.Join(blocks, "\n")), "unique live answer") {
		t.Fatalf("flush blocks missing answer: %+v", blocks)
	}
	model.markTranscriptPrinted()
	model.refreshViewport()
	if live := stripANSI(model.liveTranscriptView()); strings.Contains(live, "unique live answer") {
		t.Fatalf("flushed answer should not remain live:\n%s", live)
	}
	if len(model.liveDisplayBlocks) != 0 {
		t.Fatalf("live display blocks should be pruned after flush: %+v", model.liveDisplayBlocks)
	}
}

func TestModelHidesCompletedStreamingTurnBeforeTranscriptPrint(t *testing.T) {
	events := make(chan StreamEvent, 4)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.width = 120
	model.height = 36
	model.transcriptPrintedHeader = true
	model.busy = true
	model.streamingActive = true
	model.transcriptPrintedCount = len(model.messages)

	answer := "内容覆盖：AI认知入门、工具选择矩阵、行业角色案例、技能包、提示词库等，全部中英文双语。"
	updated, _ := model.Update(streamEventMsg{event: StreamEvent{Type: StreamText, Text: answer}, ch: events})
	model = updated.(Model)
	if !strings.Contains(stripANSI(model.liveTranscriptView()), "内容覆盖：AI认知入门") {
		t.Fatalf("streaming answer should be live before completion:\n%s", stripANSI(model.liveTranscriptView()))
	}
	updated, cmd := model.Update(responseMsg{})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected delayed transcript commit command")
	}
	for _, rendered := range []string{stripANSI(model.liveTranscriptView()), stripANSI(model.viewport.View()), stripANSI(model.View())} {
		if strings.Contains(rendered, "内容覆盖") || strings.Contains(rendered, "AI认知入门") {
			t.Fatalf("completed answer must leave live layer before terminal print:\n%s", rendered)
		}
	}
	if model.transcriptCommittingSeq != model.transcriptPrintedSeq {
		t.Fatalf("committing seq=%d printed seq=%d", model.transcriptCommittingSeq, model.transcriptPrintedSeq)
	}
	commit, ok := cmd().(transcriptFlushCommitMsg)
	if !ok {
		t.Fatalf("expected transcriptFlushCommitMsg from delayed command")
	}
	if !strings.Contains(stripANSI(commit.output), "内容覆盖：AI认知入门") {
		t.Fatalf("commit output should keep answer contiguous:\n%s", stripANSI(commit.output))
	}
}

func TestModelTenTurnTranscriptFlushDoesNotDuplicateLiveAnswers(t *testing.T) {
	events := make(chan StreamEvent, 4)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.width = 100
	model.height = 32
	var transcript []string
	for turn := 1; turn <= 10; turn++ {
		prompt := fmt.Sprintf("prompt-%02d", turn)
		answer := fmt.Sprintf("ANSWER_UNIQUE_%02d", turn)
		model.appendDisplayMessage("user", prompt, "")
		model.busy = true
		model.streamingActive = true
		blocks := model.pendingTranscriptBlocks()
		transcript = append(transcript, blocks...)
		model.markTranscriptPrinted()
		model.refreshViewport()

		updated, _ := model.Update(streamEventMsg{event: StreamEvent{Type: StreamText, Text: answer}, ch: events})
		model = updated.(Model)
		updated, _ = model.Update(streamEventMsg{event: StreamEvent{Type: StreamUsage, Result: &QueryResult{Model: "m"}}, ch: events})
		model = updated.(Model)
		model.busy = false
		model.streamingActive = false
		blocks = model.pendingTranscriptBlocks()
		transcript = append(transcript, blocks...)
		model.markTranscriptPrinted()
		model.refreshViewport()
		if live := stripANSI(model.liveTranscriptView()); strings.Contains(live, answer) {
			t.Fatalf("turn %d answer remained live after flush:\n%s", turn, live)
		}
	}
	joined := stripANSI(strings.Join(transcript, "\n"))
	for turn := 1; turn <= 10; turn++ {
		answer := fmt.Sprintf("ANSWER_UNIQUE_%02d", turn)
		if count := strings.Count(joined, answer); count != 1 {
			t.Fatalf("%s count = %d, want 1\n%s", answer, count, joined)
		}
	}
}

func TestModelRunningStatusFollowsStreamEvents(t *testing.T) {
	events := make(chan StreamEvent, 2)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	model.width = 80
	model.height = 24
	model.messages = append(model.messages, message{role: "user", content: "hi"})
	model.refreshViewportAtBottom()

	view := model.View()
	if !strings.Contains(view, "niuma is thinking...") {
		t.Fatalf("view missing default thinking status:\n%s", view)
	}

	updated, _ := model.Update(streamEventMsg{event: StreamEvent{Type: StreamThinking, Text: "considering"}, ch: events})
	model = updated.(Model)
	view = model.View()
	if !strings.Contains(view, "niuma is reasoning...") {
		t.Fatalf("view missing reasoning status:\n%s", view)
	}

	updated, _ = model.Update(streamEventMsg{event: StreamEvent{Type: StreamToolStart, ToolName: "Read", ToolID: "toolu_1"}, ch: events})
	model = updated.(Model)
	view = model.View()
	if !strings.Contains(view, "niuma is using 查看文件...") {
		t.Fatalf("view missing tool status:\n%s", view)
	}

	updated, _ = model.Update(streamEventMsg{event: StreamEvent{Type: StreamToolResult, ToolName: "Read", ToolID: "toolu_1", Output: "ok"}, ch: events})
	model = updated.(Model)
	view = model.View()
	if !strings.Contains(view, "niuma is processing tool results...") {
		t.Fatalf("view missing tool result status:\n%s", view)
	}

	updated, _ = model.Update(streamEventMsg{event: StreamEvent{Type: StreamText, Text: "done"}, ch: events})
	model = updated.(Model)
	view = model.View()
	if !strings.Contains(view, "niuma is writing...") || strings.Contains(view, "niuma is processing tool results...") {
		t.Fatalf("view missing writing status after text:\n%s", view)
	}
}

func TestModelArchivesCompletedToolActivityWithAssistantMessage(t *testing.T) {
	events := make(chan StreamEvent, 2)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	model.messages = append(model.messages, message{role: "assistant", content: "done"})
	updated, _ := model.Update(streamEventMsg{event: StreamEvent{Type: StreamToolStart, ToolName: "Read", ToolID: "toolu_1"}, ch: events})
	model = updated.(Model)
	updated, _ = model.Update(streamEventMsg{event: StreamEvent{Type: StreamToolResult, ToolName: "Read", ToolID: "toolu_1", Output: "read 12 lines"}, ch: events})
	model = updated.(Model)
	updated, _ = model.Update(streamEventMsg{event: StreamEvent{Type: StreamUsage, Result: &QueryResult{Model: "m", ToolCalls: []ToolCall{{ID: "toolu_1", Name: "Read"}}}}, ch: events})
	model = updated.(Model)
	updated, cmd := model.Update(streamEventMsg{event: StreamEvent{Type: StreamFinished}, ch: events})
	model = updated.(Model)
	if len(model.toolActivity) != 0 {
		t.Fatalf("tool activity should be archived, got %+v", model.toolActivity)
	}
	if len(model.messages) == 0 || len(model.messages[len(model.messages)-1].tools) != 1 {
		t.Fatalf("assistant message missing archived tools: %+v", model.messages)
	}
	commit, ok := cmd().(transcriptFlushCommitMsg)
	if !ok {
		t.Fatalf("expected transcriptFlushCommitMsg from delayed command")
	}
	output := stripANSI(commit.output)
	if strings.Contains(output, "\nTools") {
		t.Fatalf("archived tool should not render in the legacy message-bottom panel:\n%s", output)
	}
	if !strings.Contains(output, "✓") || !strings.Contains(output, "文件：read 12 lines") || !strings.Contains(output, "完成：读取 1 行") {
		t.Fatalf("archived tool not rendered with message:\n%s", output)
	}
}

func TestModelDoesNotRewritePrintedAssistantMetaFromUsageOnlyTurn(t *testing.T) {
	events := make(chan StreamEvent, 2)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	originalMeta := "Responded in 2s · model=glm-5.1 · tokens in/out=100/3 · cache create/read=0/20 turn_hit=20.0% · tools=0"
	model.busy = true
	model.streamingActive = true
	model.transcriptPrintedHeader = true
	model.messages = append(model.messages, message{role: "assistant", content: "previous answer", meta: originalMeta})
	model.transcriptPrintedCount = len(model.messages)

	updated, _ := model.Update(streamEventMsg{event: StreamEvent{Type: StreamUsage, Result: &QueryResult{
		Model: "glm-5.1",
		Turns: 1,
		Usage: Usage{
			InputTokens:          100,
			OutputTokens:         3,
			CacheReadInputTokens: 50,
		},
	}}, ch: events})
	model = updated.(Model)
	updated, cmd := model.Update(streamEventMsg{event: StreamEvent{Type: StreamFinished}, ch: events})
	model = updated.(Model)

	if got := model.messages[0].meta; got != originalMeta {
		t.Fatalf("printed assistant meta was rewritten:\ngot  %q\nwant %q", got, originalMeta)
	}
	if cmd != nil {
		if commit, ok := cmd().(transcriptFlushCommitMsg); ok {
			t.Fatalf("usage-only turn should not print a rewritten meta block:\n%s", stripANSI(commit.output))
		}
		t.Fatalf("usage-only turn returned unexpected command %T", cmd)
	}
	if pending := strings.Join(model.pendingTranscriptBlocks(), "\n"); strings.Contains(pending, "turn_hit=50.0%") {
		t.Fatalf("usage-only turn leaked new cache hit meta into pending transcript:\n%s", pending)
	}
}

func TestModelTaskToolInlineViewOmitsRedundantCompletedSummary(t *testing.T) {
	model := NewModel(context.Background(), Options{})
	model.width = 100
	model.toolActivity = []toolActivityItem{{
		ToolName: "Task",
		ToolID:   "toolu_task",
		Status:   "done",
		Detail:   "简单测试 subagent",
		Result:   toolResultSummary("Task", "completed"),
		Elapsed:  2 * time.Second,
	}}
	view := model.toolCallInlineView(model.toolActivity)
	if !strings.Contains(view, "子任务") || !strings.Contains(view, "简单测试 subagent") || !strings.Contains(view, "✓") {
		t.Fatalf("task inline view missing essentials:\n%s", view)
	}
	if strings.Contains(view, "→ completed") {
		t.Fatalf("task inline view should omit redundant completed result:\n%s", view)
	}
}

func TestModelTaskToolInlineViewShowsCapabilityLoopSummary(t *testing.T) {
	output := `{
		"content":"` + strings.Repeat("long hidden content ", 40) + `",
		"capability_loop":{
			"evidence":["internal/query/query.go:3928 forwards agent evidence"],
			"assumptions":["parent must synthesize the result"],
			"unknowns":["live transcript not checked"],
			"verification":["go test ./internal/tui -count=1"],
			"risks":["summary may omit secondary findings"],
			"next_action":"inspect stored evidence before final answer",
			"follow_up_id":"tool:toolu_pending",
			"resolved_follow_up":"resolved pending inspection",
			"supersedes_evidence_id":"tool:toolu_pending",
			"supersedes_evidence_ids":["task:1"]
		}
	}`
	result := toolResultSummary("Task", output)
	for _, want := range []string{
		"capability_loop:",
		"evidence: internal/query/query.go:3928 forwards agent evidence",
		"unknowns: live transcript not checked",
		"verification: go test ./internal/tui -count=1",
		"risks: summary may omit secondary findings",
		"next_action: inspect stored evidence before final answer",
		"follow_up_id: tool:toolu_pending",
		"resolved_follow_up: resolved pending inspection",
		"supersedes_evidence_id: tool:toolu_pending,task:1",
	} {
		if !strings.Contains(result, want) {
			t.Fatalf("capability loop summary missing %q:\n%s", want, result)
		}
	}
	if strings.Contains(result, "long hidden content") {
		t.Fatalf("capability loop summary should not expose raw content:\n%s", result)
	}

	model := NewModel(context.Background(), Options{})
	model.width = 160
	model.toolActivity = []toolActivityItem{{
		ToolName: "Task",
		ToolID:   "toolu_task",
		Status:   "done",
		Detail:   "evidence audit",
		Result:   result,
		Elapsed:  time.Second,
	}}
	view := model.toolCallInlineView(model.toolActivity)
	if !strings.Contains(view, "子任务") || !strings.Contains(view, "evidence audit") || !strings.Contains(view, "capability_loop:") {
		t.Fatalf("task inline view missing capability loop summary:\n%s", view)
	}
	if strings.Contains(view, "long hidden content") {
		t.Fatalf("task inline view should not expose raw content:\n%s", view)
	}
}

func TestModelTaskToolInlineViewShowsCapabilityLoopWrapperSummary(t *testing.T) {
	output := strings.Join([]string{
		"subagent raw answer " + strings.Repeat("hidden detail ", 40),
		"<capability_loop>",
		`{"capability_loop":{"evidence":["internal/tools/task/task.go returns structured parent context"],"assumptions":["parent sees synchronous Task tool_result"],"unknowns":["live original baseline not sampled"],"verification":["go test ./internal/tui -run CapabilityLoop -count=1"],"risks":["large raw output could hide the decision fields"],"next_action":"surface wrapper fields in TUI before final synthesis"}}`,
		"</capability_loop>",
		"These structured fields are parent decision context; carry evidence, assumptions, unknowns, verification, risks, and next_action forward before finalizing, retrying, or recovering.",
	}, "\n")
	result := toolResultSummary("Task", output)
	for _, want := range []string{
		"capability_loop:",
		"evidence: internal/tools/task/task.go returns structured parent context",
		"assumptions: parent sees synchronous Task tool_result",
		"unknowns: live original baseline not sampled",
		"verification: go test ./internal/tui -run CapabilityLoop -count=1",
		"risks: large raw output could hide the decision fields",
		"next_action: surface wrapper fields in TUI before final synthesis",
	} {
		if !strings.Contains(result, want) {
			t.Fatalf("capability loop wrapper summary missing %q:\n%s", want, result)
		}
	}
	if strings.Contains(result, "hidden detail") || strings.Contains(result, "parent decision context") {
		t.Fatalf("capability loop wrapper summary should not expose raw wrapper prose:\n%s", result)
	}

	model := NewModel(context.Background(), Options{})
	model.width = 160
	model.toolActivity = []toolActivityItem{{
		ToolName: "Task",
		ToolID:   "toolu_task",
		Status:   "done",
		Detail:   "wrapper evidence audit",
		Result:   result,
		Elapsed:  time.Second,
	}}
	view := model.toolCallInlineView(model.toolActivity)
	if !strings.Contains(view, "子任务") || !strings.Contains(view, "wrapper evidence audit") || !strings.Contains(view, "capability_loop:") {
		t.Fatalf("task inline view missing capability loop wrapper summary:\n%s", view)
	}
	if strings.Contains(view, "hidden detail") || strings.Contains(view, "parent decision context") {
		t.Fatalf("task inline view should not expose raw wrapper prose:\n%s", view)
	}
}

func TestModelAgentGetInlineViewShowsNestedCapabilityLoopSummary(t *testing.T) {
	output := `{
		"task":{"id":42,"status":"completed"},
		"result":{
			"content_preview":"final finding",
			"capability_loop":{
				"evidence":["internal/tools/agent/agent.go:578 exposes sanitized capability_loop"],
				"verification":["go test ./internal/tools/agent -count=1"],
				"next_action":"parent verifies anchor"
			}
		}
	}`
	result := toolResultSummary("AgentGet", output)
	for _, want := range []string{
		"capability_loop:",
		"evidence: internal/tools/agent/agent.go:578 exposes sanitized capability_loop",
		"verification: go test ./internal/tools/agent -count=1",
		"next_action: parent verifies anchor",
	} {
		if !strings.Contains(result, want) {
			t.Fatalf("AgentGet capability loop summary missing %q:\n%s", want, result)
		}
	}
	if strings.Contains(result, "content_preview") || strings.Contains(result, "final finding") {
		t.Fatalf("AgentGet summary should stay focused on capability loop:\n%s", result)
	}
}

func TestModelTodoWriteInlineViewSummarizesWithoutRawJSON(t *testing.T) {
	events := make(chan StreamEvent, 3)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	model.width = 120
	input := `{"todos":[{"content":"读 docs 背景","status":"completed"},{"content":"实现 A1-A8","status":"in_progress","priority":"high"},{"content":"跑测试","status":"pending"}]}`

	updated, _ := model.Update(streamEventMsg{ch: events, event: StreamEvent{
		Type:     StreamToolStart,
		ToolName: "TodoWrite",
		ToolID:   "toolu_todo",
		Output:   input,
	}})
	model = updated.(Model)
	view := model.toolCallInlineView(model.toolActivity)
	if !strings.Contains(view, "更新任务") || !strings.Contains(view, "3 个任务：实现 A1-A8") {
		t.Fatalf("todo inline view missing semantic summary:\n%s", view)
	}
	if strings.Contains(view, `{"todos"`) || strings.Contains(view, `"content"`) {
		t.Fatalf("todo inline view should not expose raw JSON:\n%s", view)
	}

	updated, _ = model.Update(streamEventMsg{ch: events, event: StreamEvent{
		Type:     StreamToolResult,
		ToolName: "TodoWrite",
		ToolID:   "toolu_todo",
		Input:    input,
		Output:   "Saved 3 todos to /tmp/project/.niuma-legacy/todos.json",
	}})
	model = updated.(Model)
	view = model.toolCallInlineView(model.toolActivity)
	if !strings.Contains(view, "✓") || !strings.Contains(view, "完成：已保存 3 个任务") {
		t.Fatalf("todo inline view missing completed summary:\n%s", view)
	}
	if strings.Contains(view, "status=") || strings.Contains(view, "result=") || strings.Contains(view, "saved 3 todos") {
		t.Fatalf("todo inline view should use user-facing copy:\n%s", view)
	}
	if strings.Contains(view, `{"todos"`) || strings.Contains(view, `"content"`) {
		t.Fatalf("completed todo inline view should not expose raw JSON:\n%s", view)
	}
}

func TestModelTodoWriteUsesActiveFormAndNewResultSummary(t *testing.T) {
	events := make(chan StreamEvent, 3)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	model.width = 120
	input := `{"todos":[{"content":"读 docs 背景","status":"completed"},{"content":"实现 A1-A8","activeForm":"正在实现 A1-A8","status":"in_progress","priority":"high"},{"content":"跑测试","status":"pending"}]}`

	updated, _ := model.Update(streamEventMsg{ch: events, event: StreamEvent{
		Type:     StreamToolStart,
		ToolName: "TodoWrite",
		ToolID:   "toolu_todo",
		Output:   input,
	}})
	model = updated.(Model)
	view := model.toolCallInlineView(model.toolActivity)
	if !strings.Contains(view, "更新任务") || !strings.Contains(view, "3 个任务：正在实现 A1-A8") {
		t.Fatalf("todo inline view missing activeForm summary:\n%s", view)
	}

	updated, _ = model.Update(streamEventMsg{ch: events, event: StreamEvent{
		Type:     StreamToolResult,
		ToolName: "TodoWrite",
		ToolID:   "toolu_todo",
		Input:    input,
		Output:   "Todos have been modified successfully. Ensure that you continue to use the todo list to track your progress.\n\nSummary: 3 total, 1 pending, 1 in_progress, 1 completed.",
	}})
	model = updated.(Model)
	view = model.toolCallInlineView(model.toolActivity)
	if !strings.Contains(view, "✓") || !strings.Contains(view, "完成：已更新 3 个任务") {
		t.Fatalf("todo inline view missing new completed summary:\n%s", view)
	}
	if strings.Contains(view, "status=") || strings.Contains(view, "result=") || strings.Contains(view, "updated 3 todos") {
		t.Fatalf("todo inline view should use user-facing copy:\n%s", view)
	}
	full := model.View()
	if !strings.Contains(full, "Task: 正在实现 A1-A8") {
		t.Fatalf("running status should use activeForm:\n%s", full)
	}
}

func TestModelCompletedTodoArchivesIntoTranscriptAndLeavesFooter(t *testing.T) {
	events := make(chan StreamEvent, 4)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	model.width = 120
	model.transcriptPrintedHeader = true
	model.todos = []todoItem{
		{Content: "创建 Python venv 并安装后端依赖", Status: "completed"},
		{Content: "启动后端服务", Status: "in_progress"},
	}
	input := `{"todos":[{"content":"创建 Python venv 并安装后端依赖","status":"completed"},{"content":"启动后端服务","status":"completed"}]}`

	updated, _ := model.Update(streamEventMsg{ch: events, event: StreamEvent{
		Type:     StreamToolStart,
		ToolName: "TodoWrite",
		ToolID:   "toolu_todo_done",
		Output:   input,
	}})
	model = updated.(Model)
	updated, _ = model.Update(streamEventMsg{ch: events, event: StreamEvent{
		Type:     StreamToolResult,
		ToolName: "TodoWrite",
		ToolID:   "toolu_todo_done",
		Input:    input,
		Output:   "Todos have been modified successfully.\n\nSummary: 2 total, 0 pending, 0 in_progress, 2 completed. All todos are completed, so the persisted session todo state was cleared.",
	}})
	model = updated.(Model)

	if footer := stripANSI(model.todoProgressView()); footer != "" {
		t.Fatalf("completed todos should not stay in bottom chrome:\n%s", footer)
	}
	pending := stripANSI(strings.Join(model.pendingTranscriptBlocks(), "\n"))
	for _, want := range []string{
		"✓ 本轮任务完成：2 项全部完成",
		"✓ 创建 Python venv 并安装后端依赖",
		"✓ 启动后端服务",
	} {
		if !strings.Contains(pending, want) {
			t.Fatalf("completed todos should be transcript-anchored, missing %q:\n%s", want, pending)
		}
	}
	if strings.Contains(pending, "Tasks 2/2") || strings.Contains(pending, "ctrl+y expand") {
		t.Fatalf("completed transcript item should not use footer copy:\n%s", pending)
	}
	if len(model.messages) != 0 {
		t.Fatalf("completed todo archive must not create model-visible messages: %+v", model.messages)
	}
}

func TestModelDiskLoadedCompletedTodosDoNotArchiveIntoTranscript(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, ".niuma"), 0755); err != nil {
		t.Fatal(err)
	}
	data := `[{"content":"历史任务 A","status":"completed"},{"content":"历史任务 B","status":"completed"}]`
	if err := os.WriteFile(filepath.Join(tmp, ".niuma", "todos.json"), []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	model := NewModel(context.Background(), Options{Welcome: WelcomeInfo{CWD: tmp}})

	view := stripANSI(model.View())
	pending := stripANSI(strings.Join(model.pendingTranscriptBlocks(), "\n"))
	for _, notWant := range []string{"Tasks 2/2", "本轮任务完成", "历史任务 A", "历史任务 B"} {
		if strings.Contains(view, notWant) || strings.Contains(pending, notWant) {
			t.Fatalf("disk-loaded completed todos should stay hidden, found %q\nview:\n%s\npending:\n%s", notWant, view, pending)
		}
	}
	if len(model.displayTimeline.segments) != 0 {
		t.Fatalf("disk-loaded completed todos should not create display timeline segments: %+v", model.displayTimeline.segments)
	}
}

func TestFriendlyToolUsageSummaryUsesUserFacingCopy(t *testing.T) {
	panel := usagePanel{
		ToolCalls:   43,
		ToolCount:   32,
		ToolErrors:  2,
		ToolBlocked: 1,
		LastTool:    "Bash",
	}

	full := friendlyToolUsageSummary(panel)
	for _, want := range []string{"执行了 43 次操作", "2 次未完成", "1 次安全保护", "最近使用 运行命令"} {
		if !strings.Contains(full, want) {
			t.Fatalf("friendly usage summary missing %q:\n%s", want, full)
		}
	}
	for _, notWant := range []string{"工具 43/32", "失败", "保护拦截", "最近 Bash"} {
		if strings.Contains(full, notWant) {
			t.Fatalf("friendly usage summary should avoid machine copy %q:\n%s", notWant, full)
		}
	}

	compact := compactToolSummary(panel)
	for _, want := range []string{"操作 43 次", "未完成 2", "安全保护 1"} {
		if !strings.Contains(compact, want) {
			t.Fatalf("compact usage summary missing %q:\n%s", want, compact)
		}
	}
	if strings.Contains(compact, "最近") {
		t.Fatalf("compact usage summary should stay short in narrow chrome:\n%s", compact)
	}
}

func TestModelMultiEditInlineViewSummarizesWithoutRawJSON(t *testing.T) {
	events := make(chan StreamEvent, 3)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	model.width = 140
	input := `{"file_path":"/Users/example/GolandProjects/superPM/CLAUDE.md","edits":[{"old_string":"old one","new_string":"new one"},{"old_string":"old two","new_string":"new two"}]}`

	updated, _ := model.Update(streamEventMsg{ch: events, event: StreamEvent{
		Type:     StreamToolStart,
		ToolName: "MultiEdit",
		ToolID:   "toolu_multiedit",
		Output:   input,
	}})
	model = updated.(Model)
	view := stripANSI(model.toolCallInlineView(model.toolActivity))
	if !strings.Contains(view, "编辑文件") || !strings.Contains(view, "文件：CLAUDE.md · 2 处修改") {
		t.Fatalf("MultiEdit inline view missing semantic file summary:\n%s", view)
	}
	if strings.Contains(view, `"edits"`) || strings.Contains(view, `"new_string"`) || strings.Contains(view, "{") {
		t.Fatalf("MultiEdit inline view should not expose raw JSON:\n%s", view)
	}

	updated, _ = model.Update(streamEventMsg{ch: events, event: StreamEvent{
		Type:     StreamToolResult,
		ToolName: "MultiEdit",
		ToolID:   "toolu_multiedit",
		Input:    input,
		Output:   "write path /Users/example/GolandProjects/superPM/CLAUDE.md is outside the current workspace and configured additional directories",
		IsError:  true,
	}})
	model = updated.(Model)
	view = stripANSI(model.toolCallInlineView(model.toolActivity))
	for _, want := range []string{"!", "编辑文件", "文件：CLAUDE.md · 2 处修改", "已拦截：写入位置不在当前工作区"} {
		if !strings.Contains(view, want) {
			t.Fatalf("MultiEdit blocked view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "failed") || strings.Contains(view, `"edits"`) || strings.Contains(view, `"new_string"`) {
		t.Fatalf("MultiEdit blocked view should use friendly copy:\n%s", view)
	}
}

func TestModelBashErrorInlineViewDoesNotReportExitZero(t *testing.T) {
	events := make(chan StreamEvent, 3)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	model.width = 120

	updated, _ := model.Update(streamEventMsg{ch: events, event: StreamEvent{
		Type:     StreamToolStart,
		ToolName: "Bash",
		ToolID:   "toolu_bash",
		Output:   `{"command":"go test ./..."}`,
	}})
	model = updated.(Model)
	updated, _ = model.Update(streamEventMsg{ch: events, event: StreamEvent{
		Type:     StreamToolResult,
		ToolName: "Bash",
		ToolID:   "toolu_bash",
		Output:   "package .: no go files",
		IsError:  true,
	}})
	model = updated.(Model)

	view := model.toolCallInlineView(model.toolActivity)
	if !strings.Contains(view, "✗") || !strings.Contains(view, "未完成") {
		t.Fatalf("bash error inline view missing failure summary:\n%s", view)
	}
	if strings.Contains(view, "exit 0") {
		t.Fatalf("bash error inline view must not report exit 0:\n%s", view)
	}
}

func TestModelToolExpandedViewShowsInputOutputAndFailureDetails(t *testing.T) {
	model := NewModel(context.Background(), Options{})
	model.width = 72
	model.toolPanelExpanded = true
	model.toolActivity = []toolActivityItem{{
		ToolName:  "Bash",
		ToolID:    "toolu_bash",
		Status:    "error",
		Detail:    "go test ./...",
		Result:    "failed",
		RawInput:  `{"command":"go test ./...","description":"run full tests with a deliberately long detail string"}`,
		RawOutput: "package ./broken: setup failed because one generated file is missing from the workspace",
		IsError:   true,
		Elapsed:   time.Second,
	}}
	view := stripANSI(model.toolCallInlineView(model.toolActivity))
	for _, want := range []string{"运行命令", "✗", "未完成：操作未完成", "失败原因：操作未完成", "输入：", "command=go test ./...", "description=run full tests", "输出：", "package ./broken: setup failed", "file is missing"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expanded tool view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "status=") || strings.Contains(view, "result=") || strings.Contains(view, "failure:") || strings.Contains(view, "input:") || strings.Contains(view, "output:") {
		t.Fatalf("expanded tool view should avoid raw status labels:\n%s", view)
	}
	for _, line := range strings.Split(view, "\n") {
		if got := runewidth.StringWidth(line); got > model.width {
			t.Fatalf("expanded tool detail line width=%d > %d:\n%s\nfull view:\n%s", got, model.width, line, view)
		}
	}
}

func TestToolResultDisplayModeRecognizesKeyGitCommands(t *testing.T) {
	tests := []struct {
		command string
		want    toolResultDisplayMode
	}{
		{"git diff -- file.txt", toolResultDisplayKeyOutput},
		{"git -C repo --no-pager show HEAD", toolResultDisplayKeyOutput},
		{"cd repo && git status --short", toolResultDisplayKeyOutput},
		{"GIT_PAGER=cat git log -3", toolResultDisplayKeyOutput},
		{"go test ./...", toolResultDisplaySummary},
		{"git push origin main", toolResultDisplaySummary},
	}
	for _, tt := range tests {
		item := toolActivityItem{ToolName: "Bash", Status: "done", RawInput: `{"command":"` + tt.command + `"}`}
		if got := toolResultDisplayModeFor(item); got != tt.want {
			t.Errorf("command %q display mode = %d, want %d", tt.command, got, tt.want)
		}
	}
}

func TestModelKeyGitOutputRendersDiffAndTruncates(t *testing.T) {
	model := NewModel(context.Background(), Options{})
	model.width = 96
	model.toolActivity = []toolActivityItem{{
		ToolName:  "Bash",
		ToolID:    "toolu_diff",
		Status:    "done",
		Detail:    "git diff -- file.txt",
		RawInput:  `{"command":"git diff -- file.txt"}`,
		RawOutput: "diff --git a/file.txt b/file.txt\n--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-old\n+new\n" + strings.Repeat("context\n", toolKeyOutputMaxLines),
	}}
	view := model.toolCallInlineView(model.toolActivity)
	plain := stripANSI(view)
	for _, want := range []string{"输出：", "diff --git a/file.txt b/file.txt", "-old", "+new", "@@ -1 +1 @@", "输出较长，按 Ctrl+T 展开"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("key git output missing %q:\n%s", want, plain)
		}
	}
	if !strings.Contains(view, string(toolOutputAddedStyle.Render("      +new"))) ||
		!strings.Contains(view, string(toolOutputRemovedStyle.Render("      -old"))) ||
		!strings.Contains(view, string(toolOutputHunkStyle.Render("      @@ -1 +1 @@"))) {
		t.Fatalf("diff lines did not use dedicated styles:\n%s", view)
	}
	for _, line := range strings.Split(plain, "\n") {
		if got := runewidth.StringWidth(line); got > model.width {
			t.Fatalf("key output line width=%d > %d:\n%s", got, model.width, line)
		}
	}
}

func TestModelOrdinaryBashKeepsSummaryByDefault(t *testing.T) {
	model := NewModel(context.Background(), Options{})
	model.width = 96
	model.toolActivity = []toolActivityItem{{
		ToolName:  "Bash",
		Status:    "done",
		Result:    "exit 0",
		RawInput:  `{"command":"printf hello"}`,
		RawOutput: "hello",
	}}
	view := stripANSI(model.toolCallInlineView(model.toolActivity))
	if strings.Contains(view, "输出：") || strings.Contains(view, "hello") {
		t.Fatalf("ordinary Bash should keep summary by default:\n%s", view)
	}
}

func blockedGateToolViewForTest(t *testing.T) string {
	t.Helper()
	events := make(chan StreamEvent, 3)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	model.width = 96
	model.toolPanelExpanded = true

	updated, _ := model.Update(streamEventMsg{ch: events, event: StreamEvent{
		Type:     StreamToolStart,
		ToolName: "Bash",
		ToolID:   "toolu_push",
		Output:   `{"command":"git push","description":"push to remote"}`,
	}})
	model = updated.(Model)
	updated, _ = model.Update(streamEventMsg{ch: events, event: StreamEvent{
		Type:     StreamToolResult,
		ToolName: "Bash",
		ToolID:   "toolu_push",
		Output:   `<system-reminder>Tool blocked by Shared-State Git Gate: git push requires current branch, object-state, and remote/upstream evidence first. Run git status --short --branch, git rev-parse HEAD, and a read-only remote/upstream check such as git rev-parse @{u}, git branch -vv, or git ls-remote before retrying the shared-state operation.</system-reminder>`,
		IsError:  true,
	}})
	model = updated.(Model)

	return stripANSI(model.toolCallInlineView(model.toolActivity))
}

func TestModelToolGateBlockedViewShowsFriendlyVerificationCopy(t *testing.T) {
	view := blockedGateToolViewForTest(t)
	for _, want := range []string{
		"运行命令",
		"!",
		"已拦截：需要先验证分支/HEAD/远程状态",
		"拦截器：Shared-State Git Gate",
		"拦截原因：git push 会修改远程共享状态，命令尚未执行",
		"下一步：",
		"git status --short --branch",
		"git rev-parse HEAD",
		"git rev-parse @{u}",
		"输入：",
		"command=git push",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("blocked tool view missing %q:\n%s", want, view)
		}
	}
	for _, notWant := range []string{"✗", "失败：", "失败原因：", "Tool blocked by Shared-State Git Gate", "system-reminder"} {
		if strings.Contains(view, notWant) {
			t.Fatalf("blocked tool view should not show %q:\n%s", notWant, view)
		}
	}
}

func TestModelGatePreflightViewShowsFriendlyCompletedCheck(t *testing.T) {
	events := make(chan StreamEvent, 3)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	model.width = 120
	model.toolPanelExpanded = true
	preflightInput := `{"command":"git status --short --branch && git diff --name-status && git diff --cached --name-status"}`

	updated, _ := model.Update(streamEventMsg{ch: events, event: StreamEvent{
		Type:     StreamToolStart,
		ToolName: "Bash",
		ToolID:   "toolu_commit",
		Output:   `{"command":"git commit -m tui"}`,
	}})
	model = updated.(Model)
	updated, _ = model.Update(streamEventMsg{ch: events, event: StreamEvent{
		Type:     StreamToolResult,
		ToolName: "Bash",
		ToolID:   "toolu_commit",
		Input:    preflightInput,
		Output: `<gate-preflight rule_id="pre_commit_scope" status="completed" title="提交前范围检查">
<system-reminder>已完成提交前检查，原提交命令尚未执行. Original command was not executed: git commit -m tui.</system-reminder>
<preflight-output>
## main...origin/main
M	internal/tui/app.go
</preflight-output>
</gate-preflight>`,
	}})
	model = updated.(Model)

	view := stripANSI(model.toolCallInlineView(model.toolActivity))
	for _, want := range []string{
		"运行命令",
		"✓",
		"命令：git status --short --branch && git diff --name-status",
		"完成：已完成提交前检查",
		"输入：",
		"command=git status --short --branch",
		"已完成提交前检查；原命令未执行",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("gate preflight view missing %q:\n%s", want, view)
		}
	}
	for _, notWant := range []string{"失败", "failed", "system-reminder", "git commit -m tui"} {
		if strings.Contains(view, notWant) {
			t.Fatalf("gate preflight view should not show %q:\n%s", notWant, view)
		}
	}
}

func TestModelToolGateBlockedViewCanBeExportedForManualReview(t *testing.T) {
	path := os.Getenv("NIUMA_TUI_BLOCKED_GATE_VIEW_OUT")
	if strings.TrimSpace(path) == "" {
		t.Skip("set NIUMA_TUI_BLOCKED_GATE_VIEW_OUT to export the blocked gate view")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("create output dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(blockedGateToolViewForTest(t)+"\n"), 0644); err != nil {
		t.Fatalf("write blocked gate view: %v", err)
	}
}

func TestModelUsageSeparatesBlockedGateFromToolErrors(t *testing.T) {
	model := NewModel(context.Background(), Options{})
	model.width = 140
	model.applyQueryResult(QueryResult{
		Model: "m",
		ToolCalls: []ToolCall{{
			ID:      "toolu_push",
			Name:    "Bash",
			Output:  `<system-reminder>Tool blocked by Shared-State Git Gate: git push requires current branch, object-state, and remote/upstream evidence first.</system-reminder>`,
			IsError: true,
		}, {
			ID:      "toolu_test",
			Name:    "Bash",
			Output:  "exit status 1\nFAIL",
			IsError: true,
		}},
		Context: RuntimeContext{ToolCount: 32},
	})

	view := stripANSI(model.usageView())
	for _, want := range []string{"执行了 2 次操作 · 1 次未完成 · 1 次安全保护 · 最近使用 运行命令"} {
		if !strings.Contains(view, want) {
			t.Fatalf("usage view missing %q:\n%s", want, view)
		}
	}
}

func TestModelTranscriptToolViewIsSelfContainedWithoutExpandHint(t *testing.T) {
	model := NewModel(context.Background(), Options{})
	model.width = 140
	model.transcriptPrintedHeader = true
	msg := message{role: "assistant", content: "done"}
	for i := 1; i <= 5; i++ {
		msg.tools = append(msg.tools, toolActivityItem{
			ToolName: fmt.Sprintf("Tool%d", i),
			ToolID:   fmt.Sprintf("toolu_%d", i),
			Status:   "done",
			Detail:   fmt.Sprintf("step-%d", i),
			Result:   "ok",
		})
	}
	model.messages = append(model.messages, msg)

	live := stripANSI(model.renderMessage(model.messages[0]))
	if !strings.Contains(live, "... 还有 2 次工具操作  ctrl+t 展开") {
		t.Fatalf("live tool view should stay interactive and collapsed:\n%s", live)
	}
	if strings.Contains(live, "Tool1") {
		t.Fatalf("live collapsed tool view should only show recent tools:\n%s", live)
	}

	blocks := model.pendingTranscriptBlocks()
	transcript := stripANSI(strings.Join(blocks, "\n"))
	for _, want := range []string{"Tool1", "Tool2", "Tool3", "Tool4", "Tool5"} {
		if !strings.Contains(transcript, want) {
			t.Fatalf("transcript tool view missing %q:\n%s", want, transcript)
		}
	}
	if strings.Contains(transcript, "ctrl+t expand") || strings.Contains(transcript, "ctrl+t collapse") {
		t.Fatalf("printed transcript must not advertise non-interactive ctrl+t controls:\n%s", transcript)
	}
}

func TestModelReadErrorInlineViewDoesNotReportLineCount(t *testing.T) {
	events := make(chan StreamEvent, 3)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	model.width = 120

	updated, _ := model.Update(streamEventMsg{ch: events, event: StreamEvent{
		Type:     StreamToolStart,
		ToolName: "Read",
		ToolID:   "toolu_read",
		Output:   `{"file_path":"README.md"}`,
	}})
	model = updated.(Model)
	updated, _ = model.Update(streamEventMsg{ch: events, event: StreamEvent{
		Type:     StreamToolResult,
		ToolName: "Read",
		ToolID:   "toolu_read",
		Output:   "file not found: README.md",
		IsError:  true,
	}})
	model = updated.(Model)

	view := model.toolCallInlineView(model.toolActivity)
	if !strings.Contains(view, "✗") || !strings.Contains(view, "未找到目标") {
		t.Fatalf("read error inline view missing error summary:\n%s", view)
	}
	if strings.Contains(view, "lines") {
		t.Fatalf("read error inline view must not report line count:\n%s", view)
	}
}

func TestModelTodoWriteUpdatesTaskProgressPanel(t *testing.T) {
	events := make(chan StreamEvent, 4)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	model.width = 100
	input := `{"todos":[{"content":"读 docs 背景","status":"completed"},{"content":"实现 A1-A8","status":"in_progress","priority":"high"},{"content":"跑测试","status":"pending"}]}`
	updated, _ := model.Update(streamEventMsg{ch: events, event: StreamEvent{
		Type:     StreamToolResult,
		ToolName: "TodoWrite",
		ToolID:   "toolu_todo",
		Input:    input,
		Output:   "Saved 3 todos to /tmp/project/.niuma-legacy/todos.json",
	}})
	model = updated.(Model)
	view := model.View()
	for _, want := range []string{"Tasks 1/3", "读 docs 背景", "实现 A1-A8", "跑测试", "high"} {
		if !strings.Contains(view, want) {
			t.Fatalf("todo panel missing %q:\n%s", want, view)
		}
	}
	if !strings.Contains(view, "Task: 实现 A1-A8") {
		t.Fatalf("running status should refresh to current todo after TodoWrite:\n%s", view)
	}
	if strings.Contains(view, "ctrl+y expand") || strings.Contains(view, "还有") {
		t.Fatalf("todo panel should not collapse three tasks:\n%s", view)
	}
}

func TestModelTodoPanelCollapsedDefaultsToFourTasksAroundCurrent(t *testing.T) {
	model := NewModel(context.Background(), Options{})
	model.width = 120
	model.todos = []todoItem{
		{Content: "充实 admin 角色", Status: "completed"},
		{Content: "充实 hr 角色", Status: "completed"},
		{Content: "充实 sales 角色", Status: "completed"},
		{Content: "充实 teacher 角色", Status: "completed"},
		{Content: "充实 finance 角色", Status: "in_progress", Priority: "high"},
		{Content: "充实 planner 角色", Status: "pending", Priority: "high"},
		{Content: "更新侧边栏配置 + 构建验证", Status: "pending", Priority: "high"},
	}

	view := stripANSI(model.View())
	for _, want := range []string{
		"Tasks 4/7",
		"ctrl+y expand",
		"充实 sales 角色",
		"充实 teacher 角色",
		"充实 finance 角色",
		"充实 planner 角色",
		"还有 3 个",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("collapsed todo panel missing %q:\n%s", want, view)
		}
	}
	for _, notWant := range []string{"充实 admin 角色", "充实 hr 角色", "更新侧边栏配置 + 构建验证"} {
		if strings.Contains(view, notWant) {
			t.Fatalf("collapsed todo panel should hide %q:\n%s", notWant, view)
		}
	}
}

func TestModelTodoPanelToggleExpandsAllTasks(t *testing.T) {
	model := NewModel(context.Background(), Options{})
	model.width = 100
	model.todos = []todoItem{
		{Content: "读 docs 背景", Status: "completed"},
		{Content: "实现 A1-A8", Status: "in_progress"},
		{Content: "跑测试", Status: "pending"},
		{Content: "截图验证", Status: "pending"},
		{Content: "提交推送", Status: "pending"},
	}
	collapsed := model.View()
	if strings.Contains(collapsed, "提交推送") || !strings.Contains(collapsed, "ctrl+y expand") {
		t.Fatalf("precondition failed, expected collapsed todo panel:\n%s", collapsed)
	}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	model = updated.(Model)
	expanded := model.View()
	for _, want := range []string{"Tasks 1/5 expanded", "读 docs 背景", "实现 A1-A8", "跑测试", "截图验证", "提交推送", "ctrl+y collapse"} {
		if !strings.Contains(expanded, want) {
			t.Fatalf("expanded todo panel missing %q:\n%s", want, expanded)
		}
	}
}

func TestModelRunningStatusUsesCurrentTodoOnToolStart(t *testing.T) {
	events := make(chan StreamEvent, 2)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	model.width = 100
	model.todos = []todoItem{{Content: "实现任务进度展示", Status: "in_progress"}}
	updated, _ := model.Update(streamEventMsg{ch: events, event: StreamEvent{Type: StreamToolStart, ToolName: "Read", ToolID: "toolu_read"}})
	model = updated.(Model)
	view := model.View()
	if !strings.Contains(view, "Task: 实现任务进度展示") {
		t.Fatalf("running status should show current todo:\n%s", view)
	}
}

func TestModelStreamTimeoutShowsFriendlyStoppedState(t *testing.T) {
	events := make(chan StreamEvent, 4)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	model.streamingActive = true
	model.width = 120
	model.height = 40
	model.toolPanelExpanded = true
	model.agentPanelExpanded = true
	model.transcriptPrintedHeader = true
	model.todos = []todoItem{
		{Content: "充实 finance 角色", Status: "in_progress", Priority: "high"},
		{Content: "充实 planner 角色", Status: "pending", Priority: "high"},
	}
	startedAt := time.Now().Add(-3 * time.Second).Format(time.RFC3339Nano)

	updated, _ := model.Update(streamEventMsg{ch: events, event: StreamEvent{
		Type:     StreamToolStart,
		ToolName: "Bash",
		ToolID:   "toolu_running",
		Output:   `{"command":"npm run build"}`,
	}})
	model = updated.(Model)
	updated, _ = model.Update(streamEventMsg{ch: events, event: StreamEvent{
		Type:    StreamNestedProgress,
		Event:   "started",
		TaskID:  7,
		Payload: json.RawMessage(fmt.Sprintf(`{"agent_name":"general-purpose","model":"glm-5.1","started_at":%q}`, startedAt)),
	}})
	model = updated.(Model)
	timeoutErr := errors.New("read tcp 192.0.2.10:57680->198.51.100.20:443: read: operation timed out")
	updated, cmd := model.Update(streamEventMsg{ch: events, event: StreamEvent{Type: StreamFinished, Err: timeoutErr}})
	model = updated.(Model)

	if model.busy || model.streamingActive {
		t.Fatalf("stream timeout should stop busy state: busy=%v streaming=%v", model.busy, model.streamingActive)
	}
	if len(model.toolActivity) != 1 || model.toolActivity[0].Status != "interrupted" {
		t.Fatalf("running tool should be interrupted: %+v", model.toolActivity)
	}
	if progress := model.agentProgress[7]; progress.status != "interrupted" {
		t.Fatalf("running sub-agent should be interrupted: %+v", progress)
	}
	if cmd == nil {
		t.Fatal("expected transcript flush for timeout turn")
	}
	commit, ok := cmd().(transcriptFlushCommitMsg)
	if !ok {
		t.Fatalf("expected transcriptFlushCommitMsg from timeout turn")
	}
	view := stripANSI(model.View() + "\n" + commit.output)
	for _, want := range []string{
		"网络连接超时，本轮已停止",
		"模型流式响应中断，已保留当前进度",
		"可以直接输入“继续”",
		"详情：read tcp 192.0.2.10:57680",
		"! 充实 finance 角色",
		"已中断：网络连接超时，本轮已停止",
		"interrupted",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("timeout view missing %q:\n%s", want, view)
		}
	}
	for _, notWant := range []string{"niuma is preparing context", "niuma is coordinating sub-agents"} {
		if strings.Contains(view, notWant) {
			t.Fatalf("timeout view should not show running status %q:\n%s", notWant, view)
		}
	}
}

func TestModelTaskNetworkErrorViewCanBeExportedForManualReview(t *testing.T) {
	path := strings.TrimSpace(os.Getenv("NIUMA_TUI_TASK_NETWORK_ERROR_OUT"))
	if path == "" {
		t.Skip("set NIUMA_TUI_TASK_NETWORK_ERROR_OUT to export the task/network error view")
	}
	events := make(chan StreamEvent, 4)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	model.streamingActive = true
	model.width = 120
	model.height = 42
	model.toolPanelExpanded = true
	model.agentPanelExpanded = true
	model.transcriptPrintedHeader = true
	model.todos = []todoItem{
		{Content: "充实 admin 角色", Status: "completed", Priority: "high"},
		{Content: "充实 hr 角色", Status: "completed", Priority: "high"},
		{Content: "充实 sales 角色", Status: "completed", Priority: "high"},
		{Content: "充实 teacher 角色", Status: "completed", Priority: "high"},
		{Content: "充实 finance 角色", Status: "in_progress", Priority: "high"},
		{Content: "充实 planner 角色", Status: "pending", Priority: "high"},
		{Content: "更新 VitePress 侧边栏配置 + 构建验证", Status: "pending", Priority: "high"},
	}
	updated, _ := model.Update(streamEventMsg{ch: events, event: StreamEvent{
		Type:     StreamToolStart,
		ToolName: "Bash",
		ToolID:   "toolu_build",
		Output:   `{"command":"npm run build","description":"构建验证"}`,
	}})
	model = updated.(Model)
	updated, _ = model.Update(streamEventMsg{ch: events, event: StreamEvent{
		Type:    StreamNestedProgress,
		Event:   "started",
		TaskID:  4,
		Payload: json.RawMessage(fmt.Sprintf(`{"agent_name":"general-purpose","model":"glm-5.1","started_at":%q}`, time.Now().Add(-3*time.Second).Format(time.RFC3339Nano))),
	}})
	model = updated.(Model)
	timeoutErr := errors.New("read tcp 192.0.2.10:57680->198.51.100.20:443: read: operation timed out")
	updated, cmd := model.Update(streamEventMsg{ch: events, event: StreamEvent{Type: StreamFinished, Err: timeoutErr}})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected transcript flush for timeout turn")
	}
	commit, ok := cmd().(transcriptFlushCommitMsg)
	if !ok {
		t.Fatalf("expected transcriptFlushCommitMsg from timeout turn")
	}
	view := stripANSI(commit.output + "\n\n" + model.View())
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("create output dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(view+"\n"), 0644); err != nil {
		t.Fatalf("write task/network error view: %v", err)
	}
}

func TestModelLoadsTodosFromWelcomeCWD(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, ".niuma"), 0755); err != nil {
		t.Fatal(err)
	}
	data := `[{"content":"恢复任务","status":"in_progress","priority":"high"}]`
	if err := os.WriteFile(filepath.Join(tmp, ".niuma", "todos.json"), []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	model := NewModel(context.Background(), Options{Welcome: WelcomeInfo{CWD: tmp}})
	view := model.View()
	if !strings.Contains(view, "恢复任务") || !strings.Contains(view, "Tasks 0/1") {
		t.Fatalf("model should load todos from welcome cwd:\n%s", view)
	}
}

func TestModelReloadsTodosFromResumeWelcomeCWD(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, ".niuma"), 0755); err != nil {
		t.Fatal(err)
	}
	data := `[{"content":"恢复后的任务","status":"in_progress"}]`
	if err := os.WriteFile(filepath.Join(tmp, ".niuma", "todos.json"), []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	events := make(chan StreamEvent, 2)
	model := NewModel(context.Background(), Options{Welcome: WelcomeInfo{CWD: t.TempDir()}})
	updated, _ := model.Update(streamEventMsg{ch: events, event: StreamEvent{Type: StreamSessionResume, Welcome: &WelcomeInfo{CWD: tmp}}})
	model = updated.(Model)
	view := model.View()
	if !strings.Contains(view, "恢复后的任务") || !strings.Contains(view, "Tasks 0/1") {
		t.Fatalf("resume should reload todos from event welcome cwd:\n%s", view)
	}
}

func TestModelRunningHintShowsElapsedAndTokenState(t *testing.T) {
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	model.turnStarted = time.Now().Add(-2 * time.Second)
	updated, cmd := model.Update(tickMsg(time.Now()))
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("busy tick should schedule another tick")
	}
	view := model.View()
	if !strings.Contains(view, "elapsed=") || !strings.Contains(view, "tokens pending") {
		t.Fatalf("view missing running elapsed/token state:\n%s", view)
	}

	model.usage = usagePanel{InputTokens: 10, OutputTokens: 3}
	view = model.View()
	if !strings.Contains(view, "session tokens in/out=10/3") {
		t.Fatalf("view missing session token usage:\n%s", view)
	}
}

func TestModelBackgroundWatcherShowsLoopFireAfterBaseline(t *testing.T) {
	calls := 0
	model := NewModel(context.Background(), Options{
		Run: func(context.Context, string) (string, error) { return "ok", nil },
		WatchBackground: func(context.Context, map[string]BackgroundSnapshot) ([]BackgroundUpdate, error) {
			calls++
			if calls == 1 {
				return []BackgroundUpdate{{
					ID:       "bg_loop",
					Prompt:   "drink water",
					Status:   "running",
					RunCount: 3,
					LogSize:  20,
					LogTail:  "old output",
					Silent:   true,
				}}, nil
			}
			now := time.Date(2026, 6, 26, 8, 0, 0, 0, time.UTC)
			return []BackgroundUpdate{{
				ID:         "bg_loop",
				ScheduleID: "sched_loop",
				Prompt:     "drink water",
				Status:     "running",
				RunCount:   4,
				LastRunAt:  &now,
				LogSize:    44,
				LogTail:    "remember to drink water",
			}}, nil
		},
	})

	updated, _ := model.Update(backgroundPollMsg{updates: []BackgroundUpdate{{
		ID:       "bg_loop",
		Prompt:   "drink water",
		Status:   "running",
		RunCount: 3,
		LogSize:  20,
		LogTail:  "old output",
		Silent:   true,
	}}})
	model = updated.(Model)
	if strings.Contains(model.View(), "old output") {
		t.Fatalf("silent baseline should not render old loop output:\n%s", model.View())
	}

	now := time.Date(2026, 6, 26, 8, 0, 0, 0, time.UTC)
	updated, _ = model.Update(backgroundPollMsg{updates: []BackgroundUpdate{{
		ID:         "bg_loop",
		ScheduleID: "sched_loop",
		Prompt:     "drink water",
		Status:     "running",
		RunCount:   4,
		LastRunAt:  &now,
		LogSize:    44,
		LogTail:    "remember to drink water",
	}}})
	model = updated.(Model)
	view := renderTranscriptForTest(model)
	if !strings.Contains(view, "Loop") || !strings.Contains(view, "Scheduled task fired") || !strings.Contains(view, "remember to drink water") || !strings.Contains(view, "sched_loop") {
		t.Fatalf("transcript missing loop fire output:\n%s", view)
	}
}

func TestModelBackgroundWatcherShowsBashCompletion(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Run: func(context.Context, string) (string, error) { return "ok", nil },
	})
	updated, _ := model.Update(backgroundPollMsg{updates: []BackgroundUpdate{{
		ID:      "bg_bash",
		Kind:    "bash",
		Prompt:  "npm dev server",
		Status:  "completed",
		LogSize: 22,
		LogTail: "server ready",
	}}})
	model = updated.(Model)
	view := renderTranscriptForTest(model)
	if !strings.Contains(view, "Background（后台任务）") || !strings.Contains(view, "Background command completed") || !strings.Contains(view, "server ready") || strings.Contains(view, "Loop") || strings.Contains(view, "Scheduled task fired") {
		t.Fatalf("transcript missing bash completion output:\n%s", view)
	}
}

func TestModelWelcomeSuppressesWorkbenchGoalAndCompletedTodos(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Welcome: WelcomeInfo{
			Model:          "test-model",
			Provider:       "custom/openai-compatible",
			CWD:            "/Users/example/GolandProjects/skills",
			PermissionMode: "ask",
			Sandbox:        "off",
			ToolSummary:    "31",
			Goal:           "abcd1234 active turns 2/5 tokens 42/1000",
			GoalStep:       "Execute the next useful change",
			GoalCriteria:   "0/1 passed req 0/1",
			GoalEvidence:   `doc pass Read passed: {"file_path":"goal_complete.txt"}`,
			GoalNextAction: "collect evidence for pending acceptance criteria",
		},
		Run: func(context.Context, string) (string, error) { return "ok", nil },
	})
	model.width = 120
	model.todos = []todoItem{
		{Content: "done one", Status: "completed"},
		{Content: "done two", Status: "completed"},
	}
	model.refreshViewport()
	view := stripANSI(model.transcriptHeaderView() + "\n" + model.View())
	for _, notWant := range []string{"goal=abcd1234", "step=Execute", "criteria=0/1", "evidence=doc pass", "next=collect evidence", "Tasks 2/2", "\nruntime  ui=tui", "\nsafety  permissions"} {
		if strings.Contains(view, notWant) {
			t.Fatalf("welcome should suppress workbench state %q:\n%s", notWant, view)
		}
	}
	for _, want := range []string{"test-model", "工作区 ~/GolandProjects/skills", "Quick start", "Session", "╭────────────╮", "controls", "mouse=copy", "/ commands", "ctrl+u clear"} {
		if !strings.Contains(view, want) {
			t.Fatalf("welcome missing minimal context %q:\n%s", want, view)
		}
	}
	for _, notWant := range []string{"Welcome back!", "custom/openai-compatible"} {
		if strings.Contains(view, notWant) {
			t.Fatalf("welcome should omit %q:\n%s", notWant, view)
		}
	}
}

func TestModelWelcomeHeaderGoldens(t *testing.T) {
	cases := []struct {
		name  string
		width int
		info  WelcomeInfo
	}{
		{
			name:  "wide_default",
			width: 120,
			info: WelcomeInfo{
				Version:        "dev",
				Model:          "glm-5.1",
				Provider:       "custom/openai-compatible",
				CWD:            "/Users/example/GolandProjects/evaluation",
				SessionID:      "37ace34e-a50a-4170-aaea-81d8ecb28896",
				PermissionMode: "ask",
				Sandbox:        "off",
				ToolSummary:    "31",
			},
		},
		{
			name:  "wide_danger",
			width: 120,
			info: WelcomeInfo{
				Version:        "dev",
				Model:          "glm-5.1",
				Provider:       "custom/openai-compatible",
				CWD:            "/Users/example/GolandProjects/evaluation",
				SessionID:      "37ace34e-a50a-4170-aaea-81d8ecb28896",
				PermissionMode: "bypass",
				Sandbox:        "off",
				ToolSummary:    "31",
			},
		},
		{
			name:  "compact",
			width: 72,
			info: WelcomeInfo{
				Version:        "dev",
				Model:          "glm-5.1",
				Provider:       "custom/openai-compatible",
				CWD:            "/Users/example/GolandProjects/evaluation",
				PermissionMode: "ask",
				Sandbox:        "off",
				ToolSummary:    "31",
			},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			model := NewModel(context.Background(), Options{Welcome: tt.info})
			model.width = tt.width
			// Pin time so the danger-mode mascot blink is deterministic (◆ frame).
			model.now = func() time.Time { return time.UnixMilli(0) }
			got := strings.TrimSpace(stripANSI(model.headerView(true))) + "\n"
			assertGoldenText(t, filepath.Join("testdata", "golden", "welcome_"+tt.name+".txt"), got)
		})
	}
}

func TestModelModeHintShowsGoalStatusAfterConversationStarts(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Welcome: WelcomeInfo{Model: "test-model", Goal: "abcd1234 active turns 2/5 tokens 42/1000"},
		Run:     func(context.Context, string) (string, error) { return "ok", nil },
	})
	model.width = 120
	model.messages = append(model.messages, message{role: "user", content: "start"})
	model.todos = []todoItem{{Content: "active work", Status: "in_progress"}}
	view := stripANSI(model.modeHintView("Ready"))
	if !strings.Contains(view, "goal  id=abcd1234 active turns 2/5 tokens 42/1000") {
		t.Fatalf("active conversation should show goal status:\n%s", view)
	}
}

func TestModelQuietConversationSuppressesDuplicateWorkbenchContext(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Welcome: WelcomeInfo{
			Model:          "glm-5.1",
			Provider:       "custom/openai-compatible",
			CWD:            "/Users/example/GolandProjects/skills",
			PermissionMode: "ask",
			Sandbox:        "off",
			ToolSummary:    "31",
			Goal:           "bc18207c active turns 1/3 tokens 44657/200000",
			GoalStep:       "Execute the next useful change",
			GoalCriteria:   "0/1 passed req 0/1",
			GoalEvidence:   `doc pass Read passed: {"file_path":"goal_complete.txt"}`,
			GoalNextAction: "collect evidence for pending acceptance criteria",
		},
	})
	model.width = 180
	model.messages = append(model.messages,
		message{role: "user", content: "测试1"},
		message{role: "assistant", content: "收到"},
	)
	model.usage = usagePanel{
		Model:         "glm-5.1",
		Turns:         1,
		MaxTurns:      100,
		InputTokens:   14734,
		OutputTokens:  30,
		LastInput:     14734,
		ContextWindow: 200000,
		ToolCount:     32,
		StopReason:    "end_turn",
		SessionID:     "37ace34e-a50a-4170-aaea-81d8ecb28896",
		CWD:           "/Users/example/GolandProjects/skills",
	}
	model.todos = []todoItem{
		{Content: "old done one", Status: "completed"},
		{Content: "old done two", Status: "completed"},
	}
	model.todosLoadedFromDisk = true
	model.refreshViewport()
	view := stripANSI(model.View())
	for _, notWant := range []string{
		"Tasks 2/2",
		"\nruntime  ui=tui",
		"\nsafety  permissions",
		"\ngoal  id=",
		"\nevidence  evidence=",
		"provider=custom/openai-compatible",
		"cwd=~/GolandProjects/skills",
		"session=37ace34e-a50a-4170-aaea-81d8ecb28896",
	} {
		if strings.Contains(view, notWant) {
			t.Fatalf("quiet conversation should suppress duplicate workbench context %q:\n%s", notWant, view)
		}
	}
	for _, want := range []string{"Usage", "turns=1/100", "tokens in/out=14734/30", "status  Ready", "controls", "mouse=copy", "ctrl+u clear"} {
		if !strings.Contains(view, want) {
			t.Fatalf("quiet conversation missing useful summary %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "stop=end_turn") {
		t.Fatalf("quiet conversation should suppress normal stop reason:\n%s", view)
	}
}

func TestModelQuietConversationShowsPermissionSwitchMode(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Welcome: WelcomeInfo{PermissionMode: "ask"},
		SwitchPermissionMode: func(string) (string, error) {
			return "allow", nil
		},
	})
	model.width = 120
	model.messages = append(model.messages, message{role: "assistant", content: "done"})
	model.refreshViewport()
	view := stripANSI(model.View())
	if !strings.Contains(view, "shift+tab perm=ask") {
		t.Fatalf("quiet controls should show current permission mode:\n%s", view)
	}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	model = updated.(Model)
	view = stripANSI(model.View())
	if !strings.Contains(view, "shift+tab perm=allow") {
		t.Fatalf("quiet controls should update permission mode after shift+tab:\n%s", view)
	}
}

func TestModelQuietBusyConversationSuppressesDuplicateWorkbenchContext(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Welcome: WelcomeInfo{
			Model:          "glm-5.1",
			Provider:       "custom/openai-compatible",
			CWD:            "/Users/example/GolandProjects/skills",
			PermissionMode: "ask",
			Sandbox:        "off",
			ToolSummary:    "31",
			Goal:           "bc18207c active turns 1/3 tokens 44657/200000",
			GoalStep:       "Execute the next useful change",
			GoalCriteria:   "0/1 passed req 0/1",
			GoalEvidence:   `doc pass Read passed: {"file_path":"goal_complete.txt"}`,
			GoalNextAction: "collect evidence for pending acceptance criteria",
		},
	})
	model.width = 180
	model.messages = append(model.messages, message{role: "user", content: "测试3"})
	model.busy = true
	model.runningStatus = "niuma is preparing context..."
	model.turnStarted = time.Now().Add(-1500 * time.Millisecond)
	model.usage = usagePanel{
		Model:         "glm-5.1",
		Turns:         2,
		MaxTurns:      100,
		InputTokens:   42991,
		OutputTokens:  150,
		LastInput:     28257,
		ContextWindow: 200000,
		CacheRead:     13440,
		ToolCount:     32,
		StopReason:    "end_turn",
		SessionID:     "92e49f26-fe5b-4f73-9f56-cacb359bef24",
		CWD:           "/Users/example/GolandProjects/skills",
	}
	model.todos = []todoItem{
		{Content: "old done one", Status: "completed"},
		{Content: "old done two", Status: "completed"},
	}
	model.todosLoadedFromDisk = true
	model.refreshViewport()
	view := stripANSI(model.View())
	for _, notWant := range []string{
		"Tasks 2/2",
		"\nruntime  ui=tui",
		"\nsafety  permissions",
		"\ngoal  id=",
		"\nevidence  evidence=",
		"provider=custom/openai-compatible",
		"cwd=~/GolandProjects/skills",
		"cwd=/Users/example/GolandProjects/skills",
		"session=92e49f26-fe5b-4f73-9f56-cacb359bef24",
	} {
		if strings.Contains(view, notWant) {
			t.Fatalf("quiet busy conversation should suppress duplicate workbench context %q:\n%s", notWant, view)
		}
	}
	for _, want := range []string{"Usage", "turns=2/100", "tokens in/out=42991/150", "status  niuma is preparing context...", "elapsed=", "session tokens in/out=42991/150", "controls", "mouse=copy"} {
		if !strings.Contains(view, want) {
			t.Fatalf("quiet busy conversation missing useful summary %q:\n%s", want, view)
		}
	}
}

func TestModelLightSubAgentSuppressesStaleDiskTodosAndWorkbenchChrome(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Welcome: WelcomeInfo{
			Model:          "glm-5.1",
			Provider:       "custom/openai-compatible",
			CWD:            "/Users/example/GolandProjects/skills",
			PermissionMode: "ask",
			Sandbox:        "off",
			ToolSummary:    "31",
			Goal:           "bc18207c active turns 1/3 tokens 44657/200000",
			GoalStep:       "Execute the next useful change",
			GoalCriteria:   "0/1 passed req 0/1",
			GoalEvidence:   `doc pass Read passed: {"file_path":"goal_complete.txt"}`,
			GoalNextAction: "collect evidence for pending acceptance criteria",
		},
	})
	model.width = 180
	model.busy = true
	model.runningStatus = "niuma is coordinating sub-agents..."
	model.turnStarted = time.Now().Add(-30 * time.Second)
	model.usage = usagePanel{
		Model:         "glm-5.1",
		Turns:         4,
		MaxTurns:      100,
		InputTokens:   99987,
		OutputTokens:  819,
		LastInput:     28752,
		ContextWindow: 200000,
		CacheRead:     40448,
		ToolCount:     32,
		SessionID:     "112d8018-60e6-4c60-a1cb-f224bf30a58f",
		CWD:           "/Users/example/GolandProjects/skills",
	}
	model.todos = []todoItem{
		{Content: "读取新SQL文件和所有需要修改的现有文件", Status: "completed", Priority: "high"},
		{Content: "修改 mysql-schema.md（底层schema源）", Status: "completed", Priority: "high"},
		{Content: "验证所有改动一致性", Status: "completed"},
	}
	model.todosLoadedFromDisk = true
	model.agentProgress = map[uint64]agentProgress{
		1: {
			taskID:      1,
			agent:       "general-purpose",
			model:       "glm-5.1",
			status:      "running",
			description: "测试sub-agent基本功能",
			turn:        "3",
			messages:    "5",
			lastTool:    "Glob",
		},
	}
	model.agentOrder = []uint64{1}
	model.recentAgentEvidence = "capability_loop: evidence: No structured evidence reported by sub-agent; parent must verify before finalizing"
	model.refreshViewport()

	view := stripANSI(model.View())
	for _, want := range []string{
		"Sub-agents",
		"#1 general-purpose (glm-5.1)",
		"status  niuma is coordinating sub-agents...",
		"elapsed=",
		"session tokens in/out=99987/819",
		"agents=run:1",
		"ctrl+e sub-agents",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("light sub-agent view missing %q:\n%s", want, view)
		}
	}
	for _, notWant := range []string{
		"Tasks 3/3",
		"读取新SQL文件",
		"mysql-schema.md",
		"\nruntime  ui=tui",
		"\nsafety  permissions",
		"\ngoal  id=",
		"\nevidence  evidence=",
		"\nevidence  agent=",
		"desc=测试sub-agent基本功能",
		"capability_loop:",
		"provider=custom/openai-compatible",
		"cwd=~/GolandProjects/skills",
		"session=112d8018-60e6-4c60-a1cb-f224bf30a58f",
	} {
		if strings.Contains(view, notWant) {
			t.Fatalf("light sub-agent view should suppress %q:\n%s", notWant, view)
		}
	}
}

func TestModelCompactSubAgentSuppressesStreamingTextDetails(t *testing.T) {
	events := make(chan StreamEvent, 4)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	model.width = 140
	model.height = 24
	for _, event := range []StreamEvent{
		{Type: StreamNestedProgress, Event: "started", TaskID: 1, Payload: json.RawMessage(`{"agent_name":"general-purpose","model":"glm-5.1","description":"Read directory and sample.txt"}`)},
		{Type: StreamNestedProgress, Event: "turn_start", TaskID: 1, Payload: json.RawMessage(`{"turn":2,"messages":3}`)},
		{Type: StreamNestedProgress, Event: "text_delta", TaskID: 1, Payload: json.RawMessage(`{"text":"| Directory | .claude docs sample alpha beta gamma |"}`)},
		{Type: StreamNestedProgress, Event: "tool_call", TaskID: 1, Payload: json.RawMessage(`{"tool_name":"Read"}`)},
	} {
		updated, _ := model.Update(streamEventMsg{ch: events, event: event})
		model = updated.(Model)
	}

	view := stripANSI(model.agentProgressView())
	for _, want := range []string{"Sub-agents", "#1 general-purpose (glm-5.1)", "stop #1", "turn=2", "messages=3", "tool=Read"} {
		if !strings.Contains(view, want) {
			t.Fatalf("compact sub-agent view missing %q:\n%s", want, view)
		}
	}
	for _, notWant := range []string{"| Directory |", "alpha beta gamma", "Read directory and sample.txt"} {
		if strings.Contains(view, notWant) {
			t.Fatalf("compact sub-agent view should hide streaming detail %q:\n%s", notWant, view)
		}
	}

	model.agentPanelExpanded = true
	expanded := stripANSI(model.agentProgressView())
	if !strings.Contains(expanded, "tool: Read") {
		t.Fatalf("expanded sub-agent view should keep diagnostic detail:\n%s", expanded)
	}
}

func TestModelIncompleteDiskTodosStayVisibleDuringSubAgent(t *testing.T) {
	model := NewModel(context.Background(), Options{})
	model.width = 140
	model.busy = true
	model.runningStatus = "niuma is coordinating sub-agents..."
	model.todos = []todoItem{{Content: "恢复中的任务", Status: "in_progress", Priority: "high"}}
	model.todosLoadedFromDisk = true
	model.agentProgress = map[uint64]agentProgress{1: {taskID: 1, status: "running"}}
	model.agentOrder = []uint64{1}
	model.refreshViewport()

	view := stripANSI(model.View())
	for _, want := range []string{"Sub-agents", "Tasks 0/1", "恢复中的任务"} {
		if !strings.Contains(view, want) {
			t.Fatalf("incomplete disk todos should stay visible during sub-agent, missing %q:\n%s", want, view)
		}
	}
}

func TestModelModeHintShowsRuntimeContextStatus(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Welcome: WelcomeInfo{
			CWD:            "/Users/example/GolandProjects/niuma",
			Model:          "glm-5.1",
			Provider:       "openai-compatible",
			PromptMode:     "code",
			ContextLength:  200000,
			PermissionMode: "ask",
			Sandbox:        "workspace-write",
			ToolSummary:    "enabled+mcp",
			MCPServers:     2,
			Goal:           "abcd1234 active turns 2/5 tokens 42/1000",
			GoalStep:       "Verify runtime context",
			GoalCriteria:   "1/2 passed req 1/2",
			GoalEvidence:   "test pass go test ./internal/tui passed",
			GoalNextAction: "run focused verification",
		},
	})
	model.usage = usagePanel{SessionID: "sess-1"}
	model.agentProgress = map[uint64]agentProgress{
		1: {taskID: 1, status: "running"},
		2: {taskID: 2, status: "failed"},
		3: {taskID: 3, status: "completed"},
	}
	model.todos = []todoItem{{Content: "active workbench task", Status: "in_progress"}}
	view := stripANSI(model.modeHintView("niuma is coordinating sub-agents..."))
	for _, want := range []string{
		"status  niuma is coordinating sub-agents...",
		"\nruntime  ui=tui",
		"model=glm-5.1",
		"provider=openai-compatible",
		"mode=code",
		"ctx=200k",
		"tools=enabled+mcp",
		"mcp=2",
		"cwd=~/GolandProjects/niuma",
		"\nsafety  permissions=ask",
		"sandbox=workspace-write",
		"\ngoal  id=",
		"id=abcd1234 active turns 2/5 tokens 42/1000",
		"step=Verify runtime context",
		"criteria=1/2 passed req 1/2",
		"\nevidence  evidence=",
		"evidence=test pass go test ./internal/tui passed",
		"next=run focused verification",
		"agents=run:1/fail:1/done:1",
		"\ncontrols",
		"mouse=copy",
		"ctrl+e sub-agents",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("mode hint missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "session=sess-1") {
		t.Fatalf("mode hint should not repeat usage session id:\n%s", view)
	}
}

func TestModelModeHintWrapsNarrowTerminalRows(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Welcome: WelcomeInfo{
			CWD:            "/Users/example/GolandProjects/niuma/very/long/workspace/path",
			Model:          "very-long-model-name-for-narrow-status",
			Provider:       "custom/openai-compatible-provider-with-long-name",
			PromptMode:     "code",
			ContextLength:  200000,
			PermissionMode: "bypass",
			Sandbox:        "workspace-write",
			ToolSummary:    "31",
			Goal:           "bc18207c active turns 1/3 tokens 44657/200000",
			GoalStep:       "Execute the next user-visible TUI workbench improvement without breaking copy or scroll",
			GoalEvidence:   "doc pass Read passed: {\"file_path\":\"goal_complete.txt\"}",
			GoalNextAction: "run focused verification and update progress document",
		},
	})
	model.width = 80
	model.usage = usagePanel{SessionID: "ff05db9e-fd22-4513-8d38-704f1f3fd71c"}
	model.switchPermission = func(string) (string, error) { return "ask", nil }
	model.toolActivity = []toolActivityItem{{ToolName: "Bash", Status: "done"}}
	model.todos = []todoItem{{Content: "active workbench task", Status: "in_progress"}}

	view := stripANSI(model.modeHintView("Task: 正在运行 go test ./... with a long visible status"))
	for _, line := range strings.Split(view, "\n") {
		if got := runewidth.StringWidth(line); got > 80 {
			t.Fatalf("mode hint line width=%d > 80:\n%s\nfull view:\n%s", got, line, view)
		}
	}
	for _, want := range []string{"cwd=~/GolandProjects/niuma", "ctrl+t tools", "ctrl+j newline", "/exit to quit"} {
		if !strings.Contains(view, want) {
			t.Fatalf("wrapped mode hint missing %q:\n%s", want, view)
		}
	}
}

func TestModelModeHintShowsRecentAgentEvidence(t *testing.T) {
	events := make(chan StreamEvent, 3)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.width = 220
	model.todos = []todoItem{{Content: "verify agent evidence", Status: "in_progress"}}
	output := `{"capability_loop":{"evidence":["TASK_VISIBLE_EVIDENCE"],"risks":["TASK_VISIBLE_RISK"],"next_action":"TASK_VISIBLE_NEXT_ACTION"}}`

	updated, _ := model.Update(streamEventMsg{ch: events, event: StreamEvent{
		Type:     StreamToolResult,
		ToolName: "Task",
		ToolID:   "toolu_task_visible",
		Output:   output,
	}})
	model = updated.(Model)
	view := stripANSI(model.modeHintView("niuma is processing tool results..."))
	for _, want := range []string{
		"evidence  agent=capability_loop:",
		"TASK_VISIBLE_EVIDENCE",
		"TASK_VISIBLE_RISK",
		"TASK_VISIBLE_NEXT_ACTION",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("mode hint missing recent agent evidence %q:\n%s", want, view)
		}
	}
}

func TestModelModeHintSkipsPlaceholderAgentEvidenceFields(t *testing.T) {
	events := make(chan StreamEvent, 3)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.width = 220
	model.todos = []todoItem{{Content: "verify placeholder filtering", Status: "in_progress"}}
	output := `{"capability_loop":{"evidence":["TUI_PLACEHOLDER_EVIDENCE"],"assumptions":["None observed"],"unknowns":["None observed"],"verification":["None observed"],"risks":["None observed"],"next_action":"TUI_PLACEHOLDER_NEXT_ACTION"}}`

	updated, _ := model.Update(streamEventMsg{ch: events, event: StreamEvent{
		Type:     StreamToolResult,
		ToolName: "Task",
		ToolID:   "toolu_task_placeholder",
		Output:   output,
	}})
	model = updated.(Model)
	view := stripANSI(model.modeHintView("niuma is processing tool results..."))
	for _, want := range []string{
		"evidence  agent=capability_loop:",
		"TUI_PLACEHOLDER_EVIDENCE",
		"TUI_PLACEHOLDER_NEXT_ACTION",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("mode hint missing placeholder-filtered evidence %q:\n%s", want, view)
		}
	}
	for _, notWant := range []string{"None observed", "assumptions:", "unknowns:", "verification:", "risks:"} {
		if strings.Contains(view, notWant) {
			t.Fatalf("mode hint should skip placeholder %q:\n%s", notWant, view)
		}
	}
}

func TestModelCapabilityLoopSummarySkipsDefaultCompletedNextAction(t *testing.T) {
	output := `{"capability_loop":{"evidence":["TUI_DEFAULT_NEXT_ACTION_EVIDENCE"],"next_action":"Parent agent should synthesize the sub-agent result against the user's goal and verify any unproven claims before finalizing."}}`
	result := toolResultSummary("Task", output)
	if !strings.Contains(result, "TUI_DEFAULT_NEXT_ACTION_EVIDENCE") {
		t.Fatalf("summary missing evidence:\n%s", result)
	}
	if strings.Contains(result, "next_action:") || strings.Contains(result, "Parent agent should synthesize") {
		t.Fatalf("summary should skip default completed next_action:\n%s", result)
	}
}

func TestModelModeHintShowsRecentAgentEvidenceSourceStatus(t *testing.T) {
	events := make(chan StreamEvent, 3)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.width = 260
	model.todos = []todoItem{{Content: "recover failed agent evidence", Status: "in_progress"}}
	output := `{
		"evidence_source":"agent_get",
		"task":{"id":43,"agent_name":"auditor","description":"failed recovery probe","status":"failed"},
		"result":{
			"status":"failed",
			"session_id":"failed-sub-session",
			"transcript_path":"/tmp/failed-subagent.jsonl",
			"output_file":"/tmp/failed-subagent.output",
			"worktree_path":"/tmp/failed-worktree",
			"worktree_branch":"agent/failed",
			"capability_loop":{
				"evidence":["FAILED_AGENT_VISIBLE_EVIDENCE"],
				"risks":["FAILED_AGENT_VISIBLE_RISK"],
				"next_action":"FAILED_AGENT_VISIBLE_NEXT_ACTION"
			}
		}
	}`

	updated, _ := model.Update(streamEventMsg{ch: events, event: StreamEvent{
		Type:     StreamToolResult,
		ToolName: "AgentGet",
		ToolID:   "toolu_agent_get_visible",
		Output:   output,
	}})
	model = updated.(Model)
	view := stripANSI(model.modeHintView("niuma is processing AgentGet results..."))
	for _, want := range []string{
		"evidence  agent=source: agent_get  AgentGet #43 auditor failed: failed recovery probe  capability_loop:",
		"FAILED_AGENT_VISIBLE_EVIDENCE",
		"FAILED_AGENT_VISIBLE_RISK",
		"FAILED_AGENT_VISIBLE_NEXT_ACTION",
		"artifacts:",
		"transcript_path: /tmp/failed-subagent.jsonl",
		"output_file: /tmp/failed-subagent.output",
		"worktree_path: /tmp/failed-worktree",
		"worktree_branch: agent/failed",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("mode hint missing sourced recent agent evidence %q:\n%s", want, view)
		}
	}
}

func TestModelModeHintShowsRecentTaskFailureStatus(t *testing.T) {
	events := make(chan StreamEvent, 3)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.width = 240
	model.todos = []todoItem{{Content: "recover failed task evidence", Status: "in_progress"}}
	output := strings.Join([]string{
		"partial sub-agent output",
		"<capability_loop>",
		`{"evidence_source":"terminal_agent_task_store","status":"failed","description":"partial stream audit","session_id":"partial-task-session","transcript_path":"/tmp/partial-task.jsonl","output_file":"/tmp/partial-task.output","worktree_path":"/tmp/partial-task-worktree","worktree_branch":"agent/partial-task","capability_loop":{"evidence":["PARTIAL_TASK_VISIBLE_EVIDENCE"],"risks":["PARTIAL_TASK_VISIBLE_RISK"],"next_action":"PARTIAL_TASK_VISIBLE_NEXT_ACTION"}}`,
		"</capability_loop>",
	}, "\n")

	updated, _ := model.Update(streamEventMsg{ch: events, event: StreamEvent{
		Type:     StreamToolResult,
		ToolName: "Task",
		ToolID:   "toolu_task_visible",
		Output:   output,
	}})
	model = updated.(Model)
	view := stripANSI(model.modeHintView("niuma is recovering from a Task result..."))
	for _, want := range []string{
		"evidence  agent=source: task_store  Task failed: partial stream audit  capability_loop:",
		"PARTIAL_TASK_VISIBLE_EVIDENCE",
		"PARTIAL_TASK_VISIBLE_RISK",
		"PARTIAL_TASK_VISIBLE_NEXT_ACTION",
		"artifacts:",
		"transcript_path: /tmp/partial-task.jsonl",
		"output_file: /tmp/partial-task.output",
		"worktree_path: /tmp/partial-task-worktree",
		"worktree_branch: agent/partial-task",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("mode hint missing failed Task recent evidence %q:\n%s", want, view)
		}
	}
}

func TestModelSessionResumeClearsRecentAgentEvidence(t *testing.T) {
	events := make(chan StreamEvent, 3)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.width = 180
	model.recentAgentEvidence = "capability_loop: evidence: stale | risks: stale | next_action: stale"

	updated, _ := model.Update(streamEventMsg{ch: events, event: StreamEvent{
		Type: StreamSessionResume,
		Text: "resumed",
	}})
	model = updated.(Model)
	view := stripANSI(model.modeHintView("ready"))
	if strings.Contains(view, "evidence agent=") || strings.Contains(view, "stale") {
		t.Fatalf("resume should clear recent agent evidence:\n%s", view)
	}
}

func TestModelCtrlUClearInputAndAttachments(t *testing.T) {
	model := NewModel(context.Background(), Options{
		ImportClipboard: func(ctx context.Context) (Attachment, bool, error) {
			return Attachment{Type: "image", Name: "clip.png", Path: "/tmp/clip.png"}, true, nil
		},
		Run: func(ctx context.Context, prompt string) (string, error) {
			return "answer", nil
		},
	})
	model.textarea.SetValue("draft")
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	model = updated.(Model)
	if len(model.attachments) != 1 {
		t.Fatalf("attachments = %+v", model.attachments)
	}
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	model = updated.(Model)
	if cmd != nil || model.textarea.Value() != "" || len(model.attachments) != 0 {
		t.Fatalf("cmd=%v value=%q attachments=%+v", cmd, model.textarea.Value(), model.attachments)
	}
	if !strings.Contains(model.View(), "ctrl+u clear") {
		t.Fatalf("view missing clear hint:\n%s", model.View())
	}
}

func TestModelCtrlDRemovesLastAttachment(t *testing.T) {
	imports := []Attachment{
		{Type: "image", MediaType: "image/png", Name: "clip.png", Path: "/tmp/clip.png"},
		{Type: "file", MediaType: "application/pdf", Name: "spec.pdf", Path: "/tmp/spec.pdf"},
	}
	model := NewModel(context.Background(), Options{
		ImportClipboard: func(ctx context.Context) (Attachment, bool, error) {
			next := imports[0]
			imports = imports[1:]
			return next, true, nil
		},
		Run: func(ctx context.Context, prompt string) (string, error) {
			return "answer", nil
		},
	})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	model = updated.(Model)
	if len(model.attachments) != 2 || !strings.Contains(model.textarea.Value(), "[Image #2]") {
		t.Fatalf("before remove value=%q attachments=%+v", model.textarea.Value(), model.attachments)
	}

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	model = updated.(Model)
	if cmd != nil || len(model.attachments) != 1 {
		t.Fatalf("cmd=%v attachments=%+v", cmd, model.attachments)
	}
	if strings.Contains(model.textarea.Value(), "[Image #2]") || !strings.Contains(model.textarea.Value(), "[Image #1]") {
		t.Fatalf("input after remove = %q", model.textarea.Value())
	}
	view := model.View()
	if strings.Contains(view, "spec.pdf") || !strings.Contains(view, "clip.png") || !strings.Contains(view, "ctrl+d remove") {
		t.Fatalf("view after remove:\n%s", view)
	}
}

func TestAttachmentTrayShowsTypeIconsAndManagementHints(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Run: func(ctx context.Context, prompt string) (string, error) {
			return "answer", nil
		},
	})
	model.attachments = []Attachment{
		{ID: 1, Type: "image", MediaType: "image/png", Name: "clip.png"},
		{ID: 2, Type: "video", MediaType: "video/mp4", Name: "demo.mp4"},
		{ID: 3, Type: "audio", MediaType: "audio/mpeg", Name: "note.mp3"},
		{ID: 4, Type: "file", MediaType: "application/pdf", Name: "spec.pdf"},
		{ID: 5, Type: "file", MediaType: "application/json", Name: "data.json"},
		{ID: 6, Type: "file", MediaType: "text/markdown", Name: "README.md"},
	}
	view := model.View()
	for _, want := range []string{
		"ctrl+d remove last",
		"ctrl+u clear all",
		"[img] · image · clip.png",
		"[video] · video · demo.mp4",
		"[audio] · audio · note.mp3",
		"[pdf] · file · spec.pdf",
		"[data] · file · data.json",
		"[text] · file · README.md",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q in:\n%s", want, view)
		}
	}
}

func TestModelPermissionPromptDecision(t *testing.T) {
	reply := make(chan PermissionDecision, 1)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	event := StreamEvent{
		Type: StreamPermissionRequest,
		Permission: &PermissionRequest{
			ToolName: "Bash",
			Request:  "Bash:go test",
			Reason:   "requires permission approval",
			Input:    json.RawMessage(`{"command":"go test ./..."}`),
		},
		Reply: reply,
	}
	updated, cmd := model.Update(streamEventMsg{event: event, ch: make(chan StreamEvent)})
	model = updated.(Model)
	view := model.View()
	// cmd 是 transcriptFlushInteractivePrompt 的 flush command，不再为 nil。
	if model.pendingPermission == nil || !strings.Contains(view, "Permission request") || !strings.Contains(view, "Summary: go test ./...") || !strings.Contains(view, "Risk:") || !strings.Contains(view, "executes a shell command") || !strings.Contains(view, "Rule:   Bash(Bash:go test)") || !strings.Contains(view, "Raw input:") || !strings.Contains(view, "Decision:") || !strings.Contains(view, "[ ▶ Once y ]") {
		t.Fatalf("model = %+v view=%s", model, view)
	}
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	model = updated.(Model)
	decision := <-reply
	if cmd == nil || !decision.Allowed || decision.Destination != "session" || model.pendingPermission != nil {
		t.Fatalf("cmd=%v decision=%+v model=%+v", cmd, decision, model)
	}
}

func TestModelOneShotPermissionPromptHidesPersistentScopes(t *testing.T) {
	reply := make(chan PermissionDecision, 1)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	event := StreamEvent{
		Type: StreamPermissionRequest,
		Permission: &PermissionRequest{
			ToolName: "Bash",
			Request:  "git commit -m exact",
			Reason:   "Shared-State Authorization Gate",
			Source:   "shared-state-gate",
			OneShot:  true,
		},
		Reply: reply,
	}
	updated, _ := model.Update(streamEventMsg{event: event, ch: make(chan StreamEvent)})
	model = updated.(Model)
	view := model.View()
	if !strings.Contains(view, "Scope: once=current exact Git effect") || strings.Contains(view, "session=this run") || strings.Contains(view, "project=workspace") || strings.Contains(view, "global=all workspaces") {
		t.Fatalf("one-shot permission view=%s", view)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	model = updated.(Model)
	if model.pendingPermission == nil {
		t.Fatal("persistent approval shortcut must not resolve one-shot request")
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	if model.pendingPermission == nil {
		t.Fatal("one-shot deny navigation must remain in the prompt")
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	decision := <-reply
	if decision.Allowed || decision.Destination != "once" || model.pendingPermission != nil {
		t.Fatalf("decision=%+v model=%+v", decision, model)
	}
}

func TestPermissionPromptWrapsToContentWidth(t *testing.T) {
	view := stripANSI(permissionPromptView(pendingPermission{
		request: PermissionRequest{
			ToolName: "Bash",
			Request:  "go test ./internal/tui ./internal/cli -run PermissionPromptWithLongArgumentsAndOutput",
			Reason:   "needs approval before running a long validation command in the current workspace",
			Source:   "/Users/example/GolandProjects/niuma/internal/tui/app.go",
			Input:    json.RawMessage(`{"command":"go test ./internal/tui ./internal/cli -run PermissionPromptWithLongArgumentsAndOutput","description":"validate narrow permission prompt wrapping"}`),
		},
	}, 40))
	for _, line := range strings.Split(view, "\n") {
		if got := runewidth.StringWidth(line); got > 40 {
			t.Fatalf("permission prompt line width=%d > 40:\n%s\nfull view:\n%s", got, line, view)
		}
	}
}

func TestModelPermissionPromptSelectionAndPersistentRule(t *testing.T) {
	reply := make(chan PermissionDecision, 1)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	event := StreamEvent{
		Type: StreamPermissionRequest,
		Permission: &PermissionRequest{
			ToolName: "Edit",
			Request:  "src/main.go",
			Rule:     "Edit:src/main.go",
			Reason:   "requires permission approval",
		},
		Reply: reply,
	}
	updated, _ := model.Update(streamEventMsg{event: event, ch: make(chan StreamEvent)})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	view := model.View()
	if model.pendingPermission == nil || model.pendingPermission.selected != 1 || !strings.Contains(view, "[ ▶ Session s ]") {
		t.Fatalf("selection=%+v view=%s", model.pendingPermission, view)
	}
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	decision := <-reply
	if cmd == nil || !decision.Allowed || decision.Destination != "session" || decision.Rule != "Edit:src/main.go" {
		t.Fatalf("cmd=%v decision=%+v", cmd, decision)
	}
}

func TestModelPermissionPromptPersistentRuleUsesRequestWhenRuleMissing(t *testing.T) {
	reply := make(chan PermissionDecision, 1)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	command := "python3 /Users/example/.claude/skitter/run.py"
	event := StreamEvent{
		Type: StreamPermissionRequest,
		Permission: &PermissionRequest{
			ToolName: "Bash",
			Request:  command,
			Reason:   "requires permission approval",
			Input:    json.RawMessage(`{"command":"` + command + `"}`),
		},
		Reply: reply,
	}
	updated, _ := model.Update(streamEventMsg{event: event, ch: make(chan StreamEvent)})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	model = updated.(Model)
	decision := <-reply
	if !decision.Allowed || decision.Destination != "global" || decision.Rule != "Bash("+command+")" {
		t.Fatalf("decision=%+v", decision)
	}
}

func TestModelPermissionPromptPersistentRuleNarrowsBroadRule(t *testing.T) {
	reply := make(chan PermissionDecision, 1)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	command := "python3 /Users/example/.claude/skitter/run.py"
	event := StreamEvent{
		Type: StreamPermissionRequest,
		Permission: &PermissionRequest{
			ToolName: "Bash",
			Request:  command,
			Rule:     "Bash",
			Reason:   "requires permission approval",
		},
		Reply: reply,
	}
	updated, _ := model.Update(streamEventMsg{event: event, ch: make(chan StreamEvent)})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	model = updated.(Model)
	decision := <-reply
	if !decision.Allowed || decision.Destination != "global" || decision.Rule != "Bash("+command+")" {
		t.Fatalf("decision=%+v", decision)
	}
}

func TestModelPermissionPromptTodoWriteUsesBroadPersistentRule(t *testing.T) {
	reply := make(chan PermissionDecision, 1)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	event := StreamEvent{
		Type: StreamPermissionRequest,
		Permission: &PermissionRequest{
			ToolName: "TodoWrite",
			Request:  `{"todos":[{"content":"read files","status":"completed"}]}`,
			Reason:   "requires permission approval",
		},
		Reply: reply,
	}
	updated, _ := model.Update(streamEventMsg{event: event, ch: make(chan StreamEvent)})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	model = updated.(Model)
	decision := <-reply
	if !decision.Allowed || decision.Destination != "global" || decision.Rule != "TodoWrite" {
		t.Fatalf("decision=%+v", decision)
	}
}

func TestModelSwitchPermissionModeAutoApprovesPendingPrompt(t *testing.T) {
	reply := make(chan PermissionDecision, 1)
	events := make(chan StreamEvent)
	model := NewModel(context.Background(), Options{
		RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil },
		Welcome:   WelcomeInfo{PermissionMode: "ask"},
		SwitchPermissionMode: func(string) (string, error) {
			return "allow", nil
		},
	})
	model.busy = true
	event := StreamEvent{
		Type: StreamPermissionRequest,
		Permission: &PermissionRequest{
			ToolName: "TodoWrite",
			Request:  `{"todos":[{"content":"read files","status":"completed"}]}`,
			Reason:   "requires permission approval",
		},
		Reply: reply,
	}
	updated, _ := model.Update(streamEventMsg{event: event, ch: events})
	model = updated.(Model)
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	model = updated.(Model)
	decision := <-reply
	if cmd == nil || model.pendingPermission != nil || model.welcome.PermissionMode != "allow" || !decision.Allowed || decision.Rule != "TodoWrite" {
		t.Fatalf("cmd=%v mode=%s pending=%+v decision=%+v", cmd, model.welcome.PermissionMode, model.pendingPermission, decision)
	}
}

func TestModelStreamsThinkingAndWrapsLongOutput(t *testing.T) {
	events := make(chan StreamEvent, 2)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 42, Height: 18})
	model = updated.(Model)
	updated, cmd := model.Update(streamEventMsg{event: StreamEvent{Type: StreamThinking, Text: "considering a very long intermediate reasoning line that should wrap"}, ch: events})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected next wait command")
	}
	view := model.View()
	if !strings.Contains(view, "◌ Thinking") || !strings.Contains(view, "│ considering") {
		t.Fatalf("view missing thinking:\n%s", view)
	}
}

func TestModelPreservesThinkingDeltaWhitespace(t *testing.T) {
	model := NewModel(context.Background(), Options{})
	for _, text := range []string{"first line", "\n\n", "  - nested item"} {
		model.applyStreamEvent(StreamEvent{Type: StreamThinking, Text: text})
	}

	want := "first line\n\n  - nested item"
	if len(model.messages) != 1 || model.messages[0].content != want {
		t.Fatalf("thinking message = %#v, want exact content %q", model.messages, want)
	}
	if len(model.displayTimeline.segments) != 1 || model.displayTimeline.segments[0].content != want {
		t.Fatalf("thinking timeline = %#v, want exact content %q", model.displayTimeline.segments, want)
	}
}

func TestRenderThinkingUsesStableRailAndCompletionState(t *testing.T) {
	model := NewModel(context.Background(), Options{})
	model.width = 28

	streaming := stripANSI(model.renderThinking("one\n\n- two", displaySegmentStreaming, false))
	if !strings.HasPrefix(streaming, "◌ Thinking\n") {
		t.Fatalf("streaming header missing:\n%s", streaming)
	}
	for _, line := range strings.Split(streaming, "\n")[1:] {
		if !strings.HasPrefix(line, "│ ") {
			t.Fatalf("thinking body line has no rail: %q in\n%s", line, streaming)
		}
		if runewidth.StringWidth(line) > model.contentWidth() {
			t.Fatalf("thinking body line width = %d, content width = %d: %q", runewidth.StringWidth(line), model.contentWidth(), line)
		}
	}

	done := stripANSI(model.renderThinking("complete", displaySegmentDone, false))
	if !strings.HasPrefix(done, "◇ Thought\n│ complete") {
		t.Fatalf("completed thinking state missing:\n%s", done)
	}
	continuation := strings.TrimRight(stripANSI(model.renderThinking("continued", displaySegmentDone, true)), " ")
	if continuation != "│ continued" {
		t.Fatalf("continuation = %q, want rail without repeated header", continuation)
	}
}

func TestModelHidesThinkingContentWithoutDroppingRunningStatus(t *testing.T) {
	events := make(chan StreamEvent, 2)
	showThinking := false
	model := NewModel(context.Background(), Options{
		RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil },
		Welcome:   WelcomeInfo{ShowThinking: &showThinking},
	})
	updated, cmd := model.Update(streamEventMsg{event: StreamEvent{Type: StreamThinking, Text: "private reasoning"}, ch: events})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected next wait command")
	}
	if strings.Contains(model.View(), "private reasoning") || len(model.messages) != 0 || len(model.displayTimeline.segments) != 0 {
		t.Fatalf("hidden thinking leaked into UI state: messages=%+v timeline=%+v view=%q", model.messages, model.displayTimeline, model.View())
	}
	if model.runningStatus != "niuma is reasoning..." {
		t.Fatalf("running status = %q", model.runningStatus)
	}
}

func TestModelRendersNestedAgentProgressPanel(t *testing.T) {
	events := make(chan StreamEvent, 2)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	model.width = 240
	model.height = 32
	model.now = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 45, 0, time.UTC) }
	updated, cmd := model.Update(streamEventMsg{
		ch: events,
		event: StreamEvent{
			Type:    StreamNestedProgress,
			Event:   "started",
			TaskID:  77,
			Payload: json.RawMessage(`{"agent_name":"reviewer","model":"test-model","description":"review code","prompt_preview":"inspect changes","started_at":"2026-01-01T00:00:00Z"}`),
		},
	})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected next wait command")
	}
	for _, event := range []StreamEvent{
		{Type: StreamNestedProgress, Event: "message", TaskID: 77, Payload: json.RawMessage(`{"from_agent":"coordinator","content_preview":"continue"}`)},
		{Type: StreamNestedProgress, Event: "usage", TaskID: 77, Payload: json.RawMessage(`{"input_tokens":12,"output_tokens":4,"cache_creation_input_tokens":3,"cache_read_input_tokens":5}`)},
		{Type: StreamNestedProgress, Event: "cache_state", TaskID: 77, Payload: json.RawMessage(`{"breaks_cache":true}`)},
	} {
		updated, _ = model.Update(streamEventMsg{ch: events, event: event})
		model = updated.(Model)
	}
	runningView := stripANSI(model.agentProgressView())
	for _, want := range []string{"Sub-agents  1 running / 0 done / 0 failed", "stop #77", "elapsed=45s", "tokens=in=12/out=4", "cache signature changed"} {
		if !strings.Contains(runningView, want) {
			t.Fatalf("running view missing %q:\n%s", want, runningView)
		}
	}
	for _, want := range []string{"Sub-agent #77 reviewer", "cache_state", "elapsed=45s"} {
		if !strings.Contains(model.runningStatus, want) {
			t.Fatalf("running status missing %q: %s", want, model.runningStatus)
		}
	}
	updated, _ = model.Update(streamEventMsg{
		ch: events,
		event: StreamEvent{
			Type:   StreamNestedProgress,
			Event:  "completed",
			TaskID: 77,
			Payload: json.RawMessage(`{
				"turns":2,
				"tool_calls":1,
				"duration_ms":25,
				"session_id":"sub-session",
				"transcript_path":"/tmp/subagent.jsonl",
				"output_file":"/tmp/subagent.output",
				"worktree_path":"/tmp/worktree",
				"worktree_branch":"agent/reviewer",
				"capability_loop":{
					"evidence":["internal/query/query.go:3928 forwards agent evidence"],
					"unknowns":["live transcript not checked"],
					"verification":["go test ./internal/tui -count=1"],
					"risks":["summary may omit secondary findings"],
					"next_action":"parent verifies anchor"
				}
			}`),
		},
	})
	model = updated.(Model)
	view := stripANSI(model.agentProgressView())
	for _, want := range []string{
		"Sub-agents",
		"0 running / 1 done / 0 failed",
		"#77 reviewer (test-model)",
		"completed",
		"turn=2",
		"tools=1",
		"elapsed=25ms",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("collapsed view missing %q:\n%s", want, view)
		}
	}
	for _, notWant := range []string{
		"desc=review code",
		"session=sub-session",
		"out=/tmp/subagent.output",
		"transcript=/tmp/subagent.jsonl",
		"worktree=/tmp/worktree",
		"branch=agent/reviewer",
		"capability_loop:",
		"evidence: internal/query/query.go:3928 forwards agent evidence",
		"unknowns: live transcript not checked",
		"verification: go test ./internal/tui -count=1",
		"risks: summary may omit secondary findings",
		"next_action: parent verifies anchor",
	} {
		if strings.Contains(view, notWant) {
			t.Fatalf("collapsed view should hide %q:\n%s", notWant, view)
		}
	}
	model.agentPanelExpanded = true
	expanded := stripANSI(model.agentProgressView())
	for _, want := range []string{
		"Sub-agents expanded",
		"#77 reviewer (test-model)",
		"completed",
		"2 turns",
		"1 tools",
		"desc=review code",
		"session=sub-session",
		"out=/tmp/subagent.output",
		"transcript=/tmp/subagent.jsonl",
		"worktree=/tmp/worktree",
		"branch=agent/reviewer",
		"25ms",
		"capability_loop:",
		"evidence: internal/query/query.go:3928 forwards agent evidence",
		"unknowns: live transcript not checked",
		"verification: go test ./internal/tui -count=1",
		"risks: summary may omit secondary findings",
		"next_action: parent verifies anchor",
	} {
		if !strings.Contains(expanded, want) {
			t.Fatalf("expanded view missing %q:\n%s", want, expanded)
		}
	}
}

func TestModelRendersNestedAgentTimeoutSeparatelyFromCancelled(t *testing.T) {
	events := make(chan StreamEvent, 2)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	model.width = 120
	model.height = 24

	updated, _ := model.Update(streamEventMsg{
		ch: events,
		event: StreamEvent{
			Type:    StreamNestedProgress,
			Event:   "timeout",
			TaskID:  88,
			Payload: json.RawMessage(`{"error":"context deadline exceeded","duration_ms":60000,"session_id":"timeout-session","output_file":"/tmp/timeout.output"}`),
		},
	})
	model = updated.(Model)
	updated, _ = model.Update(streamEventMsg{
		ch: events,
		event: StreamEvent{
			Type:    StreamNestedProgress,
			Event:   "cancelled",
			TaskID:  89,
			Payload: json.RawMessage(`{"source":"user","duration_ms":25}`),
		},
	})
	model = updated.(Model)

	if summary := model.agentProgressSummary(); !strings.Contains(summary, "timeout:1") || !strings.Contains(summary, "cancel:1") {
		t.Fatalf("summary = %q", summary)
	}
	if got := model.agentProgress[88].detail; got != "context deadline exceeded" {
		t.Fatalf("timeout detail = %q", got)
	}
	view := stripANSI(model.agentProgressView())
	for _, want := range []string{
		"#88",
		"timeout",
		"elapsed=1m00s",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestModelRendersNestedAgentProgressWithCurrentLiveMessage(t *testing.T) {
	events := make(chan StreamEvent, 4)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	model.width = 100
	model.height = 24

	updated, _ := model.Update(streamEventMsg{ch: events, event: StreamEvent{Type: StreamText, Text: "current answer"}})
	model = updated.(Model)
	updated, _ = model.Update(streamEventMsg{
		ch: events,
		event: StreamEvent{
			Type:    StreamNestedProgress,
			Event:   "completed",
			TaskID:  42,
			Payload: json.RawMessage(`{"turns":2,"tool_calls":1,"duration_ms":30}`),
		},
	})
	model = updated.(Model)
	view := model.View()
	answerIndex := strings.Index(view, "current answer")
	agentsIndex := strings.Index(view, "Sub-agents")
	usageIndex := strings.Index(view, "Usage")
	inputIndex := strings.Index(view, "Ready")
	if answerIndex >= 0 || agentsIndex < 0 {
		t.Fatalf("completed answer should be committed while sub-agent remains live:\n%s", view)
	}
	if usageIndex >= 0 && usageIndex < agentsIndex {
		t.Fatalf("usage should not separate answer from sub-agent progress:\n%s", view)
	}
	if inputIndex >= 0 && inputIndex < agentsIndex {
		t.Fatalf("input should not appear before sub-agent progress:\n%s", view)
	}
}

func TestModelAddsBlankLineBetweenLiveMessageAndUsage(t *testing.T) {
	events := make(chan StreamEvent, 4)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	model.width = 100
	model.height = 24

	updated, _ := model.Update(streamEventMsg{ch: events, event: StreamEvent{Type: StreamText, Text: "current answer"}})
	model = updated.(Model)
	model.applyQueryResult(QueryResult{
		Model: "glm-5.1",
		Usage: Usage{InputTokens: 100, OutputTokens: 3},
	})

	view := stripANSI(model.View())
	answerIndex := strings.Index(view, "current answer")
	usageIndex := strings.Index(view, "Usage")
	if answerIndex < 0 || usageIndex < 0 || usageIndex <= answerIndex {
		t.Fatalf("view should include answer and usage:\n%s", view)
	}
	// Verify there is at least one blank line between answer and usage
	between := view[answerIndex:usageIndex]
	if !strings.Contains(between, "\n\n") {
		t.Fatalf("view should include blank line between live answer and usage:\n%s", view)
	}
}

func TestModelArchivesNestedAgentProgressToAssistantMessage(t *testing.T) {
	events := make(chan StreamEvent, 4)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	model.width = 100
	model.height = 24

	updated, _ := model.Update(streamEventMsg{ch: events, event: StreamEvent{Type: StreamText, Text: "done"}})
	model = updated.(Model)
	updated, _ = model.Update(streamEventMsg{
		ch: events,
		event: StreamEvent{
			Type:    StreamNestedProgress,
			Event:   "completed",
			TaskID:  42,
			Payload: json.RawMessage(`{"turns":2,"tool_calls":1,"duration_ms":30}`),
		},
	})
	model = updated.(Model)
	updated, _ = model.Update(streamEventMsg{
		ch: events,
		event: StreamEvent{
			Type:   StreamUsage,
			Result: &QueryResult{Response: "done"},
		},
	})
	model = updated.(Model)
	updated, cmd := model.Update(streamEventMsg{ch: events, event: StreamEvent{Type: StreamFinished}})
	model = updated.(Model)

	if len(model.messages) == 0 || len(model.messages[len(model.messages)-1].agents) != 1 {
		t.Fatalf("assistant message should archive sub-agent progress: %+v", model.messages)
	}
	if len(model.agentProgress) != 0 || len(model.agentOrder) != 0 {
		t.Fatalf("live sub-agent progress should be cleared after archive: progress=%+v order=%+v", model.agentProgress, model.agentOrder)
	}
	commit, ok := cmd().(transcriptFlushCommitMsg)
	if !ok {
		t.Fatalf("expected transcriptFlushCommitMsg from delayed command")
	}
	output := stripANSI(commit.output)
	agentsIndex := strings.Index(output, "Sub-agents")
	if agentsIndex < 0 {
		t.Fatalf("archived sub-agent progress should remain in transcript after assistant phase commit:\n%s", output)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	model = updated.(Model)
	renderedMessage := stripANSI(model.renderTranscriptMessage(model.messages[len(model.messages)-1]))
	if !strings.Contains(renderedMessage, "done") || !strings.Contains(renderedMessage, "Sub-agents") || !strings.Contains(renderedMessage, "#42") {
		t.Fatalf("ctrl+e should still expand archived sub-agent progress:\n%s", renderedMessage)
	}
}

func TestModelFixedChromeHeightExcludesNestedAgentProgress(t *testing.T) {
	events := make(chan StreamEvent, 4)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	model.width = 100
	model.height = 24
	before := model.fixedChromeHeight()

	for i := 1; i <= 4; i++ {
		updated, _ := model.Update(streamEventMsg{
			ch: events,
			event: StreamEvent{
				Type:    StreamNestedProgress,
				Event:   "completed",
				TaskID:  uint64(i),
				Payload: json.RawMessage(`{"turns":1,"tool_calls":1,"duration_ms":10}`),
			},
		})
		model = updated.(Model)
	}

	if got := model.fixedChromeHeight(); got != before {
		t.Fatalf("sub-agent progress should not reserve fixed footer height: before=%d after=%d", before, got)
	}
}

func TestModelShowsSubAgentExpandHintWhenProgressExists(t *testing.T) {
	events := make(chan StreamEvent, 2)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	model.width = 100
	model.height = 24

	updated, _ := model.Update(streamEventMsg{
		ch: events,
		event: StreamEvent{
			Type:    StreamNestedProgress,
			Event:   "started",
			TaskID:  1,
			Payload: json.RawMessage(`{"agent_name":"local","description":"local progress"}`),
		},
	})
	model = updated.(Model)
	view := model.View()
	if !strings.Contains(view, "Sub-agents") || !strings.Contains(view, "ctrl+e sub-agents") {
		t.Fatalf("view should expose sub-agent panel and shortcut hint:\n%s", view)
	}
}

func TestModelIgnoresNestedProgressWithoutTaskIDInHistory(t *testing.T) {
	events := make(chan StreamEvent, 200)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	model.width = 100
	model.height = 24
	model.messages = append(model.messages, message{role: "user", content: "run batch"})
	before := len(model.messages)

	for i := 0; i < 100; i++ {
		updated, _ := model.Update(streamEventMsg{
			ch: events,
			event: StreamEvent{
				Type:   StreamNestedProgress,
				Event:  "completed",
				Output: "nested agent progress",
			},
		})
		model = updated.(Model)
	}

	if len(model.messages) != before {
		t.Fatalf("task_id=0 progress should not append history messages: before=%d after=%d messages=%+v", before, len(model.messages), model.messages)
	}
	view := model.View()
	if strings.Contains(view, "Sub-agent\nnested agent progress") {
		t.Fatalf("view should not render repeated fallback progress as a message:\n%s", view)
	}
}

func TestModelCollapsesOlderAgentProgress(t *testing.T) {
	events := make(chan StreamEvent, 20)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	model.width = 100
	model.height = 24

	for i := 1; i <= 6; i++ {
		updated, _ := model.Update(streamEventMsg{
			ch: events,
			event: StreamEvent{
				Type:    StreamNestedProgress,
				Event:   "completed",
				TaskID:  uint64(i),
				Payload: json.RawMessage(`{"turns":1,"tool_calls":1,"duration_ms":10}`),
			},
		})
		model = updated.(Model)
	}

	view := model.View()
	for _, hidden := range []string{"#1", "#2", "#3"} {
		if strings.Contains(view, hidden+"  completed") {
			t.Fatalf("collapsed view should hide older completed task %s:\n%s", hidden, view)
		}
	}
	for _, visible := range []string{"#4", "#5", "#6"} {
		if !strings.Contains(view, visible+"  completed") {
			t.Fatalf("collapsed view should include recent task %s:\n%s", visible, view)
		}
	}
	if !strings.Contains(view, "+3 older hidden") || !strings.Contains(view, "ctrl+e expand") {
		t.Fatalf("collapsed view missing hidden summary/expand hint:\n%s", view)
	}
}

func TestModelPrioritizesFailedAndRunningAgents(t *testing.T) {
	events := make(chan StreamEvent, 20)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	model.width = 100
	model.height = 24

	for i := 1; i <= 6; i++ {
		updated, _ := model.Update(streamEventMsg{ch: events, event: StreamEvent{Type: StreamNestedProgress, Event: "completed", TaskID: uint64(i)}})
		model = updated.(Model)
	}
	updated, _ := model.Update(streamEventMsg{ch: events, event: StreamEvent{Type: StreamNestedProgress, Event: "failed", TaskID: 99, Payload: json.RawMessage(`{"error":"boom"}`)}})
	model = updated.(Model)
	updated, _ = model.Update(streamEventMsg{ch: events, event: StreamEvent{Type: StreamNestedProgress, Event: "turn_start", TaskID: 88, Payload: json.RawMessage(`{"turn":2}`)}})
	model = updated.(Model)

	view := model.View()
	if !strings.Contains(view, "#99  failed") || !strings.Contains(view, "#88  running") {
		t.Fatalf("failed and running agents should stay visible in collapsed view:\n%s", view)
	}
	if !strings.Contains(view, "+5 older hidden") {
		t.Fatalf("collapsed view missing expected hidden count:\n%s", view)
	}
}

func TestModelTogglesAgentPanelExpanded(t *testing.T) {
	events := make(chan StreamEvent, 20)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	model.width = 100
	model.height = 24

	for i := 1; i <= 6; i++ {
		updated, _ := model.Update(streamEventMsg{ch: events, event: StreamEvent{Type: StreamNestedProgress, Event: "completed", TaskID: uint64(i)}})
		model = updated.(Model)
	}
	collapsed := model.View()
	if strings.Contains(collapsed, "#1  completed") || !strings.Contains(collapsed, "+3 older hidden") {
		t.Fatalf("precondition failed, expected collapsed view:\n%s", collapsed)
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	model = updated.(Model)
	expanded := model.View()
	if !strings.Contains(expanded, "Sub-agents expanded") || !strings.Contains(expanded, "#1  completed") {
		t.Fatalf("expanded view missing older task:\n%s", expanded)
	}
	if len(model.agentProgress) != 6 {
		t.Fatalf("expanded toggle should not drop progress data: %+v", model.agentProgress)
	}
}

func TestModelShowsErrors(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Run: func(ctx context.Context, prompt string) (string, error) {
			return "", errors.New("boom")
		},
	})
	model.textarea.SetValue("hello")
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	model = runTestCommand(t, model, cmd)
	if model.err == nil || !strings.Contains(model.View(), "boom") {
		t.Fatalf("view = %s", model.View())
	}
}

var ansiEscapeRE = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

func stripANSI(text string) string {
	return ansiEscapeRE.ReplaceAllString(text, "")
}

func lineIndexContaining(text, needle string) int {
	for i, line := range strings.Split(text, "\n") {
		if strings.Contains(line, needle) {
			return i
		}
	}
	return -1
}

func assertViewFitsTerminal(t *testing.T, model Model) {
	t.Helper()
	view := model.View()
	if got := visualLineCount(view, model.terminalWidth()); got > model.height {
		t.Fatalf("view visual height = %d, terminal height = %d\n%s", got, model.height, stripANSI(view))
	}
}

func TestModelViewportClipsLongContentToFitScreen(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Run: func(ctx context.Context, prompt string) (string, error) {
			return "answer", nil
		},
	})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 14})
	model = updated.(Model)
	model.transcriptPrintedHeader = true
	model.transcriptPrintedCount = 0
	for i := 0; i < 40; i++ {
		model.messages = append(model.messages, message{role: "assistant", content: "message line " + strconv.Itoa(i)})
	}
	model.refreshViewportAtBottom()
	view := model.View()
	viewLines := visualLineCount(view, model.terminalWidth())
	if viewLines > model.height {
		t.Fatalf("view should fit within screen height: viewLines=%d height=%d\n%s", viewLines, model.height, view)
	}
	if !strings.Contains(view, "message line 39") {
		t.Fatalf("latest message should be visible in clipped view:\n%s", view)
	}
}

func TestModelShortTerminalKeepsWrappedBottomChromeVisible(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Welcome: WelcomeInfo{PermissionMode: "allow"},
		Run: func(ctx context.Context, prompt string) (string, error) {
			return "answer", nil
		},
	})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 96, Height: 10})
	model = updated.(Model)
	model.transcriptPrintedHeader = true
	model.transcriptPrintedCount = 0
	model.messages = append(model.messages, message{role: "assistant", content: strings.Join([]string{
		"建议下一步",
		"1. 立即提交当前变更 - 先 npm run build 验证，再 commit + push",
		"2. 补齐测试验证截图",
	}, "\n")})
	model.usage = usagePanel{
		Turns:         5,
		MaxTurns:      100,
		InputTokens:   187977,
		OutputTokens:  1404,
		LastInput:     47363,
		ContextWindow: 200000,
		CacheRead:     90240,
		ToolCount:     32,
	}
	model.refreshViewportAtBottom()

	view := stripANSI(model.View())
	if usageLines := visualLineCount(model.usageView(), model.contentWidth()); usageLines < 2 {
		t.Fatalf("usage should wrap in this layout, got %d:\n%s", usageLines, view)
	}
	if controlLines := visualLineCount(model.modeHintView(model.viewStatus()), model.terminalWidth()); controlLines < 2 {
		t.Fatalf("controls should wrap in this layout, got %d:\n%s", controlLines, view)
	}
	assertViewFitsTerminal(t, model)
	for _, want := range []string{"status  Ready", "controls", "enter send"} {
		if !strings.Contains(view, want) {
			t.Fatalf("short terminal view should keep footer text visible %q:\n%s", want, view)
		}
	}
}

func assertViewFitsRenderBudget(t *testing.T, model Model) {
	t.Helper()
	budget := model.renderBudget()
	view := stripANSI(model.View())
	if got := visualLineCount(view, budget.SafeWidth); budget.SafeHeight > 0 && got > budget.SafeHeight {
		t.Fatalf("view visual height = %d, safe height = %d budget=%+v\n%s", got, budget.SafeHeight, budget, view)
	}
	for _, line := range strings.Split(view, "\n") {
		if got := runewidth.StringWidth(line); got > budget.SafeWidth {
			t.Fatalf("line width = %d > safe width = %d budget=%+v\nline=%q\nview:\n%s", got, budget.SafeWidth, budget, line, view)
		}
	}
}

func assertGoldenText(t *testing.T, path string, got string) {
	t.Helper()
	if os.Getenv("NIUMA_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir golden dir: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v", path, err)
	}
	if string(want) != got {
		t.Fatalf("golden mismatch %s\nwant:\n%s\ngot:\n%s", path, string(want), got)
	}
}

func screenshotLikeRenderBudgetModel(t *testing.T, width, height int) Model {
	t.Helper()
	model := NewModel(context.Background(), Options{
		Welcome: WelcomeInfo{
			Model:          "glm-5.1",
			Provider:       "custom/openai-compatible",
			CWD:            "/Users/example/GolandProjects/anything-ai",
			PermissionMode: "allow",
			Sandbox:        "off",
			ToolSummary:    "31",
			Resume:         "b692c129-c693-4afe-a74e-4b499bc0ebe9",
			SessionStatus:  "resumed b692c129-c693-4afe-a74e-4b499bc0ebe9",
			Goal:           "bc18207c active turns 1/3 tokens 44657/200000",
			GoalStep:       "Execute the next useful change",
			GoalCriteria:   "0/1 passed req 0/1",
			GoalEvidence:   `doc pass Read passed: {"file_path": "goal_complete.txt"}`,
			GoalNextAction: "collect evidence for pending acceptance criteria",
		},
		ImportClipboard: func(context.Context) (Attachment, bool, error) {
			return Attachment{}, false, nil
		},
		Run: func(context.Context, string) (string, error) { return "", nil },
		SwitchPermissionMode: func(string) (string, error) {
			return "allow", nil
		},
	})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: width, Height: height})
	model = updated.(Model)
	model.transcriptPrintedHeader = true
	model.transcriptPrintedCount = 0
	model.messages = append(model.messages, message{role: "assistant", content: strings.Join([]string{
		"不客气孔",
		"这是一段用于保持 live viewport 可见的回答尾部，避免底部 chrome 把当前回答区域完全挤没。",
	}, "\n")})
	model.usage = usagePanel{
		Model:         "glm-5.1",
		Turns:         22,
		MaxTurns:      100,
		InputTokens:   1287391,
		OutputTokens:  4208,
		LastInput:     57620,
		ContextWindow: 200000,
		CacheRead:     598528,
		ToolCalls:     19,
		ToolCount:     32,
		ToolErrors:    5,
		LastTool:      "Bash",
		CWD:           "/Users/example/GolandProjects/anything-ai",
	}
	model.refreshViewportAtBottom()
	return model
}

func TestModelRenderBudgetUsesSafeWidth(t *testing.T) {
	model := screenshotLikeRenderBudgetModel(t, 150, 32)
	budget := model.renderBudget()
	if budget.ReportedWidth != 150 {
		t.Fatalf("reported width = %d, want 150", budget.ReportedWidth)
	}
	if budget.SafeWidth >= budget.ReportedWidth {
		t.Fatalf("safe width should be narrower than reported width: %+v", budget)
	}
	if budget.ContentWidth >= budget.SafeWidth || budget.ChromeWidth >= budget.SafeWidth {
		t.Fatalf("derived widths should sit inside safe width: %+v", budget)
	}
}

func TestModelBottomChromeUsesSameBudgetAsView(t *testing.T) {
	model := screenshotLikeRenderBudgetModel(t, 150, 32)
	status := model.viewStatus()
	hasLive := model.liveTranscriptView() != ""
	budget := model.renderBudget()
	parts := model.bottomChromeParts(status, hasLive)
	if got, want := model.fixedChromeHeight(), model.chromeHeight(parts, budget); got != want {
		t.Fatalf("fixed chrome height = %d, rendered chrome height = %d\n%s", got, want, stripANSI(strings.Join(parts, "\n")))
	}
	assertViewFitsRenderBudget(t, model)
}

func TestModelFooterCompactsWhenDiagnosticsOverflow(t *testing.T) {
	model := screenshotLikeRenderBudgetModel(t, 150, 24)
	view := stripANSI(model.View())
	assertViewFitsRenderBudget(t, model)
	if strings.Contains(view, "session  resume=") || strings.Contains(view, "evidence  evidence=") {
		t.Fatalf("overflowing diagnostic footer should compact instead of staying fully expanded:\n%s", view)
	}
	if !strings.Contains(view, "ctrl+o details") {
		t.Fatalf("compact footer should keep a details affordance:\n%s", view)
	}
}

func TestModelControlsTailVisibleWithClipboardImageHint(t *testing.T) {
	model := screenshotLikeRenderBudgetModel(t, 150, 24)
	view := stripANSI(model.View())
	assertViewFitsRenderBudget(t, model)
	if !strings.Contains(view, "ctrl+v paste image") {
		t.Fatalf("controls tail should remain visible:\n%s", view)
	}
	if strings.Contains(view, "ctrl+v paste imag\n") {
		t.Fatalf("controls should not be visibly clipped at image hint:\n%s", view)
	}
}

func TestModelUsageTailVisibleWithLongStats(t *testing.T) {
	model := screenshotLikeRenderBudgetModel(t, 150, 24)
	view := stripANSI(model.View())
	assertViewFitsRenderBudget(t, model)
	if !strings.Contains(view, "未完成 5") {
		t.Fatalf("usage tool tail should remain visible:\n%s", view)
	}
	if strings.Contains(view, "未完成\n") {
		t.Fatalf("usage should not be visibly clipped at last tool:\n%s", view)
	}
}

func TestModelRenderBudgetMatrix(t *testing.T) {
	for _, width := range []int{40, 56, 72, 80, 96, 120, 148, 150, 160} {
		for _, height := range []int{8, 10, 12, 14, 18, 24, 40} {
			t.Run(fmt.Sprintf("%dx%d", width, height), func(t *testing.T) {
				model := screenshotLikeRenderBudgetModel(t, width, height)
				assertViewFitsRenderBudget(t, model)
				if height >= 10 && !strings.Contains(stripANSI(model.View()), "controls") {
					t.Fatalf("controls should remain visible at %dx%d:\n%s", width, height, stripANSI(model.View()))
				}
			})
		}
	}
}

func TestModelRenderBudgetAcceptanceExport(t *testing.T) {
	outDir := strings.TrimSpace(os.Getenv("NIUMA_TUI_RENDER_BUDGET_OUT"))
	if outDir == "" {
		t.Skip("set NIUMA_TUI_RENDER_BUDGET_OUT to export render-budget acceptance fixtures")
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		t.Fatalf("create output dir: %v", err)
	}
	type reportEntry struct {
		Width       int          `json:"width"`
		Height      int          `json:"height"`
		Budget      renderBudget `json:"budget"`
		VisualLines int          `json:"visual_lines"`
		Path        string       `json:"path"`
	}
	var report []reportEntry
	for _, size := range []struct {
		width  int
		height int
	}{{150, 40}, {120, 32}, {96, 24}} {
		model := screenshotLikeRenderBudgetModel(t, size.width, size.height)
		assertViewFitsRenderBudget(t, model)
		view := stripANSI(model.View())
		name := fmt.Sprintf("view-%dx%d.txt", size.width, size.height)
		path := filepath.Join(outDir, name)
		if err := os.WriteFile(path, []byte(view+"\n"), 0644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		report = append(report, reportEntry{
			Width:       size.width,
			Height:      size.height,
			Budget:      model.renderBudget(),
			VisualLines: visualLineCount(view, model.renderBudget().SafeWidth),
			Path:        path,
		})
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "report.json"), append(data, '\n'), 0644); err != nil {
		t.Fatalf("write report: %v", err)
	}
}

func TestModelLiveDisplayOrderAcceptanceExport(t *testing.T) {
	outDir := strings.TrimSpace(os.Getenv("NIUMA_TUI_DISPLAY_ORDER_OUT"))
	if outDir == "" {
		t.Skip("set NIUMA_TUI_DISPLAY_ORDER_OUT to export live display order fixtures")
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		t.Fatalf("create output dir: %v", err)
	}
	type reportEntry struct {
		Name       string         `json:"name"`
		Path       string         `json:"path"`
		LineIndex  map[string]int `json:"line_index"`
		VisualRows int            `json:"visual_rows"`
	}
	events := make(chan StreamEvent, 8)
	newModel := func() Model {
		model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
		updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 32})
		model = updated.(Model)
		model.busy = true
		model.streamingActive = true
		model.transcriptPrintedCount = len(model.messages)
		model.refreshViewportAtBottom()
		return model
	}
	apply := func(model Model, event StreamEvent) Model {
		updated, _ := model.Update(streamEventMsg{event: event, ch: events})
		return updated.(Model)
	}
	writeState := func(report *[]reportEntry, name string, text string) {
		clean := stripANSI(text)
		path := filepath.Join(outDir, name+".txt")
		if err := os.WriteFile(path, []byte(clean+"\n"), 0644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		*report = append(*report, reportEntry{
			Name: name,
			Path: path,
			LineIndex: map[string]int{
				"assistant": lineIndexContaining(clean, "解释"),
				"tool":      lineIndexContaining(clean, "查看文件"),
				"meta":      lineIndexContaining(clean, "Responded"),
			},
			VisualRows: visualLineCount(clean, 100),
		})
	}

	var report []reportEntry
	toolFirst := newModel()
	toolFirst = apply(toolFirst, StreamEvent{Type: StreamToolStart, ToolName: "Read", ToolID: "toolu_1", Output: `{"file_path":"README.md"}`})
	toolFirst = apply(toolFirst, StreamEvent{Type: StreamToolResult, ToolName: "Read", ToolID: "toolu_1", Output: "read 12 lines"})
	toolFirst = apply(toolFirst, StreamEvent{Type: StreamText, Text: "解释工具结果"})
	toolFirstBefore := stripANSI(toolFirst.liveTranscriptView())
	writeState(&report, "tool-first-before-usage", toolFirstBefore)
	toolFirst = apply(toolFirst, StreamEvent{Type: StreamUsage, Result: &QueryResult{Model: "m", ToolCalls: []ToolCall{{ID: "toolu_1", Name: "Read"}}}})
	toolFirstAfter := stripANSI(toolFirst.liveTranscriptView())
	writeState(&report, "tool-first-after-usage", toolFirstAfter)
	if before, after := lineIndexContaining(toolFirstBefore, "查看文件"), lineIndexContaining(toolFirstAfter, "查看文件"); before != after {
		t.Fatalf("tool-first tool line moved: before=%d after=%d\nbefore:\n%s\nafter:\n%s", before, after, toolFirstBefore, toolFirstAfter)
	}

	assistantFirst := newModel()
	assistantFirst = apply(assistantFirst, StreamEvent{Type: StreamText, Text: "解释后再查文件"})
	assistantFirst = apply(assistantFirst, StreamEvent{Type: StreamToolStart, ToolName: "Read", ToolID: "toolu_1", Output: `{"file_path":"README.md"}`})
	assistantFirst = apply(assistantFirst, StreamEvent{Type: StreamToolResult, ToolName: "Read", ToolID: "toolu_1", Output: "read 12 lines"})
	assistantFirstBefore := stripANSI(assistantFirst.liveTranscriptView())
	writeState(&report, "assistant-first-before-usage", assistantFirstBefore)
	assistantFirst = apply(assistantFirst, StreamEvent{Type: StreamUsage, Result: &QueryResult{Model: "m", ToolCalls: []ToolCall{{ID: "toolu_1", Name: "Read"}}}})
	assistantFirstAfter := stripANSI(assistantFirst.liveTranscriptView())
	writeState(&report, "assistant-first-after-usage", assistantFirstAfter)
	if before, after := lineIndexContaining(assistantFirstBefore, "查看文件"), lineIndexContaining(assistantFirstAfter, "查看文件"); before != after {
		t.Fatalf("assistant-first tool line moved: before=%d after=%d\nbefore:\n%s\nafter:\n%s", before, after, assistantFirstBefore, assistantFirstAfter)
	}
	if before, after := lineIndexContaining(assistantFirstBefore, "解释后再查文件"), lineIndexContaining(assistantFirstAfter, "解释后再查文件"); before != after {
		t.Fatalf("assistant-first text line moved: before=%d after=%d\nbefore:\n%s\nafter:\n%s", before, after, assistantFirstBefore, assistantFirstAfter)
	}

	toolFirst.busy = false
	toolFirst.streamingActive = false
	transcript := stripANSI(strings.Join(toolFirst.pendingTranscriptBlocks(), "\n"))
	writeState(&report, "tool-first-completed-transcript", transcript)
	if toolLine, assistantLine := lineIndexContaining(transcript, "查看文件"), lineIndexContaining(transcript, "解释工具结果"); toolLine < 0 || assistantLine < 0 || toolLine >= assistantLine {
		t.Fatalf("completed transcript order changed: tool=%d assistant=%d\n%s", toolLine, assistantLine, transcript)
	}

	todoDone := newModel()
	todoDone.todos = []todoItem{
		{Content: "创建 Python venv 并安装后端依赖", Status: "completed"},
		{Content: "启动后端服务", Status: "in_progress"},
	}
	todoInput := `{"todos":[{"content":"创建 Python venv 并安装后端依赖","status":"completed"},{"content":"启动后端服务","status":"completed"}]}`
	todoDone = apply(todoDone, StreamEvent{Type: StreamToolStart, ToolName: "TodoWrite", ToolID: "toolu_todo_done", Output: todoInput})
	todoDone = apply(todoDone, StreamEvent{
		Type:     StreamToolResult,
		ToolName: "TodoWrite",
		ToolID:   "toolu_todo_done",
		Input:    todoInput,
		Output:   "Todos have been modified successfully.\n\nSummary: 2 total, 0 pending, 0 in_progress, 2 completed. All todos are completed, so the persisted session todo state was cleared.",
	})
	todoLive := stripANSI(todoDone.liveTranscriptView())
	writeState(&report, "todo-completed-live", todoLive)
	if !strings.Contains(todoLive, "✓ 本轮任务完成：2 项全部完成") || strings.Contains(todoLive, "Tasks 2/2") {
		t.Fatalf("completed todo live view should be transcript anchored, not footer copy:\n%s", todoLive)
	}
	todoDone.busy = false
	todoDone.streamingActive = false
	todoTranscript := stripANSI(strings.Join(todoDone.pendingTranscriptBlocks(), "\n"))
	writeState(&report, "todo-completed-transcript", todoTranscript)
	if !strings.Contains(todoTranscript, "✓ 本轮任务完成：2 项全部完成") || strings.Contains(todoTranscript, "Tasks 2/2") {
		t.Fatalf("completed todo transcript should be anchored, not footer copy:\n%s", todoTranscript)
	}

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "report.json"), append(data, '\n'), 0644); err != nil {
		t.Fatalf("write report: %v", err)
	}
}

func TestModelTenTurnTranscriptAcceptanceExport(t *testing.T) {
	outDir := strings.TrimSpace(os.Getenv("NIUMA_TUI_TEN_TURN_OUT"))
	if outDir == "" {
		t.Skip("set NIUMA_TUI_TEN_TURN_OUT to export ten-turn TUI fixtures")
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		t.Fatalf("create output dir: %v", err)
	}
	events := make(chan StreamEvent, 4)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.width = 100
	model.height = 32
	var transcript []string
	type reportEntry struct {
		Turn        int    `json:"turn"`
		Answer      string `json:"answer"`
		Count       int    `json:"count"`
		LivePresent bool   `json:"live_present"`
	}
	type tailGuardReport struct {
		Tail            string `json:"tail"`
		ChromeHeight    int    `json:"chrome_height"`
		GuardLines      int    `json:"guard_lines"`
		LinesAfterTail  int    `json:"lines_after_tail"`
		TranscriptBytes int    `json:"transcript_bytes"`
	}
	var report []reportEntry
	for turn := 1; turn <= 10; turn++ {
		prompt := fmt.Sprintf("prompt-%02d", turn)
		answer := fmt.Sprintf("ANSWER_UNIQUE_%02d", turn)
		model.resetLiveDisplayBlocks()
		model.appendDisplayMessage("user", prompt, "")
		model.busy = true
		model.streamingActive = true
		transcript = append(transcript, model.pendingTranscriptBlocks()...)
		model.markTranscriptPrinted()
		model.refreshViewport()

		updated, _ := model.Update(streamEventMsg{event: StreamEvent{Type: StreamText, Text: answer}, ch: events})
		model = updated.(Model)
		updated, _ = model.Update(streamEventMsg{event: StreamEvent{Type: StreamUsage, Result: &QueryResult{Model: "m"}}, ch: events})
		model = updated.(Model)
		model.busy = false
		model.streamingActive = false
		transcript = append(transcript, model.pendingTranscriptBlocks()...)
		model.markTranscriptPrinted()
		model.refreshViewport()
		joined := stripANSI(strings.Join(transcript, "\n"))
		live := stripANSI(model.liveTranscriptView())
		report = append(report, reportEntry{
			Turn:        turn,
			Answer:      answer,
			Count:       strings.Count(joined, answer),
			LivePresent: strings.Contains(live, answer),
		})
	}
	transcriptText := stripANSI(formatTranscriptBlocks(transcript))
	finalLive := stripANSI(model.liveTranscriptView())
	if err := os.WriteFile(filepath.Join(outDir, "ten-turn-transcript.txt"), []byte(transcriptText), 0644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "ten-turn-final-live.txt"), []byte(finalLive+"\n"), 0644); err != nil {
		t.Fatalf("write final live: %v", err)
	}
	tailModel := NewModel(context.Background(), Options{Run: func(context.Context, string) (string, error) { return "", nil }})
	updated, _ := tailModel.Update(tea.WindowSizeMsg{Width: 72, Height: 24})
	tailModel = updated.(Model)
	tailModel.transcriptPrintedHeader = true
	tailModel.usage = usagePanel{
		Model:        "glm-5.1",
		Turns:        10,
		MaxTurns:     100,
		InputTokens:  20390,
		OutputTokens: 270,
	}
	tail := "TEN_TURN_TAIL_SENTINEL_MUST_STAY_VISIBLE"
	tailModel.messages = append(tailModel.messages, message{role: "assistant", content: strings.Join([]string{
		"孔龙，这个项目叫 Anything-AI，核心目的是系统性 AI 知识索引。",
		"项目结构按学习阶段组织：",
		"- 0-start-here — AI认知入门",
		"- 2-choose-tools — 工具选择矩阵",
		"- 5-skills — AI技能包",
		"- roles/ — 各行业角色案例",
		"核心理念一句话：实践出真知，用 AI 但不迷信 AI。",
		tail,
	}, "\n")})
	tailGuard := tailModel.transcriptBottomGuardLinesFor(transcriptFlushTurnComplete)
	tailOutput := stripANSI(formatTranscriptOutput(tailModel.pendingTranscriptBlocks(), tailGuard))
	if err := os.WriteFile(filepath.Join(outDir, "long-tail-transcript.txt"), []byte(tailOutput), 0644); err != nil {
		t.Fatalf("write long tail transcript: %v", err)
	}
	tailLines := strings.Split(tailOutput, "\n")
	tailLine := -1
	for i, line := range tailLines {
		if strings.Contains(line, tail) {
			tailLine = i
			break
		}
	}
	if tailLine < 0 {
		t.Fatalf("long tail transcript missing sentinel:\n%s", tailOutput)
	}
	tailChrome := tailModel.chromeHeight(tailModel.bottomChromePartsForBudget("Ready", false, tailModel.renderBudget(), tailModel.chromeModeForBudget("Ready", false, tailModel.renderBudget())), tailModel.renderBudget())
	tailReport := tailGuardReport{
		Tail:            tail,
		ChromeHeight:    tailChrome,
		GuardLines:      tailGuard,
		LinesAfterTail:  len(tailLines) - tailLine - 1,
		TranscriptBytes: len(tailOutput),
	}
	if tailReport.GuardLines != transcriptBottomGuardMinSpacer {
		t.Fatalf("long tail guard = %d, want compact spacer %d", tailReport.GuardLines, transcriptBottomGuardMinSpacer)
	}
	if tailReport.LinesAfterTail > 2 {
		t.Fatalf("long tail has large trailing gap after sentinel: lines=%d\n%s", tailReport.LinesAfterTail, tailOutput)
	}
	tailData, err := json.MarshalIndent(tailReport, "", "  ")
	if err != nil {
		t.Fatalf("marshal tail report: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "tail-guard-report.json"), append(tailData, '\n'), 0644); err != nil {
		t.Fatalf("write tail guard report: %v", err)
	}
	for _, entry := range report {
		if entry.Count != 1 || entry.LivePresent {
			t.Fatalf("bad ten-turn report entry: %+v\ntranscript:\n%s\nlive:\n%s", entry, transcriptText, finalLive)
		}
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "report.json"), append(data, '\n'), 0644); err != nil {
		t.Fatalf("write report: %v", err)
	}
}

func TestModelSessionReplayAcceptanceExport(t *testing.T) {
	outDir := strings.TrimSpace(os.Getenv("NIUMA_TUI_SESSION_REPLAY_OUT"))
	filesEnv := strings.TrimSpace(os.Getenv("NIUMA_TUI_SESSION_REPLAY_FILES"))
	if outDir == "" || filesEnv == "" {
		t.Skip("set NIUMA_TUI_SESSION_REPLAY_OUT and NIUMA_TUI_SESSION_REPLAY_FILES")
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		t.Fatalf("create output dir: %v", err)
	}
	type replayReport struct {
		Session       string         `json:"session"`
		Path          string         `json:"path"`
		TextPath      string         `json:"text_path"`
		LineIndex     map[string]int `json:"line_index"`
		TranscriptLen int            `json:"transcript_len"`
	}
	var report []replayReport
	for _, rawFile := range strings.Split(filesEnv, ",") {
		file := strings.TrimSpace(rawFile)
		if file == "" {
			continue
		}
		sessionID := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
		transcript, lines := replaySessionFileForTest(t, file)
		textPath := filepath.Join(outDir, sessionID+".txt")
		if err := os.WriteFile(textPath, []byte(transcript+"\n"), 0644); err != nil {
			t.Fatalf("write replay transcript: %v", err)
		}
		report = append(report, replayReport{
			Session:       sessionID,
			Path:          file,
			TextPath:      textPath,
			LineIndex:     lines,
			TranscriptLen: len(transcript),
		})
	}
	if len(report) == 0 {
		t.Fatal("no replay files")
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "report.json"), append(data, '\n'), 0644); err != nil {
		t.Fatalf("write report: %v", err)
	}
}

func replaySessionFileForTest(t *testing.T, file string) (string, map[string]int) {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read replay file %s: %v", file, err)
	}
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.width = 120
	model.height = 32
	model.transcriptPrintedHeader = true
	model.busy = true
	model.streamingActive = true
	seenTool := false
	firstAssistantBeforeTool := ""
	firstAssistantAfterTool := ""
	firstToolLabel := ""
	lastAssistant := ""
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("parse replay json: %v\n%s", err, line)
		}
		typ, _ := entry["type"].(string)
		role, _ := entry["role"].(string)
		content, _ := entry["content"].(string)
		switch typ {
		case "message":
			if strings.TrimSpace(content) == "" {
				continue
			}
			switch role {
			case "assistant":
				if !seenTool {
					firstAssistantBeforeTool = content
				}
				if seenTool && firstAssistantAfterTool == "" {
					firstAssistantAfterTool = content
				}
				lastAssistant = content
				model.appendDisplayTextSegment("assistant", content)
				model.markDisplayTurnDone()
			case "user", "status", "error", "recap":
				model.appendDisplayMessage(role, content, "")
			}
		case "tool_call":
			toolName := replayToolName(entry)
			if firstToolLabel == "" {
				firstToolLabel = toolDisplayName(toolName)
			}
			seenTool = true
			event := StreamEvent{Type: StreamToolStart, ToolName: toolName, ToolID: replayToolID(entry), Output: content}
			model.startToolActivity(event)
			model.appendDisplayToolFromEvent(event)
		case "tool_result":
			toolName := replayToolName(entry)
			event := StreamEvent{Type: StreamToolResult, ToolName: toolName, ToolID: replayToolID(entry), Output: content}
			model.finishToolActivity(event)
			model.updateDisplayToolFromEvent(event)
		}
	}
	model.busy = false
	model.streamingActive = false
	model.markDisplayTurnDone()
	transcript := stripANSI(formatTranscriptBlocks(model.pendingTranscriptBlocks()))
	lines := map[string]int{}
	if firstAssistantBeforeTool != "" {
		lines["assistant_before_tool"] = replayLineIndex(transcript, replayNeedle(firstAssistantBeforeTool))
	}
	if firstToolLabel != "" {
		lines["first_tool"] = replayLineIndex(transcript, firstToolLabel)
	}
	if firstAssistantAfterTool != "" {
		lines["assistant_after_tool"] = replayLineIndex(transcript, replayNeedle(firstAssistantAfterTool))
	}
	if lastAssistant != "" {
		lines["last_assistant_tail"] = replayLineIndex(transcript, replayTailNeedle(lastAssistant))
	}
	if firstAssistantBeforeTool != "" && firstToolLabel != "" && firstAssistantAfterTool != "" {
		before, tool, after := lines["assistant_before_tool"], lines["first_tool"], lines["assistant_after_tool"]
		if before < 0 || tool < 0 || after < 0 || !(before < tool && tool < after) {
			t.Fatalf("replay order failed for %s: before=%d tool=%d after=%d\n%s", file, before, tool, after, transcript)
		}
	}
	if lastAssistant != "" && lines["last_assistant_tail"] < 0 {
		t.Fatalf("replay missing assistant tail for %s needle=%q\n%s", file, replayTailNeedle(lastAssistant), transcript)
	}
	return transcript, lines
}

// replayLineIndex locates a replay needle ignoring how wide each whitespace run
// is: needles come from the raw session content, while the rendered line pads
// inline code spans, so `x` in the log arrives on screen as " x ".
func replayLineIndex(transcript, needle string) int {
	target := oneLine(needle)
	for i, line := range strings.Split(transcript, "\n") {
		if strings.Contains(oneLine(line), target) {
			return i
		}
	}
	return -1
}

func replayToolName(entry map[string]any) string {
	for _, key := range []string{"name", "tool_name"} {
		if value, _ := entry[key].(string); strings.TrimSpace(value) != "" {
			return value
		}
	}
	return "tool"
}

func replayToolID(entry map[string]any) string {
	for _, key := range []string{"tool_id", "id"} {
		if value, _ := entry[key].(string); strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func replayNeedle(content string) string {
	return truncateRunes(replayPlainNeedle(oneLine(strings.TrimSpace(content))), 16)
}

func replayTailNeedle(content string) string {
	lines := strings.Split(strings.TrimSpace(content), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" {
			return truncateRunes(replayPlainNeedle(oneLine(line)), 18)
		}
	}
	return replayNeedle(content)
}

func replayPlainNeedle(text string) string {
	replacer := strings.NewReplacer("**", "", "`", "", "*", "", "_", "")
	return strings.TrimSpace(replacer.Replace(text))
}

func truncateRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}

// newBusyStreamModel builds a busy, streaming Model with a known clock, mirroring
// the interactive wiring closely enough to exercise the live transcript body.
func newBusyStreamModel(t *testing.T, now time.Time) (Model, func(Model, StreamEvent) Model) {
	t.Helper()
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 32})
	model = updated.(Model)
	model.busy = true
	model.streamingActive = true
	model.turnStarted = time.UnixMilli(0)
	model.now = func() time.Time { return now }
	model.transcriptPrintedCount = len(model.messages)
	events := make(chan StreamEvent, 8)
	apply := func(m Model, event StreamEvent) Model {
		up, _ := m.Update(streamEventMsg{event: event, ch: events})
		return up.(Model)
	}
	return model, apply
}

// While the turn is busy but no tool/agent is actively running (waiting on the
// model between tool calls), the live body must show an animated heartbeat:
// a braille spinner frame + the running status + elapsed. This is the fix for
// "10 minutes and the TUI shows nothing" during long model round-trips.
func TestLiveWorkingLineShowsHeartbeatWhileWaiting(t *testing.T) {
	// now=12000ms → progressSpinnerFrame index 0 = "⠋"; turnStarted=0 → elapsed 12s.
	model, apply := newBusyStreamModel(t, time.UnixMilli(12000))
	model = apply(model, StreamEvent{Type: StreamToolStart, ToolName: "Bash", ToolID: "toolu_1", Output: `{"command":"echo hi"}`})
	model = apply(model, StreamEvent{Type: StreamToolResult, ToolName: "Bash", ToolID: "toolu_1", Output: "hi"})

	live := stripANSI(model.liveTranscriptView())
	if !strings.Contains(live, "⠋") {
		t.Fatalf("waiting heartbeat should show an animated braille spinner frame, got:\n%s", live)
	}
	if !strings.Contains(live, "niuma is processing tool results") {
		t.Fatalf("waiting heartbeat should show the running status text, got:\n%s", live)
	}
	if !strings.Contains(live, "12s") {
		t.Fatalf("waiting heartbeat should show elapsed time, got:\n%s", live)
	}
}

// While a tool is actively running it already renders its own spinner; the
// heartbeat status line must not be duplicated on top of it.
func TestLiveWorkingLineSuppressedWhileToolRunning(t *testing.T) {
	model, apply := newBusyStreamModel(t, time.UnixMilli(0))
	model = apply(model, StreamEvent{Type: StreamToolStart, ToolName: "Bash", ToolID: "toolu_1", Output: `{"command":"echo hi"}`})

	live := stripANSI(model.liveTranscriptView())
	if strings.Contains(live, "niuma is") {
		t.Fatalf("running tool already animates; no redundant heartbeat status line expected, got:\n%s", live)
	}
}

// truncate's limit budgets terminal columns, not bytes: slicing bytes cuts a
// multi-byte rune in half and the terminal renders the remainder as U+FFFD.
func TestTruncateDoesNotSplitMultiByteRunes(t *testing.T) {
	// The 10-byte ASCII prefix puts byte offset 60 inside a 3-byte rune.
	text := `{"query":"` + strings.Repeat("中文参数预览", 20) + `"}`

	got := truncate(text, 60)

	if !utf8.ValidString(got) {
		t.Fatalf("truncate produced invalid UTF-8: %q", got)
	}
	if width := runewidth.StringWidth(got); width > 60 {
		t.Fatalf("truncate(text, 60) = %q occupies %d columns, want <= 60", got, width)
	}
	if !strings.HasPrefix(text, strings.TrimSuffix(got, "...")) {
		t.Fatalf("truncate(text, 60) = %q is not a prefix of the input", got)
	}
}

// The tool-call parameter preview is the user-visible path for the split above.
func TestToolInputSummaryKeepsChineseDescriptionIntact(t *testing.T) {
	// "MCP " puts byte offset 60 inside a 3-byte rune.
	description := "MCP " + strings.Repeat("抓取中文网页", 20)

	got := toolInputSummary("some_mcp_tool", `{"description":`+strconv.Quote(description)+`}`)

	if !utf8.ValidString(got) {
		t.Fatalf("tool preview produced invalid UTF-8: %q", got)
	}
	if !strings.HasPrefix(description, strings.TrimSuffix(got, "...")) {
		t.Fatalf("tool preview %q is not a prefix of the description", got)
	}
}

// When the turn is idle there is no heartbeat.
func TestLiveWorkingLineHiddenWhenIdle(t *testing.T) {
	model, apply := newBusyStreamModel(t, time.UnixMilli(0))
	model = apply(model, StreamEvent{Type: StreamToolStart, ToolName: "Bash", ToolID: "toolu_1", Output: `{"command":"echo hi"}`})
	model = apply(model, StreamEvent{Type: StreamToolResult, ToolName: "Bash", ToolID: "toolu_1", Output: "hi"})
	model.busy = false
	model.streamingActive = false

	live := stripANSI(model.liveTranscriptView())
	if strings.Contains(live, "niuma is") {
		t.Fatalf("idle turn should not show a heartbeat status line, got:\n%s", live)
	}
}

func TestAskUserQuestionRenderingSummaries(t *testing.T) {
	if got := toolInputSummary("AskUserQuestion", `{"question":"选哪个方案？","choices":["A"]}`); got != "选哪个方案？" {
		t.Fatalf("toolInputSummary = %q", got)
	}
	if got := errorResultSummary("User input required: 选哪个方案？\n- A"); got != "等待用户输入" {
		t.Fatalf("errorResultSummary = %q", got)
	}
	if got := toolResultSummaryForStatus("AskUserQuestion", "User answered: 方案B", false); got != "User answered: 方案B" {
		t.Fatalf("toolResultSummaryForStatus = %q", got)
	}
	if got := friendlyToolResult("AskUserQuestion", "User answered: 方案B"); got != "方案B" {
		t.Fatalf("friendlyToolResult = %q", got)
	}
}

func TestAskUserQuestionAnswerShownInToolLine(t *testing.T) {
	model, apply := newBusyStreamModel(t, time.UnixMilli(0))
	model = apply(model, StreamEvent{Type: StreamToolStart, ToolName: "AskUserQuestion", ToolID: "toolu_q1", Output: `{"question":"你想聊哪类旅游问题？","choices":["城市漫游"]}`})
	model = apply(model, StreamEvent{Type: StreamToolResult, ToolName: "AskUserQuestion", ToolID: "toolu_q1", Output: "User answered: 城市漫游"})
	view := stripANSI(model.View())
	if !strings.Contains(view, "你想聊哪类旅游问题？") || !strings.Contains(view, "完成：城市漫游") {
		t.Fatalf("tool line should show question and user's answer, view:\n%s", view)
	}
}

func newUserQuestionModel(t *testing.T, choices []string) (Model, chan UserQuestionAnswer) {
	t.Helper()
	reply := make(chan UserQuestionAnswer, 1)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	event := StreamEvent{
		Type:          StreamUserQuestion,
		Question:      &UserQuestionRequest{Question: "选哪个方案？", Choices: choices},
		QuestionReply: reply,
	}
	updated, cmd := model.Update(streamEventMsg{event: event, ch: make(chan StreamEvent)})
	model = updated.(Model)
	// 弹卡片会返回一个 flush cmd，把本轮已完成的块推进真实 scrollback
	// （transcriptFlushInteractivePrompt），因此这里的 cmd 不再为 nil。
	if model.pendingQuestion == nil {
		t.Fatalf("cmd=%v pendingQuestion=%+v", cmd, model.pendingQuestion)
	}
	return model, reply
}

func TestModelUserQuestionChoiceSelection(t *testing.T) {
	model, reply := newUserQuestionModel(t, []string{"方案A", "方案B"})
	view := stripANSI(model.View())
	for _, want := range []string{"选哪个方案？", "方案A", "方案B", "输入:"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q in view:\n%s", want, view)
		}
	}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	answer := <-reply
	if cmd == nil || !answer.Answered || answer.Answer != "方案B" || model.pendingQuestion != nil {
		t.Fatalf("cmd=%v answer=%+v pending=%+v", cmd, answer, model.pendingQuestion)
	}
}

func TestModelUserQuestionDigitQuickSelect(t *testing.T) {
	model, reply := newUserQuestionModel(t, []string{"方案A", "方案B"})
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	model = updated.(Model)
	answer := <-reply
	if cmd == nil || !answer.Answered || answer.Answer != "方案B" || model.pendingQuestion != nil {
		t.Fatalf("cmd=%v answer=%+v", cmd, answer)
	}
}

func TestModelUserQuestionEscCancels(t *testing.T) {
	model, reply := newUserQuestionModel(t, []string{"方案A"})
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	answer := <-reply
	if cmd == nil || answer.Answered || answer.Answer != "" || model.pendingQuestion != nil {
		t.Fatalf("cmd=%v answer=%+v", cmd, answer)
	}
}

func TestModelUserQuestionFreeTextViaInputRow(t *testing.T) {
	model, reply := newUserQuestionModel(t, []string{"方案A", "方案B"})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	model = updated.(Model)
	if model.pendingQuestion == nil || model.pendingQuestion.selected != 2 {
		t.Fatalf("digit 3 should focus input row, pending=%+v", model.pendingQuestion)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("自定义答案")})
	model = updated.(Model)
	if !strings.Contains(stripANSI(model.View()), "自定义答案") {
		t.Fatalf("view missing typed text:\n%s", stripANSI(model.View()))
	}
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	answer := <-reply
	if cmd == nil || !answer.Answered || answer.Answer != "自定义答案" || model.pendingQuestion != nil {
		t.Fatalf("cmd=%v answer=%+v", cmd, answer)
	}
}

func TestModelUserQuestionTypingJumpsToInputRow(t *testing.T) {
	model, reply := newUserQuestionModel(t, []string{"方案A", "方案B"})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("山")})
	model = updated.(Model)
	if model.pendingQuestion == nil || model.pendingQuestion.selected != 2 || model.pendingQuestion.textBuf != "山" {
		t.Fatalf("typing should jump to input row, pending=%+v", model.pendingQuestion)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("水")})
	model = updated.(Model)
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	answer := <-reply
	if cmd == nil || !answer.Answered || answer.Answer != "山水" || model.pendingQuestion != nil {
		t.Fatalf("cmd=%v answer=%+v", cmd, answer)
	}
}

func TestModelUserQuestionEscClearsInputThenCancels(t *testing.T) {
	model, reply := newUserQuestionModel(t, []string{"方案A"})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("草稿")})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.pendingQuestion == nil || model.pendingQuestion.textBuf != "" {
		t.Fatalf("esc should clear input first, pending=%+v", model.pendingQuestion)
	}
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	answer := <-reply
	if cmd == nil || answer.Answered || model.pendingQuestion != nil {
		t.Fatalf("cmd=%v answer=%+v", cmd, answer)
	}
}

func TestModelUserQuestionNoChoicesFocusesInputRow(t *testing.T) {
	model, reply := newUserQuestionModel(t, nil)
	if model.pendingQuestion.selected != 0 {
		t.Fatalf("expect input row focused, pending=%+v", model.pendingQuestion)
	}
	if !strings.Contains(stripANSI(model.View()), "输入:") {
		t.Fatalf("view missing input line:\n%s", stripANSI(model.View()))
	}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("好")})
	model = updated.(Model)
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	answer := <-reply
	if cmd == nil || !answer.Answered || answer.Answer != "好" || model.pendingQuestion != nil {
		t.Fatalf("cmd=%v answer=%+v", cmd, answer)
	}
}
