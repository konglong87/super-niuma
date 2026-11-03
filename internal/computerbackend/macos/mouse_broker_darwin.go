//go:build darwin && cgo

package macos

/*
#cgo LDFLAGS: -framework CoreGraphics -framework ApplicationServices
#include <CoreGraphics/CoreGraphics.h>
#include <ApplicationServices/ApplicationServices.h>

static bool brokerPermission(void) {
    if (__builtin_available(macOS 10.15, *)) {
        return AXIsProcessTrusted() && CGPreflightPostEventAccess();
    }
    return false;
}
static bool brokerButtonsHeld(void) {
    // Reject physical and synthesized holds; neither is owned by this lease.
    for (CGMouseButton button = 0; button < 32; ++button) {
        if (CGEventSourceButtonState(kCGEventSourceStateHIDSystemState, button) ||
            CGEventSourceButtonState(kCGEventSourceStateCombinedSessionState, button)) return true;
    }
    return false;
}
static CGEventRef brokerMouseEvent(CGMouseButton button, CGEventType type, double x, double y) {
    CGEventRef event = CGEventCreateMouseEvent(NULL, type, CGPointMake(x, y), button);
    if (event != NULL) CGEventSetFlags(event, 0);
    return event;
}
static void brokerPost(CGEventRef event, double x, double y) {
    CGEventSetLocation(event, CGPointMake(x, y));
    CGEventSetFlags(event, 0);
    CGEventPost(kCGHIDEventTap, event);
}
*/
import "C"

import (
	"sync"

	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

// Held from successful prepare through close, so separate Backends in the
// same host cannot race two down events before Quartz state catches up.
var nativeMouseOwnership sync.Mutex

type nativeMouseDriver struct{}

func newHostMouseDriver() mouseDriver { return nativeMouseDriver{} }
func (nativeMouseDriver) checkPermission() error {
	if !bool(C.brokerPermission()) {
		return errMouseBrokerUnavailable
	}
	return nil
}
func (nativeMouseDriver) buttonsHeld() bool { return bool(C.brokerButtonsHeld()) }
func (nativeMouseDriver) prepare(button cu.MouseButton, x, y float64) (mouseGesture, error) {
	if !nativeMouseOwnership.TryLock() {
		return nil, errMouseBrokerUnavailable
	}
	g := &nativeMouseGesture{x: x, y: y, button: C.kCGMouseButtonLeft, dragType: C.kCGEventLeftMouseDragged}
	downType, upType := C.CGEventType(C.kCGEventLeftMouseDown), C.CGEventType(C.kCGEventLeftMouseUp)
	if button == cu.MouseButtonRight {
		g.button, g.dragType = C.kCGMouseButtonRight, C.kCGEventRightMouseDragged
		downType, upType = C.kCGEventRightMouseDown, C.kCGEventRightMouseUp
	}
	g.downEvent = C.brokerMouseEvent(g.button, downType, C.double(x), C.double(y))
	g.upEvent = C.brokerMouseEvent(g.button, upType, C.double(x), C.double(y))
	if g.downEvent == 0 || g.upEvent == 0 {
		g.close()
		return nil, errMouseBrokerUnavailable
	}
	return g, nil
}

type nativeMouseGesture struct {
	downEvent, upEvent C.CGEventRef
	button             C.CGMouseButton
	dragType           C.CGEventType
	x, y               float64
}

func (g *nativeMouseGesture) down() { C.brokerPost(g.downEvent, C.double(g.x), C.double(g.y)) }
func (g *nativeMouseGesture) drag(x, y float64) error {
	if !bool(C.brokerPermission()) {
		return errMouseBrokerUnavailable
	}
	event := C.brokerMouseEvent(g.button, g.dragType, C.double(x), C.double(y))
	if event == 0 {
		return errMouseBrokerUnavailable
	}
	C.brokerPost(event, C.double(x), C.double(y))
	C.CFRelease(C.CFTypeRef(event))
	return nil
}
func (g *nativeMouseGesture) up(x, y float64) error {
	if !bool(C.brokerPermission()) {
		return errMouseBrokerUnavailable
	}
	C.brokerPost(g.upEvent, C.double(x), C.double(y))
	return nil // attempted publication, not proof the target received it
}
func (g *nativeMouseGesture) close() {
	if g.downEvent != 0 {
		C.CFRelease(C.CFTypeRef(g.downEvent))
		g.downEvent = 0
	}
	if g.upEvent != 0 {
		C.CFRelease(C.CFTypeRef(g.upEvent))
		g.upEvent = 0
	}
	nativeMouseOwnership.Unlock()
}
