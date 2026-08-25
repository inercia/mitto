package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/inercia/mitto/internal/config"
)

// Tests for HandleListCredentials/HandleDeleteCredential (mitto-4mz.6), the
// Settings "Manage passkeys" list/delete endpoints over the existing
// PasskeyStore.List()/Delete(). Reuses newTestAuthManagerWithPasskeys and
// sessionCookieFor from webauthn_register_test.go.

func TestHandleListCredentials_Disabled404(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{Simple: &config.SimpleAuth{Username: "admin", Password: "password"}})
	defer am.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/webauthn/register/list", nil)
	w := httptest.NewRecorder()
	am.HandleListCredentials(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleListCredentials_Unauthenticated401(t *testing.T) {
	am := newTestAuthManagerWithPasskeys(t, "example.org", []string{"https://example.org"})

	req := httptest.NewRequest(http.MethodGet, "/api/webauthn/register/list", nil)
	w := httptest.NewRecorder()
	am.HandleListCredentials(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestHandleListCredentials_WrongMethod405(t *testing.T) {
	am := newTestAuthManagerWithPasskeys(t, "example.org", []string{"https://example.org"})

	req := httptest.NewRequest(http.MethodPost, "/api/webauthn/register/list", nil)
	w := httptest.NewRecorder()
	am.HandleListCredentials(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleListCredentials_ReturnsStoredCreds(t *testing.T) {
	am := newTestAuthManagerWithPasskeys(t, "example.org", []string{"https://example.org"})
	session, err := am.CreateSession("admin")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	am.passkeyStore.Add(webauthn.Credential{ID: []byte{0x01, 0x02, 0x03}})

	req := httptest.NewRequest(http.MethodGet, "/api/webauthn/register/list", nil)
	req.AddCookie(sessionCookieFor(session))
	w := httptest.NewRecorder()
	am.HandleListCredentials(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var out []CredentialInfo
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("failed to decode: %v; body=%s", err, w.Body.String())
	}
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1", len(out))
	}
	if out[0].ID.String() == "" {
		t.Error("ID is empty")
	}
	if out[0].CreatedAt.IsZero() {
		t.Error("CreatedAt is zero")
	}
}

func TestHandleDeleteCredential_Disabled404(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{Simple: &config.SimpleAuth{Username: "admin", Password: "password"}})
	defer am.Close()

	req := httptest.NewRequest(http.MethodDelete, "/api/webauthn/register/abc", nil)
	req.SetPathValue("id", "abc")
	w := httptest.NewRecorder()
	am.HandleDeleteCredential(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleDeleteCredential_Unauthenticated401(t *testing.T) {
	am := newTestAuthManagerWithPasskeys(t, "example.org", []string{"https://example.org"})

	req := httptest.NewRequest(http.MethodDelete, "/api/webauthn/register/abc", nil)
	req.SetPathValue("id", "abc")
	w := httptest.NewRecorder()
	am.HandleDeleteCredential(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestHandleDeleteCredential_WrongMethod405(t *testing.T) {
	am := newTestAuthManagerWithPasskeys(t, "example.org", []string{"https://example.org"})

	req := httptest.NewRequest(http.MethodGet, "/api/webauthn/register/abc", nil)
	req.SetPathValue("id", "abc")
	w := httptest.NewRecorder()
	am.HandleDeleteCredential(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleDeleteCredential_InvalidID400(t *testing.T) {
	am := newTestAuthManagerWithPasskeys(t, "example.org", []string{"https://example.org"})
	session, err := am.CreateSession("admin")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/webauthn/register/not-valid-b64!!", nil)
	req.SetPathValue("id", "not-valid-b64!!")
	req.AddCookie(sessionCookieFor(session))
	w := httptest.NewRecorder()
	am.HandleDeleteCredential(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestHandleDeleteCredential_NotFound404(t *testing.T) {
	am := newTestAuthManagerWithPasskeys(t, "example.org", []string{"https://example.org"})
	session, err := am.CreateSession("admin")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/webauthn/register/AQID", nil)
	req.SetPathValue("id", "AQID") // base64url("\x01\x02\x03"), never registered
	req.AddCookie(sessionCookieFor(session))
	w := httptest.NewRecorder()
	am.HandleDeleteCredential(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusNotFound, w.Body.String())
	}
}

func TestHandleDeleteCredential_Success(t *testing.T) {
	am := newTestAuthManagerWithPasskeys(t, "example.org", []string{"https://example.org"})
	session, err := am.CreateSession("admin")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	credID := []byte{0x01, 0x02, 0x03}
	am.passkeyStore.Add(webauthn.Credential{ID: credID})

	req := httptest.NewRequest(http.MethodDelete, "/api/webauthn/register/AQID", nil)
	req.SetPathValue("id", "AQID") // base64url("\x01\x02\x03")
	req.AddCookie(sessionCookieFor(session))
	w := httptest.NewRecorder()
	am.HandleDeleteCredential(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	if _, ok := am.passkeyStore.GetByCredentialID(credID); ok {
		t.Error("credential was not removed from the PasskeyStore")
	}
}
