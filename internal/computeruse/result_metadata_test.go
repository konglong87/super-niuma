package computeruse

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResultMetadataPreservesBoundIdentityWithoutPrivateFields(t *testing.T) {
	output := `{"launch_receipt":{"target_id":"fixture","bundle_id":"fixture.app","outcome":"executed","window":{"id":"42","title":"PRIVATE DOCUMENT","owner_pid":7,"bundle_id":"fixture.app","frame":{"x":1,"y":2,"width":3,"height":4}}},"observation":{"window_id":"42","active_window":{"id":"42","title":"PRIVATE DOCUMENT","owner_pid":7,"bundle_id":"fixture.app"}},"private":"SECRET"}`
	meta := ResultMetadata(output)
	if meta["window_id"] != "42" || meta["bundle_id"] != "fixture.app" || meta["launch_receipt"] == nil {
		t.Fatalf("missing identity: %#v", meta)
	}
	data, _ := json.Marshal(meta)
	if strings.Contains(string(data), "PRIVATE DOCUMENT") || strings.Contains(string(data), "SECRET") {
		t.Fatalf("private data leaked: %s", data)
	}
}

func TestResultMetadataRetainsSanitizedActionReceipt(t *testing.T) {
	output := `{"receipt":{"action_id":"action-1","session_id":"session-1","outcome":"executed","dispatch_state":"complete","verification":"unknown","before_observation_id":"obs-before","after_observation_id":"obs-after","duration":123,"completed_at":"2026-10-08T05:11:07.39121Z","error_code":"screenshot_failed","error_message":"SECRET ERROR","redacted_action_summary":"SECRET INPUT","environment_fingerprint":"SECRET ENV","before":{"id":"SECRET MEDIA"},"active_window_after":{"id":"42","owner_pid":7,"bundle_id":"fixture.app","title":"SECRET TITLE"}}}`
	meta := ResultMetadata(output)
	receipt, ok := meta["receipt"].(ActionReceipt)
	if !ok || receipt.ActionID != "action-1" || receipt.SessionID != "session-1" || receipt.DispatchState != DispatchComplete || receipt.AfterObservationID != "obs-after" || receipt.CompletedAt.IsZero() {
		t.Fatalf("missing action receipt: %#v", meta)
	}
	data, _ := json.Marshal(meta)
	if strings.Contains(string(data), "SECRET") {
		t.Fatalf("private receipt fields leaked: %s", data)
	}
}

func TestResultMetadataDoesNotInventErrorForSuccessfulReceipt(t *testing.T) {
	meta := ResultMetadata(`{"receipt":{"action_id":"a","session_id":"s","outcome":"executed","dispatch_state":"complete"}}`)
	r := meta["receipt"].(ActionReceipt)
	if r.ErrorCode != "" || meta["error_code"] != nil {
		t.Fatalf("success gained an error: %#v", meta)
	}
}
