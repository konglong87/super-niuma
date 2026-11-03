// types.go 汇总 TUI 对外暴露的数据结构与回调签名，internal/cli 只依赖这一层。

package tui

import (
	"context"
	"encoding/json"
	"time"

	"github.com/konglong87/super-niuma/internal/pendinginput"
)

type QueryFunc func(ctx context.Context, prompt string) (string, error)
type QueryResultFunc func(ctx context.Context, prompt string) (QueryResult, error)
type StreamFunc func(ctx context.Context, prompt string, events chan<- StreamEvent) error
type QueryWithAttachmentsFunc func(ctx context.Context, prompt string, attachments []Attachment) (QueryResult, error)
type StreamWithAttachmentsFunc func(ctx context.Context, prompt string, attachments []Attachment, events chan<- StreamEvent) error
type SlashCommandProvider func(ctx context.Context, prefix string) ([]SlashCommand, error)
type ResumeSessionProvider func(ctx context.Context) ([]ResumeSession, error)
type RewindCandidateProvider func(ctx context.Context) ([]RewindCandidate, error)
type ClipboardImageImporter func(ctx context.Context) (Attachment, bool, error)
type ClipboardImageDetector func(ctx context.Context) (bool, error)
type PermissionModeSwitcher func(current string) (string, error)
type AwayRecapFunc func(ctx context.Context, events chan<- StreamEvent) error

// NextStepsFunc 由 runNextStepsCmd 自建的 channel 在其自己的 goroutine 里
// **同步**调用：实现不得另起 goroutine 往 events 写——函数一返回，调用方就会
// close(events)，往已关闭的 channel 发送会 panic。写入量也不得超过调用方 channel
// 的缓冲区（目前 cap 4）：drain 端没有 reader 也没有超时，写多了会永久阻塞。
type NextStepsFunc func(ctx context.Context, prompt string, result QueryResult, events chan<- StreamEvent) error
type BackgroundWatcher func(ctx context.Context, previous map[string]BackgroundSnapshot) ([]BackgroundUpdate, error)

type Options struct {
	Title                    string
	Welcome                  WelcomeInfo
	Run                      QueryFunc
	RunResult                QueryResultFunc
	RunStream                StreamFunc
	RunWithAttachments       QueryWithAttachmentsFunc
	RunStreamWithAttachments StreamWithAttachmentsFunc
	SlashCommands            SlashCommandProvider
	ResumeSessions           ResumeSessionProvider
	RewindCandidates         RewindCandidateProvider
	ImportClipboard          ClipboardImageImporter
	DetectClipboardImage     ClipboardImageDetector
	SwitchPermissionMode     PermissionModeSwitcher
	RunAwayRecap             AwayRecapFunc
	RunNextSteps             NextStepsFunc
	RunPostTurnRecap         AwayRecapFunc
	AwayRecapDelay           time.Duration
	WatchBackground          BackgroundWatcher
	BackgroundPollInterval   time.Duration
	InitialMessages          []InitialMessage
	InitialThinking          []ThinkingDetail
	DraftStore               DraftStore
	PendingInputQueue        pendinginput.Queue
	EnableMouseTracking      bool
	WaitForInitialWindowSize bool
}

// WelcomeInfo is resolved by the CLI and rendered by the TUI at startup. Keeping
// it as plain data lets the UI stay independent from config and provider code.
type WelcomeInfo struct {
	Version        string
	Model          string
	Provider       string
	PromptMode     string
	ContextLength  int
	CWD            string
	SessionID      string
	PermissionMode string
	Sandbox        string
	// SandboxWarnings names each configured sandbox option that is not actually
	// enforced on this host. Empty on a healthy install, so the card is unchanged.
	SandboxWarnings []string
	ToolSummary     string
	MCPServers      int
	Resume          string
	SessionStatus   string
	// ShowThinking controls conversation rendering only. Nil preserves the
	// backward-compatible default and displays reasoning content.
	ShowThinking   *bool
	ThinkingMode   string
	Goal           string
	GoalStep       string
	GoalCriteria   string
	GoalEvidence   string
	GoalNextAction string
}

// InitialMessage seeds the terminal transcript before the first prompt. It is
// used for resume recovery notices without polluting the model transcript.
type InitialMessage struct {
	Role      string
	Content   string
	Turn      int
	Phase     int
	CreatedAt time.Time
}

type ThinkingDetail struct {
	Turn      int
	Phase     int
	Content   string
	CreatedAt time.Time
}

type SlashCommand struct {
	Name        string
	Description string
	Source      string
}

type ResumeSession struct {
	ID      string
	Title   string
	Preview string
	CWD     string
	Updated time.Time
}

type RewindCandidate struct {
	ID      string
	Preview string
	Mode    string
}

type Attachment struct {
	ID        int
	Type      string
	MediaType string
	Name      string
	Path      string
	URL       string
	SizeBytes int64
}

type StreamEventType string

type StreamNoticeKind string

const (
	StreamText              StreamEventType = "text"
	StreamTextAmended       StreamEventType = "text_amended"
	StreamThinking          StreamEventType = "thinking"
	StreamToolStart         StreamEventType = "tool_start"
	StreamToolResult        StreamEventType = "tool_result"
	StreamNestedProgress    StreamEventType = "nested_agent_progress"
	StreamPermissionRequest StreamEventType = "permission_request"
	StreamUserQuestion      StreamEventType = "user_question"
	StreamUsage             StreamEventType = "usage"
	StreamFinished          StreamEventType = "finished"
	StreamConfigReload      StreamEventType = "config_reload"
	StreamSessionResume     StreamEventType = "session_resume"
	StreamRecap             StreamEventType = "recap"
	StreamNextSteps         StreamEventType = "next_steps"

	StreamNoticeConfigReload StreamNoticeKind = "config_reload"
)

type QueryResult struct {
	Response   string
	Model      string
	StopReason string
	Turns      int
	SessionID  string
	Usage      Usage
	ToolCalls  []ToolCall
	Context    RuntimeContext
}

type Usage struct {
	InputTokens                         int
	OutputTokens                        int
	LastInputTokens                     int
	CacheCreationInputTokens            int
	CacheReadInputTokens                int
	CacheCreationEphemeral1hInputTokens int
	CacheCreationEphemeral5mInputTokens int
	ServiceTier                         string
	InferenceGeo                        string
	Speed                               string
}

type ToolCall struct {
	ID      string `json:"id,omitempty"`
	Name    string `json:"name,omitempty"`
	Output  string `json:"output,omitempty"`
	IsError bool   `json:"is_error,omitempty"`
}

type RuntimeContext struct {
	CWD           string
	MaxTurns      int
	MaxTokens     int
	ToolCount     int
	ContextHint   string
	ContextWindow int
}

type StreamEvent struct {
	Type   StreamEventType
	Event  string
	TaskID uint64
	Text   string
	// PrevText carries the live-streamed text a StreamTextAmended event
	// replaces (Text is the corrected text; empty Text retracts PrevText).
	PrevText      string
	ToolName      string
	ToolID        string
	Output        string
	Input         string
	IsError       bool
	Payload       json.RawMessage
	Permission    *PermissionRequest
	Reply         chan PermissionDecision
	Question      *UserQuestionRequest
	QuestionReply chan UserQuestionAnswer
	Result        *QueryResult
	Err           error
	Welcome       *WelcomeInfo
	History       []InitialMessage
	Thinking      []ThinkingDetail
	NoticeKind    StreamNoticeKind
}

type BackgroundSnapshot struct {
	RunCount    int
	LogSize     int
	Status      string
	EventOffset int64
}

type BackgroundUpdate struct {
	ID         string
	ScheduleID string
	Kind       string
	Prompt     string
	Status     string
	RunCount   int
	LastRunAt  *time.Time
	LogSize    int
	LogTail    string
	Silent     bool
}

type PermissionRequest struct {
	ToolName string
	Request  string
	Reason   string
	Rule     string
	Source   string
	Input    json.RawMessage
	OneShot  bool
}

type PermissionDecision struct {
	Allowed     bool
	Destination string
	Reason      string
	Rule        string
}

type UserQuestionRequest struct {
	Question string
	Choices  []string
}

type UserQuestionAnswer struct {
	Answered bool
	Answer   string
}
