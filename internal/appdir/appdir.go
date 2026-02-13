// Package appdir provides platform-native directory management for Mitto.
// It handles locating and creating the Mitto data directory, which stores
// configuration (settings.json) and session data (sessions/ subdirectory).
package appdir

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

const (
	// MittoDirEnv is the environment variable to override the Mitto directory.
	MittoDirEnv = "MITTO_DIR"

	// MittoRCEnv is the environment variable to override the RC file location.
	MittoRCEnv = "MITTORC"

	// RCFileName is the name of the RC file (without leading dot for some paths).
	RCFileName = "mittorc"

	// SettingsFileName is the name of the settings file.
	SettingsFileName = "settings.json"

	// WorkspacesFileName is the name of the workspaces file.
	WorkspacesFileName = "workspaces.json"

	// SessionsDirName is the name of the sessions subdirectory.
	SessionsDirName = "sessions"

	// HooksDirName is the name of the hooks subdirectory.
	HooksDirName = "hooks"

	// PromptsDirName is the name of the prompts subdirectory.
	PromptsDirName = "prompts"

	// BuiltinPromptsDirName is the name of the builtin prompts subdirectory.
	BuiltinPromptsDirName = "builtin"

	// WorkspaceConfigDirName is the name of the workspace-specific config directory.
	// This directory is located at the root of a workspace (e.g., $WORKSPACE/.mitto/).
	WorkspaceConfigDirName = ".mitto"

	// AuthSessionsFileName is the name of the auth sessions file.
	AuthSessionsFileName = "auth_sessions.json"

	// UIPreferencesFileName is the name of the UI preferences file.
	// This stores client-side UI state like grouping mode and expanded groups.
	UIPreferencesFileName = "ui_preferences.json"
)

var (
	// cachedDir stores the resolved Mitto directory to avoid repeated lookups.
	cachedDir string
	// mu protects cachedDir.
	mu sync.RWMutex
)

// Dir returns the Mitto data directory path.
// The directory is determined in the following order:
//  1. MITTO_DIR environment variable (if set)
//  2. Platform-specific default:
//     - macOS: ~/Library/Application Support/Mitto
//     - Linux: $XDG_DATA_HOME/mitto or ~/.local/share/mitto
//     - Windows: %APPDATA%\Mitto
//
// This function only returns the path; it does not create the directory.
// Use EnsureDir() to create the directory if needed.
func Dir() (string, error) {
	mu.RLock()
	if cachedDir != "" {
		dir := cachedDir
		mu.RUnlock()
		return dir, nil
	}
	mu.RUnlock()

	mu.Lock()
	defer mu.Unlock()

	// Double-check after acquiring write lock
	if cachedDir != "" {
		return cachedDir, nil
	}

	dir, err := resolveDir()
	if err != nil {
		return "", err
	}

	cachedDir = dir
	return dir, nil
}

// resolveDir calculates the Mitto directory path.
func resolveDir() (string, error) {
	// Check environment variable first
	if envDir := os.Getenv(MittoDirEnv); envDir != "" {
		return envDir, nil
	}

	// Use platform-specific directory
	switch runtime.GOOS {
	case "darwin":
		// macOS: ~/Library/Application Support/Mitto
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get home directory: %w", err)
		}
		return filepath.Join(homeDir, "Library", "Application Support", "Mitto"), nil

	case "windows":
		// Windows: %APPDATA%\Mitto
		appData := os.Getenv("APPDATA")
		if appData == "" {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("failed to get home directory: %w", err)
			}
			appData = filepath.Join(homeDir, "AppData", "Roaming")
		}
		return filepath.Join(appData, "Mitto"), nil

	default:
		// Linux and other Unix-like systems: $XDG_DATA_HOME/mitto or ~/.local/share/mitto
		dataDir := os.Getenv("XDG_DATA_HOME")
		if dataDir == "" {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("failed to get home directory: %w", err)
			}
			dataDir = filepath.Join(homeDir, ".local", "share")
		}
		return filepath.Join(dataDir, "mitto"), nil
	}
}

// EnsureDir creates the Mitto data directory if it doesn't exist.
// It also creates the sessions and hooks subdirectories.
func EnsureDir() error {
	dir, err := Dir()
	if err != nil {
		return err
	}

	// Create main directory
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create Mitto directory %s: %w", dir, err)
	}

	// Create sessions subdirectory
	sessionsDir := filepath.Join(dir, SessionsDirName)
	if err := os.MkdirAll(sessionsDir, 0755); err != nil {
		return fmt.Errorf("failed to create sessions directory %s: %w", sessionsDir, err)
	}

	// Create hooks subdirectory
	hooksDir := filepath.Join(dir, HooksDirName)
	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		return fmt.Errorf("failed to create hooks directory %s: %w", hooksDir, err)
	}

	// Create prompts subdirectory
	promptsDir := filepath.Join(dir, PromptsDirName)
	if err := os.MkdirAll(promptsDir, 0755); err != nil {
		return fmt.Errorf("failed to create prompts directory %s: %w", promptsDir, err)
	}

	return nil
}

// LogsDir returns the platform-specific logs directory path for Mitto.
// The directory is determined in the following order:
//  1. Platform-specific default:
//     - macOS: ~/Library/Logs/Mitto
//     - Linux: $XDG_STATE_HOME/mitto or ~/.local/state/mitto
//     - Windows: %LOCALAPPDATA%\Mitto\Logs
//
// This function only returns the path; it does not create the directory.
// Use EnsureLogsDir() to create the directory if needed.
func LogsDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	switch runtime.GOOS {
	case "darwin":
		// macOS: ~/Library/Logs/Mitto (standard macOS logs location)
		return filepath.Join(homeDir, "Library", "Logs", "Mitto"), nil

	case "windows":
		// Windows: %LOCALAPPDATA%\Mitto\Logs
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData == "" {
			localAppData = filepath.Join(homeDir, "AppData", "Local")
		}
		return filepath.Join(localAppData, "Mitto", "Logs"), nil

	default:
		// Linux and other Unix-like systems: $XDG_STATE_HOME/mitto or ~/.local/state/mitto
		// XDG_STATE_HOME is the standard location for state files including logs
		stateDir := os.Getenv("XDG_STATE_HOME")
		if stateDir == "" {
			stateDir = filepath.Join(homeDir, ".local", "state")
		}
		return filepath.Join(stateDir, "mitto"), nil
	}
}

// EnsureLogsDir creates the Mitto logs directory if it doesn't exist.
func EnsureLogsDir() error {
	dir, err := LogsDir()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create logs directory %s: %w", dir, err)
	}

	return nil
}

// SettingsPath returns the full path to the settings.json file.
func SettingsPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, SettingsFileName), nil
}

// WorkspacesPath returns the full path to the workspaces.json file.
func WorkspacesPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, WorkspacesFileName), nil
}

// SessionsDir returns the full path to the sessions directory.
func SessionsDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, SessionsDirName), nil
}

// HooksDir returns the full path to the hooks directory.
func HooksDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, HooksDirName), nil
}

// PromptsDir returns the full path to the prompts directory.
// This directory contains global prompt files in markdown format with YAML front-matter.
func PromptsDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, PromptsDirName), nil
}

// BuiltinPromptsDir returns the full path to the builtin prompts directory.
// This directory contains prompts that are deployed from the embedded filesystem.
func BuiltinPromptsDir() (string, error) {
	promptsDir, err := PromptsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(promptsDir, BuiltinPromptsDirName), nil
}

// WorkspacePromptsDir returns the full path to the default workspace prompts directory.
// This is $WORKSPACE/.mitto/prompts/ and is automatically searched for prompts
// when a workspace is active, without requiring explicit configuration in .mittorc.
func WorkspacePromptsDir(workspaceRoot string) string {
	return filepath.Join(workspaceRoot, WorkspaceConfigDirName, PromptsDirName)
}

// AuthSessionsPath returns the full path to the auth_sessions.json file.
// This file stores authenticated user sessions so they persist across server restarts.
func AuthSessionsPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, AuthSessionsFileName), nil
}

// UIPreferencesPath returns the full path to the ui_preferences.json file.
// This file stores client-side UI state like grouping mode and expanded groups.
// It's used by the macOS app where localStorage doesn't persist across launches
// due to random port allocation (each port is a different origin).
func UIPreferencesPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, UIPreferencesFileName), nil
}

// ResetCache clears the cached directory path.
// This is primarily useful for testing.
func ResetCache() {
	mu.Lock()
	defer mu.Unlock()
	cachedDir = ""
}

// RCFilePath returns the path to the user's RC file if it exists.
// It checks the following locations in order:
//  1. MITTORC environment variable (if set and file exists)
//  2. Platform-specific locations:
//     - macOS: ~/.mittorc
//     - Linux: ~/.mittorc, then $XDG_CONFIG_HOME/mitto/mittorc
//     - Windows: %USERPROFILE%\.mittorc
//
// Returns the path to the RC file if found, or an empty string if not found.
// Returns an error only if there's a problem getting the home directory.
func RCFilePath() (string, error) {
	// Check environment variable first
	if envPath := os.Getenv(MittoRCEnv); envPath != "" {
		if _, err := os.Stat(envPath); err == nil {
			return envPath, nil
		}
		// Env var set but file doesn't exist - continue checking other locations
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	// Platform-specific RC file locations
	switch runtime.GOOS {
	case "darwin":
		// macOS: ~/.mittorc
		rcPath := filepath.Join(homeDir, ".mittorc")
		if _, err := os.Stat(rcPath); err == nil {
			return rcPath, nil
		}

	case "windows":
		// Windows: %USERPROFILE%\.mittorc
		rcPath := filepath.Join(homeDir, ".mittorc")
		if _, err := os.Stat(rcPath); err == nil {
			return rcPath, nil
		}

	default:
		// Linux and other Unix-like systems
		// First check ~/.mittorc
		rcPath := filepath.Join(homeDir, ".mittorc")
		if _, err := os.Stat(rcPath); err == nil {
			return rcPath, nil
		}

		// Then check $XDG_CONFIG_HOME/mitto/mittorc
		xdgConfig := os.Getenv("XDG_CONFIG_HOME")
		if xdgConfig == "" {
			xdgConfig = filepath.Join(homeDir, ".config")
		}
		xdgRCPath := filepath.Join(xdgConfig, "mitto", RCFileName)
		if _, err := os.Stat(xdgRCPath); err == nil {
			return xdgRCPath, nil
		}
	}

	return "", nil
}
