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
#cgo darwin LDFLAGS: -framework Cocoa -framework Carbon

#import <Cocoa/Cocoa.h>
#import <Carbon/Carbon.h>
#include "menu_darwin.h"

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
*/
import "C"

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	webview "github.com/webview/webview_go"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/auxiliary"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/logging"
	"github.com/inercia/mitto/internal/web"
)

// Global webview reference for menu action callbacks
var (
	globalWebView   webview.WebView
	globalWebViewMu sync.Mutex
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

// openExternalURL opens a URL in the default browser.
// This is exposed to JavaScript via webview.Bind.
func openExternalURL(url string) {
	cURL := C.CString(url)
	defer C.free(unsafe.Pointer(cURL))
	C.openURLInBrowser(cURL)
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

const (
	appName       = "Mitto"
	windowWidth   = 1200
	windowHeight  = 800
	defaultServer = "claude" // Default ACP server if none configured
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Initialize logging (minimal for desktop app)
	if err := logging.Initialize(logging.Config{
		Level: "info",
	}); err != nil {
		return fmt.Errorf("failed to initialize logging: %w", err)
	}
	defer logging.Close()

	// Ensure Mitto directory exists
	if err := appdir.EnsureDir(); err != nil {
		return fmt.Errorf("failed to create Mitto directory: %w", err)
	}

	// Load configuration from settings.json (auto-creates from defaults if missing)
	cfg, err := config.LoadSettings()
	if err != nil {
		// Config loading failure is not fatal - we can use defaults
		slog.Warn("Failed to load settings, using defaults", "error", err)
		cfg = nil
	}

	// Get ACP server configuration
	server, err := getServer(cfg)
	if err != nil {
		return fmt.Errorf("failed to get ACP server: %w", err)
	}

	// Load workspaces from workspaces.json (macOS app always uses file-based persistence)
	var workspaces []web.WorkspaceConfig
	savedWorkspaces, err := config.LoadWorkspaces()
	if err != nil {
		slog.Warn("Failed to load workspaces", "error", err)
	}
	if savedWorkspaces != nil {
		workspaces = make([]web.WorkspaceConfig, len(savedWorkspaces))
		for i, ws := range savedWorkspaces {
			workspaces[i] = web.WorkspaceConfig{
				ACPServer:  ws.ACPServer,
				ACPCommand: ws.ACPCommand,
				WorkingDir: ws.WorkingDir,
			}
		}
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
				workspaces = append(workspaces, web.WorkspaceConfig{
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

	// Create workspace save callback for persistence
	onWorkspaceSave := func(ws []web.WorkspaceConfig) error {
		settings := make([]config.WorkspaceSettings, len(ws))
		for i, w := range ws {
			settings[i] = config.WorkspaceSettings{
				ACPServer:  w.ACPServer,
				ACPCommand: w.ACPCommand,
				WorkingDir: w.WorkingDir,
				Color:      w.Color,
			}
		}
		return config.SaveWorkspaces(settings)
	}

	// Create web server
	// For macOS app, workspaces are persisted to workspaces.json
	// If no workspaces exist, the frontend will show the Settings dialog
	webConfig := web.Config{
		Workspaces:      workspaces,
		AutoApprove:     false,
		Debug:           false,
		MittoConfig:     cfg,
		FromCLI:         false, // macOS app always uses file-based persistence
		OnWorkspaceSave: onWorkspaceSave,
	}

	// Set legacy fields as fallback (for auxiliary sessions, etc.)
	webConfig.ACPCommand = server.Command
	webConfig.ACPServer = server.Name

	srv, err := web.NewServer(webConfig)
	if err != nil {
		return fmt.Errorf("failed to create web server: %w", err)
	}

	// Set external port from config (used when external access is enabled via UI)
	if cfg != nil && cfg.Web.ExternalPort != 0 {
		srv.SetExternalPort(cfg.Web.ExternalPort)
	}

	// Start server on random port (macOS app always uses random for local access)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("failed to create listener: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	url := fmt.Sprintf("http://127.0.0.1:%d", port)

	// Start web server in background
	serverErr := make(chan error, 1)
	go func() {
		if err := srv.Serve(listener); err != nil && !srv.IsShutdown() {
			serverErr <- err
		}
		close(serverErr)
	}()

	// Handle signals for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		auxiliary.Shutdown()
		srv.Shutdown()
	}()

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
	w.Bind("mittoPickFolder", pickFolder)

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
	w.Navigate(url)

	// Run the WebView event loop (blocks until window is closed)
	w.Run()

	// Cleanup
	if hotkeyEnabled {
		unregisterHotkey()
	}
	auxiliary.Shutdown()
	srv.Shutdown()

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
