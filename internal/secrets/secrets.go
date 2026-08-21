// Package secrets provides a process-owned, platform-backed credential vault.
// Darwin stores one versioned Keychain blob; Linux uses a hardened local file.
package secrets

import (
	"crypto/subtle"
	"errors"
	"sync"
)

// Service name used for Mitto credentials in the system keychain.
const ServiceName = "Mitto"

// Account names for different credential types.
const (
	// AccountExternalAccess is the account name for external access credentials.
	AccountExternalAccess = "external-access"
	// AccountSharedToken is the account name for the shared bearer token used
	// by programmatic clients (SDK, CLI).
	AccountSharedToken = "shared-token"
	// AccountCredentialVault stores the complete versioned vault as one blob.
	AccountCredentialVault = "credentials-v1"
)

// ErrNotFound is returned when a credential is not found in the store.
var ErrNotFound = errors.New("credential not found")

// ErrNotSupported is returned when the secret store is not supported on the current platform.
var ErrNotSupported = errors.New("secret store not supported on this platform")

// ErrInvalidCredential is returned for malformed references or empty values.
var ErrInvalidCredential = errors.New("invalid credential")

// ErrCorruptVault is returned when a persisted vault cannot be decoded safely.
var ErrCorruptVault = errors.New("credential vault is corrupt")

// ErrUnsupportedVaultVersion is returned for a schema newer or older than this binary.
var ErrUnsupportedVaultVersion = errors.New("unsupported credential vault version")

// ErrVerificationFailed prevents legacy data deletion after an unverified write.
var ErrVerificationFailed = errors.New("credential vault verification failed")

// ErrUnsafeVaultPath is returned when Linux vault path hardening fails.
var ErrUnsafeVaultPath = errors.New("unsafe credential vault path")

// SecretStore provides an interface for secure credential storage.
// Implementations should be safe for concurrent use.
type SecretStore interface {
	// Get retrieves a password for the given service and account.
	// Returns ErrNotFound if the credential does not exist.
	Get(service, account string) (string, error)

	// Set stores a password for the given service and account.
	// If a credential already exists, it is updated.
	Set(service, account, password string) error

	// Delete removes a credential for the given service and account.
	// Returns ErrNotFound if the credential does not exist.
	Delete(service, account string) error

	// IsSupported returns true if this store is functional on the current platform.
	IsSupported() bool
}

var (
	globalsMu sync.RWMutex
	store     SecretStore
	manager   *Manager
)

func setPlatformStores(s SecretStore, backend BlobBackend) {
	globalsMu.Lock()
	defer globalsMu.Unlock()
	store = s
	manager = NewManager(backend)
}

// Default returns the default SecretStore for the current platform.
// This function always returns a valid store; on unsupported platforms,
// it returns a NoopStore that returns ErrNotSupported for all operations.
func Default() SecretStore {
	globalsMu.RLock()
	s := store
	globalsMu.RUnlock()
	if s != nil {
		return s
	}
	return &NoopStore{}
}

// DefaultManager returns the process-owned, lazily-loaded credential manager.
func DefaultManager() *Manager {
	globalsMu.RLock()
	m := manager
	globalsMu.RUnlock()
	if m != nil {
		return m
	}
	return NewManager(&unsupportedBlobBackend{})
}

// IsSupported returns true if secure credential storage is available on this platform.
func IsSupported() bool {
	return DefaultManager().IsSupported()
}

// Get retrieves a password for the given service and account using the default store.
func Get(service, account string) (string, error) {
	return Default().Get(service, account)
}

// Set stores a password for the given service and account using the default store.
func Set(service, account, password string) error {
	return Default().Set(service, account, password)
}

// Delete removes a credential for the given service and account using the default store.
func Delete(service, account string) error {
	return Default().Delete(service, account)
}

// Put stores a typed, scoped credential in the unified vault.
func Put(ref CredentialRef, value string) error { return DefaultManager().Put(ref, value) }

// Resolve retrieves a typed, scoped credential from the unified vault.
func Resolve(ref CredentialRef) (string, error) { return DefaultManager().Resolve(ref) }

// Status reports whether a typed credential is configured without returning it.
func Status(ref CredentialRef) (CredentialStatus, error) { return DefaultManager().Status(ref) }

// DeleteCredential removes a typed, scoped credential from the unified vault.
func DeleteCredential(ref CredentialRef) error { return DefaultManager().Delete(ref) }

// GetExternalAccessPassword retrieves the external access password from the secret store.
// Returns ErrNotFound if not stored in the secret store.
func GetExternalAccessPassword() (string, error) {
	return getCompatibleCredential(GlobalCredential(AccountExternalAccess), AccountExternalAccess)
}

// SetExternalAccessPassword stores the external access password in the secret store.
func SetExternalAccessPassword(password string) error {
	return setCompatibleCredential(GlobalCredential(AccountExternalAccess), AccountExternalAccess, password)
}

// DeleteExternalAccessPassword removes the external access password from the secret store.
func DeleteExternalAccessPassword() error {
	return deleteCompatibleCredential(GlobalCredential(AccountExternalAccess), AccountExternalAccess)
}

// GetSharedToken retrieves the shared bearer token from the secret store.
// Returns ErrNotFound if not stored in the secret store.
func GetSharedToken() (string, error) {
	return getCompatibleCredential(GlobalCredential(AccountSharedToken), AccountSharedToken)
}

// SetSharedToken stores the shared bearer token in the secret store.
func SetSharedToken(token string) error {
	return setCompatibleCredential(GlobalCredential(AccountSharedToken), AccountSharedToken, token)
}

// DeleteSharedToken removes the shared bearer token from the secret store.
func DeleteSharedToken() error {
	return deleteCompatibleCredential(GlobalCredential(AccountSharedToken), AccountSharedToken)
}

func getCompatibleCredential(ref CredentialRef, legacyAccount string) (string, error) {
	m := DefaultManager()
	value, vaultErr := m.Resolve(ref)
	if vaultErr == nil {
		// A prior verified migration may have failed only while deleting the
		// legacy account. Retry that cleanup once per process after restart, but
		// delete only when both stores contain the exact same credential.
		if m.claimLegacyCleanupCheck(ref) {
			legacy, err := Get(ServiceName, legacyAccount)
			if err == nil && subtle.ConstantTimeCompare([]byte(legacy), []byte(value)) == 1 {
				_ = Delete(ServiceName, legacyAccount)
			}
		}
		return value, nil
	}
	legacy, legacyErr := Get(ServiceName, legacyAccount)
	if legacyErr != nil {
		if !errors.Is(vaultErr, ErrNotFound) && !errors.Is(vaultErr, ErrNotSupported) {
			return "", vaultErr
		}
		return "", legacyErr
	}

	// Migration is best-effort: the verified legacy value remains authoritative
	// for this call unless the new vault can store and read it back exactly. A
	// migration error is intentionally not returned: availability of the legacy
	// value is the compatibility contract, and no secret-aware logger belongs here.
	_ = setCompatibleCredential(ref, legacyAccount, legacy)
	return legacy, nil
}

func setCompatibleCredential(ref CredentialRef, legacyAccount, value string) error {
	if err := DefaultManager().put(ref, value, true); err != nil {
		return err
	}
	// A failed legacy cleanup leaves a duplicate. Vault-first resolution keeps
	// the verified credential authoritative; the compatibility read path retries
	// cleanup once when a new process manager loads this vault.
	_ = Delete(ServiceName, legacyAccount)
	return nil
}

func deleteCompatibleCredential(ref CredentialRef, legacyAccount string) error {
	legacyErr := Delete(ServiceName, legacyAccount)
	vaultErr := DeleteCredential(ref)
	legacyDeleted := legacyErr == nil
	vaultDeleted := vaultErr == nil
	if legacyDeleted || vaultDeleted {
		if unexpectedDeleteError(legacyErr) {
			return legacyErr
		}
		if unexpectedDeleteError(vaultErr) {
			return vaultErr
		}
		return nil
	}
	if unexpectedDeleteError(vaultErr) {
		return vaultErr
	}
	if unexpectedDeleteError(legacyErr) {
		return legacyErr
	}
	if errors.Is(vaultErr, ErrNotSupported) && errors.Is(legacyErr, ErrNotSupported) {
		return ErrNotSupported
	}
	return ErrNotFound
}

func unexpectedDeleteError(err error) bool {
	return err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrNotSupported)
}
