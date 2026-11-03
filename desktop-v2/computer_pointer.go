package main

import (
	"context"
	"sync"
	"time"

	"github.com/konglong87/super-niuma/internal/computerbridge"
	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

// computerPointer is deliberately narrower than the Computer Use authority.
// It can only render/hide a visual cue and never posts input or changes a turn.
type computerPointer interface {
	Show(cu.ActionVisualCue)
	Hide()
	Close()
}

func startRemoteComputerVisualCueOverlay(ctx context.Context, client *computerbridge.Client, bus *cu.VisualCueBus) func() {
	if client == nil || bus == nil {
		return func() {}
	}
	pollCtx, cancel := context.WithCancel(ctx)
	localStop := startComputerVisualCueOverlay(pollCtx, bus)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		last := ""
		poll := func() {
			requestCtx, requestCancel := context.WithTimeout(pollCtx, 250*time.Millisecond)
			defer requestCancel()
			status, err := client.RuntimeStatus(requestCtx, computerRuntimeSupervisionOwner)
			if err != nil || !status.ActiveSession || status.SessionID == "" {
				return
			}
			response, err := client.VisualCue(requestCtx, computerRuntimeSupervisionOwner, status.SessionID)
			if err != nil || response.Cue == nil {
				return
			}
			cue := *response.Cue
			key := cue.SessionID + "\x00" + cue.ActionID + "\x00" + string(cue.Phase) + "\x00" + cue.CreatedAt.UTC().Format(time.RFC3339Nano)
			if key == last {
				return
			}
			last = key
			bus.PublishVisualCue(cue)
		}
		poll()
		for {
			select {
			case <-pollCtx.Done():
				return
			case <-ticker.C:
				poll()
			}
		}
	}()
	return func() {
		cancel()
		localStop()
		<-done
	}
}

func startComputerVisualCueOverlay(ctx context.Context, bus *cu.VisualCueBus) func() {
	return startComputerVisualCueOverlayWithPointer(ctx, bus, newNativeComputerPointer())
}

func startComputerVisualCueOverlayWithPointer(ctx context.Context, bus *cu.VisualCueBus, pointer computerPointer) func() {
	if ctx == nil {
		ctx = context.Background()
	}
	if bus == nil || pointer == nil {
		return func() {}
	}
	cues, cancel := bus.SubscribeVisualCues(8)
	stop := make(chan struct{})
	done := make(chan struct{})
	var stopOnce sync.Once
	go func() {
		defer close(done)
		defer cancel()
		defer pointer.Hide()
		defer pointer.Close()

		var timer *time.Timer
		var timerC <-chan time.Time
		resetTimer := func(deadline time.Time) {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				pointer.Hide()
				timerC = nil
				return
			}
			if timer == nil {
				timer = time.NewTimer(remaining)
			} else {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(remaining)
			}
			timerC = timer.C
		}
		stopTimer := func() {
			if timer == nil {
				return
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timerC = nil
		}
		defer stopTimer()

		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-timerC:
				pointer.Hide()
				timerC = nil
			case cue, ok := <-cues:
				if !ok {
					return
				}
				if cue.Phase == cu.VisualCueHidden {
					pointer.Hide()
					timerC = nil
					continue
				}
				// Display-level captures cannot reliably exclude a separate overlay
				// window. Keep the marker off unless the authoritative turn is bound
				// to a concrete target window; window captures remain isolated.
				if cue.WindowID == "" || cue.GlobalPoint == nil || !cue.ExpiresAt.After(time.Now()) {
					continue
				}
				pointer.Show(cue)
				resetTimer(cue.ExpiresAt)
			}
		}
	}()
	return func() {
		stopOnce.Do(func() { close(stop) })
		<-done
	}
}
