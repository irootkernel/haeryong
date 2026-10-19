//go:build darwin && haeryong_test_state && haeryong_native_automation

// Public own-process AppKit automation implementation (see
// automation_darwin.go). Manual retain/release is deliberate: cgo compiles
// this file without ARC, so every NSString consumed by a deferred
// dispatch_async block is created BEFORE the block is scheduled, captured by
// the copied block (which retains it), and the local ownership is released
// after scheduling — the queued block never touches a freed C string.
#import <Cocoa/Cocoa.h>
#include <stdlib.h>
#include <string.h>
#import "_cgo_export.h"

char *hyAutomationBundleIdentifier(void) {
    return strdup([[[NSBundle mainBundle] bundleIdentifier] UTF8String] ?: "");
}

static NSView *hyAutomationFindWebView(NSView *view) {
    if ([view isKindOfClass:NSClassFromString(@"WKWebView")]) return view;
    for (NSView *child in [view subviews]) {
        NSView *found = hyAutomationFindWebView(child);
        if (found) return found;
    }
    return nil;
}

void hyAutomationWindowDetails(const char *title) {
    NSString *wanted = [[NSString alloc] initWithUTF8String:title];
    dispatch_async(dispatch_get_main_queue(), ^{
        for (NSWindow *window in [NSApp windows]) {
            if (![[window title] isEqualToString:wanted]) continue;
            NSView *web = hyAutomationFindWebView([window contentView]);
            long flags = 1 | ([window isVisible] ? 2 : 0) | ([NSApp isActive] ? 4 : 0)
                | (([window occlusionState] & NSWindowOcclusionStateVisible) ? 8 : 0)
                | ([window isOnActiveSpace] ? 16 : 0) | ([window isMiniaturized] ? 32 : 0)
                | (web && ([web frame].size.width == 0 || [web frame].size.height == 0) ? 128 : 0)
                | (web ? 256 : 0) | ([web window] == window ? 512 : 0)
                | ([web isHiddenOrHasHiddenAncestor] ? 1024 : 0) | ([window isKeyWindow] ? 2048 : 0)
                | ((long)[NSApp activationPolicy] << 12);
            hyAutomationEvent(6, flags);
            return;
        }
        hyAutomationEvent(6, 0);
    });
    [wanted release];
}

static NSStatusBarButton *hyAutomationFindStatusBarButton(NSView *view, NSString *title) {
    if (view == nil) return nil;
    if ([view isKindOfClass:[NSStatusBarButton class]]) {
        NSString *t = [(NSButton *)view title];
        if (t != nil && [t isEqualToString:title]) return (NSStatusBarButton *)view;
    }
    for (NSView *sub in [view subviews]) {
        NSStatusBarButton *found = hyAutomationFindStatusBarButton(sub, title);
        if (found != nil) return found;
    }
    return nil;
}

void hyAutomationPerformCloseWindow(const char *title) {
    NSString *wanted = [[NSString alloc] initWithUTF8String:title];
    dispatch_async(dispatch_get_main_queue(), ^{
        for (NSWindow *window in [NSApp windows]) {
            if ([[window title] isEqualToString:wanted]) {
                [window performClose:nil];
                break;
            }
        }
    });
    [wanted release];
}

// Direct bundle-binary fixture launches do not go through Launch Services.
// Activate this process's own actual window before observing foreground UI.
void hyAutomationActivateWindow(const char *title) {
    NSString *wanted = [[NSString alloc] initWithUTF8String:title];
    dispatch_async(dispatch_get_main_queue(), ^{
        for (NSWindow *window in [NSApp windows]) {
            if ([[window title] isEqualToString:wanted]) {
                [NSApp activateIgnoringOtherApps:YES];
                BOOL activated = [[NSRunningApplication currentApplication]
                    activateWithOptions:NSApplicationActivateAllWindows | NSApplicationActivateIgnoringOtherApps];
                [window makeKeyAndOrderFront:nil];
                hyAutomationEvent(5, activated ? 1 : 0);
                return;
            }
        }
        hyAutomationEvent(5, 0);
    });
    [wanted release];
}

// performClick: dispatches the button's REAL installed target/action (the
// pinned SDK's status-item controller), which presents the menu through the
// SDK's own public own-process path.
void hyAutomationClickTrayButton(const char *title) {
    NSString *wanted = [[NSString alloc] initWithUTF8String:title];
    dispatch_async(dispatch_get_main_queue(), ^{
        for (NSWindow *window in [NSApp windows]) {
            NSStatusBarButton *button = hyAutomationFindStatusBarButton([window contentView], wanted);
            if (button != nil) {
                [button performClick:nil];
                return;
            }
        }
        hyAutomationEvent(2, -2);
    });
    [wanted release];
}

static NSString *hyWantedItem = nil;
static BOOL hyMenuArmed = NO;
static id hyMenuObserver = nil;

static void hyAutomationMenuDidBeginTracking(NSNotification *note) {
    NSMenu *menu = (NSMenu *)[note object];
    if (menu == nil || !hyMenuArmed || hyWantedItem == nil) return;
    for (NSInteger i = 0; i < [menu numberOfItems]; i++) {
        NSMenuItem *item = [menu itemAtIndex:i];
        if ([[item title] isEqualToString:hyWantedItem]) {
            hyMenuArmed = NO;
            // BeginTracking arrives before the tracking loop is ready to
            // consume cancelTracking. Defer to that actual run-loop mode;
            // dismiss first, then send the installed item's real action.
            [[NSRunLoop mainRunLoop] performInModes:@[NSEventTrackingRunLoopMode, NSDefaultRunLoopMode] block:^{
                [menu cancelTracking];
                [menu performActionForItemAtIndex:i];
                hyAutomationEvent(2, (long)i);
            }];
            CFRunLoopWakeUp(CFRunLoopGetMain());
            return;
        }
    }
    hyMenuArmed = NO;
    [menu cancelTracking];
    hyAutomationEvent(2, -1);
}

void hyAutomationArmMenuSelection(const char *itemTitle) {
    NSString *wanted = [[NSString alloc] initWithUTF8String:itemTitle];
    dispatch_async(dispatch_get_main_queue(), ^{
        [wanted retain];
        [hyWantedItem release];
        hyWantedItem = wanted;
        hyMenuArmed = YES;
        if (hyMenuObserver == nil) {
            hyMenuObserver = [[NSNotificationCenter defaultCenter]
                addObserverForName:NSMenuDidBeginTrackingNotification
                object:nil queue:[NSOperationQueue mainQueue]
                usingBlock:^(NSNotification *note) { hyAutomationMenuDidBeginTracking(note); }];
        }
    });
    [wanted release];
}

// Hidden windows stay in NSApp.windows, so a hidden companion is observed
// as visible=0 rather than "not found".
void hyAutomationObserveWindowByTitle(const char *title) {
    NSString *wanted = [[NSString alloc] initWithUTF8String:title];
    dispatch_async(dispatch_get_main_queue(), ^{
        for (NSWindow *window in [NSApp windows]) {
            if ([[window title] isEqualToString:wanted]) {
                hyAutomationEvent(1, [window isVisible] ? 1 : 0);
                return;
            }
        }
        hyAutomationEvent(1, -1);
    });
    [wanted release];
}

// Locate an actual installed button through public view/cell APIs only.
static NSButton *hyAutomationPanelButton(NSView *view, NSString *equivalent) {
    if ([view isKindOfClass:[NSButton class]]) {
        NSButton *button = (NSButton *)view;
        if ([[button keyEquivalent] isEqualToString:equivalent] && [button isEnabled]) return button;
    }
    for (NSView *child in [view subviews]) {
        NSButton *found = hyAutomationPanelButton(child, equivalent);
        if (found != nil) return found;
    }
    return nil;
}

// Modern XPC panels declare ok: but can throw "not implemented". Dispatch
// the real installed button/key event; never manufacture a selected URL or
// invoke a completion handler directly. The driver separately checks the
// actual picker result and shared UI outcome.
static long hyAutomationDispatchPanelAction(NSSavePanel *panel, BOOL cancel) {
    @try {
        NSButton *button = nil;
        if (!cancel) {
            NSView *control = [[panel defaultButtonCell] controlView];
            if ([control isKindOfClass:[NSButton class]] && [(NSButton *)control isEnabled]) button = (NSButton *)control;
        }
        if (button != nil) { [button performClick:nil]; return 1; }
        NSString *key = cancel ? @"\033" : @"\r";
        button = hyAutomationPanelButton([panel contentView], key);
        if (button != nil) { [button performClick:nil]; return 2; }
        NSEvent *event = [NSEvent keyEventWithType:NSEventTypeKeyDown location:NSZeroPoint
            modifierFlags:0 timestamp:[[NSProcessInfo processInfo] systemUptime]
            windowNumber:[panel windowNumber] context:nil characters:key
            charactersIgnoringModifiers:key isARepeat:NO keyCode:cancel ? 53 : 36];
        if ([panel performKeyEquivalent:event]) return 3;
        [panel sendEvent:event];
        NSEvent *released = [NSEvent keyEventWithType:NSEventTypeKeyUp location:NSZeroPoint
            modifierFlags:0 timestamp:[[NSProcessInfo processInfo] systemUptime]
            windowNumber:[panel windowNumber] context:nil characters:key
            charactersIgnoringModifiers:key isARepeat:NO keyCode:cancel ? 53 : 36];
        [panel sendEvent:released];
        return 4;
    } @catch (NSException *exception) {
        // A real action exception is an explicit harness failure, never
        // permission to fake a result or click again after an unknown effect.
        return -3;
    }
}

// Complete the first visible save/open panel (NSOpenPanel subclasses
// NSSavePanel) through the public directory/name setters and ok:.
void hyAutomationTryCompletePanel(const char *directory, const char *name, int save) {
    NSString *dir = directory ? [[NSString alloc] initWithUTF8String:directory] : nil;
    NSString *fileName = (save && name) ? [[NSString alloc] initWithUTF8String:name] : nil;
    dispatch_async(dispatch_get_main_queue(), ^{
        for (NSWindow *window in [NSApp windows]) {
            if (![window isKindOfClass:[NSSavePanel class]]) continue;
            if (![window isVisible]) continue;
            NSSavePanel *panel = (NSSavePanel *)window;
            if (dir != nil) {
                [panel setDirectoryURL:[NSURL fileURLWithPath:dir]];
            }
            if (fileName != nil) {
                [panel setNameFieldStringValue:fileName];
            }
            long route = hyAutomationDispatchPanelAction(panel, NO);
            hyAutomationEvent(3, route);
            return;
        }
        hyAutomationEvent(3, 0);
    });
    [dir release];
    [fileName release];
}

// Cancel the first visible save/open panel through the public cancel:.
void hyAutomationTryCancelPanel(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        for (NSWindow *window in [NSApp windows]) {
            if (![window isKindOfClass:[NSSavePanel class]]) continue;
            if (![window isVisible]) continue;
            long route = hyAutomationDispatchPanelAction((NSSavePanel *)window, YES);
            hyAutomationEvent(4, route);
            return;
        }
        hyAutomationEvent(4, 0);
    });
}
