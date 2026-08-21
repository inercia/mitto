package secrets

import (
	"testing"
)

func TestNoopStore_Get(t *testing.T) {
	store := &NoopStore{}
	_, err := store.Get("service", "account")
	if err != ErrNotSupported {
		t.Errorf("NoopStore.Get() error = %v, want %v", err, ErrNotSupported)
	}
}

func TestNoopStore_Set(t *testing.T) {
	store := &NoopStore{}
	err := store.Set("service", "account", "password")
	if err != ErrNotSupported {
		t.Errorf("NoopStore.Set() error = %v, want %v", err, ErrNotSupported)
	}
}

func TestNoopStore_Delete(t *testing.T) {
	store := &NoopStore{}
	err := store.Delete("service", "account")
	if err != ErrNotSupported {
		t.Errorf("NoopStore.Delete() error = %v, want %v", err, ErrNotSupported)
	}
}

func TestNoopStore_IsSupported(t *testing.T) {
	store := &NoopStore{}
	if store.IsSupported() {
		t.Error("NoopStore.IsSupported() = true, want false")
	}
}

func TestDefault(t *testing.T) {
	// Default should always return a non-nil store
	store := Default()
	if store == nil {
		t.Error("Default() returned nil store")
	}
}

func TestConstants(t *testing.T) {
	// Verify constants are as expected
	if ServiceName != "Mitto" {
		t.Errorf("ServiceName = %q, want %q", ServiceName, "Mitto")
	}
	if AccountExternalAccess != "external-access" {
		t.Errorf("AccountExternalAccess = %q, want %q", AccountExternalAccess, "external-access")
	}
	if AccountSharedToken != "shared-token" {
		t.Errorf("AccountSharedToken = %q, want %q", AccountSharedToken, "shared-token")
	}
}

// TestNoopStore_SharedTokenWrappers verifies the GetSharedToken/SetSharedToken/
// DeleteSharedToken package-level convenience wrappers (mitto-7gta.26) delegate
// to the same account/service pair on an unsupported-platform (NoopStore)
// backend, without touching any real credential store.
func TestNoopStore_SharedTokenWrappers(t *testing.T) {
	t.Cleanup(SetStoreForTest(&NoopStore{}))

	if _, err := GetSharedToken(); err != ErrNotSupported {
		t.Errorf("GetSharedToken() error = %v, want %v", err, ErrNotSupported)
	}
	if err := SetSharedToken("tok"); err != ErrNotSupported {
		t.Errorf("SetSharedToken() error = %v, want %v", err, ErrNotSupported)
	}
	if err := DeleteSharedToken(); err != ErrNotSupported {
		t.Errorf("DeleteSharedToken() error = %v, want %v", err, ErrNotSupported)
	}
}

func TestErrors(t *testing.T) {
	// Verify error messages
	if ErrNotFound.Error() != "credential not found" {
		t.Errorf("ErrNotFound.Error() = %q, want %q", ErrNotFound.Error(), "credential not found")
	}
	if ErrNotSupported.Error() != "secret store not supported on this platform" {
		t.Errorf("ErrNotSupported.Error() = %q, want %q", ErrNotSupported.Error(), "secret store not supported on this platform")
	}
}
