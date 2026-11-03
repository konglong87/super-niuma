package main

import (
	"context"
	"sync"
	"testing"
	"time"

	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

type recordingComputerPointer struct {
	mu     sync.Mutex
	shown  []cu.ActionVisualCue
	hidden int
	closed int
	showCh chan cu.ActionVisualCue
}

func (p *recordingComputerPointer) Show(cue cu.ActionVisualCue) {
	p.mu.Lock()
	p.shown = append(p.shown, cue)
	p.mu.Unlock()
	select {
	case p.showCh <- cue:
	default:
	}
}
func (p *recordingComputerPointer) Hide() {
	p.mu.Lock()
	p.hidden++
	p.mu.Unlock()
}
func (p *recordingComputerPointer) Close() {
	p.mu.Lock()
	p.closed++
	p.mu.Unlock()
}

func TestComputerVisualCueOverlayShowsCueAndCleansUp(t *testing.T) {
	bus := cu.NewVisualCueBus()
	pointer := &recordingComputerPointer{showCh: make(chan cu.ActionVisualCue, 1)}
	stop := startComputerVisualCueOverlayWithPointer(context.Background(), bus, pointer)

	now := time.Now()
	point := cu.ScreenPoint{X: 140, Y: 220}
	bus.PublishVisualCue(cu.ActionVisualCue{
		SessionID: "session", ActionID: "action", ActionKind: cu.ActionClick,
		Phase: cu.VisualCueDispatched, DispatchState: cu.DispatchComplete,
		WindowID: "window-1", GlobalPoint: &point, CreatedAt: now, ExpiresAt: now.Add(500 * time.Millisecond),
	})
	select {
	case got := <-pointer.showCh:
		if got.GlobalPoint == nil || *got.GlobalPoint != point {
			t.Fatalf("shown cue = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("visual cue was not shown")
	}

	stop()
	pointer.mu.Lock()
	defer pointer.mu.Unlock()
	if pointer.hidden == 0 || pointer.closed != 1 {
		t.Fatalf("cleanup hidden=%d closed=%d, want hidden and one close", pointer.hidden, pointer.closed)
	}
}

func TestComputerVisualCueOverlayIgnoresImageOnlyCue(t *testing.T) {
	bus := cu.NewVisualCueBus()
	pointer := &recordingComputerPointer{showCh: make(chan cu.ActionVisualCue, 1)}
	stop := startComputerVisualCueOverlayWithPointer(context.Background(), bus, pointer)
	defer stop()

	now := time.Now()
	imagePoint := cu.Point{X: 10, Y: 20}
	bus.PublishVisualCue(cu.ActionVisualCue{
		SessionID: "session", ActionID: "action", ActionKind: cu.ActionClick,
		WindowID: "window-1", Phase: cu.VisualCueDispatched, DispatchState: cu.DispatchComplete,
		ImagePoint: &imagePoint, CreatedAt: now, ExpiresAt: now.Add(time.Second),
	})
	select {
	case got := <-pointer.showCh:
		t.Fatalf("image-only cue was shown without native global point: %+v", got)
	case <-time.After(50 * time.Millisecond):
	}
}
