// menu_darwin.h - Header for macOS menu handling

#ifndef MENU_DARWIN_H
#define MENU_DARWIN_H

// setupMacOSMenu creates the standard macOS application menu.
// This must be called from the main thread after the app is running.
void setupMacOSMenu(const char* appName);

// setupQuitInterceptor sets up the NSApplication delegate to intercept quit requests.
// This must be called after the webview is created.
// confirmEnabled: if true, shows confirmation dialog when running sessions exist
// serverPort: the port number of the local HTTP server
void setupQuitInterceptor(int confirmEnabled, int serverPort);

// setWindowShowInAllSpaces configures the main window to appear in all macOS Spaces.
// enabled: if non-zero, the window will appear in all Spaces (virtual desktops)
// This must be called after the window is created.
void setWindowShowInAllSpaces(int enabled);

// activateApp activates the application and brings its window to the foreground.
// This should be called after the window is created to ensure the app gets focus on launch.
void activateApp(void);

// completeTermination signals that cleanup is complete and the app can terminate.
// This is called from Go after shutdown cleanup has finished.
void completeTermination(void);

// setupSwipeGestureRecognizer installs a two-finger horizontal swipe gesture
// recognizer on the main window. This allows navigating between conversations
// with trackpad swipes: swipe left goes to next, swipe right goes to previous.
void setupSwipeGestureRecognizer(void);

#endif // MENU_DARWIN_H

