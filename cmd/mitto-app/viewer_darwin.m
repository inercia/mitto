// viewer_darwin.m - Native viewer window for Mitto using WKWebView
// Opens files in a dedicated native macOS window with a WKWebView.
// Multiple viewer windows can be open simultaneously.

#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>

// Script message handler for viewer windows.
// Handles messages from JavaScript: "closeViewer" and "openFileURL".
@interface MittoViewerScriptHandler : NSObject <WKScriptMessageHandler>
@property (assign) NSWindow *window;
@end

@implementation MittoViewerScriptHandler

- (void)userContentController:(WKUserContentController *)controller
      didReceiveScriptMessage:(WKScriptMessage *)message {
    if ([message.name isEqualToString:@"closeViewer"]) {
        // Close the viewer window
        if (self.window) {
            [self.window close];
        }
    } else if ([message.name isEqualToString:@"openFileURL"]) {
        // Open a file URL with the system default application
        NSString *urlStr = message.body;
        if ([urlStr isKindOfClass:[NSString class]] && urlStr.length > 0) {
            NSURL *fileURL = [NSURL URLWithString:urlStr];
            if (fileURL) {
                [[NSWorkspace sharedWorkspace] openURL:fileURL];
            }
        }
    }
}

@end

// Window delegate that releases the window when closed.
@interface MittoViewerWindowDelegate : NSObject <NSWindowDelegate>
@property (strong) MittoViewerScriptHandler *scriptHandler;
@end

@implementation MittoViewerWindowDelegate

- (void)windowWillClose:(NSNotification *)notification {
    NSWindow *window = notification.object;
    if (window) {
        // Remove the delegate reference so the window can be deallocated.
        window.delegate = nil;
    }
    // The window is retained by NSApplication's window list while open.
    // Once closed and removed from that list, ARC will release it naturally.
}

@end

// openViewerWindow opens a new native viewer window that loads the given URL.
// Must be called from the main thread, or dispatched to it.
void openViewerWindow(const char* url, const char* title, int width, int height) {
    @autoreleasepool {
        NSString *urlString = [NSString stringWithUTF8String:url];
        NSString *titleString = [NSString stringWithUTF8String:title];

        int windowWidth  = (width  > 0) ? width  : 1000;
        int windowHeight = (height > 0) ? height : 750;

        dispatch_async(dispatch_get_main_queue(), ^{
            @autoreleasepool {
                // Create the window.
                NSRect frame = NSMakeRect(0, 0, windowWidth, windowHeight);
                NSWindowStyleMask styleMask = NSWindowStyleMaskTitled
                                           | NSWindowStyleMaskClosable
                                           | NSWindowStyleMaskResizable
                                           | NSWindowStyleMaskMiniaturizable;

                NSWindow *window = [[NSWindow alloc] initWithContentRect:frame
                                                               styleMask:styleMask
                                                                 backing:NSBackingStoreBuffered
                                                                   defer:NO];
                [window setTitle:titleString];
                [window setMinSize:NSMakeSize(480, 320)];

                // The window should be released when closed (not retained after close).
                window.releasedWhenClosed = NO;

                // Configure WKWebView with script message handlers.
                WKWebViewConfiguration *config = [[WKWebViewConfiguration alloc] init];

                // Create script message handler for native bridge functions.
                MittoViewerScriptHandler *scriptHandler = [[MittoViewerScriptHandler alloc] init];
                scriptHandler.window = window;

                // Register message handlers for closeViewer and openFileURL.
                [config.userContentController addScriptMessageHandler:scriptHandler
                                                                 name:@"closeViewer"];
                [config.userContentController addScriptMessageHandler:scriptHandler
                                                                 name:@"openFileURL"];

                // Inject JavaScript bridge functions that the viewer page can call.
                // These create window.mittoCloseViewer() and window.mittoOpenFileURL()
                // which post messages to the native script handler.
                NSString *bridgeScript = @"window.mittoCloseViewer = function() { "
                    @"window.webkit.messageHandlers.closeViewer.postMessage('close'); }; "
                    @"window.mittoOpenFileURL = function(url) { "
                    @"window.webkit.messageHandlers.openFileURL.postMessage(url); }; "
                    @"window.mittoIsNativeViewer = true;";
                WKUserScript *userScript = [[WKUserScript alloc]
                    initWithSource:bridgeScript
                     injectionTime:WKUserScriptInjectionTimeAtDocumentStart
                  forMainFrameOnly:YES];
                [config.userContentController addUserScript:userScript];

                // Set a user agent so the viewer page knows it is inside the native app.
                NSString *userAgent = @"Mitto/1.0 macOS Native Viewer";

                WKWebView *webView = [[WKWebView alloc] initWithFrame:frame
                                                        configuration:config];
                webView.customUserAgent = userAgent;

                // Stretch the web view to fill the entire window content area.
                webView.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;

                [window setContentView:webView];

                // Load the requested URL.
                NSURL *nsURL = [NSURL URLWithString:urlString];
                if (nsURL != nil) {
                    NSURLRequest *request = [NSURLRequest requestWithURL:nsURL];
                    [webView loadRequest:request];
                }

                // Set window delegate to handle close cleanly.
                // The delegate retains the scriptHandler to prevent it from being deallocated.
                MittoViewerWindowDelegate *delegate = [[MittoViewerWindowDelegate alloc] init];
                delegate.scriptHandler = scriptHandler;
                window.delegate = delegate;

                // Center the window on screen and bring it to front.
                [window center];
                [window makeKeyAndOrderFront:nil];

                // Activate the app so the viewer window gets focus.
                [[NSApplication sharedApplication] activateIgnoringOtherApps:YES];
            }
        });
    }
}
