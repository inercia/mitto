//go:build darwin

package secrets

import (
	"testing"
)

// testServiceName is a unique service name for tests to avoid conflicts
const testServiceName = "Mitto-Test-SecretStore"

func TestKeychainStore_IsSupported(t *testing.T) {
	store := &KeychainStore{}
	if !store.IsSupported() {
		t.Error("KeychainStore.IsSupported() = false, want true on macOS")
	}
}

func TestKeychainStore_SetGetDelete(t *testing.T) {
	store := &KeychainStore{}
	account := "test-account"
	password := "test-password-123"

	// Clean up before test (ignore errors)
	_ = store.Delete(testServiceName, account)

	// Test Set
	err := store.Set(testServiceName, account, password)
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	// Test Get
	got, err := store.Get(testServiceName, account)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got != password {
		t.Errorf("Get() = %q, want %q", got, password)
	}

	// Test Delete
	err = store.Delete(testServiceName, account)
	if err != nil {
		t.Errorf("Delete() error = %v", err)
	}

	// Verify deleted
	_, err = store.Get(testServiceName, account)
	if err != ErrNotFound {
		t.Errorf("Get() after Delete() error = %v, want %v", err, ErrNotFound)
	}
}

func TestKeychainStore_GetNotFound(t *testing.T) {
	store := &KeychainStore{}
	_, err := store.Get(testServiceName, "nonexistent-account")
	if err != ErrNotFound {
		t.Errorf("Get() error = %v, want %v", err, ErrNotFound)
	}
}

func TestKeychainStore_DeleteNotFound(t *testing.T) {
	store := &KeychainStore{}
	err := store.Delete(testServiceName, "nonexistent-account")
	if err != ErrNotFound {
		t.Errorf("Delete() error = %v, want %v", err, ErrNotFound)
	}
}

func TestKeychainStore_UpdateExisting(t *testing.T) {
	store := &KeychainStore{}
	account := "test-update-account"
	password1 := "password-v1"
	password2 := "password-v2"

	// Clean up before test (ignore errors)
	_ = store.Delete(testServiceName, account)

	// Set initial password
	err := store.Set(testServiceName, account, password1)
	if err != nil {
		t.Fatalf("Set() initial error = %v", err)
	}

	// Update password
	err = store.Set(testServiceName, account, password2)
	if err != nil {
		t.Fatalf("Set() update error = %v", err)
	}

	// Verify updated password
	got, err := store.Get(testServiceName, account)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got != password2 {
		t.Errorf("Get() = %q, want %q", got, password2)
	}

	// Clean up
	_ = store.Delete(testServiceName, account)
}

func TestDefaultIsKeychainStore(t *testing.T) {
	store := Default()
	if _, ok := store.(*KeychainStore); !ok {
		t.Errorf("Default() returned %T, want *KeychainStore on macOS", store)
	}
}

func TestIsSupportedOnMacOS(t *testing.T) {
	if !IsSupported() {
		t.Error("IsSupported() = false, want true on macOS")
	}
}

func TestPackageLevelFunctions(t *testing.T) {
	account := "test-package-level"
	password := "test-pkg-password"

	// Clean up before test
	_ = Delete(testServiceName, account)

	// Test package-level functions
	err := Set(testServiceName, account, password)
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	got, err := Get(testServiceName, account)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got != password {
		t.Errorf("Get() = %q, want %q", got, password)
	}

	err = Delete(testServiceName, account)
	if err != nil {
		t.Errorf("Delete() error = %v", err)
	}
}
