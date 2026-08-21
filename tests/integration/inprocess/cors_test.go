//go:build integration

package inprocess

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
	"github.com/inercia/mitto/internal/web"
)

// setupCORSTestServer creates a test server behind the external listener
// (so auth and CSRF are actually enforced) with a shared bearer token and a
// CORS origin allowlist configured (mitto-7gta.27).
func setupCORSTestServer(t *testing.T) *TestServer {
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
			Security: &config.WebSecurity{
				AllowedOrigins: []string{"https://app.example.com"},
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

	externalHandler := web.ExternalConnectionMiddleware(srv.Handler())
	httpServer := httptest.NewServer(externalHandler)
	t.Cleanup(httpServer.Close)

	return &TestServer{Server: srv, HTTPServer: httpServer, Store: store, TempDir: tmpDir, MockACPCmd: mockACPCmd}
}

// TestCORS_CrossOriginAccess exercises the CORS middleware (mitto-7gta.27)
// through the real middleware chain, where auth and CSRF are enforced.
func TestCORS_CrossOriginAccess(t *testing.T) {
	ts := setupCORSTestServer(t)
	baseURL := ts.HTTPServer.URL + "/mitto"

	t.Run("preflight is answered without auth", func(t *testing.T) {
		// A preflight carries no cookie, no bearer token and no CSRF token.
		// It must terminate in the CORS middleware rather than reach Auth
		// (401/redirect) or CSRF (403).
		req, _ := http.NewRequest(http.MethodOptions, baseURL+"/api/sessions", nil)
		req.Header.Set("Origin", "https://app.example.com")
		req.Header.Set("Access-Control-Request-Method", "POST")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("preflight: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
		}
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
			t.Errorf("Access-Control-Allow-Origin = %q, want the request origin", got)
		}
		if got := resp.Header.Get("Access-Control-Allow-Credentials"); got != "" {
			t.Errorf("Access-Control-Allow-Credentials = %q, want it never to be emitted", got)
		}
	})

	t.Run("cross-origin bearer-token request carries ACAO", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, baseURL+"/api/sessions", nil)
		req.Header.Set("Origin", "https://app.example.com")
		req.Header.Set("Authorization", "Bearer test-shared-token-xyz")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET /api/sessions: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
			t.Errorf("Access-Control-Allow-Origin = %q, want the request origin", got)
		}
		if got := resp.Header.Get("Access-Control-Allow-Credentials"); got != "" {
			t.Errorf("Access-Control-Allow-Credentials = %q, want it never to be emitted", got)
		}
	})

	t.Run("disallowed origin still authenticates but gets no ACAO", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, baseURL+"/api/sessions", nil)
		req.Header.Set("Origin", "https://evil.example.com")
		req.Header.Set("Authorization", "Bearer test-shared-token-xyz")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET /api/sessions: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d (a disallowed Origin must not reject the request)", resp.StatusCode, http.StatusOK)
		}
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("Access-Control-Allow-Origin = %q, want empty for a disallowed origin", got)
		}
	})
}
