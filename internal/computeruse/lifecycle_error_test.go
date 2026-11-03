package computeruse

import "testing"

func TestTargetExitedAfterLaunchIsStableAndNonTransient(t *testing.T) {
	if got := PublicErrorCode(ErrorCodeTargetExitedAfterLaunch); got != ErrorCodeTargetExitedAfterLaunch {
		t.Fatalf("public error code = %q, want %q", got, ErrorCodeTargetExitedAfterLaunch)
	}
	if transientFailureCode(ErrorCodeTargetExitedAfterLaunch) {
		t.Fatal("target exit after launch must not be treated as a transient re-observe condition")
	}
}
