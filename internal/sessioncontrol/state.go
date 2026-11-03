package sessioncontrol

import "github.com/konglong87/super-niuma/internal/agenttasks"

func ProjectStatus(input SessionStateInput) SessionStatus {
	if input.LifecycleStatus == string(StatusArchived) {
		return StatusArchived
	}
	if input.PermissionRequired {
		return StatusWaitingPermission
	}
	if input.UserInputRequired && (input.ActiveRunStatus == agenttasks.StatusRunning || input.ActiveRunStatus == agenttasks.StatusReady) {
		return StatusWaitingInput
	}
	if input.Blocked {
		return StatusBlocked
	}
	switch input.ActiveRunStatus {
	case agenttasks.StatusRunning:
		return StatusRunning
	case agenttasks.StatusReady:
		return StatusQueued
	}
	if input.PendingInputCount > 0 {
		return StatusQueued
	}
	switch input.ActiveRunStatus {
	case agenttasks.StatusCompleted:
		return StatusCompleted
	case agenttasks.StatusFailed:
		return StatusFailed
	case agenttasks.StatusCancelled, agenttasks.StatusTimeout:
		return StatusStopped
	}
	switch input.LifecycleStatus {
	case string(StatusCompleted):
		return StatusCompleted
	case string(StatusFailed):
		return StatusFailed
	case string(StatusStopped):
		return StatusStopped
	}
	return StatusIdle
}
