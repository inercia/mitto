package middleware

import (
	"bytes"
	"crypto/rand"
	"os"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/fileutil"
	"github.com/inercia/mitto/internal/logging"
)

// webauthnUserHandleBytes is the size in bytes of the randomly generated,
// stable user handle for the single External Access user. WebAuthn allows
// up to 64 bytes; 32 random bytes provide ample entropy while keeping the
// handle unrelated to (and not leaking) the configured username.
const webauthnUserHandleBytes = 32

// StoredCredential is the persisted representation of one WebAuthn
// credential bound to the single External Access user. It embeds the
// go-webauthn library's Credential (which already carries the credential
// ID, COSE public key, sign count and AAGUID via its Authenticator field,
// and supported transports) and adds the bookkeeping timestamps the
// library itself does not track.
type StoredCredential struct {
	Credential webauthn.Credential `json:"credential"`
	CreatedAt  time.Time           `json:"created_at"`
	LastUsedAt time.Time           `json:"last_used_at"`
}

// persistedPasskeys is the on-disk structure of webauthn_credentials.json.
type persistedPasskeys struct {
	UserHandle  []byte             `json:"user_handle"`
	Credentials []StoredCredential `json:"credentials"`
}

// PasskeyStore manages persisted WebAuthn (passkey) credentials for the
// single External Access user. It mirrors AuthManager's auth_sessions.json
// load/save/mutex pattern (see auth.go loadSessions/saveSessionsLocked).
type PasskeyStore struct {
	mu          sync.RWMutex
	userHandle  []byte
	credentials []StoredCredential
}

// NewPasskeyStore creates a PasskeyStore, loading any persisted credentials
// and stable user handle from disk. A missing file is not an error (fresh
// install); a stable user handle is lazily generated and persisted on first
// use via UserHandle.
func NewPasskeyStore() *PasskeyStore {
	s := &PasskeyStore{}
	s.load()
	return s
}

// load reads persisted credentials from disk, if any.
func (s *PasskeyStore) load() {
	logger := logging.Auth()

	path, err := appdir.WebAuthnCredentialsPath()
	if err != nil {
		logger.Warn("WEBAUTHN: Failed to get credentials path", "error", err)
		return
	}

	var file persistedPasskeys
	if err := fileutil.ReadJSON(path, &file); err != nil {
		if os.IsNotExist(err) {
			// No credentials file yet, that's fine.
			return
		}
		logger.Warn("WEBAUTHN: Failed to load credentials from disk", "error", err, "path", path)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.userHandle = file.UserHandle
	s.credentials = file.Credentials
}

// saveLocked persists the current state to disk. Caller must hold s.mu
// (write lock).
func (s *PasskeyStore) saveLocked() {
	logger := logging.Auth()

	path, err := appdir.WebAuthnCredentialsPath()
	if err != nil {
		logger.Warn("WEBAUTHN: Failed to get credentials path", "error", err)
		return
	}

	file := persistedPasskeys{
		UserHandle:  s.userHandle,
		Credentials: s.credentials,
	}

	if err := fileutil.WriteJSONAtomic(path, &file, 0600); err != nil {
		logger.Warn("WEBAUTHN: Failed to save credentials to disk", "error", err, "path", path)
		return
	}

	logger.Debug("WEBAUTHN: Saved credentials to disk", "count", len(file.Credentials), "path", path)
}

// UserHandle returns the stable, randomly generated user handle for the
// single External Access user, lazily generating and persisting one on
// first use.
func (s *PasskeyStore) UserHandle() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.userHandle) > 0 {
		return s.userHandle, nil
	}

	handle := make([]byte, webauthnUserHandleBytes)
	if _, err := rand.Read(handle); err != nil {
		return nil, err
	}
	s.userHandle = handle
	s.saveLocked()
	return handle, nil
}

// List returns a copy of all stored credentials.
func (s *PasskeyStore) List() []StoredCredential {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]StoredCredential, len(s.credentials))
	copy(out, s.credentials)
	return out
}

// GetByCredentialID returns the stored credential with the given credential
// ID, if any.
func (s *PasskeyStore) GetByCredentialID(id []byte) (StoredCredential, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, c := range s.credentials {
		if bytes.Equal(c.Credential.ID, id) {
			return c, true
		}
	}
	return StoredCredential{}, false
}

// Add stores a new credential, or replaces an existing one with the same
// credential ID (preserving its original CreatedAt), and persists the
// change.
func (s *PasskeyStore) Add(cred webauthn.Credential) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, c := range s.credentials {
		if bytes.Equal(c.Credential.ID, cred.ID) {
			s.credentials[i] = StoredCredential{Credential: cred, CreatedAt: c.CreatedAt, LastUsedAt: now}
			s.saveLocked()
			return
		}
	}

	s.credentials = append(s.credentials, StoredCredential{Credential: cred, CreatedAt: now, LastUsedAt: now})
	s.saveLocked()
}

// Delete removes the credential with the given credential ID, if present,
// and persists the change. Returns true if a credential was removed.
func (s *PasskeyStore) Delete(id []byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, c := range s.credentials {
		if bytes.Equal(c.Credential.ID, id) {
			s.credentials = append(s.credentials[:i], s.credentials[i+1:]...)
			s.saveLocked()
			return true
		}
	}
	return false
}

// UpdateSignCount updates the sign counter and last-used timestamp for the
// credential with the given credential ID, persisting the change. Returns
// true if the credential was found and updated.
func (s *PasskeyStore) UpdateSignCount(id []byte, signCount uint32) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, c := range s.credentials {
		if bytes.Equal(c.Credential.ID, id) {
			s.credentials[i].Credential.Authenticator.SignCount = signCount
			s.credentials[i].LastUsedAt = time.Now()
			s.saveLocked()
			return true
		}
	}
	return false
}

// User returns a webauthn.User implementation backed by this store's stable
// user handle and stored credentials, for the given display username.
// Relying Party (RP) configuration is intentionally out of scope here.
func (s *PasskeyStore) User(username string) (webauthn.User, error) {
	handle, err := s.UserHandle()
	if err != nil {
		return nil, err
	}
	return &passkeyUser{handle: handle, username: username, store: s}, nil
}

// passkeyUser implements webauthn.User for the single External Access user.
type passkeyUser struct {
	handle   []byte
	username string
	store    *PasskeyStore
}

func (u *passkeyUser) WebAuthnID() []byte { return u.handle }

func (u *passkeyUser) WebAuthnName() string { return u.username }

func (u *passkeyUser) WebAuthnDisplayName() string { return u.username }

func (u *passkeyUser) WebAuthnCredentials() []webauthn.Credential {
	stored := u.store.List()
	out := make([]webauthn.Credential, len(stored))
	for i, c := range stored {
		out[i] = c.Credential
	}
	return out
}
