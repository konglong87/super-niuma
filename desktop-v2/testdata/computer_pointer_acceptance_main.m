#import <AppKit/AppKit.h>
#import <Foundation/Foundation.h>
#include <stdio.h>
#include <stdlib.h>
#include "../computer_pointer_darwin.h"

static void fail(const char *message) {
    fprintf(stderr, "computer-pointer-acceptance: %s\n", message);
    exit(EXIT_FAILURE);
}

static BOOL captureDesktop(const char *path) {
    NSString *output = [NSString stringWithUTF8String:path];
    if (output == nil) return NO;
    NSTask *task = [[NSTask alloc] init];
    task.launchPath = @"/usr/sbin/screencapture";
    task.arguments = @[@"-x", output];
    [task launch];
    [task waitUntilExit];
    BOOL success = task.terminationStatus == 0;
    [task release];
    return success;
}

static void pumpRunLoop(NSTimeInterval seconds) {
    NSDate *deadline = [NSDate dateWithTimeIntervalSinceNow:seconds];
    while ([deadline timeIntervalSinceNow] > 0) {
        NSDate *next = [NSDate dateWithTimeIntervalSinceNow:0.02];
        [[NSRunLoop currentRunLoop] runMode:NSDefaultRunLoopMode beforeDate:next];
    }
}

int main(int argc, const char **argv) {
    if (argc != 4) fail("usage: <desktop.png> <overlay.png> <geometry.json>");
    @autoreleasepool {
        [NSApplication sharedApplication];
        [NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];

        goe2e_computer_pointer *pointer = goe2e_computer_pointer_create();
        if (pointer == NULL) fail("native pointer could not be created");
        const char *payload =
            "{\"visible\":true,\"cue\":{\"session_id\":\"pointer-acceptance-session\","
            "\"turn_id\":\"pointer-acceptance-turn\",\"action_id\":\"pointer-acceptance-action\","
            "\"action_kind\":\"click\",\"phase\":\"dispatched\",\"dispatch_state\":\"complete\","
            "\"app_bundle_id\":\"com.apple.calculator\",\"window_id\":\"acceptance-window\","
            "\"global_point\":{\"x\":370,\"y\":560}}}";
        goe2e_computer_pointer_show(pointer, payload);
        pumpRunLoop(0.4);

        if (!goe2e_computer_pointer_capture_overlay(pointer, argv[2])) {
            goe2e_computer_pointer_close(pointer);
            fail("native pointer overlay could not be captured");
        }

        double globalX = 0, globalY = 0, appkitX = 0, appkitY = 0, primaryTop = 0;
        double frameX = 0, frameY = 0, frameWidth = 0, frameHeight = 0;
        if (!goe2e_computer_pointer_geometry(pointer, &globalX, &globalY, &appkitX, &appkitY,
                                              &primaryTop, &frameX, &frameY, &frameWidth, &frameHeight)) {
            goe2e_computer_pointer_close(pointer);
            fail("native pointer geometry is unavailable");
        }
        FILE *geometry = fopen(argv[3], "w");
        if (geometry == NULL) {
            goe2e_computer_pointer_close(pointer);
            fail("could not write geometry evidence");
        }
        fprintf(geometry,
                "{\"global_x\":%.6f,\"global_y\":%.6f,\"appkit_x\":%.6f,\"appkit_y\":%.6f,"
                "\"primary_top\":%.6f,\"frame_x\":%.6f,\"frame_y\":%.6f,\"frame_width\":%.6f,\"frame_height\":%.6f}\n",
                globalX, globalY, appkitX, appkitY, primaryTop,
                frameX, frameY, frameWidth, frameHeight);
        fclose(geometry);

        if (!captureDesktop(argv[1])) {
            goe2e_computer_pointer_close(pointer);
            fail("desktop screenshot failed");
        }
        goe2e_computer_pointer_hide(pointer);
        goe2e_computer_pointer_close(pointer);
        pumpRunLoop(0.1);
    }
    return EXIT_SUCCESS;
}
