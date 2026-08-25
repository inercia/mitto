package middleware

import (
	"bytes"
	"testing"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/inercia/mitto/internal/appdir"
)

// newTestPasskeyStore isolates each test in its own MITTO_DIR, mirroring the
// pattern used by TestAuthManager_UpdateConfig_RevokesSessions et al. in
// auth_test.go, so concurrent tests never share webauthn_credentials.json.
func newTestPasskeyStore(t *testing.T) *PasskeyStore {
	t.Helper()
	t.Setenv(appdir.MittoDirEnv, t.TempDir())
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)
	return NewPasskeyStore()
}

func TestNewPasskeyStore_FreshInstall(t *testing.T) {
	s := newTestPasskeyStore(t)
	if got := s.List(); len(got) != 0 {
		t.Fatalf("List() on fresh store = %d credentials, want 0", len(got))
	}
}

func TestPasskeyStore_UserHandle_StableAndPersisted(t *testing.T) {
	s := newTestPasskeyStore(t)

	h1, err := s.UserHandle()
	if err != nil {
		t.Fatalf("UserHandle() error = %v", err)
	}
	if len(h1) != webauthnUserHandleBytes {
		t.Fatalf("UserHandle() len = %d, want %d", len(h1), webauthnUserHandleBytes)
	}

	// Calling again on the same store must return the identical handle.
	h2, err := s.UserHandle()
	if err != nil {
		t.Fatalf("UserHandle() second call error = %v", err)
	}
	if !bytes.Equal(h1, h2) {
		t.Fatalf("UserHandle() not stable within a store: %x != %x", h1, h2)
	}

	// A fresh PasskeyStore reading the same MITTO_DIR must load the same handle
	// (round-trip through webauthn_credentials.json).
	s2 := NewPasskeyStore()
	h3, err := s2.UserHandle()
	if err != nil {
		t.Fatalf("UserHandle() on reloaded store error = %v", err)
	}
	if !bytes.Equal(h1, h3) {
		t.Fatalf("UserHandle() not persisted across reload: %x != %x", h1, h3)
	}
}

func TestPasskeyStore_AddGetListDelete(t *testing.T) {
	s := newTestPasskeyStore(t)

	credA := webauthn.Credential{ID: []byte("cred-a"), PublicKey: []byte("pub-a")}
	credB := webauthn.Credential{ID: []byte("cred-b"), PublicKey: []byte("pub-b")}

	s.Add(credA)
	s.Add(credB)

	if got := s.List(); len(got) != 2 {
		t.Fatalf("List() after adding 2 credentials = %d, want 2", len(got))
	}

	got, ok := s.GetByCredentialID([]byte("cred-a"))
	if !ok {
		t.Fatalf("GetByCredentialID(cred-a) not found")
	}
	if !bytes.Equal(got.Credential.PublicKey, []byte("pub-a")) {
		t.Fatalf("GetByCredentialID(cred-a).PublicKey = %q, want %q", got.Credential.PublicKey, "pub-a")
	}
	if got.CreatedAt.IsZero() || got.LastUsedAt.IsZero() {
		t.Fatalf("GetByCredentialID(cred-a) missing timestamps: %+v", got)
	}

	if _, ok := s.GetByCredentialID([]byte("does-not-exist")); ok {
		t.Fatalf("GetByCredentialID(does-not-exist) unexpectedly found")
	}

	// Persistence round-trip: a fresh store reading the same file sees both.
	s2 := NewPasskeyStore()
	if got := s2.List(); len(got) != 2 {
		t.Fatalf("List() on reloaded store = %d, want 2", len(got))
	}

	if !s.Delete([]byte("cred-a")) {
		t.Fatalf("Delete(cred-a) = false, want true")
	}
	if got := s.List(); len(got) != 1 {
		t.Fatalf("List() after Delete(cred-a) = %d, want 1", len(got))
	}
	if _, ok := s.GetByCredentialID([]byte("cred-a")); ok {
		t.Fatalf("GetByCredentialID(cred-a) found after delete")
	}

	if s.Delete([]byte("cred-a")) {
		t.Fatalf("Delete(cred-a) on already-deleted credential = true, want false")
	}
	if s.Delete([]byte("does-not-exist")) {
		t.Fatalf("Delete(does-not-exist) = true, want false")
	}
}

func TestPasskeyStore_Add_ReplacesExistingByID_PreservesCreatedAt(t *testing.T) {
	s := newTestPasskeyStore(t)

	cred := webauthn.Credential{ID: []byte("cred-a"), PublicKey: []byte("pub-1")}
	s.Add(cred)
	first, ok := s.GetByCredentialID([]byte("cred-a"))
	if !ok {
		t.Fatalf("GetByCredentialID(cred-a) not found after first Add")
	}

	updated := webauthn.Credential{ID: []byte("cred-a"), PublicKey: []byte("pub-2")}
	s.Add(updated)

	if got := s.List(); len(got) != 1 {
		t.Fatalf("List() after replacing by ID = %d, want 1 (no duplicate)", len(got))
	}

	second, ok := s.GetByCredentialID([]byte("cred-a"))
	if !ok {
		t.Fatalf("GetByCredentialID(cred-a) not found after replacing Add")
	}
	if !bytes.Equal(second.Credential.PublicKey, []byte("pub-2")) {
		t.Fatalf("PublicKey after replace = %q, want %q", second.Credential.PublicKey, "pub-2")
	}
	if !second.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("CreatedAt not preserved across replace: %v != %v", second.CreatedAt, first.CreatedAt)
	}
}

func TestPasskeyStore_UpdateSignCount(t *testing.T) {
	s := newTestPasskeyStore(t)

	cred := webauthn.Credential{ID: []byte("cred-a"), PublicKey: []byte("pub-a")}
	s.Add(cred)

	if !s.UpdateSignCount([]byte("cred-a"), 42) {
		t.Fatalf("UpdateSignCount(cred-a) = false, want true")
	}
	got, ok := s.GetByCredentialID([]byte("cred-a"))
	if !ok {
		t.Fatalf("GetByCredentialID(cred-a) not found")
	}
	if got.Credential.Authenticator.SignCount != 42 {
		t.Fatalf("SignCount = %d, want 42", got.Credential.Authenticator.SignCount)
	}

	if s.UpdateSignCount([]byte("does-not-exist"), 1) {
		t.Fatalf("UpdateSignCount(does-not-exist) = true, want false")
	}

	// Persisted across reload too.
	s2 := NewPasskeyStore()
	got2, ok := s2.GetByCredentialID([]byte("cred-a"))
	if !ok || got2.Credential.Authenticator.SignCount != 42 {
		t.Fatalf("SignCount not persisted: ok=%v got=%+v", ok, got2)
	}
}

func TestPasskeyStore_User(t *testing.T) {
	s := newTestPasskeyStore(t)

	cred := webauthn.Credential{ID: []byte("cred-a"), PublicKey: []byte("pub-a")}
	s.Add(cred)

	u, err := s.User("alice")
	if err != nil {
		t.Fatalf("User() error = %v", err)
	}

	handle, err := s.UserHandle()
	if err != nil {
		t.Fatalf("UserHandle() error = %v", err)
	}
	if !bytes.Equal(u.WebAuthnID(), handle) {
		t.Fatalf("WebAuthnID() = %x, want %x", u.WebAuthnID(), handle)
	}
	if u.WebAuthnName() != "alice" {
		t.Fatalf("WebAuthnName() = %q, want %q", u.WebAuthnName(), "alice")
	}
	if u.WebAuthnDisplayName() != "alice" {
		t.Fatalf("WebAuthnDisplayName() = %q, want %q", u.WebAuthnDisplayName(), "alice")
	}

	creds := u.WebAuthnCredentials()
	if len(creds) != 1 || !bytes.Equal(creds[0].ID, []byte("cred-a")) {
		t.Fatalf("WebAuthnCredentials() = %+v, want one credential with ID cred-a", creds)
	}
}

func TestPasskeyStore_List_ReturnsCopyNotAlias(t *testing.T) {
	s := newTestPasskeyStore(t)
	s.Add(webauthn.Credential{ID: []byte("cred-a")})

	got := s.List()
	got[0].Credential.ID = []byte("mutated")

	got2, ok := s.GetByCredentialID([]byte("cred-a"))
	if !ok {
		t.Fatalf("GetByCredentialID(cred-a) not found after mutating a List() copy; internal state was aliased")
	}
	if !bytes.Equal(got2.Credential.ID, []byte("cred-a")) {
		t.Fatalf("internal credential mutated via List() copy: %q", got2.Credential.ID)
	}
}
