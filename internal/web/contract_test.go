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
