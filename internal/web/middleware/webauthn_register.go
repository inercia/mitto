package middleware

import (
	"net/http"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/inercia/mitto/internal/logging"
)

// registrationSessionTTL bounds how long a begin-registration ceremony may
// remain pending before it must be restarted. Generous enough for a user to
// complete a platform authenticator prompt, short enough to bound stash growth.
const registrationSessionTTL = 5 * time.Minute

// pendingRegistration stashes an in-flight WebAuthn registration ceremony's
// SessionData, keyed by the caller's authenticated session token, between
// HandleRegisterBegin and HandleRegisterFinish (mitto-4mz.3).
type pendingRegistration struct {
	session *webauthn.SessionData
	expires time.Time
}

// RegisterResponse is the JSON response shape returned by HandleRegisterBegin
// (on failure only, since success returns the credential creation options
// directly) and HandleRegisterFinish.
type RegisterResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// storePendingRegistration stashes sessionData for the given session token,
// replacing any previous pending registration for that token.
func (a *AuthManager) storePendingRegistration(sessionToken string, sessionData *webauthn.SessionData) {
	a.pendingRegMu.Lock()
	defer a.pendingRegMu.Unlock()
	a.pendingRegistrations[sessionToken] = &pendingRegistration{
		session: sessionData,
		expires: time.Now().Add(registrationSessionTTL),
	}
}

// popPendingRegistration retrieves and removes the pending registration for
// the given session token, returning ok=false if absent or expired.
func (a *AuthManager) popPendingRegistration(sessionToken string) (*webauthn.SessionData, bool) {
	a.pendingRegMu.Lock()
	defer a.pendingRegMu.Unlock()
	pending, ok := a.pendingRegistrations[sessionToken]
	if !ok {
		return nil, false
	}
	delete(a.pendingRegistrations, sessionToken)
	if time.Now().After(pending.expires) {
		return nil, false
	}
	return pending.session, true
}

// prunePendingRegistrations removes expired pending registrations. Called
// from cleanupExpiredSessions on the existing periodic cleanup cadence.
func (a *AuthManager) prunePendingRegistrations() {
	a.pendingRegMu.Lock()
	defer a.pendingRegMu.Unlock()
	now := time.Now()
	for token, pending := range a.pendingRegistrations {
		if now.After(pending.expires) {
			delete(a.pendingRegistrations, token)
		}
	}
}

// HandleRegisterBegin handles POST /api/webauthn/register/begin. It starts a
// WebAuthn registration ceremony for the single External Access user, enforcing
// resident-key (discoverable) credentials with preferred user verification, and
// stashes the resulting SessionData keyed by the caller's session token for the
// matching HandleRegisterFinish call.
func (a *AuthManager) HandleRegisterBegin(w http.ResponseWriter, r *http.Request) {
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

	session, valid := a.GetSessionFromRequest(r)
	if !valid {
		writeJSON(w, http.StatusUnauthorized, RegisterResponse{Success: false, Error: "authentication required"})
		return
	}

	user, err := store.User(session.Username)
	if err != nil {
		logger.Error("WEBAUTHN: Failed to prepare user for registration", "error", err)
		writeJSON(w, http.StatusInternalServerError, RegisterResponse{Success: false, Error: "failed to begin registration"})
		return
	}

	creation, sessionData, err := wa.BeginRegistration(user,
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementRequired,
			UserVerification: protocol.VerificationPreferred,
		}),
	)
	if err != nil {
		logger.Error("WEBAUTHN: BeginRegistration failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, RegisterResponse{Success: false, Error: "failed to begin registration"})
		return
	}

	a.storePendingRegistration(session.Token, sessionData)

	writeJSON(w, http.StatusOK, creation)
}

// HandleRegisterFinish handles POST /api/webauthn/register/finish. It validates
// the authenticator's attestation response (parsed directly from the request
// body by go-webauthn) against the SessionData stashed by HandleRegisterBegin,
// and persists the resulting credential to the PasskeyStore.
func (a *AuthManager) HandleRegisterFinish(w http.ResponseWriter, r *http.Request) {
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

	session, valid := a.GetSessionFromRequest(r)
	if !valid {
		writeJSON(w, http.StatusUnauthorized, RegisterResponse{Success: false, Error: "authentication required"})
		return
	}

	sessionData, ok := a.popPendingRegistration(session.Token)
	if !ok {
		writeJSON(w, http.StatusBadRequest, RegisterResponse{Success: false, Error: "registration session expired or not found"})
		return
	}

	user, err := store.User(session.Username)
	if err != nil {
		logger.Error("WEBAUTHN: Failed to prepare user for registration", "error", err)
		writeJSON(w, http.StatusInternalServerError, RegisterResponse{Success: false, Error: "failed to complete registration"})
		return
	}

	cred, err := wa.FinishRegistration(user, *sessionData, r)
	if err != nil {
		logger.Warn("WEBAUTHN: FinishRegistration failed", "error", err, "username", session.Username)
		writeJSON(w, http.StatusBadRequest, RegisterResponse{Success: false, Error: "failed to verify passkey registration"})
		return
	}

	store.Add(*cred)

	logger.Info("WEBAUTHN: Passkey registered", "username", session.Username)
	writeJSON(w, http.StatusOK, RegisterResponse{Success: true})
}
