package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/agenttasks/memstore"
	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/background"
	"github.com/konglong87/super-niuma/internal/compact"
	"github.com/konglong87/super-niuma/internal/config"
	goalpkg "github.com/konglong87/super-niuma/internal/goal"
	"github.com/konglong87/super-niuma/internal/goalcmd"
	"github.com/konglong87/super-niuma/internal/identity"
	imagegensvc "github.com/konglong87/super-niuma/internal/imagegen"
	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/permissions"
	"github.com/konglong87/super-niuma/internal/query"
	"github.com/konglong87/super-niuma/internal/sandbox"
	"github.com/konglong87/super-niuma/internal/scheduler"
	"github.com/konglong87/super-niuma/internal/session"
	"github.com/konglong87/super-niuma/internal/slashcommands"
	"github.com/konglong87/super-niuma/internal/tools"
	agenttool "github.com/konglong87/super-niuma/internal/tools/agent"
	"github.com/konglong87/super-niuma/internal/tools/askuserquestion"
	"github.com/konglong87/super-niuma/internal/tools/bash"
	"github.com/konglong87/super-niuma/internal/tools/bashoutput"
	computerusetool "github.com/konglong87/super-niuma/internal/tools/computeruse"
	"github.com/konglong87/super-niuma/internal/tools/fileedit"
	"github.com/konglong87/super-niuma/internal/tools/fileread"
	"github.com/konglong87/super-niuma/internal/tools/filewrite"
	"github.com/konglong87/super-niuma/internal/tools/glob"
	"github.com/konglong87/super-niuma/internal/tools/grep"
	imagegentool "github.com/konglong87/super-niuma/internal/tools/imagegen"
	"github.com/konglong87/super-niuma/internal/tools/ls"
	"github.com/konglong87/super-niuma/internal/tools/lsp"
	"github.com/konglong87/super-niuma/internal/tools/mcpresources"
	"github.com/konglong87/super-niuma/internal/tools/notebook"
	"github.com/konglong87/super-niuma/internal/tools/planmode"
	"github.com/konglong87/super-niuma/internal/tools/powershell"
	sessioncontroltool "github.com/konglong87/super-niuma/internal/tools/sessioncontrol"
	"github.com/konglong87/super-niuma/internal/tools/skill"
	"github.com/konglong87/super-niuma/internal/tools/taskoutput"
	"github.com/konglong87/super-niuma/internal/tools/todowrite"
	"github.com/konglong87/super-niuma/internal/tools/webbrowser"
	"github.com/konglong87/super-niuma/internal/tools/webfetch"
	"github.com/konglong87/super-niuma/internal/tools/websearch"
	"github.com/konglong87/super-niuma/internal/tools/workflow"
	"github.com/konglong87/super-niuma/internal/tools/worktree"
	"github.com/konglong87/super-niuma/internal/tui"
)

func runInteractive(ctx context.Context, opts options, stdout, stderr io.Writer) error {
	restoreLogger := configureTUILogger()
	defer restoreLogger()

	store := session.DefaultStore()
	if !opts.sessionOptionsResolved {
		if err := resolveSessionOptions(store, &opts); err != nil {
			return err
		}
		opts.sessionOptionsResolved = true
	}
	recorder, err := newRecorderForOptions(store, opts)
	if err != nil {
		return err
	}
	if recorder != nil {
		defer func() {
			_ = recorder.Close()
		}()
	}
	resumeHistoryLimit := tuiResumeHistoryLimit(opts.cwd)
	initialTUI, err := tuiResumeInitialMessages(store, opts, resumeHistoryLimit)
	if err != nil {
		return err
	}
	initialThinking, err := tuiResumeThinkingDetails(store, opts.resume, opts.resumeSessionAt)
	if err != nil {
		return err
	}
	permissionMode := newInteractivePermissionMode(opts)
	currentWelcome := tuiWelcomeInfoWithPermissionMode(opts, permissionMode.current())
	currentWelcome = withTUISessionID(currentWelcome, recorder)
	currentOpts := opts
	enrichment := tuiEnrichmentForOptions(currentOpts, recorder)
	activeResumeID := ""
	activeResumeEntryOffset := 0
	if strings.TrimSpace(opts.resume) != "" {
		_, _, err := loadResumeContext(store, opts.resume, opts.resumeSessionAt)
		if err != nil {
			return err
		}
		activeResumeID = strings.TrimSpace(opts.resume)
		activeResumeEntryOffset, err = transcriptEntryCount(recorder)
		if err != nil {
			return err
		}
	}

	return tui.Run(ctx, os.Stdin, stdout, tui.Options{
		Title:         "niuma",
		Welcome:       currentWelcome,
		SlashCommands: tuiSlashCommandProviderWithOptions(opts),
		ResumeSessions: tuiResumeSessionProvider(store, opts.cwd, func() []string {
			return []string{recorderSessionID(recorder), activeResumeID}
		}),
		RewindCandidates:     tuiRewindCandidateProvider(&currentOpts, recorder),
		SwitchPermissionMode: permissionMode.switchNext,
		// currentOpts 在这里是按值捕获，这是刻意的：这几个 runner 都由 bubbletea
		// 在一个 detached、不等待的 goroutine 里跑，跟当前这一轮的 in-place 修改
		// 并发。换成 &currentOpts 指针捕获看着能顺带解决「换模型不生效」的问题，
		// 但会变成一个货真价实的 -race：已经有人在核实 bubbletea 源码之前提过这个
		// 「修法」。换模型的新鲜度改由 next-steps 侧优先用 result.Model 解决（见
		// tuiNextStepsRunner），不靠这里的捕获方式。
		RunAwayRecap:           enrichment.runAwayRecap,
		RunNextSteps:           enrichment.runNextSteps,
		RunPostTurnRecap:       enrichment.runPostTurnRecap,
		AwayRecapDelay:         enrichment.awayRecapDelay,
		WatchBackground:        enrichment.watchBackground,
		BackgroundPollInterval: 5 * time.Second,
		InitialMessages:        initialTUI,
		InitialThinking:        initialThinking,
		RunStreamWithAttachments: func(runCtx context.Context, prompt string, attachments []tui.Attachment, events chan<- tui.StreamEvent) error {
			if recapHandled, exit, _, recapErr := handleTUIRecapSlash(runCtx, currentOpts, prompt, recorder, events); recapHandled {
				if exit {
					return nil
				}
				return recapErr
			}
			var slashOut, slashErr bytes.Buffer
			currentOpts.resumeContinuationEntryOffset = activeResumeEntryOffset
			if handled, exit, err := handleInteractiveSlash(runCtx, currentOpts, prompt, &slashOut, &slashErr, recorder); handled {
				if isUnknownSlashCommand(err) {
					dynamicPrompt, ok, dynamicErr := dynamicSlashPromptWithOptions(currentOpts, prompt)
					if dynamicErr != nil {
						return dynamicErr
					}
					if ok {
						prompt = dynamicPrompt
					} else {
						return err
					}
				} else {
					text := strings.TrimSpace(strings.Join([]string{slashOut.String(), slashErr.String()}, "\n"))
					if text != "" {
						events <- tui.StreamEvent{Type: tui.StreamText, Text: text}
					}
					if err == nil {
						if resumeID, ok := interactiveResumeSelection(prompt); ok && !isActiveResumeSession(recorder, activeResumeID, resumeID) {
							_, _, loadErr := loadResumeContext(store, resumeID, "")
							if loadErr != nil {
								return loadErr
							}
							activeResumeID = resumeID
							activeResumeEntryOffset, loadErr = transcriptEntryCount(recorder)
							if loadErr != nil {
								return loadErr
							}
							currentOpts.resume = resumeID
							currentOpts.resumeSessionAt = ""
							currentWelcome.Resume = resumeID
							currentWelcome.SessionStatus = "resumed " + resumeID
							history, historyErr := tuiResumeHistoryMessages(store, resumeID, "", resumeHistoryLimit)
							if historyErr != nil {
								return historyErr
							}
							thinking, thinkingErr := tuiResumeThinkingDetails(store, resumeID, "")
							if thinkingErr != nil {
								return thinkingErr
							}
							events <- tui.StreamEvent{Type: tui.StreamSessionResume, Text: text, Welcome: &currentWelcome, History: history, Thinking: thinking}
							if latest := latestRecapForResume(store, resumeID); latest != "" {
								events <- tui.StreamEvent{Type: tui.StreamRecap, Text: latest}
							}
						} else if status := sessionStatusFromInteractiveSlash(prompt, text); status != "" {
							if interactiveCompactCommand(prompt) && activeResumeID != "" {
								activeResumeID = ""
								activeResumeEntryOffset = 0
								currentOpts.resume = ""
								currentOpts.resumeSessionAt = ""
								currentWelcome.Resume = ""
							}
							currentWelcome.SessionStatus = status
							events <- tui.StreamEvent{Type: tui.StreamConfigReload, Welcome: &currentWelcome}
						} else if interactiveGoalCommand(prompt) {
							nextWelcome := tuiWelcomeInfoWithPermissionMode(currentOpts, permissionMode.current())
							nextWelcome.SessionID = currentWelcome.SessionID
							if tuiWelcomeChanged(currentWelcome, nextWelcome) {
								currentWelcome = nextWelcome
								events <- tui.StreamEvent{Type: tui.StreamConfigReload, Text: "Goal status updated.", Welcome: &currentWelcome}
							}
						}
					}
					if exit {
						return nil
					}
					return err
				}
			}
			if strings.TrimSpace(prompt) == "" {
				return nil
			}
			var initial []anthropic.MessageParam
			var initialReplacements []query.ToolResultReplacementRecord
			continuationOffset := 0
			if activeResumeID != "" {
				continuationOffset = activeResumeEntryOffset
			}
			continuation, continuationReplacements, continuationErr := resumeContinuationContext(recorder, continuationOffset)
			if continuationErr != nil {
				return continuationErr
			}
			initial = append(initial, continuation...)
			initialReplacements = append(initialReplacements, continuationReplacements...)
			streamOpts := currentOpts
			streamOpts.initialToolResultReplacements = initialReplacements
			applyInteractivePermissionMode(&streamOpts, permissionMode.current())
			reloadedOpts, reloadedWelcome, changed := reloadInteractiveOptions(streamOpts, permissionMode.current(), currentWelcome)
			if changed {
				events <- tui.StreamEvent{Type: tui.StreamConfigReload, Text: tuiConfigReloadText(currentWelcome, reloadedWelcome), Welcome: &reloadedWelcome, NoticeKind: tui.StreamNoticeConfigReload}
				currentWelcome = reloadedWelcome
			}
			streamOpts = reloadedOpts
			streamOpts.permissionPrompt = tuiPermissionPrompt(events)
			streamOpts.userQuestionPrompt = tuiUserQuestionPrompt(events)
			streamOpts.runtimePermissionMode = permissionMode.current
			streamOpts.nestedAgentProgress = tuiNestedAgentProgress(events)
			streamOpts.queryAttachments = tuiQueryAttachments(attachments)
			querySession, cleanup, err := newQuerySession(runCtx, streamOpts, initial, recorder)
			if err != nil {
				return err
			}
			defer cleanup()
			result, err := querySession.RunWithCallbacks(runCtx, prompt, tuiEventWriter{events: events}, query.SinkCallbacks(runCtx, tuiEventSink{events: events}))
			if err == nil {
				tuiResult := tuiQueryResult(result, streamOpts, len(querySession.ToolDefinitions()))
				events <- tui.StreamEvent{Type: tui.StreamUsage, Result: &tuiResult}
				// 回顾现在由 TUI 的 cmd（RunPostTurnRecap）在 StreamFinished 后驱动，
				// 它自己持有投递用的 channel，这里不再调用。
			}
			return err
		},
		RunWithAttachments: func(runCtx context.Context, prompt string, attachments []tui.Attachment) (tui.QueryResult, error) {
			if recapHandled, exit, text, recapErr := handleTUIRecapSlash(runCtx, currentOpts, prompt, recorder, nil); recapHandled {
				if exit {
					return tui.QueryResult{Context: tuiRuntimeContext(currentOpts, 0)}, nil
				}
				return tui.QueryResult{Response: text, Context: tuiRuntimeContext(currentOpts, 0)}, recapErr
			}
			var slashOut, slashErr bytes.Buffer
			currentOpts.resumeContinuationEntryOffset = activeResumeEntryOffset
			if handled, exit, err := handleInteractiveSlash(runCtx, currentOpts, prompt, &slashOut, &slashErr, recorder); handled {
				if isUnknownSlashCommand(err) {
					dynamicPrompt, ok, dynamicErr := dynamicSlashPromptWithOptions(currentOpts, prompt)
					if dynamicErr != nil {
						return tui.QueryResult{}, dynamicErr
					}
					if ok {
						prompt = dynamicPrompt
					} else {
						return tui.QueryResult{}, err
					}
				} else {
					text := strings.TrimSpace(strings.Join([]string{slashOut.String(), slashErr.String()}, "\n"))
					if err == nil {
						if resumeID, ok := interactiveResumeSelection(prompt); ok && !isActiveResumeSession(recorder, activeResumeID, resumeID) {
							_, _, loadErr := loadResumeContext(store, resumeID, "")
							if loadErr != nil {
								return tui.QueryResult{}, loadErr
							}
							activeResumeID = resumeID
							activeResumeEntryOffset, loadErr = transcriptEntryCount(recorder)
							if loadErr != nil {
								return tui.QueryResult{}, loadErr
							}
							currentOpts.resume = resumeID
							currentOpts.resumeSessionAt = ""
							currentWelcome.Resume = resumeID
							currentWelcome.SessionStatus = "resumed " + resumeID
						} else if status := sessionStatusFromInteractiveSlash(prompt, text); status != "" {
							if interactiveCompactCommand(prompt) && activeResumeID != "" {
								activeResumeID = ""
								activeResumeEntryOffset = 0
								currentOpts.resume = ""
								currentOpts.resumeSessionAt = ""
								currentWelcome.Resume = ""
							}
							currentWelcome.SessionStatus = status
						} else if interactiveGoalCommand(prompt) {
							nextWelcome := tuiWelcomeInfoWithPermissionMode(currentOpts, permissionMode.current())
							nextWelcome.SessionID = currentWelcome.SessionID
							if tuiWelcomeChanged(currentWelcome, nextWelcome) {
								currentWelcome = nextWelcome
							}
						}
					}
					if exit {
						return tui.QueryResult{Response: text, Context: tuiRuntimeContext(currentOpts, 0)}, nil
					}
					return tui.QueryResult{Response: text, Context: tuiRuntimeContext(currentOpts, 0)}, err
				}
			}
			if strings.TrimSpace(prompt) == "" {
				return tui.QueryResult{}, nil
			}
			var initial []anthropic.MessageParam
			var initialReplacements []query.ToolResultReplacementRecord
			continuationOffset := 0
			if activeResumeID != "" {
				continuationOffset = activeResumeEntryOffset
			}
			continuation, continuationReplacements, continuationErr := resumeContinuationContext(recorder, continuationOffset)
			if continuationErr != nil {
				return tui.QueryResult{}, continuationErr
			}
			initial = append(initial, continuation...)
			initialReplacements = append(initialReplacements, continuationReplacements...)
			runOpts := currentOpts
			runOpts.initialToolResultReplacements = initialReplacements
			applyInteractivePermissionMode(&runOpts, permissionMode.current())
			reloadedOpts, reloadedWelcome, changed := reloadInteractiveOptions(runOpts, permissionMode.current(), currentWelcome)
			if changed {
				currentWelcome = reloadedWelcome
			}
			runOpts = reloadedOpts
			runOpts.runtimePermissionMode = permissionMode.current
			runOpts.queryAttachments = tuiQueryAttachments(attachments)
			querySession, cleanup, err := newQuerySession(runCtx, runOpts, initial, recorder)
			if err != nil {
				return tui.QueryResult{}, err
			}
			defer cleanup()
			result, err := querySession.Run(runCtx, prompt, io.Discard)
			if err != nil {
				return tui.QueryResult{}, err
			}
			// 这条路径没有 UI channel，回顾只用于持久化到会话文件。原实现是
			// fire-and-forget（内部 60s 超时、与调用方 context 解耦），同步调用会让
			// 本来不阻塞的非流式路径多等最多 60s；用一个不投递 UI 的 goroutine
			// 包裹以保留原有的非阻塞行为——这里没有 channel 归属问题。
			if !runOpts.runtimeProfile.IsBare() {
				go func() {
					recapCtx, cancel := context.WithTimeout(context.Background(), postTurnRecapTimeout)
					defer cancel()
					// 回顾失败不影响本轮结果；telemetry 已在内部记录。
					_, _ = runPostTurnRecap(recapCtx, runOpts, recorder)
				}()
			}
			return tuiQueryResult(result, runOpts, len(querySession.ToolDefinitions())), nil
		},
	})
}

type tuiEnrichment struct {
	runAwayRecap     tui.AwayRecapFunc
	runNextSteps     tui.NextStepsFunc
	runPostTurnRecap tui.AwayRecapFunc
	awayRecapDelay   time.Duration
	watchBackground  tui.BackgroundWatcher
}

func tuiEnrichmentForOptions(opts options, recorder *session.Recorder) tuiEnrichment {
	if opts.runtimeProfile.IsBare() {
		return tuiEnrichment{}
	}
	return tuiEnrichment{
		runAwayRecap:     tuiAwayRecapRunner(opts, recorder),
		runNextSteps:     tuiNextStepsRunner(opts),
		runPostTurnRecap: tuiPostTurnRecapRunner(opts, recorder),
		awayRecapDelay:   tuiAwayRecapDelay(opts),
		watchBackground:  tuiBackgroundWatcher(opts.cwd),
	}
}

func tuiWelcomeInfo(opts options) tui.WelcomeInfo {
	return tuiWelcomeInfoWithPermissionMode(opts, "")
}

func withTUISessionID(info tui.WelcomeInfo, recorder *session.Recorder) tui.WelcomeInfo {
	if recorder != nil {
		info.SessionID = strings.TrimSpace(recorder.SessionID)
	}
	return info
}

func tuiBackgroundWatcher(cwd string) tui.BackgroundWatcher {
	return func(ctx context.Context, previous map[string]tui.BackgroundSnapshot) ([]tui.BackgroundUpdate, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		scheduleStore := scheduler.DefaultStore()
		eventSnapshot := previous["__scheduler_events__"]
		if _, seen := previous["__scheduler_events__"]; !seen {
			endOffset, err := scheduleStore.EventsEndOffset()
			if err != nil {
				return nil, err
			}
			eventSnapshot.EventOffset = endOffset
		}
		events, nextOffset, err := scheduleStore.ReadEventsAfter(eventSnapshot.EventOffset, 50)
		if err != nil {
			return nil, err
		}
		updates := []tui.BackgroundUpdate{}
		if _, seen := previous["__scheduler_events__"]; !seen {
			updates = append(updates, tui.BackgroundUpdate{
				ID:      "__scheduler_events__",
				LogSize: int(eventSnapshot.EventOffset),
				Silent:  true,
			})
		}
		for _, event := range events {
			if event.Type != "run_started" && event.Type != "run_finished" {
				continue
			}
			if !sameCleanPath(event.CWD, cwd) {
				continue
			}
			logs := ""
			if event.BackgroundID != "" {
				logs, _, _ = background.DefaultStore().Logs(event.BackgroundID)
			}
			updates = append(updates, tui.BackgroundUpdate{
				ID:         event.BackgroundID,
				ScheduleID: event.ScheduleID,
				Kind:       "loop",
				Prompt:     event.Prompt,
				Status:     event.Status,
				RunCount:   event.RunCount,
				LastRunAt:  &event.CreatedAt,
				LogSize:    len(logs),
				LogTail:    tailString(logs, 1800),
				Silent:     event.Type == "run_started",
			})
		}
		if nextOffset > eventSnapshot.EventOffset || len(events) > 0 {
			updates = append(updates, tui.BackgroundUpdate{
				ID:      "__scheduler_events__",
				LogSize: int(nextOffset),
				Silent:  true,
			})
		}
		jobs, err := background.DefaultStore().List()
		if err != nil {
			return nil, err
		}
		schedules, err := scheduleStore.List()
		if err != nil {
			return nil, err
		}
		scheduleByBackground := map[string]scheduler.Schedule{}
		for _, item := range schedules {
			if item.BackgroundID != "" {
				scheduleByBackground[item.BackgroundID] = item
			}
		}
		for _, job := range jobs {
			if (job.Kind != "loop" && job.Kind != "bash") || !sameCleanPath(job.CWD, cwd) {
				continue
			}
			logs, _, err := background.DefaultStore().Logs(job.ID)
			if err != nil {
				return nil, err
			}
			snapshot, seen := previous[job.ID]
			logSize := len(logs)
			changed := !seen || job.RunCount > snapshot.RunCount || logSize > snapshot.LogSize || job.Status != snapshot.Status
			if !changed {
				continue
			}
			schedule := scheduleByBackground[job.ID]
			updates = append(updates, tui.BackgroundUpdate{
				ID:         job.ID,
				ScheduleID: schedule.ID,
				Kind:       job.Kind,
				Prompt:     firstNonEmptyString(job.Prompt, schedule.Prompt),
				Status:     job.Status,
				RunCount:   job.RunCount,
				LastRunAt:  job.LastRunAt,
				LogSize:    logSize,
				LogTail:    tailString(logs, 1800),
				Silent:     !seen,
			})
		}
		return updates, nil
	}
}

func sameCleanPath(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return strings.TrimSpace(a) == strings.TrimSpace(b)
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func tailString(text string, maxBytes int) string {
	if maxBytes <= 0 || len(text) <= maxBytes {
		return text
	}
	start := len(text) - maxBytes
	if idx := strings.IndexByte(text[start:], '\n'); idx >= 0 {
		start += idx + 1
	}
	return text[start:]
}

func tuiWelcomeInfoWithPermissionMode(opts options, modeOverride string) tui.WelcomeInfo {
	cfg, resolveErr := resolveRuntimeProviderConfig(&opts)
	goalContext := tuiGoalContext(context.Background(), goalpkg.DefaultStore())
	if resolveErr != nil {
		cfg = config.LoadForCWD(opts.cwd)
		showThinking := configuredTUIShowThinking(cfg.Settings)
		thinkingMode := config.ResolveTUIThinkingMode(cfg.Settings)
		mode := strings.TrimSpace(opts.permissionMode)
		if mode == "" {
			mode = "ask"
		}
		if modeOverride != "" {
			mode = modeOverride
		}
		return tui.WelcomeInfo{
			Version:        versionForDisplay(),
			Model:          config.ResolveModel(opts.cwd, opts.model),
			PromptMode:     opts.promptMode,
			ContextLength:  cfg.Settings.ContextLength,
			CWD:            opts.cwd,
			PermissionMode: mode,
			Sandbox:        "unknown",
			ToolSummary:    "enabled",
			ShowThinking:   &showThinking,
			ThinkingMode:   thinkingMode,
			Goal:           goalContext.Label,
			GoalStep:       goalContext.Step,
			GoalCriteria:   goalContext.Criteria,
			GoalEvidence:   goalContext.Evidence,
			GoalNextAction: goalContext.NextAction,
		}
	}
	showThinking := configuredTUIShowThinking(cfg.Settings)
	thinkingMode := config.ResolveTUIThinkingMode(cfg.Settings)
	cfg.Settings.MCPServers = mergedMCPServers(opts.cwd, cfg.Settings.MCPServers)
	permissionMode := cfg.Settings.Permissions.DefaultMode
	if opts.skipPermissions {
		permissionMode = "bypass"
	}
	if modeOverride != "" && !opts.skipPermissions {
		permissionMode = modeOverride
	}
	if permissionMode == "" {
		permissionMode = "ask"
	}
	toolSummary := estimatedTUIToolSummary(opts, cfg.Settings)
	if len(cfg.Settings.MCPServers) > 0 {
		toolSummary += "+mcp"
	}
	return tui.WelcomeInfo{
		Version:         versionForDisplay(),
		Model:           config.ResolveModel(opts.cwd, opts.model),
		Provider:        tuiProviderLabel(cfg),
		PromptMode:      opts.promptMode,
		ContextLength:   cfg.Settings.ContextLength,
		CWD:             opts.cwd,
		PermissionMode:  permissionMode,
		Sandbox:         tuiSandboxLabel(cfg.Settings.Sandbox),
		SandboxWarnings: tuiSandboxWarnings(cfg.Settings.Sandbox),
		ToolSummary:     toolSummary,
		MCPServers:      len(cfg.Settings.MCPServers),
		Resume:          tuiResumeLabel(opts),
		ShowThinking:    &showThinking,
		ThinkingMode:    thinkingMode,
		Goal:            goalContext.Label,
		GoalStep:        goalContext.Step,
		GoalCriteria:    goalContext.Criteria,
		GoalEvidence:    goalContext.Evidence,
		GoalNextAction:  goalContext.NextAction,
	}
}

func configuredTUIShowThinking(settings config.Settings) bool {
	return config.TUIShowThinking(settings)
}

func reloadInteractiveOptions(opts options, modeOverride string, previous tui.WelcomeInfo) (options, tui.WelcomeInfo, bool) {
	reloaded := opts
	if !reloaded.modelExplicit {
		reloaded.model = config.ResolveModel(reloaded.cwd, "")
	}
	// Resolve runtime --settings and the selected provider after refreshing the
	// base model. The selected provider's model is the effective model shown by
	// the TUI and used by newQuerySession.
	_, _ = resolveRuntimeProviderConfig(&reloaded)
	welcome := tuiWelcomeInfoWithPermissionMode(reloaded, modeOverride)
	welcome.SessionID = previous.SessionID
	welcome.SessionStatus = previous.SessionStatus
	return reloaded, welcome, tuiWelcomeChanged(previous, welcome)
}

func tuiWelcomeChanged(old, next tui.WelcomeInfo) bool {
	return strings.TrimSpace(old.Model) != strings.TrimSpace(next.Model) ||
		strings.TrimSpace(old.Provider) != strings.TrimSpace(next.Provider) ||
		strings.TrimSpace(old.PromptMode) != strings.TrimSpace(next.PromptMode) ||
		old.ContextLength != next.ContextLength ||
		strings.TrimSpace(old.SessionID) != strings.TrimSpace(next.SessionID) ||
		strings.TrimSpace(old.PermissionMode) != strings.TrimSpace(next.PermissionMode) ||
		strings.TrimSpace(old.Sandbox) != strings.TrimSpace(next.Sandbox) ||
		strings.TrimSpace(old.ToolSummary) != strings.TrimSpace(next.ToolSummary) ||
		old.MCPServers != next.MCPServers ||
		strings.TrimSpace(old.SessionStatus) != strings.TrimSpace(next.SessionStatus) ||
		strings.TrimSpace(old.Goal) != strings.TrimSpace(next.Goal) ||
		strings.TrimSpace(old.GoalStep) != strings.TrimSpace(next.GoalStep) ||
		strings.TrimSpace(old.GoalCriteria) != strings.TrimSpace(next.GoalCriteria) ||
		strings.TrimSpace(old.GoalEvidence) != strings.TrimSpace(next.GoalEvidence) ||
		strings.TrimSpace(old.GoalNextAction) != strings.TrimSpace(next.GoalNextAction) ||
		strings.TrimSpace(old.ThinkingMode) != strings.TrimSpace(next.ThinkingMode) ||
		resolvedShowThinking(old.ShowThinking) != resolvedShowThinking(next.ShowThinking)
}

func resolvedShowThinking(value *bool) bool {
	return value == nil || *value
}

func tuiConfigReloadText(old, next tui.WelcomeInfo) string {
	changes := []string{}
	if old.Model != "" && next.Model != "" && old.Model != next.Model {
		changes = append(changes, "model "+old.Model+" → "+next.Model)
	}
	if old.Provider != "" && next.Provider != "" && old.Provider != next.Provider {
		changes = append(changes, "provider "+old.Provider+" → "+next.Provider)
	}
	if old.PromptMode != "" && next.PromptMode != "" && old.PromptMode != next.PromptMode {
		changes = append(changes, "mode "+old.PromptMode+" → "+next.PromptMode)
	}
	if old.ContextLength != 0 && next.ContextLength != 0 && old.ContextLength != next.ContextLength {
		changes = append(changes, fmt.Sprintf("context %s → %s", formatTUITokens(old.ContextLength), formatTUITokens(next.ContextLength)))
	}
	if old.PermissionMode != "" && next.PermissionMode != "" && old.PermissionMode != next.PermissionMode {
		changes = append(changes, "permissions "+old.PermissionMode+" → "+next.PermissionMode)
	}
	if old.Sandbox != "" && next.Sandbox != "" && old.Sandbox != next.Sandbox {
		changes = append(changes, "sandbox "+old.Sandbox+" → "+next.Sandbox)
	}
	if len(changes) == 0 {
		return ""
	}
	return strings.Join(changes, " · ")
}

func tuiQueryAttachments(attachments []tui.Attachment) []query.Attachment {
	if len(attachments) == 0 {
		return nil
	}
	out := make([]query.Attachment, 0, len(attachments))
	for _, attachment := range attachments {
		out = append(out, query.Attachment{
			ID:        strconv.Itoa(attachment.ID),
			Type:      attachment.Type,
			MediaType: attachment.MediaType,
			Name:      attachment.Name,
			Path:      attachment.Path,
			URL:       attachment.URL,
			SizeBytes: attachment.SizeBytes,
		})
	}
	return out
}

func formatTUITokens(tokens int) string {
	if tokens >= 1000 && tokens%1000 == 0 {
		return strconv.Itoa(tokens/1000) + "k"
	}
	if tokens >= 1000 {
		return fmt.Sprintf("%.1fk", float64(tokens)/1000)
	}
	return strconv.Itoa(tokens)
}

type interactivePermissionMode struct {
	mu   sync.Mutex
	mode string
}

func newInteractivePermissionMode(opts options) *interactivePermissionMode {
	mode := strings.TrimSpace(tuiWelcomeInfo(opts).PermissionMode)
	if opts.skipPermissions {
		mode = "bypass"
	}
	if mode == "" {
		mode = "ask"
	}
	return &interactivePermissionMode{mode: mode}
}

func ensureLocalAgentTaskRuntime(opts *options) {
	if opts == nil {
		return
	}
	if opts.agentTaskStore == nil {
		opts.agentTaskStore = memstore.New()
	}
	if opts.agentTaskController == nil {
		opts.agentTaskController = agenttasks.NewController()
	}
}

func (m *interactivePermissionMode) current() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mode
}

func (m *interactivePermissionMode) switchNext(current string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if strings.EqualFold(m.mode, "bypass") {
		return m.mode, nil
	}
	if normalized, ok := permissions.NormalizeMode(current); ok && normalized != "" && !strings.EqualFold(normalized, "allow") {
		m.mode = normalized
	}
	switch strings.ToLower(strings.TrimSpace(m.mode)) {
	case "ask":
		m.mode = "allow"
	case "allow":
		m.mode = "deny"
	case "deny":
		m.mode = "ask"
	default:
		m.mode = "ask"
	}
	return m.mode, nil
}

func applyInteractivePermissionMode(opts *options, mode string) {
	if opts == nil || opts.skipPermissions {
		return
	}
	opts.permissionMode = strings.TrimSpace(mode)
	opts.permissionBypass = strings.EqualFold(opts.permissionMode, "allow")
}

func tuiProviderLabel(cfg config.Config) string {
	provider := strings.TrimSpace(cfg.Provider)
	if provider == "" {
		provider = strings.TrimSpace(cfg.Settings.Provider)
	}
	if provider == "" {
		provider = "anthropic-compatible"
	}
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if strings.EqualFold(provider, "custom") {
		return "custom/openai-compatible"
	}
	if baseURL != "" && !strings.Contains(baseURL, "anthropic.com") && strings.Contains(baseURL, "/v1") {
		return provider + "/openai-compatible"
	}
	return provider
}

// tuiSandboxLabel reports what the sandbox actually enforces on this host, not
// what settings asked for. Reading settings alone (AUDIT-P1-35) printed
// "on/network" on machines where no shell command was sandboxed at all.
func tuiSandboxLabel(settings *config.SandboxSettings) string {
	return sandbox.Describe(sandboxConfigFromSettings(settings)).Label
}

// tuiSandboxWarnings is empty unless something the user configured is not being
// enforced, so a healthy install shows nothing extra.
func tuiSandboxWarnings(settings *config.SandboxSettings) []string {
	return sandbox.Describe(sandboxConfigFromSettings(settings)).Warnings
}

func tuiResumeLabel(opts options) string {
	if opts.resumeSessionAt != "" {
		return "session-at " + opts.resumeSessionAt
	}
	if opts.resume != "" {
		return opts.resume
	}
	if opts.forkSession {
		return "fork"
	}
	return ""
}

type tuiGoalStatusContext struct {
	Label      string
	Step       string
	Criteria   string
	Evidence   string
	NextAction string
}

func tuiGoalContext(ctx context.Context, store goalpkg.Store) tuiGoalStatusContext {
	if store == nil {
		return tuiGoalStatusContext{}
	}
	goals, err := store.List(ctx, goalpkg.ListFilter{Active: true})
	if err != nil || len(goals) == 0 {
		return tuiGoalStatusContext{}
	}
	goal := goals[0]
	label := strings.TrimPrefix(goal.ID, "goal_")
	if len(label) > 8 {
		label = label[:8]
	}
	tokens := goal.InputTokens + goal.OutputTokens
	parts := []string{
		fmt.Sprintf("%s %s", label, goal.Status),
	}
	parts = append(parts,
		fmt.Sprintf("turns %d/%d", goal.TurnsUsed, goal.TurnBudget),
		fmt.Sprintf("tokens %d/%d", tokens, goal.TokenBudget),
	)
	out := tuiGoalStatusContext{
		Label:      strings.Join(parts, " "),
		NextAction: shortText(goal.LastNextAction, 64),
	}
	planStore, ok := store.(goalpkg.PlanStore)
	if !ok {
		return out
	}
	if plan, ok, err := planStore.GetPlan(ctx, goal.ID); err == nil && ok {
		if current, ok := cliCurrentPlanStep(plan); ok {
			out.Step = shortText(current.Title, 48)
		}
		if len(plan.AcceptanceCriteria) > 0 {
			passed, requiredDone, requiredTotal := cliCriterionProgress(plan.AcceptanceCriteria)
			criteria := fmt.Sprintf("%d/%d passed", passed, len(plan.AcceptanceCriteria))
			if requiredTotal > 0 {
				criteria += fmt.Sprintf(" req %d/%d", requiredDone, requiredTotal)
			}
			out.Criteria = criteria
		}
	}
	if evidence, err := planStore.ListEvidence(ctx, goal.ID, 1); err == nil && len(evidence) > 0 {
		latest := evidence[len(evidence)-1]
		result := "fail"
		if latest.Passed {
			result = "pass"
		}
		source := goalEvidenceSourceLabel(latest)
		if source != "" {
			source = " source " + source
		}
		out.Evidence = shortText(fmt.Sprintf("%s %s%s %s", latest.Type, result, source, latest.Summary), 180)
	}
	return out
}

func goalEvidenceSourceLabel(evidence goalpkg.GoalEvidence) string {
	if len(evidence.Payload) == 0 {
		return ""
	}
	var payload struct {
		EvidenceSource string `json:"evidence_source"`
	}
	if err := json.Unmarshal(evidence.Payload, &payload); err != nil {
		return ""
	}
	switch strings.TrimSpace(payload.EvidenceSource) {
	case "terminal_agent_task_store":
		return "task_store"
	case "agent_get":
		return "agent_get"
	default:
		return shortText(strings.TrimSpace(payload.EvidenceSource), 32)
	}
}

func estimatedTUIToolSummary(opts options, settings config.Settings) string {
	toolList := coreRuntimeTools(settings, nil, "")
	if opts.runtimeProfile.IsBare() {
		toolList = bareRuntimeTools()
	}
	count := len(filterRuntimeTools(toolList, opts))
	if !opts.runtimeProfile.IsBare() && (!opts.toolsSpecified || containsString(opts.enabledTools, "Task")) {
		count++
	}
	if count == 0 {
		return "0"
	}
	return strconv.Itoa(count)
}

func tuiSlashCommandProvider(cwd string) tui.SlashCommandProvider {
	return tuiSlashCommandProviderWithOptions(options{cwd: cwd})
}

func tuiSlashCommandProviderWithOptions(opts options) tui.SlashCommandProvider {
	discovery := slashcommands.DiscoveryOptions{}
	if opts.runtimeProfile.IsBare() {
		discovery = slashcommands.DiscoveryOptions{Bare: true, ExplicitRoots: bareDiscoveryRoots(opts)}
	}
	return func(ctx context.Context, prefix string) ([]tui.SlashCommand, error) {
		_ = ctx
		return listTUISlashCommandsWithDiscovery(opts.cwd, prefix, discovery)
	}
}

func tuiResumeSessionProvider(store session.Store, cwd string, excludedSessionIDs func() []string) tui.ResumeSessionProvider {
	return func(ctx context.Context) ([]tui.ResumeSession, error) {
		_ = ctx
		var excluded []string
		if excludedSessionIDs != nil {
			excluded = excludedSessionIDs()
		}
		return resumeSessionItems(store, cwd, excluded, 100)
	}
}

func tuiRewindCandidateProvider(opts *options, recorder *session.Recorder) tui.RewindCandidateProvider {
	return func(ctx context.Context) ([]tui.RewindCandidate, error) {
		_ = ctx
		current := options{}
		if opts != nil {
			current = *opts
		}
		_, path, err := activeRewindSession(current, recorder)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(path) == "" {
			return nil, errors.New("/rewind has no active session")
		}
		// Offer only current-conversation messages as rewind targets (v2: current chain).
		entries, err := session.LoadConversation(path)
		if err != nil {
			return nil, err
		}
		candidates := rewindCandidates(entries, 20)
		out := make([]tui.RewindCandidate, 0, len(candidates))
		for _, candidate := range candidates {
			out = append(out, tui.RewindCandidate{ID: candidate.ID, Preview: candidate.Preview, Mode: "message"})
		}
		return out, nil
	}
}

func coreRuntimeTools(settings config.Settings, client skill.MessageStreamer, model string) []tools.Tool {
	return coreRuntimeToolsWithOptions(settings, client, model, options{})
}

func coreRuntimeToolsWithImageGenerator(settings config.Settings, client skill.MessageStreamer, model string, generator imagegensvc.Generator) []tools.Tool {
	return coreRuntimeToolsWithOptions(settings, client, model, options{imageGenerator: generator})
}

func coreRuntimeToolsWithOptions(settings config.Settings, client skill.MessageStreamer, model string, runtimeOptions options) []tools.Tool {
	toolList := []tools.Tool{
		taskoutput.New(),
		askuserquestion.New(),
		planmode.NewEnter(),
		planmode.NewExit(),
		mcpresources.NewList(settings.MCPServers),
		mcpresources.NewRead(settings.MCPServers),
		agenttool.NewList(),
		agenttool.NewGet(),
		agenttool.NewStop(),
		agenttool.NewMessage(),
		fileread.New(),
		filewrite.New(),
		fileedit.New(),
		fileedit.NewMulti(),
		notebook.NewRead(),
		notebook.NewEdit(),
		ls.New(),
		glob.New(),
		grep.New(),
		lsp.New(),
		todowrite.NewRead(),
		todowrite.New(),
		skill.New(client, model),
		workflow.New(),
		worktree.New(),
		webbrowser.New(),
		webfetch.New(),
		websearch.NewWithEndpoint(webSearchEndpoint(settings)),
		powershell.New(),
		bash.New(),
		bashoutput.New(),
		bashoutput.NewKillShell(),
	}
	if runtimeOptions.asyncChannelImages {
		if runtimeOptions.imageScheduler != nil {
			async := imagegentool.WithScheduler(runtimeOptions.imageScheduler, runtimeOptions.imageOriginFactory)
			toolList = append(toolList, imagegentool.NewGenerate(nil, async), imagegentool.NewEdit(nil, async))
		}
	} else if runtimeOptions.imageGenerator != nil {
		previewInContext := settings.ImageGeneration != nil && config.ResolveImageGenerationEnabled(settings.ImageGeneration.PreviewInContext)
		preview := imagegentool.WithArtifactPreview(previewInContext)
		toolList = append(toolList, imagegentool.NewGenerate(runtimeOptions.imageGenerator, preview), imagegentool.NewEdit(runtimeOptions.imageGenerator, preview))
	}
	toolList = append(toolList, sessionControlToolsForOptions(runtimeOptions)...)
	toolList = append(toolList, computerUseToolsForOptions(runtimeOptions)...)
	return toolList
}

// sessionControlToolsForOptions is the runtime-side profile gate. It protects
// normal CLI/TUI/chat/channel/coding prompts from both the capability and the
// seven tool-definition prompt-cost additions.
func computerUseToolsForOptions(runtimeOptions options) []tools.Tool {
	if !runtimeOptions.computerUseProfile || runtimeOptions.runtimeProfile.IsBare() ||
		runtimeOptions.computerUseService == nil || !runtimeOptions.computerUseImageSupported ||
		runtimeOptions.tenantID == 0 || runtimeOptions.tenantUserID == 0 || runtimeOptions.tenantSessionID == 0 {
		return nil
	}
	return []tools.Tool{computerusetool.New()}
}

func sessionControlToolsForOptions(runtimeOptions options) []tools.Tool {
	if !runtimeOptions.sessionControlProfile || runtimeOptions.sessionControlService == nil || runtimeOptions.tenantID == 0 || runtimeOptions.tenantUserID == 0 {
		return nil
	}
	return sessioncontroltool.New(runtimeOptions.sessionControlService)
}

func bareRuntimeTools() []tools.Tool {
	return []tools.Tool{
		fileread.New(),
		fileedit.New(),
		bash.New(),
	}
}

func webSearchEndpoint(settings config.Settings) string {
	if settings.WebSearch == nil {
		return ""
	}
	return strings.TrimSpace(settings.WebSearch.Endpoint)
}

func listTUISlashCommands(cwd, prefix string) ([]tui.SlashCommand, error) {
	return listTUISlashCommandsWithOptions(options{cwd: cwd}, prefix)
}

func listTUISlashCommandsWithOptions(opts options, prefix string) ([]tui.SlashCommand, error) {
	discovery := slashcommands.DiscoveryOptions{}
	if opts.runtimeProfile.IsBare() {
		discovery = slashcommands.DiscoveryOptions{Bare: true, ExplicitRoots: bareDiscoveryRoots(opts)}
	}
	return listTUISlashCommandsWithDiscovery(opts.cwd, prefix, discovery)
}

func listTUISlashCommandsWithDiscovery(cwd, prefix string, discovery slashcommands.DiscoveryOptions) ([]tui.SlashCommand, error) {
	commands, err := slashcommands.ListWithOptions(cwd, prefix, discovery)
	if err != nil {
		return nil, err
	}
	out := make([]tui.SlashCommand, 0, len(commands))
	for _, command := range commands {
		out = append(out, tui.SlashCommand{
			Name:        command.Name,
			Description: command.Description,
			Source:      command.Source,
		})
	}
	return out, nil
}

func configureTUILogger() func() {
	logPath := strings.TrimSpace(os.Getenv("NIUMA_TUI_LOG_PATH"))
	switch strings.ToLower(logPath) {
	case "stderr", "stdout", "-":
		return func() {}
	}
	if logPath == "" {
		path, err := config.CurrentIdentity("").GlobalStatePath("debug", "niuma-tui.log")
		if err != nil || strings.TrimSpace(path) == "" {
			return observability.SetDefaultZapLogger(nil)
		}
		logPath = path
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		return observability.SetDefaultZapLogger(nil)
	}
	level := strings.TrimSpace(os.Getenv("NIUMA_LOG_LEVEL"))
	if level == "" {
		level = strings.TrimSpace(os.Getenv("LOG_LEVEL"))
	}
	restore, err := observability.ConfigureDefaultZapLoggerWithOutputPaths(level, false, []string{logPath})
	if err != nil {
		return observability.SetDefaultZapLogger(nil)
	}
	return restore
}

type tuiEventWriter struct {
	events chan<- tui.StreamEvent
}

// tuiEventSink 把查询事件转成 TUI 流事件。
type tuiEventSink struct {
	events chan<- tui.StreamEvent
}

func (s tuiEventSink) OnThinking(_ context.Context, text string) error {
	s.events <- tui.StreamEvent{Type: tui.StreamThinking, Text: text}
	return nil
}

func (s tuiEventSink) OnToolCall(_ context.Context, event query.ToolCallEvent) error {
	s.events <- tui.StreamEvent{Type: tui.StreamToolStart, ToolID: event.ID, ToolName: event.Name, Output: string(event.Input)}
	return nil
}

func (s tuiEventSink) OnToolResult(_ context.Context, trace query.ToolTrace) error {
	s.events <- tui.StreamEvent{Type: tui.StreamToolResult, ToolID: trace.ID, ToolName: trace.Name, Output: trace.Output, IsError: trace.IsError, Input: trace.Input}
	return nil
}

func (s tuiEventSink) OnTextAmended(_ context.Context, streamed, final string) error {
	s.events <- tui.StreamEvent{Type: tui.StreamTextAmended, Text: final, PrevText: streamed}
	return nil
}

func (s tuiEventSink) OnUsage(_ context.Context, _ int, _ query.Usage) error { return nil }

func (s tuiEventSink) OnMessageStop(_ context.Context, _ int, _ string, _ query.Usage) error {
	return nil
}

func (s tuiEventSink) OnCompact(_ context.Context, _ compact.Result) error { return nil }

func tuiQueryResult(result query.Result, opts options, toolCount int) tui.QueryResult {
	toolCalls := make([]tui.ToolCall, 0, len(result.ToolCalls))
	for _, call := range result.ToolCalls {
		toolCalls = append(toolCalls, tui.ToolCall{ID: call.ID, Name: call.Name, Output: call.Output, IsError: call.IsError})
	}
	return tui.QueryResult{
		Response:   result.Response,
		Model:      result.Model,
		StopReason: result.StopReason,
		Turns:      result.Turns,
		SessionID:  result.SessionID,
		Usage: tui.Usage{
			InputTokens:                         result.Usage.InputTokens,
			OutputTokens:                        result.Usage.OutputTokens,
			LastInputTokens:                     result.Usage.LastInputTokens,
			CacheCreationInputTokens:            result.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:                result.Usage.CacheReadInputTokens,
			CacheCreationEphemeral1hInputTokens: result.Usage.CacheCreationEphemeral1hInputTokens,
			CacheCreationEphemeral5mInputTokens: result.Usage.CacheCreationEphemeral5mInputTokens,
			ServiceTier:                         result.Usage.ServiceTier,
			InferenceGeo:                        result.Usage.InferenceGeo,
			Speed:                               result.Usage.Speed,
		},
		ToolCalls: toolCalls,
		Context:   tuiRuntimeContext(opts, toolCount),
	}
}

func tuiRuntimeContext(opts options, toolCount int) tui.RuntimeContext {
	return tui.RuntimeContext{
		CWD:           opts.cwd,
		MaxTurns:      opts.maxTurns,
		MaxTokens:     opts.maxTokens,
		ToolCount:     toolCount,
		ContextWindow: tuiContextWindow(opts),
	}
}

func tuiContextWindow(opts options) int {
	settings := config.LoadForCWD(opts.cwd).Settings
	if settings.AutoCompact == nil || len(settings.AutoCompact.ModelContext) == 0 {
		return 0
	}
	return lookupTUIModelContext(settings.AutoCompact.ModelContext, config.ResolveModel(opts.cwd, opts.model))
}

func lookupTUIModelContext(values map[string]int, model string) int {
	if len(values) == 0 {
		return 0
	}
	if value := values[model]; value > 0 {
		return value
	}
	normalized := strings.ToLower(strings.TrimSpace(model))
	for key, value := range values {
		if value <= 0 {
			continue
		}
		k := strings.ToLower(strings.TrimSpace(key))
		if k != "" && (normalized == k || strings.Contains(normalized, k)) {
			return value
		}
	}
	return 0
}

func (w tuiEventWriter) Write(p []byte) (int, error) {
	if len(p) > 0 {
		w.events <- tui.StreamEvent{Type: tui.StreamText, Text: string(p)}
	}
	return len(p), nil
}

func tuiPermissionPrompt(events chan<- tui.StreamEvent) func(context.Context, tools.PermissionPromptRequest) tools.PermissionPromptResponse {
	return func(ctx context.Context, req tools.PermissionPromptRequest) tools.PermissionPromptResponse {
		reply := make(chan tui.PermissionDecision, 1)
		event := tui.StreamEvent{
			Type: tui.StreamPermissionRequest,
			Permission: &tui.PermissionRequest{
				ToolName: req.ToolName,
				Request:  req.Request,
				Reason:   req.Reason,
				Rule:     req.Rule,
				Source:   req.Source,
				Input:    req.Input,
				OneShot:  req.OneShot,
			},
			Reply: reply,
		}
		select {
		case events <- event:
		case <-ctx.Done():
			return tools.PermissionPromptResponse{Allowed: false, Reason: ctx.Err().Error()}
		}
		select {
		case decision := <-reply:
			return tools.PermissionPromptResponse{
				Allowed:     decision.Allowed,
				Reason:      decision.Reason,
				Destination: decision.Destination,
				Rule:        decision.Rule,
				Decision:    permissionDecisionText(decision.Allowed),
			}
		case <-ctx.Done():
			return tools.PermissionPromptResponse{Allowed: false, Reason: ctx.Err().Error()}
		}
	}
}

func tuiUserQuestionPrompt(events chan<- tui.StreamEvent) func(context.Context, tools.UserQuestionRequest) tools.UserQuestionResponse {
	return func(ctx context.Context, req tools.UserQuestionRequest) tools.UserQuestionResponse {
		reply := make(chan tui.UserQuestionAnswer, 1)
		event := tui.StreamEvent{
			Type: tui.StreamUserQuestion,
			Question: &tui.UserQuestionRequest{
				Question: req.Question,
				Choices:  append([]string(nil), req.Choices...),
			},
			QuestionReply: reply,
		}
		select {
		case events <- event:
		case <-ctx.Done():
			return tools.UserQuestionResponse{}
		}
		select {
		case answer := <-reply:
			return tools.UserQuestionResponse{Answered: answer.Answered, Answer: answer.Answer}
		case <-ctx.Done():
			return tools.UserQuestionResponse{}
		}
	}
}

func tuiNestedAgentProgress(events chan<- tui.StreamEvent) func(agenttasks.EventInput) {
	return func(event agenttasks.EventInput) {
		streamEvent := tui.StreamEvent{
			Type:    tui.StreamNestedProgress,
			Event:   event.EventType,
			TaskID:  event.TaskID,
			Payload: json.RawMessage(event.PayloadJSON),
		}
		select {
		case events <- streamEvent:
		default:
		}
	}
}

func permissionDecisionText(allowed bool) string {
	if allowed {
		return "allow"
	}
	return "deny"
}

func handleInteractiveSlash(ctx context.Context, opts options, input string, stdout, stderr io.Writer, recorder *session.Recorder) (handled bool, exit bool, err error) {
	if !strings.HasPrefix(input, "/") {
		return false, false, nil
	}
	fields := strings.Fields(strings.TrimPrefix(input, "/"))
	if len(fields) == 0 {
		return true, false, nil
	}
	cmd, args := fields[0], fields[1:]
	if opts.runtimeProfile.IsBare() && !slashcommands.BareBuiltinAllowed(cmd) {
		return true, false, bareUnavailableSlashError{command: cmd}
	}
	switch cmd {
	case "exit", "quit":
		return true, true, nil
	case "help":
		printSlashHelp(stdout, opts.cwd)
	case "clear":
		fmt.Fprint(stdout, "\033[H\033[2J")
	case "status":
		err = statusCommand(opts.cwd, stdout)
	case "tools":
		err = toolsCommand(ctx, opts, stdout)
	case "resume":
		if len(args) == 0 {
			err = sessionListCommand(session.DefaultStore(), opts.cwd, stdout)
		} else if len(args) == 1 && !strings.HasPrefix(args[0], "-") {
			err = interactiveResumeCommand(session.DefaultStore(), args[0], []string{recorderSessionID(recorder), opts.resume}, stdout)
		} else {
			err = sessionCommand(args, stdout)
		}
	case "sessions", "session":
		err = sessionCommand(args, stdout)
	case "agent-tasks", "agent-task", "tasks":
		err = tenantAgentTasksCommand(ctx, args, stdout)
	case "mcp":
		err = mcpCommand(ctx, args, opts.cwd, stdout)
	case "skills":
		err = skillsCommand(ctx, args, opts.cwd, stdout)
	case "plugins", "plugin":
		err = pluginsCommand(args, opts.cwd, stdout)
	case "model":
		err = modelCommand(args, opts.cwd, stdout)
	case "permissions":
		err = permissionsCommand(args, stdout)
	case "hooks":
		err = hooksCommand(args, stdout)
	case "usage", "cost":
		err = usageCommand(opts.cwd, stdout)
	case "diff":
		err = diffCommand(ctx, args, opts.cwd, stdout)
	case "branch":
		err = branchCommand(ctx, opts.cwd, stdout)
	case "review":
		err = reviewCommand(ctx, args, opts, stdout, stderr)
	case "loop":
		err = loopSlashCommand(ctx, args, opts, stdout, stderr)
	case "goal":
		err = goalCommand(ctx, args, opts, stdout, stderr)
	case "compact":
		err = compactSlashCommand(args, opts, recorder, stdout)
	case "recap":
		err = recapSlashCommand(ctx, args, opts, recorder, stdout)
	case "rewind", "checkpoint":
		err = rewindSlashCommand(args, opts, recorder, stdout)
	case "branches":
		err = interactiveBranchesCommand(args, recorder, stdout)
	case "redo":
		err = interactiveRedoCommand(args, recorder, stdout)
	case "ps":
		err = backgroundListCommand(stdout)
	case "logs":
		err = backgroundLogsCommand(args, stdout)
	case "attach":
		err = backgroundAttachCommand(args, stdout)
	default:
		err = unknownSlashCommandError{command: cmd}
	}
	return true, false, err
}

// interactiveBranchesCommand lists (or compares) the branches of the CURRENT
// session, so v2 non-destructive rewinds/redos are visible in the TUI without the
// user typing the session id. `/branches` lists; `/branches --compare <A> <B>`
// diffs two branches.
func interactiveBranchesCommand(args []string, recorder *session.Recorder, stdout io.Writer) error {
	id := recorderSessionID(recorder)
	if id == "" {
		return errors.New("/branches has no active session")
	}
	return sessionBranchesCommand(append([]string{"branches", id}, args...), stdout)
}

// interactiveRedoCommand moves the CURRENT session's active leaf back onto a
// previously abandoned branch (see `/branches` for leaf ids). The next turn's
// context is rebuilt from the new current chain.
func interactiveRedoCommand(args []string, recorder *session.Recorder, stdout io.Writer) error {
	id := recorderSessionID(recorder)
	if id == "" {
		return errors.New("/redo has no active session")
	}
	if len(args) < 1 || strings.HasPrefix(args[0], "-") {
		return errors.New("/redo requires a branch leaf id (see /branches)")
	}
	// Redo moves the active leaf out-of-band; the live recorder is realigned at the
	// next turn's start (query.Session.run → Recorder.SyncLeafFromDisk).
	return sessionRedoCommand(append([]string{"redo", id}, args...), stdout)
}

func interactiveResumeSelection(input string) (string, bool) {
	fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(input), "/"))
	if len(fields) != 2 || !strings.EqualFold(fields[0], "resume") || strings.HasPrefix(fields[1], "-") {
		return "", false
	}
	return fields[1], true
}

func recorderSessionID(recorder *session.Recorder) string {
	if recorder == nil {
		return ""
	}
	return strings.TrimSpace(recorder.SessionID)
}

func isCurrentRecorderSession(recorder *session.Recorder, sessionID string) bool {
	currentID := recorderSessionID(recorder)
	return currentID != "" && currentID == strings.TrimSpace(sessionID)
}

func isActiveResumeSession(recorder *session.Recorder, activeResumeID, sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false
	}
	return isCurrentRecorderSession(recorder, sessionID) || strings.TrimSpace(activeResumeID) == sessionID
}

func interactiveCompactCommand(input string) bool {
	fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(input), "/"))
	return len(fields) > 0 && strings.EqualFold(fields[0], "compact")
}

func transcriptEntryCount(recorder *session.Recorder) (int, error) {
	if recorder == nil || strings.TrimSpace(recorder.Path) == "" {
		return 0, nil
	}
	entries, err := session.Load(recorder.Path)
	if err != nil {
		return 0, err
	}
	return len(entries), nil
}

func resumeContinuationContext(recorder *session.Recorder, entryOffset int) ([]anthropic.MessageParam, []query.ToolResultReplacementRecord, error) {
	if recorder == nil || strings.TrimSpace(recorder.Path) == "" {
		return nil, nil, nil
	}
	entries, err := session.Load(recorder.Path)
	if err != nil {
		return nil, nil, err
	}
	if entryOffset < 0 || entryOffset > len(entries) {
		return nil, nil, fmt.Errorf("invalid resume transcript entry offset %d for %d entries", entryOffset, len(entries))
	}
	postOffset := entries[entryOffset:]
	if session.IsV2Entries(entries) {
		// v2 is a message graph: physical order != conversation order. The
		// continuation is the current-chain nodes appended after the offset, so a
		// mid-session non-destructive rewind's abandoned branch is not replayed.
		postOffset = currentChainSuffix(entries, entryOffset)
	}
	return query.MessagesFromTranscript(postOffset), query.ToolResultReplacementsFromTranscript(postOffset), nil
}

// currentChainSuffix returns the current-chain entries whose physical position is
// at or after offset, preserving chain (root→leaf) order. It is the v2 analog of
// entries[offset:]: the delta the resumed session has grown, excluding abandoned
// branches left off the current chain by a rewind.
func currentChainSuffix(entries []session.Entry, offset int) []session.Entry {
	postIDs := make(map[string]bool)
	for _, entry := range entries[offset:] {
		if entry.ID != "" {
			postIDs[entry.ID] = true
		}
	}
	var out []session.Entry
	for _, entry := range session.CurrentChain(entries) {
		if postIDs[entry.ID] {
			out = append(out, entry)
		}
	}
	return out
}

func interactiveGoalCommand(input string) bool {
	fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(input), "/"))
	return len(fields) > 0 && strings.EqualFold(fields[0], "goal")
}

func sessionStatusFromInteractiveSlash(input, output string) string {
	fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(input), "/"))
	if len(fields) == 0 {
		return ""
	}
	command := strings.ToLower(fields[0])
	text := strings.TrimSpace(output)
	switch command {
	case "compact":
		if text != "" {
			return truncateForTUIStatus(text, 96)
		}
		return "compacted current session"
	case "checkpoint":
		if text != "" {
			return truncateForTUIStatus(text, 96)
		}
		return "checkpoint selected"
	case "rewind":
		if text != "" {
			return truncateForTUIStatus(text, 96)
		}
		return "rewind selected"
	case "resume":
		if len(fields) > 1 {
			return "resumed " + fields[1]
		}
	}
	return ""
}

func truncateForTUIStatus(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if limit <= 0 || len(runes) <= limit {
		return text
	}
	if limit <= 1 {
		return string(runes[:limit])
	}
	return string(runes[:limit-1]) + "…"
}

func interactiveResumeCommand(store session.Store, sessionID string, activeSessionIDs []string, stdout io.Writer) error {
	sessionID = strings.TrimSpace(sessionID)
	for _, activeSessionID := range activeSessionIDs {
		if sessionID != "" && sessionID == strings.TrimSpace(activeSessionID) {
			fmt.Fprintf(stdout, "Session is already active: %s\n", sessionID)
			return nil
		}
	}
	summary, ok, err := store.Find(sessionID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	item := resumeSessionItemFromSummary(summary)
	fmt.Fprintf(stdout, "Resumed session %s\n", summary.SessionID)
	if item.Title != "" {
		fmt.Fprintf(stdout, "Title: %s\n", item.Title)
	}
	if item.Preview != "" {
		fmt.Fprintf(stdout, "Recent: %s\n", item.Preview)
	}
	if item.CWD != "" {
		fmt.Fprintf(stdout, "Project: %s\n", item.CWD)
	}
	fmt.Fprintln(stdout, "Next message will use this transcript as context.")
	return nil
}

type unknownSlashCommandError struct {
	command string
}

type bareUnavailableSlashError struct {
	command string
}

func (e bareUnavailableSlashError) Error() string {
	return fmt.Sprintf("/%s is unavailable in --bare mode", e.command)
}

func (e unknownSlashCommandError) Error() string {
	return fmt.Sprintf("unknown slash command: /%s", e.command)
}

func isUnknownSlashCommand(err error) bool {
	var target unknownSlashCommandError
	if errors.As(err, &target) {
		return true
	}
	var bareTarget bareUnavailableSlashError
	return errors.As(err, &bareTarget)
}

const loopDefaultInterval = 10 * time.Minute

func loopSlashCommand(ctx context.Context, args []string, opts options, stdout, stderr io.Writer) error {
	_ = ctx
	_ = stderr
	interval, prompt, note, err := parseLoopArgs(strings.Join(args, " "))
	if err != nil {
		return err
	}
	if strings.TrimSpace(prompt) == "" {
		fmt.Fprint(stdout, loopUsageMessage())
		return nil
	}
	store := background.DefaultStore()
	job, err := store.CreateWithOptions(background.Options{
		Prompt:          prompt,
		CWD:             opts.cwd,
		Kind:            "loop",
		IntervalSeconds: int(interval.Seconds()),
		Model:           opts.model,
		Provider:        opts.providerName,
		OutputFormat:    firstNonEmptyString(opts.outputFormat, "text"),
		MaxTurns:        opts.maxTurns,
		MaxTokens:       opts.maxTokens,
		Resume:          opts.resume,
		SessionID:       opts.sessionID,
		SessionName:     opts.sessionName,
		NoPersistence:   opts.noPersistence,
		SystemPrompt:    opts.systemPrompt,
		AppendSystem:    opts.appendSystem,
		AllowedTools:    opts.allowedTools,
		DeniedTools:     opts.deniedTools,
		PermissionMode:  opts.permissionMode,
		AdditionalDirs:  opts.additionalDirs,
		SkipPermissions: opts.skipPermissions,
	})
	if err != nil {
		return err
	}
	scheduleStore := scheduler.DefaultStore()
	schedule, err := scheduleStore.Create(scheduler.Options{
		Prompt:          prompt,
		CWD:             opts.cwd,
		Kind:            "loop",
		Spec:            loopCronSpec(interval),
		IntervalSeconds: int(interval.Seconds()),
		Model:           opts.model,
		Provider:        opts.providerName,
		OutputFormat:    firstNonEmptyString(opts.outputFormat, "text"),
		MaxTurns:        opts.maxTurns,
		MaxTokens:       opts.maxTokens,
		Resume:          opts.resume,
		SessionID:       opts.sessionID,
		SessionName:     opts.sessionName,
		NoPersistence:   opts.noPersistence,
		SystemPrompt:    opts.systemPrompt,
		AppendSystem:    opts.appendSystem,
		AllowedTools:    opts.allowedTools,
		DeniedTools:     opts.deniedTools,
		PermissionMode:  opts.permissionMode,
		AdditionalDirs:  opts.additionalDirs,
		SkipPermissions: opts.skipPermissions,
	}, job)
	if err != nil {
		return err
	}
	if os.Getenv("NIUMA_BG_QUEUE_ONLY") == "1" {
		if note != "" {
			fmt.Fprintln(stdout, note)
		}
		fmt.Fprintf(stdout, "Queued loop %s every %s: %s\n", job.ID, humanLoopInterval(interval), prompt)
		fmt.Fprintf(stdout, "Schedule %s uses cron spec %s\n", schedule.ID, schedule.Spec)
		return nil
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	if _, _, err := scheduleStore.EnsureDaemon(executable, os.Environ()); err != nil {
		return err
	}
	executor := scheduler.ChildProcessExecutor{Executable: executable}
	if err := executor.RunSchedule(ctx, schedule); err != nil {
		return err
	}
	next := time.Now().UTC().Add(interval)
	if _, _, err := store.RecordLoopRun(job.ID, &next); err != nil {
		return err
	}
	if _, _, err := scheduleStore.RecordRun(schedule.ID, &next, ""); err != nil {
		return err
	}
	if note != "" {
		fmt.Fprintln(stdout, note)
	}
	fmt.Fprintf(stdout, "Started loop %s every %s: %s\n", job.ID, humanLoopInterval(interval), prompt)
	fmt.Fprintf(stdout, "Schedule %s uses cron spec %s and repeats until killed with: kill %s\n", schedule.ID, schedule.Spec, job.ID)
	return nil
}

func loopCronSpec(interval time.Duration) string {
	switch {
	case interval%(24*time.Hour) == 0:
		return "@every " + strconv.Itoa(int(interval/(24*time.Hour))*24) + "h"
	case interval%time.Hour == 0:
		return "@every " + strconv.Itoa(int(interval/time.Hour)) + "h"
	default:
		return "@every " + strconv.Itoa(max(1, int(interval/time.Minute))) + "m"
	}
}

func parseLoopArgs(raw string) (time.Duration, string, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, "", "", nil
	}
	fields := strings.Fields(raw)
	if len(fields) > 0 {
		if interval, note, ok, err := parseLoopInterval(fields[0]); ok || err != nil {
			if err != nil {
				return 0, "", "", err
			}
			return interval, strings.TrimSpace(strings.TrimPrefix(raw, fields[0])), note, nil
		}
	}
	if interval, prompt, note, ok, err := parseTrailingEveryLoopInterval(raw); ok || err != nil {
		return interval, prompt, note, err
	}
	return loopDefaultInterval, raw, "", nil
}

func parseTrailingEveryLoopInterval(raw string) (time.Duration, string, string, bool, error) {
	lower := strings.ToLower(strings.TrimSpace(raw))
	fields := strings.Fields(lower)
	if len(fields) < 3 {
		return 0, "", "", false, nil
	}
	everyIndex := -1
	for i := len(fields) - 2; i >= 0; i-- {
		if fields[i] == "every" {
			everyIndex = i
			break
		}
	}
	if everyIndex < 0 {
		return 0, "", "", false, nil
	}
	intervalText := strings.Join(fields[everyIndex+1:], " ")
	interval, note, ok, err := parseLoopInterval(intervalText)
	if !ok && err == nil && everyIndex+2 < len(fields) {
		interval, note, ok, err = parseLoopInterval(fields[everyIndex+1] + fields[everyIndex+2])
	}
	if !ok || err != nil {
		return 0, "", "", ok, err
	}
	promptFields := strings.Fields(raw)[:everyIndex]
	return interval, strings.Join(promptFields, " "), note, true, nil
}

func parseLoopInterval(raw string) (time.Duration, string, bool, error) {
	text := strings.ToLower(strings.TrimSpace(raw))
	text = strings.ReplaceAll(text, " ", "")
	if n, err := strconv.Atoi(text); err == nil && n > 0 {
		note := fmt.Sprintf("Interpreted bare number %d as %dm.", n, n)
		return time.Duration(n) * time.Minute, note, true, nil
	}
	unitWords := map[string]string{
		"second": "s", "seconds": "s",
		"minute": "m", "minutes": "m",
		"hour": "h", "hours": "h",
		"day": "d", "days": "d",
	}
	for word, suffix := range unitWords {
		if strings.HasSuffix(text, word) {
			text = strings.TrimSuffix(text, word) + suffix
			break
		}
	}
	if len(text) < 2 {
		return 0, "", false, nil
	}
	unit := text[len(text)-1]
	if !strings.ContainsRune("smhd", rune(unit)) {
		return 0, "", false, nil
	}
	n, err := strconv.Atoi(text[:len(text)-1])
	if err != nil || n <= 0 {
		return 0, "", true, fmt.Errorf("invalid loop interval: %s", raw)
	}
	switch unit {
	case 's':
		minutes := max(1, (n+59)/60)
		note := ""
		if minutes*60 != n {
			note = fmt.Sprintf("Rounded %ds to %dm because loop granularity is one minute.", n, minutes)
		}
		return time.Duration(minutes) * time.Minute, note, true, nil
	case 'm':
		return time.Duration(n) * time.Minute, "", true, nil
	case 'h':
		return time.Duration(n) * time.Hour, "", true, nil
	case 'd':
		return time.Duration(n) * 24 * time.Hour, "", true, nil
	default:
		return 0, "", false, nil
	}
}

func loopUsageMessage() string {
	return `Usage: /loop [interval] <prompt>

Run a prompt or slash command on a recurring interval.

Intervals: Ns, Nm, Nh, Nd (e.g. 5m, 30m, 2h, 1d). Minimum granularity is 1 minute.
If no interval is specified, defaults to 10m.

Examples:
  /loop 5m /babysit-prs
  /loop 30m check the deploy
  /loop 1h /standup 1
  /loop check the deploy
  /loop check the deploy every 20m
`
}

func humanLoopInterval(interval time.Duration) string {
	if interval%(24*time.Hour) == 0 {
		days := int(interval / (24 * time.Hour))
		if days == 1 {
			return "1 day"
		}
		return strconv.Itoa(days) + " days"
	}
	if interval%time.Hour == 0 {
		hours := int(interval / time.Hour)
		if hours == 1 {
			return "1 hour"
		}
		return strconv.Itoa(hours) + " hours"
	}
	minutes := int(interval / time.Minute)
	if minutes <= 1 {
		return "1 minute"
	}
	return strconv.Itoa(minutes) + " minutes"
}

func dynamicSlashPrompt(cwd, input string) (string, bool, error) {
	return dynamicSlashPromptWithOptions(options{cwd: cwd}, input)
}

func dynamicSlashPromptWithOptions(opts options, input string) (string, bool, error) {
	discovery := slashcommands.DiscoveryOptions{}
	if opts.runtimeProfile.IsBare() {
		discovery = slashcommands.DiscoveryOptions{Bare: true, ExplicitRoots: bareDiscoveryRoots(opts)}
	}
	return slashcommands.ResolvePromptWithOptions(context.Background(), opts.cwd, input, discovery)
}

func bareDiscoveryRoots(opts options) []string {
	copyOpts := opts
	cfg, err := resolveRuntimeProviderConfig(&copyOpts)
	if err != nil {
		return resolveRuntimeDirectories(opts.cwd, opts.additionalDirs)
	}
	return resolveRuntimeDirectories(opts.cwd, cfg.Settings.AdditionalDirectories)
}

type slashHelpItem struct {
	Command string
	Zh      string
}

var slashHelpItems = []slashHelpItem{
	{"/help", "查看命令帮助"},
	{"/init", "初始化或优化 niuma.md"},
	{"/clear", "清屏"},
	{"/status", "查看当前项目和运行状态"},
	{"/tools", "查看可用工具"},
	{"/sessions", "查看和管理会话"},
	{"/agent-tasks [list|events|cancel]", "查看、追踪或取消 agent task"},
	{"/mcp [list|tools|call|resources|resource|prompts|prompt]", "管理 MCP 服务、工具、资源和 prompts"},
	{"/skills [list|show]", "查看和展示技能"},
	{"/plugins [list|show]", "查看和展示插件"},
	{"/model [get|set|list]", "查看、设置或列出模型"},
	{"/permissions [list|mode|allow|deny|remove|clear]", "查看和管理权限规则"},
	{"/hooks [list|add|remove|clear]", "查看和管理 hooks"},
	{"/usage", "查看 token 和费用用量"},
	{"/thinking [full|summary|hide|show <turn>]", "展开、折叠或隐藏 TUI 思考过程"},
	{"/diff [--stat]", "查看当前工作区 diff"},
	{"/branch", "查看当前 git 分支"},
	{"/review [--staged]", "对当前或暂存改动做代码审查"},
	{"/loop [interval] <prompt>", "按间隔循环执行提示词"},
	{goalcmd.SlashUsage, goalcmd.HelpDescriptionZH},
	{"/compact", "压缩当前上下文"},
	{"/recap [show|off]", "生成或查看会话回顾"},
	{"/rewind [message-id] [--conversation-only|--files-only]", "回退代码或对话到指定消息"},
	{"/branches [--compare <叶A> <叶B>]", "列出/对比 v2 会话的分支（非破坏回退产生）"},
	{"/redo <叶id> [--conversation-only]", "切回某条被搁置的分支并还原文件（见 /branches）"},
	{"/ps", "列出后台会话"},
	{"/logs <background_id>", "查看后台会话日志"},
	{"/attach <background_id>", "连接到后台会话"},
	{"/<skill-name> [args]", "运行用户可调用技能"},
	{"/<custom-command> [args]", "运行 .claude/commands/*.md 自定义命令"},
	{"/exit", "退出 TUI"},
}

func printSlashHelp(stdout io.Writer, cwd string) {
	settings := config.LoadSettings(cwd)
	id := identity.FromSettings(config.IdentitySettings(settings.Settings))
	fmt.Fprintln(stdout, "Slash commands / 斜杠命令:")
	for _, item := range slashHelpItems {
		if item.Command == "/init" {
			item.Zh = "初始化或优化 " + id.GuidanceFilename
		}
		fmt.Fprintf(stdout, "  %-58s %s\n", item.Command, item.Zh)
	}
}
