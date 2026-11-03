package main

import (
	"os"
	"strings"
	"testing"
)

func TestComputerPointerNativeOverlayIsClickThroughAndCaptureExcluded(t *testing.T) {
	source, err := os.ReadFile("computer_pointer_darwin.m")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, required := range []string{
		"NSWindowStyleMaskNonactivatingPanel",
		"ignoresMouseEvents = YES",
		"canBecomeKeyWindow",
		"canBecomeMainWindow",
		"NSWindowSharingNone",
		"NSWindowCollectionBehaviorCanJoinAllSpaces",
		"NSWindowCollectionBehaviorFullScreenAuxiliary",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("native pointer overlay missing safety property %q", required)
		}
	}
}
