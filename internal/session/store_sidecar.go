package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/inercia/mitto/internal/fileutil"
)

// WriteSessionSidecarJSONDurable opts into checked durability without changing
// the behavior of existing sidecar writers.
func (s *Store) WriteSessionSidecarJSONDurable(sessionID, name string, value any) error {
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
	return fileutil.WriteJSONAtomicDurableIfDirExists(filepath.Join(s.sessionDir(sessionID), name), value, 0600)
}

// MigrateSessionSidecarJSON holds the session write lock throughout read,
// conversion, recoverable snapshot and replacement. convert must be pure and
// must not call Store or collector methods. A nil replacement means no migration.
// A missing sidecar is passed as nil, but a missing parent is always an error.
// The complete original JSON is snapshotted, including unknown fields. Existing
// snapshots must match semantically (numbers are never converted to float64).
// Returning the unchanged source reaffirms its durability without replacing the
// snapshot; the converter must validate its version and recovery artifact first.
func (s *Store) MigrateSessionSidecarJSON(sessionID, name, backupName string, convert func(json.RawMessage, json.RawMessage) (json.RawMessage, error)) (json.RawMessage, error) {
	return s.migrateSessionSidecarJSON(sessionID, name, backupName, convert, fileutil.WriteJSONAtomicDurableIfDirExists)
}

func (s *Store) migrateSessionSidecarJSON(sessionID, name, backupName string, convert func(json.RawMessage, json.RawMessage) (json.RawMessage, error), write func(string, any, os.FileMode) error) (json.RawMessage, error) {
	if err := validateSidecarName(name); err != nil {
		return nil, err
	}
	if err := validateSidecarName(backupName); err != nil {
		return nil, err
	}
	if name == backupName || convert == nil {
		return nil, fmt.Errorf("migration requires distinct sidecars and a converter")
	}
	unlock, err := s.lockSessionWrite(sessionID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if _, err := os.Stat(s.metadataPath(sessionID)); err != nil {
		if os.IsNotExist(err) {
			return nil, ErrSessionNotFound
		}
		return nil, err
	}
	path := filepath.Join(s.sessionDir(sessionID), name)
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if os.IsNotExist(err) {
		raw = nil
	}
	backupPath := filepath.Join(s.sessionDir(sessionID), backupName)
	backup, err := os.ReadFile(backupPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if os.IsNotExist(err) {
		backup = nil
	}
	next, err := convert(bytes.Clone(raw), bytes.Clone(backup))
	if err != nil {
		return nil, err
	}
	if next == nil {
		return bytes.Clone(raw), nil
	}
	if !json.Valid(raw) || !json.Valid(next) {
		return nil, fmt.Errorf("migration requires valid source and replacement JSON")
	}
	if bytes.Equal(raw, next) {
		if err := write(path, next, 0600); err != nil {
			return nil, fmt.Errorf("reaffirm sidecar durability: %w", err)
		}
		return bytes.Clone(next), nil
	}
	if backup != nil {
		original, e1 := canonicalSidecarJSON(raw)
		snapshot, e2 := canonicalSidecarJSON(backup)
		if e1 != nil || e2 != nil || !bytes.Equal(original, snapshot) {
			return nil, fmt.Errorf("corrupt or conflicting migration snapshot %s", backupName)
		}
	}
	// Rewrite even a matching snapshot: a prior post-rename sync error may have
	// left it visible without acknowledged durability. Never publish first.
	if err := write(backupPath, json.RawMessage(raw), 0600); err != nil {
		return nil, fmt.Errorf("persist migration snapshot: %w", err)
	}
	if err := write(path, next, 0600); err != nil {
		return nil, fmt.Errorf("persist migrated sidecar: %w", err)
	}
	return bytes.Clone(next), nil
}

func canonicalSidecarJSON(raw []byte) ([]byte, error) {
	if !json.Valid(raw) {
		return nil, fmt.Errorf("invalid JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

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

// UpdateSessionSidecarJSON performs a read-modify-write of a sidecar under the
// session write lock, so concurrent updates cannot lose each other's changes.
// update receives the current bytes (nil when the sidecar is absent) and returns
// the replacement, or nil to leave the sidecar untouched. The replacement is
// written durably and is never created for a deleted session.
func (s *Store) UpdateSessionSidecarJSON(sessionID, name string, update func(current []byte) ([]byte, error)) error {
	if err := validateSidecarName(name); err != nil {
		return err
	}
	if update == nil {
		return fmt.Errorf("sidecar update requires an update function")
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
	path := filepath.Join(s.sessionDir(sessionID), name)
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if os.IsNotExist(err) {
		raw = nil
	}
	next, err := update(bytes.Clone(raw))
	if err != nil {
		return err
	}
	if next == nil {
		return nil
	}
	if !json.Valid(next) {
		return fmt.Errorf("sidecar %s update produced invalid JSON", name)
	}
	return fileutil.WriteJSONAtomicDurableIfDirExists(path, json.RawMessage(next), 0600)
}

func validateSidecarName(name string) error {
	if name == "" || name == "." || name == ".." || filepath.IsAbs(name) || filepath.Base(name) != name {
		return fmt.Errorf("invalid session sidecar name %q", name)
	}
	return nil
}
