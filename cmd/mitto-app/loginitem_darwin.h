// loginitem_darwin.h - Header for macOS login item (start at login) functionality
//
// This module provides functions to manage the app's login item status using
// LaunchAgents. A plist file is created in ~/Library/LaunchAgents/ to start
// the app automatically when the user logs in. This approach works on all
// macOS versions.

#ifndef LOGINITEM_DARWIN_H
#define LOGINITEM_DARWIN_H

#include <stdbool.h>

// isLoginItemSupported checks if the login item API is available.
// Always returns true since LaunchAgents work on all macOS versions.
bool isLoginItemSupported(void);

// isLoginItemEnabled checks if the app is currently registered as a login item.
// Returns true if the LaunchAgent plist exists, false otherwise.
bool isLoginItemEnabled(void);

// enableLoginItem registers the app as a login item by creating a LaunchAgent.
// Returns 0 on success, or a positive error code on failure.
int enableLoginItem(void);

// disableLoginItem unregisters the app as a login item by removing the LaunchAgent.
// Returns 0 on success, or a positive error code on failure.
int disableLoginItem(void);

#endif // LOGINITEM_DARWIN_H

