package computeruse

import (
	"errors"
	"time"
)

// TargetBinding is the host-owned identity of the window an execution run is
// allowed to control. It is intentionally independent of any application UI;
// every platform backend revalidates this identity before capture or input.
type TargetBinding struct {
	Window     WindowRef `json:"window"`
	Generation uint64    `json:"generation"`
	BoundAt    time.Time `json:"bound_at"`
}

func NewTargetBinding(window WindowRef, generation uint64, boundAt time.Time) (TargetBinding, error) {
	if window.ID == "" || window.OwnerPID <= 0 || window.BundleID == "" || window.Frame == nil ||
		window.Frame.Width <= 0 || window.Frame.Height <= 0 || !window.IsVisible {
		return TargetBinding{}, errors.New("invalid target window")
	}
	if boundAt.IsZero() {
		boundAt = time.Now()
	}
	return TargetBinding{Window: window, Generation: generation, BoundAt: boundAt}, nil
}

func (b TargetBinding) Valid() bool {
	return b.Window.ID != "" && b.Window.OwnerPID > 0 && b.Window.BundleID != "" &&
		b.Window.Frame != nil && b.Window.Frame.Width > 0 && b.Window.Frame.Height > 0 && b.Window.IsVisible &&
		!b.BoundAt.IsZero()
}

// Matches requires the immutable Window Server identity to remain stable: id,
// owner PID, and bundle ID. Frame and visibility can shift by a few pixels or
// briefly toggle during launch animation/retina scaling, so they are not
// exact-match identity gates. Frontmost is also not part of identity: focus is
// checked separately immediately before input. The caller still requires a
// non-nil, non-zero frame separately.
func (b TargetBinding) Matches(window WindowRef) bool {
	if !b.Valid() || window.ID == "" {
		return false
	}
	return b.Window.ID == window.ID && b.Window.OwnerPID == window.OwnerPID &&
		b.Window.BundleID == window.BundleID
}
