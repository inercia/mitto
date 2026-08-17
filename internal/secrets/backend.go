package secrets

// secretStoreBlobBackend stores the complete vault under one legacy store account.
type secretStoreBlobBackend struct {
	store SecretStore
}

func newSecretStoreBlobBackend(store SecretStore) BlobBackend {
	return &secretStoreBlobBackend{store: store}
}

func (b *secretStoreBlobBackend) Load() ([]byte, error) {
	value, err := b.store.Get(ServiceName, AccountCredentialVault)
	return []byte(value), err
}

func (b *secretStoreBlobBackend) Save(data []byte) error {
	return b.store.Set(ServiceName, AccountCredentialVault, string(data))
}

func (b *secretStoreBlobBackend) IsSupported() bool {
	return b.store != nil && b.store.IsSupported()
}

type unsupportedBlobBackend struct{}

func (*unsupportedBlobBackend) Load() ([]byte, error) { return nil, ErrNotSupported }
func (*unsupportedBlobBackend) Save([]byte) error     { return ErrNotSupported }
func (*unsupportedBlobBackend) IsSupported() bool     { return false }
