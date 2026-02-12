// menu_darwin.m - Objective-C implementation for macOS menu handling
// This file is compiled separately to avoid duplicate symbol issues with CGO

#import <Cocoa/Cocoa.h>

// Forward declaration of Go callback functions
extern void goMenuActionCallback(char* action);
extern void goQuitCallback(void);
extern void goAppDidBecomeActiveCallback(void);

// Custom menu action handler
@interface MittoMenuHandler : NSObject
+ (instancetype)sharedHandler;
- (void)newConversation:(id)sender;
- (void)closeConversation:(id)sender;
- (void)focusInput:(id)sender;
- (void)toggleSidebar:(id)sender;
- (void)showSettings:(id)sender;
- (void)reloadWebView:(id)sender;
@end

@implementation MittoMenuHandler

+ (instancetype)sharedHandler {
    static MittoMenuHandler *handler = nil;
    static dispatch_once_t onceToken;
    dispatch_once(&onceToken, ^{
        handler = [[MittoMenuHandler alloc] init];
    });
    return handler;
}

- (void)newConversation:(id)sender {
    goMenuActionCallback((char*)"new_conversation");
}

- (void)closeConversation:(id)sender {
    goMenuActionCallback((char*)"close_conversation");
}

- (void)focusInput:(id)sender {
    goMenuActionCallback((char*)"focus_input");
}

- (void)toggleSidebar:(id)sender {
    goMenuActionCallback((char*)"toggle_sidebar");
}

- (void)showSettings:(id)sender {
    goMenuActionCallback((char*)"show_settings");
}

- (void)reloadWebView:(id)sender {
    goMenuActionCallback((char*)"reload_webview");
}

@end

// Application delegate for intercepting quit
@interface MittoAppDelegate : NSObject <NSApplicationDelegate>
@property (nonatomic) BOOL confirmQuitEnabled;
@property (nonatomic) int serverPort;
@property (nonatomic) BOOL isTerminating;
@end

@implementation MittoAppDelegate

- (NSApplicationTerminateReply)applicationShouldTerminate:(NSApplication *)sender {
    // If we're already terminating (cleanup complete), allow immediate termination
    if (self.isTerminating) {
        return NSTerminateNow;
    }

    // Check if confirmation is needed for running sessions
    if (self.confirmQuitEnabled) {
        // Check for running sessions by making a synchronous HTTP request to the local server
        NSString *urlString = [NSString stringWithFormat:@"http://127.0.0.1:%d/api/sessions/running", self.serverPort];
        NSURL *url = [NSURL URLWithString:urlString];
        NSMutableURLRequest *request = [NSMutableURLRequest requestWithURL:url
                                                               cachePolicy:NSURLRequestReloadIgnoringLocalCacheData
                                                           timeoutInterval:2.0];

        // Use a session with a dedicated delegate queue to avoid blocking the main thread's run loop
        // The shared session's completion handlers require the main run loop to be running,
        // which causes a deadlock when we're blocking with dispatch_semaphore_wait
        NSOperationQueue *delegateQueue = [[NSOperationQueue alloc] init];
        delegateQueue.maxConcurrentOperationCount = 1;

        NSURLSessionConfiguration *config = [NSURLSessionConfiguration ephemeralSessionConfiguration];
        config.timeoutIntervalForRequest = 2.0;
        config.timeoutIntervalForResource = 2.0;

        NSURLSession *session = [NSURLSession sessionWithConfiguration:config
                                                              delegate:nil
                                                         delegateQueue:delegateQueue];

        __block NSData *data = nil;
        __block NSError *error = nil;
        dispatch_semaphore_t semaphore = dispatch_semaphore_create(0);

        NSURLSessionDataTask *task = [session dataTaskWithRequest:request
                                                completionHandler:^(NSData *responseData, NSURLResponse *response, NSError *responseError) {
            data = responseData;
            error = responseError;
            dispatch_semaphore_signal(semaphore);
        }];
        [task resume];

        // Wait for the request to complete (max 2 seconds)
        dispatch_semaphore_wait(semaphore, dispatch_time(DISPATCH_TIME_NOW, 2 * NSEC_PER_SEC));

        // Clean up the session
        [session invalidateAndCancel];

        if (error == nil && data != nil) {
            // Parse JSON response
            NSError *jsonError = nil;
            NSDictionary *json = [NSJSONSerialization JSONObjectWithData:data
                                                                 options:0
                                                                   error:&jsonError];
            if (jsonError == nil && json != nil) {
                NSNumber *prompting = json[@"prompting"];
                int promptingCount = prompting ? [prompting intValue] : 0;

                // Show confirmation if there are agents actively responding
                if (promptingCount > 0) {
                    // Build alert message
                    NSString *message = [NSString stringWithFormat:
                        @"There %@ %d conversation%@ with an agent actively responding.\n\n"
                        @"Quitting now will interrupt the response and may lose unsaved work.",
                        promptingCount == 1 ? @"is" : @"are",
                        promptingCount,
                        promptingCount == 1 ? @"" : @"s"];

                    // Show alert
                    NSAlert *alert = [[NSAlert alloc] init];
                    [alert setMessageText:@"Quit Mitto?"];
                    [alert setInformativeText:message];
                    [alert setAlertStyle:NSAlertStyleWarning];
                    [alert addButtonWithTitle:@"Quit"];
                    [alert addButtonWithTitle:@"Cancel"];

                    NSModalResponse result = [alert runModal];

                    if (result != NSAlertFirstButtonReturn) {
                        return NSTerminateCancel;
                    }
                }
            }
        }
    }

    // User confirmed quit (or no confirmation needed)
    // Trigger Go cleanup callback and wait for it to complete
    // The callback will call completeTermination() when done
    goQuitCallback();

    // Return NSTerminateLater to defer termination until cleanup is complete
    return NSTerminateLater;
}

// Called when the app becomes active (user switches to it, clicks on dock icon, etc.)
// This is used to trigger a WebSocket reconnect/sync in the frontend since WKWebView
// doesn't fire visibilitychange events when the app is hidden/shown.
- (void)applicationDidBecomeActive:(NSNotification *)notification {
    NSLog(@"[Mitto] applicationDidBecomeActive called");
    goAppDidBecomeActiveCallback();
}

@end

// Global delegate instance (must be kept alive)
static MittoAppDelegate *gAppDelegate = nil;

// completeTermination signals that cleanup is complete and the app can terminate.
// This is called from Go after shutdown cleanup has finished.
void completeTermination(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        if (gAppDelegate != nil) {
            gAppDelegate.isTerminating = YES;
        }
        [[NSApplication sharedApplication] replyToApplicationShouldTerminate:YES];
    });
}

// setupMacOSMenu creates the standard macOS application menu with Quit option.
// This must be called from the main thread after the app is running.
void setupMacOSMenu(const char* appName) {
    @autoreleasepool {
        // Get the shared application
        NSApplication *app = [NSApplication sharedApplication];

        // Create the main menu bar
        NSMenu *menuBar = [[NSMenu alloc] init];
        [app setMainMenu:menuBar];

        // Create the application menu (first menu item)
        NSMenuItem *appMenuItem = [[NSMenuItem alloc] init];
        [menuBar addItem:appMenuItem];

        NSMenu *appMenu = [[NSMenu alloc] init];
        [appMenuItem setSubmenu:appMenu];

        // Get the shared menu handler for custom actions
        MittoMenuHandler *handler = [MittoMenuHandler sharedHandler];

        // Add "About" menu item
        NSString *aboutTitle = [NSString stringWithFormat:@"About %s", appName];
        NSMenuItem *aboutItem = [[NSMenuItem alloc] initWithTitle:aboutTitle
                                                           action:@selector(orderFrontStandardAboutPanel:)
                                                    keyEquivalent:@""];
        [appMenu addItem:aboutItem];

        [appMenu addItem:[NSMenuItem separatorItem]];

        // Add "Settings" menu item with Cmd+, shortcut (standard macOS convention)
        NSMenuItem *settingsItem = [[NSMenuItem alloc] initWithTitle:@"Settings..."
                                                              action:@selector(showSettings:)
                                                       keyEquivalent:@","];
        [settingsItem setTarget:handler];
        [appMenu addItem:settingsItem];

        [appMenu addItem:[NSMenuItem separatorItem]];

        // Add "Hide" menu item
        NSString *hideTitle = [NSString stringWithFormat:@"Hide %s", appName];
        NSMenuItem *hideItem = [[NSMenuItem alloc] initWithTitle:hideTitle
                                                          action:@selector(hide:)
                                                   keyEquivalent:@"h"];
        [appMenu addItem:hideItem];

        // Add "Hide Others" menu item
        NSMenuItem *hideOthersItem = [[NSMenuItem alloc] initWithTitle:@"Hide Others"
                                                                action:@selector(hideOtherApplications:)
                                                         keyEquivalent:@"h"];
        [hideOthersItem setKeyEquivalentModifierMask:NSEventModifierFlagCommand | NSEventModifierFlagOption];
        [appMenu addItem:hideOthersItem];

        // Add "Show All" menu item
        NSMenuItem *showAllItem = [[NSMenuItem alloc] initWithTitle:@"Show All"
                                                             action:@selector(unhideAllApplications:)
                                                      keyEquivalent:@""];
        [appMenu addItem:showAllItem];

        [appMenu addItem:[NSMenuItem separatorItem]];

        // Add "Quit" menu item with Cmd+Q shortcut
        NSString *quitTitle = [NSString stringWithFormat:@"Quit %s", appName];
        NSMenuItem *quitItem = [[NSMenuItem alloc] initWithTitle:quitTitle
                                                          action:@selector(terminate:)
                                                   keyEquivalent:@"q"];
        [appMenu addItem:quitItem];

        // Create File menu with New Conversation
        NSMenuItem *fileMenuItem = [[NSMenuItem alloc] init];
        [menuBar addItem:fileMenuItem];

        NSMenu *fileMenu = [[NSMenu alloc] initWithTitle:@"File"];
        [fileMenuItem setSubmenu:fileMenu];

        // Add "New Conversation" menu item with Cmd+N shortcut
        NSMenuItem *newConvoItem = [[NSMenuItem alloc] initWithTitle:@"New Conversation"
                                                              action:@selector(newConversation:)
                                                       keyEquivalent:@"n"];
        [newConvoItem setTarget:handler];
        [fileMenu addItem:newConvoItem];

        // Add "Close Conversation" menu item with Cmd+W shortcut
        NSMenuItem *closeConvoItem = [[NSMenuItem alloc] initWithTitle:@"Close Conversation"
                                                                action:@selector(closeConversation:)
                                                         keyEquivalent:@"w"];
        [closeConvoItem setTarget:handler];
        [fileMenu addItem:closeConvoItem];

        // Create Edit menu (for copy/paste support in WebView)
        NSMenuItem *editMenuItem = [[NSMenuItem alloc] init];
        [menuBar addItem:editMenuItem];

        NSMenu *editMenu = [[NSMenu alloc] initWithTitle:@"Edit"];
        [editMenuItem setSubmenu:editMenu];

        // Add standard edit menu items
        NSMenuItem *undoItem = [[NSMenuItem alloc] initWithTitle:@"Undo"
                                                          action:@selector(undo:)
                                                   keyEquivalent:@"z"];
        [editMenu addItem:undoItem];

        NSMenuItem *redoItem = [[NSMenuItem alloc] initWithTitle:@"Redo"
                                                          action:@selector(redo:)
                                                   keyEquivalent:@"Z"];
        [editMenu addItem:redoItem];

        [editMenu addItem:[NSMenuItem separatorItem]];

        NSMenuItem *cutItem = [[NSMenuItem alloc] initWithTitle:@"Cut"
                                                         action:@selector(cut:)
                                                  keyEquivalent:@"x"];
        [editMenu addItem:cutItem];

        NSMenuItem *copyItem = [[NSMenuItem alloc] initWithTitle:@"Copy"
                                                          action:@selector(copy:)
                                                   keyEquivalent:@"c"];
        [editMenu addItem:copyItem];

        NSMenuItem *pasteItem = [[NSMenuItem alloc] initWithTitle:@"Paste"
                                                           action:@selector(paste:)
                                                    keyEquivalent:@"v"];
        [editMenu addItem:pasteItem];

        NSMenuItem *selectAllItem = [[NSMenuItem alloc] initWithTitle:@"Select All"
                                                               action:@selector(selectAll:)
                                                        keyEquivalent:@"a"];
        [editMenu addItem:selectAllItem];

        // Create View menu
        NSMenuItem *viewMenuItem = [[NSMenuItem alloc] init];
        [menuBar addItem:viewMenuItem];

        NSMenu *viewMenu = [[NSMenu alloc] initWithTitle:@"View"];
        [viewMenuItem setSubmenu:viewMenu];

        // Add "Toggle Sidebar" menu item with Cmd+Shift+S shortcut
        NSMenuItem *toggleSidebarItem = [[NSMenuItem alloc] initWithTitle:@"Toggle Sidebar"
                                                                   action:@selector(toggleSidebar:)
                                                            keyEquivalent:@"s"];
        [toggleSidebarItem setKeyEquivalentModifierMask:NSEventModifierFlagCommand | NSEventModifierFlagShift];
        [toggleSidebarItem setTarget:handler];
        [viewMenu addItem:toggleSidebarItem];

        // Add "Focus Input" menu item with Cmd+L shortcut (like browser address bar)
        NSMenuItem *focusInputItem = [[NSMenuItem alloc] initWithTitle:@"Focus Input"
                                                                action:@selector(focusInput:)
                                                         keyEquivalent:@"l"];
        [focusInputItem setTarget:handler];
        [viewMenu addItem:focusInputItem];

        [viewMenu addItem:[NSMenuItem separatorItem]];

        // Add "Reload" menu item with Cmd+R shortcut (standard browser reload)
        NSMenuItem *reloadItem = [[NSMenuItem alloc] initWithTitle:@"Reload"
                                                            action:@selector(reloadWebView:)
                                                     keyEquivalent:@"r"];
        [reloadItem setTarget:handler];
        [viewMenu addItem:reloadItem];

        // Create Window menu
        NSMenuItem *windowMenuItem = [[NSMenuItem alloc] init];
        [menuBar addItem:windowMenuItem];

        NSMenu *windowMenu = [[NSMenu alloc] initWithTitle:@"Window"];
        [windowMenuItem setSubmenu:windowMenu];
        [app setWindowsMenu:windowMenu];

        NSMenuItem *minimizeItem = [[NSMenuItem alloc] initWithTitle:@"Minimize"
                                                              action:@selector(performMiniaturize:)
                                                       keyEquivalent:@"m"];
        [windowMenu addItem:minimizeItem];

        NSMenuItem *zoomItem = [[NSMenuItem alloc] initWithTitle:@"Zoom"
                                                          action:@selector(performZoom:)
                                                   keyEquivalent:@""];
        [windowMenu addItem:zoomItem];

        [windowMenu addItem:[NSMenuItem separatorItem]];

        NSMenuItem *frontItem = [[NSMenuItem alloc] initWithTitle:@"Bring All to Front"
                                                           action:@selector(arrangeInFront:)
                                                    keyEquivalent:@""];
        [windowMenu addItem:frontItem];
    }
}

// setupQuitInterceptor sets up the NSApplication delegate to intercept quit requests.
void setupQuitInterceptor(int confirmEnabled, int serverPort) {
    @autoreleasepool {
        NSApplication *app = [NSApplication sharedApplication];

        // Create and configure the delegate
        gAppDelegate = [[MittoAppDelegate alloc] init];
        gAppDelegate.confirmQuitEnabled = (confirmEnabled != 0);
        gAppDelegate.serverPort = serverPort;

        // Set as the application delegate
        [app setDelegate:gAppDelegate];
    }
}

// setQuitConfirmEnabled updates the quit confirmation setting at runtime.
void setQuitConfirmEnabled(int enabled) {
    dispatch_async(dispatch_get_main_queue(), ^{
        if (gAppDelegate != nil) {
            gAppDelegate.confirmQuitEnabled = (enabled != 0);
        }
    });
}

// setWindowShowInAllSpaces configures the main window to appear in all macOS Spaces.
void setWindowShowInAllSpaces(int enabled) {
    @autoreleasepool {
        NSWindow *window = [[NSApplication sharedApplication] mainWindow];
        if (window) {
            if (enabled) {
                // Add canJoinAllSpaces to existing collection behavior
                window.collectionBehavior |= NSWindowCollectionBehaviorCanJoinAllSpaces;
            } else {
                // Remove canJoinAllSpaces from collection behavior
                window.collectionBehavior &= ~NSWindowCollectionBehaviorCanJoinAllSpaces;
            }
        }
    }
}

// activateApp activates the application and brings its window to the foreground.
// This ensures the app gets focus when launched.
void activateApp(void) {
    @autoreleasepool {
        NSApplication *app = [NSApplication sharedApplication];
        [app activateIgnoringOtherApps:YES];
        NSWindow *window = [app mainWindow];
        if (window) {
            [window makeKeyAndOrderFront:nil];
        }
    }
}

// Forward declaration of Go callback for swipe navigation
extern void goSwipeNavigationCallback(char* direction);

// Swipe gesture state tracking
static CGFloat gSwipeAccumulatedDeltaX = 0.0;
static CGFloat gSwipeAccumulatedDeltaY = 0.0;
static BOOL gSwipeInProgress = NO;

// setupSwipeGestureRecognizer installs a scroll event monitor that tracks
// horizontal two-finger swipe gestures to navigate between conversations:
// - Swipe left (fingers move left) -> Go to next conversation
// - Swipe right (fingers move right) -> Go to previous conversation
//
// The implementation tracks accumulated scroll delta during a gesture and only
// triggers navigation when the gesture ends with a significant horizontal
// displacement and minimal vertical displacement.
void setupSwipeGestureRecognizer(void) {
    @autoreleasepool {
        // Monitor scroll wheel events to detect two-finger swipe gestures
        // We track the full gesture from start to end to accumulate the total delta
        [NSEvent addLocalMonitorForEventsMatchingMask:NSEventMaskScrollWheel handler:^NSEvent *(NSEvent *event) {
            // Only process trackpad events (not mouse scroll wheel)
            // Trackpad events have a phase, mouse events don't
            if (event.phase == NSEventPhaseNone && event.momentumPhase == NSEventPhaseNone) {
                return event;
            }

            // Gesture phases:
            // NSEventPhaseBegan - fingers down, gesture starting
            // NSEventPhaseChanged - fingers moving
            // NSEventPhaseEnded - fingers lifted
            // NSEventPhaseMayBegin - system is determining if this is a gesture

            if (event.phase == NSEventPhaseBegan || event.phase == NSEventPhaseMayBegin) {
                // Start of a new gesture - reset accumulators
                gSwipeAccumulatedDeltaX = 0.0;
                gSwipeAccumulatedDeltaY = 0.0;
                gSwipeInProgress = YES;
            }

            if (gSwipeInProgress && (event.phase == NSEventPhaseChanged || event.phase == NSEventPhaseBegan)) {
                // Accumulate deltas during the gesture
                gSwipeAccumulatedDeltaX += event.scrollingDeltaX;
                gSwipeAccumulatedDeltaY += event.scrollingDeltaY;
            }

            if (event.phase == NSEventPhaseEnded && gSwipeInProgress) {
                gSwipeInProgress = NO;

                // Threshold for triggering navigation (in pixels)
                // Require a deliberate horizontal swipe with minimal vertical movement
                CGFloat horizontalThreshold = 100.0;  // Significant horizontal movement required
                CGFloat verticalLimit = 50.0;         // Maximum vertical movement allowed

                CGFloat absX = fabs(gSwipeAccumulatedDeltaX);
                CGFloat absY = fabs(gSwipeAccumulatedDeltaY);

                // Only trigger if:
                // 1. Horizontal movement exceeds threshold
                // 2. Horizontal movement is dominant (at least 2x vertical)
                // 3. Vertical movement is within limits
                if (absX > horizontalThreshold && absX > absY * 2.0 && absY < verticalLimit) {
                    // Natural scrolling: positive deltaX means fingers moved right
                    // In natural scrolling: swipe right = scroll content left = go to previous
                    // Swipe right (fingers right, deltaX > 0) -> prev conversation
                    // Swipe left (fingers left, deltaX < 0) -> next conversation
                    if (gSwipeAccumulatedDeltaX > 0) {
                        goSwipeNavigationCallback((char*)"prev");
                    } else {
                        goSwipeNavigationCallback((char*)"next");
                    }
                }

                // Reset accumulators
                gSwipeAccumulatedDeltaX = 0.0;
                gSwipeAccumulatedDeltaY = 0.0;
            }

            if (event.phase == NSEventPhaseCancelled) {
                // Gesture was cancelled - reset state
                gSwipeInProgress = NO;
                gSwipeAccumulatedDeltaX = 0.0;
                gSwipeAccumulatedDeltaY = 0.0;
            }

            // Always return the event so it can be processed normally by the WebView
            // This ensures normal scrolling still works within the content
            return event;
        }];
    }
}

// Window delegate to prevent fullscreen and handle zoom button clicks
@interface MittoWindowDelegate : NSObject <NSWindowDelegate>
@end

@implementation MittoWindowDelegate

// Allow normal zoom behavior
- (BOOL)windowShouldZoom:(NSWindow *)window toFrame:(NSRect)newFrame {
    return YES;
}

// Provide the standard frame for zoom
- (NSRect)windowWillUseStandardFrame:(NSWindow *)window defaultFrame:(NSRect)newFrame {
    return newFrame;
}

@end

// Global window delegate instance (must be kept alive)
static MittoWindowDelegate *gWindowDelegate = nil;
// Observer for window notifications
static id gWindowObserver = nil;

// Helper function to configure a window to disallow fullscreen
static void configureWindowNoFullscreen(NSWindow *window) {
    if (!window) return;

    // Set collection behavior to explicitly disallow fullscreen
    NSWindowCollectionBehavior behavior = window.collectionBehavior;
    // Clear any fullscreen-related behaviors
    behavior &= ~(NSWindowCollectionBehaviorFullScreenPrimary |
                 NSWindowCollectionBehaviorFullScreenAuxiliary |
                 NSWindowCollectionBehaviorFullScreenAllowsTiling);
    // Set to explicitly disallow fullscreen
    behavior |= NSWindowCollectionBehaviorFullScreenNone;
    window.collectionBehavior = behavior;

    // Set our delegate if not already set
    if (gWindowDelegate == nil) {
        gWindowDelegate = [[MittoWindowDelegate alloc] init];
    }
    if (window.delegate != gWindowDelegate) {
        window.delegate = gWindowDelegate;
    }
}

// disableWindowFullscreen prevents the window from entering fullscreen mode.
// This removes the fullscreen capability entirely from the window.
void disableWindowFullscreen(void) {
    @autoreleasepool {
        NSApplication *app = [NSApplication sharedApplication];

        // Register an observer to catch any window that becomes visible
        // This ensures we catch windows created by webview libraries
        if (gWindowObserver == nil) {
            gWindowObserver = [[NSNotificationCenter defaultCenter]
                addObserverForName:NSWindowDidBecomeKeyNotification
                object:nil
                queue:[NSOperationQueue mainQueue]
                usingBlock:^(NSNotification *notification) {
                    NSWindow *w = notification.object;
                    if (w) {
                        configureWindowNoFullscreen(w);
                    }
                }];
        }

        // Also observe window did become main
        static id mainObserver = nil;
        if (mainObserver == nil) {
            mainObserver = [[NSNotificationCenter defaultCenter]
                addObserverForName:NSWindowDidBecomeMainNotification
                object:nil
                queue:[NSOperationQueue mainQueue]
                usingBlock:^(NSNotification *notification) {
                    NSWindow *w = notification.object;
                    if (w) {
                        configureWindowNoFullscreen(w);
                    }
                }];
        }

        // Try to find and configure existing windows
        NSArray *windows = [app windows];
        for (NSWindow *window in windows) {
            configureWindowNoFullscreen(window);
        }

        // Also configure main window if available
        NSWindow *mainWindow = [app mainWindow];
        if (mainWindow) {
            configureWindowNoFullscreen(mainWindow);
        }

        // Apply again after delays to override any webview library settings
        void (^applyToAllWindows)(void) = ^{
            NSArray *ws = [[NSApplication sharedApplication] windows];
            for (NSWindow *w in ws) {
                configureWindowNoFullscreen(w);
            }
            NSWindow *main = [[NSApplication sharedApplication] mainWindow];
            if (main) {
                configureWindowNoFullscreen(main);
            }
        };

        dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(0.1 * NSEC_PER_SEC)),
                      dispatch_get_main_queue(), applyToAllWindows);
        dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(0.3 * NSEC_PER_SEC)),
                      dispatch_get_main_queue(), applyToAllWindows);
        dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(0.5 * NSEC_PER_SEC)),
                      dispatch_get_main_queue(), applyToAllWindows);
        dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(1.0 * NSEC_PER_SEC)),
                      dispatch_get_main_queue(), applyToAllWindows);
        dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(2.0 * NSEC_PER_SEC)),
                      dispatch_get_main_queue(), applyToAllWindows);
    }
}
