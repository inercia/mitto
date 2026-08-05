package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/inercia/mitto/internal/web/middleware"
)

// TestPprofLocalOnly_LoopbackHost_Allowed verifies the gate lets through
// requests whose Host header identifies a loopback client (mitto-aek).
func TestPprofLocalOnly_LoopbackHost_Allowed(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := pprofLocalOnly(next)

	req := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	req.Host = "127.0.0.1:8080"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !called {
		t.Fatal("expected wrapped handler to be called for a loopback host")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

// TestPprofLocalOnly_NonLoopbackHost_Returns404 verifies non-loopback
// requests are rejected with 404 (not 403), so an external probe cannot
// distinguish "enabled but forbidden" from "route absent" (mitto-aek).
func TestPprofLocalOnly_NonLoopbackHost_Returns404(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	handler := pprofLocalOnly(next)

	req := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	req.Host = "example.com"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if called {
		t.Error("wrapped handler should not be called for a non-loopback host")
	}
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

// TestPprofLocalOnly_ExternalConnection_Returns404EvenFromLoopbackHost
// verifies the defense-in-depth check: a request marked as coming through
// the external listener is rejected even if its Host header is loopback
// (mirrors the check in internal/web/handlers/image_frompath_test.go).
func TestPprofLocalOnly_ExternalConnection_Returns404EvenFromLoopbackHost(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	handler := pprofLocalOnly(next)

	req := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	req.Host = "127.0.0.1:8080" // would pass the loopback check alone
	ctx := context.WithValue(req.Context(), middleware.ContextKeyExternalConnection, true)
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if called {
		t.Error("wrapped handler should not be called for an external connection, even from a loopback host")
	}
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

// TestRegisterPProfRoutes_RegistersExpectedPaths verifies all five standard
// net/http/pprof routes are registered on the mux. Uses mux.Handler (which
// only resolves the match, without invoking it) so pprof.Profile/pprof.Trace
// are not actually triggered (they would block for real seconds otherwise).
func TestRegisterPProfRoutes_RegistersExpectedPaths(t *testing.T) {
	s := &Server{}
	mux := http.NewServeMux()
	s.registerPProfRoutes(mux)

	paths := []string{
		"/debug/pprof/",
		"/debug/pprof/cmdline",
		"/debug/pprof/profile",
		"/debug/pprof/symbol",
		"/debug/pprof/trace",
	}
	for _, p := range paths {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		req.Host = "127.0.0.1"
		_, pattern := mux.Handler(req)
		if pattern == "" {
			t.Errorf("expected a registered handler for path %q, got none (unmatched)", p)
		}
	}
}
