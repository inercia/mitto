// Package secrets provides a platform-abstracted interface for secure credential storage.
// On macOS, credentials are stored in the system Keychain.
// On other platforms, a no-op fallback is used (credentials remain in settings.json).
package secrets

import "errors"

// Service name used for Mitto credentials in the system keychain.
const ServiceName = "Mitto"

// Account names for different credential types.
const (
	// AccountExternalAccess is the account name for external access credentials.
	AccountExternalAccess = "external-access"
)

// ErrNotFound is returned when a credential is not found in the store.
var ErrNotFound = errors.New("credential not found")

// ErrNotSupported is returned when the secret store is not supported on the current platform.
var ErrNotSupported = errors.New("secret store not supported on this platform")

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

// store is the package-level secret store instance, initialized at package load time.
// It is set by the platform-specific init() function.
var store SecretStore

// Default returns the default SecretStore for the current platform.
// This function always returns a valid store; on unsupported platforms,
// it returns a NoopStore that returns ErrNotSupported for all operations.
func Default() SecretStore {
	if store == nil {
		// Fallback to noop store if not initialized (should not happen)
		store = &NoopStore{}
	}
	return store
}

// IsSupported returns true if secure credential storage is available on this platform.
func IsSupported() bool {
	return Default().IsSupported()
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

// GetExternalAccessPassword retrieves the external access password from the secret store.
// Returns ErrNotFound if not stored in the secret store.
func GetExternalAccessPassword() (string, error) {
	return Get(ServiceName, AccountExternalAccess)
}

// SetExternalAccessPassword stores the external access password in the secret store.
func SetExternalAccessPassword(password string) error {
	return Set(ServiceName, AccountExternalAccess, password)
}

// DeleteExternalAccessPassword removes the external access password from the secret store.
func DeleteExternalAccessPassword() error {
	return Delete(ServiceName, AccountExternalAccess)
}
