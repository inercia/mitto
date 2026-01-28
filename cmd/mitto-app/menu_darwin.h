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

#endif // MENU_DARWIN_H

