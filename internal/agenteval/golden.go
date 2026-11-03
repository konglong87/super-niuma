package agenteval

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/query"
	"github.com/konglong87/super-niuma/internal/session"
	"github.com/konglong87/super-niuma/internal/tools"
	"github.com/konglong87/super-niuma/internal/tools/bash"
	"github.com/konglong87/super-niuma/internal/tools/fileread"
)

// gitFixtureEnv 隔离用户全局 git 配置，固定作者身份，禁用交互编辑器，
// 保证 fixture 播种过程确定性。agent 侧（Bash 工具内）的 git 行为不受此影响，
// 由仓库本地 config 与 bash.commandEnv 的注入共同保证。
func gitFixtureEnv() []string {
	return append(os.Environ(),
		"GIT_AUTHOR_NAME=Eval Fixture",
		"GIT_AUTHOR_EMAIL=eval@example.com",
		"GIT_COMMITTER_NAME=Eval Fixture",
		"GIT_COMMITTER_EMAIL=eval@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_EDITOR=true",
		"GIT_SEQUENCE_EDITOR=true",
	)
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitFixtureEnv()
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func runGit(dir string, args ...string) error {
	out, err := gitOutput(dir, args...)
	if err != nil {
		return fmt.Errorf("git %s in %s: %w: %s", strings.Join(args, " "), dir, err, out)
	}
	return nil
}

func setupGitWorkflowFixture(workspace string, withRemoteDivergence bool) (string, string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", "", fmt.Errorf("git binary not found: %w", err)
	}
	originDir := filepath.Join(workspace, "origin.git")
	repoDir := filepath.Join(workspace, "repo")
	seedDir := filepath.Join(workspace, "seed")

	if err := runGit(workspace, "init", "--bare", "--initial-branch=main", "origin.git"); err != nil {
		return "", "", err
	}
	if err := runGit(workspace, "clone", originDir, seedDir); err != nil {
		return "", "", err
	}
	if err := configureFixtureRepo(seedDir); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(filepath.Join(seedDir, "note.txt"), []byte("base\n"), 0644); err != nil {
		return "", "", err
	}
	if err := runGit(seedDir, "add", "note.txt"); err != nil {
		return "", "", err
	}
	if err := runGit(seedDir, "commit", "-m", "base"); err != nil {
		return "", "", err
	}
	if err := runGit(seedDir, "push", "origin", "HEAD:main"); err != nil {
		return "", "", err
	}

	// repo 在 divergence 提交之前 clone，因此停留在 base
	if err := runGit(workspace, "clone", originDir, repoDir); err != nil {
		return "", "", err
	}
	if err := configureFixtureRepo(repoDir); err != nil {
		return "", "", err
	}

	if withRemoteDivergence {
		if err := os.WriteFile(filepath.Join(seedDir, "note.txt"), []byte("remote change\n"), 0644); err != nil {
			return "", "", err
		}
		if err := runGit(seedDir, "commit", "-am", "remote change"); err != nil {
			return "", "", err
		}
		if err := runGit(seedDir, "push", "origin", "HEAD:main"); err != nil {
			return "", "", err
		}
	}

	if err := os.WriteFile(filepath.Join(repoDir, "note.txt"), []byte("local change\n"), 0644); err != nil {
		return "", "", err
	}
	return repoDir, originDir, nil
}

// configureFixtureRepo 写仓库本地配置，覆盖 agent 侧 Bash 内的 git 行为：
// 固定提交身份、禁用签名、屏蔽全局 hooks，并固定 rebase 后端为 merge，
// 让 git_conflict_recovery 的 rebase 中断目录（rebase-merge）与断言路径确定一致。
func configureFixtureRepo(dir string) error {
	for _, args := range [][]string{
		{"config", "user.name", "Eval Fixture"},
		{"config", "user.email", "eval@example.com"},
		{"config", "commit.gpgsign", "false"},
		{"config", "core.hooksPath", ".git/no-hooks"},
		{"config", "rebase.backend", "merge"},
	} {
		if err := runGit(dir, args...); err != nil {
			return err
		}
	}
	return nil
}

// 步骤表体现系统提示要求的「先核查 → 再 commit/push」工作流：
// scope 核查必须在最后一次 staging（git add）之后，upstream 核查在每次 push 之前，
// 措辞与 closure gate 的放行证据（preCommitScopeVerified / prePushGitVerified）对齐，
// 因此 gate 首次即放行、GatePreflights 应为 0。
var gitWorkflowSteps = map[string][]string{
	"git_commit_push": {
		"git add note.txt",
		"git status --short --branch && git diff --name-status && git diff --cached --name-status",
		`git commit -m "local change"`,
		"git status --short --branch && git rev-parse HEAD && git rev-parse @{u}",
		"git push origin main",
		"git status --short --branch && git log --oneline -2",
	},
	"git_conflict_recovery": {
		"git add note.txt",
		"git status --short --branch && git diff --name-status && git diff --cached --name-status",
		`git commit -m "local change"`,
		"git status --short --branch && git rev-parse HEAD && git rev-parse @{u}",
		"git push origin main",
		"git pull --rebase origin main",
		`printf 'local change\n' > note.txt && git add note.txt && git rebase --continue`,
		"git status --short --branch && git rev-parse HEAD && git rev-parse @{u}",
		"git push origin main",
		"git status --short --branch && git log --oneline -3",
	},
}

var gitWorkflowFinalText = map[string]string{
	"git_commit_push":       "git commit push ok",
	"git_conflict_recovery": "git conflict recovery ok",
}

func bashToolInput(command string) string {
	data, _ := json.Marshal(map[string]string{"command": command})
	return string(data)
}

func lastToolResultText(messages []anthropic.MessageParam) string {
	for i := len(messages) - 1; i >= 0; i-- {
		for j := len(messages[i].Content) - 1; j >= 0; j-- {
			block := messages[i].Content[j]
			if block.Type == "tool_result" {
				return block.Content
			}
		}
	}
	return ""
}

// streamGitWorkflowStep 按步骤表驱动 git 工作流：
// gate preflight 拦截（原命令未执行）时重发同一条命令，否则前进；
// 命令自身失败（push 被拒、pull 冲突）不重试，由后续步骤恢复。
func (s *scriptedStreamer) streamGitWorkflowStep(cb anthropic.StreamCallbacks, req anthropic.MessagesRequest) (*anthropic.StreamResult, error) {
	steps := gitWorkflowSteps[s.caseType]
	s.mu.Lock()
	if s.gitStep > 0 && s.gitStep <= len(steps) &&
		strings.Contains(lastToolResultText(req.Messages), gatePreflightMarker) {
		s.gitStep--
	}
	step := s.gitStep
	s.gitStep++
	call := s.calls
	s.mu.Unlock()
	if step < len(steps) {
		return streamTool(fmt.Sprintf("toolu_git_%d", call), "Bash", bashToolInput(steps[step])), nil
	}
	return streamTextWithCallback(cb, gitWorkflowFinalText[s.caseType]), nil
}

// GoldenSuite 是黄金工作流回归套件：用真实 git 仓库和真实 Bash 工具
// 跑通事故级工作流，并对轮次预算做断言。每个 pending-fixes 事故分析
// 收敛后应在此新增对应案例（规约见 docs/pending-fixes/README.md）。
func GoldenSuite() Suite {
	zeroGates := 0
	return Suite{
		ID:          "niuma-golden-workflows",
		Description: "Golden workflow regression suite: real git workflows with turn and gate budgets.",
		Cases: []Case{
			{
				ID:                      "git_commit_push",
				Type:                    "git_commit_push",
				Description:             "Commit a local change and push to origin main through closure gates.",
				Prompt:                  "Commit the local change to note.txt and push it to origin main.",
				ExpectContains:          []string{"git commit push ok"},
				ExpectToolCalls:         []string{"Bash"},
				ExpectMaxTurns:          7,
				ExpectMaxGatePreflights: &zeroGates,
			},
			{
				ID:                      "git_conflict_recovery",
				Type:                    "git_conflict_recovery",
				Description:             "Recover from a rejected push and rebase conflict without leaving git in a broken state.",
				Prompt:                  "Commit the local change, push, and recover from the rebase conflict.",
				ExpectContains:          []string{"git conflict recovery ok"},
				ExpectToolCalls:         []string{"Bash"},
				ExpectMaxTurns:          11,
				ExpectMaxGatePreflights: &zeroGates,
			},
		},
	}
}

func selectSuite(opts Options) (Suite, error) {
	if strings.TrimSpace(opts.Dataset) != "" && strings.TrimSpace(opts.Suite) != "" {
		return Suite{}, fmt.Errorf("--suite and --dataset are mutually exclusive: pass a built-in suite name or a dataset file, not both")
	}
	if strings.TrimSpace(opts.Dataset) != "" {
		return loadSuite(opts.Dataset)
	}
	switch strings.TrimSpace(opts.Suite) {
	case "", "default":
		return DefaultSuite(), nil
	case "golden":
		return GoldenSuite(), nil
	default:
		return Suite{}, fmt.Errorf("unknown eval suite %q (available: default, golden)", opts.Suite)
	}
}

func runGitWorkflowCase(ctx context.Context, workspace string, testCase Case, result CaseResult) CaseResult {
	repoDir, originDir, err := setupGitWorkflowFixture(workspace, testCase.Type == "git_conflict_recovery")
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		return result
	}
	streamer := newScriptedStreamer(testCase.Type)
	registry := tools.NewRegistry(fileread.New(), bash.New())
	recorder, err := session.DefaultStore().NewRecorder(repoDir)
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		return result
	}
	defer recorder.Close()
	querySession := query.New(streamer, registry, query.Options{
		Model:           "eval-model",
		MaxTurns:        16,
		CWD:             repoDir,
		Recorder:        recorder,
		TraceID:         observability.TraceID(ctx),
		ToolResultLimit: 32 * 1024,
	})
	response, err := querySession.Run(ctx, firstNonEmpty(testCase.Prompt, "run git workflow"), io.Discard)
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
	}
	result.Evidence["response"] = response.Response
	result.Evidence["tool_calls"] = toolNames(response.ToolCalls)
	result.Evidence["usage"] = response.Usage
	metrics := collectCaseMetrics(response)
	result.Metrics = &metrics
	addBudgetChecks(&result, metrics, testCase)
	addContainsChecks(&result, response.Response, testCase.ExpectContains)
	addToolChecks(&result, response.ToolCalls, testCase.ExpectToolCalls)
	addCheck(&result, "query.completed", err == nil, errorDetail(err))

	status, statusErr := gitOutput(repoDir, "status", "--porcelain")
	result.Evidence["git_status"] = status
	addCheck(&result, "git.clean", statusErr == nil && status == "", "working tree clean after workflow")

	branch, branchErr := gitOutput(repoDir, "rev-parse", "--abbrev-ref", "HEAD")
	addCheck(&result, "git.on_main", branchErr == nil && branch == "main", "repo on main, not detached HEAD")

	// 两种 rebase 后端各留不同的中断目录：merge 后端用 rebase-merge，apply 后端用 rebase-apply。
	_, rebaseMergeErr := os.Stat(filepath.Join(repoDir, ".git", "rebase-merge"))
	_, rebaseApplyErr := os.Stat(filepath.Join(repoDir, ".git", "rebase-apply"))
	addCheck(&result, "git.rebase_completed", os.IsNotExist(rebaseMergeErr) && os.IsNotExist(rebaseApplyErr), "no interrupted rebase left behind")

	wantCommits := "2"
	if testCase.Type == "git_conflict_recovery" {
		wantCommits = "3"
	}
	commits, commitsErr := gitOutput(originDir, "rev-list", "--count", "main")
	result.Evidence["origin_commits"] = commits
	addCheck(&result, "git.pushed", commitsErr == nil && commits == wantCommits,
		fmt.Sprintf("origin main has %s commits (want %s)", commits, wantCommits))
	return result
}
