//go:build integration

package inprocess

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
	"github.com/inercia/mitto/internal/web"
)

// setupSharedTokenTestServer creates a test server behind the external
// listener (mitto-7gta.26) with both Simple auth (so IsEnabled() is true) and
// a shared bearer token configured as an additional credential.
func setupSharedTokenTestServer(t *testing.T) *TestServer {
	t.Helper()

	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	mockACPCmd := findMockACPServer(t)

	store, err := session.NewStore(filepath.Join(tmpDir, "sessions"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	workspaceDir := filepath.Join(tmpDir, "workspace")
	os.MkdirAll(workspaceDir, 0755)

	mittoConfig := &config.Config{
		ACPServers: []config.ACPServer{{Name: "mock-acp", Command: mockACPCmd}},
		Web: config.WebConfig{
			Auth: &config.WebAuth{
				Simple:      &config.SimpleAuth{Username: "admin", Password: "password"},
				SharedToken: "test-shared-token-xyz",
			},
		},
	}
	webConfig := web.Config{
		Workspaces:              []config.WorkspaceSettings{{ACPServer: "mock-acp", WorkingDir: workspaceDir}},
		ACPCommand:              mockACPCmd,
		ACPServer:               "mock-acp",
		DefaultWorkingDir:       workspaceDir,
		AutoApprove:             true,
		Debug:                   true,
		FromCLI:                 true,
		MittoConfig:             mittoConfig,
		DisableAuxiliaryPrewarm: true,
	}

	srv, err := web.NewServer(webConfig)
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	t.Cleanup(func() { _ = srv.Shutdown() })

	// Simulate the external listener: auth + CSRF are only enforced there.
	externalHandler := web.ExternalConnectionMiddleware(srv.Handler())
	httpServer := httptest.NewServer(externalHandler)
	t.Cleanup(httpServer.Close)

	return &TestServer{Server: srv, HTTPServer: httpServer, Store: store, TempDir: tmpDir, MockACPCmd: mockACPCmd}
}

// TestSharedTokenAuth_RESTAndWebSocket exercises the shared bearer token
// (mitto-7gta.26) end-to-end against the real HTTP+WebSocket stack: REST
// auth, the CSRF bypass for a state-changing request, and the WS upgrade.
func TestSharedTokenAuth_RESTAndWebSocket(t *testing.T) {
	ts := setupSharedTokenTestServer(t)
	baseURL := ts.HTTPServer.URL + "/mitto"

	t.Run("no auth rejected", func(t *testing.T) {
		resp, err := http.Get(baseURL + "/api/sessions")
		if err != nil {
			t.Fatalf("GET /api/sessions: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
		}
	})

	t.Run("invalid bearer token rejected", func(t *testing.T) {
		req, _ := http.NewRequest("GET", baseURL+"/api/sessions", nil)
		req.Header.Set("Authorization", "Bearer wrong-token")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET /api/sessions: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
		}
	})

	t.Run("valid bearer token allows state-changing request without CSRF", func(t *testing.T) {
		// POST is a state-changing method: without the bearer-token CSRF
		// exemption this would be rejected (403) for lacking a double-submit
		// cookie/header pair.
		body := []byte(`{"working_dir":"` + ts.TempDir + `/workspace"}`)
		req, _ := http.NewRequest("POST", baseURL+"/api/sessions", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer test-shared-token-xyz")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST /api/sessions: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
			t.Fatalf("status = %d, want 200/201", resp.StatusCode)
		}

		var created struct {
			SessionID string `json:"session_id"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if created.SessionID == "" {
			t.Fatal("expected a session_id in the response")
		}

		t.Run("websocket upgrade with valid bearer token succeeds", func(t *testing.T) {
			u, _ := url.Parse(ts.HTTPServer.URL)
			u.Scheme = "ws"
			u.Path = "/mitto/api/sessions/" + created.SessionID + "/ws"

			hdr := http.Header{}
			hdr.Set("Authorization", "Bearer test-shared-token-xyz")

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			conn, resp, err := websocket.DefaultDialer.DialContext(ctx, u.String(), hdr)
			if err != nil {
				t.Fatalf("websocket dial with valid bearer token failed: %v", err)
			}
			defer conn.Close()
			if resp != nil {
				resp.Body.Close()
			}
		})

		t.Run("websocket upgrade without bearer token is rejected", func(t *testing.T) {
			u, _ := url.Parse(ts.HTTPServer.URL)
			u.Scheme = "ws"
			u.Path = "/mitto/api/sessions/" + created.SessionID + "/ws"

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			conn, _, err := websocket.DefaultDialer.DialContext(ctx, u.String(), nil)
			if err == nil {
				conn.Close()
				t.Fatal("expected websocket dial without auth to fail")
			}
		})
	})
}
