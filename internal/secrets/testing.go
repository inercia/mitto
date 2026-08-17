package secrets

import "sync"

// SetStoreForTest overrides the package-level secret store for the duration
// of a test and returns a restore function that reinstates the previous
// store. Callers should invoke the returned function via t.Cleanup (or a
// plain defer) so the override never leaks across tests:
//
//	t.Cleanup(secrets.SetStoreForTest(myFakeStore))
//
// This exists so tests elsewhere in the tree (e.g. internal/config, which
// exercises Keychain-migration code paths via LoadSettings) can redirect
// every Get/Set/Delete through an in-memory fake instead of ever touching
// the real platform store (macOS Keychain). Without this seam, any test
// whose fixture carries a non-empty password/token silently overwrites the
// developer's real credentials (mitto-klux).
//
// It is exported from a non-_test.go file -- rather than living in
// secrets_test.go -- because Go does not allow importing _test.go-only
// symbols across package boundaries, and this seam must be callable from
// other packages' own test files.
func SetStoreForTest(s SecretStore) (restore func()) {
	globalsMu.Lock()
	prevStore, prevManager := store, manager
	store = s
	manager = NewManager(newSecretStoreBlobBackend(s))
	globalsMu.Unlock()
	return func() {
		globalsMu.Lock()
		store, manager = prevStore, prevManager
		globalsMu.Unlock()
	}
}

// FakeStore is an in-memory SecretStore for use with SetStoreForTest. It
// never touches any real platform credential store, so it is safe to drive
// through code paths (like internal/config's Keychain-migration logic) that
// would otherwise write to the production macOS Keychain during tests.
type FakeStore struct {
	mu   sync.Mutex
	data map[string]string
}

// NewFakeStore returns a ready-to-use empty FakeStore.
func NewFakeStore() *FakeStore {
	return &FakeStore{data: make(map[string]string)}
}

func fakeStoreKey(service, account string) string { return service + "\x00" + account }

// Get implements SecretStore.
func (f *FakeStore) Get(service, account string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.data[fakeStoreKey(service, account)]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

// Set implements SecretStore.
func (f *FakeStore) Set(service, account, password string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data[fakeStoreKey(service, account)] = password
	return nil
}

// Delete implements SecretStore.
func (f *FakeStore) Delete(service, account string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := fakeStoreKey(service, account)
	if _, ok := f.data[key]; !ok {
		return ErrNotFound
	}
	delete(f.data, key)
	return nil
}

// IsSupported implements SecretStore. FakeStore always reports supported so
// migration code paths that gate on secrets.IsSupported() actually run
// against the fake during tests.
func (f *FakeStore) IsSupported() bool { return true }
