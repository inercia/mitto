package web

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/inercia/mitto/internal/instancefile"
)

// validateInstanceBearer implements handlers.Deps.ValidateInstanceBearer
// (mitto-4rz.3): a dedicated instance-bearer guard for GET
// /api/config/snapshot and POST /api/config/patch, independent of whether
// global auth (Simple/Cloudflare/shared-token, via *middleware.AuthManager)
// is configured — AuthManager may be nil, and even when non-nil only
// adopts the instance token as ITS shared token when Simple/Cloudflare auth
// is enabled (see internal/cmd/web.go), so it cannot serve as this gate.
// There is deliberately NO loopback exemption and NO cookie/session
// fallback: the instance-bearer requirement applies regardless of how the
// request arrived.
//
// It is a standalone function (not a *Server method) so it can also be
// composed into the CSRF token-auth checker (see NewServer), which is wired
// up before the *Server value exists. It reads $MITTO_DIR/instance.json
// fresh on every call — never cached — so a rotated token
// (POST /api/auth/rotate-token) is honored immediately with no in-memory
// staleness window.
func validateInstanceBearer(r *http.Request) bool {
	token := extractInstanceBearerToken(r)
	if token == "" {
		return false
	}
	inst, err := instancefile.Read()
	if err != nil {
		// Covers ErrNotFound, ErrStale, and ErrCorrupt alike: none of them
		// represent "this running server's own current token", so none can
		// authenticate a request. This server's own instance.json record
		// should never appear stale to itself while it is running; if it
		// does (e.g. another instance overwrote the file), failing closed
		// is correct.
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(inst.Token)) == 1
}

// extractInstanceBearerToken extracts the token from an
// "Authorization: Bearer <token>" header. A deliberate, small duplicate of
// middleware's unexported extractBearerToken: this guard must work
// identically whether or not a *middleware.AuthManager exists at all, so it
// does not depend on that package's internals.
func extractInstanceBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	const prefix = "bearer "
	if len(auth) <= len(prefix) || !strings.EqualFold(auth[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(auth[len(prefix):])
}
