package main

import (
	"context"
	"testing"

	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

func TestOverlayManualCommandsSurviveRefresh(t *testing.T) {
	s := computerOverlayState{}
	prefs := ComputerPanelSnapshot{DisplayMode: computerOverlayModeAuto, Language: "zh"}
	s.configure(prefs)
	active := ComputerPanelSnapshot{SessionID: "one", Visible: true, ImageData: "image"}
	s.update(active)
	if !s.render().Expanded {
		t.Fatal("auto session should expand")
	}
	if !s.command(computerPanelCommand{Kind: computerPanelCommandCollapse, SessionID: "one"}) {
		t.Fatal("collapse ignored")
	}
	s.configure(prefs)
	s.update(active)
	if s.render().Expanded {
		t.Fatal("poll overwrote collapse")
	}
	s.command(computerPanelCommand{Kind: computerPanelCommandExpand, SessionID: "one"})
	s.update(active)
	if !s.render().Expanded {
		t.Fatal("expand ignored")
	}
	s.command(computerPanelCommand{Kind: computerPanelCommandDismiss, SessionID: "one"})
	s.configure(prefs)
	s.update(active)
	if s.render().Visible || s.render().ImageData != "" {
		t.Fatal("poll undid hide or retained hidden image")
	}
	s.show()
	if !s.render().Visible || !s.render().Expanded {
		t.Fatal("reopen failed")
	}
}

func TestOverlayStaleCommandsAndSessionReset(t *testing.T) {
	s := computerOverlayState{}
	s.configure(ComputerPanelSnapshot{DisplayMode: computerOverlayModeCompact})
	s.update(ComputerPanelSnapshot{SessionID: "one", Visible: true})
	if s.command(computerPanelCommand{Kind: computerPanelCommandDismiss, SessionID: "old"}) {
		t.Fatal("stale command accepted")
	}
	s.command(computerPanelCommand{Kind: computerPanelCommandDismiss, SessionID: "one"})
	s.update(ComputerPanelSnapshot{SessionID: "two", Visible: true})
	if !s.render().Visible || s.render().Expanded {
		t.Fatal("new session failed to reset presentation")
	}
	s.configure(ComputerPanelSnapshot{DisplayMode: computerOverlayModeExpanded})
	if !s.render().Expanded {
		t.Fatal("display mode not applied")
	}
	s.update(ComputerPanelSnapshot{})
	if s.render().Visible {
		t.Fatal("idle overlay visible")
	}
}

func TestOverlayFriendlyCopyAndManagedControls(t *testing.T) {
	session := ComputerSessionDTO{ID: "one", State: cu.SessionReady, OwnerKind: "managed_conversation", Capabilities: cu.Capabilities{SupportsStop: true, SupportsPause: true}}
	snapshot := computerOverlaySnapshot(session, "zh", ComputerObservationDTO{})
	if snapshot.Title != "电脑操作" || snapshot.Detail != "已就绪，等待下一步操作" {
		t.Fatalf("unlocalized UI: %+v", snapshot)
	}
	if !snapshot.CanStop || snapshot.CanPause || snapshot.CanResume {
		t.Fatal("managed session has wrong controls")
	}
	session.OwnerKind = "local_preview"
	if !computerOverlaySnapshot(session, "en", ComputerObservationDTO{}).CanPause {
		t.Fatal("local pause missing")
	}
	session.State = cu.SessionPaused
	if !computerOverlaySnapshot(session, "en", ComputerObservationDTO{}).CanResume {
		t.Fatal("local resume missing")
	}
	session.State = cu.SessionStopped
	stopped := computerOverlaySnapshot(session, "en", ComputerObservationDTO{ImageData: "stale"})
	if stopped.Visible || stopped.ImageData != "" || stopped.CanStop {
		t.Fatal("stopped image/control retained")
	}
}

type fakeOverlayPanel struct {
	snapshots []ComputerPanelSnapshot
	commands  []*computerPanelCommand
}

func (p *fakeOverlayPanel) Update(s ComputerPanelSnapshot) { p.snapshots = append(p.snapshots, s) }
func (p *fakeOverlayPanel) Poll() *computerPanelCommand {
	if len(p.commands) == 0 {
		return nil
	}
	c := p.commands[0]
	p.commands = p.commands[1:]
	return c
}
func (*fakeOverlayPanel) Close() {}

func TestOverlayHostDiscoveryAndCommandsWithoutWebview(t *testing.T) {
	ctx := context.Background()
	manager, _ := testComputerManager()
	session, err := manager.start(ctx, ComputerSessionStartInput{Approved: true})
	if err != nil {
		t.Fatal(err)
	}
	panel := &fakeOverlayPanel{}
	a := &app{computerManager: manager, computerPanel: panel}
	a.refreshComputerPanel(ctx)
	if !a.IsComputerOverlayAvailable() || len(panel.snapshots) == 0 || !panel.snapshots[len(panel.snapshots)-1].Visible {
		t.Fatal("host did not discover session")
	}
	for _, kind := range []string{computerPanelCommandCollapse, computerPanelCommandExpand, computerPanelCommandDismiss} {
		panel.commands = append(panel.commands, &computerPanelCommand{Kind: kind, SessionID: session.ID})
		a.handleComputerPanelCommand(ctx)
		a.refreshComputerPanel(ctx)
		got := panel.snapshots[len(panel.snapshots)-1]
		if kind == computerPanelCommandCollapse && got.Expanded {
			t.Fatal("collapse lost")
		}
		if kind == computerPanelCommandExpand && !got.Expanded {
			t.Fatal("expand ignored")
		}
		if kind == computerPanelCommandDismiss && got.Visible {
			t.Fatal("dismiss ignored")
		}
	}
	a.ShowComputerOverlay()
	if !panel.snapshots[len(panel.snapshots)-1].Visible {
		t.Fatal("show ignored")
	}
	_, err = manager.control(ctx, session.ID, cu.ActionStop)
	if err != nil {
		t.Fatal(err)
	}
	a.refreshComputerPanel(ctx)
	if panel.snapshots[len(panel.snapshots)-1].Visible {
		t.Fatal("stopped native panel remains")
	}
}

func TestOverlayControlFailureIsVisibleAndSessionScoped(t *testing.T) {
	s := computerOverlayState{}
	s.configure(ComputerPanelSnapshot{Language: "zh"})
	s.update(ComputerPanelSnapshot{SessionID: "one", Visible: true, Detail: "ready"})
	s.controlFailed = true
	if s.render().Detail != "控制未成功，请在工作区检查后重试" {
		t.Fatal("failed control was silent")
	}
	s.update(ComputerPanelSnapshot{SessionID: "two", Visible: true, Detail: "new session"})
	if s.render().Detail != "new session" {
		t.Fatal("control error leaked into new session")
	}
}
