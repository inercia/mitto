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
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/instancefile"
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

// rotationTestServer bundles a web.Server with BOTH a loopback-only
// httptest.Server (no ExternalConnectionMiddleware, like SetupTestServer)
// and one wrapped with ExternalConnectionMiddleware — sharing the same
// underlying AuthManager, so a rotation issued through one is immediately
// observable through the other.
type rotationTestServer struct {
	Server      *web.Server
	LoopbackURL string
	ExternalURL string
}

// setupSharedTokenRotationTestServer creates a server with Simple auth and a
// shared token adopted "from instance.json" (mitto-pscc.9): it writes
// instance.json with token BEFORE constructing the server and sets
// Config.SharedTokenFromInstanceFile, mirroring what internal/cmd/web.go and
// cmd/mitto-app/main.go do at real startup — required for POST
// /api/auth/rotate-token to treat the token as rotatable.
func setupSharedTokenRotationTestServer(t *testing.T, token string) *rotationTestServer {
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

	if err := instancefile.Write(&instancefile.Instance{
		PID: os.Getpid(), URL: "http://127.0.0.1:0", APIPrefix: "/mitto", Token: token,
	}); err != nil {
		t.Fatalf("write instance file: %v", err)
	}

	mittoConfig := &config.Config{
		ACPServers: []config.ACPServer{{Name: "mock-acp", Command: mockACPCmd}},
		Web: config.WebConfig{
			Auth: &config.WebAuth{
				Simple:      &config.SimpleAuth{Username: "admin", Password: "password"},
				SharedToken: token,
			},
		},
	}
	webConfig := web.Config{
		Workspaces:                  []config.WorkspaceSettings{{ACPServer: "mock-acp", WorkingDir: workspaceDir}},
		ACPCommand:                  mockACPCmd,
		ACPServer:                   "mock-acp",
		DefaultWorkingDir:           workspaceDir,
		AutoApprove:                 true,
		Debug:                       true,
		FromCLI:                     true,
		MittoConfig:                 mittoConfig,
		DisableAuxiliaryPrewarm:     true,
		SharedTokenFromInstanceFile: true,
	}

	srv, err := web.NewServer(webConfig)
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	t.Cleanup(func() { _ = srv.Shutdown() })

	loopback := httptest.NewServer(srv.Handler())
	t.Cleanup(loopback.Close)

	external := httptest.NewServer(web.ExternalConnectionMiddleware(srv.Handler()))
	t.Cleanup(external.Close)

	return &rotationTestServer{Server: srv, LoopbackURL: loopback.URL, ExternalURL: external.URL}
}

// TestAuthRotate_RejectedOnExternalListener verifies POST
// /api/auth/rotate-token is refused (403) when it arrives through the
// external listener, even though rotation works without any credential on
// the loopback listener — the endpoint must never be reachable remotely.
func TestAuthRotate_RejectedOnExternalListener(t *testing.T) {
	rs := setupSharedTokenRotationTestServer(t, "rotate-me-token")

	req, _ := http.NewRequest(http.MethodPost, rs.ExternalURL+"/mitto/api/auth/rotate-token", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST rotate-token: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
}

// TestAuthRotate_AcceptedOnLoopback_RotatesLiveToken verifies rotation over
// the loopback listener succeeds, the previously valid bearer token is then
// rejected on the external listener, the newly rewritten instance.json's
// token is accepted instead, and the response never carries the raw token
// (only a fingerprint).
func TestAuthRotate_AcceptedOnLoopback_RotatesLiveToken(t *testing.T) {
	const oldToken = "rotate-me-old-token"
	rs := setupSharedTokenRotationTestServer(t, oldToken)

	// Old token works before rotation.
	if !bearerAccepted(t, rs.ExternalURL, oldToken) {
		t.Fatal("old token should be accepted before rotation")
	}

	req, _ := http.NewRequest(http.MethodPost, rs.LoopbackURL+"/mitto/api/auth/rotate-token", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST rotate-token (loopback): %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var body struct {
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode rotate response: %v", err)
	}
	if body.Fingerprint == "" {
		t.Fatal("expected a non-empty fingerprint in the rotate response")
	}
	if strings.Contains(body.Fingerprint, oldToken) {
		t.Fatalf("rotate response leaked the old token: %q", body.Fingerprint)
	}

	// Old token now rejected.
	if bearerAccepted(t, rs.ExternalURL, oldToken) {
		t.Error("old token should be rejected after rotation")
	}

	// The rewritten instance.json carries the new token, which is accepted.
	inst, err := instancefile.Read()
	if err != nil {
		t.Fatalf("read instance file after rotation: %v", err)
	}
	if inst.Token == oldToken {
		t.Fatal("instance file token was not rewritten by rotation")
	}
	if instancefile.Fingerprint(inst.Token) != body.Fingerprint {
		t.Errorf("instance file token fingerprint = %q, want %q (from rotate response)", instancefile.Fingerprint(inst.Token), body.Fingerprint)
	}
	if !bearerAccepted(t, rs.ExternalURL, inst.Token) {
		t.Error("new token from the rewritten instance file should be accepted")
	}
}

// TestAuthRotate_RefusedForOperatorConfiguredToken verifies rotation refuses
// (409) when the shared token was NOT adopted from instance.json (i.e. it
// came from MITTO_SHARED_TOKEN/settings.json/keychain) — rotating an
// operator-managed secret through this endpoint is out of scope.
func TestAuthRotate_RefusedForOperatorConfiguredToken(t *testing.T) {
	// setupSharedTokenTestServer does not set SharedTokenFromInstanceFile,
	// simulating an operator-configured token.
	ts := setupSharedTokenTestServer(t)

	loopback := httptest.NewServer(ts.Server.Handler())
	defer loopback.Close()

	req, _ := http.NewRequest(http.MethodPost, loopback.URL+"/mitto/api/auth/rotate-token", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST rotate-token (loopback): %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusConflict)
	}
}

// TestSharedTokenAdoption_FromInstanceFile verifies the mitto-pscc.9
// adoption gap: a server configured with Simple auth but NO explicit shared
// token (SharedToken left empty, mirroring the default install where the
// operator set nothing) still accepts the value resolved via
// instancefile.ResolveToken once that value is written to instance.json and
// wired into cfg.Web.Auth.SharedToken + SharedTokenFromInstanceFile before
// construction — exactly the sequence internal/cmd/web.go and
// cmd/mitto-app/main.go perform at real startup.
func TestSharedTokenAdoption_FromInstanceFile(t *testing.T) {
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

	// Adoption step: resolve the token instance.json will carry, BEFORE the
	// server (and its AuthManager) is constructed.
	resolvedToken, err := instancefile.ResolveToken()
	if err != nil {
		t.Fatalf("ResolveToken: %v", err)
	}
	if err := instancefile.Write(&instancefile.Instance{
		PID: os.Getpid(), URL: "http://127.0.0.1:0", APIPrefix: "/mitto", Token: resolvedToken,
	}); err != nil {
		t.Fatalf("write instance file: %v", err)
	}

	mittoConfig := &config.Config{
		ACPServers: []config.ACPServer{{Name: "mock-acp", Command: mockACPCmd}},
		Web: config.WebConfig{
			Auth: &config.WebAuth{
				Simple: &config.SimpleAuth{Username: "admin", Password: "password"},
				// SharedToken intentionally left empty by the operator; the
				// adopted resolvedToken is filled in below, mirroring
				// internal/cmd/web.go's adoption branch.
				SharedToken: resolvedToken,
			},
		},
	}
	webConfig := web.Config{
		Workspaces:                  []config.WorkspaceSettings{{ACPServer: "mock-acp", WorkingDir: workspaceDir}},
		ACPCommand:                  mockACPCmd,
		ACPServer:                   "mock-acp",
		DefaultWorkingDir:           workspaceDir,
		AutoApprove:                 true,
		Debug:                       true,
		FromCLI:                     true,
		MittoConfig:                 mittoConfig,
		DisableAuxiliaryPrewarm:     true,
		SharedTokenFromInstanceFile: true,
	}

	srv, err := web.NewServer(webConfig)
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	t.Cleanup(func() { _ = srv.Shutdown() })

	external := httptest.NewServer(web.ExternalConnectionMiddleware(srv.Handler()))
	t.Cleanup(external.Close)

	if !bearerAccepted(t, external.URL, resolvedToken) {
		t.Error("the instance.json-resolved token should be accepted even though the operator configured no token explicitly")
	}
	if bearerAccepted(t, external.URL, "totally-wrong-token") {
		t.Error("an unrelated token must still be rejected")
	}
}

// bearerAccepted issues a GET /api/sessions against baseURL with the given
// bearer token and reports whether the server accepted it (200), as opposed
// to rejecting it (401).
func bearerAccepted(t *testing.T, baseURL, token string) bool {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, baseURL+"/mitto/api/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/sessions: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
