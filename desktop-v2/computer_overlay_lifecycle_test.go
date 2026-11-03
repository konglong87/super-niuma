package main

import (
	"context"
	"testing"
)

func TestDefaultStartupDoesNotCreateNativeComputerPanel(t *testing.T) {
	if nativeComputerPanelStartupEnabled {
		t.Fatal("native computer panel must remain opt-in by default")
	}

	application := &app{}
	application.startComputerPanelIfEnabled(context.Background())

	if application.computerPanel != nil {
		t.Fatal("default startup created a native computer panel")
	}
	if application.computerPanelDone != nil {
		t.Fatal("default startup created a native computer panel lifecycle")
	}
}
