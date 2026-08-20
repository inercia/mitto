package session

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/inercia/mitto/internal/fileutil"
)

// ReadSessionSidecarJSON reads a JSON sidecar while holding the session's read lock.
func (s *Store) ReadSessionSidecarJSON(sessionID, name string, value any) error {
	if err := validateSidecarName(name); err != nil {
		return err
	}
	unlock, err := s.lockSessionRead(sessionID)
	if err != nil {
		return err
	}
	defer unlock()
	if _, err := os.Stat(s.metadataPath(sessionID)); err != nil {
		if os.IsNotExist(err) {
			return ErrSessionNotFound
		}
		return err
	}
	return fileutil.ReadJSON(filepath.Join(s.sessionDir(sessionID), name), value)
}

// WriteSessionSidecarJSON atomically writes a JSON sidecar while preventing
// concurrent session deletion from removing or recreating its directory.
func (s *Store) WriteSessionSidecarJSON(sessionID, name string, value any) error {
	if err := validateSidecarName(name); err != nil {
		return err
	}
	unlock, err := s.lockSessionWrite(sessionID)
	if err != nil {
		return err
	}
	defer unlock()
	if _, err := os.Stat(s.metadataPath(sessionID)); err != nil {
		if os.IsNotExist(err) {
			return ErrSessionNotFound
		}
		return err
	}
	return fileutil.WriteJSONAtomic(filepath.Join(s.sessionDir(sessionID), name), value, 0600)
}

func validateSidecarName(name string) error {
	if name == "" || name == "." || name == ".." || filepath.IsAbs(name) || filepath.Base(name) != name {
		return fmt.Errorf("invalid session sidecar name %q", name)
	}
	return nil
}
