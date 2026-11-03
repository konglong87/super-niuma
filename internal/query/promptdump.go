package query

import (
	"fmt"
	"os"
	"strings"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/promptdump"
)

const (
	promptDumpPathEnv = promptdump.PathEnv
	promptDumpFullEnv = promptdump.FullEnv
)

type promptDumpRecord = promptdump.Record
type promptDumpMessageSummary = promptdump.MessageSummary
type promptDumpContentBlockSummary = promptdump.ContentBlockSummary
type promptDumpToolSummary = promptdump.ToolSummary
type promptDumpCompactState = promptdump.CompactState
type promptDumpToolResultStats = promptdump.ToolResultStats
type promptDumpRequestRedaction = promptdump.RequestRedaction

func (s *Session) dumpPromptRequest(turn int, req anthropic.MessagesRequest, manifest ContextManifest) error {
	path := strings.TrimSpace(os.Getenv(promptDumpPathEnv))
	if path == "" {
		return nil
	}
	record := buildPromptDumpRecord(s, turn, req, manifest, promptdump.IsEnvTruthy(promptDumpFullEnv))
	if err := promptdump.Append(path, record); err != nil {
		return fmt.Errorf("prompt dump: %w", err)
	}
	return nil
}

func buildPromptDumpRecord(s *Session, turn int, req anthropic.MessagesRequest, manifest ContextManifest, full bool) promptDumpRecord {
	req.Messages = s.computerAuditHistory(req.Messages)
	return promptdump.Build(promptdump.Metadata{
		SessionID:          s.streamSessionID(),
		TenantSessionID:    s.options.TenantSessionID,
		Turn:               turn,
		QuerySource:        s.querySource(),
		PromptMode:         s.promptProfile().String(),
		PromptProfile:      s.promptProfileVariant(),
		RuntimeProfile:     s.runtimeProfile().String(),
		ContextManifest:    manifest,
		AutoCompactEnabled: s.AutoCompactEnabled(),
		ToolResultLimit:    s.options.ToolResultLimit,
		MessageBudget:      s.options.ToolResultMessageBudget,
		HistoryBudget:      s.options.ToolResultHistoryBudget,
	}, req, full)
}
