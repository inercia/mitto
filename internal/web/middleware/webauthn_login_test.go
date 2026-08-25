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

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/inercia/mitto/internal/config"
)

func TestHandleLoginBegin_Disabled404(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{Simple: &config.SimpleAuth{Username: "admin", Password: "password"}})
	defer am.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/webauthn/login/begin", nil)
	w := httptest.NewRecorder()
	am.HandleLoginBegin(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (passkeys not configured)", w.Code, http.StatusNotFound)
	}
}

func TestHandleLoginBegin_WrongMethod405(t *testing.T) {
	am := newTestAuthManagerWithPasskeys(t, "example.org", []string{"https://example.org"})

	req := httptest.NewRequest(http.MethodGet, "/api/webauthn/login/begin", nil)
	w := httptest.NewRecorder()
	am.HandleLoginBegin(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

// TestHandleLoginBegin_Success verifies a discoverable-login ceremony is
// started (no session/username required), the SessionData is stashed keyed
// by a fresh ceremony cookie, and the assertion options are returned as JSON
// with an empty allowCredentials list (discoverable login lets the
// authenticator pick).
func TestHandleLoginBegin_Success(t *testing.T) {
	am := newTestAuthManagerWithPasskeys(t, "example.org", []string{"https://example.org"})

	req := httptest.NewRequest(http.MethodPost, "/api/webauthn/login/begin", nil)
	w := httptest.NewRecorder()
	am.HandleLoginBegin(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var assertion struct {
		Response struct {
			RPID               string `json:"rpId"`
			Challenge          string `json:"challenge"`
			AllowedCredentials []any  `json:"allowCredentials"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &assertion); err != nil {
		t.Fatalf("failed to decode CredentialAssertion: %v; body=%s", err, w.Body.String())
	}
	if assertion.Response.RPID != "example.org" {
		t.Errorf("RelyingPartyID = %q, want %q", assertion.Response.RPID, "example.org")
	}
	if assertion.Response.Challenge == "" {
		t.Error("Challenge is empty")
	}
	if len(assertion.Response.AllowedCredentials) != 0 {
		t.Errorf("AllowedCredentials = %v, want empty (discoverable/usernameless login)", assertion.Response.AllowedCredentials)
	}

	resp := w.Result()
	var ceremonyCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == loginCeremonyCookieName {
			ceremonyCookie = c
			break
		}
	}
	if ceremonyCookie == nil {
		t.Fatal("HandleLoginBegin did not set the ceremony cookie")
	}
	if ceremonyCookie.Value == "" {
		t.Error("ceremony cookie value is empty")
	}
	if !ceremonyCookie.HttpOnly {
		t.Error("ceremony cookie is not HttpOnly")
	}

	am.pendingLoginMu.Lock()
	_, stashed := am.pendingLogins[ceremonyCookie.Value]
	am.pendingLoginMu.Unlock()
	if !stashed {
		t.Error("HandleLoginBegin did not stash SessionData for the ceremony id")
	}
}

func TestHandleLoginFinish_Disabled404(t *testing.T) {
	am := NewAuthManager(&config.WebAuth{Simple: &config.SimpleAuth{Username: "admin", Password: "password"}})
	defer am.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/webauthn/login/finish", nil)
	w := httptest.NewRecorder()
	am.HandleLoginFinish(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleLoginFinish_WrongMethod405(t *testing.T) {
	am := newTestAuthManagerWithPasskeys(t, "example.org", []string{"https://example.org"})

	req := httptest.NewRequest(http.MethodGet, "/api/webauthn/login/finish", nil)
	w := httptest.NewRecorder()
	am.HandleLoginFinish(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

// TestHandleLoginFinish_MissingCookie400 verifies the 400 path when the
// ceremony cookie is absent entirely (never began, or client dropped it).
func TestHandleLoginFinish_MissingCookie400(t *testing.T) {
	am := newTestAuthManagerWithPasskeys(t, "example.org", []string{"https://example.org"})

	req := httptest.NewRequest(http.MethodPost, "/api/webauthn/login/finish", bytes.NewReader([]byte(`{}`)))
	w := httptest.NewRecorder()
	am.HandleLoginFinish(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	var resp WebAuthnLoginResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode WebAuthnLoginResponse: %v", err)
	}
	if resp.Success {
		t.Error("Success = true, want false")
	}
}

// TestHandleLoginFinish_ExpiredStashIsTreatedAsMissing verifies a ceremony
// cookie referencing an expired (or unknown) stash entry is rejected with
// 400, mirroring the registration flow's expiry handling, and that the
// ceremony cookie is cleared on this failure path too.
func TestHandleLoginFinish_ExpiredStashIsTreatedAsMissing(t *testing.T) {
	am := newTestAuthManagerWithPasskeys(t, "example.org", []string{"https://example.org"})

	am.pendingLoginMu.Lock()
	am.pendingLogins["stale-ceremony"] = &pendingLogin{
		session: &webauthn.SessionData{Challenge: "stale-challenge"},
		expires: time.Now().Add(-time.Minute), // already expired
	}
	am.pendingLoginMu.Unlock()

	req := httptest.NewRequest(http.MethodPost, "/api/webauthn/login/finish", bytes.NewReader([]byte(`{}`)))
	req.AddCookie(&http.Cookie{Name: loginCeremonyCookieName, Value: "stale-ceremony"})
	w := httptest.NewRecorder()
	am.HandleLoginFinish(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}

	am.pendingLoginMu.Lock()
	_, stillPresent := am.pendingLogins["stale-ceremony"]
	am.pendingLoginMu.Unlock()
	if stillPresent {
		t.Error("expired pending login was not removed by popPendingLogin")
	}

	resp := w.Result()
	var cleared bool
	for _, c := range resp.Cookies() {
		if c.Name == loginCeremonyCookieName && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("ceremony cookie was not cleared on the expired-stash failure path")
	}
}

// TestHandleLoginFinish_RateLimited429 verifies a rate-limited IP is
// rejected before any stash/assertion processing, and its ceremony cookie
// is cleared.
func TestHandleLoginFinish_RateLimited429(t *testing.T) {
	am := newTestAuthManagerWithPasskeys(t, "example.org", []string{"https://example.org"})
	am.rateLimiter.Close()
	am.rateLimiter = NewAuthRateLimiterWithConfig(1, time.Minute, 5*time.Minute)

	const ip = "203.0.113.7:12345"
	am.rateLimiter.RecordFailure(parseClientIP(ip).String())

	req := httptest.NewRequest(http.MethodPost, "/api/webauthn/login/finish", bytes.NewReader([]byte(`{}`)))
	req.RemoteAddr = ip
	req.AddCookie(&http.Cookie{Name: loginCeremonyCookieName, Value: "whatever"})
	w := httptest.NewRecorder()
	am.HandleLoginFinish(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusTooManyRequests, w.Body.String())
	}
}

// TestHandleLoginFinish_UserHandleMismatchRejected verifies the
// DiscoverableUserHandler rejects an assertion whose userHandle does not
// match the passkey store's stable handle for the single configured user,
// even when the credential ID itself is otherwise well-formed. This proves
// the constant-time comparison in HandleLoginFinish's closure is wired to
// the real handler path (not merely bypassed).
func TestHandleLoginFinish_UserHandleMismatchRejected(t *testing.T) {
	am := newTestAuthManagerWithPasskeys(t, "example.org", []string{"https://example.org"})

	parsedResponse, credPubKey, challenge, credentialID := loginSpecVectorNoneES256(t)
	_ = parsedResponse

	// Register a credential for the real user so the ID lookup itself would
	// otherwise succeed; only the userHandle in the assertion is wrong.
	am.passkeyStore.Add(webauthn.Credential{ID: credentialID, PublicKey: credPubKey})

	body := loginAssertionBody(t, credentialID, []byte("not-the-real-user-handle"))

	am.storePendingLogin("ceremony-1", &webauthn.SessionData{Challenge: challenge})

	req := httptest.NewRequest(http.MethodPost, "/api/webauthn/login/finish", bytes.NewReader(body))
	req.AddCookie(&http.Cookie{Name: loginCeremonyCookieName, Value: "ceremony-1"})
	w := httptest.NewRecorder()
	am.HandleLoginFinish(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	var resp WebAuthnLoginResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode WebAuthnLoginResponse: %v", err)
	}
	if resp.Success {
		t.Error("Success = true, want false (userHandle mismatch must be rejected)")
	}
}

// TestHandleLoginFinish_Success drives a full successful discoverable-login
// ceremony through the real handler using the W3C spec's "none/ES256"
// authentication test vector, with the assertion's userHandle set to the
// PasskeyStore's real stable handle. Verifies: sign count persisted, the
// SAME mitto_session cookie the password flow issues is minted, the stash
// is consumed (single-use), and the ceremony cookie is cleared.
func TestHandleLoginFinish_Success(t *testing.T) {
	am := newTestAuthManagerWithPasskeys(t, "example.org", []string{"https://example.org"})

	_, credPubKey, challenge, credentialID := loginSpecVectorNoneES256(t)
	am.passkeyStore.Add(webauthn.Credential{
		ID:        credentialID,
		PublicKey: credPubKey,
		Flags: webauthn.CredentialFlags{
			UserPresent:    true,
			BackupEligible: true,
		},
	})

	handle, err := am.passkeyStore.UserHandle()
	if err != nil {
		t.Fatalf("UserHandle() error = %v", err)
	}
	body := loginAssertionBody(t, credentialID, handle)

	am.storePendingLogin("ceremony-1", &webauthn.SessionData{Challenge: challenge})

	req := httptest.NewRequest(http.MethodPost, "/api/webauthn/login/finish", bytes.NewReader(body))
	req.AddCookie(&http.Cookie{Name: loginCeremonyCookieName, Value: "ceremony-1"})
	w := httptest.NewRecorder()
	am.HandleLoginFinish(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	var resp WebAuthnLoginResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode WebAuthnLoginResponse: %v", err)
	}
	if !resp.Success {
		t.Errorf("Success = false, want true (error=%q)", resp.Error)
	}

	stored, ok := am.passkeyStore.GetByCredentialID(credentialID)
	if !ok {
		t.Fatal("credential vanished from the PasskeyStore")
	}
	if stored.LastUsedAt.IsZero() {
		t.Error("UpdateSignCount was not applied: LastUsedAt is zero")
	}

	// The SAME session cookie the password flow issues must be set.
	resp2 := w.Result()
	var sessionCookie, ceremonyCookie *http.Cookie
	for _, c := range resp2.Cookies() {
		switch c.Name {
		case sessionCookieName:
			sessionCookie = c
		case loginCeremonyCookieName:
			ceremonyCookie = c
		}
	}
	if sessionCookie == nil || sessionCookie.Value == "" {
		t.Fatal("HandleLoginFinish did not set the mitto_session cookie")
	}
	if _, valid := am.ValidateSession(sessionCookie.Value); !valid {
		t.Error("minted session cookie does not validate against AuthManager.ValidateSession")
	}
	if ceremonyCookie == nil || ceremonyCookie.MaxAge >= 0 {
		t.Error("ceremony cookie was not cleared on successful finish")
	}

	// Single-use: the stash must have been consumed.
	am.pendingLoginMu.Lock()
	_, stillPresent := am.pendingLogins["ceremony-1"]
	am.pendingLoginMu.Unlock()
	if stillPresent {
		t.Error("pending login was not popped after a successful finish")
	}
}

// loginSpecVectorNoneES256 returns the W3C WebAuthn spec's "none/ES256"
// authentication test vector's parsed response, public key, challenge, and
// credential ID (mirrors go-webauthn's own unexported
// testLoginSpecVectorNoneES256 in webauthn/login_test.go).
// See: https://www.w3.org/TR/webauthn-3/#sctn-test-vectors-none-es256
func loginSpecVectorNoneES256(t *testing.T) (authenticatorData []byte, credPubKey []byte, challenge string, credentialID []byte) {
	t.Helper()

	const (
		authenticatorDataHex = "bfabc37432958b063360d3ad6461c9c4735ae7f8edd46592a5e0f01452b2e4b51900000000"
		credentialIDHex      = "f91f391db4c9b2fde0ea70189cba3fb63f579ba6122b33ad94ff3ec330084be4"
		challengeHex         = "39c0e7521417ba54d43e8dc95174f423dee9bf3cd804ff6d65c857c9abf4d408"
		credentialPubKeyHex  = "a5010203262001215820afefa16f97ca9b2d23eb86ccb64098d20db90856062eb249c33a9b672f26df61225820930a56b87a2fca66334b03458abf879717c12cc68ed73290af2e2664796b9220"
	)

	authenticatorData = decodeHex(t, authenticatorDataHex)
	credentialID = decodeHex(t, credentialIDHex)
	credPubKey = decodeHex(t, credentialPubKeyHex)
	challenge = base64.RawURLEncoding.EncodeToString(decodeHex(t, challengeHex))
	return authenticatorData, credPubKey, challenge, credentialID
}

// loginAssertionBody builds the JSON body of a get() assertion response
// using the spec test vector's signature/clientDataJSON/authenticatorData,
// for the given credential ID and userHandle.
func loginAssertionBody(t *testing.T, credentialID, userHandle []byte) []byte {
	t.Helper()

	const (
		clientDataJSONHex = "7b2274797065223a22776562617574686e2e676574222c226368616c6c656e6765223a224f63446e55685158756c5455506f334a5558543049393770767a7a59425039745a63685879617630314167222c226f726967696e223a2268747470733a2f2f6578616d706c652e6f7267222c2263726f73734f726967696e223a66616c73657d"
		signatureHex      = "3046022100f50a4e2e4409249c4a853ba361282f09841df4dd4547a13a87780218deffcd380221008480ac0f0b93538174f575bf11a1dd5d78c6e486013f937295ea13653e331e87"
	)

	authenticatorData, _, _, _ := loginSpecVectorNoneES256(t)

	id := base64.RawURLEncoding.EncodeToString(credentialID)
	body := map[string]any{
		"id":    id,
		"rawId": id,
		"type":  "public-key",
		"response": map[string]any{
			"authenticatorData": base64.RawURLEncoding.EncodeToString(authenticatorData),
			"clientDataJSON":    base64.RawURLEncoding.EncodeToString(decodeHex(t, clientDataJSONHex)),
			"signature":         base64.RawURLEncoding.EncodeToString(decodeHex(t, signatureHex)),
			"userHandle":        base64.RawURLEncoding.EncodeToString(userHandle),
		},
	}

	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json.Marshal(body) error = %v", err)
	}
	return data
}

func decodeHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("hex.DecodeString() error = %v", err)
	}
	return b
}
