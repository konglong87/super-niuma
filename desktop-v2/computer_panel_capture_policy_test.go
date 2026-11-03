package main

import (
	"os"
	"strings"
	"testing"
)

// Guard the production source policy: screenshot visibility must not regress
// into an acceptance-only flag. Real screenshot acceptance remains separate.
func TestComputerPanelIsCaptureVisibleWithoutAcceptanceFlags(t *testing.T) {
	source, err := os.ReadFile("computer_panel_darwin.m")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	if !strings.Contains(text, "_panel.sharingType = NSWindowSharingReadOnly;") {
		t.Fatal("native panel must allow normal desktop screenshots")
	}
	for _, forbidden := range []string{"NSWindowSharingNone", "NIUMA_PANEL_ACCEPTANCE", "NIUMA_PANEL_CAPTURE_EVIDENCE"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("capture policy still depends on %s", forbidden)
		}
	}
	if _, err := os.Stat("computer_panel_acceptance.go"); !os.IsNotExist(err) {
		t.Fatal("obsolete acceptance-only capture switch must not remain")
	}
}
