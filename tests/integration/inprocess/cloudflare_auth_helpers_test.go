//go:build integration

package inprocess

import (
	"encoding/pem"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
	"github.com/inercia/mitto/internal/web"
)

// CloudflareTestServer extends TestServer with Cloudflare Access test data.
type CloudflareTestServer struct {
	*TestServer
	CloudflareJWT    string // Valid JWT for authentication
	ExpiredJWT       string // JWT with expired timestamp
	WrongAudienceJWT string // JWT with wrong audience
}

// SetupTestServerWithCloudflareAuth creates a test server with both Cloudflare
// Access and simple auth configured.
func SetupTestServerWithCloudflareAuth(t *testing.T) *CloudflareTestServer {
	t.Helper()
	return setupCloudflareTestServer(t, true)
}

// SetupTestServerWithCloudflareOnlyAuth creates a test server with only
// Cloudflare Access auth (no simple auth).
func SetupTestServerWithCloudflareOnlyAuth(t *testing.T) *CloudflareTestServer {
	t.Helper()
	return setupCloudflareTestServer(t, false)
}

func setupCloudflareTestServer(t *testing.T, includeSimpleAuth bool) *CloudflareTestServer {
	t.Helper()

	audience := "test-audience-12345678"
	mock := newMockJWKSServer(t, audience)

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

	// Write CA cert for CACertFile config
	caCertFile := filepath.Join(tmpDir, "ca.pem")
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: mock.caCertDER})
	os.WriteFile(caCertFile, caPEM, 0644)

	authCfg := &config.WebAuth{
		Cloudflare: &config.CloudflareAuth{
			TeamDomain: mock.issuer[len("https://"):],
			Audience:   audience,
			CACertFile: caCertFile,
		},
	}
	if includeSimpleAuth {
		authCfg.Simple = &config.SimpleAuth{Username: "testuser", Password: "testpass"}
	}

	mittoConfig := &config.Config{
		ACPServers: []config.ACPServer{{Name: "mock-acp", Command: mockACPCmd}},
		Web: config.WebConfig{
			Auth: authCfg,
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

	// Wrap with externalConnectionMiddleware to simulate the external listener.
	// This marks all connections as "external", which requires auth even from localhost.
	externalHandler := web.ExternalConnectionMiddleware(srv.Handler())
	httpServer := httptest.NewServer(externalHandler)
	t.Cleanup(httpServer.Close)

	return &CloudflareTestServer{
		TestServer: &TestServer{
			Server: srv, HTTPServer: httpServer, Store: store,
			TempDir: tmpDir, MockACPCmd: mockACPCmd,
		},
		CloudflareJWT:    mock.signJWT(t, audience, "testuser@example.com", 1*time.Hour),
		ExpiredJWT:       mock.signJWT(t, audience, "expired@example.com", -1*time.Hour),
		WrongAudienceJWT: mock.signJWT(t, "wrong-audience", "wrong@example.com", 1*time.Hour),
	}
}
