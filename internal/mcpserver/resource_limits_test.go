package mcpserver

import (
	"context"
	"testing"

	"github.com/inercia/mitto/internal/session"
)

// TestMCPHTTPServerConfiguresResourceLimits reproduces mitto-syqh. Request and
// idle handling must be bounded without a WriteTimeout that would kill active
// Streamable HTTP SSE responses or long-running tools.
func TestMCPHTTPServerConfiguresResourceLimits(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("session.NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	srv, err := NewServer(Config{Port: 0}, Dependencies{Store: store})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })

	httpSrv := srv.httpSrv
	if httpSrv == nil {
		t.Fatal("HTTP transport started without an http.Server")
	}
	if httpSrv.ReadHeaderTimeout <= 0 {
		t.Errorf("ReadHeaderTimeout = %v, want a positive Slowloris deadline", httpSrv.ReadHeaderTimeout)
	}
	if httpSrv.ReadTimeout <= 0 {
		t.Errorf("ReadTimeout = %v, want bounded request ingestion", httpSrv.ReadTimeout)
	}
	if httpSrv.IdleTimeout <= 0 {
		t.Errorf("IdleTimeout = %v, want bounded idle keep-alive connections", httpSrv.IdleTimeout)
	}
	if httpSrv.MaxHeaderBytes <= 0 {
		t.Errorf("MaxHeaderBytes = %d, want an explicit positive limit", httpSrv.MaxHeaderBytes)
	}
	if httpSrv.WriteTimeout != 0 {
		t.Errorf("WriteTimeout = %v, want 0 so active SSE and long-running tools remain valid", httpSrv.WriteTimeout)
	}
}
