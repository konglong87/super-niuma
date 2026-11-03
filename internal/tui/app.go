// app.go 是 Bubble Tea 程序骨架：Model 状态、构造、Init/View 与内部消息类型。

package tui

import (
	"context"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/konglong87/super-niuma/internal/pendinginput"
)

const (
	toolActivityStoreLimit         = 20
	todoCollapsedDefaultLimit      = 4
	transcriptBottomGuardMinSpacer = 1
	transcriptFlushFrameDelay      = 16 * time.Millisecond
	renderWidthSafetyMargin        = 4
	minRenderWidth                 = 24
	minViewportHeight              = 3
	maxBottomChromeHeight          = 12
)

type message struct {
	role    string
	content string
	meta    string
	tools   []toolActivityItem
	agents  []agentProgress
}

type responseMsg struct {
	prompt      string
	attachments []Attachment
	result      QueryResult
	err         error
}

type streamEventMsg struct {
	event StreamEvent
	ch    <-chan StreamEvent
}

type streamClosedMsg struct{}

type streamProducerDoneMsg struct{}

type transcriptFlushCommitMsg struct {
	output        string
	reason        transcriptFlushReason
	printedHeader bool
	printedCount  int
	printedSeq    int64
}

type transcriptFlushPrintedMsg struct {
	printedHeader bool
	printedCount  int
	printedSeq    int64
}

type tickMsg time.Time

type welcomeDirectFrameResetMsg struct{}

type welcomeMascotTickMsg struct{}

type spinnerTickMsg struct{}

type backgroundPollMsg struct {
	updates []BackgroundUpdate
	err     error
}

type awayRecapMsg struct {
	generation uint64
	text       string
	err        error
}

type nextStepsMsg struct {
	event StreamEvent
	err   error
}

type postTurnRecapMsg struct {
	generation uint64
	text       string
	err        error
}

type clipboardImageHintMsg struct {
	ok  bool
	err error
}

type Model struct {
	ctx                        context.Context
	turnCancel                 context.CancelFunc
	turnStopping               bool
	title                      string
	welcome                    WelcomeInfo
	run                        QueryFunc
	runResult                  QueryResultFunc
	runStream                  StreamFunc
	runWithAttachments         QueryWithAttachmentsFunc
	runStreamWithAttachments   StreamWithAttachmentsFunc
	slashCommands              SlashCommandProvider
	resumeSessions             ResumeSessionProvider
	rewindCandidates           RewindCandidateProvider
	importClipboard            ClipboardImageImporter
	detectClipboardImage       ClipboardImageDetector
	switchPermission           PermissionModeSwitcher
	runAwayRecap               AwayRecapFunc
	runNextSteps               NextStepsFunc
	runPostTurnRecap           AwayRecapFunc
	lastTurnPrompt             string
	awayRecapDelay             time.Duration
	watchBackground            BackgroundWatcher
	backgroundPollInterval     time.Duration
	backgroundSnapshots        map[string]BackgroundSnapshot
	textarea                   textarea.Model
	viewport                   viewport.Model
	messages                   []message
	displayTimeline            displayTimeline
	initialTranscriptPending   bool
	transcriptPrintedHeader    bool
	transcriptPrintedCount     int
	transcriptPrintedSeq       int64
	transcriptCommittingHeader bool
	transcriptCommittingSeq    int64
	recapSuppressedSeq         int64
	recapSuppressedCount       int
	attachments                []Attachment
	nextAttachmentID           int
	clipboardHasImage          bool
	stickToBottom              bool
	busy                       bool
	streamingActive            bool
	pendingStreamResult        QueryResult
	hasPendingStreamResult     bool
	err                        error
	turnStarted                time.Time
	lastActivity               time.Time
	awayRecapArmed             bool
	awayRecapArmedAt           time.Time
	awayRecapRunning           bool
	recapGeneration            uint64
	recapCancel                context.CancelFunc
	now                        func() time.Time
	lastStreamRenderAt         time.Time
	lastLiveRenderCost         time.Duration
	hasLiveViewContent         bool
	runningStatus              string
	width                      int
	height                     int
	windowSizeKnown            bool
	waitForInitialWindowSize   bool
	forceDirectWelcomeFrame    bool
	mascotTicking              bool
	spinnerTicking             bool
	spinnerFrame               int
	pendingPermission          *pendingPermission
	pendingQuestion            *pendingUserQuestion
	toolActivity               []toolActivityItem
	liveDisplayBlocks          []liveDisplayBlock
	recentAgentEvidence        string
	agentProgress              map[uint64]agentProgress
	agentOrder                 []uint64
	agentPanelExpanded         bool
	toolPanelExpanded          bool
	todos                      []todoItem
	todosLoadedFromDisk        bool
	todoPanelExpanded          bool
	todoCompletionArchiveKey   string
	usage                      usagePanel
	slashSuggestions           []SlashCommand
	slashSelected              int
	slashErr                   error
	nextSteps                  []string
	mouseTracking              bool
	resumePicker               []ResumeSession
	resumeSelected             int
	resumeOffset               int
	resumeLastClickIndex       int
	resumeLastClickAt          time.Time
	resumePickerEnabledMouse   bool
	rewindPicker               []RewindCandidate
	rewindSelected             int
	rewindOffset               int
	rewindLastClickIndex       int
	rewindLastClickAt          time.Time
	rewindMode                 string
	thinkingMode               thinkingMode
	thinkingModeExplicit       bool
	thinkingDetailTurn         int
	thinkingArchive            []displaySegment
	currentTurn                int
	draftStore                 DraftStore
	pendingInputQueue          pendinginput.Queue
	pendingInputSelected       int
	activePendingInputID       string

	transcriptPhaseCommittingSeq int64
	// phaseBoundaryStreamCh is held until the phase transcript print is acknowledged;
	// this preserves terminal order before the next stream event is consumed.
	phaseBoundaryStreamCh <-chan StreamEvent
}

var (
	tuiBodyColor             = lipgloss.Color("253")
	tuiMutedColor            = lipgloss.Color("240")
	tuiSecondaryColor        = lipgloss.Color("246")
	tuiAccentColor           = lipgloss.Color("42")
	tuiLinkPathColor         = lipgloss.Color("81")
	tuiInlineCodeColor       = lipgloss.Color("81")
	tuiInlineCodeBackground  = lipgloss.Color("236")
	tuiErrorColor            = lipgloss.Color("196")
	tuiUserBandBackground    = lipgloss.Color("235")
	titleStyle               = lipgloss.NewStyle().Bold(true).Foreground(tuiAccentColor)
	userStyle                = lipgloss.NewStyle().Foreground(tuiBodyColor).Background(tuiUserBandBackground)
	assistantMarkerStyle     = lipgloss.NewStyle().Foreground(tuiAccentColor)
	errorStyle               = lipgloss.NewStyle().Foreground(tuiErrorColor)
	statusStyle              = lipgloss.NewStyle().Foreground(tuiMutedColor)
	secondaryStyle           = lipgloss.NewStyle().Foreground(tuiSecondaryColor)
	welcomeBorderStyle       = lipgloss.NewStyle().Foreground(tuiAccentColor)
	welcomeMascotStyle       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("209"))
	welcomeMascotBusyStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("220"))
	welcomeMascotDangerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("203"))
	welcomePermissionStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	welcomeSafetyStyle       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("94"))
	recapLabelStyle          = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("245"))
	recapTextStyle           = lipgloss.NewStyle().Foreground(lipgloss.Color("248"))
	markdownListTitleStyle   = lipgloss.NewStyle().Foreground(tuiBodyColor)
	markdownListDetailStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("246"))
	toolOutputLabelStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	toolOutputBodyStyle      = lipgloss.NewStyle().Foreground(tuiBodyColor)
	toolOutputAddedStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("114"))
	toolOutputRemovedStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("204"))
	toolOutputHunkStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("81"))
	toolOutputTruncatedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	thinkingHeaderStyle      = lipgloss.NewStyle().Bold(true).Foreground(tuiSecondaryColor)
	thinkingRailStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	thinkingBodyStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	permissionSelectedStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("63")).Padding(0, 1)
	slashSelectedStyle       = lipgloss.NewStyle().Bold(true).Foreground(tuiLinkPathColor)
	assistantMarkdownStyle   = newAssistantMarkdownStyle()
	thinkingMarkdownStyle    = newThinkingMarkdownStyle()
	agentRunningStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("81"))
	agentCompletedStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	agentFailedStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	agentCancelledStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	bashExitPattern          = regexp.MustCompile(`(?i)\bexit(?:\s+status|\s+code)?\s+([0-9]+)\b`)
	todoSavedPattern         = regexp.MustCompile(`(?i)\bsaved\s+(\d+)\s+todos?\b`)
	todoResultSummaryPattern = regexp.MustCompile(`(?i)\bSummary:\s*(\d+)\s+total\b`)
)

func NewModel(ctx context.Context, opts Options) Model {
	if ctx == nil {
		ctx = context.Background()
	}
	title := opts.Title
	if title == "" {
		title = "niuma"
	}
	input := textarea.New()
	input.Placeholder = "Ask niuma"
	input.Prompt = "> "
	configureTextareaStyle(&input)
	// Bubble textarea shows a leading "1" by default; niuma-style input keeps it hidden.
	input.ShowLineNumbers = false
	input.SetHeight(3)
	input.Focus()
	vp := viewport.New(80, 18)
	model := Model{
		ctx:                      ctx,
		title:                    title,
		welcome:                  opts.Welcome,
		run:                      opts.Run,
		runResult:                opts.RunResult,
		runStream:                opts.RunStream,
		runWithAttachments:       opts.RunWithAttachments,
		runStreamWithAttachments: opts.RunStreamWithAttachments,
		slashCommands:            opts.SlashCommands,
		resumeSessions:           opts.ResumeSessions,
		rewindCandidates:         opts.RewindCandidates,
		importClipboard:          firstClipboardImporter(opts.ImportClipboard),
		detectClipboardImage:     firstClipboardDetector(opts.DetectClipboardImage),
		switchPermission:         opts.SwitchPermissionMode,
		runAwayRecap:             opts.RunAwayRecap,
		runNextSteps:             opts.RunNextSteps,
		runPostTurnRecap:         opts.RunPostTurnRecap,
		awayRecapDelay:           opts.AwayRecapDelay,
		watchBackground:          opts.WatchBackground,
		backgroundPollInterval:   opts.BackgroundPollInterval,
		backgroundSnapshots:      map[string]BackgroundSnapshot{},
		textarea:                 input,
		viewport:                 vp,
		width:                    80,
		height:                   24,
		windowSizeKnown:          !opts.WaitForInitialWindowSize,
		waitForInitialWindowSize: opts.WaitForInitialWindowSize,
		stickToBottom:            true,
		nextAttachmentID:         1,
		agentProgress:            map[uint64]agentProgress{},
		thinkingMode:             resolveThinkingMode(opts.Welcome),
		draftStore:               opts.DraftStore,
		pendingInputQueue:        opts.PendingInputQueue,
		now:                      time.Now,
	}
	if model.draftStore == nil {
		model.draftStore = defaultDraftStore()
	}
	if model.pendingInputQueue == nil {
		model.pendingInputQueue = defaultPendingInputQueue(opts.Welcome.SessionID, opts.Welcome.CWD)
	}
	model.lastActivity = model.now()
	model.loadDraft()
	model.loadTodosFromDisk()
	model.loadThinkingArchive(opts.InitialThinking)
	// Initial messages are UI-only status/history rows. Query context is loaded separately.
	for _, initial := range opts.InitialMessages {
		model.appendInitialDisplayMessage(initial)
	}
	model.initialTranscriptPending = len(model.messages) > 0
	model.refreshViewport()
	return model
}

func Run(ctx context.Context, input io.Reader, output io.Writer, opts Options) error {
	if opts.Run == nil && opts.RunResult == nil && opts.RunStream == nil && opts.RunWithAttachments == nil && opts.RunStreamWithAttachments == nil {
		return errors.New("tui query function is required")
	}
	opts.WaitForInitialWindowSize = true
	programOptions := []tea.ProgramOption{tea.WithInput(input), tea.WithOutput(output), tea.WithoutSignalHandler()}
	// Mouse tracking captures drag selection in many terminals, which breaks
	// native copy. Keep it opt-in so message/input text remains selectable.
	if opts.EnableMouseTracking {
		programOptions = append(programOptions, tea.WithMouseCellMotion())
	}
	program := tea.NewProgram(NewModel(ctx, opts), programOptions...)
	_, err := program.Run()
	return err
}

func (m *Model) applyWindowSize(width, height int) {
	m.width = width
	m.height = height
	m.windowSizeKnown = true
	budget := m.renderBudget()
	m.textarea.SetWidth(budget.InputWidth)
	m.resizeTextareaToContent()
	m.viewport.Width = budget.ContentWidth
	m.viewport.Height = m.normalViewportHeight()
	m.refreshViewport()
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, tickEverySecond(), m.checkClipboardImage(), m.pollBackground())
}

func (m Model) View() string {
	if m.waitForInitialWindowSize && !m.windowSizeKnown {
		return ""
	}
	status := m.viewStatus()
	parts := make([]string, 0, 6)
	hasLive := false
	if m.forceDirectWelcomeFrame && m.shouldShowIdleWelcomeLive() {
		if live := m.liveTranscriptView(); live != "" {
			parts = append(parts, live)
			hasLive = true
		}
	} else if m.busy && m.pendingPermission == nil {
		// Hot streaming path: Bubble Tea calls View() once per stream delta, and
		// liveTranscriptView() runs a full glamour markdown render. Re-running it
		// here just to test non-emptiness would defeat the refreshViewport render
		// throttle and make cost scale with delta count, so reuse the content the
		// last refreshViewport already rendered into the viewport. Every busy
		// mutation path (stream events, tick, turn start/end) calls
		// refreshViewport, which keeps hasLiveViewContent fresh.
		if m.viewport.Height > 0 && m.hasLiveViewContent {
			parts = append(parts, m.viewport.View())
			hasLive = true
		}
	} else if live := m.liveTranscriptView(); live != "" && m.viewport.Height > 0 {
		// Idle / permission paths keep the original per-frame decision: View()
		// frames are sparse here (only after user/tick events), so the extra
		// render is cheap and the display logic stays exactly as before.
		parts = append(parts, m.viewport.View())
		hasLive = true
	}
	parts = append(parts, m.bottomChromeParts(status, hasLive)...)
	return strings.Join(parts, "\n")
}

func (m Model) viewStatus() string {
	if m.busy {
		if m.turnStopping {
			return "Stopping..."
		}
		return m.currentRunningStatus()
	}
	if m.err != nil {
		return "Last error: " + friendlyStreamErrorStatus(m.err)
	}
	return "Ready"
}
