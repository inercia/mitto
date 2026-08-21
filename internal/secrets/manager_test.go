package secrets

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
)

type memoryBlobBackend struct {
	mu      sync.Mutex
	data    []byte
	loads   int
	saves   int
	loadErr error
	saveErr error
}

func (b *memoryBlobBackend) Load() ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.loads++
	if b.loadErr != nil {
		return nil, b.loadErr
	}
	if b.data == nil {
		return nil, ErrNotFound
	}
	return append([]byte(nil), b.data...), nil
}

func (b *memoryBlobBackend) Save(data []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.saves++
	if b.saveErr != nil {
		return b.saveErr
	}
	b.data = append([]byte(nil), data...)
	return nil
}

func (*memoryBlobBackend) IsSupported() bool { return true }

func TestManagerScopesStatusDeleteAndLazyCache(t *testing.T) {
	backend := &memoryBlobBackend{}
	manager := NewManager(backend)
	refs := []CredentialRef{
		GlobalCredential("shared-token"),
		SlackAppCredential("app-one", "app-token"),
		SlackInstallationCredential("team-one", "bot-token"),
	}
	for i, ref := range refs {
		if err := manager.Put(ref, fmt.Sprintf("secret-%d", i)); err != nil {
			t.Fatalf("Put(%+v) error = %v", ref, err)
		}
	}
	for i, ref := range refs {
		got, err := manager.Resolve(ref)
		if err != nil || got != fmt.Sprintf("secret-%d", i) {
			t.Fatalf("Resolve(%+v) = (%q, %v)", ref, got, err)
		}
	}
	status, err := manager.Status(refs[0])
	if err != nil || !status.Configured {
		t.Fatalf("Status() = (%+v, %v), want configured", status, err)
	}
	encoded, err := json.Marshal(status)
	if err != nil || string(encoded) != `{"configured":true}` {
		t.Fatalf("status JSON = %s, %v", encoded, err)
	}
	if err := manager.Delete(refs[1]); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := manager.Resolve(refs[1]); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve(deleted) error = %v, want ErrNotFound", err)
	}
	if got, _ := manager.Resolve(refs[2]); got != "secret-2" {
		t.Fatalf("unrelated credential = %q, want secret-2", got)
	}
	if backend.loads != 1 {
		t.Fatalf("backend loads = %d, want one process-lifetime lazy load", backend.loads)
	}
}

func TestManagerConcurrentOperations(t *testing.T) {
	manager := NewManager(&memoryBlobBackend{})
	const count = 64
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ref := SlackInstallationCredential("team", fmt.Sprintf("token-%d", i))
			want := fmt.Sprintf("value-%d", i)
			if err := manager.Put(ref, want); err != nil {
				t.Errorf("Put(%d) error = %v", i, err)
				return
			}
			if got, err := manager.Resolve(ref); err != nil || got != want {
				t.Errorf("Resolve(%d) = (%q, %v)", i, got, err)
			}
			if status, err := manager.Status(ref); err != nil || !status.Configured {
				t.Errorf("Status(%d) = (%+v, %v)", i, status, err)
			}
			if err := manager.Delete(ref); err != nil {
				t.Errorf("Delete(%d) error = %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	for i := 0; i < count; i++ {
		ref := SlackInstallationCredential("team", fmt.Sprintf("token-%d", i))
		if _, err := manager.Resolve(ref); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Resolve deleted credential %d error = %v, want ErrNotFound", i, err)
		}
	}
}

func TestManagerFailedSavePreservesCachedVault(t *testing.T) {
	backend := &memoryBlobBackend{}
	manager := NewManager(backend)
	ref := GlobalCredential("token")
	if err := manager.Put(ref, "old"); err != nil {
		t.Fatal(err)
	}
	backend.saveErr = errors.New("save failed")
	if err := manager.Put(ref, "new"); err == nil {
		t.Fatal("Put() error = nil, want save failure")
	}
	if got, err := manager.Resolve(ref); err != nil || got != "old" {
		t.Fatalf("Resolve() = (%q, %v), want old", got, err)
	}
}

func TestManagerRejectsCorruptAndUnsupportedVaults(t *testing.T) {
	tests := []struct {
		name string
		data string
		want error
	}{
		{"invalid JSON", `{`, ErrCorruptVault},
		{"unknown field", `{"version":1,"namespaces":{},"extra":true}`, ErrCorruptVault},
		{"trailing data", `{"version":1,"namespaces":{}} {}`, ErrCorruptVault},
		{"missing namespaces", `{"version":1}`, ErrCorruptVault},
		{"wrong version", `{"version":2,"namespaces":{}}`, ErrUnsupportedVaultVersion},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := NewManager(&memoryBlobBackend{data: []byte(test.data)})
			_, err := manager.Resolve(GlobalCredential("token"))
			if !errors.Is(err, test.want) {
				t.Fatalf("Resolve() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestManagerCachesLoadFailure(t *testing.T) {
	backend := &memoryBlobBackend{loadErr: errors.New("unavailable")}
	manager := NewManager(backend)
	for i := 0; i < 2; i++ {
		if _, err := manager.Resolve(GlobalCredential("token")); err == nil {
			t.Fatal("Resolve() error = nil")
		}
	}
	if backend.loads != 1 {
		t.Fatalf("backend loads = %d, want 1", backend.loads)
	}
}

func TestManagerValidatesCredentialReferences(t *testing.T) {
	manager := NewManager(&memoryBlobBackend{})
	invalid := []CredentialRef{
		GlobalCredential(""),
		{Namespace: NamespaceGlobal, OwnerID: "owner", Name: "token"},
		SlackAppCredential("bad/owner", "token"),
		{Namespace: "other", Name: "token"},
	}
	for _, ref := range invalid {
		if err := manager.Put(ref, "value"); !errors.Is(err, ErrInvalidCredential) {
			t.Errorf("Put(%+v) error = %v, want ErrInvalidCredential", ref, err)
		}
	}
	if err := manager.Put(GlobalCredential("token"), ""); !errors.Is(err, ErrInvalidCredential) {
		t.Errorf("Put(empty value) error = %v, want ErrInvalidCredential", err)
	}
}

type migrationStore struct {
	*FakeStore
	failVaultSave   bool
	verifyErr       error
	verifyOverride  string
	vaultWritten    bool
	legacyDeleteErr error
}

func (s *migrationStore) Get(service, account string) (string, error) {
	if account == AccountCredentialVault && s.vaultWritten {
		if s.verifyErr != nil {
			return "", s.verifyErr
		}
		if s.verifyOverride != "" {
			return s.verifyOverride, nil
		}
	}
	return s.FakeStore.Get(service, account)
}

func (s *migrationStore) Set(service, account, password string) error {
	if account == AccountCredentialVault {
		if s.failVaultSave {
			return errors.New("vault save failed")
		}
		s.vaultWritten = true
	}
	return s.FakeStore.Set(service, account, password)
}

func (s *migrationStore) Delete(service, account string) error {
	if account != AccountCredentialVault && s.legacyDeleteErr != nil {
		return s.legacyDeleteErr
	}
	return s.FakeStore.Delete(service, account)
}

func TestLegacyCredentialMigrationRequiresVerifiedPersistence(t *testing.T) {
	tests := []struct {
		name        string
		configure   func(*migrationStore)
		legacyStays bool
	}{
		{"success", func(*migrationStore) {}, false},
		{"save failure", func(s *migrationStore) { s.failVaultSave = true }, true},
		{"verify read failure", func(s *migrationStore) { s.verifyErr = errors.New("read failed") }, true},
		{"verify mismatch", func(s *migrationStore) {
			s.verifyOverride = `{"version":1,"namespaces":{"global":{"external-access":"different"}}}`
		}, true},
		{"cleanup failure", func(s *migrationStore) { s.legacyDeleteErr = errors.New("delete failed") }, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &migrationStore{FakeStore: NewFakeStore()}
			test.configure(store)
			if err := store.FakeStore.Set(ServiceName, AccountExternalAccess, "legacy-secret"); err != nil {
				t.Fatal(err)
			}
			restore := SetStoreForTest(store)
			defer restore()
			got, err := GetExternalAccessPassword()
			if err != nil || got != "legacy-secret" {
				t.Fatalf("GetExternalAccessPassword() = (%q, %v)", got, err)
			}
			_, legacyErr := store.FakeStore.Get(ServiceName, AccountExternalAccess)
			if test.legacyStays && legacyErr != nil {
				t.Fatal("legacy credential was deleted before verified persistence")
			}
			if !test.legacyStays && !errors.Is(legacyErr, ErrNotFound) {
				t.Fatalf("legacy credential error = %v, want ErrNotFound", legacyErr)
			}
		})
	}
}

func TestLegacyCleanupRetriesAfterManagerRestart(t *testing.T) {
	store := &migrationStore{
		FakeStore:       NewFakeStore(),
		legacyDeleteErr: errors.New("delete failed"),
	}
	if err := store.FakeStore.Set(ServiceName, AccountExternalAccess, "legacy-secret"); err != nil {
		t.Fatal(err)
	}

	restore := SetStoreForTest(store)
	if got, err := GetExternalAccessPassword(); err != nil || got != "legacy-secret" {
		t.Fatalf("initial migration = (%q, %v)", got, err)
	}
	restore()
	if _, err := store.FakeStore.Get(ServiceName, AccountExternalAccess); err != nil {
		t.Fatal("legacy credential should remain after cleanup failure")
	}

	store.legacyDeleteErr = nil
	restore = SetStoreForTest(store) // A new manager simulates process restart.
	defer restore()
	if got, err := GetExternalAccessPassword(); err != nil || got != "legacy-secret" {
		t.Fatalf("post-restart resolution = (%q, %v)", got, err)
	}
	if _, err := store.FakeStore.Get(ServiceName, AccountExternalAccess); !errors.Is(err, ErrNotFound) {
		t.Fatalf("legacy credential error = %v, want cleanup after restart", err)
	}
}
