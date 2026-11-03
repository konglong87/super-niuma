package computerbridge

import (
	"testing"
	"time"

	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

func TestVisualCueRequestAndResponseValidation(t *testing.T) {
	owner := cu.SessionOwner{TenantID: 1, UserID: 1, SessionID: 1}
	request := Request{Op: OpVisualCue, Owner: owner, SessionID: "session-1"}
	if !validRequest(request) {
		t.Fatal("valid visual cue request rejected")
	}
	if validRequest(Request{Op: OpVisualCue, Owner: owner}) {
		t.Fatal("visual cue request without session accepted")
	}
	point := cu.ScreenPoint{X: 1, Y: 2}
	now := time.Now()
	response := VisualCueResponse{Cue: &cu.ActionVisualCue{SessionID: "session-1", ActionID: "action-1", ActionKind: cu.ActionClick, Phase: cu.VisualCueDispatched, DispatchState: cu.DispatchComplete, WindowID: "window-1", GlobalPoint: &point, CreatedAt: now, ExpiresAt: now.Add(time.Second)}}
	if !validVisualCueResponse(response, "session-1") {
		t.Fatal("valid visual cue response rejected")
	}
	response.Cue.SessionID = "other"
	if validVisualCueResponse(response, "session-1") {
		t.Fatal("cross-session visual cue response accepted")
	}
	response.Cue = &cu.ActionVisualCue{SessionID: "session-1", ActionID: "runtime", Phase: cu.VisualCueHidden, CreatedAt: now, ExpiresAt: now.Add(time.Millisecond)}
	if !validVisualCueResponse(response, "session-1") {
		t.Fatal("hidden visual cue response rejected")
	}
}
