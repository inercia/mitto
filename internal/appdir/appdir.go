// Package appdir provides platform-native directory management for Mitto.
// It handles locating and creating the Mitto data directory, which stores
// configuration (settings.json) and session data (sessions/ subdirectory).
package appdir

import (
	"fmt"
	"log/slog"
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

	// FoldersFileName is the name of the folders file.
	FoldersFileName = "folders.json"

	// InstanceFileName is the name of the running-instance discovery file
	// (mitto-pscc.2). It records how to reach the currently running `mitto
	// web` / macOS app server (URL, API prefix, bearer token) so local
	// clients such as the CLI can find it without extra configuration.
	InstanceFileName = "instance.json"

	// SessionsDirName is the name of the sessions subdirectory.
	SessionsDirName = "sessions"

	// ProcessorsDirName is the name of the processors subdirectory.
	ProcessorsDirName = "processors"

	// PromptsDirName is the name of the prompts subdirectory.
	PromptsDirName = "prompts"

	// BuiltinPromptsDirName is the name of the builtin prompts subdirectory.
	BuiltinPromptsDirName = "builtin"

	// BuiltinProcessorsDirName is the name of the builtin processors subdirectory.
	BuiltinProcessorsDirName = "builtin"

	// AgentsDirName is the name of the agents subdirectory.
	AgentsDirName = "agents"

	// BuiltinAgentsDirName is the name of the builtin agents subdirectory.
	BuiltinAgentsDirName = "builtin"

	// WorkspaceConfigDirName is the name of the workspace-specific config directory.
	// This directory is located at the root of a workspace (e.g., $MITTO_WORKING_DIR/.mitto/).
	WorkspaceConfigDirName = ".mitto"

	// AuthSessionsFileName is the name of the auth sessions file.
	AuthSessionsFileName = "auth_sessions.json"

	// UIPreferencesFileName is the name of the UI preferences file.
	// This stores client-side UI state like grouping mode and expanded groups.
	UIPreferencesFileName = "ui_preferences.json"

	// DefenseBlocklistFileName is the name of the scanner defense blocklist file.
	DefenseBlocklistFileName = "scanner_blocklist.json"

	// MCPToolsCacheDirName is the name of the subdirectory holding per-workspace
	// persisted real-MCP tools snapshots (one JSON file per workspace UUID).
	MCPToolsCacheDirName = "mcp-tools-cache"

	// StatsDirName is the name of the subdirectory holding the dashboard
	// time-series stats SQLite database (created by the first writer under
	// internal/stats).
	StatsDirName = "stats"

	// RememberedArgsDirName is the name of the subdirectory holding per-workspace
	// remembered prompt-argument snapshots (one JSON file per workspace UUID).
	// See internal/rememberedargs.
	RememberedArgsDirName = "remembered-args"

	// RememberedArgsConversationDirName is the name of the subdirectory holding
	// per-session remembered prompt-argument snapshots (one JSON file per
	// session ID). Used for `remember: conversation` mode (mitto-47y.6.2).
	// See internal/rememberedargs.
	RememberedArgsConversationDirName = "remembered-args-conversation"

	// ChatHistoryDirName is the name of the subdirectory holding per-conversation
	// persisted input-history snapshots for `mitto conversation chat` (one JSON
	// file per conversation ID). See internal/chatui/inputhistory.go (mitto-pscc.11).
	ChatHistoryDirName = "chat-history"

	// PendingProcessorDispatchDirName is the name of the subdirectory holding
	// per-workspace spools of undelivered prompt-mode processor batches (one
	// JSON file per workspace UUID). Deliberately independent of any single
	// session's own directory, which may already be removed from disk by the
	// time a saturated dispatch gives up (mitto-3421). See
	// internal/processors.FilePendingDispatchStore.
	PendingProcessorDispatchDirName = "pending-processor-dispatch"
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
// It also creates the sessions and processors subdirectories.
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

	// Backward compatibility note: the processor Loader will automatically fall back to
	// loading from the legacy "hooks" directory if "processors" doesn't exist.
	// Log a migration hint when both directories are present to encourage migration.
	oldHooksDir := filepath.Join(dir, "hooks")
	processorsDir := filepath.Join(dir, ProcessorsDirName)
	if _, err := os.Stat(oldHooksDir); err == nil {
		if _, err := os.Stat(processorsDir); err == nil {
			slog.Info("Both 'hooks' and 'processors' directories exist. Only 'processors' will be loaded. Please migrate files from 'hooks' to 'processors'.",
				"hooks_path", oldHooksDir,
				"processors_path", processorsDir,
			)
		}
	}

	// Create processors subdirectory
	if err := os.MkdirAll(processorsDir, 0755); err != nil {
		return fmt.Errorf("failed to create processors directory %s: %w", processorsDir, err)
	}

	// Create prompts subdirectory
	promptsDir := filepath.Join(dir, PromptsDirName)
	if err := os.MkdirAll(promptsDir, 0755); err != nil {
		return fmt.Errorf("failed to create prompts directory %s: %w", promptsDir, err)
	}

	// Create agents subdirectory
	agentsDir := filepath.Join(dir, AgentsDirName)
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		return fmt.Errorf("failed to create agents directory %s: %w", agentsDir, err)
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

// FoldersPath returns the path to the folders.json file.
func FoldersPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, FoldersFileName), nil
}

// InstancePath returns the full path to the instance.json file.
// This file records the currently running server's connection details
// (url, api_prefix, external_url, token, pid, started_at) so local clients
// (e.g. the CLI) can discover a running `mitto web` / macOS app instance
// without extra configuration. See internal/instancefile for the reader/writer.
func InstancePath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, InstanceFileName), nil
}

// SessionsDir returns the full path to the sessions directory.
func SessionsDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, SessionsDirName), nil
}

// ProcessorsDir returns the full path to the processors directory.
func ProcessorsDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ProcessorsDirName), nil
}

// BuiltinProcessorsDir returns the full path to the builtin processors directory.
// This directory contains processors that are deployed from the embedded filesystem.
func BuiltinProcessorsDir() (string, error) {
	processorsDir, err := ProcessorsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(processorsDir, BuiltinProcessorsDirName), nil
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

// AgentsDir returns the full path to the agents directory.
func AgentsDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, AgentsDirName), nil
}

// BuiltinAgentsDir returns the full path to the builtin agents directory.
// This directory contains agent configs that are deployed from the embedded filesystem.
func BuiltinAgentsDir() (string, error) {
	agentsDir, err := AgentsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(agentsDir, BuiltinAgentsDirName), nil
}

// WorkspacePromptsDir returns the full path to the default workspace prompts directory.
// This is $MITTO_WORKING_DIR/.mitto/prompts/ and is automatically searched for prompts
// when a workspace is active, without requiring explicit configuration in .mittorc.
func WorkspacePromptsDir(workspaceRoot string) string {
	return filepath.Join(workspaceRoot, WorkspaceConfigDirName, PromptsDirName)
}

// WorkspaceProcessorsDir returns the full path to the default workspace processors directory.
// This is $MITTO_WORKING_DIR/.mitto/processors/ and is automatically searched for processors
// when a workspace is active, without requiring explicit configuration in .mittorc.
func WorkspaceProcessorsDir(workspaceRoot string) string {
	return filepath.Join(workspaceRoot, WorkspaceConfigDirName, ProcessorsDirName)
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

// DefenseBlocklistPath returns the full path to the scanner_blocklist.json file.
// This file stores blocked IPs for the scanner defense system.
func DefenseBlocklistPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, DefenseBlocklistFileName), nil
}

// MCPToolsCacheDir returns the directory holding per-workspace persisted
// real-MCP tools snapshots ($MITTO_DIR/mcp-tools-cache). The directory is not
// created here; callers persist via fileutil.WriteJSONAtomic, which creates it
// on first write.
func MCPToolsCacheDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, MCPToolsCacheDirName), nil
}

// StatsDir returns the directory holding the dashboard time-series stats
// database ($MITTO_DIR/stats). The directory is not created here; the first
// writer (internal/stats) creates it via os.MkdirAll before opening the
// SQLite file. Mirrors the MCPToolsCacheDir pattern.
func StatsDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, StatsDirName), nil
}

// RememberedArgsDir returns the directory holding per-workspace remembered
// prompt-argument snapshots ($MITTO_DIR/remembered-args). The directory is not
// created here; callers persist via fileutil.WriteJSONAtomic, which creates it
// on first write. Mirrors the MCPToolsCacheDir pattern (mitto-x8v).
func RememberedArgsDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, RememberedArgsDirName), nil
}

// RememberedArgsConversationDir returns the directory holding per-session
// remembered prompt-argument snapshots ($MITTO_DIR/remembered-args-conversation).
// The directory is not created here; callers persist via
// fileutil.WriteJSONAtomic, which creates it on first write. Mirrors the
// RememberedArgsDir pattern for `remember: conversation` mode (mitto-47y.6.2).
func RememberedArgsConversationDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, RememberedArgsConversationDirName), nil
}

// ChatHistoryDir returns the directory holding per-conversation persisted
// input-history snapshots for `mitto conversation chat`
// ($MITTO_DIR/chat-history). The directory is not created here; callers
// persist via fileutil.WriteJSONAtomic, which creates it on first write.
// Mirrors the MCPToolsCacheDir pattern.
func ChatHistoryDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ChatHistoryDirName), nil
}

// PendingProcessorDispatchDir returns the directory holding per-workspace
// spools of undelivered prompt-mode processor batches
// ($MITTO_DIR/pending-processor-dispatch). The directory is not created here;
// callers persist via fileutil.WriteJSONAtomic, which creates it on first
// write. Mirrors the RememberedArgsDir pattern (mitto-3421).
func PendingProcessorDispatchDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, PendingProcessorDispatchDirName), nil
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
