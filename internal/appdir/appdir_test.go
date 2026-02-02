package appdir

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDir_EnvOverride(t *testing.T) {
	// Use t.Setenv which automatically restores the original value
	customDir := t.TempDir()
	t.Setenv(MittoDirEnv, customDir)
	ResetCache()
	t.Cleanup(ResetCache)

	dir, err := Dir()
	if err != nil {
		t.Fatalf("Dir() failed: %v", err)
	}

	if dir != customDir {
		t.Errorf("Dir() = %q, want %q", dir, customDir)
	}
}

func TestDir_DefaultPath(t *testing.T) {
	// Unset the env var to test default path resolution
	// Use t.Setenv with empty string, then unset - t.Setenv will restore original
	t.Setenv(MittoDirEnv, "")
	os.Unsetenv(MittoDirEnv)
	ResetCache()
	t.Cleanup(ResetCache)

	dir, err := Dir()
	if err != nil {
		t.Fatalf("Dir() failed: %v", err)
	}

	// Verify it contains "mitto" or "Mitto" in the path
	if !strings.Contains(strings.ToLower(dir), "mitto") {
		t.Errorf("Dir() = %q, expected path to contain 'mitto'", dir)
	}
}

func TestEnsureDir(t *testing.T) {
	// Use temp dir
	tmpDir := filepath.Join(t.TempDir(), "mitto-test")
	t.Setenv(MittoDirEnv, tmpDir)
	ResetCache()
	t.Cleanup(ResetCache)

	// Ensure the directory doesn't exist yet
	if _, err := os.Stat(tmpDir); !os.IsNotExist(err) {
		t.Fatalf("temp dir should not exist initially")
	}

	// Call EnsureDir
	if err := EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() failed: %v", err)
	}

	// Verify main directory exists
	info, err := os.Stat(tmpDir)
	if err != nil {
		t.Fatalf("main dir does not exist after EnsureDir(): %v", err)
	}
	if !info.IsDir() {
		t.Error("main path is not a directory")
	}

	// Verify sessions subdirectory exists
	sessionsDir := filepath.Join(tmpDir, SessionsDirName)
	info, err = os.Stat(sessionsDir)
	if err != nil {
		t.Fatalf("sessions dir does not exist after EnsureDir(): %v", err)
	}
	if !info.IsDir() {
		t.Error("sessions path is not a directory")
	}

	// Verify prompts subdirectory exists
	promptsDir := filepath.Join(tmpDir, PromptsDirName)
	info, err = os.Stat(promptsDir)
	if err != nil {
		t.Fatalf("prompts dir does not exist after EnsureDir(): %v", err)
	}
	if !info.IsDir() {
		t.Error("prompts path is not a directory")
	}
}

func TestSettingsPath(t *testing.T) {
	customDir := t.TempDir()
	t.Setenv(MittoDirEnv, customDir)
	ResetCache()
	t.Cleanup(ResetCache)

	settingsPath, err := SettingsPath()
	if err != nil {
		t.Fatalf("SettingsPath() failed: %v", err)
	}

	expected := filepath.Join(customDir, SettingsFileName)
	if settingsPath != expected {
		t.Errorf("SettingsPath() = %q, want %q", settingsPath, expected)
	}
}

func TestSessionsDir(t *testing.T) {
	customDir := t.TempDir()
	t.Setenv(MittoDirEnv, customDir)
	ResetCache()
	t.Cleanup(ResetCache)

	sessionsDir, err := SessionsDir()
	if err != nil {
		t.Fatalf("SessionsDir() failed: %v", err)
	}

	expected := filepath.Join(customDir, SessionsDirName)
	if sessionsDir != expected {
		t.Errorf("SessionsDir() = %q, want %q", sessionsDir, expected)
	}
}

func TestWorkspacesPath(t *testing.T) {
	customDir := t.TempDir()
	t.Setenv(MittoDirEnv, customDir)
	ResetCache()
	t.Cleanup(ResetCache)

	workspacesPath, err := WorkspacesPath()
	if err != nil {
		t.Fatalf("WorkspacesPath() failed: %v", err)
	}

	expected := filepath.Join(customDir, WorkspacesFileName)
	if workspacesPath != expected {
		t.Errorf("WorkspacesPath() = %q, want %q", workspacesPath, expected)
	}
}

func TestHooksDir(t *testing.T) {
	customDir := t.TempDir()
	t.Setenv(MittoDirEnv, customDir)
	ResetCache()
	t.Cleanup(ResetCache)

	hooksDir, err := HooksDir()
	if err != nil {
		t.Fatalf("HooksDir() failed: %v", err)
	}

	expected := filepath.Join(customDir, HooksDirName)
	if hooksDir != expected {
		t.Errorf("HooksDir() = %q, want %q", hooksDir, expected)
	}
}

func TestPromptsDir(t *testing.T) {
	customDir := t.TempDir()
	t.Setenv(MittoDirEnv, customDir)
	ResetCache()
	t.Cleanup(ResetCache)

	promptsDir, err := PromptsDir()
	if err != nil {
		t.Fatalf("PromptsDir() failed: %v", err)
	}

	expected := filepath.Join(customDir, PromptsDirName)
	if promptsDir != expected {
		t.Errorf("PromptsDir() = %q, want %q", promptsDir, expected)
	}
}

func TestAuthSessionsPath(t *testing.T) {
	customDir := t.TempDir()
	t.Setenv(MittoDirEnv, customDir)
	ResetCache()
	t.Cleanup(ResetCache)

	authSessionsPath, err := AuthSessionsPath()
	if err != nil {
		t.Fatalf("AuthSessionsPath() failed: %v", err)
	}

	expected := filepath.Join(customDir, AuthSessionsFileName)
	if authSessionsPath != expected {
		t.Errorf("AuthSessionsPath() = %q, want %q", authSessionsPath, expected)
	}
}

func TestRCFilePath_EnvOverride(t *testing.T) {
	// Create a temp RC file
	tmpDir := t.TempDir()
	rcFile := filepath.Join(tmpDir, "custom-mittorc")
	if err := os.WriteFile(rcFile, []byte("# test rc"), 0644); err != nil {
		t.Fatalf("failed to create test RC file: %v", err)
	}

	t.Setenv(MittoRCEnv, rcFile)

	path, err := RCFilePath()
	if err != nil {
		t.Fatalf("RCFilePath() failed: %v", err)
	}

	if path != rcFile {
		t.Errorf("RCFilePath() = %q, want %q", path, rcFile)
	}
}

func TestRCFilePath_EnvOverride_FileNotExists(t *testing.T) {
	// Set env to a non-existent file - should fall through to other locations
	t.Setenv(MittoRCEnv, "/nonexistent/path/to/mittorc")

	// This should not error, just return empty string if no RC file found
	path, err := RCFilePath()
	if err != nil {
		t.Fatalf("RCFilePath() failed: %v", err)
	}

	// Path should be empty or a valid existing file (if user has one)
	if path != "" {
		// If a path is returned, verify it exists
		if _, err := os.Stat(path); err != nil {
			t.Errorf("RCFilePath() returned %q but file does not exist", path)
		}
	}
}

func TestRCFilePath_HomeDirRC(t *testing.T) {
	// Clear the env override
	t.Setenv(MittoRCEnv, "")
	os.Unsetenv(MittoRCEnv)

	// Get home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("cannot get home directory: %v", err)
	}

	// Check if ~/.mittorc exists
	homeMittoRC := filepath.Join(homeDir, ".mittorc")
	if _, err := os.Stat(homeMittoRC); os.IsNotExist(err) {
		// No RC file exists - RCFilePath should return empty string
		path, err := RCFilePath()
		if err != nil {
			t.Fatalf("RCFilePath() failed: %v", err)
		}
		// Path could be empty or point to XDG location
		if path != "" {
			// Verify the returned path exists
			if _, err := os.Stat(path); err != nil {
				t.Errorf("RCFilePath() returned %q but file does not exist", path)
			}
		}
	} else {
		// RC file exists - should return it
		path, err := RCFilePath()
		if err != nil {
			t.Fatalf("RCFilePath() failed: %v", err)
		}
		if path != homeMittoRC {
			t.Errorf("RCFilePath() = %q, want %q", path, homeMittoRC)
		}
	}
}
