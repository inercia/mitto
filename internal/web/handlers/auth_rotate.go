package handlers

import (
	"errors"
	"net/http"

	"github.com/inercia/mitto/internal/web/middleware"
)

// Sentinel errors returned by Deps.RotateSharedToken (mitto-pscc.9), mapped
// to HTTP 409 by HandleRotateSharedToken below. Exported so internal/web's
// Server — which implements the closure — can return them directly with
// errors.Is semantics, without this package importing internal/web (which
// would create an import cycle).
var (
	// ErrSharedTokenNotConfigured means no shared token is configured at
	// all, so there is nothing to rotate.
	ErrSharedTokenNotConfigured = errors.New("no shared token is configured")
	// ErrSharedTokenNotRotatable means the current shared token was
	// operator-configured (MITTO_SHARED_TOKEN, settings.json, or the
	// keychain) rather than adopted from instance.json. Rotating an
	// operator-managed secret through this endpoint is out of scope
	// (mitto-pscc.9 plan) — the operator must update it at its source.
	ErrSharedTokenNotRotatable = errors.New("the shared token is operator-configured and cannot be rotated via this endpoint")
)

// HandleRotateSharedToken handles POST {prefix}/api/auth/rotate-token.
//
// SECURITY: restricted to localhost connections only, like
// HandleSaveFileToPath/HandleCheckFileExists — rotation must never be
// reachable from the external listener, since it works even when auth is
// disabled (the loopback bypass in AuthMiddleware runs before the bearer
// check, so there would otherwise be no credential gating this call).
func (h *Handlers) HandleRotateSharedToken(w http.ResponseWriter, r *http.Request) {
	// Security check 1 (defense-in-depth): reject ALL requests from the
	// external listener.
	if middleware.IsExternalConnection(r) {
		if h.deps.Logger != nil {
			h.deps.Logger.Warn("Rejected auth/rotate-token request from external listener",
				"remote_addr", r.RemoteAddr,
			)
		}
		writeErrorJSON(w, http.StatusForbidden, "", "Forbidden")
		return
	}

	// Security check 2: verify this is a localhost connection. Redundant
	// with check 1 but provides defense in depth.
	if !middleware.IsLocalhostRequest(r) {
		if h.deps.Logger != nil {
			h.deps.Logger.Warn("Rejected auth/rotate-token request from non-localhost",
				"remote_addr", r.RemoteAddr,
			)
		}
		writeErrorJSON(w, http.StatusForbidden, "", "Forbidden")
		return
	}

	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	if h.deps.RotateSharedToken == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "", "Token rotation is not available")
		return
	}

	fingerprint, err := h.deps.RotateSharedToken()
	if err != nil {
		if errors.Is(err, ErrSharedTokenNotConfigured) || errors.Is(err, ErrSharedTokenNotRotatable) {
			writeErrorJSON(w, http.StatusConflict, "", err.Error())
			return
		}
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to rotate shared token", "error", err)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to rotate shared token")
		return
	}

	writeJSONOK(w, map[string]string{"fingerprint": fingerprint})
}
