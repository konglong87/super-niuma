//go:build darwin

#import <AppKit/AppKit.h>
#import <Foundation/Foundation.h>
#include <pthread.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "computer_panel_darwin.h"

static const NSUInteger kCommandCapacity = 16;
static NSString * const kGeometryDefaultsKey = @"niuma.computer-panel.geometry.v1";
static CGFloat const kPanelMargin = 14.0;
static CGFloat const kCompactWidth = 360.0;
static CGFloat const kCompactHeight = 100.0;
static CGFloat const kExpandedWidth = 400.0;
static CGFloat const kExpandedHeight = 310.0;

@class Goe2EPanelController;

struct goe2e_computer_panel {
    pthread_mutex_t mutex;
    bool closed;
    char *commands[kCommandCapacity];
    NSUInteger commandCount;
    Goe2EPanelController *controller;
};

@interface Goe2EPanel : NSPanel
@end

@interface Goe2EPanelContentView : NSView
@end

@interface Goe2EPanelController : NSObject <NSWindowDelegate>
@property(nonatomic, assign) goe2e_computer_panel *bridge;
@property(nonatomic, retain) Goe2EPanel *panel;
@property(nonatomic, retain) NSTextField *titleLabel;
@property(nonatomic, retain) NSTextField *detailLabel;
@property(nonatomic, retain) NSImageView *previewView;
@property(nonatomic, retain) NSTextField *previewLabel;
@property(nonatomic, retain) NSButton *toggleButton;
@property(nonatomic, retain) NSButton *dismissButton;
@property(nonatomic, retain) NSButton *stopButton;
@property(nonatomic, retain) NSButton *pauseButton;
@property(nonatomic, retain) NSButton *resumeButton;
@property(nonatomic, copy) NSString *renderedSessionID;
@property(nonatomic, assign) BOOL renderedExpanded;
@property(nonatomic, assign) BOOL hasRenderedFrame;
- (instancetype)initWithBridge:(goe2e_computer_panel *)bridge;
- (void)applyJSON:(char *)json;
- (void)recalibrate;
@end

@implementation Goe2EPanel
- (BOOL)canBecomeKeyWindow { return NO; }
- (BOOL)canBecomeMainWindow { return NO; }
- (BOOL)acceptsFirstMouse:(NSEvent *)event { return YES; }
@end

@implementation Goe2EPanelContentView
- (void)mouseDown:(NSEvent *)event {
    NSPoint point = [self convertPoint:event.locationInWindow fromView:nil];
    if (point.y >= self.bounds.size.height - 48.0 && point.x < self.bounds.size.width - 84.0) {
        [self.window performWindowDragWithEvent:event];
        return;
    }
    [super mouseDown:event];
}
@end

static NSString *StringValue(NSDictionary *dictionary, NSString *key) {
    id value = dictionary[key];
    return [value isKindOfClass:[NSString class]] ? value : @"";
}

static BOOL BoolValue(NSDictionary *dictionary, NSString *key) {
    id value = dictionary[key];
    return [value isKindOfClass:[NSNumber class]] ? [value boolValue] : NO;
}

static void EnqueueCommand(goe2e_computer_panel *bridge, NSString *kind, NSString *sessionID) {
    if (bridge == NULL || kind == nil) return;
    const char *kindUTF8 = kind.UTF8String ?: "";
    const char *sessionUTF8 = sessionID.UTF8String ?: "";
    size_t length = strlen(kindUTF8) + strlen(sessionUTF8) + 32;
    char *encoded = calloc(length, sizeof(char));
    if (encoded == NULL) return;
    snprintf(encoded, length, "{\"kind\":\"%s\",\"session_id\":\"%s\"}", kindUTF8, sessionUTF8);

    pthread_mutex_lock(&bridge->mutex);
    if (bridge->closed) {
        pthread_mutex_unlock(&bridge->mutex);
        free(encoded);
        return;
    }
    if ([kind isEqualToString:@"stop"]) {
        for (NSUInteger i = 0; i < bridge->commandCount; i++) free(bridge->commands[i]);
        bridge->commandCount = 0;
        bridge->commands[bridge->commandCount++] = encoded;
        pthread_mutex_unlock(&bridge->mutex);
        return;
    }
    for (NSUInteger i = 0; i < bridge->commandCount; i++) {
        if (strcmp(bridge->commands[i], encoded) == 0) {
            pthread_mutex_unlock(&bridge->mutex);
            free(encoded);
            return;
        }
    }
    if (bridge->commandCount == kCommandCapacity) {
        free(bridge->commands[0]);
        memmove(&bridge->commands[0], &bridge->commands[1], sizeof(char *) * (kCommandCapacity - 1));
        bridge->commandCount = kCommandCapacity - 1;
    }
    bridge->commands[bridge->commandCount++] = encoded;
    pthread_mutex_unlock(&bridge->mutex);
}

@implementation Goe2EPanelController

- (instancetype)initWithBridge:(goe2e_computer_panel *)bridge {
    self = [super init];
    if (!self) return nil;
    _bridge = bridge;
    _renderedSessionID = [@"" copy];

    NSRect frame = NSMakeRect(0, 0, kCompactWidth, kCompactHeight);
    _panel = [[Goe2EPanel alloc] initWithContentRect:frame
                                           styleMask:(NSWindowStyleMaskBorderless | NSWindowStyleMaskNonactivatingPanel)
                                             backing:NSBackingStoreBuffered
                                               defer:NO];
    _panel.title = @"niuma Computer Use";
    _panel.floatingPanel = YES;
    _panel.level = NSFloatingWindowLevel;
    _panel.hidesOnDeactivate = NO;
    _panel.becomesKeyOnlyIfNeeded = YES;
    _panel.releasedWhenClosed = NO;
    _panel.opaque = NO;
    _panel.backgroundColor = NSColor.clearColor;
    _panel.hasShadow = YES;
    // The floating controls are user-visible UI and must appear in ordinary
    // desktop/region screenshots, just like the rest of the application.
    _panel.sharingType = NSWindowSharingReadOnly;
    _panel.collectionBehavior = NSWindowCollectionBehaviorCanJoinAllSpaces | NSWindowCollectionBehaviorFullScreenAuxiliary | NSWindowCollectionBehaviorIgnoresCycle;
    _panel.delegate = self;

    Goe2EPanelContentView *content = [[Goe2EPanelContentView alloc] initWithFrame:frame];
    content.wantsLayer = YES;
    content.layer.backgroundColor = [NSColor colorWithCalibratedWhite:0.08 alpha:0.97].CGColor;
    content.layer.cornerRadius = 16.0;
    content.layer.masksToBounds = YES;
    _panel.contentView = content;
    [content release];

    _titleLabel = [self labelWithSize:14 weight:NSFontWeightSemibold];
    _detailLabel = [self labelWithSize:11 weight:NSFontWeightRegular];
    _previewView = [[NSImageView alloc] initWithFrame:NSZeroRect];
    _previewView.imageScaling = NSImageScaleProportionallyUpOrDown;
    _previewView.hidden = YES;
    _previewLabel = [self labelWithSize:11 weight:NSFontWeightRegular];
    _previewLabel.textColor = [NSColor colorWithCalibratedWhite:0.75 alpha:1.0];
    _previewLabel.hidden = YES;

    _toggleButton = [self buttonWithTitle:@"Expand" action:@selector(toggleExpanded:)];
    _dismissButton = [self buttonWithTitle:@"×" action:@selector(dismiss:)];
    _stopButton = [self buttonWithTitle:@"Stop" action:@selector(stop:)];
    _pauseButton = [self buttonWithTitle:@"Pause" action:@selector(pause:)];
    _resumeButton = [self buttonWithTitle:@"Resume" action:@selector(resume:)];

    [_panel.contentView addSubview:_titleLabel];
    [_panel.contentView addSubview:_detailLabel];
    [_panel.contentView addSubview:_previewView];
    [_panel.contentView addSubview:_previewLabel];
    [_panel.contentView addSubview:_toggleButton];
    [_panel.contentView addSubview:_dismissButton];
    [_panel.contentView addSubview:_stopButton];
    [_panel.contentView addSubview:_pauseButton];
    [_panel.contentView addSubview:_resumeButton];
    _resumeButton.hidden = YES;

    [[NSNotificationCenter defaultCenter] addObserver:self selector:@selector(screenParametersChanged:) name:NSApplicationDidChangeScreenParametersNotification object:nil];
    [[NSNotificationCenter defaultCenter] addObserver:self selector:@selector(windowMovedOrResized:) name:NSWindowDidMoveNotification object:_panel];
    [[NSNotificationCenter defaultCenter] addObserver:self selector:@selector(windowMovedOrResized:) name:NSWindowDidResizeNotification object:_panel];
    return self;
}

- (NSTextField *)labelWithSize:(CGFloat)size weight:(NSFontWeight)weight {
    NSTextField *label = [[NSTextField alloc] initWithFrame:NSZeroRect];
    label.bezeled = NO;
    label.drawsBackground = NO;
    label.editable = NO;
    label.selectable = NO;
    label.textColor = NSColor.whiteColor;
    label.font = [NSFont systemFontOfSize:size weight:weight];
    label.lineBreakMode = NSLineBreakByTruncatingTail;
    return label;
}

- (NSButton *)buttonWithTitle:(NSString *)title action:(SEL)action {
    NSButton *button = [NSButton buttonWithTitle:title target:self action:action];
    button.bezelStyle = NSBezelStyleRounded;
    button.font = [NSFont systemFontOfSize:11 weight:NSFontWeightMedium];
    return [button retain];
}

- (void)dealloc {
    [[NSNotificationCenter defaultCenter] removeObserver:self];
    [_panel orderOut:nil];
    [_renderedSessionID release];
    [_titleLabel release];
    [_detailLabel release];
    [_previewView release];
    [_previewLabel release];
    [_toggleButton release];
    [_dismissButton release];
    [_stopButton release];
    [_pauseButton release];
    [_resumeButton release];
    [_panel release];
    [super dealloc];
}

- (void)applyJSON:(char *)json {
    if (json == NULL) return;
    NSData *data = [NSData dataWithBytes:json length:strlen(json)];
    NSError *error = nil;
    NSDictionary *snapshot = [NSJSONSerialization JSONObjectWithData:data options:0 error:&error];
    if (error != nil || ![snapshot isKindOfClass:[NSDictionary class]]) return;

    BOOL visible = BoolValue(snapshot, @"visible");
    if (!visible) {
        _previewView.image = nil;
        self.renderedSessionID = @"";
        [_panel orderOut:nil];
        return;
    }
    BOOL expanded = BoolValue(snapshot, @"expanded");
    NSString *language = StringValue(snapshot, @"language");
    NSString *sessionID = StringValue(snapshot, @"session_id");
    self.renderedSessionID = sessionID;
    self.renderedExpanded = expanded;
    self.hasRenderedFrame = YES;

    BOOL wasVisible = _panel.isVisible;
    [self applyLanguage:language expanded:expanded];
    [_titleLabel setStringValue:StringValue(snapshot, @"title")];
    [_detailLabel setStringValue:StringValue(snapshot, @"detail")];
    [_previewLabel setStringValue:StringValue(snapshot, @"preview_detail")];
    _stopButton.enabled = BoolValue(snapshot, @"can_stop");
    _pauseButton.enabled = BoolValue(snapshot, @"can_pause");
    _resumeButton.enabled = BoolValue(snapshot, @"can_resume");
    _pauseButton.hidden = !BoolValue(snapshot, @"can_pause");
    _resumeButton.hidden = !BoolValue(snapshot, @"can_resume");

    NSString *imageData = StringValue(snapshot, @"image_data");
    if (expanded && imageData.length > 0) {
        NSData *imageBytes = [[[NSData alloc] initWithBase64EncodedString:imageData options:NSDataBase64DecodingIgnoreUnknownCharacters] autorelease];
        _previewView.image = imageBytes.length > 0 ? [[[NSImage alloc] initWithData:imageBytes] autorelease] : nil;
    } else {
        _previewView.image = nil;
    }
    _previewView.hidden = !expanded || _previewView.image == nil;
    [self layoutForExpanded:expanded];
    [self clampAndAnchorIfNeeded:!wasVisible];
    if (!wasVisible) [_panel orderFrontRegardless];
}

- (void)applyLanguage:(NSString *)language expanded:(BOOL)expanded {
    BOOL zh = [language.lowercaseString hasPrefix:@"zh"];
    [_toggleButton setTitle:(expanded ? (zh ? @"收起" : @"Collapse") : (zh ? @"展开" : @"Expand") )];
    [_dismissButton setTitle:zh ? @"隐藏" : @"Hide"];
    _dismissButton.toolTip = zh ? @"隐藏浮层（不会停止会话）" : @"Hide panel (session continues)";
    [_dismissButton setAccessibilityLabel:zh ? @"隐藏浮层" : @"Hide panel"];
    [_stopButton setTitle:zh ? @"停止" : @"Stop"];
    [_pauseButton setTitle:zh ? @"暂停" : @"Pause"];
    [_resumeButton setTitle:zh ? @"继续" : @"Resume"];
}

- (void)layoutForExpanded:(BOOL)expanded {
    CGFloat width = expanded ? kExpandedWidth : kCompactWidth;
    CGFloat height = expanded ? kExpandedHeight : kCompactHeight;
    NSRect frame = _panel.frame;
    // Preserve the top-right anchor when expanding/collapsing after a drag.
    frame.origin.x += frame.size.width - width;
    frame.origin.y += frame.size.height - height;
    frame.size = NSMakeSize(width, height);
    [_panel setFrame:frame display:YES];

    CGFloat top = height - 38.0;
    [_titleLabel setFrame:NSMakeRect(16, top, width - 148, 22)];
    [_detailLabel setFrame:NSMakeRect(16, height - 62, width - 32, 18)];
    [_dismissButton setFrame:NSMakeRect(width - 64, top + 1, 52, 22)];
    [_toggleButton setFrame:NSMakeRect(width - 126, top + 1, 58, 22)];
    [_stopButton setFrame:NSMakeRect(16, expanded ? height - 94 : 12, 52, 22)];
    [_pauseButton setFrame:NSMakeRect(74, expanded ? height - 94 : 12, 58, 22)];
    [_resumeButton setFrame:NSMakeRect(74, expanded ? height - 94 : 12, 64, 22)];
    [_previewView setFrame:NSMakeRect(16, 16, width - 32, expanded ? height - 148 : 0)];
    [_previewLabel setFrame:NSMakeRect(16, height - 120, width - 32, 18)];
    _previewLabel.hidden = !expanded;
    _previewView.hidden = !expanded || _previewView.image == nil;
}

- (void)clampAndAnchorIfNeeded:(BOOL)anchor {
    NSScreen *screen = _panel.screen ?: NSScreen.mainScreen;
    if (!screen) return;
    NSRect visible = screen.visibleFrame;
    NSRect frame = _panel.frame;
    if (anchor) {
        frame.origin.x = NSMaxX(visible) - frame.size.width - kPanelMargin;
        frame.origin.y = NSMaxY(visible) - frame.size.height - kPanelMargin;
    }
    frame.origin.x = MIN(MAX(NSMinX(visible) + kPanelMargin, frame.origin.x), NSMaxX(visible) - frame.size.width - kPanelMargin);
    frame.origin.y = MIN(MAX(NSMinY(visible) + kPanelMargin, frame.origin.y), NSMaxY(visible) - frame.size.height - kPanelMargin);
    [_panel setFrame:frame display:YES];
}

- (void)recalibrate {
    [self clampAndAnchorIfNeeded:NO];
}

- (void)screenParametersChanged:(NSNotification *)notification {
    dispatch_async(dispatch_get_main_queue(), ^{ [self recalibrate]; });
}

- (void)windowMovedOrResized:(NSNotification *)notification {
    if (!_panel.isVisible) return;
    NSRect frame = _panel.frame;
    NSDictionary *geometry = @{ @"x": @(frame.origin.x), @"y": @(frame.origin.y), @"width": @(frame.size.width), @"height": @(frame.size.height) };
    [[NSUserDefaults standardUserDefaults] setObject:geometry forKey:kGeometryDefaultsKey];
}

- (void)stop:(id)sender { EnqueueCommand(_bridge, @"stop", _renderedSessionID); }
- (void)pause:(id)sender { EnqueueCommand(_bridge, @"pause", _renderedSessionID); }
- (void)resume:(id)sender { EnqueueCommand(_bridge, @"resume", _renderedSessionID); }
- (void)toggleExpanded:(id)sender { EnqueueCommand(_bridge, _renderedExpanded ? @"collapse" : @"expand", _renderedSessionID); }
- (void)dismiss:(id)sender { EnqueueCommand(_bridge, @"dismiss", _renderedSessionID); [_panel orderOut:nil]; }

@end

static void OnMainThread(void (^block)(void)) {
    if ([NSThread isMainThread]) block();
    else dispatch_sync(dispatch_get_main_queue(), block);
}

goe2e_computer_panel *goe2e_computer_panel_create(void) {
    goe2e_computer_panel *bridge = calloc(1, sizeof(goe2e_computer_panel));
    if (bridge == NULL) return NULL;
    pthread_mutex_init(&bridge->mutex, NULL);
    OnMainThread(^{
        Goe2EPanelController *controller = [[Goe2EPanelController alloc] initWithBridge:bridge];
        bridge->controller = controller;
    });
    return bridge;
}

void goe2e_computer_panel_update(goe2e_computer_panel *bridge, const char *jsonUTF8) {
    if (bridge == NULL || jsonUTF8 == NULL) return;
    char *copy = strdup(jsonUTF8);
    if (copy == NULL) return;
    pthread_mutex_lock(&bridge->mutex);
    BOOL closed = bridge->closed;
    Goe2EPanelController *controller = bridge->controller;
    pthread_mutex_unlock(&bridge->mutex);
    if (closed || controller == nil) { free(copy); return; }
    dispatch_async(dispatch_get_main_queue(), ^{
        pthread_mutex_lock(&bridge->mutex);
        BOOL stillOpen = !bridge->closed;
        Goe2EPanelController *current = bridge->controller;
        pthread_mutex_unlock(&bridge->mutex);
        if (stillOpen && current != nil) [current applyJSON:copy];
        free(copy);
    });
}

char *goe2e_computer_panel_poll(goe2e_computer_panel *bridge) {
    if (bridge == NULL) return NULL;
    pthread_mutex_lock(&bridge->mutex);
    if (bridge->commandCount == 0) {
        pthread_mutex_unlock(&bridge->mutex);
        return NULL;
    }
    char *result = bridge->commands[0];
    memmove(&bridge->commands[0], &bridge->commands[1], sizeof(char *) * (bridge->commandCount - 1));
    bridge->commandCount--;
    pthread_mutex_unlock(&bridge->mutex);
    return result;
}

void goe2e_computer_panel_free_string(char *value) { free(value); }

void goe2e_computer_panel_close(goe2e_computer_panel *bridge) {
    if (bridge == NULL) return;
    pthread_mutex_lock(&bridge->mutex);
    if (bridge->closed) {
        pthread_mutex_unlock(&bridge->mutex);
        return;
    }
    bridge->closed = true;
    Goe2EPanelController *controller = bridge->controller;
    bridge->controller = nil;
    for (NSUInteger i = 0; i < bridge->commandCount; i++) free(bridge->commands[i]);
    bridge->commandCount = 0;
    pthread_mutex_unlock(&bridge->mutex);

    // Queue after prior updates even when Close originates on the main thread.
    dispatch_async(dispatch_get_main_queue(), ^{
        [controller release];
        pthread_mutex_destroy(&bridge->mutex);
        free(bridge);
    });
}
