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

