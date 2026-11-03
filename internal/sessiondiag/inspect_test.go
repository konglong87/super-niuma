package sessiondiag

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/session"
)

func TestInspectCorrelatesExactSessionAndFallback(t *testing.T) {
	const sessionID = "11111111-1111-4111-8111-111111111111"
	root := t.TempDir()
	transcript := filepath.Join(root, "projects", "project", sessionID+".jsonl")
	writeFile(t, transcript, `{"timestamp":"2026-07-15T04:00:00Z","type":"message","role":"user","content":"secret"}
{"timestamp":"2026-07-15T04:00:03Z","type":"usage","model":"deepseek"}
`)
	logPath := filepath.Join(root, "debug", "niuma-tui.log")
	writeFile(t, logPath, fmt.Sprintf(`{"time":"2026-07-15T12:00:01+08:00","event_name":"model.request.started","status":"started","cli_session_id":"%[1]s","request_purpose":"main","model":"deepseek"}
{"time":"2026-07-15T12:00:01+08:00","event_name":"model.phase.request.build.finished","status":"ok","cli_session_id":"%[1]s","request_purpose":"main","model":"deepseek","properties":{"provider_name":"primary","provider_role":"primary","provider_kind":"custom","endpoint":"https://primary.test/v1"}}
{"time":"2026-07-15T12:00:02+08:00","event_name":"model.phase.stream.create.finished","status":"error","cli_session_id":"%[1]s","request_purpose":"main","model":"deepseek","error":"403","properties":{"provider_name":"primary"}}
{"time":"2026-07-15T12:00:02+08:00","event_name":"model.phase.request.build.finished","status":"ok","cli_session_id":"%[1]s","request_purpose":"main","model":"deepseek","properties":{"provider_name":"selected","provider_role":"fallback","provider_kind":"custom","endpoint":"https://selected.test/v1"}}
{"time":"2026-07-15T12:00:03+08:00","event_name":"model.phase.stream.create.finished","status":"ok","cli_session_id":"%[1]s","request_purpose":"main","model":"deepseek","properties":{"provider_name":"selected"}}
{"time":"2026-07-15T12:00:03+08:00","event_name":"model.request.finished","status":"ok","cli_session_id":"%[1]s","request_purpose":"main","model":"deepseek"}
`, sessionID))
	store := session.Store{TranscriptProjectsRoot: filepath.Join(root, "projects")}
	report, err := Inspect(Options{SessionID: sessionID, Store: store, LogPaths: []string{logPath}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Confidence != ConfidenceExact || len(report.Requests) != 1 || report.FallbackCount != 1 || report.ErrorCount != 1 {
		t.Fatalf("report = %+v", report)
	}
	request := report.Requests[0]
	if request.FinalProvider != "selected" || len(request.Attempts) != 2 || request.Attempts[1].Endpoint != "https://selected.test/v1" {
		t.Fatalf("request = %+v", request)
	}
}

func TestInspectMarksLegacyLogsHeuristic(t *testing.T) {
	const sessionID = "22222222-2222-4222-8222-222222222222"
	root := t.TempDir()
	transcript := filepath.Join(root, "projects", "project", sessionID+".jsonl")
	writeFile(t, transcript, `{"timestamp":"2026-07-15T04:00:00Z","type":"usage","model":"m"}
`)
	logPath := filepath.Join(root, "tui.log")
	writeFile(t, logPath, `{"time":"2026-07-15T12:00:01+08:00","event_name":"model.request.started","status":"started","model":"m"}
{"time":"2026-07-15T12:00:02+08:00","event_name":"model.request.finished","status":"ok","model":"m"}
`)
	report, err := Inspect(Options{SessionID: sessionID, Store: session.Store{TranscriptProjectsRoot: filepath.Join(root, "projects")}, LogPaths: []string{logPath}, WindowPadding: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if report.Confidence != ConfidenceHeuristic || len(report.Warnings) == 0 {
		t.Fatalf("report = %+v", report)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
