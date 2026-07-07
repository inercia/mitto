package mcpserver

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/inercia/mitto/internal/session"
)

// TestFastPath_InboundInitAndToolsListStayBoundedUnderLoad is the regression
// guard for mitto-54k.2. Mitto's own inbound /mcp `initialize` + `tools/list`
// are served entirely by the SDK's `mcp.NewStreamableHTTPHandler` — the
// factory (server.go ~340) is a pure passthrough returning `s.mcpServer`, and
// Mitto's own tools are registered statically at startup via `mcp.AddTool`.
// Neither `s.mu` nor `s.sessionsMu` nor any blocking helper
// (`WaitForPendingRequest`, correlation waits) is touched on that path.
//
// This test proves that property empirically: while the server is under
// concurrent MCP-client load (many parallel initialize+tools/list round-trips
// mimicking a resume-storm's pressure on goroutines and Go's scheduler), each
// individual initialize+tools/list round-trip still completes well under a
// generous bound, and the returned tool set is non-empty and stable across
// concurrent callers.
func TestFastPath_InboundInitAndToolsListStayBoundedUnderLoad(t *testing.T) {
	// Standard test-server construction, mirrors TestServerStartStop.
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	srv, err := NewServer(
		Config{Port: 0}, // random port
		Dependencies{Store: store},
	)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })

	port := srv.Port()
	if port == 0 {
		t.Fatal("Port not assigned after Start()")
	}
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/mcp", port)

	// Warm-up: one baseline initialize+tools/list to capture the expected
	// tool set (consistency check target) and confirm the endpoint is up.
	baseline := doInitAndListTools(t, ctx, endpoint, 5*time.Second)
	if len(baseline) == 0 {
		t.Fatalf("baseline tools/list returned no tools; expected the statically-registered Mitto tools")
	}
	baselineSet := make(map[string]struct{}, len(baseline))
	for _, name := range baseline {
		baselineSet[name] = struct{}{}
	}

	// Concurrent load: N goroutines each perform their own initialize +
	// tools/list round-trip. This exercises the SDK handler under real
	// concurrent HTTP contention (each round-trip is a fresh MCP session,
	// so the server spins up per-session state, sends "initialized"
	// notifications, etc.). If any Mitto lock were held across the SDK
	// dispatch on this path, we'd see the tail latency balloon.
	const (
		concurrency   = 32
		perCallBudget = 2 * time.Second
	)

	var (
		wg          sync.WaitGroup
		maxDuration atomic.Int64 // nanoseconds
		failures    atomic.Int32
	)

	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func(i int) {
			defer wg.Done()

			callCtx, callCancel := context.WithTimeout(ctx, perCallBudget)
			defer callCancel()

			start := time.Now()
			tools := doInitAndListTools(t, callCtx, endpoint, perCallBudget)
			elapsed := time.Since(start)

			// Track high-water mark.
			for {
				prev := maxDuration.Load()
				if int64(elapsed) <= prev || maxDuration.CompareAndSwap(prev, int64(elapsed)) {
					break
				}
			}

			if len(tools) == 0 {
				failures.Add(1)
				t.Errorf("worker %d: tools/list returned no tools under load", i)
				return
			}
			// Consistency: every returned tool must be in the baseline set,
			// and every baseline tool must be present here. Mitto's own /mcp
			// server registers a static tool set; concurrent load must not
			// return a partial/warm-up view.
			seen := make(map[string]struct{}, len(tools))
			for _, name := range tools {
				seen[name] = struct{}{}
				if _, ok := baselineSet[name]; !ok {
					failures.Add(1)
					t.Errorf("worker %d: tool %q returned under load but absent from baseline set", i, name)
				}
			}
			for name := range baselineSet {
				if _, ok := seen[name]; !ok {
					failures.Add(1)
					t.Errorf("worker %d: baseline tool %q missing from under-load response", i, name)
				}
			}
		}(i)
	}

	wg.Wait()

	if got := failures.Load(); got > 0 {
		t.Fatalf("%d workers reported failures under load", got)
	}

	maxObserved := time.Duration(maxDuration.Load())
	if maxObserved >= perCallBudget {
		t.Fatalf("max observed initialize+tools/list latency = %v, want < %v (per-call budget)", maxObserved, perCallBudget)
	}
	t.Logf("initialize+tools/list under %d-way concurrency: max round-trip = %v (budget %v), tools = %d",
		concurrency, maxObserved, perCallBudget, len(baseline))
}

// doInitAndListTools opens a fresh MCP client session to endpoint via the
// Streamable HTTP transport, calls tools/list, and returns the tool names.
// Any failure (build/connect/list/timeout) fails the calling test via t.Fatalf.
// The SDK's client.Connect performs the JSON-RPC `initialize` handshake, so
// this covers both methods on the fast-path.
func doInitAndListTools(t *testing.T, ctx context.Context, endpoint string, timeout time.Duration) []string {
	t.Helper()

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	transport := &mcp.StreamableClientTransport{Endpoint: endpoint}
	client := mcp.NewClient(&mcp.Implementation{Name: "mitto-fastpath-test", Version: "1.0.0"}, nil)

	sess, err := client.Connect(callCtx, transport, nil)
	if err != nil {
		t.Fatalf("mcp client Connect (initialize) failed: %v", err)
	}
	defer sess.Close()

	res, err := sess.ListTools(callCtx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("mcp client ListTools failed: %v", err)
	}

	names := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	return names
}
