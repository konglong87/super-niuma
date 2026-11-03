package agentruntime

import (
	"fmt"
	"os"
	"strings"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/promptdump"
)

func (r Runtime) dumpPromptRequest(req Request, result Result, taskID uint64, turn int, modelReq anthropic.MessagesRequest, toolResultLimit, messageBudget, historyBudget int) error {
	path := strings.TrimSpace(os.Getenv(promptdump.PathEnv))
	if path == "" {
		return nil
	}
	record := promptdump.Build(promptdump.Metadata{
		Scope:           "subagent",
		SessionID:       result.SessionID,
		TenantSessionID: req.ParentSessionID,
		ParentSessionID: req.ParentSessionID,
		TaskID:          taskID,
		Turn:            turn,
		QuerySource:     "agentruntime",
		PromptMode:      "subagent",
		AgentName:       result.AgentName,
		AgentMode:       result.AgentMode,
		ToolResultLimit: toolResultLimit,
		MessageBudget:   messageBudget,
		HistoryBudget:   historyBudget,
	}, modelReq, promptdump.IsEnvTruthy(promptdump.FullEnv))
	if err := promptdump.Append(path, record); err != nil {
		return fmt.Errorf("agent prompt dump: %w", err)
	}
	return nil
}
