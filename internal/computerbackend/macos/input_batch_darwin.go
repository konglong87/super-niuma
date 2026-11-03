//go:build darwin && cgo

package macos

/*
#cgo LDFLAGS: -framework CoreGraphics -framework ApplicationServices -framework Carbon
#include <CoreGraphics/CoreGraphics.h>
#include <ApplicationServices/ApplicationServices.h>
#include <Carbon/Carbon.h>
#include <stdlib.h>
#include <stdbool.h>
#include <stdint.h>

#define inputBatchMaxEvents 10

typedef struct inputBatchNative {
    CGEventRef events[inputBatchMaxEvents];
    size_t count;
} inputBatchNative;

static bool inputBatchPermission(void) {
    if (__builtin_available(macOS 10.15, *)) {
        return AXIsProcessTrusted() && CGPreflightPostEventAccess();
    }
    return false;
}

static bool inputBatchInputHeld(void) {
    for (CGMouseButton button = 0; button < 32; button++) {
        if (CGEventSourceButtonState(kCGEventSourceStateHIDSystemState, button) ||
            CGEventSourceButtonState(kCGEventSourceStateCombinedSessionState, button)) {
            return true;
        }
    }
    for (CGKeyCode key = 0; key < 128; key++) {
        if (CGEventSourceKeyState(kCGEventSourceStateHIDSystemState, key) ||
            CGEventSourceKeyState(kCGEventSourceStateCombinedSessionState, key)) {
            return true;
        }
    }
    return false;
}

static inputBatchNative *inputBatchNew(void) {
    return (inputBatchNative *)calloc(1, sizeof(inputBatchNative));
}

static void inputBatchClose(inputBatchNative *batch) {
    if (batch == NULL) return;
    for (size_t i = 0; i < batch->count; i++) {
        if (batch->events[i] != NULL) CFRelease(batch->events[i]);
    }
    free(batch);
}

static bool inputBatchAppend(inputBatchNative *batch, CGEventRef event) {
    if (batch == NULL || event == NULL || batch->count >= inputBatchMaxEvents) {
        if (event != NULL) CFRelease(event);
        return false;
    }
    batch->events[batch->count++] = event;
    return true;
}

static bool inputBatchAppendMouse(inputBatchNative *batch, CGMouseButton button,
                                  double x, double y, int clickCount) {
    CGEventType downType = button == kCGMouseButtonRight ? kCGEventRightMouseDown : kCGEventLeftMouseDown;
    CGEventType upType = button == kCGMouseButtonRight ? kCGEventRightMouseUp : kCGEventLeftMouseUp;
    CGPoint point = CGPointMake(x, y);
    CGEventRef down = CGEventCreateMouseEvent(NULL, downType, point, button);
    CGEventRef up = CGEventCreateMouseEvent(NULL, upType, point, button);
    if (down == NULL || up == NULL) {
        if (down != NULL) CFRelease(down);
        if (up != NULL) CFRelease(up);
        return false;
    }
    CGEventSetFlags(down, 0);
    CGEventSetFlags(up, 0);
    CGEventSetIntegerValueField(down, kCGMouseEventClickState, clickCount);
    CGEventSetIntegerValueField(up, kCGMouseEventClickState, clickCount);
    if (!inputBatchAppend(batch, down) || !inputBatchAppend(batch, up)) return false;
    return true;
}

static bool inputBatchAppendKeyboard(inputBatchNative *batch, CGKeyCode code,
                                     bool down, CGEventFlags flags) {
    CGEventRef event = CGEventCreateKeyboardEvent(NULL, code, down);
    if (event == NULL) return false;
    CGEventSetFlags(event, flags);
    return inputBatchAppend(batch, event);
}

static bool inputBatchAppendUnicode(inputBatchNative *batch, uint32_t scalar) {
    if (scalar > 0x10ffff || (scalar >= 0xd800 && scalar <= 0xdfff)) return false;
    UniChar units[2];
    UniCharCount length = 1;
    if (scalar <= 0xffff) {
        units[0] = (UniChar)scalar;
    } else {
        scalar -= 0x10000;
        units[0] = (UniChar)(0xd800 | (scalar >> 10));
        units[1] = (UniChar)(0xdc00 | (scalar & 0x3ff));
        length = 2;
    }
    CGEventRef down = CGEventCreateKeyboardEvent(NULL, 0, true);
    CGEventRef up = CGEventCreateKeyboardEvent(NULL, 0, false);
    if (down == NULL || up == NULL) {
        if (down != NULL) CFRelease(down);
        if (up != NULL) CFRelease(up);
        return false;
    }
    CGEventKeyboardSetUnicodeString(down, length, units);
    CGEventKeyboardSetUnicodeString(up, length, units);
    CGEventSetFlags(down, 0);
    CGEventSetFlags(up, 0);
    if (!inputBatchAppend(batch, down) || !inputBatchAppend(batch, up)) return false;
    return true;
}

static bool inputBatchAppendKey(inputBatchNative *batch, CGKeyCode code, CGEventFlags flags) {
    static const struct {
        CGKeyCode code;
        CGEventFlags flag;
    } modifiers[] = {
        { kVK_Control, kCGEventFlagMaskControl },
        { kVK_Option,  kCGEventFlagMaskAlternate },
        { kVK_Shift,   kCGEventFlagMaskShift },
        { kVK_Command, kCGEventFlagMaskCommand },
    };
    CGEventFlags held = 0;
    size_t pressedIndices[sizeof(modifiers) / sizeof(modifiers[0])];
    size_t pressed = 0;
    for (size_t i = 0; i < sizeof(modifiers) / sizeof(modifiers[0]); i++) {
        if ((flags & modifiers[i].flag) == 0) continue;
        held |= modifiers[i].flag;
        if (!inputBatchAppendKeyboard(batch, modifiers[i].code, true, held)) return false;
        pressedIndices[pressed++] = i;
    }
    if (!inputBatchAppendKeyboard(batch, code, true, flags) ||
        !inputBatchAppendKeyboard(batch, code, false, flags)) return false;
    for (size_t i = pressed; i > 0; i--) {
        size_t modifierIndex = pressedIndices[i - 1];
        held &= ~modifiers[modifierIndex].flag;
        if (!inputBatchAppendKeyboard(batch, modifiers[modifierIndex].code, false, held)) return false;
    }
    return true;
}

static void inputBatchCommit(inputBatchNative *batch) {
    if (batch == NULL) return;
    for (size_t i = 0; i < batch->count; i++) {
        CGEventPost(kCGHIDEventTap, batch->events[i]);
    }
}
*/
import "C"

import (
	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

type nativeInputBatchDriver struct{}

type nativePreparedInputBatch struct {
	native *C.inputBatchNative
	closed bool
}

func newHostInputBatchDriver() inputBatchDriver { return nativeInputBatchDriver{} }

func (nativeInputBatchDriver) prepareBatch(op inputBatchOperation) (preparedInputBatch, error) {
	if !nativeMouseOwnership.TryLock() {
		return nil, errMouseBrokerUnavailable
	}
	unlock := true
	defer func() {
		if unlock {
			nativeMouseOwnership.Unlock()
		}
	}()
	if !bool(C.inputBatchPermission()) || bool(C.inputBatchInputHeld()) {
		return nil, errMouseBrokerUnavailable
	}
	native := C.inputBatchNew()
	if native == nil {
		return nil, errMouseBrokerUnavailable
	}
	prepared := &nativePreparedInputBatch{native: native}
	if !appendInputBatchOperation(native, op) {
		C.inputBatchClose(native)
		return nil, errMouseBrokerUnavailable
	}
	unlock = false
	return prepared, nil
}

func appendInputBatchOperation(native *C.inputBatchNative, op inputBatchOperation) bool {
	switch op.kind {
	case cu.ActionClick, cu.ActionDoubleClick, cu.ActionRightClick:
		if op.button != cu.MouseButtonLeft && op.button != cu.MouseButtonRight {
			return false
		}
		if op.clickCount < 1 || op.clickCount > 2 {
			return false
		}
		button := C.CGMouseButton(C.kCGMouseButtonLeft)
		if op.button == cu.MouseButtonRight {
			button = C.CGMouseButton(C.kCGMouseButtonRight)
		}
		return bool(C.inputBatchAppendMouse(native, button, C.double(op.x), C.double(op.y), C.int(op.clickCount)))
	case cu.ActionType:
		return bool(C.inputBatchAppendUnicode(native, C.uint32_t(op.scalar)))
	case cu.ActionKey, cu.ActionHotkey:
		return bool(C.inputBatchAppendKey(native, C.CGKeyCode(op.keyCode), C.CGEventFlags(op.flags)))
	default:
		return false
	}
}

func (b *nativePreparedInputBatch) commit() {
	if b == nil || b.closed || b.native == nil {
		return
	}
	C.inputBatchCommit(b.native)
}

func (b *nativePreparedInputBatch) close() {
	if b == nil || b.closed {
		return
	}
	b.closed = true
	if b.native != nil {
		C.inputBatchClose(b.native)
		b.native = nil
	}
	nativeMouseOwnership.Unlock()
}
