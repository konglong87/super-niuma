//go:build darwin && cgo

package macos

/*
#cgo LDFLAGS: -framework CoreGraphics -framework ApplicationServices
#include <CoreGraphics/CoreGraphics.h>
#include <ApplicationServices/ApplicationServices.h>

static int hostCaptureAllowed(void) {
    if (__builtin_available(macOS 10.15, *)) {
        return CGPreflightScreenCaptureAccess() ? 1 : 0;
    }
    return 1;
}

static int hostInputAllowed(void) {
    int postAllowed = 1;
    if (__builtin_available(macOS 10.15, *)) {
        postAllowed = CGPreflightPostEventAccess() ? 1 : 0;
    }
    return (AXIsProcessTrusted() && postAllowed) ? 1 : 0;
}

static void requestHostPermissions(void) {
    if (__builtin_available(macOS 10.15, *)) {
        (void)CGRequestScreenCaptureAccess();
    }
    const void *key = kAXTrustedCheckOptionPrompt;
    const void *value = kCFBooleanTrue;
    const void *keys[] = {key};
    const void *values[] = {value};
    CFDictionaryRef options = CFDictionaryCreate(kCFAllocatorDefault, keys, values, 1,
        &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    (void)AXIsProcessTrustedWithOptions(options);
    if (options != NULL) CFRelease(options);
}
*/
import "C"

// RequestHostPermissions asks macOS to attribute the prompt to the signed
// desktop host process rather than only to the nested helper.
func RequestHostPermissions() {
	C.requestHostPermissions()
}

// CheckHostPermissions reads TCC state for the signed desktop host process.
// The nested helper has a different process identity and must not be trusted
// as the permission authority.
func CheckHostPermissions() (captureAllowed, inputAllowed bool) {
	return C.hostCaptureAllowed() != 0, C.hostInputAllowed() != 0
}
