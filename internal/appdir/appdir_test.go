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
