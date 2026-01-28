package session

import (
	"github.com/inercia/mitto/internal/appdir"
)

// DefaultSessionDir returns the default session storage directory.
// It uses the Mitto data directory from appdir, which handles:
//   - MITTO_DIR environment variable override
//   - Platform-specific directories:
//   - Linux: $XDG_DATA_HOME/mitto/sessions or ~/.local/share/mitto/sessions
//   - macOS: ~/Library/Application Support/Mitto/sessions
//   - Windows: %APPDATA%\Mitto\sessions
func DefaultSessionDir() (string, error) {
	return appdir.SessionsDir()
}

// DefaultStore creates a new store using the default session directory.
func DefaultStore() (*Store, error) {
	dir, err := DefaultSessionDir()
	if err != nil {
		return nil, err
	}
	return NewStore(dir)
}
