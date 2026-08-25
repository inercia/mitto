package middleware

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config"
)

// newTestAuthManagerWithPasskeys sets up an AuthManager with Simple auth and
// directly wires webAuthn/passkeyStore (bypassing ConfigurePasskey, which
// requires a derivable https external address) into an isolated MITTO_DIR
// per-test, mirroring newTestPasskeyStore in passkey_store_test.go.
func newTestAuthManagerWithPasskeys(t *testing.T, rpID string, rpOrigins []string) *AuthManager {
	t.Helper()
	t.Setenv(appdir.MittoDirEnv, t.TempDir())
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	am := NewAuthManager(&config.WebAuth{
		Simple: &config.SimpleAuth{Username: "admin", Password: "password"},
	})
	t.Cleanup(am.Close)

	wa, err := webauthn.New(&webauthn.Config{
		RPID:          rpID,
		RPDisplayName: "Mitto Test",
		RPOrigins:     rpOrigins,
	})
	if err != nil {
		t.Fatalf("webauthn.New() error = %v", err)
	}
	am.webAuthn = wa
	am.passkeyStore = NewPasskeyStore()
	return am
}

func sessionCookieFor(session *AuthSession) *http.Cookie {
	return &http.Cookie{Name: sessionCookieName, Value: session.Token}
}

func TestHandleRegisterBegin_Disabled404(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{Simple: &config.SimpleAuth{Username: "admin", Password: "password"}})
	defer am.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/webauthn/register/begin", nil)
	w := httptest.NewRecorder()
	am.HandleRegisterBegin(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (passkeys not configured)", w.Code, http.StatusNotFound)
	}
}

func TestHandleRegisterBegin_Unauthenticated401(t *testing.T) {
	am := newTestAuthManagerWithPasskeys(t, "example.org", []string{"https://example.org"})

	req := httptest.NewRequest(http.MethodPost, "/api/webauthn/register/begin", nil)
	w := httptest.NewRecorder()
	am.HandleRegisterBegin(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestHandleRegisterBegin_WrongMethod405(t *testing.T) {
	am := newTestAuthManagerWithPasskeys(t, "example.org", []string{"https://example.org"})

	req := httptest.NewRequest(http.MethodGet, "/api/webauthn/register/begin", nil)
	w := httptest.NewRecorder()
	am.HandleRegisterBegin(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleRegisterBegin_Success(t *testing.T) {
	am := newTestAuthManagerWithPasskeys(t, "example.org", []string{"https://example.org"})

	session, err := am.CreateSession("admin")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/webauthn/register/begin", nil)
	req.AddCookie(sessionCookieFor(session))
	w := httptest.NewRecorder()
	am.HandleRegisterBegin(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var creation protocol.CredentialCreation
	if err := json.Unmarshal(w.Body.Bytes(), &creation); err != nil {
		t.Fatalf("failed to decode CredentialCreation: %v; body=%s", err, w.Body.String())
	}
	if creation.Response.RelyingParty.ID != "example.org" {
		t.Errorf("RelyingParty.ID = %q, want %q", creation.Response.RelyingParty.ID, "example.org")
	}
	if len(creation.Response.Challenge) == 0 {
		t.Error("Challenge is empty")
	}
	if creation.Response.AuthenticatorSelection.ResidentKey != protocol.ResidentKeyRequirementRequired {
		t.Errorf("ResidentKey = %q, want %q", creation.Response.AuthenticatorSelection.ResidentKey, protocol.ResidentKeyRequirementRequired)
	}
	if creation.Response.AuthenticatorSelection.UserVerification != protocol.VerificationPreferred {
		t.Errorf("UserVerification = %q, want %q", creation.Response.AuthenticatorSelection.UserVerification, protocol.VerificationPreferred)
	}

	// The ceremony must be stashed under the session token for HandleRegisterFinish.
	am.pendingRegMu.Lock()
	_, stashed := am.pendingRegistrations[session.Token]
	am.pendingRegMu.Unlock()
	if !stashed {
		t.Error("HandleRegisterBegin did not stash SessionData for the session token")
	}
}

func TestHandleRegisterFinish_Disabled404(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{Simple: &config.SimpleAuth{Username: "admin", Password: "password"}})
	defer am.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/webauthn/register/finish", nil)
	w := httptest.NewRecorder()
	am.HandleRegisterFinish(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleRegisterFinish_Unauthenticated401(t *testing.T) {
	am := newTestAuthManagerWithPasskeys(t, "example.org", []string{"https://example.org"})

	req := httptest.NewRequest(http.MethodPost, "/api/webauthn/register/finish", nil)
	w := httptest.NewRecorder()
	am.HandleRegisterFinish(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestHandleRegisterFinish_WrongMethod405(t *testing.T) {
	am := newTestAuthManagerWithPasskeys(t, "example.org", []string{"https://example.org"})

	req := httptest.NewRequest(http.MethodGet, "/api/webauthn/register/finish", nil)
	w := httptest.NewRecorder()
	am.HandleRegisterFinish(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

// TestHandleRegisterFinish_MissingOrExpiredStash400 verifies the 400 path
// when no HandleRegisterBegin ceremony was ever started for this session
// (also covers the "stash expired" case, since popPendingRegistration
// treats "expired" identically to "absent").
func TestHandleRegisterFinish_MissingOrExpiredStash400(t *testing.T) {
	am := newTestAuthManagerWithPasskeys(t, "example.org", []string{"https://example.org"})

	session, err := am.CreateSession("admin")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/webauthn/register/finish", bytes.NewReader([]byte(`{}`)))
	req.AddCookie(sessionCookieFor(session))
	w := httptest.NewRecorder()
	am.HandleRegisterFinish(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}

	var resp RegisterResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode RegisterResponse: %v", err)
	}
	if resp.Success {
		t.Error("Success = true, want false")
	}
}

// TestHandleRegisterFinish_ExpiredStashIsTreatedAsMissing verifies that a
// stash entry past its TTL is rejected with 400 exactly like a missing one,
// and is removed by the pop (no reuse-after-expiry).
func TestHandleRegisterFinish_ExpiredStashIsTreatedAsMissing(t *testing.T) {
	am := newTestAuthManagerWithPasskeys(t, "example.org", []string{"https://example.org"})

	session, err := am.CreateSession("admin")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	am.pendingRegMu.Lock()
	am.pendingRegistrations[session.Token] = &pendingRegistration{
		session: &webauthn.SessionData{Challenge: "stale-challenge"},
		expires: time.Now().Add(-time.Minute), // already expired
	}
	am.pendingRegMu.Unlock()

	req := httptest.NewRequest(http.MethodPost, "/api/webauthn/register/finish", bytes.NewReader([]byte(`{}`)))
	req.AddCookie(sessionCookieFor(session))
	w := httptest.NewRecorder()
	am.HandleRegisterFinish(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}

	am.pendingRegMu.Lock()
	_, stillPresent := am.pendingRegistrations[session.Token]
	am.pendingRegMu.Unlock()
	if stillPresent {
		t.Error("expired pending registration was not removed by popPendingRegistration")
	}
}

// TestHandleRegisterFinish_Success uses the W3C WebAuthn spec's "none/ES256"
// registration test vector (also used by go-webauthn's own
// TestFinishRegistration_Success) to drive a full, successful ceremony
// through the real handler: a matching SessionData is stashed directly
// (bypassing the random challenge from a real BeginRegistration call, since
// the vector's clientDataJSON is signed over its own fixed challenge), then
// HandleRegisterFinish parses+verifies the attestation and persists the
// resulting credential to the PasskeyStore.
func TestHandleRegisterFinish_Success(t *testing.T) {
	am := newTestAuthManagerWithPasskeys(t, "example.org", []string{"https://example.org"})

	session, err := am.CreateSession("admin")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	handle, err := am.passkeyStore.UserHandle()
	if err != nil {
		t.Fatalf("UserHandle() error = %v", err)
	}

	body, challenge, credentialID := webauthnSpecVectorNoneES256(t)

	am.storePendingRegistration(session.Token, &webauthn.SessionData{
		Challenge:  challenge,
		UserID:     handle,
		CredParams: []protocol.CredentialParameter{{Type: protocol.PublicKeyCredentialType, Algorithm: webauthncose.AlgES256}},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/webauthn/register/finish", bytes.NewReader(body))
	req.AddCookie(sessionCookieFor(session))
	w := httptest.NewRecorder()
	am.HandleRegisterFinish(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp RegisterResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode RegisterResponse: %v", err)
	}
	if !resp.Success {
		t.Errorf("Success = false, want true (error=%q)", resp.Error)
	}

	stored, ok := am.passkeyStore.GetByCredentialID(credentialID)
	if !ok {
		t.Fatal("credential was not persisted to the PasskeyStore")
	}
	if stored.CreatedAt.IsZero() {
		t.Error("persisted credential has zero CreatedAt")
	}

	// The stash must have been consumed (single-use ceremony).
	am.pendingRegMu.Lock()
	_, stillPresent := am.pendingRegistrations[session.Token]
	am.pendingRegMu.Unlock()
	if stillPresent {
		t.Error("pending registration was not popped after a successful finish")
	}
}

// webauthnSpecVectorNoneES256 returns the W3C WebAuthn spec's "none/ES256"
// registration test vector body, matching challenge, and credential ID.
// Mirrors testRegistrationSpecVectorNoneES256 in go-webauthn's own
// webauthn/registration_test.go (unexported there, so duplicated here).
// See: https://www.w3.org/TR/webauthn-3/#sctn-test-vectors-none-es256
func webauthnSpecVectorNoneES256(t *testing.T) (body []byte, challenge string, credentialID []byte) {
	t.Helper()

	const (
		attestationObjectHex = "a363666d74646e6f6e656761747453746d74a068617574684461746158a4bfabc37432958b063360d3ad6461c9c4735ae7f8edd46592a5e0f01452b2e4b559000000008446ccb9ab1db374750b2367ff6f3a1f0020f91f391db4c9b2fde0ea70189cba3fb63f579ba6122b33ad94ff3ec330084be4a5010203262001215820afefa16f97ca9b2d23eb86ccb64098d20db90856062eb249c33a9b672f26df61225820930a56b87a2fca66334b03458abf879717c12cc68ed73290af2e2664796b9220"
		clientDataJSONHex    = "7b2274797065223a22776562617574686e2e637265617465222c226368616c6c656e6765223a22414d4d507434557878475453746e63647134313759447742466938767049612d7077386f4f755657345441222c226f726967696e223a2268747470733a2f2f6578616d706c652e6f7267222c2263726f73734f726967696e223a66616c73652c22657874726144617461223a22636c69656e74446174614a534f4e206d617920626520657874656e6465642077697468206164646974696f6e616c206669656c647320696e20746865206675747572652c207375636820617320746869733a20426b5165446a646354427258426941774a544c453551227d"
		credentialIDHex      = "f91f391db4c9b2fde0ea70189cba3fb63f579ba6122b33ad94ff3ec330084be4"
		challengeHex         = "00c30fb78531c464d2b6771dab8d7b603c01162f2fa486bea70f283ae556e130"
	)

	credentialID, err := hex.DecodeString(credentialIDHex)
	if err != nil {
		t.Fatalf("hex.DecodeString(credentialID) error = %v", err)
	}

	decode := func(s string) []byte {
		b, err := hex.DecodeString(s)
		if err != nil {
			t.Fatalf("hex.DecodeString() error = %v", err)
		}
		return b
	}

	challenge = base64.RawURLEncoding.EncodeToString(decode(challengeHex))
	id := base64.RawURLEncoding.EncodeToString(credentialID)
	attObj := base64.RawURLEncoding.EncodeToString(decode(attestationObjectHex))
	cdj := base64.RawURLEncoding.EncodeToString(decode(clientDataJSONHex))

	response := map[string]any{
		"id":    id,
		"rawId": id,
		"type":  "public-key",
		"response": map[string]any{
			"attestationObject": attObj,
			"clientDataJSON":    cdj,
		},
	}

	body, err = json.Marshal(response)
	if err != nil {
		t.Fatalf("json.Marshal(response) error = %v", err)
	}
	return body, challenge, credentialID
}
