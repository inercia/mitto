// Package session provides session persistence and management for Mitto.
package session

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/inercia/mitto/internal/fileutil"
)

const (
	callbackFileName    = "callback.json"
	callbackTokenPrefix = "cb_"
	callbackTokenBytes  = 32 // 256-bit entropy
)

var (
	// ErrCallbackNotFound is returned when no callback is configured.
	ErrCallbackNotFound = errors.New("callback not found")
)

// CallbackConfig stored in callback.json in the session directory.
type CallbackConfig struct {
	Token     string    `json:"token"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CallbackStore manages the callback token for a single session.
// It is safe for concurrent use.
type CallbackStore struct {
	sessionDir string
	mu         sync.RWMutex
}

// NewCallbackStore creates a new CallbackStore for the given session directory.
func NewCallbackStore(sessionDir string) *CallbackStore {
	return &CallbackStore{
		sessionDir: sessionDir,
	}
}

// callbackPath returns the path to the callback.json file.
func (cs *CallbackStore) callbackPath() string {
	return filepath.Join(cs.sessionDir, callbackFileName)
}

// Get retrieves the current callback configuration.
// Returns ErrCallbackNotFound if no callback is configured.
func (cs *CallbackStore) Get() (*CallbackConfig, error) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	var c CallbackConfig
	err := fileutil.ReadJSON(cs.callbackPath(), &c)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrCallbackNotFound
		}
		return nil, fmt.Errorf("failed to read callback file: %w", err)
	}
	return &c, nil
}

// GenerateToken creates or rotates the callback token.
// If a token already exists, it is replaced (rotation).
// Returns the new token string.
func (cs *CallbackStore) GenerateToken() (string, error) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	// Generate random bytes
	randomBytes := make([]byte, callbackTokenBytes)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", fmt.Errorf("failed to generate random token: %w", err)
	}

	// Encode to hex and prepend prefix
	token := callbackTokenPrefix + hex.EncodeToString(randomBytes)

	now := time.Now().UTC()

	// Check if this is an update or create
	existing, err := cs.getUnlocked()

	var config CallbackConfig
	if err == nil && existing != nil {
		// Update: preserve created_at
		config.CreatedAt = existing.CreatedAt
	} else {
		// Create: set created_at
		config.CreatedAt = now
	}

	config.Token = token
	config.UpdatedAt = now

	if err := fileutil.WriteJSONAtomic(cs.callbackPath(), &config, 0644); err != nil {
		return "", fmt.Errorf("failed to write callback file: %w", err)
	}

	return token, nil
}

// Revoke deletes the callback configuration.
// Returns ErrCallbackNotFound if no callback exists.
func (cs *CallbackStore) Revoke() error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	err := os.Remove(cs.callbackPath())
	if err != nil {
		if os.IsNotExist(err) {
			return ErrCallbackNotFound
		}
		return fmt.Errorf("failed to delete callback file: %w", err)
	}
	return nil
}

// getUnlocked reads the callback file without locking (caller must hold lock).
func (cs *CallbackStore) getUnlocked() (*CallbackConfig, error) {
	var c CallbackConfig
	err := fileutil.ReadJSON(cs.callbackPath(), &c)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrCallbackNotFound
		}
		return nil, fmt.Errorf("failed to read callback file: %w", err)
	}
	return &c, nil
}

// ValidateCallbackToken checks if a token has the correct format.
// Must be "cb_" + 64 hex characters (total 67 characters).
func ValidateCallbackToken(token string) bool {
	// Check length: cb_ (3) + 64 hex = 67 total
	if len(token) != len(callbackTokenPrefix)+64 {
		return false
	}

	// Check prefix
	if len(token) < len(callbackTokenPrefix) || token[:len(callbackTokenPrefix)] != callbackTokenPrefix {
		return false
	}

	// Validate hex part
	hexPart := token[len(callbackTokenPrefix):]
	_, err := hex.DecodeString(hexPart)
	return err == nil
}
