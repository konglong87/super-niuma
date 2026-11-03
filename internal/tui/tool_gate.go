// tool_gate.go 识别权限网关拦截类的工具输出，单独成卡片而不是当报错。

package tui

import (
	"strings"
)

type toolGateBlock struct {
	Rule       string
	Title      string
	Summary    string
	Reason     string
	NextAction string
}

func classifyToolGateBlock(rawOutput string) (toolGateBlock, bool) {
	text := strings.TrimSpace(rawOutput)
	if text == "" {
		return toolGateBlock{}, false
	}
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(text, "Pre-Commit Scope Gate"):
		return toolGateBlock{
			Rule:       "pre_commit_scope",
			Title:      "Pre-Commit Scope Gate",
			Summary:    "需要先验证暂存范围",
			Reason:     "git commit 会固化本地变更，命令尚未执行；需要先确认 staged 文件确实属于本次任务。",
			NextAction: "先运行 git status --short --branch、git diff --name-status、git diff --cached --name-status，确认范围后再提交。",
		}, true
	case strings.Contains(text, "Shared-State Git Gate") && strings.Contains(lower, "git push"):
		return toolGateBlock{
			Rule:       "shared_state_git_push",
			Title:      "Shared-State Git Gate",
			Summary:    "需要先验证分支/HEAD/远程状态",
			Reason:     "git push 会修改远程共享状态，命令尚未执行；需要先确认当前分支、HEAD 和 upstream。",
			NextAction: "先运行 git status --short --branch、git rev-parse HEAD、git rev-parse @{u} 或 git branch -vv，确认后再 push。",
		}, true
	case strings.Contains(text, "Destructive Shared-State Gate"):
		return toolGateBlock{
			Rule:       "destructive_shared_state",
			Title:      "Destructive Shared-State Gate",
			Summary:    "需要明确授权危险 Git 操作",
			Reason:     "命令可能 force push、删除远程 tag 或移动共享对象，尚未执行。",
			NextAction: "先向用户确认目标和影响范围；未获明确授权时只做只读检查。",
		}, true
	case strings.Contains(text, "External Action Gate"):
		return toolGateBlock{
			Rule:       "external_action",
			Title:      "External Action Gate",
			Summary:    "需要确认外部副作用",
			Reason:     "命令可能部署、发布、发送或修改外部系统，尚未执行。",
			NextAction: "先确认目标系统和授权边界；需要时改为 dry-run 或预览。",
		}, true
	case strings.Contains(text, "Final Claim Gate"):
		return toolGateBlock{
			Rule:       "final_claim",
			Title:      "Final Claim Gate",
			Summary:    "缺少完成声明证据",
			Reason:     "最终答复声称了尚未被当前会话证据支持的结果。",
			NextAction: "继续补齐缺失验证，或在回复中明确未验证边界。",
		}, true
	case strings.Contains(text, "Read Scope Gate"):
		return toolGateBlock{
			Rule:       "read_scope",
			Title:      "Read Scope Gate",
			Summary:    "需要先读取必要证据",
			Reason:     "回答依赖本地文件或仓库事实，但当前会话还没有足够读取证据。",
			NextAction: "先用 Read/Grep/Glob/LS 或只读 Bash 收集证据，再继续回答。",
		}, true
	case strings.Contains(text, "Post-Action Delta Gate"):
		return toolGateBlock{
			Rule:       "post_action_delta",
			Title:      "Post-Action Delta Gate",
			Summary:    "需要先完成变更后检查",
			Reason:     "本轮已经改变文件或共享状态，但仍缺少必要的 post-change 检查。",
			NextAction: "运行提示中的 scope、metadata 或语义验证命令，检查输出后再声明完成。",
		}, true
	case strings.Contains(text, "Failed Audit Evidence Gate"):
		return toolGateBlock{
			Rule:       "failed_audit_evidence",
			Title:      "Failed Audit Evidence Gate",
			Summary:    "审计命令失败，不能直接下全局结论",
			Reason:     "宽范围审计/搜索命令失败，但回答试图给出全局或对比结论。",
			NextAction: "补跑审计、用等价只读命令替代，或明确只给局部结论。",
		}, true
	case strings.Contains(lower, "outside the current workspace"):
		return toolGateBlock{
			Rule:       "workspace_write_boundary",
			Title:      "Workspace Write Boundary",
			Summary:    "写入位置不在当前工作区",
			Reason:     "目标文件或符号链接真实路径不在当前工作区，也不在已配置的可写目录内，所以没有执行写入。",
			NextAction: "确认是否需要把真实目录加入可写范围，或改写当前工作区内的文件。",
		}, true
	case strings.Contains(lower, "permission denied"):
		return toolGateBlock{
			Rule:       "permission_denied",
			Title:      "Permission Denied",
			Summary:    "没有写入权限",
			Reason:     "系统或运行时权限拒绝了这次操作。",
			NextAction: "确认文件权限、目录权限或当前权限模式后再重试。",
		}, true
	case strings.Contains(text, "Tool blocked by"):
		return toolGateBlock{
			Rule:       "tool_blocked",
			Title:      "Runtime Gate",
			Summary:    "安全检查要求先补充验证",
			Reason:     "niuma runtime 拦截了该工具调用，命令尚未执行。",
			NextAction: "按拦截提示补齐验证或授权后再重试。",
		}, true
	case strings.Contains(text, "Completion is blocked by"):
		return toolGateBlock{
			Rule:       "completion_blocked",
			Title:      "Completion Gate",
			Summary:    "需要先补齐完成条件",
			Reason:     "niuma runtime 拦截了当前完成声明。",
			NextAction: "按拦截提示补齐证据，或明确未验证边界。",
		}, true
	default:
		return toolGateBlock{}, false
	}
}

func isToolGateBlocked(rawOutput string) bool {
	_, ok := classifyToolGateBlock(rawOutput)
	return ok
}

func gatePreflightSummary(rawOutput string) (string, bool) {
	lower := strings.ToLower(rawOutput)
	if !strings.Contains(lower, "<gate-preflight") {
		return "", false
	}
	failed := strings.Contains(lower, `status="failed"`)
	switch {
	case strings.Contains(lower, `rule_id="pre_commit_scope"`):
		if failed {
			return "提交前检查未完成", true
		}
		return "已完成提交前检查", true
	case strings.Contains(lower, `rule_id="shared_state_git_push"`):
		if failed {
			return "推送前检查未完成", true
		}
		return "已完成推送前检查", true
	case strings.Contains(lower, `rule_id="shared_state_git_tag"`):
		if failed {
			return "标签操作前检查未完成", true
		}
		return "已完成标签操作前检查", true
	default:
		if failed {
			return "前置检查未完成", true
		}
		return "已完成前置检查", true
	}
}
