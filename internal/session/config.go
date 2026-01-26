package session

import (
	"os"
	"path/filepath"
)

const (
	// DefaultSessionDirName is the default name for the sessions directory.
	DefaultSessionDirName = "sessions"
)

// DefaultSessionDir returns the default session storage directory.
// It uses XDG_DATA_HOME if set, otherwise falls back to ~/.local/share/mitto/sessions.
func DefaultSessionDir() (string, error) {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dataHome = filepath.Join(homeDir, ".local", "share")
	}
	return filepath.Join(dataHome, "mitto", DefaultSessionDirName), nil
}

// DefaultStore creates a new store using the default session directory.
func DefaultStore() (*Store, error) {
	dir, err := DefaultSessionDir()
	if err != nil {
		return nil, err
	}
	return NewStore(dir)
}
