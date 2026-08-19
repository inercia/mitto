package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	"github.com/inercia/mitto/internal/appdir"
	configPkg "github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/web/middleware"
)

// TestHTTPServersConfigureResourceLimits reproduces mitto-syqh: every HTTP
// listener must explicitly bound header parsing and header memory.
func TestHTTPServersConfigureResourceLimits(t *testing.T) {
	t.Run("primary", func(t *testing.T) {
		t.Setenv(appdir.MittoDirEnv, t.TempDir())
		appdir.ResetCache()
		t.Cleanup(appdir.ResetCache)

		workspaceDir := t.TempDir()
		srv, err := NewServer(Config{
			Workspaces:              []configPkg.WorkspaceSettings{{ACPServer: "test", WorkingDir: workspaceDir}},
			ACPServer:               "test",
			DefaultWorkingDir:       workspaceDir,
			FromCLI:                 true,
			DisableAuxiliaryPrewarm: true,
			MittoConfig:             &configPkg.Config{Web: configPkg.WebConfig{ExternalPort: -1}},
		})
		if err != nil {
			t.Fatalf("NewServer() error = %v", err)
		}
		t.Cleanup(func() { _ = srv.Shutdown() })

		assertHTTPHeaderLimits(t, srv.httpServer)
	})

	t.Run("external", func(t *testing.T) {
		srv := newExternalListenerTestServer(t)
		if _, err := srv.StartExternalListener(0); err != nil {
			t.Fatalf("StartExternalListener() error = %v", err)
		}
		t.Cleanup(srv.StopExternalListener)

		assertHTTPHeaderLimits(t, srv.externalHTTPServer)
	})
}

func assertHTTPHeaderLimits(t *testing.T, srv *http.Server) {
	t.Helper()
	if srv.ReadHeaderTimeout <= 0 {
		t.Errorf("ReadHeaderTimeout = %v, want a positive Slowloris deadline", srv.ReadHeaderTimeout)
	}
	if srv.MaxHeaderBytes <= 0 {
		t.Errorf("MaxHeaderBytes = %d, want an explicit positive limit", srv.MaxHeaderBytes)
	}
}

// TestExternalGlobalEventsWebSocketConnectionsAreBounded reproduces mitto-syqh:
// authenticated external upgrades must admit normal startup fan-out but reject
// one abusive source before it can retain an unbounded number of pumps.
func TestExternalGlobalEventsWebSocketConnectionsAreBounded(t *testing.T) {
	srv := &Server{
		eventsManager:       NewGlobalEventsManager(),
		wsSecurityConfig:    middleware.DefaultWebSocketSecurityConfig(),
		wsConnectionLimiter: newWSConnectionLimiter(defaultMaxWSConnectionsPerIP, defaultMaxWSConnections),
	}
	handler := ExternalConnectionMiddleware(http.HandlerFunc(srv.handleGlobalEventsWS))
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")
	connections := make([]*websocket.Conn, 0, 100)
	defer func() {
		for _, conn := range connections {
			_ = conn.Close()
		}
	}()

	rejected := false
	for i := 0; i < 100; i++ {
		conn, response, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err == nil {
			connections = append(connections, conn)
			continue
		}
		if response == nil || response.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("upgrade %d failed with response=%v, err=%v; want HTTP 429 admission rejection", i+1, response, err)
		}
		if response.Header.Get("Retry-After") == "" {
			t.Error("HTTP 429 WebSocket rejection is missing Retry-After")
		}
		rejected = true
		break
	}

	if len(connections) < 8 {
		t.Fatalf("admitted %d WebSockets, want at least 8 for normal multi-session startup", len(connections))
	}
	if !rejected {
		t.Fatalf("admitted %d external WebSockets from one IP without rejection; want a bounded connection count", len(connections))
	}
}

func TestWSConnectionLimiterAdmissionAndRelease(t *testing.T) {
	limiter := newWSConnectionLimiter(2, 3)

	releaseA1, ok := limiter.acquire("203.0.113.1")
	if !ok {
		t.Fatal("first connection was rejected")
	}
	releaseA2, ok := limiter.acquire("203.0.113.1")
	if !ok {
		t.Fatal("second connection was rejected")
	}
	if _, ok := limiter.acquire("203.0.113.1"); ok {
		t.Fatal("third connection from one IP was admitted past the per-IP cap")
	}
	releaseB, ok := limiter.acquire("203.0.113.2")
	if !ok {
		t.Fatal("connection from second IP was rejected below the global cap")
	}
	if _, ok := limiter.acquire("203.0.113.3"); ok {
		t.Fatal("connection was admitted past the global cap")
	}

	releaseA1()
	releaseA1() // Release must be idempotent across competing cleanup paths.
	releaseC, ok := limiter.acquire("203.0.113.3")
	if !ok {
		t.Fatal("connection was rejected after capacity was released")
	}
	releaseA2()
	releaseB()
	releaseC()
}

func TestNormalizeWSClientIP(t *testing.T) {
	for input, want := range map[string]string{
		"203.0.113.7:49152": "203.0.113.7",
		"[2001:db8::7]:443": "2001:db8::7",
		"198.51.100.8":      "198.51.100.8",
		"unknown-client":    "unknown-client",
	} {
		if got := normalizeWSClientIP(input); got != want {
			t.Errorf("normalizeWSClientIP(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSessionWebSocketRejectsWhenAdmissionIsFull(t *testing.T) {
	srv := &Server{
		wsSecurityConfig:    middleware.DefaultWebSocketSecurityConfig(),
		wsConnectionLimiter: newWSConnectionLimiter(0, 0),
	}
	mux := http.NewServeMux()
	mux.Handle("/api/sessions/{id}/ws", ExternalConnectionMiddleware(http.HandlerFunc(srv.handleSessionWS)))

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/20260101-120000-deadbeef/ws", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, req)

	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusTooManyRequests)
	}
	if response.Header().Get("Retry-After") == "" {
		t.Error("HTTP 429 WebSocket rejection is missing Retry-After")
	}
}

func TestGlobalEventsWebSocketFailedUpgradeReleasesAdmission(t *testing.T) {
	limiter := newWSConnectionLimiter(1, 1)
	srv := &Server{
		eventsManager:       NewGlobalEventsManager(),
		wsSecurityConfig:    middleware.DefaultWebSocketSecurityConfig(),
		wsConnectionLimiter: limiter,
	}
	handler := ExternalConnectionMiddleware(http.HandlerFunc(srv.handleGlobalEventsWS))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/events", nil))
	if response.Code == http.StatusTooManyRequests {
		t.Fatal("first upgrade attempt was unexpectedly admission-limited")
	}

	release, ok := limiter.acquire("capacity-check")
	if !ok {
		t.Fatal("failed WebSocket upgrade leaked its admission token")
	}
	release()
}
