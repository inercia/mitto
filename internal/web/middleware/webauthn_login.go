package middleware

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/inercia/mitto/internal/logging"
)

// loginSessionTTL bounds how long a begin-login ceremony may remain pending
// before it must be restarted. Generous enough for a user to complete a
// platform authenticator prompt, short enough to bound stash growth.
const loginSessionTTL = 5 * time.Minute

// loginCeremonyCookieName is the short-lived HttpOnly cookie that keys the
// pendingLogins stash between HandleLoginBegin and HandleLoginFinish. Unlike
// registration (keyed by the caller's authenticated session token, since a
// session already exists), login is pre-auth — there is no session token yet
// — so a server-set ceremony cookie is the standard way to correlate the two
// requests of the same same-browser WebAuthn ceremony (mitto-4mz.4).
const loginCeremonyCookieName = "mitto_webauthn_login"

// loginCeremonyTokenBytes is the size of the random ceremony cookie value.
const loginCeremonyTokenBytes = 32

// pendingLogin stashes an in-flight WebAuthn discoverable-login ceremony's
// SessionData, keyed by the server-set ceremony cookie, between
// HandleLoginBegin and HandleLoginFinish.
type pendingLogin struct {
	session *webauthn.SessionData
	expires time.Time
}

// LoginResponse is the JSON response shape returned by HandleLoginBegin (on
// failure only, since success returns the credential assertion options
// directly) and HandleLoginFinish.
type WebAuthnLoginResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// storePendingLogin stashes sessionData for the given ceremony id, replacing
// any previous pending login for that id.
func (a *AuthManager) storePendingLogin(ceremonyID string, sessionData *webauthn.SessionData) {
	a.pendingLoginMu.Lock()
	defer a.pendingLoginMu.Unlock()
	a.pendingLogins[ceremonyID] = &pendingLogin{
		session: sessionData,
		expires: time.Now().Add(loginSessionTTL),
	}
}

// popPendingLogin retrieves and removes the pending login for the given
// ceremony id, returning ok=false if absent or expired.
func (a *AuthManager) popPendingLogin(ceremonyID string) (*webauthn.SessionData, bool) {
	a.pendingLoginMu.Lock()
	defer a.pendingLoginMu.Unlock()
	pending, ok := a.pendingLogins[ceremonyID]
	if !ok {
		return nil, false
	}
	delete(a.pendingLogins, ceremonyID)
	if time.Now().After(pending.expires) {
		return nil, false
	}
	return pending.session, true
}

// prunePendingLogins removes expired pending logins. Called from
// cleanupExpiredSessions on the existing periodic cleanup cadence.
func (a *AuthManager) prunePendingLogins() {
	a.pendingLoginMu.Lock()
	defer a.pendingLoginMu.Unlock()
	now := time.Now()
	for id, pending := range a.pendingLogins {
		if now.After(pending.expires) {
			delete(a.pendingLogins, id)
		}
	}
}

// setLoginCeremonyCookie sets the short-lived HttpOnly ceremony cookie that
// keys the pendingLogins stash entry created by HandleLoginBegin.
func (a *AuthManager) setLoginCeremonyCookie(w http.ResponseWriter, r *http.Request, ceremonyID string) {
	http.SetCookie(w, &http.Cookie{
		Name:     loginCeremonyCookieName,
		Value:    ceremonyID,
		Path:     "/",
		HttpOnly: true,
		Secure:   shouldUseSecureCookie(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(loginSessionTTL.Seconds()),
	})
}

// clearLoginCeremonyCookie removes the ceremony cookie, on both success and
// failure of HandleLoginFinish.
func (a *AuthManager) clearLoginCeremonyCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     loginCeremonyCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   shouldUseSecureCookie(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// HandleLoginBegin handles POST /api/webauthn/login/begin. It starts a
// discoverable (usernameless) WebAuthn login ceremony and stashes the
// resulting SessionData, keyed by a fresh server-set ceremony cookie, for the
// matching HandleLoginFinish call. This endpoint is pre-auth (public) and
// CSRF-exempt: there is no session yet, and the WebAuthn assertion itself is
// inherently CSRF-resistant (see publicAPIPaths / csrfExemptAPIPaths).
func (a *AuthManager) HandleLoginBegin(w http.ResponseWriter, r *http.Request) {
	logger := logging.Auth()

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	a.mu.RLock()
	wa := a.webAuthn
	store := a.passkeyStore
	a.mu.RUnlock()

	if wa == nil || store == nil {
		http.NotFound(w, r)
		return
	}

	assertion, sessionData, err := wa.BeginDiscoverableLogin()
	if err != nil {
		logger.Error("WEBAUTHN: BeginDiscoverableLogin failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, WebAuthnLoginResponse{Success: false, Error: "failed to begin login"})
		return
	}

	ceremonyID, err := generateLoginCeremonyID()
	if err != nil {
		logger.Error("WEBAUTHN: Failed to generate login ceremony id", "error", err)
		writeJSON(w, http.StatusInternalServerError, WebAuthnLoginResponse{Success: false, Error: "failed to begin login"})
		return
	}

	a.storePendingLogin(ceremonyID, sessionData)
	a.setLoginCeremonyCookie(w, r, ceremonyID)

	writeJSON(w, http.StatusOK, assertion)
}

// HandleLoginFinish handles POST /api/webauthn/login/finish. It validates the
// authenticator's assertion response (parsed directly from the request body
// by go-webauthn) against the SessionData stashed by HandleLoginBegin, using
// a DiscoverableUserHandler that resolves the single configured External
// Access user. On success it persists the updated sign count and mints the
// SAME mitto_session cookie the password login flow issues, so the rest of
// the stack (auth middleware, session validation) is unchanged.
func (a *AuthManager) HandleLoginFinish(w http.ResponseWriter, r *http.Request) {
	logger := logging.Auth()

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	a.mu.RLock()
	wa := a.webAuthn
	store := a.passkeyStore
	a.mu.RUnlock()

	if wa == nil || store == nil {
		http.NotFound(w, r)
		return
	}

	clientIP := GetClientIPWithProxyCheck(r)
	parsedIP := parseClientIP(clientIP)
	ipKey := clientIP
	if parsedIP != nil {
		ipKey = parsedIP.String()
	}

	if blocked, remaining := a.rateLimiter.IsBlocked(ipKey); blocked {
		a.clearLoginCeremonyCookie(w, r)
		retryAfter := int(remaining.Seconds()) + 1
		logger.Warn("WEBAUTHN: Login attempt from rate-limited IP", "client_ip", ipKey, "retry_after_sec", retryAfter)
		writeJSON(w, http.StatusTooManyRequests, WebAuthnLoginResponse{Success: false, Error: "Too many failed attempts. Please try again later."})
		return
	}

	cookie, err := r.Cookie(loginCeremonyCookieName)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, WebAuthnLoginResponse{Success: false, Error: "login session expired or not found"})
		return
	}

	sessionData, ok := a.popPendingLogin(cookie.Value)
	a.clearLoginCeremonyCookie(w, r)
	if !ok {
		writeJSON(w, http.StatusBadRequest, WebAuthnLoginResponse{Success: false, Error: "login session expired or not found"})
		return
	}

	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		expectedHandle, err := store.UserHandle()
		if err != nil {
			return nil, err
		}
		if len(userHandle) != len(expectedHandle) || subtle.ConstantTimeCompare(userHandle, expectedHandle) != 1 {
			return nil, ErrNoCredentials
		}
		return store.User(a.config.Simple.Username)
	}

	user, cred, err := wa.FinishPasskeyLogin(handler, *sessionData, r)
	if err != nil {
		a.rateLimiter.RecordFailure(ipKey)
		fields := append([]any{"error", err}, webAuthnErrFields(err)...)
		logger.Warn("WEBAUTHN: FinishPasskeyLogin failed", fields...)
		writeJSON(w, http.StatusBadRequest, WebAuthnLoginResponse{Success: false, Error: "failed to verify passkey login"})
		return
	}

	a.rateLimiter.RecordSuccess(ipKey)
	store.UpdateSignCount(cred.ID, cred.Authenticator.SignCount)

	username := user.WebAuthnName()
	session, err := a.CreateSession(username)
	if err != nil {
		logger.Error("WEBAUTHN: Failed to create session after passkey login", "error", err, "username", username)
		writeJSON(w, http.StatusInternalServerError, WebAuthnLoginResponse{Success: false, Error: "failed to complete login"})
		return
	}

	logger.Info("WEBAUTHN: Passkey login successful", "username", username, "client_ip", ipKey)
	a.SetSessionCookie(w, r, session)
	writeJSON(w, http.StatusOK, WebAuthnLoginResponse{Success: true})
}

// generateLoginCeremonyID creates a cryptographically secure random id used
// to key the ceremony cookie / pendingLogins stash entry.
func generateLoginCeremonyID() (string, error) {
	b := make([]byte, loginCeremonyTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
