package sessiondiag

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/session"
)

type Confidence string

const (
	ConfidenceExact     Confidence = "exact"
	ConfidenceHeuristic Confidence = "heuristic"
	ConfidenceNone      Confidence = "none"
)

type Options struct {
	SessionID     string
	Store         session.Store
	LogPaths      []string
	WindowPadding time.Duration
}

type Report struct {
	SessionID     string     `json:"session_id"`
	SessionFile   string     `json:"session_file"`
	LogFiles      []string   `json:"log_files,omitempty"`
	CWD           string     `json:"cwd,omitempty"`
	StartedAt     time.Time  `json:"started_at,omitempty"`
	EndedAt       time.Time  `json:"ended_at,omitempty"`
	Models        []string   `json:"models,omitempty"`
	Requests      []Request  `json:"requests,omitempty"`
	FallbackCount int        `json:"fallback_count"`
	ErrorCount    int        `json:"error_count"`
	Confidence    Confidence `json:"confidence"`
	Warnings      []string   `json:"warnings,omitempty"`
}

type Request struct {
	StartedAt     time.Time         `json:"started_at,omitempty"`
	Purpose       string            `json:"purpose,omitempty"`
	Model         string            `json:"model,omitempty"`
	Status        string            `json:"status,omitempty"`
	FinalProvider string            `json:"final_provider,omitempty"`
	Attempts      []ProviderAttempt `json:"attempts,omitempty"`
}

type ProviderAttempt struct {
	ProviderName string `json:"provider_name,omitempty"`
	ProviderRole string `json:"provider_role,omitempty"`
	ProviderKind string `json:"provider_kind,omitempty"`
	Endpoint     string `json:"endpoint,omitempty"`
	Status       string `json:"status,omitempty"`
	Error        string `json:"error,omitempty"`
}

type logEvent struct {
	Time           logTime        `json:"time"`
	EventName      string         `json:"event_name"`
	Action         string         `json:"action"`
	Status         string         `json:"status"`
	CLISessionID   string         `json:"cli_session_id"`
	RequestPurpose string         `json:"request_purpose"`
	Model          string         `json:"model"`
	CWD            string         `json:"cwd"`
	ResourceID     string         `json:"resource_id"`
	Error          string         `json:"error"`
	Properties     map[string]any `json:"properties"`
}

type logTime struct{ time.Time }

func (t *logTime) UnmarshalJSON(data []byte) error {
	value := strings.Trim(string(data), `"`)
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.000-0700", "2006-01-02T15:04:05-0700"} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			t.Time = parsed
			return nil
		}
	}
	return fmt.Errorf("invalid log time %q", value)
}

func Inspect(opts Options) (Report, error) {
	opts.SessionID = strings.TrimSpace(opts.SessionID)
	if opts.SessionID == "" {
		return Report{}, errors.New("session id is required")
	}
	summary, ok, err := opts.Store.Find(opts.SessionID)
	if err != nil {
		return Report{}, err
	}
	if !ok {
		return Report{}, fmt.Errorf("session not found: %s", opts.SessionID)
	}
	entries, err := session.Load(summary.Path)
	if err != nil {
		return Report{}, err
	}
	report := Report{SessionID: opts.SessionID, SessionFile: summary.Path, Confidence: ConfidenceNone}
	models := map[string]struct{}{}
	for _, entry := range entries {
		if !entry.Timestamp.IsZero() && (report.StartedAt.IsZero() || entry.Timestamp.Before(report.StartedAt)) {
			report.StartedAt = entry.Timestamp
		}
		if entry.Timestamp.After(report.EndedAt) {
			report.EndedAt = entry.Timestamp
		}
		if model := strings.TrimSpace(entry.Model); model != "" {
			models[model] = struct{}{}
		}
	}
	for model := range models {
		report.Models = append(report.Models, model)
	}
	sort.Strings(report.Models)
	padding := opts.WindowPadding
	if padding <= 0 {
		padding = 5 * time.Second
	}
	var exact, heuristic []logEvent
	for _, path := range opts.LogPaths {
		events, warnings := readLog(path)
		report.Warnings = append(report.Warnings, warnings...)
		if len(events) > 0 {
			report.LogFiles = append(report.LogFiles, path)
		}
		for _, event := range events {
			if event.CLISessionID == opts.SessionID {
				exact = append(exact, event)
				continue
			}
			if event.CLISessionID != "" || report.StartedAt.IsZero() || event.Time.Time.IsZero() {
				continue
			}
			if !event.Time.Time.Before(report.StartedAt.Add(-padding)) && !event.Time.Time.After(report.EndedAt.Add(padding)) && modelMatches(event.Model, models) {
				heuristic = append(heuristic, event)
			}
		}
	}
	events := exact
	if len(exact) > 0 {
		report.Confidence = ConfidenceExact
	} else if len(heuristic) > 0 {
		events = heuristic
		report.Confidence = ConfidenceHeuristic
		report.Warnings = append(report.Warnings, "legacy logs lack cli_session_id; requests were correlated by timestamp and model")
	} else {
		report.Warnings = append(report.Warnings, "no matching model request logs found")
	}
	sort.Slice(events, func(i, j int) bool { return events[i].Time.Time.Before(events[j].Time.Time) })
	aggregate(&report, events)
	return report, nil
}

func readLog(path string) ([]logEvent, []string) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []string{fmt.Sprintf("read log %s: %v", path, err)}
	}
	defer file.Close()
	var events []logEvent
	var warnings []string
	malformed := 0
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		var event logEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			malformed++
			continue
		}
		if event.EventName == "" {
			event.EventName = event.Action
		}
		if strings.HasPrefix(event.EventName, "model.") || strings.HasPrefix(event.EventName, "session.recap.") || event.EventName == "query.run.start" {
			events = append(events, event)
		}
	}
	if err := scanner.Err(); err != nil {
		warnings = append(warnings, fmt.Sprintf("scan log %s: %v", path, err))
	}
	if malformed > 0 {
		warnings = append(warnings, fmt.Sprintf("ignored %d malformed log lines in %s", malformed, path))
	}
	return events, warnings
}

func aggregate(report *Report, events []logEvent) {
	var current *Request
	pendingPurpose := ""
	finish := func() {
		if current == nil {
			return
		}
		if len(current.Attempts) > 0 {
			current.FinalProvider = current.Attempts[len(current.Attempts)-1].ProviderName
		}
		if len(current.Attempts) > 1 {
			report.FallbackCount += len(current.Attempts) - 1
		}
		current = nil
	}
	for _, event := range events {
		switch event.EventName {
		case "session.recap.started":
			if mode := stringProperty(event.Properties, "mode"); mode != "" {
				pendingPurpose = "recap." + strings.ReplaceAll(mode, "-", "_")
			}
		case "query.run.start":
			if report.CWD == "" {
				report.CWD = event.CWD
			}
		case "model.request.started":
			finish()
			purpose := event.RequestPurpose
			if purpose == "" {
				purpose = "main"
			}
			report.Requests = append(report.Requests, Request{StartedAt: event.Time.Time, Purpose: purpose, Model: event.Model, Status: event.Status})
			current = &report.Requests[len(report.Requests)-1]
		case "model.phase.request.build.finished":
			if current == nil {
				purpose := event.RequestPurpose
				if purpose == "" {
					purpose = pendingPurpose
				}
				report.Requests = append(report.Requests, Request{StartedAt: event.Time.Time, Purpose: purpose, Model: event.Model})
				current = &report.Requests[len(report.Requests)-1]
				pendingPurpose = ""
			}
			current.Attempts = append(current.Attempts, ProviderAttempt{
				ProviderName: stringProperty(event.Properties, "provider_name", "provider"),
				ProviderRole: stringProperty(event.Properties, "provider_role"),
				ProviderKind: stringProperty(event.Properties, "provider_kind"),
				Endpoint:     stringProperty(event.Properties, "endpoint"),
				Status:       event.Status,
			})
		case "model.phase.stream.create.finished":
			if current != nil && len(current.Attempts) > 0 {
				attempt := &current.Attempts[len(current.Attempts)-1]
				attempt.Status = event.Status
				if event.Error != "" {
					attempt.Error = "request failed"
				}
				if event.Status == "error" {
					report.ErrorCount++
				}
			}
		case "model.request.finished":
			if current != nil {
				current.Status = event.Status
			}
			finish()
		}
	}
	finish()
}

func stringProperty(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func modelMatches(model string, models map[string]struct{}) bool {
	if len(models) == 0 || strings.TrimSpace(model) == "" {
		return true
	}
	_, ok := models[strings.TrimSpace(model)]
	return ok
}
