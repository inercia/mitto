//go:build darwin

package secrets

import (
	"testing"
)

func TestKeychainStore_IsSupported(t *testing.T) {
	store := &KeychainStore{}
	if !store.IsSupported() {
		t.Error("KeychainStore.IsSupported() = false, want true on macOS")
	}
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

func TestPackageLevelFunctionsUseInjectedStore(t *testing.T) {
	fake := NewFakeStore()
	t.Cleanup(SetStoreForTest(fake))
	const service, account, password = "test-service", "test-account", "test-password"

	err := Set(service, account, password)
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	got, err := Get(service, account)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got != password {
		t.Errorf("Get() = %q, want %q", got, password)
	}

	err = Delete(service, account)
	if err != nil {
		t.Errorf("Delete() error = %v", err)
	}
}
