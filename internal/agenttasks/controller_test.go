package agenttasks

import (
	"context"
	"testing"
)

func TestControllerCancel(t *testing.T) {
	controller := NewController()
	ctx, cancel := context.WithCancel(context.Background())
	controller.Register(7, cancel)
	if !controller.Active(7) {
		t.Fatal("Active returned false for registered task")
	}
	if !controller.Cancel(7) {
		t.Fatal("Cancel returned false")
	}
	if ctx.Err() != context.Canceled {
		t.Fatalf("ctx err = %v", ctx.Err())
	}
	if controller.Active(7) {
		t.Fatal("Active returned true after cancel")
	}
	if controller.Cancel(7) {
		t.Fatal("Cancel returned true after first cancel")
	}
	_, cancel = context.WithCancel(context.Background())
	controller.Register(7, cancel)
	controller.Unregister(7)
	if controller.Active(7) {
		t.Fatal("Active returned true after unregister")
	}
	if controller.Cancel(7) {
		t.Fatal("Cancel returned true after unregister")
	}
}
