//go:build darwin

// Package main provides the entry point for the Mitto macOS desktop application.
//
// This is a native macOS app wrapper that embeds the existing web interface
// in a WebView window. It starts the internal web server on a random localhost
// port and displays the UI in a native window.
//
// Build requirements:
//   - macOS with Command Line Tools installed
//   - CGO_ENABLED=1 (required for webview)
//
// The app reuses 100% of the existing internal/web, internal/acp, and
// internal/session packages without modification.
package main

/*
#cgo darwin CFLAGS: -x objective-c
#cgo darwin LDFLAGS: -framework Cocoa -framework Carbon -framework UniformTypeIdentifiers -framework UserNotifications

#import <Cocoa/Cocoa.h>
#import <Carbon/Carbon.h>
#import <UniformTypeIdentifiers/UniformTypeIdentifiers.h>
#include "menu_darwin.h"
#include "loginitem_darwin.h"
#include "notifications_darwin.h"

// Global hotkey reference (static to avoid duplicate symbols)
static EventHotKeyRef gHotKeyRef = NULL;
static EventHandlerUPP gHotKeyHandlerUPP = NULL;

// Forward declaration of Go callback for window shown event
extern void goWindowShownCallback(void);

// Hotkey event handler - toggles app visibility
static OSStatus hotKeyHandler(EventHandlerCallRef nextHandler, EventRef theEvent, void *userData) {
    NSApplication *app = [NSApplication sharedApplication];

    // Check if app is active and visible
    if ([app isActive] && [[app mainWindow] isVisible]) {
        // Hide the app
        [app hide:nil];
    } else {
        // Show and activate the app
        [app activateIgnoringOtherApps:YES];
        [[app mainWindow] makeKeyAndOrderFront:nil];
        // Notify Go that window was shown so it can focus the input
        goWindowShownCallback();
    }

    return noErr;
}

// registerGlobalHotkeyWithParams registers a global hotkey with the given key code and modifiers.
// keyCode: macOS virtual key code (e.g., 46 for 'M')
// modifiers: combination of cmdKey, shiftKey, optionKey, controlKey
// Returns 0 on success, non-zero on failure
static inline int registerGlobalHotkeyWithParams(UInt32 keyCode, UInt32 modifiers) {
    // Create the event type spec for hotkey events
    EventTypeSpec eventType;
    eventType.eventClass = kEventClassKeyboard;
    eventType.eventKind = kEventHotKeyPressed;

    // Install the event handler
    gHotKeyHandlerUPP = NewEventHandlerUPP(hotKeyHandler);
    OSStatus status = InstallApplicationEventHandler(
        gHotKeyHandlerUPP,
        1,
        &eventType,
        NULL,
        NULL
    );

    if (status != noErr) {
        return (int)status;
    }

    // Register the hotkey
    EventHotKeyID hotKeyID;
    hotKeyID.signature = 'MTTO';  // Unique signature for Mitto
    hotKeyID.id = 1;

    status = RegisterEventHotKey(
        keyCode,
        modifiers,
        hotKeyID,
        GetApplicationEventTarget(),
        0,
        &gHotKeyRef
    );

    return (int)status;
}

// unregisterGlobalHotkey unregisters the global hotkey
static inline void unregisterGlobalHotkey(void) {
    if (gHotKeyRef != NULL) {
        UnregisterEventHotKey(gHotKeyRef);
        gHotKeyRef = NULL;
    }
    if (gHotKeyHandlerUPP != NULL) {
        DisposeEventHandlerUPP(gHotKeyHandlerUPP);
        gHotKeyHandlerUPP = NULL;
    }
}

// openURLInBrowser opens a URL in the default browser
static inline void openURLInBrowser(const char* urlStr) {
    @autoreleasepool {
        NSString *urlString = [NSString stringWithUTF8String:urlStr];
        NSURL *url = [NSURL URLWithString:urlString];
        if (url != nil) {
            [[NSWorkspace sharedWorkspace] openURL:url];
        }
    }
}

// openFolderPicker opens a native folder picker dialog and returns the selected path.
// Returns NULL if the user cancels or an error occurs.
// The caller is responsible for freeing the returned string.
// Note: This function is called from webview binding callbacks which already run on the main thread.
static inline char* openFolderPicker(void) {
    char* result = NULL;

    @autoreleasepool {
        NSOpenPanel *panel = [NSOpenPanel openPanel];
        [panel setCanChooseFiles:NO];
        [panel setCanChooseDirectories:YES];
        [panel setAllowsMultipleSelection:NO];
        [panel setMessage:@"Select a workspace directory"];
        [panel setPrompt:@"Select"];

        // Run the panel modally - we're already on the main thread from the webview callback
        NSModalResponse response = [panel runModal];

        if (response == NSModalResponseOK) {
            NSURL *url = [[panel URLs] firstObject];
            if (url != nil) {
                NSString *path = [url path];
                if (path != nil) {
                    const char *pathStr = [path UTF8String];
                    if (pathStr != NULL) {
                        result = strdup(pathStr);
                    }
                }
            }
        }
    }

    return result;
}

// openImagePicker opens a native file picker dialog for selecting images.
// Returns a JSON array of selected file paths, or NULL if cancelled.
// The caller is responsible for freeing the returned string.
// Note: This function is called from webview binding callbacks which already run on the main thread.
static inline char* openImagePicker(void) {
    char* result = NULL;

    @autoreleasepool {
        NSOpenPanel *panel = [NSOpenPanel openPanel];
        [panel setCanChooseFiles:YES];
        [panel setCanChooseDirectories:NO];
        [panel setAllowsMultipleSelection:YES];
        [panel setMessage:@"Select images to attach"];
        [panel setPrompt:@"Attach"];

        // Set allowed content types for images (using modern UTType API)
        if (@available(macOS 11.0, *)) {
            NSArray *imageTypes = @[UTTypeImage, UTTypePNG, UTTypeJPEG, UTTypeGIF, UTTypeWebP];
            [panel setAllowedContentTypes:imageTypes];
        } else {
            // Fallback for older macOS versions
            #pragma clang diagnostic push
            #pragma clang diagnostic ignored "-Wdeprecated-declarations"
            NSArray *imageTypes = @[@"png", @"jpg", @"jpeg", @"gif", @"webp"];
            [panel setAllowedFileTypes:imageTypes];
            #pragma clang diagnostic pop
        }

        // Run the panel modally - we're already on the main thread from the webview callback
        NSModalResponse response = [panel runModal];

        if (response == NSModalResponseOK) {
            NSArray *urls = [panel URLs];
            if (urls != nil && [urls count] > 0) {
                // Build a JSON array of file paths
                NSMutableArray *paths = [NSMutableArray arrayWithCapacity:[urls count]];
                for (NSURL *url in urls) {
                    NSString *path = [url path];
                    if (path != nil) {
                        [paths addObject:path];
                    }
                }

                // Convert to JSON
                NSError *error = nil;
                NSData *jsonData = [NSJSONSerialization dataWithJSONObject:paths
                                                                   options:0
                                                                     error:&error];
                if (jsonData != nil && error == nil) {
                    NSString *jsonString = [[NSString alloc] initWithData:jsonData
                                                                 encoding:NSUTF8StringEncoding];
                    if (jsonString != nil) {
                        result = strdup([jsonString UTF8String]);
                    }
                }
            }
        }
    }

    return result;
}
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	webview "github.com/webview/webview_go"

	embeddedconfig "github.com/inercia/mitto/config"
	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/auxiliary"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/hooks"
	"github.com/inercia/mitto/internal/logging"
	"github.com/inercia/mitto/internal/web"
)

// Global webview reference for menu action callbacks
var (
	globalWebView   webview.WebView
	globalWebViewMu sync.Mutex
)

// Global shutdown manager reference for quit callback
var (
	globalShutdown   *hooks.ShutdownManager
	globalShutdownMu sync.Mutex
)

//export goMenuActionCallback
func goMenuActionCallback(action *C.char) {
	globalWebViewMu.Lock()
	w := globalWebView
	globalWebViewMu.Unlock()

	if w == nil {
		return
	}

	actionStr := C.GoString(action)
	var js string

	switch actionStr {
	case "new_conversation":
		// Call the global function exposed by the frontend
		js = "if (window.mittoNewConversation) window.mittoNewConversation();"
	case "close_conversation":
		// Close the current conversation
		js = "if (window.mittoCloseConversation) window.mittoCloseConversation();"
	case "focus_input":
		// Focus the chat input textarea
		js = "if (window.mittoFocusInput) window.mittoFocusInput();"
	case "toggle_sidebar":
		// Toggle the sidebar visibility
		js = "if (window.mittoToggleSidebar) window.mittoToggleSidebar();"
	case "show_settings":
		// Open the settings dialog
		js = "if (window.mittoShowSettings) window.mittoShowSettings();"
	default:
		slog.Warn("Unknown menu action", "action", actionStr)
		return
	}

	// Dispatch to main thread and evaluate JavaScript
	w.Dispatch(func() {
		w.Eval(js)
	})
}

//export goWindowShownCallback
func goWindowShownCallback() {
	globalWebViewMu.Lock()
	w := globalWebView
	globalWebViewMu.Unlock()

	if w == nil {
		return
	}

	// Focus the input after a short delay to ensure the window is fully visible
	w.Dispatch(func() {
		w.Eval("setTimeout(function() { if (window.mittoFocusInput) window.mittoFocusInput(); }, 100);")
	})
}

//export goQuitCallback
func goQuitCallback() {
	// Run shutdown in a goroutine to avoid blocking the main thread
	go func() {
		globalShutdownMu.Lock()
		shutdown := globalShutdown
		globalShutdownMu.Unlock()

		if shutdown != nil {
			// Trigger the shutdown sequence (stops hooks, runs cleanup)
			shutdown.Shutdown("app_quit")
		}

		// Signal to macOS that cleanup is complete and the app can terminate
		C.completeTermination()
	}()
}

//export goSwipeNavigationCallback
func goSwipeNavigationCallback(direction *C.char) {
	globalWebViewMu.Lock()
	w := globalWebView
	globalWebViewMu.Unlock()

	if w == nil {
		return
	}

	dirStr := C.GoString(direction)
	var js string

	switch dirStr {
	case "next":
		// Swipe left -> go to next conversation
		js = "if (window.mittoNextConversation) window.mittoNextConversation();"
	case "prev":
		// Swipe right -> go to previous conversation
		js = "if (window.mittoPrevConversation) window.mittoPrevConversation();"
	default:
		slog.Warn("Unknown swipe direction", "direction", dirStr)
		return
	}

	// Dispatch to main thread and evaluate JavaScript
	w.Dispatch(func() {
		w.Eval(js)
	})
}

//export goNotificationPermissionCallback
func goNotificationPermissionCallback(granted C.int) {
	// This callback is called asynchronously when permission request completes
	// Currently we don't need to do anything here as the frontend will
	// check permission status when needed
	if granted != 0 {
		slog.Info("Notification permission granted")
	} else {
		slog.Info("Notification permission denied")
	}
}

//export goNotificationTappedCallback
func goNotificationTappedCallback(sessionId *C.char) {
	globalWebViewMu.Lock()
	w := globalWebView
	globalWebViewMu.Unlock()

	if w == nil {
		return
	}

	sessionIdStr := C.GoString(sessionId)
	slog.Debug("Notification tapped", "session_id", sessionIdStr)

	// Bring app to foreground and switch to the session
	w.Dispatch(func() {
		// First activate the app window
		C.activateApp()
		// Then switch to the session that was tapped
		js := fmt.Sprintf("if (window.mittoSwitchToSession) window.mittoSwitchToSession('%s');", sessionIdStr)
		w.Eval(js)
	})
}

// initNotifications initializes the notification center.
// Must be called after the app is running.
func initNotifications() {
	C.initNotificationCenter()
	slog.Debug("Notification center initialized")
}

// requestNotificationPermission requests permission to show notifications.
// This is exposed to JavaScript via webview.Bind.
func requestNotificationPermission() {
	C.requestNotificationPermission()
}

// getNotificationPermissionStatus returns the current notification permission status.
// Returns: 0 = not determined, 1 = denied, 2 = authorized
// This is exposed to JavaScript via webview.Bind.
func getNotificationPermissionStatus() int {
	return int(C.getNotificationPermissionStatus())
}

// NotificationResult represents the result of showing a notification.
type NotificationResult struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// showNativeNotification posts a notification to the macOS Notification Center.
// This is exposed to JavaScript via webview.Bind.
func showNativeNotification(title, body, sessionId string) NotificationResult {
	cTitle := C.CString(title)
	defer C.free(unsafe.Pointer(cTitle))
	cBody := C.CString(body)
	defer C.free(unsafe.Pointer(cBody))
	cSessionId := C.CString(sessionId)
	defer C.free(unsafe.Pointer(cSessionId))

	result := C.showNativeNotification(cTitle, cBody, cSessionId)
	if result != 0 {
		return NotificationResult{Success: false, Error: "Failed to show notification"}
	}
	return NotificationResult{Success: true}
}

// removeNotificationsForSession removes all delivered notifications for a session.
// This is exposed to JavaScript via webview.Bind.
func removeNotificationsForSession(sessionId string) {
	cSessionId := C.CString(sessionId)
	defer C.free(unsafe.Pointer(cSessionId))
	C.removeNotificationsForSession(cSessionId)
}

// setupMenu creates the native macOS menu bar with standard items.
func setupMenu(appName string) {
	cAppName := C.CString(appName)
	defer C.free(unsafe.Pointer(cAppName))
	C.setupMacOSMenu(cAppName)
}

// setupQuitInterceptor sets up the quit confirmation dialog.
// confirmEnabled: whether to show confirmation when running sessions exist
// serverPort: the port number of the local HTTP server
func setupQuitInterceptor(confirmEnabled bool, serverPort int) {
	var enabled C.int
	if confirmEnabled {
		enabled = 1
	}
	C.setupQuitInterceptor(enabled, C.int(serverPort))
}

// setQuitConfirmEnabled updates the quit confirmation setting at runtime.
// This is exposed to JavaScript via webview.Bind.
func setQuitConfirmEnabled(enabled bool) {
	var cEnabled C.int
	if enabled {
		cEnabled = 1
	}
	C.setQuitConfirmEnabled(cEnabled)
}

// setupSwipeGesture sets up the two-finger swipe gesture recognizer for
// navigating between conversations. Swipe left goes to next, swipe right
// goes to previous conversation.
func setupSwipeGesture() {
	C.setupSwipeGestureRecognizer()
}

// openExternalURL opens a URL in the default browser.
// This is exposed to JavaScript via webview.Bind.
func openExternalURL(url string) {
	cURL := C.CString(url)
	defer C.free(unsafe.Pointer(cURL))
	C.openURLInBrowser(cURL)
}

// openFileURL opens a file:// URL with the default application.
// This uses NSWorkspace.openURL which handles file:// URLs correctly,
// opening the file in the appropriate application based on its type.
// This is exposed to JavaScript via webview.Bind.
func openFileURL(url string) {
	cURL := C.CString(url)
	defer C.free(unsafe.Pointer(cURL))
	C.openURLInBrowser(cURL) // NSWorkspace.openURL handles file:// URLs
}

// pickFolder opens a native folder picker dialog and returns the selected path.
// Returns an empty string if the user cancels.
// This is exposed to JavaScript via webview.Bind.
func pickFolder() string {
	cPath := C.openFolderPicker()
	if cPath == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(cPath))
	return C.GoString(cPath)
}

// pickImages opens a native file picker dialog for selecting images.
// Returns a JSON array of file paths, or an empty array if cancelled.
// This is exposed to JavaScript via webview.Bind.
func pickImages() []string {
	cJSON := C.openImagePicker()
	if cJSON == nil {
		return []string{}
	}
	defer C.free(unsafe.Pointer(cJSON))

	jsonStr := C.GoString(cJSON)
	var paths []string
	if err := json.Unmarshal([]byte(jsonStr), &paths); err != nil {
		return []string{}
	}
	return paths
}

// macOS virtual key codes for common keys
var macKeyCodeMap = map[string]C.UInt32{
	"a": 0, "b": 11, "c": 8, "d": 2, "e": 14, "f": 3, "g": 5, "h": 4,
	"i": 34, "j": 38, "k": 40, "l": 37, "m": 46, "n": 45, "o": 31, "p": 35,
	"q": 12, "r": 15, "s": 1, "t": 17, "u": 32, "v": 9, "w": 13, "x": 7,
	"y": 16, "z": 6,
	"0": 29, "1": 18, "2": 19, "3": 20, "4": 21, "5": 23, "6": 22, "7": 26,
	"8": 28, "9": 25,
	"space": 49, "tab": 48, "return": 36, "enter": 36, "escape": 53, "esc": 53,
	"delete": 51, "backspace": 51,
	"f1": 122, "f2": 120, "f3": 99, "f4": 118, "f5": 96, "f6": 97,
	"f7": 98, "f8": 100, "f9": 101, "f10": 109, "f11": 103, "f12": 111,
	"left": 123, "right": 124, "up": 126, "down": 125,
}

// macOS modifier key constants (from Carbon/HIToolbox/Events.h)
const (
	cmdKeyMod     C.UInt32 = 0x0100 // cmdKey
	shiftKeyMod   C.UInt32 = 0x0200 // shiftKey
	optionKeyMod  C.UInt32 = 0x0800 // optionKey
	controlKeyMod C.UInt32 = 0x1000 // controlKey
)

// parseHotkey parses a hotkey string like "cmd+shift+m" into key code and modifiers.
// Returns keyCode, modifiers, and an error if parsing fails.
func parseHotkey(hotkey string) (keyCode C.UInt32, modifiers C.UInt32, err error) {
	parts := strings.Split(strings.ToLower(hotkey), "+")
	if len(parts) == 0 {
		return 0, 0, fmt.Errorf("empty hotkey string")
	}

	// The last part is the key, everything before is modifiers
	key := parts[len(parts)-1]
	mods := parts[:len(parts)-1]

	// Parse the key
	kc, ok := macKeyCodeMap[key]
	if !ok {
		return 0, 0, fmt.Errorf("unknown key: %q", key)
	}
	keyCode = kc

	// Parse modifiers
	for _, mod := range mods {
		switch mod {
		case "cmd", "command", "meta", "super":
			modifiers |= cmdKeyMod
		case "shift":
			modifiers |= shiftKeyMod
		case "alt", "option", "opt":
			modifiers |= optionKeyMod
		case "ctrl", "control":
			modifiers |= controlKeyMod
		default:
			return 0, 0, fmt.Errorf("unknown modifier: %q", mod)
		}
	}

	return keyCode, modifiers, nil
}

// registerHotkey registers a global hotkey to toggle app visibility.
// The hotkey parameter should be in format "modifier+modifier+key" (e.g., "cmd+shift+m").
// Returns nil on success, error on failure.
func registerHotkey(hotkey string) error {
	keyCode, modifiers, err := parseHotkey(hotkey)
	if err != nil {
		return fmt.Errorf("invalid hotkey %q: %w", hotkey, err)
	}

	result := C.registerGlobalHotkeyWithParams(keyCode, modifiers)
	if result != 0 {
		return fmt.Errorf("failed to register global hotkey (error code: %d)", result)
	}
	return nil
}

// unregisterHotkey unregisters the global hotkey.
func unregisterHotkey() {
	C.unregisterGlobalHotkey()
}

// isLoginItemSupported checks if the login item API is available.
// Always returns true since LaunchAgents work on all macOS versions.
// This is exposed to JavaScript via webview.Bind.
func isLoginItemSupported() bool {
	return bool(C.isLoginItemSupported())
}

// isLoginItemEnabled checks if Mitto is configured to start at login.
// This is exposed to JavaScript via webview.Bind.
func isLoginItemEnabled() bool {
	return bool(C.isLoginItemEnabled())
}

// setLoginItemEnabled enables or disables starting at login.
// This is exposed to JavaScript via webview.Bind.
func setLoginItemEnabled(enabled bool) error {
	var result C.int
	if enabled {
		result = C.enableLoginItem()
	} else {
		result = C.disableLoginItem()
	}
	if result != 0 {
		return fmt.Errorf("failed to update login item (error code: %d)", result)
	}
	return nil
}

const (
	appName         = "Mitto"
	windowWidth     = 1200
	windowHeight    = 800
	windowMinWidth  = 480
	windowMinHeight = 400
	defaultServer   = "claude" // Default ACP server if none configured
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Initialize logging (minimal for desktop app)
	// Default to INFO level, but allow override via MITTO_LOG_LEVEL environment variable
	logLevel := "info"
	if envLevel := os.Getenv("MITTO_LOG_LEVEL"); envLevel != "" {
		logLevel = envLevel
	}
	if err := logging.Initialize(logging.Config{
		Level: logLevel,
	}); err != nil {
		return fmt.Errorf("failed to initialize logging: %w", err)
	}
	defer logging.Close()

	// Ensure Mitto directory exists
	if err := appdir.EnsureDir(); err != nil {
		return fmt.Errorf("failed to create Mitto directory: %w", err)
	}

	// Deploy builtin prompts on first run
	builtinPromptsDir, err := appdir.BuiltinPromptsDir()
	if err != nil {
		slog.Warn("Failed to get builtin prompts directory", "error", err)
	} else {
		deployed, err := embeddedconfig.EnsureBuiltinPrompts(builtinPromptsDir)
		if err != nil {
			slog.Warn("Failed to deploy builtin prompts", "error", err)
		} else if deployed {
			slog.Info("Deployed builtin prompts", "dir", builtinPromptsDir)
		}
	}

	// Load configuration using the hierarchy:
	// 1. RC file (~/.mittorc) if it exists - settings become read-only
	// 2. settings.json if no RC file (auto-creates from defaults if missing)
	configResult, err := config.LoadSettingsWithFallback()
	var cfg *config.Config
	if err != nil {
		// Config loading failure is not fatal - we can use defaults
		slog.Warn("Failed to load settings, using defaults", "error", err)
		cfg = nil
		configResult = nil
	} else {
		cfg = configResult.Config
		if configResult.Source == config.ConfigSourceRCFile {
			slog.Info("Configuration loaded from RC file", "path", configResult.SourcePath)
		}
	}

	// Get ACP server configuration
	server, err := getServer(cfg)
	if err != nil {
		return fmt.Errorf("failed to get ACP server: %w", err)
	}

	// Load workspaces from workspaces.json (macOS app always uses file-based persistence)
	var workspaces []config.WorkspaceSettings
	savedWorkspaces, err := config.LoadWorkspaces()
	if err != nil {
		slog.Warn("Failed to load workspaces", "error", err)
	}
	if savedWorkspaces != nil {
		// Use saved workspaces directly (same type)
		workspaces = savedWorkspaces
	}

	// Check for MITTO_WORK_DIR environment variable (for testing/development)
	// This adds to the loaded workspaces, doesn't replace them
	if workDir := os.Getenv("MITTO_WORK_DIR"); workDir != "" {
		absPath, err := filepath.Abs(workDir)
		if err == nil {
			// Check if this workspace already exists
			exists := false
			for _, ws := range workspaces {
				if ws.WorkingDir == absPath {
					exists = true
					break
				}
			}
			if !exists {
				workspaces = append(workspaces, config.WorkspaceSettings{
					ACPServer:  server.Name,
					ACPCommand: server.Command,
					WorkingDir: absPath,
				})
			}
		}
	}

	// Initialize auxiliary session manager
	auxiliary.Initialize(server.Command, nil)
	defer auxiliary.Shutdown()

	// Sync login item state with config
	// This ensures the LaunchAgent registration matches the config setting
	if cfg != nil && cfg.UI.Mac != nil && isLoginItemSupported() {
		currentState := isLoginItemEnabled()
		desiredState := cfg.UI.Mac.StartAtLogin
		if currentState != desiredState {
			if err := setLoginItemEnabled(desiredState); err != nil {
				slog.Warn("Failed to sync login item state", "desired", desiredState, "error", err)
			} else {
				slog.Info("Login item state synced", "enabled", desiredState)
			}
		}
	}

	// Create workspace save callback for persistence
	onWorkspaceSave := func(ws []config.WorkspaceSettings) error {
		return config.SaveWorkspaces(ws)
	}

	// Create web server
	// For macOS app, workspaces are persisted to workspaces.json
	// If no workspaces exist, the frontend will show the Settings dialog
	// If config came from RC file, settings are read-only
	configReadOnly := configResult != nil && configResult.Source == config.ConfigSourceRCFile
	var rcFilePath string
	if configReadOnly {
		rcFilePath = configResult.SourcePath
	}

	// Initialize prompts cache for global prompts from MITTO_DIR/prompts/
	// and any additional directories from config
	promptsCache := config.NewPromptsCache()
	if len(cfg.PromptsDirs) > 0 {
		promptsCache.SetAdditionalDirs(cfg.PromptsDirs)
	}

	// Configure access logging (enabled by default for macOS app)
	// Writes to $MITTO_DIR/access.log with size-based rotation
	accessLogConfig := web.DefaultAccessLogConfig()
	mittoDir, err := appdir.Dir()
	if err != nil {
		slog.Warn("Failed to get Mitto directory for access log", "error", err)
	} else {
		accessLogConfig.Path = filepath.Join(mittoDir, "access.log")
		slog.Info("Access logging enabled", "path", accessLogConfig.Path)
	}

	webConfig := web.Config{
		Workspaces:      workspaces,
		AutoApprove:     false,
		Debug:           false,
		MittoConfig:     cfg,
		FromCLI:         false, // macOS app always uses file-based persistence
		OnWorkspaceSave: onWorkspaceSave,
		ConfigReadOnly:  configReadOnly,
		RCFilePath:      rcFilePath,
		PromptsCache:    promptsCache,
		AccessLog:       accessLogConfig,
	}

	// Set legacy fields as fallback (for auxiliary sessions, etc.)
	webConfig.ACPCommand = server.Command
	webConfig.ACPServer = server.Name

	srv, err := web.NewServer(webConfig)
	if err != nil {
		return fmt.Errorf("failed to create web server: %w", err)
	}

	// Set external port from config (used when external access is enabled via UI)
	// Note: -1 = disabled, 0 = random port, >0 = specific port. Always set from config.
	if cfg != nil {
		srv.SetExternalPort(cfg.Web.ExternalPort)
	}

	// Start server on random port (macOS app always uses random for local access)
	// SECURITY: Use CreateLocalhostListener which:
	// 1. Binds exclusively to 127.0.0.1 (never 0.0.0.0)
	// 2. Validates each connection originates from localhost at the socket level
	// 3. Uses port 0 for random port allocation (ignores any configured port)
	listener, port, err := web.CreateLocalhostListener(0)
	if err != nil {
		return fmt.Errorf("failed to create localhost listener: %w", err)
	}
	url := fmt.Sprintf("http://127.0.0.1:%d", port)

	// Start web server in background
	serverErr := make(chan error, 1)
	go func() {
		if err := srv.Serve(listener); err != nil && !srv.IsShutdown() {
			serverErr <- err
		}
		close(serverErr)
	}()

	// Start external listener if auth is configured and external port is not disabled
	// External port: -1 = disabled, 0 = random, >0 = specific port
	if cfg != nil && cfg.Web.Auth != nil && cfg.Web.ExternalPort >= 0 {
		externalPort, err := srv.StartExternalListener(cfg.Web.ExternalPort)
		if err != nil {
			slog.Error("Failed to start external listener", "error", err)
		} else {
			slog.Info("External listener started", "port", externalPort)
		}
	}

	// Run the up hook if configured
	var upHook *hooks.Process
	if cfg != nil {
		upHook = hooks.StartUp(cfg.Web.Hooks.Up, port)
	}

	// Create and run WebView
	w := webview.New(false)
	if w == nil {
		srv.Shutdown()
		return fmt.Errorf("failed to create webview")
	}
	defer w.Destroy()

	// Store global reference for menu action callbacks
	globalWebViewMu.Lock()
	globalWebView = w
	globalWebViewMu.Unlock()
	defer func() {
		globalWebViewMu.Lock()
		globalWebView = nil
		globalWebViewMu.Unlock()
	}()

	// Set up native macOS menu bar (must be done before Run)
	setupMenu(appName)

	// Set up quit confirmation interceptor
	confirmQuit := true
	if cfg != nil {
		confirmQuit = cfg.ShouldConfirmQuitWithRunningSessions()
	}
	setupQuitInterceptor(confirmQuit, port)

	// Bind Go functions to JavaScript
	// This allows the frontend to call native functions
	w.Bind("mittoOpenExternalURL", openExternalURL)
	w.Bind("mittoOpenFileURL", openFileURL)
	w.Bind("mittoPickFolder", pickFolder)
	w.Bind("mittoPickImages", pickImages)

	// Bind login item functions (start at login)
	w.Bind("mittoIsLoginItemSupported", isLoginItemSupported)
	w.Bind("mittoIsLoginItemEnabled", isLoginItemEnabled)
	w.Bind("mittoSetLoginItemEnabled", setLoginItemEnabled)

	// Bind notification functions (native macOS notifications)
	w.Bind("mittoRequestNotificationPermission", requestNotificationPermission)
	w.Bind("mittoGetNotificationPermissionStatus", getNotificationPermissionStatus)
	w.Bind("mittoShowNativeNotification", showNativeNotification)
	w.Bind("mittoRemoveNotificationsForSession", removeNotificationsForSession)

	// Bind quit confirmation setting function
	w.Bind("mittoSetQuitConfirmEnabled", setQuitConfirmEnabled)

	// Initialize notification center (must be done after app is running)
	initNotifications()

	// Register global hotkey to toggle app visibility
	hotkeyStr, hotkeyEnabled := getHotkeyConfig(cfg)
	if hotkeyEnabled {
		if err := registerHotkey(hotkeyStr); err != nil {
			// Log warning but don't fail - hotkey is a nice-to-have feature
			slog.Warn("Failed to register global hotkey", "hotkey", hotkeyStr, "error", err)
		} else {
			slog.Info("Global hotkey registered", "hotkey", hotkeyStr)
		}
		defer unregisterHotkey()
	} else {
		slog.Info("Global hotkey disabled in configuration")
	}

	w.SetTitle(appName)
	w.SetSize(windowWidth, windowHeight, webview.HintNone)
	w.SetSize(windowMinWidth, windowMinHeight, webview.HintMin)
	w.Navigate(url)

	// Configure window to show in all Spaces if enabled
	// This is dispatched to run after the window is fully created
	showInAllSpaces := false
	if cfg != nil && cfg.UI.Mac != nil {
		showInAllSpaces = cfg.UI.Mac.ShowInAllSpaces
	}
	if showInAllSpaces {
		w.Dispatch(func() {
			C.setWindowShowInAllSpaces(C.int(1))
			slog.Info("Window configured to show in all Spaces")
		})
	}

	// Request notification permission on startup if native notifications are enabled
	// This ensures permission is requested when the setting is saved and app is restarted
	nativeNotificationsEnabled := false
	if cfg != nil && cfg.UI.Mac != nil && cfg.UI.Mac.Notifications != nil {
		nativeNotificationsEnabled = cfg.UI.Mac.Notifications.NativeEnabled
	}
	if nativeNotificationsEnabled {
		w.Dispatch(func() {
			status := getNotificationPermissionStatus()
			slog.Info("Native notifications enabled, checking permission", "status", status)
			if status == 0 { // Not determined - request permission
				slog.Info("Requesting notification permission on startup")
				requestNotificationPermission()
			} else if status == 1 { // Denied
				slog.Info("Notification permission denied - notifications will not be shown")
			} else if status == 2 { // Authorized
				slog.Info("Notification permission already granted")
			}
		})
	}

	// Disable fullscreen mode for the window
	// This removes the fullscreen button from the title bar
	w.Dispatch(func() {
		C.disableWindowFullscreen()
	})

	// Activate the app to bring it to the foreground on launch
	// This is dispatched to run after the window is fully created
	w.Dispatch(func() {
		C.activateApp()
	})

	// Set up two-finger swipe gesture for navigating between conversations
	// This is dispatched to run after the window is fully created
	w.Dispatch(func() {
		setupSwipeGesture()
		slog.Info("Two-finger swipe gesture enabled for conversation navigation")
	})

	// Set up shutdown manager for graceful shutdown
	shutdown := hooks.NewShutdownManager()

	// Store global reference for quit callback
	globalShutdownMu.Lock()
	globalShutdown = shutdown
	globalShutdownMu.Unlock()
	defer func() {
		globalShutdownMu.Lock()
		globalShutdown = nil
		globalShutdownMu.Unlock()
	}()

	// Configure hooks
	var downHook config.WebHook
	if cfg != nil {
		downHook = cfg.Web.Hooks.Down
	}
	shutdown.SetHooks(upHook, downHook, port)

	// Add cleanup functions
	shutdown.AddCleanup(func(reason string) {
		auxiliary.Shutdown()
	})
	shutdown.AddCleanup(func(reason string) {
		srv.Shutdown()
	})

	// Set the UI termination callback - this will be called after all cleanup
	// to terminate the WebView event loop
	shutdown.SetTerminateUI(func() {
		w.Terminate()
	})

	// Start listening for signals
	shutdown.Start()

	// Run the WebView event loop (blocks until window is closed)
	w.Run()

	// Cleanup (runs once, whether from signal, app quit callback, or normal exit)
	// Note: This may have already been triggered by goQuitCallback if user quit via menu
	if hotkeyEnabled {
		unregisterHotkey()
	}
	shutdown.Shutdown("window_closed")

	// Check for server errors
	if err := <-serverErr; err != nil {
		return fmt.Errorf("web server error: %w", err)
	}

	return nil
}

// getServer returns the ACP server to use based on config or environment.
func getServer(cfg *config.Config) (*config.ACPServer, error) {
	// Check environment variable first
	if serverName := os.Getenv("MITTO_ACP_SERVER"); serverName != "" && cfg != nil {
		return cfg.GetServer(serverName)
	}

	// Use config default if available
	if cfg != nil {
		if server := cfg.DefaultServer(); server != nil {
			return server, nil
		}
	}

	// Fall back to default
	return &config.ACPServer{
		Name:    defaultServer,
		Command: defaultServer,
	}, nil
}

// getHotkeyConfig returns the hotkey configuration from config or defaults.
// Returns the hotkey string and whether it's enabled.
func getHotkeyConfig(cfg *config.Config) (hotkey string, enabled bool) {
	// Check environment variable override first
	if envHotkey := os.Getenv("MITTO_HOTKEY"); envHotkey != "" {
		if strings.ToLower(envHotkey) == "disabled" || envHotkey == "false" || envHotkey == "0" {
			return "", false
		}
		return envHotkey, true
	}

	// Use config if available
	if cfg != nil {
		return cfg.GetShowHideHotkey()
	}

	// Default
	return config.DefaultShowHideHotkey, true
}
