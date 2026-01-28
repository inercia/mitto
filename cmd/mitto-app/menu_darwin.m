// menu_darwin.m - Objective-C implementation for macOS menu handling
// This file is compiled separately to avoid duplicate symbol issues with CGO

#import <Cocoa/Cocoa.h>

// Forward declaration of Go callback functions
extern void goMenuActionCallback(char* action);
extern void goQuitCallback(void);

// Custom menu action handler
@interface MittoMenuHandler : NSObject
+ (instancetype)sharedHandler;
- (void)newConversation:(id)sender;
- (void)closeConversation:(id)sender;
- (void)focusInput:(id)sender;
- (void)toggleSidebar:(id)sender;
- (void)showSettings:(id)sender;
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
        NSURLRequest *request = [NSURLRequest requestWithURL:url
                                                 cachePolicy:NSURLRequestReloadIgnoringLocalCacheData
                                             timeoutInterval:2.0];

        NSURLResponse *response = nil;
        NSError *error = nil;
        NSData *data = [NSURLConnection sendSynchronousRequest:request
                                             returningResponse:&response
                                                         error:&error];

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
