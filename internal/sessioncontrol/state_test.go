package sessioncontrol

import (
	"testing"

	"github.com/konglong87/super-niuma/internal/agenttasks"
)

func TestProjectStatus(t *testing.T) {
	tests := []struct {
		name string
		in   SessionStateInput
		want SessionStatus
	}{
		{"archived wins", SessionStateInput{LifecycleStatus: "archived", PermissionRequired: true, Blocked: true, ActiveRunStatus: agenttasks.StatusRunning}, StatusArchived},
		{"permission wins", SessionStateInput{PermissionRequired: true, Blocked: true, ActiveRunStatus: agenttasks.StatusRunning}, StatusWaitingPermission},
		{"user input", SessionStateInput{UserInputRequired: true, ActiveRunStatus: agenttasks.StatusRunning}, StatusWaitingInput},
		{"permission wins user input", SessionStateInput{PermissionRequired: true, UserInputRequired: true, ActiveRunStatus: agenttasks.StatusRunning}, StatusWaitingPermission},
		{"blocked wins", SessionStateInput{Blocked: true, ActiveRunStatus: agenttasks.StatusRunning}, StatusBlocked},
		{"running", SessionStateInput{ActiveRunStatus: agenttasks.StatusRunning}, StatusRunning},
		{"queued run", SessionStateInput{ActiveRunStatus: agenttasks.StatusReady}, StatusQueued},
		{"queued input", SessionStateInput{PendingInputCount: 1}, StatusQueued},
		{"queued input wins terminal run", SessionStateInput{ActiveRunStatus: agenttasks.StatusCompleted, PendingInputCount: 1}, StatusQueued},
		{"completed", SessionStateInput{ActiveRunStatus: agenttasks.StatusCompleted}, StatusCompleted},
		{"terminal ignores user input", SessionStateInput{ActiveRunStatus: agenttasks.StatusCompleted, UserInputRequired: true}, StatusCompleted},
		{"failed", SessionStateInput{ActiveRunStatus: agenttasks.StatusFailed}, StatusFailed},
		{"stopped", SessionStateInput{ActiveRunStatus: agenttasks.StatusCancelled}, StatusStopped},
		{"idle", SessionStateInput{}, StatusIdle},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ProjectStatus(tt.in); got != tt.want {
				t.Fatalf("ProjectStatus(%+v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
