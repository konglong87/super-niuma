package sqlitestore

import (
	"errors"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/super/control"
)

// The active-run query skips cancelled history, so a cancelled root must never
// keep a notice in flight.
func TestCancelledRootCannotKeepSendingNotice(t *testing.T) {
	now := time.Now().UnixMilli()
	target := control.Target{ID: "desktop", Provider: control.ProviderDesktop}
	sending := control.Notice{ID: "notice-1", Target: target, Status: control.DeliverySending, Attempts: 1, Claim: control.Claim{Scope: "local", RunID: "run", JobID: "super.delivery.desktop", Owner: "w", Token: "t", Epoch: 1, UntilMS: now + 60000}}
	old := control.Run{Status: control.Cancelling, Grant: control.Grant{Targets: []control.Target{target}}, Notices: []control.Notice{sending}}
	next := old
	next.Status = control.Cancelled
	if err := validateControlNotices(old, next, now); !errors.Is(err, control.ErrDenied) {
		t.Fatalf("cancelled root kept an in-flight notice: %v", err)
	}
	next.Status = control.Cancelling
	if err := validateControlNotices(old, next, now); err != nil {
		t.Fatalf("cancelling root may wait for its sender: %v", err)
	}
}
