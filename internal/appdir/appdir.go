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

	// AuthSessionsFileName is the name of the auth sessions file.
	AuthSessionsFileName = "auth_sessions.json"
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

// AuthSessionsPath returns the full path to the auth_sessions.json file.
// This file stores authenticated user sessions so they persist across server restarts.
func AuthSessionsPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, AuthSessionsFileName), nil
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

// RCFileExists returns true if an RC file exists at any of the standard locations.
func RCFileExists() (bool, error) {
	path, err := RCFilePath()
	if err != nil {
		return false, err
	}
	return path != "", nil
}

// DefaultRCFilePath returns the default RC file path for the current platform.
// This is the path where the user should create their RC file.
// Unlike RCFilePath(), this does not check if the file exists.
func DefaultRCFilePath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	switch runtime.GOOS {
	case "darwin", "windows":
		return filepath.Join(homeDir, ".mittorc"), nil
	default:
		// Linux: prefer ~/.mittorc for simplicity
		return filepath.Join(homeDir, ".mittorc"), nil
	}
}
