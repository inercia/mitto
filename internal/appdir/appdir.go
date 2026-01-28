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

	// SettingsFileName is the name of the settings file.
	SettingsFileName = "settings.json"

	// WorkspacesFileName is the name of the workspaces file.
	WorkspacesFileName = "workspaces.json"

	// SessionsDirName is the name of the sessions subdirectory.
	SessionsDirName = "sessions"
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
// It also creates the sessions subdirectory.
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

// ResetCache clears the cached directory path.
// This is primarily useful for testing.
func ResetCache() {
	mu.Lock()
	defer mu.Unlock()
	cachedDir = ""
}
