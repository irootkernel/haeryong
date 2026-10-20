//go:build darwin && haeryong_test_state && haeryong_native_automation

// Public own-process AppKit automation implementation (see
// automation_darwin.go). Manual retain/release is deliberate: cgo compiles
// this file without ARC, so every NSString consumed by a deferred
// dispatch_async block is created BEFORE the block is scheduled, captured by
// the copied block (which retains it), and the local ownership is released
// after scheduling — the queued block never touches a freed C string.
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
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


// TASK-003-06: public own-window test input and observations only. These
// functions compile only with both existing test tags, just like this file.
static NSWindow *hyAutomationOwnWindow(NSString *title) {
    for (NSWindow *window in [NSApp windows]) {
        if ([[window title] isEqualToString:title]) return window;
    }
    return nil;
}

void hyAutomationInstallChildProbe(const char *title, const char *source) {
    NSString *wanted = [[NSString alloc] initWithUTF8String:title];
    NSString *code = [[NSString alloc] initWithUTF8String:source];
    dispatch_async(dispatch_get_main_queue(), ^{
        NSWindow *window = hyAutomationOwnWindow(wanted);
        WKWebView *view = (WKWebView *)hyAutomationFindWebView([window contentView]);
        if (!view || !code) { hyAutomationEvent(7, 0); return; }
        @try {
            WKContentWorld *world = [WKContentWorld worldWithName:@"haeryong.task006.observer"];
            WKUserScript *probe = [[WKUserScript alloc]
                initWithSource:code injectionTime:WKUserScriptInjectionTimeAtDocumentStart
                forMainFrameOnly:NO inContentWorld:world];
            [view.configuration.userContentController addUserScript:probe];
            [probe release];
            // This acknowledges installation, not child execution or behavior.
            hyAutomationEvent(7, 1);
        } @catch (NSException *exception) {
            hyAutomationEvent(7, -1);
        }
    });
    [wanted release];
    [code release];
}

void hyAutomationResizeWindow(const char *title, double width, double height) {
    NSString *wanted = [[NSString alloc] initWithUTF8String:title];
    dispatch_async(dispatch_get_main_queue(), ^{
        NSWindow *window = hyAutomationOwnWindow(wanted);
        if (!window) { hyAutomationEvent(8, 0); return; }
        @try {
            [window setContentSize:NSMakeSize(width, height)];
            // The driver separately measures actual CSS viewport/geometry.
            hyAutomationEvent(8, 1);
        } @catch (NSException *exception) {
            hyAutomationEvent(8, 0);
        }
    });
    [wanted release];
}

void hyAutomationSnapshot(const char *title, const char *path) {
    NSString *wanted = [[NSString alloc] initWithUTF8String:title];
    NSString *output = [[NSString alloc] initWithUTF8String:path];
    dispatch_async(dispatch_get_main_queue(), ^{
        NSWindow *window = hyAutomationOwnWindow(wanted);
        WKWebView *view = (WKWebView *)hyAutomationFindWebView([window contentView]);
        if (!view) { hyAutomationEvent(9, 0); return; }
        @try {
            WKSnapshotConfiguration *configuration = [[WKSnapshotConfiguration alloc] init];
            configuration.afterScreenUpdates = YES;
            [view takeSnapshotWithConfiguration:configuration
                completionHandler:^(NSImage *image, NSError *error) {
                if (error || !image) { hyAutomationEvent(9, 0); return; }
                NSData *tiff = [image TIFFRepresentation];
                NSBitmapImageRep *bitmap = tiff ? [[NSBitmapImageRep alloc] initWithData:tiff] : nil;
                NSData *png = bitmap ? [bitmap representationUsingType:NSBitmapImageFileTypePNG
                    properties:@{}] : nil;
                BOOL written = png && [png length] > 0 && [png writeToFile:output atomically:YES];
                hyAutomationEvent(9, written ? (long)[png length] : 0);
                [bitmap release];
            }];
            [configuration release];
        } @catch (NSException *exception) {
            hyAutomationEvent(9, 0);
        }
    });
    [wanted release];
    [output release];
}

void hyAutomationKeyPress(const char *title, const char *key, int shift, const char *text) {
    NSString *wanted = [[NSString alloc] initWithUTF8String:title];
    NSString *name = [[NSString alloc] initWithUTF8String:key];
    NSString *typed = [[NSString alloc] initWithUTF8String:text];
    dispatch_async(dispatch_get_main_queue(), ^{
        NSWindow *window = hyAutomationOwnWindow(wanted);
        if (!window || ![window isKeyWindow]) { hyAutomationEvent(10, 0); return; }
        unsigned short code = 0;
        NSString *characters = nil;
        if ([name isEqualToString:@"tab"]) { code = 48; characters = @"\t"; }
        else if ([name isEqualToString:@"return"]) { code = 36; characters = @"\r"; }
        else if ([name isEqualToString:@"space"]) { code = 49; characters = @" "; }
        else if ([name isEqualToString:@"escape"]) { code = 53; characters = @"\x1b"; }
        else if ([name isEqualToString:@"left"]) { code = 123; characters = [NSString stringWithFormat:@"%C", (unichar)NSLeftArrowFunctionKey]; }
        else if ([name isEqualToString:@"right"]) { code = 124; characters = [NSString stringWithFormat:@"%C", (unichar)NSRightArrowFunctionKey]; }
        else if ([name isEqualToString:@"down"]) { code = 125; characters = [NSString stringWithFormat:@"%C", (unichar)NSDownArrowFunctionKey]; }
        else if ([name isEqualToString:@"up"]) { code = 126; characters = [NSString stringWithFormat:@"%C", (unichar)NSUpArrowFunctionKey]; }
        else if ([name isEqualToString:@"text"] && [typed length] > 0) { characters = typed; }
        if (!characters) { hyAutomationEvent(10, 0); return; }
        @try {
            // Send only to this application's already-key window. No global
            // event posting, privacy access or host keyboard setting change.
            NSEventModifierFlags modifiers = shift ? NSEventModifierFlagShift : 0;
            NSTimeInterval now = [[NSProcessInfo processInfo] systemUptime];
            NSEvent *down = [NSEvent keyEventWithType:NSEventTypeKeyDown
                location:NSZeroPoint modifierFlags:modifiers timestamp:now
                windowNumber:[window windowNumber] context:nil
                characters:characters charactersIgnoringModifiers:characters
                isARepeat:NO keyCode:code];
            NSEvent *up = [NSEvent keyEventWithType:NSEventTypeKeyUp
                location:NSZeroPoint modifierFlags:modifiers timestamp:now + .001
                windowNumber:[window windowNumber] context:nil
                characters:characters charactersIgnoringModifiers:characters
                isARepeat:NO keyCode:code];
            if (!down || !up) { hyAutomationEvent(10, 0); return; }
            [window sendEvent:down];
            [window sendEvent:up];
            // Dispatch alone is not evidence of WebKit focus/activation.
            hyAutomationEvent(10, 1);
        } @catch (NSException *exception) {
            hyAutomationEvent(10, 0);
        }
    });
    [wanted release];
    [name release];
    [typed release];
}
