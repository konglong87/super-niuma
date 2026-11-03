package execution

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestAuditReferenceDoesNotExposeFencingCapability(t *testing.T) {
	var log bytes.Buffer
	token := "private-claim-token-must-not-appear"
	state := attemptState{request: Request{RunID: "run", JobID: "job", AttemptID: token}, logger: slog.New(slog.NewJSONHandler(&log, nil))}
	state.log("fixture", "ok")
	if strings.Contains(log.String(), token) || !strings.Contains(log.String(), "attempt_ref") {
		t.Fatalf("unsafe attempt audit: %s", log.String())
	}
}
