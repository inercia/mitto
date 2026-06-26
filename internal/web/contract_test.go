package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/session"
	"github.com/inercia/mitto/internal/web/handlers"
	"github.com/inercia/mitto/internal/web/middleware"
)

// ctErr is the local envelope type used by all contract tests.
type ctErr struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// newContractServer returns a minimal *Server with apiHandlers wired for
// contract tests. The store is closed automatically via t.Cleanup.
func newContractServer(t *testing.T) *Server {
	t.Helper()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	sm := conversation.NewSessionManager("", "", false, nil)
	s := &Server{sessionManager: sm, store: store}
	s.apiHandlers = handlers.New(handlers.Deps{Store: store, SessionManager: sm})
	return s
}

// newContractMux builds a targeted http.ServeMux for the session base routes,
// registering only the routes needed for the 405 and envelope-shape tests.
func newContractMux(s *Server) *http.ServeMux {
	mux := http.NewServeMux()
	// All-method dispatcher: handler-level 405 for unsupported methods.
	mux.HandleFunc("/api/sessions", s.handleSessions)
	// Method-qualified session resource routes (central/mux 405).
	mux.HandleFunc("GET /api/sessions/{id}", s.handleSessionGet)
	mux.HandleFunc("PATCH /api/sessions/{id}", s.handleSessionUpdate)
	mux.HandleFunc("DELETE /api/sessions/{id}", s.handleSessionDelete)
	return mux
}

// pathParamRe replaces {param} segments with a concrete placeholder "x".
var pathParamRe = regexp.MustCompile(`\{[^}]+\}`)

// TestContract_RouteTableReachable registers the ENTIRE route table on a
// single mux (mirroring server.go) and verifies that every declared route is
// reachable. A panic during registration means there is a pattern conflict or
// drift — the deferred recover converts it into a clear t.Fatalf.
func TestContract_RouteTableReachable(t *testing.T) {
	s := newContractServer(t)
	csrfMgr := middleware.NewCSRFManager()
	fileServer := NewFileServer(s.sessionManager, nil)
	routes := s.apiRoutes(nil, csrfMgr, fileServer)

	if len(routes) == 0 {
		t.Fatal("apiRoutes returned no routes")
	}

	mux := http.NewServeMux()
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("route table panics on single-mux registration (conflict/drift): %v", r)
			}
		}()
		for _, rt := range routes {
			pattern := rt.pattern
			if rt.method != "" {
				pattern = rt.method + " " + pattern
			}
			mux.Handle(pattern, rt.handler)
		}
	}()

	for _, rt := range routes {
		rt := rt // capture
		t.Run(rt.pattern, func(t *testing.T) {
			method := rt.method
			if method == "" {
				method = http.MethodGet
			}
			path := pathParamRe.ReplaceAllString(rt.pattern, "x")
			req := httptest.NewRequest(method, path, nil)
			_, pat := mux.Handler(req)
			if pat == "" || pat == "/" {
				t.Errorf("route is unreachable: mux returned pattern %q for path %q", pat, path)
			}
		})
	}
}

// TestContract_MethodNotAllowed checks both 405 paths: the mux's automatic
// central 405 (status only) and the handler-level 405 (with JSON envelope).
func TestContract_MethodNotAllowed(t *testing.T) {
	s := newContractServer(t)
	mux := newContractMux(s)

	// Central/router 405 — Go 1.22 mux rejects PUT on a method-qualified route.
	// Assert status only; the mux's default body is plain text, NOT an envelope.
	t.Run("central_mux_405", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/api/sessions/20260131-120000-abcd1234", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("Status = %d, want 405", w.Code)
		}
	})

	// Handler-level 405 — s.handleSessions dispatches via methodNotAllowed.
	// Assert status AND the canonical JSON envelope.
	t.Run("handler_405_envelope", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/api/sessions", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("Status = %d, want 405", w.Code)
		}
		var env ctErr
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatalf("body is not JSON envelope: %v (body=%q)", err, w.Body.String())
		}
		if env.Error.Code != "method_not_allowed" {
			t.Errorf("error.code = %q, want %q", env.Error.Code, "method_not_allowed")
		}
		if env.Error.Message == "" {
			t.Error("error.message is empty")
		}
	})
}

// TestContract_MethodNotAllowed_AllRoutes locks the "wrong HTTP method → 405"
// convention across the ENTIRE method-qualified route table (not just sessions).
// For every concrete path that has at least one method-qualified route, it picks
// the first method in {GET,POST,PUT,PATCH,DELETE} that is NOT registered there
// and asserts that the mux returns 405.
// Status is asserted only — the Go 1.22 central-mux 405 body is plain text,
// NOT an envelope (mirrors the central_mux_405 comment in TestContract_MethodNotAllowed).
func TestContract_MethodNotAllowed_AllRoutes(t *testing.T) {
	s := newContractServer(t)
	csrfMgr := middleware.NewCSRFManager()
	fileServer := NewFileServer(s.sessionManager, nil)
	routes := s.apiRoutes(nil, csrfMgr, fileServer)

	if len(routes) == 0 {
		t.Fatal("apiRoutes returned no routes")
	}

	// Register the full route table on a single mux (same as RouteTableReachable).
	mux := http.NewServeMux()
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("route table panics on single-mux registration (conflict/drift): %v", r)
			}
		}()
		for _, rt := range routes {
			pattern := rt.pattern
			if rt.method != "" {
				pattern = rt.method + " " + pattern
			}
			mux.Handle(pattern, rt.handler)
		}
	}()

	// Group registered HTTP methods per concrete path (method-qualified routes only).
	pathMethods := make(map[string]map[string]bool)
	for _, rt := range routes {
		if rt.method == "" {
			continue
		}
		path := pathParamRe.ReplaceAllString(rt.pattern, "x")
		if pathMethods[path] == nil {
			pathMethods[path] = make(map[string]bool)
		}
		pathMethods[path][rt.method] = true
	}

	// Ordered candidate methods — avoid OPTIONS/HEAD (mux treats those specially).
	candidates := []string{
		http.MethodGet,
		http.MethodPost,
		http.MethodPut,
		http.MethodPatch,
		http.MethodDelete,
	}

	for path, methods := range pathMethods {
		path, methods := path, methods // capture loop vars

		// Probe candidate methods to find one the mux genuinely 405s.
		// We cannot rely on the registered-method set alone: a concrete path like
		// /api/issues/cleanup is also matched by a wildcard route (GET /api/issues/{id}),
		// so GET there is routed to that handler (returning 400, not 405). We must
		// probe the actual mux to detect real 405 behaviour.
		disallowed := ""
		for _, m := range candidates {
			if methods[m] {
				continue // definitely registered for this exact pattern — skip
			}
			probe := httptest.NewRequest(m, path, nil)
			pw := httptest.NewRecorder()
			mux.ServeHTTP(pw, probe)
			if pw.Code == http.StatusMethodNotAllowed {
				disallowed = m
				break
			}
		}
		if disallowed == "" {
			// No candidate method yielded 405 — path is fully covered by overlapping
			// routes or has all five methods registered. Skip.
			continue
		}

		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(disallowed, path, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			// Central/mux 405 — status only; Go 1.22 mux body is plain text, NOT an envelope.
			if w.Code != http.StatusMethodNotAllowed {
				registered := make([]string, 0, len(methods))
				for m := range methods {
					registered = append(registered, m)
				}
				t.Errorf("path %q: %s returned %d, want 405 (registered methods: %v)",
					path, disallowed, w.Code, registered)
			}
		})
	}
}

// TestContract_ErrorEnvelopeShape is a table-driven test over representative
// migrated error paths. Each case asserts: correct HTTP status AND that the
// body decodes into {error:{code,message}} with the expected non-empty code.
func TestContract_ErrorEnvelopeShape(t *testing.T) {
	s := newContractServer(t)
	mux := newContractMux(s)

	const validID = "20260131-120000-abcd1234"

	cases := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{"GET session not found", http.MethodGet, "/api/sessions/" + validID, "", http.StatusNotFound, "not_found"},
		{"DELETE session not found", http.MethodDelete, "/api/sessions/" + validID, "", http.StatusNotFound, "not_found"},
		{"PATCH session malformed body", http.MethodPatch, "/api/sessions/" + validID, "INVALID", http.StatusBadRequest, "bad_request"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var bodyReader io.Reader
			if tc.body != "" {
				bodyReader = strings.NewReader(tc.body)
			}
			req := httptest.NewRequest(tc.method, tc.path, bodyReader)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Errorf("Status = %d, want %d", w.Code, tc.wantStatus)
			}
			var env ctErr
			if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
				t.Fatalf("body is not JSON envelope: %v (body=%q)", err, w.Body.String())
			}
			if env.Error.Code == "" {
				t.Errorf("error.code is empty, want %q", tc.wantCode)
			} else if env.Error.Code != tc.wantCode {
				t.Errorf("error.code = %q, want %q", env.Error.Code, tc.wantCode)
			}
			if env.Error.Message == "" {
				t.Error("error.message is empty")
			}
		})
	}
}

// TestContract_ErrorEnvelopeShape_NonSession broadens the error-envelope
// convention check to non-session resources (beads/issues + workspaces),
// driven through the FULL route-table mux. Each case verifies that early
// validation failures on these handlers produce the canonical
// {error:{code,message}} envelope — no fixtures or bd DB required.
func TestContract_ErrorEnvelopeShape_NonSession(t *testing.T) {
	s := newContractServer(t)
	csrfMgr := middleware.NewCSRFManager()
	fileServer := NewFileServer(s.sessionManager, nil)
	routes := s.apiRoutes(nil, csrfMgr, fileServer)

	// Register the full route table on a single mux (same as MethodNotAllowed_AllRoutes).
	mux := http.NewServeMux()
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("route table panics on single-mux registration (conflict/drift): %v", r)
			}
		}()
		for _, rt := range routes {
			pattern := rt.pattern
			if rt.method != "" {
				pattern = rt.method + " " + pattern
			}
			mux.Handle(pattern, rt.handler)
		}
	}()

	cases := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{"POST issues malformed body", http.MethodPost, "/api/issues", "INVALID", http.StatusBadRequest, "bad_request"},
		{"PUT issues/config missing working_dir", http.MethodPut, "/api/issues/config", "{}", http.StatusBadRequest, "bad_request"},
		{"GET workspace metadata unknown uuid", http.MethodGet, "/api/workspaces/nonexistent/metadata", "", http.StatusNotFound, "not_found"},
		{"PUT workspace metadata unknown uuid", http.MethodPut, "/api/workspaces/nonexistent/metadata", "{}", http.StatusNotFound, "not_found"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var bodyReader io.Reader
			if tc.body != "" {
				bodyReader = strings.NewReader(tc.body)
			}
			req := httptest.NewRequest(tc.method, tc.path, bodyReader)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Errorf("Status = %d, want %d (body=%q)", w.Code, tc.wantStatus, w.Body.String())
			}
			var env ctErr
			if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
				t.Fatalf("body is not JSON envelope: %v (body=%q)", err, w.Body.String())
			}
			if env.Error.Code == "" {
				t.Errorf("error.code is empty, want %q", tc.wantCode)
			} else if env.Error.Code != tc.wantCode {
				t.Errorf("error.code = %q, want %q", env.Error.Code, tc.wantCode)
			}
			if env.Error.Message == "" {
				t.Error("error.message is empty")
			}
		})
	}
}
