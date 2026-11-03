package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/buildinfo"
	"github.com/konglong87/super-niuma/internal/session"
	"github.com/konglong87/super-niuma/internal/telemetry"
)

const runtimeTraceAgentName = "niuma"

type RuntimeTraceReadOnlyToolMode string

const (
	RuntimeTraceReadOnlyToolModeSerial   RuntimeTraceReadOnlyToolMode = "serial"
	RuntimeTraceReadOnlyToolModeParallel RuntimeTraceReadOnlyToolMode = "parallel"
)

type RuntimeTraceRunStatus string

const (
	RuntimeTraceRunStatusUnknown   RuntimeTraceRunStatus = "unknown"
	RuntimeTraceRunStatusFailed    RuntimeTraceRunStatus = "failed"
	RuntimeTraceRunStatusCompleted RuntimeTraceRunStatus = "completed"
)

type RuntimeTraceArtifact struct {
	SchemaVersion string                     `json:"schema_version"`
	Run           RuntimeTraceRun            `json:"run"`
	Configuration *RuntimeTraceConfiguration `json:"configuration,omitempty"`
	Summary       RuntimeTraceSummary        `json:"summary"`
	Quality       RuntimeTraceQuality        `json:"quality"`
	Diagnostics   []RuntimeTraceDiagnostic   `json:"diagnostics"`
	Spans         []RuntimeTraceArtifactSpan `json:"spans"`
}

type RuntimeTraceConfiguration struct {
	ReadOnlyToolMode                  RuntimeTraceReadOnlyToolMode `json:"read_only_tool_mode,omitempty"`
	RequestedMaxParallelReadOnlyTools int                          `json:"requested_max_parallel_read_only_tools"`
	EffectiveMaxParallelReadOnlyTools int                          `json:"effective_max_parallel_read_only_tools,omitempty"`
}

type RuntimeTraceRun struct {
	RunID        string                `json:"run_id"`
	Agent        string                `json:"agent"`
	AgentVersion string                `json:"agent_version,omitempty"`
	BuildInfo    buildinfo.Info        `json:"build_info"`
	SessionID    string                `json:"session_id"`
	Status       RuntimeTraceRunStatus `json:"status"`
	StartedAt    time.Time             `json:"started_at,omitempty"`
	DurationMS   int64                 `json:"duration_ms"`
}

type RuntimeTraceSummary = traceSummary
type RuntimeTraceQuality = traceQuality
type RuntimeTraceDiagnostic = traceDiagnostic

type RuntimeTraceArtifactSpan struct {
	ID               string    `json:"id"`
	ParentID         string    `json:"parent_id,omitempty"`
	Type             string    `json:"type"`
	Name             string    `json:"name"`
	Status           string    `json:"status,omitempty"`
	Start            time.Time `json:"start,omitempty"`
	DurationMS       int64     `json:"duration_ms"`
	SelfDurationMS   int64     `json:"self_duration_ms,omitempty"`
	TraceID          string    `json:"trace_id,omitempty"`
	TurnIndex        int       `json:"turn_index,omitempty"`
	ToolName         string    `json:"tool_name,omitempty"`
	ConcurrencyClass string    `json:"concurrency_class,omitempty"`
	Model            string    `json:"model,omitempty"`
	Skill            string    `json:"skill,omitempty"`
}

func runtimeTraceArtifactFromDetail(detail traceDetailResponse, identity buildinfo.Info) RuntimeTraceArtifact {
	return runtimeTraceArtifactFromDetailWithConfiguration(detail, identity, nil)
}

func runtimeTraceArtifactFromDetailWithConfiguration(detail traceDetailResponse, identity buildinfo.Info, configuration *RuntimeTraceConfiguration) RuntimeTraceArtifact {
	detail, selectedRoot := latestRuntimeTraceRun(detail)
	spans := make([]RuntimeTraceArtifactSpan, 0, len(detail.Spans))
	var startedAt time.Time
	status := RuntimeTraceRunStatusUnknown
	runID := detail.SessionID
	if selectedRoot != nil {
		startedAt = selectedRoot.Start
		runID = firstNonEmptyTrace(selectedRoot.TraceID, selectedRoot.ID, runID)
		switch selectedRoot.Status {
		case telemetry.StatusError, telemetry.StatusDenied, telemetry.StatusBlocked:
			status = RuntimeTraceRunStatusFailed
		case telemetry.StatusOK:
			status = RuntimeTraceRunStatusCompleted
		}
	}
	for _, span := range detail.Spans {
		if selectedRoot == nil && (startedAt.IsZero() || (!span.Start.IsZero() && span.Start.Before(startedAt))) {
			startedAt = span.Start
		}
		spans = append(spans, RuntimeTraceArtifactSpan{
			ID:               span.ID,
			ParentID:         span.ParentID,
			Type:             span.Type,
			Name:             span.Name,
			Status:           span.Status,
			Start:            span.Start.UTC(),
			DurationMS:       span.DurationMS,
			SelfDurationMS:   span.SelfDurationMS,
			TraceID:          span.TraceID,
			TurnIndex:        span.TurnIndex,
			ToolName:         span.ToolName,
			ConcurrencyClass: span.ConcurrencyClass,
			Model:            span.Model,
			Skill:            span.Skill,
		})
	}
	diagnostics := append([]RuntimeTraceDiagnostic(nil), detail.Diagnostics...)
	if diagnostics == nil {
		diagnostics = []RuntimeTraceDiagnostic{}
	}
	if spans == nil {
		spans = []RuntimeTraceArtifactSpan{}
	}
	return RuntimeTraceArtifact{
		SchemaVersion: telemetry.SchemaVersionRuntimeTraceV1,
		Configuration: configuration,
		Run: RuntimeTraceRun{
			RunID:        runID,
			Agent:        runtimeTraceAgentName,
			AgentVersion: strings.TrimSpace(identity.Version),
			BuildInfo:    identity,
			SessionID:    detail.SessionID,
			Status:       status,
			StartedAt:    startedAt.UTC(),
			DurationMS:   detail.Summary.TaskWallMS,
		},
		Summary:     detail.Summary,
		Quality:     detail.Quality,
		Diagnostics: diagnostics,
		Spans:       spans,
	}
}

// latestRuntimeTraceRun converts a session-oriented Trace detail into one
// internally consistent run. Interactive and resumed sessions can contain
// several query roots; export intentionally selects the latest query and its
// descendants so run metadata, analysis and spans all describe the same task.
func latestRuntimeTraceRun(detail traceDetailResponse) (traceDetailResponse, *traceSpan) {
	rootIndex := -1
	for i := range detail.Spans {
		span := detail.Spans[i]
		if !strings.EqualFold(span.Type, "query") {
			continue
		}
		if rootIndex < 0 || span.Start.After(detail.Spans[rootIndex].Start) ||
			(span.Start.Equal(detail.Spans[rootIndex].Start) && span.Sequence > detail.Spans[rootIndex].Sequence) {
			rootIndex = i
		}
	}
	if rootIndex < 0 {
		return detail, nil
	}

	root := detail.Spans[rootIndex]
	included := map[string]bool{root.ID: true}
	for changed := true; changed; {
		changed = false
		for _, span := range detail.Spans {
			if span.ID != "" && !included[span.ID] && included[span.ParentID] {
				included[span.ID] = true
				changed = true
			}
		}
	}
	rootEnd := traceSpanEnd(root)
	selected := make([]traceSpan, 0, len(included))
	for _, span := range detail.Spans {
		keep := included[span.ID]
		if !keep && !strings.EqualFold(span.Type, "query") && !strings.EqualFold(span.Type, "api") && !strings.EqualFold(span.Type, "mobile") &&
			root.TraceID != "" && span.TraceID == root.TraceID && traceSpanInside(span, root.Start, rootEnd) {
			keep = true
		}
		if !keep {
			continue
		}
		if span.ID == root.ID || !included[span.ParentID] {
			span.ParentID = ""
		}
		selected = append(selected, span)
	}
	selected = finalizeTraceSpans(selected)
	selectedIDs := make(map[string]bool, len(selected))
	for _, span := range selected {
		selectedIDs[span.ID] = true
	}
	events := make([]traceEvent, 0)
	for _, event := range detail.Events {
		if selectedIDs[event.SpanID] || runtimeTraceEventInside(event, root, rootEnd) {
			events = append(events, event)
		}
	}
	analysis := analyzeTrace(events, selected)
	detail.Summary = analysis.Summary
	detail.Quality = analysis.Quality
	detail.Diagnostics = analysis.Diagnostics
	detail.Spans = nonNilTraceSpans(selected)
	detail.SpanTree = buildTraceSpanTree(selected)
	detail.Events = nonNilTraceEvents(events)
	return detail, &root
}

func traceSpanInside(span traceSpan, start, end time.Time) bool {
	spanEnd := traceSpanEnd(span)
	return !start.IsZero() && !end.IsZero() && !span.Start.Before(start) && !spanEnd.After(end)
}

func runtimeTraceEventInside(event traceEvent, root traceSpan, rootEnd time.Time) bool {
	if event.Time.IsZero() || root.Start.IsZero() || rootEnd.IsZero() || event.Time.Before(root.Start) || event.Time.After(rootEnd) {
		return false
	}
	if event.TraceID == "" || event.TraceID == root.TraceID {
		return true
	}
	return strings.HasPrefix(event.TraceID, "local:")
}

func ExportLocalRuntimeTrace(stores []session.Store, sessionID, outputPath string, configuration RuntimeTraceConfiguration) (RuntimeTraceArtifact, error) {
	if strings.TrimSpace(outputPath) == "" {
		return RuntimeTraceArtifact{}, errors.New("runtime trace output path is required")
	}
	detail, err := buildLocalTraceDetail(stores, sessionID)
	if err != nil {
		return RuntimeTraceArtifact{}, err
	}
	artifact := runtimeTraceArtifactFromDetailWithConfiguration(detail, buildinfo.Current(), &configuration)
	if err := writeRuntimeTraceArtifact(outputPath, artifact); err != nil {
		return RuntimeTraceArtifact{}, err
	}
	return artifact, nil
}

func writeRuntimeTraceArtifact(outputPath string, artifact RuntimeTraceArtifact) error {
	data, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal runtime trace artifact: %w", err)
	}
	data = append(data, '\n')
	directory := filepath.Dir(outputPath)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create runtime trace output directory: %w", err)
	}
	temp, err := os.CreateTemp(directory, ".runtime-trace-*.tmp")
	if err != nil {
		return fmt.Errorf("create runtime trace temp file: %w", err)
	}
	tempPath := temp.Name()
	committed := false
	defer func() {
		_ = temp.Close()
		if !committed {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return fmt.Errorf("chmod runtime trace temp file: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		return fmt.Errorf("write runtime trace artifact: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync runtime trace artifact: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close runtime trace artifact: %w", err)
	}
	if err := os.Rename(tempPath, outputPath); err != nil {
		return fmt.Errorf("replace runtime trace artifact: %w", err)
	}
	committed = true
	if dir, err := os.Open(directory); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}
