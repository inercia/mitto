package web

import (
	"net/http"
	"net/http/pprof"

	"github.com/inercia/mitto/internal/web/middleware"
)

// maybeRegisterPProfRoutes registers the pprof debug endpoints only when
// enabled, and reports whether it did. Extracted from NewServer so the
// off-by-default contract is directly testable (mitto-aek).
func (s *Server) maybeRegisterPProfRoutes(mux *http.ServeMux, enabled bool) bool {
	if !enabled {
		return false
	}
	s.registerPProfRoutes(mux)
	return true
}

// registerPProfRoutes registers the standard net/http/pprof debug endpoints
// on mux, gated to loopback-only connections (mitto-aek). Only called from
// maybeRegisterPProfRoutes when profiling is enabled.
//
// SECURITY: profiles can leak goroutine stacks, heap contents and command
// line arguments, so every handler is wrapped with pprofLocalOnly — the same
// two-step localhost gate used by internal/web/handlers/save_file.go. The
// routes are intentionally NOT added to middleware.publicAPIPaths, so the
// normal auth middleware still applies on external connections; on the
// internal listener, loopback traffic is already auth-exempt.
func (s *Server) registerPProfRoutes(mux *http.ServeMux) {
	mux.Handle("/debug/pprof/", pprofLocalOnly(http.HandlerFunc(pprof.Index)))
	mux.Handle("/debug/pprof/cmdline", pprofLocalOnly(http.HandlerFunc(pprof.Cmdline)))
	mux.Handle("/debug/pprof/profile", pprofLocalOnly(http.HandlerFunc(pprof.Profile)))
	mux.Handle("/debug/pprof/symbol", pprofLocalOnly(http.HandlerFunc(pprof.Symbol)))
	mux.Handle("/debug/pprof/trace", pprofLocalOnly(http.HandlerFunc(pprof.Trace)))
	// Named profiles (heap, goroutine, allocs, block, mutex, threadcreate)
	// are already served by pprof.Index via its internal handler lookup, so
	// no extra registration is needed for them.
}

// pprofLocalOnly wraps an http.Handler so it only serves loopback requests.
// Rejections return 404 (not 403) so an external probe cannot distinguish
// "profiling enabled but forbidden" from "endpoint does not exist".
func pprofLocalOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Defense-in-depth: reject connections that came through the
		// external listener outright, even if somehow loopback-addressed.
		if middleware.IsExternalConnection(r) {
			http.NotFound(w, r)
			return
		}
		if !middleware.IsLocalhostRequest(r) {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
