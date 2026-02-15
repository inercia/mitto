package session

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/fileutil"
)

const (
	userDataFileName = "user-data.json"
)

// UserDataAttribute represents a single name-value pair of user data.
type UserDataAttribute struct {
	// Name is the attribute name (e.g., "JIRA ticket", "Description").
	Name string `json:"name"`
	// Value is the attribute value.
	Value string `json:"value"`
}

// UserData represents the user data for a conversation.
type UserData struct {
	// Attributes is the list of user data attributes.
	Attributes []UserDataAttribute `json:"attributes"`
}

// Validate validates all attributes in user data against the schema.
// If schema is nil or empty, no attributes are allowed.
// Empty user data (no attributes) is always valid.
func (d *UserData) Validate(schema *config.UserDataSchema) error {
	if d == nil || len(d.Attributes) == 0 {
		return nil
	}

	for _, attr := range d.Attributes {
		if attr.Name == "" {
			return fmt.Errorf("attribute name cannot be empty")
		}
		if err := schema.ValidateAttribute(attr.Name, attr.Value); err != nil {
			return err
		}
	}
	return nil
}

// userDataPath returns the user data file path for a session.
func (s *Store) userDataPath(sessionID string) string {
	return filepath.Join(s.sessionDir(sessionID), userDataFileName)
}

// GetUserData reads user data for a session.
// Returns empty UserData if the file doesn't exist.
func (s *Store) GetUserData(sessionID string) (*UserData, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, ErrStoreClosed
	}

	path := s.userDataPath(sessionID)
	var data UserData
	if err := fileutil.ReadJSON(path, &data); err != nil {
		if os.IsNotExist(err) {
			// Return empty user data if file doesn't exist
			return &UserData{Attributes: []UserDataAttribute{}}, nil
		}
		return nil, fmt.Errorf("failed to read user data: %w", err)
	}
	return &data, nil
}

// SetUserData writes user data for a session.
// Creates the user data file if it doesn't exist.
func (s *Store) SetUserData(sessionID string, data *UserData) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrStoreClosed
	}

	// Check if session exists
	if !s.sessionExists(sessionID) {
		return ErrSessionNotFound
	}

	path := s.userDataPath(sessionID)
	if err := fileutil.WriteJSONAtomic(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write user data: %w", err)
	}
	return nil
}

// sessionExists checks if a session directory exists (must be called with lock held).
func (s *Store) sessionExists(sessionID string) bool {
	info, err := os.Stat(s.sessionDir(sessionID))
	return err == nil && info.IsDir()
}
