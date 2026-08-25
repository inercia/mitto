package middleware

import (
	"net/http"
	"time"

	"github.com/go-webauthn/webauthn/protocol"

	"github.com/inercia/mitto/internal/logging"
)

// CredentialInfo is the JSON shape of one entry in HandleListCredentials'
// response: enough for the Settings UI to render a "Manage passkeys" list
// with delete affordance, without exposing the public key or attestation
// details (mitto-4mz.6).
type CredentialInfo struct {
	// ID is the credential ID, base64url-encoded (protocol.URLEncodedBase64's
	// MarshalJSON), matching the encoding go-webauthn's own JSON types use so
	// the frontend need not special-case this endpoint's byte encoding.
	ID         protocol.URLEncodedBase64 `json:"id"`
	CreatedAt  time.Time                 `json:"created_at"`
	LastUsedAt time.Time                 `json:"last_used_at"`
}

// HandleListCredentials handles GET /api/webauthn/register/list. It requires
// an authenticated mitto_session cookie (registration/management is scoped to
// the single External Access user, same gate as HandleRegisterBegin/Finish)
// and returns 404 when passkeys are not enabled/derivable.
func (a *AuthManager) HandleListCredentials(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
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

	if _, valid := a.GetSessionFromRequest(r); !valid {
		writeJSON(w, http.StatusUnauthorized, RegisterResponse{Success: false, Error: "authentication required"})
		return
	}

	stored := store.List()
	out := make([]CredentialInfo, len(stored))
	for i, c := range stored {
		out[i] = CredentialInfo{
			ID:         protocol.URLEncodedBase64(c.Credential.ID),
			CreatedAt:  c.CreatedAt,
			LastUsedAt: c.LastUsedAt,
		}
	}

	writeJSONOK(w, out)
}

// HandleDeleteCredential handles DELETE /api/webauthn/register/{id}, where
// {id} is the credential ID, base64url-encoded (the same encoding
// CredentialInfo.ID and go-webauthn's own JSON types use). Requires an
// authenticated mitto_session cookie and returns 404 when passkeys are not
// enabled/derivable OR when no credential matches the given id.
func (a *AuthManager) HandleDeleteCredential(w http.ResponseWriter, r *http.Request) {
	logger := logging.Auth()

	if r.Method != http.MethodDelete {
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

	if _, valid := a.GetSessionFromRequest(r); !valid {
		writeJSON(w, http.StatusUnauthorized, RegisterResponse{Success: false, Error: "authentication required"})
		return
	}

	rawID := r.PathValue("id")
	var id protocol.URLEncodedBase64
	if err := id.UnmarshalJSON([]byte(`"` + rawID + `"`)); err != nil || len(id) == 0 {
		writeJSON(w, http.StatusBadRequest, RegisterResponse{Success: false, Error: "invalid credential id"})
		return
	}

	if !store.Delete(id) {
		http.NotFound(w, r)
		return
	}

	logger.Info("WEBAUTHN: Passkey credential deleted")
	writeJSON(w, http.StatusOK, RegisterResponse{Success: true})
}
