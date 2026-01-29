package secrets

// NoopStore is a no-op implementation of SecretStore for unsupported platforms.
// All operations return ErrNotSupported, and IsSupported returns false.
type NoopStore struct{}

// Get returns ErrNotSupported.
func (n *NoopStore) Get(service, account string) (string, error) {
	return "", ErrNotSupported
}

// Set returns ErrNotSupported.
func (n *NoopStore) Set(service, account, password string) error {
	return ErrNotSupported
}

// Delete returns ErrNotSupported.
func (n *NoopStore) Delete(service, account string) error {
	return ErrNotSupported
}

// IsSupported returns false for the NoopStore.
func (n *NoopStore) IsSupported() bool {
	return false
}
